# The independent senior review as a cloud job

**Status: design only, nothing approved.** This is a draft pull request for the owner to read. It adds
no code, no schema, no command and no recipe; it edits no file outside `docs/` and the tests that pin
them. The owner has **answered** every decision in [reconciliation.md](reconciliation.md) §4 — D892
with option (a), D893 with option (c), and **D894 with option (a)** last — and ruled on the seven
things the review of that round raised (**O1–O7** of 2026-11-01, recorded in the same section, which
take no decision number: an answer picks an option rather than allocating one) — but **an answer is
not an approval**: no task in [implementation-plan.md](implementation-plan.md) may start before the
owner approves it, and one of them waits on a gate outside this design (**E7**, below). **U7**, the
owner-run observation of the deployed fence, **is ANSWERED (2026-11-01)** — both fences hold — so it
gates nothing **for the environment it probed**, an ordinary coding job on Cloud Run gen2: it was
never CSR4b's gate alone, and the same answer is the observation the first
slice's signed `ship` path waited on, through the routing design **#1514**'s **D1074** (its
**C3.9e**) — re-confirmed by Check 125's step 7 where **CSR7**'s review-only variant runs, if that is
somewhere else.

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
| [reconciliation.md](reconciliation.md) | Every claim of the spec against the code on `main` and against the open pull requests: findings **F1–F27**, the dependencies **E1–E7** (**E7** — the credentials service enabled and admitting a project-scoped job — is a hard precondition, not a task: it blocks CSR7, CSR2 and the coordinator's `ship` → merge path), the owner decisions **D880–D894** — **all of them answered** — D880–D891, then **D892 with option (a)** (the credentials service signs the verdict, over a framed message, O3), **D893 with option (c)** (the read-only token comes from the credentials service, by name) and **D894 with option (a)** (the reviewer's session loads no configuration from the head it reviews, and is given the base's `CLAUDE.md` and `.claude/rules` in its place, O6), and the owner's rulings **O1–O7** of 2026-11-01, which amend the decisions they touch and take no number, with **D895–D899 reserved and unused**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | The tasks **CSR1–CSR7** (at most 12 files each), the shared-files table, the first slice and the release slot. **Nothing in it is approved** |

## The shape in one paragraph

The coder's job runs the cheap loop and the in-job reviewer exactly as it does today. **It is a
coding run, so it holds whatever account it was launched on** — `fugaro run --token team-a` puts that
account's subscription token on the shared project job, which is what `agent.oauth_token` means today
(spec §7); what this design adds is a *second* job whose **model calls go through the credentials
service** instead, so **the review container holds no OAuth token at all** (**O1**, the owner's ruling
of 2026-11-01, spec §2). When the coder has pushed its branch and opened its pull request, the
**coordinator** (the owner's own session, the console, or a script) launches that second job: the
**review job**, in review mode, on a **review-only job variant that mounts no GitHub App key and no
OAuth token**, on the
account the owner named, whose forge credential is a **read-only** installation token **the credentials
service mints and the job fetches by name — nothing is handed in, and the launcher, which holds no
key, is on no path** (F16, D883, **D893 (c)**, spec §2), and whose only outputs are a run record and an
artifact under `runs/<slug>/<review-run>/`. The artifact — `review.json` with a
`schema_version` plus a human-readable `review.md` — carries the verdict `ship`, `fix first` or
`reject`, one entry per issue with a severity, a `file:line`, a class and a suggested fix, and **a
signature over its own bytes and the identity that asked for it** (**D892 (a)**, framed by **O3**),
produced by the credentials service and binding the
repository, the pull request, the head and the review run, which the coordinator verifies before it
acts: `ship`
means merge, `fix first` launches a fix round whose brief **is** the findings, and `reject` means no
fix round and a person decides. **The whole chain requires the credentials service to be enabled for
the installation, and it ships dormant** — D893(c)'s precondition, which is also what keeps CSR7, CSR2 and
the coordinator's merge path blocked (spec §2, §5; reconciliation E7).

## Numbers this work holds

| Block | State |
| --- | --- |
| **D880–D899** | **this work's block: D880–D894 written and answered — D891 by the owner with option (a), D892 with option (a) (the credentials service signs the verdict, over a framed message), D893 with option (c) (the read-only token comes from the credentials service, by name) and D894 with option (a) (the reviewer's Claude session loads no configuration from the head it reviews and is given the base's `CLAUDE.md` and `.claude/rules` instead, **O6**) — with D895–D899 reserved and unused.** D892, D893 and D894 were each raised by the review of the round before, and the owner answered all three; the seven rulings of the same day (**O1–O7**, §4) amend the decisions they touch and **take no number of their own**, because an answer picks an option rather than allocating one. **D893(c) is a precondition of the whole design, not a channel**, so it is a precondition (**E7**) rather than a settled question: see [spec.md](spec.md) §2 and **E7** in [reconciliation.md](reconciliation.md) §5. Taken above every number on `main`, whose highest written is now **D872** — the tail of the `accounts add` design's D860–D872, which **merged** as #1470 and reserved **D873–D879** unused, so **D880 is still the next free number** and this block is unchanged — and above every open pull request's numbers, none of which reserves a decision above D872. The reading was taken on 2026-10-31 against `origin/main` at `48754c01` and re-read against the merged accounts design at `d558dace`; it is re-checked at launch, and the block recorded again if it moved |
| **O1–O7** | **the owner's rulings of 2026-11-01, recorded in [reconciliation.md](reconciliation.md) §4 and taking no decision number** — an answer picks an option rather than allocating one, which is the convention the coordinator design's own reconciliation states. They amend the decisions they touch: **O1** the reviewer's model calls route through the credentials service's tier A for their own account, so the review container holds no OAuth token; **O2** the new command supersedes the in-job-review design's Q16; **O3** the signature is an HMAC over a framed message — the digest plus the verified `fr`/`fs` — one per review run; **O4** the record's `reviews[].verdict` keeps `ship\|changes` and gains the display-only `independent` tier, and a failed sign call fails the run; **O5** `review.md` is never posted verbatim; **O6** the base's `CLAUDE.md` and `.claude/rules` are what the reviewer is given; **O7** the release slot is 0.16.0 or later, gated on C3.9. **D895–D899 stay reserved and unused** |
| **Check 125** | **claimed here, and not written in the register.** The register's own sentence is *"Check 124 is the Cloud Batch backend's own, and it is the next number free above 123"*: it reads **124 as a number the Cloud Batch backend took**, in four written parts (**124a–124d**), not as one still free — so the highest number in use is **124**, and the number free above it, which this work claims for the owner-run live check in [spec.md](spec.md) §8, is **125**. The register's own file is another work stream's (`docs/gcp-live-checklist.md` is not this task's to touch), so the row and the `## Check 125` section are written by the task that implements the check, under the number free at **that** merge — the register's rule that whichever of two open claims merges second renumbers itself. A reserved number is never reused and never renumbered |

## What this design does not do

- It adds no code, no schema and no command. Every command below is a proposal.
- It does **not** lift D9's hold on the OAuth reviewer stage
  ([docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md), the
  `agent.stage_auth` key). Its own recommendation is that the hold **stays**: this design needs no
  `stage_auth`, because its reviewer is a separate job execution whose credential the coder's job
  never holds. See [spec.md](spec.md) §4 and decision **D881**.
- It invents no credential, and the review container holds none of the person's. The named account is
  the one `fugaro accounts add` stores; the review job's **model calls route through the credentials
  service's tier A on `/v1/messages`, admitted for that account alone** (**O1**), so the variant mounts
  **no OAuth token and no signing key** — the owner's ruling of 2026-11-01 put the signing key in the
  **credentials service**, and O1 took the token out of the container altogether. The read-only
  installation token it works with is the **credentials service's** to mint, and it is fetched rather
  than mounted. If C3.9 slips past the point where the command must ship, the fallback is the token
  mount this design first proposed — the variant's `secret_env` carrying the account's Claude token —
  which is strictly worse and is recorded as the fallback rather than the plan (spec §2, §7).
- It changes no existing behaviour, **with the one exception D894(a) names**: an installation that
  launches no review job sees the tree it has today, but **CSR4a**'s configuration posture tightens the
  `review` stage itself — the same stage `fugaro run --pr N` runs, so **review mode, which exists
  today, gets the tightening too** — and the stage stops loading the reviewed head's `CLAUDE.md` and
  `.claude/rules` while being **given the base's copies in their place** (**O6**), launched under a
  settings posture that refuses project hooks and project MCP servers outright instead of relying on
  the checkout strip alone. That is the same untrusted-head threat the strip already answers for
  `.claude/` and `.mcp.json`, applied to the files it left behind (reconciliation §4, D894).
- It writes no pull request and no comment of its own on any path: the review job's verdict is an
  artifact, and the coordinator decides what to say.
- **It does not claim the review job is contained.** The reviewer's mutation check runs repository
  code in the container, and **O1 is what keeps the person's model credential out of it**: with the
  calls going through the credentials service there is no OAuth token in the container to read.
  **D891 is answered — option (a):** the mutation
  test runs in a subprocess with **the token scrubbed from its environment and egress denied**, and
  **Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` are denied for the rest of the review stage**,
  the mutant being applied and reverted by the runner rather than by the agent's shell. What is left is
  the `/proc` read from the agent's own stage — the fenced mutant's child no longer reads the parent's
  `/proc` — which §7 states as the residual and which only the separate container reduces.
  **The mutation half is CSR4b, and the probe it waited on has returned:** no network namespace or
  `unshare` in Cloud Run's gen2 had been checked (**U7**), and that
  question could **not be measured hermetically** — it is a property of the deployed execution
  environment — so it was an owner-run live probe, independent of CSR2
  (the old form, Check 125's step 6, ran `fugaro review --mutate` and so needed both — circular).
  **U7 is ANSWERED (2026-11-01): both fences — the network namespace and a private `/proc` — are
  available and hold**, measured on the sandbox installation with the unfenced exposure recorded
  beside them; its five caveats are [reconciliation.md](reconciliation.md) §6's, and the first is a
  requirement on CSR4b. **Check 125 step 7 stays the confirmation after CSR4b**, on the path that
  actually uses the fence. Had a child been unfenceable, the answer would have fallen back to **(b)**
  or **(c)** and the owner been asked again — the design does not assume a fence it has not seen.
  **The settings posture is CSR4a, and it ships first**: it is standalone hardening of a stage that
  exists today and waits on no probe.
- **It does not launch on a job that already exists.** The shared project job of
  `(profile, provider, account)` mounts the GitHub App key, so a read-only scope on it would be a
  comment on the launcher's intent rather than a boundary; the design adds a **review-only job variant
  and service account per account, with no App key** (**CSR7**), and the read-only token is **minted by
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
  so **CSR7, CSR2 and the coordinator's `ship` → merge path stay blocked** until it lands; the check that
  says so is CSR6's, and **E7** records the dependency.
- **The first slice cannot run until another work stream lands, and that is the critical path.** The
  first slice is **E1 + CSR1 + CSR7 + CSR2** — `fugaro accounts add` (its own plan's **A1**, the local
  config edit, then **A2**, the converge half, and **A3**, `--claude`) plus the review job and the
  command that launches it, and the shared write helper the converge half uses is the settings
  design's **S5** (`internal/cli/settings_write.go`) — and **every part of it needs the credentials
  service**: the service admitting a
  **project-scoped review identity**, the **read-only set** its mint must serve for that identity, the
  **review class on `/v1/messages`** O1 routes the reviewer's calls through, and
  **D892(a)'s sign and verify endpoints**. That is **C3.9** of the credentials service's own Slice 3,
  **unscheduled** — and split by the credentials-service routing design **#1514** into
  **C3.9a–C3.9g**, which is the form this design names because that pull request is where the split is
  written. Until it lands, **0.16.0 ships the command and the job variant and neither can run**: the
  launch is refused by the service, and **no `fugaro update` and no local step shortens the wait**
  (E7; spec §2, §5).

## Related

- [docs/accounts-add/](../accounts-add/README.md) (**merged** as #1470, design only): the one command
  that makes a named account usable end to end. It is the **dependency** this design waits on, and its
  remaining tasks — the command itself — are still unbuilt.
- [docs/credentials-service/](../credentials-service/README.md) (**plan only, the service ships
  dormant**): the service **D893(c)** makes a precondition. Its mint endpoint and keyless fetch path
  are already in the tree (`internal/credsvc/github.go`, `internal/gitprov/github`'s `ServiceTokens`);
  what is missing is the **opt-in**, its **Global Constraint 6 / C3.9** admission of a
  project-scoped job, a **read-only** set for a review identity, and the **review class on
  `/v1/messages`** that **O1** routes the reviewer's model calls through — whose decision number
  (**D1063**) and whose split of C3.9 into **C3.9a–C3.9g** live in the credentials-service routing
  design **#1514**.
- [docs/project-claude-accounts.md](../project-claude-accounts.md): where the account, its secret,
  its job and its `allow_data_to` are described today.
- [docs/design/mixed-credentials.md](../design/mixed-credentials.md) §6: the four mitigations, of
  which **mitigation 4 — a separate job execution for the reviewer — is the only one that closes the
  `/proc` and metadata-server gap**, and is what this design builds.
- [docs/in-job-review/](../in-job-review/README.md): the owner's spec for moving review into the
  coding job, and its reconciliation. This design is the complement: review **outside** the coding
  job — and **O2 supersedes that reconciliation's Q16**, which answered "extend `review-only` rather
  than add a second entry point", because a recipe cannot change a job's credential and so no
  extension of `review-only` could put the reviewer on a named account in a container the coder never
  ran in. (That document is another work stream's and this round does not edit it; the supersession is
  recorded here and in [spec.md](spec.md) §5, and the stale `fugaro review` spellings in the
  task-governance design's D320/D327 rows are corrected in that design's own reconciliation.)
- [docs/dogfooding.md](../dogfooding.md): the recorded lesson this design exists for — *"the in-loop
  reviewer is a different, stronger model than the coder, but it is still not independent review"*.
- [docs/coordinator/](../coordinator/README.md): the service that would launch the review job and act
  on its verdict.
- [docs/gcp-live-checklist.md](../gcp-live-checklist.md): the register the live check's row and
  section belong to.
