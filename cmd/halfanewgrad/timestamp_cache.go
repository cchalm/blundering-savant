package main

import (
	"fmt"
	"sync"
	"time"
)

// TimestampCache provides an interface for caching issue/PR timestamps
type TimestampCache interface {
	// GetTimestamps returns the last known timestamps for an issue and its associated PR
	GetTimestamps(owner, repo string, issueNumber int) (issueUpdatedAt, prUpdatedAt *time.Time, found bool)
	
	// SetTimestamps stores the timestamps for an issue and its associated PR
	SetTimestamps(owner, repo string, issueNumber int, issueUpdatedAt, prUpdatedAt *time.Time)
}

// MemoryTimestampCache implements TimestampCache using in-memory storage
type MemoryTimestampCache struct {
	mu    sync.RWMutex
	cache map[string]TimestampEntry
}

// TimestampEntry holds the cached timestamp information
type TimestampEntry struct {
	IssueUpdatedAt *time.Time
	PRUpdatedAt    *time.Time
}

// NewMemoryTimestampCache creates a new in-memory timestamp cache
func NewMemoryTimestampCache() *MemoryTimestampCache {
	return &MemoryTimestampCache{
		cache: make(map[string]TimestampEntry),
	}
}

// GetTimestamps returns the cached timestamps for an issue and its PR
func (c *MemoryTimestampCache) GetTimestamps(owner, repo string, issueNumber int) (issueUpdatedAt, prUpdatedAt *time.Time, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	key := fmt.Sprintf("%s/%s/%d", owner, repo, issueNumber)
	entry, exists := c.cache[key]
	if !exists {
		return nil, nil, false
	}
	
	return entry.IssueUpdatedAt, entry.PRUpdatedAt, true
}

// SetTimestamps stores the timestamps for an issue and its PR
func (c *MemoryTimestampCache) SetTimestamps(owner, repo string, issueNumber int, issueUpdatedAt, prUpdatedAt *time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	key := fmt.Sprintf("%s/%s/%d", owner, repo, issueNumber)
	c.cache[key] = TimestampEntry{
		IssueUpdatedAt: issueUpdatedAt,
		PRUpdatedAt:    prUpdatedAt,
	}
}