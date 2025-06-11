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
	WorkspaceDir    string // Directory for cloning repositories
}

// VirtualDeveloper represents our bot
type VirtualDeveloper struct {
	config            *Config
	githubClient      *github.Client
	anthropicClient   *AnthropicClient
	toolRegistry      *ToolRegistry
	fileSystemFactory githubFileSystemFactory
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
		WorkspaceDir:    os.Getenv("WORKSPACE_DIR"),
	}

	if config.GitHubToken == "" || config.AnthropicAPIKey == "" || config.GitHubUsername == "" {
		log.Fatal("Missing required environment variables: GITHUB_TOKEN, ANTHROPIC_API_KEY, or GITHUB_USERNAME")
	}

	if config.WorkspaceDir == "" {
		config.WorkspaceDir = "/tmp/virtual-dev-workspace"
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
	githubClient := github.NewClient(tc)

	return &VirtualDeveloper{
		config:            config,
		githubClient:      githubClient,
		anthropicClient:   NewAnthropicClient(config.AnthropicAPIKey),
		toolRegistry:      NewToolRegistry(),
		fileSystemFactory: githubFileSystemFactory{githubClient: githubClient},
	}
}

type githubFileSystemFactory struct {
	githubClient *github.Client
}

func (gfsf *githubFileSystemFactory) NewFileSystemForNewIssue(owner, repo, baseBranch, newBranch string) (*GitHubFileSystem, error) {
	return CreateBranch(gfsf.githubClient, owner, repo, newBranch, baseBranch)
}

func (gfsf *githubFileSystemFactory) NewFileSystemForExistingPR(owner, repo, branch string) (*GitHubFileSystem, error) {
	return NewGitHubFileSystem(gfsf.githubClient, owner, repo, branch)
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

// processNewIssue processes a new issue with AI interaction
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

	// Create branch
	branchName, err := vd.createWorkBranch(ctx, owner, repo, issue)
	// TODO create filesystem

	// Let AI decide what to do with text editor tool support
	err = vd.processWithAI(ctx, workCtx, owner, repo)
	if err != nil {
		vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)
		return fmt.Errorf("failed to process with AI: %w", err)
	}

	return nil
}

// createWorkBranch creates a branch that the bot will make changes in while working on the given issue. It returns the
// name of the branch
func (vd *VirtualDeveloper) createWorkBranch(ctx context.Context, owner, repo string, issue *github.Issue) (*string, error) {
	var baseBranch string
	if repoInfo, _, err := vd.githubClient.Repositories.Get(ctx, owner, repo); err == nil && repoInfo.DefaultBranch != nil {
		baseBranch = *repoInfo.DefaultBranch
	} else {
		// Fall back to a reasonable default
		baseBranch = "main"
	}

	// Create a descriptive branch name
	var branchName string
	if issue.Title != nil {
		sanitizedTitle := sanitizeBranchName(*issue.Title)
		branchName = fmt.Sprintf("fix/issue-%d-%s", *issue.Number, sanitizedTitle)
	} else {
		branchName = fmt.Sprintf("fix/issue-%d", *issue.Number)
	}

	// TODO (check CreateBranch in fs.go)
	createBranch()

	return &branchName, nil
}

func sanitizeBranchName(title string) string {
	// Convert to lowercase and replace invalid characters
	title = strings.ToLower(title)
	title = strings.ReplaceAll(title, " ", "-")
	title = strings.ReplaceAll(title, "_", "-")

	// Remove invalid characters for git branch names
	invalidChars := []string{"~", "^", ":", "?", "*", "[", "]", "\\", "..", "@{", "/.", "//"}
	for _, char := range invalidChars {
		title = strings.ReplaceAll(title, char, "")
	}

	// Limit length and clean up
	if len(title) > 50 {
		title = title[:50]
	}
	title = strings.Trim(title, "-.")

	return title
}

