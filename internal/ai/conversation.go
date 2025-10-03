package ai

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/anthropics/anthropic-sdk-go"
	anthropt "github.com/anthropics/anthropic-sdk-go/option"
)

type MessageSender interface {
	SendMessage(ctx context.Context, params anthropic.MessageNewParams, opts ...anthropt.RequestOption) (anthropic.Message, error)
}

// OutputFilter is a function that can modify conversation turns before they are sent to the AI.
// It receives all turns and returns a modified slice of turns. This is useful for temporarily
// suppressing verbose content (like all but the latest validation results) while keeping the full
// history intact for forking and other operations.
type OutputFilter func(turns []ConversationTurnParam) []ConversationTurnParam

type Conversation struct {
	Turns []ConversationTurn

	sender MessageSender

	model           anthropic.Model
	systemPrompt    string
	tools           []anthropic.ToolParam
	maxOutputTokens int64 // Maximum number of output tokens per response

	outputFilter OutputFilter // Optional filter to modify history before sending to AI
}

// ConversationTurn represents user instructions, assistant response, and resolved tool uses as a single unit
type ConversationTurn struct {
	Instructions  []anthropic.ContentBlockParamUnion
	Response      anthropic.Message
	ToolExchanges []ToolExchange
}

// ConversationTurnParam is a ConversationTurn as it will appear in API requests (as opposed to responses)
type ConversationTurnParam struct {
	Instructions  []anthropic.ContentBlockParamUnion
	Response      anthropic.MessageParam
	ToolExchanges []ToolExchangeParam
}

func (ct ConversationTurn) ToParam() (ConversationTurnParam, error) {
	var toolExchanges []ToolExchangeParam
	for _, toolExchange := range ct.ToolExchanges {
		toolExchangeParam, err := toolExchange.ToParam()
		if err != nil {
			return ConversationTurnParam{}, err
		}
		toolExchanges = append(toolExchanges, toolExchangeParam)
	}
	return ConversationTurnParam{
		Instructions:  ct.Instructions,
		Response:      ct.Response.ToParam(),
		ToolExchanges: toolExchanges,
	}, nil
}

// ToolExchange represents a tool use and its result
type ToolExchange struct {
	UseBlock    anthropic.ToolUseBlock
	ResultBlock *anthropic.ToolResultBlockParam
}

// ToolExchangeParam is a ToolExchange as it will appear in API requests (as opposed to responses)
type ToolExchangeParam struct {
	UseBlock    anthropic.ToolUseBlockParam
	ResultBlock anthropic.ToolResultBlockParam
}

