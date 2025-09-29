package ai

import (
	"context"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
	anthropt "github.com/anthropics/anthropic-sdk-go/option"
)

type MessageSender interface {
	SendMessage(ctx context.Context, params anthropic.MessageNewParams, opts ...anthropt.RequestOption) (*anthropic.Message, error)
}

type Conversation struct {
	sender MessageSender

	model        anthropic.Model
	systemPrompt string
	tools        []anthropic.ToolParam
	Turns        []ConversationTurn

	maxOutputTokens int64              // Maximum number of output tokens per response
	lastUsage       anthropic.Usage    // Token usage from the most recent API response
	lastResponse    *anthropic.Message // The most recent API response (for resuming conversations)
}

// ConversationTurn represents a complete interaction cycle: optional user instructions,
// assistant response, and tool exchanges. Turns are divided at places where optional
// user instructions can go - after tool result blocks in each user message.
type ConversationTurn struct {
	// UserInstructions are text blocks at the start of the turn (optional)
	UserInstructions []anthropic.TextBlock

	// AssistantTextBlocks contains text and thinking blocks from the assistant response.
	// Tool use blocks are stored separately in ToolExchanges to avoid duplication.
	AssistantTextBlocks []anthropic.ContentBlockParamUnion

	// ToolExchanges are the tool use/result pairs from this turn
	ToolExchanges []ToolExchange
}

// ToolExchange pairs a tool use block with its result
type ToolExchange struct {
	ToolUse    anthropic.ToolUseBlock
	ToolResult anthropic.ToolResultBlockParam
}

func NewConversation(
	sender MessageSender,
	model anthropic.Model,
	maxOutputTokens int64,
	tools []anthropic.ToolParam,
	systemPrompt string,
) *Conversation {

	return &Conversation{
		sender: sender,

		model:        model,
		systemPrompt: systemPrompt,
		tools:        tools,

		maxOutputTokens: maxOutputTokens,
	}
}

func ResumeConversation(
	sender MessageSender,
	history ConversationHistory,
	model anthropic.Model,
	maxOutputTokens int64,
	tools []anthropic.ToolParam,
) (*Conversation, error) {
	c := &Conversation{
		sender: sender,

		model:        model,
		systemPrompt: history.SystemPrompt,
		tools:        tools,
		Turns:        history.Messages,

		maxOutputTokens: maxOutputTokens,
		lastResponse:    history.LastResponse,
	}
	// Restore last usage from last response if available
	if history.LastResponse != nil {
		c.lastUsage = history.LastResponse.Usage
	}
	return c, nil
}

// SendMessage sends a message to the AI, awaits its response, and adds both to the conversation.
// The content blocks should include user instructions and/or tool results.
func (cc *Conversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, true, messageContent...)
}

// SeedTurn adds a turn to the conversation with pre-defined content (e.g. for testing or conversation setup)
func (cc *Conversation) SeedTurn(turn ConversationTurn) {
	cc.Turns = append(cc.Turns, turn)
}

