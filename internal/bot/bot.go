// Package bot provides the core bot logic and processing.
package bot

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/config"
	githubpkg "github.com/cchalm/blundering-savant/internal/github"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/tools"
	"github.com/cchalm/blundering-savant/internal/workspace"
)

// Bot represents an AI developer capable of addressing GitHub issues by creating and updating PRs and responding to
// comments from other users
type Bot struct {
	config                 config.Config
	githubClient           *github.Client
	anthropicClient        anthropic.Client
	toolRegistry           *tools.ToolRegistry
	workspaceFactory       workspace.WorkspaceFactory
	resumableConversations ai.ConversationHistoryStore
	botName                string
}

// NewBot creates a new Bot instance
func NewBot(cfg config.Config, githubClient *github.Client) *Bot {
	anthropicClient := anthropic.NewClient(option.WithAPIKey(cfg.AnthropicAPIKey))
	
	toolRegistry := tools.NewToolRegistry()
	workspaceFactory := workspace.NewRemoteValidationWorkspaceFactory(githubClient, cfg.ValidationWorkflowName)
	
	var resumableConversations ai.ConversationHistoryStore
	if cfg.ResumableConversationsDir != "" {
		resumableConversations = ai.NewFileSystemConversationHistoryStore(cfg.ResumableConversationsDir)
	}
	
	return &Bot{
		config:                 cfg,
		githubClient:           githubClient,
		anthropicClient:        anthropicClient,
		toolRegistry:           toolRegistry,
		workspaceFactory:       workspaceFactory,
		resumableConversations: resumableConversations,
		botName:                cfg.GitHubUsername,
	}
}

// Run starts the bot and processes tasks from the provided channel
func (b *Bot) Run(ctx context.Context, tasks <-chan task.Task) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case tsk, ok := <-tasks:
			if !ok {
				return nil // Channel closed
			}
			
			if err := b.processTask(ctx, tsk); err != nil {
				log.Printf("Error processing task for issue #%d: %v", tsk.Issue.GetNumber(), err)
				
				// Set blocked label on error
				if err := b.setLabels(ctx, tsk, []github.Label{githubpkg.LabelBlocked}); err != nil {
					log.Printf("Failed to set blocked label: %v", err)
				}
			}
		}
	}
}

func (b *Bot) processTask(ctx context.Context, tsk task.Task) error {
	log.Printf("Processing task for issue #%d: %s", tsk.Issue.GetNumber(), tsk.Issue.GetTitle())
	
	// Set working label
	if err := b.setLabels(ctx, tsk, []github.Label{githubpkg.LabelWorking}); err != nil {
		log.Printf("Failed to set working label: %v", err)
	}
	
	// Create workspace
	workspace, err := b.workspaceFactory.NewWorkspace(ctx, tsk)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	
	// Create tool context
	toolCtx := &tools.ToolContext{
		Workspace:    workspace,
		Task:         tsk,
		GithubClient: b.githubClient,
	}
	
	// Create conversation
	conversation, err := b.createConversation(ctx, tsk, toolCtx)
	if err != nil {
		return fmt.Errorf("failed to create conversation: %w", err)
	}
	
	// Process the conversation
	if err := b.processConversation(ctx, conversation, tsk, toolCtx); err != nil {
		return fmt.Errorf("failed to process conversation: %w", err)
	}
	
	// Remove working label and set bot-turn label
	if err := b.setLabels(ctx, tsk, []github.Label{githubpkg.LabelBotTurn}); err != nil {
		log.Printf("Failed to set bot-turn label: %v", err)
	}
	
	return nil
}

func (b *Bot) createConversation(ctx context.Context, tsk task.Task, toolCtx *tools.ToolContext) (*ai.ClaudeConversation, error) {
	// Load system prompt
	systemPrompt, err := ai.LoadSystemPrompt()
	if err != nil {
		return nil, fmt.Errorf("failed to load system prompt: %w", err)
	}
	
	// Generate prompt for this task
	prompt, err := ai.GeneratePrompt(tsk)
	if err != nil {
		return nil, fmt.Errorf("failed to generate prompt: %w", err)
	}
	
	// Get tools
	toolParams := b.toolRegistry.GetToolParams()
	
	// Create conversation
	conversation := ai.NewClaudeConversation(
		b.anthropicClient,
		anthropic.ModelClaude3_5Sonnet20241022,
		4096, // max tokens
		toolParams,
		systemPrompt,
	)
	
	// Add initial prompt
	conversation.AddUserMessage(prompt)
	
	return conversation, nil
}

func (b *Bot) processConversation(ctx context.Context, conversation *ai.ClaudeConversation, tsk task.Task, toolCtx *tools.ToolContext) error {
	const maxIterations = 10
	
	for i := 0; i < maxIterations; i++ {
		response, err := conversation.GetResponse(ctx)
		if err != nil {
			return fmt.Errorf("failed to get response from conversation: %w", err)
		}
		
		// Process any tool calls in the response
		hasToolCalls := false
		for _, block := range response.Content {
			if block.Type == anthropic.ContentBlockTypeToolUse {
				hasToolCalls = true
				toolUseBlock := block.ToolUse
				
				tool := b.toolRegistry.GetTool(toolUseBlock.Name)
				if tool == nil {
					return fmt.Errorf("unknown tool: %s", toolUseBlock.Name)
				}
				
				result, err := tool.Run(ctx, toolUseBlock, toolCtx)
				if err != nil {
					conversation.AddToolResult(toolUseBlock.ID, err.Error())
				} else {
					if result != nil {
						conversation.AddToolResult(toolUseBlock.ID, *result)
					} else {
						conversation.AddToolResult(toolUseBlock.ID, "Success")
					}
				}
			}
		}
		
		// If there were no tool calls, we're done
		if !hasToolCalls {
			break
		}
	}
	
	return nil
}

func (b *Bot) setLabels(ctx context.Context, tsk task.Task, labels []github.Label) error {
	owner := tsk.Issue.Owner
	repo := tsk.Issue.Repository
	issueNumber := tsk.Issue.GetNumber()
	
	// Get current labels
	currentLabels, _, err := b.githubClient.Issues.ListLabelsByIssue(ctx, owner, repo, issueNumber, nil)
	if err != nil {
		return fmt.Errorf("failed to get current labels: %w", err)
	}
	
	// Remove bot labels
	var newLabels []string
	botLabelNames := map[string]bool{
		githubpkg.LabelWorking.GetName(): true,
		githubpkg.LabelBlocked.GetName(): true,
		githubpkg.LabelBotTurn.GetName(): true,
	}
	
	for _, label := range currentLabels {
		if !botLabelNames[label.GetName()] {
			newLabels = append(newLabels, label.GetName())
		}
	}
	
	// Add requested labels
	for _, label := range labels {
		newLabels = append(newLabels, label.GetName())
	}
	
	// Set labels
	_, _, err = b.githubClient.Issues.ReplaceLabelsForIssue(ctx, owner, repo, issueNumber, newLabels)
	if err != nil {
		return fmt.Errorf("failed to set labels: %w", err)
	}
	
	return nil
}