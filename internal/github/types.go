package github

type GitHubIssue struct {
	Owner  string
	Repo   string
	Number int

	Title string
	Body  string
	URL   string

	Labels []string
}

type GitHubPullRequest struct {
	Owner  string
	Repo   string
	Number int

	Title   string
	Body    string
	URL     string
	HeadSHA string

	Labels []string
}