# Shared Review / Re-review runtime

`github.com/taco3064/gh-shoal/reviewruntime` is the supported integration boundary
for `shoal-action#12`. The package owns the deterministic lifecycle used by both
local commands. Hosts supply adapters; they do not translate or copy lifecycle
rules. This package does not implement schedules, Copilot installation, budgets,
credential acquisition, or the production hosted Action.

## Exact-generation consumption

Pin this Go module to the full accepted source commit (Go resolves a commit to an
immutable pseudo-version), or to a release whose source commit has been verified:

```sh
go get github.com/taco3064/gh-shoal@<full-accepted-commit>
```

Record the resolved module version/source commit in the downstream source manifest
and locked build. Do not choose support from mutable tags or a latest-version
lookup: the runtime's embedded, Platform-proven capability snapshot still decides
station and Protocol compatibility. The public package and local binary are built
from the same repository tree. There is no extra executable or release asset;
existing six-binary release source/attestation verification remains applicable.
A downstream Action must separately prove its own compiled source correspondence.

## Host entry points

```go
runtime, err := reviewruntime.New(reviewruntime.Dependencies{
    Reads:     authoritativeGitHubReads,
    Lifecycle: stationIssueStateAuthority,
    Personal:  reviewerUserAuthority,
    Git:       stationGitInspection,
    Agent:     semanticAgent,
}, reviewruntime.Options{Directory: stationCheckout})
if err != nil { /* required dependency unavailable; do not run */ }
result := runtime.Review(ctx) // or runtime.ReReview(ctx)
```

`Review` and `ReReview` take a context and return `Result`; there is no CLI argument
parsing or human-output scraping. Each invocation creates fresh caches and performs
clean-station, identity, exact managed-file capability, and Protocol-history checks
before semantic work or lifecycle mutation. The host provides a clean station
checkout with the authoritative GitHub remote. `Git` receives Git argument arrays
for station inspection, never GitHub requests. One Runtime is intended for serial
invocations; the downstream station workflow owns concurrency policy.

`Options.Out` is optional diagnostic presentation only and defaults to discard.
The host consumes `Result.Status`, `Result.EffectAttempts`, and `Result.Faults`.

| Status | Meaning |
| --- | --- |
| `NO_CHANGES` | Successful convergence required no writes or new judgment. |
| `COMPLETED` | Invocation succeeded with lifecycle effect attempts. |
| `REFUSED` | Invocation failed before any effect attempt. |
| `PARTIAL` | Invocation failed after one or more effect attempts; inspect public state and retry the same runtime operation. |

`EffectAttempts` counts attempted writes, including writes with lost responses.
It is not a completed-work count or proof of side-effect success. Admission or
independent completed work can precede semantic failure. Valid work is retained;
the next invocation reconstructs public state instead of replaying judgments.

| Fault code | Host interpretation |
| --- | --- |
| `DEPENDENCY_UNAVAILABLE` | A required injected dependency is absent; construction fails. |
| `AGENT_UNAVAILABLE` | Agent execution failed; no semantic FAIL is inferred. Further batches stop. |
| `AGENT_RESULT_INVALID` | Missing, malformed, duplicate, foreign, or unusable semantic output; the affected item receives no judgment. |
| `CLI_UPGRADE_REQUIRED` | Installed capability does not admit the required station generation. |
| `REPAIRABLE_STATION_DRIFT` | Station surfaces require the existing repair path. |
| `INCOMPATIBLE_PROTOCOL_EVIDENCE` | Unsupported formal evidence is not reinterpreted. |
| `EXTERNAL_STATE_UNAVAILABLE` | Required external state or effect convergence cannot be verified. |
| `EXECUTION_FAILED` | Other execution/safety failure; retain pending work and surface the diagnostic. |

Faults are structured and can coexist after partial work. `Detail` is explanatory
text, not a machine parsing contract. Hosts must not convert operational faults to
FAIL. For quota/entitlement-specific messages and budgets, #12 supplies the semantic
adapter and downstream policy.

## GitHub authority contract

