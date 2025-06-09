package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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
	Type   string                 // "analyze_file", "create_solution", "post_comment", "add_reaction", "request_review"
	Data   map[string]interface{} // Action-specific data
	ToolID string                 // Tool use ID for responses
}

// InteractionResult represents the outcome of an AI interaction
type InteractionResult struct {
	Actions          []Action
	Solution         *Solution // May be nil if AI chose not to create one
	Comments         []Comment // Comments to post
	NeedsMoreInfo    bool      // AI is waiting for responses
	ContinuationData string    // Data to continue conversation later
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
- analyze_file: Examine files to understand the codebase
- create_solution: Generate a complete solution (only when ready)
- post_comment: Post comments to engage in discussion
- add_reaction: React to existing comments
- request_review: Ask specific users for review or input

Choose the appropriate tools based on the situation. You don't always need to create a solution immediately - sometimes discussion is more valuable.`

	// Define all available tools
	tools := []anthropic.ToolParam{
		{
			Name:        "analyze_file",
			Description: anthropic.String("Analyze a file from the repository to understand its structure and content"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "The file path to analyze",
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
			Name:        "create_solution",
			Description: anthropic.String("Create a complete solution with file changes (only use when you have all necessary information)"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: ac.buildSolutionToolProperties(workCtx.IsInitialSolution),
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
		{
			Name:        "mark_ready_to_implement",
			Description: anthropic.String("Indicate that you have enough information and are ready to implement a solution in the next interaction"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"summary": map[string]interface{}{
						"type":        "string",
						"description": "Summary of what will be implemented",
					},
				},
			},
		},
	}

	// Build prompt from work context
	prompt := workCtx.BuildPrompt()

	// Add instructions about current state
	if workCtx.IsInitialSolution && workCtx.Issue != nil {
		prompt += "\n\nThis is a new issue. Analyze it carefully and decide whether to:\n"
		prompt += "1. Ask clarifying questions if requirements are unclear\n"
		prompt += "2. Create a solution if you have enough information\n"
		prompt += "3. Discuss approach or concerns before implementing"
	} else if len(workCtx.NeedsToRespond) > 0 {
		prompt += "\n\nThere are comments that may need your response. Consider whether to:\n"
		prompt += "1. Answer questions or provide clarifications\n"
		prompt += "2. Implement requested changes\n"
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

// processInteractionResponse processes the AI's response and extracts actions
func (ac *AnthropicClient) processInteractionResponse(ctx context.Context, response *anthropic.Message, workCtx *WorkContext) (*InteractionResult, error) {
	result := &InteractionResult{
		Actions:  []Action{},
		Comments: []Comment{},
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

			// Process specific action types
			switch action.Type {
			case "create_solution":
				// Extract solution from action data
				if solution, ok := action.Data["solution"].(*Solution); ok {
					result.Solution = solution
				}

			case "post_comment":
				// Extract comment from action data
				comment := Comment{
					Type: action.Data["comment_type"].(string),
					Body: action.Data["body"].(string),
				}

				if replyTo, ok := action.Data["in_reply_to"].(float64); ok {
					replyToInt := int64(replyTo)
					comment.InReplyTo = &replyToInt
				}

				if filePath, ok := action.Data["file_path"].(string); ok {
					comment.FilePath = filePath
				}

				if line, ok := action.Data["line"].(float64); ok {
					comment.Line = int(line)
				}

				if side, ok := action.Data["side"].(string); ok {
					comment.Side = side
				}

				result.Comments = append(result.Comments, comment)

			case "add_reaction":
				// Handle reactions
				if commentID, ok := action.Data["comment_id"].(float64); ok {
					if reaction, ok := action.Data["reaction"].(string); ok {
						if result.Comments == nil {
							result.Comments = []Comment{}
						}
						// Add a special comment type for reactions
						result.Comments = append(result.Comments, Comment{
							AddReactions: map[int64]string{
								int64(commentID): reaction,
							},
						})
					}
				}

			case "mark_ready_to_implement":
				result.NeedsMoreInfo = false
				result.ContinuationData = action.Data["summary"].(string)

			case "analyze_file", "request_review":
				// These will be handled by the caller
				result.NeedsMoreInfo = true
			}
		}
	}

	// Check if AI is waiting for more information
	if result.Solution == nil && len(result.Comments) > 0 {
		result.NeedsMoreInfo = true
	}

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

	// Special processing for create_solution
	if block.Name == "create_solution" {
		solution, err := ac.parseSolutionFromInput(inputMap, workCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to parse solution: %w", err)
		}
		action.Data["solution"] = solution
	}

	return action, nil
}

// parseSolutionFromInput parses solution data from tool input
func (ac *AnthropicClient) parseSolutionFromInput(input map[string]interface{}, workCtx *WorkContext) (*Solution, error) {
	solution := &Solution{
		Files: make(map[string]FileChange),
	}

	// Extract fields
	if branch, ok := input["branch"].(string); ok {
		solution.Branch = branch
	} else if !workCtx.IsInitialSolution && workCtx.PullRequest != nil && workCtx.PullRequest.Head != nil {
		solution.Branch = *workCtx.PullRequest.Head.Ref
	}

	if commitMsg, ok := input["commit_message"].(string); ok {
		solution.CommitMessage = commitMsg
	} else {
		return nil, fmt.Errorf("missing commit message")
	}

	if desc, ok := input["description"].(string); ok {
		solution.Description = desc
	}

	// Parse files
	if files, ok := input["files"].(map[string]interface{}); ok {
		for path, fileData := range files {
			if fileMap, ok := fileData.(map[string]interface{}); ok {
				fileChange := FileChange{
					Path: path,
				}

				if content, ok := fileMap["content"].(string); ok {
					fileChange.Content = content
				}

				if isNew, ok := fileMap["is_new"].(bool); ok {
					fileChange.IsNew = isNew
				}

				solution.Files[path] = fileChange
			}
		}
	}

	if len(solution.Files) == 0 {
		return nil, fmt.Errorf("solution contains no files")
	}

	return solution, nil
}

// Helper methods

func (ac *AnthropicClient) buildSolutionToolProperties(isInitialSolution bool) map[string]interface{} {
	props := map[string]interface{}{
		"commit_message": map[string]interface{}{
			"type":        "string",
			"description": "Commit message for the changes",
		},
		"files": map[string]interface{}{
			"type":        "object",
			"description": "Map of file paths to their new content",
			"additionalProperties": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Complete file content with actual implementation code",
					},
					"is_new": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether this is a new file",
					},
				},
				"required": []string{"content", "is_new"},
			},
		},
		"description": map[string]interface{}{
			"type":        "string",
			"description": "Description of the solution and what changes were made",
		},
	}

	if isInitialSolution {
		props["branch"] = map[string]interface{}{
			"type":        "string",
			"description": "Name of the branch to create (e.g., fix/issue-123-description)",
		}
	}

	return props
}
