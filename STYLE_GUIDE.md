# Go Style Guide

This document outlines the coding standards and best practices for the Blundering Savant Bot project. Following these guidelines ensures consistency, readability, and maintainability of the codebase.

## Table of Contents

- [General Principles](#general-principles)
- [Code Formatting](#code-formatting)
- [Naming Conventions](#naming-conventions)
- [Package Organization](#package-organization)
- [Error Handling](#error-handling)
- [Context Usage](#context-usage)
- [Concurrency](#concurrency)
- [Testing](#testing)
- [Documentation](#documentation)
- [Security](#security)
- [Tools and Linters](#tools-and-linters)

## General Principles

### Effective Go
Follow the official [Effective Go](https://golang.org/doc/effective_go.html) guidelines as the foundation for all Go code.

### Go Proverbs
Keep these Go proverbs in mind:
- Don't communicate by sharing memory, share memory by communicating
- Concurrency is not parallelism
- Channels orchestrate; mutexes serialize
- The bigger the interface, the weaker the abstraction
- Make the zero value useful
- interface{} says nothing
- Gofmt's style is no one's favorite, yet gofmt is everyone's favorite
- A little copying is better than a little dependency
- Syscall must always be guarded with build tags
- Cgo must always be guarded with build tags
- Cgo is not Go
- With the unsafe package there are no guarantees
- Clear is better than clever
- Reflection is never clear
- Errors are values
- Don't just check errors, handle them gracefully
- Design the architecture, name the components, document the details
- Documentation is for users

## Code Formatting

### gofmt and goimports
- Always use `gofmt` to format your code before committing
- Use `goimports` to automatically manage imports
- Configure your editor to run these tools on save

### Line Length
- Keep lines under 100 characters when possible
- Break long function signatures and calls across multiple lines for readability

### Imports
```go
// Group imports in this order:
// 1. Standard library
// 2. Third-party packages
// 3. Local packages

import (
    "context"
    "fmt"
    "net/http"

    "github.com/google/go-github/v72/github"
    "golang.org/x/oauth2"

    "github.com/cchalm/blundering-savant/internal/config"
    "github.com/cchalm/blundering-savant/internal/github"
)
```

## Naming Conventions

### General Rules
- Use camelCase for local variables and unexported functions
- Use PascalCase for exported functions, types, and constants
- Use MixedCaps instead of underscores (Go convention)
- Avoid stuttering (e.g., `http.HTTPServer` should be `http.Server`)
- All letters in an initialism should be the same case (e.g., `httpClient`, `HTTPClient`, `parseJSONFile`)

### Variables
```go
// Good
var userCount int
var maxRetries = 3
var ErrNotFound = errors.New("not found")

// Bad
var user_count int                          // Uses underscores instead of camelCase
var MaxRetries = 3                         // Private variable shouldn't be exported
var errNotFound = errors.New("not found") // Error should be exported for use across packages
```

### Functions
```go
// Good
func processGitHubEvent(event *github.Event) error { ... } // Private helper function
func NewClient(token string) *Client { ... } // Public constructor

// Bad
func InternalHelper(data []byte) error { ... } // Internal function shouldn't be exported
func newclient(token string) *Client { ... }   // Constructor should be exported and use proper camelCase
```

### Types
```go
// Good
type GitHubClient struct { ... }
type EventHandler interface { ... }

// Bad
type githubClient struct { ... }    // Should be exported if used across packages
type eventhandler interface { ... } // Should use PascalCase instead of lowercase
```

### Interfaces
- Use `-er` suffix for single-method interfaces (Go convention)
- Keep interfaces small and focused ("The bigger the interface, the weaker the abstraction")
- Define interfaces at the point of use, not the point of implementation
- Accept interfaces, return concrete types

```go
// Good
type Reader interface {
    Read([]byte) (int, error)
}

type GitHubProcessor interface {
    ProcessIssue(ctx context.Context, issue *github.Issue) error
    ProcessPullRequest(ctx context.Context, pr *github.PullRequest) error
}

// Bad
type GitHubHandler interface {
    ProcessIssue(ctx context.Context, issue *github.Issue) error
    ProcessPullRequest(ctx context.Context, pr *github.PullRequest) error
    ValidateWebhook(payload []byte) error
    HandleError(err error)
    LogActivity(message string)
    // Too many responsibilities
}
```

## Package Organization


### Package Naming
- Use short, lowercase package names
- Avoid underscores, hyphens, or mixed caps
- Use singular nouns (e.g., `user`, not `users`)

### Internal vs External Packages
- Use `internal/` for code that shouldn't be imported by other projects
- Use `pkg/` for reusable library code
- Keep the main business logic in `internal/`

## Error Handling

### Error Creation
```go
// Good - Use fmt.Errorf for simple errors
func validateToken(token string) error {
    if token == "" {
        return fmt.Errorf("token cannot be empty")
    }
    return nil
}

// Good - Use custom error types for complex errors
type ValidationError struct {
    Field   string
    Message string
}

func (e ValidationError) Error() string {
    return fmt.Sprintf("validation failed for %s: %s", e.Field, e.Message)
}
```

### Error Wrapping
```go
// Good - Wrap errors to provide context
func processIssue(ctx context.Context, issueNumber int) error {
    issue, err := githubClient.GetIssue(ctx, issueNumber)
    if err != nil {
        return fmt.Errorf("failed to fetch issue %d: %w", issueNumber, err)
    }

    if err := validateIssue(issue); err != nil {
        return fmt.Errorf("issue validation failed: %w", err)
    }

    return nil
}
```

### Error Handling Best Practices
- Always handle errors explicitly
- Don't ignore errors with `_` unless absolutely necessary
- Use `errors.Is()` and `errors.As()` for error checking
- Return errors as the last return value
- Don't panic in library code

## Context Usage

### Context as First Parameter
In Go, context should always be the first parameter in function signatures (when used):

### Context Propagation
```go
// Good - Always accept context as first parameter
func processWebhook(ctx context.Context, payload []byte) error {
    // Pass context down the call chain
    event, err := parseEvent(ctx, payload)
    if err != nil {
        return err
    }

    return handleEvent(ctx, event)
}

// Good - Use context for cancellation and timeouts
func makeAPICall(ctx context.Context, url string) (*Response, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, err
    }

    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    // ... process response
}
```

### Context Values
- Use context values sparingly
- Create typed keys to avoid collisions
- Document context keys clearly

```go
type contextKey string

const (
    RequestIDKey contextKey = "request_id"
    UserIDKey    contextKey = "user_id"
)

func withRequestID(ctx context.Context, requestID string) context.Context {
    return context.WithValue(ctx, RequestIDKey, requestID)
}

func getRequestID(ctx context.Context) string {
    if id, ok := ctx.Value(RequestIDKey).(string); ok {
        return id
    }
    return ""
}
```

## Concurrency

### Channels
Go's channels provide a way to implement "Don't communicate by sharing memory; share memory by communicating":

- Use channels to communicate between goroutines
- Close channels when done sending (sender's responsibility)
- Use `select` for non-blocking operations and timeouts
- Prefer buffered channels for producer-consumer patterns

```go
// Good - Channel patterns
func worker(ctx context.Context, jobs <-chan Job, results chan<- Result) {
    for {
        select {
        case job, ok := <-jobs:
            if !ok {
                return // Channel closed
            }
            result := processJob(job)
            select {
            case results <- result:
            case <-ctx.Done():
                return
            }
        case <-ctx.Done():
            return
        }
    }
}
```

## Testing

### Test Coverage

Aim loosely for %80 test coverage. The final 20% tends to produce a lot of false positives, which slows down subsequent
development. Invest in static analysis instead.

Do not write tests for trivial functions:

```go
func colorToString(color Color) string {
    switch color {
    case Red:
        return "red"
    case Green:
        return "green"
    case Blue:
        return "blue"
    default:
        panic("unknown color")
    }
}

// Do not write this test
func TestColorToString(t *testing.T) {
    require.Equal(t, colorToString(Red), "red")
    require.Equal(t, colorToString(Green), "green")
    require.Equal(t, colorToString(Blue), "blue")
    require.Panics(t, func() { colorToString(None) })
}
```

The test above is just as likely to contain errors as the code it tests, so it provides little-to-no real coverage.

### Test Naming
- Use `Test` prefix for test functions
- Use underscores to split test names into phrases following the pattern `Test{Function}_{Condition}`
    - e.g. `TestProcessIssue_Nil`
- Use concise names. When testing complex conditions, add a descriptive comment within the test

### Test Structure
Prefer test harnesses over table-driven tests for better IDE support:

```go
// testProcessIssue is an example of a test harness
func testProcessIssue(t *testing.T, wantErr bool, issue *github.Issue) {
    err := processIssue(context.Background(), issue)
    if (err != nil) != wantErr {
        t.Errorf("processIssue() error = %v, wantErr %v", err, wantErr)
    }
}

func TestProcessIssue_Valid(t *testing.T) {
    testProcessIssue(t,
        false, // wantErr
        &github.Issue{
            Number: github.Int(1),
            Title:  github.String("Test issue"),
        },
    )
}

func TestProcessIssue_Nil(t *testing.T) {
    testProcessIssue(t,
        true, // wantErr
        nil,
    )
}
```

### Mocking with Interfaces
Go's interfaces enable easy mocking for testing. Define interfaces for dependencies:

```go
// Define interface for external dependencies
type GitHubClient interface {
    GetIssue(ctx context.Context, number int) (*github.Issue, error)
    CreateComment(ctx context.Context, number int, comment string) error
}

// Concrete implementation
type githubClient struct {
    client *github.Client
}
```

Then fake, stub, or mock the dependency ([learn about the difference](https://www.martinfowler.com/articles/mocksArentStubs.html#TheDifferenceBetweenMocksAndStubs)):

```go
// Stub implementation for testing
//
// Stubs can be implemented various ways, but this stub demonstrates common strategies for getters and setters,
// respectively:
// - Getters: return predetermined state, e.g. stored in memory or hard-coded in the stubbed function
// - Setters: keep track of modifications in-memory for later state verification
type stubGitHubClient struct {
    issues   map[int]*github.Issue
    comments []string
}

func (m *stubGitHubClient) GetIssue(ctx context.Context, number int) (*github.Issue, error) {
    if issue, ok := m.issues[number]; ok {
        return issue, nil
    }
    return nil, fmt.Errorf("issue not found")
}

func (m *stubGitHubClient) CreateComment(ctx context.Context, number int, comment string) error {
    m.comments = append(m.comments, comment)
    return nil
}
```

Use [mockery](https://vektra.github.io/mockery/latest/) to generate mocks when needed.

## Documentation

Go has specific documentation conventions that work with `go doc` and `godoc`:

### Package Documentation
```go
// Package github provides GitHub API integration for the Blundering Savant Bot.
// It handles authentication, webhook processing, and API interactions.
package github
```

### Function Documentation
```go
// ProcessIssue analyzes a GitHub issue and creates an appropriate response.
// It validates the issue, determines the required action, and executes it.
// Returns an error if the issue cannot be processed.
func ProcessIssue(ctx context.Context, issue *github.Issue) error {
    // Implementation
}
```

### Documentation Best Practices
- Start comments with the name of the thing being described
- Use complete sentences
- Don't start comments with "This function..." or "This method..."
- Document exported functions, types, constants, and variables
- Code should be organized to minimize documentation needs through cohesive functions with descriptive names

### Type Documentation
```go
// Client represents a GitHub API client with authentication.
type Client struct {
    client *github.Client
    token  string
}

// Config holds the configuration for GitHub integration.
type Config struct {
    // Token is the GitHub personal access token
    Token string `json:"token"`

    // Org is the GitHub organization name
    Org string `json:"org"`

    // Repo is the GitHub repository name
    Repo string `json:"repo"`
}
```


## Security

### Secrets Management
- **Never** commit secrets, API keys, tokens, or passwords to version control
- Use environment variables for sensitive configuration
- Use a secrets management system in production (e.g., AWS Secrets Manager, HashiCorp Vault)
- Rotate secrets regularly

```go
// Good - Load sensitive data from environment
func NewGitHubClient() *Client {
    token := os.Getenv("GITHUB_TOKEN")
    if token == "" {
        log.Fatal("GITHUB_TOKEN environment variable is required")
    }
    return &Client{token: token}
}

// Bad - Hard coding secrets
func NewGitHubClient() *Client {
    return &Client{token: "ghp_hardcoded_token_123"} // Never do this!
}
```

### Input Validation
- Validate and sanitize all external inputs
- Use allowlists instead of blocklists when possible
- Validate webhook signatures from GitHub

```go
// Good - Validate webhook payload
func validateWebhookSignature(payload []byte, signature string, secret string) error {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    expectedSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
        return errors.New("invalid webhook signature")
    }
    return nil
}
```

## Tools and Linters

### Required Tools
- `gofmt` - Code formatting
- `goimports` - Import management
- `go vet` - Static analysis
- `golangci-lint` - Comprehensive linting

### Recommended IDE Setup
- Configure your editor to run `gofmt` and `goimports` on save
- Enable `go vet` integration
- Set up `golangci-lint` for real-time feedback



## Conclusion

This style guide should be treated as a living document that evolves with the project and Go ecosystem. When in doubt, prioritize:

1. Readability over cleverness
2. Simplicity over complexity
3. Explicit over implicit
4. Standard library over third-party dependencies

For questions not covered in this guide, refer to:
- [Effective Go](https://golang.org/doc/effective_go.html)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)