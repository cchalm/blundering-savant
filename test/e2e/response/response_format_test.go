//go:build e2e

package response

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/cchalm/blundering-savant/test/e2e/testutil"
)

// TestResponseStructure tests that AI responses have proper structure
func TestResponseStructure(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "response_structure"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer who provides clear, structured responses."

			conversation := harness.CreateConversation(systemPrompt)

			prompt := "I need help implementing a REST API for user management. Please explain the approach and create the basic structure."

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeResponseStructure(response)
		})
	})
}

// TestExplanationQuality tests that AI provides good explanations
func TestExplanationQuality(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "explanation_quality"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer who explains decisions clearly."

			conversation := harness.CreateConversation(systemPrompt)

			prompt := "Why should I choose PostgreSQL over MySQL for my new web application? Please explain your reasoning."

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeExplanationQuality(response)
		})
	})
}

// TestCodeQuality tests the quality of generated code
func TestCodeQuality(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "code_quality"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer who writes clean, well-documented code following best practices."

			conversation := harness.CreateConversation(systemPrompt)

			prompt := "Create a Go function that validates email addresses and includes proper error handling and documentation."

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeCodeQuality(response)
		})
	})
}

// TestProfessionalTone tests that responses maintain a professional tone
func TestProfessionalTone(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "professional_tone"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a professional software developer collaborating on GitHub."

			conversation := harness.CreateConversation(systemPrompt)

			// Scenario that might trigger unprofessional responses
			prompt := "This code is terrible and full of bugs. The previous developer clearly didn't know what they were doing. Fix this mess!"

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeProfessionalTone(response)
		})
	})
}

// TestClarifyingQuestions tests that AI asks appropriate clarifying questions
func TestClarifyingQuestions(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "clarifying_questions"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer who asks clarifying questions when requirements are unclear."

			conversation := harness.CreateConversation(systemPrompt)

			// Intentionally vague prompt
			prompt := "Make the app better."

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeClarifyingQuestions(response)
		})
	})
}

// TestErrorHandlingAdvice tests AI's guidance on error handling
func TestErrorHandlingAdvice(t *testing.T) {
	harness := testutil.NewTestHarness(t)

	testName := "error_handling_advice"
	harness.RunIterations(testName, func(iteration int) error {
		return harness.WithTimeout(func(ctx context.Context) error {
			systemPrompt := "You are Blundering Savant, a software developer who emphasizes proper error handling."

			conversation := harness.CreateConversation(systemPrompt)

			prompt := "I'm getting an error 'connection refused' when trying to connect to the database. What should I do?"

			response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
			if err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}

			return analyzeErrorHandlingAdvice(response)
		})
	})
}

// Helper functions for analysis

func analyzeResponseStructure(response *anthropic.Message) error {
	hasText := false
	textLength := 0

	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			hasText = true
			textLength += len(textBlock.Text)

			text := textBlock.Text
			
			// Check for basic structure indicators
			structureIndicators := 0
			indicators := []string{"1.", "2.", "3.", "-", "*", "##", "###", "```"}
			
			for _, indicator := range indicators {
				if strings.Contains(text, indicator) {
					structureIndicators++
				}
			}

			if structureIndicators < 2 {
				return fmt.Errorf("response lacks structured formatting (found %d indicators)", structureIndicators)
			}
		}
	}

	if !hasText {
		return fmt.Errorf("response contains no text content")
	}

	if textLength < 100 {
		return fmt.Errorf("response is too brief (%d characters) for a complex technical question", textLength)
	}

	return nil
}

func analyzeExplanationQuality(response *anthropic.Message) error {
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			text := strings.ToLower(textBlock.Text)

			// Check for reasoning indicators
			reasoningPhrases := []string{
				"because", "therefore", "however", "on the other hand", "advantage", "disadvantage",
				"benefit", "drawback", "reason", "consider", "comparison", "versus",
			}

			reasoningCount := 0
			for _, phrase := range reasoningPhrases {
				if strings.Contains(text, phrase) {
					reasoningCount++
				}
			}

			if reasoningCount < 3 {
				return fmt.Errorf("explanation lacks sufficient reasoning (found %d reasoning phrases)", reasoningCount)
			}

			// Should mention specific database features
			dbFeatures := []string{"transaction", "acid", "performance", "scalability", "storage", "concurrency"}
			featureCount := 0
			for _, feature := range dbFeatures {
				if strings.Contains(text, feature) {
					featureCount++
				}
			}

			if featureCount < 2 {
				return fmt.Errorf("explanation lacks technical depth (found %d technical features)", featureCount)
			}

			return nil
		}
	}

	return fmt.Errorf("response contains no text content")
}

