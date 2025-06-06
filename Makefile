.PHONY: build run stop logs clean test lint setup help

# Default target
.DEFAULT_GOAL := help

# Variables
APP_NAME := halfanewgrad
DOCKER_IMAGE := $(APP_NAME):latest
COMPOSE_FILE := docker-compose.yml

## help: Display this help message
help:
	@echo "Halfanewgrad Bot - Available Commands:"
	@echo ""
	@awk 'BEGIN {FS = ":.*##"; printf "\033[36m%-15s\033[0m %s\n", "Command", "Description"} /^[a-zA-Z_-]+:.*?##/ { printf "\033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

## setup: Initial setup - copy env file and download dependencies
setup:
	@echo "Setting up halfanewgrad bot..."
	@test -f .env || cp .env.example .env
	@echo "✓ Environment file created (please edit .env with your credentials)"
	@go mod download
	@echo "✓ Go dependencies downloaded"
	@echo ""
	@echo "Next steps:"
	@echo "1. Edit .env with your GitHub and Anthropic credentials"
	@echo "2. Run 'make build' to build the Docker image"
	@echo "3. Run 'make run' to start the bot"

## build: Build the Docker image
build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE) .
	@echo "✓ Docker image built successfully"

## run: Run the bot using Docker Compose
run:
	@echo "Starting halfanewgrad Bot..."
	docker-compose up -d
	@echo "✓ Bot started successfully"
	@echo "Run 'make logs' to view logs"

## stop: Stop the bot
stop:
	@echo "Stopping halfanewgrad Bot..."
	docker-compose down
	@echo "✓ Bot stopped"

## restart: Restart the bot
restart: stop run

## logs: View bot logs
logs:
	docker-compose logs -f $(APP_NAME)

## logs-tail: View last 100 lines of logs
logs-tail:
	docker-compose logs --tail=100 $(APP_NAME)

## status: Check bot status
status:
	@echo "Halfanewgrad Bot Status:"
	@docker-compose ps

## clean: Clean up containers and images
clean:
	@echo "Cleaning up..."
	docker-compose down -v
	docker rmi $(DOCKER_IMAGE) || true
	@echo "✓ Cleanup complete"

## test: Run tests
test:
	@echo "Running tests..."
	go test -v ./...

## lint: Run linter
lint:
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

## fmt: Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "✓ Code formatted"

## update: Update dependencies
update:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✓ Dependencies updated"

## dev: Run locally without Docker (for development)
dev:
	@test -f .env || (echo "Error: .env file not found. Run 'make setup' first." && exit 1)
	@echo "Running halfanewgrad Bot locally..."
	go run .

## shell: Open a shell in the running container
shell:
	docker-compose exec $(APP_NAME) /bin/sh

## env-check: Verify environment variables are set
env-check:
	@test -f .env || (echo "Error: .env file not found. Run 'make setup' first." && exit 1)
	@echo "Checking environment variables..."
	@grep -q "GITHUB_TOKEN=ghp_" .env || echo "⚠️  Warning: GITHUB_TOKEN not set"
	@grep -q "ANTHROPIC_API_KEY=sk-ant-" .env || echo "⚠️  Warning: ANTHROPIC_API_KEY not set"
	@grep -q "GITHUB_USERNAME=.+" .env || echo "⚠️  Warning: GITHUB_USERNAME not set"
	@echo "✓ Environment check complete"

## docker-logs: View Docker daemon logs
docker-logs:
	docker logs $(shell docker ps -q -f name=$(APP_NAME))

## health: Check health of the service
health:
	@docker-compose exec $(APP_NAME) /bin/sh -c "ps aux | grep '[v]irtual-developer'" > /dev/null 2>&1 && echo "✓ Bot is healthy" || echo "✗ Bot is not running"