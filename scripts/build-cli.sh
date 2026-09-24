#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
directory=internal/httpapi/dist
cache=.dev/build/cli
mkdir -p "$directory" "$cache"
temporary=$(mktemp -d "$directory/.build.XXXXXXXX")
trap 'rm -rf "$temporary"' 0
trap 'exit 1' 1 2 3 15

if command -v sha256sum >/dev/null; then checksum=sha256sum
else checksum='shasum -a 256'
fi
for platform in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    # The dev tag excludes the UI/download bundles, avoiding recursive embeds.
    # A stable output path lets Go reuse its linked binary as well as packages.
    CGO_ENABLED=0 GOOS=${platform%-*} GOARCH=${platform#*-} go build -tags dev -trimpath -buildvcs=false -ldflags='-s -w' -o "$cache/$platform" .
    source_hash=$($checksum < "$cache/$platform")
    source_hash=${source_hash%% *}
    if [ -f "$cache/$platform.sha256" ] &&
       [ -f "$directory/$platform.gz" ] && [ -f "$directory/$platform.sha256" ]; then
        archive_hash=$($checksum < "$directory/$platform.gz")
        archive_hash=${archive_hash%% *}
        if [ "$source_hash $archive_hash" = "$(cat "$cache/$platform.sha256")" ] && [ "$archive_hash" = "$(cat "$directory/$platform.sha256")" ]; then
            echo "CLI $platform unchanged."
            continue
        fi
    fi
    echo "Bundling CLI for $platform…"
    gzip -n -c "$cache/$platform" > "$temporary/$platform.gz"
    hash=$($checksum < "$temporary/$platform.gz")
    printf '%s %s\n' "$source_hash" "${hash%% *}" > "$temporary/source.sha256"
    printf '%s\n' "${hash%% *}" > "$temporary/$platform.sha256"
    mv "$temporary/$platform.gz" "$directory/$platform.gz"
    mv "$temporary/$platform.sha256" "$directory/$platform.sha256"
    mv "$temporary/source.sha256" "$cache/$platform.sha256"
done
