package workspace

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/filesystem"
	"github.com/cchalm/blundering-savant/internal/git"
	githubpkg "github.com/cchalm/blundering-savant/internal/github"
	"github.com/cchalm/blundering-savant/internal/task"
)

// remoteValidationWorkspaceFactory creates instances of remoteValidationWorkspace
type remoteValidationWorkspaceFactory struct {
	githubClient           *github.Client
	validationWorkflowName string
}

// NewRemoteValidationWorkspaceFactory creates a new workspace factory for remote validation
func NewRemoteValidationWorkspaceFactory(githubClient *github.Client, validationWorkflowName string) WorkspaceFactory {
	return &remoteValidationWorkspaceFactory{
		githubClient:           githubClient,
		validationWorkflowName: validationWorkflowName,
	}
}

func (rvwf *remoteValidationWorkspaceFactory) NewWorkspace(ctx context.Context, tsk task.Task) (Workspace, error) {
	return NewRemoteValidationWorkspace(ctx, rvwf.githubClient, rvwf.validationWorkflowName, tsk)
}

// remoteValidationWorkspace is a workspace that tracks working changes in-memory until they need to be validated. For
// validation, changes are committed to a "work branch" and pushed to GitHub, where GitHub Actions run validation
// workflows. For publishing, the changes are merged from the "work branch" into a "review branch", from which a PR to
// the default branch has been/will be created. This workflow is designed to reduce noise on PRs while the bot is
// iterating on solutions
type remoteValidationWorkspace struct {
	gitRepo      git.GitRepo
	fs           *filesystem.MemDiffFileSystem
	prService    githubpkg.PullRequestService
	githubClient *github.Client

	owner        string
	repo         string
	issueNumber  int
	needsPullRequest bool

	baseBranch   string
	workBranch   string
	reviewBranch string

	validator BranchValidator
}

// BranchValidator validates changes on a branch
type BranchValidator interface {
	ValidateBranch(ctx context.Context, owner, repo, branch string) error
}

// NewRemoteValidationWorkspace creates a new remote validation workspace
func NewRemoteValidationWorkspace(ctx context.Context, githubClient *github.Client, validationWorkflowName string, tsk task.Task) (Workspace, error) {
	owner := tsk.Issue.Owner
	repo := tsk.Issue.Repository
	issueNumber := tsk.Issue.GetNumber()

	// Set up branches
	baseBranch := tsk.TargetBranch
	workBranch := fmt.Sprintf("bot/issue-%d-work", issueNumber)
	reviewBranch := tsk.SourceBranch

	// Create file system
	baseFS := filesystem.NewGithubReadOnlyFileSystem(githubClient, owner, repo, baseBranch)
	fs := filesystem.NewMemDiffFileSystem(baseFS)

	// Create Git repo
	gitRepo := git.NewGithubGitRepo(githubClient.Git, githubClient.Repositories, owner, repo)

	// Create pull request service
	prService := githubpkg.NewPullRequestService(githubClient)

	// Create validator
	validator := NewWorkflowValidator(githubClient, validationWorkflowName)

	// Determine if we need a pull request
	needsPullRequest := tsk.PullRequest == nil

	workspace := &remoteValidationWorkspace{
		gitRepo:      gitRepo,
		fs:           fs,
		prService:    prService,
		githubClient: githubClient,
		owner:        owner,
		repo:         repo,
		issueNumber:  issueNumber,
		needsPullRequest: needsPullRequest,
		baseBranch:   baseBranch,
		workBranch:   workBranch,
		reviewBranch: reviewBranch,
		validator:    validator,
	}

	return workspace, nil
}

func (rvw *remoteValidationWorkspace) FileSystem() filesystem.FileSystem {
	return rvw.fs
}

func (rvw *remoteValidationWorkspace) ValidateChanges(ctx context.Context, commitMessage string) error {
	changelist := rvw.fs.GetChangelist()
	if changelist.IsEmpty() {
		return fmt.Errorf("no changes to validate")
	}

	// Create work branch
	if err := rvw.gitRepo.CreateBranch(ctx, rvw.baseBranch, rvw.workBranch); err != nil {
		return fmt.Errorf("failed to create work branch: %w", err)
	}

	// Commit changes to work branch
	_, err := rvw.gitRepo.CommitChanges(ctx, rvw.workBranch, changelist, commitMessage)
	if err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	// Validate the work branch
	if err := rvw.validator.ValidateBranch(ctx, rvw.owner, rvw.repo, rvw.workBranch); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	return nil
}

