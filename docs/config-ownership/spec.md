# Config management: one write path, semantic commands where the field has meaning

Owner: Dimi (repository owner). Date: 2026-11-15.

## Why

An audit of every configuration field in the three layers (repo `fugaro.yaml`, the published project layer and policy node, the local config) found 478 rows. 190 are writable by `fugaro settings`, 128 are manual-edit only, and 109 are written by a command that `settings` refuses. 114 registry rows name an owner command that does not write the key. The full table and the ten gaps are in the audit file `config-audit/field-management.md` (kept with this spec as an attachment, see below). The owner's question: should settings be supported through the generic `settings` subcommand, or through semantically meaningful management commands such as `accounts pool add`?

## The owner's direction

Both, with a rule for which owns a field, and with one write path underneath.

1. **Semantic command** for a field that has invariants, secrets, a publish step, multi-part list entries or a side effect. The owner chose these concepts: pools and crafts; credentials and the cascade (`credentials.*` including `fallback`, `binaries.*`, node `model_grades`); rigor and security paths; models and routes (`models use`, `providers.*`, `agent.levels.*`, `agent.crafts.*`, `model_grades.*`).
2. **Generic `fugaro settings`** for scalar leaves whose meaning stands alone (for example `governance.stage_timeout.<stage>`, budget caps, flags). `settings list` and `settings get` show every field wherever it lives and whoever owns the write.
3. **One writer per field.** Semantic commands are front doors onto the same write path `settings` uses, never a second implementation.
4. **Every registry row names its owner, and a test pins that the owner really writes the key.**
5. **`settings set` on a field owned by a semantic command refuses and names the owner** (the owner chose refusing over delegating).
6. **A refusal names the working spelling.**
7. **Layer targeting is uniform**: the same `--layer repo|project|local` on every writer, with the merge rule decided per field. A repo may only tighten a ceiling, so for those the project layer is the default write target.
8. **No field is manual-only without a stated reason.** Installation plumbing under `setup` and fake endpoints get an explicit label.
9. **Local to node publish: both.** Each semantic command writes the node itself, and `project policy publish` and `fugaro update` also reconcile the node, so no stale copy survives either way.

## Already ruled (2026-11-15)

`governance.stage_timeout` is for real use. The project layer is the 99% path and must reach runs; a repository's own value may only tighten the project's, never loosen. Today `settings set governance.stage_timeout.implement` answers "no setting has that name".

## Known gaps to close (from the audit)

1. `governance.stage_timeout.<stage>` (also `stage_budget`, `stage_tokens`, `tiers.*`) settable at project level without the `layer.` prefix; fix or remove the four broken repo-level block rows.
2. Publish `crafts`, `craft_credentials`, `craft_pools` to the policy node (today `accounts pool add|rm` edits only the local config).
3. A writer for the node-only blocks `credentials.*`, `binaries.*`, node `model_grades` (today a hand edit of the database).
4. The `--layer project` spelling for 17 `agent.*` and `git.*` rows writes a form refused at re-read; the working spelling is `layer.defaults.agent.*`.
5. Repo `rigor.default` and `rigor.areas`: `settings` points at `fugaro rigor set`, which writes only the layer file and has no `--repo` flag.
6. Model and route fields with no writer.
7. Account fields with no writer (`pools.*.owner`, `allowed_users`, `legacy_open`, `crafts.*.access`, `model_grades.*.access`, adding to `repos.*.claude_accounts`).
8. Real fields with no registry row or writer (`ab.rate`, `workflows.*.needs`, `layer.security.*`, `layer.jobs.*.paused`, `planning.decoupling` at repo level).
9. Local keys consumed but only hand-editable, with owner labels naming the wrong command.
10. Ceiling keys default to the repo file; the owner-level value needs `--layer local` or `--layer project`.

## Doc contradictions to fix

`docs/settings-commands/audit.md` is stale; `settings_set.go` treats `credentials.*` as generic-path keys but the registry has no row; `project_cmd.go` still calls the local-to-node publish pending; `plugin/skills/working/SKILL.md` omits the `layer.` spelling; `docs/project-layer.md` describes the broken `--layer project` form.

## First task

The first plan task is the registry-versus-writer conformance test, because it makes every later gap measurable.

## Not determined by the audit

Whether the released runner image is built with the ee tag (decides whether governance reaches runs at all); whether `settings set layer.defaults.agent --add-entry model=X` writes the model keys; per-leaf writers for `batch.*`, `build.*`, `compute_prices.*`; whether `endpoints.*` is ever written by `setup`.
