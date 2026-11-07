# Reconciliation: the failure-classes design against the code on `main`

**Status (2026-11-07): D1450–D1469 are answered 2026-11-07 and all tasks are approved 2026-11-07
(owner: "All tasks approved. Jobmax"); of them, FC1 is built on `main` and FC2–FC9 are not started.**
This file reads
[spec.md](spec.md) — a verbatim copy of `docs/design/failure-classes.md` — against the tree of `main`
at **`56c91f66`** (2026-11-07), path by path and symbol by symbol. Everything below was resolved
against that tree while writing it; the two commands that read the numbers this work holds are in §7,
and what could not be resolved at all is in §8. **`main` has moved since** — to **`4dc17549`**, 39
files and two new docs folders later — so the citations were re-checked against it. **Three of the 39
are files this folder cites, and all three are ones it re-read:** `docs/rigor-modes/reconciliation.md`,
whose census row this folder cites in F28, D1462 and §7 and whose count and line the move itself
changed (100 → 101, now `:752`); `docs/api-key-review/reconciliation.md`, which did not exist at
`56c91f66` and is cited at its content on `main` today; and
`docs/console-managed-screens/reconciliation.md:280`, quoted in §7 for the reason that work gives for
taking its block above D1429. **No other file this folder cites is one of the 39**, so every other line
below still resolves there; and the move's other change is the two number blocks, which §7 re-reads.

**Where this file and [spec.md](spec.md) disagree about the code, this file wins.** Where they
disagree about what the design *says*, spec.md wins. Where a task in
[implementation-plan.md](implementation-plan.md) and this file disagree, this file's findings
(**F1–F36**) and decisions (**D1450–D1469**) win, and the task is wrong.

**The design's decisions are not this file's.** §8 D1–D9 and §9 O1–O6 of the design are the owner's,
and **this file answers none of them** — not one row of §6 re-asks one, and §9 repeats them as the
owner's; the decisions §6 does carry are the plan's own, D1450–D1469, and the owner answered every one
of them on 2026-11-07 as this file recommended. The design's status line as **this folder's copy**
carries it is the dated one — *"a design; nothing is built; no decision below is answered."* — and the
design on `main` has moved past it: the copy's sentence speaks for the design's own decisions, which
the design's own status now says stay the owner's, and the line records the one thing that has shipped
— *"a design, and its FC1 has shipped; the rest of §10 is not built."*

---

## 1. The design, what it is answerable to, and the copy in this folder

**The design is merged, docs only, and already pinned.** Pull request **#1746** ("a closed failure
class on every run record, and a failures report") is **merged**; it added
`docs/design/failure-classes.md` and extended an existing docs test file,
`internal/cli/docs_pr_state_test.go`, rather than adding one, so the count of
`internal/cli/docs_*_test.go` files the docs-pin census reads did **not** move (F28). Three tests hold
the design against the tree:

