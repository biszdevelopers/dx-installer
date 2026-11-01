//go:build darwin

package artifacts

import "golang.org/x/sys/unix"

func publishDirectory(staging, target string) error {
	return unix.RenamexNp(staging, target, unix.RENAME_EXCL)
}
