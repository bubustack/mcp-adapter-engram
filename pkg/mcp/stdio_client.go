package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkk8s "github.com/bubustack/bubu-sdk-go/k8s"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// StdioOptions defines how to connect to the runner pod's stdio.
type StdioOptions struct {
	Namespace string
	PodName   string
	Container string
}

// stdioClient implements Client by attaching to a runner Pod via Pods/Attach and speaking newline-delimited JSON-RPC.
type stdioClient struct {
	opts     StdioOptions
	cfg      *rest.Config
	kclient  *kubernetes.Clientset
	writer   io.WriteCloser
	reader   *bufio.Reader
	mu       sync.Mutex
	attachMu sync.Mutex
}

func NewStdioClient(ctx context.Context, opts StdioOptions) (Client, error) {
	if opts.Namespace == "" || opts.PodName == "" || opts.Container == "" {
		return nil, fmt.Errorf("namespace, podName and container are required")
	}
	cfg, err := sdkk8s.GetConfig()
	if err != nil {
		return nil, err
	}
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	c := &stdioClient{opts: opts, cfg: cfg, kclient: kc}
	if err := c.attach(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *stdioClient) attach(ctx context.Context) error {
	c.attachMu.Lock()
	defer c.attachMu.Unlock()
	req := c.kclient.CoreV1().RESTClient().Post().Resource("pods").Namespace(c.opts.Namespace).
		Name(c.opts.PodName).SubResource("attach")
	req.VersionedParams(&corev1.PodAttachOptions{
		Container: c.opts.Container,
		Stdin:     true,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.cfg, http.MethodPost, req.URL())
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	er := nopWriter{}
	stdoutReader, stdoutWriter := io.Pipe()

	go func() {
		_ = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:  pr,
			Stdout: stdoutWriter,
			Stderr: er,
			Tty:    false,
		})
		_ = stdoutWriter.Close()
	}()

	c.writer = pw
	c.reader = bufio.NewReader(stdoutReader)
	return nil
}

type nopWriter struct{}

func (n nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// Client impl: send JSON-RPC newline-delimited; read single-line JSON replies.

func (c *stdioClient) Initialize(ctx context.Context, capabilities map[string]any) (any, error) {
	return c.rpc(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "initialize",
		Params:  buildInitParams(capabilities),
	})
}

// NotifyInitialized sends the notifications/initialized notification (fire-and-forget, no response expected).
func (c *stdioClient) NotifyInitialized(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	if err != nil {
		return err
	}
	_, err = c.writer.Write(append(b, '\n'))
	return err
}

func (c *stdioClient) ListTools(ctx context.Context) (any, error) {
	return c.rpc(ctx, jsonRPCRequest{JSONRPC: "2.0", ID: newID(), Method: "tools/list"})
}

func (c *stdioClient) CallTool(ctx context.Context, tool string, args map[string]any) (any, error) {
	if strings.TrimSpace(tool) == "" {
		return nil, fmt.Errorf("tool is required")
	}
	return c.rpc(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "tools/call",
		Params:  map[string]any{"name": tool, "arguments": args},
	})
}

func (c *stdioClient) ListResources(ctx context.Context) (any, error) {
	return c.rpc(ctx, jsonRPCRequest{JSONRPC: "2.0", ID: newID(), Method: "resources/list"})
}

func (c *stdioClient) ReadResource(ctx context.Context, uri string) (any, error) {
	if strings.TrimSpace(uri) == "" {
		return nil, fmt.Errorf("uri is required")
	}
	return c.rpc(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "resources/read",
		Params:  map[string]any{"uri": uri},
	})
}

func (c *stdioClient) GetPrompt(ctx context.Context, name string) (any, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	return c.rpc(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      newID(),
		Method:  "prompts/get",
		Params:  map[string]any{"name": name},
	})
}

func (c *stdioClient) rpc(ctx context.Context, req jsonRPCRequest) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	// Retry send+receive on disconnect, keeping the same JSON-RPC id for server-side deduplication.
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := c.writer.Write(append(b, '\n')); err != nil {
			lastErr = fmt.Errorf("stdio write failed: %w", err)
			_ = c.reattach(ctx)
			// backoff before retry
			if attempt < maxAttempts-1 {
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}
		resp, err := c.readResponse(ctx, req.ID)
		if err != nil {
			lastErr = fmt.Errorf("stdio read failed: %w", err)
			_ = c.reattach(ctx)
			if attempt < maxAttempts-1 {
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("stdio rpc failed after %d attempts: %w", maxAttempts, lastErr)
}

// readResponse reads lines from the MCP server, skipping JSON-RPC notifications
// and responses for other request IDs until the requested reqID is found.
func (c *stdioClient) readResponse(ctx context.Context, reqID string) (any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			continue
		}
		var anyJSON any
		if err := json.Unmarshal(line, &anyJSON); err != nil {
			return nil, fmt.Errorf("invalid JSON-RPC response: %w", err)
		}
		msg, ok := anyJSON.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid JSON-RPC response: expected object")
		}
		// Notifications have "method" and no "id".
		if _, hasMethod := msg["method"]; hasMethod {
			if _, hasID := msg["id"]; !hasID {
				continue
			}
		}
		if reqID != "" {
			idVal, hasID := msg["id"]
			if !hasID {
				continue
			}
			if !jsonRPCIDMatches(reqID, idVal) {
				continue
			}
		}
		return anyJSON, nil
	}
}

func jsonRPCIDMatches(expected string, id any) bool {
	switch v := id.(type) {
	case string:
		return v == expected
	case float64:
		if parsed, err := strconv.ParseFloat(expected, 64); err == nil {
			return v == parsed
		}
	case int:
		if parsed, err := strconv.Atoi(expected); err == nil {
			return v == parsed
		}
	case int64:
		if parsed, err := strconv.ParseInt(expected, 10, 64); err == nil {
			return v == parsed
		}
	case json.Number:
		if parsed, err := strconv.ParseFloat(expected, 64); err == nil {
			if n, err := v.Float64(); err == nil {
				return n == parsed
			}
		}
	}
	return false
}

func newID() string { return uuid.NewString() }

// reattach attempts to re-establish the attach stream after a disconnect.
func (c *stdioClient) reattach(ctx context.Context) error {
	// Best-effort: try a few times with small backoff
	var lastErr error
	for i := 0; i < 3; i++ {
		if err := c.attach(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return lastErr
}
