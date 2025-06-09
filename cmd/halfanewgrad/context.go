package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"
)

// WorkContext represents all the context needed for the bot to generate solutions
type WorkContext struct {
	// Core entities
	Issue       *github.Issue
	Repository  *github.Repository
	PullRequest *github.PullRequest // May be nil for initial solution

	// Code context
	StyleGuide   *StyleGuide
	CodebaseInfo *CodebaseInfo

	// Conversation context
	IssueComments    []CommentContext
	PRReviewComments []ReviewCommentContext
	PRReviews        []ReviewContext

	// Current work state
	IsInitialSolution bool
	NeedsToRespond    map[string]bool // Comment ID -> needs response

	// Continuation context for multi-turn conversations
	ContinuationContext string
	ConversationHistory []ConversationTurn

	// Configuration
	BotUsername string
}

// ConversationTurn represents a turn in the conversation with the AI
type ConversationTurn struct {
	Timestamp time.Time
	Type      string // "user_action", "ai_decision", "tool_result"
	Content   string
}

// CommentContext represents a comment with full context
type CommentContext struct {
	ID            int64
	Author        string
	AuthorType    string // "bot", "owner", "member", "contributor", "none"
	Body          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	IsEdited      bool
	Reactions     map[string]int // emoji -> count
	InReplyTo     *int64         // ID of parent comment if this is a reply
	CommentType   string         // "issue", "pr"
	NeedsResponse bool
}

// ReviewCommentContext represents a code review comment with context
type ReviewCommentContext struct {
	CommentContext
	FilePath   string
	Line       int
	StartLine  *int   // For multi-line comments
	Side       string // "LEFT" or "RIGHT"
	DiffHunk   string
	CommitID   string
	ReviewID   *int64
	ReplyChain []CommentContext // Replies to this comment
}

// ReviewContext represents a full PR review
type ReviewContext struct {
	ID          int64
	Author      string
	AuthorType  string
	State       string // "APPROVED", "CHANGES_REQUESTED", "COMMENTED", "PENDING"
	Body        string
	SubmittedAt time.Time
	Comments    []ReviewCommentContext
}

// NewWorkContext creates a new work context
func NewWorkContext(botUsername string) *WorkContext {
	return &WorkContext{
		BotUsername:    botUsername,
		NeedsToRespond: make(map[string]bool),
	}
}

// BuildPrompt generates the complete prompt for Claude based on the context
func (ctx *WorkContext) BuildPrompt() string {
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
	if len(ctx.IssueComments) > 0 || len(ctx.PRReviewComments) > 0 || len(ctx.PRReviews) > 0 {
		prompt.WriteString("\n\n## Conversation History\n")
		prompt.WriteString(ctx.buildConversationContext())
	}

	// Instructions based on state
	prompt.WriteString(ctx.buildInstructions())

	return prompt.String()
}

