package mcp

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func TestReadResponseSkipsNotificationsAndMismatchedIDs(t *testing.T) {
	c := &stdioClient{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":10}}`,
			`{"jsonrpc":"2.0","id":"other-id","result":{"ok":false}}`,
			`{"jsonrpc":"2.0","id":"target-id","result":{"ok":true}}`,
			"",
		}, "\n"))),
	}

	got, err := c.readResponse(context.Background(), "target-id")
	if err != nil {
		t.Fatalf("readResponse returned error: %v", err)
	}
	msg, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected response map, got %T", got)
	}
	if id, _ := msg["id"].(string); id != "target-id" {
		t.Fatalf("expected matched id target-id, got %q", id)
	}
}

func TestJSONRPCIDMatches(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		id       any
		want     bool
	}{
		{name: "string", expected: "abc", id: "abc", want: true},
		{name: "float", expected: "42", id: float64(42), want: true},
		{name: "mismatch", expected: "42", id: "43", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonRPCIDMatches(tt.expected, tt.id); got != tt.want {
				t.Fatalf("jsonRPCIDMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}
