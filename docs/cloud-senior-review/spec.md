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
account's `claude_accounts.<name>.allow_data_to` must list the repository. **`default` is not judged
by any of that.** `claudeAccountAllowed` returns early on it (`internal/cli/run.go`: `if name ==
config.DefaultClaudeAccount { return nil }`), so neither the `allow_data_to` list — which reads
`{default} ∪ {names whose allow_data_to lists the repository}`
(`internal/config/claude_accounts.go`) — nor the `oauth`-auth check applies to it; it is the account
every repository already has, on the unsuffixed `claude-oauth-token` secret. **Every check named
above decides a *named* account, and the default is the one account none of them reaches.** A
follow-up `--pr N` keeps its
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
accepted today for an `oauth` workflow.

**It is not why `stage_auth` is held, and this design had that wrong.** D9 states the hold itself,
in `docs/design/project-centric.md` **§10** ("Effect on work in flight", where the row sits): *"#363
OAuth reviewer stage — Multiplies per-repository variant jobs; widens token blast radius under
sharing — **Hold** until phase 5."* The two costs D9
names are a **job per repository** and a token **shared wider by a shared job** — not the `/proc`
read, which the in-job spec already lives with in every `oauth` workflow.

**Is phase 5's precondition met now? Half of it, and the half that is missing is the one that
matters.** Phase 5 is *"OAuth pool: person-owned secrets, references, `accounts share`, CLI
failover; #363 rebased"* (`project-centric.md` **§11**'s phase 5 list). The **per-(profile, account)
job** half exists today: `gcp.ProjectJobName` and `config.ProjectJobAccounts` build exactly that
axis, so a token no longer has to widen a job beyond one account (F14). The **pool** half does not:
person-owned secrets, the share operation and the launcher's own failover are the `accounts add`
design, E1, **merged as PR #1470 as a design** — the decisions and the plan are on `main`, the command
itself is not built. So D9's worst cost (a job per repository) is answerable by this design, and D9 is
still not lifted: its subject is `agent.stage_auth`, which this design does not need. **D881** states
that in [reconciliation.md](reconciliation.md) §4 with the cost of each way.

---

## 2. How a pull request's independent review runs as a cloud job

**The review job is a second execution, not a stage.** The coding run ends, its branch is pushed and
its pull request is open. Then a **review job** is launched: same repository, same installation, its
own run id, its own container, its own service account, **review mode** and the `review-only`
recipe. Its whole input is the pull request number and the head commit it resolves.

