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
	systemPrompt string
	tools        []anthropic.ToolParam
	messages     []conversationTurn

	maxTokens            int64 // Maximum number of output tokens per response
	autoCacheThreshold   int64 // Number of input tokens after which we automatically set a cache point
	cachePointsRemaining int   // Number of cache points remaining in this conversation
}

// conversationTurn is a pair of messages: a user message, and an optional assistant response
type conversationTurn struct {
	userMessage anthropic.MessageParam
	response    *anthropic.Message // May be nil
}

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

		maxTokens:            maxTokens,
		autoCacheThreshold:   10000,
		cachePointsRemaining: 4,
	}
}

func ResumeClaudeConversation(
	anthropicClient anthropic.Client,
	history conversationHistory,
	model anthropic.Model,
	maxTokens int64,
	tools []anthropic.ToolParam,
) (*ClaudeConversation, error) {
	c := &ClaudeConversation{
		client: anthropicClient,

		model:        model,
		systemPrompt: history.SystemPrompt,
		tools:        tools,
		messages:     history.Messages,

		maxTokens:            maxTokens,
		autoCacheThreshold:   10000,
		cachePointsRemaining: 4,
	}
	return c, nil
}

// SendMessage sends a user message with the given content and returns Claude's response
func (cc *ClaudeConversation) SendMessage(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, false, messageContent...)
}

// SendMessage creates a user message with the given content, sets a cache point on that message, sends it, and returns
// Claude's response
func (cc *ClaudeConversation) SendMessageAndSetCachePoint(ctx context.Context, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, true, messageContent...)
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *ClaudeConversation) sendMessage(ctx context.Context, setCachePoint bool, messageContent ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	if setCachePoint {
		if cc.cachePointsRemaining == 0 {
			log.Printf("Warning: cannot set cache point, no remaining cache points")
		} else {
			err := setCachePointOnLastApplicableBlockInContent(messageContent)
			if err != nil {
				log.Printf("Warning: failed to set cache point: %s", err)
			} else {
				cc.cachePointsRemaining--
			}
		}
	}

	cc.messages = append(cc.messages, conversationTurn{
		userMessage: anthropic.NewUserMessage(messageContent...),
	})

	messageParams := []anthropic.MessageParam{}
	for _, turn := range cc.messages {
		messageParams = append(messageParams, turn.userMessage)
		if turn.response != nil {
			messageParams = append(messageParams, turn.response.ToParam())
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

	if (response.Usage.InputTokens > cc.autoCacheThreshold) && cc.cachePointsRemaining != 0 {
		log.Println("Auto-caching")
		// Set the cache point on the last user message rather than the assistant response, since we don't know if there
		// will be text blocks in the response
		lastTurn := cc.messages[len(cc.messages)-1]
		err := setCachePointOnLastApplicableBlockInContent(lastTurn.userMessage.Content)
		if err != nil {
			log.Printf("Warning: failed to set cache point: %s", err)
		} else {
			cc.cachePointsRemaining--
		}
	}

	// Record the repsonse
	cc.messages[len(cc.messages)-1].response = &response

	// TODO remove this
	b, err := json.Marshal(append(messageParams, response.ToParam()))
	if err == nil {
		os.WriteFile("conversation.json", b, 0666)
	}

	return &response, nil
}

func setCachePointOnLastApplicableBlockInContent(content []anthropic.ContentBlockParamUnion) error {
	for i := len(content) - 1; i >= 0; i-- {
		c := content[i]
		var cacheControlParam *anthropic.CacheControlEphemeralParam
		if param := c.OfText; param != nil {
			cacheControlParam = &param.CacheControl
		} else if param := c.OfToolResult; param != nil {
			cacheControlParam = &param.CacheControl
		} else if param := c.OfToolUse; param != nil {
			cacheControlParam = &param.CacheControl
		}
		if cacheControlParam != nil {
			*cacheControlParam = anthropic.NewCacheControlEphemeralParam()
			break
		}

		if i == 0 {
			return fmt.Errorf("no cacheable blocks in content")
		}
	}

	return nil
}

// conversationHistory contains a serializable and resumable snapshot of a ClaudeConversation
type conversationHistory struct {
	SystemPrompt string             `json:"systemPrompt"`
	Messages     []conversationTurn `json:"messages"`
}

// History returns a serializable conversation history
func (cc *ClaudeConversation) History() conversationHistory {
	return conversationHistory{
		SystemPrompt: cc.systemPrompt,
		Messages:     cc.messages,
	}
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
