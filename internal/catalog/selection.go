package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type SelectionFile struct {
	SchemaVersion int      `json:"schema_version"`
	Tools         []string `json:"tools"`
}

func EncodeSelection(items *Catalog, selected []string) ([]byte, error) {
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one tool before exporting")
	}
	seen := make(map[string]bool, len(selected))
	tools := append([]string(nil), selected...)
	for _, id := range tools {
		if _, exists := items.Tool(id); !exists {
			return nil, fmt.Errorf("unknown tool %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate tool %q", id)
		}
		seen[id] = true
	}
	sort.Strings(tools)
	data, err := json.MarshalIndent(SelectionFile{SchemaVersion: 1, Tools: tools}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode selection: %w", err)
	}
	return append(data, '\n'), nil
}

func DecodeSelection(items *Catalog, data []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var selection SelectionFile
	if err := decoder.Decode(&selection); err != nil {
		return nil, fmt.Errorf("decode selection: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("selection must contain one JSON object")
		}
		return nil, fmt.Errorf("decode trailing selection data: %w", err)
	}
	if selection.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported selection schema version %d", selection.SchemaVersion)
	}
	if len(selection.Tools) == 0 {
		return nil, fmt.Errorf("selection contains no tools")
	}
	seen := make(map[string]bool, len(selection.Tools))
	for _, id := range selection.Tools {
		if _, exists := items.Tool(id); !exists {
			return nil, fmt.Errorf("selection references unknown tool %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("selection contains duplicate tool %q", id)
		}
		seen[id] = true
	}
	result := append([]string(nil), selection.Tools...)
	sort.Strings(result)
	return result, nil
}
