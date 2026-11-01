#!/bin/sh
set -eu

if [ "$#" -lt 3 ] || [ "$#" -gt 4 ]; then
    printf 'Usage: %s HOST VERSION PUBLIC_KEY_BASE64 [REMOTE_ROOT]\n' "$0" >&2
    exit 2
fi
host=$1
version=$2
public_key=$3
remote_root=${4:-/srv/www/developers-club-dx}
case "$host" in *[!A-Za-z0-9._@:-]*|'') printf 'Invalid SSH host.\n' >&2; exit 2 ;; esac
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || { printf 'Invalid release version.\n' >&2; exit 2; }
case "$remote_root" in /* ) ;; *) printf 'Remote root must be absolute.\n' >&2; exit 2 ;; esac
case "/$remote_root/" in */../* ) printf 'Remote root may not contain parent traversal.\n' >&2; exit 2 ;; esac
case "$remote_root" in *[!A-Za-z0-9/._-]* ) printf 'Remote root contains unsupported characters.\n' >&2; exit 2 ;; esac
case "$public_key" in *[!A-Za-z0-9+/=]*|'') printf 'Public key must be base64.\n' >&2; exit 2 ;; esac
encoded_root=$(printf '%s' "$remote_root" | base64 | tr -d '\n')

ssh "$host" sh -s -- "$version" "$encoded_root" "$public_key" <<'REMOTE_ROLLBACK'
set -eu
version=$1
remote_root=$(printf '%s' "$2" | base64 -d)
expected_key=$3
release="$remote_root/releases/$version"
command -v openssl >/dev/null 2>&1 || { printf 'Remote openssl is required.\n' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf 'Remote sha256sum is required.\n' >&2; exit 1; }
[ -d "$release" ] && [ -f "$release/install.sh" ] && [ -f "$release/install.ps1" ] || {
    printf 'Rollback target is not a complete published release.\n' >&2
    exit 1
}
[ "$(tr -d '\r\n' < "$release/SHA256SUMS.pub")" = "$expected_key" ] || {
    printf 'Rollback target is signed by a different key.\n' >&2
    exit 1
}
[ "$(sed -n 's/^version=//p' "$release/RELEASE")" = "$version" ] || {
    printf 'Rollback target release metadata does not match its directory.\n' >&2
    exit 1
}
raw_key=$(mktemp "${TMPDIR:-/tmp}/dx-rollback-key.XXXXXX")
spki_der=$(mktemp "${TMPDIR:-/tmp}/dx-rollback-spki.XXXXXX")
public_pem=$(mktemp "${TMPDIR:-/tmp}/dx-rollback-pem.XXXXXX")
raw_signature=$(mktemp "${TMPDIR:-/tmp}/dx-rollback-signature.XXXXXX")
cleanup() { rm -f "$raw_key" "$spki_der" "$public_pem" "$raw_signature"; }
trap cleanup EXIT HUP INT TERM
printf '%s' "$expected_key" | base64 -d > "$raw_key"
[ "$(wc -c < "$raw_key" | tr -d ' ')" -eq 32 ] || { printf 'Pinned public key has invalid length.\n' >&2; exit 1; }
{ printf '\060\052\060\005\006\003\053\145\160\003\041\000'; cat "$raw_key"; } > "$spki_der"
openssl pkey -pubin -inform DER -in "$spki_der" -out "$public_pem" >/dev/null 2>&1
base64 -d < "$release/SHA256SUMS.sig" > "$raw_signature"
openssl pkeyutl -verify -pubin -inkey "$public_pem" -rawin -in "$release/SHA256SUMS" -sigfile "$raw_signature" >/dev/null || {
    printf 'Rollback target signature is invalid.\n' >&2
    exit 1
}
(cd "$release" && sha256sum --check --strict SHA256SUMS)
cleanup
trap - EXIT HUP INT TERM
next="$remote_root/.current-rollback-$$"
ln -s "releases/$version" "$next"
mv -Tf -- "$next" "$remote_root/current"
printf 'Stable entry points now serve %s. Older releases were retained.\n' "$version"
REMOTE_ROLLBACK
