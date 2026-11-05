package artifacts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"embed"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ulikunitz/xz"

	"developers-club-dx/internal/domain/tool"
)

//go:embed testdata/archive-fixture.7z
var sevenZipFixture embed.FS

func TestExtractZipArchiveUnderDeclaredRoot(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "go.zip")
	writeZip(t, archivePath, []zipMember{
		{name: "go/", mode: os.ModeDir | 0o755},
		{name: "go/bin/", mode: os.ModeDir | 0o755},
		{name: "go/bin/go.exe", mode: 0o755, content: []byte("binary")},
		{name: "go/VERSION", mode: 0o644, content: []byte("go1.27.3")},
	})
	destination := t.TempDir()

	if err := Extract(archivePath, destination, "go", tool.Zip, Limits{}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(destination, "bin", "go.exe"): "binary",
		filepath.Join(destination, "VERSION"):       "go1.27.3",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s contents = %q, want %q", path, got, want)
		}
	}
}

func TestExtractTarGzipArchiveUnderDeclaredRoot(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "go.tar.gz")
	writeTarGzip(t, archivePath, []tarMember{
		{name: "go", kind: tar.TypeDir, mode: 0o755},
		{name: "go/bin", kind: tar.TypeDir, mode: 0o755},
		{name: "go/bin/go", kind: tar.TypeReg, mode: 0o755, content: []byte("binary")},
	})
	destination := t.TempDir()

	if err := Extract(archivePath, destination, "go", tool.TarGzip, Limits{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "bin", "go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Fatalf("extracted binary = %q", got)
	}
}

func TestExtractTarXZArchiveUnderDeclaredRoot(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "zephyr-sdk.tar.xz")
	writeTarXZ(t, archivePath, []tarMember{
		{name: "sdk", kind: tar.TypeDir, mode: 0o755},
		{name: "sdk/arm-zephyr-eabi/bin", kind: tar.TypeDir, mode: 0o755},
		{name: "sdk/arm-zephyr-eabi/bin/compiler", kind: tar.TypeReg, mode: 0o755, content: []byte("binary")},
	})
	destination := t.TempDir()
	if err := Extract(archivePath, destination, "sdk", tool.TarXZ, Limits{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "arm-zephyr-eabi", "bin", "compiler"))
	if err != nil || string(got) != "binary" {
		t.Fatalf("extracted tar.xz compiler = %q, %v", got, err)
	}
}

func TestExtractSevenZipArchiveUnderDeclaredRoot(t *testing.T) {
	archiveBytes, err := sevenZipFixture.ReadFile("testdata/archive-fixture.7z")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "sample.7z")
	if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := Extract(archivePath, destination, "sample", tool.SevenZip, Limits{MaxExpandedBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "bin", "tool.txt"))
	if err != nil || string(got) != "7z fixture payload" {
		t.Fatalf("extracted 7z payload = %q, %v", got, err)
	}
}

func TestExtractRejectsUnsafeZipMembers(t *testing.T) {
	for _, member := range []zipMember{
		{name: "go/../../outside", mode: 0o644, content: []byte("bad")},
		{name: "outside/file", mode: 0o644, content: []byte("bad")},
		{name: "go/link", mode: os.ModeSymlink | 0o777, content: []byte("target")},
		{name: "go/bin\\bad", mode: 0o644, content: []byte("bad")},
	} {
		t.Run(member.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "unsafe.zip")
			writeZip(t, archivePath, []zipMember{member})
			destination := t.TempDir()
			if err := Extract(archivePath, destination, "go", tool.Zip, Limits{}); err == nil {
				t.Fatal("unsafe ZIP archive member was accepted")
			}
		})
	}
}

