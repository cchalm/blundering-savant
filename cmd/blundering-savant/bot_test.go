package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"
)

// Helper function to create a comment with given ID and InReplyTo
func createComment(id int64, inReplyTo *int64) *github.PullRequestComment {
	return &github.PullRequestComment{
		ID:        &id,
		InReplyTo: inReplyTo,
		CreatedAt: &github.Timestamp{Time: time.UnixMilli(id)},
	}
}

// Helper function to get int64 pointer
func int64Ptr(i int64) *int64 {
	return &i
}

func TestOrganizePRReviewCommentsIntoThreads_SingleComment(t *testing.T) {
	comments := []*github.PullRequestComment{
		createComment(1, nil),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0], 1)
	require.Equal(t, int64(1), *threads[0][0].ID)
}

func TestOrganizePRReviewCommentsIntoThreads_SimpleThread(t *testing.T) {
	// Comment 1 is root, comment 2 replies to comment 1
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		createComment(2, int64Ptr(1)),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0], 2)
	require.Equal(t, int64(1), *threads[0][0].ID)
	require.Equal(t, int64(2), *threads[0][1].ID)
}

func TestOrganizePRReviewCommentsIntoThreads_SimpleThreadReversed(t *testing.T) {
	// Comment 1 is root, comment 2 replies to comment 1
	comments := []*github.PullRequestComment{
		createComment(2, int64Ptr(1)),
		createComment(1, nil),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0], 2)
	require.Equal(t, int64(1), *threads[0][0].ID)
	require.Equal(t, int64(2), *threads[0][1].ID)
}

func TestOrganizePRReviewCommentsIntoThreads_MultipleIndependentThreads(t *testing.T) {
	// Two separate threads: (1->2) and (3->4)
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		createComment(2, int64Ptr(1)),
		createComment(3, nil),
		createComment(4, int64Ptr(3)),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 2)

	// Sort threads by first comment ID for consistent testing
	if *threads[0][0].ID > *threads[1][0].ID {
		threads[0], threads[1] = threads[1], threads[0]
	}

	require.Len(t, threads[0], 2)
	require.Equal(t, int64(1), *threads[0][0].ID)
	require.Equal(t, int64(2), *threads[0][1].ID)

	require.Len(t, threads[1], 2)
	require.Equal(t, int64(3), *threads[1][0].ID)
	require.Equal(t, int64(4), *threads[1][1].ID)
}

func TestOrganizePRReviewCommentsIntoThreads_NilComment(t *testing.T) {
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		nil, // This should cause an error
		createComment(2, int64Ptr(1)),
	}

	_, err := organizePRReviewCommentsIntoThreads(comments)

	require.Error(t, err)
}

func TestOrganizePRReviewCommentsIntoThreads_NilCommentID(t *testing.T) {
	commentWithNilID := &github.PullRequestComment{
		ID:        nil,
		InReplyTo: nil,
	}

	comments := []*github.PullRequestComment{
		createComment(1, nil),
		commentWithNilID, // This should cause an error
		createComment(2, int64Ptr(1)),
	}

	_, err := organizePRReviewCommentsIntoThreads(comments)

	require.Error(t, err)
}

func TestOrganizePRReviewCommentsIntoThreads_EmptyInput(t *testing.T) {
	comments := []*github.PullRequestComment{}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 0)
}

// Test stubs and mocks for complex bot functionality tests

// stubGitHubClient implements a simple in-memory store for GitHub resources
// It behaves as a simple store of prepopulated resources provided by the caller,
// plus tracking functionality for modifications
type stubGitHubClient struct {
	// Predetermined resources
	issues      map[string]*github.Issue  // key: "owner/repo/number"
	users       map[string]*github.User   // key: login
	repos       map[string]*github.Repository // key: "owner/repo"
	refs        map[string]*github.Reference  // key: "owner/repo/ref"
	contents    map[string]*github.RepositoryContent // key: "owner/repo/path"
	comments    map[string][]*github.IssueComment // key: "owner/repo/number"
	reviews     map[string][]*github.PullRequestReview // key: "owner/repo/number"
	reactions   map[int64][]*github.Reaction // key: comment ID
	pullRequests map[string]*github.PullRequest // key: "owner/repo/number"

	// Error responses
	searchError       error
	getUserError      error
	getRepoError      error
	getRefError       error
	getContentsError  error
	listCommentsError error
	listReviewsError  error
	listReactionsError error

	// Track modifications for verification
	labelsAdded      []labelCall
	labelsRemoved    []labelCall
	commentsCreated  []commentCall
	reactionsCreated []reactionCall
	refsCreated      []refCall
}

type labelCall struct {
	owner  string
	repo   string
	number int
	label  string
}

type commentCall struct {
	owner   string
	repo    string
	number  int
	comment string
}

type refCall struct {
	owner string
	repo  string
	ref   *github.Reference
}

