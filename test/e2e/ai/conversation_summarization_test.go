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

// TestConversationSummarization tests that AI can understand its own summaries
func TestConversationSummarization(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "conversation_summarization_understanding"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			// Step 1: Manually construct a complex conversation history instead of using AI
			testTask := createTestTask(456, "Implement user authentication system",
				"We need to implement a complete user authentication system with registration, login, logout, and password reset functionality. The system should use JWT tokens and integrate with our existing user database.")
			testTask.CodebaseInfo.MainLanguage = "JavaScript"

			// Build initial prompt
			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			// Manually construct conversation history showing development progress
			initialMsg := anthropic.Message{
				Role: anthropic.MessageRoleUser,
				Content: []anthropic.MessageContent{{Text: taskContent}},
			}
			conversation.AddMessage(initialMsg)

			planResponse := anthropic.Message{
				Role: anthropic.MessageRoleAssistant,
				Content: []anthropic.MessageContent{
					{Text: "I'll implement the authentication system in phases:\n1. User registration with password hashing\n2. Login endpoint with JWT generation\n3. Token verification middleware\n4. Password reset functionality"},
				},
			}
			conversation.AddMessage(planResponse)

			userFollowup1 := anthropic.Message{
				Role: anthropic.MessageRoleUser,
				Content: []anthropic.MessageContent{{Text: "Great plan! Please start with the registration endpoint."}},
			}
			conversation.AddMessage(userFollowup1)

			registrationResponse := anthropic.Message{
				Role: anthropic.MessageRoleAssistant,
				Content: []anthropic.MessageContent{
					{Text: "I'll implement the user registration endpoint with bcrypt password hashing and input validation."},
					{
						ToolUse: &anthropic.ToolUseBlock{
							ID:   "reg-1",
							Name: "str_replace_based_edit_tool",
							Input: map[string]interface{}{
								"command": "create",
								"path":    "auth/registration.js", 
								"file_text": "const bcrypt = require('bcrypt');\nconst User = require('../models/User');\n\nasync function registerUser(req, res) {\n  const { email, password } = req.body;\n  const hashedPassword = await bcrypt.hash(password, 12);\n  const user = await User.create({ email, password: hashedPassword });\n  res.json({ success: true, userId: user.id });\n}\n\nmodule.exports = { registerUser };",
							},
						},
					},
				},
			}
			conversation.AddMessage(registrationResponse)

			userFollowup2 := anthropic.Message{
				Role: anthropic.MessageRoleUser,
				Content: []anthropic.MessageContent{{Text: "Excellent! Now implement the login endpoint with JWT tokens."}},
			}
			conversation.AddMessage(userFollowup2)

			loginResponse := anthropic.Message{
				Role: anthropic.MessageRoleAssistant,
				Content: []anthropic.MessageContent{
					{Text: "I'll create the login endpoint that generates JWT tokens for authentication."},
					{
						ToolUse: &anthropic.ToolUseBlock{
							ID:   "login-1", 
							Name: "str_replace_based_edit_tool",
							Input: map[string]interface{}{
								"command": "create",
								"path":    "auth/login.js",
								"file_text": "const bcrypt = require('bcrypt');\nconst jwt = require('jsonwebtoken');\nconst User = require('../models/User');\n\nasync function loginUser(req, res) {\n  const { email, password } = req.body;\n  const user = await User.findOne({ email });\n  if (!user || !await bcrypt.compare(password, user.password)) {\n    return res.status(401).json({ error: 'Invalid credentials' });\n  }\n  const token = jwt.sign({ userId: user.id }, process.env.JWT_SECRET, { expiresIn: '24h' });\n  res.json({ success: true, token });\n}\n\nmodule.exports = { loginUser };",
							},
						},
					},
				},
			}
			conversation.AddMessage(loginResponse)

			// Step 2: Summarize the conversation
			err = conversation.Summarize(ctx)
			if err != nil {
				return fmt.Errorf("failed to summarize conversation: %w", err)
			}

			// Step 3: Send a follow-up that requires understanding of the summarized context
			contextualFollowup := "Now that we have registration and login working, please implement the password reset functionality that we planned earlier. Make sure it integrates properly with the JWT system we set up."

			finalResponse, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(contextualFollowup))
			if err != nil {
				return fmt.Errorf("failed to send contextual followup: %w", err)
			}

			// Step 4: Verify AI understands the context from its summary
			return analyzeContextualUnderstanding(t, finalResponse, []string{
				"password reset",
				"JWT",
				"authentication", 
				"registration",
				"login",
			})
		})
	})
}

