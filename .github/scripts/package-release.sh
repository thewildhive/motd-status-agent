#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
output_dir="${2:-dist}"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 MAJOR.MINOR.PATCH [empty-output-directory]" >&2
  exit 2
fi
if [[ -z "$output_dir" || "$output_dir" == / ]]; then
  echo "refusing unsafe output directory: $output_dir" >&2
  exit 2
fi
mkdir -p "$output_dir"
if [[ -n "$(find "$output_dir" -mindepth 1 -print -quit)" ]]; then
  echo "output directory must be empty: $output_dir" >&2
  exit 2
fi
if [[ -z "${SIGNING_KEY_FILE:-}" || ! -s "$SIGNING_KEY_FILE" ]]; then
  echo "SIGNING_KEY_FILE must name a readable Ed25519 private key" >&2
  exit 2
fi

source_date_epoch="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD)}"
ldflags="-s -w -X main.VERSION=$version"
export SOURCE_DATE_EPOCH="$source_date_epoch"

build() {
  local arch="$1"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -buildvcs=false -trimpath -ldflags="$ldflags" \
    -o "$output_dir/motd-status-agent-linux-$arch" ./cmd/motd-status-agent
}
build amd64
build arm64

touch -d "@$source_date_epoch" "$output_dir"/motd-status-agent-linux-*
(
  cd "$output_dir"
  sha256sum motd-status-agent-linux-amd64 motd-status-agent-linux-arm64 > checksums.txt
)
openssl pkeyutl -sign -inkey "$SIGNING_KEY_FILE" -rawin \
  -in "$output_dir/checksums.txt" -out "$output_dir/checksums.txt.sig"
(
  cd "$output_dir"
  sha256sum --check checksums.txt
  [[ "$(wc -l < checksums.txt)" -eq 2 ]]
  [[ "$(wc -c < checksums.txt.sig)" -eq 64 ]]
  host_arch="$(go env GOARCH)"
  "./motd-status-agent-linux-$host_arch" version | grep -Fx "v$version"
)
