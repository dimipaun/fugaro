# The console's managed-project screens: implementation plan

**Nothing here is approved, and nothing here may start.** Unlike a plan whose decisions are answered,
this one is blocked twice over: **not one of D1470–D1489 has an owner's answer**
([reconciliation.md](reconciliation.md) §7), and the tasks below are written against the
recommendations, so a task may not begin before **its own** rows are answered — an answer to a
neighbouring row is not an approval, and an answer is not an approval either. The plan is here so the
owner can price the work and answer with the cost in front of him, which is what the folder's status
line means: **plan only; nothing is implemented; no task approved.**

This document adds no code, no schema, no console source, no fixture and no workflow: it names the
files the tasks will touch and edits none of them.

**The dependency that orders everything is not in this repository.** The credentials-service section
(CM9) draws the line **DON7** adds, and DON7 is open pull request **#1816**
([reconciliation.md](reconciliation.md) F22). CM9 waits for that merge; every other task can be built
against `origin/main` at `56c91f66` as it stands.

## 1. The order, and what forces it

```
   CM1  the record the last update leaves        CM2  fugaro managed status --json
        └──────────────┬───────────────────────────────┘        ← level 0, two independent CLI tasks
                       │
                      CM3  rule 6's two rows, on the CLI's side  ← level 1 (needs CM2's command to exist)
                       │
                      CM4  the two rows, on the console's side   ← level 2 (the fence before the reader)
                       │
   ┌───────────┬───────┴────┬───────────┬────────────┐
  CM5         CM6          CM7         CM8          CM9          ← level 3, five independent sections
   │           │            │           │            │              (CM9 also waits on DON7, #1816)
   └───────────┴────────────┴───────────┴────────────┘
                       │
                     CM10  the sections join the Project tab      ← level 4
                       │
                     CM11  the fixture arms and the end to end     ← level 5
                       │
                     CM12  the release notes, console 0.121.0      ← level 6
```

**Why CM1 and CM2 are level 0 and independent.** They share no file: CM1 writes the installation's own
state (`internal/localcfg` and the two `internal/cli` files that run the retire stage), CM2 writes a
new command and the contract section its document needs. CM1 is what makes the retirements screen
possible at all ([reconciliation.md](reconciliation.md) F21, D1484); CM2 is what makes the managed
state and the audit trail possible (F13, D1473). Neither can be built on top of the other, and neither
is a prerequisite of the other.

**Why CM3 comes after CM2 and not beside it.** Rule 6's table names an **exact argv**, and the Go test
that pins it (`TestDocsDesktopConsoleDesignAllowListIsExactArgv`) is a closed list — so the row cannot
be added before the command it names exists without the table naming an argv no binary answers. CM3
also touches `internal/cli/docs_desktop_console_test.go`, which CM2 touches, and that is why they are
in different levels rather than the same one (§2).

**Why CM4 comes after CM3.** The fence is one thing with two halves (F26): the design's table and
`consoleReadPaths` on the Go side, `SPAWN_COMMANDS` on the console's. The console half is a
**SECURITY-SENSITIVE** change to the spawn table and to the operations surface, and the design's rule
is that the table and the code move together — which a reviewer can only check if the table is already
there.

**Why the five sections are level 3 and parallel by construction.** Each is a **module of its own**
(`console/src/renderer/screens/managed.ts` and its four siblings, D1488) plus **its own test**, and
none of them writes `projects.ts` or any other file a sibling writes. The tab's own file has **one
writer per level**: CM10, alone at level 4.

**Why CM11 is after CM10.** Its end-to-end spec drives the real tab, so it needs the sections wired in.
Its fixture arms could land earlier, but a fixture arm no test reads is a file with no reviewer, and
the fixture is one file for every read the sections spawn — one writer, one level (§2).

**Why CM12 is last.** The release notes name what shipped; `docs/releases/README.md` requires the
notes in the same pull request as the change they describe, which is CM11's and CM10's.

## 2. Shared files

Every file more than one task here writes, and the level each writer is at. **No level has two writers
of one file** — that is the invariant the levels above exist to hold, and this table is the proof.

