package main

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
)

var (
	LabelWorking = github.Label{
		Name:        github.Ptr("bot-working"),
		Description: github.Ptr("the bot is actively working on this issue"),
		Color:       github.Ptr("fbca04"),
	}
	LabelBlocked = github.Label{
		Name:        github.Ptr("bot-blocked"),
		Description: github.Ptr("the bot encountered a problem and needs human intervention to continue working on this issue"),
		Color:       github.Ptr("f03010"),
	}
	LabelBotTurn = github.Label{
		Name:        github.Ptr("bot-turn"),
		Description: github.Ptr("it is the bot's turn to take action on this issue"),
		Color:       github.Ptr("2020f0"),
	}
)

// Bot represents an AI developer capable of addressing GitHub issues by creating and updating PRs and responding to
// comments from other users
type Bot struct {
	config                 Config
	githubClient           *github.Client
	anthropicClient        anthropic.Client
	toolRegistry           *ToolRegistry
	workspaceFactory       WorkspaceFactory
	resumableConversations ConversationHistoryStore
	botName                string
}

type ConversationHistoryStore interface {
	// Get returns the conversation history stored at the given key, or nil if there is nothing stored at that key
	Get(key string) (*conversationHistory, error)
	// Set stores a conversation history with a key
	Set(key string, value conversationHistory) error
	// Delete deletes the conversation history stored at the given key
	Delete(key string) error
}

// Workspace represents a three-stage development process: local changes, validation, and review. Callers make local
// changes using the FileSystem interface, validate them with ValidateChanges, and publish them for review using
// PublishChangesForReview
type Workspace interface {
	FileSystem

	// HasLocalChanges returns true if there are local (unvalidated) changes in the workspace
	HasLocalChanges() bool
	// ClearChanges clears any local (unvalidated) changes in the workspace
	ClearLocalChanges()

	// HasUnpublishedChanged returns true if there are validated changes that have not been published for review
	HasUnpublishedChanges(ctx context.Context) (bool, error)

	// ValidateChanges persists local changes remotely, validates them, and returns the results. A commit message must
	// be provided if there are local changes in the workspace. After calling ValidateChanges, there will be no local
	// changes in the workspace.
	ValidateChanges(ctx context.Context, commitMessage *string) (ValidationResult, error)
	// PublishChangesForReview makes validated changes available for review. reviewRequestTitle and reviewRequestBody
	// are only used the first time a review is published, subsequent publishes will ignore these parameters and update
	// the existing review
	PublishChangesForReview(ctx context.Context, reviewRequestTitle string, reviewRequestBody string) error
}

type WorkspaceFactory interface {
	NewWorkspace(ctx context.Context, tsk task) (Workspace, error)
}

type ValidationResult struct {
	Succeeded bool
	Details   string
}

func NewBot(config Config, githubClient *github.Client) *Bot {
	rateLimitedHTTPClient := &http.Client{
		Transport: WithRateLimiting(nil),
	}
	anthropicClient := anthropic.NewClient(
		option.WithHTTPClient(rateLimitedHTTPClient),
		option.WithAPIKey(config.AnthropicAPIKey),
		option.WithMaxRetries(5),
	)

	return &Bot{
		config:                 config,
		githubClient:           githubClient,
		anthropicClient:        anthropicClient,
		toolRegistry:           NewToolRegistry(),
		workspaceFactory:       remoteValidationWorkspaceFactory{githubClient: githubClient},
		resumableConversations: FileSystemConversationHistoryStore{dir: config.ResumableConversationsDir},
		botName:                config.GitHubUsername,
	}
}

// Run starts the main loop
func (b *Bot) Run(ctx context.Context, tasks <-chan taskOrError) error {
	for taskOrError := range tasks {
		tsk, err := taskOrError.task, taskOrError.err
		if err != nil {
			return err
		}

		err = b.doTask(ctx, tsk)

		if err != nil {
			// Add blocked label if there is an error, to tell the bot not to pick up this item again
			if err := b.addLabel(ctx, tsk.Issue, LabelBlocked); err != nil {
				log.Printf("failed to add blocked label: %v", err)
			}
			// Post sanitized error comment
			msg := "❌ I encountered an error while working on this issue."
			if err := b.postIssueComment(ctx, tsk.Issue, msg); err != nil {
				log.Printf("failed to post error comment: %v", err)
			}
			// Log the error and continue processing other tasks
			log.Printf("failed to process task for issue %d: %v", tsk.Issue.number, err)
		}
	}

	return nil
}

