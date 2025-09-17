# The Blundering Savant AI Coding Agent

[![Build Status](https://github.com/cchalm/blundering-savant/actions/workflows/go.yml/badge.svg)](https://github.com/cchalm/blundering-savant/actions/workflows/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/cchalm/blundering-savant)](https://goreportcard.com/report/github.com/cchalm/blundering-savant)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Blundering Savant is a generative AI coding agent that presents as a GitHub user. The agent receives instructions via
issues, reviews, and comments and proposes code changes by creating and updating pull requests.

Generative AI is fallible in ways similar to people. It makes typos, misinteprets requirements, overcomplicates, and
falls down rabbit holes. We already have tools to help fallible individuals code together: issues, pull requests,
and code reviews. Let's apply those same tools to collaborate with a new breed of intelligence.

## Installation

### Option 1: GitHub Action (Recommended)

The easiest way to run the bot is as a GitHub Action that automatically responds to assigned issues and PR comments.

#### Prerequisites

1. **Create a Bot GitHub Account**:
    - Create a new GitHub user account for your bot
    - Do not use your main GitHub account, for the same reasons that you would not share a GitHub account with a coworker
    - Be transparent: in the account's bio, disclose that the account is a bot

2. **Generate Personal Access Token**[^1]:
    - Go to Settings → Developer settings → Personal access tokens → Tokens (classic)
    - Click "Generate new token"
    - Select scopes:
      - `repo` (Full control of private repositories)
      - `workflow` (If the bot should be allowed to modify `.github/workflows`)

3. **Add Bot as Collaborator**:
    - Navigate to your GitHub repository
    - Go to Settings → Collaborators
    - Click "Add people"
    - Search for your bot account and add it to the repository
    - Switch to your bot account to accept the invite

4. **Get Anthropic API Key**:
    - Sign up for an Anthropic account at https://console.anthropic.com
    - Generate an API key from the console
    - Ensure you have sufficient credits for API usage

[^1]: There is currently no way to generate fine-grained access tokens for collaborator access to repositories owned by individuals. When you give a classic Personal Access Token to the bot, you should assume from that point on that it can and will go rogue and attempt to abuse the broad permissions of that access token. As a repository owner, use collaborator permission settings and protected branches to restrict the bot's permissions to only the minimum required to perform its intended functions.

#### Setup Instructions

1. **Configure Repository Variables**:
   - Go to your repository → Settings → Secrets and variables → Actions → Variables tab
   - Add the following repository variables:
     - `BOT_USERNAME`: Your bot's GitHub username
     - `AUTHORIZED_USERNAME`: Your main GitHub username (who can trigger the bot)

2. **Configure Repository Secrets**:
   - Go to your repository → Settings → Secrets and variables → Actions → Secrets tab
   - Add the following repository secrets:
     - `BOT_GITHUB_TOKEN`: The Personal Access Token from your bot account
     - `ANTHROPIC_API_KEY`: Your Anthropic API key

3. **Add the GitHub Action Workflow**:
   - Create `.github/workflows/bot.yml` in your repository with the following content:

```yaml
name: Bot Runner
on:
  issues:
    types: [assigned, labeled]
  issue_comment:
    types: [created]
  pull_request:
    types: [synchronize]
  pull_request_review:
    types: [submitted]

jobs:
  run:
    permissions:
      actions: write        # To create workflow dispatch events
      contents: write       # To make code changes
      issues: write         # To edit labels and comment on issues
      pull-requests: write  # To edit labels and comment on pull requests

    runs-on: ubuntu-latest

    # Prevent multiple simultaneous runs per repository target (issue or PR)
    concurrency:
      group: bot-${{ github.repository }}-${{ github.event.issue.number || github.event.pull_request.number || github.event.review.pull_request.number || 'unknown' }}
      cancel-in-progress: true

    # Run if the triggering user is the authorized user AND the issue is assigned to the bot OR the PR is owned by the bot
    if: >
      github.actor == vars.AUTHORIZED_USERNAME &&
      (
        (github.event.issue && github.event.issue.assignee.login == vars.BOT_USERNAME) ||
        (github.event.pull_request && github.event.pull_request.user.login == vars.BOT_USERNAME)
      )

    steps:
    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.24.4'

    - name: Install bot
      run: go install github.com/cchalm/blundering-savant/app/blundering-savant@latest

    - name: Run bot
      timeout-minutes: 30
      env:
        SYSTEM_GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        BOT_GITHUB_TOKEN: ${{ secrets.BOT_GITHUB_TOKEN }}
        ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
      run: |
        echo "Processing ${{ github.event_name }}.${{ github.event.action }} by ${{ github.actor }}"

        # Determine whether to use issue number or PR branch
        if [ -n "${{ github.event.issue.number }}" ]; then
          echo "Repository: ${{ github.repository }} / Issue #${{ github.event.issue.number }}"
          blundering-savant oneshot \
            --repo ${{ github.repository }} \
            --issue ${{ github.event.issue.number }} \
            --validation-workflow go.yml
        elif [ -n "${{ github.event.pull_request.head.ref }}" ]; then
          echo "Repository: ${{ github.repository }} / PR branch '${{ github.event.pull_request.head.ref }}'"
          blundering-savant oneshot \
            --repo ${{ github.repository }} \
            --pr-branch ${{ github.event.pull_request.head.ref }} \
            --validation-workflow go.yml
        else
          echo "Error: Neither issue number nor PR branch available"
          exit 1
        fi

    - name: Upload logs on failure
      if: failure()
      uses: actions/upload-artifact@v4
      with:
        name: bot-logs-${{ github.run_id }}
        path: logs/
        retention-days: 7
```

### Option 2: Pre-built Binary

Download the latest release from the [releases page](https://github.com/cchalm/blundering-savant/releases) for your platform:

1. **Download and Install**:
```bash
# For Linux x64
wget https://github.com/cchalm/blundering-savant/releases/latest/download/blundering-savant-linux-amd64
chmod +x blundering-savant-linux-amd64
sudo mv blundering-savant-linux-amd64 /usr/local/bin/blundering-savant

# For macOS (Intel)
wget https://github.com/cchalm/blundering-savant/releases/latest/download/blundering-savant-darwin-amd64
chmod +x blundering-savant-darwin-amd64
sudo mv blundering-savant-darwin-amd64 /usr/local/bin/blundering-savant

# For macOS (Apple Silicon)
wget https://github.com/cchalm/blundering-savant/releases/latest/download/blundering-savant-darwin-arm64
chmod +x blundering-savant-darwin-arm64
sudo mv blundering-savant-darwin-arm64 /usr/local/bin/blundering-savant
```

2. **Set up environment variables**:
```bash
export SYSTEM_GITHUB_TOKEN=ghp_<your_github_token>
export BOT_GITHUB_TOKEN=ghp_<your_github_token>
export ANTHROPIC_API_KEY=sk-ant-<your-anthropic-api-key>
```

3. **Run the bot**:
```bash
# Process a specific issue
blundering-savant oneshot --repo owner/repository --issue 123

# Run in polling mode (continuously check for new issues)
blundering-savant poll --repo owner/repository
```

### Option 3: Install via Go

If you have Go installed, you can install directly from source:

```bash
go install github.com/cchalm/blundering-savant/app/blundering-savant@latest
```

Then set up environment variables and run as described in Option 2.

## Usage

1. **Assign Issues**: Assign GitHub issues to your bot's username
1. **Wait for PR**: The bot will analyze the issue and create a PR
1. **Review and Repeat**: Comment on the PR with any requested changes and wait for the bot to update the PR
1. **Merge**: Once satisfied, merge the PR (the bot cannot merge PRs)

## Contributing

Interested in contributing to the project? See our [Contributing Guide](CONTRIBUTING.md) for development setup instructions, coding standards, and how to submit changes.

## Configuration Options

| Variable | Description | Default |
|----------|-------------|---------|
| `CHECK_INTERVAL` | How often to check for new issues and comments on GitHub | 5m |
| `LOG_LEVEL` | Logging verbosity | info |
| `RESUMABLE_CONVERSATIONS_DIR` | Directory in which to store interrupted conversation histories for later resumption | &lt;none&gt; |

## Best Practices

1. **Detailed Instructions**: The bot will get creative. If you want something specific, be specific
1. **Review Carefully**: Always review generated code before merging
1. **Style Guides**: Make implicit coding standards explicit with style guides

## Limitations

- The bot can only create one PR per issue
- The bot cannot create new issues
- The bot cannot approve or merge its own PRs, by design
- The bot's speed is constrained primarily by generative AI API rate limits
- Issue descriptions must be detailed
  - Current AI models avoid asking clarifying questions and prefer to guess
