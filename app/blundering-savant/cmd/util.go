package cmd

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/cchalm/blundering-savant/internal/bot"
	"github.com/cchalm/blundering-savant/internal/task"
	"github.com/cchalm/blundering-savant/internal/telemetry"
	"github.com/cchalm/blundering-savant/internal/transport"
	"github.com/cchalm/blundering-savant/internal/workspace"
	"github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

func setupContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	// Setup graceful shutdown
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		log.Println("Interrupt signal detected, shutting down gracefully...")
		cancel()
		<-interrupt
		log.Fatal("Forcing shutdown")
	}()

	return ctx
}

func createGithubClient(ctx context.Context, token string) *github.Client {
	tokenSource := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	httpClient := oauth2.NewClient(ctx, tokenSource)
	return github.NewClient(httpClient)
}

func createAnthropicClient(apiKey string) anthropic.Client {
	rateLimitedHTTPClient := &http.Client{
		Transport: transport.WithRateLimiting(nil),
	}
	return anthropic.NewClient(
		option.WithHTTPClient(rateLimitedHTTPClient),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(5),
	)
}

func createTelemetryProvider(ctx context.Context) (*telemetry.Provider, error) {
	telemetryConfig := telemetry.TelemetryConfig{
		Enabled:        config.TelemetryEnabled,
		JaegerEndpoint: config.JaegerEndpoint,
	}
	return telemetry.NewProvider(ctx, telemetryConfig)
}

// remoteValidationWorkspaceFactory creates instances of RemoteValidationWorkspace
type remoteValidationWorkspaceFactory struct {
	githubClient           *github.Client
	validationWorkflowName string
}

func (rvwf *remoteValidationWorkspaceFactory) NewWorkspace(ctx context.Context, tsk task.Task) (bot.Workspace, error) {
	return workspace.NewRemoteValidationWorkspace(ctx, rvwf.githubClient, rvwf.validationWorkflowName, tsk)
}
