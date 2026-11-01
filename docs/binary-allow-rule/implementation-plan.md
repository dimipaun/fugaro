# The binary allow rule and the no-verdict label: implementation plan

**Status: plan only; nothing is implemented; no task approved; the owner decides.** Written to
[spec.md](spec.md) and [reconciliation.md](reconciliation.md) **as recommended**, so an answer of
"as recommended" on D981–D996 needs no rewrite here.

## 1. Order, and what this plan does not own

Two works, and they are independent of each other:

- **NV — the label** (NV0–NV2): three tasks, no dependency on anything but the 0.14.2 runner group.
  It can land first, on its own, and it should: it closes a falsehood on a wire five clients read,
  and it has nothing to do with binaries.
- **AR — the rule** (AR0–AR7): the runner half (AR0, AR2–AR6) is likewise independent — it is tested
  against a policy built by hand — and only **AR1, the publish**, needs another design's work: the
  `initflow.Policy` stage that [docs/settings-commands](../settings-commands/implementation-plan.md)
  **S3** turns into a publish. **AR1 is written to sit immediately after S3** and must not be
  reordered before it.

**This plan owns no other design's files**, and it shares three with the sibling plans:

### 1.1 The files the sibling designs share

| File | Who writes it, in order | What keeps it one file |
| --- | --- | --- |
| `internal/runner/runner.go` | **AR3, AR4, AR5** (finalize's switch, the two hunks that call the checks, the refusal text) | **The 0.15.0 rulings give this file one writer across the planning runs**, and each hunk here is a few lines inside `finalize`. The implementing run that lands AR3–AR5 lands them in one slot and rebases once; a second writer in the same slot is what the ruling forbids |
| `internal/cli/project_cmd.go` | the settings design's **S3** (`buildProjectPolicy` gains the agent refusal), then **AR1** (the same function gains `binaries.*`) | **one `buildProjectPolicy`**, pinned by name already (`internal/cli/docs_project_centric_test.go:2326`) and guarded by S3's `TestUpdateStageAndTheExplicitDoorBuildTheSameNode`. AR1 adds a member to a function S3 has already reshaped; it never writes a second build path |
| `internal/cli/init_stages.go` | the settings design's **S3** (the publish becomes part of the `Policy` stage's work) | AR1 writes **no line of it**: the stage, the ETag guard and the generation guard are S3's, and the binaries block rides the node S3 already publishes. This is the whole reason AR0–AR6 can land before AR1 |

`internal/policy/policy.go` and `internal/policy/merge.go` (AR2) are shared with the governance work
only in the sense that they live in a package with a seam (`hook.go`); AR2 adds a field and a merge
line beside `AllowedModels`, and moves nothing across the seam.

## 2. Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2`, never a whole `internal/cli` or `internal/runner` package run; `go test ./internal/eeboundary/...`
where the change touches `internal/policy`; hermetic throughout — `internal/gcpfake`,
`internal/testutil`, a fake Realtime Database and `t.TempDir()`, no network, no home directory, no
cloud. Every task starts with a **red step**: the named test fails for the reason the task exists
before a line of it is written.

---

## NV. The label

### NV0. The status value on the record and on the wire (D994, D996)

The vocabulary moves in five places, and four of them are this repository's (F13). This task moves
the four and leaves the fifth (the console, E2) named.

- **Files (6):** `internal/runstore/runstore.go` (`StatusUnreviewed`, its comment, and a
  `Statuses` slice so the set has **one** spelling the way `rigor.Modes` and `tier.Tiers` do),
  `schemas/result.schema.json` (the `status` enum, line 16), `internal/digest/digest.go` (the
  `statuses` closed set reads the slice instead of restating it), `internal/runstore/runstore_test.go`,
  `schemas/schemas_test.go`, `internal/digest/digest_test.go`.
- **Red step:** `TestResultSchemaStatusesAgreeWithGo` fails — the schema's enum does not carry
  `unreviewed`, and the Go list does not either; the two fail together from the first edit, which is
  the property the test exists for (the shape `TestResultSchemaHaltReasonsAgreeWithGo` and
  `TestTaskSchemaRigorRequestMatchesTheGoRules` already use).
- **Tests:** `TestResultSchemaStatusesAgreeWithGo`, `TestDigestRendersUnreviewedAsItself` (a record
  whose status is the new value renders the word, not `other`), and `TestRunstoreStatusesAreTheWireVocabulary`
  (the list is exactly the constants, so a constant added without a list edit is red).
- **Rejected:** the literal list `internal/digest` carries today, copied into the schema test —
  two lists that drift, and the drift is invisible until a record renders `other`.

### NV1. `Decide` answers with a class, and finalize writes the label (D995)