type reactionCall struct {
	commentID int64
	reaction  string
}

// newStubGitHubClient creates a new stub with empty stores
func newStubGitHubClient() *stubGitHubClient {
	return &stubGitHubClient{
		issues:       make(map[string]*github.Issue),
		users:        make(map[string]*github.User),
		repos:        make(map[string]*github.Repository),
		refs:         make(map[string]*github.Reference),
		contents:     make(map[string]*github.RepositoryContent),
		comments:     make(map[string][]*github.IssueComment),
		reviews:      make(map[string][]*github.PullRequestReview),
		reactions:    make(map[int64][]*github.Reaction),
		pullRequests: make(map[string]*github.PullRequest),
	}
}

func (s *stubGitHubClient) SearchIssues(ctx context.Context, query string, opts *github.SearchOptions) (*github.IssuesSearchResult, *github.Response, error) {
	if s.searchError != nil {
		return nil, nil, s.searchError
	}
	// Simple implementation - return all issues
	var issues []*github.Issue
	for _, issue := range s.issues {
		issues = append(issues, issue)
	}
	return &github.IssuesSearchResult{Issues: issues}, nil, nil
}

func (s *stubGitHubClient) GetRepository(ctx context.Context, owner, repo string) (*github.Repository, *github.Response, error) {
	if s.getRepoError != nil {
		return nil, nil, s.getRepoError
	}
	key := fmt.Sprintf("%s/%s", owner, repo)
	if r, ok := s.repos[key]; ok {
		return r, nil, nil
	}
	return nil, nil, fmt.Errorf("repository not found")
}

func (s *stubGitHubClient) GetUser(ctx context.Context, login string) (*github.User, *github.Response, error) {
	if s.getUserError != nil {
		return nil, nil, s.getUserError
	}
	if user, ok := s.users[login]; ok {
		return user, nil, nil
	}
	return nil, nil, fmt.Errorf("user not found")
}

func (s *stubGitHubClient) GetRef(ctx context.Context, owner, repo, ref string) (*github.Reference, *github.Response, error) {
	if s.getRefError != nil {
		return nil, nil, s.getRefError
	}
	key := fmt.Sprintf("%s/%s/%s", owner, repo, ref)
	if r, ok := s.refs[key]; ok {
		return r, nil, nil
	}
	return nil, nil, fmt.Errorf("ref not found")
}

func (s *stubGitHubClient) CreateRef(ctx context.Context, owner, repo string, ref *github.Reference) (*github.Reference, *github.Response, error) {
	s.refsCreated = append(s.refsCreated, refCall{owner: owner, repo: repo, ref: ref})
	return ref, nil, nil
}

func (s *stubGitHubClient) GetContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
	if s.getContentsError != nil {
		return nil, nil, nil, s.getContentsError
	}
	key := fmt.Sprintf("%s/%s/%s", owner, repo, path)
	if content, ok := s.contents[key]; ok {
		return &content, nil, nil, nil
	}
	return nil, nil, nil, fmt.Errorf("content not found")
}

func (s *stubGitHubClient) AddLabelsToIssue(ctx context.Context, owner, repo string, number int, labels []string) ([]*github.Label, *github.Response, error) {
	for _, label := range labels {
		s.labelsAdded = append(s.labelsAdded, labelCall{owner: owner, repo: repo, number: number, label: label})
	}
	return nil, nil, nil
}

func (s *stubGitHubClient) RemoveLabelForIssue(ctx context.Context, owner, repo string, number int, label string) (*github.Response, error) {
	s.labelsRemoved = append(s.labelsRemoved, labelCall{owner: owner, repo: repo, number: number, label: label})
	return nil, nil
}

func (s *stubGitHubClient) CreateComment(ctx context.Context, owner, repo string, number int, comment *github.IssueComment) (*github.IssueComment, *github.Response, error) {
	if comment.Body != nil {
		s.commentsCreated = append(s.commentsCreated, commentCall{owner: owner, repo: repo, number: number, comment: *comment.Body})
	}
	return comment, nil, nil
}

func (s *stubGitHubClient) GetIssue(ctx context.Context, owner, repo string, number int) (*github.Issue, *github.Response, error) {
	key := fmt.Sprintf("%s/%s/%d", owner, repo, number)
	if issue, ok := s.issues[key]; ok {
		return issue, nil, nil
	}
	return nil, nil, fmt.Errorf("issue not found")
}

