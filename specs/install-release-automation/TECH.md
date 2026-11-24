# Installation and Release Automation Technical Design

## Context

[PRODUCT.md](PRODUCT.md) defines installation and release behavior. The agent uses a system unit that runs as the unprivileged workload owner.

Relevant current files are:

- [`cmd/motd-status-agent/main.go`](../../cmd/motd-status-agent/main.go) implements the CLI and manages the system unit.
- [`internal/server/server.go`](../../internal/server/server.go) owns Unix-socket creation and permissions.
- [`contrib/motd-status-agent.tmpfiles`](../../contrib/motd-status-agent.tmpfiles) defines runtime directory provisioning.
- [`INSTALL.md`](../../INSTALL.md) is the operator guide.

## Implementation

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

### Local Build Interface

- `make check` runs formatting, module consistency, vet, workflow validation, Linux builds, and vulnerability analysis. `make package` creates signed release assets.
- Ensure local targets and GitHub Actions invoke the same scripts or commands so CI does not define an unrepeatable second build process.
- Build release commands from `./cmd/motd-status-agent`, with `CGO_ENABLED=0`, `GOOS=linux`, and the selected `GOARCH`, using the patched Go `1.27.4` baseline or newer. Use `-buildvcs=false` and release linker flags for version metadata and stripped output.

### CI Workflow

- Add `.github/workflows/ci.yml`, patterned after `go-motd`:
  - `quality`: formatting, tidy diff, module verification, vet, actionlint, shell syntax, Conventional PR title, and offline internal link checking.
  - `builds`: Linux amd64/arm64 matrix using the same release-oriented build command.
  - `vulnerabilities`: binary-mode `govulncheck` with a pinned tool version.
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

## Verification

Exercise the real CLI and Unix-socket endpoint in a disposable environment. Verify packaged binaries, checksums, and the signature before declaring a release ready.

## Risks and Mitigations

- A system service talking to a user manager can fail when linger or `/run/user/<uid>/bus` is absent. Make these explicit prerequisites, order against `user@<uid>.service`, and verify the endpoint after activation.
- Shell commands in installation documentation can become unsafe when operators substitute unusual names or paths. Prefer installer flags and validated environment assignments; clearly constrain examples where systemd or tmpfiles escaping would otherwise be ambiguous.
- Keep the artifact list explicit so unsupported targets are not published.
- Signed checksums are useful only if operators have an independently trusted public key. Maintainer and install documentation must define that trust path before describing releases as signature-verified.
