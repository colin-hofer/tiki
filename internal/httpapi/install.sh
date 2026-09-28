#!/bin/sh
# Install the Tiki CLI from your workspace.
set -eu

main() {
    if [ "$#" -ne 1 ]; then
        echo 'Usage: install.sh SERVER_URL' >&2
        exit 1
    fi
    server=$1
    server=${server%/}
    case "$server" in
        https://*) ;;
        http://*)
            authority=${server#http://}; authority=${authority%%/*}
            case "$authority" in
                localhost|localhost:*|127.0.0.1|127.0.0.1:*|'[::1]'|'[::1]':*) ;;
                *) echo 'Tiki requires HTTPS except on localhost.' >&2; exit 1 ;;
            esac ;;
        *) echo 'Pass the HTTP(S) address of your Tiki workspace.' >&2; exit 1 ;;
    esac
    command -v curl >/dev/null || { echo 'Please install curl first.' >&2; exit 1; }
    if command -v sha256sum >/dev/null; then checksum=sha256sum
    elif command -v shasum >/dev/null; then checksum='shasum -a 256'
    else echo 'Please install sha256sum or shasum first.' >&2; exit 1
    fi

    case "$(uname -s)" in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        *) echo 'This installer supports Linux and macOS.' >&2; exit 1 ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) echo 'This installer supports x86-64 and ARM64.' >&2; exit 1 ;;
    esac
    command -v gzip >/dev/null || { echo 'Please install gzip first.' >&2; exit 1; }
    directory=${TIKI_INSTALL_DIR:-"$HOME/.local/bin"}
    download="$server/api/v1/cli/downloads/$os-$arch.gz"
    checksum_url="$server/api/v1/cli/downloads/$os-$arch.sha256"
    if [ -d "$directory/tiki" ]; then
        echo 'The install destination is a directory; remove it or choose another install location.' >&2
        exit 1
    fi
    mkdir -p "$directory"
    temporary=$(mktemp -d "$directory/.tiki-install.XXXXXXXX")
    trap 'rm -rf "$temporary"' 0
    trap 'exit 1' 1 2 3 15
    echo 'Downloading Tiki...'
    # No redirects: the artifact and checksum come from this workspace.
    curl --fail --silent --show-error --connect-timeout 15 --max-time 300 --output "$temporary/download" "$download"
    expected=$(curl --fail --silent --show-error --connect-timeout 15 --max-time 30 "$checksum_url")
    actual=$($checksum < "$temporary/download")
    actual=${actual%% *}
    if [ "$actual" != "$expected" ]; then
        echo 'Tiki checksum mismatch. Nothing was installed; try again.' >&2
        exit 1
    fi
    gzip -dc "$temporary/download" > "$temporary/tiki"
    chmod 755 "$temporary/tiki"
    "$temporary/tiki" config set-server "$server" >/dev/null
    mv -f "$temporary/tiki" "$directory/tiki"
    printf '\nInstalled Tiki at %s/tiki\nWorkspace: %s\n' "$directory" "$server"
    case ":$PATH:" in
        *":$directory:"*) echo 'Next: tiki auth login --email YOUR_EMAIL' ;;
        *)
            printf 'Add %s to your PATH, or run the installed binary by its full path.\n' "$directory"
            if [ "$directory" = "$HOME/.local/bin" ]; then
                printf '%s\n' 'For this terminal: export PATH="$HOME/.local/bin:$PATH"' 'Add that export to your shell startup file to keep it.'
            fi
            echo 'Then sign in with: tiki auth login --email YOUR_EMAIL' ;;
    esac
}

# Keep execution after the complete function so a truncated download cannot
# run a partially received installer when piped into sh.
main "$@"
