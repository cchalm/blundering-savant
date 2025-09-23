# Variables
app_name := "blundering-savant"
service_name := "bot"
docker_image := app_name + ":latest"
compose_file := "docker-compose.yml"

# Default recipe - show help
default: help

# Display help message
help:
    @echo "Blundering Savant Bot - Available Commands:"
    @echo ""
    @just --list --unsorted

# Build the Docker image
build:
    @echo "Building Docker image..."
    docker build -t {{docker_image}} .
    @echo "✓ Docker image built successfully"

# Run the bot using Docker Compose
run:
    @echo "Starting blundering-savant Bot..."
    docker-compose up -d
    @echo "✓ Bot started successfully"
    @echo "Run 'just logs' to view logs"

# Stop the bot
stop:
    @echo "Stopping blundering-savant Bot..."
    docker-compose down
    @echo "✓ Bot stopped"

# Restart the bot
restart: stop run

# View bot logs
logs:
    docker-compose logs -f {{service_name}}

# View last 100 lines of logs
logs-tail:
    docker-compose logs --tail=100 {{service_name}}

# Check bot status
status:
    @echo "Blundering Savant Bot Status:"
    @docker-compose ps

# Clean up containers and images
clean:
    @echo "Cleaning up..."
    docker-compose down -v
    -docker rmi {{docker_image}}
    @echo "✓ Cleanup complete"

# Run tests
test:
    @echo "Running tests..."
    go test -v ./...

# Run end-to-end tests (requires API keys, costs money)
test-e2e:
    @echo "Running end-to-end tests..."
    @echo "Warning: These tests use real AI APIs and cost money!"
    @echo "Make sure ANTHROPIC_API_KEY is set."
    @read -p "Continue? (y/N) " confirm && [ "$$confirm" = "y" ] || exit 1
    go test -tags=e2e -v ./test/e2e/...

# Run linter
lint:
    @echo "Running linter..."
    @which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.3.1)
    golangci-lint run

# Format code
fmt:
    @echo "Formatting code..."
    go fmt ./...
    @echo "✓ Code formatted"

# Update dependencies
update-deps:
    @echo "Updating dependencies..."
    go get -u ./...
    go mod tidy
    @echo "✓ Dependencies updated"

# Run locally without Docker (for development)
run-local:
    @test -f .env || (echo "Error: .env file not found. Copy .env.example to .env first." && exit 1)
    @echo "Running blundering-savant Bot locally..."
    go run .

# Open a shell in the running container
connect-shell:
    docker-compose exec {{app_name}} /bin/sh

# View Docker daemon logs
docker-logs:
    docker logs $(docker ps -q -f name={{app_name}})

# Run integration tests
test-integ PACKAGE:
    #!/usr/bin/env bash

    echo "🚨 Running integration tests for '{{PACKAGE}}' - this will incur API costs!"

    read -p "Continue? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 0
    fi

    # Create timestamped artifact directory
    ARTIFACT_DIR="test-artifacts/integ-$(date +%Y-%m-%d-%H%M%S)"
    echo "📁 Artifacts will be saved to: $ARTIFACT_DIR"
    mkdir -p "$ARTIFACT_DIR"

    set -a
    source .env.test || { echo "❌ Failed to load .env.test"; exit 1; }
    set +a
    if [ -z "$ANTHROPIC_API_KEY" ]; then
        echo "❌ ANTHROPIC_API_KEY not found in .env.test"
        exit 1
    fi

    echo "🧪 Running integration tests for '{{PACKAGE}}' ..."

    TEST_ARTIFACTS_DIR="$PWD/$ARTIFACT_DIR" go test -tags=integ {{PACKAGE}}

# Clean old test artifacts (keeps last N runs)
clean-artifacts KEEP="5":
    #!/usr/bin/env bash
    echo "🧹 Cleaning test artifacts (keeping {{KEEP}} most recent runs)..."
    cd test-artifacts
    ls -1dt integ-* 2>/dev/null | tail -n +$(({{KEEP}} + 1)) | xargs -r rm -rf
    echo "✅ Cleanup complete"
