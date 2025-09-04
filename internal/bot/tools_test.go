package bot

import (
	"strings"
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



func testSearchInFileToolParseInput(t *testing.T, inputJSON []byte, wantError bool) {
	tool := NewSearchInFileTool()
	block := anthropic.ToolUseBlock{
		ID:    "test",
		Name:  "search_in_file",
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

func TestSearchInFileTool_ParseInput_ValidJSON(t *testing.T) {
	validJSON := []byte(`{"file_path": "test.go", "query": "func main", "use_regex": false, "max_results": 10}`)
	testSearchInFileToolParseInput(t, validJSON, false)
}

func TestSearchInFileTool_ParseInput_MinimalValidJSON(t *testing.T) {
	validJSON := []byte(`{"file_path": "test.go", "query": "test"}`)
	testSearchInFileToolParseInput(t, validJSON, false)
}

func TestSearchInFileTool_ParseInput_InvalidJSON(t *testing.T) {
	invalidJSON := []byte(`{"file_path": "test.go", "query": "test"`) // Missing closing brace
	testSearchInFileToolParseInput(t, invalidJSON, true)
}

func TestSearchInFileTool_searchInFile(t *testing.T) {
	tool := NewSearchInFileTool()
	content := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
	fmt.Printf("Testing search functionality")
}`

	// Test basic string search
	input := &SearchInFileInput{
		FilePath:     "test.go",
		Query:        "fmt.Println",
		ContextLines: 1,
	}
	results, err := tool.searchInFile("test.go", content, input)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
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
	input = &SearchInFileInput{
		FilePath:      "test.go",
		Query:         "FUNC MAIN",
		CaseSensitive: false,
	}
	results, err = tool.searchInFile("test.go", content, input)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 case insensitive result, got %d", len(results))
	}

	// Test regex search
	input = &SearchInFileInput{
		FilePath: "test.go",
		Query:    `fmt\.\w+`,
		UseRegex: true,
	}
	results, err = tool.searchInFile("test.go", content, input)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("Expected 2 regex results, got %d", len(results))
	}

	// Test max results limit
	input = &SearchInFileInput{
		FilePath:   "test.go",
		Query:      "fmt",
		MaxResults: 1,
	}
	results, err = tool.searchInFile("test.go", content, input)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected max 1 result, got %d", len(results))
	}
}
