# Quick start

## Build and inspect

Install Go 1.26 or later, clone the repository, and run:

```powershell
go run ./cmd/catalog-check
go run ./cmd/bootstrap catalog
go run ./cmd/bootstrap inventory
```

Use `--json` with `catalog` or `inventory` for structured output. Inventory reports executable paths and versions found on the current `PATH` and the package managers already present.

## Preview a plan

```powershell
go run ./cmd/bootstrap plan --tools git,github-cli
```

Planning is read-only. A plan lists dependencies, manager, package identifier, configured manager source, scope, command, and blocking issues. If inventory is uncertain or a tool is already installed, the tool is preserved. A plan with a blocker exits without executing anything.

## Install interactively

Run `go run ./cmd/bootstrap` in an attached terminal, move to Catalog, select tools with Space, then open Plan and press `p`. Review all operations and press `i` to request approval. Type `y` to approve after the application rescans inventory. If the plan changed, review and approve the new plan. Package-manager prompts stay visible. Press `q` or Ctrl+C to exit.

The numbered screens are Overview, Catalog, Profiles, Environment, Plan, Sources, Progress, and Results. Use Tab or 1–8 to navigate; `j`/`k` or arrow keys move through lists; `f` searches; `[` and `]` filter categories; `s` changes installation-state filters; `e` exports a selection and `o` imports one. Help is available with `?`.

From Results, press `d` and enter a new path to save a redacted JSON diagnostic report. It omits command lines and executable paths, redacts credentials and user-directory paths, and refuses to overwrite an existing file.

For a deliberately noninteractive run, preselect tools and pass `--yes`:

```powershell
go run ./cmd/bootstrap install --tools git,go --yes
```

The reviewed plan is printed before execution. `--yes` is required, and a changed plan is not automatically approved; the line fallback stops when it cannot collect renewed approval.

The Sources screen currently selects a preferred detected package manager. It does not set an official-only policy, custom source, mirror, or offline archive. See [source behavior](source-selection.md).

## Tests and checks

```powershell
gofmt -w <changed-Go-files>
go test ./...
go vet ./...
go run ./cmd/catalog-check
```

No test command invokes package managers to install software. Live integration tests need disposable machines and explicit operator setup.
