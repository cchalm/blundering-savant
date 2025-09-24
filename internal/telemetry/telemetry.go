package telemetry

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"
	otelTrace "go.opentelemetry.io/otel/trace"
)

const (
	serviceName    = "blundering-savant"
	serviceVersion = "1.0.0" // TODO: Get this from build info or environment
	BotVersion     = "1.0.0" // Bot version for telemetry
)

// TelemetryConfig holds the configuration for telemetry
type TelemetryConfig struct {
	Enabled        bool
	JaegerEndpoint string
}

// Provider manages the OpenTelemetry trace provider
type Provider struct {
	provider *trace.TracerProvider
	tracer   otelTrace.Tracer
	enabled  bool
}

// NewProvider creates a new telemetry provider
func NewProvider(ctx context.Context, config TelemetryConfig) (*Provider, error) {
	if !config.Enabled {
		log.Printf("Telemetry disabled")
		return &Provider{enabled: false}, nil
	}

	log.Printf("Initializing telemetry with Jaeger endpoint: %s", config.JaegerEndpoint)

	// Create OTLP HTTP exporter
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(config.JaegerEndpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create trace provider
	provider := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
	)

	// Set as global provider
	otel.SetTracerProvider(provider)

	return &Provider{
		provider: provider,
		tracer:   provider.Tracer(serviceName),
		enabled:  true,
	}, nil
}

// Shutdown shuts down the telemetry provider
func (p *Provider) Shutdown(ctx context.Context) error {
	if !p.enabled || p.provider == nil {
		return nil
	}
	return p.provider.Shutdown(ctx)
}

// ConversationTelemetry holds telemetry data for a conversation
type ConversationTelemetry struct {
	ConversationID string
	BotVersion     string
	TurnIndex      int
}

// TurnTelemetry holds telemetry data for a conversation turn
type TurnTelemetry struct {
	TurnID     string
	TurnIndex  int
	TokenUsage TokenUsage
}

// TokenUsage represents token usage metrics
type TokenUsage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// ToolUseTelemetry holds telemetry data for a tool use
type ToolUseTelemetry struct {
	ToolName       string
	ToolUseSize    int
	ToolResultSize int
	HasError       bool
	ConversationID string
	TurnID         string
	TurnIndex      int
	BotVersion     string
}

// RecordToolUse records a tool use event
func (p *Provider) RecordToolUse(ctx context.Context, toolUse ToolUseTelemetry) {
	if !p.enabled {
		return
	}

	_, span := p.tracer.Start(ctx, "tool_use")
	defer span.End()

	span.SetAttributes(
		attribute.String("tool.name", toolUse.ToolName),
		attribute.Int("tool.use_size", toolUse.ToolUseSize),
		attribute.Int("tool.result_size", toolUse.ToolResultSize),
		attribute.Bool("tool.has_error", toolUse.HasError),
		attribute.String("conversation.id", toolUse.ConversationID),
		attribute.String("turn.id", toolUse.TurnID),
		attribute.Int("turn.index", toolUse.TurnIndex),
		attribute.String("bot.version", toolUse.BotVersion),
	)
}

// TransformToolName transforms a tool name based on its inputs (e.g., for str_replace_based_edit_tool)
func TransformToolName(toolName string, toolInput map[string]interface{}) string {
	if toolName == "str_replace_based_edit_tool" {
		if command, ok := toolInput["command"].(string); ok && command != "" {
			return fmt.Sprintf("%s[%s]", toolName, command)
		}
	}
	return toolName
}

// NewConversationID generates a new conversation UUID
func NewConversationID() string {
	return uuid.New().String()
}

// NewTurnID generates a new turn UUID
func NewTurnID() string {
	return uuid.New().String()
}
