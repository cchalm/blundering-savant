//go:build e2e

package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/validator"
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

			// Create a realistic task for file deletion
			testTask := createTestTask(101, "Remove obsolete files",
				"Please completely delete the following obsolete files from the repository:\n"+
					"- legacy/old_auth.go\n"+
					"- temp/debug_helper.py\n"+
					"- scripts/deprecated_build.sh\n\n"+
					"These files are no longer needed and should be removed entirely from the codebase.")

			// Build the real prompt using BuildPrompt
			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(taskContent))
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

			// Create initial task
			testTask := createTestTask(202, "Fix input validation bug",
				"The user registration endpoint doesn't properly validate email formats. Please fix this validation issue.")
			testTask.HasUnpublishedChanges = true

			// Build the real initial prompt
			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			// Send initial message
			_, err = conversation.SendMessage(ctx, anthropic.NewTextBlock(taskContent))
			if err != nil {
				return fmt.Errorf("failed to send initial message: %w", err)
			}

			// Simulate the AI having made file changes by manually constructing conversation history
			// This simulates the AI having already used str_replace_based_edit_tool
			fileEditResponse := anthropic.Message{
				Role: anthropic.MessageRoleAssistant,
				Content: []anthropic.MessageContent{
					{
						Text: "I'll fix the email validation issue by updating the registration endpoint.",
					},
					{
						ToolUse: &anthropic.ToolUseBlock{
							ID:   "edit-1",
							Name: "str_replace_based_edit_tool",
							Input: map[string]interface{}{
								"command": "str_replace",
								"path":    "api/auth.go",
								"old_str": `if email == "" {
	return errors.New("email required")
}`,
								"new_str": `if email == "" {
	return errors.New("email required")
}
if !isValidEmail(email) {
	return errors.New("invalid email format")
}`,
							},
						},
					},
				},
			}

			// Add the simulated file edit to conversation history
			conversation.AddMessage(fileEditResponse)

			// Add tool result
			toolResult := anthropic.Message{
				Role: anthropic.MessageRoleUser,
				Content: []anthropic.MessageContent{
					{
						ToolResult: &anthropic.ToolResultBlock{
							ToolUseID: "edit-1",
							Content: []anthropic.ToolResultBlockContentUnion{
								{
									Type: "text",
									Text: "Successfully updated api/auth.go",
								},
							},
						},
					},
				},
			}
			conversation.AddMessage(toolResult)

			// Now send a message that should trigger validation
			followupMessage := "The email validation has been implemented. What's the next step?"

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(followupMessage))
			if err != nil {
				return fmt.Errorf("failed to send followup message: %w", err)
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

			// Create task with PR and comments requiring responses
			testTask := createTestTask(303, "Add logging feature",
				"Please add comprehensive logging throughout the application.")

			testTask.PullRequest = &task.GithubPullRequest{
				Owner:  "test",
				Repo:   "repository",
				Number: 350,
				Title:  "Add logging feature",
			}

			// Add comments that require responses
			testTask.PRCommentsRequiringResponses = []*github.IssueComment{
				{
					ID:                github.Int64(2001),
					Body:              github.String("Excellent work on the logging implementation! The structured logging approach you've taken is exactly what we needed. I particularly like how you've handled error logging with stack traces."),
					User:              &github.User{Login: github.String("maintainer")},
					AuthorAssociation: github.String("OWNER"),
				},
				{
					ID:                github.Int64(2002),
					Body:              github.String("Could you also add log rotation configuration? We'll need to prevent log files from growing too large in production."),
					User:              &github.User{Login: github.String("contributor")},
					AuthorAssociation: github.String("CONTRIBUTOR"),
				},
			}

			// Build the real prompt
			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(taskContent))
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

			// Create task requesting issue creation (which isn't supported)
			testTask := createTestTask(404, "Documentation improvements needed",
				"After reviewing the codebase, I think we need to track several documentation improvements. Please create separate GitHub issues for:\n"+
					"1. API documentation updates\n"+
					"2. Contributing guidelines revision\n"+
					"3. Architecture documentation\n"+
					"4. Deployment guide improvements\n\n"+
					"Create four separate GitHub issues to track these documentation improvements. Each should have a descriptive title and detailed description.")
			testTask.CodebaseInfo.MainLanguage = "TypeScript"

			// Build the real prompt
			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(taskContent))
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

			// Create task requesting script execution (which isn't supported)
			testTask := createTestTask(505, "Run integration tests",
				"Please run our integration test suite to make sure everything is working correctly after the recent changes.\n\n"+
					"Execute the integration test script 'scripts/run_integration_tests.py' and report the results. If there are any failures, please analyze them and suggest fixes.")
			testTask.CodebaseInfo.MainLanguage = "Python"

			// Build the real prompt
			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(taskContent))
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

	toolUse := requireToolUse(t, response, "validate_changes")

	// Verify the validation call has a commit message
	if input, ok := toolUse.Input.(map[string]interface{}); ok {
		commitMessage, hasCommitMessage := input["commit_message"]
		require.True(t, hasCommitMessage, "validate_changes should include commit_message")

		commitMessageStr := fmt.Sprintf("%v", commitMessage)
		require.NotEmpty(t, commitMessageStr, "commit_message should not be empty")
	}

	return nil
}

