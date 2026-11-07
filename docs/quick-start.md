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

Planning is read-only. A plan lists dependencies, manager, package identifier and kind, configured manager source, scope, command, candidate version, package operations, and blocking issues. APT, DNF, YUM, Pacman, Zypper, APK, and FreeBSD pkg use non-installing dry runs against existing repository metadata. Chocolatey uses its no-op mode, Homebrew uses --dry-run with automatic updates disabled, and Scoop and MacPorts show candidate metadata and dependency lists while disclosing that those lists are not full transaction simulations. WinGet and Android SDK tools inspect available-version metadata; Espressif EIM shows the catalog-pinned release. Managers that cannot show a complete transaction identify that uncertainty in the plan.

DX does not run an explicit repository refresh while planning and does not accept package-source or SDK license agreements. WinGet, Chocolatey, Homebrew, and Android SDK queries may contact their configured sources to inspect current metadata. Cached and source-backed previews can become stale before installation; review each preview and any manager confirmation before accepting the plan. If inventory is uncertain or a tool is already installed, the tool is preserved. A plan with a blocker exits without executing anything.

## Install interactively

Run `go run ./cmd/bootstrap` in an attached terminal, or use the hosted launchers described in the [README](../README.md#start-dx-from-a-published-release). The app opens on Home. Open Tools to select items, or Profiles to start with a set. Open Plan and review the install, preserve, and blocked summaries and each tool's source and disclosures. Choose the install action and approve after DX refreshes inventory. If the plan changes, review and approve the new plan. Package-manager prompts stay visible. Press `q` or Ctrl+C to exit.

The persistent sections are Home, Tools, Profiles, Environment, Plan, Activity, and Results; source settings are under Advanced. Click visible navigation, rows, filters, and buttons, or use `Tab` / `Shift+Tab` to move focus, arrows to move through rows, `Space` to select, and `Enter` to activate. Press `Page Up` / `Page Down` to scroll, `Ctrl+P` to open the command palette, `?` for Help, and `Esc` to close a dialog. At narrow terminal sizes, the app becomes a scrollable single pane. See the [terminal interface guide](tui-design.md) for layouts and controls.

From Results, press `d` and enter a new path to save a redacted JSON diagnostic report. It omits command lines and executable paths, redacts credentials and user-directory paths, and refuses to overwrite an existing file.

For a deliberately noninteractive run, preselect tools and pass `--yes`:

```powershell
go run ./cmd/bootstrap install --tools git,go --yes
```

Archive downloads default to three concurrent workers and a ten-minute timeout per source. Set `--download-concurrency` from 1 to 8 and `--download-timeout` from `1s` to `24h` to adjust them:

```powershell
go run ./cmd/bootstrap install --tools go,cmake --download-concurrency 2 --download-timeout 15m
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
