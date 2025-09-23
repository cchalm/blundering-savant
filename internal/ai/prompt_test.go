package ai

import (
	"fmt"
	"testing"

	"github.com/google/go-github/v72/github"
	"github.com/stretchr/testify/require"

	"github.com/cchalm/blundering-savant/internal/task"
)

func TestBuildPrompt_BasicTemplate(t *testing.T) {
	// Create a minimal task for testing
	tsk := task.Task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: task.GithubIssue{
			Number: 123,
			Title:  "Test Issue",
			Body:   "This is a test issue description",
		},
		CodebaseInfo: &task.CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	repositoryContent, taskContent, err := BuildPrompt(tsk)
	require.NoError(t, err)

	// Verify repository content contains repository-specific information
	require.Contains(t, repositoryContent, "Repository: owner/repo")
	require.Contains(t, repositoryContent, "Main Language: Go")

	// Verify task content contains task-specific information
	require.Contains(t, taskContent, "Issue #123: Test Issue")
	require.Contains(t, taskContent, "This is a test issue description")
	require.Contains(t, taskContent, "## Your Task")

	// Verify separation: repository info should not be in task content
	require.NotContains(t, taskContent, "Repository: owner/repo")
	require.NotContains(t, taskContent, "Main Language: Go")

	// Verify separation: task info should not be in repository content
	require.NotContains(t, repositoryContent, "Issue #123: Test Issue")
	require.NotContains(t, repositoryContent, "## Your Task")
}

func TestBuildPrompt_WithPullRequest(t *testing.T) {
	tsk := task.Task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: task.GithubIssue{
			Number: 123,
			Title:  "Test Issue",
			Body:   "This is a test issue description",
		},
		PullRequest: &task.GithubPullRequest{
			Owner:  "cchalm",
			Repo:   "blundering-savant",
			Number: 456,
		},
		CodebaseInfo: &task.CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	repositoryContent, taskContent, err := BuildPrompt(tsk)
	require.NoError(t, err)

	// PR information should be in task content, not repository content
	require.Contains(t, taskContent, "User cchalm has opened pull request #456 for this issue.")
	require.NotContains(t, repositoryContent, "pull request #456")
}

func TestBuildPrompt_WithStyleGuide(t *testing.T) {
	tsk := task.Task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: task.GithubIssue{
			Number: 123,
			Title:  "Test Issue",
			Body:   "Test description",
		},
		StyleGuide: &task.StyleGuide{
			Guides: map[string]string{
				"style_guide.md": "Use tabs for indentation",
			},
		},
		CodebaseInfo: &task.CodebaseInfo{
			MainLanguage: "Go",
		},
	}

	repositoryContent, taskContent, err := BuildPrompt(tsk)
	require.NoError(t, err)

	// Style guides should be in repository content, not task content
	require.Contains(t, repositoryContent, "## Style Guides")
	require.Contains(t, repositoryContent, "style_guide.md")
	require.Contains(t, repositoryContent, "Use tabs for indentation")

	// Verify style guides are not in task content
	require.NotContains(t, taskContent, "## Style Guides")
	require.NotContains(t, taskContent, "Use tabs for indentation")
}

func TestBuildPrompt_WithFileTree(t *testing.T) {
	tsk := task.Task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: task.GithubIssue{
			Number: 123,
			Title:  "Test Issue",
			Body:   "Test description",
		},
		CodebaseInfo: &task.CodebaseInfo{
			MainLanguage: "Go",
			FileTree:     []string{"main.go", "README.md", "go.mod"},
		},
	}

	repositoryContent, taskContent, err := BuildPrompt(tsk)
	require.NoError(t, err)

	// File tree should be in repository content, not task content
	require.Contains(t, repositoryContent, "## Repository structure")
	require.Contains(t, repositoryContent, "- `main.go`")
	require.Contains(t, repositoryContent, "- `README.md`")
	require.Contains(t, repositoryContent, "- `go.mod`")

	// Verify file tree is not in task content
	require.NotContains(t, taskContent, "## Repository structure")
	require.NotContains(t, taskContent, "- `main.go`")
}

func TestBuildPrompt_WithCommentsRequiringResponses(t *testing.T) {
	tsk := task.Task{
		Repository: &github.Repository{
			FullName: github.Ptr("owner/repo"),
		},
		Issue: task.GithubIssue{
			Number: 123,
			Title:  "Test Issue",
			Body:   "Test description",
		},
		CodebaseInfo: &task.CodebaseInfo{
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

	repositoryContent, taskContent, err := BuildPrompt(tsk)
	require.NoError(t, err)

	// Comments requiring responses should be in task content, not repository content
	require.Contains(t, taskContent, "Issue comments requiring responses: 1001, 1002")
	require.Contains(t, taskContent, "PR comments requiring responses: 2001")

	require.NotContains(t, repositoryContent, "Issue comments requiring responses: 1001, 1002")
	require.NotContains(t, repositoryContent, "PR comments requiring responses: 2001")
}

func TestBuildTemplateData_TruncatesLongFileTree(t *testing.T) {
	// Create a file tree with more than 1000 files
	count := 1015
	fileTree := make([]string, count)
	for i := range count {
		fileTree[i] = fmt.Sprintf("file%d.go", i)
	}

	tsk := task.Task{
		CodebaseInfo: &task.CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := buildTemplateData(tsk)

	require.Len(t, data.FileTree, 1000)
	require.Equal(t, data.FileTreeTruncatedCount, 15)
}

func TestBuildTemplateData_DoesNotTruncateShortFileTree(t *testing.T) {
	fileTree := []string{"file1.go", "file2.go", "file3.go"}

	tsk := task.Task{
		CodebaseInfo: &task.CodebaseInfo{
			FileTree: fileTree,
		},
	}

	data := buildTemplateData(tsk)

	require.Len(t, data.FileTree, 3)
	require.Equal(t, data.FileTreeTruncatedCount, 0)
}

func TestBuildSystemTemplate(t *testing.T) {
	s, err := BuildSystemPrompt("Steve", "steve-the-dude")
	require.NoError(t, err)
	require.Contains(t, s, "Steve")
	require.Contains(t, s, "steve-the-dude")
}
