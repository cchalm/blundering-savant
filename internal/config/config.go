// Package config provides configuration management for the Blundering Savant Bot.
package config

import (
	"os"
	"time"

	"github.com/joho/godotenv"
)

// Config holds the configuration for the bot
type Config struct {
	GitHubToken               string
	AnthropicAPIKey           string
	GitHubUsername            string
	ResumableConversationsDir string
	CheckInterval             time.Duration
	ValidationWorkflowName    string
}

// Load loads the configuration from environment variables
func Load() Config {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		// No .env file found, using environment variables
	}

	config := Config{
		GitHubToken:               os.Getenv("GITHUB_TOKEN"),
		AnthropicAPIKey:           os.Getenv("ANTHROPIC_API_KEY"),
		GitHubUsername:            os.Getenv("GITHUB_USERNAME"),
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

// Validate checks if all required configuration values are present
func (c Config) Validate() error {
	if c.GitHubToken == "" {
		return &ValidationError{Field: "GITHUB_TOKEN"}
	}
	if c.AnthropicAPIKey == "" {
		return &ValidationError{Field: "ANTHROPIC_API_KEY"}
	}
	if c.GitHubUsername == "" {
		return &ValidationError{Field: "GITHUB_USERNAME"}
	}
	return nil
}

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field string
}

func (e ValidationError) Error() string {
	return "missing required environment variable: " + e.Field
}