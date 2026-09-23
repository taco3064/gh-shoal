# gh-shoal

Official source, build, release, and distribution repository for the `gh shoal`
GitHub CLI extension.

## Install

Prerelease validation artifacts are installed through the GitHub CLI extension
path with a pinned release tag:

```bash
gh extension install taco3064/gh-shoal --pin <prerelease-tag>
```

## Build

Build the extension executable from a clean checkout:

```bash
go build -o gh-shoal ./cmd/gh-shoal
```

The installed extension is invoked by GitHub CLI as:

```bash
gh shoal --help
```

## Current CLI surface

This foundation release intentionally ships only the root `gh shoal` namespace,
help, version output, and command-routing foundation.

Product command semantics are delivered by later milestones:

- `gh shoal init`
- `gh shoal review`
- `gh shoal re-review`

Until those milestones are implemented, deferred product commands are treated as
unknown commands and are not shown as shipped capabilities.

## Release

Release publication is owned by this repository through
`.github/workflows/release.yml`.

Push a version tag to publish precompiled GitHub CLI extension assets:

```bash
git tag v0.1.0-rc.1
git push origin v0.1.0-rc.1
```

Tags containing a hyphen, such as `v0.1.0-rc.1`, are published as GitHub
prereleases by `cli/gh-extension-precompile`.

The release workflow builds this initial matrix:

- Linux amd64
- Linux arm64
- macOS / Darwin amd64
- macOS / Darwin arm64
- Windows amd64
- Windows arm64

Artifact Attestations are generated for the published executables so users can
verify the relationship between the `gh-shoal` repository, release workflow,
source commit, and released binary.
