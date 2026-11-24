# Fugaro Repo-Override Rulings Spec

Owner: Dimi Paun

Status: approved by the owner, 2026-11-23

Lift L8 of the repo-coupling audit (`docs/repo-coupling-audit/report.md` §7). It applies the owner's
rulings D3237 on the benign candidates B1–B19 (report §3). Code claims were checked against
`origin/main` at `0dec0bb36`. Paths and symbols, never lines.

## 1. Purpose

A repository's `fugaro.yaml`, its `.fugaro/recipes/` folder and its RTDB cap node can still set
things the project should own. Some of them can loosen the project (reconciliation F8). D3237 says
which stay, which become tighten-only and which move to the project. This spec says how each moves,
how `fugaro update` migrates the values already written, when the repository key starts being
refused, and what every reader of a repository's file does with it.

## 2. Principles

- **Migrated once, then refused** (audit spec, "Guiding principles"). The pattern is
  model-credentials-routing's (MCR R14, F40; plan MG1–MG5, X2, X5): `update` migrates in place and
  asks nothing; for one release the old key is read, not honoured, and named; the next release
  refuses it at `validate`, `update` and a default-branch launch, with the fix. An older ref reads
  it as ignored. No `fugaro.yaml` version bump (D3072, kept).
- **The migration keeps every run where it was.** A value moves to the project home that gives the
  same answer for that repository. Where that is impossible (B3, two repositories that disagree),
  `update` says first, one line per repository, what changes.
- **The runner decides, not the launcher.** A launcher on an old CLI cannot bring a repository value
  back, because the runner on the rebuilt image resolves the default branch's file itself
  (`internal/runner/policy.go` `defaultBranchFile`, `resolvePolicy`).
- **One choke point.** Every reader in report §5 reads the file through `config.Resolve` or
  `config.Parse` (`internal/config/resolve.go`, `config.go`). The ignore-then-refuse rule lives
  there, driven by the scope table (`internal/config/scope.go` `Scopes`, `ScopeRow.Retired`), so no
  reader can honour a lifted key by accident.
- **Tighten-only drops, never refuses.** A repository value that does not tighten is dropped and
  recorded, as `policy.Merge` does (`internal/policy/merge.go`, `Effective.Ignored`). Refusing it
  would break a repository's file every time the project raises its own value.
- **Nothing asks.** No step prompts (docs/no-prompt-policy). A file change in a repository is
  offered as a diff, never made by `update`.

## 3. Rulings at a glance

| Item | Key or store | Ruling | Home after L8 |
| --- | --- | --- | --- |
| B1 | `git.base_branch` | keep | repository |
| B2 | `git.author`, `git.pr.reviewers`, `labels`, `early_draft`, `checkpoints` | keep | repository |
| B3 | `agent.model`, `agent.models.*`, `agent.crafts.*.model` | **lift, then refuse** | project layer `defaults.agent.*` |
| B4 | `agent.levels.*`, `review_rounds`, `max_budget_usd`, `first_line_*`, `adversarial_rounds`, `review_stop_fraction`, `workspace_beat`, `recipe` | **keep, tighten-only** | repository, merged tighten-only |
| B5 | `agent.max_output_tokens.*`, `max_run_tokens` | keep | repository |
| B6 | `agent.triage`, `agent.review_against_task` | keep | repository |
| B7 | `budget.*` | keep | repository |
| B8 | `governance.*` | keep | repository |
| B9 | `ab.rate` | **lift, then refuse** | project layer `ab.repos` (new) |
| B10 | `followup.trusted`, `followup.allow_public` | keep | repository |
| B11 | `rigor.default`, `rigor.areas` | **lift, then refuse** | project layer `defaults.rigor.repos` and member areas |
| B12 | `planning.decoupling` | keep | repository |
| B13 | `security.patterns` | keep | repository |
| B14 | `workflows.*.job_profile`, `needs`, `ab.reviewer_arm` | keep | repository |
| B15 | `workflows.*.commands`, `cache`, `inputs`, `checkout` | keep | repository |
| B16 | `.fugaro/recipes/<name>.yaml` | **lift, then refuse** | project recipes in the runs bucket |
| B17 | `agent.instructions`, `agent.review` files | keep | repository |
| B18 | RTDB `config/caps/repos/<slug>` | **keep, capped** | RTDB, ceiling below |
| B19 | RTDB `config/kill/repos/<slug>` | keep | RTDB |

