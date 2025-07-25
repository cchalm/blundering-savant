# Default target
.DEFAULT_GOAL := help

# Variables
APP_NAME := blundering-savant
SERVICE_NAME := bot
DOCKER_IMAGE := $(APP_NAME):latest
COMPOSE_FILE := docker-compose.yml

## help: Display this help message
help:
	@echo "Blundering Savant Bot - Available Commands:"
	@echo ""
	@awk 'BEGIN {FS = ":.*##"; printf "\033[36m%-15s\033[0m %s\n", "Command", "Description"} /^[a-zA-Z_-]+:.*?##/ { printf "\033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
.PHONY: help

## build: Build the Docker image
build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE) .
	@echo "✓ Docker image built successfully"
.PHONY: build

## run: Run the bot using Docker Compose
run:
	@echo "Starting blundering-savant Bot..."
	docker-compose up -d
	@echo "✓ Bot started successfully"
	@echo "Run 'make logs' to view logs"
.PHONY: run

## stop: Stop the bot
stop:
	@echo "Stopping blundering-savant Bot..."
	docker-compose down
	@echo "✓ Bot stopped"
.PHONY: stop

## restart: Restart the bot
restart: stop run
.PHONY: restart

## logs: View bot logs
logs:
	docker-compose logs -f ${SERVICE_NAME}
.PHONY: logs

## logs-tail: View last 100 lines of logs
logs-tail:
	docker-compose logs --tail=100 ${SERVICE_NAME}
.PHONY: logs-tail

## status: Check bot status
status:
	@echo "Blundering Savant Bot Status:"
	@docker-compose ps
.PHONY: status

## clean: Clean up containers and images
clean:
	@echo "Cleaning up..."
	docker-compose down -v
	docker rmi $(DOCKER_IMAGE) || true
	@echo "✓ Cleanup complete"
.PHONY: clean

## test: Run tests
test:
	@echo "Running tests..."
	go test -v ./...
.PHONY: test

## lint: Run linter
lint:
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run
.PHONY: lint

## fmt: Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "✓ Code formatted"
.PHONY: fmt

## update-deps: Update dependencies
update-deps:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✓ Dependencies updated"
.PHONY: update-deps

## run-local: Run locally without Docker (for development)
run-local:
	@test -f .env || (echo "Error: .env file not found. Run 'make setup' first." && exit 1)
	@echo "Running blundering-savant Bot locally..."
	go run .
.PHONY: run-local

## connect-shell: Open a shell in the running container
connect-shell:
	docker-compose exec $(APP_NAME) /bin/sh
.PHONY: connect-shell

## docker-logs: View Docker daemon logs
docker-logs:
	docker logs $(shell docker ps -q -f name=$(APP_NAME))
.PHONY: docker-logs
