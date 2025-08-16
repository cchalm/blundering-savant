package main

import (
	"context"
	"fmt"
	"log"
	"strings"
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

type WorkflowConclusion string

const (
	WorkflowConclusionSuccess WorkflowConclusion = "success"
	WorkflowConclusionFailure WorkflowConclusion = "failure"
)

type JobConclusion string

const (
	JobConclusionSuccess JobConclusion = "success"
	JobConclusionFailure JobConclusion = "failure"
)

type CheckSuiteConclusion string

const (
	CheckSuiteConclusionSuccess CheckSuiteConclusion = "success"
	CheckSuiteConclusionFailure CheckSuiteConclusion = "failure"
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

func (gacv GithubActionCommitValidator) ValidateBranch(ctx context.Context, branch string) (ValidationResult, error) {
	// Get the head SHA for the branch
	maxRedirects := 10
	branchInfo, _, err := gacv.githubClient.Repositories.GetBranch(ctx, gacv.owner, gacv.repo, branch, maxRedirects)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to get branch info: %w", err)
	}
	if branchInfo == nil || branchInfo.Commit == nil || branchInfo.Commit.SHA == nil {
		return ValidationResult{}, fmt.Errorf("branch '%s' does not have a valid commit", branch)
	}
	headSHA := *branchInfo.Commit.SHA

	// Find existing run for this commit, if any
	run, err := gacv.findWorkflowRun(ctx, headSHA)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to find workflow run: %w", err)
	}

	if run == nil {
		// No run found, trigger one
		run, err = gacv.triggerWorkflowRun(ctx, branch, headSHA)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to trigger workflow: %w", err)
		}
	}

	if run == nil || run.ID == nil {
		return ValidationResult{}, fmt.Errorf("unexpected nil in workflow run")
	}

	run, err = gacv.waitForWorkflowCompletion(ctx, *run.ID)
	if err != nil {
		return ValidationResult{}, err
	}

	succeeded := run.GetConclusion() == string(WorkflowConclusionSuccess)
	var details string
	if !succeeded {
		failureDetails, err := gacv.getWorkflowFailureDetails(ctx, run)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to get workflow failure details: %w", err)
		}
		details = failureDetails
	}

	return ValidationResult{
		Succeeded: succeeded,
		Details:   details,
	}, nil
}

// findWorkflowRun returns one workflow run for the given commit. If no workflow run exists, returns (nil, nil)
func (gacv GithubActionCommitValidator) findWorkflowRun(ctx context.Context, commitSHA string) (*github.WorkflowRun, error) {
	opts := &github.ListWorkflowRunsOptions{
		HeadSHA:     commitSHA,
		ListOptions: github.ListOptions{PerPage: 10},
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

	// Pick the least recent run, since it's the most likely to be done already
	return runs.WorkflowRuns[len(runs.WorkflowRuns)-1], nil
}

// triggerWorkflowRun triggers a workflow run for the given branch. A run will start on the head of the branch, which is
// expected to have the given SHA. If the given head SHA is not the latest commit on the branch (or if it was but a race
// condition occurs with a new commit), then this function will time out and return an error
func (gacv GithubActionCommitValidator) triggerWorkflowRun(ctx context.Context, branch string, headSHA string) (*github.WorkflowRun, error) {
	req := github.CreateWorkflowDispatchEventRequest{
		Ref: branch,
	}
	_, err := gacv.githubClient.Actions.CreateWorkflowDispatchEventByFileName(ctx, gacv.owner, gacv.repo, gacv.workflowFileName, req)
	if err != nil {
		return nil, fmt.Errorf("failed to trigger workflow run: %w", err)
	}

	run, err := gacv.waitForWorkflowStart(ctx, headSHA)
	if err != nil {
		return nil, err
	}

	return run, nil
}

func (gacv GithubActionCommitValidator) waitForWorkflowStart(ctx context.Context, headSHA string) (*github.WorkflowRun, error) {
	timeout := 10 * time.Second
	log.Printf("Waiting up to %v for workflow run to be created\n", timeout)

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		run, err := gacv.findWorkflowRun(timeoutCtx, headSHA)
		if err != nil {
			return nil, fmt.Errorf("error while searching for started workflow run: %w", err)
		}
		if run != nil && run.Status != nil && *run.Status == "in_progress" {
			return run, nil
		}

		select {
		case <-timeoutCtx.Done():
			if parentErr := ctx.Err(); parentErr != nil {
				return nil, fmt.Errorf("workflow start check canceled: %w", parentErr)
			} else if err := timeoutCtx.Err(); err == context.DeadlineExceeded {
				return nil, fmt.Errorf("workflow start check timed out after %v", timeout)
			} else {
				return nil, fmt.Errorf("workflow start check canceled: %w", err)
			}
		case <-ticker.C:
			continue
		}
	}
}

