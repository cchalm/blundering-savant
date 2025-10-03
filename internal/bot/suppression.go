package bot

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/cchalm/blundering-savant/internal/ai"
)

// newValidationSuppressionFilter creates an output filter that keeps only the most recent
// validation result in the conversation history sent to the AI. All earlier validation results
// are replaced with a brief message indicating the content was suppressed.
//
// This reduces token usage while maintaining the full conversation history for forking purposes.
func newValidationSuppressionFilter() ai.OutputFilter {
	return func(turns []ai.ConversationTurnParam) []ai.ConversationTurnParam {
		// First pass: find the index of the last turn containing a validation result
		lastValidationTurnIndex := -1
		for i := len(turns) - 1; i >= 0; i-- {
			for _, exchange := range turns[i].ToolExchanges {
				if exchange.UseBlock.Name == "validate_changes" {
					lastValidationTurnIndex = i
					break
				}
			}
			if lastValidationTurnIndex != -1 {
				break
			}
		}

		// If no validation found, return turns unchanged
		if lastValidationTurnIndex == -1 {
			return turns
		}

		// Second pass: suppress all validation results except the last one
		modifiedTurns := make([]ai.ConversationTurnParam, len(turns))
		for i, turn := range turns {
			// If this is the last validation turn, keep it unchanged
			if i == lastValidationTurnIndex {
				modifiedTurns[i] = turn
				continue
			}

			// Check if this turn has any validation results to suppress
			hasValidation := false
			for _, exchange := range turn.ToolExchanges {
				if exchange.UseBlock.Name == "validate_changes" {
					hasValidation = true
					break
				}
			}

			if !hasValidation {
				modifiedTurns[i] = turn
				continue
			}

			// Suppress validation results in this turn
			modifiedTurn := turn
			modifiedToolExchanges := make([]ai.ToolExchangeParam, len(turn.ToolExchanges))
			copy(modifiedToolExchanges, turn.ToolExchanges)

			for j, exchange := range modifiedToolExchanges {
				if exchange.UseBlock.Name == "validate_changes" {
					// Replace the result content with a suppression message
					suppressedResult := exchange.ResultBlock
					suppressedResult.Content = []anthropic.ToolResultBlockParamContentUnion{
						{OfText: &anthropic.TextBlockParam{
							Text: "[Validation result suppressed - see most recent validation for current status]",
						}},
					}
					modifiedToolExchanges[j].ResultBlock = suppressedResult
				}
			}

			modifiedTurn.ToolExchanges = modifiedToolExchanges
			modifiedTurns[i] = modifiedTurn
		}

		return modifiedTurns
	}
}
