package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/go-github/v72/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"

	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/transport"
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

	rateLimitedHTTPClient := &http.Client{
		Transport: transport.WithRateLimiting(nil),
	}
	anthropicClient := anthropic.NewClient(
		option.WithHTTPClient(rateLimitedHTTPClient),
		option.WithAPIKey(config.AnthropicAPIKey),
		option.WithMaxRetries(5),
	)

	historyStore := ai.NewFileSystemConversationHistoryStore(
		config.ResumableConversationsDir,
	)

	workspaceFactory := remoteValidationWorkspaceFactory{
		githubClient:           githubClient,
		validationWorkflowName: config.ValidationWorkflowName,
	}

	taskGen := task.NewGenerator(githubClient, githubUser, config.CheckInterval)
	b := bot.New(githubClient, githubUser, anthropicClient, historyStore, workspaceFactory)

	log.Printf("Bot started. Monitoring issues for @%s every %s", *githubUser.Login, config.CheckInterval)

	// Start generating tasks asynchronously
	tasks := taskGen.Generate(ctx)
	// Start the bot, which will consume tasks. This is a synchronous call
	err = b.Run(ctx, tasks)
	if err != nil {
		log.Fatalf("Bot encountered an error: %v", err)
	}
}
