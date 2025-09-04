package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/workspace"
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
		return nil, fmt.Errorf("error running command '%s': %w", input.Command, err)
	}
	return &result, nil
}

// Implementation methods for each command
func (t *TextEditorTool) executeView(ctx context.Context, input *TextEditorInput, fs workspace.FileSystem) (string, error) {
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
	if errors.Is(err, workspace.ErrFileNotFound) {
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

func (t *TextEditorTool) executeStrReplace(ctx context.Context, input *TextEditorInput, fs workspace.FileSystem) (string, error) {
	content, err := fs.Read(ctx, input.Path)
	if errors.Is(err, workspace.ErrFileNotFound) {
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

func (t *TextEditorTool) executeCreate(ctx context.Context, input *TextEditorInput, fs workspace.FileSystem) (string, error) {
	exists, err := fs.FileExists(ctx, input.Path)
	if err != nil {
		return "", fmt.Errorf("error checking file existence: %w", err)
	}
	if exists {
		return "", ToolInputError{fmt.Errorf("file already exists: %s", input.Path)}
	}

	err = fs.Write(ctx, input.Path, input.FileText)
	if err != nil {
		return "", fmt.Errorf("error creating file: %w", err)
	}

	return fmt.Sprintf("Successfully created file %s", input.Path), nil
}

func (t *TextEditorTool) executeInsert(ctx context.Context, input *TextEditorInput, fs workspace.FileSystem) (string, error) {
	content, err := fs.Read(ctx, input.Path)
	if errors.Is(err, workspace.ErrFileNotFound) {
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
		_, _, err = toolCtx.GithubClient.Issues.CreateComment(ctx, toolCtx.Task.Issue.Owner, toolCtx.Task.Issue.Repo, toolCtx.Task.Issue.Number, comment)
		if err != nil {
			return nil, err
		}
	case "pr":
		if toolCtx.Task.PullRequest != nil {
			comment := &github.IssueComment{
				Body: github.Ptr(input.Body),
			}
			_, _, err = toolCtx.GithubClient.Issues.CreateComment(ctx, toolCtx.Task.Issue.Owner, toolCtx.Task.Issue.Repo, toolCtx.Task.PullRequest.Number, comment)
			if err != nil {
				return nil, err
			}
		}
	case "review":
		if input.InReplyTo == nil {
			return nil, ToolInputError{fmt.Errorf("InReplyTo must be specified for review comments. The bot is currently unable to create top-level review comments")}
		}
		_, _, err = toolCtx.GithubClient.PullRequests.CreateCommentInReplyTo(
			ctx,
			toolCtx.Task.Issue.Owner,
			toolCtx.Task.Issue.Repo,
			toolCtx.Task.PullRequest.Number,
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
		_, _, err = toolCtx.GithubClient.Reactions.CreateIssueCommentReaction(ctx, toolCtx.Task.Issue.Owner, toolCtx.Task.Issue.Repo, input.CommentID, input.Reaction)
		if err != nil {
			return nil, err
		}
	case "PR review":
		_, _, err = toolCtx.GithubClient.Reactions.CreatePullRequestCommentReaction(ctx, toolCtx.Task.Issue.Owner, toolCtx.Task.Issue.Repo, input.CommentID, input.Reaction)
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

// DeleteFileTool implements the delete_file tool
type DeleteFileTool struct {
	BaseTool
}

// DeleteFileInput represents the input for delete_file
type DeleteFileInput struct {
	Path string `json:"path"`
}

// NewDeleteFileTool creates a new delete file tool
func NewDeleteFileTool() *DeleteFileTool {
	return &DeleteFileTool{
		BaseTool: BaseTool{Name: "delete_file"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *DeleteFileTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Delete a file"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to delete.",
				},
			},
			Required: []string{"path"},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *DeleteFileTool) ParseToolUse(block anthropic.ToolUseBlock) (*DeleteFileInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input DeleteFileInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the delete file command
func (t *DeleteFileTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.Path == "" {
		return nil, ToolInputError{fmt.Errorf("path is required")}
	}

	// Validate that the path doesn't start with a leading slash
	if strings.HasPrefix(input.Path, "/") {
		return nil, ToolInputError{fmt.Errorf("path must be relative (no leading slash)")}
	}

	// Check if the file exists before deleting
	exists, err := toolCtx.Workspace.FileExists(ctx, input.Path)
	if err != nil {
		return nil, fmt.Errorf("error checking if file exists: %w", err)
	}
	if !exists {
		return nil, ToolInputError{fmt.Errorf("file does not exist: %s", input.Path)}
	}

	// Check if it's a directory
	isDir, err := toolCtx.Workspace.IsDir(ctx, input.Path)
	if err != nil {
		return nil, fmt.Errorf("error checking if path is directory: %w", err)
	}
	if isDir {
		return nil, ToolInputError{fmt.Errorf("cannot delete directory: %s (only files can be deleted)", input.Path)}
	}

	// Delete the file
	err = toolCtx.Workspace.Delete(ctx, input.Path)
	if err != nil {
		return nil, fmt.Errorf("error deleting file: %w", err)
	}

	result := fmt.Sprintf("Successfully deleted file: %s", input.Path)
	return &result, nil
}

func (t *DeleteFileTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return fmt.Errorf("error parsing input: %w", err)
	}

	// Replay the deletion (same as the original run since it's an in-memory operation)
	return toolCtx.Workspace.Delete(ctx, input.Path)
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

	if toolCtx.Workspace.HasLocalChanges() {
		return nil, ToolInputError{fmt.Errorf("cannot publish while there are unvalidated changes in the workspace")}
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

// SearchInFileTool implements the search_in_file tool
type SearchInFileTool struct {
	BaseTool
}

// SearchInFileInput represents the input for search_in_file
type SearchInFileInput struct {
	FilePath      string `json:"file_path"`
	Query         string `json:"query"`
	UseRegex      bool   `json:"use_regex,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	ContextLines  int    `json:"context_lines,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
}

// SearchResult represents a single search result
type SearchResult struct {
	FilePath      string
	LineNumber    int
	Line          string
	ContextBefore []string
	ContextAfter  []string
}

// NewSearchInFileTool creates a new search in file tool
func NewSearchInFileTool() *SearchInFileTool {
	return &SearchInFileTool{
		BaseTool: BaseTool{Name: "search_in_file"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *SearchInFileTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Search for text within a specific file, with regex support and context lines"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Path to the file to search in",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "The text or pattern to search for",
				},
				"use_regex": map[string]any{
					"type":        "boolean",
					"description": "Whether to treat the query as a regular expression (default: false)",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default: 50, max: 200)",
				},
				"context_lines": map[string]any{
					"type":        "integer",
					"description": "Number of lines before and after each match to include as context (default: 2, max: 10)",
				},
				"case_sensitive": map[string]any{
					"type":        "boolean",
					"description": "Whether the search should be case sensitive (default: false)",
				},
			},
			Required: []string{"file_path", "query"},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *SearchInFileTool) ParseToolUse(block anthropic.ToolUseBlock) (*SearchInFileInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input SearchInFileInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the search in file command
func (t *SearchInFileTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *SearchInFileTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Search is read-only, no side effects to replay
	return nil
}

func (t *SearchInFileTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	if replay {
		// Search is read-only, no side effects to replay
		return nil, nil
	}

	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.FilePath == "" {
		return nil, ToolInputError{fmt.Errorf("file_path is required")}
	}

	if input.Query == "" {
		return nil, ToolInputError{fmt.Errorf("query is required")}
	}

	// Validate that the path doesn't start with a leading slash
	if strings.HasPrefix(input.FilePath, "/") {
		return nil, ToolInputError{fmt.Errorf("file_path must be relative (no leading slash)")}
	}

	// Set defaults
	if input.MaxResults <= 0 {
		input.MaxResults = 50
	}
	if input.MaxResults > 200 {
		input.MaxResults = 200
	}
	if input.ContextLines < 0 {
		input.ContextLines = 2
	}
	if input.ContextLines > 10 {
		input.ContextLines = 10
	}

	// Check if file exists
	exists, err := toolCtx.Workspace.FileExists(ctx, input.FilePath)
	if err != nil {
		return nil, fmt.Errorf("error checking if file exists: %w", err)
	}
	if !exists {
		return nil, ToolInputError{fmt.Errorf("file does not exist: %s", input.FilePath)}
	}

	// Check if it's actually a file (not a directory)
	isDir, err := toolCtx.Workspace.IsDir(ctx, input.FilePath)
	if err != nil {
		return nil, fmt.Errorf("error checking if path is directory: %w", err)
	}
	if isDir {
		return nil, ToolInputError{fmt.Errorf("path is a directory, not a file: %s", input.FilePath)}
	}

	// Read file content
	content, err := toolCtx.Workspace.Read(ctx, input.FilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	results, err := t.searchInFile(input.FilePath, content, input)
	if err != nil {
		return nil, fmt.Errorf("error searching file: %w", err)
	}

	output := t.formatResults(results, input)
	return &output, nil
}

// searchInFile searches for the query within the file content
func (t *SearchInFileTool) searchInFile(filePath, content string, input *SearchInFileInput) ([]SearchResult, error) {
	var results []SearchResult
	var searchRegex *regexp.Regexp
	var err error

	// Compile regex if needed
	if input.UseRegex {
		query := input.Query
		if !input.CaseSensitive {
			query = "(?i)" + query
		}
		searchRegex, err = regexp.Compile(query)
		if err != nil {
			return nil, ToolInputError{fmt.Errorf("invalid regular expression: %w", err)}
		}
	}

	lines := strings.Split(content, "\n")
	resultCount := 0

	for lineNum, line := range lines {
		if resultCount >= input.MaxResults {
			break
		}

		var matches bool

		if input.UseRegex && searchRegex != nil {
			matches = searchRegex.MatchString(line)
		} else {
			// Simple string matching
			searchQuery := input.Query
			searchLine := line
			if !input.CaseSensitive {
				searchQuery = strings.ToLower(searchQuery)
				searchLine = strings.ToLower(line)
			}
			matches = strings.Contains(searchLine, searchQuery)
		}

		if matches {
			result := SearchResult{
				FilePath:   filePath,
				LineNumber: lineNum + 1, // 1-indexed
				Line:       line,
			}

			// Add context lines
			if input.ContextLines > 0 {
				// Context before
				start := lineNum - input.ContextLines
				if start < 0 {
					start = 0
				}
				for i := start; i < lineNum; i++ {
					result.ContextBefore = append(result.ContextBefore, lines[i])
				}

				// Context after
				end := lineNum + input.ContextLines + 1
				if end > len(lines) {
					end = len(lines)
				}
				for i := lineNum + 1; i < end; i++ {
					result.ContextAfter = append(result.ContextAfter, lines[i])
				}
			}

			results = append(results, result)
			resultCount++
		}
	}

	return results, nil
}

// formatResults formats the search results into a readable string
func (t *SearchInFileTool) formatResults(results []SearchResult, input *SearchInFileInput) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for query '%s' in file: %s", input.Query, input.FilePath)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d result(s) for query '%s' in file: %s\n\n", len(results), input.Query, input.FilePath))

	for i, result := range results {
		if i > 0 {
			output.WriteString("\n")
		}

		output.WriteString(fmt.Sprintf("**Line %d:**\n", result.LineNumber))

		// Add context before
		for _, contextLine := range result.ContextBefore {
			output.WriteString(fmt.Sprintf("  %s\n", contextLine))
		}

		// Add the matching line (highlighted)
		output.WriteString(fmt.Sprintf("→ %s\n", result.Line))

		// Add context after
		for _, contextLine := range result.ContextAfter {
			output.WriteString(fmt.Sprintf("  %s\n", contextLine))
		}
	}

	if len(results) >= input.MaxResults {
		output.WriteString(fmt.Sprintf("\n(Results limited to %d matches)\n", input.MaxResults))
	}

	return output.String()
}



// ReportLimitationTool implements the report_limitation tool
type ReportLimitationTool struct {
	BaseTool
}

// ReportLimitationInput represents the input for report_limitation
type ReportLimitationInput struct {
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	Suggestions string `json:"suggestions,omitempty"`
}

// NewReportLimitationTool creates a new report limitation tool
func NewReportLimitationTool() *ReportLimitationTool {
	return &ReportLimitationTool{
		BaseTool: BaseTool{Name: "report_limitation"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *ReportLimitationTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Report when you need to perform an action that you don't have a tool for. Use this instead of trying workarounds with available tools."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "The action you want to perform but don't have a tool for",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Why this action is needed to complete the task",
				},
				"suggestions": map[string]any{
					"type":        "string",
					"description": "Optional suggestions for how this limitation could be addressed or alternative approaches",
				},
			},
			Required: []string{"action", "reason"},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *ReportLimitationTool) ParseToolUse(block anthropic.ToolUseBlock) (*ReportLimitationInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input ReportLimitationInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the report limitation command
func (t *ReportLimitationTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.Action == "" {
		return nil, ToolInputError{fmt.Errorf("action is required")}
	}

	if input.Reason == "" {
		return nil, ToolInputError{fmt.Errorf("reason is required")}
	}

	// Create a formatted limitation report
	var report strings.Builder
	report.WriteString("## Tool Limitation Report\n\n")
	report.WriteString(fmt.Sprintf("**Action needed:** %s\n\n", input.Action))
	report.WriteString(fmt.Sprintf("**Reason:** %s\n\n", input.Reason))

	if input.Suggestions != "" {
		report.WriteString(fmt.Sprintf("**Suggestions:** %s\n\n", input.Suggestions))
	}

	report.WriteString("This action cannot be performed with the currently available tools. ")
	report.WriteString("Human intervention or additional tool support may be required.")

	// Post the limitation report as a comment on the issue
	comment := &github.IssueComment{
		Body: github.Ptr(report.String()),
	}
	_, _, err = toolCtx.GithubClient.Issues.CreateComment(ctx, toolCtx.Task.Issue.Owner, toolCtx.Task.Issue.Repo, toolCtx.Task.Issue.Number, comment)
	if err != nil {
		return nil, fmt.Errorf("failed to post limitation report: %w", err)
	}

	result := fmt.Sprintf("Posted limitation report for action: %s", input.Action)
	return &result, nil
}

func (t *ReportLimitationTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// No side effects to replay - the comment was already posted
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
	registry.Register(NewDeleteFileTool())
	registry.Register(NewPostCommentTool())
	registry.Register(NewAddReactionTool())
	registry.Register(NewValidateChangesTool())
	registry.Register(NewPublishChangesForReviewTool())
	registry.Register(NewSearchInFileTool())
	registry.Register(NewReportLimitationTool())

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
		// If the error is an input issue, one of two things has probably happened:
		// - The original call had an input issue, in which case that error was reported to the bot and is already in
		//   the conversation history
		// - The original call was successful but repeating it produces an expected error (e.g. cannot create file that
		//   already exists)
		// In either case, there is no need to do anything
	} else if err != nil {
		return fmt.Errorf("error while replaying tool: %w", err)
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
