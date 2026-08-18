#!/usr/bin/env bash
set -euo pipefail

agent_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_motd_revision="${GO_MOTD_REVISION:-e10242630cdd1a7bf75bdc0aced3be94959306f5}"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/motd-status-integration.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

git clone --quiet --filter=blob:none https://github.com/thewildhive/go-motd.git "$tmp_dir/go-motd"
mkdir -p "$tmp_dir/motd-status-agent"
git -C "$tmp_dir/go-motd" fetch --quiet --depth=1 origin "$go_motd_revision"
git -C "$tmp_dir/go-motd" checkout --quiet --detach "$go_motd_revision"
test "$(git -C "$tmp_dir/go-motd" rev-parse HEAD)" = "$go_motd_revision"

bash "$tmp_dir/go-motd/scripts/test-status-agent-integration.sh" \
  --agent-dir "$agent_dir" \
  --go-motd-dir "$tmp_dir/go-motd"
