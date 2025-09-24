package workspace

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v72/github"
)

// InsufficientPermissionsError indicates that an operation failed due to insufficient GitHub token permissions.
type InsufficientPermissionsError struct {
	Operation string
	Reason    string
}

func (ipe InsufficientPermissionsError) Error() string {
	return fmt.Sprintf("insufficient permissions to %s: %s", ipe.Operation, ipe.Reason)
}

type Changelist interface {
	ForEachModified(fn func(path string, content string) error) error
	ForEachDeleted(fn func(path string) error) error
	IsModified(path string) bool
	IsDeleted(path string) bool
	IsEmpty() bool
}

// githubGitRepo implements a handful of porcelain git commands using the GitHub API. It manipulates a remote git
// repository directly; e.g. commits appear on the remote without a push
type githubGitRepo struct {
	git          *github.GitService          // For low-level git operations
	reposService *github.RepositoriesService // For high-level operations supported by the github API

	owner string
	repo  string
}

func NewGithubGitRepo(gitService *github.GitService, reposService *github.RepositoriesService, owner string, repo string) githubGitRepo {
	return githubGitRepo{
		git:          gitService,
		reposService: reposService,

		owner: owner,
		repo:  repo,
	}
}

func (ggr *githubGitRepo) GetBranchHead(ctx context.Context, branch string) (*github.Commit, error) {
	ref, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref for '%s': %w", branch, err)
	}

	commit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit from ref: %w", err)
	}

	return commit, nil
}

// CreateBranch creates a new branch. If the branch already exists, CreateBranch does not return an error
func (ggr *githubGitRepo) CreateBranch(ctx context.Context, baseBranch string, newBranch string) error {
	// Check if branch already exists
	_, resp, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", newBranch))
	if err == nil {
		return nil
	} else if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected error while checking if branch exists: %w", err)
	}

	// Get the base branch reference
	baseRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", baseBranch))
	if err != nil {
		return fmt.Errorf("failed to get base branch ref: %w", err)
	}

	// Create new branch reference
	newRef := &github.Reference{
		Ref:    github.Ptr(fmt.Sprintf("refs/heads/%s", newBranch)),
		Object: &github.GitObject{SHA: baseRef.Object.SHA},
	}

	_, _, err = ggr.git.CreateRef(ctx, ggr.owner, ggr.repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

func (ggr *githubGitRepo) CommitChanges(ctx context.Context, branch string, changelist Changelist, commitMessage string) (*github.Commit, error) {
	if changelist.IsEmpty() {
		return nil, fmt.Errorf("no changes to commit")
	}

	// Get current tree SHA from the target branch
	ref, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	baseCommit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	baseTree := baseCommit.Tree

	// Build tree entries for changes
	var treeChangeEntries []*github.TreeEntry

	// Add modified/new files
	err = changelist.ForEachModified(func(path string, content string) error {
		// Create blob for file content
		blob := &github.Blob{
			Content:  github.Ptr(content),
			Encoding: github.Ptr("utf-8"),
		}

		createdBlob, _, err := ggr.git.CreateBlob(ctx, ggr.owner, ggr.repo, blob)
		if err != nil {
			return fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		// Add tree entry
		treeEntry := &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  createdBlob.SHA,
		}
		treeChangeEntries = append(treeChangeEntries, treeEntry)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add modified files to new tree: %w", err)
	}

	// Mark entries for deletion
	err = changelist.ForEachDeleted(func(path string) error {
		// Add tree entry
		treeEntry := &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  nil, // Nil SHA indicates delete
		}
		treeChangeEntries = append(treeChangeEntries, treeEntry)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to mark deleted files in new tree: %w", err)
	}

	newTree, resp, err := ggr.git.CreateTree(ctx, ggr.owner, ggr.repo, *baseTree.SHA, treeChangeEntries)
	if err != nil {
		if resp.StatusCode == http.StatusNotFound && ggr.isLikelyWorkflowPermissionError(treeChangeEntries) {
			return nil, InsufficientPermissionsError{
				Operation: "modify GitHub workflow files",
				Reason:    "the GitHub token does not include the 'workflow' scope",
			}
		}
		return nil, fmt.Errorf("failed to create tree: %w", err)
	}

	commit := &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    newTree,
		Parents: []*github.Commit{baseCommit},
	}

	createdCommit, _, err := ggr.git.CreateCommit(ctx, ggr.owner, ggr.repo, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference to point to new commit
	ref.Object.SHA = createdCommit.SHA
	_, _, err = ggr.git.UpdateRef(ctx, ggr.owner, ggr.repo, ref, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch ref: %w", err)
	}

	return createdCommit, nil
}

func (ggr *githubGitRepo) Merge(ctx context.Context, sourceBranch string, targetBranch string) (*github.Commit, error) {
	// Compare the branches to determine the merge strategy
	comparison, _, err := ggr.reposService.CompareCommits(ctx, ggr.owner, ggr.repo, targetBranch, sourceBranch, &github.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}

	if comparison == nil || comparison.AheadBy == nil || comparison.BehindBy == nil {
		return nil, fmt.Errorf("unexpected nil in comparison result: %+v", comparison)
	}

	sourceBranchRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", sourceBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get source branch ref: %w", err)
	}

	targetBranchRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", targetBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get target branch ref: %w", err)
	}

	// Handle no-op case: nothing to merge
	if *comparison.AheadBy == 0 {
		// No changes to merge - return the current commit of the target branch
		targetCommit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, *targetBranchRef.Object.SHA)
		if err != nil {
			return nil, fmt.Errorf("failed to get target branch commit: %w", err)
		}

		return targetCommit, nil
	}

	// Handle fast-forward case: target branch is behind source branch with no divergent commits
	if *comparison.BehindBy == 0 {
		// This is a fast-forward merge - just update the target branch reference

		// Update target branch to point to source branch commit
		targetBranchRef.Object.SHA = sourceBranchRef.Object.SHA
		_, _, err = ggr.git.UpdateRef(ctx, ggr.owner, ggr.repo, targetBranchRef, false)
		if err != nil {
			return nil, fmt.Errorf("failed to update target branch ref for fast-forward: %w", err)
		}

		// Return the commit that target branch now points to
		sourceCommit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, *sourceBranchRef.Object.SHA)
		if err != nil {
			return nil, fmt.Errorf("failed to get source branch commit: %w", err)
		}

		return sourceCommit, nil
	}

	// Branches have diverged so a three-way merge is required. We will not handle this case at the moment
	return nil, fmt.Errorf("three-way merge required but not yet implemented")
}

func (ggr *githubGitRepo) CompareCommits(ctx context.Context, base string, head string) (*github.CommitsComparison, error) {
	comparison, _, err := ggr.reposService.CompareCommits(ctx, ggr.owner, ggr.repo, base, head, &github.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}

	return comparison, nil
}

// isLikelyWorkflowPermissionError checks if the tree entries suggest a workflow permission issue
func (ggr *githubGitRepo) isLikelyWorkflowPermissionError(entries []*github.TreeEntry) bool {
	for _, entry := range entries {
		if entry.Path != nil {
			path := *entry.Path
			// Check for GitHub workflows directory
			if strings.HasPrefix(path, ".github/workflows/") {
				return true
			}
		}
	}
	return false
}
