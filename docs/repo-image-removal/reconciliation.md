# Repository image removal: reconciliation against the code

**Status: a design, and every decision in it is answered.** **Owner 2026-11-04: moved to 0.19.0
because the live catalog check found #1701/#1703 first** — superseded by the note below. **Owner
2026-11-04 (after the 0.18.0 tag): moved again to 0.20.0 (0.19.0 = config management first).** The
owner's ruling of 2026-11-04 —
*"Completely disable repo-level images for now to make things simpler."* — is reconciled here against
the tree of **`777b902d`** (the merge of `main` this round read, 2026-11-04), and **where this file
disagrees with [spec.md](spec.md), this file wins**. Each decision **D1190–D1208** carries the owner's
answer of **2026-11-04**, the one-line reason it rests on, its recommendation and the cost of the
alternative and of the recommendation; the block and how it was read are §3.

---

## 1. The audit, against the tree

Each finding is **Exists**, **Missing** or **Conflicts**: what the brief assumed, read against the
code. A `:line` is a **declaration** line at `777b902d`; the symbol is what to search for.

- **F1: Exists. A job's image is per `(repository, workflow)`, a `:latest` tag in the repository's own
  registry.** `internal/infra/spec.go:1019` computes
  `gcp.ImageName(c.registryPath(), slug, name) + ":latest"`, `internal/backend/gcp/names.go:233` is the
  name function, and `internal/infra/spec.go:757` (`registryPath`) is the registry it lands in. Nothing
  about the image is project-level except the catalog's newer path.

- **F2: Exists. `image:` is the repository's build input and it has no name.**
  `internal/config/config.go:631` is the field, `internal/config/config.go:780` the block it holds
  (`apt`, `setup`, `tools`, `skip_build_scripts`, `key_files`, and the `node`/`jdk` tombstones
  `internal/config/image.go:119`, `:122`). The block is rendered into a Dockerfile by
  `internal/image/render.go:163`; nothing in it is addressable from outside the repository.

- **F3: Exists. `dockerfile:` is one repository-relative path, and only the repository's own.**
  `internal/config/config.go:632`, validated inside `validateImage` (`internal/config/image.go:109`),
  which refuses a path outside the repository. It is the escape hatch for exactly the repository that
  commits it.

- **F4: Exists. The catalog is the *other* path, and one predicate turns it off.**
  `internal/catalog/select.go:318` (`Applies`) returns false as soon as the workflow sets `image:` or
  `dockerfile:`, which is why `internal/infra/spec.go:807` (`catalogJobImage`) is reached for every
  workflow *except* the ones this design is about. The tree carries two image paths today, and that is
  finding F4's whole point.

- **F5: Exists. The catalog's definitions live in the project's configuration and are published.**
  `internal/config/catalog.go:25` (the type), `internal/localcfg/localcfg.go:236` (the
  `image_catalog` key), `internal/projectpolicy/policy.go:1131` (its validation where it is published).

- **F6: Exists. The catalog's deploy-time selection is built**, with the tag-cover rule and the two
  refusals: `internal/infra/spec.go:807`, and the config-side references
  (`image_name`, `needs`) at `internal/config/config.go:647`, `:626`.

- **F7: Exists, in two pieces and at two stages. The catalog's scheduled check has its decision layer
  and its job mode, and not yet its schedule.** `internal/imagecheck/catalog.go:20` (`CheckCatalog`)
  landed, and so has the job mode itself — `fugaro image check --job --catalog`
  (`internal/cli/image_check_catalog.go:31`, the spec it reads out of `catalogCheckSpecEnv`), merged as
  **#1679**; the Terraform piece the D1148 answer calls for — the installation-level Scheduler and its
  target — is open as **#1683**. `internal/cli/imagecheck.go:241` (`checkedWorkflows`) still decides
  what the **per-repository** check looks at, and that is the arm the removal takes.

- **F8: Exists, and the order has moved under it since this finding was written: the catalog is the
  shipped path on `main`.** `internal/cli/recipes_skew.go:176` and `internal/infra/readiness.go:762`
  both hold `imageCatalogShipped` **true** — "as of 0.18.0, the first release that ships the flip", its
  own comment says (`internal/cli/recipes_skew.go:171`), 0.17.0 having published the key with the gate
  still false. The pull request this finding named as open, **#1665**, is **merged**, and so is
  **#1679**, the catalog check job's mode. What is still open is **#1683**, D1148 (ii)'s
  installation-level schedule and its repo-module pieces, so the removal is sequenced behind **one**
  merge rather than behind a flag, and the plan's step 0 (§2) is already done. The safe direction this
  finding described is also the reason the dogfood migration can be planned rather than hoped for: a
  workflow that named an entry while the flag was false was **held back, not silently mis-imaged** —
  readiness refused the job and said why (`internal/infra/readiness.go:462`, the same words again at
  `:512`).

