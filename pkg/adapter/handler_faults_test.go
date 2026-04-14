package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/bubustack/core/contracts"
	cfgpkg "github.com/bubustack/mcp-adapter-engram/pkg/config"
)

type stubMCPClient struct {
	callToolResponses []any
	callToolCalls     int
	listToolsResponse any
}

func (s *stubMCPClient) Initialize(context.Context, map[string]any) (any, error) { return nil, nil }
func (s *stubMCPClient) NotifyInitialized(context.Context) error                 { return nil }
func (s *stubMCPClient) ListTools(context.Context) (any, error) {
	if s.listToolsResponse != nil {
		return s.listToolsResponse, nil
	}
	return map[string]any{"result": map[string]any{"tools": []any{}}}, nil
}
func (s *stubMCPClient) CallTool(context.Context, string, map[string]any) (any, error) {
	if s.callToolCalls >= len(s.callToolResponses) {
		s.callToolCalls++
		return nil, nil
	}
	res := s.callToolResponses[s.callToolCalls]
	s.callToolCalls++
	return res, nil
}
func (s *stubMCPClient) ListResources(context.Context) (any, error) { return nil, nil }
func (s *stubMCPClient) ReadResource(context.Context, string) (any, error) {
	return nil, nil
}
func (s *stubMCPClient) GetPrompt(context.Context, string) (any, error) { return nil, nil }

func TestBuildEnvForRunnerUsesOnlyServerBucket(t *testing.T) {
	t.Setenv(contracts.SecretPrefixEnv+"server", "env:SERVER_")
	t.Setenv("SERVER_TOKEN", "server-token")
	t.Setenv(contracts.SecretPrefixEnv+"other", "env:OTHER_")
	t.Setenv("OTHER_SIDEKEY", "other-token")

	e := &engramImpl{}
	env := e.buildEnvForRunner()

	if got := env["TOKEN"]; got != "server-token" {
		t.Fatalf("expected server TOKEN to be propagated, got %q", got)
	}
	if _, exists := env["SIDEKEY"]; exists {
		t.Fatalf("expected non-server secret key SIDEKEY to be excluded, got %#v", env)
	}
}

func TestIsMCPTransientErrorClassifier(t *testing.T) {
	permanent := map[string]any{
		"result": map[string]any{
			"isError": true,
			"error": map[string]any{
				"message": "validation failed: missing required field",
			},
		},
	}
	if isMCPTransientError(permanent) {
		t.Fatal("expected permanent error to be non-retryable")
	}

	transient := map[string]any{
		"result": map[string]any{
			"isError": true,
			"error": map[string]any{
				"message": "not logged in yet, try again",
			},
		},
	}
	if !isMCPTransientError(transient) {
		t.Fatal("expected transient error to be retryable")
	}
}

func TestCallToolWithRetryRetriesOnlyTransientErrors(t *testing.T) {
	client := &stubMCPClient{
		callToolResponses: []any{
			map[string]any{
				"result": map[string]any{
					"isError": true,
					"error": map[string]any{
						"message": "not logged in yet, try again",
					},
				},
			},
			map[string]any{
				"result": map[string]any{
					"isError": false,
					"content": []any{
						map[string]any{"type": "text", "text": `{"ok":true}`},
					},
				},
			},
		},
	}
	e := &engramImpl{
		client: client,
		cfg: cfgpkg.Config{
			MCP: cfgpkg.MCPInitConfig{
				CallToolRetries:    2,
				CallToolRetryDelay: time.Millisecond,
			},
		},
	}

	result, err := e.callToolWithRetry(context.Background(), Inputs{
		Action:    "callTool",
		Tool:      "demo",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("callToolWithRetry returned error: %v", err)
	}
	if client.callToolCalls != 2 {
		t.Fatalf("expected 2 callTool attempts, got %d", client.callToolCalls)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	inner, _ := got["result"].(map[string]any)
	if isErr, _ := inner["isError"].(bool); isErr {
		t.Fatalf("expected final result to be non-error, got %#v", result)
	}
}

func TestCallToolWithRetryDoesNotRetryPermanentMCPError(t *testing.T) {
	client := &stubMCPClient{
		callToolResponses: []any{
			map[string]any{
				"result": map[string]any{
					"isError": true,
					"error": map[string]any{
						"message": "validation failed: unsupported parameter",
					},
				},
			},
		},
	}
	e := &engramImpl{
		client: client,
		cfg: cfgpkg.Config{
			MCP: cfgpkg.MCPInitConfig{
				CallToolRetries:    3,
				CallToolRetryDelay: time.Millisecond,
			},
		},
	}

	_, err := e.callToolWithRetry(context.Background(), Inputs{
		Action:    "callTool",
		Tool:      "demo",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("callToolWithRetry returned error: %v", err)
	}
	if client.callToolCalls != 1 {
		t.Fatalf("expected exactly 1 callTool attempt for permanent errors, got %d", client.callToolCalls)
	}
}

func TestProcessReturnsDataEnvelopeContract(t *testing.T) {
	e := &engramImpl{
		client: &stubMCPClient{
			listToolsResponse: map[string]any{
				"result": map[string]any{
					"tools": []any{},
				},
			},
		},
	}
	res, err := e.Process(context.Background(), nil, Inputs{Action: "listTools"})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	payload, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected result payload map, got %T", res.Data)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected result envelope under data, got %#v", payload)
	}
	if _, ok := data["result"]; !ok {
		t.Fatalf("expected data.result field, got %#v", data)
	}
}
