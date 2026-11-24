# Rootless Podman Status Agent Technical Design

## Context

The product behavior is defined in [PRODUCT.md](PRODUCT.md). The Linux-only, standard-library Go agent runs as a systemd system service under the account that owns the rootless Podman Quadlets. It exposes normalized status through a Unix socket, not the Podman API.

The v1 JSON field meanings and enum values are shared with `go-motd`; JSON formatting is not part of the contract.

## Implementation

### Repository and Command

- `cmd/motd-status-agent` uses internal packages for protocol handling, Quadlet discovery and normalization, and service configuration.
- Support `serve` for the long-running service, `check` for prerequisite/configuration diagnostics, and `install`/`uninstall` for lifecycle setup. Commands must be non-interactive when all required flags are supplied so configuration management can use them.
- Store durable agent configuration under the workload owner's config directory. Runtime socket data remains under `/var/run/motd-status`; no status snapshots are persisted.

### Shared Protocol

Serve HTTP/1.1 over `/var/run/motd-status/agent.sock` by default. The administrator may override the absolute socket path and access group. The default group is `motd-status`; an existing group such as `media` is valid.

The only successful route is `GET /v1/status`. A `200 OK` response has `Content-Type: application/json` and this schema:

```json
{
  "protocol_version": 1,
  "observed_at": "2026-08-18T12:34:56.123456789Z",
  "workloads": [
    {
      "name": "jellyfin",
      "state": "running",
      "health": "healthy"
    }
  ]
}
```

`protocol_version` is numeric and exactly `1`. UTC timestamps use RFC 3339 with nanosecond-capable formatting. Workload names are non-empty and unique. State values are `running`, `stopped`, `failed`, and `unknown`; health values are `healthy`, `unhealthy`, `starting`, `none`, and `unknown`. Responses contain no aggregate fields. New optional fields may be added during v1, but existing field meaning and enums remain stable.

Errors use an appropriate HTTP status and:

```json
{
  "protocol_version": 1,
  "error": {
    "code": "collection_timeout",
    "message": "workload status collection timed out"
  }
}
```

Stable codes are `bad_request`, `not_found`, `method_not_allowed`, `runtime_unavailable`, `permission_denied`, `collection_timeout`, `response_too_large`, and `internal_error`. Limit the entire request, including headers, to 8 KiB. Bound status collection to 750 milliseconds and encoded responses to 1 MiB; return `response_too_large` rather than truncating a successful snapshot.

### Quadlet Discovery and Normalization

- Inspect the owning user's generated user-systemd units and rootless Podman state to discover `.container` and `.pod` Quadlets. Do not infer scope only from currently running containers because stopped and failed workloads must remain visible.
- Use one top-level `.container` or `.pod` unit as the stable workload identity. A container attached to an included pod contributes to the pod and is suppressed as a separate top-level workload.
- Derive stable names from the operator-defined Quadlet unit identity, stripping generated service suffixes consistently. Reject or normalize empty identities without exposing paths or container IDs.
- Combine systemd active/result state with Podman container state. All regular non-one-shot members are required. Successful completed one-shot helpers are neutral; failed or incomplete one-shots degrade the workload.
- Normalize conservatively. Failed systemd units become `failed`; known inactive clean units become `stopped`; active units whose required members run become `running`; conflicting or unavailable evidence becomes `unknown`. Health rolls up as unhealthy first, then starting, then unknown, then healthy; use `none` only when no required member defines a healthcheck.
- Collect one coherent snapshot under a 750-millisecond context. If scope enumeration fails, return an error. If an enumerated workload alone cannot be inspected, retain it with unknown fields.

### Socket and HTTP Service

- The installer creates `/var/run/motd-status` through an administrator-owned systemd-tmpfiles rule or equivalent boot-persistent runtime-directory declaration, owned by the workload user and configured group with setgid/group traversal permissions. The system service creates `agent.sock` with owner/group access and no other-user access.
- Refuse symlinks, non-socket collisions, and active listeners. Remove only a stale socket proven to be at the configured agent path. On shutdown, compare the listening socket identity before removal.
- Configure `http.Server` read-header, read, write, and idle timeouts and cap concurrent connections to bounded values. Disable unnecessary HTTP features where practical. Reject bodies on `GET /v1/status`.
- Use structured, concise journal messages. Do not log successful payloads, raw Podman inspect output, environment, labels, mounts, or command arguments.

### Installation and Lifecycle

- `install` provisions and enables the system unit and tmpfiles rule, and configures linger when requested.
- Separate privileged and user-context actions clearly. Never run the serving process as root and never widen the Podman socket permissions.
- `check` validates Linux, Podman, user systemd connectivity, supported Quadlet discovery, socket parent ownership/mode, group existence/membership, and endpoint operation when running.
- Upgrade installs a verified replacement, restarts the system service, and checks the socket. Keep the prior release available for rollback.
- `uninstall` stops/disables the service and removes owned runtime/service/config artifacts. Group deletion, membership changes, and disabling linger require explicit flags and warnings.

### Project Automation

- CI checks formatting, module consistency, vet, workflows, vulnerabilities, and Linux builds. See [Maintainer Operations](../../MAINTAINERS.md) for protected merges and packaging.
- `testdata/v1/status.json` is the canonical protocol fixture. Incompatible changes require protocol v2 and a coordinated rollout.


## Risks and Mitigations

- Quadlet-to-generated-unit mapping varies by Podman/systemd version. Report unknown rather than inventing healthy state.
- `/run` is recreated at boot. A tmpfiles rule creates the socket parent before the system unit starts.
- Group access reveals workload names. Defaults use a dedicated group, overrides are explicit, and payload fields remain minimal.
- Shelling out can deadlock or leak output. Every command uses a context deadline, bounded output, a trusted executable path, and sanitized errors.
- Verify protocol changes against the real agent socket and consumer.
