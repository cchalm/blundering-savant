package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/go-github/v57/github"
)

// AnthropicClient handles communication with the Anthropic API
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewAnthropicClient creates a new Anthropic API client
func NewAnthropicClient(apiKey string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:  apiKey,
		baseURL: "https://api.anthropic.com/v1",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Message represents an Anthropic message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool represents a tool that can be used by Claude
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicRequest represents a request to the Anthropic API
type AnthropicRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
	Tools     []Tool    `json:"tools,omitempty"`
	System    string    `json:"system,omitempty"`
}

// AnthropicResponse represents a response from the Anthropic API
type AnthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	Model        string `json:"model"`
	StopReason   string `json:"stop_reason"`
	StopSequence string `json:"stop_sequence"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// CreateMessage sends a message to the Anthropic API
func (c *AnthropicClient) CreateMessage(prompt string, tools []Tool) (*AnthropicResponse, error) {
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
- Ensure your branch names are descriptive and follow conventions (e.g., fix/issue-description, feature/new-feature)
- Write clear, conventional commit messages`

	request := &AnthropicRequest{
		Model: "claude-3-opus-20240229",
		Messages: []Message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens: 4096,
		Tools:     tools,
		System:    systemPrompt,
	}

	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/messages", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var response AnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}

// AnalyzeCodeContext analyzes code context for better responses
func (c *AnthropicClient) AnalyzeCodeContext(fileContent, issueDescription, styleGuide string) (*AnthropicResponse, error) {
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

	return c.CreateMessage(prompt, nil)
}

// GenerateCodeReview generates a response to code review comments
func (c *AnthropicClient) GenerateCodeReview(comment, codeContext string, isApproval bool) (*AnthropicResponse, error) {
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
5. Maintains a collaborative and respectful tone`, comment, codeContext, map[bool]string{true: "approving", false: "change-requesting"}[isApproval])

	return c.CreateMessage(prompt, nil)
}

// GeneratePRDescription creates a detailed PR description
func (c *AnthropicClient) GeneratePRDescription(issue *github.Issue, changes map[string]FileChange, styleGuide string) (string, error) {
	changesDescription := ""
	for path, change := range changes {
		action := "Modified"
		if change.IsNew {
			action = "Created"
		}
		changesDescription += fmt.Sprintf("- %s `%s`\n", action, path)
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

Keep it concise but informative.`, *issue.Title, *issue.Body, changesDescription)

	response, err := c.CreateMessage(prompt, nil)
	if err != nil {
		return "", err
	}

	// Extract the text from the response
	if len(response.Content) > 0 && response.Content[0].Type == "text" {
		return response.Content[0].Text, nil
	}

	return "", fmt.Errorf("unexpected response format")
}
