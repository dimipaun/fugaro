# Forge labels: implementation plan

**Spec:** [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (decisions D2430-D2437, findings F1-F16, resolutions R1-R7). **Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's own explicit go. Owner: Dimi Paun. Date: 2026-11-15. Checked against `origin/main` at `f70f520cc`.

What it builds: the orchestrator daemon puts `fugaro:running`, `fugaro:failed` and `spec:<name>` labels on the pull requests of the runs it launched, through a new optional label capability on the forge interface, a GitHub adapter, a label-only mint in the credentials service, and a syncer on the tick. Answers that shaped the plan: GitHub only (D2431), orchestrator-launched tasks only (D2432), `failed` cleared on the next job or on merge/close (D2433), tick-driven with one bulk read per repository per tick (D2434, D2435), a sibling label mint (D2436, amended; O1), colours (D2437).

The repository's rigor default is `startup` and `planning.decoupling` is on, so the contract-first pass ran. It **reported no gain and was undone**: with the contract (C1) the critical path weighed 10 against 9 without it, because the wiring task is needed either way and the sides it would separate (an interface of a few lines, a request shape) are small. The plan has no contract task; the first writer of each shape defines it (T1 the `Labeler` interface, T3 the mint's request shape), and the dependents wait on that one task, which is one small change each.

## Conventions every part follows

- `.claude/rules/credentials-service.md` loads for `internal/creds/**` and `internal/credsvc/**`; T3, T4 and T7 are **opus tier (T6 too, because it edits `SECURITY.md`) with an independent security review**. T1, T2 and T5 are standard.
- **Boundaries.** *Always:* hermetic tests (`internal/gitprov/fake`, `internal/gcpfake`, `httpfixture`, `t.TempDir()`), `gofmt -l .` empty, the targeted command named in each task, comments that say why. *Ask first:* a new dependency, a change to `PRSpec`, `ApplyReady` or `git.pr.labels`, any change under `.github/workflows/`. *Never:* a prompt or a confirmation step (CLAUDE.md, "Fugaro asks for nothing"), a secret-shaped literal, a live cloud or forge call by an agent, a label write outside the `fugaro:` and `spec:` namespaces, removing a label a person added.
- The daemon's label failures never fail a tick: log one line per cause and continue (spec, "best-effort").
- Documents are tested: after T6, `go test ./internal/cli/ -run Docs -count=1` and, if `website/content/security.md` changed, `npm run check` in `website/`.

## Global constraints and review focus

| Input or failure mode | Pinned by |
| --- | --- |
| Label names outside `fugaro:` and `spec:` are never added or removed, even when the list has them | T1 `TestOwnedLabel`, T5 `TestSyncNeverTouchesForeignLabels` |
| A hostile plan-task name cannot make a label name (R3) | T5 `TestSpecLabelShape` |
| A run's identity or an agent's session cannot mint a label token | T3 `TestLabelMintRefusesRunIdentity`, T4 `TestLabelMintClientRefusesPlainHTTP` |
| A second tick with nothing changed makes no write (F15) | T5 `TestSyncChangeOnly`, T7 `TestTickLabelsOnce` |
| Bitbucket and any provider without the capability: silent no-op, no warning per tick (R7) | T2 `TestBitbucketLabelsSilentNoOp` |
| Merged or closed pull request loses both status labels (R4, D2433) | T5 `TestSyncClearsOnMergedPR` |
| A failed label call never fails the tick | T5 `TestSyncFailureLogsOnce` |
| A daemon restart does not cause redundant writes (F10) | T5 `TestSyncAfterRestartReadsFirst` |

## Slice 1: the sides

