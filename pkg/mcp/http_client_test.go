package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClientReturnsErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}
	_, err = client.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected non-2xx response to fail")
	}
	if !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("expected status in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("expected body in error, got: %v", err)
	}
}

func TestHTTPClientParsesJSONBodyOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"ok":true}}`))
	}))
	defer srv.Close()

	client, err := NewHTTPClient(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("NewHTTPClient returned error: %v", err)
	}
	got, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	msg, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map response, got %T", got)
	}
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %#v", msg["result"])
	}
	if okVal, _ := result["ok"].(bool); !okVal {
		t.Fatalf("expected result.ok=true, got %#v", result["ok"])
	}
}
