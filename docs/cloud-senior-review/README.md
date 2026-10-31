# The independent senior review as a cloud job

**Status: design only, nothing approved.** This is a draft pull request for the owner to read. It adds
no code, no schema, no command and no recipe; it changes no existing file, and no task in
[implementation-plan.md](implementation-plan.md) may start before the owner answers the decisions in
[reconciliation.md](reconciliation.md) §4.

**Asked by:** the owner, 2026-10-31, as priority ONE right after the CLI 0.14.1 and console 0.116.1
releases: the **independent review** run today on the owner's own machine — a local session with the
owner's eyes on the diff plus the pull request's CI — should run **in the cloud, as the senior
reviewer**, on a **named Claude OAuth account**, so the strongest available model reviews a pushed
head without the owner's laptop in the loop. The head of that chain is `fugaro accounts add`
([docs/accounts-add/](../accounts-add/README.md) in the open pull request #1470), which is what makes
a named account usable end to end; this design is the second link.

**What it is:** a **separate Cloud Run job execution**, launched after a coding run's pull request
exists, that clones the pushed head, reviews it fresh with the reviewer's own model on a **named
OAuth account**, and writes a **versioned verdict artifact** the coordinator acts on — ship, fix
first, or reject. The job is **read-only on the forge**: it can fetch the head and nothing else.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The design: what exists today for a reviewer on an OAuth account, how a cloud review of a pushed branch runs, the output contract, how it differs from the in-job reviewer, the trigger, what the coordinator does with the verdict, the threat model, and the owner-run live check |
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `main` and against the open pull requests: findings **F1–F22**, the dependencies **E1–E6**, and the owner decisions **D880–D889**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **R0–R6** (at most 12 files each), the shared-files table, the first slice and the release slot. **Nothing in it is approved** |

## The shape in one paragraph

The coder's job runs the cheap loop and the in-job reviewer exactly as it does today, and it never
holds the subscription token. When it has pushed its branch and opened its pull request, the
**coordinator** (the owner's own session, the console, or a script) launches a **second job**: the
**review job**, in review mode, on the account the owner named, whose forge credential is a
**read-only** installation token minted by the launcher and whose only outputs are a run record and
an artifact under `runs/<slug>/<review-run>/`. The artifact — `review.json` with a
`schema_version` plus a human-readable `review.md` — carries the verdict `ship`, `fix first` or
`reject`, and one entry per issue with a severity, a `file:line`, a class and a suggested fix. The
coordinator reads that artifact by the review run's own id: `ship` means merge, `fix first` launches
a fix round whose brief **is** the findings, and `reject` means no fix round and a person decides.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D880–D899** | **this work's block: D880–D889 written, D890–D899 reserved and unused.** Taken above every number on `main` (the highest is **D859**, the tail of the `settings` design's D840–D859, which merged as #1464) and above every open pull request's numbers (the highest is **D879**, reserved by the `accounts add` design, PR #1470, whose written range is D860–D870). The reading was taken on 2026-10-31 against `origin/main` at `48754c01` and the open pull requests listed in [reconciliation.md](reconciliation.md) §2; it is re-checked at launch, and the block recorded again if it moved |
| **Check 125** | **claimed here, and not written in the register.** `docs/gcp-live-checklist.md` reads **124 as the next free number above 123** and the Cloud Batch backend took it, so the number free above it is **125**; this work claims it for the owner-run live check in [spec.md](spec.md) §8. The register's own file is another work stream's (`docs/gcp-live-checklist.md` is not this task's to touch), so the row and the `## Check 125` section are written by the task that implements the check, under the number free at **that** merge — the register's rule that whichever of two open claims merges second renumbers itself. A reserved number is never reused and never renumbered |

## What this design does not do

- It adds no code, no schema and no command. Every command below is a proposal.
- It does **not** lift D9's hold on the OAuth reviewer stage
  ([docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md), the
  `agent.stage_auth` key). Its own recommendation is that the hold **stays**: this design needs no
  `stage_auth`, because its reviewer is a separate job execution whose credential the coder's job
  never holds. See [spec.md](spec.md) §4 and decision **D881**.
- It invents no credential. The named account is the one `fugaro accounts add` stores; the review
  job mounts that account's token and no other secret it does not need.
- It changes no existing behaviour: an installation that launches no review job sees the tree it has
  today.
- It writes no pull request and no comment of its own on any path: the review job's verdict is an
  artifact, and the coordinator decides what to say.

## Related

- [docs/accounts-add/](../accounts-add/README.md) (open PR #1470): the one command that makes a
  named account usable end to end. It is the **dependency** this design waits on.
- [docs/project-claude-accounts.md](../project-claude-accounts.md): where the account, its secret,
  its job and its `allow_data_to` are described today.
- [docs/design/mixed-credentials.md](../design/mixed-credentials.md) §6: the four mitigations, of
  which **mitigation 4 — a separate job execution for the reviewer — is the only one that closes the
  `/proc` and metadata-server gap**, and is what this design builds.
- [docs/in-job-review/](../in-job-review/README.md): the owner's spec for moving review into the
  coding job, and its reconciliation. This design is the complement: review **outside** the coding
  job.
- [docs/dogfooding.md](../dogfooding.md): the recorded lesson this design exists for — *"the in-loop
  reviewer is a different, stronger model than the coder, but it is still not independent review"*.
- [docs/coordinator/](../coordinator/README.md): the service that would launch the review job and act
  on its verdict.
- [docs/gcp-live-checklist.md](../gcp-live-checklist.md): the register the live check's row and
  section belong to.
