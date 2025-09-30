package bot

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/validator"
	"github.com/cchalm/blundering-savant/internal/workspace"
)

// Bot represents an AI developer capable of addressing GitHub issues by creating and updating PRs and responding to
// comments from other users
type Bot struct {
	githubClient           *github.Client
	sender                 ai.MessageSender
	toolRegistry           *ToolRegistry
	workspaceFactory       WorkspaceFactory
	resumableConversations ConversationHistoryStore // May be nil

	tokenLimit int64 // Determines when conversation summarization is triggered

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

func New(
	githubClient *github.Client,
	githubUser *github.User,
	sender ai.MessageSender,
	historyStore ConversationHistoryStore,
	workspaceFactory WorkspaceFactory,
) *Bot {
	return &Bot{
		githubClient:           githubClient,
		sender:                 sender,
		toolRegistry:           NewToolRegistry(),
		workspaceFactory:       workspaceFactory,
		resumableConversations: historyStore,
		tokenLimit:             100000, // Use a limit of 100k tokens, half of the context limit of 200k
		user:                   githubUser,
	}
}

// Run starts the main loop
func (b *Bot) Run(ctx context.Context, tasks <-chan task.TaskOrError) error {
	for taskOrError := range tasks {
		tsk, err := taskOrError.Task, taskOrError.Err
		if err != nil {
			return err
		}

		err = b.DoTask(ctx, tsk)

		if err != nil {
			// Log the error and continue processing other tasks
			log.Printf("failed to process task for issue %d: %v", tsk.Issue.Number, err)
		}
	}

	return nil
}

func (b *Bot) DoTask(ctx context.Context, tsk task.Task) (err error) {
	if err := addLabel(ctx, b.githubClient.Issues, tsk.Issue, task.LabelWorking); err != nil {
		log.Printf("failed to add in-progress label: %v", err)
	}
	defer func() {
		if err := removeLabel(ctx, b.githubClient.Issues, tsk.Issue, task.LabelWorking); err != nil {
			log.Printf("failed to remove in-progress label: %v", err)
		}

		if err != nil {
			// Add blocked label if there is an error, to tell the bot not to pick up this item again
			if err := addLabel(ctx, b.githubClient.Issues, tsk.Issue, task.LabelBlocked); err != nil {
				log.Printf("failed to add blocked label: %v", err)
			}
			// Post sanitized error comment
			msg := "❌ I encountered an error while working on this issue."
			if err := b.postIssueComment(ctx, tsk.Issue, msg); err != nil {
				log.Printf("failed to post error comment: %v", err)
			}
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

		if b.resumableConversations != nil {
			// Persist the conversation history up to this point
			err = b.resumableConversations.Set(strconv.Itoa(tsk.Issue.Number), conversation.History())
			if err != nil {
				return fmt.Errorf("failed to persist conversation history: %w", err)
			}
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
			// Execute tool uses and add results to conversation
			err = b.runTools(ctx, toolCtx, conversation)
			if err != nil {
				return err
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

		log.Printf("    Responding to AI")
		response, err = sendMessage(ctx, conversation, b.tokenLimit)
		if err != nil {
			return err
		}

		if s, err := conversation.ToMarkdown(); err != nil {
			log.Printf("Warning: failed to serialize conversation as markdown: %v", err)
		} else if err := os.MkdirAll("logs", os.ModePerm); err != nil {
			log.Printf("Warning: failed to create logs directory: %v", err)
		} else if err := os.WriteFile(fmt.Sprintf("logs/conversation_issue_%d.md", tsk.Issue.Number), []byte(s), 0666); err != nil {
			log.Printf("Warning: failed to write conversation to markdown file for debugging: %v", err)
		}

		i++
	}

	// We're done!

	if b.resumableConversations != nil {
		// Delete the conversation history so that we don't try to resume it later
		err = b.resumableConversations.Delete(strconv.Itoa(tsk.Issue.Number))
		if err != nil {
			return fmt.Errorf("failed to delete conversation history for concluded conversation: %w", err)
		}
	}

	err = removeLabel(ctx, b.githubClient.Issues, tsk.Issue, task.LabelBotTurn)
	if err != nil {
		return fmt.Errorf("failed to remove bot turn label: %w", err)
	}

	log.Print("AI interaction concluded")
	return nil
}

// sendMessage sends a message in the given conversation with summarization behavior to avoid token limits
func sendMessage(
	ctx context.Context,
	conversation *ai.Conversation,
	tokenLimit int64,
	instructions ...anthropic.ContentBlockParamUnion,
) (*anthropic.Message, error) {

	if tokenUsageExceedsLimit(conversation, tokenLimit) {
		keepFirst, keepLast := 0, 10 // Keep the last 10 messages
		err := summarize(ctx, conversation, keepFirst, keepLast)
		if err != nil {
			return nil, err
		}
	}

	response, err := conversation.SendMessage(ctx, instructions...)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return response, nil
}

// needsSummarization checks if the conversation should be summarized due to token limits
func tokenUsageExceedsLimit(conversation *ai.Conversation, tokenLimit int64) bool {
	if len(conversation.Turns) == 0 {
		return false
	}

	// Get the most recent response
	lastMessage := conversation.Turns[len(conversation.Turns)-1]
	if lastMessage.Response == nil {
		return false
	}

	// Check token usage from the most recent turn (which includes cumulative history)
	// Include cache create tokens as they contribute to context size
	totalTokens := lastMessage.Response.Usage.InputTokens +
		lastMessage.Response.Usage.CacheReadInputTokens +
		lastMessage.Response.Usage.CacheCreationInputTokens
	return totalTokens > tokenLimit
}

// runTools executes pending tool calls and adds their results to the conversation
func (b *Bot) runTools(ctx context.Context, toolCtx *ToolContext, conversation *ai.Conversation) error {
	pendingToolUses := conversation.GetPendingToolUses()

	if len(pendingToolUses) == 0 {
		log.Printf("    WARNING: Stop reason was 'tool_use', but no pending tool uses found. This shouldn't happen.")
		// Add an error message as an instruction so the AI can self-correct
		_, err := conversation.SendMessage(ctx, anthropic.NewTextBlock("Error: No tool uses found in message. Was there a formatting issue?"))
		return err
	}

	for _, toolUse := range pendingToolUses {
		log.Printf("    Executing tool: %s", toolUse.Name)

		// Process the tool use with the registry
		toolResult, err := b.toolRegistry.ProcessToolUse(ctx, toolUse, toolCtx)
		if err != nil {
			return fmt.Errorf("failed to process tool use: %w", err)
		}

		// Add the result to the conversation
		err = conversation.AddToolResult(*toolResult)
		if err != nil {
			return fmt.Errorf("failed to add tool result: %w", err)
		}
	}

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
func addLabel(ctx context.Context, issuesService *github.IssuesService, issue task.GithubIssue, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot add label with nil name")
	}
	if err := ensureLabelExists(ctx, issuesService, issue.Owner, issue.Repo, label); err != nil {
		log.Printf("Warning: Could not ensure label exists: %v", err)
	}

	labels := []string{*label.Name}
	_, _, err := issuesService.AddLabelsToIssue(ctx, issue.Owner, issue.Repo, issue.Number, labels)
	return err
}

// removeLabel removes a label from an issue, if present
func removeLabel(ctx context.Context, issuesService *github.IssuesService, issue task.GithubIssue, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot remove label with nil name")
	}
	resp, err := issuesService.RemoveLabelForIssue(ctx, issue.Owner, issue.Repo, issue.Number, *label.Name)
	if err != nil && resp.StatusCode == http.StatusNotFound {
		// If the label isn't present, ignore the error
		return nil
	}
	return err
}

// ensureLabelExists creates a label if it doesn't exist
func ensureLabelExists(ctx context.Context, issuesService *github.IssuesService, owner, repo string, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("nil label name")
	}
	_, _, err := issuesService.GetLabel(ctx, owner, repo, *label.Name)
	if err == nil {
		return nil
	}

	_, _, err = issuesService.CreateLabel(ctx, owner, repo, &label)
	return err
}

// Utility functions

// initConversation either constructs a new conversation or resumes a previous conversation
func (b *Bot) initConversation(ctx context.Context, tsk task.Task, toolCtx *ToolContext) (*ai.Conversation, *anthropic.Message, error) {
	model := anthropic.ModelClaudeSonnet4_5
	var maxTokens int64 = 64000

	tools := b.toolRegistry.GetAllToolParams()

	var history *ai.ConversationHistory
	if b.resumableConversations != nil {
		// Check if there is a resumable conversation for this task
		var err error
		history, err = b.resumableConversations.Get(strconv.Itoa(tsk.Issue.Number))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to look up resumable conversation by issue number: %w", err)
		}
	}

	if history != nil {
		return b.resumeConversation(ctx, *history, model, maxTokens, tools, toolCtx)
	} else {
		return b.newConversation(ctx, tsk, model, maxTokens, tools)
	}
}

func (b *Bot) resumeConversation(
	ctx context.Context,
	history ai.ConversationHistory,
	model anthropic.Model,
	maxTokens int64,
	tools []anthropic.ToolParam,
	toolCtx *ToolContext,
) (*ai.Conversation, *anthropic.Message, error) {
	conv, err := ai.ResumeConversation(b.sender, history, model, maxTokens, tools)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resume conversation: %w", err)
	}

	err = b.rerunStatefulToolCalls(ctx, toolCtx, conv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to rerun stateful tool calls: %w", err)
	}

	// Extract the last message of the resumed conversation. If tool uses are already resolved, send the next
	// message. Otherwise, return the response from the last message
	var response *anthropic.Message
	if len(conv.GetPendingToolUses()) == 0 {
		log.Printf("Resuming previous conversation from a completed turn - sending next message")
		r, err := conv.SendMessage(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to send message: %w", err)
		}
		response = r
	} else {
		log.Printf("Resuming previous conversation from an incomplete turn - returning previous response")

		// We should be careful here. Assistant message handling is not necessarily idempotent, e.g. if the bot
		// sends a message with two tool calls and we get through one of them before encountering an error with the
		// second, the handling of the first tool call may have had side effects that would be damaging to repeat.
		// Consider implementing transactions with rollback for parallel tool calls.

		lastTurn := conv.Turns[len(conv.Turns)-1]
		response = lastTurn.Response
	}
	return conv, response, nil
}

