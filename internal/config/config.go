// Package config provides configuration management for the blundering-savant bot.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds the configuration for the bot
type Config struct {
	GitHubToken               string
	AnthropicAPIKey           string
	ResumableConversationsDir string
	CheckInterval             time.Duration
	ValidationWorkflowName    string
}

// Load loads configuration from environment variables
func Load() Config {
	config := Config{
		GitHubToken:               os.Getenv("GITHUB_TOKEN"),
		AnthropicAPIKey:           os.Getenv("ANTHROPIC_API_KEY"),
		ResumableConversationsDir: os.Getenv("RESUMABLE_CONVERSATIONS_DIR"),
		CheckInterval:             5 * time.Minute, // Default
		ValidationWorkflowName:    os.Getenv("VALIDATION_WORKFLOW_NAME"),
	}

	// Parse check interval if provided
	if interval := os.Getenv("CHECK_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			config.CheckInterval = d
		}
	}

	return config
}

// Validate checks if the required configuration is present
func (c Config) Validate() error {
	if c.GitHubToken == "" {
		return fmt.Errorf("missing required environment variable: GITHUB_TOKEN")
	}
	if c.AnthropicAPIKey == "" {
		return fmt.Errorf("missing required environment variable: ANTHROPIC_API_KEY")
	}
	return nil
}