func (rvw *remoteValidationWorkspace) PublishChangesForReview(ctx context.Context, pullRequestTitle, pullRequestBody string) error {
	changelist := rvw.fs.GetChangelist()
	if changelist.IsEmpty() {
		return fmt.Errorf("no changes to publish")
	}

	// Create review branch if it doesn't exist
	if err := rvw.gitRepo.CreateBranch(ctx, rvw.baseBranch, rvw.reviewBranch); err != nil {
		return fmt.Errorf("failed to create review branch: %w", err)
	}

	// Merge work branch into review branch
	_, err := rvw.gitRepo.Merge(ctx, rvw.workBranch, rvw.reviewBranch)
	if err != nil {
		return fmt.Errorf("failed to merge work branch into review branch: %w", err)
	}

	// Create or update pull request
	if rvw.needsPullRequest {
		_, err := rvw.prService.CreatePullRequest(ctx, rvw.owner, rvw.repo, rvw.baseBranch, rvw.reviewBranch, pullRequestTitle, pullRequestBody)
		if err != nil {
			return fmt.Errorf("failed to create pull request: %w", err)
		}
		rvw.needsPullRequest = false
	} else {
		// Find existing PR and update it
		pr, err := rvw.prService.GetPullRequestByBranch(ctx, rvw.owner, rvw.repo, rvw.reviewBranch)
		if err != nil {
			return fmt.Errorf("failed to get pull request: %w", err)
		}
		if pr == nil {
			return fmt.Errorf("expected pull request to exist but none found")
		}

		_, err = rvw.prService.UpdatePullRequest(ctx, rvw.owner, rvw.repo, pr.GetNumber(), &pullRequestTitle, &pullRequestBody)
		if err != nil {
			return fmt.Errorf("failed to update pull request: %w", err)
		}
	}

	return nil
}

// WorkflowValidator validates branches using GitHub Actions workflows
type WorkflowValidator struct {
	client       *github.Client
	workflowName string
}

// NewWorkflowValidator creates a new workflow validator
func NewWorkflowValidator(client *github.Client, workflowName string) BranchValidator {
	return &WorkflowValidator{
		client:       client,
		workflowName: workflowName,
	}
}

func (wv *WorkflowValidator) ValidateBranch(ctx context.Context, owner, repo, branch string) error {
	if wv.workflowName == "" {
		// No validation workflow configured
		return nil
	}

	// Trigger workflow
	_, err := wv.client.Actions.CreateWorkflowDispatchEventByFileName(ctx, owner, repo, wv.workflowName, github.CreateWorkflowDispatchEventRequest{
		Ref: branch,
	})
	if err != nil {
		return fmt.Errorf("failed to trigger validation workflow: %w", err)
	}

	// Wait for workflow to complete
	return wv.waitForWorkflowCompletion(ctx, owner, repo, branch)
}

func (wv *WorkflowValidator) waitForWorkflowCompletion(ctx context.Context, owner, repo, branch string) error {
	// This is a simplified implementation. In practice, you might want more sophisticated polling
	// and timeout handling.
	
	const maxWaitTime = 10 * time.Minute
	const pollInterval = 30 * time.Second
	
	timeout := time.After(maxWaitTime)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("validation workflow timed out after %v", maxWaitTime)
		case <-ticker.C:
			// Check workflow status
			workflows, _, err := wv.client.Actions.ListWorkflowRunsByFileName(ctx, owner, repo, wv.workflowName, &github.ListWorkflowRunsOptions{
				Branch: branch,
				Status: "completed",
			})
			if err != nil {
				continue // Continue polling on error
			}

			if len(workflows.WorkflowRuns) > 0 {
				latestRun := workflows.WorkflowRuns[0]
				if latestRun.GetConclusion() == "success" {
					return nil
				} else {
					return fmt.Errorf("validation workflow failed with conclusion: %s", latestRun.GetConclusion())
				}
			}
		}
	}
}