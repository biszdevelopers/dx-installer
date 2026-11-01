# Developers' Club DX Bootstrap

DX is a temporary, approval-driven toolchain bootstrapper. It inventories the current machine, lets the user select tools, resolves declared dependencies, previews package-manager operations, and verifies selected tools after installation.

The client is written in Go. Its catalog is embedded in the executable and uses strict, typed YAML manifests. It does not install DX, create a `dx` command, keep a package cache, or send telemetry.

## Current build

The repository is being initialized from the production specification. Build and run the interactive application with Go 1.26 or later:

```powershell
go run ./cmd/bootstrap
```

List the embedded catalog or inspect the detected environment without starting the interface:

```powershell
go run ./cmd/bootstrap catalog
go run ./cmd/bootstrap inventory
go run ./cmd/catalog-check
```

Run the Go test suite with `go test ./...` and format Go files with `gofmt -w`.

## Bootstrap distribution

`bootstrap/install.ps1` and `bootstrap/install.sh` are release templates. The release tool fills their version, origin, platform artifact names, pinned SHA-256 digests, and public key from maintainer-supplied release configuration. A generated entry point refuses to run while required release values are absent. No production domain, signing key, or binary digest is embedded in this source tree.

The VPS deployment files describe static HTTPS hosting with no application backend, package proxy, or cache. Deployment requires the operator to provide a real hostname and SSH access.

## Security and behavior

- Only selected catalog entries and their declared dependencies enter a plan.
- Existing tools are preserved; unknown, broken, or conflicting installations are surfaced for review.
- The user approves a complete plan before any package-manager command runs.
- Package managers must already be present. DX never bootstraps a package manager.
- Commands are built by typed adapters from executable names, fixed argument templates, and validated package identifiers. Manifest data cannot supply shell scripts.
- System package managers that need elevated privileges are called through `sudo` only after the reviewed plan is approved.
- Each install result is checked with the tool's declared version command.
- The bootstrap binary is intended to run from temporary storage. Only explicitly selected development tools persist.

See [docs/architecture.md](docs/architecture.md), [docs/security.md](docs/security.md), and [docs/vps-deployment.md](docs/vps-deployment.md) for operational details.
