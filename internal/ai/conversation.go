package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

type Conversation struct {
	client anthropic.Client

	model        anthropic.Model
	systemPrompt string
	tools        []anthropic.ToolParam
	Messages     []conversationTurn

	maxTokens int64 // Maximum number of output tokens per response
	
	// Token limit for summarization
	tokenLimit int64 // When to trigger summarization
}

// conversationTurn is a pair of messages: a user message, and an optional assistant response
type conversationTurn struct {
	UserMessage anthropic.MessageParam
	Response    *anthropic.Message // May be nil
}

func NewConversation(
	anthropicClient anthropic.Client,
	model anthropic.Model,
	maxTokens int64,
	tools []anthropic.ToolParam,
	systemPrompt string,
) *Conversation {

	return &Conversation{
		client: anthropicClient,

		model:        model,
		systemPrompt: systemPrompt,
		tools:        tools,

		maxTokens: maxTokens,
		tokenLimit: 100000, // 100k token limit
	}
}

func ResumeConversation(
	anthropicClient anthropic.Client,
	history ConversationHistory,
	model anthropic.Model,
	maxTokens int64,
	tools []anthropic.ToolParam,
) (*Conversation, error) {
	c := &Conversation{
		client: anthropicClient,

		model:        model,
		systemPrompt: history.SystemPrompt,
		tools:        tools,
		Messages:     history.Messages,

		maxTokens:  maxTokens,
		tokenLimit: 100000, // 100k token limit
	}
	return c, nil
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *Conversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	// Always set a cache point. Unsupported cache points, e.g. on content that is below the minimum length for caching,
	// will be ignored
	cacheControl, err := getLastCacheControl(messageContent)
	if err != nil {
		log.Printf("Warning: failed to set cache point: %s", err)
	}
	*cacheControl = anthropic.NewCacheControlEphemeralParam()

	cc.Messages = append(cc.Messages, conversationTurn{
		UserMessage: anthropic.NewUserMessage(messageContent...),
	})

	messageParams := []anthropic.MessageParam{}
	for _, turn := range cc.Messages {
		messageParams = append(messageParams, turn.UserMessage)
		if turn.Response != nil {
			messageParams = append(messageParams, turn.Response.ToParam())
		}
	}

	params := anthropic.MessageNewParams{
		Model:     cc.model,
		MaxTokens: cc.maxTokens,
		System: []anthropic.TextBlockParam{
			{
				Text: cc.systemPrompt,
				// Always cache the system prompt, which will be the same for each iteration of this conversation _and_
				// will be the same for other conversations by this bot
				// Actually, currently the system prompt is relatively small, so let's save the cache points for later
				// CacheControl: anthropic.NewCacheControlEphemeralParam(),
			},
		},
		Messages: messageParams,
	}

	toolParams := []anthropic.ToolUnionParam{}
	for _, tool := range cc.tools {
		toolParams = append(toolParams, anthropic.ToolUnionParam{
			OfTool: &tool,
		})
	}
	params.Tools = toolParams

	stream := cc.client.Messages.NewStreaming(ctx, params)
	response := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		err := response.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate response content stream: %w", err)
		}
	}
	if stream.Err() != nil {
		return nil, fmt.Errorf("failed to stream response: %w", stream.Err())
	}
	if response.StopReason == "" {
		b, err := json.Marshal(response)
		if err != nil {
			log.Printf("error while marshalling corrupt message for inspection: %v", err)
		}
		return nil, fmt.Errorf("malformed message: %v", string(b))
	}

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d, Total (Input+CacheRead): %d",
		response.Usage.InputTokens,
		response.Usage.CacheCreationInputTokens,
		response.Usage.CacheReadInputTokens,
		response.Usage.InputTokens + response.Usage.CacheReadInputTokens,
	)

	// Record the response
	cc.Messages[len(cc.Messages)-1].Response = &response

	// Remove the cache control element from the conversation history. Anthropic's automatic prefix checking should
	// reuse previously-cached sections without explicitly marking them as such in subsequent messages
	*cacheControl = anthropic.CacheControlEphemeralParam{}

	return &response, nil
}

func getLastCacheControl(content []anthropic.ContentBlockParamUnion) (*anthropic.CacheControlEphemeralParam, error) {
	for i := len(content) - 1; i >= 0; i-- {
		c := content[i]
		if cacheControl := c.GetCacheControl(); cacheControl != nil {
			return cacheControl, nil
		}
	}

	return nil, fmt.Errorf("no cacheable blocks in content")
}