func TestExtractRejectsUnsafeTarMembersAndTypes(t *testing.T) {
	for _, member := range []tarMember{
		{name: "go/../../outside", kind: tar.TypeReg, mode: 0o644, content: []byte("bad")},
		{name: "outside/file", kind: tar.TypeReg, mode: 0o644, content: []byte("bad")},
		{name: "go/link", kind: tar.TypeSymlink, mode: 0o777, link: "../../outside"},
		{name: "go/hardlink", kind: tar.TypeLink, mode: 0o644, link: "go/bin/go"},
	} {
		t.Run(member.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			writeTarGzip(t, archivePath, []tarMember{member})
			destination := t.TempDir()
			if err := Extract(archivePath, destination, "go", tool.TarGzip, Limits{}); err == nil {
				t.Fatal("unsafe tar archive member was accepted")
			}
		})
	}
}

func TestExtractAllowsSafeLinksAndRejectsWritingThroughThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links may require elevated Windows privileges")
	}
	safeArchive := filepath.Join(t.TempDir(), "safe.tar.gz")
	writeTarGzip(t, safeArchive, []tarMember{
		{name: "sdk/bin/tool", kind: tar.TypeReg, mode: 0o755, content: []byte("binary")},
		{name: "sdk/bin/alias", kind: tar.TypeSymlink, mode: 0o777, link: "tool"},
	})
	safeDestination := t.TempDir()
	if err := Extract(safeArchive, safeDestination, "sdk", tool.TarGzip, Limits{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(safeDestination, "bin", "alias")); err != nil || string(got) != "binary" {
		t.Fatalf("safe archive link resolved to %q, %v", got, err)
	}

	unsafeArchive := filepath.Join(t.TempDir(), "through-link.tar.gz")
	writeTarGzip(t, unsafeArchive, []tarMember{
		{name: "sdk/link", kind: tar.TypeSymlink, mode: 0o777, link: "bin"},
		{name: "sdk/link/escaped", kind: tar.TypeReg, mode: 0o644, content: []byte("bad")},
	})
	if err := Extract(unsafeArchive, t.TempDir(), "sdk", tool.TarGzip, Limits{}); err == nil {
		t.Fatal("archive wrote through an earlier symbolic link")
	}
}

func TestExtractEnforcesEntryAndExpandedByteLimits(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "large.zip")
	writeZip(t, archivePath, []zipMember{
		{name: "go/a", mode: 0o644, content: []byte("1234")},
		{name: "go/b", mode: 0o644, content: []byte("5678")},
	})
	for _, limits := range []Limits{{MaxEntries: 1}, {MaxExpandedBytes: 7}} {
		if err := Extract(archivePath, t.TempDir(), "go", tool.Zip, limits); err == nil {
			t.Fatalf("archive passed restrictive limits %#v", limits)
		}
	}
}

func TestExtractRequiresEmptyDestinationAndDeclaredRoot(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "sample.zip")
	writeZip(t, archivePath, []zipMember{{name: "sample/bin/tool", mode: 0o755, content: []byte("x")}})
	destination := t.TempDir()
	if err := os.WriteFile(filepath.Join(destination, "existing"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Extract(archivePath, destination, "sample", tool.Zip, Limits{}); err == nil {
		t.Fatal("non-empty extraction destination was accepted")
	}
	if err := Extract(archivePath, t.TempDir(), "other", tool.Zip, Limits{}); err == nil {
		t.Fatal("archive without the declared root was accepted")
	}
}

type zipMember struct {
	name    string
	mode    os.FileMode
	content []byte
}

func writeZip(t *testing.T, path string, members []zipMember) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, member := range members {
		header := &zip.FileHeader{Name: member.name}
		header.SetMode(member.mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(member.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

type tarMember struct {
	name    string
	kind    byte
	mode    int64
	link    string
	content []byte
}

func writeTarGzip(t *testing.T, path string, members []tarMember) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	writer := tar.NewWriter(compressed)
	for _, member := range members {
		header := &tar.Header{Name: member.name, Typeflag: member.kind, Mode: member.mode, Linkname: member.link, Size: int64(len(member.content))}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTarXZ(t *testing.T, path string, members []tarMember) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := xz.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(compressed)
	for _, member := range members {
		header := &tar.Header{Name: member.name, Typeflag: member.kind, Mode: member.mode, Linkname: member.link, Size: int64(len(member.content))}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
