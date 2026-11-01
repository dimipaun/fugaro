# The independent senior review as a cloud job

**Status: design only, nothing approved.** This is a draft pull request for the owner to read. It adds
no code, no schema, no command and no recipe; it changes no existing file. The owner has **answered**
the decisions in [reconciliation.md](reconciliation.md) §4 — D892 with option (a) and D893 with option
(c) last — but **an answer is not an approval**: no task in
[implementation-plan.md](implementation-plan.md) may start before the owner approves it, and two of
them wait on gates outside this design (E7 and U7, below).

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
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `main` and against the open pull requests: findings **F1–F27**, the dependencies **E1–E7** (**E7** — the credentials service enabled and admitting a project-scoped job — is a hard precondition, not a task: it blocks R7, R2 and the coordinator's `ship` → merge path), and the owner decisions **D880–D893**, **all answered** — D880–D891, then **D892 with option (a)** (the credentials service signs the verdict) and **D893 with option (c)** (the read-only token comes from the credentials service, by name), with **D894–D899 reserved and unused**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **R1–R7** (at most 12 files each), the shared-files table, the first slice and the release slot. **Nothing in it is approved** |

## The shape in one paragraph

The coder's job runs the cheap loop and the in-job reviewer exactly as it does today. **It is a
coding run, so it holds whatever account it was launched on** — `fugaro run --token team-a` puts that
account's subscription token on the shared project job, which is what `agent.oauth_token` means today
(spec §7); what this design adds is a *second* job that holds the token for the review, not a first
job that does not. When the coder has pushed its branch and opened its pull request, the
**coordinator** (the owner's own session, the console, or a script) launches that second job: the
**review job**, in review mode, on a **review-only job variant that mounts no GitHub App key**, on the
account the owner named, whose forge credential is a **read-only** installation token **the credentials
service mints and the job fetches by name — nothing is handed in, and the launcher, which holds no
key, is on no path** (F16, D883, **D893 (c)**, spec §2), and whose only outputs are a run record and an
artifact under `runs/<slug>/<review-run>/`. The artifact — `review.json` with a
`schema_version` plus a human-readable `review.md` — carries the verdict `ship`, `fix first` or
`reject`, one entry per issue with a severity, a `file:line`, a class and a suggested fix, and **a
signature over its own bytes** (**D892 (a)**), produced by the credentials service and binding the
repository, the pull request, the head and the review run, which the coordinator verifies before it
acts: `ship`
means merge, `fix first` launches a fix round whose brief **is** the findings, and `reject` means no
fix round and a person decides. **The whole chain requires the credentials service to be enabled for
the installation, and it ships dormant** — D893(c)'s precondition, which is also what keeps R7, R2 and
the coordinator's merge path blocked (spec §2, §5; reconciliation E7).

## Numbers this work holds

| Block | State |
| --- | --- |
| **D880–D899** | **this work's block: D880–D893 written and answered — D891 by the owner with option (a), D892 with option (a) (the credentials service signs the verdict) and D893 with option (c) (the read-only token comes from the credentials service, by name) — with D894–D899 reserved and unused.** D892 and D893 were raised by the review of this branch's fix round; the owner has since answered both. **D893(c) is a precondition of the whole design, not a channel**, so it is the block's own open edge rather than a settled question: see [spec.md](spec.md) §2 and **E7** in [reconciliation.md](reconciliation.md) §5. Taken above every number on `main`, whose highest written is now **D872** — the tail of the `accounts add` design's D860–D872, which **merged** as #1470 and reserved **D873–D879** unused, so **D880 is still the next free number** and this block is unchanged — and above every open pull request's numbers, none of which reserves a decision above D872. The reading was taken on 2026-10-31 against `origin/main` at `48754c01` and re-read against the merged accounts design at `d558dace`; it is re-checked at launch, and the block recorded again if it moved |
| **Check 125** | **claimed here, and not written in the register.** The register's own sentence is *"Check 124 is the Cloud Batch backend's own, and it is the next number free above 123"*: it reads **124 as a number the Cloud Batch backend took**, in four written parts (**124a–124d**), not as one still free — so the highest number in use is **124**, and the number free above it, which this work claims for the owner-run live check in [spec.md](spec.md) §8, is **125**. The register's own file is another work stream's (`docs/gcp-live-checklist.md` is not this task's to touch), so the row and the `## Check 125` section are written by the task that implements the check, under the number free at **that** merge — the register's rule that whichever of two open claims merges second renumbers itself. A reserved number is never reused and never renumbered |

## What this design does not do

- It adds no code, no schema and no command. Every command below is a proposal.
- It does **not** lift D9's hold on the OAuth reviewer stage
  ([docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md), the
  `agent.stage_auth` key). Its own recommendation is that the hold **stays**: this design needs no
  `stage_auth`, because its reviewer is a separate job execution whose credential the coder's job
  never holds. See [spec.md](spec.md) §4 and decision **D881**.
- It invents no credential. The named account is the one `fugaro accounts add` stores; the review
  job mounts **that account's Claude token and nothing else** — the owner's ruling of 2026-11-01 put
  the signing key in the **credentials service**, so no signing key and no App key goes on the
  variant. The read-only installation token it works with is the **credentials service's** to mint,
  and it is fetched rather than mounted.
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
  gate:** no network namespace or `unshare` in Cloud Run's gen2 has been checked (**U7**), and that
  question **cannot be measured hermetically** — it is a property of the deployed execution
  environment — so **it is an owner-run live probe taken before R4 launches**, independent of R2 and
  R4 (the old form, Check 125's step 6, ran `fugaro review --mutate` and so needed both — circular);
  **Check 125 step 6 is now the confirmation after R4**, and the probe is its own step. If a child
  process cannot be fenced the answer falls back to **(b)**
  or **(c)** and the owner is asked again — the design does not assume a fence it has not seen.
- **It does not launch on a job that already exists.** The shared project job of
  `(profile, provider, account)` mounts the GitHub App key, so a read-only scope on it would be a
  comment on the launcher's intent rather than a boundary; the design adds a **review-only job variant
  and service account per account, with no App key** (**R7**), and the read-only token is **minted by
  the credentials service and fetched by the job by name** — the launcher, which holds no key, is on
  no path (**D893 (c)**).
- **It settles the verdict's provenance with a signature, and says what that does not close.**
  Ordering keeps a *coder* from forging its own review (spec §6), but another job on the same shared
  project job can write under the review run's prefix. **D892 is answered — option (a):** the
  **credentials service signs `review.json`**, holding the account's HMAC key so that it **never
  enters a container that runs untrusted code**, and **the coordinator verifies the signature before
  it acts** — a forged, replayed or unsigned verdict is refused. What it does not close is an injected
  *reviewer* writing a dishonest verdict inside its own run, which is why the verdict is still advice.
- **It does not make the read-only token's channel free — it makes it a precondition.** **D893 is
  answered — option (c):** the job fetches the token from the **credentials service, by name**, so
  nothing is handed in and no value sits in an execution object. **The service ships dormant**, so the
  cloud senior reviewer **requires it to be enabled for the installation** (`fugaro update
  --provision-credentials`, the App key in the bundle), **and requires the service to admit the review
  job's own account** — a project-scoped job, which the service refuses today (its Global Constraint
  6; **C3.9**, unscheduled, is the task that lifts it). **No `fugaro update` can supply that half**,
  so **R7, R2 and the coordinator's `ship` → merge path stay blocked** until it lands; the check that
  says so is R6's, and **E7** records the dependency.

## Related

- [docs/accounts-add/](../accounts-add/README.md) (**merged** as #1470, design only): the one command
  that makes a named account usable end to end. It is the **dependency** this design waits on, and its
  remaining tasks — the command itself — are still unbuilt.
- [docs/credentials-service/](../credentials-service/README.md) (**plan only, the service ships
  dormant**): the service **D893(c)** makes a precondition. Its mint endpoint and keyless fetch path
  are already in the tree (`internal/credsvc/github.go`, `internal/gitprov/github`'s `ServiceTokens`);
  what is missing is the **opt-in**, its **Global Constraint 6 / C3.9** admission of a
  project-scoped job, and a **read-only** set for a review identity.
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
