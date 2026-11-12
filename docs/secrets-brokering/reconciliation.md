# Secrets and credential brokering: reconciliation (draft)

**Status: reconciliation answered (D497a amended after review, O5); plan written; no task approved.**
This document is the reading of 2026-10-27 and is kept as written: much of it has since been built —
[status-2026-11-07.md](status-2026-11-07.md) is the census of 2026-11-07 and says task by task what
is on `main`, what remains and what can launch now. The
owner answered D490–D506 on 2026-10-27 (§5): all as recommended except **D501** (the service keeps
scaling to zero: not the recommendation; D502, staying opt-in, was relayed with it). **D494** is the
recommendation and **departs from the owner's spec** (no RTDB token store). The plan is
[implementation-plan.md](implementation-plan.md). Answering a decision approves no task.
**2026-11-10:** §10 reads the later slices S2–S5 against the tree: all four are kept, two pieces are
subsumed by other plans (DS3.5, MC6), and the owner answered **D2070–D2080** on 2026-11-10, all as
recommended; the owner's 2026-11-10 "approve all tasks" ruling approves tasks B2.1–B5.9, each
keeping its own gate.

**Checked against:** `origin/main` at `db4c5f390`, on **2026-10-27**, then merged forward to
`922a9cc74`, which carries #1167 (run dc73, the `allow_data_to` segment wildcard, merged); #907 (the
App key's mount drop) is still open. `graphify` has
no graph for this repository, so every claim was located with `git grep` and read in place. Paths
and symbols are cited, never line numbers. Where this document disagrees with
[spec.md](spec.md), this document wins.

## 1. Summary

The spec asks for one rule (only the credentials service reads Secret Manager), a Bitbucket proxy
inside that service so a workspace token never leaves it, an RTDB-backed proxy token, model-key
proxying, availability work and new `fugaro doctor` checks.

Read against the repository, **about half of it exists**:

- The credentials service is built and merged (`internal/credsvc`, `internal/creds`,
  `fugaro credentials serve`), ships **dormant** behind an owner-run opt-in, authenticates callers
  with Google-signed ID tokens plus the run's own identity, holds its credentials in one bundle
  secret, audits every act by name, proxies model calls for `cascade` runs and mints GitHub
  installation tokens.
- The in-job gateway (`internal/gateway`) already keeps model keys out of the agent's own
  environment and, with the service on, out of the job for `cascade` calls.
- RTDB SSE streaming (`internal/rtdb/stream.go`) and a sweeper (`internal/budget/sweep.go`) exist.

What is **new** is the Bitbucket git and REST proxy, the "only the service reads Secret Manager"
end state for every class of secret, and the doctor checks of §7.

**The headline correction:** Fugaro has **no agent sandbox**. The agent runs as the runner's own
user in the same container. It can read the runner's `/proc` and reach the job's service account
through the metadata server (SECURITY.md, "What one run's agent can reach"). So "the agent never
calls the credential service" cannot be enforced, and the agent can do anything the runner can.
The proxy's real value is different: the **real Bitbucket token leaves the job**, and what a run can
do with Bitbucket is narrowed by the proxy's per-request policy to one repository and one branch,
for the run's life. That is a large gain over today, where the agent holds the token's own value
(F7 of [../bitbucket-workspace-token/reconciliation.md](../bitbucket-workspace-token/reconciliation.md)).

**The second correction:** the spec's proxy token (an opaque token in RTDB, cached, invalidated by
a subscription, swept) duplicates the per-run identity the service **already** verifies (D134:
the job service account's ID token plus the run's Firebase identity, revoked by the run's
write-once outcome). Reusing it removes the token store, the subscription and the sweep from the
first slice (D494).

**The size:** the Bitbucket proxy is a large, security-sensitive build. It is a git smart-HTTP
forward with pkt-line parsing for branch rules, a REST allow-list, a runner-side forwarder, a new
run-identity claim, Terraform and two or more owner-run live checks. It is not a 0.12.1 item. It
competes with 0.13.0's planned Bitbucket shared-job work (D460–D477), which it partly supersedes
(§6).

## 2. What the spec names, and what the repository already has

