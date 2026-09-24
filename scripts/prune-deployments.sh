#!/usr/bin/env bash
# Only managed release names are eligible; manual backups and live data are not.
set -euo pipefail
[[ $# == 2 ]] || { echo "Usage: $0 RELEASE_ROOT BACKUP_DIRECTORY" >&2; exit 2; }
root=$(cd "$1" && pwd -P)
backups=$(cd "$2" && pwd -P)
current=$(readlink -e "$root/current")
previous=$(readlink -e "$root/previous" || true)
[[ -d $current && ${current%/*} == "$root/releases" ]] || { echo 'Invalid current release; refusing to prune.' >&2; exit 1; }
[[ -z $previous || ( -d $previous && ${previous%/*} == "$root/releases" ) ]] || { echo 'Invalid previous release; refusing to prune.' >&2; exit 1; }
pattern='^[0-9]{8}T[0-9]{6}Z-[a-zA-Z0-9]{8}$'

for directory in "$root"/releases/*; do
    [[ -d $directory && ! -L $directory && ${directory##*/} =~ $pattern ]] || continue
    if [[ $directory != "$current" && $directory != "$previous" ]]; then
        rm -rf -- "$directory"
        echo "Removed old release: ${directory##*/}"
    fi
done

snapshots=()
for file in "$backups"/*.db; do
    name=${file##*/}
    [[ -f $file && ! -L $file && ${name%.db} =~ $pattern ]] && snapshots+=("$file")
done
# Shell glob order is chronological for these UTC timestamp names.
for ((i=${#snapshots[@]}-8; i>=0; i--)); do
    file=${snapshots[i]}
    name=${file##*/}
    [[ ${name%.db} == "${current##*/}" || ${name%.db} == "${previous##*/}" ]] && continue
    rm -- "$file"
    echo "Removed old deployment backup: $name"
done
