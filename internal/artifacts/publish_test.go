package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishDirectoryNeverReplacesAnExistingTarget(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging")
	target := filepath.Join(root, "installed")
	if err := os.Mkdir(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishDirectory(staging, target); err == nil {
		t.Fatal("publication replaced an existing installation target")
	}
	if contents, err := os.ReadFile(filepath.Join(target, "keep.txt")); err != nil || string(contents) != "keep" {
		t.Fatalf("existing target changed: %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(filepath.Join(staging, "new.txt")); err != nil || string(contents) != "new" {
		t.Fatalf("staging directory changed after rejected publication: %q, %v", contents, err)
	}
}
