#!/usr/bin/env bash
# One authenticated SSH connection for the entire build/upload/install cycle.
set -euo pipefail
cd "$(dirname "$0")/.."

if [[ $# != 1 || $1 == -* || ! $1 =~ ^[a-zA-Z0-9_.@:-]+$ ]]; then
    echo "Usage: $0 [user@]ssh-host" >&2
    exit 2
fi
host=$1
# Keep the socket path short, including on macOS with a long TMPDIR.
stage=$(mktemp -d /tmp/tiki-build.XXXXXXXX)
connection=(-o "ControlPath=$stage/ssh" -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=3)
cleanup() {
    ssh "${connection[@]}" -O exit "$host" 2>/dev/null || true
    rm -rf -- "$stage"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "Connecting to $host…"
ssh "${connection[@]}" -o ControlMaster=yes -o ControlPersist=no -Nf "$host"
# If the connection dies, fail instead of asking for the passphrase again.
connection+=(-o ControlMaster=no -o BatchMode=yes -o ProxyCommand=false)
platform=$(ssh "${connection[@]}" "$host" 'uname -sm')
case "$platform" in
    'Linux x86_64') arch=amd64 ;;
    'Linux aarch64'|'Linux arm64') arch=arm64 ;;
    *) echo "Unsupported server platform: $platform" >&2; exit 1 ;;
esac

make -j2 frontend cli
mkdir -p .dev/build
binary=".dev/build/server-linux-$arch"
CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -buildvcs=false -ldflags='-s -w' -o "$binary" .
cp "$binary" "$stage/tiki"
cp deploy/tiki.service scripts/install-server.sh scripts/prune-deployments.sh "$stage/"
if command -v sha256sum >/dev/null; then checksum=(sha256sum); else checksum=(shasum -a 256); fi
(cd "$stage"; "${checksum[@]}" tiki tiki.service > SHA256SUMS)

files=(tiki.service install-server.sh prune-deployments.sh SHA256SUMS)
current=$(ssh "${connection[@]}" "$host" 'if [ -d /opt/tiki/current ]; then cd /opt/tiki/current && sha256sum tiki tiki.service; fi')
if [[ $current == "$(cat "$stage/SHA256SUMS")" ]]; then
    echo 'Server binary and service unchanged; skipping binary upload.'
else
    files+=(tiki)
fi

# Transfer and cleanup share one remote shell, including interrupted installs.
tar -C "$stage" -czf - "${files[@]}" | ssh "${connection[@]}" "$host" '
    set -eu
    stage=$(mktemp -d /tmp/tiki-deploy.XXXXXXXX)
    trap '\''rm -rf -- "$stage"'\'' EXIT
    trap '\''exit 129'\'' HUP
    trap '\''exit 130'\'' INT
    trap '\''exit 143'\'' TERM
    tar -xzf - -C "$stage"
    if [ "$(id -u)" -eq 0 ]; then
        bash "$stage/install-server.sh"
    else
        sudo -n bash "$stage/install-server.sh"
    fi
'
