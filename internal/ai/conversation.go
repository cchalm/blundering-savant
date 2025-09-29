package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
)

type Conversation struct {
	client anthropic.Client

	model        anthropic.Model
	systemPrompt string
	tools        []anthropic.ToolParam
	Turns        []ConversationTurn

	maxOutputTokens int64 // Maximum number of output tokens per response
}

// ConversationTurn is a pair of messages: a user message, and an optional assistant response
type ConversationTurn struct {
	UserMessage anthropic.MessageParam
	Response    *anthropic.Message // May be nil
}

func NewConversation(
	anthropicClient anthropic.Client,
	model anthropic.Model,
	maxOutputTokens int64,
	tools []anthropic.ToolParam,
	systemPrompt string,
) *Conversation {

	return &Conversation{
		client: anthropicClient,

		model:        model,
		systemPrompt: systemPrompt,
		tools:        tools,

		maxOutputTokens: maxOutputTokens,
	}
}

func ResumeConversation(
	anthropicClient anthropic.Client,
	history ConversationHistory,
	model anthropic.Model,
	maxOutputTokens int64,
	tools []anthropic.ToolParam,
) (*Conversation, error) {
	c := &Conversation{
		client: anthropicClient,

		model:        model,
		systemPrompt: history.SystemPrompt,
		tools:        tools,
		Turns:        history.Messages,

		maxOutputTokens: maxOutputTokens,
	}
	return c, nil
}

// SendMessage sends a message to the AI, awaits its response, and adds both to the conversation
func (cc *Conversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, true, messageContent...)
}

// SeedTurn adds a message to the conversation with a hard-coded (i.e. fake) response
func (cc *Conversation) SeedTurn(ctx context.Context, turn ConversationTurn) {
	cc.Turns = append(cc.Turns, turn)
}

// ResendLastMessage erases the last turn in the conversation history and resends the user message in that turn
func (cc *Conversation) ResendLastMessage(ctx context.Context) (*anthropic.Message, error) {
	if len(cc.Turns) == 0 {
		return nil, fmt.Errorf("cannot resend last message: no messages")
	}

	var lastTurn ConversationTurn
	// Pop the last message off of the conversation history
	lastTurn, cc.Turns = cc.Turns[len(cc.Turns)-1], cc.Turns[:len(cc.Turns)-1]

	return cc.SendMessage(ctx, lastTurn.UserMessage.Content...)
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

	cc.Turns = append(cc.Turns, ConversationTurn{
		UserMessage: anthropic.NewUserMessage(messageContent...),
	})

	messageParams := []anthropic.MessageParam{}
	for _, turn := range cc.Turns {
		messageParams = append(messageParams, turn.UserMessage)
		if turn.Response != nil {
			messageParams = append(messageParams, turn.Response.ToParam())
		}
	}

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

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d, Total: %d",
		response.Usage.InputTokens,
		response.Usage.CacheCreationInputTokens,
		response.Usage.CacheReadInputTokens,
		response.Usage.InputTokens+response.Usage.CacheCreationInputTokens+response.Usage.CacheReadInputTokens,
	)

	// Record the response
	cc.Turns[len(cc.Turns)-1].Response = &response

	// Remove the cache control element from the conversation history if caching was enabled
	if enableCache {
		// Remove the cache control element from the conversation history. Anthropic's automatic prefix checking should
		// reuse previously-cached sections without explicitly marking them as such in subsequent messages
		if cacheControl, err := getLastCacheControl(messageContent); err == nil {
			*cacheControl = anthropic.CacheControlEphemeralParam{}
		}
	}

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

// ConversationHistory contains a serializable and resumable snapshot of a Conversation
type ConversationHistory struct {
	SystemPrompt string             `json:"systemPrompt"`
	Messages     []ConversationTurn `json:"messages"`
}

// History returns a serializable conversation history
func (cc *Conversation) History() ConversationHistory {
	return ConversationHistory{
		SystemPrompt: cc.systemPrompt,
		Messages:     cc.Turns,
	}
}
