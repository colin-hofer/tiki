#!/usr/bin/env bash
# Invoked by deploy.sh after upload. Requires root (or passwordless sudo).
set -euo pipefail
umask 077
cd "$(dirname "$0")"

if [[ $EUID != 0 ]]; then
    echo 'Run this installer as root.' >&2
    exit 1
fi
for command in systemctl sqlite3 curl runuser flock ss; do
    if ! command -v "$command" >/dev/null; then
        echo "Missing $command. On Debian/Ubuntu: apt-get install sqlite3 curl util-linux iproute2" >&2
        exit 1
    fi
done
exec 9>/run/lock/tiki-deploy.lock
flock -n 9 || { echo 'Another Tiki deployment is running.' >&2; exit 1; }

if ! id tiki >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /var/lib/tiki --shell /usr/sbin/nologin tiki
fi
install -d -m 0755 /opt/tiki /opt/tiki/releases
install -d -o tiki -g tiki -m 0700 /var/lib/tiki
install -d -m 0700 /var/backups/tiki
database=/var/lib/tiki/tiki.db
release=$(mktemp -d "/opt/tiki/releases/$(date -u +%Y%m%dT%H%M%SZ)-XXXXXXXX")
chmod 0755 "$release"
install -m 0755 tiki "$release/tiki"
install -m 0644 tiki.service "$release/tiki.service"
"$release/tiki" --version

previous=$(readlink -e /opt/tiki/current || true)
was_active=false
if systemctl is-active --quiet tiki.service; then was_active=true; fi
stopped=false
activated=false
complete=false
backup=
finish() {
    status=$?
    if [[ $complete == false ]]; then
        if [[ $activated == true ]]; then
            systemctl stop tiki.service || true
            echo 'Deployment failed; Tiki is stopped. The database was not automatically restored.' >&2
            echo "Previous release: ${previous:-none}; database backup: ${backup:-none}" >&2
            echo 'Inspect: journalctl -u tiki -n 50 --no-pager' >&2
        elif [[ $stopped == true && $was_active == true ]]; then
            systemctl start tiki.service || true
        fi
    fi
    exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

systemctl stop tiki.service 2>/dev/null || { [[ $was_active == false ]] || exit 1; }
stopped=true
if [[ -n $(ss -H -ltn 'sport = :8080') ]]; then
    echo 'Port 8080 is occupied by another process; refusing to replace it.' >&2
    exit 1
fi
if [[ -f $database ]]; then
    backup="/var/backups/tiki/$(basename "$release").db"
    sqlite3 "$database" ".backup '$backup'"
    if [[ $(sqlite3 "$backup" 'PRAGMA integrity_check;') != ok ]]; then
        echo 'Database backup failed integrity check; deployment aborted.' >&2
        exit 1
    fi
    echo "Database backup: $backup"
fi

# The symlink is replaced atomically; never overwrite a running executable.
if [[ -d $previous ]]; then
    ln -sfn "$previous" /opt/tiki/previous
fi
ln -sfnT "$release" /opt/tiki/current.new
mv -Tf /opt/tiki/current.new /opt/tiki/current
activated=true
install -m 0644 "$release/tiki.service" /etc/systemd/system/tiki.service
systemctl daemon-reload
systemctl enable tiki.service

if [[ ! -f $database ]] || [[ $(sqlite3 "$database" 'SELECT count(*) FROM users;') == 0 ]]; then
    complete=true
    echo 'Installed. Create the first administrator, then start Tiki:'
    echo '  sudo -u tiki /opt/tiki/current/tiki init --db /var/lib/tiki/tiki.db --name YOUR_NAME --email YOUR_EMAIL'
    echo '  sudo systemctl start tiki'
    exit 0
fi

systemctl start tiki.service
for ((attempt=0; attempt<60; attempt++)); do
    if systemctl is-active --quiet tiki.service && curl --fail --silent --max-time 2 http://127.0.0.1:8080/readyz >/dev/null; then
        complete=true
        echo "Deployed $release; Tiki is ready on 127.0.0.1:8080."
        exit 0
    fi
    sleep 1
done
echo 'Tiki did not become ready within 60 checks.' >&2
exit 1
