# Composite management commands: reconciliation

`spec.md` as amended by the code and by the owner's answers. Where this file disagrees with
`spec.md`, this file wins. Checked against `origin/main` at `1ad2d3143` on 2026-11-07.
Decisions take **D1560-D1579** and checks **390-399**; all are reserved, used or not.
The sibling package `docs/no-prompt-policy/` (D1540-D1559, checks 380-389) is the spec that rule 5
points at; this package depends on it and says where (External edges in the plan).

## 1. Headline: spec concept to what the repo has

| Spec concept | What exists today |
| --- | --- |
| Management commands over the YAML files | `fugaro settings set/unset/get` (`internal/cli/settings_set.go`, three layers: local, project, repo), `fugaro image add/rm` (`image_catalog.go`, `runImageAdd`), `fugaro accounts`, `fugaro project repos add/rm/sync` (`project_enroll.go`), `fugaro recipes publish`, `fugaro budget set`, `fugaro secrets/creds set` |
| "Policy publish" | `fugaro project policy publish` (`project_cmd.go`) writes `config/policy/shared`; `fugaro config publish` (`config_publish.go`) writes the project layer; both are also done inside `fugaro update` (`init_policy_publish.go`, `init_layer_stage.go`) |
| "Update" | `fugaro update` (`update.go`, `updateDoor`): **a list of named stages** (`initStages` in `init_stages.go`; names, order and `needs` edges in `internal/initflow/stage.go`, `Names()`/`Needs()`): installation, firebase, images (mirrors and pins the **base** images, builds no catalog image), rules, policy, layer, plugin, repository |
| A single apply command | **None.** `apply` is not a top-level command; the only apply is `fugaro managed apply ROOT` (`managed_apply.go`, `newManagedCmd`), the managed Terraform root's |
| Pending-steps record | **None** (`git grep` finds no pending/needs-publish state) |
| `--no-apply` / `--only` | Neither exists in `internal/cli`; `--only` is unused |
| Console through the same commands | Reads: exact argv in `consoleReadPaths` (`internal/cli/project.go`). **Writes too**: `consoleLaunchPaths` (`internal/cli/docs_desktop_console_test.go`) and the `settings.edit` row of `console/src/main/spawn.ts` spawn `settings set`/`settings unset` with `--layer` and the phase words `--dry-run`/`--yes` |
| A subset of the stages run by one command | `accounts add` already runs three stages in-process (`accountsAddStages` in `accounts_add_converge.go`, built from `initStages`: `Policy`, `Installation`, `Repository`) |

## 2. Findings

- **F1 (Wrong, amended D1560).** The brief and the owner's question assumed `fugaro apply ROOT`
  exists at the top level and would have to move under `managed`. It is already
  `fugaro managed apply ROOT`; top-level `apply` is free. No rename, no docs/skills churn for it.
- **F2 (Exists, corrected).** `image add` writes only this machine's project config ("Nothing is built and
  nothing is published here"). What a catalog entry then needs is: the policy stage (publishes
  `image_catalog` in the policy node, inside `update`), a **catalog image build** (`fugaro image build
  <name>`, `image_build_catalog.go`, billable, typed today) and a job redeploy. **`fugaro update` never
  builds a catalog image**: its `images` stage mirrors and pins the base images (`init_images.go`), and
  catalog builds exist only in `image build <name>` and the daily check job (`image_check_catalog.go`).
  So "update = all steps" was wrong (D1561 amended, D1577).