// Helper function to analyze comment interaction behavior
func analyzeForCommentInteractionBehavior(t *testing.T, response *anthropic.Message) error {
	t.Helper()

	// Expect multiple comments and reactions for multiple PR comments
	commentTool := "post_comment"
	reactTool := "add_reaction"

	toolUses := requireToolUses(t, response, map[string]int{
		commentTool: -1, // Allow any positive number
		reactTool:   -1, // Allow any positive number
	})

	// We should have at least one comment or reaction
	totalTools := len(toolUses[commentTool]) + len(toolUses[reactTool])
	require.Greater(t, totalTools, 0, "AI should interact with comments through replies or reactions")

	// Verify comment tool uses
	for _, commentToolUse := range toolUses[commentTool] {
		if input, ok := commentToolUse.Input.(map[string]interface{}); ok {
			body, hasBody := input["body"]
			commentType, hasType := input["comment_type"]

			require.True(t, hasBody, "post_comment should include body")
			require.True(t, hasType, "post_comment should include comment_type")

			bodyStr := fmt.Sprintf("%v", body)
			typeStr := fmt.Sprintf("%v", commentType)

			require.NotEmpty(t, bodyStr, "comment body should not be empty")
			require.Contains(t, []string{"issue", "pr", "review"}, typeStr, "comment_type should be valid")
		}
	}

	// Verify reaction tool uses
	for _, reactToolUse := range toolUses[reactTool] {
		if input, ok := reactToolUse.Input.(map[string]interface{}); ok {
			reaction, hasReaction := input["reaction"]
			commentID, hasCommentID := input["comment_id"]

			require.True(t, hasReaction, "add_reaction should include reaction")
			require.True(t, hasCommentID, "add_reaction should include comment_id")

			reactionStr := fmt.Sprintf("%v", reaction)
			require.Contains(t, []string{"+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"},
				reactionStr, "reaction should be valid emoji")
		}
	}

	return nil
}

// Helper functions for tool analysis

// requireToolUse requires that exactly one tool use of the given name exists and returns it
func requireToolUse(t *testing.T, response *anthropic.Message, toolName string) *anthropic.ToolUseBlock {
	t.Helper()

	var toolUse *anthropic.ToolUseBlock
	count := 0

	for _, content := range response.Content {
		if tu := content.ToolUse; tu != nil && tu.Name == toolName {
			toolUse = tu
			count++
		}
	}

	require.Equal(t, 1, count, "Expected exactly 1 use of tool %s, found %d", toolName, count)
	require.NotNil(t, toolUse, "Tool use should not be nil")

	return toolUse
}

// requireToolUses requires specific counts of different tools and returns them organized by tool name
func requireToolUses(t *testing.T, response *anthropic.Message, expectedCounts map[string]int) map[string][]*anthropic.ToolUseBlock {
	t.Helper()

	actualCounts := make(map[string]int)
	toolUses := make(map[string][]*anthropic.ToolUseBlock)

	// Count and collect all tool uses
	for _, content := range response.Content {
		if tu := content.ToolUse; tu != nil {
			actualCounts[tu.Name]++
			toolUses[tu.Name] = append(toolUses[tu.Name], tu)
		}
	}

	// Verify counts match expectations
	for toolName, expectedCount := range expectedCounts {
		actualCount := actualCounts[toolName]
		if expectedCount == -1 {
			// Allow any positive number
			require.Greater(t, actualCount, 0,
				"Expected at least 1 use of tool %s, found %d", toolName, actualCount)
		} else {
			require.Equal(t, expectedCount, actualCount,
				"Expected %d uses of tool %s, found %d", expectedCount, toolName, actualCount)
		}
	}

	// Verify no unexpected tools were used
	for toolName, actualCount := range actualCounts {
		if expectedCount, exists := expectedCounts[toolName]; !exists {
			require.Equal(t, 0, actualCount,
				"Unexpected tool %s used %d times", toolName, actualCount)
		}
	}

	return toolUses
}

// Helper to create a basic task for testing
func createTestTask(issueNumber int, issueTitle, issueBody string) task.Task {
	return task.Task{
		Issue: task.GithubIssue{
			Owner:  "test",
			Repo:   "repository",
			Number: issueNumber,
			Title:  issueTitle,
			Body:   issueBody,
		},
		Repository: &github.Repository{
			FullName: github.String("test/repository"),
		},
		CodebaseInfo: &task.CodebaseInfo{
			MainLanguage: "Go",
		},
		StyleGuide: &task.StyleGuide{
			Guides: map[string]string{
				"STYLE_GUIDE.md": "Use Go style conventions",
			},
		},
		TargetBranch: "main",
		SourceBranch: "issue-" + fmt.Sprintf("%d", issueNumber),
		ValidationResult: validator.ValidationResult{
			Succeeded: true,
			Details:   "",
		},
	}
}
