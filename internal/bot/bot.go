// Package bot provides the core bot logic and processing.
package bot

import (
	"context"
	_ "embed"
	"fmt"
	"log"

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
	
	// Initialize conversation
	conversation, response, err := b.initConversation(ctx, tsk, toolCtx)
	if err != nil {
		return fmt.Errorf("failed to initialize conversation: %w", err)
	}
	
	// Process the conversation
	if err := b.processConversation(ctx, conversation, response, tsk, toolCtx); err != nil {
		return fmt.Errorf("failed to process conversation: %w", err)
	}
	
	// Remove working label and set bot-turn label
	if err := b.setLabels(ctx, tsk, []github.Label{githubpkg.LabelBotTurn}); err != nil {
		log.Printf("Failed to set bot-turn label: %v", err)
	}
	
	return nil
}

func (b *Bot) initConversation(ctx context.Context, tsk task.Task, toolCtx *tools.ToolContext) (*ai.ClaudeConversation, *anthropic.Message, error) {
	model := anthropic.ModelClaude3_5Sonnet20241022
	var maxTokens int64 = 4096

	// Load system prompt
	systemPrompt, err := ai.LoadSystemPrompt()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load system prompt: %w", err)
	}
	
	// Generate prompt for this task
	prompt, err := ai.GeneratePrompt(tsk)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate prompt: %w", err)
	}
	
	// Get tools
	toolParams := b.toolRegistry.GetToolParams()
	
	// Create conversation
	conversation := ai.NewClaudeConversation(
		b.anthropicClient,
		model,
		maxTokens,
		toolParams,
		systemPrompt,
	)
	
	// Send initial message
	log.Printf("Sending initial message to AI")
	response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to send initial message to AI: %w", err)
	}
	
	return conversation, response, nil
}

func (b *Bot) processConversation(ctx context.Context, conversation *ai.ClaudeConversation, response *anthropic.Message, tsk task.Task, toolCtx *tools.ToolContext) error {
	const maxIterations = 10
	
	i := 0
	for response.StopReason != anthropic.StopReasonEndTurn {
		if i > maxIterations {
			return fmt.Errorf("exceeded maximum iterations (%d) without completion", maxIterations)
		}
		
		log.Printf("Processing AI response, iteration: %d", i+1)
		
		switch response.StopReason {
		case anthropic.StopReasonToolUse:
			// Process tool uses and collect tool results
			toolUses := []anthropic.ToolUseBlock{}
			for _, content := range response.Content {
				switch block := content.AsAny().(type) {
				case anthropic.ToolUseBlock:
					toolUses = append(toolUses, block)
				}
			}

			toolResults := []anthropic.ContentBlockParamUnion{}
			for _, toolUse := range toolUses {
				log.Printf("    Executing tool: %s", toolUse.Name)

				// Process the tool use with the registry
				toolResult, err := b.toolRegistry.ProcessToolUse(ctx, toolUse, toolCtx)
				if err != nil {
					return fmt.Errorf("failed to process tool use: %w", err)
				}
				toolResults = append(toolResults, anthropic.ContentBlockParamUnion{OfToolResult: toolResult})
			}
			log.Printf("    Sending tool results to AI and streaming response")
			var err error
			response, err = conversation.SendMessage(ctx, toolResults...)
			if err != nil {
				return fmt.Errorf("failed to send tool results to AI: %w", err)
			}
		case anthropic.StopReasonMaxTokens:
			return fmt.Errorf("exceeded max tokens")
		case anthropic.StopReasonRefusal:
			return fmt.Errorf("the AI refused to generate a response due to safety concerns")
		case anthropic.StopReasonEndTurn:
			return fmt.Errorf("that's weird, it shouldn't be possible to reach this branch")
		default:
			return fmt.Errorf("unexpected stop reason: %v", response.StopReason)
		}

		i++
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