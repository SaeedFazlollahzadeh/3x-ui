#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
bundle="$repo_dir/saeedxui"
mode=install
case "${1:-}" in
    --build-only) mode=build; shift ;;
    --help|-h)
        cat <<'HELP'
Usage: sudo ./saeedinstall.sh [bundle-directory]
       ./saeedinstall.sh --build-only [bundle-directory]

The default bundle directory is saeedxui next to this script. Its layout is:
  etc/x-ui/                 optional initial database and settings
  usr/bin/x-ui              management command
  usr/local/x-ui/           panel binary, service units, and bin/ assets

Installation uses a complete local bundle or builds missing release files.
Source builds need Linux, Go (see go.mod), Node.js 26+, npm, and a C compiler.
Go and npm may fetch dependencies. Copy geoip.dat, any additional geo data,
and the optional MTProto sidecar to the bundle's bin/ directory. An existing
installation can supply these assets. On a fresh install without geoip.dat,
the script fetches that data file for the default Xray configuration.
HELP
        exit 0 ;;
esac
(( $# <= 1 )) || { echo "Usage: $0 [--build-only] [bundle-directory]" >&2; exit 2; }
bundle="${1:-$bundle}"
fail() { echo "$*" >&2; exit 1; }

[[ "$(uname -s)" == Linux ]] || fail "Run this script on the target Linux architecture."
if [[ "$mode" == install ]]; then
    [[ "$EUID" == 0 ]] || fail "Run installation as root."
    command -v systemctl >/dev/null || fail "Installation requires systemd."
fi

case "$(uname -m)" in
    x86_64) platform=amd64 ;;
    i386|i486|i586|i686) platform=386 ;;
    aarch64) platform=arm64 ;;
    armv7l) platform=armv7 ;;
    armv6l) platform=armv6 ;;
    riscv64) platform=riscv64 ;;
    s390x) platform=s390x ;;
    ppc64le) platform=ppc64le ;;
    *) fail "Unsupported architecture: $(uname -m)" ;;
esac

if [[ -f /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
fi
case " ${ID:-} ${ID_LIKE:-} " in
    *" arch "*) unit=x-ui.service.arch ;;
    *" rhel "*|*" fedora "*|*" centos "*|*" suse "*) unit=x-ui.service.rhel ;;
    *) unit=x-ui.service.debian ;;
esac

app="$bundle/usr/local/x-ui"
cli="$bundle/usr/bin/x-ui"
core_arch="$platform"
case "$platform" in armv6|armv7) core_arch=arm32 ;; esac
core="$app/bin/xray-linux-$core_arch"
if [[ "$mode" == build || ! -s "$app/x-ui" || ! -s "$core" || ! -s "$app/$unit" ]]; then
    for dependency in go node npm "${CC:-cc}"; do
        command -v "$dependency" >/dev/null || fail "Source build requires $dependency."
    done
    stage="$(mktemp -d)"
    trap 'rm -rf -- "$stage"' EXIT
    (
        cd "$repo_dir/frontend"
        npm ci
        npm run build
    )
    (
        cd "$repo_dir"
        CGO_ENABLED=1 go build -trimpath -ldflags '-s -w' -o "$stage/x-ui" .
    )
    install -d "$app/bin" "$bundle/etc/x-ui" "$(dirname -- "$cli")"
    install -m 0755 "$stage/x-ui" "$app/x-ui"
    install -m 0755 "$stage/xray" "$core"
    install -m 0755 "$repo_dir/x-ui.sh" "$app/x-ui.sh"
    install -m 0755 "$repo_dir/x-ui.sh" "$cli"
    install -m 0644 "$repo_dir"/x-ui.service.{arch,debian,rhel} "$app/"
fi

if [[ "$mode" == build ]]; then
    echo "Built local bundle at $bundle"
    exit 0
fi

[[ -s "$cli" ]] || cli="$app/x-ui.sh"
[[ -s "$cli" ]] || fail "Missing management command in bundle."
dest=/usr/local/x-ui
if [[ ! -s "$app/bin/geoip.dat" && ! -s "$dest/bin/geoip.dat" ]]; then
    command -v curl >/dev/null || fail "Place geoip.dat in $app/bin/ or install curl."
    geo_temp="$(mktemp)"
    if ! curl -fsSL --retry 3 -o "$geo_temp" \
        https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat ||
        [[ ! -s "$geo_temp" ]] || ! install -m 0644 "$geo_temp" "$app/bin/geoip.dat"; then
        rm -f -- "$geo_temp"
        fail "Could not fetch geoip.dat. Place a local copy in $app/bin/."
    fi
    rm -f -- "$geo_temp"
fi
if ! systemctl is-enabled x-ui.service >/dev/null 2>&1 && ! systemctl cat x-ui.service >/dev/null 2>&1; then
    [[ -d /run/systemd/system ]] || fail "Systemd must be running."
fi
# Replace executable files atomically so a running Xray or sidecar can finish.
install_executable() {
    local source="$1" target="$2" temp
    temp="$(mktemp "$(dirname -- "$target")/.x-ui.XXXXXX")"
    if ! install -m 0755 "$source" "$temp" || ! mv -f -- "$temp" "$target"; then
        rm -f -- "$temp"
        return 1
    fi
}

install -d "$dest/bin" /var/log/x-ui
install -d -m 0700 /etc/x-ui
if [[ -d "$bundle/etc/x-ui" ]]; then
    (umask 077; cp -Rn -- "$bundle/etc/x-ui/." /etc/x-ui/)
fi
# Keep an existing config and any geo/sidecar files omitted by the bundle.
if [[ -f "$app/bin/config.json" && ! -e "$dest/bin/config.json" ]]; then
    install -m 0600 "$app/bin/config.json" "$dest/bin/config.json"
fi
for asset in "$app"/bin/*; do
    [[ -f "$asset" ]] || continue
    case "${asset##*/}" in
        config.json) ;;
        xray-*|mtg-*) install_executable "$asset" "$dest/bin/${asset##*/}" ;;
        *) install -m 0644 "$asset" "$dest/bin/" ;;
    esac
done
install_executable "$app/x-ui" "$dest/x-ui"
install_executable "$cli" "$dest/x-ui.sh"
install_executable "$cli" /usr/bin/x-ui
for service_file in "$app"/x-ui.service.*; do
    [[ -f "$service_file" ]] && install -m 0644 "$service_file" "$dest/"
done
install -m 0644 "$app/$unit" /etc/systemd/system/x-ui.service
systemctl daemon-reload
systemctl enable x-ui.service
systemctl restart x-ui.service
systemctl is-active x-ui.service
