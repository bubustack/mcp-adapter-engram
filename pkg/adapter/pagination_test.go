package adapter

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeClient struct {
	responses []any
	calls     int
}

func (f *fakeClient) Initialize(context.Context, map[string]any) (any, error) { return nil, nil }
func (f *fakeClient) NotifyInitialized(context.Context) error                 { return nil }
func (f *fakeClient) ListTools(context.Context) (any, error)                  { return nil, nil }
func (f *fakeClient) CallTool(context.Context, string, map[string]any) (any, error) {
	if f.calls >= len(f.responses) {
		return nil, nil
	}
	res := f.responses[f.calls]
	f.calls++
	return res, nil
}
func (f *fakeClient) ListResources(context.Context) (any, error)        { return nil, nil }
func (f *fakeClient) ReadResource(context.Context, string) (any, error) { return nil, nil }
func (f *fakeClient) GetPrompt(context.Context, string) (any, error)    { return nil, nil }

func slackListResult(channels []map[string]any, cursor string) map[string]any {
	payload := map[string]any{
		"ok":       true,
		"channels": channels,
		"response_metadata": map[string]any{
			"next_cursor": cursor,
		},
	}
	b, _ := json.Marshal(payload)
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"result": map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": string(b),
				},
			},
		},
	}
}

func TestNormalizeAndPaginateAggregatesItems(t *testing.T) {
	client := &fakeClient{responses: []any{
		slackListResult([]map[string]any{{"id": "C2", "name": "beta"}}, ""),
	}}
	engram := &engramImpl{client: client}
	input := Inputs{
		Action: "callTool",
		Tool:   "slack_list_channels",
		Arguments: map[string]any{
			"limit": 2,
		},
		Pagination: &Pagination{
			CursorParam: "cursor",
			CursorPath:  "response_metadata.next_cursor",
			ItemsPath:   "channels",
			MaxPages:    3,
		},
	}
	first := slackListResult([]map[string]any{{"id": "C1", "name": "alpha"}}, "next")

	normalized, parsed, err := engram.normalizeAndPaginate(context.Background(), input, first)
	if err != nil {
		t.Fatalf("expected pagination ok, got %v", err)
	}
	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed map, got %T", parsed)
	}
	if client.calls != 1 {
		t.Fatalf("expected 1 pagination call, got %d", client.calls)
	}
	assertPaginationPayload(t, normalized, parsedMap)
}

func assertPaginationPayload(t *testing.T, normalized any, parsedMap map[string]any) {
	t.Helper()
	channels, ok := parsedMap["channels"].([]any)
	if !ok || len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %#v", parsedMap["channels"])
	}
	normalizedMap, ok := normalized.(map[string]any)
	if !ok {
		t.Fatalf("expected normalized map")
	}
	resultMap, ok := normalizedMap["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected normalized jsonrpc result")
	}
	contentList, ok := resultMap["content"].([]any)
	if !ok || len(contentList) == 0 {
		t.Fatalf("expected normalized content list")
	}
	item, ok := contentList[0].(map[string]any)
	if !ok {
		t.Fatalf("expected normalized content item map")
	}
	text, ok := item["text"].(string)
	if !ok {
		t.Fatalf("expected normalized content item text")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("expected normalized content text to be JSON: %v", err)
	}
	normalizedChannels, ok := payload["channels"].([]any)
	if !ok || len(normalizedChannels) != 2 {
		t.Fatalf("expected normalized content to include 2 channels, got %#v", payload["channels"])
	}
}
