# Security model

DX treats installation as a consequential operation. It inventories first, presents the package identifier, source label, scope, and arguments, requires explicit approval, rescans before execution, and verifies the resulting executable. Uncertain or broken existing installations are preserved and block replacement.

## Command execution

- Manifests cannot contain shell scripts or executable paths.
- Package IDs and manager/platform/scope combinations are validated.
- Adapters build `exec.CommandContext` argument arrays; no shell evaluates a concatenated command string.
- License and source agreements are not silently accepted.
- Package-manager commands run serially. System managers use `sudo` on Unix-like systems only after plan approval.
- Package managers must already be installed; DX never bootstraps one.

## Release verification

The release generator checks PE, Mach-O, and ELF format and target architecture. It hashes every release file except the separate public-key file, signs the exact `SHA256SUMS` bytes with Ed25519, verifies the generated signature and every staged file, and atomically publishes a new immutable version directory.

The generated entry scripts contain version-specific bootstrap binary hashes. They download over HTTPS, reject unsupported targets, verify the digest before execution, forward arguments and exit status, and clean a unique temporary directory. POSIX curl redirects are restricted to HTTPS. No downloaded tool archive is retained by DX because direct archive installers are not implemented yet.

`SHA256SUMS.pub` is compared with the public key embedded into each release executable. The `verify-release` command verifies the Ed25519 signature and every listed SHA-256 digest. The deployment process separately checks the operator-configured public key before validating an upload.

The entry script itself is initially trusted through the HTTPS origin and its TLS configuration. A compromised origin could replace a script with a malicious one. Users can download and inspect the entry point before running it; the one-line PowerShell and curl pipelines do not remove this trust boundary.

## Temporary data and privacy

The CLI creates a unique temporary workspace with owner-only permissions where supported and removes it during exit. It does not create permanent DX configuration, PATH entries, services, tasks, or telemetry. JSON inventory output may include executable paths, so users should treat explicitly redirected output as local diagnostic data.

The static host sends no-store headers, discards HTTP access logs, has no dynamic routes, and does not receive inventory or installation results. Operating-system package managers may retain their own package caches; DX neither manages nor deletes those shared caches.

## Limits

SHA-256 protects generated bootstrap binaries from substitution relative to the downloaded script, and Ed25519 authenticates release checksum lists. Catalog package identifiers are reviewed as source code, but current installers rely on the package manager's configured trust and repository. There is no signed external catalog, direct publisher signature verifier, regional-source trust store, archive extraction engine, or offline bundle verifier in the current client.
