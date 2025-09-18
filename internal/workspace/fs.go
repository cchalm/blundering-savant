package workspace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v72/github"
)

var (
	ErrFileNotFound error = fmt.Errorf("file not found")
	ErrIsFile       error = fmt.Errorf("path is a file")
	ErrIsDir        error = fmt.Errorf("path is a directory")
)

// ReadOnlyFileSystem is a basic interface for reading files
type ReadOnlyFileSystem interface {
	// Read reads the content of a file at the given path. Returns ErrIsDir if the given path is a directory
	Read(ctx context.Context, path string) (string, error)

	// FileExists returns true if the file at the given path exists, false otherwise. Returns false if the given path is
	// a directory
	FileExists(ctx context.Context, path string) (bool, error)

	// IsDir returns true if the given path is a directory, false otherwise. Returns false if the given path is a file
	IsDir(ctx context.Context, dir string) (bool, error)
	// List lists the fully-qualified paths of all files in the given directory. Returns ErrIsFile if the given path is
	// a file
	ListDir(ctx context.Context, dir string) ([]string, error)
}

// FileSystem is a basic interface for reading and writing files
type FileSystem interface {
	ReadOnlyFileSystem

	// Write writes the content to a file at the given path, creating the file if it doesn't exist
	Write(ctx context.Context, path string, content string) error
	// Delete deletes a file at the given path. Returns ErrIsDir if the path is a directory. Returns ErrFileNotFound if
	// no such file exists
	Delete(ctx context.Context, path string) error
}

// memDiffFileSystem sits on top of a ReadOnlyFileSystem and tracks changes in-memory
type memDiffFileSystem struct {
	baseFileSystem ReadOnlyFileSystem

	workingTree  map[string]string   // path -> content (files we've modified)
	deletedFiles map[string]struct{} // path -> struct{}{} (files we've deleted)
}

func NewMemDiffFileSystem(baseFileSystem ReadOnlyFileSystem) memDiffFileSystem {
	return memDiffFileSystem{
		baseFileSystem: baseFileSystem,
		workingTree:    map[string]string{},
		deletedFiles:   map[string]struct{}{},
	}
}

// Read reads a file from the work branch with any in-memory changes applied
func (dfs memDiffFileSystem) Read(ctx context.Context, path string) (string, error) {
	// Check if file is deleted
	if _, ok := dfs.deletedFiles[path]; ok {
		return "", fmt.Errorf("file is deleted: %w", ErrFileNotFound)
	}

	// Check working tree
	if content, exists := dfs.workingTree[path]; exists {
		return content, nil
	}

	// Fall back to baseFileSystem
	return dfs.baseFileSystem.Read(ctx, path)
}

// Write writes a file in-memory
func (dfs *memDiffFileSystem) Write(_ context.Context, path string, content string) error {
	// Note some limitations of this file system: directories can be implicitly created via calls like
	// Write("dir1/dir2/file.txt", ...), but these directories cannot be read from the in-memory diff

	dfs.workingTree[path] = content
	// Remove from deleted files if it was marked as deleted
	delete(dfs.deletedFiles, path)
	return nil
}

// DeleteFile marks a file as deleted in-memory
func (dfs *memDiffFileSystem) Delete(ctx context.Context, path string) error {
	if exists, err := dfs.FileExists(ctx, path); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	} else if !exists {
		return ErrFileNotFound
	}

	dfs.deletedFiles[path] = struct{}{}
	// Remove from working tree if it was modified
	delete(dfs.workingTree, path)
	return nil
}

// FileExists checks if a file exists in the current state
func (dfs memDiffFileSystem) FileExists(ctx context.Context, path string) (bool, error) {
	// Check if file is deleted
	if _, ok := dfs.deletedFiles[path]; ok {
		return false, nil
	}

	// Check working tree
	if _, exists := dfs.workingTree[path]; exists {
		return true, nil
	}

	// Fall back to baseFileSystem
	return dfs.baseFileSystem.FileExists(ctx, path)
}

// IsDir checks if a path is a directory
func (dfs memDiffFileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	return dfs.baseFileSystem.IsDir(ctx, path)
}

