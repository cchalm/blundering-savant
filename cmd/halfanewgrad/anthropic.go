package main

import (
	"context"
	"fmt"

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

// InteractionResult represents the outcome of an AI interaction
type InteractionResult struct {
	ToolUses         []anthropic.ToolUseBlock // Tools used by the AI
	NeedsMoreInfo    bool                     // AI is waiting for tool responses
	ContinuationData string                   // Data to continue conversation later
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
func (ac *AnthropicClient) ProcessWorkItem(ctx context.Context, workCtx *WorkContext, toolRegistry *ToolRegistry) (*InteractionResult, error) {
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

	// Get all tool parameters from the registry
	tools := toolRegistry.GetAllToolParams()

	// Build prompt from work context
	prompt := workCtx.BuildPrompt()

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
		ToolUses: []anthropic.ToolUseBlock{},
	}

	for _, content := range response.Content {
		switch block := content.AsAny().(type) {
		case anthropic.ToolUseBlock:
			result.ToolUses = append(result.ToolUses, block)
		}
	}

	return result, nil
}