func (s *stubGitHubClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (*github.PullRequest, *github.Response, error) {
	key := fmt.Sprintf("%s/%s/%d", owner, repo, number)
	if pr, ok := s.pullRequests[key]; ok {
		return pr, nil, nil
	}
	return nil, nil, fmt.Errorf("pull request not found")
}

func (s *stubGitHubClient) ListComments(ctx context.Context, owner, repo string, number int, opts *github.IssueListCommentsOptions) ([]*github.IssueComment, *github.Response, error) {
	if s.listCommentsError != nil {
		return nil, nil, s.listCommentsError
	}
	key := fmt.Sprintf("%s/%s/%d", owner, repo, number)
	if comments, ok := s.comments[key]; ok {
		return comments, &github.Response{NextPage: 0}, nil
	}
	return []*github.IssueComment{}, &github.Response{NextPage: 0}, nil
}

func (s *stubGitHubClient) ListReviews(ctx context.Context, owner, repo string, number int, opts *github.ListOptions) ([]*github.PullRequestReview, *github.Response, error) {
	if s.listReviewsError != nil {
		return nil, nil, s.listReviewsError
	}
	key := fmt.Sprintf("%s/%s/%d", owner, repo, number)
	if reviews, ok := s.reviews[key]; ok {
		return reviews, nil, nil
	}
	return []*github.PullRequestReview{}, nil, nil
}

func (s *stubGitHubClient) ListIssueCommentReactions(ctx context.Context, owner, repo string, id int64, opts *github.ListOptions) ([]*github.Reaction, *github.Response, error) {
	if s.listReactionsError != nil {
		return nil, nil, s.listReactionsError
	}
	if reactions, ok := s.reactions[id]; ok {
		return reactions, nil, nil
	}
	return []*github.Reaction{}, nil, nil
}

func (s *stubGitHubClient) ListPullRequestCommentReactions(ctx context.Context, owner, repo string, id int64, opts *github.ListOptions) ([]*github.Reaction, *github.Response, error) {
	if s.listReactionsError != nil {
		return nil, nil, s.listReactionsError
	}
	if reactions, ok := s.reactions[id]; ok {
		return reactions, nil, nil
	}
	return []*github.Reaction{}, nil, nil
}

func (s *stubGitHubClient) CreateIssueCommentReaction(ctx context.Context, owner, repo string, id int64, reaction string) (*github.Reaction, *github.Response, error) {
	s.reactionsCreated = append(s.reactionsCreated, reactionCall{commentID: id, reaction: reaction})
	return &github.Reaction{}, nil, nil
}

// Additional test stubs for more focused testing

// Interface definitions for testing - these would normally be in the main code

// FileSystemFactory interface for creating file systems
type FileSystemFactory interface {
	NewFileSystem(ctx context.Context, owner, repo, branch string) (*GitHubFileSystem, error)
}

// stubConversationHistoryStore implements ConversationHistoryStore for testing
type stubConversationHistoryStore struct {
	conversations map[string]*conversationHistory
	getError      error
	setError      error
	deleteError   error
}

func (s *stubConversationHistoryStore) Get(key string) (*conversationHistory, error) {
	if s.getError != nil {
		return nil, s.getError
	}
	return s.conversations[key], nil
}

func (s *stubConversationHistoryStore) Set(key string, value conversationHistory) error {
	if s.setError != nil {
		return s.setError
	}
	if s.conversations == nil {
		s.conversations = make(map[string]*conversationHistory)
	}
	s.conversations[key] = &value
	return nil
}

func (s *stubConversationHistoryStore) Delete(key string) error {
	if s.deleteError != nil {
		return s.deleteError
	}
	delete(s.conversations, key)
	return nil
}

// stubFileSystemFactory creates stubbed file systems
type stubFileSystemFactory struct {
	fileSystem *stubGitHubFileSystem
	error      error
}

func (f *stubFileSystemFactory) NewFileSystem(ctx context.Context, owner, repo, branch string) (*GitHubFileSystem, error) {
	if f.error != nil {
		return nil, f.error
	}
	// Return a stubbed filesystem instead of a real one for testing
	return &GitHubFileSystem{
		client:       nil, // We'll use the stub filesystem for testing
		owner:        owner,
		repo:         repo,
		branch:       branch,
		workingTree:  make(map[string]string),
		deletedFiles: make(map[string]bool),
	}, nil
}

// stubGitHubFileSystem provides a simple file system stub
type stubGitHubFileSystem struct {
	files       map[string]string
	hasChanges  bool
	commitError error
	prError     error
}

func (s *stubGitHubFileSystem) ReadFile(path string) (string, error) {
	content, exists := s.files[path]
	if !exists {
		return "", errors.New("file not found")
	}
	return content, nil
}

func (s *stubGitHubFileSystem) WriteFile(path, content string) error {
	if s.files == nil {
		s.files = make(map[string]string)
	}
	s.files[path] = content
	s.hasChanges = true
	return nil
}

func (s *stubGitHubFileSystem) HasChanges() bool {
	return s.hasChanges
}

func (s *stubGitHubFileSystem) CommitChanges(message string) (*github.Commit, error) {
	if s.commitError != nil {
		return nil, s.commitError
	}
	s.hasChanges = false
	return &github.Commit{}, nil
}

func (s *stubGitHubFileSystem) CreatePullRequest(title, body, targetBranch string) (*github.PullRequest, error) {
	return &github.PullRequest{}, s.prError
}

func (s *stubGitHubFileSystem) ClearChanges() {
	s.hasChanges = false
}

// stubToolRegistry provides a minimal tool registry for testing
type stubToolRegistry struct {
	toolResults       map[string]*string
	toolErrors        map[string]error
	replayError       error
	processedToolUses []anthropic.ToolUseBlock
	replayedToolUses  []anthropic.ToolUseBlock
}

func newStubToolRegistry() *stubToolRegistry {
	return &stubToolRegistry{
		toolResults: make(map[string]*string),
		toolErrors:  make(map[string]error),
	}
}

func (s *stubToolRegistry) GetAllToolParams() []anthropic.ToolParam {
	return []anthropic.ToolParam{
		{Name: "str_replace_based_edit_tool"},
		{Name: "commit_changes"},
		{Name: "create_pull_request"},
		{Name: "post_comment"},
		{Name: "add_reaction"},
		{Name: "request_review"},
	}
}

func (s *stubToolRegistry) ProcessToolUse(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) (*anthropic.ToolResultBlockParam, error) {
	s.processedToolUses = append(s.processedToolUses, block)
	
	if s.toolErrors != nil && s.toolErrors[block.Name] != nil {
		return nil, s.toolErrors[block.Name]
	}
	
	result := ""
	if s.toolResults != nil && s.toolResults[block.Name] != nil {
		result = *s.toolResults[block.Name]
	}
	
	return &anthropic.ToolResultBlockParam{
		ToolUseID: block.ID,
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: result}},
		},
		IsError: anthropic.Bool(false),
	}, nil
}

