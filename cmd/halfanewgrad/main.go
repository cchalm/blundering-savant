package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
)

// Config holds the configuration for the virtual developer
type Config struct {
	GitHubToken     string
	AnthropicAPIKey string
	GitHubUsername  string
	CheckInterval   time.Duration
}

// VirtualDeveloper represents our bot
type VirtualDeveloper struct {
	config          *Config
	githubClient    *github.Client
	anthropicClient *AnthropicClient
}

// Solution represents the generated code solution
type Solution struct {
	Branch        string
	CommitMessage string
	Files         map[string]FileChange
	Description   string
}

// FileChange represents a change to a file
type FileChange struct {
	Path       string
	Content    string
	IsNew      bool
	OldContent string
}

const (
	LabelInProgress = "virtual-dev-in-progress"
	LabelCompleted  = "virtual-dev-completed"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	config := &Config{
		GitHubToken:     os.Getenv("GITHUB_TOKEN"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		GitHubUsername:  os.Getenv("GITHUB_USERNAME"),
		CheckInterval:   5 * time.Minute,
	}

	if config.GitHubToken == "" || config.AnthropicAPIKey == "" || config.GitHubUsername == "" {
		log.Fatal("Missing required environment variables: GITHUB_TOKEN, ANTHROPIC_API_KEY, or GITHUB_USERNAME")
	}

	// Parse check interval if provided
	if interval := os.Getenv("CHECK_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			config.CheckInterval = d
		}
	}

	vd := NewVirtualDeveloper(config)

	log.Printf("Virtual Developer started. Monitoring issues for @%s every %v", config.GitHubUsername, config.CheckInterval)

	// Start the main loop
	vd.Run()
}

// NewVirtualDeveloper creates a new instance
func NewVirtualDeveloper(config *Config) *VirtualDeveloper {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.GitHubToken},
	)
	tc := oauth2.NewClient(ctx, ts)

	return &VirtualDeveloper{
		config:          config,
		githubClient:    github.NewClient(tc),
		anthropicClient: NewAnthropicClient(config.AnthropicAPIKey),
	}
}

// Run starts the main loop
func (vd *VirtualDeveloper) Run() {
	ticker := time.NewTicker(vd.config.CheckInterval)
	defer ticker.Stop()

	// Initial check
	vd.checkAndProcessWorkItems()

	for range ticker.C {
		vd.checkAndProcessWorkItems()
	}
}

// checkAndProcessWorkItems checks for work items that need attention
func (vd *VirtualDeveloper) checkAndProcessWorkItems() {
	ctx := context.Background()

	// First, handle new issues
	vd.processIssuesWithoutPRs(ctx)

	// Then, handle issues in discussion (no PR yet but already engaged)
	vd.processIssuesInDiscussion(ctx)

	// Finally, process existing PRs
	vd.processExistingPRs(ctx)
}

// processIssuesWithoutPRs handles issues that don't have PRs yet
func (vd *VirtualDeveloper) processIssuesWithoutPRs(ctx context.Context) {
	// Search for issues assigned to bot without PRs
	query := fmt.Sprintf("assignee:%s is:issue is:open", vd.config.GitHubUsername)
	issues, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching issues: %v", err)
		return
	}

	for _, issue := range issues.Issues {
		if issue == nil || issue.RepositoryURL == nil || issue.Number == nil {
			continue
		}

		// Extract owner and repo
		parts := strings.Split(*issue.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		// Check if this issue already has a PR
		if vd.issueHasPR(ctx, owner, repo, *issue.Number) {
			continue
		}

		issueTitle := "untitled"
		if issue.Title != nil {
			issueTitle = *issue.Title
		}

		log.Printf("Processing issue #%d in %s/%s: %s", *issue.Number, owner, repo, issueTitle)

		// Process this issue (generate solution and create PR)
		if err := vd.processNewIssue(ctx, owner, repo, issue); err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
			// Post sanitized error comment
			vd.postIssueComment(ctx, owner, repo, *issue.Number,
				"I encountered an error while working on this issue. I'll retry on the next check cycle.")
		}
	}
}

// processExistingPRs handles PRs that may need updates
func (vd *VirtualDeveloper) processExistingPRs(ctx context.Context) {
	// Search for open PRs created by the bot
	query := fmt.Sprintf("author:%s is:pr is:open", vd.config.GitHubUsername)
	prs, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching PRs: %v", err)
		return
	}

	for _, pr := range prs.Issues {
		if pr == nil || pr.RepositoryURL == nil || pr.Number == nil {
			continue
		}

		// Extract owner and repo
		parts := strings.Split(*pr.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		// Process this PR for updates
		if err := vd.processExistingPR(ctx, owner, repo, *pr.Number); err != nil {
			log.Printf("Error processing PR #%d: %v", *pr.Number, err)
		}
	}
}

// Updated processNewIssue to handle flexible AI interactions
func (vd *VirtualDeveloper) processNewIssue(ctx context.Context, owner, repo string, issue *github.Issue) error {
	// Add in-progress label
	if err := vd.addLabel(ctx, owner, repo, *issue.Number, LabelInProgress); err != nil {
		return fmt.Errorf("failed to add in-progress label: %w", err)
	}

	// Post initial comment
	if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
		"👋 I'm analyzing this issue and will assist you shortly."); err != nil {
		log.Printf("Warning: failed to post initial comment: %v", err)
	}

	// Build work context
	workCtx, err := vd.buildWorkContext(ctx, owner, repo, issue, nil)
	if err != nil {
		vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)
		return fmt.Errorf("failed to build work context: %w", err)
	}

	// Let AI decide what to do
	result, err := vd.processWithAI(ctx, workCtx, owner, repo)
	if err != nil {
		vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)
		return fmt.Errorf("failed to process with AI: %w", err)
	}

	// If AI created a solution, create the PR
	if result.Solution != nil {
		pr, err := vd.createPullRequestWithSolution(ctx, owner, repo, issue, result.Solution)
		if err != nil {
			vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)
			return fmt.Errorf("failed to create pull request: %w", err)
		}

		// Update labels
		if err := vd.addLabel(ctx, owner, repo, *issue.Number, LabelCompleted); err != nil {
			log.Printf("Warning: Failed to add completed label: %v", err)
		}
		if err := vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress); err != nil {
			log.Printf("Warning: Failed to remove in-progress label: %v", err)
		}

		// Post success comment
		if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
			fmt.Sprintf("I've created PR #%d with my solution. Please review and let me know if any changes are needed.", *pr.Number)); err != nil {
			log.Printf("Warning: Failed to post success comment: %v", err)
		}
	} else if result.NeedsMoreInfo {
		// AI is engaging in discussion, keep in-progress label
		log.Printf("AI is engaging in discussion for issue #%d", *issue.Number)
	}

	return nil
}

