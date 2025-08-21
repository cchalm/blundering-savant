package task

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"

	"github.com/cchalm/blundering-savant/internal/config"
	githubpkg "github.com/cchalm/blundering-savant/internal/github"
)

// Generator generates tasks for the bot to work on
type Generator struct {
	config       config.Config
	githubClient *github.Client
}

// NewGenerator creates a new task generator
func NewGenerator(config config.Config, githubClient *github.Client) *Generator {
	return &Generator{
		config:       config,
		githubClient: githubClient,
	}
}

// Generate continuously generates tasks from assigned GitHub issues
func (tg *Generator) Generate(ctx context.Context) <-chan Task {
	tasks := make(chan Task)

	go func() {
		defer close(tasks)

		ticker := time.NewTicker(tg.config.CheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := tg.generateTasks(ctx, tasks); err != nil {
					log.Printf("Error generating tasks: %v", err)
				}
			}
		}
	}()

	return tasks
}

func (tg *Generator) generateTasks(ctx context.Context, tasks chan<- Task) error {
	// Search for issues assigned to the bot
	query := fmt.Sprintf("assignee:%s is:issue is:open", tg.config.GitHubUsername)
	searchResult, _, err := tg.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		return fmt.Errorf("failed to search for issues: %w", err)
	}

	for _, issue := range searchResult.Issues {
		task, err := tg.createTaskFromIssue(ctx, issue)
		if err != nil {
			log.Printf("Failed to create task from issue #%d: %v", issue.GetNumber(), err)
			continue
		}

		select {
		case tasks <- *task:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (tg *Generator) createTaskFromIssue(ctx context.Context, issue *github.Issue) (*Task, error) {
	repo := issue.GetRepository()
	if repo == nil {
		return nil, fmt.Errorf("issue has no repository")
	}

	owner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()
	issueNumber := issue.GetNumber()

	// Create GitHub issue wrapper
	githubIssue := githubpkg.GitHubIssue{
		Issue:      issue,
		Owner:      owner,
		Repository: repoName,
	}

	// Get repository details
	repository, _, err := tg.githubClient.Repositories.Get(ctx, owner, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	// Check for existing pull request
	var pullRequest *githubpkg.GitHubPullRequest
	sourceBranch := fmt.Sprintf("bot/issue-%d", issueNumber)
	
	prs, _, err := tg.githubClient.PullRequests.List(ctx, owner, repoName, &github.PullRequestListOptions{
		Head:  fmt.Sprintf("%s:%s", owner, sourceBranch),
		State: "open",
	})
	if err == nil && len(prs) > 0 {
		pullRequest = &githubpkg.GitHubPullRequest{
			PullRequest: prs[0],
			Owner:       owner,
			Repository:  repoName,
		}
	}

	// Get style guide
	styleGuide, err := tg.getStyleGuide(ctx, owner, repoName)
	if err != nil {
		log.Printf("Failed to get style guide: %v", err)
		styleGuide = &StyleGuide{Exists: false}
	}

	// Get codebase info
	codebaseInfo, err := tg.getCodebaseInfo(ctx, owner, repoName, repository)
	if err != nil {
		log.Printf("Failed to get codebase info: %v", err)
		codebaseInfo = &CodebaseInfo{}
	}

	// Get issue comments
	issueComments, _, err := tg.githubClient.Issues.ListComments(ctx, owner, repoName, issueNumber, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue comments: %w", err)
	}

	// Get PR comments and reviews if PR exists
	var prComments []*github.IssueComment
	var prReviewCommentThreads [][]*github.PullRequestComment
	var prReviews []*github.PullRequestReview

	if pullRequest != nil {
		prComments, _, err = tg.githubClient.Issues.ListComments(ctx, owner, repoName, pullRequest.GetNumber(), nil)
		if err != nil {
			log.Printf("Failed to get PR comments: %v", err)
		}

		prReviews, _, err = tg.githubClient.PullRequests.ListReviews(ctx, owner, repoName, pullRequest.GetNumber(), nil)
		if err != nil {
			log.Printf("Failed to get PR reviews: %v", err)
		}

		reviewComments, _, err := tg.githubClient.PullRequests.ListComments(ctx, owner, repoName, pullRequest.GetNumber(), nil)
		if err != nil {
			log.Printf("Failed to get PR review comments: %v", err)
		} else {
			prReviewCommentThreads = tg.groupReviewComments(reviewComments)
		}
	}

	// Determine which comments require responses
	botUsername := tg.config.GitHubUsername
	issueCommentsRequiringResponses := tg.filterCommentsRequiringResponses(issueComments, botUsername)
	prCommentsRequiringResponses := tg.filterCommentsRequiringResponses(prComments, botUsername)
	prReviewThreadsRequiringResponses := tg.filterReviewThreadsRequiringResponses(prReviewCommentThreads, botUsername)

	task := &Task{
		Issue:        githubIssue,
		Repository:   repository,
		PullRequest:  pullRequest,
		TargetBranch: repository.GetDefaultBranch(),
		SourceBranch: sourceBranch,
		StyleGuide:   styleGuide,
		CodebaseInfo: codebaseInfo,
		IssueComments: issueComments,
		PRComments: prComments,
		PRReviewCommentThreads: prReviewCommentThreads,
		PRReviews: prReviews,
		IssueCommentsRequiringResponses: issueCommentsRequiringResponses,
		PRCommentsRequiringResponses: prCommentsRequiringResponses,
		PRReviewCommentThreadsRequiringResponses: prReviewThreadsRequiringResponses,
	}

	return task, nil
}

func (tg *Generator) getStyleGuide(ctx context.Context, owner, repo string) (*StyleGuide, error) {
	// Try common style guide file names
	styleGuideFiles := []string{"STYLE_GUIDE.md", "STYLE.md", "CONTRIBUTING.md", ".github/STYLE_GUIDE.md"}
	
	for _, filename := range styleGuideFiles {
		content, _, resp, err := tg.githubClient.Repositories.GetContents(ctx, owner, repo, filename, nil)
		if err == nil && content != nil {
			fileContent, err := content.GetContent()
			if err != nil {
				continue
			}
			
			return &StyleGuide{
				Exists:  true,
				Name:    filename,
				Content: fileContent,
			}, nil
		}
		if resp != nil && resp.StatusCode != 404 {
			return nil, fmt.Errorf("failed to check for %s: %w", filename, err)
		}
	}
	
	return &StyleGuide{Exists: false}, nil
}

func (tg *Generator) getCodebaseInfo(ctx context.Context, owner, repo string, repository *github.Repository) (*CodebaseInfo, error) {
	// Get languages
	languages, _, err := tg.githubClient.Repositories.ListLanguages(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get languages: %w", err)
	}

	// Get README
	var readmeExcerpt string
	readme, _, err := tg.githubClient.Repositories.GetReadme(ctx, owner, repo, nil)
	if err == nil && readme != nil {
		content, err := readme.GetContent()
		if err == nil {
			// Take first 1000 characters as excerpt
			if len(content) > 1000 {
				readmeExcerpt = content[:1000] + "..."
			} else {
				readmeExcerpt = content
			}
		}
	}

	// Generate basic file tree (simplified)
	fileTree, err := tg.generateFileTree(ctx, owner, repo)
	if err != nil {
		log.Printf("Failed to generate file tree: %v", err)
		fileTree = "Unable to generate file tree"
	}

	return &CodebaseInfo{
		MainLanguage:  repository.GetLanguage(),
		Languages:     languages,
		FileTree:      fileTree,
		ReadmeExcerpt: readmeExcerpt,
	}, nil
}

func (tg *Generator) generateFileTree(ctx context.Context, owner, repo string) (string, error) {
	// This is a simplified implementation. In practice, you might want to
	// implement a more sophisticated tree generation.
	_, contents, _, err := tg.githubClient.Repositories.GetContents(ctx, owner, repo, "", nil)
	if err != nil {
		return "", err
	}

	var tree strings.Builder
	for _, content := range contents {
		if content.GetName() != "" {
			tree.WriteString("- " + content.GetName() + "\n")
		}
	}

	return tree.String(), nil
}

func (tg *Generator) groupReviewComments(comments []*github.PullRequestComment) [][]*github.PullRequestComment {
	// Group comments by their original commit ID and path
	groups := make(map[string][]*github.PullRequestComment)
	
	for _, comment := range comments {
		key := fmt.Sprintf("%s:%s", comment.GetCommitID(), comment.GetPath())
		groups[key] = append(groups[key], comment)
	}
	
	var result [][]*github.PullRequestComment
	for _, group := range groups {
		result = append(result, group)
	}
	
	return result
}

func (tg *Generator) filterCommentsRequiringResponses(comments []*github.IssueComment, botUsername string) []*github.IssueComment {
	var requiring []*github.IssueComment
	
	for _, comment := range comments {
		// Skip comments by the bot itself
		if comment.GetUser().GetLogin() == botUsername {
			continue
		}
		
		// Check if comment mentions the bot or asks a question
		body := strings.ToLower(comment.GetBody())
		if strings.Contains(body, "@"+strings.ToLower(botUsername)) || 
		   strings.Contains(body, "?") {
			requiring = append(requiring, comment)
		}
	}
	
	return requiring
}

func (tg *Generator) filterReviewThreadsRequiringResponses(threads [][]*github.PullRequestComment, botUsername string) [][]*github.PullRequestComment {
	var requiring [][]*github.PullRequestComment
	
	for _, thread := range threads {
		if len(thread) == 0 {
			continue
		}
		
		// Check if the last comment in the thread is not by the bot
		lastComment := thread[len(thread)-1]
		if lastComment.GetUser().GetLogin() != botUsername {
			requiring = append(requiring, thread)
		}
	}
	
	return requiring
}