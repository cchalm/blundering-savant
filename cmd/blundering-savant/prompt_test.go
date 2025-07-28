package main

import (
	"fmt"
	"testing"

	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"
)

func TestBuildPrompt_BasicTemplate(t *testing.T) {
	// Create a minimal task for testing
	tsk := task{
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

	promptPtr, err := BuildPrompt(tsk)
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
	tsk := task{
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

	promptPtr, err := BuildPrompt(tsk)
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Pull Request #456 is open for this issue")
}

func TestBuildPrompt_WithStyleGuide(t *testing.T) {
	tsk := task{
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

	promptPtr, err := BuildPrompt(tsk)
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Style Guide:")
	require.Contains(t, prompt, "Use tabs for indentation")
}

func TestBuildPrompt_WithFileTree(t *testing.T) {
	tsk := task{
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

	promptPtr, err := BuildPrompt(tsk)
	require.NoError(t, err)
	prompt := *promptPtr

	require.Contains(t, prompt, "Repository structure (sample files):")
	require.Contains(t, prompt, "- main.go")
	require.Contains(t, prompt, "- README.md")
	require.Contains(t, prompt, "- go.mod")
}

func TestBuildPrompt_WithCommentsRequiringResponses(t *testing.T) {
	tsk := task{
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

	promptPtr, err := BuildPrompt(tsk)
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

	tsk := task{
		CodebaseInfo: &CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := buildTemplateData(tsk)

	require.Len(t, data.FileTree, 20)
	require.True(t, data.FileTreeTruncated)
}

func TestBuildTemplateData_DoesNotTruncateShortFileTree(t *testing.T) {
	fileTree := []string{"file1.go", "file2.go", "file3.go"}

	tsk := task{
		CodebaseInfo: &CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := buildTemplateData(tsk)

	require.Len(t, data.FileTree, 3)
	require.False(t, data.FileTreeTruncated)
}