| Test | Line | What it holds |
| --- | --- | --- |
| `TestDocsFailureClassDesignPinsTheVocabulary` | `internal/cli/docs_pr_state_test.go:127` | the status line, the nineteen-value vocabulary (**read from `failure.Classes()` in `internal/failure`, and held to the document in both directions** — FC1's own change, which replaced the literal list this row described before it: the table's rows are checked against the slice, the slice against the table, the two orders against each other, and the sentence's count word against `len(Classes())`), every sentence an implementer acts on — `failure_class`, `stays const 1`, the `recordEnd` signature, `--group-by failure_class`, `FailuresTable`, `failure_class_high`, `failure_class_new`, `ClassOf`, `stageFailureClass`, the HARD RULE — the backlog census it promises not to move, and the file its own pin lives in |
| `TestDocsFailureClassCensusMatchesTheRunner` | `internal/cli/docs_pr_state_test.go:303` | the 36-statements / 24-functions / 8-files census, **counted from `internal/runner`'s own files** with the regexp `\brec\.Status[^=!]*= runstore\.Status`, so a new writer reddens the design and the pin together |
| `TestDocsFailureClassDesignCitationsResolve` | `internal/cli/docs_pr_state_test.go:386` | every `path:line` the design cites names exactly one file of the tree and a line that is **inside** that file, and every relative `:NNN` resolves to its paragraph's nearest preceding citation |

**What that pin does not check, and it matters here: it does not check that the symbol is on the
cited line**, only that the line exists. Every `runner.go` citation above `:3700` has drifted by four
lines since the design was read on 2026-11-05 (F19), and the pin is green. A reader who follows the
design's own anchor lands on a comment.

**This folder's copy is dated.** `spec.md` is the document as it stands at `56c91f66`. The design's
§10 says its own **FC2** rewrites §1's census and the pin above in the same commit (F25), so the copy
is falsified by the work it plans. The copy says so in its header and this file says so here; the
design document itself is never edited by this folder, and its pin reads the design, not the copy.

---

## 2. The record and its readers, today

The design's §7 says what moves and asserts that nothing breaks. This is the census that claim needs:
every reader of `result.json`'s shape, and what a new field on it costs each one.

### 2.1 `result.json` and its schema

`runstore.Record` (`internal/runstore/runstore.go:688`) carries `Version` (`:689`), `Status` (`:718`),
`Stage` (`:719`), `Outcome` (`:720`), `Reason` (`:721`, `json:"reason,omitempty"`) and, when the run
halted, `Halt *Halt` (`:790`) whose `Reason` is the closed `HaltReason` (`:45`).
`schemas/result.schema.json` declares `"additionalProperties": false` (`:7`), `"required"` without any
of the optional blocks (`:8`) and `"version": { "const": 1 }` (`:10`), and
`TestResultSchemaAcceptsRunnerRecords` (`schemas/schemas_test.go:696`) validates a fully populated
record against it. **A property added to the schema and not to `Record`, or the reverse, is red in the
same commit** — the two are one edit, which is what the design says and what the tree shows.

The additive precedent the design leans on is real, and its test is
`TestResultSchemaAcceptsARecordWithoutArtifacts` (`schemas/schemas_test.go:1331`): a record written
before a field existed validates with the field absent, and the field is not in `required`.

### 2.2 `runs ls --json`: `runview.Row` and `lsDoc`

The row is `runview.Row` (`internal/runview/runview.go:74`); it copies the record's sentence into
`Reason` (`:106`), the read side's own derivation into `Derived` (`:38`–`:57`, `DerivedPreempted`), the
halt into `Halt`, and the run's five-key status vocabulary into `Status`. The document is `lsDoc`
(`internal/cli/runs.go:214`) — `project`, `runs`, `totals`, `warnings` — and the rows come from
`(*cli.Cloud).LoadRows` (`internal/cli/api.go:301`), which `runs ls` and every other row reader call.
`--state`'s closed list is `lsStates` (`internal/cli/runs.go:65`) and it is refused by
`slices.Contains`, which is the shape the design gives `--group-by`'s own closed list.

**Two consequences the design's §6.1 does not carry.** First, a row key is a **contract key** for the
console: §2.4. Second, `lsDoc` is **embedded** by another document the CLI emits — §2.5.

### 2.3 `fugaro diagnose`

`Diagnosis` (`internal/cli/diagnose.go:60`) is diagnose's own document: it carries the row, the halt,
the stage timings, the reviews, the tolerated findings and the checkpoint, each redacted on the way in
(`:227` for the row's reason, `:228` for the halt). It is a wire shape with its own contract section
(§2.4), so the design's D9 — "does `diagnose` print the class?" — has a text half and a **shape** half,
and only the first is in the design's words.

### 2.4 The console

- `console/docs/json-contract.md` §5 (`:567`) is the `runs ls --json` section: the `lsDoc` field table
  and the `runview.Row` field table, "the fields the console reads", one row per key. §6.1 (`:748`) is
  diagnose's.
- `TestDocsDesktopConsoleContractNamesTheOtherShapesFields`
  (`internal/cli/docs_desktop_console_test.go:353`) **reflects over `runview.Row{}`** (and
  `runview.Totals{}`, `Diagnosis`'s neighbours, the plan documents and more) and fails when a Go key is
  not named in that file. So a new key on the row is a failing test until the contract gains its row:
  this is the pin the design names, and it is real.
- `console/src/source/redact.ts`'s `CONTRACT_KEYS` (`:1159`) is a **second enumeration, and not the
  row's key set**: twelve contract field names across the documents the console reads (`run`, `run_id`,
  `id`, `stage`, `status`, `auth`, `health`, `deadline`, `verify`, `outcome`, `mode`, `state`), of which
  `id`, `auth`, `health`, `deadline`, `verify`, `mode` and `state` are not keys of `runview.Row{}` at
  all —
  its subject is the person pass, which never rewrites those fields, so a requester called `queued` or
  `api` cannot turn a run's stage or its auth into a pseudonym. A new key arrives, as the design says,
  as a new entry — but it arrives **only if the console task adds it**, and the design's §6.3 names a
  chip or a column and not the redactor (F22).
- `console/src/renderer/screens/run.ts`'s `RUN_FAILURE_OUTCOMES` (`:1324`) is the console's own
  outcome vocabulary — `['draft']` — beside `RUN_SUCCESS_OUTCOMES`, which is the reader that makes the
  design's §8 D5 a five-client question.

### 2.5 The phone (`mobile/`) and `shared/`

The design's §7 says "`mobile/` and `shared/` do not read `runs ls` at all". **The second half is
right and the first is not.** The phone reads `watch --json`'s **degraded** document, and that document
is `lsDoc` **embedded** (`watchDegradedDoc`, `internal/cli/watch.go:518` — `lsDoc` plus `degraded` and
`notice`); its own README-shaped comment says so in the mobile reader
(`mobile/src/feed/document.js`). The phone's reader judges that document **strictly**:
`DEGRADED_FIELDS = ['project', 'runs', 'totals', 'warnings', 'degraded', 'notice']`
(`mobile/src/feed/document.js:122`) and it refuses an unknown key with `UNKNOWN_FIELD`
(`:358` for a degraded document, `:393` for a live one). `mobile/src/feed/document.test.js:69` names
`runview.Row`'s shape as the row it accepts and drops. `shared/` reads neither.

**So the report's new block is a mobile question.** A key `lsDoc` gains and emits would be refused by
the phone, and the refusal is of the whole document, not the key: the app renders degraded-as-nothing.
The safe shape — and the one the plan holds — is a key that is `omitempty` and set only when
`--group-by` was asked for, which the watch path never does (F14, D1458).

### 2.6 The history census: `internal/userstats` and `runs summary`

`fugaro runs stats` reads a per-person census, not the rows: `userstats.Counts`
(`internal/userstats/userstats.go:125`) has `Succeeded`, `Failed`, `Halted`, `InfraError`, `Other` and
`NoRecord`, and the switch that fills them (`:196`) reads `runstore.Status` values only.
`fugaro runs summary` has its own state vocabulary, `summaryStates`
(`internal/cli/runs_summary.go:62`). **Neither reads a cause, and neither needs the class** — which is
what makes the design's §8 D5 ("should these runs end as `failed` at all?") a change to two
vocabularies at once, and why the design recommends leaving it alone in this work.

### 2.7 The plugin

Two files read the row's key set, and the design names only one of them:

- `plugin/skills_lint_test.go` resolves every `fugaro …` a skill names — commands *and* flags —
  against the CLI's own cobra tree, so a skill that names `--group-by` before the flag exists fails.
  The design says exactly this, and it is the reason the flag and its skill sentence land together.
- `plugin/skills/working/reference/status.md:31` **names fourteen of `runs ls --json`'s per-run keys**
  in one sentence: "Each run has `run`, `repo`, `status`, `stage`, `reason`, `halt` (when halted),
  `pr_url`, `branch`, `outcome`, `created`, `cost`, `terminal`, `settled` and `launched_by`". **It is
  a selection, not the set**: the row is `runview.Row` (`internal/runview/runview.go:74`), it carries
  **44** json keys (none `json:"-"`), and `lsDoc.Runs` is `[]runview.Row`
  (`internal/cli/runs.go:216`), so the sentence names fourteen of forty-four — in an order of its
  own, which is not the row's (the row's order puts `launched_by` before `pr_url`; the sentence puts
  it last). Nothing in the sentence claims completeness, and no test could ask for it.
  `TestWorkingSkillStatusNamesWhoStartedEachRun` (`plugin/routing_skill_test.go:86`) is the test that
  ties one of those names to the code — it reads `runview.Row`'s own json tag for `LaunchedBy`, the
  listing's header for the `BY` column, and the cobra tree for the command, and requires the skill to
  name all three. It holds one key, not the list.

**The design's §6.3 sentence — "nothing in `plugin/` enumerates `runview.Row`'s keys except
`plugin/routing_skill_test.go:86`, which pins `launched_by` alone" — is wrong** (F13): the *skill*
names fourteen of the row's keys in prose, CONTRIBUTING's rule is that a CLI change touching a
documented command changes the skill in the same PR, and the design's §10 FC8 file list does not
include `status.md`. **The correction is one key and one clause, and its pin may not ask for more**:
the sentence is a selection, so a pin requiring every key of `runview.Row{}` would fail on the thirty
the skill deliberately leaves out (F13).

### 2.8 `watch`

`internal/watch/finished.go`'s `isFailure` (`:43`) switches on three status values and is untouched by
a new record field, as the design says. The watch document (`watchDegradedDoc`, §2.5) is the one watch
surface that is not: it embeds `lsDoc`, so it rises and falls with what `lsDoc` **emits**.

---

## 3. Where a failure reason is produced today

### 3.1 `internal/runner`

**The census is exactly the design's, and it is reproducible line by line.** Every one of these is a
method on the run's own type (**`*run`**, not `*Runner`, declared in `internal/runner/runner.go`) — the
receiver a task editing them touches.

```
grep -rn 'rec\.Status[^=!]*= runstore\.Status' internal/runner --include='*.go' | grep -v _test.go
```

gives **36 statements across 8 files**, and the declaration beside each statement line:

| File | Statements (declaration in parentheses) |
| --- | --- |
| `artifact.go` (7) | `artifactMode` :195, :199, :213 (declared :134); `endArtifactCancelled` :1478 (:1477); `endArtifactHalted` :1489 (:1488); `failArtifactInfra` :1499 (:1498); `endArtifactFailed` :1514 (:1509) |
| `followup.go` (4) | `storeUnposted` :522 (:520), on a **copy** of the record; `endUnchanged` :558, :562, :567 (:557) |
| `gate.go` (4) | `endWithoutPR` :380 (:379); `failInfra` :392 (:391); `endCancelled` :407 (:406); `endHalted` :420 (:419) |
| `refused.go` (1) | `endRefused` :322 (:320) |
| `reviewartifact.go` (1) | `writeReviewArtifact` :1058 (:979) |
| `reviewmode.go` (7) | `refuseReview` :181 (:179); `reviewMode` :703 (:577); `endReviewCancelled` :979 (:978); `endReviewHalted` :988 (:987); `failReviewInfra` :999 (:998); `endReviewFailed` :1016 (:1010); `endReviewInvalidVerdict` :1029 (:1028) |
| `runner.go` (7) | the deferred fallback in `Run` :586 (`Status` read at :585); the two bootstrap terminals :650, :656; `finalize`'s four cases :4162, :4164, :4166, :4168 (declared :3899) |
| `shadow.go` (5) | `refuseArm` :164 (:162); `shadowFinalize` :379, :381, :383, :385 (:318) |

The **24 functions** and the **12 `end*` / 3 `fail*Infra` / 2 `refuse*`** split are the design's and are
exact.

The name that is not a statement is `storeUnposted`'s: it writes `rec := *r.rec` into the stored
report, so a guard written for `r.rec.Status` alone would leave it free — which is why the design's
FC2 guard must match `\.Status` anywhere in the package, and why the design's pin uses
`\brec\.Status[^=!]*= runstore\.Status` with the character class that keeps `==` and `!=` out of the
count (the pin's own comment records that the first draft of the design's list was made by eye and was
wrong).

**The first-failure writer.** `fail` (`internal/runner/runner.go:1547`) and `failLocked` (`:1554`) keep
the first reason a run cannot produce a ready PR; `failure()` (`internal/runner/halt.go:128`) reads it
back for the pull request's section and for `finalize`'s decision, and `recordHaltLocked`
(`internal/runner/halt.go:57`) calls it with the halt's own text. **Three sites overwrite the record's
reason after it**: `endHalted` (`internal/runner/gate.go:419`, statement :420), `endRefused`
(`internal/runner/refused.go:320`, statement :322) and `endUnchanged` (`internal/runner/followup.go:557`,
statement :558) — the fact
the design's §4.1 and D3 rest on, and it holds.

**What `finalize` does.** `r.rec.Reason = reason` is written at `internal/runner/runner.go:4159`
*before* the switch, and the switch's four cases are `:4162`/`:4164`/`:4166`/`:4168` (F18). The
`Decide` call whose refusal becomes the class is at `:4030`, and the `ReasonNoVerifiedTest` adjustment
at `:4032`; `the agent made no commits` is `r.fail(...)` at `:3985`.

**The classifier's machinery exists, in the file the design says.** `stageErrorReason`
(`internal/runner/runner.go:3759`) picks a named reason when the result carries `is_error`
(`case res.IsError:` at `:3733`), calls `subscriptionUsageLimit` (`:3810`) against
`oauthLimitPhrases` (`:3802`) and folds the text through `stageErrorText` (`:3784`, the two redactions
and the one-line fold). The subtype is no help — the comment at `:3729`–`:3731` says why — so the design
is right that a phrase table over the text is the only place the three observed causes can be told
apart, and right that the table must sit beside `oauthLimitPhrases`.

**The lines have drifted** (F19): `:3755`→`:3759`, `:3780`→`:3784`, `:3798`→`:3802`, `:3806`→`:3810`,
`:4026`→`:4030`, `:4028`→`:4032`, `:3981`→`:3985`, `:3729`→`:3733`. Everything at or below `:1554`
(`fail`, `failLocked`, the deferred `Run` `:585`–`:586`, the bootstrap terminals `:650`/`:656`) is
exact. The drift is uniform, which means one insertion in `runner.go` between `:1554` and `:3729` moved
the rest, and nothing **in that file** has moved to another function. **One citation in another file
has**, and F19 carries it: the design's `writeReviewArtifact` anchor
(`internal/runner/reviewartifact.go:905`) is inside `decodeReviewAnswer` (`:887`), and the statement's
own pair — `:1058`, declared `:979` — is the one this table holds.

### 3.2 `internal/runstore`

`Status` (`:27`) is `running`, `succeeded`, `failed`, `infra_error`, `cancelled`, `halted` — the closed
set `TestResultSchemaStatusesAgreeWithGo` would have held if that test existed (F15).
`Outcome` (`:117`) is `ready`, `draft`, `none`, `reviewed`. `HaltReason` (`:45`) is the closed set the
design's three halt families read: `kill_switch` (`:48`), the budget family `run_cap`, `repo_daily_cap`,
`global_daily_cap`, `no_cap`, `token_cap` (`:49`–`:53`), `budget_unavailable` (`:54`) and
`budget_token_expired` (`:55`), the credential family `no_credential` (`:62`) and `creds_rules_denied`
(`:71`), and the governance family `stage_budget`, `review_ratio`, `fanout`, `review_scope`,
`expected_exceeded` (`:82`–`:96`) with `GovernanceHaltReasons` (`:104`) the one slice the checkpoint
schema and the digest are pinned to. The design's `halted_credential` row cites `:55–71`, which misses
`HaltBudgetUnavailable` at `:54` (F17).

`runstore` imports no Fugraro package of its own for these values (`internal/rigor`, `internal/task`
and `internal/verify` are imported for other fields), which is the property the vocabulary's own
package has to respect: **a type that `Record.FailureClass` uses makes `runstore` import that package,
so that package cannot import `runstore`** (F21). This is the single most load-bearing thing the design
leaves implicit, and three of §6's decisions are about it.

### 3.3 `internal/cli`'s `diagnose`

`fugaro diagnose RUN` (`internal/cli/diagnose.go`) reads the run's rows through the same `loadRows`
(`:218`) and builds `Diagnosis` (`:60`): the row with its `Reason` redacted (`:227`), the halt (`:228`),
the stage timings (`:313`), the review lines (`:373`), the tolerated findings (`:430`), the checkpoint
(`:461`) and the route (`:496`). Nothing in it groups anything: it is the one-run reader the design's
§6.3 leaves alone, and D9's "one more line" is the only question about it.

---

## 4. Findings

Thirty-six, each labelled **Wrong** (the design says something the tree contradicts), **Exists** (a
claim the tree bears out, recorded so the plan can be held to it), **Gap** (true, and the design leaves
something out that a task needs), **Rule** (a repository rule the design obeys, restated so a task can
be held to it) or **Open** (the design leaves it to the owner).

### Wrong

- **F13 — the plugin names fourteen of the row's keys, not none and not all.** Design §6.3: "nothing
  in `plugin/` enumerates `runview.Row`'s keys except `plugin/routing_skill_test.go:86`, which pins
  `launched_by` alone." The tree: `plugin/skills/working/reference/status.md:31` **names fourteen of
  the row's forty-four keys** in one sentence (a *selection*, in an order of its own — §2.7), and
  `TestWorkingSkillStatusNamesWhoStartedEachRun` (`plugin/routing_skill_test.go:86`) holds *one* of
  them to the code. So the design's sentence is false about the skill, CONTRIBUTING's rule reaches it,
  and §10 FC8's file list does not include `status.md`. **Stated precisely, because the correction is
  small and easy to overstate**: the sentence is not an enumeration of the row, so FC9 adds one key
  (`failure_class`) and one clause, and the pin it adds holds the keys the sentence *does* name to the
  row — never completeness (the thirty unnamed keys) and never the row's order (the sentence's order
  differs). → **FC9**.
