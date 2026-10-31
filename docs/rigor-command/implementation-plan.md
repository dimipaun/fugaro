# `fugaro rigor`: implementation plan

**D830 and D831 are answered** (the owner's SD4 ruling of 2026-10-31) and R0–R2 are written to that
answer; **D832–D839 are recommendations with no answer yet** ([reconciliation.md](reconciliation.md)
§3), and a decision answered the other way changes the tasks that cite it. Each task touches at most
12 files, names its tests, starts with a red step, targets **CLI 0.15.0** and ends at the gates
below.

## Order, and what this plan does not own

- **External dependency E1 (the shared comment-preserving YAML editor), which R2 now depends on
  rather than duplicating.** There is **one** such editor in this CLI, and settings, the `models`
  plan and rigor all use it: three documents (the local project config, the local layer file,
  `fugaro.yaml`), one splice (locate in the node tree, replace the bytes). **R2 must be that editor
  or call it — it must not create a second one under a name of its own** (`internal/config/layeredit.go`
  is exactly the duplicate this ruling removes). **E1 has not landed**: there is no shared editor
  package on `main` today (verified 2026-10-31), and the one path this folder has named for it,
  `internal/localcfg/edit.go`, **does not exist** — it is the file the `models` plan's M0 *creates*
  (`docs/models-commands/implementation-plan.md`, "Files: `internal/localcfg/edit.go` (new)"), not a
  file to go and read. So the plan names **no existing file** for E1: M0's path is a proposal, the
  `settings` work owns the package, and the only precedent that exists today is
  `setGCPProjectLine` (`internal/cli/init_fugaroyaml.go`) — one scalar on one line, which R2's splice
  generalises rather than repeats. **R2 resolves this at its start, not by assuming:** if the shared
  package has landed, R2 adds its ops to it; if it has not, R2 lands the package at M0's path (or
  wherever the `settings` work has put it by then, whichever the owner's go says) and the other two
  works reuse it. That is a sequencing detail, never a reason for two editors. **Not** a merge of the
  three works' plans: they are separate plans with separate owners.
- **External dependency E2 (the mode floors, `docs/rigor-modes` plan T1.5).** `show`'s floors
  section prints the review floor (F10) and one line saying the mode floors are not built (F9). The
  task that ships T1.5 adds the sensitive-path set to that section; until then no surface here may
  claim to display a floor that does not exist. Not a blocker.
- **The console's project view** (plan P6, not built) is not an external dependency of anything:
  D836 puts no console task in this plan.
- **Files other work owns:** registering the command in `internal/cli/root.go`, the generated
  `website/content/cli-reference.md`, `plugin/**` and `console/**` are each owned by another open
  work stream at the time of writing. R3 and R6 name them, and the task that touches them waits for
  the owner's go for those files.

**The `Needs` column is a *code* dependency: what must be merged for that task to build, test and be
reviewed on its own.** It is not a statement about the release. A task whose code is independent of
R1 still cannot be *shipped* without it, and that is the operational side of the same table, written
out below it.

| Level | Tasks | Needs (code) |
| --- | --- | --- |
| 0 | R0 | nothing |
| 1 | R1, R2 | R0 for R1; E1 for R2 (the shared editor, or R2 lands it) |
| 2 | R3 | R0 |
| 3 | R4 | R0, R2 |
| 4 | R5 | R4 |
| 5 | R6 | R1–R5 |
| 6 | R7, R8 | nothing (R7 lands nothing before the owner answers) |

**R1 is an operational prerequisite of the release, not a code dependency of R3–R5.** The two are
different questions and the table above answers only the first one:

- **Code.** Once the row's own needs are merged, each of R3–R5 builds, tests and reviews on its own:
  they read and write §1's file, which R0 names, and **none of them calls anything R1 adds**. So no
  row of the table lists R1, and a branch may land without it.
- **Release.** The release that carries R3–R5 carries R1, because a `set` that lands without the
  layer stage leaves the owner's next step as `fugaro config publish FILE` — the second command the
  HARD RULE forbids (F18). This is not a merge-order rule and not something the level table can
  express: it is a rule about which tasks may be in one release, and it binds the release, not the
  branches.
- **And R1's seeding half is what makes the commands usable on a real installation.** R3–R5 are
  correct without it — their own tests create the file — but on an installation whose layer predates
  this release the file is absent while a layer is published, so the first `rigor set` is D830's
  refusal and the owner's next step is again a second command. The commands ship as written; the
  *release* ships with the seed. R1's task therefore carries both halves, and the release notes
  (R6's `### For operators`) say the one command now seeds as well as publishes.

**R7 and R8 are follow-ups this design found and does not fix** (reconciliation §4): the missing
hotfix gate (F13) and the loader that accepts two area rules with one glob (F14). Neither blocks
R0–R6; both target 0.15.0 with them. R7's first step is a question to the owner and it lands nothing
before the answer, so its level is the last row of the table and not a gate on anything. Whichever of
R7 and R8 lands in the same release as R0–R6 has its user-visible half written into that release's
notes by R6; a release that carries neither notes neither.

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`. Never the whole
`internal/cli` package.

## R0. The one local layer file, and the doors that name it (D830, answered)

Without it, "the file the owner publishes from" has no name, and `set` cannot be written (F3). The
answer is a convention, not a key: the path is the project's own name under the config home's
`projects/` directory, beside the project's config, so **no `--layer` flag and no `project_layer:`
key exist** — there is nothing to resolve and nothing to record.

- **Files (6):** `internal/localcfg/localcfg.go` (`LayerFile` — the path, derived from the project
  name; the same directory helper the project config uses), `internal/localcfg/localcfg_test.go`,
  `internal/cli/config_publish.go` (the FILE argument becomes optional and defaults to that path),
  `internal/cli/config_publish_test.go`, `internal/cli/config_show.go` (`config layer` prints the
  local path and whether it differs from the published object, with sha256s), and
  `internal/cli/doctor_layer.go` plus `internal/cli/doctor_layer_test.go`. `docs/project-layer.md`
  (§6 and §7) is R6's, so that this task's diff stays code.
- **What R0 changes in `doctor_layer.go`, which already exists (9.2 KB, so this is an addition and
  not a rewrite).** It **adds one function**, `localLayerFileCheck(lc *localcfg.Config, rf resolvedFile)
  []doctorCheck`, and **one check ID**, `local-layer-file`; it appends that function's result to
  `doctorLayerChecks`'s output and touches nothing else. The existing checks and their IDs stay
  exactly as they are: `project-layer` (the layer that applies, or that none does), `project-layer-drift`
  (the repository's last run against the published layer), `project-layer-copy` (the repository's own
  copy), `image-config-*` and `imageLeafKeys`' attribution. The new check reports §1's path, whether
  the file exists, and — when it does — its sha256 beside the published object's, saying `drift` when
  they differ and adding `fugaro update` as the way to publish it, the same sentence `config layer`
  and `show` print (one wording, three doors). **It needs no bucket round trip**, which is why it is
  appended on *every* path `doctorLayerChecks` returns from — including the two early returns (no
  layer resolved; a rewritten origin) that today skip the rest — because the published side it
  compares against is the already-resolved `rf.Layer` (its `SHA256`, and its `Note` when it came from
  the offline cache), never a fresh read. A check that vanished exactly when the bucket was
  unreachable would be the one line the owner needs in the state that breaks `set`.
- **Red step:** `TestConfigPublishWithNoFilePublishesTheLocalLayer` fails while `publish` takes an
  exact-1 argument and no path is derived.
- **Tests:** `TestLayerFileIsTheProjectNameUnderProjects` (the path, and that a project name that is
  not a single path element is refused), `TestConfigPublishWithNoFilePublishesTheLocalLayer`,
  `TestConfigPublishRefusesWhenTheLocalFileDoesNotExist` (the message names `fugaro update`, not a
  flag), `TestConfigLayerPrintsTheLocalPathAndItsDrift`,
  `TestDoctorReportsALocalLayerFileThatDrifts` (the new `local-layer-file` check's ID, its severity
  and its sentence, with the existing `project-layer*` checks asserted unchanged in the same test so
  the addition is shown not to have moved them).
- **Hermetic:** `t.TempDir()` config home, `internal/gcpfake` for the bucket, `internal/testutil`.

## R1. `fugaro update` seeds and publishes the local layer file (D831, answered). **SEC**

The stage writes the object every repository of the project resolves against: it changes build and
test commands for everyone, and it is the key whose change the executable-key gate exists for. Its
**seeding** half writes only a local file, and it is what makes R0's convention usable on an
installation that has never had one.

- **Files (8):** `internal/initflow/stage.go` (the `Layer` stage name, its slot after `Policy`, and
  its `needs`), `internal/initflow/stage_test.go`, `internal/cli/init_layer_stage.go` (new: the seed,
  the read, the diff, the confirmation, `publishLayer` and `fanOutLayer`), `internal/cli/init_layer_stage_test.go`
  (new), `internal/cli/init.go` (registering the stage), `internal/cli/config_publish.go` (the shared
  publish path, so the two doors cannot drift), `docs/project-layer.md` (§7 and §8).
- **Red step:** `TestUpdateSeedsTheLocalLayerFromThePublishedOne` fails while no stage writes a local
  layer file.
- **Tests:** `TestUpdateSeedsTheLocalLayerFromThePublishedOne` (no local file, a published layer: the
  file appears with the published bytes, mode 0600, and the update says what it wrote),
  `TestUpdateSeedWritesNothingWhenTheLocalFileExists` (an owner's file is never overwritten by the
  seed, even when it differs — that is the publish half's job, after a diff),
  `TestUpdateSeedWritesNothingWhenNoLayerIsPublished` (the first `update` of a project that has never
  published one leaves no empty file behind),
  `TestUpdatePublishesTheProjectLayerWhenItDrifts` (the diff is printed, the object is
  written once, the fan-out runs),
  `TestUpdateLayerStageSkipsWithNoLocalFileAndNoPublishedLayer` (the one case where there is nothing
  to seed from and nothing to publish, said in a sentence),
  `TestUpdateLayerStageRefusesAFileThatDoesNotParse`,
  `TestUpdateLayerStageRefusesExecutableChangesWithoutTheFlag` (the same banner and refusal
  `config publish` gives), `TestUpdateLayerStageAppliesNothingInAnAgentSessionEvenWithYes`,
  `TestInstallationAtThePreviousVersionConverges` (an installation whose local layer predates this
  release ends the update with it published — the HARD RULE's own test).
- **Hermetic:** `internal/gcpfake` for the runs bucket, `t.TempDir()` for the state and the layer
  file. No apply, no deploy, no cloud call.

## R2. The rigor ops in the shared comment-preserving editor (D830). **SEC**

The layer decides what every repository of the project runs, and a lossy rewrite is a change nobody
reviewed. The write must keep comments, key order and every key it does not touch — and it must be
the **same** write the settings work and the `models` plan use, because three documents and one
splice is a design, while two editors is a bug waiting for one of them to be fixed.

- **Files (5):** the shared editor package's source and test — **E1's package, not a new one**: it
  locates in the node tree and splices bytes for the local project config, the local layer file and
  `fugaro.yaml`. R2 adds this work's ops to it; it does **not** create
  `internal/config/layeredit.go` or any second copy (spec §9). **Which file that is, R2 settles by
  looking, because there is none to name today**: `internal/localcfg/edit.go` does not exist on
  `main` (verified 2026-10-31) — it is the *new* file the `models` plan's M0 creates — so if E1 has
  landed R2 edits the package where it finds it, and if it has not, R2 lands it at M0's path and the
  other two works reuse it (see "Order" above). The plan names the **package** and never asserts a
  path as if it were there. Plus three fixtures for the layer document, in
  that package's own `testdata/` (created with the package if E1 has not landed): `hand-written-layer.yaml`,
  `hand-written-layer-comments.yaml` and `hand-written-rigor-areas.yaml` (new; placeholder names
  only).
- **Interface:** `EditRigor(data []byte, at []string, ops ...RigorOp) ([]byte, error)`, where `at` is
  the path to the block — `{"defaults", "rigor"}` for the project layer, `{"rigor"}` for a
  repository's file (R5) — and the ops are `SetDefault(mode)`, `RemoveDefault()`,
  `SetArea(name, match, mode)`, `RenameArea(match, name)` and `RemoveArea(match)`. It parses the
  document into a `yaml.Node` tree only to locate the node it must change and splices bytes there;
  it never re-emits the document (spec §9). A refusal returns the input untouched, so a caller can
  compare bytes to know nothing was written.
- **Two checks, and neither stands in for the other.** *Structural validity* is
  `config.ParseProjectLayer` on the output, against the same anchor the publisher uses: a document
  that parses is a layer the cloud accepts, and it is what the red step's second half shows a struct
  round trip still passing. *Preservation* is a **byte** comparison of the output against the input
  around the edit: every comment, key order, quoting style, blank line and unrelated key kept exactly
  as it was. A strict YAML decoder cannot answer the second, because comments are parse artifacts —
  discarded before any struct is decoded — so a rewrite that drops every comment passes the first
  check and fails the second. The tests assert the two separately, and
  `TestEditRigorRefusesWhatParseProjectLayerRefuses` is the first.
- **Red step:** `TestEditRigorKeepsCommentsAndOrder` fails while `EditRigor` does not exist, and the
  same fixture through `ParseProjectLayer` and a struct round trip is shown to lose its comments
  while still parsing (the reason the task exists).
- **Tests:** `TestEditRigorKeepsCommentsAndOrder` (the byte comparison),
  `TestEditRigorDecodesToTheIntendedChangeOnly` (the decoded old layer plus the op equals the decoded
  new layer — the structural half), `TestEditRigorRefusesWhatParseProjectLayerRefuses`,
  `TestEditRigorAddsDefaultAfterTheKeysAlreadyThere`,
  `TestEditRigorAtTheRepositoryAnchorTouchesOnlyItsOwnBlock` (a `fugaro.yaml` fixture through the
  `{"rigor"}` anchor: its other keys and comments survive, and a `defaults.rigor` anchor on the same
  bytes is a refusal, not a silent edit of the wrong block).

## R3. `fugaro rigor show` (D835, D839)

- **Files (8):** `internal/cli/rigor.go` (new: the command group, whose subcommand list is built so
  the **`floor` subgroup SD10 rules for** is added by one entry and not by a reshape — the group
  exists to hold more than `show`, `set` and `unset`), `internal/cli/rigor_show.go`
  (new), `internal/cli/rigor_show_test.go` (new), `schemas/rigor-show.schema.json` (new),
  `schemas/schemas_test.go` (the new schema loads and the command's output validates),
  `internal/cli/root.go` (registration; owned elsewhere, see "Order"), `docs/project-layer.md`
  (§7's command table gains a row).
- **Red step:** `TestRigorShowPrintsTheResolvedModeAndItsLayer` fails while the command does not
  exist.
- **Tests:** `TestRigorShowPrintsTheResolvedModeAndItsLayer` (the four sections of spec §2, in
  order), `TestRigorShowNamesTheAreaRuleThatMatchedAPath` (`--path`, and the strictest of two),
  `TestRigorShowUnknownForARepositoryWithNoCheckout`,
  `TestRigorShowPrintsTheReviewFloorAndSaysTheModeFloorsAreNotBuilt` (and names **`fugaro rigor
  floor`** as the command that changes a floor, SD10),
  `TestRigorShowReportsDriftFromThePublishedLayer`,
  `TestRigorShowJSONLayerNamesWhatWasRead` (spec §2's `layer` member: `published` and `local` present
  with their sha256s, `source` one of the two words, `drift` true only when both are there and
  differ — and **absent**, never `null`, for a project that has published nothing and for a machine
  with no local file), `TestRigorShowJSONMatchesSchema`,
  `TestRigorShowJSONNeverSaysRequestOrReached`, `TestRigorShowWritesNothing`,
  `TestRigorGroupHasNoFloorFlag` (the family's `set`/`unset`/`show` take no flag that writes a floor:
  SD10's line, asserted rather than remembered).
- **Hermetic:** `t.TempDir()` config home, a fake checkout made with `git init` in the temp dir, a
  seeded layer cache or `internal/gcpfake` for the published object.

## R4. `fugaro rigor set` and `unset` — the project default and its areas (D832, D834, D837, D838). **SEC**

It writes the key that decides how much review a whole project gets, and `unset` removes one.

- **Files (5):** `internal/cli/rigor_set.go` (new), `internal/cli/rigor_set_test.go` (new),
  `internal/cli/rigor_write.go` (new: diff, confirmation, agent-session refusal, `backupFile`,
  `writeFileAtomic`, shared with R5 — the *writing* is shared, the *editing* is not: the document goes
  through R2's editor), `internal/cli/rigor_write_test.go` (new), `internal/cli/rigor.go`. It names
  **no file of its own for the edit**: an op R2 did not foresee is added to R2's shared editor
  (E1's package, spec §9), never to a second file under this task's name.
- **Red step:** `TestRigorSetRefusesHotfixInEveryForm` fails while the subcommand does not exist.
- **Tests:** `TestRigorSetWritesTheProjectDefault`, `TestRigorSetRefusesAVocabularyNearMiss`,
  `TestRigorSetRefusesHotfixInEveryForm`, `TestRigorSetLoweringAsksForTheTypedProjectName` (and
  `--yes` does not stand in), `TestRigorSetRaisingAsksYesNo`,
  `TestRigorSetRefusesWithNoLocalFileWhileALayerIsPublished` (the message's remedy is **`fugaro
  update`**, which is the one documented command; it names no flag and no second command),
  `TestRigorSetCreatesTheFileWhenNoLayerIsPublished`,
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
  `internal/cli/rigor.go`.
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
  `docs/rigor-command/README.md` (the status line), `plugin/skills/working/SKILL.md` (owned
  elsewhere, see "Order"), `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`" and
  what it will ask — including that the stage **seeds** the local layer file when it is absent and
  otherwise **publishes** it when it differs, with the diff and its confirmation, so the operator
  knows the one command now does both), `website/data/release-summaries.yaml`, the
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

- **Its first step is a question, not code.** Put the current behaviour in front of the owner and ask
  what the gate should be. What `hotfix` does today, once something resolves it:
  `internal/rigor/label.go` gives it the security gate alone (every other band is tolerated, its row
  is `overrideSecurity` in `internal/rigor/table.go`); `internal/rigor/filing.go` files one backlog
  item for what it deferred; its levels are **Pro's** (`internal/rigor/levels.go`, O10's ruling) and
  its rounds are 1 (`internal/rigor/rounds.go`), which `minRoundCeiling` clamps to today's default of
  2 while O3 is open. So a hand-written `rigor.default: hotfix` in a layer or in a `fugaro.yaml`
  already reaches a mode that tolerates almost everything with **no gate at all**, while
  `fugaro run --mode hotfix` cannot take effect (raise-only, D6) — the two doors disagree, and only
  one of them is closed.
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

## Live checks

None. Every behaviour above is a local file edit, a local read or an offline resolution, and each is
tested hermetically. R1's cloud write goes through the publish path the installation's existing
update checks already exercise; no new check number is taken. R7 lands no behaviour until the owner
answers, so it takes none either.
