package adapter

import (
	"encoding/json"
	"strings"
)

func normalizeMCPResult(result any) (any, any) {
	if result == nil {
		return nil, nil
	}
	switch typed := result.(type) {
	case map[string]any:
		if parsed := parseDataItemJSON(typed); parsed != nil {
			return typed, parsed
		}
		if text, ok := typed["text"].(string); ok {
			if parsed, ok := parseJSONText(text); ok {
				return typed, parsed
			}
		}
		if parsed := parseJSONRPCContent(typed); parsed != nil {
			return typed, parsed
		}
		return typed, nil
	case string:
		if parsed, ok := parseJSONText(typed); ok {
			return typed, parsed
		}
		return typed, nil
	default:
		return result, nil
	}
}

func parseDataItemJSON(result map[string]any) any {
	raw, ok := result["data"]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	parsedItems := make([]any, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		text, ok := entry["text"].(string)
		if !ok {
			continue
		}
		parsed, ok := parseJSONText(text)
		if !ok {
			continue
		}
		if _, exists := entry["parsed"]; !exists {
			entry["parsed"] = parsed
		}
		parsedItems = append(parsedItems, parsed)
	}
	if len(parsedItems) == 0 {
		return nil
	}
	if len(parsedItems) == 1 {
		return parsedItems[0]
	}
	return parsedItems
}

func parseJSONRPCContent(result map[string]any) any {
	raw, ok := result["result"].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := raw["content"]
	if !ok {
		return nil
	}
	if contentMap, ok := content.(map[string]any); ok {
		if parsed := parseDataItemJSON(contentMap); parsed != nil {
			return parsed
		}
		if text, ok := contentMap["text"].(string); ok {
			if parsed, ok := parseJSONText(text); ok {
				return parsed
			}
		}
		return nil
	}
	if contentList, ok := content.([]any); ok {
		for _, item := range contentList {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if parsed := parseDataItemJSON(itemMap); parsed != nil {
				return parsed
			}
			if text, ok := itemMap["text"].(string); ok {
				if parsed, ok := parseJSONText(text); ok {
					return parsed
				}
			}
		}
	}
	return nil
}

func updateNormalizedWithParsed(normalized any, parsed any) bool {
	root, ok := normalized.(map[string]any)
	if !ok {
		return false
	}
	raw, ok := root["result"].(map[string]any)
	if !ok {
		return false
	}
	content, ok := raw["content"]
	if !ok {
		return false
	}
	if contentMap, ok := content.(map[string]any); ok {
		return updateContentMap(contentMap, parsed)
	}
	if contentList, ok := content.([]any); ok {
		for _, item := range contentList {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if updateContentMap(itemMap, parsed) {
				return true
			}
		}
	}
	return false
}

func updateContentMap(content map[string]any, parsed any) bool {
	if _, ok := content["text"].(string); ok {
		b, err := json.Marshal(parsed)
		if err != nil {
			return false
		}
		content["text"] = string(b)
		content["parsed"] = parsed
		return true
	}
	dataRaw, ok := content["data"].([]any)
	if !ok || len(dataRaw) == 0 {
		return false
	}
	item, ok := dataRaw[0].(map[string]any)
	if !ok {
		return false
	}
	if _, ok := item["text"].(string); !ok {
		return false
	}
	b, err := json.Marshal(parsed)
	if err != nil {
		return false
	}
	item["text"] = string(b)
	item["parsed"] = parsed
	return true
}

func parseJSONText(text string) (any, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, false
	}
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}