func (b *Bot) doTask(ctx context.Context, tsk task) (err error) {
	if err := b.addLabel(ctx, tsk.Issue, LabelWorking); err != nil {
		log.Printf("failed to add in-progress label: %v", err)
	}
	defer func() {
		if err := b.removeLabel(ctx, tsk.Issue, LabelWorking); err != nil {
			log.Printf("failed to remove in-progress label: %v", err)
		}
	}()

	workspace, err := b.workspaceFactory.NewWorkspace(ctx, tsk)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	// Do some prep work to avoid unnecessary back-and-forths with the AI

	hasUnpublishedChanges, err := workspace.HasUnpublishedChanges(ctx)
	if err != nil {
		return fmt.Errorf("failed to check for unpublished changes: %w", err)
	}

	validationResult, err := workspace.ValidateChanges(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch validation results: %w", err)
	}

	tsk.HasUnpublishedChanges = hasUnpublishedChanges
	tsk.ValidationResult = validationResult

	// Let the AI do its thing
	err = b.processWithAI(ctx, tsk, workspace)
	if err != nil {
		return fmt.Errorf("failed to process with AI: %w", err)
	}

	return nil
}

// processWithAI handles the AI interaction with text editor tool support
func (b *Bot) processWithAI(ctx context.Context, task task, workspace Workspace) error {
	maxIterations := 50

	// Create tool context
	toolCtx := &ToolContext{
		Workspace:    workspace,
		Task:         task,
		GithubClient: b.githubClient,
	}

	// Initialize conversation
	conversation, response, err := b.initConversation(ctx, task, toolCtx)
	if err != nil {
		return fmt.Errorf("failed to initialize conversation: %w", err)
	}

	i := 0
	for response.StopReason != anthropic.StopReasonEndTurn {
		if i > maxIterations {
			return fmt.Errorf("exceeded maximum iterations (%d) without completion", maxIterations)
		}
		// Persist the conversation history up to this point
		err = b.resumableConversations.Set(strconv.Itoa(task.Issue.number), conversation.History())
		if err != nil {
			return fmt.Errorf("failed to persist conversation history: %w", err)
		}

		log.Printf("Processing AI response, iteration: %d", i+1)
		for _, contentBlock := range response.Content {
			switch block := contentBlock.AsAny().(type) {
			case anthropic.TextBlock:
				log.Print("    <text> ", block.Text)
			case anthropic.ToolUseBlock:
				log.Print("    <tool use> ", block.Name)
			case anthropic.ServerToolUseBlock:
				log.Print("    <server tool use> ", block.Name)
			case anthropic.WebSearchToolResultBlock:
				log.Print("    <web search tool result>")
			case anthropic.ThinkingBlock:
				log.Print("    <thinking>", block.Thinking)
			case anthropic.RedactedThinkingBlock:
				log.Print("    <redacted thinking>")
			default:
				log.Print("    <unknown>")
			}
		}

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

	// We're done! Delete the conversation history so that we don't try to resume it later
	err = b.resumableConversations.Delete(strconv.Itoa(task.Issue.number))
	if err != nil {
		return fmt.Errorf("failed to delete conversation history for concluded conversation: %w", err)
	}

	err = b.removeLabel(ctx, task.Issue, LabelBotTurn)
	if err != nil {
		return fmt.Errorf("failed to remove bot turn label: %w", err)
	}

	log.Print("AI interaction concluded")
	return nil
}

// Helper functions

func (b *Bot) postIssueComment(ctx context.Context, issue githubIssue, body string) error {
	comment := &github.IssueComment{
		Body: github.Ptr(body),
	}
	_, _, err := b.githubClient.Issues.CreateComment(ctx, issue.owner, issue.repo, issue.number, comment)
	return err
}

// Label management functions

// addLabel adds a label to an issue
func (b *Bot) addLabel(ctx context.Context, issue githubIssue, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot add label with nil name")
	}
	if err := b.ensureLabelExists(ctx, issue.owner, issue.repo, label); err != nil {
		log.Printf("Warning: Could not ensure label exists: %v", err)
	}

	labels := []string{*label.Name}
	_, _, err := b.githubClient.Issues.AddLabelsToIssue(ctx, issue.owner, issue.repo, issue.number, labels)
	return err
}

