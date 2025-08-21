// Package github provides GitHub API integration for the Blundering Savant Bot.
// It handles authentication, webhook processing, and API interactions.
package github

import (
	"github.com/google/go-github/v72/github"
)

// GitHubIssue represents a GitHub issue with additional metadata
type GitHubIssue struct {
	*github.Issue
	Owner      string
	Repository string
}

// GitHubPullRequest represents a GitHub pull request with additional metadata
type GitHubPullRequest struct {
	*github.PullRequest
	Owner      string
	Repository string
}

var (
	// LabelWorking indicates the bot is actively working on an issue
	LabelWorking = github.Label{
		Name:        github.Ptr("bot-working"),
		Description: github.Ptr("the bot is actively working on this issue"),
		Color:       github.Ptr("fbca04"),
	}
	
	// LabelBlocked indicates the bot encountered a problem and needs human intervention
	LabelBlocked = github.Label{
		Name:        github.Ptr("bot-blocked"),
		Description: github.Ptr("the bot encountered a problem and needs human intervention to continue working on this issue"),
		Color:       github.Ptr("f03010"),
	}
	
	// LabelBotTurn indicates it is the bot's turn to take action on this issue
	LabelBotTurn = github.Label{
		Name:        github.Ptr("bot-turn"),
		Description: github.Ptr("it is the bot's turn to take action on this issue"),
		Color:       github.Ptr("2020f0"),
	}
)