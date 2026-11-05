//go:build windows

package packagemanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func execBatchCommand(ctx context.Context, commandSpec Command) (*exec.Cmd, error) {
	if strings.ContainsAny(commandSpec.Executable, "%\r\n\x00\"") {
		return nil, errors.New("batch executable path contains characters that cannot be passed safely to cmd.exe")
	}
	for _, argument := range commandSpec.Arguments {
		if argument == "" || strings.ContainsAny(argument, " \t\r\n\x00\"&|<>^%!") {
			return nil, errors.New("batch executable arguments contain characters that cannot be passed safely to cmd.exe")
		}
	}
	commandProcessor, err := exec.LookPath("cmd.exe")
	if err != nil {
		return nil, errors.New("cmd.exe is unavailable for the verified batch executable")
	}
	commandLine := `"` + commandProcessor + `" /d /s /c ""` + commandSpec.Executable + `"`
	for _, argument := range commandSpec.Arguments {
		commandLine += " " + argument
	}
	commandLine += `"`
	command := exec.CommandContext(ctx, commandProcessor)
	command.Args = nil
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: commandLine}
	if len(commandSpec.Environment) > 0 {
		command.Env = mergedEnvironment(os.Environ(), commandSpec.Environment)
	}
	return command, nil
}
