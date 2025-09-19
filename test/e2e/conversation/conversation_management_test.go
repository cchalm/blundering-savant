//go:build e2e

package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/test/e2e/testutil"
)

// TestConversationResumption tests that conversations can be resumed from history
func TestConversationResumption(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "conversation_resumption"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			// Create initial conversation
			systemPrompt := "You are Blundering Savant, a software developer. Remember context across conversation resumptions."

			conversation := harness.CreateConversation(systemPrompt)

			// Send initial message establishing context
			initialPrompt := "I'm working on a project to create a user authentication system. I need to implement login validation and password hashing. Please help me start by creating a basic user model structure."

			response1, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(initialPrompt))
			if err != nil {
				return fmt.Errorf("failed to send initial message: %w", err)
			}

			// Get the conversation history
			history := conversation.History()

			// Resume the conversation
			resumedConv, err := harness.ResumeConversation(history)
			if err != nil {
				return fmt.Errorf("failed to resume conversation: %w", err)
			}

			// Send a follow-up that requires context from the original conversation
			followupPrompt := "Great! Now based on the user model we discussed, please add email verification functionality to it."

			response2, err := resumedConv.SendMessage(ctx, anthropic.NewTextBlock(followupPrompt))
			if err != nil {
				return fmt.Errorf("failed to send followup message: %w", err)
			}

			// Verify that the resumed conversation maintains context
			return analyzeContextRetention(response1, response2)
		})
	})
}

// TestConversationSummarization tests the summarization functionality
func TestConversationSummarization(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	// Skip if we don't have enough iterations to make summarization worthwhile
	if harness.Config().Iterations < 2 {
		t.Skip("Skipping summarization test with less than 2 iterations due to cost")
	}

	testName := "conversation_summarization"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a helpful software developer."

			// Create a conversation with a low token limit to trigger summarization
			conversation := ai.NewConversation(
				harness.AnthropicClient(),
				harness.Config().Model,
				harness.Config().MaxTokens,
				harness.ToolRegistry().GetAllToolParams(),
				systemPrompt,
			)

			// Force token limit lower for testing
			// Note: We can't directly manipulate internal conversation state in real tests
			// This test validates that the conversation can handle multiple turns

			// Send multiple messages to build up conversation history
			messages := []string{
				"Help me create a web application with user authentication",
				"Add password hashing to the login system",
				"Implement session management",
				"Add password reset functionality",
				"Create user profile management",
			}

			var lastResponse *anthropic.Message
			for i, msg := range messages {
				response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(msg))
				if err != nil {
					return fmt.Errorf("failed to send message %d: %w", i, err)
				}
				lastResponse = response
			}

			// Check if summarization was triggered (this is hard to test without access to internals)
			// For now, just verify the conversation is still functional
			finalResponse, err := conversation.SendMessage(ctx, anthropic.NewTextBlock("Summarize what we've accomplished so far"))
			if err != nil {
				return fmt.Errorf("failed to send final message: %w", err)
			}

			// Verify the AI can still recall the context of our work
			return analyzeSummaryQuality(finalResponse, messages)
		})
	})
}

// TestContextualToolUsage tests that tools are used appropriately based on conversation context
func TestContextualToolUsage(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "contextual_tool_usage"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer working on a GitHub repository."

			conversation := harness.CreateConversation(systemPrompt)

			// Build up context about working on a specific feature
			contextPrompt := `I'm working on Issue #42: "Add user authentication system". I've been making changes to implement login functionality. Here's what I need to do:

1. Create authentication middleware
2. Add login/logout endpoints  
3. Validate the changes
4. Publish for review

Please help me start with the authentication middleware.`

			response1, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(contextPrompt))
			if err != nil {
				return fmt.Errorf("failed to send context message: %w", err)
			}

			// The AI should use file tools to create the middleware
			hasFileTools := false
			for _, content := range response1.Content {
				if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					if toolBlock.Name == "str_replace_based_edit_tool" {
						hasFileTools = true
						break
					}
				}
			}

			if !hasFileTools {
				return fmt.Errorf("expected AI to use file editing tools for creating middleware")
			}

			// Continue the conversation asking to validate and publish
			nextPrompt := "Perfect! Now that we have the middleware, let's validate our changes and get them ready for review."

			response2, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(nextPrompt))
			if err != nil {
				return fmt.Errorf("failed to send validation request: %w", err)
			}

			// Should use validation and publication tools
			hasValidation := false
			for _, content := range response2.Content {
				if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
					if toolBlock.Name == "validate_changes" {
						hasValidation = true
						break
					}
				}
			}

			if !hasValidation {
				return fmt.Errorf("expected AI to use validate_changes tool when asked to validate")
			}

			return nil
		})
	})
}

