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

## Conversation

{{- range .Messages}}
{{- if eq .Type "user_text"}}

### 👤 User

{{.Text}}
{{- else if eq .Type "assistant_text"}}

### 🤖 Assistant
{{- if .TokenUsage}}
*Token Usage: {{.TokenUsage.InputTokens}} input, {{.TokenUsage.OutputTokens}} output{{if gt .TokenUsage.CacheCreationTokens 0}}, {{.TokenUsage.CacheCreationTokens}} cache create{{end}}{{if gt .TokenUsage.CacheReadTokens 0}}, {{.TokenUsage.CacheReadTokens}} cache read{{end}}*
{{- end}}

{{- range $line := splitLines .Text}}
> {{$line}}
{{- end}}
{{- else if eq .Type "assistant_thinking"}}

<details>
<summary>🤔 Claude's Thinking</summary>

```
{{.Thinking}}
```

</details>
{{- else if eq .Type "tool_action"}}

<details>
<summary>{{toolSummary .ToolName .ToolInput .Command .Path}}</summary>

**Tool:** `{{.ToolName}}`

{{- if .ToolInput}}
**Input:**
```json
{{prettifyJSON .ToolInput}}
```
{{- end}}

{{- if .ToolResult}}
**Result:**{{if .IsError}} ⚠️ **Error**{{end}}
```
{{truncateContent .ToolResult}}
```
{{- end}}

</details>
{{- end}}
{{- end}}

---

## Summary

- **Total Messages:** {{len .Messages}}
- **Total Tokens:** {{add .TokenUsage.TotalInputTokens .TokenUsage.TotalOutputTokens}} ({{.TokenUsage.TotalInputTokens}} input + {{.TokenUsage.TotalOutputTokens}} output)
{{- if gt .TokenUsage.TotalCacheCreationTokens 0}}
- **Cache Performance:** {{.TokenUsage.TotalCacheCreationTokens}} tokens created, {{.TokenUsage.TotalCacheReadTokens}} tokens read
{{- end}}

---

*Exported from Blundering Savant conversation system*