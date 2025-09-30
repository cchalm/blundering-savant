package ai

import (
	"context"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
	anthropt "github.com/anthropics/anthropic-sdk-go/option"
)

type MessageSender interface {
	SendMessage(ctx context.Context, params anthropic.MessageNewParams, opts ...anthropt.RequestOption) (*anthropic.Message, error)
}

type Conversation struct {
	Turns []ConversationTurn

	sender MessageSender

	model           anthropic.Model
	systemPrompt    string
	tools           []anthropic.ToolParam
	maxOutputTokens int64 // Maximum number of output tokens per response
}

// ConversationTurn represents user instructions, assistant response, and resolved tool uses as a single unit
type ConversationTurn struct {
	Instructions  []anthropic.ContentBlockParamUnion
	Response      *anthropic.Message // May be nil
	ToolExchanges []ToolExchange
}

type ToolExchange struct {
	UseBlock    anthropic.ToolUseBlock
	ResultBlock *anthropic.ToolResultBlockParam
}

func NewConversation(
	sender MessageSender,
	model anthropic.Model,
	maxOutputTokens int64,
	tools []anthropic.ToolParam,
	systemPrompt string,
) *Conversation {

	return &Conversation{
		sender: sender,

		model:        model,
		systemPrompt: systemPrompt,
		tools:        tools,

		maxOutputTokens: maxOutputTokens,
	}
}

func ResumeConversation(
	sender MessageSender,
	history ConversationHistory,
	model anthropic.Model,
	maxOutputTokens int64,
	tools []anthropic.ToolParam,
) (*Conversation, error) {
	c := &Conversation{
		sender: sender,

		model:        model,
		systemPrompt: history.SystemPrompt,
		tools:        tools,
		Turns:        history.Turns,

		maxOutputTokens: maxOutputTokens,
	}
	return c, nil
}

// SendMessage sends the last turn's tool results and optional supplemental instructions to the AI, awaits its response,
// and adds both to the conversation as a new turn
func (cc *Conversation) SendMessage(ctx context.Context, instructions ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	return cc.sendMessage(ctx, true, instructions...)
}

// ResendLastMessage erases the last turn in the conversation history and resends the user message in that turn
func (cc *Conversation) ResendLastMessage(ctx context.Context) (*anthropic.Message, error) {
	if len(cc.Turns) == 0 {
		return nil, fmt.Errorf("cannot resend last message: no messages")
	}

	var lastTurn ConversationTurn
	// Pop the last message off of the conversation history
	lastTurn, cc.Turns = cc.Turns[len(cc.Turns)-1], cc.Turns[:len(cc.Turns)-1]

	return cc.SendMessage(ctx, lastTurn.Instructions...)
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *Conversation) sendMessage(ctx context.Context, enableCache bool, instructions ...anthropic.ContentBlockParamUnion) (*anthropic.Message, error) {
	messages, err := convertTurnsToMessages(append(cc.Turns, ConversationTurn{Instructions: instructions}))
	if err != nil {
		return nil, fmt.Errorf("failed to convert turns to messages: %w", err)
	}

	// Set cache point only if caching is enabled
	var cacheControl *anthropic.CacheControlEphemeralParam
	if enableCache {
		// Always set a cache point. Unsupported cache points, e.g. on content that is below the minimum length for caching,
		// will be ignored
		var err error
		cacheControl, err = getLastCacheControl(messages)
		if err != nil {
			log.Printf("Warning: failed to set cache point: %s", err)
		} else {
			*cacheControl = anthropic.NewCacheControlEphemeralParam()
		}
	}

	params := anthropic.MessageNewParams{
		Model:     cc.model,
		MaxTokens: cc.maxOutputTokens,
		System: []anthropic.TextBlockParam{
			{
				Text: cc.systemPrompt,
				// Always cache the system prompt, which will be the same for each iteration of this conversation _and_
				// will be the same for other conversations by this bot
				// Actually, currently the system prompt is relatively small, so let's save the cache points for later
				// CacheControl: anthropic.NewCacheControlEphemeralParam(),
			},
		},
		Messages: messages,
	}

	toolParams := []anthropic.ToolUnionParam{}
	for _, tool := range cc.tools {
		toolParams = append(toolParams, anthropic.ToolUnionParam{
			OfTool: &tool,
		})
	}
	params.Tools = toolParams

	response, err := cc.sender.SendMessage(ctx, params)
	if err != nil {
		return nil, err
	}

	log.Printf("Token usage - Input: %d, Cache create: %d, Cache read: %d, Total: %d",
		response.Usage.InputTokens,
		response.Usage.CacheCreationInputTokens,
		response.Usage.CacheReadInputTokens,
		response.Usage.InputTokens+response.Usage.CacheCreationInputTokens+response.Usage.CacheReadInputTokens,
	)

	// Record the turn
	cc.Turns = append(cc.Turns, ConversationTurn{
		Instructions:  instructions,
		Response:      response,
		ToolExchanges: buildToolExchangesFromResponse(response),
	})

	if cacheControl != nil {
		// Remove the cache control element from the conversation history. Anthropic's automatic prefix checking should
		// cause previously-cached blocks to be read from the cache without explicitly marking them for cache control
		*cacheControl = anthropic.CacheControlEphemeralParam{}
	}

	return response, nil
}

