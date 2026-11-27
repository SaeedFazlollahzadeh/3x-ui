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
On source builds, missing system packages are installed with the Linux package
manager (using sudo when needed). Missing or older Go and Node.js/npm are
downloaded into the temporary build directory for this run. A prebuilt bundle
skips toolchain setup. Source builds require internet access for uncached files.
Copy geoip.dat, additional geo data, and the optional MTProto sidecar to
the bundle's bin/ directory. An existing
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

install_build_packages() {
    local -a elevate=()
    if (( EUID != 0 )); then
        command -v sudo >/dev/null || fail "Missing system packages. Run with root access or install a C compiler, libc headers, git, curl, CA certificates, tar, gzip, xz, and coreutils."
        elevate=(sudo)
    fi

    if command -v apt-get >/dev/null; then
        "${elevate[@]}" apt-get update
        "${elevate[@]}" apt-get install -y build-essential git curl ca-certificates tar gzip xz-utils coreutils
    elif command -v dnf >/dev/null; then
        "${elevate[@]}" dnf install -y gcc glibc-devel git curl ca-certificates tar gzip xz coreutils
    elif command -v yum >/dev/null; then
        "${elevate[@]}" yum install -y gcc glibc-devel git curl ca-certificates tar gzip xz coreutils
    elif command -v pacman >/dev/null; then
        "${elevate[@]}" pacman -S --needed --noconfirm gcc glibc git curl ca-certificates tar gzip xz coreutils
    elif command -v zypper >/dev/null; then
        "${elevate[@]}" zypper --non-interactive install gcc glibc-devel git curl ca-certificates tar gzip xz coreutils
    else
        fail "Install a C compiler, libc headers, git, curl, CA certificates, tar, gzip, xz, and coreutils using your distribution's package manager."
    fi
}

version_at_least() {
    [[ "$1" =~ ^[0-9]+([.][0-9]+){0,2}$ ]] &&
        [[ "$(printf '%s\n' "$2" "$1" | sort -V | head -n1)" == "$2" ]]
}

download_file() {
    local url="$1" output="$2"
    echo "Downloading $url" >&2
    curl -fsSL --retry 3 "$url" -o "$output" || fail "Download failed: $url"
}

download_archive() {
    local url="$1" archive="$2" checksum="$3"
    [[ "$checksum" =~ ^[[:xdigit:]]{64}$ ]] || fail "No published SHA-256 checksum for $archive."
    download_file "$url" "$stage/$archive"
    (cd "$stage" && printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c -)
}

prepare_build_tools() {
    local dependency go_min go_installed go_arch node_major node_installed npm_installed
    local node_arch archive checksum node_url node_release
    for dependency in git curl tar gzip xz sha256sum; do
        if ! command -v "$dependency" >/dev/null; then
            install_build_packages
            break
        fi
    done
    if ! printf '#include <stdlib.h>\nint main(void) { return 0; }\n' |
        "${CC:-cc}" -x c - -o "$stage/compiler-check" 2>/dev/null; then
        install_build_packages
        printf '#include <stdlib.h>\nint main(void) { return 0; }\n' |
            "${CC:-cc}" -x c - -o "$stage/compiler-check" || fail "A working C compiler and libc headers are required."
    fi

    go_min="$(awk '$1 == "go" {print $2; exit}' "$repo_dir/go.mod")"
    go_installed="$(GOTOOLCHAIN=local go version 2>/dev/null | awk '{sub(/^go/, "", $3); print $3}' || true)"
    if ! version_at_least "$go_installed" "$go_min"; then
        go_arch="$platform"
        case "$platform" in armv6|armv7) go_arch=armv6l ;; esac
        archive="go$go_min.linux-$go_arch.tar.gz"
        download_file 'https://go.dev/dl/?mode=json&include=all' "$stage/go-releases.json"
        checksum="$(awk -F '"' -v file="$archive" '
            $2 == "filename" {match_file = ($4 == file)}
            match_file && $2 == "sha256" {print $4; exit}
        ' "$stage/go-releases.json")"
        download_archive "https://go.dev/dl/$archive" "$archive" "$checksum"
        tar -xzf "$stage/$archive" -C "$stage"
        export PATH="$stage/go/bin:$PATH"
    fi

    node_major="$(tr -d '[:space:]' < "$repo_dir/.nvmrc")"
    node_installed="$(node --version 2>/dev/null || true)"
    npm_installed="$(npm --version 2>/dev/null || true)"
    if ! version_at_least "${node_installed#v}" "$node_major.0.0" ||
       ! version_at_least "$npm_installed" 11.0.0; then
        case "$platform" in
            amd64) node_arch=x64 ;;
            arm64|ppc64le|s390x|riscv64) node_arch="$platform" ;;
            *) fail "Install Node.js $node_major or newer and npm 11 or newer for $platform before building." ;;
        esac
        node_url="https://nodejs.org/dist/latest-v$node_major.x"
        download_file "$node_url/SHASUMS256.txt" "$stage/node-checksums"
        read -r checksum archive < <(awk -v suffix="-linux-$node_arch.tar.xz" '
            substr($2, length($2) - length(suffix) + 1) == suffix {print $1, $2; exit}
        ' "$stage/node-checksums") || fail "No official Node.js $node_major archive for $platform. Install Node.js and npm on this host before building."
        [[ "$archive" =~ ^node-v[0-9]+[.][0-9]+[.][0-9]+-linux-[a-z0-9]+[.]tar[.]xz$ ]] ||
            fail "Unexpected Node.js archive name."
        node_release="${archive#node-}"
        node_release="${node_release%%-linux-*}"
        download_archive "https://nodejs.org/dist/$node_release/$archive" "$archive" "$checksum"
        tar -xJf "$stage/$archive" -C "$stage"
        export PATH="$stage/${archive%.tar.xz}/bin:$PATH"
    fi
    go version
    node --version
    npm --version
}

app="$bundle/usr/local/x-ui"
cli="$bundle/usr/bin/x-ui"
core_arch="$platform"
case "$platform" in armv6|armv7) core_arch=arm32 ;; esac
core="$app/bin/xray-linux-$core_arch"
if [[ "$mode" == build || ! -s "$app/x-ui" || ! -s "$core" || ! -s "$app/$unit" ]]; then
    stage="$(mktemp -d)"
    trap 'rm -rf -- "$stage"' EXIT
    prepare_build_tools
    (
        cd "$repo_dir/frontend"
        npm ci
        npm run build
    )
    (
        cd "$repo_dir"
        CGO_ENABLED=1 go build -trimpath -ldflags '-s -w' -o "$stage/x-ui" .
        ./scripts/build-saeed-xray.sh "$stage/xray"
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
