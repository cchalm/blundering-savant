package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
	anthropt "github.com/anthropics/anthropic-sdk-go/option"
)

type StreamingMessageSender struct {
	client anthropic.Client
}

func NewStreamingMessageSender(client anthropic.Client) StreamingMessageSender {
	return StreamingMessageSender{
		client: client,
	}
}

func (sms StreamingMessageSender) SendMessage(
	ctx context.Context,
	params anthropic.MessageNewParams,
	opts ...anthropt.RequestOption,
) (anthropic.Message, error) {
	stream := sms.client.Messages.NewStreaming(ctx, params)
	response := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		err := response.Accumulate(event)
		if err != nil {
			return anthropic.Message{}, fmt.Errorf("failed to accumulate response content stream: %w", err)
		}
	}
	if stream.Err() != nil {
		return anthropic.Message{}, fmt.Errorf("failed to stream response: %w", stream.Err())
	}
	if response.StopReason == "" {
		b, err := json.Marshal(response)
		if err != nil {
			log.Printf("error while marshalling corrupt message for inspection: %v", err)
		}
		return anthropic.Message{}, fmt.Errorf("malformed message: %v", string(b))
	}

	return response, nil
}
