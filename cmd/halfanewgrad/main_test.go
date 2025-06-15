package main

import (
	"testing"

	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"
)

// Helper function to create a comment with given ID and InReplyTo
func createComment(id int64, inReplyTo *int64) *github.PullRequestComment {
	return &github.PullRequestComment{
		ID:        &id,
		InReplyTo: inReplyTo,
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

func TestOrganizePRReviewCommentsIntoThreads_LongThread(t *testing.T) {
	// Chain: 1 -> 2 -> 3 -> 4
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		createComment(2, int64Ptr(1)),
		createComment(3, int64Ptr(2)),
		createComment(4, int64Ptr(3)),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0], 4)
	for i := 0; i < 4; i++ {
		require.Equal(t, int64(i+1), *threads[0][i].ID)
	}
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

func TestOrganizePRReviewCommentsIntoThreads_OutOfOrderComments(t *testing.T) {
	// Thread 1->2->3 but comments given out of order
	comments := []*github.PullRequestComment{
		createComment(3, int64Ptr(2)),
		createComment(1, nil),
		createComment(2, int64Ptr(1)),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0], 3)
	require.Equal(t, int64(1), *threads[0][0].ID)
	require.Equal(t, int64(2), *threads[0][1].ID)
	require.Equal(t, int64(3), *threads[0][2].ID)
}

func TestOrganizePRReviewCommentsIntoThreads_ComplexMerge(t *testing.T) {
	// Test the complex case where a comment connects two separate parts
	// This tests the okHead && okTail case
	// Create scenario: comment 2 replies to 1, comment 4 replies to 3, comment 5 replies to 2
	// This should create thread: 1->2->5 and 3->4 initially, then when processed in right order
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		createComment(3, nil),
		createComment(2, int64Ptr(1)),
		createComment(4, int64Ptr(3)),
		createComment(5, int64Ptr(2)), // This extends the 1->2 thread
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 2)

	// Find the longer thread (should be 1->2->5)
	var longThread, shortThread []*github.PullRequestComment
	if len(threads[0]) > len(threads[1]) {
		longThread = threads[0]
		shortThread = threads[1]
	} else {
		longThread = threads[1]
		shortThread = threads[0]
	}

	require.Len(t, longThread, 3)
	require.Equal(t, int64(1), *longThread[0].ID)
	require.Equal(t, int64(2), *longThread[1].ID)
	require.Equal(t, int64(5), *longThread[2].ID)

	require.Len(t, shortThread, 2)
	require.Equal(t, int64(3), *shortThread[0].ID)
	require.Equal(t, int64(4), *shortThread[1].ID)
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

func TestOrganizePRReviewCommentsIntoThreads_ReplyToUnknownComment(t *testing.T) {
	// Comment 2 replies to comment 999 which doesn't exist
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		createComment(2, int64Ptr(999)), // 999 doesn't exist
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.Error(t, err)
	require.Contains(t, err.Error(), "found comments that reply to unknown comments")
	require.Nil(t, threads)
}

func TestOrganizePRReviewCommentsIntoThreads_ThreadHeadInsertion(t *testing.T) {
	// Test the specific case where okHead is true but okTail is false
	// This happens when we have a comment that becomes the head of an existing thread
	// but doesn't reply to the tail of another thread

	// Process in this order: comment 2 replies to 1, then comment 3 replies to 2,
	// then insert comment 0 which comment 1 replies to
	comments := []*github.PullRequestComment{
		createComment(2, int64Ptr(1)),
		createComment(3, int64Ptr(2)),
		createComment(1, int64Ptr(0)), // This will be the head when 0 is added
		createComment(0, nil),         // This will become new head of thread 1->2->3
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.Len(t, threads[0], 4)
	require.Equal(t, int64(0), *threads[0][0].ID)
	require.Equal(t, int64(1), *threads[0][1].ID)
	require.Equal(t, int64(2), *threads[0][2].ID)
	require.Equal(t, int64(3), *threads[0][3].ID)
}

func TestOrganizePRReviewCommentsIntoThreads_PreservesCommentCount(t *testing.T) {
	// Test that the sanity check works correctly
	comments := []*github.PullRequestComment{
		createComment(1, nil),
		createComment(2, int64Ptr(1)),
		createComment(3, nil),
		createComment(4, int64Ptr(3)),
		createComment(5, int64Ptr(4)),
	}

	threads, err := organizePRReviewCommentsIntoThreads(comments)

	require.NoError(t, err)

	// Count total comments in all threads
	totalComments := 0
	for _, thread := range threads {
		totalComments += len(thread)
	}

	require.Equal(t, len(comments), totalComments)
}
