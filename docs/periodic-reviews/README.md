# Periodic run reviews: the reports, the patterns, the practice and the learnings

**Status: design answered by the owner 2026-11-04, and ruled on again 2026-11-05; plan only; nothing
implemented; no task approved.** D960–D971 are written, **D972–D979** are reserved and unused, and the
plan is written to the owner's answers ([reconciliation.md](reconciliation.md) §3) **and to the
rulings of 2026-11-05 (§5, E1–E8)**, which take no decision number and move the reports out of the
repositories and into the project's runs bucket.

**Asked by:** the owner, 2026-10-25: "daily, weekly, monthly and yearly run-review reports, each
committed to the tree; the official skills updated so every Fugaro user does the periodic reviews;
and a skill that encourages submitting valuable learnings centrally, to enter the official skills."
The owner agreed the shape the same day, approved a design pull request on 2026-10-31, **answered
the design on 2026-11-04** and **ruled again on 2026-11-05** (the reports are not committed per
repository; they are written into the project's runs bucket, and committing one anywhere is opt-in).
**The verbatim original of the request was not recovered by this run**:
[spec.md](spec.md) §1 holds the recorded request, copied as the spec, and every organisation and
person in it and in these documents is a placeholder (`owner/fugaro`, `owner/repo-a`, `person-1`) —
this design is written for a tree that may be public.

**Reconciled against** `origin/main` at `b41709ea`, and **refreshed on 2026-11-04** against the tree
that commit carries and the work streams the launcher names: the managed gate, the person-class
reads, the `settings`/`rigor`/`accounts` commands, the project image catalog, `fugaro upgrade` and
`internal/runner/stray.go`. What the refresh found, and what it changed, is
[reconciliation.md](reconciliation.md) §4. **A second refresh, the owner's rulings of 2026-11-05, is
[reconciliation.md](reconciliation.md) §5**: the reports move to the project's runs bucket, the
project-level reports job and the opt-in commit are the plan's new tasks, and `spec.md` is not
rewritten for it.

## What it is

**Most of the request already shipped, under `task-governance` D320–D328** (answered 2026-10-26; the
recipe and the digest step landed in 0.13.0 and every release since carries them — 0.17.0 is the
current one). That work shipped the whole loop: `fugaro runs digest` writes one closed period's
report of one repository — day, week, month or year — with a generated index
(`internal/digest/layout.go:23`); the fixed pattern rules read the reports over time
(`internal/digest/patterns.go:91`); the `run-digest` recipe has a run write, commit and open the pull
request (`internal/recipe/catalog/run-digest.yaml`); `--submit-draft` prints a redacted learning draft
and sends nothing (`internal/cli/runs_digest_submit.go`); and the `working` skill carries the cadence,
the reading and the submission step (`plugin/skills/working/reference/periodic-reviews.md`), pinned
by `plugin/periodic_reviews_skill_test.go`. **Where it writes is what the 2026-11-05 rulings change:**
the shipped command writes into the repository's own `docs/fugaro-reviews/<YYYY>/<key>.md`, and the
plan moves the product's default to the project's runs bucket under `reports/`, with committing a
report anywhere an opt-in run aimed at the project's designated repository or folder
([reconciliation.md](reconciliation.md) §5, E1, E5, E8). **What is missing is the part that makes a
user who is not asking keep the cadence**, and a handful of smaller gaps. This design reconciles each
claim of the request against the tree and settles those.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The recorded request (§1), what the periodic review is (§2–§7), what this design adds (§8–§9). The commands are written to the owner's answers of 2026-11-04, **and this run does not rewrite it**: §3, §5 and §6 keep their 2026-11-04 text and are read through [reconciliation.md](reconciliation.md) §5 |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists (F1–F13), what is missing or conflicts (F14–F24), the decisions **D960–D971** with the owner's answers (§3), what the 2026-11-04 refresh found (§4), and **the owner's rulings of 2026-11-05 (§5, E1–E8)**, which amend D321, D322 and G2. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks R0–R13 (the reports job's module and grants, its paused setting, the plan and the command, the aggregate, the opt-in commit and the `runs digest` default among them) with the shared-files table, the levels, **Open items** and the release slot. **Plan only: no task is approved and nothing is implemented** |

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

## The owner's rulings, 2026-11-05

The owner ruled again the next day, and **the rulings amend D321, D322 and G2 of the 2026-11-04
reconciliation** ([reconciliation.md](reconciliation.md) §5, E1–E8). They take **no decision number**
— an answer that amends one takes none — so D960–D971 stay written and D972–D979 stay reserved.

- **E1 — the reports are not committed per repository.** Every report, each repository's and a
  project-level aggregate, is generated into the project's runs bucket under a `reports/` prefix; a
  report may be committed afterwards, but only to the project's **designated repository or folder**,
  never into a code repository's own docs tree by default. **Not the recommendation**: the earlier
  answer was the repository's own tree. *Why:* the writer is one project-level job with no git
  credential, and a commit decides a branch, an identity and a message in a repository it does not
  own.
- **E2 — D322 is restated and G2's absolute rule is relaxed.** D322's own answer already read "by
  default … committed only with an explicit flag into a repository the person names", so what the
  ruling amends is **G2's hard line** ("one repository per report; the digest reads only the
  repository it is pointed at"): the aggregate names every repository and may be committed into a
  repository the user chose, and public or private is theirs to weigh. **Not the recommendation**,
  as D322's record reads. *Why:* the aggregate is the one document that answers what the project
  spent.
