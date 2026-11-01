# Adding a tool

1. Confirm the tool's executable and harmless version arguments on each claimed platform. Avoid arguments that execute code, open a shell, or mutate state.
2. Verify the exact package identifier, package-manager source, platform, architecture, and supported install scope from maintained package metadata.
3. Add one strict YAML manifest in `internal/catalog/data/tools/`. Declare only tested architectures and manager mappings. Set `upgrade_policy` to `never_automatically`.
4. Add the tool to relevant profile manifests without duplicating IDs.
5. Run `go run ./cmd/catalog-check`, `go test ./...`, `go vet ./...`, and inspect `go run ./cmd/bootstrap plan --tools <id>` on a disposable platform with the corresponding manager.
6. Add regression tests when new validation or adapter behavior is needed. Commit the manifest/profile/test changes one file at a time with `scripts/commit-series.ps1`.

An embedded manifest does not prove that the manager installs the intended publisher package. Do not label a platform supported until the package ID, prompts, resulting executable path, and verification command have been exercised in a disposable environment.
