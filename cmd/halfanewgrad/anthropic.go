package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicClient wraps the official Anthropic SDK client
type AnthropicClient struct {
	client anthropic.Client
}

// NewAnthropicClient creates a new Anthropic API client using the official SDK
func NewAnthropicClient(apiKey string) *AnthropicClient {
	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)
	return &AnthropicClient{
		client: client,
	}
}

// Action represents an action the AI decided to take
type Action struct {
	Type   string                 // "view", "str_replace", "create", "insert", "post_comment", "add_reaction", etc.
	Data   map[string]interface{} // Action-specific data
	ToolID string                 // Tool use ID for responses
}

// InteractionResult represents the outcome of an AI interaction
type InteractionResult struct {
	Actions          []Action
	NeedsMoreInfo    bool   // AI is waiting for tool responses
	ContinuationData string // Data to continue conversation later
}

// Comment represents a comment to be posted
type Comment struct {
	Type         string // "issue", "pr", "review", "review_reply"
	Body         string
	InReplyTo    *int64           // For reply threads
	FilePath     string           // For review comments
	Line         int              // For review comments
	Side         string           // For review comments
	AddReactions map[int64]string // CommentID -> reaction emoji
}

// ProcessWorkItem processes a work item and returns what actions to take
func (ac *AnthropicClient) ProcessWorkItem(ctx context.Context, workCtx *WorkContext) (*InteractionResult, error) {
	systemPrompt := `You are a highly skilled software developer working as a virtual assistant on GitHub.

Your responsibilities include:
1. Analyzing GitHub issues and pull requests
2. Engaging in technical discussions professionally
3. Creating high-quality code solutions when appropriate
4. Following repository coding standards and style guides
5. Providing guidance on best practices

When interacting:
- Ask clarifying questions when requirements are unclear
- Push back professionally on suggestions that violate best practices
- Explain your reasoning when disagreeing with suggestions
- Only create solutions when you have enough information
- Engage in discussion threads appropriately
- Add reactions to acknowledge you've seen comments

You have access to several tools:
- str_replace_based_edit_tool: A text editor for viewing, creating, and editing files
  - view: Examine file contents or list directory contents
  - str_replace: Replace specific text in files with new text
  - create: Create new files with specified content
  - insert: Insert text at specific line numbers
- create_branch: Create a new branch (for initial solutions)
- create_pull_request: Create a pull request from the current branch
- post_comment: Post comments to engage in discussion
- add_reaction: React to existing comments
- request_review: Ask specific users for review or input

The text editor tool is your primary way to examine and modify code. Use it to:
- View files to understand the codebase structure
- Make precise edits using str_replace
- Create new files when needed
- Insert code at specific locations

When working on a new issue:
1. First explore the codebase with the text editor
2. Create a branch with create_branch
3. Make your changes using the text editor tools
4. Create a pull request with create_pull_request

When using str_replace:
- The old_str must match EXACTLY, including whitespace
- Include enough context to make the match unique
- Use line numbers from view output for reference

Choose the appropriate tools based on the situation. You don't always need to create a solution immediately - sometimes discussion is more valuable.`

	// Define all available tools
	tools := []anthropic.ToolParam{
		{
			Type: "text_editor_20250429",
			Name: "str_replace_based_edit_tool",
		},
		{
			Name:        "create_branch",
			Description: anthropic.String("Create a new branch for working on an issue (only for initial solutions)"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"branch_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the branch to create (e.g., fix/issue-123-description)",
					},
				},
			},
		},
		{
			Name:        "create_pull_request",
			Description: anthropic.String("Create a pull request with all current changes"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"commit_message": map[string]interface{}{
						"type":        "string",
						"description": "Commit message for the changes",
					},
					"pull_request_title": map[string]interface{}{
						"type":        "string",
						"description": "Title for the pull request",
					},
					"pull_request_body": map[string]interface{}{
						"type":        "string",
						"description": "Description of the solution and what changes were made",
					},
				},
			},
		},
		{
			Name:        "post_comment",
			Description: anthropic.String("Post a comment to engage in discussion"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"comment_type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"issue", "pr", "review", "review_reply"},
						"description": "Type of comment to post",
					},
					"body": map[string]interface{}{
						"type":        "string",
						"description": "The comment text (markdown supported)",
					},
					"in_reply_to": map[string]interface{}{
						"type":        "integer",
						"description": "ID of comment being replied to (for threads)",
					},
					"file_path": map[string]interface{}{
						"type":        "string",
						"description": "File path (for review comments)",
					},
					"line": map[string]interface{}{
						"type":        "integer",
						"description": "Line number (for review comments)",
					},
					"side": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"LEFT", "RIGHT"},
						"description": "Side of diff (for review comments)",
					},
				},
			},
		},
		{
			Name:        "add_reaction",
			Description: anthropic.String("Add a reaction to acknowledge or respond to a comment"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"comment_id": map[string]interface{}{
						"type":        "integer",
						"description": "ID of the comment to react to",
					},
					"reaction": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"},
						"description": "The reaction emoji to add",
					},
				},
			},
		},
		{
			Name:        "request_review",
			Description: anthropic.String("Request review or input from specific users"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"usernames": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "GitHub usernames to request review from",
					},
					"message": map[string]interface{}{
						"type":        "string",
						"description": "Message explaining what input is needed",
					},
				},
			},
		},
	}

	// Rest of the function remains the same...
	// Build prompt from work context
	prompt := workCtx.BuildPrompt()

	// Add instructions about current state
	if workCtx.IsInitialSolution && workCtx.Issue != nil {
		prompt += "\n\nThis is a new issue. Analyze it carefully and decide whether to:\n"
		prompt += "1. Ask clarifying questions if requirements are unclear\n"
		prompt += "2. Use the text editor tool to examine the codebase and understand the structure\n"
		prompt += "3. Create a branch for your work using create_branch\n"
		prompt += "4. Make your changes using the text editor tools\n"
		prompt += "5. Create a pull request using create_pull_request\n"
		prompt += "6. Discuss approach or concerns before implementing\n\n"
		prompt += "Start by using the text editor tool to view key files and understand the codebase structure."
	} else if len(workCtx.NeedsToRespond) > 0 {
		prompt += "\n\nThere are comments that may need your response. Consider whether to:\n"
		prompt += "1. Answer questions or provide clarifications\n"
		prompt += "2. Use the text editor tool to examine files and implement requested changes\n"
		prompt += "3. Explain why certain suggestions might not be appropriate\n"
		prompt += "4. Ask for more information"
	}

	// Create message with tools
	message, err := ac.CreateMessage(ctx, prompt, tools, systemPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	// Process response
	return ac.processInteractionResponse(ctx, message, workCtx)
}

