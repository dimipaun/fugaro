# Repo-override rulings (lift L8): reconciliation

Checked against `origin/main` at `0dec0bb36` on 2026-11-23. **Where this document disagrees with
[spec.md](spec.md), this document wins.** The spec is lift L8 of the repo-coupling audit
([report.md](../repo-coupling-audit/report.md) §7): it applies the owner's rulings D3237 on the
benign candidates B1–B19, and it proposes to carry lift L13 as well (its §9).

The owner answered the spec's own questions Q1–Q9 on 2026-11-23 (D3290–D3298). The six decisions this
reconciliation raised (D3299–D3304) were answered the same day, each as recommended.

## 1. Assumptions

- A1. "The routing table decides" in D3237's B3 row means what spec §5.1 says: the project layer
  (`defaults.agent.model`, `defaults.agent.models.*`, `defaults.agent.crafts.*.model`) decides which
  model a role uses, and model-credentials-routing's table (`model_routing`, `internal/routing`)
  decides which source serves that model. Nothing in L8 writes the routing table.
- A2. An "enrolled repository" is one the published policy admits (`projectpolicy.Policy.AllowRepos`,
  `projectpolicy.AdmitRepo`) — the set D3239 has `update` work over for L5. **Amended by D3306:**
  `update` can migrate only the repositories it can **list**: the local config's repositories and
  every concrete name in the published admits. An admit that is a pattern (`*`, `github/acme/*`,
  `github/acme/app-*`; `projectpolicy` accepts them) cannot be expanded into names, so a repository
  admitted only by a pattern is not migrated.
- A3. The B18 ceiling is, for daily and for per-run separately, the global cap when it is set and
  otherwise the default repository cap (spec §5.6). "Unless the global cap allows more" (D3237) is
  read as: a set global cap is the ceiling.
- A4. `<owner-repo>` in D3295 is the readable half of a repository's slug (`task.Slug`: "acme/app"
  becomes `acme-app`), lower case. **Amended after review (D3302):** the readable half alone is not
  unique — `task.Slug` merges "acme/app-web" and "acme-app/web" into one readable `acme-app-web` and
  keeps them apart only by its 16-hex hash — so the copy carries the first 8 hex of that hash.
- A5. L13 sits in wave 2 by D3238; carrying it inside L8 (spec §9) needs the owner's go (D3299).
- A6. Release A and release B are two consecutive CLI releases (D3298); every image-age gate below
  names the release that carries its side, as the `*Since` constants of
  `internal/cli/recipes_skew.go` do.
- A7. "Refused" in the spec means the strict-decode style refusal of `validate` and the launch, with
  one sentence naming the fix, never a prompt (`docs/no-prompt-policy/`).

## 2. Scope check and capability map — approved (D3299)

The spec bundles capabilities that ship and are verified separately: B18's cap ceiling touches only
the budget package and the database rules; B4's tighten rule needs no migration; L13 needs no
migration and has its own consumers. They share one choke point (`config.Resolve`, plus
`config.AgentOf` for the readers that bypass it, F25, and the scope
table) and one `update` stage, so they stay one spec, split into modules. The map below is proposed
for the owner's approval; the plan is written on it, and every finding, decision and task names a
module id. **Approved by the owner on 2026-11-23 (D3299).**

| Module id | Responsibility | Depends on |
| --- | --- | --- |
| `lift-keys` | The list of lifted keys and their phase; the scope rows marked; `config.Resolve` drops them (release A) and refuses them on the default branch (release B); every reader of report §5 proved; the writers refuse them | — |
| `migrate` | `fugaro update`'s L8 stage: reads every enrolled repository's default branch, runs each item's planner, prints one line per repository that changes before any write, writes, and changes nothing on a second run | `lift-keys` |
| `model-choice` | B3: the model keys and `agent.levels.lN.model` (D3291) move to the project layer | `lift-keys`, `migrate` |
| `review-tighten` | B4: the tighten-only merge, against the project's value or the run's mode default | — |
| `ab-repos` | B9: `ab.repos` in the layer's `ab:` block, read by ee/ab | `lift-keys`, `migrate` |
| `rigor-repos` | B11: `rigor.repos` and member areas, the new precedence (D3294), `Configured` per repository, the writers | `lift-keys`, `migrate` |
| `project-recipes` | B16: repository recipes move to the flat project store (D3295), each repository pointed at its own copy, the launcher and runner stop reading `.fugaro/recipes/` | `lift-keys`, `migrate` |
| `repo-caps` | B18: the repository cap's ceiling, at the writer, at every read and in the rules | `migrate` |
| `dead-keys` | L13: `model_grades.*.model` in a repository's file and `agent.compression` refused, their merge plumbing removed | `lift-keys` |

Build order: `lift-keys` and `review-tighten` first (the contracts of §6 let `migrate`'s planners
run beside them), then the items, then release B.

## 3. What the spec names and what the repository has

