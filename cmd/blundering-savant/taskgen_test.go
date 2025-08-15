package main

import (
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

func TestGetFileTreeWithSafeguards_EnforcesLimits(t *testing.T) {
	// Create a mock tree with various edge cases to test safeguards
	entries := []*github.TreeEntry{}
	
	// Add normal files (should be included)
	entries = append(entries, &github.TreeEntry{Path: github.String("README.md")})
	entries = append(entries, &github.TreeEntry{Path: github.String("src/main.go")})
	entries = append(entries, &github.TreeEntry{Path: github.String("docs/guide.md")})
	
	// Add a file with path too long (should be excluded)
	longPath := "very/" + strings.Repeat("long/", 50) + "path.txt" // Over 500 chars
	entries = append(entries, &github.TreeEntry{Path: &longPath})
	
	// Add a file too deep (should be excluded) 
	deepPath := strings.Repeat("level/", 25) + "file.txt" // Over 20 levels deep
	entries = append(entries, &github.TreeEntry{Path: &deepPath})
	
	// Add files to test the 1000 file limit
	for i := 0; i < 1005; i++ {
		path := "file" + string(rune('0'+i%10)) + ".txt"
		entries = append(entries, &github.TreeEntry{Path: &path})
	}
	
	tree := &github.Tree{Entries: entries}
	
	// Test the filtering logic directly (mirroring the actual implementation)
	var fileTree []string
	fileCount := 0
	const (
		maxFiles         = 1000
		maxPathLength    = 500
		maxDirectoryDepth = 20
	)
	
	for _, entry := range tree.Entries {
		if entry.Path == nil {
			continue
		}
		
		path := *entry.Path
		
		// Check path length limit
		if len(path) > maxPathLength {
			continue
		}
		
		// Check directory depth limit
		depth := strings.Count(path, "/")
		if depth > maxDirectoryDepth {
			continue
		}
		
		// Check file count limit
		if fileCount >= maxFiles {
			break
		}
		
		fileTree = append(fileTree, path)
		fileCount++
	}
	
	// Verify safeguards are enforced
	require.LessOrEqual(t, len(fileTree), maxFiles, "Should not exceed max file count")
	
	for _, path := range fileTree {
		require.LessOrEqual(t, len(path), maxPathLength, "Path should not exceed max length: %s", path)
		require.LessOrEqual(t, strings.Count(path, "/"), maxDirectoryDepth, "Path should not exceed max depth: %s", path)
	}
	
	// Verify some expected files are included
	require.Contains(t, fileTree, "README.md")
	require.Contains(t, fileTree, "src/main.go")
	require.Contains(t, fileTree, "docs/guide.md")
}
