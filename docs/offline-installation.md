# Offline installation

DX supports catalog-pinned local archives and signed platform-specific offline bundles. Package-manager installs can also use repositories and caches that the operating system already has configured; DX does not create or manage those shared caches.

## Use a local archive

For a tool with a direct archive in the embedded catalog, give DX the absolute path to the archive. DX checks it against the catalog's version and SHA-256 before extraction:

```powershell
go run ./cmd/bootstrap plan --tools go --offline-archive go=C:\artifacts\go1.27.3.linux-amd64.tar.gz
go run ./cmd/bootstrap install --tools go --offline-archive go=C:\artifacts\go1.27.3.linux-amd64.tar.gz --yes
```

Use a local file whose bytes match the catalog pin for the current operating system and architecture. The source path remains under the user's control; DX does not copy or retain the original archive. The archive is extracted into a temporary staging directory, verified, and installed to the planned user-level tool location.

## Create and use a signed bundle

The maintainer utility packages catalog-pinned archives and signs the bundle metadata with the Ed25519 key trusted by the client. A bundle targets one operating system and architecture. Include one archive for each tool to make available offline:

```powershell
go run ./cmd/offline-bundle `
  --platform windows `
  --arch amd64 `
  --output C:\artifacts\dx-offline-windows-amd64 `
  --signing-key C:\secure\dx-release-ed25519.key `
  --artifact go=C:\artifacts\go1.27.3.windows-amd64.zip
```

The signing key is a maintainer secret and must match the public key pinned in the client. Do not distribute the private key with a bundle. Bundle files are `bundle.json`, `bundle.sig`, and the listed payloads; DX rejects unlisted or missing members, symlinks, path traversal, mismatched catalog versions or hashes, and bundles for another platform. Individual payloads are bounded to the catalog's declared download limit, with a 32 GiB aggregate limit.

Load the bundle from the Sources screen with `b`, or pass it to a plan/install command:

```powershell
go run ./cmd/bootstrap plan --tools go --offline-bundle C:\artifacts\dx-offline-windows-amd64
go run ./cmd/bootstrap install --tools go --offline-bundle C:\artifacts\dx-offline-windows-amd64 --yes
```

The bundle directory stays where the operator placed it. DX verifies it before planning, then uses the selected payload as a local archive. An explicitly supplied custom URL and a bundle payload cannot both supply the same tool.

## Limits

Only tools with a direct archive and a catalog digest can be supplied as local archives or bundle payloads. A package-manager-only tool still needs its configured package manager and a reachable or pre-populated repository. The offline bundle command creates a bundle from files already on disk; it does not download or resolve tool archives for the operator.
