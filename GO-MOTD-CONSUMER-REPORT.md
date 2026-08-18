# go-motd Consumer Integration Report

This report is the integration handoff for the agent implementing rootless
container status consumption in `go-motd`.

## Source of Truth

This report is based on:

- `specs/rootless-podman-status/PRODUCT.md`
- `specs/rootless-podman-status/TECH.md`
- `internal/protocol/protocol.go`
- `internal/server/server.go`
- `README.md`

The repository currently has no commit history. The report describes the
current working-tree implementation and the approved v1 specs.

## Endpoint

The agent exposes HTTP/1.1 over a Unix domain socket. It does not listen on
TCP or UDP.

| Item | Value |
| --- | --- |
| Default socket | `/var/run/motd-status/agent.sock` |
| Method | `GET` |
| Path | `/v1/status` |
| Request body | Must be empty |
| Protocol | HTTP/1.1 |
| Success content type | `application/json` |
| Collection deadline | 750 ms |
| Maximum encoded response | 1 MiB |
| Protocol version | Numeric `1` |

The socket path is configurable by the operator. The consumer should expose a
configuration setting for the path rather than hard-coding the default, while
using the default when no override is configured.

## Go Client Transport

Use an `http.Client` with a custom Unix-socket dialer. The URL host is only a
placeholder because the connection is made through the Unix socket.

```go
func newAgentClient(socketPath string) *http.Client {
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   time.Second,
	}
}

request, err := http.NewRequestWithContext(
	ctx,
	http.MethodGet,
	"http://motd-status-agent/v1/status",
	nil,
)
```

The request must not include a body, query parameters, user-selected runtime,
user, Podman socket, command, or discovery scope. The agent has no request
fields that select or mutate anything.

The consumer should use a bounded context/deadline and close the response body
on every response. A client timeout should be slightly above the agent's
750 ms collection bound, for example one second, so the agent's structured
`collection_timeout` response can normally be observed.

## Successful Response

The v1 success schema is:

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

Go types can be represented as:

```go
type AgentStatus struct {
	ProtocolVersion int        `json:"protocol_version"`
	ObservedAt      time.Time  `json:"observed_at"`
	Workloads       []Workload `json:"workloads"`
}

type Workload struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Health string `json:"health"`
}
```

The consumer should tolerate unknown optional JSON fields, but it must reject
an unsupported `protocol_version`. Version 1 keeps these existing enum values
and meanings stable. Incompatible changes require protocol version 2.

### Runtime State Values

| State | Meaning |
| --- | --- |
| `running` | Logical workload is active and required containers are running |
| `stopped` | Workload is known but cleanly or intentionally inactive |
| `failed` | Workload or its user systemd unit failed |
| `unknown` | Reliable runtime state could not be determined |

### Health Values

| Health | Meaning |
| --- | --- |
| `healthy` | All applicable required checks pass |
| `unhealthy` | At least one applicable required check fails |
| `starting` | Applicable checks have not reached a result |
| `none` | No applicable health check exists |
| `unknown` | A check is expected or may exist, but its result is unavailable |

Do not combine `state` and `health` into a Podman status string. These are
independent dimensions. Examples include `running` plus `unhealthy`,
`running` plus `starting`, and `failed` plus `unknown`.

## go-motd Aggregation

The agent response contains the complete logical workload list and no
precomputed counts. Derive counts directly from that list:

```text
total = len(workloads)
online = count(workload.state == "running" &&
               (workload.health == "healthy" || workload.health == "none"))
```

Every other combination is not online. In particular, these are not online:

- `running` + `unhealthy`
- `running` + `starting`
- `running` + `unknown`
- `stopped` + any health
- `failed` + any health
- `unknown` + any health

An empty successful list means `total = 0` and `online = 0`. It is different
from an agent or collection failure and should not be rendered as a healthy
empty runtime unless the existing `go-motd` UX explicitly distinguishes the
error condition.

The list is already sorted by stable workload name. The consumer should not
depend on the order for counting, but preserving the order is useful for
diagnostics and deterministic tests.

## Error Responses

Errors use JSON with protocol version 1:

```json
{
  "protocol_version": 1,
  "error": {
    "code": "collection_timeout",
    "message": "workload status collection timed out"
  }
}
```

Stable error codes are:

