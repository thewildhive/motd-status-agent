# Installation and Release Automation

## Summary

`motd-status-agent` provides a complete `INSTALL.md` for installing the agent as a system-managed service that runs as the rootless Podman workload owner, sharing status safely with `go-motd` through a local Unix socket. The repository also provides GitHub Actions that continuously validate changes and publish verifiable Linux release binaries through a reviewable, recoverable release process.

For installation and service lifecycle behavior, this focused specification supersedes the user-service assumptions in `specs/rootless-podman-status/PRODUCT.md`; the agent's runtime and protocol behavior remain governed by that original specification.

## Goals

- Give an operator a complete path from a release artifact or source checkout to a working agent and `go-motd` container summary.
- Preserve rootless Podman isolation while allowing explicitly authorized local users to read status.
- Make pull requests and releases reproducible, reviewable, and resistant to accidental artifact replacement.
- Follow the mature `go-motd` automation model where it applies to this Linux-only service.

## Non-goals

- Installing or configuring Podman, Quadlet workloads, or `go-motd` media integrations unrelated to container status.
- Supporting macOS, Windows, Docker, TCP access, or remote consumers.
- Automatically changing the Podman API socket or granting consumers Podman control.
- Publishing distribution-native packages in the initial release.
- Adding an agent self-update command.

## Behavior

### Installation Guide

1. The repository contains a top-level `INSTALL.md` that is the authoritative operator guide for installation, upgrade, verification, troubleshooting, rollback, and uninstall. `README.md` links to it rather than duplicating the full procedure.

2. The guide identifies the supported deployment model before presenting commands:
   - Linux on amd64 or arm64.
   - Rootless Podman workloads generated from `.container` or `.pod` Quadlets.
   - An existing unprivileged workload-owning account.
   - An active user systemd manager for that account, normally kept available through linger.
   - A system-level `motd-status-agent.service` that explicitly runs as the workload owner, not a user service installed through another account.

3. The guide explains the security boundary: the agent may inspect only the workload owner’s rootless runtime; `go-motd` receives normalized read-only status; consumers are never granted the Podman API socket or membership solely intended to control Podman.

4. The release installation path provides architecture-specific commands that download the latest Linux binary and checksum manifest from the project’s GitHub Release, verify the selected binary against the manifest, and install it at a stable system path. Download or checksum failure stops installation before replacing a working binary.

5. A source installation path states the required Go version, currently Go `1.25.13` or newer, builds `./cmd/motd-status-agent`, and installs the resulting binary at the same stable path used by release installation. Source installation does not imply that uncommitted or unverified builds are equivalent to signed release artifacts.

6. Before service installation, the guide tells the operator how to determine and record:
   - The workload owner’s username, UID, primary group, home directory, and user runtime directory.
   - The group authorized to query status.
   - The intended socket path, defaulting to `/var/run/motd-status/agent.sock` (equivalent to `/run/motd-status/agent.sock` on normal Linux systems).
   - Whether each intended `go-motd` user is already a member of the access group.

7. The default guide uses a dedicated `motd-status` access group. It also documents using an existing shared group, such as `media`, when that is an intentional local policy. It never assumes the workload owner’s primary group is appropriate for every installation.

8. The guide provides administrator commands to create or validate the access group, add each intended consumer to it, and explain that existing login sessions may need to be restarted before new supplementary group membership takes effect.

9. The runtime directory is recreated across boots with a documented systemd-tmpfiles rule. It is owned by the workload owner and configured access group, permits owner and group traversal, and denies access to other users. The socket is owner/group readable and writable and inaccessible to other users.

10. The documented system service:
    - Uses `User=` for the workload owner and `Group=` for the configured access group.
    - Sets `HOME` to the workload owner’s home.
    - Sets `XDG_RUNTIME_DIR=/run/user/<uid>`.
    - Sets `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/<uid>/bus`.
    - Requires and starts after `user@<uid>.service`.
    - Starts the installed agent with its durable configuration.
    - Restarts after unexpected failure without running the serving process as root.
    - Is enabled and controlled with system-level `systemctl` commands.

11. Installation does not depend on `sudo su - <user>`, copied shell-session variables, `systemctl --machine=... --user`, or remotely invoking `systemctl --user`. An operator with administrator access can install and manage the system unit without opening an interactive session as the workload owner.

12. The guide explains linger as a prerequisite for keeping the workload owner’s user manager and Quadlets available without a login session. It shows how to inspect linger and how to enable it when needed, but does not instruct an operator to enable it again when it is already active.

13. The installation procedure is safe to repeat for the same user, group, paths, and binary version. Re-running it repairs expected managed files and restarts or reloads the service as needed without modifying Quadlets or Podman data.

14. A successful installation is not declared until all of the following are verified:
    - The workload owner’s user manager is active.
    - The system service is enabled and active with the expected effective user and group.
    - The runtime directory and socket have the intended owner, group, and modes.
    - `GET /v1/status` succeeds over the Unix socket as the workload owner.
    - The same request succeeds as an intended `go-motd` consumer without root privileges.
    - The response reports protocol version `1` and a workload list, which may legitimately be empty.

