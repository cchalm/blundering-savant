// Package workspace provides workspace management and validation types.
package workspace

import (
	"context"
)

// ValidationResult represents the outcome of validation
type ValidationResult struct {
	Succeeded bool
	Details   string
}

// FileSystem is a basic interface for reading and writing files
type FileSystem interface {
	Read(ctx context.Context, path string) (string, error)
	Write(ctx context.Context, path string, content string) error
	Delete(ctx context.Context, path string) error
	FileExists(ctx context.Context, path string) (bool, error)
	IsDir(ctx context.Context, dir string) (bool, error)
	ListDir(ctx context.Context, dir string) ([]string, error)
}