// CreateMessage sends a message to the Anthropic API with optional tools
func (ac *AnthropicClient) CreateMessage(ctx context.Context, prompt string, tools []anthropic.ToolParam, systemPrompt string) (*anthropic.Message, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_0,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	}

	// Add tools if provided
	if len(tools) > 0 {
		toolsUnion := make([]anthropic.ToolUnionParam, len(tools))
		for i, tool := range tools {
			toolsUnion[i] = anthropic.ToolUnionParam{
				OfTool: &tool,
			}
		}
		params.Tools = toolsUnion
	}

	message, err := ac.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

// CreateMessageWithHistory creates a message with conversation history
func (ac *AnthropicClient) CreateMessageWithHistory(ctx context.Context, messages []anthropic.MessageParam) (*anthropic.Message, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_0,
		MaxTokens: 4096,
		Messages:  messages,
	}

	message, err := ac.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

// CreateMessageWithSystemPrompt creates a message with conversation history and system prompt
func (ac *AnthropicClient) CreateMessageWithSystemPrompt(ctx context.Context, messages []anthropic.MessageParam, systemPrompt string) (*anthropic.Message, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_0,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: messages,
	}

	message, err := ac.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

// processInteractionResponse processes the AI's response and extracts actions
func (ac *AnthropicClient) processInteractionResponse(ctx context.Context, response *anthropic.Message, workCtx *WorkContext) (*InteractionResult, error) {
	result := &InteractionResult{
		Actions: []Action{},
	}

	for _, content := range response.Content {
		switch block := content.AsAny().(type) {
		case anthropic.ToolUseBlock:
			action, err := ac.processToolUse(block, workCtx)
			if err != nil {
				log.Printf("Warning: failed to process tool use %s: %v", block.Name, err)
				continue
			}

			result.Actions = append(result.Actions, *action)
		}
	}

	// Check if AI is waiting for tool responses
	hasToolActions := false
	for _, action := range result.Actions {
		switch action.Type {
		case "view", "str_replace", "create", "insert", "undo_edit":
			hasToolActions = true
		}
	}

	result.NeedsMoreInfo = hasToolActions

	return result, nil
}

