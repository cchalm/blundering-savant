package task

import "github.com/google/go-github/v72/github"

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