| Spec concept | What exists | Where | New? |
| --- | --- | --- | --- |
| A credential service holding secrets | The credentials service, a Cloud Run service in the installation root, dormant unless the owner runs `fugaro update --provision-credentials` | `internal/credsvc` (`Server`, `NewServer`), `internal/cli/credentials_serve.go`, `deploy/terraform/gcp/modules/installation/credentials.tf`, `images/credentials/` | Exists |
| Its secrets in Secret Manager, only it reads them | **One** bundle secret, one `secretmanager.secretAccessor` grant to the service's account (Q6, 2026-10-19); written by `fugaro creds set`, listed by `fugaro creds ls` | `internal/credsvc/bundle.go` (`BundleCacheTTL` 30 s), `credentials.tf` `google_secret_manager_secret_iam_member.credentials_bundle`, `internal/cli/creds.go` | Exists, for model credentials and the App key only |
| Callers authenticate with workload identity | Cloud Run IAM invoker (only job accounts hold it), plus the service's own check of the Google ID token's signature and `email` against the installation's per-repository job accounts, plus the run's Firebase identity (`uid`, `fp`, `fx`) and liveness from the write-once outcome | `internal/credsvc/auth.go` (`Authenticator`, `AuthConfig.JobKeys`, `AuthConfig.SharedJobAccounts`), `server.go` `JobAccountsFor` | Exists |
| The runner fetches credentials on the agent's behalf | The gateway forwards the agent's model calls to the service (D135); the runner fetches GitHub installation tokens from the service (C2.2) | `internal/gateway`, `internal/gitprov/providers/providers.go` `FromEnv` (`ServiceHeldKey`), `internal/credsvc/github.go` | Exists |
| GitHub: short-lived repo-scoped installation tokens | Minted at the service when the installation opted in (C2.1, C2.2 merged); in-job otherwise. The job **still mounts** the App key: `config.ServiceHeldSecretNames` is empty and the flip (#907) waits on Check 96 | `internal/credsvc/github.go`, `internal/config/providers.go` `ServiceHeldSecretNames`, `internal/gitprov/github/apptoken.go` | Exists, flip pending |
| Bitbucket proxy | Nothing. The parked "push and pull-request proxy", option (d) of D363 in docs/bitbucket-credentials, is exactly this | `docs/bitbucket-credentials/reconciliation.md` D363 | New |
| Proxy token in RTDB | Nothing of that shape. The run identity plays the role (D134) | `internal/budget/token`, `internal/credsvc/auth.go` | Duplicate (D494) |
| RTDB subscription | An SSE stream reader exists in the RTDB client | `internal/rtdb/stream.go` | Exists |
| Periodic sweep | The history job's `--sweep` mode writes outcomes for dead runs | `internal/budget/sweep.go` | Exists |
| Audit log | One line per issuance, probe and verified refusal, names only | `internal/credsvc/audit.go`, `.claude/rules/credentials-service.md` | Exists for model calls and mints |
| Model keys out of the sandbox | The gateway (agent gets a per-run gateway token and a loopback URL); with the service on, the job mounts no model credential | `internal/gateway`, `internal/runner/provision.go`, SECURITY.md "The credentials service" | Exists, partly wired (F9) |
| Stop issuing / revoke | The kill switches (`config/kill/global`, `config/kill/repos/$slug`) stop the service; a run's outcome ends its identity | `internal/credsvc/server.go` `RTDBKills`, SECURITY.md | Exists, per repository not per run |
| Doctor credential checks | `creds-broken`, `creds-unknown-state`, `project-secret-extra-accessors` (live `secretAccessor` read on project-wide containers), `project-secret-stored`, `secret-scope`, `--check-github-app` | `internal/cli/doctor.go` | Exists, partly (F20) |

## 3. Who reads Secret Manager today (the audit §1 asks for)

| Reader | What it reads | How | Where |
| --- | --- | --- | --- |
| Per-repository job accounts (one per repository, workflow and Claude account variant) | The git credential (`bitbucket-token` or `github-app-key`), model credentials (`claude-oauth-token*`, `anthropic-api-key`, provider keys) **unless** the installation opted into the service, and the workflow's own `secrets:` | Cloud Run `secret_key_ref` env mounts, resolved by the platform as the job account at execution start | `modules/workflow/sa.tf` `google_secret_manager_secret_iam_member.job`, `modules/workflow/job.tf`, `internal/infra/spec.go` `SecretMounts` |
| Shared project job accounts (phase 3, GitHub profiles) | The App key, the profile's provider keys and Claude account token | Same | `modules/project/sa.tf`, `modules/project/job.tf` |
| The repository's build account | The git credential and the workflows' build secrets, during Cloud Build; the daily image-check job runs as it too | Cloud Build `availableSecrets`, BuildKit `--secret id=git-credentials`; the check job's `secret_key_ref` | `modules/repo/build.tf`, `modules/repo/check.tf`, `images/derived/cloudbuild.yaml`, `internal/cli/gitcred.go` (`fugaro image git-credential`), `internal/cli/imagecheck.go` |
| The coordinator job's account | The GitHub App key | `secret_key_ref` | `modules/installation/coordinator.tf` `google_secret_manager_secret_iam_member.coordinator_app_key` |
| Batch VM accounts (the compute backend, when selected) | The workflow's secrets | Batch `secretVariables`, resolved as the VM's account | `internal/backend/batch` `Options.Secrets`, `checkSecrets` |
| The build request built in Go | The git credential and each workflow build secret added to `availableSecrets` | Cloud Build, as the build account | `internal/backend/gcp/build.go` |
| The credentials service's account | The bundle | Secret Manager API | `credentials.tf`, `internal/credsvc/bundle.go` |
| The owner, on their machine | The bundle (read-modify-write); the App key for `doctor --check-github-app` | Their own credentials | `internal/cli/creds.go`, `internal/cli/doctor.go` |
| Operators | Add versions, list metadata; never read a value | `secretVersionAdder` and `viewer` | `modules/repo/secrets.tf`, `modules/installation/secrets.tf` |
| Launchers | List names, labels and version counts | `fugaroLauncher` custom role | `modules/installation/iam.tf` |
| Managed projects' accounts (`fmgd-`) | `fmgd-` secrets a managed plan creates and grants | The managed deployer holds `secretmanager.secrets.setIamPolicy` on its own prefix | `modules/installation/deployer.tf` |
| Project Owners | Anything: `roles/owner` carries `secretmanager.versions.access` and bypasses secret-level policy | Project IAM, outside every grant Fugaro writes | `SECURITY.md` "The credentials service" |

So **six runtime account classes** (the five above and the Batch VM account when that backend is
selected; the Go-built build request is how the build account reads) read secret values today, not one, plus humans. Each per-repository
job is its own account, so an installation with N repositories and W workflows has at least N×W
job accounts holding `secretAccessor`.

## 4. Findings

**F1, Exists.** The credentials service, its caller authentication and its runner-side fetch exist
(§2). Spec §2's first bullet is today's design (D135, C1.10). *Consequence:* the proxy is a third
route of an existing service, not a new service. It inherits `Authenticator`, the bundle, the audit
log, the kill switches and the "no admin routes" rule.

**F2, Wrong.** "It is the only component with access to Secret Manager." Six runtime account
classes read values today (§3), and the service itself reads only model credentials and the App
key (D145). *Consequence:* the principle is an end state reached class by class, each with a
breaking IAM change (D500).

**F3, Rule.** D148 (2026-10-17) already ruled the wording: "the only Fugaro runtime identity with
access to model credentials", naming the others. Humans can never be excluded: Project Owners
bypass secret-level IAM, the owner reads the bundle to write it (Q6's accepted cost), and
operators add versions. *Consequence:* the spec's §1 bullet "remove Secret Manager access from every
service account except the credential service's" is achievable for **service accounts**; SECURITY.md
must keep saying that humans are outside it.

**F3, corrected 2026-10-29 (owner's ruling, B0.3 follow-up; widened by the independent review of
2026-10-29).** Owners only. `roles/editor` carries `secretmanager.secrets.getIamPolicy` and delete
but not `secretmanager.versions.access`, so an Editor is not a reader of every secret and reads one
**directly** only through a `secretAccessor` grant a container's own policy gives it — a grant the
per-container sweep reports like any other, and one that naming Editors as readers would hide. It
reads one **indirectly** as well: `roles/editor` also carries `iam.serviceAccounts.actAs` and
`run.services.update`, so an Editor can deploy code that runs as an account holding a grant (the
`credentials-service-deployers` line names the Editor as a deployer for exactly this reason). This
finding, §3's table, the audit's E12 row, SECURITY.md and `website/content/security.md` are corrected
together; `fugaro doctor` was right all along
(`TestDoctorSecretReadersDoesNotCallAnEditorAReader`).

**F4, Rule.** D145 (2026-10-17) kept two classes out of the service on purpose: Bitbucket tokens
("cannot be minted per job") and workflow `secrets:` ("the code under test needs them in its env",
`internal/runner/runner.go` `secretEnvs`). The spec reverses both. The Bitbucket half is D145's own
"proxied push/PR creation: later, optional". The workflow half gains **no reach reduction**: a
secret the code under test reads must be a plain value in the job, so the service can only become
the place the runner fetches it from at bootstrap ("vault mode", D363(b) in
docs/bitbucket-credentials). The spec accepts that ("even where an exception would bring no
benefit"). *Consequence:* D491.

**F5, Wrong.** "The agent sandbox must never call the credential service directly." There is no
sandbox. The agent runs as the runner's user in one container. It can read `/proc/<runner>/environ`
(the run's sealed-token key, any token the runner holds) and the metadata server (the job account's
ID token). With both, it can call the service exactly as the runner does (SECURITY.md "What one
run's agent can reach", "The launch's own agent is outside the seal"). The same holds on the Batch
backend, where the metadata server is reachable and Docker is root. *Consequence:* every
per-request rule of the proxy binds **the run**, not the runner as distinct from the agent. The
rule "the runner fetches on the agent's behalf" stays as hygiene (it keeps tokens out of the
agent's own environment and transcripts), and the security claims must say the agent can act as the
run (T1).

**F6, Exists, with a nuance.** "The proxy must not be built into the CLI." The service **is** the
`fugaro` binary (`fugaro credentials serve`, `images/credentials/`), deployed as a separate Cloud
Run service with its own account. The spec's intent (the real token is never in a process the agent
can read) holds for that shape. The existing model path also has an in-job forwarder, the gateway,
which holds no real credential once the service is on (D135). *Consequence:* a runner-side git
forwarder that holds no real token keeps the intent; whether it violates the spec's letter is D495.

**F7, Exists.** Who calls the Bitbucket REST API: the runner (`internal/runner/prflow.go`,
`reviewmode.go`, `runner.go`, through `bitbucket.Provider`'s `EnsurePR`, `Comment`, `Comments`,
`PullRequest`, `FetchPRHead`, `UpdatePR`, `ApplyReady`, `Repository`), the image-check job
(`internal/cli/imagecheck.go`, as the build account), and the coordinator only through
docs/bitbucket-credentials' read consumer (planned, not built: `Authenticator` has no coordinator
caller class). **The agent has no Bitbucket API client.** It gets git credentials only
(`gitops.CredentialVars`: `FUGARO_GIT_TOKEN`, `FUGARO_GIT_USERNAME` and a credential helper scoped
to the origin's host), and on Bitbucket no `GH_TOKEN` equivalent. *Consequence:* the REST half of
the proxy serves the runner, with a short endpoint allow-list (D496). The agent needs git only.

**F8, Exists.** The runner, not the agent, pushes. Checkpoint pushes and the final push are the
runner's (`internal/runner/checkpoint.go`, `prompts.go`: "Fugaro pushes each new commit"); a
follow-up's, review run's and artifact run's agent gets no git credential at all
(`run.refreshGitAuth`). The first run's agent credential is "a convenience, not a security
boundary" (docs/design/v1.md §6.1). Rewritten commits are pushed at the run's end, so the run's own
branch needs non-fast-forward pushes. *Consequence:* the proxy's push rule is "the run's own branch
only, force allowed there"; nothing else is pushed by a legitimate run.

**F9, Exists, partly.** Model keys (spec §5): with the gateway on, the agent's environment holds a
per-run gateway token and a loopback URL, and the real key is in the runner's environment, readable
through `/proc`. With the service on, a per-repository job mounts **no** model credential, but only
`cascade` calls are served: the other `agent.auth` modes are not wired, so such a job has no model
credential at all (credentials-service README, status of 2026-10-23). With budget off there is no
gateway and the key sits in the agent's environment (SECURITY.md "Budget mode defaults to off").
`auth: vertex` has no key: the job account holds `roles/aiplatform.user` through the metadata server.
Shared project jobs are refused by the service (Global Constraint 6) and keep their mounts.
*Consequence:* spec §5 is "finish the existing wiring", not a new mechanism (D498).

**F10, Gap.** Git through a proxy, mechanically. Git over HTTPS authenticates with HTTP Basic from a
credential helper; `http.<url>.extraHeader` adds arbitrary headers; `url.<base>.insteadOf` rewrites
a remote. Cloud Run IAM wants a Google ID token in `Authorization`, which Basic auth also uses.
Two shapes work:
(a) git talks to the service directly, with the ID token in Cloud Run's alternative IAM header
(`X-Serverless-Authorization`, UNVERIFIED U1) through `extraHeader`, and the run identity as the
Basic password; the runner rewrites both every refresh (ID tokens last one hour; the run identity is
refreshed by `token.Session`). A git command that outlives its token fails.
(b) git talks to a loopback forwarder in the runner, which attaches fresh tokens per request (the
gateway's pattern, D135). *Consequence:* D495. Either way `remoteHost` and the admission check in
`internal/runner` (`admissionCheck`, `remoteEndpoint`, `repoNamedByRemote`), which refuse a remote
on a host no provider owns, must learn the proxy's endpoint.

**F11, Gap.** Policing git per action. Fetch is `GET …/info/refs?service=git-upload-pack` then
`POST …/git-upload-pack`; push is `GET …/info/refs?service=git-receive-pack` then
`POST …/git-receive-pack`, whose body starts with pkt-line commands (`<old> <new> <ref>`, the first
carrying capabilities after a NUL) up to a flush packet, then the pack. *Feasible:* identify the
repository from the URL path, tell fetch from push by the service name, read only the command list
before streaming the pack on, and refuse any ref other than the run's branch, any ref deletion (new
id all zeros) and any tag. *Not feasible without the object graph:* telling a force-push from a
fast-forward on that branch (it needs the old commit's ancestry); it is not needed (F8). Push
options and protocol v2 for fetch pass through. Git LFS is a separate API and is out of scope
unless a repository uses it (UNVERIFIED whether any does, U6). The refusal reaches git as an HTTP
error; a `report-status` refusal per ref is nicer but not required.

**F12, Gap.** The run's branch is not bound to anything the service trusts. The run identity's
claims are `fs` (slug), `fr` (run), `fp` (project), `rb` (launcher) and `fx` (expiry)
(`internal/budget/token`). The branch lives in the task the launcher writes to the runs bucket,
which the service's account cannot read (its only grants are the bundle and the custom database
role). The run's own RTDB nodes are writable by the run (`agents/` is `RUN`-writable), so they bind
nothing. *Consequence:* the branch must be a **launcher-minted claim** (a new `fb` claim; launchers
are trusted, D475) or the push rule is repository-only. D497.

**F13, Gap.** Cloud Run limits on the proxy path (U2). A request's timeout is configurable to 60
minutes (`credentials.tf` sets `timeout = "3600s"`): a long clone or push binds one request, not a
run. A request body over **32 MiB on HTTP/1** is refused; git's large pushes are chunked POSTs, and
a chunked body above that limit is refused too — no longer UNVERIFIED: Check 190 (RUN 2026-10-30) got
a **413** for a 33 MiB pack, and its proxy saw four bytes of it. End-to-end HTTP/2 (h2c) lifts the
request limit but needs the container to serve h2c. Responses (a clone's pack) stream. Check 91,
which measures the HTTP/1.1 body limit, is written and **not run**. *Consequence:* a first live check
(Check 190) had to push a pack above 32 MiB through the proxy before the design commits to HTTP/1, and
it did: the design does
**not** commit to HTTP/1, and S1 serves h2c end to end (B1.0, B1.5).

**F14, Gap.** Blobless clones multiply requests. The runner clones with
`gitops.CloneBlobless`, so later commands fetch missing blobs lazily, each a request. Through the
proxy each is a Cloud Run request with an identity check (an RTDB read of the run's outcome, as
today for model calls) and an audit line. *Consequence:* the request count per run, not the latency
of one, is what to measure (Check 190); per-request RTDB reads are already the model path's cost.

**F15, Gap.** The service's availability today. One Cloud Run service, CPU `1`, memory `512Mi`, no
`min_instance_count` (scale to zero, so cold starts), no health route: `TestServiceHasNoAdminRoutes`
asserts `GET /healthz` is a 404 (D143's "no admin surface"). An unreachable service halts the run,
never falls back (D143). Cloud Run is regional and spreads instances across zones by itself.
*Consequence:* spec §6 is `min_instance_count`, a probe that adds no route (a TCP or startup probe),
and documentation; multi-region adds nothing while RTDB is one region (D501).

**F16, Gap (feasibility of the spec's token store).** An RTDB subscription in a Cloud Run service
needs CPU outside requests. Under request-based billing Cloud Run throttles CPU between requests,
so an SSE stream held for cache invalidation stalls; on reconnect RTDB sends the current state, so
the cache heals but revocation is not "immediate". It needs instance-based billing
(`cpu_idle = false`) and a stream per instance. *Consequence:* another reason to reuse the run
identity and read liveness per request, as the service does today (D494).

**F17, Rule.** The service is **dormant and opt-in** (D147; `.claude/rules/credentials-service.md`
"The service ships dormant"); the spec makes it "the critical path for every task". Making it
mandatory turns every installation's next `fugaro update` into a deploy of a new service plus an IAM
flip, and adds a hard dependency to every run. *Consequence:* D502.

**F18, Rule.** Where the upstream Bitbucket token lives. Q6 ruled **one bundle, one grant**. Today
the workspace token is a project secret (`fugaro secrets set bitbucket-token --project-wide`)
mounted by every allow-listed job. Moving it into the bundle keeps Q6 and makes rotation one
`fugaro creds set` with no running job affected (the service reads `latest`, 30 s cache). It also
inherits Q6's cost: a teammate who may add a token but not read one cannot use the path.
*Consequence:* D506.

**F19, Gap.** Rotation evidence (spec §4, §7). Bitbucket shows an access token's expiry in its UI;
whether the API exposes it to the token itself is UNVERIFIED (U3). Secret Manager records each
version's creation time, which doctor already lists (`gcp.Secrets` `Versions.List`). The GitHub App
key has no expiry. *Consequence:* "near expiry" is only checkable if the operator records the
expiry; "overdue for rotation" is checkable as version age against a rotation period (D503).

**F20, Gap.** Doctor runs as the operator. It cannot call the service as a job (it is no job
account; the service refuses it before anything else), so "failing to authenticate the caller via
workload identity" can only be checked by reading the service's state and IAM through the Run admin
API, or by a check job. The Secret Manager IAM sweep is feasible: `project-secret-extra-accessors`
already reads live `secretAccessor` grants on project-wide containers. *Consequence:* D503 lists
which doctor check lands in which slice and what each can really see.

**F21, Rule.** Shared project jobs are refused by the service (Global Constraint 6,
`AuthConfig.SharedJobAccounts`, `internal/credsvc/sharedjobs_test.go`). The owner ruled Bitbucket
into shared project jobs for 0.13.0 (D460). A proxy that serves only per-repository jobs leaves
shared Bitbucket jobs holding the workspace token. *Consequence:* tension T4; D505.

**F22, Rule.** The 2026-10-27 Bitbucket rulings. What survives the proxy and what it replaces:

| Ruling | Under the proxy |
| --- | --- |
| D461: a workspace or project token, the narrowest that fits | **Survives.** The proxy holds it |
| D462: the agent's reach is the token's reach, accepted | **Replaced** for installations that turn the proxy on: the run reaches one repository and one branch. Stays true for those that do not |
| D464 (dc73): `allow_data_to` segment wildcard | **Survives as data.** The list becomes the proxy's admission source. Its recorded consequence (a new matching repository is admitted silently) now admits it to the **proxy**, not to the token's value |
| D465: a derived `bitbucket_token` block in the policy node, checked by the runner | **Survives as data, enforcement moves** to the service, which already reads the policy node (O4's admissions precedent). A runner-side check binds nothing the agent cannot bypass (F5) |
| D466: doctor warns about a repository's own token overriding the project one | **Moot** once the proxy holds the one token |
| D467: copy `project_secrets` to every operator machine | **Survives** while the allow-list is local |
| D474: pin the Bitbucket API root | **Survives for non-proxied repositories**, whose runner still holds the token. A proxied run's adapter does not read `FUGARO_BITBUCKET_API_URL` (its transport goes to the forwarder). An owner-listed root is not served through the proxy in S1 (O4) |
| D475: launchers trusted | **Survives.** A launcher can still mint any repository's run identity (accepted limit) |
| D476: no code mitigation for cross-repository pull requests | **Resolved by the proxy**: a run cannot push to or open a pull request in another repository |
| D477: policy version 3 → 4 in 0.13.0 | **Tension T4**: if the proxy adds policy fields, a second lockstep bump follows unless both ship together |
| `FUGARO_GIT_TOKEN` holds the token's value on Bitbucket | **Replaced** by a run-scoped credential valid only at the proxy |

**F23, Gap.** Image builds and the image-check job clone with the git credential as the build
account (§3), and the build hands it to BuildKit (F18 and U8 of the Bitbucket work: whether a
Dockerfile step can read it is unexamined). Cloud Build is not a Cloud Run job: the service has no
caller class for it, and whether a Cloud Build step can fetch a Google ID token for the build account
is UNVERIFIED (U4). *Consequence:* builds keep their own mounted credential in the first slices, a
listed exception with an exit (D491).

**F24, Exists.** A CI-time guard has precedents. `deploy/terraform/terraform_test.go` already
refuses a project-level `secretAccessor` and pins the job and project modules' accessor grants, and
`TestServiceHeldSecretsMatchTheCredentialsService` holds the Go list to the module.
`.github/workflows/` cannot be edited from an agent run. *Consequence:* the "fails on direct secret
reads" check is a Go test over Terraform and Go imports with an explicit exceptions table, run by
the existing `go test` CI job (D499). CI runs on pushes to `main` and is not a merge gate (`.github/workflows/ci.yml`), so the guard fails `main` and the release gate, not the pull request.

**F25, Gap.** Spec §3's per-request log line. The rules file requires exactly one audit line per
refusal once the identity is verified, and names only. Git makes several requests per operation.
*Consequence:* one line per proxied request (task, repository, action, ref for a push, outcome),
names only, never a header or a body; resolution R2.

**F26, Exists.** Licensing. The subscriptions spec keeps the credentials service in the free core
("Never paywall safety", `docs/subscriptions/spec.md`), and the hosted key-signing service is a
separate component (D438, D440). *Consequence:* the proxy is MIT core; nothing here touches `ee/`.

**F27, Open.** Spec phase 2 (GitHub through the proxy). Today's GitHub token is already short-lived
and scoped to one repository; branch rules exist natively as rulesets; the agent's `gh` CLI calls
`api.github.com` with `GH_TOKEN`, and pointing it at a proxy means an Enterprise-style host
(`GH_HOST`), which changes `gh`'s paths. *Consequence:* D497b; deferring costs nothing today.

**F28, Gap (found while planning; corrected in review round 1).** Every branch a run may push is a
Fugaro branch: `task.Spec` validation refuses a `Branch` that does not match `task.BranchRE`
(`fugaro/<run-id>`, "the only branch a follow-up may continue", `internal/task/task.go`), and the
runner pushes only `fugaro/` branches. A first run's branch is `fugaro/<fr>`; a follow-up's names
the earlier run that created it. *Consequence:* the launcher-minted claim (D497a) is checked against
`task.BranchRE` always, and must equal `fugaro/<fr>` or name an earlier run, so even a hostile
launcher cannot aim the proxy at `main`; it can aim it at any Fugaro branch of a repository it may
launch (D475). Review round 1 also found that branch binding alone leaves the runner's REST writes
open: `UpdatePR`, `Comment`, `ApplyReady` and create take any pull request of the repository, and a
review-only run comments on pull requests that are not Fugaro branches (`task.Spec.ReviewPRs`).
So the claim also names the run's pull requests (`fpr`: `task.Spec.PR` or `ReviewPRs`), and the
service lets a pull-request write through only when the pull request's source is `fb` or its
number is in `fpr` (D497a, **amended after review**, O5). An identity minted by a launcher older than
the claims carries none: the runner refuses it at bootstrap on a proxied repository, and the policy
version bump (D477) already makes an older CLI refuse to launch against a v4 node (O3).

## 5. Decisions (D490–D506), answered 2026-10-27

Block: **D490–D519** and **Checks 190–199** (§8). Each row: the question, the recommendation and its
reason, the alternative and its cost, and the owner's answer of 2026-10-27 (relayed from
`AskUserQuestion`), with a one-line why.

| # | Question | Recommendation | Alternative and cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D490** | Which slice ships first, and in which release? | **0.12.1: nothing of this spec** beyond one sentence in the already-approved T1.1 pointing here. **0.13.0 or later, in this order:** S0 (inert: the audit table of §3 kept as a doc, the CI guard D499 with today's exceptions listed, doctor's SA sweep D503a, Check 190 written and run by the owner); S1 (the Bitbucket proxy for per-repository jobs of opted-in installations: git plus the runner's REST calls, run identity, repository plus run-branch rules, token in the bundle, the job's `bitbucket-token` mount dropped by a flip list). Honest size: S0 is 3–4 tasks; S1 is 10–14 security-sensitive tasks with independent reviews and two owner-run checks, on the order of the credentials service's Slice 2. It does not fit beside 0.13.0's current Bitbucket S2 and "control the spend" scope without dropping something | Start S1 in 0.12.1: breaks the "quick" ruling (D463). Or skip S0: the proxy's HTTP/1 size risk (F13) is found late | **2026-10-27: the recommendation, with the releases set by the owner.** S0 (the audit table, the CI guard, doctor's Secret Manager sweep and owner-run Check 190) in **0.13.0**; the Bitbucket proxy (S1) in **0.14.0**; 0.12.1 gets only the pointer sentence in the approved T1.1. Why: 0.12.1 stays quick, and the proxy is too large to sit beside 0.13.0's scope |
| **D491** | How uniform is the principle? Workflow `secrets:`, build secrets and image builds (F4, F23) | **Staged, uniform at the end.** S1 moves the Bitbucket token. A later slice adds a "vault" fetch: the runner gets the workflow's secrets from the service at bootstrap, and the job loses its accessor on them. Builds stay a listed exception until a build caller class exists (U4). The CI guard's exceptions table shrinks with each slice and is empty at the end | **Keep D145's exceptions permanently, documented.** No uniform rule, but no work for no reach gain, and no run-start dependency on the service for repositories that use neither git proxying nor model brokering | **2026-10-27: the recommendation.** Staged: Bitbucket first; workflow secrets later through a vault fetch; builds a listed exception with an exit slice; uniform at the end. Why: the reach gain first, the uniform rule as the end state |
| **D492** | Does the proxy replace or complement the workspace-token path of 0.12.1? (F22) | **Complement now, replace per installation later.** 0.12.1 ships dc73, T1.1 and T1.2 as approved. The allow-list stays the admission source. When an installation turns the proxy on, its jobs lose the token and D462's accepted reach ends there | **Hold 0.12.1's "supported path" docs until the proxy exists.** Leaves the 50-repository operator with no documented path for months | **2026-10-27: the recommendation.** 0.12.1 ships dc73, T1.1 and T1.2; the proxy replaces the agent-held token per installation when it is turned on. Why: the many-repository operator gets a documented path now |
| **D493** | 0.13.0's Bitbucket S2 (shared jobs, runner-checked `bitbucket_token` block, policy v4, API-root pin) | **Re-plan S2 against the proxy before building it.** Keep T2.8 (the API-root pin) and the policy block as data; move enforcement to the service; ship policy v4 once, carrying both the block and any proxy fields; shared Bitbucket jobs wait for D505 | **Build S2 as planned, then move the check.** Two enforcement points in a row, and likely a second lockstep policy bump | **2026-10-27: the recommendation.** Re-plan S2 against the proxy: T2.8 and the policy block are kept as data, enforcement is in the service, and the policy version is bumped once. docs/bitbucket-workspace-token's plan carries a dated pointer. Why: no double build and one lockstep upgrade. *Which S2 tasks ship in which release is open item O2* |
| **D494** | The proxy credential: the spec's opaque token in RTDB, or the existing run identity? (F12, F16) | **The run identity plus the job account's ID token**, verified by `Authenticator` as today (D134), revoked by the write-once outcome and the kill switches. No new store, no subscription, no sweep. Per-run early revocation, if wanted, is a small `creds/` mark the service checks | **The spec's RTDB token store:** a new subtree and rules, a mint endpoint, a cache with an SSE subscription that needs always-on CPU (F16), a sweep, and a second per-run identity beside D134's. **Or signed JWTs:** a new signing key to hold and rotate | **2026-10-27: the recommendation. It DEPARTS FROM THE OWNER'S SPEC** (spec §4, "Proxy token design"): the run identity plus the job's ID token; no RTDB token store, no subscription, no sweep. Why: D134's identity already exists, is verified by `Authenticator` and ends with the run's outcome |
| **D495** | How does git reach the proxy? (F6, F10) | **A loopback forwarder in the runner**, beside the gateway, that holds no real credential and attaches the job's ID token and the run identity per request; git and the runner's Bitbucket client point at it. Reason: tokens refresh per request, no git command outlives a token, and it is D135's pattern | **git straight at the service** with `extraHeader` (U1) and the run identity as the Basic password: no forwarder, but tokens are rewritten in git's environment at every refresh and a long command can fail mid-way. It also follows the spec's letter ("not in the CLI") more closely | **2026-10-27: the recommendation.** A loopback forwarder in the runner that holds no secret (D135's pattern). It departs from the spec's letter ("not built into the CLI") and keeps its intent: no real token is in the job (F6). Why: fresh tokens per request, and no git command outlives a token |
| **D496** | The proxy's per-request rules (F7, F8, F11) | **Repository plus action from the first slice:** fetch from the task's repository; push only to the run's branch (force allowed there), no deletes, no tags; REST only the endpoints the runner uses, for that repository; everything else refused. Reason: parsing the receive-pack command list is cheap, and repository-only would still let a run push to the base branch | **Repository only first,** branch rules later. Simpler, and the base branch then relies on Bitbucket's branch restrictions alone | **2026-10-27: the recommendation.** Repository and action rules from the start: fetch only from the task's repository; push only to the run's branch, force allowed, no deletes, no tags; REST only the runner's endpoints; the receive-pack command list is read before the pack is streamed on. Why: the base branch must not depend on Bitbucket's branch restrictions alone |
| **D497** | (a) How is the run's branch bound? (F12) (b) GitHub through the proxy (phase 2)? (F27) | **(a) A new launcher-minted claim** in the run identity (launchers are trusted, D475), checked by the service; a version-skew constant for old launchers. **(b) Defer phase 2** until Check 191 proves the Bitbucket proxy; GitHub's tokens are already short-lived and repository-scoped, and rulesets give branch rules | (a) Repository-only pushes (D496's alternative). (b) Plan GitHub proxying now: `gh` through an Enterprise-style host, a second REST surface, for a smaller gain | **2026-10-27: the recommendation.** (a) A launcher-minted branch claim, now. (b) GitHub through the proxy is deferred. Why: the branch must be bound by something the service trusts; GitHub's tokens are already narrow |
| **D498** | Model keys (spec §5, F9) | **No new mechanism:** wire the remaining `agent.auth` modes through the service (the existing plan's open half), keep Vertex out of scope (no key; the job account's role), and let doctor report installations where a key reaches the agent (budget off, no service) | **A new model proxy:** duplicates the gateway and the service's forward | **2026-10-27: the recommendation.** No new mechanism: the remaining `agent.auth` modes are wired through the service; Vertex is out. Why: the gateway and the service's forward already exist |
| **D499** | The CI check (spec §1) | **A Go test** that fails on any Terraform `secretAccessor` member, `secret_key_ref` or Cloud Build `availableSecrets`, and any Go use of the Secret Manager API, outside an exceptions table in the test, each row naming its decision and its exit slice. Runs in the existing CI `go test` job | **A shell lint in a workflow:** needs a `.github/workflows/` change an agent cannot push, and is easier to bypass | **2026-10-27: the recommendation.** A Go test with an exceptions table; no workflow edit. Why: an agent cannot edit `.github/workflows/`, and the existing CI job runs it |
| **D500** | Removing Secret Manager access from job accounts (breaking) | **Per installation and per secret class**, each behind a flip list like `config.ServiceHeldSecretNames` and its Terraform mirror, each flip gated on an owner-run live check, with a `### For operators` entry; `belong` is never touched (D147) | **One release that removes every accessor:** one code path sooner, but every installation's runs depend on a just-deployed service at once, with no fallback | **2026-10-27: the recommendation.** Per installation and per secret class, flip lists gated on owner-run live checks, `### For operators` release notes; `belong` is never touched. Why: no installation depends on a just-deployed service without a fallback |
| **D501** | Availability and hardening (spec §6, F15) | **`min_instance_count = 1`**, a TCP startup and liveness probe (no new route, so `TestServiceHasNoAdminRoutes` stands), Cloud Run's own multi-zone spread, fail-closed kept (D143), and a doctor read of who may deploy it (who holds `run.services.update` and `actAs` on its account). No multi-region | **An HTTP `/healthz`:** reverses D143's no-route rule for a probe that a TCP check already gives. **Multi-region:** two services and a global front end in front of a one-region database | **2026-10-27: NOT the recommendation** (which was `min_instance_count = 1` and TCP probes). **The service keeps scaling to zero, with no probe added.** The doctor read of who may deploy it is kept. **Recorded consequence:** an opted-in installation meets a cold start on the critical path of the first service call after idle (every model call of a `cascade` run, every mint, and from 0.14.0 every proxied git and REST request), and there is no redundancy beyond Cloud Run's own; `TestServiceHasNoAdminRoutes` stands. **Revisit before any release that makes the service a default.** Why (as relayed): keep today's shape |
| **D502** | Dormant opt-in versus "critical path for every task" (F17) | **Stays opt-in per installation** until S1 and the model wiring have passed their live checks; then a later release decides default-on for new installations | **Mandatory now:** every installation deploys the service on its next update and every run depends on it | **2026-10-27: stays opt-in as today** (the recommendation's opt-in half), relayed together with D501's scale-to-zero, which is **not** the recommendation. The consequence is D501's. Why: no installation is moved without the owner's act |
| **D503** | Which doctor checks land when (spec §7, F19, F20) | (a) **S0:** any account other than the service's holding `secretAccessor` (extends `project-secret-extra-accessors` to every container), listed against D499's exceptions. (b) **S1:** the service deployed and its invoker grants present (Run admin API; doctor cannot call it as a job), the Bitbucket token in the bundle, a Bitbucket repository still mounting its own token on an opted-in installation. (c) **Rotation:** version age against an operator-set rotation period; "near expiry" only when the operator records an expiry (U3). (d) Model keys reachable in the agent: from the installation's budget mode, the service opt-in and the workflow's `auth` | **All of §7 in one task:** some checks would report on things that do not exist yet | **2026-10-27: the recommendation.** (a) in S0, (b)–(d) in S1. Why: each check lands with the thing it checks |
| **D504** | Spec §4's "revocable early" for a run | **The kill switch per repository** (exists) plus `fugaro cancel`; a per-run service-side block only if the owner wants it (a `creds/` mark) | **A per-run revocation list now:** new state and a rules change for a case cancel already covers | **2026-10-27: the recommendation.** The per-repository kill switch plus `fugaro cancel`; no per-run block. Why: cancel already covers the case |
| **D505** | Bitbucket shared project jobs (D460) under the proxy (F21) | **Proxy first for per-repository jobs.** Shared Bitbucket jobs wait until the service admits shared-job callers by profile (the credentials plan's C3.9) | **Ship shared Bitbucket jobs in 0.13.0 with the token mounted**, as planned, and move them to the proxy later | **2026-10-27: the recommendation.** The proxy first for per-repository jobs; shared Bitbucket jobs wait for the credentials plan's C3.9. Why: the service refuses shared-job callers today (Global Constraint 6) |
| **D506** | Where the upstream Bitbucket token lives (F18) | **In the bundle** (Q6: one secret, one grant), written by `fugaro creds set bitbucket-token` | **A project secret with a second grant** to the service: keeps teammate write-without-read, breaks Q6 and the rules file's "one grant" | **2026-10-27: the recommendation.** The Bitbucket token lives in the bundle, written by `fugaro creds set bitbucket-token`. Why: Q6's one secret, one grant |

**D503a, extended 2026-10-29 (owner's rulings, B0.3 follow-up).** The B0.3 sweep as merged read one
role per container (`secretAccessor`) and one member class at the project level (service accounts).
Four changes, all of them coverage of reads that already existed rather than a new check:
(a) a container's readers are the members of **every** role carrying `secretmanager.versions.access`
— `secretAccessor`, `roles/secretmanager.admin`, and a custom role whose own definition lists it —
with a role nobody could read reported as unknown, not as one that carries nothing;
(b) the project-level half names a person or a group holding a reading role there, outside the
Owners, since a project-level grant is every container at once whoever holds it;
(c) the deployer read (D501) counts a project-level `iam.serviceAccountUser` holder, whose grant is
`actAs` on every account of the project, the service's own included;
(d) the remove advice and its `gcloud secrets remove-iam-policy-binding` line are for a container
carrying Fugaro's managed label only — for a container Fugaro did not create the line reviews its
readers instead, because they may belong to another application in the same project. The plan's B0.3
behaviour line carries the same four, and the tests that pin them are named there.

**D497a, amended after review (rounds 1 and 2, 2026-10-27), outside the owner's cell, for the owner to confirm (O5):** the code fact that every runnable branch matches `task.BranchRE` makes the branch rule stronger than the row assumed; and branch binding alone leaves pull-request writes open (F28), so the launcher-minted claim also names the run's pull requests (`fpr`), and is not minted for review and artifact runs, which never push (review round 2); the row's "version-skew constant for old launchers" is dropped because a constant in the new binary cannot stop an older launcher (O3). The owner's intent (a launcher-minted binding, now) is kept; only the claim's content widens B1.1 and B1.4's `fpr` rules wait on O5.

**Resolutions** (one defensible answer):

- **R1.** The proxy is a route of the existing credentials service, not a new service (F1).
- **R2.** One audit line per proxied request, names only: task, repository, action, the pushed ref
  only when it is the run's branch (otherwise a refusal class, never caller text), outcome (F25;
  corrected in review round 2).
- **R3.** SECURITY.md keeps D148's wording and adds that the agent can act as the run (F3, F5).
- **R4.** The proxy is MIT core (F26).

## 6. What 0.12.1 does meanwhile (D490, D492)

0.12.1 ships what is approved: run dc73 (the wildcard) and tasks T1.1 and T1.2 of
[../bitbucket-workspace-token/](../bitbucket-workspace-token/README.md). They change no runtime
reach that a per-repository job with a project copy does not already have. T1.1's statement of the
reach (D462) gains one sentence: a proxy that keeps the token out of the job is designed in
docs/secrets-brokering and not built (D490). Nothing else of this spec is in 0.12.1. S0 is 0.13.0;
the proxy (S1) is 0.14.0.

## 7. Tensions, as the rulings handle them, and open items

- **T1. "The agent never calls the service" against one container (F5). Not settled by a ruling.**
  The rule cannot be enforced: the agent can read what the runner holds and call the service as
  the run. What the proxy buys is a **run** narrowed to one repository and branch, with no real
  token in reach. R3 states it in SECURITY.md (plan B1.10). A real agent sandbox is a separate,
  larger project. → **O1.**
- **T2. The spec's token store against D134. Settled by D494**: the run identity; the spec's store
  is not built. Recorded as a departure from the owner's spec.
- **T3. Uniformity against D145. Settled by D491**: staged, uniform at the end. Recorded
  consequence: the later vault slice (S3) adds a service call to every run start of an opted-in
  installation, under D501's scale-to-zero, so a cold start lands there too.
- **T4. The proxy against 0.13.0's Bitbucket S2. Resolved by D493** (re-plan against the proxy, one
  policy bump) and **D505** (shared Bitbucket jobs wait for C3.9). Which S2 tasks go in which
  release → **O2.**
- **T5. "Critical path for every task" against a dormant opt-in service. Settled by D501/D502 as
  ruled**: it stays opt-in and scales to zero. Recorded consequence: an opted-in installation's
  first call after idle waits for a cold start, and there is no redundancy beyond Cloud Run's own;
  revisit before any release that makes the service a default.
- **T6. Health checks against "no admin surface". Settled by D501**: no probe and no route;
  `TestServiceHasNoAdminRoutes` stands.

**Open items for the owner:**

- **O1 (T1).** Confirm that SECURITY.md says plainly, from S1 on, that the agent can act as the run
  at the proxy (one repository, one branch, the run's life), and that no agent sandbox is planned.
  The plan writes it so (B1.10); the owner may want a sandbox project instead.
- **O2 (D493).** The re-planned S2's release split. This document's reading: **every** remaining
  S2 task moves to 0.14.0 with the proxy, so the policy version is bumped once, in 0.14.0, on the
  day an enforcer for the block exists. T2.1 and T2.3 are **amended** (review round 1): the block is
  derived from `project_secrets.bitbucket-token.allow_data_to` whether or not a profile mounts the
  token (a per-repository proxied job needs it), it gains a `proxy` switch, and `VersionSince[4]` is
  0.14.0's. T2.8 is unchanged. T2.11's 0.13.0 notes are replaced by the plan's 0.14.0 operator notes
  (B1.13). T2.2, T2.4, T2.6, T2.7 (shared Bitbucket jobs, Check 181) wait for C3.9 (D505); T2.9 (the
  runner's check) is replaced by the service's (B1.4). The alternative is to ship T2.1, T2.3, T2.8
  and the v4 bump in 0.13.0 as data with no enforcer, which still bumps once but asks every
  installation for a lockstep upgrade for data nothing reads until 0.14.0. **Cost of this reading
  (review round 2):** T2.8's API-root pin, ruled for 0.13.0 (D474), reaches non-proxied
  installations one release later. **The plan uses this document's reading until the owner rules.**
- **O3 (F28, amended after review).** No launcher skew constant (a constant in the new binary cannot
  stop an older launcher). Instead the runner on a proxied repository refuses, at bootstrap, an
  identity without the claims, naming the launcher's version, and the v4 policy bump makes an older
  CLI refuse to launch. Confirm.
- **O4 (D474 and T2's ruling, found in review).** An owner-listed Bitbucket API root (T2.8) is not
  served through the proxy in S1: the service forwards only to Bitbucket Cloud's own hosts. A
  repository behind an owner-listed root stays non-proxied. Confirm, or ask for the roots in S1.
- **O5 (D497a, amended after review).** The run identity carries the run's pull requests (`fpr`)
  beside the branch (`fb`), and pull-request writes are bound to them (F28). Confirm the amended
  answer.
- **O6 (D496's consequence, found in review round 2).** A proxied run fetches only its own
  repository, so git submodules and private git dependencies in other Bitbucket repositories, which
  today's workspace token fetches, are refused. Choices: accept and document it (the plan's
  default: B1.10, the operator notes), or let the proxy also fetch (never push) the other
  repositories the block admits.

- **O7 (found in review round 3).** `fugaro update --provision-credentials` refuses a development
  build (`internal/cli/init_credentials.go` `resolveCredentialsImage`), so Check 191 runs only on a
  released 0.14.0, and the flip and Check 192 move to the next release. D490's "the proxy in 0.14.0" holds for
  the inert proxy; confirm that the flip follows in the next release.
- **O8 (review round 3, left open after the last round).** The flip has no readiness gate that the
  bundle holds a Bitbucket token (only the owner can read the bundle): the operator notes order it
  first and doctor warns. Whether `fugaro update --repo` should require an explicit confirmation
  before dropping the mount is the owner's call.

## 8. Numbers this work holds

**D490–D519**: D490–D506 written and answered (2026-10-27); D507–D519 reserved and unused.
**Checks 190–199**: the plan assigns 190 (the proxy's feasibility, S0), 191 (S1 end to end) and
192 (the Bitbucket mount flip); 193–199 are reserved and unused. All three are owner-run. Both blocks sit above the Bitbucket workspace-token work's
D460–D489 and Checks 180–189. They were checked against `main` and the docs of every open PR on
2026-10-27: none writes D490 or above, or Check 190 or above.

## 9. UNVERIFIED

- **U1.** Cloud Run accepts a Google ID token in `X-Serverless-Authorization` for IAM when the
  application uses `Authorization` itself (D495's alternative).
- **U2.** Cloud Run's request-body limit (believed 32 MiB on HTTP/1), whether a chunked git push
  above it is accepted, and whether h2c end to end lifts it (F13). **Two of the three are answered
  by Check 190 (RUN 2026-10-30, its run record in the register):** a **33 MiB** pack on HTTP/1.1 is
  refused — the front end answers **413** before the proxy sees four bytes of it — so the limit is
  measured and S1 serves **h2c end to end** (B1.0, B1.5). Whether **h2c end to end lifts it** stays
  UNVERIFIED: no live step has read a push over h2c. Check 91 measures the HTTP/1.1 half and is not
  run.
- **U3.** Whether Bitbucket's API reveals an access token's expiry to the token itself (F19).
- **U4.** Whether a Cloud Build step can fetch a Google ID token for the build account to call the
  service (F23).
- **U5.** Whether Bitbucket's git endpoint and REST API accept a workspace or project access token
  exactly as a repository token (U1 of the workspace-token work, Check 180, not run).
- **U6.** Whether any enrolled Bitbucket repository uses Git LFS, which a git-only proxy would not
  cover (F11).
- **U7.** The latency and request count of a blobless clone through the proxy (F14); **answered by
  Check 190** (RUN 2026-10-30): a blobless clone took **3 s** in **5** requests, where a full clone
  took **10 s** in **3** (1080 B in / 23008 out).
- **U8.** Cold-start latency of the service as the first hop of a git request after idle (D501's
  consequence); **answered by Check 190** (RUN 2026-10-30): the first git request after **2100 s**
  idle took **1 s**, on a **new** instance.

## 10. The later slices read against the tree (2026-11-10)

**What this section is.** The plan carried S2–S5 as four one-line bullets (B2.1, B3.1, B4.1, B5.1)
with no files, tests or red steps, so no run could be launched for any of them. This section reads
each one against `origin/main` at `d51ada6d5` (2026-11-10) and against the rulings taken since
2026-10-27 — the deploy-now, switch-later ruling of 2026-11-05
([../credentials-service/README.md](../credentials-service/README.md), line 3), the dynamic-delivery
addendum ([dynamic-delivery/reconciliation.md](dynamic-delivery/reconciliation.md), D1930–D1946),
the model-crafts and API-key work (`docs/model-crafts`, D1634, D1638; `docs/api-key-review`), the
project workflow secret (`docs/project-workflow-secrets`, D1267) and the Bitbucket readings of
2026-11-10 (D1980–D1984) — and turns what remains into the tasks of the plan's section "Later slices,
planned 2026-11-10". Its decisions, **D2070–D2080, were answered by the owner on 2026-11-10, all as
recommended**: each keeps its recommendation, the alternatives and their cost, and the answer in its
last cell. The owner's 2026-11-10 "approve all tasks" ruling approves tasks B2.1–B5.9; each keeps
its own gate (the owner-run checks, Check 195 before S4's code tasks, the serialization the plan
names).

### 10.1 Verdict per later task

| Task | Verdict | Why (path and line on `d51ada6d5`) |
| --- | --- | --- |
| **B2.1** (S2, D498) every `agent.auth` mode through the service | **KEPT, re-scoped; partly subsumed** | The provider half is other plans' work: the bundle's provider key is AK6b (merged, `26e724261`, #2172), the OpenRouter upstream is dynamic-delivery's **DS3.5** (in a run, #2288), and the service serving Anthropic-key and OAuth **pool members** of a cascade is model-crafts' **MC6** (not started, held on AK7). What no plan carries: the switch admits `cascade` alone — `infra.CredentialsServiceServes` returns `installed && ready && auth == config.AuthCascade` (`internal/infra/spec.go:287-289`) — so an `api-key` or `oauth` repository keeps its job-mounted model credential and gets no service URL (`internal/cli/run.go:4603`). That residue is B2.1–B2.6 (F29–F31) |
| **B3.1** (S3, D491) workflow `secrets:` through a vault fetch | **KEPT** | No later ruling retires it: dynamic-delivery R2 restates the order (model credentials first, "then workflow secrets and builds last") and leaves the mount flips to "a later plan" (`dynamic-delivery/implementation-plan.md`, "Declined, and why"); D1267 leaves the vault fetch to B3.1 and owes it one sentence (F33). The runner still reads every workflow secret from its own environment (`internal/runner/runner.go:2786-2791`). B3.1–B3.8 plan it; D2073–D2075 shape it |
| **B4.1** (S4, D491, U4) builds and the image check through the service | **KEPT, gated on U4** | The build account still reads the git credential and every build secret: `google_secret_manager_secret_iam_member.build` (`deploy/terraform/gcp/modules/repo/build.tf:15-20`), the request built in Go (`internal/backend/gcp/build.go:204-211`, `internal/backend/gcp/catalog.go:334-341`), the check job's mounts (`deploy/terraform/gcp/modules/repo/check.tf:66-71`), and on GitHub the build step holds the App's private key itself (`internal/cli/gitcred.go`, `gitCredAppKeyEnv`). Whether a Cloud Build step can mint an ID token the service admits is still U4. B4.1–B4.7 plan it; D2076–D2077 shape it |
| **B5.1** (S5, D505) shared project jobs at the service, then Bitbucket shared jobs | **KEPT** | A coding run on a shared job is still refused whole (`AuthConfig.SharedJobAccounts`, `internal/credsvc/auth.go:310-324`); the launcher hands no service URL to a project job (`internal/cli/run.go:4594`, `job != ""`); the runner never proxies a project job (`internal/runner/project.go:491`, `project` is the first refusal of `proxiedBitbucket`). The credentials plan's C3.9 general half is planned nowhere (`docs/credentials-service/implementation-plan.md:674`). docs/bitbucket-workspace-token's **T2.2 and T2.4 wait on it** (D1980, D1982, answered 2026-11-10). B5.1–B5.6 plan it; D2078–D2079 shape it |

**None of the four is SUPERSEDED or MERGED whole.** Two pieces are subsumed by other plans and are
not re-planned here: the OpenRouter upstream (DS3.5) and pool selection inside the service (MC6). Two
pieces are moot under the rulings: the "provider routes" half of B2.1 at the service (D1634 keeps
OpenRouter and Vertex members on the runner path, `docs/model-crafts/reconciliation.md:100`), and
Vertex, which has no key (D498).

### 10.2 Findings

**F29, Gap (Check 191's first run, 2026-11-10).** `proxy: true` is inert for a repository off the
credentials-service switch: `proxiedBitbucket` returns false without `FUGARO_CREDENTIALS_SERVICE_URL`
(`internal/runner/project.go:494`), and the launcher sets that variable only where
`CredentialsServiceServes` holds, which is `cascade` alone (`internal/cli/run.go:4603`,
`internal/infra/spec.go:288`). The sandbox repository of Check 191's first run was not on the switch,
and the check now says so (`6e319b650`, #2284). *Consequence:* a Bitbucket repository whose
`agent.auth` is `oauth`, `api-key` or `vertex` (the default, `internal/config/defaults.go:56`) cannot
be proxied today. S2 is therefore not only a model-key slice: it is what lets a non-cascade Bitbucket
repository use S1, and why D2080 recommends S2 first.

**F30, Gap.** An `oauth` run never starts the gateway: `GatewayOn` admits `api-key`, `vertex` and
`cascade` only (`internal/runner/routing.go:292-299`), and `gatewayUpstream` refuses any other auth
(`internal/runner/gateway.go:279-305`). Every gateway also needs the budget mode at `observe` or
`enforce` (`internal/runner/routing.go:298`). *Consequence:* serving `oauth` through the service needs
the gateway on for an `oauth` run on the switch, and a repository whose installation runs budget mode
off cannot be switched without changing what that mode means (D2071).

**F31, Gap.** The mount drop and the URL are one answer by design (`CredentialsServiceServes`'s own
comment, `internal/infra/spec.go:282-286`), and `dropModelMounts` drops **every** model secret,
provider keys included (`internal/infra/readiness.go:135-144`; `config.IsModelSecretEnv` is
`ModelSecretEnvs` or `IsProviderKeyEnv`, `internal/config/providers.go:529-531`). For a `cascade`
repository that is right — the cascade refuses a provider pin (`CheckProviderAuth`,
`internal/config/providers.go:930`). For an `api-key` repository it is not: its provider keys stay on
the runner path (D1634), so its drop takes the Anthropic key alone. And a launcher or a job image
older than the change, meeting a repository whose mount was dropped, has no credential at all (the
runner reads `ANTHROPIC_API_KEY` from its own environment, `internal/runner/gateway.go:281-283`).
*Consequence:* D2072 — the URL first with the mounts kept, the mounts dropped later behind a flip list
and an owner-run check, as D500 does for the Bitbucket token.

**F32, Rule.** D502 ("stays opt-in") is superseded for the deploy by the owner's ruling of 2026-11-05:
`fugaro update` deploys the service for every installation and a repository is switched onto it per
repository, where the service can serve it (`docs/credentials-service/README.md`, line 3;
`.claude/rules/credentials-service.md`). *Consequence:* every later slice is "per repository, where the
switch holds", never "per opted-in installation"; G9's "without the opt-in" reads "off the switch".

**F33, Rule.** D1267 (`docs/project-workflow-secrets/reconciliation.md:745-759`) fixes what B3.1
reads: "the vault fetch reads the container the mount reads — `gcp.ProjectSecretID` of the logical
name", the per-repository container being `gcp.SecretID` (`internal/backend/gcp/names.go:203`,
`:214`), and the operator-local allow-list keeps deciding the reach. *Consequence:* no value moves and
no name changes (dynamic-delivery's R6 holds for this class too); the service must learn the
allow-list's answer per repository, because it cannot read an operator's local config (D2073), and it
must be able to read those containers (D2074).

**F34, Gap.** Under Q6 the service holds exactly one accessor, on the bundle
(`TestCredentialsServiceGrantsAreTheOneBundle`, cited in `internal/credsvc/dynamicsecrets.go`'s header
comment); D1931 departed from it once, for the dynamic-delivery prefixes only. A workflow secret's
container carries the existing name, `fugaro-<slug>-<logical>-<hash>`, which no name-prefix condition
separates from the job's git token and model secrets (dynamic-delivery F6). *Consequence:* D2074.

**F35, Gap.** The Batch backend resolves `Options.Secrets` as the VM's own account
(`internal/backend/batch/batch.go:203-207`, `internal/backend/batch/run.go:235`), and a Batch VM is not
a job account the service admits (secret-readers E5). B1.7 already refuses a proxied run on Batch
(G14). *Consequence:* D2075.

**F36, Gap (U4 still open).** Nothing has measured whether a Cloud Build step running as the build
account can fetch an ID token that the service's IAM and `Authenticator` admit; the daily check job is
a Cloud Run job (`deploy/terraform/gcp/modules/repo/check.tf`) and can, as any job can.
*Consequence:* S4 starts with an owner-run check (Check 195), and every S4 code task waits on it.

**F37, Gap.** The GitHub build path holds the App's **private key** inside the build step and mints
there (`internal/cli/gitcred.go`, `gitCredAppKeyEnv`, `gitCredGitHubAPI`). The service already answers a
read-only GitHub request (`internal/credsvc/githubread.go:235`, `githubRead`). *Consequence:* R5.

**F38, Rule.** C3.9's precondition is "the phase-3 shared-job rules are on `main`"
(`docs/credentials-service/implementation-plan.md:111`, `:674`), and `:674` names one piece: "the
reference record of project-centric §7 becomes the tier-A registry and the source of admissions
(D131, O4)". On `main`: profile jobs (`internal/infra/projectjobs.go:68`, `ProjectJobs`), the
profiles in the published node, the service reading admissions from that node (O4,
`internal/credsvc/cascade.go:211`, `admittedAccounts`), and the review half of C3.9 (D1070,
`docs/c39-review-identity/`), which already binds a shared caller to its profile
(`AuthConfig.ReviewJobAccounts`, `internal/credsvc/auth.go:325-338`). *Consequence:* D2078 asks
whether the published node is that record (the recommendation) or a separate registry is owed.

**F39, Gap.** docs/bitbucket-workspace-token's T2.2 deploys a shared Bitbucket job that **mounts** the
token (its "Behaviour" list). Under S1 a proxied repository's run reads no token; a shared job whose
repositories are all proxied would carry a mount nothing reads, and one that is not proxied puts the
workspace token's whole reach into a job serving several repositories — the case the 2026-10-15
ruling kept Bitbucket off shared jobs for (`docs/git-providers.md:71`). *Consequence:* D2079.

**F40, Gap.** A run identity names one slug, and the service binds a per-repository caller's
repository through `JobAccount.Repo` (G3). A shared job's account serves every repository of its
profile, so a shared coding caller's binding is "the identity's repository, and it is one of the
profile's" — the shape the review half already uses. *Consequence:* B5.1's interface; a launcher can
still mint an identity for any repository of the profile (D475, accepted).

### 10.3 Resolutions (one defensible answer each)

- **R5.** A build caller gets a read-only GitHub installation token for its own repository from the
  service's existing read path, never the App key (F37).
- **R6.** An `api-key` repository's provider keys stay on the runner path and keep their mounts in S2
  (D1634); S2's drop takes `anthropic-api-key` and the OAuth token only (F31).
- **R7 (amended after review round 1).** For a non-cascade repository the service decides from
  `update`'s `creds/repoauth/<slug>` record, never from the caller: the run's declaration must match
  it, a missing or mismatched one is refused, and only a repository with no record or a `cascade`
  record gets today's cascade (F30, D2070).
- **R8.** Every flip in S2–S5 follows D500: a flip list in `internal/config`, its Terraform mirror and
  guard test, an owner-run check before and after, the `### For operators` line "run
  `fugaro update`", and `belong` never touched.

### 10.4 Decisions D2070–D2080 (answered 2026-11-10, all as recommended)

Block **D2070–D2089**, taken above D2069, the highest number written or reserved on `main` or in any
open pull request on 2026-11-10 (outer-loop-escalation's D2030–D2069, #2285, open when this was read
and merged since as `280a0c379`). D2081–D2089 are reserved and unused. This folder's own
D507–D519 stay reserved and unused.

| # | Question | Recommendation and reason | Alternatives and their cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D2070** | How does the service serve an `api-key` or `oauth` repository (B2.1, F29)? *Amended after review round 1, before any answer: the first text let the caller's own header narrow, and a hostile agent could omit it and be served the whole cascade, tier B included (`internal/credsvc/cascade.go:28-31`).* | **(a) A `creds/repoauth/<slug>` record that `update` writes** from the repository's `fugaro.yaml` (its auth and, for `oauth`, the accounts its workflows name — the account is per workflow and per run, `oauth_token` and `--token`), read by the service; the run's declared mode and account are checked against it, and a missing or mismatched declaration on a non-cascade repository is refused (R7). Reason: no run writes `creds/`, no policy bump, and the cascade's admission, back-off and audit are reused | **(b) Supersede B2.1:** operators move to `cascade` with a one-member pool (MC5/MC6). Cost: no code, but every non-cascade Bitbucket repository stays unproxiable until its owner edits `fugaro.yaml`, and an `api-key` repository with a provider pin can never move (`CheckProviderAuth` refuses the pin under cascade). **(c) The repository's auth in the published node.** Cost: a policy field and a lockstep version bump (D477). **(d) The caller's header alone** (the first text). Cost: no boundary — the agent omits it |  Owner, 2026-11-10: as recommended |
| **D2071** | An `api-key` or `oauth` repository on an installation whose budget mode is off (F30) | **Held off the switch:** the switch gains the mode as a condition; the repository keeps its mount and doctor says why. Reason: the gateway is what reaches the service, and it exists only in `observe` and `enforce`. **Consequence (review round 1):** the mode is read at `update` while the runner reads it live, so after B2.7's flip a publish that lowers the mode to `off` would leave switched jobs with no mount and every run halting until `update --repo`; under (a) B2.7 makes `fugaro project policy publish` refuse that lowering while such a repository is switched | **(b) Turn the gateway on for a switched repository whatever the mode.** Cost: "budget mode off" stops meaning "no gateway" for those runs, against every doc of the mode, but the mode can then change freely |  Owner, 2026-11-10: as recommended |
| **D2072** | How S2 rolls out (F31) | **Two steps:** B2.3 hands out the URL and the runner uses the service, every mount kept (an older launcher or image still works **until the flip**; after B2.7 an older CLI, whose predicate is cascade-only, hands no URL to a job with no mount, and `serviceAuthSince` guards images, not launchers — a residue under the owner's no-skew ruling); B2.6 drops the Anthropic key and OAuth token mounts behind a flip list (`config.ServiceServedAuths`, empty until then) after Check 193 and the owner's go (R8) | **(b) One release drops both.** Cost: a teammate's older CLI, or a job image before `serviceAuthSince`, meets a repository with no credential and its runs halt. **(c) A policy version bump** so an older CLI refuses to launch. Cost: a lockstep upgrade of every installation for one slice |  Owner, 2026-11-10: as recommended |
| **D2073** | Where the service learns which workflow secrets a repository's job may fetch (S3, F33) | **An RTDB node under `creds/`** (`creds/workflowsecrets/<slug>`) that `fugaro update` writes from the answer it already computes for the mounts. Reason: the service already reads RTDB with its own role, and the rules already deny every run identity `creds/` (dynamic-delivery §2) | **(b) A published policy field.** Cost: a version bump and a lockstep upgrade. **(c) A bucket object, as D1940's admin list.** Cost: a new grant to the service's account (today the bundle and `fugaroCredentialsDatabase` only) |  Owner, 2026-11-10: as recommended |
| **D2074** | How the service reads a workflow secret's container (S3, F34) | **Per-container grants** from the repository root, one for each secret the job mounts, beside the job's own; a guard test holds the two sets equal. Reason: exact, and it follows D1931 in departing from Q6 for one class | **(b) A project-level accessor on a name-prefix condition.** Cost: no condition separates a workflow secret from the git token and model secrets under the existing names. **(c) Move workflow secrets into the bundle.** Cost: every value re-entered by a person (`update` cannot read a value: HARD RULE, dynamic-delivery R6) |  Owner, 2026-11-10: as recommended |
| **D2075** | The Batch backend's workflow secrets (E5, F35) | **A permanent listed exception:** E5's exit becomes `never`, and a repository on `backend: cloud-batch` keeps `secretVariables` | **(b) A Batch VM caller class** in the service. Cost: a new caller class, a new principal kind in `cover.go`'s `grantRules`, for a backend that already runs no proxied repository (G14) |  Owner, 2026-11-10: as recommended |
| **D2076** | A Bitbucket repository's build credential (S4) | **The service hands the build its repository's token for the build;** the accessor leaves the build account, the reach does not change (a Dockerfile can still read what BuildKit mounts). Reason: S4's aim is the accessor, and a build-side forwarder is a large new component | **(b) The build clones through the proxy** with a fetch-only build class. Cost: a forwarder in the build step, new rules in `internal/gitproxy` and a check of its own; it is the only option that narrows the reach |  Owner, 2026-11-10: as recommended |
| **D2077** | A workflow build secret's value in S4 | **From the service, with the git credential,** so the build account's accessor list empties | **(b) Keep `availableSecrets` for build secrets** and move only the git credential. Cost: E3 and E6 never exit, and the guard keeps a runtime row |  Owner, 2026-11-10: as recommended |
| **D2078** | Is C3.9's precondition met on `main` (F38)? | **Yes:** the one named piece — "the reference record of project-centric §7 becomes the tier-A registry and the source of admissions (D131, O4)" — is what the published node already is for the service (O4: the service learns each repository's admitted accounts from what `fugaro project policy publish` publishes; `admittedAccounts`, `internal/credsvc/cascade.go:211`), and profile jobs and the review half's profile binding are on `main`; B5.1 builds the coding half on them | **(b) Build a separate tier-A registry first,** as the sentence's letter reads. Cost: a second source of admissions beside the published node, which O4 already ruled is the source, and S5 waits on a design nobody has written |  Owner, 2026-11-10: as recommended |
| **D2079** | A Bitbucket shared job (T2.2 there, F39) | **Proxied only:** a shared Bitbucket job is deployed only for a profile whose every repository the published block proxies, and it mounts no token | **(b) As T2.2 is written: mount the token.** Cost: the workspace token's whole reach in a job serving several repositories, which the proxy exists to end. **(c) Both, by profile.** Cost: two shapes of one job to test and explain |  Owner, 2026-11-10: as recommended |
| **D2080** | The order of the four slices | **S2 first** (it unlocks S1 for every non-cascade repository, F29), then **S5** (it unblocks docs/bitbucket-workspace-token's T2.2 and T2.4), then **S3**, then **S4** (gated on U4) | **(b) Numeric order S2, S3, S4, S5.** Cost: Bitbucket shared jobs wait behind the vault and the builds. **(c) S5 first.** Cost: shared jobs move to the service while most per-repository jobs still cannot |  Owner, 2026-11-10: as recommended |

**Tension T7, accepted with D2070–D2080: DS3.5 stays as built.** DS3.5 (#2288) puts an OpenRouter
upstream in the service, while D1634 (model-crafts) keeps OpenRouter members on the runner path.
This section follows D1634 (R6) for S2's drop, which takes the Anthropic key and the OAuth token
only; the owner's answers of 2026-11-10 accept the tension as recorded, and DS3.5 stays as built.

### 10.5 UNVERIFIED (added)

- **U9.** That a Cloud Run execution started before a re-apply that drops a mount keeps the mounted
  value for its whole life (believed: the platform resolves `secret_key_ref` at execution start;
  `docs/design/update-in-flight.md` marks the neighbouring ForceNew case UNVERIFIED). Every flip in
  S2–S5 leans on it, so Checks 193, 194, 196 and 197 each flip with a run in flight.
- **U10.** That a Cloud Build step can fetch an ID token for an arbitrary audience from the metadata
  server as the build account (U4 restated for S4; Check 195).
- **U11.** That D2074's per-container grants stay inside IAM's limits for an installation with many
  repositories (not read here).
