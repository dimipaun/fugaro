# Removing the per-repository images: the design

**Status: a design, and nothing is built. Every decision in it is answered.** The owner's ruling of
**2026-11-04** — *"Completely disable repo-level images for now to make things simpler."* — is the
requirement; the reading of it into this tree is [reconciliation.md](reconciliation.md), which also
carries the owner's answers of the same day to all nineteen questions the ruling opened — and **where
this file and that one disagree, the reconciliation wins**.

**How a `:line` in this folder reads.** A citation names a file and a symbol, and the number is the
symbol's **declaration** line as it stood at `aa034db1`; a line drifts the first time anyone edits the
file above it, so the **symbol**, not the number, is what a reader should search for, and every claim
also states the fact in words.

---

## 1. What is removed

The removal is one change in four places: the configuration, the commands, the deploy path, and the
cloud objects the deploy path creates. Nothing here is conditional and nothing is kept behind a flag.

### 1.1 The configuration

Gone from `fugaro.yaml`, from a workflow:

| Key | What it is today | Declaration |
| --- | --- | --- |
| `image.*` (`apt`, `setup`, `tools`, `skip_build_scripts`, `key_files`) | the generated Dockerfile's build instructions | `internal/config/config.go:752` |
| `dockerfile` | the repository's own Dockerfile, one repository-relative path | `internal/config/config.go:604` |
| `rebuild.*` (`check`, `max_age`, `lockfiles`, `base`, `paths`) | the daily check's triggers for this repository's image | `internal/config/config.go:714` |

