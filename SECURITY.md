# Security policy

DX changes a user's environment only after presenting the complete package-manager plan and receiving explicit approval. Please report suspected security issues through the private vulnerability reporting mechanism of the repository host once this repository is published. Do not include credentials, tokens, or private user paths in reports.

## Release trust

The bootstrap entry points are generated from maintainer-supplied release metadata. They verify a pinned SHA-256 digest before executing a downloaded bootstrap binary. Release checksum files are intended to be signed with an Ed25519 key held outside the repository and CI logs. The corresponding public key is pinned into release artifacts by the release process.

The one-command HTTPS bootstrap trusts the configured distribution origin and its TLS endpoint. Users who require independent review should download the entry point and release metadata, inspect them, and verify the signed checksums before execution.

## Runtime boundaries

- Tool manifests are embedded and strictly decoded; they cannot contain executable shell snippets.
- Package-manager invocations use argument arrays and fixed adapters.
- Downloads use HTTPS and integrity checks.
- The application does not collect telemetry or credentials.
- Temporary bootstrap artifacts are removed when the process exits.
