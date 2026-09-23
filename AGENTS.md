# Repository Instructions

This repository owns the source, build, release, and GitHub CLI distribution
surface for the `gh shoal` extension.

## Release Lifecycle Verification

Do not create Git tags or GitHub Releases during pull request validation.
Pull requests must prove release readiness without publishing distribution
artifacts.

Keep these validation signals separate:

- Exact PR head verification checks the accepted candidate commit itself.
- Synthetic PR merge-ref verification checks integration with the current base
  branch.

Jobs that claim release-readiness or local-extension runtime evidence must check
out the exact PR head commit on `pull_request` runs:

```yaml
with:
  ref: ${{ github.event.pull_request.head.sha || github.sha }}
```

Those jobs must assert the checked-out commit before building or installing:

```bash
expected="${{ github.event.pull_request.head.sha || github.sha }}"
actual="$(git rev-parse HEAD)"
test "$actual" = "$expected"
```

Exact-head runtime verification must install the checkout through GitHub CLI's
local extension path and execute the installed extension:

```bash
go build -o gh-shoal ./cmd/gh-shoal
gh extension install .
gh shoal --help
```

Future runtime checks for shipped commands must use the same local-install path
with controlled fixtures or mocked GitHub boundaries. Do not replace local
extension execution with only direct binary execution when validating extension
runtime behavior.

Release-readiness checks may build the full supported asset matrix and verify
release workflow configuration, permissions, asset names, and attestation
configuration. They must not publish a real release.

After merge, real distribution verification is main-only:

1. confirm CI is green for the exact intended `main` commit;
2. tag that exact `main` commit with the next `0.x` validation release tag;
3. let `.github/workflows/release.yml` publish the six supported binaries;
4. verify release asset digests;
5. verify Artifact Attestations against this repository, release workflow, and
   tagged source commit;
6. verify remote install with:

   ```bash
   gh extension install taco3064/gh-shoal --pin <main-release-tag>
   gh shoal --help
   ```

Close the release-foundation work item only after the main-stage distribution
gate passes.
