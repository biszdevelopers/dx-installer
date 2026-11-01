#!/bin/sh
set -eu

usage() {
    printf 'Usage: %s HOST RELEASE_DIR VERSION ORIGIN PUBLIC_KEY_BASE64 [REMOTE_ROOT]\n' "$0" >&2
    exit 2
}

[ "$#" -ge 5 ] && [ "$#" -le 6 ] || usage
host=$1
release_directory=$2
version=$3
origin=$4
public_key=$5
remote_root=${6:-/srv/www/developers-club-dx}

case "$host" in *[!A-Za-z0-9._@:-]*|'') printf 'Invalid SSH host: %s\n' "$host" >&2; exit 2 ;; esac
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || {
    printf 'Invalid release version: %s\n' "$version" >&2
    exit 2
}
case "$origin" in https://*) origin_host=${origin#https://} ;; *) printf 'Origin must be HTTPS.\n' >&2; exit 2 ;; esac
case "$origin_host" in ''|*[!A-Za-z0-9.-]*|*.*.*.*.*) printf 'Origin must be a plain HTTPS hostname.\n' >&2; exit 2 ;; esac
case "$remote_root" in /* ) ;; *) printf 'Remote root must be an absolute path.\n' >&2; exit 2 ;; esac
case "/$remote_root/" in */../* ) printf 'Remote root may not contain parent traversal.\n' >&2; exit 2 ;; esac
case "$remote_root" in *[!A-Za-z0-9/._-]* ) printf 'Remote root contains unsupported characters.\n' >&2; exit 2 ;; esac
case "$public_key" in *[!A-Za-z0-9+/=]*|'') printf 'Public key must be base64.\n' >&2; exit 2 ;; esac

for command in ssh rsync openssl base64; do
    command -v "$command" >/dev/null 2>&1 || { printf 'Required command not found: %s\n' "$command" >&2; exit 1; }
done
[ -d "$release_directory" ] || { printf 'Release directory does not exist: %s\n' "$release_directory" >&2; exit 1; }
[ -f "$release_directory/install.sh" ] && [ -f "$release_directory/install.ps1" ] || {
    printf 'Release must contain both generated bootstrap entry points.\n' >&2
    exit 1
}
for name in SHA256SUMS SHA256SUMS.sig SHA256SUMS.pub RELEASE; do
    [ -f "$release_directory/$name" ] || { printf 'Release is missing %s.\n' "$name" >&2; exit 1; }
done
if find "$release_directory" -mindepth 1 -maxdepth 1 ! -type f -print -quit | grep -q .; then
    printf 'Release directory may contain only regular files.\n' >&2
    exit 1
fi

encoded_root=$(printf '%s' "$remote_root" | base64 | tr -d '\n')
staging_name=".staging-$version-$(openssl rand -hex 12)"
published=0
cleanup_local() {
    result=$?
    trap - EXIT HUP INT TERM
    if [ "$published" -eq 0 ]; then
        ssh "$host" sh -s -- "$encoded_root" "$staging_name" <<'REMOTE_CLEANUP' >/dev/null 2>&1 || true
set -eu
remote_root=$(printf '%s' "$1" | base64 -d)
stage_name=$2
case "$stage_name" in .staging-v[0-9]*.[0-9]*.[0-9]*-???????????????????????? ) ;; *) exit 2 ;; esac
rm -rf -- "$remote_root/releases/$stage_name"
REMOTE_CLEANUP
    fi
    exit "$result"
}
trap cleanup_local EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

ssh "$host" sh -s -- "$version" "$encoded_root" "$public_key" "$staging_name" <<'REMOTE_PREPARE'
set -eu
version=$1
remote_root=$(printf '%s' "$2" | base64 -d)
stage_name=$4
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || exit 2
case "$remote_root" in /* ) ;; *) exit 2 ;; esac
case "/$remote_root/" in */../* ) exit 2 ;; esac
case "$stage_name" in .staging-v[0-9]*.[0-9]*.[0-9]*-???????????????????????? ) ;; *) exit 2 ;; esac
release_root="$remote_root/releases"
target="$release_root/$version"
stage="$release_root/$stage_name"
[ -d "$release_root" ] || { printf 'Remote releases directory does not exist.\n' >&2; exit 1; }
[ ! -e "$target" ] && [ ! -L "$target" ] || { printf 'Versioned release is immutable and already exists: %s\n' "$version" >&2; exit 1; }
[ ! -e "$stage" ] && [ ! -L "$stage" ] || { printf 'Remote staging path already exists.\n' >&2; exit 1; }
umask 022
mkdir -m 0755 -- "$stage"
REMOTE_PREPARE

rsync -a --no-owner --no-group -- "$release_directory/" "$host:$remote_root/releases/$staging_name/"

ssh "$host" sh -s -- "$version" "$encoded_root" "$public_key" "$staging_name" "$origin" <<'REMOTE_PUBLISH'
set -eu
version=$1
remote_root=$(printf '%s' "$2" | base64 -d)
expected_key=$3
stage_name=$4
origin=$5
release_root="$remote_root/releases"
stage="$release_root/$stage_name"
target="$release_root/$version"
case "$stage_name" in .staging-v[0-9]*.[0-9]*.[0-9]*-???????????????????????? ) ;; *) exit 2 ;; esac
expected_files=
actual_files=
raw_key="$stage/.release-public-key.der"
raw_signature="$stage/.release-signature.bin"
public_pem="$stage/.release-public-key.pem"
spki_der="$stage/.release-spki.der"
current_next="$remote_root/.current-$stage_name"
install_sh_next="$remote_root/.install.sh-$stage_name"
install_ps1_next="$remote_root/.install.ps1-$stage_name"
target_created=0
switched=0
cleanup_remote() {
    result=$?
    rm -f "$raw_key" "$raw_signature" "$public_pem" "$spki_der"
    rm -f "$current_next" "$install_sh_next" "$install_ps1_next"
    [ -z "$expected_files" ] || rm -f -- "$expected_files"
    [ -z "$actual_files" ] || rm -f -- "$actual_files"
    if [ "$result" -ne 0 ] && [ -d "$stage" ]; then
        rm -rf -- "$stage"
    fi
    if [ "$result" -ne 0 ] && [ "$target_created" -eq 1 ] && [ "$switched" -eq 0 ]; then
        rm -rf -- "$target"
    fi
    exit "$result"
}
trap cleanup_remote EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

command -v openssl >/dev/null 2>&1 || { printf 'Remote openssl is required to verify the signature.\n' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf 'Remote sha256sum is required to verify artifact hashes.\n' >&2; exit 1; }
command -v mktemp >/dev/null 2>&1 || { printf 'Remote mktemp is required to verify the staged release.\n' >&2; exit 1; }
command -v readlink >/dev/null 2>&1 || { printf 'Remote readlink is required to validate stable entry points.\n' >&2; exit 1; }
[ -d "$stage" ] || { printf 'Remote staging directory disappeared.\n' >&2; exit 1; }
if find "$stage" -mindepth 1 -maxdepth 1 ! -type f -print -quit | grep -q .; then
    printf 'Staged release contains a non-regular file.\n' >&2
    exit 1
fi
[ "$(tr -d '\r\n' < "$stage/SHA256SUMS.pub")" = "$expected_key" ] || {
    printf 'Staged release key differs from the configured release key.\n' >&2
    exit 1
}
[ "$(sed -n 's/^version=//p' "$stage/RELEASE")" = "$version" ] || {
    printf 'Staged release version metadata does not match the requested version.\n' >&2
    exit 1
}
[ "$(sed -n 's/^origin=//p' "$stage/RELEASE")" = "$origin" ] || {
    printf 'Staged release origin metadata does not match the configured origin.\n' >&2
    exit 1
}
command -v diff >/dev/null 2>&1 || { printf 'Remote diff is required for strict release validation.\n' >&2; exit 1; }
expected_files=$(mktemp "${TMPDIR:-/tmp}/dx-release-expected.XXXXXX")
actual_files=$(mktemp "${TMPDIR:-/tmp}/dx-release-actual.XXXXXX")
awk 'length($0) < 67 || length(substr($0, 67)) > 201 || substr($0, 65, 2) != "  " || substr($0, 1, 64) ~ /[^0-9a-f]/ || substr($0, 67) !~ /^[A-Za-z0-9][A-Za-z0-9._+-]*$/ || substr($0, 67) == "SHA256SUMS" || substr($0, 67) == "SHA256SUMS.sig" || substr($0, 67) == "SHA256SUMS.pub" || seen[substr($0, 67)]++ { exit 1 } { print substr($0, 67) } END { if (NR == 0) exit 1 }' "$stage/SHA256SUMS" > "$expected_files" || {
    printf 'Checksum list contains an invalid entry.\n' >&2
    exit 1
}
printf '%s\n' SHA256SUMS SHA256SUMS.sig SHA256SUMS.pub >> "$expected_files"
sort -o "$expected_files" "$expected_files"
(cd "$stage" && find . -mindepth 1 -maxdepth 1 -type f -printf '%f\n' | sort) > "$actual_files"
diff -u "$expected_files" "$actual_files" || {
    printf 'Staged release has missing or unsigned files.\n' >&2
    exit 1
}
printf '%s' "$expected_key" | base64 -d > "$raw_key"
[ "$(wc -c < "$raw_key" | tr -d ' ')" -eq 32 ] || { printf 'Pinned public key has invalid length.\n' >&2; exit 1; }
{ printf '\060\052\060\005\006\003\053\145\160\003\041\000'; cat "$raw_key"; } > "$spki_der"
openssl pkey -pubin -inform DER -in "$spki_der" -out "$public_pem" >/dev/null 2>&1
base64 -d < "$stage/SHA256SUMS.sig" > "$raw_signature"
openssl pkeyutl -verify -pubin -inkey "$public_pem" -rawin -in "$stage/SHA256SUMS" -sigfile "$raw_signature" >/dev/null || {
    printf 'Staged release checksum signature is invalid.\n' >&2
    exit 1
}
(cd "$stage" && sha256sum --check --strict SHA256SUMS)
rm -f "$raw_key" "$raw_signature" "$public_pem" "$spki_der"

[ ! -e "$target" ] && [ ! -L "$target" ] || { printf 'Versioned release is immutable and already exists.\n' >&2; exit 1; }
mv -- "$stage" "$target"
target_created=1
current_path="$remote_root/current"
if [ -e "$current_path" ] && [ ! -L "$current_path" ]; then
    printf 'Current release path exists and is not a symlink.\n' >&2
    exit 1
fi
if [ -L "$current_path" ]; then
    for entry in install.sh install.ps1; do
        stable_path="$remote_root/$entry"
        if [ -e "$stable_path" ] && [ ! -L "$stable_path" ]; then
            printf 'Stable entry point exists and is not a symlink: %s\n' "$entry" >&2
            exit 1
        fi
        if [ "$(readlink "$stable_path" 2>/dev/null || true)" != "current/$entry" ]; then
            if [ "$entry" = install.sh ]; then entry_next=$install_sh_next; else entry_next=$install_ps1_next; fi
            ln -s "current/$entry" "$entry_next"
            mv -Tf -- "$entry_next" "$stable_path"
        fi
    done
fi
ln -s "releases/$version" "$current_next"
mv -Tf -- "$current_next" "$current_path"
switched=1
for entry in install.sh install.ps1; do
    stable_path="$remote_root/$entry"
    if [ ! -L "$stable_path" ]; then
        if [ "$entry" = install.sh ]; then entry_next=$install_sh_next; else entry_next=$install_ps1_next; fi
        ln -s "current/$entry" "$entry_next"
        mv -Tf -- "$entry_next" "$stable_path"
    fi
done
printf 'Published %s and atomically switched current.\n' "$version"
REMOTE_PUBLISH
published=1

printf 'Deployed %s to %s:%s\n' "$version" "$host" "$remote_root"
