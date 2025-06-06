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

// StyleGuide represents coding style information
type StyleGuide struct {
	Content   string
	FilePath  string
	RepoStyle map[string]string // language -> style patterns
}

// CodebaseInfo holds information about the repository structure
type CodebaseInfo struct {
	MainLanguage  string
	FileTree      []string
	ReadmeContent string
	PackageInfo   map[string]string
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
	vd.checkAndProcessIssues()

	for range ticker.C {
		vd.checkAndProcessIssues()
	}
}

// checkAndProcessIssues checks for new issues assigned to the bot
func (vd *VirtualDeveloper) checkAndProcessIssues() {
	ctx := context.Background()

	// Search for issues assigned to our bot that are not already in progress
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

		issueTitle := "untitled"
		if issue.Title != nil {
			issueTitle = *issue.Title
		}

		log.Printf("Processing issue #%d in %s/%s: %s", *issue.Number, owner, repo, issueTitle)

		if err := vd.processIssue(ctx, owner, repo, issue); err != nil {
			log.Printf("Error processing issue #%d: %v", *issue.Number, err)
			// Post error comment on issue and remove in-progress label
			vd.postIssueComment(ctx, owner, repo, *issue.Number,
				fmt.Sprintf("I encountered an error while working on this issue: %v\n\nI'll need human assistance to proceed.", err))
			vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress)
		}
	}

	// Check for PR reviews and comments
	vd.checkPullRequestUpdates()
}

