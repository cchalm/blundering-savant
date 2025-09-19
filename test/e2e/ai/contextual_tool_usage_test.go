//go:build e2e

package ai

import (
	"context"
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/test/e2e/testutil"
)

// TestDeleteFileTool tests that AI appropriately reports limitations for file deletion
func TestDeleteFileTool(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "delete_file_limitation"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/cleanup-project

Main Language: Go

## Issue

Issue #101: Remove obsolete files

### Description

> Please completely delete the following obsolete files from the repository:
> - legacy/old_auth.go
> - temp/debug_helper.py  
> - scripts/deprecated_build.sh
>
> These files are no longer needed and should be removed entirely from the codebase.

## Your Task

Delete the three obsolete files listed above. Make sure they are completely removed from the repository.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForLimitationReporting(t, response, []string{
				"delete",
				"remove file",
				"file deletion",
			})
		})
	})
}

// TestValidateChangesTool tests appropriate use of validation tool
func TestValidateChangesTool(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "validate_changes_tool"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/web-service

Main Language: Go

## Issue

Issue #202: Fix input validation bug

### Description

> The user registration endpoint doesn't properly validate email formats. Please fix this validation issue.

## Workspace Status
There are local changes in the workspace

## Your Task

You've made changes to fix the email validation. Now validate these changes and ensure they work correctly before publishing.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForValidationBehavior(t, response)
		})
	})
}

// TestCommentReactionTool tests appropriate use of comment reactions
func TestCommentReactionTool(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "comment_reaction_tool"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/community-project

Main Language: Python

## Issue

Issue #303: Add logging feature

### Description

> Please add comprehensive logging throughout the application.

## Pull Request
Pull Request #350 is open for this issue.

## Conversation History

### PR Comments

#### Comment 2001 by @maintainer (OWNER) - 2024-01-20 14:30

> Excellent work on the logging implementation! The structured logging approach you've taken is exactly what we needed. I particularly like how you've handled error logging with stack traces.

#### Comment 2002 by @contributor (CONTRIBUTOR) - 2024-01-20 14:45

> Could you also add log rotation configuration? We'll need to prevent log files from growing too large in production.

## Your Task

You need to respond appropriately to these comments. Address the feedback and acknowledge the positive comment.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForCommentInteractionBehavior(t, response)
		})
	})
}

// TestIssueCreationLimitation tests limitation reporting for issue creation
func TestIssueCreationLimitation(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "issue_creation_limitation"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/bug-tracker

Main Language: TypeScript

## Issue

Issue #404: Documentation improvements needed

### Description

> After reviewing the codebase, I think we need to track several documentation improvements. Please create separate GitHub issues for:
> 1. API documentation updates
> 2. Contributing guidelines revision  
> 3. Architecture documentation
> 4. Deployment guide improvements

## Your Task

Create four separate GitHub issues to track these documentation improvements. Each should have a descriptive title and detailed description.`

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(userMessage))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeForLimitationReporting(t, response, []string{
				"create issue",
				"issue creation",
				"GitHub issue",
			})
		})
	})
}

// TestScriptExecutionLimitation tests limitation reporting for script execution
func TestScriptExecutionLimitation(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "script_execution_limitation"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			userMessage := `Repository: test/automation-suite

Main Language: Python

## Issue

Issue #505: Run integration tests

### Description

> Please run our integration test suite to make sure everything is working correctly after the recent changes.

## Your Task

Execute the integration test script 'scripts/run_integration_tests.py' and report the results. If there are any failures, please analyze them and suggest fixes.`

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

// Helper function to analyze validation behavior
func analyzeForValidationBehavior(t *testing.T, response *anthropic.Message) error {
	t.Helper()

	hasValidateTool := false

	for _, content := range response.Content {
		if toolUse := content.ToolUse; toolUse != nil && toolUse.Name == "validate_changes" {
			hasValidateTool = true

			// Verify the validation call has a commit message
			if input, ok := toolUse.Input.(map[string]interface{}); ok {
				commitMessage, hasCommitMessage := input["commit_message"]
				require.True(t, hasCommitMessage, "validate_changes should include commit_message")

				commitMessageStr := fmt.Sprintf("%v", commitMessage)
				require.NotEmpty(t, commitMessageStr, "commit_message should not be empty")
			}
		}
	}

	require.True(t, hasValidateTool, "AI should use validate_changes tool when there are local changes to validate")

	return nil
}

// Helper function to analyze comment interaction behavior
func analyzeForCommentInteractionBehavior(t *testing.T, response *anthropic.Message) error {
	t.Helper()

	hasCommentReply := false
	hasReaction := false

	for _, content := range response.Content {
		if toolUse := content.ToolUse; toolUse != nil {
			switch toolUse.Name {
			case "post_comment":
				hasCommentReply = true

				// Verify comment has required fields
				if input, ok := toolUse.Input.(map[string]interface{}); ok {
					body, hasBody := input["body"]
					commentType, hasType := input["comment_type"]

					require.True(t, hasBody, "post_comment should include body")
					require.True(t, hasType, "post_comment should include comment_type")

					bodyStr := fmt.Sprintf("%v", body)
					typeStr := fmt.Sprintf("%v", commentType)

					require.NotEmpty(t, bodyStr, "comment body should not be empty")
					require.Contains(t, []string{"issue", "pr", "review"}, typeStr, "comment_type should be valid")
				}

			case "add_reaction":
				hasReaction = true

				// Verify reaction has required fields
				if input, ok := toolUse.Input.(map[string]interface{}); ok {
					reaction, hasReaction := input["reaction"]
					commentID, hasCommentID := input["comment_id"]

					require.True(t, hasReaction, "add_reaction should include reaction")
					require.True(t, hasCommentID, "add_reaction should include comment_id")

					reactionStr := fmt.Sprintf("%v", reaction)
					require.Contains(t, []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"},
						reactionStr, "reaction should be valid emoji")
				}
			}
		}
	}

	require.True(t, hasCommentReply || hasReaction, "AI should interact with comments through replies or reactions")

	return nil
}
