# An image's `ENV` reaching a run: implementation plan

**Status: IE1 is implemented (this branch, not merged); IE2, IE3 and IE4 are planned and not
implemented.** Four tasks, **IE1–IE4**, each with its
files (IE2's is the longest at eight, because a change to the reserved list is a change to its
schema mirror too), its interfaces, its named tests, its **red step**,
its **mutation**, its **reviewer tier**, what it depends on, its command and its commit. Written against
the tree of **2026-11-06** (`c52d40f9`, v0.20.2, the commit the fix round merged into this branch; the
first reading was at `57f7d871`); every citation below was resolved while writing it. **The owner
answered every question this design opened with**: the scope and the release on 2026-11-05 (**O1**,
**O2**) and **O3, O4 and O5 on 2026-11-06** — each the way its decision recommended — so every task's
block is settled and none of them waits on a question.

**Recorded 2026-11-06, and reconciled in [reconciliation.md](reconciliation.md) §8:** **O3 = names
only**, no value this work passes is registered with the redactor (D1376, the recommendation), so IE2
carries no redaction loop; **O4 = yes**, `BATCH_` joins `config.ReservedEnvPrefixes` and the three
edits that follow from it are **IE2's** (the list, the schema mirror of it and the doc enumeration),
not IE1's; **O5 = yes**, the report is **`fugaro doctor --all`** (D1377), which is IE3.
**The owner's approval of this plan** is what let IE1 start, and it is what the rest wait on.

