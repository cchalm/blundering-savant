package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/filesystem"
)

// parseInputJSON is a helper to unmarshal tool input
func parseInputJSON(block anthropic.ToolUseBlock, target any) error {
	err := json.Unmarshal(block.Input, target)
	if err != nil {
		err = NewToolInputError(err)
	}
	return err
}

// StrReplaceBasedEditTool implements the str_replace_based_edit_tool
type StrReplaceBasedEditTool struct{}

type TextEditorInput struct {
	Command    string `json:"command"`
	Path       string `json:"path"`
	OldStr     string `json:"old_str,omitempty"`
	NewStr     string `json:"new_str,omitempty"`
	FileText   string `json:"file_text,omitempty"`
	ViewRange  []int  `json:"view_range,omitempty"`
	InsertLine int    `json:"insert_line,omitempty"`
	InsertText string `json:"insert_text,omitempty"`
}

func (t *StrReplaceBasedEditTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "text_editor_20250429",
		Name: "str_replace_based_edit_tool",
	}
}

func (t *StrReplaceBasedEditTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *StrReplaceBasedEditTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	_, err := t.run(ctx, block, toolCtx, true)
	return err
}

func (t *StrReplaceBasedEditTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	var input TextEditorInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	fs := toolCtx.Workspace.FileSystem()

	switch input.Command {
	case "view":
		if replay {
			return nil, nil // No side effects to replay
		}
		return t.executeView(ctx, input, fs)
	case "str_replace":
		return t.executeStrReplace(ctx, input, fs)
	case "create":
		return t.executeCreate(ctx, input, fs)
	case "insert":
		return t.executeInsert(ctx, input, fs)
	default:
		return nil, NewToolInputError(fmt.Errorf("unknown command: %s", input.Command))
	}
}

