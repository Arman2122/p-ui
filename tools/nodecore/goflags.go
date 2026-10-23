package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// GOFLAGS uses cmd/internal/quoted fields, not shell escaping or Windows argv rules.
func splitGoFlags(value string) ([]string, error) {
	var fields []string
	for value = strings.TrimLeft(value, " \t\r\n"); value != ""; value = strings.TrimLeft(value, " \t\r\n") {
		if value[0] == '\'' || value[0] == '"' {
			end := strings.IndexByte(value[1:], value[0])
			if end < 0 {
				return nil, fmt.Errorf("unterminated GOFLAGS quote")
			}
			fields = append(fields, value[1:end+1])
			value = value[end+2:]
			continue
		}
		end := strings.IndexAny(value, " \t\r\n")
		if end < 0 {
			end = len(value)
		}
		fields = append(fields, value[:end])
		value = value[end:]
	}
	return fields, nil
}

func joinGoFlags(fields []string) (string, error) {
	encoded := make([]string, len(fields))
	for i, field := range fields {
		switch {
		case !strings.ContainsAny(field, " \t\r\n'\""):
			encoded[i] = field
		case !strings.ContainsRune(field, '\''):
			encoded[i] = "'" + field + "'"
		case !strings.ContainsRune(field, '"'):
			encoded[i] = "\"" + field + "\""
		default:
			return "", fmt.Errorf("GOFLAGS argument contains both quote forms")
		}
	}
	return strings.Join(encoded, " "), nil
}

func moduleGoFlags(root, original, replacement string) (string, string, error) {
	fields, err := splitGoFlags(original)
	if err != nil {
		return "", "", err
	}
	selected := filepath.Join(root, "go.mod")
	kept := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		name := strings.TrimPrefix(field, "-")
		name = strings.TrimPrefix(name, "-")
		if strings.HasPrefix(name, "modfile=") {
			selected = strings.TrimPrefix(name, "modfile=")
			if selected == "" {
				return "", "", fmt.Errorf("empty GOFLAGS module file")
			}
			if !filepath.IsAbs(selected) {
				selected = filepath.Join(root, selected)
			}
			continue
		}
		kept = append(kept, field)
	}
	if filepath.Ext(selected) != ".mod" {
		return "", "", fmt.Errorf("module file must end in .mod")
	}
	if replacement != "" {
		kept = append(kept, "-modfile="+replacement)
	}
	flags, err := joinGoFlags(kept)
	return flags, selected, err
}

func testGoFlags(original string, args []string) (string, error) {
	fields, err := splitGoFlags(original)
	if err != nil {
		return "", err
	}
	for _, group := range [][]string{fields, args} {
		for _, field := range group {
			name := strings.TrimLeft(strings.SplitN(field, "=", 2)[0], "-")
			if name == "trimpath" {
				return original, nil
			}
		}
	}
	return joinGoFlags(append(fields, "-trimpath"))
}
