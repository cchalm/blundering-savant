package main

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestDeleteFileTool_ParseInput(t *testing.T) {
	tool := NewDeleteFileTool()

	tests := []struct {
		name      string
		input     map[string]any
		wantError bool
	}{
		{
			name:      "Valid path",
			input:     map[string]any{"path": "test.txt"},
			wantError: false,
		},
		{
			name:      "Empty path - should parse successfully",
			input:     map[string]any{"path": ""},
			wantError: false,
		},
		{
			name:      "Path with leading slash - should parse successfully",
			input:     map[string]any{"path": "/test.txt"},
			wantError: false,
		},
		{
			name:      "Missing path - should parse successfully with empty path",
			input:     map[string]any{},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON, _ := json.Marshal(tt.input)
			block := anthropic.ToolUseBlock{
				ID:    "test",
				Name:  "delete_file",
				Input: inputJSON,
			}

			result, err := tool.ParseToolUse(block)
			if (err != nil) != tt.wantError {
				t.Errorf("ParseToolUse() error = %v, wantError %v", err, tt.wantError)
			}
			
			if !tt.wantError && result == nil {
				t.Error("Expected non-nil result for successful parse")
			}
		})
	}
}

func TestDeleteFileTool_ToolParam(t *testing.T) {
	tool := NewDeleteFileTool()
	param := tool.GetToolParam()

	if param.Name != "delete_file" {
		t.Errorf("Expected tool name 'delete_file', got %s", param.Name)
	}

	if param.Description.Value == "" {
		t.Error("Expected tool description, got empty string")
	}

	// Check that input schema has properties
	if param.InputSchema.Properties == nil {
		t.Error("Expected input schema properties, got nil")
	}

	// Verify the path property exists and has correct type
	properties, ok := param.InputSchema.Properties.(map[string]any)
	if !ok {
		t.Error("Expected input schema properties to be a map")
	}

	pathProp, ok := properties["path"]
	if !ok {
		t.Error("Expected 'path' property in input schema")
	}

	pathPropMap, ok := pathProp.(map[string]any)
	if !ok {
		t.Error("Expected path property to be a map")
	}

	if pathPropMap["type"] != "string" {
		t.Errorf("Expected path type 'string', got %v", pathPropMap["type"])
	}
}