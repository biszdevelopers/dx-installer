package temporary

import (
	"fmt"
	"os"
	"path/filepath"
)

type Workspace struct {
	path   string
	closed bool
}

func NewWorkspace() (*Workspace, error) {
	path, err := os.MkdirTemp("", "dx-bootstrap-")
	if err != nil {
		return nil, fmt.Errorf("create temporary bootstrap workspace: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.RemoveAll(path)
		return nil, fmt.Errorf("restrict temporary workspace permissions: %w", err)
	}
	return &Workspace{path: path}, nil
}

func (w *Workspace) Path() string {
	return w.path
}

func (w *Workspace) File(name string) (string, error) {
	if w.closed {
		return "", fmt.Errorf("temporary workspace is already closed")
	}
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." || name == ".." {
		return "", fmt.Errorf("temporary file name must be a single relative path component")
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("temporary file name must not contain a directory")
	}
	return filepath.Join(w.path, name), nil
}

func (w *Workspace) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := os.RemoveAll(w.path); err != nil {
		return fmt.Errorf("remove temporary bootstrap workspace: %w", err)
	}
	return nil
}