// ListDir lists the contents of a directory
func (dfs memDiffFileSystem) ListDir(ctx context.Context, dir string) ([]string, error) {
	// Check working tree for a file with this path
	if _, exists := dfs.workingTree[dir]; exists {
		return nil, ErrIsFile
	}

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
func (dfs memDiffFileSystem) HasChanges() bool {
	return len(dfs.workingTree) > 0 || len(dfs.deletedFiles) > 0
}

func (dfs *memDiffFileSystem) Reset() {
	dfs.workingTree = map[string]string{}
	dfs.deletedFiles = map[string]struct{}{}
}

type MemChangelist struct {
	modified map[string]string
	deleted  map[string]struct{}
}

func (mc MemChangelist) ForEachModified(fn func(path string, content string) error) error {
	for path, content := range mc.modified {
		err := fn(path, content)
		if err != nil {
			return fmt.Errorf("error while handling modified file '%s': %w", path, err)
		}
	}
	return nil
}

func (mc MemChangelist) ForEachDeleted(fn func(path string) error) error {
	for path := range mc.deleted {
		err := fn(path)
		if err != nil {
			return fmt.Errorf("error while handling deleted file '%s': %w", path, err)
		}
	}
	return nil
}

func (mc MemChangelist) IsModified(path string) bool {
	_, ok := mc.modified[path]
	return ok
}

func (mc MemChangelist) IsDeleted(path string) bool {
	_, ok := mc.deleted[path]
	return ok
}

func (mc MemChangelist) IsEmpty() bool {
	return len(mc.modified) == 0 && len(mc.deleted) == 0
}

func (dfs memDiffFileSystem) GetChangelist() MemChangelist {
	return MemChangelist{
		modified: dfs.workingTree,
		deleted:  dfs.deletedFiles,
	}
}

// GithubFileSystem provides a read-only view into the contents of a particular branch of a GitHub repository
type GithubFileSystem struct {
	repos  *github.RepositoriesService
	owner  string
	repo   string
	branch string
}

func NewGithubFileSystem(repos *github.RepositoriesService, owner string, repo string, branch string) GithubFileSystem {
	return GithubFileSystem{
		repos:  repos,
		owner:  owner,
		repo:   repo,
		branch: branch,
	}
}

// Read reads the content of a file at the given path
func (gfs GithubFileSystem) Read(ctx context.Context, path string) (string, error) {
	fileContent, dirContent, resp, err := gfs.repos.GetContents(ctx, gfs.owner, gfs.repo, path, &github.RepositoryContentGetOptions{
		Ref: gfs.branch,
	})
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return "", ErrFileNotFound
		}
		return "", fmt.Errorf("failed to get file contents: %w", err)
	}

	if fileContent == nil {
		if dirContent != nil {
			return "", fmt.Errorf("expected file: %w", ErrIsDir)
		}
		return "", fmt.Errorf("file content nil")
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

// FileExists returns true if the file at the given path exists, false otherwise
func (gfs GithubFileSystem) FileExists(ctx context.Context, path string) (bool, error) {
	_, err := gfs.Read(ctx, path)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return false, nil
		} else if errors.Is(err, ErrIsDir) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if file '%s' exists: %w", path, err)
	}
	return true, nil
}

// IsDir returns true if the given path is a directory, false otherwise
func (gfs GithubFileSystem) IsDir(ctx context.Context, dir string) (bool, error) {
	_, err := gfs.ListDir(ctx, dir)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return false, nil
		} else if errors.Is(err, ErrIsFile) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if path '%s' is a directory: %w", dir, err)
	}
	return true, nil
}

// List lists all files in the given directory
func (gfs GithubFileSystem) ListDir(ctx context.Context, dir string) ([]string, error) {
	fileContent, dirContents, resp, err := gfs.repos.GetContents(ctx, gfs.owner, gfs.repo, dir, &github.RepositoryContentGetOptions{
		Ref: gfs.branch,
	})
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}
	if fileContent != nil {
		return nil, fmt.Errorf("expected directory: %w", ErrIsFile)
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