- **E3 — retention.** The `reports/` prefix gets its own bucket lifecycle, **18 months** — the
  bucket's **fifth** rule; run records keep their 90 days (F24) untouched. *Why:* a yearly report
  rolls twelve stored monthly reports up, and they must outlive the records by more than a year.
- **E4 — one project-level writer, no per-orchestrator scheduler.** A single scheduled Cloud Run job
  in the project's GCP, on the 0.18.0 catalog check job's pattern: its own narrowly granted account,
  **created paused behind a setting**, planned by `fugaro update`, with `objectViewer` on `runs/`
  **through the prefix-limited listing grant** and `objectUser` on `reports/` — **no git credential,
  no model credential and no secret**, and **no launcher write on `reports/`**: the job is its one
  writer and a person only reads it. D965 is not reopened — what it refused is a schedule written
  into a repository Fugaro does not own. The listing mechanism (`objectListPrefix`) is unverified in
  this repo, so **an owner-run check proves it before the job's Terraform lands**; if it fails the
  plan stops and asks.
- **E5 — committing is opt-in and separate.** One normal Fugaro run, on the push path and identity
  runs already have, pulls the stored reports into the designated repository or folder and opens a
  pull request; the read of `reports/` is the **starting person's launcher account**, since no job
  account holds that prefix. Nothing else needs a git credential.
- **E6 — the designated project repo or folder is a separate design item**, listed under Open items;
  the committing task depends on it and no config key is invented for it here.
- **E7 — the pattern rules read the stored reports** in the bucket, and the committed ones if they
  are there, so the patterns work whether or not anything is committed. A project-level aggregate has
  no rules in the first slice.
- **E8 — `fugaro runs digest` generates to the bucket by default**; `--dir` still writes locally for
  a person who wants the files. **How the command writes there at all is left open**: E4's answer
  makes the job the only writer of `reports/` and every launcher a reader, so the command has no
  grant to write `reports/` with the caller's own credentials (Open items).

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D960–D979** | this work's block, **kept at the launcher's 2026-11-04 reading**: D960–D971 written here and **every one answered**; **D972–D979** reserved and unused. **The owner's rulings of 2026-11-05 take no number of their own** — they amend D321, D322 and G2 rather than allocating one, the shape `docs/cloud-senior-review/README.md:67` records for the same situation — so the block is unchanged. **It collides with nothing on `main`**: grepping `main`'s `docs/` for D960–D979 returns only other designs **naming** this block — `docs/managed-agent-converge/reconciliation.md:253` (and its README's inventory at `:40`), `docs/run-limit-carry-over/README.md:54`, `docs/c39-review-identity/README.md:81` and `docs/run-without-checkout/README.md:101` — and never a decision written in it. Main's own highest block is `docs/config-management`'s **D1160–D1189** (D1160–D1183 written, D1184–D1189 reserved), and the launcher's 2026-11-04 reading takes, above it, **#1676** D1190–D1219, **the stateless-orchestrator work** D1220–D1259, **#1678**'s design D1260–D1289 and **#1492**'s new block D1290–D1309. Those branches are not in this checkout, so the reading is the launcher's and a merger re-reads it. **D1310** is the first number free above all of them; this work keeps its own block rather than renumbering into it, which is what the two census tables that name it already expect |
| **Checks** | **none taken, and two are named.** The **2026-11-05 rulings add the first behaviour here that cannot be tested hermetically** — the reports job, its grants and its paused schedule (E4) — so R12 and R7 name **two** owner-run live checks, the listing grant (`storage.googleapis.com/objectListPrefix`, which the bucket-IAM design records as unverified — V7 — and which R12's Terraform depends on: the check lists `runs/<slug>/` and `reports/`, reads one `runs/` object and is refused a `tokens/` read and a `tokens/` or `cache/` listing) and the job's own firing, and **numbers neither from a block**: this folder reserved none, and the register's own rule is that a work which reserved none takes the number free at the merge of the task that writes it (`docs/run-limit-carry-over/README.md:55`, `docs/project-image-catalog/README.md:99` are the same shape). The one live check in this area that already exists and is registered stays what it is: **Check 136**, the report run's own pull request (`docs/gcp-live-checklist.md:785`), **NOT RUN** and the owner's (`docs/gcp-live-checklist.md:10643`). The register is not touched by this design |

## What this design does not own

`plugin/**` (the skills), `website/**` (the figures the reports feed), `internal/runner/runner.go`
and `internal/cli/root.go` are each owned by another work stream or an open pull request. **#1488
(`docs/plugin-user-scope`) moves `plugin/**` to user scope and rewrites the setup skill**, so the
tasks that touch it — R3's rule list and R6's §7 sentence — are sequenced after it
([implementation-plan.md](implementation-plan.md), "Order"). R3 names the borrowed files and waits
for the owner's go on them; R5 is the owner's own task at their terminal, and R5 and R6 name the
`website/**` line they do not write. `docs/models-commands` does the same for its own borrowed
files. **The 2026-11-05 rulings add three of the same kind:** `docs/gcp-live-checklist.md` is another
work stream's file, so R12's and R7's live checks are named and not written here; the **designated
project repository or folder** is a separate design item (E6) that R9 depends on, and no config key
for it is invented here; and the `budget`-database read the aggregate's figures need is a question
about R12's grants that is listed under Open items rather than decided.
