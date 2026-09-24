#!/usr/bin/env bash
# Build locally, then install on a Linux systemd host over ordinary SSH/Tailscale.
set -euo pipefail
cd "$(dirname "$0")/.."

if [[ $# != 1 || $1 == -* || ! $1 =~ ^[a-zA-Z0-9_.@:-]+$ ]]; then
    echo "Usage: $0 [user@]ssh-host" >&2
    exit 2
fi
host=$1
platform=$(ssh "$host" 'uname -sm')
case "$platform" in
    'Linux x86_64') arch=amd64 ;;
    'Linux aarch64'|'Linux arm64') arch=arm64 ;;
    *) echo "Unsupported server platform: $platform" >&2; exit 1 ;;
esac

stage=$(mktemp -d)
remote=
cleanup() {
    rm -rf -- "$stage"
    if [[ -n $remote ]]; then
        ssh "$host" "rm -rf -- '$remote'" </dev/null || true
    fi
}
trap cleanup EXIT

make frontend cli
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -o "$stage/tiki" .
cp deploy/tiki.service scripts/install-server.sh "$stage/"
remote=$(ssh "$host" 'mktemp -d /tmp/tiki-deploy.XXXXXXXX')
if [[ ! $remote =~ ^/tmp/tiki-deploy\.[a-zA-Z0-9]+$ ]]; then
    remote=
    echo 'Server returned an invalid staging directory.' >&2
    exit 1
fi
scp "$stage/tiki" "$stage/tiki.service" "$stage/install-server.sh" "$host:$remote/"
ssh "$host" "if [ \$(id -u) -eq 0 ]; then bash '$remote/install-server.sh'; else sudo -n bash '$remote/install-server.sh'; fi"
