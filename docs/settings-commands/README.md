# `fugaro settings`: one command to see and change every setting, with `fugaro update` publishing what it changes

**Status: design approved; tasks queue for CLI 0.15.0 (and console 0.117.0).** SD1–SD18 were
answered by the owner on 2026-10-31 (SD1–SD5 with named answers, the rest as recommended); the
answers are in [reconciliation.md](reconciliation.md) §3, and the plan is written to them. The
design's own decisions — everything the audit left open that is not one of its SDs — are
**D840–D854**, in [reconciliation.md](reconciliation.md) §4: **every one of the fifteen was answered
by the owner on 2026-10-31 as recommended**, and **D854 — the stale local copy that could overwrite a
newer publish — is answered with them: the generation is recorded**, so a stage refuses to publish
over a published generation that has moved. **D855–D859** are reserved and unused. Nothing is built
yet.

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
   `KEY SCOPE VALUE WINS DEFAULT STATE`, and with `--why`, `--changed`, `--repo`, `--all-repos`,
   `--layer`, `--grep`, `--offline` and a versioned `--json`.
2. **A key and defaults registry**, one machine-readable row per setting, which is where the
   DEFAULT column, `--changed`, the JSON Schema `default` keywords and the scope table all come
   from — instead of the four scattered copies that exist today.
3. **`fugaro settings get|set|unset`**, the generic write path for every scalar and string-list key,
   editing the layer's own file through the one shared comment-preserving editor, `internal/yamledit`,
   with the diff, the confirmation and the agent-session refusal the `models` and `rigor` designs
   already fixed.
4. **Dedicated families** where a setting needs more than a value: `rigor floor`, `accounts`,
   `secrets allow`, `project profile`, `project routing`, `project credentials`,
   `project clone-url`.
5. **The publish foundation**: `fugaro update` gains the stage that publishes the policy node from
   the local config, the stage that publishes the project layer (the same stage the `rigor` design's
   R1 builds — one stage, not two), the re-converge of provider drift (D787), and the migration that
   imports the node's `credentials.*` and `clone_urls` into new local-config blocks so the console is
   never the only writer again. **D854 adds a third guard to them** — the stage remembers the
   generation this machine's file was written from, so a stale copy cannot replace a newer publish,
   which is the owner's answer of 2026-10-31.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The read command and its columns, the write path, the seven dedicated families, the publish foundation, the registry, the shared mechanics the `models` and `rigor` designs already own, the migration of a hand-edited installation, and the console contract section a Settings view will need |
| [reconciliation.md](reconciliation.md) | Findings F1–F20 against the code on `main`, the owner's answers to SD1–SD18, and this design's own decisions D840–D854, every one with the owner's answer of 2026-10-31. **Where it disagrees with [spec.md](spec.md), it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks S0–S11 (at most 12 files each), the external dependencies E1–E3, and the order; each task names its tests, starts with a red step and carries a SEC flag where it writes policy, the layer or a credential block. **The first slice is S0–S3**; S11 is the end-to-end converge test |

## The joint order, and the rulings the three designs share

`fugaro settings`, `fugaro models` and `fugaro rigor` write the same two documents, publish them
through the same two `fugaro update` stages, and splice them with the same editor, so the three
designs carry the same lines. [spec.md](spec.md) §7 holds the four shared mechanisms in full;
[implementation-plan.md](implementation-plan.md) holds the tasks. This section is the summary the
three READMEs share.

**The joint order**, agreed with the `models` and `rigor` designs and the same list in all three:
**editor → policy stage (S3) → settings read (S0–S2, S4) → layer stage (R0, R1) → set (S5) → rigor
(R2–R5) and models.**

| # | What lands | Owner | Why it sits here |
| --- | --- | --- | --- |
| 1 | The one editor, `internal/yamledit` | the editor task (E3) | every later task splices a file with it, so landing it first is what keeps anyone from writing a second one meanwhile |
| 2 | `update` publishes the policy node | **S3**, the `models` design's E1 | it is the first slice's second half, it is what lets the read stop saying `unpublished` for the policy, and S6–S9 write keys only this stage publishes |
| 3 | The settings read | **S0–S2, S4** | useful before every writer, and it is what makes G1, G2 and G5 visible today |
| 4 | The layer stage, and the local layer file it publishes | the `rigor` design's **R0, R1** | `settings set --layer project` (S5) needs both the file and its publisher |
| 5 | `fugaro settings get\|set\|unset` | **S5** | the first writer over all three documents; it comes after 2 and 4 because its `next: fugaro update` line is false until both stages publish |
| 6 | `fugaro rigor …` (R2–R5) and the `models` writes | the `rigor` and `models` designs | they reuse the editor and both stages, and their keys reach runs through the stage of 2 |

