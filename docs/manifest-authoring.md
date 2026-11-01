# Manifest authoring

Tool manifests are embedded YAML files under `internal/catalog/data/tools/`. The canonical JSON Schema is `internal/catalog/schema/tool.schema.json`; runtime validation is stricter and is performed by `internal/domain/tool.Manifest.Validate` and `internal/catalog.Load`.

Every manifest needs schema version 1, a stable lowercase ID, a dotted lowercase category, a human-readable description, a version channel, and `upgrade_policy: never_automatically`. Executable names must be plain command names. Version arguments are passed directly to the executable, without a shell, and command/evaluation flags are rejected. Every platform architecture and installer must be explicit.

Installer entries name a package manager, exact package ID, scope, and source label. Scope must match manager capabilities. Tool dependencies and conflicts use catalog IDs and are checked for unknown references and dependency cycles. YAML fields not declared in the Go model are rejected.

Manifests must not declare arbitrary install commands, scripts, URLs, unverified hashes, or repository configuration. Verify package IDs and installer scope against the manager's maintained metadata before adding them. Add a profile only when its complete tool IDs exist in the catalog, then run `go run ./cmd/catalog-check` and `go test ./...`.
