package cmd

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"
)

var config Config

type Config struct {
	// Common config
	SystemGithubToken      string // The token used for operations with no attribution requirements
	BotGithubToken         string // The token used for operations that should be attributed to the AI
	AnthropicAPIKey        string
	ValidationWorkflowName string

	// One-shot options
	QualifiedRepoName string
	IssueNumber       *int
	PRBranch          *string

	// Polling options
	CheckInterval             time.Duration
	ResumableConversationsDir string
}

func loadFromEnv[T any](dest *T, key string) {
	parseFromEnv(dest, key, func(v string) (T, error) {
		var parsed T
		err := json.NewDecoder(strings.NewReader(v)).Decode(&parsed)
		return parsed, err
	})
}

func parseFromEnv[T any](dest *T, key string, parseFn func(string) (T, error)) {
	str := os.Getenv(key)
	if str == "" {
		log.Fatalf("%s not set", key)
	}
	v, err := parseFn(str)
	if err != nil {
		log.Fatal("failed to parse environment variable '%s' value '%s' as '%T': %v", key, str, dest, err)
	}
	*dest = v
}
