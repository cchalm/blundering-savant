//go:build e2e

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/test/e2e/testutil"
)

// TestFullDevelopmentWorkflow tests a complete development workflow scenario
func TestFullDevelopmentWorkflow(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	// This is an expensive test, run fewer iterations
	config := harness.Config()
	if config.Iterations > 2 {
		config.Iterations = 2
	}

	testName := "full_development_workflow"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			return runFullWorkflow(ctx, harness)
		})
	})
}

// TestIssueCommentInteraction tests realistic GitHub issue interaction
func TestIssueCommentInteraction(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "issue_comment_interaction"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			return runIssueCommentScenario(ctx, harness)
		})
	})
}

// TestPullRequestReviewCycle tests PR review and response workflow
func TestPullRequestReviewCycle(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "pull_request_review_cycle"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			return runPRReviewScenario(ctx, harness)
		})
	})
}

// runFullWorkflow simulates a complete development workflow
func runFullWorkflow(ctx context.Context, harness *testutil.TestHarness) error {
	// Build system prompt
	systemPrompt, err := ai.BuildSystemPrompt("Blundering Savant", "testbot")
	if err != nil {
		return fmt.Errorf("failed to build system prompt: %w", err)
	}

	conversation := harness.CreateConversation(systemPrompt)
	workspace := testutil.NewMockWorkspace()

	// Set up some existing files in the workspace
	workspace.SetFile("main.go", `package main

import "fmt"

func main() {
	fmt.Println("Hello World")
}`)

	workspace.SetFile("README.md", "# Test Project\n\nA simple test project.")

	// Create a mock tool context
	mockTask := task.Task{
		Issue: task.GithubIssue{
			Owner:  "testowner",
			Repo:   "testrepo",
			Number: 123,
		},
		HasUnpublishedChanges: false,
		ValidationResult: struct {
			Succeeded bool
			Details   string
		}{
			Succeeded: true,
			Details:   "All checks passed",
		},
	}

	toolCtx := &bot.ToolContext{
		Workspace:    workspace,
		Task:         mockTask,
		GithubClient: harness.GitHubClient(),
	}

	// Simulate getting a task to implement user authentication
	initialPrompt := `Repository: testowner/testrepo

## Style Guides
Follow Go best practices and clean code principles.

## README excerpt
# Test Project
A simple test project for testing the bot.

## Issue
Issue #123: Implement user authentication system

### Description
We need to add user authentication to our application. This should include:

1. User registration functionality
2. Login/logout endpoints
3. Password hashing and validation  
4. Session management
5. Middleware for protected routes

Please implement a complete authentication system following Go best practices.

## Workspace Status
There are no unpublished changes in the workspace.

### Latest validation results
Status: PASSED`

	response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(initialPrompt))
	if err != nil {
		return fmt.Errorf("failed to send initial prompt: %w", err)
	}

	// Process the workflow through multiple iterations
	maxIterations := 10
	for i := 0; i < maxIterations; i++ {
		if response.StopReason == anthropic.StopReasonEndTurn {
			break
		}

		if response.StopReason == anthropic.StopReasonToolUse {
			// Process tool uses
			toolResults := []anthropic.ContentBlockParamUnion{}

			for _, content := range response.Content {
				if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					result, err := processToolUseForTesting(ctx, toolBlock, toolCtx)
					if err != nil {
						return fmt.Errorf("failed to process tool use %s: %w", toolBlock.Name, err)
					}
					toolResults = append(toolResults, anthropic.ContentBlockParamUnion{OfToolResult: result})
				}
			}

			if len(toolResults) > 0 {
				response, err = conversation.SendMessage(ctx, toolResults...)
				if err != nil {
					return fmt.Errorf("failed to send tool results: %w", err)
				}
			}
		} else {
			return fmt.Errorf("unexpected stop reason: %v", response.StopReason)
		}
	}

	if response.StopReason != anthropic.StopReasonEndTurn {
		return fmt.Errorf("workflow did not complete within %d iterations", maxIterations)
	}

	// Analyze the final state
	return analyzeWorkflowCompletion(workspace, toolCtx)
}

