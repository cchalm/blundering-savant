package task

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/go-github/v72/github"
)

type TaskOrError struct {
	Task Task
	Err  error
}

type generator struct {
	checkInterval time.Duration
	githubClient  *github.Client
	githubUser    *github.User

	builder builder
}

func NewGenerator(githubClient *github.Client, githubUser *github.User, checkInterval time.Duration) *generator {
	return &generator{
		checkInterval: checkInterval,
		githubClient:  githubClient,
		githubUser:    githubUser,

		builder: NewBuilder(githubClient, githubUser),
	}
}

func (tg *generator) Generate(ctx context.Context) chan TaskOrError {
	tasks := make(chan TaskOrError)

	go func() {
		defer close(tasks)
		for {
			tg.yield(ctx, func(task Task, err error) {
				tasks <- TaskOrError{Task: task, Err: err}
			})
		}
	}()

	return tasks
}

func (tg *generator) yield(ctx context.Context, yield func(task Task, err error)) {
	ticker := time.Tick(tg.checkInterval)
	for {
		issues, err := tg.searchIssues(ctx)
		if err != nil {
			return
		}
		if len(issues) == 0 {
			log.Println("[taskgen] No issues found")
		}

		for _, issue := range issues {
			tsk, err := tg.builder.buildTaskFromIssue(ctx, issue)
			if err != nil {
				yield(Task{}, fmt.Errorf("failed to build task for issue %d: %w", issue.Number, err))
			}

			if tg.builder.NeedsAttention(*tsk) {
				log.Printf("[taskgen] Yielding task for issue #%d in %s/%s", issue.Number, issue.Owner, issue.Repo)
				yield(*tsk, nil)
			} else {
				log.Printf("[taskgen] Skipping issue #%d in %s/%s: no attention needed", issue.Number, issue.Owner, issue.Repo)
			}
		}

		log.Printf("[taskgen] Waiting for next check (up to %v)\n", tg.checkInterval)
		select {
		case <-ticker:
		case <-ctx.Done():
			yield(Task{}, ctx.Err())
			return
		}
	}
}

func (tg *generator) searchIssues(ctx context.Context) ([]GithubIssue, error) {
	// Search for issues assigned to the bot that are not being worked on and are not blocked
	query := fmt.Sprintf("assignee:%s is:issue is:open -label:%s -label:%s", *tg.githubUser.Login, *LabelWorking.Name, *LabelBlocked.Name)
	result, _, err := tg.githubClient.Search.Issues(ctx, query, nil)
	if err != nil {
		return nil, fmt.Errorf("error searching issues: %w", err)
	}

	// Convert issue response into simpler structures
	issues := []GithubIssue{}
	for _, issue := range result.Issues {
		converted, err := convertIssue(issue)
		if err != nil {
			log.Printf("[taskgen] Warning: skipping issue: %v", err)
		}

		issues = append(issues, converted)
	}

	return issues, nil
}
