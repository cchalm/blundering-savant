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

//go:embed prompt_template.tmpl
var promptTemplate string

//go:embed repository_info_template.tmpl
var repositoryInfoTemplate string

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
	IssueNumber            int
	IssueTitle             string
	IssueBody              string
	PullRequestNumber      *int
	HasConversationHistory bool
	// Conversation data structures for template to format
	IssueComments                      []commentData
	PRComments                         []commentData
	PRReviewCommentThreads             []reviewCommentThreadData
	PRReviews                          []reviewData
	IssueCommentsRequiringResponses    []commentData
	PRCommentsRequiringResponses       []commentData
	PRReviewCommentsRequiringResponses []reviewCommentData
	HasUnpublishedChanges              bool
	ValidationResult                   ValidationResult
}

// BuildRepositoryInfo generates the repository-specific content block for Claude
func BuildRepositoryInfo(tsk task) (*string, error) {
	data := buildRepositoryTemplateData(tsk)

	// Create template with helper functions
	funcMap := template.FuncMap{
		"indent": func(prefix string, text string) string {
			prefixed := strings.Builder{}
			for line := range strings.Lines(text) {
				prefixed.WriteString(prefix)
				prefixed.WriteString(line)
			}
			return prefixed.String()
		},
	}

	tmpl, err := template.New("repository_info").Funcs(funcMap).Parse(repositoryInfoTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repository info template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("failed to execute repository info template: %w", err)
	}

	result := buf.String()
	return &result, nil
}

