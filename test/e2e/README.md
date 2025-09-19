# End-to-End Prompt Testing Framework

This directory contains end-to-end tests for validating AI prompt behavior. These tests use the actual Anthropic API and incur real costs, so they are designed to run manually rather than in CI.

## Purpose

The end-to-end tests help validate prompt changes by:

1. Constructing simulated conversation histories
2. Sending them to the actual AI
3. Making assertions on response features 
4. Running multiple iterations for consistency

## Running Tests

### Prerequisites

Set up your environment with the required API keys:

```bash
export ANTHROPIC_API_KEY="your-api-key"
export GITHUB_TOKEN="your-github-token"  # Optional, for GitHub API tests
```

### Running All End-to-End Tests

```bash
go test -tags=e2e ./test/e2e/...
```

### Running Specific Test Suites

```bash
# Test tool calling behavior
go test -tags=e2e ./test/e2e/tools/...

# Test conversation management
go test -tags=e2e ./test/e2e/conversation/...

# Test limitation reporting
go test -tags=e2e ./test/e2e/limitations/...
```

### Running with Verbose Output

```bash
go test -tags=e2e -v ./test/e2e/...
```

## Test Structure

Each test follows the pattern:

1. **Setup**: Create a conversation and simulated history
2. **Execute**: Send message to AI and get response
3. **Assert**: Validate response characteristics
4. **Repeat**: Run multiple iterations for consistency

## Test Categories

- **Tool Calling Tests**: Verify the AI uses tools correctly (parallel calls, proper parameters)
- **Conversation Tests**: Test conversation resumption and summarization
- **Limitation Tests**: Ensure AI reports limitations instead of attempting workarounds
- **Response Format Tests**: Validate response structure and content

## Adding New Tests

1. Create a new test file in the appropriate subdirectory
2. Add the `//go:build e2e` build tag at the top
3. Follow the existing test harness patterns
4. Use the shared test utilities in the `testutil` package

## Cost Considerations

These tests use real API calls and cost money. Use judiciously:

- Run before releases to validate prompt changes
- Run when iterating on prompts for specific behaviors
- Consider using smaller models or shorter conversations when possible
- Monitor API usage and costs

## Test Configuration

Tests can be configured with environment variables:

- `E2E_MODEL`: Anthropic model to use (default: claude-3-5-sonnet-20241022)
- `E2E_MAX_TOKENS`: Maximum tokens per response (default: 4000) 
- `E2E_ITERATIONS`: Number of iterations per test (default: 3)
- `E2E_TIMEOUT`: Test timeout in seconds (default: 300)