# Rootless Podman Status Agent

## Summary

`motd-status-agent` runs as the same unprivileged user that owns rootless Podman Quadlet workloads and exposes their current status to authorized local consumers through a Unix socket. It translates Podman- and Quadlet-specific information into a small runtime-neutral workload contract that `go-motd` and other local tools can consume without impersonating the workload owner or receiving access to the Podman API.

## Problem

Rootless Podman workloads belong to one user's runtime and user systemd manager. System-wide MOTD processes and other users cannot reliably inspect those workloads directly, while broad access to the owner's Podman socket would grant more authority than status reporting requires.

## Goals

- Report the state and health of rootless Podman workloads managed by Quadlet.
- Run without root privileges as the user that owns those workloads.
- Give explicitly authorized local users read-only status access without exposing Podman's management API.
- Keep the external status contract independent of Podman and Quadlet terminology where possible.
- Provide a complete installation, service-lifecycle, upgrade, and removal experience.

## Non-goals

- Starting, stopping, restarting, updating, or otherwise managing containers, pods, or systemd units.
- Exposing logs, environment variables, labels, image metadata, mounted paths, secrets, or Podman API access.
- Monitoring workloads owned by other users or by root.
- Serving remote or TCP clients.
- Supporting Docker or other container runtimes in the initial release.
- Sending notifications or retaining historical workload status.

## Behavior

1. The agent runs as a user systemd service under the same unprivileged account that owns the rootless Podman workloads it reports. It never requires execution as root during normal operation.

2. Installation may require an administrator to create a dedicated local access group and establish socket-directory permissions, but it does not grant consumers membership in the workload owner's account or access to the owner's Podman socket.

3. The setup flow allows an administrator or workload owner to choose:
   - The account under which the agent runs.
   - The dedicated local group whose members may query status.
   - The absolute Unix socket path.
   - Whether the user's systemd manager should remain active while that user is logged out.

   The offered defaults are `/var/run/motd-status/agent.sock` and a dedicated `motd-status` group. Operators may select an existing group such as `media` instead.

4. The documented default setup enables linger for a dedicated non-interactive workload account so status remains available and Quadlet workloads remain managed after logout. If the operator declines linger, setup clearly states that the service and socket may disappear when the user's final session ends.

5. Installation provides a user systemd service that can be enabled and started using normal user-service management. A successful installation leaves the service enabled, running, and queryable at the configured socket.

6. Setup verifies that Podman and the user's systemd manager are available before reporting success. Missing prerequisites produce actionable errors and do not leave a falsely successful or partially enabled service.

7. The agent listens only on a local Unix socket. It does not open a TCP, UDP, or externally reachable network listener.

8. Filesystem permissions are the only client authentication mechanism. The socket is accessible to the workload-owning user and the configured access group, and inaccessible to other local users by default.

9. The socket and every parent directory needed to reach it use permissions that allow configured group members to connect without making the socket world-readable or world-writable.

10. The agent never changes permissions on the Podman API socket or recommends adding status consumers to permissions that allow direct Podman control.

11. On service start, stale socket entries left by an unclean shutdown are handled safely. The agent replaces an entry only when it can establish that the path is intended for its own socket; it refuses to overwrite a regular file, directory, symlink to an unrelated target, or another active listener.

12. On a clean stop or uninstall, the agent removes its socket entry. It does not remove a socket or filesystem entry that has been replaced by another process.

13. The initial release reports `.container` and `.pod` Quadlet workloads for the agent's user. Containers or pods started manually, `.kube` Quadlets, and other Quadlet types are not reported.

14. Each top-level `.container` or `.pod` Quadlet appears as one logical workload. Containers attached to a reported `.pod` are rolled into that pod and are not also reported as independent workloads.

15. Each reported workload has a stable, non-empty name suitable for display and machine correlation. The same Quadlet workload retains the same name across agent restarts, Podman restarts, and container recreation.

16. Workload names do not contain secrets, filesystem paths, command arguments, environment values, container IDs, or other runtime details beyond the workload's operator-defined identity.

17. The agent reports every workload in the selected Quadlet scope, including workloads that are stopped, failed, still starting, unhealthy, or temporarily unavailable. It must not report only currently running containers.

18. Each workload has one normalized runtime state:
   - `running`: the logical workload is active and its required containers are running.
   - `stopped`: the workload is known but intentionally or cleanly inactive.
   - `failed`: the workload or its user systemd unit has failed.
   - `unknown`: the agent cannot determine a reliable runtime state.

