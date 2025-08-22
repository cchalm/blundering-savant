package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"
)

// remoteValidationWorkspaceFactory creates instances of remoteValidationWorkspace
type remoteValidationWorkspaceFactory struct {
	githubClient           *github.Client
	validationWorkflowName string
}

func (rvwf remoteValidationWorkspaceFactory) NewWorkspace(ctx context.Context, tsk task) (Workspace, error) {
	return NewRemoteValidationWorkspace(ctx, rvwf.githubClient, rvwf.validationWorkflowName, tsk)
}

// remoteValidationWorkspace is a workspace that tracks working changes in-memory until they need to be validated. For
// validation, changes are committed to a "work branch" and pushed to GitHub, where GitHub Actions run validation
// workflows. For publishing, the changes are merged from the "work branch" into a "review branch", from which a PR to
// the default branch has been/will be created. This workflow is designed to reduce noise on PRs while the bot is
// iterating on solutions
type remoteValidationWorkspace struct {
	git       GitRepo
	fs        *memDiffFileSystem
	prService PullRequestService

	issueNumber      int
	needsPullRequest bool

	baseBranch   string
	workBranch   string
	reviewBranch string

	validator BranchValidator
}

type GitRepo interface {
	GetBranchHead(ctx context.Context, branch string) (*github.Commit, error)
	CreateBranch(ctx context.Context, baseBranch string, newBranch string) error
	CommitChanges(ctx context.Context, branch string, changelist Changelist, commitMessage string) (*github.Commit, error)
	Merge(ctx context.Context, baseBranch string, targetBranch string) (*github.Commit, error)
	CompareCommits(ctx context.Context, base string, head string) (*github.CommitsComparison, error)
}

type BranchValidator interface {
	// ValidateBranch validates the given commit SHA, which is expected to be the head of the given branch
	ValidateBranch(ctx context.Context, branch string, commitSHA string) (ValidationResult, error)
}

type PullRequestService interface {
	Create(ctx context.Context, title string, body string) error
}

func NewRemoteValidationWorkspace(
	ctx context.Context,
	githubClient *github.Client,
	validationWorkflowName string,
	tsk task,
) (*remoteValidationWorkspace, error) {
	owner, repo := tsk.Issue.owner, tsk.Issue.repo

	// Get default branch
	repoInfo, _, err := githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repo info: %w", err)
	}
	if repoInfo.DefaultBranch == nil {
		return nil, fmt.Errorf("nil default branch")
	}
	baseBranch := *repoInfo.DefaultBranch

	workBranch := getWorkBranchName(tsk.Issue)
	reviewBranch := tsk.SourceBranch

	gitRepo := NewGithubGitRepo(githubClient.Git, githubClient.Repositories, owner, repo)
	githubFS := NewGithubFileSystem(githubClient.Repositories, owner, repo, workBranch)
	diffFS := NewMemDiffFileSystem(githubFS)

	// Create the work and review branches if they don't exist
	err = gitRepo.CreateBranch(ctx, baseBranch, workBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to create work branch '%s': %v", workBranch, err)
	}
	err = gitRepo.CreateBranch(ctx, baseBranch, reviewBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to create review branch '%s': %v", reviewBranch, err)
	}

	// We rely on these branches existing after this point, and it can take a moment, so let's wait
	err = awaitBranchCreation(ctx, githubClient, owner, repo, workBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to await creation of work branch '%s': %v", workBranch, err)
	}
	err = awaitBranchCreation(ctx, githubClient, owner, repo, reviewBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to await creation of work branch '%s': %v", reviewBranch, err)
	}

	prService := NewGithubPullRequestService(githubClient.PullRequests, owner, repo, reviewBranch, baseBranch)

	validator := NewGithubActionCommitValidator(githubClient, owner, repo, validationWorkflowName)

	return &remoteValidationWorkspace{
		git:       &gitRepo,
		fs:        &diffFS,
		prService: &prService,

		issueNumber:      tsk.Issue.number,
		needsPullRequest: tsk.PullRequest == nil,

		baseBranch:   baseBranch,
		workBranch:   workBranch,
		reviewBranch: reviewBranch,

		validator: validator,
	}, nil
}

