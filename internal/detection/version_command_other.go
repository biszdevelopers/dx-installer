//go:build !windows

package detection

import (
	"context"
	"os/exec"
)

func versionCommand(ctx context.Context, path string, arguments []string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, path, arguments...), nil
}