### T1: the GitHub label adapter
- Acceptance: `gitprov.Labeler` exists (`Supported() bool`, `EnsureLabel`, `AddLabels`, `RemoveLabels`, `ListPRLabels` returning every open pull request's labels by number) with `OwnedLabel`, true only for names starting `fugaro:` or `spec:`, found by type assertion like `gitprov.MintExempt`; and `github.Provider` implements it: `EnsureLabel` creates a label with colour and description and treats "already exists" as success; `AddLabels` and `RemoveLabels` are idempotent (removing an absent label is success); `ListPRLabels` returns the labels of every open pull request in one paged read.
- Files: `internal/gitprov/labels.go` (new), `internal/gitprov/labels_test.go` (new), `internal/gitprov/github/labels.go` (new), `internal/gitprov/github/labels_test.go` (new), `internal/gitprov/github/testdata/labels_*.json` (new, recorded-shape fixtures).
- Interfaces: produces the `Labeler` interface and `OwnedLabel` that T2 and T5 consume, and the real adapter T7 wires; consumes nothing.
- Tests: `TestOwnedLabel`, `TestGitHubEnsureLabelExists`, `TestGitHubListPRLabelsPaged`, `TestGitHubRemoveAbsentLabel`.
- Command: `go test ./internal/gitprov/ ./internal/gitprov/github/ -run 'TestOwnedLabel|TestGitHub(EnsureLabel|ListPRLabels|RemoveAbsent)' -count=1`
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
- Depends on: T1 (the interface only; the adapters do not need GitHub's calls).
- Commit: `gitprov: the fake and Bitbucket label sides`.
- Reviewer tier: standard.

### T3: the credentials service's label mint
- Acceptance: `creds` holds the mint's request and response shapes, the path constant `/v1/github/label-token` and the refusal errors; `POST /v1/github/label-token` mints a repository-scoped GitHub token for the orchestrator class only; a run's identity, an unknown caller and a repository outside the installation are refused with the contract's codes; the rate limit per repository holds; the path equals `creds`' constant (a test pins both).
- Files: `internal/creds/labelmint.go` (new: the shapes, the path, the errors and the `LabelMinter` interface), `internal/credsvc/labelmint.go` (new), `internal/credsvc/labelmint_test.go` (new), `internal/credsvc/server.go` (route).
- Interfaces: produces the shapes and `LabelMinter` T4 consumes; mirrors `credsvc/mergemint.go` and `creds/mergemint.go`.
- Tests: `TestLabelMintRefusesRunIdentity`, `TestLabelMintScopedToRepo`, `TestLabelMintPathMatchesClient`.
- Command: `go test ./internal/credsvc/ -run 'TestLabelMint' -count=1`
- Red step: the route does not exist, so the tests get 404.
- Depends on: none.
- Commit: `credsvc: a label-only token mint`.
- Reviewer tier: opus with a mandatory independent security review, and at least one mutation check (widen the identity check, expect a test to fail).

### T4: the label mint client
- Acceptance: `creds.ServiceLabelMint` implements `creds.LabelMinter` as `ServiceMergeMint` does (the daemon's own ID token, https or loopback only, every refusal mapped to the contract's errors).
- Files: `internal/creds/labelmint_service.go` (new), `internal/creds/labelmint_service_test.go` (new).
- Interfaces: consumes T3's shapes and `LabelMinter`; produces the client T7 wires.
- Tests: `TestLabelMintClientRefusesPlainHTTP`, `TestLabelMintClientMapsRefusals`.
- Command: `go test ./internal/creds/ -run 'TestLabelMintClient' -count=1`
- Red step: the type does not exist.
- Depends on: T3 (the request and response shape only).
- Commit: `creds: the label mint client`.
- Reviewer tier: opus with an independent security review.

### T5: the orchestrator's label syncer
- Acceptance: a `LabelSeams` field on `orchestrator.Tick`, unset meaning no labelling; given items with phases, pull request numbers and spec names, one tick reads labels once per repository, computes the desired `fugaro:` and `spec:` set (R2-R4), and writes only the difference through a `gitprov.Labeler`, ensures labels once, and never touches a name outside the namespaces; failures log once per cause.
- Files: `internal/orchestrator/labelplan.go` (new), `internal/orchestrator/labelsync.go` (new), `internal/orchestrator/labelsync_test.go` (new), `internal/orchestrator/tick.go` (field and one call).
- Interfaces: consumes T1's `Labeler` through a recording double in its own test; produces `LabelSeams` for T7.
- Tests: `TestSyncChangeOnly`, `TestSyncNeverTouchesForeignLabels`, `TestSpecLabelShape`, `TestSyncClearsOnMergedPR`, `TestSyncFailureLogsOnce`, `TestSyncAfterRestartReadsFirst`, `TestPhaseToLabels`.
- Command: `go test ./internal/orchestrator/ -run 'TestSync|TestSpecLabelShape|TestPhaseToLabels' -count=1`
- Red step: the tests reference `LabelSeams`, which does not exist.
- Depends on: T1 (the interface and `OwnedLabel`).
- Commit: `orchestrator: sync status and spec labels on the tick`.
- Reviewer tier: standard.

### T6: the documents
- Acceptance: `docs/git-providers.md` gains the status-label section (what the labels are, GitHub only, orchestrator-launched runs only, Bitbucket none, `git.pr.labels` unchanged, a plain `fugaro run` not labelled); `SECURITY.md` and `website/content/security.md` name the label mint among what the service mints, in the same words; the Docs tests pass.
- Files: `docs/git-providers.md`, `SECURITY.md`, `website/content/security.md`, `docs/releases/` (the next release's notes: `### For operators` is "run `fugaro update`").
- Interfaces: written against the reconciliation (R1-R7, D2430-D2437); T7's review checks it against the built behaviour.
- Tests: the existing `Docs` tests, and `npm run check` in `website/`.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: a new pinned sentence added to the Docs test that names the section fails before the text is added.
- Depends on: none.
- Commit: `docs: forge status labels`.
- Reviewer tier: opus (it edits `SECURITY.md`, a credentials-service document).

## Slice 2: the join

### T7: wire the daemon to the real label path and prove it
- Acceptance: `fugaro orchestrator run`'s tick gets `LabelSeams` built from `creds.ServiceLabelMint` and the GitHub adapter, resolves each item's pull request number from the run record and its spec name from `PlanTask`; an end-to-end test against `gcpfake` and the fake provider shows a `running` item labelled, a `failed` item relabelled, and a second unchanged tick making no write.
- Files: `internal/cli/orchestrator_labels.go` (new), `internal/cli/orchestrator_labels_test.go` (new), `internal/cli/orchestrator_run.go` (the field).
- Tests: `TestTickLabelsOnce`, `TestTickLabelsFailedThenRetry`.
- Command: `go test ./internal/cli/ -run 'TestTickLabels' -count=1`
- Red step: the daemon's tick has no `LabelSeams`, so the test sees no label write.
- Depends on: T1, T2 (the fake used in the end-to-end test), T3, T4, T5: it uses all of them.
- Commit: `cli: the orchestrator daemon labels pull requests`.
- Reviewer tier: opus with an independent security review (it joins the mint to the forge write).

### CK: Check 640
- Acceptance: `scripts/live-checks/check640.sh` and a row in `docs/gcp-live-checklist.md`: with the daemon running on the dogfood installation and one approved task launched, the pull request shows `fugaro:running`, then none (ready) or `fugaro:failed`, and a `spec:<name>` label with the right colour; it settles U1 and U3.
- Files: `scripts/live-checks/check640.sh` (new), `docs/gcp-live-checklist.md`.
- Tests: the repository's script tests for the live-checks folder.
- Command: `bash scripts/live-checks/check640.sh --help`
- Red step: the register's Docs test for the row fails before the row exists.
- Depends on: T7 (the check runs the real path).
- Commit: `checks: Check 640, the status labels on a real pull request`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check640.sh --installation fugaro` after the release carrying T7 is installed. No agent performs it.

## Parallel execution

**Edges kept** (the dependent needs the other task's work, and a contract would not shorten the path):
- T2 to T1, T5 to T1: they need the `Labeler` interface, which T1 writes in a few lines; the contract-first pass offered to pull it into its own task and reported no gain (above).
- T4 to T3: the client needs the request and response shape T3 writes.
- T7 to T1, T2, T3, T4, T5: the wiring uses all of them.
- CK to T7: the check runs the real path.

**Edges broken by contract:** none; the pass was run and undone (no gain). The graph carries no contract and no decoupling entry.

**Levels** (tasks in one level run at once):
- L0: T1, T3, T6.
- L1: T2, T4, T5.
- L2: T7.
- L3: CK.

**Critical path:** T1, T5, T7, CK by weight (`fugaro plan graph` counts tasks and prints the equal-length path through T2). T6 and the L1 tasks other than T5 run beside it.

**Shared files:** only T5 edits `internal/orchestrator/tick.go`; only T3 edits `internal/credsvc/server.go`; only T7 edits `internal/cli/orchestrator_run.go`; T6 owns the three documents. No level holds two writers of one file.

| File | Writer | Level |
| --- | --- | --- |
| `internal/gitprov/labels.go`, `internal/gitprov/github/labels.go` | T1 | L0 |
| `internal/creds/labelmint.go`, `internal/credsvc/labelmint.go`, `server.go` | T3 | L0 |
| `docs/git-providers.md`, `SECURITY.md`, `website/content/security.md` | T6 | L0 |
| `internal/gitprov/fake/labels.go`, `internal/gitprov/bitbucket/labels.go` | T2 | L1 |
| `internal/creds/labelmint_service.go` | T4 | L1 |
| `internal/orchestrator/labelsync.go`, `labelplan.go`, `tick.go` | T5 | L1 |
| `internal/cli/orchestrator_labels.go`, `orchestrator_run.go` | T7 | L2 |
| `scripts/live-checks/check640.sh`, `docs/gcp-live-checklist.md` | CK | L3 |

**Considered and declined.** A contract task for the `Labeler` interface and the mint shape (no gain); a GitLab adapter (D2431); a webhook receiver (D2434); `fugaro setup` or `update` pre-creating the labels (O2, R6); writing labels from the runner (D2432); a separate label credential store (D2436).

## Checks the plan takes

**Check 640** (reserved block 640-649): the labels on a real pull request on the dogfood installation, run by the owner (CK).

## After-deploy steps

None for a person beyond `fugaro update`, which already redeploys the credentials service and re-renders the daemon's unit; the new release's `### For operators` says exactly that (T6). **One conditional owner step:** if U1 shows the GitHub App lacks the permission for label writes, the owner raises it on the App's settings page; that cannot be done by an agent. Check 640 reports it.
