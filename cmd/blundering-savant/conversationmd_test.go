package main

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"
)

// TestConvertAssistantMessage_ToolOnlyResponse tests that token usage is properly attached
// to tool actions when there are no text blocks in an assistant response
func TestConvertAssistantMessage_ToolOnlyResponse(t *testing.T) {
	// Create a mock assistant message with only tool uses (no text blocks)
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			anthropic.NewToolUseBlock("tool1", "str_replace_based_edit_tool", []byte(`{"command": "view", "path": "test.go"}`)),
			anthropic.NewToolUseBlock("tool2", "post_comment", []byte(`{"comment_type": "issue", "body": "test comment"}`)),
		},
		Usage: anthropic.Usage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 10,
			CacheReadInputTokens:     5,
		},
	}

	pendingToolUses := make(map[string]conversationMessage)

	// Convert the message
	messages := convertAssistantMessage(msg, pendingToolUses)

	// Should have no messages returned yet (tool uses are stored in pendingToolUses)
	require.Len(t, messages, 0)

	// Check that tool uses are stored in pendingToolUses
	require.Len(t, pendingToolUses, 2)

	// The first tool use should have token usage attached
	tool1, exists := pendingToolUses["tool1"]
	require.True(t, exists)
	require.NotNil(t, tool1.TokenUsage)
	require.Equal(t, int64(100), tool1.TokenUsage.InputTokens)
	require.Equal(t, int64(50), tool1.TokenUsage.OutputTokens)
	require.Equal(t, int64(10), tool1.TokenUsage.CacheCreationTokens)
	require.Equal(t, int64(5), tool1.TokenUsage.CacheReadTokens)

	// The second tool use should not have token usage attached
	tool2, exists := pendingToolUses["tool2"]
	require.True(t, exists)
	require.Nil(t, tool2.TokenUsage)
}

// TestConvertAssistantMessage_TextWithTools tests that token usage is attached to text
// when both text and tool blocks are present
func TestConvertAssistantMessage_TextWithTools(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			anthropic.NewTextBlock("I'll help you with that task."),
			anthropic.NewToolUseBlock("tool1", "str_replace_based_edit_tool", []byte(`{"command": "view", "path": "test.go"}`)),
		},
		Usage: anthropic.Usage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 10,
			CacheReadInputTokens:     5,
		},
	}

	pendingToolUses := make(map[string]conversationMessage)

	// Convert the message
	messages := convertAssistantMessage(msg, pendingToolUses)

	// Should have 1 message returned (the text block)
	require.Len(t, messages, 1)

	// The text message should have token usage
	require.Equal(t, "assistant_text", messages[0].Type)
	require.NotNil(t, messages[0].TokenUsage)
	require.Equal(t, int64(100), messages[0].TokenUsage.InputTokens)

	// Check that tool use is stored in pendingToolUses without token usage
	require.Len(t, pendingToolUses, 1)
	tool1, exists := pendingToolUses["tool1"]
	require.True(t, exists)
	require.Nil(t, tool1.TokenUsage) // Should not have token usage since text got it
}

// TestConvertAssistantMessage_TextOnly tests that token usage is attached to text
// when only text blocks are present
func TestConvertAssistantMessage_TextOnly(t *testing.T) {
	msg := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{
			anthropic.NewTextBlock("This is a text-only response."),
		},
		Usage: anthropic.Usage{
			InputTokens:  75,
			OutputTokens: 25,
		},
	}

	pendingToolUses := make(map[string]conversationMessage)

	// Convert the message
	messages := convertAssistantMessage(msg, pendingToolUses)

	// Should have 1 message returned (the text block)
	require.Len(t, messages, 1)

	// The text message should have token usage
	require.Equal(t, "assistant_text", messages[0].Type)
	require.NotNil(t, messages[0].TokenUsage)
	require.Equal(t, int64(75), messages[0].TokenUsage.InputTokens)
	require.Equal(t, int64(25), messages[0].TokenUsage.OutputTokens)

	// No pending tool uses
	require.Len(t, pendingToolUses, 0)
}