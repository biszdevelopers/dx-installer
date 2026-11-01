package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"developers-club-dx/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dx-release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("dx-release", flag.ContinueOnError)
	version := flags.String("version", "", "immutable release version such as v1.2.3")
	origin := flags.String("origin", "", "public HTTPS distribution origin, such as https://dx.example.org")
	output := flags.String("output-dir", "", "local distribution root to write")
	source := flags.String("source-dir", ".", "repository root containing cmd/bootstrap")
	keyPath := flags.String("signing-key", "", "maintainer Ed25519 private key file (PEM, base64, seed, or private key)")
	goBinary := flags.String("go", "go", "Go compiler executable")
	targetsValue := flags.String("targets", "", "comma-separated platform/architecture targets; default is the tested release matrix")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *version == "" || *origin == "" || *output == "" || *keyPath == "" {
		return fmt.Errorf("--version, --origin, --output-dir, and --signing-key are required")
	}
	privateKey, err := release.LoadPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("loaded signing key does not have an Ed25519 public key")
	}
	targets, err := parseTargets(*targetsValue)
	if err != nil {
		return err
	}
	absoluteSource, err := filepath.Abs(*source)
	if err != nil {
		return fmt.Errorf("resolve source directory: %w", err)
	}
	config := release.Config{
		Version:    *version,
		Origin:     *origin,
		OutputDir:  *output,
		Targets:    targets,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}
	result, err := release.BuildAndGenerate(context.Background(), absoluteSource, *goBinary, config)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Published local immutable release %s\n", result.Version)
	fmt.Fprintf(os.Stdout, "Directory: %s\n", result.Directory)
	fmt.Fprintf(os.Stdout, "Targets: %s\n", formatTargets(result.Targets))
	fmt.Fprintf(os.Stdout, "Signed SHA256SUMS for %d release files.\n", len(result.Checksums))
	return nil
}

func parseTargets(value string) ([]release.Target, error) {
	if strings.TrimSpace(value) == "" {
		return release.DefaultTargets(), nil
	}
	parts := strings.Split(value, ",")
	targets := make([]release.Target, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		target, err := release.ParseTarget(part)
		if err != nil {
			return nil, err
		}
		if seen[target.Key()] {
			return nil, fmt.Errorf("release target %q is repeated", target)
		}
		seen[target.Key()] = true
		targets = append(targets, target)
	}
	return targets, nil
}

func formatTargets(targets []release.Target) string {
	values := make([]string, len(targets))
	for index, target := range targets {
		values[index] = target.String()
	}
	return strings.Join(values, ", ")
}
