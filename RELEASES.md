# Releasing Pantalk

This document describes how to build, version, and release pantalk binaries.

## Overview

Releases are driven by the **`VERSION` file**. Bumping it on `main` starts an
automated pipeline that tags the exact commit tested by CI, publishes
multi-platform binaries as a GitHub Release, and publishes a matching
multi-platform container image:

1. Edit `VERSION` and add its matching section to `CHANGELOG.md`.
2. Merge the change to `main`.
3. CI runs the race-enabled tests and the static release-mode tests.
4. After CI succeeds, `tag-release.yaml` creates an annotated `v*` tag and
   dispatches `release.yaml` at that tag.
5. The release workflow validates, rebuilds, packages, checksums, and publishes
   the binaries and container image with notes from `CHANGELOG.md`.

Existing tags and releases are not changed by this process.

## Version embedding

Every binary embeds the release tag via Go linker flags. The variable lives in
`internal/version/version.go` and defaults to `"dev"` when no flag is set.
Update checks are skipped for those development builds.

### Target Platforms

| OS      | Architecture |
| ------- | ------------ |
| Linux   | amd64, arm64 |
| macOS   | amd64, arm64 |
| Windows | amd64        |

Container images are published for Linux amd64 and arm64 at
`ghcr.io/pantalk/pantalk`. Stable releases update `latest`; prereleases publish
only versioned image tags.

After the first container publication, an organization owner must make the
GitHub Container Registry package public so it can be pulled without
authentication.

## Local builds

A `Makefile` is provided for building locally:

```bash
# Build all binaries (version auto-detected from git tags)
make

# Build with an explicit version
make VERSION=vX.Y.Z

# Build the container image with an explicit version
docker build \
  --build-arg VERSION=vX.Y.Z \
  --tag pantalk/pantalk:local \
  .

# Cross-compile for a specific platform
make cross GOOS=darwin GOARCH=arm64

# Run tests
make test

# Exercise the embedded local connector and Codex startup. Enter /quit after
# the prompt appears. This uses disposable Pantalk state.
./pantalk local --workdir . --ephemeral

# Exercise the installed Claude Code integration with the same local connector.
./pantalk local --driver claude --workdir . --ephemeral

# Clean build artifacts
make clean
```

The Codex smoke test requires an installed and authenticated Codex CLI; check
it with `codex login status`. The Claude smoke test similarly requires
`claude auth status`. Automated release tests use fake protocol processes and
do not require either credential or make model API calls.

## Update notifications

Release binaries automatically check for newer versions by querying the GitHub
Releases API. This happens:

- On `pantalk version` / `pantalkd --version`
- After a successful `pantalk` command (printed to stderr)
- At `pantalkd` startup (logged)

The check is **skipped entirely** when the version is `"dev"` (i.e. when
running via `go run` or `go install` without ldflags), so it only applies to
distributed binaries.

## Versioning guidelines

- Follow [Semantic Versioning](https://semver.org/).
- Use `v` prefix on tags (`v1.0.0`, not `1.0.0`).
- Pre-release versions: `v0.1.0-beta.1`.
- Keep `VERSION` bare, without the `v` prefix.
- Add a matching `CHANGELOG.md` section before merging a version bump.
- Breaking protocol changes between client and server warrant a major bump.
