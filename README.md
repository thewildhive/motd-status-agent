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
