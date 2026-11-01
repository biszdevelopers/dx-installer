package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"developers-club-dx/internal/security"
)

func verifyRelease(args []string) error {
	flags := flag.NewFlagSet("verify-release", flag.ContinueOnError)
	directory := flags.String("directory", ".", "release directory containing signed checksum metadata")
	if err := flags.Parse(args); err != nil {
		return err
	}
	root, err := filepath.Abs(*directory)
	if err != nil {
		return fmt.Errorf("resolve release directory: %w", err)
	}
	checksums, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		return fmt.Errorf("read checksum list: %w", err)
	}
	signatureFile, err := os.ReadFile(filepath.Join(root, "SHA256SUMS.sig"))
	if err != nil {
		return fmt.Errorf("read checksum signature: %w", err)
	}
	signature, err := security.DecodeSignature(signatureFile)
	if err != nil {
		return err
	}
	entries, err := security.VerifyPinnedChecksums(checksums, signature)
	if err != nil {
		return err
	}
	publishedKey, err := os.ReadFile(filepath.Join(root, "SHA256SUMS.pub"))
	if err != nil {
		return fmt.Errorf("read published public key: %w", err)
	}
	pinnedKey, err := security.DecodePublicKey(security.PinnedEd25519PublicKey)
	if err != nil {
		return fmt.Errorf("pinned release public key is unavailable: %w", err)
	}
	published, err := security.DecodePublicKey(string(publishedKey))
	if err != nil {
		return fmt.Errorf("published public key is invalid: %w", err)
	}
	if !bytes.Equal(published, pinnedKey) {
		return fmt.Errorf("published public key does not match the key pinned in this executable")
	}
	if err := security.VerifyReleaseFiles(root, entries); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Verified signed release metadata and %d artifact checksums.\n", len(entries))
	return nil
}