// removeLabel removes a label from an issue, if present
func (b *Bot) removeLabel(ctx context.Context, issue githubIssue, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot remove label with nil name")
	}
	resp, err := b.githubClient.Issues.RemoveLabelForIssue(ctx, issue.owner, issue.repo, issue.number, *label.Name)
	if err != nil && resp.StatusCode == http.StatusNotFound {
		// If the label isn't present, ignore the error
		return nil
	}
	return err
}

// ensureLabelExists creates a label if it doesn't exist
func (b *Bot) ensureLabelExists(ctx context.Context, owner, repo string, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("nil label name")
	}
	_, _, err := b.githubClient.Issues.GetLabel(ctx, owner, repo, *label.Name)
	if err == nil {
		return nil
	}

	_, _, err = b.githubClient.Issues.CreateLabel(ctx, owner, repo, &label)
	return err
}

// Utility functions

//go:embed system_prompt.md
var systemPrompt string

// initConversation either constructs a new conversation or resumes a previous conversation
func (b *Bot) initConversation(ctx context.Context, tsk task, toolCtx *ToolContext) (*ClaudeConversation, *anthropic.Message, error) {
	model := anthropic.ModelClaudeSonnet4_0
	var maxTokens int64 = 64000

	conversationStr, err := b.resumableConversations.Get(strconv.Itoa(tsk.Issue.number))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to look up resumable conversation by issue number: %w", err)
	}
	tools := b.toolRegistry.GetAllToolParams()

	if conversationStr != nil {
		conv, err := ResumeClaudeConversation(b.anthropicClient, *conversationStr, model, maxTokens, tools)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resume conversation: %w", err)
		}

		err = b.rerunStatefulToolCalls(ctx, toolCtx, conv)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to rerun stateful tool calls: %w", err)
		}

		// Extract the last message of the resumed conversation. If it is a user message, send it and return the
		// response. If it is an assistant response, simply return that
		lastTurn := conv.messages[len(conv.messages)-1]
		var response *anthropic.Message
		if lastTurn.Response != nil {
			// We should be careful here. Assistant message handling is not necessarily idempotent, e.g. if the bot
			// sends a message with two tool calls and we get through one of them before encountering an error with the
			// second, the handling of the first tool call may have had side effects that would be damaging to repeat.
			// Consider implementing transactions with rollback for parallel tool calls.

			// Resuming from a response
			log.Printf("Resuming previous conversation from an assistant message")
			response = lastTurn.Response
		} else {
			// Resuming from a user message
			log.Printf("Resuming previous conversation from a user message - sending message")
			r, err := conv.SendMessage(ctx, lastTurn.UserMessage.Content...)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to send last message of resumed conversation: %w", err)
			}
			response = r
		}
		return conv, response, nil
	} else {
		c := NewClaudeConversation(b.anthropicClient, model, maxTokens, tools, systemPrompt)

		log.Printf("Sending initial message to AI")
		promptPtr, err := BuildPrompt(tsk)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build prompt: %w", err)
		}
		// TODO remove this
		if err := os.WriteFile("logs/prompt.md", []byte(*promptPtr), 0644); err != nil {
			log.Printf("Warning: failed to write prompt to file for debugging: %v", err)
		}
		// Send initial message with a cache breakpoint, because the initial message tends to be very large and we are
		// likely to need several back-and-forths after this
		response, err := c.SendMessageAndSetCachePoint(ctx, anthropic.NewTextBlock(*promptPtr))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to send initial message to AI: %w", err)
		}
		return c, response, nil
	}
}

func (b *Bot) rerunStatefulToolCalls(ctx context.Context, toolCtx *ToolContext, conversation *ClaudeConversation) error {
	for turnNumber, turn := range conversation.messages {
		if turnNumber == len(conversation.messages)-1 {
			// Skip the last message in the conversation, since this message was not previously handled
			break
		}
		for _, block := range turn.Response.Content {
			switch toolUseBlock := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				err := b.toolRegistry.ReplayToolUse(ctx, toolUseBlock, toolCtx)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
