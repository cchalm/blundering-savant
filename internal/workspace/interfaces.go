// Package workspace provides workspace management for the bot.
package workspace

import (
	"context"

	"github.com/cchalm/blundering-savant/internal/filesystem"
	"github.com/cchalm/blundering-savant/internal/task"
)

// Workspace represents a working environment for the bot
type Workspace interface {
	// FileSystem returns the file system interface for this workspace
	FileSystem() filesystem.FileSystem

	// ValidateChanges validates the current changes in the workspace
	ValidateChanges(ctx context.Context, commitMessage string) error

	// PublishChangesForReview publishes changes to a review branch and creates/updates a pull request
	PublishChangesForReview(ctx context.Context, pullRequestTitle, pullRequestBody string) error
}

// WorkspaceFactory creates workspaces for tasks
type WorkspaceFactory interface {
	NewWorkspace(ctx context.Context, tsk task.Task) (Workspace, error)
}