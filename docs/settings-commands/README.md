# `fugaro settings`: one command to see and change every setting, with `fugaro update` publishing what it changes

**Status: design approved. SD1–SD18 were answered by the owner on 2026-10-31 (SD1–SD5 with named answers, the rest as recommended); the tasks queue for CLI 0.15.0.** The answers are in
[reconciliation.md](reconciliation.md) §3, and the plan is written to them. The design's own
decisions — everything the audit left open that is not one of its SDs — are **D840–D853**, in
[reconciliation.md](reconciliation.md) §4, all recommended; **D854–D859** are reserved and unused.
Nothing is built yet.

**Asked by:** the owner, 2026-10-31: "for project level and repo level settings, in fact for ALL
the settings, you should not expect people to edit the text file directly, but have Fugaro commands
that manage those settings: reports it, updates it, etc." Two later additions the same day: one
command that lists every setting with its name, the level that wins and the value; and that every
row also shows the default value.

**The audit this design answers:** [audit.md](audit.md), the independent inventory of how every
setting is shown, set and published today, with its gap matrix G1–G12 and its §6 decision list
SD1–SD18. This folder is the design the owner asked for; the audit is unchanged and stays as the
evidence.

## What it is

Every owner-level setting except the IAM lists and the budget caps is a hand edit today, and the
two documents that carry them are published by commands `fugaro update` never runs: the policy node
by `fugaro project policy publish`, the project layer by `fugaro config publish FILE`. So an edit
followed by `fugaro update` changes nothing and warns about nothing — the "published change that
silently does nothing" class the HARD RULE in `CLAUDE.md` exists to forbid. Two blocks
(`credentials.*`, `clone_urls`) have no writer at all outside the Firebase console, and one of them
decides whether a run spends the paid credential tiers. And nothing lists the settings: no command
answers "what is set, at which level, and what would a run actually get".

This work adds:

1. **`fugaro settings`**, a read over every layer — default, repository `fugaro.yaml`, project
   layer, profile, local project config, shared config, published policy node, Realtime Database
   caps and kills, environment variables — as one row per (key, scope) with the columns
   `KEY VALUE WINS DEFAULT STATE`, and with `--why`, `--changed`, `--repo`, `--all-repos`,
   `--layer`, `--grep`, `--offline` and a versioned `--json`.
2. **A key and defaults registry**, one machine-readable row per setting, which is where the
   DEFAULT column, `--changed`, the JSON Schema `default` keywords and the scope table all come
   from — instead of the four scattered copies that exist today.
3. **`fugaro settings get|set|unset`**, the generic write path for every scalar and string-list key,
   editing the layer's own file through the shared comment-preserving editor, with the diff, the
   confirmation and the agent-session refusal the `models` and `rigor` designs already fixed.
4. **Dedicated families** where a setting needs more than a value: `rigor floor`, `accounts`,
   `secrets allow`, `project profile`, `project routing`, `project credentials`,
   `project clone-url`.
5. **The publish foundation**: `fugaro update` gains the stage that publishes the policy node from
   the local config, the stage that publishes the project layer (the same stage the `rigor` design's
   R1 builds — one stage, not two), the re-converge of provider drift (D787), and the migration that
   imports the node's `credentials.*` and `clone_urls` into new local-config blocks so the console
   is never the only writer again.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The read command and its columns, the write path, the seven dedicated families, the publish foundation, the registry, the shared mechanics the `models` and `rigor` designs already own, the migration of a hand-edited installation, and the console contract section a Settings view will need |
| [reconciliation.md](reconciliation.md) | Findings F1–F20 against the code on `main`, the owner's answers to SD1–SD18, and this design's own decisions D840–D853 (each with a recommendation and the cost of the alternative). **Where it disagrees with [spec.md](spec.md), it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks S0–S10 (at most 12 files each), the external dependencies E1–E3, and the order; each task names its tests, starts with a red step and carries a SEC flag where it writes policy, the layer or a credential block. **The first slice is S0–S3** |

## How this was reconciled

Against `origin/main` at `29004ec9` and the 18 pull requests open on 2026-10-31, read with `grep`,
offset reads and `git show`. The audit's own citations (`file:line` at `1c9be2a2c`) were re-checked
against that commit; every claim in [reconciliation.md](reconciliation.md) names the file and the
symbol it was read from. `docs/rigor-command/` (PR #1463) was read in full through `gh pr diff`,
because its R0–R6 and this plan's S4–S7 write the same two documents and the same two `fugaro
update` stages.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D840–D859** | this work's block: **D840–D853** written, recommended; **D854–D859** reserved and unused. Verified free as follows: on `main` the highest number anywhere in `docs/` is **D829** (`docs/console-coordinator-v2/reconciliation.md`, the tail of that work's D810–D829), and the highest in any open pull request's docs is **D839** (`docs/rigor-command/README.md`, PR #1463, which renumbered its block to **D830–D839**). The models block (D780–D799) and the console auto-update plan's block (D760–D772, #1377) are below both |
| **The collision this design waited on, resolved** | **PR #1463 (`docs/rigor-command`) had claimed D810–D829, which `main`'s `docs/console-coordinator-v2` already holds.** PR #1463 has since renumbered itself to **D830–D839**, so this design takes **D840**, the next free number above every block written on `main` and in an open pull request. Nothing here cites the rigor design by its old numbers; where it does cite it — the local layer file that `rigor set` writes (**D830**) and the `Layer` stage in `fugaro update` that publishes it with seeding (**D831**) — it cites the renumbered decisions. What the earlier reading recorded, and what it cost, is in [reconciliation.md](reconciliation.md) §5 |
| **Checks** | **none taken.** Everything this design adds is a local file edit, an offline read, or a stage of `fugaro update` whose cloud writes go through the publish paths the installation's existing update checks already exercise. The one verification that would need a check number — the first `update` importing a node-only block on a real installation — is the same path `fugaro project policy publish` already runs in the live checklist, and no new operator step is added |

## What this design does not do

It changes no code, no schema, no allow-list row and no existing document. It writes nothing under
`console/`, `mobile/`, `shared/`, `ee/`, `plugin/` or `website/`; it names the sections those works
would add, and nothing more. It proposes no new dependency. If the owner rejects the read command,
the registry or the families, the files here are deleted rather than amended: they are a proposal,
and [implementation-plan.md](implementation-plan.md) is written so that each task can be dropped on
its own.
