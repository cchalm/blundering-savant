# Contributing to Blundering Savant

Thank you for your interest in contributing to the Blundering Savant project! This document provides instructions for setting up a development environment and contributing to the codebase.

## Development Setup

### Prerequisites

1. **Go**: Install Go 1.24 or later from [https://golang.org/dl/](https://golang.org/dl/)
2. **Git**: Ensure you have Git installed
3. **Docker**: Install Docker for containerized development and testing

### Setup

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
```

Edit the `.env` file with your development credentials:

```env
# GitHub Configuration
SYSTEM_GITHUB_TOKEN=ghp_<your_github_token>
BOT_GITHUB_TOKEN=ghp_<your_github_token>  # Can be the same as SYSTEM_GITHUB_TOKEN for development

# Anthropic Configuration
ANTHROPIC_API_KEY=sk-ant-<your-anthropic-api-key>

# Bot Configuration
CHECK_INTERVAL=1m
RESUMABLE_CONVERSATIONS_DIR=./conversations
VALIDATION_WORKFLOW_NAME=go.yml
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

This project follows the Go style guide outlined in [`STYLE_GUIDE.md`](STYLE_GUIDE.md). Please refer to that document for detailed coding standards and best practices.







### Getting Help

- Check the [Issues](https://github.com/cchalm/blundering-savant/issues) page for existing questions
- Create a new issue for bugs or feature requests
- Review the [Style Guide](STYLE_GUIDE.md) for coding standards
- Look at existing code for examples and patterns

## License

By contributing to this project, you agree that your contributions will be licensed under the MIT License.