func (s *stubToolRegistry) ReplayToolUse(ctx context.Context, block anthropic.ToolUseBlock, toolCtx *ToolContext) error {
	s.replayedToolUses = append(s.replayedToolUses, block)
	return s.replayError
}

// Test helper functions

func createTestBot(t *testing.T) (*Bot, *stubGitHubClient, *stubConversationHistoryStore, *stubToolRegistry) {
	t.Helper()
	
	githubClient := newStubGitHubClient()
	conversationStore := &stubConversationHistoryStore{conversations: make(map[string]*conversationHistory)}
	toolRegistry := newStubToolRegistry()
	fileSystemFactory := &stubFileSystemFactory{}
	
	bot := &Bot{
		config: &Config{
			GitHubUsername: "test-bot",
		},
		githubClient:           nil, // We'll set this per test
		anthropicClient:        nil, // We'll set this per test
		toolRegistry:           toolRegistry,
		fileSystemFactory:      fileSystemFactory,
		resumableConversations: conversationStore,
		botName:                "test-bot",
	}
	
	return bot, githubClient, conversationStore, toolRegistry
}

func createTestIssue(number int, title string) *github.Issue {
	return &github.Issue{
		Number:        github.Int(number),
		Title:         github.String(title),
		RepositoryURL: github.String("https://api.github.com/repos/owner/repo"),
		URL:           github.String("https://github.com/owner/repo/issues/1"),
	}
}

func createTestUser(login string) *github.User {
	return &github.User{
		Login: github.String(login),
	}
}

func createTestRepository(defaultBranch string) *github.Repository {
	return &github.Repository{
		DefaultBranch: github.String(defaultBranch),
	}
}

// Test bot.needsAttention method
func testNeedsAttention(t *testing.T, task task, expected bool) {
	t.Helper()
	bot, _, _, _ := createTestBot(t)
	result := bot.needsAttention(task)
	require.Equal(t, expected, result)
}

func TestNeedsAttention_NewIssue(t *testing.T) {
	task := task{
		IssueComments: []*github.IssueComment{},
		PullRequest:   nil,
	}
	testNeedsAttention(t, task, true)
}

func TestNeedsAttention_IssueCommentsRequiringResponse(t *testing.T) {
	task := task{
		IssueComments:                      []*github.IssueComment{{}},
		PullRequest:                        nil,
		IssueCommentsRequiringResponses:    []*github.IssueComment{{}},
		PRCommentsRequiringResponses:       []*github.IssueComment{},
		PRReviewCommentsRequiringResponses: []*github.PullRequestComment{},
	}
	testNeedsAttention(t, task, true)
}

func TestNeedsAttention_PRCommentsRequiringResponse(t *testing.T) {
	task := task{
		IssueComments:                      []*github.IssueComment{{}},
		PullRequest:                        &github.PullRequest{},
		IssueCommentsRequiringResponses:    []*github.IssueComment{},
		PRCommentsRequiringResponses:       []*github.IssueComment{{}},
		PRReviewCommentsRequiringResponses: []*github.PullRequestComment{},
	}
	testNeedsAttention(t, task, true)
}

