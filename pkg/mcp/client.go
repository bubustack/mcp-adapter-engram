package mcp

import "context"

// Initialize is exposed by the HTTP client via NewHTTPClient; kept here for parity when adding transports.
func Initialize(ctx context.Context, c Client, capabilities map[string]any) (any, error) {
	return c.Initialize(ctx, capabilities)
}

// buildInitParams constructs MCP-spec-compliant initialize params.
// The MCP spec requires protocolVersion, capabilities (non-null), and clientInfo.
func buildInitParams(capabilities map[string]any) map[string]any {
	if capabilities == nil {
		capabilities = map[string]any{}
	}
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    capabilities,
		"clientInfo": map[string]any{
			"name":    "bubustack-mcp-adapter",
			"version": "1.0.0",
		},
	}
}
