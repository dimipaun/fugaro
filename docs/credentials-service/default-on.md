# The credentials service deploys for every installation — the owner's ruling of 2026-11-05

**Status (2026-11-06): the Open item is ANSWERED — DEPLOY NOW, SWITCH LATER.** The owner answered the
BLOCKING item (§5 item 1) on **2026-11-05**: the service is **deployed for every installation** by
`fugaro update`, and **the jobs switch to it later, only where and when the service can actually serve
them**. **The tasks below are PLAN ONLY and none of them is approved** — but the block is lifted: the
eight tasks are now the **deploy** (DON1–DON7) and the **switch** (DON8), and the answer is why the
switch is a task of its own rather than a sentence inside the deploy.

The independent review of this file found a fact in the tree that contradicts the premise the ruling
was taken on: **the service's deploy and the jobs' switch to it are two different changes, and the
second one, as the tree stands, breaks every installation's runs** (§1.4 — the mount drop is auth-blind
and the GitHub provider opens keyless with no fallback). **The owner's intent is unchanged and this file
does not weaken it: the service is deployed for every installation.** What changed is that the deploy is
separated from the switch, and the switch now has a rule rather than a flag.

**The one consequence to hold on to, because every task below depends on it:** the deploy alone is not
safe. `internal/infra/spec.go:1177` drops every repository's model mounts once the installation root's
block exists — the drop lands on each repository root's **next apply**, one repository at a time, which
§1.4 pins down — so **the gate that holds the switch off is part of 0.20.0 and ships with the deploy** —
see §1.5 and §3 DON8. A release that shipped DON1 without DON8's gate would break every run of every
repository whose root is converged after it, in the field.

This file is a **dated amendment** to [implementation-plan.md](implementation-plan.md),
[reconciliation.md](reconciliation.md) and [runbook.md](runbook.md). It amends exactly one thing those
documents say — the service's **deployment posture** — and it rewrites none of them: where a sentence of
theirs is reversed, §3's task DON4 names it and moves it, and this file leaves the sentence where it is.

It changes no ruling of theirs about the service's **shape** (Q6's one bundle secret and one grant, D143's
fail-closed refusals, D145's keep-out invariant, D146/D147's placement in the installation root and away
from `fugaro managed apply`), no check number, no decision number, and no task those documents already
carry. **Nothing here approves a task**, and every task below needs the owner's explicit go exactly as
the plan's own do.

**What it supersedes, in one line:** the earlier "the service **ships dormant** / is **optional** / is
**opt-in**" ruling, wherever it is stated. That phrase is the thing this file retires; §3(d) is the task
that finds every place it is written and every test that pins it.

| | |
|---|---|
| **Ruling** | the credentials service is deployed **automatically for every installation** by `fugaro update`, including Bitbucket-only ones |
| **Date and form** | 2026-11-05, the owner's answer to an `AskUserQuestion` — **the answer taken is not the recommendation the question carried** |
| **Why, in the owner's words** | the reason for opt-in was **cost**, and a Cloud Run service that **scales to zero costs nothing idle**; so **remove the opt-in complexity** |
| **Reverses** | the "ships dormant / optional" rulings and the `--provision-credentials` opt-in as the *only* way the service is deployed |
| **Amended after review** | 2026-11-06 — the **deploy** stands for every installation; the **switch** (the repository roots reading the service back and the run handing the job its URL) is separated from it, because the drop it triggers is auth-blind (§1.4) |
| **Amended after review** | 2026-11-07 — the readiness record is a **per-repository fact written by the door that applies that repository's root**, and it is written as the value that root's **plan was built with**: a record vouches for a grant, only a root's own apply makes one, and the same rule runs the other way (§3 DON8) — and **neither door writes one in a coding agent's session or in a run's own process**, where the bundle is not read at all |
| **Amended after review** | 2026-11-07 — the record is the **switch itself**, not the reading of the bundle: the auth its plan was built for is **folded into it** (`infra.CredentialsServiceServes`, not a second copy of its conjunction), so a root applied before its owner committed the cascade is recorded held off and no launch is handed an address whose `roles/run.invoker` grant no apply made. The auth stays **read live** at every build and every launch, which is what honours an edit in the other direction |
| **Amended after review** | 2026-11-07 — **the record carries the plan's FIRST condition as well as its third**: `installed` is read from the installation root's **outputs** — the source the spec reads it from — and not from the local config's block, which is what an owner's opt-out leaves behind while the outputs carry no service, so a `fugaro update --repo` can no longer record `true` for a root it applied held off (§3 DON8) |
| **Amended after review** | 2026-11-07 — **a read that failed is not a reading and therefore not an answer.** The three states that answer are facts about the bundle, true for every reader (no version, a newest version that is `DISABLED`, a document that parses without a `KindModel` entry); an error is a fact about **this machine's identity**, and the record travels in the local config `fugaro update` publishes, so a `false` written from it switches the service off for every machine of the installation — including the jobs whose mounts the last apply dropped. A failed read leaves the record as the last reading wrote it (§3 DON8) |
| **Amended after review** | 2026-11-07 — **the reading is asked once per run, and BOTH of its answers are memoized together** (the sixth review round): one run asks twice — the installation door's backfill, then the repository stage's record — and memoizing the reading alone left the second door answering "the bundle holds no model credential" out of a memo the first door had written as "there was no reading", which is the same installation-wide `false` the row above refuses, reached through the run's own second door (§3 DON8) |
| **Amended after review** | 2026-11-07 — **the record is a tri-state, not a boolean, and the upgrade's backfill reads the INSTALLATION's own state** (the seventh review round, three findings, §3 DON8). *No record* and a recorded `false` are two documents (`localcfg.Repo.CredentialsServiceReady` is a pointer): the first means no machine has judged that root, the second is an apply that ran with the switch off, and only the first may be refilled — so `MergeShared` carries a published record over a local entry that has none, or every machine that did not apply that root would publish the absence over the record the machine that did wrote, leaving a `cascade` repository's runs with no address on every machine of the installation. The backfill's evidence is the repository root's **state in the installation's state bucket** (`infra.RepoCredentialsServiceApplied`) and not the tfvars in this machine's workdir, which per-machine — and per-checkout — state would leave an installation whose roots a teammate applied converging one `fugaro update --repo` at a time, the operator step the HARD RULE forbids. And a repository the local config does not hold gets its entry **before** `infra.Repo` reads it, so a first onboarding plans the switched-on root and records it in one run, as it did before this release |
| **Amended after review** | 2026-11-07 — **a launch that cannot read its document falls back to the record, and the backfill re-reads a recorded `false`** (the eighth review round, two findings, §3 DON8). The workdir's tfvars are one answer and not the only one: the machine that never applied that root holds none of them, and that is every console launch (`console/src/main/launch.ts` spawns `fugaro run --repo` from a private empty directory), so an empty `agent.auth` is judged on `installed && (record || deployed)` — withholding the address there starts a `cascade` run whose job mounts no model credential (D143). And a recorded `false` is no longer skipped by the upgrade's backfill: a record carries no stamp, so an earlier apply's `false` outranks the `true` a later apply on another machine published, installation-wide and with no `fugaro update`-only way back; the backfill reads the installation's own state for any entry whose record is not `true` and writes it again where the state shows the grant |
| **The Open item, answered** | **2026-11-05: DEPLOY NOW, SWITCH LATER.** The service deploys everywhere; the switch happens **per repository** and only where the service can serve that repository's runs — its auth mode must be one the service serves **and** the bundle must hold what that mode needs. §1.5 states what the switch flips; §3 DON8 is the task |
| **Does not touch** | Q6, D143, D145, D146, D147, Global Constraints 6 and 12, Checks 90–99, D130–D159, and every task in [implementation-plan.md](implementation-plan.md) |

---

## 1. The ruling, and what it costs — verified against this tree

### 1.1 The ruling

The credentials service is deployed by `fugaro update` **for every installation**, with no flag, no
typed extra confirmation of its own and no per-installation choice. The installation root's
`credentials_service` variable stops being the thing an owner opts into and becomes part of the state
`update` converges every installation on — which is the shape the **HARD RULE** in `CLAUDE.md` already
requires of everything else `update` does: one command, no second step, no manual repair.

**The ruling is about the deploy, and the review separated it from the switch.** What the owner ruled
is that the service **exists** on every installation. Whether the jobs then **use** it — the repository
roots reading it back, and a run handing its job `FUGARO_CREDENTIALS_SERVICE_URL` — is a second change
with its own failure mode, and the tree ties the two together in a way the ruling did not assume.
**§1.4 states the fact; §1.5 states what the switch flips; §3 DON8 is the switch.** The owner answered
the question that split them on 2026-11-05: **deploy now, switch later** (§5 item 1). Everything else in
this file is written against the deploy.

**Bitbucket-only installations are in scope, and they are the case worth naming**, because the service
has been read as a GitHub-App-key holder: it is not. The App key is **one** credential the bundle may
hold (§2), and the service's other work — the read mint that a Bitbucket-only project's coordinator and
operator read through (`FUGARO_PERSON_ACCOUNTS`, `FUGARO_COORDINATOR_ACCOUNT`), the model cascade, and
the verdict sign/verify route — has nothing to do with GitHub. An installation whose repositories are
all Bitbucket gets the service, its account, its bundle container and its two grants exactly as a GitHub
installation does.

### 1.2 What the tree says today, and the gap the ruling has to close

**VERIFIED — the Cloud Run service has no `scaling` block.** `deploy/terraform/gcp/modules/installation/
credentials.tf` declares `google_cloud_run_v2_service.credentials` (line 305) and the string `scaling`
does not occur anywhere in that file. So the plan states no minimum instance count for this service, and
the number that decides whether it bills while idle is the **provider's default** rather than anything
this repository writes down.

**VERIFIED — the tree already has the precedent that states it, and the guard that reads it.** The MCP
front door, the one other always-on endpoint this installation deploys, pins it explicitly:

- `deploy/terraform/gcp/modules/installation/coordinator.tf:437-439` —
  `scaling { min_instance_count = 0 }`, under a comment that gives the reason in the same words the
  owner's ruling does ("a public endpoint nobody calls costs nothing, and a cold start is what a caller
  waits for rather than what the installation pays for around the clock").
- `internal/infra/tf/managed_cost.go:176-213` — `managedCostList`'s first entry,
  `managedCostMinInstances`, blocks a plan whose `google_cloud_run_v2_service` states a
  `min_instance_count` above the line, reading it at **three** paths: `template[0].scaling[0]
  .min_instance_count`, `scaling[0].min_instance_count` and `scaling[0].manual_instance_count`.

