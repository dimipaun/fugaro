# The managed agent converge: implementation plan

**Status: a design, and nothing is built. Every decision in this design's block (D1011–D1026) is unanswered, and the tasks below are written to the recommendation in each row of [reconciliation.md](reconciliation.md) §3.** The owner answers them; a task whose decision is answered differently is rewritten before it starts. **Nothing here may begin before D1011 (the command set), D1012 (the rules) and D1013 (the cost bound) are answered** — the three that decide what the rest of the plan is.

Each task touches at most twelve files, names its tests, starts with a **red step**, and carries a **SEC** flag where it moves a security boundary. **Where this plan and [spec.md](spec.md) disagree, the reconciliation wins**; where a task and the reconciliation disagree, the reconciliation wins.

Slot: **CLI 0.15.0** (§6). Nothing here is a console, mobile, website, plugin or `ee` change, and no task may edit a file the 2026-10-30 rulings reserve.

## 1. What this plan does not own

- **The settings, rigor and accounts commands.** `fugaro settings set`, `fugaro rigor set` and `fugaro accounts add` are other designs' tasks, and they do not exist on `main` (reconciliation F17). Their gate is **a sentence in their own task and a rule in this gate** (D1018): when one of them lands, it calls the gate this plan builds, and its per-command rules are its own design's. No task here creates a command another design owns, and no file here is another design's.
- **`fugaro managed apply`, `fugaro managed flag`, the delete list, the deployer fence, the state bucket and the inventory.** They are cited and never touched (reconciliation §2). If a task finds itself needing a line inside one of them, it **stops and reports**.
- **`fugaro setup`'s questions and the setup skill.** M5 adds a field to what the creating run writes; it does not add a question, remove one, or edit `plugin/**`.
- **The setup wizard's Typed steps and the console.** D1019 grants the console nothing; no console task exists here.
- **The live checks.** No task adds, runs or edits a live step: the brief forbids one, and `docs/gcp-live-checklist.md` Check 68 (the deployer fence) stays the owner's own. Every test in this plan is hermetic (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`).
- **`internal/cli/root.go`.** Every command this plan changes is already registered; `unflag` (M8) is a subcommand of the `fugaro managed` group `internal/cli/managed_apply.go` already creates, so no task registers anything.

## 2. Order, and what forces it

**The `Needs` column is a code dependency: what must be merged for that task to build, test and be reviewed on its own.**

