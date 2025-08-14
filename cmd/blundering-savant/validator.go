package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/go-github/v72/github"
)

type WorkflowStatus string

const (
	WorkflowStatusCompleted  WorkflowStatus = "completed"
	WorkflowStatusInProgress WorkflowStatus = "in_progress"
	WorkflowStatusQueued     WorkflowStatus = "queued"
	WorkflowStatusPending    WorkflowStatus = "pending"
	WorkflowStatusRequested  WorkflowStatus = "requested"
	WorkflowStatusWaiting    WorkflowStatus = "waiting"
)

type GithubActionCommitValidator struct {
	githubClient     *github.Client
	owner            string
	repo             string
	workflowFileName string
}

func NewGithubActionCommitValidator(githubClient *github.Client, owner string, repo string, workflowFileName string) GithubActionCommitValidator {
	return GithubActionCommitValidator{
		githubClient:     githubClient,
		owner:            owner,
		repo:             repo,
		workflowFileName: workflowFileName,
	}
}

func (gacv GithubActionCommitValidator) ValidateCommit(ctx context.Context, commitSHA string) (ValidationResult, error) {
	// Find existing run for this commit, if any
	run, err := gacv.findWorkflowRun(ctx, commitSHA)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to find workflow run: %w", err)
	}

	if run == nil {
		// No run found, trigger one
		run, err = gacv.triggerWorkflowRun(ctx, commitSHA)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to trigger workflow: %w", err)
		}
	}

	if run == nil || run.ID == nil {
		return ValidationResult{}, fmt.Errorf("unexpected nil in workflow run")
	}

	conclusion, err := gacv.waitForWorkflowCompletion(ctx, *run.ID)
	if err != nil {
		return ValidationResult{}, err
	}

	return ValidationResult{
		Output: conclusion,
	}, nil
}

// findWorkflowRun returns one workflow run for the given commit on the given branch. If no workflow run exists, returns (nil, nil)
func (gacv GithubActionCommitValidator) findWorkflowRun(ctx context.Context, commitSHA string) (*github.WorkflowRun, error) {
	opts := &github.ListWorkflowRunsOptions{
		HeadSHA:     commitSHA,
		ListOptions: github.ListOptions{PerPage: 1},
	}
	runs, _, err := gacv.githubClient.Actions.ListWorkflowRunsByFileName(ctx, gacv.owner, gacv.repo, gacv.workflowFileName, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list workflow runs: %w", err)
	}
	if runs == nil || runs.TotalCount == nil {
		return nil, fmt.Errorf("unexpected nil")
	}
	if *runs.TotalCount == 0 {
		return nil, nil
	} else if *runs.TotalCount > 1 {
		log.Printf("Warning: multiple workflow runs found, picking one")
	}

	return runs.WorkflowRuns[0], nil
}

func (gacv GithubActionCommitValidator) triggerWorkflowRun(ctx context.Context, commitSHA string) (*github.WorkflowRun, error) {
	req := github.CreateWorkflowDispatchEventRequest{
		Ref: commitSHA,
	}
	_, err := gacv.githubClient.Actions.CreateWorkflowDispatchEventByFileName(ctx, gacv.owner, gacv.repo, gacv.workflowFileName, req)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger workflow run: %w", err)
	}
	return nil, nil
}

func (gacv GithubActionCommitValidator) waitForWorkflowCompletion(ctx context.Context, runID int64) (string, error) {
	pollInterval := 15 * time.Second
	timeout := 45 * time.Minute

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			if parentErr := ctx.Err(); parentErr != nil {
				return "", fmt.Errorf("workflow completion check canceled: %w", parentErr)
			} else if err := timeoutCtx.Err(); err == context.DeadlineExceeded {
				return "", fmt.Errorf("workflow completion check timed out after %v", timeout)
			} else {
				return "", fmt.Errorf("workflow completion check canceled: %w", err)
			}
		case <-ticker.C:
			run, _, err := gacv.githubClient.Actions.GetWorkflowRunByID(ctx, gacv.owner, gacv.repo, runID)
			if err != nil {
				return "", fmt.Errorf("failed to get workflow run: %w", err)
			}

			status := run.GetStatus()
			conclusion := run.GetConclusion()

			log.Printf("Workflow run %d status: %s", runID, status)
			if conclusion != "" {
				log.Printf(" (conclusion: %s)", conclusion)
			}
			log.Println()

			switch WorkflowStatus(status) {
			case WorkflowStatusCompleted:
				return conclusion, nil
			case WorkflowStatusInProgress,
				WorkflowStatusQueued,
				WorkflowStatusPending,
				WorkflowStatusRequested,
				WorkflowStatusWaiting:
				continue
			default:
				return status, fmt.Errorf("unexpected workflow status: %s", status)
			}
		}
	}
}
