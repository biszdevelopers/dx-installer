# Embedded tool catalog

The catalog currently contains 41 tools and 10 profiles. Inspect exact manifests with `go run ./cmd/bootstrap catalog --json`; each manifest also lives under `internal/catalog/data/tools/`.

| Category | Tools |
| --- | --- |
| Version control | Git, Git LFS, GitHub CLI |
| JavaScript | Bun, Node.js LTS |
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
| Database | SQLite CLI, PostgreSQL Client |
| Embedded | OpenOCD |
| Security | Trivy |

Profiles are Complete, Database, Essential, Full Stack, Go, Java, Python, Rust, Systems Programming, and Web Development. Profiles are editable selections; applying one does not run a command.

Manifests specify catalog IDs, version channel and no-automatic-upgrade policy, executable names and version arguments, dependencies/conflicts, declared platforms, and package-manager installers. They do not contain shell fragments or URLs. The runtime loader uses typed Go structs, rejects unknown YAML fields, validates manager/platform/scope combinations, and rejects missing dependencies, duplicate IDs, and graph cycles.

## Coverage limits

The current catalog does not yet include the larger Phase 7 and Phase 9 language, mobile, embedded, database-server, DevOps, and security tool sets in the product specification. A package-manager installer is only a declared adapter mapping; actual availability still depends on the OS, architecture, configured repository, and detected manager. Catalog presence is not a claim that every listed manager/platform pair has been exercised in a disposable VM.

Before adding a manifest, verify every package identifier against the named package manager's maintained metadata and check the package's publisher and scope. Add a focused manifest validation test, update relevant profiles, run the catalog checker, and document any real platform test.
