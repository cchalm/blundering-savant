package task

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v72/github"
)

type TaskOrError struct {
	Task Task
	Err  error
}

type taskGenerator struct {
	checkInterval time.Duration
	githubClient  *github.Client
	githubUser    *github.User
}

func NewTaskGenerator(githubClient *github.Client, githubUser *github.User, checkInterval time.Duration) *taskGenerator {
	return &taskGenerator{
		checkInterval: checkInterval,
		githubClient:  githubClient,
		githubUser:    githubUser,
	}
}

func (tg *taskGenerator) Generate(ctx context.Context) chan TaskOrError {
	tasks := make(chan TaskOrError)

	go func() {
		defer close(tasks)
		for {
			tg.yield(ctx, func(task Task, err error) {
				tasks <- TaskOrError{Task: task, Err: err}
			})
		}
	}()

	return tasks
}

func (tg *taskGenerator) yield(ctx context.Context, yield func(task Task, err error)) {
	ticker := time.Tick(tg.checkInterval)
	for {
		issues, err := tg.searchIssues(ctx)
		if err != nil {
			return
		}
		if len(issues) == 0 {
			log.Println("[taskgen] No issues found")
		}

		for _, issue := range issues {
			tsk, err := tg.buildTask(ctx, issue, tg.githubUser)
			if err != nil {
				yield(Task{}, fmt.Errorf("failed to build task for issue %d: %w", issue.Number, err))
			}

			if tg.needsAttention(*tsk) {
				log.Printf("[taskgen] Yielding task for issue #%d in %s/%s", issue.Number, issue.Owner, issue.Repo)
				yield(*tsk, nil)
			} else {
				log.Printf("[taskgen] Skipping issue #%d in %s/%s: no attention needed", issue.Number, issue.Owner, issue.Repo)
			}
		}

		log.Printf("[taskgen] Waiting for next check (up to %v)\n", tg.checkInterval)
		select {
		case <-ticker:
		case <-ctx.Done():
			yield(Task{}, ctx.Err())
			return
		}
	}
}

func (tg *taskGenerator) searchIssues(ctx context.Context) ([]GithubIssue, error) {
	// Search for issues assigned to the bot that are not being worked on and are not blocked
	query := fmt.Sprintf("assignee:%s is:issue is:open -label:%s -label:%s", *tg.githubUser.Login, *LabelWorking.Name, *LabelBlocked.Name)
	result, _, err := tg.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		return nil, fmt.Errorf("error searching issues: %w", err)
	}

	// Convert issue response into simpler structures
	issues := []GithubIssue{}
	for _, issue := range result.Issues {
		if issue == nil || issue.RepositoryURL == nil || issue.Number == nil || issue.Title == nil || issue.URL == nil {
			log.Print("[taskgen] Warning: unexpected nil, skipping issue")
			continue
		}

		// Extract owner and repo
		parts := strings.Split(*issue.RepositoryURL, "/")
		if len(parts) < 2 {
			log.Printf("[taskgen] Warning: failed to parse repo URL '%s', skipping issue '%d'", *issue.RepositoryURL, *issue.Number)
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		// Convert labels into a list of strings
		labels := []string{}
		for _, label := range issue.Labels {
			labels = append(labels, *label.Name)
		}

		issues = append(issues, GithubIssue{
			Owner:  owner,
			Repo:   repo,
			Number: *issue.Number,

			Title: *issue.Title,
			Body:  issue.GetBody(),
			URL:   *issue.URL,

			Labels: labels,
		})
	}

	return issues, nil
}