func (gacv GithubActionCommitValidator) waitForWorkflowCompletion(ctx context.Context, runID int64) (*github.WorkflowRun, error) {
	pollInterval := 15 * time.Second
	timeout := 45 * time.Minute

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		run, _, err := gacv.githubClient.Actions.GetWorkflowRunByID(timeoutCtx, gacv.owner, gacv.repo, runID)
		if err != nil {
			return nil, fmt.Errorf("failed to get workflow run: %w", err)
		}

		status := run.GetStatus()
		conclusion := run.GetConclusion()

		log.Printf("Found workflow run %d (status: %s, conclusion: %s)\n", runID, status, conclusion)

		switch WorkflowStatus(status) {
		case WorkflowStatusCompleted:
			return run, nil
		case WorkflowStatusInProgress,
			WorkflowStatusQueued,
			WorkflowStatusPending,
			WorkflowStatusRequested,
			WorkflowStatusWaiting:
			// Do nothing, continue polling
		default:
			return nil, fmt.Errorf("unexpected workflow status: %s", status)
		}

		select {
		case <-timeoutCtx.Done():
			if parentErr := ctx.Err(); parentErr != nil {
				return nil, fmt.Errorf("workflow completion check canceled: %w", parentErr)
			} else if err := timeoutCtx.Err(); err == context.DeadlineExceeded {
				return nil, fmt.Errorf("workflow completion check timed out after %v", timeout)
			} else {
				return nil, fmt.Errorf("workflow completion check canceled: %w", err)
			}
		case <-ticker.C:
			continue
		}
	}
}

// getWorkflowFailureDetails fetches detailed information about why a workflow failed
func (gacv GithubActionCommitValidator) getWorkflowFailureDetails(ctx context.Context, run *github.WorkflowRun) (string, error) {
	if run.CheckSuiteID == nil {
		return "", fmt.Errorf("workflow run does not have a check suite ID")
	}

	checkRuns, _, err := gacv.githubClient.Checks.ListCheckRunsCheckSuite(ctx, gacv.owner, gacv.repo, *run.CheckSuiteID, &github.ListCheckRunsOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list check runs: %w", err)
	}

	detailsBuilder := strings.Builder{}
	for _, checkRun := range checkRuns.CheckRuns {
		annotations, _, err := gacv.githubClient.Checks.ListCheckRunAnnotations(ctx, gacv.owner, gacv.repo, *checkRun.ID, &github.ListOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to list check run annotations: %w", err)
		}
		for _, annotation := range annotations {
			if annotation.GetPath() == ".github" {
				detailsBuilder.WriteString(fmt.Sprintf(" - [%s] %s\n",
					annotation.GetAnnotationLevel(),
					annotation.GetMessage(),
				))
			} else {
				detailsBuilder.WriteString(fmt.Sprintf(" - [%s] %s#L%d: %s\n",
					annotation.GetAnnotationLevel(),
					annotation.GetPath(),
					annotation.GetStartLine(),
					annotation.GetMessage(),
				))
			}
		}
	}

	return detailsBuilder.String(), nil
}
