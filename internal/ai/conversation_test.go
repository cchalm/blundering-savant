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
	response       anthropic.Message
	capturedParams *anthropic.MessageNewParams
	err            error
}

func (m *messageSenderStub) SendMessage(_ context.Context, params anthropic.MessageNewParams, _ ...anthropt.RequestOption) (anthropic.Message, error) {
	m.capturedParams = &params
	if m.err != nil {
		return anthropic.Message{}, m.err
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
func newAnthropicMessage(t *testing.T, content ...anthropic.ContentBlockParamUnion) anthropic.Message {
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

	return msg
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
				// Response is zero value (no response set)
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

func TestSendMessage_RejectsToolResults(t *testing.T) {
	stub := &messageSenderStub{
		response: newAnthropicMessage(t, anthropic.NewTextBlock("response")),
	}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, nil, "system prompt")

	// Try to send a tool result as an instruction (this should be rejected)
	toolResult := newToolResultBlockParam("tool_123", "result", false)

	_, err := conv.SendMessage(context.Background(), anthropic.ContentBlockParamUnion{OfToolResult: &toolResult})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool results must not be passed as instructions")
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

func TestFork_AtIndexZero(t *testing.T) {
	stub := &messageSenderStub{}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")

	// Add some turns
	conv.Turns = []ConversationTurn{
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 1")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 2")}},
	}

	forked, err := conv.Fork(0)

	require.NoError(t, err)
	assert.Empty(t, forked.Turns)
	assert.Equal(t, "test prompt", forked.systemPrompt)
	assert.Equal(t, anthropic.ModelClaudeSonnet4_5, forked.model)
	assert.Equal(t, int64(1000), forked.maxOutputTokens)
}

func TestFork_InMiddle(t *testing.T) {
	stub := &messageSenderStub{}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")

	// Add some turns
	conv.Turns = []ConversationTurn{
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 1")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 2")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 3")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 4")}},
	}

	forked, err := conv.Fork(2)

	require.NoError(t, err)
	require.Len(t, forked.Turns, 2)
	assert.Equal(t, "turn 1", forked.Turns[0].Instructions[0].OfText.Text)
	assert.Equal(t, "turn 2", forked.Turns[1].Instructions[0].OfText.Text)
}

func TestFork_AtEnd(t *testing.T) {
	stub := &messageSenderStub{}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")

	// Add some turns
	conv.Turns = []ConversationTurn{
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 1")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 2")}},
	}

	forked, err := conv.Fork(2)

	require.NoError(t, err)
	require.Len(t, forked.Turns, 2)
	assert.Equal(t, "turn 1", forked.Turns[0].Instructions[0].OfText.Text)
	assert.Equal(t, "turn 2", forked.Turns[1].Instructions[0].OfText.Text)
}

func TestFork_BeyondEnd(t *testing.T) {
	stub := &messageSenderStub{}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")

	// Add some turns
	conv.Turns = []ConversationTurn{
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 1")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 2")}},
	}

	forked, err := conv.Fork(3)

	require.Error(t, err)
	assert.Nil(t, forked)
	assert.Contains(t, err.Error(), "turnIndex is 3, but there are only 2 turns")
}

func TestFork_IndependentCopy(t *testing.T) {
	stub := &messageSenderStub{
		response: newAnthropicMessage(t, anthropic.NewTextBlock("response")),
	}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")

	// Add some turns
	conv.Turns = []ConversationTurn{
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 1")}},
		{Instructions: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("turn 2")}},
	}

	forked, err := conv.Fork(1)
	require.NoError(t, err)

	// Modify the forked conversation
	_, err = forked.SendMessage(context.Background(), anthropic.NewTextBlock("new instruction"))
	require.NoError(t, err)

	// Original conversation should be unchanged
	require.Len(t, conv.Turns, 2)
	require.Len(t, forked.Turns, 2) // 1 from fork + 1 from SendMessage
	assert.Equal(t, "turn 1", conv.Turns[0].Instructions[0].OfText.Text)
	assert.Equal(t, "turn 2", conv.Turns[1].Instructions[0].OfText.Text)
	assert.Equal(t, "turn 1", forked.Turns[0].Instructions[0].OfText.Text)
	assert.Equal(t, "new instruction", forked.Turns[1].Instructions[0].OfText.Text)
}

