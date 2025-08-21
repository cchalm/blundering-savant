package git

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v72/github"
)

// githubGitRepo implements a handful of porcelain git commands using the GitHub API. It manipulates a remote git
// repository directly; e.g. commits appear on the remote without a push
type githubGitRepo struct {
	git          *github.GitService          // For low-level git operations
	reposService *github.RepositoriesService // For high-level operations supported by the github API

	owner string
	repo  string
}

// NewGithubGitRepo creates a new GitHub-backed Git repository
func NewGithubGitRepo(gitService *github.GitService, reposService *github.RepositoriesService, owner string, repo string) GitRepo {
	return &githubGitRepo{
		git:          gitService,
		reposService: reposService,
		owner:        owner,
		repo:         repo,
	}
}

// CreateBranch creates a new branch. If the branch already exists, CreateBranch does not return an error
func (ggr *githubGitRepo) CreateBranch(ctx context.Context, baseBranch string, newBranch string) error {
	// Check if branch already exists
	_, resp, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", newBranch))
	if err == nil {
		return nil
	} else if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unexpected error while checking if branch exists: %w", err)
	}

	// Get the base branch reference
	baseRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", baseBranch))
	if err != nil {
		return fmt.Errorf("failed to get base branch reference: %w", err)
	}

	// Create the new branch
	newRef := &github.Reference{
		Ref: github.Ptr(fmt.Sprintf("refs/heads/%s", newBranch)),
		Object: &github.GitObject{
			SHA: baseRef.Object.SHA,
		},
	}

	_, _, err = ggr.git.CreateRef(ctx, ggr.owner, ggr.repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch: %w", err)
	}

	return nil
}

// CommitChanges commits the given changelist to the specified branch
func (ggr *githubGitRepo) CommitChanges(ctx context.Context, branch string, changelist Changelist, commitMessage string) (*github.Commit, error) {
	if changelist.IsEmpty() {
		return nil, fmt.Errorf("changelist is empty")
	}

	// Get the current branch reference
	branchRef, _, err := ggr.git.GetRef(ctx, ggr.owner, ggr.repo, fmt.Sprintf("refs/heads/%s", branch))
	if err != nil {
		return nil, fmt.Errorf("failed to get branch reference: %w", err)
	}

	// Get the commit object that the branch currently points to
	currentCommit, _, err := ggr.git.GetCommit(ctx, ggr.owner, ggr.repo, branchRef.Object.GetSHA())
	if err != nil {
		return nil, fmt.Errorf("failed to get current commit: %w", err)
	}

	// Get the tree that the current commit points to
	baseTree, _, err := ggr.git.GetTree(ctx, ggr.owner, ggr.repo, currentCommit.Tree.GetSHA(), false)
	if err != nil {
		return nil, fmt.Errorf("failed to get base tree: %w", err)
	}

	// Create tree entries for all modified files
	var entries []*github.TreeEntry
	err = changelist.ForEachModified(func(path string, content string) error {
		blob, _, err := ggr.git.CreateBlob(ctx, ggr.owner, ggr.repo, &github.Blob{
			Content:  github.Ptr(content),
			Encoding: github.Ptr("utf-8"),
		})
		if err != nil {
			return fmt.Errorf("failed to create blob for %s: %w", path, err)
		}

		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"), // Regular file mode
			Type: github.Ptr("blob"),
			SHA:  blob.SHA,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Add entries for deleted files (with nil SHA to indicate deletion)
	err = changelist.ForEachDeleted(func(path string) error {
		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(path),
			Mode: github.Ptr("100644"),
			Type: github.Ptr("blob"),
			SHA:  nil, // nil SHA indicates deletion
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Create a new tree with our changes
	newTree, _, err := ggr.git.CreateTree(ctx, ggr.owner, ggr.repo, baseTree.GetSHA(), entries)
	if err != nil {
		return nil, fmt.Errorf("failed to create tree: %w", err)
	}

	// Create a new commit
	newCommit, _, err := ggr.git.CreateCommit(ctx, ggr.owner, ggr.repo, &github.Commit{
		Message: github.Ptr(commitMessage),
		Tree:    newTree,
		Parents: []*github.Commit{currentCommit},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	// Update the branch reference to point to the new commit
	_, _, err = ggr.git.UpdateRef(ctx, ggr.owner, ggr.repo, &github.Reference{
		Ref: github.Ptr(fmt.Sprintf("refs/heads/%s", branch)),
		Object: &github.GitObject{
			SHA: newCommit.SHA,
		},
	}, false)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch reference: %w", err)
	}

	return newCommit, nil
}

// Merge merges baseBranch into targetBranch
func (ggr *githubGitRepo) Merge(ctx context.Context, baseBranch string, targetBranch string) (*github.Commit, error) {
	merge, _, err := ggr.reposService.Merge(ctx, ggr.owner, ggr.repo, &github.RepositoryMergeRequest{
		Base:          github.Ptr(targetBranch),
		Head:          github.Ptr(baseBranch),
		CommitMessage: github.Ptr(fmt.Sprintf("Merge %s into %s", baseBranch, targetBranch)),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to merge branches: %w", err)
	}

	return merge.Commit, nil
}

// CompareCommits compares two commits and returns the diff
func (ggr *githubGitRepo) CompareCommits(ctx context.Context, base string, head string) (*github.CommitsComparison, error) {
	comparison, _, err := ggr.reposService.CompareCommits(ctx, ggr.owner, ggr.repo, base, head, &github.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}

	return comparison, nil
}