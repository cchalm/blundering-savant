package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
)

type ConversationHistoryStore interface {
	// Get returns the conversation history stored at the given key, or nil if there is nothing stored at that key
	Get(key string) (*conversationHistory, error)
	// Set stores a conversation history with a key
	Set(key string, value conversationHistory) error
	// Delete deletes the conversation history stored at the given key
	Delete(key string) error
}

// FileSystemConversationHistoryStore implements ConversationHistoryStore using the OS file system
type FileSystemConversationHistoryStore struct {
	dir string // The directory keys will be relative to
}

func (fskvs FileSystemConversationHistoryStore) Get(key string) (*conversationHistory, error) {
	path := path.Join(fskvs.dir, key)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	var value conversationHistory
	err = json.Unmarshal(b, &value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation history: %w", err)
	}
	return &value, nil
}

func (fskvs FileSystemConversationHistoryStore) Set(key string, value conversationHistory) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation history: %w", err)
	}
	path := path.Join(fskvs.dir, key)
	err = os.WriteFile(path, b, 0666)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (fskvs FileSystemConversationHistoryStore) Delete(key string) error {
	path := path.Join(fskvs.dir, key)
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}
