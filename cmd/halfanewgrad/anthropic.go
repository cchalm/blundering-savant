package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/go-github/v72/github"
)

// AnthropicClient wraps the official Anthropic SDK client
type AnthropicClient struct {
	client anthropic.Client
}

// NewAnthropicClient creates a new Anthropic API client using the official SDK
func NewAnthropicClient(apiKey string) *AnthropicClient {
	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
	)
	return &AnthropicClient{
		client: client,
	}
}

// CreateMessage sends a message to the Anthropic API with optional tools
func (ac *AnthropicClient) CreateMessage(ctx context.Context, prompt string, tools []anthropic.ToolParam) (*anthropic.Message, error) {
	systemPrompt := `You are a highly skilled software developer working as a virtual assistant on GitHub.
Your responsibilities include:
1. Analyzing GitHub issues and creating high-quality code solutions
2. Following repository coding standards and style guides meticulously
3. Writing clean, maintainable, and well-tested code
4. Responding professionally to PR comments and reviews
5. Making minimal, focused changes that directly address the issue

When analyzing code:
- Pay careful attention to existing patterns and conventions
- Maintain consistency with the surrounding codebase
- Consider edge cases and error handling
- Write appropriate tests when applicable

When using tools:
- Use analyze_file to examine relevant files before making changes
- Use create_solution to provide your complete solution with all necessary file changes
- Ensure your solutions include actual file content, not just placeholders
- Write clear, conventional commit messages
- Always include at least one meaningful file change in your solution

IMPORTANT: When creating solutions, you MUST include actual implementation code in the files, not just comments or placeholders. The solution should be ready to run and address the specific issue described.`

	params := anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_0,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	}

	// Add tools if provided
	if len(tools) > 0 {
		toolsUnion := make([]anthropic.ToolUnionParam, len(tools))
		for i, tool := range tools {
			toolsUnion[i] = anthropic.ToolUnionParam{
				OfTool: &tool,
			}
		}
		params.Tools = toolsUnion
	}

	message, err := ac.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

// AnalyzeCodeContext analyzes code context for better responses
func (ac *AnthropicClient) AnalyzeCodeContext(ctx context.Context, fileContent, issueDescription, styleGuide string) (*anthropic.Message, error) {
	prompt := fmt.Sprintf(`Analyze this code file in the context of the following issue:

Issue: %s

File content:
%s

Style guide (if available):
%s

Please identify:
1. The main purpose and structure of this code
2. Relevant coding patterns and conventions used
3. How this file might relate to the issue
4. Any potential areas that need modification`, issueDescription, fileContent, styleGuide)

	return ac.CreateMessage(ctx, prompt, nil)
}

// GenerateCodeReview generates a response to code review comments
func (ac *AnthropicClient) GenerateCodeReview(ctx context.Context, comment, codeContext string, isApproval bool) (*anthropic.Message, error) {
	reviewType := "change-requesting"
	if isApproval {
		reviewType = "approving"
	}

	prompt := fmt.Sprintf(`A reviewer has left the following comment on a pull request:

Comment: %s

Code context:
%s

This comment is part of a %s review.

Please generate an appropriate response that:
1. Acknowledges the feedback professionally
2. Explains any reasoning behind the current implementation if needed
3. Indicates whether and how you'll address the feedback
4. Asks for clarification if the comment is unclear
5. Maintains a collaborative and respectful tone`, comment, codeContext, reviewType)

	return ac.CreateMessage(ctx, prompt, nil)
}

