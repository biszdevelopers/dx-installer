package offline

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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
}

func testBundleCatalog(t *testing.T, checksum string) *catalog.Catalog {
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
    format: zip
    archive_root: sample
`, checksum)
	items, err := catalog.Load(fstest.MapFS{"data/tools/sample.yaml": {Data: []byte(manifest)}})
	if err != nil {
		t.Fatal(err)
	}
	return items
}