- **F9: Exists. One Cloud Run job per repository, one Scheduler, one name function.**
  `deploy/terraform/gcp/modules/repo/check.tf:16` (`google_cloud_run_v2_job "check"`), `:116`
  (`google_cloud_scheduler_job "check"`), `internal/backend/gcp/names.go:280` (`CheckJobName` →
  `fugarochk-<slug>`). It is per repository, not per project and not per workflow.

- **F10: Exists. The per-repository build path: the repository's own Artifact Registry repository, its
  build account, and the API-submitted Cloud Build.** `deploy/terraform/gcp/modules/repo/registry.tf:10`
  (`google_artifact_registry_repository "images"`), `deploy/terraform/gcp/modules/repo/build.tf:5`
  (`google_service_account "build"`), `internal/backend/gcp/build.go:169` (`BuildRequest`). There is no
  Cloud Build **trigger** anywhere in `deploy/terraform`, so "the per-repository build path" is code
  plus IAM, not a Terraform trigger.

- **F11: Exists. The repository's registry and its check job are protected from an ordinary converge.**
  `deploy/terraform/gcp/modules/repo/registry.tf:10` sets `prevent_destroy = true`, and
  `deploy/terraform/gcp/modules/repo/check.tf:27` sets `deletion_protection = !var.allow_job_delete`.
  This is what makes the retirement in §4 of [spec.md](spec.md) a separate, explicit act.

- **F12: Exists. Readiness asks for a repository image, by tag, in the repository's own registry.**
  `internal/infra/readiness.go:30` (`MissingImage`), `:731` (`hasLatest`), and the per-workflow
  decision at `internal/infra/readiness.go:445`. The catalog branch is already beside it
  (`catalogImageEntry`), which is what lets the removal **replace** the check rather than delete it.

- **F13: Exists. `repoCtx.check` is the one producer of the check spec.**
  `internal/infra/spec.go:1451` builds the `CheckSpec` (`internal/infra/spec.go:326`) that the module
  turns into the job and the Scheduler, from the head's workflows and the repository's registry.

- **F14: Exists. The image is fixed at deploy time, so every choice here is a deploy-time choice.**
  `internal/infra/spec.go:807`'s comment states it and `internal/infra/spec.go:1019` implements it: the
  job's image is written into the job, and a run cannot change it. A refusal for a bad image therefore
  has to happen where the spec is built, not where the run starts.

- **F15: Missing. There is no `fugaro image migrate`.** The only converter is
  `fugaro image promote` (`internal/cli/image_promote.go:19`), which reads the repository's definition
  and writes a catalog entry. The brief's example command does not exist, and the one that does is the
  one this design proposes to remove.

