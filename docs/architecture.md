# Architecture

DX consists of a static release origin, a short-lived Go application, an embedded declarative catalog, and existing operating-system package managers or verified direct sources. There is no provisioning backend, database, package proxy, telemetry endpoint, or user account system.

## Runtime flow

1. `cmd/bootstrap` loads the embedded catalog and detects OS, architecture, distribution, shell, installed managers, region preference signals, and existing tool state.
2. `internal/detection` checks declared executables on `PATH` and a bounded set of standard locations with bounded version commands. It queries supported package databases with timeouts and output limits; an installed package without a discoverable executable remains unknown.
3. `internal/tui` presents the machine state, catalog, profiles, source options, and a dry-run plan. A line-oriented terminal flow is available when Bubble Tea cannot render.
4. `internal/planning` expands common and platform-specific dependency edges in deterministic topological order. Healthy and unknown existing installations are preserved; broken, partial, conflicting, unsupported, and un-installable selections block execution. Source choices and all installation metadata are included in plan approval.
5. After explicit approval, DX rescans inventory. If the plan has changed, it returns for review. Package-manager commands are typed argument arrays. Direct artifact installs use catalog-pinned hashes, bounded downloads, safe archive extraction, versioned user-level paths, and planned PATH updates. Registry installs use exact catalog versions and temporary caches. Offline bundles are verified before their artifacts enter the plan.
6. Installation runs one operation at a time, skips dependents after a failed dependency, continues unrelated work, and verifies results by rescanning executable versions and architecture where available.
7. The CLI removes its unique temporary workspace on exit. The selected tools remain installed; temporary bootstrap files, downloads, and registry caches are cleaned up.

## Package responsibilities

- `internal/domain/tool`: manifest types, identifiers, platforms, managers, scopes, artifact limits, and validation.
- `internal/catalog`: strict YAML decoding, embedded tools and profiles, dependency graph validation, and explicit selection import/export.
- `internal/platform`: OS, architecture, shell and distribution detection, plus Windows process-PATH refresh.
- `internal/adapters/packagemanager`: detection, typed command construction, process execution, and package inventory queries for WinGet, Scoop, Chocolatey, Homebrew, MacPorts, APT, DNF, Pacman, Zypper, APK, FreeBSD `pkg`, Android SDK tools, and Espressif EIM.
- `internal/adapters/registry`: exact-version npm, PyPI, Go module, and Cargo installers with temporary cache locations.
- `internal/detection`: executable and supported package-database inventory with bounded output, timeouts, architecture checks, and diagnostics.
- `internal/planning`: selection closure, dependency ordering, preservation, source choice, install paths, PATH disclosures, and blockers.
- `internal/sources`: HTTPS validation, bounded retries and redirects, regional fallback, SHA-256 verification, and local archive copies.
- `internal/artifacts`: bounded ZIP, tar.gz, tar.xz, and 7z extraction with path and link safety checks, temporary staging, and typed vendor setup actions.
- `internal/offline`: signed, platform-specific bundle creation and validation against catalog pins and the embedded Ed25519 key.
- `internal/pathconfig`: duplicate-safe user PATH planning and explicit persistent changes.
- `internal/installation`: serialized execution, results, verification state, and dependency failure handling.
- `internal/temporary`: unique temporary workspace lifecycle.
- `internal/security`: Ed25519 release verification, safe checksum filenames, and key handling.
- `internal/release`: cross-builds, architecture checks, script generation, checksums, signing, and local release verification.

## Source and integrity boundary

The catalog is compiled into the release executable. Package-manager installs rely on the manager's configured repositories. Direct artifact sources are selected per tool and platform: official publisher URLs are default; an explicitly selected policy can allow catalog-declared regional fallback; a user may provide a custom HTTPS URL or local archive for a catalog-pinned artifact. Every direct payload must match its embedded SHA-256. The supported Go, Android command-line tools, and Zephyr SDK archives publish checksums but no detached publisher signatures, so DX cannot independently verify signatures for those archive payloads. Registry overrides apply only to manifests with a matching ecosystem installer and do not modify persistent registry configuration. Signed offline bundles bind a platform, tool IDs, versions, hashes, and size limits to metadata signed by the key pinned in the client.

The regional policy uses local locale and time-zone signals only; it does not geolocate the network. The current catalog has digest-pinned HUST regional mirrors for every declared Go archive (Windows amd64/arm64, macOS amd64/arm64, Linux amd64/arm64, and FreeBSD amd64). Regional mirror coverage remains partial: Android SDK Command-Line Tools and Zephyr SDK have no verified mirror entry and use official URLs only. Direct archives, registry installers, and package-manager mappings are independently declared per manifest; a feature being implemented does not mean it is available for every tool/platform pair.

## Release boundary

`cmd/release` consumes a maintainer-supplied Ed25519 key and HTTPS origin. It writes immutable local `releases/vX.Y.Z` directories. Generated entry scripts download only the versioned bootstrap executable, verify its pinned SHA-256 before execution, forward arguments and exit status, and remove their temporary files. The client embeds the release public key through a linker setting.

`deployment/server/Caddyfile` serves static files with no-store response headers and discards access logs. The SSH deployment script validates a signed release before atomically publishing it and switching `current`. Tool downloads and package-manager traffic do not pass through the VPS. A real DNS name, VPS, SSH identity, signing key, and protected release tag are operator-supplied prerequisites; no production endpoint or credential is included here.

## Boundaries and remaining limits

Inventory is deliberately bounded and can miss nonstandard install locations. Health checks currently confirm declared version commands rather than exercising each tool's build or runtime functionality. Package-manager mappings are broader than live integration coverage: CI exercises Ninja through APT, DNF, Pacman, Zypper, APK, and FreeBSD `pkg`; SQLite through Chocolatey on Windows amd64; and OpenOCD through Homebrew on macOS amd64 and arm64. Other manager/tool combinations are not claimed as live-tested. FreeBSD amd64 is the only supported BSD install target; other BSD systems are detected but have no catalog install adapter.

The catalog is embedded rather than updated remotely. The currently supported archive families do not publish detached signatures for their archive payloads; DX verifies their catalog-pinned SHA-256 digests. Inventory does not perform arbitrary filesystem-wide discovery. TUI automated coverage includes golden snapshots for all eight screens, per-screen resize bounds, Unicode display width, ASCII fallback, source-plan invalidation, fallback behavior, and environment-search fuzzing.
