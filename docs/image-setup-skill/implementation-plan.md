# Image setup skill: implementation plan

**Status as of 2026-11-10: in progress, 4/14 merged; see [docs/status.md](../status.md) for the current table.** Nothing below is approved: **IS8b's bake step is implemented and unmerged**, in draft PR #2171, written because it waited on a ruling and not on the go (see IS8b), and no other task is implemented. This builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (D1900–D1906, F1–F22, T1). The owner's answers shaped it: repo-level images are **disabled, code kept** (D1900); the bake switch is a **policy boolean** (D1901); the survey reads files through the forge, no clones (D1902); the cap of four is **the skill's** (D1903); services are an **entry key plus the workflow's `env:`** (D1904); the skill is a **new `fugaro:images`** (D1905); the repo-to-image map is **`image_map` in the policy node** (D1906). **Answering a decision is not approving a task**: every task below needs the owner's explicit go. **T1, O3 and O4 were answered 2026-11-09 — T1 (b), O3 (b), O4 (b)**, the readings this plan was written for, so nothing below moves: IS1 keeps T1 (b) and O4 (b), and the bake step IS8b, which waited on O3, is no longer blocked. **IS8b's engine half is implemented and unmerged** (the bake step itself: `internal/imagebake` and the CLI wiring, draft PR #2171); its reader is IS8, still unapproved, so a composed definition bakes a checkout nothing opens yet. Task numbers are IS1–IS13 with IS6 unused (kept so the numbers match the first draft's review).

## Conventions every task follows

- [CLAUDE.md](../../CLAUDE.md) at the root, plus `.claude/rules/terraform.md` (IS1, IS2 touch `internal/infra/**`) and, for IS10, the CLI's provider opener (`api.go` `OpenProvider`, the process environment's token); no `internal/creds*` path is touched.
- **The HARD RULE on `fugaro update`** (F15): IS4, IS5, IS7 and IS8b each carry a test that an installation at the previous version converges through `update`, and `TestUpdateLeavesARunInFlightRunning` still passes. The release says "run `fugaro update`", nothing more.
- Hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`); no secret-shaped literal; no live cloud action by an agent; **never edit `.github/workflows/`**.
- Targeted tests: name them; `go test ./internal/cli/ -run Docs` after any doc change. Skills: `plugin/` tests check every command and flag a skill names against the CLI.
- Wire shapes: a new field may be added; none is retyped or reused (`image_map`, `bake_repos` and the entry's `services` are new names).
- Comments say why, never what; no TODO comments.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| Once a catalog default exists the resolver never returns a per-repo image, and warns once per ignored key | IS1 `TestJobImageIgnoresRepoLevelConfig` |
| A secret never reaches an image layer; generated Dockerfiles use `--mount=type=secret`, no `ARG`/`COPY` of a token | IS12 `TestImagesSkillNoBuildArgSecrets` (text) and IS11b (code) |
| The survey reads through the CLI's provider opener (process-environment token), read-only, and prints no file contents beyond the named signals | IS10 `TestSurveyReadsOnlySignals` (mutation check) |
| A rerun on an unchanged project proposes nothing | IS12 `TestImagesSkillRerunProposesDiffOnly` |
| A skill never builds locally | IS12 `TestImagesSkillNeverLocalBuild` |
| Services start only from a catalog entry's own list, never from repo text | IS9 `TestServicesFromEntryOnly` |
| No path under `fugaro update` needs a second command | IS5 `TestUpdateConvergesPreviousPolicy`, IS8b `TestUpdateConvergesImageBake` |
| A catalog build bakes the checkout of the repository it runs for and no other, at the per-slug path, and says which repositories clone at start | IS8b `TestDecideBakesNothingForAnotherImage`, `TestCatalogBuildBakesNothingWithoutTheMap` |
| An absent `bake_repos` decides exactly as a written true (one field, one meaning), and no rebuild clones a repository the definition was not composed for | IS8b `TestDecideAnUnsetSwitchIsOn`, `TestDecideBakesASharedImageAndNamesTheReaders`, `TestStageRefusesAnotherRepositorysURL`, `TestCatalogCheckJobRefusesADefinitionBakedForAnotherRepository` |
| A definition changes only when the checkout being built runs the entry being built: a project that names no entry and maps none keeps byte for byte what it had | IS8b `TestDecideBakesNothingForAnotherImage`, `TestCatalogBuildBakesNothingWithoutTheMap` |

## Slices and tasks

**Slice A: disable repo-level images** (IS3, IS5, IS1, IS2). **Slice B: the contract and the base** (IS4, IS11). **Slice C: bake and services** (IS7, IS8, IS9). **Slice D: survey and skill** (IS10, IS12). **Slice E: release** (IS13).

### IS3: park the removal chain (docs only)
- **Files:** `docs/repo-image-removal/README.md` (status note, which also corrects the stale "nothing is built" line: R0 and R1 are merged), `internal/cli/docs_repo_image_removal_test.go` only if it pins the edited sentence.
- **Interfaces:** none. **Schema/rules/config:** none.
- **Tests:** `-run 'Docs'`. **Command:** `go test ./internal/cli/ -run Docs -count=1`.
- **Depends on:** none. **Commit:** `docs(repo-image-removal): R2-R10 parked by the image setup skill (D1900)`.
- **Red step:** the pinned-sentence test fails if the note breaks a pin. **Tier:** standard.

### IS5: the policy fields `image_map` and `bake_repos`
- **Files:** `internal/projectpolicy/policy.go`, `policy_test.go`, `internal/localcfg/localcfg.go` (the fields the published node derives from, and their layering against the published value), `internal/settings` (registry and write-allowlist rows), `internal/cli/init_policy_publish.go` and its test, plus the managed-gate and agent-session rows for the writing command (names re-read at start). No `schemas/` file covers the policy node today, so none is edited.
- **Interfaces:** produces `ImageMap map[string]string` and `BakeRepos *bool` on the node (absent = true, D1901); consumed by IS1, IS7, IS9, IS12.
- **Schema change:** two optional keys; an older binary lists them in `Policy.UnknownKeys` and survives. A command (`fugaro image map set`, name re-read) writes `image_map`; it is a write the managed gate and an agent session refuse.
- **Tests:** `TestPolicyImageMapStrictDecode`, `TestBakeReposDefaultsTrue`, `TestUpdateConvergesPreviousPolicy`, `TestUpdateLeavesARunInFlightRunning`.
- **Command:** `go test ./internal/projectpolicy/ ./internal/cli/ -run 'ImageMap|BakeRepos|UpdateConvergesPreviousPolicy|UpdateLeavesARunInFlightRunning' -count=1`.
- **Depends on:** none. **Commit:** `policy: image_map and bake_repos on the published node (D1901, D1906)`.
- **Red step:** before the field exists the key lands in `UnknownKeys`; the test asserts it decodes into the field. **Tier:** standard.

### IS1: one resolver that returns the project image
- **Files:** `internal/infra/spec.go`, `spec_test.go`, `internal/catalog/select.go`, `select_test.go`, and the other callers of the selection: `internal/cli/run.go` (the launcher's D1149 judgment), `internal/cli/doctor_layer.go`, `internal/cli/image_catalog.go` (F21; they are writers at this level and appear in the shared-files table).
- **Interfaces:** consumes IS5's `ImageMap`; produces `jobImage(repo, workflow, policy)` that the `if` chain, `catalogJobImage`'s callers and the doctor call. Order: a repo's catalog reference, then `image_map`, then the default; `image:` block, `dockerfile:` and `rebuild:` are ignored with one warning each (reading **b** of T1; reading **a** also ignores the reference).
- **Tests:** `TestJobImageIgnoresRepoLevelConfig`, `TestJobImageUsesImageMap`, `TestJobImageNoDefaultFallsBack` (O4 b: with no catalog default the per-repo image is kept, so `update` converges), `TestUpdateConvergesNoCatalogDefault`, `TestJobImageCallersAgree`. **Command:** `go test ./internal/infra/ ./internal/catalog/ ./internal/cli/ -run 'JobImage|Applies|UpdateConvergesNoCatalogDefault' -count=1`. For `checkout: clone` the resolver returns the base image and for `checkout: shared` the shared-layer image, as `MayApply` already does; only `checkout: baked` workflows reach the catalog.
- **Depends on:** IS5. **Commit:** `infra: one resolver, always the project image; repo-level image keys warn and are ignored (D1900)`.
- **Red step:** a workflow with `image.setup` currently resolves to `gcp.ImageName(...)`; the test asserts the catalog default. **Tier:** standard.

### IS2: the repo-image commands and check job go quiet
- **Files:** `internal/infra/spec.go` (second writer, a later level than IS1), `internal/infra/repo.go`, `internal/infra/readiness.go` (the `imagecheck.RecordKey` readiness), `deploy/terraform/gcp/modules/repo/check.tf` (under `.claude/rules/terraform.md`), `internal/cli/imagecheck.go` (`fugaro image check --job`), `internal/cli/image_refresh.go`, `internal/cli/image.go`, `internal/cli/doctor_imageenv.go` if it reports the old path, their tests. Exact command names re-read first (U4).
- **Interfaces:** consumes IS1. A repo-level build or refresh answers "repo-level images are disabled; the project's catalog image is used" and exits 0 with nothing built; the per-repo check job is no longer emitted. Retirement of existing objects is **not** this task (nothing is deleted, D1900).
- **Tests:** `TestRepoImageRefreshIsDisabled`, `TestNoRepoCheckJobEmitted`. **Command:** `go test ./internal/cli/ ./internal/infra/ -run 'RepoImageRefreshIsDisabled|NoRepoCheckJob' -count=1`.
- **Depends on:** IS1. **Commit:** `cli, infra: repo-level image commands and check job disabled, code kept`.
- **Red step:** the current refresh builds; the test expects the notice. **Tier:** standard.

### IS4: the Claude-only base
- **Files:** `images/base/Dockerfile` (the harness stage and the four binary-harness ARG pins), `images/base/tools.tsv` (the seven `harness` rows), `images/base/harnesses/package.json` and `package-lock.json` (delete), `internal/image/tools.go`, `internal/image/selftest.go` (the smoke contract), `images/base_test.go`, `images/smoke_test.go`, `images/size-budget.sh`, `docs/base-image.md`, `docs/design/base-image.md`, the root `README.md` (names all seven), `.github/dependabot.yml` (the `images/base/harnesses/` entry; not a workflow file), `images/base_smoke_test.go`, `images/smoke.sh`, `internal/image/tools_test.go`, `selftest_test.go`, and the `HARNESS_NODE_VERSION` runtime in the Dockerfile if nothing else uses it.
- **Interfaces:** the contract list shrinks to Claude Code plus Fugaro's own components (F8). **Tests:** `TestBaseHasOnlyClaudeHarness` (the seven `harness` rows of F8) replaces the harness-entry assertions and the smoke contract's harness tools; `TestUpdateConvergesSlimBase` (an installation on the previous base converges through `update`); size budget lowers.
- **Command:** `go test ./images/ ./internal/image/ ./internal/cli/ -run 'Base|SizeBudget|Tools|Selftest|UpdateConvergesSlimBase' -count=1`; image build tests run behind their tags in CI.
- **Depends on:** none. **Commit:** `images: the base keeps the Claude harness only`.
- **Red step:** the test asserts the three entries are absent and fails while they exist. **Tier:** `opus + mandatory independent review` (the agent's environment). Rebuild reaches users through `fugaro update` (F15).

### IS11: validate prebuilt entries
- **Files:** `internal/backend/gcp/catalog.go`, `internal/cli/image_build_catalog.go`, tests.
- **Interfaces:** a `source: image` entry gets a smoke-only Cloud Build run of the runtime contract (F10); a failure is reported by `fugaro doctor`. **Tests:** `TestPrebuiltEntrySmoked`. **Command:** `go test ./internal/backend/gcp/ ./internal/cli/ -run PrebuiltEntrySmoked -count=1`.
- **Depends on:** none. **Commit:** `catalog: a prebuilt entry is smoked against the runtime contract`.
- **Red step:** a `source: image` entry is passed over by `image_build_catalog.go` and never smoked; the test asserts a smoke result for it. May also touch `images/derived/cloudbuild.yaml` (re-read; if so it is not in IS4's `images/base` set). **Tier:** standard.

### IS11b: refuse a secret-shaped Dockerfile
- **Files:** `internal/catalog/fetch.go` (the Dockerfile read), `fetch_test.go`, `internal/cli/catalog_copy.go` (the caller that gets the refusal). **Interfaces:** an inline or fetched Dockerfile with an `ARG`/`ENV` named like a credential, or a clone URL with userinfo, is refused with a message naming `--mount=type=secret`. **Tests:** `TestDockerfileSecretArgRefused`, `TestDockerfileUserinfoURLRefused`. **Command:** `go test ./internal/catalog/ -run 'DockerfileSecret|DockerfileUserinfo' -count=1`. **Depends on:** none (level 0). **Commit:** `catalog: refuse a Dockerfile that carries a credential`. **Red step:** both are accepted today. **Tier:** `opus + mandatory independent review`.

### IS7: the `setup` prompt
- **Files:** `internal/cli/setup.go`, `internal/cli/setup_wizard_test.go`, a no-prompt path for a managed project (the owner's rule: no prompts there; the value comes from a flag or the policy), `docs/setup-wizard/` page naming prompts if pinned.
- **Interfaces:** asks "Bake your repos into the Docker images? This makes jobs start faster." default yes, writes `bake_repos` through IS5; `fugaro settings` change followed by a rebuild notice. **Tests:** `TestSetupAsksBakeDefaultYes`, `TestSetupUpdateConvergesBake`. **Command:** `go test ./internal/cli/ -run 'SetupAsksBake|SetupUpdateConvergesBake' -count=1`.
- **Depends on:** IS5. **Commit:** `setup: ask whether to bake repos, default yes`.
- **Red step:** the wizard has no such prompt. **Tier:** standard.

### IS8: a baked checkout belongs to the job's repository
- **Files:** `internal/runner/runner.go` (`openCheckout`), `internal/gitops/gitops.go` if the origin read lives there, tests. (The presence logic already exists, F7.)
- **Interfaces:** a shared image holds each baked repo at a per-slug path; `openCheckout` opens only the path for the job's repo and **fails closed** when the checkout's origin is not the job's remote.
- **Tests:** `TestBakedCheckoutOriginMismatchRefuses`, `TestBakedCheckoutPerSlugPath`, `TestBakedMissingFallsBackToClone`. **Command:** `go test ./internal/runner/ -run 'BakedCheckout|BakedMissing' -count=1 -timeout 40m` (named tests only).
- **Depends on:** none. **Commit:** `runner: a baked checkout is used only for its own repository`.
- **Red step:** repo A's job with repo B's checkout at `WorkDir` is opened today. **Tier:** `opus + mandatory independent review`.

### IS8b: the bake step (O3 answered 2026-11-09: reading (b); **implemented, unmerged — draft PR #2171**)
- **Files:** `internal/imagebake/` (new: the rule and the generated stage), `internal/cli/image_build_catalog.go` (the decision and the note a build prints), `internal/cli/catalog_copy.go` (`writeCatalogDockerfile` composes the stage into the object it copies), `internal/cli/image_check_catalog.go` (the nightly check smokes the definition where it put the checkout), their tests.
- **Interfaces:** the build decides from the published node's own `image_map` and `bake_repos` — the resolver's order, so the repository's own catalog reference beats the map (T1 b) — and composes one engine stage onto the entry's definition. The stage bakes the **build's own** repository at `imagebake.Root`/`<slug>` (`/work/baked/<slug>`, IS8's per-slug path) and never at `/work/repo`, through the git-credentials secret mount the catalog build already makes: no new credential, no new build argument, no token in a layer (D1150, IS12). `Plan.Cloned` names the entry's other repositories, which clone at job start (O3 b). `bake_repos: false` bakes nothing, and anything else bakes: an absent switch and `true` are one statement, which is what the switch means everywhere else it is read (`projectpolicy.Policy.BakesRepos`, `localcfg.BakeReposEffective`), so one published node cannot tell a settings reader "on" and a build "nothing is baked". The bake does land in an entry the map puts other repositories on — that is F18's boundary, and it is the operator's own answer to F18, kept with `bake_repos: false` — and `Plan.Cloned` carries the repositories that can then read the checkout, so the line a build prints names them rather than leaving "bake your repos" to read like every repository of the project. A definition that bakes nothing is byte-for-byte the entry's own bytes, and a build of it says why (the repository does not run this entry, or the switch is off).

The stage is pinned to the composing repository: it refuses a `REPO_URL` that is not the one it was composed for, because the nightly check rebuilds an entry as whichever repository the spec assigns it to (`catalogCheckRepos`), and such a rebuild would otherwise clone one repository into another's slug path under the marker and the tag the composed definition gave it. The nightly check refuses the same thing one step earlier, before submitting a build, naming the entry, the baked checkout and the command that fixes it, and the assignment it rebuilds under resolves an inline entry's repository the way a build does — through the project's `image_map` (`internal/infra` `catalogCheckRepos`), so the definition and the account that rebuilds it name one repository. A clone URL that carries a credential, or that a generated definition cannot quote, is refused where the stage is generated. `BakedDir` reads the marker line back out of the object, which is what both smoke paths use instead of the `/work/repo` a hand-written definition bakes at. The composed bytes are bounded by `catalog.MaxDockerfileBytes` here as well, because that is the limit every reader of the object reads it with. The line a build prints claims what the image carries and where, never that a job reads it: no job does until IS8 lands.
- **Tests:** `TestDecideBakesTheBuildsOwnRepo`, `TestDecideClonesTheOtherReposAtStart`, `TestDecideBakesNothingForAnotherImage`, `TestDecideReferenceBeatsTheMap`, `TestDecideAnUnsetSwitchIsOn`, `TestDecideBakesASharedImageAndNamesTheReaders`, `TestDecideBakeSwitchOff`, `TestDecideRefusesASlugThatIsNotOne`, `TestStageClonesWithTheBuildsOwnCredential`, `TestStageRefusesAnotherRepositorysURL`, `TestBakedSlugIsTheMarkerRepository`, `TestBakedSlugIsTheLastMarker`, `TestAppendAndBakedDir`, `TestCatalogBuildBakesTheMappedReposCheckout`, `TestCatalogBuildBakesTheEntryTheCheckoutNames`, `TestCatalogBuildBakesNothingWithoutTheMap`, `TestCatalogBuildNamesTheReposThatCloneAtStart`, `TestCatalogCopyRefusesABakeThatLeavesTheBound`, `TestCatalogCheckJobSmokesABakedDefinitionWhereTheImagePutIt`, `TestCatalogCheckJobRefusesADefinitionBakedForAnotherRepository`, `TestUpdateConvergesImageBake`, and `internal/infra`'s `TestCatalogCheckAssignsAnInlineEntryToTheRepositoryThatRunsIt`. **Command:** `go test ./internal/imagebake/ -count=1` (that package is one file and takes a second, so it is run whole) then `go test ./internal/cli/ -run 'TestCatalogBuild|TestCatalogCopy|TestCatalogCheckJob|TestUpdateConvergesImageBake' -count=1` — about 10 s. The names are spelled whole: a bare word like `Stage` matches a hundred unrelated tests in `internal/cli`, whose package takes about 25 minutes and would time out.
- **Depends on:** IS5 (the policy fields it reads), O3. **Not on IS8:** the bake is written whether or not a reader opens it, and until IS8 lands the checkout is inert — no job reads `/work/baked/<slug>` yet, so nothing is faster and nothing is exposed. **Commit:** `imagebake: the generated Dockerfile clone a build appends (IS8b, O3 (b))`, `cli: a catalog build composes the bake stage into the definition (IS8b, O3 (b))`.
- **Red step:** a catalog build copies the entry's Dockerfile verbatim, so an entry the skill generates carries no checkout and its jobs always clone. **Tier:** `opus + mandatory independent review` (it writes a credential-carrying clone into definitions every job of an entry runs from).
- **Convergence:** the two inputs the bake is decided from reach an installation one release behind through `fugaro update` alone (`TestUpdateConvergesImageBake`, F15), and the image converges at the rebuild the catalog already has (D1148's schedule, O1) — `update` builds and copies no entry, so a bake arrives only at a build an operator runs.

### IS9: services on a catalog entry
- **Files:** `internal/config/catalog.go`, `internal/projectpolicy/policy.go` (`catalogEntryMembers`, the strict member list, F20), `internal/runner/runner.go` (a call site only if U7 shows none; **IS8 is a different level**), built on `images/common/fugaro-services` and its service names (F12; no new `services.go`), `internal/backend/launchenv.go`, `internal/infra/spec.go` is **not** touched: the services stamp is carried by the launch env, written always (empty when none) so an image's own `ENV` cannot forge it (the `ImageEntryEnv` precedent), tests.
- **Interfaces:** an entry gets `services: [postgres, redis, firebase-emulator]`; the workflow's existing `env:` carries the user's switch variable; the runner starts the listed services before tests (D1904). **Tests:** `TestServicesFromEntryOnly`, `TestServicesClosedAllowlist` (unknown names refused), `TestServicesStartWithAgentEnv` (the stripped env of `agentenv.go`, not the runner's credentialed one), `TestServicesStampAlwaysWritten`, `TestEntryServicesOlderReaderConverges` (an installation at the previous version converges through `update`) and `TestEntryServicesRunInFlightKeepsReading`: the strict member list makes an older reader fail on the whole policy, which a run in flight re-reads, so the entry key is carried **as a top-level policy key older readers list and tolerate** or behind a version step; if neither holds it goes back to the owner (HARD RULE). **Command:** `go test ./internal/config/ ./internal/runner/ ./internal/projectpolicy/ ./internal/backend/ ./internal/cli/ -run 'Services' -count=1`.
- **Depends on:** IS5. **Commit:** `catalog: an entry lists the services the runner starts`.
- **Red step:** `DisallowUnknownFields` refuses the member until it is listed. **Tier:** `opus + mandatory independent review`.

### IS10: `fugaro image survey`
- **Files:** `internal/cli/image_survey.go` (new), `internal/imagesurvey/` (new: signal rules), their tests, the managed-gate table file (name re-read) for the read-only row, `schemas/` for `--json`.
- **Interfaces:** consumes `gitprov.ContentsReader`; produces `--json`: repos with signals (lockfile, version files, Dockerfile, CI, docker-compose/Testcontainers hints) and the current catalog, map and bake setting. Read-only; no clone.
- **Rules:** every value is pattern-validated and printed through `pluginwire.Printable` (F22); fixed signal paths only; the repo list from the project's configured repositories (U6).
- **Tests:** `TestSurveyValuesAreValidated`, `TestSurveyReadsOnlySignals`, `TestSurveyBitbucketAndGitHub`, `TestSurveyGateRowReadOnly`. **Command:** `go test ./internal/cli/ ./internal/imagesurvey/ -run 'Survey' -count=1`.
- **Depends on:** none. **Commit:** `image: survey, a read-only report of a project's repos and signals`.
- **Red step:** the command does not exist. **Tier:** `opus + mandatory independent review`.

