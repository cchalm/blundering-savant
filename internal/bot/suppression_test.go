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
	validationToolUse := anthropic.ToolUseBlock{
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

	turns := []ai.ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("validate changes"),
			},
			ToolExchanges: []ai.ToolExchange{
				{UseBlock: validationToolUse, ResultBlock: &validationResult},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Convert to params and apply filter
	turnParams := make([]ai.ConversationTurnParam, len(turns))
	for i, turn := range turns {
		tp, err := turn.ToParam()
		require.NoError(t, err)
		turnParams[i] = tp
	}
	results := filter(turnParams)

	// With only one validation, it should NOT be suppressed
	require.Len(t, results, 1)
	require.Len(t, results[0].ToolExchanges, 1)
	assert.Equal(t, "validation succeeded", results[0].ToolExchanges[0].ResultBlock.Content[0].OfText.Text)
}

func TestValidationSuppressionFilter_MultipleValidations(t *testing.T) {
	// Create turns with multiple validation results
	validation1 := anthropic.ToolUseBlock{
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

	validation2 := anthropic.ToolUseBlock{
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

	turns := []ai.ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("first validation"),
			},
			ToolExchanges: []ai.ToolExchange{
				{UseBlock: validation1, ResultBlock: &result1},
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
			ToolExchanges: []ai.ToolExchange{
				{UseBlock: validation2, ResultBlock: &result2},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Convert to params and apply filter
	turnParams := make([]ai.ConversationTurnParam, len(turns))
	for i, turn := range turns {
		tp, err := turn.ToParam()
		require.NoError(t, err)
		turnParams[i] = tp
	}
	results := filter(turnParams)

	// First validation should be suppressed
	require.Len(t, results[0].ToolExchanges, 1)
	assert.Contains(t, results[0].ToolExchanges[0].ResultBlock.Content[0].OfText.Text, "suppressed")

	// Second turn has no validation, should be unchanged
	assert.Equal(t, turnParams[1], results[1])

	// Last validation should NOT be suppressed
	require.Len(t, results[2].ToolExchanges, 1)
	assert.Equal(t, "validation 2 succeeded", results[2].ToolExchanges[0].ResultBlock.Content[0].OfText.Text)
}

func TestValidationSuppressionFilter_MixedToolCalls(t *testing.T) {
	// Create a turn with both validation and other tool calls
	validation := anthropic.ToolUseBlock{
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

	otherTool := anthropic.ToolUseBlock{
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

	latestValidation := anthropic.ToolUseBlock{
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

	turns := []ai.ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("mixed tools"),
			},
			ToolExchanges: []ai.ToolExchange{
				{UseBlock: validation, ResultBlock: &validationResult},
				{UseBlock: otherTool, ResultBlock: &otherResult},
			},
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("latest validation"),
			},
			ToolExchanges: []ai.ToolExchange{
				{UseBlock: latestValidation, ResultBlock: &latestResult},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Convert to params and apply filter
	turnParams := make([]ai.ConversationTurnParam, len(turns))
	for i, turn := range turns {
		tp, err := turn.ToParam()
		require.NoError(t, err)
		turnParams[i] = tp
	}
	results := filter(turnParams)

	// Validation should be suppressed, other tool should not
	require.Len(t, results[0].ToolExchanges, 2)
	assert.Contains(t, results[0].ToolExchanges[0].ResultBlock.Content[0].OfText.Text, "suppressed")
	assert.Equal(t, "other tool result", results[0].ToolExchanges[1].ResultBlock.Content[0].OfText.Text)
}

func TestValidationSuppressionFilter_NoValidations(t *testing.T) {
	// Create turns with no validation results
	otherTool := anthropic.ToolUseBlock{
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

	turns := []ai.ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("no validations"),
			},
			ToolExchanges: []ai.ToolExchange{
				{UseBlock: otherTool, ResultBlock: &otherResult},
			},
		},
	}

	filter := newValidationSuppressionFilter()

	// Convert to params and apply filter
	turnParams := make([]ai.ConversationTurnParam, len(turns))
	for i, turn := range turns {
		tp, err := turn.ToParam()
		require.NoError(t, err)
		turnParams[i] = tp
	}
	results := filter(turnParams)

	// Nothing should be suppressed
	assert.Equal(t, turnParams, results)
}
