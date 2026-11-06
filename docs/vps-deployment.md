# VPS deployment

This distribution server only serves release files. The supplied Caddyfile disables access logs, sets no-store headers, serves one static root, and defines no dynamic handlers or reverse proxy. Caddy obtains and renews HTTPS certificates automatically after the operator configures DNS and network access.

## One-time host setup

Use a Linux VPS with Caddy, OpenSSH, rsync-compatible SSH access, and enough disk space for retained releases. The host also needs OpenSSL Ed25519 support, `sha256sum`, `base64`, `find`, `awk`, `sed`, `readlink`, and GNU `mv -T` for remote release validation and atomic switching. The deployment account must own the static root; do not run the DX application on the VPS.

Create `/srv/www/developers-club-dx/releases` and install `deployment/server/Caddyfile` as the Caddy site config for `dx.bisz.dev`. Point its A/AAAA records directly at the VPS and allow inbound TCP 80/443 for certificate issuance and HTTPS. Keep SSH access restricted to deployment operators. Do not add a CDN or cache proxy.

The root directory is intentionally expected to be pre-created. Each successful publication creates `current -> releases/vX.Y.Z`, while `install.sh` and `install.ps1` are stable symlinks through `current`. Versioned release directories are never overwritten or removed by these scripts.

## Publish

Build locally or in the protected release workflow, then pass the generated release directory, version, HTTPS origin, and base64 public key to:

```sh
deployment/scripts/publish-release.sh \
  deploy@example-host /path/to/dist/releases/v1.2.3 v1.2.3 \
  https://your-real-hostname BASE64_ED25519_PUBLIC_KEY
```

The host must already contain a `releases` directory. The script creates a unique hidden staging directory, uploads regular files over authenticated SSH, checks the uploaded version/origin and pinned public key, verifies the Ed25519 signature and every listed digest, then atomically moves the versioned directory and current symlink. Failed uploads remove only their unique staging directory. An existing version is rejected as immutable.

After switching `current`, it checks stable installer and versioned artifact endpoints over HTTPS:

```sh
deployment/scripts/check-published-release.sh https://your-real-hostname v1.2.3
```

The smoke check validates TLS through curl, endpoint availability, no-store headers, expected target artifacts, and that `/api/health` is not a dynamic endpoint. It does not install a tool or claim a package-manager integration test. Run it after `dx.bisz.dev` points to the configured VPS and HTTPS is available.

## Roll back

Choose an earlier validated release and switch `current` atomically:

```sh
deployment/scripts/rollback-release.sh \
  deploy@example-host v1.2.2 BASE64_ED25519_PUBLIC_KEY
```

Rollback verifies that release's signature and hashes before changing the symlink. It retains both the old and current version directories. It does not delete or rewrite release files.