func (tg *taskGenerator) buildTask(ctx context.Context, issue GithubIssue, botUser *github.User) (*Task, error) {
	tsk := Task{
		Issue: issue,
	}

	owner, repo := issue.Owner, issue.Repo

	repoInfo, _, err := tg.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repo info: %w", err)
	}
	if repoInfo.DefaultBranch == nil {
		return nil, fmt.Errorf("nil default branch")
	}

	tsk.TargetBranch = *repoInfo.DefaultBranch
	tsk.SourceBranch = getSourceBranchName(issue)

	// Get the existing pull request, if any
	pr, err := getPullRequest(ctx, tg.githubClient, owner, repo, tsk.SourceBranch, *botUser.Login)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request for branch: %w", err)
	}
	tsk.PullRequest = pr

	// Get repository
	repository, _, err := tg.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}
	tsk.Repository = repository

	// Get style guide
	styleGuide, err := tg.findStyleGuides(ctx, owner, repo)
	if err != nil {
		log.Printf("[taskgen] Warning: Could not find style guides: %v", err)
	}
	tsk.StyleGuide = styleGuide

	// Get codebase info
	codebaseInfo, err := tg.analyzeCodebase(ctx, owner, repo)
	if err != nil {
		log.Printf("[taskgen] Warning: Could not analyze codebase: %v", err)
	}
	tsk.CodebaseInfo = codebaseInfo

	comments, err := tg.getAllIssueComments(ctx, owner, repo, issue.Number)
	if err != nil {
		log.Printf("[taskgen] Warning: Could not get issue comments: %v", err)
	}
	tsk.IssueComments = comments

	// If there is a PR, get PR comments, reviews, and review comments
	if pr != nil {
		// Get PR comments
		comments, err := tg.getAllIssueComments(ctx, owner, repo, pr.Number)
		if err != nil {
			return nil, fmt.Errorf("could not get pull request comments: %w", err)
		}
		tsk.PRComments = comments

		// Get reviews
		reviews, err := tg.getAllPRReviews(ctx, owner, repo, pr.Number)
		if err != nil {
			return nil, fmt.Errorf("could not get PR reviews: %w", err)
		}
		tsk.PRReviews = reviews

		// Get PR review comment threads
		reviewComments, err := tg.getAllPRReviewComments(ctx, owner, repo, pr.Number)
		if err != nil {
			return nil, fmt.Errorf("could not get PR comments: %w", err)
		}
		reviewCommentThreads, err := organizePRReviewCommentsIntoThreads(reviewComments)
		if err != nil {
			return nil, fmt.Errorf("could not organize review comments into threads: %w", err)
		}

		tsk.PRReviewCommentThreads = reviewCommentThreads
	}

	// Get comments requiring responses
	commentsReq, err := tg.pickIssueCommentsRequiringResponse(ctx, owner, repo, tsk.IssueComments, botUser)
	if err != nil {
		return nil, fmt.Errorf("could not get issue comments requiring response: %w", err)
	}
	prCommentsReq, err := tg.pickIssueCommentsRequiringResponse(ctx, owner, repo, tsk.PRComments, botUser)
	if err != nil {
		return nil, fmt.Errorf("could not get PR comments requiring response: %w", err)
	}
	prReviewCommentsReq, err := tg.pickPRReviewCommentsRequiringResponse(ctx, owner, repo, tsk.PRReviewCommentThreads, botUser)
	if err != nil {
		return nil, fmt.Errorf("could not get PR review comments requiring response: %w", err)
	}
	tsk.IssueCommentsRequiringResponses = commentsReq
	tsk.PRCommentsRequiringResponses = prCommentsReq
	tsk.PRReviewCommentsRequiringResponses = prReviewCommentsReq

	return &tsk, nil
}

func (tg *taskGenerator) needsAttention(task Task) bool {
	if len(task.IssueComments) == 0 && task.PullRequest == nil {
		// If there are no issue comments and no pull request, this is a brand new issue and requires our attention
		return true
	}
	// Check if there are comments needing responses
	if len(task.IssueCommentsRequiringResponses) > 0 ||
		len(task.PRCommentsRequiringResponses) > 0 ||
		len(task.PRReviewCommentsRequiringResponses) > 0 {

		return true
	}
	// Check if there is a "bot turn" label, which is a manual prompt for the bot to take action
	if slices.Contains(task.Issue.Labels, *LabelBotTurn.Name) {
		return true
	}

	return false
}

// Repository analysis functions

