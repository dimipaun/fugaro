# Implementation plan: removing the per-repository derived images

**Status: plan only; nothing is implemented; no task approved.** Eleven tasks, **R0–R10**, each with its
files, its interface, its **red step**, its named tests and its **SEC** flag. Every task is written
against the tree of 2026-11-04 (`aa034db1`) and every citation in it was resolved while writing.
**Nothing here starts until the owner answers the decisions in [reconciliation.md](reconciliation.md)
§4** — each task names the decisions it implements, so an unanswered decision is a task that does not
run.

## 1. What this plan does not own

- **No decision.** A task implements a decision; a task whose decision changed changes with it, and a
  task that meets a question the decisions do not answer **reports it** rather than deciding it in code
  (`CLAUDE.md`: a fix round adds no functionality).
- **The image catalog's own build.** `fugaro image add`, `image build <entry>`, `image ls`, `image rm`
  and the catalog's own Terraform are [docs/project-image-catalog](../project-image-catalog/README.md)'s
  (PIC1–PIC7 the tasks, **PIC4** the build). This plan **removes** the other path and **uses** that one.
- **D1148's two owed tasks** (the installation-level scheduled check and its driver). They change the
  same four files this plan's R4 and R6 change; §4 states the rule and §2 the order.
- **The managed build's M0/M6 and the managed-mode tasks M0–M9.** R9 writes the gate's **rows**; the
  gate's code is `docs/managed-agent-converge/implementation-plan.md`'s.
- **`.github/workflows/`.** No task creates, edits or deletes a workflow file.
- **A new dependency.** `go.mod` is unchanged by every task.
- **The ceiling of twelve files is per commit.** Every task lands as commits of at most twelve files,
  which is what review sees. One task — **R3**, the fixture sweep — is one subject whose files are the
  **50** `testdata/` fixtures the removal invalidates (`reconciliation.md` D1197), so it lands as four
  commits, one per directory, and not as four tasks: splitting it would give four tasks one red step and
  four chances to leave the tree half-migrated.

## 2. Order, and what forces it

**The dogfood comes first; the removal comes after D1148; the tag waits for the owner's live check.**

