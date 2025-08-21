// Package ai provides AI conversation handling and prompt management.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// ClaudeConversation manages a conversation with Claude
type ClaudeConversation struct {
	client anthropic.Client

	model        anthropic.Model
	systemPrompt string
	tools        []anthropic.ToolParam
	messages     []conversationTurn

	maxTokens int64 // Maximum number of output tokens per response
}

// conversationTurn is a pair of messages: a user message, and an optional assistant response
type conversationTurn struct {
	UserMessage anthropic.MessageParam
	Response    *anthropic.Message // May be nil
}

// NewClaudeConversation creates a new Claude conversation
func NewClaudeConversation(
	anthropicClient anthropic.Client,
	model anthropic.Model,
	maxTokens int64,
	tools []anthropic.ToolParam,
	systemPrompt string,
) *ClaudeConversation {
	return &ClaudeConversation{
		client: anthropicClient,
		model:        model,
		systemPrompt: systemPrompt,
		tools:        tools,
		maxTokens: maxTokens,
	}
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *ClaudeConversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	// Always set a cache point. Unsupported cache points, e.g. on content that is below the minimum length for caching,
	// will be ignored
	cacheControl, err := getLastCacheControl(messageContent)
	if err != nil {
		log.Printf("Warning: failed to set cache point: %s", err)
	}
	*cacheControl = anthropic.NewCacheControlEphemeralParam()

	cc.messages = append(cc.messages, conversationTurn{
		UserMessage: anthropic.NewUserMessage(messageContent...),
	})

	messageParams := []anthropic.MessageParam{}
	for _, turn := range cc.messages {
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

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d",
		response.Usage.InputTokens,
		response.Usage.CacheCreationInputTokens,
		response.Usage.CacheReadInputTokens,
	)

	// Record the repsonse
	cc.messages[len(cc.messages)-1].Response = &response

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

// GetResponse gets the next response from Claude
func (cc *ClaudeConversation) GetResponse(ctx context.Context) (*anthropic.Message, error) {
	// Find the last message without a response
	var targetIndex = -1
	for i := len(cc.messages) - 1; i >= 0; i-- {
		if cc.messages[i].Response == nil {
			targetIndex = i
			break
		}
	}

	if targetIndex == -1 {
		return nil, fmt.Errorf("no messages without responses found")
	}

	// Build the message history for the API call
	var messageHistory []anthropic.MessageParam
	for i := 0; i <= targetIndex; i++ {
		messageHistory = append(messageHistory, cc.messages[i].UserMessage)
		if cc.messages[i].Response != nil {
			responseMessage := anthropic.MessageParam{
				Role:    anthropic.MessageRoleAssistant,
				Content: make([]anthropic.ContentBlockParamUnion, len(cc.messages[i].Response.Content)),
			}
			
			for j, block := range cc.messages[i].Response.Content {
				switch block.Type {
				case anthropic.ContentBlockTypeText:
					responseMessage.Content[j] = anthropic.TextBlockParam{
						Type: anthropic.ContentBlockTypeText,
						Text: block.Text,
					}
				case anthropic.ContentBlockTypeToolUse:
					responseMessage.Content[j] = anthropic.ToolUseBlockParam{
						Type:  anthropic.ContentBlockTypeToolUse,
						ID:    block.ToolUse.ID,
						Name:  block.ToolUse.Name,
						Input: block.ToolUse.Input,
					}
				}
			}
			messageHistory = append(messageHistory, responseMessage)
		}
	}

	// Create the message request
	messageRequest := anthropic.MessageNewParams{
		Model:     anthropic.F(cc.model),
		MaxTokens: anthropic.F(cc.maxTokens),
		System:    anthropic.F([]anthropic.TextBlockParam{{Type: anthropic.ContentBlockTypeText, Text: cc.systemPrompt}}),
		Messages:  anthropic.F(messageHistory),
		Tools:     anthropic.F(cc.tools),
	}

	// Make the API call
	response, err := cc.client.Messages.New(ctx, messageRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	// Store the response
	cc.messages[targetIndex].Response = response

	return response, nil
}

// GetHistory returns the conversation history
func (cc *ClaudeConversation) GetHistory() *conversationHistory {
	return &conversationHistory{
		Model:        string(cc.model),
		SystemPrompt: cc.systemPrompt,
		MaxTokens:    cc.maxTokens,
		Messages:     cc.messages,
	}
}

// LoadFromHistory loads a conversation from history
func (cc *ClaudeConversation) LoadFromHistory(history *conversationHistory) {
	cc.model = anthropic.Model(history.Model)
	cc.systemPrompt = history.SystemPrompt
	cc.maxTokens = history.MaxTokens
	cc.messages = history.Messages
}

// conversationHistory represents the serializable history of a conversation
type conversationHistory struct {
	Model        string             `json:"model"`
	SystemPrompt string             `json:"system_prompt"`
	MaxTokens    int64              `json:"max_tokens"`
	Messages     []conversationTurn `json:"messages"`
}