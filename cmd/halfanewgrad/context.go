package main

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v72/github"
)

// workContext represents all the context needed for the bot to generate solutions
type workContext struct {
	// Core entities
	Issue       *github.Issue
	Repository  *github.Repository
	PullRequest *github.PullRequest // May be nil if no pull request has yet been created

	// Branches
	TargetBranch string // The branch that changes will be merged into after review
	WorkBranch   string // The branch that work will be done in

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
	Content   string
	FilePath  string
	RepoStyle map[string]string // language -> style patterns
}


// GetMainLanguageInfo returns information about the main programming language
func (ctx workContext) GetMainLanguageInfo() (string, map[string]string) {
	if ctx.CodebaseInfo == nil {
		return "unknown", make(map[string]string)
	}

	lang := ctx.CodebaseInfo.MainLanguage
	if lang == "" {
		lang = "unknown"
	}

	styleInfo := make(map[string]string)
	if ctx.StyleGuide != nil && ctx.StyleGuide.RepoStyle != nil {
		if info, exists := ctx.StyleGuide.RepoStyle[strings.ToLower(lang)]; exists {
			styleInfo[lang] = info
		}
	}

	return lang, styleInfo
}

// GetRepositoryStructure returns a formatted view of the repository structure
func (ctx workContext) GetRepositoryStructure() string {
	if ctx.CodebaseInfo == nil || len(ctx.CodebaseInfo.FileTree) == 0 {
		return "Repository structure not available"
	}

	var structure strings.Builder
	structure.WriteString("Repository Structure:\n")

	for i, file := range ctx.CodebaseInfo.FileTree {
		if i >= 30 { // Limit to first 30 files
			structure.WriteString("  ... (and more files)\n")
			break
		}
		structure.WriteString(fmt.Sprintf("  %s\n", file))
	}

	return structure.String()
}

// Utility functions

// truncateString truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