| Spec concept | What the repository has (`origin/main`) |
| --- | --- |
| "Mark the lifted rows `Retired: InRepo`; `config.Resolve` drops them" | `config.ScopeRow.Retired` and `RetiredWhy` (`internal/config/scope.go`); `config.RetiredKeyNotes` (`internal/config/retired.go`) **names** a retired key and "decides nothing" — a retired key "still decodes and still validates (D3063)". Model-credentials-routing's retired rows (`agent.auth`, craft and grade `access`) are still honoured on purpose until its X2 (F1) |
| "`RetiredKeyNotes` names them in `validate`, `doctor` and the launch" | No caller of `RetiredKeyNotes` outside a docs test (`internal/cli/docs_credentials_auth_list_test.go`) (F2) |
| "The runner resolves the default branch's file itself" | The runner resolves its whole config from the task's ref (`internal/runner/runner.go`, the default arm reads the work tree's `fugaro.yaml` after `CheckoutNewBranch(spec.Ref)`), from `origin/<ref>` for a follow-up, a review or an evaluate run (`(*run).readBaseConfig`, `internal/runner/followup.go`); `(*run).defaultBranchFile` is read only for the policy and the project check (`(*run).resolvePolicy`, `internal/runner/project.go`) (F3) |
| `gitprov.Provider.Contents` | `gitprov.ContentsReader` and `gitprov.TreeReader` are optional interfaces beside `Provider`, implemented by the GitHub, Bitbucket and file-backed providers (`internal/gitprov/gitprov.go`) (F17) |
| B4 "baseline: the run's rigor mode default" | The mode is resolved per run and per round from the touched paths (`rigor.Resolve`; `internal/runner/plan.go` `setRounds` with `rigor.DefaultRounds`, `rigor.AdversarialDefault`), never inside `config.Resolve` (F4) |
| B11 "`fugaro rigor set <mode> --repo acme/app`" as the fix | `--repo` today names the repository of a `--layer repo` write (`internal/cli/rigor.go` `rigorOptions.repo`); nothing writes `defaults.rigor.repos` (`internal/cli/writers_labels.go` `reasonRigorRepos`: "a file a person writes") (F13) |
| B16 per-repository prefix `fugaro/recipes/repos/<slug>/` | Replaced by D3295 (one flat namespace). The store is `recipe.ObjectKey` = `fugaro/recipes/<name>.yaml`; a recipe name is `recipe.NameRE` (`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`: no dot, 40 characters) and its `name:` must equal the file name (`internal/runner/recipe.go`, `internal/cli/recipes_resolve.go` `parseRecipeAt`) (F5) |
| "A test runs each reader on a file that writes every lifted key" | `TestEveryConsumerResolvesTheSameBytes` (`internal/cli/consumers_test.go`) already drives every consumer through the runner's own `runner.ResolveConfig` (F22) |
| L13's `agent.compression` refusal | Already refused: `config.Validate` refuses any non-empty `Agent.Compression` (`internal/config/validate.go`), and report RC22 says so (F16) |
| B18 "one rules version, published by `update`" | The rules template and its marker exist (`internal/budget/rules/rules.go`, `rules.json.tmpl`, `marker.go`); the lease check reads `config/caps/repos/$slug` (`rules.go`) |

Every other path and symbol the spec names was found on `origin/main`: `config.Resolve`, `overlay`,
`mergeLevel` (`internal/config/resolve.go`); `config.Parse` (which is `Resolve(data, nil)`);
`policy.Merge` and `Effective.Ignored`; `config/pins.go` `pins`, `levelPins`, `craftModels`;
`craft.ModelRungs`; `ee/ab` `launchAgent`, `abRate`, `abBlock`; `config.validateRepoAB`;
`rigor.Config.ProjectRepos`, `Configured`, `RepoBlock`, `rigor.Member`, `areaNameRE`;
`Config.RigorConfig`; `recipes_skew.go` `launchLevelsConfig`, `refuseRigorSkew`, `repoRecipeForGate`,
`checkImageSince`; `recipes_resolve.go` `resolveRecipe`, `refuseFollowUpRepoRecipe`; `run.go`
`repoRecipeAtRef`, `repoRecipeFrom`, `cascadeRefConfig`, `defaultBranchPolicyLayer`; `recipes.go`
`localRoot`; `validate_recipes.go`; runner `resolveRecipe`, `readRepoRecipe`; `settingsAccessGate`;
`config.MaxFirstLineRounds` (3); `config.Efforts`; `config.ResolveLevels`; `ResolvedLevel.Inserted`;
`budget.Caps.RepoDaily`, `RepoPerRun`, `PerRun`, `budget.PathCapsRepo`; every reader of spec §6;
`policy.tightenGrades`, `Config.RepoModelGradesSet`; `TestScopeTableMatchesDocs`;
`TestUpdateLeavesARunInFlightRunning`; every document of spec §8.

## 4. Findings

### `lift-keys`

- **F1 (Wrong).** Spec §4 has release A mark the lifted keys `Retired: InRepo` and has
  `config.Resolve` drop them. On `main` "retired" means named and still honoured
  (`RetiredKeyNotes`: "decides nothing", D3063), and model-credentials-routing relies on that: its
  retired `agent.auth`, craft `access` and grade `access` drive a run until its release 2 (MCR X2).
  A `Resolve` that dropped every `Retired: InRepo` row would cut MCR's bridge, and marking the
  lifted rows `Retired` breaks MCR's own test of the retired list. → R1 (amended after review).
