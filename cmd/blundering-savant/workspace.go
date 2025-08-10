package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v72/github"
)

// remoteValidationWorkspaceFactory creates instances of remoteValidationWorkspace
type remoteValidationWorkspaceFactory struct {
	githubClient *github.Client
}

func (rvwf remoteValidationWorkspaceFactory) NewWorkspace(ctx context.Context, tsk task) (Workspace, error) {
	return NewRemoteValidationWorkspace(ctx, rvwf.githubClient, tsk)
}

// remoteValidationWorkspace is a workspace that tracks working changes in-memory until they need to be validated. For
// validation, changes are committed to a "work branch" and pushed to GitHub, where GitHub Actions run validation
// workflows. For publishing, the changes are merged from the "work branch" into a "review branch", from which a PR to
// the default branch has been/will be created. This workflow is designed to reduce noise on PRs while the bot is
// iterating on solutions
type remoteValidationWorkspace struct {
	githubClient *github.Client
	owner        string
	repo         string

	workBranch   string
	reviewBranch string

	baseCommit   *github.Commit    // The commit we started from
	workingTree  map[string]string // path -> content (files we've modified)
	deletedFiles map[string]bool   // path -> true (files we've deleted)

	// Git objects
	currentTreeSHA string // Current tree SHA we're building on
}

func NewRemoteValidationWorkspace(
	ctx context.Context,
	githubClient *github.Client,
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

	// Create the work and review branches if they don't exist
	err = createBranchIfNotExist(ctx, githubClient, owner, repo, baseBranch, workBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to create work branch: %w", err)
	}
	err = createBranchIfNotExist(ctx, githubClient, owner, repo, baseBranch, reviewBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to create review branch: %w", err)
	}

	// Get the current commit for the work branch
	ref, _, err := githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", workBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	// Get the commit object
	commit, _, err := githubClient.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	return &remoteValidationWorkspace{
		githubClient: githubClient,
		owner:        owner,
		repo:         repo,

		workBranch:   workBranch,
		reviewBranch: reviewBranch,

		baseCommit:   commit,
		workingTree:  make(map[string]string),
		deletedFiles: make(map[string]bool),

		currentTreeSHA: *commit.Tree.SHA,
	}, nil
}

// CreateBranch creates a new branch from the default branch, if it doesn't already exist
func createBranchIfNotExist(ctx context.Context, githubClient *github.Client, owner string, repo string, baseBranch string, branch string) error {
	// Check if branch already exists
	_, resp, err := githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err == nil {
		return nil
	} else if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected error while checking if branch exists: %w", err)
	}

	// Get the base branch reference
	baseRef, _, err := githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", baseBranch))
	if err != nil {
		return fmt.Errorf("failed to get base branch ref: %w", err)
	}

	// Create new branch reference
	newRef := &github.Reference{
		Ref:    github.Ptr(fmt.Sprintf("refs/heads/%s", branch)),
		Object: &github.GitObject{SHA: baseRef.Object.SHA},
	}

	_, _, err = githubClient.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

