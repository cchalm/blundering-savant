package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

	autoCacheThreshold   int64 // Number of input tokens after which we automatically set a cache point
	cachePointsRemaining int   // Number of cache points remaining in this conversation
}

func NewClaudeConversation(anthropicClient anthropic.Client, model anthropic.Model, maxTokens int64, systemPrompt string, tools []anthropic.ToolParam) *ClaudeConversation {
	return &ClaudeConversation{
		client: anthropicClient,

		model:                model,
		maxTokens:            maxTokens,
		systemPrompt:         systemPrompt,
		tools:                tools,
		autoCacheThreshold:   10000,
		cachePointsRemaining: 3, // 4 minus 1 for the system prompt
	}
}

// SendMessage sends a user message with the given content and returns Claude's response
func (cc *ClaudeConversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, false, messageContent...)
}

// SendMessage sends a user message with the given content and returns Claude's response, and sets a cache breakpoint in
// the response, which will cause the cache to be written on the next message
func (cc *ClaudeConversation) SendMessageAndSetCachePoint(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, true, messageContent...)
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *ClaudeConversation) sendMessage(ctx context.Context, setCachePoint bool, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	cc.messages = append(cc.messages, anthropic.NewUserMessage(messageContent...))

	params := anthropic.MessageNewParams{
		Model:     cc.model,
		MaxTokens: cc.maxTokens,
		System: []anthropic.TextBlockParam{
			{
				Text: cc.systemPrompt,
				// Always cache the system prompt, which will be the same for each iteration of this conversation _and_
				// will be the same for other conversations by this bot
				CacheControl: anthropic.NewCacheControlEphemeralParam(),
			},
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

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d",
		message.Usage.InputTokens,
		message.Usage.CacheCreationInputTokens,
		message.Usage.CacheReadInputTokens,
	)

	messageParam := message.ToParam()
	if setCachePoint && cc.cachePointsRemaining == 0 {
		log.Printf("Warning: cannot set cache point, no cache points remaining")
	}
	if (setCachePoint || message.Usage.InputTokens > cc.autoCacheThreshold) && cc.cachePointsRemaining != 0 {
		// Set the cache point at the last text block in the content
		for i := len(messageParam.Content) - 1; i >= 0; i-- {
			if text := messageParam.Content[i].OfText; text != nil {
				text.CacheControl = anthropic.NewCacheControlEphemeralParam()
				break
			}
			if setCachePoint && i == 0 {
				fmt.Printf("Warning: unable to set cache point, no text blocks in message")
			}
		}
	}

	// Append the generated message to the conversation for continuation
	cc.messages = append(cc.messages, message.ToParam())

	// TODO remove this
	b, err := json.Marshal(cc.messages)
	if err == nil {
		os.WriteFile("conversation.json", b, 0666)
	}

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
	// Preserve the original request body for retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		req.Body.Close()
	}

	for {
		// Restore the request body for each attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

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