- **F14 — the phone.** Design §7: "`mobile/` and `shared/` do not read `runs ls` at all." The tree:
  the phone reads `watch --json`'s degraded document, which **embeds `lsDoc`**
  (`internal/cli/watch.go:518`), and refuses an unknown key in it
  (`mobile/src/feed/document.js:122`, `:358`, `:393`), while naming `runview.Row`'s shape as the row it
  drops (`mobile/src/feed/document.test.js:69`). `shared/` reads neither. → **D1458**.
- **F15 — a test that does not exist.** Design §7 and §3 call the shape for an enum-mirroring test
  "`TestResultSchemaStatusesAgreeWithGo`". No such test exists anywhere in the tree. The shape is
  `TestResultSchemaHaltReasonsAgreeWithGo` (`schemas/schemas_test.go:1985`), with
  `TestResultSchemaCheckpointAgreeWithGo` (`:1917`) and `TestResultSchemaReviewScopeReasonsAgreeWithGo`
  (`:2094`) as its siblings. → **FC1** names the sibling it copies.
- **F16 — `api.LoadRows`.** Design §6.1: "the listing already reads the window (`api.LoadRows`,
  `internal/cli/runs.go`)". The symbol is `(*cli.Cloud).LoadRows`, declared in
  `internal/cli/api.go:301`; `internal/cli/runs.go:109` names it in a comment only.