- **F3 (Wrong, corrected).** `update` is **not** one undivided converge. `initStages` builds named
  stages and `internal/initflow/stage.go` holds the order with `needs` edges (`Names()`, `Needs()`);
  `accounts add` already runs a subset in-process (`accountsAddStages`: "invokes the same stages
  `fugaro update` does rather than a copy of them"). The registry is **built over the initflow stage
  names** (no second registry: search before you write a helper); the only non-stage step is the
  catalog image build.
- **F4 (Gap).** Not every change is "live" after a publish. A **repo-layer** setting edits a checkout's
  `fugaro.yaml`, live only when merged. No command may commit or open a PR in a customer's repository
  quietly (no-prompt rule 5). D1564.
- **F5 (Rule).** Each stage keeps what its door has today: the agent refusal (`agentMarker`,
  `initflow.AgentRefusal`), the managed gate and the **loosening judgement** (a publish that loosens a
  ceiling takes the typed name today, `init_policy_publish.go`; the managed gate refuses unjudged members
  such as `image_catalog`). Extracting a stage must not drop any of the three (Global constraint, tested
  per stage under the agent marker with a mutation check). The no-prompt plan changes the typed name and
  the refusal; this plan does not.
- **F6 (Rule).** The catalog build costs money and goes through the no-prompt plan's budget lease
  (D1543). Today `image build` asks a typed confirmation and a count bound (`ImageBuilds`) limits it. An
  unmanaged project must not reach a billable build unasked through `--no-apply` then `apply` before
  the no-prompt slices land (Global constraint).
- **F7 (Wrong, corrected).** The console **already spawns write verbs**: `settings set`/`unset` through
  `consoleLaunchPaths` and the `settings.edit` row of `spawn.ts`, which always passes `--yes` (and
  `--dry-run` for the preview). After AP5 a console settings edit would run the chain and its `--yes`
  would be read as answering steps. D1572.
- **F8 (Rule).** `plugin/skills` name commands and flags that the lint checks, within `maxSkillBytes`
  (24 KiB) per file; docs are tested (`go test ./internal/cli -run Docs`).
- **F9 (Rule).** A new wire shape (pending file, `apply --status --json`) obeys "a field never changes
  meaning": versioned, additive, schema in `schemas/`.
- **F10 (Open, answered D1563).** A failed follow-up leaves the change saved and the step pending, with
  one sentence naming `fugaro apply`: a tension (section 4). **`accounts add` is the exception**: it is
  already composite and its failure rule is "error, re-run repairs", not "mark pending"; AP6 keeps that.
- **F11 (Audit set, redone).** The set is the output of
  `git grep -lE "fugaro (update|config publish|project policy publish|image build|image refresh)" -- 'internal/cli/*.go' ':!*_test.go'`
  at task time (about 90 files: nearly every refusal text). The files whose text is phrased as a **next
  step** ("run / next / then / now / with ... fugaro update|config publish|...", "`fugaro update`
  publishes ...") on 2026-11-07: `accounts_add_converge`, `accounts_rm`, `config_publish`, `config_show`,
  `doctor`, `doctor_batch`, `doctor_layer`, `doctor_render`, `exec`, `history_rollover`,
  `image_build_catalog`, `image_catalog`, `image_check_catalog`, `image_layer`, `image_promote`,
  `image_refresh`, `image_refresh_rules`, `image_refresh_steps`, `imagecheck`, `init`, `init_anchor`,
  `init_credentials`, `init_firebase`, `init_fugaroyaml`, `init_images`, `init_layer_stage`,
  `init_new_machine`, `init_plugin_stage`, `init_policy_migrate`, `init_project`, `init_retire`,
  `launch_budget`, `models_add`, `operator_write`, `pair`, `project_cmd`, `project_enroll`,
  `project_job`, `recipes_skew`, `restore`, `reviewverify`, `run`, `settings_set`, `sharedcfg`,
  `skillwarn`, `update`, `upgrade_cloud`, `user`, `validate`, `workspace`. (`review.go` names the
  commands without a next-step phrasing; the task's grep decides.) The audit classifies each text:
  (a) a chain a management command now runs itself, (b) a stale-installation fix naming `fugaro update`
  or `fugaro apply` (stays: the CLAUDE.md hard rule), (c) not a next step.
- **F12 (UNVERIFIED, amended).** The earlier claim that pending steps are "derivable from doctor drift"
  is not established: `doctor_layer.go` detects an unpublished **project layer** and its three causes,
  nothing for the catalog or other keys. The file is authoritative for what is owed; only the layer's
  drift is recomputed. D1575 says how a deleted file is detected.
- **F13 (Gap).** `settings set budget.*` followed by an automatic publish raises a cap through a path
  the no-prompt plan's owner-only cap raise (D1542) does not fence (it fences `budget set`). Once the
  gate is audit-only (D1545) nothing else stops it. D1576.
- **F14 (Rule).** `image add` has **no `agentMarker` check** today (it was inert). With a chain it is an
  agent-reachable route to publish, a Cloud Build of an `--inline` Dockerfile and a redeploy, possibly
  `--default`: executable content of the kind `settingsProfilesRule` (`settings_managed.go`) refuses for
  profile keys. D1573.
- **F15 (Gap).** A job redeploys **per checkout** (`update --repo`); the `deploy` step has a repository
  scope. D1574.
- **F16 (Rule).** The pending file is writable by an agent session that also edits the local config;
  the owner's later `apply` would publish it with the owner's credentials. D1575.

## 3. Decisions

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1560** | Name of the pending-steps command, given `apply ROOT` | **`fugaro apply`, bare, applies the pending steps.** | A different word: clumsy for the thing run most | 2026-11-07: recommended; **amended after review (F1)**: the premise ("ROOT form moves under managed") is false, it is already `fugaro managed apply ROOT`; owner's intent kept, no rename needed. Confirm in 4. |
| **D1561** | How are follow-up steps defined | **Named steps in one registry, built OVER the initflow stage names (F3)**: each setting key and resource type declares the stages it needs (plus the one non-stage step, the catalog image build); `initflow` orders and dedupes; `update` stays the full stage list and `apply` runs a subset the way `accountsAddStages` does | Own registry: a second list that can disagree; or full `update` after every change: slow and billable | 2026-11-07: recommended; **amended after review (F2, F3)**: the premise "`update` = all steps, one converge" was wrong (named stages exist; `update` builds no catalog image). Intent kept. Confirm in 4. |
| **D1562** | Where pending steps are kept | **A local file beside the project config**, per machine; the console reads it through a CLI read; doctor recomputes drift | Recompute only (no file) or in the cloud policy node | 2026-11-07: recommended |
| **D1563** | A follow-up fails after the change is saved | **Keep the change, mark pending, exit non-zero, say "saved, not live yet" and name `fugaro apply`** | Roll back: loses the user's input, needs per-setting undo | 2026-11-07: recommended; tension recorded in 4 |
| **D1564** | A repo-layer change (a checkout's `fugaro.yaml`) | **The command writes the file and says plainly it is live after the change is merged; it commits and opens nothing.** Nothing is pending (no Fugaro step can make it live) | Command opens a PR: touches an outside resource (no-prompt rule 5) | *(empty: not yet asked)* |
| **D1565** | The skip flag's name | **`--no-apply`** only (`--only` is unused but reads as "apply only this"). One shared flag helper, so every command spells it identically | `--only` as an alias: two spellings | *(empty: not yet asked)* |
| **D1566** | Which commands are management commands | **In:** `settings set/unset`, `image add/rm`, `accounts add/rm` (`add` is already composite, F10), `project repos add/rm/sync`, `models add/rm`, the `workspace` writes (`workspace.go`: add, rm, member add/rm, cap set), `project policy publish` (is itself a step). **Out (no follow-up exists, they write the cloud now):** `budget set`, `secrets/creds set`, `recipes publish`, `allow-delete` | Include them with an empty step list: same outcome, more wiring | *(empty: not yet asked)* |
| **D1567** | `update` and pending | **`update` runs every step and clears the pending file;** `apply` runs only what is pending. Same step code | `apply` as an alias of `update`: runs billable steps with nothing owed | *(empty: not yet asked)* |
| **D1568** | `fugaro apply` with nothing pending | **Exit 0 with "Nothing to apply."** and, in `--json`, an empty list | Exit non-zero: breaks scripts | *(empty: not yet asked)* |
| **D1569** | Console reads | **One new read, `fugaro apply --status --json`, on the fence (row, `spawn.ts`, Go equality test); other console pending writes stay the console plan's** | Console runs the pending writes now | *(empty: not yet asked)* |
| **D1570** | The pending file's shape | **Versioned JSON, `schemas/pending-steps.schema.json`, additive only; carries step names and the change that asked for it, never a value** | Free-form | *(empty: not yet asked)* |
| **D1571** | Agent sessions and unmanaged projects | **No rule of their own: a chain's steps run under the no-prompt plan's rules (D1541, D1542, D1545).** On a project those refuse, the command saves and marks pending | Own rules here: two rule sets | *(empty: not yet asked)* |
| **D1572** | The console's `settings.edit` (always `--yes`) after AP5 | **The row also passes `--no-apply`** (a row word in `spawn.ts`, the equality test, the typed contract); the console shows pending steps from the D1569 read. `--yes` covers only the local write, never steps | Console runs the chain: its `--yes` answers steps it never showed | *(empty: not yet asked)* |
| **D1573** | `image add` and executable catalog entries in agent sessions | **In a coding agent's session `image add` refuses `--inline`, `--dockerfile` and `--default` unless the project is managed AND the policy judgement admits it; `--image <ref>` follows the profile-key rule.** AP4 is opus + independent review (in-run level 3) | Chain runs for any agent: an agent publishes, builds and deploys code it wrote | *(empty: not yet asked)* |
| **D1574** | Deploy scope | **`deploy` is per repository; `apply` redeploys the checkout's repository; entries for repositories with no checkout here stay pending and the sentence says where to run `fugaro apply`** | Deploy every enrolled repository from anywhere: needs every repo's config | *(empty: not yet asked)* |
| **D1575** | The pending file's trust | **Each entry binds the Fugaro project name and the GCP project ID and `apply` refuses a mismatch; `apply` prints each step's diff, re-judges loosening itself whoever queued it, and shows the entry's "change that asked" through `safetext`. A deleted file is detected by a `last_apply` stamp (hash of the local project config at the last successful apply) that `doctor` compares: a mismatch with no pending file says "config changed, nothing owed is recorded"** | No binding: an agent can queue what the owner then publishes | *(empty: not yet asked)* |
| **D1576** | `settings set budget.*` then publish routes around the cap-raise fence (F13) | **The publish step refuses a loosened budget key in an agent session (same refusal as `budget set`, naming the owner's command); the owner's session publishes** | Leave it: the cap stops being a limit | *(empty: not yet asked)* |
| **D1577** | Does `update` gain the catalog build step (F2) | **No. The catalog build is a registry step of `image add`/`apply`, leased from the budget; `update` stays the stage list** | `update` builds every catalog image: a billable surprise on every update | *(empty: not yet asked)* |
| D1578-D1579 | Reserved | | | |

## 4. Open tensions and open items for the owner

1. **D1563 against the spec's "never tell the user to run a command".** "Saved, not live yet; run `fugaro apply`" is the single sanctioned exception (it names neither publish nor update). Confirm, or choose rollback.
2. **D1560 and D1561 amended after review** (F1; F2, F3): confirm the corrected texts.
3. **D1564-D1577 were not put to the owner** (recommendations stand). D1572-D1576 are security-relevant (console `--yes`, agent reach of `image add`, pending-file trust, the cap-raise route) and should be asked before any task is approved.
4. **Sequencing.** With an audit-only gate (D1545), the refusals D1573 and D1576 rely on must hold on their own (this plan states them) or be restated by the no-prompt plan. Until it lands, a chain's steps refuse in a coding agent's session exactly as `update` does.

## UNVERIFIED

- Whether other console screens call write verbs beyond `settings set/unset` (only `consoleLaunchPaths` and the `settings.edit` row were read).
- Whether `recipes publish` has a follow-up (`recipes_skew.go` not read in full).
- Whether drift for keys other than the project layer can be recomputed (F12).
