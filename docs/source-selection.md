# Package managers and download sources

The Sources screen and CLI flags configure sources for a reviewed plan. DX never installs a package manager, adds a repository, or changes a package manager's persistent configuration.

## Existing package managers

The Sources screen lists detected package managers. Select one to prefer it when a selected tool declares a compatible installer; press `0` to clear the preference. The plan shows the package identifier, configured source label, scope, and exact command arguments. `--manager <name>` applies the same preference to `plan` and `install` and fails if that manager is not detected.

A preferred manager without a matching manifest entry is not used. Planning may use another detected manager declared for that tool. Package-manager trust and configured repositories remain the operating system's responsibility.

WinGet commands use the manifest's exact package ID and declared source name, and include `--no-upgrade`. DX does not verify that the configured WinGet source name points to an official publisher. On Windows, inventory uses `winget export` to query package IDs and versions. WinGet may consult configured sources during that query; DX disables prompts, requests English diagnostics, accepts no source agreements, limits the temporary export to 8 MiB and 50,000 package records, and treats warnings or errors as incomplete inventory. Affected tools remain unknown rather than being marked missing. See Microsoft's [WinGet export documentation](https://learn.microsoft.com/en-us/windows/package-manager/winget/export) and [global output options](https://github.com/microsoft/winget-cli/blob/master/doc/windows/package-manager/winget/index.md).

## Direct artifact sources

Only tools with a platform-specific artifact in the embedded catalog can use direct archive sources. Each download is checked against its catalog-pinned SHA-256 digest, then extracted to a user-level installation location. Archive extraction enforces size limits and rejects unsafe paths and links.

| Option | Behavior |
| --- | --- |
| `--source-policy automatic` | Use region signals to decide whether a digest-pinned regional mirror may follow retryable official-source failures. This is the default. |
| `--source-policy official-only` | Use only the official catalog URL. |
| `--source-policy official-first` | Try official first and use an eligible verified mirror after retryable failures. |
| `--source-policy mainland-china` | Explicitly select the same fallback order as `official-first`. |
| `--source-policy global` | Do not use regional mirrors. |
| `--custom-source tool-id=https://...` | Use an explicit HTTPS URL for a catalog-pinned artifact; the catalog SHA-256 remains mandatory. Repeat for different tools. |
| `--offline-archive tool-id=absolute-path` | Use a local regular archive file and verify it against the catalog SHA-256. Repeat for different tools. |
| `--offline-bundle absolute-directory` | Load a signed platform-specific bundle whose metadata and files match the pinned client key and embedded catalog. |

For example, `go run ./cmd/bootstrap plan --tools go --source-policy official-only` prints a read-only plan. Add the same source flags to an explicitly approved `install` command to execute it. Regional signals and current artifact source are shown on the Sources screen. See [regional behavior](regional-support.md) and [offline installation](offline-installation.md).

## Registry overrides

Tools that declare a registry installer use an exact catalog package version. `--registry ecosystem=https://...` overrides the registry for a supported ecosystem; repeat the flag for multiple ecosystems. Supported names are `npm`, `pypi`, `go-modules`, and `cargo`. URLs must use HTTPS and cannot contain credentials, a query, or a fragment. Registry downloads use a temporary cache, which DX removes after the operation. The installer uses ecosystem-specific integrity features where configured, including the Go checksum database and locked Cargo resolution.

Registry overrides are per invocation and are included in the plan. They do not edit npm, uv, Go, or Cargo configuration files. The selected tool must declare a matching registry installer and have its required runtime or package tool available.

## Retry and integrity behavior

Direct downloads use bounded retries for transient network and server failures. A regional fallback is attempted only when the selected policy permits it and the catalog declares a mirror for that artifact. Custom and regional URLs must use HTTPS, reject unsafe redirects, and provide bytes matching the embedded SHA-256 pin. Integrity failures stop the install without falling back to another source.
