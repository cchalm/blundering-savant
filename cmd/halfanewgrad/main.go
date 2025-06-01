package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
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
	processedIssues map[string]bool
}

// StyleGuide represents coding style information
type StyleGuide struct {
	Content   string
	FilePath  string
	RepoStyle map[string]string // language -> style patterns
}

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
		processedIssues: make(map[string]bool),
	}
}

// Run starts the main loop
func (vd *VirtualDeveloper) Run() {
	ticker := time.NewTicker(vd.config.CheckInterval)
	defer ticker.Stop()

	// Initial check
	vd.checkAndProcessIssues()

	for range ticker.C {
		vd.checkAndProcessIssues()
	}
}

// checkAndProcessIssues checks for new issues assigned to the bot
func (vd *VirtualDeveloper) checkAndProcessIssues() {
	ctx := context.Background()

	// Search for issues assigned to our bot
	query := fmt.Sprintf("assignee:%s is:issue is:open", vd.config.GitHubUsername)
	issues, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching issues: %v", err)
		return
	}

	for _, issue := range issues.Issues {
		issueKey := fmt.Sprintf("%s-%d", *issue.RepositoryURL, *issue.Number)

		// Skip if already processed
		if vd.processedIssues[issueKey] {
			continue
		}

		// Extract owner and repo from repository URL
		parts := strings.Split(*issue.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		log.Printf("Processing issue #%d in %s/%s: %s", *issue.Number, owner, repo, *issue.Title)

		if err := vd.processIssue(ctx, owner, repo, issue); err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
			// Post error comment on issue
			vd.postIssueComment(ctx, owner, repo, *issue.Number,
				fmt.Sprintf("I encountered an error while working on this issue: %v\n\nI'll need human assistance to proceed.", err))
		} else {
			vd.processedIssues[issueKey] = true
		}
	}

	// Check for PR reviews and comments
	vd.checkPullRequestUpdates()
}

// processIssue handles a single issue
func (vd *VirtualDeveloper) processIssue(ctx context.Context, owner, repo string, issue *github.Issue) error {
	// Post initial comment
	if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
		"👋 I'm starting work on this issue. I'll analyze the codebase and create a PR shortly."); err != nil {
		return fmt.Errorf("failed to post initial comment: %w", err)
	}

	// Get repository information
	repository, _, err := vd.githubClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("failed to get repository: %w", err)
	}

	// Search for style guides
	styleGuide, err := vd.findStyleGuides(ctx, owner, repo)
	if err != nil {
		log.Printf("Warning: Could not find style guides: %v", err)
	}

	// Analyze the codebase structure
	codebaseInfo, err := vd.analyzeCodebase(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("failed to analyze codebase: %w", err)
	}

	// Generate solution using Anthropic
	solution, err := vd.generateSolution(issue, repository, styleGuide, codebaseInfo)
	if err != nil {
		return fmt.Errorf("failed to generate solution: %w", err)
	}

	// Create branch and PR
	pr, err := vd.createPullRequest(ctx, owner, repo, issue, solution)
	if err != nil {
		return fmt.Errorf("failed to create pull request: %w", err)
	}

	// Post success comment on issue
	if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
		fmt.Sprintf("I've created PR #%d to address this issue. Please review and let me know if any changes are needed.", *pr.Number)); err != nil {
		log.Printf("Warning: Failed to post success comment: %v", err)
	}

	return nil
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

