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

// AddUserMessage adds a user message to the conversation
func (cc *ClaudeConversation) AddUserMessage(content string) {
	userMessage := anthropic.MessageParam{
		Role: anthropic.MessageRoleUser,
		Content: []anthropic.ContentBlockParamUnion{
			anthropic.TextBlockParam{
				Type: anthropic.ContentBlockTypeText,
				Text: content,
			},
		},
	}

	cc.messages = append(cc.messages, conversationTurn{
		UserMessage: userMessage,
		Response:    nil,
	})
}

// AddToolResult adds a tool result to the conversation
func (cc *ClaudeConversation) AddToolResult(toolCallID string, result string) {
	if len(cc.messages) == 0 {
		log.Printf("Warning: Adding tool result with no messages in conversation")
		return
	}

	// Get the last message
	lastIndex := len(cc.messages) - 1
	lastMessage := &cc.messages[lastIndex]

	if lastMessage.Response == nil {
		log.Printf("Warning: Adding tool result but last message has no response")
		return
	}

	// Create tool result message
	toolResultMessage := anthropic.MessageParam{
		Role: anthropic.MessageRoleUser,
		Content: []anthropic.ContentBlockParamUnion{
			anthropic.ToolResultBlockParam{
				Type:       anthropic.ContentBlockTypeToolResult,
				ToolUseID:  toolCallID,
				Content:    result,
			},
		},
	}

	// Add as a new conversation turn
	cc.messages = append(cc.messages, conversationTurn{
		UserMessage: toolResultMessage,
		Response:    nil,
	})
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