// NeedsSummarization checks if the conversation should be summarized due to token limits
func (cc *Conversation) NeedsSummarization() bool {
	if len(cc.Messages) == 0 {
		return false
	}
	
	// Get the most recent response
	lastMessage := cc.Messages[len(cc.Messages)-1]
	if lastMessage.Response == nil {
		return false
	}
	
	// Check token usage from the most recent turn (which includes cumulative history)
	totalTokens := lastMessage.Response.Usage.InputTokens + lastMessage.Response.Usage.CacheReadInputTokens
	return totalTokens > cc.tokenLimit
}

// SummarizeConversation creates a condensed version of the conversation history, preserving
// the first message (initial repository and task content) while summarizing the rest
func (cc *Conversation) SummarizeConversation(ctx context.Context) error {
	if len(cc.Messages) <= 2 {
		// Don't summarize if we have too few messages
		return nil
	}

	// Get token count from most recent response for logging
	var totalTokens int64
	if lastMsg := cc.Messages[len(cc.Messages)-1]; lastMsg.Response != nil {
		totalTokens = lastMsg.Response.Usage.InputTokens + lastMsg.Response.Usage.CacheReadInputTokens
	}

	log.Printf("Conversation has %d messages and %d total tokens (input+cache read), summarizing...", 
		len(cc.Messages), totalTokens)

	// Preserve the first message (initial repository and task content)
	numFirstMessagesToPreserve := 1
	
	// Ensure we have something to summarize
	if len(cc.Messages) <= numFirstMessagesToPreserve {
		return nil
	}

	// Generate summary using AI (this will add a summary request/response to the conversation)
	summary, err := cc.generateConversationSummary(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate conversation summary: %w", err)
	}

	// The summary request and response are now the last message in the conversation
	// We need to account for this when reconstructing the conversation
	summaryRequestResponseCount := 1 // The summary request/response is one conversation turn

	// Create a summary message to replace the middle portion
	summaryMessage := conversationTurn{
		UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock(summary)),
		// No response - this is just context for future messages
	}

	// Reconstruct the conversation: preserved first messages + summary
	newMessages := []conversationTurn{}
	
	// Add preserved first messages
	newMessages = append(newMessages, cc.Messages[:numFirstMessagesToPreserve]...)
	
	// Add summary message
	newMessages = append(newMessages, summaryMessage)

	// Update the conversation
	originalMessageCount := len(cc.Messages)
	cc.Messages = newMessages

	log.Printf("Conversation summarized: %d messages -> %d messages", 
		originalMessageCount, len(cc.Messages))

	return nil
}

// generateConversationSummary creates a summary of the conversation using AI
func (cc *Conversation) generateConversationSummary(ctx context.Context) (string, error) {
	// Build a summary request that will be sent as part of the current conversation
	var summaryPrompt strings.Builder
	summaryPrompt.WriteString("Please summarize all of the work you have done so far. Focus on:\n")
	summaryPrompt.WriteString("1. Key decisions and changes made\n")
	summaryPrompt.WriteString("2. Current state of the codebase\n")
	summaryPrompt.WriteString("3. Any important context for continuing the work\n")
	summaryPrompt.WriteString("4. Tools used and their outcomes\n\n")
	summaryPrompt.WriteString("Please provide a comprehensive but concise summary that captures all important information needed to continue working effectively. ")
	summaryPrompt.WriteString("There's no need to include the system prompt or initial repository information in your summary - focus on the actual work and changes made during our conversation.")

	// Temporarily disable token limit to avoid recursive summarization during summary generation
	originalTokenLimit := cc.tokenLimit
	cc.tokenLimit = 0

	// Send summary request as part of the original conversation
	response, err := cc.SendMessage(ctx, anthropic.NewTextBlock(summaryPrompt.String()))
	if err != nil {
		// Restore original token limit
		cc.tokenLimit = originalTokenLimit
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	// Restore original token limit
	cc.tokenLimit = originalTokenLimit

	// Extract text from response
	var summary strings.Builder
	summary.WriteString("## Conversation Summary\n\n")
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			summary.WriteString(textBlock.Text)
		}
	}

	return summary.String(), nil
}



// ConversationHistory contains a serializable and resumable snapshot of a Conversation
type ConversationHistory struct {
	SystemPrompt string             `json:"systemPrompt"`
	Messages     []conversationTurn `json:"messages"`
}

// History returns a serializable conversation history
func (cc *Conversation) History() ConversationHistory {
	return ConversationHistory{
		SystemPrompt: cc.systemPrompt,
		Messages:     cc.Messages,
	}
}