// processExistingPR checks if a PR needs updates and processes them
func (vd *VirtualDeveloper) processExistingPR(ctx context.Context, owner, repo string, prNumber int) error {
	// Get PR details
	pr, _, err := vd.githubClient.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR details: %w", err)
	}

	// Get associated issue
	var issue *github.Issue
	if pr.Body != nil {
		if issueNum := extractIssueNumber(*pr.Body); issueNum > 0 {
			issue, _, _ = vd.githubClient.Issues.Get(ctx, owner, repo, issueNum)
		}
	}

	if issue == nil {
		log.Printf("Warning: Could not find associated issue for PR #%d", prNumber)
		return nil
	}

	// Build work context with PR
	workCtx, err := vd.buildWorkContext(ctx, owner, repo, issue, pr)
	if err != nil {
		return fmt.Errorf("failed to build work context: %w", err)
	}

	// Check if we need to do anything
	if !vd.needsAttention(workCtx) {
		return nil
	}

	log.Printf("Processing PR #%d", prNumber)

	// Let AI decide what to do
	result, err := vd.processWithAI(ctx, workCtx, owner, repo)
	if err != nil {
		// Post sanitized error comment
		vd.postIssueComment(ctx, owner, repo, prNumber,
			"I encountered an error while processing this. I'll retry on the next check cycle.")
		return fmt.Errorf("failed to process with AI: %w", err)
	}

	// If AI created an updated solution, apply it
	if result.Solution != nil {
		if err := vd.updatePullRequest(ctx, owner, repo, pr, result.Solution); err != nil {
			return fmt.Errorf("failed to update pull request: %w", err)
		}
	}

	return nil
}

// processWithAI handles the AI interaction and executes the actions it decides
func (vd *VirtualDeveloper) processWithAI(ctx context.Context, workCtx *WorkContext, owner, repo string) (*InteractionResult, error) {
	// Continue conversation until AI has done what it wants to do
	maxIterations := 10
	var conversationMessages []anthropic.MessageParam

	for i := 0; i < maxIterations; i++ {
		log.Printf("AI interaction iteration %d", i+1)

		// Get AI's decision
		result, err := vd.anthropicClient.ProcessWorkItem(ctx, workCtx)
		if err != nil {
			return nil, fmt.Errorf("AI processing failed: %w", err)
		}

		// Execute the actions the AI decided to take
		needsContinuation := false
		for _, action := range result.Actions {
			switch action.Type {
			case "analyze_file":
				// Fetch file content for AI
				path := action.Data["path"].(string)
				content, err := vd.fetchFileContent(ctx, owner, repo, path)
				if err != nil {
					log.Printf("Failed to fetch file %s: %v", path, err)
					// Add error to conversation
					conversationMessages = append(conversationMessages,
						anthropic.NewUserMessage(anthropic.NewToolResultBlock(
							action.ToolID,
							fmt.Sprintf("Error: Could not fetch file '%s'", path),
							true)))
				} else {
					// Add file content to conversation
					conversationMessages = append(conversationMessages,
						anthropic.NewUserMessage(anthropic.NewToolResultBlock(
							action.ToolID,
							fmt.Sprintf("File: %s\n\n%s", path, content),
							false)))
				}
				needsContinuation = true

			case "post_comment":
				// Post the comment
				comment := vd.extractCommentFromAction(action)
				if err := vd.postComment(ctx, owner, repo, workCtx, comment); err != nil {
					log.Printf("Failed to post comment: %v", err)
				}

			case "add_reaction":
				// Add reaction
				commentID := int64(action.Data["comment_id"].(float64))
				reaction := action.Data["reaction"].(string)
				vd.addCommentReaction(ctx, owner, repo, commentID, reaction)

			case "request_review":
				// Request review from users
				if usernames, ok := action.Data["usernames"].([]interface{}); ok {
					vd.requestReview(ctx, owner, repo, workCtx, usernames)
				}

			case "create_solution":
				// Solution is already in result.Solution
				log.Printf("AI created a solution")

			case "mark_ready_to_implement":
				// AI will implement in next iteration
				needsContinuation = true
			}
		}

		// Post any comments the AI wants to make
		for _, comment := range result.Comments {
			if err := vd.postComment(ctx, owner, repo, workCtx, comment); err != nil {
				log.Printf("Failed to post comment: %v", err)
			}
		}

		// If AI doesn't need to continue, we're done
		if !needsContinuation && !result.NeedsMoreInfo {
			return result, nil
		}

		// If we have a solution, we're done
		if result.Solution != nil {
			return result, nil
		}

		// Update context for next iteration
		workCtx, err = vd.buildWorkContext(ctx, owner, repo, workCtx.Issue, workCtx.PullRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to rebuild context: %w", err)
		}

		// Add continuation context if provided
		if result.ContinuationData != "" {
			// This would be added to the work context for the next iteration
			workCtx.ContinuationContext = result.ContinuationData
		}
	}

	return nil, fmt.Errorf("exceeded maximum iterations without completion")
}

// needsAttention checks if a work item needs AI attention
func (vd *VirtualDeveloper) needsAttention(workCtx *WorkContext) bool {
	// Check if there are comments needing responses
	if len(workCtx.NeedsToRespond) > 0 {
		return true
	}

	// Check for unaddressed change requests
	for _, review := range workCtx.PRReviews {
		if review.State == "CHANGES_REQUESTED" && review.Author != workCtx.BotUsername {
			// Would need to check if this is newer than last commit
			return true
		}
	}

	// Check for new comments since last bot activity
	lastBotActivity := vd.getLastBotActivity(workCtx)
	for _, comment := range workCtx.IssueComments {
		if comment.Author != workCtx.BotUsername && comment.CreatedAt.After(lastBotActivity) {
			return true
		}
	}

	for _, comment := range workCtx.PRReviewComments {
		if comment.Author != workCtx.BotUsername && comment.CreatedAt.After(lastBotActivity) {
			return true
		}
	}

	return false
}

