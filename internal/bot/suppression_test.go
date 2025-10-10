package bot

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidationSuppressionFilter_SingleValidation(t *testing.T) {
	// Create a turn with a single validation result
	validationToolUse := anthropic.ToolUseBlockParam{
		ID:    "val_123",
		Name:  "validate_changes",
		Input: []byte(`{"commit_message":"test commit"}`),
	}

	validationResult := anthropic.ToolResultBlockParam{
		ToolUseID: "val_123",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "validation succeeded"}},
		},
	}

	turns := []ai.ConversationTurnParam{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("validate changes"),
			},
			ToolExchanges: []ai.ToolExchangeParam{
				{UseBlock: validationToolUse, ResultBlock: validationResult},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Apply filter
	filteredTurns := filter(turns)

	// With only one validation, it should NOT be suppressed
	require.Len(t, filteredTurns, 1)
	require.Len(t, filteredTurns[0].ToolExchanges, 1)
	assert.Equal(t, "validation succeeded", filteredTurns[0].ToolExchanges[0].ResultBlock.Content[0].OfText.Text)
}

func TestValidationSuppressionFilter_MultipleValidations(t *testing.T) {
	// Create turns with multiple validation filteredTurns
	validation1 := anthropic.ToolUseBlockParam{
		ID:    "val_1",
		Name:  "validate_changes",
		Input: []byte(`{"commit_message":"commit 1"}`),
	}
	result1 := anthropic.ToolResultBlockParam{
		ToolUseID: "val_1",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "validation 1 failed"}},
		},
	}

	validation2 := anthropic.ToolUseBlockParam{
		ID:    "val_2",
		Name:  "validate_changes",
		Input: []byte(`{"commit_message":"commit 2"}`),
	}
	result2 := anthropic.ToolResultBlockParam{
		ToolUseID: "val_2",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "validation 2 succeeded"}},
		},
	}

	turns := []ai.ConversationTurnParam{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("first validation"),
			},
			ToolExchanges: []ai.ToolExchangeParam{
				{UseBlock: validation1, ResultBlock: result1},
			},
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("some other instruction"),
			},
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("second validation"),
			},
			ToolExchanges: []ai.ToolExchangeParam{
				{UseBlock: validation2, ResultBlock: result2},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Apply filter
	filteredTurns := filter(turns)

	// First validation should be suppressed
	require.Len(t, filteredTurns[0].ToolExchanges, 1)
	assert.Contains(t, filteredTurns[0].ToolExchanges[0].ResultBlock.Content[0].OfText.Text, "suppressed")

	// Second turn has no validation, should be unchanged
	assert.Equal(t, turns[1], filteredTurns[1])

	// Last validation should NOT be suppressed
	require.Len(t, filteredTurns[2].ToolExchanges, 1)
	assert.Equal(t, "validation 2 succeeded", filteredTurns[2].ToolExchanges[0].ResultBlock.Content[0].OfText.Text)
}

func TestValidationSuppressionFilter_MixedToolCalls(t *testing.T) {
	// Create a turn with both validation and other tool calls
	validation := anthropic.ToolUseBlockParam{
		ID:    "val_1",
		Name:  "validate_changes",
		Input: []byte(`{"commit_message":"commit"}`),
	}
	validationResult := anthropic.ToolResultBlockParam{
		ToolUseID: "val_1",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "validation succeeded"}},
		},
	}

	otherTool := anthropic.ToolUseBlockParam{
		ID:    "tool_1",
		Name:  "other_tool",
		Input: []byte(`{}`),
	}
	otherResult := anthropic.ToolResultBlockParam{
		ToolUseID: "tool_1",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "other tool result"}},
		},
	}

	latestValidation := anthropic.ToolUseBlockParam{
		ID:    "val_2",
		Name:  "validate_changes",
		Input: []byte(`{"commit_message":"commit 2"}`),
	}
	latestResult := anthropic.ToolResultBlockParam{
		ToolUseID: "val_2",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "latest validation"}},
		},
	}

	turns := []ai.ConversationTurnParam{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("mixed tools"),
			},
			ToolExchanges: []ai.ToolExchangeParam{
				{UseBlock: validation, ResultBlock: validationResult},
				{UseBlock: otherTool, ResultBlock: otherResult},
			},
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("latest validation"),
			},
			ToolExchanges: []ai.ToolExchangeParam{
				{UseBlock: latestValidation, ResultBlock: latestResult},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Apply filter
	filteredTurns := filter(turns)

	// Validation should be suppressed, other tool should not
	require.Len(t, filteredTurns[0].ToolExchanges, 2)
	assert.Contains(t, filteredTurns[0].ToolExchanges[0].ResultBlock.Content[0].OfText.Text, "suppressed")
	assert.Equal(t, "other tool result", filteredTurns[0].ToolExchanges[1].ResultBlock.Content[0].OfText.Text)
}

func TestValidationSuppressionFilter_NoValidations(t *testing.T) {
	// Create turns with no validation filteredTurns
	otherTool := anthropic.ToolUseBlockParam{
		ID:    "tool_1",
		Name:  "other_tool",
		Input: []byte(`{}`),
	}
	otherResult := anthropic.ToolResultBlockParam{
		ToolUseID: "tool_1",
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: "other tool result"}},
		},
	}

	turns := []ai.ConversationTurnParam{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("no validations"),
			},
			ToolExchanges: []ai.ToolExchangeParam{
				{UseBlock: otherTool, ResultBlock: otherResult},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Apply filter
	filteredTurns := filter(turns)

	// Nothing should be suppressed
	assert.Equal(t, turns, filteredTurns)
}
