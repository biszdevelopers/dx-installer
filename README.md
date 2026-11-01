# Developers' Club DX Bootstrap

DX is a temporary, approval-driven developer-tool selector. It inventories executables available on the current `PATH`, lets a user select tools, resolves declared dependencies, previews package-manager commands, and verifies tools after approved installs. The Go client embeds its strict YAML catalog and does not install or persist itself.

## Run from source

Use Go 1.26 or later:

```powershell
go run ./cmd/bootstrap
go run ./cmd/bootstrap catalog
go run ./cmd/bootstrap inventory --json
go run ./cmd/bootstrap plan --tools git,github-cli
go run ./cmd/catalog-check
go test ./...
```

The interactive install flow requires a terminal. It refreshes inventory after approval and asks for approval again if the plan changed. `plan` is read-only. The application never bootstraps a package manager, never adds a package repository, and never runs a package-manager command without approval.

The catalog currently contains 17 tool manifests and 10 profiles. Package-manager installations use only package managers already detected on the machine. The source screen lets the user prefer one of those managers; it does not yet implement direct publisher downloads, custom source overrides, regional mirror selection, or offline bundle installation. Inventory currently verifies executable names found on `PATH`; it does not query every operating-system package database or search arbitrary disk locations. See [platform support](docs/platform-support.md) and [source behavior](docs/source-selection.md) for the exact boundaries.

## Build a signed release

The release tool cross-builds the default Windows, macOS, and Linux matrix (amd64 and arm64), verifies binary formats and architectures, generates versioned PowerShell and POSIX entry points containing artifact digests, signs `SHA256SUMS` with Ed25519, and verifies the staged output before publishing it locally.

```powershell
go run ./cmd/release `
  --version v1.2.3 `
  --origin https://your-real-hostname `
  --output-dir .\dist `
  --signing-key C:\secure\dx-release-ed25519.key
```

The command requires an operator-supplied signing key and real HTTPS origin. The repository intentionally contains neither. Release directories are immutable; choose a new semantic version for each build. An already-built client can run `dx-bootstrap verify-release --directory PATH` to verify the signed checksums with the public key pinned into that executable.

## Static distribution

The server is only a static file origin. Caddy configuration disables access logs and sends no-store headers. The deployment scripts upload to a hidden staging directory, verify the pinned Ed25519 signature and checksums on the host, atomically publish `releases/vX.Y.Z`, and atomically switch the `current` symlink. Previous releases remain available for rollback. They require an operator-provided DNS name, Caddy TLS setup, SSH host key, deployment account, and signing key; no production host or credentials are fabricated here.

See [architecture](docs/architecture.md), [quick start](docs/quick-start.md), [release process](docs/release-process.md), and [VPS deployment](docs/vps-deployment.md).

## Current verification

Run `go test ./...`, `go vet ./...`, and `go run ./cmd/catalog-check`. CI is configured to run the Go tests, static analysis, manifest validation, and native build on Windows, macOS, and Linux for amd64 and arm64. That matrix does not exercise live package-manager installs or BSD; no release defaults to BSD.
