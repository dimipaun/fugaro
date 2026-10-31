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
which **D880–D893 are written** — D880–D891 answered (D891 by the owner, option (a)) and **D892 and
D893 open questions for the owner** — and **D894–D899 are reserved**. `D800` in `docs/` is the false positive the
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
`CoordinatorTokenPermissions` (`internal/gitprov/github/apptoken.go:128` — **in code, not in a design
document**, which an earlier draft of this finding got wrong by citing
`docs/coordinator/reconciliation.md`). §2's set is a fourth, and new work.

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
review-only job variant and service account whose `secret_env` holds the account's OAuth token and no
`github-app-key`, and whose account holds no accessor on that key. That is new infra, not a
permission string, and [implementation-plan.md](implementation-plan.md) carries it as **R7**.

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
no App key. Naming, deploying and census-reading that variant is **R7**.

**F15. `Rule` — a shared project job's bucket reach is installation-wide, and the accepted ruling
says so.** `docs/design/bucket-iam.md` §2.1 and §10: a project job account holds
`roles/storage.objectUser` conditioned on the whole of `runs/`, `cache/` and `locks/` (one clause per
prefix, **no slug**, `gcp.ProjectBucketCondition`), and the owner ruled on 2026-10-17 to **accept**
that reach — a profile is a secret boundary, not a bucket one. **Consequence:** a review job that
runs on a shared job can write any run's node, so §6's integrity argument is the **ordering** (the
review run's id is minted after the coder's job ended) and **not** a boundary. §6 says this, and this
finding is why it must keep saying it.

**F16. `Rule` — the minting precedent an earlier draft leaned on is not one, because every mint in
this tree happens where the App key is mounted.** `internal/cli/gitcred.go:92`'s `mintBuildToken`
reads `FUGARO_GITHUB_APP_ID`/`FUGARO_GITHUB_APP_PRIVATE_KEY` **from its own environment** and is
called by the build it serves; `internal/cli/imagecheck.go:530`'s `jobGitEnv` does the same inside a
check job. `github.BuildTokenPermissions` is the exported spelling of the set, not a way to mint
without a key. **A launcher on the owner's laptop holds no App key and must not be given one.**
**Consequence:** F3's fix needs a **mint site**, and there are two candidates — the **credentials
service** (its bundle already holds the App key for exactly this, where the installation has opted
into one) or the **coordinator** (which already holds the write credential). It also needs a
**hand-in**: the launch path gives a job its per-run variables through a Cloud Run execution override
(`internal/backend/gcp/run.go:169`, `ContainerOverrides[].Env`), whose values are plain values in the
execution object and readable by anyone who can read that execution. Whether that is acceptable, or
whether the token has to be a short-lived mounted secret instead, is **UNVERIFIED-2** in §6.

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
`runner.ParseVerdict`, which is also the coding run's path (`internal/runner/runner.go:2674`, `:2752`).
`VerdictSchema`'s `verdict` enum is `ship|changes` and its `required` is `["verdict","findings"]`
(`internal/runner/verdict.go:18`), and `ParseVerdict` answers a result it cannot read with a finding of
its own — `severity: "blocker"`, `summary: "review produced no parseable verdict: " + why`, the last
`why` being *"nothing in the result was a verdict"* (`:58` is the `blocker` literal, `:93` the `why`
text; an earlier draft cited `:53`/`:46`, the two function headers). **Consequence:** `spec.md` §3's
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
scrubbed and egress denied, and Bash is denied for the rest of the review stage, which leaves the
`/proc` read as the residual.

**F25. `Rule` — the artifact is the one thing a review job produces that leaves the run, and the
redaction rule covers the transcript, not it.** `internal/agent/redact.go` redacts what is published
from agent output, and the transcript artifact rides that rule (F18); §3 lets a coordinator or a
person paste `review.md` into a comment or a description. Reviewer prose generated from an untrusted
diff is exactly the sort of text the redaction exists for. **Consequence:** §3 names the artifact as
something that passes through the same pass before it is published anywhere, and R3 has to make that
true rather than assume it.

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

