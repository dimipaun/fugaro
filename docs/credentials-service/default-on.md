# The credentials service deploys for every installation — the owner's ruling of 2026-11-05

**Status (2026-11-06): the ruling is the owner's; the tasks below are PLAN ONLY and none of them is
approved.** This file is a **dated amendment** to [implementation-plan.md](implementation-plan.md),
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
| **Does not touch** | Q6, D143, D145, D146, D147, Global Constraints 6 and 12, Checks 90–99, D130–D159, and every task in [implementation-plan.md](implementation-plan.md) |

---

## 1. The ruling, and what it costs — verified against this tree

### 1.1 The ruling

The credentials service is deployed by `fugaro update` **for every installation**, with no flag, no
typed extra confirmation of its own and no per-installation choice. The installation root's
`credentials_service` variable stops being the thing an owner opts into and becomes part of the state
`update` converges every installation on — which is the shape the **HARD RULE** in `CLAUDE.md` already
requires of everything else `update` does: one command, no second step, no manual repair.

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

### 1.3 What actually costs something while idle — the six resources, and the rest

The service's Terraform is **six resources**, all gated on the one local `credentials_service_on`
(`deploy/terraform/gcp/modules/installation/credentials.tf:22`, counted by
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
§5's UNVERIFIED list carries it as UNVERIFIED.**

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
  its free allowance. §5 carries it.

**Nothing else in the installation root becomes always-on because of this service.** The invoker grants
(`deploy/terraform/gcp/modules/installation/iam.tf:163`, `:194`) are
`google_cloud_run_v2_service_iam_member` — IAM bindings, not billed resources. The repository-side half
(`deploy/terraform/gcp/modules/workflow/sa.tf:211`) is the same. **So the honest statement of the
owner's cost claim is:** *the service itself is free while idle once its minimum instance count is
pinned at zero; what it adds to the bill is one active Secret Manager version once a bundle is written,
and one cached container image; and today the first of those rests on an unpinned provider default that
DON3 fixes.*

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
| the run/read mint | an empty job-account table, or a caller that is not one of them | `FUGARO_JOB_ACCOUNTS`; Global Constraint 6 |
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

Seven tasks, **DON1–DON7**, matching §1 and the owner's brief one for one. They follow
[../cloud-senior-review/implementation-plan.md](../cloud-senior-review/implementation-plan.md) §3's
convention exactly: decisions and findings named, an exact file list with its cap, the interfaces
produced, named tests, a red step, the mutation that holds it, a reviewer tier, whether it is OWNER-RUN,
its dependencies and its commit message. **No task is approved.**

**The numbering is deliberately not `C4.x`.** `C0`–`C3` are [implementation-plan.md](implementation-plan.md)'s
slices and `CSR1`–`CSR13` are the cloud senior review's; a third stream taking `C4` would collide with
the C-numbers of four other plans in this repository (`docs/design/budget-token-channel.md`,
`docs/forge-authorship-images/`, `docs/project-image-catalog/`, `docs/console-coordinator-v2/`). `DON` is
this file's own prefix and collides with nothing.

### DON1. `fugaro update` sets `credentials_service` for every installation — SECURITY-SENSITIVE

**Decisions:** the ruling of 2026-11-05 (this file). **Reverses:** D147's "off by default" half only —
D146's placement and D145's invariant are untouched. **Verification:** `internal/cli`, `internal/infra`.

- **Files:** `internal/cli/init_stages.go` (the unconditional set path; today `pinCredentialsImage`
  returns early at "`doc == nil` — no `credentials_service` in the installation's tfvars: nothing opted
  in"), `internal/cli/init.go` (the flag at `:109`/`:458` and its two gating sites at `:1301`,
  `:1318-1321`; the `CarriedCredentialsService` call sites at `:736`, `:2090`),
  `internal/infra/tfvars.go` (`CarriedCredentialsService` `:591-612`, `Credentials` `:355`,
  `CredentialsServiceURLOf` `:448`, `SetCredentialsServiceURL` `:467`),
  `internal/cli/init_credentials.go` (the `--provision-credentials` engine stage is **removed**;
  `resolveCredentialsImage` is kept, because `pinCredentialsImage` pins through it),
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
     `--provision-credentials`, no manual repair, and the service's block, image pin and URL all
     present after that one run;
  2. an installation that **already opted in** under an earlier release **does not change** beyond the
     image moving to this release — same account, same container, same grants, and above all the
     **same recorded URL**, because a rebuild that dropped and re-derived it would re-plan the service.
     `CarriedCredentialsService`'s own comment is the reason this direction exists.
- **Tests:** `TestAnInstallationAt019ConvergesWithUpdateAlone` and
  `TestAnAlreadyOptedInInstallationDoesNotChange`, plus `TestConvergenceAsksNoSecondQuestion` (the run's
  prompts are the ones `update` already asks; the credentials stage's own typed confirmation is gone with
  the stage).
- **Red step:** `TestAnInstallationAt019ConvergesWithUpdateAlone` fails against today's tree — the
  fixture converges and the block is absent, because nothing writes it without the flag. It is written
  **before** DON1's change and is DON1's own red step seen from the outside, which is what makes the
  HARD RULE test rather than assert.
- **Mutation to hold it:** make the update path require the flag → the first test fails; make the
  already-opted-in path re-derive the block from the spec rather than carry it → the second fails.
- **Reviewer tier:** **B** (a test-only task over a path DON1 owns; it is A only in the sense that what
  it proves is the HARD RULE).
- **OWNER-RUN:** no.
- **Depends on:** **DON1**.
- **Commit:** `cli: an installation at 0.19.0 converges on the credentials service with update alone`.

### DON3. The service scales to zero, and the cost guard can see it — SECURITY-SENSITIVE

**Decisions:** the ruling of 2026-11-05. **Findings:** §1.2 and §1.3 of this file. **Verification:**
`deploy/terraform`, `internal/infra/tf`.

- **Files:** `deploy/terraform/gcp/modules/installation/credentials.tf` (the `scaling` block),
  `internal/infra/tf/managed_cost.go` + `managed_cost_test.go` (the entry grows a rule it can actually
  fire on, or a test that it fires), `internal/infra/tf/testdata/golden/installation-credentials.plan.json`
  (regenerated — the plan's JSON changes the moment a block is added), `deploy/terraform/terraform_test.go`
  (the static pin), `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` (the
  credentials image run at `:2439` gains the assertion). **5 files.** The golden plan is regenerated by
  `scripts/gen-golden-plans.sh`, never by hand.
- **Interfaces:** `google_cloud_run_v2_service.credentials` states, in a `scaling` block on the
  **template**, `min_instance_count = 0` and a `max_instance_count`. The precedent is the MCP front
  door's own block (`coordinator.tf:437-439`), and the numbers are chosen rather than copied: the
  **minimum is 0** because that is the owner's cost claim made checkable, and the **maximum is bounded**
  because an unbounded service is the other half of a cost the owner did not consent to — a runaway
  caller, or a retry storm, scales this service as far as Cloud Run allows. The plan proposes
  `max_instance_count = 4` (a small number an installation's own run concurrency cannot exceed, and one
  an operator raises deliberately), and the value is the task's to settle with its own reasoning.
- **The guard, and it is the reason this task exists at all.** `managedCostList`'s
  `managedCostMinInstances` entry already blocks a plan that states a minimum above the line — but, as
  §1.2 verifies, it fires only on a **stated** value, so today it cannot see this service. This task
  closes that hole **in the direction that lasts**: the block is pinned *and*
  `managed_cost_test.go` gains the case that the entry fires on a plan whose Cloud Run **service** states
  `min_instance_count = 1`, plus the case that it does **not** fire on the pinned zero. **A guard that is
  only satisfied by a value nobody can change is not a guard; a guard that fires on a raised value is.**
  Whether the entry needs a new rule (it does not appear to — the template-level path at
  `managed_cost.go:195` is exactly the path this block lands on) is the task's to determine, and its
  answer goes in the commit.
- **Tests:** `TestCredentialsServicePinsMinInstanceZero` (the block is on the template, states `0`, and
  states a maximum), `TestCredentialsServicePlanStatesTheMinimum` (the **golden plan**, not the source:
  the plan's `after` carries `template[0].scaling[0].min_instance_count == 0` — this is the test that
  would have caught the gap §1.2 describes), `TestManagedCostFiresOnARaisedServiceMinimum` and
  `TestManagedCostDoesNotFireOnThePinnedCredentialsService`.
- **Red step:** `TestCredentialsServicePlanStatesTheMinimum` fails on today's golden: the plan states no
  `scaling` key at all. **SEC flag:** it is a cost guard, and the second test is the one that matters
  most — the guard must still be able to fire.
- **Mutation to hold it:** raise `min_instance_count` to `1` → the second test fails and the managed gate
  refuses; delete the block → the second test fails again, because the plan states nothing; drop the
  `max_instance_count` → the first fails.
- **Reviewer tier:** **A** (it is the check that keeps the owner's cost claim true).
- **OWNER-RUN:** no.
- **Depends on:** **DON1** — both hold `credentials.tf`, and DON1 has it in level 1.
- **Commit:** `terraform: pin the credentials service at zero minimum instances, and make the cost guard see it`.

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
  `internal/infra/managed/marker_test.go` (`:1829`), `internal/localcfg/shared_test.go` (`:493`).
  **At most 26 files**, and the cap is the list, not a licence to widen it.
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
  | `docs/run-without-checkout/{spec,reconciliation}.md` | L80-81; L145, L257, L296 |
  | `docs/stateless-orchestrator/reconciliation.md` | L32 |
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
  `website/`, `SECURITY.md`, `CLAUDE.md`, `.claude/rules/` and the release notes, with the same
  false-positive discipline `internal/cli/docs_live_init_census_test.go` uses for the roots it exempts:
  the coordinator's and the accounts' own "dormant" uses are **not** this service's and are named in the
  test as excluded) and `TestTheCredentialsServiceIsDeployedForEveryInstallation` (the sentence this
  file's ruling turns into, required in `SECURITY.md` and in the website mirror).
- **Red step:** `TestNoDocumentCallsTheCredentialsServiceOptional` fails on today's tree with the full
  list above. It is written first and its failure is the task's work list.
- **Mutation to hold it:** restore one retired sentence in any of the documents → the first test fails
  and names the file; delete the new required sentence from `SECURITY.md` → the second fails.
- **Reviewer tier:** **B** (a docs-and-pins task, with the SECURITY.md half reviewed as a security
  claim — `SECURITY.md`'s statements about what a job holds are claims, and D148 is why they move
  deliberately).
- **OWNER-RUN:** no.
- **Depends on:** **DON1** (the flag's removal changes the strings this task moves) and **DON3** (the
  guard's wording).
- **Commit:** `docs: the credentials service is deployed for every installation, not opt-in`.

### DON5. The release notes, and the one prompt that remains

**Decisions:** the ruling of 2026-11-05; `CLAUDE.md`'s HARD RULE;
[docs/releases/README.md](../releases/README.md)'s `### For operators` rule. **Verification:**
`internal/cli` (`-run Docs`), `website`.

- **Files:** `docs/releases/vX.Y.Z.md` (the release this lands in),
  `website/data/release-summaries.yaml` (its one line, no backticks),
  `docs/credentials-service/{README,implementation-plan,runbook,reconciliation}.md`. **6 files.**
- **Interfaces:** the release's `### For operators` section reads, in full and in this shape:

  > ### For operators
  >
  > 1. Run `fugaro update` once per installation. It deploys the credentials service on every
  >    installation now, whether or not you opted in before; there is no second command and nothing to
  >    uninstall.
  > 2. Where the bundle holds no GitHub App key, `fugaro update` will say so and the one step left is
  >    yours: `fugaro creds set github-app-key`, at your own terminal. A run that needs an installation
  >    token is refused until then, and every other part of the service works without it.

  **Step 2 is conditional and must be written as conditional**: an installation with no GitHub App, and
  a Bitbucket-only one, has no key to write and nothing to do. This is the same shape the current
  release notes use for a conditional operator step (`docs/releases/v0.10.1.md:14`), and it is the only
  operator step this ruling adds — **there is no step to opt in and no step to uninstall.**
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
- **Mutation to hold it:** write the operator section without the conditional clause → the reviewer sees
  a Bitbucket-only installation told to run a command it cannot run.
- **Reviewer tier:** **B**.
- **OWNER-RUN:** no.
- **Depends on:** **DON1** (the flag's removal is what step 1's wording reports) and **DON4** (the
  wording must be the same in both).
- **Commit:** `docs: the release notes for a credentials service that deploys itself`.

### DON6. The managed project: how an installation with no owner at a terminal reaches it

**Decisions:** the ruling of 2026-11-05; Global Constraint 7 (the managed path never deploys the
credentials service). **Verification:** `internal/cli`, `internal/managed`.

- **Files:** `internal/cli/upgrade_cloud.go`, `internal/cli/managed*.go` (the managed convergence path),
  `internal/cli/managed_test.go`, `internal/cli/update_managed_test.go`,
  `internal/cli/docs_managed_test.go` (if the file exists on this tree — the task says which it chose).
  **At most 5 files.**
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

- **Files:** `internal/cli/doctor_brokering.go`, `internal/cli/doctor_brokering_test.go`. **2 files.**
  **`internal/cli/doctor.go` is deliberately NOT this task's** — its two flag-naming strings (`:693`,
  `:696`) are DON4's, so that no level holds two writers of one file (§4).
- **Interfaces:** `doctorCredentialsServiceDeployed` (`internal/cli/doctor_brokering.go:102`) stops
  returning `nil` for an installation with no `credentials_service` block — that early return
  (`if lc.CredentialsService == nil { return nil }`) is exactly the sentence the ruling makes false: an
  installation that has not converged is now a state worth reporting rather than one worth staying
  silent about. It produces **one line**, in the shape the other checks already use (an `ID`, a
  `Severity`, a `Problem`, a `Fix`), that says the service is **deployed and serving and admitting
  nobody**, and names **which** half is missing:

  | state | the line |
  |---|---|
  | no block in the local config | the installation has not converged on the service; `fugaro update` deploys it |
  | block present, Cloud Run answers `NOT_FOUND` | the block is written and the service is not deployed; `fugaro update` re-applies the root |
  | deployed, the bundle holds no version | deployed and admitting nobody: the bundle is empty, and `fugaro creds set NAME` is the writer |
  | deployed, no `verifier_accounts` | deployed and admitting nobody on the verify route: the verifier account is not named |
  | deployed, a job account missing `roles/run.invoker` | the existing half of the check, unchanged |

  The severity stays **warning**, for the reason the check's own comment gives (an installation mid-
  convergence is a legitimate state) and one more this ruling adds: **an installation that has not run
  `update` yet is not broken**, it is behind. `--strict` is where that becomes a failure, as it already
  is.
- **Tests:** `TestDoctorReportsTheServiceStateInOneLine` (the five states, each producing exactly one
  check with the expected `ID` and severity), `TestDoctorNamesTheMissingHalfNotNotConfigured` (the line
  never reads "not configured": it names the App key, the verifier account, the bundle or the invoker
  grant), and the existing `doctor_brokering_test.go` cases at `:335`, `:1218`, `:1373` move with the
  `Fix` strings DON1's flag removal changes.
- **Red step:** `TestDoctorReportsTheServiceStateInOneLine` fails on today's tree: a config with no
  block produces **no check at all**.
- **Mutation to hold it:** put the `nil` early return back → the first test fails; collapse the five
  states into one "not configured" line → the second fails.
- **Reviewer tier:** **B**.
- **OWNER-RUN:** no.
- **Depends on:** **DON1**.
- **Commit:** `doctor: one line for the credentials service's state, deployed and admitting nobody`.

---

## 4. Levels, shared files, the critical path, and what must precede what

### 4.1 Levels — no level holds two writers of one file

The rule is [../cloud-senior-review/implementation-plan.md](../cloud-senior-review/implementation-plan.md)
§2's: a file is one writer's at a time, and **within a level no two tasks may hold one**.

| File | Level 1 | Level 2 | Level 3 | Note |
|---|---|---|---|---|
| `internal/cli/init_stages.go`, `init.go`, `init_credentials.go` | **DON1** | — | — | the set path, the flag's removal, the stage's removal |
| `internal/infra/tfvars.go` | **DON1** | — | — | `CarriedCredentialsService`, `Credentials` |
| `deploy/terraform/gcp/roots/installation/variables.tf` | **DON1** | — | — | the variable's doc comment |
| `deploy/terraform/gcp/modules/installation/credentials.tf` | **DON1** | **DON3** | — | the verifier rendering first, the `scaling` block second |
| `internal/cli/init_credentials_test.go`, `setup_refusals_test.go` | **DON1** | — | — | — |
| `internal/cli/doctor_brokering.go`, `doctor_brokering_test.go` | **DON7** | — | — | the one line |
| `internal/cli/init_converge_test.go`, `update_managed_test.go` | — | **DON2** | — | the convergence pair |
| `internal/infra/tf/managed_cost.go`, `managed_cost_test.go` | — | **DON3** | — | the guard |
| `internal/infra/tf/testdata/golden/installation-credentials.plan.json` | — | **DON3** | — | regenerated |
| `deploy/terraform/terraform_test.go` | — | **DON3** | — | the static pin |
| `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` | — | **DON3** | — | the credentials image run |
| `internal/cli/upgrade_cloud.go`, `managed*.go`, `managed_test.go` | — | **DON6** | — | the managed case |
| `internal/cli/doctor.go`, `doctor_test.go`, `validate.go`, `validate_test.go`, `review.go`, `review_test.go`, `run.go`, `creds.go`, `cascade_test.go`, `managed_test.go`(the pin at `:941`), `doctor_secret_readers_test.go`, `run_credentials_env_test.go` | — | — | **DON4** | the code-side pins |
| `docs/credentials-service/**` | — | — | **DON5** | this directory's own documents |
| `docs/releases/vX.Y.Z.md`, `website/data/release-summaries.yaml` | — | — | **DON5** | the release |
| every other document DON4 moves (§3's table) | — | — | **DON4** | — |

**`internal/cli/managed_test.go` is the one file two tasks want, and the level is what settles it.**
DON6 takes it in level 2 (the managed convergence test), and DON4 takes it in level 3 (the pin at
`:941`, which names the credentials stage DON1 removes). Serialising them is the point of the levels;
a level with two writers of one file is the thing this table exists to make impossible.

- **Level 1: DON1, DON7.** Neither depends on the other, and their file sets are disjoint — DON1 holds
  the init path and the two installation roots, DON7 holds `doctor_brokering.go` alone. **They are
  parallel and must not be serialised on the strength of this drawing.**
- **Level 2: DON2, DON3, DON6.** Each depends on DON1 and on nothing in level 1's other half. DON2
  tests what DON1 writes; DON3 takes `credentials.tf` after DON1 released it; DON6 reads the managed
  path DON1 changed. **DON3 → DON4** is not an edge of the code but of the wording: DON4 moves the
  strings that describe the guard DON3 writes.
- **Level 3: DON4, DON5.** Both are the same change seen in two places, and they are parallel because
  their files are disjoint — DON4 takes the pins outside this directory, DON5 this directory and the
  release notes. **Their wording must be identical**, which is a coordination cost and not a shared
  file: the sentence DON4 requires in `SECURITY.md` is the sentence DON5 writes in the release notes.

### 4.2 The critical path

```
[the verifier account, settled and named — §4.3]  ──▶  DON1  ──┬──▶ DON3 ──▶ DON4 ──▶ (the release)
                                                              ├──▶ DON2
                                                              └──▶ DON6
                          DON7 ‖ DON1                          DON5 ‖ DON4
```

`the verifier account → DON1 → DON3 → DON4 → the release`, and **the longest wait in it is not code**:
it is the verifier account's name, which is another work stream's Open item (§4.3), plus an owner at a
terminal for the one step §2.1 keeps. The chain is five links and the last one is a person's, which is
the same honest shape the cloud senior review's own §6.3 reaches.

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

**What this ruling does *not* change in the CSR plan:** CSR1–CSR9, CSR11's flip and the live checks
themselves. The live checks are still the owner's and still the last link.

---

## 5. Open items — the owner's, and none of them decided here

**1. An installation with no GitHub App at all.** A Bitbucket-only installation, or one whose
repositories use a different provider, has no App key to write and never will. §3 DON5 writes the
operator step as **conditional** for exactly this reason, and §2.3's table says the service works
without a key. **What is not decided here:** whether such an installation should be *told* anything at
all beyond the one conditional sentence — whether `doctor`'s line (DON7) should stay silent about a
missing App key on an installation with no GitHub repository, or name it. The recommendation is to stay
silent, because a warning an operator cannot act on is noise, and the read is one the doctor already
makes (`infra.CredentialsJobAccounts` knows the providers). **The owner's.**

**2. Whether the service should deploy before a key exists.** The ruling says the service deploys for
every installation; it does not say whether the **first** deploy should wait for the bundle to hold the
App key. The tree's own answer is that it should not — the service is designed to start, serve and admit
nobody (§2.3), and the loader fails closed — and DON1 follows it. **But it is a decision the owner made
implicitly**, and this item records it so it is not discovered later: the alternative (deploy the
service only once `creds set github-app-key` has run) would make the service's existence depend on a
credential, which is the shape the ruling was taken to remove. **The owner's, and the recommendation is
to leave the tree's answer standing.**

**3. The cost wording on the website.** §1.3 states the idle cost **accurately rather than rounded**: the
service is free while idle *once the minimum instance count is pinned*, plus **one active Secret Manager
version** and **one cached container image**, whose published prices are **not facts this tree holds**.
`website/content/security.md` and `website/content/subscription-terms.md` are public statements a
customer reads, and "costs nothing idle" is a claim a customer may hold the project to. **What is not
decided here:** whether the website should say "costs nothing while idle" (the owner's own framing,
true of the compute) or the narrower true sentence (free while idle, plus one secret version and one
cached image, with the numbers). The recommendation is the narrower one on the **pricing** pages and the
owner's framing in the **design** documents, because a design document states an intent and a pricing
page states a bill. **The owner's.**

**4. The `max_instance_count` value.** §3 DON3 proposes `4` and gives its reasoning; the value is a
cost-and-capacity decision rather than a code fact, and the owner may want a different number (or an
explicit statement that the service is deliberately unbounded above, which would be the first unbounded
always-on resource this installation deploys). **The owner's.**

**5. Whether DON1's removal of `--provision-credentials` is the right call.** §3 DON1 decides it
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
