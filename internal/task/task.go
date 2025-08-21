// Package task provides task management and generation for the bot.
package task

import (
	"github.com/google/go-github/v72/github"

	githubpkg "github.com/cchalm/blundering-savant/internal/github"
)

// Task represents all the context needed for the bot to generate solutions
type Task struct {
	// Core entities
	Issue       githubpkg.GitHubIssue
	Repository  *github.Repository
	PullRequest *githubpkg.GitHubPullRequest // May be nil if no pull request has yet been created

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
	PRReviewCommentThreadsRequiringResponses [][]*github.PullRequestComment

	ValidationFailures []ValidationFailure
}

// StyleGuide represents style guide information for the repository
type StyleGuide struct {
	Exists  bool
	Name    string
	Content string
}

// CodebaseInfo represents information about the codebase
type CodebaseInfo struct {
	MainLanguage string
	Languages    map[string]int
	FileTree     string
	ReadmeExcerpt string
}

// ValidationFailure represents a validation failure
type ValidationFailure struct {
	Type    string
	Message string
	File    string
	Line    int
}