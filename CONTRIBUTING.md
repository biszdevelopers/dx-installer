# Contributing

Use Go 1.26 or later. Keep tool-specific behavior in the tool manifest and keep command construction in typed adapters. Manifests must not include arbitrary commands, shell fragments, or unverified download URLs.

Before submitting a change, run:

```powershell
gofmt -w <changed-go-files>
go test ./...
go run ./cmd/catalog-check
```

Add or update focused tests with behavior changes. Do not claim platform support based only on cross-compilation. Installation support must be exercised against the named package manager and a disposable environment.

The `Package manager integration` workflow builds the client in each matrix job and runs its approved Ninja install and post-install verification in disposable containers for apt, dnf, pacman, zypper, and apk. It also executes the FreeBSD amd64 bootstrap in a FreeBSD VM and installs Ninja through `pkg`. If a change adds or modifies another manager/platform mapping, add an equivalent disposable-environment integration job before describing that mapping as integration-tested. Windows and macOS package-manager mappings do not currently have live install jobs.

Commit each changed file in a separate scoped Conventional Commit. Use `scripts/commit-series.ps1` to make a series of one-file commits with explicit messages.