The kept items change nothing. L8 lists them so the scope table and `docs/project-layer.md` can say
"approved by D3237" beside each.

## 4. Release shape

- **Before L8: L1.** The required-version stamp must have shipped. It fences CLIs from L1's release
  on. A CLI older than L1 checks no stamp; it stays harmless because the runner decides (§2) and
  the cap ceiling is applied where caps are read (§5.6).
- **Release A (L8 part 1).** `update` migrates. The lifted keys are marked `Retired: InRepo` in
  `Scopes` with a `RetiredWhy` naming the new home, so `config.RetiredKeyNotes` names them in
  `validate`, `doctor` and the launch. `config.Resolve` drops them before the merge: read, not
  honoured. B4's and B18's tighten rules take effect.
- **Release B (L8 part 2).** A lifted key in a repository's file, or a `.fugaro/recipes/*.yaml` on
  the default branch, is refused at `validate`, `update` and a default-branch launch, with the fix.
  An older ref (a follow-up's branch) reads them as ignored. Release B's launch is gated on the
  job image (`checkImageSince`, `internal/cli/recipes_skew.go`), as MCR X2 is.

## 5. Per item

### 5.1 B3: model choice (`agent.model`, `agent.models.*`, `agent.crafts.*.model`)

**MCR owns the credential half, L8 the model half.** MCR MG1–MG6 and X2–X5 own `agent.auth`,
`agent.crafts.*.access`, `agent.oauth_token`, `model_grades.*.access` and the per-stage accounts:
already `Retired` rows in `Scopes`. MCR left the model choice in the repository on purpose (MCR
reconciliation F7: "the repository keeps its model choice"). D3237 reverses that for the
repository. MCR's F7 gets a dated amendment naming D3237. MCR's routing table still decides which
source serves a model; the project layer decides which model a role uses.

- **Keys.** `agent.model`; `agent.models.coder|reviewer|background|vision|diagnose`;
  `agent.crafts.<craft>.model` for the five crafts. Each `Scopes` row today is
  `InProject | InRepo` (`agent.model` also `InOverride`; the `--model` flag stays).
- **Readers today.** All read the resolved config: `config.Resolve` (`overlay` lays the repository
  over `defaults:`), `config/pins.go` `pins`, `levelPins`, `craftModels`, `internal/craft`
  `ModelRungs`, `internal/runner/poolchoice.go` (a `craft.Layer` named `craft.SourceRepo` from
  `cfg.Agent.Crafts`), `internal/runner/routing.go`, `gateway.go`, `internal/cli/validate.go`,
  `models_use.go`, `craft.go` `checkoutFugaroYAML`, and ee/ab `launchAgent`, whose comment records
  that a launch inside and outside a checkout see different coders today. Lifting B3 ends that
  split.
- **Home.** The project layer's `defaults.agent.model`, `defaults.agent.models.*`,
  `defaults.agent.crafts.*.model` (existing keys, published by `config_publish.go` `publishLayer`).
- **Migration.** `update` reads every enrolled repository's default-branch `fugaro.yaml` through the
  provider (`gitprov.Provider.Contents`, as `internal/catalog/fetch.go` does), key by key:
  - The project already sets the key: the project's value stays. Each repository that wrote another
    value is named: "acme/app: coder claude-x -> claude-y (the project's)".
  - The project is silent and every repository that writes the key agrees: that value becomes the
    project's. Repositories that wrote nothing move from Fugaro's default to it, and are named.
  - The project is silent and repositories disagree: see Q1. Each repository whose model changes is
    named.
  - `budget.allowed_models` (B7, kept) still narrows per repository, so a repository that must not
    use a model can still say so.
- **Refusal (release B).** "agent.models.coder is the project's choice (D3237): remove it from
  fugaro.yaml; the project sets it with `fugaro models use --layer project`." `models use` defaults
  to `--layer project`, and `--layer repo` is refused for these keys.

### 5.2 B4: review depth and spend, tighten-only

Today the repository's value replaces the project's either way (`config.Resolve` `overlay`; only
the levels merge per key, `mergeLevel`). Release A adds a tighten rule in `overlay` for these
paths. A value that does not tighten is dropped, its source stays the project's, and
`Resolution.Sources` plus a recorded "ignored" line (the `policy.Ignored` shape) say why. The
baseline is the project's value, else the run's rigor mode default for that key, so a silent
project does not let a repository lower the mode's review.

| Key | Tighter means |
| --- | --- |
| `agent.review_rounds` | more rounds |
| `agent.max_budget_usd` | a smaller positive figure |
| `agent.first_line_review` | `on` > `auto` > `off` |
| `agent.first_line_rounds` | more rounds, up to `MaxFirstLineRounds` (3) |
| `agent.adversarial_rounds` | more rounds, up to 10 |
| `agent.review_stop_fraction` | a larger share (the loop reviews longer before finalize), up to 1 |
| `agent.levels.lN.none` | a level on beats off: a repository may turn a level on, never off |
| `agent.levels.lN.effort` | later in `config.Efforts` (low < medium < high < xhigh < max) |
| `agent.levels.lN.focus` | additive: the project's text and the repository's are both quoted |
| `agent.levels.lN.model` | a model, so B3's rule (Q2) |
| `agent.recipe` | any project or catalog recipe; the resolved levels hold whatever it is (Q3) |
| `agent.workspace_beat` | not review depth; the replace rule stays (Q4) |

The owner's floor still applies last (`config.ResolveLevels`, `policy.Effective.ReviewFloor`), and
a level that is on but missing from the recipe is inserted (`config.ResolvedLevel.Inserted`). So a
recipe cannot remove a level the merge turned on. No migration: values stay in the repository.
Release A's `validate` and launch print each dropped value once.

### 5.3 B9: `ab.rate`

- **Key and readers.** `ab.rate` (`Scopes` row `InRepo`; `config.AB`, `Config.AB`). A repository may
  only write 0 (`validateRepoAB`). Read by ee/ab `abRate`, which merges the layer's rate with the
  repository's through `policy.Merge` (`Layer.ABRate`), and by `abBlock`.
- **Home.** A new map in the layer's top-level `ab:` block, `ab.repos.<owner/name>: <rate>`, keyed
  like `rigor.repos`, each value in `[0, ab.rate]`. `abRate` feeds `repo.ABRate` from the map.
  `policy.Merge`'s rule does not change.
- **Migration.** Every repository whose default branch writes `ab.rate: 0` gets
  `ab.repos.<repo>: 0`. Repositories cannot disagree: 0 is the only value a repository may write.
- **Skew.** A runner or CLI before L8 decodes the layer with `KnownFields` and would refuse the new
  member. The launch gets an `abReposSince` gate, as the `*Since` constants in
  `internal/cli/recipes_skew.go` do, judged only when the layer writes `ab.repos`.
- **Refusal (release B).** "ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml;
  opt this repository out with `ab.repos.<owner/name>: 0` in the project layer."

### 5.4 B11: `rigor.default`, `rigor.areas`

- **Readers today.** `rigor.Resolve` (`internal/rigor/resolve.go`), through `Config.RigorConfig`
  (`config.go`). It reads the repository's level and areas after the project's, so a repository can
  lower the project (F8). Callers: `internal/runner/rigor.go`, `outcome.go`, `triage_stage.go`
  (`Configured`), `internal/cli/launch_budget.go`, `run.go`, `validate.go`, `budget_preview.go`,
  `recipes_skew.go` (`launchLevelsConfig`, `refuseRigorSkew`), ee/ab `launch.go`. Writers: `fugaro
  rigor set --layer repo` (`internal/cli/rigor.go`) and `settings set` (`settingsRepoFile`).
- **Home.** `rigor.default` goes to `defaults.rigor.repos.<owner/name>` (`rigor.Config.ProjectRepos`,
  an existing key). Each `rigor.areas` rule becomes a project area with one member,
  `{repo: <owner/name>, paths: [<its glob>]}` (`rigor.Member`, existing shape). Its name is kept,
  with the repository's name appended where two repositories used the same one (`areaNameRE`: 40
  characters).
