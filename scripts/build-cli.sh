#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
directory=internal/httpapi/dist
mkdir -p "$directory"
temporary=$(mktemp -d "$directory/.build.XXXXXXXX")
trap 'rm -rf "$temporary"' 0
trap 'exit 1' 1 2 3 15

if command -v sha256sum >/dev/null; then checksum=sha256sum
else checksum='shasum -a 256'
fi
for platform in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    echo "Building CLI for $platform…"
    # The dev tag excludes the UI/download bundles, avoiding recursive embeds.
    CGO_ENABLED=0 GOOS=${platform%-*} GOARCH=${platform#*-} go build -tags dev -trimpath -ldflags='-s -w' -o "$temporary/tiki" .
    gzip -n -c "$temporary/tiki" > "$temporary/$platform.gz"
    hash=$($checksum < "$temporary/$platform.gz")
    printf '%s\n' "${hash%% *}" > "$temporary/$platform.sha256"
    mv "$temporary/$platform.gz" "$directory/$platform.gz"
    mv "$temporary/$platform.sha256" "$directory/$platform.sha256"
done