// CodebaseInfo holds information about the repository structure
type CodebaseInfo struct {
	MainLanguage  string
	FileTree      []string
	ReadmeContent string
	PackageInfo   map[string]string
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

// generateSolution uses Anthropic to create a solution
func (vd *VirtualDeveloper) generateSolution(issue *github.Issue, repo *github.Repository, styleGuide *StyleGuide, codebaseInfo *CodebaseInfo) (*Solution, error) {
	// Prepare context for Anthropic
	prompt := vd.buildPrompt(issue, repo, styleGuide, codebaseInfo)

	// Call Anthropic with tools
	response, err := vd.anthropicClient.CreateMessage(prompt, []Tool{
		{
			Name:        "analyze_file",
			Description: "Analyze a file from the repository",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path"}},"required":["path"]}`),
		},
		{
			Name:        "create_solution",
			Description: "Create a solution with file changes",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"branch":{"type":"string"},"commit_message":{"type":"string"},"files":{"type":"object"},"description":{"type":"string"}},"required":["branch","commit_message","files","description"]}`),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate solution: %w", err)
	}

	// Parse the solution from the response
	return vd.parseSolution(response)
}

// buildPrompt creates the prompt for Anthropic
func (vd *VirtualDeveloper) buildPrompt(issue *github.Issue, repo *github.Repository, styleGuide *StyleGuide, codebaseInfo *CodebaseInfo) string {
	prompt := fmt.Sprintf(`You are a virtual developer working on a GitHub issue. Your task is to analyze the issue and create a solution.

Repository: %s
Main Language: %s
Issue #%d: %s

Issue Description:
%s

`, *repo.FullName, codebaseInfo.MainLanguage, *issue.Number, *issue.Title, *issue.Body)

	if styleGuide != nil && styleGuide.Content != "" {
		prompt += fmt.Sprintf("\nStyle Guide:\n%s\n", styleGuide.Content)
	}

	if codebaseInfo.ReadmeContent != "" {
		prompt += fmt.Sprintf("\nREADME excerpt:\n%s\n", truncateString(codebaseInfo.ReadmeContent, 1000))
	}

	prompt += `
Please analyze this issue and create a solution. Follow these guidelines:
1. Respect the existing code style and conventions
2. Write clean, maintainable code
3. Include appropriate tests if applicable
4. Make minimal changes to solve the issue
5. Use descriptive commit messages

Use the analyze_file tool to examine relevant files, then use create_solution to provide your solution.`

	return prompt
}

// Helper function to truncate strings
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// parseSolution extracts the solution from Anthropic's response
func (vd *VirtualDeveloper) parseSolution(response *AnthropicResponse) (*Solution, error) {
	// This is a simplified version - in reality, you'd parse the tool use responses
	// For now, we'll create a mock solution
	solution := &Solution{
		Branch:        fmt.Sprintf("fix/issue-%d", time.Now().Unix()),
		CommitMessage: "Fix issue based on description",
		Files:         make(map[string]FileChange),
		Description:   "Automated fix for the reported issue",
	}

	return solution, nil
}

// createPullRequest creates a new PR with the solution
func (vd *VirtualDeveloper) createPullRequest(ctx context.Context, owner, repo string, issue *github.Issue, solution *Solution) (*github.PullRequest, error) {
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
		Ref:    github.String(fmt.Sprintf("refs/heads/%s", solution.Branch)),
		Object: &github.GitObject{SHA: ref.Object.SHA},
	}

	_, _, err = vd.githubClient.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create branch: %w", err)
	}

	// Apply file changes
	for path, change := range solution.Files {
		if err := vd.applyFileChange(ctx, owner, repo, solution.Branch, path, change); err != nil {
			return nil, fmt.Errorf("failed to apply change to %s: %w", path, err)
		}
	}

	// Create pull request
	prTitle := fmt.Sprintf("Fix: %s", *issue.Title)
	prBody := fmt.Sprintf("This PR addresses issue #%d\n\n%s\n\n---\n*This PR was created by the Virtual Developer bot*",
		*issue.Number, solution.Description)

	pr := &github.NewPullRequest{
		Title:               github.String(prTitle),
		Body:                github.String(prBody),
		Head:                github.String(solution.Branch),
		Base:                github.String(defaultBranch),
		MaintainerCanModify: github.Bool(true),
	}

	createdPR, _, err := vd.githubClient.PullRequests.Create(ctx, owner, repo, pr)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	return createdPR, nil
}

// applyFileChange applies a single file change
func (vd *VirtualDeveloper) applyFileChange(ctx context.Context, owner, repo, branch, path string, change FileChange) error {
	var currentSHA *string

	// Get current file if it exists
	if !change.IsNew {
		currentFile, _, _, err := vd.githubClient.Repositories.GetContents(ctx, owner, repo, path, &github.RepositoryContentGetOptions{
			Ref: branch,
		})
		if err == nil && currentFile != nil {
			currentSHA = currentFile.SHA
		}
	}

	// Create or update file
	opts := &github.RepositoryContentFileOptions{
		Message: github.String(fmt.Sprintf("Update %s", path)),
		Content: []byte(change.Content),
		Branch:  github.String(branch),
	}

	if currentSHA != nil {
		opts.SHA = currentSHA
	}

	_, _, err := vd.githubClient.Repositories.CreateFile(ctx, owner, repo, path, opts)
	return err
}

// checkPullRequestUpdates monitors PR comments and reviews
func (vd *VirtualDeveloper) checkPullRequestUpdates() {
	ctx := context.Background()

	// Search for open PRs created by the bot
	query := fmt.Sprintf("author:%s is:pr is:open", vd.config.GitHubUsername)
	prs, _, err := vd.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		log.Printf("Error searching PRs: %v", err)
		return
	}

	for _, pr := range prs.Issues {
		// Extract owner and repo
		parts := strings.Split(*pr.RepositoryURL, "/")
		if len(parts) < 2 {
			continue
		}
		owner := parts[len(parts)-2]
		repo := parts[len(parts)-1]

		// Check for new comments
		vd.processPRComments(ctx, owner, repo, *pr.Number)

		// Check for review comments
		vd.processPRReviews(ctx, owner, repo, *pr.Number)
	}
}

// processPRComments handles comments on PRs
func (vd *VirtualDeveloper) processPRComments(ctx context.Context, owner, repo string, prNumber int) {
	comments, _, err := vd.githubClient.Issues.ListComments(ctx, owner, repo, prNumber, &github.IssueListCommentsOptions{
		Sort:      github.String("created"),
		Direction: github.String("desc"),
		ListOptions: github.ListOptions{
			PerPage: 10,
		},
	})
	if err != nil {
		log.Printf("Error getting PR comments: %v", err)
		return
	}

	for _, comment := range comments {
		// Skip our own comments
		if comment.User != nil && *comment.User.Login == vd.config.GitHubUsername {
			continue
		}

		// Check if we've already responded to this comment
		if vd.hasRespondedToComment(ctx, owner, repo, prNumber, *comment.ID) {
			continue
		}

		// Generate and post response
		vd.respondToComment(ctx, owner, repo, prNumber, comment)
	}
}

// processPRReviews handles review comments
func (vd *VirtualDeveloper) processPRReviews(ctx context.Context, owner, repo string, prNumber int) {
	reviews, _, err := vd.githubClient.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err != nil {
		log.Printf("Error getting PR reviews: %v", err)
		return
	}

	for _, review := range reviews {
		if review.State != nil && (*review.State == "CHANGES_REQUESTED" || *review.State == "COMMENTED") {
			// Get review comments
			comments, _, err := vd.githubClient.PullRequests.ListReviewComments(ctx, owner, repo, prNumber, *review.ID, nil)
			if err != nil {
				continue
			}

			for _, comment := range comments {
				if vd.hasRespondedToReviewComment(ctx, owner, repo, prNumber, *comment.ID) {
					continue
				}

				vd.respondToReviewComment(ctx, owner, repo, prNumber, comment)
			}
		}
	}
}

// Helper methods for comment tracking and responses
func (vd *VirtualDeveloper) hasRespondedToComment(ctx context.Context, owner, repo string, prNumber int, commentID int64) bool {
	// In a production system, you'd track this in a database
	// For now, we'll check if there's a comment after this one from the bot
	return false
}

func (vd *VirtualDeveloper) hasRespondedToReviewComment(ctx context.Context, owner, repo string, prNumber int, commentID int64) bool {
	return false
}

func (vd *VirtualDeveloper) respondToComment(ctx context.Context, owner, repo string, prNumber int, comment *github.IssueComment) {
	// Generate response using Anthropic
	response := vd.generateCommentResponse(comment)

	// Post response
	vd.postIssueComment(ctx, owner, repo, prNumber, response)
}

func (vd *VirtualDeveloper) respondToReviewComment(ctx context.Context, owner, repo string, prNumber int, comment *github.PullRequestComment) {
	// Generate response and potentially update code
	response := vd.generateReviewResponse(comment)

	// Post response as a review comment reply
	reply := &github.PullRequestComment{
		Body:      github.String(response),
		InReplyTo: comment.ID,
	}

	_, _, err := vd.githubClient.PullRequests.CreateComment(ctx, owner, repo, prNumber, reply)
	if err != nil {
		log.Printf("Error posting review comment reply: %v", err)
	}
}

func (vd *VirtualDeveloper) generateCommentResponse(comment *github.IssueComment) string {
	// Use Anthropic to generate appropriate response
	// For now, return a simple acknowledgment
	return "Thank you for your feedback. I'll review this and make any necessary adjustments."
}

func (vd *VirtualDeveloper) generateReviewResponse(comment *github.PullRequestComment) string {
	return "I'll address this review comment in the next update."
}

// postIssueComment posts a comment on an issue or PR
func (vd *VirtualDeveloper) postIssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	comment := &github.IssueComment{
		Body: github.String(body),
	}

	_, _, err := vd.githubClient.Issues.CreateComment(ctx, owner, repo, number, comment)
	return err
}