func TestNeedsAttention_PRReviewCommentsRequiringResponse(t *testing.T) {
	task := task{
		IssueComments:                      []*github.IssueComment{{}},
		PullRequest:                        &github.PullRequest{},
		IssueCommentsRequiringResponses:    []*github.IssueComment{},
		PRCommentsRequiringResponses:       []*github.IssueComment{},
		PRReviewCommentsRequiringResponses: []*github.PullRequestComment{{}},
	}
	testNeedsAttention(t, task, true)
}

func TestNeedsAttention_NoActionNeeded(t *testing.T) {
	task := task{
		IssueComments:                      []*github.IssueComment{{}},
		PullRequest:                        &github.PullRequest{},
		IssueCommentsRequiringResponses:    []*github.IssueComment{},
		PRCommentsRequiringResponses:       []*github.IssueComment{},
		PRReviewCommentsRequiringResponses: []*github.PullRequestComment{},
	}
	testNeedsAttention(t, task, false)
}

// Test conversation interruption and resumption scenarios

func TestInitConversation_ConversationStoreError(t *testing.T) {
	bot, _, conversationStore, _ := createTestBot(t)
	
	// Setup conversation store to return an error
	conversationStore.getError = errors.New("store error")
	
	task := task{
		Issue:      createTestIssue(1, "Test Issue"),
		Repository: createTestRepository("main"),
	}
	
	toolCtx := &ToolContext{
		Task: task,
	}
	
	// Test error handling when conversation store fails
	_, _, err := bot.initConversation(context.Background(), task, toolCtx)
	
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to look up resumable conversation")
}

// Tests for branch name handling

func TestGetWorkBranchName_WithTitle(t *testing.T) {
	issue := &github.Issue{
		Number: github.Int(123),
		Title:  github.String("Fix important bug in authentication"),
	}
	
	branchName := getWorkBranchName(issue)
	
	require.Equal(t, "fix/issue-123-fix-important-bug-in-authentication", branchName)
}

func TestGetWorkBranchName_WithoutTitle(t *testing.T) {
	issue := &github.Issue{
		Number: github.Int(456),
		Title:  nil,
	}
	
	branchName := getWorkBranchName(issue)
	
	require.Equal(t, "fix/issue-456", branchName)
}

