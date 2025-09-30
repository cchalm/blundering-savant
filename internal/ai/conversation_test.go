package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// messageSenderStub is a stub implementation of MessageSender for testing
type messageSenderStub struct {
	response       *anthropic.Message
	capturedParams *anthropic.MessageNewParams
	err            error
}

func (m *messageSenderStub) SendMessage(_ context.Context, params anthropic.MessageNewParams, _ ...anthropt.RequestOption) (*anthropic.Message, error) {
	m.capturedParams = &params
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// newToolResultBlockParam creates a ToolResultBlockParam for testing
func newToolResultBlockParam(toolID string, result string, isError bool) anthropic.ToolResultBlockParam {
	return anthropic.ToolResultBlockParam{
		ToolUseID: toolID,
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: result}},
		},
		IsError: anthropic.Bool(isError),
	}
}

// newAnthropicMessage creates an anthropic.Message for testing by serializing and deserializing
func newAnthropicMessage(t *testing.T, content ...anthropic.ContentBlockParamUnion) *anthropic.Message {
	if t != nil {
		t.Helper()
	}

	messageParam := anthropic.NewAssistantMessage(content...)
	paramJSON, err := json.Marshal(messageParam)
	if t != nil {
		require.NoError(t, err)
	} else if err != nil {
		panic(err)
	}

	var msg anthropic.Message
	err = json.Unmarshal(paramJSON, &msg)
	if t != nil {
		require.NoError(t, err)
	} else if err != nil {
		panic(err)
	}

	// Set default usage stats
	msg.Usage = anthropic.Usage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheCreationInputTokens: 10,
		CacheReadInputTokens:     5,
	}

	return &msg
}

func TestNewConversation(t *testing.T) {
	sender := &messageSenderStub{}
	model := anthropic.ModelClaudeSonnet4_0
	maxTokens := int64(4000)
	tools := []anthropic.ToolParam{{Name: "test_tool"}}
	systemPrompt := "test system prompt"

	conv := NewConversation(sender, model, maxTokens, tools, systemPrompt)

	assert.Equal(t, model, conv.model)
	assert.Equal(t, maxTokens, conv.maxOutputTokens)
	assert.Equal(t, systemPrompt, conv.systemPrompt)
	assert.Equal(t, tools, conv.tools)
	assert.Empty(t, conv.Turns)
}

func TestConversationHistory(t *testing.T) {
	conv := &Conversation{
		systemPrompt: "test prompt",
		Turns:        []ConversationTurn{},
	}

	history := conv.History()

	assert.Equal(t, "test prompt", history.SystemPrompt)
	assert.Equal(t, 0, len(history.Turns))
}

func TestResumeConversation(t *testing.T) {
	history := ConversationHistory{
		SystemPrompt: "test system prompt",
		Turns:        []ConversationTurn{},
	}

	conv, err := ResumeConversation(nil, history, anthropic.ModelClaudeSonnet4_0, 4000, []anthropic.ToolParam{})

	require.NoError(t, err)
	assert.Equal(t, "test system prompt", conv.systemPrompt)
	assert.Equal(t, 0, len(conv.Turns))
}

func TestSendMessage_WithTextInstructions(t *testing.T) {
	response := newAnthropicMessage(t, anthropic.NewTextBlock("assistant response"))
	sender := &messageSenderStub{response: response}

	conv := NewConversation(sender, anthropic.ModelClaudeSonnet4_0, 4000, nil, "system prompt")

	ctx := context.Background()
	instructions := []anthropic.ContentBlockParamUnion{
		anthropic.NewTextBlock("user instruction"),
	}

	msg, err := conv.SendMessage(ctx, instructions...)

	require.NoError(t, err)
	assert.Equal(t, response, msg)
	assert.Len(t, conv.Turns, 1)
	assert.Equal(t, instructions, conv.Turns[0].Instructions)
	assert.Equal(t, response, conv.Turns[0].Response)
	assert.Empty(t, conv.Turns[0].ToolExchanges)
}

