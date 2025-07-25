package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/go-github/v72/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
)

// Config holds the configuration for the bot
type Config struct {
	GitHubToken               string
	AnthropicAPIKey           string
	GitHubUsername            string
	ResumableConversationsDir string
	CheckInterval             time.Duration
}

// VirtualDeveloper represents our bot
type VirtualDeveloper struct {
	config                 *Config
	githubClient           *github.Client
	anthropicClient        anthropic.Client
	toolRegistry           *ToolRegistry
	fileSystemFactory      githubFileSystemFactory
	resumableConversations FileSystemConversationHistoryStore
	botName                string
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

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	config := &Config{
		GitHubToken:               os.Getenv("GITHUB_TOKEN"),
		AnthropicAPIKey:           os.Getenv("ANTHROPIC_API_KEY"),
		GitHubUsername:            os.Getenv("GITHUB_USERNAME"),
		ResumableConversationsDir: os.Getenv("RESUMABLE_CONVERSATIONS_DIR"),
		CheckInterval:             5 * time.Minute, // Default
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

	log.Printf("Bot started. Monitoring issues for @%s every %s", config.GitHubUsername, config.CheckInterval)

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
	githubClient := github.NewClient(tc)
	rateLimitedHTTPClient := &http.Client{
		Transport: WithRateLimiting(nil),
	}
	anthropicClient := anthropic.NewClient(
		option.WithHTTPClient(rateLimitedHTTPClient),
		option.WithAPIKey(config.AnthropicAPIKey),
		option.WithMaxRetries(5),
	)

	return &VirtualDeveloper{
		config:                 config,
		githubClient:           githubClient,
		anthropicClient:        anthropicClient,
		toolRegistry:           NewToolRegistry(),
		fileSystemFactory:      githubFileSystemFactory{githubClient: githubClient},
		resumableConversations: FileSystemConversationHistoryStore{dir: config.ResumableConversationsDir},
		botName:                config.GitHubUsername,
	}
}

type githubFileSystemFactory struct {
	githubClient *github.Client
}

func (gfsf *githubFileSystemFactory) NewFileSystem(owner, repo, branch string) (*GitHubFileSystem, error) {
	return NewGitHubFileSystem(gfsf.githubClient, owner, repo, branch)
}

// Run starts the main loop
func (vd *VirtualDeveloper) Run() {
	ctx := context.Background()

	for {
		vd.checkAndProcessWorkItems(ctx)
		log.Printf("Sleeping for %s", vd.config.CheckInterval)
		time.Sleep(vd.config.CheckInterval)
	}
}

// checkAndProcessWorkItems checks for work items that need attention
func (vd *VirtualDeveloper) checkAndProcessWorkItems(ctx context.Context) {
	// Search for issues assigned to the bot that are not being worked on and are not blocked
	query := fmt.Sprintf("assignee:%s is:issue is:open -label:%s -label:%s", vd.config.GitHubUsername, *LabelWorking.Name, *LabelBlocked.Name)
	result, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching issues: %v", err)
		return
	}
	log.Printf("Found %d issue(s)", len(result.Issues))

	for _, issue := range result.Issues {
		if issue == nil || issue.RepositoryURL == nil || issue.Number == nil {
			log.Print("Warning: unexpected nil")
			continue
		}

		// Extract owner and repo
		parts := strings.Split(*issue.RepositoryURL, "/")
		if len(parts) < 2 {
			log.Print("Warning: failed to parse repo URL")
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		log.Printf("Processing issue #%d in %s/%s: %s (%s)", *issue.Number, owner, repo, *issue.Title, *issue.URL)

		// Process this issue
		if err := vd.processIssue(ctx, owner, repo, issue); err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
		}
	}
}

