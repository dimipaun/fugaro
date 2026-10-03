# M9e: Early Draft PR Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Briefs to implementers stay short: the task text plus the Review Focus line that applies.

**Goal:** A run opens a **draft** PR at its first verified push, keeps its description's status section current at each stage boundary, and flips it to ready at the end only by the §4.2 rule. Reviewers and labels are applied only when the PR becomes ready, so nobody (EdgeWeb's real reviewer included) is notified by a draft. Finalize still guarantees a PR for every run that has a branch.

**Spec:** [m9-spec-v3-reconciliation.md](../design/m9-spec-v3-reconciliation.md) (M9e), [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) D15 (revised), [v1.md](../design/v1.md) §4.1, §4.2, new §4.2a (written with this plan), §4.5, §4.6, §14 backlog (Bitbucket marker).

## What is built and what this reuses

| Need | Already there |
|---|---|
| Create or update a PR | `Provider.EnsurePR(PRSpec)`: find-by-branch then create or draft-toggle; `Number != 0` updates draft state only, returns `ErrPRNotOpen`; `PartialError` for labels/reviewers (`gitprov.go`, `github`, `bitbucket`, `fake`) |
| Draft support | GitHub: `draft` on create, GraphQL convert both ways, `[DRAFT] ` title fallback when refused (`draftUnsupported`). Bitbucket: `draft` on create and PUT both directions, verified live 2026-09-27 |
| Runner finalize | `runner.finalize`: commit leftovers, empty commit if none ahead, `Push`, `PushedHead` saved, `ensurePR` (3 attempts, `RetryDelay`), `Decide`, outcome/status, report comment, `storeReport`; follow-up path by number (`pushFollowUp`, `endUnchanged`, `reportPosted`) |
| Run record | `runstore.Record.PR *PRRef{Number,URL}`, `Outcome ready|draft|none`, `PushedHead`; `r.save(ctx)` writes `result.json` |
| Agent loop | `agentLoop`: `implement`, then `review`/`fix` rounds; `r.stage`, `r.capReached`, `r.rec.Reviews`; verify records via `verify.Records(StateDir)`, `latestVerifiedTest`, `Decide` (`outcome.go`) |
| PR text | `prText()` from `pr.md` or the task; clip bounds (`maxTitleRunes`, `maxBodyBytes`); the agent has no git token |
| Report and markers | `FollowUpReport`, `CostLine` (notional vs model dollars), `gitprov.ReportMarker`/`FugaroRun`/`StripMarkers` (HTML comment form) |
| Config | `git.pr.labels`, `git.pr.reviewers` (`internal/config`) |
| Rendering | `fugaro diagnose` prints `PR:`; `ls` shows the PR column |

**Gaps (why this milestone has work):** no provider method edits a PR's title or body; `PullRequest` (PRInfo) does not return the body; reviewers and labels are applied at creation, even for a draft; `finalize` is the only place a PR is created, after the last stage.

## Decisions already made (binding)

User rulings: open the DRAFT PR after the first verified push; update its description at each stage boundary; flip to ready at the end only if the outcome rule says ready; **reviewers are added only when the PR is made ready** (a draft carries none); a halt before the branch exists opens no PR (D9, exit 0); a halt after the first push leaves the draft with a halted comment; the sweeper/diagnose marks stale drafts of crashed runs; follow-ups (`--pr N`) keep working by number; never merge PRs on EdgeWeb; live tests only on the Bitbucket sandbox `edgeappinc/fugarosandbox` (no reviewers there). No backward compatibility (memory): finalize's old "reviewers on a draft" behaviour is removed, not kept.

## Rulings (settled here; each says what it costs if wrong)

**E1. When the draft opens.** At the end of an `implement` or `fix` stage that returned ok (not capped, not cancelled, not halted), if `verify.Records` holds a `test` record with `head_sha == HEAD`, `clean_tree`, `passed` (the same test as `Decide` rule 1, no review needed), and no PR number is recorded: `Push`, set `PushedHead`, `EnsurePR(Draft: true)`, record `PR{Number,URL}` and `r.save` **before anything else**. Later verified boundaries push the new tip and update the status; unverified ones push nothing, so the remote branch is always a verified state. First follow-up: no early open (the PR exists). *If wrong:* a branch pushed unverified; pinned by `TestNoPushWithoutVerifiedTest`.

