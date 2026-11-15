# Forge labels: implementation plan

**Spec:** [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (decisions D2430-D2440, findings F1-F26, resolutions R1-R8). **Status: plan only; nothing is implemented yet; the owner approved all tasks on 2026-11-15 (#2787 merged).** Owner: Dimi Paun. Date: 2026-11-15. Checked against `origin/main` at `f70f520cc`; revised after review rounds 1 to 4.

What it builds: the orchestrator daemon puts `fugaro:running`, `fugaro:failed` and `spec:<name>` labels on the pull requests of the runs it launched. The daemon asks the credentials service to perform each label read and write; the service enforces the namespaces and corroborates the pull request, and no token leaves it (D2439). Answers that shaped the plan: GitHub only (D2431), orchestrator-launched tasks only (D2432), `failed` cleared on the next job or on merge/close (D2433), tick-driven with one bulk read per repository per tick (D2434, D2435), a `spec` field set in the work item's patch (D2438), the service creating labels on first use (D2440), colours (D2437).

**Prerequisite outside this plan (F19, O2).** The daemon has no identity to the credentials service today: the daemon account, the invoker grant (Terraform), the variable naming the service and the daemon's ID-token plumbing are the merge mint's unfinished wiring (E10b, `docs/outer-loop-escalation`). T7's live path and Check 640 cannot pass until it lands (T7's own tests run against a fake service); T1-T6, T8 and T9 do not need it. If the owner wants it built here, it is a task added to this plan (O2).

## Contract-first pass

The repository's rigor default is `startup` and `planning.decoupling` is on, so the pass ran. The interface edges are T2 to T1 and T5 to T1 (the `Labeler` interface) and T4 to T3 (the request shape). A contract task C1 would write both shapes first; with the plan's sizes the critical path is 13 without it (T1 3, T3 3, T4 2, T7 3, CK 2) and 12 with it (C1 1, T1 3, T3 3, T7 3, CK 2; T4 runs beside T3), a gain of one weight for a contract task and a review of it. **Declined**: the gain does not pay for the extra task. The T3 to T1 edge is behaviour (the service performs its writes through T1's adapter), so no contract removes it. The graph carries no contract and no decoupling entry.

## Conventions every part follows

- `.claude/rules/credentials-service.md` loads for `internal/creds/**`, `internal/credsvc/**`, `SECURITY.md` and `website/content/security.md`. T3, T4, T6, T7 and T9 are **opus tier with an independent security review**; T1, T2, T5 and T8 are standard.
- **Boundaries.** *Always:* hermetic tests (`internal/gitprov/fake`, `internal/gcpfake`, `httpfixture`, `t.TempDir()`), `gofmt -l .` empty, the targeted command named in each task, comments that say why. *Ask first:* a new dependency, a change to `PRSpec`, `ApplyReady` or `git.pr.labels`, any change under `.github/workflows/`, Terraform. *Never:* a prompt or a confirmation step (CLAUDE.md, "Fugaro asks for nothing"), a secret-shaped literal, a live cloud or forge call by an agent, a label write outside the namespaces, removing a label a person added.
- The daemon's label failures never fail a tick: log one line per cause and continue (spec, "best-effort").
- Documents are tested: after T6, `go test ./internal/cli/ -run Docs -count=1`. `website/content/security.md` is regenerated from `SECURITY.md` with `go run ./internal/clidoc`, never edited by hand.
- A work-item field is additive: `schemas/orchestrator-state.schema.json` and `schemas/orchestrator-patch.schema.json` gain an optional property and no version bumps (CLAUDE.md, "A field never changes meaning"). *(Amended 2026-11-15, as T8 shipped: the **state** schema is the exception, and its own rule is the one that binds — it declares the document closed, every field modelled and a decoder that refuses a field it does not model, so `schema_version` moves whenever a field is added. `orchestrator.SchemaVersion` is 3 and `Migrate` has a 2 → 3 step that also rewrites stored digests, because `spec` is digested. A patch document carries no version of its own — it is read against the node's revision, and a CLI that does not model `spec` refuses a document carrying it rather than applying half of it.)*

## Global constraints and review focus

| Input or failure mode | Pinned by |
| --- | --- |
| A label name outside `fugaro:running`, `fugaro:failed` and an allowed `spec:` shape is refused by the service, whatever the daemon sends | T3 `TestLabelRouteRefusesForeignNames` |
| The syncer removes only the two exact status names and never a `spec:` label a person added; names match case-insensitively | T5 `TestSyncNeverTouchesForeignLabels`, T1 `TestOwnedLabel` |
| A hostile spec name cannot make a label name (R3) | T5 `TestSpecLabelShape`, T3 `TestLabelRouteRefusesForeignNames` |
| A run that writes another pull request's number into its record cannot get that pull request relabelled: the head must be exactly `fugaro/<run id>` for the run id the daemon sends (F17) | T3 `TestLabelRouteCorroboratesPullRequest` |
| Every caller class but the orchestrator is refused, each refusal with one audit line | T9 `TestRouteCallerClassMatrix` (the existing matrix gains the route), T3 `TestLabelRouteRefusesEveryOtherClass` |
| A stolen identity cannot remove a `spec:` label or write label text; colours and descriptions are the service's constants (F26, D2437) | T3 `TestLabelRouteRemovesOnlyStatusNames`, `TestLabelRouteEnsureUsesFixedText` |
| A finished item's pull request number is read from its run record once and cached (F25) | T7 `TestPRReaderCachesPerRun` |
| `failed` stays on a failed run's pull request until it closes (D2433, R8) | T5 `TestPhaseToLabels`, `TestSyncFailedStaysUntilClose` |
| The route's permission set is exactly the narrowest the host needs (U2) | T3 `TestLabelRoutePermissionSet` |
| A second tick with nothing changed makes no write (F15) | T5 `TestSyncChangeOnly`, T7 `TestTickLabelsOnce` |
| Bitbucket and any provider without the capability: silent no-op, no warning per tick (R7) | T2 `TestBitbucketLabelsSilentNoOp` |
| A merged or closed pull request loses both status labels, though the bulk list shows only open ones (R5) | T5 `TestSyncClearsOnMergedPR` |
| Label state is per run and its pull request: a retry's new pull request leaves the first one `failed` until it closes (R8, F21) | T5 `TestSyncPerRunPullRequest` |
| A failed label call never fails the tick | T5 `TestSyncFailureLogsOnce` |
| A daemon restart does not cause redundant writes (F10) | T5 `TestSyncAfterRestartReadsFirst` |
| The route's own rate limit holds a tick every 30 seconds that writes only on a difference (F22) | T3 `TestLabelRouteRateSizedForTick` |

## Slice 1: the sides

### T1: the label capability and the GitHub adapter
- Acceptance: `gitprov.Labeler` exists (`Supported() bool`, `EnsureLabel(name, colour, description)`, `AddLabels(ctx, number, runID, names)`, `RemoveLabels(ctx, number, runID, names)` (the run id is what the service corroborates the pull request's head against, F17; the GitHub adapter ignores it), `ListOpenPRLabels` returning every open pull request's labels by number in one paged read, `PRLabels(number)` returning the state and the labels of one pull request), found by type assertion like `gitprov.MintExempt`; `OwnedLabel` is true only for names in the `fugaro:` and `spec:` namespaces, compared case-insensitively; `github.Provider` implements it, with `EnsureLabel` treating "already exists" as success and `RemoveLabels` of an absent label as success.
- Files: `internal/gitprov/labels.go` (new), `internal/gitprov/labels_test.go` (new), `internal/gitprov/github/labels.go` (new), `internal/gitprov/github/labels_test.go` (new), `internal/gitprov/github/testdata/labels_*.json` (new).
- Interfaces: produces `Labeler` and `OwnedLabel` for T2, T3, T4, T5; consumes nothing.
- Tests: `TestOwnedLabel`, `TestGitHubEnsureLabelExists`, `TestGitHubListOpenPRLabelsPaged`, `TestGitHubRemoveAbsentLabel`, `TestGitHubPRLabelsClosed`.
- Command: `go test ./internal/gitprov/ ./internal/gitprov/github/ -run 'TestOwnedLabel|TestGitHub(EnsureLabel|ListOpenPRLabels|RemoveAbsent|PRLabels)' -count=1`
- Red step: the tests call methods that do not exist.
- Depends on: none.
- Commit: `github: the label capability`.
- Reviewer tier: standard.

### T2: the fake and Bitbucket sides of the capability
- Acceptance: `gitprov/fake` implements `Labeler` with a recorded call log and injectable failures; `bitbucket.Provider` implements it with `Supported() == false` and no-op methods that log nothing (the existing `warnLabels` for `git.pr.labels` is untouched).
- Files: `internal/gitprov/fake/labels.go` (new), `internal/gitprov/fake/labels_test.go` (new), `internal/gitprov/bitbucket/labels.go` (new), `internal/gitprov/bitbucket/labels_test.go` (new).
- Interfaces: consumes T1's `Labeler`.
- Tests: `TestFakeLabeler`, `TestBitbucketLabelsSilentNoOp`.
- Command: `go test ./internal/gitprov/fake/ ./internal/gitprov/bitbucket/ -run 'TestFakeLabeler|TestBitbucketLabelsSilentNoOp' -count=1`
- Red step: the tests do not compile before the methods exist.
- Depends on: T1 (the interface only).
- Commit: `gitprov: the fake and Bitbucket label sides`.
- Reviewer tier: standard.

### T3: the credentials service's label route
- Acceptance: `creds` holds the route's request and response shapes, its mirror of the path constant and the refusal errors; `POST /v1/github/labels` performs one of `list`, `get` (one pull request's state and labels), `ensure`, `add`, `remove` through T1's adapter, using a token it mints itself with exactly `{pull_requests: write, metadata: read}`, caches until near expiry and never returns; the request carries a label name only, and `ensure` applies the colour and description of D2437 itself; it accepts only the orchestrator caller class; it accepts only `fugaro:running`, `fugaro:failed` and a `spec:` name of the shape in R3, and `remove` only the two status names; a write carries the run id of the run that opened the pull request (O3) and the service refuses a pull request whose head branch is not exactly `fugaro/<run id>` or whose head repository is not the base repository (a fork can carry that branch name) or whose author is not Fugaro's own identity (the App's bot login, as `gitprov.PRHead.Self` reports it); a repository outside the installation is refused; exactly one audit line per call, served or refused, under the purpose T9 adds; a name is checked as ASCII before it is lowered; a pull request number of zero or less is refused; the head is read from `PRInfo.SourceBranch` and `SourceRepo` and the author from `PRHead.Self`, two reads per write; its rate limits are stated as numbers (writes 60 an hour and reads 240 an hour per repository, counted apart) and tested for a tick every 30 seconds, and the plan notes that the reads draw on the same App installation's GitHub budget the runs use.
- Files: `internal/creds/labelroute.go` (new), `internal/credsvc/labelroute.go` (T9 creates it as the refusing default; T3 fills it), `internal/credsvc/labelroute_test.go` (new).
- Interfaces: consumes T1 and T9; produces the shapes T4 consumes and the route T7 reaches.
- Tests: `TestLabelRouteRefusesForeignNames`, `TestLabelRouteRemovesOnlyStatusNames`, `TestLabelRouteEnsureUsesFixedText`, `TestLabelRouteCorroboratesPullRequest`, `TestLabelRouteRefusesEveryOtherClass` (job, review, verifier, coordinator, person, launcher and a `GcloudAudience` token), `TestLabelRoutePermissionSet`, `TestLabelRouteRateSizedForTick`, `TestLabelRoutePathMatchesClient`.
- Command: `go test ./internal/credsvc/ ./internal/creds/ -run 'TestLabelRoute' -count=1`
- Red step: the handler does not exist, so the tests get the refusing default from T9.
- Depends on: T1 (the adapter it calls: behaviour, not only a shape), T9 (the purpose, the path constant and the gated route).
- Commit: `credsvc: the label route`.
- Reviewer tier: opus with a mandatory independent security review and mutation checks on the caller class, the permission set, the name check and the head check (weaken each, expect a test to fail).

### T4: the daemon's label client
- Acceptance: `creds.ServiceLabeler` implements a `creds`-owned interface with the `Labeler` method set (`creds` imports only the standard library, as the precedent in `gitprov/merge.go` keeps it; T7 adapts it to `gitprov.Labeler`) by calling T3's route as `ServiceMergeMint` calls its own (the daemon's own ID token, https or loopback only, every refusal mapped to the shape's errors, no token or secret in any error text).
- Files: `internal/creds/labelclient.go` (new), `internal/creds/labelclient_test.go` (new).
- Interfaces: consumes T3's shapes and mirrors T1's method set; produces the client T7 adapts and wires.
- Tests: `TestLabelClientRefusesPlainHTTP`, `TestLabelClientMapsRefusals`.
- Command: `go test ./internal/creds/ -run 'TestLabelClient' -count=1`
- Red step: the type does not exist.
- Depends on: T1 (the interface), T3 (the request and response shape only).
- Commit: `creds: the label client`.
- Reviewer tier: opus with an independent security review.

### T5: the orchestrator's label syncer
- Acceptance: a `Labels LabelSeams` field on `orchestrator.Tick`, unset meaning no labelling; given items with phase, pull request number, repository and spec name, one tick reads once per repository, computes the desired set (R2-R5, R8), and writes only the difference through a `gitprov.Labeler`, sending the run id of the run that opened each pull request with every add and remove: it adds its own `spec:` label and the wanted status label, removes only the exact names `fugaro:running` and `fugaro:failed`, ensures labels once, and reads by number a pull request absent from the open list until it is known closed and clean; failures log once per cause.
- Files: `internal/orchestrator/labelplan.go` (new), `internal/orchestrator/labelsync.go` (new), `internal/orchestrator/labelsync_test.go` (new), `internal/orchestrator/tick.go` (field and one call).
- Interfaces: consumes T1's `Labeler` through a recording double in its own test and T8's `WorkItem.Spec`; produces `LabelSeams` for T7.
- Tests: `TestSyncChangeOnly`, `TestSyncNeverTouchesForeignLabels`, `TestSpecLabelShape`, `TestSyncClearsOnMergedPR`, `TestSyncPerRunPullRequest`, `TestSyncFailureLogsOnce`, `TestSyncAfterRestartReadsFirst`, `TestSyncFailedStaysUntilClose`, `TestPhaseToLabels`.
- Command: `go test ./internal/orchestrator/ -run 'TestSync|TestSpecLabelShape|TestPhaseToLabels' -count=1`
- Red step: the tests reference `LabelSeams`, which does not exist.
- Depends on: T1 (the interface), T8 (the field).
- Commit: `orchestrator: sync status and spec labels on the tick`.
- Reviewer tier: standard.

### T6: the documents
- Acceptance: `docs/git-providers.md` gains the status-label section (the labels, GitHub only, orchestrator-launched runs only, Bitbucket none, `git.pr.labels` unchanged, a plain `fugaro run` not labelled); `SECURITY.md` names the label route among what the service does and states its real reach in the same words as T3's code (the service performs the write, no token leaves it, a stolen daemon identity can relabel only Fugaro's own pull requests with the allowed names, and an agent's session on the operator's machine can present that identity as it can for the merge route); `website/content/security.md` is regenerated; the next release's notes (`docs/releases/vX.Y.Z.md`, named when the task is launched) say `### For operators`: run `fugaro update`; the Docs tests pass.
- Files: `docs/git-providers.md`, `SECURITY.md`, `website/content/security.md` (regenerated), `docs/releases/vX.Y.Z.md`, `internal/cli/docs_forge_labels_test.go` (new).
- Interfaces: written against the reconciliation and T3's behaviour; T7's review checks it against the built behaviour.
- Tests: the existing `Docs` tests and `internal/cli/docs_website_security_test.go`.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run Docs -count=1`
- Red step: a new pinned sentence added to the Docs test that names the section fails before the text is added.
- Depends on: T3 (it documents the route's reach, which T3 fixes).
- Commit: `docs: forge status labels`.
- Reviewer tier: opus (it edits `SECURITY.md`).

### T8: the work item carries its spec
- Acceptance: `WorkItem` gains an optional `spec` field (a plan folder name, shape-checked as in R3), set by `work_item_upsert` (the daemon has no plan import: a planning session or a person writes it in the patch, D2438), and in both schemas as an optional property with no version bump; an item with none is unchanged; an upsert that omits it leaves it alone. *(Amended 2026-11-15, as T8 shipped: the **state** version does move — `orchestrator.SchemaVersion` is 3, and `Migrate` gains a 2 → 3 step. `spec` is declarative, so `SpecDigest` covers it, and the wider walk answers something else for every item the release before this one wrote: an approval a person gave would never match its item again, so the step rewrites an item's approval and intent digests, and only where the stored value is what the old walk answered for that item — a digest that was never the item's is left refused rather than blessed. Every item moves, not only a labelled one: an empty `spec` is a hashed empty string, not an absent field, so the clause above that an item with none is unchanged holds for its encoding and not for its digest. The step runs at the daemon's start-up, so `fugaro update` stays the whole upgrade. The patch document's shape is additive as written: it carries no version, and what refuses it is the reader that does not model a field.)*
- Files: `internal/orchestrator/state.go`, `internal/orchestrator/patch.go`, `internal/orchestrator/validate.go`, `internal/orchestrator/state_test.go`, `internal/orchestrator/patch_test.go`, `schemas/orchestrator-state.schema.json`, `schemas/orchestrator-patch.schema.json` (the state summary, if it lists fields, is the seventh file; split the schemas off if the run finds it past seven). *(Amended 2026-11-15: `validate.go` is where the three shape checks live, and `Migrate`'s step with `redigestV2` is in `state.go` — the migration is not a file of its own. The run's footprint was wider than this list: `internal/orchestrator/summary.go` and its test (the summary the line above allows), `internal/orchestrator/testdata/state-a.json` (the two pinned digests), and, over the review of the first commit, `internal/cli/orchestrator_patch.go`, `internal/cli/orchestrator_patch_test.go`, `internal/cli/orchestrator_run_test.go` and `schemas/orchestrator-output.schema.json`.)*
- Interfaces: produces `WorkItem.Spec` for T5 and T7.
- Tests: `TestWorkItemSpecRoundTrip`, `TestWorkItemSpecShapeRefused`, `TestStateSchemasAcceptSpec`.
- Command: `go test ./internal/orchestrator/ -run 'TestWorkItemSpec|TestStateSchemasAcceptSpec' -count=1`
- Red step: the tests set a field `WorkItem` does not have.
- Depends on: none.
- Commit: `orchestrator: a work item carries its spec`.
- Reviewer tier: standard.

### T9: the label route's census
- Acceptance: a purpose `label` is valid in the audit vocabulary (`ValidPurpose`, `callerWithoutJobPurpose`, `repositoryOnlyPurpose`, the tier rule and the `routeAct` oracle); `internal/credsvc` owns the path constant `/v1/github/labels` and serves it with the caller-class gate final (only the orchestrator class passes, every other class is refused with one audit line) and a default refusal behind the gate, in `internal/credsvc/labelroute.go`, until T3 fills it; the route sits in the caller-class matrix, and the census pins that name served routes (the 404 sentence and its pins) are updated.
- Files: `internal/credsvc/labelroute.go` (new, the refusing default), `internal/credsvc/audit.go`, `internal/credsvc/server.go`, `internal/credsvc/callerclass_test.go`, `internal/credsvc/audit_answer_test.go`, `internal/credsvc/slip_test.go` (which also holds the new `TestServedRoutesCensus`).
- Interfaces: produces the purpose, the path constant and the gated route T3 fills.
- Tests: `TestRouteCallerClassMatrix`, `TestAuditPurposeLabel`, `TestServedRoutesCensus` (new).
- Command: `go test ./internal/credsvc/ -run 'TestRouteCallerClassMatrix|TestAuditPurposeLabel|TestServedRoutesCensus' -count=1`
- Red step: the matrix names a route that is not served.
- Depends on: none.
- Commit: `credsvc: the label route's purpose, gate and census`.
- Reviewer tier: opus with an independent security review.

## Slice 2: the join

### T7: wire the daemon to the real label path and prove it
- Acceptance: `fugaro orchestrator run`'s tick gets `LabelSeams` built from `creds.ServiceLabeler`, taking the repository from the daemon's own item, the opening run's id from the item's own intents (O3), the adapter from `creds.ServiceLabeler` to `gitprov.Labeler`, the spec name from `WorkItem.Spec` and the pull request number from the run record through a reader cached per run id (a finished or failed item keeps its labels too); an end-to-end test against `gcpfake` and the fake provider shows a `running` item labelled, a `failed` item relabelled, a merged pull request cleared, and a second unchanged tick making no write.
- Files: `internal/cli/orchestrator_labels.go` (new), `internal/cli/orchestrator_labels_test.go` (new), `internal/cli/orchestrator_run.go` (the field).
- Tests: `TestTickLabelsOnce`, `TestTickLabelsFailedThenRetry`, `TestPRReaderCachesPerRun`.
- Command: `go test ./internal/cli/ -run 'TestTickLabels|TestPRReaderCachesPerRun' -count=1`
- Red step: the daemon's tick has no `LabelSeams`, so the test sees no label write.
- Depends on: T2 (the fake provider), T3, T4, T5, T8, and the prerequisite above (the daemon's identity to the service; until it lands the end-to-end test runs against a fake service and only the live path stays unproven).
- Commit: `cli: the orchestrator daemon labels pull requests`.
- Reviewer tier: opus with an independent security review.

### CK: Check 640
- Acceptance: `scripts/live-checks/check640.sh`, its test `check640_test.sh`, a Docs test `internal/cli/docs_check640_test.go` and a row in `docs/gcp-live-checklist.md`: with the daemon running on the dogfood installation and one approved task launched, the pull request shows `fugaro:running`, then none (ready) or `fugaro:failed`, and a `spec:<name>` label with the right colour; it settles U1 and U3.
- Files: `scripts/live-checks/check640.sh` (new), `scripts/live-checks/check640_test.sh` (new), `scripts/live_checks_test.go` (adds both scripts to `liveCheckScripts`, or CI never runs the test), `internal/cli/docs_check640_test.go` (new), `docs/gcp-live-checklist.md`.
- Tests: `check640_test.sh` and `TestDocsCheck640`.
- Command: `bash scripts/live-checks/check640_test.sh && go test ./internal/cli/ -run TestDocsCheck640 -count=1`
- Red step: `TestDocsCheck640` fails before the register's row exists.
- Depends on: T7 (the check runs the real path).
- Commit: `checks: Check 640, the status labels on a real pull request`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check640.sh --installation fugaro` after the release carrying T7 is installed and the prerequisite has landed. No agent performs it.

## Parallel execution

**Edges kept:**
- T2 to T1, T5 to T1: they need the `Labeler` interface T1 writes (the contract pass was run and declined, above).
- T3 to T1: the route performs its writes through T1's adapter (behaviour). T3 to T9: the route fills the purpose and the registered route T9 adds (same census, one writer of `server.go`).
- T4 to T3, T4 to T1: the client speaks T3's shape and implements T1's interface.
- T5 to T8: the syncer reads the `spec` field.
- T6 to T3: the documents state what T3 fixes.
- T7 to T2, T3, T4, T5, T8: the wiring uses all of them.
- CK to T7: the check runs the real path.

**Edges broken by contract:** none.

**Levels** (tasks in one level run at once):
- L0: T1, T8, T9.
- L1: T2, T3, T5.
- L2: T4, T6.
- L3: T7.
- L4: CK.

**Critical path:** T1, T3, T4, T7, CK. T9, T8, T5, T2 and T6 run beside it.

**Shared files:** only T1 writes `internal/gitprov/labels.go`; only T9 writes `internal/credsvc/server.go`, `audit.go` and the census pins; only T5 writes `internal/orchestrator/tick.go`; T8 writes `state.go`, `patch.go` and their tests and T5 does not; only T7 writes `internal/cli/orchestrator_run.go`; T6 owns the documents and the Docs test it adds. No level holds two writers of one file.

| File | Writer | Level |
| --- | --- | --- |
| `internal/gitprov/labels.go`, `internal/gitprov/github/labels.go` | T1 | L0 |
| `internal/orchestrator/state.go`, `patch.go`, `state_test.go`, `patch_test.go`, the two schemas | T8 | L0 |
| `internal/gitprov/fake/labels.go`, `internal/gitprov/bitbucket/labels.go` | T2 | L1 |
| `internal/creds/labelroute.go`, `internal/credsvc/labelroute_test.go` | T3 | L1 |
| `internal/credsvc/labelroute.go` (the refusing default), `audit.go`, `server.go`, `callerclass_test.go`, `audit_answer_test.go`, `slip_test.go` | T9 | L0 |
| `internal/credsvc/labelroute.go` (filled) | T3 | L1 |
| `internal/orchestrator/labelsync.go`, `labelplan.go`, `tick.go` | T5 | L1 |
| `internal/creds/labelclient.go` | T4 | L2 |
| `docs/git-providers.md`, `SECURITY.md`, `website/content/security.md`, `docs/releases/vX.Y.Z.md`, `internal/cli/docs_forge_labels_test.go` | T6 | L2 |
| `internal/cli/orchestrator_labels.go`, `orchestrator_run.go` | T7 | L3 |
| `scripts/live-checks/check640*.sh`, `scripts/live_checks_test.go`, `internal/cli/docs_check640_test.go`, `docs/gcp-live-checklist.md` | CK | L4 |

**Considered and declined.** A contract task for the interface and the request shape (a gain of one weight; above); a GitLab adapter (D2431); a webhook receiver (D2434); `fugaro setup` or `update` pre-creating the labels (D2440); writing labels from the runner (D2432); a sibling token mint with `pull_requests: write` (D2439: a token that can approve or close any pull request); the person's own credential (it is not available to a daemon that runs without them); reading the spec name from the run's launch record (D2438: the run writes it); deriving it in a plan import (none exists).

## Checks the plan takes

**Check 640** (reserved block 640-649): the labels on a real pull request on the dogfood installation, run by the owner (CK).

## After-deploy steps

For a person, only `fugaro update`, which already redeploys the credentials service and re-renders the daemon's unit; the release's `### For operators` says exactly that (T6). The prerequisite (the daemon's identity to the service, Terraform) is carried by `fugaro update` when it lands, under its own plan; this plan adds no operator step. **One conditional owner step:** if Check 640 shows the GitHub App lacks `pull_requests: write` (U1), the owner raises it on the App's settings page; an agent cannot.
