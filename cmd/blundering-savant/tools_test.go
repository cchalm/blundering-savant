package main

import (
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

