// Package tools provides the AI tool system and implementations.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/workspace"
)

// AnthropicTool defines the interface for all tools
type AnthropicTool interface {
	// GetToolParam creates and returns an anthropic.ToolParam defining the tool
	GetToolParam() anthropic.ToolParam

	// Run takes a ToolUseBlock, performs the tool call, and returns a string result or an error. The error will be a
	// ToolInputError if it is recoverable by fixing inputs. A call to Run has no side effects if it returns
	// ToolInputError
	Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error)

	// Replay is the same as Run, except that it skips actions with persistent side effects, e.g. pushing git commits to
	// a remote. Persistent side effects also include anything persisted in the conversation, e.g. fetching the content
	// of a file.
	// Call this to restore local state changes of a previous tool call in a new environment.
	// Note that this function does not return a string, because a response should already have been added to the
	// conversation from the original run of this tool.
	Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error
}

// ToolContext provides context needed by tools during execution
type ToolContext struct {
	Workspace    workspace.Workspace
	Task         task.Task
	GithubClient *github.Client
}

// ToolInputError represents an error that could be recovered by correcting inputs to the tool. This error will be
// uploaded to the AI, so it must not contain any sensitive information
type ToolInputError struct {
	cause error
}

func (tie ToolInputError) Error() string {
	return fmt.Sprintf("tool input error: %s", tie.cause)
}

func NewToolInputError(cause error) ToolInputError {
	return ToolInputError{cause: cause}
}

// ToolRegistry manages all available tools
type ToolRegistry struct {
	tools map[string]AnthropicTool
}

// NewToolRegistry creates a new tool registry with all available tools
func NewToolRegistry() *ToolRegistry {
	registry := &ToolRegistry{
		tools: make(map[string]AnthropicTool),
	}

	// Register all tools
	registry.registerTool("str_replace_based_edit_tool", &StrReplaceBasedEditTool{})
	registry.registerTool("delete_file", &DeleteFileTool{})
	registry.registerTool("post_comment", &PostCommentTool{})
	registry.registerTool("add_reaction", &AddReactionTool{})
	registry.registerTool("validate_changes", &ValidateChangesTool{})
	registry.registerTool("publish_changes_for_review", &PublishChangesForReviewTool{})
	registry.registerTool("report_limitation", &ReportLimitationTool{})

	return registry
}

func (tr *ToolRegistry) registerTool(name string, tool AnthropicTool) {
	tr.tools[name] = tool
}

// GetTool returns a tool by name
func (tr *ToolRegistry) GetTool(name string) AnthropicTool {
	return tr.tools[name]
}

// GetToolParams returns all tool parameters for API calls
func (tr *ToolRegistry) GetToolParams() []anthropic.ToolParam {
	var params []anthropic.ToolParam
	for _, tool := range tr.tools {
		params = append(params, tool.GetToolParam())
	}
	return params
}