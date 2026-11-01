# Platform support

Platform detection maps Go targets to Windows, macOS, Linux, and BSD families. Linux distribution identification reads only `ID` and `VERSION_ID` from `/etc/os-release`. The manager adapter detects existing winget, Scoop, Chocolatey, Homebrew, MacPorts, APT, DNF, Pacman, Zypper, APK, and FreeBSD `pkg` executables.

The default release target matrix is:

| OS | Architectures in release generator | Package-manager adapters |
| --- | --- | --- |
| Windows | amd64, arm64 | winget, Scoop, Chocolatey |
| macOS | amd64, arm64 | Homebrew, MacPorts |
| Linux | amd64, arm64 | APT, DNF, Pacman, Zypper, APK |
| BSD | Not included by default | FreeBSD `pkg` adapter exists in source |

The CI workflow is configured for native Go tests and native builds on all six Windows/macOS/Linux architecture pairs. That configuration does not prove a future run passed, and it does not test installing packages through live package managers. Confirm the CI run is green before publishing a release. The repository has no BSD CI runner; do not advertise BSD downloads until it has a native integration environment and passing tests.

Tool support is per manifest. A tool is installable only if its declared platform/architecture matches and a compatible already-installed package manager is found. Unsupported selections block the whole plan. The engine does not install managers, change repositories, or substitute another architecture.

Inventory checks executable names discoverable on `PATH`, with the tool's declared version arguments. It also queries local package records on Linux through `dpkg-query`, `rpm`, `pacman`, or `apk`, on macOS through Homebrew or MacPorts, and on FreeBSD through `pkg`. A package record without a discoverable executable is shown as unknown and preserved; it is not claimed to be functional. Winget, Scoop, and Chocolatey package databases are not queried yet. DX does not search arbitrary locations, so non-package installs outside `PATH`, multiple non-PATH versions, and system-managed runtimes can still be missed.
