# Image setup skill: the spec reconciled against the code

**Status: the owner's answers to D1900–D1906 were given 2026-11-09; nothing is implemented and no task is approved.**
Checked against `origin/main` at `871ce0569` on 2026-11-09. **Where this file disagrees with [spec.md](spec.md), this file wins.**
Findings are `F1…`, decisions `D1900–D1929` (this work's block; D1907–D1929 reserved and unused), checks `500–509` (reserved; none taken).
A citation names a file and a symbol, never a line.

## 1. The headline: most of the spec's machinery already exists, and one ruling is reversed

| Spec concept | What the repo already has |
| --- | --- |
| A small project-level image set | The **project image catalog** (`internal/config/catalog.go` `Catalog`/`Entry`, sources `dockerfile`/`inline`/`image`, tags, one default), built and merged ([docs/project-image-catalog/](../project-image-catalog/README.md)) |
| One function picks the project image | `catalog.Applies` / `catalog.MayApply` (`internal/catalog/select.go`) and `repoCtx.catalogJobImage` (`internal/infra/spec.go`); the final choice is an inline `if` chain in the job-spec builder that falls back to `gcp.ImageName(...) + ":latest"`, the per-repo image |
| Repo-level images off | [docs/repo-image-removal/](../repo-image-removal/README.md): **removal**, owner 2026-11-04, R0/R1 merged |
| Runtime contract, any base | Catalog D1151 (`FROM` unchecked) and the post-build smoke in `internal/backend/gcp/catalog.go`; the contract list is in [docs/base-image.md](../base-image.md) |
| Build secrets never in layers | Catalog D1150: BuildKit secret mounts through the workflow-secret path |
| Nightly rebuild in Cloud Build | `deploy/terraform/gcp/modules/installation/catalogcheck.tf` (D1148) |
| Dockerfile for a repo's own CI file | `docs/catalog-build-context/` (`context:` key; CB1 merged) |
| Docker-needing repos on VMs | `internal/backend/batch`, and the catalog's `docker` tag (D1149) |
| Skill | `plugin/skills/setup` is **per repository** and loops on a **local** image build |

## 2. Findings

- **F1 Rule: the spec reverses a recorded ruling.** [docs/repo-image-removal/](../repo-image-removal/README.md) D1190–D1208 (owner 2026-11-04) remove the per-repo derived image, its check job and its refusals. The spec says "disabled, not removed". Resolved by **D1900**: the spec wins; R2–R10 are parked (see F2).
- **F2 Exists/Wrong: nothing today "disables" without removing.** `MayApply` returns false the moment `image:` or `dockerfile:` is set, so a repo with those keys still gets a per-repo image: the opposite of "always returns the project image". The disabled state needs a new resolver, not a flag on the old path.
- **F3 Gap: the "one function" does not exist.** The choice is spread over `catalog.Applies`, `catalogJobImage` and an `if` chain around `gcp.ImageName`. The plan adds one `jobImage`-style function that the `if` chain, the doctor checks and the image commands all call.
- **F4 Gap: "ignores any repo-level config" collides with a repo's `image: <name>`.** `image: <name>` and `needs:` are *catalog references*, not repo-level images, and the shipped path honours them. **Open tension T1** below.
- **F5 Gap: there is no repo-to-image map at project level.** Mapping lives in each repo's `fugaro.yaml`. **D1906** adds `image_map` to the published policy node (`internal/projectpolicy/policy.go`, beside `ImageCatalog`). Adding a field is allowed by CLAUDE.md; an older binary decodes strictly, so the field is read only after `update` and is listed in the release's operators section.
- **F6 Wrong: "Baked repos" is not a catalog setting.** The catalog has no bake list; `checkout: baked` is the *per-repo derived image* that holds a clone, and removal D1208 makes a catalog entry's own Dockerfile do the clone. **D1901**: a project policy boolean (default true) says the skill's generated Dockerfiles clone each mapped repo.
- **F7 Gap: the job logic "repo in image → fetch and check out; else clone" is not in the runner as stated.** The runner reads `FUGARO_CHECKOUT` (`internal/runner`, `checkout: baked|clone`); detecting a baked copy by presence is new. UNVERIFIED how `finalize-checkout` behaves for a catalog image whose clone was made by the entry's Dockerfile (U1).
- **F8 Wrong: "Remove the other harnesses".** The base image carries three Node harnesses (`@google/gemini-cli`, `@mariozechner/pi-coding-agent`, `@qwen-code/qwen-code`) in `images/base/harnesses/`, documented as *present but not driven*. Removing them is one Dockerfile stage and its tests (`images/base_test.go` pins their entries). Claude Code stays. One defensible answer: stated, not asked.
- **F9 Rule: "recipe" is already a Fugaro word.** `docs/recipes.md` is the task-loop recipe (`implement`, `review`, `fix`). The spec's "recipe" (the contract) is called **the runtime contract** everywhere in this folder; no document here uses "recipe" for it.
- **F10 Gap: validation covers builds, not prebuilt entries.** The smoke runs after a catalog **build**; a `source: image` entry is never built, so nothing validates it. The plan adds a Cloud Build smoke-only step for prebuilt entries.
- **F11 Rule: the spec's "never local" conflicts with the setup skill.** `plugin/skills/setup` validates with a local image build. The new skill uses only the cloud catalog build (`fugaro image build`), never the local one; the architecture rule is stated in the skill.
- **F12 Gap: test services have no Fugaro-side start.** Services today come from the repo's own `image.setup`. The spec's "Fugaro sets the flag and starts the services" needs an entry key and a runner step (**D1904**). That step changes the agent's environment, so it is security-sensitive.
- **F13 Gap: survey at 50+ repos needs file reads, not clones.** `gitprov.ContentsReader` exists (GitHub and Bitbucket); no command lists a project's repos with signals. **D1902**: a read-only `fugaro image survey` command.
- **F14 Exists: build-time secrets are done.** No plan work, except the generated Dockerfiles must use `RUN --mount=type=secret` and a skill test pins that no `ARG`/`COPY` carries a token.
- **F15 Rule: HARD RULE on `fugaro update`.** Changing the base image, adding policy fields and rebuilding baked images must all converge through `fugaro update` with a test that an installation at the previous version converges, and the run-in-flight safety stays (`TestUpdateLeavesARunInFlightRunning`). Every task touching policy or images carries that test.
- **F16 Gap: reruns need prior decisions to compare to.** They are the catalog (`image_catalog`), the map (`image_map`) and the bake boolean: all published and operator-owned. The skill reads them through the existing policy read and proposes only differences.
- **F17 Open: Cloud Batch for repos that truly need Docker.** The catalog's `docker` tag already routes to Batch (D1149); the spec's "60–70 s vs 10 s, double cost" figures are the owner's and are used in skill text only as "about", UNVERIFIED (U2).

## 3. Decisions

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1900** | The spec disables repo-level images; repo-image-removal removes them. Which wins? | Spec wins: one resolver, repo definitions ignored with a warning, code kept; R2–R10 parked | Removal wins: contradicts "keep the code" | **2026-11-09: spec wins, disable and keep the code (the recommendation).** Newer ruling |
| **D1901** | Where is "Bake your repos?" stored? | Project policy boolean, default true | Per-entry repo list: more config for 50+ repos | **2026-11-09: project policy key (the recommendation).** Matches "stored as a project setting" |
| **D1902** | How does the skill read 50+ repos? | Forge file reads through `ContentsReader` | Local clones: user clones 50 repos first | **2026-11-09: forge file reads (the recommendation).** No clone |
| **D1903** | Where is "fewer than five images" enforced? | The skill only | Catalog validation: a hard limit on hand curation | **2026-11-09: skill only (the recommendation).** Hand-editing stays free |
| **D1904** | How are test services declared? | A catalog entry key plus the workflow's existing `env:` | New `services:` workflow key; or a script only | **2026-11-09: entry key plus workflow env (the recommendation).** Project-level, not per-repo |
| **D1905** | Where does the skill live? | New `fugaro:images` skill | Extend `fugaro:setup` | **2026-11-09: new skill (the recommendation).** `setup` is per repo |
| **D1906** | Where is the repo-to-image map? | Policy node `image_map` | Each repo's `fugaro.yaml`: 50 PRs | **2026-11-09: policy node map (the recommendation).** One change |

Resolutions that are not questions: harness removal (F8), the word "recipe" (F9), cloud-only builds (F11), build secrets (F14), the HARD RULE tests (F15).

## 4. Open tensions and open items for the owner

- **T1 (F4, D1900 against D1906).** D1900 says the resolver "ignores any repo-level config"; D1906's recommended text had a repo's own `image:` reference still win over the map. Choices: **(a)** the map is authoritative and a repo's `image: <name>`/`needs:` is also ignored with a warning, which keeps the spec literal and makes every existing catalog-referencing repo change behaviour; **(b)** only the repo-level *definition* keys (`image:` block, `dockerfile:`, `rebuild:`) are ignored, a catalog reference still beats the map, which keeps shipped repos working and makes "always the project image" true only for repos with no reference. The plan is written for **(b)** as the narrower change and **(a)** is a one-line change in IS1. **The owner rules.**
- **O1.** Bake default yes means every `fugaro update` rebuilds all baked entries; the rebuild is behind its own confirmation (CLAUDE.md HARD RULE). Is a nightly baked rebuild (D1148's schedule) enough, or should `update` also rebuild? The plan does the schedule only.
- **O2.** What the owner's Rocket Core-sized survey does for a repo with no readable lockfile (private submodule, monorepo): the plan maps it to the default image and flags it; confirm.

## 5. UNVERIFIED

- **U1.** How `finalize-checkout` and the runner treat a catalog entry's pre-cloned repo (F7): read `internal/runner` before IS7.
- **U2.** The Batch start-time and cost ratio in the spec.
- **U3.** Whether `docs/backends.md`'s "cloud-run is the only valid value" is stale (code for Batch exists under `internal/backend/batch`).
- **U4.** The exact name of the repo-image build and refresh commands to refuse under D1900 (`internal/cli/image_refresh.go`, `image.go`); IS2 re-reads them.