func TestSanitizeForBranchName_BasicCases(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"Simple Title", "simple-title"},
		{"Title with CAPS", "title-with-caps"},
		{"Title_with_underscores", "title-with-underscores"},
		{"Title with special chars: []", "title-with-special-chars-"},
		{"Title/with/slashes", "title-with-slashes"},
		{"Title~with^invalid:chars", "title-with-invalid-chars"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := sanitizeForBranchName(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestNormalizeBranchName_LengthLimit(t *testing.T) {
	longName := "fix/issue-1-this-is-a-very-long-branch-name-that-exceeds-the-seventy-character-limit-and-should-be-truncated"
	
	result := normalizeBranchName(longName)
	
	require.LessOrEqual(t, len(result), 70)
	require.Equal(t, "fix/issue-1-this-is-a-very-long-branch-name-that-exceeds-the-seven", result)
}

func TestNormalizeBranchName_TrailingSeparators(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"branch-name-", "branch-name"},
		{"branch-name.", "branch-name"},
		{"branch-name-.", "branch-name"},
		{"-branch-name-", "branch-name"},
		{".branch-name.", "branch-name"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := normalizeBranchName(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// Tests for comment filtering logic

func TestIsBotComment_BotOwnComment(t *testing.T) {
	bot, _, _, _ := createTestBot(t)
	
	botUser := &github.User{Login: github.String("test-bot")}
	commentUser := &github.User{Login: github.String("test-bot")}
	
	result := bot.isBotComment(commentUser, botUser)
	
	require.True(t, result)
}

func TestIsBotComment_DifferentUser(t *testing.T) {
	bot, _, _, _ := createTestBot(t)
	
	botUser := &github.User{Login: github.String("test-bot")}
	commentUser := &github.User{Login: github.String("other-user")}
	
	result := bot.isBotComment(commentUser, botUser)
	
	require.False(t, result)
}

func TestIsBotComment_NilUsers(t *testing.T) {
	bot, _, _, _ := createTestBot(t)
	
	result := bot.isBotComment(nil, nil)
	
	require.False(t, result)
}

func TestIsBotComment_NilLogins(t *testing.T) {
	bot, _, _, _ := createTestBot(t)
	
	botUser := &github.User{Login: nil}
	commentUser := &github.User{Login: nil}
	
	result := bot.isBotComment(commentUser, botUser)
	
	require.False(t, result)
}

// Tests for resumable conversation functionality - conversation interruption and resumption

func TestRerunStatefulToolCalls_ReplaysPreviousToolUses(t *testing.T) {
	bot, _, _, toolRegistry := createTestBot(t)
	
	// Create a conversation with multiple tool calls in previous turns
	conversation := &ClaudeConversation{
		messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("First message")),
				Response: &anthropic.Message{
					Content: []anthropic.ContentBlock{
						{OfToolUse: anthropic.ToolUseBlock{
							ID:   "tool-1",
							Name: "str_replace_based_edit_tool",
						}},
						{OfToolUse: anthropic.ToolUseBlock{
							ID:   "tool-2", 
							Name: "commit_changes",
						}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Second message")),
				Response: &anthropic.Message{
					Content: []anthropic.ContentBlock{
						{OfToolUse: anthropic.ToolUseBlock{
							ID:   "tool-3",
							Name: "post_comment",
						}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Current message")),
				// No response yet - this is the current message being processed
			},
		},
	}
	
	toolCtx := &ToolContext{}
	
	err := bot.rerunStatefulToolCalls(context.Background(), toolCtx, conversation)
	
	require.NoError(t, err)
	// Verify that all previous tool uses were replayed, but not the current turn
	require.Len(t, toolRegistry.replayedToolUses, 3)
	require.Equal(t, "tool-1", toolRegistry.replayedToolUses[0].Name)
	require.Equal(t, "tool-2", toolRegistry.replayedToolUses[1].Name) 
	require.Equal(t, "tool-3", toolRegistry.replayedToolUses[2].Name)
}

func TestRerunStatefulToolCalls_ReplayError(t *testing.T) {
	bot, _, _, toolRegistry := createTestBot(t)
	
	// Setup tool registry to return an error during replay
	toolRegistry.replayError = errors.New("replay failed")
	
	conversation := &ClaudeConversation{
		messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Test message")),
				Response: &anthropic.Message{
					Content: []anthropic.ContentBlock{
						{OfToolUse: anthropic.ToolUseBlock{
							ID:   "tool-1",
							Name: "str_replace_based_edit_tool",
						}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Current message")),
			},
		},
	}
	
	toolCtx := &ToolContext{}
	
	err := bot.rerunStatefulToolCalls(context.Background(), toolCtx, conversation)
	
	require.Error(t, err)
	require.Contains(t, err.Error(), "replay failed")
}

// Tests for conversation history store integration

func TestConversationPersistenceForResumption(t *testing.T) {
	bot, _, conversationStore, _ := createTestBot(t)
	
	// Test the complete conversation persistence cycle that enables resumption
	originalHistory := conversationHistory{
		SystemPrompt: "Test system prompt",
		Messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Initial request")),
				Response: &anthropic.Message{
					StopReason: anthropic.StopReasonToolUse,
					Content: []anthropic.ContentBlock{
						{OfToolUse: anthropic.ToolUseBlock{ID: "tool-1", Name: "str_replace_based_edit_tool"}},
					},
				},
			},
		},
	}
	
	// Store conversation (simulating interruption)
	err := conversationStore.Set("123", originalHistory)
	require.NoError(t, err)
	
	// Later, retrieve for resumption  
	retrieved, err := conversationStore.Get("123")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	
	// Verify the conversation can be resumed with complete state
	require.Equal(t, originalHistory.SystemPrompt, retrieved.SystemPrompt)
	require.Len(t, retrieved.Messages, 1)
	require.NotNil(t, retrieved.Messages[0].Response)
	require.Equal(t, anthropic.StopReasonToolUse, retrieved.Messages[0].Response.StopReason)
	
	// Test completion cleanup - delete after successful completion
	err = conversationStore.Delete("123")
	require.NoError(t, err)
	
	// Verify conversation is cleaned up
	retrieved, err = conversationStore.Get("123")
	require.NoError(t, err)
	require.Nil(t, retrieved)
}

// Tests for multiple tool calls in one message - testing how the bot handles multiple tools

func TestProcessWithAI_MultipleToolCallsInOneMessage(t *testing.T) {
	bot, githubClient, conversationStore, toolRegistry := createTestBot(t)
	
	// Setup GitHub client with required data  
	githubClient.users["test-bot"] = createTestUser("test-bot")
	githubClient.repos["owner/repo"] = createTestRepository("main")
	githubClient.refs["owner/repo/refs/heads/main"] = &github.Reference{
		Object: &github.GitObject{SHA: github.String("abc123")},
	}
	
	// Create test task
	testTask := task{
		Issue:        createTestIssue(123, "Test issue"),
		Repository:   createTestRepository("main"),
		TargetBranch: "main",
		WorkBranch:   "fix/issue-123-test-issue",
		BotUsername:  "test-bot",
	}
	
	// Mock a conversation with multiple tool calls in previous turns
	conversationStore.conversations["123"] = &conversationHistory{
		SystemPrompt: "Test system prompt",
		Messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Please edit files and commit")),
				Response: &anthropic.Message{
					StopReason: anthropic.StopReasonToolUse,
					Content: []anthropic.ContentBlock{
						{OfText: anthropic.TextBlock{Text: "I'll help you with that."}},
						{OfToolUse: anthropic.ToolUseBlock{ID: "tool-1", Name: "str_replace_based_edit_tool"}},
						{OfToolUse: anthropic.ToolUseBlock{ID: "tool-2", Name: "commit_changes"}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(
					anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
						ToolUseID: "tool-1",
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{OfText: &anthropic.TextBlockParam{Text: "File edited"}},
						},
					}},
					anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
						ToolUseID: "tool-2",
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{OfText: &anthropic.TextBlockParam{Text: "Changes committed"}},
						},
					}},
				),
				Response: &anthropic.Message{
					StopReason: anthropic.StopReasonToolUse,
					Content: []anthropic.ContentBlock{
						{OfText: anthropic.TextBlock{Text: "Now I'll post a comment."}},
						{OfToolUse: anthropic.ToolUseBlock{ID: "tool-3", Name: "post_comment"}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(
					anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
						ToolUseID: "tool-3",
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{OfText: &anthropic.TextBlockParam{Text: "Comment posted"}},
						},
					}},
				),
				// No response - this is where conversation was interrupted
			},
		},
	}
	
	// Process with AI - this tests how multiple tool calls across turns are replayed
	err := bot.processWithAI(context.Background(), testTask, "owner", "repo")
	
	// Expect error due to missing Anthropic client, but verify replay logic worked
	require.Error(t, err)
	
	// Verify that all tools from previous turns were replayed, but not the current turn
	require.Len(t, toolRegistry.replayedToolUses, 3, "All tools from completed turns should be replayed") 
	require.Equal(t, "str_replace_based_edit_tool", toolRegistry.replayedToolUses[0].Name)
	require.Equal(t, "commit_changes", toolRegistry.replayedToolUses[1].Name)
	require.Equal(t, "post_comment", toolRegistry.replayedToolUses[2].Name)
}

func TestToolRegistry_ProcessesExpectedTools(t *testing.T) {
	_, _, _, toolRegistry := createTestBot(t)
	
	// Test that the tool registry can handle the expected tools
	expectedTools := []string{
		"str_replace_based_edit_tool",
		"commit_changes", 
		"create_pull_request",
		"post_comment",
		"add_reaction",
		"request_review",
	}
	
	toolCtx := &ToolContext{
		Task: task{Issue: createTestIssue(123, "Test issue")},
	}
	
	// Verify all expected tools can be processed
	for _, toolName := range expectedTools {
		toolUse := anthropic.ToolUseBlock{
			ID:   fmt.Sprintf("test-%s", toolName),
			Name: toolName,
		}
		
		result, err := toolRegistry.ProcessToolUse(context.Background(), toolUse, toolCtx)
		
		// We expect no error for tool processing (even if individual tools might have errors)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, toolUse.ID, result.ToolUseID)
	}
	
	// Verify all tools were tracked as processed
	require.Len(t, toolRegistry.processedToolUses, len(expectedTools))
	
	// Verify tool names match expectations
	for i, toolUse := range toolRegistry.processedToolUses {
		require.Equal(t, expectedTools[i], toolUse.Name) 
	}
}

// Test processIssue business logic - conversation interruption and resumption

func TestProcessIssue_ConversationResumption(t *testing.T) {
	bot, githubClient, conversationStore, toolRegistry := createTestBot(t)
	
	// Setup GitHub client with required data
	githubClient.users["test-bot"] = createTestUser("test-bot")
	githubClient.repos["owner/repo"] = createTestRepository("main")
	githubClient.refs["owner/repo/refs/heads/main"] = &github.Reference{
		Object: &github.GitObject{SHA: github.String("abc123")},
	}
	
	// Setup file system factory
	bot.fileSystemFactory = &stubFileSystemFactory{
		fileSystem: &stubGitHubFileSystem{},
	}
	
	// Create test issue
	issue := createTestIssue(1, "Test Issue")
	
	// Store a previous conversation that was interrupted
	conversationStore.conversations["1"] = &conversationHistory{
		SystemPrompt: "Test system prompt",
		Messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Original request")),
				Response: &anthropic.Message{
					StopReason: anthropic.StopReasonToolUse,
					Content: []anthropic.ContentBlock{
						{OfToolUse: anthropic.ToolUseBlock{ID: "tool-1", Name: "str_replace_based_edit_tool"}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(
					anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
						ToolUseID: "tool-1",
						Content: []anthropic.ToolResultBlockParamContentUnion{
							{OfText: &anthropic.TextBlockParam{Text: "File edited"}},
						},
					}},
				),
				// No response - this is where the conversation was interrupted
			},
		},
	}
	
	ctx := context.Background()
	
	// Process the issue - this should attempt to resume the conversation
	err := bot.processIssue(ctx, "owner", "repo", issue)
	
	// We expect an error due to missing Anthropic client, but verify resumption logic was triggered
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to process with AI")
	
	// Verify that the tool was replayed from the stored conversation
	require.Len(t, toolRegistry.replayedToolUses, 1)
	require.Equal(t, "str_replace_based_edit_tool", toolRegistry.replayedToolUses[0].Name)
}

