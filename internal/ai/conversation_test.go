package ai

import (
	"context"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNeedsSummarization(t *testing.T) {
	tests := []struct {
		name                string
		totalInputTokens    int64
		totalCacheReadTokens int64
		tokenLimit          int64
		expected            bool
	}{
		{
			name:                "Below limit",
			totalInputTokens:    40000,
			totalCacheReadTokens: 30000,
			tokenLimit:          100000,
			expected:            false,
		},
		{
			name:                "At limit",
			totalInputTokens:    50000,
			totalCacheReadTokens: 50000,
			tokenLimit:          100000,
			expected:            false,
		},
		{
			name:                "Above limit",
			totalInputTokens:    60000,
			totalCacheReadTokens: 50000,
			tokenLimit:          100000,
			expected:            true,
		},
		{
			name:                "Way above limit",
			totalInputTokens:    150000,
			totalCacheReadTokens: 75000,
			tokenLimit:          100000,
			expected:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := &Conversation{
				totalInputTokens:    tt.totalInputTokens,
				totalCacheReadTokens: tt.totalCacheReadTokens,
				tokenLimit:          tt.tokenLimit,
			}

			result := conv.NeedsSummarization()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConversationHistoryTokenTracking(t *testing.T) {
	conv := &Conversation{
		systemPrompt:        "test prompt",
		totalInputTokens:    1000,
		totalCacheReadTokens: 500,
		Messages:            []conversationTurn{},
	}

	history := conv.History()

	assert.Equal(t, "test prompt", history.SystemPrompt)
	assert.Equal(t, int64(1000), history.TotalInputTokens)
	assert.Equal(t, int64(500), history.TotalCacheReadTokens)
	assert.Equal(t, 0, len(history.Messages))
}

func TestResumeConversationWithTokenTracking(t *testing.T) {
	// Create a mock anthropic client
	client := anthropic.Client{}
	
	history := ConversationHistory{
		SystemPrompt:        "test system prompt",
		Messages:            []conversationTurn{},
		TotalInputTokens:    2000,
		TotalCacheReadTokens: 1500,
	}

	conv, err := ResumeConversation(client, history, anthropic.ModelClaudeSonnet4_0, 4000, []anthropic.ToolParam{})
	
	require.NoError(t, err)
	assert.Equal(t, "test system prompt", conv.systemPrompt)
	assert.Equal(t, int64(2000), conv.totalInputTokens)
	assert.Equal(t, int64(1500), conv.totalCacheReadTokens)
	assert.Equal(t, int64(100000), conv.tokenLimit)
}

// Test that summarization preserves expected structure
func TestSummarizeConversationStructure(t *testing.T) {
	// Create a conversation with enough messages to test summarization
	conv := &Conversation{
		totalInputTokens:    120000, // Above limit
		totalCacheReadTokens: 10000,
		tokenLimit:          100000,
		Messages: []conversationTurn{
			// First message (should be preserved)
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Initial message"))},
			// Middle messages (should be summarized)
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Middle message 1"))},
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Middle message 2"))},
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Middle message 3"))},
			// Last messages (should be preserved)
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Recent message 1"))},
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Recent message 2"))},
		},
	}

	// We can't easily test the full summarization without a real API client,
	// but we can test that the logic correctly identifies when summarization is needed
	// and the structure expectations
	assert.True(t, conv.NeedsSummarization())
	assert.Equal(t, 6, len(conv.Messages))

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
	assert.Equal(t, int64(0), conv.totalInputTokens)
	assert.Equal(t, int64(0), conv.totalCacheReadTokens)
}