// runIssueCommentScenario simulates responding to GitHub issue comments
func runIssueCommentScenario(ctx context.Context, harness *testutil.TestHarness) error {
	systemPrompt, err := ai.BuildSystemPrompt("Blundering Savant", "testbot")
	if err != nil {
		return fmt.Errorf("failed to build system prompt: %w", err)
	}

	conversation := harness.CreateConversation(systemPrompt)

	scenario := `You are working on Issue #456 and received these comments:

Comment ID 789 from @alice (2 hours ago): "I think the approach looks good, but could you add some unit tests for the validation logic?"

Comment ID 790 from @bob (1 hour ago): "There's a potential edge case when the input is null. Have you considered that?"

Comment ID 791 from @charlie (30 minutes ago): "Great work! This will solve the issue we've been having. 👍"

Please respond to these comments appropriately. Use parallel tool calls for efficiency.`

	response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(scenario))
	if err != nil {
		return fmt.Errorf("failed to send scenario: %w", err)
	}

	// Analyze response for appropriate comment handling
	return analyzeCommentHandling(response)
}

// runPRReviewScenario simulates a PR review workflow
func runPRReviewScenario(ctx context.Context, harness *testutil.TestHarness) error {
	systemPrompt, err := ai.BuildSystemPrompt("Blundering Savant", "testbot")
	if err != nil {
		return fmt.Errorf("failed to build system prompt: %w", err)
	}

	conversation := harness.CreateConversation(systemPrompt)

	scenario := `You have a pull request open for Issue #789. You received this review feedback:

Review from @senior_dev:
- "The implementation looks solid overall"
- "Please add error handling to the database connection logic on line 45"
- "Consider extracting the validation logic into a separate function"
- "Add documentation for the exported functions"

You should:
1. Address the review feedback by updating the code
2. Validate your changes  
3. Respond to the review comments
4. Update the PR description if needed

Handle this efficiently with appropriate tool usage.`

	response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(scenario))
	if err != nil {
		return fmt.Errorf("failed to send scenario: %w", err)
	}

	// Process any tool calls
	maxIterations := 5
	for i := 0; i < maxIterations; i++ {
		if response.StopReason == anthropic.StopReasonEndTurn {
			break
		}

		if response.StopReason == anthropic.StopReasonToolUse {
			// Create mock tool results
			toolResults := []anthropic.ContentBlockParamUnion{}

			for _, content := range response.Content {
				if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					result := &anthropic.ToolResultBlockParam{
						ToolUseID: toolBlock.ID,
						Type:      "tool_result",
						Content:   []anthropic.ContentBlockParamUnion{anthropic.NewTextBlockParam("Tool executed successfully")},
					}
					toolResults = append(toolResults, anthropic.ContentBlockParamUnion{OfToolResult: result})
				}
			}

			if len(toolResults) > 0 {
				response, err = conversation.SendMessage(ctx, toolResults...)
				if err != nil {
					return fmt.Errorf("failed to send tool results: %w", err)
				}
			}
		}
	}

	return analyzePRReviewHandling(response)
}

// Helper functions for processing and analysis