// GeneratePRDescription creates a detailed PR description
func (ac *AnthropicClient) GeneratePRDescription(ctx context.Context, issue *github.Issue, changes map[string]FileChange, styleGuide string) (string, error) {
	changesDescription := ""
	for path, change := range changes {
		action := "Modified"
		if change.IsNew {
			action = "Created"
		}
		changesDescription += fmt.Sprintf("- %s `%s`\n", action, path)
	}

	// Safe extraction with nil checks
	issueTitle := "<No title>"
	if issue.Title != nil {
		issueTitle = *issue.Title
	}

	issueBody := "<No description>"
	if issue.Body != nil {
		issueBody = *issue.Body
	}

	prompt := fmt.Sprintf(`Generate a clear and comprehensive pull request description for the following:

Issue Title: %s
Issue Description: %s

Files changed:
%s

Please create a PR description that:
1. Clearly explains what the PR does
2. References the original issue
3. Lists the key changes made
4. Mentions any testing performed
5. Notes any potential impacts or considerations
6. Follows professional PR description conventions

Keep it concise but informative.`, issueTitle, issueBody, changesDescription)

	response, err := ac.CreateMessage(ctx, prompt, nil)
	if err != nil {
		return "", err
	}

	// Extract the text from the response
	if len(response.Content) > 0 {
		switch content := response.Content[0].AsAny().(type) {
		case anthropic.TextBlock:
			return content.Text, nil
		}
	}

	return "", fmt.Errorf("unexpected response format")
}

// generateSolutionWithTools generates a solution using Claude with tools
func (ac *AnthropicClient) generateSolutionWithTools(ctx context.Context, issue *github.Issue, repo *github.Repository, styleGuide *StyleGuide, codebaseInfo *CodebaseInfo) (*Solution, error) {
	// Define tools for Claude to use
	tools := []anthropic.ToolParam{
		{
			Name:        "analyze_file",
			Description: anthropic.String("Analyze a file from the repository to understand its structure and content"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "The file path to analyze",
					},
				},
			},
		},
		{
			Name:        "create_solution",
			Description: anthropic.String("Create a solution with file changes to resolve the issue"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"branch": map[string]interface{}{
						"type":        "string",
						"description": "Name of the branch to create (e.g., fix/issue-123-description)",
					},
					"commit_message": map[string]interface{}{
						"type":        "string",
						"description": "Commit message for the changes",
					},
					"files": map[string]interface{}{
						"type":        "object",
						"description": "Map of file paths to their new content",
						"additionalProperties": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"content": map[string]interface{}{
									"type":        "string",
									"description": "Complete file content with actual implementation code",
								},
								"is_new": map[string]interface{}{
									"type":        "boolean",
									"description": "Whether this is a new file",
								},
							},
							"required": []string{"content", "is_new"},
						},
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Description of the solution and what changes were made",
					},
				},
			},
		},
	}

	prompt := buildSolutionPrompt(issue, repo, styleGuide, codebaseInfo)

	response, err := ac.CreateMessage(ctx, prompt, tools)
	if err != nil {
		return nil, fmt.Errorf("failed to generate solution: %w", err)
	}

	// Process the response and extract tool uses
	solution, err := ac.processToolResponse(ctx, response, issue)
	if err != nil {
		return nil, fmt.Errorf("failed to process tool response: %w", err)
	}

	// Validate that the solution has files
	if len(solution.Files) == 0 {
		return nil, fmt.Errorf("solution contains no file changes")
	}

	return solution, nil
}

