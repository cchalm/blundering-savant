package main

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func testDeleteFileToolParseInput(t *testing.T, input map[string]any, wantError bool) {
	tool := NewDeleteFileTool()
	inputJSON, _ := json.Marshal(input)
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

func TestDeleteFileTool_ParseInput_Valid(t *testing.T) {
	testDeleteFileToolParseInput(t, map[string]any{"path": "test.txt"}, false)
}

func TestDeleteFileTool_ParseInput_Empty(t *testing.T) {
	testDeleteFileToolParseInput(t, map[string]any{"path": ""}, false)
}

func TestDeleteFileTool_ParseInput_LeadingSlash(t *testing.T) {
	testDeleteFileToolParseInput(t, map[string]any{"path": "/test.txt"}, false)
}

func TestDeleteFileTool_ParseInput_Missing(t *testing.T) {
	testDeleteFileToolParseInput(t, map[string]any{}, false)
}