// processToolUse processes a single tool use block
func (ac *AnthropicClient) processToolUse(block anthropic.ToolUseBlock, workCtx *WorkContext) (*Action, error) {
	action := &Action{
		Type:   block.Name,
		Data:   make(map[string]interface{}),
		ToolID: block.ID,
	}

	// Marshal and unmarshal to convert to map
	inputJSON, err := json.Marshal(block.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	var inputMap map[string]interface{}
	if err := json.Unmarshal(inputJSON, &inputMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	action.Data = inputMap

	// Special processing for text editor commands
	if block.Name == "str_replace_based_edit_tool" {
		// Extract the command type from the input
		if command, ok := inputMap["command"].(string); ok {
			action.Type = command
		}
	}

	return action, nil
}

// ExecuteTextEditorCommand executes a text editor command and returns the result
func (ac *AnthropicClient) ExecuteTextEditorCommand(ctx context.Context, action Action, fileSystem *GitHubFileSystem) (string, error) {
	command := action.Type
	_, ok := action.Data["path"].(string)
	if !ok {
		return "", fmt.Errorf("no path specified for command %s", command)
	}

	switch command {
	case "view":
		return ac.executeViewCommand(action, fileSystem)
	case "str_replace":
		return ac.executeStrReplaceCommand(action, fileSystem)
	case "create":
		return ac.executeCreateCommand(action, fileSystem)
	case "insert":
		return ac.executeInsertCommand(action, fileSystem)
	case "undo_edit":
		return ac.executeUndoEditCommand(action, fileSystem)
	default:
		return "", fmt.Errorf("unknown text editor command: %s", command)
	}
}

// executeViewCommand handles the view command
func (ac *AnthropicClient) executeViewCommand(action Action, fs *GitHubFileSystem) (string, error) {
	path := action.Data["path"].(string)

	isDir, err := fs.IsDirectory(path)
	if err != nil {
		return "", fmt.Errorf("error checking path: %w", err)
	}

	if isDir {
		// List directory contents
		files, err := fs.ListDirectory(path)
		if err != nil {
			return "", fmt.Errorf("error listing directory: %w", err)
		}

		result := fmt.Sprintf("Directory contents of %s:\n", path)
		for _, file := range files {
			result += fmt.Sprintf("  %s\n", file)
		}
		return result, nil
	}

	// Read file contents
	content, err := fs.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	// Handle view_range if specified
	if viewRange, ok := action.Data["view_range"].([]interface{}); ok && len(viewRange) == 2 {
		startLine := int(viewRange[0].(float64))
		endLine := int(viewRange[1].(float64))

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

	// Return full file with line numbers
	lines := strings.Split(content, "\n")
	var result strings.Builder
	for i, line := range lines {
		result.WriteString(fmt.Sprintf("%d: %s\n", i+1, line))
	}
	return result.String(), nil
}

// executeStrReplaceCommand handles the str_replace command
func (ac *AnthropicClient) executeStrReplaceCommand(action Action, fs *GitHubFileSystem) (string, error) {
	path := action.Data["path"].(string)
	oldStr, ok := action.Data["old_str"].(string)
	if !ok {
		return "", fmt.Errorf("old_str not specified")
	}
	newStr, ok := action.Data["new_str"].(string)
	if !ok {
		return "", fmt.Errorf("new_str not specified")
	}

	// Read current file content
	content, err := fs.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	// Check if old_str exists and is unique
	count := strings.Count(content, oldStr)
	if count == 0 {
		return "", fmt.Errorf("old_str not found in file")
	}
	if count > 1 {
		return "", fmt.Errorf("old_str found %d times in file, must be unique", count)
	}

	// Perform replacement
	newContent := strings.Replace(content, oldStr, newStr, 1)

	// Write back to file
	err = fs.WriteFile(path, newContent)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	return fmt.Sprintf("Successfully replaced text in %s", path), nil
}

// executeCreateCommand handles the create command
func (ac *AnthropicClient) executeCreateCommand(action Action, fs *GitHubFileSystem) (string, error) {
	path := action.Data["path"].(string)
	fileText, ok := action.Data["file_text"].(string)
	if !ok {
		return "", fmt.Errorf("file_text not specified")
	}

	// Check if file already exists
	exists, err := fs.FileExists(path)
	if err != nil {
		return "", fmt.Errorf("error checking file existence: %w", err)
	}
	if exists {
		return "", fmt.Errorf("file already exists: %s", path)
	}

	// Create the file
	err = fs.WriteFile(path, fileText)
	if err != nil {
		return "", fmt.Errorf("error creating file: %w", err)
	}

	return fmt.Sprintf("Successfully created file %s", path), nil
}

// executeInsertCommand handles the insert command
func (ac *AnthropicClient) executeInsertCommand(action Action, fs *GitHubFileSystem) (string, error) {
	path := action.Data["path"].(string)
	insertLine, ok := action.Data["insert_line"].(float64)
	if !ok {
		return "", fmt.Errorf("insert_line not specified")
	}
	newStr, ok := action.Data["new_str"].(string)
	if !ok {
		return "", fmt.Errorf("new_str not specified")
	}

	// Read current file content
	content, err := fs.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	lines := strings.Split(content, "\n")
	lineNum := int(insertLine)

	// Insert at the specified line
	if lineNum < 0 || lineNum > len(lines) {
		return "", fmt.Errorf("invalid insert_line: %d", lineNum)
	}

	// Insert the new text
	newLines := strings.Split(newStr, "\n")
	var result []string

	if lineNum == 0 {
		// Insert at beginning
		result = append(newLines, lines...)
	} else if lineNum >= len(lines) {
		// Insert at end
		result = append(lines, newLines...)
	} else {
		// Insert in middle
		result = append(lines[:lineNum], newLines...)
		result = append(result, lines[lineNum:]...)
	}

	newContent := strings.Join(result, "\n")

	// Write back to file
	err = fs.WriteFile(path, newContent)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	return fmt.Sprintf("Successfully inserted text at line %d in %s", lineNum, path), nil
}

// executeUndoEditCommand handles the undo_edit command
func (ac *AnthropicClient) executeUndoEditCommand(action Action, fs *GitHubFileSystem) (string, error) {
	// This would require maintaining edit history - simplified implementation
	return "", fmt.Errorf("undo_edit not implemented - please use version control instead")
}
