# Contributing to Blundering Savant

Thank you for your interest in contributing to the Blundering Savant project! This document provides instructions for setting up a development environment and contributing to the codebase.

## Development Setup

### Prerequisites

1. **Go**: Install Go 1.24 or later from [https://golang.org/dl/](https://golang.org/dl/)
2. **Git**: Ensure you have Git installed
3. **GitHub Account**: You'll need a GitHub account for testing and contributing

### Local Development Environment

1. **Clone the repository**:
```bash
git clone https://github.com/cchalm/blundering-savant.git
cd blundering-savant
```

2. **Install development tools**:
```bash
# Install Just command runner
sudo apt install just  # Ubuntu/Debian
# or
brew install just      # macOS
# or see: https://github.com/casey/just#installation

# Install golangci-lint for linting
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin
```

3. **Set up configuration**:
```bash
# Copy the environment template
cp .env.example .env

# Edit .env with your credentials
# You'll need:
# - A GitHub Personal Access Token with repo and workflow scopes
# - An Anthropic API key
```

4. **Build and test**:
```bash
# Build the project
just build

# Run tests
just test

# Run linting
just lint

# See all available commands
just help
```

### Environment Configuration

Edit the `.env` file with your development credentials:

```env
# GitHub Configuration
SYSTEM_GITHUB_TOKEN=ghp_<your_github_token>
BOT_GITHUB_TOKEN=ghp_<your_github_token>  # Can be the same as SYSTEM_GITHUB_TOKEN for development

# Anthropic Configuration
ANTHROPIC_API_KEY=sk-ant-<your-anthropic-api-key>

# Bot Configuration
CHECK_INTERVAL=1m
LOG_LEVEL=debug  # Use debug for development
RESUMABLE_CONVERSATIONS_DIR=./conversations
VALIDATION_WORKFLOW_NAME=go.yml
```

### Running the Bot Locally

1. **Run in polling mode** (continuously checks for assigned issues):
```bash
just run
```

2. **Process a specific issue**:
```bash
just build
./bin/blundering-savant oneshot --repo owner/repository --issue 123
```

3. **View logs**:
```bash
just logs
```

4. **Stop the bot**:
```bash
just stop
```

### Testing Your Changes

1. **Run unit tests**:
```bash
just test
```

2. **Run linting**:
```bash
just lint
```

3. **Test with a real issue**:
   - Create a test repository or use an existing one
   - Create an issue and assign it to your bot account
   - Run the bot locally to process the issue

### Code Style

This project follows the Go style guide outlined in [`STYLE_GUIDE.md`](STYLE_GUIDE.md). Key points:

- Use `gofmt` and `goimports` for formatting
- Follow Go naming conventions (camelCase for private, PascalCase for public)
- Write comprehensive tests for new functionality
- Document exported functions and types
- Handle errors explicitly

### Submitting Changes

1. **Fork the repository** on GitHub
2. **Create a feature branch**: `git checkout -b feature/your-feature-name`
3. **Make your changes** following the coding standards
4. **Test thoroughly** including unit tests and manual testing
5. **Commit with descriptive messages**
6. **Push to your fork**: `git push origin feature/your-feature-name`
7. **Create a Pull Request** with:
   - Clear description of what the change does
   - Why the change is needed
   - Any testing performed
   - Screenshots or examples if applicable

### Available Just Commands

Run `just` or `just help` to see all available commands. Common ones include:

- `just build` - Build the binary
- `just test` - Run tests
- `just lint` - Run linting
- `just run` - Start the bot in polling mode
- `just stop` - Stop the running bot
- `just logs` - View bot logs
- `just clean` - Clean build artifacts

### Project Structure

- `app/blundering-savant/` - Main application entry point
- `internal/` - Private Go packages
  - `ai/` - AI conversation and prompt handling
  - `bot/` - Core bot logic and tool implementations
  - `task/` - Task generation and processing
  - `transport/` - GitHub API interactions
  - `validator/` - Code validation logic
  - `workspace/` - File system and Git operations
- `.github/workflows/` - GitHub Actions workflows
- `STYLE_GUIDE.md` - Coding standards and best practices

### Getting Help

- Check the [Issues](https://github.com/cchalm/blundering-savant/issues) page for existing questions
- Create a new issue for bugs or feature requests
- Review the [Style Guide](STYLE_GUIDE.md) for coding standards
- Look at existing code for examples and patterns

## License

By contributing to this project, you agree that your contributions will be licensed under the MIT License.