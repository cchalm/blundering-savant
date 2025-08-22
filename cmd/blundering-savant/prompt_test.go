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
		Issue: githubIssue{
			number: 123,
			title:  "Test Issue",
			body:   "This is a test issue description",
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	promptPtr, err := BuildPrompt(tsk)
	require.NoError(t, err)
	prompt := *promptPtr

	// Verify the template was executed and contains expected content
	// Repository-specific content should NOT be in task-specific prompt
	require.NotContains(t, prompt, "Repository: owner/repo")
	require.NotContains(t, prompt, "Main Language: Go")
	require.Contains(t, prompt, "Issue #123: Test Issue")
	require.Contains(t, prompt, "This is a test issue description")
	require.Contains(t, prompt, "## Your Task")
}

func TestBuildPrompt_WithPullRequest(t *testing.T) {
	tsk := task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: githubIssue{
			number: 123,
			title:  "Test Issue",
			body:   "This is a test issue description",
		},
		PullRequest: &githubPullRequest{
			number: 456,
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
		Issue: githubIssue{
			number: 123,
			title:  "Test Issue",
			body:   "Test description",
		},
		StyleGuide: &StyleGuide{
			Guides: map[string]string{
				"style_guide.md": "Use tabs for indentation",
			},
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	promptPtr, err := BuildPrompt(tsk)
	require.NoError(t, err)
	prompt := *promptPtr

	// Style guides should NOT be in task-specific prompt
	require.NotContains(t, prompt, "## Style Guides")
	require.NotContains(t, prompt, "style_guide.md")
	require.NotContains(t, prompt, "Use tabs for indentation")
}

func TestBuildPrompt_WithFileTree(t *testing.T) {
	tsk := task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: githubIssue{
			number: 123,
			title:  "Test Issue",
			body:   "Test description",
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage: "Go",
			FileTree:     []string{"main.go", "README.md", "go.mod"},
		},
	}

	promptPtr, err := BuildPrompt(tsk)
	require.NoError(t, err)
	prompt := *promptPtr

	// File tree should NOT be in task-specific prompt
	require.NotContains(t, prompt, "## Repository structure")
	require.NotContains(t, prompt, "- `main.go`")
	require.NotContains(t, prompt, "- `README.md`")
	require.NotContains(t, prompt, "- `go.mod`")
}

func TestBuildPrompt_WithCommentsRequiringResponses(t *testing.T) {
	tsk := task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: githubIssue{
			number: 123,
			title:  "Test Issue",
			body:   "Test description",
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

func TestBuildRepositoryTemplateData_TruncatesLongFileTree(t *testing.T) {
	// Create a file tree with more than 1000 files
	count := 1015
	fileTree := make([]string, count)
	for i := range count {
		fileTree[i] = fmt.Sprintf("file%d.go", i)
	}

	tsk := task{
		CodebaseInfo: &CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := buildRepositoryTemplateData(tsk)

	require.Len(t, data.FileTree, 1000)
	require.Equal(t, data.FileTreeTruncatedCount, 15)
}

func TestBuildRepositoryTemplateData_DoesNotTruncateShortFileTree(t *testing.T) {
	fileTree := []string{"file1.go", "file2.go", "file3.go"}

	tsk := task{
		CodebaseInfo: &CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := buildRepositoryTemplateData(tsk)

	require.Len(t, data.FileTree, 3)
	require.Equal(t, data.FileTreeTruncatedCount, 0)
}

func TestBuildRepositoryInfo_BasicTemplate(t *testing.T) {
	// Create a comprehensive task for testing
	tsk := task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		CodebaseInfo: &CodebaseInfo{
			MainLanguage:  "Go",
			ReadmeContent: "This is a test README",
			FileTree:      []string{"main.go", "README.md", "go.mod"},
		},
		StyleGuide: &StyleGuide{
			Guides: map[string]string{
				"STYLE_GUIDE.md": "Use tabs for indentation",
			},
		},
	}

	repoInfoPtr, err := BuildRepositoryInfo(tsk)
	require.NoError(t, err)
	repoInfo := *repoInfoPtr

	// Verify repository-specific content is included
	require.Contains(t, repoInfo, "Repository: owner/repo")
	require.Contains(t, repoInfo, "Main Language: Go")
	require.Contains(t, repoInfo, "## Style Guides")
	require.Contains(t, repoInfo, "STYLE_GUIDE.md")
	require.Contains(t, repoInfo, "Use tabs for indentation")
	require.Contains(t, repoInfo, "## README excerpt")
	require.Contains(t, repoInfo, "This is a test README")
	require.Contains(t, repoInfo, "## Repository structure")
	require.Contains(t, repoInfo, "- `main.go`")
	require.Contains(t, repoInfo, "- `README.md`")
	require.Contains(t, repoInfo, "- `go.mod`")
}

func TestBuildSystemTemplate(t *testing.T) {
	s, err := BuildSystemPrompt("Steve", "steve-the-dude")
	require.NoError(t, err)
	require.Contains(t, s, "Steve")
	require.Contains(t, s, "steve-the-dude")
}
