# The independent senior review as a cloud job: spec

**Status: design only; nothing approved.** This document describes a shape; it changes nothing and
commits nobody. Its decisions are numbered in
[reconciliation.md](reconciliation.md) §4 and its tasks in
[implementation-plan.md](implementation-plan.md). Where this document and the reconciliation
disagree, the reconciliation wins, because it was written against the code.

---

## 1. What exists today for a reviewer on an OAuth account

**The reviewer is a stage inside the coding run, and the account is chosen at launch.** Nothing here
is new; it is what the tree has on `main`, read at `48754c01`.

**The stage and the recipe.** `internal/runner` drives the run as a stage machine
([docs/design/v1.md](../design/v1.md) §4): `implement`, an optional first-line `review_first`, the
senior `review`, and `fix`, until a round's verdict is `ship` or the rounds run out. A recipe names
the steps (`internal/recipe/catalog/`): `default` is `first_line` then `review`, `standard` adds
`bounce: first_line`, `premium` sets `roles: {coder: reviewer}` so the strong model does the whole
loop. **`review-only.yaml` is the one recipe that is not a loop at all**: `mode: review`, one
`review` step with `max_rounds: 1`, *"No coding. The senior model reviews an existing pull request
and comments on it."* It is launched as `fugaro run --pr N --recipe review-only`, which resolves the
pull request's head, checks it out and reviews it **inside the run's own job** — same credentials,
same mounts, same container as a coding run.

