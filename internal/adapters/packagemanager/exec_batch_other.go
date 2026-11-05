//go:build !windows

package packagemanager

import (
	"context"
	"errors"
	"os/exec"
)

func execBatchCommand(context.Context, Command) (*exec.Cmd, error) {
	return nil, errors.New("batch executable invocation is available only on Windows")
}
