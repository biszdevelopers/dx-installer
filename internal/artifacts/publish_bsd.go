//go:build freebsd || openbsd || netbsd || dragonfly

package artifacts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func publishDirectory(staging, target string) (publishErr error) {
	if err := os.Mkdir(target, 0o700); err != nil {
		return err
	}
	reservation, err := os.Lstat(target)
	if err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("inspect reserved installation target: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		current, inspectErr := os.Lstat(target)
		if inspectErr == nil && os.SameFile(reservation, current) {
			if cleanupErr := os.RemoveAll(target); cleanupErr != nil {
				publishErr = errors.Join(publishErr, cleanupErr)
			}
		}
	}()
	if err := copyDirectoryContents(staging, target); err != nil {
		return err
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("remove installation staging directory: %w", err)
	}
	committed = true
	return nil
}

func copyDirectoryContents(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to publish symbolic link %s", sourcePath)
		}
		if info.IsDir() {
			if err := os.Mkdir(destinationPath, info.Mode().Perm()&0o755); err != nil {
				return err
			}
			if err := copyDirectoryContents(sourcePath, destinationPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to publish special file %s", sourcePath)
		}
		if err := copyRegularFile(sourcePath, destinationPath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("installation staging file changed or is not regular")
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode&0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
