# Troubleshooting

## No tools or package managers are detected

DX inventories command names on the process `PATH` and supported package databases. Open a new terminal after installing a tool, then run `go run ./cmd/bootstrap inventory`. Package records without visible executables are shown as unknown and preserved. A tool installed only in a nonstandard directory may not appear. WinGet export can fail when a configured source is unavailable or requires an agreement; DX does not accept source agreements automatically. DX does not scan arbitrary disks or query every manager's package database.

## A tool is unsupported or the plan is blocked

Check the manifest's declared platforms and the detected package managers. DX will not bootstrap a manager, add a repository, substitute an undeclared package, or replace a broken/unknown installation. An unsupported or conflicting plan runs no command.

## A package-manager prompt appears

The manager runs in the terminal after explicit plan approval. DX does not automatically accept package agreements. Read the prompt and answer directly. To stop, use the manager's normal cancellation key or Ctrl+C; DX then rescans and reports the actual tool state.

## The TUI cannot render

The CLI prints the renderer error and offers a line-oriented selection and plan approval interface. If input or output is not an interactive terminal, install commands stop before execution. Use `catalog`, `inventory`, or `plan` for read-only inspection.

## Release generation fails

- Confirm the version is `vMAJOR.MINOR.PATCH` with an optional prerelease suffix.
- Use an HTTPS origin with no path or credentials.
- Confirm the signing key is Ed25519 and the Unix key file is owner-only.
- Confirm each prebuilt artifact is a regular executable of the requested target architecture.
- Choose a new version if that version directory already exists.
- For deployment, confirm remote OpenSSL supports Ed25519, `sha256sum` and GNU `mv -T` exist, the remote `releases` directory is pre-created, and SSH known-host verification succeeds.

## Temporary cleanup reports an error

The CLI reports cleanup failures to stderr. Remove only the exact path named by that message after verifying it is a DX temporary directory; do not remove shared package-manager caches.
