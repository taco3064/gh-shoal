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
GitHub's workflow enablement prompt. `init` also ensures Issues are enabled and
repairs the GitHub workflow state of the canonical
`.github/workflows/reviewer-summary.yml` if it is inactive. It enables only that
workflow and verifies it is active. `init` does not enable repository-level
Actions or change Actions policy. A successful `init` confirms managed file,
Issues, and canonical workflow-state repair; it does not prove that the manual
Actions confirmation happened or that a workflow run will execute successfully.

Run Automated Review from a clean local Reviewer Node checkout, authenticated as
its Personal Account owner. The canonical Network Root is the root owner's
Reviewer Node; other Reviewers use a direct fork. `gh shoal init` remains a
direct-fork synchronization command and must not run against the Network Root:

```bash
gh shoal review --agent codex
```

Choose one installed Local AI Agent explicitly: `claude`, `codex`, `gemini`,
`opencode`, `cursor`, `grok`, `qwen`, or `kimi`. The extension scans all current
open Issues and applies the admission rules. Invalid requests receive an
explanation and are closed. Valid pending Reviews run in FIFO batches of at
most five. The Agent reads the Reviewer Node's `README.md` policy and writes
its judgments to the ignored temporary `.shoal/review-results.json` file.
The extension then ensures the corresponding Star state, appends a
machine-readable Review Result with the Agent explanation and commit identities,
and closes successfully completed Issues. Failed items remain open for a later
run. The extension does not synchronize your local branch or run Target code.

Run Reviewer-triggered maintenance and pending Re-review work with the same
explicit Local AI Agent contract:

```bash
gh shoal re-review --agent codex
```

`re-review` inspects existing canonical Review Threads, including completed
closed threads and canonical threads already reopened by a valid
`RE_REVIEW_REQUESTED` event. It uses the latest usable Review Judgment as the
prior basis and compares the current Target default-branch HEAD plus the current
`README.md` Review Policy commit. Threads with a changed basis are revalidated
again immediately before semantic judgment and then processed through the same
FIFO batches of at most five and the same Agent result contract as Automated
Review. A successful semantic Re-review records the Protocol-defined Judgment
type using the judgment-start Target and Policy commits: PASS records
`RE_REVIEWED`, while FAIL that revokes the endorsement records `STAR_REVOKED`.

When the Review Basis has not changed, `re-review` does not ask the Agent for a
new judgment. Instead, it verifies endorsement state against the latest usable
Judgment: a missing Star after PASS is restored, and a present Star after FAIL is
removed. This deterministic maintenance does not create a new semantic Review
Event. A closed thread with no usable prior Judgment is skipped rather than
reconstructed, and malformed older comments do not override a newer valid
Judgment. Target repositories are identified by stable GitHub Repository ID and
are not executed during discovery, classification, or maintenance.

The machine-readable request and event contract comes from
[`shoal-app/protocol/review-v1.json`](https://github.com/taco3064/shoal-app/blob/main/protocol/review-v1.json).
Its checked-in embedded copy at `reviewruntime/protocol/review-v1.json` must remain
byte-identical to the approved platform contract.

## Compatibility, migration, and recovery

The installed binary uses a generated capability snapshot of the exact reviewed
Platform source at `134d82457c99777cba752a549fb7e26ab239d71c` (tree
`7ff3bfca36f1e0b8b4c5d46d43e0510454d99b5c`). The snapshot includes the exact
Protocol bytes and the Platform's explicit managed-surface / Summary contract
bindings. The accepted workload generation uses Protocol 1 / Summary Schema 2;
all four retained official Schema 1 generations keep their original bindings.
The CLI uses that schema only for compatibility, without computing workload
metrics or changing Review / Re-review semantics. CI and release verification compare committed Platform bytes and
regenerate the snapshot. Updating a component version does not expand Protocol
support; mutable runtime discovery never expands an installed binary's authority.

Review commands check the remote default-branch managed surfaces at one exact
commit and scan formal history before admission, Agent execution, or lifecycle
mutation. Older explicitly supported official station generations can still be
reviewed. An explicit `init` nevertheless synchronizes to the current verified
Network Root, provided that generation is within the installed capability. It
never changes `README.md` or historical comments.

| Diagnostic | Safe next action |
| --- | --- |
| `SUPPORTED` | Existing Review lifecycle rules apply. |
| `REPAIRABLE_STATION_DRIFT` | Run `gh shoal init` from synchronized main; Root owners repair canonical surfaces through maintainer work. |
| `CLI_UPGRADE_REQUIRED` | Upgrade the official Extension before retrying. |
| `INCOMPATIBLE_PROTOCOL_EVIDENCE` | Inspect unsupported formal evidence; this binary will not reinterpret or rewrite it. |
| `EXTERNAL_STATE_UNAVAILABLE` | Restore observable external state before retrying; inspect ambiguous writes rather than blindly repeating them. |
| `NO_CHANGES` | Successful no-op; no synthetic mutation is needed. |

Lost write acknowledgements are followed by bounded authoritative reads. An
exact owner-authored comment or verified converged Star / Issue / settings state
can complete recovery. Unknown state never authorizes a second blind write.
A completed Judgment followed by a failed close is recognized on retry without
another semantic Judgment. Failed `init` pushes retain local rollback behavior;
reconcile the observable remote main before retrying if the remote may have
accepted the push.

Reproduce source correspondence and installed-command smoke with Node 24, Go,
and GitHub CLI available:

```bash
node script/compatibility.mjs <exact-shoal-app-checkout>
go test ./...
node script/smoke-compatibility.mjs
```

The smoke uses an isolated GitHub CLI installation, native local Git transport,
and controlled GitHub / Agent responses. It performs no real Review, Star,
workflow, or repository-setting writes.

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
git tag v0.1.0
git push origin v0.1.0
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

## Shared host runtime

The local commands and downstream hosted integration use the same public
[`reviewruntime` package](docs/shared-review-runtime.md). The contract documents
exact-generation consumption, semantic Agent input/results, separate GitHub
authority roles, structured failures, and deterministic recovery.