| Level | Tasks | Needs (code) |
| --- | --- | --- |
| 0 | **M0** | nothing |
| 1 | **M5**, **M6**, **M8** | M0 for M6; nothing for M5 and M8 (both are marker work) |
| 2 | **M1**, **M2**, **M3** | M0 (the gate), M6 (the audit write) |
| 3 | **M4** | M0, M5 (the recorded bound), M6 |
| 4 | **M7** | M6 (the record's reader) |
| 5 | **M9** | M1–M8 |

Three ordering facts are load-bearing and are why the table is not alphabetical:

- **M0 lands the gate before any command reads it.** The gate is the value every later task calls, and shipping a command that lifts the refusal before the gate exists is the one shape this design must not have (the parent's own Task 3.1: *"the apply is inert until phase 4 lands"*). M0 ships the type and its property tests and **wires it to nothing**.
- **M6 lands the audit writer before the commands that must write it.** D1014 says a failure to write the record fails the command, so a command shipped before the writer exists either writes nothing (the state the section exists to end) or fails every apply. The three command tasks each depend on it.
- **M5 lands the marker's version 3 before M4 reads it.** M4's bound is read from an object M5 makes possible, and M5's own task says what an object of an older version means (the default, never "no bound").
- **M8 needs nothing and may land at any level.** It is the revocation command (D1020), it depends on no gate, and an owner who wants out of managed mode before any other task lands is exactly the owner it is for.

**Three handoffs, which are not tasks.** Each of the settings, rigor and accounts designs owns its own `set`/`add`/`rm` command and its own rules, and each needs **its own** edit in **its own** file when it lands: not one sentence, because what the gate applies to each command is that design's own rules and they differ (§4.2, §9.3). The handoff is written here as the three rows below so the design's owner can copy their row rather than reconstruct it, and M9 records which rows have landed.

| Design | Its command | What its task adds | What stays refused in a managed project, and who refuses it |
| --- | --- | --- | --- |
| **settings** (`docs/settings-commands/`) | `fugaro settings set` / `unset` | The gate call, with **its registry** as the rule for a key, **its D854 generation guard** as the rule for a stale local copy, and its scope check for `--layer local` | The reserved keys (`credentials.*`, `clone_urls`, `budget.*`, `agent.oauth_token`, `governance.*`); a **floor removed or a ceiling widened**, which keeps its typed project name (the gate never admits a `Typed` class, D1026); a repository-scoped `set`, which is allowed today and is **not** refused here |
| **rigor** (`docs/rigor-command/`) | `fugaro rigor set` / `unset` | The gate call, with **its rank rule** (at or above the resolved mode), **D834's hotfix refusal** and **D838's glob rule** as the rules, and R1's layer publish as the same run's write | `hotfix` in any of its four forms (D834, unchanged, naming `fugaro run --mode hotfix`); a duplicate area glob (D838); the missing base-branch opt-in the design's R7 owes, **which this plan does not close and must not be read as closing** |
| **accounts** (`docs/accounts-add/`) | `fugaro accounts add` / `rm` | The gate call, with **its reserved names** (`reservedClaudeAccountName`, and `default`'s own rule), its profile/reference coverage check, and its `allow_data_to` rule as the rules | `add` with a widening `allow_data_to` — **D868's "`--yes` covers everything" is a widening inside that design and is still refused in a coding agent's session**; `rm` while a profile or repository still references the account; **any token entry at all**, which is §3.2's never-set and no gate admits |

**A row that has not landed leaves its command agent-refused in a managed project — the safe direction.** The gate is a call a command makes; a command that does not make it keeps the refusal it has today, so a settings `set` that shipped before its row landed would refuse an agent's session rather than run ungated. A row that landed and whose command is **not** in the gate's set at all would be a different bug: M9 checks the three rows against `spec.md` §3.1 before the release notes are written, and reports a mismatch rather than editing another design's file.

## 3. Gates (every task)

- `go build ./... && gofmt -l . && go vet ./...` — gofmt prints nothing.
- The task's named tests, `GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1 -timeout 40m`. **Never the whole `internal/cli` or `internal/runner` package.**
- The **wider pin gate** the brief sets: for every file the task changes or adds, `grep -rln '<its base name>' --include='*_test.go' internal/ cmd/ scripts/ deploy/ schemas/ plugin/ ee/ images/`, and each hit's tests run with a `-run` filter naming them. Any doc touched: `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m`.
- `go test ./internal/eeboundary/... -count=1`.
- **A test this plan adds that fails locally stops the task**: never push over it, never weaken, skip or delete it. Report it.
- **No new dependency** (`go.mod` unchanged), no TODO comment, no section-header comment, no comment that restates a name.

## 4. The shared-files table

Files more than one task touches, and the rule for each. **A task that needs a file in this table that another task owns adds its edit only after that task has merged**, and never re-derives what the owner landed.

| File | Tasks | What each one does with it |
| --- | --- | --- |
| `internal/managed/` (the package) | M0, M5, M6 | M0 adds the gate's own file; M5 adds the bound's read; M6 adds the audit's write. **Three new files in one package, one owner each, no shared file** |
| `internal/infra/managed/marker.go` | M5, M8 | M5 adds `MarkerVersionBuilds` and the field; M8 reads the same constants to report which half of the mark it removed. **M5's edit lands first**; M8 rebases on it and adds nothing to the constants |
| `internal/infra/managed/read.go` | M5, M6 | M5 adds the bound to what the read answers with; M6 adds the generation the audit records. Both **extend the existing read** rather than adding a second one — the file's own warning about `gcpProject` is why there is exactly one |
| `internal/cli/managed_flag.go` | M5, M8 | M5 adds the recorded bound to what `flag` writes; M8 adds `unflag` to the same file or a sibling that reuses its two checks. **M8 does not restate the checks** — it calls the same functions |
| `internal/cli/managed_apply.go` | none | **Untouched.** `unflag` (M8) is registered in the `fugaro managed` group `newManagedCmd` already builds, in whichever file M8 adds |
| `docs/managed-agent-converge/` | M9 | M9 is the only task that edits this folder, and it changes a *status* line, never a decision |
| `internal/cli/doctor*.go` | M7 | One new check file plus its test; the shared render helpers are read, never edited |
| `internal/cli/api.go` | none | **Reserved by the 2026-10-30 rulings.** M7's report section reads what core already re-exports there and adds no symbol; if it cannot, the section is a **handoff to the report's own owner** and M7 reports it rather than editing the file |

## M0. The gate's shape in `internal/managed` (D1021, D1022). **SEC**

Without it every later task would invent its own predicate over its own document, which is the drift D1012 refuses.

- **Interface.** One new file, `internal/managed/converge.go`: a `Converge` type with **unexported fields**, a safe answer at the zero value (**refuse**), one constructor per verdict source, one predicate and one sentence — `enable.go`'s and `check.go`'s shape, cited rather than described. It carries: the document's **digest** (D1022), the **class** the step's confirmation is (`initflow.YesOrTyped` or `Typed`), the **rules** that fired, and the verdict. `Refuse()` is the whole behaviour and is the **first** thing a command reads.
- **The class rule (D1026) lives here and in one place**: a `Typed` class is refused whatever the verdict — the gate can only narrow, and the check is inside the type so no caller can forget it.
- **Red step.** Write `TestConvergeZeroValueRefuses` (a `Converge` nobody built refuses, for every class) and `TestConvergeRefusesATypedClass` (a clean verdict over a `Typed` class is still refused) — both fail to compile against a package that has no such type, which is the red the task exists for. Then `TestConvergeDigestIsOfTheBytesRead` (two documents differing by one byte have different digests, and the digest a verdict carries is the one computed from the bytes the rules read).
- **Files (3).** `internal/managed/converge.go`, `internal/managed/converge_test.go`, and — only if the sentence needs it — `internal/managed/refusal.go`.
- **Not in this task**: any caller. M0 wires it to nothing, and a task that finds `converge.go` imported from `internal/cli` has done M1's job.

## M1. `fugaro update`'s stages under the gate (D1016, D1026). **SEC**

The door the HARD RULE makes the whole upgrade is the door the agent must be able to run, and it is the largest surface in the set.

- **Rules.** Terraform stages — `tf.ManagedCover` over the embedded roots plus `tf.Guard` with the owner's list; the rules stage against the binary's own `projectpolicy.RulesVersion`; the policy stage against `budgetDBUnadopted` **first** (D1017) and the D854 generation after; the **secrets** stage never admitted (§3.2 of the spec); the **Credentials** and **Compute** stages stay `noYes` and are not admitted. `tf.ManagedCosts` and `tf.ManagedCostRefusals` run over every plan the gate reads (reconciliation F8) — the plan's cost control already exists and this task must not re-derive one.
- **Where the refusal stops.** Per stage, before that stage's write, and the command's own later stages do not run: an unattended run that applied four stages and stopped at the fifth has left the installation half-converged, which the stage table's own ordering already guards and this task must not weaken.
- **Red step.** `TestUpdateManagedStageRefusesARemoval` — a fake plan with a `delete` on an address the owner's list does not name, in a managed project with an agent marker: today the run is refused before the stage is reached, and after the task it must be refused **by the gate**, with the gate's own sentence, and the test asserts which sentence. Then `TestUpdateManagedStageAdmitsAnAdditivePlan` (the same project, an additive plan, the stage runs and `AgentProceeded` is what a reader sees), `TestUpdateManagedNeverAdmitsTheSecretsStage`, and `TestUpdateManagedLeavesCredentialsAndComputeOwnerRun`.
- **Files (≤10).** `internal/cli/update.go`, `internal/cli/init.go` (the stage wiring), a new `internal/cli/update_managed.go`, and the tests; plus the per-stage call sites the wiring needs, named at implementation time.
- **SEC.** It is the command that can change the installation. Its refusals must be fail-closed on the three shapes M0's rules name: an unreadable plan, a missing configuration, an unknown plan field.

## M2. `fugaro config publish` under the gate (D1012). **SEC**

- **Rules.** Every key in the registry; none of the reserved ones (`credentials.*`, `clone_urls`, `budget.*`, `agent.oauth_token`, `governance.*`); no executable-key change (`commands.*`, `image.apt`, `image.setup`) without the owner's own flag; the D854 generation check; a diff that widens no scope rule. A widening of `allow_repos` or of a profile's scope is refused (rule 4.1.5).
- **Red step.** `TestConfigPublishManagedRefusesACredentialsKey` and `TestConfigPublishManagedRefusesAStaleGeneration` (each fails today because the command's agent refusal fires first and neither rule is reached) — and, on the other side, `TestConfigPublishManagedAdmitsARegistryKey` with `AgentProceeded`.
- **Files (≤6).** `internal/cli/config_publish.go`, a new `internal/cli/config_publish_managed.go`, and the tests.

## M3. `fugaro project policy publish` under the gate (D1012). **SEC**

- **Rules.** A node under the deployed rules generation; `repos` unchanged or narrower; routing unchanged; credentials block unchanged; **`--delete`, `--seed-repos` and `--remove-routing` are never admitted**. The node bounds runs that are already launching (`project_cmd.go`'s own long text), so a widening is refused for the same reason a delete is.
- **Red step.** `TestPolicyPublishManagedRefusesDelete` (`--delete` in a managed project with an agent marker reaches the gate and is refused by it, not by the marker) and `TestPolicyPublishManagedRefusesSeedRepos`; then `TestPolicyPublishManagedAdmitsANarrowerNode` with `AgentProceeded`.
- **Files (≤6).** `internal/cli/project_cmd.go`, a new `internal/cli/project_policy_managed.go`, and the tests.

## M4. `fugaro image refresh` under the gate, and the build bound (D1013). **SEC**

The one command in the set whose cost is not in a plan (reconciliation F16), and the only decision here that changes what an installation spends.

- **Rules.** Per build: the base digest is the release's (`--expect-digest` satisfied where given); the derived tag moves in the direction the skew rules allow; the build account is the project's own; **the count is within the bound M5 records**. The RTDB kill switch (an unreadable switch counts as ON) and the recorded amount are **re-read before each build**, not once per command.
- **Red step.** `TestImageRefreshManagedStopsAtTheRecordedBound` — a marker recording one build, a plan naming two: the first runs and the second is refused with the owner's command named. It fails today because the gate does not exist and the refresh is agent-refused first. Then `TestImageRefreshManagedUnreadableKillSwitchStops`, `TestImageRefreshManagedUnknownMarkerVersionRefuses`, and `TestImageRefreshManagedDefaultBoundIsOne` (a version-2 object with no recorded bound admits one build and refuses the second).
- **Files (≤8).** `internal/cli/image_refresh.go`, `internal/cli/image_refresh_steps.go`, a new `internal/cli/image_refresh_managed.go`, and the tests.
- **SEC**, and the task most likely to be split if the per-build re-read turns out to need a surface `image_refresh_steps.go` does not have.

## M5. The marker's recorded build bound (D1013). **SEC**

- **The shape.** `MarkerVersionBuilds` in `internal/infra/managed/marker.go`, carrying `max_image_builds` beside the amount and currency; written by the two owner's paths (`fugaro managed flag`, and the creating run of `setup --create-project --managed`); read through the **same** number-and-label proof as the rest of the object, by the **same** read (`read.go`), never a second reader (reconciliation F3).
- **Fail-closed, stated as a table the tests pin**: unknown version → refuse; value outside the checked range → refuse; object unreadable → refuse; **version 1 or 2 (no recorded bound) → the default of one, never "no bound"**.
- **Red step.** `TestManagedMarkerBuildsVersionIsRefusedByAnOlderReader` — a version this build does not know is refused rather than read as the older shape (the fail-closed direction), which fails today because the version does not exist. Then `TestManagedMarkerOlderVersionMeansTheDefaultBound`, `TestManagedFlagWritesTheRecordedBound` (hermetic: `internal/gcpfake`), and `TestManagedMarkerRecordedBoundRangeIsChecked`.
- **Files (≤6).** `internal/infra/managed/marker.go`, `internal/infra/managed/read.go`, `internal/cli/managed_flag.go`, the creating run's write site, and the tests.
- **Not in this task**: any question in `fugaro setup`. The amount is already a required question with no default (D64) and the bound is **not** a new question — a command that asks a person nothing else asks nothing here, and the default is one.

## M6. The audit record: write and read (D1014, D1025). **SEC**

- **What it writes.** `gs://fugaro-runs-<gcp-project>/fugaro/managed-audit/<utc-date>/<id>.json` with the fields spec §6 lists, **for a refusal as well as an admission**, with the **operator's** credentials, into the prefix only operators may write. **A failure to write the record fails the command.**
- **What it reads.** One parser, in core, so what the record holds and what a surface prints cannot drift.
- **Red step.** `TestManagedAuditRefusalIsStillRecorded` — a gate refusal leaves one object carrying `verdict: refused` and the rule that fired; it fails today because nothing writes. Then `TestManagedAuditPlanDigestIsTheJudgedBytes`, `TestManagedAuditWriteFailureFailsTheCommand`, and `TestManagedAuditIsWrittenUnderTheOperatorPrefix` (the object's key is under `fugaro/`, which is what a job account cannot write).
- **The delete list's generation is asserted, not assumed.** `delete_list_generation` is the field that answers "which version of the owner's list admitted this delete" (§6), so it carries the same weight as the plan digest and gets the same treatment: `TestManagedAuditRecordsTheDeleteListGeneration` — the record names the generation that was in force when a removal was admitted — and its negative, `TestManagedAuditRecordsNoDeleteListGenerationWhenNoRemovalWasJudged` (an apply with no removal carries an empty field rather than a zero that reads as "generation 0 was in force"). A record that cannot say which list admitted a delete is a record an owner cannot audit against the file they wrote, which is why the field is in §6's table at all.
- **Files (≤6).** `internal/managed/audit.go`, `internal/managed/audit_test.go`, the reader's file in core, and the small call the three command tasks add.
- **SEC.** The record is a control (D1022's digest), not a courtesy, and its write path is the operator's own.

## M7. `fugaro doctor` shows it, and `fugaro report` lists it (D1014)

- **The check.** One new file beside the existing `internal/cli/doctor*.go` checks: the last managed agent apply — command, verdict, plan digest, age — and the count of records in the last day.
- **The report section.** The reader lives in core, the section lives where the report lives (reconciliation F19). `internal/cli/api.go` is **reserved**: M7 adds no symbol there, and if the section cannot be reached without one, it is a **handoff** — M7 reports it and the report's own owner writes the section.
- **Red step.** `TestDoctorShowsTheLastManagedAgentApply` (a hermetic record present → the check prints its command and verdict; absent → the check says none was recorded, and does not fail a project that never ran one).
- **Files (≤5).** a new `internal/cli/doctor_managed_audit.go`, its test, the check's registration point, and the report handoff's note.
- **Not SEC**: it reads and prints.

## M8. `fugaro managed unflag PROJECT_ID` (D1020). **SEC**

- **What it does.** Removes **both** marks in one command — the object and the project label — says which half was already gone rather than failing on it, records the revocation in M6's audit family, and **does not remove the deployer fence** (it says so before it writes).
- **What it refuses.** A coding agent's session and a Fugaro run's own process, through the **same two checks** `flag` carries (reconciliation F4) — called, never restated; `--yes`, a prefix, a wildcard and a bulk form do not exist; the project's exact ID is typed.
- **Red step.** `TestManagedUnflagRefusesAnAgentSession` and `TestManagedUnflagRefusesARunProcess` (both fail today because the subcommand does not exist), then `TestManagedUnflagRemovesBothMarks` and `TestManagedUnflagConvergesAHalfRemovedProject` (the object gone and the label present, or the reverse — the state a manual removal leaves — converges instead of needing a second command).
- **Files (≤6).** a new `internal/cli/managed_unflag.go`, its test, and the read/write helpers it calls (M5's and `managed_flag.go`'s, reused).
- **SEC**, and the task the owner should review hardest after M4: it is a command that removes a control, and its own refusals are the reason it is safe.

## M9. Documentation, the handoff sentence and the release notes

- **This folder's status line** changes from "unanswered" to what the owner answered. **No decision's text is edited by this task** — a changed decision is a rewritten design, in its own pull request.
- **The handoff sentence** for the settings, rigor and accounts designs (§2), written as a note in this folder for their owners rather than into their files.
- **`docs/releases/v0.15.0.md`'s `### For operators`**: *"run `fugaro update`"* plus what it will ask. The one thing an owner of a managed project must know — that `fugaro managed flag` now records a build bound and an existing marker does not — belongs there, and a release that carries M5 and not M4's bound says nothing about a bound that cannot be reached.
- **The skill's paragraph** (`plugin/skills/setup/reference/managed.md`, which says *"never write that label yourself"*) is **reserved by the 2026-10-30 rulings**: M9 writes the paragraph as a **handoff** in this folder and does not edit the file.
- **Live checks.** None added. Check 68 (the deployer fence) stays the owner's, and M9 says in the release notes that the fence is still unmeasured.
- **Files (≤5).** `docs/managed-agent-converge/README.md`, the new handoff note, and — where the rulings allow it — the release notes' operator line.

## 5. Testing, and the two properties that need a test of their own

Beyond each task's named red step, two properties are cross-cutting and each gets a test that is nobody's task in particular:

- **The wiring property (spec §8.5).** *Every* command in the set reads the gate **before** it writes. `internal/managed/check.go` states in its own words why a type cannot enforce this: a caller that constructs a clean verdict by hand is lying about a read, and the wiring tests are what catch it. So each command task carries one test in that shape — `TestUpdateReadsTheGateBeforeAnyWrite` and its siblings — and the plan's review asks for them by name.
- **The narrowing property (D1026).** The gate can only narrow: a `Typed` class, a `noYes` stage and a step with no confirmation at all are each unaffected by a clean plan verdict. M0's `TestConvergeRefusesATypedClass` is the type-level half; each command task's test that a money-or-permanent step is still refused is the wiring half.

## 6. The recommended slot

**CLI 0.15.0**, beside the settings, rigor and accounts designs (all three approved for 0.15.0 on 2026-10-31) — and for one reason that is not scheduling: **D1018 makes those designs' `set` commands callers of this gate.** Landing them in one release is what lets the handoff sentence in §2 be written once, in one release note, instead of two releases each carrying half of a rule. The order inside 0.15.0 is §2's, and M0 first.

A later slot costs more than a later date:

- **The parent design's fence is still unmeasured** (Check 68). A release that ships this before an operator has run it ships a gate whose forged-mark half rests on an edge nobody has measured, and §5 of the reconciliation says so rather than assuming it. The owner may reasonably want the fence measured first; that is a decision about this plan's slot and not about its content.
- **The settings, rigor and accounts commands do not exist yet** (F17). This gate can land before them — M0–M8 build the gate and the four commands that exist today — but a release of the gate before them is a release whose `set` commands still refuse an agent, and the ruling's point is only half answered until they land.
- **Nothing here blocks on a console, mobile, website or plugin release.** The console gains nothing (D1019), the report section is an `ee`-side handoff (M7), and the skill paragraph is reserved (M9). A release that shipped the CLI alone would leave a managed project's owner able to read the audit only from `doctor` until the report's owner lands the section — which is a smaller gap than it sounds, because `doctor` is the surface an owner already runs.