func (te ToolExchange) ToParam() (ToolExchangeParam, error) {
	if te.ResultBlock == nil {
		return ToolExchangeParam{}, fmt.Errorf("cannot create ToolExchangeParam from ToolExchange with nil ResultBlock")
	}
	return ToolExchangeParam{
		UseBlock:    te.UseBlock.ToParam(),
		ResultBlock: *te.ResultBlock,
	}, nil
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
func (cc *Conversation) SendMessage(ctx context.Context, instructions ...anthropic.ContentBlockParamUnion) (anthropic.Message, error) {
	return cc.sendMessage(ctx, true, instructions...)
}

// ResendLastMessage erases the last turn in the conversation history and resends the user message in that turn
func (cc *Conversation) ResendLastMessage(ctx context.Context) (anthropic.Message, error) {
	if len(cc.Turns) == 0 {
		return anthropic.Message{}, fmt.Errorf("cannot resend last message: no messages")
	}

	var lastTurn ConversationTurn
	// Pop the last message off of the conversation history
	lastTurn, cc.Turns = cc.Turns[len(cc.Turns)-1], cc.Turns[:len(cc.Turns)-1]

	return cc.SendMessage(ctx, lastTurn.Instructions...)
}

// sendMessage is the internal implementation with a boolean parameter to specify caching
func (cc *Conversation) sendMessage(ctx context.Context, enableCache bool, instructions ...anthropic.ContentBlockParamUnion) (anthropic.Message, error) {
	// Safeguard: prevent tool results from being passed as instructions
	for _, instruction := range instructions {
		if instruction.OfToolResult != nil {
			return anthropic.Message{}, fmt.Errorf("tool results must not be passed as instructions; use AddToolResult instead")
		}
	}

	var turnParams []ConversationTurnParam
	for i, turn := range cc.Turns {
		turnParam, err := turn.ToParam()
		if err != nil {
			return anthropic.Message{}, fmt.Errorf("failed to convert turn %d to param: %w", i, err)
		}
		turnParams = append(turnParams, turnParam)
	}
	turnParams = append(turnParams, ConversationTurnParam{
		Instructions: instructions,
	})

	// Apply output filter if set
	if cc.outputFilter != nil {
		turnParams = cc.outputFilter(turnParams)
	}

	messages, err := convertTurnParamsToMessages(turnParams)
	if err != nil {
		return anthropic.Message{}, fmt.Errorf("failed to convert turns to messages: %w", err)
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
		return anthropic.Message{}, err
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

// SetOutputFilter sets a filter function that will be applied to conversation turns before sending to the AI.
// The filter receives all turns and can analyze and modify them (e.g., suppress verbose tool results).
// The original conversation history is not modified, only the messages sent to the AI.
func (cc *Conversation) SetOutputFilter(filter OutputFilter) {
	cc.outputFilter = filter
}

// Fork returns a new conversation with the same history as this one up to but not including the turn at the given
// index. E.g. if the given index is 3, the forked conversation's history will be turns 0, 1, and 2
func (cc Conversation) Fork(turnIndex int) (*Conversation, error) {
	if turnIndex > len(cc.Turns) {
		return nil, fmt.Errorf("turnIndex is %d, but there are only %d turns in the conversation", turnIndex, len(cc.Turns))
	}
	cc.Turns = slices.Clone(cc.Turns[:turnIndex])
	return &cc, nil
}

func buildToolExchangesFromResponse(response anthropic.Message) []ToolExchange {
	toolExchanges := []ToolExchange{}
	for _, block := range response.Content {
		if toolUseBlock, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
			toolExchanges = append(toolExchanges, ToolExchange{UseBlock: toolUseBlock})
		}
	}
	return toolExchanges
}

// cloneTurns creates a deep copy of conversation turns to prevent filters from modifying the original history
func cloneTurns(turns []ConversationTurn) []ConversationTurn {
	cloned := make([]ConversationTurn, len(turns))
	for i, turn := range turns {
		clonedTurn := ConversationTurn{
			Response: turn.Response, // Response is immutable (from API), no need to clone
		}

		// Clone Instructions slice
		if turn.Instructions != nil {
			clonedTurn.Instructions = make([]anthropic.ContentBlockParamUnion, len(turn.Instructions))
			copy(clonedTurn.Instructions, turn.Instructions)
		}

		// Clone ToolExchanges slice and its elements
		if turn.ToolExchanges != nil {
			clonedTurn.ToolExchanges = make([]ToolExchange, len(turn.ToolExchanges))
			for j, exchange := range turn.ToolExchanges {
				clonedExchange := ToolExchange{
					UseBlock: exchange.UseBlock, // UseBlock is immutable (from API response)
				}
				// Clone ResultBlock if present
				if exchange.ResultBlock != nil {
					resultCopy := *exchange.ResultBlock
					clonedExchange.ResultBlock = &resultCopy
				}
				clonedTurn.ToolExchanges[j] = clonedExchange
			}
		}

		cloned[i] = clonedTurn
	}
	return cloned
}

func convertTurnParamsToMessages(turns []ConversationTurnParam) ([]anthropic.MessageParam, error) {
	messages := []anthropic.MessageParam{}

	// Convert conversation turns into API messages
	var previousTurn *ConversationTurnParam
	for _, turn := range turns {
		userBlocks := []anthropic.ContentBlockParamUnion{}
		// Start the message with tool results from the previous turn
		if previousTurn != nil {
			for _, toolExchange := range previousTurn.ToolExchanges {
				userBlocks = append(userBlocks, anthropic.ContentBlockParamUnion{OfToolResult: &toolExchange.ResultBlock})
			}
		}
		// Add current turn's instructions
		userBlocks = append(userBlocks, turn.Instructions...)

		// We're done with the user message part of the turn
		messages = append(messages, anthropic.NewUserMessage(userBlocks...))

		// Add the assistant response part of the turn
		messages = append(messages, turn.Response)

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
