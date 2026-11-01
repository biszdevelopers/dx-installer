package catalog

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelectionCodecSortsAndValidatesCatalogIDs(t *testing.T) {
	items, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeSelection(items, []string{"uv", "git"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte("\"tools\": [\n    \"git\",\n    \"uv\"")) {
		t.Fatalf("selection is not deterministic and sorted:\n%s", encoded)
	}
	decoded, err := DecodeSelection(items, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 || decoded[0] != "git" || decoded[1] != "uv" {
		t.Fatalf("unexpected decoded selection: %#v", decoded)
	}
}

func TestSelectionCodecRejectsMalformedFiles(t *testing.T) {
	items, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"unknown field": `{"schema_version":1,"tools":["git"],"extra":true}`,
		"unknown tool":  `{"schema_version":1,"tools":["no-such-tool"]}`,
		"duplicate":     `{"schema_version":1,"tools":["git","git"]}`,
		"empty":         `{"schema_version":1,"tools":[]}`,
		"version":       `{"schema_version":2,"tools":["git"]}`,
		"trailing":      `{"schema_version":1,"tools":["git"]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeSelection(items, []byte(input)); err == nil {
				t.Fatalf("malformed selection was accepted: %s", input)
			}
		})
	}
	if _, err := EncodeSelection(items, []string{"git", "git"}); err == nil {
		t.Fatal("duplicate export was accepted")
	}
	if _, err := DecodeSelection(items, []byte(strings.Repeat(" ", 32))); err == nil {
		t.Fatal("whitespace-only file was accepted")
	}
}
