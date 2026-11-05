//go:build !windows

package tui

func platformRequiresASCII() bool { return false }
