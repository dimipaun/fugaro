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
| "Update" | `fugaro update` (`update.go`, `updateDoor`, one converge: Terraform, rules, policy, layer, image builds, plugin pin, local config) |
| A single apply command | **None.** `apply` is not a top-level command; the only apply is `fugaro managed apply ROOT` (`managed_apply.go`, `newManagedCmd`), the managed Terraform root's |
| Pending-steps record | **None** (`git grep` finds no pending/needs-publish state) |
| `--no-apply` / `--only` | Neither exists in `internal/cli`; `--only` is unused |
| Console through the same commands | The console spawns only exact argv in `consoleReadPaths` (`internal/cli/project.go`), held equal to `console/src/main/spawn.ts` by a Go test |

## 2. Findings

- **F1 (Wrong, amended D1560).** The brief and the owner's question assumed `fugaro apply ROOT`
  exists at the top level and would have to move under `managed`. It is already
  `fugaro managed apply ROOT`; top-level `apply` is free. No rename, no docs/skills churn for it.
- **F2 (Exists).** `image add` writes only this machine's project config (its Long text says
  "Nothing is built and nothing is published here: fugaro image build ... and fugaro update publishes
  the catalog"). So the three-command chain the spec complains about is real, and the steps a catalog
  entry needs are: publish the catalog (inside `update`), build the image, redeploy the job that uses it.
- **F3 (Gap).** `update` is one undivided converge. A one-line setting needs "publish the policy" and
  nothing else; running all of `update` (Terraform plan, image rebuilds, billable) for it is wrong. The
  registry (D1561) needs `update` split into callable steps with the same code behind both doors.
- **F4 (Gap).** Not every change is "live" after a publish. A **repo-layer** setting (`settings set
  --layer repo`) edits a checkout's `fugaro.yaml`, which takes effect only when merged. No command may
  commit or open a pull request in a customer's repository quietly: that is the no-prompt spec's
  rule 5 (outside the managed project is a design decision). D1564 settles the behaviour.
- **F5 (Rule).** `fugaro update` and `config publish` refuse a coding agent's session unless the project
  is managed and the gate admits (`agentMarker`, `initflow.AgentRefusal`). A composite command run by an
  agent inherits that; it is the no-prompt plan (D1541, D1542, D1545) that changes it, so this plan's
  chains wait on it (External edges).
- **F6 (Rule).** A chain step that costs money (an image build) goes through the no-prompt plan's budget
  lease (D1543). Today `image build` asks a typed confirmation and a count bound (`ImageBuilds`)
  limits builds; neither may be what a composite command runs into.
- **F7 (Rule).** The console reads through an exact-argv fence (`consoleReadPaths`): a pending-steps
  read the console draws needs a row, a `spawn.ts` row and the Go equality test. A write verb is not on
  the fence at all; D1569 keeps console write chains out of this plan.
- **F8 (Rule).** `plugin/skills` name commands and flags that the lint checks against the CLI, within
  `maxSkillBytes` (24 KiB) per file; any skill that says "now run `fugaro update`" is changed in the
  same PR as the command (CLAUDE.md, "Plugin"). Docs are tested (`go test ./internal/cli -run Docs`).
- **F9 (Rule).** A new wire shape (the pending file, `fugaro apply --status --json`) obeys "a field never
  changes meaning": versioned, additive, with a schema in `schemas/`.
- **F10 (Open, answered D1563).** A failed follow-up leaves the change saved and the step pending.
  That puts one sentence in front of the user that names a command (`fugaro apply`), which the spec's
  "no message tells the user to run publish or update by hand" does not forbid (it names neither), but
  it is a tension listed in section 4.
- **F11 (Audit set).** These `internal/cli` files print "run fugaro update / config publish / policy
  publish / image build / image refresh" text and are the rule's audit set (non-test, found with
  `git grep` on 2026-11-07): `config_publish`, `doctor`, `doctor_batch`, `doctor_layer`, `exec`,
  `history_rollover`, `image_build_catalog`, `image_catalog`, `image_check_catalog`, `image_layer`,
  `image_promote`, `image_refresh`, `image_refresh_steps`, `imagecheck`, `init`, `init_anchor`,
  `init_firebase`, `init_layer_stage`, `init_new_machine`, `init_plugin_stage`, `init_project`,
  `launch_budget`, `project_cmd`, `readyaml`, `recipes_skew`, `restore`, `reviewverify`, `sharedcfg`,
  `skillwarn`, `upgrade_cloud`, `user`, `validate`, `workspace`. Many are **doctor/refusal** texts
  that legitimately name `fugaro update` for a stale installation (the CLAUDE.md hard rule: update is
  the one command); the audit classifies each as (a) a chain a management command should now run itself,
  (b) a stale-installation fix naming `fugaro update`/`fugaro apply`, which stays, or (c) not a
  next-step at all. Only (a) moves into a command.
