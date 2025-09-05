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
	
	// Token tracking for summarization
	totalInputTokens    int64
	totalCacheReadTokens int64
	tokenLimit          int64 // When to trigger summarization
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

		maxTokens:            maxTokens,
		tokenLimit:           100000, // 100k token limit
		totalInputTokens:     history.TotalInputTokens,
		totalCacheReadTokens: history.TotalCacheReadTokens,
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

	// Update token tracking
	cc.totalInputTokens += response.Usage.InputTokens
	cc.totalCacheReadTokens += response.Usage.CacheReadInputTokens

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d, Total (Input+CacheRead): %d",
		response.Usage.InputTokens,
		response.Usage.CacheCreationInputTokens,
		response.Usage.CacheReadInputTokens,
		cc.totalInputTokens + cc.totalCacheReadTokens,
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
	return cc.totalInputTokens + cc.totalCacheReadTokens > cc.tokenLimit
}

// SummarizeConversation creates a condensed version of the conversation history, preserving
// the system prompt and initial repository state while summarizing the middle interactions
func (cc *Conversation) SummarizeConversation(ctx context.Context) error {
	if len(cc.Messages) <= 2 {
		// Don't summarize if we have too few messages
		return nil
	}

	log.Printf("Conversation has %d messages and %d total tokens (input+cache read), summarizing...", 
		len(cc.Messages), cc.totalInputTokens + cc.totalCacheReadTokens)

	// Preserve the first message (initial repository and task content) and last few messages
	// to maintain context while summarizing the middle portion
	preserveFirstMessages := 1
	preserveLastMessages := 2
	
	// Ensure we have something to summarize
	if len(cc.Messages) <= preserveFirstMessages + preserveLastMessages {
		return nil
	}

	// Extract messages to summarize (middle portion, excluding the last few messages)
	startIdx := preserveFirstMessages
	endIdx := len(cc.Messages) - preserveLastMessages
	messagesToSummarize := cc.Messages[startIdx:endIdx]
	
	if len(messagesToSummarize) == 0 {
		return nil
	}

	// Generate summary using AI (this will add a summary request/response to the conversation)
	summary, err := cc.generateConversationSummary(ctx, messagesToSummarize)
	if err != nil {
		return fmt.Errorf("failed to generate conversation summary: %w", err)
	}

	// The summary request and response are now the last two messages in the conversation
	// We need to account for this when reconstructing the conversation
	summaryRequestResponseCount := 1 // The summary request/response is one conversation turn

	// Create a summary message to replace the middle portion
	summaryMessage := conversationTurn{
		UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock(summary)),
		// No response - this is just context for future messages
	}

	// Reconstruct the conversation with preserved messages + summary + recent messages (excluding summary request/response)
	newMessages := []conversationTurn{}
	
	// Add preserved first messages
	newMessages = append(newMessages, cc.Messages[:preserveFirstMessages]...)
	
	// Add summary message
	newMessages = append(newMessages, summaryMessage)
	
	// Add preserved last messages, but exclude the summary request/response we just added
	originalLastMessages := cc.Messages[endIdx:len(cc.Messages)-summaryRequestResponseCount]
	newMessages = append(newMessages, originalLastMessages...)

	// Update the conversation
	originalMessageCount := len(cc.Messages)
	cc.Messages = newMessages
	
	// Reset token counters since we've condensed the conversation
	cc.totalInputTokens = 0
	cc.totalCacheReadTokens = 0

	log.Printf("Conversation summarized: %d messages -> %d messages", 
		originalMessageCount, len(cc.Messages))

	return nil
}

// generateConversationSummary creates a summary of the given conversation turns using AI
func (cc *Conversation) generateConversationSummary(ctx context.Context, messages []conversationTurn) (string, error) {
	// Build a summary request that will be sent as part of the current conversation
	var summaryPrompt strings.Builder
	summaryPrompt.WriteString("Please create a concise summary of the conversation we've had so far. Focus on:\n")
	summaryPrompt.WriteString("1. Key decisions and changes made\n")
	summaryPrompt.WriteString("2. Current state of the codebase\n")
	summaryPrompt.WriteString("3. Any important context for continuing the work\n")
	summaryPrompt.WriteString("4. Tools used and their outcomes\n\n")
	summaryPrompt.WriteString("This summary will replace the middle portion of our conversation history to save tokens while preserving context. ")
	summaryPrompt.WriteString("Please provide a comprehensive but concise summary that captures all important information needed to continue working effectively.")

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
	SystemPrompt         string             `json:"systemPrompt"`
	Messages             []conversationTurn `json:"messages"`
	TotalInputTokens     int64              `json:"totalInputTokens"`
	TotalCacheReadTokens int64              `json:"totalCacheReadTokens"`
}

// History returns a serializable conversation history
func (cc *Conversation) History() ConversationHistory {
	return ConversationHistory{
		SystemPrompt:         cc.systemPrompt,
		Messages:             cc.Messages,
		TotalInputTokens:     cc.totalInputTokens,
		TotalCacheReadTokens: cc.totalCacheReadTokens,
	}
}
