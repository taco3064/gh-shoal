# gh-shoal

Official source, build, release, and distribution repository for the `gh shoal`
GitHub CLI extension.

## Install

Validation releases are installed through the GitHub CLI extension path with a
pinned release tag:

```bash
gh extension install taco3064/gh-shoal --pin <validation-release-tag>
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

The extension includes `gh shoal init` for repair of a direct Personal Account
fork of the canonical Network Root. Run it from a clean local `main` synchronized
with the fork's remote `main`, while authenticated as the fork owner with `gh auth`.
It synchronizes the canonical Issue Form and Summary Workflow, preserves your
`README.md` policy, and commits and pushes only if either managed file changes.
On a fresh fork, the owner must first open the fork's Actions page and confirm
GitHub's workflow enablement prompt. `init` does not enable Actions or workflows;
its success confirms station file synchronization, not workflow execution readiness.

Product command semantics are delivered by later milestones:

- `gh shoal review`
- `gh shoal re-review`

Until those milestones are implemented, deferred product commands are treated as
unknown commands and are not shown as shipped capabilities.

## Release

Release publication is owned by this repository through
`.github/workflows/release.yml`.

Pull request validation proves release readiness without creating a real tag or
GitHub Release. It builds the full asset matrix, checks release workflow
configuration, runs native smoke checks, installs the current checkout through
GitHub CLI's local extension path, and verifies:

```bash
gh extension install .
gh shoal --help
```

Normal release publication happens only after merge. Tag the intended `main`
commit to publish precompiled GitHub CLI extension assets:

```bash
git switch main
git pull --ff-only origin main
git tag v0.1.1
git push origin v0.1.1
```

The release tag must point at the exact `main` commit intended for publication.
PR-stage tags and PR-stage GitHub Releases are not part of the normal release
lifecycle.

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

After the release workflow completes, verify the published assets, attestations,
and remote installation before closing the release-foundation issue:

```bash
gh release download <validation-release-tag> \
  --repo taco3064/gh-shoal \
  --pattern "gh-shoal-*"

for asset in gh-shoal-*; do
  gh attestation verify "$asset" --repo taco3064/gh-shoal
done

gh extension install taco3064/gh-shoal --pin <validation-release-tag>
gh shoal --help
```

The post-merge release gate verifies distribution mechanics: tag selection,
release publication, asset upload, digest, attestation, and remote asset
resolution. It is not a substitute for PR-stage runtime testing. If the remote
install resolves but `gh shoal --help` crashes or exposes behavior that the PR
exact-head local-install gate should have caught, treat that as a PR validation
escape and tighten PR CI before closing the release-foundation issue.