- **F17 — the credential family's range.** Design §3's `halted_credential` row cites
  `runstore.go:55–71`. `HaltBudgetUnavailable` is at `:54`, outside it; the four names span `:54–71`.
- **F18 — the `finalize` switch.** Design §1 and §4.2 cite `internal/runner/runner.go:4156–4165` for
  the switch "whose four cases are four of the 36", and `:4155` for `r.rec.Reason = reason`. The four
  cases are at `:4162`, `:4164`, `:4166` and `:4168`, and the reason assignment is at `:4159`.
- **F19 — the drift, in `runner.go` and in one function of `reviewartifact.go`.** Every
  `internal/runner/runner.go` citation above `:3700` is four lines short against today's tree (the map
  is in §3.1). **The one `internal/runner/reviewartifact.go` citation is worse than short**: design
  §3.1 cites `writeReviewArtifact` at `:905`, and `:905` is inside **`decodeReviewAnswer`** (declared
  `:887`), while the statement `writeReviewArtifact` makes is at **`:1058`**, in the function declared
  at `:979` — 153 lines out, and another function's name on it. §3.1's table already carries the right
  pair, which is what FC2's implementer, the task that edits the file, reads this finding for. The
  design was read on 2026-11-05 and `main` moved after it; the citation pin does not catch either by
  design (it checks a line exists, never a symbol), so a reader following the design's anchor reads a
  comment.
- **F33 — a count that does not match its own list.** Design §10's FC4 says **"Files (4)"** and then
  names **three**: `internal/runner/runner.go`, `internal/runner/stageerror_internal_test.go` (or the
  existing `stageerror_test.go`) and `internal/runner/redaction_test.go`. The tree has both stageerror
  files, so the task's diff is three files: the plan says three and names the file it writes.
- **F20 — one function, two names.** Design §5 names the legacy reader
  `legacyClass(rec *runstore.Record) failure.Class`; §10 FC5 names it
  `ClassOf(rec *runstore.Record) failure.Class`. One of the two is the function and the other is a
  different name for it, in the same document. → **D1454**.

### Exists

- **F1 — the free text is all there is.** `runstore.Record.Reason` (`:721`) is the only place a
  non-success run's *why* is kept; `runview.Row.Reason` (`:106`) copies it to the listing; nothing
  groups it. Verified by reading both, and by `fugaro diagnose`'s one-run shape (§2.3).
- **F2 — the 36/24/8 census**, reproduced from the tree (§3.1) and held by
  `TestDocsFailureClassCensusMatchesTheRunner`.
- **F3 — the helper census**: 12 `end*`, 3 `fail*Infra`, 2 `refuse*`, and the rest inline. Verified by
  name against the 36 statements.
- **F4 — `fail`/`failLocked` and the three overwriters** (§3.1). The fact D3 and FC2 rest on.
- **F5 — the classifier machinery** exists at the names the design gives, with the lines drifted
  (§3.1), including the fact that the subtype is `success` for a refused call.
- **F6 — nothing groups failures**: `runs stats`'s census (`internal/userstats/userstats.go:125`,
  `:196`), `runs summary`'s states (`internal/cli/runs_summary.go:62`), the digest's six flags
  (`internal/digest/digest.go:79`–`:93`, `FlagNames` `:97`) and `diagnose` (§3.3).
- **F7 — the halt vocabulary and `GovernanceHaltReasons`** (`internal/runstore/runstore.go:45`–`:104`),
  the closed set the design's three halt classes translate.
- **F8 — the schema's shape**: `additionalProperties: false` (`schemas/result.schema.json:7`),
  `version` `const 1` (`:10`), the additive precedent's test (`schemas/schemas_test.go:1331`) and the
  fully-populated-record test (`:696`).
- **F9 — the five readers of a status change** (design §8 D5): `RUN_FAILURE_OUTCOMES`
  (`console/src/renderer/screens/run.ts:1324`), `mobile/` (§2.5), the history census
  (`internal/userstats/userstats.go:196`), `runs summary` (`internal/cli/runs_summary.go:62`) and
  `watch`'s `isFailure` (`internal/watch/finished.go:43`). All five exist; the plugin's status table
  (`plugin/skills/working/reference/status.md:80`) is a sixth surface that *explains* the statuses and
  would move with an answer to D5.
- **F10 — the digest's discipline**: `RunsTable` (`internal/digest/render.go:74`, "every cell is a
  figure or a closed vocabulary"), the `summary` block (`render.go:91`), `Patterns`
  (`internal/digest/patterns.go:91`), the four rule constants (`:24`–`:33`), `RuleNames` (`:37`), the thresholds
  beside them (`:40`–`42`) and D326 (`docs/task-governance/reconciliation.md:302`) whose words are "a
  fixed, documented rule over consecutive periods" computed from the committed blocks.
- **F11 — the console contract's pin**: `TestDocsDesktopConsoleContractNamesTheOtherShapesFields`
  (`internal/cli/docs_desktop_console_test.go:353`) reflects over `runview.Row{}` and
  `console/docs/json-contract.md` §5 (`:567`) is where its rows live.
- **F12 — the plugin's lint**: `plugin/skills_lint_test.go` resolves every command and flag a skill
  names against the cobra tree; `resolveSkillCommand`, the helper that does it, is
  `plugin/skillcli_test.go:41`, and `plugin/routing_skill_test.go:122` and
  `plugin/setup_skill_test.go:670` are two of its callers.

### Gap

- **F21 — the import direction.** The class type is used by `runstore.Record`, so `runstore` imports
  the package that declares it; that package therefore cannot import `runstore`, which decides where
  `ClassOf` and the `HaltReason`→class translation can live (§3.2). The design leaves both placements
  open. → **D1453**, **D1454**, **D1455**.
- **F22 — the console's redactor.** `console/src/source/redact.ts`'s `CONTRACT_KEYS` (`:1159`) is a
  second enumeration, **but a hand-kept one over the contract's field names, not over the row's keys**
  (§2.4): most of its entries are not row keys, and most of the row's forty-four are absent from it. It
  still has to gain `failure_class` — a class value outside the set is a value the person pass may
  rewrite as a name — and the design's §6.3 names neither it nor the file. → **D1459**.
- **F23 — `diagnose`'s shape.** D9 asks about a printed line; `Diagnosis` (`internal/cli/diagnose.go:60`)
  is a contract shape too (`console/docs/json-contract.md:748`), so a class on it is a second
  contract row. → **D1460**.
