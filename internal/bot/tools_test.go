package bot

import (
	"context"
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func testDeleteFileToolParseInput(t *testing.T, inputJSON []byte, wantError bool) {
	tool := NewDeleteFileTool()
	block := anthropic.ToolUseBlock{
		ID:    "test",
		Name:  "delete_file",
		Input: inputJSON,
	}

	result, err := tool.ParseToolUse(block)
	if (err != nil) != wantError {
		t.Errorf("ParseToolUse() error = %v, wantError %v", err, wantError)
	}

	if !wantError && result == nil {
		t.Error("Expected non-nil result for successful parse")
	}
}

func TestDeleteFileTool_ParseInput_ValidJSON(t *testing.T) {
	validJSON := []byte(`{"path": "test.txt"}`)
	testDeleteFileToolParseInput(t, validJSON, false)
}

func TestDeleteFileTool_ParseInput_InvalidJSON(t *testing.T) {
	invalidJSON := []byte(`{"path": "test.txt"`) // Missing closing brace
	testDeleteFileToolParseInput(t, invalidJSON, true)
}

// Mock workspace for testing
type mockWorkspace struct {
	files map[string]string
	dirs  map[string][]string
}

func newMockWorkspace() *mockWorkspace {
	return &mockWorkspace{
		files: make(map[string]string),
		dirs:  make(map[string][]string),
	}
}

func (mw *mockWorkspace) addFile(path, content string) {
	mw.files[path] = content
}

func (mw *mockWorkspace) addDir(path string, files []string) {
	mw.dirs[path] = files
}

func (mw *mockWorkspace) Read(ctx context.Context, path string) (string, error) {
	content, exists := mw.files[path]
	if !exists {
		return "", fmt.Errorf("file not found")
	}
	return content, nil
}

func (mw *mockWorkspace) Write(ctx context.Context, path string, content string) error {
	mw.files[path] = content
	return nil
}

func (mw *mockWorkspace) Delete(ctx context.Context, path string) error {
	delete(mw.files, path)
	return nil
}

func (mw *mockWorkspace) FileExists(ctx context.Context, path string) (bool, error) {
	_, exists := mw.files[path]
	return exists, nil
}

func (mw *mockWorkspace) IsDir(ctx context.Context, path string) (bool, error) {
	_, exists := mw.dirs[path]
	return exists, nil
}

func (mw *mockWorkspace) ListDir(ctx context.Context, path string) ([]string, error) {
	files, exists := mw.dirs[path]
	if !exists {
		return nil, fmt.Errorf("directory not found")
	}
	return files, nil
}

func testSearchRepositoryToolParseInput(t *testing.T, inputJSON []byte, wantError bool) {
	tool := NewSearchRepositoryTool()
	block := anthropic.ToolUseBlock{
		ID:    "test",
		Name:  "search_repository",
		Input: inputJSON,
	}

	result, err := tool.ParseToolUse(block)
	if (err != nil) != wantError {
		t.Errorf("ParseToolUse() error = %v, wantError %v", err, wantError)
	}

	if !wantError && result == nil {
		t.Error("Expected non-nil result for successful parse")
	}
}

func TestSearchRepositoryTool_ParseInput_ValidJSON(t *testing.T) {
	validJSON := []byte(`{"query": "func main", "use_regex": false, "max_results": 10}`)
	testSearchRepositoryToolParseInput(t, validJSON, false)
}

func TestSearchRepositoryTool_ParseInput_MinimalValidJSON(t *testing.T) {
	validJSON := []byte(`{"query": "test"}`)
	testSearchRepositoryToolParseInput(t, validJSON, false)
}

func TestSearchRepositoryTool_ParseInput_InvalidJSON(t *testing.T) {
	invalidJSON := []byte(`{"query": "test"`) // Missing closing brace
	testSearchRepositoryToolParseInput(t, invalidJSON, true)
}

func TestSearchRepositoryTool_shouldSearchFile(t *testing.T) {
	tool := NewSearchRepositoryTool()

	// Test with no filters
	input := &SearchRepositoryInput{Query: "test"}
	if !tool.shouldSearchFile("test.go", input) {
		t.Error("Expected to search file with no filters")
	}

	// Test with file extension filter
	input = &SearchRepositoryInput{
		Query:          "test",
		FileExtensions: []string{"go", "md"},
	}
	if !tool.shouldSearchFile("test.go", input) {
		t.Error("Expected to search .go file with go extension filter")
	}
	if tool.shouldSearchFile("test.txt", input) {
		t.Error("Expected not to search .txt file with go extension filter")
	}

	// Test with path filter
	input = &SearchRepositoryInput{
		Query:      "test",
		PathFilter: "internal/*",
	}
	if !tool.shouldSearchFile("internal/test.go", input) {
		t.Error("Expected to search file matching path filter")
	}
}

func TestSearchRepositoryTool_searchInFile(t *testing.T) {
	tool := NewSearchRepositoryTool()
	content := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
	fmt.Printf("Testing search functionality")
}`

	// Test basic string search
	input := &SearchRepositoryInput{
		Query:        "fmt.Println",
		ContextLines: 1,
	}
	results := tool.searchInFile("test.go", content, input, nil)
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
	if results[0].LineNumber != 6 {
		t.Errorf("Expected line number 6, got %d", results[0].LineNumber)
	}
	if len(results[0].ContextBefore) != 1 {
		t.Errorf("Expected 1 context line before, got %d", len(results[0].ContextBefore))
	}
	if len(results[0].ContextAfter) != 1 {
		t.Errorf("Expected 1 context line after, got %d", len(results[0].ContextAfter))
	}

	// Test case insensitive search
	input = &SearchRepositoryInput{
		Query:         "FUNC MAIN",
		CaseSensitive: false,
	}
	results = tool.searchInFile("test.go", content, input, nil)
	if len(results) != 1 {
		t.Errorf("Expected 1 case insensitive result, got %d", len(results))
	}
}

func TestSearchRepositoryTool_isBinaryFile(t *testing.T) {
	tool := NewSearchRepositoryTool()

	// Test text file
	textContent := "This is a normal text file"
	if tool.isBinaryFile(textContent) {
		t.Error("Expected text content to not be detected as binary")
	}

	// Test binary file (with null bytes)
	binaryContent := "This has null bytes\x00here"
	if !tool.isBinaryFile(binaryContent) {
		t.Error("Expected content with null bytes to be detected as binary")
	}
}
