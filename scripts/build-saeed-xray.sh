#!/usr/bin/env bash
set -euo pipefail

[[ $# == 1 ]] || { echo "Usage: $0 <output-binary>" >&2; exit 2; }
output="$1"
case "$output" in /*) ;; *) output="$PWD/$output" ;; esac
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
stage="$(mktemp -d)"
trap 'chmod -R u+w "$stage"; rm -rf -- "$stage"' EXIT
go mod download github.com/xtls/xray-core
module_dir="$(go list -m -f '{{.Dir}}' github.com/xtls/xray-core)"
cp -R "$module_dir" "$stage/core"
chmod u+w "$stage/core/app/dispatcher" "$stage/core/app/dispatcher/default.go" "$stage/core/app/log/log.go"
go run ./tools/xray-limit-patch "$stage/core"
cp go.mod "$stage/xray.mod"
cp go.sum "$stage/xray.sum"
go mod edit -modfile="$stage/xray.mod" -replace="github.com/xtls/xray-core=$stage/core"
CGO_ENABLED=0 go build -modfile="$stage/xray.mod" -trimpath -ldflags '-s -w' -o "$output" github.com/xtls/xray-core/main
