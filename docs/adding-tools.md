# Adding a tool

1. Confirm the executable and harmless version arguments on each claimed platform. Avoid arguments that execute code, open a shell, or mutate state.
2. Verify the exact package ID and source label against maintained package-manager metadata, or verify the publisher's artifact URL, version, archive format, and SHA-256 from an authoritative release source. Do not invent hashes or rely on an unverified mirror.
3. Declare only tested operating systems and architectures. Choose typed package-manager, exact-version registry, or direct archive installer entries that are appropriate for each platform. Keep the upgrade policy at `never_automatically`.
4. For direct archives, declare the safe archive root and any download/expanded-size limits, redirect hosts, version marker, PATH entries, and installation disclosure. Keep vendor setup in a typed, allowlisted action; never add a shell fragment to YAML. Test archive traversal, unsafe links, malformed files, and configured size limits where relevant.
5. Add the manifest under `internal/catalog/data/tools/` and update the Complete profile plus any relevant developer profile. Add or refine focused manifest, planning, detection, and adapter tests for new behavior.
6. Run `go run ./cmd/catalog-check`, `go test ./...`, and `go vet ./...`. Inspect `go run ./cmd/bootstrap plan --tools <id>` on a disposable platform and exercise each claimed installer in a disposable environment before calling it supported.
7. Commit each changed file separately with a specific Conventional Commit using `scripts/commit-series.ps1`.

An embedded manifest does not prove that a manager installs the intended publisher package or that an artifact works on every declared platform. Record live install coverage accurately and keep unsupported manager/tool pairs out of claims about tested support.
