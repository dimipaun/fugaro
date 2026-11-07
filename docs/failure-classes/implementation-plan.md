# A closed failure class on every run record, and a failures report: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Nine tasks, **FC1–FC9** — the
design's own FC1–FC8 (its §10, kept by name, by order and by edge) plus **FC9**, which the plugin
finding F13 adds — each with its files, its interfaces, its named tests **and the command that runs
them**, its **red step**, the mutation that holds it, its **reviewer tier**, its **SEC** and
**owner-run** flags, what it depends on and its commit. **Every citation below was resolved against
the tree of `main` at `56c91f66` on 2026-11-07 while writing it**, and where the design's own citation
had drifted the reconciliation's line is used and the drift is named (F18, F19).

**Written to:** [spec.md](spec.md) — the design, `docs/design/failure-classes.md`, byte for byte — and
[reconciliation.md](reconciliation.md). **Where a task below and the reconciliation disagree, the
reconciliation's findings (F1–F33) and decisions (D1450–D1469) win, and the task is wrong.**

**Nothing here is an approval.** The design is a design and this is a plan (§10 of the design and
[README.md](README.md) say so); each task's gate is in §7, and most of them name a decision row whose
owner's-answer cell is empty.

**Rules every task obeys** (this repository's own, not this plan's invention):

- a **red step** first: the named test fails for the reason the task exists, and the failure is quoted
  in the pull request;
- **targeted tests only** — never a whole package that costs half an hour. `internal/cli` and
  `internal/runner` are ~25 minutes each, so every command below names its tests and carries
  `-count=1`, and the runner's carry `-timeout 40m`;