**E2. First round fails verification.** No push, no PR. A later `fix` that verifies opens the draft then. If none ever does, finalize opens a **draft** with the failure explanation, exactly as today. "Always ends in a PR" is finalize's guarantee for any run that has a branch, unchanged; M9e only moves the opening earlier when there is something verified to show. A run halted before the branch exists still opens none (D9). *Pinned by* `TestFirstRoundFailsThenFinalizeOpensDraft`.

**E3. Single writer.** Only the runner creates or edits the PR; the agent environment has no git token (existing test) and `pr.md` is data the runner reads. Nothing in the system prompt changes. A PR the agent somehow opened is found by branch (existing find) and adopted into the record.

**E4. Idempotency.** The PR number is saved in `result.json` right after creation. Every later write (status, ready flip, reviewers) is by number. Crash between create and save: the retry's `EnsurePR` finds the PR by branch (existing behaviour) and saves the number then. Finalize order: by number if recorded, else by branch. Recorded PR no longer open (a human closed or merged it): no recreation, **no second PR**; the branch is pushed, nothing is posted, the run ends `failed`, outcome `none`, reason `PR #N was closed during the run; the branch was pushed`, the follow-up rule (§4.1) reused through `endUnchanged`. *If wrong:* a duplicate PR; pinned by `TestFinalizeNeverCreatesSecondPR`, `TestClosedMidRunEndsNone`.