**VERIFIED — and the guard is silent on today's credentials service, which is the whole gap.** The
entry's `above` function is called "only for a value the plan states and knows"
(`internal/infra/tf/managed_cost.go:169-171`). A resource with **no** `scaling` block states no such
path, so there is nothing for the rule to fire on: the guard cannot see the credentials service at all,
in either direction. That is the same shape `managed_cost.go`'s own comment already reasons about for a
Cloud Run **job** ("a rule reading a key no plan of the type has is a rule that can never fire on a
stated value"). So the ruling's cost claim rests, today, on a provider default that nothing in this tree
pins and nothing in this tree checks. **DON3 is the task that pins it** (§3).

**CORRECTED AFTER REVIEW — and the guard is not this service's, in either direction, and never will be.**
The first draft of §3 DON3 promised that pinning the block would put this service under the managed
project's cost guard. **It does not, and DON3 must not claim it does.** `internal/managed/plan.go:173`
(`var costFindings = tf.ManagedCosts`, invoked at `:234`) is `tf.ManagedCosts`'s **only** caller in the
tree, and it is the **managed** gate; `deploy/terraform/gcp/modules/installation/credentials.tf:3` states
why this service is never in a plan that gate reads ("installation root — `fugaro managed apply` never
deploys it (Global Constraint 7)"). So a test that the `managedCostMinInstances` entry fires on a plan
stating a raised minimum would pass while `fugaro update` checked **nothing at all** about this service
— a green test over a path no installation takes. **What guards this service is the static pin on
`credentials.tf` and the golden-plan pin on the plan it produces** (§3 DON3's Tests), both of which are
DON3's own. The entry itself needs no change: the template-level path at `managed_cost.go:195` is
already the path the block lands on, and the entry's own behaviour is pinned by its own tests in
`managed_cost_test.go`, which this task does not touch.

### 1.3 What actually costs something while idle — the six resources, and the rest

The service's Terraform is **six resources**, all gated on the one local `credentials_service_on`
(`deploy/terraform/gcp/modules/installation/credentials.tf:25`, counted by
`TestCredentialsServiceResourcesAreCounted`, `deploy/terraform/terraform_test.go:4347`):

| # | Resource | `credentials.tf` line | Bills while idle? |
|---|---|---|---|
| 1 | `google_service_account.credentials` | 111 | **No.** An IAM identity is not a billed resource. |
| 2 | `google_secret_manager_secret.credentials_bundle` | 149 | **No, while it holds no version.** See below. |
| 3 | `google_secret_manager_secret_iam_member.credentials_bundle` | 175 | **No.** An IAM binding. |
| 4 | `google_project_iam_custom_role.credentials_database` | 238 | **No.** A custom role definition. |
| 5 | `google_project_iam_member.credentials_database` | 271 | **No.** An IAM binding. |
| 6 | `google_cloud_run_v2_service.credentials` | 305 | **No, at zero minimum instances.** See below. |

**The bundle container (row 2), precisely.** Terraform creates the **container and never a version** —
`TestNoSecretVersionsInTerraform` (`deploy/terraform/terraform_test.go:206`) is what holds that, and the
file's own comment says why ("no credential value ever reaches Terraform or its state"). A container with
no version is a container that holds nothing to bill. The first version is written by the owner's own
`fugaro creds set NAME` (`internal/cli/creds.go`, C1.25), and **every write disables the version it
replaced** — `internal/cli/creds.go:140`, `:202` and `disable` at `:392` — so the installation carries
**exactly one active version**, not one per rotation. What that costs per month is Google's published
Secret Manager list price for one active secret version; **that price is not a fact this tree holds, and
§6's UNVERIFIED list carries it as UNVERIFIED.**

**The Cloud Run service (row 6), precisely.** With request-based billing and a minimum instance count of
zero the service is charged only while it is serving a request, plus the request and CPU/allocation
charges that go with it; **there is no idle charge.** That is the owner's claim and it is the reason for
the ruling, and it holds **only while the minimum instance count really is zero** — which, per §1.2, this
tree does not state today. **The price of a served request is likewise not in this tree (UNVERIFIED).**

**The one always-on cost the ruling does not name, and it is real: container image storage.** The
service's image is pulled through the installation's **remote (pull-through) repository**,
`google_artifact_registry_repository.ghcr` (`deploy/terraform/gcp/modules/installation/registry.tf`,
unconditional — it exists whether or not the credentials service does), and that repository **carries no
cleanup policies by design**, with the file's own reason ("an eviction during an upstream outage would
turn a warm pull into a 504"). Cached layers therefore accumulate and are never pruned by this tree.
What the credentials image adds to that cache is bounded and known:

- **VERIFIED:** `images/credentials_test.go:209-220` pins the image's budget at **100 MB** with a
  warning line at **85 MB**, and states the binary it wraps is **55.4 MB** (`-s -w`, CGO off, amd64) in a
  distroless static layer of "a few MB". So the image is **~60 MB uncompressed**, and its compressed
  layers, which is what a registry bills, are smaller again.
- **UNVERIFIED (not in this tree):** Artifact Registry's published storage price per GB per month and
  its free allowance. §6 carries it.

**Nothing else in the installation root becomes always-on because of this service.** The invoker grants
(`deploy/terraform/gcp/modules/installation/iam.tf:163`, `:194`) are
`google_cloud_run_v2_service_iam_member` — IAM bindings, not billed resources. The repository-side half
(`deploy/terraform/gcp/modules/workflow/sa.tf:211`) is the same. **So the honest statement of the
owner's cost claim is:** *the service itself is free while idle once its minimum instance count is
pinned at zero; what it adds to the bill is one active Secret Manager version once a bundle is written,
and one cached container image; and today the first of those rests on an unpinned provider default that
DON3 fixes.*

### 1.4 The premise the ruling was taken on, and the code fact that contradicts it — AMENDED AFTER REVIEW

**The premise, in the owner's own words:** the reason for opt-in was **cost**, a Cloud Run service that
scales to zero costs nothing idle, **so remove the opt-in complexity**. That premise treats *deploying*
the service as the cheap part and treats the jobs' *use* of it as free. **The second half is not free,
and the tree says so in the four places below.** This subsection states the fact; §5 item 1 is the ruling
it was put to, and **the owner answered it on 2026-11-05 — the deploy lands and the switch is a task of
its own, §1.5 and DON8.** **It is the reason the status line above reads `amended after review`, and it
does not change the owner's intent: the service still deploys for every installation.**

**VERIFIED — the mount drop is auth-blind, and it is the whole of what deploying triggers.**
`deploy/terraform/gcp/modules/workflow/sa.tf`:

- `:89-90` names the model credentials as literals — `model_secret_env_names =
  ["ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]` and `model_secret_env_prefix =
  "FUGARO_PROVIDER_KEY_"`;
- `:135-137` builds `is_service_held` over them (`env => contains(local.model_secret_env_names, env) ||
  startswith(env, local.model_secret_env_prefix) || …`);
- `:148-151` is the drop itself: `job_secret_names = toset([for env, logical in var.workflow.secret_env
  : logical if var.credentials_service == null || !local.is_service_held[env]])`.

**The gate is `var.credentials_service == null` and nothing else. It never reads `agent.auth`.** An
`oauth` job and an `api-key` job lose their model credential's accessor exactly as a `cascade` job does.
**`SECURITY.md` already states this as a limit of the release, in as many words:** "the mount drop is
auth-blind (an `oauth` or `api-key` repository job of an opted-in installation declares no model
credential), while only a `cascade` run's calls go through the service" (`SECURITY.md`, the credentials
service section). So this is a **known, written-down** property of the tree, not a new finding — what is
new is that the ruling makes it reach **every** installation rather than the ones that opted in.

**VERIFIED — writing the installation root's block is what triggers it, so the deploy is not separable
from the drop by itself.** `internal/infra/spec.go:1177-1178` — `if c.inst.CredentialsService != nil {
dropModelMounts(&ws) }` — reads the **installation root's** tfvars, and `:1314` hands
`repoCredentialsService(c.inst.CredentialsService)` (`:244-248`) to **every** repository root. So the
moment DON1 writes the block, every repository root in that installation drops its model mounts on its
next apply. There is no flag, no second command and no per-repository choice in between.

**VERIFIED — the run's own half is worse than a drop: it is a removal with no fallback.**
`internal/gitprov/providers/providers.go:93-99` — when `FUGARO_CREDENTIALS_SERVICE_URL` is set, the
GitHub provider is opened with `ServiceHeldKey: true` and the function **returns before `appKey(vars)`
is ever called**, so there is no path that falls back to the mounted App key. The comment says why
("a run that reached for a key it was not given would be a credential the platform meant to withhold").
`internal/cli/run.go:3647` (`credentialsServiceLaunchEnv`) is what sets it, from `lc.CredentialsService
.URL`. **And the App key's own half is inert today**, which is the one mercy here: `config
.ServiceHeldSecretNames` is empty (`internal/config/providers.go:542`, #907), so `sa.tf`'s
`service_held_secret_names` names no key and the *App key's* accessor is not dropped — the mount drop
that fires today is the **model credentials'**, and the keyless-provider half fires from the URL alone.

**The consequence, stated plainly.** An installation that converges on DON1 as it was first written is
an installation whose `oauth` workflow has lost `CLAUDE_CODE_OAUTH_TOKEN` (or `api-key`'s
`ANTHROPIC_API_KEY`) while the bundle holds nothing that replaces it — because `creds set` is a person's
step that has not run, and for an `oauth` job the service has no route that would serve it anyway.
**Every run of every repository fails, on every installation, in the release that deploys the service.**
That is the failure the review caught, and it is why the deploy and the switch are now two changes.

**When exactly it arrives — VERIFIED, and it is worth one sentence because it is the release's timing
and not a detail.** The drop lands on the repository root's **next apply**, and the apply is the door
that takes a checkout: `internal/cli/init.go:344` routes `--repo` to `runInitRepo` and everything else
to `runInit`, so `fugaro update` without `--repo` converges the installation and leaves every repository
root alone (`fugaro run` does not apply one either — `internal/cli/run.go` calls no `infra.Repo`, and
neither does `internal/runner`; §6 V9 states the limit of that reading). **So the failure does not fire
on the installation's own convergence — it fires on each repository's, one repository at a time, which
is the shape that makes it worse rather than better:** it arrives while the owner is doing something
else, on a repository they just converged, and the first symptom is a failed run rather than a failed
update.

**And the failure is not hypothetical: it is the limit opt-in already has, made universal.** The
README's own status paragraph states the other half of it in as many words — "the other `agent.auth`
modes are not wired: a repository job in one has no model credential of its own and nothing yet to
replace it" — and `SECURITY.md` states the drop as a limit of the release rather than a defect. So an
installation that opted in today and runs an `oauth` workflow is **already** in the state §1.4
describes; opt-in is coherent today only for a `cascade` workflow, which is exactly why the opt-in was
per-installation and deliberate. **The ruling's premise did not miss a bug; it missed that the limit is
what becomes universal.** That is the whole of the amendment, and it is why the deploy and the switch
are now separate: the deploy is cheap and the ruling's own reason holds for it, and the switch is the
limit.

**So the ruling is split, and the two halves are two tasks:**

| | What it is | Where it is | State |
|---|---|---|---|
| **the deploy** | the installation root's `credentials_service` block: the service account, the bundle container and its grants, and the Cloud Run service (§1.3's six resources) | **DON1** | **the owner's ruling; every installation, in 0.20.0** |
| **the switch** | the repository roots reading the block back (`repoCredentialsService` → `dropModelMounts`) and a run handing the job `FUGARO_CREDENTIALS_SERVICE_URL` (`credentialsServiceLaunchEnv`) | **DON8** | **the owner's answer of 2026-11-05: later, per installation, only where the service can serve that job — and its gate ships in 0.20.0 with DON1, because the deploy alone is not safe** |

**The owner's answer, in the words §5 item 1 was answered with:** the service is deployed everywhere,
and the repository roots' `credentials_service` input and the run's `FUGARO_CREDENTIALS_SERVICE_URL`
**stay off until the bundle holds what that job's auth mode needs**, so an installation that updates
keeps every run working with `update` alone. **The owner's one design constraint:** the switch is
`fugaro update`'s own automatic act — an `update` that switches only when the bundle is ready and the
workflow's auth mode is covered, with a `doctor` line saying why not, is preferred to any second
command; if it cannot be done without a second command, that is an Open item and no operator step that
is another command ships (`CLAUDE.md`'s HARD RULE). **§3 DON8 is that design, and it needs no second
command.** §5 item 1 records the answer and is closed.

---

### 1.5 What the switch actually flips — VERIFIED, three flips and their conditions

The brief for this amendment asks what the switch flips, verified against the tree rather than assumed.
It is **three** things, they live in **three** files, and each one has a condition of its own — which is
why one flag could never have been the whole switch.

| # | What flips | Where it is, verified | Condition it needs before it may flip |
|---|---|---|---|
| 1 | the job account's **accessor on the model secrets**, and the job's **mount** of them | `deploy/terraform/gcp/modules/workflow/sa.tf:148-151` (`job_secret_names`, gated on `var.credentials_service == null` and `is_service_held` at `:135-137`); the Go spec's `dropModelMounts` (`internal/infra/spec.go:1177-1178`) and `repoCredentialsService` (`:244-248`, called at `:1314`) | **the bundle holds at least one model credential** — the service is the only source left once the mount is gone (D143, fail closed), and a `cascade` run with an empty bundle stops at the service's named refusal |
| 2 | the **keyless GitHub provider** | `internal/gitprov/providers/providers.go:93-99` — `FUGARO_CREDENTIALS_SERVICE_URL` set opens the provider with `ServiceHeldKey: true` and returns **before `appKey(vars)`**, so the mounted key is never read | **the installation's service must mint**, which it does from the App key in the bundle: the runner installs the service as the token source (`internal/runner/runner.go:1119`, `serviceTokens` at `:1399-1420`), so this flip is already wired and needs the bundle's App key, not a code change |
| 3 | the **URL on the execution** | `internal/cli/run.go:3647` (`credentialsServiceLaunchEnv`), which is what sets `FUGARO_CREDENTIALS_SERVICE_URL` at all (`:3553`) | **the workflow's `agent.auth` must be one the service serves** — today `cascade` alone (`config.AuthCascade`, `internal/config/defaults.go:77`); for an `oauth`, `api-key` or `vertex` job the URL buys nothing and flips 1 and 2 against it |

**The auth-mode condition is not a new rule — it is the tree's own rule, read from the same place.**
`internal/infra/spec.go`'s `SecretMounts` (`:926`) already branches on `cfg.Agent.Auth`: the `cascade`
case mounts **no model credential at all** ("the credentials service hands the run one at call time"),
and every other case mounts its own. So the switch's static half is the same predicate the mount list is
already built from, and a `cascade` job is the only job for which "the service holds my credential" is
already true.

**The readiness condition has an existing reader, and the switch must use that one and not a new one.**
`internal/cli/doctor_brokering.go:432` (`doctorBundleChecks`) already reads the bundle with the
operator's own identity and parses it with **the service's own loader**: `openBundleStore(ctx, lc)`
(`internal/cli/creds.go:314`) → `store.check` → `store.newest` → `store.access` → `credsvc.ParseBundle`.
The question the switch asks of the result is the loader's own: **does the document hold at least one
model credential** — `Bundle.Credentials()`, `internal/credsvc/bundle.go:438`, "the whole of what the
cascade is offered". **That is the point of reading it this way:** a readiness test written against the
raw document could say yes to a bundle the service refuses, and the switch would then drop a mount the
service cannot replace.

**Three things the switch does NOT flip, and each is a deliberate no:**
- **the Bitbucket token.** `providers.go`'s Bitbucket branch never reads `EnvCredentialsServiceURL`, and
  `config.ServiceHeldSecretNames` is empty (`internal/config/providers.go:542`, #907), so
  `sa.tf`'s `service_held_secret_names` names nothing and no git credential's accessor is dropped by
  this switch. A proxied repository's token is the separate `is_proxied_git` clause (D500/D502) and is
  not this ruling's.
- **the App key's own accessor and mount.** They are C2.3's, held behind the owner's live Check 96
  (§6 V8), and the switch works without them: the runner mints through the service (flip 2), so the key
  is simply an accessor the job holds and no longer reads.
- **the workflow's `agent.auth`.** A repository the service cannot serve is not switched, and nothing
  rewrites its mode.

**And the finding that makes the switch small, verified: flip 1 needs no Terraform change at all.**
`sa.tf:148-151` gates the drop on `var.credentials_service == null`, and that variable is not the
installation's block directly — it is what `modules/repo/workflows.tf:36` passes down from the
**repository root's** `installation.credentials_service`, which `internal/infra/spec.go:1314` builds
through `repoCredentialsService` (`:244-248`). So **the module already reads the switch; what changes is
the value the repository root hands it.** A held-off repository's root carries no
`installation.credentials_service` (the field is `omitempty`, `RepoSpec.Installation`), the workflow
module receives null, and its own expression keeps the accessors and the mounts — the pre-ruling plan,
byte for byte. **`sa.tf`, `modules/workflow/variables.tf`, `modules/repo/workflows.tf` and
`roots/repo/variables.tf` are therefore not edited by the switch, and no golden plan moves.** The same
predicate is what `internal/infra/spec.go:1177` reads before `dropModelMounts`, so the Go spec and the
Terraform plan cannot disagree about a repository: they are one expression read twice.

---

## 2. What stays the user's, and what the service's state is between deploy and key

### 2.1 The GitHub App private key

**The App key is the one credential no agent and no update can supply, and it is the only one.**
`fugaro creds set github-app-key` is the bundle's only writer, and it refuses a coding agent's session
and a run's own process **before it reads anything** — `internal/cli/creds.go:118-119`, on the same
`agentMarker`/`initflow.AgentRefusal` path every owner-only command uses. That refusal is deliberate and
this file does not weaken it: the bundle is read to be written, so the ability to run the command is the
ability to read every credential in it. **The ruling changes when the service is deployed; it does not
change who may write the bundle.**

So the App key stays a person's step at a real terminal, and it is the only one. Everything else the
service needs is either created by Terraform (the account, the container, the grants, the service) or
written by `fugaro update` itself (the URL, the job-account table, the admitted people).

### 2.2 The verifier account — and it is not a person

**Per CSR Open item 5, and this file takes its answer rather than re-opening it:** the identity that may
call the verify route is a **dedicated verifier service account** — `credentials_service
.verifier_accounts` names it — and **never a person.** The reason is a fact about the service's own
start-up and is **VERIFIED**: every `user:` operator is rendered into `FUGARO_PERSON_ACCOUNTS`
(`credentials.tf`, the `FUGARO_PERSON_ACCOUNTS` dynamic block, from `local.credentials_read_people`), and
`NewAuthenticator` refuses an address that is **both** a person and a verifier — "a person account is
also one of the verifiers, so one caller would be two classes" (`internal/credsvc/auth.go:574`). An
installation whose `verifier_accounts` named the owner **would not start at all.** So this is not a
preference: the entry is a service account, and DON1 renders it.

**Nothing else is the user's.** The service's account, its bundle container, its one Secret Manager
grant, its database role and binding, its Cloud Run revision and every invoker grant are Terraform's.
The only operator-facing *value* is the verifier account's address, and §4 says which task must settle it
before DON1 can land.

### 2.3 The service's state between deploy and key: deployed, serving, admitting nobody

This state is already designed and is **VERIFIED in the tree**; the ruling makes it the *normal* state of
a fresh installation rather than an edge case, so it is worth stating in as many words.

An installation that has just converged on this release has a service that **is deployed and is
serving** — the revision starts, passes its health check, and Cloud Run routes to it — and that
**admits nobody**, on every route, each for a named reason:

| Route | What refuses | Where the refusal comes from |
|---|---|---|
| every caller | Cloud Run IAM: `invoker_iam_disabled = false` and no `allUsers`/`allAuthenticatedUsers` member anywhere (`credentials.tf:305-320`); the only members are each job account's own `roles/run.invoker` and the operators' (`iam.tf:163`, `:194`) | the front end, before the container runs |
| the run/read mint | a caller that is not one of the installation's job accounts — and, on an installation with no repository jobs at all, an empty table, which admits nobody | `FUGARO_JOB_ACCOUNTS`, rendered on every installation; Global Constraint 6's shared-job gate |
| the read mint's person class | an empty list — "a read mint that admits nobody" | `FUGARO_PERSON_ACCOUNTS`, rendered only when the installation has `user:` operators |
| the verify route | an empty verifier list — the class's own refusal for every caller | `FUGARO_VERIFIER_ACCOUNTS`, `AuthConfig.VerifierAccounts`, empty by default |
| any model call | the bundle holds no document, or none the loader can parse | the loader fails **closed** — `credentials.tf:149-160`'s own comment: "a container with no document yet is fine: the loader fails closed … so the service answers a named refusal rather than an empty credential set" |
| the GitHub mint | no App key in the bundle | D143: a named refusal, **never** a fall back to the job's own copy |

**The one sentence that must be true of this state**, and the thing DON7 puts in `doctor`: the service is
**deployed and serving, and it is admitting nobody** — and the line names *which* half is missing (the
App key, the verifier account, the job accounts, the bundle) rather than reporting one undifferentiated
"not configured".

---

## 3. The tasks

Eight tasks, **DON1–DON8**: the **deploy** (DON1–DON7) and the **switch** (DON8). They follow
[../cloud-senior-review/implementation-plan.md](../cloud-senior-review/implementation-plan.md) §3's
convention exactly: decisions and findings named, an exact file list with its cap, the interfaces
produced, named tests, a red step, the mutation that holds it, a reviewer tier, whether it is OWNER-RUN,
its dependencies and its commit message. **No task is approved** — the block §5 item 1 raised is lifted
(the owner answered it on 2026-11-05, §1.4), but these are still a plan, and **the tasks are held in
order, not together**: §1.4 verifies that DON1's deploy, as the tree stands, makes every `oauth` and
`api-key` job lose the model credential its run needs, so **DON8's gate ships in the same release as
DON1** — it is the one thing here that may not be deferred, because the deploy alone breaks the field.
**DON8 is new after the answer**, and its whole content is the split the answer makes: the switch turns
on only where the bundle holds what that job's auth mode needs. **DON3 is the one whose content the
answer did not change** (a `scaling` block on a resource gated on `credentials_service != null` is right
either way).

**The numbering is deliberately not `C4.x`.** `C0`–`C3` are [implementation-plan.md](implementation-plan.md)'s
slices and `CSR1`–`CSR13` are the cloud senior review's; a third stream taking `C4` would collide with
the C-numbers of four other plans in this repository (`docs/design/budget-token-channel.md`,
`docs/forge-authorship-images/`, `docs/project-image-catalog/`, `docs/console-coordinator-v2/`). `DON` is
this file's own prefix and collides with nothing.

### DON1. `fugaro update` sets `credentials_service` for every installation — SECURITY-SENSITIVE

**Decisions:** the ruling of 2026-11-05 (this file). **Reverses:** D147's "off by default" half only —
D146's placement and D145's invariant are untouched. **Verification:** `internal/cli`, `internal/infra`.

**This task is the DEPLOY and nothing else — AMENDED AFTER REVIEW, and the owner answered §5 item 1 on
2026-11-05.** It writes the installation root's block, which is what makes the service exist. It does
**not** decide whether the repository roots read the block back or whether a run hands its job
`FUGARO_CREDENTIALS_SERVICE_URL`: §1.4 verifies that the block alone triggers `dropModelMounts`, and the
answer is **deploy now, switch later** — so the read-back and the URL are **DON8's**, and **DON8's gate
is a release blocker for this task**: DON1 and DON8 ship in the same release, and DON1's own tests
assert the switch is off (`TestAnOAuthInstallationStillLaunchesAfterUpdateAlone`, moved to DON8, is the
one that fails if it is not). **Nothing in this task's file list is a bundle write**: the answer is the
one reading under which `update` never writes a credential, so `fugaro creds set` stays the only writer
of the bundle and this task's file list, its tests and its SEC flag are as they were first written.

- **Files:** `internal/cli/init_stages.go` (the unconditional set path; today `pinCredentialsImage`
  returns early at "`doc == nil` — no `credentials_service` in the installation's tfvars: nothing opted
  in", and the stage's own gate is `:181`), `internal/cli/init.go` (the flag at `:109`/`:458` and its
  three gating sites at `:1301`, `:1318` and `:3089`; the `CarriedCredentialsService` call sites at
  `:736`, `:2090`),
  `internal/infra/tfvars.go` (`CarriedCredentialsService` `:591-612`, `Credentials` `:355`,
  `CredentialsServiceURLOf` `:448`, `SetCredentialsServiceURL` `:467`),
  `internal/cli/init_credentials.go` (the `--provision-credentials` engine stage is **removed**;
  `resolveCredentialsImage` is kept, because `pinCredentialsImage` pins through it — **and the two
  functions the stage owns that outlive it move rather than die: `fillAudience`
  (`internal/cli/init_credentials.go:522`, the second apply that reads the assigned URL back out of the
  module's own outputs through `infra.CredentialsServiceURLOf` and writes it with
  `infra.SetCredentialsServiceURL`) and `recordURL` (`:609`, called at `:258` and `:305`, which reads
  the same outputs once more and records the URL in the project's local config through the coordinator's
  `writeConfig`). **Both move into `internal/cli/init_stages.go`, beside `pinCredentialsImage`**, keeping
  their bodies — the URL is still assigned by Cloud Run and still unknown until the apply, so the second
  apply and the config record are both still needed — and losing the stage's own typed confirmation,
  which is the thing that goes with the flag. **This is the destination the review asked for:** the
  recorder does not disappear with the stage, and a task that removed the stage without naming where its
  recorder goes would leave `update` deploying a service whose URL nothing ever records, which is
  exactly the state §2.3's "deployed, serving, admitting nobody" table would then be unable to
  distinguish from a service that is unreachable.)
  `deploy/terraform/gcp/roots/installation/variables.tf` (`:230`, the variable's own doc comment),
  `deploy/terraform/gcp/modules/installation/credentials.tf` (the `verifier_accounts` rendering and the
  file's header comment), `internal/cli/init_credentials_test.go`,
  `internal/cli/setup_refusals_test.go`. **At most 9 files.**
- **Interfaces:** `update` writes a `credentials_service` block into the installation root's tfvars on
  **every** run, for every installation, from `infra.Credentials(lc, image, publishedVerifiers)` — the
  same function the credentials stage calls today — with the released image resolved and pinned by digest
  through the existing `pinCredentialsImage` path. The block carries the verifier account of §2.2.
  `--provision-credentials` is **removed**, not aliased. **The decision and its reason:** a no-op flag
  whose name and `--help` text say "opt in" is a lie in the one surface a user reads to learn what a
  command does, and the generated `website/content/cli-reference.md` would carry that lie into the
  published reference; the brief says no backward compatibility is needed pre-1.0; and there is nothing
  for the flag to *do* — `update` is the whole upgrade (the HARD RULE), so the flag cannot be the only
  route to anything. The removal is cheap to reverse if the owner prefers the alias, which is why it is
  stated here as a decision rather than left implicit.
  **And what this task must NOT touch, because it is DON8's and not the deploy's:**
  `internal/infra/spec.go`'s `repoCredentialsService` (`:244-248`) and `dropModelMounts`
  (`:1177-1178`), `deploy/terraform/gcp/modules/workflow/sa.tf`'s `is_service_held` (`:135-137`) and
  `job_secret_names` (`:148-151`), and `internal/cli/run.go`'s `credentialsServiceLaunchEnv` (`:3647`).
  **The answered reading leaves all four exactly as they are** — it adds a condition *in front of* them
  rather than changing them, and that condition is DON8's file list. **A DON1 that touched them would be
  making the switch's decision by accident, which is the failure §1.4 exists to prevent.**
- **Tests:** `TestUpdateSetsCredentialsServiceOnEveryInstallation` (a fresh installation's tfvars carry
  the block after one `update`), `TestUpdateKeepsAnOptedInInstallationsServiceUnchanged` (an
  installation that already opted in converges to the same block, byte for byte, with the URL it
  already recorded), `TestUpdateCarriesTheVerifierAccount` (the block's `verifier_accounts` is the
  account §2.2 names, and is not an operator's address), `TestProvisionCredentialsFlagIsGone` (the flag
  is unknown and the error names `fugaro update`), and `TestUpdatePinsTheServiceImageByDigest` (the
  block's `image` ends in `@sha256:` and the module's validation is what refuses a tag — an existing
  rule, re-asserted because this task is the one that starts writing the block everywhere).
- **Red step:** `TestUpdateSetsCredentialsServiceOnEveryInstallation` fails on an installation whose
  tfvars carry no block: `update` sets it only behind the flag. **SEC flag:** this is the task that
  decides the service exists on every installation; the reviewer tier is **A** and the independent
  review is **mandatory** — the blast radius is every installation in the field.
- **Mutation to hold it:** put the write back behind the flag → the first test fails; make
  `TestUpdateKeepsAnOptedInInstallationsServiceUnchanged` rebuild the block from the spec instead of
  carrying it → it fails on the recorded URL; replace the verifier account with an operator's address →
  the third fails, and the service would refuse to start (`internal/credsvc/auth.go:574`).
- **Reviewer tier:** **A.** **OWNER-RUN:** no — the task is code and tests; the *live* convergence is
  Check 94's, which is the owner's.
- **Depends on:** **the verifier account of §4** — DON1 renders `verifier_accounts` with an account that
  must exist and be named first, or every installation deploys with an empty verifier list and CSR10's
  reader refuses every brief.
- **Commit:** `update: deploy the credentials service on every installation`.

### DON2. A convergence test that 0.19.0 reaches this state with `update` alone — the HARD RULE

**Decisions:** the ruling of 2026-11-05; `CLAUDE.md`'s HARD RULE. **Verification:** `internal/cli`.

- **Files:** `internal/cli/init_converge_test.go` (the convergence pair),
  `internal/cli/update_managed_test.go` (its `--yes --provision-credentials` invocations at `:798`,
  `:808` become plain `--yes`). **2 files.**
- **Interfaces:** consumes DON1's write. Produces no interface of its own: it is the test that makes the
  HARD RULE falsifiable. Two directions, and the second is the one that is easy to forget:
  1. an installation whose recorded state is **0.19.0** — no `credentials_service` block, the
     pre-ruling shape — reaches the new state with **`fugaro update` alone**: no second command, no
     `--provision-credentials`, no manual repair, and the service's block and image pin both present
     after that one run. **The URL is the one part of that sentence the answer decides, and it decided
     "not yet":** the block carries the URL the module assigned (§2.3 — the service is reachable the
     moment it exists), and **the launch hands the job none** until the bundle holds what the job's auth
     mode needs (§1.5, DON8). **The test asserts that state and asserts the other one is *not*
     reached** — a convergence that flipped the switch without the bundle being ready is the failure
     this direction exists to catch, and the `oauth` half of it is DON8's test, not this one's;
  2. an installation that **already opted in** under an earlier release **does not change** beyond the
     image moving to this release — same account, same container, same grants, and above all the
     **same recorded URL**, because a rebuild that dropped and re-derived it would re-plan the service.
     `CarriedCredentialsService`'s own comment is the reason this direction exists.
- **Tests:** `TestAnInstallationAt019ConvergesWithUpdateAlone` and
  `TestAnAlreadyOptedInInstallationDoesNotChange`, plus `TestConvergenceAsksNoSecondQuestion` (the run's
  prompts are the ones `update` already asks; the credentials stage's own typed confirmation is gone with
  the stage). **The review's own regression test, `TestAnOAuthInstallationStillLaunchesAfterUpdateAlone`,
  is DON8's** — the review added it here because DON1 alone would have broken it, and after the answer it
  belongs to the task that owns the switch. It is named in DON8 with the same body and the same fixture,
  and **DON8's `Depends on` is what makes it a release blocker for DON1 rather than a suggestion.**
- **Red step:** `TestAnInstallationAt019ConvergesWithUpdateAlone` fails against today's tree — the
  fixture converges and the block is absent, because nothing writes it without the flag. It is written
  **before** DON1's change and is DON1's own red step seen from the outside, which is what makes the
  HARD RULE test rather than assert.
- **Mutation to hold it:** make the update path require the flag → the first test fails; make the
  already-opted-in path re-derive the block from the spec rather than carry it → the second fails.
  **The switch half of this task's mutation is DON8's** (`flip the switch without the bundle being
  ready` → its `oauth` test fails), because the switch is no longer this task's to get wrong.
- **Reviewer tier:** **B** (a test-only task over a path DON1 owns; it is A only in the sense that what
  it proves is the HARD RULE).
- **OWNER-RUN:** no.
- **Depends on:** **DON1**.
- **Commit:** `cli: an installation at 0.19.0 converges on the credentials service with update alone`.

### DON3. The service scales to zero, and the pin is the guard — SECURITY-SENSITIVE

**Decisions:** the ruling of 2026-11-05. **Findings:** §1.2 and §1.3 of this file. **Verification:**
`deploy/terraform`, `internal/infra/tf`.

**AMENDED AFTER REVIEW — this task promised a cost guard that never reads this service's plan, and it
does not get one.** The first draft said DON3 would make the managed project's cost guard see the
credentials service. It cannot: `internal/managed/plan.go:173` (`var costFindings = tf.ManagedCosts`,
invoked at `:234`) is that guard's only caller, and `credentials.tf:3` says in as many words that the
managed path never deploys this service (Global Constraint 7). So a test that `managedCostMinInstances`
fires on a plan stating a raised minimum would be **green over a plan no installation produces**, while
`fugaro update` checked nothing. **The guard is the pins, and the pins are this task's own**: the static
pin on the module source and the golden-plan pin on the plan the module produces. Both fail the moment
someone raises the minimum, and neither depends on a gate this service never passes through.
`internal/infra/tf/managed_cost.go` and `managed_cost_test.go` are therefore **not this task's files**
(§1.2's CORRECTED paragraph is why), and the two `managed_cost` tests the first draft named are
**dropped**: a rule-level test would be a test of the entry, not of this service, and the entry's own
behaviour is already pinned in `managed_cost_test.go` where it belongs.

- **Files:** `deploy/terraform/gcp/modules/installation/credentials.tf` (the `scaling` block),
  `internal/infra/tf/testdata/golden/installation-credentials.plan.json`
  (regenerated — the plan's JSON changes the moment a block is added), `deploy/terraform/terraform_test.go`
  (the static pin), `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` (the
  credentials image run at `:2439` gains the assertion). **4 files**, and **not**
  `internal/infra/tf/managed_cost.go` or `managed_cost_test.go`. The golden plan is regenerated by
  `scripts/gen-golden-plans.sh`, never by hand.
- **Interfaces:** `google_cloud_run_v2_service.credentials` states, in a `scaling` block on the
  **template**, `min_instance_count = 0` and a `max_instance_count`. The precedent is the MCP front
  door's own block (`coordinator.tf:437-439`), and the numbers are chosen rather than copied: the
  **minimum is 0** because that is the owner's cost claim made checkable, and the **maximum is bounded**
  because an unbounded service is the other half of a cost the owner did not consent to — a runaway
  caller, or a retry storm, scales this service as far as Cloud Run allows. The plan proposes
  `max_instance_count = 4` (a small number an installation's own run concurrency cannot exceed, and one
  an operator raises deliberately), and the value is the task's to settle with its own reasoning.
- **The guard, and it is the reason this task exists at all.** The service states its minimum **in the
  tree** and the tree checks it **twice**, at two levels, both of which this task writes: the **static
  pin** (`TestCredentialsServicePinsMinInstanceZero`, reading `credentials.tf` and refusing a file that
  states no `scaling` block or a non-zero minimum) and the **golden-plan pin**
  (`TestCredentialsServicePlanStatesTheMinimum`, reading the **plan** rather than the source, so a
  provider-level default or a module change cannot make the source look right while the plan is wrong).
  **A guard that is only satisfied by a value nobody can change is not a guard**, so both pins are
  written to fail on a *raised* value and on a *deleted* block, not merely to confirm the value today.
  **What this task does not do, and the review is why:** it does not reach the managed project's cost
  guard, and it does not add a rule to `managedCostList`. The entry's template-level path
  (`managed_cost.go:195`) is already the path this block lands on, so **no rule change is needed** — and
  even with the block stated, that entry would never be handed this service's plan (§1.2's CORRECTED
  paragraph, Global Constraint 7). The task states that in its commit so the next reader does not
  re-derive the promise the first draft made.
- **Tests:** `TestCredentialsServicePinsMinInstanceZero` (the block is on the template, states `0`, and
  states a maximum) and `TestCredentialsServicePlanStatesTheMinimum` (the **golden plan**, not the
  source: the plan's `after` carries `template[0].scaling[0].min_instance_count == 0` — this is the test
  that would have caught the gap §1.2 describes). **No `managed_cost` test**, per the amendment above.
- **Red step:** `TestCredentialsServicePlanStatesTheMinimum` fails on today's golden: the plan states no
  `scaling` key at all. **SEC flag:** it is a cost claim made checkable, and the second test is the one
  that matters most — it reads what the provider would actually apply.
- **Mutation to hold it:** raise `min_instance_count` to `1` → the golden-plan test fails; delete the
  block → it fails again, because the plan states nothing; drop the `max_instance_count` → the static
  pin fails. **None of the three is caught by a managed gate**, which is the point of the amendment.
- **Reviewer tier:** **A** (it is the check that keeps the owner's cost claim true).
- **OWNER-RUN:** no.
- **Depends on:** **DON1** — both hold `credentials.tf`, and DON1 has it in level 1. **And it is the one
  task whose content the answer could not have changed** — a `scaling` block on a resource gated on
  `credentials_service != null` is correct whichever way the switch went, which is why it was the one
  task this file did not hold behind the item. The owner may still release it alone if they want the pin
  in hand first; this file does not reorder the levels on its own.
- **Commit:** `terraform: pin the credentials service at zero minimum instances`.

### DON4. Every "dormant" and "opt-in" pin, moved in lockstep — docs and tests

**Decisions:** the ruling of 2026-11-05. **Verification:** `internal/cli` (`-run Docs`), `website`.

- **Files:** `internal/cli/doctor.go` (the two strings at `:693`, `:696`),
  `internal/cli/doctor_test.go` (`:2398`), `internal/cli/validate.go` (`:742`, `:744`),
  `internal/cli/validate_test.go` (`:3321`, `:3327`, `:3331`, `:3345`), `internal/cli/review.go`
  (`:213`, `:271`), `internal/cli/review_test.go` (`:299`), `internal/cli/run.go` (`:2486`),
  `internal/cli/upgrade_cloud.go` (`:407`), `internal/cli/creds.go` (`:325`),
  `internal/cli/init_credentials_test.go` (the largest pin set — see below),
  `internal/cli/docs_credentials_auth_list_test.go` (`:428`'s banned list and `:365`),
  `internal/cli/docs_credentials_runbook_test.go` (`:34-35`, `:141`),
  `internal/cli/docs_credentials_cascade_checks_test.go` (`:103-104`, `:173`),
  `internal/cli/docs_credentials_bundle_test.go` (`:175`, `:246`),
  `internal/cli/docs_security_test.go` (`:405`), `internal/cli/cascade_test.go` (`:1086`),
  `internal/cli/doctor_brokering_test.go` (`:335`, `:1218`, `:1373`),
  `internal/cli/managed_test.go` (`:941`), `internal/cli/doctor_secret_readers_test.go` (`:83`, `:427`),
  `internal/cli/run_credentials_env_test.go` (`:65-75`, `:126`),
  `internal/infra/readiness_credentials_test.go`, `internal/infra/credentialsvars_test.go`,
  `internal/infra/installation_test.go`, `internal/infra/spec_test.go`,
  `internal/infra/managed/marker_test.go` (`:1829`), `internal/localcfg/shared_test.go` (`:493`),
  **`internal/config/providers.go` (`:542`, `ServiceHeldSecretNames`'s own doc: "once an installation
  opts in AND the job's own account has given its own copy up")** and
  **`internal/cli/init_coordinator_test.go` (`:113`, `TestTheCoordinatorOptInIsRefusedWhereItCannotBe
  Confirmed`, whose comment states "the opt-in follows `--provision-credentials` — `--yes` never covers
  it")** — the last two added by the review, and both are **Go sources with a retired phrase in them**,
  not documents, which is why they belong in this list rather than in the table below.
  **At most 28 Go files**, plus the documents in the table below — and the cap is the list, not a
  licence to widen it.
- **The documents this task moves, and no others.** **This file's own directory is out of scope for
  DON4's document half and is DON5's** (§3 DON5). The documents DON4 moves are the ones that state the
  posture **outside** `docs/credentials-service/`:

  | Document | What it says today |
  |---|---|
  | `CLAUDE.md` | L50, "the credentials service … is optional and ships dormant" |
  | `.claude/rules/credentials-service.md` | L16, "The service ships dormant. An installation opts in with `fugaro update --provision-credentials`…" |
  | `SECURITY.md` | L34, L66, L68, L74, L76, L92 ("**Off unless an installation opts in.**"), L142, L146, L168 |
  | `website/content/security.md` | L37, L69, L71, L77, L79, L95, L123, L145, L149, L171 (the website mirror — the two move together) |
  | `website/content/cli-reference.md` | L4827, L5013 — **generated** by `go run ./ee/clidoc`; the flag row leaves when DON1 removes the flag, and the file is regenerated rather than edited |
  | `website/data/release-summaries.yaml` | L47 (v0.10.1's summary, "while staying dormant until an owner opts in") |
  | `website/content/subscription-terms.md` | L262, "the **optional credentials service**" |
  | `docs/licensing/subscription-terms.md` | L278, the same sentence |
  | `docs/release.md` | L167, "It still **ships dormant**: `credentials_service` is null by default…" |
  | `docs/gcp-live-checklist.md` | the Check 94/95/99 rows (`:767`, `:768`, `:770`) and the Check 94 section (`:7522`, `:7524`, `:7526`, `:7581`, `:7687`, `:7739`, `:8270`, `:8472`) |
  | `docs/secrets-brokering/{reconciliation,implementation-plan,README}.md` | L25, L61, L245-246, L355 (D502), L411, L451; L52, L75, L82, L572; L5 |
  | `docs/bitbucket-credentials/reconciliation.md` | L231 (F13), L503 (D362), L656 |
  | `docs/bitbucket-credentials/implementation-plan.md` | L95, "`fugaro creds set` and `fugaro update --provision-credentials` are never run by an agent" — the flag half is retired; the `creds set` half **stays true** and the task keeps it, which is why this row is a wording change and not a deletion (added by the review) |
  | `docs/run-without-checkout/{spec,reconciliation}.md` | L80-81; L145, L257, L296 |
  | `docs/stateless-orchestrator/reconciliation.md` | L38 |
  | `docs/c39-review-identity/{README,implementation-plan,reconciliation,spec}.md` | L29; L50; L200; L913 |
  | `docs/cloud-senior-review/{README,spec,reconciliation,implementation-plan}.md` | L60, L135, L163, L165; L8, L77, L230, L233, L601, L1066; L136, L413, L589, L590, L812 (E7); L97, L100, L243, L575, L579, L757, L793, L1146, L1167, L1203, L1355 |
  | `docs/ee-reorg/{implementation-plan,reconciliation}.md` | L542; L65 |
  | `.goreleaser.yaml` | L116, the flag named in a comment |
  | `ee/coordinator/cli/coordinator.go` | L961, the read-mint refusal naming the flag |
  | `deploy/terraform/gcp/modules/installation/variables.tf` | L439, "**OFF by default (null)**" |
  | `deploy/terraform/gcp/roots/installation/variables.tf` | L246 |
  | `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` | L2381 |
  | `deploy/terraform/gcp/modules/installation/credentials.tf` | L1-6, L19-24 (the file's own header) |
  | `schemas/settings.schema.json` | L423-425, `"unset = none: the credentials service is not deployed"` |
  | `internal/localcfg/localcfg.go` | L275 |

  **The lockstep rule, and it is the trap this task must not fall into.** Two sets of pins point
  opposite ways today, and a wording change satisfies one and breaks the other unless they move in the
  same commit: `internal/cli/docs_credentials_auth_list_test.go:428` and
  `internal/cli/docs_credentials_runbook_test.go:141` **ban** the phrase "ships dormant" (they were
  written when 0.10.1 wired the launch and the phrase became false), while
  `internal/cli/docs_credentials_cascade_checks_test.go:103-104` **require** it in the live checklist's
  Check 94 row. Both are correct about the tree they were written against and both are wrong after this
  ruling, so DON4 moves them **together** and says so in its commit.
- **Interfaces:** produces no code interface. It produces the repository's **one** story about the
  service's posture, which is what makes the ruling checkable at all: after it, no document says the
  service is optional and no test requires that it be.
- **Tests:** the pinned sentences themselves are the tests, and the task adds the two that did not exist:
  `TestNoDocumentCallsTheCredentialsServiceOptional` (a repository-wide scan for the retired phrases —
  "ships dormant", "optional credentials service", "opt-in" applied to *this* service — over `docs/`,
  `website/`, `SECURITY.md`, `CLAUDE.md`, `.claude/rules/`, the release notes and `.goreleaser.yaml`,
  whose footer is the body published with **every** GitHub release and which the fifth review round
  found still naming the retired flag, with the same
  false-positive discipline `internal/cli/docs_live_init_census_test.go` uses for the roots it exempts:
  the coordinator's and the accounts' own "dormant" uses are **not** this service's and are named in the
  test as excluded) and `TestTheCredentialsServiceIsDeployedForEveryInstallation` (the sentence this
  file's ruling turns into, required in `SECURITY.md` and in the website mirror).
- **Red step:** `TestNoDocumentCallsTheCredentialsServiceOptional` fails on today's tree with the full
  list above. It is written first and its failure is the task's work list.
- **Mutation to hold it:** restore one retired sentence in any of the documents → the first test fails
  and names the file — the release template included, which is why its footer is a scan root; delete the
  new required sentence from `SECURITY.md` → the second fails.
- **Reviewer tier:** **B** (a docs-and-pins task, with the SECURITY.md half reviewed as a security
  claim — `SECURITY.md`'s statements about what a job holds are claims, and D148 is why they move
  deliberately).
- **OWNER-RUN:** no.
- **Depends on:** **DON1** (the flag's removal changes the strings this task moves) and **DON3** (the
  guard's wording).
- **Commit:** `docs: the credentials service is deployed for every installation, not opt-in`.

### DON5. The release notes, and an operator section that has exactly one step

**Decisions:** the ruling of 2026-11-05; `CLAUDE.md`'s HARD RULE;
[docs/releases/README.md](../releases/README.md)'s `### For operators` rule. **Verification:**
`internal/cli` (`-run Docs`), `website`.

**AMENDED AFTER REVIEW, and settled by the owner's answer of 2026-11-05.** The first draft's step 2 is
struck: it read "where the bundle holds no GitHub App key, `fugaro update` will say so and the one step
left is yours: `fugaro creds set github-app-key`". Written that way it is **a second command in a
release's `### For operators` section**, which is exactly what `CLAUDE.md`'s HARD RULE forbids ("a
release must not ship an operator step that is another command"). `fugaro creds set` is a real thing an
owner does — §2.1 keeps it as the bundle's only writer — but it is a step of a **capability**, not a
repair of the upgrade, and the release notes may not present it as something `update` left undone.
**The answer makes the section step 1 alone, and the note under it is the whole of what changes.**
**Reading (b) is not written**: the answer is deploy now, switch later, so `update` migrates nothing and
no step runs at a terminal.

- **Files:** `docs/releases/vX.Y.Z.md` (the release this lands in),
  `website/data/release-summaries.yaml` (its one line, no backticks),
  `docs/credentials-service/{README,implementation-plan,runbook,reconciliation}.md`. **6 files.**
- **Interfaces:** the release's `### For operators` section reads, in full and in this shape, **and it
  is one step**:

  > ### For operators
  >
  > 1. Run `fugaro update` once per installation. It deploys the credentials service on every
  >    installation now, whether or not you opted in before; there is no second command and nothing to
  >    uninstall.
  >
  > **Note.** Your runs are unchanged by this release. The service is deployed and idle, and no run
  > uses it yet: a repository's runs move onto it only once the service holds what that repository's
  > auth mode needs, and `fugaro update` does that by itself when it does. `fugaro doctor` says which
  > repositories are on it and, for the others, why not. Nothing to do.

  **And the three rules this section is written under, each of which the first draft broke:**
  **no step 2**; **no `creds set` named as a step of the upgrade** — it may appear in a note as the way
  to *change* a credential the service already holds, which is what it is, and never as the completion
  of `fugaro update`; and **no step that uninstalls or opts out**. The conditional-step precedent the
  first draft cited (`docs/releases/v0.10.1.md:14`) is still the right **shape** for the note; it is not
  a licence for a second step.
  **The question the first draft left open — what turns the switch on — is answered by DON8 and is not a
  second command:** it is **the same `fugaro update`**, run whenever the owner runs it anyway, with the
  bundle read at that moment and a `doctor` line saying why not. **What that costs, stated so the note
  is not read as promising more than it can:** an owner who fills the bundle and never runs `update`
  again stays switched off — that is the conservative direction, every run keeps working, and `doctor`
  names it. **The one thing the note may not claim is that the switch needs no further action ever**;
  it claims what is true, which is that no *separate* command is needed.
- **The documents this task moves are this directory's own**, and it moves them rather than DON4,
  because they are this file's neighbours and because one of them (`README.md`) is where this file is
  linked from. It changes: `README.md`'s status paragraph and its table (the new row for
  [default-on.md](default-on.md), and the `runbook.md` row's description of what the runbook is *for*);
  `runbook.md`'s L3, L7, L15 and L62; `implementation-plan.md`'s status lines (L5, L9) and the C1.20 /
  C1.24 task text that describes the stage; and `reconciliation.md`'s §4, where the amendment is
  recorded as an amendment rather than as a rewrite (the file's own rule: "Where it disagrees with
  `spec.md`, it wins" — and a dated amendment is recorded, not back-dated).
- **Tests:** the existing pins are the tests, and they move with the sentences:
  `internal/cli/docs_credentials_runbook_test.go`, `docs_credentials_bundle_test.go`,
  `docs_credentials_auth_list_test.go`, `docs_credentials_plan_tests_test.go`,
  `internal/cli/docs_coordinator_test.go` (its `coordinatorCredentialsReadme` row at `:1827` reads the
  README's numbers table — **that table and its rows are kept exactly as they are**, which is the
  "keep the existing pinned sentences" rule this file is written under).
- **Red step:** `go test ./internal/cli/ -run Docs` fails once the README gains its row and the runbook
  its new sentences, until the pins move with them.
- **Mutation to hold it:** put a second numbered step back in → the HARD RULE is broken in the one
  surface a person reads, which is what the review caught; drop the note → an owner reads that their
  runs changed when they did not, or reads the switch as needing a command it does not need; make the
  note promise that nothing further is ever needed → it claims more than DON8 can make true.
- **Reviewer tier:** **B**.
- **OWNER-RUN:** no.
- **Depends on:** **DON1** (the flag's removal is what step 1's wording reports), **DON4** (the wording
  must be the same in both) and **DON8** (the note's second sentence is a description of what DON8
  does, and may not be written before it is true).
- **Commit:** `docs: the release notes for a credentials service that deploys itself`.

### DON6. The managed project: how an installation with no owner at a terminal reaches it

**Decisions:** the ruling of 2026-11-05; Global Constraint 7 (the managed path never deploys the
credentials service). **Verification:** `internal/cli`, `internal/managed`.

- **Files:** `internal/cli/upgrade_cloud.go` (its credentials-service string at `:407`),
  `internal/cli/managed_apply.go`, `internal/cli/managed_apply_test.go` — plus whichever file the task
  finds holds the managed convergence path, which it names in its own pull request rather than guessing
  here. **At most 4 files**, and **not** `internal/cli/managed_test.go` or
  `internal/cli/update_managed_test.go`, which belong to DON4 and DON2 (§4.1).
- **Interfaces:** the managed case is a genuine exception and must be **stated rather than papered
  over**, and the reason is already in the tree: `credentials.tf`'s own header says "`fugaro managed
  apply` never deploys it (Global Constraint 7)". So a **managed** project's `fugaro update` cannot set
  `credentials_service` the way an ordinary installation's does — the managed path applies a
  **published** state, not the owner's own root. Two readings, and the task takes one and says why:
  - **(i) the managed project reaches it through the ordinary path**: the managed project's owner runs
    `fugaro update` from a checkout (which is what `update` already is for a managed project — the
    managed half is what it *also* applies), and the installation root is applied by that same run.
    This is the reading that matches the HARD RULE.
  - **(ii) the managed project does not get the service**, and `doctor` says so in the one line DON7
    writes, with the reason (the managed path applies a published state, and the service is deliberately
    not in it).
  **The task determines which is true of this tree** — it is not decided here, because it depends on
  whether a managed project's `update` applies its own installation root, which the task reads rather
  than assumes — and whichever it finds, the refusal or the line names it rather than leaving a managed
  project to discover a service it cannot reach.
- **Tests:** `TestAManagedProjectsUpdateReachesTheCredentialsService` (reading (i)) or
  `TestAManagedProjectIsToldTheServiceIsNotDeployed` (reading (ii)) — the task writes the one its
  determination supports, plus `TestTheManagedPathNeverAppliesTheCredentialsModule` (Global Constraint
  7 still holds: `fugaro managed apply` plans no `google_cloud_run_v2_service.credentials`, whatever
  `update` does).
- **Red step:** the first test fails on today's tree in either reading — no managed project reaches the
  service at all, and nothing tells it why.
- **Mutation to hold it:** make the managed apply plan the credentials module → the third test fails
  and Global Constraint 7 is broken.
- **Reviewer tier:** **A** (a managed project is one an agent operates; getting this wrong is a
  silently absent service).
- **OWNER-RUN:** no.
- **Depends on:** **DON1**.
- **Commit:** `cli: what a managed project's update does about the credentials service`.

### DON7. `doctor` reports the service state in one line

**Decisions:** the ruling of 2026-11-05. **Verification:** `internal/cli`.

- **Files:** `internal/cli/doctor_brokering.go`, `internal/cli/doctor_brokering_test.go`, and the one
  short form the default view needs in `internal/cli/doctor_render.go` (`:829`, the sentence the
  one screen prints for this ID). **3 files.** **`internal/cli/doctor.go` is deliberately NOT this
  task's** — its two flag-naming strings (`:693`, `:696`) are DON4's, so that no level holds two
  writers of one file (§4); `doctor_render.go` is no task's in §4's table, and the entry is the one
  this check's line cannot be correct without.
- **Interfaces:** `doctorCredentialsServiceDeployed` (`internal/cli/doctor_brokering.go:102`) stops
  returning `nil` for an installation with no `credentials_service` block — that early return
  (`if lc.CredentialsService == nil { return nil }`) is exactly the sentence the ruling makes false: an
  installation that has not taken the opt-in is now a state worth reporting rather than one worth
  staying silent about. It produces **one line**, in the shape the other checks already use (an `ID`, a
  `Severity`, a `Problem`, a `Fix`) that names **which** half is missing. **What the one screen prints
  is the ID's short form and not the state's** (`doctor_render.go:829`): the lookup has the ID and
  nothing else, so that sentence has to be true of every state this check reports as a **warning** —
  the service not deployed, a job account it does not admit, an empty bundle, a newest version Secret
  Manager will not serve — while the state's own `Fix` stays the line under it. A short form that named
  one state's problem (the callers, the invoker grant) put a sentence and a command about two different
  things on one screen for the two bundle states. The line for an installation
  that never opted in says that state and not one of being behind: **on this release the flag is the
  opt-in**, what it changes is the installation's own workflows, and a doctor line is read as a step to
  take, so that line says what the opt-in asks for and does rather than presenting a run that re-writes
  every repository root as routine (DON1 rewrites it when the flag goes, and then the state is
  genuinely "update and the service is there"):

  | state | the line |
  |---|---|
  | no block in the local config | the installation has not opted into the service: this release deploys it only behind `fugaro update --provision-credentials` (the flag DON1 removes: until it lands, the credentials stage is registered behind it and nothing else runs it), and every run of an installation that never asked for it fetches its model credential the way it did before the service existed, so nothing here is broken or behind. Taking the opt-in is what moves the credential out of the jobs: every repository root re-applied after it drops its jobs' model credential in **every** `agent.auth` mode (which is why a project that takes it runs its workflows with `agent.auth: cascade`), the stage asks for its own **typed confirmation** (a `--yes` run does not cover it), and a GitHub run of it refuses to mint until the bundle holds the App's key (`fugaro creds set github-app-key`) |
  | block present, Cloud Run answers `NOT_FOUND` | the block is written and the service is not deployed; `fugaro update --provision-credentials` re-applies the root (the same flag as the row above, for the same reason) |
  | deployed, the bundle holds no version | deployed and admitting nobody: the bundle is empty, and `fugaro creds set NAME` is the writer. **Warning**: the loader fails closed, so every model call of a run against it is refused |
  | deployed, the bundle's newest version is not `ENABLED` | deployed and admitting nobody: the version and its document are there and Secret Manager will not serve them, so the loader's read fails closed — the same refused model call as the row above, so the same **warning**. The line names the version and its state; `DISABLED` is the one an operator's incident disable leaves, and the fix names the enable (`gcloud secrets versions enable <version> --secret=<id>`) because `creds set` refuses while the newest version is disabled |
  | deployed, `verifier_accounts` written **empty** | deployed and admitting nobody on the verify route: a list written empty names no verifier account. The list is the one the service is deployed with (`localcfg.VerifierAccountsEffective`): this checkout's `verifier_accounts` when it states one, the published policy node's otherwise, and the line names which of the two holds the empty list. An absent key is NOT this state — it leaves the module's own account admitted |
  | deployed, a job account missing `roles/run.invoker` | the existing half of the check, unchanged |

  **And, because the answer made the switch a state of its own, one line per repository — the "why
  not" the owner asked for by name (DON8).** It is not a sixth state of the service; it is the same
  check reporting the half the service's state does not cover, which is *which repositories' runs
  actually go through it*:

  | repository's state | the line |
  |---|---|
  | switched on | the repository's runs go through the service; the bundle holds its credential and its auth mode is one the service serves |
  | held off, `agent.auth` is not `cascade` | held off: the service serves a `cascade` workflow today, and this repository's `agent.auth` is `<mode>` |
  | held off, the bundle holds no model credential | held off: the bundle holds nothing to replace the job's credential; `fugaro creds set NAME` is the writer |
  | held off, never converged since the switch landed | held off: `fugaro update` turns it on once the bundle is ready |

  **The line is read, never guessed, and it is the same read the switch itself makes (§1.5, DON8)** —
  `Bundle.Credentials()` through the service's own loader for the second row, `config.AuthCascade` for
  the first. A `doctor` that answered this question with its own second implementation would be a
  second answer to "is this repository switched on", and the first time the two disagreed `doctor`
  would be the one lying.

  **The severity is the state's own, and it is the state's own for the one reason doctor's classes
  exist: whether a run can work around it.** The four states a run cannot work around keep the
  **warning** the check already has — the block written and no service behind it, a job account the
  service does not admit, and the two where the service is deployed and has nothing to serve with (a
  bundle holding no version, a newest version Secret Manager will not serve) — for the reason the
  classes exist: they print in the default view, and `--strict` is where they become a failure. A
  measurement there would print "everything checked is ok" and exit 0 under `--strict` for an
  installation whose every model call is refused. The two that are states rather than faults are `OK`
  with severity **info**, doctor's measurement class: **an installation that has not taken the opt-in
  is not broken** (on this release it is running the way it did before the service existed, and DON1
  makes it an installation that has not run `update` yet), and **a verify route emptied on purpose is
  not broken either, it is the answer's own state** — so neither may be an item of doctor's one screen,
  and a clean installation would print an item for merely being behind the release. They are the answer
  §1.4 asks for under `--all` and in `--json`, where a reader who wants the service *used* reads which
  half is missing, and neither of them fails `--strict`.
- **Tests:** `TestDoctorReportsTheServiceStateInOneLine` (the six service states, each producing
  exactly one check with the expected `ID` and severity), `TestDoctorNamesTheMissingHalfNotNotConfigured`
  (the line never reads "not configured": it names the App key, the verifier account, the bundle or the
  invoker grant), `TestDoctorSaysWhyARepositoryIsHeldOff` (the three held-off rows, each naming its own
  reason, and an `oauth` repository never reported as switched on), and the existing
  `doctor_brokering_test.go` cases at `:335`, `:1218`, `:1373` move with the `Fix` strings DON1's flag
  removal changes.
- **Red step:** `TestDoctorReportsTheServiceStateInOneLine` fails on today's tree: a config with no
  block produces **no check at all**.
- **Mutation to hold it:** put the `nil` early return back → the first test fails; collapse the five
  states into one "not configured" line → the second fails; **report a repository as switched on
  without reading the bundle** → the third fails, and it is the mutation that matters most, because a
  `doctor` that says "on" while the job's credential is gone is worse than one that says nothing.
- **Reviewer tier:** **B**.
- **OWNER-RUN:** no.
- **Depends on:** **DON1** — and its per-repository half on **DON8**, whose predicate it reads. **The
  file is one writer's (§4.1), so this is one task and not two:** the service line and the
  per-repository line are one check's output, and a second task writing the same file would be the
  collision §4.1 exists to prevent. **If DON8 slips, this task's per-repository half slips with it and
  the service half lands alone** — which is why the two halves are separate tables above.
- **Commit:** `doctor: one line for the credentials service's state, deployed and admitting nobody`.

### DON8. The switch: `update` turns a repository's runs onto the service, and only when it can serve them — SECURITY-SENSITIVE

**Decisions:** the owner's answer of 2026-11-05 (§5 item 1); `CLAUDE.md`'s HARD RULE. **Findings:**
§1.4 (the drop is auth-blind) and §1.5 (what the switch flips, and each flip's condition).
**Verification:** `internal/infra`, `internal/cli`, `internal/localcfg`.

**This task is the SWITCH, and it is new after the answer.** It is the second half of the ruling: DON1
deploys the service everywhere, and this is what turns a repository's runs onto it — **later, per
repository, and only where the service can serve that job.** It is **the same release as DON1 and it is
a release blocker for it**: §1.4 verifies that DON1 alone makes a repository root drop its model mounts
the next time that root is applied, so a release that shipped DON1 without this task's gate would break
the field. **It needs no second command**, which is the owner's own constraint: the switch is
`fugaro update`'s act, and the readiness it reads is a read `fugaro doctor` already makes.

- **Files:** `internal/infra/spec.go` (a `repoCtx` field and its resolver beside `resolveBitbucketProxied`
  at `:624`, and the two decision points at `:1177` and `:1314`), `internal/infra/spec_test.go`,
  `internal/infra/repo.go` (**the backfill's evidence**, `RepoCredentialsServiceApplied` and its
  address, beside the repository state readers it is built on), `internal/infra/repo_test.go`,
  `internal/localcfg/localcfg.go` (the per-repository record, beside `Repo.Vertex` at `:1030-1033`),
  `internal/localcfg/shared.go` (`mergeRepoRecords`, the one field a union carries rather than wins, and
  the `SharedDiff` rule that follows from it), `internal/localcfg/shared_test.go`,
  `internal/localcfg/select_shared_test.go`, `internal/cli/init.go` (the converge path that writes the
  record — the same place `internal/cli/init.go:4271` writes `Vertex` — and the backfill's one call from
  `install`), `internal/cli/init_credentials.go` (both doors: the record write and the backfill that
  reads the installation's state), `internal/cli/run.go` (`credentialsServiceLaunchEnv` at `:3647` and
  its call site at `:3553`), `internal/cli/update_switch_test.go` (new, so that DON2's
  `init_converge_test.go` stays DON2's — §4.1), `internal/cli/run_credentials_env_test.go`. **The count
  is the honest one and not six**: the two review rounds after the first plan added the third reader of
  the record (`internal/localcfg/shared.go`), the state-bucket evidence (`internal/infra/repo.go`) and
  the second door (`internal/cli/init_credentials.go`), each with its test — **`internal/cli/doctor_brokering.go`
  is still not one of them**: DON7 owns that file and reads this task's predicate (§4.1).
- **Interfaces:** three conditions, and **all three must hold** for a repository's runs to go through
  the service. They are checked in **one place**, an exported predicate in `internal/infra` that
  `internal/cli`'s launch path and DON7's `doctor` line both call — a second implementation of "is this
  repository switched on" would be a second answer, and the first time the two disagreed `doctor` would
  be the one lying:
  1. **the installation has the block** — DON1's deploy, and it is the precondition rather than a
     condition: without it there is no service to switch to;
  2. **the bundle is ready** — the bundle holds **at least one model credential**, read through **the
     service's own loader** and not through a new one (§1.5: `openBundleStore` → `check` → `newest` →
     `access` → `credsvc.ParseBundle` → `len(b.Credentials()) > 0`, the read `doctorBundleChecks` at
     `internal/cli/doctor_brokering.go:432` already performs). **This is the condition that makes the
     switch safe**, because the mount it removes is the credential the service must then hand back;
  3. **the repository's `agent.auth` is one the service serves** — today `config.AuthCascade` alone
     (`internal/config/defaults.go:77`), and this is the condition the review's finding is about. **It
     is read live from `c.in.Cfg.Agent.Auth` at every build of the repository root's spec**, not stored:
     the auth mode is in `fugaro.yaml` in the checkout, and a stored copy could outlive an edit to it,
     which is the one way this switch could break a run that used to work. The record below carries a
     copy of the auth **an apply was made for** — a fact about that apply, not a second source for this
     condition: the launch reads the live auth whatever the record says, in both directions of an edit
     (the header's third 2026-11-07 row).
- **The record, and why there is one.** Conditions 1 and 3 are readable wherever a spec is built, but
  **condition 2 is not**: the bundle is a Secret Manager secret, `internal/infra` is a hermetic spec
  builder that reads no cloud, and `internal/infra/spec.go`'s whole input is the local config, the
  checkout's `fugaro.yaml` and the published policy. So the readiness half is **recorded** by the
  command that does read the cloud — `fugaro update` — as a per-repository record in the local config,
  following `localcfg.Repo.Vertex` (`internal/localcfg/localcfg.go:1030-1033`, the fact `update` sets
  from `infra`'s spec at `internal/cli/init.go:4488`). **The record is written where the root is
  applied, and only there** (the header's second *amended after review* row): `fugaro update --repo PATH`
  — and the converge's repository stage, which is the same engine — reads the bundle once and refreshes
  the entry for the repository whose root it applies. **The installation door applies no repository root,
  so it writes no record for one — with one exception, and it is the migration this release owes**
  (added by the follow-up run; the word *backfill* is this record's). An installation that opted in under
  an earlier release has roots whose tfvars already carry `installation.credentials_service` and entries
  that carry **no record at all**, because the field did not exist in the release that applied them — and
  this release's launcher reads the record, so without the backfill every one of those repositories would
  run held off until its own checkout ran another `fugaro update --repo`, one checkout at a time.
  `fugaro update`'s installation door
  therefore calls `backfillCredentialsServiceReady` (`internal/cli/init_credentials.go`, from `install()`
  before `writeConfig`) once, for every entry that carries **no record** and whose **own** root's
  **state** says the service is on — `infra.RepoCredentialsServiceApplied`
  (`internal/infra/repo.go`): the repository root's state objects in the **installation's state bucket**
  (`fugaro/repos/<slug>/*.tfstate`), parsed and matched by address, so the evidence is the root that was
  applied and never a blanket yes: a repository whose state holds no such grant — never applied
  anywhere, applied with the service off, or forgotten — keeps the record it has. **The state, and not
  the tfvars in a workdir** (the seventh review round's second finding): `infra.RepoWorkdir` is
  **per-machine** state (`<state>/fugaro/terraform/<project>/repos/<slug>`), so a repository whose root
  was applied from another machine, or from another checkout of this one, has no document here at all
  and the workdir read would report "never applied" for a root that is applied — converging the
  upgrade one checkout per `fugaro update`, which is an operator step beside `fugaro update` and what
  CLAUDE.md's HARD RULE forbids. The state is also the honest half of the pair: the tfvars are what a
  run *handed* Terraform, so an apply that failed or was refused leaves a document ahead of the state,
  and the state bucket is written by whichever machine ran the apply and read by the identity that runs
  the update already. It is the same fact the repository door's own refresh is built on, one door
  earlier, because the repository stage converges the checkout's own root alone and the siblings would
  otherwise need one `fugaro update --repo` each. The workdir read stays where it is still the right
  question — `deployedRepoCredentialsService`, a launch outside any checkout, which may read no state
  bucket and needs only this machine's own last apply — **but it is one answer and not the only one**
  (the eighth review round's first finding): a machine that never applied that root holds no tfvars of
  it, and that machine is *every* console launch, because `console/src/main/launch.ts` spawns
  `fugaro run --repo` from a private empty directory, so the document is never readable there. The
  empty auth is therefore judged on `installed && (record || deployed)` — either the repository's own
  record or this machine's tfvars — since both are written by an apply that dropped the job's mounts
  and granted its account `roles/run.invoker`, and withholding the address from a repository whose
  mounts are already gone halts its `cascade` run at the service's door (D143). **A recorded `false` is
  revisited** (the eighth round's second finding): it is an apply that ran with the switch off, which is
  evidence and not an absence, but a record carries no stamp, so a `false` left by an *earlier* apply
  outranks the `true` a *later* apply on another machine published — installation-wide, for every
  machine that reads the published file, with no `fugaro update`-only way back. The backfill therefore
  reads the installation's own state for an entry whose record is not `true` — no record, or a recorded
  `false` — and writes the record true again where the state shows the grant and this run's bundle
  holds a model credential. An entry already recorded `true` is still never revisited. The reason is the grant: the thing that makes the
  service usable to a repository is the `roles/run.invoker` grant its **own** root's apply makes
  (`deploy/terraform/gcp/modules/workflow/sa.tf`'s `google_cloud_run_v2_service_iam_member.credentials`;
  the installation root grants `run.invoker` to the operators, the coordinator and the verifier and to no
  job account), so a record written for a repository whose root was not applied would tell that
  repository's launch the service is ready while its job accounts hold no grant — and a `cascade` run has
  no credential of its own to fall back to (D143). **It is written `false` as well as `true`**, and it is
  written as **the value the plan was built with** and not from a second reading of the bundle, for the
  same reason in the other direction: a record that says ready for a root whose plan was made held off is
  the same broken state. A repository joining the local config is **not** that case (the seventh review
  round's third finding): `recordCredentialsServiceReady` makes its entry — provider and nothing else —
  **before `infra.Repo` reads it**, so the spec the run plans from carries this run's record, the root is
  deployed for the service, and the write after the apply fills the entry in. An entry created only in
  that write, which is where it was first written, left the onboarding `fugaro update --repo` deploying a
  **held-off** root — mounts kept, no `roles/run.invoker` — while recording `false`, so the launch after
  it withheld the address too and a `cascade` run had no model credential and nothing to swap it for
  until a **second**, unannounced update in the same checkout. **On main one run sufficed for a first
  onboarding, and it still does** — and `fugaro update --repo` is the documented way a repository joins
  the config, so the second run would have been an operator step inside the one documented command.
  **The record is a tri-state, and it has to be** (the seventh round's first finding): "no record" says
  *no machine has judged this root*, a recorded `false` says *an apply ran with the switch off*, and the
  field is a pointer so that both are documents a config can hold
  (`localcfg.RecordSwitch`/`Repo.CredentialsServiceOn`; every reader of the switch reads the two the
  same way, and `MergeShared` is the one place that must not). The record is published with the local
  config, and `MergeShared` unions the repository entries with **the local entry winning a key clash**,
  so a machine whose entry carries no record would otherwise publish that absence — as `false`, which is
  what a boolean could only write — over the record another machine wrote from an apply it made: every
  launch of that installation, on every machine, then withholds the address from a `cascade` repository
  whose job gave its mounts up to that apply, and halts at the service's door (D143). So the record is
  the one field of a repository entry that is **carried rather than won**: a local entry with no record
  takes the published one, and a local entry that has a record wins in both directions, because only the
  door that applies that root writes one and a recorded `false` is the switch-off travelling to the
  other machines. The same rule keeps `fugaro doctor`'s shared-config diff honest: a published record
  over a local entry that has none is not a difference, since publishing again writes it back unchanged. **The record is the SWITCH, not the bundle
  reading**, and that means the auth of the plan it was built with is folded into it (the header's third
  2026-11-07 row, which is the round that found the gap): `recordCredentialsServiceReady` writes
  `infra.CredentialsServiceServes`' own answer, and not a second copy of its conjunction.
  The state it closes is the natural adoption order — an owner fills the bundle and runs
  `fugaro update --repo` while the checkout still says `agent.auth: oauth` (that root is planned held
  off: mounts kept, no `roles/run.invoker`), and commits the cascade afterwards. A record written from
  bare readiness would say ready there; the next launch reads the cascade from the document, sees the
  record vouch for the service and hands the job the address — whose grant no apply ever made — so the
  provider opens GitHub keyless for a mint the private service refuses and a `cascade` run has nothing
  of its own to fall back to (D143). Folded in, that run records `false`, the launch withholds the
  address, the job keeps the mounts that apply kept, and the run after the cascade is committed — the
  one that applies the root with the switch on — is the one that records `true`. The auth is judged as
  the RUNS read it (`deploymentConfig`: the `fugaro.yaml` at the default branch, else this working
  tree), the same document the plan above is built from, or the record and the plan are one edit apart.
  **And the plan's FIRST condition is in it too** (the header's fifth 2026-11-07 row): `installed` is
  read from the **installation root's own outputs**, the source the spec reads it from
  (`infra.Inputs.Installation`, which `withDefaults` never fills from the local config) — not from the
  local config's block, which is the stale half in the state an owner's opt-out leaves
  ([runbook.md](runbook.md) §9): the config still carrying the block and its URL, the installation's
  outputs carrying no service at all, and a `fugaro update --repo` in a checkout, which applies no
  installation root. The bundle's reading alone would record `true` there, for a root this run applies
  held off, and the next launch — which reads the config's block — would put the address of a service
  that is not there on a job whose mounts are still in place: the provider opens keyless for a mint
  nothing answers, and the run halts at the service's door (D143) while the credential it needed is
  still mounted and unread.
  The backfill needs no such fold: its evidence is the deployed root's own tfvars, which carry the
  service exactly when that apply dropped the mounts and made the grant, so the auth of that apply is
  already in the document it reads. A bundle that is later emptied switches the
  repository back off on the next `update`, which re-adds the mounts, which is the fail-safe direction
  and not a failure. **Both doors leave the record alone in a coding agent's session and in a run's own
  process** (the header's 2026-11-07 row): the bundle holds every model
  credential the service issues, and the two commands that read it, `fugaro creds ls` and `fugaro creds
  set`, refuse both before their first call (`credsOwnerOnly`, `internal/cli/creds.go`), so
  `credentialsBundleReady` answers "nothing was read" there and neither door writes — every entry keeps
  the record the person's own last run left on it, and the repository door's plan is the one that record
  already answers. An agent's run therefore moves no switch; the person's own next `fugaro update` is
  what converges it, which is still the one command CLAUDE.md's HARD RULE promises. **A read that
  failed leaves the record alone the same way** (the header's other 2026-11-07 row): the three states
  that answer — no version, a newest version that is `DISABLED`, a document with no `KindModel` entry —
  are facts about the bundle and true for every reader, while an error is a fact about the identity
  that read. `credentialsBundleReady` says so in its second answer, which is `false` wherever there is
  no reading to write (both markers, every failure), so a `false` reaches the config only from a
  reading that produced it. The reason is what the record *is*: it is published with the local config
  (`internal/localcfg/shared.go`), so one identity refused the bundle's version list — an operator
  without the accessor role, a listing that failed — would write the switch off for every machine of
  the installation, and every teammate's `cascade` run would then be handed no service address by
  `credentialsServiceLaunchEnv` and stop at the service's door (D143) on the word of a machine that
  read nothing. **One run asks the bundle twice** — the installation door's backfill, then the
  repository stage's record (`init.go`'s `install` and repoEngine) — so the two answers are memoized
  **together**: memoizing the reading alone would leave the second door reading the memo's `false` as a
  reading of its own and writing it, which is the same installation-wide `false` reached through the
  run's own second door rather than through another machine. The cost of the rule is a convergence, not a wrong write: an entry the failed read
  could not judge stays unrecorded, and the next run that *can* read the bundle — the same door, the
  next time it works — is the one that records it. The three answering states are kept as writes
  because each of them means the same thing to every reader of the bundle, the service included.
- **What the switch flips, and the one thing it does not touch.** §1.5's three flips are this task's
  output, and **flip 1 needs no Terraform change at all** — which is the finding that makes this task
  small. `deploy/terraform/gcp/modules/workflow/sa.tf:148-151` gates the mount drop on
  `var.credentials_service == null`, and that variable is what `modules/repo/workflows.tf:36` passes
  down from the repository root's `installation.credentials_service`. `internal/infra/spec.go:1314`
  builds that value through `repoCredentialsService` (`:244-248`). So **returning `nil` from
  `repoCredentialsService` for a repository the switch holds off is the whole of flip 1**: the module
  receives null, `sa.tf`'s own expression keeps the accessors and the mounts, and the job account gets
  no `roles/run.invoker` on the service. `sa.tf` is not edited, the `workflow` and `repo` modules'
  variables are not edited, and **no golden plan moves** — every installation's repository roots plan
  today exactly what a held-off repository must plan. Flip 3 is the same predicate at
  `internal/cli/run.go:3647`, where the launch decides whether to set `FUGARO_CREDENTIALS_SERVICE_URL`;
  flip 2 (`internal/gitprov/providers/providers.go:93-99`) follows the URL and needs nothing.
- **Tests:** `TestTheSwitchIsOffUntilTheBundleHoldsAModelCredential` (a converged installation with an
  empty bundle plans a repository root whose `installation.credentials_service` is null and whose jobs
  keep their model mounts — the review's own regression test, moved here from DON2 with its body and its
  fixture: **`TestAnOAuthInstallationStillLaunchesAfterUpdateAlone`**, a 0.19.0 installation whose
  workflow is `agent.auth: oauth` and whose job mounts the GitHub App key **still launches a run after
  `update` alone**, asserting the job's `job_secret_names` still carries its model credential's logical
  name, the App key is still readable by the job's account, and `credentialsServiceLaunchEnv` hands the
  launch no URL — **it is written against the tree's own fixtures and reads the plan, not the prose**,
  the shape `TestServiceHeldSecretsMatchTheCredentialsService` and `dropModelMounts`' own tests use,
  because the failure it catches is a Terraform expression and a Go predicate disagreeing);
  `TestTheSwitchTurnsOnForACascadeRepositoryWithAReadyBundle` (a `cascade` repository of an installation
  whose bundle holds a model credential plans the drop, the invoker grant and the URL — all three
  flips, from the one predicate); `TestTheSwitchStaysOffForANonCascadeRepositoryWithAReadyBundle` (the
  same installation, `agent.auth: oauth`, **every one of the three flips still off** — the condition the
  review's finding is about); `TestEditingTheAuthModeTakesEffectWithoutAnotherUpdate` (a record that
  says on, a `fugaro.yaml` that now says `oauth`, and the spec holds the switch off — the live read of
  condition 3); `TestTheRecordIsWrittenFalseWhenTheBundleIsEmptied` (the fail-safe direction);
  `TestTheRecordFollowsTheAuthTheRunsRead` (the record is the switch: one ready bundle, two auths, and
  the `oauth` one records nothing — the direction the third review round found, with the held-off apply
  it belongs to) and `TestACascadeRepositoryAppliedHeldOffIsNotHandedTheService` (that same state at the
  launch: a cascade document, the block present, no record, and the job is handed no address);
  `TestAnUnreadableBundleLeavesTheRecordAlone` (the fourth review round: over a bundle the record was
  written from, the version list starts answering 403, and the record is still `true` in the entry
  **and in the file `fugaro update` publishes** — the write a failed read may not make);
  `TestAnOptedOutInstallationRecordsNoSwitch` (the fifth: one rig and one ready bundle, and the
  installation's outputs move from carrying the service to carrying none — the config's block stays,
  which is the opt-out — and the record has to go with the plan, `false` and absent from the file);
  `TestAFailedReadIsNotAnsweredByTheRunsSecondDoor` (the sixth: one run, the backfill's refused read,
  and the converged repository's record still `true` in the entry **and in the file** — the run's own
  second door may not read the memo's "no reading" as an answer); and
  `TestNoRepositoryRootGainsAServiceTheBundleCannotServe` (the whole-predicate guard: **no** repository
  root in the fixtures plans `installation.credentials_service` set unless all three conditions hold);
  `TestMergeSharedCarriesTheRepositoriesCredentialsServiceRecord` and
  `TestSharedDiffIgnoresARecordItWouldPublishBack` (the seventh review round's first finding, in
  `internal/localcfg`: a published record over a local entry that has none survives the union **and** is
  not reported as a difference, while a local record is a difference in both directions — the second
  half is what keeps the fix from trading one wrong write for a `doctor` nag);
  `TestRepoCredentialsServiceAppliedReadsTheSharedState` (the seventh round's second finding, in
  `internal/infra`: never applied, applied with the service off, another repository's state, applied
  with the grant, a second workflow module, and an object that is not a state — the last one an error
  and not a `false`); and
  `TestAFirstOnboardingConvergesInOneRun` (the seventh round's third finding: a `fugaro update --repo`
  in a checkout the config does not hold yet, **one** run, and the entry carries a record — the shape
  the onboarding had on main); and the eighth review round's two, in `internal/cli`:
  `TestALaunchOutsideACheckoutReadsTheRecordOrTheDeployedRoot` (a `fugaro run --repo` from a directory
  holding no checkout — the console's own spawn — once with the root's tfvars, once with the record
  alone and no tfvars on this machine, both handing the address, and once with neither handing
  nothing) and `TestAStaleFalseIsCorrectedByTheInstallationsState` (two machines: the local entry
  records `false`, the installation's state carries the grant a later apply made, and one ordinary
  `fugaro update` leaves the record `true` **and the shared merge publishing it**).
- **Red step:** `TestAnOAuthInstallationStillLaunchesAfterUpdateAlone` fails the moment DON1 lands and
  this task's gate does not — the fixture converges, the repository root drops the model mount, and the
  launch hands the job a URL the service cannot serve. **It is the one test in this file whose red step
  is a real failure of a real change rather than of today's tree**, which is exactly why it is a release
  blocker rather than a guard: it fails on the release, not on the plan. **SEC flag:** the reviewer tier
  is **A** and the independent review is **mandatory** — this task is what stands between DON1 and every
  non-`cascade` repository in the field.
- **Mutation to hold it:** drop condition 2 and switch on readiness by the block alone → the empty-bundle
  test fails, and a `cascade` run in the field stops at the service's named refusal;
  drop condition 3 → the `oauth` test fails, which is the review's finding reintroduced; store the auth
  mode in the record instead of reading it live → the edit-the-`fugaro.yaml` test fails; record the
  bundle's reading alone and leave the auth to the launch → `TestTheRecordFollowsTheAuthTheRunsRead`
  fails, the `oauth` apply recorded ready over a root it planned held off; read `installed` from the
  local config's block instead of the outputs → `TestAnOptedOutInstallationRecordsNoSwitch` fails, the
  record vouching for a service the installation does not have; answer a failed read with
  `false` rather than with no answer → `TestAnUnreadableBundleLeavesTheRecordAlone` fails, and one
  refused listing publishes the switch off for the whole installation; memoize the reading without its
  record flag → `TestAFailedReadIsNotAnsweredByTheRunsSecondDoor` fails, the failed read written down by
  the run's own second door; make
  `repoCredentialsService` return the block regardless → the no-repository-gains test fails and `sa.tf`
  drops the mounts of a repository nobody switched on; implement the predicate a second time in
  `internal/cli` and let the two disagree → the launch and the plan part company, which is the state
  where a run gets a URL for a job whose credential was never dropped; make the union let the local
  entry win with no record, as a boolean could only write it → the merge test fails, and one machine
  that never applied that root publishes the switch off for the whole installation, which its own
  `fugaro update` cannot repair because it reads its own stale entry; report a record the union carried
  as a difference → the `SharedDiff` test fails, and `doctor` nags on every machine of the installation
  about a file that publishing again writes back unchanged; read the backfill's evidence from the
  workdir's tfvars rather than the installation's state → the converges-with-one-update test fails (its
  workdir does not exist, which is the machine that opted in from another checkout), and every root
  applied elsewhere stays unbackfilled until that checkout runs `fugaro update --repo`, an operator step
  inside the one documented command; write a new repository's entry only in the write after the apply →
  `TestAFirstOnboardingConvergesInOneRun` fails, and the first onboarding of a `cascade` repository
  deploys a held-off root while recording `false`, so its runs keep failing at bootstrap until a second
  update nobody announced; judge the empty auth on this machine's tfvars alone → the
  reads-the-record-or-the-deployed-root test fails on the record-only case, and every console launch of
  a switched-on `cascade` repository hands its job no address, so its runs fail at bootstrap with no PR;
  skip a recorded `false` in the backfill again → the stale-false test fails, and the machine that
  applied with the switch off keeps publishing its `false` over the `true` another machine wrote from a
  later apply, an installation-wide outage of the switch that no `fugaro update` repairs.
- **Reviewer tier:** **A.** **OWNER-RUN:** no — the task is code and tests. **Check 96 stays the
  owner's**, and this task does not depend on it: §1.5's flip 2 is wired today
  (`internal/runner/runner.go:1119`), so the switch mints through the service without the App key's own
  accessor being dropped.
- **Depends on:** **DON1** — and it is **the reverse edge too**: DON1 may not ship without this task.
  §4.1 places it in level 2, because the record write is a hunk in `internal/cli/init.go`, which DON1
  rewrites in level 1 — **and its release edge runs the other way**, which is a stronger constraint than
  the build order and one the level table cannot show: DON1 may not *ship* without this task, because
  §1.4 verifies that DON1 alone drops the mounts of a repository the service cannot serve. **A level is
  a build order; a release is not.**
- **Commit:** `cli: the credentials service switch, on only where the bundle can serve the job`.

---

## 4. Levels, shared files, the critical path, and what must precede what

### 4.1 Levels — no level holds two writers of one file

The rule is [../cloud-senior-review/implementation-plan.md](../cloud-senior-review/implementation-plan.md)
§2's: a file is one writer's at a time, and **within a level no two tasks may hold one**.

| File | Level 1 | Level 2 | Level 3 | Note |
|---|---|---|---|---|
| `internal/cli/init_stages.go`, `init.go`, `init_credentials.go` | **DON1** | **DON8** (`init.go` only, `:4271`) | — | the set path and the stage first, the record write second |
| `internal/infra/tfvars.go` | **DON1** | — | — | `CarriedCredentialsService`, `Credentials` |
| `internal/infra/spec.go`, `spec_test.go` | — | **DON8** | — | the switch's predicate and its two decision points (`:1177`, `:1314`) |
| `internal/localcfg/localcfg.go` | — | **DON8** | — | the per-repository readiness record |
| `internal/cli/run.go` (`credentialsServiceLaunchEnv`, `:3553`, `:3647`) | — | **DON8** | **DON4** | flip 3 first, the flag strings second |
| `internal/cli/update_switch_test.go` (new) | — | **DON8** | — | the switch's tests, new so that `init_converge_test.go` stays DON2's |
| `deploy/terraform/gcp/roots/installation/variables.tf` | **DON1** | — | — | the variable's doc comment |
| `deploy/terraform/gcp/modules/installation/credentials.tf` | **DON1** | **DON3** | — | the verifier rendering first, the `scaling` block second |
| `internal/cli/init_credentials_test.go`, `setup_refusals_test.go` | **DON1** | — | — | — |
| `internal/cli/doctor_brokering.go`, `doctor_brokering_test.go` | **DON7** | — | — | the one line |
| `internal/cli/init_converge_test.go`, `update_managed_test.go` | — | **DON2** | — | the convergence pair |
| `internal/infra/tf/testdata/golden/installation-credentials.plan.json` | — | **DON3** | — | regenerated |
| `deploy/terraform/terraform_test.go` | — | **DON3** | — | the static pin |
| `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` | — | **DON3** | — | the credentials image run |
| `internal/cli/upgrade_cloud.go`, `internal/cli/managed_apply.go`, `managed_apply_test.go` | — | **DON6** | — | the managed case |
| `internal/cli/doctor.go`, `doctor_test.go`, `validate.go`, `validate_test.go`, `review.go`, `review_test.go`, `creds.go`, `cascade_test.go`, `managed_test.go`(the pin at `:941`), `doctor_secret_readers_test.go`, `run_credentials_env_test.go`, `init_coordinator_test.go`(`:113`), `internal/config/providers.go`(`:542`) | — | — | **DON4** | the code-side pins, including the two the review added |
| `internal/infra/tf/managed_cost.go`, `managed_cost_test.go` | — | — | — | **nobody's** — §3 DON3's amendment says why: the managed gate never sees this service's plan |
| `docs/credentials-service/**` | — | — | **DON5** | this directory's own documents |
| `docs/releases/vX.Y.Z.md`, `website/data/release-summaries.yaml` | — | — | **DON5** | the release |
| every other document DON4 moves (§3's table) | — | — | **DON4** | — |

**Four files cross levels, and the levels are what settle each of them.** A file is one writer's at a
time; it may be two tasks' **across** levels and never within one. So:

- `internal/cli/init.go` is **DON1's in level 1** (the set path and the flag's gating sites) and
  **DON8's in level 2** (the readiness record written at `:4271`, beside `Vertex`). **This is the one
  file whose second writer is the reason DON8 sits in level 2 rather than beside DON1**: the record
  write is a hunk in a file DON1 rewrites, so it waits for DON1 rather than racing it.
- `internal/cli/run.go` is **DON8's in level 2** (`credentialsServiceLaunchEnv`, `:3553` and `:3647`)
  and **DON4's in level 3** (the `--provision-credentials` strings).
- `internal/cli/upgrade_cloud.go` and `internal/cli/managed_test.go` are **DON6's in level 2** (the
  managed convergence path and its test) and **DON4's in level 3** (the `--provision-credentials` strings
  at `upgrade_cloud.go:407` and the pin at `managed_test.go:941`, which name a stage DON1 removes).
- `internal/cli/doctor_brokering_test.go` is **DON7's in level 1** (the one line and its five states) and
  **DON4's in level 3** (the `Fix` strings at `:335`, `:1218`, `:1373`).

**A level with two writers of one file is the thing this table exists to make impossible**, and the
first draft of this section had exactly that: DON2 and DON6 both wanted
`internal/cli/update_managed_test.go`. The task that holds it is **DON2** — the file's `--yes
--provision-credentials` invocations are the convergence test's own fixture, so the task that owns the
convergence owns them — and DON6 takes `managed_apply_test.go` instead. Naming the collision is the
point: a shared-files table that has never caught one is a table nobody checked.

- **Level 1: DON1, DON7.** Neither depends on the other, and their file sets are disjoint — DON1 holds
  the init path and the two installation roots, DON7 holds `doctor_brokering.go` alone. **They are
  parallel and must not be serialised on the strength of this drawing.**
- **Level 2: DON2, DON3, DON6, DON8.** Each depends on DON1 and on nothing in level 1's other half.
  DON2 tests what DON1 writes; DON3 takes `credentials.tf` after DON1 released it; DON6 reads the
  managed path DON1 changed; DON8 writes the record into `init.go` after DON1 rewrote it. **DON3 → DON4**
  is not an edge of the code but of the wording: DON4 moves the strings that describe the guard DON3
  writes. **DON8's edge to DON1 runs the other way in the release and not in the build**, and that is
  the one thing this drawing cannot show: DON8 is *built* after DON1 and DON1 may not *ship* without
  DON8, because §1.4 verifies that DON1 alone drops the mounts of a repository the service cannot serve.
  **A level is a build order; a release is not**, and a reader who takes the two for the same thing
  would ship the field-breaking release this file exists to prevent.
- **Level 3: DON4, DON5.** Both are the same change seen in two places, and they are parallel because
  their files are disjoint — DON4 takes the pins outside this directory, DON5 this directory and the
  release notes. **Their wording must be identical**, which is a coordination cost and not a shared
  file: the sentence DON4 requires in `SECURITY.md` is the sentence DON5 writes in the release notes.

### 4.2 The critical path

```
[§5 item 1 — ANSWERED 2026-11-05, deploy now, switch later]
                                     │
[the verifier account, named — §4.3] ┘──▶  DON1 ──┬──▶ DON3 ──▶ DON4 ──▶ (the release)
                                                 ├──▶ DON2
                                                 ├──▶ DON6
                                                 └──▶ DON8  ══▶ (the release: DON1 may not ship without it)
                                     DON7 ‖ DON1            DON5 ‖ DON4
```

`the verifier account → DON1 → DON3 → DON4 → the release`, with **DON8 a second tail of DON1 that
returns to the release rather than hanging off it**: the double line is the release edge, not a build
order, and it is the one edge in this drawing that a level table cannot express. **The owner's answer is
no longer on the chain** — it was the first link while the item was open, and answering it turned it
into the shape of the chain (two tails where there was one) rather than a link in it. **Two links are
still not code:** the verifier account's name, which is another work stream's Open item (§4.3), and an
owner at a terminal for the one step §2.1 keeps. The chain is five code links and the last one is a
person's, which is the same shape the cloud senior review's own §6.3 reaches.

### 4.3 What must precede what — the verifier account, and CSR10 / CSR12 / CSR13

**The verifier account must exist, and be named, before DON1 lands.** This is the one ordering
constraint that is not negotiable, and it is the reason the brief asks for it in as many words.
`credentials_service.verifier_accounts` is what admits a caller to the verify route; the account is a
**dedicated verifier service account**, never a person (§2.2, CSR Open item 5, and the class refusal at
`internal/credsvc/auth.go:574` that makes a person's address fatal to the service's own start-up). If
DON1 lands first, **every installation deploys with an empty verifier list**, and the consequence is not
a quiet one: CSR10's launcher reader fails closed, so **every fix round refuses every brief** on every
installation. The order is therefore:

1. **the verifier-account Terraform task** (the cloud senior review's own, referenced and not written
   here) — creates the account, names it in `credentials_service.verifier_accounts`, and publishes the
   impersonation grant;
2. **DON1**, which renders that name for every installation;
3. everything else in §4.1.

**And the three CSR tasks this ruling moves, each referenced and none edited here:**

- **CSR10** (the launcher verifies the verdict before it uses it, `docs/cloud-senior-review/
  implementation-plan.md:943`) is **unblocked in one half and unchanged in the other**. Its `Depends on`
  names "a caller the installation lists" as a precondition that today holds on nobody; after the
  verifier-account task and DON1, the list is populated on **every** installation, so CSR10's live
  verification stops being conditional on an installation having opted in. Its reader, its fail-closed
  behaviour and its tests are untouched — **an empty list must still refuse**, because an installation
  whose verifier account is not yet created is a real state, and CSR10's reader is the thing that must
  not paper over it.
- **CSR12** (the live-check switch, `:1171`) is **changed in its premise, and the change is a docs one**.
  Its switch exists because E7 — "the service is enabled for the installation" — was a gate a person had
  to open. After DON1 that gate is always open, so the *first* of E7's two halves stops being a refusal
  any installation can meet, and the refusal sentence CSR2 writes ("half one is `fugaro update
  --provision-credentials` plus `fugaro creds set github-app-key`", `:243`) names a command DON1
  **removes**. **So CSR2's refusal sentence and E7's row must move in the same release as DON1** — that
  is a change under `docs/cloud-senior-review/` and to `internal/cli/review.go`'s refusal, both of which
  are **another run's** and are listed here rather than made here. CSR12's switch itself still has its
  job: it is what lets a launch start against a service that **admits nobody**, which is exactly the
  state every installation is in until the verifier account and the job accounts are published.
- **CSR13** (the unwritten fourth runner task, `:1449`) is **smaller by one half**. Its cost is "another
  SEC task … over `internal/runner/gateway.go`, `internal/runner/routing.go` and
  `internal/runner/agentenv.go`", and one of the things it would have had to arrange — reaching a
  service that may not exist — is gone: after DON1 the service exists on every installation, so CSR13's
  work is the class-keyed credential and nothing else. **It is not a precondition of DON1 and DON1 is
  not a precondition of it.**

**And one ordering DON8 adds, which no CSR task owns:** **the switch and the App-key flip are
independent, and neither waits on the other.** C2.3's drop of the App key's own accessor is held behind
the owner's live Check 96 (§6 V8) and `config.ServiceHeldSecretNames` is empty until it
(`internal/config/providers.go:542`, #907) — while DON8's switch works without it, because the runner
already mints through the service (`internal/runner/runner.go:1119`). **So Check 96 does not gate the
switch and the switch does not gate Check 96**, which is the opposite of what §5 item 1's reading (b)
would have required (its cost (iv): under (b) Check 96 stops being a later confirmation and becomes a
precondition). **The owner's answer removed that dependency**, and it is worth stating plainly because
it is the answer's one unambiguous gain.

**What this ruling does *not* change in the CSR plan:** CSR1–CSR9, CSR11's flip and the live checks
themselves. The live checks are still the owner's and still the last link.

---

## 5. Open items — the owner's, and none of them decided here

**Item 1 was BLOCKING and is ANSWERED; the other five are not blocking and are still open.** It decided
how the jobs switch to the service, and **every task in §3 was held behind it** — that hold is lifted.
Items 2–6 are the ones the ruling itself leaves open, and each can be answered independently.

**1. ANSWERED — 2026-11-05: DEPLOY NOW, SWITCH LATER.** The owner took reading **(a)**: the service is
deployed for every installation by `fugaro update`, and **the repository roots' `credentials_service`
input and the run's `FUGARO_CREDENTIALS_SERVICE_URL` stay off until the bundle holds what that job's
auth mode needs**, so an installation that updates keeps every run working with `update` alone. **The
one-line why:** (a) is the reading under which the deploy is the cheap change the ruling was taken for
and no run changes at all, and its one real cost — "something has to turn the switch on" — turned out to
be answerable without the second command (b) was chosen to avoid: **the something is `fugaro update`
itself, reading the bundle through the loader the doctor already reads** (§3 DON8, and §1.5 states what
it flips). **The recommendation this file carried was (b)**, and the owner did not take it; the reasons
(a) won on are the two the answer names, and the cost (b) would have paid — a `fugaro update` that
refuses a coding agent's session (cost (iii) below) — is one the owner declined. **What the answer
leaves the plan with:** §3's eight tasks, DON8 new and a release blocker for DON1, and §4.2's chain
starting at the verifier account rather than here.

**The item as it was put to the owner, kept as the record of what was weighed.** §1.4 verifies the fact
this item existed for: **the deploy and the switch are two changes, and the switch, as the tree stands,
breaks every installation's runs.** Writing the installation root's
`credentials_service` block triggers `dropModelMounts` on every repository root, the next time each of
them is applied (`internal/infra/spec.go:1177-1178`, `:1314`), the drop is **auth-blind** (`sa.tf:148-151`, and
`SECURITY.md` states it as a limit of the release), and a run that hands its job
`FUGARO_CREDENTIALS_SERVICE_URL` opens the GitHub provider **keyless with no fallback**
(`internal/gitprov/providers/providers.go:93-99`). So an `oauth` or `api-key` job of a converged
installation declares no model credential while the bundle holds nothing that replaces it. **The owner's
intent is not in question — the service is deployed for every installation. What this item decides is
how the second change happens, and the two readings are:**

- **(a) The service is deployed everywhere, and the switch stays off.** The installation root's block
  lands (DON1); the repository roots' read-back (`repoCredentialsService`, `dropModelMounts`) and the
  run's URL (`credentialsServiceLaunchEnv`) **stay off** until the bundle holds what that job's auth
  mode needs. The jobs keep their mounts and every run is unchanged; the service is deployed, serving
  and admitting nobody (§2.3) on every installation.
  **Cost, and it is two costs.** *(i)* **The opt-in comes back as a readiness gate.** The ruling existed
  to remove "per-installation choice"; (a) replaces it with a per-installation *readiness* condition,
  and an installation whose owner never writes the bundle keeps the old path indefinitely — so the
  service is on every installation and used by none of them. *(ii)* **Something has to turn the switch
  on, and the HARD RULE forbids that something being a second command.** If the flip is another
  `fugaro update`, the release's `### For operators` section has a second step in it after all and
  DON5 cannot write what this file promised; if the flip is a **readiness read taken at convergence
  time** — the convergence path asking Secret Manager what the bundle holds, and setting the read-back
  from the answer — then `fugaro update` (and `fugaro init` for a repository root) gains a cloud read
  and a new decision in a path that today derives everything from the local config and the policy. **The
  second is the reading that keeps the HARD RULE; it is also the one that needs a design, and this file
  does not write it.**
- **(b) The credential drop becomes auth-aware, and `fugaro update` migrates the existing secrets in the
  owner's terminal.** The switch lands **with** the deploy: a job's mounts depend on its own
  `agent.auth`, and the bundle is filled from what the installation already holds, so the first `update`
  is also the last step.
  **Cost, and it is four, and three of them are new code.** *(i)* **`is_service_held` becomes
  per-workflow**, and the Go mirror must move with it in one commit: `sa.tf:135-137`'s expression and
  `internal/infra/spec.go`'s `dropModelMounts` (`:1177-1178`) and `mountsServiceHeldSecret` (`:1720`)
  are already held to each other by `TestServiceHeldSecretsMatchTheCredentialsService`, so the pair
  moves together or the guard fires. *(ii)* **`update` becomes a bundle writer**, which is a class of
  code the CLI does not have: it reads the project's existing secrets — the model credential, and the
  App key, which is **already a project-wide secret** (`gcp.ProjectSecretID(lc.Name,
  config.GitHubAppKeySecret)`, `internal/cli/project_enroll.go:918`, which is what makes this reading
  feasible rather than a prompt) — and writes them into the one bundle secret, so the CLI holds raw
  credential values on a path that today never does. Terraform cannot do it: no credential value may
  reach Terraform or its state (`TestNoSecretVersionsInTerraform`), so the migration is the CLI's or
  nobody's. *(iii)* **`update` must carry `creds set`'s agent refusal for that half**
  (`internal/cli/creds.go:118-119`, `agentMarker`/`initflow.AgentRefusal`), or `update` becomes the
  agent-runnable route to writing every credential in the bundle that the refusal exists to close. That
  is a change to the one command the Fugaro skill drives and the HARD RULE names. *(iv)* **The App key's
  own drop is gated behind Check 96.** `config.ServiceHeldSecretNames` is empty today (#907) and the
  README's status paragraph says the flip waits on the owner's live Check 96 proving the deployed
  service mints; under (b) that flip is part of this change, so **Check 96 stops being a later
  confirmation and becomes a precondition.**

**The recommendation this file carried, kept because the answer went the other way:** **(b)**, because
it is the reading that keeps the HARD RULE's "one command" true and because the migration is genuinely
available — the App key is already a project secret and the model credentials already are — while (a)
buys the same service on every installation and leaves it unused, with the flip postponed to a design
that did not exist yet. **The owner answered (a) on 2026-11-05, and the design that did not exist yet is
§3 DON8** — so the recommendation's first objection is met (there is no second command after all) and
its second is not (the service is still unused on an installation whose owner never writes the bundle,
which §2.3's "deployed, serving, admitting nobody" is the honest name for). **The cost of (b) the owner
declined is (iii):** a `fugaro update` that refuses a coding agent's session is a different command from
the one the plugin runs today. **Answered, and the hold on §3 is lifted.**

**2. An installation with no GitHub App at all.** A Bitbucket-only installation, or one whose
repositories use a different provider, has no App key to write and never will. §3 DON5 writes the
operator step as **conditional** for exactly this reason, and §2.3's table says the service works
without a key. **What is not decided here:** whether such an installation should be *told* anything at
all beyond the one conditional sentence — whether `doctor`'s line (DON7) should stay silent about a
missing App key on an installation with no GitHub repository, or name it. The recommendation is to stay
silent, because a warning an operator cannot act on is noise, and the read is one the doctor already
makes (`infra.CredentialsJobAccounts` knows the providers). **The owner's.**

**3. Whether the service should deploy before a key exists.** The ruling says the service deploys for
every installation; it does not say whether the **first** deploy should wait for the bundle to hold the
App key. The tree's own answer is that it should not — the service is designed to start, serve and admit
nobody (§2.3), and the loader fails closed — and DON1 follows it. **But it is a decision the owner made
implicitly**, and this item records it so it is not discovered later: the alternative (deploy the
service only once `creds set github-app-key` has run) would make the service's existence depend on a
credential, which is the shape the ruling was taken to remove. **The owner's, and the recommendation is
to leave the tree's answer standing.**

**4. The cost wording on the website.** §1.3 states the idle cost **accurately rather than rounded**: the
service is free while idle *once the minimum instance count is pinned*, plus **one active Secret Manager
version** and **one cached container image**, whose published prices are **not facts this tree holds**.
`website/content/security.md` and `website/content/subscription-terms.md` are public statements a
customer reads, and "costs nothing idle" is a claim a customer may hold the project to. **What is not
decided here:** whether the website should say "costs nothing while idle" (the owner's own framing,
true of the compute) or the narrower true sentence (free while idle, plus one secret version and one
cached image, with the numbers). The recommendation is the narrower one on the **pricing** pages and the
owner's framing in the **design** documents, because a design document states an intent and a pricing
page states a bill. **The owner's.**

**5. The `max_instance_count` value.** §3 DON3 proposes `4` and gives its reasoning; the value is a
cost-and-capacity decision rather than a code fact, and the owner may want a different number (or an
explicit statement that the service is deliberately unbounded above, which would be the first unbounded
always-on resource this installation deploys). **The owner's.**

**6. Whether DON1's removal of `--provision-credentials` is the right call.** §3 DON1 decides it
**removed** rather than kept as a no-op alias, and gives its reason. The decision is cheap to reverse —
the flag is one `BoolVar` and one gate — and it is listed here because it is a **surface a user types**,
which is the class of decision this file does not make for the owner. **The owner's.**

---

## 6. UNVERIFIED

| # | Claim | Why it matters | How it gets settled |
|---|---|---|---|
| V1 | Secret Manager's price per **active secret version** per month | §1.3's "one active version" is the only Secret Manager line this ruling adds, and the number is the whole of it | Google's published price list, read by the owner. **Nothing in this tree states a price**, and this file does not invent one |
| V2 | Artifact Registry's price per GB per month of storage, and its free allowance | §1.3's cached-image line | the same |
| V3 | Cloud Run's price per request and per CPU/allocation-second, and that a zero-minimum service has **no** idle charge | The owner's own reason for the ruling ("a Cloud Run service that scales to zero costs nothing idle"). **The scale-to-zero behaviour is not in this tree** — what is in this tree is the MCP front door's `min_instance_count = 0` and a comment saying why (`coordinator.tf:437`) | Cloud Run's published documentation and price list, read by the owner; and, once DON3 lands, **Check 94 read back on a real installation** |
| V4 | That the Cloud Run provider's default for `min_instance_count` really is `0` | §1.2 rests on it: the gap is that the tree states nothing, so the *default* is what decides today | The provider's schema, read by the owner. The tree does not state it, and DON3's whole point is that the tree should not have to |
| V5 | Whether a **managed** project's `fugaro update` applies its own installation root | §3 DON6 cannot be written until this is known, and it is a fact about the managed convergence path rather than about this ruling | Read out of `internal/cli/upgrade_cloud.go` and the managed path **by DON6's own red step**, which is why that task determines rather than assumes it |
| V6 | That the compressed size of the credentials image is materially below its ~60 MB uncompressed size | §1.3's "smaller again" | The image's manifest, read by the owner (`docker buildx imagetools inspect`). The **uncompressed** size is VERIFIED in the tree (`images/credentials_test.go:209-220`) and this row is only about the compressed figure |
| V7 | That no **other** installation-root resource becomes always-on because of this service | §1.3's "nothing else" | The module's own resource list, which is six and is counted by `TestCredentialsServiceResourcesAreCounted`. This row stands until DON1 adds the verifier account's resource, which is a **seventh** — and DON1's own task must say so and move that test's number |
| V8 | That the deployed service really **mints** an installation token from the bundle's App key, so the App key's own accessor can be dropped (C2.3, `config.ServiceHeldSecretNames`, #907) | §1.5's flip 2 and §4.3's "Check 96 does not gate the switch": the switch mints through the service, and the **runner half is wired today** (`internal/runner/runner.go:1119`) — but "wired" is not "proven to mint" | **The owner's live Check 96**, which is why the App key's drop is held behind it and the model-credential drop is not. **This file does not claim the mint works**; it claims the switch does not depend on the answer |
| V9 | That **`fugaro update --repo PATH` is the only door that applies a repository root** — the fact §1.4's "the next time that root is applied" and DON8's "which door applies its root" both rest on | §1.4 says the drop lands on the repository root's next apply and not on DON1's own convergence; if some other path applied repository roots, the failure would arrive sooner than this file says | Read out of `internal/cli/init.go:344`'s door switch (`case o.repo: runInitRepo`) and by the absence of any `infra.Repo` call in `internal/cli/run.go` and in `internal/runner`. **Read, not proven exhaustive** — a third caller this amendment did not find would move the timing and not the conclusion |
| V10 | That `fugaro update`'s convergence can read the bundle **in the stage that writes the local config**, so DON8's record write needs no new cloud client on that path | DON8's design is that the readiness read is the one `doctorBundleChecks` already makes (`internal/cli/doctor_brokering.go:432`). Whether that read is available at the moment the record is written is a fact about the stage order, and **DON8's own implementation determines it** — the fallback is a separate read in the stage, which changes no interface | Read out of `internal/cli/init.go`'s stage list by DON8's own red step, which is why that task determines rather than assumes it |
