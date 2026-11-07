# Implementation plan: removing the per-repository derived images

**Status: plan only; nothing is implemented; no task approved.** Eleven tasks, **R0–R10**, each with its
files, its interface, its **red step**, its named tests and its **SEC** flag. Every task is written
against the tree of 2026-11-04 (`777b902d`, the merge of `main` this round read) and every citation in
it was resolved while writing. **The owner answered the decisions in
[reconciliation.md](reconciliation.md) §4 on 2026-11-04**, so each task's block is settled and none of
them waits on a question; what is still owed is the owner's **approval of the plan itself**, and the one
question its residual raises — whether the removal's release may ship the per-repository `--retire` as
an operator step ([reconciliation.md](reconciliation.md) §5). **No task lands before that is picked**,
because the answer decides whether the removal release needs the retirement folded into `update`'s own
converge, and that is a change to a task rather than a detail inside it.

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
  `testdata/` fixtures the removal invalidates (`reconciliation.md` D1197), so it lands as **four**
  commits of at most twelve files and not as four tasks: splitting it would give four tasks one red step
  and four chances to leave the tree half-migrated.

## 2. Order, and what forces it

**The dogfood comes first; the removal comes after D1148; the tag waits for the owner's live check.**

| Order | Tasks | What forces it |
| --- | --- | --- |
| 0 | **the catalog ships — done, and it is the one step that had a date outside the plan.** Both `imageCatalogShipped` constants are `true` on `main` (`internal/cli/recipes_skew.go:176`, `internal/infra/readiness.go:762`; #1665 merged), the flip being 0.18.0's | While the flag was false, readiness held a job whose image is a catalog entry back rather than running it (`internal/infra/readiness.go:462`), so R0 could land and the dogfood would stop launching. **R0 now waits on nothing** |
| 1 | **R0** | D1193: the entry, its build and the pin, before any removal. Nothing else may land first: every later task's red step runs against this repository's own pipeline |
| 2 | **D1148's two tasks** | They add the installation-level schedule and its driver to `internal/infra/spec.go`, `internal/infra/tf/cover.go` and `internal/imagecheck/` — the files R4 and R6 change. Removing first would make D1148's driver read a check this plan deletes |
| 3 | **R1**, **R2**, **R3** | R1 (the `baked` rule) and R2 (the refusals) are the configuration's two halves and neither depends on the other; R3 is R2's fixtures — **R2 first**, because a red step cannot assert a message that is not written |
| 4 | **R4** | R2: the deploy path stops producing a name the configuration no longer defines. **SEC.** R5 rides on it — readiness has nothing to probe once R4 stops deploying |
| 5 | **R5** | R4. The catalog branch of readiness is already written (`internal/infra/readiness.go:449`); R5 turns the repository branch off |
| 6 | **R6**, then **R7** | R2 (the keys the commands read) and R4 (the deploy path they submit into). **The two are serialized and not parallel**: both edit `internal/cli/image.go` — R6 the build's derived arm, R7 the parent's registration of `promote` — and R7 also edits `internal/cli/init.go`, so R6 lands first and R7 rebases onto it rather than the two writing one file at once (§4). R7 removes `promote`, which R0's migration ran with — safe only because R0 is step 1 and the owner's half is done |
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
- **D1148 (ii) is #1683, and it merges before R4.** It adds the installation-level Scheduler, its
  target, the repo module's catalog-check pieces and its own `knownCommands` and cover rows — the same
  `internal/infra/spec.go`, `internal/infra/tf/cover.go`, `deploy/terraform/gcp/modules/repo/` and
  `deploy/terraform/gcp/modules/installation/` R4 edits (§4).
- **R4 removes resource types from a module and D1148 adds one at installation level.** Both edit
  `internal/infra/tf/cover.go:22`'s `allowedTypes` — R4 to drop the check job's and the Scheduler's
  types, D1148 to add its own. **The table is never widened to make a plan pass**; each task adds or
  removes exactly the types its own `plan` prints.
- **D1148's driver is `fugaro image check --job --catalog`, and R6 must not delete it.** The driver
  **has merged** (#1679): the arm reads its spec out of `catalogCheckSpecEnv`
  (`internal/cli/image_check_catalog.go:31`) and decides the catalog through the library the decision
  layer already landed (`internal/imagecheck/catalog.go:20`, `CheckCatalog`). So R6 removes the
  **repository** arms of `image check` — the local workflow check and the `--job` mode, which read
  `rebuild.*` and the repository's registry — and leaves the `--catalog` arm standing; a plan that
  removed the command whole would delete the driver D1148 shipped, which is the one mistake this
  paragraph exists to prevent. What D1148 (ii) still owes is the **Terraform** half, open as **#1683**:
  the installation-level Scheduler and its target, the repo module pieces it reads, the
  `image check --job --catalog` row in `knownCommands` (`internal/infra/tf/cover.go:794`) and its own
  cover types. **#1683 merges before R4**, which edits the same `cover.go` and the same `spec.go`.

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
| `internal/infra/spec.go` | **D1148's driver**, R4, R5, R6 | D1148's driver adds the installation-level check's spec; R4 removes the derived image name and the per-repository `CheckSpec` producer (`internal/infra/spec.go:1451`); R5 leaves the catalog branch of the image choice alone; R6 reads what R4 left. **D1148 first, then R4** — R4 deletes the producer D1148's check replaces, and a driver written against the old producer is a driver rewritten twice |
| `deploy/terraform/gcp/modules/repo/` | **D1148's Terraform**, R4 | D1148 adds nothing here (its schedule is installation level) but reads the module's registry and build account; R4 removes `check.tf`'s job and Scheduler, the registry repository's role or the account's grants that only a repository image needed. **D1148's task merges first**, so R4 removes from a module D1148 has stopped depending on |
| `deploy/terraform/gcp/modules/installation/` | **D1148's Terraform**, R4 | D1148 adds the Scheduler job and its target; R4 touches the shared registry's grants only. **D1148 first** |
| `internal/infra/tf/cover.go` (its `allowedTypes` at `internal/infra/tf/cover.go:22`, and `ManagedAllowedTypes` at `internal/infra/tf/managed_cover.go:124`) | **D1148's Terraform**, R4 | D1148 adds its types, R4 removes the check job's and the Scheduler's. Each edits the list for its own plan and **neither widens it to make a test pass** |
| `deploy/terraform/gcp/modules/repo/catalogcheck.tf`, `deploy/terraform/gcp/modules/repo/variables.tf` | **D1148 (ii)** (#1683), R4 | #1683 adds the module's catalog-check pieces and the variables they read; R4 edits the same `variables.tf` to drop the check job's own. **#1683 merges first**, so R4 removes from a module #1683 has finished with |
| `internal/backend/gcp/names.go` | **D1148 (ii)** (#1683), R4 | The name functions are shared: `CheckJobName` (`internal/backend/gcp/names.go:280`) and `CatalogReadCondition` (`:599`). #1683 reuses both for the installation-level job, so R4 must not delete a name the schedule still calls — what goes with the per-repository job is its **call site**, not the function. **#1683 merges first** |
| `deploy/terraform/gcp/modules/installation/` and `deploy/terraform/gcp/roots/installation/` | **D1148 (ii)** (#1683), R4 | #1683 adds the Scheduler, its target, the root's wiring and their `terraform test` fixtures and goldens; R4 touches the shared registry's grants only. **#1683 merges first**, and R4 regenerates the goldens #1683 just regenerated |
| `internal/imagecheck/` (`internal/imagecheck/catalog.go:20`, the per-repository check) | **D1148's driver**, R6 | D1148's driver calls `CheckCatalog` over every buildable entry; R6 removes the per-repository triggers' readers (`rebuild.*`) and `checkedWorkflows` (`internal/cli/imagecheck.go:241`). **D1148 first** |
| `internal/settings/registry.go` | **G0/G1**, R2 | The configuration-management plan's G0 and G1 add rows and widen the generic writer's allowlist; R2 removes the twenty-one rows of the removed keys (`internal/settings/registry.go:229`, `:454`, `:477`). **One row, one owner**: R2 touches only the removed keys' rows and never the writer |
| `schemas/settings.schema.json` | **G2**, **G8**, **G9**, R2 | They add member rows for the writes they add; R2 removes the members of the keys it removes (`schemas/settings.schema.json:832`, `:1100` are the two 0.7.0 tombstones they sat between). **R2 does not move the document's version** — the rule G2 states |
| `internal/cli/image_refresh*.go` | **M-series** (`image_refresh_steps.go`, `image_refresh_rules.go`), R6 | The managed build's M-series changes what `image refresh` converges; R6 removes its derived-image arm. **The M-series lands first**, or R6 rewrites lines it is about to be handed back |
| `internal/cli/init.go`, `internal/cli/update.go` | **M-series** (`update.go`, `init.go`), R7 | R7 changes `setup`'s first-build offer (`offerBuilds`, `internal/cli/init.go:3415`) and the retirement door is left alone (`:444`); the M-series adds the converge stages. **Different functions, one file, M-series first** |
| `docs/rigor-modes/reconciliation.md` | the design's own commit, **R2** | The design's commit moves the **pins** row (93 → 94, with its amendment sentence). **R2 moves `Config scope rows` (93 → 82)**, which is the eleven rows of spec §1.1 leaving `internal/config/scope.go`, and `TestDocsRigorModesCensusStatesItsCommands` re-runs that row's own command — so the row and the removal land together or the removal fails a test no task names. The rows R2 does **not** move, checked rather than assumed: the two top-level-key rows (a **workflow** key is not a top-level property), the skew-constant row (no `Since` constant leaves with the removal) and the pins row (no pin is deleted, one is rewritten) |
| `internal/cli/docs_repo_image_removal_test.go` | **R2**, **R3**, R4 (and R10 reads it) | It pins this folder's facts **against the tree of the design's own commit**, so every task that moves a figure it states re-points it in the same commit: **R2** the three settable surfaces (11 scope rows, 21 registry rows, 21 schema members) and the sentences that state them — re-counted after the removal, which is 0 for each, the two tombstone rows that stay never having been among them; **R3** the fixture figure, since the invalid fixtures that become the new refusal's keep their keys and the count is therefore not 0; and **R4** nothing, once the retirement half reads the guard that stays (`deploy/terraform/gcp/modules/workflow/job.tf:15`) rather than the check job's. A pin left asserting the pre-removal tree is the first removal task's failing test, and it is a file no other test in the tree names |
| `docs/gcp-live-checklist.md` | **R10 only** | The file is the owner's register; a coding agent's session does not edit it. **R10 is the one task this plan lifts that rule for**, it adds a register row and one section and edits nothing else, and no other task touches the file |

**And the files this plan shares with work already in flight.** The rows above are files two *tasks of
this plan* share. The rows below are files a task here shares with a **pull request or issue already
running**, and the rule for every one of them is the same: **the stream in flight merges first**, and a
task of this plan re-reads the file before it edits it rather than rebasing blind. None of them is a
dependency of the order in §2 — they are a fact about how many writers one file has on the day the task
starts, which is why they are written here and not left to the merge.

| File | Streams in flight | What each one does with it |
| --- | --- | --- |
| `internal/config/validate.go`, `internal/infra/spec.go` (`repoCtx.check`), `plugin/skills/setup/` | **#1685**, R4, R8 | The validate-and-check stream edits the check spec's producer (`internal/infra/spec.go:1451`) and the setup skill. R4 **deletes** that producer and R8 rewrites that skill, so the two collide head-on. **#1685 merges first**: R4 then deletes a producer that has stopped moving, and R8 rebases its skill edit onto it |
| `internal/cli/image.go`, the catalog commands (`internal/cli/image_catalog.go`, `image_build_catalog.go`) | **#1686**, R6, R7 | The Bitbucket-credentials stream adds the contents reader and the catalog fetch, and touches `fugaro image add` / `validate` — the same file R6's derived arm and R7's `promote` registration live in. **#1686 merges first**; R6 lands next and R7 after it (§2 step 6) |
| `internal/cli/init_policy_migrate.go` | **#1656**, R10 | The policy-migration stream rides on **M1** (#1672), the stage of `fugaro update` that publishes the policy node, and it changes what an `update` run prints. It shares no file with a task here, but R10's check reads an `update` run back. **#1656 merges first**, so the check measures the tree it will ship in |
| `internal/cli/project_cmd.go`, `internal/cli/init_policy_publish.go`, the setup stage's missing-file message | **the `project` family's two streams (A and B)**, R8, R9 | They collide with R8's setup skill and reference documents — whose example the missing-file message repeats — and with R9's gate rows, because `fugaro project policy publish` is one of the seven the gate covers. **They merge first** |
| `internal/cli/settings_set.go`, `internal/cli/settings_write.go` | **M10**, R2 | M10 is the writer both halves of R2 pass through — the scope and registry rows R2 removes are the rows that file's allowlist admits. **M10 merges first**, and R2 re-reads the allowlist before removing a row from it |
| the `workflows.*` writers (G3), the secrets family (G8), `internal/runner/accountchoice.go` (A7) | **0.19.0's slice** — G3+ of [docs/config-management](../config-management/implementation-plan.md) (G3–G13, **G8** among them) and **A7** of [docs/accounts-add](../accounts-add/implementation-plan.md) — and R2, which lands in the removal's own release | These ride the release **before** the removal's, so none of them holds a task back: they merge first by the same rule, and R2 — landing after them — re-reads the rows it removes instead of assuming the surface they wrote into is the one it was designed against |

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
  `image promote`) appear in no non-test file. It lands with R9, because R9 is the last code task, and it is the only test whose failure means "the removal is not
  finished" rather than "this change is wrong".

## 7. The recommended slot

**0.20.0 — two releases after D1148's two tasks and the `imageCatalogShipped` flip, both of which are
0.18.0's.** That is not a convenience: the removal's whole premise is that the
catalog **is** the deployed image path, and shipping the removal in a release whose catalog the launcher
does not yet read would leave the installation with the refusal in
`internal/infra/readiness.go:462` and no image. The flip is in — both constants are `true` on `main`,
#1665 merged — so the premise holds, and the extra releases cost the premise nothing. What the removal
still owes is D1148 (ii), open as **#1683** and owed by 0.18.0, and the ruling on the `--retire` step
[reconciliation.md](reconciliation.md) §5 raises.

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
- **Order note.** R0's precondition is **already met**: the catalog shipped — both
  `imageCatalogShipped` constants are `true` on `main` and #1665 is merged (§2 step 0) — so R0 is the
  first task that can land and waits on nothing. With the flag false, readiness would have held the job
  back rather than running it (`internal/infra/readiness.go:462`), which is what the step was for.

## R1. `checkout: baked` keeps both values, and a prebuilt entry is refused on one (D1191). **SEC**

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
- **SEC.** The task writes a **refusal on the deploy path** (the owner's answer to D1191), so a reviewer
  reads the condition and its message the way the `docker`-on-Cloud-Run refusal is read: which source
  kinds it covers, that it fires where the spec is built and not at run time, and that an entry the
  catalog gains later cannot slip past it — the refusal is a list, and an incomplete list deploys a job
  whose first minute fails.

## R2. The configuration: tombstones, the scope rows, the registry rows and the three schemas (D1195, D1196). **SEC**

- **Interface.** Each removed key becomes a tombstone that refuses by name and names the catalog command
  — the mechanism `image.node`/`image.jdk` already are (`internal/config/image.go:119`, `:122`), whose
  sentence is built from `MigrationDoc`. `internal/config/scope.go:108` loses the eleven rows of the
  removed keys (`:113`, `:131`, `:135`, `:179`, `:189`), `internal/settings/registry.go:229` its
  twenty-one (`:454`, `:477`), and the three schemas their members
  (`schemas/fugaro.schema.json:332` and `schemas/project-layer.schema.json:236` are the `image` members
  the two 0.7.0 tombstones keep, and `schemas/settings.schema.json:832`, `:1100` the two rows theirs
  sat between).
  `image_name`, `needs` and `image_catalog.*` keep every member they have, and no version moves.
  **The two tombstones stay**: `internal/config/scope.go:106` and `:107` are `image.node` and
  `image.jdk`, the 0.7.0 keys the removal keeps refusing, so they are **not** among the eleven and its
  first removed row is `:108`.
  **This task also moves the two documents the removal's figures live in**: the `Config scope rows` row
  of `docs/rigor-modes/reconciliation.md` (93 → **82**, the eleven rows) and this folder's own pin
  (`internal/cli/docs_repo_image_removal_test.go`, whose three surface counts and whose sentences about
  them become the post-removal truth) — §4.
- **Red step.** `TestRemovedImageKeysAreRefusedByName` — a table of the removed paths, each asserted to
  fail with a message naming `fugaro image add`: today each fails, or is accepted, with the decoder's
  own words. `TestRemovedImageKeysLeaveTheScopeTable` and
  `TestRemovedImageKeysLeaveTheSettingsRegistry` are the two the surface can lie without: a scope row
  and a registry row are what make a key *settable* after the config stopped accepting it. Both, and
  this folder's pin, are what the docs' own tests read back: `TestDocsRigorModesCensusStatesItsCommands`
  re-runs the census row, so a task that emptied the scope table without moving the row fails a test
  that names neither.
- **Files (nine non-test files, so it lands as two commits).** `internal/config/config.go`,
  `internal/config/image.go`, `internal/config/scope.go`, `internal/settings/registry.go`, the three
  schemas, and the two documents the figures live in (`internal/cli/docs_repo_image_removal_test.go`,
  `docs/rigor-modes/reconciliation.md`) — beside their tests, which is more than one commit of twelve.
  **The split is by subject and not by directory**: the first commit is the **configuration's** half
  (`internal/config/` and its tests — the tombstones and the scope rows, which is what makes a removed
  key refused), and the second is the **settable-surface** half (`internal/settings/registry.go`, the
  three schemas and their tests — the rows that would otherwise leave a settings command able to write a
  key the config refuses) **with the two documents' figures moved in that same commit**, because
  `TestDocsRigorModesCensusStatesItsCommands` re-runs the census row and a half-moved figure fails a
  test no task names. R3's fixtures follow both.
- **Order note.** R2 is the task that makes the removal **observable**: after it, a repository still
  carrying `image:` is refused at validation, so R0 has to have landed (§2). Its two commits land in
  order and neither is optional: a scope row removed without its registry row and its schema member
  leaves the settings family offering a key the config no longer accepts, which is the half of the
  removal a green config test would not see.
- **SEC.** It is the configuration's own refusal surface: the tombstones are what a person reads when
  their `fugaro.yaml` stops working, and the scope, registry and schema rows are what decide whether a
  **write** can still reach a removed key. A reviewer reads the refusal messages line by line and checks
  the three surfaces against the same list — the exact-set question again, on three files at once.

## R3. The fixtures and the golden (D1197)

- **Interface.** The **39** files under `testdata/` still carrying one of the three keys
  (`grep -rlE '^[[:space:]]*(image|dockerfile|rebuild):' testdata/`) move. The *valid* ones already
  went, swept with R2 by review order: each lost the key, the image ones carry the catalog reference
  the key became (`image_name:`), and the two whole `rebuild` blocks moved into the invalid corpus
  rather than lose their subject. What is left is the *invalid* ones that assert an old refusal —
  `image-and-dockerfile.yaml`, the `legacy-image-*` pair, the `checkout-clone-*` set — which become
  fixtures asserting the **new** refusal, and `internal/cli/testdata/init_retire.golden`, which is
  regenerated.
- **Red step.** R2's refusal message is the red step; this task is what proves the **fixtures** agree
  with it: `go test ./internal/config/ -run 'TestLoad|TestValidate' -count=1` fails on every
  fixture R2 did not sweep, and the golden test fails until it is regenerated.
- **Files (40, in four commits of ≤12).** After R2's sweep the 39 files are two **invalid**
  directories and the golden, so the split is by *what a fixture asserts* and never one commit per
  directory: `testdata/config/invalid/` — **29 files, over the ceiling on its own** — across three
  (12 + 12 + 5), and the 10 `testdata/project-layer/invalid/` ones in a fourth, which also carries the
  regenerated `internal/cli/testdata/init_retire.golden`. **One subject, four commits** (§1).

## R4. The deploy path: the derived name, the check job, the Scheduler and the registry. **SEC**

- **Interface.** `repoCtx.check` (`internal/infra/spec.go:1451`) and the `CheckSpec` it produces
  (`:318`) go, with `deploy/terraform/gcp/modules/repo/check.tf:16`'s job (`:116` is its Scheduler) and
  their `allowedTypes` entries. The derived image name (`internal/infra/spec.go:1019`) and
  `registryPath` (`internal/infra/spec.go:757`) go from the job's image; `catalogJobImage` (`:799`)
  becomes the only producer.
  The repository's Artifact Registry repository stays **in the configuration** and its object is
  destroyed only by the owner's `--retire` run (D1194): its `prevent_destroy`
  (`deploy/terraform/gcp/modules/repo/registry.tf:10`) is **not** touched. The check job's own
  `deletion_protection` (`deploy/terraform/gcp/modules/repo/check.tf:27`) leaves with the job, and the
  guard's meaning does not: the workflow job that stays carries the same one
  (`deploy/terraform/gcp/modules/workflow/job.tf:15`, `!var.allow_job_delete`), so an ordinary converge
  still cannot delete a job. The build account
  (`deploy/terraform/gcp/modules/repo/build.tf:5`) stays, with its grants narrowed to the shared
  registry, the base registry and ghcr, and **the catalog grants D1148's schedule runs on are kept**
  (§2). `knownCommands` (`internal/infra/tf/cover.go:794`) loses the **per-repository** row
  (`{{"fugaro"}, {"image", "check", "--job"}}`) with the job it describes, and keeps the
  `image check --job --catalog` row #1683 adds for the installation-level job — the two are different
  containers running different modes, and covering one does not cover the other. **R4 lands after
  #1683 merged**, so the row it removes is the one it means.
- **Red step.** `TestDerivedImageNameIsGoneFromTheSpec` — the spec of a repository whose workflow names
  an entry carries the shared registry's name and nothing of the repository's own; today it carries
  `gcp.ImageName(c.registryPath(), slug, name) + ":latest"`. Then the Terraform half:
  `scripts/gen-golden-plans.sh` regenerates the goldens, and the plan shows the check job and the
  Scheduler leaving and **nothing else**.
- **Files (≤10).** `internal/infra/spec.go`, `deploy/terraform/gcp/modules/repo/{check,build}.tf` and
  `outputs.tf`, `internal/infra/tf/cover.go`, the spec test, the regenerated goldens, and
  `internal/cli/docs_repo_image_removal_test.go` where its retirement assertion still names the check
  job (§4). **`deploy/terraform/gcp/modules/repo/registry.tf` is not in this list** (D1201): the file
  and its `prevent_destroy` stay exactly as they are.
- **SEC.** It is a Terraform deployment: an independent reviewer reads the plan's destroy list line by
  line, and `terraform plan` on the `installation` and `repo` roots is the evidence — never a widened
  cover table.

## R5. Readiness asks the shared registry, not the repository's (D1203). **SEC**

- **Interface.** `hasLatest` (`internal/infra/readiness.go:731`) and the per-workflow branch at `:445`
  stop asking the repository's registry for a workflow that names an entry, leaving the catalog branch
  (`:449`) as the only image probe; `MissingImage` (`:30`) keeps its meaning and its kind. The two
  `imageCatalogShipped` refusals (`internal/infra/readiness.go:462`, `:512`) are already unreachable —
  both constants are `true` on `main` since #1665 — so R5 removes the branch that reads them rather
  than the flag. **R5 does not move the constants**, and nothing in this plan does: the flip is 0.18.0's
  and it has shipped in the source.
- **Red step.** `TestReadinessReadsTheCatalogNotTheRepositoryRegistry` — a workflow pinned to a built
  entry is ready with the repository's own registry empty, which today is only true on the catalog
  branch; its control asserts that an entry with no `:latest` is still refused, with the sentence naming
  `fugaro image build <entry>` (the message that exists today).
- **Files (≤3).** `internal/infra/readiness.go` and its two test files.
- **SEC.** Readiness is the last thing between a broken image and a run, and this task **re-points** it:
  a reviewer reads the probe's new subject (the shared registry's `:latest` for the chosen entry) and
  confirms that the branch it replaces cannot be reached at all — a readiness check removed without its
  replacement is a deploy that starts a job on an image nobody built, which is the failure readiness
  exists for.

## R6. The commands that build, refresh and check a repository's image (D1190, D1199, D1200)

- **Interface.** `fugaro image build` (`internal/cli/image.go:78`) keeps its catalog arm
  (`fugaro image build <entry>`, `internal/cli/image_build_catalog.go`) and loses the derived one — the
  `--local` arm, the render step and the repository-registry submit. `fugaro image check` keeps its
  catalog job arm (`--job --catalog`, `internal/cli/image_check_catalog.go:31`, D1148's driver merged as
  #1679) and loses the two repository arms around it: the local workflow check and the per-repository
  `--job` mode, with `checkedWorkflows` (`internal/cli/imagecheck.go:241`) and the `"the repository's"`
  route out of `refuseJobSubmission` (`internal/cli/imagecheck.go:595`). **Four commands go whole**, as
  §1.2 and §6 say they do — `render` (`internal/cli/image.go:513`), `status`
  (`internal/cli/imagecheck.go:982`) of the family's own, and the derived build's two steps `gate` and
  `record` (`internal/cli/image_record.go:60`, `:145`), beside `promote`, which R7 removes. They are
  **removed, not narrowed**: the check a workflow's image needed is the one D1148's project-level
  schedule makes over catalog entries, and that schedule runs the `--catalog` arm rather than anything
  this task leaves behind. `internal/infra/tf/cover.go:794`'s per-repository `image check --job` row goes
  with the job in R4; the `--catalog` row stays.
  `checkedWorkflows` (`internal/cli/imagecheck.go:241`) goes, and `image refresh`'s derived steps
  (`internal/cli/image_refresh_steps.go`) go. **`rebuild.*` is read by nothing after this** (D1200) —
  the trigger set is `internal/imagecheck/catalog.go`'s and D1148's.
- **Red step.** `TestImageRefreshNoDerivedSteps` — the command's own list, asserted to name no
  repository image. `TestImageRenderIsGone`, `TestImageStatusIsGone`, `TestImageGateIsGone` and
  `TestImageRecordIsGone` assert each command is not registered, so a half-removed command cannot be
  reached by its old name — and the gate's own §6 row ("no row: the commands do not exist", R9) is only
  true once these pass. `TestImageCheckKeepsOnlyTheCatalogJobArm` is the counterweight, and the one this
  task must not get wrong: `fugaro image check --job --catalog` still decides a catalog and still
  refuses to run outside a Cloud Run job (`refuseJobSubmission`), while the local mode and the
  per-repository `--job` mode are gone.
- **Files (≤12).** `internal/cli/image.go`, `internal/cli/imagecheck.go`, `internal/cli/image_record.go`,
  `internal/cli/image_refresh.go`, `internal/cli/image_refresh_steps.go`,
  `internal/cli/image_refresh_rules.go`, and the tests that name them — including the test files of the
  removed commands, which are deleted with them.

## R7. `promote` goes, the refusal names the forward path, and `setup` offers a catalog build (D1192, D1207). **SEC**

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
  repository has run promote (§2 step 6), and **after R6**, which edits `internal/cli/image.go` in the
  same release: the two are serialized rather than rebased over each other (§4).
- **SEC.** The task removes a command and re-points two doors that **write**: `promote` (the only CLI
  path that wrote a workflow's image keys, F16) and `setup`'s first build. A reviewer reads the deleted
  command's removal for leftovers — a flag, a registration, a mention in a document the CLI tests pin —
  and reads `setup`'s offer for what it now triggers (`fugaro image add`, then `fugaro image build
  <entry>`) and for the fact that `--no-build` still means what it meant.

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
  removal's tree check lands here (§6): the removed identifiers appear in no non-test file. It does
  **not** assert anything about `imageCatalogShipped` (`internal/cli/recipes_skew.go:176`,
  `internal/infra/readiness.go:762`): both constants are already `true` — they are 0.18.0's flip, merged
  as #1665 — so a check on them would be green before the task starts and would prove nothing about the
  removal.
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

## R10. The owner-run live check, and the 0.20.0 tag (the final task)

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
  6. **the retirement, then the read-backs**: the owner runs `fugaro update --repo <sandbox> --retire`
     **first** — the typed confirmations are the check's own step and no agent runs them — because
     nothing else removes the check job: an ordinary converge of that repository stops **before its
     apply** while the retired job's state says `deletion_protection = true`
     (`internal/cli/init_retire.go:305`, `:327`). **Then**: the per-repository check job is gone from the
     job list and its Scheduler from `gcloud scheduler jobs list` — the Scheduler is the one object the
     `--retire` run's own apply destroys, an ordinary converge never reaching it (spec §4) — the
     repository's own registry **still exists and is still in the configuration** with its
     `prevent_destroy` (D1201: no step of this check and no part of the retirement destroys it), and the
     installation's shared registry is untouched otherwise;
  7. **the undo**: the pin's commit reverted and the run's branch deleted, the entry left in the catalog
     (it is what the sandbox runs from now on), and the check's own dollars recorded.

- **Red step.** The register row and the section are the deliverable; `registerRow`-shaped pins
  (`internal/cli/docs_check96_test.go:221`) and the ordering pin that a check is written above the
  highest written section (`internal/cli/docs_checks_145_149_test.go:179`) fail until both are written,
  and `website/test/checklist.mjs` reads the file's sections. Nothing here is `go test`-provable beyond
  that: **the check's own result is the owner's**, and the 0.20.0 tag follows it (§7).
- **Files (≤3).** `docs/gcp-live-checklist.md`, the new pin test, and the release notes' `### For
  operators` line for the release it ships in.
- **Note.** This is the one task this plan lifts the "no agent edits the checklist" rule for, and it
  edits nothing else: **no agent runs any step of the check.**