19. Each workload has one normalized health state:
   - `healthy`: all applicable required health checks currently pass.
   - `unhealthy`: at least one applicable required health check currently fails.
   - `starting`: applicable health checks have not yet reached a healthy or unhealthy result.
   - `none`: the workload has no applicable health check.
   - `unknown`: a health check is expected or may exist, but its result cannot be determined reliably.

20. For a multi-container workload, all regular non-one-shot child containers are required and determine the logical result. Any required failed or stopped child prevents the workload from being `running`, and any required unhealthy child makes the workload `unhealthy`. A one-shot helper that completed successfully does not make an otherwise healthy workload degraded; a failed or incomplete one-shot helper does.

21. A workload's runtime and health states remain separate. For example, a running workload may be `unhealthy` or `starting`, and a failed workload may have `unknown` health. The agent does not collapse these dimensions into a Podman-specific status string.

22. Values that cannot be mapped confidently use `unknown`; they are never guessed as `running`, `healthy`, or `none` merely to improve aggregate status.

23. Every successful response contains:
   - Numeric `protocol_version` equal to `1`.
   - An observation timestamp in UTC.
   - A complete list of logical workloads as observed together.
   - Each workload's stable name, normalized runtime state, and normalized health state.

24. The observation timestamp represents when the underlying workload data was collected, not when the response finished writing. It is generated by the agent and is not accepted from a client.

25. Workload results in a response are ordered deterministically by stable name. Enumeration order from systemd or Podman never affects response order.

26. An account with no matching Quadlet workloads receives a successful response with the current observation timestamp and an empty workload list. This is distinct from an agent or discovery failure.

27. Each client request receives a newly observed snapshot. The agent does not serve a previous successful snapshot as though it were current after collection fails.

28. Multiple authorized clients may query the agent concurrently. Each receives a complete response, and one slow or disconnected client does not indefinitely block other clients.

29. Client requests are read-only and require no request fields that can select another user, execute a command, identify an arbitrary Podman socket, alter discovery scope, or mutate workload state.

30. The agent serves HTTP/1.1 over the Unix socket and exposes `GET /v1/status`. Requests larger than 8 KiB, malformed or incomplete requests, unsupported methods or paths, and unsupported protocol use are rejected without terminating the service or affecting workloads. Repeated invalid requests from an authorized local client do not cause unbounded resource use.

31. If workload collection fails entirely, the agent returns an explicit error response rather than a successful empty workload list or stale data. The error distinguishes broad categories useful to a local consumer, such as runtime unavailable, permission denied, collection timeout, or internal failure, without leaking sensitive command output.

32. If only one workload cannot be inspected while the remaining scope can be enumerated reliably, the response remains successful and includes that workload with `unknown` state or health as appropriate. It does not silently omit the affected workload.

33. Workload collection is limited to 750 milliseconds, and every encoded response is limited to 1 MiB. A wedged Podman runtime or systemd manager cannot leave client connections open indefinitely, and a timeout yields an explicit collection error rather than partial data presented as complete.

34. The service emits operational diagnostics to the user systemd journal. Logs identify startup, shutdown, socket configuration errors, collection failures, protocol errors, and client disconnects without logging complete successful payloads or sensitive Podman output by default.

35. The service does not require persistent status storage. Restarting it loses no required history because every successful request represents a current snapshot.

36. If Podman or the user systemd manager restarts, the agent either resumes reporting current workloads automatically or returns an explicit temporary error until collection succeeds. It never continues serving the pre-restart snapshot as current.

37. Upgrades preserve the configured workload-owning user, access group, socket path, and linger choice. An upgrade restarts the service when required and verifies that the installed protocol remains compatible with the documented `go-motd` consumer version.

38. A failed upgrade leaves either the previously working version active or the service stopped with an actionable error. It does not silently leave a new binary running with an incompatible service definition or protocol.

39. Uninstall disables and stops the user service, removes agent-owned service files and socket entries, and explains whether administrator-created group membership or linger remains configured. It does not remove Podman, Quadlet files, containers, images, volumes, or unrelated user services.

40. Uninstall does not automatically delete a shared access group, remove users from that group, or disable linger unless the operator explicitly requests those system-level changes and is shown their effects.

41. The external contract is runtime-neutral: protocol fields describe workloads, runtime state, and health rather than exposing Podman commands, systemd unit internals, or Quadlet file formats. Podman- and Quadlet-specific terminology remains appropriate in installation, discovery, and diagnostics intended for the operator.

42. The initial protocol is compatible with the corresponding `go-motd` rootless container status feature: the consumer can derive online and total counts solely from the complete workload list, treating only `running` workloads with `healthy` or `none` health as online.

43. Protocol version 1 may add optional JSON fields without changing `protocol_version`. Existing fields, their meanings, and normalized enum values do not change within version 1; incompatible changes require a new protocol version.
