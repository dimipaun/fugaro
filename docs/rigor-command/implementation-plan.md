# `fugaro rigor`: implementation plan

**Nothing here is approved.** Every decision (D810–D819) is a recommendation with no answer yet
([reconciliation.md](reconciliation.md) §3). A decision answered the other way changes the tasks
that cite it. Each task touches at most 12 files, names its tests, starts with a red step, targets
**CLI 0.15.0** and ends at the gates below.

## Order, and what this plan does not own

- **External dependency E1 (the `models` plan's M0), which R2 does not wait for.** That task writes a
  comment-preserving editor for a **different document** — the local project config,
  `internal/localcfg/edit.go` — with a different shape, so it is a neighbour rather than a
  dependency: R2 creates `internal/config/layeredit.go` whether or not E1 has shipped, and R2 says
  which helpers (not files, not ops) may be shared between them. One design principle, two
  documents, two files. **Not** a merge of the two works' plans: they are separate plans with
  separate owners.
- **External dependency E2 (the mode floors, `docs/rigor-modes` plan T1.5).** `show`'s floors
  section prints the review floor (F10) and one line saying the mode floors are not built (F9). The
  task that ships T1.5 adds the sensitive-path set to that section; until then no surface here may
  claim to display a floor that does not exist. Not a blocker.
- **The console's project view** (plan P6, not built) is not an external dependency of anything:
  D816 puts no console task in this plan.
- **Files other work owns:** registering the command in `internal/cli/root.go`, the generated
  `website/content/cli-reference.md`, `plugin/**` and `console/**` are each owned by another open
  work stream at the time of writing. R3 and R6 name them, and the task that touches them waits for
  the owner's go for those files.

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | R0, R2 | nothing |
| 1 | R1, R3 | R0 |
| 2 | R4 | R0, R2 |
| 3 | R5 | R4 |
| 4 | R6 | R1–R5 |

**R1 is a prerequisite of the release, not of the commands.** R3–R5 may merge before it, but the
release that carries them carries R1: without it the owner's next step after `rigor set` is
`fugaro config publish FILE`, which is the second command the HARD RULE forbids (F18).

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`. Never the whole
`internal/cli` package.

## R0. The local project layer file has a home (D810)

Without it, "the file the owner publishes from" has no name, and `set` cannot be written (F3).

- **Files (8):** `internal/localcfg/localcfg.go` (an optional `project_layer:` path, absolute, and
  its validation), `internal/localcfg/localcfg_test.go`, `internal/cli/config_publish.go` (the FILE
  argument becomes optional and defaults to the resolved path), `internal/cli/config_publish_test.go`,
  `internal/cli/config_show.go` (`config layer` prints the local path and whether it differs from the
  published object, with sha256s), `internal/cli/doctor_layer.go` (the same as a doctor check),
  `internal/cli/doctor_layer_test.go`, `docs/project-layer.md` (§6 and §7).
- **Red step:** `TestConfigPublishWithNoFilePublishesTheLocalLayer` fails while `publish` takes an
  exact-1 argument and no path is resolved.
- **Tests:** `TestProjectLayerPathIsTheRecordedOneElseTheConvention` (recorded key wins; otherwise
  `<config home>/fugaro/projects/<project>-layer.yaml`), `TestProjectLayerPathMustBeAbsolute`,
  `TestConfigPublishRefusesWithoutAFileAndWithoutALocalLayer`,
  `TestDoctorReportsALocalLayerFileThatDrifts`, `TestConfigLayerPrintsTheLocalPath`.
- **Hermetic:** `t.TempDir()` config home, `internal/gcpfake` for the bucket, `internal/testutil`.

## R1. `fugaro update` publishes the project layer (D811). **SEC**

The stage writes the object every repository of the project resolves against: it changes build and
test commands for everyone, and it is the key whose change the executable-key gate exists for.

- **Files (8):** `internal/initflow/stage.go` (the `Layer` stage name, its slot after `Policy`, and
  its `needs`), `internal/initflow/stage_test.go`, `internal/cli/init_layer_stage.go` (new: the
  read, the diff, the confirmation, `publishLayer` and `fanOutLayer`), `internal/cli/init_layer_stage_test.go`
  (new), `internal/cli/init.go` (registering the stage), `internal/cli/config_publish.go` (the shared
  publish path, so the two doors cannot drift), `docs/project-layer.md` (§7 and §8).
- **Red step:** `TestUpdatePublishesTheProjectLayerWhenItDrifts` fails while no stage reads a local
  layer file.
- **Tests:** `TestUpdatePublishesTheProjectLayerWhenItDrifts` (the diff is printed, the object is
  written once, the fan-out runs), `TestUpdateLayerStageSkipsWithNoLocalFileAndSaysSo`,
  `TestUpdateLayerStageRefusesAFileThatDoesNotParse`,
  `TestUpdateLayerStageRefusesExecutableChangesWithoutTheFlag` (the same banner and refusal
  `config publish` gives), `TestUpdateLayerStageAppliesNothingInAnAgentSessionEvenWithYes`,
  `TestInstallationAtThePreviousVersionConverges` (an installation whose local layer predates this
  release ends the update with it published — the HARD RULE's own test).
- **Hermetic:** `internal/gcpfake` for the runs bucket, `t.TempDir()` for the state and the layer
  file. No apply, no deploy, no cloud call.

## R2. A comment-preserving editor for the project layer file (D810). **SEC**

The layer decides what every repository of the project runs, and a lossy rewrite is a change nobody
reviewed. The write must keep comments, key order and every key it does not touch.

- **Files (5):** `internal/config/layeredit.go` (new — this is the file R2 owns, named rather than
  conditional: it is the project layer's editor, and E1's `internal/localcfg/edit.go` is the local
  project config's, a different document), `internal/config/layeredit_test.go` (new),
  `internal/config/testdata/hand-written-layer.yaml` (new; the directory does not exist yet, so R2
  creates it; placeholder names only),
  `internal/config/testdata/hand-written-layer-comments.yaml` (new),
  `internal/config/testdata/hand-written-rigor-areas.yaml` (new).
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

## R3. `fugaro rigor show` (D815, D819)

- **Files (8):** `internal/cli/rigor.go` (new: the command group), `internal/cli/rigor_show.go`
  (new), `internal/cli/rigor_show_test.go` (new), `schemas/rigor-show.schema.json` (new),
  `schemas/schemas_test.go` (the new schema loads and the command's output validates),
  `internal/cli/root.go` (registration; owned elsewhere, see "Order"), `docs/project-layer.md`
  (§7's command table gains a row).
- **Red step:** `TestRigorShowPrintsTheResolvedModeAndItsLayer` fails while the command does not
  exist.
- **Tests:** `TestRigorShowPrintsTheResolvedModeAndItsLayer` (the four sections of spec §2, in
  order), `TestRigorShowNamesTheAreaRuleThatMatchedAPath` (`--path`, and the strictest of two),
  `TestRigorShowUnknownForARepositoryWithNoCheckout`,
  `TestRigorShowPrintsTheReviewFloorAndSaysTheModeFloorsAreNotBuilt`,
  `TestRigorShowReportsDriftFromThePublishedLayer`, `TestRigorShowJSONMatchesSchema`,
  `TestRigorShowJSONNeverSaysRequestOrReached`, `TestRigorShowWritesNothing`.
- **Hermetic:** `t.TempDir()` config home, a fake checkout made with `git init` in the temp dir, a
  seeded layer cache or `internal/gcpfake` for the published object.

## R4. `fugaro rigor set` and `unset` — the project default and its areas (D812, D814, D817, D818). **SEC**

It writes the key that decides how much review a whole project gets, and `unset` removes one.

- **Files (6):** `internal/cli/rigor_set.go` (new), `internal/cli/rigor_set_test.go` (new),
  `internal/cli/rigor_write.go` (new: diff, confirmation, agent-session refusal, `backupFile`,
  `writeFileAtomic`, shared with R5 — the *writing* is shared, the *editing* is not: the document goes
  through R2's editor), `internal/cli/rigor_write_test.go` (new), `internal/cli/rigor.go`,
  `internal/config/layeredit.go` (R2's file, and only if an op R2 did not foresee is needed).
- **Red step:** `TestRigorSetRefusesHotfixInEveryForm` fails while the subcommand does not exist.
- **Tests:** `TestRigorSetWritesTheProjectDefault`, `TestRigorSetRefusesAVocabularyNearMiss`,
  `TestRigorSetRefusesHotfixInEveryForm`, `TestRigorSetLoweringAsksForTheTypedProjectName` (and
  `--yes` does not stand in), `TestRigorSetRaisingAsksYesNo`,
  `TestRigorSetRefusesWithNoLocalFileWhileALayerIsPublished`,
  `TestRigorSetCreatesTheFileWhenNoLayerIsPublished`, `TestRigorSetRecordsThePathItWasGiven`,
  `TestRigorSetRefusesInAnAgentSessionEvenWithYes`, `TestRigorSetAreaAddsARuleAndRequiresAName`,
  `TestRigorSetAreaReplacesTheModeOfAnExistingGlob`, `TestRigorSetAreaRefusesATwinGlob`,
  `TestRigorSetAreaRefusesAGlobOutsideTheRepository`, `TestRigorUnsetRemovesTheKeyRatherThanWritingPro`,
  `TestRigorUnsetOfNothingIsAnExitZeroSentence`, `TestRigorSetKeepsTheRestOfTheFile`,
  `TestRigorSetPrintsOnlyFugaroUpdateNext`, `TestRigorSetWritesNothingInTheCloud`.

## R5. `fugaro rigor set --repo R` (D813)

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
- **Red step:** `TestRigorRepoEditsOnlyTheDefaultLine` fails while the subcommand does not exist.
- **Tests:** `TestRigorRepoEditsOnlyTheDefaultLine`, `TestRigorRepoInsertsTheRigorBlockWhenAbsent`,
  `TestRigorRepoRefusesHotfix`, `TestRigorRepoLoweringAsksForTheTypedProjectName`,
  `TestRigorRepoNeverCommits`, `TestRigorRepoRefusesWithoutACheckout`, `TestRigorRepoRunsInAnAgentSession`,
  `TestRigorRepoAreaWritesTheRepositorysOwnRule`.

## R6. Documentation, skill and release notes

- **Files (6):** `docs/project-layer.md` (§2's rigor paragraph and §7's command table lead with the
  commands; the hand edit stays as the reference), `docs/rigor-command/README.md` (the status line),
  `plugin/skills/working/SKILL.md` (owned elsewhere, see "Order"), `docs/releases/vX.Y.Z.md`
  (`### For operators`: "run `fugaro update`" and what it will ask — including that it may now
  publish the project layer and how it confirms that), `website/data/release-summaries.yaml`, the
  generated `website/content/cli-reference.md` (regenerated, not hand-edited).
- **Red step:** the plugin's command test fails while the skill names `fugaro rigor set` before R4 is
  merged; it turns green on R4's branch.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs`, the plugin tests, `npm run check` in
  `website/`.

## Live checks

None. Every behaviour above is a local file edit, a local read or an offline resolution, and each is
tested hermetically. R1's cloud write goes through the publish path the installation's existing
update checks already exercise; no new check number is taken.
