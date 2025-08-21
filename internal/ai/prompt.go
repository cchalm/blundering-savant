package ai

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/task"
)

//go:embed prompt_template.tmpl
var promptTemplate string

//go:embed system_prompt.md
var systemPrompt string

// LoadSystemPrompt returns the system prompt for the bot
func LoadSystemPrompt() (string, error) {
	return systemPrompt, nil
}

// GeneratePrompt generates a prompt for the given task
func GeneratePrompt(tsk task.Task) (string, error) {
	tmpl, err := template.New("prompt").Parse(promptTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse prompt template: %w", err)
	}

	data := promptData{
		Repository: repositoryData{
			Owner:       tsk.Issue.Owner,
			Name:        tsk.Issue.Repository,
			Language:    tsk.Repository.GetLanguage(),
			Description: tsk.Repository.GetDescription(),
		},
		Issue: issueData{
			Number:      tsk.Issue.GetNumber(),
			Title:       tsk.Issue.GetTitle(),
			Body:        tsk.Issue.GetBody(),
			State:       tsk.Issue.GetState(),
			User:        userData{Login: tsk.Issue.GetUser().GetLogin()},
			Assignees:   convertUsersToUserData(tsk.Issue.Assignees),
			Labels:      convertLabelsToLabelData(tsk.Issue.Labels),
			CreatedAt:   tsk.Issue.GetCreatedAt().Format("2006-01-02 15:04:05 UTC"),
			UpdatedAt:   tsk.Issue.GetUpdatedAt().Format("2006-01-02 15:04:05 UTC"),
		},
		CodebaseInfo: codebaseInfoData{
			MainLanguage:  tsk.CodebaseInfo.MainLanguage,
			FileTree:      tsk.CodebaseInfo.FileTree,
			ReadmeExcerpt: tsk.CodebaseInfo.ReadmeExcerpt,
		},
		StyleGuide: styleGuideData{
			Exists:  tsk.StyleGuide.Exists,
			Name:    tsk.StyleGuide.Name,
			Content: tsk.StyleGuide.Content,
		},
		IssueComments: convertCommentsToCommentData(tsk.IssueComments),
		HasPR:         tsk.PullRequest != nil,
	}

	if tsk.PullRequest != nil {
		data.PullRequest = pullRequestData{
			Number:    tsk.PullRequest.GetNumber(),
			Title:     tsk.PullRequest.GetTitle(),
			Body:      tsk.PullRequest.GetBody(),
			State:     tsk.PullRequest.GetState(),
			User:      userData{Login: tsk.PullRequest.GetUser().GetLogin()},
			CreatedAt: tsk.PullRequest.GetCreatedAt().Format("2006-01-02 15:04:05 UTC"),
			UpdatedAt: tsk.PullRequest.GetUpdatedAt().Format("2006-01-02 15:04:05 UTC"),
		}
		data.PRComments = convertCommentsToCommentData(tsk.PRComments)
		data.PRReviews = convertReviewsToReviewData(tsk.PRReviews)
		data.PRReviewCommentThreads = convertReviewCommentThreadsToData(tsk.PRReviewCommentThreads)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute prompt template: %w", err)
	}

	return buf.String(), nil
}

// Template data types

type promptData struct {
	Repository              repositoryData
	Issue                   issueData
	PullRequest             pullRequestData
	CodebaseInfo            codebaseInfoData
	StyleGuide              styleGuideData
	IssueComments           []commentData
	HasPR                   bool
	PRComments              []commentData
	PRReviews               []reviewData
	PRReviewCommentThreads  [][]reviewCommentData
}

type repositoryData struct {
	Owner       string
	Name        string
	Language    string
	Description string
}

type issueData struct {
	Number    int
	Title     string
	Body      string
	State     string
	User      userData
	Assignees []userData
	Labels    []labelData
	CreatedAt string
	UpdatedAt string
}

type pullRequestData struct {
	Number    int
	Title     string
	Body      string
	State     string
	User      userData
	CreatedAt string
	UpdatedAt string
}

type codebaseInfoData struct {
	MainLanguage  string
	FileTree      string
	ReadmeExcerpt string
}

