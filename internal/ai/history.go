package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
)

// FileSystemConversationHistoryStore implements ConversationHistoryStore using the OS file system
type FileSystemConversationHistoryStore struct {
	dir string // The directory keys will be relative to
}

func NewFileSystemConversationHistoryStore(dir string) FileSystemConversationHistoryStore {
	return FileSystemConversationHistoryStore{
		dir: dir,
	}
}

func (fschv FileSystemConversationHistoryStore) Get(key string) (*ConversationHistory, error) {
	path := path.Join(fschv.dir, key)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// The file doesn't exist so nothing is stored at this key
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	var value ConversationHistory
	err = json.Unmarshal(b, &value)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal conversation history: %w", err)
	}
	return &value, nil
}

func (fschv FileSystemConversationHistoryStore) Set(key string, value ConversationHistory) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation history: %w", err)
	}
	path := path.Join(fschv.dir, key)
	err = os.WriteFile(path, b, 0666)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (fschv FileSystemConversationHistoryStore) Delete(key string) error {
	path := path.Join(fschv.dir, key)
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}
