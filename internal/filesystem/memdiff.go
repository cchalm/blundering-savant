package filesystem

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/git"
)

// MemDiffFileSystem sits on top of a ReadOnlyFileSystem and tracks changes in-memory
type MemDiffFileSystem struct {
	baseFileSystem ReadOnlyFileSystem

	workingTree  map[string]string   // path -> content (files we've modified)
	deletedFiles map[string]struct{} // path -> struct{}{} (files we've deleted)
}

// NewMemDiffFileSystem creates a new in-memory diff file system
func NewMemDiffFileSystem(baseFileSystem ReadOnlyFileSystem) *MemDiffFileSystem {
	return &MemDiffFileSystem{
		baseFileSystem: baseFileSystem,
		workingTree:    make(map[string]string),
		deletedFiles:   make(map[string]struct{}),
	}
}

func (mdfs *MemDiffFileSystem) Read(ctx context.Context, path string) (string, error) {
	// Check if file is deleted
	if _, exists := mdfs.deletedFiles[path]; exists {
		return "", ErrFileNotFound
	}

	// Check if file is in working tree
	if content, exists := mdfs.workingTree[path]; exists {
		return content, nil
	}

	// Fall back to base file system
	return mdfs.baseFileSystem.Read(ctx, path)
}

func (mdfs *MemDiffFileSystem) Write(ctx context.Context, path string, content string) error {
	// Remove from deleted files if it was deleted
	delete(mdfs.deletedFiles, path)

	// Add to working tree
	mdfs.workingTree[path] = content
	return nil
}

func (mdfs *MemDiffFileSystem) Delete(ctx context.Context, path string) error {
	// Remove from working tree if it exists there
	delete(mdfs.workingTree, path)

	// Add to deleted files
	mdfs.deletedFiles[path] = struct{}{}
	return nil
}

func (mdfs *MemDiffFileSystem) FileExists(ctx context.Context, path string) (bool, error) {
	// Check if file is deleted
	if _, exists := mdfs.deletedFiles[path]; exists {
		return false, nil
	}

	// Check if file is in working tree
	if _, exists := mdfs.workingTree[path]; exists {
		return true, nil
	}

	// Fall back to base file system
	return mdfs.baseFileSystem.FileExists(ctx, path)
}

func (mdfs *MemDiffFileSystem) IsDir(ctx context.Context, dir string) (bool, error) {
	return mdfs.baseFileSystem.IsDir(ctx, dir)
}

func (mdfs *MemDiffFileSystem) ListDir(ctx context.Context, dir string) ([]string, error) {
	baseFiles, err := mdfs.baseFileSystem.ListDir(ctx, dir)
	if err != nil {
		return nil, err
	}

	// Create a map to track all files
	allFiles := make(map[string]struct{})

	// Add base files
	for _, file := range baseFiles {
		allFiles[file] = struct{}{}
	}

	// Add files from working tree that are in this directory
	for path := range mdfs.workingTree {
		if strings.HasPrefix(path, dir+"/") || path == dir {
			allFiles[path] = struct{}{}
		}
	}

	// Remove deleted files
	for path := range mdfs.deletedFiles {
		delete(allFiles, path)
	}

	// Convert back to slice
	result := make([]string, 0, len(allFiles))
	for file := range allFiles {
		result = append(result, file)
	}

	return result, nil
}

// GetChangelist returns the changes tracked in this filesystem as a Changelist
func (mdfs *MemDiffFileSystem) GetChangelist() git.Changelist {
	return &memDiffChangelist{
		workingTree:  mdfs.workingTree,
		deletedFiles: mdfs.deletedFiles,
	}
}

// memDiffChangelist implements the Changelist interface for MemDiffFileSystem
type memDiffChangelist struct {
	workingTree  map[string]string
	deletedFiles map[string]struct{}
}

func (mdcl *memDiffChangelist) ForEachModified(fn func(path string, content string) error) error {
	for path, content := range mdcl.workingTree {
		if err := fn(path, content); err != nil {
			return err
		}
	}
	return nil
}

func (mdcl *memDiffChangelist) ForEachDeleted(fn func(path string) error) error {
	for path := range mdcl.deletedFiles {
		if err := fn(path); err != nil {
			return err
		}
	}
	return nil
}

func (mdcl *memDiffChangelist) IsModified(path string) bool {
	_, exists := mdcl.workingTree[path]
	return exists
}

func (mdcl *memDiffChangelist) IsDeleted(path string) bool {
	_, exists := mdcl.deletedFiles[path]
	return exists
}

func (mdcl *memDiffChangelist) IsEmpty() bool {
	return len(mdcl.workingTree) == 0 && len(mdcl.deletedFiles) == 0
}

// githubReadOnlyFileSystem implements ReadOnlyFileSystem for GitHub repositories
type githubReadOnlyFileSystem struct {
	client *github.Client
	owner  string
	repo   string
	ref    string
}

// NewGithubReadOnlyFileSystem creates a new GitHub-backed read-only file system
func NewGithubReadOnlyFileSystem(client *github.Client, owner, repo, ref string) ReadOnlyFileSystem {
	return &githubReadOnlyFileSystem{
		client: client,
		owner:  owner,
		repo:   repo,
		ref:    ref,
	}
}

func (grfs *githubReadOnlyFileSystem) Read(ctx context.Context, path string) (string, error) {
	fileContent, _, resp, err := grfs.client.Repositories.GetContents(ctx, grfs.owner, grfs.repo, path, &github.RepositoryContentGetOptions{
		Ref: grfs.ref,
	})
	
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return "", ErrFileNotFound
		}
		return "", fmt.Errorf("failed to get file content: %w", err)
	}

	if fileContent == nil {
		return "", fmt.Errorf("file content is nil")
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

func (grfs *githubReadOnlyFileSystem) FileExists(ctx context.Context, path string) (bool, error) {
	_, _, resp, err := grfs.client.Repositories.GetContents(ctx, grfs.owner, grfs.repo, path, &github.RepositoryContentGetOptions{
		Ref: grfs.ref,
	})
	
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if file exists: %w", err)
	}

	return true, nil
}

func (grfs *githubReadOnlyFileSystem) IsDir(ctx context.Context, dir string) (bool, error) {
	_, directoryContent, resp, err := grfs.client.Repositories.GetContents(ctx, grfs.owner, grfs.repo, dir, &github.RepositoryContentGetOptions{
		Ref: grfs.ref,
	})
	
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if path is directory: %w", err)
	}

	return directoryContent != nil, nil
}

func (grfs *githubReadOnlyFileSystem) ListDir(ctx context.Context, dir string) ([]string, error) {
	_, directoryContent, resp, err := grfs.client.Repositories.GetContents(ctx, grfs.owner, grfs.repo, dir, &github.RepositoryContentGetOptions{
		Ref: grfs.ref,
	})
	
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("directory not found: %s", dir)
		}
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	if directoryContent == nil {
		return nil, fmt.Errorf("path is not a directory: %s", dir)
	}

	var files []string
	for _, content := range directoryContent {
		if content.Name != nil {
			files = append(files, *content.Name)
		}
	}

	return files, nil
}