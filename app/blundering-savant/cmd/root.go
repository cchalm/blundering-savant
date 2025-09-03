package cmd

import (
	"log"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "blundering-savant",
	Short: "AI coding agent that operates via GitHub",
	Long: `Blundering Savant is a generative AI coding agent that presents as a GitHub user.
The agent receives instructions via issues, reviews, and comments and proposes code 
changes by creating and updating pull requests.`,
	PreRun: loadRootConfig,
}

func Execute() error {
	return rootCmd.Execute()
}

func loadRootConfig(_ *cobra.Command, _ []string) {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables")
	}

	loadFromEnv(&config.SystemGithubToken, "SYSTEM_GITHUB_TOKEN")
	loadFromEnv(&config.BotGithubToken, "BOT_GITHUB_TOKEN")
	loadFromEnv(&config.AnthropicAPIKey, "ANTHROPIC_API_KEY")
}

func init() {
	rootCmd.PersistentFlags().StringVar(&config.ValidationWorkflowName, "validation-workflow", "", "GitHub Actions workflow name for validation")
	_ = oneShotCmd.MarkFlagRequired("validation-workflow")
}
