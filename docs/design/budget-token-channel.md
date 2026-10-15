# The budget token's channel

**Status: design only, awaiting the owner's approval (2026-10-15).** Phase 3b, Task 3b.1 of
[the project-centric plan](../plans/2026-10-14-project-centric.md). SECURITY-SENSITIVE: it is
flagged for an independent review, and it decides nothing by itself. This is an approval-gated
design, and the gate is real: no code follows this document until the owner approves it — no
change under `internal/`, no Terraform, no config, no live action, no secret handled. Its one red
step is the documentation test beside it,
`internal/cli/docs_budget_token_channel_test.go`, which fails until this file and the
[SECURITY.md](../../SECURITY.md) pointer exist.

This document exists because of D13. Phase 3 puts one Cloud Run job — and one service account —
behind every repository of a profile (design §4.1; plan Task 3.2), which widens that account's
bucket grant from one repository's prefixes to a whole prefix (plan Task 3.2 step 2). Any run of
that job can then read, and delete, any other run's budget token. D13 accepted that exposure and
sent it to SECURITY.md's own words beside D4; phase 3b designs the channel that replaces it. One
half of D13 stands whatever this document decides, and the recommendation below does not touch it:
inside a shared job the profile is the isolation boundary.

Every symbol below was read on `next` on 2026-10-15 and is cited by name rather than by line
number, because the lines move.

## 1. The channel as it is

Five steps, all of them shipped:

| step | where |
| --- | --- |
| **Mint** | `internal/cli/launch_budget.go`: `mintBudgetToken` (the decision is `budgetMintOn`) mints `token.Claims{Slug, Run, FX, FP, RB}` with `token.Mint` through `token.IAMSigner` built by `tokenSigner` |
| **Deliver** | `internal/budget/token/object.go`: `PutObject` writes the custom token at `ObjectKey(slug, run)` = `runs/<slug>/<run>/budget-token`, create-if-absent (`ErrObjectExists`); the runs bucket's lifecycle rule deletes an untaken one after a day (`deploy/terraform/gcp/modules/installation/bucket.tf`, `google_storage_bucket.runs`); `DeleteObject` removes it after a launch that did not start (`dropBudgetToken`) |
| **Take** | `internal/runner/projectpolicy.go`: `takeBudgetToken` → `token.TakeObject`, which reads the object, **deletes it**, and refuses one whose own `fs`/`fr` claims are not the names the read used (`checkNames`). `budgetClient` takes exactly once per run and shares the result with the budget session |
| **Exchange** | `internal/budget/token/token.go`: `Exchange` turns the custom token into an ID token; its claims, `fs`, `fr`, `fx`, `fp`, `rb` and the uid `UID`, are the run's database identity, and they are what the rules test (`internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`, e.g. `auth.token.fs == $slug && auth.token.fr == $run`) |
| **Mark** | `internal/cli/run.go` sets `lspec.Env = []string{runner.BudgetTokenEnv + "=1"}`; `internal/backend/backend.go`'s `LaunchSpec.Env` carries it into the execution's container override (`internal/backend/gcp/run.go`, `Launch`), and `internal/runner/budget_session.go`'s `BackendFromEnv` parses it |

Two properties of that channel are load-bearing and easy to lose in a redesign:

- **The token is not in the override.** The override carries one bit. The credential itself is a
  bucket object the launcher writes and one run reads. Design §4.2 states the rule for the token
  ("The budget token deliberately does not travel as an override"); `mintBudgetToken`'s own header
  says the token is never in the launch request, an env var, an error or a message.
- **The take is the consume.** `TakeObject` deletes as it reads, so a token can be used once, and a
  run that finds none where its own marker says one was minted refuses to start rather than run
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
   where the provider must not). The reason is concrete: an override is part of the execution's own
   specification, which the service keeps and serves back — the CLI reads a job's whole environment
   through that API (`internal/backend/gcp/run.go`, `JobFullEnv`), and an execution's record carries
   the overrides it was started with — and the account that may set
   overrides holds `run.jobs.run` and `run.jobs.runWithOverrides`
   (`deploy/terraform/gcp/modules/installation/iam.tf`, `google_project_iam_custom_role.job_runner`;
   `deploy/terraform/gcp/modules/workflow/job.tf`, `google_cloud_run_v2_job_iam_member.launcher`, whose
   own comment is "Launchers start runs with overrides, on this job only").