**Written to:** [spec.md](spec.md) (issue **#1755** verbatim) and [reconciliation.md](reconciliation.md).
**Where they disagree, the reconciliation wins**; where a task below and the reconciliation disagree,
**the reconciliation's findings (F1–F18) and decisions (D1370–D1383) win**, and the task is wrong.

**Rules every task obeys** (this repository's own, not this plan's invention):

- a **red step** first: the named test fails for the reason the task exists, and the failure is quoted
  in the pull request;
- **targeted tests only** (`GOFLAGS=-p=2`, `-count=1`, `-timeout 40m` where a package is slow), never a
  whole package that takes half an hour;
- **hermetic tests only** (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`): no live call, no
  registry, no real job, no network;
- **no secret-shaped literal** anywhere, in a test or a doc;
- **a field never changes meaning**: no task reuses or retypes an existing wire field, config key or
  environment name, and the stamp is a new name (D1371);
- **comments explain why, never what**; no TODO, no section header, no restatement of a name;
- **`fugaro update` stays the whole upgrade**: nothing here adds a command, and a job that predates
  the stamp collects it the next time that repository's own resources converge —
  **`fugaro update --repo .`**, the repository stage a plain `fugaro update` does not run (D1371,
  D1382);
- **`.github/workflows/` is never touched** by any task.

---

## 1. The order, and what forces it

| Order | Task | What forces it |
| --- | --- | --- |
| 1 | **IE1** — the job carries the catalog entry it was deployed with (the stamp) | Nothing. It is the fact every other task needs and the only one the deploy can supply (F5, F6). It changes no behaviour: a stamp nothing reads yet is what makes IE2 safe to write and IE3 honest |
| 2 | **IE3** — `fugaro doctor --all` names what a run drops | IE1 alone, and **not IE2**: the report is a statement about the installation and its jobs, and it is the whole of option 3's command half. It is in the release **whatever happens to IE2** (D1383), which is why it is not behind it |
| 2 | **IE2** — the passthrough (SEC) | IE1, **O3** (the redaction question — answered 2026-11-06: no redaction, so nothing of it is a line in this task) and **O4** (`BATCH_` — answered yes, and that line is this task's). The one task that changes what a run can reach: **SEC — opus, with the independent review mandatory** |
| 3 | **IE4** — the two doc sentences, the skill clause and the live check | IE2 and IE3 for the half that describes behaviour (**a doc that says an image's `ENV` reaches a run is false until IE2 has landed**), and IE1+IE3 alone for the half that states the rule and points at the report. IE4 splits in exactly that way and says so in its commit |

**IE2 and IE3 are the same level and share no file**, which is why they are two tasks and not one: IE2
is `internal/agent` and `internal/runner`, IE3 is `internal/cli`'s doctor; each can land alone, and IE3
landing first is the smaller release. **IE2 is the expected one to slip** — it is the largest of the
four, the only SEC one, and the only one whose design (a new environment write) is what the
independent review is for. Nothing else in this plan moves if it slips: the release ships the sentence,
the report and the rule (D1383) and is never worse than the tree it replaces.

## 2. Shared files

| File | Tasks | Note |
| --- | --- | --- |
| `internal/infra/spec.go` | IE1 | the job spec's env, where `CatalogEntry` is already in hand (`:1103-1104`) and the two other marks are written (`:1127-1135`). One writer; no other task here touches it |
| `internal/runner/provision.go` | IE1 | where `CheckoutEnv` (`:29`) and `RemoteEnv` (`:57`) live, and `internal/infra/spec.go:79`/`:88` alias both. The stamp's constant goes beside them for the same reason: one spelling, two readers. IE1's `internal/runner/imageenv_names.go` sits beside it: the subtraction IE2 and IE3 both call |
| `internal/config/validate.go` | IE1 (the `EnvNameRE` export) and IE2 (O4's answer, `BATCH_`) | the source of the filter's shape half and, from IE1, the spelling the runner reads (`EnvNameRE`, declared at the file's end for the reason that file records, so no citation into it moves); the prefix list `BATCH_` joins is IE2's edit to the same file |
| `schemas/fugaro.schema.json` | IE2 (O4's answer) | the reserved secret variables' `env` pattern (`:473`), the mirror `TestFugaroSchemaReservesTheSameEnv` (`schemas/schemas_test.go`) holds to `config.ReservedEnv`. **`BATCH_` added to one list and not the other reddens `go test ./...`**, which CI runs: the two are one edit |
| `docs/design/v1.md` | IE2 (O4's answer) | §"Reserved secret variables" (`:616`), the enumeration of both lists; the same edit moves it |
| `internal/runner/agentenv.go` | IE2 | `parentEnv()` and the env plumbing the subtraction belongs with |
| `internal/runner/runner.go` | IE2 | **reserved: one writer across work streams.** IE2 takes it for exactly one call site (`:2294-2312`) and says so in its pull request; if the subtraction turns out to need a second site, IE2 takes the file alone and reports it rather than editing it silently |
| `internal/cli/doctor.go`, `doctor_render.go`, `doctor_launcher.go` | IE3 | the report's command, its renderer and the job read it needs. No other task here touches `internal/cli` |
| `docs/base-image.md`, `docs/design/base-image.md` | IE4 | the two sentences F2 found, in the same commit |
| `plugin/skills/working/SKILL.md` | IE4 | the catalog bullet's one clause |
| `docs/gcp-live-checklist.md` | IE4 | **only** the check's own section and summary row, written by the task that implements it, under the register's own rule |

**Not touched by this plan, and named so it is not discovered later:** `SECURITY.md` (the answer in
reconciliation §4 is a trust statement this design writes down, not a change to that file's claims —
O3's answer of 2026-11-06 means the sentence the alternative owed there is **not** written),
`schemas/` other than the reserved-variable mirror IE2 edits with O4's answer (no config key, no wire
field, and no other schema file), `deploy/terraform/**` (the launcher role
already carries `run.jobs.get` and `run.jobs.list`, `deploy/terraform/gcp/modules/installation/iam.tf:9-10`,
which is the read IE3 adds — if the read needs nothing, Terraform is untouched by every task),
`internal/config/**` other than `validate.go`, which IE1 reads (`EnvNameRE`) and **O4**'s answer of
2026-11-06 has IE2 edit: its denylist source is that file's prefix list with `BATCH_` added there,
and IE2 says so, `docs/repo-image-removal/**`,
`docs/cloud-senior-review/**`, `ee/**`, `console/**`, `mobile/**`, `shared/**`, `website/**` and
`.github/workflows/**`.

## 3. Tasks

### IE1. The job carries the catalog entry it was deployed with

**Decisions:** D1371, D1378. **Findings:** F5, F6, F7, F9. **Depends on:** nothing.

- **Files:** `internal/infra/spec.go`, `internal/infra/spec_test.go`, `internal/runner/provision.go`, `internal/config/validate.go` (one export, `EnvNameRE`), and the new pure function that spells the subtraction once (the names a run drops: a job's env names minus the reserved, platform, launch, stamp and declared-secret names) with its test, in `internal/runner` (new, `imageenv_names.go`, beside `ImageEntryEnv`; nothing calls it yet). IE2's `imageEnv` and IE3's report both call it, which is why it is IE1's and not either's: IE3 therefore depends on IE1 alone, as D1383 says.
  **6 files.**
- **Interfaces:** produces `runner.ImageEntryEnv = "FUGARO_IMAGE_ENTRY"` beside `CheckoutEnv` and
  `RemoteEnv`, aliased as `infra.ImageEntryEnv = runner.ImageEntryEnv` the way those two are
  (`internal/infra/spec.go:79`, `:88`), and one assignment in `repoCtx.workflow` where
  `CatalogEntry` is set: `if entry.Name != "" { ws.Env[ImageEntryEnv] = entry.Name }`. **The value is an
  entry's own name and nothing else** — it is `config.ValidateCatalog`-checked operator text, so it is
  a name, not a reference, a URL or a digest, and the runner never parses it. **It is written for every
  job whose image the catalog chose**, prebuilt and built alike (`CatalogImageEntry` reads the entry
  back out of a prebuilt reference, `internal/backend/gcp/catalog.go:168-192`), and for no other job:
  a repository-built job and `checkout: clone`'s base image carry the key **empty** (the owner's choice of 2026-11-06 after the security review of IE1: a key the job sets overrides a derived image's own `ENV`, an absent one does not, so a repository Dockerfile could otherwise forge the stamp), which is the fail-closed direction D1371 asks for; IE2 reads an empty stamp as unstamped. It is a new name under a reserved prefix, so no existing
  field changes meaning and the agent's own environment never sees it (D1372). **As landed** (2026-11-06,
  this branch): the constant is `runner.ImageEntryEnv`, aliased `infra.ImageEntryEnv` and assigned in
  `repoCtx.workflow`; the subtraction is `runner.ImageEnvDroppedNames(jobEnv, declared)`, with
  `runner.SecretEnvNames(jobEnv)` reading the job's own `FUGARO_SECRET_ENVS` list and `declared` the
  names the calling run may read, and nothing calls either yet. It applies **both halves** of D1372's
  filter — `config.ReservedEnv`, and a name outside `config.EnvNameRE`'s shape — which is why this
  task exports that pattern from `internal/config` (at the end of `validate.go`, the file's own
  convention, so no citation into that file moves) rather than spelling it again in the runner.
  **IE2 applies the filter through this function and adds no shape rule of its own.**
- **Tests:** `TestSpecStampsTheCatalogEntryOnTheJob` (a job whose workflow runs a built entry and one
  whose workflow runs a prebuilt entry both carry `FUGARO_IMAGE_ENTRY` = the entry's name), and
  `TestSpecStampsNoEntryOnAJobWithNoCatalogEntry` (a repository-built job and a `clone` job carry the
  key present and empty). Both extend the assertions the existing
  `TestSpecRecordsABuiltCatalogEntry` and `TestReadinessDeploysAPrebuiltCatalogEntry`
  (`internal/infra/readiness_catalog_test.go:155-207`) already make about the same spec. The
  subtraction's own tests live beside it (`internal/runner/imageenv_names_test.go`): the names an
  image sets for itself are kept, the reserved ones are dropped,
  `TestImageEnvDroppedNamesDropsANameAShellCannotExpress` is the corpus for the shape half of the
  filter (a hyphen, a space, a lower-case name, a leading digit, a dot, a `+`), the stamp and the
  platform names are dropped, an undeclared secret name is dropped and a declared one is not, and the
  result is sorted.
- **Red step:** `TestSpecStampsTheCatalogEntryOnTheJob` fails on the env lookup: `FUGARO_IMAGE_ENTRY` is
  absent from every job's container env, because nothing writes it (F5).
- **Mutation to hold it:** write the stamp unconditionally (drop the `entry.Name != ""` guard) →
  `TestSpecStampsNoEntryOnAJobWithNoCatalogEntry` fails, and that is exactly the mutation that would
  make a repository-built image pass its `ENV` through IE2. Second mutation: write `ws.Image` as the
  value instead of `entry.Name` → the first test fails on a value that is a reference, and a name is
  what the report prints.
- **Reviewer tier:** standard. It writes a name into a container's env and reads nothing; the reviewer
  confirms the guard, the constant's two spellings and that no other env entry changed.
- **Command:** `GOFLAGS=-p=2 go test ./internal/infra/ -run 'Catalog|Spec|PlatformEnv' -count=1` then
  `go build ./...`.
- **Commit:** `infra: the job carries the catalog entry its image came from`.

### IE2. The passthrough: an operator's catalog image reaches its own runs (SEC)

**Decisions:** D1370, D1372, D1373, D1374, D1375, D1376. **Findings:** F1, F3, F4, F8, F9, F10, F11,
F12, F16, F18. **Depends on:** IE1, **and O3 and O4, both answered on 2026-11-06** — O3 answers
D1376's recommendation, so this task writes **no** redaction loop, and O4 answers **yes**, so
`BATCH_` joins `config.ReservedEnvPrefixes` (`internal/config/validate.go`) **in this task**.
**Reviewer tier: SEC — opus, with the independent review mandatory.**

- **Files:** `internal/agent/env.go`, `internal/agent/agent_test.go`, `internal/runner/agentenv.go`,
  `internal/runner/agentenv_test.go`, `internal/runner/runner.go` (the one call site, `:2294-2312`),
  and **O4's three**: `internal/config/validate.go` (`BATCH_` joins `ReservedEnvPrefixes`),
  `schemas/fugaro.schema.json` (the same name joins the `env` pattern's refused families, `:473`) and
  `docs/design/v1.md` (§"Reserved secret variables", `:616`). **8 files.** O4's answer is not one line
  in one file: `TestFugaroSchemaReservesTheSameEnv` (`schemas/schemas_test.go:1543`) holds the schema's
  list to `config.ReservedEnv` and **fails when the Go list gains `BATCH_` alone**, and CI runs
  `go test -race ./...`, so a task that changed only the Go list would redden `main` — and `v1.md`'s
  enumeration is the doc mirror of both. IE2's pull request says so.
- **Interfaces:** produces `EnvSpec.ImageEnv map[string]string` — *"the variables the operator's own
  catalog image declares, already narrowed by the caller; a name here is dropped if it is reserved,
  and it is written before everything Fugaro writes, so nothing an image sets can override the routing,
  the auth, a declared secret or a `FUGARO_*` name"*; and `func (r *run) imageEnv() map[string]string`
  in `agentenv.go`, which is the one place the set is computed. `imageEnv` returns nil — the whole
  feature off — when `r.parentEnv()[ImageEntryEnv]` is empty (D1371's fail-closed), and otherwise the
  runner's environment **minus** every name `config.ReservedEnv` refuses (D1372 — and the shape rule
  with it: both halves make up IE1's `runner.ImageEnvDroppedNames`, which this task calls rather than
  re-implements; re-applied inside `BuildEnv` too, so the filter sits next to the allow-list it
  protects whatever a caller passes),
  minus the platform and launch names (`internal/backend/launchenv.go:32-54`), and minus every name in
  `FUGARO_SECRET_ENVS` this run did not declare (the declared names are `secretEnvs`, built at `internal/runner/runner.go:2192-2196` before the `BuildEnv` call; `r.secretList()` returns redaction values and is not the set;
  F8 — those reach the agent through the existing declared-secret path alone). **The write goes after
  the allow-list copy and the `DOCKER_HOST` arm (`internal/agent/env.go:85-116`) and before the auth switch
  (`:137`)**, which is what makes `maps.Copy(out, spec.Set)` (`:216`) and `GatewayVars` (`:218-223`,
  *"Last, so nothing declared or inherited can override the routing"*) the last word, as D1374 requires
  and O1 ruled. **The `DOCKER_HOST` arm is not a hole in that order**: `DOCKER_` is already a
  `ReservedEnvPrefixes` entry (`internal/config/validate.go:65`), so an image's `DOCKER_HOST` — like
  anything else the arm reads — is refused by the same filter when `docker: true` ships later
  (`internal/agent/env.go:91-116`: *"Nothing sets spec.Docker yet … so this fails closed until then"*),
  and the value the job set is the one that stands. **Nothing is registered with the redactor by this
  task**, which is O3's answer of 2026-11-06 (D1376): a passed value is not a secret, the agent can already read
  the runner's environment through
  `/proc` (`SECURITY.md:141`), and the doc already forbids a credential in an image
  (`docs/base-image.md:28`), so the `r.addSecret(v)` loop the alternative would have added is not written.
- **Tests:** `TestBuildEnvPassesAnImagesVariables` (an image's `NEXUS_BASEURL` and `MAVEN_OPTS` reach the
  agent), `TestBuildEnvDropsAReservedImageVariable` (a table over the denylist's shape: `PATH`, `HOME`,
  `ANTHROPIC_BASE_URL`, `FUGARO_BUCKET`, `LD_PRELOAD`, `GOOGLE_APPLICATION_CREDENTIALS`, `LC_ALL`,
  `NODE_OPTIONS`, `JAVA_TOOL_OPTIONS` — none is written from `ImageEnv`), and
  `TestBuildEnvSetsFugarosVariablesLast` (an image that sets the gateway's own name loses to the
  gateway, and one that sets `spec.Set`'s name loses to `spec.Set`). In `internal/runner`:
  `TestImageEnvIsNilWithoutTheStamp` (**the load-bearing test**: a job with no `FUGARO_IMAGE_ENTRY`
  passes nothing, whatever its environment holds), `TestImageEnvDropsTheJobsUndeclaredSecrets` (a name
  in `FUGARO_SECRET_ENVS` the run did not declare is absent from the agent's environment while a
  declared one is present, and no value of the dropped one is registered as a secret), and
  `TestImageEnvDropsThePlatformNames` (the five `backend.PlatformEnvNames` and the launch names never
  come from the image arm).
- **Red step:** `TestImageEnvIsNilWithoutTheStamp` fails to compile — `imageEnv` does not exist — and
  the first honest integer of the task is `TestBuildEnvPassesAnImagesVariables` failing with
  `NEXUS_BASEURL` absent from the built environment, which is issue #1755's own bug reproduced as a
  test. **Both are quoted in the pull request.**
- **Mutation to hold it:** (1) drop the stamp check in `imageEnv` → `TestImageEnvIsNilWithoutTheStamp`
  fails, and that is the mutation that would let a repository-built image pass its `ENV` (D1370's whole
  point); (2) remove the `config.ReservedEnv` filter → `TestBuildEnvDropsAReservedImageVariable`
  fails; (3) move the `ImageEnv` write from before the auth switch to after `GatewayVars` →
  `TestBuildEnvSetsFugarosVariablesLast` fails, and that is the mutation that would let an image route
  the run around the gateway; (4) drop the `FUGARO_SECRET_ENVS` subtraction →
  `TestImageEnvDropsTheJobsUndeclaredSecrets` fails, and that is the mutation that would hand a run a
  secret its task never declared (F8).
- **Also in the pull request:** the trust statement of reconciliation §4, in the task's own words — the
  widening the owner accepted (an operator's image is trusted as the operator is), the names that stay
  out (the language-runtime hooks a **repository** could otherwise reach: `NODE_OPTIONS`, `NODE_PATH`,
  `JAVA_TOOL_OPTIONS`, `_JAVA_OPTIONS`, `PERL5*`, `RUBY*`, `PYTHON*`, `BASH_ENV`), and the statement
  that a passed value is **not** a secret and is **not** redacted (D1375, D1376) — because the reviewer
  and the release notes both read it from there.
- **Command:** `GOFLAGS=-p=2 go test ./internal/agent/ -run 'BuildEnv' -count=1` then
  `GOFLAGS=-p=2 go test ./internal/runner/ -run 'ImageEnv|BuildEnv' -count=1 -timeout 40m`.
- **Commit:** `runner: an operator's catalog image's variables reach its own runs`.

### IE3. `fugaro doctor --all` names what a run drops

**Decisions:** D1377, D1378, D1375. **Findings:** F13, F14. **Depends on:** IE1. **Reviewer tier:**
standard, with the doctor pins run in the same pull request.

- **Files:** `internal/cli/doctor.go`, `internal/cli/doctor_render.go`,
  `internal/cli/doctor_render_test.go`, `internal/cli/doctor_launcher.go`, and — a new file, because the
  subtraction is IE2's and this is the reader that has to agree with it —
  `internal/cli/doctor_imageenv.go` plus its test. **6 files.**
- **Interfaces:** consumes the job's container env (`JobFullEnv`,
  `internal/backend/gcp/run.go:377` — **one `Jobs.Get` per job (a new read: doctor lists jobs and reads their IAM, it never gets one), and the launcher role already carries
  `run.jobs.get`**, `deploy/terraform/gcp/modules/installation/iam.tf:9-10`) and produces, per job, the
  names that will not reach runs: the job's own env names minus the reserved names (D1372) minus the
  platform, launch and stamp names minus the names in the job's own `FUGARO_SECRET_ENVS`. **The
  subtraction is spelled once, as a function that both IE2's `imageEnv` and this report call** — the
  function lives where both can reach it (`internal/runner`, exported beside `ImageEntryEnv`), which is
  what D1378 means by "the report and the behaviour can never disagree". The printed line is **names
  only, never values** (D1375), and a job with **no stamp** prints as *not catalog-stamped* rather than
  as "drops nothing" — the two are different facts (a job deployed before IE1, or a repository image).
  **It goes in the `--all` view and `--json`, and the default screen is untouched**: the default is
  pinned at 25 lines by `TestDoctorDefaultIsOneScreen` (`doctor_render_test.go:233-253`) and an
  installation with nothing wrong prints one line and no pointer (`doctor_render.go:391-396`); the
  report is a **measurement**, not a finding (F13).
- **Tests:** `TestDoctorAllNamesAJobsDroppedImageVariables` (a fake installation with one stamped job
  whose env carries `NEXUS_BASEURL`, one reserved name and one undeclared secret name: `--all` prints
  the first and neither of the others), `TestDoctorAllPrintsNoImageValues` (the same fake with a value
  that would be recognisable: the line holds the name and the value appears nowhere in the output),
  `TestDoctorAllMarksAnUnstampedJob` (no stamp: the line says so, and it is not the empty list), and
  `TestDoctorDefaultIsStillOneScreen` (the existing test, re-run in this pull request: the default view
  is unchanged with the report in `--all`). `--json` gets one: `TestDoctorJSONCarriesTheDroppedNames`.
- **Red step:** `TestDoctorAllNamesAJobsDroppedImageVariables` fails on an empty `--all` view: doctor
  reads no job's container environment at all today (F14).
- **Mutation to hold it:** report the names **and** their values → `TestDoctorAllPrintsNoImageValues`
  fails; put the line on the default screen → `TestDoctorDefaultIsStillOneScreen` fails.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Doctor' -count=1 -timeout 40m`.
- **Commit:** `doctor: say which of an image's variables a run drops`.

### IE4. The rule, in the two places a reader looks, and the live check

**Decisions:** D1379, D1380, D1381. **Findings:** F2, F14. **Depends on:** IE2 and IE3 for the half
that describes behaviour; IE1 and IE3 alone for the half that states the rule. **Reviewer tier:**
standard, with `go test ./internal/cli/ -run 'Docs|Backlog'` and `go test ./plugin/` run in the same
pull request.

- **Files:** `docs/base-image.md`, `docs/design/base-image.md`, `plugin/skills/working/SKILL.md`,
  `docs/gcp-live-checklist.md` (the check's section and its summary row), and — only if a docs test
  pins the sentence that moves — the `internal/cli/docs_*_test.go` file that pins it. **At most 6
  files, and at most one pin file.**
- **Interfaces:** the sentence in `docs/base-image.md` next to "Bring your own" (`:130`), where an
  operator deciding what their image carries reads — the file's own §"Harness environment variables"
  table and the allowlist clause at `:45` are where the **harness's** variables are listed, and F2 is
  that the image's own `ENV` is stated nowhere near them. It says three things and no more: **what
  happens** (an image the project's `image_catalog` chose passes its `ENV` to the run's agent, minus
  the names Fugaro reserves and minus the job's own secret names, with everything Fugaro sets applied
  last), **what a reader does about it** (`fugaro doctor --all` names what a job drops, per job), and
  **what is not promised** (a per-repository image passes nothing; a value in an image is not a secret
  and is not redacted, so never bake a credential into one — the rule `docs/base-image.md:28` already
  has). `docs/design/base-image.md` §6's allowlist sentence (`:167`) gains the pointer to it, so the two
  stop contradicting the code. `plugin/skills/working/SKILL.md`'s catalog bullet gains one clause: a
  catalog image's `ENV` reaches the run except the reserved names, and `fugaro doctor --all` names what
  a job drops. **The check's section in the register is written by whoever implements the check, under
  the register's own rule, and not by this task** (D1381, §6 below). **No release-notes file is written
  here**: `docs/releases/` belongs to the release (`scripts/release.sh X.Y.Z`, `docs/release.md`), and
  the `### For operators` sentence D1382 owes is quoted in §5 so it is not invented on release day.
- **Tests:** `go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m` (the pins over both
  docs, and the backlog census, which must still read **"2 items: 2 open, 0 closed, 0 dropped."**), and
  `go test ./plugin/ -count=1`. If a pin asserts one of the two sentences word for word, the pin is
  updated in the same commit with the wording changed in both places — never the pinned fact.
- **Red step:** for the doc half, the docs test that pins the old clause fails as soon as the clause is
  moved, which is the honest red step of a documentation change in this repository; for the plugin half,
  `plugin/plugin_test.go`'s command/flag population fails the moment the skill names a command or flag
  the CLI does not have (`fugaro doctor --all` exists, so the clause is legal — the red step is taken
  by writing the clause first with the wrong flag and quoting the failure).
- **Mutation to hold it:** write the sentence as *"an image's `ENV` reaches the agent"* with no
  reservation clause, and `docs/design/base-image.md` §6's allowlist sentence (`:167`) then contradicts
  the file it points at — the docs pin over that section fails. Second: name `fugaro doctor --env` in
  the skill → the plugin's command population fails.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m` and
  `go test ./plugin/ -count=1`.
- **Commit:** `docs(image-env): what a catalog image's ENV does in a run, and what it does not`.

## 4. What is deliberately not in this plan

- **Per-repository images** (D1370, O1's ruling): a separate design is removing that path
  (`docs/repo-image-removal/`, its tasks R0–R2 in open pull requests #1693, #1694 and #1698; the
  re-slot that moved that design to 0.20.0 is **#1709, which merged**, and the removal itself is
  **not built** on `main`). No task here reads or changes it, and none depends on it (F17).
- **A plain `env:` map on a workflow or a project profile** (option 2, O1): it is the design
  `docs/design/agent-env.md` already is, unbuilt, and its §2 rule is that the allow-list is Fugaro's own
  Go code and never a `fugaro.yaml` field. If the owner ever wants a value with no rebuild, that design
  is the place to rule it — this plan adds no config key and no wire field, and its one schema edit
  (O4's `BATCH_` in the `env` pattern) is a name added to a list that already exists.
- **Reading the image's config at run time** (a registry call for `Config.Env`): refused in the
  reconciliation's §6 — it needs a registry credential inside the job, it makes bootstrap depend on the
  network, and it cannot answer for a prebuilt entry (F5).
- **`BATCH_` in the denylist**: answered **yes** on 2026-11-06 (**O4**), so it is one name the
  subtraction reads and **three edits, in IE2**: `internal/config/validate.go`'s prefix list,
  `schemas/fugaro.schema.json`'s `env` pattern and `docs/design/v1.md`'s enumeration of both lists.
  The answer, not this plan, decided it (F9) — and the schema edit is not optional: the mirror is
  tested (`TestFugaroSchemaReservesTheSameEnv`, `schemas/schemas_test.go:1543`) and a Go-side-only
  change reddens `main`.
- **Registering every passed value with the redactor**: answered **no** on 2026-11-06 (**O3**), so
  D1376's recommendation stands and IE2 writes no loop at all.
- **A `SECURITY.md` change.** O3's alternative would have been the one thing to add it: the redaction
  sentence would go in IE2's pull request and IE2's file list would grow by one. The answer is the
  recommendation, so that file is untouched by every
  task, because reconciliation §4's trust statement is a statement **about** `SECURITY.md`'s existing
  assumptions (`:6`, `:53`, `:141`, `:192`), not a change to them.
- **A new dependency.** `go.mod` and `package.json` are unchanged by every task.
- **The stale comment at `internal/backend/backend.go:131`** (F15): a one-line comment fix, not this
  design's file. Whoever takes IE1 may fix it in the same commit; the plan does not require it.

## 5. Release slot

**0.20.0, which is O2's answer.** The design note and option 3 land in 0.20.0; the code only if this
design's review is clean in time (D1382). The slot needs nothing from this plan to be announced — the
design folder itself is the note — and the release that carries IE1–IE3 adds **no operator step**: a
repository's jobs converge through `fugaro update --repo .` already, so the stamp arrives with that
repository's next re-converge rather than through a command anyone has to learn, and an installation
that never runs a catalog image behaves exactly as it does today.

**`main` has since tagged 0.20.0 and moved past it** — `docs/releases/v0.20.0.md`, `v0.20.1.md` and
`v0.20.2.md` are all written, the last at `c52d40f9` — and neither this design nor the code it plans
shipped in any of them. The answer is not undone by that: **O2 named 0.20.0, and the slot is the
owner's to re-take**, at whichever release the owner names next. **No task here waits on a number**,
and nothing in this plan has to be rewritten if the owner's next answer is a different one — §7's
gates name no release.

**The `### For operators` sentence the release owes, quoted here so it is not invented on release day**
(it is written by the release, `scripts/release.sh X.Y.Z`, not by any task above): *"Run `fugaro update`.
Nothing else is needed: a catalog image's job collects the stamp the next time that repository's own
resources converge with `fugaro update --repo .`, and an image built before this release passes
nothing to a run until then. `fugaro doctor --all` names what any job still drops."* If the image refresh is asked for during the update, the derived and catalog images
should be refreshed so the stamp and the image agree — the job is what carries the stamp either way.

**Check 291 is not a gate on the release and does not move the slot.** It is the evidence the owner runs
afterwards, and the register's rule is that the number free is taken **at the check's own merge**
(D1381).

## 6. Check 291 — the live check (owner-run, NOT RUN)

**One catalog entry on the sandbox, one run on it, and three facts read back.** It is owner-run by the
rule every live check in this repository follows: **no agent runs any step of it**, and it is taken on
the sandbox installation and the sandbox repository only, never `belong` (the repository the owner's own
work runs in).

1. Publish a catalog entry on the sandbox whose Dockerfile sets an ordinary marker
   (`ENV NEXUS_BASEURL=https://example.invalid/repository/maven-public/`) and one reserved name
   (`ENV GOOGLE_APPLICATION_CREDENTIALS=/dev/null`) — **no credential and no secret-shaped value on this
   check's image**, since the whole point is that an image is not a secret store.
2. `fugaro update --repo <sandbox repo>` so the job converges and carries the stamp, then
   `fugaro doctor --all` and read the job's line: the marker name is listed as passing, the reserved
   name is listed as dropped, and **no value appears**.
3. Run one trivial task with `checkout: clone` on the entry's image, and from the run's transcript read:
   the marker variable present in the environment of a command the agent ran through `fugaro verify`,
   and `GOOGLE_APPLICATION_CREDENTIALS` absent — **and the image's value nowhere in the transcript**
   (that is D1375's claim, and this step is what falsifies it if a later change prints values).
4. **Refutation step, and it is the one that matters:** a job on a **repository-built** image (the same
   sandbox repository, `dockerfile:` in its `fugaro.yaml`) whose Dockerfile sets the same marker variable
   — the variable must be **absent** from the run, and `doctor --all` must report that job as not
   catalog-stamped. This is D1370's scope, and it is the half a code review cannot settle.

**Number:** **Check 291** — the first number free above the register's highest **written** section
(**290**: `docs/gcp-live-checklist.md`'s `## Check 290`, the mixed-credentials run, and its
summary-table row — named by content and not by line, which is what the register's own paragraphs do
and what keeps a citation off a number the next insertion moves), and no open pull request claims
**291** itself: the claims other work holds above that
section start higher and leave 291 free (`docs/stateless-orchestrator`'s 300–309 and #1780's 311–314,
read 2026-11-06). **A claim, not a reservation**: the register's own rule is that whichever of two
claims merges second renumbers itself,
so the task that writes the section takes the number free **at that merge**, and writes both the
section and its summary-table row, the shape Check 290's own row has. **The section is written by that
task, not by this plan.**

## 7. Gates, and what a task may not start without

| Task | Gate |
| --- | --- |
| **IE1** | the owner's approval of this plan. Nothing else |
| **IE2** | IE1 merged, and **O3 and O4 answered on 2026-11-06** — no redaction loop (D1376's recommendation) and `BATCH_` added to `internal/config/validate.go`'s prefix list in this task, both of which are inside the diff a SEC task is reviewed for |
| **IE3** | IE1 merged. It may land **before** IE2 and does not wait on O3 or O4 |
| **IE4** | IE3 merged for the sentence and the skill clause; **IE2 merged for the behaviour half** — the sentence that says an image's `ENV` reaches a run is false until IE2 has landed, and a doc that states it earlier is the one failure mode this repository's doc discipline exists to prevent |
| **Check 291** | IE2 and IE3 deployed on the sandbox; **owner-run, and it gates only the claim that the feature works**, never the release (D1382) |

**If IE2 slips out of 0.20.0**, IE1, IE3 and IE4's rule-and-report half ship anyway, and the release is
strictly better than the tree it replaces: today the drop is silent and no command reads it, and after
IE3 the owner can see, per job, exactly what a run never received (D1383).