- **F24 — which document owns the two rules.** The design says "`docs/task-governance/reconciliation.md`
  (D326's rules) or the periodic-reviews folder's own document — the one that owns the rule list,
  whichever the tree says by then". The tree has both: D326 (`:302`) is the rule's *idea*, and
  `docs/periodic-reviews/` is where the rule *list* is written down
  (`docs/periodic-reviews/spec.md:114`, `implementation-plan.md:266`, `:279`, the skill's own list in
  `plugin/skills/working/reference/periodic-reviews.md:54`, which names the four patterns and what each
  one means). → **D1463**.
- **F25 — this folder's copy is falsified by FC2.** `TestDocsFailureClassCensusMatchesTheRunner`
  (`internal/cli/docs_pr_state_test.go:303`) fatals on `statements == 0`, which is exactly the state
  `recordEnd` produces; `TestDocsFailureClassDesignPinsTheVocabulary` (`:127`) pins sentences of the
  design FC2 rewrites. So FC2 edits the design **and its pin**, and `spec.md`'s copy is a snapshot of
  the pre-FC2 document. Nothing in the tree pins this folder's copy, and nothing should.
- **F32 — the contract's rows land with the Go keys, not two tasks later.** The design's §10.2 gives
  `console/docs/json-contract.md` to **FC8 alone**. But the forward pin
  `TestDocsDesktopConsoleContractNamesTheOtherShapesFields`
  (`internal/cli/docs_desktop_console_test.go:353`) reflects over `runview.Row{}` and asserts the whole
  document contains the literal `` `failure_class` ``, and its walker promotes an embedded struct's
  keys into the shape that embeds it (`:284`), so `watchDegradedDoc{}` (`:611`) makes `lsDoc`'s keys —
  `groups` — the same requirement. **A task that adds the Go key and no contract row turns the Docs run
  red**, so the §5 row is FC5's and the `groups` row is FC6's, and FC8's remaining row is diagnose's
  (D1460). → **FC5**, **FC6**, **§2** of the plan.
- **F26 — the plan's own gate.** The design has no decision about **whether the two digest rules ship
  in the same work as the field** (§8 D7 recommends they do, with thresholds), and a rule that reads
  only blocks carrying the new key cannot fire in its first week (the design says this). Whether the
  rules are a task of this work or a follow-up is a plan-level question. → **D1466**.
- **F34 — the class `endUnchanged` passes has no row in the legacy table.** `endUnchanged`
  (`internal/runner/followup.go:557`) is not a plain failure: it sets `status: failed` with a real
  reason, flips to `cancelled` when the run was cancelled (`:559`–`:563`), and flips to `halted` with
  the halt attached when a halt stands (`:564`–`:569`), so a class written at its site has to follow
  the branch (the design's §4.2: the last writer's class wins). But the write side's class for the
  reason it actually carries — every one of its callers is the pull request or the branch changing
  under the run (`followup.go:462`, `:478`, `:487`; `runner.go:4110`, `:4114`, `:4177`), which is the
  vocabulary's own `branch_moved` row — has no row in the design's §5 legacy table: the design lists
  the reason families it means to map and says an unmatched reason reads `unclassified`. So the same
  fact would read `unclassified` on a record written before the field shipped and `branch_moved` on
  one written after — the week split into two that §5 exists to prevent, and a writer and a reader
  disagreeing about one sentence, which is what D1455 asks them not to. → **FC2** writes
  `branch_moved` at the site, and **FC5**'s table gains those prefixes so the reader classes the same
  sentence the same way (`TestLegacyClassFromReasonTable`).
- **F35 — the design's §10 test name asserts the field its own §3 leaves absent.** §10's FC2 names
  `TestUnclassifiedIsWrittenWhereNoClassIsChosen` (`docs/design/failure-classes.md`, `spec.md:653`).
  §3 of the same design says `failure_class` "is written for every run that did not end `succeeded`"
  and that a `succeeded` run, a `mode: review` run that reviewed (`outcome: reviewed`), an artifact
  run that delivered (`outcome: artifacts`) and a row that never started carry none. Written as
  named, the pin asserts a class on exactly the records the design leaves it off, and the claim it
  describes in half — an unmatched reason reading `unclassified` — is the reader's, not a run's. →
  **FC2** names the absence pin `TestNoClassIsWrittenWhereTheRunDidNotFail`, **FC5** keeps
  `TestLegacyUnmatchedReasonIsUnclassified`, and **D1464** is the decision the absence answers.
- **F36 — the file the design puts a sentence in is nine bytes from its budget.**
  `plugin/skills/working/reference/diagnose.md` is 133 lines and **16375 bytes** on 2026-11-07, and
  `plugin/skills_lint_test.go` fails any reference file over `maxReferenceLines` 300 (`:37`) or
  `maxReferenceBytes` 16384 (`:39`, compared at `:348`) — **nine bytes of room**, against a sentence
  that costs far more than nine. The design's §10 puts its one sentence there
  (`docs/design/failure-classes.md:705`, `spec.md:741`; the design marks it conditional on **O1**'s
  answer), so the sentence is written by **trimming the file as much as
  it adds**: the prose it replaces leaves, or the sentence is shorter than what it displaces, and
  `go test ./plugin/` — which FC8's own gate already runs — is what says so. The precedent is the
  repository's own: 0.9.5 → 0.10.0's bump added two bytes to each skill file and pushed
  `plugin/skills/setup/SKILL.md` over `maxSkillBytes`, which reddened `TestPluginSkillsLintClean` on
  `main` and put the tag on a follow-up (`docs/release.md:103`), and that is why the release runs the
  plugin tests on a version-bumped tree before it tags (`scripts/release.sh`'s `plugin_budget_check`,
  `docs/release.md:182`). → **FC8** states the budget in its file list, and its red step says what red
  looks like (`diagnose.md: 16390 bytes, the budget is 16384`). The other file this folder's tasks
  edit, `status.md`, is 94 lines and 8711 bytes and has room; the byte budget is not a second finding
  there.

### Rule

- **F27 — the closed-field rule.** `CLAUDE.md`: adding a field to a format read outside the same binary
  is fine; removing or retyping one bumps that format's version; **a field whose meaning must change
  gets a new name**. `failure_class` is a new name beside `reason`; `result.json`'s `version` stays
  `const 1`; `reason` keeps its text and its meaning byte for byte; the enum may grow inside the field
  (a reader that does not know a value reads `unclassified`), and the set is written **once**, in one Go
  slice the schema's enum and the report's table are read from.
- **F28 — the docs-pin census.** `internal/cli/docs_*_test.go` files are counted (**101** today — the
  row moved from 100 to 101 on 2026-11-07, when `docs/cli-output-console-graph` added its own pin file
  and amended the row's narrative in the same commit, `docs/rigor-modes/reconciliation.md:752` — and
  the design quotes the count as one it does not move), so **this folder adds no test file** and the
  plan's tasks write their pins into the existing `docs_pr_state_test.go`. The design's own promise —
  it kept `docs/backlog/README.md` at "2 items: 2 open, 0 closed, 0 dropped"
  (`docs/backlog/README.md:13`) — is quoted by the vocabulary pin, which reads `docs/backlog/README.md`
  at `internal/cli/docs_pr_state_test.go:248` and fails when the line is gone (`:251`) or the design
  stopped quoting it (`:255`).
- **F29 — the HARD RULE, and the one word in it that needs reading.** A field addition owes no operator
  step: `fugaro update` is the whole upgrade, the policy node is untouched, no schema version moves, no
  image contract changes and no plugin pin **migration** is owed. The design's §10.3 states it and the
  plan repeats it (§5). The pin is a plain move, though, and reading the design's own words strictly is
  worth one sentence here: `.claude/settings.json` pins the plugin by **tag ref** (`ref: vX.Y.Z`,
  `internal/pluginwire/wire.go:191`, `:262`, `internal/pluginwire/status.go:207`), and every release
  rewrites `plugin/.claude-plugin/plugin.json`'s version and every skill header to the new number
  (`scripts/bump-plugin-version.sh`, run by `scripts/release.sh:400`), so the pin moves on **every**
  release. This work is no exception — it edits two skill files (FC8's `diagnose.md`, FC9's
  `status.md`), the release publishes the tag that carries them, and `fugaro update` moves the pin to
  it as it does for any release, asking nothing new of the command. What the design's sentence denies
  is a migration, and that is exactly what holds (F36 is the other thing the plugin files owe).
- **F30 — `.github/workflows/` is never edited by an agent run**, and this folder is docs only.

### Open

- **F31 — the design's own decisions.** §8 D1–D9 and §9 O1–O6 are unanswered, and this file does not
  answer one of them (§9). The plan's gates (§7 of the implementation plan) name D-numbers from §6 and
  never an Open item: an Open item is the owner's own question and is not a decision row in disguise,
  and the plan states, rather than folds away, which of its tasks an Open item would hold — here,
  none of them.

---

## 5. The closed-field rule, applied format by format

| Format | What this work adds | What moves | Version |
| --- | --- | --- | --- |
| `result.json` (`schemas/result.schema.json`, `runstore.Record`) | `failure_class`, an enum of the nineteen values, **not** in `required` | the schema's enum and `Record`'s field in one commit, or `TestResultSchemaAcceptsRunnerRecords` (`schemas/schemas_test.go:696`) is red | **stays `const 1`** (`schemas/result.schema.json:10`) |
| `runs ls --json` (`lsDoc` + `runview.Row`) | one row key `failure_class`, and one `lsDoc` key `groups` under `--group-by` | `console/docs/json-contract.md` §5's row table, held by the struct-reflecting pin (`internal/cli/docs_desktop_console_test.go:353`); **`groups` must be `omitempty`** so the embedded `watchDegradedDoc` never emits it (F14) | no version key exists on this document; additive |
| `fugaro diagnose --json` (`Diagnosis`) | one field, if D1460 answers yes | `console/docs/json-contract.md` §6.1 (`:748`) | additive |
| the digest's committed report and its `summary` block | the Failures table, `failures` in the block, two rules | `docs/periodic-reviews/`'s rule list and D326's document (F24) | the block is read key by key; the two rules read only blocks that carry the key |
| `console/docs/json-contract.md` | the rows above | — | the contract's own version constant is the console's; a row is additive |
| the console's redactor | `failure_class` in `CONTRACT_KEYS` (`console/src/source/redact.ts:1159`) | — | console-internal (F22) |
| the plugin's status reference | `failure_class` among the fourteen per-run keys that sentence names (`plugin/skills/working/reference/status.md:31`) | — | the skill is prose, held by CONTRIBUTING's rule (F13); the sentence is a selection of the row's 44 keys, so the pin holds what it names, not the set |
| `mobile/`, `shared/` | **nothing** — and `groups` must not reach the phone's document | — | — |

**What does not move, and is named so it is not discovered later:** `result.json`'s `version`, `reason`
(text and meaning), `status`, `outcome`, `halt`, `internal/userstats`' census, `runs summary`'s states,
`watch`'s `isFailure`, `internal/watch`'s feed shapes, `internal/runner`'s `fail`/`failLocked`
signatures, the pull request's report section, the six digest flags, and `fugaro doctor`, `fugaro runs
stats` and every command the design's §6.3 does not name.

---

## 6. Decisions D1450–D1469

**Every one of these is answered, as recommended, on 2026-11-07.** The owner's dated answer is in
every row — put to him through `AskUserQuestion`, every option the recommended one, and he took all
of them — and every task in [implementation-plan.md](implementation-plan.md) was approved the same day
(owner: "All tasks approved. Jobmax"), so no gate the plan names is still open — **FC1's included, and
FC1 is the one task of the nine that is built and merged; FC2–FC9 are not started.** Each row states
the question, what this reconciliation recommends and why, the alternative, and what the alternative
costs. **No row re-asks one of the design's §8 D1–D9 or §9 O1–O6**; where a question here touches one
of them, the row says so and leaves that question to the design's own numbering.

| # | Question | Recommendation (and why) | Alternative | Its cost | Owner's answer |
| --- | --- | --- | --- | --- | --- |
| **D1450** | Does this plan keep the design's task names and its §10 order, or start its own series? | **Keep FC1–FC8 and add FC9** (F13). The design's §10 is the same work written in the same convention, and a second series is two names for one task — the failure mode `docs/catalog-build-context` F29 names for two plans of one feature | A new series (FK1…) with its own edges and levels | Two documents listing the same work under two sets of names, and a reviewer reconciling them by hand at every build | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1451** | Is [spec.md](spec.md) a verbatim copy of the merged design, or a pointer to it? | **A verbatim copy with a dated header** (what this folder does). The convention's four files give a reader the document the reconciliation is answerable to without leaving the folder, and the header states which copy is pinned | A one-paragraph pointer to `docs/design/failure-classes.md` | The folder has no spec; the four-file shape breaks; a reader of the reconciliation follows every citation into another folder, and the *spec wins* rule has nothing to point at | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1452** | Where does `recordEnd` live: in `internal/runner/runner.go`, or in a file of its own? | **A file of its own, `internal/runner/recordend.go`.** The writer's rule is the one thing FC2's diff has to make reviewable, and §10's FC2 is already at 11 files against the twelve-file cap; a 4,000-line file also makes the shared-files row meaningless | `runner.go`, beside `finalize` | one 4,000-line file is the diff of record and of two other tasks (FC3, FC4), and the cap is spent before the guard test is in the list | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1453** | Does the vocabulary live in a new `internal/failure` package, or in `internal/runstore`? | **`internal/failure`.** Three packages need it (`runstore` for the field's type, `runview` for the reader, `digest` for the table) and `runstore` importing nothing new is the property its own graph is built on; the type is a wire vocabulary and belongs with the other closed ones (`"the shape `runstore.HaltReason` … has`) | `internal/runstore/failure.go` | the runner, the reader and the digest import the record's package for a value that is not a record, and the "one slice the tests read" acquires the record's dependency weight | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1454** | Where does the legacy reader live, and under which of the design's two names (F20)? | **`internal/runview.ClassOf`, exported, called by the digest** — the name FC5 gives it, not §5's `legacyClass`, because it is the one function both readers call and a reader in another package cannot call an unexported one. It cannot live in `internal/failure`, which §D1453 forbids importing `runstore` (F21) | `failure.ClassOf(status, outcome, reason string, halt string)`, taking the four scalars instead of the record | the reader's signature has to grow every time a legacy pattern needs a fifth input, and two call sites build the same four values by hand | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1455** | Where does the `runstore.HaltReason` → class translation live, so a legacy halted row and a new one cannot disagree? | **Two thin switches over one table of names**: `haltClass` in `internal/runner` for the writer, and the same mapping read by `internal/runview` for old records, with a test that walks `runstore.GovernanceHaltReasons()` plus the budget and credential families and asserts both agree. It cannot be one function in `internal/failure` for the same import reason (F21) | one switch in `internal/failure` over the halt reason's **string** value | the string form is the one thing the closed type exists to avoid: a halt reason renamed would compile everywhere and read as a budget halt, and the reader's table would be the only place that noticed | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1456** | Is FC2 one task (11 files, `runner.go` plus eight sibling files) or two? | **One task, with `recordend.go`** (D1452). The change is mechanical and the guard test is what makes it safe; splitting it leaves one half of the tree writing a status directly, which is the state the guard fails on | Split into "the writer and the helpers" and "the deferred and bootstrap sites" | the first half cannot land green (the guard sees the second half's statements) unless the guard is weakened to a list, which is the pin the design rejects | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1457** | Does `plugin/skills/working/reference/status.md` gain `failure_class` in the same PR as the row (F13)? | **Yes — FC9, in the same PR as the row.** CONTRIBUTING's rule is that a CLI change touching a documented command changes the skill in the same PR, the skill names fourteen of the row's keys in prose (§2.7), and the key is the answer to the question the skill's own "Needs attention" section tells a user to ask | leave the skill to a later PR | `runs ls --json` carries a key the skill never names, and a user asking "why did this fail?" is told to run `diagnose`, which is the state the design exists to leave | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1458** | Is `lsDoc.Groups` `omitempty`, and does anything hold it out of the watch document (F14)? | **Yes, and a test.** The phone's reader refuses an unknown key in the degraded document (`mobile/src/feed/document.js:122`, `:358`) and `watchDegradedDoc` embeds `lsDoc` (`internal/cli/watch.go:518`), so an emitted `groups` breaks the app; `--group-by` is never a watch flag, so `omitempty` is enough, and a test that decodes the watch document's key set holds it | emit `groups: {}` always | the phone refuses the whole degraded document — the app renders degraded-as-nothing — for a block it does not read | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1459** | Does the console task add `failure_class` to `CONTRACT_KEYS` in `console/src/source/redact.ts:1159` (F22)? | **Yes, and the console task names the file.** The set is contract-wide — the fields whose values the console never rewrites as a person (§2.4), not the row's keys — and a class value not in it is a value the person pass may rewrite, i.e. a figure drawn wrong with no error | the chip or column alone | the redactor treats a class value as a possible person's name, and a recording's class can come back as a pseudonym | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1460** | Does `fugaro diagnose` print the class, and does `Diagnosis` carry it (F23)? | **Both, one field and one line** — the design's D9 recommends the line and the shape half is what makes it honest: diagnose's `--json` is a contract (`console/docs/json-contract.md:748`) and a text-only answer would leave the console able to print the class for `runs ls` and not for the one run a person is looking at | the printed line only | the two documents disagree about one run: the listing says `review_not_converged` and diagnose, the command that explains it, cannot | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1461** | Does `internal/watch`'s own feed (`JSONRun`, `JSONFinished`) carry the class? | **No.** The feed is a different document with its own contract and its own reader, the class needs a decision about the watch screen (a column, a flag) first, and the design deliberately leaves `watch` out; a second consumer added silently is the opposite of what its §7 argues | add the key to `JSONFinished` | the phone and the console's live screen grow a key whose screen has not been designed, and the contract row is written before the reading of it | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1462** | Does the plan add a `internal/cli/docs_failure_classes_test.go` pin of its own? | **No.** The docs-pin census counts `internal/cli/docs_*_test.go` files (**101** today, `docs/rigor-modes/reconciliation.md:752`) and the design promises not to move it (F28); the plan's pins go into the existing `docs_pr_state_test.go`, which is where the design's own pin lives, and this folder's documents are pinned by nothing, as the convention's other folders are | a pin file for this folder | the census moves, the row `docs/rigor-modes/reconciliation.md:752` goes stale, and **four folders' pins over that one row redden**: `internal/cli/docs_rigor_modes_test.go:388` runs every census row's own command, and `internal/cli/docs_config_management_test.go:397`, `docs_managed_agent_converge_test.go:190` and `docs_repo_image_removal_test.go:525` each re-read the row and compare it with the tree's glob — so a merge that added a pin file without moving the row is a red build, and the design's own sentence about the count becomes false | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1463** | Which document owns the two digest rules' prose (F24)? | **`docs/periodic-reviews/`'s rule list, with D326's document named as the rule's source** — the rule *list* is written there (`spec.md:114`, `implementation-plan.md:266`, `:279`) and the plugin's own reference is a fourth place that must not become a fifth | `docs/task-governance/reconciliation.md` alone, beside D326 | the rule is written where the reader of the *report* does not look, and the skill's list and the digest's constants become two lists that drift | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1464** | Is a class written on a `mode: review` run that reviewed, or on an artifact run that delivered? | **No** (the design's §3, restated as a plan rule): those runs have no failure to name, `outcome` already says what they delivered, and a class on them would be a classification of a success. The task's guard asserts the field is absent, so a later writer cannot add one without a decision | classify them `unclassified` | every successful delivery carries a value that means "the vocabulary does not hold this", and the report's share of classified runs stops meaning anything | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1465** | In the grouped table, is `share` a share of the window's **runs** or of its spend? | **Runs**, with the spend beside it as its own column and the totals line's two figures unchanged. "33% of runs, 42% of spend" is two readings of one window, and a single `share` column that could be either is a figure a reader takes the wrong way | spend | the biggest class by spend and the biggest by count print the same `share`, and the row a person acts on is not the row that is marked | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1466** | Do the two digest rules ship in this work, or does the table ship alone (F26)? | **The table ships; the rules are FC7's second half and may be dropped without touching FC7's first half.** A rule reads only summaries carrying the block, so the first week after the release raises neither (the design says so) and the rules cost a week of silence either way | hold the rules back entirely | the digest's whole point (D326) is that a committed report is read over time, and a table with no rule is a figure somebody has to remember | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1467** | Does `fugaro runs stats` / `internal/userstats` learn about classes (F9, §2.6)? | **No.** The census counts statuses by person and reads no rows: `userstats.Counts` (`internal/userstats/userstats.go:125`) is filled from `runstore.Status` alone (`:196`). A class census is a different question (by cause, not by person) and `runs ls --group-by` is where it lives | a class column in the census | the census document grows a dimension its own reader (`fugaro runs stats --json`, the console's spend screen) has no use for, and its "they add up" invariant grows a term | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1468** | Must `docs/design/v1.md` §4.6 gain the field's sentence, and is that a doc-pin risk? | **Yes, one sentence, in FC8** — §4.6 is the record's own field list (`docs/design/v1.md:406`) and a field it does not name is a field the next reader of the design does not know exists. The doc pins read `docs/`, and the Docs run is FC8's gate | leave `v1.md` to the release notes | the design of record and the record disagree, which is the state `docs/design/v1.md` exists to prevent | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |
| **D1469** | What release carries it, and does it owe an operator step? | **The owner's 0.22.0**, stated in [README.md](README.md) and [implementation-plan.md](implementation-plan.md) §5. `docs/release-streams.yaml`'s cli stream reads `current: "0.20.3"`, `next: Rhino`, so 0.22.0 is the name after Rhino, and `docs/catalog-build-context`'s D1409 already names 0.22.0 for its own work — a shared slot, not a second claim of the number. **No operator step**: `fugaro update` is the whole upgrade (F29) | a slot of its own, earlier | a second release for a field addition that owes nothing, and the runs between the two releases carry classes only the second one reads | Recommended -- owner 2026-11-07 (AskUserQuestion: all recommended) |

---

## 7. The number block, and how it was read

**Read twice on 2026-11-07.** First against `origin/main` at **`56c91f66`** — the commit this branch is
based on — with a fresh `git fetch` of **every** open pull request's head (`git fetch origin
pull/<n>/head:pr<n>` for the twenty open pull requests, #620 through #1819), while this folder was
written. Then again against `origin/main` at **`4dc17549`**, which is what `main` is now: the tree had
moved (39 files, two of them new docs folders), so every figure below is the **second** reading, and
the commands are the ones that produced it. The second reading could **not** re-fetch the pull-request
heads — this job has no network — so everything below that is a statement about a head is the first
reading's, and §8 carries it as UNVERIFIED. A statement about `main` is re-read.

**Decisions: D1450–D1469.** Commands, with the results of the second reading beside them:

```
git grep -ohE '\bD1[0-9]{3}\b' origin/main -- docs | sort -n | uniq | tail -1   # D1499
git grep -ohE '\bD14[5-6][0-9]\b' origin/main -- docs internal ee console mobile plugin schemas website | wc -l   # 0
```

**What `main` holds now.** The highest number **written** on `main` is **D1489** and the highest
**appearing** is **D1499**, both in `docs/console-managed-screens`: that work holds D1470–D1489 and
reserves D1490–D1499, and it took them **above** the numbers of its day rather than in the gap —
*"so that two works in flight cannot claim adjacent numbers and collide at their merge"*, the reason
it gives for reading the highest on `main` as **D1409** and the highest in a pull-request head as
**D1429**. `docs/api-key-review` — pull request **#1819**, which this folder read while it was open and
which has merged since — holds **D1410–D1429** on `main` today, and **Check 330** with it. **D1430–D1469
is therefore a gap both works stepped over** — the file says so itself, taking its block *"above D1429
rather than at D1430"* (`docs/console-managed-screens/reconciliation.md:280`) — and this work's block is
its upper half.

**What this folder holds.** **D1450–D1469**, taken on the day the highest claim was D1429 (in #1819),
**twenty-one above it** (D1430–D1449 left free below the block, which is why that stretch is the lower
half of the gap the two later works stepped over) — the same reading of `main`'s highest D1409 and the
highest in a head D1429 the other two works took on that day, which is why none of the three reads
another's numbers. **The second command prints nothing on `main`**: no path on `main` uses `D145x`
or `D146x`, so the block is still unused, and the two blocks `main` has taken since (D1410–D1429,
D1470–D1499) are neither inside it nor adjacent to it. Positive controls were run so that "nothing" is
not a broken command: `D1409` is found in `docs/catalog-build-context/README.md`, and `D1429` in
`docs/api-key-review/reconciliation.md`.

**The first reading** ran the same greps over the **twenty fetched pull-request heads** as well, with
`for r in $(git for-each-ref --format='%(refname)' refs/remotes/pr/*); do git grep -ohE
'\bD14[5-6][0-9]\b' "$r" -- docs internal ee console mobile plugin schemas website; done`, and over the
same heads for `Check 3[0-9]{2}` and `Check 3[5-9][0-9]`: nothing answered in either. Those runs are
what made this block a claim about the whole tree rather than about `main` alone, and the second
reading could not repeat them (§8).

**Checks: Check 350+.** Commands:

```
git grep -ohE '^## Check [0-9]+' origin/main -- docs | awk '{print $3}' | sort -n | tail -1   # 290
git grep -ohE 'Check 3[0-9]{2}' origin/main -- docs | sort -u    # 300 301 302 303 304 305 309 311 313 314 330 360
git grep -ohE 'Check 3(5[0-9])' origin/main -- docs | sort -u    # (nothing)
```

The highest **written** `## Check` section anywhere is still **290** (`docs/gcp-live-checklist.md`, the
mixed-credentials run — and `docs/api-key-review` reads that row as "USER-RUN, NOT RUN", which is why
its own check carries a contrast case rather than assuming 290's numbers exist). `main` names 300–305,
309 and 311–314 as blocks other designs claim (`docs/stateless-orchestrator`,
`docs/cli-output-console-graph`), **330** (`docs/api-key-review`) and **360**
(`docs/console-managed-screens`, which reads 361–369 as available above it). **Nothing on `main` names
a number in 350–359**, the gap between those two claims, so this work takes **Check 350** and
reserves 351–359 for a second owner-run check if a task needs one. Neither claim around it collides
with it: 350 is neither 330 nor 360, and the register's rule renumbers the later of two **colliding**
claims, which this is not. The register's own summary row and `## Check 350` section are written by the
task that implements the check, under the number free **at that merge** — the register's rule, and not
this folder's choice.

---

## 8. UNVERIFIED

- **The design's §2 figures.** "585 runs, 191 not succeeded (33%), $354.68 of $844.27", the per-cause
  rows, and the premise that "the arithmetic was done by hand in a shell" are the owner's own reading
  of a real installation's bucket. **Nothing in this tree can confirm any of it** — no runs bucket, no
  report, and the design quotes no run id. They are the reason the work exists, and they are not
  evidence about the code.
- **The §2 per-cause rows' causes.** The three causes inside `the agent reported an error`, the
  GraphQL-ready failure, the push race and the merged-during-run case are named as *observed in a
  window*, not as states the tree records. The vocabulary is designed to count them; whether a new
  window shows the same shapes is what Check 350 and the first report after the release will say.
- **The retention window.** The design says records are kept 90 days and cites
  `docs/rigor-modes/reconciliation.md` F24. That is a claim about a deployed bucket's lifecycle rule,
  not about a file in this tree; the plan's `ClassOf` is written so it is right either way (an old
  record is read, never written back).
- **The pull-request-head half of the number reading.** §7's first reading fetched **every** open pull
  request's head on 2026-11-07 — twenty of them, #620 through #1819 — and found nothing in this work's
  two blocks; that is what makes the blocks claims about the whole tree and not about `main`. **The
  re-read could not repeat it**: the job that took the second reading has no network, so `git
  ls-remote` returns nothing and no `refs/pull/*` ref exists in this clone. Everything §7 says about
  `main` (`4dc17549`) is re-read; everything it says about a head is the first reading's, quoted as it
  was taken.
- **The console's and the phone's behaviour.** Both were read as source; neither was run. The design's
  §7 claim "a reader older than the field sees the record and the row it always saw" is a claim about
  shipped builds, and the version floors it would need (`console/docs/json-contract.md`'s own version
  constant, the phone's own release) are not stated anywhere this folder could read.

---

## 9. Open items

These are the owner's, and none is decided here. **They are the design's own §9 items**, repeated here so that a reader of
the plan sees them in the same place as its gates; this folder adds no Open item of its own and
answers none.

1. **O1 — the label** (design §8 D5): whether a run that delivered a usable draft because its review
   did not converge should be reported as `failed` at all, or carry a status of its own; and if it
   should, whether that value is the one the no-verdict work plans. Five readers change either way
   (F9), and the plugin's status table is a sixth surface that explains them.
2. **O2 — the vocabulary's size** (design §8 D2): nineteen values on the "a different act follows"
   rule, or fewer.
3. **O3 — whether the class may carry a reason beyond the enum** (design §4.3): this design says no.
4. **O4 — the report's audience**: whether the committed Failures table may name run ids.
5. **O5 — the thresholds** (design §8 D7): what "high" means.
6. **O6 — no class names the spent subscription**: the OAuth account's usage limit is written by
   `stageErrorReason` today (`internal/runner/runner.go:3759`) and falls into
   `stage_error_unexplained`; whether it earns a value of its own is a ruling on O2.

**Everything else in this folder is a decision row (§6) or a finding (§4) — and the rows are
answered: every one of D1450–D1469 takes the recommendation, 2026-11-07.** Every task in
[implementation-plan.md](implementation-plan.md) is approved the same day (owner: "All tasks
approved. Jobmax"), so the gates that named those rows are satisfied. The design's own §10 status
stands unchanged beside all of it, in `docs/design/failure-classes.md`, which is the pinned copy
[spec.md](spec.md) carries and is not this folder's to rewrite.
