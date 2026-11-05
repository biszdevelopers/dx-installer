# Release process

## Signing key

Create an Ed25519 release key in a dedicated secret-management environment, not in the repository. For example, OpenSSL can create a PKCS#8 private key:

```sh
umask 077
openssl genpkey -algorithm Ed25519 -out dx-release-ed25519.pem
```

The release command accepts PKCS#8 PEM, base64 Ed25519 key bytes, a raw 32-byte seed, or a raw 64-byte private key. On Unix, it refuses a key file accessible to group or other users. Back up and rotate signing keys through an operator-controlled process; changing keys requires a client release that pins the new public key.

## Local build

Run the platform CI workflow successfully first. Create an immutable tag such as `v1.2.3`, then build with a real HTTPS origin and a private key supplied outside the repository:

```powershell
go test ./...
go vet ./...
go run ./cmd/catalog-check
go run ./cmd/release `
  --version v1.2.3 `
  --origin https://your-real-hostname `
  --output-dir .\dist `
  --signing-key C:\secure\dx-release-ed25519.pem
```

The default release artifact matrix builds Windows, macOS, and Linux on amd64 and arm64 plus FreeBSD amd64. The native CI and release verification jobs run on the six Windows/macOS/Linux architecture pairs; FreeBSD amd64 is cross-built and separately exercised in the FreeBSD package-manager integration VM. Explicit target selection is available with `--targets windows/amd64,macos/arm64,linux/amd64`; only publish targets with matching successful verification. The generator validates executable file formats and architectures, signs the release checksum list, and refuses to overwrite an existing version.

The output is `dist/releases/vX.Y.Z`. `SHA256SUMS.sig` is a base64 Ed25519 signature over the exact `SHA256SUMS` bytes; `SHA256SUMS.pub` contains the corresponding base64 public key. The client binary has that key pinned at link time. Run the generated binary's `verify-release --directory` command before deployment.

## Protected GitHub release workflow

The manual workflow accepts a version and requires dispatch from the matching Git tag. Configure the `production` environment with required reviewers and only permit protected release tags. Configure these environment values:

- Variable `DX_PUBLIC_ORIGIN`: the real HTTPS origin, without a path.
- Variable `DX_DEPLOY_ROOT`: optional remote static root (defaults to `/srv/www/developers-club-dx`).
- Secret `DX_DEPLOY_HOST`: an SSH `user@host` name.
- Secret `DX_SIGNING_PRIVATE_KEY`: maintainer Ed25519 key file contents.
- Secret `DX_SSH_PRIVATE_KEY`: deployment SSH private key.
- Secret `DX_SSH_KNOWN_HOSTS`: preverified known-host entries; do not populate this with an unauthenticated `ssh-keyscan` at deployment time.

The workflow tests all six native Windows/macOS/Linux architecture jobs and gates publication on the disposable package-manager integration workflow. That workflow installs and verifies Ninja through five Linux managers and FreeBSD `pkg` in a FreeBSD 15.1 VM, SQLite through Chocolatey on Windows amd64, and OpenOCD through Homebrew on macOS amd64 and arm64. Native jobs build and run the bootstrap catalog command as a smoke check. It generates scripts and artifacts on a fresh runner, deploys only through SSH, and checks the public HTTPS endpoints after publication. It never prints the private key. A real domain, server account, key material, production environment approval, and protected tag are external prerequisites; without them, no production release can be created.

## Rollback

Use `deployment/scripts/rollback-release.sh` to validate a retained prior release and atomically point `current` to it. Do not overwrite a versioned release to repair it; create a new version and retain the defective directory for investigation.