### IS12: the `fugaro:images` skill
- **Files:** `plugin/skills/setup/SKILL.md` (its per-repo local image build loop becomes the catalog path, F11), `plugin/skills/images/SKILL.md` (new), `plugin/images_skill_test.go` (new), `plugin/plugin_test.go` (`TestSkillSetIsFive` becomes six), `plugin/readme_test.go`, the repository's root `README.md` and any "five skills" docs.
- **Interfaces:** flow of the spec: survey (IS10), propose under five images with a mapping and drift, base-or-BYO per image, apply bake (IS5), generate Dockerfiles with secret mounts, validate through the cloud catalog build and smoke (IS11), never local; reruns propose only differences (F16).
- **Tests:** `TestImagesSkillNamesRealCommands`, `TestImagesSkillNeverLocalBuild`, `TestImagesSkillNoBuildArgSecrets`, `TestImagesSkillRerunProposesDiffOnly`. **Command:** `go test ./plugin/ -run 'ImagesSkill|Readme' -count=1`.
- **Depends on:** IS1, IS5, IS7, IS9, IS10, IS11, IS11b. **Commit:** `plugin: the fugaro:images skill`.
- **Honest limit:** the skill tests pin the skill's *text* (commands named, no local build, secret mounts, diff-only rerun, at most four images); behaviour is checked by Check 500. A Dockerfile with a token `ARG` or a userinfo clone URL is refused by code in the catalog's Dockerfile copy step (`internal/cli/catalog_copy.go` through `catalog.Fetch`, IS11b above; `image add` itself never fetches), not only by the skill's words.
- **Red step:** the skill and its tests do not exist. **Tier:** `opus + mandatory independent review` (a plugin skill).

