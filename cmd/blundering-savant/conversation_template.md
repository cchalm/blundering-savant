# Claude Conversation Export

**Generated:** {{.CreatedAt}}

## Token Usage Summary

- **Total Input Tokens:** {{.TokenUsage.TotalInputTokens}}
- **Total Output Tokens:** {{.TokenUsage.TotalOutputTokens}}
- **Cache Creation Tokens:** {{.TokenUsage.TotalCacheCreationTokens}}
- **Cache Read Tokens:** {{.TokenUsage.TotalCacheReadTokens}}

## System Prompt

<details>
<summary>View System Prompt</summary>

```
{{.SystemPrompt}}
```

</details>

---

## Conversation Turns

{{- range .Turns}}

### Turn {{.TurnNumber}} - {{.Timestamp}}

#### 👤 User Message
{{- range .UserMessage.Content}}
{{- if eq .Type "text"}}

{{.Text}}
{{- else if eq .Type "tool_result"}}

**🔄 Tool Result** ({{.ToolID}}){{if .IsError}} ⚠️ **Error**{{end}}

<details>
<summary>View Tool Result</summary>

```
{{formatFileContent .ToolResult}}
```

</details>
{{- else if eq .Type "tool_use"}}

**🔧 Tool Use:** `{{.ToolName}}` ({{.ToolID}})

<details>
<summary>View Tool Input</summary>

```json
{{formatJSON .ToolInput}}
```

</details>
{{- else}}

**{{.Type}}:** {{.Text}}
{{- end}}
{{- end}}

{{- if .AssistantReply}}

#### 🤖 Assistant Reply

{{- if .AssistantReply.TokenUsage}}
**Token Usage:** Input: {{.AssistantReply.TokenUsage.InputTokens}}, Output: {{.AssistantReply.TokenUsage.OutputTokens}}, Cache Create: {{.AssistantReply.TokenUsage.CacheCreationTokens}}, Cache Read: {{.AssistantReply.TokenUsage.CacheReadTokens}}
{{- end}}

{{- range .AssistantReply.Content}}
{{- if eq .Type "text"}}

{{.Text}}
{{- else if eq .Type "tool_use"}}

**🔧 Tool Use:** `{{.ToolName}}` ({{.ToolID}})

<details>
<summary>View Tool Input</summary>

```json
{{formatJSON .ToolInput}}
```

</details>
{{- else if eq .Type "thinking"}}

<details>
<summary>🤔 Claude's Thinking</summary>

```
{{.Thinking}}
```

</details>
{{- else}}

**{{.Type}}:** {{.Text}}
{{- end}}
{{- end}}

**Stop Reason:** `{{.AssistantReply.StopReason}}`
{{- else}}

#### 🤖 Assistant Reply
*No response yet - conversation was interrupted*
{{- end}}

---
{{- end}}

## Summary

- **Total Turns:** {{len .Turns}}
- **Completed Turns:** {{countCompletedTurns .Turns}}
- **Total Tokens:** {{add .TokenUsage.TotalInputTokens .TokenUsage.TotalOutputTokens}} ({{.TokenUsage.TotalInputTokens}} input + {{.TokenUsage.TotalOutputTokens}} output)
{{- if gt .TokenUsage.TotalCacheCreationTokens 0}}
- **Cache Performance:** {{.TokenUsage.TotalCacheCreationTokens}} tokens created, {{.TokenUsage.TotalCacheReadTokens}} tokens read
{{- end}}

---

*Exported from Blundering Savant conversation system*