2. **Never widen a service account's grants beyond its prefix.** Every grant is a member resource,
   never a binding or a policy (`deploy/terraform/gcp/modules/installation/iam.tf` says so at the top
   of the file), and a job's is `roles/storage.objectUser` restricted by a condition that names its
   own prefixes (`deploy/terraform/gcp/modules/workflow/sa.tf`,
   `google_storage_bucket_iam_member.job`). A design that needs the run-time creation of IAM bindings,
   or a bucket-wide role, breaks this and reverses 0.7.0's hardening.
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
  from this path and which no Fugaro account holds for the runs bucket today (the launcher's
  unconditional grant there is `roles/storage.objectAdmin`, an object-level role;
  `deploy/terraform/gcp/modules/installation/iam.tf`, `google_storage_bucket_iam_member.runs`).
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
  is distinguishable and a plaintext object is distinguishable from a sealed one.
- The key travels in the override the launcher already writes. `internal/cli/run.go`'s
  `lspec.Env = []string{runner.BudgetTokenEnv + "=1"}` becomes a marker whose *value* is the
  execution's key; no new variable name, no new permission, no new resource.
- The runner's `takeBudgetToken` unseals before `checkNames`, and `BackendFromEnv` parses the value:
  a present value that cannot be parsed is `MalformedEnv` and stops the run, exactly as today.

Why this satisfies §4: the peer's reach is unchanged — it can still read the object — and what it
reads is ciphertext under a key that was delivered to this execution and no other. The key is not in
the bucket, and a job account holds no permission on the Cloud Run API from which an execution's
override could be read back (constraint 5: a *launcher* can, and could already mint anything).

