package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v72/github"
)

type Changelist interface {
	ForEachModified(fn func(path string, content string) error) error
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

	// Get the current tree
	baseTree, _, err := ggr.git.GetTree(ctx, ggr.owner, ggr.repo, *baseCommit.SHA, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get current tree: %w", err)
	}

	// Build new tree entries based on current tree + changes
	var treeEntries []*github.TreeEntry

	// Start with existing files from current tree, excluding deleted ones
	for _, entry := range baseTree.Entries {
		if entry.Path == nil {
			continue
		}

		path := *entry.Path

		// Skip deleted files
		if changelist.IsDeleted(path) {
			continue
		}

		// Skip files that we're updating (they'll be added below)
		if changelist.IsModified(path) {
			continue
		}

		// Keep existing file as-is
		treeEntries = append(treeEntries, entry)
	}

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
		treeEntries = append(treeEntries, treeEntry)

		return nil
	})
	if err != nil {
		return nil, err
	}

	newTree, _, err := ggr.git.CreateTree(ctx, ggr.owner, ggr.repo, "", treeEntries)
	if err != nil {
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

func (ggr *githubGitRepo) Merge(ctx context.Context, sourceBranch string, targetBranch string, commitMessage string) (*github.Commit, error) {
	// Get the current commit from the source branch
	sourceBranchRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", sourceBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get source branch ref: %w", err)
	}
	sourceBranchCommitSHA := sourceBranchRef.Object.SHA

	// Get the target branch reference
	targetBranchRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", targetBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get target branch ref: %w", err)
	}
	targetBranchCommitSHA := targetBranchRef.Object.SHA

	// Check if source branch is ahead of target branch
	if *sourceBranchCommitSHA == *targetBranchCommitSHA {
		return nil, fmt.Errorf("source branch has no new commits to merge")
	}

	// Get the source branch commit to use as the new commit for target branch
	sourceBranchCommit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, *sourceBranchCommitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get source branch commit: %w", err)
	}

	// Get the target branch commit to use as parent
	targetBranchCommit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, *targetBranchCommitSHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get target branch commit: %w", err)
	}

	// Create a new commit on the target branch that merges the source branch changes
	// This creates a merge commit with both parents
	mergeCommit := &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    sourceBranchCommit.Tree,                                  // Use the tree from source branch (contains all the changes)
		Parents: []*github.Commit{targetBranchCommit, sourceBranchCommit}, // Both parents for merge commit
	}

	createdMergeCommit, _, err := ggr.git.CreateCommit(ctx, ggr.owner, ggr.repo, mergeCommit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create merge commit: %w", err)
	}

	// Update the target branch reference to point to the new merge commit
	targetBranchRef.Object.SHA = createdMergeCommit.SHA
	_, _, err = ggr.git.UpdateRef(ctx, ggr.owner, ggr.repo, targetBranchRef, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update target branch ref: %w", err)
	}

	return createdMergeCommit, nil
}

func (ggr *githubGitRepo) CompareCommits(ctx context.Context, base string, head string) (*github.CommitsComparison, error) {
	comparison, _, err := ggr.reposService.CompareCommits(ctx, ggr.owner, ggr.repo, base, head, &github.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}

	return comparison, nil
}