| Code | Current HTTP status | Consumer meaning |
| --- | ---: | --- |
| `bad_request` | 400 | Request shape or protocol was rejected |
| `not_found` | 404 | Path is not `/v1/status` |
| `method_not_allowed` | 405 | Method is not `GET` |
| `permission_denied` | 403 | Agent could not access required status data |
| `runtime_unavailable` | 503 | Podman is unavailable |
| `collection_timeout` | 504 | Collection exceeded 750 ms |
| `response_too_large` | 500 currently | Complete snapshot exceeded 1 MiB |
| `internal_error` | 500 | Other agent-side failure |

For normal operation, the consumer should treat any non-2xx response as a
status-unavailable result, parse the JSON error when possible, and avoid
displaying the raw `message` to untrusted or broad audiences. The message is
sanitized for the consumer, but the stable `code` is the useful machine-level
classification.

Also handle failures before an HTTP response exists:

- Unix socket missing: agent is not installed, not running, or runtime
  directory is unavailable.
- Unix socket permission denied: the consumer user is not in the configured
  access group.
- Dial timeout or connection reset: treat as temporarily unavailable.
- Invalid JSON, missing required fields, or unsupported protocol version:
  treat as an integration/protocol error, not as an empty workload list.

Never use a previous successful response as current after a failed request.
The agent intentionally does not provide a stale snapshot fallback.

## Partial Inspection Behavior

If the agent can enumerate the Quadlet scope but cannot inspect one workload,
it keeps that workload in the successful response and uses `unknown` state or
health as appropriate. The consumer must not infer that an omitted workload is
stopped, healthy, or deleted.

Only a collection failure that prevents reliable scope collection produces an
error response.

## Stable Identity and Display

`workload.name` is the operator-defined Quadlet workload identity. It is
intended for display and correlation and remains stable across agent restarts,
Podman restarts, and container recreation.

Names do not intentionally contain container IDs, paths, commands,
environment values, labels, mounts, secrets, or other runtime details.

The consumer may display the name, but should not assume it is a Podman
container name or reconstruct Podman commands from it.

## Compatibility Fixtures

The agent and `go-motd` should share equivalent v1 fixtures for at least these
responses:

1. Empty successful workload list.
2. One running healthy workload.
3. One running workload with `none` health.
4. Running unhealthy workload.
5. Running starting workload.
6. Failed workload with unknown health.
7. Stopped workload.
8. Unknown workload state and health.
9. Multiple workloads in non-sorted input order, verifying deterministic
   output and count derivation.
10. Each stable error code, especially `runtime_unavailable`,
    `permission_denied`, and `collection_timeout`.
11. Additive unknown JSON fields, verifying v1 decoding remains compatible.
12. Unsupported protocol version, verifying a clear compatibility failure.

The success fixture should preserve these exact field names:

```json
{
  "protocol_version": 1,
  "observed_at": "2026-08-18T12:34:56.123456789Z",
  "workloads": [
    {"name":"alpha","state":"running","health":"healthy"},
    {"name":"beta","state":"stopped","health":"none"}
  ]
}
```

The consumer should compare semantic JSON fields rather than requiring
whitespace or object formatting to match. `observed_at` is RFC 3339 UTC with
nanosecond-capable formatting and should be parsed as a timestamp.

## Current Implementation Caveats

These items are relevant to integration testing and should not be mistaken
for consumer requirements:

- The current implementation uses the configured Quadlet directory and
  systemd/Podman command adapters, but full real-user systemd and Podman
  lifecycle integration tests are still outstanding.
- The current server configures an 8 KiB HTTP header limit and rejects invalid
  request shapes, but a dedicated front-end is still needed to guarantee the
  full request line plus headers plus body limit for every malformed framing
  case.
- The current `observed_at` value is generated after collection returns in the
  HTTP handler. The product contract intends it to represent collection time;
  the server-side implementation should be corrected before treating the
  timestamp as a precise observation boundary.
- `response_too_large` currently uses HTTP 500. Consumers should use the JSON
  error code rather than depend on that status code until the server status
  mapping is finalized.

None of these caveats change the v1 count rule or the normalized workload
field meanings.

## Recommended Consumer Test

Add an end-to-end test using a fake Unix socket server or a real temporary Unix
socket. The test should issue `GET /v1/status`, decode the response, verify
protocol version and enum values, then assert:

```text
online == 2
total == 5
```

for this workload set:

```json
[
  {"name":"a","state":"running","health":"healthy"},
  {"name":"b","state":"running","health":"none"},
  {"name":"c","state":"running","health":"starting"},
  {"name":"d","state":"stopped","health":"none"},
  {"name":"e","state":"failed","health":"unknown"}
]
```

Also test that a `collection_timeout` response and a socket dial failure do
not produce a zero-count success or reuse a prior successful snapshot.
