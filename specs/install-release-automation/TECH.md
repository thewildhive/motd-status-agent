# Installation and Release Automation Technical Design

## Context

The product requirements are defined in [PRODUCT.md](PRODUCT.md). The existing rootless status spec promises lifecycle documentation and project automation, but does not fully define their deliverables and assumes a user-installed unit. This focused design supersedes that installation/lifecycle design with the validated system-unit model while retaining the original runtime and protocol design. The current repository has no commits, so stable commit-pinned source links are not yet possible.

Relevant current files are:

- [`cmd/motd-status-agent/main.go`](../../cmd/motd-status-agent/main.go) contains `serve`, `check`, `install`, and `uninstall`. Its current installer writes a user unit and controls a user manager, which conflicts with this feature’s required system-unit deployment model and must be changed during implementation, not during this spec-only change.
- [`internal/server/server.go`](../../internal/server/server.go) owns Unix-socket creation and permissions.
- [`contrib/motd-status-agent.tmpfiles`](../../contrib/motd-status-agent.tmpfiles) is the starting point for boot-persistent `/run/motd-status` provisioning.
- [`README.md`](../../README.md) contains abbreviated setup instructions that should become a pointer to the authoritative installation guide.
- [`testdata/v1/status.json`](../../testdata/v1/status.json) and the fixture server provide the agent side of the existing cross-repository protocol test.
- The sibling `go-motd` repository’s `docs/INSTALL.md`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.github/workflows/publish-release.yml`, release scripts, Release Please configuration, and `scripts/test-status-agent-integration.sh` are the reference patterns. Copy their guarantees, not their unsupported OS matrix or project-specific binary names.

## Proposed Changes

### Installation and Service Model

- Add top-level `INSTALL.md` with release install, source install, prerequisites, administrator provisioning, agent configuration, system service setup, `go-motd` configuration, verification, troubleshooting, upgrade, rollback, and uninstall sections. Keep commands parameterized with clearly introduced values such as `WORKLOAD_USER`, `WORKLOAD_UID`, `ACCESS_GROUP`, and `SOCKET_PATH`; also include one coherent default example.
- Update `README.md` to retain a short overview and link to `INSTALL.md`. Remove or correct commands that install the obsolete user-service model.
- Convert lifecycle installation to a root-owned system unit at `/etc/systemd/system/motd-status-agent.service`. The generated unit must use the selected workload user/access group and the owner’s rootless manager environment, and order against `user@<uid>.service`.
- Use system `systemctl daemon-reload`, `enable --now`, `disable --now`, and status operations. Remove the `systemctl --machine=<user>@.host --user` path. Runtime collection still invokes `systemctl --user` from the service process, where `XDG_RUNTIME_DIR` and `DBUS_SESSION_BUS_ADDRESS` point at the workload owner’s manager.
- Keep durable agent configuration in the workload owner’s existing config location unless implementation discovers a concrete system-unit readability or ownership issue. The system unit passes its absolute path to `serve`.
- Treat `/run/motd-status` as volatile. Install or document `/etc/tmpfiles.d/motd-status.conf` with owner `WORKLOAD_USER`, group `ACCESS_GROUP`, and mode `02750`; keep socket mode `0660`. Use `/run` in the tmpfiles rule even though the public default is spelled `/var/run`.
- Make `install` and `uninstall` behavior match the guide. If one command cannot safely own both privileged system files and unprivileged runtime validation, split the documented procedure into explicit administrator and verification phases rather than reintroducing remote user-manager orchestration.
- Add a version command or flag and link-time version variable before release publication. Preserve existing subcommand exit behavior.

### `go-motd` Compatibility

- Put the exact `system.container_status` JSON snippet in `INSTALL.md`, with `/var/run/motd-status/agent.sock` and `max_age` aligned with the current consumer schema.
- Add an agent-side integration entry point that checks out `go-motd` at a repository-pinned tag or full commit SHA into a temporary directory and runs its existing fixture-backed integration harness against the current agent checkout. Do not rely on `../go-motd` in CI.
- Store the pinned consumer revision in one obvious reviewed location, such as a script constant or text file. Verify the fetched commit exactly before testing.
- Keep `testdata/v1/status.json` canonical. Any fixture change must pass both agent protocol tests and the pinned consumer test.

### Local Build Interface

- Add a small `Makefile` or equivalently documented scripts with authoritative targets for formatting/module checks, vet, unit/race tests, vulnerability analysis, workflow validation, Linux cross-builds, compatibility integration, and release packaging.
- Ensure local targets and GitHub Actions invoke the same scripts or commands so CI does not define an unrepeatable second build process.
- Build release commands from `./cmd/motd-status-agent`, with `CGO_ENABLED=0`, `GOOS=linux`, and the selected `GOARCH`, using the patched Go `1.25.13` baseline or newer. Use `-buildvcs=false` and release linker flags for version metadata and stripped output.

### CI Workflow

- Add `.github/workflows/ci.yml`, patterned after `go-motd`:
  - `quality`: formatting, tidy diff, module verification, vet, actionlint, shell syntax, Conventional PR title, and offline internal link checking.
  - `tests`: race-enabled tests with atomic coverage and a step summary.
  - `builds`: Linux amd64/arm64 matrix using the same release-oriented build command.
  - `vulnerabilities`: binary-mode `govulncheck` with a pinned tool version.
  - `compatibility`: deterministic checkout and fixture-backed `go-motd` integration.
  - `dependency-review`: PR-only moderate-severity gate.
  - `required`: aggregate result for branch protection.
- Pin all actions by full commit SHA and retain version comments. Use top-level `contents: read`; grant no write permissions in CI.
- Add concurrency cancellation keyed by pull request number or Git ref and bounded job timeouts.

### Release Workflows

- Add `release-please-config.json`, `.release-please-manifest.json`, and `.github/workflows/release.yml`. Configure a single Go package, stable tags without component names, and Conventional Commit changelog sections equivalent to `go-motd`.
- Initial version selection must be decided before implementation merges. Since no agent release exists, default to `0.1.0` unless maintainers explicitly declare the protocol-complete first release as `1.0.0`.
- Restrict Release Please to the canonical repository and create its token with `RELEASE_APP_CLIENT_ID` and `RELEASE_APP_PRIVATE_KEY`, following the sibling repository.
- Add `.github/workflows/publish-release.yml` triggered by published releases and manual tag input. Validate stable SemVer, verify the release/tag and ancestry from `main`, then build from the immutable tag.
- Add release scripts under `.github/scripts/` for packaging and idempotent upload. Adapt `go-motd`’s scripts to exactly two raw binaries. Produce deterministic asset names, `checksums.txt`, and `checksums.txt.sig` using an Ed25519 private key supplied through `SIGNING_PRIVATE_KEY`/a temporary `SIGNING_KEY_FILE`.
- Upload with release-scoped `contents: write` only in the publish job. Existing remote assets are downloaded or hashed before deciding whether to skip or fail; never use overwrite semantics.
- Add maintainer documentation for the GitHub App variables/secrets, signing public-key distribution, release recovery, and key rotation. The install guide must identify how operators obtain the trusted public key before claiming signature verification.

## Testing and Validation

- Documentation command review executes shell snippets in disposable Linux environments where practical and uses `shellcheck` or `bash -n` for extracted scripts. A manual clean-host test covers release/source install, repeated install, boot recreation of `/run/motd-status`, upgrade, rollback, and uninstall (Behavior 1-14, 18-22).
- A disposable systemd-capable Linux VM test creates separate workload-owner and consumer users, starts a lingering owner manager, installs the system unit, and proves service UID/GID, manager access, socket modes, authorized access, unauthorized denial, and no Podman-socket permission changes (Behavior 2-14, 19, 22).
- The same environment installs or builds compatible `go-motd`, applies the documented JSON, and verifies terminal, JSON, unavailable-agent, stale-response, empty-list, `healthy`, `none`, and `unknown` behavior (Behavior 15-17).
- Unit tests cover system-unit rendering, UID-derived environment values, quoting/rejection of unsafe paths, tmpfiles rendering, idempotent file replacement, and uninstall scope. Command fakes assert only system-level service control is used (Behavior 9-13, 20-22).
- CI is validated with actionlint and shell syntax checks. Pull-request runs demonstrate every job and the aggregate `Required` result; a fork PR demonstrates that no privileged release step runs (Behavior 23-32, 41).
- Compatibility CI checks out the exact configured `go-motd` revision, runs the real fixture boundary, and intentionally fails for a bad revision and an incompatible fixture (Behavior 30-32).
- Release scripts are tested locally with an ephemeral Ed25519 key. Tests verify both architectures, static linkage, executable version output, checksum verification, signature verification, stable filenames, no secret output, idempotent identical-asset recovery, and rejection of differing existing assets (Behavior 33-40).
- A release dry run against a temporary repository verifies Release Please PR creation, immutable-tag checkout, ancestry validation, least-privilege workflow permissions, manual recovery, and fork isolation before enabling canonical publication (Behavior 33-41).

## Risks and Mitigations

- A system service talking to a user manager can fail when linger or `/run/user/<uid>/bus` is absent. Make these explicit prerequisites, order against `user@<uid>.service`, and verify the endpoint after activation.
- Shell commands in installation documentation can become unsafe when operators substitute unusual names or paths. Prefer installer flags and validated environment assignments; clearly constrain examples where systemd or tmpfiles escaping would otherwise be ambiguous.
- Copying `go-motd` release automation wholesale could publish unsupported targets or stale project names. Keep agent artifact lists explicit and test exact filenames.
- Pinning `go-motd` prevents surprise breakage but can hide compatibility drift. Update the pin through reviewed dependency-style changes and periodically test its latest stable release separately before advancing the required pin.
- Signed checksums are useful only if operators have an independently trusted public key. Maintainer and install documentation must define that trust path before describing releases as signature-verified.

## Parallelization

After the product and technical specs are approved, two local agents can work in parallel, followed by one integration pass:

- `agent-install`: owns lifecycle conversion, system unit/tmpfiles behavior, lifecycle tests, `INSTALL.md`, and `README.md`. Local worktree `/home/calmcacil/worktrees/motd-status-agent-install-release`, branch `feat/system-install-docs`.
- `agent-automation`: owns the Makefile/scripts, CI, Release Please configuration, publishing workflow, release scripts, and automation tests. Local worktree `/home/calmcacil/worktrees/motd-status-agent-automation`, branch `feat/github-automation`.
- The compatibility integration script is coordinated at merge time because it touches automation while depending on the documented consumer contract. Merge both branches into `feat/install-release-automation`, run all local and VM gates, and submit one combined PR so documentation never advertises unpublished artifacts or an unavailable service model.

The agents must not both edit `README.md`; `agent-install` owns it. `agent-automation` communicates final artifact names, version command syntax, trusted-key location, and required repository settings before the integration pass finalizes `INSTALL.md`.
