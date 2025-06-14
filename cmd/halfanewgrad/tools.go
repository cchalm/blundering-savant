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

	// Run takes a ToolUseBlock, performs the tool call, and returns a string result or an error
	Run(block anthropic.ToolUseBlock, ctx *ToolContext) (*string, error)
}

// ToolContext provides context needed by tools during execution
type ToolContext struct {
	FileSystem   *GitHubFileSystem
	Owner        string
	Repo         string
	WorkContext  workContext
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
func (t *TextEditorTool) Run(block anthropic.ToolUseBlock, ctx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %v", err)
	}

	var result string
	switch input.Command {
	case "view":
		result, err = t.executeView(input, ctx.FileSystem)
	case "str_replace":
		result, err = t.executeStrReplace(input, ctx.FileSystem)
	case "create":
		result, err = t.executeCreate(input, ctx.FileSystem)
	case "insert":
		result, err = t.executeInsert(input, ctx.FileSystem)
	case "undo_edit":
		result = ""
		err = ToolInputError{fmt.Errorf("undo_edit not supported")}
	default:
		result = ""
		err = ToolInputError{fmt.Errorf("unknown text editor command: %s", input.Command)}
	}

	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}
	return &result, nil
}

// Implementation methods for each command
func (t *TextEditorTool) executeView(input *TextEditorInput, fs *GitHubFileSystem) (string, error) {
	if fs == nil {
		return "", fmt.Errorf("file system not initialized")
	}

	isDir, err := fs.IsDirectory(input.Path)
	if err != nil {
		return "", fmt.Errorf("error checking path: %w", err)
	}

	if isDir {
		files, err := fs.ListDirectory(input.Path)
		if err != nil {
			return "", fmt.Errorf("error listing directory: %w", err)
		}

		result := fmt.Sprintf("Directory contents of %s:\n", input.Path)
		for _, file := range files {
			result += fmt.Sprintf("  %s\n", file)
		}
		return result, nil
	}

	content, err := fs.ReadFile(input.Path)
	if err != nil {
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

func (t *TextEditorTool) executeStrReplace(input *TextEditorInput, fs *GitHubFileSystem) (string, error) {
	content, err := fs.ReadFile(input.Path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	count := strings.Count(content, input.OldStr)
	if count == 0 {
		return "", fmt.Errorf("old_str not found in file")
	}
	if count > 1 {
		return "", fmt.Errorf("old_str found %d times in file, must be unique", count)
	}

	newContent := strings.Replace(content, input.OldStr, input.NewStr, 1)
	err = fs.WriteFile(input.Path, newContent)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	return fmt.Sprintf("Successfully replaced text in %s", input.Path), nil
}

func (t *TextEditorTool) executeCreate(input *TextEditorInput, fs *GitHubFileSystem) (string, error) {
	exists, err := fs.FileExists(input.Path)
	if err != nil {
		return "", fmt.Errorf("error checking file existence: %w", err)
	}
	if exists {
		return "", fmt.Errorf("file already exists: %s", input.Path)
	}

	err = fs.WriteFile(input.Path, input.FileText)
	if err != nil {
		return "", fmt.Errorf("error creating file: %w", err)
	}

	return fmt.Sprintf("Successfully created file %s", input.Path), nil
}

func (t *TextEditorTool) executeInsert(input *TextEditorInput, fs *GitHubFileSystem) (string, error) {
	content, err := fs.ReadFile(input.Path)
	if err != nil {
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
	err = fs.WriteFile(input.Path, newContent)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	return fmt.Sprintf("Successfully inserted text at line %d in %s", lineNum, input.Path), nil
}

// CreatePullRequestTool implements the create_pull_request tool
type CreatePullRequestTool struct {
	BaseTool
}

// CreatePullRequestInput represents the input for create_pull_request
type CreatePullRequestInput struct {
	CommitMessage    string `json:"commit_message"`
	PullRequestTitle string `json:"pull_request_title"`
	PullRequestBody  string `json:"pull_request_body"`
}

// NewCreatePullRequestTool creates a new create pull request tool
func NewCreatePullRequestTool() *CreatePullRequestTool {
	return &CreatePullRequestTool{
		BaseTool: BaseTool{Name: "create_pull_request"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *CreatePullRequestTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Create a pull request with all current changes"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"commit_message": map[string]any{
					"type":        "string",
					"description": "Commit message for the changes",
				},
				"pull_request_title": map[string]any{
					"type":        "string",
					"description": "Title for the pull request",
				},
				"pull_request_body": map[string]any{
					"type":        "string",
					"description": "Description of the solution and what changes were made",
				},
			},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *CreatePullRequestTool) ParseToolUse(block anthropic.ToolUseBlock) (*CreatePullRequestInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input CreatePullRequestInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the create pull request command
func (t *CreatePullRequestTool) Run(block anthropic.ToolUseBlock, ctx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %v", err)
	}

	if ctx.FileSystem == nil {
		return nil, fmt.Errorf("file system not initialized")
	}

	if input.CommitMessage == "" {
		return nil, ToolInputError{fmt.Errorf("commit_message is required")}
	}

	if input.PullRequestTitle == "" {
		return nil, ToolInputError{fmt.Errorf("pull_request_title is required")}
	}

	if input.PullRequestBody == "" {
		return nil, ToolInputError{fmt.Errorf("pull_request_body is required")}
	}

	// Check if there are changes to commit
	if !ctx.FileSystem.HasChanges() {
		return nil, fmt.Errorf("no changes to commit")
	}

	// Commit the changes
	_, err = ctx.FileSystem.CommitChanges(input.CommitMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to commit changes: %w", err)
	}

	// Determine target branch
	var targetBranch string
	if ctx.WorkContext.PullRequest == nil {
		// Get default branch for new PRs
		repository, _, err := ctx.GithubClient.Repositories.Get(context.Background(), ctx.Owner, ctx.Repo)
		if err != nil {
			return nil, fmt.Errorf("failed to get repository: %w", err)
		}
		targetBranch = repository.GetDefaultBranch()

		// Add issue reference to PR body
		issueNumber := 0
		if ctx.WorkContext.Issue != nil && ctx.WorkContext.Issue.Number != nil {
			issueNumber = *ctx.WorkContext.Issue.Number
		}

		input.PullRequestBody = fmt.Sprintf(`%s

Fixes #%d

---
*This PR was created by the Virtual Developer bot.*`, input.PullRequestBody, issueNumber)
	} else {
		// For existing PRs, use the same target branch
		if ctx.WorkContext.PullRequest != nil && ctx.WorkContext.PullRequest.Base != nil && ctx.WorkContext.PullRequest.Base.Ref != nil {
			targetBranch = *ctx.WorkContext.PullRequest.Base.Ref
		} else {
			return nil, fmt.Errorf("could not determine target branch for existing PR")
		}
	}

	_, err = ctx.FileSystem.CreatePullRequest(input.PullRequestTitle, input.PullRequestBody, targetBranch)
	if err != nil {
		return nil, err
	}

	return nil, nil
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
	FilePath    string `json:"file_path,omitempty"`
	Line        int    `json:"line,omitempty"`
	Side        string `json:"side,omitempty"`
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
					"enum":        []string{"issue", "pr", "review", "review_reply"},
					"description": "Type of comment to post",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "The comment text (markdown supported)",
				},
				"in_reply_to": map[string]any{
					"type":        "integer",
					"description": "ID of comment being replied to (for threads)",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path (for review comments)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (for review comments)",
				},
				"side": map[string]any{
					"type":        "string",
					"enum":        []string{"LEFT", "RIGHT"},
					"description": "Side of diff (for review comments)",
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
func (t *PostCommentTool) Run(block anthropic.ToolUseBlock, ctx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %v", err)
	}

	if input.Body == "" {
		return nil, ToolInputError{fmt.Errorf("body is required")}
	}

	if input.CommentType == "" {
		return nil, ToolInputError{fmt.Errorf("comment_type is required")}
	}

	switch input.CommentType {
	case "issue":
		if ctx.WorkContext.Issue != nil && ctx.WorkContext.Issue.Number != nil {
			comment := &github.IssueComment{
				Body: github.Ptr(input.Body),
			}
			_, _, err = ctx.GithubClient.Issues.CreateComment(context.Background(), ctx.Owner, ctx.Repo, *ctx.WorkContext.Issue.Number, comment)
			if err != nil {
				return nil, err
			}
		}
	case "pr":
		if ctx.WorkContext.PullRequest != nil && ctx.WorkContext.PullRequest.Number != nil {
			comment := &github.IssueComment{
				Body: github.Ptr(input.Body),
			}
			_, _, err = ctx.GithubClient.Issues.CreateComment(context.Background(), ctx.Owner, ctx.Repo, *ctx.WorkContext.PullRequest.Number, comment)
			if err != nil {
				return nil, err
			}
		}
	case "review":
		if ctx.WorkContext.PullRequest != nil && ctx.WorkContext.PullRequest.Number != nil {
			pr, _, err := ctx.GithubClient.PullRequests.Get(context.Background(), ctx.Owner, ctx.Repo, *ctx.WorkContext.PullRequest.Number)
			if err != nil {
				return nil, err
			}

			reviewComment := &github.PullRequestComment{
				Body:     github.Ptr(input.Body),
				Path:     github.Ptr(input.FilePath),
				Line:     github.Ptr(input.Line),
				Side:     github.Ptr(input.Side),
				CommitID: pr.Head.SHA,
			}

			_, _, err = ctx.GithubClient.PullRequests.CreateComment(context.Background(), ctx.Owner, ctx.Repo, *ctx.WorkContext.PullRequest.Number, reviewComment)
			if err != nil {
				return nil, err
			}
		}
	case "review_reply":
		if input.InReplyTo != nil {
			return nil, fmt.Errorf("TODO review comment reply not fully implemented - needs PR tracking")
		}
	}

	return nil, nil
}

// AddReactionTool implements the add_reaction tool
type AddReactionTool struct {
	BaseTool
}

// AddReactionInput represents the input for add_reaction
type AddReactionInput struct {
	CommentID int64  `json:"comment_id"`
	Reaction  string `json:"reaction"`
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
func (t *AddReactionTool) Run(block anthropic.ToolUseBlock, ctx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %v", err)
	}

	if input.CommentID == 0 {
		return nil, ToolInputError{fmt.Errorf("comment_id is required")}
	}

	if input.Reaction == "" {
		return nil, ToolInputError{fmt.Errorf("reaction is required")}
	}

	// TODO what about issue comments?
	_, _, err = ctx.GithubClient.Reactions.CreatePullRequestCommentReaction(context.Background(), ctx.Owner, ctx.Repo, input.CommentID, input.Reaction)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// RequestReviewTool implements the request_review tool
type RequestReviewTool struct {
	BaseTool
}

// RequestReviewInput represents the input for request_review
type RequestReviewInput struct {
	Usernames []string `json:"usernames"`
	Message   string   `json:"message"`
}

// NewRequestReviewTool creates a new request review tool
func NewRequestReviewTool() *RequestReviewTool {
	return &RequestReviewTool{
		BaseTool: BaseTool{Name: "request_review"},
	}
}

// GetToolParam returns the tool parameter definition
func (t *RequestReviewTool) GetToolParam() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        t.Name,
		Description: anthropic.String("Request review or input from specific users"),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"usernames": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "GitHub usernames to request review from",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Message explaining what input is needed",
				},
			},
		},
	}
}