func TestSendMessage_WithToolUseResponse(t *testing.T) {
	response := newAnthropicMessage(t,
		anthropic.NewTextBlock("using tool"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	sender := &messageSenderStub{response: response}
	conv := NewConversation(sender, anthropic.ModelClaudeSonnet4_0, 4000, nil, "system prompt")

	ctx := context.Background()
	instructions := []anthropic.ContentBlockParamUnion{
		anthropic.NewTextBlock("please use the tool"),
	}

	msg, err := conv.SendMessage(ctx, instructions...)

	require.NoError(t, err)
	assert.Equal(t, response, msg)
	assert.Len(t, conv.Turns, 1)
	assert.Len(t, conv.Turns[0].ToolExchanges, 1)
	assert.Equal(t, "tool_123", conv.Turns[0].ToolExchanges[0].UseBlock.ID)
	assert.Equal(t, "test_tool", conv.Turns[0].ToolExchanges[0].UseBlock.Name)
	assert.Nil(t, conv.Turns[0].ToolExchanges[0].ResultBlock)
}

func TestSendMessage_MultipleTurns(t *testing.T) {
	response1 := newAnthropicMessage(t, anthropic.NewTextBlock("first response"))
	response2 := newAnthropicMessage(t, anthropic.NewTextBlock("second response"))

	sender := &messageSenderStub{response: response1}
	conv := NewConversation(sender, anthropic.ModelClaudeSonnet4_0, 4000, nil, "system prompt")

	ctx := context.Background()

	// First turn
	msg1, err := conv.SendMessage(ctx, anthropic.NewTextBlock("first"))
	require.NoError(t, err)
	assert.Equal(t, response1, msg1)
	assert.Len(t, conv.Turns, 1)

	// Second turn
	sender.response = response2
	msg2, err := conv.SendMessage(ctx, anthropic.NewTextBlock("second"))
	require.NoError(t, err)
	assert.Equal(t, response2, msg2)
	assert.Len(t, conv.Turns, 2)
}

func TestSendMessage_Error(t *testing.T) {
	expectedErr := fmt.Errorf("api error")
	sender := &messageSenderStub{err: expectedErr}
	conv := NewConversation(sender, anthropic.ModelClaudeSonnet4_0, 4000, nil, "system prompt")

	ctx := context.Background()
	instructions := []anthropic.ContentBlockParamUnion{
		anthropic.NewTextBlock("user instruction"),
	}

	msg, err := conv.SendMessage(ctx, instructions...)

	require.Error(t, err)
	assert.Nil(t, msg)
	assert.Empty(t, conv.Turns)
}

func TestResendLastMessage_Success(t *testing.T) {
	response1 := newAnthropicMessage(t, anthropic.NewTextBlock("first response"))
	response2 := newAnthropicMessage(t, anthropic.NewTextBlock("resent response"))

	sender := &messageSenderStub{response: response1}
	conv := NewConversation(sender, anthropic.ModelClaudeSonnet4_0, 4000, nil, "system prompt")

	ctx := context.Background()
	instructions := []anthropic.ContentBlockParamUnion{
		anthropic.NewTextBlock("first"),
	}

	// Send first message
	_, err := conv.SendMessage(ctx, instructions...)
	require.NoError(t, err)
	assert.Len(t, conv.Turns, 1)

	// Resend last message
	sender.response = response2
	msg, err := conv.ResendLastMessage(ctx)

	require.NoError(t, err)
	assert.Equal(t, response2, msg)
	assert.Len(t, conv.Turns, 1)
	assert.Equal(t, instructions, conv.Turns[0].Instructions)
	assert.Equal(t, response2, conv.Turns[0].Response)
}

func TestResendLastMessage_NoMessages(t *testing.T) {
	sender := &messageSenderStub{}
	conv := NewConversation(sender, anthropic.ModelClaudeSonnet4_0, 4000, nil, "system prompt")

	ctx := context.Background()
	msg, err := conv.ResendLastMessage(ctx)

	require.Error(t, err)
	assert.Nil(t, msg)
	assert.Contains(t, err.Error(), "cannot resend last message: no messages")
}

func TestGetPendingToolUses_NoPendingTools(t *testing.T) {
	conv := &Conversation{Turns: []ConversationTurn{}}

	pending := conv.GetPendingToolUses()

	assert.Nil(t, pending)
}

func TestGetPendingToolUses_WithPendingTools(t *testing.T) {
	response := newAnthropicMessage(nil,
		anthropic.NewTextBlock("test"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	// Extract the tool use block from the response
	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	conv := &Conversation{
		Turns: []ConversationTurn{
			{
				Response: response,
				ToolExchanges: []ToolExchange{
					{UseBlock: toolUseBlock, ResultBlock: nil},
				},
			},
		},
	}

	pending := conv.GetPendingToolUses()

	require.Len(t, pending, 1)
	assert.Equal(t, toolUseBlock, pending[0])
}

func TestGetPendingToolUses_NoResponseInLastTurn(t *testing.T) {
	conv := &Conversation{
		Turns: []ConversationTurn{
			{
				Response: nil,
			},
		},
	}

	pending := conv.GetPendingToolUses()

	assert.Nil(t, pending)
}

func TestGetPendingToolUses_ToolsWithResults(t *testing.T) {
	response := newAnthropicMessage(nil,
		anthropic.NewTextBlock("test"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	// Extract the tool use block from the response
	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	toolResult := newToolResultBlockParam("tool_123", "result", false)

	conv := &Conversation{
		Turns: []ConversationTurn{
			{
				Response: response,
				ToolExchanges: []ToolExchange{
					{UseBlock: toolUseBlock, ResultBlock: &toolResult},
				},
			},
		},
	}

	pending := conv.GetPendingToolUses()

	assert.Empty(t, pending)
}

func TestAddToolResult_Success(t *testing.T) {
	response := newAnthropicMessage(nil,
		anthropic.NewTextBlock("test"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	// Extract the tool use block from the response
	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	conv := &Conversation{
		Turns: []ConversationTurn{
			{
				Response: response,
				ToolExchanges: []ToolExchange{
					{UseBlock: toolUseBlock, ResultBlock: nil},
				},
			},
		},
	}

	toolResult := newToolResultBlockParam("tool_123", "result", false)

	err := conv.AddToolResult(toolResult)

	require.NoError(t, err)
	assert.NotNil(t, conv.Turns[0].ToolExchanges[0].ResultBlock)
	assert.Equal(t, toolResult.ToolUseID, conv.Turns[0].ToolExchanges[0].ResultBlock.ToolUseID)
}

func TestAddToolResult_NoTurns(t *testing.T) {
	conv := &Conversation{Turns: []ConversationTurn{}}

	toolResult := newToolResultBlockParam("tool_123", "result", false)

	err := conv.AddToolResult(toolResult)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "conversation has no turns")
}

func TestAddToolResult_ToolUseNotFound(t *testing.T) {
	response := newAnthropicMessage(nil,
		anthropic.NewTextBlock("test"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	// Extract the tool use block from the response
	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	conv := &Conversation{
		Turns: []ConversationTurn{
			{
				Response: response,
				ToolExchanges: []ToolExchange{
					{UseBlock: toolUseBlock, ResultBlock: nil},
				},
			},
		},
	}

	toolResult := newToolResultBlockParam("tool_456", "result", false)

	err := conv.AddToolResult(toolResult)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool use block with ID 'tool_456' not found")
}

func TestConvertTurnsToMessages_Empty(t *testing.T) {
	turns := []ConversationTurn{}

	messages, err := convertTurnsToMessages(turns)

	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestConvertTurnsToMessages_SingleTurnWithoutResponse(t *testing.T) {
	turns := []ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("user instruction"),
			},
		},
	}

	messages, err := convertTurnsToMessages(turns)

	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[0].Role)
}

func TestConvertTurnsToMessages_SingleTurnWithResponse(t *testing.T) {
	turns := []ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("user instruction"),
			},
			Response: newAnthropicMessage(nil, anthropic.NewTextBlock("assistant response")),
		},
	}

	messages, err := convertTurnsToMessages(turns)

	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[0].Role)
	assert.Equal(t, anthropic.MessageParamRoleAssistant, messages[1].Role)
}

func TestConvertTurnsToMessages_MultipleTurnsWithToolExchange(t *testing.T) {
	response1 := newAnthropicMessage(nil,
		anthropic.NewTextBlock("use tool"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	// Extract the tool use block from the response
	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response1.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	toolResult := newToolResultBlockParam("tool_123", "result", false)

	turns := []ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("first instruction"),
			},
			Response: response1,
			ToolExchanges: []ToolExchange{
				{UseBlock: toolUseBlock, ResultBlock: &toolResult},
			},
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("second instruction"),
			},
			Response: newAnthropicMessage(nil, anthropic.NewTextBlock("final response")),
		},
	}

	messages, err := convertTurnsToMessages(turns)

	require.NoError(t, err)
	require.Len(t, messages, 4)
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[0].Role)
	assert.Equal(t, anthropic.MessageParamRoleAssistant, messages[1].Role)
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[2].Role)
	// Second user message should contain tool result
	require.Len(t, messages[2].Content, 2)
	assert.NotNil(t, messages[2].Content[0].OfToolResult)
	assert.Equal(t, anthropic.MessageParamRoleAssistant, messages[3].Role)
}