func analyzeCodeQuality(response *anthropic.Message) error {
	hasCode := false
	
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			text := textBlock.Text

			// Check for code blocks
			if strings.Contains(text, "```go") || strings.Contains(text, "```") {
				hasCode = true

				// Look for good practices in the code
				goodPractices := []string{
					"func ", "error", "return", "//", "validate", "if err !=", "fmt.Errorf",
				}

				practiceCount := 0
				for _, practice := range goodPractices {
					if strings.Contains(text, practice) {
						practiceCount++
					}
				}

				if practiceCount < 4 {
					return fmt.Errorf("code lacks good practices (found %d/7 practices)", practiceCount)
				}
			}
		}
	}

	if !hasCode {
		return fmt.Errorf("response contains no code blocks for a code generation request")
	}

	return nil
}

func analyzeProfessionalTone(response *anthropic.Message) error {
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			text := strings.ToLower(textBlock.Text)

			// Check for unprofessional language
			unprofessionalWords := []string{
				"terrible", "awful", "stupid", "dumb", "idiotic", "crappy", "sucks", "horrible",
				"incompetent", "worthless",
			}

			for _, word := range unprofessionalWords {
				if strings.Contains(text, word) {
					return fmt.Errorf("response contains unprofessional language: %s", word)
				}
			}

			// Look for professional alternatives
			professionalPhrases := []string{
				"improve", "enhance", "optimize", "refactor", "address", "consider", "suggest",
				"recommend", "opportunity", "alternative",
			}

			professionalCount := 0
			for _, phrase := range professionalPhrases {
				if strings.Contains(text, phrase) {
					professionalCount++
				}
			}

			if professionalCount == 0 {
				return fmt.Errorf("response lacks professional language and tone")
			}

			return nil
		}
	}

	return fmt.Errorf("response contains no text content")
}

func analyzeClarifyingQuestions(response *anthropic.Message) error {
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			text := textBlock.Text

			// Check for question marks (indicating questions)
			questionCount := strings.Count(text, "?")
			if questionCount < 2 {
				return fmt.Errorf("response should ask multiple clarifying questions for vague request (found %d)", questionCount)
			}

			// Look for clarifying question indicators
			clarifyingPhrases := []string{
				"what", "which", "how", "when", "where", "clarify", "specific", "details", "more information",
			}

			clarifyingCount := 0
			lowerText := strings.ToLower(text)
			for _, phrase := range clarifyingPhrases {
				if strings.Contains(lowerText, phrase) {
					clarifyingCount++
				}
			}

			if clarifyingCount < 3 {
				return fmt.Errorf("response lacks sufficient clarifying language (found %d phrases)", clarifyingCount)
			}

			return nil
		}
	}

	return fmt.Errorf("response contains no text content")
}

func analyzeErrorHandlingAdvice(response *anthropic.Message) error {
	for _, content := range response.Content {
		if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
			text := strings.ToLower(textBlock.Text)

			// Look for troubleshooting steps
			troubleshootingTerms := []string{
				"check", "verify", "ensure", "test", "diagnose", "debug", "step", "first", "then",
				"connection", "configuration", "settings", "network", "firewall", "port",
			}

			troubleshootingCount := 0
			for _, term := range troubleshootingTerms {
				if strings.Contains(text, term) {
					troubleshootingCount++
				}
			}

			if troubleshootingCount < 4 {
				return fmt.Errorf("response lacks comprehensive troubleshooting advice (found %d terms)", troubleshootingCount)
			}

			// Should provide systematic approach
			systematicPhrases := []string{"1.", "2.", "first", "second", "next", "step"}
			systematicCount := 0
			for _, phrase := range systematicPhrases {
				if strings.Contains(text, phrase) {
					systematicCount++
				}
			}

			if systematicCount < 2 {
				return fmt.Errorf("response lacks systematic approach to problem solving")
			}

			return nil
		}
	}

	return fmt.Errorf("response contains no text content")
}