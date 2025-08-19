package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"
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
	Workspace    Workspace
	Task         task
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

func (tie ToolInputError) Unwrap() error {
	return tie.cause
}

// Base tool implementation helper
type BaseTool struct {
	Name string
}

// parseInputJSON is a helper to unmarshal tool input
func parseInputJSON(block anthropic.ToolUseBlock, target any) error {
	err := json.Unmarshal(block.Input, target)
	if err != nil {
		err = ToolInputError{cause: err}
	}
	return err
}

// TextEditorTool implements the str_replace_based_edit_tool
type TextEditorTool struct {
	BaseTool
}

// TextEditorInput represents the input for text editor commands
type TextEditorInput struct {
	Command    string `json:"command"`
	Path       string `json:"path"`
	OldStr     string `json:"old_str,omitempty"`
	NewStr     string `json:"new_str,omitempty"`
	FileText   string `json:"file_text,omitempty"`
	ViewRange  []int  `json:"view_range,omitempty"`
	InsertLine int    `json:"insert_line,omitempty"`
}

// NewTextEditorTool creates a new text editor tool
func NewTextEditorTool() *TextEditorTool {
	return &TextEditorTool{
		BaseTool: BaseTool{Name: "str_replace_based_edit_tool"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *TextEditorTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "text_editor_20250429",
		Name: t.Name,
	}
}

// ParseToolUse parses the tool use block into structured input
func (t *TextEditorTool) ParseToolUse(block anthropic.ToolUseBlock) (*TextEditorInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input TextEditorInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the text editor command
func (t *TextEditorTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *TextEditorTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	_, err := t.run(ctx, block, toolCtx, true)
	return err
}

func (t *TextEditorTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	var result string
	switch input.Command {
	case "view":
		if replay {
			// No side effects to replay
			return nil, nil
		}
		result, err = t.executeView(ctx, input, toolCtx.Workspace)
	case "str_replace":
		result, err = t.executeStrReplace(ctx, input, toolCtx.Workspace)
	case "create":
		result, err = t.executeCreate(ctx, input, toolCtx.Workspace)
	case "insert":
		result, err = t.executeInsert(ctx, input, toolCtx.Workspace)
	case "undo_edit":
		result = ""
		err = ToolInputError{fmt.Errorf("undo_edit not supported")}
	default:
		result = ""
		err = ToolInputError{fmt.Errorf("unknown text editor command: %s", input.Command)}
	}

	if err != nil {
		return nil, fmt.Errorf("error running tool: %w", err)
	}
	return &result, nil
}

// Implementation methods for each command
func (t *TextEditorTool) executeView(ctx context.Context, input *TextEditorInput, fs FileSystem) (string, error) {
	if fs == nil {
		return "", fmt.Errorf("file system not initialized")
	}

	isDir, err := fs.IsDir(ctx, input.Path)
	if err != nil {
		return "", fmt.Errorf("error checking path: %w", err)
	}

	if isDir {
		files, err := fs.ListDir(ctx, input.Path)
		if err != nil {
			return "", fmt.Errorf("error listing directory: %w", err)
		}

		result := fmt.Sprintf("Directory contents of %s:\n", input.Path)
		for _, file := range files {
			result += fmt.Sprintf("  %s\n", file)
		}
		return result, nil
	}

	content, err := fs.Read(ctx, input.Path)
	if errors.Is(err, ErrFileNotFound) {
		return "", ToolInputError{err}
	} else if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	if len(input.ViewRange) == 2 {
		startLine := input.ViewRange[0]
		endLine := input.ViewRange[1]

		lines := strings.Split(content, "\n")
		if endLine == -1 {
			endLine = len(lines)
		}

		if startLine < 1 {
			startLine = 1
		}
		if endLine > len(lines) {
			endLine = len(lines)
		}

		var result strings.Builder
		for i := startLine - 1; i < endLine; i++ {
			result.WriteString(fmt.Sprintf("%d: %s\n", i+1, lines[i]))
		}
		return result.String(), nil
	}

	lines := strings.Split(content, "\n")
	var result strings.Builder
	for i, line := range lines {
		result.WriteString(fmt.Sprintf("%d: %s\n", i+1, line))
	}
	return result.String(), nil
}

func (t *TextEditorTool) executeStrReplace(ctx context.Context, input *TextEditorInput, fs FileSystem) (string, error) {
	content, err := fs.Read(ctx, input.Path)
	if errors.Is(err, ErrFileNotFound) {
		return "", ToolInputError{err}
	} else if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	count := strings.Count(content, input.OldStr)
	if count == 0 {
		return "", ToolInputError{fmt.Errorf("old_str not found in file")}
	}
	if count > 1 {
		return "", ToolInputError{fmt.Errorf("old_str found %d times in file, must be unique", count)}
	}

	newContent := strings.Replace(content, input.OldStr, input.NewStr, 1)
	err = fs.Write(ctx, input.Path, newContent)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	return fmt.Sprintf("Successfully replaced text in %s", input.Path), nil
}

func (t *TextEditorTool) executeCreate(ctx context.Context, input *TextEditorInput, fs FileSystem) (string, error) {
	exists, err := fs.FileExists(ctx, input.Path)
	if err != nil {
		return "", fmt.Errorf("error checking file existence: %w", err)
	}
	if exists {
		return "", fmt.Errorf("file already exists: %s", input.Path)
	}

	err = fs.Write(ctx, input.Path, input.FileText)
	if err != nil {
		return "", fmt.Errorf("error creating file: %w", err)
	}

	return fmt.Sprintf("Successfully created file %s", input.Path), nil
}

func (t *TextEditorTool) executeInsert(ctx context.Context, input *TextEditorInput, fs FileSystem) (string, error) {
	content, err := fs.Read(ctx, input.Path)
	if errors.Is(err, ErrFileNotFound) {
		return "", ToolInputError{err}
	} else if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	lines := strings.Split(content, "\n")
	lineNum := input.InsertLine

	if lineNum < 0 || lineNum > len(lines) {
		return "", fmt.Errorf("invalid insert_line: %d", lineNum)
	}

	newLines := strings.Split(input.NewStr, "\n")
	var result []string

	if lineNum == 0 {
		result = append(newLines, lines...)
	} else if lineNum >= len(lines) {
		result = append(lines, newLines...)
	} else {
		result = append(lines[:lineNum], newLines...)
		result = append(result, lines[lineNum:]...)
	}

	newContent := strings.Join(result, "\n")
	err = fs.Write(ctx, input.Path, newContent)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	return fmt.Sprintf("Successfully inserted text at line %d in %s", lineNum, input.Path), nil
}

// ValidateChangesTool implements the validate_changes tool
type ValidateChangesTool struct {
	BaseTool
}

// ValidateChangesInput represents the input for validate_changes
type ValidateChangesInput struct {
	CommitMessage string `json:"commit_message"`
}

// NewValidateChangesTool creates a new create pull request tool
func NewValidateChangesTool() *ValidateChangesTool {
	return &ValidateChangesTool{
		BaseTool: BaseTool{Name: "validate_changes"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *ValidateChangesTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Validate all previous file changes, e.g. run tests and static analysis"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"commit_message": map[string]any{
					"type": "string",
					"description": "Commit message for file changes made since the last call to this tool. May or " +
						"may not be used depending on the implementation, but a non-empty string must be provided",
				},
			},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *ValidateChangesTool) ParseToolUse(block anthropic.ToolUseBlock) (*ValidateChangesInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input ValidateChangesInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the validate changes command
func (t *ValidateChangesTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.CommitMessage == "" {
		return nil, ToolInputError{fmt.Errorf("commit_message is required")}
	}

	// Validate changes, if any
	result, err := toolCtx.Workspace.ValidateChanges(ctx, &input.CommitMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to commit changes: %w", err)
	}

	var msg string
	if !result.Succeeded {
		msg = fmt.Sprintf("Validation failed. Details:\n```\n%s\n```\n", result.Details)
	} else {
		msg = "validation succeeded"
	}
	return &msg, nil
}

func (t *ValidateChangesTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Changes were persisted remotely when they were validated the first time, so we can clear them locally
	toolCtx.Workspace.ClearLocalChanges()
	return nil
}

// PostCommentTool implements the post_comment tool
type PostCommentTool struct {
	BaseTool
}

// PostCommentInput represents the input for post_comment
type PostCommentInput struct {
	CommentType string `json:"comment_type"`
	Body        string `json:"body"`
	InReplyTo   *int64 `json:"in_reply_to,omitempty"`
}

// NewPostCommentTool creates a new post comment tool
func NewPostCommentTool() *PostCommentTool {
	return &PostCommentTool{
		BaseTool: BaseTool{Name: "post_comment"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *PostCommentTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Post a comment to engage in discussion"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"comment_type": map[string]any{
					"type":        "string",
					"enum":        []string{"issue", "pr", "review"},
					"description": "Type of comment to post",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "The comment text (markdown supported)",
				},
				"in_reply_to": map[string]any{
					"type":        "integer",
					"description": "ID of comment being replied to (for review comments only)",
				},
			},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *PostCommentTool) ParseToolUse(block anthropic.ToolUseBlock) (*PostCommentInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input PostCommentInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the post comment command
func (t *PostCommentTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.Body == "" {
		return nil, ToolInputError{fmt.Errorf("body is required")}
	}

	if input.CommentType == "" {
		return nil, ToolInputError{fmt.Errorf("comment_type is required")}
	}

	switch input.CommentType {
	case "issue":
		comment := &github.IssueComment{
			Body: github.Ptr(input.Body),
		}
		_, _, err = toolCtx.GithubClient.Issues.CreateComment(ctx, toolCtx.Task.Issue.owner, toolCtx.Task.Issue.repo, toolCtx.Task.Issue.number, comment)
		if err != nil {
			return nil, err
		}
	case "pr":
		if toolCtx.Task.PullRequest != nil {
			comment := &github.IssueComment{
				Body: github.Ptr(input.Body),
			}
			_, _, err = toolCtx.GithubClient.Issues.CreateComment(ctx, toolCtx.Task.Issue.owner, toolCtx.Task.Issue.repo, toolCtx.Task.PullRequest.number, comment)
			if err != nil {
				return nil, err
			}
		}
	case "review":
		if input.InReplyTo == nil {
			return nil, fmt.Errorf("InReplyTo must be specified for review comments. The bot is currently unable to create top-level review comments")
		}
		_, _, err = toolCtx.GithubClient.PullRequests.CreateCommentInReplyTo(
			ctx,
			toolCtx.Task.Issue.owner,
			toolCtx.Task.Issue.repo,
			toolCtx.Task.PullRequest.number,
			input.Body,
			*input.InReplyTo,
		)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (t *PostCommentTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// No side effects to replay
	return nil
}

// AddReactionTool implements the add_reaction tool
type AddReactionTool struct {
	BaseTool
}

// AddReactionInput represents the input for add_reaction
type AddReactionInput struct {
	CommentID   int64  `json:"comment_id"`
	CommentType string `json:"comment_type"`
	Reaction    string `json:"reaction"`
}

// NewAddReactionTool creates a new add reaction tool
func NewAddReactionTool() *AddReactionTool {
	return &AddReactionTool{
		BaseTool: BaseTool{Name: "add_reaction"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *AddReactionTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Add a reaction to acknowledge or respond to a comment"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"comment_type": map[string]any{
					"type":        "string",
					"enum":        []any{"issue", "PR", "PR review"},
					"description": "Whether this is a comment on an issue, a comment on a PR, or a comment that is part of a PR review",
				},
				"comment_id": map[string]any{
					"type":        "integer",
					"description": "ID of the comment to react to",
				},
				"reaction": map[string]any{
					"type":        "string",
					"enum":        []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"},
					"description": "The reaction emoji to add",
				},
			},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *AddReactionTool) ParseToolUse(block anthropic.ToolUseBlock) (*AddReactionInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input AddReactionInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the add reaction command
func (t *AddReactionTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.CommentID == 0 {
		return nil, ToolInputError{fmt.Errorf("comment_id is required")}
	}

	if input.Reaction == "" {
		return nil, ToolInputError{fmt.Errorf("reaction is required")}
	}

	switch input.CommentType {
	case "issue", "PR":
		_, _, err = toolCtx.GithubClient.Reactions.CreateIssueCommentReaction(ctx, toolCtx.Task.Issue.owner, toolCtx.Task.Issue.repo, input.CommentID, input.Reaction)
		if err != nil {
			return nil, err
		}
	case "PR review":
		_, _, err = toolCtx.GithubClient.Reactions.CreatePullRequestCommentReaction(ctx, toolCtx.Task.Issue.owner, toolCtx.Task.Issue.repo, input.CommentID, input.Reaction)
		if err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func (t *AddReactionTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// No side effects to replay
	return nil
}

type PublishChangesForReviewTool struct {
	BaseTool
}

type PublishChangesForReviewInput struct {
	PullRequestTitle string `json:"pull_request_title"`
	PullRequestBody  string `json:"pull_request_body"`
}

func NewPublishChangesForReviewTool() *PublishChangesForReviewTool {
	return &PublishChangesForReviewTool{
		BaseTool: BaseTool{Name: "publish_changes_for_review"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *PublishChangesForReviewTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Publish changes for review by other developers"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"pull_request_title": map[string]any{
					"type":        "string",
					"description": "Title for the new pull request, if any. Ignored if a pull request already exists",
				},
				"pull_request_body": map[string]any{
					"type":        "string",
					"description": "Description of the solution and what changes were made. Ignored if a pull request already exists",
				},
			},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *PublishChangesForReviewTool) ParseToolUse(block anthropic.ToolUseBlock) (*PublishChangesForReviewInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input PublishChangesForReviewInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the request review command
func (t *PublishChangesForReviewTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if toolCtx.Task.PullRequest == nil {
		if input.PullRequestTitle == "" {
			return nil, ToolInputError{fmt.Errorf("a new pull request will be created, so pull_request_title is required")}
		}

		if input.PullRequestBody == "" {
			return nil, ToolInputError{fmt.Errorf("a new pull request will be created, so pull_request_body is required")}
		}
	}

	err = toolCtx.Workspace.PublishChangesForReview(ctx, input.PullRequestTitle, input.PullRequestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to publish changes: %w", err)
	}

	return nil, err
}

func (t *PublishChangesForReviewTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Changes were published from one remote location to another by the original invocation of this tool, so there is
	// nothing to do here
	return nil
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
	registry.Register(NewTextEditorTool())
	registry.Register(NewPostCommentTool())
	registry.Register(NewAddReactionTool())
	registry.Register(NewValidateChangesTool())
	registry.Register(NewPublishChangesForReviewTool())

	return registry
}

// Register adds a tool to the registry
func (r *ToolRegistry) Register(tool AnthropicTool) {
	param := tool.GetToolParam()
	r.tools[param.Name] = tool
}

// GetTool retrieves a tool by name
func (r *ToolRegistry) GetTool(name string) (AnthropicTool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// GetAllToolParams returns all tool parameters for use with the API
func (r *ToolRegistry) GetAllToolParams() []anthropic.ToolParam {
	var params []anthropic.ToolParam
	for _, tool := range r.tools {
		params = append(params, tool.GetToolParam())
	}
	return params
}

// ProcessToolUse processes a tool use block with the appropriate tool
func (r *ToolRegistry) ProcessToolUse(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*anthropic.ToolResultBlockParam, error) {
	tool, ok := r.GetTool(block.Name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", block.Name)
	}

	response, err := tool.Run(ctx, block, toolCtx)

	var resultBlock anthropic.ToolResultBlockParam
	var tie ToolInputError
	if errors.As(err, &tie) {
		// Respond to with an error result block to give the AI the opportunity to correct the inputs
		resultBlock = newToolResultBlockParam(block.ID, tie.Error(), true)
		log.Print("Warning: recoverable tool error, reporting to the AI to give it an opportunity to retry")
	} else if err != nil {
		return nil, fmt.Errorf("error while running tool: %w", err)
	} else if response != nil {
		resultBlock = newToolResultBlockParam(block.ID, *response, false)
	} else {
		resultBlock = newToolResultBlockParam(block.ID, "", false)
	}
	return &resultBlock, nil
}

// ReplayToolUse replays a tool use block with the appropriate tool
func (r *ToolRegistry) ReplayToolUse(ctx context.Context, toolUseBlock anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	tool, ok := r.GetTool(toolUseBlock.Name)
	if !ok {
		return fmt.Errorf("unknown tool: %s", toolUseBlock.Name)
	}

	err := tool.Replay(ctx, toolUseBlock, toolCtx)

	var tie ToolInputError
	if errors.As(err, &tie) {
		// If the error is an input issue, the reporting of that error is already in the conversation history, so there
		// is no need to repeat it. Do nothing
	} else if err != nil {
		return fmt.Errorf("error while running tool: %w", err)
	}
	return nil
}

// Helper function to create a ToolResultBlockParam, in contrast to anthropic.NewToolResultBlockParam which creates a
// ContentBlockParamUnion
func newToolResultBlockParam(toolID string, result string, isError bool) anthropic.ToolResultBlockParam {
	return anthropic.ToolResultBlockParam{
		ToolUseID: toolID,
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: result}},
		},
		IsError: anthropic.Bool(isError),
	}
}
