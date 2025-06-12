package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v72/github"
)

// GitHubFileSystem implements file edits via the GitHub API. It maintains a working tree in memory and can commit
// changes to a branch
type GitHubFileSystem struct {
	client       *github.Client
	owner        string
	repo         string
	branch       string
	baseCommit   *github.Commit    // The commit we started from
	workingTree  map[string]string // path -> content (files we've modified)
	deletedFiles map[string]bool   // path -> true (files we've deleted)

	// Git objects
	currentTreeSHA string // Current tree SHA we're building on
}

// NewGitHubFileSystem creates a new GitHub file system that works on a specific branch
func NewGitHubFileSystem(client *github.Client, owner, repo, branch string) (*GitHubFileSystem, error) {
	ctx := context.Background()

	gfs := &GitHubFileSystem{
		client:       client,
		owner:        owner,
		repo:         repo,
		branch:       branch,
		workingTree:  make(map[string]string),
		deletedFiles: make(map[string]bool),
	}

	// Get the current commit for this branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	// Get the commit object
	commit, _, err := client.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	gfs.baseCommit = commit
	gfs.currentTreeSHA = *commit.Tree.SHA

	return gfs, nil
}

// ReadFile reads a file from the current state (working tree or GitHub)
func (gfs *GitHubFileSystem) ReadFile(path string) (string, error) {
	path = normalizePath(path)

	// Check if file is deleted
	if gfs.deletedFiles[path] {
		return "", fmt.Errorf("file not found: %s", path)
	}

	// Check working tree first
	if content, exists := gfs.workingTree[path]; exists {
		return content, nil
	}

	// Fall back to GitHub
	ctx := context.Background()
	fileContent, _, _, err := gfs.client.Repositories.GetContents(ctx, gfs.owner, gfs.repo, path, &github.RepositoryContentGetOptions{
		Ref: gfs.branch,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get file contents: %w", err)
	}

	if fileContent == nil {
		return "", fmt.Errorf("file not found: %s", path)
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

// WriteFile stores changes in the working tree
func (gfs *GitHubFileSystem) WriteFile(path, content string) error {
	path = normalizePath(path)

	gfs.workingTree[path] = content
	// Remove from deleted files if it was marked as deleted
	delete(gfs.deletedFiles, path)
	return nil
}

// DeleteFile marks a file as deleted in the working tree
func (gfs *GitHubFileSystem) DeleteFile(path string) error {
	path = normalizePath(path)

	gfs.deletedFiles[path] = true
	// Remove from working tree if it was modified
	delete(gfs.workingTree, path)
	return nil
}

// FileExists checks if a file exists in the current state
func (gfs *GitHubFileSystem) FileExists(path string) (bool, error) {
	path = normalizePath(path)

	// Check if file is deleted
	if gfs.deletedFiles[path] {
		return false, nil
	}

	// Check working tree
	if _, exists := gfs.workingTree[path]; exists {
		return true, nil
	}

	// Check GitHub
	_, err := gfs.ReadFile(path)
	if err != nil {
		if strings.Contains(err.Error(), "file not found") || strings.Contains(err.Error(), "404") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsDirectory checks if a path is a directory
func (gfs *GitHubFileSystem) IsDirectory(path string) (bool, error) {
	ctx := context.Background()
	_, dirContents, _, err := gfs.client.Repositories.GetContents(ctx, gfs.owner, gfs.repo, path, &github.RepositoryContentGetOptions{
		Ref: gfs.branch,
	})

	if dirContents != nil {
		return true, nil
	}

	if err != nil && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found")) {
		return false, nil
	}

	return false, err
}

// ListDirectory lists contents of a directory
func (gfs *GitHubFileSystem) ListDirectory(path string) ([]string, error) {
	ctx := context.Background()
	_, dirContents, _, err := gfs.client.Repositories.GetContents(ctx, gfs.owner, gfs.repo, path, &github.RepositoryContentGetOptions{
		Ref: gfs.branch,
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

// HasChanges returns true if there are uncommitted changes in the working tree
func (gfs *GitHubFileSystem) HasChanges() bool {
	return len(gfs.workingTree) > 0 || len(gfs.deletedFiles) > 0
}

// GetChangedFiles returns a list of files that have been modified
func (gfs *GitHubFileSystem) GetChangedFiles() []string {
	var files []string

	for path := range gfs.workingTree {
		files = append(files, path)
	}

	for path := range gfs.deletedFiles {
		files = append(files, path)
	}

	return files
}

// CommitChanges creates a commit with all changes in the working tree
func (gfs *GitHubFileSystem) CommitChanges(commitMessage string) (*github.Commit, error) {
	if !gfs.HasChanges() {
		return nil, fmt.Errorf("no changes to commit")
	}

	ctx := context.Background()

	// Get the current tree
	currentTree, _, err := gfs.client.Git.GetTree(ctx, gfs.owner, gfs.repo, gfs.currentTreeSHA, true)
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
		if gfs.deletedFiles[path] {
			continue
		}

		// Skip files that we're updating (they'll be added below)
		if _, isModified := gfs.workingTree[path]; isModified {
			continue
		}

		// Keep existing file as-is
		treeEntries = append(treeEntries, entry)
	}

	// Add modified/new files
	for path, content := range gfs.workingTree {
		// Create blob for file content
		blob := &github.Blob{
			Content:  github.Ptr(content),
			Encoding: github.Ptr("utf-8"),
		}

		createdBlob, _, err := gfs.client.Git.CreateBlob(ctx, gfs.owner, gfs.repo, blob)
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

	createdTree, _, err := gfs.client.Git.CreateTree(ctx, gfs.owner, gfs.repo, "", treeEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %w", err)
	}

	commit := &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    createdTree,
		Parents: []*github.Commit{gfs.baseCommit},
	}

	createdCommit, _, err := gfs.client.Git.CreateCommit(ctx, gfs.owner, gfs.repo, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference to point to new commit
	ref, _, err := gfs.client.Git.GetRef(ctx, gfs.owner, gfs.repo, fmt.Sprintf("refs/heads/%s", gfs.branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	ref.Object.SHA = createdCommit.SHA
	_, _, err = gfs.client.Git.UpdateRef(ctx, gfs.owner, gfs.repo, ref, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch ref: %w", err)
	}

	// Update our state
	gfs.baseCommit = createdCommit
	gfs.currentTreeSHA = *createdTree.SHA
	gfs.workingTree = make(map[string]string)
	gfs.deletedFiles = make(map[string]bool)

	return createdCommit, nil
}

// CreatePullRequest creates a PR from the current branch to the target branch
func (gfs *GitHubFileSystem) CreatePullRequest(title, body, targetBranch string) (*github.PullRequest, error) {
	if gfs.HasChanges() {
		return nil, fmt.Errorf("cannot create PR with uncommitted changes - call CommitChanges first")
	}

	ctx := context.Background()

	pr := &github.NewPullRequest{
		Title:               github.Ptr(title),
		Body:                github.Ptr(body),
		Head:                github.Ptr(gfs.branch),
		Base:                github.Ptr(targetBranch),
		MaintainerCanModify: github.Bool(true),
	}

	createdPR, _, err := gfs.client.PullRequests.Create(ctx, gfs.owner, gfs.repo, pr)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	return createdPR, nil
}

// GetBranch returns the current branch name
func (gfs *GitHubFileSystem) GetBranch() string {
	return gfs.branch
}

// GetCommitSHA returns the current commit SHA
func (gfs *GitHubFileSystem) GetCommitSHA() string {
	if gfs.baseCommit != nil && gfs.baseCommit.SHA != nil {
		return *gfs.baseCommit.SHA
	}
	return ""
}

func normalizePath(path string) string {
	// Paths in git are always relative, they cannot start with a slash
	return strings.TrimPrefix(path, "/")
}
