// Package ai provides AI conversation management and related utilities.
package ai

import "github.com/anthropics/anthropic-sdk-go"

// ConversationTurn is a pair of messages: a user message, and an optional assistant response
type ConversationTurn struct {
	UserMessage anthropic.MessageParam
	Response    *anthropic.Message // May be nil
}

// ConversationHistory contains a serializable and resumable snapshot of a ClaudeConversation
type ConversationHistory struct {
	SystemPrompt string             `json:"systemPrompt"`
	Messages     []ConversationTurn `json:"messages"`
}