Gone from the **project layer file** too: the same `image.*` keys and `rebuild.*`
(`schemas/project-layer.schema.json:236` is the profile's `image` member) — a profile can set them, so
removing them from `fugaro.yaml` alone would leave a second door to the same build.

**What is not removed, and why it is named here:** `image_name` and `needs`
(`internal/config/config.go:619`, `:626`) are the catalog's own selectors and stay; `checkout`
(`internal/config/config.go:657`) keeps both its values (§2.2), and the `image.node`/`image.jdk`
tombstones stay with them — they are not settings but refusals with a migration sentence
(`internal/config/image.go:119`, `:122`), which is the mechanism the keys above reuse (C4, D1195).

The eleven scope rows that made the removed keys settable
(`internal/config/scope.go:108`, `:113`, `:131`, `:135`) went with them, and so did their entries in
`ExecutableKeys` and `NonExecutableImageKeys` (`internal/config/scope.go:179`, `:189`): no scope row, no
registry row and no settings-schema member stands for a removed key now. **Not every
`image.*` row goes**: `internal/config/scope.go:106` and `:107` are `image.node` and `image.jdk`, the
two tombstones that stay, so the removal's first scope row is `:108` and its last `:135`. A key that is
gone from the scope table is a key no settings command may write, which is the second half of the
removal and not a separate one.

### 1.2 The commands

| Command | Disposition |
| --- | --- |
| `fugaro image build` | **loses its repository arm.** It builds a catalog entry, named or all (`internal/cli/image_build_catalog.go:37`); the derived fall-through (`internal/cli/image.go:194`) is deleted, and with it `--workflow`, `--local`, `--base` and `--no-smoke`'s derived meaning |
| `fugaro image refresh` | **loses its repository-image steps**: `refreshCheckJob` (`internal/cli/image_refresh_steps.go:110`) and `refreshBuilds` (`:325`). The base pin and `refreshCloneJobs` (`:244`) stay — a `checkout: clone` job's image is the installation's base and still moves |
| `fugaro image check` | **loses its repository arms and keeps the catalog job's.** The local workflow check and the per-repository `--job` mode go with the repository image — they read `rebuild.*` and the repository's registry, and the job they serve is R4's. **`--job --catalog` stays** (`internal/cli/image_check_catalog.go:31`, merged as #1679): one run of the installation's catalog check job, reading its spec out of `catalogCheckSpecEnv` and deciding every entry of the project's catalog — that is D1148's driver, and not this removal's to delete |
| `fugaro image status` | **removed** (`internal/cli/imagecheck.go:982`) |
| `fugaro image render` | **removed** (`internal/cli/image.go:510`). It prints the Dockerfile the template generates, and there is no template |
| `fugaro image gate`, `fugaro image record` | **removed** (`internal/cli/image_record.go:60`, `:145`). They are the derived build's own steps; the catalog build carries its own pin, tag and record steps (`internal/backend/gcp/catalog.go:338`, `:376`, `:410`) and calls neither |
| `fugaro image selftest` | **stays** (`internal/cli/image.go:44`). It is the runtime-contract check the *catalog* build's smoke runs too (`internal/cli/image_build_catalog.go:208`), so it is the catalog's step now |
| `fugaro image git-credential` | **stays** (`internal/cli/gitcred.go:39`); the catalog build clones with it |
| `fugaro image ls`, `add`, `rm` | **stay unchanged** (`internal/cli/image_catalog.go:60`, `:286`, `:407`) |
| `fugaro image promote` | **removed**, after the migration (§3, D1192) |

### 1.3 The deploy path

- **The job's image stops being derived.** `internal/infra/spec.go:1019` currently computes
  `gcp.ImageName(c.registryPath(), slug, name) + ":latest"` for a `baked` workflow and lets the catalog
  override it; after the removal a `baked` workflow's image **is** the catalog's choice, and there is
  no fall-through left (`internal/infra/spec.go:807`).
- **The per-repository check job and its Scheduler go.**
  `deploy/terraform/gcp/modules/repo/check.tf:16` (`google_cloud_run_v2_job "check"`) and
  `:116` (`google_cloud_scheduler_job "check"`) are deleted, with their invoker grant and their
  variables; `internal/infra/spec.go:1451` (`repoCtx.check`) stops producing a `CheckSpec`
  (`internal/infra/spec.go:326`) at all. This **folds in the ruling** "no repo-level scheduled check
  unless there is a repo-level image": with no repository image there is nothing to check, so there is
  no repository-level schedule.
- **The per-repository build path goes, and the repository's own registry stays.** The writer and
  tag-mover grants a repository image needed, and the build account's submitter role for this path
  (`deploy/terraform/gcp/modules/repo/build.tf:5`), leave the module. The repository's own Artifact
  Registry repository (`deploy/terraform/gcp/modules/repo/registry.tf:10`) **does not**: the owner ruled
  that it stays in the configuration with its `prevent_destroy`, so an ordinary converge produces it
  exactly as it does today and no `--retire` takes it either (D1201). The **shared** registry
  (`deploy/terraform/gcp/modules/installation/registry.tf:93`) and the **base** registry stay for the
  reason they always did: a catalog build writes into the shared one and reads the base's.
- **Readiness stops asking for a repository image.** `MissingImage`
  (`internal/infra/readiness.go:30`) and the `hasLatest` probe against the repository's own registry
  (`internal/infra/readiness.go:731`) are replaced by "the chosen catalog entry has a `:latest` in the
  shared registry" (§2.3, D1203).
- **The Terraform root stops producing the check.** `internal/infra/repo.go` writes the repo root's
  tfvars from `RepoSpec` (`internal/infra/spec.go:395`); the `check` member goes with the resources it
  drove, and `deploy/terraform/gcp/roots/repo/main.tf:28` passes one fewer member.

### 1.4 Every path that writes the removed settings

- **The setup skill** (`plugin/skills/setup/SKILL.md:84`–`:91`) teaches the agent to write `image:` and
  `dockerfile:` into `fugaro.yaml`; it is rewritten to write a **catalog entry** instead (§8, D1205).
- **The plugin's reference documents** (`plugin/skills/setup/reference/services-and-images.md:110` and
  its neighbours) carry the same instructions and the same YAML examples, and
  `plugin/setup_skill_test.go:154` validates every example against the tree — so the examples are a
  test-visible surface, not prose.