| Order | Tasks | What forces it |
| --- | --- | --- |
| 0 | **the catalog ships** (PR #1665 flips `imageCatalogShipped` at `internal/cli/recipes_skew.go:177` and `internal/infra/readiness.go:755`) | Until it does, a workflow naming an entry is held back by readiness rather than run (`internal/infra/readiness.go:459`) — so R0 could land and the dogfood would stop launching. **R0 waits on this and on nothing else** |
| 1 | **R0** | D1193: the entry, its build and the pin, before any removal. Nothing else may land first: every later task's red step runs against this repository's own pipeline |
| 2 | **D1148's two tasks** | They add the installation-level schedule and its driver to `internal/infra/spec.go`, `internal/infra/tf/cover.go` and `internal/imagecheck/` — the files R4 and R6 change. Removing first would make D1148's driver read a check this plan deletes |
| 3 | **R1**, **R2**, **R3** | R1 (the `baked` rule) and R2 (the refusals) are the configuration's two halves and neither depends on the other; R3 is R2's fixtures — **R2 first**, because a red step cannot assert a message that is not written |
| 4 | **R4** | R2: the deploy path stops producing a name the configuration no longer defines. **SEC.** R5 rides on it — readiness has nothing to probe once R4 stops deploying |
| 5 | **R5** | R4. The catalog branch of readiness is already written (`internal/infra/readiness.go:449`); R5 turns the repository branch off |
| 6 | **R6**, **R7** | R2 (the keys the commands read) and R4 (the deploy path they submit into). R7 removes `promote`, which R0's migration ran with — safe only because R0 is step 1 and the owner's half is done |
| 7 | **R8**, **R9** | R6 and R7: the published surfaces name commands, and a surface moved before its command is a document describing a tree that does not exist yet. **R9 is last of the code**, because a gate row is a claim about a command's final behaviour |
| 8 | **R10** | Every task above: the check it registers measures the whole removal, on the sandbox, before the tag |

**The signature of the order** — the plan's own reading, and what a reader is owed when a task moves:

**catalog ships → R0 → D1148 (both tasks) → R1 → R2 → R3 → R4 → R5 → R6 → R7 → R8 → R9 → R10.**

**What D1148 owes this plan, and what this plan owes D1148.** Two constraints travel with the order and
are the reason it is not a preference:

- **A catalog entry whose source is a Dockerfile in repository X is built as X's build account**
  (`reconciliation.md` D1202, `internal/cli/image_build_catalog.go:366` rejects any other). The
  installation-level schedule D1148 adds must therefore build each entry as **its source repository's**
  account — one schedule, many accounts — and a repository that is retired takes its entries' ability to
  rebuild with it. R4 narrows the build account's grants with this in mind and must not remove the
  catalog grants D1148's schedule runs on.
- **R4 removes resource types from a module and D1148 adds one at installation level.** Both edit
  `internal/infra/tf/cover.go:22`'s `allowedTypes` — R4 to drop the check job's and the Scheduler's
  types, D1148 to add its own. **The table is never widened to make a plan pass**; each task adds or
  removes exactly the types its own `plan` prints.

## 3. Gates (every task)

- `go build ./... && gofmt -l . && go vet ./...` — gofmt prints nothing.
- The task's named tests: `GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1 -timeout 40m`.
  **Never the whole `internal/cli` or `internal/runner` package.**
- The **wider pin gate**: for every file the task changes, find the tests that name it
  (`grep -rln '<base name>' --include='*_test.go' internal/ cmd/`) and run each with a `-run` filter.
- A task that touches Terraform: `terraform fmt -check -recursive deploy/terraform`, then `init
  -backend=false -lockfile=readonly` + `validate` + `terraform test` in **every root the module reaches**
  (`installation` and `repo` for R4), then `go test -tags terraform ./internal/infra/...`, with the
  golden plans regenerated by `scripts/gen-golden-plans.sh` and never by hand.
- A doc touched: `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m`.
- **A test this plan adds that fails locally stops the task**: never push over it, never weaken, skip or
  delete it. Report it.
- **SEC** marks the surfaces the brief names: a Terraform deployment, a retirement, the managed gate and
  the build accounts. A **SEC** task's reviewer is a second pair of eyes, and its refusals are read line
  by line rather than tested only.

## 4. The shared-files table

Files more than one task touches, and the rule for each. **A task that needs a file another task owns
adds its edit only after that task has merged**, and never re-derives what the owner landed.

| File | Tasks | What each one does with it |
| --- | --- | --- |
| `internal/infra/spec.go` | **D1148's driver**, R4, R5, R6 | D1148's driver adds the installation-level check's spec; R4 removes the derived image name and the per-repository `CheckSpec` producer (`internal/infra/spec.go:1440`); R5 leaves the catalog branch of the image choice alone; R6 reads what R4 left. **D1148 first, then R4** — R4 deletes the producer D1148's check replaces, and a driver written against the old producer is a driver rewritten twice |
| `deploy/terraform/gcp/modules/repo/` | **D1148's Terraform**, R4 | D1148 adds nothing here (its schedule is installation level) but reads the module's registry and build account; R4 removes `check.tf`'s job and Scheduler, the registry repository's role or the account's grants that only a repository image needed. **D1148's task merges first**, so R4 removes from a module D1148 has stopped depending on |
| `deploy/terraform/gcp/modules/installation/` | **D1148's Terraform**, R4 | D1148 adds the Scheduler job and its target; R4 touches the shared registry's grants only. **D1148 first** |
| `internal/infra/tf/cover.go` (its `allowedTypes` at `internal/infra/tf/cover.go:22`, and `ManagedAllowedTypes` at `internal/infra/tf/managed_cover.go:124`) | **D1148's Terraform**, R4 | D1148 adds its types, R4 removes the check job's and the Scheduler's. Each edits the list for its own plan and **neither widens it to make a test pass** |
| `internal/imagecheck/` (`internal/imagecheck/catalog.go:20`, the per-repository check) | **D1148's driver**, R6 | D1148's driver calls `CheckCatalog` over every buildable entry; R6 removes the per-repository triggers' readers (`rebuild.*`) and `checkedWorkflows` (`internal/cli/imagecheck.go:221`). **D1148 first** |
| `internal/settings/registry.go` | **G0/G1**, R2 | The configuration-management plan's G0 and G1 add rows and widen the generic writer's allowlist; R2 removes the twenty-one rows of the removed keys (`internal/settings/registry.go:229`, `:454`, `:477`). **One row, one owner**: R2 touches only the removed keys' rows and never the writer |
| `schemas/settings.schema.json` | **G2**, **G8**, **G9**, R2 | They add member rows for the writes they add; R2 removes the members of the keys it removes (`schemas/settings.schema.json:1130`, `:1181`). **R2 does not move the document's version** — the rule G2 states |
| `internal/cli/image_refresh*.go` | **M-series** (`image_refresh_steps.go`, `image_refresh_rules.go`), R6 | The managed build's M-series changes what `image refresh` converges; R6 removes its derived-image arm. **The M-series lands first**, or R6 rewrites lines it is about to be handed back |
| `internal/cli/init.go`, `internal/cli/update.go` | **M-series** (`update.go`, `init.go`), R7 | R7 changes `setup`'s first-build offer (`offerBuilds`, `internal/cli/init.go:3415`) and the retirement door is left alone (`:444`); the M-series adds the converge stages. **Different functions, one file, M-series first** |
| `docs/rigor-modes/reconciliation.md` | none after the design | The census row this folder moves is moved **in the design's own commit** (`docs_repo_image_removal_test.go`'s own pin moves it, 93 → 94) |
| `docs/gcp-live-checklist.md` | **R10 only** | The file is the owner's register; a coding agent's session does not edit it. **R10 is the one task this plan lifts that rule for**, it adds a register row and one section and edits nothing else, and no other task touches the file |

