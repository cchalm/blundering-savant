package cmd

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/spf13/cobra"
)

var oneShotCmd = &cobra.Command{
	Use:   "oneshot",
	Short: "Process a single specified task",
	Long: `Processes a single issue or pull request. This mode is designed to be
triggered by GitHub Actions, webhooks, etc.`,
	PreRun: loadOneShotConfig,
	RunE:   runTaskMode,
}

func loadOneShotConfig(cmd *cobra.Command, args []string) {
	// No additional config to load, simply call the parent
	cmd.Parent().PreRun(cmd.Parent(), args)
}

func init() {
	oneShotCmd.Flags().StringVar(&config.QualifiedRepoName, "repo", "", "Repository name in the format 'owner/repo'")
	oneShotCmd.Flags().IntVar(&config.IssueNumber, "issue-number", 0, "Issue number to process")
	oneShotCmd.Flags().IntVar(&config.PRNumber, "pr-number", 0, "Pull request number to process")

	_ = oneShotCmd.MarkFlagRequired("repo")
	oneShotCmd.MarkFlagsOneRequired("issue-number", "pr-number")
	oneShotCmd.MarkFlagsMutuallyExclusive("issue-number", "pr-number")

	rootCmd.AddCommand(oneShotCmd)
}

func runTaskMode(cmd *cobra.Command, args []string) error {
	ctx := setupContext()

	log.Printf("Starting Blundering Savant in TASK mode")
	log.Printf("Repository: %s", config.QualifiedRepoName)

	// Parse repository owner and name
	parts := strings.Split(config.QualifiedRepoName, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repository format '%s', expected owner/repo", config.QualifiedRepoName)
	}
	owner, repo := parts[0], parts[1]

	// Resolve issue number from either direct issue flag or PR number
	var issueNumber int
	if config.IssueNumber != 0 {
		issueNumber = config.IssueNumber
	} else if config.PRNumber != 0 {
		// Fetch PR branch name from GitHub and parse issue number
		var err error
		issueNumber, err = getIssueNumberFromPR(ctx, owner, repo, config.PRNumber)
		if err != nil {
			return fmt.Errorf("failed to resolve issue number from PR #%d: %w", config.PRNumber, err)
		}
		log.Printf("Resolved PR #%d to issue #%d", config.PRNumber, issueNumber)
	} else {
		return fmt.Errorf("issue number and PR number are both nil")
	}

	log.Printf("Processing issue #%d", issueNumber)

	// Create clients
	systemGithubClient := createGithubClient(ctx, config.SystemGithubToken)
	botGithubClient := createGithubClient(ctx, config.BotGithubToken)
	anthropicClient := createAnthropicClient(config.AnthropicAPIKey)

	sender := ai.NewStreamingMessageSender(anthropicClient)

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
	b := bot.New(botGithubClient, botUser, sender, nil, workspaceFactory)

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

func getIssueNumberFromPR(ctx context.Context, owner, repo string, prNumber int) (int, error) {
	// Create a GitHub client to fetch PR details
	githubClient := createGithubClient(ctx, config.SystemGithubToken)

	// Fetch the pull request
	pr, _, err := githubClient.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch PR #%d: %w", prNumber, err)
	}

	// Parse issue number from branch name
	branchName := pr.Head.GetRef()
	var issueNumber int
	_, err = fmt.Sscanf(branchName, "fix/issue-%d", &issueNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to parse issue number from PR branch '%s': %w", branchName, err)
	}

	return issueNumber, nil
}