// Fork creates a new conversation from this conversation up to (but not including) the specified turn index.
// The forked conversation will have the same configuration (model, system prompt, tools) but will contain
// only the turns before the specified index.
func (cc *Conversation) Fork(turnIndex int) *Conversation {
	if turnIndex < 0 {
		turnIndex = 0
	}
	if turnIndex > len(cc.Turns) {
		turnIndex = len(cc.Turns)
	}

	forked := &Conversation{
		sender:          cc.sender,
		model:           cc.model,
		systemPrompt:    cc.systemPrompt,
		tools:           cc.tools,
		maxOutputTokens: cc.maxOutputTokens,
		Turns:           make([]ConversationTurn, turnIndex),
	}

	copy(forked.Turns, cc.Turns[:turnIndex])
	return forked
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *Conversation) sendMessage(ctx context.Context, enableCache bool, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	// Set cache point only if caching is enabled
	if enableCache {
		// Always set a cache point. Unsupported cache points, e.g. on content that is below the minimum length for caching,
		// will be ignored
		cacheControl, err := getLastCacheControl(messageContent)
		if err != nil {
			log.Printf("Warning: failed to set cache point: %s", err)
		} else {
			*cacheControl = anthropic.NewCacheControlEphemeralParam()
		}
	}

	// Extract tool results from the message content and attach them to the previous turn's tool exchanges
	if len(cc.Turns) > 0 {
		toolResults := extractToolResults(messageContent)
		if len(toolResults) > 0 {
			prevTurn := &cc.Turns[len(cc.Turns)-1]
			// Match tool results with tool uses by ID
			for i := range prevTurn.ToolExchanges {
				for _, result := range toolResults {
					if result.ToolUseID == prevTurn.ToolExchanges[i].ToolUse.ID {
						prevTurn.ToolExchanges[i].ToolResult = result
						break
					}
				}
			}
		}
	}

	// Create a new turn with user instructions from the message content
	newTurn := ConversationTurn{
		UserInstructions:    extractTextBlocks(messageContent),
		AssistantTextBlocks: []anthropic.ContentBlockParamUnion{},
		ToolExchanges:       []ToolExchange{},
	}
	cc.Turns = append(cc.Turns, newTurn)

	// Build API messages from logical turns
	messageParams := cc.buildAPIMessages()

	params := anthropic.MessageNewParams{
		Model:     cc.model,
		MaxTokens: cc.maxOutputTokens,
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

	response, err := cc.sender.SendMessage(ctx, params)
	if err != nil {
		return nil, err
	}

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d, Total: %d",
		response.Usage.InputTokens,
		response.Usage.CacheCreationInputTokens,
		response.Usage.CacheReadInputTokens,
		response.Usage.InputTokens+response.Usage.CacheCreationInputTokens+response.Usage.CacheReadInputTokens,
	)

	// Track token usage and response from this API call
	cc.lastUsage = response.Usage
	cc.lastResponse = response

	// Parse the response and update the turn
	err = cc.updateTurnWithResponse(len(cc.Turns)-1, response)
	if err != nil {
		return nil, fmt.Errorf("failed to update turn with response: %w", err)
	}

	// Remove the cache control element from the conversation history if caching was enabled
	if enableCache {
		// Remove the cache control element from the conversation history. Anthropic's automatic prefix checking should
		// reuse previously-cached sections without explicitly marking them as such in subsequent messages
		if cacheControl, err := getLastCacheControl(messageContent); err == nil {
			*cacheControl = anthropic.CacheControlEphemeralParam{}
		}
	}

	return response, nil
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

// buildAPIMessages constructs the API message parameters from the logical turns
func (cc *Conversation) buildAPIMessages() []anthropic.MessageParam {
	messages := []anthropic.MessageParam{}

	for i, turn := range cc.Turns {
		// Build user message content: user instructions (if any) + tool results from previous turn (if any)
		userContent := []anthropic.ContentBlockParamUnion{}

		// If this is not the first turn, add tool results from the previous turn
		if i > 0 {
			prevTurn := cc.Turns[i-1]
			for _, exchange := range prevTurn.ToolExchanges {
				userContent = append(userContent, anthropic.ContentBlockParamUnion{
					OfToolResult: &exchange.ToolResult,
				})
			}
		}

		// Add user instructions for this turn
		for _, textBlock := range turn.UserInstructions {
			userContent = append(userContent, anthropic.ContentBlockParamUnion{
				OfText: &anthropic.TextBlockParam{
					Text: textBlock.Text,
					Type: textBlock.Type,
				},
			})
		}

		// Only add user message if there's content
		if len(userContent) > 0 {
			messages = append(messages, anthropic.NewUserMessage(userContent...))
		}

		// Build assistant message content: text blocks + tool uses
		assistantContent := []anthropic.ContentBlockParamUnion{}

		// Add assistant text blocks
		assistantContent = append(assistantContent, turn.AssistantTextBlocks...)

		// Add tool uses
		for _, exchange := range turn.ToolExchanges {
			assistantContent = append(assistantContent, anthropic.ContentBlockParamUnion{
				OfToolUse: &anthropic.ToolUseBlockParam{
					ID:    exchange.ToolUse.ID,
					Input: exchange.ToolUse.Input,
					Name:  exchange.ToolUse.Name,
					Type:  exchange.ToolUse.Type,
				},
			})
		}

		// Only add assistant message if there's content
		if len(assistantContent) > 0 {
			messages = append(messages, anthropic.NewAssistantMessage(assistantContent...))
		}
	}

	return messages
}

// extractTextBlocks extracts text blocks from content block parameters
func extractTextBlocks(content []anthropic.ContentBlockParamUnion) []anthropic.TextBlock {
	var textBlocks []anthropic.TextBlock
	for _, block := range content {
		if textParam := block.OfText; textParam != nil {
			textBlocks = append(textBlocks, anthropic.TextBlock{
				Text: textParam.Text,
				Type: textParam.Type,
			})
		}
	}
	return textBlocks
}

// updateTurnWithResponse parses an AI response and updates the specified turn with the assistant's
// response content and any tool uses. Tool results will be added when the next message is sent.
func (cc *Conversation) updateTurnWithResponse(turnIndex int, response *anthropic.Message) error {
	if turnIndex < 0 || turnIndex >= len(cc.Turns) {
		return fmt.Errorf("turn index %d out of range", turnIndex)
	}

	turn := &cc.Turns[turnIndex]

	// Parse response content blocks
	for _, contentBlock := range response.Content {
		switch block := contentBlock.AsAny().(type) {
		case anthropic.TextBlock:
			turn.AssistantTextBlocks = append(turn.AssistantTextBlocks, anthropic.ContentBlockParamUnion{
				OfText: &anthropic.TextBlockParam{
					Text: block.Text,
					Type: block.Type,
				},
			})
		case anthropic.ThinkingBlock:
			turn.AssistantTextBlocks = append(turn.AssistantTextBlocks, anthropic.ContentBlockParamUnion{
				OfThinking: &anthropic.ThinkingBlockParam{
					Thinking: block.Thinking,
					Type:     block.Type,
				},
			})
		case anthropic.ToolUseBlock:
			// Add tool use without result - result will be added when the next message is sent
			turn.ToolExchanges = append(turn.ToolExchanges, ToolExchange{
				ToolUse: block,
			})
		}
	}

	return nil
}

// extractToolResults extracts tool result blocks from content block parameters
func extractToolResults(content []anthropic.ContentBlockParamUnion) []anthropic.ToolResultBlockParam {
	var results []anthropic.ToolResultBlockParam
	for _, block := range content {
		if resultParam := block.OfToolResult; resultParam != nil {
			results = append(results, *resultParam)
		}
	}
	return results
}

// ConversationHistory contains a serializable and resumable snapshot of a Conversation
type ConversationHistory struct {
	SystemPrompt string              `json:"systemPrompt"`
	Messages     []ConversationTurn  `json:"messages"`
	LastResponse *anthropic.Message  `json:"lastResponse,omitempty"`
}

// History returns a serializable conversation history
func (cc *Conversation) History() ConversationHistory {
	return ConversationHistory{
		SystemPrompt: cc.systemPrompt,
		Messages:     cc.Turns,
		LastResponse: cc.lastResponse,
	}
}

// LastUsage returns the token usage from the most recent API response
func (cc *Conversation) LastUsage() anthropic.Usage {
	return cc.lastUsage
}

// getLastResponse returns the most recent API response, or nil if there isn't one.
// This is useful for resuming conversations where we need to return the last assistant response.
func (cc *Conversation) getLastResponse() *anthropic.Message {
	return cc.lastResponse
}
