// Package git provides Git operations and abstractions.
package git

import (
	"context"

	"github.com/google/go-github/v72/github"
)

// Changelist represents a set of changes to be committed
type Changelist interface {
	ForEachModified(fn func(path string, content string) error) error
	ForEachDeleted(fn func(path string) error) error
	IsModified(path string) bool
	IsDeleted(path string) bool
	IsEmpty() bool
}

// GitRepo provides Git repository operations
type GitRepo interface {
	CreateBranch(ctx context.Context, baseBranch string, newBranch string) error
	CommitChanges(ctx context.Context, branch string, changelist Changelist, commitMessage string) (*github.Commit, error)
	Merge(ctx context.Context, baseBranch string, targetBranch string) (*github.Commit, error)
	CompareCommits(ctx context.Context, base string, head string) (*github.CommitsComparison, error)
}