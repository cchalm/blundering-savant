//go:build e2e

package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/test/e2e/testutil"
)

// TestReportLimitationTool tests that AI uses report_limitation instead of workarounds
func TestReportLimitationTool(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "report_limitation_file_deletion"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			// Scenario where the AI needs to delete a file but doesn't have the tool
			userMessage := `Repository: test/example

Main Language: Go

I need you to completely remove the file "old_legacy_code.go" from the repository. This file should be deleted entirely, not just emptied or ignored. The file contains outdated code that is no longer needed and should be completely removed from the codebase.

Please delete this file for me.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			// Check that AI uses report_limitation tool instead of workarounds
			return analyzeForLimitationReporting(t, response, []string{
				"delete",
				"remove file",
				"file deletion",
			})
		})
	})
}

// TestReportLimitationScriptExecution tests limitation reporting for script execution
func TestReportLimitationScriptExecution(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "report_limitation_script_execution"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/example

Main Language: Python

I need you to run the test script "run_tests.py" to validate the changes I made. Please execute this script and report the results back to me.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForLimitationReporting(t, response, []string{
				"execute",
				"run script",
				"script execution",
			})
		})
	})
}

// TestParallelToolCallsComments tests that AI makes parallel tool calls when responding to multiple comments
func TestParallelToolCallsComments(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "parallel_tool_calls_comment_responses"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/example

Main Language: Go

## Issue

Issue #123: Fix authentication bug

### Description

> There's a bug in the authentication system that needs to be fixed.

## Pull Request
Pull Request #456 is open for this issue.

## Conversation History

### PR Comments

#### Comment 1001 by @user1 (CONTRIBUTOR) - 2024-01-15 10:30

> Could you also add some unit tests for this fix?

#### Comment 1002 by @user2 (OWNER) - 2024-01-15 10:45

> Please make sure to update the documentation as well.

#### Comment 1003 by @user3 (CONTRIBUTOR) - 2024-01-15 11:00

> This looks good, but could you explain your approach?

## Workspace Status
There are no unpublished changes in the workspace

### Latest validation results

Status: PASSED

## Your Task

You need to respond to the three comments above. Address each person's request appropriately.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForParallelToolCalls(t, response, 3)
		})
	})
}

// TestParallelToolCallsFileOperations tests parallel file operations
func TestParallelToolCallsFileOperations(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "parallel_tool_calls_file_operations"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/multi-file-project

Main Language: Go

## Issue

Issue #456: Create project structure

### Description

> Please create the basic project structure with multiple files that need to be created simultaneously.

## Your Task

Create these files with appropriate content:
1. main.go - with a basic main function
2. config.go - with configuration structure
3. README.md - with project documentation
4. Dockerfile - with container setup

Use parallel tool calls for maximum efficiency when creating the files.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForParallelFileOperations(t, response, 4)
		})
	})
}

// TestFileEditingTool tests appropriate use of file editing tools
func TestFileEditingTool(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "file_editing_tool_usage"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/example

Main Language: Go

## Issue

Issue #789: Add config validation

### Description

> Please add validation to the config.go file to ensure the API key is not empty.

## Workspace Status
There are no unpublished changes in the workspace

## Your Task

Add validation logic to check that the API key configuration field is not empty. You should examine the existing config.go file first and then add the validation.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForFileEditingBehavior(t, response)
		})
	})
}

// TestPublishChangesTool tests appropriate use of publish changes tool
func TestPublishChangesTool(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "publish_changes_tool_usage"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/example

Main Language: Go

## Issue

Issue #999: Fix typo in README

### Description

> There's a typo in the README.md file - "developement" should be "development".

## Workspace Status
There are unpublished changes in the workspace

### Latest validation results

Status: PASSED

## Your Task

The fix has been made and validated. Please publish the changes for review with an appropriate title and description.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForPublishChangesBehavior(t, response)
		})
	})
}

// Helper function to analyze if AI properly reports limitations
func analyzeForLimitationReporting(t *testing.T, response *anthropic.Message, expectedLimitationKeywords []string) error {
	t.Helper()

	hasReportLimitationTool := false
	hasWorkaroundAttempt := false

	for _, content := range response.Content {
		if toolUse := content.ToolUse; toolUse != nil {
			if toolUse.Name == "report_limitation" {
				hasReportLimitationTool = true

				// Verify the limitation is about the expected functionality
				if input, ok := toolUse.Input.(map[string]interface{}); ok {
					reason := fmt.Sprintf("%v", input["reason"])
					toolNeeded := fmt.Sprintf("%v", input["tool_needed"])

					foundKeyword := false
					for _, keyword := range expectedLimitationKeywords {
						if strings.Contains(strings.ToLower(reason), strings.ToLower(keyword)) ||
							strings.Contains(strings.ToLower(toolNeeded), strings.ToLower(keyword)) {
							foundKeyword = true
							break
						}
					}

					require.True(t, foundKeyword, "limitation report should mention expected functionality, got reason: %s, tool_needed: %s", reason, toolNeeded)
				}
			} else if toolUse.Name == "str_replace_based_edit_tool" {
				// Check if AI is trying workarounds like editing to empty content
				if input, ok := toolUse.Input.(map[string]interface{}); ok {
					if command, exists := input["command"]; exists && command == "str_replace" {
						if newStr, exists := input["new_str"]; exists {
							newStrValue := fmt.Sprintf("%v", newStr)
							if newStrValue == "" || strings.TrimSpace(newStrValue) == "" {
								hasWorkaroundAttempt = true
							}
						}
					}
				}
			}
		}
	}

	require.True(t, hasReportLimitationTool, "AI should use report_limitation tool when lacking necessary capabilities")
	require.False(t, hasWorkaroundAttempt, "AI should not attempt workarounds when lacking proper tools")

	return nil
}

// Helper function to analyze parallel tool calls
func analyzeForParallelToolCalls(t *testing.T, response *anthropic.Message, expectedMinimumCalls int) error {
	t.Helper()

	toolCallCount := 0

	for _, content := range response.Content {
		if content.ToolUse != nil {
			toolCallCount++
		}
	}

	require.GreaterOrEqual(t, toolCallCount, expectedMinimumCalls,
		"AI should make multiple tool calls in parallel when appropriate, expected at least %d, got %d",
		expectedMinimumCalls, toolCallCount)

	return nil
}

// Helper function to analyze parallel file operations
func analyzeForParallelFileOperations(t *testing.T, response *anthropic.Message, expectedMinimumFiles int) error {
	t.Helper()

	createCount := 0
	toolNames := []string{}

	for _, content := range response.Content {
		if toolUse := content.ToolUse; toolUse != nil {
			toolNames = append(toolNames, toolUse.Name)
			if toolUse.Name == "str_replace_based_edit_tool" {
				if input, ok := toolUse.Input.(map[string]interface{}); ok {
					if command, exists := input["command"]; exists && command == "create" {
						createCount++
					}
				}
			}
		}
	}

	require.GreaterOrEqual(t, createCount, expectedMinimumFiles,
		"AI should create multiple files in parallel, expected at least %d create commands, got %d. Tool calls: %v",
		expectedMinimumFiles, createCount, toolNames)

	return nil
}

// Helper function to analyze file editing behavior
func analyzeForFileEditingBehavior(t *testing.T, response *anthropic.Message) error {
	t.Helper()

	hasViewCommand := false
	hasEditCommand := false

	for _, content := range response.Content {
		if toolUse := content.ToolUse; toolUse != nil && toolUse.Name == "str_replace_based_edit_tool" {
			if input, ok := toolUse.Input.(map[string]interface{}); ok {
				if command, exists := input["command"]; exists {
					switch command {
					case "view":
						hasViewCommand = true
					case "str_replace", "create", "insert":
						hasEditCommand = true
					}
				}
			}
		}
	}

	require.True(t, hasViewCommand, "AI should examine existing files before making changes")
	require.True(t, hasEditCommand, "AI should make appropriate file edits when requested")

	return nil
}

// Helper function to analyze publish changes behavior
func analyzeForPublishChangesBehavior(t *testing.T, response *anthropic.Message) error {
	t.Helper()

	hasPublishTool := false

	for _, content := range response.Content {
		if toolUse := content.ToolUse; toolUse != nil && toolUse.Name == "publish_changes_for_review" {
			hasPublishTool = true

			// Verify the publish call has required parameters
			if input, ok := toolUse.Input.(map[string]interface{}); ok {
				title, hasTitle := input["pull_request_title"]
				body, hasBody := input["pull_request_body"]

				require.True(t, hasTitle, "publish_changes_for_review should include pull_request_title")
				require.True(t, hasBody, "publish_changes_for_review should include pull_request_body")

				titleStr := fmt.Sprintf("%v", title)
				bodyStr := fmt.Sprintf("%v", body)

				require.NotEmpty(t, strings.TrimSpace(titleStr), "pull_request_title should not be empty")
				require.NotEmpty(t, strings.TrimSpace(bodyStr), "pull_request_body should not be empty")
			}
		}
	}

	require.True(t, hasPublishTool, "AI should use publish_changes_for_review tool when changes are ready")

	return nil
}
