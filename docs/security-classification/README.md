# Security classification — each project says what counts as security-sensitive for it

**Status: three of the plan's tasks are in the tree and no decision is answered.** **SC1** — the floor,
the matcher and the union merge — landed as `e5a4605f` (#1916:
`internal/orchestrator/sensitivity/{pattern,floor}.go`); **SC4** — doctor's line about the list in force,
D1513 — landed as `0ee73e2e` (#1975: `internal/cli/doctor_security.go`); and **SC2** — the pin for this
folder's own claims — lands with this change (`internal/cli/docs_security_classification_test.go`). The
other eight tasks are written and not built, and what this folder **proposes** for a repository — the two
config keys, `fugaro security paths`, the runner's escalation — is not in force, because **every one of
the twenty decisions `D1500–D1519` still carries an empty owner's-answer cell**
([reconciliation.md](reconciliation.md) §8): the ask's own rule is that nothing is decided silently.
Beyond the landed tasks' code and SC2's pin the folder adds no code, no schema, no config, no fixture and
no workflow: it edits no file outside `docs/`, and apart from those it writes nothing under `schemas/`,
`plugin/`, `console/`, `mobile/`, `shared/`, `ee/`, `deploy/` or `testdata/`.

**Asked by:** the owner, 2026-11-07, as task **SC0**: "we have to find a way to allow each project using
Fugaro to determine what counts as security sensitive for their own situation. It should have a project
level component, and an optional repo-level one." His words are [spec.md](spec.md), verbatim, and they
are the only text here he wrote.

**Spec owner:** the owner, 2026-11-07. [spec.md](spec.md) is his ask, verbatim; where
[reconciliation.md](reconciliation.md) disagrees with it about the code, **reconciliation wins** — and
it does, twice: the ask's `internal/projectpolicy` is the published policy node rather than the project
layer (reconciliation F4); its tasks **OD1 and OD2 were not in this tree when this was read** — **OD1
landed the same day** (`505718ab`, #1882: a finding marked `needs_owner` stops the loop rather than
buying a fix round) and OD2 has not — while the classifier its **pull request #1822** names **is in it**
(`405dae8b`, an ancestor of the base this folder was read against), which is F15.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's ask, verbatim, with a header naming him by role |
| [reconciliation.md](reconciliation.md) | The headline table of the ask's seven questions (§1); what "security-sensitive" is today, in **four** places and none of them a config key (§2); the code the design has to stand on, by path and symbol (§3); the findings **F1–F26** (§4); the list as a security control, as an attacker would see it (§5); the floor written down — **43 named globs**, which match **every one of the 32 paths** in the classifier's own routing fixture **and all 11 of its `sensitiveExceptions` rules** (§6); what this design does not do (§7); **the decisions D1500–D1519**, each a table row with a recommendation, the alternative, its cost and an **empty owner's-answer cell** (§8); the number block and how it was read (§9); **the open tensions** (§10); **UNVERIFIED** (§11) |
| [implementation-plan.md](implementation-plan.md) | The three slices, the eleven tasks **SC1–SC11** in eight levels, each with its files (at most six), its interfaces, its named tests and command, its **red step**, its **mutation**, its **reviewer tier** and its **owner-run flag**; the critical path and the shared-files table proving **no level has two writers of one file** (§1–§2); the slices (§3); the tasks in detail (§4); the levels and the critical path (§5); the owner-run live check **Check 370** (§6); what is deliberately not in the plan (§7). **Nothing in it is implemented and no task is approved** |

## What it is

The rule the ask wants configurable — *security-sensitive work gets the independent review* — is real
today, and the tree keeps it in **four places at once**: the path classifier
(`internal/orchestrator/sensitivity/classify.go`, `Classify`, `routineRules`, `sensitiveExceptions`),
the reviewer-routing rule (which is a **test fixture** plus a paragraph in a plan document), the
`.claude/rules/*.md` `paths:` scopes, and the review brief `.fugaro/review.md`. **None of the four is a
config key** (F2), and the classifier — which *is* program code, and is on `main` — has **zero call
sites**: no production file imports it and no class reaches a model, a level or a record (F1).

Three things this design settles that the ask does not, and each changes the answer rather than
decorating it:

1. **The engine's floor is not a list that can be copied.** The classifier is an **allowlist of routine
   trees** — `docs/` and `website/` minus a handful of exceptions — so its "Sensitive" side is
   *everything else*, including every tree nobody has classified. The ask's "the engine's built-in
   default list … as the floor" therefore has no list to re-export: the floor's *effect* exists and is
   fail-closed (F22), but its *membership* must be **written**, as one named glob per category
   ([reconciliation.md](reconciliation.md) §6, F1, D1505).
2. **The level-3 trigger has to be a positive list, and it is a different question from the model
   class.** `Sensitive` decides the **model** (never below Opus); the pattern list decides the
   **review level**. "Level 3 for everything that is not documentation" is not the rule the ask wants —
   it would run the adversarial pass on nearly every run (D1505, D1511, tension 1).
3. **The readers ship before the actors.** Slice 1 is the floor, `fugaro security paths --check --json`
   and doctor's line, and it **changes no run's cost**; the escalation is slice 2 and the two config
   keys are slice 3 (D1519). An owner can see their list in force before anything acts on it.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D1500–D1519** | **this work's block**, taken above every number written or reserved on `main` and **reserving nothing above it**. Read on 2026-11-07 against `origin/main` at **`af2644fb`**: the highest D number **written** is **D1489** (`docs/console-managed-screens/`, which holds D1470–D1489), and **D1490–D1499** appear only as that work's own reserve row. **Verified free**: no `D15xx` appears anywhere on `main` or in any local ref (§9 carries the commands). All twenty rows are written and **every one of them has an empty owner's-answer cell** — none is answered, and none is approved |
| **Check 370** | this work's live check (implementation-plan §6), **with 371–379 available**. The highest `## Check` **section written** on `main` is **291** (`docs/gcp-live-checklist.md`); the highest number **cited** on `main` is **360** (`docs/console-managed-screens/`, whose README offers 361–369). **370 is the first number above every claim and every reserve on `main`** — and it is chosen *well above* the highest cited number rather than adjacent to it, for the reason in the next row |

**What this reading could not include, and why the block is taken high.** The ask says to grep `main`
**and every open PR's docs**; **this checkout has fetched no pull-request head at all**
(`git for-each-ref 'refs/pull/*/head' \| wc -l` is **0**), and the fetch that would read them needs the
network. So a number claimed in an unmerged pull request is **not** in this reading. That is exactly why
370 is taken well above the highest **cited** number (360) instead of beside it, and why
[reconciliation.md](reconciliation.md) §9 states the two readings that *are* available — the written
section and the cited number — rather than claiming all four.

## What this design does not do

- Beyond the code the two landed tasks add (`internal/orchestrator/sensitivity/`,
  `internal/cli/doctor_security.go`), it **changes no code, schema, config, fixture or workflow**, and it
  edits no file outside `docs/` and the one Go test that pins this folder.
- It **adds one pinning test, and the census moved once.** There is **no registry test over `docs/`**
  (F25), so a folder may be documents alone — `docs/image-env/` is the four-document precedent this
  folder follows — and the brief's DOCS ONLY left the pin to the plan's tasks, which name the one each
  adds: this folder's is `internal/cli/docs_security_classification_test.go` (SC2, D1519), and its
  arrival moved `docs/rigor-modes/reconciliation.md`'s census row **113 → 114**, as that row's own
  amendment sentence records. The three census re-check tests in other folders' files (F17) re-derive
  the count from the tree and needed no edit.
- It **implements no part of OD1's or OD2's work** and is written to be independent of both (D1518):
  **OD1 landed while this folder was being read** (2026-11-07, `505718ab`: a finding marked `needs_owner`
  stops the loop), and this design neither consumes nor changes it — no pattern list is read or written
  by it — while **OD2 is still not in this tree** (F15).
- It **builds no console screen** (D1514): the ask defers it and the console's own rule agrees — a read
  is an exact-argv row on two fences held equal by a Go test (F24).
- It **adds nothing to `settings set`'s allow-list** (D1515): the list is edited in the layer file and
  the repository's `fugaro.yaml`, and the managed allow-list admits nothing new.
- It **invents no role**, no new permission model and **no second matcher** — the dialect is the
  `doublestar` glob the tree already matches paths with (F21).
- It adds **no dependency** and proposes none.

## Related

- [docs/stateless-orchestrator/](../stateless-orchestrator/) — O1, the classifier this design builds
  its floor beside, and D1234, the ruling that the class "must not depend on how a change describes
  itself"; O4, its intended model consumer.
- [docs/rigor-modes/](../rigor-modes/) and [docs/review-levels-ab-jobmax/](../review-levels-ab-jobmax/) —
  what a rigor mode is, what level 3 is (the adversarial review), and how the level resolver's
  precedence works.
- [docs/console-managed-screens/](../console-managed-screens/) — the number block this one reads above,
  and the precedent for a deferred read-only console screen.
- [docs/project-layer.md](../project-layer.md), [docs/design/layered-config.md](../design/layered-config.md) —
  the project layer the new key joins, and its scope table.
- [.claude/rules/credentials-service.md](../../.claude/rules/credentials-service.md), [console.md](../../.claude/rules/console.md),
  [terraform.md](../../.claude/rules/terraform.md) — three of the four places the definition lives today,
  by `paths:` scope.
- [docs/managed-agent-converge/](../managed-agent-converge/) — D1027, the managed allow-list and its
  floor rule, which D1515 and D1516 respect.
- [plugin/skills/working/](../../plugin/skills/working/) — the skill SC11 gives its sentence to, and the
  lint that checks every command it names against the CLI.
