package ai

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/task"
)

//go:embed conversation_template.tmpl
var conversationTemplate string

// GenerateConversationMarkdown generates markdown representation of a conversation for a task
func GenerateConversationMarkdown(tsk task.Task) (string, error) {
	tmpl, err := template.New("conversation").Parse(conversationTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse conversation template: %w", err)
	}

	data := conversationData{
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
		return "", fmt.Errorf("failed to execute conversation template: %w", err)
	}

	return buf.String(), nil
}

type conversationData struct {
	Repository              repositoryData
	Issue                   issueData
	PullRequest             pullRequestData
	IssueComments           []commentData
	HasPR                   bool
	PRComments              []commentData
	PRReviews               []reviewData
	PRReviewCommentThreads  [][]reviewCommentData
}