# Installation Guide

This guide installs `motd-status-agent` on Linux for rootless Podman workloads managed by Quadlet and connects it to `go-motd`.

## Deployment Model

The agent supports Linux `amd64` and `arm64`. It reports `.container` and `.pod` Quadlets owned by one unprivileged user, such as `media`.

The service is installed as the root-owned system unit `motd-status-agent.service`, but the process runs as the workload owner. It uses that user's rootless Podman and user-systemd environment. Consumers receive only the normalized status API over a local Unix socket; they do not receive access to the Podman API socket.

The examples below use:

```sh
WORKLOAD_USER=media
ACCESS_GROUP=motd-status
SOCKET_PATH=/var/run/motd-status/agent.sock
AGENT_PATH=/usr/local/bin/motd-status-agent
```

Replace these values when using an existing access group such as `media`.

## Prerequisites

The workload owner must already have:

- A non-root Linux account.
- Rootless Podman installed and usable as that account.
- Quadlet files in `$HOME/.config/containers/systemd`.
- A working user systemd manager.
- Linger enabled if workloads and status must survive logout.

Inspect the account and runtime before installation:

```sh
id "$WORKLOAD_USER"
getent passwd "$WORKLOAD_USER"
loginctl show-user "$WORKLOAD_USER" -p Linger -p RuntimePath
sudo -u "$WORKLOAD_USER" -H podman --version
sudo -u "$WORKLOAD_USER" -H env \
  XDG_RUNTIME_DIR="$(loginctl show-user "$WORKLOAD_USER" -p RuntimePath --value)" \
  DBUS_SESSION_BUS_ADDRESS="unix:path=$(loginctl show-user "$WORKLOAD_USER" -p RuntimePath --value)/bus" \
  systemctl --user is-system-running
```

If `Linger=no`, enable it once as an administrator:

```sh
sudo loginctl enable-linger "$WORKLOAD_USER"
```

Do not repeatedly enable linger when it is already `yes`.

## Install A Release Binary

Download the latest release for the host architecture and verify its checksum before installation:

```sh
case "$(uname -m)" in
  x86_64) ASSET=motd-status-agent-linux-amd64 ;;
  aarch64|arm64) ASSET=motd-status-agent-linux-arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

TAG="$(curl -fsSL https://api.github.com/repos/thewildhive/motd-status-agent/releases/latest | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p')"
test -n "$TAG"
BASE="https://github.com/thewildhive/motd-status-agent/releases/download/$TAG"
curl -fL "$BASE/$ASSET" -o "$ASSET"
curl -fL "$BASE/checksums.txt" -o checksums.txt
grep -F "  $ASSET" checksums.txt | sha256sum -c -
sudo install -m 0755 "$ASSET" "$AGENT_PATH"
rm -f "$ASSET" checksums.txt
"$AGENT_PATH" version
```

The checksum manifest verifies artifact integrity. Signature verification requires the maintainer-distributed Ed25519 public key; obtain that key through the project’s trusted maintainer channel before using `checksums.txt.sig` as an authenticity check.

## Build From Source

Use Go `1.25.13` or a newer supported Go release, as declared by `go.mod`:

```sh
git clone https://github.com/thewildhive/motd-status-agent.git
cd motd-status-agent
go build -buildvcs=false -trimpath -ldflags='-s -w -X main.VERSION=dev' \
  -o /tmp/motd-status-agent ./cmd/motd-status-agent
sudo install -m 0755 /tmp/motd-status-agent "$AGENT_PATH"
```

Source builds are not equivalent to signed release artifacts. Use a reviewed commit and verify the source before building.

## Provision Access

Create a dedicated read-only access group and add every account that runs `go-motd`:

```sh
sudo groupadd --system "$ACCESS_GROUP" 2>/dev/null || true
sudo usermod -aG "$ACCESS_GROUP" calmcacil
```

Replace `calmcacil` with the actual consumer account. Existing login sessions must be restarted, or a new session must be opened, before supplementary group membership is effective.

Do not add consumers to the workload owner's Podman API socket group.

Create the boot-persistent runtime directory rule:

```sh
sudo install -d -m 0755 /etc/tmpfiles.d
printf 'd /run/motd-status 02750 %s %s -\n' "$WORKLOAD_USER" "$ACCESS_GROUP" \
  | sudo tee /etc/tmpfiles.d/motd-status-agent.conf >/dev/null
sudo systemd-tmpfiles --create /etc/tmpfiles.d/motd-status-agent.conf
```

The directory is owned by the workload user and access group. The agent creates the socket as mode `0660`.

## Install And Start The Service

Run the installer as root. It writes `/etc/systemd/system/motd-status-agent.service`, preserves the workload owner's configuration under its home directory, and uses system-level systemd control:

