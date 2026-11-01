//go:build windows

package detection

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func versionCommand(ctx context.Context, path string, arguments []string) (*exec.Cmd, error) {
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".cmd" && extension != ".bat" {
		return exec.CommandContext(ctx, path, arguments...), nil
	}
	if strings.ContainsAny(path, "%\r\n\x00\"") {
		return nil, errors.New("batch executable path contains characters that cannot be passed safely to cmd.exe")
	}
	for _, argument := range arguments {
		if argument == "" || strings.ContainsAny(argument, " \t\r\n\x00\"&|<>^%!") {
			return nil, errors.New("batch executable arguments contain characters that cannot be passed safely to cmd.exe")
		}
	}
	commandProcessor, err := exec.LookPath("cmd.exe")
	if err != nil {
		return nil, errors.New("cmd.exe is unavailable for the verified batch executable")
	}
	commandLine := `"` + commandProcessor + `" /d /s /c ""` + path + `"`
	for _, argument := range arguments {
		commandLine += " " + argument
	}
	commandLine += `"`
	command := exec.CommandContext(ctx, commandProcessor)
	command.Args = nil
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: commandLine}
	return command, nil
}
