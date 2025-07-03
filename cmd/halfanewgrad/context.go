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

//go:embed prompt_template.txt
var promptTemplate string

// Custom types for template data to avoid pointer dereferencing in templates

// TemplateUser represents a user in template data
type TemplateUser struct {
	Login string
}

// TemplateComment represents a comment in template data
type TemplateComment struct {
	ID                  int64
	Body                string
	User                TemplateUser
	AuthorAssociation   string
	CreatedAt           string
	UpdatedAt           string
	IsEdited            bool
}

// TemplateReview represents a PR review in template data
type TemplateReview struct {
	ID                int64
	Body              string
	User              TemplateUser
	AuthorAssociation string
	SubmittedAt       string
	State             string
}

// TemplateReviewComment represents a PR review comment in template data
type TemplateReviewComment struct {
	ID                    int64
	Body                  string
	User                  TemplateUser
	AuthorAssociation     string
	CreatedAt             string
	Path                  string
	Line                  *int
	StartLine             *int
	DiffHunk              string
	PullRequestReviewID   *int64
}

// TemplateReviewCommentThread represents a thread of PR review comments
type TemplateReviewCommentThread []TemplateReviewComment

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
	// Conversation data structures for template to format
	IssueComments                        []TemplateComment
	PRComments                           []TemplateComment
	PRReviewCommentThreads              []TemplateReviewCommentThread
	PRReviews                           []TemplateReview
	BotUsername                         string
	IssueCommentsRequiringResponses      []TemplateComment
	PRCommentsRequiringResponses         []TemplateComment
	PRReviewCommentsRequiringResponses   []TemplateReviewComment
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
func (ctx workContext) BuildPrompt() (*string, error) {
	data := ctx.buildTemplateData()
	
	// Create template with helper functions
	funcMap := template.FuncMap{
		"commentIDs": func(comments interface{}) string {
			switch c := comments.(type) {
			case []TemplateComment:
				var ids []string
				for _, comment := range c {
					ids = append(ids, strconv.FormatInt(comment.ID, 10))
				}
				return strings.Join(ids, ", ")
			case []TemplateReviewComment:
				var ids []string
				for _, comment := range c {
					ids = append(ids, strconv.FormatInt(comment.ID, 10))
				}
				return strings.Join(ids, ", ")
			default:
				return ""
			}
		},
		"truncateDiff": func(diff string) string {
			if len(diff) > 1000 {
				return fmt.Sprintf("<Large diff (%d bytes) omitted>", len(diff))
			}
			return diff
		},
	}
	
	tmpl, err := template.New("prompt").Funcs(funcMap).Parse(promptTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse prompt template: %w", err)
	}
	
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("failed to execute prompt template: %w", err)
	}
	
	result := buf.String()
	return &result, nil
}

// Helper functions to convert GitHub types to template types

func convertGitHubUser(user *github.User) TemplateUser {
	if user == nil || user.Login == nil {
		return TemplateUser{Login: "unknown"}
	}
	return TemplateUser{Login: *user.Login}
}

func convertGitHubComment(comment *github.IssueComment) TemplateComment {
	if comment == nil {
		return TemplateComment{}
	}
	
	tc := TemplateComment{
		Body: safeDeref(comment.Body, ""),
		User: convertGitHubUser(comment.User),
		AuthorAssociation: safeDeref(comment.AuthorAssociation, "<none>"),
	}
	
	if comment.ID != nil {
		tc.ID = *comment.ID
	}
	
	if comment.CreatedAt != nil {
		tc.CreatedAt = comment.CreatedAt.Format("2006-01-02 15:04")
	}
	
	if comment.UpdatedAt != nil {
		tc.UpdatedAt = comment.UpdatedAt.Format("2006-01-02 15:04")
		tc.IsEdited = comment.CreatedAt != nil && !comment.CreatedAt.Equal(comment.UpdatedAt.Time)
	}
	
	return tc
}

func convertGitHubReview(review *github.PullRequestReview) TemplateReview {
	if review == nil {
		return TemplateReview{}
	}
	
	tr := TemplateReview{
		Body: safeDeref(review.Body, ""),
		User: convertGitHubUser(review.User),
		AuthorAssociation: safeDeref(review.AuthorAssociation, "<none>"),
		State: safeDeref(review.State, ""),
	}
	
	if review.ID != nil {
		tr.ID = *review.ID
	}
	
	if review.SubmittedAt != nil {
		tr.SubmittedAt = review.SubmittedAt.Format("2006-01-02 15:04")
	}
	
	return tr
}

func convertGitHubReviewComment(comment *github.PullRequestComment) TemplateReviewComment {
	if comment == nil {
		return TemplateReviewComment{}
	}
	
	trc := TemplateReviewComment{
		Body: safeDeref(comment.Body, ""),
		User: convertGitHubUser(comment.User),
		AuthorAssociation: safeDeref(comment.AuthorAssociation, "<none>"),
		Path: safeDeref(comment.Path, ""),
		DiffHunk: safeDeref(comment.DiffHunk, ""),
	}
	
	if comment.ID != nil {
		trc.ID = *comment.ID
	}
	
	if comment.CreatedAt != nil {
		trc.CreatedAt = comment.CreatedAt.Format("2006-01-02 15:04")
	}
	
	// These remain as pointers since they can be legitimately nil
	trc.Line = comment.Line
	trc.StartLine = comment.StartLine
	trc.PullRequestReviewID = comment.PullRequestReviewID
	
	return trc
}

func safeDeref[T any](ptr *T, defaultVal T) T {
	if ptr == nil {
		return defaultVal
	}
	return *ptr
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
	
	// Conversation history - convert GitHub types to template types
	if len(ctx.IssueComments) > 0 || len(ctx.PRComments) > 0 || len(ctx.PRReviewCommentThreads) > 0 || len(ctx.PRReviews) > 0 {
		data.HasConversationHistory = true
		data.BotUsername = ctx.BotUsername
		
		// Convert issue comments
		for _, comment := range ctx.IssueComments {
			data.IssueComments = append(data.IssueComments, convertGitHubComment(comment))
		}
		
		// Convert PR comments
		for _, comment := range ctx.PRComments {
			data.PRComments = append(data.PRComments, convertGitHubComment(comment))
		}
		
		// Convert PR reviews
		for _, review := range ctx.PRReviews {
			data.PRReviews = append(data.PRReviews, convertGitHubReview(review))
		}
		
		// Convert PR review comment threads
		for _, thread := range ctx.PRReviewCommentThreads {
			var convertedThread TemplateReviewCommentThread
			for _, comment := range thread {
				convertedThread = append(convertedThread, convertGitHubReviewComment(comment))
			}
			data.PRReviewCommentThreads = append(data.PRReviewCommentThreads, convertedThread)
		}
	}
	
	// Comments requiring responses - convert to template types
	for _, comment := range ctx.IssueCommentsRequiringResponses {
		data.IssueCommentsRequiringResponses = append(data.IssueCommentsRequiringResponses, convertGitHubComment(comment))
	}
	
	for _, comment := range ctx.PRCommentsRequiringResponses {
		data.PRCommentsRequiringResponses = append(data.PRCommentsRequiringResponses, convertGitHubComment(comment))
	}
	
	for _, comment := range ctx.PRReviewCommentsRequiringResponses {
		data.PRReviewCommentsRequiringResponses = append(data.PRReviewCommentsRequiringResponses, convertGitHubReviewComment(comment))
	}
	
	
	return data
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
