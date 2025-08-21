package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v72/github"
)

// PullRequestService handles GitHub pull request operations
type PullRequestService interface {
	CreatePullRequest(ctx context.Context, owner, repo, baseBranch, sourceBranch, title, body string) (*github.PullRequest, error)
	UpdatePullRequest(ctx context.Context, owner, repo string, prNumber int, title, body *string) (*github.PullRequest, error)
	GetPullRequestByBranch(ctx context.Context, owner, repo, branch string) (*github.PullRequest, error)
}

// pullRequestService implements PullRequestService using GitHub API
type pullRequestService struct {
	client *github.Client
}

// NewPullRequestService creates a new PullRequestService
func NewPullRequestService(client *github.Client) PullRequestService {
	return &pullRequestService{
		client: client,
	}
}

func (prs *pullRequestService) CreatePullRequest(ctx context.Context, owner, repo, baseBranch, sourceBranch, title, body string) (*github.PullRequest, error) {
	newPR := &github.NewPullRequest{
		Title:               github.Ptr(title),
		Head:                github.Ptr(sourceBranch),
		Base:                github.Ptr(baseBranch),
		Body:                github.Ptr(body),
		MaintainerCanModify: github.Ptr(true),
	}

	pr, _, err := prs.client.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	return pr, nil
}

func (prs *pullRequestService) UpdatePullRequest(ctx context.Context, owner, repo string, prNumber int, title, body *string) (*github.PullRequest, error) {
	pr := &github.PullRequest{
		Title: title,
		Body:  body,
	}

	updatedPR, _, err := prs.client.PullRequests.Edit(ctx, owner, repo, prNumber, pr)
	if err != nil {
		return nil, fmt.Errorf("failed to update pull request: %w", err)
	}

	return updatedPR, nil
}

func (prs *pullRequestService) GetPullRequestByBranch(ctx context.Context, owner, repo, branch string) (*github.PullRequest, error) {
	opts := &github.PullRequestListOptions{
		Head:        fmt.Sprintf("%s:%s", owner, branch),
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	prs_list, _, err := prs.client.PullRequests.List(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list pull requests: %w", err)
	}

	for _, pr := range prs_list {
		if strings.EqualFold(pr.GetHead().GetRef(), branch) {
			return pr, nil
		}
	}

	return nil, nil // No PR found
}