// Tests for comment filtering logic - this tests non-trivial business logic

func TestPickIssueCommentsRequiringResponse_FiltersCorrectly(t *testing.T) {
	bot, githubClient, _, _ := createTestBot(t)
	
	botUser := createTestUser("test-bot")
	otherUser := createTestUser("other-user")
	
	// Setup comments with mixed ownership
	comments := []*github.IssueComment{
		{
			ID:   github.Int64(1),
			User: botUser, // Bot's own comment - should be filtered out
			Body: github.String("Bot comment"),
		},
		{
			ID:   github.Int64(2),
			User: otherUser, // Other user's comment, no bot reaction
			Body: github.String("User comment needing response"),
		},
		{
			ID:   github.Int64(3),
			User: otherUser, // Other user's comment, bot already reacted
			Body: github.String("User comment already handled"),
		},
	}
	
	// Setup reactions - bot reacted to comment 3 but not 2
	githubClient.reactions[3] = []*github.Reaction{
		{User: botUser}, // Bot reacted to comment 3
	}
	
	ctx := context.Background()
	result, err := bot.pickIssueCommentsRequiringResponse(ctx, "owner", "repo", comments, botUser)
	
	require.NoError(t, err)
	require.Len(t, result, 1, "Should return only comment 2 (not bot's own comment and not already reacted to)")
	require.Equal(t, int64(2), *result[0].ID)
}

