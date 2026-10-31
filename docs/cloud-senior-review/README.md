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
([docs/accounts-add/](../accounts-add/README.md), merged as #1470), which is what makes a named
account usable end to end; this design is the second link.

**What it is:** a **separate Cloud Run job execution**, launched after a coding run's pull request
exists, that clones the pushed head, reviews it fresh with the reviewer's own model on a **named
OAuth account**, and writes a **versioned verdict artifact** the coordinator acts on — ship, fix
first, or reject. The job is **read-only on the forge**: it can fetch the head and nothing else.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The design: what exists today for a reviewer on an OAuth account, how a cloud review of a pushed branch runs, the output contract, how it differs from the in-job reviewer, the trigger, what the coordinator does with the verdict, the threat model, and the owner-run live check |
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `main` and against the open pull requests: findings **F1–F27**, the dependencies **E1–E6**, and the owner decisions **D880–D893** (D880–D891 answered, D892 and D893 open). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **R1–R7** (at most 12 files each), the shared-files table, the first slice and the release slot. **Nothing in it is approved** |

## The shape in one paragraph

The coder's job runs the cheap loop and the in-job reviewer exactly as it does today. **It is a
coding run, so it holds whatever account it was launched on** — `fugaro run --token team-a` puts that
account's subscription token on the shared project job, which is what `agent.oauth_token` means today
(spec §7); what this design adds is a *second* job that holds the token for the review, not a first
job that does not. When the coder has pushed its branch and opened its pull request, the
**coordinator** (the owner's own session, the console, or a script) launches that second job: the
**review job**, in review mode, on a **review-only job variant that mounts no GitHub App key**, on the
account the owner named, whose forge credential is a **read-only** installation token **minted where a
key exists — the credentials service or the coordinator, never the launcher, which holds none** (F16,
D883, spec §2), and whose only outputs are a run record and an artifact under
`runs/<slug>/<review-run>/`. The artifact — `review.json` with a
`schema_version` plus a human-readable `review.md` — carries the verdict `ship`, `fix first` or
`reject`, and one entry per issue with a severity, a `file:line`, a class and a suggested fix. The
coordinator reads that artifact by the review run's own id: `ship` means merge, `fix first` launches
a fix round whose brief **is** the findings, and `reject` means no fix round and a person decides.

## Numbers this work holds

| Block | State |
| --- | --- |
| **D880–D899** | **this work's block: D880–D893 written — D880–D891 answered (D891 by the owner, option (a)), and D892 and D893 open questions for the owner, both raised by the review of this branch's fix round — with D894–D899 reserved and unused.** Taken above every number on `main`, whose highest written is now **D872** — the tail of the `accounts add` design's D860–D872, which **merged** as #1470 and reserved **D873–D879** unused, so **D880 is still the next free number** and this block is unchanged — and above every open pull request's numbers, none of which reserves a decision above D872. The reading was taken on 2026-10-31 against `origin/main` at `48754c01` and re-read against the merged accounts design at `d558dace`; it is re-checked at launch, and the block recorded again if it moved |
| **Check 125** | **claimed here, and not written in the register.** The register's own sentence is *"Check 124 is the Cloud Batch backend's own, and it is the next number free above 123"*: it reads **124 as a number the Cloud Batch backend took**, in four written parts (**124a–124d**), not as one still free — so the highest number in use is **124**, and the number free above it, which this work claims for the owner-run live check in [spec.md](spec.md) §8, is **125**. The register's own file is another work stream's (`docs/gcp-live-checklist.md` is not this task's to touch), so the row and the `## Check 125` section are written by the task that implements the check, under the number free at **that** merge — the register's rule that whichever of two open claims merges second renumbers itself. A reserved number is never reused and never renumbered |

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
- **It does not claim the review job is contained.** The reviewer's mutation check runs repository
  code in the container that holds the person's token. **D891 is answered — option (a):** the mutation
  test runs in a subprocess with **the token scrubbed from its environment and egress denied**, and
  **Bash is denied for the rest of the review stage**, the mutant being run by the runner rather than
  by the agent's shell. What is left is the `/proc` read, which §7 states as the residual and which
  only the separate container reduces. **R4 is unblocked and carries that machinery, subject to one
  gate:** no network namespace or `unshare` in Cloud Run's gen2 has been checked (**U7**), so **R4's
  first step is that measurement**, and if a child process cannot be fenced the answer falls back to
  **(b)** or **(c)** and the owner is asked again — the design does not assume a fence it has not
  seen.
- **It does not launch on a job that already exists.** The shared project job of
  `(profile, provider, account)` mounts the GitHub App key, so a read-only scope on it would be a
  comment on the launcher's intent rather than a boundary; the design adds a **review-only job variant
  and service account per account, with no App key** (**R7**), and it says who mints the read-only
  token — never the launcher, which holds no key.
- **It does not settle the verdict's provenance, and it says so.** Ordering keeps a *coder* from
  forging its own review (spec §6), but another job on the same shared project job can write under the
  review run's prefix; the mitigation — a signature, a narrower prefix, or accepting the reach — is
  **D892, still open**, and until the owner answers **this design relies on none of the three**.
- **It does not settle how the minted read-only token reaches the job either.** That is **D893, still
  open**: the design recommends a per-launch secret over the execution override (a plain value there
  is readable by any project member holding `run.viewer`), and **R7 implements whichever the owner
  names, claiming no channel of its own before then**.

## Related

- [docs/accounts-add/](../accounts-add/README.md) (**merged** as #1470, design only): the one command
  that makes a named account usable end to end. It is the **dependency** this design waits on, and its
  remaining tasks — the command itself — are still unbuilt.
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
