# Releasing Pantalk

This document describes how to build, version, and release pantalk binaries.

## Overview

Pantalk uses **Git tags** to trigger automated releases. When a tag matching
`v*` is pushed to the `pantalk/pantalk` repository, a GitHub Actions workflow
builds multi-platform binaries and publishes them as a GitHub Release.

## Version Embedding

Every binary embeds a version string at build time via Go linker flags. The
version variable lives in `internal/version/version.go` and defaults to `"dev"`
when no flag is set (e.g. when running with `go run`).

When the version is `"dev"`, update checks are skipped entirely.

## How to Release

### 1. Ensure the repository is up to date

Make sure all changes have been pushed to the `pantalk/pantalk` repository on
GitHub before tagging a release.

### 2. Tag the release

Create and push a semver tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Use semantic versioning: `vMAJOR.MINOR.PATCH`.

### 3. Wait for CI

The [release workflow](.github/workflows/release.yaml) runs automatically and:

1. Builds all binaries (`pantalk`, `pantalkd`) for each platform.
2. Packages them into `.tar.gz` archives.
3. Generates SHA-256 checksums.
4. Creates a GitHub Release with auto-generated release notes.

### Target Platforms

| OS      | Architecture |
| ------- | ------------ |
| Linux   | amd64, arm64 |
| macOS   | amd64, arm64 |
| Windows | amd64        |

## Local Builds

A `Makefile` is provided for building locally:

```bash
# Build all binaries (version auto-detected from git tags)
make

# Build with an explicit version
make VERSION=v0.1.0

# Cross-compile for a specific platform
make cross GOOS=darwin GOARCH=arm64

# Run tests
make test

# Clean build artifacts
make clean
```

## Update Notifications

Release binaries automatically check for newer versions by querying the GitHub
Releases API. This happens:

- On `pantalk version` / `pantalkd --version`
- After a successful `pantalk` command (printed to stderr)
- At `pantalkd` startup (logged)

The check is **skipped entirely** when the version is `"dev"` (i.e. when
running via `go run` or `go install` without ldflags), so it only applies to
distributed binaries.

## Versioning Guidelines

- Follow [Semantic Versioning](https://semver.org/).
- Use `v` prefix on tags (`v1.0.0`, not `1.0.0`).
- Pre-release versions: `v0.1.0-beta.1`.
- Breaking protocol changes between client and server warrant a major bump.