// processIssue processes a single issue
func (vd *VirtualDeveloper) processIssue(ctx context.Context, owner, repo string, issue *github.Issue) (err error) {
	// Add in-progress label
	if err := vd.addLabel(ctx, owner, repo, *issue.Number, LabelWorking); err != nil {
		return fmt.Errorf("failed to add in-progress label: %w", err)
	}
	// Remove in-progress label when done
	defer func() {
		vd.removeLabel(ctx, owner, repo, *issue.Number, LabelWorking)
		if err != nil {
			// Add blocked label if there is an error, to tell the bot not to pick up this item again
			vd.addLabel(ctx, owner, repo, *issue.Number, LabelBlocked)
			// Post sanitized error comment
			vd.postIssueComment(ctx, owner, repo, *issue.Number,
				"❌ I encountered an error while working on this issue.")
		}
	}()

	botUser, _, err := vd.githubClient.Users.Get(ctx, vd.botName)
	if err != nil {
		return fmt.Errorf("failed to get bot user: %w", err)
	}

	// Build work context
	workCtx, err := vd.buildWorkContext(ctx, owner, repo, issue, botUser)
	if err != nil {
		return fmt.Errorf("failed to build work context: %w", err)
	}

	// Create work branch, if it doesn't already exist
	err = createBranch(vd.githubClient, owner, repo, workCtx.TargetBranch, workCtx.WorkBranch)

	// Check if we need to do anything
	if !vd.needsAttention(*workCtx) {
		log.Printf("issue does not require attention")
		return nil
	}

	// Let AI decide what to do with text editor tool support
	err = vd.processWithAI(ctx, *workCtx, owner, repo)
	if err != nil {
		return fmt.Errorf("failed to process with AI: %w", err)
	}

	return nil
}

