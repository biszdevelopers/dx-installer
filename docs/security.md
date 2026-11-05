# Security model

DX treats installation as a consequential operation. It inventories first, presents the package identifier or artifact source, scope, install path, integrity digest, size disclosure, setup action, and proposed persistent PATH changes, then requires explicit approval and rescans before execution. A changed plan requires fresh review. Uncertain or broken existing installations are preserved and block replacement.

## Command execution

- Manifests cannot contain shell scripts or arbitrary executable paths. Vendor setup is limited to explicitly enumerated typed actions.
- Package IDs and manager, platform, and scope combinations are validated.
- Package-manager commands use typed argument arrays. The Windows adapter uses a constrained `cmd.exe` invocation only for verified `.bat` and `.cmd` entry points.
- License and source agreements are not silently accepted.
- Package-manager commands run serially. System managers use `sudo` on Unix-like systems only after plan approval.
- Package managers must already be installed; DX never bootstraps one.

## Release and artifact verification

The release generator checks PE, Mach-O, and ELF format and target architecture. It hashes every release file except the separate public-key file, signs the exact `SHA256SUMS` bytes with Ed25519, verifies the generated signature and every staged file, and atomically publishes a new immutable version directory.

Generated entry scripts contain version-specific bootstrap binary hashes. They download over HTTPS, reject unsupported targets, verify the digest before execution, forward arguments and exit status, and clean a unique temporary directory. POSIX redirects are restricted to HTTPS.

Direct artifact URLs are embedded in strict manifests and every download must match its catalog-pinned SHA-256. Regional and user-supplied custom URLs are also required to provide those exact bytes. Redirect counts, allowed redirect hosts, timeouts, retries, download sizes, expanded archive sizes, archive paths, and link targets are bounded. Archives are extracted into temporary staging directories; temporary downloads and registry caches are removed after the operation. The selected tool remains at its intended installation path.

The currently supported Go, Android command-line tools, and Zephyr SDK archives publish SHA-256 checksums but no detached publisher signatures for the archive payloads. DX verifies the catalog-pinned digest; it cannot independently verify a publisher signature that is not published for these files. Package-manager installations rely on each manager's configured repository trust and signature checks.

Offline bundles use `bundle.json` plus an Ed25519 signature verified with the public key pinned in the client. Their target, tool IDs, versions, hashes, per-artifact size limits, and directory contents are checked against the embedded catalog. Unsigned or extra members, symlinks, path traversal, and incompatible targets are rejected.

`SHA256SUMS.pub` is compared with the public key embedded into each release executable. The `verify-release` command verifies the Ed25519 signature and every listed SHA-256 digest. The deployment process separately checks the operator-configured public key before validating an upload.

The entry script itself is initially trusted through the HTTPS origin and its TLS configuration. A compromised origin could replace a script with a malicious one. Users can download and inspect the entry point before running it; one-line PowerShell and curl pipelines do not remove this trust boundary.

## Temporary data and privacy

The CLI creates unique temporary workspaces with owner-only permissions where supported and removes them on exit. It does not create permanent DX configuration, PATH entries, services, scheduled tasks, or telemetry. JSON inventory output may include executable paths, so users should treat explicitly redirected output as local diagnostic data.

The static host sends no-store headers, discards HTTP access logs, has no dynamic routes, and does not receive inventory or installation results. Operating-system package managers may retain their own package caches; DX neither manages nor deletes those shared caches. Registry installer caches are isolated to a temporary DX workspace.

## Trust boundaries and limits

Release binaries and signed offline-bundle metadata use Ed25519. Tool archive payloads use SHA-256 pins in the embedded catalog because the supported publishers do not provide detached signatures for those files. Package-manager installs rely on the manager's configured repository and trust model, and DX does not independently verify repository ownership or configured source URLs. The catalog is compiled into each client release rather than fetched as a separately signed document.

Regional auto-selection uses local locale and time-zone settings as preference signals, not network geolocation. It contacts only catalog-declared mirrors whose payload must match the same embedded digest as the official artifact. No telemetry or external location request is made.