func TestOutputFilter_Basic(t *testing.T) {
	// Create a simple filter that replaces all tool result text with "SUPPRESSED"
	filter := func(turns []ConversationTurnParam) []ConversationTurnParam {
		modifiedTurns := make([]ConversationTurnParam, len(turns))
		for i, turn := range turns {
			modifiedTurn := turn
			modifiedToolExchanges := make([]ToolExchangeParam, len(turn.ToolExchanges))
			copy(modifiedToolExchanges, turn.ToolExchanges)

			for j, exchange := range modifiedToolExchanges {
				suppressedResult := exchange.ResultBlock
				suppressedResult.Content = []anthropic.ToolResultBlockParamContentUnion{
					{OfText: &anthropic.TextBlockParam{Text: "SUPPRESSED"}},
				}
				modifiedToolExchanges[j].ResultBlock = suppressedResult
			}

			modifiedTurn.ToolExchanges = modifiedToolExchanges
			modifiedTurns[i] = modifiedTurn
		}
		return modifiedTurns
	}

	response1 := newAnthropicMessage(nil,
		anthropic.NewTextBlock("use tool"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	var toolUseBlock anthropic.ToolUseBlock
	for _, content := range response1.Content {
		if block, ok := content.AsAny().(anthropic.ToolUseBlock); ok {
			toolUseBlock = block
			break
		}
	}

	toolResult := newToolResultBlockParam("tool_123", "original result", false)

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
		},
	}

	// Convert turns to params and apply filter
	turnParams := make([]ConversationTurnParam, len(turns))
	for i, turn := range turns {
		tp, err := turn.ToParam()
		require.NoError(t, err)
		turnParams[i] = tp
	}
	filteredTurns := filter(turnParams)
	messages, err := convertTurnParamsToMessages(filteredTurns)

	require.NoError(t, err)
	require.Len(t, messages, 3)

	// The second user message should contain the suppressed tool result
	assert.Equal(t, anthropic.MessageParamRoleUser, messages[2].Role)
	require.Len(t, messages[2].Content, 2)
	assert.NotNil(t, messages[2].Content[0].OfToolResult)
	assert.Len(t, messages[2].Content[0].OfToolResult.Content, 1)
	assert.Equal(t, "SUPPRESSED", messages[2].Content[0].OfToolResult.Content[0].OfText.Text)
}

func TestOutputFilter_AppliedDynamically(t *testing.T) {
	// Create a filter that counts how many times it's called
	callCount := 0
	filter := func(turns []ConversationTurnParam) []ConversationTurnParam {
		callCount++
		return turns // No-op filter
	}

	stub := &messageSenderStub{
		response: newAnthropicMessage(t, anthropic.NewTextBlock("response")),
	}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")
	conv.SetOutputFilter(filter)

	// Send first message
	_, err := conv.SendMessage(context.Background(), anthropic.NewTextBlock("message 1"))
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Send second message
	_, err = conv.SendMessage(context.Background(), anthropic.NewTextBlock("message 2"))
	require.NoError(t, err)
	assert.Equal(t, 2, callCount)
}

func TestOutputFilter_ReceivesAllTurns(t *testing.T) {
	// Create a filter that verifies it receives all turns
	var capturedTurnCount int
	filter := func(turns []ConversationTurnParam) []ConversationTurnParam {
		capturedTurnCount = len(turns)
		return turns
	}

	stub := &messageSenderStub{
		response: newAnthropicMessage(t, anthropic.NewTextBlock("response")),
	}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")
	conv.SetOutputFilter(filter)

	// Send first message
	_, err := conv.SendMessage(context.Background(), anthropic.NewTextBlock("message 1"))
	require.NoError(t, err)
	assert.Equal(t, 1, capturedTurnCount) // Initial turn being sent

	// Send second message
	_, err = conv.SendMessage(context.Background(), anthropic.NewTextBlock("message 2"))
	require.NoError(t, err)
	assert.Equal(t, 2, capturedTurnCount) // Previous turn + new turn being sent
}

func TestOutputFilter_DoesNotModifyOriginalHistory(t *testing.T) {
	// Create a malicious filter that tries to modify turns in place
	maliciousFilter := func(turns []ConversationTurnParam) []ConversationTurnParam {
		// Try to corrupt the first turn's instructions
		if len(turns) > 0 && len(turns[0].Instructions) > 0 {
			turns[0].Instructions[0] = anthropic.NewTextBlock("CORRUPTED")
		}
		return turns
	}

	response1 := newAnthropicMessage(t,
		anthropic.NewTextBlock("response 1"),
		anthropic.NewToolUseBlock("tool_123", map[string]string{"param": "value"}, "test_tool"),
	)

	stub := &messageSenderStub{response: response1}
	conv := NewConversation(stub, anthropic.ModelClaudeSonnet4_5, 1000, []anthropic.ToolParam{}, "test prompt")
	conv.SetOutputFilter(maliciousFilter)

	// Send first message
	originalInstruction := "original message"
	_, err := conv.SendMessage(context.Background(), anthropic.NewTextBlock(originalInstruction))
	require.NoError(t, err)

	// Add tool result
	toolResult := newToolResultBlockParam("tool_123", "result", false)
	err = conv.AddToolResult(toolResult)
	require.NoError(t, err)

	// Send second message - this will trigger the filter again
	stub.response = newAnthropicMessage(t, anthropic.NewTextBlock("response 2"))
	_, err = conv.SendMessage(context.Background(), anthropic.NewTextBlock("message 2"))
	require.NoError(t, err)

	// Verify the original conversation history was not corrupted
	require.Len(t, conv.Turns, 2)
	require.Len(t, conv.Turns[0].Instructions, 1)
	assert.Equal(t, originalInstruction, conv.Turns[0].Instructions[0].OfText.Text)
	assert.NotEqual(t, "CORRUPTED", conv.Turns[0].Instructions[0].OfText.Text)
}