- **`fugaro config init` writes none of these keys** (`internal/cli/config_init.go:21`): the minimal
  file it writes carries `version`, `project`, `gcp_project` and at most `git.base_branch` and
  `profile`. Nothing to remove, and this design records that rather than assuming it.
- **`fugaro setup`** itself builds the first images (`internal/cli/init.go:3415`). After the removal it
  offers a **catalog** build, not a derived one (§8, D1207); the `--no-build` flag keeps its meaning.
- **`fugaro image promote`** is the only CLI path that writes a workflow's image keys, and it is
  removed (§3).

### 1.5 The console and the website

The console's never-reachable command table lists the five image commands with a reason and a file
(`console/src/main/ipc.ts:1764`–`:1788`), pinned by `console/test/unit/ipc.test.ts:681`; the generated
CLI reference (`website/content/cli-reference.md`) lists every command and flag, pinned by
`internal/cli/docs_website_cliref_test.go:186` and regenerated-checked by `ee/clidoc/cliref_test.go:26`.
Both move with the commands (§8, D1204).

---

## 2. What stays

### 2.1 `checkout: clone` workflows

A `checkout: clone` workflow's job runs the installation's base image directly
(`internal/infra/spec.go:1023`) and always has: it never had a derived image, which is why the daily
check skips it (`internal/cli/imagecheck.go:241`) and why the refresh chain's `refreshCloneJobs`
(`internal/cli/image_refresh_steps.go:244`) moves its job image instead of rebuilding anything. It is
untouched by this design, base pin included.

### 2.2 The catalog, and the one thing this design changes about it

Catalog entries stay exactly as [docs/project-image-catalog/](../project-image-catalog/) designed them:
a name, free-form tags, one of three sources (`internal/config/catalog.go:59` — a Dockerfile at a
repository and path, an inline definition, or a prebuilt image), a description and a default marker;
selected per job by the tag-cover rule with `image_name` as a pin and `needs` as a requirement
(`internal/config/catalog.go:34`, `internal/config/image.go:180`). **A Dockerfile source in a
repository stays** — it is now simply the way a repository's toolchain reaches the catalog — and it is
what the two dogfood repositories migrate onto (§3).

**The one change: what `checkout: baked` may point at (D1191).** Today `baked` means "the job's image
is the image Fugaro built for this repository and this workflow", and the engine's own build is what
makes the image hold the checkout. After the removal `baked` still means "the job's image holds this
repository's checkout", but the image is the operator's catalog entry — so a `baked` workflow whose
chosen entry's source is a **prebuilt image** (`SourceImage`) is **refused at deploy time**, the way
the `docker` tag is refused on Cloud Run today, because nothing guarantees that image holds the
checkout. The two values of `checkout` and both their meanings are otherwise unchanged.

### 2.3 What decides a job's image after the removal

One rule, in one place:

1. `checkout: clone` → the installation's base image for that workflow's base kind.
2. otherwise → the catalog's choice: the entry `image_name` pins, else the entry covering `needs`,
   else the default, with the tag-cover order and the two refusals of
   [docs/project-image-catalog/reconciliation.md](../project-image-catalog/reconciliation.md) §3.

A project with no catalog and no `image_name` on a non-clone workflow is refused before the job is
deployed, naming `fugaro image add`. **Readiness** asks the same question one step earlier: that the
chosen entry's `:latest` exists in the shared registry — the catalog's counterpart of today's
`hasLatest` probe.

### 2.4 The build accounts, the shared registry and the D1148 schedule

- **The build accounts stay.** A catalog entry whose source is a Dockerfile in repository X is built
  **as X's build account** with X's credential and secrets
  (`internal/backend/gcp/catalog.go:79`, `internal/cli/image_build_catalog.go:366`), so the repository
  module keeps its build account and its grants on the **shared** registry, the base registry and the
  ghcr pull-through (`deploy/terraform/gcp/modules/repo/build.tf:5`) — narrowed by the grants that
  only existed for the repository's own registry.
