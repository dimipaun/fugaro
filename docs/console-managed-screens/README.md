# The console's managed-project screens — read-only screens for what the CLI does on a managed project

**Status: plan only; nothing is implemented; the decisions D1470–D1489 are answered 2026-11-07 and
all tasks are approved 2026-11-07 (owner: "All tasks approved. Jobmax").** This is a design-only
folder: it adds no code, no schema, no console source, no test fixture and no workflow, and it edits
no file outside `docs/console-managed-screens/`. Every command, flag, field, operation and file
below is a proposal. **No decision here is answered** ([reconciliation.md](reconciliation.md) §7
carries D1470–D1489, each with an empty owner's-answer cell), and **no task in
[implementation-plan.md](implementation-plan.md) may start before the owner approves it** — an
answer is not an approval.

**Asked by:** the owner, 2026-11-07, in the release planning for **console 0.121.0 (Raspberry)**: put
"managed-project screens" in that release — **read-only** screens showing what the CLI does on a
managed project. The five things he named are the managed state, the review account keys, the policy
notes, the retirements the last plain `fugaro update` made, and the credentials service state once
DON7 lands. His words are [spec.md](spec.md), verbatim, and they are the only text here he wrote.

**Spec owner:** the owner, 2026-11-07. [spec.md](spec.md) is his ask, verbatim; where
[reconciliation.md](reconciliation.md) disagrees with it about the code, **reconciliation wins**.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's ask, verbatim, with a header naming him by role |
| [reconciliation.md](reconciliation.md) | Findings **F1–F27** against the console and the CLI on this tree, read by path and symbol; the security analysis as an attacker would see it (§5); what the screens may not do (§6); the decisions **D1470–D1489**, each a table row with a recommendation, an alternative, its cost and an **empty owner's-answer cell** (§7); the number block and how it was read (§8); **UNVERIFIED** (§9) |
| [implementation-plan.md](implementation-plan.md) | The CLI tasks **CM1–CM2**, the fence tasks **CM3–CM4** and the console tasks **CM5–CM13**, each with its files (at most six), its interfaces, its named tests and command, its **red step**, its **mutation**, its **reviewer tier**, its dependencies and its commit; the levels, the critical path and the shared-files table proving no level has two writers of one file (§1–§2), the tasks in detail (§3), what is deliberately not in the plan (§4), the release slot (§5), the owner-run live check **Check 360** (§6) and the gates a task may not start without (§7). **Nothing in it is approved** |

## What it is

The console draws **five tabs** — Live, Graph, Spend, Project, Coordinator (`VIEWS`,
`console/src/renderer/entry.ts:2461`) — and the Project tab is the project's own screen: **nine panels
from three reads**, seven in `PROJECT_PANEL_KEYS` (`console/src/renderer/screens/projects.ts:166`) plus
the models panel and the settings panel, the second and third read's. Its sources are five command
lines in `PROJECTS_SOURCE_LINES` (`projects.ts:108`): `budget show --json`, `accounts ls --json`,
`secrets ls --json`, `models ls --json` and `settings --json`.

The owner's ask is about a different set of facts — the ones a **managed project** has and an ordinary
one does not: whether the project is managed and who admitted what, which accounts a run's stages use,
what the published policy node holds and what the last update changed, what the last plain `fugaro
update` retired, and (once DON7 lands) what the credentials service is doing. **None of them is on
that screen, and four of the five are not reachable from the console at all** — only the review
account keys are, through the `settings --json` read the tab already makes.

Three things this design settles that the ask does not:

1. **The screens are read-only, and that is a fence rather than a feature list.** The console's whole
   surface is `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts:529`) and its spawn allow-list is **exact
   argv** (`argvFor`, `console/src/main/spawn.ts:1334`; rule 6's table, `docs/design/desktop-console.md`
   §5.1). Two of its operations write — `run.launch` (rule 11) and `settings.edit` (rule 12) — and
   **this work adds neither a third write nor a button that edits** (D1471). Nothing here spawns
   `managed apply`, `managed flag`, `managed unflag`, `settings set`, `project policy publish` or
   `update`: **the console never writes the managed marks.**
