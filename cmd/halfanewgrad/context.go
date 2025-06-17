package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"text/template"

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

//go:embed prompt_template.txt
var promptTemplate string

// promptTemplateData holds the data used to render the prompt template
type promptTemplateData struct {
	Repository                           string
	MainLanguage                         string
	IssueNumber                          int
	IssueTitle                           string
	IssueBody                            string
	PullRequestNumber                    *int
	StyleGuideContent                    string
	ReadmeContent                        string
	FileTree                             []string
	FileTreeTruncated                    bool
	HasConversationHistory               bool
	ConversationHistory                  string
	IssueCommentsRequiringResponses      string
	PRCommentsRequiringResponses         string
	PRReviewCommentsRequiringResponses   string
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
	data := ctx.buildTemplateData()
	
	tmpl, err := template.New("prompt").Parse(promptTemplate)
	if err != nil {
		// Fallback to basic prompt if template parsing fails
		return fmt.Sprintf("Error parsing prompt template: %v\n\nFallback prompt:\nRepository: %s\nIssue: %s", 
			data.Repository, data.IssueTitle)
	}
	
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		// Fallback to basic prompt if template execution fails
		return fmt.Sprintf("Error executing prompt template: %v\n\nFallback prompt:\nRepository: %s\nIssue: %s", 
			data.Repository, data.IssueTitle)
	}
	
	return buf.String()
}

// buildTemplateData creates the data structure for template rendering
func (ctx workContext) buildTemplateData() promptTemplateData {
	data := promptTemplateData{}
	
	// Basic repository and issue information
	if ctx.Repository != nil && ctx.Repository.FullName != nil {
		data.Repository = *ctx.Repository.FullName
	} else {
		data.Repository = "unknown"
	}
	
	if ctx.CodebaseInfo != nil {
		data.MainLanguage = ctx.CodebaseInfo.MainLanguage
	}
	if data.MainLanguage == "" {
		data.MainLanguage = "unknown"
	}
	
	if ctx.Issue != nil {
		if ctx.Issue.Number != nil {
			data.IssueNumber = *ctx.Issue.Number
		}
		if ctx.Issue.Title != nil {
			data.IssueTitle = *ctx.Issue.Title
		} else {
			data.IssueTitle = "No title"
		}
		if ctx.Issue.Body != nil {
			data.IssueBody = *ctx.Issue.Body
		} else {
			data.IssueBody = "No description provided"
		}
	}
	
	// Pull request information
	if ctx.PullRequest != nil && ctx.PullRequest.Number != nil {
		data.PullRequestNumber = ctx.PullRequest.Number
	}
	
	// Style guide content
	if ctx.StyleGuide != nil && ctx.StyleGuide.Content != "" {
		data.StyleGuideContent = ctx.StyleGuide.Content
	}
	
	// Codebase information
	if ctx.CodebaseInfo != nil {
		if ctx.CodebaseInfo.ReadmeContent != "" {
			data.ReadmeContent = truncateString(ctx.CodebaseInfo.ReadmeContent, 1000)
		}
		
		if len(ctx.CodebaseInfo.FileTree) > 0 {
			maxFiles := 20
			if len(ctx.CodebaseInfo.FileTree) > maxFiles {
				data.FileTree = ctx.CodebaseInfo.FileTree[:maxFiles]
				data.FileTreeTruncated = true
			} else {
				data.FileTree = ctx.CodebaseInfo.FileTree
			}
		}
	}
	
	// Conversation history
	if len(ctx.IssueComments) > 0 || len(ctx.PRComments) > 0 || len(ctx.PRReviewCommentThreads) > 0 || len(ctx.PRReviews) > 0 {
		data.HasConversationHistory = true
		data.ConversationHistory = ctx.buildConversationContext()
	}
	
	// Comments requiring responses
	if len(ctx.IssueCommentsRequiringResponses) > 0 {
		var commentIDs []string
		for _, comment := range ctx.IssueCommentsRequiringResponses {
			commentIDs = append(commentIDs, strconv.FormatInt(*comment.ID, 10))
		}
		data.IssueCommentsRequiringResponses = strings.Join(commentIDs, ", ")
	}
	
	if len(ctx.PRCommentsRequiringResponses) > 0 {
		var commentIDs []string
		for _, comment := range ctx.PRCommentsRequiringResponses {
			commentIDs = append(commentIDs, strconv.FormatInt(*comment.ID, 10))
		}
		data.PRCommentsRequiringResponses = strings.Join(commentIDs, ", ")
	}
	
	if len(ctx.PRReviewCommentsRequiringResponses) > 0 {
		var commentIDs []string
		for _, comment := range ctx.PRReviewCommentsRequiringResponses {
			commentIDs = append(commentIDs, strconv.FormatInt(*comment.ID, 10))
		}
		data.PRReviewCommentsRequiringResponses = strings.Join(commentIDs, ", ")
	}
	
	return data
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
		reviewStr := fmt.Sprintf("\n### PR Review %d by @%s (%s) - %s\n",
			*review.ID,
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
		if topComment.Line != nil {
			if topComment.StartLine != nil {
				formatted.WriteString(fmt.Sprintf(" (lines %d-%d)", *topComment.StartLine, *topComment.Line))
			} else {
				formatted.WriteString(fmt.Sprintf(" (line %d)", *topComment.Line))
			}
		}

		if topComment.DiffHunk != nil {
			if len(*topComment.DiffHunk) > 1000 {
				formatted.WriteString(fmt.Sprintf("\n<Large diff (%d bytes) omitted>\n", len(*topComment.DiffHunk)))
			} else {
				formatted.WriteString(fmt.Sprintf("\n```diff\n%s\n```\n", *topComment.DiffHunk))
			}
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
	if comment.PullRequestReviewID != nil {
		formatted.WriteString(fmt.Sprintf(" in Review %d", *comment.PullRequestReviewID))
	}

	formatted.WriteString(fmt.Sprintf(" - %s\n", comment.CreatedAt.Format("2006-01-02 15:04")))

	formatted.WriteString(fmt.Sprintf("\n%s\n", *comment.Body))

	return formatted.String()
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
