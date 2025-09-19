//go:build e2e

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/test/e2e/testutil"
)

// TestParallelToolCallsScenario tests that the AI uses parallel tool calls when appropriate
func TestParallelToolCallsScenario(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "parallel_tool_calls"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			// Create a conversation with a scenario that should trigger parallel tool calls
			systemPrompt := "You are Blundering Savant, a software developer. You have access to tools for file operations and commenting."

			conversation := harness.CreateConversation(systemPrompt)

			// Simulate a conversation history that should lead to multiple parallel actions
			scenarioPrompt := `You need to respond to multiple GitHub comments. Here are the comments:

Comment ID 123 (user @alice): "Could you explain the approach you're taking?"
Comment ID 456 (user @bob): "I think there might be a bug in the validation logic"  
Comment ID 789 (user @charlie): "The tests are failing, can you check?"

You should respond to all three comments and also add appropriate reactions. Use parallel tool calls for maximum efficiency.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(scenarioPrompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			// Analyze the response for parallel tool usage
			return analyzeParallelToolUsage(response)
		})
	})
}

// TestFileOperationsBatch tests parallel file operations
func TestFileOperationsBatch(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "parallel_file_operations"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer with access to file editing tools."

			conversation := harness.CreateConversation(systemPrompt)

			// Scenario that should trigger parallel file operations
			scenarioPrompt := `You need to update multiple files simultaneously. Please:

1. Create a file called "config.go" with basic configuration structure
2. Create a file called "main.go" with a main function
3. Create a file called "README.md" with project documentation
4. View the contents of all three files to confirm they were created

Use parallel tool calls for maximum efficiency when creating the files.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(scenarioPrompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			// Check that multiple tool calls are made in parallel
			toolUseCount := 0
			for _, content := range response.Content {
				if _, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					toolUseCount++
				}
			}

			if toolUseCount < 3 {
				return fmt.Errorf("expected at least 3 tool calls for creating files, got %d", toolUseCount)
			}

			// Verify that the AI mentions parallel execution or efficiency
			hasTextContent := false
			for _, content := range response.Content {
				if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
					hasTextContent = true
					text := strings.ToLower(textBlock.Text)
					if strings.Contains(text, "parallel") || strings.Contains(text, "simultaneously") || strings.Contains(text, "efficiency") {
						return nil // Success: AI is aware of parallel execution
					}
				}
			}

			if !hasTextContent {
				return fmt.Errorf("response contains no text blocks to analyze")
			}

			// Still consider it successful if multiple tools are used, even without explicit mention
			return nil
		})
	})
}

// TestReactionAndCommentCombination tests that AI can combine reactions and comments in parallel
func TestReactionAndCommentCombination(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "reaction_and_comment_parallel"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer who can react to and comment on GitHub discussions."

			conversation := harness.CreateConversation(systemPrompt)

			scenarioPrompt := `You received this comment on a pull request:

Comment ID 987 from @developer: "I've implemented the requested changes. The new validation logic should handle edge cases better. Let me know if you need any adjustments!"

You should both react positively to this comment (showing approval) and respond with a detailed comment thanking them and asking a follow-up question. Use parallel tool calls for efficiency.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(scenarioPrompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			// Check for both reaction and comment tools
			hasReactionTool := false
			hasCommentTool := false

			for _, content := range response.Content {
				if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					switch toolBlock.Name {
					case "add_reaction":
						hasReactionTool = true
					case "post_comment":
						hasCommentTool = true
					}
				}
			}

			if !hasReactionTool {
				return fmt.Errorf("expected add_reaction tool call, but none found")
			}

			if !hasCommentTool {
				return fmt.Errorf("expected post_comment tool call, but none found")
			}

			return nil
		})
	})
}

// analyzeParallelToolUsage analyzes a response for evidence of parallel tool usage
func analyzeParallelToolUsage(response *anthropic.Message) error {
	toolUseCount := 0
	var toolNames []string

	for _, content := range response.Content {
		if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseCount++
			toolNames = append(toolNames, toolBlock.Name)
		}
	}

	if toolUseCount == 0 {
		return fmt.Errorf("no tool calls found in response")
	}

	// For the parallel comments scenario, we expect multiple post_comment or add_reaction calls
	commentCalls := 0
	reactionCalls := 0

	for _, name := range toolNames {
		switch name {
		case "post_comment":
			commentCalls++
		case "add_reaction":
			reactionCalls++
		}
	}

	// We should have multiple comment/reaction operations for the parallel scenario
	totalActions := commentCalls + reactionCalls
	if totalActions < 2 {
		return fmt.Errorf("expected multiple parallel actions, got %d tool calls: %v", totalActions, toolNames)
	}

	return nil
}

// TestParallelValidationAndPublication tests workflow operations in parallel
func TestParallelValidationAndPublication(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "parallel_workflow_operations"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer working on code changes."

			conversation := harness.CreateConversation(systemPrompt)

			// Simulate having made code changes that need validation and publication
			scenarioPrompt := `You have made several code changes to fix an issue. The workspace has uncommitted changes that need to be:

1. Validated (run tests and static analysis)
2. Published for review once validation passes

After completing these workflow steps, you should also:
3. Post a comment explaining what changes were made
4. Add a positive reaction to show completion

Handle these operations efficiently with appropriate tool usage.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(scenarioPrompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			// The AI should at minimum use validate_changes tool
			hasValidationTool := false
			toolCount := 0

			for _, content := range response.Content {
				if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					toolCount++
					if toolBlock.Name == "validate_changes" {
						hasValidationTool = true
					}
				}
			}

			if !hasValidationTool {
				return fmt.Errorf("expected validate_changes tool call, but none found")
			}

			// Should have at least the validation tool
			if toolCount == 0 {
				return fmt.Errorf("no tool calls found, expected at least validation")
			}

			return nil
		})
	})
}