func (t *StrReplaceBasedEditTool) executeView(ctx context.Context, input TextEditorInput, fs filesystem.FileSystem) (*string, error) {
	if input.Path == "" {
		return nil, NewToolInputError(fmt.Errorf("path is required for view command"))
	}

	// Check if it's a directory
	isDir, err := fs.IsDir(ctx, input.Path)
	if err == nil && isDir {
		files, err := fs.ListDir(ctx, input.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to list directory: %w", err)
		}
		
		var result strings.Builder
		result.WriteString(fmt.Sprintf("Directory contents of %s:\n", input.Path))
		for _, file := range files {
			result.WriteString(fmt.Sprintf("  %s\n", file))
		}
		
		resultStr := result.String()
		return &resultStr, nil
	}

	// Try to read as file
	content, err := fs.Read(ctx, input.Path)
	if err != nil {
		if err == filesystem.ErrFileNotFound {
			return nil, NewToolInputError(fmt.Errorf("file not found: %s", input.Path))
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(content, "\n")
	
	// Handle view range
	start, end := 1, len(lines)
	if len(input.ViewRange) >= 2 {
		start = input.ViewRange[0]
		end = input.ViewRange[1]
		if end == -1 {
			end = len(lines)
		}
	}

	// Validate range
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return nil, NewToolInputError(fmt.Errorf("invalid view range: start %d > end %d", start, end))
	}

	var result strings.Builder
	for i := start - 1; i < end; i++ {
		result.WriteString(fmt.Sprintf("%d: %s\n", i+1, lines[i]))
	}

	resultStr := result.String()
	return &resultStr, nil
}

func (t *StrReplaceBasedEditTool) executeStrReplace(ctx context.Context, input TextEditorInput, fs filesystem.FileSystem) (*string, error) {
	if input.Path == "" {
		return nil, NewToolInputError(fmt.Errorf("path is required for str_replace command"))
	}
	if input.OldStr == "" {
		return nil, NewToolInputError(fmt.Errorf("old_str is required for str_replace command"))
	}

	content, err := fs.Read(ctx, input.Path)
	if err != nil {
		if err == filesystem.ErrFileNotFound {
			return nil, NewToolInputError(fmt.Errorf("file not found: %s", input.Path))
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Check if old_str exists and is unique
	count := strings.Count(content, input.OldStr)
	if count == 0 {
		return nil, NewToolInputError(fmt.Errorf("old_str not found in file"))
	}
	if count > 1 {
		return nil, NewToolInputError(fmt.Errorf("old_str appears %d times in file, must be unique", count))
	}

	// Replace the string
	newContent := strings.Replace(content, input.OldStr, input.NewStr, 1)

	if err := fs.Write(ctx, input.Path, newContent); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	result := fmt.Sprintf("File %s updated successfully", input.Path)
	return &result, nil
}

func (t *StrReplaceBasedEditTool) executeCreate(ctx context.Context, input TextEditorInput, fs filesystem.FileSystem) (*string, error) {
	if input.Path == "" {
		return nil, NewToolInputError(fmt.Errorf("path is required for create command"))
	}
	if input.FileText == "" {
		return nil, NewToolInputError(fmt.Errorf("file_text is required for create command"))
	}

	if err := fs.Write(ctx, input.Path, input.FileText); err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	result := fmt.Sprintf("File %s created successfully", input.Path)
	return &result, nil
}

func (t *StrReplaceBasedEditTool) executeInsert(ctx context.Context, input TextEditorInput, fs filesystem.FileSystem) (*string, error) {
	if input.Path == "" {
		return nil, NewToolInputError(fmt.Errorf("path is required for insert command"))
	}
	if input.InsertText == "" {
		return nil, NewToolInputError(fmt.Errorf("insert_text is required for insert command"))
	}

	content, err := fs.Read(ctx, input.Path)
	if err != nil {
		if err == filesystem.ErrFileNotFound {
			return nil, NewToolInputError(fmt.Errorf("file not found: %s", input.Path))
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(content, "\n")
	
	// Validate insert line
	if input.InsertLine < 0 || input.InsertLine > len(lines) {
		return nil, NewToolInputError(fmt.Errorf("invalid insert_line: %d (file has %d lines)", input.InsertLine, len(lines)))
	}

	// Insert the text
	var newLines []string
	newLines = append(newLines, lines[:input.InsertLine]...)
	newLines = append(newLines, input.InsertText)
	newLines = append(newLines, lines[input.InsertLine:]...)

	newContent := strings.Join(newLines, "\n")

	if err := fs.Write(ctx, input.Path, newContent); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	result := fmt.Sprintf("Text inserted at line %d in %s", input.InsertLine, input.Path)
	return &result, nil
}

// DeleteFileTool implements the delete_file tool
type DeleteFileTool struct{}

type DeleteFileInput struct {
	Path string `json:"path"`
}

func (t *DeleteFileTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "function",
		Name: "delete_file",
		Description: "Delete a file",
		InputSchema: anthropic.ToolParamInputSchema{
			Type: "object",
			Properties: map[string]anthropic.ToolParamInputSchemaProperty{
				"path": {
					Type:        "string",
					Description: "Path to the file to delete.",
				},
			},
			Required: []string{"path"},
		},
	}
}

func (t *DeleteFileTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *DeleteFileTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	_, err := t.run(ctx, block, toolCtx, true)
	return err
}

func (t *DeleteFileTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	var input DeleteFileInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.Path == "" {
		return nil, NewToolInputError(fmt.Errorf("path is required"))
	}

	fs := toolCtx.Workspace.FileSystem()
	
	if err := fs.Delete(ctx, input.Path); err != nil {
		return nil, fmt.Errorf("failed to delete file: %w", err)
	}

	result := fmt.Sprintf("File %s deleted successfully", input.Path)
	return &result, nil
}

// PostCommentTool implements the post_comment tool
type PostCommentTool struct{}

type PostCommentInput struct {
	Body        string `json:"body"`
	CommentType string `json:"comment_type"`
	InReplyTo   int64  `json:"in_reply_to,omitempty"`
}

func (t *PostCommentTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "function",
		Name: "post_comment",
		Description: "Post a comment to engage in discussion",
		InputSchema: anthropic.ToolParamInputSchema{
			Type: "object",
			Properties: map[string]anthropic.ToolParamInputSchemaProperty{
				"body": {
					Type:        "string",
					Description: "The comment text (markdown supported)",
				},
				"comment_type": {
					Type:        "string",
					Description: "Type of comment to post",
					Enum:        []string{"issue", "pr", "review"},
				},
				"in_reply_to": {
					Type:        "integer",
					Description: "ID of comment being replied to (for review comments only)",
				},
			},
			Required: []string{"body", "comment_type"},
		},
	}
}

func (t *PostCommentTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *PostCommentTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Skip posting comments in replay mode
	return nil
}

func (t *PostCommentTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	if replay {
		return nil, nil // Skip in replay mode
	}

	var input PostCommentInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.Body == "" {
		return nil, NewToolInputError(fmt.Errorf("body is required"))
	}

	owner := toolCtx.Task.Issue.Owner
	repo := toolCtx.Task.Issue.Repository
	issueNumber := toolCtx.Task.Issue.GetNumber()

	var commentURL string
	var err error

	switch input.CommentType {
	case "issue":
		comment, _, err := toolCtx.GithubClient.Issues.CreateComment(ctx, owner, repo, issueNumber, &github.IssueComment{
			Body: &input.Body,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to post issue comment: %w", err)
		}
		commentURL = comment.GetHTMLURL()

	case "pr":
		if toolCtx.Task.PullRequest == nil {
			return nil, NewToolInputError(fmt.Errorf("no pull request exists for this issue"))
		}
		prNumber := toolCtx.Task.PullRequest.GetNumber()
		comment, _, err := toolCtx.GithubClient.Issues.CreateComment(ctx, owner, repo, prNumber, &github.IssueComment{
			Body: &input.Body,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to post PR comment: %w", err)
		}
		commentURL = comment.GetHTMLURL()

	case "review":
		if toolCtx.Task.PullRequest == nil {
			return nil, NewToolInputError(fmt.Errorf("no pull request exists for this issue"))
		}
		// For review comments, we would need additional context like file path and position
		// This is a simplified implementation
		return nil, NewToolInputError(fmt.Errorf("review comments not yet fully implemented"))

	default:
		return nil, NewToolInputError(fmt.Errorf("invalid comment_type: %s", input.CommentType))
	}

	result := fmt.Sprintf("Comment posted successfully: %s", commentURL)
	return &result, nil
}

// AddReactionTool implements the add_reaction tool
type AddReactionTool struct{}

type AddReactionInput struct {
	CommentID   int64  `json:"comment_id"`
	CommentType string `json:"comment_type"`
	Reaction    string `json:"reaction"`
}

func (t *AddReactionTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "function",
		Name: "add_reaction",
		Description: "Add a reaction to acknowledge or respond to a comment",
		InputSchema: anthropic.ToolParamInputSchema{
			Type: "object",
			Properties: map[string]anthropic.ToolParamInputSchemaProperty{
				"comment_id": {
					Type:        "integer",
					Description: "ID of the comment to react to",
				},
				"comment_type": {
					Type:        "string",
					Description: "Whether this is a comment on an issue, a comment on a PR, or a comment that is part of a PR review",
					Enum:        []string{"issue", "PR", "PR review"},
				},
				"reaction": {
					Type:        "string",
					Description: "The reaction emoji to add",
					Enum:        []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"},
				},
			},
			Required: []string{"comment_id", "comment_type", "reaction"},
		},
	}
}

func (t *AddReactionTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *AddReactionTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Skip adding reactions in replay mode
	return nil
}

func (t *AddReactionTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	if replay {
		return nil, nil // Skip in replay mode
	}

	var input AddReactionInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.CommentID == 0 {
		return nil, NewToolInputError(fmt.Errorf("comment_id is required"))
	}

	owner := toolCtx.Task.Issue.Owner
	repo := toolCtx.Task.Issue.Repository

	switch input.CommentType {
	case "issue":
		_, _, err := toolCtx.GithubClient.Reactions.CreateIssueCommentReaction(ctx, owner, repo, input.CommentID, input.Reaction)
		if err != nil {
			return nil, fmt.Errorf("failed to add reaction to issue comment: %w", err)
		}

	case "PR":
		_, _, err := toolCtx.GithubClient.Reactions.CreateIssueCommentReaction(ctx, owner, repo, input.CommentID, input.Reaction)
		if err != nil {
			return nil, fmt.Errorf("failed to add reaction to PR comment: %w", err)
		}

	case "PR review":
		_, _, err := toolCtx.GithubClient.Reactions.CreatePullRequestCommentReaction(ctx, owner, repo, input.CommentID, input.Reaction)
		if err != nil {
			return nil, fmt.Errorf("failed to add reaction to PR review comment: %w", err)
		}

	default:
		return nil, NewToolInputError(fmt.Errorf("invalid comment_type: %s", input.CommentType))
	}

	result := fmt.Sprintf("Added %s reaction to comment %d", input.Reaction, input.CommentID)
	return &result, nil
}

// ValidateChangesTool implements the validate_changes tool
type ValidateChangesTool struct{}

type ValidateChangesInput struct {
	CommitMessage string `json:"commit_message"`
}

func (t *ValidateChangesTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "function",
		Name: "validate_changes",
		Description: "Validate all previous file changes, e.g. run tests and static analysis",
		InputSchema: anthropic.ToolParamInputSchema{
			Type: "object",
			Properties: map[string]anthropic.ToolParamInputSchemaProperty{
				"commit_message": {
					Type:        "string",
					Description: "Commit message for file changes made since the last call to this tool. May or may not be used depending on the implementation, but a non-empty string must be provided",
				},
			},
			Required: []string{"commit_message"},
		},
	}
}

func (t *ValidateChangesTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *ValidateChangesTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Skip validation in replay mode
	return nil
}

func (t *ValidateChangesTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	if replay {
		return nil, nil // Skip in replay mode
	}

	var input ValidateChangesInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.CommitMessage == "" {
		return nil, NewToolInputError(fmt.Errorf("commit_message is required"))
	}

	if err := toolCtx.Workspace.ValidateChanges(ctx, input.CommitMessage); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	result := "Changes validated successfully"
	return &result, nil
}

// PublishChangesForReviewTool implements the publish_changes_for_review tool
type PublishChangesForReviewTool struct{}

type PublishChangesForReviewInput struct {
	PullRequestTitle string `json:"pull_request_title"`
	PullRequestBody  string `json:"pull_request_body"`
}

func (t *PublishChangesForReviewTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "function",
		Name: "publish_changes_for_review",
		Description: "Publish changes for review by other developers",
		InputSchema: anthropic.ToolParamInputSchema{
			Type: "object",
			Properties: map[string]anthropic.ToolParamInputSchemaProperty{
				"pull_request_title": {
					Type:        "string",
					Description: "Title for the new pull request, if any. Ignored if a pull request already exists",
				},
				"pull_request_body": {
					Type:        "string",
					Description: "Description of the solution and what changes were made. Ignored if a pull request already exists",
				},
			},
			Required: []string{"pull_request_title", "pull_request_body"},
		},
	}
}

func (t *PublishChangesForReviewTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *PublishChangesForReviewTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Skip publishing in replay mode
	return nil
}

func (t *PublishChangesForReviewTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	if replay {
		return nil, nil // Skip in replay mode
	}

	var input PublishChangesForReviewInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.PullRequestTitle == "" {
		return nil, NewToolInputError(fmt.Errorf("pull_request_title is required"))
	}
	if input.PullRequestBody == "" {
		return nil, NewToolInputError(fmt.Errorf("pull_request_body is required"))
	}

	if err := toolCtx.Workspace.PublishChangesForReview(ctx, input.PullRequestTitle, input.PullRequestBody); err != nil {
		return nil, fmt.Errorf("failed to publish changes: %w", err)
	}

	result := "Changes published for review successfully"
	return &result, nil
}

// ReportLimitationTool implements the report_limitation tool
type ReportLimitationTool struct{}

type ReportLimitationInput struct {
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	Suggestions string `json:"suggestions,omitempty"`
}

func (t *ReportLimitationTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Type: "function",
		Name: "report_limitation",
		Description: "Report when you need to perform an action that you don't have a tool for. Use this instead of trying workarounds with available tools.",
		InputSchema: anthropic.ToolParamInputSchema{
			Type: "object",
			Properties: map[string]anthropic.ToolParamInputSchemaProperty{
				"action": {
					Type:        "string",
					Description: "The action you want to perform but don't have a tool for",
				},
				"reason": {
					Type:        "string",
					Description: "Why this action is needed to complete the task",
				},
				"suggestions": {
					Type:        "string",
					Description: "Optional suggestions for how this limitation could be addressed or alternative approaches",
				},
			},
			Required: []string{"action", "reason"},
		},
	}
}

func (t *ReportLimitationTool) Run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*string, error) {
	return t.run(ctx, block, toolCtx, false)
}

func (t *ReportLimitationTool) Replay(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	// Skip reporting limitations in replay mode
	return nil
}

func (t *ReportLimitationTool) run(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext, replay bool) (*string, error) {
	if replay {
		return nil, nil // Skip in replay mode
	}

	var input ReportLimitationInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, fmt.Errorf("error parsing input: %w", err)
	}

	if input.Action == "" {
		return nil, NewToolInputError(fmt.Errorf("action is required"))
	}
	if input.Reason == "" {
		return nil, NewToolInputError(fmt.Errorf("reason is required"))
	}

	result := fmt.Sprintf("Limitation reported: Cannot perform '%s' because %s", input.Action, input.Reason)
	if input.Suggestions != "" {
		result += fmt.Sprintf(". Suggestions: %s", input.Suggestions)
	}

	return &result, nil
}