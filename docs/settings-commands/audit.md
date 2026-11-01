# Fugaro settings audit: what can be set by command, and what still needs a hand edit

**Asked by:** the owner, 2026-10-31: "for project level and repo level settings, in fact for ALL the settings, you should not expect people to edit the text file directly, but have Fugaro commands that manage those settings: reports it, updates it, etc." Two later additions the same day: one command that lists every setting with its name, the level that wins and the value; and that every row also shows the default value.

**Read against:** `origin/main` at `1c9be2a2c`, a detached worktree. Command shapes were checked with the installed `fugaro` 0.14.0 (`--help` only). Open PR #1463 (`docs/rigor-command`) was read with `git show`. Every claim below cites `file:line` on that commit. Read-only: no command wrote anything.

**Numbers refreshed since:** #1460 moved three of the files cited here, so their citations carry
**today's `main`** numbering rather than `1c9be2a2c`'s: `internal/cli/init.go` past its midpoint (+5:
the shared-config publish, the agent-session refusal and the diff/backup/atomic triple),
`internal/cli/init_policy_migrate.go` (+1 above its doc comment, +47 from the ownership gate #1460
inserted in front of it, so the agent-session refusal is `:459`) and `internal/cli/upgrade_cloud.go`
(+3). Every other citation is the one that commit's line numbering gives.

