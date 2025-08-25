package workspace

import (
	"context"
	"fmt"

	"github.com/google/go-github/v72/github"
)

// githubPullRequestService is a wrapper around github.PullRequestsService.Create
type githubPullRequestService struct {
	prService    *github.PullRequestsService
	owner        string
	repo         string
	sourceBranch string
	targetBranch string
}

func NewGithubPullRequestService(
	prService *github.PullRequestsService,
	owner string,
	repo string,
	sourceBranch string,
	targetBranch string,
) githubPullRequestService {
	return githubPullRequestService{
		prService:    prService,
		owner:        owner,
		repo:         repo,
		sourceBranch: sourceBranch,
		targetBranch: targetBranch,
	}
}

func (gprs *githubPullRequestService) Create(ctx context.Context, title string, body string) error {
	pr := &github.NewPullRequest{
		Title: github.Ptr(title),
		Body:  github.Ptr(body),
		Head:  &gprs.sourceBranch,
		Base:  &gprs.targetBranch,
	}

	_, _, err := gprs.prService.Create(ctx, gprs.owner, gprs.repo, pr)
	if err != nil {
		return fmt.Errorf("failed to create pull request: %w", err)
	}
	return nil
}