// TestSummaryAccuracy tests that summaries preserve important context
func TestSummaryAccuracy(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "summary_accuracy"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			conversation, err := harness.CreateConversationWithSystemPrompt("Blundering Savant", "blunderingsavant")
			require.NoError(t, err)

			// Manually construct a conversation with specific technical decisions
			testTask := createTestTask(789, "Optimize database queries for user search",
				"The user search endpoint is slow. We need to optimize the database queries and implement caching.\n\n"+
				"Analyze the performance issues and propose optimizations. Focus on database indexing and Redis caching integration.")

			_, taskContent, err := ai.BuildPrompt(testTask)
			require.NoError(t, err)

			// Manually build conversation history with specific technical details
			initialMsg := anthropic.Message{
				Role: anthropic.MessageRoleUser,
				Content: []anthropic.MessageContent{{Text: taskContent}},
			}
			conversation.AddMessage(initialMsg)

			analysisResponse := anthropic.Message{
				Role: anthropic.MessageRoleAssistant,
				Content: []anthropic.MessageContent{
					{Text: "I'll analyze the performance issues and implement optimizations focusing on database indexing and Redis caching."},
				},
			}
			conversation.AddMessage(analysisResponse)

			technicalRequirements := anthropic.Message{
				Role: anthropic.MessageRoleUser,
				Content: []anthropic.MessageContent{
					{Text: "Perfect analysis! Let's implement the solution with these specific requirements:\n" +
						"1. Create a composite index on (username, email, created_at)\n" +
						"2. Use Redis with 5-minute TTL for search results\n" +
						"3. Implement cache invalidation on user updates\n" +
						"4. Add database connection pooling with max 20 connections\n\n" +
						"Please start with the database schema changes."},
				},
			}
			conversation.AddMessage(technicalRequirements)

			implementationResponse := anthropic.Message{
				Role: anthropic.MessageRoleAssistant,
				Content: []anthropic.MessageContent{
					{Text: "I'll implement the database optimization with the composite index on (username, email, created_at) and set up Redis caching with 5-minute TTL. I'll also configure connection pooling with a maximum of 20 connections for optimal performance."},
				},
			}
			conversation.AddMessage(implementationResponse)

			// Summarize the conversation
			err = conversation.Summarize(ctx)
			if err != nil {
				return fmt.Errorf("failed to summarize conversation: %w", err)
			}

			// Get the summarized history
			summarizedHistory := conversation.History()

			// Verify summary contains key technical details
			return analyzeSummaryCompleteness(t, summarizedHistory, []string{
				"composite index",
				"username, email, created_at",
				"Redis", 
				"5-minute TTL",
				"cache invalidation",
				"connection pooling",
				"20 connections",
				"user search",
				"performance",
			})
		})
	})
}

// Helper function to analyze if AI understands context from summary
func analyzeContextualUnderstanding(t *testing.T, response *anthropic.Message, expectedContextKeywords []string) error {
	t.Helper()

	responseText := ""
	for _, content := range response.Content {
		if content.Text != "" {
			responseText += content.Text + " "
		}
	}

	responseText = strings.ToLower(responseText)

	foundKeywords := 0
	for _, keyword := range expectedContextKeywords {
		if strings.Contains(responseText, strings.ToLower(keyword)) {
			foundKeywords++
		}
	}

	// Require at least 60% of keywords to be present, indicating contextual understanding
	minRequired := (len(expectedContextKeywords) * 3) / 5 // 60%
	require.GreaterOrEqual(t, foundKeywords, minRequired,
		"AI should demonstrate understanding of previous context, found %d/%d keywords in response",
		foundKeywords, len(expectedContextKeywords))

	return nil
}

// Helper function to analyze summary completeness
func analyzeSummaryCompleteness(t *testing.T, history ai.ConversationHistory, expectedTechnicalDetails []string) error {
	t.Helper()

	// Get the summary content from the conversation history
	summaryText := ""
	for _, message := range history.Messages {
		if message.Role == anthropic.MessageRoleAssistant {
			for _, content := range message.Content {
				if content.Text != "" {
					summaryText += content.Text + " "
				}
			}
		}
	}

	summaryText = strings.ToLower(summaryText)

	foundDetails := 0
	missingDetails := []string{}

	for _, detail := range expectedTechnicalDetails {
		if strings.Contains(summaryText, strings.ToLower(detail)) {
			foundDetails++
		} else {
			missingDetails = append(missingDetails, detail)
		}
	}

	// Require at least 70% of technical details to be preserved
	minRequired := (len(expectedTechnicalDetails) * 7) / 10 // 70%
	require.GreaterOrEqual(t, foundDetails, minRequired,
		"Summary should preserve important technical details, found %d/%d details. Missing: %v",
		foundDetails, len(expectedTechnicalDetails), missingDetails)

	return nil
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
