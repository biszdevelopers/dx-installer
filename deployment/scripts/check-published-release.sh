#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
    printf 'Usage: %s HTTPS_ORIGIN VERSION\n' "$0" >&2
    exit 2
fi
origin=$1
version=$2
case "$origin" in https://*) ;; *) printf 'Origin must use HTTPS.\n' >&2; exit 2 ;; esac
printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || {
    printf 'Invalid release version.\n' >&2
    exit 2
}
command -v curl >/dev/null 2>&1 || { printf 'curl is required.\n' >&2; exit 1; }
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/dx-release-check.XXXXXXXX")
cleanup() {
    result=$?
    trap - EXIT HUP INT TERM
    rm -rf "$temporary_directory" || { printf 'Could not remove smoke-test files.\n' >&2; [ "$result" -ne 0 ] || result=1; }
    exit "$result"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

check_headers() {
    url=$1
    header_file="$temporary_directory/headers"
    curl --fail --silent --show-error --head --proto '=https' --tlsv1.2 --dump-header "$header_file" --output /dev/null "$url"
    grep -Eiq '^Cache-Control:[[:space:]]*.*no-store' "$header_file" || { printf 'Missing no-store cache policy: %s\n' "$url" >&2; return 1; }
    grep -Eiq '^Pragma:[[:space:]]*no-cache' "$header_file" || { printf 'Missing Pragma no-cache header: %s\n' "$url" >&2; return 1; }
    grep -Eiq '^Expires:[[:space:]]*0' "$header_file" || { printf 'Missing Expires: 0 header: %s\n' "$url" >&2; return 1; }
}

for path in install.sh install.ps1 "releases/$version/install.sh" "releases/$version/install.ps1" "releases/$version/SHA256SUMS" "releases/$version/SHA256SUMS.sig" "releases/$version/SHA256SUMS.pub" "releases/$version/RELEASE"; do
    check_headers "$origin/$path"
done

curl --fail --silent --show-error --proto '=https' --tlsv1.2 "$origin/releases/$version/RELEASE" --output "$temporary_directory/RELEASE"
grep -Fqx "version=$version" "$temporary_directory/RELEASE" || { printf 'Published release metadata has the wrong version.\n' >&2; exit 1; }
while IFS='=' read -r key target; do
    [ "$key" = target ] || continue
    platform=${target%%/*}
    architecture=${target#*/}
    case "$platform-$architecture" in
        windows-amd64|windows-arm64) artifact="dx-bootstrap-$platform-$architecture.exe" ;;
        macos-amd64|macos-arm64|linux-amd64|linux-arm64) artifact="dx-bootstrap-$platform-$architecture" ;;
        *) printf 'Unexpected target in published metadata: %s\n' "$target" >&2; exit 1 ;;
    esac
    check_headers "$origin/releases/$version/$artifact"
done < "$temporary_directory/RELEASE"

status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --proto '=https' --tlsv1.2 "$origin/api/health" || true)
[ "$status" = 404 ] || { printf 'Unexpected non-static endpoint /api/health returned HTTP %s.\n' "$status" >&2; exit 1; }
printf 'Published HTTPS release endpoints, cache headers, and static-only route check passed for %s.\n' "$version"
