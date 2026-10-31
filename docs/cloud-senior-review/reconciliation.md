# The independent senior review as a cloud job: reconciliation

**How this was checked.** Every claim below was located and read on **`origin/main` at `48754c01`**
(2026-10-31), and then against the **19 pull requests open on 2026-10-31**, named in §2. Paths and Go
symbols were re-read in place; line numbers are never cited except inside a quoted comment, and every
file path named here exists on that commit. This document is the pin: where it disagrees with
[spec.md](spec.md), **it wins**.

**Owner:** the owner of this installation. **Date:** 2026-10-31.

---

## 1. The one sentence the rest of this document serves

The **independent review** is run today on the owner's own machine, by hand, and it is the review
that actually decides whether a pull request is good ([docs/dogfooding.md](../dogfooding.md): *"keep
the real review outside the loop … a run's own `ship` never replaces it"*). Moving it into the cloud
needs two things that do not exist: **a named Claude account usable end to end** (the
[docs/accounts-add/](../accounts-add/README.md) design, PR #1470) and **a job that is not the coder's
job**, because the whole reason the in-job reviewer is not the independent review is that it shares
the loop's context, its family and its container. This design is the second half.

## 2. What was reconciled against

`origin/main` at `48754c01`. The open pull requests read for this section, with the ones that matter
to this design:

| PR | What it holds | Relevance here |
| --- | --- | --- |
| **#1470** | `docs/accounts-add/`: `fugaro accounts add`/`rm`, D860–D879 | **E1, the dependency.** The named account end to end |
| **#1467** | `internal/yamledit`: a comment-preserving YAML editor | **E2.** What `accounts add` edits the local config with |
| **#1464** (merged as `48754c01`) | `docs/settings-commands/`: `fugaro settings`, the update publish stages, D840–D859 | **E3.** The publish path `accounts add` reuses; also the highest D number on `main` |
| **#1463** | `docs/rigor-command/`: `fugaro rigor show/set/unset`, D830–D839 | The owner-side story of the review level a reviewer runs at |
| **#1424** | `task: the review_levels field (review levels L1.1)`, D724 | **E5/E6.** The effort key and the version-gate precedent |
| **#1407** | `recipe: the adversarial step (review level 3)` | The security-sensitive level this design's checklist overlaps |
| **#1468** | `upgrade: the rules step, and the docs list update's steps` | The publish/deploy staging `accounts add` converges |
| **#1418, #1416, #1415, #1411, #1407, #1405, #1425, #1420, #1449, #1377, #907, #715, #620** | other work in flight | Read for number collisions and for paths this design would otherwise claim |

**Number reading, recorded because the README's block is a claim about it:** the highest decision
number **written** on `main` is **D859** (the `settings` design's D840–D859, merged as #1464); the
highest in any open pull request is **D879** (reserved by #1470's D860–D879, of which D860–D870 are
written). **D880 is therefore the next free number**, and this work's block is **D880–D899**, of
which **D880–D890 are written** and D891–D899 reserved. `D800` in `docs/` is the false positive the
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
read-only sets exist but neither is this one: `buildTokenPermissions`
(`{contents: read, metadata: read}`, exported as `BuildTokenPermissions`, used by
`internal/cli/gitcred.go` and `internal/cli/imagecheck.go`) and the coordinator's
`{pull_requests: read, metadata: read}` (`docs/coordinator/reconciliation.md`). §2's third set is new
work.

**F3. `Rule` — a permission set is not a boundary, because the job holds the App key.**
`internal/runner/runner.go`'s `serviceTokens` mints through the credentials service where the
installation has one, and otherwise the job's own environment holds `providers.EnvGitHubAppID` and
the App's private key as a mounted secret (`internal/cli/gitcred.go`'s `gitCredAppKeyEnv`). A key can
mint **any** subset the App holds. **Consequence:** the read-only scope of §2 is only real if the
review job mounts **no** App key and receives a minted read-only token instead. That is a change to
what the review job is given, not a permission string.

**F4. `Exists` — the account selection rule this design reuses.** `internal/cli/run.go`'s
`claudeAccountFor`: the `--token` flag, then `agent.oauth_token` at the ref the run checks out, then
the checkout's own, then `default`; judged by `claudeAccountAllowed` against
`config.AllowedClaudeAccounts` (which is `{default}` plus every `claude_accounts` name whose
`allow_data_to` lists the repository). **Consequence:** §5's `fugaro review --token` inherits a rule
with tests rather than inventing a second one.

**F5. `Gap` — `fugaro accounts add` and `fugaro accounts rm` do not exist.** `internal/cli/accounts.go`
has exactly two subcommands, `ls` (names, state and the reason for it) and `clear NAME` (ends a
broken mark after a token is replaced). The one command that stores a named token, declares the
account, adds it to a profile and converges the policy and the job set is **E1**, in PR #1470.
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

**F14. `Exists` — the job axis is `(profile, provider, account)`.** `gcp.ProjectJobName` builds it,
`config.ProjectJobAccounts` is the one function the three sides of a project job compute a profile's
job set with, `internal/runner` recomputes the candidates it matches `CLOUD_RUN_JOB` against, and the
launcher picks the entry a launch targets — so a review job on a named account is a job that may
already exist, and one that does not is a refusal at open rather than a surprise at run time.
**Consequence:** `fugaro review --token NAME` needs no new job naming, only the right entry.

**F15. `Rule` — a shared project job's bucket reach is installation-wide, and the accepted ruling
says so.** `docs/design/bucket-iam.md` §2.1 and §10: a project job account holds
`roles/storage.objectUser` conditioned on the whole of `runs/`, `cache/` and `locks/` (one clause per
prefix, **no slug**, `gcp.ProjectBucketCondition`), and the owner ruled on 2026-10-17 to **accept**
that reach — a profile is a secret boundary, not a bucket one. **Consequence:** a review job that
runs on a shared job can write any run's node, so §6's integrity argument is the **ordering** (the
review run's id is minted after the coder's job ended) and **not** a boundary. §6 says this, and this
finding is why it must keep saying it.

**F16. `Exists` — the launcher can already mint a narrow installation token and hand it in.**
`internal/cli/gitcred.go`'s `mintBuildToken` mints the `{contents: read, metadata: read}` set from
the environment's App key and passes it to a build; `github.BuildTokenPermissions` is the exported
spelling. **Consequence:** F3's fix has a precedent and needs no new credential plumbing — only a
third permission set and a second caller.

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
`why` being *"nothing in the result was a verdict"* (`:53`, `:46`). **Consequence:** `spec.md` §3's
review job writes `review.json`, and if its stage result carried the findings only in the file, the
stage's reader would find no verdict and the run's outcome would be decided by a sentence about the
parser — a *blocker* finding nobody wrote, stored beside real ones, for a review that may have been
complete and correct. The two formats' separation is therefore not only about readers: the review job
must also pass **its own** schema to its own stage and read it with **its own** decoder, and an
unreadable artifact must end the run **invalid** (`endReviewInvalidVerdict`,
`internal/runner/reviewmode.go:815`) rather than fabricate a finding (**D890**).

## 4. Owner decisions

Recommendation first in each, with the cost of the alternative. **D880–D890 are this work's block;
D891–D899 are reserved and unused.**

- **D880. The trigger is a new command, `fugaro review --pr N`.** *Recommended:* a command, because
  the review job is a different job with a different account and a different token scope, and only a
  command can name that; `fugaro run --pr N --recipe review-only` stays as the low-level verb for a
  review that needs no separate credential. *Cost of the alternative:* spelling it as `fugaro run
  --pr N --recipe review-only` (no new command) runs the review **in the coder's job** with the
  coder's credential (**F1**, **F3**), which is the configuration this design exists to leave; a
  recipe stage cannot change a job's credential at all.
- **D881. D9's hold on the OAuth reviewer stage stays. This design does not lift it.** *Recommended:*
  do not lift. `agent.stage_auth` (the held plan's P1–P17) puts the subscription token in the
  container that also runs repository code, which `mixed-credentials.md` §6 mitigation 4 says a
  separate job is the only way to close; the separate job needs `agent.oauth_token` to keep its
  meaning as a preference and needs **E1**, and needs no part of that plan. *Cost of the
  alternative:* lifting the hold ships P1–P17 as well — a second `EnvSpec`, a per-stage managed
  settings write, per-stage cost fields, the infra variant jobs and a SECURITY.md line — and buys a
  reviewer that is still, by the plan's own Review Focus 1 and 2, readable through `/proc` by the
  coder. Two mechanisms for one job would then both be in the tree.
- **D882. The reviewer's account is chosen by the run's own rule, and the failover stays the
  coordinator's.** *Recommended:* `--token` over `agent.oauth_token` over `default` (**F4**,
  inheriting its tests); an account whose mark is `exhausted`, `backed-off` or `broken` is **refused
  with its reset time** rather than retried, and the coordinator decides whether to try the next
  account (**F6**). *Cost of the alternative:* build the cascade in the launcher (tier A then tier B,
  stickiness, lease) — a retry loop, a state protocol and a policy surface, i.e. the credentials
  service's job done in the CLI, and it would have to be built twice if the service lands.
- **D883. The review job's forge credential is read-only and the launcher posts nothing on its
  behalf.** *Recommended:* the job receives `{contents: read, pull_requests: read, metadata: read}`
  and **no App private key** (**F2**, **F3**, **F16**); it posts no comment, no review and no label,
  and the verdict is an artifact. *Cost of the alternative:* grant `pull_requests: write` so the job
  posts its own comment — one fewer caller, and a container that reads untrusted code holds a write
  credential it does not need, on the pull request under review.
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
  (**E1**, PR #1470) *plus* the review job — because E1 alone makes a named account usable by
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
| **E1** | **`fugaro accounts add`/`rm`** (PR #1470, `docs/accounts-add/`, D860–D879) | nothing at all: it is the prerequisite. It must be merged — or its remaining tasks carried by this design's own first slice (**D888**) — **before** any review job can mount a named token | **first** |
| **E2** | `internal/yamledit` (PR #1467) | nothing; it is E1's editor for the local config | with E1 |
| **E3** | the `settings` design's publish stages (PR #1464, merged) and `upgrade`'s rules step (PR #1468) | nothing; E1 reuses them to converge the policy and the job set | with E1 |
| **E4** | `website/content/cli-reference.md` (generated by `ee/clidoc`) and `website/**` | it must **not** hand-edit it: the task that adds `fugaro review` regenerates the page, and if the page is outside its scope it records that in its own pull request (F22) | with R3 |
| **E5** | review levels L1.1/L4.3 (PR #1424, #1407) — the effort key, the L3 adversarial level | nothing for correctness: this design **records** the effort it sets and works with no key, and its security-sensitive checklist overlaps L3 without depending on it | independent |
| **E6** | `internal/cli/recipes_skew.go`'s `…Since` gate precedent | it must copy the shape (an unshipped constant, a release-checklist line, a refusal naming the release) for every new task field or recipe construct it adds | with R1/R3 |

## 6. UNVERIFIED

| # | Question | How it is settled |
| --- | --- | --- |
| **U1** | Does a *read-only* installation token suffice to fetch a pull request's head and its `author_association` (F1's gate) from GitHub, or does the gate need a permission this set lacks? | the coordinator's `{pull_requests: read, metadata: read}` set is minted today but its two callers only clone; **Check 125's step 4** is the observation, and the implementation task's first step is to read the gate's API calls against the set |
| **U2** | Does `claude setup-token`'s flow and the seven-step page ever make the **D-U2** claim true (the login's output parsed without a person copying it)? | it is **E1**'s own live check, not this design's; this design only consumes the account E1 creates |
| **U3** | Is `CLAUDE_CODE_EFFORT_LEVEL` honoured on the **review job's** direct stage the same way it was measured locally? | measured offline against the pinned binary (F8); the live observation is Check 125's step 3's recorded `effort`, and a mismatch is **recorded, not corrected** |
| **U4** | Does a truncated verdict output announce itself (a Claude Code `result` event, a non-zero exit) or does it look like a short answer? | **D886**'s "truncated is a failed review" needs an observation to hang on; the implementation task's test is a fake agent returning a clipped result, and Check 125's step 3 is the live one |
| **U5** | Does the review job's transcript carry the token if the reviewer echoes its environment? | `redact.go` and `r.addSecret` say it should not; the observation is Check 125's step 2 ("its log must name no token value") plus a hermetic test in the implementing task |
| **U6** | Can a launched review job be told the head SHA the coordinator observed, so a head that moved between the coordinator's read and the job's fetch is refused rather than silently reviewed? | `FetchPRHead` resolves the head at bootstrap today and records it (`ReviewedPR.SHA`); whether to pass the expected SHA in is **not decided here** and is left to the implementing task with the owner's D880 answer in hand |

Nothing above is approved by being written down. Answering a decision is not approving a task.
