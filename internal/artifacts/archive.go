package artifacts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

type Limits struct {
	MaxEntries       int
	MaxExpandedBytes int64
}

const (
	defaultMaxEntries       = 100000
	defaultMaxExpandedBytes = 2 << 30
)

func Extract(archivePath, destination, archiveRoot string, format tool.ArchiveFormat, limits Limits) error {
	if limits.MaxEntries < 1 {
		limits.MaxEntries = defaultMaxEntries
	}
	if limits.MaxExpandedBytes < 1 {
		limits.MaxExpandedBytes = defaultMaxExpandedBytes
	}
	if !safeArchiveRoot(archiveRoot) {
		return fmt.Errorf("unsafe archive root %q", archiveRoot)
	}
	archiveInfo, err := os.Lstat(archivePath)
	if err != nil {
		return fmt.Errorf("inspect downloaded archive: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() {
		return errors.New("downloaded archive must be a regular file")
	}
	destinationInfo, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("inspect extraction directory: %w", err)
	}
	if !destinationInfo.IsDir() || destinationInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("extraction destination must be a real directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("read extraction directory: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("extraction destination must be empty")
	}

	state := extractState{
		destination: destination,
		root:        archiveRoot,
		limits:      limits,
		seen:        make(map[string]bool),
	}
	switch format {
	case tool.TarGzip:
		err = state.extractTarGzip(archivePath)
	case tool.Zip:
		err = state.extractZip(archivePath)
	default:
		err = fmt.Errorf("unsupported archive format %q", format)
	}
	if err != nil {
		return err
	}
	if !state.rootSeen {
		return fmt.Errorf("archive does not contain the declared root %q", archiveRoot)
	}
	return nil
}

type extractState struct {
	destination string
	root        string
	limits      Limits
	seen        map[string]bool
	count       int
	expanded    int64
	rootSeen    bool
}

func (s *extractState) extractTarGzip(archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar archive entry: %w", err)
		}
		s.count++
		if s.count > s.limits.MaxEntries {
			return fmt.Errorf("archive exceeds the %d entry safety limit", s.limits.MaxEntries)
		}
		pathName, isRoot, err := s.memberPath(header.Name)
		if err != nil {
			return err
		}
		if isRoot {
			if header.Typeflag != tar.TypeDir {
				return errors.New("archive root must be a directory")
			}
			s.rootSeen = true
			continue
		}
		s.rootSeen = true
		switch header.Typeflag {
		case tar.TypeDir:
			if err := s.makeDirectory(pathName); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return fmt.Errorf("archive entry %q has a negative size", header.Name)
			}
			if err := s.writeFile(pathName, reader, header.Size, header.Mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q uses unsupported or unsafe type %d", header.Name, header.Typeflag)
		}
	}
}

func (s *extractState) extractZip(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		s.count++
		if s.count > s.limits.MaxEntries {
			return fmt.Errorf("archive exceeds the %d entry safety limit", s.limits.MaxEntries)
		}
		pathName, isRoot, err := s.memberPath(entry.Name)
		if err != nil {
			return err
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive entry %q is a symbolic link", entry.Name)
		}
		if isRoot {
			if !entry.FileInfo().IsDir() {
				return errors.New("archive root must be a directory")
			}
			s.rootSeen = true
			continue
		}
		s.rootSeen = true
		if entry.FileInfo().IsDir() {
			if err := s.makeDirectory(pathName); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("archive entry %q is not a regular file or directory", entry.Name)
		}
		if entry.UncompressedSize64 > uint64(s.limits.MaxExpandedBytes) {
			return fmt.Errorf("archive entry %q exceeds the expanded-size limit", entry.Name)
		}
		contents, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open archive entry %q: %w", entry.Name, err)
		}
		writeErr := s.writeFile(pathName, contents, int64(entry.UncompressedSize64), modeBits(entry.Mode()))
		closeErr := contents.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return fmt.Errorf("close archive entry %q: %w", entry.Name, closeErr)
		}
	}
	return nil
}

func (s *extractState) memberPath(name string) (string, bool, error) {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") || path.IsAbs(name) {
		return "", false, fmt.Errorf("archive entry %q has an unsafe path", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != trimmed {
		return "", false, fmt.Errorf("archive entry %q has a non-canonical or traversing path", name)
	}
	if clean != s.root && !strings.HasPrefix(clean, s.root+"/") {
		return "", false, fmt.Errorf("archive entry %q is outside the declared root %q", name, s.root)
	}
	if clean == s.root {
		return "", true, nil
	}
	relative := strings.TrimPrefix(clean, s.root+"/")
	if relative == "" || hasUnsafeSegment(relative) {
		return "", false, fmt.Errorf("archive entry %q has an unsafe relative path", name)
	}
	if runtime.GOOS == "windows" && hasWindowsDeviceName(relative) {
		return "", false, fmt.Errorf("archive entry %q uses a reserved Windows file name", name)
	}
	return relative, false, nil
}

func (s *extractState) makeDirectory(relative string) error {
	if s.seen[relative] {
		return fmt.Errorf("archive repeats path %q", relative)
	}
	s.seen[relative] = true
	path := filepath.Join(s.destination, filepath.FromSlash(relative))
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create extracted directory %q: %w", relative, err)
	}
	return nil
}

func (s *extractState) writeFile(relative string, reader io.Reader, size int64, sourceMode int64) error {
	if size < 0 || size > s.limits.MaxExpandedBytes-s.expanded {
		return fmt.Errorf("archive entry %q exceeds the expanded-size safety limit", relative)
	}
	if s.seen[relative] {
		return fmt.Errorf("archive repeats path %q", relative)
	}
	s.seen[relative] = true
	s.expanded += size
	filePath := filepath.Join(s.destination, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", relative, err)
	}
	mode := os.FileMode(0o644)
	if sourceMode&0o111 != 0 {
		mode = 0o755
	}
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create extracted file %q: %w", relative, err)
	}
	written, copyErr := io.CopyN(file, reader, size)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(filePath)
		return fmt.Errorf("extract file %q after %d of %d bytes: %w", relative, written, size, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(filePath)
		return fmt.Errorf("close extracted file %q: %w", relative, closeErr)
	}
	return nil
}

func safeArchiveRoot(root string) bool {
	return root != "" && root != "." && root != ".." && !path.IsAbs(root) && path.Clean(root) == root && !strings.ContainsAny(root, "\\:\x00") && !strings.HasPrefix(root, "../")
}

func hasUnsafeSegment(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, ":\x00") {
			return true
		}
	}
	return false
}

func hasWindowsDeviceName(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		segment = strings.ToUpper(strings.TrimRight(segment, ". "))
		base := strings.SplitN(segment, ".", 2)[0]
		switch base {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
			return true
		}
	}
	return false
}

func modeBits(mode os.FileMode) int64 {
	return int64(mode.Perm())
}
