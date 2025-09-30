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

func TestFork(t *testing.T) {
	conv := &Conversation{
		systemPrompt:    "test prompt",
		model:           anthropic.ModelClaudeSonnet4_0,
		maxOutputTokens: 4000,
		Turns: []ConversationTurn{
			{
				UserInstructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 0")},
			},
			{
				UserInstructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 1")},
			},
			{
				UserInstructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 2")},
			},
		},
	}

	// Fork at turn 2 should include turns 0 and 1
	forked := conv.Fork(2)

	assert.Equal(t, "test prompt", forked.systemPrompt)
	assert.Equal(t, 2, len(forked.Turns))
	assert.Equal(t, "turn 0", forked.Turns[0].UserInstructions[0].OfText.Text)
	assert.Equal(t, "turn 1", forked.Turns[1].UserInstructions[0].OfText.Text)
}

func TestForkBounds(t *testing.T) {
	conv := &Conversation{
		Turns: []ConversationTurn{
			{UserInstructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 0")}},
		},
	}

	// Fork at negative index should result in empty conversation
	forked := conv.Fork(-1)
	assert.Equal(t, 0, len(forked.Turns))

	// Fork beyond length should include all turns
	forked = conv.Fork(10)
	assert.Equal(t, 1, len(forked.Turns))
}