**F27. `Gap` — nothing bounds the fix ↔ review loop, and a subscription hides the cost.**
`agent.max_budget_usd` reaches Claude Code as `--max-budget-usd` and is an estimate
([docs/multi-model.md](../multi-model.md)), not enforcement; a review job is not gateway-priced at all
(F9); what bounds a run is a wall clock (the profile's `timeout_class` → the job's `task_timeout_s`,
and the launch's `Overrides.Timeout` from `timeouts.total`). §6's "the coordinator then asks for
another review" is a loop with **no counter in this design** — the plan defers the counting to the
coordinator, where the owner's policy can see it. **Consequence:** §7 states the three bounds it has,
the one it does not, and the one the owner actually feels: a named account's weekly and five-hour
usage windows are shared with the owner's own interactive work.

## 4. Owner decisions

Recommendation first in each, with the cost of the alternative. **D880–D893 are this work's block;
D894–D899 are reserved and unused.** **D880–D891 are answered** — D891 the last, with option (a) —
and **D892 and D893 are open questions for the owner**, raised by the review of this branch's fix
round because each one decides something the design cannot decide for itself: whether a coordinator
may **act** on a verdict at all (D892), and whether the review job's token can reach it without being
left in plain sight (D893). **R4 is unblocked, subject to U7's gate**, and the plan carries the
machinery D891(a) names.

- **D891 (ANSWERED by the owner, option (a)). May the review job's reviewer run repository code —
  Bash, and D885's mutation test — in the container that holds the person's token?** *The owner's
  answer:* **(a) the mutation test runs in a subprocess with the token scrubbed from its environment
  and egress denied, and Bash is denied for the rest of the stage.** The reviewer keeps the one
  capability it needs for a security-sensitive pull request (`cargo test`-style, one named test, one
  mutant), the stage loses the shell as a general exfiltration path, and the two things F24 names (the
  environment and the network) are the two things this closes; the residual it leaves is the `/proc`
  read, which is what `mixed-credentials.md` §6 already lives with and which the separate job is the
  only mitigation for. **The machinery is R4's** (a SEC task): the mutant is applied, run and reverted
  by the **runner** rather than by the agent's Bash, and `internal/agent/settings.go`'s `DenyRules`
  gains Bash for the rest of the review stage. **How egress is denied for a subprocess inside a Cloud
  Run container is UNVERIFIED** — a network namespace or `unshare` in gen2 has not been checked; that
  is **U7** in §6. *The alternatives the owner did not take, kept for the record* — **(b) deny Bash
  and drop D885's cloud mutation check**: cheaper and simpler, everything stays read-only, and the
  independent review of a security-sensitive pull request loses the check that tells "the test
  exists" from "the test would have caught this" — the review goes back to reading the test rather
  than running it, which is what the owner does by hand today;
  **(c) accept the residual and record it**: no machinery at all, and the design then has to say, in
  `SECURITY.md`'s own posture, that a review job is a container that holds a person's subscription
  token and runs an untrusted diff's code — which is the same exposure the coder's job already has on
  that account, so the separate job's value would be *context* and *scope*, not containment.

- **D892 (OPEN). How does the coordinator know a verdict is the review job's, given that another job
  on the same shared project job can write under the review run's own prefix?** The gap is real and
  §6 states it: the ordering argument covers the reviewed pull request's own coder and **not** a
  concurrent run on the same shared job, whose account holds `objectUser` on the whole of `runs/`
  (F15), can `storage.objects.list`, and can therefore write a forged `review.json` under a prefix it
  can see. *Recommended:* **(a) sign the artifact with a per-account review-signing secret mounted
  only on R7's variant, and have the coordinator verify it before it acts on a verdict** — the secret
  is one more per-account secret (its own accessor grant, none on the App key), the review job
  computes an HMAC over the artifact's bytes as it writes them, and a concurrent job cannot forge it
  because it cannot read that secret. Honest about what it does and does not do: it stops **another
  job** forging a verdict; it does not stop an injected *reviewer* from writing a dishonest one inside
  its own run, which is the §7 residual no signature closes. *Cost of the alternatives* — **(b)
  narrow the bucket so only the review job's account can write a review run's prefix**: the strongest
  answer and no key material, and it is a bucket-layout change with an IAM migration, which the
  owner's 2026-10-17 ruling accepted the opposite of (**F15**) and which belongs to the project-job
  work, not here; **(c) accept it and make it the coordinator's rule that a verdict never merges on
  its own**: no machinery at all, and the design then has to say that its "the coordinator merges on
  `ship`" path is not a path — every merge keeps the owner's own look, which is what happens today by
  hand. **Until the owner answers, §6 says which of the three the design relies on (none) and the
  coordinator acts on a verdict only as advice.**
- **D893 (OPEN). How does the minted read-only installation token reach the review job?** This is the
  question **UNVERIFIED-2** used to hold, and the review of this branch's fix round is right that it
  is a credential-handling decision rather than an implementation detail: the launcher holds no App
  key (F16), so the token is minted elsewhere and then **handed in**, and the obvious channel leaves
  it readable. *Recommended:* **(a) a per-launch Secret Manager secret** — the minter writes a
  version, grants R7's variant service account `secretAccessor` on it, hands the launcher the **name**
  alone, and the run's own cleanup deletes the secret version when the review ends, so nothing
  plaintext sits in an execution object and the grant is one secret wide. *Cost of the alternatives*
  — **(b) the launch's execution override** (`internal/backend/gcp/run.go:169`,
  `ContainerOverrides[].Env`): the simplest channel, no new secret, and the token is a **plain value
  in the execution object**, readable by anyone in the project who can read that execution;
  **(c) the job fetches it from the credentials service by name at boot** (the service already holds
  the App key): no plaintext anywhere and no per-launch secret, and it works only where the
  installation has opted into the service, which is optional and ships dormant. **The cost (a) has to
  be paid honestly**: a Secret Manager write and delete per review, an accessor grant, and a cleanup
  that has to work when a run is killed — R7's task, not a later one. **Until the owner answers, the
  design claims no channel**, and R7's first step is the owner's answer.