// processToolResponse processes Claude's response containing tool uses
func (ac *AnthropicClient) processToolResponse(ctx context.Context, response *anthropic.Message, issue *github.Issue) (*Solution, error) {
	var solution *Solution

	for _, content := range response.Content {
		switch block := content.AsAny().(type) {
		case anthropic.ToolUseBlock:
			if block.Name == "create_solution" {
				// Parse the tool input
				var solutionData struct {
					Branch        string                     `json:"branch"`
					CommitMessage string                     `json:"commit_message"`
					Files         map[string]json.RawMessage `json:"files"`
					Description   string                     `json:"description"`
				}

				inputJSON, err := json.Marshal(block.Input)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal tool input: %w", err)
				}

				if err := json.Unmarshal(inputJSON, &solutionData); err != nil {
					return nil, fmt.Errorf("failed to parse solution data: %w", err)
				}

				// Validate required fields
				if solutionData.Branch == "" {
					return nil, fmt.Errorf("solution missing branch name")
				}
				if solutionData.CommitMessage == "" {
					return nil, fmt.Errorf("solution missing commit message")
				}
				if len(solutionData.Files) == 0 {
					return nil, fmt.Errorf("solution contains no files")
				}

				// Convert to our Solution type
				solution = &Solution{
					Branch:        solutionData.Branch,
					CommitMessage: solutionData.CommitMessage,
					Description:   solutionData.Description,
					Files:         make(map[string]FileChange),
				}

				// Parse file changes
				for path, fileData := range solutionData.Files {
					var fileChange struct {
						Content string `json:"content"`
						IsNew   bool   `json:"is_new"`
					}
					if err := json.Unmarshal(fileData, &fileChange); err != nil {
						log.Printf("Warning: failed to parse file change for %s: %v", path, err)
						continue
					}

					// Validate file content is not empty
					if strings.TrimSpace(fileChange.Content) == "" {
						log.Printf("Warning: file %s has empty content", path)
						continue
					}

					solution.Files[path] = FileChange{
						Path:    path,
						Content: fileChange.Content,
						IsNew:   fileChange.IsNew,
					}
				}

				return solution, nil
			}
		}
	}

	// If no solution was found in tool use, return an error
	if solution == nil {
		return nil, fmt.Errorf("no valid solution found in Claude's response")
	}

	return solution, nil
}

// buildSolutionPrompt creates the prompt for generating a solution
func buildSolutionPrompt(issue *github.Issue, repo *github.Repository, styleGuide *StyleGuide, codebaseInfo *CodebaseInfo) string {
	// Safe string extraction with nil checks
	repoName := "unknown"
	if repo != nil && repo.FullName != nil {
		repoName = *repo.FullName
	}

	mainLang := "unknown"
	if codebaseInfo != nil {
		mainLang = codebaseInfo.MainLanguage
	}

	issueNumber := 0
	if issue.Number != nil {
		issueNumber = *issue.Number
	}

	issueTitle := "No title"
	if issue.Title != nil {
		issueTitle = *issue.Title
	}

	issueBody := "No description provided"
	if issue.Body != nil {
		issueBody = *issue.Body
	}

	prompt := fmt.Sprintf(`You are working on a GitHub issue. Your task is to analyze the issue and create a complete, working solution.

Repository: %s
Main Language: %s
Issue #%d: %s

Issue Description:
%s

`, repoName, mainLang, issueNumber, issueTitle, issueBody)

	if styleGuide != nil && styleGuide.Content != "" {
		prompt += fmt.Sprintf("\nStyle Guide:\n%s\n", styleGuide.Content)
	}

	if codebaseInfo != nil && codebaseInfo.ReadmeContent != "" {
		prompt += fmt.Sprintf("\nREADME excerpt:\n%s\n", truncateString(codebaseInfo.ReadmeContent, 1000))
	}

	if codebaseInfo != nil && len(codebaseInfo.FileTree) > 0 {
		prompt += "\nRepository structure (sample files):\n"
		for i, file := range codebaseInfo.FileTree {
			if i >= 20 { // Limit to first 20 files
				prompt += "...\n"
				break
			}
			prompt += fmt.Sprintf("- %s\n", file)
		}
	}

	prompt += fmt.Sprintf(`
Please analyze this issue and create a complete solution. Follow these guidelines:

1. Create a descriptive branch name following the pattern: fix/issue-%d-brief-description
2. Implement the actual solution code - do not use placeholders or TODOs
3. Include complete, working file contents
4. Respect existing code style and conventions
5. Write a clear commit message describing what was fixed
6. Provide a comprehensive description of the changes

You must use the create_solution tool to provide your complete implementation. Ensure that:
- All file contents are complete and functional
- The solution directly addresses the issue described
- Branch names are descriptive and follow conventions
- At least one file is modified or created

Start by creating your solution now.`, issueNumber)

	return prompt
}
