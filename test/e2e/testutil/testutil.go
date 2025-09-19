//go:build e2e

package testutil

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/bot"
)

// TestConfig holds configuration for end-to-end tests
type TestConfig struct {
	Model        anthropic.Model
	MaxTokens    int64
	Iterations   int
	Timeout      time.Duration
	AnthropicKey string
	GitHubToken  string
}

// LoadTestConfig loads test configuration from environment variables
func LoadTestConfig() TestConfig {
	config := TestConfig{
		Model:      anthropic.ModelClaudeSonnet4_0,
		MaxTokens:  4000,
		Iterations: 3,
		Timeout:    300 * time.Second,
	}

	if model := os.Getenv("E2E_MODEL"); model != "" {
		config.Model = anthropic.Model(model)
	}

	if tokens := os.Getenv("E2E_MAX_TOKENS"); tokens != "" {
		if val, err := strconv.ParseInt(tokens, 10, 64); err == nil {
			config.MaxTokens = val
		}
	}

	if iterations := os.Getenv("E2E_ITERATIONS"); iterations != "" {
		if val, err := strconv.Atoi(iterations); err == nil {
			config.Iterations = val
		}
	}

	if timeout := os.Getenv("E2E_TIMEOUT"); timeout != "" {
		if val, err := strconv.Atoi(timeout); err == nil {
			config.Timeout = time.Duration(val) * time.Second
		}
	}

	config.AnthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	config.GitHubToken = os.Getenv("GITHUB_TOKEN")

	return config
}

// TestHarness provides utilities for end-to-end testing
type TestHarness struct {
	t               *testing.T
	config          TestConfig
	anthropicClient anthropic.Client
	githubClient    *github.Client
	toolRegistry    *bot.ToolRegistry
}

// NewTestHarness creates a new test harness
func NewTestHarness(t *testing.T) *TestHarness {
	config := LoadTestConfig()

	require.NotEmpty(t, config.AnthropicKey, "ANTHROPIC_API_KEY environment variable is required for e2e tests")

	anthropicClient := anthropic.NewClient(
		anthropic.WithAPIKey(config.AnthropicKey),
	)

	var githubClient *github.Client
	if config.GitHubToken != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: config.GitHubToken},
		)
		tc := oauth2.NewClient(context.Background(), ts)
		githubClient = github.NewClient(tc)
	}

	toolRegistry := bot.NewToolRegistry()

	return &TestHarness{
		t:               t,
		config:          config,
		anthropicClient: anthropicClient,
		githubClient:    githubClient,
		toolRegistry:    toolRegistry,
	}
}

// Config returns the test configuration
func (h *TestHarness) Config() TestConfig {
	return h.config
}

// AnthropicClient returns the Anthropic client
func (h *TestHarness) AnthropicClient() anthropic.Client {
	return h.anthropicClient
}

// GitHubClient returns the GitHub client (may be nil if no token provided)
func (h *TestHarness) GitHubClient() *github.Client {
	return h.githubClient
}

// ToolRegistry returns the bot's tool registry
func (h *TestHarness) ToolRegistry() *bot.ToolRegistry {
	return h.toolRegistry
}

// CreateConversation creates a new conversation with the configured settings
func (h *TestHarness) CreateConversation(systemPrompt string) *ai.Conversation {
	tools := h.toolRegistry.GetAllToolParams()

	return ai.NewConversation(
		h.anthropicClient,
		h.config.Model,
		h.config.MaxTokens,
		tools,
		systemPrompt,
	)
}

// ResumeConversation resumes a conversation from history
func (h *TestHarness) ResumeConversation(history ai.ConversationHistory) (*ai.Conversation, error) {
	tools := h.toolRegistry.GetAllToolParams()

	return ai.ResumeConversation(
		h.anthropicClient,
		history,
		h.config.Model,
		h.config.MaxTokens,
		tools,
	)
}

// RunIterations runs a test function multiple times and reports results
func (h *TestHarness) RunIterations(testName string, testFunc func(iteration int) error) {
	h.t.Helper()

	successCount := 0
	var lastError error

	for i := 0; i < h.config.Iterations; i++ {
		h.t.Logf("Running iteration %d/%d of %s", i+1, h.config.Iterations, testName)

		err := testFunc(i)
		if err != nil {
			h.t.Logf("Iteration %d failed: %v", i+1, err)
			lastError = err
		} else {
			successCount++
			h.t.Logf("Iteration %d succeeded", i+1)
		}
	}

	h.t.Logf("Test %s: %d/%d iterations succeeded", testName, successCount, h.config.Iterations)

	// Require at least 2/3 success rate for tests to pass
	minSuccessCount := (h.config.Iterations*2 + 2) / 3
	if successCount < minSuccessCount {
		require.NoErrorf(h.t, lastError, "Test %s failed with %d/%d successes (minimum %d required)",
			testName, successCount, h.config.Iterations, minSuccessCount)
	}
}

// WithTimeout runs a function with the configured timeout
func (h *TestHarness) WithTimeout(fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer cancel()

	return fn(ctx)
}

// MockWorkspace is a simple mock workspace for testing that doesn't require real file operations
type MockWorkspace struct {
	files                 map[string]string
	hasLocalChanges       bool
	hasUnpublishedChanges bool
}

// NewMockWorkspace creates a new mock workspace
func NewMockWorkspace() *MockWorkspace {
	return &MockWorkspace{
		files: make(map[string]string),
	}
}

func (m *MockWorkspace) Read(ctx context.Context, path string) (string, error) {
	if content, exists := m.files[path]; exists {
		return content, nil
	}
	return "", os.ErrNotExist
}

func (m *MockWorkspace) Write(ctx context.Context, path string, content string) error {
	m.files[path] = content
	m.hasLocalChanges = true
	return nil
}

func (m *MockWorkspace) FileExists(ctx context.Context, path string) (bool, error) {
	_, exists := m.files[path]
	return exists, nil
}

func (m *MockWorkspace) ListFiles(ctx context.Context, dir string) ([]string, error) {
	var files []string
	for path := range m.files {
		files = append(files, path)
	}
	return files, nil
}

func (m *MockWorkspace) Delete(ctx context.Context, path string) error {
	delete(m.files, path)
	m.hasLocalChanges = true
	return nil
}

func (m *MockWorkspace) HasLocalChanges() bool {
	return m.hasLocalChanges
}

func (m *MockWorkspace) ClearLocalChanges() {
	m.hasLocalChanges = false
}

func (m *MockWorkspace) HasUnpublishedChanges(ctx context.Context) (bool, error) {
	return m.hasUnpublishedChanges, nil
}

func (m *MockWorkspace) ValidateChanges(ctx context.Context, commitMessage *string) (result struct {
	Succeeded bool
	Details   string
}, err error) {
	m.hasLocalChanges = false
	m.hasUnpublishedChanges = true
	return struct {
		Succeeded bool
		Details   string
	}{Succeeded: true, Details: "Mock validation succeeded"}, nil
}

func (m *MockWorkspace) PublishChangesForReview(ctx context.Context, title, body string) error {
	m.hasUnpublishedChanges = false
	return nil
}

// SetFile sets the content of a file in the mock workspace
func (m *MockWorkspace) SetFile(path, content string) {
	m.files[path] = content
}

// GetFiles returns all files in the mock workspace
func (m *MockWorkspace) GetFiles() map[string]string {
	result := make(map[string]string)
	for k, v := range m.files {
		result[k] = v
	}
	return result
}
