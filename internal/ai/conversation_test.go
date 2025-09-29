package ai

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationHistory(t *testing.T) {
	conv := &Conversation{
		systemPrompt: "test prompt",
		Turns:        []ConversationTurn{},
	}

	history := conv.History()

	assert.Equal(t, "test prompt", history.SystemPrompt)
	assert.Equal(t, 0, len(history.Messages))
}

func TestResumeConversation(t *testing.T) {
	history := ConversationHistory{
		SystemPrompt: "test system prompt",
		Messages:     []ConversationTurn{},
	}

	conv, err := ResumeConversation(nil, history, anthropic.ModelClaudeSonnet4_0, 4000, []anthropic.ToolParam{})

	require.NoError(t, err)
	assert.Equal(t, "test system prompt", conv.systemPrompt)
	assert.Equal(t, 0, len(conv.Turns))
}
