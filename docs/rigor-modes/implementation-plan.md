# Implementation plan — rigor modes

**Status: approved to build on the branch `rigor-modes`; the owner approval gate is passed.**

Nine slices, ordered so that each one merges alone and leaves `main` green, and
so that a repository that configures nothing sees only what D19's answer made
observable — the default is **Pro**, so what "ready" means for an unconfigured
repository changes with the commit that gives the mode a reader, and the
migration note (T8.2) and the one-line pin `rigor.default: critical` are what a
repository answers that with. This plan is written from [reconciliation.md](reconciliation.md)'s
findings **F1–F48**, decisions **D1–D32**, items **O1–O7** and the UNVERIFIED
table **U1–U12**, and it cites them rather than restating them. Slice 1 is
ordered by the owner's scope ruling of 2026-10-20 (the reconciliation's §1.2):
the project-wide default lands first, the repository's override second, the area
override last, and each of the three merges on its own. Slice 4's last five tasks
are ordered by the owner's backlog ruling of the same day (the reconciliation's
§1.3): the item's format and its first files land first, the generated index and
its two pins second, the filing command and the run that files third, the
per-mode rule fourth, and closing with the one-backlog practice last — each
merging alone.

## 0. What this does not do

- **It does not implement anything.** No slice is started and no line of this
  plan is written. The [OWNER APPROVAL GATE](#owner-approval-gate) **is passed**
  — the owner answered §5's decisions (**D1–D32**) on 2026-10-20 — so the slices are approved to build
  on the branch `rigor-modes`, and approved is not started.
- **It does change the readiness rule's meaning for an unconfigured repository,
  on purpose.** D19's answer makes the default **Pro**, so a repository with no
  `rigor:` block starts tolerating what Pro tolerates — findings that used to fail
  a run may now be tolerated and filed. The **security-sensitive tier stays
  Critical** (the D18 floors, outside every layer), and a repository that wants
  today's behaviour exactly pins it back with `rigor.default: critical`. Every
  claim below that a slice "relaxes nothing" or is "byte-identical to today" is
  therefore **true of Critical mode only**, and T8.2's migration note is the loud
  version of this paragraph.
- **It does not add a merge path, a deployer, or a scheduler.** `gitprov` has no
  merge call and the runner never deploys (F29); D13 and D15 say what Hotfix and
  the automatic follow-up mean instead, and both are smaller than they sound.
- **It does not touch `.github/workflows/`.** The post-merge full suite is the
  existing CI on `main` (F27, D13). No workflow file is created, edited or
  proposed by this plan — a run may not touch that path at all (F12), and this
  plan's tasks are written to be run in a way that respects it.
- **It does not add a second trust list, a second glob dialect, a second
  readiness rule, or a second configuration file.** F13, F9, F4 and D5: the
  existing ones are reused, and a task that proposes a new one is proposing a
  decision, not a task.
- **It does not put the backlog in a provider's issues, in a second repository,
  or in a scheduler.** The owner's ruling of 2026-10-20 keeps the backlog as
  Markdown under `docs/backlog/` (the reconciliation's §1.3): a run files an item
  into its own repository and its own branch, because that is the one repository
  its token reaches and the one it can push (F43, F44), and **D29** is the owner's
  question about which repository holds a project's items; its **value** is the
  one thing in the block still the owner's to name, and T4.5's **Depends on**
  line says so.
- **It does not decide anything the owner has not.** D1–D32 are **answered**
  (2026-10-20); O1–O7 are items for the owner and U1–U12 are unverified claims,
  and those two stay open. A task below that depends on an answer says so in its
  **Depends on** line, and the one value still unnamed — D29's designated
  repository — is named there too.

## 1. Builds on merged code

This work is a second storey on parts of the tree that already exist, and every
one of them is named so a reader can check the claim:

| Existing | Used for | Finding |
| --- | --- | --- |
| `internal/config/resolve.go` `Resolve`, `config.LayerKey` | the mode's three layers, in the layering that already exists — the project default is a new member of the closed `defaults:` | F8, F36 |
| `internal/config/scope.go` `Scopes`, pinned by `TestScopeTableMatchesDocs` | which layer may set `rigor.*` | F7 |
| `github.com/bmatcuk/doublestar/v4` | path rules, validated by `internal/config/validate.go` | F9 |
| `internal/runstore/changedfiles.go`, `Record.ChangedFiles` | the post-hoc floor check | F11 |
| `internal/followup/trust.go` `Trust`, `Allows` | who may invoke Hotfix, and the only route by which a floor is ever lowered | F13 |
| `internal/runner/followup.go` `readBaseConfig` | the revision the mode's configuration is read from, as it already is for a review run and a follow-up | F35 |
| `internal/watch/build.go` `runRow`, `budget.AgentEntry` | the live row a mode has to reach, and why it costs a registry child | F40 |
| `internal/runner/changedfiles.go` `recordChangedFiles` | the measured paths the area layer resolves against in `finalize` | F39 |
| `internal/runner/gate.go` | the mode's `check` gate | F26 |
| `internal/runner/outcome.go` `Decide`, `Plural` | where the mode's policy is applied | F4, F5 |
| `internal/runner/prflow.go` `finalSection`, `reviewsPart`, `applyReady`, and `internal/runner/runner.go` `finalize` (which builds `PRSpec{Draft: !ready}` and calls `settlePR`) | "left open by policy", and the ready PR | F28, F29 |
| `internal/cli/budget_preview.go` `StageBound` | the mode's ceiling | F17 |
| `internal/cli/diagnose.go` `Diagnosis.Findings` | reading the backlog back out | F18 |
| `internal/runner/refused.go` `workflowGuard` | the refusal a mode may never route around | F12 |
| `internal/gitprov/github/apptoken.go` `tokenSource.Token` | the one repository a run's token reaches, and the reason an item is filed where the run is | F43 |
| `internal/gitprov/gitprov.go` `PRSpec` | the provider call a filed item travels in, and the reason a cross-repo filing is a task and not a line | F43 |
| `internal/clidoc`, and `internal/pricing/catalog/gencatalog` for the other half of the discipline | the generated-index pattern the backlog's index follows, and the trap its second guard answers | F45 |
| `internal/runstore` `ReviewSummary`, and the stored transcript `fugaro diagnose` re-reads | what a deferred finding looks like today: a count on the record, its text in the transcript | F46, F47 |
| `docs/plans/2026-10-17-managed-projects-review-follow-ups.md`, and the four `docs/plans` lists F46 names | the deferral practice the backlog replaces, and what D32's sweep converts | F46 |

## 2. The slices, in one table

| Slice | What it is | Observable change | Depends on |
| --- | --- | --- | --- |
| **0** | the folder, and the guard that owns it | none (documents and one test) | — |
| **1** | the mode's three layers, and where they show | the resolved mode and the layer that won are recorded and printed; behaviour unchanged | 0 |
| **2** | the score, acted on by nothing | findings carry scores in the transcript and the prompt asks for them | 1 |
| **3** | the run loop applies the mode | a configured mode tolerates what it says it tolerates | 2, **and T4.5** — a mode may not tolerate a finding before there is an item file to put it in (D9) |
| **4** | the backlog surface, and the Markdown backlog | the tolerated findings are readable in the PR and by `fugaro diagnose`; an item is a file under `docs/backlog/`, and the index is generated | T4.1–T4.2 depend on 3; the backlog's own five tasks (T4.3–T4.7) depend on Slice 0 alone and are **picked up first** (D9) |
| **5** | the task file, and Hotfix | a task may ask for a mode; Hotfix opens a ready PR | 3 |
| **6** | cost | per-mode budget defaults; `budget preview --mode` | 3 |
| **7** | the documentation and the practice | `docs/rigor-modes.md`, the project layer, `docs/dogfooding.md` | 4, 5, 6 |
| **8** | the live checks, and the migration note | Check 119; the release note | 7 |

Slices 4, 5 and 6 are independent of each other and may be built in any order or
in parallel (§6). Slice 7 needs all three because it documents them. The
backlog's five tasks (T4.3–T4.7) are independent of Slices 2 and 3 as well: an
item filed by hand needs no mode (T4.3, T4.4), and only the run-side filing
(T4.5) waits for a mode to exist at all.

**The order is the owner's (D9): the backlog comes first.** D7's answer makes the
findings a mode tolerates into backlog items, so a mode must not be able to
tolerate a finding before there is a file to put it in. The backlog's five tasks
are therefore picked up **before** Slice 3 — T4.3 (the format and the first
items), T4.4 (the index and its pins) and T4.5 (the filing command and the run
that files) land first, and **Slice 3's T3.1 depends on T4.5**, not the reverse.
An item is filed on the **same run branch and in the same pull request** as the
change it was filed from — the question the ruling left this folder to settle,
answered in the reconciliation's D9 — so the filing dirties nothing that was not
already part of the pull request a reviewer is reading, and a follow-up to be
filed later is a file in that same pull request.

## 3. Check numbers this plan takes

**This plan proposes Check 119.** The register is
`docs/gcp-live-checklist.md`, and `docs/coordinator/README.md` §"The numbers
this work holds" is the table that reads it. At `1d325928`: **Checks 1–109 are
taken or reserved** (block by block, as that register's own row lists), and above
them every number to 118 is held by an open claim:

| Number | Who holds it, and in what state |
| --- | --- |
| **110** | **claimed twice over**: by the always-on orchestrator design (`docs/design/always-on-orchestrator.md`, PR #632, the claim `docs/coordinator/README.md`'s row records) and, separately, named by the mobile P5.3 design (`docs/mobile/design/voice-evaluation-switch.md`, its Q4 answered by the owner). Written by nobody, either way |
| **111** | taken by `docs/setup-wizard/` — the billing picker's five failure modes, task W6.2 |
| **112** | **proposed** by `docs/windows-cli/` — one owner-run first run on a real Windows machine (its PR #757 is merged) |
| **113–118** | taken by `docs/mobile/voice-session/` — six checks, **numbered but not written**, its D219 |

So the next free number is **119**, and this plan takes it. The register's own
rule governs a collision, and this plan is written to it: **whichever of two open
claims merges second renumbers itself**, to the number free at its own merge, and
updates the register's paragraph in the same commit. It never renumbers a number
an earlier plan has already written, and a reserved number is never reused. The
six numbers above 112 — 113 to 118 — are claims rather than written sections:
`docs/mobile/voice-session/` is merged at this plan's own base `1d325928` and its
D219 sits in the tree there (the D-block paragraph below reads it), so the claim
is written down and what is outstanding is the check sections themselves. Written
or not, this plan does **not** move up into the gap: it keeps 119, which is free
either way. The
task that writes the check (T8.1) owns any renumbering and the register line in
the same commit.

**This plan takes no D-number.** Its decisions are **D1–D32**, folder-local and
distinct by folder, like `docs/windows-cli/`'s D1–D16 and
`docs/setup-wizard/`'s D1–D12. The register
(`docs/coordinator/README.md` §"The numbers this work holds") allocates
**D1–D192** — the owner's allocation, block by block — and anchors that figure
itself, *"the highest number written anywhere in `docs/` at `c6009b5f` is D192"*,
which is why it has to be read anchored, the way `docs/mobile/voice-session/`
reads it: at this plan's base `1d325928` the register's own command finds
**D220**, because voice-session writes **D201–D220** there, the same work whose
**D219** the check table above cites. So **D193 onward is not free at this
tree** — the next free block is **D221 onward**. O7 states the choice, the
reading and the collision; this plan takes no number either way.

## 4. Conventions every task follows

The list `docs/setup-wizard/implementation-plan.md` states, restated because
this repository has no `CLAUDE.md` and no `.claude/rules/` to point at (F34's
correction, C5):

1. **Documents and pins move together.** A task that changes what a document
   promises changes the test that holds it in the same commit. A pin that goes
   red is the task's red step, not an obstacle to it.
2. **Goldens are regenerated, never hand-edited.** `website/content/cli-reference.md`
   is generated by `go run ./internal/clidoc` and held by
   `internal/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent`; a flag change
   regenerates it rather than editing it.
3. **Nothing is decided by a task.** A task that needs a D-number answered stops
   and says so. The plan's tasks are written so the dependency is visible in the
   **Depends on** line.
4. **No secret-shaped literals.** Never read, write or ask for API keys, tokens,
   secrets, certificates or passwords; never write a literal that looks like one.
   The path *patterns* in Slice 1 are globs, not credentials.
5. **Tests are hermetic.** No live cloud action, no live provider call, no
   network. A test that needs one is a live check, and the live checks are §9.
6. **No task edits `.github/workflows/`.** A task that believes it needs to says
   so in the pull request instead; the runner refuses the push (F12).
7. **Refusals are immutable.** A task may not weaken `workflowGuard`,
   `workflowRefusal` or the App's missing Workflows permission, and a task whose
   change would is a security decision for a person (D18, §7).
8. **The pin commands run with `GOFLAGS=-p=2`** and a generous timeout
   (`-timeout 40m`), because the documentation tests are IO- and regex-heavy and
   the package is large.
9. **Test-first, and mutation-proven.** A task's red step is a test that fails
   for the reason the task exists. A test that passes before the change is not a
   red step.

## 5. Global constraints

- **The readiness rule stays one rule.** `runner.Decide` is where readiness is
  decided (F4) and no task may add a second answer to "may this PR be ready".
  Slice 3's T3.1 changes what `Decide` is given, not what it means.
- **Critical *mode* is byte-identical to today — and only Critical, because D19's
  answer makes Pro the default.** T3.1's first test asserts it: a run whose
  resolved mode is Critical produces the same outcome and the same reason string
  as `main` does, and the same PR body apart from the one line D18 requires every
  run's body to carry, naming the mode it ran under. If that test cannot be
  written, the slice is wrong. The same claim is **not** true of a run in an
  unconfigured repository: that run is Pro, and T3.1's second test is the one that
  says what Pro tolerates.
- **Fail closed, everywhere.** An unscored finding, an unparseable verdict, or a
  policy that cannot decide blocks in every mode (D4, F3). T2.3 and T3.1 both
  carry a test for it.
- **The deployed budget cap is the ceiling.** No mode raises it (F15, D16); the
  per-mode defaults are fractions below it.
- **The mode used is one value**, recorded in the PR body and on the run record
  (D18), so a mismatch is a bug rather than a documentation drift.
- **No new configuration file, trust list or glob dialect** (§0).
- **Security-sensitive work is not done by a run.** `docs/dogfooding.md`
  ("anything that handles money or security" goes through a local session) and
  `docs/credentials-service/implementation-plan.md` Global Constraint 10 (F31).
  Tasks whose **Reviewer** cell reads *operator* are in that class: they are done
  by a person or a local session, never by a Fugaro run, and a run that picks one
  up has found a bug in this plan.

## 6. Parallel execution

**Edges.** `0 → 1 → 2 → {T4.3 → T4.4 → T4.5} → 3 → {4, 5, 6} → 7 → 8`. Slice 3's
first task waits for T4.5 as well as for Slice 2 (D9), so the backlog's first
three tasks are built before the run loop applies the mode — and Slice 4's own
five tasks hang off T0.1 rather than off Slice 3, exactly as the table in §2
says. Within a slice, the tasks are listed in the order they must land, except
where a task says otherwise in its **Depends on** line.

**Levels** (what may run at the same time, by dependency depth):

| Level | Tasks |
| --- | --- |
| 0 | T0.1, T0.2 |
| 1 | T1.1; T2.1 and T4.3 (neither depends on Slice 1 at all) |
| 2 | T1.2, T1.3, T1.5, T1.6, T2.2, T2.3, T4.4 |
| 3 | T1.4 (needs T1.3's key), T1.7, T2.4, T4.5, T4.7 |
| 4 | T3.1 (it may not tolerate a finding before T4.5 can file one, D9) |
| 5 | T3.2, T3.3, T3.4, T3.5, T5.1 |
| 6 | T4.1, T4.2, T4.6, T5.2, T5.3, T6.1 |
| 7 | T5.4, T6.2, T6.3 |
| 8 | T5.5, T7.1, T7.2, T7.3 |
| 9 | T8.1, T8.2, T8.3 |

**File lanes.** Two tasks in different lanes may be built in parallel without a
conflict; two tasks in the same lane may not.

| Lane | Files | Tasks |
| --- | --- | --- |
| A | `internal/rigor/**` (new package) | T1.1, T1.4, T1.5, T2.3, T4.6, T5.3 |
| B | `internal/config/**`, `schemas/fugaro.schema.json` | T1.2, T1.3, T1.4, T6.1 |
| C | `internal/runner/**` | T1.4, T1.6's PR line, T2.1, T2.2, T3.1, T3.2, T3.3, T5.4 |
| D | `internal/runstore/**`, `schemas/result.schema.json`, `internal/task/**` | T1.2, T3.4, T5.1, T5.5 |
| E | `internal/cli/**`, `website/content/cli-reference.md` | T1.6, T4.1, T6.2, T6.3 |
| F | `docs/**` (outside this folder) | T1.2 and T1.4 (the project layer's table and its rule), T7.1, T7.2, T7.3, T8.1, T8.3 |
| G | `docs/rigor-modes/**`, `internal/cli/docs_rigor_modes_test.go` | T0.1, T0.2 |
| H | `internal/budget/**`, `internal/watch/**`, `console/docs/json-contract.md` | T1.6's console contract, T1.7, T4.4's `--json` entry |
| I | `internal/backlog/**` (new package), `internal/cli/backlog.go`, `docs/backlog/**` | T4.3, T4.4, T4.5, T4.7 |

**Critical path:** T0.2 → T1.1 → T1.2 → T2.1 → T2.3 → T4.3 → T4.4 → T4.5 → T3.1
→ T3.3 → T3.4 → T4.1 → T7.1 → T8.1. **The backlog ruling lengthens it on
purpose** (D9): T4.3–T4.7 hang off T0.1 and would not be on the path by
themselves, but T4.5's run-side half waits for T1.2 and Slice 3 waits for T4.5,
so the backlog's first three tasks are built first and everything else waits for
them.

**What must be sequential, and why:**

- **`schemas/fugaro.schema.json` and `schemas/result.schema.json`.** Each is one
  file with one writer: two tasks editing one schema in parallel produce a
  conflict in a hand-written file, and the corpus tests fail on neither side
  cleanly. T1.2, T1.3, T1.4 and T6.1 touch the first; T1.2, T3.4 and T5.5
  touch the second.
- **`internal/runner/outcome.go`.** T3.1 is the only task that may change what
  `Decide` returns, and it lands before T3.2, T3.3 and T3.4 so they are written
  against the rule that will exist.
- **`internal/cli/run.go`.** T1.6 defines the flags (`--mode`, `--path`); T5.2
  makes `--mode` raise-only on a follow-up; T6.3 prints the mode. In that order.
- **`docs/backlog/README.md`.** It is a generated file with one writer at a time
  (D28): T4.4 commits the generator and the first index, T4.5's filing command and
  T4.7's close regenerate it rather than edit it, and two branches that
  regenerated it at once are reconciled by re-running `fugaro backlog index`, not
  by merging the file. A hand edit to it is the thing its own pin refuses.
- **The pin test in Slice 0** must land before any slice that a reader would want
  held, because a later slice that edits a document this folder's index names
  updates the pin in the same commit.

## 7. Review focus

What a reviewer of these pull requests should look at, in order:

1. **Does the change alter a run at Critical mode?** It must not: Critical is
   byte-identical to today (apart from the mode line D18 requires in every run's
   body), and a slice that changes Critical's outcome, reason or body is not
   mergeable as written.
2. **Is the change the one D19's answer asks for in an unconfigured repository?**
   The default is **Pro**, so a repository with no `rigor:` block tolerates what
   Pro tolerates. The question is not "does this alter an unconfigured repository"
   — it must, that is the answer — but "is what it tolerates **Pro's table**, and
   is the mode line, the filed item and the migration note all present beside it".
   **Pro's threshold table is the behaviour of every unconfigured repository**,
   which is why it is the one table whose review cannot be deferred and why T2.3
   and T3.1 are read together.
3. **Is there a second answer to "may this PR be ready"?** If yes, F4 is broken.
   `Decide` is the only place.
4. **Can a mode lower a floor, or route around `workflowGuard`?** If yes, §5's
   security constraint is broken. Grep the new code for a mode comparison that
   *permits* rather than *requires*.
5. **Is a finding able to disappear?** Every finding must end in exactly one of
   three places: fixed, recorded as tolerated with its mode, or blocking. A
   finding that vanishes is the failure mode this whole feature risks.
6. **Does a new schema property land with its pin?** F20 and F7: the corpus tests
   and `TestResultSchemaAcceptsRunnerRecords` are the check.
7. **Is anything security-sensitive being done by a run?** §5's last constraint.
8. **Is there still one backlog?** An item is one file under `docs/backlog/` and
   the index is generated (D26–D32): a hand-edited index, a new deferral list in
   a `docs/plans` file, or a finding "left open by policy" with no item behind it
   is the drift this question exists to catch (F46, D32). Asking it of a pull
   request that touches Slices 3 or 4 is the cheap moment to catch it.
9. **Is the item filed in the right place?** An item a run files is a file on
   that run's own branch, in the same pull request as the change it was filed from
   (D9), reviewed with it — never a second pull request, never a provider issue,
   and never a write to the base branch. A filing path that would dirty a pull
   request that is *not* the run's own is the bug this question exists to catch.
10. **Is the order stated in more than one place, or can a layer reach under a
   floor?** The ruling asks for one resolver and one statement (D6, D20): §2 of
   `docs/project-layer.md` and `internal/rigor`'s `Resolve` are the two places it
   may live, and a third restatement in a comment, a doc or a test name is the
   failure this question is looking for. A comparison that lets a repository
   value, an area or a flag *lower* a floor is the other one (D18, D21).

## 8. The slices

### Slice 0 — the folder, and the guard that owns it

The document set is the deliverable of the analysis and it is already written;
this slice is what makes it stay true. It is one pull request, and it changes no
product code.

**T0.1 — the four documents**
- **Files.** `docs/rigor-modes/spec.md` (the owner's spec, verbatim, in its own
  commit), `docs/rigor-modes/reconciliation.md`,
  `docs/rigor-modes/implementation-plan.md`, `docs/rigor-modes/README.md`.
- **Tests.** None yet: this task is the folder's first commit and nothing reads
  it. The test that will read it is T0.2's, written red first.
- **Red step.** None — a document that does not exist cannot fail a test. The
  honest statement is that this is the one task in the plan whose red step is
  T0.2, and T0.2 is written before it passes.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'Docs|Lint|CliRef' -count=1 -timeout 40m`
  (green, because nothing yet reads the folder).
- **Depends on.** —
- **Reviewer.** senior (in-loop) — documents only; the owner's review is the real
  one, it happened on 2026-10-20, and the OWNER APPROVAL GATE below is where that
  answer is recorded.
- **Commit.** `docs(rigor-modes): the owner's spec, the reconciliation and the plan`

**T0.2 — the folder's pin test**
- **Files.** `internal/cli/docs_rigor_modes_test.go` (new).
- **Tests.** `TestDocsRigorModesIsWritten` (the index exists, carries the status
  line word for word, names the other three files and the revision);
  `TestDocsRigorModesFindingsDecisionsAndItemsAreNumbered` (F1–F48, D1–D32,
  O1–O7 and U1–U12 run from 1 with no gap, every finding carries one of the five
  class words, and the index's stated ranges are checked against the counts
  rather than trusted); `TestDocsRigorModesCensusStatesItsCommands` (every census
  row carries a command); `TestDocsRigorModesEveryTaskIsComplete` (every
  `**T<n>.<m> —` task in this document carries all seven labels — Files, Tests,
  Red step, Verification, Depends on, Reviewer, Commit — and every slice is a
  `## Slice` heading); `TestDocsRigorModesEveryCitedTaskExists` (every `T<n>.<m>`
  a document cites resolves to a task in this document);
  `TestDocsRigorModesEveryBlockRangeIsCurrent` (every block range a document
  states ends where the block does — counted from the reconciliation — or is a
  part of one cited beside its section, or a register this folder quotes by name;
  and no document cites a number the block does not have);
  `TestDocsRigorModesSpecIsTheOwners` (the spec's own sentences survive, and no
  document in the folder puts a decision in the owner's mouth);
  `TestDocsRigorModesCensusSkipsGitOnlyWhereAShellRunsIt` (the census guard's own
  skip rule, pinned in both directions: a command that runs `git` is skipped in a
  tree without a repository, and a command that only reads a path with the three
  letters in it is not); and
  `TestDocsRigorModesAnswersSayWhatTheyChange` (the sentences the answers of
  2026-10-20 displaced are the ones the documents carry: the one-line pin D19's
  answer owes a repository, every unchanged-behaviour claim scoped to Critical
  mode, no phrasing that puts a person in the ready path, D9's same-pull-request
  rule with T3.1 waiting for T4.5, T5.3's project-configured inputs, and D29's
  value still the owner's).
- **Red step.** The test file does not exist; written first, each of the nine
  fails on a folder that is missing the sentence it names. The task is not done
  until each is red once and green after, and what each one names becomes T0.1's fix.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsRigorModes' -count=1 -timeout 40m`
- **Depends on.** T0.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `docs(rigor-modes): pin the folder's own claims`

**T0.3 — the register line, if O7 is answered with one sequence**
- **Files.** `docs/coordinator/README.md` (the D-block row),
  `internal/cli/docs_coordinator_test.go` (its pin, which holds that row to the
  line the document carries it on).
- **Tests.** `docs_coordinator_test.go`'s own guards, unchanged, which is the
  point: the register's line moves and the test that reads it stays the same.
  The line it records is **D221 onward** — the next free block read from this
  plan's base (O7), not the register's unanchored **D193 onward**, which
  `docs/mobile/voice-session/` already sits inside.
- **Red step.** The register does not mention this folder, so a test asserting
  that it does fails. If O7 is answered "keep the block local", this task is
  dropped and its absence is recorded in the index.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsCoordinator' -count=1 -timeout 20m`
- **Depends on.** O7 answered.
- **Reviewer.** senior (in-loop).
- **Commit.** `docs(rigor-modes): record this work's D-block in the register`

### Slice 1 — the mode's three layers, and where they show

The slice is ordered by the owner's scope ruling (the reconciliation's §1.2): the
**project default lands first** (T1.2), the **repository's override second**
(T1.3), the **area override last** (T1.4), and each of the three merges on its
own. T1.1 is the vocabulary and the resolver the three stand on; T1.5 is the
security floor, which is not a layer; T1.6 and T1.7 are the surfaces — the run,
the PR and `fugaro watch` — because the ruling asks for the mode *and the layer
that won* to be visible and a value nobody can read is a value nobody can check.

Everything here is inert until a later slice acts on it: `Decide` still decides
by today's rule at the end of this slice, so a repository that configures POC
gets the behaviour of Critical — the stricter direction — plus a record that says
what it asked for. That is what lets each task merge alone and leaves `main`
green with no behaviour change at all.

**T1.1 — the mode vocabulary, and the one resolver**
- **Files.** `internal/rigor/mode.go` (new: `Mode`, its parse, `Rank`,
  `Strictest`), `internal/rigor/resolve.go` (new: `Resolve`, implementing D6's
  order — area, then repository, then project default, then Fugaro's own default,
  with the task's request able to raise and never lower, and the floors outside
  every layer, only tightening), and the path-rule matching that reuses
  `github.com/bmatcuk/doublestar/v4` the way `internal/config/validate.go`
  validates patterns.
- **Tests.** `internal/rigor/resolve_test.go`: one table case per rank and per
  pair of ranks (an area beats the repository's value, the repository's beats the
  project's, the project's beats Fugaro's default, a task request raises and
  cannot lower, a floor wins over all of them, a narrow layer that says nothing
  changes nothing); an unknown mode name is refused; a pattern that
  `doublestar.ValidatePattern` rejects is refused at load rather than at match;
  `Strictest` over a set. Each case is mutation-proven by inverting the
  comparison.
- **Red step.** The package does not exist. The first test written is "a task
  requesting POC over a Critical area resolves to Critical", which cannot compile.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor -run 'Resolve|Mode|Strictest' -count=1 -timeout 20m`
- **Depends on.** T0.2.
- **Reviewer.** senior (in-loop).
- **Commit.** `rigor: one vocabulary, one resolver, one order`

**T1.2 — the project default, and the run records which layer set the mode**
- **Files.** `internal/config/layer.go` (the third member of the closed
  `LayerDefaults`), `internal/config/scope.go` (its `InProject` rows),
  `internal/config/resolve.go` (the merge into every repository of the project),
  `docs/project-layer.md` (the printed scope table), `testdata/config/layered/`
  (a new case), `internal/runstore/runstore.go` (the record's rigor block: the
  mode, the layer that won, the areas that matched, and the moment), and
  `schemas/result.schema.json` (its entry, `version` staying `1` per D9's note).
- **Tests.** `internal/config`'s corpus and layer tests, `TestScopeTableMatchesDocs`
  in `internal/config/docs_scope_test.go`, `schemas/schemas_test.go`'s
  `TestResultSchemaAcceptsRunnerRecords` — which marshals a fully-populated record
  and so goes red the moment the field lands without its schema entry — and a
  resolution test that a project default reaches a repository whose `fugaro.yaml`
  says nothing.
- **Red step.** The layer case is written first and fails with the strict
  decoder's "unknown field" (`KnownFields(true)`), which is exactly F36's point;
  and the record test fails on its schema until the entry is added, which is F7's.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/config ./schemas ./internal/runstore -count=1 -timeout 20m`
- **Depends on.** T1.1 (the vocabulary and the resolver).
- **Reviewer.** senior (in-loop). This is a configuration value, not a credential:
  D18 puts the security-sensitive work in T1.5.
- **Commit.** `config(rigor): a project-wide default, and the record that names its layer`

**T1.3 — the repository's own `rigor:` block in `fugaro.yaml`**
- **Files.** `internal/config/config.go` (the struct), `internal/config/defaults.go`,
  `internal/config/scope.go` (the `InRepo` rows), `schemas/fugaro.schema.json`,
  `testdata/config/valid/rigor.yaml` **(new)** — the corpus is at the repository
  root, not under `internal/config/` — `testdata/config/invalid/rigor.yaml`
  **(new)**, whose near-miss (`default: "Poc"`) proves the vocabulary is closed,
  and `docs/project-layer.md` (the scope table's rows).
- **Tests.** `internal/config/corpus_test.go`'s `TestCorpus`, which globs
  `testdata/config/{valid,invalid}/*.yaml` from `internal/config` (so a new case
  file is the whole edit), the `ScopeOf` cases in
  `internal/config/scope_test.go`, and `TestScopeTableMatchesDocs`, which holds
  `docs/project-layer.md` in step with the scope table — the doc itself is read by
  `internal/cli/docs_project_layer_test.go`.
- **Red step.** The corpus case is written first and fails with the strict
  decoder's "unknown field `rigor`" (`KnownFields(true)`), which is exactly F7's
  point: this is the same five edits, done in the repository's own file.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/config ./internal/cli -run 'Config|Scope|DocsProjectLayer' -count=1 -timeout 20m`
- **Depends on.** T1.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `config(rigor): the repository's default, read by nothing yet`

**T1.4 — the area layer: named globs, chosen from the files the run touches**
- **Files.** `internal/rigor/areas.go` (new: `Area{Name, Match, Mode}` and its
  load-time validation), `internal/rigor/select.go` (new: `Select`, D23's
  strictest-of-matched, returning the names of every area that matched),
  `internal/config/config.go`, `internal/config/defaults.go` and
  `internal/config/scope.go` (`rigor.areas` in either layer), `schemas/fugaro.schema.json`,
  `testdata/config/valid/areas.yaml` and `testdata/config/invalid/areas.yaml`
  **(new**: a pattern `doublestar.ValidatePattern` rejects, a mode name outside
  the vocabulary, an entry with no `name`), `docs/project-layer.md` (§2's one rule
  gains the area rank — **the one place the order is stated**, D6, D20),
  `internal/cli/run.go` (`--path`, repeatable), and `internal/runner/runner.go`
  (D22's three moments: at bootstrap from the declared paths, after a round that
  changed the tree, from the run's own diff
  `r.repo.ChangedFiles(ctx, r.workBase())` that `internal/runner/changedfiles.go`
  already calls, and in `finalize` against `runstore.Record.ChangedFiles`; monotone
  across all three).
- **Tests.** `internal/rigor/select_test.go`: a path matching two areas takes the
  strictest and the result names both; a path matching none leaves the mode as it
  was; a path cannot lower below a floor; a rule whose pattern or mode is bad is
  refused at load. The config corpus for the two new cases. A runner test over the
  three moments: the mode rises and never falls, the record names the area and the
  moment, and a run with no declared paths starts at its repository's value. The
  guard on §2's sentence stands beside `TestScopeTableMatchesDocs` (F37: the
  table is pinned today and the sentence is not).
- **Red step.** Written first: "a run whose declared path matches a Critical area
  resolves to Critical while its repository says POC" fails, because there is no
  area — and the doc guard fails, because §2's rule does not yet name the rank.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor ./internal/config ./internal/runner -run 'Select|Area|Config|Rigor' -count=1 -timeout 20m`
- **Depends on.** T1.1, T1.3 (the block the areas live in).
- **Reviewer.** senior (in-loop).
- **Commit.** `rigor: areas — named globs, resolved from the files the run touches`

**T1.5 — the built-in sensitive-path floors**
- **Files.** `internal/rigor/floors.go` (new), and a comment-or-test link to
  `internal/runner/refused.go`'s `WorkflowDir` so the two lists are visibly one
  idea rather than two.
- **Tests.** `internal/rigor/floors_test.go`: `.github/workflows/` resolves to
  Critical in every mode and for every input, including a task that asks for POC
  and a follow-up that asks for Hotfix; a credential or IAM path declared by the
  repository cannot resolve below Critical; an area rule that names a floored path
  with a lower mode changes nothing (D21); and the built-in set is a `doublestar`
  pattern set like the declared one, so there is one matcher.
- **Red step.** Written first: "a task requesting POC for
  `.github/workflows/ci.yml` resolves to Critical" fails, because no such floor
  exists.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor -run 'Floor' -count=1 -timeout 20m`
- **Depends on.** T1.1, T1.2.
- **Reviewer.** **operator.** This is the security floor, and §5's last
  constraint says security-sensitive work is not done by a run: it is done by a
  person or a local session. A Fugaro run must not pick up T1.5.
- **Commit.** `rigor: the paths no mode may run below`

**T1.6 — `fugaro run --mode`, and the mode in the list, the diagnosis and the PR**
- **Files.** `internal/cli/run.go` (`--mode`; `--path` is T1.4's flag and this
  task only prints it beside `--mode`),
  `website/content/cli-reference.md` (regenerated, never hand-edited),
  `internal/runview/runview.go` (the row), `internal/cli/runs.go` and
  `internal/cli/diagnose.go` (the column and the printed line),
  `console/docs/json-contract.md` §6.1 (the console's contract),
  `internal/runner/prflow.go` (`finalSection`: the mode, the layer and the area,
  in the shape D9 already plans for the backlog).
- **Tests.** `internal/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent`;
  `internal/runview`'s row and `Join` tests; `internal/cli`'s diagnose test and
  the console-contract test; `internal/runner`'s `prflow` test that the status
  section names the mode and where it came from; and a CLI test that `--mode
  hotfix` is accepted, that an unknown value is refused, and that the flag absent
  means Critical (D19).
- **Red step.** `--mode hotfix` is refused by cobra as an unknown flag; the
  regenerate-and-compare test fails until the reference is regenerated; and the
  PR test fails because the section has no such line.
- **Verification.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/clidoc ./internal/runview ./internal/runner ./internal/cli -run 'CliRef|Row|Rigor|Diagnose' -count=1 -timeout 20m`
- **Depends on.** T1.1 (the vocabulary), T1.2 (the record it prints).
- **Reviewer.** senior (in-loop).
- **Commit.** `cli(run): --mode, and the mode on the run, in the list and in the PR`

**T1.7 — the mode in `fugaro watch`, and the registry child it costs**
- **Files.** `internal/budget/model.go` (`AgentEntry`), `internal/runner/budget_session.go`
  (`registryEntry`, which fills it), `internal/budget/registry.go` (`Session.Start`'s
  write and its drop-on-old-rules path — the `Recipe` precedent F40 records),
  `internal/budget/rules/rules.json.tmpl` (the child) and
  `internal/budget/rules/testdata/rules.golden.json` (regenerated),
  `internal/watch/build.go` (`runRow`) and `internal/watch/json.go` (`JSONRun`).
- **Tests.** The rules' golden test; `internal/budget`'s registry test that an
  entry carrying the field against rules that refuse it is written *without* it,
  once, with the warning, and remembered for the session; `internal/watch`'s
  build and JSON tests (a live row carries the mode and its layer; a row whose
  entry has none shows the same `-` every other absent value shows).
- **Red step.** The watch test asserting a live row's mode fails, because neither
  the row nor the entry has the field.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/budget ./internal/watch -count=1 -timeout 20m`
- **Depends on.** T1.2.
- **Reviewer.** senior (in-loop). **The rules deploy is the owner's**
  (`fugaro init --firebase-project <firebase-project-id>`): a container cannot deploy
  them, and §9's last check is the one that observes it.
- **Commit.** `watch: a live run's mode, and the registry child it needs`

### Slice 2 — the score, acted on by nothing

The reviewer starts emitting scores and the repository starts carrying them. No
decision reads them yet, which is deliberate: the measurement (T2.4) has to be
possible before the thresholds are trusted, and a slice that ships scores without
policies is one that cannot make a PR ready that would not have been ready
before.

**T2.1 — the finding item gains four optional properties**
- **Files.** `internal/runner/verdict.go` (`VerdictSchema`, `Finding`).
- **Tests.** `internal/runner/verdict_test.go` **(new)**, plus cases added to
  `TestParseVerdict` in `internal/runner/pure_test.go`, which is where the parse
  contract is held today: a verdict in today's shape
  (`severity` only) still parses; a scored verdict round-trips; a verdict with
  `class: "security"` parses; an out-of-range score is *not* rejected by the Go
  side (the schema constrains the model, and the Go parser stays permissive so a
  stored transcript never becomes unreadable) — a test that states that as the
  intent rather than leaving it as an accident.
- **Red step.** The test asserting that a scored finding survives `ParseVerdict`
  fails, because `Finding` has no such fields and `json.Unmarshal` drops them.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'Verdict|Finding' -count=1 -timeout 20m`
- **Depends on.** —
- **Reviewer.** senior (in-loop).
- **Commit.** `runner: score a finding on three axes, labelled by class`

**T2.2 — the anchors, in the review prompt**
- **Files.** `internal/runner/prompts.go` (the review prompt's instructions).
- **Tests.** The prompt-invariant tests in `internal/runner/pure_test.go`
  (`TestPromptWorkflowRule` and its siblings), plus a new case: every anchor word
  appears once per axis, the five labels are named, and the scoring instruction is present in the review prompt and absent
  from the implement prompt — a test that catches the copy-paste that would make
  a coder score its own work.
- **Red step.** The test asserting the anchors fails: the prompt asks for a
  `severity` string and nothing else.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'Prompt' -count=1 -timeout 20m`
- **Depends on.** T2.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `runner: tell the reviewer what 1 and 5 mean on each axis`

**T2.3 — the threshold table, as data**
- **Files.** `internal/rigor/table.go` (new: the per-mode thresholds of D2 as
  data, and the split of a verdict's findings into blocking and tolerated),
  `internal/rigor/label.go` (new: the five-word label derived from the axes).
- **Tests.** `internal/rigor/table_test.go`: the spec's own two examples — "rare
  but catastrophic" blocks in Startup, "common but cosmetic" does not — are table
  cases, and so is the security override in **every** mode, Hotfix included, whose
  row the owner's ruling of 2026-10-26 (D7's amendment) leaves the security gate
  alone: a `data_loss` finding and an impact of 5 are tolerated there, so the
  Hotfix cases are the ruling's and the four on-axis modes' are the override's; an
  unscored finding blocks in every mode (D4's fail-closed rule, asserted mode by
  mode rather than once); and Critical blocks on every finding, scored or not.
- **Red step.** The table does not exist; the spec's two examples are the first
  test cases and both fail to compile.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor -run 'Table|Label|Threshold' -count=1 -timeout 20m`
- **Depends on.** T2.1, T1.3.
- **Reviewer.** senior (in-loop).
- **Commit.** `rigor: the per-mode thresholds, as data with the spec's examples as tests`

**T2.4 — the calibration run**
- **Files.** `internal/rigor/testdata/findings/` (a small corpus of scored
  findings, taken from real runs once T2.1–T2.2 are in use), and the report that
  reads it — a `go test` that prints the distribution rather than asserting one,
  so it is a measurement and not a gate.
- **Tests.** The corpus parses; every entry carries all three axes and a class;
  and the distribution test prints, and asserts only that each axis value occurs
  somewhere (a corpus that clusters on one value is a calibration failure worth
  seeing, not a build failure).
- **Red step.** No corpus exists. The first version is collected by hand from at
  least five finished runs, which is why this task is *after* the prompt change
  and not before it.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor -run 'Calibration' -v -count=1 -timeout 20m`
- **Depends on.** T2.3. **Depends on U1**: whether a cheap model scores reliably
  is UNVERIFIED, and this task is what answers it. If it cannot, the honest
  outcome is a decision for the owner — D4 already makes the failure safe, and
  O1 is where the answer lands.
- **Reviewer.** **operator** — the corpus is drawn from real runs and the
  judgement about whether the scores are usable is the owner's.
- **Commit.** `rigor: measure what the reviewer actually scores before trusting a threshold`

### Slice 3 — the run loop applies the mode

The slice where behaviour changes, and the slice whose first test is that it does
not change for anyone who has not configured a mode.

**T3.1 — the policy, inside `Decide`**
- **Files.** `internal/runner/outcome.go` (`Decide` gains the split it is given),
  `internal/runner/runner.go` (the call site), `internal/rigor/table.go` (used,
  not changed, if T2.3's shape holds).
- **Tests.** `internal/runner/outcome_test.go` **(new)**, with cases added to
  `TestDecide` in `internal/runner/pure_test.go` so the existing four reasons stay
  held by the test that holds them today: the existing four reasons are
  unchanged for a run with no tolerated findings; a tolerated finding yields a
  ready PR with a reason that names the mode and the count; an unscored finding
  still fails in every mode; and **Critical is byte-identical**: the same records
  produce the same outcome and the same reason string as `main`'s `Decide` does.
  That last one is written as a table of (records, mode) → (ready, reason) whose
  Critical column is the pre-change behaviour, so a later edit that moves it is
  caught rather than argued about.
- **Red step.** A tolerated finding at Startup still returns `false` with
  `review round N still has M findings` — the whole feature, in one failing
  assertion.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'Decide|Outcome' -count=1 -timeout 30m`
- **Depends on.** T2.3, **and T4.5** — D9's order: a mode may not tolerate a
  finding before there is an item file to put it in, so the task that files one
  lands first and this task is the first that needs it to have landed.
- **Reviewer.** senior (in-loop) **plus** the owner, because this is the one
  change in the plan that can make a PR ready that was not ready before. Reviewer
  tier is in-loop; the owner's eye is what §7's first two questions and the
  OWNER APPROVAL GATE's third item exist for.
- **Commit.** `runner: the mode decides which findings block, inside Decide`

**T3.2 — the mode supplies the loop counts**
- **Files.** `internal/rigor/rounds.go` (the table: `roundDefaults`, one
  `{inner, senior}` pair per mode, `DefaultRounds` and `AdversarialDefault`),
  `internal/runner/plan.go` (`planOf` with the `setRounds` and `reviewRounds`
  helpers that fill an unset knob from the row and clamp it to the recipe's bound),
  `internal/config/defaults.go` (which no longer fills the two knobs, so an unset 0
  reaches the mode), and `internal/cli/budget_preview.go`, which counts its stages
  at the project's own mode.
- **Tests.** O3's answer is the table, asserted mode by mode with nothing set
  (POC 1/1, Startup 2/2, Pro 3/3, Critical 3/5, Hotfix 1/1); each precedence step
  — the task override, the recipe's `max_rounds`, the repository's
  `agent.review_rounds` and `agent.first_line_rounds`, and the project layer —
  replaces the row, up and down, within the recipe's bounds (inner 1–3, senior
  1–10), which are the only ceiling; a mode raised between rounds by an area or a
  floor (T1.4, T3.5) supplies the senior count where the rounds are decided, so it
  bounds the rounds that remain rather than the rounds already spent; and a
  repository with no `rigor:` block resolves Pro and runs Pro's row.
- **Red step.** The per-mode test fails: the mode is not an input to the counts,
  and the clamp to two holds a row below it.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor ./internal/config -count=1`,
  `GOFLAGS=-p=2 go test ./internal/runner -run 'Round|Plan|FirstLine' -count=1 -timeout 40m`,
  and `GOFLAGS=-p=2 go test ./internal/cli -run 'BudgetPreview' -count=1 -timeout 40m`
- **Depends on.** T3.1. **O3 is answered** (2026-10-31), which unblocked this task.
- **Reviewer.** senior (in-loop).
- **Commit.** `rigor: each mode sets the default first-line and senior round counts`

**T3.3 — the PR body says what was left open, and by which mode**
- **Files.** `internal/runner/prflow.go` (`finalSection`, `reviewsPart`).
- **Tests.** The PR-body golden tests the package already has, extended: a run
  with tolerated findings names each one with its label and its mode; a run with
  none is unchanged; and the mode line T1.6 added still appears in every PR a
  mode-aware run opens, including a run with zero findings (D18's audit
  requirement does not depend on there being something to tolerate).
- **Red step.** The golden does not contain the sentence, and the assertion that
  it does fails.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'PR|Body|Section' -count=1 -timeout 30m`
- **Depends on.** T3.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `runner: name the mode and what it left open in the PR body`

**T3.4 — persist the findings, and keep the schema's version**
- **Files.** `internal/runstore/runstore.go` (the finding list on the run),
  `schemas/result.schema.json`. This is F19's task, and it should land *with*
  the in-job-review folder's own Q8 work rather than a second time — D9's note
  says so, and a reviewer should check whether it has already landed before
  writing it.
- **Tests.** `schemas/schemas_test.go`'s `TestResultSchemaAcceptsRunnerRecords`
  (the pin that makes a record field and its schema entry one commit);
  `internal/runstore`'s own round-trip tests; and a test that an *older* record —
  one written before this field — still decodes, which is the compatibility claim
  the unchanged `version` rests on.
- **Red step.** The schema test fails on the new field with
  `additionalProperties: false`, and the older-record test is written first to
  prove the field is additive.
- **Verification.** `GOFLAGS=-p=2 go test ./schemas ./internal/runstore -count=1 -timeout 20m`
- **Depends on.** T3.1. **Depends on D9's version note** (recommendation: keep
  `version: 1`).
- **Reviewer.** senior (in-loop).
- **Commit.** `runstore: keep the findings a mode tolerated, with the count`

**T3.5 — the resolved mode decides readiness, and names itself in the reason**
- **Files.** `internal/runner/outcome.go` (`Decide`'s readiness answer and its
  reason, reading the mode T1.4 resolved), and the call site in
  `internal/runner/runner.go`'s `finalize` that decides before the PR is opened.
  The *resolution* is T1.4's; this task is what makes a raised mode act.
- **Tests.** A run whose mode was raised after a round — by an area or by a floor
  — and whose last verdict still carries what the raised mode does not tolerate
  does not become ready, and the reason names the mode, the area or the path, and
  the moment it was raised. A run whose mode never rose is unchanged (D19). A mode
  raised at `finalize`, too late to buy rounds, leaves the PR a draft rather than
  ready: D22's third moment with nothing acting on it is the failure mode this
  task closes.
- **Red step.** The test fails: `Decide` answers today's question and ignores the
  mode, so a raised mode changes no answer.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'Floor|Ready|Decide|Area' -count=1 -timeout 30m`
- **Depends on.** T3.1 (the policy), T1.4 (the resolution it reads).
- **Reviewer.** senior (in-loop), with T1.5's operator having already ruled on
  the floors themselves.
- **Commit.** `runner: a raised mode decides readiness and says so in the reason`

### Slice 4 — the backlog surface, and the Markdown backlog

T4.1 and T4.2 are the surface the tolerated findings are read on. T4.3–T4.7 build
the backlog the owner ruled on 2026-10-20 (the reconciliation's §1.3, D26–D32):
one Markdown file per item under `docs/backlog/`, a generated index two guards
hold, a filing command, a per-mode rule and a way to close an item. They are one
slice because they are one subject, and five tasks because each has to merge
alone — T4.3 changes no Go code at all, T4.4 adds no behaviour to a run, T4.5 is
the first that writes an item from a run, and T4.6 and T4.7 are rules about what
is already written. **Their order is D9's**: T4.3, T4.4 and T4.5 are picked up
before Slice 3, and T3.1 waits for T4.5 (§2, §6).

**T4.1 — `fugaro diagnose` shows the score and the mode**
- **Files.** `internal/cli/diagnose.go` (`Diagnosis.Findings`, `seniorRound`,
  the human row), `writeJSON` in `internal/cli/budget.go`, and
  `console/docs/json-contract.md` §6.1 (the console's input contract).
- **Tests.** A diagnose golden over a stored transcript with scored findings; the
  JSON shape asserted field by field, because the console reads it and a renamed
  field is a silent break there; and an unscored finding rendering exactly as it
  does today, so old transcripts stay readable.
- **Red step.** The golden lacks the score columns; the JSON test fails on the
  missing fields.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Diagnose' -count=1 -timeout 30m`
- **Depends on.** T3.4 (the findings must be persisted to be read back).
- **Reviewer.** senior (in-loop).
- **Commit.** `cli(diagnose): read back what a mode left open`

**T4.2 — the run's report carries the same list**
- **Files.** `internal/runner/report.go`.
- **Tests.** The report golden carries the tolerated findings and the mode, and
  differs from today's by nothing but the mode line when a run tolerated nothing.
- **Red step.** The golden does not carry the list.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'Report' -count=1 -timeout 30m`
- **Depends on.** T3.4.
- **Reviewer.** senior (in-loop).
- **Commit.** `runner: the run's report lists what the mode tolerated`

**T4.3 — the backlog's folder, its format, and its first items**
- **Files.** `docs/backlog/FORMAT.md` **(new)** — the front-matter table, the id
  rule, the body headings and the closing rule, as D26, D27 and D31 state them —
  the first item files under `docs/backlog/`, and
  `internal/cli/docs_backlog_test.go` **(new)**, the folder's own pin. This task
  also moves §4's pin-count row in the reconciliation, because it writes the
  forty-ninth `docs_*_test.go` in `internal/cli`.
- **Tests.** The new pin parses every `docs/backlog/*.md` and asserts the eight
  required front-matter keys, the four body headings, `status` in the three-word
  vocabulary, an `id` equal to the file name's first two segments, and no two
  items sharing an id. Written red first, against a folder that does not exist.
- **Red step.** The pin fails: there is no `docs/backlog/`, so the first item it
  demands is absent.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Backlog' -count=1 -timeout 30m`
- **Depends on.** T0.1, and nothing else: an item filed by hand needs no mode to
  exist, and `mode: none` is what it records until T4.6.
- **Reviewer.** senior (in-loop).
- **Commit.** `docs(backlog): one file per item, and the format they follow`

**T4.4 — the index, the tool, and the two pins**
- **Files.** `internal/backlog/` **(new)** — the item parser, the `Render` D28
  specifies and the id allocation D26 defines — `internal/cli/backlog.go` **(new)**
  for the read-only pair `fugaro backlog index` and `fugaro backlog list`,
  `docs/backlog/README.md` (generated, carrying `internal/clidoc`'s header line),
  `console/docs/json-contract.md` §6 if `list` ships a `--json`, and
  `website/content/cli-reference.md` (regenerated).
- **Tests.** Two guards of different kinds (F45): the **regenerate-and-compare**
  pin, which renders the folder in memory and compares byte for byte with the
  committed index (`internal/clidoc`'s model), and the **tree-derived completeness**
  guard, which counts the item files, asserts a row for every id in the tree and
  no row for an id that is not, so a generator that silently skips an item is
  caught by the guard that does not consult the generator. Plus determinism (two
  renders are equal, and no clock is read) and an item added without regenerating
  the index going red.
- **Red step.** The regenerate-and-compare pin fails on a missing index; the
  completeness guard fails on a folder with items and no rows.
- **Verification.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/backlog ./internal/cli -run 'Backlog|Index|CliRef' -count=1 -timeout 30m`
- **Depends on.** T4.3.
- **Reviewer.** senior (in-loop).
- **Commit.** `cli(backlog): the index is generated, and two guards hold it`

**T4.5 — filing an item: the command, and the run that files**
- **Files.** `internal/cli/backlog.go` (`add`), `internal/backlog/write.go`,
  `internal/runner/runner.go` (`finalize`, which files the run's items before the
  pull request is settled) and `internal/runner/prflow.go` (the status section
  names the item ids beside the findings),
  `internal/runstore/runstore.go` + `schemas/result.schema.json` (the filed ids,
  additive, with `version` staying 1 per D9's note), and
  `website/content/cli-reference.md` (regenerated).
- **Tests.** `add` writes the file, allocates the id, refuses a duplicate and
  regenerates the index; a run at a mode that files produces the item in its own
  branch (asserted on the tree the run pushes, not on the working directory) and
  its ids on the record; the pull request body names them; and the record decodes
  with and without the new field, which is `TestResultSchemaAcceptsRunnerRecords`
  extended rather than replaced.
- **Red step.** There is no `fugaro backlog add`, and `finalize` writes no item,
  so a run at a mode that tolerates something leaves nothing behind.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/backlog ./internal/runner ./internal/cli ./internal/runstore -run 'Backlog|Item|Finalize|Schema' -count=1 -timeout 40m`
- **Depends on.** T4.4; **T1.2 supplies the mode**, and T1.4 the area name — until
  T1.4 lands an item filed by a run records `area: repo`, and this task says so in
  its commit message rather than guessing an area. **It also depends on D29,
  whose value is still the owner's**: the ruling settles that a project's items
  live in one repository the project configuration names, and not which repository
  that is. Until the owner names it, this task is built for this repository's own
  installation, the key is not added, and the commit message says which value it
  assumes. Slice 3 depends on this task (D9), so the value is worth naming before
  the run loop is picked up.
- **Reviewer.** senior (in-loop).
- **Commit.** `runner(backlog): a run files the items it left open`

**T4.6 — how each of the five modes files, and nothing else**
- **Files.** the filing decision beside the policy T3.1 owns, in `internal/rigor/`
  — D30's table, so the per-mode rule is one table rather than a condition inside
  the runner.
- **Tests.** POC files nothing and its pull request body's line is the whole
  record; Startup and Pro file one item per tolerated finding; Critical files none
  of its own (D7 tolerates nothing) and a run whose mode is unconfigured files
  nothing new (D19); Hotfix files its follow-up item always, including when it
  tolerated nothing; and no mode can suppress an item the owner asked for by hand.
- **Red step.** No mode files anything: T4.5's run-side write is reached by no
  mode, so a Pro run that tolerated three findings files none.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor ./internal/runner -run 'Rigor|File|Item|Mode' -count=1 -timeout 30m`
- **Depends on.** T4.5, T3.1, T3.4.
- **Reviewer.** senior (in-loop).
- **Commit.** `rigor: the mode decides what is filed, and nothing else`

**T4.7 — closing an item, and the one-backlog practice**
- **Files.** `docs/backlog/FORMAT.md` (the closing rule), the item a fixing pull
  request flips, `docs/dogfooding.md` (the practice sentence: a deferral is filed
  as an item, and nothing new is written in the shape F46 counts), and — D32's
  answer takes the recommendation that was written for it — the `docs/plans`
  sections F46 names, converted in one mechanical pull request, each keeping its
  own text with a one-line pointer to the items it became.
- **Tests.** A `closed` item with no `## Closed by` line naming the pull request
  fails the folder's pin; a `dropped` item with no reason fails it; and flipping
  `status` without regenerating the index fails T4.4's regenerate-and-compare pin,
  which is the assertion that makes "closed in the same commit" a rule the tree
  holds rather than a sentence here.
- **Red step.** The closing rule is unwritten and nothing enforces it: an item
  flipped to `closed` with a stale index passes everything.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Backlog|Docs' -count=1 -timeout 40m`
- **Depends on.** T4.4, and D32 for the sweep half — which lands as its own commit
  with the count of converted rows in its message, because that count is volatile
  and this plan does not state a figure without its command.
- **Reviewer.** senior (in-loop), and **operator** for the sweep half, which
  rewrites documents the owner wrote.
- **Commit.** `docs(backlog): an item is closed by the pull request that fixes it`

### Slice 5 — the task file, and Hotfix

**T5.1 — a task may ask for a mode**
- **Files.** `internal/task/task.go` (the `rigor:` field and its validation),
  `internal/cli/recipes_skew.go` (the `…Since` constant and `checkImageSince`),
  and the remedy path the user is told about (`fugaro image refresh`).
- **Tests.** The parse tests: `rigor: hotfix` parses; `rigor: mode: hotfix` (the
  collision F22 warns about) is a *parse error*, not a silent reading of the
  recipe mode; an older image refuses the task with the skew message and the
  remedy, which is the test that makes the ordering honest. **The key is `rigor`,
  never `mode`** (F22).
- **Red step.** A task file with `rigor:` is refused by
  `DisallowUnknownFields`, loudly — which is F22's point and the reason this is
  safe to add.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/task ./internal/cli -run 'Task|Skew|Since' -count=1 -timeout 30m`
- **Depends on.** T1.3, T3.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `task: a task may raise its rigor, and an old image says so`

**T5.2 — a follow-up keeps its PR's mode, and `--mode` may only raise it**
- **Files.** `internal/cli/run.go` (the follow-up path), `internal/followup/`
  (read, not changed), `internal/runstore/runstore.go` (reading the PR's
  recorded mode).
- **Tests.** `fugaro run --pr N --mode poc` on a PR recorded at Pro refuses to
  lower it, and says which mode the PR carries; raising it succeeds and the run
  record shows the raise; a follow-up with no `--mode` uses the recorded one; and
  a comment can never set the mode at all (there is no path from `followup.Select`
  to the mode, asserted by a test that the comment body's nonce block has no way
  to carry one).
- **Red step.** The lowering test fails: the flag is accepted and lowers.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/followup -run 'Followup|Mode' -count=1 -timeout 30m`
- **Depends on.** T1.6, T5.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `runner: a follow-up may raise its mode and never lower it`

**T5.3 — Hotfix's deterministic gate**
- **Files.** `internal/rigor/hotfix.go` (new: the diff rule of D14, whose every
  input is one the project configured — the recipe's own `check`, the protected
  paths the project declares in `rigor:`, the diff-size and file-count bounds with
  their tunable defaults, and the conservative branch when the rule cannot
  decide), reading `internal/runstore/changedfiles.go`, `internal/config`
  (the tunable bounds and the declared paths) and the recipe it selects.
- **Tests.** A diff touching `.github/workflows/` or a credential-file path is
  refused whatever the agent says — the two universal built-ins, and the only two
  this rule hard-codes; a diff touching a path the *project* declared is refused
  for the same reason; a diff over the configured size or file-count bound takes
  the conservative branch, and the same diff under a repository's own larger bound
  does not, which is the test that proves the bounds are **configured** rather
  than written into the runner; a diff under the bound with the recipe's `check`
  passing takes the fast branch; and the agent's stated confidence is recorded and
  **never read** — a test that asserts the rule's inputs do not include it, which
  is D14's whole point.
- **Note.** D14's answer is that this rule must work for **any** project: no path
  of this repository's own is written into it, and a project that declares no
  protected paths still gets the two built-ins and its own recipe's check.
- **Red step.** The refusal test fails: nothing inspects a Hotfix diff.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/rigor -run 'Hotfix' -count=1 -timeout 20m`
- **Depends on.** T1.5, T3.5.
- **Reviewer.** **operator** — it is the gate on the mode with no review, and §5
  puts it in the class a run must not do.
- **Commit.** `rigor: a hotfix's gate is the diff, not the agent's confidence`

**T5.4 — the ready PR**
- **Files.** `internal/runner/runner.go` (`finalize`, which builds
  `PRSpec{Draft: !ready}` and calls `settlePR`), `internal/runner/prflow.go`
  (`applyReady`), and the label naming in `internal/gitops`/`gitprov` as they
  stand.
- **Tests.** A Hotfix run whose gate passed opens a **ready** PR and gets its
  labels applied (`applyReady`'s existing behaviour, asserted rather than
  assumed); a Hotfix run whose gate failed opens a draft with the reason in the
  body; and every other mode still opens a draft (D11), asserted for all four.
- **Red step.** The ready assertion fails: `finalize` drafts everything.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner -run 'Finalize|Ready|Draft' -count=1 -timeout 30m`
- **Depends on.** T5.3.
- **Reviewer.** senior (in-loop); the ready-PR rule itself is unchanged and D11's
  answer states it — a pull request is ready if and only if `runner.Decide` says
  so, the runner is what marks it ready (`markPullRequestReadyForReview`), and no
  person is in that path in any mode.
- **Commit.** `runner: hotfix opens a ready PR, and only hotfix does`

**T5.5 — the recorded follow-up task**
- **Files.** `internal/runner/` (the run's closing output) and
  `internal/runstore/runstore.go` if the text is kept on the record.
- **Tests.** A finished Hotfix run produces a task file that parses
  (`task.Parse`), names the PR, the paths and the area's normal mode, and whose
  mode is **not** Hotfix; and the file is *not* run automatically (D15: there is
  no scheduler, and the test asserts the absence by asserting that nothing in the
  run's exit path starts one). **The item half** (D15's amendment, D30): the run
  also files one item, whose `mode:` is the area's normal mode and never `hotfix`,
  and whose `filed-in:` names the run — so a reader of the backlog sees the
  follow-up without reading the pull request.
- **Note.** The task file is what to run; the item is where it is written down.
  If Slice 4 has not landed, the item half waits for T4.5 and the task file half
  ships without it — which is why this task's **Depends on** line names both.
- **Red step.** No such text is produced.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/runner ./internal/task -run 'Hotfix|Followup' -count=1 -timeout 30m`
- **Depends on.** T5.4, and T4.5 for the item half.
- **Reviewer.** senior (in-loop).
- **Commit.** `runner: a hotfix records the task that revisits it`

### Slice 6 — cost

**T6.1 — the per-mode budget defaults**
- **Files.** `internal/config/defaults.go` (the defaults as fractions of the
  deployed cap), `schemas/fugaro.schema.json` if the value is configurable, and
  the enforcement read path `internal/runner/policy.go` / `internal/budget/lease.go`
  (read, not changed).
- **Tests.** Every default is strictly below the deployed cap and cannot exceed
  it whatever the config says (F15's structural claim, asserted rather than
  documented); the order is POC < Startup < Pro < Critical; and each default has
  a floor high enough to complete a verify stage, which is asserted as "a run at
  this cap finishes the smoke check" using the existing stage-bound arithmetic.
- **Red step.** No mode contributes a default.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/config ./internal/budget ./internal/runner -run 'Budget|Policy|Lease' -count=1 -timeout 30m`
- **Depends on.** T1.3. **Depends on D16** if the owner wants a different
  ordering.
- **Reviewer.** senior (in-loop).
- **Commit.** `config(rigor): a mode lowers the ceiling and never raises it`

**T6.2 — `fugaro budget preview --mode`**
- **Files.** `internal/cli/budget_preview.go`, `website/content/cli-reference.md`
  (regenerated).
- **Tests.** The preview's arithmetic with a mode equals the arithmetic with the
  mode's recipe named explicitly (which is the honest claim: the mode changes
  `StageBound` through the recipe and nothing else); the output still says
  ceiling and not forecast; and the `typical:` line is absent when fewer than
  five real-cost runs exist, in every mode.
- **Red step.** The flag is unknown; the equality test cannot compile.
- **Verification.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'BudgetPreview|CliRef' -count=1 -timeout 30m`
- **Depends on.** T6.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `cli(budget): preview a mode's ceiling, still not a forecast`

**T6.3 — `typical:` per mode, now that every run's row carries its mode**
- **Files.** wherever the empirical `typical:` line is computed, and its `--json`
  (`internal/cli/`). The mode reaches the row in T1.6; this task is the
  statistic, not the field.
- **Tests.** The line is computed per mode once that mode has enough finished
  runs, and falls back to the all-modes figure when it does not; a run whose
  record carries no mode (one written before T1.2) counts toward the all-modes
  line and does not fail the decoder.
- **Red step.** The line is one figure across every mode, whatever the records
  say.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Typical|Budget|Runs' -count=1 -timeout 30m`
- **Depends on.** T1.6 (the row), and D9 as amended by the backlog ruling: the
  ledger is ruled (D26–D32), and this task is the statistic rather than the
  carrier.
- **Reviewer.** senior (in-loop).
- **Commit.** `cli(budget): the empirical typical: line, per mode`

### Slice 7 — the documentation and the practice

**T7.1 — `docs/rigor-modes.md`, the product document**
- **Files.** `docs/rigor-modes.md` **(new)**, and `internal/cli/docs_rigor_modes_product_test.go`
  **(new)** — the pin. `docs/recipes.md` and `docs/project-layer.md` each have a
  guard of their own (`internal/cli/docs_recipes_test.go`,
  `internal/cli/docs_project_layer_test.go`), but **`docs/dogfooding.md` has
  none**: it is only read by other folders' tests
  (`docs_base_test.go`, `docs_project_wide_git_test.go`, `docs_providers_test.go`
  and `docs_in_job_review_test.go`, which names it), so the new product document
  gets its own new pin rather than an extension of an existing one.
- **Tests.** The new pin (the document's status line, the fact that it is a
  product document and not a plan, and that every mode in its table is a mode the
  code has — read out of `internal/rigor`'s own vocabulary rather than restated);
  `TestScopeTableMatchesDocs` if the scope table changed.
- **Red step.** The guard is written first and fails on a missing document.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1 -timeout 40m`
- **Depends on.** T4.1, T5.4, T6.2 (it documents all three).
- **Reviewer.** senior (in-loop), and the owner for the words.
- **Commit.** `docs: rigor modes, as a product surface`

**T7.2 — the practice the modes formalise, in `docs/dogfooding.md`**
- **Files.** `docs/dogfooding.md` (the sentences F30 quotes, now with the mode
  that automates each), and `internal/cli/docs_dogfooding_test.go` **(new)** —
  the document has no pin today, and this task is where it gets one.
- **Tests.** The new pin, asserting that each practice F30 quotes names a mode,
  which is what makes the mapping checkable rather than prose. It also holds the
  sentences themselves, so the document the modes formalise cannot drift out from
  under them.
- **Red step.** The pin is written first and fails on the sentences carrying no
  mode name.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1 -timeout 40m`
- **Depends on.** T7.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `docs(dogfooding): which mode automates which habit`

**T7.3 — the owner sets their own repository's mode** *(an owner action)*
- **Files.** `fugaro.yaml` (this repository's own), after `rigor:` exists.
- **Tests.** The repository's own config is validated by `fugaro validate` and by
  the corpus suite.
- **Red step.** None: this is a configuration change by the owner, and it is
  listed here so it is not forgotten rather than so an agent does it.
- **Verification.** `fugaro validate`
- **Depends on.** T1.3, D19.
- **Reviewer.** **operator** — the owner.
- **Commit.** `fugaro.yaml: run this repository at <mode>`

### Slice 8 — the live checks, and the migration note

**T8.1 — Check 119, written**
- **Files.** `docs/gcp-live-checklist.md` (the section, and the register's
  paragraph), `docs/coordinator/README.md` (the check-number row, which
  `docs_coordinator_test.go` holds), and this plan's §3 if the number moved.
- **Tests.** `docs_coordinator_test.go`'s own guards — the row and the section
  are held to each other, and the register's numbering rule is asserted by the
  test rather than restated here.
- **Red step.** The coordinator pin fails: the register states the number and the
  checklist has no such section (or the reverse).
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1 -timeout 40m`
- **Depends on.** T7.1, and the register being free at its merge (§3's
  renumber-second rule).
- **Reviewer.** senior (in-loop).
- **Commit.** `docs(live-checks): Check 119, the modes on a real repository`

**T8.2 — the live checks, run** *(an owner action, not a task an agent can finish)*
- **Files.** `docs/gcp-live-checklist.md` (the section's status line, from NOT
  RUN to its result), once it has been run.
- **Tests.** None: a live check is the unit of testing here, and §9's table is
  what it proves.
- **Red step.** The check is written NOT RUN, and it is red in the sense that
  matters: nobody knows whether the modes work on a real repository.
- **Verification.** The five live checks in §9.
- **Depends on.** T8.1, and a real repository configured with at least two modes.
- **Reviewer.** **operator** — the owner, at their own terminal.
- **Commit.** `docs(live-checks): Check 119, <result>`

**T8.3 — the migration note**
- **Files.** `docs/releases/` (the 0.10.0 notes), where the default is stated:
  **Critical, today's behaviour, nothing relaxed** (D19).
- **Tests.** The release-notes pin.
- **Red step.** The note does not state the default.
- **Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Release' -count=1 -timeout 40m`
- **Depends on.** T8.1.
- **Reviewer.** senior (in-loop).
- **Commit.** `docs(releases): 0.10.0 runs at Critical until you say otherwise`

## 9. The live checks the owner would run

A container cannot run these, and each one is the reason a claim in this plan is
a claim rather than a fact.

| # | What | Why a container cannot |
| --- | --- | --- |
| 1 | a real POC run on a scratch repository, then `fugaro diagnose` | a live model call and a real PR |
| 2 | the same task at Critical, diffed against #1 | two real runs, compared |
| 3 | a Hotfix run touching a floored path — refused by the mode, not the guard | a repository configured with a floor |
| 4 | `fugaro budget preview --mode` against a finished run's gateway figure | the gateway's price is not in the tree |
| 5 | a merged Hotfix PR's post-merge CI | CI fires on the provider |
| 6 | a project whose two repositories differ only in their own `rigor.default` | a published project layer and two repositories |
| 7 | a run started with `--ref` at a branch whose `rigor:` disagrees with the base branch's | the answer D25 is waiting for: which revision the mode came from |
| 8 | a run whose diff matches two areas, and a run whose second round reaches a new one | a real diff in both areas, and a live multi-round agent |
| 9 | `fugaro watch` on a live run after `fugaro init --firebase-project` redeploys the rules | a deployed database, and the old-rules path F40 records |
| 10 | the pull request that fixes a filed item — the flip and the regenerated index in one commit (D31) | a real pull request, read as a diff by a person |
| 11 | two branches filing items on the same day, merged in either order (D26, D28: a rename, and one command to reconcile the index) | two real branches and the provider's own conflict |

Checks 1–11 are **Check 119**, written by T8.1 and run by T8.2. Checks 6–9 are the
scope ruling's own (the reconciliation's §1.2 and §7); checks 10 and 11 are the
backlog ruling's (§1.3); and check 9 is the only one that needs a deploy before
it can be run at all.

**One more check is proposed and not numbered** (D24's answer). The live
`fugaro watch` row needs a new registry child, a rules regeneration with its
golden file, and a deploy — and the deploy is the owner's to run, not a run's
(§5's last constraint). It is therefore written as a plan part with an operator
step, and it is **not** folded into Check 119's numbering: the register in
`docs/gcp-live-checklist.md` is not touched by it, and T8.1's task writes it as a
step beside the check rather than as a check. What it says: regenerate the rules
with the new child, deploy them, start a run, and read the mode in
`fugaro watch` while the run is in flight — with the drop-and-warn path T1.7 takes
from `Recipe` as the thing to watch for on an installation whose rules are older.

## 10. Risks

| Risk | Where it comes from | What holds it |
| --- | --- | --- |
| a bad PR becomes ready because a threshold is loose | this feature's entire point, and it is now the behaviour of every unconfigured repository (D19's Pro default) | D4's fail-closed rule; §7's first two questions — Critical mode's byte-identical table, and Pro's table reviewed as the default rather than as one mode among five |
| the reviewer scores unreliably, so the thresholds are noise | **U1**, unverified | T2.4 measures before the thresholds are trusted; D4 keeps the failure safe |
| a credential path is missed by the floors | D18's declared-plus-built-in split | T1.5's built-in set and its operator review; the floors can only be raised |
| schema drift between the record and `result.schema.json` | F20 | the existing `TestResultSchemaAcceptsRunnerRecords`, which is the red step of T3.4 |
| a mode becomes a second readiness rule | F4 | §5's first constraint; §7's second review question |
| the config key collides with `task.Spec.Mode` | F22 | the key is `rigor` (T5.1), and the collision is a parse error rather than a misreading |
| the check number collides with an open claim | §3 | the register's own renumber-second rule, owned by T8.1 |
| a branch sets its own rigor, and the run obeys it | F35, D25 | D25's answer is the real one — the mode is read from the **base branch** for every run kind, so the branch under review never supplies it; the base branch is read as the repository's default branch (D25, "as T1.2 read it"), because `git.base_branch` is a key the branch being reviewed writes for itself; the built-in floors (T1.5) hold whatever the revision says |
| the project default is read as a floor by a reader | D21's line between a default and a floor | D5's part three and D18: a floor is policy, and `docs/project-layer.md` §2 says "only tightens" of exactly that kind of value |
| a live `watch` deploy breaks an installation whose rules are older | F40 | the drop-and-warn path T1.7 takes from `Recipe`, tested; the marker-and-halt path is the alternative the answer did not take, and §9's unnumbered check is where the deploy is watched |
| cost: the modes make runs cheaper in theory and not in fact | **U2**, unverified | T6.3's per-mode reporting and D17's refusal to forecast |
| a Hotfix ships a wrong fix with the record agreeing it was confident | D14's weak point | the gate is the diff, not the agent's number (T5.3), every input to it is one the project configured, and the follow-up task exists (T5.5) |
| the Hotfix gate knows nothing about a project's own sensitive paths | D14's answer: the rule is built from configuration, never from this repository's paths | the declared `rigor:` paths are read as inputs (T5.3), so a project teaches the gate its own paths; the two universal built-ins are the floor under that |
| the backlog's index conflicts on every parallel filing | F46's precedent: two pull requests writing one list | D26 (one file per item) and D28 (the index is regenerated, never merged); U12 is the unverified half of the claim |
| two branches file the same day's Nth item | D26's allocation has no central counter | the pin's duplicate-id guard (T4.3), and the rename-second rule the registers already use; the collision surfaces on the branch that would merge second, where a rename is cheap |
| the backlog becomes a second backlog beside the `docs/plans` notes | F46, D32 | the practice sentence (T4.7), the conversion count in that pull request's own body, and §7's first review question |
| an item is closed and the index left stale, or an item deleted | D31 | the regenerate-and-compare pin: the flip and the regeneration are one commit or the tree is red |

## OWNER APPROVAL GATE

**This gate is passed: the owner answered §5's decisions (D1–D32) on 2026-10-20, and the plan is approved to build on the branch `rigor-modes`.**

1. **The decisions are answered.** D1–D32 carry the owner's answers, each in place
   of the recommendation it displaces, with the displaced recommendation kept
   beneath it; a task that depends on an answer says so in its **Depends on**
   line. **One value is still the owner's to name**: **D29**'s designated
   repository for a project's items — the shape is settled, the repository is not
   — and T4.5's **Depends on** line carries it, so T4.5 and everything Slice 3
   waits on are the tasks to check before picking them up.
2. **D19's answer is the one that changes behaviour, and it is deliberate.** The
   default is **Pro**, not Critical, so the first cut **does** change what "ready"
   means for a repository that configures nothing — its runs start tolerating what
   Pro tolerates. Every slice and every claim written on the assumption that an
   unconfigured repository does not change is scoped to **Critical mode**; the
   security-sensitive tier stays Critical (D18's floors), the one-line pin
   `rigor.default: critical` restores today's behaviour exactly, and **Pro's
   threshold table is now the behaviour of every unconfigured repository**, which
   is why §7 gives it its own review question. T8.2's migration note is what tells
   those repositories about it.
3. **T3.1 still gets a human eye.** It is the one change that can make a pull
   request ready that would not have been ready before, and it is now the change
   that decides what every unconfigured repository tolerates. The plan asks for it
   to be reviewed by the owner, not only by the in-loop reviewer.
4. **T1.5, T5.3 and T7.3, and the live checks, are not run by a Fugaro run.**
   They are the security floor, the gate on the mode with no review, the owner's
   own configuration and the live checks — §5's last constraint. A run that picks
   one up has found a bug in this plan. D24's watch deploy is the same class and
   is written in §9 as a proposed, unnumbered step: the owner runs it.
5. **Slice 0 is already written.** The documents in this folder are the
   deliverable of the analysis — including the answers recorded on 2026-10-20 —
   and T0.2 (the pin test) landed with them, because a guard on a plan is not a
   step of the plan.
6. **Nothing is timed.** No date, no sequence, no estimate — the slices are
   ordered by dependency and by what keeps `main` green, which is the only
   ordering this document can support. The one order the owner did give is D9's:
   the backlog's tasks come first, and Slice 3 depends on them.