// ParseToolUse parses the tool use block
func (t *RequestReviewTool) ParseToolUse(block anthropic.ToolUseBlock) (*RequestReviewInput, error) {
	if block.Name != t.Name {
		return nil, fmt.Errorf("tool use block is for %s, not %s", block.Name, t.Name)
	}

	var input RequestReviewInput
	if err := parseInputJSON(block, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

// Run executes the request review command
func (t *RequestReviewTool) Run(block anthropic.ToolUseBlock, ctx *ToolContext) (*string, error) {
	input, err := t.ParseToolUse(block)
	if err != nil {
		return nil, fmt.Errorf("error parsing input: %v", err)
	}

	if len(input.Usernames) == 0 {
		return nil, ToolInputError{fmt.Errorf("usernames are required")}
	}

	if input.Message == "" {
		return nil, ToolInputError{fmt.Errorf("message is required")}
	}

	reviewRequest := github.ReviewersRequest{
		Reviewers: input.Usernames,
	}

	_, _, err = ctx.GithubClient.PullRequests.RequestReviewers(context.Background(), ctx.Owner, ctx.Repo, *ctx.WorkContext.PullRequest.Number, reviewRequest)
	return nil, err
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
	registry.Register(NewCreatePullRequestTool())
	registry.Register(NewPostCommentTool())
	registry.Register(NewAddReactionTool())
	registry.Register(NewRequestReviewTool())

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
func (r *ToolRegistry) ProcessToolUse(block anthropic.ToolUseBlock, ctx *ToolContext) (*anthropic.ToolResultBlockParam, error) {
	tool, ok := r.GetTool(block.Name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", block.Name)
	}

	response, err := tool.Run(block, ctx)

	var resultBlock anthropic.ToolResultBlockParam
	var tie *ToolInputError
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