// getLastBotActivity finds the timestamp of the last bot activity
func (vd *VirtualDeveloper) getLastBotActivity(workCtx *WorkContext) time.Time {
	var lastActivity time.Time

	// Check issue comments
	for _, comment := range workCtx.IssueComments {
		if comment.Author == workCtx.BotUsername && comment.CreatedAt.After(lastActivity) {
			lastActivity = comment.CreatedAt
		}
	}

	// Check PR comments
	for _, comment := range workCtx.PRReviewComments {
		if comment.Author == workCtx.BotUsername && comment.CreatedAt.After(lastActivity) {
			lastActivity = comment.CreatedAt
		}
	}

	return lastActivity
}

// postComment posts a comment based on the AI's decision
func (vd *VirtualDeveloper) postComment(ctx context.Context, owner, repo string, workCtx *WorkContext, comment Comment) error {
	// Handle reactions separately
	if len(comment.AddReactions) > 0 {
		for commentID, reaction := range comment.AddReactions {
			vd.addCommentReaction(ctx, owner, repo, commentID, reaction)
		}
		return nil
	}

	switch comment.Type {
	case "issue":
		if workCtx.Issue != nil && workCtx.Issue.Number != nil {
			return vd.postIssueComment(ctx, owner, repo, *workCtx.Issue.Number, comment.Body)
		}

	case "pr":
		if workCtx.PullRequest != nil && workCtx.PullRequest.Number != nil {
			return vd.postIssueComment(ctx, owner, repo, *workCtx.PullRequest.Number, comment.Body)
		}

	case "review":
		if workCtx.PullRequest != nil && workCtx.PullRequest.Number != nil {
			return vd.postReviewComment(ctx, owner, repo, *workCtx.PullRequest.Number, comment)
		}

	case "review_reply":
		if comment.InReplyTo != nil {
			return vd.postReviewCommentReply(ctx, owner, repo, *comment.InReplyTo, comment.Body)
		}
	}

	return fmt.Errorf("unable to post comment of type %s", comment.Type)
}

// postReviewComment posts a review comment on specific code
func (vd *VirtualDeveloper) postReviewComment(ctx context.Context, owner, repo string, prNumber int, comment Comment) error {
	// Get the latest commit SHA
	pr, _, err := vd.githubClient.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return err
	}

	reviewComment := &github.PullRequestComment{
		Body:     github.String(comment.Body),
		Path:     github.String(comment.FilePath),
		Line:     github.Int(comment.Line),
		Side:     github.String(comment.Side),
		CommitID: pr.Head.SHA,
	}

	_, _, err = vd.githubClient.PullRequests.CreateComment(ctx, owner, repo, prNumber, reviewComment)
	return err
}

// postReviewCommentReply posts a reply to an existing review comment
func (vd *VirtualDeveloper) postReviewCommentReply(ctx context.Context, owner, repo string, commentID int64, body string) error {
	// First, get the original comment to find the PR number
	// This is a limitation of the GitHub API - we need to track PR numbers separately in production
	// For now, we'll need to maintain this mapping elsewhere
	log.Printf("Posting reply to comment %d: %s", commentID, body)

	// In a real implementation, you'd need to track which PR a comment belongs to
	// GitHub's API requires the PR number to post a reply
	return fmt.Errorf("review comment reply not fully implemented - needs PR tracking")
}

// requestReview requests review from specific users
func (vd *VirtualDeveloper) requestReview(ctx context.Context, owner, repo string, workCtx *WorkContext, usernames []interface{}) error {
	if workCtx.PullRequest == nil || workCtx.PullRequest.Number == nil {
		return fmt.Errorf("no pull request to request review on")
	}

	var reviewers []string
	for _, u := range usernames {
		if username, ok := u.(string); ok {
			reviewers = append(reviewers, username)
		}
	}

	reviewRequest := github.ReviewersRequest{
		Reviewers: reviewers,
	}

	_, _, err := vd.githubClient.PullRequests.RequestReviewers(ctx, owner, repo, *workCtx.PullRequest.Number, reviewRequest)
	return err
}

// extractCommentFromAction converts an action to a Comment
func (vd *VirtualDeveloper) extractCommentFromAction(action Action) Comment {
	comment := Comment{}

	if commentType, ok := action.Data["comment_type"].(string); ok {
		comment.Type = commentType
	}

	if body, ok := action.Data["body"].(string); ok {
		comment.Body = body
	}

	if replyTo, ok := action.Data["in_reply_to"].(float64); ok {
		replyToInt := int64(replyTo)
		comment.InReplyTo = &replyToInt
	}

	if filePath, ok := action.Data["file_path"].(string); ok {
		comment.FilePath = filePath
	}

	if line, ok := action.Data["line"].(float64); ok {
		comment.Line = int(line)
	}

	if side, ok := action.Data["side"].(string); ok {
		comment.Side = side
	}

	return comment
}

// Updated WorkContext to include continuation context
type WorkContextExtended struct {
	WorkContext
	ContinuationContext string // For maintaining context across AI iterations
}

// processIssuesInDiscussion handles issues where AI is engaged in discussion
func (vd *VirtualDeveloper) processIssuesInDiscussion(ctx context.Context) {
	// Search for issues with in-progress label but no PR
	query := fmt.Sprintf("assignee:%s is:issue is:open label:\"%s\"", vd.config.GitHubUsername, LabelInProgress)
	issues, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching issues in discussion: %v", err)
		return
	}

	for _, issue := range issues.Issues {
		if issue == nil || issue.RepositoryURL == nil || issue.Number == nil {
			continue
		}

		// Extract owner and repo
		parts := strings.Split(*issue.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		// Check if this issue has a PR
		if vd.issueHasPR(ctx, owner, repo, *issue.Number) {
			continue
		}

		// Build context and check if there's new activity
		workCtx, err := vd.buildWorkContext(ctx, owner, repo, issue, nil)
		if err != nil {
			log.Printf("Error building context for issue #%d: %v", *issue.Number, err)
			continue
		}

		// Check if there's new activity since last bot comment
		if !vd.needsAttention(workCtx) {
			continue
		}

		log.Printf("Continuing discussion on issue #%d", *issue.Number)

		// Let AI continue the discussion
		result, err := vd.processWithAI(ctx, workCtx, owner, repo)
		if err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
			continue
		}

		// If AI created a solution, create the PR
		if result.Solution != nil {
			pr, err := vd.createPullRequestWithSolution(ctx, owner, repo, issue, result.Solution)
			if err != nil {
				log.Printf("Error creating PR for issue #%d: %v", *issue.Number, err)
				continue
			}

			// Update labels
			vd.addLabel(ctx, owner, repo, *issue.Number, LabelCompleted)
			vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)

			// Post success comment
			vd.postIssueComment(ctx, owner, repo, *issue.Number,
				fmt.Sprintf("Based on our discussion, I've created PR #%d with my solution. Please review and let me know if any changes are needed.", *pr.Number))
		}
	}
}