// findStyleGuides searches for coding style documentation
func (tg *taskGenerator) findStyleGuides(ctx context.Context, owner, repo string) (*StyleGuide, error) {
	styleGuide := &StyleGuide{
		Guides: map[string]string{},
	}

	paths := []string{
		"STYLE_GUIDE.md",
		"CONTRIBUTING.md",
		"STYLE.md",
		"CODING_STYLE.md",
		".github/CONTRIBUTING.md",
		"docs/CONTRIBUTING.md",
	}

	for _, path := range paths {
		content, _, _, err := tg.githubClient.Repositories.GetContents(ctx, owner, repo, path, nil)
		if err == nil && content != nil {
			decodedContent, err := content.GetContent()
			if err == nil {
				styleGuide.Guides[path] = decodedContent
			}
		}
	}

	if len(styleGuide.Guides) == 0 {
		return nil, fmt.Errorf("no style guides found")
	}

	return styleGuide, nil
}

// analyzeCodebase examines the repository structure
func (tg *taskGenerator) analyzeCodebase(ctx context.Context, owner, repo string) (*CodebaseInfo, error) {
	info := &CodebaseInfo{
		PackageInfo: make(map[string]string),
	}

	// Get repository languages
	languages, _, err := tg.githubClient.Repositories.ListLanguages(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to list languages: %w", err)
	}

	// Find main language
	maxBytes := 0
	for lang, bytes := range languages {
		if bytes > maxBytes {
			maxBytes = bytes
			info.MainLanguage = lang
		}
	}

	// Get file tree
	fileTree, err := tg.getFileTree(ctx, owner, repo)
	if err != nil {
		log.Printf("[taskgen] Warning: Could not get file tree: %v", err)
	} else {
		info.FileTree = fileTree
	}

	// Get README
	readme, _, err := tg.githubClient.Repositories.GetReadme(ctx, owner, repo, nil)
	if err == nil {
		content, err := readme.GetContent()
		if err == nil {
			info.ReadmeContent = content
		}
	}

	return info, nil
}

// getFileTree retrieves the complete file tree with safety limits
func (tg *taskGenerator) getFileTree(ctx context.Context, owner, repo string) ([]string, error) {
	const (
		maxFiles      = 2000
		maxPathLength = 500
	)

	// Get the full recursive tree
	tree, _, err := tg.githubClient.Git.GetTree(ctx, owner, repo, "HEAD", true)
	if err != nil {
		return nil, fmt.Errorf("failed to get recursive tree: %w", err)
	}

	var fileTree []string
	fileCount := 0

	for _, entry := range tree.Entries {
		if entry.Path == nil {
			continue
		}

		path := *entry.Path

		// Check path length limit
		if len(path) > maxPathLength {
			continue
		}

		// Check file count limit
		if fileCount >= maxFiles {
			break
		}

		fileTree = append(fileTree, path)
		fileCount++
	}

	return fileTree, nil
}

// Comment retrieval functions

