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

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/validator"
	"github.com/cchalm/blundering-savant/internal/workspace"
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

	user *github.User
}

type ConversationHistoryStore interface {
	// Get returns the conversation history stored at the given key, or nil if there is nothing stored at that key
	Get(key string) (*ai.ConversationHistory, error)
	// Set stores a conversation history with a key
	Set(key string, value ai.ConversationHistory) error
	// Delete deletes the conversation history stored at the given key
	Delete(key string) error
}

// Workspace represents a three-stage development process: local changes, validation, and review. Callers make local
// changes using the FileSystem interface, validate them with ValidateChanges, and publish them for review using
// PublishChangesForReview
type Workspace interface {
	workspace.FileSystem

	// HasLocalChanges returns true if there are local (unvalidated) changes in the workspace
	HasLocalChanges() bool
	// ClearChanges clears any local (unvalidated) changes in the workspace
	ClearLocalChanges()

	// HasUnpublishedChanged returns true if there are validated changes that have not been published for review
	HasUnpublishedChanges(ctx context.Context) (bool, error)

	// ValidateChanges persists local changes remotely, validates them, and returns the results. A commit message must
	// be provided if there are local changes in the workspace. After calling ValidateChanges, there will be no local
	// changes in the workspace.
	ValidateChanges(ctx context.Context, commitMessage *string) (validator.ValidationResult, error)
	// PublishChangesForReview makes validated changes available for review. reviewRequestTitle and reviewRequestBody
	// are only used the first time a review is published, subsequent publishes will ignore these parameters and update
	// the existing review. PublishChangesForReview will return an error if there are unvalidated local changes in the
	// workspace; all local changes must be validated before calling PublishChangesForReview
	PublishChangesForReview(ctx context.Context, reviewRequestTitle string, reviewRequestBody string) error
}

type WorkspaceFactory interface {
	NewWorkspace(ctx context.Context, tsk task.Task) (Workspace, error)
}

func NewBot(config Config, githubClient *github.Client, githubUser *github.User) *Bot {
	rateLimitedHTTPClient := &http.Client{
		Transport: ai.WithRateLimiting(nil),
	}
	anthropicClient := anthropic.NewClient(
		option.WithHTTPClient(rateLimitedHTTPClient),
		option.WithAPIKey(config.AnthropicAPIKey),
		option.WithMaxRetries(5),
	)

	return &Bot{
		config:          config,
		githubClient:    githubClient,
		anthropicClient: anthropicClient,
		toolRegistry:    NewToolRegistry(),
		workspaceFactory: remoteValidationWorkspaceFactory{
			githubClient:           githubClient,
			validationWorkflowName: config.ValidationWorkflowName,
		},
		resumableConversations: ai.NewFileSystemConversationHistoryStore(
			config.ResumableConversationsDir,
		),
		user: githubUser,
	}
}

// Run starts the main loop
func (b *Bot) Run(ctx context.Context, tasks <-chan task.TaskOrError) error {
	for taskOrError := range tasks {
		tsk, err := taskOrError.Task, taskOrError.Err
		if err != nil {
			return err
		}

		err = b.doTask(ctx, tsk)

		if err != nil {
			// Add blocked label if there is an error, to tell the bot not to pick up this item again
			if err := b.addLabel(ctx, tsk.Issue, task.LabelBlocked); err != nil {
				log.Printf("failed to add blocked label: %v", err)
			}
			// Post sanitized error comment
			msg := "❌ I encountered an error while working on this issue."
			if err := b.postIssueComment(ctx, tsk.Issue, msg); err != nil {
				log.Printf("failed to post error comment: %v", err)
			}
			// Log the error and continue processing other tasks
			log.Printf("failed to process task for issue %d: %v", tsk.Issue.Number, err)
		}
	}

	return nil
}