- **Disagreement.** None between repositories: each gets its own entry. Where the project already
  has `rigor.repos.<repo>` and the file says otherwise, the file's value wins today, so it is
  written, and named.
- **Precedence gap.** `rigor.Resolve` ranks `ProjectRepos` above project areas. A repository area
  that today lowers or raises a path below the repository's own default would, once moved, lose to
  `rigor.repos`. See Q5; the recommendation changes the order so a member area naming this
  repository ranks above `rigor.repos`.
- **Opt-in gap.** `rigor.Config.Configured` turns backlog filing on for every repository once
  `ProjectRepos` is non-empty, and a repository's empty `rigor:` block (`RepoBlock`) is itself an
  opt-in. L8 makes `Configured` per repository (project default or project-wide areas, else this
  repository's `rigor.repos` entry or a member area naming it). An empty block migrates as
  `rigor.repos.<repo>` set to the level it resolves to today.
- **Refusal (release B).** "rigor.default is the project's (D3237): this repository's level is
  `rigor.repos.acme/app` in the project layer; set it with `fugaro rigor set <mode> --repo
  acme/app`." `rigor set --layer repo` is refused.

### 5.5 B16: `.fugaro/recipes/`

- **Readers today.** Launcher: `internal/cli/recipes_resolve.go` `resolveRecipe` (checkout first,
  then the project, then the catalog), `refuseFollowUpRepoRecipe`, `run.go` `repoRecipeAtRef`,
  `repoRecipeFrom`, `recipes_skew.go` `repoRecipeForGate`, `recipes.go` `localRoot` (`recipes ls`),
  `validate_recipes.go`. Runner: `internal/runner/recipe.go` `resolveRecipe` and `readRepoRecipe`,
  which read the file at the task's ref, so a branch can bring its own recipe.
- **Home.** Project recipes in the runs bucket (`recipe.ObjectKey`: `fugaro/recipes/<name>.yaml`),
  written by `fugaro recipes publish` behind `settingsAccessGate`. A migrated file goes to a
  per-repository prefix of the same store, `fugaro/recipes/repos/<slug>/<name>.yaml`, which the
  launcher reads before the project's flat names. The run record keeps `source: project`, so no
  reader of the record meets a new word.
- **Why a per-repository prefix.** Two repositories can hold different `review.yaml` files, and each
  repository's `agent.recipe` and task names stay valid without a rename. An admin can later
  publish one flat recipe and remove the per-repository copies. Q6 offers the flat alternative.
- **Migration.** `update` lists `.fugaro/recipes/*.yaml` on each enrolled repository's default
  branch and publishes each regular file that parses (`recipe.Parse`, 16 KiB limit). A file that
  does not parse is left and named; its runs fail today too. Files only on other branches are not
  migrated.
- **The files in the repository.** `update` cannot commit to a repository. Release A: the launcher
  and runner stop reading the folder once the project holds the copy; `doctor`, `validate` and the
  launch print, per repository, the diff that deletes the folder (`git rm -r .fugaro/recipes`), the
  same offer the plugin pin removal makes, moved to `doctor` as L5 moves it. Release B: a
  default-branch launch with the folder present is refused with that diff. Q7 asks whether `update`
  should also open one cleanup pull request per repository.

### 5.6 B18: RTDB repository caps

- **Today.** `budget.Caps.RepoDaily` and `RepoPerRun` (`internal/budget/model.go`) take the
  repository node, else `config/caps/defaults`. `Caps.PerRun` is the smaller of that and the global
  per-run cap. The daily repository cap is checked beside the global daily cap
  (`internal/budget/evaluate.go`, `precheck.go`, `lease.go`). The database rules read the node the
  same way (`internal/budget/rules/testdata/rules.golden.json`). Writer: `fugaro budget set --repo`
  (`internal/cli/budget.go`, `budget.PathCapsRepo`).
- **Ceiling.** For each of daily and per-run: the global cap when it is set, else the default
  repository cap. A repository node above its ceiling counts as the ceiling. No ceiling (neither
  set): the node stands.
- **Where it holds.** `budget set --repo` refuses a figure above the ceiling, naming it and the
  command that raises the global cap. `Caps.RepoDaily` and `RepoPerRun` clamp. The rules' lease
  check clamps the same way (one rules version, published by `update`). An admin's direct database
  write bypasses rules, so the clamp at read is what holds.
- **Migration.** `update` lowers each node above its ceiling in place and names it. With a global
  cap set this changes nothing a run meets: the global already binds. It matters only for a project
  with defaults and no global cap, which means `observe` mode (enforce needs the global caps,
  `budget.go`).

## 6. Every reader of a repository's file (report §5)

| Reader | What L8 does |
| --- | --- |
| `cloud.go` `checkoutParse`, `run_layer.go` `embedProjectLayer`, `craft.go` `checkoutFugaroYAML`, `config_show.go` `configLayerAnchor`, `project_job.go` `checkoutFugaroYAMLAtRef` | through `config.Resolve`: lifted keys dropped and named (A), refused (B) |
| `run.go` `cascadeRefConfig`, `defaultBranchPolicyLayer`; `recipes_skew.go` `launchLevelsConfig` | same; B4's tighten rule applies |
| `init_credentials.go` `deploymentConfig`; `init_images.go` `imagesStage.kinds`; `init_policy_migrate.go` `checkoutWorkflows`; `initsecrets.go` `secretsStage.resolve` | same; they read no lifted key, and L5 moves them off the working directory |
| `settings_set.go` `settingsRepoFile`; `rigor.go` `--layer repo`; `models_use.go` `--layer repo` | refuse a lifted key with the project-layer command |
| `config.EffectiveBaseBranch` | B1, kept: no change |
| `internal/runner/policy.go`, `rigor.go`, `followup.go` `readBaseConfig` | default branch: dropped (A), refused (B); run branch and an older ref: ignored and named |
| `security.go` `securityBaseFile` | B13, kept: no change |
| `cloud.go` `refuseSharedCheckout` | no lifted key: no change |
| `update_budget_off.go` `offerBudgetModeRemoval` | B7, kept; L5 owns it |
| recipe readers (§5.5) | stop reading `.fugaro/recipes/` (A); refuse the folder (B) |

A test runs each reader on a file that writes every lifted key and fails if any value reaches the
resolved config.

## 7. Members on an old CLI, and runs in flight

- **A CLI at L1's release or later, below the stamp,** refuses project commands with `brew upgrade`
  and `fugaro update` (L1). It cannot write a lifted key through `settings set`, `models use`,
  `rigor set` or `budget set --repo`.
- **A CLI older than L1** is not fenced. It can still edit a file or write a cap node. A file edit
  is dropped by the runner (A) or refused at launch (B). A cap node above the ceiling is clamped
  where caps are read. Its launches run on the job image, whose runner resolves the default branch
  itself.
- **A run in flight** keeps its task, its embedded layer and its embedded recipe (`update` changes
  nothing it holds, `docs/design/update-in-flight.md`). A run that reads a repository recipe at its
  ref still finds the file, since nothing is deleted. The one thing it can meet is a lowered
  repository cap at its next lease; `update`'s in-flight line names it, as it names the budget
  mode's raise.
- **An older job image** honours repository keys. Release A's launch refuses an image before L8 when
  the default branch writes a lifted key, with `fugaro update` as the fix (an `l8Since` gate).

## 8. Docs, schemas and skills

- `internal/config/scope.go`: the lifted rows marked `Retired: InRepo` (A), then `InRepo` removed
  (B); every kept row's `Why` cites D3237. `docs/project-layer.md` prints the table
  (`TestScopeTableMatchesDocs`).
- `schemas/fugaro.schema.json`: lifted keys described as retired (A), removed (B).
  `schemas/project-layer.schema.json`: `ab.repos`. `internal/settings/registry.go` and
  `schemas/settings.schema.json`: the rows' coverage test.
- `docs/recipes.md` and `docs/recipes/spec.md` §5: the new resolution order. `docs/layered-config/
  spec.md`: B4's tighten rule. The rigor docs: the new precedence if Q5 is accepted.
- `plugin/skills/setup/SKILL.md` step 3 and `reference/decisions.md` §3: the model question moves to
  the project (`fugaro models use --layer project`), not the repository's file.
  `plugin/skills/working/SKILL.md`: `agent.models.vision` "on the repository" becomes the project.
  `plugin/skills/parallelism/reference/decoupling.md` and `spec-to-pr/reference/plan.md` read
  `rigor.default` per repository: they read `rigor.repos` instead.
- `website/content/cli-reference.md` regenerated; release notes `### For operators`: "run `fugaro
  update`", plus what it names.

## 9. L13: inside or separate?

**Recommend inside L8, as its own module.** L13 refuses the dead repository keys
(`model_grades.*` in a repository's file, RC20; `agent.compression`, RC22) and removes their merge
plumbing (`policy.tightenGrades`'s repository half, `Config.RepoModelGradesSet`). It edits the same
rows of `Scopes`, the same schema, the same `config.Resolve` and the same refusal wording, and waits
on L1 too. Two specs would mean two refusal styles and two plans editing one table. It needs no
migration, so its tasks can run first. Coordinate with MCR X2, which refuses `model_grades.*.access`
in the same subtree.

## 10. Acceptance criteria

1. `update` on a project whose repositories write each lifted key converges, publishes the project
   values of §5, prints one line per repository that changes before any write, and changes nothing
   on a second run.
2. After release A, a run of each migrated repository resolves the same models (where the
   repositories agreed), the same rigor level and areas for the same paths, the same A/B rate and
   the same recipe text as before. A table-driven test covers each item.
3. Release A: every reader in §6 on a file with every lifted key yields a resolved config with none
   of their values, and `validate`, `doctor` and the launch name each key with its fix.
4. Release B: the same file is refused at `validate`, `update` and a default-branch launch; a
   follow-up on an older ref ignores the keys and names them.
5. B4: for each key of the table in §5.2, a looser repository value is dropped and recorded and a
   tighter one wins; a project that raises its value does not break a repository's file.
6. B18: `budget set --repo` above the ceiling is refused with the fix; a node written above it is
   clamped by `Caps`, by the rules' lease check (emulator test) and lowered by `update`.
7. B16: a repository recipe reaches a run only from the project store after release A; a
   default-branch launch with `.fugaro/recipes/` is refused in release B with the diff.
8. A run in flight across `update` finishes on its own embedded layer and recipe
   (`TestUpdateLeavesARunInFlightRunning` extended).
9. A launch on a job image before L8 is refused where the default branch writes a lifted key, and
   where the layer writes `ab.repos`.
10. No command asks a question; the Docs tests and the plugin tests pass.

## 11. Open questions, with recommendations

- **Q1. B3: repositories disagree and the project is silent.** Recommend: the project takes the
  value most repositories write, ties broken by the cheaper model in the price table; every
  repository that moves is named before the write. Alternative: write nothing and leave Fugaro's
  default.
- **Q2. `agent.levels.lN.model`.** It is in B4's row but is a model. Recommend: B3's rule (lifted,
  then refused); effort, `none` and focus stay tighten-only.
- **Q3. "Tighter" for `agent.recipe`.** Recommend: any project or catalog recipe, guarded by the
  levels and the owner's floor, which hold whatever the recipe. Alternative: a project allow-list of
  recipes a repository may name.
- **Q4. `agent.workspace_beat`.** It is not review depth and the scope table says neither value
  widens. Recommend: keep it with the replace rule, outside the tighten-only list.
- **Q5. Rigor precedence after the move.** Recommend: a project area with a member naming this
  repository ranks above `rigor.repos`, so moved areas keep their effect. This amends LD4's order
  for projects that already use both; the alternative is a new per-repository area key.
- **Q6. B16 store.** Recommend the per-repository prefix. Alternative: one flat namespace, with
  `update` refusing to migrate two different files of one name, which leaves an admin step and
  breaks "update is the whole upgrade".
- **Q7. Repository cleanup.** Recommend the diff in `doctor`, `validate` and the launch (the plugin
  pin's pattern). Alternative: `update` opens one cleanup pull request per repository through the
  provider, so nobody has to type the diff; it needs a write identity on every repository.
- **Q8. `ab.repos` values.** Recommend any rate in `[0, ab.rate]`. Alternative: 0 only, today's
  exact power.
- **Q9. Release B timing.** Recommend the release after A, behind the image-age gate, as MCR X2.
