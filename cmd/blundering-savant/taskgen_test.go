package main

import (
	"fmt"
	"strings"
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


