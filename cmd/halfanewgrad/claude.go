package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
)

type ClaudeConversation struct {
	client anthropic.Client

	model        anthropic.Model
	maxTokens    int64
	systemPrompt string
	tools        []anthropic.ToolParam
	messages     []anthropic.MessageParam
}

func NewClaudeConversation(anthropicClient anthropic.Client, model anthropic.Model, maxTokens int64, systemPrompt string, tools []anthropic.ToolParam) *ClaudeConversation {
	return &ClaudeConversation{
		client: anthropicClient,

		model:        model,
		maxTokens:    maxTokens,
		systemPrompt: systemPrompt,
		tools:        tools,
	}
}

// SendMessage sends a user message with the given content and returns Claude's response
func (cc *ClaudeConversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	cc.messages = append(cc.messages, anthropic.NewUserMessage(messageContent...))

	params := anthropic.MessageNewParams{
		Model:     cc.model,
		MaxTokens: cc.maxTokens,
		System: []anthropic.TextBlockParam{
			{Text: cc.systemPrompt},
		},
		Messages: cc.messages,
	}

	toolParams := []anthropic.ToolUnionParam{}
	for _, tool := range cc.tools {
		toolParams = append(toolParams, anthropic.ToolUnionParam{
			OfTool: &tool,
		})
	}
	params.Tools = toolParams

	message, err := cc.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}
	// Append the generated message to the conversation for continuation
	cc.messages = append(cc.messages, message.ToParam())

	messageJSON, err := json.Marshal(message)
	if err == nil {
		log.Print("Assistant message: ", string(messageJSON))
	}

	return message, nil
}
