package adapter

import (
	"context"
	"log/slog"

	sdk "github.com/bubustack/bubu-sdk-go"
)

func (e *engramImpl) debugEnabled(ctx context.Context, logger *slog.Logger) bool {
	if sdk.DebugModeEnabled() {
		return true
	}
	if logger == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return logger.Enabled(ctx, slog.LevelDebug)
}

func (e *engramImpl) logAdapterRequest(ctx context.Context, logger *slog.Logger, in Inputs) {
	if !e.debugEnabled(ctx, logger) {
		return
	}
	attrs := []any{slog.String("action", in.Action)}
	if in.Tool != "" {
		attrs = append(attrs, slog.String("tool", in.Tool))
	}
	if len(in.Arguments) > 0 {
		attrs = append(attrs, slog.Any("arguments", in.Arguments))
	}
	if in.ResourceURI != "" {
		attrs = append(attrs, slog.String("resourceURI", in.ResourceURI))
	}
	if in.PromptName != "" {
		attrs = append(attrs, slog.String("promptName", in.PromptName))
	}
	if in.Server != "" {
		attrs = append(attrs, slog.String("server", in.Server))
	}
	logger.Debug("MCP adapter request", attrs...)
}

func (e *engramImpl) logAdapterResponse(ctx context.Context, logger *slog.Logger, data map[string]any) {
	if !e.debugEnabled(ctx, logger) {
		return
	}
	logger.Debug("MCP adapter response", slog.Any("data", data))
}
