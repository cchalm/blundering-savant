package main

import (
	"context"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/workspace"
)

// remoteValidationWorkspaceFactory creates instances of remoteValidationWorkspace
type remoteValidationWorkspaceFactory struct {
	githubClient           *github.Client
	validationWorkflowName string
}

func (rvwf remoteValidationWorkspaceFactory) NewWorkspace(ctx context.Context, tsk task.Task) (bot.Workspace, error) {
	return workspace.NewRemoteValidationWorkspace(ctx, rvwf.githubClient, rvwf.validationWorkflowName, tsk)
}