func getWorkBranchName(issue *github.Issue) string {
	var branchName string
	if issue.Title != nil {
		branchName = fmt.Sprintf("fix/issue-%d-%s", *issue.Number, sanitizeForBranchName(*issue.Title))
	} else {
		branchName = fmt.Sprintf("fix/issue-%d", *issue.Number)
	}

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

// CreateBranch creates a new branch from the default branch
func createBranch(client *github.Client, owner, repo, baseBranch, newBranch string) error {
	ctx := context.Background()

	// Get the base branch reference
	baseRef, _, err := client.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", baseBranch))
	if err != nil {
		return fmt.Errorf("failed to get base branch ref: %w", err)
	}

	// Create new branch reference
	newRef := &github.Reference{
		Ref:    github.Ptr(fmt.Sprintf("refs/heads/%s", newBranch)),
		Object: &github.GitObject{SHA: baseRef.Object.SHA},
	}

	_, _, err = client.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

// TODO look into merging owner, repo, and branch into workCtx
// processWithAI handles the AI interaction with text editor tool support
func (vd *VirtualDeveloper) processWithAI(ctx context.Context, workCtx workContext, owner, repo string) error {
	maxIterations := 50

	// fs may be nil if no branch name is given, e.g. if the issue is currently in the requirements clarification phase
	fs, err := vd.fileSystemFactory.NewFileSystem(owner, repo, workCtx.WorkBranch)
	if err != nil {
		return fmt.Errorf("failed to create file system: %w", err)
	}

	// Create tool context
	toolCtx := &ToolContext{
		FileSystem:   fs,
		Owner:        owner,
		Repo:         repo,
		WorkContext:  workCtx,
		GithubClient: vd.githubClient,
	}

	// Initialize conversation
	conversation, response, err := vd.initConversation(ctx, workCtx, toolCtx)
	if err != nil {
		return fmt.Errorf("failed to initialize conversation: %w", err)
	}

	i := 0
	for response.StopReason != anthropic.StopReasonEndTurn {
		if i > maxIterations {
			return fmt.Errorf("exceeded maximum iterations (%d) without completion", maxIterations)
		}
		// Persist the conversation history up to this point
		vd.resumableConversations.Set(strconv.Itoa(*workCtx.Issue.Number), conversation.History())

		log.Printf("Processing AI response, iteration: %d", i+1)
		for _, contentBlock := range response.Content {
			switch block := contentBlock.AsAny().(type) {
			case anthropic.TextBlock:
				log.Print("    <text> ", block.Text)
			case anthropic.ToolUseBlock:
				log.Print("    <tool use> ", block.Name)
			case anthropic.ServerToolUseBlock:
				log.Print("    <server tool use> ", block.Name)
			case anthropic.WebSearchToolResultBlock:
				log.Print("    <web search tool result>")
			case anthropic.ThinkingBlock:
				log.Print("    <thinking>", block.Thinking)
			case anthropic.RedactedThinkingBlock:
				log.Print("    <redacted thinking>")
			default:
				log.Print("    <unknown>")
			}
		}

		switch response.StopReason {
		case anthropic.StopReasonToolUse:
			// Process tool uses and collect tool results
			toolUses := []anthropic.ToolUseBlock{}
			for _, content := range response.Content {
				switch block := content.AsAny().(type) {
				case anthropic.ToolUseBlock:
					toolUses = append(toolUses, block)
				}
			}

			toolResults := []anthropic.ContentBlockParamUnion{}
			for _, toolUse := range toolUses {
				log.Printf("    Executing tool: %s", toolUse.Name)

				// Process the tool use with the registry
				toolResult, err := vd.toolRegistry.ProcessToolUse(toolUse, toolCtx)
				if err != nil {
					return fmt.Errorf("failed to process tool use: %w", err)
				}
				toolResults = append(toolResults, anthropic.ContentBlockParamUnion{OfToolResult: toolResult})
			}
			log.Printf("    Sending tool results to AI and streaming response")
			response, err = conversation.SendMessage(ctx, toolResults...)
			if err != nil {
				return fmt.Errorf("failed to send tool results to AI: %w", err)
			}
		case anthropic.StopReasonMaxTokens:
			return fmt.Errorf("exceeded max tokens")
		case anthropic.StopReasonRefusal:
			return fmt.Errorf("the AI refused to generate a response due to safety concerns")
		case anthropic.StopReasonEndTurn:
			return fmt.Errorf("that's weird, it shouldn't be possible to reach this branch")
		default:
			return fmt.Errorf("unexpected stop reason: %v", response.StopReason)
		}

		i++
	}

	// We're done! Delete the conversation history so that we don't try to resume it later
	err = vd.resumableConversations.Delete(strconv.Itoa(*workCtx.Issue.Number))
	if err != nil {
		return fmt.Errorf("failed to delete conversation history for concluded conversation: %w", err)
	}

	log.Print("AI interaction concluded")
	return nil
}

// Helper functions

// needsAttention checks if a work item needs AI attention
func (vd *VirtualDeveloper) needsAttention(workCtx workContext) bool {
	if len(workCtx.IssueComments) == 0 && workCtx.PullRequest == nil {
		// If there are no issue comments and no pull request, this is a brand new issue and requires our attention
		return true
	}
	// Check if there are comments needing responses
	if len(workCtx.IssueCommentsRequiringResponses) > 0 ||
		len(workCtx.PRCommentsRequiringResponses) > 0 ||
		len(workCtx.PRReviewCommentsRequiringResponses) > 0 {

		return true
	}

	return false
}

// GitHub API helper functions

// pickIssueCommentsRequiringResponse gets regular issue/PR comments that haven't been reacted to by the bot
func (vd *VirtualDeveloper) pickIssueCommentsRequiringResponse(ctx context.Context, owner, repo string, comments []*github.IssueComment, botUser *github.User) ([]*github.IssueComment, error) {
	var commentsRequiringResponse []*github.IssueComment

	for _, comment := range comments {
		// Skip if this is the bot's own comment
		if vd.isBotComment(comment.User, botUser) {
			continue
		}

		// Check if bot has reacted to this comment
		hasReacted, err := vd.hasBotReactedToIssueComment(ctx, owner, repo, *comment.ID, botUser)
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
func (vd *VirtualDeveloper) pickPRReviewCommentsRequiringResponse(ctx context.Context, owner, repo string, commentThreads [][]*github.PullRequestComment, botUser *github.User) ([]*github.PullRequestComment, error) {
	var commentsRequiringResponse []*github.PullRequestComment

	for _, thread := range commentThreads {
		// Look at every comment, not just the last comment in each thread. Multiple replies may have been added to a
		// chain since the bot last looked at it, and for other contributors' peace of mind the bot should explicitly
		// acknolwedge that it has seen every comment in the chain, even if it only replied to the last one
		for _, comment := range thread {
			// Skip if this is the bot's own comment
			if vd.isBotComment(comment.User, botUser) {
				continue
			}

			// Check if bot has reacted to this comment
			hasReacted, err := vd.hasBotReactedToReviewComment(ctx, owner, repo, *comment.ID, botUser)
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
func (vd *VirtualDeveloper) isBotComment(commentUser, botUser *github.User) bool {
	return commentUser != nil && botUser.Login != nil &&
		commentUser.Login != nil && *commentUser.Login == *botUser.Login
}

// hasBotReactedToIssueComment checks if the bot has reacted to an issue comment
func (vd *VirtualDeveloper) hasBotReactedToIssueComment(ctx context.Context, owner, repo string, commentID int64, botUser *github.User) (bool, error) {
	if botUser.Login == nil {
		return false, nil
	}

	reactions, _, err := vd.githubClient.Reactions.ListIssueCommentReactions(ctx, owner, repo, commentID, nil)
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
func (vd *VirtualDeveloper) hasBotReactedToReviewComment(ctx context.Context, owner, repo string, commentID int64, botUser *github.User) (bool, error) {
	if botUser.Login == nil {
		return false, nil
	}

	reactions, _, err := vd.githubClient.Reactions.ListPullRequestCommentReactions(ctx, owner, repo, commentID, nil)
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

func (vd *VirtualDeveloper) postIssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	comment := &github.IssueComment{
		Body: github.Ptr(body),
	}
	_, _, err := vd.githubClient.Issues.CreateComment(ctx, owner, repo, number, comment)
	return err
}

// Label management functions

// addLabel adds a label to an issue
func (vd *VirtualDeveloper) addLabel(ctx context.Context, owner, repo string, issueNumber int, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot add label with nil name")
	}
	if err := vd.ensureLabelExists(ctx, owner, repo, label); err != nil {
		log.Printf("Warning: Could not ensure label exists: %v", err)
	}

	labels := []string{*label.Name}
	_, _, err := vd.githubClient.Issues.AddLabelsToIssue(ctx, owner, repo, issueNumber, labels)
	return err
}

// removeLabel removes a label from an issue
func (vd *VirtualDeveloper) removeLabel(ctx context.Context, owner, repo string, issueNumber int, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("cannot remove label with nil name")
	}
	_, err := vd.githubClient.Issues.RemoveLabelForIssue(ctx, owner, repo, issueNumber, *label.Name)
	return err
}

// ensureLabelExists creates a label if it doesn't exist
func (vd *VirtualDeveloper) ensureLabelExists(ctx context.Context, owner, repo string, label github.Label) error {
	if label.Name == nil {
		return fmt.Errorf("nil label name")
	}
	_, _, err := vd.githubClient.Issues.GetLabel(ctx, owner, repo, *label.Name)
	if err == nil {
		return nil
	}

	_, _, err = vd.githubClient.Issues.CreateLabel(ctx, owner, repo, &label)
	return err
}

// Context building functions

// buildWorkContext creates a complete work context
func (vd *VirtualDeveloper) buildWorkContext(ctx context.Context, owner, repo string, issue *github.Issue, botUser *github.User) (*workContext, error) {
	workCtx := workContext{
		BotUsername: vd.config.GitHubUsername,
		Issue:       issue,
	}

	repoInfo, _, err := vd.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repo info: %w", err)
	}
	if repoInfo.DefaultBranch == nil {
		return nil, fmt.Errorf("nil default branch")
	}

	workCtx.TargetBranch = *repoInfo.DefaultBranch
	// We'll use this branch name to implicitly link the issue and the pull request 1-1
	workCtx.WorkBranch = getWorkBranchName(issue)

	// Get the existing pull request, if any
	pr, err := getPullRequest(ctx, vd.githubClient, owner, repo, workCtx.WorkBranch, workCtx.BotUsername)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request for branch: %w", err)
	}
	workCtx.PullRequest = pr

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
	if issue.Number != nil {
		comments, err := vd.getAllIssueComments(ctx, owner, repo, *issue.Number)
		if err != nil {
			log.Printf("Warning: Could not get issue comments: %v", err)
		}
		workCtx.IssueComments = comments
	}

	// If there is a PR, get PR comments, reviews, and review comments
	if pr != nil && pr.Number != nil {
		// Get PR comments
		comments, err := vd.getAllIssueComments(ctx, owner, repo, *pr.Number)
		if err != nil {
			return nil, fmt.Errorf("could not get pull request comments: %w", err)
		}
		workCtx.PRComments = comments

		// Get reviews
		reviews, err := vd.getAllPRReviews(ctx, owner, repo, *pr.Number)
		if err != nil {
			return nil, fmt.Errorf("could not get PR reviews: %w", err)
		}
		workCtx.PRReviews = reviews

		// Get PR review comment threads
		reviewComments, err := vd.getAllPRReviewComments(ctx, owner, repo, *pr.Number)
		if err != nil {
			return nil, fmt.Errorf("could not get PR comments: %w", err)
		}
		reviewCommentThreads, err := organizePRReviewCommentsIntoThreads(reviewComments)
		if err != nil {
			return nil, fmt.Errorf("could not organize review comments into threads: %w", err)
		}

		workCtx.PRReviewCommentThreads = reviewCommentThreads
	}

	// Get comments requiring responses
	commentsReq, err := vd.pickIssueCommentsRequiringResponse(ctx, owner, repo, workCtx.IssueComments, botUser)
	if err != nil {
		return nil, fmt.Errorf("could not get issue comments requiring response: %w", err)
	}
	prCommentsReq, err := vd.pickIssueCommentsRequiringResponse(ctx, owner, repo, workCtx.PRComments, botUser)
	if err != nil {
		return nil, fmt.Errorf("could not get PR comments requiring response: %w", err)
	}
	prReviewCommentsReq, err := vd.pickPRReviewCommentsRequiringResponse(ctx, owner, repo, workCtx.PRReviewCommentThreads, botUser)
	if err != nil {
		return nil, fmt.Errorf("could not get PR review comments requiring response: %w", err)
	}
	workCtx.IssueCommentsRequiringResponses = commentsReq
	workCtx.PRCommentsRequiringResponses = prCommentsReq
	workCtx.PRReviewCommentsRequiringResponses = prReviewCommentsReq

	return &workCtx, nil
}

// getPullRequest returns a pull request by source branch and owner, if exactly one such pull request exists. If no such
// pull request exists, returns (nil, nil). If more than one such pull request exists, returns an error
func getPullRequest(ctx context.Context, githubClient *github.Client, owner, repo, branch, author string) (*github.PullRequest, error) {
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

	return pr, nil
}

// Repository analysis functions

// findStyleGuides searches for coding style documentation
func (vd *VirtualDeveloper) findStyleGuides(ctx context.Context, owner, repo string) (*StyleGuide, error) {
	styleGuide := &StyleGuide{
		RepoStyle: make(map[string]string),
	}

	patterns := []string{
		"CONTRIBUTING.md",
		"STYLE.md",
		"CODING_STYLE.md",
		".github/CONTRIBUTING.md",
		"docs/CONTRIBUTING.md",
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

	if styleGuide.Content == "" {
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

	// Get file tree
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

// Comment retrieval functions

// getAllIssueComments retrieves all comments on an issue
func (vd *VirtualDeveloper) getAllIssueComments(ctx context.Context, owner, repo string, issueNumber int) ([]*github.IssueComment, error) {
	var allComments []*github.IssueComment

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
		allComments = append(allComments, comments...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allComments, nil
}

// getAllPRReviews retrieves all reviews on a PR, sorted chronologically
func (vd *VirtualDeveloper) getAllPRReviews(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestReview, error) {
	var allReviews []*github.PullRequestReview

	reviews, _, err := vd.githubClient.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
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
func (vd *VirtualDeveloper) getAllPRReviewComments(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestComment, error) {
	var allComments []*github.PullRequestComment

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
			if comment == nil || comment.ID == nil {
				log.Println("Warning: comment or comment.ID unexpectedly nil")
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

// Utility functions

//go:embed system_prompt.md
var systemPrompt string

// initConversation either constructs a new conversation or resumes a previous conversation
func (vd *VirtualDeveloper) initConversation(ctx context.Context, workCtx workContext, toolCtx *ToolContext) (*ClaudeConversation, *anthropic.Message, error) {
	model := anthropic.ModelClaudeSonnet4_0
	var maxTokens int64 = 64000

	conversationStr, err := vd.resumableConversations.Get(strconv.Itoa(*workCtx.Issue.Number))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to look up resumable conversation by issue number: %w", err)
	}
	tools := vd.toolRegistry.GetAllToolParams()

	if conversationStr != nil {
		conv, err := ResumeClaudeConversation(vd.anthropicClient, *conversationStr, model, maxTokens, tools)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resume conversation: %w", err)
		}

		err = vd.rerunStatefulToolCalls(ctx, workCtx, toolCtx, conv)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to rerun stateful tool calls: %w", err)
		}

		// Extract the last message of the resumed conversation. If it is a user message, send it and return the
		// response. If it is an assistant response, simply return that
		lastTurn := conv.messages[len(conv.messages)-1]
		var response *anthropic.Message
		if lastTurn.Response != nil {
			// We should be careful here. Assistant message handling is not necessarily idempotent, e.g. if the bot
			// sends a message with two tool calls and we get through one of them before encountering an error with the
			// second, the handling of the first tool call may have had side effects that would be damaging to repeat.
			// Consider implementing transactions with rollback for parallel tool calls.

			// Resuming from a response
			log.Printf("Resuming previous conversation from an assistant message")
			response = lastTurn.Response
		} else {
			// Resuming from a user message
			log.Printf("Resuming previous conversation from a user message - sending message")
			r, err := conv.SendMessage(ctx, lastTurn.UserMessage.Content...)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to send last message of resumed conversation: %w", err)
			}
			response = r
		}
		return conv, response, nil
	} else {
		c := NewClaudeConversation(vd.anthropicClient, model, maxTokens, tools, systemPrompt)

		log.Printf("Sending initial message to AI")
		promptPtr, err := BuildPrompt(workCtx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build prompt: %w", err)
		}
		// Send initial message with a cache breakpoint, because the initial message tends to be very large and we are
		// likely to need several back-and-forths after this
		response, err := c.SendMessageAndSetCachePoint(ctx, anthropic.NewTextBlock(*promptPtr))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to send initial message to AI: %w", err)
		}
		return c, response, nil
	}
}

func (vd *VirtualDeveloper) rerunStatefulToolCalls(ctx context.Context, workCtx workContext, toolCtx *ToolContext, conversation *ClaudeConversation) error {
	for turnNumber, turn := range conversation.messages {
		if turnNumber == len(conversation.messages)-1 {
			// Skip the last message in the conversation, since this message was not previously handled
			break
		}
		for _, block := range turn.Response.Content {
			switch toolUseBlock := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				err := vd.toolRegistry.ReplayToolUse(toolUseBlock, toolCtx)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
