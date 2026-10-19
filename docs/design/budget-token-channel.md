# The budget token's channel

**Status: the decisions are answered (2026-10-15); both halves are built for 0.9.0 — Slice A (the
runner side and the token package, PR #417) and Slice B (the launcher seals, the retry re-seal, the
`tokens/` move with its grants, SECURITY.md's two sentences; the pull request stacked on #417).
Check 60 (owner-run, sandbox only) is written in `docs/gcp-live-checklist.md` and has not run.**
Phase 3b, Task 3b.1 of [the project-centric plan](../plans/2026-10-14-project-centric.md).
SECURITY-SENSITIVE: it is flagged for an independent review, and it decides nothing by itself.
**D14 to D19 are no longer open**: the owner answered all six on 2026-10-15, and each answer is
recorded under the decision it answers, in the owner's own terms. The answers settle *what* to
build, not *whether to start*. This is still an approval-gated design and the gate is real:
no code follows this document until the owner approves it — D14's "build the seal" is the approval
the runner side was built under, and the paragraphs below mark what it does and does not cover —
no Terraform, no config, no live action, no secret handled. Its one red step is the documentation test
beside it,
`internal/cli/docs_budget_token_channel_test.go`, which fails until this file and the
[SECURITY.md](../../SECURITY.md) pointer exist.

This document exists because of D13. Phase 3 puts one Cloud Run job — and one service account —
behind every repository of a profile (design §4.1; plan Task 3.2), which widens that account's
bucket grant from one repository's prefixes to a whole prefix (plan Task 3.2 step 2). Any run of
that job can then read, and delete, any other run's budget token. D13 accepted that exposure and
sent it to SECURITY.md's own words beside D4; phase 3b designs the channel that replaces it. One
half of D13 stands whatever this document decides, and the recommendation below does not touch it:
inside a shared job the profile is the isolation boundary for secrets only. It is not one for bucket
objects — every project job account holds the same whole `runs/`, `cache/` and `locks/` prefixes
(SECURITY.md, bucket-iam.md §10) — so D13's "separate profiles" mitigation is open for the owner to
re-rule.

**Slice A is built (2026-10-15).** The owner's "build the seal" (D14) is the approval the paragraph
above was waiting for, and Slice A is the seal's **runner side**, in the order D19 fixes — runner
and image first: the versioned envelope in `internal/budget/token` (the new `seal.go`:
`Seal`/`Unseal`, the version byte, the object's name and the version byte as additional
authenticated data, `ParseKey`/`EncodeKey`), `PutObject`/`TakeObject` understanding it, every
refusal §7.4 lists, the
runner's strict parse of the marker's value with `"1"` still accepted as the one-bit marker, the key
registered with the runner's redactor before anything can print it, the marker stripped from every
git call's environment — unconditionally, beside the provider keys, and not through the caller's
`Repo.StripEnv` list, which a `Repo` built without it would silently miss — and the `%q` leak gone:
no code path prints the marker's value. That strip is hygiene rather than a boundary, and §7.1 says
why: the run's own agent shares the container and its user with the runner and can read the marker
from the runner's `/proc` whatever git's environment holds. **Slice B follows, and is not in this change:** the launcher seals
and hands its key to the execution it starts, a `--retry` re-mints and re-seals under a new key
(D17), the object moves to `tokens/` with the grant that reads it (D16, Task 3.2), and SECURITY.md's
two sentences are rewritten with the launcher's seal (D15). Until Slice B the launcher still writes
the token plaintext, which is why SECURITY.md's wording is unchanged here: the marker's nature
change arrives with the launcher, not with the runner that is ready for it.

**Slice B is built (2026-10-17), in D19's order.** The launcher draws a 32-byte key per launch
(`token.NewKey`), seals the token under it and hands the key to the one execution it starts as the
marker's value (`LaunchSpec.SecretEnv`); a `--retry` re-mints and re-seals under a new key and
deletes the earlier object first (D17); the launcher's own errors are redacted of the key. The
object moves to `tokens/<slug>/<run>/budget-token` (`token.ObjectPrefix`), `PutObject` has no
plaintext form, and the take is read-only — no delete, no fallback keyed on a 403 (§7.2). A mixed
fleet is gated both ways, closed except for one warned case: the launcher refuses to mint against a
job image whose release base predates `sealSince` (the gate D19 allows), or whose build record is
missing or names no base; on the owner's answer of 2026-10-17 it also refuses a development base
whose record does not say `sealed_budget_token` (the base's own fugaro writes it at render) and was
built before the seal's runner half (`sealRunnerIntroduced`). A development base with neither — no
`sealed_budget_token` and a later or missing `built_at`, which is the job image's build time and
not the base's — cannot be shown either way and **only warns**, so a launch against an old
development base rebuilt today still goes ahead; the exact list is `docs/release.md`'s `sealSince`
line. **What the record shows, and what it does not:** `sealed_budget_token` and `base_ref` are
written by the base image's own fugaro during the image build — they say only that the base image's
own build step recorded them. The launch runs the repository's `:latest`, which no launch compares
with the record's `image_digest`, and a `dockerfile:` workflow's image is the repository's own to
build, its fugaro included. So a hand-pushed `:latest`, or a `dockerfile:` image that ships an
older fugaro, still gets the key echoed into `result.json` (where peer runs of the repository can
read it), whatever its record says. **Unscheduled follow-up, for after 0.9.0 (the round-2
reviewer's suggestion, not decided):** once 0.9.0 is released, refuse a development record that
lacks `sealed_budget_token` instead of warning about it. A runner handed a marker of `1` — a launcher from before the seal — refuses at the take with
the old side named, never reading a plaintext object.
The grants moved with the object (§7.2, §7.5): the job account's `tokens/<slug>/` is
`roles/storage.objectViewer` (`google_storage_bucket_iam_member.job_tokens`, in the workflow
module), and the launchers' conditioned write is widened to `runs/ || tokens/` under the new title
`fugaro-launchers-runs-tokens`, in Terraform, its precondition and tftest lines, and the four Go
files that write and re-check it. **The project module of Task 3.2 does not exist on the base Slice
B was built on**, so the read-only grant is in the per-repository workflow module
(`tokens/<slug>/`); Task 3.2's shared job must carry the same read-only grant on its own prefix
condition, and must not add `tokens` to the job's objectUser prefixes. **Both landed when the seal
was merged into the phase-3 tree** (`integration-0.9.0`, 2026-10-18): the shared job's grant is
`google_storage_bucket_iam_member.job_tokens` in `deploy/terraform/gcp/modules/project/sa.tf`, one
unconditioned clause over this bucket's whole `tokens/` prefix
(`gcp.ProjectTokenBucketCondition`, title `fugaro-project-tokens-<account>`), beside the widened
`runs/`+`cache/`+`locks/` objectUser grant and never inside it. It is read-only and covers the whole
prefix because the job serves every repository admitted to its profile, so a per-repository
`tokens/<slug>/` grant would leave every other repository's runs unable to read their own token;
`fugaro doctor`'s `job-token-grant` check knows both shapes, and the cover, the module's
`precondition` and the installation root's variable type hold the expression to the one clause.

**The Batch backend refuses a sealed launch (decided in Slice B; an open owner question).** Once
the launcher seals, the key it hands the execution would, on Batch, become a plain variable of the
job's definition — Batch has no per-execution override. **The note this replaces named the wrong
reader** (a peer's `gcloud run jobs describe`): on Batch a run's own account holds only
`roles/batch.agentReporter` and `roles/logging.logWriter` (`deploy/terraform/gcp/modules/compute/iam.tf`),
so a peer run cannot describe the job. The real audience is **every launcher**, through
`roles/batch.jobsEditor` conditioned on the Fugaro job-name prefix (same file), which reads any
Fugaro job's variables; **the reaper**, whose custom role carries `batch.jobs.get`, through
`gcloud batch jobs describe`; and possibly the **CreateJob audit record** (unverified, and **no check
reads it** — Check 60 step 3 reads a Batch job's own variables and step 4 the Cloud Run `RunJob`
entry). That is close to the override's audience in kind, but it is a standing copy in
the job resource for as long as the job is kept rather than one request, and it widens to the
reaper's account (compute Task 7, `docs/design/compute-backend.md`). This document
allows no other carrier: a per-run Secret Manager secret read by the VM's account is C2, rejected
above (the shared account reads every peer's secret, and revocation is not fail-closed). So the
launcher hands the key as a *secret* launch variable (`LaunchSpec.SecretEnv`), Cloud Run carries it
in the override, and Batch refuses the launch before creating anything (`ErrRejected`, the value
never quoted). Batch's launcher wiring is not built, so no launch reaches this today. **Open for the
owner:** whether Batch should ever run a budget-on run, and through which carrier — this document
names none that satisfies §3's constraints.

Every symbol below was read on `next` on 2026-10-15 and is cited by name rather than by line
number, because the lines move.

## 1. The channel as it is

Five steps, all of them shipped. This is the **merged tree's** channel: the seal of §7.1 and the
move out of `runs/` of §7.2 are shipped too, so those two sections are this table's account rather
than a proposal for it, and they supersede the pre-seal shape wherever one is named — §2 keeps the
premise the four candidates were priced against (the token object under the `runs/` prefix, a
readable plaintext token, a take that deletes), and it is the premise, not the shipped code.

| step | where |
| --- | --- |
| **Mint** | `internal/cli/launch_budget.go`: `mintBudgetToken` (the decision is `budgetMintOn`) mints `token.Claims{Slug, Run, FX, FP, RB}` with `token.Mint` through `token.IAMSigner` built by `tokenSigner` |
| **Deliver** | `internal/budget/token/object.go`: `PutObject` writes the custom token **sealed** under a key the launcher draws for this launch (`Seal`, §7.1) at `ObjectKey(slug, run)` = `tokens/<slug>/<run>/budget-token` — the name §7.2 moved it to, and the object's own name is the envelope's AAD — create-if-absent (`ErrObjectExists`); the runs bucket's lifecycle rule deletes an untaken one after a day (`deploy/terraform/gcp/modules/installation/bucket.tf`, `google_storage_bucket.runs`); `DeleteObject` removes it after a launch that did not start, or an earlier attempt's object on a retry (`dropBudgetToken`), and both are the launcher's write: the run's own grant on `tokens/` carries no delete (D16) |
| **Take** | `internal/runner/projectpolicy.go`: `takeBudgetToken` → `token.TakeObject` (`internal/budget/token/object.go`), which reads the object, **unseals it under the launch's key** (`Unseal`, which checks the envelope version first and the AAD — the object's name and that version byte — on the open) and **never deletes it**, and refuses one whose own `fs`/`fr` claims are not the names the read used (`checkNames`, same file). `budgetClient` takes exactly once per run — one call site, and the read is read-only, so a second one would find the object again — and shares the result with the budget session |
| **Exchange** | `internal/budget/token/exchange.go`: `Exchange` turns the custom token into an ID token; its claims, `fs`, `fr`, `fx`, `fp`, `rb` and the uid `UID`, are the run's database identity, and they are what the rules test (`internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`, e.g. `auth.token.fs == $slug && auth.token.fr == $run`). The claims and `UID` are declared in `internal/budget/token/token.go` |
| **Mark** | `internal/cli/run.go` sets `lspec.SecretEnv = []string{sealMarker(sealKey)}` — `FUGARO_BUDGET_TOKEN` whose *value* is this launch's seal key (`internal/cli/launch_budget.go`), the one copy of it that leaves the launcher (D15); `internal/backend/backend.go`'s `LaunchSpec.SecretEnv` carries it as a secret member of the execution's container override (`internal/backend/gcp/run.go`, `Launch`), and `internal/runner/budget_session.go`'s `BackendFromEnv` parses it strictly (a value of `"1"` is a launcher from before the seal, which the run refuses at the take rather than reading an unsealed object, D19) |

Two properties of that channel are load-bearing and easy to lose in a redesign:

- **The token is not in the override.** The override carries the launch's seal key, never the
  token: what a reader of the override holds is a key to an object it may not have, and without
  the object the key opens nothing. The credential itself is a
  bucket object the launcher writes and one run reads. Design §4.2 states the rule for the token
  ("The budget token deliberately does not travel as an override"), and `internal/cli/launch_budget.go`
  states it for this code in the **file-level comment** above the launcher's part of the budget
  backend — the token "is never in the jobs.run request (the request reaches audit logs), an env var,
  an error or a message" — which is where the invariant is written. `mintBudgetToken`'s own doc
  comment is about the mint it performs (the earlier attempt's object is replaced) and does not carry
  the sentence.
- **The take is the consume**, and since the seal it is *not* the delete that makes it one:
  `TakeObject` never calls delete — the job account's grant on `tokens/` is read-only (D16,
  §7.2), so a delete is a request the run already knows is denied, and gocloud reports the denial
  as `NotFound`, the same error an object already gone gets, which is why no path may infer
  anything from one. What makes a token usable once is the key: the launcher draws it for a single
  execution, delivers it there and nowhere else (D15, D17), and `takeBudgetToken` has one call
  site — `budgetClient`, which memoizes what it read — so the second read a second take would need
  finds ciphertext under a key this run has nothing left to open. A run that finds none where its
  own marker says one was minted refuses to start rather than run
  without a session, a cap or a kill switch (`budgetClient`'s `TokenMarked` branch). That refusal
  is the fail-closed path the L-b finding of PR #379 hardened: an environment that cannot be
  parsed is `MalformedEnv` and stops the run whatever its mode.

Who may mint: the signer service account holds no roles (`deploy/terraform/gcp/modules/firebase/iam.tf`,
`google_service_account.signer`), and the custom role `google_project_iam_custom_role.token_minter`
holds exactly `iam.serviceAccounts.signJwt`, granted to the launchers and operators by
`google_service_account_iam_member.minter`. So the ability to mint a budget identity for any
repository is a property of "may launch", which SECURITY.md already states.

## 2. What phase 3 changes about it

Phase 3 (plan Task 3.2) creates `deploy/terraform/gcp/modules/project/`, which does not exist on
`next`, and replaces the per-repository condition with one condition per prefix —
`internal/backend/gcp/names.go`'s `JobBucketPrefixes` (`runs`, `cache`, `locks`) lose the slug, and
`BucketCondition` gives way to a project form. One job account, one condition per prefix, every
repository of the profile.

The consequence for this channel is exact: `runs/<other-slug>/<other-run>/budget-token` becomes
readable by every run of the job. The listing denial does not help. A conditional grant gives no
`storage.objects.list` (`docs/design/bucket-iam.md` §2.3; live check 7 records listing `runs/` as a
403), and the theft does not need a listing, because both names are computable offline:

- the slug is a function of the provider kind and the canonical repository path
  (`internal/task/task.go`, `Slug`), so anyone who can name a repository can compute its slug;
- the run id is a timestamp plus two random bytes (`internal/task/task.go`, `NewRunID`, checked by
  `runIDRE`), so the search space is bounded by the minute the peer was launched — and the launcher
  writes the token *before* the execution starts, so there is a queue window in which the object is
  there and no run has taken it;
- a run can also simply delete a queued peer's object, which is a denial of service rather than a
  theft, but comes from the same widened grant.

D13 accepted the theft, because the phase-3 alternative was worse and the isolation that matters
(the profile's) is unchanged. This document is the replacement, not a reversal of that ruling.

## 3. The constraints

A candidate channel has to satisfy all of these. They are quoted from the plan (Global Constraints,
Task 3b.1's "What it must not do") and from the designs it builds on.

1. **Never a launcher-overridable environment variable.** The token does not travel as an override
   (design §4.2), and a value that decides a *trust* question never comes from one (§12a item 2,
   where the provider must not). The reason is the carrier's audience, and it is wider than the
   launchers: an override is part of the `jobs.run` request body, and that body reaches audit logs
   which any log viewer on the project can read — `docs/design/m9-budget-and-dashboard.md` §6.4
   states exactly this as the reason the token itself does not travel that way, and SECURITY.md
   repeats it in its launcher paragraph. The account that may *set* overrides holds `run.jobs.run`
   and `run.jobs.runWithOverrides`
   (`deploy/terraform/gcp/modules/installation/iam.tf`, `google_project_iam_custom_role.job_runner`;
   `deploy/terraform/gcp/modules/workflow/job.tf`, `google_cloud_run_v2_job_iam_member.launcher`, whose
   own comment is "Launchers start runs with overrides, on this job only"), and the accounts that may
   only *read* an execution are the second audience: the sweep's `fugaroHistory` holds
   `run.executions.get` and `run.jobs.get` (`deploy/terraform/gcp/modules/installation/history.tf`),
   as does the launchers' own `fugaroLauncher` role (`.../iam.tf`). **Whether holding that
   permission makes them readers of the override is the half U2 leaves open**, and it is priced that
   way below and in §7.1; what is documented, by `m9-budget-and-dashboard.md` §6.4 and SECURITY.md,
   is the audit log. Installations with
   `log_isolation` on (`deploy/terraform/gcp/modules/installation/logging.tf`) route a *job's own*
   container logs to a bucket only launchers and operators may view; that narrows the run log, not
   the audit log, and owners and editors read everything either way.
   **UNVERIFIED, and its probe:** whether the API serves an execution's own override back at all.
   The CLI reads a *job's* whole environment through that API (`internal/backend/gcp/run.go`,
   `JobFullEnv`), and whether an execution reports the override on top of the template is design
   §4.2's D10 option (b), marked UNVERIFIED there and settled by plan Task 3.0 step 3 — the U2 probe,
   Check 52 step 3, which has not run. **Until that probe reports, every sentence in this document
   that would rest on the execution record or on an override view is a hypothesis, not a fact**, and
   it is marked as one where it appears (§7.1, §7.2, and Check 60 step 3). The part that does not
   depend on the answer is the audit log, which is stated by `m9-budget-and-dashboard.md` §6.4 and
   SECURITY.md above; §7.1 says so where the seal's audience is priced.
2. **Never widen a service account's grants beyond its prefix.** Every grant is a member resource,
   never a binding or a policy (`deploy/terraform/gcp/modules/installation/iam.tf` says so at the top
   of the file), and a job's is `roles/storage.objectUser` restricted by a condition that names its
   own prefixes (`deploy/terraform/gcp/modules/workflow/sa.tf`,
   `google_storage_bucket_iam_member.job`). A design that needs the run-time creation of IAM bindings,
   or a bucket-wide role, breaks this and reverses 0.7.0's hardening. **One grant this document does
   widen, deliberately and by decision**, is a *user* account's and not a service account's: the
   launchers' conditioned write on `runs/` has to cover `tokens/` once the object has moved (§7.2,
   D16), because the launcher is what mints and cleans up that object — "the prefix it needs" grows
   with the object, and the member's `precondition` re-checks the new condition in the same change.
   The constraint this leaves standing is the one that matters: the widened grant is still one
   condition on one prefix, not a role on the bucket.
3. **Fail closed.** A channel that cannot be read, or cannot be parsed, must fail closed: it stops
   the run. The shipped model is the L-b one: the marker's presence is what makes a missing token a
   refusal rather than a quiet "no session here", and a malformed value is an error
   (`BackendFromEnv`).
4. **Runs of one shared job must not reach each other's tokens.** This is the threat being designed
   against (§2 above).
5. **The launcher stays trusted (D10).** The runner cannot remove the launcher's ability to override
   a job's environment, and no phase claims to. A design that trusts the launcher *for less* than
   "it mints what it mints, after it wins the claim" buys nothing and costs a permission.
6. **Preserve what the channel already has.** Consume-once; one identity per run (`fs`/`fr` are the
   run's own keys, and the rules and the spend attribution depend on them); the object out of the
   path a peer shares, which Task 3b.1's own output names ("migration of the token object out of
   `runs/`").

## 4. The threat model, stated once

The adversary is an agent inside a run of a job that phase 3 shares between repositories: it has
the job's own credentials, it can read the job's environment, it can reach the metadata server, and
it can write code. It is *not* an operator, it is not a launcher, and it holds no permission on the
Cloud Run API — a job account is granted nothing there (`deploy/terraform/gcp/modules/workflow/sa.tf`).

What it wants is a peer run's token: those claims are a peer's database identity, so they are that
peer's spend ledger, its kill-switch state and its attribution. What it already has is the peer's
task spec, its logs and its checkout, once phase 3 widens the grant — D13's accepted exposure, and
the reason SECURITY.md says what it says.

Two failures are in scope and one is not:

- **Reach** (can the peer *read* the token object at all): a channel that only moves the object does
  not answer this, because phase 3's grant is per prefix.
- **Use** (can the peer turn what it reads into an identity): a channel that makes the object
  useless without a second factor answers the theft even where the reach stays.
- Not in scope: a launcher, or anyone who may launch, minting a token for any repository. That is
  D10 and SECURITY.md's own paragraph; it is not this channel's boundary, and pretending otherwise
  would be the mistake §12a item 2 warns about.

## 5. The four candidates the plan lists, priced

### C1. A signed URL minted by the launcher and handed over as an override

The launcher mints a V4 signed URL for the token object and puts the URL in the execution's
override instead of the object staying where it is.

- **Permission.** Signing a URL needs `iam.serviceAccounts.signBlob` (or a private key) on the
  signer. Today the signer holds no key at all — `token.IAMSigner` calls `signJwt` against the IAM
  Credentials API precisely so that no key exists — and `google_project_iam_custom_role.token_minter`
  holds exactly `signJwt`. C1 therefore widens the minting role, and with it every launcher and
  operator: **fails constraint 2**, and it widens the most sensitive role in the project.
- **Delivery.** The URL is a bearer credential in a query string, and it travels in the override —
  the one place the design forbids the token to be. It is retained by Cloud Run in the execution's
  specification and is visible to the account that may set overrides: **fails constraint 1**, which
  is the constraint this candidate fails most literally.
- **Property loss.** A URL is valid for its whole window, however many times it is used, so it
  replaces consume-once with "valid until it expires", and the exposure window becomes the token's
  own lifetime rather than the queue window.
- **Does it even help?** The object stays in `runs/<slug>/<run>/`, which a peer of the shared job can
  read. C1 gives a *different* reader a URL; it removes nothing from the peer's reach. It buys
  nothing against §4's threat.

**Verdict: reject.** It fails two constraints and answers no part of the threat. Its one real
advantage — the token never enters a bucket object the job account can read — is not achieved,
because the object it signs is still there.

### C2. A launcher-created Secret Manager secret, read only by the run's own service account

- **"The run's own service account" does not exist under phase 3.** The service account is the
  job's, one per (profile, provider, account). A run's own account would be a per-run account, which
  is exactly what phase 3 removes. What is left is the *shared* job account, and a secret it may read
  is a secret every peer run may read: the design would have to distinguish peers by something other
  than the account — a name prefix condition, which is the probe below — or grant each run's secret
  individually.
- **The probe gates the prefix form, and it has not run.** Plan Task 3.0 (Check 52) step 4 is the U3
  probe: whether Secret Manager IAM conditions accept a name-prefix condition and whether an
  impersonated `versions access` is then allowed exactly on the matching prefix. The owner **has not
  run Check 52**. If the probe answers **yes**, a `tokens/`-style prefix with a per-run condition is
  expressible on secrets, and C2 becomes the same design as C3 with a different store — inheriting
  C3's enumeration problem, because the condition can only name what the launcher wrote into it. If
  the probe answers **no**, the only remaining shape is one secret per run, granted at the secret
  level, which needs a new permission the launcher does not hold today
  (`google_project_iam_custom_role.launcher` is metadata-only around secrets) and a revoke step
  after the run.
- **Revocation is not fail-closed.** A grant created at launch and removed "after the run" has no
  owner for the interval when the launcher dies or the execution never starts: the channel stays open,
  and the object of that failure is a live token. Constraint 3 is about refusal, and this path's
  failure mode is an open grant.
- **Cost and surface.** Secret Manager adds a resource per run, a version, an access audit trail
  that names the accessor and not the run, and project-level resources for what is one small
  per-run object. Design §6.3's rule for project-scope keys (D2) points the other way: a project
  secret inside a shared job has no IAM line between repositories either.

**Verdict: reject as stated; revivable only by a probe result, and even then it is C3's design in a
heavier store.** The dependency is explicit: C2 depends on Check 52 step 4 (U3), and if that probe
fails, C2 needs a new launcher permission and a revoke step that no phase currently owns.

### C3. A `tokens/<run-hash>` prefix whose IAM condition names one run

- **An IAM condition names what is written into the binding.** Conditions are evaluated against the
  resource, not derived from it, so "names one run" means a binding whose expression contains that
  run's name — one binding per run, created at launch and removed after. On the runs bucket that is
  `storage.buckets.setIamPolicy` **at run time**, which is the permission 0.7.0's hardening removed
  from this path and which no Fugaro account holds for the runs bucket today: the one unconditional
  bucket-wide grant there is the **operators'** `roles/storage.objectAdmin`, an object-level role
  (`deploy/terraform/gcp/modules/installation/iam.tf`, `google_storage_bucket_iam_member.runs`), and
  the launchers hold `runs_reader` (`objectViewer`, no condition, the whole bucket) and the
  conditioned `runs_launcher` (`objectUser`, conditioned to `runs/` alone as this alternative was
  priced — §7.2 records where the seal's own merge widened it to `runs/ || tokens/`, which only
  makes a launcher's account wider here, not narrower) — neither carries
  `storage.buckets.setIamPolicy`.
  Project-level roles bypass bucket IAM whatever this design does — SECURITY.md already says so —
  and this adds one more thing to that set for a launcher's own account.
  **Fails constraint 2**, in its strongest form: the widening is not a wider condition but the
  permission to *write* conditions, held by the launcher's own user accounts.
- **Removal has no owner.** The same fail-open interval as C2: a launcher that dies between the mint
  and the start leaves a binding that grants the job account one run's token forever.
- **A static prefix names no run.** A `tokens/` prefix shared by the job account is phase 3's
  problem moved one prefix over. To be a boundary the name would have to be unguessable, and a name
  built by hashing the run id is not: the run id is enumerable (§2), and a hash of an enumerable
  input is enumerable one step later.
- **The bucket is the wrong object for it.** Bindings are also byte-exact and precondition-pinned
  elsewhere in this codebase (`internal/backend/gcp/names.go`, `BucketCondition`, and
  `LauncherBucketCondition`'s title, which `deploy/terraform/gcp/modules/installation/iam.tf`
  re-checks in a `precondition`), so a per-run binding is a per-run Terraform-shaped object created
  outside Terraform — a second IAM world beside the one the bootstrap owns.

**Verdict: reject.** It needs the permission that would undo the hardening, and its per-run form
fails open when a launcher dies.

### C4. Exchanging the run's own Cloud Run execution identity for an RTDB identity

This deletes the launcher's mint, and with it the launcher's `signJwt` reach — which is genuinely
attractive. There are three ways to do it, and they fail differently:

- **(a) A Fugaro service that trusts the caller's workload identity.** The service sees the
  execution's service-account identity and mints claims for it. But under phase 3 that identity is
  *the shared job's*: it cannot distinguish one run from another, so the run's own names would have
  to come from the run — self-asserted claims. Then any run of any job, in any project, can ask for
  any repository's identity, which is strictly worse than D13's exposure and makes `fs`/`fr`
  decorative. It also inverts constraint 5: today the trusted party is the launcher, and the run is
  the untrusted one.
- **(b) The run authenticates to the database directly with its own cloud identity.** That identity
  has no `fs`, `fr`, `fx` or `fp` claims, and the rules are written in terms of them, so the rules
  would have to be redesigned to test something else: project-level database IAM
  (`roles/firebasedatabase.*`), which is per project rather than per run. Per-run spend attribution,
  the per-run cap and the kill switch keyed on the run all lose the claim they are keyed on.
- **(c) Per-run impersonated identities.** Per-run service accounts are what phase 3 removes; and
  even with them, a token minted by any holder of `iam.serviceAccounts.getAccessToken` is the same
  bearer credential in a different wrapper.

**Verdict: reject.** (a) is the shape to avoid reinventing later under the name of simplification —
it moves the trust decision from the launcher to the run — and (b) and (c) cost the per-run identity
that the budget is built on. The one thing worth keeping from C4 is its direction of travel: if a
future phase ever gives each run its own workload identity, this document's channel becomes
unnecessary, and the seal below becomes hygiene rather than a control.

## 6. The score sheet

| | never an override | no widened grant | fails closed | answers the theft | keeps consume-once | launcher trusted as today |
| --- | --- | --- | --- | --- | --- | --- |
| **C1** signed URL | **no** | **no** (`signBlob`) | yes | **no** | **no** | yes |
| **C2** Secret Manager | yes | probe-dependent | **no** (revoke) | probe-dependent | yes | needs a new permission |
| **C3** `tokens/<run-hash>` per-run condition | yes | **no** (`setIamPolicy`) | **no** (fail-open interval) | partly | yes | needs a new permission |
| **C4** execution identity | yes | yes | yes | **no** (a) / loses `fr` (b) | n/a | **inverts it** |

## 7. The recommendation: seal the token, and move it out of the prefixes a peer shares

Nothing in the four candidates survives the score sheet, because they are all attempts to keep the
peer from *reading* the object, and phase 3's grant is per prefix: the reach cannot be narrowed
without a widening of a different, worse kind. So the recommendation changes the question. Give the
object a second factor that the launcher hands to exactly one execution, so that reading it is worth
nothing.

### 7.1 The seal (the part that answers the theft)

- `token.PutObject` writes the custom token **sealed**: AES-256-GCM under a key the launcher
  generates with `crypto/rand` for this launch, in a small versioned envelope so that a later format
  is distinguishable and a plaintext object is distinguishable from a sealed one. The envelope is
  **`version byte || 12-byte nonce || ciphertext || 16-byte tag`**, and the object's own key —
  `token.ObjectKey(slug, run)`, the `tokens/…` form of §7.2 once the object has moved — **followed by
  a zero byte and the envelope's version byte**, is the GCM **additional authenticated data**. The
  zero byte is the separator (an object key carries none), so a name and a version cannot be
  rearranged into another pair with the same AAD. The version byte is in the AAD as well as checked
  — it is the envelope's first byte, and `Unseal` refuses a foreign one before it opens anything —
  because the check is a statement about *this* build's format and the binding is a statement about
  the bytes: a later version's envelope can then only be opened by code that knows which format it
  holds. The AAD is not part of the envelope, so binding it changes nothing about the shape above.
  **What stops a sealed object from being replayed sideways is the
  per-launch key, not the AAD**: while the object lived under `runs/` — where it sat before the
  move, and where D19's seal-only step priced it staying — copying one run's object onto another
  run's key was a write the peer's grant allows (`google_storage_bucket_iam_member.job`, whose
  condition covers the bucket's declared prefixes), but such a copy is sealed under the *other*
  run's key, so it fails to unseal at the victim regardless of what the AAD says. In the merged tree,
  after the move, the peer cannot make that copy at all:
  its grant on `tokens/` is read-only (D16, §7.2), so the sideways *write* stops being available
  while the sideways *read* is exactly what the seal is for. The AAD's job is narrower and still
  worth its line: it binds the ciphertext to the object's own name, so a blob sealed for one key
  string cannot be read under another — which is what makes the move out of `runs/` safe rather than
  merely tidy, since the move changes the key string and the AAD with it. **What lands together is
  therefore the name change and the code that computes the name, not the seal and the move**: D18's
  rule held in the merge rather than being deferred — Slice B moved `token.ObjectKey` to the
  `tokens/` form in the same change that sealed, so no shipped tree ever seals under the `runs/`
  name, and the grants that read the new name travelled with it (the per-repository one in the
  workflow module, the shared job's whole-prefix one in the phase-3 tree: the paragraph above).
  **The AAD cannot be proved
  by the end-to-end check**: every step of Check 60 that could exercise it already fails earlier, at
  the unseal, because the keys differ. It needs a unit test in `internal/budget/token` that seals under
  one key string and opens under another and asserts the tag check refuses — that test, not a check
  step, is where this claim is held. The version byte's half of the AAD is held the same way and
  needs its own: through `Unseal` the binding is invisible (the version byte is checked before the
  open), so the test that holds it opens the envelope with `aad` itself and with a changed version
  (`seal_aad_test.go`, in the package rather than beside it).
- The key is **32 bytes from `crypto/rand`**, carried in the override as **unpadded base64url** (43
  characters), and the parse is strict, because the value decides whether the run unseals anything
  at all: a value that is not exactly one unpadded base64url string decoding to 32 bytes —
  whitespace, `=` padding, a second segment, a shorter or longer key, or a non-canonical spelling of
  the right 32 bytes — is `MalformedEnv` and stops the run (§7.4, row 2). The last of those is not
  pedantry: 32 bytes is three whole base64url groups plus two, so the last character carries four
  significant bits and two the encoding discards, and **three other 43-character spellings decode to
  the same key**. The runner registers the *encoded* key with its redactor, so a lax parse would
  leave three live spellings of the key that nothing redacts; `token.ParseKey` decodes through
  `base64.RawURLEncoding.Strict()` and the rubric's value is one spelling, the one the redactor
  knows. It is a key, not a passphrase, so there is no KDF anywhere in the channel.
- The key travels in the override the launcher already writes. `internal/cli/run.go`'s
  `lspec.Env = []string{runner.BudgetTokenEnv + "=1"}` becomes a marker whose *value* is the
  execution's key; no new variable name, no new permission and no new resource for the carrier.
- The runner's `takeBudgetToken` unseals before `checkNames`, computing the same `ObjectKey` for the
  AAD, and `BackendFromEnv` parses the value: a present value that cannot be parsed is
  `MalformedEnv` and stops the run, exactly as today.

Why this satisfies §4: the peer's reach is unchanged — it can still read the object — and what it
reads is ciphertext under a key that was delivered to this execution and no other. The key is not in
the bucket, and a **peer run** — the attacker §4 defines, holding the job's bucket grant and nothing
else — holds no permission on the Cloud Run API from which an execution's override could be read
back, nor on the logs that would carry it (constraint 5; the paragraph below prices the wider
audience, which is where the temptation to overstate the seal lies).

This is also where the design's tension is, and it should be named rather than buried: the seal
moves a secret into the one carrier constraint 1 distrusts. The answer is **not** "an override is
readable only by launchers" — that claim is false, and this repository's own designs are where it
was settled: the token itself does not travel this way *because* a `jobs.run` request body reaches
audit logs that every project log viewer can read (`m9-budget-and-dashboard.md` §6.4). That half is
documented and holds regardless of U2. The second half — the accounts that hold only
`run.executions.get` (the sweep's `fugaroHistory`, the launchers' `fugaroLauncher`, §3.1) are
readers of the override as well — **is the half U2 leaves open, and it is stated here as a
hypothesis, not as a fact**: it is true only if the API serves an execution's override back, which
is D10 option (b), UNVERIFIED in the design and unprobed (Check 52 step 3). If U2 reports "no", this
paragraph loses nothing it needs — the audit-log half is the audience constraint 1 is about. The
honest answer is the audience itself: the carrier is readable by the project's *readers* —
launchers, operators, and anyone the owner has given log read, and, under U2, execution read — and
it is **not** readable by a peer run of the shared job, which is
the adversary §4 names and the only one whose reach phase 3 widens: a run of the job holds its
bucket grant and nothing on the Cloud Run API (`deploy/terraform/gcp/modules/workflow/sa.tf`), so
`run.jobs.get` and `run.executions.get` are closed to it, and the audit log is not a log it may
read. The design's own rule, "the token does not travel as an override", is about the *token*: a
bearer credential whose theft is the whole attack. A per-execution key whose audience is the
project's readers — who can already read every log, and whom the bucket grants already let read every
object — is a different object, and the honest way to keep the rule is to say precisely what its
audience is instead of widening it in silence. If the owner would rather not carry a secret in an
override at all, D15's second option (a strictly parsed variable) changes the name and not the
audience. One property is worth stating because it is what makes the carrier safe in the meantime:
overriding the key cannot widen anything. The object's bytes are sealed under the key generated by
the claim-winning launch that wrote it, so a different key, whoever set it, unseals nothing — it
only makes this execution's take fail, which is this design's refusal and not a grant. Constraint
1's rule is kept where it still has teeth: what a run must never accept is a *credential* from an
override, and the key is not one; the token stays in the bucket object it has always been in.

**One reader falls outside the seal, and pricing that case is what keeps this section honest.** A
principal that holds *both* halves — an override view and the sealed object — recovers the token,
because the seal has no third factor and the key is not secret from that principal by any mechanism
this design adds. That principal exists today: the sealed object is readable by every run of the job
(§2's reach) and by the operators, whose grant on the runs bucket is unconditional
`roles/storage.objectAdmin` (`google_storage_bucket_iam_member.runs`, the grant C3 names and prices),
while the
override half is what a project log viewer has — documented by `m9-budget-and-dashboard.md` §6.4 —
and, **under U2**, what a `run.executions.get` holder has as well. Two compositions follow, and both
are accepted rather than fixed:

- **"an operator who reads the audit log"** — the operator's object grant is unconditional, so the
  object half is theirs for the asking, and the audit-log half is documented. Nothing this design
  adds takes either away, and the seal does nothing against them.
- **"a project log viewer, plus any run that hands over the sealed object it can read."** This one
  does not need U2 and it does not need an operator: §2's reach means every run of the job — the
  peer §4 defines included — can *read* every other run's object, and a compromised or hostile run
  can post those bytes anywhere the log viewer can see (a log line, a comment, a request out). The
  log viewer then holds both halves, and the seal is defeated. It is not a new power either — a
  compromised run can read the *token object itself* today, which is what the seal is a mitigation
  for, and the unsealed objection is one an attacker who already runs code inside the job has
  already answered — but it is the reason the seal's sentence below is "capability, not a boundary"
  and not "readable by nobody but the execution".

Neither composition is a new power, and this document must not be read as claiming to remove one: an
operator can already read every object in the bucket, and a launcher can already mint any
repository's budget identity with
`signJwt` (SECURITY.md's own paragraph). What the seal removes is the *peer run's* theft — the
exposure D13 accepted — and for every principal outside §4's adversary the honest description of it
is the sentence §7.1 already ends on: a capability, not a boundary.

This is a **capability, not a boundary**: it removes the value of what a peer can reach; it does not
narrow what a peer can reach. The distinction is the whole point of writing it down, because the
natural next question — "so can a peer read another run's token?" — has the honest answer "it can
read bytes that are not usable without a key it does not have", and not "no".

**The launch's own agent holds the key, and that is the residual the seal does not remove.** The
adversary §4 defines is the *peer*, and the seal answers it by delivering the key to one execution —
but that execution is the one the run's own agent shares. The agent's own environment is built from
an allow-list and never carries the marker (`internal/agent`'s `BuildEnv` takes `baseEnv`, the auth
variables and the workflow's declared secrets, and `FUGARO_BUDGET_TOKEN` is in none of them), and
`internal/gitops`' `processEnv` strips it from every git call and from anything git runs; neither
closes the last route, because the agent runs as the same user in the same container as the runner,
which holds the marker in its own process environment: the agent can read `/proc/<runner-pid>/environ`
and recover the key by exactly the route it already recovers the model credentials, the provider keys
and the job's service account token by (SECURITY.md, "What one run's agent can reach"). It is stated
rather than assumed because §7.1's whole method is naming the audience of the key instead of
widening it in silence, and because the two mitigations above are orderings and hygiene, not
boundaries: what the agent gets from the key is its own run's token — the identity the run already
spends under, whose theft by the run itself is the "an agent that reads the real key from `/proc` can
spend outside it" case SECURITY.md already prices — and not a peer's. The seal neither adds nor
removes anything here; a run that reads it is the principal the token was minted for. Closing it
would need the runner to hand the execution's key to something the agent's own user cannot read,
which is a sandbox this document does not design and no candidate in §5 provides.

### 7.2 The move out of `runs/` (the part that stops the denial of service)

Today a peer can *delete* a queued run's token object, and the victim then refuses to start
(`budgetClient`'s `TokenMarked` branch is exactly that refusal). The seal does not change this, so
the second half of the recommendation moves the object:

- the token object lives under a `tokens/` prefix (`ObjectKey` becomes
  `tokens/<slug>/<run>/budget-token`), out of `runs/`, which is where the task spec, the launch
  record and the logs live;
- the job account's grant on that prefix is read-only (`roles/storage.objectViewer`-shaped, or the
  equivalent under the project module's per-prefix condition), so a peer cannot delete a queued
  token. **The launchers' grant does not stay as it is**, and pretending otherwise would be the
  expensive kind of wrong: their only write grant on the runs bucket
  (`deploy/terraform/gcp/modules/installation/iam.tf`,
  `google_storage_bucket_iam_member.runs_launcher`, `roles/storage.objectUser`) was conditioned to
  `runs/` alone, under the title `fugaro-launchers-runs` and the expression
  `resource.name.startsWith("projects/_/buckets/<bucket>/objects/runs/")`. **The 2026-10-18 review
  of the merged tree caught this paragraph pricing a choice the merge had already made**: it landed
  as one widened member, not a second conditioned one, and the widened expression carries a new
  title, because an installation still holding the runs/-only binding has to read as a *different*
  binding rather than as this one. The title is now `fugaro-launchers-runs-tokens` and the
  expression is the two-prefix string `gcp.LauncherBucketCondition` builds —
  `resource.name.startsWith("…/objects/runs/") || resource.name.startsWith("…/objects/tokens/")`
  (`internal/backend/gcp/names.go`) — with the member's own `precondition` pinning exactly that
  pair, its tftest line and the install guard's reading of the grant beside it. `PutObject`,
  `DeleteObject` and `dropBudgetToken` under `tokens/` are what the widened expression is for. This
  is a deliberate widening of a *launcher* grant — the price of the move, not of the seal — and D16
  and D19 are where it is decided and ordered. The launchers need nothing new to *read* the prefix:
  their `google_storage_bucket_iam_member.runs_reader` (`objectViewer`, no condition) already covers
  the whole bucket.
- the bucket's one-day lifecycle rule moved with it: it is scoped by
  `matches_prefix = ["runs/", "tokens/"]` and `matches_suffix = ["/budget-token"]`
  (`deploy/terraform/gcp/modules/installation/bucket.tf`), so an untaken token still dies;
- the take has **one shape, and no path infers anything from a delete's error.** `token.ObjectKey`
  is fixed at `tokens/<slug>/<run>/budget-token`, and the take on it is the read-only one: it
  unseals the object with the launch's key and **never calls delete at all** — no delete error
  exists to be misread, and the run never issues a request its grant already denies. **The
  2026-10-18 review caught this bullet describing two shapes and a switch between them**: the
  runs/-keyed shape, and with it the fatal delete (`token.TakeObject`'s own contract then, "the
  token object could not be deleted, so it is not used"), was Slice A's, and the merge shipped the
  seal and the move in one change, so the merged tree has one shape only and there is nothing for a
  fallback to fall back to. The rule the switch was written for is still worth stating, for anyone
  tempted to key a fallback on an error later: gocloud maps every 403 to `NotFound`
  (`docs/design/bucket-iam.md` §2.3), so "the delete was refused" and "the object is already gone"
  arrive as the same error, and a fallback keyed on that error would read a denied delete as a clean
  consume-once and would silently stop deleting wherever the grant is misconfigured. Consume-once
  then rests on the key, which is delivered once, to one execution, because the launch claim was won
  once; an untaken token still dies by the launcher's own `dropBudgetToken` and by the lifecycle
  rule above.

  **The read-only take leaves the object in place, and that is a residue this section has to
  state.** Under `tokens/` the take never deletes, so after the legitimate execution has read it the
  sealed object stays in the bucket until the lifecycle rule above reaps it — up to a day. **Two
  clocks bound that residue, and they are not the same clock** — the round-3 review caught this
  paragraph reading `token.ExpiryFX` as the token's own life, which it is not. The *token* is
  exchangeable for as long as it is valid inside Firebase: `token.MintAt` sets its `exp` to the mint
  plus `token.Lifetime`, one hour, which is Firebase's maximum for a custom token. So a copied object
  **and** its key can be traded for a session for about an hour after the mint, whenever the copy was
  taken. The session that trade yields has a longer life, and a different one: its `fx` claim is
  `token.ExpiryFX(launch, jobTimeout)` — the launch plus the job's own timeout plus `QueueAllowance`
  (an hour) plus `Slack` (five minutes) — and the rules stop honouring the identity at `fx`, whatever
  the token's lifetime was. For a job whose timeout is an hour, that is a little over two hours past
  the mint. What the residue is *not* is nothing: anyone who holds the object **and** that
  execution's key can replay the unseal inside the first of those two windows — about an hour — and
  exchange the token while it is still live, and the identity they get runs to `fx`. It is the same
  both-halves composition §7.1 prices, now with a day to notice it instead of a single read.
  Consume-once under `tokens/` rests entirely on the key, which is delivered once, to one execution,
  in the override of the launch that won the claim, and whose audience is §7.1's and not §4's peer
  (D15); the object's own stay in the bucket gives no second delivery, which is why a copy taken
  *before* the legitimate take is the case that matters. The alternative — making the read-only take
  delete — is not available, since the delete is exactly the write the job's grant does not have
  (this section's second bullet, and D16).

### 7.3 What this does not change

- D13's paragraph in SECURITY.md stays, narrowed to what it still describes: the *other* objects a
  shared job's account can read across repositories — task specs, launch records, logs, cache,
  locks. Inside a shared job the profile is the isolation boundary for secrets only, not for those
  objects (an open owner question, SECURITY.md), and this document does not move it. If phase 3 lands with this design, the paragraph says the token is sealed and says why the
  rest is not.
- The mint, the claims, the rules, the exchange, the marker's *presence* semantics and the fail-closed
  parse are unchanged. The budget's model does not move.
- No new resource, no new API, and no new permission **for the seal**: it is in
  `internal/budget/token`, its key is carried by a variable that already exists, and nothing is
  granted for it. The `tokens/` move is *not* free in the same sense, and claiming it was would be
  the false sentence this section exists to avoid: it adds a prefix to the job account's grant and
  **widens the launchers' conditioned write grant** (§7.2) — a Terraform change with its own
  `precondition`, its tftest lines and the install guard's reading of the grant, none of which the
  seal touches. That split is the point of D16 and D19: the seal can ship on its own, while the
  per-repository conditions still protect the object, and the move lands with phase 3, when the
  grant is what changes anyway.

### 7.4 The failure paths, and why each of them refuses

| failure | behaviour |
| --- | --- |
| the marker is absent on a budget-on run | refuse (shipped: `budgetClient`'s `ErrNoToken` branch) |
| the marker is present and unparsable | refuse as damaged (shipped: `MalformedEnv`), and the parse must stop echoing the value — see below |
| the object is missing under a present marker | refuse (shipped, `TokenMarked`) |
| the object is sealed and the tag does not verify | refuse: an unsealed or tampered object is never read as a token, not even during a mixed deployment |
| the object is sealed and no key is presented to this execution (the marker is absent or empty, or the run is not the one the object was sealed for) | refuse, and **never** fall back to reading the bytes as a token: a sealed object with no usable key is not "a plaintext token in a mixed deployment", it is the theft case, and treating it as readable would hand the peer's stolen copy straight back. It is the row that keeps a mixed deployment from becoming an unsealing one |
| the object was sealed for another name (the AAD is the key it was stored under, §7.1) | refuse: a sealed object copied onto another run's key does not unseal. **The key, not the AAD, is what makes this refuse** — the copy is sealed under a different per-launch key — so this row's property is the per-launch key's; the AAD's own property (a blob sealed for one name cannot be opened under another) is held by the unit test §7.1 names, not by this row |
| the object is plaintext and the seal is expected | refuse, for the same reason: a fallback to plaintext is the whole exposure coming back quietly |
| the key is wrong because the execution is not the one the launcher minted for | the unseal fails and the run refuses; the take's `checkNames` still refuses a token whose own claims are not this run's |

One concrete requirement falls out of the second row, and the round-3 review was right that this
paragraph's earlier wording did not state it consistently: it is an **ordering** requirement, and a
redactor cannot carry it alone. `internal/runner/budget_session.go`'s `BackendFromEnv` words its
marker error as `fmt.Errorf("%s %q: it must be 1, ...", BudgetTokenEnv, marker)` — it echoes the
value. Harmless while the value is `"1"`; a leak of the run's own key into a bootstrap error, and
from there into the run's log, once the value is a key. The parse that can print it runs **before
the runner exists**: `internal/cli/exec.go`'s `runExec` calls
`runner.BackendFromEnv(os.LookupEnv, platform.Local())` at bootstrap, and the failure reaches the
record only later, as the run's `infra_error` — so at the moment the value could be formatted there
is no redactor to register the key with, and none to catch the value if it is printed. **The parse
must therefore not print the value at all**; that is the requirement, and the fix is to word the
error around the variable and the *shape* it wants ("it must be one unpadded base64url string
decoding to 32 bytes") with no value in it. The redactor is the second, weaker half: once the runner
holds the key it must register it with its own redactor, the way `takeBudgetToken` already does for
the token itself (`internal/runner/runner.go`'s `r.addSecret`, called from
`internal/runner/projectpolicy.go`). It is the runner, not the launcher: `addSecret` is a method on
the run's redactor and is the only door into it, so the launcher has no way to register anything —
whatever the launcher knows is redacted only once the runner's `takeBudgetToken` has said so. Both
halves belong to D19's first step, and both are why that step ships before the launcher seals: an
image that has neither prints the key.

### 7.5 What the later implementation would touch (not this task)

`internal/cli/launch_budget.go` (`mintBudgetToken`, `dropBudgetToken`, `tokenSigner`),
`internal/budget/token/object.go` (`ObjectKey`, `PutObject`, `TakeObject`, `checkNames`) plus the
seal's own file beside them, `internal/budget/token/mint.go` (`Mint`), `internal/runner/budget_session.go`
(`BackendFromEnv`, `BudgetTokenEnv`, `TokenMarked`, `MalformedEnv`) and
`internal/runner/projectpolicy.go` (`takeBudgetToken`), `internal/cli/run.go`
(the marker assignment), `internal/backend/backend.go` (`LaunchSpec.Env`'s documentation of what the
value is), the bucket IAM in `deploy/terraform/gcp/modules/workflow/sa.tf` and the project module
Task 3.2 creates, **the launchers' conditioned grant and its `precondition` in
`deploy/terraform/gcp/modules/installation/iam.tf` with the tftest lines that re-check it**
(§7.2), the lifecycle rule in `deploy/terraform/gcp/modules/installation/bucket.tf`, and the D13
paragraph in SECURITY.md — which cannot move before the marker's own sentence does (D15).

The condition the `precondition` pins is not the module's alone: four Go files read it, write it or
re-check it, and a change that misses them leaves the tree inconsistent rather than merely
undocumented:

- `internal/backend/gcp/names.go` — `LauncherBucketCondition` and `LauncherBucketConditionTitle`
  build the expression and the title the Terraform member's `precondition` pins; they are the source
  of the string, so the widened form starts here.
- `internal/infra/tfvars.go` — writes both into the generated tfvars, and reads them back.
- `internal/infra/hardening.go` — the install guard that reports a bucket finding when the live
  grant's condition does not match what the module expects, including the launcher condition.
- `internal/infra/tf/cover.go` — the Terraform cover that asserts the created members' roles and
  conditions, `runs_launcher` among them.

The seal's own file (the second entry in the list above) is the only one of these that the *seal*
touches; the four above belong to the move, and the split is D16's and D19's.

## The owner's open decisions

These continue the numbering of the project-centric series — D1 to D12 in
`docs/design/project-centric.md`, D13 in the plan. They are **this document's own D14 to D19** and
are unrelated to the D14 to D19 of `docs/design/m9-budget-and-dashboard.md`, which governs the
budget's own behaviour and not this channel.

**All six were answered by the owner on 2026-10-15**, and each answer is recorded beneath the
decision it answers, in the owner's own terms; the recommendation is left standing above it, so a
reader sees what was asked against what was decided. The heading is unchanged because it is the name
this section has been cited by since the first round, and the register of decisions should not move
under the readers who cite it. **The answers decide *what* to build; the gate is untouched** — no
code follows this document until the owner says go, and the status line's own words are what the
answers now point at.

**D14. Is the seal built at all, or is D13's documentation the permanent answer?** Phase 3b's
deliverable is this design, and D13 remains in force until something replaces it. If the owner
answers "documented is enough", this document becomes the record of a considered alternative and
nothing more. The recommendation is to build it, before or with phase 3 (see D19).

**Owner's answer (2026-10-15): build the seal.** The recommendation is the answer — the seal is
built, before phase 3 or with it, in the order D19 fixes (the seal first, on its own) — so this
document is not the record of "documented is enough". D13's paragraph in SECURITY.md stays as the
interim record until the seal lands — §7.3 says what it keeps then, and D15 says what the marker's
own sentence and the launcher paragraph beside it must become — and "documented is enough" is not
the answer a later implementer may fall back on.

**D15. Where does the key travel?** Recommended: in the existing marker's value
(`FUGARO_BUDGET_TOKEN=<key>`), because no new name has to stay in step between the launcher, the
runner, the docs and the tests, and the existing fail-closed parse can be tightened in place. The
alternative is a second, strictly parsed variable (a new name in `BackendFromEnv`, a new line in
`LaunchSpec.Env`, a new row in SECURITY.md and in the doc tests). A third option — a key object in
the bucket beside the token — is self-defeating and is listed only to be struck out. **Note the
nature change either way:** the marker stops being a one-bit marker and becomes a secret, whose
audience is the project's readers rather than the execution alone (§7.1: the launchers, anyone the
owner has given log read, and, under U2, anyone given execution read), which is precisely the
audience `m9-budget-and-dashboard.md` §6.4 keeps the *token* away from. So SECURITY.md's description
of the marker and the launcher
paragraph beside it — "The budget token deliberately does not travel that way … because the request
that starts a job reaches audit logs" — have to be rewritten **together**, or the two sentences
contradict each other; the distinction to write is the one §7.1 draws, a bearer token against a
per-execution key. The `%q` above makes the leak half of this concrete.

**Owner's answer (2026-10-15): the marker's value, with SECURITY.md's two sentences rewritten in the
same change.** The key travels in the existing marker — `FUGARO_BUDGET_TOKEN=<key>`, no second
variable — and the marker's description in SECURITY.md and the launcher paragraph beside it are
rewritten **together**, because a marker described one way and carried another is exactly the
contradiction the recommendation was written to avoid. What those two sentences have to say is the
distinction §7.1 draws: a **bearer token** is a credential whose theft is the whole attack, which is
why it does not travel as an override; a **per-execution key** is not a credential at all — it
unseals one object, for the one execution the launcher minted for, and grants nothing by itself — and
its audience is named rather than assumed (the project's readers: the launchers, anyone with log
read, and, under U2, anyone with execution read). The nature change stands and is priced rather than
waved away: the marker stops being a one-bit marker the moment the launcher seals.

**D16. Does `tokens/` become a fourth granted prefix, at which role?** Read-only for the job account
is what makes a peer unable to delete a queued token (7.2); it also means the runner no longer
deletes on take, so consume-once rests entirely on the key. Keeping `objectUser` on `tokens/` keeps
the code path and the shipped behaviour and leaves the denial of service in place. The
recommendation is read-only, and the decision is the owner's because it is a Terraform and a
runner-semantics change at once. **Either answer changes the launchers' grant**, which is the part
this document used to get wrong: their only write on the runs bucket is the `runs_launcher`
condition pinned to `runs/` by a `precondition`, so `tokens/` needs a second conditioned member or a
widened expression — a new title, the `precondition` that re-checks it and its tftest line (§7.2,
Check 60 step 8). The job account's read on the prefix is the decision; the launchers' write is the
consequence of the move itself.

**Owner's answer (2026-10-15): `tokens/` becomes a fourth granted prefix, read-only for the job
account.** The runner no longer deletes on take — §7.2's read-only shape, and no fallback keyed on the
403 that gocloud reports as `NotFound` — and consume-once rests on the key. The consequence the owner
named with the answer is the launchers' side of it: their `runs_launcher` write is conditioned to
`runs/` by its `precondition`, so `tokens/` needs either a second conditioned member or a widened
expression with its own title, its `precondition` and its tftest line, and that change lands with the
move in Task 3.2 (D19), not with the seal. The job account's *read* on the prefix and the launchers'
*write* on it are two grants; this answer is about the first, and the second follows from the move
itself.

**D17. The seal's lifetime and the re-mint path.** A `--retry` re-mints and re-puts the object
(`mintBudgetToken` deletes the earlier attempt's before writing), so it must re-seal and re-hand the
key to the new execution; the object's one-day lifecycle rule is unchanged, and so are the two clocks
§7.2 states apart — the **token's** own validity (`token.MintAt` sets `exp` to the mint plus
`token.Lifetime`, one hour, Firebase's maximum for a custom token) and the **identity's** `fx`
(`token.ExpiryFX`: the launch plus the job's own timeout plus `QueueAllowance` and `Slack`), which is
the longer of the two. This paragraph read `ExpiryFX` as the token's life when it was written, which
the round-3 review caught; the two are not the same clock and the difference is the hour of
exchangeability against the identity's life to `fx`. Deciding this now avoids a later implementation
choosing "seal once, reuse the key", which would put one key in front of two executions of the same
run.

**Owner's answer (2026-10-15): a retry re-mints, re-seals and hands a NEW key to the new
execution** — never one key in front of two executions of the same run. The path is the launcher's
own, and saying why bounds how many times a key can be handed out at all: Cloud Run never starts a
second execution behind the launcher — the job sets **`max_retries = 0`**
(`deploy/terraform/gcp/modules/workflow/job.tf`: "The provider's default is 3; a run is never retried
behind the launcher's back") — so the only second execution of a run is the launcher's own
`fugaro run --retry` (`internal/cli/run.go`), which launches the stored task of a run that never
started, and whose `mintBudgetToken` already deletes the earlier attempt's object before `PutObject`.
The re-seal rides that same call: a fresh key per attempt, the earlier attempt's object gone, and no
attempt able to unseal the next one's.

**D18. The envelope's shape and where it lives.** Recommended: a versioned sealed envelope defined
in `internal/budget/token` beside `PutObject`/`TakeObject` (one package owns both halves of the
channel), with the format's version as the envelope's first byte so a later format is refusable
rather than misread, and with a plaintext object treated as a refusal once the launcher seals. The
shape is fixed by §7.1 and decided here only in the sense of being accepted: `version byte ||
12-byte nonce || ciphertext || 16-byte tag`, the object's key followed by the version byte as
additional authenticated data,
and a 32-byte key carried as unpadded base64url. **The AAD makes the envelope's bytes depend on the
name they are stored under**, so the `tokens/` move of §7.2 changes the AAD and cannot be deployed
against a launcher or a runner that still computes the old key — **the move and the code that
computes the name land together**, and the merge did exactly that rather than deferring it: this
paragraph read "that change is Task 3.2's, not the seal's" while it was a plan, and the 2026-10-18
review of the merged tree found Slice B had moved the name as it sealed (§7.1, §7.2) — and the formats are
not interchangeable across the move (an object sealed under one name does not unseal under the
other, which is the property and not a migration problem, since a queued token is short-lived and a
re-launch re-seals). A generic helper package is the alternative; it would be the
first generic sealing code in the tree and it would put the format's compatibility rules away from
their only caller.

**Owner's answer (2026-10-15): the versioned envelope, in `internal/budget/token`.** The file lives
beside `PutObject`/`TakeObject`; the shape is **`version byte || 12-byte nonce || ciphertext ||
16-byte tag`**; the object's own name and the version byte are the additional authenticated data; the key is **32 bytes**
carried as **unpadded base64url**; and a plaintext object is **refused** once the launcher seals —
no fallback to plaintext, for the reason §7.4's rows give (a mixed deployment is not an unsealing
one), because a fallback would hand the exposure D13 accepted straight back.

**D19. Ordering against phase 3.** Recommended: the seal ships on its own first, while the
per-repository conditions are still what protects the object, because its tests are unit tests in
`internal/budget/token` and its only live effect is a launch and a take on an unchanged grant; the
`tokens/` move and the prefix condition then land with Task 3.2, when the grant is what changes. The
alternative — one change with phase 3 — means the exposure D13 accepts is never actually closed for
a window in which both the grant and the channel move at once. The owner decides, and this decision
is the one a reviewer should look at hardest.

**Owner's answer (2026-10-15): the seal ships first, on its own** — unit tests in
`internal/budget/token`, no grant change — and the `tokens/` move and the prefix condition land
later, with Task 3.2. **Ordering is load-bearing in one more direction than the recommendation
said, and this is where it is written down**, because the round-3 review found it and it is a live
consequence of D15 rather than a detail of it. Once a launcher seals, the marker's value is a key,
and an image built before the parse stopped echoing it prints that key: `internal/cli/exec.go`'s
`runExec` calls `runner.BackendFromEnv(os.LookupEnv, platform.Local())` at bootstrap,
`internal/runner/budget_session.go`'s marker error formats the value with `%q`, and the failure is
recorded as the run's `infra_error` — all of it **before any redactor exists** (§7.4). So the fleet
must first carry a runner that (a) refuses to echo the value and (b) registers the key with its
redactor as soon as it holds it, and **only then** may the launcher start sealing: **runner and image
first, launcher second, in that order and in separate deployments.** A gate is the alternative and
is acceptable to the owner as well; the repository does not need a new one, because it has this
shape already — `gcpProjectFieldSince` (`internal/cli/init_fugaroyaml.go`) with `imagePredates`
compares the image build record's `FugaroVersion` (`internal/imagecheck/record.go`) against a
`*Since` constant, records `reasonRecordVersionOld` when the image predates it, and
`checkCheckImageKnowsGitSecretID` (`internal/cli/init.go`) is the check that reads it; plan P8's
"jobs still carry the old image" message is the same idea in prose. Whichever form it takes, it is
two ordered halves of the seal's own deployment, and neither half is a Terraform change.

**Which release the seal ships in is an owner decision, and not a ruling of the 0.8.0 split.** Left
unsaid until the release ruling of 2026-10-15 made it a question, it is now stated here and in the
plan (Phase 3b) so the two agree: **the seal ships with phase 3, in 0.9.0 — unless the owner pulls
Slice A earlier.** *Slice A is the runner's half* of §7.1 above: the runner that refuses to echo the
marker's value, registers the key with its redactor as soon as it holds it, unseals before
`checkNames`, and parses the value strictly in `BackendFromEnv`. It is **runner-first and safe to
release on its own**, which is why it is the only half that may come earlier: the envelope's version
byte keeps a plaintext object from a launcher that does not seal yet readable, and no grant changes
with it. **The launcher-seals half** (the mint writing a sealed object, and the marker's value
becoming the key) **and the `tokens/` move** — with its prefix condition, in the same change as the
grant that reads it, which the merge made in Slice B rather than leaving to Task 3.2 (§7.1, per D18
and D19 above) — are **not earlier than 0.9.0**.

## The check outline: Check 60, owner-run, sandbox only

None of this is run by this task, and none of it may be run by an agent: every owner-run step in the
plan is the owner's (Global Constraints, "Every owner-run step … no agent performs it"), and the
`belong` installation is never touched. Checks 48 to 56 are already named by the plan's phases, and
the numbers above them are taken: **57 by PR #375**, the compute-instance backend, **merged** —
`main` carries its summary row, and its 57a and 57b sections live in
`docs/design/compute-backend.md` §7.2, which is a number taken even when the section is not in the
checklist; **58 by PR #393**, the artifact-runs design, **merged**, whose §11 writes the
reservation and records the duplicate's resolution (`docs/design/artifact-runs.md`, **on `main`** —
#393 and #401 are `main` PRs and this branch is `next`'s, so the two files are read there); and
**59 by PR #401**, the desktop-console design, **merged**, which takes 59 as **59a and 59b** — its
own plan's table says why (`docs/plans/2026-10-15-desktop-console.md`, on `main` too: "#393 takes 58
and merges first; #393 does not use 59, which leaves 59 free for this plan") — so
#401 is the later writer and renumbers itself off 58, which is the register's own rule (a number
belongs to the check that first wrote a row, and a duplicate is fixed by the later writer — the rule
the plan already states for #359's 45 and 46). Both of those PRs are merged and neither has written
its row yet — each plan says its own docs task writes the row and the section when its phase ships —
so `main`'s summary table ends at 57 and the two reservations are read from the two plans rather
than from a grep for `## Check 58`. **That leaves 60 as the first free number**, and this outline
proposes **Check 60** for the later implementation task, which is the task that would write it into
`docs/gcp-live-checklist.md` in Check 43's block form, with the guard block, the FACT lines and the
restore step that shape requires.

Before anything: **Check 52 has not been run**, and two of its step 3 (U2) facts bear directly on
this document. C2 depends on its step 4 (the U3 Secret Manager IAM condition probe), and D14's
answer does not depend on it — the recommendation uses neither Secret Manager nor a new condition.
If the owner runs Check 52 and step 4 fails, C2 is closed (it would need a new launcher permission
and a revoke step nobody owns) and this document's recommendation is unaffected; if step 4 succeeds,
C2 becomes viable *as C3's design in a different store*, and a later design could revisit it. Step
3's two facts are priced here as UNVERIFIED (§3.1, §7.1): whether a **secret reference** works
inside a container env override — the alternative of carrying the key as a `valueSource.secretKeyRef`
rather than a literal, which is not the recommendation because a shared job account reading a secret
is C2's problem in another store — and whether an execution's own **overrides are visible to the
container** (design §4.2's D10 option (b)). Neither changes the recommendation, and both bound what
the later implementation may assert; Check 60 step 3 is where the owner records them again on the
deployed shape. The audit-log half of the audience is *documented* rather than unverified, but it is
not thereby measured, and Check 60 step 4 is where the owner records what the RunJob audit entry
actually carries — the sentence constraint 1 rests on, checked rather than cited.

What Check 60 would have to prove, in the sandbox installation only, with the `belong` installation
untouched:

1. **A sealed token still works.** A launch of a sandbox repository with the budget on: the run
   bootstraps, takes its token, exchanges it and reads the project's policy. The run's log shows no
   key and no token (`fugaro logs`, and the run's outcome record).
2. **The object is worthless without the key.** With the owner's own credentials, read the token
   object out of the runs bucket: it is not a JWT, it does not parse as one, and it carries no
   readable claim. Record the object's size and the fact that `fs`/`fr` are not in it.
3. **The key is not in the bucket, and not in the logs.** Reads of the job's own environment
   (`gcloud run jobs describe`, the v1 path Check 52 step 1 uses; and, on an installation with the
   compute backend, `gcloud batch jobs describe` of a Fugaro Batch job, which must show no key
   because a sealed launch is refused on Batch) and of the execution's overrides
   (`gcloud run jobs executions describe`, Check 52 step 3's own read) show no key, and neither does
   the run's own log (`fugaro logs`). **The execution's record is a FACT, not a fact to assume**
   (Check 52 step 3, §3.1): U2 — the design's D10 option (b), whether the API serves an execution's
   own override back at all — is UNVERIFIED and unprobed, so this step **records what the execution
   record shows of the override and does not assert it**. **Two FACTs of U2 belong here**: whether an
   execution's own overrides are visible back through the API at all, and what the execution record
   shows of them. The sentence this step leaves behind is the answer as found, and §3.1 and §7.1 are
   read as hypotheses until it is recorded.
4. **What the audit log actually records.** The documented half of constraint 1 is that a `jobs.run`
   request body reaches audit logs a project log viewer can read (`m9-budget-and-dashboard.md` §6.4).
   This step is where that is confirmed on the deployed shape rather than cited: launch a sandbox run
   with the marker set, read the **RunJob audit entry** for that launch, and record as a FACT whether
   the entry carries the override's *value* — the key — verbatim, in a redacted form, or not at all,
   and who can read that entry. The answer decides how §3.1's and §7.1's audience sentences are
   worded, and it is the step that would show the key needs a redaction requirement of its own. It
   does **not** decide whether the seal is right: the seal's subject is the object a peer can read,
   and this step's subject is the carrier's audience.
5. **A wrong key refuses.** Hand a second execution a different key (a sandbox-only override) and
   watch it refuse at bootstrap with the shipped refusal text, exit code and outcome; this is the
   `MalformedEnv`/unseal path of 7.4.
6. **A plaintext object refuses.** Put a plaintext token object at the run's key and watch the run
   refuse rather than read it — the mixed-deployment case of 7.4, and the mutation that proves the
   refusal is real.
7. **A peer cannot delete a queued token.** With the `tokens/` grant read-only, have a run of the same
   job attempt a delete of a queued peer's object: 403. Record it the way live check 7 records the
   listing denial, and record the two things it does *not* prove: the peer can still read the object,
   and the seal is why that read is worth nothing.
8. **The grants the move changes, and the ones it does not.** The job's conditions on `runs/`,
   `cache/` and `locks/` are byte-identical to what the bootstrap set. The launchers' grant is **not**
   unchanged, and this step records the change instead of asserting it away: read the plan's diff for
   `google_storage_bucket_iam_member.runs_launcher` and record the added or widened `tokens/`
   condition — its title and its expression, the ones `internal/backend/gcp/names.go` builds, which
   the member's `precondition` in `deploy/terraform/gcp/modules/installation/iam.tf` must now match —
   beside the FACT that the job's three conditions are untouched in the same plan. Constraint 2 is
   read off the live plan, not by inspection of the module source.
9. **A sideways replay refuses, and what that does and does not prove.** Copy a sealed object from one
   run's key to another run's key in the sandbox bucket and watch the second run refuse rather than
   unseal it; then put the bytes back. The copy is made with the operator's own credentials, which
   after the move are the only ones with a write on `tokens/`: a peer *run* could make this copy only
   in a tree that still kept the object under `runs/`, the seal-only step D19 priced and the merge
   did not ship (§7.1, D16).
   **This step proves the per-launch key, not the AAD.** The copy
   is sealed under a different launch's key, so it fails at the unseal before the AAD is ever
   consulted — the refusal would be identical if the AAD were the empty string. The AAD's own property
   (a blob sealed for one name cannot be opened under another) is a unit test in `internal/budget/token`,
   named in §7.1, because no live step can isolate it: every live path to it fails earlier for the
   key's reason. Record that limit beside the FACT, the way step 7 records what its 403 does not prove.

## What this document does not decide

It does not decide phase 3's grant (Task 3.2's), the D13 paragraph's final wording (a later task's,
against Check 60's evidence), the budget's own model (that is `m9-budget-and-dashboard.md`), or
whether a future phase gives each run its own workload identity, which would make all of this
unnecessary. It decides what the channel should become if the owner wants the theft closed, and it
says plainly that nothing is built until the owner says so.
