//go:build windows

package artifacts

import "golang.org/x/sys/windows"

func publishDirectory(staging, target string) error {
	from, err := windows.UTF16PtrFromString(staging)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}