**Why a separate job.** `mixed-credentials.md` §6 mitigation 4 says it plainly: *"a separate job
execution for the reviewer, with its own container and service account, is the only mitigation that
closes the gap"* — the `/proc` read and the metadata server. **O1 makes that gap smaller than it was:
the review job's model calls route through the credentials service's tier A on `/v1/messages`,
admitted for the named account alone, so the container holds no OAuth token to read** — not in its
environment, not in `/proc`, not on the variant's `secret_env`. What the container does hold is the
one-hour, read-only installation token the service minted for this run, in the runner's own memory,
which writes nothing and expires with the run. **It is not a container that never runs repository
code, and this design will not claim that it is:** D885's mutation check runs a test from the head
under review by design, and the review stage's own deny list covers Claude Code's tools — `WebSearch`,
`WebFetch` and the binary reads (`internal/agent/settings.go`'s `DenyRules`) — rather than a shell.
**D891 is answered, option
(a):** the mutation test runs in a **subprocess with the token scrubbed from its environment and
egress denied**, and **Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` are denied for the rest of
the review stage**, with the mutant applied and reverted by the runner instead of by the agent's shell
— so the stage's general exfiltration path (the environment and the network) is closed, and the
residual is the `/proc` read §7 states. What the
separate container does remove is a configuration the owner has to reason about: in the coder's job
there are two credentials and a per-stage environment to get right (the held plan's P10, P11, P14);
in a separate job there is one stage, and under O1 no model credential in it at all.

**The forge token is read-only — and that needs a job that does not exist yet.** A run's installation
token today asks for `{contents: write, pull_requests: write, issues: read, metadata: read}`
(`internal/gitprov/github/apptoken.go`'s `tokenPermissions`, exported as `RunTokenPermissions`),
because a run pushes its branch and opens its pull request. Three narrower read-only sets are already
in this tree: the image build's `{contents: read, metadata: read}`
(`buildTokenPermissions`/`BuildTokenPermissions`, used by `internal/cli/gitcred.go` and
`internal/cli/imagecheck.go`), the coordinator's (`coordinatorTokenPermissions` at
`apptoken.go:128`, exported as `CoordinatorTokenPermissions` at `:142`), and the challenger arm's
`shadowTokenPermissions` (`{contents: read, metadata: read}`, `apptoken.go:111`).

The review job's set is a fifth: **`{contents: read, pull_requests: read, metadata: read}`** — read
the head, read the pull request's metadata, write nothing. **A permission set is not a boundary by
itself**: a GitHub App's private key can mint *any* subset the App holds, so a job that mounts the App
key can mint a write token whatever the launcher asked for.

**`fugaro run --token <name>` already puts the token on a job that mounts that key, and this design's
first draft said otherwise.** The shared project job of `(profile, provider, account)` — the job
[docs/accounts-add/](../accounts-add/README.md) deploys for a named account (`+ standard/github/team-a`
in that design) and the job a coding run on that account executes — mounts `FUGARO_GITHUB_APP_PRIVATE_KEY` from the installation's project-wide copy, with
`secretAccessor` for that job's own service account
(`deploy/terraform/gcp/roots/installation/tests/project_job.tftest.hcl`'s
`shared_job_mounting_the_app_key_keeps_the_accessor`, which pins both the mount and the grant). So a
review job launched on it would inherit **both** the account's OAuth token **and** the App key, and
"mounts no App key" would be false of the thing this design launches.

**The review job must therefore be its own job variant, with its own service account, per account.**
Same `(profile, provider, account)` axis and the same `gcp.ProjectJobName` shape (a suffix or a
parallel entry in the installation root's `project_jobs`), and the differences are the whole point:
its `secret_env` (the Terraform module's env→secret map, `internal/infra/projectjobs.go`'s
`ProjectJob.SecretEnv`) carries **no `github-app-key`** and, under **O1**, **no OAuth token either** —
the reviewer's calls reach the model through the credentials service, and the account is named by the
run's own task and by the service's admission of the review class rather than by a mounted secret —
and its service account holds **no `secretAccessor`** on that key. It is deployed by `fugaro update`
with the rest of the job set, and the implementation plan carries it as its own task (CSR7) because a
launcher cannot launch a job that does not exist.

**Who mints the read-only installation token, since the launcher holds no key — and how it reaches the
job.** The two mints in this tree both happen *inside* something that mounts the App key —
`mintBuildToken` (`internal/cli/gitcred.go:92`) reads
`FUGARO_GITHUB_APP_ID`/`FUGARO_GITHUB_APP_PRIVATE_KEY` from its own environment and is called from a
build, and `internal/cli/imagecheck.go:530` does the same inside a check job. **Neither is a precedent
for a launcher on the owner's laptop**, which has no key and must not be given one. **The owner's
answer is D893, option (c): the credentials service mints it and the job fetches it by name — nothing
is handed in at all** ([reconciliation.md](reconciliation.md) §4). There is no per-launch secret and
no value in an execution object, because there is no hand-in to design: the review job's variant
carries the installation's `FUGARO_CREDENTIALS_SERVICE_URL` like any other job of an opted-in
installation, and the keyless path already in the tree does the rest — `internal/gitprov/github`'s
`ServiceTokens` (`apptoken.go:381`, its doc comment above it) makes one `POST /v1/github/installation-token` that carries
the App ID and nothing else, and the **service** decides the repository and the permissions.

**Which repository, for a job that serves many — and this is new service work.** Today the mint
resolves the repository from the **calling job account** (`JobAccount.Repo`,
`internal/credsvc/github.go:245`), which is a complete answer because one job account belongs to one
repository. **A review variant is per `(profile, account)` and serves every repository that account
may review**, so no such binding names one, and the answer has to come from the other identity the
call presents: the **verified run identity**, whose `fs` claim is the run's own repository slug
(`Identity.Slug`, `internal/credsvc/auth.go:160`), taken **within the account's allowed
repositories**. What authorizes it is launcher-side and unchanged: the account's `allow_data_to` must
list that repository or the launch is refused (§1), and only a launcher holding the run's minting
authority can mint the Firebase identity the service verifies. Growing the service to read the
repository from the run identity for a review caller is **C3.9's** work with the rest of §2's
precondition below, and it is not a field a caller could name: the request still carries the App ID
alone.

The job authenticates with the two
identities the service already verifies on every call: the **job service account's Google ID token**
(Cloud Run IAM admits it for `run.invoker`, and the service checks the `email` claim against the
installation's job accounts, so the IAM bit says *an* authorised identity called and the email says
*which*) and the **run's own budget identity**, the Firebase ID token for `r~<slug>~<run>` the launcher
minted, verified against Google's keys with `fp` checked against the project and the run's liveness
read from the write-once outcome. The token is GitHub's own installation token — **one hour** — minted
on demand and refreshed by the same contract the in-job source holds; it lives in the process's memory
for the run's length, is never mounted and is never written down.

**D893(c) is a precondition, not a channel, and it is the one thing here that can stop the whole
design.** The credentials service **ships dormant**, so the cloud senior reviewer **requires it to be
enabled for the installation**:

- **the opt-in is the owner's**: `fugaro update --provision-credentials` — a typed confirmation at a
  real terminal, never `--yes`, refused in a coding agent's session — and the bundle must hold the App
  key the mint signs with (`fugaro creds set github-app-key`). Both are the credentials service's own
  documented steps, not new ones this design adds;
- **the service must admit the review job's own account, and it does not today.** The variant is a
  **project-scoped** job, and the service refuses a caller whose account belongs to a shared project
  job (Global Constraint 6 of
  [docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md));
  **C3.9**, in that plan's Slice 3, *unscheduled*, is the task that lifts it — and the
  credentials-service routing design **#1514** splits it into **C3.9a–C3.9g**, which is the form this
  design names because that pull request is where the split is written. The same work must
  teach the service four more things a review caller needs: the **review class on `/v1/messages`** that
  **O1** routes the reviewer's calls through, admitted for the account the review run names and for no
  other (this **amends #1514's D1063**, and it is why a review job needs no
  `routing.claude: {via: cascade}` to be admitted — that switch is the operator's consent for a coding
  run's *cascade*, while a review job's credential is decided by its own class); CSR1's
  **read-only** set, which it does
  not serve today (`internal/credsvc/github.go:263` mints the run's own), the repository resolved
  from the run identity rather than from a one-repository job account (above), and **D892(a)'s sign
  and verify endpoints** with their authentication (§6);
- **no `fugaro update` can do that second half.** This design's HARD RULE — the whole upgrade is one
  command — is exactly what it cannot satisfy here, and the honest statement is that the reviewer is
  **blocked on another work stream's task** until C3.9 lands. **CSR7, CSR2 and the coordinator's
  `ship` → merge path are therefore blocked on it**, and the check that tells an operator so
  (`fugaro doctor`, `fugaro validate`) is CSR6's. **E7** in [reconciliation.md](reconciliation.md) §5
  records the dependency.

**The review job writes nothing to the pull request**: not a comment, not a review, not a label. Its
verdict is an artifact, and the coordinator decides what, if anything, to post on its own credential.

**Worktree isolation, and mutation checks in the cloud.** The review job checks out the pull
request's head at the SHA the provider reports (`FetchPRHead`, already what review mode does) into
its **own** filesystem, never the launcher's checkout and never the coding run's worktree, which no
longer exists. **The checkout is bounded, and the bounds are part of the contract rather than
hygiene** — the head is a tree this job did not write:

- **no path is followed out of the worktree.** Every path the checkout and the configuration strip
  touch is resolved with `Lstat` and confined to the worktree root, and a **symlink** is refused or
  removed rather than followed — a head that ships `CLAUDE.md -> /proc/self/environ`, or a
  `.claude/settings.json` that links outside the tree, is exactly the shape that turns a strip into a
  read of the runner's own process;
- **the reviewer's input is size-bounded.** The diff and the tree handed to the reviewer are bounded
  the way D886 bounds the artifact: a diff past the bound is truncated **with the truncation recorded
  in the artifact**, never silently dropped, and a head whose size cannot fit the session's context is
  a **failed review**, not a short one — the same rule, applied to the input side as well as the
  output side;
- **submodules and LFS are not fetched.** The read-only installation token holds `contents: read` on
  this repository and nothing else, so a `.gitmodules` entry naming another repository is not
  initialized and an LFS pointer is left a pointer: recursing would send the run's token to a
  repository the account's `allow_data_to` never admitted, and an LFS smudge would run a filter
  process inside the container that reads the untrusted head. **The review prompt says the tree was
  shallow and unrecursed**, so the reviewer does not report a submodule it never received as a finding
  about the head.

**What the reviewer's session is allowed to load, and where it comes from — D894(a) and O6.**
`review` mode today strips the head's `.claude/` and `.mcp.json` before the stage runs
(`stripClaudeConfig`, `internal/runner/reviewmode.go:253`) and **leaves the head's `CLAUDE.md` in
place** (`:238`). **D894 is answered with option (a): the configuration posture is tightened rather
than the existing strip widened**, and it is tightened **on the `review` stage itself** — the stage
`fugaro run --pr N` already runs — so a review that exists today gets the same posture and the design
does not leave the older path behind it. Three things change:

**The posture is review mode's and not the in-loop reviewer's: it applies to the standalone review job
and to the `review` mode of `fugaro run --pr N`, while a coding run's own in-loop review rounds stay
byte-for-byte as they are today until a later task tightens them.**

- **the head's `CLAUDE.md` and `.claude/rules/` are not loaded at all.** They are the head's own
  instructions to a model, written by the change under review, so a reviewer that reads them is being
  told what to think by the thing it is judging. **O6 is answered: the reviewer is given the BASE
  branch's copies instead**, read from the base ref — not from the head, and not out of the head's
  `.git` — and where the base has none the reviewer is given none rather than falling back to the
  head's;
- **the session runs under managed-only settings.** The deny rules live in the review container's
  managed settings file (`agent.ManagedSettingsPath`, `internal/agent/settings.go:16`), and project
  settings, project hooks and project MCP servers are **refused outright** rather than only deleted
  from the checkout: the strip is a deletion from a tree the head wrote, so a posture that names what
  may be loaded does not depend on that deletion having caught every spelling of every file;
- **and the stage's `HOME` is a fresh directory the job creates**, so no user-level settings file
  baked into the image contributes a rule either. **CSR4a pins both and says which is load-bearing:**
  the deny rules are the managed file's, and the fresh home is what keeps the image's own file out of
  the session. If a fresh home turns out not to work with the agent's own state directory, the
  fallback is a home seeded from the managed file alone — what CSR4a may not do is run with the
  image's home as it stands.

**Account selection in the head is not read either.** A head whose settings name a model or an account
chooses nothing: the account is the launcher's (§1's `--token` over `agent.oauth_token` over
`default`) and the model is the reviewer's pin, so the head's own configuration is not an input to this
run at all.

On a security-sensitive pull request the reviewer may do what an independent reviewer
does by hand: **apply a mutant** to a file in its worktree, **run the named test**, see it fail (or
not), and **revert**. **The revert restores the bytes the runner read before it applied the mutant,
held in the runner's own memory** — not `git checkout --`, which would consult the head's
`.gitattributes` filters, a configured `core.fsmonitor`, a running fsmonitor daemon and any hook the
repository configures, so the untrusted tree would be choosing what the revert runs. Where a git read
is unavoidable it runs with hooks and fsmonitor off, filters ignored and `.git` read-only, and the
mutation path reaches **`gitops`'s push functions not at all** — which is what makes this section's
"never pushes" a fact rather than a convention. That is a scratch edit inside a container that is
thrown away. It
is off unless asked for, it is bounded (one mutant, one named test, a time limit, and a revert
before the stage ends), and the artifact says which test was run and what it did.

**The job never holds a credential it does not need — which is why CSR7's variant is not optional.**
**Under O1 it mounts no secret at all**: not the account's Claude OAuth token (the model calls go
through the credentials service, above), not the App private key (**the difference from the
shared project job it would otherwise reuse**), not **the review-signing key** — D892(a) is answered
with the **service** signing, so no key is mounted and none is readable from the job (§6) — not the
Bitbucket token, not the credentials bundle, not the budget token and not the git signing material.
It is **handed no credential either**: the read-only installation token is minted by the credentials
service and fetched by the job itself, by name (D893(c), above), and the service's URL is the only
thing in its environment that names the service. **The fallback, if C3.9 slips past the point where
the command must ship**, is the token mount this design first proposed — the variant's `secret_env`
carrying the account's Claude token — which is strictly worse and is recorded as a fallback rather
than as the plan (§7). The billing identity this design chooses is the thing to check against the tree: the
variant it inherits its shape from is the **shared project job** of `(profile, account)`, whose bucket
condition is the whole of `runs/` (`gcp.ProjectBucketCondition`, `docs/design/bucket-iam.md` §2.1) —
the accepted, installation-wide reach the owner ruled on 2026-10-17. A review job needs to write one
run's own record and artifact, so that reach is more than it needs; narrowing it is a bucket-layout
change this design does **not** make and instead records as a finding (**F15**). Note what the
variant therefore *keeps*: the reach, and the `CLOUD_RUN_JOB` identity the runner matches itself
against. `fugaro doctor`'s job census (`internal/cli/doctor.go`) has to learn the suffix, or a
converged installation reports a job it does not know about.

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
  "created_at": "2026-10-31T21:14:07Z",
  "repository": "owner-repo-a-1a2b3c4d",
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
reader never has to read JSON to understand a review — **and it is never published verbatim.** **O5
is answered:** what a coordinator may post is a **bounded, re-redacted summary** it derives itself,
never the artifact's own bytes, and **never on a public repository**, where a review of an untrusted
head would publish that head's prose under the installation's name. The artifact is a document for
the run's own bucket; the comment is the coordinator's, and the coordinator owns every word of it
(§6).

**Nothing of it leaves the run unredacted — and the redaction happens before the signature, not at
publish time.** Both artifacts are reviewer prose generated from an untrusted diff: `review.md` is
what a coordinator (or a person) may paste into a comment, a pull request description or a chat, and
`review.json` carries the same prose in its `overview`, `strengths`, `issues[]`, `security`,
`performance` and `test_coverage` fields. **So the writer redacts both, in the run, before the digest
is taken.** The order is a rule rather than a preference: D892(a)'s signature covers the artifact's
exact bytes (§6), and a redaction applied later, at publish time, would change those bytes and break
every signature over them — the redacted bytes are the signed bytes. Both pass through
`internal/agent/redact.go`, exactly as the transcript does (§7). Today that pass covers what is
published *from agent output*; this design names the artifact as one of those things, and CSR3 is the
task that has to make it true rather than assume it. **Redaction is a literal-value match, and §7
states what it does not reach.**

**The fields, and where they come from.** `severity` is the vocabulary the runner already reads
(`blocker`/`major`/`minor` — the in-job spec's §6; `runstore.Finding` keeps `Severity` free-form and
reads the three anchored axes `likelihood`, `impact` and `fix_cost` instead,
[docs/rigor-modes/](../rigor-modes/spec.md)).
`class` is the closed set `runner.VerdictSchema` already carries: `correctness`, `performance`,
`security`, `data_loss`, `maintainability`, `cosmetic`. `file:line` is one location, and **the line
is a claim the reviewer makes about the head it read**, so `head_sha` is recorded beside it and a
reader that finds the file moved knows which commit the line meant. `fix` is a suggestion, never a
patch the reviewer applied. **The four sections the in-job template has become fields rather than
prose** (`security`, `performance`, `test_coverage`, plus `overview` and `strengths`), so a
coordinator can route on them without parsing English.

**`created_at` is the artifact's own clock, not a lookup.** It is the review job's own timestamp,
**RFC 3339 in UTC**, written by the artifact's writer at the moment it writes the object, so a reader
holding only `review.json` can still say when the review was performed. **Why it is a field and not
"read the run record":** the record and the artifact are two objects, and the artifact is the one that
travels — it is copied into a fix round's brief (CSR5), may be pasted into a comment or a chat, and may
outlive the run's own listing in a later audit — while the record does not travel with it. §6's
ordering argument is about *when the review happened*, and that is a claim the artifact should be able
to make about itself. `created_at` is the **writer's** claim and is not trusted on its own: the
coordinator reads it **beside** the run record's own timestamps (§6), and the signature (D892(a)) is
what binds the whole object, this field included, to the **account's key in the credentials service**.
So a forged
`created_at` is a forged artifact and fails verification; an honest one makes the artifact
self-describing.

**`repository` is the run's own slug, and it is the first of the four things the signature binds.**
The spelling is the one the artifact's own path uses — `runs/<slug>/<review-run>/artifacts/` — so the
coordinator, which names the run by slug, compares like with like. It is a field rather than
something a reader infers from the path because a **copy** of the artifact loses the path: an object
pasted into a fix round's brief, a comment or a later audit carries no prefix to read a repository
out of. With `review_run`, `pr` and `head_sha` it is what makes a verdict a statement about **one
review of one pull request at one commit**; §6 states the four checks the verifier makes on them, and
why the signature is what makes those checks worth doing.

**The signature is not a field of this object.** It is a **sidecar object beside it** —
`runs/<slug>/<review-run>/artifacts/review.json.sig`, one line of lowercase hex — so that the bytes
that were signed are exactly the bytes that are stored, and a reader can hash the object without
first having to remove a field from it. An artifact whose sidecar is absent is refused, not accepted
by default (§6).

**`mutation_checks[].observed` is a closed four-word vocabulary: `pass`, `fail`, `error`, `timeout`.**
The example above shows `fail` because that is the *good* outcome — the mutant was killed. A
coordinator routes on this word without parsing prose: `pass` means the mutant **survived**, which is
the finding worth reporting; `fail` means the test killed it; `error` means the test or the harness
did not run to a verdict; `timeout` means the bounded run expired. **A fifth word is refused**, for
the same reason §3's verdict refuses a fourth: a reader that compares against the four would take
anything else for one of them. `reverted` is the runner's own statement that the worktree was put
back, and `applied` is the mutation's description — neither is a claim by the reviewer's prose.
**CSR4b's `TestMutationObservedVocabularyIsClosed` pins this vocabulary** — every `observed` value the
task can write is one of the four, and a fifth is refused — and it asserts the `error` and `timeout`
arms rather than leaving them to the writer's discretion. `TestMutationAppliesRunsRevertsAndPushesNothing`
is CSR4b's too, and what it pins is the revert and the absence of a push, not these four words.

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

**The review stage's own result *is* this artifact — it is not an in-run verdict.** The review job
runs the same `review` stage review mode runs, and that stage today passes `runner.VerdictSchema` as
its `--json-schema` and reads the result with `runner.ParseVerdict`
(`internal/runner/reviewmode.go:444` and `:465`). **The coding run's review rounds are no longer this
reader:** `seniorLoop` (`internal/runner/runner.go:2675`) runs its rounds through `seniorRound`
(`:2728`), which reads with **`TryVerdict`** (`:2746`, `:2810`), asks once more when an answer carries
no parseable verdict (`noVerdictRetries`, `:2715`) and then lets the round through — so review mode
and diagnose (`internal/cli/diagnose.go:246`) are `ParseVerdict`'s only callers, and review mode is
the one that must have an answer. Two words and `required: ["verdict", "findings"]`. **A review job
that emitted its findings
only into the file would hand that reader a result with no verdict in it**, and `ParseVerdict` would
answer with a finding of its own — *"review produced no parseable verdict: nothing in the result was a
verdict"*, severity `blocker` (`internal/runner/verdict.go:64` is the literal, `:99` the `why` text) —
so a complete, careful review would be recorded as an unreadable one, and in review mode the run's outcome would be decided by a sentence
about the parser rather than by anything the reviewer found. **So the review job passes
`schemas/review.schema.json` as its `--json-schema` and decodes with the artifact's own reader**; the
artifact's three words and the in-run pair share no reader *and no producer*. Nothing about the two
words changes for the coding run, whose `review` stage keeps `VerdictSchema` exactly as it is.

**And when the artifact cannot be read, the run is failed — not given a verdict it did not write.**
The in-run posture synthesises a `blocker` finding so the loop fixes what it could not read; the
independent review has no loop to fix it and a coordinator that reads artifacts, so a synthesized
finding would be a code finding invented by the parser. The review job ends as an **invalid verdict**
(`endReviewInvalidVerdict`'s shape: `StatusFailed`, `OutcomeNone`, the reason the decoder's own,
`internal/runner/reviewmode.go:815`), and §6's rule holds — an unreadable verdict is never a `ship`.

**The artifact is versioned like every other wire shape here.** A new field is additive and keeps
`schema_version`; removing or retyping one bumps it; a field whose meaning must change gets a new
name (`CLAUDE.md`'s "a field never changes meaning"). The JSON Schema lands in `schemas/` with the
task that first writes the artifact, and the console and the coordinator read the version rather
than the field list.

**What the review job contributes to the run record.** The record already carries `reviews[]`
(`runstore.ReviewSummary`: `round`, `tier`, `verdict`, `findings` and the head the verdict read) and
`runstore.Cost`. A review job's record is a run record like any other: mode `review`, the PR's number
**as the record's own `pr` link** (`runstore.PRRef`) beside the head it read in the record's own
`head_sha` (`internal/runstore/runstore.go:648`), the account it ran as (`runs ls`'s
`ACCOUNT` column and `fugaro diagnose`'s `Account:` line already exist), and its cost as **notional**
— the subscription's calls are not gateway-priced. **Two of those are not provenance but the cap:**
the **PR link** is what makes the review job a member of the pull request's chain, and the record
carries **exactly one `reviews[]` entry** — the round, the verdict and the findings count — because
the chain counts its rounds as the `reviews[]` entries the runner writes (D1058,
[docs/run-limit-carry-over/](../run-limit-carry-over/spec.md) §8: a review job on PR N joins the PR's
chain, and the chain's `--max-review-rounds` counts senior-review iterations, which is what bounds the
fix ↔ review loop). A review job that wrote its verdict nowhere the chain can read would be an
iteration the cap never sees. The artifact is the review's substance; the record is its provenance
**and its place in the chain**.

**The record's `tier` gains a value, and its `verdict` does not gain a word.** **O4 is answered:** the
record's `reviews[].verdict` keeps the two words it has today (`ship`|`changes`; the field is a free
string, `schemas/result.schema.json:45`, and the same pair is the closed enum on `reviewed_pr`'s own
entry, `:291`), because a field never changes meaning and every reader of a record compares it against
those two. What changes is `tier` (`:44`, today `first`|`senior`|`adversarial`): it gains
**`independent`**, the value a review job's single entry carries, and that value is **display-only** —
it tells a reader which reviewer wrote the entry and **carries no authority**. The **signed artifact
is the only authority** on the independent review's outcome, and the record's `changes` is what the
chain's cap counts, not what a coordinator ships on (§6). So **CSR3 lists `schemas/result.schema.json`
among its files**: `tier` is a published wire shape the console and the history read, and adding a
value to it is a schema change with a version to check, not a field a writer may invent.

**And a signature that cannot be produced ends the run failed.** D892(a) puts the signing call on the
job's critical path: the artifact is written, its digest is taken and the service is asked to sign it.
**O4 is explicit about that failure arm** — a sign call that fails, whether the service refuses, the
network drops or the account is not admitted, ends the run **`StatusFailed`** with the reason the
service gave, and **never** a run that reports `ship` with an unsigned artifact. There is no
"unsigned but otherwise fine" state to fall into: §6's verifier refuses an artifact with no sidecar,
so a run that shipped one would have produced a verdict no coordinator may act on while looking, in
the record, like it had produced one.

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
| **Credential** | the coder's job credential, in a container that also runs repository code | its own job **variant** and service account (§2), **no token at all** (O1: the model calls go through the credentials service), a **read-only** forge token fetched by name, and no App key |
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
§1's rule (`--token` over `agent.oauth_token` over `default`), and prints the review run's id.
`--mutate <file:test>` asks for a mutation check (§2), and `--json` prints the **launch's** own result
(the run id, the job, the account) as JSON, like every other command's `--json`.

**The launch refuses where the precondition is not met, and says which half is missing.** Because
D893(c) requires the credentials service (§2), `fugaro review` on an installation that has not opted
in — or whose service does not yet admit the review job's own account — is **refused before the job is
started**, with the reason named: the service is off (`fugaro update --provision-credentials` is the
owner's step, at a real terminal), or the service is on but this installation's service refuses a
project-scoped caller (the C3.9 half, which no `fugaro update` can supply). A launch that started
anyway would end as an infra fault inside the job, where the operator sees a refusal from the service
rather than the sentence that says what to do. **`fugaro doctor` reports the same two facts** for an
installation that has named a review account, and both are CSR6's to build with the rest of the gates.

**Reading the artifact back is the same command family's second verb, and it ships with the first.**
`fugaro review show <review-run>` prints `review.json` and `review.md`; `fugaro review show <review-run>
--json` prints the artifact's JSON alone. **An earlier draft of this document had the launch itself
printing the artifact** — which cannot be true of a launch, because the verdict does not exist until
the job has finished — and `show` appeared only in the plan's later task. **Both verbs are CSR2's**,
and §8's live check uses both.

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

**This command supersedes an earlier answer, and the reason is the credential.** **O2 is answered:**
the in-job-review design's reconciliation, **Q16**, answered *"extend `review-only` rather than add a
second entry point"* (`docs/in-job-review/reconciliation.md:370`, answered there as option 1) — and
that answer is superseded here, not because it was wrong about the recipes but because **a recipe
cannot change a job's credential**. No extension of `review-only` can put the reviewer on a named
account in a container the coder never ran in, which is what §2 and §4 exist for, so the second entry
point is the one this design adds and the older question's answer no longer decides the spelling. The
supersession is recorded here, in [reconciliation.md](reconciliation.md)'s D880 and in
[README.md](README.md); that document is another work stream's and this round does not edit it.

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

**`ship` → the coordinator merges** (or tells the owner to). **The verdict is advice and the merge is
the coordinator's act, taken after the pull request's CI, the readiness rule the run already computed
and a person's own look**; the artifact's `head_sha` is the commit the
advice is about, and a pull request whose head has moved since gets no merge on that verdict. **The
signature comes first** (D892(a), below): the coordinator verifies it before it reads the verdict at
all, and a verdict that does not verify is never a `ship`.

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

**What the coordinator may publish is its own words, never the artifact's.** **O5 is answered:**
`review.md` is **never posted verbatim** — not as a comment, not in a pull request description, not in
a chat — and **not on a public repository at all**, where reviewer prose derived from an untrusted head
would be published under the installation's name and outside the run's own bucket. A coordinator that
has something to say derives a **bounded, re-redacted summary** of its own and posts that. §3 states
the rule at the artifact; this is the same rule at the reader, and it is the second reason the review
job writes **no comment and no pull request of its own on any path** (§2).

**A verdict that cannot be read is not a `ship`.** A missing, unparseable or truncated artifact, a
review run that failed, or a review that ended without writing its artifact is an **infra fault the
coordinator reports**, never a silent approval — the same posture `runner.ParseVerdict` takes when a
review produces no parseable verdict, which is to refuse the pass. The review job refuses it the other
way round from the in-run path, and §3 says why: the coding run turns an unreadable verdict into a
`blocker` finding for the loop to fix, while the review job has no loop and **ends the run invalid**,
because a `blocker` sentence about the parser, stored beside real findings, is a code finding nobody
wrote. Either way the coordinator sees a run that did not produce a verdict and says so.

**The verdict artifact's integrity.** A coded run must not be able to forge its reviewer's verdict
(§7). What makes that true, honestly: the review run's id is minted by the launcher **after** the
coding run's job has ended, so *that* coder would have had to write a path under an id that did not
exist yet; and the artifact's provenance is the review run's own record — its id, its account and its
timestamps — which the coordinator reads beside the object. **The artifact also carries its own
`created_at` (§3), so the object is self-describing**: a reader holding only `review.json` — a copy in
a fix round's brief, in a comment, or in a later audit — can say when the review was performed
without the record travelling with it. The record stays the authority the coordinator checks that
timestamp against; the artifact's own field is what makes the claim portable. This is a real property
of the ordering and, on its own, **not** a signature: nothing in the object is cryptographically bound
to the review job, and the job's bucket condition reaches every run's prefix (F15) — which is the gap
D892(a), below, closes.

**The ordering argument covers the reviewed pull request's own coder and stops there.** It says
nothing about **another run executing at the same time on the same shared job**: that job's account
can write any `runs/<slug>/<run>` prefix, so a concurrent run of a *different* repository on the same
profile — or a second run of this one, waiting behind the review — can write under any prefix it can
name. What the design can honestly claim is narrower than that: the review run's prefix does not
exist while the coder's job runs (`task.NewRunID` mints a timestamp and four hex digits *at launch*),
and the coordinator reads the artifact **beside the review run's own record**, so an object written
under a prefix of the writer's own choosing is an orphan rather than a verdict. A concurrent job is a
different matter: that account holds `objectUser` on the whole of `runs/`, which reaches
`storage.objects.list`, so once the review run exists such a job can **see** the prefix and write
under it — for that writer the ordering buys nothing at all, and only the narrowing F15 records as
out of scope, or a signature over the artifact, would. **The design records that gap rather than
claiming a boundary it does not have.**

**D892 is answered, option (a): the verdict is signed, and the signature is what the coordinator
acts on.** Recording the gap was not enough, because the design's own `ship` path — *the coordinator
merges* — is what the gap would defeat. **The signature is produced by the credentials service, and
the signing key never enters a container that runs untrusted code.** The review job does not hold it
and does not mount it: that container runs a test from the head under review by design (§2, D885), so
a key in its environment would share an OS user and the `/proc` residual with the account's OAuth
token and be reachable by exactly the read §7 records as what the separate job cannot close. The
owner's ruling is that a key whose compromise forges **every** verdict of that account is not worth
that read. The flow is one call:

1. the artifact writer (CSR3) computes **SHA-256 over `review.json`'s exact bytes** — the digest, and
   not the object, because the object is reviewer prose derived from an untrusted diff and there is
   no reason for it to travel to the service at all;
2. the job **posts the digest** to the credentials service's sign endpoint, authenticated by the two
   identities the service already verifies on every call (§2): the job service account's Google ID
   token, whose `email` claim the service checks against the installation's job accounts, and the
   **run's own Firebase identity**, whose claims name the repository (`fs`), the run (`fr`) and the
   pull requests it may write (`fpr`);
3. the service answers with the **HMAC-SHA-256 over a framed message under the account's
   review-signing key**, lowercase hex. **The algorithm is fixed by this design, not left to the
   implementation:** the signer is the service's code and the verifier is a second call into it, and a
   design that named no algorithm would let the two ends disagree — or let one of them accept a weaker
   construction — with no error on either side.
   **O3 is answered: the message is framed, and it is not the bare digest.** What is signed is the
   digest **together with the two identities the service verified on the call** — the run's Firebase
   identity (`fr`, the review run) and the repository claim (`fs`) — concatenated in a fixed order
   with fixed separators, so a signature cannot be lifted from one review run to another even where two
   artifacts happen to hash alike, and so what the signer attests to is *who asked* as well as *what
   was asked about*. **The artifact writer sends only the digest** (CSR3's call); **the frame lives in
   the service** — built in `internal/credsvc` from the `fs` and `fr` it verified (C3.9e, D1073), and a
   sign request that names a run or a repository is refused — and **the verifier re-frames from its own
   copy of the two identities** rather than from anything the artifact or the caller asserts — the
   verify call presents the digest, the signature and the same two claims, and the service answers yes
   or no;
4. the job writes the answer into the **sidecar object beside the artifact** (§3) —
   `runs/<slug>/<review-run>/artifacts/review.json.sig`, one line of lowercase hex — and not into
   `review.json`, so the bytes that were signed are the bytes that are stored.

**What the signature binds, and what the verifier refuses.** The digest covers the object's exact
bytes, so every field of it is bound — `repository`, `pr`, `head_sha` and `review_run` (§3) among
them — and a field cannot be altered without the signature failing; O3's framing binds the run and the
repository beside the digest, so a signature cannot be moved to another run's frame either. That alone
would still let a **whole verdict be replayed**: an honest artifact of an *earlier* review of the same
pull request is a valid signature over a valid object, and it is the wrong answer to the question
being asked. So the
verifier refuses unless all four agree with the present moment: **`review_run` equals the run the
coordinator launched** (the nonce — the launcher mints that id after the coder's job has ended, and
the coordinator asks about the run *it* launched), **`repository` and `pr` are that pull request's**,
and **`head_sha` equals the pull request's current head** — which is also §5's rule that a verdict
about a commit that is no longer the head is a verdict about nothing. **The signature is what makes
those four checks worth making:** without it they would be checks on a document that anyone able to
write under the prefix could have written.

**Who holds the key, and what it costs.** The key is the **credentials service's**: a Secret Manager
secret in the installation's project, **one per account**, read by the service's own service account
and by nothing else — not the review variant, not the shared project job, not the coder's job, not
the coordinator. **Verification is a call to the same service**, for the same reason: a second holder
of the key is a second place it can leak from, and the whole point of this answer is that there is
one. A verify call presents a digest and a signature and gets a yes or a no; the service never sees
the artifact. **A verdict that does not verify is not a `ship`** — it is treated exactly like an
unreadable artifact, an infra fault the coordinator reports.

**What C3.9 has to grow, because none of this is built.** Both endpoints are new work in the
credentials service, and so are the four things they need: **their authentication** (the two
identities above for a sign call; the coordinator's own credentials for a verify call, which is a
caller class the service does not have today); **which repositories and runs may request a
signature** (the run identity's `fs` and `fpr` against the account's allowed repositories — the
launcher's `allow_data_to` check plus the minting of the run's Firebase identity, and nothing a
caller sends); **per-account key rotation** (below); and the **audit line** a sign call owes on the
terms C1.13 already sets for a mint, because a signature is an act that record exists for.

**Rotating the key, and what rotation costs.** The key is a Secret Manager secret with **versions**,
and rotation is the service's own operation: a new version the service signs with, and a window in
which it accepts a verification against **the version before it and nothing older**. **An artifact
signed under a retired version stops verifying**, and the design says so plainly rather than
pretending otherwise: the remedy is not to trust the old object but to **re-review the head**, which
is cheap next to merging on an unverifiable verdict. What rotation never does is invalidate the
*decision* — the coordinator acts on a verdict once, at the time it verifies it, and a merge already
made is not re-litigated. There is no key escrow and no re-signing path, and **a leaked key forges
every verdict of that account until the rotation**: that is the cost of the one-holder design, and
it is why the key is kept out of the container that reads an untrusted diff. **Honest about what this
does and does not close:** it stops **another job** forging a verdict; it does not stop an injected
*reviewer* from writing a dishonest one inside its own run, which is §7's residual and which no
signature reaches. That residual is why **the verdict is still advice and the merge is still the
coordinator's act, taken after the pull request's CI, the readiness rule the run already computed and
a person's own look**.

---

## 7. The threat model

**The OAuth token's blast radius in a cloud job.** A subscription OAuth token is **a person's**: it
is usable anywhere Claude Code runs, it has no dollar cap, and it shares the owner's own rate limits
and usage windows, so stealing it costs the owner their interactive work as well as the
installation's. Who may use whose is decided in two places and nowhere else: the account's
`claude_accounts.<name>.allow_data_to` in the owner's local config, which the launcher checks at
launch; and, where the account is shared beyond one person, the policy's reference record with its
own `allowed profiles/repositories` and the **one job service account per (profile, account)** that
mounts exactly that person's secret (`project-centric.md` §7, `project-named-oauth-tokens.md`).

**What this design adds, stated at its true size.** **Under O1 the review job variant mounts no token
at all** (§2): the reviewer's model calls go through the credentials service, so the container that
runs a test from the head under review holds no OAuth token to read. That is the change O1 makes, and
it is a change to this design rather than a claim about it. What stays true, and is why this paragraph
exists, is that **the account's other jobs still hold the token**: `fugaro run --token team-a` puts the
same token on the shared project job, because that is what `agent.oauth_token` means today. **So the
older claim in this document — "the coder's job does not have it, so the container a
prompt-injection reaches has nothing to steal" — was false**, and it was false twice over:

- a **coding run on a named account already mounts that account's token**, so the container that runs
  the pull request's code *does* hold it, exactly as `mixed-credentials.md` §6 describes — and **O1
  does not change that**: a coding run is not a review run, and its model calls are the ones the
  gateway prices today;
- the **review job itself runs repository code** (§2): the mutation check runs a test from the head
  under review. **Left alone, the token would be reachable from that stage** through its own
  environment (`CLAUDE_CODE_OAUTH_TOKEN` is a mounted secret env var), through `/proc`, and through
  **network egress**, which a job has by default and which no deny rule closes — `denyWeb` denies
  Claude Code's `WebSearch`/`WebFetch` **tools**, and does not stop `curl` from Bash. **O1 is what
  takes that token out of this container altogether**; the fallback puts it back, which is the whole
  reason the fallback is recorded as strictly worse.

**D891 is answered, option (a), and the answer closes two of those three.** The mutation test is run
by the **runner** as its own subprocess with the token scrubbed from that subprocess's environment and
**egress denied**, and **Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` are denied for the rest
of the review stage** (`DenyRules` gains them), so the agent's own shell stops being a general
exfiltration path, the agent cannot rewrite the tree it is being checked against while the check runs,
and the reviewer's environment and the network are no longer routes out. **Under O1 there is no token
in that environment for the scrub to remove** — the environment route is closed by not mounting the
token at all, and the scrub is what remains for the fallback — so what D891(a) still has to close is
`/proc` and the network. **How egress is denied for a subprocess inside a Cloud Run container
is UNVERIFIED** — a network namespace or `unshare` in gen2 has not been checked, and
[reconciliation.md](reconciliation.md) §6's **U7** is where that is settled. **U7 asks about `/proc`
in the same breath, because the two residuals have the same shape of answer:** if the child can be
given **its own PID namespace with its own `/proc` mount, or run under a different uid**, then neither
the parent's `/proc` nor the parent's environment is readable from the stage that runs the head's test;
if neither can be done, `/proc` stays open and the design says so rather than assuming a fence. **So
the answer is conditional, and the design says so:** if a child process cannot be fenced, D891(a)
cannot be implemented as written, the fallback is the owner's **(b)** or **(c)**, and the owner is
asked again — which is why **U7 is an owner-run live observation taken before CSR4b launches**, rather
than an assumption the task starts on. **It is a standalone probe, and §8's step 6 is where it is
written**: the question is a property of the deployed gen2 execution environment — can a child process
be denied the network and given a `/proc` of its own — so it is asked directly, by a probe the owner
runs on the sandbox installation, and **not** through `fugaro review --mutate`, which would need CSR2's
command and CSR4b's mutation code to exist before the answer that decides whether CSR4b should exist.
**What is left either way is the `/proc` read, and O1 is what makes it small:** with the token gone
from the container, the best case for a process that reads `/proc` is the **one-hour, read-only
installation token** the runner holds in its own memory, not the person's subscription token. That is
a real reduction and not a closure, and only U7's fence moves it further.

What the design still buys, and it is worth having: a container that **did not run the coder**, a
read-only forge scope, no App key in that container, a run whose whole purpose is the review — and,
**under O1, no OAuth token in it at all**. What it does **not** buy is a container with nothing
readable in it: no scrub and no egress rule reaches a process that can read `/proc`, and the honest
statement of the residual is that what such a process can reach is the review job's own read-only,
hour-long credential rather than the person's.

**The read-only forge scope is one scope among several, so the job holds no minting key — in the
variant CSR7 adds.** The review job holds `{contents: read, pull_requests: read, metadata: read}` —
minted by the credentials service for a review identity, never asked for by the job — and, **on the
job variant of §2 and not on the shared project job**, no GitHub App private key: a key would let the
job mint a write token whatever the launcher asked for, so the scope is the **service's** decision and
not a permission the container could widen. The job's own request carries the App ID and nothing else
(§2), so it cannot even name a wider set. The consequence is that the review job cannot push,
cannot merge, cannot comment and cannot label; whatever is posted to the pull request is posted by the
coordinator, which already holds the write credential this installation already trusts. The scope
itself is a **provider-side** property: it bounds what the *token* can do, and says nothing about what
the *container* can do with the token it holds.

**Prompt injection from the pull request's own diff.** The reviewed code is untrusted input. A
reviewer must read it — that is its job — so the injection surface is not removable, only bounded:

- **what the job can take is bounded, not absent.** It holds no write credential, no App key and no
  other account's secret — and, **under O1, no OAuth token either**, so the container that runs the
  diff's code holds no model credential to steal. The *coding* run on a named account still does, and
  this section's first paragraph says so; the fallback is what would put the token back in the review
  container, and that is the whole reason it is the fallback;
- **the token's revocation is the provider's, not ours.** `fugaro accounts rm` deletes the local
  record and the installation's secret; it does not revoke an OAuth token that has already left, and
  it cannot — only the person's own account settings can. What the accounts design offers instead is
  **rotation by re-running `add`**, and the mark (`accounts ls`) is how a dead or exhausted token is
  seen. A stolen token therefore stays valid until the person rotates it at the provider, and the
  honest mitigation is that the review job's token is the *account's*, not the installation's, so the
  blast radius is one person's subscription rather than everyone's;
- **the job writes nothing outward it was given a way to write.** No comment, no push, no artifact
  outside the run's own prefix, and no web **tool** (`denyWeb: true`, which the held plan's P10 already
  calls for). **`denyWeb` is a tool rule, not a network rule**: it does not stop `curl` or anything
  else a shell can run against a job's default egress. **D891(a) is what closes that** — a
  token-scrubbed subprocess with egress denied, and Bash, Edit, Write, NotebookEdit and
  `Read(/proc/**)` denied for the rest of the stage — and nothing in this paragraph rests on `denyWeb`
  alone;
