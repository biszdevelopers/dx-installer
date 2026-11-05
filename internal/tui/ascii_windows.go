//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

func platformRequiresASCII() bool {
	if os.Getenv("WT_SESSION") != "" || os.Getenv("TERM") != "" || os.Getenv("ConEmuANSI") != "" {
		return false
	}
	codePage, err := windows.GetConsoleOutputCP()
	return err == nil && codePage != 65001
}