// Tests for processIssue error handling and label management

func TestProcessIssue_ErrorHandling(t *testing.T) {
	bot, githubClient, _, _ := createTestBot(t)
	
	// Setup GitHub client to return an error when getting bot user
	githubClient.getUserError = errors.New("failed to get user")
	
	issue := createTestIssue(1, "Test Issue")
	
	err := bot.processIssue(context.Background(), "owner", "repo", issue)
	
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get bot user")
	
	// Verify that the working label would have been removed and blocked label added on error
	// (This tests the defer function's error handling logic)
	require.Eventually(t, func() bool {
		for _, call := range githubClient.labelsRemoved {
			if call.label == "bot-working" {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	
	require.Eventually(t, func() bool {
		for _, call := range githubClient.labelsAdded {
			if call.label == "bot-blocked" {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
}

// Tests for conversation interruption and clean conversation completion

func TestInitConversation_ResumesFromAssistantMessage(t *testing.T) {
	bot, _, conversationStore, _ := createTestBot(t)
	
	// Store a conversation that was interrupted after an assistant response
	conversationStore.conversations["123"] = &conversationHistory{
		SystemPrompt: "Test system prompt",
		Messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Please help me")),
				Response: &anthropic.Message{
					StopReason: anthropic.StopReasonEndTurn,
					Content: []anthropic.ContentBlock{
						{OfText: anthropic.TextBlock{Text: "I can help you with that."}},
					},
				},
			},
		},
	}
	
	testTask := task{
		Issue:      createTestIssue(123, "Test Issue"),
		Repository: createTestRepository("main"),
	}
	
	toolCtx := &ToolContext{
		Task: testTask,
	}
	
	// Try to initialize/resume the conversation
	_, response, err := bot.initConversation(context.Background(), testTask, toolCtx)
	
	// We expect this to fail due to missing Anthropic client
	// But we can verify the conversation resumption logic was attempted
	require.Error(t, err)
	require.Nil(t, response)
	require.Contains(t, err.Error(), "failed to resume conversation")
}

func TestInitConversation_ResumesFromUserMessage(t *testing.T) {
	bot, _, conversationStore, _ := createTestBot(t)
	
	// Store a conversation that was interrupted after a user message (no assistant response yet)
	conversationStore.conversations["123"] = &conversationHistory{
		SystemPrompt: "Test system prompt", 
		Messages: []conversationTurn{
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Initial request")),
				Response: &anthropic.Message{
					StopReason: anthropic.StopReasonEndTurn,
					Content: []anthropic.ContentBlock{
						{OfText: anthropic.TextBlock{Text: "Got it."}},
					},
				},
			},
			{
				UserMessage: anthropic.NewUserMessage(anthropic.NewTextBlock("Follow up request")),
				Response:    nil, // No response yet - conversation was interrupted here
			},
		},
	}
	
	testTask := task{
		Issue:      createTestIssue(123, "Test Issue"),
		Repository: createTestRepository("main"),
	}
	
	toolCtx := &ToolContext{
		Task: testTask,
	}
	
	// Try to initialize/resume the conversation
	_, response, err := bot.initConversation(context.Background(), testTask, toolCtx)
	
	// We expect this to fail due to missing Anthropic client
	require.Error(t, err)
	require.Nil(t, response)
}
