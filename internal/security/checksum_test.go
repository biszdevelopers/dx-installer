package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignedChecksumListRejectsTampering(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	contents := []byte(SHA256([]byte("artifact")) + "  dx-bootstrap-linux-amd64\n")
	signature := ed25519.Sign(privateKey, contents)
	entries, err := VerifySignedChecksums(contents, signature, publicKey)
	if err != nil {
		t.Fatalf("valid signature was rejected: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "dx-bootstrap-linux-amd64" {
		t.Fatalf("unexpected parsed entries: %#v", entries)
	}
	if _, err := VerifySignedChecksums(append(contents, ' '), signature, publicKey); err == nil {
		t.Fatal("modified checksum list was accepted")
	}
	if _, err := VerifySignedChecksums(contents, signature, publicKey[:len(publicKey)-1]); err == nil {
		t.Fatal("truncated public key was accepted")
	}
}

func TestParseChecksumListRejectsUnsafeAndDuplicateNames(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, name := range []string{"../artifact", "folder/artifact", "..", "bad\\name", "SHA256SUMS", "SHA256SUMS.pub"} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseChecksumList([]byte(digest + "  " + name + "\n")); err == nil {
				t.Fatalf("unsafe filename %q was accepted", name)
			}
		})
	}
	duplicate := digest + "  artifact\n" + digest + "  artifact\n"
	if _, err := ParseChecksumList([]byte(duplicate)); err == nil {
		t.Fatal("duplicate artifact entry was accepted")
	}
	if _, err := ParseChecksumList([]byte(strings.Repeat("z", 64) + "  artifact\n")); err == nil {
		t.Fatal("non-hex digest was accepted")
	}
}

func TestVerifyReleaseFilesRejectsUnsignedPayloads(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"RELEASE":        []byte("version=v1.2.3\n"),
		"SHA256SUMS":     []byte("signed list"),
		"SHA256SUMS.sig": []byte("signature"),
		"SHA256SUMS.pub": []byte("public key"),
		"artifact":       []byte("payload"),
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries := []Checksum{
		{SHA256: SHA256(files["RELEASE"]), Name: "RELEASE"},
		{SHA256: SHA256(files["artifact"]), Name: "artifact"},
	}
	if err := VerifyReleaseFiles(root, entries); err != nil {
		t.Fatalf("complete signed release was rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unsigned"), []byte("unlisted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyReleaseFiles(root, entries); err == nil {
		t.Fatal("release with an unsigned file was accepted")
	}
}

func TestVerifyFilesChecksOnlyRegularFilesAndDigests(t *testing.T) {
	root := t.TempDir()
	contents := []byte("verified payload")
	if err := os.WriteFile(filepath.Join(root, "artifact"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFiles(root, []Checksum{{SHA256: SHA256(contents), Name: "artifact"}}); err != nil {
		t.Fatalf("valid artifact was rejected: %v", err)
	}
	if err := VerifyFiles(root, []Checksum{{SHA256: strings.Repeat("0", 64), Name: "artifact"}}); err == nil {
		t.Fatal("artifact with an incorrect digest was accepted")
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFiles(root, []Checksum{{SHA256: SHA256(nil), Name: "directory"}}); err == nil {
		t.Fatal("directory was accepted as a release artifact")
	}
}

func TestPublicKeyAndSignatureEncodingRoundTrip(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := EncodePublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	decodedKey, err := DecodePublicKey(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(decodedKey) != string(publicKey) {
		t.Fatal("public key changed during encoding")
	}
	encodedSignature, err := SignChecksums([]byte("signed bytes"), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := DecodeSignature(encodedSignature)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(decodedKey, []byte("signed bytes"), signature) {
		t.Fatal("encoded signature did not verify")
	}
}
