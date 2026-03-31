package config

import (
	"fmt"
	"strconv"
	"strings"
)

func parseTOML(input string) (map[string]any, error) {
	root := map[string]any{}
	current := root
	lines := strings.Split(input, "\n")

	for i := 0; i < len(lines); i++ {
		line := stripComments(lines[i])
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]"):
			path := strings.TrimSpace(line[2 : len(line)-2])
			table, err := ensureArrayTable(root, strings.Split(path, "."))
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			current = table
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			path := strings.TrimSpace(line[1 : len(line)-1])
			table, err := ensureTable(root, strings.Split(path, "."))
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			current = table
		default:
			key, raw, ok := strings.Cut(line, "=")
			if !ok {
				return nil, fmt.Errorf("line %d: expected key=value", i+1)
			}
			key = strings.TrimSpace(key)
			raw = strings.TrimSpace(raw)
			if strings.HasPrefix(raw, "[") && !strings.Contains(raw, "]") {
				for i+1 < len(lines) {
					i++
					next := stripComments(lines[i])
					raw += "\n" + next
					if strings.Contains(next, "]") {
						break
					}
				}
			}
			value, err := parseValue(raw)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			current[key] = value
		}
	}
	return root, nil
}

func parseValue(raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "'"):
		return parseString(raw)
	case strings.HasPrefix(raw, "["):
		return parseArray(raw)
	case raw == "true" || raw == "false":
		return raw == "true", nil
	default:
		n, err := strconv.Atoi(raw)
		if err == nil {
			return n, nil
		}
		return raw, nil
	}
}

func parseString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 {
		return "", fmt.Errorf("invalid string %q", raw)
	}
	quote := raw[0]
	if raw[len(raw)-1] != quote {
		return "", fmt.Errorf("unterminated string %q", raw)
	}
	return raw[1 : len(raw)-1], nil
}

func parseArray(raw string) ([]any, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return nil, fmt.Errorf("invalid array %q", raw)
	}
	body := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if body == "" {
		return []any{}, nil
	}
	parts := splitArrayItems(body)
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		v, err := parseValue(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func splitArrayItems(body string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	for _, r := range body {
		switch {
		case quote != 0:
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			current.WriteRune(r)
		case r == ',':
			item := strings.TrimSpace(current.String())
			if item != "" {
				parts = append(parts, item)
			}
			current.Reset()
		case r == '\n' || r == '\r':
		default:
			current.WriteRune(r)
		}
	}
	item := strings.TrimSpace(current.String())
	if item != "" {
		parts = append(parts, item)
	}
	return parts
}

func ensureTable(root map[string]any, path []string) (map[string]any, error) {
	current := root
	for _, key := range path {
		next, ok := current[key]
		if !ok {
			child := map[string]any{}
			current[key] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path %q is not a table", strings.Join(path, "."))
		}
		current = child
	}
	return current, nil
}

func ensureArrayTable(root map[string]any, path []string) (map[string]any, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty array table path")
	}
	parent, err := ensureTable(root, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	var items []any
	if existing, ok := parent[last]; ok {
		var ok bool
		items, ok = existing.([]any)
		if !ok {
			return nil, fmt.Errorf("path %q is not an array table", strings.Join(path, "."))
		}
	}
	child := map[string]any{}
	parent[last] = append(items, child)
	return child, nil
}

func stripComments(line string) string {
	var out strings.Builder
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			out.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			out.WriteRune(r)
		case r == '#':
			if i == 0 || line[i-1] != '\\' {
				return out.String()
			}
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
