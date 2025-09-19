//go:build e2e

package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/internal/ai"
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

			// Step 1: Build a complex conversation history
			initialMessage := `Repository: test/web-app

Main Language: JavaScript

## Issue

Issue #456: Implement user authentication system

### Description

> We need to implement a complete user authentication system with registration, login, logout, and password reset functionality. The system should use JWT tokens and integrate with our existing user database.

## Your Task

Please analyze the requirements and create a plan for implementing the authentication system. Consider security best practices and scalability.`

			// Send initial message to establish context
			response1, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(initialMessage))
			if err != nil {
				return fmt.Errorf("failed to send initial message: %w", err)
			}

			// Add several more exchanges to build conversation history
			followup1 := `Great analysis! Now please start by implementing the user registration endpoint. Make sure to include proper input validation and password hashing.`

			response2, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(followup1))
			if err != nil {
				return fmt.Errorf("failed to send followup1: %w", err)
			}

			followup2 := `Thanks! Now let's add the login endpoint that generates JWT tokens. Please also implement middleware for token verification.`

			response3, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(followup2))
			if err != nil {
				return fmt.Errorf("failed to send followup2: %w", err)
			}

			// Step 2: Force summarization
			err = conversation.Summarize(ctx)
			if err != nil {
				return fmt.Errorf("failed to summarize conversation: %w", err)
			}

			// Get the summarized history
			summarizedHistory := conversation.History()

			// Step 3: Resume conversation with summarized history
			resumedConv, err := harness.ResumeConversation(summarizedHistory)
			if err != nil {
				return fmt.Errorf("failed to resume conversation: %w", err)
			}

			// Step 4: Send a follow-up that requires understanding of previous context
			contextualFollowup := `Now that we have registration and login working, please implement the password reset functionality that we planned earlier. Make sure it integrates properly with the JWT system we set up.`

			finalResponse, err := resumedConv.SendMessage(ctx, anthropic.NewTextBlock(contextualFollowup))
			if err != nil {
				return fmt.Errorf("failed to send contextual followup: %w", err)
			}

			// Step 5: Verify AI understands the context from its summary
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

			// Create a conversation with specific technical decisions
			initialMessage := `Repository: test/api-service

Main Language: Go

## Issue

Issue #789: Optimize database queries for user search

### Description

> The user search endpoint is slow. We need to optimize the database queries and implement caching.

## Your Task

Analyze the performance issues and propose optimizations. Focus on database indexing and Redis caching integration.`

			response1, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(initialMessage))
			if err != nil {
				return fmt.Errorf("failed to send initial message: %w", err)
			}

			// Add technical details that should be preserved in summary
			technicalFollowup := `Perfect analysis! Let's implement the solution with these specific requirements:
1. Create a composite index on (username, email, created_at)
2. Use Redis with 5-minute TTL for search results
3. Implement cache invalidation on user updates
4. Add database connection pooling with max 20 connections

Please start with the database schema changes.`

			response2, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(technicalFollowup))
			if err != nil {
				return fmt.Errorf("failed to send technical followup: %w", err)
			}

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