This is also where the design's tension is, and it should be named rather than buried: the seal
moves a secret into the one carrier constraint 1 distrusts. The answer is what the carrier's
audience is. An override is readable by whoever may launch — the same set that holds `signJwt` on
the signer and can already mint a budget identity for any repository (SECURITY.md's own paragraph),
who already holds the job's own credentials under D10 — and it is not readable by a peer run, which
is the set the threat model is about. The design's own rule, "the token does not travel as an
override", is about the *token*: a bearer credential whose theft is the whole attack. A per-execution
key whose audience can already mint is a different object, and the honest way to keep the rule is to
say precisely that instead of widening it in silence. If the owner would rather not carry a secret
in an override at all, D15's second option (a strictly parsed variable) changes the name and not the
audience. One property is worth stating because it is what makes the carrier safe in the meantime:
overriding the key cannot widen anything. The object's bytes are sealed under the key generated by
the claim-winning launch that wrote it, so a different key, whoever set it, unseals nothing — it only
makes this execution's take fail, which is this design's refusal and not a grant. Constraint 1's rule
is kept where it still has teeth: what a run must never accept is a *credential* from an override,
and the key is not one; the token stays in the bucket object it has always been in.

This is a **capability, not a boundary**: it removes the value of what a peer can reach; it does not
narrow what a peer can reach. The distinction is the whole point of writing it down, because the
natural next question — "so can a peer read another run's token?" — has the honest answer "it can
read bytes that are not usable without a key it does not have", and not "no".

### 7.2 The move out of `runs/` (the part that stops the denial of service)

Today a peer can *delete* a queued run's token object, and the victim then refuses to start
(`budgetClient`'s `TokenMarked` branch is exactly that refusal). The seal does not change this, so
the second half of the recommendation moves the object:

- the token object lives under a `tokens/` prefix (`ObjectKey` becomes
  `tokens/<slug>/<run>/budget-token`), out of `runs/`, which is where the task spec, the launch
  record and the logs live;
- the job account's grant on that prefix is read-only (`roles/storage.objectViewer`-shaped, or the
  equivalent under the project module's per-prefix condition), so a peer cannot delete a queued
  token, and the launcher keeps the write and delete it needs (`PutObject`, `DeleteObject`,
  `dropBudgetToken`);
- the bucket's one-day lifecycle rule moves with it (the rule is scoped by
  `matches_prefix = ["runs/"]` and `matches_suffix = ["/budget-token"]`,
  `deploy/terraform/gcp/modules/installation/bucket.tf`), so an untaken token still dies;
- the runner's take stays a read-then-delete where its grant allows the delete and becomes
  read-and-leave-otherwise; either way, consume-once stops resting on the delete and rests on the
  key, which is delivered once, to one execution, because the launch claim was won once.

### 7.3 What this does not change

- D13's paragraph in SECURITY.md stays, narrowed to what it still describes: the *other* objects a
  shared job's account can read across repositories — task specs, launch records, logs, cache,
  locks. Inside a shared job the profile is the isolation boundary, and this document does not move
  it. If phase 3 lands with this design, the paragraph says the token is sealed and says why the
  rest is not.
- The mint, the claims, the rules, the exchange, the marker's *presence* semantics and the fail-closed
  parse are unchanged. The budget's model does not move.
- No new permission, no new resource, no new API. The seal is in `internal/budget/token`, the key is
  carried by a variable that already exists, and the prefix is one more entry in the list the project
  module already builds per prefix.

### 7.4 The failure paths, and why each of them refuses

| failure | behaviour |
| --- | --- |
| the marker is absent on a budget-on run | refuse (shipped: `budgetClient`'s `ErrNoToken` branch) |
| the marker is present and unparsable | refuse as damaged (shipped: `MalformedEnv`), and the parse must stop echoing the value — see below |
| the object is missing under a present marker | refuse (shipped, `TokenMarked`) |
| the object is sealed and the tag does not verify | refuse: an unsealed or tampered object is never read as a token, not even during a mixed deployment |
| the object is plaintext and the seal is expected | refuse, for the same reason: a fallback to plaintext is the whole exposure coming back quietly |
| the key is wrong because the execution is not the one the launcher minted for | the unseal fails and the run refuses; the take's `checkNames` still refuses a token whose own claims are not this run's |

One concrete requirement falls out of the second row and belongs in the later implementation:
`internal/runner/budget_session.go`'s `BackendFromEnv` currently words its marker error as
`fmt.Errorf("%s %q: it must be 1, ...", BudgetTokenEnv, marker)` — it echoes the value. Harmless while
the value is `"1"`; a leak of the run's own key into a bootstrap error, and from there into the run's
log, once the value is a key. The parse must not print it, and the launcher must register the key
with its redactor before anything can print it, the way `takeBudgetToken` already does for the token
itself (`r.addSecret(custom)`).

### 7.5 What the later implementation would touch (not this task)

`internal/cli/launch_budget.go` (`mintBudgetToken`, `dropBudgetToken`, `tokenSigner`),
`internal/budget/token/object.go` (`ObjectKey`, `PutObject`, `TakeObject`, `checkNames`),
`internal/runner/budget_session.go` (`BackendFromEnv`, `BudgetTokenEnv`, `TokenMarked`,
`MalformedEnv`) and `internal/runner/projectpolicy.go` (`takeBudgetToken`), `internal/cli/run.go`
(the marker assignment), `internal/backend/backend.go` (`LaunchSpec.Env`'s documentation of what the
value is), the bucket IAM in `deploy/terraform/gcp/modules/workflow/sa.tf` and the project module
Task 3.2 creates, the lifecycle rule in `deploy/terraform/gcp/modules/installation/bucket.tf`, and
the D13 paragraph in SECURITY.md.

## The owner's open decisions

These continue the numbering of the project-centric series — D1 to D12 in
`docs/design/project-centric.md`, D13 in the plan. They are **this document's own D14 to D19** and
are unrelated to the D14 to D18 of `docs/design/m9-budget-and-dashboard.md`, which governs the
budget's own behaviour and not this channel.

**D14. Is the seal built at all, or is D13's documentation the permanent answer?** Phase 3b's
deliverable is this design, and D13 remains in force until something replaces it. If the owner
answers "documented is enough", this document becomes the record of a considered alternative and
nothing more. The recommendation is to build it, before or with phase 3 (see D19).

**D15. Where does the key travel?** Recommended: in the existing marker's value
(`FUGARO_BUDGET_TOKEN=<key>`), because no new name has to stay in step between the launcher, the
runner, the docs and the tests, and the existing fail-closed parse can be tightened in place. The
alternative is a second, strictly parsed variable (a new name in `BackendFromEnv`, a new line in
`LaunchSpec.Env`, a new row in SECURITY.md and in the doc tests). A third option — a key object in
the bucket beside the token — is self-defeating and is listed only to be struck out. **Note the
nature change either way:** the marker stops being a one-bit marker and becomes a secret, which
SECURITY.md's description of it must follow, and which the `%q` above makes concrete.

**D16. Does `tokens/` become a fourth granted prefix, at which role?** Read-only for the job account
is what makes a peer unable to delete a queued token (7.2); it also means the runner no longer
deletes on take, so consume-once rests entirely on the key. Keeping `objectUser` on `tokens/` keeps
the code path and the shipped behaviour and leaves the denial of service in place. The
recommendation is read-only, and the decision is the owner's because it is a Terraform and a
runner-semantics change at once.

**D17. The seal's lifetime and the re-mint path.** A `--retry` re-mints and re-puts the object
(`mintBudgetToken` deletes the earlier attempt's before writing), so it must re-seal and re-hand the
key to the new execution; the object's one-day lifecycle rule and the token's own one-hour claim
(`token.ExpiryFX`, with `Slack`) are unchanged. Deciding this now avoids a later implementation
choosing "seal once, reuse the key", which would put one key in front of two executions of the same
run.

**D18. The envelope's shape and where it lives.** Recommended: a versioned sealed envelope defined
in `internal/budget/token` beside `PutObject`/`TakeObject` (one package owns both halves of the
channel), with the format's version as the envelope's first byte so a later format is refusable
rather than misread, and with a plaintext object treated as a refusal once the launcher seals.
A generic helper package is the alternative; it would be the first generic sealing code in the tree
and it would put the format's compatibility rules away from their only caller.

**D19. Ordering against phase 3.** Recommended: the seal ships on its own first, while the
per-repository conditions are still what protects the object, because its tests are unit tests in
`internal/budget/token` and its only live effect is a launch and a take on an unchanged grant; the
`tokens/` move and the prefix condition then land with Task 3.2, when the grant is what changes. The
alternative — one change with phase 3 — means the exposure D13 accepts is never actually closed for
a window in which both the grant and the channel move at once. The owner decides, and this decision
is the one a reviewer should look at hardest.

## The check outline: Check 57, owner-run, sandbox only

None of this is run by this task, and none of it may be run by an agent: every owner-run step in the
plan is the owner's (Global Constraints, "Every owner-run step … no agent performs it"), and the
`belong` installation is never touched. Checks 49 to 56 are already named by the plan's phases; this
outline proposes **Check 57** for the later implementation task, which is the task that would write
it into `docs/gcp-live-checklist.md` in Check 43's block form, with the guard block, the FACT lines
and the restore step that shape requires.

Before anything: **Check 52 has not been run.** C2 depends on its step 4 (the U3 Secret Manager IAM
condition probe), and D14's answer does not depend on it — the recommendation uses neither Secret
Manager nor a new condition. If the owner runs Check 52 and step 4 fails, C2 is closed (it would need
a new launcher permission and a revoke step nobody owns) and this document's recommendation is
unaffected; if step 4 succeeds, C2 becomes viable *as C3's design in a different store*, and a later
design could revisit it. Say which candidates depend on the probe, and record the probe's answer
either way.

What Check 57 would have to prove, in the sandbox installation only, with the `belong` installation
untouched:

1. **A sealed token still works.** A launch of a sandbox repository with the budget on: the run
   bootstraps, takes its token, exchanges it and reads the project's policy. The run's log shows no
   key and no token (`fugaro logs`, and the run's outcome record).
2. **The object is worthless without the key.** With the owner's own credentials, read the token
   object out of the runs bucket: it is not a JWT, it does not parse as one, and it carries no
   readable claim. Record the object's size and the fact that `fs`/`fr` are not in it.
3. **The key is not in the bucket, and not in the logs.** `fugaro env`-style reads of the job show no
   key; the execution's own record shows the override, as D10 says it will, which is the honest limit
   to record beside the fact.
4. **A wrong key refuses.** Hand a second execution a different key (a sandbox-only override) and
   watch it refuse at bootstrap with the shipped refusal text, exit code and outcome; this is the
   `MalformedEnv`/unseal path of 7.4.
5. **A plaintext object refuses.** Put a plaintext token object at the run's key and watch the run
   refuse rather than read it — the mixed-deployment case of 7.4, and the mutation that proves the
   refusal is real.
6. **A peer cannot delete a queued token.** With the `tokens/` grant read-only, have a run of the same
   job attempt a delete of a queued peer's object: 403. Record it the way live check 7 records the
   listing denial, and record the two things it does *not* prove: the peer can still read the object,
   and the seal is why that read is worth nothing.
7. **The 0.7.0 grant is unchanged where it still applies.** The job's conditions on `runs/`, `cache/`
   and `locks/` are byte-identical to what the bootstrap set, and the launchers' condition title and
   expression still pass their `precondition` (constraint 2, checked on the live plan, not by
   inspection).

## What this document does not decide

It does not decide phase 3's grant (Task 3.2's), the D13 paragraph's final wording (a later task's,
against Check 57's evidence), the budget's own model (that is `m9-budget-and-dashboard.md`), or
whether a future phase gives each run its own workload identity, which would make all of this
unnecessary. It decides what the channel should become if the owner wants the theft closed, and it
says plainly that nothing is built until the owner says so.