2. **Two of the five screens need a CLI field that does not exist yet, and a third needs a read the
   console is not allowed to spawn.** The managed marks and the audit records are read by `doctor`
   today and printed as **prose** in a check's `problem`/`fix`
   (`internal/cli/doctor_managed_audit.go`), never as fields; nothing records the retirement set a
   plain `fugaro update` made, the runs in flight when it ran, or the diff the last policy publish
   carried. Two CLI tasks (**CM1**, **CM2**) add them, as **additions** — a new field is fine, a field
   never changes meaning, and the console accepts any newer CLI. The policy notes need no new CLI
   field at all: the node is printable today and the console has **no row that reaches it**, which is
   a fence to move rather than a field to add (F4, D1480).
3. **One screen is not this work's to build.** The credentials service state is **DON7**
   (`docs/credentials-service/default-on.md`, open pull request **#1816**); the screen draws doctor's
   own line rather than re-deriving the state (D1486), and it waits.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1470–D1489** | **this work's block, all twenty written and NONE answered.** Taken above every number on `main` and in every pull-request head this checkout could fetch. On `main` (`origin/main` at `56c91f66`) the highest number written or cited is **D1409** (`docs/catalog-build-context/reconciliation.md`, the tail of that work's D1390–D1409); across the pull-request heads the highest is **D1429**. **Verified free on 2026-11-07**: no `D147x` or `D148x` appears anywhere on `main` or in any of the **1,796** fetched `refs/pull/*/head` refs (§8 carries the commands) |
| **D1490–D1499** | **reserved and unused**, so no second work can take a number adjacent to this one |
| **Check 360** | this work's live check (implementation-plan §6). The highest `## Check` **section written** on `main` is **290** (`docs/gcp-live-checklist.md`); the highest number **cited** on `main` is **314**; the highest claimed in a pull-request head is **330** (`docs/api-key-review`, pull request **#1819**). 360 is a fresh block, well above all three, and 361–369 are available if a task needs a second check. Like every claim above the highest written section, 360 is **claimed, not reserved**: the register's rule is that a claim is settled at its merge, and whichever of two claims merges second renumbers itself |

## What this design does not do

- It changes no code, no schema, no config, no console source, no fixture and no workflow. It writes
  nothing under `console/`, `internal/`, `schemas/`, `plugin/`, `mobile/`, `shared/` or `ee/`.
- It **adds no pinning test**, and it does not move the census row in `docs/rigor-modes/reconciliation.md`
  that counts the `internal/cli/docs_*_test.go` files (**100**, unchanged): there is no registry test
  over `docs/` in `internal/cli`, so a folder may be documents alone, and `docs/image-env/` is the
  precedent this folder follows. The plan's tasks name their own pins.
- It implements neither **DON7** (pull request **#1816**) nor any part of the credentials-service
  work; it depends on DON7 and says so (D1486, plan CM9).
- It adds no dependency and proposes none.
- It **invents no role**: "administrator" is not a role this design creates. Who may change a setting
  is the CLI's own permission outcome, and these screens are read-only for everyone.
- It does not build the **settings pencil** or the launch: those are rule 11 and rule 12, and this
  work adds no third write. Where a screen shows a value the pencil could edit
  (`default_claude_account`), the screen shows it and offers no control (D1471, D1488).

## Related

- [docs/design/desktop-console.md](../design/desktop-console.md) §5.1 — the hard rules: rule 5 (the
  IPC allow-list), rule 6 (the exact-argv spawn allow-list and its never table), rule 7 (job-written
  text is text), rule 11 (the launch, the first write) and rule 12 (the settings edit, the second).
- [console/docs/json-contract.md](../../console/docs/json-contract.md) §6 (the other documents), §9
  (the read-only rule and its two exceptions) and §10 (what `watch --json` guarantees).
- [.claude/rules/console.md](../../.claude/rules/console.md) — the owner's rule of 2026-10-31: a screen
  renders each part when its data arrives, independent reads run in parallel, and no screen waits for
  all of them.
- [docs/settings-commands/](../settings-commands/README.md) and
  [docs/console-settings-edit/](../console-settings-edit/README.md) — the settings read and the pencil
  this work reuses and does not extend.
- [docs/credentials-service/default-on.md](../credentials-service/default-on.md) §DON7 — the one line
  the credentials-service screen draws, and the pull request (**#1816**) it waits on.
- [docs/managed-agent-converge/](../managed-agent-converge/) and
  [docs/design/managed-projects.md](../design/managed-projects.md) — the managed project itself: its
  two marks, its gate and its audit trail.