- **F16: Exists. Exactly two writers touch a workflow's image keys today, and neither is `fugaro
  config init`.** `internal/cli/image_promote.go:179` (`setWorkflowImageName`) removes `image:` and
  `dockerfile:` and writes `image_name`; the **setup skill** writes the keys by hand
  (`plugin/skills/setup/SKILL.md:82`, `:89`). `internal/cli/config_init.go:21` (`minimalFugaroYAML`)
  writes `version`, `project`, `gcp_project` and at most `git.base_branch` and `profile` — **no image
  key at all**, so the brief's "`config init`" is not a writer to remove.

- **F17: Exists. Retirement exists, and it is owner-run behind two typed confirmations.**
  `internal/cli/init.go:444` is the `--retire` flag on the `init`/`update --repo` door,
  `internal/cli/init_retire.go:22` the flow, `internal/infra/retire.go:26` the set it acts on, with
  `Protected` and `ProtectedRead` as the reading of `deletion_protection`. Nothing in the managed gate
  admits it.

- **F18: Exists. The scope table is what makes a key settable, and the image keys are in it.**
  `internal/config/scope.go:106`, `:108`, `:113`, `:131` are the rows, `:179` (`ExecutableKeys`) and
  `:189` (`NonExecutableImageKeys`) the classifications. Removing a key from `fugaro.yaml` and leaving
  its scope row would leave the settings commands able to write a key the config refuses.

- **F19: Missing. `internal/infra/cover.go` does not exist.** The brief names it; the cover logic is
  `internal/infra/tf/cover.go:22` (`allowedTypes`), whose test fails if a module plans a resource type
  the list does not carry, plus `internal/infra/tf/managed_cover.go:124` for the managed set. A removal
  that deletes resource types from a module must edit **that** file or its test fails.

- **F20: Exists. `fugaro image refresh` is one of the seven commands the managed gate covers**, and
  its row is written as "derived images" and "the per-build list"
  ([docs/managed-agent-converge/spec.md](../managed-agent-converge/spec.md) §3.1). The removal changes
  what the command does, so its row changes with it.

- **F21: Exists. The console and the website pin the command set.**
  `console/src/main/ipc.ts:1764`–`:1788` is the console's never-reachable table
  (`console/test/unit/ipc.test.ts:681` classifies each), and `ee/clidoc/cliref_test.go:26`
  (`TestCliReferenceIsCurrent`) regenerates-and-compares `website/content/cli-reference.md`, beside
  `internal/cli/docs_website_cliref_test.go:186`. Removing a command without moving these fails a test.

- **F22: Conflicts. `checkout: baked`'s image *is* the per-repository tag, so removing the tag changes
  what `baked` resolves to.** `internal/infra/spec.go:1019` computes it and `:1023` gives `clone` the
  base image instead. `checkout` keeps both its values, but what `baked` points at is no longer built by
  the engine — a decision, not an implementation detail (D1191).

- **F23: Exists. The project layer can set the same keys.** `schemas/project-layer.schema.json:265`
  (`image`) and `:338` (`rebuild`), validated through `internal/config/layer.go:328`
  (`ParseProjectLayer`, which refuses a key outside its scope). A profile is therefore a **second door**
  to the removed build, so the removal has four configuration surfaces to move — this repository's
  schema, the project layer's, the settings document's and the registry's — and not one.

- **F24: Exists. Two dogfood repositories carry the keys.** This repository's `go` workflow sets
  `image.setup` (`./fugaro.yaml:43`), and the sandbox repository's workflows are what the live checks run.
  Nothing in the tree enumerates a third, which is §5's residual.

- **F25: Exists, and it is what makes the migration possible at all. A catalog build hands the entry's
  Dockerfile every tool the renderer's own clone needs — the repository's URL and branch (the
  `_REPO_URL`/`_BASE_BRANCH` substitutions, `internal/backend/gcp/catalog.go:286`), the commit the
  copied definition was read at (the pin step writes it, `internal/backend/gcp/catalog.go:364`), and
  the `git-credentials` file secret on the shared build step
  (`images/derived/cloudbuild.yaml:178`, read from `/workspace/out/source-commit`).** So a Dockerfile in
  a repository can `git clone "$REPO_URL"` at `$FUGARO_COMMIT` with the build's credential and bake the
  same dependency cache the renderer baked — the covered migration is not a degraded one. What the
  renderer does that a hand-written Dockerfile does not is the closing step: `finalize-checkout`
  (`images/derived/Dockerfile.tmpl:130`, whose whole purpose is stated at
  `images/common/finalize-checkout.sh:2`: *the last step of every derived image build*), listed as a
  `critical` tool of the base image (`images/base/tools.tsv:14`). A migrated entry has to call it
  itself, and nothing tests that a third party's Dockerfile does (D1208, §5).

---

## 2. Where the brief's own reading was wrong

Six corrections, each a fact of the tree rather than an opinion:

- **C1.** **`internal/infra/cover.go` does not exist** (F19). The file is
  `internal/infra/tf/cover.go:22`, and its `allowedTypes` list plus
  `internal/infra/tf/managed_cover.go:124` are what a Terraform removal must satisfy.
- **C2.** **The migration command the brief offers as an example already exists under another name**
  (F15). `fugaro image promote` is exactly "write the catalog entry from the repository's current
  definition, as `fugaro image promote` does" — so the decision is not "add a migrate command" but
  "does the tree keep a reader of removed keys at all" (D1192).
- **C3.** **Readiness is not deleted, it is re-pointed** (F12). The catalog branch already exists beside
  the repository one; after the removal readiness asks the shared registry instead of the repository's
  (D1203).
- **C4.** **`image.*` includes two tombstones, and they are not settings.** `image.node` and
  `image.jdk` were removed in 0.7.0 and their fields exist only to refuse with a migration sentence
  (`internal/config/image.go:119`, `:122`). The removal takes the block and **keeps the tombstone
  mechanism**, because it is the mechanism the removed keys need (D1195).
- **C5.** **`fugaro config init` writes no image key and `fugaro setup` does not write them either**
  (F16). The writers to move are the setup skill's instructions and the catalog commands; `setup`
  *builds* images (`internal/cli/init.go:3415`), which is a different task (D1207).
- **C6.** **The `rebuild:` block's fate is a consequence, not a separate question.** It is this
  repository's triggers for *its own* image; with the repository-level check gone there is nothing for
  it to describe, and a per-entry age cap is the catalog's or D1148's (D1200).

---

## 3. The number block

The decision block is taken **above every `D<number>` on `main` and in every open work stream**, read on
**2026-11-04** by fetching each **branch** and reading it rather than by trusting a neighbour's table.

- **On `main`**, re-read in this round at `777b902d`, the highest **written** decision is **D1183**
  (`docs/config-management/reconciliation.md`, that work's block D1160–D1183) and the highest number
  **named** is **D1189**, its reserved tail. No `D119x` or `D12xx` appears anywhere in `docs/`.
- **In the open work streams**, re-checked by fetching each branch and reading it — the branch is the
  evidence. The highest number any of them names is again **D1189**, from the same
  configuration-management folder `main` carries; `docs/workspaces` (#1492), `docs/binary-allow-rule`
  (#1491), `docs/periodic-reviews` (#1490), `docs/mcp-server` (#1487), `docs/plugin-user-scope`
  (#1488) and `docs/run-without-checkout` (#1481) all sit below D1027, and the managed-mode, settings
  and catalog **code** streams (#1663, #1665, #1666–#1673, and the open #1683, #1685 and #1686) write
  no new decision. **D1190 is therefore free.**
- **This work takes D1190–D1219: D1190–D1208 are written here, and D1209–D1219 are reserved and
  unused** — the thirty-wide width `docs/ee-reorg` took for D520–D549. A later task that needs a number
  re-checks this section's facts rather than its conclusion.

---

## 4. The decisions

**The owner answered all nineteen on 2026-11-04.** Seven of them — D1190, D1191, D1192, D1195, D1201,
D1202 and D1208 — are **answered as recommended**, or with the one correction the answer itself states;
the other twelve — D1193, D1194, D1196–D1200 and D1203–D1207 — carry **`default-by-owner: not
objected`**, which is what it says: the owner read the recommendation and did not object, so it stands
as the design's default and **is not a ruling** — a later reader may still raise one. Each entry keeps
its `Recommendation` and both costs, so the answer and the price of the other reading sit together.

- **D1190 (what exactly is removed, and in how many pieces) — ANSWERED by the owner, 2026-11-04, as
  recommended: all four places, in 0.20.0.** The reason, in the owner's own terms: this is a removal and
  not a deprecation, so splitting it across releases would leave both image paths alive and the ruling's
  simplification unpaid.
  Recommendation: the four places of [spec.md](spec.md) §1 — the **configuration** (the `image.*`
  block, `dockerfile:`, `rebuild.*`, the scope rows, the registry rows and the three schemas), the
  **commands**
  (`image build`'s derived arm, `image refresh`'s derived steps, `check`, `status`, `render`, `gate`,
  `record`, `promote`), the **deploy path** (the derived image name, the per-repository check job and
  its Scheduler, the derived build path's grants, readiness) and the **writers** (the setup skill, the
  plugin, the console, the website) — in one release, in the order [spec.md](spec.md) §3.1 gives:
  configuration, deploy path, commands.
  Cost of the alternative: leaving the commands alive while the configuration refuses produces a
  command that can never succeed, and the refusal would then be the only thing telling two stories.
  Cost of the recommendation: 0.20.0 carries a Terraform change, a CLI change and two schema changes
  at once, and the Terraform one is a **SEC** task that a reviewer reads line by line.

- **D1191 (`checkout: baked` after the removal, and the one rule the catalog gains) — ANSWERED by the
  owner, 2026-11-04: keep both `checkout:` values, and refuse a prebuilt entry on a `baked` workflow at
  deploy time.** The reason: `checkout` is a wire field and `CLAUDE.md` forbids re-meaning one, and a
  prebuilt image is the one source kind nothing guarantees holds the checkout. **The refusal is a new
  deploy-time refusal, so R1 is a SEC task.**
  Recommendation: `checkout` **keeps both values** (`internal/config/config.go:685`) and both their
  meanings; a `baked` workflow's image is the **catalog's** choice, and a `baked` workflow whose chosen
  entry's source is a **prebuilt image** (`SourceImage`, `internal/config/catalog.go:71`) is **refused
  at deploy time**, where the spec is built (F14), the way the `docker` tag is refused on Cloud Run
  today.
  Cost of the alternative: collapsing `checkout` to one kind changes a wire field's meaning — which
  `CLAUDE.md`'s rule forbids outright — and a repository that loses its preinstalled toolchain also
  loses the reason its runs are fast, so every dogfood run would install its dependencies again.
  Cost of the recommendation: a new refusal to write and test, and it has to be **complete**, because
  a prebuilt entry that passes it deploys a job whose first minute fails.

- **D1192 (how an existing repository migrates) — ANSWERED by the owner, 2026-11-04, as recommended: no migration command.** The reason: the dogfood repository and the sandbox repository run `fugaro image promote` **before** the removal lands, and the removed key's refusal names `fugaro image add`.
  Recommendation: **no migration command.** The migration is the two dogfood repositories' steps of
  [spec.md](spec.md) §3.1, run with the binary that still has `fugaro image promote`
  (`internal/cli/image_promote.go:19`) **before** the removal lands, and `promote` is removed in the
  same release as the keys it reads. The refusal a person meets names the forward path:
  `fugaro image add --dockerfile <path> --tags <tags>` (or `--inline` with the block the old `image:`
  held) plus `image_name: <name>`.
  Cost of the alternative: a `fugaro image migrate` — or a `promote` kept for one release — is a
  **reader of removed keys** living in the tree, which is the compatibility shim the ruling forbids; it
  has to be deleted again later, so the removal costs two releases instead of one, and the second
  release's removal has no fixture of its own.
  Cost of the recommendation: a repository nobody migrated by hand has no automatic path — its owner
  reads the old `image:` block out of git history and passes it to `fugaro image add --inline`, which
  is a hand conversion, not a lost image.

- **D1193 (the order, so the dogfood pipeline never loses its image) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: for this repository and the sandbox repository, **the entry, its build, the
  `image_name` pin and one confirmed run come first** — a task for this repository, an owner-run live
  step for the sandbox ([spec.md](spec.md) §3.1) — and every removal task is sequenced behind them and
  behind **D1148's two tasks**, which change the same Terraform, `internal/infra/spec.go`,
  `internal/infra/tf/cover.go` and `internal/imagecheck`.
  Cost of the alternative: landing the removal first and migrating afterwards is the failure the brief
  names — a dogfood run whose job points at an image that does not exist — and every later task would
  be tested against a pipeline that is already broken.
  Cost of the recommendation: the first task of the series changes no engine code (it is a Dockerfile
  and a pin), so the series opens with a commit whose review value is its ordering.

- **D1194 (retirement) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: retirement stays **owner-run only** — `fugaro update --repo <r> --retire` with the
  two typed confirmations (`internal/cli/init.go:444`) — it is **never automatic**, **never** admitted
  by the managed gate, and `deletion_protection`
  (`deploy/terraform/gcp/modules/repo/check.tf:27`) and `prevent_destroy`
  (`deploy/terraform/gcp/modules/repo/registry.tf:10`) are unchanged. The plan shows the destroy and refuses it without the flag.
  Cost of the alternative: an ordinary converge destroying them removes an Artifact Registry
  repository that may hold a live job's image and the versions a rollback needs, with no confirmation
  and no record — which is why the two protections exist.
  Cost of the recommendation: an installation keeps an idle check job, an idle Scheduler entry and an
  empty registry until the owner runs one command, and that costs money in the meantime.

- **D1195 (what a removed key says when it is read) — ANSWERED by the owner, 2026-11-04, as recommended: a removed key is a refusal that NAMES the catalog command.** The reason: a person who meets the refusal has to be told what replaces the key, and a decoder's own words name nothing.
  Recommendation: each removed key becomes a **tombstone** in the config — the mechanism
  `image.node`/`image.jdk` already use (`internal/config/image.go:119`, `:122`) — and its refusal names
  the release and the catalog command that replaces it. Strict decoding
  (`internal/config/config.go:1223`) is what makes a tombstone necessary: the field has to exist for
  the refusal to be the message rather than the decoder's.
  Cost of the alternative: deleting the fields makes every removed key read as *field image not found*,
  which names no replacement, reads as a typo and gives a person nothing to do next.
  Cost of the recommendation: the removed keys stay in the tree as names for at least one release, so
  "removed" means "refused and named", not "absent from the source".

- **D1196 (the schemas, and the rows that make the keys writable) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: `schemas/fugaro.schema.json:358` loses `image` (`:396` for `dockerfile`, `:511` for
  `rebuild`) and its mutual-exclusion clauses (`:306`) shrink to `image_name` with `needs`;
  `schemas/project-layer.schema.json:265` loses the same keys (`:338` for `rebuild`);
  `internal/settings/registry.go:229` loses the twenty-one rows that make the keys writable (`:234`,
  `:254`, `:258`, and the `layerRow`s at `internal/settings/registry.go:454`, `:458`, `:477`, `:481`),
  and `schemas/settings.schema.json:1145` loses their twenty-one members (`:832`, `:1181`) — a key
  with no scope row and no registry row is a key `settings set` must not offer. `image_catalog.*`
  (`schemas/settings.schema.json:547`), `base_images` and the catalog's own rows are untouched.
  Cost of the alternative: a schema that still allows a removed key is the file a person's editor
  validates against, so it would let a `fugaro.yaml` through that the CLI refuses — the schema would be
  the one document that lies about the removal — and a surviving registry row would make
  `fugaro settings set workflows.web.image.setup` a writer for a key the config refuses.
  Cost of the recommendation: both schemas **and** the registry change in the same commit as the
  validators, so the removal cannot be split across two tasks, and this is also where the removal
  meets G0/G1 of [docs/config-management/implementation-plan.md](../config-management/implementation-plan.md)
  in `internal/settings/registry.go` (§4).

- **D1197 (the fixtures and the golden) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: the **50** files under `testdata/` carrying one of the three keys
  (`grep -rlE '^[[:space:]]*(image|dockerfile|rebuild):' testdata/`) are the task's own list. The
  **invalid** fixtures that assert an old refusal — `image-and-dockerfile.yaml`, the `legacy-image-*`
  pair, the `checkout-clone-*` set — are **replaced** by fixtures asserting the new refusal, and
  `internal/cli/testdata/init_retire.golden` is regenerated.
  Cost of the alternative: deleting the invalid fixtures leaves the new refusal as the one behaviour of
  the removal with no fixture, and the removal's message is the only thing a person will read.
  Cost of the recommendation: 50 fixture files move in one commit, which is a large but purely
  mechanical diff, and every one of them is a file some test names.

- **D1198 (skew) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: **no new skew pair.** `imageCatalogSince`/`imageCatalogShipped`
  (`internal/cli/recipes_skew.go:169`, `:176`) keep their meaning and are not duplicated; the removed
  keys are a **repository's own `fugaro.yaml`**, read and written by the same binary, so the removal's
  skew is a launcher-and-checkout pairing, which the release's `### For operators` states in one
  sentence. No wire shape is removed: `checkout`, `image_name` and `needs` are unchanged.
  Cost of the alternative: a `repoImageSince`/`repoImageShipped` pair exists to keep an *older reader*
  working against a *newer document*; here the older reader is the older binary, which the release's
  pin and `fugaro update` already move together, so the pair would be a constant with no document to
  guard.
  Cost of the recommendation: an installation that upgrades the checkout before the CLI meets a refusal
  that names an upgrade, which is the ordinary direction of every skew in this tree.