## 5. What the plan owes the gate, and what the gate owes the plan

- **The rows R9 writes are [spec.md](spec.md) §6's**, and `--retire` is not among them: a retirement
  deletes, the gate's first rule is no deletes, and **a Typed step is never admitted (D1026)** — so the
  retirement has no gate row to write, which R9 states rather than leaves to be discovered.
- **R6's and R7's commands call the gate where the gate admits them** (M0 in
  `docs/managed-agent-converge/implementation-plan.md`). A command whose gate call has not landed stays
  agent-refused, which is the safe direction, and R9 **reports** the missing call rather than inventing
  a predicate.
- **R4's Terraform is not gate territory**: `fugaro update`'s converge confirmation covers it through
  `internal/infra/tf/cover.go`, which R4 edits on purpose and for the removal's own resource types.

## 6. Testing, and the two properties that need a test of their own

Beyond each task's red step, two properties are cross-cutting and neither belongs to one task:

- **The dogfood property.** After R0, **no workflow of this repository or of the sandbox repository
  names a per-repository image**, and no task of this plan may land while one does. A pin on the
  repository's own `fugaro.yaml` (`TestDogfoodWorkflowPinsTheCatalogEntry`, R0) is what makes the
  property mechanical for this repository; the sandbox's half is the owner's, and R10's check reads it
  rather than assuming it.
- **The one-image-path property.** After R9 there is **no** command, key or code path that builds an
  image for one repository — the test for it is a **tree check**, not a behaviour: the removed
  identifiers (`ImageName`'s derived call site, `catalog.Applies`'s two keys, the `rebuild.*` readers,
  `image promote`) appear in no non-test file, and the two `imageCatalogShipped` constants
  (`internal/cli/recipes_skew.go:177`, `internal/infra/readiness.go:755`) are both true. It lands with
  R9, because R9 is the last code task, and it is the only test whose failure means "the removal is not
  finished" rather than "this change is wrong".

## 7. The recommended slot

**0.18.0 — the same release as D1148's two tasks and the release that flips `imageCatalogShipped`.**
That is not a convenience: the removal's whole premise is that the catalog **is** the deployed image
path, and shipping the removal in a release whose catalog the launcher does not yet read would leave the
installation with the refusal in `internal/infra/readiness.go:459` and no image.

**The tag waits for R10.** The registry, the build account, the job and the run are one artefact and the
hermetic fakes are written from the API documentation, so a green suite cannot show that a job built
from a catalog entry **runs**. The owner runs R10's check on the sandbox, confirms one run, and the tag
follows.

