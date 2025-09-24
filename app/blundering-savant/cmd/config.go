package cmd

import (
	"log"
	"os"
	"time"
)

var config = Config{}

type Config struct {
	// Common config
	SystemGithubToken      string // The token used for operations with no attribution requirements
	BotGithubToken         string // The token used for operations that should be attributed to the AI
	AnthropicAPIKey        string
	ValidationWorkflowName string

	// Telemetry config
	TelemetryEnabled bool
	JaegerEndpoint   string

	// One-shot options
	QualifiedRepoName string
	IssueNumber       int
	PRBranch          string

	// Polling options
	CheckInterval             time.Duration
	ResumableConversationsDir string
}

func loadFromEnv(dest *string, key string) {
	parseFromEnv(dest, key, func(v string) (string, error) { return v, nil })
}

func parseFromEnv[T any](dest *T, key string, parseFn func(string) (T, error)) {
	str := os.Getenv(key)
	if str == "" {
		log.Fatalf("%s not set", key)
	}
	v, err := parseFn(str)
	if err != nil {
		log.Fatalf("failed to parse environment variable '%s' value '%s' as '%T': %v", key, str, *dest, err)
	}
	*dest = v
}

func loadOptionalFromEnv(dest *string, key string) {
	parseOptionalFromEnv(dest, key, func(v string) (string, error) { return v, nil })
}

func parseOptionalFromEnv[T any](dest *T, key string, parseFn func(string) (T, error)) {
	str := os.Getenv(key)
	if str == "" {
		return // Leave default value
	}
	v, err := parseFn(str)
	if err != nil {
		log.Fatalf("failed to parse environment variable '%s' value '%s' as '%T': %v", key, str, *dest, err)
	}
	*dest = v
}
