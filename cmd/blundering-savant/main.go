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

	"github.com/cchalm/blundering-savant/internal/config"
)



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

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	tokenSource := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: cfg.GitHubToken},
	)
	httpClient := oauth2.NewClient(ctx, tokenSource)
	githubClient := github.NewClient(httpClient)

	githubUser, _, err := githubClient.Users.Get(ctx, "")
	if err != nil {
		log.Fatalf("failed to get github user: %v", err)
	}

	taskGen := newTaskGenerator(cfg, githubClient, githubUser)
	b := NewBot(cfg, githubClient, githubUser)

	log.Printf("Bot started. Monitoring issues for @%s every %s", *githubUser.Login, cfg.CheckInterval)

	// Start generating tasks asynchronously
	tasks := taskGen.generate(ctx)
	// Start the bot, which will consume tasks. This is a synchronous call
	err = b.Run(ctx, tasks)
	if err != nil {
		log.Fatalf("Bot encountered an error: %v", err)
	}
}
