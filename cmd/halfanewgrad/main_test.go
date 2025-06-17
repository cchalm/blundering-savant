package main

import (
	"testing"
	"time"

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

func TestMemoryTimestampCache_GetTimestamps_NotFound(t *testing.T) {
	cache := NewMemoryTimestampCache()
	
	issueUpdatedAt, prUpdatedAt, found := cache.GetTimestamps("owner", "repo", 123)
	
	require.False(t, found)
	require.Nil(t, issueUpdatedAt)
	require.Nil(t, prUpdatedAt)
}

func TestMemoryTimestampCache_SetAndGetTimestamps(t *testing.T) {
	cache := NewMemoryTimestampCache()
	
	issueTime := time.Now()
	prTime := time.Now().Add(1 * time.Hour)
	
	// Set timestamps
	cache.SetTimestamps("owner", "repo", 123, &issueTime, &prTime)
	
	// Get timestamps
	issueUpdatedAt, prUpdatedAt, found := cache.GetTimestamps("owner", "repo", 123)
	
	require.True(t, found)
	require.NotNil(t, issueUpdatedAt)
	require.NotNil(t, prUpdatedAt)
	require.True(t, issueTime.Equal(*issueUpdatedAt))
	require.True(t, prTime.Equal(*prUpdatedAt))
}

func TestMemoryTimestampCache_SetAndGetTimestamps_NilPR(t *testing.T) {
	cache := NewMemoryTimestampCache()
	
	issueTime := time.Now()
	
	// Set timestamps with nil PR
	cache.SetTimestamps("owner", "repo", 123, &issueTime, nil)
	
	// Get timestamps
	issueUpdatedAt, prUpdatedAt, found := cache.GetTimestamps("owner", "repo", 123)
	
	require.True(t, found)
	require.NotNil(t, issueUpdatedAt)
	require.Nil(t, prUpdatedAt)
	require.True(t, issueTime.Equal(*issueUpdatedAt))
}

func TestMemoryTimestampCache_UpdateExisting(t *testing.T) {
	cache := NewMemoryTimestampCache()
	
	// Set initial timestamps
	issueTime1 := time.Now()
	prTime1 := time.Now().Add(1 * time.Hour)
	cache.SetTimestamps("owner", "repo", 123, &issueTime1, &prTime1)
	
	// Update timestamps
	issueTime2 := time.Now().Add(2 * time.Hour)
	prTime2 := time.Now().Add(3 * time.Hour)
	cache.SetTimestamps("owner", "repo", 123, &issueTime2, &prTime2)
	
	// Get updated timestamps
	issueUpdatedAt, prUpdatedAt, found := cache.GetTimestamps("owner", "repo", 123)
	
	require.True(t, found)
	require.NotNil(t, issueUpdatedAt)
	require.NotNil(t, prUpdatedAt)
	require.True(t, issueTime2.Equal(*issueUpdatedAt))
	require.True(t, prTime2.Equal(*prUpdatedAt))
	require.False(t, issueTime1.Equal(*issueUpdatedAt))
	require.False(t, prTime1.Equal(*prUpdatedAt))
}

func TestMemoryTimestampCache_DifferentIssues(t *testing.T) {
	cache := NewMemoryTimestampCache()
	
	issueTime1 := time.Now()
	issueTime2 := time.Now().Add(1 * time.Hour)
	
	// Set timestamps for different issues
	cache.SetTimestamps("owner", "repo", 123, &issueTime1, nil)
	cache.SetTimestamps("owner", "repo", 456, &issueTime2, nil)
	
	// Get timestamps for first issue
	issueUpdatedAt1, prUpdatedAt1, found1 := cache.GetTimestamps("owner", "repo", 123)
	require.True(t, found1)
	require.True(t, issueTime1.Equal(*issueUpdatedAt1))
	require.Nil(t, prUpdatedAt1)
	
	// Get timestamps for second issue
	issueUpdatedAt2, prUpdatedAt2, found2 := cache.GetTimestamps("owner", "repo", 456)
	require.True(t, found2)
	require.True(t, issueTime2.Equal(*issueUpdatedAt2))
	require.Nil(t, prUpdatedAt2)
	
	// Verify they are different
	require.False(t, issueTime1.Equal(*issueUpdatedAt2))
}

func TestMemoryTimestampCache_DifferentRepos(t *testing.T) {
	cache := NewMemoryTimestampCache()
	
	issueTime := time.Now()
	
	// Set timestamps for same issue number in different repos
	cache.SetTimestamps("owner1", "repo1", 123, &issueTime, nil)
	cache.SetTimestamps("owner2", "repo2", 123, &issueTime, nil)
	
	// Both should be found independently
	_, _, found1 := cache.GetTimestamps("owner1", "repo1", 123)
	_, _, found2 := cache.GetTimestamps("owner2", "repo2", 123)
	_, _, found3 := cache.GetTimestamps("owner1", "repo2", 123)
	
	require.True(t, found1)
	require.True(t, found2)
	require.False(t, found3) // Different combination should not be found
}