func (cc *Conversation) GetPendingToolUses() []anthropic.ToolUseBlock {
	if len(cc.Turns) == 0 {
		return nil
	}

	lastTurn := &cc.Turns[len(cc.Turns)-1]
	if lastTurn.Response == nil {
		return nil
	}

	var pending []anthropic.ToolUseBlock
	for _, toolExchange := range lastTurn.ToolExchanges {
		if toolExchange.ResultBlock == nil {
			pending = append(pending, toolExchange.UseBlock)
		}
	}

	return pending
}

func (cc *Conversation) AddToolResult(result anthropic.ToolResultBlockParam) error {
	if len(cc.Turns) == 0 {
		return fmt.Errorf("conversation has no turns")
	}

	lastTurn := cc.Turns[len(cc.Turns)-1]
	for i, toolExchange := range lastTurn.ToolExchanges {
		if toolExchange.UseBlock.ID == result.ToolUseID {
			lastTurn.ToolExchanges[i].ResultBlock = &result
			return nil
		}
	}

	return fmt.Errorf("tool use block with ID '%s' not found", result.ToolUseID)
}

func buildToolExchangesFromResponse(response *anthropic.Message) []ToolExchange {
	toolExchanges := []ToolExchange{}
	for _, block := range response.Content {
		if toolUseBlock, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			toolExchanges = append(toolExchanges, ToolExchange{UseBlock: toolUseBlock})
		}
	}
	return toolExchanges
}

func convertTurnsToMessages(turns []ConversationTurn) ([]anthropic.MessageParam, error) {
	messages := []anthropic.MessageParam{}

	// Convert conversation turns into API messages
	var previousTurn *ConversationTurn
	for _, turn := range turns {
		userBlocks := []anthropic.ContentBlockParamUnion{}
		// Start the message with tool results from the previous turn
		if previousTurn != nil {
			for _, toolExchange := range previousTurn.ToolExchanges {
				if toolExchange.ResultBlock == nil {
					return nil, fmt.Errorf("no result added for tool use '%s' (%s)", toolExchange.UseBlock.ID, toolExchange.UseBlock.Name)
				}
				userBlocks = append(userBlocks, anthropic.ContentBlockParamUnion{OfToolResult: toolExchange.ResultBlock})
			}
		}
		// Add current turn's instructions
		userBlocks = append(userBlocks, turn.Instructions...)

		// We're done with the user message part of the turn
		messages = append(messages, anthropic.NewUserMessage(userBlocks...))

		// Add the assistant response part of the turn, if populated
		if turn.Response != nil {
			messages = append(messages, turn.Response.ToParam())
		}

		previousTurn = &turn
	}

	return messages, nil
}

func getLastCacheControl(messages []anthropic.MessageParam) (*anthropic.CacheControlEphemeralParam, error) {
	for _, message := range messages {
		content := message.Content
		for i := len(content) - 1; i >= 0; i-- {
			c := content[i]
			if cacheControl := c.GetCacheControl(); cacheControl != nil {
				return cacheControl, nil
			}
		}
	}

	return nil, fmt.Errorf("no cacheable blocks in content")
}

// ConversationHistory contains a serializable and resumable snapshot of a Conversation
type ConversationHistory struct {
	SystemPrompt string             `json:"systemPrompt"`
	Turns        []ConversationTurn `json:"turns"`
}

// History returns a serializable conversation history
func (cc *Conversation) History() ConversationHistory {
	return ConversationHistory{
		SystemPrompt: cc.systemPrompt,
		Turns:        cc.Turns,
	}
}
