package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/go-github/v72/github"
	"github.com/joho/godotenv"
	"golang.org/x/oauth2"

	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/transport"
)

// Config holds the configuration for the bot
type Config struct {
	BotGithubToken         string
	SystemGithubToken      string
	AnthropicAPIKey        string
	ValidationWorkflowName string

	QualifiedRepoName string
	IssueNumber       *int
	PRBranch          *string
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
		SystemGithubToken:      os.Getenv("SYSTEM_GITHUB_TOKEN"),
		BotGithubToken:         os.Getenv("BOT_GITHUB_TOKEN"),
		AnthropicAPIKey:        os.Getenv("ANTHROPIC_API_KEY"),
		ValidationWorkflowName: os.Getenv("VALIDATION_WORKFLOW_NAME"),

		QualifiedRepoName: os.Getenv("QUALIFIED_REPO_NAME"),
	}

	if s := os.Getenv("ISSUE_NUMBER"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			log.Fatalf("Failed to parse issue number '%s' as integer: %v", s, err)
		}
		config.IssueNumber = &n
	}

	if s := os.Getenv("PR_BRANCH"); s != "" {
		config.PRBranch = &s
	}

	if config.SystemGithubToken == "" || config.BotGithubToken == "" || config.AnthropicAPIKey == "" {
		log.Fatal("Missing required environment variables: SYSTEM_GITHUB_TOKEN, BOT_GITHUB_TOKEN, ANTHROPIC_API_KEY")
	}

	// The system github client authenticates using credentials provisioned for the github action
	systemGithubClient := createGithubClient(ctx, config.SystemGithubToken)
	// The bot github client authenticates using credentials belonging to the coding agent's github account
	botGithubClient := createGithubClient(ctx, config.BotGithubToken)

	botUser, _, err := botGithubClient.Users.Get(ctx, "")
	if err != nil {
		log.Fatalf("Failed to get github user: %v", err)
	}

	rateLimitedHTTPClient := &http.Client{
		Transport: transport.WithRateLimiting(nil),
	}
	anthropicClient := anthropic.NewClient(
		option.WithHTTPClient(rateLimitedHTTPClient),
		option.WithAPIKey(config.AnthropicAPIKey),
		option.WithMaxRetries(5),
	)

	workspaceFactory := remoteValidationWorkspaceFactory{
		githubClient:           botGithubClient,
		validationWorkflowName: config.ValidationWorkflowName,
	}

	b := bot.New(botGithubClient, botUser, anthropicClient, nil /* no conversation history */, workspaceFactory)

	var issueNumber int
	if config.IssueNumber != nil {
		issueNumber = *config.IssueNumber
	} else if config.PRBranch != nil {
		_, err := fmt.Sscanf(*config.PRBranch, "fix/issue-%d", &issueNumber)
		if err != nil {
			log.Fatalf("Failed to parse issue number from PR branch name '%s': %v", *config.PRBranch, err)
		}
	} else {
		log.Fatal("Issue number and PR branch are both nil")
	}

	parts := strings.Split(config.QualifiedRepoName, "/")
	if len(parts) != 2 {
		log.Fatalf("Failed to parse owner and repo from qualified repo name '%s'", config.QualifiedRepoName)
	}
	owner, repo := parts[0], parts[1]

	taskBuilder := task.NewBuilder(systemGithubClient, botUser)
	tsk, err := taskBuilder.BuildTask(ctx, owner, repo, issueNumber)
	if err != nil {
		log.Fatalf("Failed to build task for issue %d: %v", issueNumber, err)
	}

	if taskBuilder.NeedsAttention(*tsk) {
		log.Printf("Bot processing task for issue %d", issueNumber)

		err = b.DoTask(ctx, *tsk)
		if err != nil {
			log.Fatalf("Bot encountered an error: %v", err)
		}
	} else {
		log.Printf("Issue %d does not require any attention, skipping", issueNumber)
	}
}

func createGithubClient(ctx context.Context, token string) *github.Client {
	tokenSource := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	httpClient := oauth2.NewClient(ctx, tokenSource)
	githubClient := github.NewClient(httpClient)
	return githubClient
}
