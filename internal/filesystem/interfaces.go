// Package filesystem provides file system abstractions and implementations.
package filesystem

import (
	"context"
	"fmt"
)

var (
	ErrFileNotFound error = fmt.Errorf("file not found")
)

// ReadOnlyFileSystem is a basic interface for reading files
type ReadOnlyFileSystem interface {
	// Read reads the content of a file at the given path
	Read(ctx context.Context, path string) (string, error)

	// FileExists returns true if the file at the given path exists, false otherwise
	FileExists(ctx context.Context, path string) (bool, error)

	// IsDir returns true if the given path is a directory, false otherwise
	IsDir(ctx context.Context, dir string) (bool, error)
	
	// ListDir lists all files in the given directory
	ListDir(ctx context.Context, dir string) ([]string, error)
}

// FileSystem is a basic interface for reading and writing files
type FileSystem interface {
	ReadOnlyFileSystem

	// Write writes the content to a file at the given path, creating the file if it doesn't exist
	Write(ctx context.Context, path string, content string) error
	
	// Delete deletes a file at the given path
	Delete(ctx context.Context, path string) error
}