- **hermetic tests only**: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`; no live call, no
  bucket, no registry, no network (a live check is §6, and it is the owner's);
- **no secret-shaped literal** anywhere, in a test, a fixture or a doc;
- **a field never changes meaning** (F27): `failure_class` is a new name beside `reason`; `reason`
  keeps its text and its meaning byte for byte; `result.json`'s `version` stays `const 1`; the enum
  grows inside the field, so a value added later moves no version either;
- **comments explain why, never what**; no TODO, no section header, no restatement of a name;
- **the docs pins move in the same commit as the thing they pin** — `internal/cli/docs_*_test.go`
  (F11, F28, F32) and `ee/clidoc`'s `TestCliReferenceIsCurrent` (`ee/clidoc/cliref_test.go:26`, which
  holds the committed `website/content/cli-reference.md` to what `go run ./ee/clidoc` renders), so a
  task whose test reddens in another package carries both;
- **`fugaro update` stays the whole upgrade** (F29, §5): no task adds a command, a policy node, a
  schema version or an image contract, and no task owes an operator step;
- **`.github/workflows/` is never touched by any task** (F30);
- **a fix round for a review finding adds no functionality**: a finding that needs a new behaviour is
  reported to the owner, not fixed inside a fix round.

---

## 1. The order, and what forces it

| Level | Tasks | What forces the level |
| --- | --- | --- |
| **L0** | FC1 — the vocabulary, the field and its schema | Nothing: it is the fact every other task reads, and it changes no behaviour (a field nothing writes yet is what makes FC2 safe to write and FC5 honest). Its own decision is **D1453** |
| **L1** | FC2 — one writer for the terminal status (SEC) | FC1, and **D1452** (where `recordEnd` lives) and **D1456** (that it stays one task). It is the long pole (§1.2) |
| **L2** | FC3 — the classes the runner knows structurally | FC2, and **D1455** (where the halt table lives, so the writer and the reader cannot disagree) |
| **L3** | FC4 — the classifier over the agent's own message (SEC) | FC2 alone. Independent of FC3: it reads the agent's message, not the runner's own state |
| **L3** | FC5 — the legacy reader (the row's key) | FC1 and FC3, and **D1454** (`ClassOf`'s home and its name, F20). It carries the console contract's row on the row's key, because the reflect pin reddens in the same commit (F32) |
| **L4** | FC6 — `runs ls --group-by failure_class` | FC5, and **D1458** (the `groups` key is `omitempty`, and the phone's document never carries it) and **D1465** (`share` is a share of runs) |
| **L4** | FC7 — the Failures table and the two rules | FC5, and **D1463** (which document owns the rules' prose) and **D1466** (the table ships; the rules are the half that may be dropped) |
| **L5** | FC8 — the documents, the contract and the skills | FC6 and FC7, and **D1460** (diagnose prints and carries the class), **D1468** (`docs/design/v1.md` §4.6) and **D1469** (the slot). It writes no behaviour: its red step is the Docs run |
| **L5** | FC9 — the status reference names the failure class | FC6. F13: `plugin/skills/working/reference/status.md:31` names fourteen of `runview.Row`'s forty-four keys in prose, and the design's §6.3 says nothing in `plugin/` names any of them. **D1457** |

**Edges** (from each "Depends on"): FC1 → FC2; FC1 → FC5; FC2 → FC3; FC2 → FC4; FC3 → FC5;
FC5 → FC6; FC5 → FC7; FC6 → FC8; FC6 → FC9; FC7 → FC8.

**Critical path: FC1 → FC2 → FC3 → FC5 → FC6 → FC8.** FC4, FC7 and FC9 hang off it.

### 1.1 Which tasks can run at the same time

Two tasks in one level share no file (§2), so a level's tasks are parallel by construction: **FC4 and
FC5 are one level**, **FC6 and FC7 are one level**, and **FC8 and FC9 are one level**. That is what
the six levels buy: a build with three runs available takes **six rounds** — L0, L1 and L2 forced one
after the other (FC1, FC2, FC3), then L3's two, L4's two and L5's two — rather than the nine a serial
order of the same tasks takes.

**Three tasks write `internal/runner/runner.go`** (FC2, FC3, FC4), and §§2's rule is the reason they
are three levels apart rather than three runs at once; a build that lands two of them together
rebases once, in the order of their levels. Two tasks write `internal/cli/runs_test.go` (FC5, FC6) and
**three** write `console/docs/json-contract.md` (FC5 in L3, FC6 in L4, and FC8 in L5 if **D1460**'s
answer is yes — §2's row), and the same rebase applies.

### 1.2 The long pole

**FC2**, and the reason is its surface, not its difficulty: 36 statements in eight files become one
call, the package's own suite costs ~25 minutes, and its test is a guard over the tree rather than a
fixture per site. Everything except FC4 waits on it. **If one task in this plan is expected to slip,
it is FC2** — and nothing else in the plan moves if it does: FC1 ships alone (a field nothing writes,
which is harmless and true), and FC3–FC9 simply start later.

---

## 2. Shared files

**No level holds two writers of one file.** The writers, with their level:

| File | Writers (level) | Note |
| --- | --- | --- |
| `internal/failure/failure.go`, `internal/failure/failure_test.go` | FC1 (L0) | new: the class type, `Classes()`, the one table and its properties |
| `internal/runstore/runstore.go` | FC1 (L0) | `Record.FailureClass` beside `Reason` (`:684`), and nothing else |
| `schemas/result.schema.json` | FC1 (L0) | the property and its enum, in the commit that adds the Go field (the schema is `additionalProperties: false`, F8) |
| `internal/cli/docs_pr_state_test.go` | FC1 (L0), FC2 (L1) | FC1 adds the vocabulary's sentence to the design's pin; FC2 **rewrites the census pin**, which reaches its `statements == 0` fatal the moment `recordEnd` takes the status (F25) |
| `internal/runner/recordend.go` | FC2 (L1) | new, if **D1452** answers the recommendation: the one writer |
| `internal/runner/runner.go` | FC2 (L1), FC3 (L2), FC4 (L3) | three tasks, three levels, three sets of functions: FC2's `finalize` switch and deferred/bootstrap terminals, FC3's `verify`/`runCommits`/config refusals, FC4's `stageFailureClass` and phrase tables beside `oauthLimitPhrases` (`:3802`) |
| `internal/runner/refused.go` | FC2 (L1), FC3 (L2) | FC2's `endRefused` (`:320`), FC3's `branch_moved` |
| `internal/runner/gate.go`, `internal/runner/followup.go`, `internal/runner/reviewmode.go`, `internal/runner/artifact.go`, `internal/runner/reviewartifact.go`, `internal/runner/shadow.go`, `internal/runner/failure_class_internal_test.go`, `docs/design/failure-classes.md` | FC2 (L1) | the other 25 statements, the guard's own file, and the design's §1 census, which FC2 falsifies and rewrites |
| `internal/runner/outcome.go`, `internal/runner/halt.go`, `internal/runner/prflow.go` | FC3 (L2) | the four `Decide` refusals, `haltClass` beside `haltReasonText` (`:159`), `git_provider_error` |
| `internal/runner/stageerror_internal_test.go`, `internal/runner/redaction_test.go` | FC4 (L3) | the classifier's tests and the no-text property |
| `internal/runview/runview.go`, `internal/runview/failureclass_test.go` | FC5 (L3) | `Row.FailureClass` and `ClassOf`; `internal/runview/launched_by_test.go`'s neighbour is the guard's new home |
| `internal/cli/runs_test.go` | FC5 (L3), FC6 (L4) | the row's class assertions, then the grouping's |
| `console/docs/json-contract.md` | FC5 (L3), FC6 (L4), FC8 (L5) | **one row each, in three levels** — the row's key (FC5), the `groups` block (FC6), and diagnose's field if **D1460** says yes (FC8, which may not add a Go field: if the answer is yes the field is FC5's and this row rides that commit, F32) |
| `internal/cli/runs.go`, `internal/cli/runs_group.go`, `internal/cli/runs_group_test.go`, `internal/cli/testdata/runs_group.golden`, `website/content/cli-reference.md` | FC6 (L4) | the flag, its closed value list, `lsDoc.Groups`, the helper, the tests and the regenerated reference (`go run ./ee/clidoc`) |
| `internal/digest/*.go`, `internal/digest/testdata/digest.golden.md` | FC7 (L4) | `Failures`, `FailuresTable`, the two rules and their constants, and the golden report |
| `docs/design/v1.md`, `docs/periodic-reviews/spec.md`, `docs/periodic-reviews/implementation-plan.md`, `plugin/skills/working/reference/diagnose.md`, `internal/cli/docs_desktop_console_test.go` | FC8 (L5) | §4.6's sentence, the rule list in the document that owns it (**D1463**), the one sentence that tells a person to use the grouping, and the contract's pins if a row moves |
| `plugin/skills/working/reference/status.md`, `plugin/routing_skill_test.go` | FC9 (L5) | the sentence and its pin (F13) |

**Not touched by this plan, and named so it is not discovered later:** `internal/userstats/**` and
`internal/cli/runs_summary.go` (the history census and the summary keep their status vocabularies,
**D1467**), `internal/watch/**` (`isFailure` switches on three statuses and the feed's shapes are
another contract, **D1461**), `mobile/**` and `shared/**` (nothing is emitted for them: `groups` is
`omitempty` and the watch path never sets it, **D1458**), `console/src/**` (the chip or column is the
console's own plan, and **D1459** — `CONTRACT_KEYS` in `console/src/source/redact.ts:1159` — is the
question that plan inherits; the design's §6.3 puts it out of scope), `internal/runner/outcome.go`'s
`Decide` (FC3 adds the class beside the reason `Decide` already returns at `:46`; the reason's own text
and meaning do not change),
`deploy/terraform/**` (no policy node, no rules change, no new IAM), `schemas/` other than
`result.schema.json` (no other format moves), `ee/**`, `website/**` other than the generated
reference FC6 regenerates, and `.github/workflows/**`.

---

## 3. Tasks

### FC1. The vocabulary, the field and its schema

**Decisions:** D1453 (the vocabulary's package), D1462 (no new pin file). **Findings:** F2, F8, F15,
F21, F27, F28. **Depends on:** nothing. **Reviewer tier:** standard. **SEC:** no. **Owner-run:** no.

- **Files (6):** `internal/failure/failure.go` and `internal/failure/failure_test.go` (new — a new
  package, because `runstore` must import the type that its `Record` field carries and therefore
  cannot host it, F21), `internal/runstore/runstore.go` (`Record.FailureClass failure.Class` with
  `json:"failure_class,omitempty"`, beside `Reason` at `:684`), `schemas/result.schema.json`,
  `schemas/schemas_test.go`, `internal/cli/docs_pr_state_test.go`.
- **Interfaces:** `type Class string`; `failure.Classes() []Class` — **the one slice**, in the
  document's own order, from which the schema's enum and the report's table are read;
  `failure.Unclassified`; and the per-class properties (`Kind`, `Retryable`, `NeedsOwner`) read from
  one unexported table with a test that every value of `Classes()` has a row and no row is orphaned.
  The record's field is **not** in the schema's `required` (a succeeded record carries none, F8), and
  `result.json`'s `"version"` stays `const 1` (`schemas/result.schema.json:10`).
- **Tests:** `TestResultSchemaFailureClassesAgreeWithGo` — **the shape `TestResultSchemaHaltReasonsAgreeWithGo`
  uses (`schemas/schemas_test.go:1978`), not the `…StatusesAgreeWithGo` the design names, which does
  not exist** (F15): the schema's enum is `failure.Classes()`, in order. `TestFailureClassesAreClosedAndPropertyComplete`
  (every class has its row, every row its class). `TestResultSchemaAcceptsRunnerRecords` (`:694`,
  re-run: the fully-populated record carries the field).
- **Red step:** write the schema's enum from the Go slice before the slice exists — the test fails
  because the two lists disagree (an empty slice against a written enum), which is the failure the
  commit's message quotes.
- **Mutation to hold it:** drop one value from `Classes()` → the schema test fails; add a value to the
  schema's enum alone → the same test fails, in the other direction.
- **Command:** `GOFLAGS=-p=2 go test ./internal/failure ./internal/runstore ./schemas -run 'FailureClass|Classes|Accepts' -count=1`
- **Commit:** `feat(failure): the failure_class vocabulary and the record's field`.

### FC2. One writer for the terminal status (SEC)

**Decisions:** D1452, D1456, D1464. **Findings:** F2, F3, F4, F18, F19, F25, F28. **Depends on:**
FC1. **Reviewer tier: independent (SEC)** — the reviewer's one question is *can a class or a status be
written anywhere else, and is the last-writer rule the only rule?* **Owner-run:** no.

- **Files (12 — the design's cap, and the design's own list is 11: `recordend.go` is the twelfth, which
  is D1452's cost):** `internal/runner/recordend.go` (new), `internal/runner/runner.go`,
  `internal/runner/gate.go`, `internal/runner/refused.go`, `internal/runner/followup.go`,
  `internal/runner/reviewmode.go`, `internal/runner/artifact.go`, `internal/runner/reviewartifact.go`,
  `internal/runner/shadow.go`, `internal/runner/failure_class_internal_test.go` (new — an internal test
  package, because the writer is unexported and *not* one of the `docs_*_test.go` files the census
  counts, F28), and the two files the change itself falsifies: `docs/design/failure-classes.md` (§1's
  census sentence) and `internal/cli/docs_pr_state_test.go` (its census pin).
- **Interfaces:** `func (r *run) recordEnd(status runstore.Status, outcome runstore.Outcome, class failure.Class, reason string)`.
  The **36 statements** of F2/§3.1 of the reconciliation become one call each; the sites that write a
  halt keep writing it and pass the halt's class (`endHalted` `:419`, `endArtifactHalted` `:1488`,
  `endReviewHalted` `:987`); `endUnchanged` (`:557`) passes `unclassified` where the run's terminal
  state is not a failure of its own; the deferred fallback in `Run` (`:586`) and the two bootstrap
  terminals (`:650`, `:656`) keep their meanings exactly.
- **Tests:** `TestRecordEndIsTheOnlyWriterOfATerminalStatus` — **the guard parses the Go code**
  (`go/parser` over `internal/runner`, the shape the rewritten census pin uses) and asserts that no
  statement outside `recordend.go` assigns a `runstore.Status` to a record's `Status` field. It has
  to see *any* assignment, not only `r.rec.Status = …`: `storeUnposted`
  (`internal/runner/followup.go:520`, the statement at `:522`) writes a **copy** of the record through
  a local `rec`, and a guard written for `r.rec.Status` alone would leave exactly that statement free
  to carry a status past `recordEnd`. The task accounts for that copy in the guard's own comment
  rather than excepting it silently. Also `TestUnclassifiedIsWrittenWhereNoClassIsChosen` (a run that
  ends with no failure carries no class, D1464) and `TestHaltedRunCarriesItsHaltsClass` (a failure
  then a halt ends `halted` with the halt's class). And the **rewritten**
  `TestDocsFailureClassCensusMatchesTheRunner` (`internal/cli/docs_pr_state_test.go:250`): its
  `statements == 0` fatal is the state this task produces, so the pin turns from *a census of 36
  statements* into *no statement outside `recordend.go` writes a status*, and the design's §1 sentence
  it holds is rewritten in the same commit (F25).
- **Red step:** `TestRecordEndIsTheOnlyWriterOfATerminalStatus` fails against today's tree with **the
  36 statements listed by file and line** — the count, not a sample — and both that output and the
  census pin's own failure are quoted in the pull request.
- **Mutation to hold it:** let one site keep writing `r.rec.Status` itself → the guard fails; make the
  guard match the text instead of the AST → `storeUnposted`'s copy passes unnoticed, and the mutation
  that proves it is writing a status through a local record in a fresh helper.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'RecordEnd|Unclassified|HaltedRun' -count=1 -timeout 40m`
  then `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsFailureClass' -count=1 -timeout 40m`.
- **Commit:** `runner: one writer for a run's terminal status and class`.

### FC3. The classes the runner knows structurally

**Decisions:** D1455. **Findings:** F7, F17. **Depends on:** FC2. **Reviewer tier:** standard.
**SEC:** no (the classes come from the runner's own state, not from model output). **Owner-run:** no.

- **Files (5, plus the test files that carry the three assertions):** `internal/runner/outcome.go` (the four `Decide` refusals),
  `internal/runner/halt.go` (`haltClass` beside `haltReasonText`, `:159`), `internal/runner/runner.go`
  (`verify`, `runCommits`, the `config_invalid` refusals), `internal/runner/refused.go`
  (`branch_moved`), `internal/runner/prflow.go` (`git_provider_error`), and the test files that carry
  the three assertions.
- **Interfaces:** `failure.ClassForVerify(rec verify.Record, sha string) failure.Class` (the reason
  `outcome.Decide` already returns at `internal/runner/outcome.go:46`, beside it) and
  `haltClass(h runstore.HaltReason) failure.Class`. **The halt table is the load-bearing one**
  (D1455): the writer's `haltClass` and the reader's legacy mapping (FC5) must agree halt reason by
  halt reason, including `HaltBudgetUnavailable` (`internal/runstore/runstore.go:53`), which the
  design's `halted_credential` range `:54–70` misses (F17) and which is a **budget** halt. The one
  slice is `runstore.GovernanceHaltReasons()` (`:103`) plus the budget and credential families, and
  the agreement test walks it.
- **Tests:** `TestDecideRefusalsCarryTheirClass`, `TestHaltReasonsMapToTheirFamily` (every value of
  the closed `HaltReason` set, including `:53`), `TestConfigRefusalIsConfigInvalid`, and
  `TestHaltClassesAgreeWithTheReadersTable` — the test D1455 exists for: the same walk over the same
  slice, asserted against the reader's mapping, so a rename in one place and not the other is red.
- **Red step:** every class assertion fails against a tree where the class is `unclassified` — the
  tree after FC2 and before this task.
- **Mutation to hold it:** move `HaltBudgetUnavailable` to `halted_credential` → the family test and
  the agreement test both fail.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'DecideRefusals|HaltReasons|ConfigRefusal|HaltClasses' -count=1 -timeout 40m`
- **Commit:** `runner: the classes the runner knows from its own state`.

### FC4. The classifier over the agent's own message (SEC)

**Decisions:** none of its own. **Findings:** F5, F19, F33. **Depends on:** FC2. **Reviewer tier:
independent (SEC)** — the reviewer's question: *is every byte of the message outside the record, and
does anything act on the class?* **Owner-run:** no.

- **Files (3 — the design says four and names three, F33):** `internal/runner/runner.go`
  (`stageFailureClass`, its phrase tables beside `oauthLimitPhrases` at `:3802`, and the `res.IsError`
  case at `:3733`), `internal/runner/stageerror_internal_test.go`, and — for the security property —
  `internal/runner/redaction_test.go`.
- **Interfaces:** `func (r *run) stageFailureClass(auth string, res agent.Result) failure.Class`,
  called from the `res.IsError` case that calls `stageErrorReason` (`:3759`) today. It reads the
  **already-folded** text `stageErrorText` (`:3784`) produced, and it reads it **only** to choose a
  value: the class is a value of a closed set, no byte of the message reaches the record, and nothing
  in the runner branches on the class (it is written and reported, never acted on). `subscriptionUsageLimit`
  (`:3810`) stays exactly as it is — the OAuth limit falls into `stage_error_unexplained` until **O6**
  is answered.
- **Tests:** `TestStageFailureClassNamesTheAuthRefusal`, `TestStageFailureClassNamesTheStreamFailure`,
  `TestStageFailureClassFallsBackWithoutText` (no text, or text the table does not know, is
  `stage_error_unexplained`, never a guess), and `TestStageFailureClassCopiesNoText` (the class
  printed into a record-shaped value contains no byte of the message — the redaction suite's own
  shape).
- **Red step:** the three naming tests fail against a classifier that reads no text (one class for
  all), which is FC2's tree.
- **Mutation to hold it:** return the message's first line as the class → `TestStageFailureClassCopiesNoText`
  fails; match on the subtype alone → the naming tests fail, and the reason is the one the design
  records at §4.3 (the subtype is `success` for a refused call).
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'StageFailureClass' -count=1 -timeout 40m`
- **Commit:** `runner: classify a stage's own error message, without copying it`.

### FC5. The legacy reader, and the row's key

**Decisions:** D1454. **Findings:** F20, F21, F27, F32. **Depends on:** FC1, FC3. **Reviewer tier:**
standard, with the Docs run in the same pull request. **SEC:** no. **Owner-run:** no.

- **Files (5):** `internal/runview/runview.go` (`Row.FailureClass`, beside `Reason` at `:106`, and
  `ClassOf`), `internal/runview/failureclass_test.go` (new, `launched_by_test.go`'s neighbour),
  `internal/cli/runs_test.go` (the row's class on the listing), `console/docs/json-contract.md`
  (**the row's `failure_class` row, in this commit** — the reflect pin
  `TestDocsDesktopConsoleContractNamesTheOtherShapesFields`
  (`internal/cli/docs_desktop_console_test.go:353`) walks `runview.Row{}` and asserts the document
  contains the literal key, so a commit that adds the field alone is red, F32), and — only if
  **D1460** answers yes — `internal/cli/diagnose.go`'s `Diagnosis` field with its own contract row,
  because a docs-only task may not add a Go field (§3's FC8).
- **Interfaces:** `func ClassOf(rec *runstore.Record) failure.Class` — **exported, in
  `internal/runview`, under the name the design's FC5 gives it, not §5's `legacyClass`** (F20): it is
  the one function the row and the digest both call, and a reader in another package cannot call an
  unexported one. It returns `rec.FailureClass` when the field is there and the fixed table's value
  otherwise, and it **never writes the record back** (§5 of the design): a read is a read, the bytes
  the store handed over are the bytes the next reader gets, and the retention window the design cites
  is therefore irrelevant to correctness. It cannot live in `internal/failure`: the type it reads is
  `*runstore.Record`, and F21's import direction forbids `failure` importing `runstore`.
- **Tests:** `TestLegacyClassFromReasonTable` (an old record's `reason` maps as the design's §5 table
  says, cause by cause), `TestLegacyUnmatchedReasonIsUnclassified`, `TestLegacyClassIsNeverWrittenBack`
  (serialise the record before and after a read through `ClassOf` and `runs ls`: the bytes are equal
  — this is the test that holds the read-only property), `TestRowCarriesTheClassTheRecordHolds`, and
  `TestRunsLsCarriesTheLegacyClass` (a window holding one old and one new record: both rows carry a
  class, and the old one's comes from its reason).
- **Red step:** `TestLegacyClassFromReasonTable` fails against a function that returns the class only
  when the field is present (every legacy row is `unclassified`), and the Docs run fails on the
  contract before the row is added.
- **Mutation to hold it:** write the derived class back onto the record → the bytes test fails;
  unexport `ClassOf` → `internal/digest` does not compile, which is the proof D1454's recommendation
  is the only placement that works.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runview -run 'Legacy|RowCarries|RunsLs' -count=1`
  then `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|RunsLs' -count=1 -timeout 40m`.
- **Commit:** `feat(runview): read a legacy record's class from its reason`.

### FC6. `runs ls --group-by failure_class`

**Decisions:** D1458, D1465. **Findings:** F6, F14, F32. **Depends on:** FC5. **Reviewer tier:**
standard, with the Docs and console-contract pins in the same pull request. **SEC:** no.
**Owner-run:** no.

- **Files (7):** `internal/cli/runs.go` (the flag, `lsOptions`, `lsDoc.Groups`, the refusals — the
  closed value list and the refusal by `slices.Contains` are the shape `--state` and `lsStates`
  (`:65`) already have), `internal/cli/runs_group.go` (new: the grouping over the rows the listing
  already read, `(*cli.Cloud).LoadRows`, `internal/cli/api.go:301`), `internal/cli/runs_group_test.go`
  (new), `internal/cli/runs_test.go`, `internal/cli/testdata/runs_group.golden` (new),
  `console/docs/json-contract.md` (the `groups` row — the pin sees this key through
  `watchDegradedDoc`'s embedded `lsDoc`, `internal/cli/docs_desktop_console_test.go:611` and `:284`,
  F32), and `website/content/cli-reference.md` (**regenerated, not edited**: `go run ./ee/clidoc`,
  held by `TestCliReferenceIsCurrent`, `ee/clidoc/cliref_test.go:26`).
- **Interfaces:** `--group-by`'s one value (`failure_class`), refused like `--state` for anything
  else; `lsDoc.Groups []lsGroup` with `json:"groups,omitempty"` — **`omitempty` is a requirement, not
  a style** (D1458): `lsDoc` is **embedded** by the watch path's degraded document
  (`watchDegradedDoc`, `internal/cli/watch.go:518`, which the phone reads and refuses an unknown key
  in, `mobile/src/feed/document.js:122`, `:358`), and `--group-by` is never a watch flag, so an
  always-emitted `groups` would break the app's degraded screen for a block it does not read. Each
  group carries the class, the runs, the share **of the window's runs** (D1465), and the spend beside
  it; the totals line's two figures do not move. The block is present **only** under `--group-by`: the
  plain listing's document is byte for byte what it was.
- **Tests:** `TestRunsGroupByFailureClassNamesEveryClass` (a fixture window with one run of each
  class: every group named, in `failure.Classes()`'s order, and the groups' runs adding up to the
  window's), `TestRunsGroupByRefusesAnotherValue` (the refusal names the one value, the way `--state`
  names its list), `TestRunsGroupByShareIsRunsNotSpend` (a window whose biggest class by spend is not
  its biggest by runs: the `share` column follows the runs — D1465's whole point),
  `TestRunsWithoutGroupByIsUnchanged` (the plain document against its existing goldens),
  `TestRunsGroupByGolden` (the new golden), `TestLegacyRowsAreGroupedByTheirReason` (an old record
  lands in the group its reason maps to), and — for the phone, F14 — `TestWatchDocumentCarriesNoGroups`
  (the `watch --json` document's shape, read through the same comparison
  `internal/cli/watch_prstate_test.go:415` makes against `internal/cli/testdata/watch_json_keys.golden`,
  carries no `groups` key).
- **Red step:** the grouping test fails against a listing with no block, and `TestWatchDocumentCarriesNoGroups`
  is written against the emitting version first and quoted failing, which is the half of this task a
  reviewer cannot see by reading the diff.
- **Mutation to hold it:** drop `omitempty` → the watch test fails; make `share` a share of spend →
  `TestRunsGroupByShareIsRunsNotSpend` fails.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'RunsGroupBy|RunsWithoutGroupBy|WatchDocumentCarriesNoGroups' -count=1 -timeout 40m`
  then `go run ./ee/clidoc && go test ./ee/clidoc/ -run CliReference -count=1`.
- **Commit:** `feat(cli): group runs ls by failure class`.

### FC7. The Failures table and the two rules

**Decisions:** D1463, D1466. **Findings:** F10, F24, F26. **Depends on:** FC5. **Reviewer tier:**
standard, with the digest's own pins and the weekly-report golden in the same pull request.
**SEC:** no. **Owner-run:** no.

- **Files (6):** `internal/digest/digest.go` (`Failures` beside the six flags and `FlagNames`), `internal/digest/render.go` (`FailuresTable` — every cell a figure or a closed vocabulary, the
  discipline `RunsTable` (`:73`) keeps — and `failures` in the `summary` block, `:91`),
  `internal/digest/patterns.go` (the two rules, their constants beside the existing thresholds at
  `:40`–`:42`, and `RuleNames` at `:37`), `internal/digest/digest_test.go`,
  `internal/digest/patterns_test.go`, `internal/digest/testdata/digest.golden.md`.
- **Interfaces:** `Failures` in the report's document; `FailuresTable` in the rendered report;
  `failure_class_high` and `failure_class_new` in `RuleNames` and in `Patterns` (one row each, the
  shape the four existing rules use, `RuleHaltsHigh` at `:31`). **The two rules read only the
  summaries that carry `failures`** — a block written before this release has no key, the rule skips
  it, and neither rule can fire in the first week after the release. That is D7's own statement and
  D1466's reason for making the rules the droppable half of this task: the **table ships whatever
  happens**, and if **D1466** answers "the table alone", `FailuresTable`, the summary key and their
  tests stay and the two rules are deferred to a later release with no other change.
- **Tests:** `TestDigestFailuresTableIsClosed` (every class of `failure.Classes()` appears, every
  legacy record counted through its reason, no row invented), `TestFailureClassHighNeedsItsWeeks`
  (the rule fires only with its weeks, and not with one), `TestFailureClassNewIgnoresBlocksWithoutTheField`
  (two old summaries and one new: nothing fires, and the rule reports the periods it read),
  `TestDigestSummaryCarriesFailures` (the block's key), and the golden report.
- **Red step:** the two rule tests fail with the four rules alone; the table test fails with no table.
- **Mutation to hold it:** count a legacy record as `unclassified` instead of reading its reason →
  the table test fails; let `failure_class_high` fire on one week → its own test fails.
- **Command:** `GOFLAGS=-p=2 go test ./internal/digest -count=1`
- **Commit:** `feat(digest): a period's failures by class, and two rules`.

### FC8. The documents, the contract and the skills

**Decisions:** D1460, D1468, D1469. **Findings:** F10, F23, F24, F27. **Depends on:** FC6, FC7.
**Reviewer tier:** standard, with `go test ./internal/cli -run 'Docs'`, `go test ./plugin/` and
`go test -tags terraform ./internal/infra/...`'s absence of a change noted in the pull request.
**SEC:** no. **Owner-run:** no.

- **Files (5):** `docs/design/v1.md` (§4.6, the record's field list at `:406`, one sentence —
  D1468), `docs/periodic-reviews/spec.md` and `docs/periodic-reviews/implementation-plan.md` (the
  rule list, which is where the two rules' prose belongs — **D1463**; D326
  (`docs/task-governance/reconciliation.md:302`) is the rule's *idea* and is named as its source),
  `plugin/skills/working/reference/diagnose.md` (one sentence, telling a person to use the grouping
  when a run failed — the file that already tells a reader what to do with a failed run), and
  `internal/cli/docs_desktop_console_test.go` **only if a pin's wording moves**. If **D1460** answers
  yes, diagnose's field is FC5's Go change and this task carries its contract §6.1 row and the
  printed line's sentence (F23) — a docs task adds no Go field.
- **Interfaces:** none of its own: this task writes prose and, where a pin counts something, it moves
  the pin in the same commit. Its prose obeys F27 — `reason` keeps its meaning, the field is new, and
  no sentence promises a version move.
- **Tests:** the pins that already read those documents — `go test ./internal/cli -run 'Docs'`
  (which carries `TestDocsDesktopConsoleContractNamesTheOtherShapesFields`,
  `TestDocsDesktopConsoleContractCitesBySymbolNotLine`, the design's own three pins and the
  `docs/backlog/README.md` census the design promises not to move) and `go test ./plugin/`
  (`skills_lint_test.go` resolves every command and flag a skill names against the tree, so the
  sentence that names `--group-by` is checked against the flag).
- **Red step:** none of its own — the task writes no behaviour, and its gate is the Docs run plus the
  plugin lint. The first honest failure it can produce is a **wrong** sentence: a sentence saying
  diagnose prints the class in a release that does not is red at the next release's audit, which is
  why the sentence is only written for what has landed.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1 -timeout 40m` then
  `go test ./plugin/ -count=1`.
- **Commit:** `docs: the failure class on the record, the contract and the report`.

### FC9. The plugin's own enumeration of the row's keys

**Decisions:** D1457. **Findings:** F13, F12. **Depends on:** FC6 (the skill names `--group-by`, and
`plugin/skills_lint_test.go` refuses a skill naming a flag that does not exist). **Reviewer tier:**
standard, with `go test ./plugin/` in the same pull request. **SEC:** no. **Owner-run:** no.

- **Files (2):** `plugin/skills/working/reference/status.md` (`:31` — the sentence that names the
  run keys a reader of the status screen is given, which is where `failure_class` belongs, in the same
  PR as the row that adds it: CONTRIBUTING's rule is that a CLI change touching a documented command
  changes the skill in the same PR), and `plugin/routing_skill_test.go`, the pin.
  **The sentence names fourteen of `runview.Row`'s forty-four json keys, in an order of its own; it
  is a selection and claims nothing more** (§2.7 of the reconciliation, and F13): the row is
  `internal/runview/runview.go:74` and `lsDoc.Runs` is `[]runview.Row` (`internal/cli/runs.go:216`).
  So this task adds **one key and one clause**, and the pin it adds holds the two things the sentence
  really does claim: that it names `failure_class`, and that **every key it names is a key of
  `runview.Row{}`**. **It asks for neither completeness nor the row's order** — a pin written for the
  whole row would fail on the thirty keys the skill deliberately leaves out, and could not be greened
  by this task's edit. The file's own `TestWorkingSkillStatusNamesWhoStartedEachRun` (`:86`) holds
  **one** key (`launched_by`) to the code; this is the same discipline over the rest of the sentence,
  and it is what the design's §6.3 sentence ("nothing in `plugin/` enumerates `runview.Row`'s keys
  except `plugin/routing_skill_test.go:86`, which pins `launched_by` alone") misses: the names are
  there, and nothing holds them.
- **Interfaces:** none — prose and a pin. The skill's sentence says `failure_class` is the closed
  cause `runs ls --json` carries for a run that did not succeed, and points at
  `--group-by failure_class` for the window's grouping; it names no value of the vocabulary, so a
  value added later does not stale it (F27's property, applied to prose).
- **Tests:** `TestWorkingSkillStatusNamesTheFailureClass` (new). It has two halves: it **reads the
  keys out of the sentence itself** and asserts each one is a json key of `runview.Row{}` (so the pin
  follows the prose instead of keeping a second copy of it — a key the skill invents or a name the
  CLI no longer emits is red), and it asserts that the sentence names `failure_class`, **that one
  name a literal**, because it is D1457's claim and the reason this task exists. Plus the existing
  `TestWorkingSkillStatusNamesWhoStartedEachRun` and `skills_lint_test.go`'s resolution of
  `--group-by`.
- **Red step:** `TestWorkingSkillStatusNamesTheFailureClass` fails against today's sentence because
  `failure_class` is none of the keys it names — after FC6 the row carries the field and the skill's
  own sentence does not name it.
- **Mutation to hold it:** misspell a key in the sentence (`failure_classs`) → the first half fails;
  drop the clause that names `failure_class` → the second half fails, which is the mutation that
  proves the sentence is now held rather than trusted.
- **Command:** `go test ./plugin/ -run 'WorkingSkillStatus' -count=1`
- **Commit:** `plugin: the working skill's status reference names the failure class`.

---

## 4. What is deliberately not in this plan

- **The console's own reading of the class** — a chip or a column in
  `console/src/renderer/screens/run.ts` (`RUN_FAILURE_OUTCOMES`, `:1324`) and the key's entry in
  `console/src/source/redact.ts`'s `CONTRACT_KEYS` (`:1159`). The design's §6.3 puts it out of scope
  and names it so it is not forgotten; **D1459** is the question that plan inherits. Nothing here is
  gated on it, and the contract's rows are documented before the console reads them, which is the
  order that lets the console's work start whenever the owner wants it.
- **A status or outcome vocabulary change** (the design's D5 — whether a draft delivered by a
  non-converging review should be `failed` at all). Five readers move with it and the plugin's status
  table explains them (F9); it is **O1**, and this plan does not touch `status`, `outcome`,
  `internal/userstats`, `runs summary` or `watch`'s `isFailure`.
- **`internal/watch`'s feed** (**D1461**): `JSONRun`, `JSONFinished` and the live document's key set
  (`internal/cli/testdata/watch_json_keys.golden`) are another contract with another reader, and a
  key added there is a screen that has not been designed. The **only** watch-side change in this plan
  is the negative one FC6 holds: `groups` never reaches that document.
- **The history census** (**D1467**): `internal/userstats.Counts` (`:124`, filled at `:196`) counts
  statuses by person and reads no cause; a class census is a different question.
- **A backfill, and a second command.** There is nothing to backfill: old records are read through
  `reason` (FC5) and never written back, so **the whole upgrade is `fugaro update`** and no
  installation converges differently because of this work (F29, §5).
- **A schema or format version move.** `result.json`'s `version` stays `const 1` and no other
  format's version moves; the enum grows inside the field (F27).
- **A policy node, a Firebase rule or a Terraform change.** The vocabulary is not policy, is not
  operator-settable and is not per-repository: it is a fact the runner records. `deploy/terraform/**`
  and the published policy are untouched.
- **The design's O1–O6** (the label, the vocabulary's size, whether anything beyond the enum may be
  stored from model output, whether a committed report may name run ids, the thresholds, and the
  spent subscription). **No task here is gated on one of them and none is answered here**: O6 is the
  one that sits closest to FC4's phrase table, and its answer — a value of its own for the spent
  subscription — would add a class and one row, not change a task.
- **`docs/releases/vX.Y.Z.md`.** The release note is written by `scripts/release.sh` at release time
  ([docs/release.md](../release.md)), so no task writes it; §5 fixes the sentence its `### For
  operators` section carries.
- **`.github/workflows/**`**, **`ee/**`**, **`mobile/**`**, **`shared/**`** and **`website/**`** other
  than the generated reference FC6 regenerates (F30).

---

## 5. Release slot, and what the release owes

**The owner's slot is 0.22.0**, which `docs/release-streams.yaml` names **Sloth** (the CLI's stream
reads `current: "0.20.3"`, `next: Rhino`, and the names run in alphabetical order, one per minor:
Rhino is 0.21 and Sloth 0.22). It is a **shared** slot — `docs/catalog-build-context`'s D1409 already
puts its own work there — and not a second claim of the number (**D1469**).

**The HARD RULE, and it is satisfied by construction:** `fugaro update` is the whole upgrade. This
work publishes no policy node, moves no schema version, changes no image contract, changes no plugin
pin and adds no command, so an installation at the previous version converges with **nothing new to
do** — no migration runs because none is owed, and no run between the two releases needs a backfill
because old records are read from `reason` (FC5). **The release's `### For operators` section is
"run `fugaro update`" and nothing else.** Its Highlights line says the run record now says why a run
failed, in a closed vocabulary, and that the digest commits a Failures table.

**What the release notes must not claim:** that a class is retroactive (it is derived at read time
for old records — the record is never rewritten), that the two digest rules fire immediately (they
read only the blocks that carry the key, so the first week raises neither — the design's own
statement), or that the console draws the class (it does not; §4).

---

## 6. Check 350 — the live check (owner-run, NOT RUN)

**Six steps on the sandbox installation and the sandbox repository only, never the repository the
owner's own work runs in.** No agent runs any step of it: every live check in this repository is
owner-run, and this one is read by a person because it is a claim about **what the owner can see**,
which is the claim the design exists to make true.

1. On the sandbox repository, take one run that fails for a cause the classifier can name on purpose
   (a `fugaro.yaml` with an invalid key is `config_invalid`, and it is repeatable without touching
   anything else). Read `fugaro runs ls --json` and `fugaro diagnose <run>`: **the row and the
   diagnosis carry the same class**, and the class is the one the cause deserves — not
   `unclassified`.
2. Read `fugaro runs ls --group-by failure_class` over a window the owner recognises: the groups'
   runs add up to the window's runs, `share` follows the runs and not the spend, and the totals
   line's two figures are the ones the plain listing prints.
3. Read `fugaro runs digest` for the week: the Failures table is there, the summary block carries
   `failures`, and — the half that matters — **an old record in the same window lands in the group
   its `reason` maps to**, so the week is not split into before and after.
4. **The read-only step, and it is the one a code review cannot settle:** read the same old record
   twice through `runs ls --json` and once through `diagnose`, then compare the record's object in
   the bucket with what it was (a hash taken before the first read): **the bytes are unchanged**.
   §5's claim is that nothing is written back, and this is what falsifies it if a later change does.
5. **Refutation step:** a job image whose agent call is refused by the account's own limit produces
   `stage_error_unexplained` and **not** a class of its own — which is **O6** unanswered, and the
   check is what tells the owner what the report looks like meanwhile.
6. **The HARD RULE, on a real installation:** `brew upgrade` and then **`fugaro update` alone**, on
   an installation at the previous version. No second command, no prompt beyond the ones `update`
   already asks, and `fugaro doctor` clean afterwards. A step needed here that this plan did not name
   is a defect in the release, not in the check (F29).

**Number: Check 350** — a fresh block. The register's highest **written** `## Check` section is
**290** (`docs/gcp-live-checklist.md`); `main` names 300–305, 309 and 311–314 as blocks other designs
claim, and open pull request #1819 claims **330**. **Nothing in `main` or in any open pull request's
head names a number in 350–359**, so this work takes **350** and reserves 351–359 (the reconciliation
§7 carries the commands). **A claim, not a reservation:** the register's own rule is that whichever of
two claims merges second renumbers itself, so **the task that implements the check writes
`## Check 350` and its summary-table row under the number free at that merge**, the shape Check 290's
row has (`docs/gcp-live-checklist.md:791`). **The section is written by that task, not by this plan**,
and it is not one of FC1–FC9.

---

## 7. Gates, and what a task may not start without

**No task is approved.** Every row below needs the owner's explicit go, and the plan as a whole needs
**D1450** answered (keep FC1–FC8 and add FC9) before any of it starts.

| Task | Gate |
| --- | --- |
| **FC1** | the owner's approval of this plan, and **D1453** (the vocabulary's package — the runner, the reader and the digest all need the type, and `runstore` must import it, F21) |
| **FC2** | FC1 merged, and **D1452** (`recordEnd`'s home: a file of its own, or `runner.go` beside `finalize`) and **D1456** (that FC2 stays one task) answered |
| **FC3** | FC2 merged, and **D1455** (where the halt table lives, so the writer and the reader cannot disagree — F21's import direction is what makes this a question) |
| **FC4** | FC2 merged. No decision gates it |
| **FC5** | FC1 and FC3 merged, and **D1454** (`ClassOf`'s home and its name — F20's two names). The contract's row rides this commit (F32) |
| **FC6** | FC5 merged, and **D1458** (`groups` is `omitempty`, and the phone's document never carries it) and **D1465** (`share` follows the runs) answered |
| **FC7** | FC5 merged, and **D1463** (which document owns the two rules' prose) and **D1466** (the table ships, the rules are the droppable half) answered |
| **FC8** | FC6 and FC7 merged, and **D1460** (diagnose prints *and* carries the class), **D1468** (`docs/design/v1.md` §4.6) and **D1469** (the slot) answered |
| **FC9** | FC6 merged, and **D1457** (the skill names the key) answered |
| **FC2 and FC4** (the SEC half) | their own gates, **plus an independent review that is not waived**: these are the two tasks that touch model output and the one writer, and the design's §10 flags them for exactly that |
| **Check 350** | FC6 and FC7 deployed on the sandbox; **owner-run, and it gates only the claim that the owner can see the failure classes**, never the release |

**D1451 and D1462 are not gates of any task.** D1451 is this folder's own shape and it is already
taken ([spec.md](spec.md) *is* the design's verbatim copy, and the README states which copy is
pinned); D1462 is a rule every task above obeys — the pins go into the existing
`internal/cli/docs_*_test.go` files, and no task adds one, because the docs-pin census counts them
(`ls internal/cli/docs_*_test.go | wc -l`, `internal/cli/docs_rigor_modes_test.go:528`) and four other
folders' reconciliations state the same count in a row their own pins re-run.

**If FC7's second half is dropped** (D1466 = the table alone), FC8 loses one document and one
sentence and nothing else moves. **If D1460 = no**, FC5's diagnose field and FC8's row and sentence
fall away. **If FC2 slips**, FC1 ships alone — a field nothing writes, which is harmless and true —
and FC3–FC9 start when it lands.

**And the plan's own status, unchanged:** a plan; **no task is approved**; the design's own §10 status
and every empty owner's-answer cell in [reconciliation.md](reconciliation.md) §6 stand beside every
row above.