// getAllIssueComments retrieves all comments on an issue
func (tg *taskGenerator) getAllIssueComments(ctx context.Context, owner, repo string, issueNumber int) ([]*github.IssueComment, error) {
	var allComments []*github.IssueComment

	opts := &github.IssueListCommentsOptions{
		Sort:      github.Ptr("created"),
		Direction: github.Ptr("asc"),
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		comments, resp, err := tg.githubClient.Issues.ListComments(ctx, owner, repo, issueNumber, opts)
		if err != nil {
			return nil, err
		}
		allComments = append(allComments, comments...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allComments, nil
}

// getAllPRReviews retrieves all reviews on a PR, sorted chronologically
func (tg *taskGenerator) getAllPRReviews(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestReview, error) {
	var allReviews []*github.PullRequestReview

	reviews, _, err := tg.githubClient.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, err
	}

	for _, review := range reviews {
		if review == nil {
			continue
		}

		allReviews = append(allReviews, review)
	}

	return allReviews, nil
}

// getAllPRComments retrieves all review comments on a PR, sorted chronologically
func (tg *taskGenerator) getAllPRReviewComments(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestComment, error) {
	var allComments []*github.PullRequestComment

	opts := &github.PullRequestListCommentsOptions{
		Sort:      "created",
		Direction: "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		comments, resp, err := tg.githubClient.PullRequests.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, err
		}

		for _, comment := range comments {
			if comment == nil || comment.ID == nil {
				log.Println("[taskgen] Warning: comment or comment.ID unexpectedly nil")
				continue
			}

			allComments = append(allComments, comment)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allComments, nil
}

// GitHub API helper functions

// pickIssueCommentsRequiringResponse gets regular issue/PR comments that haven't been reacted to by the bot
func (tg *taskGenerator) pickIssueCommentsRequiringResponse(ctx context.Context, owner, repo string, comments []*github.IssueComment, botUser *github.User) ([]*github.IssueComment, error) {
	var commentsRequiringResponse []*github.IssueComment

	for _, comment := range comments {
		// Skip if this is the bot's own comment
		if tg.isBotComment(comment.User, botUser) {
			continue
		}

		// Check if bot has reacted to this comment
		hasReacted, err := tg.hasBotReactedToIssueComment(ctx, owner, repo, *comment.ID, botUser)
		if err != nil {
			return nil, fmt.Errorf("failed to check reactions for comment %d: %w", *comment.ID, err)
		}
		if hasReacted {
			continue
		}

		commentsRequiringResponse = append(commentsRequiringResponse, comment)
	}

	return commentsRequiringResponse, nil
}

// getReviewComments gets PR review comments that haven't been replied to or reacted to by the bot
func (tg *taskGenerator) pickPRReviewCommentsRequiringResponse(ctx context.Context, owner, repo string, commentThreads [][]*github.PullRequestComment, botUser *github.User) ([]*github.PullRequestComment, error) {
	var commentsRequiringResponse []*github.PullRequestComment

	for _, thread := range commentThreads {
		// Look at every comment, not just the last comment in each thread. Multiple replies may have been added to a
		// chain since the bot last looked at it, and for other contributors' peace of mind the bot should explicitly
		// acknolwedge that it has seen every comment in the chain, even if it only replied to the last one
		for _, comment := range thread {
			// Skip if this is the bot's own comment
			if tg.isBotComment(comment.User, botUser) {
				continue
			}

			// Check if bot has reacted to this comment
			hasReacted, err := tg.hasBotReactedToReviewComment(ctx, owner, repo, *comment.ID, botUser)
			if err != nil {
				return nil, fmt.Errorf("failed to check reactions for review comment %d: %w", *comment.ID, err)
			}
			if hasReacted {
				continue
			}

			commentsRequiringResponse = append(commentsRequiringResponse, comment)
		}
	}

	return commentsRequiringResponse, nil
}

// isBotComment checks if a comment was made by the bot
func (tg *taskGenerator) isBotComment(commentUser, botUser *github.User) bool {
	return commentUser != nil && botUser.Login != nil &&
		commentUser.Login != nil && *commentUser.Login == *botUser.Login
}

// hasBotReactedToIssueComment checks if the bot has reacted to an issue comment
func (tg *taskGenerator) hasBotReactedToIssueComment(ctx context.Context, owner, repo string, commentID int64, botUser *github.User) (bool, error) {
	if botUser.Login == nil {
		return false, nil
	}

	reactions, _, err := tg.githubClient.Reactions.ListIssueCommentReactions(ctx, owner, repo, commentID, nil)
	if err != nil {
		return false, fmt.Errorf("failed to list reactions: %w", err)
	}

	for _, reaction := range reactions {
		if reaction.User != nil && reaction.User.Login != nil &&
			*reaction.User.Login == *botUser.Login {
			return true, nil
		}
	}

	return false, nil
}

// hasBotReactedToReviewComment checks if the bot has reacted to a review comment
func (tg *taskGenerator) hasBotReactedToReviewComment(ctx context.Context, owner, repo string, commentID int64, botUser *github.User) (bool, error) {
	if botUser.Login == nil {
		return false, nil
	}

	reactions, _, err := tg.githubClient.Reactions.ListPullRequestCommentReactions(ctx, owner, repo, commentID, nil)
	if err != nil {
		return false, fmt.Errorf("failed to list reactions: %w", err)
	}

	for _, reaction := range reactions {
		if reaction.User != nil && reaction.User.Login != nil &&
			*reaction.User.Login == *botUser.Login {
			return true, nil
		}
	}

	return false, nil
}

// getPullRequest returns a pull request by source branch and owner, if exactly one such pull request exists. If no such
// pull request exists, returns (nil, nil). If more than one such pull request exists, returns an error
func getPullRequest(ctx context.Context, githubClient *github.Client, owner, repo, branch, author string) (*GithubPullRequest, error) {
	query := fmt.Sprintf("type:pr repo:%s/%s head:%s author:%s", owner, repo, branch, author)

	opts := &github.SearchOptions{
		Sort:        "created",
		Order:       "desc",
		ListOptions: github.ListOptions{PerPage: 50},
	}

	result, _, err := githubClient.Search.Issues(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to search issues: %w", err)
	}
	if len(result.Issues) > 1 {
		return nil, fmt.Errorf("found %d pull requests, expected 0 or 1", len(result.Issues))
	}

	if len(result.Issues) == 0 {
		// Expected, return nil
		return nil, nil
	}

	issue := result.Issues[0]
	pr, _, err := githubClient.PullRequests.Get(ctx, owner, repo, *issue.Number)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pull request: %w", err)
	}

	if pr == nil || pr.Number == nil || pr.Title == nil || pr.URL == nil || pr.Base == nil || pr.Base.Ref == nil {
		return nil, fmt.Errorf("unexpected nil in pull request struct")
	}

	return &GithubPullRequest{
		Owner:  owner,
		Repo:   repo,
		Number: *pr.Number,

		Title: *pr.Title,
		URL:   *pr.URL,

		BaseBranch: *pr.Base.Ref,
	}, nil
}

// organizePRReviewCommentsIntoThreads takes a list of pull request review comments and returns a list of comment
// threads, where each thread is a list of comments that reply to the next
func organizePRReviewCommentsIntoThreads(comments []*github.PullRequestComment) ([][]*github.PullRequestComment, error) {
	// In github, it appears that all comments in a thread are replies to the top comment, rather than replies to each
	// other in a chain. Therefore we will simply collect all replies to a comment and sort them by date to form a chain

	// threadsMap maps a comment ID to that comment and all of its replies
	threadsMap := map[int64][]*github.PullRequestComment{}

	for _, comment := range comments {
		if comment == nil || comment.ID == nil {
			return nil, fmt.Errorf("unexpected nil comment or comment.ID")
		}
		if comment.InReplyTo == nil {
			// Top-level comment
			threadsMap[*comment.ID] = append(threadsMap[*comment.ID], comment)
		} else {
			// Reply comment
			threadsMap[*comment.InReplyTo] = append(threadsMap[*comment.InReplyTo], comment)
		}
	}

	threads := [][]*github.PullRequestComment{}
	for _, thread := range threadsMap {
		slices.SortFunc(thread, func(a, b *github.PullRequestComment) int {
			return a.CreatedAt.Compare(b.CreatedAt.Time)
		})
		threads = append(threads, thread)
	}

	return threads, nil
}

func getSourceBranchName(issue GithubIssue) string {
	branchName := fmt.Sprintf("fix/issue-%d-%s", issue.Number, sanitizeForBranchName(issue.Title))
	return normalizeBranchName(branchName)
}

func sanitizeForBranchName(s string) string {
	// Convert to lowercase and replace invalid characters
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove invalid characters for git branch names
	invalidChars := []string{"~", "^", ":", "?", "*", "[", "]", "\\", "..", "@{", "/.", "//"}
	for _, char := range invalidChars {
		s = strings.ReplaceAll(s, char, "")
	}

	return s
}

func normalizeBranchName(s string) string {
	// Limit length
	if len(s) > 70 {
		s = s[:70]
	}
	// Clean up trailing separators
	s = strings.Trim(s, "-.")

	return s
}