// buildBasicInfo creates the basic issue/PR information section
func (ctx *WorkContext) buildBasicInfo() string {
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
func (ctx *WorkContext) buildCodebaseContext() string {
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
func (ctx *WorkContext) buildConversationContext() string {
	var timeline []string

	// Add issue comments
	for _, comment := range ctx.IssueComments {
		timeline = append(timeline, ctx.formatComment(comment, "Issue"))
	}

	// Add PR reviews and their comments
	for _, review := range ctx.PRReviews {
		// Add the main review
		reviewStr := fmt.Sprintf("\n### PR Review by @%s (%s) - %s\n",
			review.Author,
			review.AuthorType,
			review.SubmittedAt.Format("2006-01-02 15:04"))

		if review.State != "" {
			reviewStr += fmt.Sprintf("**Status: %s**\n", review.State)
		}

		if review.Body != "" {
			reviewStr += fmt.Sprintf("\n%s\n", review.Body)
		}

		timeline = append(timeline, reviewStr)

		// Add review comments
		for _, comment := range review.Comments {
			timeline = append(timeline, ctx.formatReviewComment(comment))
		}
	}

	// Add standalone PR comments
	for _, comment := range ctx.PRReviewComments {
		if comment.ReviewID == nil {
			timeline = append(timeline, ctx.formatReviewComment(comment))
		}
	}

	// Sort chronologically
	// Note: In a real implementation, we'd attach timestamps to sort properly

	return strings.Join(timeline, "\n")
}

// formatComment formats a regular comment
func (ctx *WorkContext) formatComment(comment CommentContext, commentType string) string {
	var formatted strings.Builder

	formatted.WriteString(fmt.Sprintf("\n### %s Comment by @%s", commentType, comment.Author))

	if comment.AuthorType != "" && comment.AuthorType != "none" {
		formatted.WriteString(fmt.Sprintf(" (%s)", comment.AuthorType))
	}

	formatted.WriteString(fmt.Sprintf(" - %s\n", comment.CreatedAt.Format("2006-01-02 15:04")))

	if comment.IsEdited {
		formatted.WriteString("*(edited)*\n")
	}

	if comment.InReplyTo != nil {
		formatted.WriteString(fmt.Sprintf("*In reply to comment #%d*\n", *comment.InReplyTo))
	}

	formatted.WriteString(fmt.Sprintf("\n%s\n", comment.Body))

	// Add reactions if any
	if len(comment.Reactions) > 0 {
		formatted.WriteString("\nReactions: ")
		var reactions []string
		for emoji, count := range comment.Reactions {
			reactions = append(reactions, fmt.Sprintf("%s (%d)", emoji, count))
		}
		formatted.WriteString(strings.Join(reactions, ", "))
		formatted.WriteString("\n")
	}

	if comment.NeedsResponse && comment.Author != ctx.BotUsername {
		formatted.WriteString("\n**[Needs Response]**\n")
	}

	return formatted.String()
}

// formatReviewComment formats a code review comment
func (ctx *WorkContext) formatReviewComment(comment ReviewCommentContext) string {
	var formatted strings.Builder

	formatted.WriteString(fmt.Sprintf("\n### Code Comment on `%s`", comment.FilePath))
	if comment.Line > 0 {
		formatted.WriteString(fmt.Sprintf(" (line %d", comment.Line))
		if comment.StartLine != nil && *comment.StartLine != comment.Line {
			formatted.WriteString(fmt.Sprintf("-%d", comment.Line))
		}
		formatted.WriteString(")")
	}

	formatted.WriteString(fmt.Sprintf(" by @%s", comment.Author))

	if comment.AuthorType != "" && comment.AuthorType != "none" {
		formatted.WriteString(fmt.Sprintf(" (%s)", comment.AuthorType))
	}

	formatted.WriteString(fmt.Sprintf(" - %s\n", comment.CreatedAt.Format("2006-01-02 15:04")))

	if comment.DiffHunk != "" {
		formatted.WriteString(fmt.Sprintf("\n```diff\n%s\n```\n", comment.DiffHunk))
	}

	formatted.WriteString(fmt.Sprintf("\n%s\n", comment.Body))

	// Add reply chain if exists
	if len(comment.ReplyChain) > 0 {
		formatted.WriteString("\n**Replies:**\n")
		for _, reply := range comment.ReplyChain {
			formatted.WriteString(ctx.formatComment(reply, "Reply"))
		}
	}

	if comment.NeedsResponse && comment.Author != ctx.BotUsername {
		formatted.WriteString("\n**[Needs Response]**\n")
	}

	return formatted.String()
}

// buildInstructions creates task-specific instructions
func (ctx *WorkContext) buildInstructions() string {
	var instructions strings.Builder

	instructions.WriteString("\n\n## Your Task\n\n")

	if ctx.IsInitialSolution {
		instructions.WriteString(`Create a complete solution for this issue. Follow these guidelines:

1. Analyze the issue and any comments carefully
2. Create a descriptive branch name following the pattern: fix/issue-NUMBER-brief-description
3. Implement the actual solution code - do not use placeholders or TODOs
4. Include complete, working file contents
5. Write a clear commit message describing what was fixed
6. Provide a comprehensive description of the changes

Consider all comments and feedback provided on the issue when crafting your solution.`)
	} else {
		instructions.WriteString(`Update the solution based on the feedback provided. Follow these guidelines:

1. Address all feedback points comprehensively
2. Maintain the original intent of fixing the issue
3. Write a commit message that explains what was changed based on the feedback
4. Include a clear description of what updates were made
5. Respond to specific comments that need responses

Review all comments, reviews, and feedback carefully. Make sure to address each point raised.`)

		// List specific comments needing responses
		needsResponse := []string{}
		for id, needs := range ctx.NeedsToRespond {
			if needs {
				needsResponse = append(needsResponse, id)
			}
		}

		if len(needsResponse) > 0 {
			instructions.WriteString(fmt.Sprintf("\n\nComments requiring responses: %s", strings.Join(needsResponse, ", ")))
		}
	}

	instructions.WriteString(`

You must use the create_solution tool to provide your complete implementation. Ensure that:
- All file contents are complete and functional
- The solution directly addresses the issue and all feedback
- At least one file is modified or created`)

	return instructions.String()
}

// AnalyzeComments determines which comments need responses
func (ctx *WorkContext) AnalyzeComments() {
	// Reset the map
	ctx.NeedsToRespond = make(map[string]bool)

	// Check issue comments
	for i, comment := range ctx.IssueComments {
		if ctx.shouldRespondToComment(comment) {
			commentID := fmt.Sprintf("issue-comment-%d", comment.ID)
			ctx.NeedsToRespond[commentID] = true
			ctx.IssueComments[i].NeedsResponse = true
		}
	}

	// Check PR review comments
	for i, comment := range ctx.PRReviewComments {
		if ctx.shouldRespondToComment(comment.CommentContext) {
			commentID := fmt.Sprintf("review-comment-%d", comment.ID)
			ctx.NeedsToRespond[commentID] = true
			ctx.PRReviewComments[i].NeedsResponse = true
		}
	}

	// Check PR reviews
	for _, review := range ctx.PRReviews {
		if review.State == "CHANGES_REQUESTED" && review.Author != ctx.BotUsername {
			reviewID := fmt.Sprintf("review-%d", review.ID)
			ctx.NeedsToRespond[reviewID] = true
		}
	}
}

// shouldRespondToComment determines if a comment needs a response
func (ctx *WorkContext) shouldRespondToComment(comment CommentContext) bool {
	// Don't respond to our own comments
	if comment.Author == ctx.BotUsername {
		return false
	}

	// Respond to direct questions (simple heuristic)
	if strings.Contains(comment.Body, "?") {
		return true
	}

	// Respond to comments mentioning the bot
	if strings.Contains(comment.Body, "@"+ctx.BotUsername) {
		return true
	}

	// Respond to comments with certain keywords
	keywords := []string{"please", "could you", "can you", "would you", "fix", "change", "update"}
	lowerBody := strings.ToLower(comment.Body)
	for _, keyword := range keywords {
		if strings.Contains(lowerBody, keyword) {
			return true
		}
	}

	return false
}

// GetCommentResponses generates responses for comments that need them
func (ctx *WorkContext) GetCommentResponses() map[string]string {
	responses := make(map[string]string)

	// This would be enhanced with AI-generated responses
	// For now, return a placeholder
	for commentID := range ctx.NeedsToRespond {
		responses[commentID] = "I'll address this in my solution update."
	}

	return responses
}