// buildWorkContext creates a complete work context
func (vd *VirtualDeveloper) buildWorkContext(ctx context.Context, owner, repo string, issue *github.Issue, pr *github.PullRequest) (*WorkContext, error) {
	workCtx := NewWorkContext(vd.config.GitHubUsername)
	workCtx.Issue = issue
	workCtx.PullRequest = pr
	workCtx.IsInitialSolution = (pr == nil)

	// Get repository
	repository, _, err := vd.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}
	workCtx.Repository = repository

	// Get style guide
	styleGuide, err := vd.findStyleGuides(ctx, owner, repo)
	if err != nil {
		log.Printf("Warning: Could not find style guides: %v", err)
	}
	workCtx.StyleGuide = styleGuide

	// Get codebase info
	codebaseInfo, err := vd.analyzeCodebase(ctx, owner, repo)
	if err != nil {
		log.Printf("Warning: Could not analyze codebase: %v", err)
	}
	workCtx.CodebaseInfo = codebaseInfo

	// Get issue comments
	if issue != nil && issue.Number != nil {
		comments, err := vd.getAllIssueComments(ctx, owner, repo, *issue.Number)
		if err != nil {
			log.Printf("Warning: Could not get issue comments: %v", err)
		}
		workCtx.IssueComments = comments
	}

	// Get PR comments and reviews if PR exists
	if pr != nil && pr.Number != nil {
		// Get PR reviews
		reviews, err := vd.getAllPRReviews(ctx, owner, repo, *pr.Number)
		if err != nil {
			log.Printf("Warning: Could not get PR reviews: %v", err)
		}
		workCtx.PRReviews = reviews

		// Get PR comments
		comments, err := vd.getAllPRComments(ctx, owner, repo, *pr.Number)
		if err != nil {
			log.Printf("Warning: Could not get PR comments: %v", err)
		}
		workCtx.PRReviewComments = comments
	}

	// Analyze which comments need responses
	workCtx.AnalyzeComments()

	return workCtx, nil
}

