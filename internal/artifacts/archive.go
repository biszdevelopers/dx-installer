package artifacts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/ulikunitz/xz"

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
	return ExtractWithContext(context.Background(), archivePath, destination, archiveRoot, format, limits)
}

func ExtractWithContext(ctx context.Context, archivePath, destination, archiveRoot string, format tool.ArchiveFormat, limits Limits) error {
	if ctx == nil {
		return errors.New("extraction context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
		ctx:         ctx,
		destination: destination,
		root:        archiveRoot,
		limits:      limits,
		seen:        make(map[string]bool),
	}
	switch format {
	case tool.TarGzip:
		err = state.extractTarGzip(archivePath)
	case tool.TarXZ:
		err = state.extractTarXZ(archivePath)
	case tool.Zip:
		err = state.extractZip(archivePath)
	case tool.SevenZip:
		err = state.extractSevenZip(archivePath)
	default:
		err = fmt.Errorf("unsupported archive format %q", format)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !state.rootSeen {
		return fmt.Errorf("archive does not contain the declared root %q", archiveRoot)
	}
	return nil
}

type extractState struct {
	ctx         context.Context
	destination string
	root        string
	limits      Limits
	seen        map[string]bool
	count       int
	expanded    int64
	rootSeen    bool
	symlinks    map[string]bool
}

func (s *extractState) extractTarGzip(archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(contextReader{ctx: s.ctx, reader: file})
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer compressed.Close()
	return s.extractTar(tar.NewReader(compressed))
}

func (s *extractState) extractTarXZ(archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tar.xz archive: %w", err)
	}
	defer file.Close()
	compressed, err := (xz.ReaderConfig{DictCap: 64 << 20}).NewReader(contextReader{ctx: s.ctx, reader: file})
	if err != nil {
		return fmt.Errorf("open xz stream: %w", err)
	}
	return s.extractTar(tar.NewReader(compressed))
}

func (s *extractState) extractTar(reader *tar.Reader) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
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
		case tar.TypeSymlink:
			if err := s.makeSymlink(pathName, header.Linkname); err != nil {
				return fmt.Errorf("extract symbolic link %q: %w", header.Name, err)
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

func (s *extractState) extractSevenZip(archivePath string) error {
	reader, err := sevenzip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open 7z archive: %w", err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		s.count++
		if s.count > s.limits.MaxEntries {
			return fmt.Errorf("archive exceeds the %d entry safety limit", s.limits.MaxEntries)
		}
		pathName, isRoot, err := s.memberPath(entry.Name)
		if err != nil {
			return err
		}
		mode := entry.Mode()
		if isRoot {
			if !mode.IsDir() {
				return errors.New("archive root must be a directory")
			}
			s.rootSeen = true
			continue
		}
		s.rootSeen = true
		if mode&os.ModeSymlink != 0 {
			contents, err := readSevenZipLink(s.ctx, entry)
			if err != nil {
				return fmt.Errorf("read symbolic link %q: %w", entry.Name, err)
			}
			if err := s.makeSymlink(pathName, contents); err != nil {
				return fmt.Errorf("extract symbolic link %q: %w", entry.Name, err)
			}
			continue
		}
		if mode.IsDir() {
			if err := s.makeDirectory(pathName); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("archive entry %q is not a regular file or directory", entry.Name)
		}
		if entry.UncompressedSize > uint64(s.limits.MaxExpandedBytes) {
			return fmt.Errorf("archive entry %q exceeds the expanded-size limit", entry.Name)
		}
		contents, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open archive entry %q: %w", entry.Name, err)
		}
		writeErr := s.writeFile(pathName, contextReader{ctx: s.ctx, reader: contents}, int64(entry.UncompressedSize), int64(mode.Perm()))
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

func readSevenZipLink(ctx context.Context, entry *sevenzip.File) (string, error) {
	contents, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer contents.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: contents}, 4097))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > 4096 || strings.ContainsRune(string(data), '\x00') {
		return "", errors.New("symbolic link target is empty or too long")
	}
	return string(data), nil
}

func (s *extractState) extractZip(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if err := s.ctx.Err(); err != nil {
			return err
		}
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
		writeErr := s.writeFile(pathName, contextReader{ctx: s.ctx, reader: contents}, int64(entry.UncompressedSize64), modeBits(entry.Mode()))
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
	if clean == ".." || strings.HasPrefix(clean, "../") || clean != trimmed {
		return "", false, fmt.Errorf("archive entry %q has a non-canonical or traversing path", name)
	}
	if s.root == "." {
		if clean == "." {
			return "", true, nil
		}
		if hasUnsafeSegment(clean) {
			return "", false, fmt.Errorf("archive entry %q has an unsafe relative path", name)
		}
		if runtime.GOOS == "windows" && hasWindowsDeviceName(clean) {
			return "", false, fmt.Errorf("archive entry %q uses a reserved Windows file name", name)
		}
		return clean, false, nil
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
	if err := s.rejectSymlinkAncestors(relative); err != nil {
		return err
	}
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
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.rejectSymlinkAncestors(relative); err != nil {
		return err
	}
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
	written, copyErr := io.CopyN(file, contextReader{ctx: s.ctx, reader: reader}, size)
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

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func (s *extractState) makeSymlink(relative, target string) error {
	if err := s.rejectSymlinkAncestors(relative); err != nil {
		return err
	}
	if target == "" || strings.ContainsAny(target, "\\:\x00") || path.IsAbs(target) {
		return errors.New("symbolic link target must be a safe relative path")
	}
	resolved := path.Clean(path.Join(path.Dir(relative), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(resolved) {
		return errors.New("symbolic link target escapes the declared archive root")
	}
	if runtime.GOOS == "windows" && hasWindowsDeviceName(target) {
		return errors.New("symbolic link target uses a reserved Windows file name")
	}
	if s.seen[relative] {
		return fmt.Errorf("archive repeats path %q", relative)
	}
	s.seen[relative] = true
	if s.symlinks == nil {
		s.symlinks = make(map[string]bool)
	}
	s.symlinks[relative] = true
	linkPath := filepath.Join(s.destination, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return fmt.Errorf("create parent directory for symbolic link %q: %w", relative, err)
	}
	if err := os.Symlink(filepath.FromSlash(target), linkPath); err != nil {
		return fmt.Errorf("create symbolic link %q: %w", relative, err)
	}
	return nil
}

func (s *extractState) rejectSymlinkAncestors(relative string) error {
	for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
		if s.symlinks[parent] {
			return fmt.Errorf("archive path %q descends through symbolic link %q", relative, parent)
		}
	}
	return nil
}

func safeArchiveRoot(root string) bool {
	return root == "." || (root != "" && root != ".." && !path.IsAbs(root) && path.Clean(root) == root && !strings.ContainsAny(root, "\\:\x00") && !strings.HasPrefix(root, "../"))
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
