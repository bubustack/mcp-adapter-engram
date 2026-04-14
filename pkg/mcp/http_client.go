package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// httpClient implements MCP Streamable HTTP transport against a single MCP endpoint.
// It adheres to the MCP spec:
//   - All client messages are POSTed to a single endpoint (baseURL)
//   - Server may return a single JSON object or open an SSE stream
//   - SSE frames are returned verbatim to callers (as []any)
//   - A per-session Mcp-Session-Id header is set for resumability/session scoping
//     (clients must still handle reconnection out-of-band; resumability is server-defined)
type httpClient struct {
	baseURL   string
	headers   map[string]string
	client    *http.Client
	sessionID string
}

// NewHTTPClient creates a spec-correct Streamable HTTP client.
// Accept includes both application/json and text/event-stream as required by the spec.
// Callers may pass static headers (e.g. Authorization) which are merged with secret-derived headers.
func NewHTTPClient(
	baseURL string,
	headersFromSecrets map[string]string,
	staticHeaders map[string]string,
) (Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	h := map[string]string{
		"Content-Type": "application/json",
		// Accept both JSON and SSE per MCP Streamable HTTP
		"Accept": "application/json, text/event-stream",
	}
	maps.Copy(h, headersFromSecrets)
	maps.Copy(h, staticHeaders)
	sid := uuid.NewString()
	h["Mcp-Session-Id"] = sid
	return &httpClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		headers:   h,
		client:    &http.Client{Timeout: 0},
		sessionID: sid,
	}, nil
}

// Initialize sends an MCP initialize request. Result is returned verbatim.
func (c *httpClient) Initialize(ctx context.Context, capabilities map[string]any) (any, error) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      uuid.NewString(),
		Method:  "initialize",
		Params:  buildInitParams(capabilities),
	}
	return c.postJSONRPCMaybeStream(ctx, req)
}

// NotifyInitialized sends the notifications/initialized notification.
func (c *httpClient) NotifyInitialized(ctx context.Context) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	_, err := c.postJSONRPCMaybeStream(ctx, req)
	return err
}

// ListTools requests the server's tools list and returns the raw payload.
func (c *httpClient) ListTools(ctx context.Context) (any, error) {
	req := jsonRPCRequest{JSONRPC: "2.0", ID: uuid.NewString(), Method: "tools/list"}
	return c.postJSONRPCMaybeStream(ctx, req)
}

// CallTool invokes a tool by name with arguments. Payload is returned verbatim.
func (c *httpClient) CallTool(ctx context.Context, tool string, args map[string]any) (any, error) {
	if tool == "" {
		return nil, fmt.Errorf("tool is required")
	}
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      uuid.NewString(),
		Method:  "tools/call",
		Params:  map[string]any{"name": tool, "arguments": args},
	}
	return c.postJSONRPCMaybeStream(ctx, req)
}

// ListResources returns the server's resources description.
func (c *httpClient) ListResources(ctx context.Context) (any, error) {
	req := jsonRPCRequest{JSONRPC: "2.0", ID: uuid.NewString(), Method: "resources/list"}
	return c.postJSONRPCMaybeStream(ctx, req)
}

// ReadResource fetches resource content by URI.
func (c *httpClient) ReadResource(ctx context.Context, uri string) (any, error) {
	if uri == "" {
		return nil, fmt.Errorf("resource uri is required")
	}
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      uuid.NewString(),
		Method:  "resources/read",
		Params:  map[string]any{"uri": uri},
	}
	return c.postJSONRPCMaybeStream(ctx, req)
}

// GetPrompt fetches a named prompt; raw payload is returned.
func (c *httpClient) GetPrompt(ctx context.Context, name string) (any, error) {
	if name == "" {
		return nil, fmt.Errorf("prompt name is required")
	}
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      uuid.NewString(),
		Method:  "prompts/get",
		Params:  map[string]any{"name": name},
	}
	return c.postJSONRPCMaybeStream(ctx, req)
}

// postJSONRPCMaybeStream POSTs a JSON-RPC request and supports both JSON and SSE responses.
// SSE frames are accumulated and returned as []any, keeping payloads verbatim.
func (c *httpClient) postJSONRPCMaybeStream(ctx context.Context, rpcReq jsonRPCRequest) (any, error) {
	b, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
			return nil, fmt.Errorf("mcp http request failed: status %d: %s", resp.StatusCode, trimmed)
		}
		return nil, fmt.Errorf("mcp http request failed: status %d", resp.StatusCode)
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return decodeSSEFrames(ctx, resp.Body)
	}
	return decodeJSONOrText(resp.Body)
}

func decodeSSEFrames(ctx context.Context, body io.Reader) ([]any, error) {
	dec := newSSEDecoder(body)
	var frames []any
	for {
		ev, err := dec.NextEvent(ctx)
		if errors.Is(err, io.EOF) {
			return frames, nil
		}
		if err != nil {
			return frames, err
		}
		if len(ev.Data) == 0 {
			continue
		}
		var anyJSON any
		if err := json.Unmarshal(ev.Data, &anyJSON); err == nil {
			frames = append(frames, anyJSON)
			continue
		}
		frames = append(frames, string(ev.Data))
	}
}

func decodeJSONOrText(body io.Reader) (any, error) {
	respBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if len(respBytes) == 0 {
		return nil, nil
	}
	var anyJSON any
	if err := json.Unmarshal(respBytes, &anyJSON); err == nil {
		return anyJSON, nil
	}
	return string(respBytes), nil
}

// sseDecoder is a tiny reader for text/event-stream that collates multi-line data fields.
type sseDecoder struct{ r *bufio.Reader }

type sseEvent struct {
	ID   string
	Data []byte
}

func newSSEDecoder(body io.Reader) *sseDecoder { return &sseDecoder{r: bufio.NewReader(body)} }

// NextEvent reads until a blank line, aggregating data: fields; ignores comment/retry lines.
func (d *sseDecoder) NextEvent(ctx context.Context) (*sseEvent, error) {
	var id string
	var dataBuf bytes.Buffer
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if time.Now().After(deadline) && dataBuf.Len() == 0 {
			deadline = time.Now().Add(30 * time.Second)
		}
		line, err := d.r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if dataBuf.Len() > 0 {
					return &sseEvent{ID: id, Data: bytes.TrimRight(dataBuf.Bytes(), "\n")}, nil
				}
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if dataBuf.Len() == 0 && id == "" {
				continue
			}
			return &sseEvent{ID: id, Data: bytes.TrimRight(dataBuf.Bytes(), "\n")}, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "id:") {
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload != "" {
				dataBuf.WriteString(payload)
				dataBuf.WriteByte('\n')
			}
			continue
		}
	}
}