| File | Written by (level) | What each writes |
| --- | --- | --- |
| `internal/cli/docs_desktop_console_test.go` | CM2 (0), CM3 (1) | CM2 adds the new document's structs to the shape list and the new command to the flag inventory; CM3 adds the two argvs to rule 6's `want` list and moves the command count 21 → 23 |
| `console/docs/json-contract.md` | CM2 (0) | §6.21, the new document's own section (F27) |
| `docs/design/desktop-console.md` | CM3 (1) | §5.1 rule 6's table gains two rows and rule 5's operation count moves |
| `console/src/main/spawn.ts`, `console/src/main/ipc.ts`, `console/src/preload/index.ts` | CM4 (2) | the two spawn rows and the two operations, with the preload's written-out method list |
| `console/test/unit/spawn.test.ts`, `ipc.test.ts`, `preload-surface.test.ts` | CM4 (2) | the ordered words list, the operations snapshot, the method list |
| `console/src/renderer/screens/projects.ts`, `console/src/renderer/entry.ts` | CM10 (4) | the five sections' wiring into the Project tab |
| `console/test/unit/entry-wiring.test.ts`, `build-files.test.ts` | CM10 (4) | the wiring and the module list |
| `docs/design/console-styling.md` | CM10 (4) | §2.5 gains the five sections' class hooks |
| `console/test/fixtures/bin/fugaro` | CM11 (5) | an arm for each of the two reads CM4 added; the fixture already answers `settings --json` for the tab's own panels |
| `docs/gcp-live-checklist.md` | CM11 (5) | Check 360's own section, and the register's row |
| `docs/releases/console/v0.121.0.md`, `docs/releases/console/README.md`, `console/package.json` | CM12 (6) | the notes, the next fruit in that directory's own table, and the version line |

**Files a task shares with work outside this design**, so a conflict there is a semantic one:
`internal/cli/init.go`, `internal/cli/init_retire.go` (the update/init path, touched by every
installation change), `internal/localcfg` (the installation's own state, beside
`checkouts.go`), `internal/cli/project.go` (`consoleReadPaths`, beside the resolver every cloud
command goes through), and `console/src/main/ipc.ts` and `spawn.ts` (the console's two largest
reviewable artifacts, touched by every console feature).

## 3. The tasks

### CM1. The last plain `fugaro update` leaves a record of what it retired, and of the runs in flight when it did (F20, F21, D1484, D1485). **size M**

