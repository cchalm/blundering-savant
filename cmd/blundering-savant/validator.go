package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/workspace"
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

type JobStatus string

const (
	JobStatusCompleted  JobStatus = "completed"
	JobStatusInProgress JobStatus = "in_progress"
	JobStatusQueued     JobStatus = "queued"
	JobStatusPending    JobStatus = "pending"
	JobStatusRequested  JobStatus = "requested"
	JobStatusWaiting    JobStatus = "waiting"
)

type JobConclusion string

const (
	JobConclusionSuccess JobConclusion = "success"
	JobConclusionFailure JobConclusion = "failure"
)

type StepStatus string

const (
	StepStatusCompleted  StepStatus = "completed"
	StepStatusInProgress StepStatus = "in_progress"
	StepStatusQueued     StepStatus = "queued"
	StepStatusPending    StepStatus = "pending"
	StepStatusRequested  StepStatus = "requested"
	StepStatusWaiting    StepStatus = "waiting"
)

type StepConclusion string

const (
	StepConclusionSuccess StepConclusion = "success"
	StepConclusionFailure StepConclusion = "failure"
)

type CheckSuiteConclusion string

const (
	CheckSuiteConclusionSuccess CheckSuiteConclusion = "success"
	CheckSuiteConclusionFailure CheckSuiteConclusion = "failure"
)

type WorkflowRun struct {
	ID         int64
	Status     WorkflowStatus
	Conclusion WorkflowConclusion

	Jobs []WorkflowJob
}

type WorkflowJob struct {
	ID         int64
	Status     JobStatus
	Conclusion JobConclusion

	Steps []WorkflowStep
}

type WorkflowStep struct {
	Number     int64
	Name       string
	Status     StepStatus
	Conclusion StepConclusion

	StartedAt   time.Time
	CompletedAt time.Time

	Logs string
}

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

func (gacv GithubActionCommitValidator) ValidateBranch(ctx context.Context, branch string, commitSHA string) (workspace.ValidationResult, error) {
	log.Printf("Validating branch '%s' with workflow '%s'", branch, gacv.workflowFileName)

	// Find existing run for this commit, if any
	run, err := gacv.findWorkflowRun(ctx, commitSHA)
	if err != nil {
		return workspace.ValidationResult{}, fmt.Errorf("failed to find workflow run: %w", err)
	}

	if run == nil {
		log.Println("No existing workflow run found for branch, triggering a new run")

		// No run found, trigger one
		run, err = gacv.triggerWorkflowRun(ctx, branch, commitSHA)
		if err != nil {
			return workspace.ValidationResult{}, fmt.Errorf("failed to trigger workflow: %w", err)
		}
	} else {
		log.Printf("Found existing workflow run %d (status: '%s', conclusion: '%s')", *run.ID, run.GetStatus(), run.GetConclusion())
	}

	if run == nil || run.ID == nil {
		return workspace.ValidationResult{}, fmt.Errorf("unexpected nil in workflow run")
	}

	run, err = gacv.waitForWorkflowCompletion(ctx, *run.ID)
	if err != nil {
		return workspace.ValidationResult{}, err
	}

	succeeded := run.GetConclusion() == string(WorkflowConclusionSuccess)
	var logs string
	if !succeeded {
		logs, err = gacv.getWorkflowRunLogs(ctx, run)
		if err != nil {
			return workspace.ValidationResult{}, fmt.Errorf("failed to get workflow run logs: %w", err)
		}
	}

	return workspace.ValidationResult{
		Succeeded: succeeded,
		Details:   logs,
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
	pollInterval := 2 * time.Second
	timeout := 10 * time.Second

	log.Printf("Waiting up to %v for workflow run to be created", timeout)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		run, err := gacv.findWorkflowRun(timeoutCtx, headSHA)
		if err != nil {
			return nil, fmt.Errorf("error while searching for started workflow run: %w", err)
		}
		if run != nil && run.Status != nil {
			log.Printf("Workflow run %d created (status: '%s')", *run.ID, run.GetStatus())
			return run, nil
		}

		log.Printf("Checking again in %v...", pollInterval)

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

	log.Printf("Waiting up to %v for workflow run to be completed", timeout)

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

		switch WorkflowStatus(status) {
		case WorkflowStatusCompleted:
			log.Printf("Workflow run %d completed (status: '%s', conclusion: '%s')", *run.ID, status, run.GetConclusion())
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

		log.Printf("Status '%s'. Checking again in %v...", status, pollInterval)

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

// getWorkflowRunDetails fetches and parses relevant information about a workflow run
func (gacv GithubActionCommitValidator) getWorkflowRunLogs(ctx context.Context, run *github.WorkflowRun) (string, error) {
	jobsResult, _, err := gacv.githubClient.Actions.ListWorkflowJobs(ctx, gacv.owner, gacv.repo, *run.ID, &github.ListWorkflowJobsOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list workflow jobs: %w", err)
	}

	logsBuilder := strings.Builder{}
	for _, job := range jobsResult.Jobs {
		logs, err := gacv.fetchWorkflowJobLogs(ctx, *job.ID)
		if err != nil {
			return "", fmt.Errorf("failed to fetch workflow job logs: %w", err)
		}

		logsBuilder.WriteString(fmt.Sprintf("Job %d (%s) logs:\n%s\n\n", *job.ID, job.GetName(), logs))
	}

	return logsBuilder.String(), nil
}

func (gacv GithubActionCommitValidator) fetchWorkflowJobLogs(ctx context.Context, jobID int64) (string, error) {
	maxRedirects := 10
	logsURL, _, err := gacv.githubClient.Actions.GetWorkflowJobLogs(ctx, gacv.owner, gacv.repo, jobID, maxRedirects)
	if err != nil {
		return "", fmt.Errorf("failed to get workflow run logs URL: %w", err)
	}

	if logsURL == nil {
		return "", fmt.Errorf("workflow run logs URL is nil")
	}

	return httpFetchUTF8(ctx, logsURL)
}

func httpFetchUTF8(ctx context.Context, url *url.URL) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL '%s': %w", url.String(), err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	// Strip the BOM, if present, before reading
	br := bufio.NewReader(httpResp.Body)
	r, _, err := br.ReadRune()
	if err != nil {
		return "", fmt.Errorf("failed to read rune from response: %w", err)
	}
	if r != '\uFEFF' {
		err := br.UnreadRune() // Not a BOM -- put the rune back
		if err != nil {
			return "", fmt.Errorf("failed to UN-read rune from response: %w", err)
		}
	}

	b, err := io.ReadAll(br)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(b), nil
}
