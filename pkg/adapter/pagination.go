package adapter

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Pagination configures cursor-based pagination for MCP tool calls.
type Pagination struct {
	CursorParam string `mapstructure:"cursorParam"`
	CursorPath  string `mapstructure:"cursorPath"`
	ItemsPath   string `mapstructure:"itemsPath"`
	MaxPages    int    `mapstructure:"maxPages"`
}

func (e *engramImpl) normalizeAndPaginate(ctx context.Context, in Inputs, result any) (any, any, error) {
	normalized, parsed := normalizeMCPResult(result)
	if in.Pagination == nil || parsed == nil {
		return normalized, parsed, nil
	}
	if strings.TrimSpace(in.Action) != "callTool" || strings.TrimSpace(in.Tool) == "" {
		return normalized, parsed, nil
	}
	paged, err := e.paginateCallTool(ctx, in, parsed)
	if err != nil {
		return normalized, parsed, err
	}
	_ = updateNormalizedWithParsed(normalized, paged)
	return normalized, paged, nil
}

func (e *engramImpl) paginateCallTool(ctx context.Context, in Inputs, parsed any) (any, error) {
	cursorParam, cursorPath, itemsPath, maxPages, err := paginationConfig(in.Pagination)
	if err != nil {
		return parsed, err
	}
	aggregated, cursor, err := paginationState(parsed, itemsPath, cursorPath)
	if err != nil {
		return parsed, err
	}
	page := 1
	for cursor != "" && page < maxPages {
		nextItems, nextCursor, stop, err := e.fetchPaginationPage(
			ctx,
			in,
			cursorParam,
			cursor,
			itemsPath,
			cursorPath,
		)
		if err != nil {
			return parsed, err
		}
		if stop {
			cursor = ""
			break
		}
		aggregated = append(aggregated, nextItems...)
		cursor = nextCursor
		page++
	}
	setPath(parsed, itemsPath, aggregated)
	if cursor != "" || cursorPath != "" {
		setPath(parsed, cursorPath, cursor)
	}
	return parsed, nil
}

func paginationConfig(cfg *Pagination) (string, string, string, int, error) {
	if cfg == nil {
		return "", "", "", 0, nil
	}
	cursorParam := strings.TrimSpace(cfg.CursorParam)
	cursorPath := strings.TrimSpace(cfg.CursorPath)
	itemsPath := strings.TrimSpace(cfg.ItemsPath)
	if cursorParam == "" || cursorPath == "" || itemsPath == "" {
		return "", "", "", 0, fmt.Errorf("pagination requires cursorParam, cursorPath, and itemsPath")
	}
	maxPages := cfg.MaxPages
	if maxPages <= 0 {
		maxPages = 5
	}
	return cursorParam, cursorPath, itemsPath, maxPages, nil
}

func paginationState(parsed any, itemsPath, cursorPath string) ([]any, string, error) {
	itemsRaw, ok := getPath(parsed, itemsPath)
	if !ok {
		return nil, "", fmt.Errorf("pagination itemsPath not found: %s", itemsPath)
	}
	items, ok := itemsRaw.([]any)
	if !ok {
		return nil, "", fmt.Errorf("pagination itemsPath is not a list: %s", itemsPath)
	}
	return append([]any{}, items...), getStringPath(parsed, cursorPath), nil
}

func (e *engramImpl) fetchPaginationPage(
	ctx context.Context,
	in Inputs,
	cursorParam, cursor, itemsPath, cursorPath string,
) ([]any, string, bool, error) {
	args := cloneAnyMap(in.Arguments)
	args[cursorParam] = cursor
	nextResult, err := e.client.CallTool(ctx, in.Tool, args)
	if err != nil {
		return nil, "", false, fmt.Errorf("pagination callTool failed: %w", err)
	}
	_, nextParsed := normalizeMCPResult(nextResult)
	if nextParsed == nil {
		return nil, "", false, fmt.Errorf("pagination result not parseable")
	}
	nextItemsRaw, ok := getPath(nextParsed, itemsPath)
	if !ok {
		return nil, "", true, nil
	}
	nextItems, ok := nextItemsRaw.([]any)
	if !ok {
		return nil, "", true, nil
	}
	return nextItems, getStringPath(nextParsed, cursorPath), false, nil
}

type pathToken struct {
	key   string
	index *int
}

func getPath(root any, path string) (any, bool) {
	if path == "" {
		return root, true
	}
	tokens, err := parsePath(path)
	if err != nil {
		return nil, false
	}
	current := root
	for _, tok := range tokens {
		if tok.key != "" {
			m, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			val, ok := m[tok.key]
			if !ok {
				return nil, false
			}
			current = val
		}
		if tok.index != nil {
			arr, ok := current.([]any)
			if !ok {
				return nil, false
			}
			idx := *tok.index
			if idx < 0 || idx >= len(arr) {
				return nil, false
			}
			current = arr[idx]
		}
	}
	return current, true
}

func getStringPath(root any, path string) string {
	val, ok := getPath(root, path)
	if !ok {
		return ""
	}
	if str, ok := val.(string); ok {
		return str
	}
	return ""
}

func setPath(root any, path string, value any) {
	if path == "" {
		return
	}
	tokens, err := parsePath(path)
	if err != nil || len(tokens) == 0 {
		return
	}
	parent, ok := walkToParent(root, tokens[:len(tokens)-1])
	if !ok {
		return
	}
	setFinalToken(parent, tokens[len(tokens)-1], value)
}

func walkToParent(root any, tokens []pathToken) (any, bool) {
	current := root
	for _, tok := range tokens {
		next, ok := descend(current, tok)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func descend(current any, tok pathToken) (any, bool) {
	if tok.key != "" {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		child, ok := m[tok.key]
		if !ok {
			return nil, false
		}
		current = child
	}
	if tok.index == nil {
		return current, true
	}
	arr, ok := current.([]any)
	if !ok {
		return nil, false
	}
	idx := *tok.index
	if idx < 0 || idx >= len(arr) {
		return nil, false
	}
	return arr[idx], true
}

func setFinalToken(current any, tok pathToken, value any) {
	if tok.key != "" {
		m, ok := current.(map[string]any)
		if !ok {
			return
		}
		m[tok.key] = value
		return
	}
	if tok.index == nil {
		return
	}
	arr, ok := current.([]any)
	if !ok {
		return
	}
	idx := *tok.index
	if idx < 0 || idx >= len(arr) {
		return
	}
	arr[idx] = value
}

func parsePath(path string) ([]pathToken, error) {
	segments := strings.Split(path, ".")
	tokens := make([]pathToken, 0, len(segments))
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		for seg != "" {
			if seg[0] == '[' {
				end := strings.Index(seg, "]")
				if end == -1 {
					return nil, fmt.Errorf("invalid path: %s", path)
				}
				idx, err := strconv.Atoi(seg[1:end])
				if err != nil {
					return nil, fmt.Errorf("invalid index in path: %s", path)
				}
				tokens = append(tokens, pathToken{index: &idx})
				seg = seg[end+1:]
				continue
			}
			idx := strings.Index(seg, "[")
			if idx == -1 {
				tokens = append(tokens, pathToken{key: seg})
				seg = ""
				continue
			}
			if idx > 0 {
				tokens = append(tokens, pathToken{key: seg[:idx]})
			}
			seg = seg[idx:]
		}
	}
	return tokens, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
