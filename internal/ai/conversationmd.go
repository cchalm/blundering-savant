package ai

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"
)

//go:embed conversation_template.tmpl
var conversationMarkdownTemplate string

// conversationMarkdownData represents the simplified data structure for markdown rendering
type conversationMarkdownData struct {
	SystemPrompt string                 `json:"systemPrompt"`
	Messages     []conversationMessage  `json:"messages"`
	CreatedAt    string                 `json:"createdAt"`
	TokenUsage   conversationTokenUsage `json:"tokenUsage"`
}

// conversationMessage represents a single sequential message in the conversation
type conversationMessage struct {
	Type       string             `json:"type"` // "user_text", "assistant_text", "assistant_thinking", "tool_action"
	Text       string             `json:"text,omitempty"`
	Thinking   string             `json:"thinking,omitempty"`
	ToolName   string             `json:"toolName,omitempty"`
	ToolInput  string             `json:"toolInput,omitempty"`
	ToolResult string             `json:"toolResult,omitempty"`
	IsError    bool               `json:"isError,omitempty"`
	TokenUsage *messageTokenUsage `json:"tokenUsage,omitempty"`
	// Tool-specific parsed fields for template use
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
}

// conversationTokenUsage represents aggregate token usage
type conversationTokenUsage struct {
	TotalInputTokens         int64 `json:"totalInputTokens"`
	TotalOutputTokens        int64 `json:"totalOutputTokens"`
	TotalCacheCreationTokens int64 `json:"totalCacheCreationTokens"`
	TotalCacheReadTokens     int64 `json:"totalCacheReadTokens"`
}

