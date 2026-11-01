package temporary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceIsPrivateAndRemovesItsContents(t *testing.T) {
	workspace, err := NewWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := workspace.Path()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("workspace path is not a directory: %s", path)
	}
	if os.PathSeparator != '\\' && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("workspace permissions are too broad: %o", info.Mode().Perm())
	}
	file, err := workspace.File("bootstrap.exe")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(file) != path {
		t.Fatalf("workspace file escaped its directory: %s", file)
	}
	if err := os.WriteFile(file, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("workspace remains after Close: %v", err)
	}
	if err := workspace.Close(); err != nil {
		t.Fatalf("Close should be idempotent: %v", err)
	}
}

func TestWorkspaceFileRejectsTraversalAndUseAfterClose(t *testing.T) {
	workspace, err := NewWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.Close()
	for _, name := range []string{"", ".", "..", "../escape", "folder/file", filepath.Join("folder", "file"), filepath.Join(workspace.Path(), "escape")} {
		if _, err := workspace.File(name); err == nil {
			t.Errorf("unsafe workspace filename %q was accepted", name)
		}
	}
	if err := workspace.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.File("after-close"); err == nil {
		t.Fatal("workspace accepted a filename after closing")
	}
}
