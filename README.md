# Developers' Club DX

DX is a cross-platform developer-tool installer with a full-screen, mouse-clickable terminal interface. It detects tools already on your machine, helps you choose tools or a developer profile, and shows an installation plan for review before it runs anything. The DX program itself is a single executable; it does not install itself or add itself to your `PATH`.

## Start DX from a published release

The hosted commands below work after a signed release has been published to `dx.bisz.dev`. If the site has no published release yet, use [Build and run from source](#build-and-run-from-source).

### Windows PowerShell

Open PowerShell or Windows Terminal and run:

```powershell
irm https://dx.bisz.dev/install.ps1 | iex
```

`irm` downloads the PowerShell launcher and `iex` runs it. The launcher detects Windows x64 or ARM64, downloads the matching DX executable to a temporary folder, checks its SHA-256 digest, starts DX, and removes the temporary executable when DX exits.

To use a read-only command instead of opening the interface, fetch the launcher as a script block and pass a command:

```powershell
$dx = [scriptblock]::Create((irm 'https://dx.bisz.dev/install.ps1'))
& $dx catalog
& $dx inventory
& $dx plan --tools 'git,github-cli'
```

### macOS, Linux, and FreeBSD

Open a terminal and run:

```sh
curl -fsSL https://dx.bisz.dev/install.sh | sh
```

The launcher detects the operating system and CPU architecture, downloads the matching executable to a temporary folder, verifies its SHA-256 digest, starts DX, and removes the temporary executable when DX exits. When the launcher is piped into `sh`, it reconnects DX to the terminal so the interface can receive keyboard and mouse input.

Pass a read-only command after `--` to the shell:

```sh
curl -fsSL https://dx.bisz.dev/install.sh | sh -s -- catalog
curl -fsSL https://dx.bisz.dev/install.sh | sh -s -- inventory
curl -fsSL https://dx.bisz.dev/install.sh | sh -s -- plan --tools git,github-cli
```

These launchers require an attached terminal for the interactive installer. They do not install Go or a package manager on the target machine. Package managers used for tool installation must already be present.

## Your first install

DX opens to **Home**. Use the sidebar to open **Tools** and select individual tools, or open **Profiles** and choose a starter set. DX shows selected tools, detected tools, and installation details as you work.

Open **Plan** and review each item, including its source, version, download size and digest when available, destination, dependencies, and any setup or PATH changes. Choose the install action and approve the refreshed plan. DX rescans the machine immediately before approval; if the plan changed or became blocked, it returns to review. Package-manager prompts remain visible. Existing tools are preserved rather than upgraded automatically.

Click visible controls or use the keyboard. Press `Tab` / `Shift+Tab` to move focus, arrow keys to move between rows, `Space` to select, `Enter` to activate, `Page Up` / `Page Down` to scroll, `Ctrl+P` to open the command palette, `?` for help, and `Esc` to close a dialog. Mouse input depends on terminal support; keyboard navigation remains available. The layout adjusts to smaller terminals. See the [terminal interface guide](docs/tui-design.md) for details.

## Build and run from source

Install Go 1.26 or later, then run these commands from the repository folder:

```powershell
go run ./cmd/bootstrap
go run ./cmd/bootstrap catalog
go run ./cmd/bootstrap inventory
go run ./cmd/bootstrap plan --tools git,github-cli
go run ./cmd/catalog-check
```

`catalog` lists the embedded tools, `inventory` reports tools detected on the current machine, and `plan` previews changes without installing anything. To build a single executable for the current Windows machine:

```powershell
go build -trimpath -o dx-bootstrap.exe ./cmd/bootstrap
```

On macOS or Linux, use `-o dx-bootstrap` instead. The Go release tool can cross-build the complete supported release set and create the download launchers; see [Build and publish a signed release](docs/release-process.md).

## Tool availability and machine changes

The catalog contains 221 tool manifests and 18 editable profiles. Availability varies by operating system and CPU architecture; see the current [platform support matrix](docs/platform-support.md) before choosing a tool for another machine. A declared route depends on its package manager or runtime being installed and configured. Some tools use an explicitly labeled native equivalent where the original tool is unavailable; the plan describes compatibility limits.

DX does not install package managers, add package repositories, or run package-manager commands before you approve a reviewed plan. Some managers may request system-level permission for a tool whose installation is system-scoped; the plan identifies that scope. Tool installations remain on the machine according to the selected package manager or install path. DX itself runs from a temporary location when started by the hosted launcher and is removed when it exits.

## Catalog, sources, and offline installs

The catalog includes exact-version registry entries and catalog-pinned direct archives for tools that declare them. Direct archives are checked against embedded SHA-256 pins before installation. Plans disclose the selected source, install target, and persistent PATH changes. The **Advanced** area contains source policies, package-manager preferences, custom sources, and offline options.

From **Results**, save a redacted JSON diagnostic report when you need help. DX creates it only after you request one and will not overwrite an existing file. Read more about [source selection](docs/source-selection.md), [regional sources](docs/regional-support.md), and [offline installation](docs/offline-installation.md).

## Build and publish a signed release

For a hosted release, choose a new immutable version and supply a persistent Ed25519 signing key from a secure location outside the repository. Set the public origin to `https://dx.bisz.dev`:

```powershell
go test ./...
go vet ./...
go run ./cmd/catalog-check
go run ./cmd/release `
  --version v1.2.3 `
  --origin https://dx.bisz.dev `
  --output-dir .\dist `
  --signing-key C:\secure\dx-release-ed25519.pem
```

The generated release includes Windows, macOS, and Linux executables for x64 and ARM64, FreeBSD x64, and the `install.ps1` and `install.sh` launchers shown above. Before publishing, verify the signed release and follow the [VPS deployment guide](docs/vps-deployment.md). The repository does not contain a production signing key or server credentials. Keep the signing key for future releases; the client pins its matching public key.

## Verification

Run `go test ./...`, `go vet ./...`, and `go run ./cmd/catalog-check` for local checks. CI builds natively on Windows, macOS, and Linux for x64 and ARM64. Its live package-manager installs cover a smaller set than the catalog; see the [platform support guide](docs/platform-support.md) for the tested combinations and uncovered tools.

For architecture, source, security, and rollback details, see [quick start](docs/quick-start.md), [release process](docs/release-process.md), [security](docs/security.md), and [VPS deployment](docs/vps-deployment.md).
