package main

import (
	"fmt"
	"os"
	"time"

	"github.com/cchalm/blundering-savant/app/blundering-savant/cmd"
)

// Version information set by ldflags during build
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// Config holds the configuration for the bot
type Config struct {
	// Authentication
	GitHubToken       string // For poll mode (bot's own token)
	SystemGitHubToken string // For task mode (GitHub Actions token)
	BotGitHubToken    string // For task mode (bot's token)
	AnthropicAPIKey   string

	// Bot configuration
	ResumableConversationsDir string
	CheckInterval             time.Duration
	ValidationWorkflowName    string

	// Task mode specific
	QualifiedRepoName string
	IssueNumber       *int
	PRBranch          *string
}
	
func main() {
	cmd.SetVersionInfo(Version, GitCommit, BuildTime)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