func awaitBranchCreation(ctx context.Context, githubClient *github.Client, owner, repo, branch string) error {
	timeout := 10 * time.Second
	checkInterval := 2 * time.Second

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.Tick(checkInterval)
	for {
		maxRedirects := 10
		_, resp, err := githubClient.Repositories.GetBranch(ctx, owner, repo, branch, maxRedirects)
		if err == nil {
			// Found the branch
			break
		} else if resp == nil || resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("unexpected error while waiting for branch creation: %w", err)
		}

		select {
		case <-timeoutCtx.Done():
			if parentErr := ctx.Err(); parentErr != nil {
				return fmt.Errorf("branch creation check canceled: %w", parentErr)
			} else if err := timeoutCtx.Err(); err == context.DeadlineExceeded {
				return fmt.Errorf("branch creation check timed out after %v", timeout)
			} else {
				return fmt.Errorf("branch creation check canceled: %w", err)
			}
		case <-ticker:
			continue
		}
	}

	return nil
}

// Read reads a file from the work branch with any in-memory changes applied
func (rvw remoteValidationWorkspace) Read(ctx context.Context, path string) (string, error) {
	path = normalizePath(path)
	return rvw.fs.Read(ctx, path)
}

// Write writes a file in-memory
func (rvw *remoteValidationWorkspace) Write(ctx context.Context, path string, content string) error {
	path = normalizePath(path)
	return rvw.fs.Write(ctx, path, content)
}

// DeleteFile marks a file as deleted in-memory
func (rvw *remoteValidationWorkspace) Delete(ctx context.Context, path string) error {
	path = normalizePath(path)
	return rvw.fs.Delete(ctx, path)
}

// FileExists checks if a file exists in the current state
func (rvw remoteValidationWorkspace) FileExists(ctx context.Context, path string) (bool, error) {
	path = normalizePath(path)
	return rvw.fs.FileExists(ctx, path)
}

// IsDir checks if a path is a directory
func (rvw remoteValidationWorkspace) IsDir(ctx context.Context, path string) (bool, error) {
	path = normalizePath(path)
	return rvw.fs.IsDir(ctx, path)
}

// ListDir lists contents of a directory
func (rvw remoteValidationWorkspace) ListDir(ctx context.Context, path string) ([]string, error) {
	path = normalizePath(path)
	return rvw.fs.ListDir(ctx, path)
}

func (rvw remoteValidationWorkspace) HasLocalChanges() bool {
	return rvw.fs.HasChanges()
}

// HasUnpublishedChanges returns true if there are changes in the working branch that have not been merged to the review
// branch
func (rvw remoteValidationWorkspace) HasUnpublishedChanges(ctx context.Context) (bool, error) {
	// Compare the working branch against the review branch
	comparison, err := rvw.git.CompareCommits(ctx, rvw.reviewBranch, rvw.workBranch)
	if err != nil {
		return false, fmt.Errorf("failed to compare branches %s..%s: %w", rvw.reviewBranch, rvw.workBranch, err)
	}

	// If there are commits ahead, then there are unmerged changes
	return *comparison.AheadBy > 0, nil
}

// ClearLocalChanges deletes changes staged in-memory
func (rvw *remoteValidationWorkspace) ClearLocalChanges() {
	rvw.fs.Reset()
}