// getAllIssueComments retrieves all comments on an issue
func (vd *VirtualDeveloper) getAllIssueComments(ctx context.Context, owner, repo string, issueNumber int) ([]CommentContext, error) {
	var allComments []CommentContext

	opts := &github.IssueListCommentsOptions{
		Sort:      github.Ptr("created"),
		Direction: github.Ptr("asc"),
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		comments, resp, err := vd.githubClient.Issues.ListComments(ctx, owner, repo, issueNumber, opts)
		if err != nil {
			return nil, err
		}

		for _, comment := range comments {
			if comment == nil {
				continue
			}

			commentCtx := CommentContext{
				ID:          comment.GetID(),
				Author:      comment.User.GetLogin(),
				Body:        comment.GetBody(),
				CreatedAt:   comment.GetCreatedAt().Time,
				UpdatedAt:   comment.GetUpdatedAt().Time,
				IsEdited:    comment.CreatedAt != comment.UpdatedAt,
				CommentType: "issue",
				Reactions:   make(map[string]int),
			}

			// Get author association
			if comment.AuthorAssociation != nil {
				commentCtx.AuthorType = *comment.AuthorAssociation
			}

			// Get reactions
			if comment.Reactions != nil {
				commentCtx.Reactions["+1"] = comment.Reactions.GetPlusOne()
				commentCtx.Reactions["-1"] = comment.Reactions.GetMinusOne()
				commentCtx.Reactions["laugh"] = comment.Reactions.GetLaugh()
				commentCtx.Reactions["confused"] = comment.Reactions.GetConfused()
				commentCtx.Reactions["heart"] = comment.Reactions.GetHeart()
				commentCtx.Reactions["hooray"] = comment.Reactions.GetHooray()
				commentCtx.Reactions["rocket"] = comment.Reactions.GetRocket()
				commentCtx.Reactions["eyes"] = comment.Reactions.GetEyes()
			}

			allComments = append(allComments, commentCtx)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allComments, nil
}

// getAllPRReviews retrieves all reviews on a PR
func (vd *VirtualDeveloper) getAllPRReviews(ctx context.Context, owner, repo string, prNumber int) ([]ReviewContext, error) {
	var allReviews []ReviewContext

	reviews, _, err := vd.githubClient.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, err
	}

	for _, review := range reviews {
		if review == nil {
			continue
		}

		reviewCtx := ReviewContext{
			ID:          review.GetID(),
			Author:      review.User.GetLogin(),
			State:       review.GetState(),
			Body:        review.GetBody(),
			SubmittedAt: review.GetSubmittedAt().Time,
		}

		// Get author association
		if review.AuthorAssociation != nil {
			reviewCtx.AuthorType = *review.AuthorAssociation
		}

		// Get review comments
		if review.ID != nil {
			comments, _, err := vd.githubClient.PullRequests.ListReviewComments(ctx, owner, repo, prNumber, *review.ID, nil)
			if err == nil {
				for _, comment := range comments {
					reviewCtx.Comments = append(reviewCtx.Comments, vd.convertReviewComment(comment, review.ID))
				}
			}
		}

		allReviews = append(allReviews, reviewCtx)
	}

	return allReviews, nil
}

// getAllPRComments retrieves all review comments on a PR
func (vd *VirtualDeveloper) getAllPRComments(ctx context.Context, owner, repo string, prNumber int) ([]ReviewCommentContext, error) {
	var allComments []ReviewCommentContext

	opts := &github.PullRequestListCommentsOptions{
		Sort:      "created",
		Direction: "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		comments, resp, err := vd.githubClient.PullRequests.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, err
		}

		for _, comment := range comments {
			if comment == nil {
				continue
			}

			allComments = append(allComments, vd.convertReviewComment(comment, nil))
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	// Group comments into reply chains
	allComments = vd.buildReplyChains(allComments)

	return allComments, nil
}

// convertReviewComment converts a GitHub review comment to our context
func (vd *VirtualDeveloper) convertReviewComment(comment *github.PullRequestComment, reviewID *int64) ReviewCommentContext {
	commentCtx := ReviewCommentContext{
		CommentContext: CommentContext{
			ID:          comment.GetID(),
			Author:      comment.User.GetLogin(),
			Body:        comment.GetBody(),
			CreatedAt:   comment.GetCreatedAt().Time,
			UpdatedAt:   comment.GetUpdatedAt().Time,
			IsEdited:    comment.CreatedAt != comment.UpdatedAt,
			CommentType: "pr",
			Reactions:   make(map[string]int),
		},
		FilePath: comment.GetPath(),
		Line:     comment.GetLine(),
		Side:     comment.GetSide(),
		DiffHunk: comment.GetDiffHunk(),
		CommitID: comment.GetCommitID(),
		ReviewID: reviewID,
	}

	// Handle multi-line comments
	if comment.StartLine != nil {
		commentCtx.StartLine = comment.StartLine
	}

	// Get author association
	if comment.AuthorAssociation != nil {
		commentCtx.AuthorType = *comment.AuthorAssociation
	}

	// Handle reply references
	if comment.InReplyTo != nil {
		replyTo := int64(*comment.InReplyTo)
		commentCtx.InReplyTo = &replyTo
	}

	// Get reactions
	if comment.Reactions != nil {
		commentCtx.Reactions["+1"] = comment.Reactions.GetPlusOne()
		commentCtx.Reactions["-1"] = comment.Reactions.GetMinusOne()
		commentCtx.Reactions["laugh"] = comment.Reactions.GetLaugh()
		commentCtx.Reactions["confused"] = comment.Reactions.GetConfused()
		commentCtx.Reactions["heart"] = comment.Reactions.GetHeart()
		commentCtx.Reactions["hooray"] = comment.Reactions.GetHooray()
		commentCtx.Reactions["rocket"] = comment.Reactions.GetRocket()
		commentCtx.Reactions["eyes"] = comment.Reactions.GetEyes()
	}

	return commentCtx
}

// buildReplyChains organizes comments into reply chains
func (vd *VirtualDeveloper) buildReplyChains(comments []ReviewCommentContext) []ReviewCommentContext {
	// Create a map for quick lookup
	commentMap := make(map[int64]*ReviewCommentContext)
	for i := range comments {
		commentMap[comments[i].ID] = &comments[i]
	}

	// Build reply chains
	var rootComments []ReviewCommentContext
	for i := range comments {
		if comments[i].InReplyTo != nil {
			// This is a reply, add it to its parent's reply chain
			if parent, ok := commentMap[*comments[i].InReplyTo]; ok {
				parent.ReplyChain = append(parent.ReplyChain, comments[i].CommentContext)
			}
		} else {
			// This is a root comment
			rootComments = append(rootComments, comments[i])
		}
	}

	return rootComments
}

// issueHasPR checks if an issue already has an associated PR
func (vd *VirtualDeveloper) issueHasPR(ctx context.Context, owner, repo string, issueNumber int) bool {
	// Search for PRs that reference this issue
	query := fmt.Sprintf("repo:%s/%s is:pr is:open %d", owner, repo, issueNumber)
	results, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil || results == nil {
		return false
	}

	// Check if any of the PRs were created by the bot
	for _, pr := range results.Issues {
		if pr.User != nil && pr.User.GetLogin() == vd.config.GitHubUsername {
			return true
		}
	}

	return false
}

// needsUpdate checks if a PR needs updates based on its context
func (vd *VirtualDeveloper) needsUpdate(workCtx *WorkContext) bool {
	// Check if there are comments needing responses
	if len(workCtx.NeedsToRespond) > 0 {
		return true
	}

	// Check for change requests
	for _, review := range workCtx.PRReviews {
		if review.State == "CHANGES_REQUESTED" && review.Author != workCtx.BotUsername {
			// Check if this review is newer than our last commit
			// (In a real implementation, we'd check commit timestamps)
			return true
		}
	}

	return false
}

// createPullRequestWithSolution creates a new PR with the solution
func (vd *VirtualDeveloper) createPullRequestWithSolution(ctx context.Context, owner, repo string, issue *github.Issue, solution *Solution) (*github.PullRequest, error) {
	// Get default branch
	repository, _, err := vd.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	defaultBranch := repository.GetDefaultBranch()

	// Get the latest commit on the default branch
	ref, _, err := vd.githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", defaultBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get default branch ref: %w", err)
	}

	// Create new branch
	newRef := &github.Reference{
		Ref:    github.Ptr(fmt.Sprintf("refs/heads/%s", solution.Branch)),
		Object: &github.GitObject{SHA: ref.Object.SHA},
	}

	_, _, err = vd.githubClient.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch: %w", err)
	}

	// Apply the solution to create commits
	if err := vd.applySolutionToNewBranch(ctx, owner, repo, solution, ref.Object.SHA); err != nil {
		return nil, fmt.Errorf("failed to apply solution: %w", err)
	}

	// Create pull request
	issueNumber := 0
	if issue.Number != nil {
		issueNumber = *issue.Number
	}

	issueTitle := "Fix issue"
	if issue.Title != nil {
		issueTitle = *issue.Title
	}

	prTitle := fmt.Sprintf("Fix: %s", issueTitle)
	prBody := fmt.Sprintf(`This PR addresses issue #%d

## Solution
%s

## Changes Made
%s

## Issue Details
%s

---
*This PR was created by the Virtual Developer bot.*`,
		issueNumber,
		solution.Description,
		vd.formatFileChanges(solution.Files),
		getIssueDescription(issue))

	pr := &github.NewPullRequest{
		Title:               github.Ptr(prTitle),
		Body:                github.Ptr(prBody),
		Head:                github.Ptr(solution.Branch),
		Base:                github.Ptr(defaultBranch),
		MaintainerCanModify: github.Bool(true),
	}

	createdPR, _, err := vd.githubClient.PullRequests.Create(ctx, owner, repo, pr)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	return createdPR, nil
}