// processWithAI handles the AI interaction with text editor tool support
func (vd *VirtualDeveloper) processWithAI(ctx context.Context, workCtx *WorkContext, owner, repo string) error {
	maxIterations := 15

	// Create appropriate file system
	var fs *GitHubFileSystem
	var err error

	if workCtx.IsInitialSolution {
		// For new issues, we'll start without a file system and create it when branch is created
		fs = nil
	} else {
		// For existing PRs, work on the existing branch
		if workCtx.PullRequest == nil || workCtx.PullRequest.Head == nil || workCtx.PullRequest.Head.Ref == nil {
			return fmt.Errorf("invalid PR head reference")
		}

		fs, err = vd.fileSystemFactory.NewFileSystemForExistingPR(owner, repo, *workCtx.PullRequest.Head.Ref)
		if err != nil {
			return fmt.Errorf("failed to create file system: %w", err)
		}
	}

	// Create tool context
	toolCtx := &ToolContext{
		FileSystem:   fs,
		Owner:        owner,
		Repo:         repo,
		WorkContext:  workCtx,
		GithubClient: vd.githubClient,
	}

	// Build conversation history
	var messages []anthropic.MessageParam

	for i := 0; i < maxIterations; i++ {
		log.Printf("AI interaction iteration %d", i+1)

		var result *InteractionResult

		if i == 0 {
			// First interaction - use ProcessWorkItem
			result, err = vd.anthropicClient.ProcessWorkItem(ctx, workCtx, vd.toolRegistry)
			if err != nil {
				return fmt.Errorf("AI processing failed: %w", err)
			}
		} else {
			// Continuation - use the message history
			systemPrompt := `Continue working on the task. You have access to the text editor tools and other actions.

If you've made file changes and are ready to create a pull request, use the create_pull_request tool.
If you need to make more changes, continue using the text editor tools.
If you want to engage in discussion, use post_comment.`

			response, err := vd.anthropicClient.CreateMessageWithSystemPrompt(ctx, messages, systemPrompt)
			if err != nil {
				return fmt.Errorf("failed to continue conversation: %w", err)
			}

			// Process the response
			result, err = vd.anthropicClient.processInteractionResponse(ctx, response, workCtx)
			if err != nil {
				return fmt.Errorf("failed to process continuation response: %w", err)
			}
		}

		needsContinuation := false
		var toolResults []anthropic.ContentBlockParamUnion

		// Process tool uses and collect tool results
		for _, toolUse := range result.ToolUses {
			log.Printf("Processing tool use: %s", toolUse.Name)

			// Process the tool use with the registry
			toolResult, err := vd.toolRegistry.ProcessToolUse(toolUse, toolCtx)
			if err != nil {
				return fmt.Errorf("failed to process tool use: %w", err)
			}

			toolResults = append(toolResults, anthropic.ContentBlockParamUnion{
				OfToolResult: toolResult,
			})

			// Check if this tool requires continuation
			switch toolUse.Type {
			case "view", "str_replace", "create", "insert", "undo_edit", "create_branch":
				needsContinuation = true
			}
		}

		// If we have tool results, we need to continue the conversation
		if len(toolResults) > 0 {
			// First, add the assistant's message with tool uses to the conversation
			if i == 0 {
				// For the first iteration, we need to reconstruct the assistant message from the result
				assistantContent := []anthropic.ContentBlockParamUnion{}
				for _, toolUse := range result.ToolUses {
					assistantContent = append(assistantContent, anthropic.ContentBlockParamUnion{OfToolUse: github.Ptr(toolUse.ToParam())})
				}
				if len(assistantContent) > 0 {
					assistantMessage := anthropic.NewAssistantMessage(assistantContent...)
					messages = append(messages, assistantMessage)
				}
			}

			// Then add the user message with tool results
			userMessage := anthropic.NewUserMessage(toolResults...)
			messages = append(messages, userMessage)

			needsContinuation = true
		}

		if !needsContinuation {
			return nil
		}
	}

	return fmt.Errorf("exceeded maximum iterations (%d) without completion", maxIterations)
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

	// Let AI decide what to do with text editor support
	err = vd.processWithAI(ctx, workCtx, owner, repo)
	if err != nil {
		// Post sanitized error comment
		vd.postIssueComment(ctx, owner, repo, prNumber,
			"I encountered an error while processing this. I'll retry on the next check cycle.")
		return fmt.Errorf("failed to process with AI: %w", err)
	}

	return nil
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
		err = vd.processWithAI(ctx, workCtx, owner, repo)
		if err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
			continue
		}
	}
}

// Helper functions

// needsAttention checks if a work item needs AI attention
func (vd *VirtualDeveloper) needsAttention(workCtx *WorkContext) bool {
	// Check if there are comments needing responses
	if len(workCtx.NeedsToRespond) > 0 {
		return true
	}

	// Check for unaddressed change requests
	for _, review := range workCtx.PRReviews {
		if review.State == "CHANGES_REQUESTED" && review.Author != workCtx.BotUsername {
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

// GitHub API helper functions

// issueHasPR checks if an issue already has an associated PR
func (vd *VirtualDeveloper) issueHasPR(ctx context.Context, owner, repo string, issueNumber int) bool {
	query := fmt.Sprintf("repo:%s/%s is:pr is:open %d", owner, repo, issueNumber)
	results, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil || results == nil {
		return false
	}

	for _, pr := range results.Issues {
		if pr.User != nil && pr.User.GetLogin() == vd.config.GitHubUsername {
			return true
		}
	}
	return false
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
func (vd *VirtualDeveloper) addLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error {
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
	_, _, err := vd.githubClient.Issues.GetLabel(ctx, owner, repo, labelName)
	if err == nil {
		return nil
	}

	label := &github.Label{
		Name:  github.Ptr(labelName),
		Color: github.Ptr("0366d6"),
	}

	if labelName == LabelInProgress {
		label.Color = github.Ptr("fbca04")
		label.Description = github.Ptr("Issue is being worked on by the virtual developer")
	} else if labelName == LabelCompleted {
		label.Color = github.Ptr("28a745")
		label.Description = github.Ptr("Issue has been addressed by the virtual developer")
	}

	_, _, err = vd.githubClient.Issues.CreateLabel(ctx, owner, repo, label)
	return err
}

// Context building functions

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
		reviews, err := vd.getAllPRReviews(ctx, owner, repo, *pr.Number)
		if err != nil {
			log.Printf("Warning: Could not get PR reviews: %v", err)
		}
		workCtx.PRReviews = reviews

		comments, err := vd.getAllPRComments(ctx, owner, repo, *pr.Number)
		if err != nil {
			log.Printf("Warning: Could not get PR comments: %v", err)
		}
		workCtx.PRReviewComments = comments
	}

	workCtx.AnalyzeComments()
	return workCtx, nil
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

			if comment.AuthorAssociation != nil {
				commentCtx.AuthorType = *comment.AuthorAssociation
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

		if review.AuthorAssociation != nil {
			reviewCtx.AuthorType = *review.AuthorAssociation
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
			}

			if comment.AuthorAssociation != nil {
				commentCtx.AuthorType = *comment.AuthorAssociation
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

// Utility functions

// formatFileChanges formats file changes for PR description
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

// extractIssueNumber extracts issue number from PR body
func extractIssueNumber(body string) int {
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