func (b *Bot) doTask(ctx context.Context, tsk task.Task) (err error) {
	if err := b.addLabel(ctx, tsk.Issue, task.LabelWorking); err != nil {
		log.Printf("failed to add in-progress label: %v", err)
	}
	defer func() {
		if err := b.removeLabel(ctx, tsk.Issue, task.LabelWorking); err != nil {
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
func (b *Bot) processWithAI(ctx context.Context, tsk task.Task, workspace Workspace) error {
	maxIterations := 500

	// Create tool context
	toolCtx := &ToolContext{
		Workspace:    workspace,
		Task:         tsk,
		GithubClient: b.githubClient,
	}

	// Initialize conversation
	conversation, response, err := b.initConversation(ctx, tsk, toolCtx)
	if err != nil {
		return fmt.Errorf("failed to initialize conversation: %w", err)
	}

	i := 0
	for response.StopReason != anthropic.StopReasonEndTurn {
		if i > maxIterations {
			return fmt.Errorf("exceeded maximum iterations (%d) without completion", maxIterations)
		}
		// Persist the conversation history up to this point
		err = b.resumableConversations.Set(strconv.Itoa(tsk.Issue.Number), conversation.History())
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

		if s, err := conversation.ToMarkdown(); err != nil {
			log.Printf("Warning: failed to serialize conversation as markdown: %v", err)
		} else if err := os.WriteFile(fmt.Sprintf("logs/conversation_issue_%d.md", tsk.Issue.Number), []byte(s), 0666); err != nil {
			log.Printf("Warning: failed to write conversation to markdown file for debugging: %v", err)
		}

		i++
	}

	// We're done! Delete the conversation history so that we don't try to resume it later
	err = b.resumableConversations.Delete(strconv.Itoa(tsk.Issue.Number))
	if err != nil {
		return fmt.Errorf("failed to delete conversation history for concluded conversation: %w", err)
	}

	err = b.removeLabel(ctx, tsk.Issue, task.LabelBotTurn)
	if err != nil {
		return fmt.Errorf("failed to remove bot turn label: %w", err)
	}

	log.Print("AI interaction concluded")
	return nil
}

// Helper functions

func (b *Bot) postIssueComment(ctx context.Context, issue task.GithubIssue, body string) error {
	comment := &github.IssueComment{
		Body: github.Ptr(body),
	}
	_, _, err := b.githubClient.Issues.CreateComment(ctx, issue.Owner, issue.Repo, issue.Number, comment)
	return err
}

// Label management functions

// addLabel adds a label to an issue
func (b *Bot) addLabel(ctx context.Context, issue task.GithubIssue, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot add label with nil name")
	}
	if err := b.ensureLabelExists(ctx, issue.Owner, issue.Repo, label); err != nil {
		log.Printf("Warning: Could not ensure label exists: %v", err)
	}

	labels := []string{*label.Name}
	_, _, err := b.githubClient.Issues.AddLabelsToIssue(ctx, issue.Owner, issue.Repo, issue.Number, labels)
	return err
}

// removeLabel removes a label from an issue, if present
func (b *Bot) removeLabel(ctx context.Context, issue task.GithubIssue, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot remove label with nil name")
	}
	resp, err := b.githubClient.Issues.RemoveLabelForIssue(ctx, issue.Owner, issue.Repo, issue.Number, *label.Name)
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

// initConversation either constructs a new conversation or resumes a previous conversation
func (b *Bot) initConversation(ctx context.Context, tsk task.Task, toolCtx *ToolContext) (*ai.ClaudeConversation, *anthropic.Message, error) {
	model := anthropic.ModelClaudeSonnet4_0
	var maxTokens int64 = 64000

	conversationStr, err := b.resumableConversations.Get(strconv.Itoa(tsk.Issue.Number))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to look up resumable conversation by issue number: %w", err)
	}
	tools := b.toolRegistry.GetAllToolParams()

	if conversationStr != nil {
		conv, err := ai.ResumeClaudeConversation(b.anthropicClient, *conversationStr, model, maxTokens, tools)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resume conversation: %w", err)
		}

		err = b.rerunStatefulToolCalls(ctx, toolCtx, conv)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to rerun stateful tool calls: %w", err)
		}

		// Extract the last message of the resumed conversation. If it is a user message, send it and return the
		// response. If it is an assistant response, simply return that
		lastTurn := conv.Messages[len(conv.Messages)-1]
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
		systemPrompt, err := ai.BuildSystemPrompt("Blundering Savant", *b.user.Login)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build system prompt: %w", err)
		}

		c := ai.NewClaudeConversation(b.anthropicClient, model, maxTokens, tools, systemPrompt)

		log.Printf("Sending initial message to AI")
		repositoryContent, taskContent, err := ai.BuildPrompt(tsk)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build prompt: %w", err)
		}

		// Send repository content as cacheable block, followed by task-specific content
		repositoryBlock := anthropic.NewTextBlock(repositoryContent)
		taskBlock := anthropic.NewTextBlock(taskContent)

		response, err := c.SendMessage(ctx, repositoryBlock, taskBlock)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to send initial message to AI: %w", err)
		}
		return c, response, nil
	}
}

func (b *Bot) rerunStatefulToolCalls(ctx context.Context, toolCtx *ToolContext, conversation *ai.ClaudeConversation) error {
	for turnNumber, turn := range conversation.Messages {
		if turnNumber == len(conversation.Messages)-1 {
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

// remoteValidationWorkspaceFactory creates instances of remoteValidationWorkspace
type remoteValidationWorkspaceFactory struct {
	githubClient           *github.Client
	validationWorkflowName string
}

func (rvwf remoteValidationWorkspaceFactory) NewWorkspace(ctx context.Context, tsk task.Task) (Workspace, error) {
	return workspace.NewRemoteValidationWorkspace(ctx, rvwf.githubClient, rvwf.validationWorkflowName, tsk)
}