- **F12 (Gap).** Doctor already knows drift ("update" class `doctorClassUpdate`); the pending-file's
  contents are derivable from it, which is what makes D1562 safe (the file is a cache of intent, never
  the only copy of what is owed).

## 3. Decisions

| # | Question | Recommendation and reason | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1560** | Name of the pending-steps command, given `apply ROOT` | **`fugaro apply`, bare, applies the pending steps.** | A different word: clumsy for the thing run most | 2026-11-07: recommended; **amended after review (F1)**: the premise ("ROOT form moves under managed") is false, it is already `fugaro managed apply ROOT`; owner's intent kept, no rename needed. Confirm in 4. |
| **D1561** | How are follow-up steps defined | **Named steps in one registry** (publish-layer, publish-policy, image-build, deploy, plugin-pin); each setting key and resource type declares its steps; the registry orders and dedupes; `update` = all steps | Full `update` after every change: slow and billable | 2026-11-07: recommended |
| **D1562** | Where pending steps are kept | **A local file beside the project config**, per machine; the console reads it through a CLI read; doctor recomputes drift | Recompute only (no file) or in the cloud policy node | 2026-11-07: recommended |
| **D1563** | A follow-up fails after the change is saved | **Keep the change, mark pending, exit non-zero, say "saved, not live yet" and name `fugaro apply`** | Roll back: loses the user's input, needs per-setting undo | 2026-11-07: recommended; tension recorded in 4 |
| **D1564** | A repo-layer change (a checkout's `fugaro.yaml`) | **The command writes the file and says plainly it is live after the change is merged; it commits and opens nothing.** Nothing is pending (no Fugaro step can make it live) | Command opens a PR: touches an outside resource (no-prompt rule 5) | *(empty: not yet asked)* |
| **D1565** | The skip flag's name | **`--no-apply`** only (`--only` is unused but reads as "apply only this"). One shared flag helper, so every command spells it identically | `--only` as an alias: two spellings | *(empty: not yet asked)* |
| **D1566** | Which commands are management commands | **In:** `settings set/unset`, `image add/rm`, `accounts add/rm`, `project repos add/rm/sync`, `project policy publish` (is itself a step). **Out (no follow-up exists, they write the cloud now):** `budget set`, `secrets/creds set`, `recipes publish`, `allow-delete` | Include them with an empty step list: same outcome, more wiring | *(empty: not yet asked)* |
| **D1567** | `update` and pending | **`update` runs every step and clears the pending file;** `apply` runs only what is pending. Same step code | `apply` as an alias of `update`: runs billable steps with nothing owed | *(empty: not yet asked)* |
| **D1568** | `fugaro apply` with nothing pending | **Exit 0 with "Nothing to apply."** and, in `--json`, an empty list | Exit non-zero: breaks scripts | *(empty: not yet asked)* |
| **D1569** | Console | **One read, `fugaro apply --status --json`, on the fence (row, `spawn.ts`, Go equality test); console write chains are the console plan's** | Console runs the writes now: a new IPC/allow-list surface | *(empty: not yet asked)* |
| **D1570** | The pending file's shape | **Versioned JSON, `schemas/pending-steps.schema.json`, additive only; carries step names and the change that asked for it, never a value** | Free-form | *(empty: not yet asked)* |
| **D1571** | Agent sessions and unmanaged projects | **No rule of their own: a chain's steps run under the no-prompt plan's rules (D1541, D1542, D1545).** On a project those refuse, the command saves and marks pending | Own rules here: two rule sets | *(empty: not yet asked)* |
| D1572-D1579 | Reserved | | | |

## 4. Open tensions and open items for the owner

1. **D1563 against the spec's rule 4/Enforcement.** "Saved, not live yet; run `fugaro apply`" tells the
   user to run a command by hand. It is the single sanctioned exception (it names neither publish nor
   update). Confirm, or choose rollback.
2. **D1560 amended**: confirm the corrected text (no rename; bare `apply` is new).
3. **D1564-D1571 were not put to the owner** (recommendations stand, marked "not yet asked").
4. **Sequencing.** This plan's chains depend on the no-prompt plan (`docs/no-prompt-policy`): an
   audit-only gate and no confirmations. Until it lands, a chain's steps refuse in a coding agent's
   session exactly as `update` does; the plan's tasks say so.

## UNVERIFIED

- Which console screens call a write verb today (the fence lists reads only; write allow-list not read).
- Whether `recipes publish` has a skew follow-up (`recipes_skew.go` prints a text; not read in full).
- The exact order constraints between image-build and deploy inside `update` (read at task time).