// applySolutionToNewBranch applies solution files to a new branch
func (vd *VirtualDeveloper) applySolutionToNewBranch(ctx context.Context, owner, repo string, solution *Solution, baseSHA *string) error {
	// Get the base commit
	baseCommit, _, err := vd.githubClient.Git.GetCommit(ctx, owner, repo, *baseSHA)
	if err != nil {
		return fmt.Errorf("failed to get base commit: %w", err)
	}

	// Create tree entries for all changes
	var treeEntries []*github.TreeEntry
	for path, change := range solution.Files {
		// Create blob for file content
		blob := &github.Blob{
			Content:  github.Ptr(change.Content),
			Encoding: github.Ptr("utf-8"),
		}

		createdBlob, _, err := vd.githubClient.Git.CreateBlob(ctx, owner, repo, blob)
		if err != nil {
			return fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		treeEntry := &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"), // Regular file
			Type: github.Ptr("blob"),
			SHA:  createdBlob.SHA,
		}
		treeEntries = append(treeEntries, treeEntry)
	}

	createdTree, _, err := vd.githubClient.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, treeEntries)
	if err != nil {
		return fmt.Errorf("failed to create tree: %w", err)
	}

	commit := &github.Commit{
		Message: github.Ptr(solution.CommitMessage),
		Tree:    createdTree,
		Parents: []*github.Commit{baseCommit},
	}

	createdCommit, _, err := vd.githubClient.Git.CreateCommit(ctx, owner, repo, commit, nil)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference
	branchRef, _, err := vd.githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", solution.Branch))
	if err != nil {
		return fmt.Errorf("failed to get branch ref: %w", err)
	}

	branchRef.Object.SHA = createdCommit.SHA
	_, _, err = vd.githubClient.Git.UpdateRef(ctx, owner, repo, branchRef, false)
	if err != nil {
		return fmt.Errorf("failed to update branch ref: %w", err)
	}

	return nil
}

// postCommentResponses posts responses to comments that need them
func (vd *VirtualDeveloper) postCommentResponses(ctx context.Context, owner, repo string, workCtx *WorkContext, solution *Solution) error {
	// Generate a summary response for the PR
	summaryComment := fmt.Sprintf(`I've updated the PR based on the feedback:

%s

All requested changes have been addressed in the latest commit.`, solution.Description)

	// Post the summary comment on the PR
	if workCtx.PullRequest != nil && workCtx.PullRequest.Number != nil {
		if err := vd.postIssueComment(ctx, owner, repo, *workCtx.PullRequest.Number, summaryComment); err != nil {
			return fmt.Errorf("failed to post summary comment: %w", err)
		}
	}

	// Add reactions to comments we've addressed
	for _, comment := range workCtx.PRReviewComments {
		if comment.NeedsResponse {
			// Add a thumbs up reaction to acknowledge we've seen and addressed it
			vd.addCommentReaction(ctx, owner, repo, comment.ID, "+1")
		}
	}

	return nil
}

// addCommentReaction adds a reaction to a comment
func (vd *VirtualDeveloper) addCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) {
	_, _, err := vd.githubClient.Reactions.CreatePullRequestCommentReaction(ctx, owner, repo, commentID, reaction)
	if err != nil {
		log.Printf("Warning: Failed to add reaction to comment %d: %v", commentID, err)
	}
}

// handleNewIssues checks for issues that don't have PRs yet and creates empty PRs
func (vd *VirtualDeveloper) handleNewIssues() {
	ctx := context.Background()

	// Search for issues assigned to our bot that don't have the in-progress label
	query := fmt.Sprintf("assignee:%s is:issue is:open -label:\"%s\"", vd.config.GitHubUsername, LabelInProgress)
	issues, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching issues: %v", err)
		return
	}

	for _, issue := range issues.Issues {
		if issue == nil || issue.RepositoryURL == nil || issue.Number == nil {
			continue
		}

		// Extract owner and repo from repository URL
		parts := strings.Split(*issue.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		issueTitle := derefOr(issue.Title, "<untitled>")

		log.Printf("Creating PR for issue #%d in %s/%s: %s", *issue.Number, owner, repo, issueTitle)

		// Create empty PR for this issue
		if err := vd.createPRForIssue(ctx, owner, repo, issue); err != nil {
			log.Printf("Error creating PR for issue #%d: %v", *issue.Number, err)
			// Post error comment on issue
			vd.postIssueComment(ctx, owner, repo, *issue.Number,
				fmt.Sprintf("I encountered an error while creating a PR for this issue: %v\n\nI'll retry on the next check cycle.", err))
		}
	}
}

// createPRForIssue creates an empty draft PR for an issue
func (vd *VirtualDeveloper) createPRForIssue(ctx context.Context, owner, repo string, issue *github.Issue) error {
	if issue == nil || issue.Number == nil {
		return fmt.Errorf("invalid issue: missing required fields")
	}

	// Add in-progress label first
	if err := vd.addLabel(ctx, owner, repo, *issue.Number, LabelInProgress); err != nil {
		return fmt.Errorf("failed to add in-progress label: %w", err)
	}

	// Post initial comment
	if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
		"👋 I'm starting work on this issue. I'll create a draft PR and begin analyzing the codebase."); err != nil {
		log.Printf("Warning: failed to post initial comment: %v", err)
	}

	// Create the empty draft PR
	pr, err := vd.createEmptyPullRequest(ctx, owner, repo, issue)
	if err != nil {
		// Remove in-progress label if we failed
		vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)
		return fmt.Errorf("failed to create empty pull request: %w", err)
	}

	// Post success comment on issue with PR link
	if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
		fmt.Sprintf("I've created PR #%d for this issue. I'll analyze the codebase and push my solution shortly.", *pr.Number)); err != nil {
		log.Printf("Warning: Failed to post PR creation comment: %v", err)
	}

	return nil
}