A later slot costs the one thing this design is bought for: **the two image paths are the reason
`catalog.Applies` exists** (`internal/catalog/select.go:318`), and every release between this one and
that one keeps a per-repository build input, a per-repository scheduled job and the two pairs of
readiness branches that decide between them.

## R0. The dogfood repository's entry, its build and its pin (D1193, D1208, D1191)

- **Interface.** A Dockerfile **in this repository** — `images/catalog/go/Dockerfile` — that takes
  `FROM $FUGARO_BASE`, clones `$REPO_URL` at `$FUGARO_COMMIT` with the `git-credentials` secret the build
  mounts (`images/derived/cloudbuild.yaml:178`), runs today's `image.setup` verbatim
  (`go mod download && go build ./...`), and **ends with** `finalize-checkout`
  (`images/derived/Dockerfile.tmpl:130`). A catalog entry in the project layer whose source is that
  Dockerfile at that path, published and **built by the owner before the pin lands**: `fugaro image add
  --dockerfile images/catalog/go/Dockerfile --tags go`, `fugaro image build <entry>`; then the `go`
  workflow drops its `image:` block (`./fugaro.yaml:43`) and sets `image_name: <entry>`.
- **Red step.** `TestDogfoodWorkflowPinsTheCatalogEntry` — reads the repository's own `fugaro.yaml` and
  fails today: the `go` workflow carries `image:` and no `image_name`. Its second half reads the
  Dockerfile the entry names and fails if it does not end with `finalize-checkout`, which is the one
  step a hand-written Dockerfile can omit without any other symptom (D1208). **The owner's half runs
  before the pin commit**, and the task's own note records the run that confirmed it.
- **Files (≤5).** `images/catalog/go/Dockerfile` (new), `fugaro.yaml`, the project layer's catalog file,
  the new test, and `docs/dogfooding.md`'s one sentence about how the dogfood image is built.
- **Order note.** R0 lands **after the catalog ships** (§2 step 0): with `imageCatalogShipped` false,
  readiness holds the job back rather than running it (`internal/infra/readiness.go:459`).

## R1. `checkout: baked` keeps both values, and a prebuilt entry is refused on one (D1191)

- **Interface.** `catalog.Applies` (`internal/catalog/select.go:318`) stops reading `image:`/`dockerfile:`
  as "not a catalog workflow" — those keys no longer exist — and keeps its other refusals. The new
  refusal lives where the spec is built: a workflow whose `checkout` is `baked`
  (`internal/config/config.go:657`) and whose chosen entry's source is a prebuilt image
  (`SourceImage`, `internal/config/catalog.go:71`) is refused with a sentence naming the entry, the
  source kind and the two ways out (`checkout: clone`, or an entry that is built). `checkout`'s two
  values and both their meanings are unchanged.
- **Red step.** `TestBakedWorkflowRefusesAPrebuiltCatalogEntry` — a fixture project with a
  `checkout: baked` workflow pinned to an `image`-sourced entry: today it selects the entry and deploys
  a job whose first minute fails (F14: the image is written into the job at deploy time); after R1 the
  spec build refuses it by name. `TestCloneWorkflowAcceptsAPrebuiltCatalogEntry` is the control, so the
  refusal is not simply "prebuilt entries are refused".
- **Files (≤4).** `internal/catalog/select.go`, `internal/infra/spec.go`, their two test files.

## R2. The configuration: tombstones, the scope rows, the registry rows and the three schemas (D1195, D1196)

- **Interface.** Each removed key becomes a tombstone that refuses by name and names the catalog command
  — the mechanism `image.node`/`image.jdk` already are (`internal/config/image.go:119`, `:122`), whose
  sentence is built from `MigrationDoc`. `internal/config/scope.go:106` loses the eleven rows of the
  removed keys (`:108`, `:113`, `:131`, `:179`, `:189`), `internal/settings/registry.go:229` its
  twenty-one (`:454`, `:477`), and the three schemas their members
  (`schemas/fugaro.schema.json:358`, `:396`, `:511`; `schemas/project-layer.schema.json:265`, `:338`;
  `schemas/settings.schema.json:1130`, `:1181`).
  `image_name`, `needs` and `image_catalog.*` keep every member they have, and no version moves.
