#!/bin/sh
# Fixed official CLI artifact for isolated capability probes. No npm lifecycle
# hooks, launcher, voice, shell helpers or host login are used.
set -eu

aihub_backend=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
aihub_os=${1:-$(uname -s | tr '[:upper:]' '[:lower:]')}
aihub_arch=${2:-$(uname -m)}
aihub_destination=${3:-$aihub_backend/bin/codex}
case "$aihub_arch" in
    amd64|x86_64) aihub_arch=x64; aihub_triple_arch=x86_64 ;;
    arm64|aarch64) aihub_arch=arm64; aihub_triple_arch=aarch64 ;;
    *) echo 'Unsupported Codex probe architecture' >&2; exit 1 ;;
esac
case "$aihub_os-$aihub_arch" in
    linux-x64) aihub_integrity='uwRaOsrYogLQhcxAM/GoeWrw0QIWEMRo7c9gM8sKAviVkZQHlu9kRLJ5PkufbV+ABGjyZgIwiFdJ0LCJ0N6e8w==' ;;
    linux-arm64) aihub_integrity='IH2/R+4OarjGvsAf1ll3fdyLwrPgYLl3uzp6u9CyD9sj2Lw+ViDSe3QBZVHdcHJs0eIiZFg6Wprcuy4CRZaUGw==' ;;
    darwin-x64) aihub_integrity='Ioj7h+B1ME9ZU1PH1ATfvFV5DiototEpFFaSSbg3kfDifB9djhl5fcJ++g5mpcL/+uVbcnReei+RiSXEx8o+kw==' ;;
    darwin-arm64) aihub_integrity='i7Dzk2a5aGmuGM9aNZgaUVTojZ/4bc5lRfosz++Y7X2BGhDkz+cySgbD+NYS1pOxfm0VEbLi9PZ3pAulI4kchw==' ;;
    *) echo 'Unsupported Codex probe operating system' >&2; exit 1 ;;
esac
case "$aihub_os" in
    linux) aihub_triple=$aihub_triple_arch-unknown-linux-musl ;;
    darwin) aihub_triple=$aihub_triple_arch-apple-darwin ;;
esac
aihub_stage=$(mktemp -d)
trap 'rm -rf -- "$aihub_stage"' EXIT HUP INT TERM
aihub_archive=${AIHUB_CODEX_ARCHIVE:-$aihub_backend/bin/codex-archives/0.163.0-$aihub_os-$aihub_arch.tgz}
if [ ! -f "$aihub_archive" ]; then
    mkdir -p -- "$(dirname -- "$aihub_archive")"
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 20 --max-time 180 --retry 2 \
        "https://registry.npmjs.org/@openai/codex/-/codex-0.163.0-$aihub_os-$aihub_arch.tgz" \
        --output "$aihub_stage/archive.tgz"
    aihub_actual=$(openssl dgst -sha512 -binary "$aihub_stage/archive.tgz" | openssl base64 -A)
    [ "$aihub_actual" = "$aihub_integrity" ] || { echo 'Codex artifact checksum mismatch' >&2; exit 1; }
    mv -- "$aihub_stage/archive.tgz" "$aihub_archive"
fi
aihub_actual=$(openssl dgst -sha512 -binary "$aihub_archive" | openssl base64 -A)
[ "$aihub_actual" = "$aihub_integrity" ] || { echo 'Codex cached artifact checksum mismatch' >&2; exit 1; }
# Extract only the checked executable; archive paths never select destinations.
tar -xzOf "$aihub_archive" "package/vendor/$aihub_triple/bin/codex" > "$aihub_stage/codex"
test -s "$aihub_stage/codex"
chmod 755 "$aihub_stage/codex"
mkdir -p -- "$(dirname -- "$aihub_destination")"
mv -- "$aihub_stage/codex" "$aihub_destination"
