# End-to-End Test Examples

This document provides examples of how to create and run end-to-end tests for the Blundering Savant prompt system.

## Basic Test Structure

All e2e tests follow this pattern:

```go
//go:build e2e

package mypackage

import (
    "context"
    "testing"
    "github.com/cchalm/blundering-savant/test/e2e/testutil"
)

func TestMyScenario(t *testing.T) {
    harness := testutil.NewTestHarness(t)
    
    testName := "my_scenario"
    harness.RunIterations(testName, func(iteration int) error {
        return harness.WithTimeout(func(ctx context.Context) error {
            // Your test logic here
            return nil
        })
    })
}
```

## Example: Testing Tool Usage

```go
func TestFileCreation(t *testing.T) {
    harness := testutil.NewTestHarness(t)
    
    harness.RunIterations("file_creation", func(iteration int) error {
        return harness.WithTimeout(func(ctx context.Context) error {
            conversation := harness.CreateConversation("You are a helpful developer.")
            
            prompt := "Create a Go file called hello.go with a main function"
            response, err := conversation.SendMessage(ctx, anthropic.NewTextBlock(prompt))
            if err != nil {
                return err
            }
            
            // Check that the AI used the file creation tool
            for _, content := range response.Content {
                if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
                    if toolBlock.Name == "str_replace_based_edit_tool" {
                        return nil // Success!
                    }
                }
            }
            
            return fmt.Errorf("AI did not use file creation tool")
        })
    })
}
```

## Example: Testing Conversation Coherence

```go
func TestConversationFlow(t *testing.T) {
    harness := testutil.NewTestHarness(t)
    
    harness.RunIterations("conversation_flow", func(iteration int) error {
        return harness.WithTimeout(func(ctx context.Context) error {
            conversation := harness.CreateConversation("You are a software architect.")
            
            // Start a technical discussion
            _, err := conversation.SendMessage(ctx, 
                anthropic.NewTextBlock("I need to design a microservices architecture"))
            if err != nil {
                return err
            }
            
            // Continue the conversation
            response, err := conversation.SendMessage(ctx,
                anthropic.NewTextBlock("What about service discovery?"))
            if err != nil {
                return err
            }
            
            // Verify the response shows contextual awareness
            return analyzeContextualResponse(response)
        })
    })
}

func analyzeContextualResponse(response *anthropic.Message) error {
    for _, content := range response.Content {
        if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
            text := strings.ToLower(textBlock.Text)
            
            // Look for architecture-related terms
            if strings.Contains(text, "service") || 
               strings.Contains(text, "microservice") ||
               strings.Contains(text, "discovery") {
                return nil
            }
        }
    }
    return fmt.Errorf("response lacks contextual awareness")
}
```

## Example: Testing Limitation Reporting

```go
func TestLimitationReporting(t *testing.T) {
    harness := testutil.NewTestHarness(t)
    
    harness.RunIterations("limitation_reporting", func(iteration int) error {
        return harness.WithTimeout(func(ctx context.Context) error {
            conversation := harness.CreateConversation(
                "You are a developer with limited tools.")
            
            // Ask for something the AI can't do
            response, err := conversation.SendMessage(ctx,
                anthropic.NewTextBlock("Please run the test suite"))
            if err != nil {
                return err
            }
            
            // Verify it reports the limitation properly
            hasLimitationReport := false
            for _, content := range response.Content {
                if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
                    if toolBlock.Name == "report_limitation" {
                        hasLimitationReport = true
                        break
                    }
                }
            }
            
            if !hasLimitationReport {
                return fmt.Errorf("AI should have reported limitation")
            }
            
            return nil
        })
    })
}
```

## Running Tests

### Set up environment
```bash
export ANTHROPIC_API_KEY="your-api-key"
export GITHUB_TOKEN="your-token"  # Optional
```

### Run all e2e tests
```bash
just test-e2e
```

### Run specific test packages
```bash
go test -tags=e2e -v ./test/e2e/tools/
go test -tags=e2e -v ./test/e2e/limitations/
```

### Run with custom configuration
```bash
E2E_MODEL=claude-3-haiku-20241022 \
E2E_MAX_TOKENS=2000 \
E2E_ITERATIONS=5 \
go test -tags=e2e -v ./test/e2e/...
```

## Best Practices

### Cost Management
- Use fewer iterations for expensive tests
- Consider using cheaper models for simple scenarios
- Set reasonable token limits
- Group related assertions in single tests

### Reliability
- Make assertions flexible (look for patterns, not exact matches)
- Use majority-success criteria (2/3 success rate)
- Handle API errors gracefully
- Include timeout protection

### Maintainability
- Use descriptive test names
- Extract common analysis functions
- Document expected behaviors clearly
- Use test harnesses for repeated patterns

## Common Patterns

### Analyzing Tool Usage
```go
func hasToolCall(response *anthropic.Message, toolName string) bool {
    for _, content := range response.Content {
        if toolBlock, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
            if toolBlock.Name == toolName {
                return true
            }
        }
    }
    return false
}
```

### Checking Text Content
```go
func hasTextContent(response *anthropic.Message, keywords []string) bool {
    for _, content := range response.Content {
        if textBlock, ok := content.AsAny().(anthropic.TextBlock); ok {
            text := strings.ToLower(textBlock.Text)
            for _, keyword := range keywords {
                if strings.Contains(text, strings.ToLower(keyword)) {
                    return true
                }
            }
        }
    }
    return false
}
```

### Parsing Tool Inputs
```go
func parseToolInput(toolBlock anthropic.ToolUseBlock, target any) error {
    return json.Unmarshal(toolBlock.Input, target)
}
```