**What all three designs rule the same way:**

- **One editor, `internal/yamledit`.** Generic locate-and-splice operations, one owning task, and a
  cross-document test that all three documents preserve comments, key order and untouched bytes the
  same way (D847).
- **One policy stage and one layer stage.** `fugaro update` publishes both documents behind its own
  confirmation; whichever design lands each stage first, the others delete their copy. No design
  adds a second publish command (SD3, SD4).
- **One `unpublished` word.** The state a local file that differs from its published object is in is
  spelled `unpublished` by `settings`, `rigor show`, `config layer` and `doctor` alike.
- **One sentence for the repository with no checkout**: `unknown (no checkout on this machine)`
  (the `rigor` design's D839), everywhere it is printed.
- **One write path.** The diff, the confirmation, the backup and the atomic write are the same five
  helpers everywhere; the sensitive directions ask for the typed project name, `--yes` never covers
  it, and in a coding agent's session a write outside a repository prints the command and writes
  nothing (SD9, D846).

## How this was reconciled

Against `origin/main` at `29004ec9` and the 18 pull requests open on 2026-10-31, read with `grep`,
offset reads and `git show`. The audit's own citations (`file:line` at `1c9be2a2c`) were re-checked
against that commit; every claim in [reconciliation.md](reconciliation.md) names the file and the
symbol it was read from. `docs/rigor-command/` (PR #1463) was read in full through `gh pr diff`,
because its R0–R6 and this plan's S4–S7 write the same two documents, the same two `fugaro update`
stages and the same editor.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D840–D859** | this work's block: **D840–D854** written and **all answered by the owner on 2026-10-31, every one as recommended**; **D854 — the stale local copy — is answered by recording the generation and refusing to publish over one that has moved, with the three-way state and a re-seed under confirmation**; **D855–D859** reserved and unused. Verified free as follows: on `main` the highest number anywhere in `docs/` is **D829** (`docs/console-coordinator-v2/reconciliation.md`, the tail of that work's D810–D829), and the highest in any open pull request's docs is **D839** (`docs/rigor-command/README.md`, PR #1463, which renumbered its block to **D830–D839**). The models block (D780–D799) and the console auto-update plan's block (D760–D772, #1377) are below both |
| **The collision this design waited on, resolved** | **PR #1463 (`docs/rigor-command`) had claimed D810–D829, which `main`'s `docs/console-coordinator-v2` already holds.** PR #1463 has since renumbered itself to **D830–D839**, so this design takes **D840**, the next free number above every block written on `main` and in an open pull request. Nothing here cites the rigor design by its old numbers; where it does cite it — the local layer file that `rigor set` writes (**D830**) and the `Layer` stage in `fugaro update` that publishes it with seeding (**D831**) — it cites the renumbered decisions. What the earlier reading recorded, and what it cost, is in [reconciliation.md](reconciliation.md) §5 |
| **Checks** | **none taken.** Everything this design adds is a local file edit, an offline read, or a stage of `fugaro update` whose cloud writes go through the publish paths the installation's existing update checks already exercise. The one verification that would need a check number — the first `update` importing a node-only block on a real installation — is the same path `fugaro project policy publish` already runs in the live checklist, and no new operator step is added |

## What this design does not do

It changes no code, no schema, no allow-list row and no existing document. It writes nothing under
`console/`, `mobile/`, `shared/`, `ee/`, `plugin/` or `website/`; it names the sections those works
would add, and nothing more. It proposes no new dependency. The design is approved and the tasks
queue for the release named above, and [implementation-plan.md](implementation-plan.md) is written
so that each task can be dropped on its own — a task that turns out not to be wanted is deleted
rather than amended, which is why every one of them names its own files and its own tests.
