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
	git *github.GitService
}

func NewGithubGitRepo(gitService *github.GitService) githubGitRepo {
	return githubGitRepo{
		git: gitService,
	}
}

// CreateBranch creates a new branch. If the branch already exists, CreateBranch does not return an error
func (ggr *githubGitRepo) CreateBranch(ctx context.Context, owner string, repo string, baseBranch string, newBranch string) error {
	// Check if branch already exists
	_, resp, err := ggr.git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", newBranch))
	if err == nil {
		return nil
	} else if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected error while checking if branch exists: %w", err)
	}

	// Get the base branch reference
	baseRef, _, err := ggr.git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", baseBranch))
	if err != nil {
		return fmt.Errorf("failed to get base branch ref: %w", err)
	}

	// Create new branch reference
	newRef := &github.Reference{
		Ref:    github.Ptr(fmt.Sprintf("refs/heads/%s", newBranch)),
		Object: &github.GitObject{SHA: baseRef.Object.SHA},
	}

	_, _, err = ggr.git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

func (ggr *githubGitRepo) CommitChanges(ctx context.Context, owner string, repo string, branch string, changelist Changelist, commitMessage string) (*github.Commit, error) {
	if changelist.IsEmpty() {
		return nil, fmt.Errorf("no changes to commit")
	}

	// Get current tree SHA from the target branch
	ref, _, err := ggr.git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	baseCommit, _, err := ggr.git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Get the current tree
	baseTree, _, err := ggr.git.GetTree(ctx, owner, repo, *baseCommit.SHA, true)
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

		createdBlob, _, err := ggr.git.CreateBlob(ctx, owner, repo, blob)
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

	newTree, _, err := ggr.git.CreateTree(ctx, owner, repo, "", treeEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %w", err)
	}

	commit := &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    newTree,
		Parents: []*github.Commit{baseCommit},
	}

	createdCommit, _, err := ggr.git.CreateCommit(ctx, owner, repo, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference to point to new commit
	ref.Object.SHA = createdCommit.SHA
	_, _, err = ggr.git.UpdateRef(ctx, owner, repo, ref, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch ref: %w", err)
	}

	return createdCommit, nil
}