- **F2 (Wrong).** "`config.RetiredKeyNotes` names them in `validate`, `doctor` and the launch": no
  command calls it today; the comment in `scope.go` says `update`, `validate` and `doctor` print it,
  and none does. L8 has to wire it. → R1, task S2.
- **F3 (Wrong).** "The runner decides … the runner on the rebuilt image resolves the default
  branch's file itself (`defaultBranchFile`, `resolvePolicy`)": the runner resolves the model, the
  rigor and the levels from the task's ref, or from `origin/<ref>` (F3's row in §3);
  `defaultBranchFile` feeds only the policy layer and the project check. The principle still holds,
  because the drop lives in `config.Resolve`, which every ref's file goes through. A launch with
  `--ref feature` whose file writes a lifted key is dropped the same way. → R2.
- **F4 (Gap).** B4's baseline "else the run's rigor mode default for that key" cannot be computed in
  `config.Resolve`: the mode is resolved from the run's paths and can rise within a run (`rigor.Resolve`,
  `Request.Reached`). → R3.
- **F15 (Gap).** The new layer member `ab.repos` (and nothing else L8 writes, on D3303's
  recommendation) meets every reader that decodes the layer strictly (`config.ParseProjectLayer`,
  `KnownFields`). A runner before L8 is kept off by the launch's `abReposSince` gate (spec §5.3). A
  **CLI** before L8 fails to read the published layer at all: a CLI at L1's release or later is
  refused earlier, with `brew upgrade`, by the stamp; a CLI before L1 is not fenced (L1 Q2: the
  owner accepted that gap) and fails with a decode error naming `repos`. The spec relies on no
  `projectpolicy.Version` bump, and none is needed. → R9.
- **F18 (Gap).** Spec §7 says a CLI before L1 is harmless because the runner decides and the cap
  clamp holds. That is true of the keys (F3), but two launcher-side readers decide before the runner
  does: ee/ab's `abRate` (the sample is drawn at launch) and the recipe the launcher embeds
  (`resolvedRecipe.taskRecipe`). A pre-L8 launcher honours `ab.rate: 0` (the safe direction) and
  sends a repository recipe by name (`task.Recipe{Source: repo}`), which only the runner can then
  judge. → R8.
- **F19 (Gap).** The window between `brew upgrade` and `fugaro update`: a release-A CLI drops the
  lifted keys before the project holds their migrated values. The spec's `l8Since` gate (§7) refuses
  a launch on a pre-L8 image where the default branch writes a lifted key, and until `update`
  rebuilds them every image is pre-L8, so the window is closed for keys. It is open for B16: a
  default branch that holds `.fugaro/recipes/` writes no lifted key, so the gate as specified lets
  that launch through and the release-A launcher no longer reads the folder. → R16.
