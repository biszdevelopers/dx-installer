# Architecture

DX is split into four parts: a static release origin, a short-lived Go program, a declarative catalog, and existing operating-system package managers. There is no backend, database, package proxy, telemetry endpoint, or user account system.

## Runtime flow

1. `cmd/bootstrap` loads the embedded catalog and detects OS, architecture, distribution, shell, and installed package managers.
2. `internal/detection` checks each declared executable on `PATH` with a bounded version command. It uses at most eight workers, caps command output, and applies a three-second timeout per version check.
3. `internal/tui` presents the machine state, catalog, profiles, manager preference, and a dry-run plan. The line-oriented UI is available if Bubble Tea cannot render.
4. `internal/planning` expands dependency edges in deterministic topological order. Healthy and unknown existing installations are preserved; broken, partial, conflicting, unsupported, and un-installable selections block execution.
5. After approval, the application rescans inventory. A changed plan returns to the review screen. Commands are argument arrays produced by typed package-manager adapters; they are not shell strings.
6. `internal/installation` runs one package-manager process at a time, skips dependents after a failed dependency, continues unrelated work, then verifies results by rescanning executable versions.
7. The CLI removes its unique temporary workspace on exit.

## Package responsibilities

- `internal/domain/tool`: manifest types, identifiers, platforms, managers, scopes, and validation.
- `internal/catalog`: strict YAML decoding, embedded tools and profiles, dependency graph validation, and explicit selection import/export.
- `internal/platform`: OS/architecture/shell detection and Windows process-PATH refresh.
- `internal/adapters/packagemanager`: existing manager discovery, typed argument construction, display, and process creation.
- `internal/detection`: executable inventory with bounded concurrency and diagnostics.
- `internal/planning`: selection closure, dependency ordering, preservation, and blockers.
- `internal/installation`: serialized execution results and dependency failure handling.
- `internal/tui`: screen state, review/approval, terminal rendering, and fallback input.
- `internal/temporary`: restricted unique temporary workspace lifecycle.
- `internal/security`: Ed25519 checksum verification and safe checksum filename handling.
- `internal/release`: cross-builds, architecture checks, script generation, checksums, and signing.

## Release boundary

`cmd/release` consumes a maintainer-supplied Ed25519 key and HTTPS origin. It writes immutable local `releases/vX.Y.Z` directories. The generated entry scripts download only the versioned bootstrap executable, verify its pinned SHA-256 digest, forward arguments and exit status, and remove their temporary files. The client embeds the release public key through a linker setting.

`deployment/server/Caddyfile` serves static files with no-store response headers and discards HTTP access logs. The SSH deployment script validates the signed release before atomically publishing it and changing `current`. Tool downloads and package-manager traffic do not pass through the VPS.

## Boundaries and current gaps

Inventory is executable-on-PATH detection, not a complete operating-system package database query. A tool installed outside `PATH` may not be discovered. Package-manager adapters cover listed managers, but the repository does not currently include direct vendor archive installers, regional source fallback, custom source configuration, or offline bundles. These features must be added with publisher-verified identifiers and integrity metadata before being represented as supported behavior.

The default release matrix is Windows, macOS, and Linux on amd64 and arm64. BSD platform identifiers and a FreeBSD package-manager adapter exist in the client code, but BSD is not in default release generation or the hosted integration matrix.
