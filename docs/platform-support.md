# Platform support

Platform detection maps Go targets to Windows, macOS, Linux, and BSD families. Linux distribution identification reads `ID` and `VERSION_ID` from `/etc/os-release`. Manifest support is per tool: a tool can be installed only when its declared platform and architecture match and a compatible installer is available.

## Release and install targets

| OS | Bootstrap release architectures | Available installer families |
| --- | --- | --- |
| Windows | amd64, arm64 | WinGet, Scoop, Chocolatey; Android SDK and Espressif EIM special-purpose installers; catalog-pinned archives and registry installers where declared |
| macOS | amd64, arm64 | Homebrew, MacPorts; Android SDK and Espressif EIM special-purpose installers; catalog-pinned archives and registry installers where declared |
| Linux | amd64, arm64 | APT, DNF, Pacman, Zypper, APK, existing Homebrew; Android SDK and Espressif EIM special-purpose installers; catalog-pinned archives and registry installers where declared |
| FreeBSD | amd64 | FreeBSD `pkg`; catalog-pinned archives where declared |

CI runs native Go tests and builds on the six Windows/macOS/Linux architecture pairs. The release generator also cross-builds and validates FreeBSD amd64. The package-manager workflow tests Ninja through five Linux managers in disposable containers and through `pkg` in a FreeBSD 15.1 VM, SQLite through Chocolatey on Windows amd64, and OpenOCD through Homebrew on macOS amd64 and arm64. Release publication waits for both workflows. WinGet, Scoop, MacPorts, Android SDK, Espressif EIM, and other manager/tool combinations are not live integration-tested.

Direct artifact support is declared separately from package-manager support. Catalog manifests may provide ZIP, tar.gz, tar.xz, or 7z archives for exact OS/architecture pairs. The archive is hash-checked, size-bounded, safely extracted to a user-level path, and verified with its declared version command. Registry installs are available only for manifests that declare an exact-version npm, PyPI, Go module, or Cargo installer. Neither capability implies universal support across the catalog.

## Operating-system boundaries

The BSD value groups BSD-family systems, but the only implemented BSD package-manager adapter is FreeBSD `pkg`; FreeBSD is the only BSD release and integration target. OpenBSD, NetBSD, and DragonFly can be identified, but have no catalog install adapter. Their selections block instead of running FreeBSD package commands.

Package managers must already exist. DX does not install them or change their repositories. System-scope package commands may require approval for `sudo` on Unix-like systems; package-manager prompts remain visible. Direct artifact installers use user-level paths and disclose any planned persistent PATH change before approval.

## Inventory and verification boundaries

Inventory checks executable names on `PATH` and a bounded set of standard locations, including common JDK, Python, Node version-manager, Scoop-version, and Homebrew Cellar paths. DX does not scan the entire disk, so nonstandard locations can be missed. It queries local package records through Scoop and Chocolatey metadata, `dpkg-query`, `rpm`, Pacman, APK, Homebrew, MacPorts, and FreeBSD `pkg`. WinGet inventory uses a temporary export bounded to 8 MiB and 50,000 packages; its query may consult configured sources, but DX accepts no source agreements. If a manager query fails, affected tools remain unknown. A package record without a discoverable executable is also preserved as unknown.

Health checks currently run each manifest's declared version command and verify detected architecture where supported. They do not run a tool-specific build or runtime probe. CI integration coverage is intentionally narrower than the declared manager matrix; see [release process](release-process.md) for the current jobs.
