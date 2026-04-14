package adapter

import (
	"fmt"
	"strings"
)

// Selection configures extracting a value from parsed MCP payloads.
//
//nolint:unused // Reserved for upcoming parsed-result selection support.
type Selection struct {
	ItemsPath   string `mapstructure:"itemsPath"`
	MatchField  string `mapstructure:"matchField"`
	MatchValue  string `mapstructure:"matchValue"`
	ValuePath   string `mapstructure:"valuePath"`
	OutputKey   string `mapstructure:"outputKey"`
	IncludeItem bool   `mapstructure:"includeItem"`
}

//nolint:unused // Reserved for upcoming parsed-result selection support.
func selectFromParsed(parsed any, sel *Selection) (map[string]any, error) {
	if sel == nil {
		return nil, nil
	}
	itemsPath := strings.TrimSpace(sel.ItemsPath)
	if itemsPath == "" {
		return nil, fmt.Errorf("select requires itemsPath")
	}
	itemsRaw, ok := getPath(parsed, itemsPath)
	if !ok {
		return nil, fmt.Errorf("select itemsPath not found: %s", itemsPath)
	}
	items, ok := itemsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("select itemsPath is not a list: %s", itemsPath)
	}
	var matched map[string]any
	matchField := strings.TrimSpace(sel.MatchField)
	matchValue := sel.MatchValue
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if matchField == "" {
			matched = entry
			break
		}
		val, ok := getPath(entry, matchField)
		if !ok {
			continue
		}
		if valuesEqual(val, matchValue) {
			matched = entry
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("select no match for %s=%q", matchField, matchValue)
	}
	selectedVal := any(matched)
	if strings.TrimSpace(sel.ValuePath) != "" {
		val, ok := getPath(matched, sel.ValuePath)
		if !ok {
			return nil, fmt.Errorf("select valuePath not found: %s", sel.ValuePath)
		}
		selectedVal = val
	}
	key := strings.TrimSpace(sel.OutputKey)
	if key == "" {
		key = "value"
	}
	out := map[string]any{key: selectedVal}
	if sel.IncludeItem {
		out["item"] = matched
	}
	return out, nil
}

//nolint:unused // Reserved for upcoming parsed-result selection support.
func valuesEqual(val any, expected string) bool {
	switch typed := val.(type) {
	case string:
		return typed == expected
	case fmt.Stringer:
		return typed.String() == expected
	default:
		return fmt.Sprint(val) == expected
	}
}
