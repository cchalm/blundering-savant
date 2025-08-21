package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"

	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/config"
	"github.com/cchalm/blundering-savant/internal/task"
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

	// Load configuration
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}

	tokenSource := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: cfg.GitHubToken},
	)
	httpClient := oauth2.NewClient(ctx, tokenSource)
	githubClient := github.NewClient(httpClient)

	taskGen := task.NewGenerator(cfg, githubClient)
	b := bot.NewBot(cfg, githubClient)

	log.Printf("Bot started. Monitoring issues for @%s every %s", cfg.GitHubUsername, cfg.CheckInterval)

	// Start generating tasks asynchronously
	tasks := taskGen.Generate(ctx)
	// Start the bot, which will consume tasks. This is a synchronous call
	err := b.Run(ctx, tasks)
	if err != nil {
		log.Fatalf("Bot encountered an error: %v", err)
	}
}
