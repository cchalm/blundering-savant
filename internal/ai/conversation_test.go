package ai

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNeedsSummarization(t *testing.T) {
	tests := []struct {
		name           string
		inputTokens    int64
		cacheReadTokens int64
		tokenLimit     int64
		expected       bool
	}{
		{
			name:            "Below limit",
			inputTokens:     40000,
			cacheReadTokens: 30000,
			tokenLimit:      100000,
			expected:        false,
		},
		{
			name:            "At limit",
			inputTokens:     50000,
			cacheReadTokens: 50000,
			tokenLimit:      100000,
			expected:        false,
		},
		{
			name:            "Above limit",
			inputTokens:     60000,
			cacheReadTokens: 50000,
			tokenLimit:      100000,
			expected:        true,
		},
		{
			name:            "Way above limit",
			inputTokens:     150000,
			cacheReadTokens: 75000,
			tokenLimit:      100000,
			expected:        true,
		},
		{
			name:        "No messages",
			tokenLimit:  100000,
			expected:    false,
		},
		{
			name:        "Message without response",
			tokenLimit:  100000,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := &Conversation{
				tokenLimit: tt.tokenLimit,
			}

			if tt.name == "No messages" {
				// Empty conversation
			} else if tt.name == "Message without response" {
				// Add a message without a response
				conv.Messages = []conversationTurn{
					{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("test"))},
				}
			} else {
				// Create a mock response with the specified token usage
				response := &anthropic.Message{
					Usage: anthropic.Usage{
						InputTokens:          tt.inputTokens,
						CacheReadInputTokens: tt.cacheReadTokens,
					},
				}
				conv.Messages = []conversationTurn{
					{
						UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("test")),
						Response:    response,
					},
				}
			}

			result := conv.NeedsSummarization()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConversationHistory(t *testing.T) {
	conv := &Conversation{
		systemPrompt: "test prompt",
		Messages:     []conversationTurn{},
	}

	history := conv.History()

	assert.Equal(t, "test prompt", history.SystemPrompt)
	assert.Equal(t, 0, len(history.Messages))
}

func TestResumeConversation(t *testing.T) {
	// Create a mock anthropic client
	client := anthropic.Client{}
	
	history := ConversationHistory{
		SystemPrompt: "test system prompt",
		Messages:     []conversationTurn{},
	}

	conv, err := ResumeConversation(client, history, anthropic.ModelClaudeSonnet4_0, 4000, []anthropic.ToolParam{})
	
	require.NoError(t, err)
	assert.Equal(t, "test system prompt", conv.systemPrompt)
	assert.Equal(t, int64(100000), conv.tokenLimit)
	assert.Equal(t, 0, len(conv.Messages))
}

// Test that summarization preserves expected structure
func TestSummarizeConversationStructure(t *testing.T) {
	// Create a conversation with enough messages to test summarization
	// Create a mock response that exceeds the token limit
	response := &anthropic.Message{
		Usage: anthropic.Usage{
			InputTokens:          120000,
			CacheReadInputTokens: 10000,
		},
	}
	
	conv := &Conversation{
		tokenLimit: 100000,
		Messages: []conversationTurn{
			// First message (should be preserved)
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Initial message"))},
			// Middle messages (would be summarized in actual implementation)
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Middle message 1"))},
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Middle message 2"))},
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Middle message 3"))},
			// Last message with response that exceeds token limit
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Final message")),
				Response:    response,
			},
		},
	}

	// We can't easily test the full summarization without a real API client,
	// but we can test that the logic correctly identifies when summarization is needed
	// and the structure expectations
	assert.True(t, conv.NeedsSummarization())
	assert.Equal(t, 5, len(conv.Messages))

	// For now, we'll skip the actual AI summarization test since it requires a real API client
	// In a real test environment, you might use a mock client or integration test
}

func TestNewConversationDefaults(t *testing.T) {
	client := anthropic.Client{}
	model := anthropic.ModelClaudeSonnet4_0
	maxTokens := int64(4000)
	tools := []anthropic.ToolParam{}
	systemPrompt := "test system prompt"

	conv := NewConversation(client, model, maxTokens, tools, systemPrompt)

	assert.Equal(t, model, conv.model)
	assert.Equal(t, maxTokens, conv.maxTokens)
	assert.Equal(t, systemPrompt, conv.systemPrompt)
	assert.Equal(t, int64(100000), conv.tokenLimit) // Default token limit
	assert.Equal(t, 0, len(conv.Messages))
}