// Simplified createEmptyPullRequest - only creates the PR, no solution generation
func (vd *VirtualDeveloper) createEmptyPullRequest(ctx context.Context, owner, repo string, issue *github.Issue) (*github.PullRequest, error) {
	// Get default branch
	repository, _, err := vd.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository: %w", err)
	}

	defaultBranch := repository.GetDefaultBranch()

	// Generate branch name
	issueNumber := 0
	if issue.Number != nil {
		issueNumber = *issue.Number
	}

	issueTitle := "untitled"
	if issue.Title != nil {
		issueTitle = *issue.Title
	}

	// Create a safe branch name
	branchName := fmt.Sprintf("fix/issue-%d-%s", issueNumber, sanitizeBranchName(issueTitle))

	// Get the latest commit on the default branch
	ref, _, err := vd.githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", defaultBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to get default branch ref: %w", err)
	}

	// Create new branch from default branch
	newRef := &github.Reference{
		Ref:    github.Ptr(fmt.Sprintf("refs/heads/%s", branchName)),
		Object: &github.GitObject{SHA: ref.Object.SHA},
	}

	_, _, err = vd.githubClient.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch: %w", err)
	}

	// Create draft pull request
	prTitle := fmt.Sprintf("Fix: %s", issueTitle)
	prBody := fmt.Sprintf(`This PR addresses issue #%d

## Status
🔄 **In Progress** - I'm currently analyzing the codebase and will push a solution soon.

## Issue Details
%s

---
*This PR was created by the Virtual Developer bot.*`,
		issueNumber, getIssueDescription(issue))

	pr := &github.NewPullRequest{
		Title:               github.Ptr(prTitle),
		Body:                github.Ptr(prBody),
		Head:                github.Ptr(branchName),
		Base:                github.Ptr(defaultBranch),
		Draft:               github.Bool(true), // Create as draft initially
		MaintainerCanModify: github.Bool(true),
	}

	createdPR, _, err := vd.githubClient.PullRequests.Create(ctx, owner, repo, pr)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	return createdPR, nil
}

// Helper function to sanitize branch names
func sanitizeBranchName(title string) string {
	// Convert to lowercase and replace spaces with hyphens
	safe := strings.ToLower(title)
	safe = strings.ReplaceAll(safe, " ", "-")

	// Remove any characters that aren't alphanumeric or hyphens
	var result strings.Builder
	for _, ch := range safe {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			result.WriteRune(ch)
		}
	}

	// Limit length and trim trailing hyphens
	name := result.String()
	if len(name) > 50 {
		name = name[:50]
	}
	name = strings.Trim(name, "-")

	// Ensure we have something
	if name == "" {
		name = "fix"
	}

	return name
}

// Helper function to format file changes for PR description
func (vd *VirtualDeveloper) formatFileChanges(files map[string]FileChange) string {
	var changes []string
	for path, change := range files {
		action := "Modified"
		if change.IsNew {
			action = "Added"
		}
		changes = append(changes, fmt.Sprintf("- %s `%s`", action, path))
	}
	return strings.Join(changes, "\n")
}

// addLabel adds a label to an issue
func (vd *VirtualDeveloper) addLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error {
	// Ensure the label exists in the repository
	if err := vd.ensureLabelExists(ctx, owner, repo, label); err != nil {
		log.Printf("Warning: Could not ensure label exists: %v", err)
	}

	labels := []string{label}
	_, _, err := vd.githubClient.Issues.AddLabelsToIssue(ctx, owner, repo, issueNumber, labels)
	return err
}

// removeLabel removes a label from an issue
func (vd *VirtualDeveloper) removeLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error {
	_, err := vd.githubClient.Issues.RemoveLabelForIssue(ctx, owner, repo, issueNumber, label)
	return err
}

// ensureLabelExists creates a label if it doesn't exist
func (vd *VirtualDeveloper) ensureLabelExists(ctx context.Context, owner, repo, labelName string) error {
	// Check if label already exists
	_, _, err := vd.githubClient.Issues.GetLabel(ctx, owner, repo, labelName)
	if err == nil {
		return nil // Label already exists
	}

	// Create the label
	label := &github.Label{
		Name:  github.Ptr(labelName),
		Color: github.Ptr("0366d6"), // Blue color
	}

	if labelName == LabelInProgress {
		label.Color = github.Ptr("fbca04") // Yellow for in-progress
		label.Description = github.Ptr("Issue is being worked on by the virtual developer")
	} else if labelName == LabelCompleted {
		label.Color = github.Ptr("28a745") // Green for completed
		label.Description = github.Ptr("Issue has been addressed by the virtual developer")
	}

	_, _, err = vd.githubClient.Issues.CreateLabel(ctx, owner, repo, label)
	return err
}

// findStyleGuides searches for coding style documentation
func (vd *VirtualDeveloper) findStyleGuides(ctx context.Context, owner, repo string) (*StyleGuide, error) {
	styleGuide := &StyleGuide{
		RepoStyle: make(map[string]string),
	}

	// Common style guide file patterns
	patterns := []string{
		"CONTRIBUTING.md",
		"CONTRIBUTING",
		"STYLE.md",
		"CODING_STYLE.md",
		"docs/style-guide.md",
		"docs/coding-standards.md",
		".github/CONTRIBUTING.md",
		"DEVELOPMENT.md",
	}

	for _, pattern := range patterns {
		content, _, _, err := vd.githubClient.Repositories.GetContents(ctx, owner, repo, pattern, nil)
		if err == nil && content != nil {
			decodedContent, err := content.GetContent()
			if err == nil {
				styleGuide.Content += fmt.Sprintf("\n\n--- %s ---\n%s", pattern, decodedContent)
				styleGuide.FilePath = pattern
			}
		}
	}

	// Also check for language-specific config files
	configFiles := map[string][]string{
		"go":         {".golangci.yml", "go.mod"},
		"javascript": {".eslintrc", ".prettierrc", "package.json"},
		"python":     {".flake8", "setup.cfg", "pyproject.toml"},
		"rust":       {"rustfmt.toml", ".rustfmt.toml"},
	}

	for lang, files := range configFiles {
		for _, file := range files {
			content, _, _, err := vd.githubClient.Repositories.GetContents(ctx, owner, repo, file, nil)
			if err == nil && content != nil {
				decodedContent, err := content.GetContent()
				if err == nil {
					styleGuide.RepoStyle[lang] = decodedContent
				}
			}
		}
	}

	if styleGuide.Content == "" && len(styleGuide.RepoStyle) == 0 {
		return nil, fmt.Errorf("no style guides found")
	}

	return styleGuide, nil
}

