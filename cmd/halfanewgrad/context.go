package main

import (
	"fmt"
	"strconv"
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
	IssueComments          []*github.IssueComment
	PRComments             []*github.IssueComment         // PRs are issues under the hood, so PR comments are issue comments
	PRReviewCommentThreads [][]*github.PullRequestComment // List of comment threads
	PRReviews              []*github.PullRequestReview

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

// BuildPrompt generates the complete prompt for Claude based on the context
func (ctx workContext) BuildPrompt() string {
	var prompt strings.Builder

	// Basic information
	prompt.WriteString(ctx.buildBasicInfo())

	// Code context
	if ctx.StyleGuide != nil && ctx.StyleGuide.Content != "" {
		prompt.WriteString(fmt.Sprintf("\n\nStyle Guide:\n%s\n", ctx.StyleGuide.Content))
	}

	if ctx.CodebaseInfo != nil {
		prompt.WriteString(ctx.buildCodebaseContext())
	}

	// Conversation context
	if len(ctx.IssueComments) > 0 || len(ctx.PRComments) > 0 || len(ctx.PRReviewCommentThreads) > 0 || len(ctx.PRReviews) > 0 {
		prompt.WriteString("\n\n## Conversation History\n")
		prompt.WriteString(ctx.buildConversationContext())
	}

	// Instructions based on state
	prompt.WriteString(ctx.buildInstructions())

	return prompt.String()
}

// buildBasicInfo creates the basic issue/PR information section
func (ctx workContext) buildBasicInfo() string {
	var info strings.Builder

	repoName := "unknown"
	if ctx.Repository != nil && ctx.Repository.FullName != nil {
		repoName = *ctx.Repository.FullName
	}

	mainLang := "unknown"
	if ctx.CodebaseInfo != nil {
		mainLang = ctx.CodebaseInfo.MainLanguage
	}

	issueNumber := 0
	if ctx.Issue != nil && ctx.Issue.Number != nil {
		issueNumber = *ctx.Issue.Number
	}

	issueTitle := "No title"
	if ctx.Issue != nil && ctx.Issue.Title != nil {
		issueTitle = *ctx.Issue.Title
	}

	issueBody := "No description provided"
	if ctx.Issue != nil && ctx.Issue.Body != nil {
		issueBody = *ctx.Issue.Body
	}

	info.WriteString(fmt.Sprintf(`Repository: %s
Main Language: %s
Issue #%d: %s

Issue Description:
%s`, repoName, mainLang, issueNumber, issueTitle, issueBody))

	if ctx.PullRequest != nil && ctx.PullRequest.Number != nil {
		info.WriteString(fmt.Sprintf("\n\nPull Request #%d is open for this issue.", *ctx.PullRequest.Number))
	}

	return info.String()
}

// buildCodebaseContext creates the codebase information section
func (ctx workContext) buildCodebaseContext() string {
	var info strings.Builder

	if ctx.CodebaseInfo.ReadmeContent != "" {
		info.WriteString(fmt.Sprintf("\n\nREADME excerpt:\n%s\n", truncateString(ctx.CodebaseInfo.ReadmeContent, 1000)))
	}

	if len(ctx.CodebaseInfo.FileTree) > 0 {
		info.WriteString("\nRepository structure (sample files):\n")
		for i, file := range ctx.CodebaseInfo.FileTree {
			if i >= 20 {
				info.WriteString("...\n")
				break
			}
			info.WriteString(fmt.Sprintf("- %s\n", file))
		}
	}

	return info.String()
}

// buildConversationContext creates a chronological view of all comments
func (ctx workContext) buildConversationContext() string {
	var timeline []string

	// Add issue comments
	for _, comment := range ctx.IssueComments {
		timeline = append(timeline, ctx.formatComment(comment, "Issue"))
	}

	// Add PR reviews and their comments
	for _, review := range ctx.PRReviews {
		// Add the main review
		reviewStr := fmt.Sprintf("\n### PR Review by @%s (%s) - %s\n",
			*review.User.Login,
			*review.AuthorAssociation,
			review.SubmittedAt.Format("2006-01-02 15:04"))

		if review.State != nil {
			reviewStr += fmt.Sprintf("**Status: %s**\n", *review.State)
		}

		if review.Body != nil {
			reviewStr += fmt.Sprintf("\n%s\n", *review.Body)
		}

		timeline = append(timeline, reviewStr)
	}

	for _, prComment := range ctx.PRComments {
		timeline = append(timeline, ctx.formatComment(prComment, "Issue"))
	}

	// TODO consider grouping comments with their reviews

	// Add PR review comment threads
	for _, thread := range ctx.PRReviewCommentThreads {
		timeline = append(timeline, ctx.formatReviewCommentThread(thread))
	}

	return strings.Join(timeline, "\n")
}

// formatComment formats a regular comment
func (ctx workContext) formatComment(comment *github.IssueComment, commentType string) string {
	var formatted strings.Builder

	formatted.WriteString(fmt.Sprintf("\n### %s Comment %d by @%s", commentType, *comment.ID, *comment.User.Login))

	if comment.AuthorAssociation != nil && *comment.AuthorAssociation != "" && *comment.AuthorAssociation != "none" {
		formatted.WriteString(fmt.Sprintf(" (%s)", *comment.AuthorAssociation))
	}

	formatted.WriteString(fmt.Sprintf(" - %s\n", comment.CreatedAt.Format("2006-01-02 15:04")))

	if comment.UpdatedAt != nil && comment.CreatedAt != comment.UpdatedAt {
		formatted.WriteString("*(edited)*\n")
	}

	formatted.WriteString(fmt.Sprintf("\n%s\n", *comment.Body))

	return formatted.String()
}

func (ctx workContext) formatReviewCommentThread(thread []*github.PullRequestComment) string {
	var formatted strings.Builder

	if len(thread) != 0 {
		topComment := thread[0]

		formatted.WriteString(fmt.Sprintf("\n### PR Review Comment Thread on `%s`", *topComment.Path))
		if topComment.Line != nil && *topComment.Line > 0 {
			formatted.WriteString(fmt.Sprintf(" (line %d", *topComment.Line))
			if topComment.StartLine != nil && *topComment.StartLine != *topComment.Line {
				formatted.WriteString(fmt.Sprintf("-%d", *topComment.Line))
			}
			formatted.WriteString(")")
		}

		if topComment.DiffHunk != nil && *topComment.DiffHunk != "" {
			formatted.WriteString(fmt.Sprintf("\n```diff\n%s\n```\n", *topComment.DiffHunk))
		}

		for _, comment := range thread {
			formatted.WriteString(ctx.formatReviewComment(comment))
		}
	}

	return formatted.String()
}

// formatReviewComment formats a code review comment
func (ctx workContext) formatReviewComment(comment *github.PullRequestComment) string {
	var formatted strings.Builder

	formatted.WriteString(fmt.Sprintf("PR Review Comment %d by @%s", *comment.ID, *comment.User.Login))

	if comment.AuthorAssociation != nil && *comment.AuthorAssociation != "" && *comment.AuthorAssociation != "none" {
		formatted.WriteString(fmt.Sprintf(" (%s)", *comment.AuthorAssociation))
	}

	formatted.WriteString(fmt.Sprintf(" - %s\n", comment.CreatedAt.Format("2006-01-02 15:04")))

	formatted.WriteString(fmt.Sprintf("\n%s\n", *comment.Body))

	return formatted.String()
}

// buildInstructions creates task-specific instructions
func (ctx workContext) buildInstructions() string {
	var instructions strings.Builder

	instructions.WriteString("\n\n## Your Task\n\n")
	instructions.WriteString(`Analyze this issue, ask clarifying questions, respond or react to comments, and/or create a solution. Follow these guidelines:

1. If needed to complete the task, use the text editor tool to examine the codebase structure and understand the implementation
2. If needed to complete the task, view relevant files to understand how the code works
3. If the requirements are unclear, do not guess. Comment on the issue to ask clarifying questions, and then stop. Do not make code changes if requirements are unclear.
3. If other developers' suggestions are unsafe or unwise based on common best practices, or if they violate the repository's coding guidelines, politely and professionally suggest alternatives. If the other developer insists, apply their suggestion.
4. If the requirements are clear, implement the actual solution code using the text editor tools - do not use placeholders or TODOs
    - Use str_replace for precise modifications to existing files
    - Use create for new files when needed
    - Use insert to add code at specific locations
5. If changes are made, create or update an existing pull request using create_pull_request with:
   - A clear commit message describing what was fixed
   - A descriptive PR title (if creating a new PR)
   - A comprehensive description of the changes
6. When updating a PR, maintain the original intent of fixing the issue

Workflow:
1. If needed, view files to understand the codebase
2. If needed, ask clarifying questions with post_comment instead of making changes. Clarify, do not guess
3. If needed, respond to feedback with post_comment
4. Make changes with text editor tools (view, str_replace, create, insert)
5. Create or update pull request with create_pull_request

Review all comments, reviews, and feedback carefully. Make sure to address each point raised using the appropriate text editor commands.

Use tools in parallel whenever possible.`)

	// List specific comments needing responses
	needsResponseIssueCommentIDs := []string{}
	for _, comment := range ctx.IssueCommentsRequiringResponses {
		needsResponseIssueCommentIDs = append(needsResponseIssueCommentIDs, strconv.FormatInt(*comment.ID, 10))
	}
	needsResponsePRCommentIDs := []string{}
	for _, comment := range ctx.PRCommentsRequiringResponses {
		needsResponsePRCommentIDs = append(needsResponsePRCommentIDs, strconv.FormatInt(*comment.ID, 10))
	}
	needsResponsePRReviewCommentIDs := []string{}
	for _, comment := range ctx.PRReviewCommentsRequiringResponses {
		needsResponsePRReviewCommentIDs = append(needsResponsePRReviewCommentIDs, strconv.FormatInt(*comment.ID, 10))
	}

	if len(needsResponseIssueCommentIDs) > 0 {
		instructions.WriteString(fmt.Sprintf("\n\nIssue comments requiring responses: %s", strings.Join(needsResponseIssueCommentIDs, ", ")))
	}
	if len(needsResponsePRCommentIDs) > 0 {
		instructions.WriteString(fmt.Sprintf("\n\nPR comments requiring responses: %s", strings.Join(needsResponsePRCommentIDs, ", ")))
	}
	if len(needsResponsePRReviewCommentIDs) > 0 {
		instructions.WriteString(fmt.Sprintf("\n\nPR review comments requiring responses: %s", strings.Join(needsResponsePRReviewCommentIDs, ", ")))
	}

	return instructions.String()
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

// sanitizeForPrompt removes or replaces characters that might interfere with prompt processing
func sanitizeForPrompt(s string) string {
	// Remove null bytes and other control characters
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	// Limit very long lines
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if len(line) > 500 {
			lines[i] = line[:500] + "..."
		}
	}

	return strings.Join(lines, "\n")
}

// extractKeywords extracts potential keywords from text for analysis
func extractKeywords(text string) []string {
	// Simple keyword extraction - in production this could be more sophisticated
	words := strings.Fields(strings.ToLower(text))
	keywords := make(map[string]bool)

	for _, word := range words {
		// Remove punctuation and filter by length
		word = strings.Trim(word, ".,!?;:\"'()[]{}*")
		if len(word) > 3 && len(word) < 20 {
			keywords[word] = true
		}
	}

	var result []string
	for keyword := range keywords {
		result = append(result, keyword)
	}

	return result
}
