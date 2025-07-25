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

//go:embed prompt_template.txt
var promptTemplate string

// Custom types for template data to avoid pointer dereferencing in templates

// userData represents a user in template data
type userData struct {
	Login string
}

// commentData represents a comment in template data
type commentData struct {
	ID                int64
	Body              string
	User              userData
	AuthorAssociation string
	CreatedAt         string
	UpdatedAt         string
	IsEdited          bool
}

// reviewData represents a PR review in template data
type reviewData struct {
	ID                int64
	Body              string
	User              userData
	AuthorAssociation string
	SubmittedAt       string
	State             string
}

// reviewCommentData represents a PR review comment in template data
type reviewCommentData struct {
	ID                  int64
	Body                string
	User                userData
	AuthorAssociation   string
	CreatedAt           string
	Path                string
	Line                *int
	StartLine           *int
	DiffHunk            string
	PullRequestReviewID *int64
}

// reviewCommentThreadData represents a thread of PR review comments
type reviewCommentThreadData []reviewCommentData

// promptTemplateData holds the data used to render the prompt template
type promptTemplateData struct {
	Repository             string
	MainLanguage           string
	IssueNumber            int
	IssueTitle             string
	IssueBody              string
	PullRequestNumber      *int
	StyleGuideContent      string
	ReadmeContent          string
	FileTree               []string
	FileTreeTruncated      bool
	HasConversationHistory bool
	// Conversation data structures for template to format
	IssueComments                      []commentData
	PRComments                         []commentData
	PRReviewCommentThreads             []reviewCommentThreadData
	PRReviews                          []reviewData
	BotUsername                        string
	IssueCommentsRequiringResponses    []commentData
	PRCommentsRequiringResponses       []commentData
	PRReviewCommentsRequiringResponses []reviewCommentData
}

// BuildPrompt generates the complete prompt for Claude based on the context
func BuildPrompt(ctx workContext) (*string, error) {
	data := buildTemplateData(ctx)

	// Create template with helper functions
	funcMap := template.FuncMap{
		"commentIDs": func(comments interface{}) string {
			switch c := comments.(type) {
			case []commentData:
				var ids []string
				for _, comment := range c {
					ids = append(ids, strconv.FormatInt(comment.ID, 10))
				}
				return strings.Join(ids, ", ")
			case []reviewCommentData:
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

func convertGitHubUser(user *github.User) userData {
	if user == nil || user.Login == nil {
		return userData{Login: "unknown"}
	}
	return userData{Login: *user.Login}
}

func convertGitHubComment(comment *github.IssueComment) commentData {
	if comment == nil {
		return commentData{}
	}

	tc := commentData{
		Body:              derefOr(comment.Body, ""),
		User:              convertGitHubUser(comment.User),
		AuthorAssociation: derefOr(comment.AuthorAssociation, "<none>"),
	}

	if comment.ID != nil {
		tc.ID = *comment.ID
	}

	if comment.CreatedAt != nil {
		tc.CreatedAt = comment.CreatedAt.Format("2006-01-02 15:04")
	}

	if comment.UpdatedAt != nil {
		tc.UpdatedAt = comment.UpdatedAt.Format("2006-01-02 15:04")
		tc.IsEdited = comment.CreatedAt != nil && !comment.CreatedAt.Time.Equal(comment.UpdatedAt.Time)
	}

	return tc
}

func convertGitHubReview(review *github.PullRequestReview) reviewData {
	if review == nil {
		return reviewData{}
	}

	tr := reviewData{
		Body:              derefOr(review.Body, ""),
		User:              convertGitHubUser(review.User),
		AuthorAssociation: derefOr(review.AuthorAssociation, "<none>"),
		State:             derefOr(review.State, ""),
	}

	if review.ID != nil {
		tr.ID = *review.ID
	}

	if review.SubmittedAt != nil {
		tr.SubmittedAt = review.SubmittedAt.Format("2006-01-02 15:04")
	}

	return tr
}

func convertGitHubReviewComment(comment *github.PullRequestComment) reviewCommentData {
	if comment == nil {
		return reviewCommentData{}
	}

	trc := reviewCommentData{
		Body:              derefOr(comment.Body, ""),
		User:              convertGitHubUser(comment.User),
		AuthorAssociation: derefOr(comment.AuthorAssociation, "<none>"),
		Path:              derefOr(comment.Path, ""),
		DiffHunk:          derefOr(comment.DiffHunk, ""),
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

func derefOr[T any](ptr *T, defaultVal T) T {
	if ptr == nil {
		return defaultVal
	}
	return *ptr
}

// buildTemplateData creates the data structure for template rendering
func buildTemplateData(ctx workContext) promptTemplateData {
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
			var convertedThread reviewCommentThreadData
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

// truncateString truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
