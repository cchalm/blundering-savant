package main

import (
	"github.com/google/go-github/v72/github"
)

// task represents all the context needed for the bot to generate solutions
type task struct {
	// Core entities
	Issue       githubIssue
	Repository  *github.Repository
	PullRequest *githubPullRequest // May be nil if no pull request has yet been created

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

	// Configuration
	BotUsername string

	// State computed from the workspace after initial task generation (unpopulated until then)
	HasUnpublishedChanges bool
	ValidationResult      ValidationResult
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
