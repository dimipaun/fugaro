# Periodic run reviews: the reports, the patterns, the practice and the learnings

**Status: design only; nothing here is approved and no code exists for it.** The owner decides.
**D960–D971 are written and open** (the owner answered none of them); **D972–D979 are reserved and
unused**. The plan is written to the recommended answer of each decision.

**Asked by:** the owner, 2026-10-25: "daily, weekly, monthly and yearly run-review reports, each
committed to the tree; the official skills updated so every Fugaro user does the periodic reviews;
and a skill that encourages submitting valuable learnings centrally, to enter the official skills."
The owner agreed the shape the same day and approved a design pull request on 2026-10-31.
**The verbatim original of the request was not recovered by this run**: [spec.md](spec.md) §1 holds
the recorded request, copied as the spec, and every organisation and person in it and in these
documents is a placeholder (`owner/fugaro`, `owner/repo-a`, `person-1`) — this design is written for
a tree that may be public.

**Reconciled against** `origin/main` at `3d6ed04d` and the 23 pull requests open on 2026-11-01.

## What it is

Most of the request already exists, and it is tested. The `task-governance` work (D320–D328,
answered 2026-10-26, merged through 0.13.0–0.14.1) shipped the whole loop: `fugaro runs digest`
writes one closed period's report of one repository — day, week, month or year — into that
repository's own `docs/fugaro-reviews/<YYYY>/<key>.md` with a generated index
(`internal/digest/layout.go:23`); the fixed pattern rules read the committed reports over time
(`internal/digest/patterns.go:91`); the `run-digest` recipe has a run write, commit and open the
pull request (`internal/recipe/catalog/run-digest.yaml`); `--submit-draft` prints a redacted
learning draft and sends nothing (`internal/cli/runs_digest_submit.go`); and the `working` skill
carries the cadence, the reading and the submission step
(`plugin/skills/working/reference/periodic-reviews.md`), pinned by
`plugin/periodic_reviews_skill_test.go`. **What is missing is the part that makes a user who is not
asking keep the cadence**, and a handful of smaller gaps. This design reconciles each claim of the
request against the tree and settles those.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The recorded request (§1), what the periodic review is (§2–§7), what this design adds (§8–§9). The commands are written to the recommended answer of each decision |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists (F1–F13), what is missing or conflicts (F14–F24), and the decisions **D960–D971**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks R0–R6 (at most 12 files each, named tests, red steps, SEC flags) with the shared-files table, and the release slot. **Nothing in it is approved** |

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D960–D979** | this work's block: **D960–D971** written and **every one open — the owner has answered none**; **D972–D979** reserved and unused. Taken above every number on `main`, whose highest written is **D900** (`docs/rigor-command/README.md:117`), and above every open pull request's numbers, re-checked on 2026-11-01 rather than inherited: **#1472** writes D880–D899; **#1481** (`docs/run-without-checkout`) writes D901–D919; **#1485** (`docs/run-limit-carry-over`) writes D901–D920 — the two overlap, and neither is this work's to fix (**noticed, not done**: both were open on 2026-11-01, and which of them renumbers is their owners' to settle); **#1487** (`docs/mcp-server`) writes D920–D934 and reserves D935–D939; **#1488** (`docs/plugin-user-scope`) writes D940–D953, reserves D954–D959 and names **D960** as the next free number. So D960 is the first number free on `main` and in every open pull request. Nothing beyond D979 is reserved here; a later task takes D980 and re-checks these facts rather than this conclusion |
| **Checks** | **none.** Every behaviour this design proposes is an offline read of a committed folder or of the local config, and the plan tests each one hermetically (`t.TempDir()`, a fake clock). The one live check in this area already exists and is registered: **Check 136**, the report run's own pull request (`docs/gcp-live-checklist.md:764`), which is **NOT RUN** and is the owner's (`docs/gcp-live-checklist.md:10612`). The register is not touched by this design |

## What this design does not own

`plugin/**` (the skills), `website/**` (the figures the reports feed), `internal/runner/runner.go`
and `internal/cli/root.go` are each owned by another 0.15.0 work stream or an open pull request.
Tasks R3 and R5 name them and wait for the owner's go, as `docs/models-commands` does for its own
borrowed files.
