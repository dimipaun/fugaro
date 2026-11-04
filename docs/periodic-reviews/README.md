# Periodic run reviews: the reports, the patterns, the practice and the learnings

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** D960–D971 are written, **D972–D979** are reserved and unused, and the plan is written to
the owner's answers ([reconciliation.md](reconciliation.md) §3).

**Asked by:** the owner, 2026-10-25: "daily, weekly, monthly and yearly run-review reports, each
committed to the tree; the official skills updated so every Fugaro user does the periodic reviews;
and a skill that encourages submitting valuable learnings centrally, to enter the official skills."
The owner agreed the shape the same day, approved a design pull request on 2026-10-31, and **answered
the design on 2026-11-04**. **The verbatim original of the request was not recovered by this run**:
[spec.md](spec.md) §1 holds the recorded request, copied as the spec, and every organisation and
person in it and in these documents is a placeholder (`owner/fugaro`, `owner/repo-a`, `person-1`) —
this design is written for a tree that may be public.

**Reconciled against** `origin/main` at `b41709ea`, and **refreshed on 2026-11-04** against the tree
that commit carries and the work streams the launcher names: the managed gate, the person-class
reads, the `settings`/`rigor`/`accounts` commands, the project image catalog, `fugaro upgrade` and
`internal/runner/stray.go`. What the refresh found, and what it changed, is
[reconciliation.md](reconciliation.md) §4.

## What it is

**Most of the request already shipped, under `task-governance` D320–D328** (answered 2026-10-26; the
recipe and the digest step landed in 0.13.0 and every release since carries them — 0.17.0 is the
current one). That work shipped the whole loop: `fugaro runs digest` writes one closed period's
report of one repository — day, week, month or year — into that repository's own
`docs/fugaro-reviews/<YYYY>/<key>.md` with a generated index (`internal/digest/layout.go:23`); the
fixed pattern rules read the committed reports over time (`internal/digest/patterns.go:91`); the
`run-digest` recipe has a run write, commit and open the pull request
(`internal/recipe/catalog/run-digest.yaml`); `--submit-draft` prints a redacted learning draft and
sends nothing (`internal/cli/runs_digest_submit.go`); and the `working` skill carries the cadence,
the reading and the submission step (`plugin/skills/working/reference/periodic-reviews.md`), pinned
by `plugin/periodic_reviews_skill_test.go`. **What is missing is the part that makes a user who is
not asking keep the cadence**, and a handful of smaller gaps. This design reconciles each claim of
the request against the tree and settles those.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The recorded request (§1), what the periodic review is (§2–§7), what this design adds (§8–§9). The commands are written to the owner's answers of 2026-11-04 |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists (F1–F13), what is missing or conflicts (F14–F24), the decisions **D960–D971** with the owner's answers (§3), and what the 2026-11-04 refresh found (§4). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks R0–R6 (at most 12 files each, named tests, red steps, SEC flags) with the shared-files table, and the release slot. **Plan only: no task is approved and nothing is implemented** |

## The owner's answers, 2026-11-04

Three decisions were answered in words, **each as recommended, so nothing here is "not the
recommendation"**; the other nine were not singled out and the recommendation stands for each
([reconciliation.md](reconciliation.md) §3).

- **D965 — the user installs the cadence; Fugaro writes no workflow and no crontab.** *Why:* a
  schedule Fugaro wrote would commit into a repository it does not own, under the user's identity,
  for a choice that is cheap for the user to make and expensive for Fugaro to get wrong.
- **D968 — yes, one longer window: `cost_per_run_rising`, over five months.** *Why:* the four rules
  read at most four weeks and one month against the month before, so a slow rise across a quarter and
  a half is invisible to every one of them.
- **D969 — the committed report keeps run IDs, stages, recipes and figures and carries no person,
  address, task text or log line; the outgoing draft renumbers the runs and drops the repository
  name.** *Why:* the report's reader is the repository's own agent and reviewer, who acts on a
  flagged row by running `fugaro diagnose RUN`, while the draft's reader is a public issue that needs
  the figures and none of the identifiers.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D960–D979** | this work's block, **kept at the launcher's 2026-11-04 reading**: D960–D971 written here and **every one answered**; **D972–D979** reserved and unused. **It collides with nothing on `main`**: grepping `main`'s `docs/` for D960–D979 returns only two other designs naming *this* block (`docs/managed-agent-converge/reconciliation.md:250`, `docs/run-limit-carry-over/README.md:54`), never a decision. Main's own highest block is `docs/config-management`'s **D1160–D1189** (D1160–D1183 written, D1184–D1189 reserved), and the launcher's 2026-11-04 reading takes, above it, **#1676** D1190–D1219, **the stateless-orchestrator work** D1220–D1259, **#1678**'s design D1260–D1289 and **#1492**'s new block D1290–D1309. Those branches are not in this checkout, so the reading is the launcher's and a merger re-reads it. **D1310** is the first number free above all of them; this work keeps its own block rather than renumbering into it, which is what the two census tables that name it already expect |
| **Checks** | **none.** Every behaviour this design proposes is an offline read of a committed folder or of the local config, and the plan tests each one hermetically (`t.TempDir()`, a fake clock). The one live check in this area already exists and is registered: **Check 136**, the report run's own pull request (`docs/gcp-live-checklist.md:764`), which is **NOT RUN** and is the owner's (`docs/gcp-live-checklist.md:10621`). The register is not touched by this design |

## What this design does not own

`plugin/**` (the skills), `website/**` (the figures the reports feed), `internal/runner/runner.go`
and `internal/cli/root.go` are each owned by another work stream or an open pull request. **#1488
(`docs/plugin-user-scope`) moves `plugin/**` to user scope and rewrites the setup skill**, so the
tasks that touch it — R3, R4's rule list and R6's §7 sentence — are sequenced after it
([implementation-plan.md](implementation-plan.md), "Order"). R3 names the borrowed files and waits
for the owner's go on them; R5 is the owner's own task at their terminal, and R5 and R6 name the
`website/**` line they do not write. `docs/models-commands` does the same for its own borrowed
files.
