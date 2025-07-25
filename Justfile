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

# Run linter
lint:
    @echo "Running linter..."
    @which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
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