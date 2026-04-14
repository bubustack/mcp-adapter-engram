package mcp

import "context"

// Client abstracts MCP operations used by the adapter.
type Client interface {
	Initialize(ctx context.Context, capabilities map[string]any) (any, error)
	NotifyInitialized(ctx context.Context) error
	ListTools(ctx context.Context) (any, error)
	CallTool(ctx context.Context, tool string, args map[string]any) (any, error)
	ListResources(ctx context.Context) (any, error)
	ReadResource(ctx context.Context, uri string) (any, error)
	GetPrompt(ctx context.Context, name string) (any, error)
}