- **Red step.** `TestRemovedImageKeysAreRefusedByName` — a table of the removed paths, each asserted to
  fail with a message naming `fugaro image add`: today each fails, or is accepted, with the decoder's
  own words. `TestRemovedImageKeysLeaveTheScopeTable` and
  `TestRemovedImageKeysLeaveTheSettingsRegistry` are the two the surface can lie without: a scope row
  and a registry row are what make a key *settable* after the config stopped accepting it.
- **Files (≤8).** `internal/config/config.go`, `internal/config/image.go`, `internal/config/scope.go`,
  `internal/settings/registry.go`, the three schemas, and the tests.
- **Order note.** R2 is the task that makes the removal **observable**: after it, a repository still
  carrying `image:` is refused at validation, so R0 has to have landed (§2).

## R3. The fixtures and the golden (D1197)

- **Interface.** The **50** files under `testdata/` carrying one of the three keys
  (`grep -rlE '^[[:space:]]*(image|dockerfile|rebuild):' testdata/`) move: the *valid* ones lose the
  keys (and the workflows they describe become catalog workflows), the *invalid* ones that assert an old
  refusal — `image-and-dockerfile.yaml`, the `legacy-image-*` pair, the `checkout-clone-*` set — become
  fixtures asserting the **new** refusal, and `internal/cli/testdata/init_retire.golden` is regenerated.
- **Red step.** R2's refusal message is the red step; this task is what proves the **fixtures** agree
  with it: `go test ./internal/config/ -run 'TestLoad|TestValidate' -count=1` fails on every
  fixture R2 did not sweep, and the golden test fails until it is regenerated.
- **Files (50, in four commits of ≤12).** `testdata/config/valid/`, `testdata/config/invalid/`,
  `testdata/project-layer/valid/`, `testdata/project-layer/invalid/`, one commit each, plus the golden.
  **One subject, four commits** (§1).

## R4. The deploy path: the derived name, the check job, the Scheduler and the registry. **SEC**

- **Interface.** `repoCtx.check` (`internal/infra/spec.go:1440`) and the `CheckSpec` it produces
  (`:318`) go, with `deploy/terraform/gcp/modules/repo/check.tf:16`'s job (`:116` is its Scheduler) and
  their `allowedTypes` entries. The derived image name (`internal/infra/spec.go:1011`) and
  `registryPath` (`internal/infra/spec.go:749`) go from the job's image; `catalogJobImage` (`:799`)
  becomes the only producer.
  The repository's Artifact Registry repository stays **in the configuration** and its object is
  destroyed only by the owner's `--retire` run (D1194) — so `deletion_protection`
  (`deploy/terraform/gcp/modules/repo/check.tf:27`) and `prevent_destroy`
  (`deploy/terraform/gcp/modules/repo/registry.tf:10`) are **not** touched. The build account
  (`deploy/terraform/gcp/modules/repo/build.tf:5`) stays, with its grants narrowed to the shared
  registry, the base registry and ghcr, and **the catalog grants D1148's schedule runs on are kept**
  (§2).
- **Red step.** `TestDerivedImageNameIsGoneFromTheSpec` — the spec of a repository whose workflow names
  an entry carries the shared registry's name and nothing of the repository's own; today it carries
  `gcp.ImageName(c.registryPath(), slug, name) + ":latest"`. Then the Terraform half:
  `scripts/gen-golden-plans.sh` regenerates the goldens, and the plan shows the check job and the
  Scheduler leaving and **nothing else**.
- **Files (≤9).** `internal/infra/spec.go`, `deploy/terraform/gcp/modules/repo/{check,registry,build}.tf`
  and `outputs.tf`, `internal/infra/tf/cover.go`, the spec test, and the regenerated goldens.
- **SEC.** It is a Terraform deployment: an independent reviewer reads the plan's destroy list line by
  line, and `terraform plan` on the `installation` and `repo` roots is the evidence — never a widened
  cover table.

## R5. Readiness asks the shared registry, not the repository's (D1203)

- **Interface.** `hasLatest` (`internal/infra/readiness.go:723`) and the per-workflow branch at `:445`
  stop asking the repository's registry for a workflow that names an entry, leaving the catalog branch
  (`:449`) as the only image probe; `MissingImage` (`:30`) keeps its meaning and its kind, and the
  `imageCatalogShipped` refusal at `:459` goes with the flag's flip (it is R9's tree check that asserts
  both constants are true).
