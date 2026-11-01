# Contributing

Use Go 1.26 or later. Keep tool-specific behavior in the tool manifest and keep command construction in typed adapters. Manifests must not include arbitrary commands, shell fragments, or unverified download URLs.

Before submitting a change, run:

```powershell
gofmt -w <changed-go-files>
go test ./...
go run ./cmd/catalog-check
```

Add or update focused tests with behavior changes. Do not claim platform support based only on cross-compilation. Installation support must be exercised against the named package manager and a disposable environment.

The `Package manager integration` workflow builds the client in each job and verifies Ninja installs through apt, dnf, pacman, zypper, and apk in disposable Linux containers, plus FreeBSD `pkg` in a FreeBSD VM. It also verifies SQLite through Chocolatey on Windows amd64 and OpenOCD through Homebrew on macOS amd64 and arm64. These are representative manager/tool pairs, not coverage of every declared mapping. Add an equivalent disposable-environment job before describing another mapping as integration-tested.

Commit each changed file in a separate scoped Conventional Commit. Use `scripts/commit-series.ps1` to make a series of one-file commits with explicit messages.