- **`image_name` pins stay**, with their warning and their deploy-time enforcement (D1146).
- **The project-level D1148 scheduled rebuild stays** and becomes the *only* check: one installation-level
  scheduler over every catalog entry, the decision layer for which already landed
  (`internal/imagecheck/catalog.go:20`), with its Terraform piece still to come.
- **`rebuild:` does not move to the catalog (D1200).** `rebuild.max_age` was a *workflow's* trigger;
  after the removal there is no per-workflow image for it to describe. A per-entry age limit, if the
  catalog wants one, is D1148's or a catalog key's — not a repository's, and not this design's.

---

## 3. How a repository migrates, and the order

### 3.1 The two repositories that must not lose their image

Two repositories carry a per-repository image today, and both are the dogfood pipeline:

- **this repository**, whose `go` workflow sets `image.setup` (`./fugaro.yaml:43`);
- **the sandbox repository**, whose workflows the live checks and the release's smoke runs use.

**The order is the requirement, not a preference.** The catalog entry and the pin land **before** the
removal, so there is no commit, no release and no window in which a dogfood run's job points at an
image that does not exist:

1. **The entry is created and built** — `fugaro image add` with a Dockerfile source in the repository
   (or `fugaro image promote`, §3.2), then `fugaro image build <entry>`, so the shared registry holds
   `:latest` for it. Owner-run for the sandbox; a task for this repository.
2. **The workflow is repointed** — `image_name: <entry>` (or `needs: [<tag>]`) replaces the `image:`
   block in `fugaro.yaml`, and one run is launched and confirmed.
3. **The removal lands**, first in the configuration and the schemas, then in the deploy path, then in
   the commands.

Every later removal task is ordered behind those three steps for the two repositories named above; a
task that removes a path still carrying a dogfood workflow **fails its own red step** rather than
landing (D1193).

### 3.2 `fugaro image promote` already is the migration — and it is removed (D1192)

`fugaro image promote <repo> [name]` reads the repository's current definition — its `image:` block
rendered through `internal/image/render.go`, or its `dockerfile:` path — writes the catalog entry, and
repoints the repository in one commit (`internal/cli/image_promote.go:19`, `:179`). That is exactly the
migration this design needs, and it exists today.

**What the entry's Dockerfile has to do that a rendered one did for it** (D1208): promote writes the
entry's **source**, and for a repository whose image bakes its own dependency cache — this one's
`image.setup` is `go mod download && go build ./...` — the source is a Dockerfile that does the clone
itself. The build hands it exactly what the renderer used: `REPO_URL`, `BASE_BRANCH` and
`FUGARO_COMMIT` as build arguments and `git-credentials` as a file secret
(`images/derived/cloudbuild.yaml:178`), so the entry clones `$REPO_URL` at `$FUGARO_COMMIT`, warms
the cache, and ends with `finalize-checkout` (`images/derived/Dockerfile.tmpl:130`) — the last step of
every derived image build (`images/common/finalize-checkout.sh:2`). A Dockerfile source that skips that
last step produces an image whose checkout still carries a credential setting, which is the one thing
the migration can get wrong on its own (D1208, reconciliation §5).

**Recommendation: ship no migration command, and remove `fugaro image promote` in the same release as
the keys it reads.** The migration runs *before* the refusal (step 1 above), with the binary that
still has promote; after the removal there is no definition left for a migration command to read, and a
command that still read the removed keys would be the compatibility shim the ruling forbids. The
refusal a person meets names the forward path instead (`fugaro image add --dockerfile … --tags …` and
`image_name`), and `fugaro image add --inline` takes the very block the old `image:` held.

The alternative — a one-time `fugaro image migrate` (or a promote kept alive for one release) — is
priced in [reconciliation.md](reconciliation.md) D1192; it buys an automatic conversion for a
repository nobody has yet, at the price of a reader of removed keys living in the tree.

---

## 4. Retirement of the surviving cloud objects

The removal changes the **configuration**; the objects the old configuration created are still there.
They are destroyed by the flow the tree already has and by nothing else:

- **`deletion_protection` stays, and what an ordinary converge does with it is the point.** The check
  job keeps `deletion_protection = true` (`deploy/terraform/gcp/modules/repo/check.tf:27`) and the
  repository's registry keeps `prevent_destroy = true`
  (`deploy/terraform/gcp/modules/repo/registry.tf:10`) — the registry's guard is now protecting a
  resource that **stays in the configuration** (D1201), so nothing in this release asks it to destroy
  one. The guard is the **object's**, not the file's: R4 takes the check job out of the configuration,
  and the job that is still running carries the flag, which is why this is a retirement and not a
  delete. What the flag means for a job is not lost either — a repository's workflow jobs, which stay,
  carry the same `!var.allow_job_delete` (`deploy/terraform/gcp/modules/workflow/job.tf:15`).
- **So an ordinary converge of that repository stops before its apply, and the Scheduler survives that
  run with it.** The retired check job's state says `deletion_protection = true`, so
  `blockingJobs` (`internal/cli/init_retire.go:305`) names it and the run refuses with
  `fugaro update --repo . --retire` (`internal/cli/init_retire.go:327`, called from
  `internal/cli/init.go:3845`) — **before the apply**, which is why the Scheduler, which carries no
  protection of its own (`deploy/terraform/gcp/modules/repo/check.tf:116`), is not destroyed by an
  ordinary converge either: the run stops, rather than destroying what it can and failing on what it
  cannot. `fugaro update --repo <r> --retire` is the one path that finishes it: it removes the protected
  address from Terraform's state first (destroying nothing), applies — and **that** apply destroys the
  Scheduler — then deletes the job through the Cloud Run API once its labels say it is this
  repository's. `--plan-only` shows the destroy without acting on it. The operator consequence is named
  in [reconciliation.md](reconciliation.md) §5 and is not hidden here: until the owner runs it for a
  repository, that repository's converge is refused.
- **Retirement is owner-run: `fugaro update --repo <r> --retire`**, with the two typed confirmations
  `--yes` cannot cover (`internal/cli/init.go:444`; the flow is `internal/cli/init_retire.go:22` and
  the set it acts on is `internal/infra/retire.go:26`). A coding agent's session, a pipe, `--json` and
  `--non-interactive` cover neither.
- **It is never automatic.** No `fugaro update` converge, no `setup`, no managed gate and no run
  performs it; the retirement set is *printed* by an ordinary run and acted on only under `--retire`.
- **The plan shows the destroy and refuses it without the flag.** The retirement set names each address
  and whether it is protected (`internal/infra/retire.go:26`); a run without `--retire` prints the set
  and leaves it alone, and the Terraform plan is what names the destroy (D1194).

---

## 5. Schema, golden and skew handling

No compatibility, in the shape the tree already uses for a removed key.

- **`fugaro.yaml` validation** (`internal/config/config.go:1195` decodes strictly, so an unknown field
  is already an error) **refuses each removed key by name**, with a message that names the catalog
  command that replaces it — the pattern the `image.node` and `image.jdk` tombstones already set
  (`internal/config/image.go:119`, `:122`). The keys stay decodable as tombstones and their *refusal*
  is the message; a bare "field image not found" names nothing and is not what ships (D1195).
