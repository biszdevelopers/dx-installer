# Platform support

Platform detection maps Go targets to Windows, macOS, Linux, and BSD families. Linux distribution identification reads only `ID` and `VERSION_ID` from `/etc/os-release`. The manager adapter detects existing winget, Scoop, Chocolatey, Homebrew, MacPorts, APT, DNF, Pacman, Zypper, APK, and FreeBSD `pkg` executables.

The default release target matrix is:

| OS | Architectures in release generator | Package-manager adapters |
| --- | --- | --- |
| Windows | amd64, arm64 | winget, Scoop, Chocolatey |
| macOS | amd64, arm64 | Homebrew, MacPorts |
| Linux | amd64, arm64 | APT, DNF, Pacman, Zypper, APK |
| FreeBSD | amd64 | FreeBSD `pkg` |

The CI workflow runs native Go tests and native builds on the six Windows/macOS/Linux architecture pairs. The package-manager workflow tests Ninja through five Linux managers in disposable containers and through `pkg` in a FreeBSD 15.1 VM, SQLite through Chocolatey on Windows amd64, and OpenOCD through Homebrew on macOS amd64 and arm64. Release publication waits for both workflows. Confirm all jobs are green before publishing. Scoop, WinGet, MacPorts, and other manager/tool pairs are not integration-tested.

Tool support is per manifest. A tool is installable only if its declared platform/architecture matches and a compatible already-installed package manager is found. Unsupported selections block the whole plan. The engine does not install managers, change repositories, or substitute another architecture.

Inventory checks executable names on `PATH` and in a bounded set of standard installation directories. It includes common JDK, Python, Node version-manager, Scoop-version, and Homebrew Cellar locations, so multiple instances found there are reported separately. DX does not scan the whole disk; nonstandard locations can still be missed. It also queries local package records on Windows through Scoop's application directories and Chocolatey's `lib` metadata, Linux through `dpkg-query`, `rpm`, `pacman`, or `apk`, macOS through Homebrew or MacPorts, and FreeBSD through `pkg`. WinGet inventory runs `winget export` with version information into a unique temporary directory, bounds the export to 8 MiB and 50,000 packages, and removes the temporary file. Microsoft documents that export can consult configured sources to match installed apps; DX disables interaction and does not accept source agreements, so an unavailable source can produce a warning instead of package records ([WinGet export command](https://learn.microsoft.com/en-us/windows/package-manager/winget/export)). A package record without a discoverable executable is shown as unknown and preserved; it is not claimed to be functional. Health checks currently confirm the declared version command, not a tool-specific build or runtime probe.