- **D1199 (the per-repository check job and its Scheduler) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: **removed**, with the ruling it folds in — *no repo-level scheduled check unless
  there is a repo-level image* — and `checkedWorkflows` (`internal/cli/imagecheck.go:241`) and
  `repoCtx.check` (`internal/infra/spec.go:1451`) go with them. The catalog's rebuilds are the
  project-level schedule D1148 answers, and nothing per repository survives.
  Cost of the alternative: keeping one job per repository to check **catalog** entries makes N
  repositories race over one shared registry and one build bound, and it is a second scheduler doing
  the job the D1148 answer put at installation level — two schedules, one subject.
  Cost of the recommendation: a project all of whose entries are prebuilt images gets a scheduled check
  that fires nothing, which is D1148's subject and not a reason to keep a repository-level one.

- **D1200 (`rebuild:`'s disposition) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: **removed entirely** with the repository-level check, and a per-entry age cap, if the
  catalog wants one, is D1148's or a catalog key's — never a repository's.
  Cost of the alternative: keeping `rebuild.max_age` as a repository key read by the project-level check
  means an **entry** inherits the age cap of whichever repository happens to name it — one image,
  several caps, decided by reading order — which is exactly the coupling the catalog exists to end.
  Cost of the recommendation: a repository that wanted a shorter rebuild interval for its own toolchain
  has no knob until the catalog gets one.

- **D1201 (the repository's own Artifact Registry repository) — ANSWERED by the owner, 2026-11-04: it
  STAYS in the Terraform configuration, `prevent_destroy` and all, and only the owner's typed
  `fugaro update --repo <r> --retire` may ever take the object.** The reason: the registry holds the
  images a rollback needs, and taking a `prevent_destroy` resource out of the configuration is the one
  change no ordinary converge may make — and the recommendation below is **corrected here**: its first
  sentence used to say the registry leaves the configuration, which is what R4 contradicted.
  Recommendation: the registry **stays in the configuration**, `prevent_destroy` and all, and the
  removal's Terraform task touches neither `deploy/terraform/gcp/modules/repo/registry.tf:10` nor its
  guard. **R4 stays as written**: it removes the check job, its Scheduler and the derived build path's
  grants from the same module.
  An ordinary converge of a repository that still holds one therefore produces the registry exactly as
  it does today — it is in the state and in the spec, so it is **not** in the retirement set, and no
  `--retire` takes it either. The installation's shared and base registries
  (`deploy/terraform/gcp/modules/installation/registry.tf:93`) are untouched the same way.
  Cost of the alternative: removing the resource from the configuration in this release would destroy a
  `prevent_destroy` object, which the provider refuses at apply time — the release would ship a converge
  that cannot finish, the failure D1194's design exists to avoid. Keeping it costs one empty registry per
  repository, with its cleanup policies and its IAM, so the removal's promise "one image path" is true
  in the configuration and not in the bill.
  Cost of the recommendation: an installation keeps paying for an empty registry per repository for as
  long as it keeps the repository — a real cost, named rather than hidden — and a reader who wants it
  gone has to plan a second, destructive change with its own retirement.

- **D1202 (the build account) — ANSWERED by the owner, 2026-11-04, as recommended: the build accounts stay, with NARROWED grants.** The reason: a catalog entry is built as its source repository's account, and one installation-level account for every catalog build would hold every repository's credential at once. **This is a SEC Terraform task, and its test is an exact-set one** — the grants the module leaves are the grants the list names, no more and no fewer.
  Recommendation: **the build accounts stay** (`deploy/terraform/gcp/modules/repo/build.tf:5`) with
  their grants narrowed to the **shared** registry, the base registry and the ghcr pull-through. A
  catalog entry whose source is a Dockerfile in repository X is built as **X's** build account with X's
  credential and secrets, which is what the tree already enforces
  (`internal/backend/gcp/catalog.go:79`, `internal/cli/image_build_catalog.go:366`).
  Cost of the alternative: one installation-level build account for every catalog build would hold
  **every** repository's provider credential and every workflow's secrets in one identity, which is the
  segregation the per-repository account exists for.
  Cost of the recommendation: a catalog entry stays **coupled to its source repository** — its build
  account, its credential and its secrets — so a project-level schedule must build each entry as its
  source repository's account, and a repository that is retired takes its entries' ability to rebuild
  with it. This is handed to D1148 as a constraint, and §5 repeats it.

- **D1203 (readiness for an image) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: **re-pointed, not deleted.** The repository-image probe (`MissingImage`,
  `internal/infra/readiness.go:30`; `hasLatest`, `:731`) becomes "the chosen catalog entry has a
  `:latest` in the **shared** registry", which is the catalog branch `internal/infra/readiness.go:449`
  already reads.
  Cost of the alternative: dropping readiness for images altogether deploys a job pointing at an entry
  that has never been built, moving the failure from the operator's terminal to a run's first minute —
  the failure readiness exists to prevent.
  Cost of the recommendation: readiness now depends on the shared registry's contents rather than on a
  registry the repository owns, so the probe and the build account's reader grant must agree.

- **D1204 (the console and the website) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: the console's never-reachable table drops the commands that no longer exist and keeps
  the two that stay (`console/src/main/ipc.ts:1764`; `console/test/unit/ipc.test.ts:681` moves with it),
  and `website/content/cli-reference.md` is **regenerated in the same task**
  (`ee/clidoc/cliref_test.go:26`, `internal/cli/docs_website_cliref_test.go:186`).
  Cost of the alternative: leaving the console's table alone is survivable at run time — it is a list of
  commands the console never runs — but the website's reference has a regenerate-and-compare test, so
  "leave it" is not available, and a stale console row is a reason string naming a command that is not
  there.
  Cost of the recommendation: the console's command classification is regenerated by a person rather
  than by a command, so the task has to run the console's own `npm run test:unit` to prove it.

- **D1205 (the setup skill and the plugin) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: the skill writes a **catalog entry** instead of the removed keys
  (`plugin/skills/setup/SKILL.md:82`, `:89`), its reference documents carry the new examples, and the
  two tests that pin them are the task's red step (`plugin/setup_skill_test.go:154`,
  `plugin/skills_lint_test.go:789`).
  Cost of the alternative: leaving the skill teaching `image:`/`dockerfile:` makes it the one writer in
  the tree that produces a `fugaro.yaml` the CLI refuses, and the person it happens to is the one
  following the instructions.
  Cost of the recommendation: the skill's decided shape — add an entry, point the workflow at it — is
  more steps than "write a block", and the skill has to make that legible.

- **D1206 (the managed gate's rows) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: the rows of [spec.md](spec.md) §6 — `fugaro image refresh` re-written to what it still
  does, an `fugaro image build` row for the catalog arm and the D1013 bound, no row for a command that
  no longer exists, and **`--retire` never admitted**. **A Typed step is never admitted (D1026)**, and a
  retirement is never admitted, which the gate's own first rule (no deletes) already settles.
  Cost of the alternative: leaving the `image refresh` row as it stands is a row that admits builds
  which no longer exist, and the gate's promise — that a command's whole effect is visible in a document
  it produces before it writes — becomes a promise about a command the gate can no longer read.
  Cost of the recommendation: a row set changes in the same release as the commands, so the managed
  build's own task order has to take the removal into account, which the plan's §5 states.

- **D1207 (`fugaro setup` and its first-build offer) — default-by-owner: not objected, 2026-11-04.** No answer came back on this one, so the design's recommendation stands as the default — on the reason the recommendation itself gives below, which is the reason a reader should re-check before building it.
  Recommendation: `setup` offers a **catalog** build where it offered a derived one — `fugaro image add`
  then `fugaro image build <name>` — and `--no-build` keeps its meaning
  (`internal/cli/init.go:3415`).
  Cost of the alternative: leaving the first build to a separate command contradicts the HARD RULE that
  the one documented command brings an installation to the new version with no other step.
  Cost of the recommendation: `setup` now has to sequence a catalog write and a build around a project
  whose catalog may be empty, which is more states than "submit the first derived image".

- **D1208 (how a migrated entry bakes the repository's own dependency cache) — ANSWERED by the owner, 2026-11-04, as recommended: the entry's own Dockerfile does the clone, the warm-up and a final `finalize-checkout`, and the engine gains NO bake mode.** The reason: an engine-side bake keyed by repository re-creates the per-repository build input inside the catalog, which is the thing the ruling removes.
  Recommendation: **the entry's Dockerfile does it, with what the build already hands it** (F25) —
  `git clone "$REPO_URL"` at `$FUGARO_COMMIT` under the `git-credentials` secret, then the dependency
  warm-up, then `finalize-checkout` as the last step, exactly as the renderer's template does today
  (`images/derived/Dockerfile.tmpl:130`). The engine gains **no bake mode**: a catalog build keeps
  cloning nothing of its own, which is its stated rule (`internal/backend/gcp/catalog.go:39`), and the
  dogfood migration of R0 is written as that Dockerfile.
  Cost of the alternative: an engine-side bake keyed by repository — "clone this repository at this
  commit before running the entry's Dockerfile" — re-creates a **per-repository build input inside the
  catalog**, which is the thing the owner's ruling removes, arrived at from the other side; the two
  paths would differ only in name.
  Cost of the recommendation: the clone recipe and the `finalize-checkout` call become the
  **repository's** responsibility, so an engine change to the checkout contract has to be repeated by
  hand in every entry's Dockerfile, and nothing in this tree tests that a third party's Dockerfile ends
  with it — which is §5's residual.

---

## 5. The residual this design refuses to hide

- **The two removals are not the same size, and one of them is destructive.** Removing the *code* is
  reversible and reviewed; destroying the check job, its Scheduler and a repository's registry is not,
  and it is one command away for the owner. This design does not make that command safer — it only keeps
  it owner-run, typed and out of the gate.
- **The `finalize-checkout` call is the migration's one hand-carried step (F25, D1208).** The renderer
  ended every derived image with it; a migrated entry's Dockerfile has to, and nothing here tests a
  third party's Dockerfile for it. An image that omits it is an image whose checkout still holds a
  credential setting, which is the hazard the script exists for.
- **A third repository may carry a per-repository image and nobody knows it.** The tree enumerates two
  (F24); nothing enumerates the rest, because a per-repository image is invisible outside its own
  repository. Everyone else meets the refusal at `fugaro.yaml` validation and converts by hand
  (D1192). There is no inventory to check, and this design does not invent one.
- **The catalog entry is coupled to its source repository (D1202).** A project-level entry whose source
  is a Dockerfile in repository X is built as X's build account, with X's credential and secrets. Retire
  X, or remove X's build account, and the entry can no longer be rebuilt — a project-level artifact with
  a repository-level dependency. The D1148 schedule has to live with it, and this design hands it over
  as a constraint rather than pretending the catalog is fully project-level.
- **The `baked` refusal (D1191) is only as good as its completeness.** A prebuilt entry that passes it
  deploys a job whose first minute fails. The refusal is a list of source kinds, and a source kind added
  to the catalog later has to be added to it.
- **An ordinary converge of a repository that had a check job stops before its apply, and that is where
  this design meets the HARD RULE.** The check job's state carries `deletion_protection = true`
  (`deploy/terraform/gcp/modules/repo/check.tf:27`), so once R4 takes it out of the configuration the
  address is retired **and protected**: `blockingJobs` (`internal/cli/init_retire.go:305`) names it, and
  `retireRefusal` (`internal/cli/init_retire.go:327`, called from `internal/cli/init.go:3845`) stops the
  run **before the apply**, naming `fugaro update --repo . --retire`. Two consequences are named here
  rather than hidden. The unprotected Scheduler (`deploy/terraform/gcp/modules/repo/check.tf:116`, which
  carries no `deletion_protection` of its own) is **not** destroyed by that run either, because the run
  stops before the apply; it is destroyed by the apply `--retire` runs, and by nothing else. And an
  installation whose repositories had a per-repository image needs the owner's `--retire` **per
  repository** before its converge completes, which is an operator step beyond `fugaro update` — while
  `CLAUDE.md`'s HARD RULE says a release must not ship one. This design states the tension and refuses
  to settle it alone: the readings are (a) the removal's release teaches `update` to perform the
  retirement inside its own converge, with the typed confirmation D1194 keeps and no second command for
  the operator to remember, or (b) it ships the step and says so in `### For operators`. **Picking
  between them is a new decision, not one of these nineteen**, and no task of the plan may land before
  it is picked.
- **Unit tests cannot show the deploy path.** The registry, the build account, the job and the run are
  one artefact, and the hermetic fakes are written from the API documentation. That is why the 0.20.0
  tag waits for the owner's live check rather than for a green suite, and why the check is a step of the
  plan rather than a claim of this document.