// TestConversationCoherence tests that conversation maintains logical flow
func TestConversationCoherence(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "conversation_coherence"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, maintaining coherent conversations about software development."

			conversation := harness.CreateConversation(systemPrompt)

			// Start a logical sequence
			step1 := "I need to fix a bug in the payment processing module. The issue is that credit card validation is failing for valid cards."

			response1, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(step1))
			if err != nil {
				return fmt.Errorf("failed to send step 1: %w", err)
			}

			step2 := "Good analysis! Now let's implement the fix you suggested."

			response2, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(step2))
			if err != nil {
				return fmt.Errorf("failed to send step 2: %w", err)
			}

			step3 := "The fix looks good. Can you also add some tests to prevent this regression?"

			response3, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(step3))
			if err != nil {
				return fmt.Errorf("failed to send step 3: %w", err)
			}

			// Analyze conversation for coherence
			return analyzeConversationCoherence([]*anthropic.Message{response1, response2, response3})
		})
	})
}

// Helper functions for analysis

func analyzeContextRetention(initial, followup *anthropic.Message) error {
	// Check that the followup response references or builds upon the initial response
	initialHasContent := false
	followupHasContent := false
	
	for _, content := range initial.Content {
		if _, ok := content.AsAny().(anthropic.TextBlock); ok {
			initialHasContent = true
			break
		}
	}
	
	for _, content := range followup.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			followupHasContent = true
			text := strings.ToLower(textBlock.Text)
			
			// Look for context references
			contextWords := []string{"user", "model", "authentication", "based on", "we discussed", "earlier"}
			contextFound := false
			for _, word := range contextWords {
				if strings.Contains(text, word) {
					contextFound = true
					break
				}
			}
			
			if !contextFound {
				return fmt.Errorf("followup response doesn't appear to reference previous context")
			}
		}
	}
	
	if !initialHasContent {
		return fmt.Errorf("initial response has no text content")
	}
	
	if !followupHasContent {
		return fmt.Errorf("followup response has no text content")
	}
	
	return nil
}

func analyzeSummaryQuality(response *anthropic.Message, originalMessages []string) error {
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			text := strings.ToLower(textBlock.Text)
			
			// Check that summary mentions key concepts from original messages
			keyTerms := []string{"authentication", "password", "session", "profile", "user"}
			foundTerms := 0
			
			for _, term := range keyTerms {
				if strings.Contains(text, term) {
					foundTerms++
				}
			}
			
			if foundTerms < 2 {
				return fmt.Errorf("summary doesn't contain enough context from original conversation (found %d/%d key terms)", foundTerms, len(keyTerms))
			}
			
			// Summary should be substantive
			if len(strings.Fields(text)) < 20 {
				return fmt.Errorf("summary appears too brief to be comprehensive")
			}
			
			return nil
		}
	}
	
	return fmt.Errorf("summary response contains no text content")
}

func analyzeConversationCoherence(responses []*anthropic.Message) error {
	if len(responses) < 2 {
		return fmt.Errorf("need at least 2 responses to analyze coherence")
	}
	
	// Extract text from each response
	var texts []string
	for _, response := range responses {
		for _, content := range response.Content {
			if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
				texts = append(texts, strings.ToLower(textBlock.Text))
				break // Use first text block
			}
		}
	}
	
	if len(texts) != len(responses) {
		return fmt.Errorf("not all responses contain text content")
	}
	
	// Check for topic consistency - all should mention payment/credit/validation
	paymentTerms := []string{"payment", "credit", "card", "validation", "fix", "bug"}
	
	for i, text := range texts {
		hasPaymentContext := false
		for _, term := range paymentTerms {
			if strings.Contains(text, term) {
				hasPaymentContext = true
				break
			}
		}
		
		if !hasPaymentContext {
			return fmt.Errorf("response %d lacks coherence with payment processing context", i+1)
		}
	}
	
	// Later responses should show progression (mentioning implementation, tests, etc.)
	if len(texts) >= 3 {
		finalText := texts[len(texts)-1]
		progressTerms := []string{"test", "regression", "prevent", "coverage"}
		hasProgression := false
		
		for _, term := range progressTerms {
			if strings.Contains(finalText, term) {
				hasProgression = true
				break
			}
		}
		
		if !hasProgression {
			return fmt.Errorf("final response doesn't show logical progression to testing phase")
		}
	}
	
	return nil
}