# The independent senior review as a cloud job: reconciliation

**How this was checked.** Every claim below was located and read on **`origin/main` at `48754c01`**
(2026-10-31), and then against the **19 pull requests open on 2026-10-31**, named in §2. Paths and Go
symbols were re-read in place; a line number appears only beside a quoted comment or as a hint for a
call site whose symbol is named with it, because the commit in hand is what binds and a line moves,
and every file path named here exists on that commit. (An earlier draft of this preamble said line
numbers were never cited, which its own findings contradicted from **F23** on; the symbol is what a
reader should check, the number is where it was.) This document is the pin: where it disagrees with
[spec.md](spec.md), **it wins**.

**Owner:** the owner of this installation. **Date:** 2026-10-31.

**Written before one of its answers, and dated so a reader is not misled:** §6's **U7** was settled by
an observation the owner ran on **2026-11-01**, so that row carries the answer where the rest of this
document records the question, and [spec.md](spec.md) §7, spec §8's step 6 and CSR4b's block in the
plan carry it too.

---

## 1. The one sentence the rest of this document serves

The **independent review** is run today on the owner's own machine, by hand, and it is the review
that actually decides whether a pull request is good ([docs/dogfooding.md](../dogfooding.md): *"keep
the real review outside the loop … a run's own `ship` never replaces it"*). Moving it into the cloud
needs two things that do not exist: **a named Claude account usable end to end** (the
[docs/accounts-add/](../accounts-add/README.md) design, merged as PR #1470 — designed, not built) and
**a job that is not the coder's job**, because the whole reason the in-job reviewer is not the
independent review is that it shares the loop's context, its family and its container. This design is
the second half.

## 2. What was reconciled against

`origin/main` at `48754c01`. The open pull requests read for this section, with the ones that matter
to this design. **#1470 has since merged** (`d558dace`), and the rows stay because the reading was
taken while it was open; what changed about it is only that it is on `main` now:

| PR | What it holds | Relevance here |
| --- | --- | --- |
| **#1470** (**merged as `d558dace`**) | `docs/accounts-add/`: `fugaro accounts add`/`rm`, D860–D879 (D860–D872 written) | **E1, the dependency.** The named account end to end |
| **#1467** | `internal/yamledit`: a comment-preserving YAML editor | **E2.** What `accounts add` edits the local config with |
| **#1464** (merged as `48754c01`) | `docs/settings-commands/`: `fugaro settings`, the update publish stages, D840–D859 | **E3.** The publish path `accounts add` reuses |
| **#1463** | `docs/rigor-command/`: `fugaro rigor show/set/unset`, D830–D839 | The owner-side story of the review level a reviewer runs at |
| **#1424** | `task: the review_levels field (review levels L1.1)`, D724 | **E5/E6.** The effort key and the version-gate precedent |
| **#1407** | `recipe: the adversarial step (review level 3)` | The security-sensitive level this design's checklist overlaps |
| **#1468** | `upgrade: the rules step, and the docs list update's steps` | The publish/deploy staging `accounts add` converges |
| **#1418, #1416, #1415, #1411, #1407, #1405, #1425, #1420, #1449, #1377, #907, #715, #620** | other work in flight | Read for number collisions and for paths this design would otherwise claim |

**Number reading, recorded because the README's block is a claim about it:** the highest decision
number **written** on `main` is now **D872**, the tail of the `accounts add` design's **D860–D872**
(merged as #1470), which reserved **D873–D879** and left them unused; below it sit the `settings`
design's D840–D859 (merged as #1464). No open pull request reserves a decision above D872. **D880 is
therefore still the next free number**, and this work's block is **D880–D899**, of
which **D880–D894 are written and answered** — D891 by the owner with option (a), **D892 with option
(a)**, **D893 with option (c)** and **D894 with option (a)** — and **D895–D899 are reserved and
unused**. The owner's seven rulings of 2026-11-01 (**O1–O7**, §4) amend the decisions they touch and
**take no decision number of their own**, because an answer picks an option rather than allocating
one — the convention this work's own §4 states, borrowed from the coordinator design's reconciliation.
`D800` in `docs/` is the false positive the
`docs/models-commands/` README records (`U+D800` inside a quoted value), not a decision. The check
register reads **124 as the next free number above 123** and the Cloud Batch backend took it, so this
work claims **Check 125**.

## 3. Findings

Each is classified **Exists** (the repository already does this, with the path), **Gap** (it does
not), **Rule** (it conflicts with a rule the repository states) or **Open** (a question only the
owner can settle — see §4).

### The stage, the recipe and the credential

**F1. `Exists` — review mode is real, and it is review *inside the coder's job*.**
`internal/recipe/catalog/review-only.yaml` is `mode: review` with one `review` step and
`max_rounds: 1`; `fugaro run --pr N --recipe review-only` resolves the pull request's head
(`internal/runner/reviewmode.go`'s `checkReviewPRs` → `FetchPRHead`), checks it out and reviews it,
gated by the trusted-author rule (`checkReviewAuthor`: OWNER, MEMBER or COLLABORATOR only, a fork
refused unless its owner is that author, and Bitbucket failing closed). **Consequence:** half of §2's
job already exists; what does not exist is a way to run it **on another account in another
container**.

**F2. `Gap` — the review job's forge token would be a write token, because review mode posts a
comment.** `postReviewComments` (`internal/runner/reviewmode.go`) calls `r.provider.Comment` and
records `runstore.ReviewedPR{Number, SHA, Verdict}`; the run's token asks for
`{contents: write, pull_requests: write, issues: read, metadata: read}`
(`internal/gitprov/github/apptoken.go`'s `tokenPermissions`, exported as `RunTokenPermissions`).
**Consequence:** a review job that reuses today's path holds a write token it does not need. The
read-only sets exist but none is this one: `buildTokenPermissions` (`{contents: read, metadata:
read}`, exported as `BuildTokenPermissions`, used by `internal/cli/gitcred.go` and
`internal/cli/imagecheck.go`); the challenger arm's `shadowTokenPermissions` (`{contents: read,
metadata: read}`, `apptoken.go:111`); and the coordinator's `coordinatorTokenPermissions`, exported as
`CoordinatorTokenPermissions` (`internal/gitprov/github/apptoken.go:128` is the map, `:142` the
exported accessor that clones it — **in code, not in a design
document**, which an earlier draft of this finding got wrong by citing
`docs/coordinator/reconciliation.md`). §2's set is a fifth, and new work.

**F3. `Rule` — a permission set is not a boundary, because the job holds the App key, and the job
this design first proposed *is* that job.** `internal/runner/runner.go`'s `serviceTokens` mints
through the credentials service where the installation has one, and otherwise the job's own
environment holds `providers.EnvGitHubAppID` and the App's private key as a mounted secret
(`internal/cli/gitcred.go`'s `gitCredAppKeyEnv`). A key can mint **any** subset the App holds.

The part an earlier draft got wrong: **the shared project job of `(profile, provider, account)`
mounts that key**, so "the review job mounts no App key" was false of the job the design proposed to
launch on. `deploy/terraform/gcp/roots/installation/tests/project_job.tftest.hcl`'s
`shared_job_mounting_the_app_key_keeps_the_accessor` pins it twice — `secret_env` carries
`FUGARO_GITHUB_APP_PRIVATE_KEY` from the installation's project-wide copy at version `latest`, and the
job's own service account holds `roles/secretmanager.secretAccessor` on it (the retention the run
records as open, D131/C3.9) — and `docs/accounts-add/` (PR #1470) deploys exactly that job for a named
account (its `+ standard/github/team-a` spelling), as does `fugaro run --token team-a`.

**Consequence:** the read-only scope of §2 needs a **job that does not exist yet** — a per-account
review-only job variant and service account whose `secret_env` names no `github-app-key` (and, under
**O1**, holds **no secret at all** — no OAuth token either), and whose account holds no accessor on
that key. That is new infra, not a
permission string, and [implementation-plan.md](implementation-plan.md) carries it as **CSR7**.

**F4. `Exists` — the account selection rule this design reuses.** `internal/cli/run.go`'s
`claudeAccountFor`: the `--token` flag, then `agent.oauth_token` at the ref the run checks out, then
the checkout's own, then `default`; judged by `claudeAccountAllowed` against
`config.AllowedClaudeAccounts` (which is `{default}` plus every `claude_accounts` name whose
`allow_data_to` lists the repository). **Consequence:** §5's `fugaro review --token` inherits a rule
with tests rather than inventing a second one.

**F5. `Gap` — `fugaro accounts add` and `fugaro accounts rm` do not exist.** `internal/cli/accounts.go`
has exactly two subcommands, `ls` (names, state and the reason for it) and `clear NAME` (ends a
broken mark after a token is replaced). The one command that stores a named token, declares the
account, adds it to a profile and converges the policy and the job set is **E1**, designed in PR #1470
(merged) and not yet built.
**Consequence:** the first slice of this design cannot be the review job alone; it is **E1 plus the
review job**, which is what [implementation-plan.md](implementation-plan.md) says.

**F6. `Exists` — the mark, and `Gap` — the cascade. `fugaro accounts ls` reads
`creds/marks/<name>` and reports `available`, `exhausted` (with a reset time), `backed-off` or
`broken` (`internal/cli/accounts.go`, `internal/creds`'s `state.go`); the cascade itself — tier A the
subscription tokens, tier B the paid key, cheapest-usable-first, with per-run stickiness and a lease
— is `internal/creds/select.go`, and it belongs to the **optional credentials service**. Neither
`fugaro run` nor this design's launch selects through it today; the failover the owner knows is the
**skill's**, and `project-centric.md` §7's ruling ("the launcher's own accounts first, then shared
ones") is a plan. **Consequence:** §1's "failover order" is the *selection* order (F4) plus the
**mark as a refusal**, not a retry loop; a retry loop is its own design and decision **D882**.

### The model, the effort, the cap and the cost

**F7. `Exists` — the reviewer's model and output cap.** `config.Agent.ModelFor(RoleReviewer)`
(`agent.models.reviewer`, falling back to `agent.model`) and `agent.max_output_tokens.reviewer`
(`RoleTokens`, `0` meaning no limit of Fugaro's). The role's figure reaches Claude Code as
`CLAUDE_CODE_MAX_OUTPUT_TOKENS` and the **gateway refuses a request whose `max_tokens` is above it**,
recorded as a violation that fails the stage ([docs/design/v1.md](../design/v1.md) §5's pins).
**Consequence:** a reviewer that must emit a long structured artifact is silently truncated by a cap
sized for conversation — the lesson §1 carries, and decision **D886**.

**F8. `Gap` — there is no effort key.** `internal/config` has none; the per-process mechanism is
`CLAUDE_CODE_EFFORT_LEVEL` with the closed set `low, medium, high, xhigh, max`, measured on
2026-10-30 against the pinned Claude Code ([docs/multi-model.md](../multi-model.md), "How the pinned
Claude Code takes an effort setting"). The key that would set it is review level L1.1/L4.3
([docs/review-levels-ab-jobmax/](../review-levels-ab-jobmax/implementation-plan.md)), in **E5**.
**Consequence:** §1's "which effort" is *the model's own default* today, and this design **records
the effort it set** rather than reading it back, because the level does not appear in the stream
(measured: the `init` event carries only `per_turn_effort_active`).

**F9. `Exists` — a subscription stage is not priced by the gateway, and the notional machinery that
would record it is not in the tree.** For `auth: oauth` the gateway is off, `runstore.ModelBasis` is
`subscription`, and `cost.model_usd` carries Claude Code's own estimate; the four fields that would
split a run's billed dollars from a subscription stage's — `runstore.StageTiming.Credential`,
`Cost.ModelNotionalUSD` and the constructors' notional argument — are the **held** plan's Task 3 and
do not exist on `main`. **Consequence:** the review job's record is honest today (a whole run on one
subscription credential reads as `subscription`), and this design needs **no** part of the held plan
for it. It must not claim the notional fields exist.

**F10. `Exists` — `agent.max_budget_usd` is the only bound on a subscription stage, and it is Claude
Code's own estimate** (pass `--max-budget-usd`), accurate for a Claude model and about twenty-five
times too high for a provider model ([docs/multi-model.md](../multi-model.md),
[docs/design/mixed-credentials.md](../design/mixed-credentials.md) §5). **Consequence:** the review
job's cost ceiling is that estimate and **not** `budget.per_run_usd`, which binds nothing on a
subscription credential. The live check's step 5 observes exactly this.

### The output contract

**F11. `Exists` — the in-run verdict is two words and refuses a third, on purpose.**
`internal/runner/verdict.go`'s `decodeVerdict` returns an error for anything but `ship` and
`changes`, with the reason written in the file (*"every reader of a verdict compares it against those
two, so a third word would be a run that shipped nothing and said so"*), and `ParseVerdict` turns an
unreadable result into a **blocking** finding rather than a pass. **Consequence:** §3's third outcome
(`reject`) **cannot** be expressed in that format without changing what a run reports; the
independent review's artifact is a new versioned shape, decision **D884**.

**F12. `Exists`/`Rule` — the finding shape exists twice, and neither carries a fix or a line.**
`runner.VerdictSchema`/`runner.Finding` carry `severity` (free-form), the three anchored axes
`likelihood`, `impact`, `fix_cost`, the closed `class` set (`correctness`, `performance`, `security`,
`data_loss`, `maintainability`, `cosmetic`), `file` and `summary`. `runstore.ToleratedFinding` is a
second, narrower copy for what a run's mode let through. **Consequence:** §3's issue is the existing
shape **plus** `line`, `fix` and `evidence`; the design reuses the class vocabulary rather than
inventing one, and it must say whether the ancillary axes are carried (**D884**'s sub-question).

**F13. `Exists` — artifacts have a path, a staging pass and a record.** `internal/runner/artifact.go`
stages under `<StateDir>/artifacts/` and uploads to `runs/<slug>/<run>/artifacts/`; transcripts are
uploaded as `transcripts/<stage>-<n>.jsonl` (`internal/runner/runner.go`). **Consequence:** §3's
`review.json` and `review.md` ride the existing path, and the transcript's redaction applies to a
review job's transcript too (§7).

### The job, the bucket and the launch

**F14. `Exists` — the job axis is `(profile, provider, account)`, and that axis is not yet the review
job's.** `gcp.ProjectJobName` builds it, `config.ProjectJobAccounts` is the one function the three
sides of a project job compute a profile's job set with, `internal/runner` recomputes the candidates it
matches `CLOUD_RUN_JOB` against, the launcher picks the entry a launch targets, and
`internal/infra/projectjobs.go`'s `ProjectJob` (with its `SecretEnv` map and its
`ServiceAccount`) is the shape the installation root deploys. **Consequence, corrected:** an earlier
draft said `fugaro review --token NAME` "needs no new job naming, only the right entry" — **that was
wrong**, because the entry it would pick is the shared project job, which mounts the App key (**F3**).
The axis exists and the machinery to add a job to it exists (`config.ProjectJobAccounts` is where a
profile's job set is computed, and `fugaro doctor`'s census reads it); what does not exist is the
**review-only variant** — a second entry per `(profile, provider, account)`, its own service account,
no App key. Naming, deploying and census-reading that variant is **CSR7**.

**F15. `Rule` — a shared project job's bucket reach is installation-wide, and the accepted ruling
says so.** `docs/design/bucket-iam.md` §2.1 and §10: a project job account holds
`roles/storage.objectUser` conditioned on the whole of `runs/`, `cache/` and `locks/` (one clause per
prefix, **no slug**, `gcp.ProjectBucketCondition`), and the owner ruled on 2026-10-17 to **accept**
that reach — a profile is a secret boundary, not a bucket one. **Consequence:** a review job that
runs on a shared job can write any run's node, so §6's integrity argument is the **ordering** (the
review run's id is minted after the coder's job ended) and **not** a boundary — which is exactly the
gap **D892** was raised for, and its answered **(a)** is what closes it for the coordinator: a
signature over the artifact, verified before a verdict is acted on. §6 says this, and this finding is
why it must keep saying it.

**F16. `Rule` — the minting precedent an earlier draft leaned on is not one, because every mint in
this tree happens where the App key is mounted.** `internal/cli/gitcred.go:92`'s `mintBuildToken`
reads `FUGARO_GITHUB_APP_ID`/`FUGARO_GITHUB_APP_PRIVATE_KEY` **from its own environment** and is
called by the build it serves; `internal/cli/imagecheck.go:530`'s `jobGitEnv` does the same inside a
check job. `github.BuildTokenPermissions` is the exported spelling of the set, not a way to mint
without a key. **A launcher on the owner's laptop holds no App key and must not be given one.**
**Consequence:** F3's fix needs a **mint site**, and the owner's **D893** answer is **(c)**: the
**credentials service** mints it — its bundle already holds the App key for exactly this, and the
keyless path (`github.ServiceTokens`, `POST /v1/github/installation-token`) is already in the tree.
**There is no hand-in to design:** nothing is handed to the job, so neither the launch's execution
override (`internal/backend/gcp/run.go:169`, `ContainerOverrides[].Env`, whose values are plain
values in the execution object) nor a per-launch secret is on any path. The cost of (c) is a
**precondition rather than a channel** — the service must be enabled, must admit a project-scoped job,
must mint the read-only set and must **sign the artifact**, since D892(a)'s signer is the service's
(the owner's ruling of 2026-11-01) — and D893's own entry in §4 states it; the row that used to hold
this question, **UNVERIFIED-2** in §6, is closed by that answer.

**F17. `Gap` — the coding run has no way to say "review me after I finish".** Nothing in
`internal/runner` launches a second job; a run's last act is its pull request
(`internal/runner/prflow.go`). **Consequence:** §5's "the trigger is the coordinator's" is a real
gap, not a preference: something outside the run has to launch the review, and this design chooses
the CLI command as the thing that something calls.

### Security and the downstream consumers

**F18. `Exists` — the redaction and name-only rules this design reuses.** `internal/agent/redact.go`
redacts anything published from agent output; `internal/runner`'s `r.addSecret` registry is how a
value is kept out of the log and the transcript; `gitops.ModelCredentialVars`
(`internal/gitops/gitops.go`) already lists `CLAUDE_CODE_OAUTH_TOKEN` for the git side; and
`internal/creds`'s `Credential` carries **a name and never a value**, which `accounts ls` and the
audit rule depend on. **Consequence:** §7's "never capture the token" is a rule the tree already
enforces on the paths that exist, and the review job's transcript and the dormant crash reporter are
the two new places to hold it.

**F19. `Exists` — the reviewers-are-read-only rule is stated and is not yet true of the *job*.** The
in-job spec's §5 says *"reviewers are read-only. They comment; they never edit code"*, and
`SECURITY.md` carries why a verdict is advice rather than evidence. Today the review **stage** is
read-only and the review **run** is not: it posts a comment and it pushes nothing only because a
review-mode run has nothing to push. **Consequence:** §2's read-only scope turns a prompt rule into a
permission, and §6's "the review job itself changes nothing" becomes true rather than conventional.

**F20. `Gap` — a fix round cannot be handed findings today.** `fugaro run --pr N` is a follow-up
whose brief is written by the launcher; `internal/runner/prompts.go` already tells a fix stage to fix
what the findings name, but there is **no path from a stored review artifact to a launched run's
brief**. The pieces exist — `runstore.ToleratedFindings` is the precedent for a bounded, quoted list
of findings on a record — and the wire is not built. **Consequence:** §6's "no human copies them" is
new work, and it is where the untrusted-input rule has to be applied to reviewer output (decision
**D887**).

**F21. `Open` — the crash reporter is dormant and its gate is the place the token must not leak.**
The Sentry work is T2.1 of the console plan (the console crash reporter is gated, PR #1449), and the
rigor plan's §4 fixes its posture: *"no breadcrumbs, no local variables, no default PII, usernames
stripped from paths"*. **Consequence:** nothing to change today, and one sentence this design owes:
an environment-derived secret is exactly the class that gate is written for, so it is held to it
before the reporter wakes.

**F22. `Open` — the command's reference row is another work stream's file.**
`website/content/cli-reference.md` is generated (`ee/clidoc`'s `TestCliReferenceIsCurrent`) and
`website/**` is named in the 0.15.0 rulings as not this kind of task's to touch. **Consequence:** the
task that adds `fugaro review` regenerates the page and, if that is not allowed in its scope, says so
in its own pull request rather than hand-editing the file (**E4**).

**F23. `Exists` — the `review` stage's result is parsed by the in-run reader, so a review that writes
only an artifact has no verdict at all.** `internal/runner/reviewmode.go:444` passes
`runner.VerdictSchema` as the stage's `--json-schema` and `:465` reads the result with
`runner.ParseVerdict`. **The coding run's review rounds are no longer that path, and the earlier
draft's citation of `internal/runner/runner.go:2674`/`:2752` for them was wrong twice over — the lines moved and the
reader changed:** `seniorLoop` (`internal/runner/runner.go:2675`) runs its rounds through
`seniorRound` (`:2728`), which reads with **`TryVerdict`** (`:2746`, and the fix-line retry at
`:2810`) — a round whose answer carries no parseable verdict is **asked once more**
(`noVerdictRetries`, `:2715`) and then **let through**, so a reviewer that answers nothing does not
fail a coding run (the reader's own comment, `internal/runner/verdict.go:56`, says which callers are
left). **Review mode is what is left**, and that is why F23 holds: `reviewmode.go:465` and diagnose
(`internal/cli/diagnose.go:246`) are `ParseVerdict`'s only callers, and review mode is the one that
must have an answer.
`VerdictSchema`'s `verdict` enum is `ship|changes` and its `required` is `["verdict","findings"]`
(`internal/runner/verdict.go:18`), and `ParseVerdict` answers a result it cannot read with a finding of
its own — `severity: "blocker"`, `summary: "review produced no parseable verdict: " + why`, the last
`why` being *"nothing in the result was a verdict"* (`:64` is the `blocker` literal, `:99` the `why`
text; an earlier draft cited `:58`/`:93`, which two later edits moved, and before that `:53`/`:46`,
the two function headers). **Consequence:** `spec.md` §3's
review job writes `review.json`, and if its stage result carried the findings only in the file, the
stage's reader would find no verdict and the run's outcome would be decided by a sentence about the
parser — a *blocker* finding nobody wrote, stored beside real ones, for a review that may have been
complete and correct. The two formats' separation is therefore not only about readers: the review job
must also pass **its own** schema to its own stage and read it with **its own** decoder, and an
unreadable artifact must end the run **invalid** (`endReviewInvalidVerdict`,
`internal/runner/reviewmode.go:815`) rather than fabricate a finding (**D890**).

**F24. `Rule` — the review job runs the repository's code while it holds the token, so §2's and §7's
"never runs repository code" / "nothing to steal" were false.** `internal/agent/settings.go:63`'s
`DenyRules` denies exactly `WebSearch`, `WebFetch` (`:31`) and the review stage's binary reads
(`:53`) — Claude Code's **tools**, not the shell: the reviewer drove the stage with Bash, and D885's
mutation check runs a test from the head **by design**. And `denyWeb` does not stop `curl` from Bash
against a job's default egress, so a token in the stage's environment (`CLAUDE_CODE_OAUTH_TOKEN`, a
mounted secret env var) is reachable from the reviewed diff three ways: the environment, `/proc`, and
the network. **Consequence:** the design states the residual instead of claiming a mitigation it has
not built, and **D891 is answered — option (a)**: the mutant runs in a subprocess with the token
scrubbed and egress denied, and **Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` are denied for
the rest of the review stage** — the agent's shell stops being a general exfiltration path and it
cannot rewrite the tree it is being checked against — which leaves the `/proc` read as the residual.
**O1 removes the token from that environment altogether** (the reviewer's model calls go through the
credentials service), so the residual shrinks to the review job's own one-hour, read-only installation
token; U7's probe asked the `/proc` half as well as the egress half, and §6 records its answer.

**F25. `Rule` — the artifact is the one thing a review job produces that leaves the run, and the
redaction rule covers the transcript, not it.** `internal/agent/redact.go` redacts what is published
from agent output, and the transcript artifact rides that rule (F18); §3 once let a coordinator or a
person paste `review.md` into a comment or a description. Reviewer prose generated from an untrusted
diff is exactly the sort of text the redaction exists for — and **`review.json` carries the same
prose** (`overview`, `strengths`, `issues[].summary`/`.fix`/`.evidence`, `security`, `performance`,
`test_coverage`), so it is not only the markdown. **Consequence:** §3 names **both** artifacts as
something the writer passes through the same pass **before the digest is taken**, and CSR3 has to make
that true rather than assume it. **The order is a rule, not a preference:** D892(a) signs the
artifact's exact bytes, so a redaction applied at publish time would change the bytes that were signed
and break every signature over them; the redacted bytes are the signed bytes. **And redaction is a
literal-value match** (`internal/agent/redact.go`'s registered forms: the raw value, its JSON-escaped
form, a multi-line secret's own lines, and the encodings `encodedForms` lists) — §7 states what that
does not reach. **And O5 answers what the redaction does not make safe: `review.md` is never posted
verbatim** — a coordinator that has something to say derives a **bounded, re-redacted summary** of its
own — **and never on a public repository** at all. Redaction is what makes the artifact safe to *store*
and to *sign*; it is not what makes it safe to *publish*, and the ruling keeps the two apart.

**F26. `Rule` — `accounts rm` is not revocation. It said so in this pull request's *first* draft, and
the current spec.md no longer does.** What
`fugaro accounts rm` (E1, PR #1470) removes is the local record and the installation's copy of the
secret; an OAuth token that has already left a container is revoked at the **provider**, in the
person's own account, and no command of this CLI can reach it. The accounts design's rotation path is
**re-running `add`**, and `accounts ls`'s mark is how a dead token is seen. **This finding is the rule
and not a live contradiction:** the draft this pull request opened with said the owner "can revoke it
(`fugaro accounts rm`…)", the fix round of commit `6753d669` corrected §7's bullet to what is written
above, and there is nothing left in `spec.md` for a reader to reconcile — the finding is here so the
correction is not undone by a later edit, and so the *reason* it was wrong is on the record.
**Consequence for the design:** §7 says the token's blast radius is one person's subscription until
*they* rotate it, and names rotation as the only remedy the design has.

**F27. `Gap` — a subscription hides the cost of the fix ↔ review loop, and until #1485 nothing bounded
it.** `agent.max_budget_usd` reaches Claude Code as `--max-budget-usd` and is an estimate
([docs/multi-model.md](../multi-model.md)), not enforcement; a review job is not gateway-priced at all
(F9); what bounds a run is a wall clock (the profile's `timeout_class` → the job's `task_timeout_s`,
and the launch's `Overrides.Timeout` from `timeouts.total`). §6's "the coordinator then asks for
another review" is a loop, and this design still has **no counter of its own** — but **the counter now
exists outside it, and this design joins it: #1485 is merged (D1058), so a review job on PR N joins
the pull request's chain by its PR link and the chain's `--max-review-rounds` counts senior-review
iterations** ([docs/run-limit-carry-over/](../run-limit-carry-over/spec.md) §8, its reconciliation
F16). What that costs this design is §3's rule that the review job's record carries the **PR link** and
**exactly one `reviews[]` entry**, because a chain counts rounds as the `reviews[]` entries the runner
writes (`internal/runstore/runstore.go:653`, the struct at `:145`). **Consequence, and it is now
narrower than the finding's first form:** §7 states the bounds it has — the chain's rounds **when a
chain is set at all**, which is opt-in, the wall clock, and the owner's own weekly and five-hour
windows, which a named account shares with their interactive work — and the one it does not: the
**money**. A subscription's calls never reach the chain's ledger, so `--max-usd` does not bound a
review job; the rounds cap is what does, and it is a **count read off the run records** that the
carry-over design's own §4 calls **advisory** (no rule enforces it, chain-scoped or per run), so it is
a bound an honest coordinator honours rather than one the database stops it at.

## 4. Owner decisions

Recommendation first in each, with the cost of the alternative. **D880–D894 are this work's block;
D895–D899 are reserved and unused.** **All of them are answered, and the last of them is D894.** D891
was answered first, with option (a); the review of this branch's fix round then raised **D892** and
**D893** — whether a coordinator may **act** on a verdict at all, and how the review job's read-only
token reaches it — and the owner answered both: **D892 with option (a)** — the verdict is signed, and
the owner's ruling of 2026-11-01 moved **who signs**: the **credentials service** produces the
signature, so the signing key never enters a container that runs untrusted code — and **D893 with
option (c)** (the token comes from the credentials service, by name — not the per-launch secret this
design recommended). **D894 was raised after them**, by the review of the round that answered them: the
cloud reviewer reads a branch it must not trust, and the design had never said which configuration its
Claude session loads — **the owner answered it with option (a)**, so the head is diff content and never
configuration, and **O6** fixes what the reviewer is given in its place.

**The seven rulings of 2026-11-01 (O1–O7) take no decision number.** They are recorded here, in the
entries they amend, and in the README's own row; an answer picks an option rather than allocating a
number, which is the convention this section borrows from the coordinator design's reconciliation. In
one place: **O1** routes the reviewer's model calls through the credentials service's tier A, so the
review container holds no OAuth token and needs no `routing.claude: {via: cascade}` (it amends #1514's
**D1063**); **O2** makes the new command supersede the in-job-review design's **Q16**; **O3** frames
the signed message (the digest plus the verified `fr`/`fs`) rather than signing a bare digest; **O4**
keeps the record's `verdict` at `ship|changes` and puts the display-only `independent` on `tier`, and
fails the run when the sign call fails; **O5** forbids posting `review.md` verbatim; **O6** gives the
reviewer the **base** branch's `CLAUDE.md` and `.claude/rules`; **O7** moves the release slot to
**0.16.0 or later**, gated on C3.9.

**CSR4a is unblocked and ships first; CSR4b is unblocked too** — U7's owner-run live observation
returned on 2026-11-01 and both fences hold (§6) —
and the plan carries the machinery D891(a) names. **D893(c) makes the credentials service a hard
precondition of the whole design**, and because that service ships dormant and does not yet admit a
project-scoped job, **CSR7, CSR2 and the coordinator's `ship` → merge path stay blocked until it is
met** (§6's **E7** and D893's own entry below say exactly what is missing).

- **D891 (ANSWERED by the owner, option (a)). May the review job's reviewer run repository code —
  Bash, and D885's mutation test — in the container that holds the person's token?** *The owner's
  answer:* **(a) the mutation test runs in a subprocess with the token scrubbed from its environment
  and egress denied, and Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` are denied for the rest
  of the review stage.** The reviewer keeps the one capability it needs for a security-sensitive pull
  request (`cargo test`-style, one named test, one mutant), the stage loses the shell as a general
  exfiltration path, the agent can no longer rewrite the tree it is being checked against while the
  check runs, and the two things F24 names (the environment and the network) are the two things this
  closes; the residual it leaves is the `/proc` read, which is what `mixed-credentials.md` §6 already
  lives with and which the separate job is the only mitigation for. **O1 shrinks that residual
  further, and the two are independent:** with the reviewer's model calls going through the
  credentials service, **there is no OAuth token in the review container for the scrub to remove** —
  the environment route is closed by not mounting the token at all — so the best case for a process
  that reads `/proc` is the review job's own **one-hour, read-only installation token**, not the
  person's subscription token. **The machinery is CSR4b's** (a SEC task): the mutant is applied, run and
  reverted by the **runner** rather than by the agent's Bash — **restored from the bytes the runner
  read before it applied the mutant and holds in its own memory**, not by `git checkout --`, which
  would let the head's `.gitattributes` filters, `core.fsmonitor` and hooks choose what the revert
  runs — and `internal/agent/settings.go`'s `DenyRules` gains the five rules for the rest of the
  review stage. **How egress is denied for a subprocess inside a Cloud
  Run container was U7, and U7 is ANSWERED (2026-11-01): both fences — the network namespace and the
  private `/proc` — are available and hold** (§6). That question asked the `/proc` half in the same
  breath (a PID namespace with its own `/proc` mount, or a different uid), because the two residuals
  are the same question asked twice; §6 records the observation and its five caveats, and the first
  of them — the child must be started with an explicit environment rather than inheriting one — is a
  requirement on **CSR4b**.
  *The alternatives the owner did not take, kept for the record* — **(b) deny Bash
  and drop D885's cloud mutation check**: cheaper and simpler, everything stays read-only, and the
  independent review of a security-sensitive pull request loses the check that tells "the test
  exists" from "the test would have caught this" — the review goes back to reading the test rather
  than running it, which is what the owner does by hand today;
  **(c) accept the residual and record it**: no machinery at all, and the design then has to say, in
  `SECURITY.md`'s own posture, that a review job is a container that runs an untrusted diff's code —
  which, under O1, it is *without* a model credential in it, so the separate job's value would be
  *context* and *scope* rather than containment, and the fallback (a mounted token) is what would make
  the older exposure statement true again.

- **D892 (ANSWERED by the owner, option (a)). How does the coordinator know a verdict is the review
  job's, given that another job on the same shared project job can write under the review run's own
  prefix?** The gap is real and §6 states it: the ordering argument covers the reviewed pull request's
  own coder and **not** a concurrent run on the same shared job, whose account holds `objectUser` on
  the whole of `runs/` (F15), can `storage.objects.list`, and can therefore write a forged
  `review.json` under a prefix it can see. *The owner's answer:* **(a) the verdict is signed, and the
  signature is what the coordinator acts on — with the signing done by the credentials service, so
  the key never enters a container that runs untrusted code.** A forged, unsigned or replayed
  `review.json` is refused. **Why the signer moved (the owner's ruling of 2026-11-01):** the review
  container runs a test from the head under review by design (D885), so a signing key mounted on that
  variant would share an OS user and the `/proc` residual with the account's OAuth token, and a key
  whose compromise forges **every** verdict of that account is not worth the read §7 already records.
  **The flow:** the artifact writer (CSR3) computes **SHA-256 over `review.json`'s exact bytes** — the
  digest, not the object, so the reviewer's prose never travels to the service — and **posts the
  digest** to the service's sign endpoint, authenticated by the two identities the service already
  verifies on every call (**F16**, and §4's D893 entry): the job service account's Google ID token,
  whose `email` claim is checked against the installation's job accounts, and the **run's own Firebase
  identity**, whose claims name the repository (`fs`), the run (`fr`) and the pull requests it may
  write (`fpr`). The service answers with the **HMAC-SHA-256 over a framed message under the account's
  review-signing key**, lowercase hex, and the job writes it into the **sidecar object beside the
  artifact**, `runs/<slug>/<review-run>/artifacts/review.json.sig`, one line of lowercase hex — not
  into `review.json`, so the bytes that were signed are the bytes that are stored. **The algorithm is
  the design's, not the implementation's** — named here so the signer and the verifier cannot disagree
  without an error on either side, and so neither end can quietly accept a weaker construction.
  **O3 fixes the message, and it is not the bare digest:** what is signed is the digest **together with
  the two identities the service verified on the call** — the run's Firebase identity (`fr`) and the
  repository claim (`fs`) — concatenated in a fixed order with fixed separators, one signature per
  review run. A signature over a bare digest could be lifted from one review run to another wherever
  two artifacts happen to hash alike; the frame binds *who asked* beside *what was asked about*. **The
  artifact writer sends only the digest** (**CSR3**'s call); **the frame lives in the service**
  (`internal/credsvc`, **C3.9e**, **D1073**), built from the `fs` and `fr` it verified — a sign request
  that names a run or a repository is refused — and **the verifier re-frames from its own copy of the
  two claims** rather than from anything the artifact or the caller asserts — the verify call presents
  the digest, the signature and the same two claims, and the service answers yes or no.
  **What the key is exposed to, stated at its true size:** it is held by the **credentials service
  only** — a Secret Manager secret in the installation's project, **one per account**, read by the
  service's own service account and by nothing else (not the review variant, not the shared project
  job, not the coder's job, not the coordinator); verification is **a second call to the same
  service**, for the same reason, because a second holder is a second place it can leak from. **A
  leaked key forges every verdict of that account until rotation**, which is the price of the
  one-holder design and the reason the key is kept out of the container that reads an untrusted diff.
  **Both artifacts are redacted by the writer, before the digest** (F25): the signature does not make
  reviewer prose safe to publish, and a redaction applied at publish time would change the bytes that
  were signed. So `review.json` and `review.md` both pass through `internal/agent/redact.go` **in the
  run, before CSR3 takes the digest**, and the signature covers the redacted bytes. **Replay, and what the verifier refuses:** the digest covers the object's exact
  bytes, so `repository`, `pr`, `head_sha` and `review_run` are bound by the signature, and **O3's
  frame binds the run and the repository beside the digest**, so a signature cannot be moved to
  another run's frame either — and a valid
  artifact of an *earlier* review of the same pull request is still a valid signature over a valid
  object, so the verifier additionally refuses unless **`review_run` equals the run the coordinator
  launched** (the nonce), **`repository` and `pr` are that pull request's**, and **`head_sha` equals
  the pull request's current head**. Without the signature those four would be checks on a document
  anyone able to write under the prefix could have written. **Rotation:** the key is a versioned
  Secret Manager secret and rotation is the service's own operation — a new version it signs with, and
  a window in which it accepts a verification against **the version before it and nothing older**; an
  artifact signed under a retired version stops verifying, and the honest remedy is **re-reviewing the
  head**, not trusting the old object. There is no escrow and no re-signing. **What C3.9 must grow,
  because none of this is built:** the sign and verify endpoints themselves; **their authentication**
  (the two identities above for a sign call, and the coordinator's own credentials for a verify call —
  a caller class the service does not have today); **which repositories and runs may request a
  signature** (the run identity's `fs` and `fpr` against the account's allowed repositories, and
  nothing a caller sends); **per-account key rotation**; and the **audit line** a sign call owes on
  the terms C1.13 already sets for a mint. **The refusal is tested, not asserted:** CSR3 ships the
  digest-and-sidecar writer and the verifier's four checks in `internal/runner/reviewartifact.go`,
  against a fake service, and `TestReviewArtifactIsSignedByTheCredentialsService`,
  `TestVerifyRefusesAForgedArtifact`, `TestVerifyRefusesAnUnsignedArtifact` and
  `TestVerifyRefusesAReplayedArtifact` are its tests; the coordinator calls the same checks rather
  than writing a second set. **Honest about what it does and does not do:** it stops **another job**
  forging a verdict; it does not stop an injected *reviewer* from writing a dishonest one inside its
  own run, which is the §7 residual no signature closes, and which is why **the verdict is advice and
  the merge is the coordinator's act, taken after the pull request's CI, the readiness rule the run
  already computed and a person's own look** (spec §6). *The
  alternatives the owner did not take, kept for the record* — **(b) narrow the bucket so only the
  review job's account can write a review run's prefix**: the strongest answer and no key material,
  and it is a bucket-layout change with an IAM migration, which the owner's 2026-10-17 ruling accepted
  the opposite of (**F15**) and which belongs to the project-job work, not here; **(c) accept it and
  make it the coordinator's rule that a verdict never merges on its own**: no machinery at all, and the
  design would then have to say that its "the coordinator merges on `ship`" path is not a path — every
  merge keeps a person's own look as its last step, which is what happens today by hand.
- **D893 (ANSWERED by the owner, option (c)). How does the read-only installation token reach the
  review job?** This is the question **UNVERIFIED-2** used to hold, and the review of this branch's
  fix round was right that it is a credential-handling decision rather than an implementation detail:
  the launcher holds no App key (F16), so the token is minted elsewhere and then reaches the job, and
  the obvious channels leave it readable. *The owner's answer:* **(c) the job fetches it from the
  credentials service, by name** — nothing is handed in at all: no per-launch secret and no value in
  an execution object. The design recommended (a); the owner took (c), which is the stronger channel
  where it works and carries a **precondition the reviewer cannot ship without** (below).
  **How it works, on the code that is already there.** The keyless path exists and is wired:
  `internal/gitprov/github.ServiceTokens` (`apptoken.go:381`, the type; its doc comment sits above
  it) is the `gitprov.TokenSource` a
  provider installs when the job's environment carries `FUGARO_CREDENTIALS_SERVICE_URL` — the fact
  `internal/cli/run.go`'s `credentialsServiceLaunchEnv` puts on an opted-in installation's execution —
  and `ServiceTokens.mint` makes one `POST /v1/github/installation-token` carrying the App ID and
  **nothing else**: the repository is never the caller's to name, and the service mints for the one
  the calling job account is bound to (`internal/credsvc/github.go:245`, `JobAccount.Repo`). **That
  binding is a per-repository job's and the review variant is not one** — it is per `(profile,
  account)` and serves every repository that account may review, so `ja.Repo` cannot name a single
  one. For a review caller the repository must therefore come from the other verified identity, the
  run's own `fs` claim (`Identity.Slug`, `internal/credsvc/auth.go:160`), and that is **part of what
  C3.9 has to grow** rather than something the caller may send: the request still carries the App ID
  alone. CSR7's
  variant therefore carries that variable like any other job of the installation, and its runner mints
  nothing in-job and mounts no key. **How the job authenticates:** with the two identities the service
  already verifies on every call — the job service account's Google ID token, which Cloud Run IAM
  admits for `run.invoker` and whose `email` claim the service checks against the installation's job
  accounts (so the IAM bit says *an* authorised identity called and the email says *which*), and the
  **run's own budget identity**, the Firebase ID token for `r~<slug>~<run>` the launcher minted,
  verified against Google's keys, with `fp` checked against the project and the run's liveness read
  from the write-once outcome. **Who authorizes it, and for which repositories:** the service, from
  those two verified identities and from nothing the caller sent — and the repository is the one the
  **run identity** names, its `fs` claim, because the job account cannot name one here (below). Two
  things authorize that, and both are the launcher's and neither is new: the account's
  **`allow_data_to`** must list the repository (`claudeAccountAllowed`, **F4**) or the launch is
  refused, and the run's Firebase identity can only be minted by whoever holds the launcher's minting
  authority — so the repository a review run may ask for is bounded by *who launched it on which
  account*, not by the container it runs in. The review run's identity is the natural fit for this and
  already exists in the model: a review identity carries `fpr` (the pull requests it reviews) and **no
  `fb`** (it pushes nothing, `docs/secrets-brokering/implementation-plan.md`'s G6), which is exactly
  the shape a read-only mint is for. **The token's lifetime:** GitHub's own installation token — one hour — minted
  on demand and refreshed by the same `Token(ctx, minValid)` contract the in-job source holds; it
  lives in the process's memory for the run's length, is never mounted and never written down.
  **What the service has to grow, and it is not built:** the mint hard-codes the run's own set
  (`RunTokenPermissions`, `internal/credsvc/github.go:263`), so a review identity must be served **CSR1's fifth,
  read-only set**, and the choice must be made from the verified identity's own claims (no `fb` → a
  run that pushes nothing) rather than from any field a caller could send; the repository must come
  from the run identity's `fs` for a review caller (above); the **review class on `/v1/messages`**
  **O1** routes the reviewer's model calls through, admitted for the account the review run names and
  for no other (this **amends the credentials-service routing design #1514's D1063**; a review job
  needs no `routing.claude: {via: cascade}` to be admitted, because that switch is the operator's
  consent for a *coding* run's cascade while a review job's credential is decided by its own class);
  and the **D892 sign and verify
  endpoints** with their authentication and the per-account key rotation behind them must exist at
  all. **The C3.9 that all of this is:** #1514 splits it into **C3.9a–C3.9g**, which is the form this
  design names because that pull request is where the split is written. **The precondition, said
  plainly:** the cloud senior reviewer **requires the credentials service to be enabled for the
  installation**. The service **ships dormant**, and (c) turns that from an option into a
  requirement, so: the installation must have opted in (`fugaro update --provision-credentials`, the
  owner's own typed confirmation at a real terminal, and the bundle must hold the App key the mint
  signs with, `fugaro creds set github-app-key` — both of which are the credentials service's own
  documented steps, not new ones this design adds); **and the service must admit the review job's own
  account**, which is a **project-scoped** job: today the service refuses a caller whose account
  belongs to a shared project job (**Global Constraint 6** of
  [docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md),
  whose **C3.9** in Slice 3 — *unscheduled* — is the task that lifts it), and it does not know the
  read-only set at all. **No `fugaro update` can do that half**, which is the honest form of this
  design's HARD-RULE problem: the reviewer is blocked on another work stream's task, and the design
  says so rather than shipping a launch that is refused by the service. So **CSR7, CSR2 and the
  coordinator's `ship` → merge path stay blocked until the precondition is met**; the check that tells
  an operator so is CSR6's (`doctor`/`validate`), and **E7** in §5 records the dependency. *The
  alternatives the owner did not take, kept for the record* — **(a) a per-launch Secret Manager
  secret** (the design's own recommendation): the minter writes a version, grants CSR7's variant
  `secretAccessor`, hands the launcher the name alone and the run's cleanup deletes it — no dependency
  on the service at all, and it costs a Secret Manager write and delete per review, an accessor grant,
  and a cleanup that has to work when a run is killed; **(b) the launch's execution override**
  (`internal/backend/gcp/run.go:169`, `ContainerOverrides[].Env`): the simplest channel, and the token
  is a **plain value in the execution object**, readable by anyone in the project who can read that
  execution.

- **D894 (ANSWERED by the owner, option (a)). Which configuration does the cloud reviewer's Claude
  session load, given that it reads a branch it must not trust?** *The gap, stated against the code:* the
  review job's working directory is the **PR head**, so `claude -p` loads **that head's** Claude Code
  configuration. **Part of that is already closed on `main`, and the design never said so:** review
  mode's checkout **removes** the head's `.claude/`, `.claude-plugin/`, `.mcp.json` and
  `CLAUDE.local.md` at any depth before the stage ever starts (`stripClaudeConfig`,
  `internal/runner/reviewmode.go:253`, called from `checkoutReviewHeads` at `:202`/`:212`; its test is
  `TestReviewModeStripsClaudeConfig`, `internal/runner/reviewmode_test.go:1598`), and `fugaro.yaml`
  and `agent.review` are read from the **base** (`readBaseConfig`, `internal/runner/followup.go:88`;
  `readReviewCommandFromBase`, `internal/runner/reviewmode.go:351`), so a head cannot name the review
  prompt it is reviewed by. **What is left open is the rest of it, and it is not small.**
  `CLAUDE.md` is the one file `stripClaudeConfig` deliberately **leaves in place**, everywhere it
  appears (`reviewmode.go:238`), on the reasoning that it is untrusted *prompt text* rather than
  configuration Claude Code *runs* — but it is still **instructions the session loads**, and the
  head's `CLAUDE.md` can tell the reviewer to `ship`; and there is **no settings-level posture at all**:
  nothing in `internal/agent` sets `disableAllHooks`, `allowManagedHooksOnly` or `--setting-sources`,
  so the protection is one runner-side deletion on one code path rather than a property of the launch
  the agent is started under. A hook that survived or was re-created after the strip — by a later
  stage, by a command the recipe runs, or by a path that does not go through `checkoutReviewHeads` —
  is **a shell command run by Claude Code itself**, with `CLAUDE_CODE_OAUTH_TOKEN` in the environment
  and the job's ordinary egress, and **D891(a)'s scrub, Bash denial and egress fence are on the
  agent's own shell and the mutation subprocess, not on Claude Code's hook runner**, so none of them
  stops it. `docs/design/v1.md:866` already records exactly this shape for a follow-up (the
  configuration is the base's, the working tree is the PR's) and calls it a residual risk of the same
  trust as write access to the branch; this design never carried that sentence across, and a cloud
  reviewer is a *worse* place for it than a follow-up, because the whole point of the job is that its
  verdict is independent of the branch it reads. *The owner's answer:* **(a) the head is diff content,
  never configuration.** The head's `CLAUDE.md` and `.claude/rules/`, at every depth, are **not
  loaded** — they stay in the diff the reviewer is given, which is where a change to them belongs and
  where a security review should see it — and **O6 fixes what the reviewer is given in their place:
  the BASE branch's copies**, read from the base ref and not from the head and not out of the head's
  `.git`; where the base has none, the reviewer is given none rather than falling back to the head's.
  The stage's **managed settings** are tightened so the launch itself is the boundary: **managed hooks
  only**, **no project MCP**, and **setting sources from user and managed only** — so a hook or an MCP
  server that reaches the checkout by any other route still does not run — and the session's `HOME` is
  a **fresh directory the job creates**, so no user-level settings file baked into the image
  contributes a rule either. `fugaro.yaml`, the instructions and the review prompt stay the base's, as
  they already are, **and so is the account: `agent.oauth_token` is resolved from `--token` or from
  the configuration at the base ref (§1's order), so a head that names an account or a model in its
  own settings chooses nothing.** **This tightens the `review` stage itself, not a new one** — the
  stage `fugaro run --pr N` already runs — so a review that exists today gets the same posture and the
  design does not leave the older path behind it.

  **The posture is review mode's and not the in-loop reviewer's: it applies to the standalone review
  job and to the `review` mode of `fugaro run --pr N`, while a coding run's own in-loop review rounds
  stay byte-for-byte as they are today until a later task tightens them.**

  **It is CSR4a's (SEC), it is standalone hardening of
  a stage that exists today, and it ships first:** it waits on U7 for nothing. CSR4b is the other half
  of the old CSR4 — D885's mutation machinery, which waited on U7's owner-run probe, answered
  2026-11-01. **CSR4a's red
  test is two halves, because only one of them is red:**
  `TestReviewModeStripsClaudeConfig` **already** removes a planted hook and `.mcp.json`, so a test
  that only planted those would pass today and would be no red step at all — the red halves are the
  **settings posture** the stage is launched under (which carries no
  `disableAllHooks`/`allowManagedHooksOnly`/setting-sources keys today) and the head's **`CLAUDE.md`**
  (which survives the strip by design, and which the base's copy replaces rather than simply deleting,
  O6). *Cost:* the head's
  own configuration is not honoured at all, so a repository that legitimately configures its review
  through `.claude/` or `CLAUDE.md` gets the base's configuration instead — which is the correct
  default for a reviewer that must not be steered by the thing it reviews, and the cost is one
  paragraph in `docs/recipes.md` rather than a behaviour anybody relies on today. *Cost of the
  alternatives:* **(b) replace the checkout's `.claude/`, `CLAUDE.md` and `.mcp.json` with the base's
  copies before the stage** — close to what `stripClaudeConfig` already does, and **cruder in the one
  way that matters: it hides the diff's own changes to those files from the reviewer**, so a hook the
  pull request *adds* — which is exactly the change a security review should flag — becomes invisible
  instead of a finding; **(c) accept it and record it** — nothing to build, and it costs the design
  its containment claim: with a project hook running under the job's environment, **D891(a) is not a
  control at all** (one `SessionStart` hook bypasses the scrub, the Bash denial and the egress fence
  in a single line), so §7 has to say that plainly and what is left of the reviewer's isolation is
  the `/proc` residual and the read-only token scope, not the mutation posture.

- **D880. The trigger is a new command, `fugaro review --pr N`.** *Recommended:* a command, because
  the review job is a different job with a different account and a different token scope, and only a
  command can name that; `fugaro run --pr N --recipe review-only` stays as the low-level verb for a
  review that needs no separate credential. **O2 is answered: this command supersedes the
  in-job-review design's Q16** (`docs/in-job-review/reconciliation.md:370`), which answered *extend
  `review-only`/`pick` rather than add a second entry point* — superseded here not because that answer
  was wrong about the recipes but because **a recipe cannot change a job's credential**, so no
  extension of `review-only` can put the reviewer on a named account in a container the coder never
  ran in. That document is another work stream's and this round does not edit it; the supersession is
  recorded here and in [spec.md](spec.md) §5. *Cost of the alternative:* spelling it as `fugaro run
  --pr N --recipe review-only` (no new command) runs the review **in the coder's job** with the
  coder's credential (**F1**, **F3**), which is the configuration this design exists to leave; a
  recipe stage cannot change a job's credential at all.
- **D881. D9's hold on the OAuth reviewer stage stays. This design does not lift it.** *Recommended:*
  do not lift. **D9 in its own words**, `docs/design/project-centric.md` **§10**'s "Effect on work in
  flight" table (the row) and **§11**'s phase 5 list: *"#363 OAuth reviewer stage | Multiplies
  per-repository variant jobs; widens token
  blast radius under sharing. | **Hold** until phase 5 (owner, D9)"*, with phase 5 being *"OAuth
  pool: person-owned secrets, references, `accounts share`, CLI failover; #363 rebased"*. **The two
  costs D9 names are job multiplication and a token shared by a shared job — not the `/proc` read**,
  which an earlier draft of this design asserted and which is wrong: every `oauth` workflow already
  lives with that read (§1). Of the two, the **job-per-repository** cost is the one this design
  answers (its jobs are per `(profile, account)`, F14), and the **pool** half of phase 5 is **not**
  met: person-owned secrets, `accounts share` and the launcher's failover are E1, **merged as PR #1470
  as a design** and not built. D9's subject is `agent.stage_auth` (the held plan's P1–P17), which this
  design needs no part of. *Cost of the alternative:* lifting the hold ships P1–P17 as well — a second `EnvSpec`, a
  per-stage managed settings write, per-stage cost fields, the infra variant jobs and a `SECURITY.md`
  line — and buys a reviewer whose token is in the coder's container, which is the exposure this
  design exists to avoid; and it would do so **before** phase 5's pool exists, i.e. while the shared
  job F3 describes still holds the App key and the whole profile's repositories share it.
- **D882. The reviewer's account is chosen by the run's own rule, and the failover stays the
  coordinator's.** *Recommended:* `--token` over `agent.oauth_token` over `default` (**F4**,
  inheriting its tests); an account whose mark is `exhausted`, `backed-off` or `broken` is **refused
  with its reset time** rather than retried, and the coordinator decides whether to try the next
  account (**F6**). *Cost of the alternative:* build the cascade in the launcher (tier A then tier B,
  stickiness, lease) — a retry loop, a state protocol and a policy surface, i.e. the credentials
  service's job done in the CLI, and it would have to be built twice if the service lands.
- **D883. The review job's forge credential is read-only, on a job that mounts no App key, and the
  launcher posts nothing on its behalf.** *Recommended:* a **fifth permission set**
  `{contents: read, pull_requests: read, metadata: read}` on a job that is a **new per-account
  variant with no `github-app-key` in its `secret_env` and no accessor on that key** (**F2**, **F3**,
  **F16**, **CSR7**), the read-only token minted by the **credentials service** — never the launcher,
  which holds no key — and **fetched by the job from the service by name, with nothing handed in**
  (**F16**, **D893 (c)**); the job posts no comment, no review and no label, and the verdict is an
  artifact. *Cost of the alternative:* launch on the **existing shared project job** —
  no new infra, no new deploy step, and it mounts the App key with `secretAccessor`, so the container
  that runs the untrusted diff can mint a token with `contents: write` and `pull_requests: write`
  however narrow the token the launcher handed it; the read-only set would then be a comment on the
  launcher's intent rather than a boundary. A second alternative — grant `pull_requests: write` so the
  job posts its own comment — is one fewer caller and a write credential in a container that reads
  untrusted code, on the pull request under review.
- **D884. The verdict vocabulary is the artifact's own: `ship`, `fix first`, `reject`.** *Recommended:*
  a new versioned artifact (`schema_version`), because the in-run format refuses a third word on
  purpose and folds *fix first* and *reject* into one (**F11**); the issue shape reuses the closed
  `class` set and the `severity` vocabulary, and adds `line`, `fix` and `evidence` (**F12**), and
  carries the three anchored axes only if the owner wants the in-job mode thresholds to read an
  independent review too — recommendation: **carry them**, they are what a mode's threshold table
  reads. *Cost of the alternative:* reuse `ship|changes` and map `changes` to *fix first* — cheap,
  and it makes *reject* unexpressible, so a coordinator launches a fix round on a pull request a
  person should have seen.
- **D885. Mutation checks are allowed, in the review job's own worktree, never pushed.** *Recommended:*
  the reviewer may apply one mutant to one file, run one named test, record what happened and
  **revert** — off unless `--mutate <file:test>` asks, bounded, and recorded in
  `mutation_checks`; on a security-sensitive pull request the checklist asks for one. *Cost of the
  alternative:* no mutation ever — cheaper and simpler, and the independent review of a
  security-sensitive pull request loses the one check that distinguishes "the test exists" from "the
  test would have caught this".
- **D886. `agent.max_output_tokens.reviewer` must fit the artifact, and it is a policy key.**
  *Recommended:* the review job's cap is the role's figure (the installation ceiling, a repository may
  only tighten it), the design states that a reviewer whose model maximum is under 32000 needs it
  raised to what the artifact needs, and a **truncated artifact is a failed review** (F7, F11's
  "unreadable is not a ship"). *Cost of the alternative:* leave the cap to whatever a repository
  already sets — a review that looks complete and stops mid-JSON, which is the failure mode the
  existing verdict parser was written for.
- **D887. `fix first` launches the fix round with the findings as its brief, automatically.**
  *Recommended:* the launcher renders the review run's `issues[]` into the fix run's brief, bounded
  and quoted, naming the review run it came from, and the coordinator then asks for a review of the
  **new head** (**F20**). *Cost of the alternative:* the owner copies the findings — which is what
  happens today by hand, and it is the step this design exists to remove.
- **D888. The first slice is E1 plus the review job, and the release slot is 0.16.0 or later.**
  *Recommended:* the smallest thing that lets a cloud run use a named account end to end is
  `fugaro accounts add` (**E1**, designed in PR #1470, merged, not built — its own tasks **A1** the
  local config edit, **A2** the converge half and **A3** `--claude`) *plus* the review job — the
  permission set (CSR1),
  **the review-only job variant and service account that make the set a boundary (CSR7)**, and the
  command that launches it (CSR2) —
  because E1 alone makes a named account usable by
  `fugaro run --token`, and the review job is the first thing in this design that needs it; **0.16.0
  or later, gated on C3.9** (**O7** is answered, and it moves the slot this decision first named as
  0.15.0), with the version gates (`internal/cli/recipes_skew.go`'s `…Since` precedent) in the same
  release. The slice also reuses the settings design's **S5** (`internal/cli/settings_write.go`), the
  shared write helper `accounts add`'s converge half uses. **Why the gate and not just the number:**
  the whole first slice needs the credentials service — the review identity admitted, the read-only
  set, the review class on `/v1/messages` and the sign and verify endpoints, i.e. **C3.9** of that
  service's Slice 3, split by #1514 into **C3.9a–C3.9g** — so **0.16.0 ships the command and the job
  variant and neither can run** until C3.9 lands (**E7**). *Cost of the alternative:* a 0.14.2 slice —
  it would have to carry E1 (a new command, a config
  edit, a policy publish and a job deploy) and the review job (a new job shape, a third permission
  set and a new artifact format) with no room for the gates, and one missed gate silently ignores a
  field the owner typed.
- **D889. The live check is Check 125, and its row lands at implementation.** *Recommended:* the
  number is claimed in this work's README and the eight steps are written in [spec.md](spec.md) §8;
  the `docs/gcp-live-checklist.md` row and section are written by the task that implements the check,
  because that file is another work stream's. *Cost of the alternative:* write the row now — it means
  touching a file the 0.16.0 rulings name, and the register's own rule is that a claim is settled at
  its merge, so a row written here could still have to be renumbered.
- **D890. The review job's stage passes its own schema and reads its own artifact.** *Recommended:* the
  review job hands `schemas/review.schema.json` to its stage and decodes the result with the artifact's
  own reader; `runner.VerdictSchema` and `runner.ParseVerdict` are **not** on a review job's path, and a
  result the artifact's decoder refuses ends the run as an **invalid verdict**
  (`endReviewInvalidVerdict`: `StatusFailed`, `OutcomeNone`, the decoder's reason) rather than a
  synthesized `blocker` finding (**F23**). The coding run's `review` stage keeps `VerdictSchema` and its
  `blocker`-finding posture untouched. *Cost of the alternative:* leave the stage on `VerdictSchema` —
  nothing new to build, and it costs the design its own vocabulary: `fix first` and `reject` are not
  deliverable through a two-word schema, so the artifact would have to be a *second* thing the reviewer
  writes beside the verdict the stage actually parsed, with two results that can disagree, and the run's
  outcome decided by whichever one the parser read.

## 5. Dependencies

| # | What | Needs this design to… | Order |
| --- | --- | --- | --- |
| **E1** | **`fugaro accounts add`/`rm`** (`docs/accounts-add/`, **merged as PR #1470**; D860–D872 written, D873–D879 reserved) | nothing at all: it is the prerequisite. Its **design** is on `main`; its remaining tasks — the command itself — must land, or be carried by this design's own first slice (**D888**), **before** any review job can mount a named token | **first** |
| **E2** | `internal/yamledit` (PR #1467) | nothing; it is E1's editor for the local config | with E1 |
| **E3** | the `settings` design's publish stages (PR #1464, merged) and `upgrade`'s rules step (PR #1468) | nothing; E1 reuses them to converge the policy and the job set | with E1 |
| **E4** | `website/content/cli-reference.md` (generated by `ee/clidoc`) and `website/**` | it must **not** hand-edit it: the task that adds `fugaro review` regenerates the page, and if the page is outside its scope it records that in its own pull request (F22) | with CSR2 |
| **E5** | review levels L1.1/L4.3 (PR #1424, #1407) — the effort key, the L3 adversarial level | nothing for correctness: this design **records** the effort it sets and works with no key, and its security-sensitive checklist overlaps L3 without depending on it | independent |
| **E6** | `internal/cli/recipes_skew.go`'s `…Since` gate precedent | it must copy the shape (an unshipped constant, a release-checklist line, a refusal naming the release) for every new task field or recipe construct it adds | with CSR1/CSR6/CSR7 |
| **E7** | **the credentials service, enabled and admitting a project-scoped job** ([docs/credentials-service/](../credentials-service/implementation-plan.md): the `--provision-credentials` opt-in, the App key in the bundle, **Global Constraint 6** and its **C3.9** in Slice 3, *unscheduled* — **split by the routing design #1514 into C3.9a–C3.9g**; the read-only set its mint must serve, the repository resolved from the run identity rather than from a one-repository job account, **the review class on `/v1/messages`** that **O1** routes the reviewer's model calls through (amending #1514's **D1063**), and **D892(a)'s sign and verify endpoints** with their authentication and the per-account key rotation) | nothing structurally — the keyless fetch path is already in the tree — but **D893(c) makes it a precondition, not an option**: without it the review job cannot fetch the head at all, and D892(a)'s signer lives there too. It is another work stream's task and this plan adds no task for it | **blocks CSR7, CSR2 and the coordinator's `ship` → merge path** |

## 6. UNVERIFIED

| # | Question | How it is settled |
| --- | --- | --- |
| **U1** | Does a *read-only* installation token suffice to fetch a pull request's head and its `author_association` (F1's gate) from GitHub, or does the gate need a permission this set lacks? | the coordinator's `{pull_requests: read, metadata: read}` set is minted today but its two callers only clone; **Check 125's step 4** is the observation, and the implementation task's first step is to read the gate's API calls against the set |
| **U2** | **Moot — there is nothing left to verify.** It asked whether `claude setup-token`'s flow and the seven-step page ever make the **D-U2** claim true (the login's output parsed without a person copying it). The owner's ruling on the accounts design's **D862** answered the design's **alternative (a)**: `--claude` is two acts — the login in **the owner's own terminal**, then **one paste** — with **no pty and no output parsing**, so nothing in the command reads the login's output and this question has no subject. `spec.md` §8's step 1 says so |
| **U3** | Is `CLAUDE_CODE_EFFORT_LEVEL` honoured on the **review job's** direct stage the same way it was measured locally? | measured offline against the pinned binary (F8); the live observation is Check 125's step 3's recorded `effort`, and a mismatch is **recorded, not corrected** |
| **U4** | Does a truncated verdict output announce itself (a Claude Code `result` event, a non-zero exit) or does it look like a short answer? | **D886**'s "truncated is a failed review" needs an observation to hang on; the implementation task's test is a fake agent returning a clipped result, and Check 125's step 3 is the live one |
| **U5** | Does the review job's transcript carry the token if the reviewer echoes its environment? | `redact.go` and `r.addSecret` say it should not; the observation is Check 125's step 2 ("its log must name no token value") plus a hermetic test in the implementing task |
| **U6** | Can a launched review job be told the head SHA the coordinator observed, so a head that moved between the coordinator's read and the job's fetch is refused rather than silently reviewed? | `FetchPRHead` resolves the head at bootstrap today and records it (`ReviewedPR.SHA`); whether to pass the expected SHA in is **not decided here** and is left to the implementing task with the owner's D880 answer in hand |
| **U7** | **ANSWERED (2026-11-01) — both fences exist and both hold.** *(The question, kept for the record.)* **How is egress denied for a subprocess inside a Cloud Run container — and can that child be given a `/proc` of its own?** D891(a) answers that the mutation test runs with the token scrubbed and **egress denied**, and no network namespace or `unshare` in gen2 had been checked — Cloud Run's second-generation execution environment might not have permitted the one, and the job's own egress is the job's, not a child process's. **The `/proc` half is the same question asked twice** (§7): if the child cannot be fenced, the parent's `/proc` and environment stay readable from the stage that runs the head's test, and if it can be given **its own PID namespace with its own `/proc` mount, or run under a different uid**, they do not | **It was the standalone owner-run probe of the deployed gen2 environment this row asked for — not the task's first step, and not a hermetic test — and the owner ran it on 2026-11-01: of the three outcomes, the first is what it saw, both fences available and both holding.** It could not be measured hermetically: the plan's rule is hermetic tests only (`t.TempDir()`, no network, no cloud), and whether a child process can be fenced is a property of the **deployed execution environment**, which no test in this repository can hold. **The observation: sandbox probe run `20261101-210556-146e`** on the sandbox installation — **kernel 6.9.12, Cloud Run gen2, uid 1000, no capabilities, `NoNewPrivs` 1** — forking the child this row describes. **The network fence is available and holds:** the child fenced with `unshare -rn`, and the same fence under the PID namespace, `unshare -rnpf --mount-proc`, reaches nothing. **So is the private `/proc`:** `unshare -rpf --mount-proc` and `unshare -rnpf --mount-proc` each give the child a `/proc` of its own. **`unshare -n`, `setpriv` and `runuser` are refused in this environment**, which is why the working form is the user-namespace one. **And the unfenced baseline is exactly the exposure the fence exists to close:** the same child, unfenced, reached the internet **and both metadata addresses**, and read the parent's environment and `/proc/1/environ`. **It is deliberately not asked through the mutation check**, which was this row's first form and was circular: `fugaro review --mutate` needs CSR2's command and CSR4b's mutation code, and CSR4b could not start until U7 returned, so the question would have been asked by the thing whose existence it decides. So it was **Check 125's step 6**, and that step was a **probe of its own** — a one-off execution of the deployed job image whose entrypoint forks a child that tries to reach the network, tries to read the parent's environment through `/proc`, and is fenced with a network namespace (`unshare -n` where the image has it) **and a PID namespace with its own `/proc` mount, or a different uid**, recording which of the three outcomes it saw. **The answer carries five caveats, and each one is a requirement or a limit rather than a note:** (1) **a namespace stops the child reading the *parent's* `/proc`, not the variables it was handed at start** — so the mutant/test child **must** be started with an explicit environment (`env -i` plus an allow-list), a requirement on **CSR4b** and on #1514's **C3.9e**; (2) **the claim that the parent's environ is denied inside the PID namespace is partly an artifact** — `$PPID` reads 0 there — so the evidence is the **visible-PID drop, 11 to 4**, and not that read on its own; (3) **`unshare -rn` removes all networking, loopback included**, so **only the tool or test child can be fenced and never the agent**, whose model calls need the loopback gateway; (4) **the probe ran under the agent's shell in an ordinary coding job**, so if **CSR7**'s review-only variant runs in a different execution environment, **Check 125's step 7 confirms again**; (5) **the unfenced baseline exposure is real**, which is why the fence is a requirement rather than hardening. **U7 gates nothing now:** CSR4b does not start on it, and the same answer is the observation the routing design **#1514**'s **D1074** (its **C3.9e**) made a precondition of the first slice's signed `ship` path — the probe was taken on the sandbox environment that decision names, so that precondition is met. **Check 125's step 7 stays the confirmation afterwards**, on the mutation path that actually uses the fence; had the fence not been there, it would have been an amendment to D891's answer and the owner asked again — the design does not quietly fall back to `denyWeb`. **O1 shrinks what the `/proc` half protects:** with the model calls routed through the credentials service, the best case for a process that reads the parent's `/proc` is the review job's own one-hour, read-only installation token, not the person's subscription token — and caveat 3 is why that stays true of the agent's own stage while the mutant's fenced child no longer reads the parent's `/proc` at all |
| **UNVERIFIED-2** | **Answered by D893 (c): the token is never handed in.** How does the minted read-only installation token **reach** the review job, and is the channel acceptable? The launch path passes per-run variables as a Cloud Run execution override (`internal/backend/gcp/run.go:169`), whose value is a **plain value in the execution object**, readable by anyone who can read that execution; a mounted short-lived secret would not be. | It was a **decision, not an observation**, and the owner has taken it: **(c) the job fetches the token from the credentials service, by name** — no per-launch secret, no value in an execution object, and **CSR7 wires the variant to the service** rather than handing it anything. What the answer costs is not a channel but a **precondition** (the service enabled, admitting a project-scoped job, and minting CSR1's read-only set), and D893's entry in §4 states it in full |

Nothing above is approved by being written down. Answering a decision is not approving a task.