- **F25 (Wrong, review round 2).** Spec §2's "one choke point: every reader reads the file through
  `config.Resolve` or `config.Parse`" is not true of the agent block: `config.AgentOf` decodes the
  repository's own file strictly and bypasses `Resolve`, and four readers use it for model choice —
  `internal/cli/craft.go` (`craft ls`'s layers), `validate.go` `validateCraftLayers`,
  `budget_stops.go` (`config.UnpricedPins`) and `run.go` `agentAtRef`. (`accounts_rm.go` also calls
  it, for `agent.oauth_token` alone, which is MCR's.) → R21.
- **F22 (Exists).** `TestEveryConsumerResolvesTheSameBytes` is the home of spec §6's reader test.
  → task I1.
- **F23 (Gap).** Spec §6 lists the writers' refusal without a release; §5.1 puts `models use
  --layer repo` under release B. A write that release A's runs drop is a trap. → R15.

### `model-choice`

- **F6 (Rule).** D3237's B3 ruling reverses model-credentials-routing's F7 ("the repository keeps
  its model choice"). That folder must say so in a dated amendment naming D3237. → R10, task A1.
- **F20 (Rule).** Model-credentials-routing is mid-flight on the same files: its MG5 rewrites the
  retired notes in `internal/config/scope.go` and both schemas, its X2 edits `config.go`, `layer.go`,
  `validate.go` and both schemas, its MG6 edits `models_use.go`. → open item O2.

### `review-tighten`

- F4 above.

### `rigor-repos`

- **F11 (Gap).** `rigor.Select` deduplicates the areas it matched **by name**, keeping the strictest.
  A moved repository area that takes the name of an area the project layer already has (any area,
  not only another repository's moved one) would merge with it. The 40-character limit
  (`areaNameRE`) also binds a name with a repository appended. → R4.
- **F12 (Gap).** Today a repository's own areas are read at their own rank, so a repository rule can
  **lower** a project area's level for its paths (D20 as amended). After the move, and with D3294,
  a moved area and an existing project area naming the same repository sit at one rank, where the
  strictest wins: such a path's level rises. → R5.
- **F13 (Gap).** The refusal's fix "`fugaro rigor set <mode> --repo acme/app`" does not write
  `defaults.rigor.repos` today, and nothing does. → R6, task RG3.

### `project-recipes`

- **F5 (Gap).** D3295's renamed copy `<name>.<owner-repo>` is not a recipe name: `recipe.NameRE`
  has no dot and 40 characters at most, `resolveRecipe` refuses the name before it looks anywhere,
  and a recipe's own `name:` must equal it. A runner before L8 parses an embedded recipe with the
  old grammar and would refuse a dotted one. → D3302.
- **F7 (Gap).** "Keeps each repository pointed at its own copy" has no mechanism yet: a repository's
  `agent.recipe`, its tasks' `--recipe` and a follow-up's inherited name all say `review`, and
  `update` cannot edit a repository's file. → D3303.
- **F8 (Gap).** The runner never reads the project store ("a project recipe reaches a run only
  through fugaro run, which embeds it", `internal/runner/recipe.go`). Three launches today leave a
  repository recipe to the runner: a first run launched from outside a checkout whose ref's file
  names `agent.recipe` (`repoRecipeAtRef` reads the ref; `chooseRecipe` reads only the checkout),
  a follow-up that inherits a repository recipe by name (`carryChosenRecipe`,
  `refuseFollowUpRepoRecipe`), and any run that falls back to the name `default`, which resolves
  to `.fugaro/recipes/default.yaml` first. Once the copy lives in the project store, each of those
  must embed it. → R7.
- **F9 (Gap).** A task that names a repository recipe (`task.Recipe{Source: repo}`) from a launcher
  before L8 reaches a release-A runner; the task carries no launcher version to judge it by. → R8.
- **F10 (Gap).** A renamed copy's text differs from the repository's file in its `name:` line, so
  its sha256 differs; acceptance 2's "the same recipe text" holds for every line but that one. → R14.
- **F21 (Gap).** The owner's clash rule names "two repositories, different files, one name". A
  flat publish also changes other repositories' runs when the name is one they already resolve
  elsewhere: an existing project recipe of that name, a catalog name (the catalog's `default`
  first of all — a repository's `.fugaro/recipes/default.yaml` is today that repository's default
  for every run), since the store outranks the catalog for every repository of the project. → D3301.

### `repo-caps`

- Spec §5.6 checked: `Caps.PerRun` already takes the smaller of the repository and the global
  per-run caps, so the per-run clamp changes nothing where a global is set; the daily repository
  cap above a set global daily cap is bounded by the global. No finding.

### `migrate`

- **F17 (Wrong).** The read is `gitprov.ContentsReader.Contents` for `fugaro.yaml` and
  `gitprov.TreeReader.Tree` for `.fugaro/recipes/`, optional interfaces a host may lack; a
  `TreeFile` carries no file mode, so "each regular file" cannot be judged from it (U3). → R12.
- **F24 (Gap).** L5 (D3239) has `update` read every enrolled repository's default branch too. Two
  plans writing two readers of the same files is the duplicate CLAUDE.md warns of. → contract C2,
  open item O1.

### `dead-keys`

- **F16 (Exists).** `agent.compression` is refused already (`config.Validate`); L13's half for it is
  the scope row and the schema. Its other half removes `Config.RepoModelGradesSet`, which the
  model-grades skew gates read (`refuseModelGradesSkew`, defined in `recipes_skew.go` and called
  from `init.go`), and the repository half of `tightenGrades` (`internal/policy/grades.go`, called
  from `merge.go`'s `Merge`). → R11.

### Release shape

- **F14 (Rule, open tension T1).** Spec §4 says "Before L8: L1. The required-version stamp must have
  shipped." L1's Q2 recommendation was "L8 at least one release after L1, plus a
  `projectpolicy.Version` bump"; the owner answered "accept the gap" (not the recommendation). The
  answer is plain about the bump and about pre-L1 CLIs; it does not say whether release A may ride
  L1's own release. If it does, every CLI below that release is pre-L1, so the stamp fences nobody
  against L8 at all. → D3300.

## 5. Decisions

### The owner's answers to the spec's questions (2026-11-23)

| # | Question | Recommendation | Answer (2026-11-23) |
| --- | --- | --- | --- |
| D3290 | Q1. B3: repositories disagree and the project is silent | The value most repositories write; ties to the cheaper model in the price table; every repository that moves named before the write | **As recommended** |
| D3291 | Q2. `agent.levels.lN.model` | B3's rule (lifted, then refused); effort, `none` and focus stay tighten-only | **As recommended.** Adds `agent.levels.l1–l3.model` to the lifted keys |
| D3292 | Q3. "Tighter" for `agent.recipe` | Any project or catalog recipe, guarded by the levels and the owner's floor | **As recommended** |
| D3293 | Q4. `agent.workspace_beat` | Keep, replace rule, outside the tighten-only list | **As recommended** |
| D3294 | Q5. Rigor precedence after the move | A project area with a member naming this repository ranks above `rigor.repos` | **As recommended.** Amends LD4's order (`docs/project-layer.md`, `rigor.Resolve`) for projects that use both |
| D3295 | Q6. B16 store | A per-repository prefix | **One flat namespace. Not the recommendation.** Follow-up, as recommended in the follow-up: on a clash (two repositories, different files, one name) `update` renames the clashing copies `<name>.<owner-repo>`, keeps each repository pointed at its own copy, and prints one line per rename; no admin step. Followed through: spec §5.5's per-repository prefix, its "Why a per-repository prefix" paragraph, the launcher's "per-repository prefix before the flat names" and §8's resolution order are replaced by the flat store, the clash rule (D3301) and the pointer (D3303); acceptance 7 reads "from the project store" unchanged |
| D3296 | Q7. Repository cleanup | The diff in `doctor`, `validate` and the launch | **As recommended** |
| D3297 | Q8. `ab.repos` values | Any rate in `[0, ab.rate]` | **As recommended** |
| D3298 | Q9. Release B timing | The release after A, behind the image-age gate | **As recommended** |

### Raised by this reconciliation — answered 2026-11-23

| # | Question | Recommendation, and why | Alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| D3299 | Approve the capability map of §2, with L13 inside L8 as module `dead-keys` (spec §9)? | **Approve.** L13 edits the same scope rows, schema, `Resolve` and refusal wording, needs no migration and waits on L1 too; two plans would write one table twice | L13 stays its own small spec in wave 2 (D3238 as it stands): the `dead-keys` tasks DK1–DK2 leave this plan; cost: a second plan over `scope.go`, `resolve.go` and the schema, and two refusal styles | **As recommended (2026-11-23).** The capability map is approved and L13 is module `dead-keys`, because one plan then writes the scope table and the refusal wording once; amends D3238's wave-2 placement of L13 |
| D3300 | May release A ride L1's own release? (F14, T1) | **No: release A is cut at least one release after the release that carries L1.** That is what spec §4's "must have shipped" says, and it is what makes the stamp fence the CLIs of L1's release against L8 | Release A rides L1's release; cost: no CLI is fenced against L8 at all, and the lift rests wholly on the runner, the clamp and the image gates (F18, F19) | **As recommended (2026-11-23).** Release A is cut at least one release after L1's, so the stamp fences the CLIs of L1's release against L8; settles T1 |
| D3301 | Under D3295, what is a clash? (F21) | **A name some other repository of the project already resolves to different text: another repository's file, an existing project recipe, or a catalog recipe.** Each clashing copy is renamed `<name>.<owner-repo>`; a name nothing else answers is published flat. This keeps every run where it was (spec §2) | Only two repositories' files (the follow-up's words); cost: a repository's `default.yaml` or a catalog-named file published flat becomes every other repository's recipe of that name on the next launch | **As recommended (2026-11-23).** A clash is any name another repository already resolves to different text (its file, a project recipe or a catalog recipe), so no other repository's runs move |
| D3302 | How is the renamed copy spelled, given `recipe.NameRE`? (F5) | **The owner's spelling, `<name>.<owner-repo>`**: `recipe` gains a second grammar for a project copy (a recipe name, one dot, the readable slug; up to 81 characters), the launcher and the runner accept it, and a launch that embeds one is gated on the image (`recipeCopySince`) | `<name>--<owner>-<repo>` inside today's grammar; cost: not the owner's words, and the 40-character limit forces a truncation with a hash for most real slugs | **As recommended (2026-11-23).** `<name>.<owner-repo>` stays, with the copy-name grammar and the `recipeCopySince` gate, because it is the owner's own spelling. **Amended after review, the owner's answer of 2026-11-23:** the copy is `<name>.<owner-repo>-<8 hex>`, the 8 hex the first eight of the repository's `task.Slug` hash, because the readable half merges "acme/app-web" and "acme-app/web" (`task.Slug`'s own comment) and the hash keeps them apart. Length: a name of at most 40, the dot, a readable half of at most 46 (`maxSlugReadable` = 63 − 1 − 16), the hyphen and 8 hex: **at most 96 characters** (the 81 first written was wrong). The amended form is the owner's own answer (O4 closed) |
| D3303 | How is each repository "pointed at its own copy"? (F7) | **A lookup rule, no new data:** for repository R, the launcher resolves `<name>.<owner-repo of R>` in the store before `<name>`, then the catalog. Nothing in the layer changes, so no strict-decode skew (F15) and no new writer | A map in the project layer (`recipes.repos.<owner/name>.<name>: <copy>`); cost: a new layer member every pre-L8 reader refuses, a gate of its own, and a writer | **As recommended (2026-11-23).** The lookup rule, no new data, so no new layer member, gate or writer |
| D3304 | What does a release-A runner do with a task that leaves the recipe to the repository's folder (`Source: repo` by name, or no recipe on a ref that holds `.fugaro/recipes/<name>.yaml`)? (F9, F18) | **Refuse at bootstrap with the fix** ("the recipe is the project's now (D3237): launch with fugaro 0.X or later"): acceptance 7 says a repository recipe reaches a run only from the store, and a pre-L1 CLI is the accepted gap, which fails closed this way | Read the folder as today until release B, record `source: repo`; cost: any pre-L8 launcher, including the unfenced pre-L1 ones, keeps bringing repository recipes for a whole release | **As recommended (2026-11-23).** Refuse at bootstrap with the fix, so a repository recipe reaches a run only from the store (acceptance 7) |

### The owner's answers to review round 2 (2026-11-23)

| # | Question | Recommendation | Alternative and its cost | Answer |
| --- | --- | --- | --- | --- |
| D3305 | Does `update` migrate a repository every time it runs, or once? | Once per repository, recorded: a per-repository record in the runs bucket, `fugaro/lift/l8/<slug>.json` (the release and the time), written by `update` after that repository's writes land; a planner acts only on a repository with no record, never overwrites a project value written after it, and names (never applies) a file value that appears after it; the planners retire in release B (XB5) | Every run re-migrates; cost: a project value an admin changes later is overwritten by a repository's file at the next `update` | **As recommended (2026-11-23).** RG2 loses "the file wins over an existing entry", PR4 publishes only on a first migration, M2 writes the record |
| D3306 | Which repositories does `update` migrate, and what happens to one it cannot read? | Fail closed: refuse a launch of a repository that writes a lifted key and was never migrated | **The real list, fail open** | **Not the recommendation (2026-11-23).** `update` migrates the repositories it can list (the local config's, and the concrete names in the published admits); a repository admitted only by a pattern, or unreadable at update time, is not migrated and is refused nothing: its lifted keys are dropped and only the project's values stand. **Consequence, stated plainly:** such a repository can lose an `ab.rate: 0` opt-out (its runs become eligible for A/B sampling at the project's rate), a raised rigor level and its own models; and a recipe name it uses resolves to whatever a listed repository published flat under that name, or else to the catalog recipe of that name, which is the case D3301 exists to prevent (O6). `update` prints each admit pattern it could not expand and each repository it could not read; release A's `### For operators` says so in one line |
| D3307 | O5: the pre-L8 task that embeds no recipe, its folder gone, a catalog name | Accept it, and name it in the release notes | Refuse every unembedded task; cost: every pre-L8 launcher refused outright | **As recommended (2026-11-23).** O5 is closed |

### The owner's answer on O6 (2026-11-23)

| # | Question | Recommendation, and why | Alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| D3308 | O6: under D3306, a repository `update` cannot list resolves a recipe name to another repository's flat copy or to the catalog's. Keep, or close? | **Refuse only that case at launch.** Fail-open stays for every other lifted key. The release-A launcher refuses a launch of repository R for recipe `<name>` when R has no migrated record (`fugaro/lift/l8/<slug>.json`, D3305) and R's default branch holds `.fugaro/recipes/<name>.yaml`. The refusal names the fix: list R in the project, then run `fugaro update`. That is exactly the case where a run would silently change recipe, and the default-branch read is the one S5's gate (R16) already makes | Accept it and name it in the notes (runs change silently); publish every copy renamed whenever a pattern is unexpanded (the unlisted repository still falls to the catalog); fail closed for every lifted key (reverses D3306) | **As recommended (2026-11-23).** Closes O6. A repository with no record and no folder file for that name launches as before, and so does a migrated one. Before the first release-A `update`, every repository that still holds its folder is refused with the same fix, which is the one documented upgrade step (HARD RULE). Lands in PR1; C2 exports the record's path |

- **R21 (F25, review round 2).** `config.AgentOf` drops the lifted keys too (the agent block's
  model keys), in S1, and I1 covers its four readers.

### Resolutions (one defensible answer)

- **R22 (review round 3).** A repository's own `default` recipe becomes `default` (flat) or its copy
  `default.<owner-repo>-<8 hex>`. Where a check compares a name with `recipe.DefaultName`: the
  follow-up's carry (`internal/cli/followup.go`) treats the repository's copy of `default` as its
  default (PR2); `validate`'s read of `.fugaro/recipes/default.yaml` (`validate.go`) goes with the
  folder (XB6); `runs ls` (`runs.go`) shows a copy's own name, which is intended, since a copy is a
  recipe of its own in the record. (`budget_session.go` has no such check.) The orchestrator's
  recipe-name checks (`internal/orchestrator/validate.go`) keep refusing a copy name: a copy is the
  launcher's lookup result, never a name a person or the orchestrator writes into a work item, which
  is intended. `repoHasRecipe` (`budget_preview.go`) stops reading the folder with PR1.

- **R1 (F1, F2). Amended after review.** A lifted key is **not** marked `Retired`: it is on its own
  list in `internal/config/lifted.go` (`LiftedKeys`: the path, its home and its fix), which
  `config.Resolve` reads to drop it and `LiftedKeyNotes` reads to name it. Marking the rows
  `Retired` would break `TestEveryRetiredKeyIsNamedWhereItIsWritten` (`internal/config/retired_test.go`,
  which holds `RetiredKeys` to MCR's R3 list exactly) and, in a build between the scope edit and
  release A, would tell a person a key had moved before `update` can move it. MCR's retired rows
  stay MCR's and stay honoured; a test holds the two lists apart. Release B takes `InRepo` off the
  lifted rows (XB1), so "`Retired` is a subset of `In`" (`ScopeRow.Retired`) never comes into play.
  `validate`, `doctor` and the launch print the notes (S2, S5).
- **R2 (F3).** The drop lives in `config.Resolve`, both arms (with and without a layer), so every
  ref's file — the default branch, a `--ref` branch, a follow-up's base — loses the lifted keys in
  release A. Release B refuses them only on the default branch; every other ref keeps the drop and
  the note (spec §4). The same holds for L13's `model_grades.*.model` (DK1). The resolution's
  repository-own fields read after the drop: `Config.RepoRigor` and `RepoRigorSet`,
  `RepoRigorLevelSet`, `RepoRigorScalarSet` (set in both arms of `Resolve`) see no lifted rigor key,
  while an empty `rigor:` block, which is no lifted key, keeps opting in until release B retires it
  (XB3).
- **R3 (F4).** `config.Resolve` applies B4's rule against the project's value where the project
  sets one; where it is silent, it keeps the repository's value marked as unchecked in the
  resolution, and the places that apply the mode's defaults (`internal/runner/plan.go` `setRounds`,
  `internal/cli/launch_budget.go`, `budget_preview.go`) take the tighter of that value and the
  mode's. The record says which decided.
- **R4 (F11).** A moved area keeps its name unless the project layer already has an area of that
  name (any area); then `-<owner-repo>` is appended and the result cut to 40 characters with
  `areaNameRE`; a cut that still collides takes a short hash. Each rename is one of `update`'s lines.
- **R5 (F12), amended after review round 2.** D3294's new order moves levels both ways: a moved
  area and an existing member area at one rank can raise a path, and a member area that now
  outranks `rigor.repos` can lower one (`rigor.repos` critical, a member area poc). Before any write,
  `update` names every repository and path whose level changes, in either direction, with the old
  and the new level.
- **R6 (F13).** `fugaro rigor set <mode> --repo <owner/name>` with no `--layer` writes
  `defaults.rigor.repos.<owner/name>`, and `--area <glob> --name <n> --repo <owner/name>` writes a
  member area; `--layer repo` is refused for both keys from release A (R15). `writers_labels.go`
  names the writer.
- **R7 (F8).** The release-A launcher embeds the project copy on every path: an explicit
  `--recipe`, `agent.recipe` read from the ref's file when the launch has no checkout, a follow-up's
  inherited repository recipe (mapped to the copy through D3303's rule), and the `default` fallback.
- **R8 (F9, F18).** On D3304's recommendation: a release-A runner never reads `.fugaro/recipes/`; a
  task naming a repository recipe is refused at bootstrap with the fix. A pre-L8 launcher's
  `ab.rate: 0` is the safe direction and needs nothing.
- **R9 (F15).** The only new layer member is `ab.repos`, written only for a repository that wrote
  `ab.rate`. A pre-L1 CLI on such a project fails to read the layer (the gap L1 Q2 accepted); the
  release notes' `### For operators` says "every member upgrades the CLI" in one line. No
  `projectpolicy.Version` bump.
- **R10 (F6).** Task A1 writes the dated amendment of model-credentials-routing's F7 naming D3237,
  in that folder's reconciliation and README.
- **R11 (F16), amended after review.** `dead-keys` refuses `model_grades.*.model` in a repository's
  file on the default branch, and drops and names it on an older ref (R2), from release A (no
  migration: the merge drops it today); the `model_grades.*.access` rows are MCR's (`Retired: InRepo`
  in `scope.go`) and stay out of this plan; removes `policy.tightenGrades`' repository half,
  `Config.RepoModelGradesSet` and the skew gates that read it; `agent.compression`'s row loses
  `InRepo` and the schema says so. MCR X2 refuses `model_grades.*.access` in the same subtree:
  one refusal sentence for both.
- **R12 (F17), amended by D3306.** The reader uses `ContentsReader` and `TreeReader` over the
  repositories it can list (A2); a host or repository it cannot read is named in one line and
  skipped, each admit pattern it cannot expand is named, and `update` still converges for the rest.
  Such a repository is refused nothing except the recipe case D3308 closes: its lifted keys are
  dropped by every reader and never moved, and a launch whose recipe name its default branch still
  holds in `.fugaro/recipes/` is refused with the fix (D3308, O6).
  A symbolic link in `.fugaro/recipes/` is judged by the recipe's own parse and size limit.
- **R13 (F24).** The reader is a contract (C2) shaped so L5 can call the same function.
- **R14 (F10).** A renamed copy is the repository's file with its `name:` line rewritten and
  nothing else; `update`'s line says so, and the run record keeps `source: project`.
- **R15 (F23).** The writers refuse a lifted key from release A: `settings set` and `models use`
  with `--layer repo`, `rigor set --layer repo`; `models use` defaults to `--layer project`.
- **R16 (F19).** The release-A launch gate refuses a pre-L8 image also where the default branch
  holds `.fugaro/recipes/`, with `fugaro update` as the fix.
- **R17 (review).** MC1 never moves a repository onto a model its own `budget.allowed_models`
  (B7, kept) excludes without saying so: such a repository is named before the write with the
  fix (its allow-list, or the project's value), and it is not counted in the majority (D3290)
  for a model it may not use. The planner writes no per-repository model, since B3 is lifted.
- **R18 (review).** The release fence. Release A's gated tasks are not launched before the release
  carrying L1 has been **tagged** (`docs/release.md`: a tag is cut from `main`'s head), and release
  A lands **as one batch**: from the first merge of a gated task until **every** gated task has
  merged, no CLI tag (`vX.Y.Z`) is cut, and a tag is put only on a commit holding all of them or
  none (the plan's tag-fence check). Release B is fenced the same way, over XB1–XB6. A batch rather than a switch, because a switch is code the spec does not ask for and
  a tag freeze costs nothing; console and mobile tags are not affected.
- **R19 (review).** Every recipe-name check that refuses a dot accepts a copy name: `recipe.Parse`'s
  name check, `schemas/recipe.schema.json`, the task's recipe validation (`internal/task`), the
  recipe cache (`internal/localcfg/recipecache.go`), `recipes ls`, `publish` and `rm`
  (`internal/cli/recipes.go`, so an admin can replace or remove a migrated copy),
  `resolveRecipe`, `budget_preview.go`'s recipe read and the follow-up's inherited name
  (`internal/cli/followup.go`, which today drops a name `NameRE` refuses without a word) (PR6, PR1, PR2).
- **R20 (review).** B4's baseline for a silent project comes from where the defaults are applied:
  the mode's `rigor.DefaultLevels` inside `config.ResolveLevels` for the levels (`none`, effort,
  model of a level), the mode's rounds (R3) for the round counts, and Fugaro's `applyDefaults` for a
  key no mode row holds. A repository's `l3.none: true` under a silent project leaves a critical
  run's L3 running (RT2). Against `applyDefaults`, a silent project's `agent.max_budget_usd` baseline
  is `config.DefaultMaxBudgetUSD` (25): a repository that wrote more has its runs stop at 25, so
  `update` names each such repository (RT3) and release A's `### For operators` says so.

## 6. Open tensions and open items

- **T1 (settled 2026-11-23 by D3300).** Spec §4 ("L1 must have shipped") against L1 Q2's answer (the recommendation of "one
  release after" was not taken). → D3300: release A is cut at least one release after L1's.
- **O1.** L5's plan (not written yet) needs the same enrolled-repository reader. Contract C2 is
  shaped for it; whichever plan lands second calls the first one's reader instead of writing one.
- **O2.** Model-credentials-routing's open tasks MG5, X2 and MG6 write `scope.go`, `config.go`,
  `layer.go`, `validate.go`, both schemas and `models_use.go`. An L8 task that finds one of them
  merged first rebases on it; none waits for it.
- **O3.** L1's plan (`docs/required-version-stamp/`, D3260–D3289) is not on `main`. L8 needs
  nothing from its code; the dependency is the release order (D3300, R18).
- **O4 (closed 2026-11-23).** D3302's amended form, `<name>.<owner-repo>-<8 hex>` (at most 96
  characters), is the owner's own answer; nothing is left to confirm.
- **O6 (closed 2026-11-23 by D3308).** D3306 against D3301: a repository `update` cannot list keeps no
  recipe of its own, and a name it uses resolves to another repository's flat copy or to the
  catalog's — the run-changing case D3301 was answered to prevent. The owner kept fail-open and
  closed this one case: the launcher refuses it (D3308).
- **O5 (closed 2026-11-23 by D3307, accepted).** A residue of D3304: a task from a pre-L8 launcher that embeds no recipe, on a ref whose
  `.fugaro/recipes/` the repository has already removed, for a name the catalog also has (`default`
  first), runs the catalog recipe, because nothing left at the ref says a copy exists (`internal/runner/recipe.go`
  `resolveRecipe`). PR3 refuses every case it can see (the file still at the ref). Closing the rest
  means every release-A launcher embeds every recipe and the runner refuses any task that embeds
  none, which refuses every pre-L8 launcher outright. The owner accepted the residue; release A's
  notes name it.

## 7. UNVERIFIED

- **U1.** Whether a retried execution of a run launched before `update` boots on the rebuilt image.
  If it does, D3304's refusal could meet an in-flight run whose task names a repository recipe.
  It is the platform's behaviour, not this repository's code, so Check 913 settles it (task CK3,
  owner-run, read-only: each running execution's image against its job's after an update).
- **U2.** Spec §5.6's "enforce needs the global caps (`budget.go`)" was not found as a rule in
  `internal/cli/budget.go`; the B18 migration does not depend on it. Settled by a reader of
  `internal/budget/evaluate.go`.
- **U3.** Whether the GitHub and Bitbucket tree reads skip or return a symbolic link in
  `.fugaro/recipes/` (`gitprov.TreeFile` has no mode). Settled in task M1's tests against each
  adapter's fixture.
- **U4.** The names L1's plan gives the stamp and its refusal, which L8's release notes cite. Settled
  when `docs/required-version-stamp/` merges.
