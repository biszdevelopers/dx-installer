# Contributing

Use Go 1.26 or later. Keep tool-specific behavior in the tool manifest and keep command construction in typed adapters. Manifests must not include arbitrary commands, shell fragments, or unverified download URLs.

Before submitting a change, run:

```powershell
gofmt -w <changed-go-files>
go test ./...
go run ./cmd/catalog-check
```

Add or update focused tests with behavior changes. Do not claim platform support based only on cross-compilation. Installation support must be exercised against the named package manager and a disposable environment.

Commit each changed file in a separate scoped Conventional Commit. Use `scripts/commit-series.ps1` to make a series of one-file commits with explicit messages.
