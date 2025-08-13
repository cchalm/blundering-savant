package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrFileNotFound error = fmt.Errorf("file not found")
)

// FileSystem is a basic interface for reading and writing files
type FileSystem interface {
	// Read reads the content of a file at the given path
	Read(ctx context.Context, path string) (string, error)
	// Write writes the content to a file at the given path, creating the file if it doesn't exist
	Write(ctx context.Context, path string, content string) error
	// Delete deletes a file at the given path
	Delete(ctx context.Context, path string) error

	// FileExists returns true if the file at the given path exists, false otherwise
	FileExists(ctx context.Context, path string) (bool, error)

	// IsDir returns true if the given path is a directory, false otherwise
	IsDir(ctx context.Context, dir string) (bool, error)
	// List lists all files in the given directory
	ListDir(ctx context.Context, dir string) ([]string, error)
}

// diffFileSystem sits on top of a FileSystem and tracks changes in-memory
type diffFileSystem struct {
	baseFileSystem FileSystem

	workingTree  map[string]string   // path -> content (files we've modified)
	deletedFiles map[string]struct{} // path -> struct{}{} (files we've deleted)
}

// Read reads a file from the work branch with any in-memory changes applied
func (dfs diffFileSystem) Read(ctx context.Context, path string) (string, error) {
	path = normalizePath(path)

	// Check if file is deleted
	if _, ok := dfs.deletedFiles[path]; ok {
		return "", fmt.Errorf("file is deleted: %w", ErrFileNotFound)
	}

	// Check working tree first
	if content, exists := dfs.workingTree[path]; exists {
		return content, nil
	}

	// Fall back to baseFileSystem
	return dfs.baseFileSystem.Read(ctx, path)
}

// Write writes a file in-memory
func (dfs *diffFileSystem) Write(_ context.Context, path string, content string) error {
	path = normalizePath(path)

	dfs.workingTree[path] = content
	// Remove from deleted files if it was marked as deleted
	delete(dfs.deletedFiles, path)
	return nil
}

// DeleteFile marks a file as deleted in-memory
func (dfs *diffFileSystem) Delete(_ context.Context, path string) error {
	path = normalizePath(path)

	dfs.deletedFiles[path] = struct{}{}
	// Remove from working tree if it was modified
	delete(dfs.workingTree, path)
	return nil
}

// FileExists checks if a file exists in the current state
func (dfs diffFileSystem) FileExists(ctx context.Context, path string) (bool, error) {
	path = normalizePath(path)

	_, err := dfs.Read(ctx, path)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsDir checks if a path is a directory
func (dfs diffFileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	path = normalizePath(path)

	return dfs.baseFileSystem.IsDir(ctx, path)
}

// ListDir lists the contents of a directory
func (dfs diffFileSystem) ListDir(ctx context.Context, dir string) ([]string, error) {
	dir = normalizePath(dir)

	basePaths, err := dfs.baseFileSystem.ListDir(ctx, dir)
	if err != nil {
		return nil, err
	}

	// Move paths into a map for uniqueness
	pathsMap := map[string]struct{}{}

	for _, path := range basePaths {
		// Only keep non-deleted files
		if _, ok := dfs.deletedFiles[path]; !ok {
			pathsMap[path] = struct{}{}
		}
	}

	for path := range dfs.workingTree {
		if _, ok := pathsMap[path]; ok {
			// path is already in the result (file exists in the base file system and is modified)
			continue
		}

		idx := strings.LastIndex(path, "/")
		dirPart := ""
		if idx != -1 {
			dirPart = path[:idx]
		}

		if dirPart == dir {
			// This path is new in the working tree and is in the requested directory, so add it to the result
			pathsMap[path] = struct{}{}
		}
	}

	allPaths := []string{}
	for path := range pathsMap {
		allPaths = append(allPaths, path)
	}
	return allPaths, nil
}

// HasChanges checks if the diffFileSystem has any changes on top of the base file system
func (dfs diffFileSystem) HasChanges() bool {
	return len(dfs.workingTree) > 0 || len(dfs.deletedFiles) > 0
}

type DFSChangelist struct {
	Path    string
	Content *string // nil for delete
}

func (dfs diffFileSystem) GetChangelist() DFSChangelist {
	changes := []FileChange{}
	for path, content := range dfs.workingTree {
		changes = append(changes, FileChange{
			Path:    path,
			Content: &content,
		})
	}
	for path := range dfs.deletedFiles {
		changes = append(changes, FileChange{
			Path:    path,
			Content: nil,
		})
	}
	return changes
}

func normalizePath(path string) string {
	// All file paths are absolute in our simplified file system
	return strings.TrimPrefix(path, "/")
}