```sh
sudo "$AGENT_PATH" install \
  --user "$WORKLOAD_USER" \
  --group "$ACCESS_GROUP" \
  --socket "$SOCKET_PATH" \
  --linger=false
```

The installer does not use `sudo su -`, `systemctl --user` from an administrator session, or `systemctl --machine=... --user`. The system service sets `User`, `Group`, `HOME`, `XDG_RUNTIME_DIR`, and `DBUS_SESSION_BUS_ADDRESS` for the workload owner and requires `user@<uid>.service`.

## Verify The Agent

As an administrator, verify the system service and effective identity:

```sh
sudo systemctl is-enabled motd-status-agent.service
sudo systemctl is-active motd-status-agent.service
sudo systemctl show motd-status-agent.service -p User -p Group -p Environment -p ExecStart
stat -c '%U:%G %a %F %n' /run/motd-status /var/run/motd-status/agent.sock
```

As the workload owner, verify the endpoint:

```sh
sudo -u "$WORKLOAD_USER" -H curl \
  --unix-socket "$SOCKET_PATH" \
  --http1.1 --fail --silent --show-error \
  http://localhost/v1/status
```

As an authorized consumer, run the same request without `sudo`:

```sh
curl --unix-socket "$SOCKET_PATH" \
  --http1.1 --fail --silent --show-error \
  http://localhost/v1/status
```

The response has `protocol_version: 1`, an `observed_at` timestamp, and a complete workload list. An empty list is valid when the owner has no supported Quadlets.

## Configure go-motd

Add the agent socket to the `go-motd` JSON configuration:

```json
{
  "system": {
    "container_status": {
      "socket_path": "/var/run/motd-status/agent.sock",
      "max_age": "30s"
    }
  }
}
```

Run the consumer checks:

```sh
motd check-config
motd
motd --json
```

Normal output displays the aggregate `Containers` summary. JSON output includes compatible workload details. If the socket is unavailable, inaccessible, stale, or incompatible, `go-motd` omits the container section rather than reporting zero workloads.

Health meanings are distinct:

- `healthy`: applicable health checks pass.
- `unhealthy`: an applicable health check fails.
- `starting`: a health check has not completed.
- `none`: no applicable health check is configured.
- `unknown`: health was expected or available but could not be determined.

## Upgrade And Rollback

Download and verify a replacement binary before replacing the installed one. Then restart and verify:

```sh
sudo install -m 0755 verified-motd-status-agent "$AGENT_PATH"
sudo systemctl restart motd-status-agent.service
sudo systemctl is-active motd-status-agent.service
curl --unix-socket "$SOCKET_PATH" --http1.1 --fail http://localhost/v1/status
```

For rollback, select an explicit prior immutable release tag, verify its architecture-specific binary and checksum manifest, install only that binary, and restart the service. Do not move tags or overwrite release assets. Repair a bad release with a new patch release.

## Troubleshooting

Inspect the service journal without dumping workload secrets:

```sh
sudo journalctl -u motd-status-agent.service -n 100 --no-pager
sudo systemctl status motd-status-agent.service --no-pager
```

Common causes:

- `user@<uid>.service` or D-Bus is unavailable: verify `loginctl show-user`, linger, `/run/user/<uid>`, and `/run/user/<uid>/bus`.
- Permission denied for a consumer: verify `id`, group membership, directory traversal, and socket mode. Start a new login session after `usermod`.
- Wrong architecture: check `uname -m` and `file "$AGENT_PATH"`.
- No workloads: confirm Quadlets exist under the workload owner’s `.config/containers/systemd` directory.
- `unknown` state: inspect the user service and Podman runtime as the workload owner; the agent does not guess when evidence conflicts or is unavailable.
- `health: none`: inspect whether the Quadlet defines `HealthCmd`; `none` means no applicable check, not unhealthy.
- Socket missing after reboot: verify `/etc/tmpfiles.d/motd-status-agent.conf`, run `sudo systemd-tmpfiles --create`, then restart the service.

## Uninstall

```sh
sudo "$AGENT_PATH" uninstall --user "$WORKLOAD_USER"
sudo rm -f /etc/tmpfiles.d/motd-status-agent.conf
sudo systemd-tmpfiles --remove /etc/tmpfiles.d/motd-status-agent.conf 2>/dev/null || true
sudo systemctl daemon-reload
```

Uninstall preserves Quadlets, containers, images, volumes, Podman configuration, the access group, group membership, linger, and `go-motd` configuration. Remove those separately only when intentionally decommissioning the workload.

## Development Checks

```sh
make check
make test-status-agent-integration
make cross-compile
```

See the GitHub Actions workflows for the required pull-request gates and release process.
