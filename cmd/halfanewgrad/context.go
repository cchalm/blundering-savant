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
	// Conversation data structures for template to format
	IssueComments                        []*github.IssueComment
	PRComments                           []*github.IssueComment
	PRReviewCommentThreads              [][]*github.PullRequestComment
	PRReviews                           []*github.PullRequestReview
	BotUsername                         string
	IssueCommentsRequiringResponses      []*github.IssueComment
	PRCommentsRequiringResponses         []*github.IssueComment
	PRReviewCommentsRequiringResponses   []*github.PullRequestComment
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
			case []*github.IssueComment:
				var ids []string
				for _, comment := range c {
					if comment.ID != nil {
						ids = append(ids, strconv.FormatInt(*comment.ID, 10))
					}
				}
				return strings.Join(ids, ", ")
			case []*github.PullRequestComment:
				var ids []string
				for _, comment := range c {
					if comment.ID != nil {
						ids = append(ids, strconv.FormatInt(*comment.ID, 10))
					}
				}
				return strings.Join(ids, ", ")
			default:
				return ""
			}
		},
		"formatTime": func(t interface{}) string {
			switch v := t.(type) {
			case *github.Timestamp:
				if v != nil {
					return v.Format("2006-01-02 15:04")
				}
			}
			return ""
		},
		"truncateDiff": func(diff *string) string {
			if diff == nil {
				return ""
			}
			if len(*diff) > 1000 {
				return fmt.Sprintf("<Large diff (%d bytes) omitted>", len(*diff))
			}
			return *diff
		},
		"deref": func(ptr interface{}) interface{} {
			switch v := ptr.(type) {
			case *string:
				if v != nil {
					return *v
				}
				return ""
			case *int:
				if v != nil {
					return *v
				}
				return 0
			case *int64:
				if v != nil {
					return *v
				}
				return int64(0)
			}
			return ptr
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
	
	// Conversation history - pass raw data structures to template
	if len(ctx.IssueComments) > 0 || len(ctx.PRComments) > 0 || len(ctx.PRReviewCommentThreads) > 0 || len(ctx.PRReviews) > 0 {
		data.HasConversationHistory = true
		data.IssueComments = ctx.IssueComments
		data.PRComments = ctx.PRComments
		data.PRReviewCommentThreads = ctx.PRReviewCommentThreads
		data.PRReviews = ctx.PRReviews
		data.BotUsername = ctx.BotUsername
	}
	
	// Comments requiring responses
	data.IssueCommentsRequiringResponses = ctx.IssueCommentsRequiringResponses
	data.PRCommentsRequiringResponses = ctx.PRCommentsRequiringResponses
	data.PRReviewCommentsRequiringResponses = ctx.PRReviewCommentsRequiringResponses
	
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