**Rules this audit is measured against**
- The HARD RULE in `CLAUDE.md`: `fugaro update` is the whole upgrade. No operator step may be another command or a manual repair.
- A field never changes meaning.
- The pattern already designed for `fugaro models` (`docs/models-commands/spec.md` §1, §4.5, §8, §9) and `fugaro rigor` (#1463):
  - Edit the local file only, through a comment-preserving YAML node-tree editor.
  - Print the diff and confirm. A coding agent's session prints the diff and writes nothing, even with `--yes`.
  - `fugaro update` publishes.
  - Read commands have `--json` and say where each value comes from.

---

## 0. Summary

1. **Every owner-level setting except the IAM lists and the budget caps is a hand edit today.**
   - Hand edits: `budget.*`, `providers`, `routing`, `model_prices`, `claude_accounts`, `project_secrets`, the job `profiles`, `base_branch`, `review_floor`, `bitbucket_api_roots`, `max_parallel` and the rest of the local project config.
   - The whole project layer is also a hand edit.
   - The only commands that write the local project config are the converge doors (`setup`/`update` flags) and `fugaro user add|rm`.
2. **Two different publish paths, and `fugaro update` runs neither.**
   - **The published policy node** is rebuilt from the local config only by `fugaro project policy publish` (`internal/cli/project_cmd.go:95`, `:215`, `:670`). `fugaro update` only *migrates* the node's version (`internal/cli/init_stages.go:178-190`, `internal/cli/init_policy_migrate.go:21-37`).
   - **The project layer** is published only by `fugaro config publish FILE` (`internal/cli/config_publish.go:31`). It has no local source file at all; the only local copy is a cache of the published object (`internal/localcfg/layercache.go`).
   - So after an edit plus `fugaro update`, nothing changes and nothing warns. This is the "published change that silently does nothing" class.
3. **Two node blocks have no writer of any kind.**
   - `credentials.*` (the cascade's fallback, wait threshold, backoff, stop-after-exhaustion and per-token concurrency) and `clone_urls` live only in the Realtime Database node.
   - The publisher only carries them over (`project_cmd.go:662-669`, `:815-832`). The only way to set them is the Firebase console.
   - This is the riskiest gap: it is untyped and unvalidated until the next read, and the field controls whether paid tiers are spent (`fallback: auto`).
4. **No single command lists settings.**
   - `fugaro config show` covers one checkout's resolved `fugaro.yaml` (repo, project layer, default).
   - It does not cover the local config, the policy node, the Realtime Database caps and mode, the shared config, environment variables, other repositories, or the ceiling clamp `policy.Merge` applies.
5. **Defaults are scattered over four places that nothing ties together.**
   - The schemas carry zero JSON-Schema `default` keywords.
   - `applyDefaults` (`internal/config/defaults.go:199`) uses literals (25, 4, `8Gi`, 90m, `"main"`), and some of them duplicate named constants elsewhere.
   - The annotated `example.yaml` restates them in comments.

**Proposed command families**
- One read command, `fugaro settings`, over every layer: winning level, value, default, staleness, and "unpublished" drift.
- One write path, `fugaro settings set|unset KEY`, for scalar and list keys, driven by a key registry and the shared node-tree editor.
- Dedicated families for structured blocks: `models` and `rigor` (both designed), plus `accounts`, `project profile`, `project routing`, `project credentials`, `project clone-url`, `rigor floor`, and `secrets allow`.
- All local writes reach the cloud only through `fugaro update`, which needs three new stages: publish the policy, publish the layer, and re-converge provider drift.

**First slice:** the read-only `fugaro settings` (offline layers first, then cloud layers, with a DEFAULT column from a new defaults registry and an `unpublished` state), together with `fugaro update`'s policy-publish stage (F8 of the models design, already a dependency there).

---

## 1. Inventory: every setting a person can configure, by layer

Abbreviations used in the columns:
- **Show**: the command that prints it today.
- **Set**: the command that changes it today ("hand" = a text edit).
- **Reaches cloud**: how a change takes effect.
- **Who**: owner (project Owner/Editor), operator (`terraform.operators`), launcher, or repo contributor.
- **json**: whether the show command has `--json`.
- **agent**: whether the set command refuses a coding agent's session (`agentMarker`, `internal/cli/initsecrets.go:141`).
- **Gap**: H/M/L, the need for a command.

### 1.1 The repository's `fugaro.yaml`

Sources: `internal/config/config.go:24-1030`; the scope table is `internal/config/scope.go:44-131`.

These keys are PR-reviewed and reach a run through the base branch, the "default branch" layer of `policy.Merge` (`internal/policy/merge.go:31-39`). No key has a set command except `project`/`gcp_project`/`version` (written by `fugaro config init`, `internal/cli/config_init.go:91`, only when no file exists) and `gcp_project` (`fugaro update --anchor`). Reading is always `fugaro config show [--json]` (`internal/cli/config_show.go:56`) plus `fugaro validate [--json]`. Writing the repository file in an agent session is allowed by design, because the file goes through a PR (`config init --help`).

| Setting group (keys) | Cite | Show / json | Set | Reaches cloud | Who | Gap |
|---|---|---|---|---|---|---|
| `version`, `project`, `gcp_project`, `profile` | config.go:25-36 | config show / yes | `config init` (new file), `update --anchor` (gcp_project), else hand | merge to base branch | repo | L |
| `git.provider`, `git.base_branch`, `git.author`, `git.pr.{labels,reviewers,early_draft,checkpoints}` | config.go:340-384 | config show / yes | hand | merge | repo (`provider`, `labels`, `early_draft` and `checkpoints` can also be set in the project layer) | M |
| `agent.{auth,model,review_rounds,max_budget_usd,instructions,review,first_line_review,first_line_rounds,recipe,oauth_token,compression}` | config.go:397-454 | config show / yes | hand; `--model`, `--review-rounds`, `--recipe` per run (InOverride, scope.go:15) | merge | repo, layer | M |
| `agent.models.{coder,reviewer,background}` | config.go:481-485 | config show / yes | hand; **`fugaro models use` designed** (models spec §6) | merge | repo, layer | M (designed) |
| `agent.max_output_tokens.*`, `agent.max_run_tokens` | config.go:412-415 | config show + validate clamp | hand | merge; may only tighten the ceiling (scope.go `whyPolicy`) | repo | L |
| `budget.{mode,per_run_usd,allowed_models,per_day_usd}` | config.go:164-173 | config show; validate prints clamps (`internal/cli/validate.go:105`) | hand | merge; tighten-only (merge.go:39) | repo | L |
| `governance.*` (ratios, stage budget/tokens/timeouts, tiers) | config.go:194-284 | config show | hand | tighten-only under the layer's governance (`validate.go:92`) | repo, layer | M |
| `ab.{rate,max_pairs_per_day,challengers,judge_model,exchange_rate}` | ab.go:29-50 | config show | hand | the repository may only lower the rate (layer.go:77-85) | repo, layer | L |
| `rigor.{default,areas}` | config.go:113-123 | config show | hand; **`fugaro rigor set|unset --repo` designed** (#1463) | merge | repo, layer | M (designed) |
| `followup.{trusted,allow_public}` | config.go:313-320 | config show | hand | merge | repo only (scope.go `whyFollowup`) | M (security-relevant) |
| `workflows.*` (profile, job_profile, image.*, dockerfile, commands.*, cache, secrets, resources, timeouts, rebuild.*, inputs.*, checkout) | config.go:551-995 | config show `--workflow`; image status | hand; the `fugaro:setup` skill authors it | merge plus image rebuild (`image refresh` or the daily check) | repo, layer profile | L (the setup skill covers authoring) |

### 1.2 The project layer: bucket object `fugaro/project-layer.yaml`

Sources: `internal/config/layer.go:29`, `:50-180`. A per-repository copy is written to `builds/<slug>/project-layer.yaml` (layer.go:42).

| Setting group | Cite | Show / json | Set | Reaches cloud | Who | agent | Gap |
|---|---|---|---|---|---|---|---|
| `defaults.git.{provider,pr.labels,pr.early_draft,pr.checkpoints}` | layer.go:139-149 | `config layer` (needs a git checkout: config_show.go:205), `config show` (resolved) / yes | **hand**: `config layer > f`, edit, `config publish f` | `config publish` writes the object and fans it out (config_publish.go:70-77). **Not** `update` | anyone who can write the runs bucket (launchers too: models spec D792) | refused (config_publish.go:36) | **H** |
| `defaults.agent.{auth,model,models.*,review_rounds,max_budget_usd,first_line_*,recipe}` | layer.go:152-161 | same | same | same | same | same | **H** (`models use` deliberately does not write here, D792) |
| `defaults.rigor.{default,areas}` | layer.go:133-136 | same | same; **`fugaro rigor set` designed** with a local layer file (#1463 D830/D831) | same; #1463 adds an update "Layer" stage | same | same | **H** (designed) |
| `profiles.<name>.*` (image, commands, cache, resources, timeouts, rebuild, inputs, checkout), `default_profile` | layer.go:55-56, :164-180 | same | same; executable keys need `--executable-changes` (config_publish.go:85, :129-141) | same | same | same | **H** |
| `claude_models` (top level) | layer.go:58-64 | `config layer` | same; checked against the routes at publish (config_publish.go:67) | same | same | same | M |
| `governance` (top level) | layer.go:66-75 | `config layer` | same | same | same | same | M |
| `ab` (top level) | layer.go:77-85 | `config layer` | same | same | same | same | L |

### 1.3 The local project config: `~/.config/fugaro/projects/<name>.yaml`

Sources: `internal/localcfg/localcfg.go:40-279`. The installation-wide subset is published as the shared config (§1.4). Other keys reach the cloud through Terraform (`update`), through the job environment (`update --repo`), or through the policy node (`project policy publish`).

| Setting group | Cite | Show / json | Set | Reaches cloud | Who | agent (set) | Gap |
|---|---|---|---|---|---|---|---|
| `name`, `gcp_project`, `region`, `runs_bucket`, `registry*`, `scheduler_region`, `image_source`, `base_images`, `log_view`, `credentials_service.url`, `budget.{rtdb_url,firebase_project,firebase_api_key,token_signer}`, `setup_pending` | localcfg.go:41-101, :214, :434-442 | `config list --json` (partly); doctor | written by `setup`/`update` from Terraform outputs or flags (`--region`, `--scheduler-region`, `--runs-bucket`, `--state-bucket`, `--image-source`, `--base-image`) | Terraform via `update` | owner/operator | update applies nothing in an agent session (update `--help`) | L |
| `terraform.{launchers,operators}` | localcfg.go:594-595 | `user ls --json` | **`user add|rm --role`** (user.go:186), `update --launcher/--operator` | IAM via `update` | owner | **refused** (user.go:320) | closed |
| `terraform.{budget_admins,alert_email,registry_cleanup,no_log_isolation,budget_backend,coordinator,state_bucket}` | localcfg.go:592-614 | none dedicated (doctor partly) | `update --budget-admin/--alert-email/--registry-cleanup/--no-log-isolation/--firebase-project/--coordinator/--state-bucket` (init.go flags; e.g. init.go:372). **Turning the coordinator off is a hand edit** (init.go:2445-2446) | Terraform via `update` | owner | yes | M |
| `budget.{mode,per_run_usd,max_run_tokens,allowed_models,unreachable_grace}` | localcfg.go:422-446 | none local (`project policy show` prints the published copy) | `mode`: `update --budget-mode` (init.go:372). **The rest are hand edits** | **two paths**: the job environment through `update --repo` (validate.go:65-77) and the node through `project policy publish` (project_cmd.go:795) | owner | none (hand) | **H** |
| `model_prices` | localcfg.go:107, :453-470 | `budget prices --json` | hand; **`models add --price-*` designed** | node via `policy publish` (project_cmd.go:799) | owner | – | H (designed) |
| `providers.<name>.{kind,base_url,auth,secret,route_fee_pct,require_zdr,price_policy,models,allow_data_to}` | providers.go:41-74; localcfg.go:110 | `budget prices`, doctor `routing-resolution` | hand; **`models add|rm` designed** (extends existing routes only; adding a route stays a hand edit, models reconciliation F12) | the job environment `FUGARO_MODEL_PROVIDERS` per repository through `update --repo` (upgrade_cloud.go:431); the node gets a copy | owner | – | **H** |
| `routing.claude.{via,models}` | localcfg.go:111-132; routing.go:58-79 | `project policy show`, doctor | hand | node only, via `policy publish` (localcfg.go:118-121) | owner | – | **H** |
| `claude_accounts.<name>.allow_data_to` | localcfg.go:136; claude_accounts.go:63-66 | `accounts ls --json` | hand (tokens through `secrets set`/`creds set`) | the node's `accounts` (derived, project_cmd.go:731-743) plus jobs per account via `update --repo` | owner | – | **H** |
| `project_secrets.<reserved>.{allow_data_to,proxy}` | localcfg.go:144; project_secrets.go:23-37 | `secrets ls --json` (allow-lists) | hand. **The `secrets set --help` text tells the operator to copy this entry to every operator machine by hand** | the node's `bitbucket_token` (derived, project_cmd.go:762) plus update | owner | – | **H** |
| `bitbucket_api_roots` | localcfg.go:145-161 | `project policy show` | hand | node via `policy publish`; also patched by update's migration (init_policy_migrate.go:34-36) | owner | – | M |
| `profiles.<name>` (job profiles: provider, cpu, memory, timeout_class, secrets, allow_repos, claude_accounts) | localcfg.go:162-177; config.go:866-893 | `project policy show` | hand | node via `policy publish` plus job deploy via `update` | owner | – | **H** |
| `base_branch` (project) and `repos.<r>.base_branch` | localcfg.go:243-254, :878 | `project policy show` (project value only) | hand | project value: node via `policy publish`; the per-repository value is local only | owner | – | M |
| `review_floor.<mode>: [levels]` | localcfg.go:255-278 | `project policy show` | hand; an absent key keeps the node's floor, so removing a floor needs a written `{}` | node via `policy publish` | owner | – | **H** (a floor, and its semantics are subtle) |
| `repos.<r>.{provider,workflows,github_app_id,vertex}` | localcfg.go:871-886 | `config list --json` | `update --repo`, `--github-app-id` | Terraform per repository | operator | yes | L |
| `max_parallel` (default 20) | localcfg.go:241, :1160-1161 | none | hand | local launch check (`run.go:856`) | each machine | – | M |
| `user`, `commit_name`, `commit_email` | localcfg.go:221-240 | none | hand | the task (git.author: launcher) | personal | – | M |
| `update_check` | localcfg.go:215-220 | none | hand (or `FUGARO_NO_UPDATE_CHECK`) | local | personal | – | L |
| `watch.burn_alert_usd_per_hour` | localcfg.go:395-399 | none | hand | local (`watch.go:271`) | personal | – | L |
| `mailbox.keychain_item` | localcfg.go:287-311 | `pair ls --json` | `pair` / `pair revoke` | published in shared | operator | – | closed |
| `compute_prices`, `build.machine_type`, `backend`, `batch.*`, `endpoints.*` | localcfg.go:80-98, :656-867 | `backends ls --json` | hand, or `setup --provision-batch` | Terraform / launch | owner | – | L (rare; `endpoints` is for fakes) |

### 1.4 The shared config (runs bucket, `infra.SharedConfigObject`)

- **What it is:** a projection of the local config, never authored directly. `Config.Shared()` drops owner-only and personal keys (`internal/localcfg/shared.go:12-80`).
- **How it is published:** automatically by `setup` and `update` after the local config is written (`internal/cli/init.go:2467`, `:2491-2505`), or with `update --publish-config` (init.go:382, :642-650). It is refused in an agent session (init.go:2492).
- **Show:** `config list --json` says "what is known about its published shared config". Nothing prints the published object.
- **Gap:** no write gap. There is a read gap, folded into §4B.

### 1.5 The published policy node: Realtime Database `config/policy/shared` plus `config/policy/repos/<slug>`

Sources: `internal/projectpolicy/paths.go:19-22`; members at `policy.go:130-253`.

| Member | Source today | Show / json | Set | agent | Gap |
|---|---|---|---|---|---|
| `budget`, `base_branch`, `providers`, `model_prices`, `profiles`, `accounts` (derived), `bitbucket_token` (derived), `bitbucket_api_roots`, `review_floor`, `routing` | local config, via `buildProjectPolicy` (project_cmd.go:670-834) | `project policy show --json`, `project show --json` (project_cmd.go:2251, :2264) | `project policy publish` (project_cmd.go:95) | **not refused** (no `agentMarker` in project_cmd.go) | **H**: not published by `update` (models reconciliation F8) |
| `allow_repos` | the node itself | `project repos ls` (**no `--json`**), `project show --json` | `project repos add|rm` (project_enroll.go:92, :109), `project sync` (:757); seeded by `policy publish --seed-repos` | not refused (only `--github-app-key` is, project_enroll.go:778) | closed (json missing) |
| `credentials.{fallback,wait_threshold,backoff.{initial,cap},stop_after_exhaustion_per_window,concurrency_per_token}` | **the node only**: "the node is the only place the block exists" (project_cmd.go:819-832). Defaults at policy.go:416-419 | `project policy show` (project_cmd.go:1693) | **none: Firebase console hand edit** | – | **H, highest risk** |
| `clone_urls.<canonical>` | **the node only**: "no command of this release writes one" (project_cmd.go:662-669, :815-818) | `project policy show` | **none: Firebase console hand edit** | – | M (rare, but a typo silently clones from the wrong host until the node is read) |
| `version` | `update` migrates in place (init_policy_migrate.go) | doctor | `update` | refused in an agent session (init_policy_migrate.go:459) | closed |

### 1.6 Realtime Database budget and runtime nodes

Paths: `internal/budget/model.go:31-52`.

| Setting | Path | Show / json | Set | agent | Gap |
|---|---|---|---|---|---|
| caps mode (observe/enforce) | `config/mode` | `budget show --json` | `budget set --global --mode`; seeded by `update --budget-mode` | not refused, by design (accounts.go:449) | closed. A naming hazard: see SD6 |
| global daily and per-run caps | `config/caps/global` | `budget show --json` | `budget set --global --daily/--per-run` (budget.go:881) | not refused; a raise asks for the project's name | closed |
| default per-repository caps | `config/caps/defaults` | same | `budget set --defaults` | same | closed |
| per-repository caps | `config/caps/repos/<slug>` | same (`--repo`, `--all`) | `budget set --repo` | same | closed |
| max reserve | `config/limits.maxReserveMicros` (model.go:228-230) | same | `budget set --global --max-reserve` | same | closed |
| kill switches | `config/kill/global`, `config/kill/repos/<slug>` (model.go:318-327) | `budget show`, `watch` | `budget kill|resume` | not refused | closed |
| credential marks | `creds/marks/NAME` | `accounts ls --json` | `accounts clear` (accounts.go:409) | not refused, by design | closed |
| paired devices | `config/admin/devices` (sweep.go:85) | `pair ls --json` | `pair`, `pair revoke` (pair.go:1086) | – | closed |

### 1.7 Other owner objects

| Setting | Store | Show | Set | agent | Gap |
|---|---|---|---|---|---|
| managed marker plus monthly budget alert amount | `gs://<runs>/fugaro/managed.json` plus project label | none dedicated | `managed flag` (managed_flag.go:98) | refused (:188) | L (adds only; "edits nothing", per its `--help`) |
| approved deletes | `fugaro/allow-delete.json` | `allow-delete` with no arguments | `allow-delete ADDR=ID...` (allow_delete.go:69) | refused (:198) | closed |
| project recipes | `fugaro/recipes/NAME.yaml` | `recipes ls|show --json` | `recipes publish FILE` (recipes.go:327) | refused (:332) | M (same FILE-publish shape as the layer) |
| secrets | Secret Manager | `secrets ls --json`, `creds ls` | `secrets set` (secrets.go:279), `creds set` (creds.go:128) | `creds` refused (:116); `secrets set` not refused | closed (values only, never shown) |

### 1.8 Environment variables that are operator settings

Every other `FUGARO_*` name in the code is a job-environment variable written by the deploy, not a setting a person sets.

| Variable | Meaning | Cite |
|---|---|---|
| `FUGARO_CONFIG`, `FUGARO_PROJECT` | select the project config | `internal/localcfg/select.go:36`, :91, :208 |
| `FUGARO_NO_UPDATE_CHECK` | silences the update nag (the same as `update_check: false`) | `internal/cli/updatewarn.go:21` |
| `FUGARO_NO_SKILL_WARNING` | silences the skill warning | `internal/cli/skillwarn.go:16` |
| `XDG_CONFIG_HOME`, `XDG_CACHE_HOME` | where configs and caches live | `config list --help`; layercache.go |

No command shows these. A `settings` read should list them as a layer (§4B).

---

## 2. How a change reaches the cloud today, and what `fugaro update` publishes

`fugaro update` runs these stages (`init_stages.go:160-195`): Terraform roots → secrets → plugin → repository → rules → **policy (migration only)** → checkout. After writing the local config it publishes the shared config (init.go:2467).

| Change | What the user must run today | Does `update` do it? |
|---|---|---|
| Launchers or operators | `user add`, then `update` | yes |
| A Terraform-backed local key | `update` (the flag) | yes |
| A key from the policy section of the local config (budget, providers, prices, routing, profiles, accounts, project_secrets, base_branch, review_floor, bitbucket_api_roots) | hand edit → `fugaro project policy publish` | **no**. The node is only migrated (init_policy_migrate.go:21-37) |
| providers, budget ceiling, or accounts in a job's environment | hand edit → `fugaro update --repo` **in every repository** (upgrade_cloud.go:431) | **no**. D787 (answered) assigns it to update |
| Project layer | `config layer > f` (only from inside a checkout) → edit → `config publish f [--executable-changes]` | **no** (#1463 F18; D830/D831 proposed) |
| `credentials.*`, `clone_urls` | Firebase console | **no**, and nothing else does it either |
| Caps, kill switches, marks | `budget set|kill|resume`, `accounts clear` (immediate) | n/a (runtime controls, see SD7) |
| Project recipes | `recipes publish FILE` | no |

A related message bug: `internal/cli/run.go:2032` tells the user to "add the allow-list to the **project layer** and publish it (fugaro project policy publish)". `budget.allowed_models` lives in the **local project config** (localcfg.go:430); the project layer has no such key (layer.go:98-161). The sentence names the wrong file.

---

## 3. Gap matrix, ranked by how often a normal user needs it × the risk of a hand edit

| # | Gap | Frequency | Hand-edit risk | Rank |
|---|---|---|---|---|
| G1 | Policy-section local keys need a second command (`project policy publish`); `update` neither publishes them nor says they are unpublished | high (budget, models, accounts) | **silent no-op** after `update`; YAML indentation errors in a file with no schema of its own | **1** |
| G2 | Project layer: no local source file, publish is a separate FILE command, `config layer` needs a checkout | high (defaults, profiles, rigor) | silent no-op; an executable change needs a flag; the concurrent-publisher race is handled (config_publish.go:150-151) | **2** |
| G3 | `credentials.*` and `clone_urls` writable only in the Firebase console | low–medium | **highest**: no validation at write time, no diff, no ETag, money (`fallback: auto`) | **3** |
| G4 | `providers` changes need `update --repo` per repository (D787 not built) | medium | silent partial rollout: some repositories route with the old table | 4 |
| G5 | `budget.*` local ceiling (per_run_usd, allowed_models, max_run_tokens): hand only, two propagation paths | medium | a value set but read nowhere until both paths run | 5 |
| G6 | `claude_accounts`, `project_secrets`, job `profiles`: hand only; `project_secrets` must be copied to every operator machine by hand (`secrets set --help`) | medium | a machine without the entry asks for a per-repository token; an allow-list typo widens or narrows a credential | 6 |
| G7 | `review_floor`: hand only; an absent key and `{}` mean different things at publish (localcfg.go:271-277) | low | lowering review by accident is the risk the floor exists to prevent | 7 |
| G8 | `routing`: hand only; the publish refuses silence (project_cmd.go:708-730) | low | a misrouted Claude call, which is a data-egress issue | 8 |
| G9 | Repository `fugaro.yaml` keys (agent.*, git.*, followup.*): hand plus PR | high | low (PR review, `validate`, schema) | 9 |
| G10 | Personal and local keys (max_parallel, commit_name/email, user, watch, update_check): hand | low | low | 10 |
| G11 | Opt-outs without flags (coordinator off: init.go:2445) | rare | medium (Terraform removal shows in the plan) | 11 |
| G12 | Reads: no unified listing; `project repos ls` lacks `--json`; the published shared config is unprinted; env vars are invisible | high | n/a | (see §4B) |

Agent-refusal inconsistencies:
- Refused: `config publish` (config_publish.go:36) and `recipes publish` (recipes.go:332).
- Not refused, although each writes owner policy into the cloud: `project policy publish` (no `agentMarker` in project_cmd.go) and `project repos add` (project_enroll.go; only `--github-app-key` is refused).
- The deliberate exceptions are `budget set|kill` and `accounts clear` (accounts.go:449).

---

## 4. Proposals

### 4A. The publish foundation (required by the HARD RULE before any `set` command ships)

1. **`fugaro update` gains a "Policy publish" stage.** It extends today's migration stage: build the node from the local config with `buildProjectPolicy` (project_cmd.go:670), show the diff, confirm under update's one confirmation, and write under the ETag.
   - This is already the dependency F8 of `docs/models-commands/reconciliation.md` (UC1 part b).
   - `project policy publish` stays, for `--delete`, `--seed-repos` and scripted use.
   - It gains an agent refusal, for parity with `config publish`.
2. **`fugaro update` gains a "Layer" stage** (#1463 D831). It publishes `~/.config/fugaro/projects/<name>.layer.yaml` (D830) through `publishLayer` and `fanOutLayer` (config_publish.go:99, :357).
   - **Migration:** the first `update` on an installation whose layer is published and has no local file *seeds* the local file from the published bytes, shows it, and confirms. Without this, the first `rigor set` or `settings set --layer project` would fail with "no local file", which would be a manual step the HARD RULE forbids.
   - `config publish` defaults its FILE argument to that path.
   - `config layer` stops requiring a checkout when `--project` is given.
3. **`fugaro update` re-converges each recorded repository** whose job's provider or ceiling copy differs (D787, answered). `fugaro upgrade` already computes this drift (upgrade_cloud.go:322-428).
4. **The node-only blocks get a local source (SD5).** Add local config blocks `credentials:` and `clone_urls:`, published by stage 1. The first `update` imports the node's current values into the local file (diff plus confirm), so nothing is lost and no console edit is needed again.

### 4B. One unified read: `fugaro settings`

**Name.** `fugaro settings` (SD1). `config` today means `fugaro.yaml` ("Work with fugaro.yaml") but also lists project configs (`config list`) and publishes the layer. A name that spans the local config, the node and the Realtime Database fits better as its own noun. `fugaro config show` stays, and is `settings --repo . --layer repo,project,default` under the hood (one resolver).

**(A) What exists today, and what it does not cover**

| Command | Prints | Does NOT cover |
|---|---|---|
| `config show [--json] [--workflow] [--offline] [--project-layer F]` (config_show.go:56-190) | every resolved `fugaro.yaml` key of **this checkout** as PATH VALUE SOURCE, with source ∈ `default`, `project`, `profile <n>`, `repo` (resolve.go:17-23, SourceOf :54); the layer's generation/sha/checked_at; `layer_unknown` | local config keys; the policy node; the Realtime Database caps, mode and kills; the shared config; env vars; **other repositories**; the **ceiling clamp** (a repository's `budget.per_run_usd: 50` under a 10-dollar ceiling shows 50 with source `repo`; only `validate` warns); per-run overrides; default values when they differ from the shown value (it shows only the winner); which repository file the value came from (it reads the working tree, not the base branch the run reads) |
| `config layer [--json]` | the published layer's raw YAML | needs a checkout; no resolution |
| `config list [--json]` | project configs, gcp_project, region, shared-config status, checkouts | values |
| `validate [--json]` | problems plus clamp warnings | not a listing |
| `budget show [--json]` | Realtime Database mode, caps, counters, kills, runs in flight | the local and node budget ceilings |
| `project show [--json]` / `project policy show [--json]` | node caps, policy members, allow-list provenance | the local config, and whether local differs from published |
| `budget prices [--json]` | the price table with local overrides | – |
| `accounts ls`, `secrets ls`, `user ls`, `pair ls`, `backends ls` | one block each | – |
| `doctor [--json]` | findings, not values | – |

**(B) Design**

Synopsis:
```
fugaro settings [KEY|GLOB ...] [--repo owner/name | --all-repos] [--layer default,project,profile,repo,local,shared,policy,caps,env,flag]
                [--grep RE] [--changed] [--why] [--offline] [--json]
```

One row per (key, scope), where scope is `project` or `repo:<canonical>`. The text columns are:

`KEY  VALUE  WINS  DEFAULT  STATE`

- **KEY** is a stable dotted name from a key registry (§4C).
  - The names are unique across stores, so a field never changes meaning. For example, `budget.mode` is the policy ceiling mode (local config, node, `fugaro.yaml`), while `caps.mode` is the Realtime Database `config/mode`.
  - The two "profiles" are named distinctly as `layer.profiles.<n>.*` and `job_profiles.<n>.*` (SD14).
- **WINS** is the level that decided the effective value. It uses the precedence derived from the code (stated once in the help and in the schema):
  - **Resolved `fugaro.yaml` keys:** `default` < `project` (layer `defaults:`) < `profile <n>` (layer profile, `workflows.*` keys only) < `repo` < `flag` (InOverride keys only: scope.go:15, :58-63). Source: resolve.go:80-85.
  - **Ceiling keys** (`budget.{mode,per_run_usd,allowed_models,max_run_tokens}`, `governance.*`): `default` → `local` (the job environment as of the last `update --repo`; validate.go:65-77) → `policy` (the node replaces it per key; `runner/policy.go:258`) → `project` (layer governance may loosen Fugaro's defaults; validate.go:79-100) → `repo` (default branch) → `branch` (the run's branch).
    - **Tighten only.** The smaller cap, the stricter mode and the intersection of lists win (merge.go:31-39).
    - A looser lower value is shown as `ignored` under `--why`, from `Effective.Ignored`.
  - **`review_floor`:** `policy` ∪ lower layers (a union; a lower layer only adds; localcfg.go:255-266).
  - **`rigor.default`:** `repo` > `project` > `default` (`pro`, `internal/rigor/mode.go:46`). `rigor.areas` is a union of both layers (scope.go:124-130).
  - **Runtime caps** (`caps.*`, `kill.*`): `caps.defaults` < `caps.repo` for repository caps (model.go:246-255). They are independent of, and enforced beside, the ceiling.
  - **Local and installation keys:** `local` only. For keys in the shared projection, `shared` stands in when this machine's file lacks them (`MergeShared`, shared.go).
  - **Env vars:** `env` overrides `local` where both exist (`update_check`).
- **VALUE** is the effective value. **Secret-bearing values are never printed.**
  - No setting value is a secret today: provider `secret` and accounts are names (providers.go:50), and `mailbox` holds a keychain item name.
  - The registry still carries a `sensitive` flag. Such keys print `set`/`unset` only. `budget.firebase_api_key` is public but should be redacted by default.
  - Unknown node members print their names, never their values (the precedent is init_policy_migrate.go:38-41).
- **DEFAULT** is the built-in default with its source, or a sentence saying what unset means (`unset = off`, `unset = inherit`, `unset = every repository`, `none`). A key set at no layer shows WINS `default` and VALUE equal to DEFAULT, never blank.
- **STATE** is one of:
  - `ok`
  - `stale <age>` (a cached layer read offline)
  - `unread: <reason>` (no permission, offline, no budget backend), drawn as such and never filled with a default
  - **`unpublished`**: the local value differs from the published node or layer, so it will reach runs on the next `fugaro update`. This is the G1 and G2 detector.
  - `unknown (no checkout)` for a repository with no checkout on this machine (the convention of models spec §2.4)
- **`--why`** adds one sub-row per level that sets the key: the value, and `ignored (looser than <winner>)` or `overridden`.
- **`--changed`** shows rows whose value differs from DEFAULT.
- **`--all-repos`** iterates the local config's `repos:` plus remembered checkouts (`internal/localcfg/checkouts.go`).
- **`--json`** prints one versioned document: `{"version":1, "project":…, "sources":[{"layer":"policy","read_at":…, "duration_ms":…, "status":"ok|stale|unread","note":…}], "settings":[{"key","scope","value","wins","default":{"value","source"},"state","levels":[…]}]}`. Members are only ever added within a version, and the schema goes in `schemas/settings.schema.json`.
- **Security:** read-only, so it is allowed in an agent session. It never calls a Secret Manager access method (names only, as `secrets ls`), and never reads Terraform state.

**(C) Reuse and reader cost**

| Layer | Reader to reuse | Cost |
|---|---|---|
| default | the new defaults registry (§4C) | offline, µs |
| repo / project / profile | `config.Resolve` + `Resolution.SourceOf` (resolve.go:54, :90); `showValues` (config_show.go:93) | offline per checkout; the layer from the cache (layercache.go) or one GCS read |
| local | `localcfg` select + Parse | offline, ms |
| shared | `ParseShared`, the shared cache (sharedcache.go) | offline cache; GCS to refresh |
| policy | `projectpolicy` decode (read.go), as `project policy show` does | one RTDB read (ADC), roughly 100–500 ms |
| ceiling merge | `runCeiling` (validate.go:92) plus `policy.Merge` (merge.go:39) for `Effective.Sources/Ignored` | offline once the inputs are read |
| caps / kill | the budget show reader (budget.go:243; `db.Get("config/caps")` as livedays.go:28) | one RTDB read |
| env | `os.Getenv` of §1.8 | offline |

**Rendering order** (the console's progressive-render lesson):
1. Print every offline layer at once.
2. Then the cloud layers, in parallel with a bounded timeout (the 5-second precedent of `accounts ls --help`), each row marked with its source's `read_at` and duration.
3. `--offline` stops after step 1, and the cloud rows read `unread: offline`.

**(D) The console**

A read-only **Settings** view that spawns `fugaro settings --json --offline` first, then `fugaro settings --json`. The command must be added to the console's spawn allow-list (the same list `config list --help` mentions). The view:
- groups rows by layer and scope;
- draws `unpublished`, `stale` and `unread` with distinct badges;
- offers a "copy command" for the CLI `set` that would change a row;
- writes nothing.

This is a later task, after the CLI's JSON schema is fixed.

### 4C. Defaults: where they live today, and a recommendation

Today the defaults are scattered:
- **`applyDefaults`** (`internal/config/defaults.go:199-264`) uses inline literals:
  - `"main"` (although `DefaultBaseBranch` exists at basebranch.go:6), `max_budget_usd` 25, `cpu` 4, `memory` `8Gi`, `total` 90m, and derived timeouts (`stage` min(40m, total), `verify` min(30m, total), `finalize_reserve` min(5m, total/5), `render` the remainder).
  - Reports globs.
  - `Rebuild.Defaults` (config.go:686-700): daily, 14d, true, true.
- **Named constants:**
  - `DefaultReviewRounds` 2, `DefaultFirstLineRounds` 1, `DefaultAgentAuth` "vertex" (defaults.go:40-46)
  - `DefaultJobProfile` "standard" (config.go:644)
  - `DefaultInput*` (config.go:984-986)
  - `ABDefaultMaxPairsPerDay` 5 (ab.go:59)
  - `rigor.Default` pro (rigor/mode.go:46)
  - `budget.DefaultGrace` 3m (budget/grace.go:10)
  - `projectpolicy.Default{WaitThreshold,BackoffInitial,BackoffCap,ConcurrencyPerToken}` (policy.go:416-419)
  - governance `WithDefaults` (policy/policy.go:249)
- **Local config defaults** sit in the loader as literals: `max_parallel` 20, `build.machine_type` E2_HIGHCPU_8 (localcfg.go:1157-1164).
- **Schemas:** there are **no** JSON-Schema `default` keywords. The only `"default"` strings in `schemas/*.json` are the property name `rigor.default` (fugaro.schema.json:203, project-layer.schema.json:60) and a `not const "default"` (fugaro.schema.json:89). Nothing tests schema defaults against the code, because there are none to test.
- **`internal/config/example.yaml`** restates several defaults in comments (e.g. lines 21, 25, 74-75). I found no test that compares them with `applyDefaults`; `TestExampleForFillsProject`, config_test.go:495, checks only the project line.

**Recommendation (SD16): build one machine-readable registry.** This would be `internal/settings/registry.go`, or an extension of the `config.Scopes` rows (scope.go:44). Each row carries the key, the stores and layers it may be set in, the merge rule (replace, tighten or union), the default (value plus constant, or an "unset means" sentence), the `sensitive` flag and the owning command. Then:
- `applyDefaults` and the local loader read their values from it;
- `settings --changed` and the DEFAULT column read it;
- the JSON Schema `default` keywords and the `docs/project-layer.md` scope table are generated from it, or pinned against it by a test (as `TestScopeTableMatchesDocs` already does);
- a test asserts every `fugaro.yaml`, layer and local-config field has a row, the same guard `TestExecutableKeyValuesCoversEveryExecutableKey` gives executable keys.

### 4D. The write side: one generic path plus structured families

**Shared mechanics** (the models spec §4.5 and §9, and #1463):
- One node-tree editor package for the three documents (local config, local layer file, repository `fugaro.yaml`): locate in the node tree, splice bytes.
- Strict re-parse and a decoded-config comparison.
- Reuse `backupFile`, `writeFileAtomic` and `lineDiff` (init.go:2583, :2619, :2646). Not `localcfg.Config.Marshal` (localcfg.go:1610), which drops comments.
- Every local or layer write ends with `next: fugaro update`.
- In an agent session, a local or layer write prints the diff and the command and writes nothing, `--yes` included (D789). A write to the repository `fugaro.yaml` is allowed.
- Loosening a ceiling or floor asks for the typed project name (the `budget set` precedent).

| Family | Synopsis | Writes | Covers |
|---|---|---|---|
| **generic** | `fugaro settings get KEY [--repo R]`; `fugaro settings set KEY VALUE [--layer local|project|repo] [--repo R] [--yes]`; `fugaro settings set KEY --add V \| --remove V` (lists); `fugaro settings unset KEY [--layer …]` | the layer's file | every **scalar or string-list** key in the registry: `budget.{per_run_usd,max_run_tokens,allowed_models,unreachable_grace}`, `base_branch`, `repos.<r>.base_branch`, `max_parallel`, `update_check`, `watch.burn_alert_usd_per_hour`, `commit_name/email`, `defaults.git.*`, `defaults.agent.*`, repository `agent.*`, `git.*`, `followup.*`, `governance.*` scalars, `default_profile`. It refuses keys owned by a dedicated family and points to that family |
| `models` (designed) | `ls`, `search`, `add`, `rm`, `use` | local; repository (`use`) | providers routes (extend only), model_prices, allowed_models, agent.models.* |
| `rigor` (designed, #1463) | `show`, `set <mode> [--repo R \| --area GLOB]`, `unset` | local layer file; repository | rigor.* |
| `rigor floor` | `fugaro rigor floor show [--json]`; `set <mode> <l1,l2,…>`; `unset <mode>` | local `review_floor` | G7; lowering needs the typed name (SD10) |
| `accounts` | `fugaro accounts add NAME --allow REPO…`; `allow\|deny NAME REPO`; `rm NAME` | local `claude_accounts` | G6 (the token stays `secrets set`/`creds set`) |
| `secrets allow` | `fugaro secrets allow NAME REPO-PATTERN…`; `deny`; `--proxy` | local `project_secrets` | G6. Removes the "copy to every operator machine" step: publish through the node, and teach `update --repo` to read it from there (SD17) |
| `project profile` | `fugaro project profile ls\|show [--json]`; `add NAME --provider P --cpu N --memory M --timeout-class C`; `set NAME KEY VALUE`; `rm NAME` | local job `profiles` | G6 |
| `project routing` | `fugaro project routing show [--json]`; `set claude --via direct\|cascade\|openrouter\|auto [--models …]`; `unset` | local `routing` | G8 (keeps the publisher's silence rules) |
| `project credentials` | `fugaro project credentials show [--json]`; `set fallback auto\|oauth-only…`; `set wait-threshold 15m`; `set backoff --initial D --cap D`; … | local `credentials:` (new, SD5) | G3 |
| `project clone-url` | `fugaro project clone-url ls\|set CANON URL\|rm CANON` | local `clone_urls:` (new, SD5) | G3 |
| `config` (layer) | `config layer [--project P]` without a checkout; `config publish [FILE]` defaulting to the local layer file; `config layer pull` (re-seed from published) | – | G2 |
| `update` negations | `--no-coordinator`, … | local `terraform.*` | G11 |
| unchanged | `budget set\|kill\|resume`, `user add\|rm`, `project repos add\|rm\|sync`, `secrets set`, `creds set`, `pair`, `managed flag`, `allow-delete` | – | already commands; add `--json` to `project repos ls` |

---

## 5. Recommended first slice

1. **`fugaro settings` (read-only)** over default, repo, project, profile, local, shared and env (offline), plus policy and caps (cloud).
   - Includes the WINS, DEFAULT and STATE columns with `unpublished` drift, plus `--json`, `--why`, `--changed`, `--offline`, `--repo` and `--all-repos`.
   - Starts the defaults registry with every key the read prints.
   - It is useful on its own (it makes G1, G2 and G5 visible today), has no security surface beyond reads, and every later `set` command reuses its registry.
2. **The `fugaro update` policy-publish stage** (4A.1; models F8, UC1 part b). Without it, no local `set` can ship under the HARD RULE.

Then, in order: the layer stage plus seeding (4A.2, #1463 R1) → the shared node-tree editor → generic `settings set|unset` for local scalars → `project credentials` and `clone-url` with the import migration (G3) → `accounts`, `secrets allow` and `project profile` → `rigor floor` → the console Settings view.

---

## 6. Decisions for the owner

No D-number is taken. The numbers are assigned when a design folder is written.

| # | Question | Recommendation | Alternative and its cost |
|---|---|---|---|
| SD1 | Name of the unified command | **`fugaro settings`** (read, plus `get`/`set`/`unset`); `config show` stays as its repository view | `config show --all`: overloads a group that already means three things, and it needs a checkout today |
| SD2 | Generic `set` or one command per block | **Hybrid**: generic for scalar and list keys from the registry, dedicated families for structured blocks (routes, accounts, profiles, routing, credentials, floor) | Generic only: typing a provider map on a command line is error-prone, and the cross-key checks are lost |
| SD3 | Does `fugaro update` publish the policy node from the local config every run? | **Yes**, behind its one confirmation, with a diff; `project policy publish` keeps `--delete`/`--seed-repos` | Keep the separate publish: violates the HARD RULE (G1) |
| SD4 | Does `fugaro update` publish the project layer, with a seeded local file? | **Yes** (#1463 D830/D831, plus seeding on first update) | `config publish FILE` only: G2 stays |
| SD5 | `credentials.*` and `clone_urls`: where is the source of truth? | **Move them to local config blocks published by update; the first update imports the node's values** | Commands that write the node directly: a second door to the cloud outside update (the model D780 refused) |
| SD6 | Two modes: the policy `budget.mode` and the Realtime Database `config/mode` (`budget set --mode`, and `update --budget-mode` sets both) | **Keep both, name them distinctly in settings keys (`budget.mode`, `caps.mode`)**, and have `update --help` say which it writes | Merge into one: changes what a field means, which is forbidden |
| SD7 | `budget set|kill` stay immediate writes outside update? | **Yes**: they are runtime controls (the kill switch must be instant), not upgrade state | Route them through update: a kill switch delayed is useless |
| SD8 | Agent refusal for `project policy publish` and `project repos add` | **Add it** (parity with `config publish`, `recipes publish` and `user add`) | Leave as is: an agent can widen the allow-list or publish a policy |
| SD9 | Agent session behaviour for local or layer writes | **Print the diff and the command, write nothing, `--yes` included** (D789); repository writes allowed | Allow with `--yes`: contradicts D789 |
| SD10 | Where the review floor command lives | **`fugaro rigor floor show|set|unset`**, with lowering behind the typed name | `project floor`: splits rigor across two nouns. #1463 asks that `rigor set` itself gain no floor flag, which this respects |
| SD11 | Provider drift re-converge in update | already answered (D787); listed so the dependency is visible | – |
| SD12 | Env vars as a settings layer, without a `set` | **Show only** | Write to a shell profile: not Fugaro's |
| SD13 | Should project-layer writes stay launcher-reachable (bucket objectAdmin)? | **Out of scope here; record it.** The layer holds `defaults.agent.*` that a launcher can change (D792) | – |
| SD14 | The two "profiles" (layer workflow profiles vs local job profiles) | **Distinct settings keys now (`layer.profiles`, `job_profiles`); a YAML rename is a separate decision** | Rename the YAML key now: churn across policy, node and docs |
| SD15 | Negation flags for opt-ins (`--no-coordinator`) | **Add them as needed**, so turning an opt-in off is a command | Hand edit (today) |
| SD16 | One machine-readable defaults and keys registry | **Build it**; generate or pin the schema `default`s, the scope table doc and `applyDefaults` from it | Keep scattered: the DEFAULT column and `--changed` would need a fourth copy |
| SD17 | The `project_secrets` per-machine copy | **Publish it in the node and read it from there in `update --repo`** | Keep the manual copy: a documented hand step on every operator machine |
| SD18 | Who may `settings set --layer local` | **Anyone may edit their own file; the boundary stays IAM at publish** (update's policy and layer writes) | Enforce roles locally: not enforceable on a file the user owns |

---

## 7. Smaller findings noticed along the way

- `internal/cli/run.go:2032`: the message names the project layer as the home of `budget.allowed_models`; it is the local project config (localcfg.go:430).
- `fugaro config layer` requires a git checkout (config_show.go:205-208), so an owner outside a repository cannot read the layer.
- `fugaro project repos ls` has no `--json`; every other read in this area has one.
- `applyDefaults` hard-codes `"main"` (defaults.go:201) beside `config.DefaultBaseBranch` (basebranch.go:6). These are two copies of one default.
- `fugaro config show` reports a repository's `budget.per_run_usd` as source `repo` even when the ceiling clamps it; only `validate` says so (validate.go:105-125).
