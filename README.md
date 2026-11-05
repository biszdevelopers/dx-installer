# Developers' Club DX Bootstrap

DX is a temporary, approval-driven developer-tool selector. It inventories executables available on the current `PATH` and package records from supported local package databases, lets a user select tools, resolves declared dependencies, previews package-manager commands, and verifies tools after approved installs. The Go client embeds its strict YAML catalog and does not install or persist itself.

## Run from source

Use Go 1.26 or later:

```powershell
go run ./cmd/bootstrap
go run ./cmd/bootstrap catalog
go run ./cmd/bootstrap inventory --json
go run ./cmd/bootstrap plan --tools git,github-cli
go run ./cmd/bootstrap plan --tools git,github-cli --manager winget
go run ./cmd/bootstrap plan --tools go --source-policy official-first
go run ./cmd/catalog-check
go test ./...
```

The interactive install flow requires a terminal. It refreshes inventory after approval and asks for approval again if the plan changed. `plan` is read-only. The application never bootstraps a package manager, never adds a package repository, and never runs a package-manager command without approval.

The embedded catalog contains 219 tool manifests and 18 profiles. Package-manager installations use only managers already detected on the machine. Some tools also have catalog-pinned direct archives, registry installers, regional mirrors, and signed offline-bundle support; availability depends on the selected tool and platform. Direct archives and local copies are checked against embedded SHA-256 pins, and signed bundles are verified against the public key pinned in the client. Plans show sources, integrity data, size disclosures, install paths, setup actions, and persistent PATH changes before approval.

Inventory checks executables on `PATH`, a bounded set of standard locations, and supported local package databases. Package records without a discoverable executable are preserved as unknown; failed manager queries also leave affected tools unknown. WinGet inventory uses a temporary export and may consult configured sources; DX does not accept source agreements. From Results, press `d` to save an explicitly requested redacted JSON diagnostic report; reports are created as new files and never overwrite existing paths. See [platform support](docs/platform-support.md), [source behavior](docs/source-selection.md), [regional behavior](docs/regional-support.md), and [offline installation](docs/offline-installation.md) for the exact support boundaries.

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

Run `go test ./...`, `go vet ./...`, and `go run ./cmd/catalog-check`. CI runs Go tests, static analysis, manifest validation, and native builds on Windows, macOS, and Linux for amd64 and arm64. The integration workflow installs Ninja through apt, dnf, pacman, zypper, and apk in disposable containers, Ninja through `pkg` in a FreeBSD 15.1 VM, SQLite through Chocolatey on Windows amd64, and OpenOCD through Homebrew on macOS amd64 and arm64. Release publication waits for these jobs. Scoop, WinGet, MacPorts, and the remaining manager/tool pairs do not have live install coverage.
