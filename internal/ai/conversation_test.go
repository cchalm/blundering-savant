package ai

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testNeedsSummarization is a test harness for NeedsSummarization method
func testNeedsSummarization(t *testing.T, inputTokens int64, cacheReadTokens int64, tokenLimit int64, expected bool) {
	// Create a mock response with the specified token usage
	response := &anthropic.Message{
		Usage: anthropic.Usage{
			InputTokens:          inputTokens,
			CacheReadInputTokens: cacheReadTokens,
		},
	}

	conv := &Conversation{
		tokenLimit: tokenLimit,
		Messages: []ConversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("test")),
				Response:    response,
			},
		},
	}

	result := conv.NeedsSummarization()
	assert.Equal(t, expected, result)
}

func TestNeedsSummarization_BelowLimit(t *testing.T) {
	testNeedsSummarization(t, 40000, 30000, 100000, false)
}

func TestNeedsSummarization_AtLimit(t *testing.T) {
	testNeedsSummarization(t, 50000, 50000, 100000, false)
}

func TestNeedsSummarization_AboveLimit(t *testing.T) {
	testNeedsSummarization(t, 60000, 50000, 100000, true)
}

func TestNeedsSummarization_WayAboveLimit(t *testing.T) {
	testNeedsSummarization(t, 150000, 75000, 100000, true)
}

func TestNeedsSummarization_NoMessages(t *testing.T) {
	conv := &Conversation{
		tokenLimit: 100000,
	}

	result := conv.NeedsSummarization()
	assert.Equal(t, false, result)
}

func TestNeedsSummarization_MessageWithoutResponse(t *testing.T) {
	conv := &Conversation{
		tokenLimit: 100000,
		Messages: []ConversationTurn{
			{UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("test"))},
		},
	}

	result := conv.NeedsSummarization()
	assert.Equal(t, false, result)
}

func TestConversationHistory(t *testing.T) {
	conv := &Conversation{
		systemPrompt: "test prompt",
		Messages:     []ConversationTurn{},
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
		Messages:     []ConversationTurn{},
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
		Messages: []ConversationTurn{
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