**The model and the effort.** The senior stage's model is `agent.models.reviewer`, falling back to
`agent.model` (`config.Agent.ModelFor(RoleReviewer)`); the per-call output limit is
`agent.max_output_tokens.reviewer`, and `0` means no limit of Fugaro's
(`internal/config/config.go`). **Effort has no Fugaro key today.** The per-process mechanism exists
and is measured ([docs/multi-model.md](../multi-model.md), "How the pinned Claude Code takes an
effort setting", measured 2026-10-30 against the pinned Claude Code): `CLAUDE_CODE_EFFORT_LEVEL` is
the environment variable, its closed set is `low, medium, high, xhigh, max`, it beats both the
`--effort` flag and an `effortLevel` settings key, and a managed settings file can only clamp it
down through `maxEffortLevel`. The key that would set it is review level **L1.1/L4.3** of
[docs/review-levels-ab-jobmax/](../review-levels-ab-jobmax/implementation-plan.md), which is not in
the tree; so **the reviewer's effort is whatever the model's own default is** today, and this design
records the effort it sets rather than reading it back (the level does not appear in the stream).

**The output cap is load-bearing for a structured verdict.** The gateway refuses a request whose
`max_tokens` is above the role's `agent.max_output_tokens` (recorded as a violation, and the stage
fails), and the role's figure reaches Claude Code as `CLAUDE_CODE_MAX_OUTPUT_TOKENS`
([docs/design/v1.md](../design/v1.md) §5's pins). A reviewer that must emit a long structured answer
— a verdict, one entry per issue with a location and a suggested fix, and prose around it — **is
truncated silently by a limit sized for a conversational reply**, which is the lesson this design
carries: a reviewer model whose own maximum output is under 32000 needs
`agent.max_output_tokens.reviewer` raised to what the artifact needs, and the raised figure is a
policy key (an installation ceiling in the operator config, a repository may only tighten it).

**Who picks the account, and the failover order.** Three settings, in this order, exactly as
`internal/cli/run.go`'s `claudeAccountFor` resolves them: the **`--token NAME` flag**; then
`agent.oauth_token` **as the file at the ref the run checks out** states it; then the checkout's own
`agent.oauth_token` (layer-applied); then **`default`**, the unsuffixed `claude-oauth-token` secret
every repository already uses. A name is judged by `claudeAccountAllowed`: it must be
`oauth`-auth, and it must be in `config.AllowedClaudeAccounts(claudeAccounts, repo)` — that is, the
account's `claude_accounts.<name>.allow_data_to` must list the repository. **The default account is
always in that list and is gated by nothing** (`internal/config/claude_accounts.go`: the list is
`{default} ∪ {names whose allow_data_to lists the repository}`). A follow-up `--pr N` keeps its
previous run's account unless `--token` is given explicitly.

**There is no failover in the launcher today.** The credential **cascade** — tier A the subscription
OAuth tokens, tier B the paid API key, cheapest-usable-first, with per-run stickiness and a lease —
exists in `internal/creds` (`select.go`, `state.go`) and belongs to the optional credentials
service; the runner selects through it only where the credentials service is deployed. What the CLI
does have is the **mark**: `fugaro accounts ls` reads `creds/marks/<name>` and prints whether an
account is `available`, `exhausted` (with its reset time), `backed-off` or `broken`, and
`fugaro accounts clear NAME` is what ends a broken mark after a token is replaced
(`internal/cli/accounts.go`, the two subcommands that exist today; `add` and `rm` are the design in
[docs/accounts-add/](../accounts-add/README.md), not in the tree). **The failover the owner knows
from the skill is the skill's**, not the CLI's: `project-centric.md` §7's ruling — the launcher's own
accounts first, then shared ones, with a tried-list — is a plan, not code.

**Cost accounting: a subscription call is not priced by the gateway.** For an `auth: oauth` workflow
the budget gateway is off for that credential's calls, so **nothing prices them**: the run's
`cost.model_usd` carries Claude Code's own estimate of its usage, `runstore.ModelBasis` is
`subscription`, and the caps (`budget.per_run_usd`, the project ceiling, the day cap) do not bind
them. This is stated in [docs/design/mixed-credentials.md](../design/mixed-credentials.md) §5 and §7
("*Capped by the gateway: no; only Claude Code's own estimate*"), and it is the property the owner is
buying: the senior review costs no dollars per run, bounded only by `agent.max_budget_usd` — which
is passed to Claude Code as `--max-budget-usd` and **is Claude Code's own estimate**, accurate for a
Claude model and about twenty-five times too high for some provider models
(`docs/multi-model.md`). The in-job reviewer of a budget-on run is the reverse: gateway-priced at
list rates, every call counted against the caps.

**The token's reach in the job that has it.** A workflow whose `agent.auth` is `oauth` mounts
`claude-oauth-token-<account>` as a secret environment variable on the job that runs it, and the
job's service account holds `secretAccessor` on that secret. The runner's own environment therefore
carries `CLAUDE_CODE_OAUTH_TOKEN` for the whole job, and the coding agent runs as the same OS user in
the same container — so a prompt-injected coder can read it from `/proc/<runner>/environ` or from
the metadata server (`mixed-credentials.md` §6; the plan's Review Focus 1 and 2). That residual is
accepted today for an `oauth` workflow and is the whole reason `stage_auth` is **held** (D9).

---

## 2. How a pull request's independent review runs as a cloud job

**The review job is a second execution, not a stage.** The coding run ends, its branch is pushed and
its pull request is open. Then a **review job** is launched: same repository, same installation, its
own run id, its own container, its own service account, **review mode** and the `review-only`
recipe. Its whole input is the pull request number and the head commit it resolves.

**Why a separate job.** `mixed-credentials.md` §6 mitigation 4 says it plainly: *"a separate job
execution for the reviewer, with its own container and service account, is the only mitigation that
closes the gap"* — the `/proc` read and the metadata server. The token is then held in a container
that has never run a line of the repository's code, has read nothing but the head it fetched, and
will be gone when the review is over. It also removes a configuration the owner has to reason about:
in the coder's job there are two credentials and a per-stage environment to get right (the held
plan's P10, P11, P14); in a separate job there is one credential and one stage.

**The forge token is read-only, and the job holds no key that could mint anything else.** A run's
installation token today asks for `{contents: write, pull_requests: write, issues: read, metadata:
read}` (`internal/gitprov/github/apptoken.go`'s `tokenPermissions`, exported as
`RunTokenPermissions`), because a run pushes its branch and opens its pull request. Two narrower
read-only sets are already minted in this tree: the image build's `{contents: read, metadata: read}`
(`buildTokenPermissions`/`BuildTokenPermissions`), used by `internal/cli/gitcred.go` and
`internal/cli/imagecheck.go`, and the coordinator's `{pull_requests: read, metadata: read}`.

The review job's set is the third: **`{contents: read, pull_requests: read, metadata: read}`** — read
the head, read the pull request's metadata, write nothing. **A permission set is not a boundary by
itself**: a GitHub App's private key can mint *any* subset the App holds, so a job that mounts the
App key can mint a write token whatever the launcher asked for. The review job therefore **mounts no
App private key at all**; the launcher mints the read-only installation token and hands it in (a
short-lived secret, the `mintBuildToken` precedent, or the credentials service where the
installation has opted into one). **The review job writes nothing to the pull request**: not a
comment, not a review, not a label. Its verdict is an artifact, and the coordinator — which already
holds the write credential — decides what, if anything, to post.

**Worktree isolation, and mutation checks in the cloud.** The review job checks out the pull
request's head at the SHA the provider reports (`FetchPRHead`, already what review mode does) into
its **own** filesystem, never the launcher's checkout and never the coding run's worktree, which no
longer exists. On a security-sensitive pull request the reviewer may do what an independent reviewer
does by hand: **apply a mutant** to a file in its worktree, **run the named test**, see it fail (or
not), and **revert** — with `git checkout --` on its own working tree, or by discarding the
worktree, and never with a push. That is a scratch edit inside a container that is thrown away. It
is off unless asked for, it is bounded (one mutant, one named test, a time limit, and a revert
before the stage ends), and the artifact says which test was run and what it did.

**The job never holds a credential it does not need.** It mounts exactly one secret — the named
account's Claude OAuth token — and receives exactly one credential — the read-only installation
token. It does not mount the App private key, the Bitbucket token, the credentials bundle, the
budget token or the git signing material. The billing identity this design chooses is the thing to
check against the tree: a review job launched on the **shared project job** of `(profile, account)`
inherits that job's bucket condition, which is the whole of `runs/` (`gcp.ProjectBucketCondition`,
`docs/design/bucket-iam.md` §2.1) — the accepted, installation-wide reach the owner ruled on
2026-10-17. A review job needs to write one run's own record and artifact, so that reach is more
than it needs; narrowing it is a bucket-layout change this design does **not** make and instead
records as a finding (**F12**).

---

## 3. The output contract

**Two artifacts and one record, all three versioned.**

**`review.json` — the machine shape.** Written under `runs/<slug>/<review-run>/artifacts/`, with the
same staging and upload path every run's artifacts take (`internal/runner/artifact.go`), so the
bucket layout, the name sanitisation and the record's own artifact list are the ones that exist:

```json
{
  "schema_version": 1,
  "review_run": "20261031-210911-718a",
  "pr": 1470,
  "head_sha": "<40 hex>",
  "base": "main",
  "reviewer": { "account": "team-a", "model": "claude-opus-5-5", "effort": "high" },
  "verdict": "fix first",
  "overview": "…",
  "strengths": ["…"],
  "issues": [
    {
      "severity": "blocker",
      "class": "security",
      "file": "internal/creds/select.go",
      "line": 155,
      "summary": "…",
      "fix": "…",
      "evidence": "…"
    }
  ],
  "security": "…",
  "performance": "…",
  "test_coverage": "…",
  "mutation_checks": [
    { "file": "...", "test": "TestX", "applied": "...", "observed": "fail", "reverted": true }
  ]
}
```

**`review.md` — the same thing for a person.** Generated from the JSON, section for section:
overview, strengths, issues to flag, security, performance, test coverage, verdict. It exists so a
reader never has to read JSON to understand a review, and so a comment or a PR description can be
the markdown verbatim.

**The fields, and where they come from.** `severity` is the vocabulary the runner already reads
(`blocker`/`major`/`minor` — the in-job spec's §6; `runstore.Finding` keeps `Severity` free-form and
reads the three anchored axes `likelihood`, `impact` and `fix_cost` instead, [docs/rigor-modes/]).
`class` is the closed set `runner.VerdictSchema` already carries: `correctness`, `performance`,
`security`, `data_loss`, `maintainability`, `cosmetic`. `file:line` is one location, and **the line
is a claim the reviewer makes about the head it read**, so `head_sha` is recorded beside it and a
reader that finds the file moved knows which commit the line meant. `fix` is a suggestion, never a
patch the reviewer applied. **The four sections the in-job template has become fields rather than
prose** (`security`, `performance`, `test_coverage`, plus `overview` and `strengths`), so a
coordinator can route on them without parsing English.

**The verdict vocabulary is this artifact's own: `ship`, `fix first`, `reject`.** It is **not** the
in-run verdict's vocabulary. `internal/runner/verdict.go` decodes exactly two words, `ship` and
`changes`, and **refuses a third on purpose** — *"every reader of a verdict compares it against
those two, so a third word would be a run that shipped nothing and said so"*. An independent review
has a third outcome the in-run rule cannot express: an issue that no automated fix round should
touch. So the independent review's artifact carries its own `schema_version` and its own three
words, and the two formats never share a reader. **Why this is a decision and not a detail:** the
alternative — reuse `ship|changes` and let the launcher map `changes` to *fix first* — cannot tell
*fix first* from *reject*, so a coordinator would launch a fix round on a pull request a human
should have looked at.

**The artifact is versioned like every other wire shape here.** A new field is additive and keeps
`schema_version`; removing or retyping one bumps it; a field whose meaning must change gets a new
name (`CLAUDE.md`'s "a field never changes meaning"). The JSON Schema lands in `schemas/` with the
task that first writes the artifact, and the console and the coordinator read the version rather
than the field list.

**What the review job contributes to the run record.** The record already carries `reviews[]` with a
`verdict` and a `findings` count, and `runstore.Cost`. A review job's record is a run record like
any other: mode `review`, the PR's number and SHA, the account it ran as (`runs ls`'s `ACCOUNT`
column and `fugaro diagnose`'s `Account:` line already exist), and its cost as **notional** — the
subscription's calls are not gateway-priced. The artifact is the review's substance; the record is
its provenance.

---

## 4. How it differs from the in-job reviewer

**This is a recorded lesson, not a preference.** [docs/dogfooding.md](../dogfooding.md): *"the
in-loop reviewer is a different, stronger model than the coder, but it is still not independent
review … both are DeepSeek models of the same family, reading the code the other wrote, so it is
still the loop judging itself: keep the real review **outside** the loop … A run's own `ship` never
replaces it."* Every difference below is a way of being outside the loop rather than a stronger
prompt inside it.

| | In-job reviewer (`review` stage) | The independent review job |
| --- | --- | --- |
| **When** | during the run, between `implement` and `fix`, changing what the run does next | **after** the run, on a head that is already pushed and already has a pull request |
| **What it reads** | the working tree and the run's own session history | a **fresh clone of the pushed head**, with no memory of the coder's reasoning and no diff against an earlier round |
| **Model** | `agent.models.reviewer` — often a cheap provider model on the same family as the coder | the strongest Claude model available, on a **named OAuth account** the owner controls |
| **Credential** | the coder's job credential, in a container that also runs repository code | its own job, its own account, a **read-only** forge token, and no App key |
| **Context** | shares the run's context budget, its pins and its settings file | a session of its own, with its own output limit and effort |
| **Checklist** | the review prompt's rules | the same rules plus the **security-sensitive checklist** and, on request, a **mutation check** |
| **Effect** | a `changes` verdict drives a `fix` stage inside the same job | a **verdict artifact** the coordinator acts on; the review job itself changes nothing |

**The security-sensitive checklist is part of the contract, not a mode.** A pull request that
touches a credential path, a bucket condition, an IAM grant, a signature or an auth boundary gets
its named test **run against a mutant** and its blast radius stated in the artifact's `security`
section. Which paths count is a list this design does not invent: the repository already marks such
work (`SECURITY-SENSITIVE` in a task's own text, the paths `SECURITY.md` and `.claude/rules/` cover,
and the coordinator's own §7 on what an independent review of a security-sensitive part must cover).

---

## 5. The trigger: `fugaro review --pr N`

**Recommendation first: a new command, `fugaro review --pr N`.** It launches the review job
described in §2 for pull request `N` in the current checkout's repository, picks the account by
§1's rule (`--token` over `agent.oauth_token` over `default`), and prints the review run's id so the
coordinator can read its artifact. `fugaro review --pr N --json` prints the artifact's JSON.
`--mutate <file:test>` asks for a mutation check (§2).

**Why a command and not one of the two existing spellings.** `fugaro run --pr N --recipe
review-only` already does an independent review, and it is the right low-level verb — but **it runs
in the run's own job**, with the run's credential, its mounts and its App key. That is exactly the
configuration this design exists to leave, and it is also the reason a recipe stage cannot be the
answer: **a recipe cannot change a job's credential**, so no recipe stage can put the reviewer on the
named account in a container the coder never ran in. A new command is the honest spelling for "a
different job, a different account, a different token scope". The existing spelling stays, unchanged,
for a review of a pull request that needs no separate credential.

| Option | Cost of choosing it |
| --- | --- |
| **`fugaro review --pr N`** (recommended) | one new command, one new job variant, a version gate and a `cli-reference.md` row (which another work stream owns) |
| `fugaro run --pr N --recipe review-only` in the coder's job | no new command — and no separate credential either, so the reviewer stays in a container that ran repository code, which is the gap `mixed-credentials.md` §6 mitigation 4 names |
| A recipe stage inside the run (the held plan's `stage_auth`) | the per-stage machinery the held plan already wrote (P1–P17), D9's hold lifted, and the token in the coder's job: the very exposure the separate job removes |
| A `/review` skill that drives the CLI | it can be *added* later and changes nothing here: a skill is a caller of the command, and the command is what has to exist first |

**The trigger is the coordinator's, not the run's.** A run never launches its own review: a run that
could would be the loop judging itself again with extra steps. What launches the review job is
whoever owns the pull request after the run — today the owner's own session, later the coordinator
or the console. **The one exception worth writing down:** a `fugaro review` on a pull request whose
coding run is still going is refused, because the head would move under the review (a review run
records `head_sha`, and a verdict about a commit that is no longer the head is a verdict about
nothing).

---

## 6. What the coordinator does with the verdict

**Nothing automatic is a merge today, and this design does not make one.** The review job holds no
forge write permission (§2), so a verdict cannot merge anything. The **coordinator** reads the
artifact by the review run's id and does one of three things.

**`ship` → the coordinator merges** (or tells the owner to), having done what it always does: the
pull request's CI, the readiness rule the run already computed, and the human's own look. The
verdict is advice and the merge is the coordinator's act; the artifact's `head_sha` is the commit the
advice is about, and a pull request whose head has moved since gets no merge on that verdict.

**`fix first` → the coordinator launches a fix round whose brief is the findings.** This is the
part that must not need a human copying anything. The fix round is an ordinary follow-up —
`fugaro run --pr N --recipe <the repository's recipe>` — and the findings reach **its brief** from
the artifact, by run id, without a person in the middle:

- the launcher reads `runs/<slug>/<review-run>/artifacts/review.json` and renders the `issues[]` as
  the brief's findings section, each as a bounded, quoted block: severity, class, `file:line`, the
  summary and the suggested fix;
- the brief **names the review run** it came from, so a reader of the fix run's record can see which
  review it answers and a later review can be compared against it;
- the findings are **quoted data and never instructions**: they are reviewer output derived from the
  pull request's own diff, which is untrusted input (§7), so they are bounded in size, never passed
  through a shell, and the prompt says they are findings to weigh rather than orders to follow
  (review levels L1.1's `focus` rule, `internal/runner/prompts.go`'s existing "fix only what the
  findings name" posture);
- the fix round is an ordinary run: it edits, tests, commits and pushes the branch, and the
  coordinator then asks for **another review** of the new head — the artifact's `head_sha` makes
  "reviewed again" a fact rather than an assumption.

**`reject` → no fix round.** The coordinator stops: no follow-up is launched, the pull request waits
for a person, and the artifact says why. This is the outcome the in-run `ship|changes` vocabulary
cannot express (§3), and it is the reason the two vocabularies stay apart.

**A verdict that cannot be read is not a `ship`.** A missing, unparseable or truncated artifact, a
review run that failed, or a review that ended without writing its artifact is an **infra fault the
coordinator reports**, never a silent approval — the same posture `runner.ParseVerdict` takes when a
review produces no parseable verdict (it becomes a blocker rather than a pass).

**The verdict artifact's integrity.** A coded run must not be able to forge its reviewer's verdict
(§7). What makes that true, honestly: the review run's id is minted by the launcher **after** the
coding run's job has ended, so a coder that wanted to write the artifact would have had to write a
path under an id that did not exist yet; and the artifact's provenance is the review run's own record
— its id, its account and its timestamps — which the coordinator reads beside the object. This is a
real property of the ordering and **not** a signature: nothing in the object is cryptographically
bound to the review job, and a shared project job's bucket condition reaches every run's prefix
(F12). The design records that gap rather than claiming a boundary it does not have.

---

## 7. The threat model

**The OAuth token's blast radius in a cloud job.** A subscription OAuth token is **a person's**: it
is usable anywhere Claude Code runs, it has no dollar cap, and it shares the owner's own rate limits
and usage windows, so stealing it costs the owner their interactive work as well as the
installation's. Who may use whose is decided in two places and nowhere else: the account's
`claude_accounts.<name>.allow_data_to` in the owner's local config, which the launcher checks at
launch; and, where the account is shared beyond one person, the policy's reference record with its
own `allowed profiles/repositories` and the **one job service account per (profile, account)** that
mounts exactly that person's secret (`project-centric.md` §7, `project-named-oauth-tokens.md`). This
design adds one more thing and it is the important one: **the token is mounted on the review job and
on no other job.** The coder's job does not have it, so the container that runs the pull request's
code — the container a prompt injection actually reaches — has nothing to steal. That is the
difference between this design and the held `stage_auth` plan, and it is what
`mixed-credentials.md` §6 mitigation 4 promised.

**The read-only forge scope is one scope among several, so the job holds no minting key.** The
review job receives `{contents: read, pull_requests: read, metadata: read}` and **no GitHub App
private key** (§2): a key would let the job mint a write token whatever the launcher asked for, so
the scope is a token handed in and not a permission the container could widen. The consequence is
that the review job cannot push, cannot merge, cannot comment and cannot label; whatever is posted
to the pull request is posted by the coordinator, which already holds the write credential this
installation already trusts.

**Prompt injection from the pull request's own diff.** The reviewed code is untrusted input. A
reviewer must read it — that is its job — so the injection surface is not removable, only bounded:

- **the job has nothing worth taking except its own reviewer's token.** No write credential, no App
  key, no other account's secret, no bucket reach beyond the run prefixes it already has;
- **the token it does hold is a person's, and the owner can revoke it** (`fugaro accounts rm`, the
  rotation path the accounts design describes) and see it refused (`accounts ls`'s mark);
- **the job writes nothing outward.** No web tool (`denyWeb: true`, which the held plan's P10 already
  calls for and which a direct stage needs most), no comment, no push, no artifact outside the run's
  own prefix;
- **everything the reviewer says is treated as data downstream.** The artifact is read and rendered,
  never executed; the findings that become a fix round's brief are quoted and bounded (§6);
- **what this does not close, stated plainly:** an injected reviewer can put anything it likes into
  its verdict, including a dishonest `ship`. The mitigation for that is not technical — it is that
  the verdict is advice, the coordinator decides, the CI still runs, and the owner's own look is
  still the last step. That is the same posture `SECURITY.md` takes on a review verdict elsewhere:
  advice, not evidence.

**The verdict artifact's integrity.** Covered in §6: the ordering (the review run's id does not exist
while the coder runs) is a real property; a signature over the artifact is not built, and the design
says so rather than claiming one.

**The crash reporter and the logs never capture the token.** The rules exist and this design reuses
rather than invents them: `internal/agent/redact.go` redacts anything published from agent output;
`internal/runner`'s secret registry (`r.addSecret`) is how a value is kept out of the log and the
transcript, and `gitops.ModelCredentialVars` already lists `CLAUDE_CODE_OAUTH_TOKEN` for the git
side; the credential bundle's rule is that **only the name travels** and never a value
(`internal/creds`'s `Credential` carries a name). Two things this design owes on top: the review
job's **transcript artifact** goes through the same redaction as a run's (it is a first-class
artifact, `transcripts/<stage>-<n>.jsonl`), and the **dormant crash reporter** (Sentry, the console
work's T2.1) must be built so that an environment-derived secret is never captured — the crash
reporter's own rule is already "no breadcrumbs, no local variables, no default PII", and a token in
`env` is exactly the class that rule exists for. Neither is a claim about code that is not there: the
reporter is dormant and this is what its gate has to hold when it wakes.

**What is out of scope and stated as such:** a review job on a **public** repository still sees a
public diff (nothing to leak there), but it still holds a person's token, so the owner's rule for a
public repository — respected by the account's `allow_data_to` — is what decides whether one runs at
all. This design adds no gate of its own for it: `allow_data_to` already is the gate, and the
`followup.allow_public` refusal the held plan writes is about follow-ups in the coder's job.

---

## 8. The owner-run live check

**Check 125.** Owner-run, sandbox installation only, never an agent, and **not run** — the row and the
`## Check 125` section belong in [docs/gcp-live-checklist.md](../gcp-live-checklist.md), which this
design may not edit; the task that implements the check writes them under the number free at its own
merge. **The owner types nothing secret into chat, and no agent ever sees a token.** Every command is
run by the owner in their own terminal.

1. **Add the named account.** In a normal terminal — not a coding agent's session — the owner runs
   the `fugaro accounts add <name>` of [docs/accounts-add/](../accounts-add/README.md) (or the
   `--claude` login), which stores the token, declares the account and its repositories, and
   converges the policy and the deployed job set. Read back: `fugaro accounts ls` names the account
   and its state; `fugaro doctor` raises nothing about it. **Stop here if it does not converge** —
   the review job mounts a job that must already exist.
2. **Launch a cloud review of a known pull request.** On a sandbox repository with a pull request
   whose defects the owner already knows, run `fugaro review --pr N --token <name>` and record the
   review run's id. Watch it with `fugaro watch <run>` or `fugaro logs <run>`; it must end, not be
   killed, and its log must name no token value.
3. **Read the verdict artifact back.** `fugaro review show <review-run>` (or the artifact read
   directly from the runs bucket) prints the JSON and the markdown. Check four things by hand: the
   artifact's `head_sha` is the pull request's head; the verdict is one of the three words; the
   issues the owner already knew about appear, with a `file:line` that lands on the right line of
   that commit; and the **markdown and JSON agree** section for section.
4. **Check what the job could not do.** Confirm from the pull request that the review job posted
   **nothing** — no comment, no review, no label — and that `git ls-remote` shows no branch the
   review run pushed. This is the read-only scope's positive test: it is the absence that has to be
   observed, because a write permission that was never exercised still exists.
5. **Check the accounting.** The review run's record shows the account it ran as and a **notional**
   cost; the installation's caps are unchanged by it, and `fugaro budget show` shows no counted
   spend for it. This is the subscription-versus-gateway property from §1, observed rather than
   assumed.
6. **Run the mutation check once.** On a security-sensitive pull request, `fugaro review --pr N
   --mutate <file:test>` and read `mutation_checks` in the artifact: the test ran, the mutant was
   applied, the observed result is recorded, and the worktree was reverted. Confirm again that
   nothing was pushed.
7. **Drive the fix path once.** Take a `fix first` verdict, launch the fix round the coordinator
   would launch, and read the fix run's brief: it must name the review run and carry the findings,
   with no human having copied them. Then confirm the head moved and that a second review of the new
   head is what would follow.