func processToolUseForTesting(ctx context.Context, toolBlock anthropic.ToolUseBlock, toolCtx *bot.ToolContext) (*anthropic.ToolResultBlockParam, error) {
	// Mock tool execution for testing
	var content string

	switch toolBlock.Name {
	case "str_replace_based_edit_tool":
		var input struct {
			Command  string `json:"command"`
			Path     string `json:"path"`
			FileText string `json:"file_text,omitempty"`
		}
		
		if err := json.Unmarshal(toolBlock.Input, &input); err != nil {
			content = fmt.Sprintf("Error parsing input: %v", err)
		} else {
			switch input.Command {
			case "create":
				toolCtx.Workspace.Write(ctx, input.Path, input.FileText)
				content = fmt.Sprintf("Created file %s", input.Path)
			case "view":
				if fileContent, err := toolCtx.Workspace.Read(ctx, input.Path); err != nil {
					content = fmt.Sprintf("File not found: %s", input.Path)
				} else {
					content = fmt.Sprintf("File contents of %s:\n%s", input.Path, fileContent)
				}
			default:
				content = fmt.Sprintf("Executed %s command on %s", input.Command, input.Path)
			}
		}

	case "validate_changes":
		content = "Validation succeeded - all tests pass"

	case "publish_changes_for_review":
		content = "Changes published for review successfully"

	case "post_comment":
		var input struct {
			Body string `json:"body"`
		}
		
		if err := json.Unmarshal(toolBlock.Input, &input); err != nil {
			content = fmt.Sprintf("Error parsing comment: %v", err)
		} else {
			content = fmt.Sprintf("Posted comment: %s", input.Body[:min(50, len(input.Body))])
		}

	case "add_reaction":
		var input struct {
			Reaction string `json:"reaction"`
		}
		
		if err := json.Unmarshal(toolBlock.Input, &input); err != nil {
			content = "Error parsing reaction"
		} else {
			content = fmt.Sprintf("Added reaction: %s", input.Reaction)
		}

	case "report_limitation":
		var input struct {
			ToolNeeded string `json:"tool_needed"`
			Reason     string `json:"reason"`
		}
		
		if err := json.Unmarshal(toolBlock.Input, &input); err != nil {
			content = "Error parsing limitation report"
		} else {
			content = fmt.Sprintf("Reported limitation: need %s because %s", input.ToolNeeded, input.Reason)
		}

	default:
		content = fmt.Sprintf("Unknown tool: %s", toolBlock.Name)
	}

	return &anthropic.ToolResultBlockParam{
		ToolUseID: toolBlock.ID,
		Type:      "tool_result",
		Content:   []anthropic.ContentBlockParamUnion{anthropic.NewTextBlockParam(content)},
	}, nil
}

func analyzeWorkflowCompletion(workspace *testutil.MockWorkspace, toolCtx *bot.ToolContext) error {
	files := workspace.GetFiles()

	// Should have created authentication-related files
	authFiles := []string{"auth.go", "user.go", "middleware.go", "handlers.go"}
	createdAuthFiles := 0

	for _, authFile := range authFiles {
		if _, exists := files[authFile]; exists {
			createdAuthFiles++
		}
	}

	if createdAuthFiles == 0 {
		return fmt.Errorf("no authentication files were created (expected some of: %v)", authFiles)
	}

	// Check if files contain authentication-related content
	for path, content := range files {
		if strings.Contains(path, "auth") || strings.Contains(path, "user") {
			lowerContent := strings.ToLower(content)
			authTerms := []string{"password", "hash", "login", "session", "authenticate"}
			
			hasAuthContent := false
			for _, term := range authTerms {
				if strings.Contains(lowerContent, term) {
					hasAuthContent = true
					break
				}
			}
			
			if !hasAuthContent {
				return fmt.Errorf("authentication file %s lacks authentication-related content", path)
			}
		}
	}

	return nil
}

func analyzeCommentHandling(response *anthropic.Message) error {
	commentTools := 0
	reactionTools := 0

	for _, content := range response.Content {
		if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			switch toolBlock.Name {
			case "post_comment":
				commentTools++
			case "add_reaction":
				reactionTools++
			}
		}
	}

	// Should respond to multiple comments
	if commentTools < 2 {
		return fmt.Errorf("expected multiple comment responses, got %d", commentTools)
	}

	// Should add reactions to positive feedback
	if reactionTools == 0 {
		return fmt.Errorf("expected some reactions to positive feedback")
	}

	return nil
}

func analyzePRReviewHandling(response *anthropic.Message) error {
	hasFileEdit := false
	hasValidation := false
	hasComment := false

	for _, content := range response.Content {
		if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			switch toolBlock.Name {
			case "str_replace_based_edit_tool":
				hasFileEdit = true
			case "validate_changes":
				hasValidation = true
			case "post_comment":
				hasComment = true
			}
		}
	}

	if !hasFileEdit {
		return fmt.Errorf("expected file edits to address review feedback")
	}

	if !hasValidation {
		return fmt.Errorf("expected validation after making changes")
	}

	if !hasComment {
		return fmt.Errorf("expected response to reviewer comments")
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}