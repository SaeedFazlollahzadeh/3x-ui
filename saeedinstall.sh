#!/usr/bin/env bash
if [ "${BASH_SOURCE:-}" != "$0" ]; then
    echo "Run this installer as ./saeedinstall.sh." >&2
    return 0
fi
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
It provisions local PostgreSQL and migrates existing SQLite data on first
install with this script. The original SQLite database remains on disk.
On source builds, missing system packages are installed with the Linux package
manager (using sudo when needed). If Go is missing or too old, the script
installs docker.io on APT-based systems when Docker is missing. It pulls
Go from ArvanCloud using Docker, retries immediately on
failure, and extracts Go into the temporary build directory. Press Ctrl+C to
stop retries. Missing or older Node.js/npm is downloaded separately.
A prebuilt bundle skips toolchain setup. Set GOPROXY to override the module
proxy and its direct-source fallback.
Copy geoip.dat, additional geo data, and the optional MTProto sidecar to
the bundle's bin/ directory. An existing
installation can supply these assets. On a fresh install without geoip.dat,
the script fetches that data file for the default Xray configuration.
HELP
        exit 0 ;;
esac
(( $# <= 1 )) || { echo "Usage: $0 [--build-only] [bundle-directory]" >&2; exit 2; }
bundle="${1:-$bundle}"
fail() { echo "$*" >&2; return 1; }

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

extract_go_from_docker() (
    local version="$1" variant=bookworm image mirror_image container
    local -a elevate=()
    if ! command -v docker >/dev/null; then
        command -v apt >/dev/null || fail "Install Docker or put Go $version or newer on PATH."
        if (( EUID != 0 )); then
            command -v sudo >/dev/null || fail "Installing docker.io requires root access."
            elevate=(sudo)
        fi
        "${elevate[@]}" apt update
        "${elevate[@]}" apt install -y docker.io
        if ! docker info >/dev/null 2>&1 && command -v systemctl >/dev/null; then
            "${elevate[@]}" systemctl start docker
        fi
    fi
    docker info >/dev/null || fail "Start Docker and ensure this user can access its daemon."

    case "$platform" in
        riscv64|s390x) variant=trixie ;;
        armv6) variant=alpine ;;
    esac
    image="golang:$version-$variant"
    mirror_image="docker.arvancloud.ir/$image"
    if ! docker image inspect "$mirror_image" >/dev/null 2>&1; then
        while true; do
            docker pull "$mirror_image" && break
        done
    fi
    docker tag "$mirror_image" "$image"
    container="$(docker create "$image")"
    trap 'docker rm "$container" >/dev/null || true' EXIT
    install -d "$stage/go"
    docker cp "$container:/usr/local/go/." "$stage/go/"
)

prepare_build_tools() {
    local dependency go_min go_installed node_major node_installed npm_installed
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
        extract_go_from_docker "$go_min"
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
    if [[ -z "${GOPROXY+x}" && "$(go env GOPROXY)" == 'https://proxy.golang.org,direct' ]]; then
        export GOPROXY='https://proxy.golang.org|direct'
    fi
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
    copy_mode=-n
    if cp --update=none --version >/dev/null 2>&1; then
        copy_mode=--update=none
    fi
    (umask 077; cp -R "$copy_mode" -- "$bundle/etc/x-ui/." /etc/x-ui/)
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
configure_postgres() (
    local env_file db_type db_folder db_source dsn work
    local was_active=0
    case "$unit" in
        x-ui.service.arch) env_file=/etc/conf.d/x-ui ;;
        x-ui.service.rhel) env_file=/etc/sysconfig/x-ui ;;
        *) env_file=/etc/default/x-ui ;;
    esac

    read_db_setting() {
        [[ -r "$env_file" ]] || return 0
        sed -nE "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*//p" "$env_file" |
            tail -n 1 | sed -E "s/^[\"']//; s/[\"'][[:space:]]*$//; s/[[:space:]]*$//"
    }
    db_type="$(read_db_setting XUI_DB_TYPE)"
    case "$(printf '%s' "$db_type" | tr '[:upper:]' '[:lower:]')" in
        postgres|postgresql|pg)
            systemctl restart x-ui.service
            return
            ;;
    esac
    db_folder="$(read_db_setting XUI_DB_FOLDER)"
    db_source="${db_folder:-/etc/x-ui}/x-ui.db"
    if [[ "$db_source" != /* ]]; then
        db_source="$dest/$db_source"
    fi

    echo "Provisioning a local PostgreSQL database for x-ui."
    dsn="$(bash "$repo_dir/x-ui.sh" provision-postgres)" ||
        fail "Could not provision PostgreSQL."
    [[ "$dsn" == postgres://* && "$dsn" != *$'\n'* ]] ||
        fail "PostgreSQL setup did not return a connection string."

    install -d "$(dirname -- "$env_file")"
    work="$(mktemp -d "$(dirname -- "$env_file")/.x-ui-db.XXXXXX")"
    trap 'rm -rf -- "$work"' EXIT
    if [[ -e "$env_file" ]]; then
        cp -p -- "$env_file" "$work/previous"
        sed -E '/^[[:space:]]*XUI_DB_(TYPE|DSN)[[:space:]]*=/d' "$env_file" > "$work/env"
    fi
    (
        umask 077
        printf 'XUI_DB_TYPE=postgres\nXUI_DB_DSN=%s\n' "$dsn" >> "$work/env"
    )
    chmod 0600 "$work/env"

    if systemctl is-active --quiet x-ui.service; then
        was_active=1
        systemctl stop x-ui.service
    fi

    restore_previous_service() {
        if (( was_active )); then
            systemctl start x-ui.service || true
        fi
    }

    if [[ -f "$db_source" ]]; then
        echo "Migrating $db_source into PostgreSQL."
        if ! "$dest/x-ui" migrate-db --src "$db_source" --dsn "$dsn"; then
            restore_previous_service
            fail "Migration failed. The original SQLite database is still in place."
        fi
    elif ! XUI_DB_TYPE=postgres XUI_DB_DSN="$dsn" "$dest/x-ui" migrate; then
        restore_previous_service
        fail "Could not initialize the PostgreSQL schema."
    fi

    if ! mv -f -- "$work/env" "$env_file"; then
        restore_previous_service
        fail "Could not update $env_file."
    fi
    if ! systemctl restart x-ui.service || ! systemctl is-active --quiet x-ui.service; then
        systemctl stop x-ui.service || true
        if [[ -f "$work/previous" ]]; then
            mv -f -- "$work/previous" "$env_file"
        else
            rm -- "$env_file"
        fi
        restore_previous_service
        fail "x-ui could not start with PostgreSQL. The previous database settings were restored."
    fi
)

configure_postgres
systemctl is-active x-ui.service
