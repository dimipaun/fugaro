# A closed failure class on every run record, and a failures report — spec, reconciliation and plan

**Status: plan only; nothing is implemented; no task approved.** This is a plan for the owner to read.
The design it reads is already merged (`docs/design/failure-classes.md`, pull request **#1746**, docs
only) and already pinned; what this folder adds is the reconciliation of that design against the code
on `main`, the reader census it needs and does not carry, and a plan sliced so the tasks can run in
parallel. It adds no code, no schema, no config key, no command and no Terraform; it edits no file
outside `docs/`; and **nothing in it is an approval** — every task in
[implementation-plan.md](implementation-plan.md) needs the owner's own go, and the first of them needs
the decisions below narrowed to a range first (see **Open items**).

**Asked by:** the repository owner, in a message of **2026-11-07** that put the work in the **0.22.0**
slot. The role is the one the design's own §2 describes: the person who owns the installation and
whose 7-day report the design's arithmetic came from. No `@handle` and no name is recorded anywhere in
this folder, by this repository's rule. The ask is reproduced in [spec.md](spec.md), which is a
verbatim copy of the design.

**What it is.** Today a run that did not succeed says why in one free-text `reason`
(`runstore.Record.Reason`, `internal/runstore/runstore.go:684`), and nothing groups those sentences:
`fugaro runs stats` counts runs by *person* and buckets statuses, `fugaro runs digest` writes six
per-run flags and no cause, and `fugaro diagnose` explains one run at a time. The design adds
`failure_class` — a closed enum of nineteen values, each with `kind`, `retryable` and `needs_owner` —
written by the one call that writes a run's terminal `status`, read for old records from `reason` and
never written back, and printed by `fugaro runs ls --group-by failure_class` and by a Failures table in
the digest. `reason` keeps its meaning byte for byte; the field is additive, so `result.json`'s
`version` stays `const 1`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | `docs/design/failure-classes.md`, **byte for byte**, with a header naming the owner by role, the pull request that merged it and the fact that the copy is dated. Where this file and the others disagree about what the design *says*, this file wins |
| [reconciliation.md](reconciliation.md) | Every claim of the design against the code on `main`, read by path and symbol: how the record and its five readers work today (§2: `result.json` and the schema, `runs ls --json`, `diagnose`, the console, the phone, the history census, the plugin and `watch`), where a failure reason is produced today (§3: `internal/runner`, `internal/runstore`, `internal/cli`'s `diagnose`), the findings **F1–F36** (Wrong / Exists / Gap / Rule / Open), the closed-field rule applied (§5), the decisions **D1450–D1469** with an **empty owner's-answer cell each** (§6), the number block and how it was read (§7), the **UNVERIFIED** section (§8) and the owner's **Open items** (§9). **Where it disagrees with [spec.md](spec.md) about the code, it wins** |
| [implementation-plan.md](implementation-plan.md) | The **six levels** and the tasks **FC1–FC9**, each with its files, its interfaces, its named tests **and the command that runs them**, what it depends on, its **red step**, its **reviewer tier**, whether it is owner-run and whether it is SEC, and its commit; the levels and the **critical path** (§1), the **shared-files table that holds no two writers of one file in a level** (§2), what is deliberately not in the plan (§4), the release slot the owner named (§5), the owner-run live check **Check 350** (§6) and the gates a task may not start without (§7) |
| `README.md` | This file: the status, the asker, the shape in one paragraph, the numbers, and what a reader should not take from here |

## What this folder found, in one paragraph

**The design's central claim is exactly true and already pinned: the runner writes a terminal status
in 36 statements, in 24 functions across 8 files** — reproduced statement by statement from
`grep -rn 'rec\.Status[^=!]*= runstore\.Status' internal/runner --include='*.go' | grep -v _test.go`,
and held by `TestDocsFailureClassCensusMatchesTheRunner`
(`internal/cli/docs_pr_state_test.go:250`), which counts it from the tree rather than trusting the
document. Everything the design's §1–§5 rests on — the free-text `reason`, `fail`/`failLocked` and the
three sites that overwrite the record's reason after them, the phrase-table classifier beside
`oauthLimitPhrases`, the six digest flags, the four pattern rules, the additive-schema precedent —
**exists, by name, where the design says it does**. The largest of what the reconciliation adds,
finding by finding (§4), are: **its line citations above `runner.go:3700` are four lines short** (F19) and the
`finalize` switch's range is wrong (F18); **the plugin's own status reference does name `runview.Row`'s keys** — fourteen of the
row's forty-four, in one sentence, as a selection and not as the set
(`plugin/skills/working/reference/status.md:31`) — so the design's §6.3 sentence that says nothing in
`plugin/` does is wrong, its §10 FC8 file list is one file short, and the task it adds is one key and
one clause (F13); **the phone reads `lsDoc` embedded and refuses
an unknown key in it**, which makes the report's new block a mobile question and not only a console one
(F14); **one test the design names does not exist** (F15); and **the vocabulary's import direction
decides where three functions can live**, a rule the design leaves implicit (F21); and **two of the
design's own sites do not hold together**: the class `endUnchanged` has to pass has no row in §5's
legacy table, so one sentence would read two ways across the release (F34), and §10's FC2 test name
asserts the class §3 of the same design leaves absent (F35); and **the file the design names for its
one sentence is nine bytes from a hard budget** — `plugin/skills/working/reference/diagnose.md` is
16375 bytes against `plugin/skills_lint_test.go`'s 16384 for a reference file, so that sentence is
written by shortening the prose it displaces, and the plugin lint is what says so (F36).

## What the reconciliation had to read first

The run record is a format read outside the same binary, so the design's whole safety argument is the
repository's own field rule: **adding a field is fine, removing or retyping one bumps the format's
version, and a field whose meaning must change gets a new name**. `failure_class` is a new name
beside `reason`, `result.json`'s `version` stays `const 1`, `runs ls --json` gains a row key and (under
`--group-by`) one top-level block, and the enum may grow inside the field without a version move. The
reconciliation §2 reads **every reader** — the console (`console/docs/json-contract.md`,
`console/src/source/redact.ts`'s `CONTRACT_KEYS`, `console/src/renderer/screens/run.ts`), the phone
(`mobile/src/feed/document.js`), the history census (`internal/userstats`, `runs summary`), the plugin
(`plugin/skills/working/reference/status.md`) and `watch`
(`internal/watch/finished.go`'s `isFailure`) — and §5 states the rule and what each one costs.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1450–D1469** | **this work's block, read above every allocation of the day it was taken and re-read against `main` since.** On 2026-11-07 the highest claim on `main` was **D1409** (`docs/catalog-build-context`) and the highest in an open head **D1429** (PR **#1819**), so this work took five above them; both of those works have merged since and taken **D1410–D1429** and **D1470–D1489** (reserving D1490–D1499), leaving **D1431–D1469 a gap both stepped over**, of which this block is the upper half. A `git grep` for `D14[5-6][0-9]` over `origin/main` today returns **nothing at all**, so the block is still unused and no number of another work is claimed. The readings are in [reconciliation.md](reconciliation.md) §7 with their commands |
| **Check 350** | this work's owner-run live check (implementation-plan §6). The register's highest **written** `## Check` section is **290** (`docs/gcp-live-checklist.md`); `main` names 300–305, 309 and 311–314 as blocks other designs claim, **330** (`docs/api-key-review`) and **360** (`docs/console-managed-screens`). **Nothing on `main` names a number in 350–359** — the gap between those two — so this work takes **350** and reserves 351–359, and neither claim collides with it. The register's own summary row and `## Check 350` section are written by the task that implements the check, under the number free **at that merge** |
| **F1–F36** | this folder's own numbering, in [reconciliation.md](reconciliation.md) §4, taking no number any other work reads |
| **O1–O6** | the **Open items** of [reconciliation.md](reconciliation.md) §9. **O1–O6 are the design's own** (§9 of the design: the label, the vocabulary's size, whether anything beyond the enum may be stored from model output, whether a committed report may name run ids, the thresholds, and the spent subscription). **Nothing in this folder answers one of them.** This folder's decisions are D1450–D1469, and the plan's gates name which of them a task waits on — an Open item is not a task's gate, and a task that needed one would say so |

## What a reader should not take from this folder

- **It is not an approval, and it is not a decision.** D1450–D1469 are questions with a recommended
  answer each and an **empty** owner's-answer cell; no recommendation here has been taken.
- **It is not a second design.** The design is merged and pinned; this folder does not re-argue it. It
  reads it against the tree, and where a citation drifted or a claim does not match the code, the
  reconciliation says so and the plan is written against the correction.
- **It does not answer the design's own decisions.** §8 D1–D9 and §9 O1–O6 of the design are the
  owner's and stay unanswered here; D1450–D1469 are plan-level questions the reconciliation raises,
  not a second numbering of the design's.
- **It claims no number of another work.** The decision block and the live check are read above every
  allocation on `main` and in the open pull requests (§7 of the reconciliation), and the register's own
  file is not this task's to write (implementation-plan §6).
- **It is docs only, and it stays docs only.** No Go file, no schema, no config, no `fugaro.yaml`, no
  `console/`, `mobile/`, `shared/`, `ee/` or `website/` file is touched by this folder, and no
  `.github/workflows/` file is touched by anything here — the design's tasks name their own files, and
  they are not this task's to edit.