// processIssue handles a single issue
func (vd *VirtualDeveloper) processIssue(ctx context.Context, owner, repo string, issue *github.Issue) error {
	if issue == nil || issue.Number == nil {
		return fmt.Errorf("invalid issue: missing required fields")
	}

	// Add in-progress label first
	if err := vd.addLabel(ctx, owner, repo, *issue.Number, LabelInProgress); err != nil {
		return fmt.Errorf("failed to add in-progress label: %w", err)
	}

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

	// Add completed label and remove in-progress
	if err := vd.addLabel(ctx, owner, repo, *issue.Number, LabelCompleted); err != nil {
		log.Printf("Warning: Failed to add completed label: %v", err)
	}
	if err := vd.removeLabel(ctx, owner, repo, *issue.Number, LabelInProgress); err != nil {
		log.Printf("Warning: Failed to remove in-progress label: %v", err)
	}

	// Post success comment on issue
	if pr != nil && pr.Number != nil {
		if err := vd.postIssueComment(ctx, owner, repo, *issue.Number,
			fmt.Sprintf("I've created PR #%d to address this issue. Please review and let me know if any changes are needed.", *pr.Number)); err != nil {
			log.Printf("Warning: Failed to post success comment: %v", err)
		}
	}

	return nil
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
		Name:  github.String(labelName),
		Color: github.String("0366d6"), // Blue color
	}

	if labelName == LabelInProgress {
		label.Color = github.String("fbca04") // Yellow for in-progress
		label.Description = github.String("Issue is being worked on by the virtual developer")
	} else if labelName == LabelCompleted {
		label.Color = github.String("28a745") // Green for completed
		label.Description = github.String("Issue has been addressed by the virtual developer")
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

// generateSolution uses Anthropic to create a solution
func (vd *VirtualDeveloper) generateSolution(issue *github.Issue, repo *github.Repository, styleGuide *StyleGuide, codebaseInfo *CodebaseInfo) (*Solution, error) {
	ctx := context.Background()
	return vd.anthropicClient.generateSolutionWithTools(ctx, issue, repo, styleGuide, codebaseInfo)
}

// Helper function to truncate strings
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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

	// Ensure we have file changes to commit
	if len(solution.Files) == 0 {
		return nil, fmt.Errorf("no file changes specified in solution")
	}

	// Get the base tree
	baseCommit, _, err := vd.githubClient.Git.GetCommit(ctx, owner, repo, *ref.Object.SHA)
	if err != nil {
		return nil, fmt.Errorf("failed to get base commit: %w", err)
	}

	// Create tree entries for all changes
	var treeEntries []*github.TreeEntry
	for path, change := range solution.Files {
		// Create blob for file content
		blob := &github.Blob{
			Content:  github.String(change.Content),
			Encoding: github.String("utf-8"),
		}

		createdBlob, _, err := vd.githubClient.Git.CreateBlob(ctx, owner, repo, blob)
		if err != nil {
			return nil, fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		treeEntry := &github.TreeEntry{
			Path: github.String(path),
			Mode: github.String("100644"), // Regular file
			Type: github.String("blob"),
			SHA:  createdBlob.SHA,
		}
		treeEntries = append(treeEntries, treeEntry)
	}

	createdTree, _, err := vd.githubClient.Git.CreateTree(ctx, owner, repo, *baseCommit.Tree.SHA, treeEntries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %w", err)
	}

	// Create commit
	commit := &github.Commit{
		Message: github.String(solution.CommitMessage),
		Tree:    createdTree,
		Parents: []*github.Commit{baseCommit},
	}

	createdCommit, _, err := vd.githubClient.Git.CreateCommit(ctx, owner, repo, commit, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	// Update branch reference to point to new commit
	branchRef, _, err := vd.githubClient.Git.GetRef(ctx, owner, repo, fmt.Sprintf("refs/heads/%s", solution.Branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch ref: %w", err)
	}

	branchRef.Object.SHA = createdCommit.SHA
	_, _, err = vd.githubClient.Git.UpdateRef(ctx, owner, repo, branchRef, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch ref: %w", err)
	}

	// Create pull request with safe field access
	issueTitle := "Fix issue"
	if issue != nil && issue.Title != nil {
		issueTitle = *issue.Title
	}

	issueNumber := 0
	if issue != nil && issue.Number != nil {
		issueNumber = *issue.Number
	}

	prTitle := fmt.Sprintf("Fix: %s", issueTitle)
	prBody := fmt.Sprintf("This PR addresses issue #%d\n\n%s\n\n---\n*This PR was created by the Virtual Developer bot*",
		issueNumber, solution.Description)

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

		// Process PR feedback and potentially update the diff
		vd.processPRFeedback(ctx, owner, repo, *pr.Number)
	}
}

// processPRFeedback handles all feedback on a PR and potentially updates it
func (vd *VirtualDeveloper) processPRFeedback(ctx context.Context, owner, repo string, prNumber int) {
	// Get the PR details
	pr, _, err := vd.githubClient.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		log.Printf("Error getting PR details: %v", err)
		return
	}

	// Check if we need to update based on feedback
	needsUpdate, feedback := vd.checkForUnaddressedFeedback(ctx, owner, repo, prNumber)
	if !needsUpdate {
		return
	}

	log.Printf("Processing feedback for PR #%d in %s/%s", prNumber, owner, repo)

	// Get the original issue
	var issue *github.Issue
	if pr.Body != nil {
		// Extract issue number from PR body
		if issueNum := extractIssueNumber(*pr.Body); issueNum > 0 {
			issue, _, _ = vd.githubClient.Issues.Get(ctx, owner, repo, issueNum)
		}
	}

	// Generate updated solution based on feedback
	updatedSolution, err := vd.generateUpdatedSolution(ctx, owner, repo, pr, issue, feedback)
	if err != nil {
		log.Printf("Error generating updated solution: %v", err)
		return
	}

	// Apply the updates
	if err := vd.updatePullRequest(ctx, owner, repo, pr, updatedSolution); err != nil {
		log.Printf("Error updating PR: %v", err)
		return
	}

	// Post a comprehensive review response
	vd.postComprehensiveReview(ctx, owner, repo, prNumber, feedback, updatedSolution)
}

// checkForUnaddressedFeedback checks if there's feedback that needs addressing
func (vd *VirtualDeveloper) checkForUnaddressedFeedback(ctx context.Context, owner, repo string, prNumber int) (bool, []string) {
	var feedback []string

	// Get recent comments (last 10)
	comments, _, err := vd.githubClient.Issues.ListComments(ctx, owner, repo, prNumber, &github.IssueListCommentsOptions{
		Sort:      github.String("created"),
		Direction: github.String("desc"),
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

// generateUpdatedSolution creates an updated solution based on feedback
func (vd *VirtualDeveloper) generateUpdatedSolution(ctx context.Context, owner, repo string, pr *github.PullRequest, issue *github.Issue, feedback []string) (*Solution, error) {
	// Get current PR files
	prFiles, _, err := vd.githubClient.PullRequests.ListFiles(ctx, owner, repo, *pr.Number, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR files: %w", err)
	}

	// Build context for the AI
	feedbackContext := strings.Join(feedback, "\n\n---\n\n")

	var currentFiles []string
	for _, file := range prFiles {
		if file.Filename != nil {
			currentFiles = append(currentFiles, *file.Filename)
		}
	}

	// Use Anthropic to generate updated solution
	prompt := fmt.Sprintf(`I have a pull request that has received feedback. Please analyze the feedback and update the solution accordingly.

Original Issue: %s

Current PR Files: %s

Feedback received:
%s

Please generate an updated solution that addresses all the feedback while maintaining the original intent of fixing the issue. Use the create_solution tool to provide the complete updated implementation.`,
		getIssueDescription(issue),
		strings.Join(currentFiles, ", "),
		feedbackContext)

	// Define tools for creating updated solution
	tools := []anthropic.ToolParam{
		{
			Name:        "create_solution",
			Description: anthropic.String("Create an updated solution with file changes to address the feedback"),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: map[string]interface{}{
					"commit_message": map[string]interface{}{
						"type":        "string",
						"description": "Commit message for the updates",
					},
					"files": map[string]interface{}{
						"type":        "object",
						"description": "Map of file paths to their new content",
						"additionalProperties": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"content": map[string]interface{}{"type": "string"},
								"is_new":  map[string]interface{}{"type": "boolean"},
							},
						},
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Description of the updates made",
					},
				},
			},
		},
	}

	response, err := vd.anthropicClient.CreateMessage(ctx, prompt, tools)
	if err != nil {
		return nil, fmt.Errorf("failed to generate updated solution: %w", err)
	}

	// Process the response
	return vd.anthropicClient.processToolResponse(ctx, response, issue)
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
			Content:  github.String(change.Content),
			Encoding: github.String("utf-8"),
		}

		createdBlob, _, err := vd.githubClient.Git.CreateBlob(ctx, owner, repo, blob)
		if err != nil {
			return fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		treeEntry := &github.TreeEntry{
			Path: github.String(path),
			Mode: github.String("100644"),
			Type: github.String("blob"),
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
		Message: github.String(solution.CommitMessage),
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
		Body:  github.String(reviewBody),
		Event: github.String("COMMENT"),
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
		Body: github.String(body),
	}

	_, _, err := vd.githubClient.Issues.CreateComment(ctx, owner, repo, number, comment)
	return err
}