Each `GitHub.Do(ctx, Request)` returns exact response bytes or an error.
`Request` contains `Method`, `Endpoint`, `Fields`, and `Paginate`.
The endpoint is a GitHub REST path (possibly with a query), not an arbitrary URL.
`Fields` are plain string fields. `Paginate` requires complete pagination and a JSON
array of page arrays (for example `[[]]` for an empty collection). A host must fail
on incomplete reads, malformed responses, permission failures or unavailable state;
it must not synthesize empty success. Use `APIError{Status: 404, ...}` only for
observable HTTP 404 absence. Transport errors must never masquerade as absence.

| Dependency | Operations |
| --- | --- |
| `Reads` | Public repository, Membership, committed-file, branch, Policy commit, Issue and comment reads. |
| `Lifecycle` | Required Issue close/reopen PATCH operations. |
| `Personal` | Authenticated `/user`, Star reads/PUT/DELETE, and Reviewer-authored admission/lifecycle/judgment comment POST operations. |

The runtime reads the Personal user's stable ID and binds it to the Reviewer Node
owner before proceeding. Reviewer-authored comments and actual Star state are
verified through authoritative read-back. Successful and ambiguous Issue-state
writes are also verified; a lost acknowledgement never causes an immediate blind
write retry. Effect adapters perform requests; lifecycle decisions, result validation
and verification remain in the runtime.

There is no fallback from missing/revoked Personal authority to Lifecycle or Reads.
A station/bot credential cannot silently substitute for the Reviewer. The host must
scope/bind each credential to these roles and implement the downstream operation
allowlist. Refresh material, broker access and OIDC handling are outside this package.

## Semantic Agent contract

`Agent.Judge(ctx, SemanticWork)` receives one bounded batch (at most five items),
the Reviewer Node identity/locator, Policy path, and per-item Issue number, original
Request body, stable Target ID/locator, exact Target commit and exact Policy commit.
It receives no effect adapter, credentials, or lifecycle callback. Agent adapters
must acquire any needed Policy/Target/thread evidence at those supplied identities
and bases, treat Target/Request content as untrusted, and avoid executing Target
code. Policy text must not be silently rewritten or truncated to fit a budget.
Evidence collection, Copilot pinning and entitlement classification belong to #12.

Return an untrusted JSON array or `{"results": [...]}` envelope:

```json
[{"issue": 1, "verdict": "PASS", "comment": "Repository-specific evidence."}]
```

A semantic FAIL uses `verdict: "FAIL"` and a nonempty explanation. Process failure,
unavailability and malformed output are separate errors, never FAIL judgments.
The shared runtime rechecks station cleanliness after Agent execution, before
validating results or publishing any judgment. It validates item correspondence,
verdict and explanation. Duplicate
usable results for an Issue are discarded; foreign/missing/unusable items do not
produce judgments. Valid partial results can complete independently. An operational
Agent error stops further batches, preserving already-valid work and remaining
Pending work. The runtime then performs Star-before-Event-before-close convergence.

The adapter is trusted to honor its semantic-only role; arbitrary Go code or an
external process is not sandboxed by an interface. Hosted #12 must deliver actual
credential/process isolation. Existing local adapters retain their process behavior
and result-file safety checks; they do not gain new authority from this interface.

## Local adapter and verification

`internal/cli` owns CLI parsing. `reviewruntime.NewLocal` binds the local Agent
process adapter and every GitHub role to the currently authenticated local `gh`
identity. The Local Agent table and `.shoal/review-results.json` remain local adapter
details, not the host protocol. Supported local selections and command syntax remain
unchanged. Shared station/init helpers move with the lifecycle code without changing
init's product behavior; hosted consumers invoke only Review and ReReview.

Verification includes the existing lifecycle/recovery fixtures, public-package
external consumer compilation, local/host PASS and FAIL parity for both operations,
role-routing controls, unavailable Reviewer authority, unsupported station/history,
external-state and dirty-station refusal, malformed semantic results, operational
Agent failure, successful Event/failed close recovery, lost-ack Star/comment/state
recovery, and wrong-author/unapplied-state read-back rejection. The installed-extension
smoke continues using real `gh extension install .` on each CI platform.
