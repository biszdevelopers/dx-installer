# Quick start

## Build and inspect

Install Go 1.26 or later, clone the repository, and run:

```powershell
go run ./cmd/catalog-check
go run ./cmd/bootstrap catalog
go run ./cmd/bootstrap inventory
```

Use `--json` with `catalog` or `inventory` for structured output. Inventory reports executable paths and versions found on the current `PATH` and in a bounded set of standard installation directories, along with package managers already present.

## Preview a plan

```powershell
go run ./cmd/bootstrap plan --tools git,github-cli
```

Planning is read-only. A plan lists dependencies, manager, package identifier, configured manager source, scope, command, and blocking issues. DX does not yet query candidate package versions or simulate the package manager's full transaction; the plan discloses this uncertainty, and any manager confirmation should be reviewed before it is accepted. If inventory is uncertain or a tool is already installed, the tool is preserved. A plan with a blocker exits without executing anything.

## Install interactively

Run `go run ./cmd/bootstrap` in an attached terminal, move to Catalog, select tools with Space, then open Plan and press `p`. Review all operations and press `i` to request approval. Type `y` to approve after the application rescans inventory. If the plan changed, review and approve the new plan. Package-manager prompts stay visible. Press `q` or Ctrl+C to exit.

The numbered screens are Overview, Catalog, Profiles, Environment, Plan, Sources, Progress, and Results. Use Tab or 1–8 to navigate; `j`/`k` or arrow keys move through lists; `f` searches; `[` and `]` filter categories; `s` changes installation-state filters; `e` exports a selection and `o` imports one. Help is available with `?`.

From Results, press `d` and enter a new path to save a redacted JSON diagnostic report. It omits command lines and executable paths, redacts credentials and user-directory paths, and refuses to overwrite an existing file.

For a deliberately noninteractive run, preselect tools and pass `--yes`:

```powershell
go run ./cmd/bootstrap install --tools git,go --yes
```

The reviewed plan is printed before execution. `--yes` is required, and a changed plan is not automatically approved; the line fallback stops when it cannot collect renewed approval.

The Sources screen can prefer a detected manager, select the automatic, official-only, official-first, mainland-China, or global policy, configure a catalog-pinned custom archive URL or local archive, set an npm/PyPI/Go/Cargo registry, or load a signed offline bundle. The same options are available to `plan` and `install` as `--source-policy`, repeatable `--custom-source tool-id=https://URL`, `--offline-archive tool-id=absolute-path`, repeatable `--registry ecosystem=https://URL`, and `--offline-bundle absolute-directory` flags. For example:

```powershell
go run ./cmd/bootstrap plan --tools go --source-policy official-only
go run ./cmd/bootstrap install --tools go --offline-archive go=C:\artifacts\go1.27.3.windows-amd64.zip --yes
```

Direct archive and local sources remain bound to the SHA-256 digest in the embedded catalog. Options apply only to tools with the matching artifact or registry installer. See [source behavior](source-selection.md), [regional behavior](regional-support.md), and [offline installation](offline-installation.md).

## Tests and checks

```powershell
gofmt -w <changed-Go-files>
go test ./...
go vet ./...
go run ./cmd/catalog-check
```

These local check commands do not invoke package managers to install software. The `Package manager integration` GitHub Actions workflow provisions disposable Linux containers and a FreeBSD VM for install checks; running live installs locally requires a separate disposable environment.