- **everything the reviewer says is treated as data downstream.** The artifact is read and rendered,
  never executed; the findings that become a fix round's brief are quoted and bounded (§6);
- **what this does not close, stated plainly:** an injected reviewer can put anything it likes into
  its verdict, including a dishonest `ship` — and, because the artifact is text the run writes,
  **it can also encode a secret it can read into `review.json` or `review.md`** rather than say it
  aloud. **Redaction does not close that, and the design says why:** `internal/agent/redact.go`
  replaces the **literal values** it has registered (a secret's raw form, its JSON-escaped form, a
  multi-line secret's own lines, and the encodings `encodedForms` lists), and what it does not reach
  is stated rather than glossed: the encodings it lists are the four base64 alphabets (including the
  alignment-independent cores, so a secret embedded in a longer encoded header or env dump is caught),
  the URL escapes and the wrapped line pieces — **so an encoding it does not list (hex, gzip, an
  encryption), a value the reviewer splits, reorders or truncates across the text, or a secret that
  was never registered at all is not matched.** The artifact is redacted **before
  it is signed** (§3), which makes the signature cover what the redactor left, not what it should
  have caught. **What that costs:** a verdict artifact can carry a secret-shaped payload out of the
  run even with every scrub in this design working, which is one more reason the verdict is advice
  and the artifact is read by the coordinator rather than published. The mitigation for the dishonest
  verdict itself is not technical — it is that **the verdict is advice and the merge is the
  coordinator's act, taken after the pull request's CI, the readiness rule the run already computed
  and a person's own look**. That is the same posture `SECURITY.md` takes on a review verdict
  elsewhere: advice, not evidence.

**The verdict artifact's integrity.** Covered in §6, and **D892 is answered — option (a)**. The
ordering (the review run's id does not exist while the coder runs) remains a real property **for that
coder**, and it is not one for a concurrent job on the same shared job, which can list `runs/` and
write under the prefix it sees. **What closes that is the signature the owner chose:** the **credentials
service** signs **O3's framed message — the artifact's digest together with the verified `fr` and
`fs`** — with the account's key, a key that never enters a container that runs untrusted code, and
**the coordinator verifies it before it acts on a verdict**; a forged,
unsigned or replayed artifact is refused rather than merged (§6). The bucket-prefix narrowing (the
owner's other option) is still not built, and §6's account of
why it belongs to the project-job work stands. **What no signature closes** is the injected reviewer
writing a dishonest verdict inside its own run: that is the residual this section's last bullet
already states, and it is why a verdict is advice rather than evidence.

**Cost runaway, which a subscription hides.** A review job is not priced by the gateway (§1), so none
of the dollar caps is a bound on it — **the loop's bound is the pull request's chain instead**:

- **the fix ↔ review loop is bounded by the chain's rounds, and only when a chain is set.** `fix
  first` launches a fix round (§6) and the coordinator then asks for another review of the new head;
  **the review job joins the pull request's chain by its PR link**, and the chain's
  **`--max-review-rounds`** counts senior-review iterations, so the loop is capped where the rounds are
  counted rather than in this design (D1058,
  [docs/run-limit-carry-over/](../run-limit-carry-over/spec.md) §8 and its reconciliation F16; the
  record the chain reads is §3's). **Three honest limits on that answer:** the cap is **opt-in** — a
  launch that sets no chain limit gets no chain, so nothing but the wall clock below bounds the loop —
  it is a **rounds** cap, never a money one, because a subscription's calls do not reach the
  chain's money ledger at all, and that design's §4 calls the round count **advisory**: it is read off
  the run records and no database rule enforces it, so this bound is one an honest coordinator honours
  rather than one a rule stops it at. A design that looped until `ship` with no chain set would loop
  until a person noticed;
- **the run's own bound is a wall clock, not a cost.** A review job inherits a task timeout (the
  profile's `timeout_class` → the deployed job's `task_timeout_s` → Cloud Run's task timeout, plus
  `backend.TaskTimeoutSlack`, `internal/config/config.go`), and the launch sets the execution's own
  timeout from the workflow's `timeouts.total` (`internal/backend/gcp/run.go`'s
  `Overrides.Timeout`); `agent.max_budget_usd` is passed to Claude Code as `--max-budget-usd` and is
  **Claude Code's
  own estimate**, accurate for a Claude model and about twenty-five times too high for a provider
  model (`docs/multi-model.md`) — an estimate, not an enforcement;
- **and the subscription's usage windows are shared with the owner's own work.** Every review job on a
  named account spends the same weekly and five-hour windows the person spends interactively, on the
  *same* account: an expensive review does not cost dollars, it costs the owner their session. That
  is the bound the owner actually feels, and the design states it rather than leaving it implicit.

The live check observes the accounting (§8's step 5); nothing here enforces a figure — the one bound on
the loop is the chain's own rounds cap, and it is the chain's, and advisory there — and the design does not
pretend to.

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
   the `fugaro accounts add <name>` of [docs/accounts-add/](../accounts-add/README.md) (**merged** as
   #1470 as a design; the command is still unbuilt) or its `--claude` login — which, per the owner's
   ruling on that design's **D862**, is **two acts**: the login runs in **the owner's own terminal**
   and the owner **pastes once** afterwards, with **no pty and nothing parsed from the login's
   output**, so this check reads nothing back from it either ([reconciliation.md](reconciliation.md)
   §6's **U2** is moot for that reason). The command stores the token, declares the account and its
   repositories, and converges the policy and the deployed job set. Read back: `fugaro accounts ls` names the account
   and its state; `fugaro doctor` raises nothing about it. **And check the precondition this design
   cannot do without (D893(c)):** the credentials service must be **enabled for the installation** —
   `fugaro doctor` reports the service deployed and the installation opted in, which is the owner's
   `fugaro update --provision-credentials` at a real terminal, with the App key in the bundle
   (`fugaro creds set github-app-key`), and the launch refuses without it (§5). **Stop here if either
   is missing** — the review job mounts a job that must already exist, and it fetches its read-only
   token from that service.
2. **Launch a cloud review of a known pull request.** On a sandbox repository with a pull request
   whose defects the owner already knows, run `fugaro review --pr N --token <name>` and record the
   review run's id. Watch it with `fugaro watch <run>` or `fugaro logs <run>`; it must end, not be
   killed, and its log must name no token value.
3. **Read the verdict artifact back.** `fugaro review show <review-run>` (or the artifact read
   directly from the runs bucket) prints the JSON and the markdown. Check five things by hand: the
   artifact's `head_sha` is the pull request's head; the verdict is one of the three words; the
   issues the owner already knew about appear, with a `file:line` that lands on the right line of
   that commit; the **markdown and JSON agree** section for section; and **the signature verifies** —
   D892(a)'s own observation, over **O3's frame** (the digest and the two claims §6 names), and the
   one a reader can also test the other way round by flipping one byte of the stored JSON and
   confirming the verifier refuses it. Two more, and they are the replay
   check: the artifact's `repository` is this repository and its `review_run` is the id this step
   recorded, and an artifact of an **earlier** review of the same pull request — one whose `head_sha`
   is no longer the head — is refused rather than acted on (§6). **And the record, not the artifact,
   is where O4 is observed:** `fugaro review show <review-run> --json` prints the artifact, and the
   run's own record shows one `reviews[]` entry whose `tier` is `independent` and whose `verdict` is
   one of the record's two words — the artifact's three-word verdict never appears there (§3).
4. **Check what the job could not do.** Confirm from the pull request that the review job posted
   **nothing** — no comment, no review, no label — and that `git ls-remote` shows no branch the
   review run pushed. This is the read-only scope's positive test: it is the absence that has to be
   observed, because a write permission that was never exercised still exists.
5. **Check the accounting.** The review run's record shows the account it ran as and a **notional**
   cost; the installation's caps are unchanged by it, and `fugaro budget show` shows no counted
   spend for it. This is the subscription-versus-gateway property from §1, observed rather than
   assumed.
6. **Probe the execution environment's fence — the network and `/proc` both — and this is the one step
   that runs *before* the task it decides.** **U7** ([reconciliation.md](reconciliation.md) §6) asks
   whether a child process inside this deployment's gen2 execution can be denied the network at all
   **and given a `/proc` of its own**, because D891(a) assumes the first and §7's residual is the
   second. **The probe is standalone: it needs no `fugaro review`, no `--mutate` and no mutation code
   from CSR4b** — those are the things whose existence depends on its answer, so asking the question
   through them would be circular. On the sandbox installation, the owner runs a one-off execution of
   the deployed job image whose entrypoint forks a child that (a) tries to reach a host on the
   network **and the instance metadata server**, (b) tries to read the parent's environment through
   `/proc`, and (c) is fenced — a network
   namespace (`unshare -n` where the image has it, or the equivalent the platform allows), **and a PID
   namespace with its own `/proc` mount, or a different uid, whichever the platform allows** — and
   records what the child could do. **What is being observed is one of three things:** the child
   cannot reach the network and cannot read the parent's `/proc` when fenced, and both fences are
   available; a fence is unavailable in this execution environment; or a fence is available and does
   not hold. The second and third amend D891's answer and the owner is asked again between **(b)** and
   **(c)**; the design does not quietly fall back to `denyWeb`. **Why it is the owner's and not a
   test:** whether a child process can be fenced is a property of the **deployed execution
   environment**, and this repository's tests are hermetic — no network, no cloud, `t.TempDir()`.
   **CSR4b does not start until this step has returned**, and nothing in CSR4b's file list is opened
   before it does.
7. **Run the mutation check once, and watch D891's answer hold.** On a security-sensitive pull
   request, `fugaro review --pr N --mutate <file:test>` and read `mutation_checks` in the artifact:
   the test ran, the mutant was applied, the observed result is recorded, and the worktree was
   reverted. Confirm again that nothing was pushed, that the mutant's subprocess carried no token in
   its environment and could not reach the network, and that the stage ran no other shell — **and that
   the review container held no OAuth token at all, which is O1's own observation**: the job's
   environment and the execution's own description name no token, and the model calls are the
   credentials service's. **This is the confirmation after CSR4b, not the gate before it:** step 6
   settled whether the fence exists at all, and this step is the fence observed on the path that
   actually uses it — the one step whose result can still amend D891's answer once the machinery is in
   place.
8. **Drive the fix path once.** Take a `fix first` verdict, launch the fix round the coordinator
   would launch, and read the fix run's brief: it must name the review run and carry the findings,
   with no human having copied them. Then confirm the head moved and that a second review of the new
   head is what would follow.