- **Red step.** `TestReadinessReadsTheCatalogNotTheRepositoryRegistry` — a workflow pinned to a built
  entry is ready with the repository's own registry empty, which today is only true on the catalog
  branch; its control asserts that an entry with no `:latest` is still refused, with the sentence naming
  `fugaro image build <entry>` (the message that exists today).
- **Files (≤3).** `internal/infra/readiness.go` and its two test files.

## R6. The commands that build, refresh and check a repository's image (D1190, D1199, D1200)

- **Interface.** `fugaro image build` (`internal/cli/image.go:78`) keeps its catalog arm
  (`fugaro image build <entry>`, `internal/cli/image_build_catalog.go`) and loses the derived one — the
  `--local` arm, the render step and the repository-registry submit. `fugaro image render`
  (`internal/cli/image.go:513`) goes, `fugaro image check` (`internal/cli/imagecheck.go:64`) keeps the
  catalog pass and loses the per-repository one, `checkedWorkflows` (`:221`) goes, and `fugaro image
  status` (`:952`) reports catalog entries instead of per-workflow derived images. `image refresh`'s
  derived steps (`internal/cli/image_refresh_steps.go`) go. **`rebuild.*` is read by nothing after
  this** (D1200) — the trigger set is `internal/imagecheck/catalog.go`'s and D1148's.
- **Red step.** `TestImageRefreshNoDerivedSteps` and `TestImageCheckChecksOnlyCatalogEntries` — the two
  commands' own lists, asserted to name no repository image. `TestImageRenderIsGone` asserts the command
  is not registered, so a half-removed command cannot be reached by its old name.
- **Files (≤12).** `internal/cli/image.go`, `internal/cli/imagecheck.go`, `internal/cli/image_refresh.go`,
  `internal/cli/image_refresh_steps.go`, `internal/cli/image_refresh_rules.go`, and the tests that name
  them.

## R7. `promote` goes, the refusal names the forward path, and `setup` offers a catalog build (D1192, D1207)

- **Interface.** `fugaro image promote` (`internal/cli/image_promote.go:19`) and
  `setWorkflowImageName` (`:179`) go: after R2 there is no definition for them to read. The refusal R2
  writes names the forward path (`fugaro image add --dockerfile … --tags …` plus `image_name`, or
  `--inline` with the block the old key held). `setup`'s first-build offer
  (`offerBuilds`, `internal/cli/init.go:3415`) offers a catalog entry's build in place of the derived
  one; `--no-build` is unchanged, and the retirement door (`:444`) is untouched.
- **Red step.** `TestImagePromoteIsGone`, plus `TestSetupOffersACatalogBuild` — the offer's own text,
  asserted to name `fugaro image build <entry>` and not a workflow image.
- **Files (≤6).** `internal/cli/image_promote.go` (deleted), `internal/cli/image.go`,
  `internal/cli/init.go`, the promote test files, and the setup-offer test.
- **Order note.** R7 may land only after the owner's half of R0 is done and any other migrated
  repository has run promote — §2 step 6.

## R8. The writers and the published surfaces (D1204, D1205)

- **Interface.** `plugin/skills/setup/SKILL.md:84` teaches `fugaro image add` and `image_name` in place
  of the removed keys (`:91`), with the reference documents and the plugin's two pins moving with
  it (`plugin/setup_skill_test.go:154`, `plugin/skills_lint_test.go:789`). The console's never-reachable
  table drops the removed commands (`console/src/main/ipc.ts:1764`, with
  `console/test/unit/ipc.test.ts:681`), and the generated CLI reference is regenerated
  (`ee/clidoc/cliref_test.go:26`, `internal/cli/docs_website_cliref_test.go:186`).
- **Red step.** `go test ./plugin/ -run 'TestSetupSkill|TestSkills' -count=1` — the skill's examples are
  validated against the CLI, so a skill still teaching `image:` fails once R2 has landed; the console's
  classification test fails next; and `TestCliReferenceIsCurrent` fails until it is regenerated.
- **Files (≤10).** `plugin/skills/setup/SKILL.md` and its reference files, the plugin's two test files,
  `console/src/main/ipc.ts`, `console/test/unit/ipc.test.ts`, `website/content/cli-reference.md`, and the
  website's own pin if it names a removed command.
