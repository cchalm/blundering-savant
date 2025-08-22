// Package task provides task management and related types for the bot.
package task

import (
	"github.com/google/go-github/v72/github"

	githubTypes "github.com/cchalm/blundering-savant/internal/github"
	"github.com/cchalm/blundering-savant/internal/workspace"
)

// Task represents all the context needed for the bot to generate solutions
type Task struct {
	// Core entities
	Issue       githubTypes.GitHubIssue
	Repository  *github.Repository
	PullRequest *githubTypes.GitHubPullRequest // May be nil if no pull request has yet been created

	// The branch that changes should be merged into to resolve the task
	TargetBranch string
	// The branch name used for the pull request, generated from issue details
	SourceBranch string

	// Code context
	StyleGuide   *StyleGuide
	CodebaseInfo *CodebaseInfo

	// Conversation context
	IssueComments          []*github.IssueComment         // Issue comments are sorted by timestamp
	PRComments             []*github.IssueComment         // PRs are issues under the hood, so PR comments are issue comments. These are also sorted by timestamp
	PRReviewCommentThreads [][]*github.PullRequestComment // List of comment threads
	PRReviews              []*github.PullRequestReview    // PR reviews are sorted by timestamp

	// Current work state
	IssueCommentsRequiringResponses    []*github.IssueComment
	PRCommentsRequiringResponses       []*github.IssueComment
	PRReviewCommentsRequiringResponses []*github.PullRequestComment

	// State computed from the workspace after initial task generation (unpopulated until then)
	HasUnpublishedChanges bool
	ValidationResult      workspace.ValidationResult
}

// CodebaseInfo holds information about the repository structure
type CodebaseInfo struct {
	MainLanguage  string
	FileTree      []string
	ReadmeContent string
	PackageInfo   map[string]string
}

// StyleGuide represents coding style information
type StyleGuide struct {
	Guides map[string]string // repo path -> style guide content
}