// messageTokenUsage represents token usage for a single message
type messageTokenUsage struct {
	InputTokens         int64 `json:"inputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	CacheCreationTokens int64 `json:"cacheCreationTokens"`
	CacheReadTokens     int64 `json:"cacheReadTokens"`
}

// ToMarkdown converts the ClaudeConversation to a well-organized markdown string
func (cc *Conversation) ToMarkdown() (string, error) {
	data, err := cc.buildMarkdownData()
	if err != nil {
		return "", fmt.Errorf("failed to build conversation data: %w", err)
	}

	return renderConversationMarkdown(data)
}

// buildMarkdownData converts ClaudeConversation to simplified markdown data
func (cc *Conversation) buildMarkdownData() (*conversationMarkdownData, error) {
	data := &conversationMarkdownData{
		SystemPrompt: cc.systemPrompt,
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05 MST"),
		TokenUsage:   conversationTokenUsage{},
	}

	// Process each turn
	for _, turn := range cc.Turns {
		// Process user instructions
		for _, textBlock := range turn.UserInstructions {
			data.Messages = append(data.Messages, conversationMessage{
				Type: "user_text",
				Text: textBlock.Text,
			})
		}

		// Process assistant text blocks
		for _, block := range turn.AssistantTextBlocks {
			if textParam := block.OfText; textParam != nil {
				data.Messages = append(data.Messages, conversationMessage{
					Type: "assistant_text",
					Text: textParam.Text,
				})
			} else if thinkingParam := block.OfThinking; thinkingParam != nil {
				data.Messages = append(data.Messages, conversationMessage{
					Type:     "assistant_thinking",
					Thinking: thinkingParam.Thinking,
				})
			}
		}

		// Process tool exchanges
		for _, exchange := range turn.ToolExchanges {
			toolMsg := conversationMessage{
				Type:      "tool_action",
				ToolName:  exchange.ToolUse.Name,
				ToolInput: string(exchange.ToolUse.Input),
			}

			// Only process tool result if ToolUseID is set (meaning result was provided)
			if exchange.ToolResult.ToolUseID != "" {
				toolMsg.IsError = exchange.ToolResult.IsError.Or(false)

				// Extract tool result text
				var resultText strings.Builder
				for _, resultContent := range exchange.ToolResult.Content {
					if resultContent.OfText != nil {
						resultText.WriteString(resultContent.OfText.Text)
					} else if resultContent.OfImage != nil {
						resultText.WriteString("[Image content]")
					}
				}
				toolMsg.ToolResult = resultText.String()
			}

			parseToolSpecificFields(&toolMsg)
			data.Messages = append(data.Messages, toolMsg)
		}
	}

	// Token usage is tracked in lastUsage field now
	data.TokenUsage.TotalInputTokens = cc.lastUsage.InputTokens
	data.TokenUsage.TotalOutputTokens = cc.lastUsage.OutputTokens
	data.TokenUsage.TotalCacheCreationTokens = cc.lastUsage.CacheCreationInputTokens
	data.TokenUsage.TotalCacheReadTokens = cc.lastUsage.CacheReadInputTokens

	return data, nil
}

// renderConversationMarkdown renders the conversation data using the template
func renderConversationMarkdown(data *conversationMarkdownData) (string, error) {
	// Create template with helper functions - these are purely for data manipulation, not formatting
	funcMap := template.FuncMap{
		"splitLines": func(text string) []string {
			return strings.Split(text, "\n")
		},
		"prettifyJSON": func(jsonStr string) string {
			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, []byte(jsonStr), "", "  "); err == nil {
				return prettyJSON.String()
			}
			return jsonStr
		},
		"truncateContent": func(content string) string {
			if len(content) > 5000 {
				return content[:5000] + "\n... (content truncated)"
			}
			return content
		},
		"toolSummary": func(toolName, toolInput, command, path string) string {
			// Template function to generate tool summaries
			switch toolName {
			case "str_replace_based_edit_tool":
				switch command {
				case "view":
					return fmt.Sprintf("👀 Reading '%s'", path)
				case "str_replace":
					return fmt.Sprintf("✏️ Editing '%s'", path)
				case "create":
					return fmt.Sprintf("📄 Creating '%s'", path)
				case "insert":
					return fmt.Sprintf("➕ Inserting into '%s'", path)
				default:
					if path != "" {
						return fmt.Sprintf("🔧 %s '%s'", command, path)
					}
					return fmt.Sprintf("🔧 %s", command)
				}
			case "post_comment":
				// Parse comment type from input for more specific summary
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(toolInput), &input); err == nil {
					if commentType, ok := input["comment_type"].(string); ok {
						switch commentType {
						case "issue":
							return "💬 Posting issue comment"
						case "pr":
							return "💬 Posting PR comment"
						case "review":
							return "💬 Replying to review comment"
						}
					}
				}
				return "💬 Posting comment"
			case "add_reaction":
				// Parse reaction from input for more specific summary
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(toolInput), &input); err == nil {
					if reaction, ok := input["reaction"].(string); ok {
						emojiMap := map[string]string{
							"+1":       "👍",
							"-1":       "👎",
							"laugh":    "😄",
							"confused": "😕",
							"heart":    "❤️",
							"hooray":   "🎉",
							"rocket":   "🚀",
							"eyes":     "👀",
						}
						if emoji, found := emojiMap[reaction]; found {
							return fmt.Sprintf("%s Adding reaction", emoji)
						}
						return fmt.Sprintf("%s Adding reaction", reaction)
					}
				}
				return "👍 Adding reaction"
			case "validate_changes":
				return "✅ Validating changes"
			case "publish_changes_for_review":
				return "📤 Publishing changes for review"
			case "delete_file":
				// Parse path from input for more specific summary
				var input map[string]interface{}
				if err := json.Unmarshal([]byte(toolInput), &input); err == nil {
					if path, ok := input["path"].(string); ok && path != "" {
						return fmt.Sprintf("🗑️ Deleting '%s'", path)
					}
				}
				return "🗑️ Deleting file"
			case "report_limitation":
				return "🆘 Reporting limitation"
			default:
				return fmt.Sprintf("🔧 Using tool: %s", toolName)
			}
		},
		"indent": func(prefix string, text string) string {
			prefixed := strings.Builder{}
			for line := range strings.Lines(text) {
				prefixed.WriteString(prefix)
				prefixed.WriteString(line)
			}
			return prefixed.String()
		},
	}

	tmpl, err := template.New("conversation").Funcs(funcMap).Parse(conversationMarkdownTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse conversation template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", fmt.Errorf("failed to execute conversation template: %w", err)
	}

	return buf.String(), nil
}

// parseToolSpecificFields extracts tool-specific fields for template use
func parseToolSpecificFields(msg *conversationMessage) {
	if msg.ToolName == "str_replace_based_edit_tool" {
		var input map[string]interface{}
		if err := json.Unmarshal([]byte(msg.ToolInput), &input); err == nil {
			if command, ok := input["command"].(string); ok {
				msg.Command = command
			}
			if path, ok := input["path"].(string); ok {
				msg.Path = path
			}
		}
	}
}
