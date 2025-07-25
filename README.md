# The Blundering Savant AI Coding Agent

Blundering Savant is a generative AI coding agent that presents as a GitHub user. The agent receives instructions via
issues, reviews, and comments and proposes code changes by creating and updating pull requests.

Generative AI is fallible in ways similar to people. It makes typos, misinteprets requirements, overcomplicates, and
falls down rabbit holes. We already have tools to help fallible individuals code together: issues, pull requests,
and code reviews. Let's apply those same tools to collaborate with a new breed of intelligence.

## Setup

### GitHub User Setup

1. Create a new GitHub user account for your bot
    - Do not use your main GitHub account, for the same reasons that you would not share a GitHub account with a coworker
    - Be transparent: in the account's bio, disclose that the account is a bot
1. Generate a Personal Access Token[^1]:
    - Go to Settings → Developer settings → Personal access tokens → Tokens (classic)
    - Click "Generate new token"
    - Select scopes:
      - `repo` (Full control of private repositories)
1. Add the bot to a project as a collaborator
    - Switch to your main GitHub account
    - Navigate to a GitHub repository that you are the owner of
    - Go to Settings → Collaborators
    - Click "Add people"
    - Search for your bot account via whatever method you prefer and select it
    - Click "Add to repository"
    - Switch to your bot account to accept the invite

[^1]: There is currently no way to generate fine-grained access tokens for collaborator access to repositories owned by
individuals. When you give a classic Personal Access Token to the bot, you should assume from that point on that it can
and will go rogue and attempt to abuse the broad permissions of that access token. As a repository owner, use
collaborator permission settings and protected branches to restrict the bot's permissions to only the minimum required
to perform its intended functions.

### Anthropic API Setup

1. Sign up for an Anthropic account at https://console.anthropic.com
1. Generate an API key from the console
1. Ensure you have sufficient credits for API usage

### Configuration

1. Clone this repository:
```bash
git clone <repository-url>
cd blundering-savant
```

2. Copy the environment template:
```bash
cp .env.example .env
```

3. Edit `.env` with your credentials:
```env
GITHUB_TOKEN=ghp_<your_github_token>
GITHUB_USERNAME=<your-bot-username>
ANTHROPIC_API_KEY=sk-ant-<your-anthropic-api-key>
```

### Running the Bot

Prerequisites: Install [Just](https://github.com/casey/just) command runner.

1. Build: `just build`
1. Run: `just run`  
1. Logs: `just logs`
1. Stop: `just stop`

Run `just` or `just help` to see all available commands.

## Usage

1. **Assign Issues**: Assign GitHub issues to your bot's username
1. **Wait for PR**: The bot will analyze the issue and create a PR
1. **Review and Repeat**: Comment on the PR with any requested changes and wait for the bot to update the PR
1. **Merge**: Once satisfied, merge the PR (the bot cannot merge PRs)

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

- The bot cannot compile code
- The bot cannot run tests
- The bot can only use one branch and create one PR per issue
- The bot cannot create new issues
- The bot cannot approve or merge its own PRs, by design
- The bot's speed is constrained primarily by generative AI API rate limits
- Issue descriptions must be detailed
  - Current AI models avoid asking clarifying questions and prefer to guess
