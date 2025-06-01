# Virtual Developer Bot

A GitHub bot powered by Anthropic's Claude API that acts as a virtual developer, automatically solving issues and creating pull requests.

## Features

- 🤖 **Automated Issue Resolution**: Monitors GitHub issues assigned to the bot and creates PRs to solve them
- 📝 **Style Guide Compliance**: Automatically detects and follows repository coding standards
- 💬 **Interactive PR Management**: Responds to comments and review feedback on pull requests
- 🔄 **Continuous Monitoring**: Regularly checks for new issues and PR updates
- 🐳 **Docker Support**: Runs in a containerized environment for easy deployment

## Prerequisites

- Docker and Docker Compose
- GitHub Personal Access Token with appropriate permissions
- Anthropic API Key
- A GitHub account for the bot

## Setup

### 1. GitHub Setup

1. Create a new GitHub account for your bot (or use an existing one)
2. Generate a Personal Access Token:
   - Go to Settings → Developer settings → Personal access tokens → Tokens (classic)
   - Click "Generate new token"
   - Select scopes:
     - `repo` (Full control of private repositories)
     - `workflow` (Update GitHub Action workflows)
     - `write:discussion` (Write discussion comments)

### 2. Anthropic API Setup

1. Sign up for an Anthropic account at https://console.anthropic.com
2. Generate an API key from the console
3. Ensure you have sufficient credits for API usage

### 3. Configuration

1. Clone this repository:
```bash
git clone <repository-url>
cd virtual-developer-bot
```

2. Copy the environment template:
```bash
cp .env.example .env
```

3. Edit `.env` with your credentials:
```env
GITHUB_TOKEN=ghp_your_token_here
ANTHROPIC_API_KEY=sk-ant-your_key_here
GITHUB_USERNAME=your-bot-username
```

### 4. Running the Bot

Using Docker Compose:
```bash
docker-compose up -d
```

Using Docker directly:
```bash
docker build -t virtual-developer .
docker run -d \
  --name virtual-developer \
  -e GITHUB_TOKEN=$GITHUB_TOKEN \
  -e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY \
  -e GITHUB_USERNAME=$GITHUB_USERNAME \
  virtual-developer
```

## Usage

1. **Assign Issues**: Assign GitHub issues to your bot's username
2. **Wait for PR**: The bot will analyze the issue and create a PR within a few minutes
3. **Review and Feedback**: Comment on the PR with any requested changes
4. **Merge**: Once satisfied, merge the PR (the bot cannot merge PRs)

## How It Works

1. **Issue Detection**: The bot periodically scans for issues assigned to it
2. **Code Analysis**: It analyzes the repository structure and coding standards
3. **Solution Generation**: Uses Claude to generate appropriate code changes
4. **PR Creation**: Creates a new branch and pull request with the solution
5. **Feedback Loop**: Monitors and responds to PR comments and reviews

## Configuration Options

| Variable | Description | Default |
|----------|-------------|---------|
| `CHECK_INTERVAL` | How often to check for new issues | 5m |
| `PR_UPDATE_CHECK_INTERVAL` | How often to check PR updates | 2m |
| `MAX_CONCURRENT_ISSUES` | Max issues to process at once | 3 |
| `LOG_LEVEL` | Logging verbosity | info |

## Monitoring

View logs:
```bash
docker-compose logs -f virtual-developer
```

Check health:
```bash
docker-compose ps
```

## Best Practices

1. **Start Small**: Test with simple issues first
2. **Clear Issues**: Write detailed issue descriptions with acceptance criteria
3. **Review Carefully**: Always review generated code before merging
4. **Style Guides**: Maintain clear coding standards in your repository
5. **Permissions**: Only give the bot necessary repository permissions

## Limitations

- The bot cannot approve or merge its own PRs
- Complex architectural changes may require human intervention
- Limited by GitHub API rate limits
- Requires clear issue descriptions to work effectively

## Troubleshooting

### Bot not responding to issues
- Check logs: `docker-compose logs virtual-developer`
- Verify the issue is assigned to the correct username
- Ensure the bot has repository access

### API errors
- Verify your API keys are correct
- Check API rate limits and quotas
- Ensure sufficient Anthropic credits

### PR creation failures
- Check GitHub token permissions
- Verify branch protection rules allow bot PRs
- Review repository settings

## Security Considerations

- Keep API keys secure and never commit them
- Use environment variables for sensitive data
- Regularly rotate access tokens
- Monitor bot activity for unusual behavior
- Limit repository permissions to minimum required

## Contributing

Contributions are welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

## License

MIT License - See LICENSE file for details

## Support

For issues and questions:
- Create an issue in this repository
- Check existing issues for solutions
- Review logs for error messages

## Roadmap

- [ ] Add support for multiple programming languages
- [ ] Implement test generation
- [ ] Add database for state persistence
- [ ] Support for GitHub Actions integration
- [ ] Advanced code review capabilities
- [ ] Team collaboration features