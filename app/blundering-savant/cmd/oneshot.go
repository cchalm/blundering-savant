package cmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/spf13/cobra"
)

var oneShotCmd = &cobra.Command{
	Use:   "task",
	Short: "Process a single specified task",
	Long: `Processes a single issue or pull request. This mode is designed to be
triggered by GitHub Actions, webhooks, etc.`,
	RunE: runTaskMode,
}

func init() {
	oneShotCmd.Flags().StringVar(&config.QualifiedRepoName, "repo", "", "Repository name in the format 'owner/repo'")
	oneShotCmd.Flags().IntVar(config.IssueNumber, "issue", 0, "Issue number to process")
	oneShotCmd.Flags().StringVar(config.PRBranch, "pr-branch", "", "Pull request branch name")

	_ = oneShotCmd.MarkFlagRequired("repo")
	oneShotCmd.MarkFlagsOneRequired("issue", "pr-branch")

	rootCmd.AddCommand(oneShotCmd)
}

func runTaskMode(cmd *cobra.Command, args []string) error {
	ctx := setupContext()

	log.Printf("Starting Blundering Savant in TASK mode")
	log.Printf("Repository: %s", config.QualifiedRepoName)

	// Parse issue number from PR branch if needed
	var issueNumber int
	if config.IssueNumber != nil {
		issueNumber = *config.IssueNumber
	} else if config.PRBranch != nil {
		_, err := fmt.Sscanf(*config.PRBranch, "fix/issue-%d", &issueNumber)
		if err != nil {
			return fmt.Errorf("failed to parse issue number from PR branch '%s': %w", *config.PRBranch, err)
		}
	} else {
		return fmt.Errorf("issue number and PR branch are both nil")
	}

	log.Printf("Processing issue #%d", issueNumber)

	// Parse repository owner and name
	parts := strings.Split(config.QualifiedRepoName, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repository format '%s', expected owner/repo", config.QualifiedRepoName)
	}
	owner, repo := parts[0], parts[1]

	// Create clients
	systemGithubClient := createGithubClient(ctx, config.SystemGithubToken)
	botGithubClient := createGithubClient(ctx, config.BotGithubToken)
	anthropicClient := createAnthropicClient(config.AnthropicAPIKey)

	// Get bot user info
	botUser, _, err := botGithubClient.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get github user: %w", err)
	}

	// Create workspace factory
	workspaceFactory := &remoteValidationWorkspaceFactory{
		githubClient:           botGithubClient,
		validationWorkflowName: config.ValidationWorkflowName,
	}

	// Create bot (no conversation history in task mode)
	b := bot.New(botGithubClient, botUser, anthropicClient, nil, workspaceFactory)

	// Build task
	taskBuilder := task.NewBuilder(systemGithubClient, botUser)
	tsk, err := taskBuilder.BuildTask(ctx, owner, repo, issueNumber)
	if err != nil {
		return fmt.Errorf("failed to build task for issue %d: %w", issueNumber, err)
	}

	// Process if needed
	if taskBuilder.NeedsAttention(*tsk) {
		log.Printf("Issue #%d requires attention, processing...", issueNumber)
		if err := b.DoTask(ctx, *tsk); err != nil {
			return fmt.Errorf("bot encountered an error: %w", err)
		}
		log.Printf("Successfully processed issue #%d", issueNumber)
	} else {
		log.Printf("Issue #%d does not require attention, skipping", issueNumber)
	}

	return nil
}