**E5. Title and description.** Title: the task title (`pr.md` first line, else the task's first line), **no prefix** while in progress (the draft badge says so; a prefix must be stripped again at ready and fights human edits). Provider without real drafts keeps the existing `[DRAFT] ` title prefix, stripped at ready. Body: the description (`pr.md` body, else the task text) then one **status section**:

```
[//]: # (fugaro:status begin)
### Fugaro status
**Running** · stage `review` round 2 · verify: passed on `abc1234` · model cost $1.20 · updated 14:03Z
Run `20261003-...`  (a stale time here means the run died: `fugaro diagnose <run>`)
[//]: # (fugaro:status end)
```

Link-reference definitions render nothing on GitHub and, per the backlog note, on Bitbucket (**unverified**, ⚠ live item 3). Fallback if Bitbucket shows them: the visible `### Fugaro status` header stays and the markers become `<!-- -->` on GitHub only (adapter-chosen). The parser (`ReplaceStatus`, `StripStatus`) accepts both forms. The report comment and its HTML marker are untouched, so `FugaroRun`/`StripMarkers` keep working; the status section is stripped from any body a follow-up quotes.

**E6. Update mechanics.** At a stage boundary: `PullRequest` read (now returns `Body`, `Title`), replace only the section, `UpdatePR(number, body)`. A human edit outside the markers is kept; a body with no markers gets the section appended. **At most one update per 20 s** (boundaries closer together coalesce; finalize always writes). **A failed update is a warning, never a failed run**: log, count; after 3 consecutive failures stop updating until finalize. Rate limit (403/429 with `Retry-After`): treated as a failure, no sleep. At finalize the description is replaced from `pr.md` only if the body outside the markers still equals what the runner last wrote (digest kept in memory and in `PRRef.Desc`), else only the section changes.

**E7. Finalize.** Same steps (commit, empty commit, push, `PushedHead`), then: PR recorded: `PullRequest` read, `UpdatePR` (final section), `EnsurePR{Number, Draft: !ready}`; not recorded: `EnsurePR` by branch creates it (draft or ready), as today. If ready, `ApplyReady` (E8). A failing description update at finalize is a warning; a failing draft/ready flip keeps today's retry and `PartialError` logic (a PR may never look more ready than the outcome). The report comment and `storeReport` are as today. The status section's final text is the run summary: outcome, reason, verify, reviews, cost.

**E8. Reviewers and labels at ready only.** New `Provider.ApplyReady(ctx, pr, reviewers, labels)` called after a successful flip to ready: GitHub `requested_reviewers` plus labels; Bitbucket `PUT {reviewers}` (labels unsupported, warned once as today). Idempotent, so a retry is safe. A failure here is a `PartialError`-style warning in the report: the PR stays ready (the outcome is the run's verification, not the notification). A follow-up that flips draft to ready applies them again; one that finds a ready PR does not. Draft outcomes get **neither** (a change from today). Finalize's old creation-time reviewers go away.

**E9. Providers without drafts.** GitHub private repositories on free plans refuse `draft` (422); the adapter already opens a normal PR with the `[DRAFT] ` prefix and reports `PR.Draft = false`. Reviewers still wait for ready, but a normal PR may auto-request CODEOWNERS reviews: unavoidable once opened. New `git.pr.early_draft: true|false` (default true): `false` skips E1 and keeps today's finalize-only PR for repositories known to lack drafts. The runner logs and records `draft_fallback` (shown in diagnose) when the prefix fallback happens. Bitbucket: real drafts (verified).

**E10. Halts, cancel, crash.** Halt (budget) or cancel after the first push: finalize runs, the section is rewritten `Halted: <reason>` or `Cancelled`, the draft stays a draft and the report comment is posted as today (status `halted`/`cancelled`). Halt before the push or branch: no PR (D9), exit 0. A hard crash leaves a draft whose status line says `Running` with an old time; the history sweeper has no git credentials and does not edit PRs, it records `crashed` on the run (existing); `fugaro diagnose`/`ls` render `PR #N draft, run crashed: the draft is stale; continue with fugaro run --pr N`. `fugaro cancel --now` skips finalize: the draft stays as in a crash.

**E11. Cost in the body.** The status section shows model dollars as `$1.20 of $5 cap` for `api` and `notional $1.20 (not billed)` for `oauth`, using `CostLine`/`Cost`; compute is not shown (it is reported by `fugaro report`, M9d). Never a number that is not in `result.json`.

**E12. Follow-ups.** They never open a PR and never rewrite a description. If the body already holds a status section they update it at stage boundaries by number (same rules, E6) and rewrite it at finalize; if not (a PR from before M9e), they change nothing but the draft state and post the report, as today. They do not push mid-run (the stricter `PushExisting` stays finalize-only).

**E13. Rebase before opening.** Out of M9e (it needs a bounce back to the agent): kept in the v3 open items.

## Review Focus

The runner's PR flow touches every run's PR (user-facing risk), so **T3 gets its own review**; everything else is reviewed once on the branch at the end (user's token-economy rule).

1. **A reviewer notified by a draft, or never notified when ready.** T1/T3: `TestDraftCreateSendsNoReviewers`, `TestApplyReadyOnlyAfterReadyFlip`, `TestDraftOutcomeGetsNoReviewersOrLabels`, `TestFollowUpFlipAppliesReviewers`, `TestReviewerFailureKeepsReady`.
2. **A duplicate, closed or overwritten PR.** `TestFinalizeNeverCreatesSecondPR`, `TestClosedMidRunEndsNone`, `TestCrashAfterCreateRecoversByBranch`, `TestHumanEditOutsideMarkersKept`.
3. **A status update that fails the run or blocks finalize.** `TestStatusUpdateFailureOnlyWarns`, `TestThreeFailuresStopUpdates`, `TestFinalizeStillCreatesAfterStatusFailures`, `TestCoalescesWithin20s`.
4. **Ready when it should not be.** `TestNoPushWithoutVerifiedTest`, `TestPartialErrorNeverReportsReady` (existing, must still pass), `TestDraftFallbackNotReadyLooking`.
5. **Hostile or forged text** in the body: `TestStatusStripsForgedMarkers` (an agent's `pr.md` carrying the status markers or a report marker), redaction of the section.

## File Structure

| Path | Role |
|---|---|
| `internal/gitprov/gitprov.go` | `PRInfo.Body/Title`, `PRUpdate`, `UpdatePR`, `ApplyReady`, `PR.DraftFallback`; `EnsurePR` creates a draft without reviewers or labels |
| `internal/gitprov/{github,bitbucket,fake}` | the new methods and the no-reviewers-on-draft change |
| `internal/gitprov/status.go` | `ReplaceStatus`, `StripStatus`, both marker forms; pure |
| `internal/runner/prflow.go` (new) | `openDraft`, `statusUpdate` (coalesce, failure counter), finalize helpers |
| `internal/runner/runner.go`, `followup.go` | call sites in `agentLoop` and `finalize` |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | `PRRef.Desc`, `draft_fallback` |
| `internal/config` | `git.pr.early_draft` |
| `internal/cli/diagnose.go`, `ls` | stale-draft and fallback rendering |
| `docs/design/v1.md`, `README.md`, `docs/gcp-live-checklist.md` | docs and the live check |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on |
|---|---|---|---|
| T1 gitprov: `UpdatePR`, `PRInfo.Body`, `ApplyReady`, drafts without reviewers; github, bitbucket, fake | M | A | none |
| T2 status section: `ReplaceStatus`/`StripStatus`, both marker forms | S | B | none |
| T3 runner PR flow: early open, status updates, finalize by number, ready-time reviewers, follow-up, config key **(own review)** | L | A | T1, T2 |
| T4 diagnose and ls: stale draft, fallback; result schema | S | B | T3 |
| T5 docs: README, config docs, checklist item | S | B | T3 |
| T6 live verification on the sandbox | S | controller | all |

### Task 1 (M, lane A): Provider surface

**Files:** `gitprov.go`, `github/github.go`, `bitbucket/bitbucket.go`, `fake/`, `gitprov_test.go`, adapter tests with `httpfixture`.

- `UpdatePR(ctx, number, PRUpdate{Title, Body *string})` (GitHub `PATCH /pulls/N`; Bitbucket `PUT` with the title kept, since PUT needs it); `PullRequest` returns `Body` and `Title`; `ApplyReady(ctx, number, reviewers, labels)`; `EnsurePR` creation and find-update stop sending reviewers and labels (a new-PR spec with `Draft: false` still applies them, so a ready-at-creation finalize works through `ApplyReady` anyway: one code path). The fake records every call so the runner tests can assert order.
- [ ] **Failing tests first:** `TestDraftCreateSendsNoReviewers`, `TestUpdatePRChangesBodyOnly`, `TestApplyReadyIdempotent`, `TestBitbucketReadyPutCarriesReviewers`, `TestPullRequestReturnsBody`, `TestGitHubDraftFallbackReported`.
- [ ] Commit: `gitprov: update PR text, reviewers at ready`

### Task 2 (S, lane B): Status section

**Files:** `internal/gitprov/status.go`, `status_test.go`.

- `ReplaceStatus(body, section) string` (replaces between markers, appends when absent, one section only), `StripStatus`; both marker forms; section text rendered by the runner. Forged markers in `pr.md` are removed before the runner's own is added.
- [ ] **Failing tests first:** `TestReplaceStatusAppends`, `TestReplaceStatusReplacesOnlySection`, `TestStatusBothMarkerForms`, `TestStatusStripsForgedMarkers`, `TestStatusSurvivesHumanEdits`, `TestMarkersLeaveReportMarkerAlone`.
- [ ] Commit: `gitprov: the PR status section`

### Task 3 (L, lane A): Runner PR flow **(critical: own review)**

**Files:** `internal/runner/prflow.go`, `runner.go`, `followup.go`, `internal/runstore`, `internal/config`, tests against the fake provider and fake `claude`.

- `afterStage(ctx)` in `agentLoop` (E1): verified-boundary check, push, `openDraft`, record and save the number, then `statusUpdate` (E6). `finalize` per E7, E4, E8, E10; follow-up per E12; `git.pr.early_draft` (E9). The section text from `rec` (stage, round, last verify, `CostLine`).
- [ ] **Failing tests first:** the names in Review Focus 1-5, plus `TestOpensDraftAfterFirstVerifiedStage`, `TestFirstRoundFailsThenFinalizeOpensDraft`, `TestHaltBeforeBranchOpensNoPR`, `TestHaltAfterPushLeavesDraftWithComment`, `TestCancelFinalizesDraftWithNote`, `TestEarlyDraftFalseKeepsFinalizeOnly`, `TestFollowUpWithoutSectionChangesOnlyDraftState`, `TestStatusShowsNotionalForOAuth`.
- [ ] Commit: `runner: early draft PR, status section, reviewers at ready`

### Task 4 (S, lane B): diagnose and ls

**Files:** `internal/cli/diagnose.go`, `ls` rendering, `schemas/result.schema.json`.

- A run with a PR and status `crashed` (or still `running` long after its heartbeat) renders the stale-draft line; `draft_fallback` shown. Strings pass `oneLine`.
- [ ] **Failing tests first:** `TestDiagnoseStaleDraft`, `TestLsShowsDraftInProgress`, `TestSchemaAcceptsPRDesc`.
- [ ] Commit: `diagnose: stale draft PRs`

### Task 5 (S, lane B): Docs

**Files:** `README.md`, the config reference for `git.pr.early_draft`, `docs/gcp-live-checklist.md` (new check), `docs/design/v1.md` backlog (link-reference markers).

- [ ] Commit: `docs: early draft PRs`

### Task 6 (S, controller): Live verification (sandbox only)

Bitbucket sandbox `edgeappinc/fugarosandbox`, no reviewers on it, the user's go-ahead and task text for each run. Record every `FACT` in the PR. EdgeWeb is never used and never merged.

1. Unit and fake runs green; `fugaro init --repo` so the job carries the new runner.
2. **⚠ CONFIRM, one sandbox run on a task that passes**: the draft appears after the first verified push (not at start), the status section updates per stage (note latency, `FACT`), the PR flips ready at the end, the body keeps the description.
3. **⚠ CONFIRM, the markers**: does Bitbucket render `[//]: # (fugaro:status ...)` invisibly in a PR description (A2)? If not, switch to the visible form before merging T3.
4. **⚠ CONFIRM, reviewers**: temporarily add a throwaway reviewer account to the sandbox config (user's call, an account they own): none on the draft, requested at ready, and does the draft-to-ready PUT with `reviewers` notify exactly then (A1)?
5. **⚠ CONFIRM, a failing task** (the user's text, for example one the verify cannot pass): no early PR, finalize opens a draft with the reason, no reviewers.
6. **⚠ CONFIRM, a budget halt** (a tiny cap on the sandbox repo) after the first push: the draft stays, `Halted:` in the section and the comment.
7. **⚠ CONFIRM, `fugaro cancel` mid-run** after the first push, then `cancel --now` on a second: the first leaves a draft with a cancelled note, the second a stale draft; `diagnose` says so.
8. **⚠ CONFIRM, a follow-up** (`fugaro run --pr N`) on the draft and on the ready PR: updates the same PR, section kept.
9. Close the sandbox PRs afterwards.

*Rollback:* revert the runner PR and re-run `fugaro init --repo`; PRs already opened are ordinary PRs. `git.pr.early_draft: false` is the per-repo switch.

## Unverified assumptions

- A1. GitHub requests reviews, and notifies, only when a PR is ready (and not for a draft with explicit requested reviewers); Bitbucket notifies reviewers when a draft becomes ready and not before. Not verified: step 4 settles Bitbucket; GitHub is untested (no sandbox), so reviewers are applied at ready by our own call and never rely on the provider's rule.
- A2. Bitbucket renders `[//]: # (...)` link references as nothing in PR descriptions.
- A3. Bitbucket `PUT` of `reviewers` on a ready transition keeps the description and the draft state in one request.
- A4. GitHub's `PATCH /pulls/N` with only `body` leaves title, draft and reviewers alone (documented, not run live).
- A5. One body read plus one write per boundary stays far under provider rate limits (about ten boundaries per run).
- A6. A free-plan private GitHub repository refuses `draft` with a 422 mentioning "draft" (already handled; shape from docs).

## Risks

- **The most-touched path changes.** Every run's PR passes through T3; hence its own review and the fake-provider order assertions.
- **A human edits the description mid-run.** E6 keeps edits outside the markers; a deleted marker pair means the section is appended again.
- **A reviewer notified early on a provider without drafts** (E9): `early_draft: false`.
- **Closed-mid-run PR** ends the run as `failed/none` rather than reopening: the safe direction, same as follow-ups.

## Open questions for the user (defaults in italics)

1. **Title prefix while in progress.** *Default: none* (the draft badge says so; a prefix fights edits). Alternative `[Fugaro] <title>` stripped at ready.
2. **Labels at ready only (your ruling) or at draft too?** *Default: ready only,* as ruled; labels notify nobody, so adding them to a draft would be harmless and helps filtering.
3. **`git.pr.early_draft` (E9).** *Default: add it, default true.* Alternative: no key, accept early normal PRs on drafts-less repositories.
4. **Reviewer re-request on a follow-up's draft-to-ready flip.** *Default: yes.*
5. **Sweeper edits PRs?** *Default: no* (the history job has no git credentials; adding them widens a privileged job). Stale drafts are marked by their own status time and `diagnose`.
6. **Rebase before opening the PR (v3 open item).** *Default: defer past M9e.*
7. **A PR closed by a human mid-run (E4).** *Default: `failed`, outcome `none`, branch pushed, nothing posted.*

## Execution

Subagent-driven. Lanes A (T1, T3) and B (T2, then T4, T5). Only **T3** gets its own review (the runner's PR flow is user-facing risk for every run's PR); everything else is reviewed once on the whole branch, with the batch workflow's round-2 review. T6 is controller-only, one question per step.