- **The schemas**: `schemas/fugaro.schema.json` loses the workflow's `image`, `dockerfile` and
  `rebuild` members (`schemas/fugaro.schema.json:332` is the `image` member the two 0.7.0 tombstones
  keep), and its mutual-exclusion clauses shrink to the pairs that survive (`image_name` with `needs`);
  `schemas/project-layer.schema.json` loses the same keys (`schemas/project-layer.schema.json:236` is
  its profile's `image` member). **`schemas/settings.schema.json` loses the removed keys' own member
  rows** — the twenty-one that sat between the two 0.7.0 tombstones the document keeps
  (`schemas/settings.schema.json:832`, `:1100`), beside their rows in
  `internal/settings/registry.go:229`, without which
  `fugaro settings set` would still offer a key the config refuses — **and keeps `base_images` and the
  flattened `image_catalog.*` rows untouched** (`schemas/settings.schema.json:547`), as
  `image_name`/`needs` keep their members (D1196). The two 0.7.0 tombstones are the control: a schema
  member and a registry row stand for each of them still, because what the config refuses and the
  document has to explain is a key the settings family may not write.
- **The goldens and the fixtures**: the **39** files under `testdata/` that still carry one of the
  three keys — `grep -rlE '^[[:space:]]*(image|dockerfile|rebuild):' testdata/` — are the removal's own
  list, in the two **invalid** directories (`testdata/config/invalid`,
  `testdata/project-layer/invalid`) once the *valid* ones have lost the key, as the sweep with R2 did;
  the *invalid* ones that assert an old refusal (`image-and-dockerfile.yaml`, the `legacy-image-*` pair,
  the `checkout-clone-*` set) are replaced by fixtures asserting the **new** refusal, and
  `internal/cli/testdata/init_retire.golden` is regenerated (D1197).
- **Skew.** `imageCatalogSince`/`imageCatalogShipped` (`internal/cli/recipes_skew.go:169`, `:176`) keep
  their meaning — a node carrying `image_catalog` read by an older CLI — and this design adds no second
  pair. What the removal *does* need is one sentence in the release's own notes: a launcher older than
  the removal reads a `fugaro.yaml` carrying the removed keys, so the checkout and the launcher move
  together. There is no wire-format version bump, because no wire shape is removed: `checkout`,
  `image_name` and `needs` are unchanged, and the removed keys are a repository's own configuration
  file, which the same binary both writes and reads.

---

## 6. The managed gate's rows (D1206)

`fugaro image refresh` is one of the seven commands the managed gate covers
([docs/managed-agent-converge/spec.md](../managed-agent-converge/spec.md) §3.1), and its row is
written against "derived images" and "the per-build list". The removal changes what that command does,
so its row changes with it, and the plan carries the amended rows:

| Command | Row after the removal |
| --- | --- |
| `fugaro image refresh` | admitted **per build**: the base pin, and a `checkout: clone` job's image move. The derived rebuilds it used to admit are gone with the builds |
| `fugaro image build` | admitted for a **catalog** entry — the entry exists in the effective catalog, the build account is the project's own for that entry's source repository, and the count is within D1013's `max_image_builds` bound |
| `fugaro image check` | **no row, and it never had one.** What is left of the command is `--job --catalog`, a job's own mode — the entrypoint the installation's catalog check job runs, not a command a session runs. The gate's set is the seven commands of §3.1, and a container's entrypoint is not one of them |
| `fugaro image status`, `render`, `gate`, `record`, `promote`, `migrate` | **no row**: the commands do not exist (and `migrate` is not written at all, §3.2) |
| `fugaro update --repo <r> --retire` | **never admitted.** A retirement is a delete, and D1026's rule that a `Typed` step is never admitted settles it twice over: the gate's first rule refuses a delete, and the typed confirmation is a step no plan gate may carry |

**A Typed step is never admitted (D1026), and a retirement is never admitted** — not for a managed
project, not for any flag, and not by inheritance from any other row. The row set is the
managed build's, and this design edits no file of that folder except by the one task that writes the
rows.

---

## 7. The safeguard: the owner-run live check that gates the 0.19.0 tag

**The owner runs it, and the tag waits for it.** It builds a catalog entry on the sandbox, launches a
run on it, and confirms the run — which is the one thing no unit test can show, because it is the
deploy path, the registry, the build account and the job all at once, after the path that used to exist
is gone.

**The named check — seven steps:**

1. Create a catalog entry on the sandbox with a Dockerfile source in the sandbox repository, and a
   second, prebuilt-image entry beside it.
2. `fugaro image build <entry>` builds the Dockerfile-sourced one; the image appears in the
   installation's **shared** registry with its content tag and `latest`.
3. A sandbox repository on `checkout: baked` with `image_name: <entry>` converges (`fugaro update
   --repo`), and its **job** carries the shared registry's image, not a `fugaro-…` tag in the
   repository's own registry.
4. A run launches on it and reaches its pull request: the toolchain the entry carries is present and
   the checkout is there.
5. A sandbox workflow with no catalog reference and no `image_name` is **refused before the job is
   deployed**, with the message that names `fugaro image add`.
6. A `baked` workflow pointing at the **prebuilt** entry is **refused before the job is deployed**.
7. `fugaro update --repo <r>` **without** `--retire` prints the retirement set — the check job, its
   Scheduler, the repository's registry — and destroys nothing; the destroy appears only in the plan
   `--retire` produces.

**Cost:** one sandbox project's build minutes and one run's spend; nothing created, enabled or deleted
outside the sandbox. Steps 1–4 are the check the owner ruled on; 5–7 are the same session's evidence
that the refusal and the retirement behave as this document says. The task that writes it into
`docs/gcp-live-checklist.md` is the plan's last one, and the file is lifted for that task only.

---

## 8. The skill, the plugin, the console and the website

- **The setup skill** (`plugin/skills/setup/SKILL.md:84`) stops writing `image:`/`dockerfile:` and
  writes a catalog entry: it asks what toolchain the repository needs, adds the Dockerfile the entry
  points at, and sets `image_name` (or `needs`) on the workflow. The skill's own rule that it "writes
  the Dockerfile only when needed" survives in the catalog's vocabulary (D1205).
- **The plugin's reference documents** are rewritten with it, and `plugin/setup_skill_test.go:154` — which
  validates every example against the real config and the real renderer — is the test that proves the
  examples still parse. `plugin/skills_lint_test.go:789` proves the commands the skills name still exist,
  which is what makes the removal of a command a skill change in the same pull request (D1205).
- **The console** drops the five image commands that no longer exist from its never-reachable table
  (`console/src/main/ipc.ts:1764`) and keeps the two that stay; `console/test/unit/ipc.test.ts:681` is
  updated with it. The console's contract with the CLI is unchanged in shape: the table is a list of
  commands it never runs (D1204).
- **The website's CLI reference** is regenerated (`website/content/cli-reference.md`), and
  `internal/cli/docs_website_cliref_test.go:186` plus `ee/clidoc/cliref_test.go:26` are what fail if it
  is not (D1204).
- **`fugaro setup`** offers a catalog build where it offered a derived one
  (`internal/cli/init.go:3415`); `--no-build` is unchanged (D1207).

---

## 9. The refusals a person reads

Three sentences, and their shape is the design's:

- **A repository still carrying a removed key**, at `fugaro.yaml` validation: the key's name, the fact
  that it was removed in this release, and the catalog command that replaces it —
  `fugaro image add --dockerfile <path> --tags <tags>` plus `image_name: <name>` on the workflow
  (D1195).
- **A non-clone workflow with no image**, at deploy time: the catalog is empty or covers nothing, and
  the message names `fugaro image add` and the missing tags.
- **The retirement**, without `--retire`: the addresses that would be destroyed, each with whether it
  is protected, and the one command that would destroy them.

None of the three names a variable, a flag or a marker a reader could change to get the old behaviour
back, because there is no old behaviour to get back.

---

## 10. What this design is not

- **Not code.** No Go, no Terraform, no schema, no rules change, and no live action. Every command in
  it is a proposal.
- **Not an approval.** Nothing is built until the owner approves the plan.
- **Not a replacement for the catalog design.** [docs/project-image-catalog/](../project-image-catalog/)
  stands; this design makes it the only image path and changes exactly one rule in it (§2.2).
- **Not a change to the managed gate's decisions.** D1011–D1026 are cited and given rows; none is
  re-opened.
- **Not a lift of any refusal.** `checkout: shared`, the agent-session refusals, the retirement's typed
  confirmations and the skew gates all stay exactly as they are.
