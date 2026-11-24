Land all commits and releases through pull requests on protected `main`; never push directly to `main`, create release tags manually, or bypass required checks.

- Keep the agent Linux-only and standard-library-only. Preserve the v1 Unix-socket contract with `go-motd`.
- Do not add or run test suites. Verify changes using the real CLI, socket, or packaging path and record the observed output.
- Use Conventional Commit PR titles. See [MAINTAINERS.md](MAINTAINERS.md) for the release procedure and [INSTALL.md](INSTALL.md) for operator guidance.
