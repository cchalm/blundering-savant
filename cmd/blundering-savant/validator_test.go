package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSerializeFailureDetails(t *testing.T) {
	run := WorkflowRun{
		ID:         100,
		Status:     WorkflowStatusCompleted,
		Conclusion: WorkflowConclusionFailure,

		Jobs: []WorkflowJob{
			{
				ID:         200,
				Status:     JobStatusCompleted,
				Conclusion: JobConclusionSuccess,

				Steps: []WorkflowStep{
					{
						Number: 1,
						Logs:   "Not expected in output",
					},
				},
			},
			{
				ID:         201,
				Status:     JobStatusCompleted,
				Conclusion: JobConclusionFailure,

				Steps: []WorkflowStep{
					{
						Number:     301,
						Name:       "build",
						Status:     StepStatusCompleted,
						Conclusion: StepConclusionSuccess,

						StartedAt:   time.UnixMilli(1000),
						CompletedAt: time.UnixMilli(1999),

						Logs: "Step 301 log line 1\nStep 301 log line 2\nStep 301 log line 3",
					},
					{
						Number:     302,
						Name:       "lint",
						Status:     StepStatusCompleted,
						Conclusion: StepConclusionFailure,

						StartedAt:   time.UnixMilli(2000),
						CompletedAt: time.UnixMilli(2999),

						Logs: "Step 302 log line 1\nStep 302 log line 2\nStep 302 log line 3",
					},
					{
						Number:     303,
						Name:       "test",
						Status:     StepStatusCompleted,
						Conclusion: StepConclusionSuccess,

						StartedAt:   time.UnixMilli(3000),
						CompletedAt: time.UnixMilli(3999),

						Logs: "Step 303 log line 1\nStep 303 log line 2\nStep 303 log line 3",
					},
				},
			},
			{
				ID:         202,
				Status:     JobStatusCompleted,
				Conclusion: JobConclusionSuccess,

				Steps: []WorkflowStep{
					{
						Number: 1,
						Logs:   "Not expected in output",
					},
				},
			},
		},
	}

	s, err := serializeFailureDetails(run)

	require.NoError(t, err)

	expectedStr := `Job 201 failed:
  Step 302 (lint) failed:
    Step 302 log line 1
    Step 302 log line 2
    Step 302 log line 3`

	require.Equal(t, expectedStr, s)
}

func TestChronoLogChunker_NormalUsage(t *testing.T) {
	lines := []string{}
	for i := 1; i < 10; i++ {
		// E.g. line 1 @ time=1000
		t := time.UnixMilli(int64(i) * 1000).Format(time.RFC3339)
		lines = append(lines, fmt.Sprintf("%s log line %d\n", t, i))
	}

	chunker := newChronoLogChunker(strings.Join(lines, ""))

	{
		chunk, err := chunker.NextUntil(time.UnixMilli(2500))
		require.NoError(t, err)
		// Expect lines 1 and 2
		expectedChunk := strings.Join(lines[:2], "")
		require.Equal(t, expectedChunk, chunk)
	}

	{
		chunk, err := chunker.NextUntil(time.UnixMilli(5000))
		require.NoError(t, err)
		// Expect lines 3, 4, and 5 (inclusive end time)
		expectedChunk := strings.Join(lines[2:5], "")
		require.Equal(t, expectedChunk, chunk)
	}

	{
		chunk, err := chunker.NextUntil(time.UnixMilli(5500))
		require.NoError(t, err)
		// Expect no lines
		expectedChunk := ""
		require.Equal(t, expectedChunk, chunk)
	}

	{
		chunk, err := chunker.NextUntil(time.UnixMilli(9999))
		require.NoError(t, err)
		// Expect remaining lines
		expectedChunk := strings.Join(lines[5:], "")
		require.Equal(t, expectedChunk, chunk)
	}

	{
		chunk, err := chunker.NextUntil(time.UnixMilli(9999))
		require.NoError(t, err)
		// Expect no lines (none left)
		expectedChunk := ""
		require.Equal(t, expectedChunk, chunk)
	}
}
