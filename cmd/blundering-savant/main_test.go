package main

import (
	"fmt"
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

func TestBuildPrompt_BasicTemplate(t *testing.T) {
	// Create a minimal workContext for testing
	ctx := workContext{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: &github.Issue{
			Number: github.Ptr(123),
			Title:  github.Ptr("Test Issue"),
			Body:   github.Ptr("This is a test issue description"),
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	promptPtr, err := ctx.BuildPrompt()
	require.NoError(t, err)
	prompt := *promptPtr

	// Verify the template was executed and contains expected content
	require.Contains(t, prompt, "Repository: owner/repo")
	require.Contains(t, prompt, "Main Language: Go")
	require.Contains(t, prompt, "Issue #123: Test Issue")
	require.Contains(t, prompt, "This is a test issue description")
	require.Contains(t, prompt, "## Your Task")
}

func TestBuildPrompt_WithPullRequest(t *testing.T) {
	ctx := workContext{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: &github.Issue{
			Number: github.Ptr(123),
			Title:  github.Ptr("Test Issue"),
			Body:   github.Ptr("This is a test issue description"),
		},
		PullRequest: &github.PullRequest{
			Number: github.Ptr(456),
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	promptPtr, err := ctx.BuildPrompt()
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Pull Request #456 is open for this issue")
}

func TestBuildPrompt_WithStyleGuide(t *testing.T) {
	ctx := workContext{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: &github.Issue{
			Number: github.Ptr(123),
			Title:  github.Ptr("Test Issue"),
			Body:   github.Ptr("Test description"),
		},
		StyleGuide: &StyleGuide{
			Content: "Use tabs for indentation",
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	promptPtr, err := ctx.BuildPrompt()
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Style Guide:")
	require.Contains(t, prompt, "Use tabs for indentation")
}

func TestBuildPrompt_WithFileTree(t *testing.T) {
	ctx := workContext{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: &github.Issue{
			Number: github.Ptr(123),
			Title:  github.Ptr("Test Issue"),
			Body:   github.Ptr("Test description"),
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
			FileTree:     []string{"main.go", "README.md", "go.mod"},
		},
	}

	promptPtr, err := ctx.BuildPrompt()
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Repository structure (sample files):")
	require.Contains(t, prompt, "- main.go")
	require.Contains(t, prompt, "- README.md")
	require.Contains(t, prompt, "- go.mod")
}

func TestBuildPrompt_WithCommentsRequiringResponses(t *testing.T) {
	ctx := workContext{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: &github.Issue{
			Number: github.Ptr(123),
			Title:  github.Ptr("Test Issue"),
			Body:   github.Ptr("Test description"),
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
		},
		IssueCommentsRequiringResponses: []*github.IssueComment{
			{ID: github.Ptr(int64(1001))},
			{ID: github.Ptr(int64(1002))},
		},
		PRCommentsRequiringResponses: []*github.IssueComment{
			{ID: github.Ptr(int64(2001))},
		},
	}

	promptPtr, err := ctx.BuildPrompt()
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Issue comments requiring responses: 1001, 1002")
	require.Contains(t, prompt, "PR comments requiring responses: 2001")
}

func TestBuildTemplateData_TruncatesLongFileTree(t *testing.T) {
	// Create a file tree with more than 20 files
	fileTree := make([]string, 25)
	for i := 0; i < 25; i++ {
		fileTree[i] = fmt.Sprintf("file%d.go", i)
	}

	ctx := workContext{
		CodebaseInfo: &CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := ctx.buildTemplateData()

	require.Len(t, data.FileTree, 20)
	require.True(t, data.FileTreeTruncated)
}

func TestBuildTemplateData_DoesNotTruncateShortFileTree(t *testing.T) {
	fileTree := []string{"file1.go", "file2.go", "file3.go"}

	ctx := workContext{
		CodebaseInfo: &CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := ctx.buildTemplateData()

	require.Len(t, data.FileTree, 3)
	require.False(t, data.FileTreeTruncated)
}