// analyzeCodebase examines the repository structure
func (vd *VirtualDeveloper) analyzeCodebase(ctx context.Context, owner, repo string) (*CodebaseInfo, error) {
	info := &CodebaseInfo{
		PackageInfo: make(map[string]string),
	}

	// Get repository languages
	languages, _, err := vd.githubClient.Repositories.ListLanguages(ctx, owner, repo)
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

	// Get file tree (limited to avoid rate limits)
	tree, _, err := vd.githubClient.Git.GetTree(ctx, owner, repo, "HEAD", false)
	if err != nil {
		log.Printf("Warning: Could not get file tree: %v", err)
	} else {
		for _, entry := range tree.Entries {
			if entry.Path != nil {
				info.FileTree = append(info.FileTree, *entry.Path)
			}
		}
	}

	// Get README
	readme, _, err := vd.githubClient.Repositories.GetReadme(ctx, owner, repo, nil)
	if err == nil {
		content, err := readme.GetContent()
		if err == nil {
			info.ReadmeContent = content
		}
	}

	return info, nil
}

// fetchFileContent fetches file content from GitHub
func (vd *VirtualDeveloper) fetchFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	fileContent, _, _, err := vd.githubClient.Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get file contents: %w", err)
	}

	if fileContent == nil {
		return "", fmt.Errorf("file not found")
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return content, nil
}

// Helper function to truncate strings
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// checkForUnaddressedFeedback checks if there's feedback that needs addressing
func (vd *VirtualDeveloper) checkForUnaddressedFeedback(ctx context.Context, owner, repo string, prNumber int) (bool, []string) {
	var feedback []string

	// Get recent comments (last 10)
	comments, _, err := vd.githubClient.Issues.ListComments(ctx, owner, repo, prNumber, &github.IssueListCommentsOptions{
		Sort:      github.Ptr("created"),
		Direction: github.Ptr("desc"),
		ListOptions: github.ListOptions{
			PerPage: 10,
		},
	})
	if err == nil {
		for _, comment := range comments {
			// Skip our own comments
			if comment.User != nil && *comment.User.Login == vd.config.GitHubUsername {
				continue
			}
			if comment.Body != nil {
				feedback = append(feedback, *comment.Body)
			}
		}
	}

	// Get review comments
	reviews, _, err := vd.githubClient.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err == nil {
		for _, review := range reviews {
			if review.State != nil && (*review.State == "CHANGES_REQUESTED" || *review.State == "COMMENTED") {
				if review.Body != nil && *review.Body != "" {
					feedback = append(feedback, *review.Body)
				}

				// Get individual review comments
				reviewComments, _, err := vd.githubClient.PullRequests.ListReviewComments(ctx, owner, repo, prNumber, *review.ID, nil)
				if err == nil {
					for _, comment := range reviewComments {
						if comment.Body != nil {
							feedback = append(feedback, *comment.Body)
						}
					}
				}
			}
		}
	}

	return len(feedback) > 0, feedback
}

// updatePullRequest applies updates to an existing PR
func (vd *VirtualDeveloper) updatePullRequest(ctx context.Context, owner, repo string, pr *github.PullRequest, solution *Solution) error {
	if pr.Head == nil || pr.Head.Ref == nil {
		return fmt.Errorf("invalid PR head reference")
	}

	branchName := *pr.Head.Ref

	// Get current branch reference
	ref, _, err := vd.githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", branchName))
	if err != nil {
		return fmt.Errorf("failed to get branch ref: %w", err)
	}

	// Get the current commit
	currentCommit, _, err := vd.githubClient.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return fmt.Errorf("failed to get current commit: %w", err)
	}

	// Create tree entries for all changes
	var treeEntries []*github.TreeEntry
	for path, change := range solution.Files {
		// Create blob for file content
		blob := &github.Blob{
			Content:  github.Ptr(change.Content),
			Encoding: github.Ptr("utf-8"),
		}

		createdBlob, _, err := vd.githubClient.Git.CreateBlob(ctx, owner, repo, blob)
		if err != nil {
			return fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		treeEntry := &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  createdBlob.SHA,
		}
		treeEntries = append(treeEntries, treeEntry)
	}

	createdTree, _, err := vd.githubClient.Git.CreateTree(ctx, owner, repo, *currentCommit.Tree.SHA, treeEntries)
	if err != nil {
		return fmt.Errorf("failed to create tree: %w", err)
	}

	// Create new commit
	commit := &github.Commit{
		Message: github.Ptr(solution.CommitMessage),
		Tree:    createdTree,
		Parents: []*github.Commit{currentCommit},
	}

	createdCommit, _, err := vd.githubClient.Git.CreateCommit(ctx, owner, repo, commit, nil)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference
	ref.Object.SHA = createdCommit.SHA
	_, _, err = vd.githubClient.Git.UpdateRef(ctx, owner, repo, ref, false)
	if err != nil {
		return fmt.Errorf("failed to update branch ref: %w", err)
	}

	return nil
}

// postComprehensiveReview posts a single review addressing all feedback
func (vd *VirtualDeveloper) postComprehensiveReview(ctx context.Context, owner, repo string, prNumber int, feedback []string, solution *Solution) {
	reviewBody := fmt.Sprintf(`Thank you for the feedback! I've updated the PR to address all the points raised:

%s

The changes have been committed and should address all the concerns mentioned. Please let me know if you need any further adjustments.`, solution.Description)

	review := &github.PullRequestReviewRequest{
		Body:  github.Ptr(reviewBody),
		Event: github.Ptr("COMMENT"),
	}

	_, _, err := vd.githubClient.PullRequests.CreateReview(ctx, owner, repo, prNumber, review)
	if err != nil {
		log.Printf("Error posting comprehensive review: %v", err)
	}
}

// Helper functions

// extractIssueNumber extracts issue number from PR body
func extractIssueNumber(body string) int {
	// Look for patterns like "#123" or "issue #123"
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.Contains(line, "#") {
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.HasPrefix(part, "#") {
					var num int
					if _, err := fmt.Sscanf(part, "#%d", &num); err == nil {
						return num
					}
				}
			}
		}
	}
	return 0
}

// getIssueDescription safely gets issue description
func getIssueDescription(issue *github.Issue) string {
	if issue == nil {
		return "No issue provided"
	}

	description := ""
	if issue.Title != nil {
		description += "Title: " + *issue.Title + "\n"
	}
	if issue.Body != nil {
		description += "Description: " + *issue.Body
	}

	return description
}

// postIssueComment posts a comment on an issue or PR
func (vd *VirtualDeveloper) postIssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	comment := &github.IssueComment{
		Body: github.Ptr(body),
	}

	_, _, err := vd.githubClient.Issues.CreateComment(ctx, owner, repo, number, comment)
	return err
}

func derefOr[T any](ptr *T, fallback T) T {
	if ptr != nil {
		return *ptr
	}
	return fallback
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
