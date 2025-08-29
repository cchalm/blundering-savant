package task

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v72/github"
)

type GithubIssue struct {
	Owner  string
	Repo   string
	Number int

	Title string
	Body  string
	URL   string

	Labels []string
}

type GithubPullRequest struct {
	Owner  string
	Repo   string
	Number int

	Title string
	URL   string

	BaseBranch string
}

var (
	LabelWorking = github.Label{
		Name:        github.Ptr("bot-working"),
		Description: github.Ptr("the bot is actively working on this issue"),
		Color:       github.Ptr("fbca04"),
	}
	LabelBlocked = github.Label{
		Name:        github.Ptr("bot-blocked"),
		Description: github.Ptr("the bot encountered a problem and needs human intervention to continue working on this issue"),
		Color:       github.Ptr("f03010"),
	}
	LabelBotTurn = github.Label{
		Name:        github.Ptr("bot-turn"),
		Description: github.Ptr("it is the bot's turn to take action on this issue"),
		Color:       github.Ptr("2020f0"),
	}
)

func convertIssue(issue *github.Issue) (GithubIssue, error) {
	if issue == nil || issue.RepositoryURL == nil || issue.Number == nil || issue.Title == nil || issue.URL == nil {
		return GithubIssue{}, fmt.Errorf("unexpected nil")
	}

	// Extract owner and repo
	parts := strings.Split(*issue.RepositoryURL, "/")
	if len(parts) < 2 {
		return GithubIssue{}, fmt.Errorf("failed to parse repo URL '%s'", *issue.RepositoryURL)
	}
	owner := parts[len(parts)-2]
	repo := parts[len(parts)-1]

	// Convert labels into a list of strings
	labels := []string{}
	for _, label := range issue.Labels {
		labels = append(labels, *label.Name)
	}

	return GithubIssue{
		Owner:  owner,
		Repo:   repo,
		Number: *issue.Number,

		Title: *issue.Title,
		Body:  issue.GetBody(),
		URL:   *issue.URL,

		Labels: labels,
	}, nil
}