// BuildPrompt generates the complete prompt for Claude based on the context
func BuildPrompt(tsk task) (*string, error) {
	data := buildTemplateData(tsk)

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
		"indent": func(prefix string, text string) string {
			prefixed := strings.Builder{}
			for line := range strings.Lines(text) {
				prefixed.WriteString(prefix)
				prefixed.WriteString(line)
			}
			return prefixed.String()
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

// BuildRepositoryInfo generates the repository-specific content block
func BuildRepositoryInfo(tsk task) (*string, error) {
	data := buildRepositoryTemplateData(tsk)

	// Create template with helper functions
	funcMap := template.FuncMap{
		"indent": func(prefix string, text string) string {
			prefixed := strings.Builder{}
			for line := range strings.Lines(text) {
				prefixed.WriteString(prefix)
				prefixed.WriteString(line)
			}
			return prefixed.String()
		},
	}

	tmpl, err := template.New("repository_info").Funcs(funcMap).Parse(repositoryInfoTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repository info template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("failed to execute repository info template: %w", err)
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

// repositoryTemplateData holds repository-specific data that can be cached
type repositoryTemplateData struct {
	Repository             string
	MainLanguage           string
	StyleGuides            map[string]string // path -> content
	ReadmeContent          string
	FileTree               []string
	FileTreeTruncatedCount int // The number of files that were truncated from the file tree to cap length
}

// buildRepositoryTemplateData creates the repository-specific data structure for template rendering
func buildRepositoryTemplateData(tsk task) repositoryTemplateData {
	data := repositoryTemplateData{}

	// Basic repository information
	if tsk.Repository != nil && tsk.Repository.FullName != nil {
		data.Repository = *tsk.Repository.FullName
	} else {
		data.Repository = "unknown"
	}

	if tsk.CodebaseInfo != nil {
		data.MainLanguage = tsk.CodebaseInfo.MainLanguage
	}
	if data.MainLanguage == "" {
		data.MainLanguage = "unknown"
	}

	// Style guides
	if tsk.StyleGuide != nil {
		data.StyleGuides = tsk.StyleGuide.Guides
	}

	// Codebase information
	if tsk.CodebaseInfo != nil {
		if tsk.CodebaseInfo.ReadmeContent != "" {
			data.ReadmeContent = truncateString(tsk.CodebaseInfo.ReadmeContent, 1000)
		}

		if len(tsk.CodebaseInfo.FileTree) > 0 {
			maxFiles := 1000
			if len(tsk.CodebaseInfo.FileTree) > maxFiles {
				data.FileTree = tsk.CodebaseInfo.FileTree[:maxFiles]
				data.FileTreeTruncatedCount = len(tsk.CodebaseInfo.FileTree) - maxFiles
			} else {
				data.FileTree = tsk.CodebaseInfo.FileTree
			}
		}
	}

	return data
}

// buildTemplateData creates the data structure for template rendering
func buildTemplateData(tsk task) promptTemplateData {
	data := promptTemplateData{}

	// Issue information
	data.IssueNumber = tsk.Issue.number
	data.IssueTitle = tsk.Issue.title
	data.IssueBody = tsk.Issue.body

	// Pull request information
	if tsk.PullRequest != nil {
		data.PullRequestNumber = &tsk.PullRequest.number
	}

	// Conversation history - convert GitHub types to template types
	if len(tsk.IssueComments) > 0 || len(tsk.PRComments) > 0 || len(tsk.PRReviewCommentThreads) > 0 || len(tsk.PRReviews) > 0 {
		data.HasConversationHistory = true

		// Convert issue comments
		for _, comment := range tsk.IssueComments {
			data.IssueComments = append(data.IssueComments, convertGitHubComment(comment))
		}

		// Convert PR comments
		for _, comment := range tsk.PRComments {
			data.PRComments = append(data.PRComments, convertGitHubComment(comment))
		}

		// Convert PR reviews
		for _, review := range tsk.PRReviews {
			data.PRReviews = append(data.PRReviews, convertGitHubReview(review))
		}

		// Convert PR review comment threads
		for _, thread := range tsk.PRReviewCommentThreads {
			var convertedThread reviewCommentThreadData
			for _, comment := range thread {
				convertedThread = append(convertedThread, convertGitHubReviewComment(comment))
			}
			data.PRReviewCommentThreads = append(data.PRReviewCommentThreads, convertedThread)
		}
	}

	// Comments requiring responses - convert to template types
	for _, comment := range tsk.IssueCommentsRequiringResponses {
		data.IssueCommentsRequiringResponses = append(data.IssueCommentsRequiringResponses, convertGitHubComment(comment))
	}

	for _, comment := range tsk.PRCommentsRequiringResponses {
		data.PRCommentsRequiringResponses = append(data.PRCommentsRequiringResponses, convertGitHubComment(comment))
	}

	for _, comment := range tsk.PRReviewCommentsRequiringResponses {
		data.PRReviewCommentsRequiringResponses = append(data.PRReviewCommentsRequiringResponses, convertGitHubReviewComment(comment))
	}

	data.HasUnpublishedChanges = tsk.HasUnpublishedChanges
	data.ValidationResult = tsk.ValidationResult

	return data
}

// buildRepositoryTemplateData creates the data structure for repository info template rendering
func buildRepositoryTemplateData(tsk task) repositoryInfoTemplateData {
	data := repositoryInfoTemplateData{}

	// Basic repository information
	if tsk.Repository != nil && tsk.Repository.FullName != nil {
		data.Repository = *tsk.Repository.FullName
	} else {
		data.Repository = "unknown"
	}

	if tsk.CodebaseInfo != nil {
		data.MainLanguage = tsk.CodebaseInfo.MainLanguage
	}
	if data.MainLanguage == "" {
		data.MainLanguage = "unknown"
	}

	// Style guides
	if tsk.StyleGuide != nil {
		data.StyleGuides = tsk.StyleGuide.Guides
	}

	// Codebase information
	if tsk.CodebaseInfo != nil {
		if tsk.CodebaseInfo.ReadmeContent != "" {
			data.ReadmeContent = truncateString(tsk.CodebaseInfo.ReadmeContent, 1000)
		}

		if len(tsk.CodebaseInfo.FileTree) > 0 {
			maxFiles := 1000
			if len(tsk.CodebaseInfo.FileTree) > maxFiles {
				data.FileTree = tsk.CodebaseInfo.FileTree[:maxFiles]
				data.FileTreeTruncatedCount = len(tsk.CodebaseInfo.FileTree) - maxFiles
			} else {
				data.FileTree = tsk.CodebaseInfo.FileTree
			}
		}
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