- **Files (4):** `internal/runner/outcome.go` (the typed answer; `KindUnreviewed` for both
  no-verdict cases, F6), `internal/runner/runner.go` (the one `case` in finalize's switch),
  `internal/runner/outcome_test.go`, `internal/runner/runner_test.go` (one new test function).
- **Red step:** `TestUnreviewedRunIsNotFailed` fails — a run whose senior round answered nothing is
  recorded `failed` at `runner.go:3653` today, which is the lie this task removes.
- **Tests:** `TestUnreviewedRunIsNotFailed` (a record whose last senior round is `verdict: none`
  ends `unreviewed` / `draft`, and the reason still names the round), `TestNoSeniorRoundIsUnreviewed`
  (`last == nil` takes the same label), `TestDecideClassMatchesTheReadiness` (a `ready` answer is
  never `KindUnreviewed` and vice versa), and `TestFindingsBlocksStayFailed` (a `changes` verdict
  outside the mode's tolerance still ends `failed` — D991's other half).
- **Not in this task:** the readiness rule itself, and which round `Decide` reads for a
  first-line-only mode (F7, D723, L4.2 — another design's).

### NV2. The readers that list runs (D996)

- **Files (4):** `internal/watch/finished.go` (`isFailure` does **not** gain the value: nothing
  failed), `internal/userstats/userstats.go` (the census keeps it in `Other`), and their two test
  files.
- **Red step:** `TestWatchDoesNotCallAnUnreviewedRunAFailure` fails under the naive edit that adds
  the new status to `isFailure`'s `case` list, and `TestUserStatsCountUnreviewedAsOther` pins the
  census so a later task that adds a counter changes it deliberately.
- **Tests:** those two, plus `TestFinishedRowCarriesTheStatusWord` (the row's status is the value
  itself, so a watcher reads `unreviewed` rather than a blank).
- **External:** the console's and the phone's chips are **E2**. Until they ship, the value reaches
  them unchanged (`status` is a contract key in `console/src/source/redact.ts`).

---

## AR. The rule

### AR0. The node member, its validation, and the version (D981–D985, D990)

The foundation: the canonical shape and the one place it is validated. It is written against the
node only — nothing reads it yet — so it lands without waiting for anything.

- **Files (6):** `internal/projectpolicy/binaries.go` (new: the `Binaries` carrier, its decode, its
  validation — the four path-glob checks of `rigor.Area.Validate` / `rebuild.paths`, the cap's
  positivity and ceiling), `internal/projectpolicy/policy.go` (the member, `knownTopLevelKeys`),
  `internal/projectpolicy/version.go` (`Version = 5`, `VersionSince[5] = "0.15.0"`), and the three
  test files `binaries_test.go` (new), `policy_test.go`, `version_test.go`.
- **Red step:** `TestBinariesAllowPatternsAreRefusedAtDecode` fails — `website/public/**.png`,
  `/etc/passwd`, `..`, and an empty entry are all accepted by a decoder that has no such member, and
  the task exists to refuse them where they are written.
- **Tests:** `TestBinariesAllowPatternsAreRefusedAtDecode`, `TestBinariesAbsentAdmitsNothing`,
  `TestBinariesWrittenEmptyAdmitsNothing` (the two spellings of D987, told apart),
  `TestBinariesMaxBytesDefaultsAndLowerOnly`, `TestVersionSinceCoversEveryVersion` (already exists;
  it fails until the row lands), and `TestPolicyUnknownKeysStillListsBinariesForAnOlderReader`
  (the wrong direction: a version-4 reader must list a version-5 node's member, never decode past
  it silently — the bump is what makes the refusal `RunnerSkew` instead, D990).
- **Bounds:** entries bounded in count and length the way `rigor.MaxAreas` / `MaxAreaPattern` bound
  areas, because the list reaches the run record and the pull request body.

### AR1. The owner's carrier, the publish, and the convergence (D987, D990). **Needs S3**

- **Files (8):** `internal/localcfg/localcfg.go` (`BinariesAllow`, `BinariesMaxBytes`, validated at
  load — a misspelt pattern is a refusal, never a rule that quietly admits nothing),
  `internal/cli/sharedcfg.go` (`sharedForbidden` gains `binaries`), `internal/cli/project_cmd.go`
  (`buildProjectPolicy` fills the member; silence keeps the node's list, a written empty block clears
  it — `review_floor`'s rule, D987), `internal/cli/init_policy_migrate.go` (the version-4 → 5
  republish path the stage already owns), and the four test files
  `internal/localcfg/localcfg_test.go`, `internal/cli/project_policy_test.go`,
  `internal/cli/sharedcfg_test.go`, `internal/cli/update_converge_test.go`.
- **Red step:** `TestInstallationAtThePreviousVersionConvergesBinaries` fails — an installation whose
  published node is version 4 ends `fugaro update` with a version-5 node carrying its `binaries`
  block, and today the stage publishes nothing at all (it does not exist until S3).
- **Tests:** that one, `TestUpdatePolicyStagePublishesTheBinariesBlock`,
  `TestPublishKeepsTheBinariesListWhenTheLocalFileSaysNothing`,
  `TestPublishClearsTheBinariesListOnAWrittenEmptyBlock`,
  `TestConfigPublishRefusesTheBinariesKey` (the shared config is a launcher-writable file, F11), and
  `TestLocalConfigRefusesAnUnreadableBinariesPattern`.
- **Hermetic:** a fake Realtime Database, an ETag, `t.TempDir()`. No apply, no deploy, no cloud.
- **Carries its own `### For operators` note, in its PR body, for the release PR to copy:**
  "`fugaro update` republishes the project policy as version 5, which carries `binaries` — the paths
  a run may add a binary at. Nothing is required of an operator beyond `fugaro update`; the runner
  image is refreshed by the same command, and a project that sets no list keeps refusing every
  uncommitted binary exactly as before." `docs/releases/**` is another stream's file, so the wording
  travels in the PR body and the release PR copies it.

### AR2. The ceiling, and the direction the merge already has (D986, F10)

- **Files (5):** `internal/policy/policy.go` (`Layer.BinariesAllow`, `Layer.BinariesMaxBytes`),
  `internal/policy/merge.go` (the intersection of allow-lists, the smaller cap — `AllowedModels` and
  `TightenInt` already do both), `internal/runner/policy.go` (the ceiling builder, beside
  `policyCeiling`'s `ReviewFloor`), and the two test files `internal/policy/merge_test.go`,
  `internal/runner/policy_test.go`.
- **Red step:** `TestBinariesListMergesByIntersection` fails — a layer below the ceiling that adds a
  path grows the effective list, and the task exists to make that impossible.
- **Tests:** `TestBinariesListMergesByIntersection`, `TestBinariesCapTightensOnly`,
  `TestPolicyCeilingCarriesTheBinariesList`, `TestRepositoryKeyCannotWidenTheList` (the runner's
  resolved policy is what the check reads, and a `fugaro.yaml` that named the key does not load at
  all — AR2's half of P2).

### AR3. finalize's untracked check, widened by the rule (D988)

- **Files (3):** `internal/runner/stray.go` (an admitted path is not a stray; the reason gains the
  cap and the rule), `internal/runner/runner.go` (the hunk that hands the check the run's policy),
  `internal/runner/stray_test.go` (today's `finalize_binary_test.go` on #1476, renamed or extended).
- **Red step:** `TestAnAdmittedImageIsCommitted` fails — a run that leaves a new `.webp` under an
  admitted path is refused and ends a draft, which is item 1 in one test.
- **Tests:** `TestAnAdmittedImageIsCommitted` (the file is in the leftover commit and the run is not
  failed), `TestAnOverSizedAdmittedImageIsRefused` (the cap, D983),
  `TestAnUnadmittedBinaryIsStillRefused` (#1476's behaviour, unchanged, and the test that keeps this
  change from becoming a loosening), `TestAnAdmittedPathOutsideItsPatternIsRefused` (the glob is
  matched, not prefix-compared).

### AR4. The residual: the run's own diff (D989, F3, F4, F14)

- **Files (4):** `internal/runner/stray.go` (the diff reader and the shared predicate),
  `internal/runner/diffbase.go` (`--name-status` beside the numstat read, so an **added** path is
  known), `internal/runner/runner.go` (the hunk that runs it), and
  `internal/runner/stray_test.go`.
- **Red step:** `TestASelfCommittedBinaryFailsTheRun` fails — an agent that commits a payload in
  implement, and one that stages it with `git add -N` first, both reach the branch with no refusal
  today.
- **Tests:** `TestASelfCommittedBinaryFailsTheRun`, `TestAGitAddIntentToAddBinaryFailsTheRun` (F4),
  `TestAModifiedTrackedBinaryIsNotRefused` (§2.1's deliberate tolerance),
  `TestTheAutofixCommitIsChecked` (`gate.go`'s path), `TestTheDigestStepCommitIsChecked`
  (`digest.go`'s path), and `TestTheRefusalNamesThePathAndTheSize`.
- **Note:** the run is refused; its commits are not rewritten (D988).

### AR5. What the agent sees (D992, D993)

- **Files (4):** `internal/runner/prompts.go` (the implement prompt names the admitted paths and the
  cap), `internal/runner/stray.go` (the refusal sentence: the rule, the two readers who can act, and
  that a repository cannot widen it), `internal/runner/prflow.go` (the bounded `binaries: …` status
  line, D993), and `internal/runner/prompts_test.go` (plus a case in `stray_test.go`).
- **Red step:** `TestTheImplementPromptNamesTheAdmittedPaths` fails — the prompt says nothing about
  a rule the run is refused by.
- **Tests:** `TestTheImplementPromptNamesTheAdmittedPaths`,
  `TestTheRefusalSaysARepositoryCannotWidenTheRule`, `TestTheStatusLineIsBounded` (a run that
  admitted many files gets a count, not a wall of paths).

### AR6. The end-to-end test (P1, P5)

- **Files (3):** `internal/runner/stray_test.go` (or a new `binaryrule_e2e_test.go` in the same
  package), `internal/runner/runner_test.go` (one function), and `internal/gcpfake` only if the
  policy needs a node (a policy built by hand needs none).
- **Red step:** `TestARunThatAddsAnAdmittedImageIsReady` fails — the whole property, end to end: an
  admitted image is committed, the tests pass, the review ships, and the run is `succeeded` /
  `ready`.
- **Tests:** `TestARunThatAddsAnAdmittedImageIsReady` and its mirror
  `TestARunThatLeavesABuildProductIsADraft`, which is #1476's refusal asserted through the same
  harness so the two answers cannot drift.

### AR7. Documentation, skill and release notes

- **Files (5):** `docs/project-layer.md` or `docs/policy-migration.md` (the one place the node's
  members are listed for an operator — whichever the publish doc already is), `plugin/skills/**`
  only if a skill names a policy member (it does not today; this task checks rather than assumes),
  and the release-notes wording carried in the PR body for `docs/releases/vX.Y.Z.md`'s
  `### For operators` section, which the release PR writes.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`, and the plugin's own
  `go test ./plugin/...` if a skill changed.

## 3. External dependencies

| # | What | Why it blocks | What this plan does meanwhile |
| --- | --- | --- | --- |
| **E1** | **PR #1476** (the 0.14.2 runner group) | AR3 widens a check that only exists there, and NV1's second no-verdict case is #1476's | AR0 and AR2 are written against the node and the merge and land without it; AR3–AR6 wait for it; NV0 and the first half of NV1 are independent |
| **E2** | **`console/**` and `mobile/**`** | a new status value is a chip and a label in two clients this plan does not own | the value reaches both unchanged (`status` is a contract key, so the person pass never rewrites it) and `internal/runview` passes a terminal status through; the console task is a one-row change to its status table, filed by the console's own stream |
| **E3** | **the settings design's S3** | AR1's publish rides the `Policy` stage S3 builds | AR0, AR2–AR6 need no publish to be tested: a policy built by hand is the fixture. AR1 is the last AR task and sits after S3 in the slot below |

## 4. The recommended slot

The 0.15.0 line is planned by three designs at once — `settings`, `rigor`, `models` — plus the
review-levels work and this one. The order below is this design's recommendation, stated against
the joint order [docs/settings-commands](../settings-commands/implementation-plan.md) already
publishes.

| # | What lands | Tasks | Why it sits here |
| --- | --- | --- | --- |
| 1 | **The status label, whole** | NV0–NV2 | it closes a falsehood, has no dependency at all, and does not touch a policy, a stage or the settings work. It can land in the same release as anything else, first, and it is the cheapest thing to review |
| 2 | **The node member and the merge** | AR0, AR2 | no publish, no stage, no other design: the shape and the direction, tested against a policy built by hand. This is the slice that a reviewer of this design can judge on its own |
| 3 | **The runner's two checks** | AR3, AR4, AR6 (with **E1**, #1476) | the enforcement lands once, in finalize, where the run's own commits and its stray files are both readable |
| 4 | **The prompt, the refusals and the pull request line** | AR5 | the sentences a run and a person read, after the checks they describe exist |
| 5 | **The publish** | AR1 (with **E3**, settings S3) | last, because it rides a stage S3 builds. Before it lands the rule is enforceable but unpublished, which is exactly what step 2's hand-built fixture tests |
| 6 | **The docs, the skill and the notes** | AR7 | with the release, not before it |

**The one-line answer:** the label first (NV0–NV2), the rule's runner half next (AR0, AR2, then
AR3–AR6 once #1476 has landed), and the publish last, immediately after the settings design's S3.

## 5. Operator steps

**One, and it is the existing one: `fugaro update`.** It republishes the project policy as version 5
— the node that carries `binaries` — and refreshes the runner image in the same command, in the same
confirmation, with nothing else to run and nothing to delete. A project that sets no `binaries`
block is unchanged: the node gains a member it does not use, and the run that leaves a build product
in the tree is refused exactly as it is today.

The release notes carry that as one line in `### For operators` (`docs/releases/README.md`), drafted
in AR1's PR body because `docs/releases/**` is written by the release PR and no task may name a
version it does not know.
