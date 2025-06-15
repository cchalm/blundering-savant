package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

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

	stream := cc.client.Messages.NewStreaming(ctx, params)
	message := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		err := message.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate response content stream: %w", err)
		}
	}
	if stream.Err() != nil {
		return nil, fmt.Errorf("failed to stream response: %w", stream.Err())
	}
	if message.StopReason == "" {
		b, err := json.Marshal(message)
		if err != nil {
			log.Printf("error while marshalling corrupt message for inspection: %v", err)
		}
		return nil, fmt.Errorf("malformed message: %v", string(b))
	}
	// Append the generated message to the conversation for continuation
	cc.messages = append(cc.messages, message.ToParam())

	return &message, nil
}

type RateLimitedTransport struct {
	base http.RoundTripper
}

func WithRateLimiting(base http.RoundTripper) *RateLimitedTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RateLimitedTransport{base: base}
}

func (t *RateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return resp, err
		}

		// Check for 429 status
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfterStr := resp.Header.Get("retry-after")
			if retryAfterStr != "" {
				// Parse retry-after header
				var waitDuration time.Duration

				// Try parsing as seconds
				if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
					waitDuration = time.Duration(seconds) * time.Second
				} else if retryTime, err := time.Parse(time.RFC1123, retryAfterStr); err == nil {
					waitDuration = time.Until(retryTime)
				}

				if waitDuration > 0 {
					// Close the response body to free resources
					resp.Body.Close()

					// Wait for the specified duration
					log.Printf("Rate limited, waiting %s", waitDuration)
					select {
					case <-req.Context().Done():
						return nil, req.Context().Err()
					case <-time.After(waitDuration):
						// Continue the loop to retry
						continue
					}
				}
			}
		}

		// Return response for all other cases
		return resp, err
	}
}
