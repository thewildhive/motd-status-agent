#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
asset_dir="${2:-}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ || ! -d "$asset_dir" ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH asset-directory" >&2
  exit 2
fi
mapfile -t assets < <(find "$asset_dir" -maxdepth 1 -type f -printf '%f\n' | sort)
expected=(checksums.txt checksums.txt.sig motd-status-agent-linux-amd64 motd-status-agent-linux-arm64)
if [[ "${assets[*]}" != "${expected[*]}" ]]; then
  printf 'unexpected release asset set:\n%s\n' "${assets[*]}" >&2
  exit 1
fi

existing="$(gh release view "$tag" --json assets --jq '.assets[].name')"
download_dir="$(mktemp -d)"
trap 'rm -rf "$download_dir"' EXIT
for name in "${assets[@]}"; do
  path="$asset_dir/$name"
  if grep -Fxq "$name" <<<"$existing"; then
    gh release download "$tag" --pattern "$name" --dir "$download_dir"
    if ! cmp -s "$path" "$download_dir/$name"; then
      echo "existing release asset differs; refusing to replace: $name" >&2
      exit 1
    fi
    echo "verified existing asset: $name"
  else
    gh release upload "$tag" "$path"
  fi
done
