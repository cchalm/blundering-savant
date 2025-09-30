package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/cchalm/blundering-savant/internal/ai"
	"github.com/stretchr/testify/require"
)

var (
	summary = anthropic.NewTextBlock("this is a summary")
)

func TestSummarize_Basic(t *testing.T) {
	turns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn(t, 3),
		turn(t, 4),
		turn(t, 5),
		turn(t, 6),
		turn(t, 7),
		turn(t, 8),
		turn(t, 9),
		turn(t, 10),
	}

	keepFirst := 2
	keepLast := 3

	turn3 := turn(t, 3)
	turn3.UserInstructions = append(turn3.UserInstructions, repeatSummaryRequest)
	turn3.AssistantTextBlocks = []anthropic.ContentBlockParamUnion{summary}

	turn7 := turn(t, 7)

	expectedSummarizedTurns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn3,
		{
			UserInstructions:    []anthropic.ContentBlockParamUnion{resumeFromSummaryRequest},
			AssistantTextBlocks: turn7.AssistantTextBlocks,
			ToolExchanges:       []ai.ToolExchange{},
		},
		turn(t, 8),
		turn(t, 9),
		turn(t, 10),
	}

	testSummarize(t, turns, keepFirst, keepLast, expectedSummarizedTurns)
}

func TestSummarize_KeepNone(t *testing.T) {
	turns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn(t, 3),
		turn(t, 4),
		turn(t, 5),
		turn(t, 6),
		turn(t, 7),
		turn(t, 8),
		turn(t, 9),
		turn(t, 10),
	}

	keepFirst := 0
	keepLast := 0

	turn1 := turn(t, 1)
	turn1.UserInstructions = append(turn1.UserInstructions, repeatSummaryRequest)
	turn1.AssistantTextBlocks = []anthropic.ContentBlockParamUnion{summary}

	turn10 := turn(t, 10)

	expectedSummarizedTurns := []ai.ConversationTurn{
		turn1,
		{
			UserInstructions:    []anthropic.ContentBlockParamUnion{resumeFromSummaryRequest},
			AssistantTextBlocks: turn10.AssistantTextBlocks,
			ToolExchanges:       []ai.ToolExchange{},
		},
	}

	testSummarize(t, turns, keepFirst, keepLast, expectedSummarizedTurns)
}

func TestSummarize_KeepAllButTwo(t *testing.T) {
	turns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn(t, 3),
		turn(t, 4),
		turn(t, 5),
		turn(t, 6),
		turn(t, 7),
		turn(t, 8),
		turn(t, 9),
		turn(t, 10),
	}

	keepFirst := 6
	keepLast := 2

	turn7 := turn(t, 7)
	turn7.UserInstructions = append(turn7.UserInstructions, repeatSummaryRequest)
	turn7.AssistantTextBlocks = []anthropic.ContentBlockParamUnion{summary}

	turn8 := turn(t, 8)

	expectedSummarizedTurns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn(t, 3),
		turn(t, 4),
		turn(t, 5),
		turn(t, 6),
		turn7,
		{
			UserInstructions:    []anthropic.ContentBlockParamUnion{resumeFromSummaryRequest},
			AssistantTextBlocks: turn8.AssistantTextBlocks,
			ToolExchanges:       []ai.ToolExchange{},
		},
		turn(t, 9),
		turn(t, 10),
	}

	testSummarize(t, turns, keepFirst, keepLast, expectedSummarizedTurns)
}

func TestSummarize_KeepAllButOne(t *testing.T) {
	turns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn(t, 3),
		turn(t, 4),
		turn(t, 5),
		turn(t, 6),
		turn(t, 7),
		turn(t, 8),
		turn(t, 9),
		turn(t, 10),
	}

	keepFirst := 4
	keepLast := 5

	// Expect summarization to be silently skipped
	expectedSummarizedTurns := turns

	testSummarize(t, turns, keepFirst, keepLast, expectedSummarizedTurns)
}

func TestSummarize_NotEnoughTurns(t *testing.T) {
	turns := []ai.ConversationTurn{
		turn(t, 1),
		turn(t, 2),
		turn(t, 3),
		turn(t, 4),
		turn(t, 5),
		turn(t, 6),
		turn(t, 7),
	}

	keepFirst := 2
	keepLast := 10

	// Expect summarization to be silently skipped
	expectedSummarizedTurns := turns

	testSummarize(t, turns, keepFirst, keepLast, expectedSummarizedTurns)
}

func testSummarize(
	t *testing.T,
	originalTurns []ai.ConversationTurn,
	keepFirst int,
	keepLast int,
	expectedTurns []ai.ConversationTurn,
) {
	t.Helper()

	sender := senderStub{
		response: newAnthropicResponse(t, summary),
	}
	history := ai.ConversationHistory{
		SystemPrompt: "some system prompt",
		Messages:     originalTurns,
	}
	model := anthropic.ModelClaudeSonnet4_5
	var maxTokens int64 = 10000
	tools := []anthropic.ToolParam{}

	conversation, err := ai.ResumeConversation(sender, history, model, maxTokens, tools)
	require.NoError(t, err)

	ctx := context.Background()
	err = summarize(ctx, conversation, keepFirst, keepLast)
	require.NoError(t, err)
	for _, turn := range conversation.Turns {
		for _, block := range turn.UserInstructions {
			if block.OfText != nil {
				fmt.Printf("User: %s\n", block.OfText.Text)
			}
		}
		for _, block := range turn.AssistantTextBlocks {
			if block.OfText != nil {
				fmt.Printf("Asst: %s\n", block.OfText.Text)
			}
		}
	}
	require.Equal(t, expectedTurns, conversation.Turns)
}

type senderStub struct {
	response *anthropic.Message
}

func (ss senderStub) SendMessage(_ context.Context, _ anthropic.MessageNewParams, _ ...anthropt.RequestOption) (*anthropic.Message, error) {
	return ss.response, nil
}

// turn creates a conversation turn with fake, hard-coded content
func turn(t *testing.T, n int) ai.ConversationTurn {
	userBlock := anthropic.NewTextBlock(fmt.Sprintf("user message %d", n))
	assistantText := anthropic.NewTextBlock(fmt.Sprintf("response %d", n))
	return ai.ConversationTurn{
		UserInstructions:    []anthropic.ContentBlockParamUnion{userBlock},
		AssistantTextBlocks: []anthropic.ContentBlockParamUnion{assistantText},
		ToolExchanges:       []ai.ToolExchange{},
	}
}

// newAnthropicResponse creates an *anthropic.Message, which is difficult to create otherwise because the SDK only
// intends users to get one by deserializing an API response. newAnthropicResponse is only intended to be used for
// testing; it serializes and deserializes JSON, so it's fairly expensive
func newAnthropicResponse(t *testing.T, content ...anthropic.ContentBlockParamUnion) *anthropic.Message {
	t.Helper()

	requireNoError := func(err error) {
		if t != nil {
			require.NoError(t, err)
		} else if err != nil {
			panic(err)
		}
	}

	messageParam := anthropic.NewAssistantMessage(content...)

	paramJSON, err := json.Marshal(messageParam)
	requireNoError(err)

	var msg anthropic.Message
	err = json.Unmarshal(paramJSON, &msg)
	requireNoError(err)

	return &msg
}
