# Rootless Podman Status Agent Technical Design

## Context

The product behavior is defined in [PRODUCT.md](PRODUCT.md). This is a greenfield repository with no source code or Git history yet. The implementation will be a Linux-only Go command using the standard library, installed as a user systemd service for the account that owns the rootless Podman Quadlets. It exposes only normalized status through an administrator-managed Unix socket and never exposes Podman's API to consumers.

The protocol must remain byte-level compatible with `go-motd`'s sibling `specs/rootless-container-status/TECH.md`; both implementations should share equivalent contract fixtures even though they are separate modules.

## Proposed Changes

### Repository and Command

- Initialize a Go module and a single `cmd/motd-status-agent` command, with internal packages for protocol handling, Quadlet discovery/normalization, and service configuration. Keep dependencies to the standard library unless Podman behavior proves impossible to normalize reliably without a narrowly justified dependency.
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

- The installer creates `/var/run/motd-status` through an administrator-owned systemd-tmpfiles rule or equivalent boot-persistent runtime-directory declaration, owned by the workload user and configured group with setgid/group traversal permissions. The user service creates `agent.sock` with owner/group access and no other-user access.
- Refuse symlinks, non-socket collisions, and active listeners. Remove only a stale socket proven to be at the configured agent path. On shutdown, compare the listening socket identity before removal.
- Configure `http.Server` read-header, read, write, and idle timeouts and cap concurrent connections to bounded values. Disable unnecessary HTTP features where practical. Reject bodies on `GET /v1/status`.
- Use structured, concise journal messages. Do not log successful payloads, raw Podman inspect output, environment, labels, mounts, or command arguments.

### Installation and Lifecycle

- `install` accepts workload user, group, socket path, and linger choice. Administrative steps create/validate the group, runtime-directory rule, group membership, and linger; user-context steps install and enable the service.
- Separate privileged and user-context actions clearly. Never run the serving process as root and never widen the Podman socket permissions.
- `check` validates Linux, Podman, user systemd connectivity, supported Quadlet discovery, socket parent ownership/mode, group existence/membership, and endpoint operation when running.
- Upgrade replaces the binary atomically, runs compatibility/config checks, and restarts the user service. Package/release automation must support rollback to the previous executable if activation fails.
- `uninstall` stops/disables the service and removes owned runtime/service/config artifacts. Group deletion, membership changes, and disabling linger require explicit flags and warnings.

### Project Automation

- Add unit, integration, race, vet, formatting, and vulnerability checks; Linux amd64 and arm64 builds; a systemd/Podman integration test environment; release packaging; checksums; and installation documentation.
- Treat the protocol response fixtures as compatibility artifacts. Additive changes must continue decoding in the current `go-motd` consumer tests; incompatible changes require protocol v2 and a coordinated rollout.

## Testing and Validation

- Protocol unit tests cover exact successful/error JSON, content type, deterministic ordering, empty workloads, route/method handling, body rejection, 8 KiB request limits, 1 MiB response limits, enum validity, and concurrent clients (Behavior 23-33, 41-43).
- Discovery fixtures cover stopped and failed units, `.container`, `.pod`, pod-member deduplication, stable names across recreated container IDs, unsupported Quadlet types, and manually started containers (Behavior 13-17).
- Normalization tables cover all systemd/Podman state combinations, absent/healthy/unhealthy/starting checks, contradictory data, required members, successful one-shots, failed one-shots, and partial inspection failure (Behavior 18-22, 32).
- Timeout tests use blocked fake collectors and assert a `collection_timeout` response within the service bound with no stale fallback (Behavior 27, 31, 33, 36).
- Socket tests cover ownership/mode, configurable group including `media`, missing parent, stale socket cleanup, symlink/file collision refusal, active listener refusal, clean removal, and unauthorized-user denial (Behavior 2-12).
- Lifecycle tests in disposable Linux VMs or containers with user systemd verify install, linger-enabled logout survival, no-linger behavior, restart, upgrade rollback, and uninstall preservation of Podman/Quadlet data (Behavior 1-6, 34-40).
- End-to-end validation runs `go-motd --json` and terminal output against the real agent for all-online, degraded, empty, permission-denied, runtime-down, stale-delay, and oversized-scope scenarios (Behavior 41-43 and the consumer contract).
- The repository owns `testdata/v1/status.json` as the canonical compatibility fixture. `cmd/motd-status-agent-fixture` serves that payload through the production Unix-socket HTTP server with a dummy collector; `../go-motd/make test-status-agent-integration` builds both projects and verifies the live consumer boundary without Podman, systemd, root, or a permanent socket.

## Risks and Mitigations

- Quadlet-to-generated-unit mapping varies by Podman/systemd version. Use fixture coverage across supported versions and fail to unknown rather than inventing healthy state.
- `/var/run` is recreated at boot. A tmpfiles/runtime-directory mechanism must recreate the parent before the user service starts, with explicit ordering and diagnostics.
- Group access reveals workload names. Defaults use a dedicated group, overrides are explicit, and payload fields remain minimal.
- Shelling out can deadlock or leak output. Every command uses a context deadline, bounded output, a trusted executable path, and sanitized errors.
- Separate repositories can drift. Identical v1 fixtures and a cross-repository end-to-end gate are release requirements.

## Parallelization

After a short sequential bootstrap establishes the module, protocol package, fixture files, and command skeleton on `feat/rootless-podman-status`, three local agents can proceed:

- `agent-discovery`: owns Quadlet discovery and status normalization plus fixtures. Use `/home/calmcacil/worktrees/motd-status-agent-discovery` on `feat/rootless-podman-discovery`.
- `agent-server`: owns Unix socket lifecycle, HTTP protocol, limits, concurrency, and protocol tests. Use `/home/calmcacil/worktrees/motd-status-agent-server` on `feat/rootless-podman-server`.
- `agent-install`: owns CLI lifecycle commands, systemd unit, tmpfiles/runtime directory, documentation, packaging, and VM integration harness. Use `/home/calmcacil/worktrees/motd-status-agent-install` on `feat/rootless-podman-install`.

Merge discovery and server first and validate their in-process integration. Rebase installer onto that merge, then run lifecycle and cross-repository end-to-end tests. Land one combined PR for the initial release because none of the pieces is independently usable.