15. The `go-motd` integration section provides the exact JSON configuration under `system.container_status`, using the same socket path as the agent and a documented `max_age` value. It identifies the supported `go-motd` version or release range for protocol version `1`.

16. The guide verifies integration in both consumer surfaces: normal `go-motd` output displays the aggregate `Containers` status, while `go-motd --json` exposes the compatible structured status. It explains that an unavailable, inaccessible, stale, or incompatible agent is omitted by `go-motd` rather than shown as zero healthy workloads.

17. The guide distinguishes agent health from workload health. Containers with applicable passing checks report `healthy`; workloads without an applicable health check report `none`; inability to determine an expected result reports `unknown`. Operators are not told that `none` means unhealthy.

18. Troubleshooting is organized by observable failures and gives non-destructive diagnostics for at least: missing user manager or D-Bus, inactive linger, missing Podman, wrong binary architecture, service startup failure, absent socket parent after reboot, permission denied for a consumer, connection refused or stale socket, empty workload list, `unknown` workload state, and unexpected `health: none`.

19. Troubleshooting commands do not print secrets, broaden the Podman socket, make the status socket world-accessible, or recommend running the serving process as root.

20. Upgrade instructions download and verify the replacement before stopping or replacing the working executable, preserve the configured workload owner, group, socket, and `go-motd` configuration, restart the service, and repeat endpoint and consumer verification.

21. Rollback instructions select an explicit prior immutable release tag, verify its artifact, replace only the agent binary, restart the service, and verify protocol compatibility. Operators are told not to move a release tag or overwrite published assets.

22. Uninstall instructions stop and disable the system service and remove only agent-owned unit, configuration, binary, and socket artifacts. They preserve Quadlets, containers, images, volumes, Podman configuration, shared groups, group membership, linger, and `go-motd` configuration unless the operator explicitly chooses to remove those separately.

### Continuous Integration

23. GitHub Actions CI runs for pull requests targeting `main`, pushes to `main`, merge queues, and manual dispatch. Concurrent superseded CI runs for the same branch or pull request are cancelled.

24. CI uses read-only repository contents permission by default and pins third-party actions to immutable commit SHAs with a readable version comment.

25. A required quality gate rejects unformatted Go, module files changed by `go mod tidy`, failed module verification, `go vet` findings, invalid workflow syntax, invalid release shell scripts, broken internal Markdown links, and non-Conventional pull request titles.

26. A required test gate runs the complete Go test suite with the race detector and publishes a human-readable coverage summary. Tests do not require access to production Podman workloads or a persistent host socket.

27. A required build matrix compiles every shipped target: Linux amd64 and Linux arm64. Unsupported operating systems are not presented as release targets merely because Go can cross-compile them.

28. A required vulnerability gate analyzes the built agent with a pinned `govulncheck` version. Pull requests also receive dependency review for newly introduced dependencies of moderate or greater known severity when GitHub supports the check.

29. A single stable required-check job fails when any applicable CI gate fails and treats intentionally skipped event-specific gates as acceptable. Branch protection can therefore require one consistently named result.

30. The agent-owned protocol fixture and server tests run in CI. Cross-repository `go-motd` compatibility is also gated using an explicit, reproducible `go-motd` revision rather than whatever happens to be present in a sibling checkout.

31. Cross-repository testing exercises the actual Unix-socket HTTP boundary and verifies protocol decoding, aggregate online counts, JSON workload details, aggregate-only terminal output, and omission when the agent becomes unavailable. It does not require root, systemd, Podman, or a permanent socket.

32. A failure to fetch the pinned `go-motd` compatibility revision is a failed compatibility check, not a silent skip. Updating that revision is a reviewable repository change.

### Releases

33. Releases follow Semantic Versioning and are proposed from Conventional Commits by Release Please. Automation opens or updates a reviewable release pull request; it does not write release changes directly to `main`.

34. Release automation runs only in the canonical repository and uses a short-lived GitHub App token for the release pull request. Repository variables and secrets required for that flow are named in maintainer documentation.

35. Merging a release pull request creates an immutable `vMAJOR.MINOR.PATCH` tag and GitHub Release. A separate publishing workflow checks out that exact tag and verifies that it belongs to `main` before building assets.

36. Each stable release publishes raw, statically linked Linux binaries named `motd-status-agent-linux-amd64` and `motd-status-agent-linux-arm64`, plus `checksums.txt` and a detached signature for the checksum manifest. Artifact names remain stable within a major version so installation automation can select them reliably.

37. Release binaries expose the release version through the command’s documented version output. The displayed version matches the immutable release tag from which the artifact was built.

38. Release publishing builds artifacts from source at the tag, generates SHA-256 checksums, signs the checksum manifest with the repository’s configured signing key, and verifies the resulting files before upload. The private signing key is never written to the repository or logs.

39. The publish workflow can be manually dispatched for an existing stable tag to recover from an interrupted publication. Recovery uploads missing assets, accepts byte-identical existing assets, and refuses to replace assets whose bytes differ.

40. Published tags and assets are never mutated to repair a bad release. A corrected build is published as a new patch release, and operators may explicitly reinstall a prior verified release to roll back.

41. Forks and untrusted pull requests can run CI without release credentials but cannot create canonical releases, mint the canonical release token, sign artifacts, or upload release assets.