- **Note.** The console and the website are their own packages: `npm run typecheck`, `npm run test:unit`
  in `console/` and `npm run check` in `website/` are this task's gate, not the Go suite.

## R9. The managed gate's rows, and the removal's tree check (D1206). **SEC**

- **Interface.** The rows of [spec.md](spec.md) §6 in
  `docs/managed-agent-converge/spec.md` §3.1: `fugaro image refresh`'s row re-written to what it still
  does, an `fugaro image build` row for the catalog arm and the D1013 bound
  (`internal/managed/builds.go:39`, which counts the builds one invocation may start and is called
  rather than restated — no task here copies it), no row for a command that no longer exists, and
  **`--retire` admitted nowhere** — a retirement deletes and a Typed step is never admitted (D1026). The
  removal's tree check lands here (§6): the removed identifiers appear in no non-test file, and both
  `imageCatalogShipped` constants (`internal/cli/recipes_skew.go:177`,
  `internal/infra/readiness.go:755`) are true.
- **Red step.** `TestGateAdmitsNoRetirementAndNoRemovedCommand` — the gate's command set, read out of
  its own table, asserted to name none of the removed commands and no retirement; and
  `TestNoPerRepositoryImagePathRemains` — the tree check, which fails today and is the task's own
  definition of done. **The folder has no pin test of its own today** (nothing under `internal/` reads
  `docs/managed-agent-converge/`), so this task's test is the pin it adds, and the row set it writes is
  what that test holds.
- **Files (≤6).** `docs/managed-agent-converge/spec.md`, the new pin test, and the new tree-check test
  file.
- **SEC.** It is the gate: an independent reviewer reads the row set and the tree check together, and
  neither widens a covered set to make a test pass.

## R10. The owner-run live check, and the 0.18.0 tag (the final task)

- **Interface.** A new registered check in `docs/gcp-live-checklist.md` — **Check 191**, taken above the
  register's highest written section (190) at merge, as the register's own rule requires — plus its row
  in the register's table. The check is the brief's safeguard and the sandbox's migration in one
  procedure, all of it owner-run:

  1. the guard block, run at the owner's terminal, with the window's project and repository named;
  2. **the entry**: `fugaro image add --dockerfile <path in the sandbox repository> --tags <tags>` on
     the sandbox installation, then `fugaro config publish` so the node carries it (D1140's publish);
  3. **the build**: `fugaro image build <entry>`, confirming the build account ran it (D1202's coupling)
     and that the shared registry holds `:latest` and the definition's content tag;
  4. **the pin**: the sandbox repository's workflow drops `image:`/`dockerfile:` and names the entry
     (or `needs:`), with one commit;
  5. **the run**: one ordinary task through `fugaro run --repo <sandbox>`, confirming the job's image is
     the shared registry's entry — read out of `fugaro doctor` or the run record, **not** out of the
     repository's own registry — and that the run completes with a draft pull request;
  6. **the read-backs**: the per-repository check job and its Scheduler are gone from
     `gcloud scheduler jobs list` / the job list for that repository, the repository's own registry
     still exists with its `deletion_protection` intact (**not deleted by this check** — retirement is
     its own owner-run act, D1194), and the installation's shared registry is untouched otherwise;
  7. **the undo**: the pin's commit reverted and the run's branch deleted, the entry left in the catalog
     (it is what the sandbox runs from now on), and the check's own dollars recorded.

- **Red step.** The register row and the section are the deliverable; `registerRow`-shaped pins
  (`internal/cli/docs_check96_test.go:221`) and the ordering pin that a check is written above the
  highest written section (`internal/cli/docs_checks_145_149_test.go:179`) fail until both are written,
  and `website/test/checklist.mjs` reads the file's sections. Nothing here is `go test`-provable beyond
  that: **the check's own result is the owner's**, and the 0.18.0 tag follows it (§7).
- **Files (≤3).** `docs/gcp-live-checklist.md`, the new pin test, and the release notes' `### For
  operators` line for the release it ships in.
- **Note.** This is the one task this plan lifts the "no agent edits the checklist" rule for, and it
  edits nothing else: **no agent runs any step of the check.**
