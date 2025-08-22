package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/google/go-github/v72/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"
)

// Config holds the configuration for the bot
type Config struct {
	GitHubToken               string
	AnthropicAPIKey           string
	ResumableConversationsDir string
	CheckInterval             time.Duration
	ValidationWorkflowName    string
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	// Set up graceful shutdown on interrupt
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		log.Println("Interrupt signal detected, shutting down gracefully. Interrupt again to force shutdown")
		cancel()
		<-interrupt
		log.Fatal("Forcing shutdown")
	}()

	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	config := Config{
		GitHubToken:               os.Getenv("GITHUB_TOKEN"),
		AnthropicAPIKey:           os.Getenv("ANTHROPIC_API_KEY"),
		ResumableConversationsDir: os.Getenv("RESUMABLE_CONVERSATIONS_DIR"),
		CheckInterval:             5 * time.Minute, // Default
		ValidationWorkflowName:    os.Getenv("VALIDATION_WORKFLOW_NAME"),
	}

	if config.GitHubToken == "" || config.AnthropicAPIKey == "" {
		log.Fatal("Missing required environment variables: GITHUB_TOKEN, ANTHROPIC_API_KEY")
	}

	// Parse check interval if provided
	if interval := os.Getenv("CHECK_INTERVAL"); interval != "" {
		if d, err := time.ParseDuration(interval); err == nil {
			config.CheckInterval = d
		}
	}

	tokenSource := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.GitHubToken},
	)
	httpClient := oauth2.NewClient(ctx, tokenSource)
	githubClient := github.NewClient(httpClient)

	githubUser, _, err := githubClient.Users.Get(ctx, "")
	if err != nil {
		log.Fatalf("failed to get github user: %v", err)
	}

	taskGen := newTaskGenerator(config, githubClient, githubUser)
	b := NewBot(config, githubClient, githubUser)

	log.Printf("Bot started. Monitoring issues for @%s every %s", *githubUser.Login, config.CheckInterval)

	// Start generating tasks asynchronously
	tasks := taskGen.generate(ctx)
	// Start the bot, which will consume tasks. This is a synchronous call
	err = b.Run(ctx, tasks)
	if err != nil {
		log.Fatalf("Bot encountered an error: %v", err)
	}
}