func TestConvertTurnsToMessages_MissingToolResult(t *testing.T) {
	response1 := newAnthropicMessage(nil,
		anthropic.NewTextBlock("use tool"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	// Extract the tool use block from the response
	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response1.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	turns := []ConversationTurn{
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("first instruction"),
			},
			Response: response1,
			ToolExchanges: []ToolExchange{
				{UseBlock: toolUseBlock, ResultBlock: nil},
			},
		},
		{
			Instructions: []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock("second instruction"),
			},
		},
	}

	messages, err := convertTurnsToMessages(turns)

	require.Error(t, err)
	assert.Nil(t, messages)
	assert.Contains(t, err.Error(), "no result added for tool use")
}

func TestBuildToolExchangesFromResponse_NoToolUses(t *testing.T) {
	response := newAnthropicMessage(nil, anthropic.NewTextBlock("just text"))

	exchanges := buildToolExchangesFromResponse(response)

	assert.Empty(t, exchanges)
}

func TestBuildToolExchangesFromResponse_WithToolUses(t *testing.T) {
	response := newAnthropicMessage(nil,
		anthropic.NewTextBlock("using tools"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value1"}, "test_tool_1"),
		anthropic.NewToolUseBlock("tool_456", map[string]string{"param": "value2"}, "test_tool_2"),
	)

	exchanges := buildToolExchangesFromResponse(response)

	require.Len(t, exchanges, 2)
	assert.Equal(t, "tool_123", exchanges[0].UseBlock.ID)
	assert.Equal(t, "test_tool_1", exchanges[0].UseBlock.Name)
	assert.Nil(t, exchanges[0].ResultBlock)
	assert.Equal(t, "tool_456", exchanges[1].UseBlock.ID)
	assert.Equal(t, "test_tool_2", exchanges[1].UseBlock.Name)
	assert.Nil(t, exchanges[1].ResultBlock)
}