- **Files:** `internal/localcfg/updaterecord.go` (new — the record's own read and write, in the state
  directory `CheckoutsFile` already lives in), `internal/localcfg/updaterecord_test.go` (new),
  `internal/cli/init_retire.go` (the write, where the retirement set is final), `internal/cli/init.go`
  (the record joins `initResult`'s `retire` member in `--json`, so the run that made it prints it too),
  `internal/cli/init_retire_test.go`.
- **Interface:** a JSON document under `localcfg`'s state directory holding, per project: `at`, the
  `retire` rows exactly as `infra.Retirement` marshals them (`address`, `kind`, `cause`, `kept`,
  `logical`, `secret_id`, `job`, `region`, `protected`), and `runs_in_flight` — the number
  `runsInFlight` returned for the run that wrote it. **Additive**: a new member on a document the
  console, mobile and history do not read today, so no format version moves (a field never changes
  meaning).
- **Red step:** `TestUpdateRecordsTheRetirementsItMade` fails on today's tree — after an update whose
  plan retires a derived resource, nothing under the state directory names it.
- **Tests:** `TestUpdateRecordsTheRetirementsItMade`, `TestUpdateRecordsTheRunsInFlightItSaw`,
  `TestTheUpdateRecordIsTheLastRunsAndNotAMerge` (a second update replaces the first's set),
  `TestUpdateRecordRoundTrips`, `TestUpdateRecordInAnUnwritableStateDirectoryIsReported`. Command:
  `GOFLAGS=-p=2 go test ./internal/cli ./internal/localcfg -run 'UpdateRecord|Retirements|RunsInFlight' -count=1`.
- **Mutation (run, restore):** delete the write from `init_retire.go`; the first three fail.
- **Why the state directory and not the local config:** the local config is a file a person edits by
  hand, and `policyGenerationRecord` writes its one scalar into it with `yamledit` precisely because
  the whole struct must not be marshalled over their comments and order. A retirement set is a list,
  which is not that writer's shape, and the state directory beside it holds JSON documents already
  (`checkouts.json`).
- **Depends on:** nothing. **Reviewer tier:** standard. **Commit:** `cli: the last update records what
  it retired, and the runs in flight then`.

### CM2. `fugaro managed status --json` prints the marks and the trail (F10, F11, F12, F13, D1472, D1473, D1474, D1476, D1477). **SEC — size M**

- **Files:** `internal/cli/managed_status.go` (new — the command, and the document its `--json`
  prints), `internal/cli/managed_status_test.go` (new), `internal/cli/managed_apply.go` (the
  subcommand joins `newManagedCmd`'s three), `internal/cli/docs_desktop_console_test.go` (the new
  structs in the shape list, the command in the flag inventory), `console/docs/json-contract.md`
  (§6.21, the document's own section).
- **Interface:** `fugaro managed status --json [--project NAME] [--config PATH]` — one closed
  document: `project`, `gcp_project`, `managed`, `reason`, `version`, `generation` (the four
  `MarkFacts` fields, D1474), `audit` — the bounded subset of the newest record, D1476: `at`,
  `command`, `verdict`, `identity`, `plan_digest`, **never `argv`** — `audit_day` and `audit_count`,
  and `audit_state` for a read that did not answer. It reads through `managed.ReadMarkFacts` and
  `managed.ListAuditRecords`, the parser the record's own writer ships, and it never prints a
  "revoked" (D1475).
- **Red step:** `TestManagedStatusPrintsTheMarks` fails on today's tree — the command does not exist.
- **Tests:** `TestManagedStatusPrintsTheMarks`, `TestManagedStatusPrintsTheBoundedRecordSubset`,
  `TestManagedStatusNeverPrintsArgv`, `TestManagedStatusOnAnUnmanagedProjectSaysWhy`,
  `TestManagedStatusReadsTwoDaysAndSaysWhichTwo`, `TestManagedStatusUnreadIsNotAnEmptyDay`,
  `TestManagedStatusNamesItsFieldsInTheContract`. Command: `GOFLAGS=-p=2 go test ./internal/cli -run
  'ManagedStatus|Docs' -count=1` (the second name is the contract half, F27).
- **Mutation (run, restore):** add `Argv` to the document's record member;
  `TestManagedStatusNeverPrintsArgv` and the contract's invented-field test fail.
- **Why SEC:** the document prints an account identity and a plan digest, and its whole design is a
  disclosure bound — the same class of decision rule 6's table is.
- **Depends on:** nothing. **Commit:** `cli: fugaro managed status --json prints the marks and the
  trail`.

### CM3. The two reads join rule 6's fence, on the CLI's side (F5, F26). **SEC — size S**

- **Files:** `internal/cli/project.go` (`consoleReadPaths` gains `fugaro managed status` and `fugaro
  project policy show`), `internal/cli/docs_desktop_console_test.go` (rule 6's `want` list 22 → 24, and
  the command count 21 → 23), `internal/cli/checkouts_test.go` (both reads in
  `TestConsoleAllowListedReadsWriteNothing`), `docs/design/desktop-console.md` (§5.1 rule 6's table
  gains the two rows, rule 5's operation count moves).
- **Interface:** the two rows, spelled exactly as the console will spawn them — `managed status --json
  --project <name> [--config <path>]` and `project policy show --json --project <name> [--config
  <path>]` — and nothing else: no `--strict` on the first, no second read of `project show`.
- **Red step:** `TestDocsDesktopConsoleDesignAllowListIsExactArgv` fails the moment either row is in
  the design's table and not in its `want` list — which is the order this task writes them in, so the
  red step is the design's table edited first.
- **Tests:** the three named above, with `GOFLAGS=-p=2 go test ./internal/cli -run
  'AllowList|ConsoleAllowListedReads|Docs' -count=1`.
- **Mutation (run, restore):** remove one entry from `consoleReadPaths`; the read-only test fails
  naming the argv, which is the whole point of the pair.
- **Why SEC:** `consoleReadPaths` is what stops the resolver remembering a checkout on a console read
  — a command wrongly on it writes the operator's state file on every paint.
- **Depends on:** CM2 (the first row names a command CM2 adds). **Commit:** `console: the managed reads
  join rule 6's fence, on the CLI's side`.

### CM4. The two reads join the console's surface (F5, D1480, D1481). **SEC — size M**

- **Files:** `console/src/main/spawn.ts` (`SPAWN_COMMANDS` gains the two rows, `takesProject: true`),
  `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS` gains `managed.status` and `policy.show`),
  `console/src/preload/index.ts` (the written-out method list), `console/test/unit/spawn.test.ts` (the
  ordered words list, and the `takesProject` count 20 → 22), `console/test/unit/ipc.test.ts` (the
  operations snapshot and the preload-surface equality), `console/test/unit/preload-surface.test.ts`.
- **Interface:** `managed.status` with no argument beyond the target the surface already passes, and
  `policy.show` the same — both reads, both refused in a session whose installation is older than the
  CLI the row needs, and neither reachable from a renderer that has not asked.
- **Red step:** `spawn.test.ts`'s ordered list fails while `SPAWN_COMMANDS` carries a row the list does
  not — the red step is the row added to `spawn.ts` first.
- **Tests:** `npm run test:unit -- --maxWorkers=2`, and `npm run typecheck` for the preload's surface.
- **Mutation (run, restore):** add a row to `SPAWN_COMMANDS` without adding it to `ipc.ts`'s
  operations; the surface test fails naming the operation with no handler.
- **Why SEC:** the spawn table and the operations surface are the console's two fences, and every new
  row on either is a security review by the console rule.
- **Depends on:** CM3. **Commit:** `console: the managed reads reach the renderer as two operations`.

### CM5. The managed state and the audit trail, as a section (F11, F12, F13, D1472, D1474, D1475, D1476, D1477, D1488). **size M**

- **Files:** `console/src/renderer/screens/managed.ts` (new — the section, its decoder and its two
  blocks), `console/test/unit/managed.test.ts` (new).
- **Interface:** one section, one read (`managed.status`), two blocks — **the marks** (managed or not,
  the reason when it is not, the version and the generation) and **the trail** (the day, its count,
  and the newest record's time, command, verdict, identity and plan digest). Every string goes through
  `putText` with the class its wire key declares; the section refuses on its own read and names its own
  command, and it draws no "revoked".
- **Red step:** the test fails with the module absent; then, with the module present and the trail
  block removed, the second block's case fails — the section is one read and the test is written for
  both blocks.
- **Tests:** `console/test/unit/managed.test.ts` — the marks' four fields, a project that is not
  managed (the reason drawn and no trail), the trail's bounded fields, `argv` never drawn, a refusal
  naming `managed status`, and a busy state that does not hold the other sections.
- **Mutation (run, restore):** draw the record's raw document instead of the bounded subset; the
  `argv` case fails.
- **Depends on:** CM4. **Reviewer tier:** standard. **Commit:** `console: the managed state and the
  audit trail, as a section`.

### CM6. The review account keys, as a section (F16, F17, D1478, D1479). **size S**

- **Files:** `console/src/renderer/screens/accountkeys.ts` (new), `console/test/unit/accountkeys.test.ts`
  (new).
- **Interface:** one section, one read — `settings.read`, the read the tab already makes, so this
  section **adds no operation** — showing `default_claude_account` and the stage rows of
  `stages.*.claude_account` with the stage each belongs to. A row the read marks `sensitive` is
  dropped whole; a key the read does not carry is drawn as unset, not as empty.
- **Red step:** the test fails with the module absent; with the module present and the sensitive-row
  guard removed, `TestAccountKeysDropASensitiveRow` fails.
- **Tests:** `console/test/unit/accountkeys.test.ts` — both keys drawn with their values, the stage
  mapping, a sensitive row dropped, a missing key drawn as unset, and no control on any row.
- **Mutation (run, restore):** draw the sensitive row with an empty value instead of dropping it; the
  guard's case fails.
- **Depends on:** CM4. **Reviewer tier:** standard. **Commit:** `console: the review account keys, as a
  section`.

### CM7. The policy notes, as a section (F4, F18, F19, D1481, D1482). **size M**

- **Files:** `console/src/renderer/screens/policynotes.ts` (new),
  `console/test/unit/policynotes.test.ts` (new).
- **Interface:** one section, two reads that already have rows — `policy.show` for the node (the
  members the screen draws: the accounts, `allow_repos`, `base_branch`, the routing and the image
  catalog) and `doctor --json` filtered to the six `policy-*` checks for what is wrong with it. The
  section says which of the two a line came from, and it claims no diff: the last publish's change is
  printed by `fugaro update` and recorded nowhere (D1482).
- **Red step:** the test fails with the module absent; with the module present and the check filter
  removed, `TestPolicyNotesDrawOnlyThePolicyChecks` fails on a `managed-audit` line.
- **Tests:** `console/test/unit/policynotes.test.ts` — the node's members drawn, the six checks drawn
  and no other check, a node that cannot be read refusing with its own command, and no line claiming
  what the last update changed.
- **Mutation (run, restore):** draw every check `doctor` returns; the filter's case fails.
- **Depends on:** CM4. **Reviewer tier:** standard. **Commit:** `console: the policy notes, as a
  section`.

### CM8. The last update's retirements, as a section (F21, D1484, D1485). **size S**

- **Files:** `console/src/renderer/screens/retirements.ts` (new),
  `console/test/unit/retirements.test.ts` (new).
- **Interface:** one section, one read — the record CM1 writes, through the operation CM4 added for
  the marks (`managed.status` carries it) or a row of its own if CM1's record is its own document; the
  task settles which with the owner and says so in its commit. It draws the rows' addresses, kinds,
  causes and flags, the time, and the runs in flight, and it says **"nothing retired by the last
  update"** and **"no update recorded yet"** as two different sentences.
- **Red step:** the test fails with the module absent; with the module present and the empty-record
  case merged into the empty-set case, `TestRetirementsTellNoRecordFromNoRetirements` fails.
- **Tests:** `console/test/unit/retirements.test.ts` — the set drawn with its flags, the two empty
  states distinguished, a record older than the installation's CLI refused, and no Terraform plan
  anywhere in the section.
- **Mutation (run, restore):** draw the retirement's `secret_id` as a value rather than a name; the
  naming case fails.
- **Depends on:** CM4 (and CM1, which is what gives it anything to draw). **Reviewer tier:** standard.
  **Commit:** `console: the last update's retirements, as a section`.

### CM9. The credentials service state, as a section — after DON7 (F22, D1486). **size S**

- **Files:** `console/src/renderer/screens/credentialsservice.ts` (new),
  `console/test/unit/credentialsservice.test.ts` (new).
- **Interface:** one section, one read — the `credentials-service-deployed` line of `doctor --json`,
  drawn as the CLI wrote it (its severity, its problem and its fix) and **re-derived nowhere**. An
  installation whose CLI predates DON7 carries no such check, and the section says exactly that
  rather than drawing "off".
- **Red step:** the test fails with the module absent; with the module present and the
  missing-check case drawn as a state, `TestCredentialsServiceSaysWhenTheCheckIsAbsent` fails.
- **Tests:** `console/test/unit/credentialsservice.test.ts` — the five service states drawn as the CLI
  words them, the per-repository held-off rows, the absent-check sentence, and no credential-shaped
  read anywhere in the module.
- **Mutation (run, restore):** read the bundle's own configuration instead of the check; the module's
  no-read assertion fails.
- **Depends on:** CM4, **and on DON7 (pull request #1816) merging** — the check it draws does not
  exist before that. **Reviewer tier:** standard. **Commit:** `console: the credentials service state,
  as a section`.

### CM10. The sections join the Project tab (F2, F3, F8, F9, D1470, D1487, D1488). **size S**

- **Files:** `console/src/renderer/screens/projects.ts` (the five sections registered as panels of the
  tab, each with its own source line in `PROJECTS_SOURCE_LINES`, and the policy panel's `operation`
  no longer null), `console/src/renderer/entry.ts` (the wiring), `console/test/unit/entry-wiring.test.ts`,
  `console/test/unit/build-files.test.ts`, `docs/design/console-styling.md` (§2.5).
- **Interface:** the tab's panels gain five, each with its own read, its own busy state and its own
  refusal, started in parallel and painted on arrival — and **jobs are not in the first paint**, which
  is the owner's rule of 2026-10-31. The tab's `view` stays `caps`, and the sections add no
  `REDUCED_VIEWS` entry (D1487).
- **Red step:** `entry-wiring.test.ts` fails while a module exists and the tab does not mount it.
- **Tests:** `console/test/unit/entry-wiring.test.ts`, `console/test/unit/build-files.test.ts`,
  `npm run typecheck`, and the golden frames are **not** re-recorded here (they are skipped in CI and
  the coordinator re-records them on the owner's machine).
- **Mutation (run, restore):** mount the sections inside the tab's first paint; the wiring test's
  first-paint case fails.
- **Depends on:** CM5–CM9. **Reviewer tier:** standard. **Commit:** `console: the managed sections join
  the Project tab`.

### CM11. The fixture arms and the hermetic end to end (F3). **size M**

- **Files:** `console/test/fixtures/bin/fugaro` (an arm for each of the two reads CM4 added — `managed
  status --json` and `project policy show --json` — each answering the argv CM4 allows and nothing
  else; the fixture already answers `settings --json` for the account keys section and the tab's
  existing panels), `console/test/unit/fixture.test.ts`, `console/test/e2e/managed.spec.ts`
  (new), `docs/gcp-live-checklist.md` (Check 360's section and the register's row, §6).
- **Interface:** the fixture answers the two new argvs and refuses any argv it was not given — the
  property that makes the console's hermetic suite a fence rather than a stub.
- **Red step:** the spec fails against a fixture with no arm for `managed status`, with the child
  answering a refusal the section draws.
- **Tests:** `npm run test:unit -- --maxWorkers=2` and `npm run test:e2e`; the spec walks the tab, sees
  the five sections paint independently, and sees one section refuse while its siblings draw. The
  check's own document is held by `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Mutation (run, restore):** make the fixture answer a read it was not asked for; `fixture.test.ts`
  fails.
- **Depends on:** CM10. **Reviewer tier:** standard. **Commit:** `console: the managed sections'
  fixture arms, end to end, and Check 360`.

### CM12. The release notes, console 0.121.0 (Raspberry) (D1489). **size S**

- **Files:** three, and **`docs/releases/console/README.md` is the one that says so** — a console
  release is not an entry of the CLI's changelog, and the CLI's `docs/releases/README.md` (whose
  `### For operators` section this task must **not** copy: the console's format has no such heading)
  governs the other directory, not this one. The three are:
  - `docs/releases/console/v0.121.0.md` (new) — the release body of `console-v0.121.0`: its first line
    is the heading `# Fugaro Console 0.121.0 "Raspberry"`, then one sentence for a user who has read
    nothing else, then a bullet per thing they can now do, then `### Known limits`. **No
    `### For operators`** and no `## Highlights` wrapper: that README's format is a heading and a body.
  - `docs/releases/console/README.md` — the fruit table gains its next row (`0.121.0` / Raspberry),
    which that README requires **"in the commit that writes its notes file"**.
  - `console/package.json` — the `version` line to `0.121.0`, the other half that README requires in
    the same commit, before the tag exists.
- **Interface:** none — this is the release's own documents. The notes name the CLI version the
  screens need (CM1's and CM2's), and they say in a user's words what the five sections give them.
- **Red step:** the reachable one is the docs tests' expectation of the file's shape; the release
  workflow's own refusals (a missing or empty notes file, a tag whose `X.Y.Z` is not the version
  line's) happen at the tag, which is after this commit and is `scripts/release.sh`'s.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`, and `npm run check` in
  `website/` — `website/test/console.mjs` reads `docs/releases/console/v<version>.md` against
  `console/package.json`'s version, which is exactly the pair this task writes.
- **Depends on:** CM10, CM11. **Reviewer tier:** standard. **Commit:** `docs: console 0.121.0,
  Raspberry`.

## 4. What is deliberately not in this plan

- **DON7, and any part of the credentials-service work.** CM9 draws doctor's line; it does not add it.
  The line is pull request **#1816**'s, and this plan does not duplicate it (D1486).
- **Any write.** No task adds a third write to the console, and no task puts a control on a screen that
  changes anything (D1471). The two writes stay rule 11's launch and rule 12's settings edit.
- **The recorded policy diff, and the recorded in-flight count at the publish.** Both were considered
  and refused with their costs (D1482, D1483). A task may not add either without a new decision.
- **A history the operator pages through.** The trail is the newest record of two days (D1477), and no
  task adds a date control or a paging control.
- **A `docs_console_managed_screens_test.go`.** This folder ships no pinning test (F25), so it does not
  move the rigor-modes census row that counts those files (**100**). The plan's tasks name their own
  pins; the documents here are held by review, as `docs/image-env/`'s are.
- **A sixth tab, and a reduced-view entry.** D1470 and D1487 recommend against both, and no task may
  take either without a new answer.
- **A new dependency**, in either language.

## 5. The release slot

**Console 0.121.0 (Raspberry)**, the slot the owner named in the ask (D1489). The CLI half (CM1, CM2)
ships in the CLI release that precedes it, because the screens read fields that release adds: a console
that shipped first would draw five empty sections and would be a promise rather than a feature. CM12 is
the console's own notes; the CLI's notes for CM1 and CM2 belong to the CLI release that carries them
and are that release's task, not this plan's.

## 6. Check 360, owner-run

The live check this work claims ([reconciliation.md](reconciliation.md) §8). It is **owner-run**: no
test in this repository may run it, because it needs an installation, a managed project and an
account — the three things the hermetic suite exists to avoid. It is written here as the register
expects, and the task that implements it writes the section in `docs/gcp-live-checklist.md` and the
row in the register.

**Check 360 — the managed screens show what the CLI says, on a managed project.**

1. On a machine with an installation and a managed project, run `fugaro update` once and let it
   finish. Record what it prints for the retirements and for the runs in flight.
2. Run `fugaro managed status --json` and `fugaro project policy show --json`, and keep both
   documents.
3. Open the console, select the project, and open the Project tab.
4. **Expected:** the five sections appear, each within its own read; the marks and the trail agree with
   the first document, the account keys with `fugaro settings --json`, the policy notes with the
   second document and with `fugaro doctor`'s policy lines, and the retirements with what step 1
   printed. **No section offers a control that changes anything**, and no token appears anywhere.
5. **Refutation:** any figure that disagrees with the document it came from, any section that waits
   for a sibling before drawing, or any control that writes — the last of which fails the check
   outright rather than being noted.

## 7. Gates (every task)

**CLI tasks (CM1–CM3):** `go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the
task's named tests with `GOFLAGS=-p=2 go test ./internal/cli -run '<names>' -count=1`; and
`GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1 -timeout 40m` whenever a document, the
contract or a `--json` shape changed. **Never the whole `internal/cli` package** — it takes about
twenty-five minutes.

**Console tasks (CM4–CM11):** `npm run typecheck`; `npm run test:unit -- --maxWorkers=2`; `npm run
test:e2e` for CM11. Goldens are **skipped in CI** and are re-recorded only by the coordinator on the
owner's machine — **no task here re-records one**.

**Every task that touches `docs/design/desktop-console.md` or `console/docs/json-contract.md`** runs
the `Docs` filter in the same commit, because the Go tests that pin both live there (F26, F27) and a
document changed without them is a red test on `main`.

**Security flag.** A task marked **SEC** carries `SECURITY-SENSITIVE: independent review mandatory` and
may not merge without that review. CM2 is SEC on its own merits (it bounds a disclosure), and CM3 and
CM4 are SEC by the console rule: every move of §5.1 rule 5's count or rule 6's table is.

**A task may not start before its own decisions are answered**, and every task here is blocked until
then (§7 of [reconciliation.md](reconciliation.md), whose owner's-answer cells are empty).
