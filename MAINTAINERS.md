# Maintainer Operations

This document describes the GitHub configuration required by the release workflows. It is not needed by operators installing the agent.

## Repository

The canonical repository is `thewildhive/motd-status-agent`. The release workflows are guarded so forks and pull requests cannot publish canonical assets.

Protected `main` accepts squash merges through pull requests after `Required` and `CodeQL` pass. Do not push directly to `main` or bypass the rules.

## Release Please GitHub App

Create or reuse the project GitHub App with permission to create and update pull requests, contents, and releases as required by Release Please. Store:

- Repository variable `RELEASE_APP_CLIENT_ID`: the App client ID.
- Repository secret `RELEASE_APP_PRIVATE_KEY`: the App private key in PEM format.

The Release workflow uses a short-lived installation token and runs only when `github.repository` is the canonical repository.

## Signing Key

Generate an Ed25519 signing key outside the repository and protect the private key:

```sh
openssl genpkey -algorithm Ed25519 -out checksums-signing-key.pem
openssl pkey -in checksums-signing-key.pem -pubout -out checksums-signing-key.pub.pem
chmod 600 checksums-signing-key.pem
```

Store the private key as the repository secret `SIGNING_PRIVATE_KEY`. Never commit it, place it in an artifact, or print it in a workflow log.

Distribute the public key through an independently trusted project-maintainer channel and record its fingerprint in release documentation:

```sh
openssl pkey -pubin -in checksums-signing-key.pub.pem -outform DER \
  | sha256sum
```

Operators must obtain and verify that public key before treating `checksums.txt.sig` as an authenticity check. Rotate keys by publishing the new public-key fingerprint first, then changing `SIGNING_PRIVATE_KEY` in a coordinated release-maintenance change.

## Release Flow

1. Squash-merge a Conventional Commit pull request into `main` after required checks pass.
2. Release Please opens or updates the release pull request.
3. Review and merge the release pull request to create the immutable `vMAJOR.MINOR.PATCH` tag and GitHub Release.
4. Confirm the publish workflow checks out the tag, builds Linux amd64 and arm64 binaries, signs `checksums.txt`, and uploads all four assets.
5. Verify the published checksums and signature from a clean environment.

If publication is interrupted, manually dispatch `Publish Release` with the existing stable tag. The upload script accepts identical existing assets, uploads missing assets, and refuses differing assets. Never delete or overwrite a published asset to repair a release; publish a new patch release instead.
