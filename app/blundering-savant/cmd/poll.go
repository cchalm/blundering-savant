package cmd

import (
	"fmt"
	"log"
	"time"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/spf13/cobra"
)

var pollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Run in polling mode",
	Long: `Starts the bot in long-running mode where it continuously polls GitHub
for issues assigned to it and processes them automatically.`,
	RunE: runPollMode,
}

func init() {
	pollCmd.Flags().DurationVar(&config.CheckInterval, "check-interval", 5*time.Minute, "Interval between GitHub checks")
	pollCmd.Flags().StringVar(&config.ResumableConversationsDir, "conversations-dir", "", "Directory for resumable conversations")

	rootCmd.AddCommand(pollCmd)
}

func runPollMode(cmd *cobra.Command, args []string) error {
	ctx := setupContext()

	log.Printf("Starting Blundering Savant in POLL mode")
	log.Printf("Check interval: %s", config.CheckInterval)
	if config.ResumableConversationsDir != "" {
		log.Printf("Resumable conversations directory: %s", config.ResumableConversationsDir)
	}

	// Create clients
	systemGithubClient := createGithubClient(ctx, config.SystemGithubToken)
	botGithubClient := createGithubClient(ctx, config.BotGithubToken)
	anthropicClient := createAnthropicClient(config.AnthropicAPIKey)

	// Get bot user info
	githubUser, _, err := botGithubClient.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get github user: %w", err)
	}

	// Setup conversation history store
	var historyStore bot.ConversationHistoryStore
	if config.ResumableConversationsDir != "" {
		historyStore = ai.NewFileSystemConversationHistoryStore(config.ResumableConversationsDir)
	}

	// Create workspace factory
	workspaceFactory := &remoteValidationWorkspaceFactory{
		githubClient:           botGithubClient,
		validationWorkflowName: config.ValidationWorkflowName,
	}

	// Create task generator and bot
	taskGen := task.NewGenerator(systemGithubClient, githubUser, config.CheckInterval)
	b := bot.New(botGithubClient, githubUser, anthropicClient, historyStore, workspaceFactory)

	log.Printf("Bot started. Monitoring issues for @%s every %s", *githubUser.Login, config.CheckInterval)

	// Start generating tasks asynchronously
	tasks := taskGen.Generate(ctx)

	// Start the bot (blocking)
	return b.Run(ctx, tasks)
}
