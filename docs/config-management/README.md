# Managing every configuration in Fugaro: no configuration file edited by hand

**Status: a design, and nothing is built.** The owner gave the ruling this folder answers on
**2026-11-03** — *"The idea would be not to touch any of the configuration files by hand, be it repo,
project, or local level, meaning that we should have management commands in Fugaro just like with
settings."* — and he accepted the audit it rests on, asking for a design with a task plan for
**0.18.0**, **built behind managed mode** ([the managed agent converge](../managed-agent-converge/README.md),
D1011–D1026). This is a **design-only** pull request: it writes four documents, no code, no schema, no
Terraform, no `go.mod`. **No decision here is answered yet** — each carries a recommendation and the
cost of every alternative, and the plan's tasks start only when the owner approves them.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The design in full: what "managed" means for a configuration, the generic settings writer's extension (concrete entries for wildcard keys, `--add-entry` / `--remove-entry`), which dedicated commands remain and their rules, the registry's owner column, the project layer and a repository's `workflows.*` as managed entries, what stays a setup/update-owned installation fact, and the managed gate's rows. **Where it disagrees with [reconciliation.md](reconciliation.md), the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `main`: findings **F1–F14**, what exists and what is missing, the decisions **D1160–D1183** (§4) with the recommendation and the cost of each alternative, the number block and how it was read (§3), and the residual this design refuses to hide (§5) |
| [implementation-plan.md](implementation-plan.md) | Fourteen tasks **G0–G13**, each with its files (at most ten), its interface, its **red step**, its named tests and its **SEC** flag; the shared-files table (§4) and the order (§2), which puts the generic writer first; and the recommended slot (§7), **0.18.0**, beside the managed-mode build **M0–M9** |
| `README.md` | This file: the status, the reading of the number block, what is being asked, and what a reader should not take from here |

## The owner's ruling (2026-11-03)

> **Owner, 2026-11-03:** *"The idea would be not to touch any of the configuration files by hand, be it
> repo, project, or local level, meaning that we should have management commands in Fugaro just like
> with settings."*

He accepted the audit recorded in [reconciliation.md](reconciliation.md) §1 and asked for a design with
a task plan for **0.18.0**, **built behind managed mode**: the managed gate's command set
([spec.md](../managed-agent-converge/spec.md) §3.1, D1011) needs a **row** for every new command, and
**D1026** — a `Typed` step is never admitted by a plan gate — is inherited unchanged.

## What is being asked

Today **three** documents a person owns still need a hand edit, and every one of them is a document
that decides where a repository's code may go or what a run may cost:

- a repository's **`fugaro.yaml`** — its `workflows.*` block (37 registry keys) has **no writer at
  all**: `fugaro config init` writes a minimal file, and the setup skill writes the rest with the user
  in the user's own agent session (`plugin/skills/setup/SKILL.md:13`);
- the **project layer file** (`<config home>/fugaro/projects/<project>.layer.yaml`,
  `internal/localcfg/localcfg.go:1120`) — its `profiles.*` (36), `defaults.*` and
  `ab.*` entries are hand-edited and then published by a **second** command, `fugaro config publish`,
  which the HARD RULE in `CLAUDE.md` forbids;
- the **local project config** — `fugaro rigor set` and `fugaro rigor floor`, which the registry names
  as the owners of seven keys (`internal/settings/registry.go:266`, `:273`, `:682`),
  **do not exist**; `secrets` has no `rm` and no policy flags; `accounts` has no `rm`.

`fugaro settings set` already writes scalars and string lists
(`internal/cli/settings_set.go:81`): the allowlist carries **164 of the registry's 381 rows** (88 by name
and 76 through its prefixes), and the remaining **217 rows have no generic writer**
([reconciliation.md](reconciliation.md) §1, F1). The root cause is one sentence: **the
generic writer handles scalars and string lists only**, and every gap above is a map, an object list, or
a key the allowlist does not carry.

**The gateway enforces the cost cap on this run.** Every figure Claude Code prints about this session's
own spend is ignored: it misprices non-Anthropic models, here by roughly a factor of twenty-five, and
the gateway is the only authority. The run stops for one reason only — the gateway refuses a call.

## The number block

The decision block is taken **above every `D<number>` on `main` and in every open work stream**, read
on **2026-11-03** by fetching each **branch** and reading it, not by trusting a neighbour's table.

- **On `main`**, the highest *written* decision is **D1159** (`docs/project-image-catalog/reconciliation.md`,
  that work's block D1140–D1159).
- **In the open work streams**, re-checked in this run by fetching each branch and reading it — the
  branch is the evidence. The highest number any of them writes is again **D1159**, from the same
  project image catalog folder that `main` already carries; the open docs designs among them
  (`docs/workspaces` #1492, `docs/binary-allow-rule` #1491, `docs/periodic-reviews` #1490,
  `docs/mcp-server` #1487, `docs/plugin-user-scope` #1488, `docs/run-without-checkout` #1481,
  `docs/run-limit-carry-over` #1485) all sit below D1027, and the managed-mode **code** branches
  (#1650, #1651) write no new decision. **D1160 is therefore free.**

**This work takes D1160–D1189: D1160–D1183 are written here, and D1184–D1189 are reserved and unused.**
Every decision in the block is **OPEN, with a recommendation** — none is answered, and the owner
answers them or the design is not approved. A later task that needs a number re-checks this section's
facts rather than its conclusion.

## What this design does not do

- **It does not build anything.** No task in [implementation-plan.md](implementation-plan.md) is
  approved, and no file outside `docs/` and the one Go test that pins this folder is touched.
- **It does not add a compatibility or skew shim.** There are no production users (the owner's policy of
  2026-11-03): where a shape must change it changes directly, and where a name is reused it is
  re-meant only by giving the new meaning a new name.
- **It changes no field's meaning.** For every wire and JSON shape a reader outside this binary
  consumes — the settings read, the settings-write document, the policy node, the run record and the
  config formats — members are **added only**; the settings-write document **keeps the version it has**
  and gains its entry members as optional ones, so the console's settings edit keeps working unchanged
  ([spec.md](spec.md) §2.4, D1164).
- **It does not touch the managed gate's decisions.** D1011–D1026 are cited and extended with a row per
  new command; no decision there is re-opened, and no file of
  [docs/managed-agent-converge/](../managed-agent-converge/README.md) is edited by any task here.
- **It removes the agent-session refusal nowhere.** Every command here is refused in a coding agent's
  session exactly as today, except where the managed gate of D1011 admits it in a managed project.
- **It does not make `fugaro setup` agent-runnable**, and it does not move an installation fact
  (endpoints, terraform, batch, build, `gcp_project`, region, `runs_bucket`, registry, generations)
  out of setup/update's hands ([spec.md](spec.md) §6, D1182).

## Related

- [docs/settings-commands/](../settings-commands/README.md) — the design of `fugaro settings`, whose
  **S5** lands the write path every edit here extends, whose **§4.4** is the allowlist this design
  widens, and whose **D846** reserves the typed project name for the widening directions.
- [docs/rigor-command/](../rigor-command/README.md) — the design of `fugaro rigor set` and its area
  rules, which this design **builds** rather than re-designs (D1169).
- [docs/managed-agent-converge/](../managed-agent-converge/README.md) — the gate this design adds rows
  to, and the D1026 rule every row inherits.
- [docs/project-layer.md](../project-layer.md) — the project layer file whose entries this design
  manages, and the `fugaro config publish` step it folds into one command.
