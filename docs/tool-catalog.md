# Embedded tool catalog

The catalog currently contains 60 tools and 15 profiles. Inspect exact manifests with `go run ./cmd/bootstrap catalog --json`; each manifest also lives under `internal/catalog/data/tools/`.

| Category | Tools |
| --- | --- |
| Version control | Git, Git LFS, GitHub CLI |
| JavaScript | Bun, Node.js LTS, pnpm, Yarn Classic, TypeScript |
| Python | Python 3, uv |
| Java | Eclipse Temurin JDK 25, Maven, Gradle, Kotlin |
| PHP and Ruby | PHP CLI, Ruby |
| BEAM languages | Erlang/OTP, Elixir |
| Other languages | GHC, OCaml, R, GNU Fortran, Perl, Julia, Zig |
| Mobile and infrastructure | Dart SDK, Flutter SDK, Android Platform Tools (ADB), kubectl, Docker CLI |
| Go | Go |
| Rust | Rustup |
| C/C++ | GCC, Clang, CMake, Ninja, GDB, LLDB |
| .NET | .NET SDK 10 |
| Shell | PowerShell |
| API and protocols | Protocol Buffers, buf, grpcurl, grpcui |
| Database | SQLite CLI, PostgreSQL Client, DuckDB |
| Embedded | OpenOCD |
| DevOps and cloud | Helm, Ansible, AWS CLI, Azure CLI |
| Security and code quality | Trivy, Gitleaks, ShellCheck, shfmt, actionlint, Syft, Grype, Checkov |

Profiles are API and Protocol, Complete, Database, DevOps, Embedded, Essential, Full Stack, Go, Java, Mobile, Python, Rust, Security, Systems Programming, and Web Development. Profiles are editable selections; applying one does not run a command. Mobile selects ADB and Flutter, Embedded selects the existing C toolchains and OpenOCD, DevOps selects Docker CLI, kubectl, Helm, Ansible, AWS CLI, and Azure CLI, Security selects local secret, shell, workflow, SBOM, and vulnerability analysis tools, Database selects SQLite, PostgreSQL, and DuckDB, and API and Protocol selects Protocol Buffers and gRPC tools.

Manifests specify catalog IDs, version channel and no-automatic-upgrade policy, executable names and version arguments, dependencies/conflicts, declared platforms, and package-manager installers. They do not contain shell fragments or URLs. The runtime loader uses typed Go structs, rejects unknown YAML fields, validates manager/platform/scope combinations, and rejects missing dependencies, duplicate IDs, and graph cycles.

## Coverage limits

The catalog still does not include every Phase 7 and Phase 9 language, mobile, embedded, database-server, DevOps, and security tool listed in the product specification. The new API, cloud, and security entries currently have Homebrew mappings for macOS arm64; Gitleaks, ShellCheck, and actionlint also declare amd64. A package-manager installer is only a declared adapter mapping; actual availability still depends on the OS, architecture, configured repository, and detected manager. CI exercises Ninja through apt, dnf, pacman, zypper, and apk in disposable Linux containers and FreeBSD `pkg` in a FreeBSD VM, SQLite through Chocolatey on Windows amd64, and OpenOCD through Homebrew on macOS amd64 and arm64. It does not exercise every declared manager/platform pair.

Before adding a manifest, verify every package identifier against the named package manager's maintained metadata and check the package's publisher and scope. Add a focused manifest validation test, update relevant profiles, run the catalog checker, and document any real platform test.
