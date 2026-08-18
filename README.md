# motd-status-agent

`motd-status-agent` exposes a read-only rootless Podman Quadlet status snapshot
over a local Unix socket for authorized consumers such as `go-motd`.

## Build and Run

```sh
go build -o motd-status-agent ./cmd/motd-status-agent
./motd-status-agent check
./motd-status-agent version
```

The service reads its configuration from
`~/.config/motd-status-agent/config.json`. The default endpoint is
`/var/run/motd-status/agent.sock`; the only successful request is
`GET /v1/status` using HTTP/1.1 over that socket.

## Installation

See [`INSTALL.md`](INSTALL.md) for release/source installation, system service
setup, shared socket access, `go-motd` configuration, verification,
troubleshooting, upgrade, rollback, and uninstall.

## Consumer Integration Test

The repository includes a canonical v1 status fixture and a fixture server that
uses the same Unix-socket HTTP server as the production agent. The integration
test fetches a pinned `go-motd` revision into a temporary directory. Run:

```bash
make test-status-agent-integration
```

The test builds both projects, serves `testdata/v1/status.json` through a
temporary Unix socket, verifies that `go-motd` reports 2 of 5 workloads online,
checks that per-workload details are present only in JSON output, and verifies
that the container section disappears when the socket stops. It does not
require Podman, systemd, root privileges, or a permanent socket directory.