### IS13: docs, release notes, operator line
- **Files:** `scripts/check-500.sh`, `scripts/check-501.sh` (new), `docs/releases/vX.Y.Z.md` (new; version at cut), `website/data/release-summaries.yaml`, `docs/image-setup-skill/README.md`, `docs/gcp-live-checklist.md` (Checks 500, 501).
- **Operators:** `### For operators`: "run `fugaro update`" and what it asks (a rebuild of the base-derived images behind its own confirmation).
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`; `npm run check` in `website/` if content changed. **Depends on:** IS1–IS12. **Commit:** `docs: release notes and live checks for the image setup skill`.
- **Red step:** the docs-guard test pins the new check numbers. **Tier:** standard.

## Parallel execution

**Edges** (from Depends on): IS5→IS1, IS5→IS7, IS5→IS9, IS1→IS2, {IS1, IS5, IS7, IS9, IS10, IS11, IS11b}→IS12, all→IS13.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | IS3, IS4, IS5, IS8, IS10, IS11, IS11b |
| 1 | IS1, IS7, IS9 (IS8b joins when O3 is answered) |
| 2 | IS2, IS12 |
| 3 | IS13 |

**Critical path:** IS5 → IS1 → IS2 → IS13 (also IS5 → IS9 → IS12 → IS13). **Sequential because:** IS1 reads IS5's new field; IS2 and IS1 both write `internal/infra/spec.go`; IS12 names commands and keys from IS1/IS9/IS10/IS11.

### Shared files (no level holds two writers)

| File | Writers (level) |
| --- | --- |
| `internal/projectpolicy/policy.go` | IS5 (0), IS9 (1) |
| `internal/infra/spec.go` | IS1 (1), IS2 (2) |
| `internal/cli/setup.go` | IS7 (1) |
| `internal/cli/image_refresh.go`, `image.go` | IS2 (2) |
| `internal/backend/gcp/catalog.go`, `internal/cli/image_build_catalog.go`, `images/derived/cloudbuild.yaml` (if touched) | IS11 (0) |
| `internal/config/catalog.go` | IS9 (1) |
| `internal/runner/runner.go` | IS8 (0), IS9 (1) |
| `internal/runner/services.go` | IS9 (1) |
| `internal/cli/run.go`, `doctor_layer.go`, `image_catalog.go` | IS1 (1) |
| `internal/localcfg/localcfg.go`, `internal/settings/*`, `init_policy_publish.go` | IS5 (0) |
| `internal/catalog/fetch.go` | IS11b (0) |
| `internal/image/tools.go`, `selftest.go`, `images/base/tools.tsv`, `images/smoke_test.go` | IS4 (0) |
| `plugin/**`, root `README.md` | IS12 (2) |
| `docs/releases/*`, `website/data/release-summaries.yaml` | IS13 (3) |
| `images/base/**`, `images/smoke.sh`, `images/*_test.go`, `docs/base-image.md`, root `README.md`, `.github/dependabot.yml` | IS4 (0) |
| per-command gate files (e.g. `internal/cli/image_build_managed.go`) | IS10 (0, its own); IS5 (0, the map command's own) |
| `deploy/terraform/gcp/modules/repo/check.tf`, `internal/infra/repo.go`, `readiness.go`, `internal/cli/imagecheck.go` | IS2 (2) |

## Checks (reserved 500–509)

- **Both checks ship as one script each** (`scripts/check-500.sh`, `check-501.sh`, written by IS13), so the owner types one command.
- **Check 500 (OWNER-RUN):** on the sandbox project, the owner runs the skill end to end, builds the proposed images with `fugaro image build`, launches one run on a baked image, and confirms the run and the warning on a repo with a stale `image:` block. No agent performs any step.
- **Check 501 (OWNER-RUN):** after `fugaro update`, a run on the slim base reaches `result.json`; the owner types `fugaro update` then `fugaro run` on the sandbox.
- Nothing runs against the dev installation `belong` without the owner's own go.

## Considered and declined

Removal (R2–R10) in this release: parked, not cancelled (D1900). Catalog validation of the cap: declined, D1903. One unified image: out of scope in the spec.
