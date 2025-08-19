package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

//go:embed conversation_template.md
var conversationTemplateMarkdown string

// conversationMarkdownData represents the simplified data structure for markdown rendering
type conversationMarkdownData struct {
	SystemPrompt string                     `json:"systemPrompt"`
	Turns        []conversationTurnMarkdown `json:"turns"`
	CreatedAt    string                     `json:"createdAt"`
	TokenUsage   conversationTokenUsage     `json:"tokenUsage"`
}

// conversationTurnMarkdown represents a simplified turn for markdown rendering
type conversationTurnMarkdown struct {
	UserMessage    userMessageMarkdown     `json:"userMessage"`
	AssistantReply *assistantReplyMarkdown `json:"assistantReply,omitempty"`
	TurnNumber     int                     `json:"turnNumber"`
	Timestamp      string                  `json:"timestamp"`
}

// userMessageMarkdown represents a simplified user message
type userMessageMarkdown struct {
	Content []contentBlockMarkdown `json:"content"`
}

// assistantReplyMarkdown represents a simplified assistant reply
type assistantReplyMarkdown struct {
	Content    []contentBlockMarkdown `json:"content"`
	StopReason string                 `json:"stopReason"`
	TokenUsage *messageTokenUsage     `json:"tokenUsage,omitempty"`
}