type styleGuideData struct {
	Exists  bool
	Name    string
	Content string
}

type userData struct {
	Login string
}

type labelData struct {
	Name        string
	Color       string
	Description string
}

type commentData struct {
	ID                int64
	Body              string
	User              userData
	AuthorAssociation string
	CreatedAt         string
	UpdatedAt         string
	IsEdited          bool
}

type reviewData struct {
	ID                int64
	Body              string
	User              userData
	AuthorAssociation string
	SubmittedAt       string
	State             string
}

type reviewCommentData struct {
	ID                  int64
	Body                string
	User                userData
	AuthorAssociation   string
	CreatedAt           string
	UpdatedAt           string
	IsEdited            bool
	Path                string
	Position            int
	OriginalPosition    int
	CommitID            string
	OriginalCommitID    string
	DiffHunk            string
	InReplyToID         int64
}

// Conversion functions

func convertUsersToUserData(users []*github.User) []userData {
	var result []userData
	for _, user := range users {
		if user != nil {
			result = append(result, userData{Login: user.GetLogin()})
		}
	}
	return result
}

func convertLabelsToLabelData(labels []*github.Label) []labelData {
	var result []labelData
	for _, label := range labels {
		if label != nil {
			result = append(result, labelData{
				Name:        label.GetName(),
				Color:       label.GetColor(),
				Description: label.GetDescription(),
			})
		}
	}
	return result
}

func convertCommentsToCommentData(comments []*github.IssueComment) []commentData {
	var result []commentData
	for _, comment := range comments {
		if comment != nil {
			result = append(result, commentData{
				ID:                comment.GetID(),
				Body:              comment.GetBody(),
				User:              userData{Login: comment.GetUser().GetLogin()},
				AuthorAssociation: comment.GetAuthorAssociation(),
				CreatedAt:         comment.GetCreatedAt().Format("2006-01-02 15:04:05 UTC"),
				UpdatedAt:         comment.GetUpdatedAt().Format("2006-01-02 15:04:05 UTC"),
				IsEdited:          !comment.GetCreatedAt().Equal(comment.GetUpdatedAt()),
			})
		}
	}
	return result
}

func convertReviewsToReviewData(reviews []*github.PullRequestReview) []reviewData {
	var result []reviewData
	for _, review := range reviews {
		if review != nil {
			result = append(result, reviewData{
				ID:                review.GetID(),
				Body:              review.GetBody(),
				User:              userData{Login: review.GetUser().GetLogin()},
				AuthorAssociation: review.GetAuthorAssociation(),
				SubmittedAt:       review.GetSubmittedAt().Time.Format("2006-01-02 15:04:05 UTC"),
				State:             review.GetState(),
			})
		}
	}
	return result
}

func convertReviewCommentThreadsToData(threads [][]*github.PullRequestComment) [][]reviewCommentData {
	var result [][]reviewCommentData
	for _, thread := range threads {
		var threadData []reviewCommentData
		for _, comment := range thread {
			if comment != nil {
				threadData = append(threadData, reviewCommentData{
					ID:                  comment.GetID(),
					Body:                comment.GetBody(),
					User:                userData{Login: comment.GetUser().GetLogin()},
					AuthorAssociation:   comment.GetAuthorAssociation(),
					CreatedAt:           comment.GetCreatedAt().Format("2006-01-02 15:04:05 UTC"),
					UpdatedAt:           comment.GetUpdatedAt().Format("2006-01-02 15:04:05 UTC"),
					IsEdited:            !comment.GetCreatedAt().Equal(comment.GetUpdatedAt()),
					Path:                comment.GetPath(),
					Position:            comment.GetPosition(),
					OriginalPosition:    comment.GetOriginalPosition(),
					CommitID:            comment.GetCommitID(),
					OriginalCommitID:    comment.GetOriginalCommitID(),
					DiffHunk:            comment.GetDiffHunk(),
					InReplyToID:         comment.GetInReplyTo(),
				})
			}
		}
		if len(threadData) > 0 {
			result = append(result, threadData)
		}
	}
	return result
}