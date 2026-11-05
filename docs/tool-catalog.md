# Embedded tool catalog

The embedded catalog contains **220 tool manifests and 18 profiles**. The complete, current list is available from `go run ./cmd/bootstrap catalog`; `go run ./cmd/bootstrap catalog --json` emits the manifest data as JSON. Source manifests are in `internal/catalog/data/tools/`, and profiles are in `internal/catalog/data/profiles/`.

The catalog covers version control, JavaScript and TypeScript, JVM, C and C++, Python, Go, Rust, .NET, PHP, Ruby, Swift, Dart and Flutter, Zig, Lua, BEAM, Haskell, OCaml, Clojure, Julia, R, Perl, Fortran, additional languages, mobile and embedded development, database clients, API and protocol tooling, containers and DevOps, security and code quality, and documentation utilities.

The 18 editable profiles are Additional languages, API and protocol tooling, Complete catalog, Database clients, DevOps tooling, .NET development, Embedded development, Essential, Full-stack, Go, Java, Mobile development, Python, Rust, Security and code quality, Swift, Systems programming, and Web development. Applying a profile changes the selection; it does not execute an installation.

## Manifest and installer model

Manifests declare tool IDs, version channels, no-automatic-upgrade policy, executables and version arguments, common and platform-specific dependencies, conflicts, supported platforms, and compatible installer methods. Installer methods include existing OS package managers, exact-version npm/PyPI/Go/Cargo registry entries, and catalog-pinned direct archives. Direct archives may include size limits, digest-verified regional URLs, expected archive roots, version markers, PATH entries, user-visible setup actions, and installation disclosures. Manifests cannot contain arbitrary shell fragments.

The runtime loader uses typed Go structures and strict YAML decoding. It rejects unknown fields, invalid manager/platform/scope combinations, malformed artifact metadata, duplicate IDs, missing dependencies, and dependency cycles. `go run ./cmd/catalog-check` validates the embedded catalog and profiles.

## Availability and verification

Catalog entries are declarations, not a promise that every installer is available on every machine. The selected tool must match the detected OS and architecture, and the declared package manager or runtime must be present. Direct archive and registry methods are also limited to the exact platform and tool entries in each manifest. A plan reports the chosen source, command, integrity digest, install target, and any persistent PATH change before approval.

CI exercises native builds on Windows, macOS, and Linux amd64/arm64. Live package-manager integration currently covers Ninja through APT, DNF, Pacman, Zypper, APK, and FreeBSD `pkg`; SQLite through Chocolatey on Windows amd64; and OpenOCD through Homebrew on macOS amd64 and arm64. Other declared manager/tool pairs are not claimed as live-tested. See [platform support](platform-support.md) for the current matrix.

When adding a manifest, verify each package identifier and source against maintained upstream or package-manager metadata, check publisher and scope, declare only tested platform mappings, update relevant profiles, add focused validation coverage, and run the catalog checker.
