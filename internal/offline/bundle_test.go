package offline

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/platform"
	"testing/fstest"
)

func TestCreateAndLoadSignedPlatformBundle(t *testing.T) {
	contents := []byte("verified sample archive")
	digest := sha256.Sum256(contents)
	checksum := hex.EncodeToString(digest[:])
	items := testBundleCatalog(t, checksum)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "sample.zip")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "prepared.dx-bundle")
	manifest := Manifest{
		SchemaVersion: 1,
		Platform:      tool.Windows,
		Architecture:  tool.AMD64,
		Artifacts:     []Payload{{ToolID: "sample", Version: "1.0.0", File: "sample.zip", SHA256: checksum}},
	}
	if err := CreateBundle(output, manifest, map[string]string{"sample.zip": source}, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, privateKey); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBundle(output, items, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Artifacts["sample"] != filepath.Join(output, "sample.zip") || loaded.Manifest.Artifacts[0].Version != "1.0.0" {
		t.Fatalf("loaded offline bundle = %#v", loaded)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != string(contents) {
		t.Fatalf("bundle creation changed the source archive: %q, %v", got, err)
	}
}

func TestSignedBundleSupportsCatalogPinnedLargeArchiveLimit(t *testing.T) {
	contents := []byte("small fixture with a catalog-pinned large size limit")
	digest := sha256.Sum256(contents)
	checksum := hex.EncodeToString(digest[:])
	const maxBytes = int64(2 << 30)
	items := testBundleCatalogWithLimit(t, checksum, maxBytes)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "sample.zip")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "large-limit.bundle")
	manifest := Manifest{
		SchemaVersion: 1,
		Platform:      tool.Windows,
		Architecture:  tool.AMD64,
		Artifacts:     []Payload{{ToolID: "sample", Version: "1.0.0", File: "sample.zip", SHA256: checksum, MaxBytes: maxBytes}},
	}
	machine := platform.Info{OS: tool.Windows, Architecture: tool.AMD64}
	if err := CreateBundle(output, manifest, map[string]string{"sample.zip": source}, machine, privateKey); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBundle(output, items, machine, publicKey); err != nil {
		t.Fatalf("catalog-pinned large artifact limit was rejected: %v", err)
	}
}

func TestSignedBundlePreservesAnExecutableBootstrapPayload(t *testing.T) {
	contents := []byte("verified sample archive")
	digest := sha256.Sum256(contents)
	checksum := hex.EncodeToString(digest[:])
	items := testBundleCatalog(t, checksum)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	archive := filepath.Join(root, "sample.zip")
	bootstrap := filepath.Join(root, "bootstrap.exe")
	if err := os.WriteFile(archive, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bootstrap, []byte("verified bootstrap"), 0o600); err != nil {
		t.Fatal(err)
	}
	bootstrapDigest := sha256.Sum256([]byte("verified bootstrap"))
	output := filepath.Join(root, "bundle")
	manifest := Manifest{
		SchemaVersion: 1,
		Platform:      tool.Windows,
		Architecture:  tool.AMD64,
		Artifacts:     []Payload{{ToolID: "sample", Version: "1.0.0", File: "sample.zip", SHA256: checksum}},
		Bootstrap:     &Payload{File: "bootstrap.exe", SHA256: hex.EncodeToString(bootstrapDigest[:]), Executable: true},
	}
	if err := CreateBundle(output, manifest, map[string]string{"sample.zip": archive, "bootstrap.exe": bootstrap}, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, privateKey); err != nil {
		t.Fatal(err)
	}
	verified, err := LoadBundle(output, items, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Bootstrap != filepath.Join(output, "bootstrap.exe") {
		t.Fatalf("verified bootstrap path = %q", verified.Bootstrap)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(verified.Bootstrap)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("bundled bootstrap is not executable: %v", info.Mode().Perm())
		}
	}
}

func TestLoadBundleRejectsTamperingWrongPlatformAndUnsignedMembers(t *testing.T) {
	contents := []byte("verified sample archive")
	digest := sha256.Sum256(contents)
	checksum := hex.EncodeToString(digest[:])
	items := testBundleCatalog(t, checksum)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "sample.zip")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bundle")
	manifest := Manifest{SchemaVersion: 1, Platform: tool.Windows, Architecture: tool.AMD64, Artifacts: []Payload{{ToolID: "sample", Version: "1.0.0", File: "sample.zip", SHA256: checksum}}}
	if err := CreateBundle(output, manifest, map[string]string{"sample.zip": source}, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, privateKey); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBundle(output, items, platform.Info{OS: tool.Linux, Architecture: tool.AMD64}, publicKey); err == nil || !strings.Contains(err.Error(), "targets") {
		t.Fatalf("wrong-platform bundle error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(output, "sample.zip"), []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBundle(output, items, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, publicKey); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered artifact error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(output, "unlisted.txt"), []byte("unsigned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBundle(output, items, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, publicKey); err == nil {
		t.Fatal("unsigned bundle member was accepted")
	}
}

func TestBundleCreationRefusesExistingOutputAndUnverifiedSource(t *testing.T) {
	contents := []byte("verified sample archive")
	digest := sha256.Sum256(contents)
	checksum := hex.EncodeToString(digest[:])
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "sample.zip")
	if err := os.WriteFile(source, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "bundle")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: 1, Platform: tool.Windows, Architecture: tool.AMD64, Artifacts: []Payload{{ToolID: "sample", Version: "1.0.0", File: "sample.zip", SHA256: checksum}}}
	if err := CreateBundle(output, manifest, map[string]string{"sample.zip": source}, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, privateKey); err == nil {
		t.Fatal("existing bundle output was replaced")
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	manifest.Artifacts[0].File = "../outside.zip"
	if err := CreateBundle(output, manifest, map[string]string{"../outside.zip": source}, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, privateKey); err == nil {
		t.Fatal("unsafe bundle filename was accepted")
	}
	manifest.Artifacts[0].File = "sample.zip"
	manifest.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	if err := CreateBundle(output, manifest, map[string]string{"sample.zip": source}, platform.Info{OS: tool.Windows, Architecture: tool.AMD64}, privateKey); err == nil {
		t.Fatal("unverified source was accepted")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("failed bundle generation left a partial output directory: %v", err)
	}
}

func testBundleCatalog(t *testing.T, checksum string) *catalog.Catalog {
	return testBundleCatalogWithLimit(t, checksum, 0)
}

func testBundleCatalogWithLimit(t *testing.T, checksum string, maxBytes int64) *catalog.Catalog {
	t.Helper()
	manifest := fmt.Sprintf(`schema_version: 1
id: sample
name: Sample
category: language.general
description: Sample developer tool.
version:
  channel: stable
  upgrade_policy: never_automatically
executables: [sample]
version_args: [--version]
platforms:
  windows: [amd64]
artifacts:
  - platform: windows
    architecture: amd64
    version: 1.0.0
    url: https://downloads.example/sample.zip
    sha256: %s
    max_download_bytes: %d
    format: zip
    archive_root: sample

`, checksum, maxBytes)
	items, err := catalog.Load(fstest.MapFS{"data/tools/sample.yaml": {Data: []byte(manifest)}})
	if err != nil {
		t.Fatal(err)
	}
	return items
}
