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

Inventory currently checks executable names discoverable on `PATH`, with the tool's declared version arguments. It does not query package databases or search arbitrary locations. A tool installed outside `PATH` can therefore be missed; check `go run ./cmd/bootstrap inventory` before approving installation. Package ownership, multiple non-PATH versions, system-managed runtimes, and arbitrary filesystem discovery remain incomplete.
