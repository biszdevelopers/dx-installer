package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/domain/tool"
	"developers-club-dx/internal/offline"
	"developers-club-dx/internal/platform"
	"developers-club-dx/internal/release"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dx-offline-bundle:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("offline-bundle", flag.ContinueOnError)
	platformValue := flags.String("platform", "", "target platform: windows, macos, linux, or bsd")
	architectureValue := flags.String("arch", "", "target architecture: amd64, arm64, 386, arm, or riscv64")
	outputValue := flags.String("output", "", "new output directory for the signed bundle")
	keyPath := flags.String("signing-key", "", "maintainer Ed25519 private key used to sign the bundle")
	bootstrapPath := flags.String("bootstrap", "", "optional platform bootstrap executable to include")
	var artifacts stringList
	flags.Var(&artifacts, "artifact", "tool-id=absolute-archive-path; repeat once per tool")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *platformValue == "" || *architectureValue == "" || *outputValue == "" || *keyPath == "" || len(artifacts) == 0 {
		return errors.New("--platform, --arch, --output, --signing-key, and at least one --artifact are required")
	}
	machine := platform.Info{OS: tool.OperatingSystem(strings.ToLower(*platformValue)), Architecture: tool.Architecture(strings.ToLower(*architectureValue))}
	if !validPlatformArchitecture(machine) {
		return fmt.Errorf("unsupported platform and architecture %s/%s", machine.OS, machine.Architecture)
	}
	items, err := catalog.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load embedded catalog: %w", err)
	}
	manifest := offline.Manifest{SchemaVersion: 1, Platform: machine.OS, Architecture: machine.Architecture}
	sourceFiles := make(map[string]string, len(artifacts)+1)
	seenTools := make(map[string]bool, len(artifacts))
	for _, entry := range artifacts {
		id, path, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(id) != id || strings.TrimSpace(path) != path || id == "" || path == "" {
			return fmt.Errorf("artifact %q must use tool-id=absolute-archive-path", entry)
		}
		if seenTools[id] {
			return fmt.Errorf("artifact for tool %q was repeated", id)
		}
		seenTools[id] = true
		toolManifest, exists := items.Tool(id)
		if !exists {
			return fmt.Errorf("unknown catalog tool %q", id)
		}
		artifact, exists := toolManifest.ArtifactFor(machine.OS, machine.Architecture)
		if !exists {
			return fmt.Errorf("tool %q has no catalog-pinned archive for %s/%s", id, machine.OS, machine.Architecture)
		}
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve artifact %q: %w", id, err)
		}
		filename := fmt.Sprintf("%s-%s-%s-%s.%s", id, machine.OS, machine.Architecture, artifact.Version, artifact.Format)
		manifest.Artifacts = append(manifest.Artifacts, offline.Payload{ToolID: id, Version: artifact.Version, File: filename, SHA256: artifact.SHA256})
		sourceFiles[filename] = absolutePath
	}
	if *bootstrapPath != "" {
		absolutePath, err := filepath.Abs(*bootstrapPath)
		if err != nil {
			return fmt.Errorf("resolve bootstrap executable: %w", err)
		}
		contents, err := os.ReadFile(absolutePath)
		if err != nil {
			return fmt.Errorf("read bootstrap executable: %w", err)
		}
		digest := sha256.Sum256(contents)
		filename := fmt.Sprintf("dx-bootstrap-%s-%s", machine.OS, machine.Architecture)
		if machine.OS == tool.Windows {
			filename += ".exe"
		}
		manifest.Bootstrap = &offline.Payload{File: filename, SHA256: hex.EncodeToString(digest[:])}
		sourceFiles[filename] = absolutePath
	}
	privateKey, err := release.LoadPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(*outputValue)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if err := offline.CreateBundle(output, manifest, sourceFiles, machine, privateKey); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Created signed %s/%s offline bundle with %d tool artifact(s): %s\n", machine.OS, machine.Architecture, len(manifest.Artifacts), output)
	return nil
}

func validPlatformArchitecture(machine platform.Info) bool {
	supported := map[tool.OperatingSystem]map[tool.Architecture]bool{
		tool.Windows: {tool.AMD64: true, tool.ARM64: true, tool.I386: true},
		tool.MacOS:   {tool.AMD64: true, tool.ARM64: true},
		tool.Linux:   {tool.AMD64: true, tool.ARM64: true, tool.I386: true, tool.ARM: true, tool.RISCV64: true},
		tool.BSD:     {tool.AMD64: true, tool.ARM64: true},
	}
	return supported[machine.OS][machine.Architecture]
}