// Read reads a file from the work branch with any in-memory changes applied
func (rvw remoteValidationWorkspace) Read(ctx context.Context, path string) (string, error) {
	path = normalizePath(path)

	// Check if file is deleted
	if rvw.deletedFiles[path] {
		return "", fmt.Errorf("file is deleted: %w", ErrFileNotFound)
	}

	// Check working tree first
	if content, exists := rvw.workingTree[path]; exists {
		return content, nil
	}

	// Fall back to GitHub
	fileContent, _, resp, err := rvw.githubClient.Repositories.GetContents(ctx, rvw.owner, rvw.repo, path, &github.RepositoryContentGetOptions{
		Ref: rvw.workBranch,
	})
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return "", ErrFileNotFound
		}
		return "", fmt.Errorf("failed to get file contents: %w", err)
	}

	if fileContent == nil {
		return "", fmt.Errorf("file content nil")
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

// Write writes a file in-memory
func (rvw *remoteValidationWorkspace) Write(_ context.Context, path string, content string) error {
	path = normalizePath(path)

	rvw.workingTree[path] = content
	// Remove from deleted files if it was marked as deleted
	delete(rvw.deletedFiles, path)
	return nil
}

// DeleteFile marks a file as deleted in-memory
func (rvw *remoteValidationWorkspace) Delete(_ context.Context, path string) error {
	path = normalizePath(path)

	rvw.deletedFiles[path] = true
	// Remove from working tree if it was modified
	delete(rvw.workingTree, path)
	return nil
}

// FileExists checks if a file exists in the current state
func (rvw remoteValidationWorkspace) FileExists(ctx context.Context, path string) (bool, error) {
	path = normalizePath(path)

	_, err := rvw.Read(ctx, path)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsDir checks if a path is a directory
func (rvw remoteValidationWorkspace) IsDir(ctx context.Context, path string) (bool, error) {
	_, dirContents, _, err := rvw.githubClient.Repositories.GetContents(ctx, rvw.owner, rvw.repo, path, &github.RepositoryContentGetOptions{
		Ref: rvw.workBranch,
	})

	if dirContents != nil {
		return true, nil
	}

	if err != nil && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found")) {
		return false, nil
	}

	return false, err
}

// ListDir lists contents of a directory
func (rvw remoteValidationWorkspace) ListDir(ctx context.Context, path string) ([]string, error) {
	_, dirContents, _, err := rvw.githubClient.Repositories.GetContents(ctx, rvw.owner, rvw.repo, path, &github.RepositoryContentGetOptions{
		Ref: rvw.workBranch,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	var files []string
	for _, content := range dirContents {
		if content.Name != nil {
			name := *content.Name
			if content.Type != nil && *content.Type == "dir" {
				name += "/"
			}
			files = append(files, name)
		}
	}
	return files, nil
}

// HasChanges returns true if there are uncommitted changes in-memory OR changes in the working branch that have not
// been merged to the review branch
func (rvw remoteValidationWorkspace) HasChanges(_ context.Context) (bool, error) {
	// TODO diff the working branch against the review branch
	return rvw.hasChangesInMemory(), nil
}

func (rvw remoteValidationWorkspace) hasChangesInMemory() bool {
	return len(rvw.workingTree) > 0 || len(rvw.deletedFiles) > 0
}

// GetChangedFiles returns a list of files that have been modified
func (rvw remoteValidationWorkspace) GetChangedFiles() []string {
	var files []string

	for path := range rvw.workingTree {
		files = append(files, path)
	}

	for path := range rvw.deletedFiles {
		files = append(files, path)
	}

	return files
}

// ClearChanges deletes any file changes staged in this file system
func (rvw *remoteValidationWorkspace) ClearChanges(_ context.Context) {
	rvw.workingTree = map[string]string{}
	rvw.deletedFiles = map[string]bool{}
}

func (rvw *remoteValidationWorkspace) ValidateChanges(ctx context.Context, commitMessage string) (ValidationResult, error) {
	hasChanges, err := rvw.HasChanges(ctx)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to check changes: %w", err)
	}
	if !hasChanges {
		return ValidationResult{}, fmt.Errorf("no changes to validate")
	}

	// Commit changes to the work branch
	_, err = rvw.commitToWorkBranch(ctx, commitMessage)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to commit changes to work branch: %w", err)
	}

	// TODO run validation workflows
	result := ValidationResult{
		TestOutput: "Validation not yet implemented", // TODO
		LintOutput: "Validation not yet implemented", // TODO
	}

	return result, nil
}

func (rvw *remoteValidationWorkspace) commitToWorkBranch(ctx context.Context, commitMessage string) (*github.Commit, error) {
	if !rvw.hasChangesInMemory() {
		return nil, fmt.Errorf("no changes to commit")
	}

	// Get the current tree
	currentTree, _, err := rvw.githubClient.Git.GetTree(ctx, rvw.owner, rvw.repo, rvw.currentTreeSHA, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get current tree: %w", err)
	}

	// Build new tree entries based on current tree + changes
	var treeEntries []*github.TreeEntry

	// Start with existing files from current tree, excluding deleted ones
	for _, entry := range currentTree.Entries {
		if entry.Path == nil {
			continue
		}

		path := *entry.Path

		// Skip deleted files
		if rvw.deletedFiles[path] {
			continue
		}

		// Skip files that we're updating (they'll be added below)
		if _, isModified := rvw.workingTree[path]; isModified {
			continue
		}

		// Keep existing file as-is
		treeEntries = append(treeEntries, entry)
	}

	// Add modified/new files
	for path, content := range rvw.workingTree {
		// Create blob for file content
		blob := &github.Blob{
			Content:  github.Ptr(content),
			Encoding: github.Ptr("utf-8"),
		}

		createdBlob, _, err := rvw.githubClient.Git.CreateBlob(ctx, rvw.owner, rvw.repo, blob)
		if err != nil {
			return nil, fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		// Add tree entry
		treeEntry := &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  createdBlob.SHA,
		}
		treeEntries = append(treeEntries, treeEntry)
	}

	createdTree, _, err := rvw.githubClient.Git.CreateTree(ctx, rvw.owner, rvw.repo, "", treeEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %w", err)
	}

	commit := &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    createdTree,
		Parents: []*github.Commit{rvw.baseCommit},
	}

	createdCommit, _, err := rvw.githubClient.Git.CreateCommit(ctx, rvw.owner, rvw.repo, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference to point to new commit
	ref, _, err := rvw.githubClient.Git.GetRef(ctx, rvw.owner, rvw.repo, fmt.Sprintf("refs/heads/%s", rvw.workBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	ref.Object.SHA = createdCommit.SHA
	_, _, err = rvw.githubClient.Git.UpdateRef(ctx, rvw.owner, rvw.repo, ref, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch ref: %w", err)
	}

	// Update our state
	rvw.baseCommit = createdCommit
	rvw.currentTreeSHA = *createdTree.SHA
	rvw.ClearChanges(ctx)

	return createdCommit, nil
}

// PublishChanges merges changes in the working branch into the review branch. Returns an error if there are in-memory
// changes that have not been committed to the work branch via a ValidateChanges call
func (rvw *remoteValidationWorkspace) PublishChanges(ctx context.Context, commitMessage string) error {
	_, err := rvw.mergeWorkBranchToReviewBranch(ctx, commitMessage)
	return err
}

func (rvw *remoteValidationWorkspace) mergeWorkBranchToReviewBranch(ctx context.Context, commitMessage string) (*github.Commit, error) {
	if rvw.hasChangesInMemory() {
		return nil, fmt.Errorf("cannot merge from the work branch to the review branch while there are uncommitted changes in-memory")
	}

	// Get the current commit from the work branch
	workBranchRef, _, err := rvw.githubClient.Git.GetRef(ctx, rvw.owner, rvw.repo, fmt.Sprintf("refs/heads/%s", rvw.workBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get work branch ref: %w", err)
	}
	workBranchCommitSHA := workBranchRef.Object.SHA

	// Get the review branch reference
	reviewBranchRef, _, err := rvw.githubClient.Git.GetRef(ctx, rvw.owner, rvw.repo, fmt.Sprintf("refs/heads/%s", rvw.reviewBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get review branch ref: %w", err)
	}
	reviewBranchCommitSHA := reviewBranchRef.Object.SHA

	// Check if work branch is ahead of review branch
	if *workBranchCommitSHA == *reviewBranchCommitSHA {
		return nil, fmt.Errorf("work branch has no new commits to merge")
	}

	// Get the work branch commit to use as the new commit for review branch
	workBranchCommit, _, err := rvw.githubClient.Git.GetCommit(ctx, rvw.owner, rvw.repo, *workBranchCommitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get work branch commit: %w", err)
	}

	// Get the review branch commit to use as parent
	reviewBranchCommit, _, err := rvw.githubClient.Git.GetCommit(ctx, rvw.owner, rvw.repo, *reviewBranchCommitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get review branch commit: %w", err)
	}

	// Create a new commit on the review branch that merges the work branch changes
	// This creates a merge commit with both parents
	mergeCommit := &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    workBranchCommit.Tree,                                  // Use the tree from work branch (contains all the changes)
		Parents: []*github.Commit{reviewBranchCommit, workBranchCommit}, // Both parents for merge commit
	}

	createdMergeCommit, _, err := rvw.githubClient.Git.CreateCommit(ctx, rvw.owner, rvw.repo, mergeCommit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create merge commit: %w", err)
	}

	// Update the review branch reference to point to the new merge commit
	reviewBranchRef.Object.SHA = createdMergeCommit.SHA
	_, _, err = rvw.githubClient.Git.UpdateRef(ctx, rvw.owner, rvw.repo, reviewBranchRef, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update review branch ref: %w", err)
	}

	return createdMergeCommit, nil
}

// GetCommitSHA returns the current commit SHA
func (rvw remoteValidationWorkspace) GetCommitSHA() string {
	if rvw.baseCommit != nil && rvw.baseCommit.SHA != nil {
		return *rvw.baseCommit.SHA
	}
	return ""
}

func normalizePath(path string) string {
	// Paths in git are always relative, they cannot start with a slash
	return strings.TrimPrefix(path, "/")
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