func (b *Bot) newConversation(
	ctx context.Context,
	tsk task.Task,
	model anthropic.Model,
	maxTokens int64,
	tools []anthropic.ToolParam,
) (*ai.Conversation, *anthropic.Message, error) {
	systemPrompt, err := buildSystemPrompt("Blundering Savant", *b.user.Login)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build system prompt: %w", err)
	}

	c := ai.NewConversation(b.sender, model, maxTokens, tools, systemPrompt)

	log.Printf("Sending initial message to AI")
	repositoryContent, taskContent, err := buildPrompt(tsk)
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

func (b *Bot) rerunStatefulToolCalls(ctx context.Context, toolCtx *ToolContext, conversation *ai.Conversation) error {
	for turnNumber, turn := range conversation.Turns {
		if turnNumber == len(conversation.Turns)-1 {
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

// buildSummaryPrompt creates the prompt for requesting a conversation summary
func buildSummaryPrompt() string {
	var summaryPrompt strings.Builder
	summaryPrompt.WriteString("Please summarize all of the work you have done so far. Focus on:\n")
	summaryPrompt.WriteString("1. Key decisions and changes made\n")
	summaryPrompt.WriteString("2. Current state of the codebase\n")
	summaryPrompt.WriteString("3. Any important context for continuing the work\n")
	summaryPrompt.WriteString("4. Tools used and their outcomes\n\n")
	summaryPrompt.WriteString("Please provide a comprehensive but concise summary that captures all important information needed to continue working effectively. ")
	summaryPrompt.WriteString("There's no need to include the system prompt or initial repository information in your summary - focus on the actual work and changes made during our conversation.")

	return summaryPrompt.String()
}

var (
	// repeatSummaryRequest is a content block that will be used to simulate the assistant being prompted to produce its
	// previously-generated summary
	repeatSummaryRequest = anthropic.NewTextBlock(
		"You have already done some work on this task, but there was an interruption. " +
			"Before the interruption, you were asked to generate a summary of the work you had done so far. " +
			"Please respond with that summary.",
	)
	// resumeFromSummaryRequest is a content block that will be used to prompt the assistant to continue work after
	// summarization
	resumeFromSummaryRequest = anthropic.NewTextBlock("Please resume working on this task based on your summary.")
)

// summarize compresses conversation history using an AI-generated summary. It modifies the given conversation in-place.
//
// keepFirst specifies how many turns from the beginning of the conversation to keep in the summarized conversation.
// E.g. this can be used to preserve seeded turns used to guide the assistant's behavior. Must be >= 0.
//
// keepLast specifies how many turns from the end of the conversation to keep in the summarized conversation. This is
// used to maintain the continuity of the assistant's recent thoughts upon resumption. Must be >= 0.
// The assistant message from the turn _before_ the preserved turns will also appear in the summarized converation.
// E.g. if keepLast == 1, the 2nd-to-last turn of the summarized conversation will
func summarize(ctx context.Context, conversation *ai.Conversation, keepFirst int, keepLast int) error {
	// Example summarization with keepFirst == 2 and keepLast == 2
	//
	//                  **Original conversation**                     **Summary request**                        **Summarized conversation**
	// Turn 0:     User: <repo info> / <task info>			>    User: <repo info> / <task info>		>	User: <repo info> / <task info>
	//             Asst: read file foo.txt					>    Asst: read file foo.txt				>	Asst: read file foo.txt
	// Turn 1:     User: N/A								>    User: N/A								>	User: N/A
	//             Asst: replace string in file foo.txt		>    Asst: replace string in file foo.txt	>	Asst: replace string in file foo.txt
	// Turn 2:     User: N/A								>    User: N/A								>
	//             Asst: read file bar.txt					>    Asst: read file bar.txt				>	User: <simulated summary request>
	// Turn 3:     User: N/A								>    User: N/A								>	Asst: <summary>
	//             Asst: replace string in file bar.txt		>    Asst: replace string in file bar.txt	>	User: please resume work from summary
	// Turn 4:     User: N/A								>    User: <summary_request>				>	Asst: validate
	//             Asst: validate							>    										>
	// Turn 5:     User: N/A								>    										>	User: N/A
	//             Asst: replace string in file foo.txt		>    										>	Asst: replace string in file foo.txt
	// Turn 6:     User: N/A								>    										>	User: N/A
	//             Asst: validate							>    										>	Asst: validate

	if keepFirst < 0 {
		return fmt.Errorf("keepFirst must be >= 0")
	}
	if keepLast < 0 {
		return fmt.Errorf("keepLast must be >= 0")
	}

	// Technically a summary could be constructed with keepFirst+keepLast+1 messages, but then all of the messages from the
	// original conversation would be in the summarized conversation, rendering the summary useless
	if minTurns := keepFirst + keepLast + 2; len(conversation.Turns) < minTurns {
		log.Printf("Warning: Conversation has %d turns, but at least %d are required for summarization. Skipping summarization",
			len(conversation.Turns), minTurns)
		return nil
	}

	{
		// Get token count from most recent response for logging
		var totalTokens int64
		if lastMsg := conversation.Turns[len(conversation.Turns)-1]; lastMsg.Response != nil {
			totalTokens = lastMsg.Response.Usage.InputTokens +
				lastMsg.Response.Usage.CacheReadInputTokens +
				lastMsg.Response.Usage.CacheCreationInputTokens
		}

		log.Printf("    Conversation has %d messages and %d total input tokens, summarizing...",
			len(conversation.Turns), totalTokens)
	}

	// Generate a summary of the conversation with AI
	summaryMessage, err := generateSummary(ctx, conversation, keepLast)
	if err != nil {
		return fmt.Errorf("failed to generate summary: %w", err)
	}

	// Reconstruct the conversation: preserved first messages + summary exchange + preserved last messages
	summarizedTurns := slices.Clone(conversation.Turns[:keepFirst])
	summarizedTurns = append(summarizedTurns, []ai.ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{repeatSummaryRequest},
			Response:     summaryMessage,
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{resumeFromSummaryRequest},
			Response:     conversation.Turns[len(conversation.Turns)-keepLast-1].Response,
		},
	}...)
	summarizedTurns = append(summarizedTurns, conversation.Turns[len(conversation.Turns)-keepLast:]...)

	log.Printf("    Conversation summarized: %d messages -> %d messages",
		len(conversation.Turns), len(summarizedTurns))

	// Update the conversation
	conversation.Turns = summarizedTurns

	return nil
}

// generateSummary uses AI to generate a summary of the given conversation excluding the specified number of
// conversation turns. Does not modify the given conversation. excludeLast must be > 0
func generateSummary(ctx context.Context, conversation *ai.Conversation, excludeLast int) (*anthropic.Message, error) {
	summaryConversation, err := conversation.Fork(len(conversation.Turns) - excludeLast)
	if err != nil {
		return nil, fmt.Errorf("failed to fork conversation at summarization point: %w", err)
	}
	summaryPrompt := buildSummaryPrompt()

	// With the new conversation abstraction, tool results are stored in ToolExchanges and automatically handled by
	// convertTurnsToMessages, so we can simply send the summary prompt directly without preserving any existing content.
	return summaryConversation.SendMessage(ctx, anthropic.NewTextBlock(summaryPrompt))
}
