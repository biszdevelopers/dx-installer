package detection

import (
	"os"
	"runtime"
	"testing"

	"developers-club-dx/internal/domain/tool"
)

func TestBinaryArchitecturesIdentifiesCurrentTestExecutable(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	architectures, recognized, err := binaryArchitectures(path)
	if err != nil {
		t.Fatal(err)
	}
	if !recognized {
		t.Fatal("test executable format was not recognized")
	}
	want := map[string]tool.Architecture{
		"amd64":   tool.AMD64,
		"arm64":   tool.ARM64,
		"386":     tool.I386,
		"arm":     tool.ARM,
		"riscv64": tool.RISCV64,
	}[runtime.GOARCH]
	if want == "" || !containsArchitecture(architectures, want) {
		t.Fatalf("executable architectures = %#v, want %s", architectures, want)
	}
}

func TestBinaryArchitecturesLeavesScriptsUnverified(t *testing.T) {
	path := t.TempDir() + string(os.PathSeparator) + "tool-script"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho ready\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	architectures, recognized, err := binaryArchitectures(path)
	if err != nil {
		t.Fatal(err)
	}
	if recognized || len(architectures) != 0 {
		t.Fatalf("script architecture result = %#v, recognized %t", architectures, recognized)
	}
}