func (rvw *remoteValidationWorkspace) ValidateChanges(ctx context.Context, commitMessage *string) (ValidationResult, error) {
	var commitSHA string
	if rvw.HasLocalChanges() {
		if commitMessage == nil {
			return ValidationResult{}, fmt.Errorf("no commit message provided for validating local changes")
		}
		commit, err := rvw.commitToWorkBranch(ctx, *commitMessage)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to commit changes to work branch: %w", err)
		}
		commitSHA = *commit.SHA
	} else {
		headCommit, err := rvw.git.GetBranchHead(ctx, rvw.workBranch)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to get work branch info: %w", err)
		}
		commitSHA = *headCommit.SHA
	}

	if rvw.validator == nil {
		return ValidationResult{}, fmt.Errorf("failed to validate commit, no validator provided")
	}

	result, err := rvw.validator.ValidateBranch(ctx, rvw.workBranch, commitSHA)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to validate commit: %w", err)
	}

	return result, nil
}

func (rvw *remoteValidationWorkspace) commitToWorkBranch(ctx context.Context, commitMessage string) (*github.Commit, error) {
	if !rvw.fs.HasChanges() {
		return nil, fmt.Errorf("no changes to commit")
	}

	createdCommit, err := rvw.git.CommitChanges(ctx, rvw.workBranch, rvw.fs.GetChangelist(), commitMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to commit changes: %w", err)
	}

	// Reset in-memory changes
	rvw.fs.Reset()

	return createdCommit, nil
}

// PublishChangesForReview merges changes in the working branch into the review branch and creates a pull request, if
// one doesn't already exist. Returns an error if there are in-memory changes that have not been committed to the work
// branch via a ValidateChanges call
func (rvw *remoteValidationWorkspace) PublishChangesForReview(ctx context.Context, reviewRequestTitle string, reviewRequestBody string) error {
	_, err := rvw.mergeWorkBranchToReviewBranch(ctx)
	if err != nil {
		return fmt.Errorf("failed to merge work branch into review branch: %w", err)
	}

	if rvw.needsPullRequest {
		err := rvw.createPullRequest(ctx, reviewRequestTitle, reviewRequestBody)
		if err != nil {
			return fmt.Errorf("failed to create pull request: %w", err)
		}
		rvw.needsPullRequest = false
	}

	return err
}

func (rvw *remoteValidationWorkspace) createPullRequest(ctx context.Context, title string, body string) error {
	// Add issue reference and disclaimer to PR body
	body = fmt.Sprintf(`%s

Fixes #%d

---
*This PR was created by the Blundering Savant bot.*`, body, rvw.issueNumber)

	err := rvw.prService.Create(ctx, title, body)
	if err != nil {
		return fmt.Errorf("failed to create pull request: %w", err)
	}

	return nil
}

func (rvw *remoteValidationWorkspace) mergeWorkBranchToReviewBranch(ctx context.Context) (*github.Commit, error) {
	if rvw.HasLocalChanges() {
		return nil, fmt.Errorf("cannot merge from the work branch to the review branch while there are uncommitted changes in-memory")
	}

	commit, err := rvw.git.Merge(ctx, rvw.workBranch, rvw.reviewBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to merge work branch into review branch: %w", err)
	}

	return commit, nil
}

func getWorkBranchName(issue githubIssue) string {
	branchName := fmt.Sprintf("wip/issue-%d-%s", issue.number, sanitizeForBranchName(issue.title))
	return normalizeBranchName(branchName)
}

func getSourceBranchName(issue githubIssue) string {
	branchName := fmt.Sprintf("fix/issue-%d-%s", issue.number, sanitizeForBranchName(issue.title))
	return normalizeBranchName(branchName)
}

func sanitizeForBranchName(s string) string {
	// Convert to lowercase and replace invalid characters
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove invalid characters for git branch names
	invalidChars := []string{"~", "^", ":", "?", "*", "[", "]", "\\", "..", "@{", "/.", "//"}
	for _, char := range invalidChars {
		s = strings.ReplaceAll(s, char, "")
	}

	return s
}

func normalizeBranchName(s string) string {
	// Limit length
	if len(s) > 70 {
		s = s[:70]
	}
	// Clean up trailing separators
	s = strings.Trim(s, "-.")

	return s
}

func normalizePath(path string) string {
	// All file paths are absolute in our simplified file system
	return strings.TrimPrefix(path, "/")
}
