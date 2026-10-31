# `fugaro rigor`: implementation plan

**Design approved; the tasks here queue for CLI 0.15.0 (and console 0.117.0, which is not this
plan's — D836).** Every decision in this design's block is answered
([reconciliation.md](reconciliation.md) §3): **D830 and D831** by the owner's SD4 ruling of
2026-10-31, **D832–D839 on 2026-10-31, all as recommended**. The one decision the plan **cites** and
does not own — **D854**, the settings design's, whether the `Layer` stage records the generation it
read at the seed — is **answered by the owner on 2026-10-31: record the generation**, and R1 is
written to that answer, with the within-run guard it also carries unconditional. **D900** — whether
the setup wizard asks the project's rigor mode explicitly — was added to this design on 2026-10-31
and is **answered by the owner the same day: it does**, and its task is **W**, at the end of the plan
and after R0–R3 and S5. The tasks are written to the answers above. Each task touches at most 12
files, names its tests, starts with a red step and ends at the gates below.

## Order, and what this plan does not own

- **External dependency E1: `internal/yamledit`, the shared comment-preserving YAML editor.** The
  package, its generic operations and the rulings the two designs share are named **once**, in the
  [README](README.md) §"the joint order, and the rulings the rigor and settings designs share" —
  this plan does not restate them. **One task owns it — the editor task — and it lands first in the
  joint order**; rigor does not own it, does not fork it, and is not "the second caller that keeps a
  copy": **R2 is a thin caller** (`EditRigor`, spec §9) and **R4 and R5 call the shared write helper
  settings S5 lands** — the joint order's position 5 — **not** an `internal/cli/rigor_write.go` this
  plan does not have. **E1 has not landed**: there is no `internal/yamledit` on `main` today
  (verified 2026-10-31), and the path this folder once proposed for the editor,
  `internal/localcfg/edit.go`, does not exist either. The plan therefore names the **package and the
  task that lands it**, never an existing file it cannot point at; the only precedent that exists is
  `setGCPProjectLine` (`internal/cli/init_fugaroyaml.go`) — one scalar on one line, which the splice
  generalises rather than repeats. **Not** a merge of the works' plans: they stay separate plans with
  separate owners, and the README's joint order is what keeps them from colliding.
- **External dependency E2 (the mode floors, `docs/rigor-modes` plan T1.5).** `show`'s floors
  section prints the review floor (F10) and one line saying the mode floors are not built (F9). The
  task that ships T1.5 adds the sensitive-path set to that section; until then no surface here may
  claim to display a floor that does not exist. Not a blocker.
- **The console's project view** (plan P6, not built) is not an external dependency of anything:
  D836 puts no console task in this plan.
- **The `rigor` command group is shared, and nobody may assume it exists.** `internal/cli/rigor.go`
  — the group that holds `show`, `set`, `unset` and (SD10) the `floor` subgroup — is created by
  **whichever of this design's R3 and the settings design's S7 lands first**, and the second adds its
  own subcommand to what the first created. The settings design states the same sentence; neither
  plan may write "the group exists by then", and a task that touches the file says which of the two
  it expects.
- **Files other work owns:** registering the command in `internal/cli/root.go`, the generated
  `website/content/cli-reference.md`, `plugin/**` and `console/**` are each owned by another open
  work stream at the time of writing. R3 and R6 name them, and the task that touches them waits for
  the owner's go for those files.

**The `Needs` column is a *code* dependency: what must be merged for that task to build, test and be
reviewed on its own.** Three of its entries reach into another design — E1, the editor task's
`internal/yamledit`; S5, the settings task that lands the shared write helper
(`internal/cli/settings_write.go`); and **R3 or S7**, the two tasks that can create the `rigor`
command group `internal/cli/rigor.go` — and each is a position in the [README](README.md)'s joint
order rather than a task in this plan. R3 is this plan's; S7 is the settings design's.

| Level | Tasks | Needs (code) |
| --- | --- | --- |
| 0 | R0 | nothing |
| 1 | R1, R2 | R0 for R1; E1 for R2 (the editor task's `internal/yamledit`) |
| 2 | R3 | R0 |
| 3 | R4 | R0, R1, R2, S5, and R3 or S7 (whichever created `internal/cli/rigor.go`) |
| 4 | R5 | R4 (and the same code needs: R0, R1, R2, S5, and R3 or S7) |
| 5 | R6 | R1–R5 |
| 6 | R7, R8 | nothing (R7 lands nothing before the owner answers) |
| 7 | W | R0, R1, R2, R3, S5, and R4's hotfix refusal (D834) |

**R4 and R5 need R1 among their code needs — and that one is what makes the HARD RULE hold
mechanically rather than by promise.** The row is the whole list (R0, R1, R2, S5, and R3 or S7 —
`internal/cli/rigor.go`, the group both R4 and R5 extend and both R3 and the settings design's S7
can create, so the row names either and the task waits for whichever landed first); the three
bullets below are about the entry the HARD RULE turns on, which is not the only one the row names:
they say nothing about R0, R2, S5 or R3/S7, and nothing here may be read as shortening the row. A
`set` that
lands without the publish stage leaves the owner's next step as `fugaro config publish FILE` — the
second command the HARD RULE forbids (F18) — so no task here may write the layer before the task
that publishes it has landed, and the table says so in the same column it says everything else:

- **R1's publish half is what makes `set`'s last line true.** R4 ends by printing `next: fugaro
  update` (spec §3 step 7); that sentence is only honest once `update` publishes the layer.
- **R1's seeding half is what makes `set` usable on a real installation.** On an installation whose
  layer predates this release the file is absent while a layer is published, so without the seed the
  first `rigor set` is D830's refusal and the owner's next step is a second command after all. R1's
  task carries both halves, and the release notes (R6's `### For operators`) say the one command now
  seeds as well as publishes.
- **R3 is the exception, and deliberately so.** `show` writes nothing, so it needs neither the
  publish stage nor the seed: it reports `unpublished` (README, the shared state word) instead of
  fixing it, which is exactly the reading an owner needs *before* the stage has run.

**R7 and R8 are follow-ups this design found and does not fix** (reconciliation §4): the missing
hotfix gate (F13) and the loader that accepts two area rules with one glob (F14). Neither blocks
R0–R6; both target 0.15.0 with them. R7's first step is a question to the owner and it lands nothing
before the answer, so its level is the last row of the table and not a gate on anything. Whichever of
R7 and R8 lands in the same release as R0–R6 has its user-visible half written into that release's
notes by R6; a release that carries neither notes neither.

**W is not a follow-up: it is D900's task, and it is the one task here that is not a `rigor`
subcommand.** The owner's ruling of 2026-10-31 is that the setup wizard asks the project's rigor mode
explicitly, and W is that question — a row of the wizard's own question table, the `--rigor <mode>`
flag beside it, and the answer written into the one local layer file by R1's stage and published by
the same run (the HARD RULE, F18). Its letter is the wizard's, not this family's, because the files it
changes are the wizard's and the setup skill's; the tasks it needs are R0, R1, R2, R3 and the settings
design's S5, plus R4's refusal of `hotfix` as a default (D834), and it is the last row of the table
for that reason. A release that carries it is the one where `fugaro setup` asks the question, and R6's
`### For operators` says so in the same notes.

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`. Never the whole
`internal/cli` package.

## R0. The one local layer file, and the doors that name it (D830, answered)

Without it, "the file the owner publishes from" has no name, and `set` cannot be written (F3). The
answer is a convention, not a key: the path is the project's own name under the config home's
`projects/` directory, beside the project's config, so **no `--layer` flag and no `project_layer:`
key exist** — there is nothing to record **of the path** and nothing to resolve. The *generation*
record is a different thing and it does exist (D854, answered 2026-10-31: record the generation):
it is R1's, and this task adds its **field**, because this is the task that already edits
`internal/localcfg`.

- **Where the record lives, and this task declares it: `localcfg.Config`'s `layer_generation`.** The
  generation a publishing stage last wrote from is recorded on the local project config's own struct
  (`internal/localcfg/localcfg.go`): **`layer_generation`** for R1's layer stage — declared by this
  task, in the file it already edits — and **`policy_generation`** for the settings design's policy
  stage, which its S3 declares in the same struct. One record per stage that publishes, both
  `omitempty`, neither a second home: a local file's own record, never wire, and an installation
  that carries neither decodes exactly as it does today (D854, answered; the settings design's
  `reconciliation.md` §4 names both fields). R1 writes and reads `layer_generation`, and the
  explicit door writes it too: `fugaro config publish` publishes through the same `publishLayer`
  (D831), and `fugaro rigor show` must not read a `config publish` as a stale copy the moment the
  owner edits the file afterwards — so **every** door that writes the file from a published object,
  or publishes it, records the generation it wrote at.
- **Files (7):** `internal/localcfg/localcfg.go` (`LayerFile` — the path, derived from the project
  name; the same directory helper the project config uses — and `layer_generation` above),
  `internal/localcfg/localcfg_test.go`,
  `internal/cli/config_publish.go` (the FILE argument becomes optional and defaults to that path,
  and the publish records the generation it wrote at),
  `internal/cli/config_publish_test.go`, `internal/cli/config_show.go` (`config layer` prints the
  local path and whether the published object is `unpublished`, with sha256s),
  `internal/cli/doctor_layer.go` and `internal/cli/doctor_layer_test.go`. `docs/project-layer.md`
  (§6 and §7) is R6's, so that this task's diff stays code.
- **What R0 changes in `doctor_layer.go`, which already exists — 9,205 bytes on `main` at
  `7efc78e0`, so this is an addition and not a rewrite.** It **adds one function**,
  `localLayerFileCheck(lc *localcfg.Config, rf resolvedFile) []doctorCheck`, and **one check ID**,
  `local-layer-file`; it appends that function's result to
  `doctorLayerChecks`'s output and touches nothing else. The existing checks and their IDs stay
  exactly as they are: `project-layer` (the layer that applies, or that none does), `project-layer-drift`
  (the repository's *last run* against the published layer — **a different comparison, not the state
  word below: its ID stays**), `project-layer-copy` (the repository's own copy), `image-config-*` and
  `imageLeafKeys`' attribution. The new check reports §1's path, whether the file exists, and — when
  it does — its sha256 beside the published object's, saying **`unpublished`** when they differ and
  adding `fugaro update` as the way to publish it, the same word and the same sentence `config layer`
  and `show` print (one word, one sha256 comparison, three doors — README, the shared rulings) —
  **and which cause it is**: the same record comparison `show` makes (the local project config's
  `layer_generation`, D854 answered), so a **local edit** reads as one to publish and a **stale copy
  from another machine** reads as one to re-seed. This task declares the record the check reads, so
  the doors that print the word agree on why. **It
  needs no bucket round trip**, which is why it is appended on **every** path `doctorLayerChecks`
  returns from — the four early returns (`internal/cli/doctor_layer.go`: no layer resolved; an
  origin that could not be read or was rewritten; `task.Slug` failing on the origin's repository; and
  `blobx.Open` failing on the bucket) that today skip the rest — because the published side it
  compares against is the already-resolved `rf.Layer.Layer` (its `SHA256` — so
  `rf.Layer.Layer.SHA256`, not `rf.Layer.SHA256` — and its `Note` when it came from the offline
  cache), never a fresh read. A check that vanished exactly when the bucket was unreachable would be
  the one line the owner needs in the state that breaks `set`.
- **Red step:** `TestConfigPublishWithNoFilePublishesTheLocalLayer` fails while `publish` takes an
  exact-1 argument and no path is derived.
- **Tests:** `TestLayerFileIsTheProjectNameUnderProjects` (the path, and that a project name that is
  not a single path element is refused), `TestConfigPublishWithNoFilePublishesTheLocalLayer`,
  `TestConfigPublishRefusesWhenTheLocalFileDoesNotExist` (the message names `fugaro update`, not a
  flag), `TestConfigPublishRecordsTheGenerationItWroteAt` (the explicit door writes the record, so
  an edit made after a `config publish` reads as a local edit and not as another machine's copy),
  `TestConfigLayerPrintsTheLocalPathAndItsUnpublishedState`,
  `TestDoctorReportsALocalLayerFileThatIsUnpublished` (the new `local-layer-file` check's ID, its
  severity, and that it says `unpublished` — the shared word — with the existing `project-layer*`
  checks asserted unchanged in the same test so the addition is shown not to have moved them,
  `project-layer-drift`'s ID included).
- **Hermetic:** `t.TempDir()` config home, `internal/gcpfake` for the bucket, `internal/testutil`.

## R1. `fugaro update` seeds and publishes the local layer file (D831, answered). **SEC**

The stage writes the object every repository of the project resolves against: it changes build and
test commands for everyone, and it is the key whose change the executable-key gate exists for. Its
**seeding** half writes only a local file, and it is what makes R0's convention usable on an
installation that has never had one.

- **The stage runs before `Checkout`, and `Checkout` needs it.** The canonical order
  (`internal/initflow/stage.go`) does **not** end at `Policy`: `Checkout` and `Compute` follow it,
  and `Checkout` is what refreshes the images for the checkout's repositories. A layer that changes
  `image.*` or `commands.*` therefore has to be published **before** that refresh, or the run builds
  from the layer the owner just replaced and the next `update` is needed to build from the right one
  — the second command the HARD RULE forbids, one step to the side. So this task gives `Checkout`
  `Layer` in its `needs` beside the `Repository` it already has, and appends the new stage **after
  `e.policy` and before `newCheckoutStage`** (`internal/cli/init_stages.go`: the same function
  appends `e.policy` and then the checkout stage, so the new append goes between them).
- **Files (6):** `internal/initflow/stage.go` (three edits, all named here),
  `internal/initflow/loop_test.go`, `internal/cli/init_layer_stage.go` (new: the seed, the read,
  the diff, the confirmation, and the calls into `publishLayer` and `fanOutLayer`, which already exist in
  `config_publish.go`),
  `internal/cli/init_layer_stage_test.go`
  (new), **`internal/cli/init_stages.go`** (registering the stage — the stages are appended in
  `initStages`, where `newRulesStage`, `newPolicyMigrateStage` and `newCheckoutStage` already are;
  **not `internal/cli/init.go`**, which holds the command and the engine but appends no stage), and
  `internal/cli/config_publish.go` (the shared publish path, so the two doors cannot disagree).
  `docs/project-layer.md` (§7 and §8) is R6's, the same as R0's, so this task's diff stays code.
- **The three edits in `internal/initflow/stage.go`, and they are all required.** A stage name the
  package does not know is not a stage: `internal/initflow` holds its names as **constants** in one
  block (`Preflight`, `Project`, …, `Policy`, `Checkout`, `Compute`) and its canonical order as the
  **`order` slot slice** below them, and the loop walks `order`. So this task **adds the constant**
  `Layer = "layer"` to that block, beside `Policy` and `Checkout`, and **adds the slot** to `order`:
  `{name: Layer, needs: []string{Repository}}`, placed between the `Policy` slot and the `Checkout`
  slot — the position D831 chose. Its `needs` name the installation's own stage, `Repository`, the
  way `Checkout`'s slot does. `needs` is not a second ordering engine: the loop walks `order`, so an
  entry gates nothing at run time — but it is **checked**, not merely documentary, because
  `TestStageOrderMatchesDependencies` (below) requires every entry to name a stage that comes earlier
  in `order` and fails on one that does not. So no entry here may be read as a new dependency of the
  layer on the policy node, and no entry may name a stage that does not precede it either. Third,
  `Checkout`'s existing slot gains `Layer` in its `needs`, which is what the stage-order test below
  reads as "the layer is published before the images are refreshed".
- **`internal/initflow/loop_test.go` changes with them.** `TestStageOrderMatchesDependencies` pins
  the stage count (fifteen) **and** the exact order
  (`… Repository, Rules, Policy, Checkout, Compute`), and checks every `needs` entry against it, so
  the new constant and slot move it to sixteen with `Layer` between `Policy` and `Checkout`, and
  `Checkout`'s gained need is the second assertion it already makes. **There is no
  `internal/initflow/stage_test.go`**: the package's tests are `loop_test.go`.
- **Two guards, and the first is unconditional.** The stage refuses to publish when the published
  object's generation moved **since its own read** — someone else published while this update ran —
  rather than overwriting an object this update never read. That guard needs **no record and no
  first-run special case**: the write carries the generation the read returned (`blobx.Read`'s
  generation into `blobx.ReplaceIf`), so it holds on the first `update` of an installation exactly as
  it holds on the hundredth. This is `TestUpdateLayerStageRefusesWhenTheGenerationMovedSinceTheSeed`
  (below), and it is written as the unconditional test it is.
- **The second guard is across runs, and D854 is answered: the generation is recorded.** A publish
  that lands **after** the run that seeded the file is invisible to the guard above — a generation
  this run never read is one it cannot compare with — which is the case the owner's ruling answers.
  So the stage **records** the generation the local layer file's bytes were last written from, in
  `layer_generation` on the local project config (R0 declares the field), written when it seeds and
  when it publishes, and **not** kept in the layer cache, which is the cloud's copy and may be
  dropped at any time. It is written through the write the converge already gives that file —
  `writeConfig` on the engine, which holds `lc`, `spec`, `path` and `old`, the way the credentials
  stage calls it (`internal/cli/init_credentials.go`) — so the field gets the diff, the confirmation
  and the backup every other config write gets, and the project config gains no second writer. Every
  later `update` compares that record with the generation it reads: equal or older, it publishes as
  usual and records what it read; **newer** — someone else published since this machine last wrote —
  it refuses (`TestUpdateLayerStageRefusesWhenThePublishedGenerationIsNewer`, below), shows the
  **three-way state** (this machine's file, the generation it was written from, the object published
  now) and offers to **re-seed** the local file from the published one under its own confirmation
  and with a backup — the seeding half this same stage already carries, aimed at a file that is
  there instead of one that is not. A refusal that showed nothing would leave the owner to guess
  which of the two files to keep.
- **First run, nothing recorded: this stage adopts.** An installation that carries no
  `layer_generation` — every installation the release reaches — is not refused and not re-seeded: the
  stage publishes as it would, then **records the generation it read** and says that it adopted it
  (`TestUpdateLayerStageAdoptsTheGenerationWhenNothingIsRecorded`, below; the settings design's
  `reconciliation.md` §4 gives its policy stage the same first-run behaviour, so the two stages
  answer the same question the same way). Refusing once to force a record would leave the first
  `update` after the release incomplete, which is the HARD RULE's own objection.
- **The record is what keeps `unpublished` from being one word for two states.** The wording `show`
  (R3) and doctor's `local-layer-file` check (R0) print must say which reading it is, because the
  recorded generation is the only thing that tells them apart: a **local edit** (the bytes differ and
  the recorded generation is still the published one — the owner's change, to publish) or a **stale
  copy from another machine** (the published generation has moved past the recorded one — someone
  else's publish, which this file would revert). The two need different remedies, so one word for
  both would send the owner to the wrong one.
- **Red step:** `TestUpdateSeedsTheLocalLayerFromThePublishedOne` fails while no stage writes a local
  layer file.
- **Tests:** `TestStageOrderMatchesDependencies` (`internal/initflow/loop_test.go`: sixteen stages,
  with `Layer` between `Policy` and `Checkout` in the order it pins, and `Checkout`'s `needs`
  carrying `Layer` — the slot registration the loop reads),
  `TestUpdateSeedsTheLocalLayerFromThePublishedOne` (no local file, a published layer: the
  file appears with the published bytes, mode 0600, and the update says what it wrote),
  `TestUpdateSeedWritesNothingWhenTheLocalFileExists` (an owner's file is never overwritten by the
  seed, even when it differs — that is the publish half's job, after a diff),
  `TestUpdateSeedWritesNothingWhenNoLayerIsPublished` (the first `update` of a project that has never
  published one leaves no empty file behind),
  `TestUpdatePublishesTheProjectLayerWhenItIsUnpublished` (the diff is printed, the object is
  written once, the fan-out runs),
  `TestUpdateLayerStageSkipsWithNoLocalFileAndNoPublishedLayer` (the one case where there is nothing
  to seed from and nothing to publish, said in a sentence),
  `TestUpdateLayerStageRefusesAFileThatDoesNotParse`,
  `TestUpdateLayerStageRefusesExecutableChangesWithoutTheFlag` (the same banner and refusal
  `config publish` gives), `TestUpdateLayerStageAppliesNothingInAnAgentSessionEvenWithYes`,
  `TestUpdateLayerStageRefusesWhenTheGenerationMovedSinceTheSeed` (the unconditional within-run
  guard above: a publish that landed between the seed and the write is a refusal, and the owner's
  file is left as it is — the write carries the generation the read returned, so no record is
  consulted and none is needed, and it holds on a first run as on any other),
  `TestUpdateLayerStageRefusesWhenThePublishedGenerationIsNewer` (the across-runs guard: the record
  says the local file was written from an older generation than the published object's, so the stage
  refuses, prints the three-way state and offers the re-seed — and does not publish),
  `TestUpdateLayerStageAdoptsTheGenerationWhenNothingIsRecorded` (a local file and a published layer
  with no `layer_generation` anywhere: the stage publishes what it would have published, records the
  generation it read, and says it adopted it),
  `TestInstallationAtThePreviousVersionConverges` (an installation whose local layer predates this
  release ends the update with it published — the HARD RULE's own test).
- **Hermetic:** `internal/gcpfake` for the runs bucket, `t.TempDir()` for the state and the layer
  file. No apply, no deploy, no cloud call.

## R2. `EditRigor`, the rigor ops on the shared editor (D830). **SEC**

The layer decides what every repository of the project runs, and a lossy rewrite is a change nobody
reviewed. The write must keep comments, key order and every key it does not touch — and it must be
the **same** splice the settings work and the `models` plan use, because three documents and one
splice is a design, while two editors is a bug waiting for one of them to be fixed.

- **R2 adds a caller, not an editor.** `internal/yamledit` (E1, README's joint order position 1)
  owns the generic half: locate the node in the tree, splice the bytes, keep every other byte. R2
  adds a **thin rigor layer** over it — `EditRigor(data []byte, at []string, ops ...RigorOp)
  ([]byte, error)` — which maps this work's vocabulary onto the package's generic operations and
  hands it the anchor. It creates **no** editor: not `internal/config/layeredit.go`, not a copy of
  the locator under a name of its own (spec §9).
- **Files (5):** `internal/rigor/edit.go` (new: `EditRigor` and the `RigorOp` vocabulary), its test
  `internal/rigor/edit_test.go` (new), and three fixtures in `internal/rigor/testdata/` (new):
  `hand-written-layer.yaml`, `hand-written-layer-comments.yaml` and `hand-written-rigor-areas.yaml`
  (placeholder names only). The fixtures live here rather than with the editor because they are
  *layer* documents — the `defaults.rigor` and `rigor:` anchors are this work's, while
  `internal/yamledit`'s own fixtures are about documents, not about fugaro's keys.
- **The interface, stated once here.** `at` is the path to the block: `{"defaults", "rigor"}` for the
  project layer, `{"rigor"}` for a repository's file (R5). The ops are `SetDefault(mode)`,
  `RemoveDefault()`, `SetArea(match, name, mode)`, `RenameArea(match, name)` and `RemoveArea(match)`.
  **An area argument list reads `match` first, in all three ops, because the glob is the key**: a
  rule is addressed by its glob (spec §5), while `--name` is what may be *set* on it and is required
  only when the glob is new. `name` is therefore never the subject of an op and never first; a
  signature that put it first (`SetArea(name, match, mode)`) reads as "address the rule called
  `name`", which is the bug this ordering prevents — and with `RenameArea` writing them the other way
  round, the two would disagree with each other, which is how a caller comes to swap them.
  `EditRigor` locates, maps and splices; it never re-emits the document, and a refusal returns the
  input untouched so a caller can compare bytes to know nothing was written.
- **Two checks, and neither stands in for the other.** *Structural validity* is
  `config.ParseProjectLayer` on the output, against the same anchor the publisher uses: a document
  that parses is a layer the cloud accepts, and it is what the red step's second half shows a struct
  round trip still passing. *Preservation* is a **byte** comparison of the output against the input
  around the edit: every comment, key order, quoting style, blank line and unrelated key kept exactly
  as it was. A strict YAML decoder cannot answer the second, because comments are parse artifacts —
  discarded before any struct is decoded — so a rewrite that drops every comment passes the first
  check and fails the second. The tests assert the two separately, and
  `TestEditRigorRefusesWhatParseProjectLayerRefuses` is the first. **The generic half of the byte
  comparison is the editor task's test**, not this one: R2 asserts that it *called* the editor with
  the right anchor and the right operation, and that what came back still parses.
- **Red step:** `TestEditRigorMapsEachOpOntoTheEditor` fails while `EditRigor` does not exist.
- **Tests:** `TestEditRigorMapsEachOpOntoTheEditor` (each of the five ops, against a stub or the real
  package: the anchor it is given and the generic operation it becomes),
  `TestEditRigorKeepsCommentsAndOrder` (the same fixture through `EditRigor`: the byte comparison
  that shows the caller preserved what the editor preserved),
  `TestEditRigorDecodesToTheIntendedChangeOnly` (the decoded old layer plus the op equals the decoded
  new layer — the structural half), `TestEditRigorRefusesWhatParseProjectLayerRefuses`,
  `TestEditRigorAddsDefaultAfterTheKeysAlreadyThere`,
  `TestEditRigorAtTheRepositoryAnchorTouchesOnlyItsOwnBlock` (a `fugaro.yaml` fixture through the
  `{"rigor"}` anchor: its other keys and comments survive, and a `defaults.rigor` anchor on the same
  bytes is a refusal, not a silent edit of the wrong block).

## R3. `fugaro rigor show` (D835, D839)

- **Files (6):** `internal/cli/rigor.go` (the command group, whose subcommand list is built so the
  **`floor` subgroup SD10 rules for** is added by one entry and not by a reshape — the group exists
  to hold more than `show`, `set` and `unset`), `internal/cli/rigor_show.go` (new),
  `internal/cli/rigor_show_test.go` (new), `schemas/rigor-show.schema.json` (new),
  `schemas/schemas_test.go` (the new schema loads and the command's output validates),
  `internal/cli/root.go` (registration; owned elsewhere, see "Order"). `docs/project-layer.md` (§7's
  command table gains a row) is **R6's**, the same as R0's and R1's, so this task's diff stays code.
- **Who creates `internal/cli/rigor.go`.** **Whichever of R3 and the settings design's S7 lands
  first** creates it; the second adds its own subcommand to the group the first built (README, and
  the settings design states the same sentence). R3 therefore does **not** assume the file is there,
  and S7 must not either: the task's first commit either creates the group or adds to it, and says
  which in the message.
- **The resolution is `rigor.Resolve` and nothing else.** `show` calls
  `rigor.Resolve` (`internal/rigor/resolve.go`) per repository and prints what it returns —
  `internal/rigor` is the one place the modes are ordered and the precedence settled (spec §2), and
  the settings row calls the same function. R3 adds **no** ordering, no precedence table and no
  second answer to "what mode does this repository get": a `show` that agreed with a run only by
  construction is the bug this sentence prevents.
- **Red step:** `TestRigorShowPrintsTheResolvedModeAndItsLayer` fails while the command does not
  exist.
- **Tests:** `TestRigorShowPrintsTheResolvedModeAndItsLayer` (the four sections of spec §2, in
  order), `TestRigorShowNamesTheAreaRuleThatMatchedAPath` (`--path`, and the strictest of two),
  `TestRigorShowUnknownForARepositoryWithNoCheckout` (and that the row, the sentence and the `source`
  string are §2.3's, word for word — `unknown (no checkout on this machine)`, the wording both
  designs share),
  `TestRigorShowPrintsTheReviewFloorAndSaysTheModeFloorsAreNotBuilt` (and names **`fugaro rigor
  floor`** as the command that changes a floor, SD10),
  `TestRigorShowReportsTheUnpublishedLocalLayer` (the state word: `unpublished`, with both sha256s,
  the same comparison `config layer` and doctor make, **and the cause** — the recorded
  `layer_generation` against the published generation: a local edit while the two are equal, a stale
  copy from another machine once the published one has moved past it),
  `TestRigorShowResolvesThroughRigorResolve` (a mode `show` prints is `rigor.Resolve`'s answer for
  the same inputs — the resolver is called, not re-implemented),
  `TestRigorShowJSONLayerNamesWhatWasRead` (spec §2's `layer` member: `published` and `local`
  present with their sha256s, `source` one of the two words, `note` carrying the cache read's own
  sentence and not only the enum, `recorded` carrying the local project config's `layer_generation`
  and **absent** when nothing is recorded, `unpublished` true only when both are there and differ
  (**both causes covered**: a local edit, `recorded` equal to `published`, and a stale copy,
  `recorded` older — the pair a consumer reads to choose between publishing and re-seeding, spec
  §2's `layer` table) — and that `published` and `local` are **absent**, never `null`, for a project
  that has published nothing and for a machine with no local file), `TestRigorShowJSONMatchesSchema`,
  `TestRigorShowJSONNeverSaysRequestOrReached`, `TestRigorShowWritesNothing`,
  `TestRigorGroupHasNoFloorFlag` (the family's `set`/`unset`/`show` take no flag that writes a floor:
  SD10's line, asserted rather than remembered).
- **Hermetic:** `t.TempDir()` config home, a fake checkout made with `git init` in the temp dir, a
  seeded layer cache or `internal/gcpfake` for the published object.

## R4. `fugaro rigor set` and `unset` — the project default and its areas (D832, D834, D837, D838). **SEC**

It writes the key that decides how much review a whole project gets, and `unset` removes one.

- **Files (3):** `internal/cli/rigor_set.go` (new: both `set` and `unset`, which share one write
  path), `internal/cli/rigor_set_test.go` (new), and `internal/cli/rigor.go` (the group — created by
  R3 or by the settings design's S7, whichever landed first; R4 adds its subcommands to it).
- **Neither half of the write is rigor's own.** The **editing** is `EditRigor` (R2), which is a
  caller of `internal/yamledit`; the **writing** — the diff, the confirmation, the agent-session
  refusal, the backup and the atomic write — is the **shared write helper settings S5 lands** in
  `internal/cli/settings_write.go` (README, the joint order's position 5), which R4 calls. **There is
  no `internal/cli/rigor_write.go` in this plan**: a second copy of the confirmation and the refusal
  is exactly how `rigor set` and `config set` would come to ask differently for the same kind of
  change, and D830's whole point is that one file has one way in.
- **Red step:** `TestRigorSetRefusesHotfixInEveryForm` fails while the subcommand does not exist.
- **Tests:** `TestRigorSetWritesTheProjectDefault`, `TestRigorSetRefusesAVocabularyNearMiss`,
  `TestRigorSetRefusesHotfixInEveryForm`, `TestRigorSetLoweringAsksForTheTypedProjectName` (and
  `--yes` does not stand in), `TestRigorSetRaisingAsksYesNo`,
  `TestRigorSetRefusesWithNoLocalFileWhileALayerIsPublished` (the message's remedy is **`fugaro
  update`**, which is the one documented command; it names no flag and no second command),
  `TestRigorSetCreatesTheFileWhenNoLayerIsPublished` (the created document carries the local
  config's anchors **and** `defaults.rigor.default: <mode>` — not three bare keys — and it parses
  through `config.ParseProjectLayer` against the same anchor the publisher uses, spec §3 step 4),
  `TestRigorSetRefusesInAnAgentSessionEvenWithYes` (in that session the command **prints the diff and
  the command it would have run and writes nothing**, `--yes` included — SD9's wording, the same
  F15 pins for `update`'s own write), `TestRigorSetAreaAddsARuleAndRequiresAName`,
  `TestRigorSetAreaReplacesTheModeOfAnExistingGlob`, `TestRigorSetAreaRefusesATwinGlob`,
  `TestRigorSetAreaRefusesAGlobOutsideTheRepository`, `TestRigorUnsetRemovesTheKeyRatherThanWritingPro`,
  `TestRigorUnsetOfNothingIsAnExitZeroSentence`, `TestRigorSetKeepsTheRestOfTheFile`,
  `TestRigorSetPrintsOnlyFugaroUpdateNext`, `TestRigorSetWritesNothingInTheCloud`.

## R5. `fugaro rigor set --repo R` (D833)

It edits a repository's own file in its working tree, which the user reviews and commits; it writes
no layer and nothing in the cloud.

- **The write path is R2's editor, pointed at this target's anchor** (spec §9: one editor, two
  anchors — R5 does not re-implement the locator, and the smallest diff is what the splice gives). It
  calls `EditRigor` with `{"rigor"}`: the scalar `rigor.default`, or a sequence item under
  `rigor.areas` for `--area`, with every other byte of the user's file untouched. `setGCPProjectLine`
  (`internal/cli/init_fugaroyaml.go`) stays as the precedent the splice follows, not as a second
  strategy.
- **Files (3):** `internal/cli/rigor_repo.go` (new), `internal/cli/rigor_repo_test.go` (new),
  `internal/cli/rigor.go` (the group: created by R3 or by the settings design's S7, whichever landed
  first, and already extended by R4). The diff, the confirmation and the backup are the shared write
  helper's (`internal/cli/settings_write.go`, settings S5) as in R4 — R5 writes a different document,
  not a different way.
- **A lowering here asks for the typed project name too, and a coding agent's session cannot answer
  it** (spec §4): the confirmation is a sentence only a person can complete, so in that session the
  command **prints the diff and the command it would have run and writes nothing** — SD9's wording,
  the same one F15 pins for the other writes. It is not asked to answer, and it does not edit the
  working tree "to be helpful": the diff it prints is the whole of what it may produce. Raising the
  mode is the ordinary y/N and runs in that session like any other edit.
- **Red step:** `TestRigorRepoEditsOnlyTheDefaultLine` fails while the subcommand does not exist.
- **Tests:** `TestRigorRepoEditsOnlyTheDefaultLine`, `TestRigorRepoInsertsTheRigorBlockWhenAbsent`,
  `TestRigorRepoRefusesHotfix`, `TestRigorRepoLoweringAsksForTheTypedProjectName`,
  `TestRigorRepoLoweringInAnAgentSessionPrintsTheDiffAndWritesNothing` (the SD9 half, asserted
  separately from the refusal: the tree is byte-identical afterwards),
  `TestRigorRepoNeverCommits`, `TestRigorRepoRefusesWithoutACheckout`,
  `TestRigorRepoRaisingRunsInAnAgentSession` (the other half: an ordinary edit is not refused there),
  `TestRigorRepoAreaWritesTheRepositorysOwnRule`.

## R6. Documentation, skill and release notes

- **Files (6):** `docs/project-layer.md` (§2's rigor paragraph and §7's command table lead with the
  commands; the hand edit stays as the reference; the floor sentence names **`fugaro rigor floor`**
  rather than a flag on `set` (SD10), and the table gains `fugaro rigor floor`'s row only when that
  command ships — nothing here may document a command that does not exist),
  `docs/rigor-command/README.md` (the status line — **approved, tasks queue for CLI 0.15.0 (and
  console 0.117.0)** as it reads now, which this task moves to *shipped* in the release that lands
  R0–R5, so the folder never claims a version that is not out — and the "joint order and shared
  rulings" section, whose **order and rulings are shared with the settings design**: each folder
  states them in its own words and points at the other, so this task changes the order or a ruling
  here and in the settings design's own statement of it in the same release, and leaves the wording
  of the other alone), `plugin/skills/working/SKILL.md` (owned
  elsewhere, see "Order"), `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`" and
  what it will ask — including that the stage **seeds** the local layer file when it is absent and
  otherwise **publishes** it when it is **`unpublished`**, with the diff and its confirmation, so the
  operator knows the one command now does both — and, in a release that also lands **W**, that
  `fugaro setup` asks the project's rigor mode once and `fugaro update` never asks it),
  `website/data/release-summaries.yaml`, the
  generated `website/content/cli-reference.md` (regenerated, not hand-edited).
- **Red step:** the plugin's command test fails while the skill names `fugaro rigor set` before R4 is
  merged; it turns green on R4's branch.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs`, the plugin tests, `npm run check` in
  `website/`.

## R7. The hotfix gate D12 names, which does not exist (F13). **SEC**

A follow-up this design found and does not fix. D12 requires `fugaro run --mode hotfix` to be gated
on the base branch's configuration (`rigor.hotfix`), and `internal/cli/followup.go` repeats that
sentence when it refuses a follow-up's hotfix — but no such key exists: `schemas/fugaro.schema.json`'s
`rigor` block carries `default` and `areas` only, `internal/config/scope.go` has two rigor rows and
neither is it, and no code reads one.

- **What `hotfix` is today, plainly.** It is a **mode value**, and that is all: it resolves like
  `pro` (its levels are Pro's set and its bands are Pro's, bar the security override below), it is
  **never a default** in any layer — no built-in, no document, and this design's `set` refuses to
  write it (D834) — and it is **not writable by `fugaro rigor set`** in any of its four forms. What
  does not exist is the *gate*: the base-branch opt-in D12 names. So the honest reading of this
  design's approval is that `hotfix` is settled as a value and the gate is not settled at all. **The
  gate stays its own future design task**, with its own design and its own independent review; the
  approval of 2026-10-31 answers D830–D839 and **not D12**, and F13 is left exactly where it is,
  recorded and unsolved.
- **Its first step is a question, not code.** Put the current behaviour in front of the owner and ask
  what the gate should be. What `hotfix` does today, once something resolves it:
  `internal/rigor/label.go` gives it the security gate alone (every other band is tolerated, its row
  is `overrideSecurity` in `internal/rigor/table.go`); `internal/rigor/filing.go` files one backlog
  item for what it deferred; its levels are **Pro's** (`internal/rigor/levels.go`, the other
  design's **O10** — `docs/review-levels-ab-jobmax/reconciliation.md` §4, the owner's ruling of
  2026-10-30) and its rounds are **1**, the number it spends: **O3 is answered** (2026-10-31) — a mode
  **may** go below today's default of two — so hotfix is **1/1**, and the answer sets the per-mode loop
  defaults to **POC 1/1, Startup 2/2, Pro 3/3, Critical 3/5, Hotfix 1/1**, each pair being the mode's
  review ceiling and its adversarial ceiling (`RoundCeiling` and `AdversarialCeiling`,
  `internal/rigor/rounds.go`). The `minRoundCeiling` clamp to two goes with the answer, in the task
  that implements it — the rigor-modes design's **T3.2**, whose `Depends on O3` line the answer
  unblocks (`docs/rigor-modes/implementation-plan.md`) — so until that task lands the code still
  clamps, and this paragraph is the ruling rather than a reading of `main`. So a hand-written
  `rigor.default: hotfix` in a layer or in a `fugaro.yaml` already reaches a mode that tolerates
  almost everything with **no gate at all**, while `fugaro run --mode hotfix` cannot take effect
  (raise-only, D6) — the two doors disagree, and only one of them is closed.
- **Nothing lands before the answer.** The design above refuses to *write* hotfix (D834) and says
  plainly that it does not close this (F13, D834's row); a task here that invented a gate would be
  making the product decision O-8-shaped decisions are for the owner. The question is therefore the
  deliverable: which key, read from the base branch of which repository, refusing on the first run
  and on every later one, and whether the row's tolerance is right at all given O10's "Pro's set".
- **When it is answered**, the shape is small: the key in `schemas/fugaro.schema.json` and in
  `internal/config/scope.go`, the read in the resolution path, one refusal sentence that names
  `fugaro run --mode hotfix`'s own door, and tests that a first run without the opt-in is refused and
  that a hand-written `rigor.default: hotfix` with no opt-in is refused too.
- **Owner of the answer:** the owner (D12 is theirs). **Not a blocker** of R0–R6.

## R8. Two area rules with one glob are refused where the layer is validated (F14)

The other follow-up. Two area rules with the same `match` in one layer are accepted today, and
`rigor.Strictest` (`internal/rigor/mode.go`) then applies the stricter of the two: the weaker rule's
intent is discarded with **nothing said**, and the layer still parses.

- **Files (3):** `internal/config/validate.go` — the refusal goes in **`validateRigor`**, which is
  the one place that already sees the whole `areas` slice and runs `rigor.Area.Validate` per rule;
  it is reached from `ParseProjectLayer` (`internal/config/layer.go`), so both the merged layer a run
  reads and the published object's own validation get it from one edit — plus
  `internal/config/validate_test.go` and the strict-decoding test in `internal/config/layer_test.go`
  that already pins the layer's unknown-field behaviour. **Not in `rigor.Area.Validate` itself**,
  which sees one rule and cannot know about its twin; the check needs the slice, and `validateRigor`
  is where the slice is. `schemas/fugaro.schema.json` is unchanged: a duplicate glob is a document
  error, not a shape error, and the schema has no way to say "unique by `match`".
- **Red step:** `TestLayerRefusesTwoAreaRulesWithTheSameGlob` fails while the second rule loads.
- **Tests:** `TestLayerRefusesTwoAreaRulesWithTheSameGlob` (the refusal names both modes and the
  glob), `TestLayerAllowsTwoAreaRulesWithDifferentGlobs` (the half that must keep working, including
  two globs where one is a prefix of the other — different strings are different rules),
  `TestRigorRefusalIsSameFromBothAnchors` (`validateRigor`'s one function serves the project layer's
  `defaults.rigor` and a repository's own `rigor` block — the same "one function, two prefixes" shape
  the governance bounds already have — so `fugaro validate` on a `fugaro.yaml` and the published
  object's own validation refuse the same document, and `config publish` cannot accept what a run
  rejects).
- **Hermetic:** `t.TempDir()`, the existing layer fixtures. **Not a blocker** of R0–R6.
- **Why a refusal and not a warning:** a layer is not a log. The two rules disagree about one path
  and the run reads one of them; saying nothing is what makes a hand-written layer lie, and a warning
  nobody reads is the same silence with more output.

## W. `fugaro setup` asks the project's rigor mode explicitly (D900, answered 2026-10-31)

The one task here that is not a `rigor` subcommand, and the one the owner asked for by name: the
wizard asks the mode, because `defaults.rigor.default` is the key that decides how much review a whole
project's runs get and a wizard that never names it leaves every project on the built-in default
(`rigor.Default`, `internal/rigor/mode.go`) with nothing said — the "important and easy to miss" state
the ruling of 2026-10-31 ends. Its letter is the wizard's and not this family's, because the files it
changes are the wizard's and the setup skill's.

- **The question is a row of `setupQuestions`, and that is what makes it required.** The wizard's
  questions are one table (`internal/cli/setup.go`'s `setupQuestions`, the "table the phase's own
  list" W3.5 built): `wizardStage` (`internal/cli/wizard_stage.go`) asks each row in the table's order
  through `questionFor(flag)`, `Missing()` returns every row's `missing` name — which is what
  `initflow.Run`'s non-interactive check turns into the one error naming all of them — and
  `TestEveryWizardQuestionHasAFlag` (`internal/cli/setup_wizard_test.go`) holds the table to the run in
  both directions, so a question with no flag, or a flag nobody can pass, fails there. W adds the row
  `{what: "the project's rigor mode", label: "Rigor mode", flag: "--rigor", missing: "--rigor <mode>"}`
  and asks it from `wizardStage.ask` with the other questions, after the name (the mode is about the
  project the name settles). Nothing else about the phase moves: the answer is recorded on the run's
  own options the way `o.cloud.gcpProject` and `o.cloud.region` are, and the phase still applies
  nothing (`wizardStage.Apply` refuses).
- **The four modes, the recommended answer first, and no silent default.** The question prints the
  four project modes with one line each — what it costs and what review it runs — from the two tables
  that already hold those answers (`internal/rigor/levels.go` for the levels, `internal/rigor/rounds.go`
  for the rounds, whose defaults **O3's answer** set):
  - **`pro` — the recommendation, printed first**: level 2, the senior review that decides readiness,
    and no adversarial pass; up to 3 review rounds. It is the built-in default, so it is what a project
    that says nothing already runs today, and the one line says so.
  - `startup` — the same level 2 review, up to 2 rounds: less spend, less to spend it on.
  - `poc` — level 1 only (`levels.go`'s row turns L2 off and L1 on, so D723's first-line review runs
    and decides readiness) and 1 round: the cheapest mode there is.
  - `critical` — level 2 **and** level 3, the adversarial pass after it; 3 review rounds and up to 5
    adversarial ones: the most review, and the most money.
  The four lines are printed before the prompt, the way the billing question prints its list, and the
  prompt itself carries **no suggestion**: it is `initRun.prompt` with `def` empty
  (`internal/cli/init_inputs.go`), so Enter is not an answer — an empty line prints "a value is
  needed" and asks again — and the answer is validated against the closed vocabulary (`rigor.Parse`,
  whose refusal names `rigor.ModeList()`), so a near miss is refused in the wizard's words exactly as
  `fugaro run --mode` refuses it (F8). A default nobody typed is the state this task exists to end: on
  every other question the wizard takes Enter as the answer, and here it must not.
- **`hotfix` is not offered and not writable — D834 stands.** It is a per-run mode (D12's own door,
  `fugaro run --mode hotfix`), the prompt does not list it, and `--rigor hotfix` is refused with the
  sentence `fugaro rigor set` refuses it with: R4's refusal (D834), which names that operator door and
  is owed on its own terms. W does not fork the sentence and does not close F13's missing gate, which
  stays where R7 leaves it.
- **The write is the R-chain's, and the run that asks publishes it (HARD RULE).** The answer never
  edits a file from the question phase, which applies nothing: it is carried on the run's options into
  **R1's `Layer` stage** (`internal/cli/init_layer_stage.go`), the one stage that writes and publishes
  the one local layer file. Before it publishes, the stage splices `defaults.rigor.default: <mode>`
  into that file — created when the project has published none, exactly as `rigor set` creates it
  (D830, `TestRigorSetCreatesTheFileWhenNoLayerIsPublished`) — through **R2's `EditRigor`** over
  `internal/yamledit` and the **shared write helper settings S5 lands**
  (`internal/cli/settings_write.go`: the diff, the confirmation, the agent-session refusal, the backup,
  the atomic write), which are the same two helpers `fugaro rigor set` (R4) calls. It then publishes
  through `publishLayer` and fans out through `fanOutLayer` behind update's one confirmation. **No
  second command**: the answer reaches runs in the setup run itself, which is F18's HARD RULE read
  literally, and a run whose confirmation is declined leaves the file **`unpublished`** — the shared
  state word, with `fugaro update` as the one documented next step — rather than a state only a
  hand-edit or `fugaro config publish FILE` could fix. Where the file or the published layer already
  carries a `defaults.rigor.default` (a project whose layer someone else published), the answer
  replaces it and **the diff shows the value it replaces**, so a teammate's value is never overwritten
  without the old one printed beside the new.
- **Non-interactive: `--rigor <mode>`, and without it the run is refused.** `--rigor` is the row's flag
  and the wizard's own spelling, registered with the flags `fugaro setup` and `fugaro update` already
  share (`internal/cli/init.go`, beside `--name` and `--gcp-project`), and its help names only the
  reader's door (`TestNoDoorsFlagHelpNamesTheOtherDoor`). A run that cannot ask — `--non-interactive`,
  a pipe, `--json`, `--yes`, or `--print-vars` (`initRun.canAsk`) — is **refused with the question's
  own text**, `--rigor <mode>`, in the one error the loop builds from `InputStage.Missing()`
  (`wizardFlagsLeft`): the recommended answer is **not** taken for the caller, because a scripted setup
  is where a silent default would be least visible. **`--yes` does not choose the mode either**: D12's
  answer covers the ordinary steps a door owns, and this is a product decision the wizard asks, not a
  step to confirm — so `fugaro setup --yes` with no `--rigor` is refused exactly as a run with no
  terminal is. **`--rigor` is a first run's flag**: an installation that already has a project config
  converges as `fugaro update` does (`announceAlreadySetUp`, `internal/cli/setup.go`), where the mode
  is not asked and not written, and the flag there is refused naming **`fugaro rigor set`** — the one
  command that changes a project default — rather than honoured as a second door to the same key.
- **`fugaro update` asks nothing, and its summary shows the current mode.** `update` registers no
  question phase at all: `wizardStage.ask` returns early when this run selected a project config
  (`!creating`), so the mode is never re-asked and an answer already in the layer is never rewritten
  by a converge. What the run says instead is one line in the summary it already prints — the review
  screen's fact block (`internal/cli/init_review.go`'s `reviewScreen`, where `alert email:` and the
  billing account are) — carrying the mode `rigor.Resolve` returns for the project
  (`internal/rigor/resolve.go`) with its own source word, so a project that answered the question reads
  `pro (project)` and one that has not reads the built-in default as the built-in default rather than
  as a choice. The wording is **R3's** (`internal/cli/rigor_show.go`'s line for the project layer), not
  a second sentence saying the same thing two ways.
- **The console and the mobile app ask nothing, and this task adds nothing there.** Read: the console's
  onboarding is two screens and one command (`console/src/main/onboarding.ts`, whose
  `FUGARO_SETUP_COMMAND` is the string `fugaro setup`, pinned to the CLI's own declaration by a unit
  test), and its own ruling is that **the console asks the operator for nothing** — it lists the
  installations the CLI finds and, with none, prints the command to run. `fugaro setup` needs a real
  terminal and the user's own credentials and refuses a coding agent's session, so the question is the
  CLI's, asked at the operator's terminal; the console's screens, its `spawn`/IPC allow-lists and
  `console/docs/json-contract.md` have nothing to change, and no console task is added (D836's own
  reason). The mobile app has no setup path at all: nothing under `mobile/` or `shared/` names `fugaro
  setup`, `defaults.rigor` or the layer, so there is nothing there to ask and no task.
- **Docs, the skill, and the plugin's coverage.** `docs/gcp-setup.md` is the page a first-time user
  follows, and its sentence listing the wizard's questions ("the GCP project ID …, the region … and the
  name itself are each asked once with a suggestion that Enter takes") becomes false the moment this
  question exists: the page adds the rigor question and says it is the one question with **no**
  suggestion, inside `TestDocsGCPSetupStaysShort`'s line budget (`internal/cli/docs_gcp_setup_test.go`).
  `plugin/skills/setup/SKILL.md` gains the sentence that tells the user's agent what the question is —
  a project-wide decision the user makes once, whose answer is *not* the skill's `agent.review_rounds`
  topic (that one is per repository, and the two must not be bundled, the rule
  `TestSetupSkillSplitsMachineSizeAndReviewRounds` already holds the skill to) — and its step 1 block
  (`fugaro setup --project <name>`) is unchanged. The plugin's command-and-flag coverage needs **no
  test change**: the skill lint resolves every command and flag a skill names against the CLI tree
  (`plugin/skills_lint_test.go`'s flag rule, and `plugin/setup_skill_test.go` for the `setup` lines), so
  a `--rigor` the tree does not have is a failure and one it has is the coverage — the file to change
  is the skill, not the test. `docs/setup-wizard/implementation-plan.md` is that folder's own plan and
  its question table is there: the row is added with the owner's go for those files (the plan's "Files
  other work owns" rule), and `docs/project-layer.md` §2/§7 and this folder's README are **R6's**, so
  the sentence that says the wizard asks is written by R6's task in the release that lands W.
- **Files (12):** `internal/cli/setup.go` (the `setupQuestions` row, the four lines it prints and the
  refusal of `--rigor` on a converge), `internal/cli/init.go` (the `--rigor` flag and its help, and the
  field the answer is recorded on), `internal/cli/wizard_stage.go` (the question itself),
  `internal/cli/init_layer_stage.go` (R1's stage: the splice before the publish),
  `internal/cli/init_review.go` (the summary line), `internal/cli/setup_rigor_test.go` (new),
  `internal/cli/setup_wizard_test.go` (the table's contract, extended),
  `internal/cli/init_layer_stage_test.go` (R1's file: the answer published by the same run),
  `plugin/skills/setup/SKILL.md`, `docs/gcp-setup.md` and `docs/setup-wizard/implementation-plan.md`.
  **Eleven named and one new test file is twelve**; W adds no file under `internal/rigor` and no new
  command, and nothing here is a fifth helper beside `internal/cli/settings_write.go`.
- **Red step:** `TestSetupAsksTheRigorModeWithNoSilentDefault` fails while no question prints the four
  modes and Enter is still an answer (`setupQuestions` has no rigor row, so nothing prints and nothing
  refuses).
- **Tests:** `TestSetupAsksTheRigorModeWithNoSilentDefault` (the four lines, the recommendation first,
  and that an empty answer is not accepted), `TestSetupRigorAnswerRefusesANearMiss` (the vocabulary),
  `TestSetupRigorRefusesHotfix` (D834's sentence, naming `fugaro run --mode hotfix`),
  `TestSetupWithoutATerminalIsRefusedWithTheRigorQuestion` (`--non-interactive`, `--yes`, `--json` and
  a pipe: the one error names `--rigor <mode>`), `TestSetupYesNeverChoosesTheRigorMode` (the other half
  of the same rule, asserted on its own), `TestSetupWritesTheRigorAnswerAndPublishesItInTheSameRun`
  (one run: the file carries `defaults.rigor.default`, the object is published once, the fan-out runs —
  the HARD RULE's test at this door, beside R1's `TestInstallationAtThePreviousVersionConverges`),
  `TestSetupRigorAnswerShowsTheValueItReplaces` (a layer that already carries `pro`, the answer
  `critical`: the diff shows both), `TestSetupRigorAnswerLeavesNoSecondCommandWhenDeclined` (the run's
  confirmation declined: the file is `unpublished`, nothing is published, and the sentence names
  `fugaro update`), `TestSetupRigorAnswerKeepsTheRestOfTheFile` (comments and unrelated keys survive
  the splice — R2's own property, asserted here at this caller), `TestUpdateNeverAsksTheRigorQuestion`
  (an existing project: no prompt at all, `--rigor` refused naming `fugaro rigor set`, and
  `TestEveryWizardQuestionHasAFlag` still green with the new row), `TestUpdateSummaryShowsTheResolvedMode`
  (the summary line, `pro (project)` for a layer that writes it and the built-in default otherwise),
  `TestSetupInACodingAgentsSessionWritesNothing` (the shared helper's refusal, `--yes` included, with
  the diff and the command it would have run printed).
- **Hermetic:** `t.TempDir()` config home for the layer file and the local project config,
  `internal/gcpfake` for the runs bucket, `internal/testutil`, `git init` for a fake checkout. No
  apply, no deploy, no cloud call.
- **Depends on:** **R0** (the layer file's path), **R1** (the stage that publishes it — without it the
  answer is the second command the HARD RULE forbids), **R2** (`EditRigor`), **R3** (the summary line's
  wording), the settings design's **S5** (the shared write helper) and **R4** (the sentence the refusal
  of `hotfix` uses, D834). Not on R5, R6, R7 or R8; R7's missing gate is untouched by it.

## Live checks

None. Every behaviour above is a local file edit, a local read or an offline resolution, and each is
tested hermetically. R1's cloud write goes through the publish path the installation's existing
update checks already exercise; no new check number is taken. R7 lands no behaviour until the owner
answers, so it takes none either. **W** takes none of its own and adds none to `fugaro update`: the
question is local, its write is R1's, and the one cloud write it causes is the same run's publish,
which the existing update checks already cover.