- **D880. The trigger is a new command, `fugaro review --pr N`.** *Recommended:* a command, because
  the review job is a different job with a different account and a different token scope, and only a
  command can name that; `fugaro run --pr N --recipe review-only` stays as the low-level verb for a
  review that needs no separate credential. *Cost of the alternative:* spelling it as `fugaro run
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
  launcher posts nothing on its behalf.** *Recommended:* a **fourth permission set**
  `{contents: read, pull_requests: read, metadata: read}` on a job that is a **new per-account
  variant with no `github-app-key` in its `secret_env` and no accessor on that key** (**F2**, **F3**,
  **F16**, **R7**), the read-only token minted where a key exists — the credentials service or the
  coordinator, **never the launcher**, which holds no key — and handed in through the launch
  override (**F16**, **UNVERIFIED-2**); the job posts no comment, no review and no label, and the
  verdict is an artifact. *Cost of the alternative:* launch on the **existing shared project job** —
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
- **D888. The first slice is E1 plus the review job, and the release slot is 0.15.0.** *Recommended:*
  the smallest thing that lets a cloud run use a named account end to end is `fugaro accounts add`
  (**E1**, designed in PR #1470, merged, not built) *plus* the review job — the permission set (R1),
  **the review-only job variant and service account that make the set a boundary (R7)**, and the
  command that launches it (R2) —
  because E1 alone makes a named account usable by
  `fugaro run --token`, and the review job is the first thing in this design that needs it; 0.15.0,
  with the version gates (`internal/cli/recipes_skew.go`'s `…Since` precedent) in the same release.
  *Cost of the alternative:* a 0.14.2 slice — it would have to carry E1 (a new command, a config
  edit, a policy publish and a job deploy) and the review job (a new job shape, a third permission
  set and a new artifact format) with no room for the gates, and one missed gate silently ignores a
  field the owner typed.
- **D889. The live check is Check 125, and its row lands at implementation.** *Recommended:* the
  number is claimed in this work's README and the seven steps are written in [spec.md](spec.md) §8;
  the `docs/gcp-live-checklist.md` row and section are written by the task that implements the check,
  because that file is another work stream's. *Cost of the alternative:* write the row now — it means
  touching a file the 0.15.0 rulings name, and the register's own rule is that a claim is settled at
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
| **E4** | `website/content/cli-reference.md` (generated by `ee/clidoc`) and `website/**` | it must **not** hand-edit it: the task that adds `fugaro review` regenerates the page, and if the page is outside its scope it records that in its own pull request (F22) | with R2 |
| **E5** | review levels L1.1/L4.3 (PR #1424, #1407) — the effort key, the L3 adversarial level | nothing for correctness: this design **records** the effort it sets and works with no key, and its security-sensitive checklist overlaps L3 without depending on it | independent |
| **E6** | `internal/cli/recipes_skew.go`'s `…Since` gate precedent | it must copy the shape (an unshipped constant, a release-checklist line, a refusal naming the release) for every new task field or recipe construct it adds | with R1/R6/R7 |

## 6. UNVERIFIED

| # | Question | How it is settled |
| --- | --- | --- |
| **U1** | Does a *read-only* installation token suffice to fetch a pull request's head and its `author_association` (F1's gate) from GitHub, or does the gate need a permission this set lacks? | the coordinator's `{pull_requests: read, metadata: read}` set is minted today but its two callers only clone; **Check 125's step 4** is the observation, and the implementation task's first step is to read the gate's API calls against the set |
| **U2** | **Moot — there is nothing left to verify.** It asked whether `claude setup-token`'s flow and the seven-step page ever make the **D-U2** claim true (the login's output parsed without a person copying it). The owner's ruling on the accounts design's **D862** answered the design's **alternative (a)**: `--claude` is two acts — the login in **the owner's own terminal**, then **one paste** — with **no pty and no output parsing**, so nothing in the command reads the login's output and this question has no subject. `spec.md` §8's step 1 says so |
| **U3** | Is `CLAUDE_CODE_EFFORT_LEVEL` honoured on the **review job's** direct stage the same way it was measured locally? | measured offline against the pinned binary (F8); the live observation is Check 125's step 3's recorded `effort`, and a mismatch is **recorded, not corrected** |
| **U4** | Does a truncated verdict output announce itself (a Claude Code `result` event, a non-zero exit) or does it look like a short answer? | **D886**'s "truncated is a failed review" needs an observation to hang on; the implementation task's test is a fake agent returning a clipped result, and Check 125's step 3 is the live one |
| **U5** | Does the review job's transcript carry the token if the reviewer echoes its environment? | `redact.go` and `r.addSecret` say it should not; the observation is Check 125's step 2 ("its log must name no token value") plus a hermetic test in the implementing task |
| **U6** | Can a launched review job be told the head SHA the coordinator observed, so a head that moved between the coordinator's read and the job's fetch is refused rather than silently reviewed? | `FetchPRHead` resolves the head at bootstrap today and records it (`ReviewedPR.SHA`); whether to pass the expected SHA in is **not decided here** and is left to the implementing task with the owner's D880 answer in hand |
| **U7** | **How is egress denied for a subprocess inside a Cloud Run container?** D891(a) answers that the mutation test runs with the token scrubbed and **egress denied**, and no network namespace or `unshare` in gen2 has been checked — Cloud Run's second-generation execution environment may not permit the one, and the job's own egress is the job's, not a child process's | **R4**'s first step: measure it in a hermetic harness where it can be measured, and observe it in **Check 125's step 6** (the mutation check, with the container's egress watched). If it turns out a child cannot be fenced, that is an amendment to D891's answer and the owner is asked again — the design does not quietly fall back to `denyWeb` |
| **UNVERIFIED-2** | **Closed by D893.** How does the minted read-only installation token **reach** the review job, and is the channel acceptable? The launch path passes per-run variables as a Cloud Run execution override (`internal/backend/gcp/run.go:169`), whose value is a **plain value in the execution object**, readable by anyone who can read that execution; a mounted short-lived secret would not be. | It is a **decision, not an observation**, and the review of this branch's fix round was right to say it cannot be left to the implementing task: **D893** puts the three channels to the owner (a per-launch secret, the execution override, the credentials service by name) and **R7 implements the answer** — the grant, and the cleanup that has to work when a run is killed, are that task's |

Nothing above is approved by being written down. Answering a decision is not approving a task.
