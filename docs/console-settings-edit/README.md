# Editing the Project tab's settings from the console, behind a pencil

**Status: plan only; nothing is implemented; no task approved.** This is a design-only folder: it adds
no code, no schema, no console source, no test fixture and no workflow, and it edits no file outside
`docs/` and the one Go test that pins this folder. Every command, flag, field and argv below is a
proposal. The owner answered its decisions on 2026-11-01 ([spec.md](spec.md)): **D1100–D1121, each as
recommended — except D1107, which stays `OPEN`**. An answer is not an approval: no task in
[implementation-plan.md](implementation-plan.md) may start before the owner approves it.

**Asked by:** the owner, 2026-11-01: "In the console, under the Project tab, for each of those values,
a way for administrators to edit them from within the console: a little pencil icon to trigger an
edit." The rulings he gave with the ask: **yes to the feature**, and **all edits, including
widenings, from day one** — not narrowing-first.

**Spec owner:** the owner, 2026-11-01. [spec.md](spec.md) is his ask, verbatim, followed by the
rulings he gave on the design that came back from it; it is the only document here he wrote, and the
answers to **D1100–D1121** are recorded in it. Where [reconciliation.md](reconciliation.md) disagrees
with it, **reconciliation wins**.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's ask, verbatim, with the two rulings he gave with it |
| [reconciliation.md](reconciliation.md) | Findings **F1–F16** against the console and the CLI on this tree and against **S5** (`fugaro settings get\|set\|unset`, pull request **#1525**, merged), and this design's own decisions **D1100–D1121**, each with its options, a recommendation and its reason, and its answer field. **Where it disagrees with [spec.md](spec.md), it wins** |
| [implementation-plan.md](implementation-plan.md) | The prerequisite CLI tasks **P1–P4** and the console tasks **C1–C6**, their sizes, files, security flags, verification, the parallel levels, the critical path and the release slot. **Nothing in it is approved** |

## What it is

The Project tab draws five panels today, from three commands: `budget show --json`,
`accounts ls --json` and `secrets ls --json` (`PROJECTS_SOURCE_LINES`,
`console/src/renderer/screens/projects.ts:92`). What it shows is caps, kill switches, repositories,
accounts and a secrets inventory — and two panels that say they have no command at all. The owner's
ask is about **those values**: the settings of this one project, each of them editable from the
console behind a little pencil.

The console has **no editing code of its own**, and this design does not give it one. Every edit is
`fugaro settings set` or `fugaro settings unset` — the generic write path the settings design's **S5**
lands (`internal/cli/settings_set.go`, `internal/cli/settings_write.go`, pull request **#1525**, merged
into `main` while this design was in review) — spawned through the console's existing exact-argv
allow-list, behind a confirmation
window built on the pattern rule 11 already fixed for the launch: a window the **main process** owns,
which shows **what will be spawned** and spawns it verbatim on a yes from **its own top frame alone**.

Three things this design settles that the ask does not:

1. **Which rows get a pencil is the CLI's answer, never the console's.** S5 keeps the allow-list of
   writable rows (`settingsGenericKeys` and `settingsGenericPrefixes`, `internal/cli/settings_set.go:62`, `:83`) and
   the registry's `Owner` and `Sensitive` facts (`internal/settings/registry.go:80`). The console
   reads them and keeps no list of its own; the read's own field is the subject of **D1102** and the
   prerequisite **P2**. A row the read marks `sensitive: true` is **hidden** — no key, no value, no
   pencil — which is the owner's ruling of 2026-11-01 and is pinned as `a Sensitive row is absent
   from the rendered table` (**D1118**).
2. **Widening versus narrowing is the CLI's classification too** — the console asks, it does not
   re-derive (**D1106**, prerequisite **P3**).
3. **The widening path is a deliberate weakening of D846 on one path, and the design says so.** D846
   reserves a widening for the project's name typed at a terminal, and `--yes` never stands in for it
   (`confirmTypedStrict`, `internal/cli/budget.go:239`). The console has no terminal, so it cannot
   perform a widening through S5 as it stands. **D1107** records the three ways out — a narrow CLI
   hook (recommended), narrowings only, or the typed name drawn by the console (the form the owner
   already called "too much") — and is `OPEN`. **It is the one decision the owner's ruling of
   2026-11-01 left open**, and the prerequisite **P4** is what its option **A** costs.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1100–D1139** | **this work's block.** **D1100–D1121 are written and ANSWERED** by the owner on 2026-11-01 ([spec.md](spec.md)): D1100 and D1101 in the ask itself, D1102–D1121 as recommended in his ruling on this design, **except D1107, which is written and `OPEN`**; **D1122–D1139 are reserved and unused**. Taken above every number on `main` and in the open design pull requests. On this branch the highest number **written as a decision** is **D894** (`docs/cloud-senior-review/reconciliation.md`, the tail of that work's D880–D894), and the highest number **cited at all** is **D1074**, which `docs/cloud-senior-review/implementation-plan.md` attributes to the credentials-service routing design **#1514** — an open pull request whose own folder is not on `main`; the numbers between D895 and D1075 are therefore held by open pull requests and by reservations this work does not read. **D1100** is clear of all of them, and the block is taken at the round number above that range rather than at D1076 so that two works cannot claim adjacent numbers and collide. **Verified free on 2026-11-01**: no `D11xx` appears anywhere in `docs/` on this branch |

## What this design does not do

- It changes no code, no schema, no config, no console source, no fixture and no workflow. It writes
  nothing under `console/`, `internal/`, `schemas/`, `plugin/`, `mobile/`, `shared/` or `ee/`.
- It adds no dependency and proposes none.
- It **edits no existing document**, with **one mechanical exception**: the census row in
  `docs/rigor-modes/reconciliation.md` that counts the `internal/cli/docs_*_test.go` pin files moves
  from 90 to 91, because this folder ships the ninety-first and the row's own guard re-runs the
  command it prints. The figure is a count of the tree rather than a decision, and nothing else in
  that document moves. `docs/design/desktop-console.md` §5.1 (rules 5, 6 and the new rule 12 for the
  second write), `console/docs/json-contract.md` §6.15 and `docs/design/console-styling.md` §2.5 are
  **named in the plan** as the documents the implementing tasks change, and not one of them is
  touched here.
- It does not implement **S5**. `fugaro settings get|set|unset` is pull request **#1525**, merged
  into `main` while this design was in review; this design depends on it and names the changes it
  needs as prerequisites **P1–P4** rather than assuming them.
- It **invents no roles** (**D1110**): "administrator" is whoever can write the local config and the
  cloud project today, which is the CLI's own permission outcome and nothing the console decides.
- It does not settle whether the console may ever run `fugaro update`; its recommendation
  (**D1120**) is that it may not, and that the console shows the sentence instead.

## Related

- [docs/settings-commands/](../settings-commands/README.md) — the design of `fugaro settings`, whose
  **S5** is the command every edit here spawns, whose **D846** is the rule the widening path weakens,
  and whose §4.4 is the allow-list of writable rows.
- [docs/design/desktop-console.md](../design/desktop-console.md) §5.1 — rules 5, 6 and 11: the IPC
  allow-list, the exact-argv spawn allow-list with its never table, and the launch, which is the
  pattern this design's confirmation window copies.
- [docs/design/console-styling.md](../design/console-styling.md) §2.5 — the Project screen's class
  hooks, which the pencil and the settings panels extend.
- [docs/console-windows/](../console-windows/README.md) — the WSL route, where the CLI's argv is
  handed to a shell inside a distribution and the wrapper refuses any word with shell metacharacters;
  the value rule of **D1104** has to hold there too.
