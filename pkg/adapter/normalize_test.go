package adapter

import "testing"

func TestNormalizeMCPResultParsesDataTextJSON(t *testing.T) {
	result := map[string]any{
		"contentType": "json",
		"data": []any{
			map[string]any{
				"type": "text",
				"text": `{"ok":true,"channels":[{"id":"C123","name":"daily-digest"}]}`,
			},
		},
	}

	normalized, parsed := normalizeMCPResult(result)
	if normalized == nil {
		t.Fatalf("expected normalized result")
	}
	if parsed == nil {
		t.Fatalf("expected parsed result")
	}
	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed result map, got %T", parsed)
	}
	channels, ok := parsedMap["channels"].([]any)
	if !ok || len(channels) != 1 {
		t.Fatalf("expected channels in parsed result, got %#v", parsedMap["channels"])
	}
	first, ok := channels[0].(map[string]any)
	if !ok || first["id"] != "C123" {
		t.Fatalf("expected channel id C123, got %#v", first)
	}
	dataItems, ok := normalized.(map[string]any)["data"].([]any)
	if !ok || len(dataItems) != 1 {
		t.Fatalf("expected data items in normalized result")
	}
	item, ok := dataItems[0].(map[string]any)
	if !ok {
		t.Fatalf("expected data item map")
	}
	if _, ok := item["parsed"]; !ok {
		t.Fatalf("expected parsed field in data item")
	}
}

func TestNormalizeMCPResultParsesJSONRPCContent(t *testing.T) {
	result := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"result": map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": `{"ok":true,"channels":[{"id":"C999","name":"daily-digest"}]}`,
				},
			},
		},
	}

	normalized, parsed := normalizeMCPResult(result)
	if normalized == nil {
		t.Fatalf("expected normalized result")
	}
	if parsed == nil {
		t.Fatalf("expected parsed result")
	}
	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed result map, got %T", parsed)
	}
	channels, ok := parsedMap["channels"].([]any)
	if !ok || len(channels) != 1 {
		t.Fatalf("expected channels in parsed result, got %#v", parsedMap["channels"])
	}
	payload, ok := normalized.(map[string]any)["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected jsonrpc result in normalized")
	}
	content, ok := payload["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected content list in normalized")
	}
	item, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected content item map in normalized")
	}
	if text, ok := item["text"].(string); !ok || text == "" {
		t.Fatalf("expected content item text in normalized")
	}
}

func TestNormalizeMCPResultParsesJSONString(t *testing.T) {
	raw := `{"ok":true,"value":"demo"}`
	_, parsed := normalizeMCPResult(raw)
	if parsed == nil {
		t.Fatalf("expected parsed result for JSON string")
	}
	parsedMap, ok := parsed.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed map, got %T", parsed)
	}
	if parsedMap["value"] != "demo" {
		t.Fatalf("expected value demo, got %#v", parsedMap["value"])
	}
}