// contentBlockMarkdown represents different types of content blocks
type contentBlockMarkdown struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	ToolID     string `json:"toolId,omitempty"`
	ToolInput  string `json:"toolInput,omitempty"`
	ToolResult string `json:"toolResult,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
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
func (cc *ClaudeConversation) ToMarkdown() (string, error) {
	data, err := cc.buildMarkdownData()
	if err != nil {
		return "", fmt.Errorf("failed to build conversation data: %w", err)
	}

	return renderConversationMarkdown(data)
}

// buildMarkdownData converts ClaudeConversation to simplified markdown data
func (cc *ClaudeConversation) buildMarkdownData() (*conversationMarkdownData, error) {
	data := &conversationMarkdownData{
		SystemPrompt: cc.systemPrompt,
		CreatedAt:    time.Now().Format("2006-01-02 15:04:05 MST"),
		TokenUsage:   conversationTokenUsage{},
	}

	// Convert each turn
	for i, turn := range cc.messages {
		markdownTurn := conversationTurnMarkdown{
			TurnNumber: i + 1,
			Timestamp:  time.Now().Format("15:04:05"),
		}

		// Convert user message
		userMsg, err := convertUserMessage(turn.UserMessage)
		if err != nil {
			return nil, fmt.Errorf("failed to convert user message in turn %d: %w", i+1, err)
		}
		markdownTurn.UserMessage = *userMsg

		// Convert assistant reply if present
		if turn.Response != nil {
			assistantReply, err := convertAssistantReply(turn.Response)
			if err != nil {
				return nil, fmt.Errorf("failed to convert assistant reply in turn %d: %w", i+1, err)
			}
			markdownTurn.AssistantReply = assistantReply

			// Accumulate token usage
			data.TokenUsage.TotalInputTokens += turn.Response.Usage.InputTokens
			data.TokenUsage.TotalOutputTokens += turn.Response.Usage.OutputTokens
			data.TokenUsage.TotalCacheCreationTokens += turn.Response.Usage.CacheCreationInputTokens
			data.TokenUsage.TotalCacheReadTokens += turn.Response.Usage.CacheReadInputTokens
		}

		data.Turns = append(data.Turns, markdownTurn)
	}

	return data, nil
}

// renderConversationMarkdown renders the conversation data using the template
func renderConversationMarkdown(data *conversationMarkdownData) (string, error) {
	// Create template with helper functions
	funcMap := template.FuncMap{
		"formatFileContent": func(content string) string {
			// Truncate very long content for readability
			if len(content) > 5000 {
				return content[:5000] + "\n... (content truncated)"
			}
			return content
		},
		"escapeMarkdown": func(text string) string {
			// Escape markdown special characters in tool inputs/outputs
			text = strings.ReplaceAll(text, "`", "\\`")
			text = strings.ReplaceAll(text, "*", "\\*")
			text = strings.ReplaceAll(text, "_", "\\_")
			return text
		},
		"formatJSON": func(jsonStr string) string {
			// Pretty print JSON if possible, otherwise truncate
			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, []byte(jsonStr), "", "  "); err == nil {
				result := prettyJSON.String()
				if len(result) > 2000 {
					return result[:2000] + "\n... (truncated)"
				}
				return result
			}
			// If JSON parsing fails, just truncate the raw string
			if len(jsonStr) > 500 {
				return jsonStr[:500] + "... (truncated)"
			}
			return jsonStr
		},
		"add": func(a, b int64) int64 {
			return a + b
		},
		"countCompletedTurns": func(turns []conversationTurnMarkdown) int {
			count := 0
			for _, turn := range turns {
				if turn.AssistantReply != nil {
					count++
				}
			}
			return count
		},
	}

	tmpl, err := template.New("conversation").Funcs(funcMap).Parse(conversationTemplateMarkdown)
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

// convertUserMessage converts anthropic.MessageParam to userMessageMarkdown
func convertUserMessage(msg anthropic.MessageParam) (*userMessageMarkdown, error) {
	userMsg := &userMessageMarkdown{}

	for _, contentBlock := range msg.Content {
		block := contentBlockMarkdown{}

		switch content := contentBlock.GetContent().AsAny().(type) {
		case anthropic.TextBlockParam:
			block.Type = "text"
			block.Text = content.Text

		case anthropic.ToolResultBlockParam:
			block.Type = "tool_result"
			block.ToolID = content.ToolUseID
			block.IsError = content.IsError.Or(false)

			// Extract text content from tool result
			var resultText strings.Builder
			for _, resultContent := range content.Content {
				if textBlock := resultContent.OfText; textBlock != nil {
					resultText.WriteString(textBlock.Text)
				} else if imageBlock := resultContent.OfImage; imageBlock != nil {
					resultText.WriteString("[Image content]")
				}
			}
			block.ToolResult = resultText.String()

		case anthropic.ToolUseBlockParam:
			block.Type = "tool_use"
			block.ToolName = content.Name
			block.ToolID = content.ID
			block.ToolInput = fmt.Sprint(content.Input)

		default:
			block.Type = "unknown"
			block.Text = fmt.Sprintf("Unknown content type: %T", content)
		}

		userMsg.Content = append(userMsg.Content, block)
	}

	return userMsg, nil
}

// convertAssistantReply converts anthropic.Message to assistantReplyMarkdown
func convertAssistantReply(msg *anthropic.Message) (*assistantReplyMarkdown, error) {
	reply := &assistantReplyMarkdown{
		StopReason: string(msg.StopReason),
	}

	// Convert token usage if present
	reply.TokenUsage = &messageTokenUsage{
		InputTokens:         msg.Usage.InputTokens,
		OutputTokens:        msg.Usage.OutputTokens,
		CacheCreationTokens: msg.Usage.CacheCreationInputTokens,
		CacheReadTokens:     msg.Usage.CacheReadInputTokens,
	}

	// Convert content blocks
	for _, contentBlock := range msg.Content {
		block := contentBlockMarkdown{}

		switch content := contentBlock.AsAny().(type) {
		case anthropic.TextBlock:
			block.Type = "text"
			block.Text = content.Text

		case anthropic.ToolUseBlock:
			block.Type = "tool_use"
			block.ToolName = content.Name
			block.ToolID = content.ID
			block.ToolInput = string(content.Input)

		case anthropic.ThinkingBlock:
			block.Type = "thinking"
			block.Thinking = content.Thinking

		case anthropic.RedactedThinkingBlock:
			block.Type = "thinking"
			block.Thinking = "[Thinking content redacted]"

		default:
			block.Type = "unknown"
			block.Text = fmt.Sprintf("Unknown content type: %T", content)
		}

		reply.Content = append(reply.Content, block)
	}

	return reply, nil
}
