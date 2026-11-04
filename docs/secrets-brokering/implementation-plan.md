# Secrets and credential brokering: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). It
references decisions by `D#`, findings by `F#` and open items by `O#`. Where they disagree with the
spec, the reconciliation wins.

## Status

- **Nothing is implemented. No task is approved.** Each task needs the owner's own explicit go.
  Answering a decision approves no task.
- **The owner's answers of 2026-10-27 that shape the plan** (reconciliation §5):
  - **D490:** S0 in **0.13.0**; the Bitbucket proxy (S1) in **0.14.0** — the owner moved S1's proxy
    chain to **0.15.0** on 2026-10-30 (below), so 0.14.0 carries only S1's inert and data-only
    pieces; 0.12.1 gets only one pointer sentence in the already-approved T1.1 of
    docs/bitbucket-workspace-token.
  - **D491:** staged; Bitbucket first; workflow secrets through a later vault fetch (S3); builds a
    listed exception with an exit slice (S4).
  - **D492/D493:** the wildcard path ships now (dc73, T1.1, T1.2 in 0.12.1); that plan's S2 is
    re-planned against the proxy ("The re-planned Bitbucket S2" below).
  - **D494 (departs from the owner's spec):** the proxy credential is the run identity plus the
    job's ID token. No RTDB token store, subscription or sweep is built.
  - **D495:** a loopback forwarder in the runner that holds no secret.
  - **D496:** repository and action rules from the start.
  - **D497:** (a) a launcher-minted branch claim now (**amended after review**: the claim also
    names the run's pull requests, O5); (b) GitHub through the proxy deferred (no task).
  - **D498:** no new model mechanism; the other `agent.auth` modes are wired later (S2).
  - **D499:** the guard is a Go test with an exceptions table.
  - **D500:** flip lists per installation and per secret class, each gated on an owner-run check,
    with operator release notes; `belong` is never touched.
  - **D501 (NOT the recommendation) and D502:** the service stays opt-in and **keeps scaling to
    zero**, with no probe added; the doctor read of who may deploy it is kept. Consequence: cold
    starts on the critical path of an opted-in installation, no redundancy beyond Cloud Run's own.
    Revisit before any release that makes the service a default.
  - **D503:** doctor (a) in S0, (b)–(d) in S1. **D504:** kill switch plus `fugaro cancel`.
    **D505:** per-repository jobs first; shared jobs wait for C3.9. **D506:** the token in the
    bundle.
- **Provisional:** the re-planned Bitbucket S2's release split and the amendments it needs follow
  this plan's reading until the owner rules on **O2**; **O1** and **O3–O6** are open too
  (reconciliation §7).
- **Review round 1 (2026-10-27)** reshaped B1.1 (the branch rule keys on `task.BranchRE`, the claim
  names the run's pull requests, no launcher skew constant), B1.4 and B1.5 (`GET /user` allowed,
  PR writes bound to the run, pkt-line gaps, no raw-path forwarding), B1.6 (moved into
  `internal/gateway`, after B1.5, mirroring the existing wire contract), B1.7 (an explicit per-policy
  switch so S1 is inert on upgrade; pagination), the Terraform tasks' files and commands, the fakes,
  and the edges.
- **Review round 2 (2026-10-27)** made the flip per repository (it follows `proxy: true` and the
  allow-list, not the opt-in alone), withheld `fb` from review and artifact runs, bound pull-request
  writes to the source repository and the destination, added a git-credential kind to the bundle,
  widened the guard, added a launch-time image check (`proxySince`), named the missing REST calls,
  files, Terraform commands and register-recording tasks, and listed cross-repository fetches as a
  consequence (O6).
- **Review round 3 (2026-10-27, the last)** found that `fugaro update --provision-credentials`
  refuses a development build (`internal/cli/init_credentials.go` `resolveCredentialsImage`), so
  Checks 191 and 192 can run only against a **released** release carrying the proxy — 0.15.0 since
  the 2026-10-30 move, not 0.14.0 — so the flip moves after that release (O7). It also
  made the published node the one source of whether a repository is proxied, put doctor's bundle
  read behind a flag, refused proxied runs on the Batch backend, added the h2c fallback task, the
  Terraform plumbing, the missing tests, and moved the exceptions table into the guard test as D499
  says. What it raised and this PR does not settle is in reconciliation §7 (O7, O8).
- **The owner moved the Bitbucket proxy chain to 0.15.0 (2026-10-30, after the v0.13.0 tag).** B1.5,
  B1.6, B1.7, B1.9, B1.10 and B1.11 ship in **0.15.0**, not 0.14.0, and B1.12 runs Check 191 on the
  released 0.15.0. 0.14.0 ships only the pieces that are inert or data-only: policy version 4 with
  the `bitbucket_token` block and the `proxy` switch (BW-T2.1, BW-T2.3), the API-root pin (BW-T2.8),
  the bundle's Bitbucket token (B1.2), the git proxy's parser and rules (B1.3, B1.4), the flip list
  still empty (B1.8) and the operator draft (B1.13 part one). No proxied run exists in 0.14.0: a
  Bitbucket run's job still mounts and reads its own token, and `proxy: true` in a published node
  changes nothing such a run does. The flip (B1.13 part two) follows the chain it flips, so it ships
  in the release after the one Check 191 passes on — 0.16.0 under this reading (O7). This is the
  move the 0.14.0 docs and the levels below record.

## Conventions every part follows

- `CLAUDE.md` and `.claude/rules/credentials-service.md` (every task touching `internal/credsvc`,
  `internal/creds` or `SECURITY.md`): independent review before merge, one audit line per verified
  refusal, no value in a log or error, the service dormant, `ServiceHeldSecretNames` untouched by
  this plan, the service account's grants unchanged (the bundle and `fugaroCredentialsDatabase`),
  `website/content/security.md` regenerated with `go run ./internal/clidoc`.
- `.claude/rules/terraform.md` for `deploy/terraform/**` and `internal/infra/**`: `terraform fmt`,
  the `terraform`-tagged tests and the golden plans (`scripts/gen-golden-plans.sh`) with any module
  change.
- No secret-shaped literal in a test, a fixture or a doc. No agent runs a live step,
  `fugaro update --provision-credentials`, `fugaro creds set` or a live check: those are OWNER-RUN.
  An owner-run check's result block is pasted by the owner; an agent writes it into the register in
  a docs PR, as Check 90's run record was.
- Docs are tested: `go test ./internal/cli/ -run Docs` after any doc change.
- CI runs on pushes to `main`, not on pull requests: a guard test fails `main`'s `ci` job and the
  release gate, not the PR. Every task runs its own command before it asks for review.
- Steps after deploy go in the release's `### For operators` (B1.13's draft, copied by the 0.14.0
  release PR, as docs/bitbucket-workspace-token's T2.11 drafts its notes).
- Every task marked SECURITY-SENSITIVE: **opus + mandatory independent review**.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | No real Bitbucket token is in a **run** job of a proxied repository once its flip is on: not in an env, a mount or the runner's memory. The daily image-check job and builds keep the build account's copy until S4 | B1.7 (no token read), B1.13 (mount and accessor dropped), Check 192 |
| G2 | A push names only `refs/heads/<fb>`; no ref deletion, no tag; a non-run ref is refused **before** any pack byte is forwarded | B1.3, B1.4, B1.5 |
| G3 | The repository is taken from the classified request, canonicalised as `projectpolicy.Canonical("bitbucket", …)` (the form `JobAccount.Repo` carries, provider included), and must equal the calling job account's repository (`JobAccount.Repo`, which `Server.ServeHTTP` already binds to the identity's slug through `s.jobs`) and be admitted by the published `bitbucket_token` block with `proxy: true` | B1.4, B1.5, BW-T2.1 amended |
| G4 | REST is an allow-list of the calls `bitbucket.Provider` makes (`GET /user`, the repository, `commit/{rev}`, the pull-request list with its `q=` query, pull-request reads, create, update, comments), for that repository; every pull-request write is bound to the run (G12); merge, approve, decline, `/workspaces`, pipelines, settings and every other path are refused | B1.4 |
| G5 | The forwarder holds no credential: it attaches the job's ID token, the run identity and the job name, nothing else; it listens on loopback only; it never forwards the caller's `Authorization` | B1.6 |
| G6 | `fb` is minted only for runs that push (first runs and follow-ups), always matches `task.BranchRE` (which `task.Spec` validation already requires of every branch a run may continue), and either equals `fugaro/<fr>` or names a run id that sorts before `fr`. A review or artifact run has no `fb`, so the proxy refuses every push and every pull-request write but its own review comments on `fpr` | B1.1, B1.4 |
| G7 | One audit line per proxied request, names only (R2); no header, body or token in a line; a pushed ref is logged only when it equals `fb`, otherwise as the refusal class `other_ref` (caller text is never echoed) | B1.5 |
| G8 | The kill switches and the run's outcome stop the proxy exactly as they stop a model call (D504) | B1.5 |
| G9 | Without the opt-in **and** `proxy: true` for that repository (the operator's entry, with the repository in `allow_data_to`), nothing changes: no forwarder, no claim needed, and the job keeps mounting and using its token, **after the flip too**: the flip drops the mount only for proxied repositories (D502) | B1.7, B1.8, B1.13 |
| G10 | The guard fails on any new Secret Manager reader not in the exceptions table | B0.2 |
| G11 | A git request never outlives its credentials (D495) | B1.6 |
| G12 | A pull-request write (create, update, comment, ready, reviewers) is allowed only when the pull request's **source repository is the run's repository** and its source branch is `fb`, or (comments only) its number is in `fpr`; a create names `fb` and the run's repository as its source; an update never changes `destination`; merge, approve and decline are never allowed | B1.4, B1.5 |
| G13 | The upstream URL is rebuilt from the classified workspace, repository and action, never copied from the raw path; redirects are not followed; a gzip-encoded receive-pack body is refused; git's large-push probe (an update request of only a flush packet) passes as a no-op | B1.3, B1.5 |
| G14 | A run whose image predates the proxy, or that would run on the Batch backend (its VM account is not a job account the service admits, and Batch secrets come from the installation's own config), is not launched on a proxied repository | B1.7's launch check |
| G15 | Whether a repository is proxied has **one** source: the published node's `bitbucket_token` block. The runner, the service and the flip (`fugaro update --repo`) all read it; a local entry that disagrees with the node is refused at update | B1.7, B1.8 |

**Attacker's view, for every review:** the agent can read the runner's `/proc` and the metadata
server (F5), so it holds the run identity and can mint the job's ID token. Review every rule as if
the agent sends the request itself. A launcher is trusted (D475) and can mint any identity, but
not one whose branch fails `task.BranchRE` (G6).

## Slices

| Slice | Release | What | Inert? |
| --- | --- | --- | --- |
| **S0** | **0.13.0** | The secret-reader audit as a tested doc, the guard, doctor's Secret Manager sweep and deployer read, Check 190 written and run | Yes: no behaviour change except doctor findings |
| **S1** | **0.15.0**, then the flip after it (O7) | The Bitbucket proxy for per-repository jobs of opted-in installations and the re-planned Bitbucket S2 tasks ship in 0.15.0 (owner, 2026-10-30); 0.14.0 ships only the inert and data-only pieces — policy v4 with the block and the `proxy` switch (BW-T2.1, BW-T2.3), the API-root pin (BW-T2.8), the bundle's Bitbucket token (B1.2), the proxy's parser and rules (B1.3, B1.4), the empty flip list (B1.8) and the operator draft (B1.13 part one); Check 191 runs against the released 0.15.0; the flip (B1.13 part two) and Check 192 follow in the next release | Inert on upgrade: a repository is proxied only when the owner publishes `proxy: true` (B1.7); the mount stays until the flip list is filled (B1.13); in 0.14.0 no run is proxied at all, whatever the node says. The policy v4 bump itself is a lockstep upgrade (D477) |
| **S2** | unscheduled | Every `agent.auth` mode through the service (D498) | — |
| **S3** | unscheduled | Workflow `secrets:` through a vault fetch (D491) | — |
| **S4** | unscheduled | The build exception's exit: builds and the image check through the service (D491, U4) | — |
| **S5** | unscheduled | Shared project jobs at the service (the credentials plan's C3.9), then Bitbucket shared jobs (D505) | — |

Considered and not planned: the spec's RTDB token store (D494), GitHub through the proxy (D497b),
an HTTP health route and minimum instances (D501), an agent sandbox (O1), a launcher skew constant
for the claim (review round 1: a constant in the new binary cannot stop an older launcher; the
runner's early refusal in B1.7 and the policy v4 bump do).

## The re-planned Bitbucket S2 (D493, provisional on O2)

docs/bitbucket-workspace-token's S2 is re-planned against the proxy. Its task text stays in that
plan; this table says what happens to each task, and the levels below include the ones that ship.

| Task there | Here |
| --- | --- |
| T2.1 (the node's block, `bitbucket_api_roots`, profile rules, policy v4) | **0.14.0, amended** (BW-T2.1): `VersionSince[4]` is 0.14.0's; the block is derived from `project_secrets.bitbucket-token.allow_data_to` **whether or not a profile mounts the token** (a per-repository proxied job needs it as its admission source); it gains `proxy bool`, from the operator's `project_secrets.bitbucket-token.proxy`, which this amendment adds to `config.ProjectSecret` (`internal/config/project_secrets.go`, accepted on `bitbucket-token` only, with its tests) (G3, G9). The one version bump of D493 |
| T2.3 (publish derives the block) | **0.14.0, amended** (BW-T2.3): it publishes the amended block and `proxy` |
| T2.8 (the API-root pin) | **0.14.0, unchanged** (BW-T2.8). Under the proxy it protects the **non-proxied** repositories, whose runner still holds the token; a proxied run's adapter does not read `FUGARO_BITBUCKET_API_URL` (B1.7). An owner-listed root is not served through the proxy in S1 (O4). **Cost of O2's reading:** the pin, ruled for 0.13.0 (D474), reaches non-proxied installations one release later |
| T2.9 (the runner enforces the block) | **Replaced** by the service's check (B1.4, B1.5): a runner-side check binds nothing the agent cannot bypass (F5) |
| T2.2, T2.4, T2.6, T2.7 (Bitbucket shared jobs, the own-copy warning, phase-3 docs, Check 181) | **Wait for S5** (D505). T2.4's warning is moot for proxied repositories |
| T2.11 (the 0.13.0 lockstep notes) | **Replaced** by B1.13's `release-0.14.0-operators.md`, which carries the v4 lockstep steps; T2.11's shared-job step waits for S5 |
| T1.3, T1.4 (Check 180) | Unchanged: later, once a Premium sandbox exists. If T1.3 is scheduled in 0.13.0, B0.4 runs after it (one file) |
| T1.1, T1.2 (0.12.1, APPROVED) | **Unchanged**, plus one pointer sentence in T1.1 (D490), pinned by its own `TestDocsStateTheBitbucketProjectTokenReach` |

## Tasks

### S0 (0.13.0)

#### B0.1: The secret-reader audit, as a tested doc

- **Files:** `docs/secrets-brokering/secret-readers.md` (new); `internal/cli/docs_secrets_brokering_test.go` (new).
- **Interfaces produced:** a table, one row per reader, with a stable ID (`E1`…), a **kind**
  (`runtime` or `operator`), the reader (an account class or a Go package), what it reads, how
  (a `secret_key_ref` mount, `availableSecrets` in YAML or built in Go, Batch `secretVariables`, the
  Secret Manager API), the decision that keeps it (D145, D491, D506 …) and its **exit slice** (S1,
  S3, S4, or "never" for the service's own grant and for operator code). The rows are
  reconciliation §3, plus the Batch backend (`internal/backend/batch` `Options.Secrets`) and the
  build request built in Go (`internal/backend/gcp/build.go`), plus the managed projects' `fmgd-` secrets (the deployer's `secretmanager.secrets.setIamPolicy` in
  `modules/installation/deployer.tf` lets a managed plan grant readers), plus every Go **file** that
  imports the Secret Manager API, each with its kind: `internal/credsvc/bundle.go` and
  `internal/cli/credentials_serve.go` are the service's runtime; `internal/backend/gcp/secrets.go`,
  `internal/cli/creds.go` and the `internal/infra` files are operator code. B0.2 parses this table.
- **Schema/rules/config change:** none.
- **Tests:** `TestDocsSecretReadersTableShape` (every row has an ID, a kind, a decision and an exit
  slice named in this plan's Slices table), `TestDocsSecretsBrokeringReadmeStatus` (the README's
  status line; it reads the README, which B0.4 writes at the same level, and pins only the status
  line). Both match `-run Docs`, so every later docs task's command runs them.
- **Command:** `go test ./internal/cli/ -run 'TestDocsSecretReadersTableShape|TestDocsSecretsBrokeringReadmeStatus' -count=1`
- **Red step:** the shape test first, against a table with a row whose exit slice is `S9`: it fails.
- **Depends on:** none. **Reviewer tier:** standard.
- **Commit:** `docs(secrets-brokering): who reads Secret Manager today, each exception with its decision and exit`

#### B0.2: The guard (D499)

- **Files:** `deploy/terraform/secret_readers_test.go` (new: **the exceptions table lives here**, as
  D499's answer says; the test also checks that `secret-readers.md` lists exactly the same IDs);
  `docs/secrets-brokering/README.md` (index row for `secret-readers.md`).
- **Interfaces consumed:** B0.1's doc (its IDs).
- **Behaviour:** the test finds every reader and maps each to one table row: in
  `deploy/terraform/gcp/**/*.tf`, every `google_secret_manager_secret_iam_member`, `_iam_binding` and
  `_iam_policy`, every project-level grant of a role that carries `secretmanager.versions.access`
  (a predefined Secret Manager role or a custom role listing it), every `secret_key_ref` and every
  `volumes { secret }` (by module and resource name); in `images/**/cloudbuild.yaml`,
  `availableSecrets`; in non-test Go under `internal/` and `cmd/`, every import of
  `google.golang.org/api/secretmanager/v1` and every construction of an `AvailableSecrets` or a
  Batch `secretVariables` value, and every string naming `secretmanager.googleapis.com` (a raw HTTP
  reader), all by **file**, so a new reader file in a listed package fails. A hit with no row fails; a
  row with no hit fails, so the table cannot go stale. A row maps to a resource or a package, so a
  per-class split inside one resource (the job's accessor covers the git token, model keys and
  workflow `secrets:`) is the table's `what` column, not a separate hit.
- **Tests:** `TestEverySecretReaderIsAListedException`, `TestEveryListedExceptionStillExists`,
  `TestSecretReaderScanFindsBindingsVolumesAndCustomRoles` (on fixture `.tf` text),
  `TestSecretReadersDocMatchesTheTable`.
- **Command:** `go test ./deploy/terraform/ -run 'TestEverySecretReader|TestEveryListedException|TestSecretReaderScan|TestSecretReadersDoc' -count=1`
- **Red step:** run against B0.1's table with one row deleted: the first test fails naming the
  resource.
- **Depends on:** B0.1. **Reviewer tier:** opus + mandatory independent review (it fences secret
  access).
- **Commit:** `terraform: a new Secret Manager reader fails the build unless it is a listed exception`

#### B0.3: Doctor's Secret Manager sweep and the deployer read (D503a, D501)

- **Files:** `internal/cli/doctor_secret_readers.go` (new); `internal/cli/doctor_secret_readers_test.go` (new); `internal/cli/doctor.go` (registration only); `internal/backend/gcp/secrets.go` (`SecretPolicy`: a container's policy as bindings, since the role is part of the answer); `internal/gcpfake/runservices.go` (new: a Cloud Run v2 services fake with `getIamPolicy`), `internal/gcpfake/runservices_test.go` (new).
- **Behaviour:** `secret-readers-unexpected` (warning): every member of a role that reads a
  container's values — `roles/secretmanager.secretAccessor`, `roles/secretmanager.admin`, or a
  custom role whose own definition lists `secretmanager.versions.access` — on every
  container of the installation's project that is not one of the account classes Fugaro creates
  for that container (computed forward, as `doctorProjectSecretAccessorChecks` does; the managed
  `fmgd-` accounts on `fmgd-` containers are a known class), or is a principal outside the
  installation; and every principal holding, **at the project level**, a role that carries
  `secretmanager.versions.access` (a predefined Secret Manager role or a custom role listing it),
  other than the classes Fugaro grants — service accounts, and, because a project-level grant is
  every container at once, a person or a group. The account classes are the guard's exception rows
  (B0.2). Owners are reported once, as info (F3); an Editor is not a project-wide reader — its
  container grant is a finding like any other — though it can read a value indirectly, since
  `roles/editor` carries `iam.serviceAccounts.actAs` and `run.services.update` and it may deploy
  code that runs as an account holding a grant (the deployer line below names it). **The remove advice and its
  `gcloud secrets remove-iam-policy-binding` line belong to a container carrying Fugaro's managed
  label**: for a container Fugaro did not create the line reviews its readers instead, because they
  may belong to another application in the same project (the owner's ruling, 2026-10-29). A policy
  or a role that could not be read is named as unread rather than counted as one that grants
  nothing.
  `credentials-service-deployers` (info, only when the installation opted in): the principals that
  hold, on the project or on the service, `roles/owner`, `roles/editor`, `roles/run.admin`,
  `roles/run.developer` or a custom role whose permissions include `run.services.update`, and those
  holding `iam.serviceAccounts.actAs` on the service's account or, through a project-level grant, on
  every account of the project (D501). The two reads take the project's policy and the custom-role
  cache from one lookup each.
- **Tests:** `TestDoctorSecretReadersFlagsAnUnknownAccessor`, `TestDoctorSecretReadersAcceptsFugaroAccounts`, `TestDoctorSecretReadersFlagsProjectLevelAccess`,
  `TestDoctorCredentialsServiceDeployersListsPrincipals`, `TestDoctorCredentialsServiceDeployersExpandsCustomRoles`,
  `TestDoctorCredentialsServiceDeployersSilentWhenNotOptedIn` (all on `internal/gcpfake`), and the
  2026-10-29 follow-up's `TestDoctorSecretReadersAdvisesReviewForAContainerFugaroDidNotCreate`,
  `TestDoctorSecretReadersFlagsEveryRoleThatReadsAContainer`,
  `TestDoctorSecretReadersReportsAContainerRoleItCouldNotRead`,
  `TestDoctorSecretReadersFlagsProjectLevelReadersWhoAreNotServiceAccounts`,
  `TestDoctorCredentialsServiceDeployersCountsProjectLevelActAs`,
  `TestDoctorSecretManagerChecksReadsTheProjectPolicyOnce`,
  `TestDoctorSecretManagerChecksReadsACustomRoleOnce`.
- **Command:** `go test ./internal/cli/ -run 'TestDoctorSecretReaders|TestDoctorCredentialsServiceDeployers|TestDoctorSecretManagerChecks' -count=1 && go test ./internal/gcpfake/ -count=1`
- **Red step:** the unknown-accessor test, with a fake policy carrying a stray user: no finding yet.
  The follow-up's seven tests were run against the merged code before it changed: each failed on the
  behaviour it pins (the foreign container's fix still advised a removal; a container's
  `secretmanager.admin` grant was not named; a person at the project level was not; the project
  policy was read twice without the credentials service and three times with it).
- **Depends on:** none. **Reviewer tier:** standard (read-only, reports).
- **Commit:** `doctor: report every Secret Manager reader Fugaro did not create, and who may deploy the credentials service`
- **Follow-up (2026-10-29, owner's rulings):** `doctor: the sweep's advice for a container Fugaro did not create, and every role it reads` — the four coverage changes above, the Editor wording aligned to the code, and this plan's, `reconciliation.md`'s, `secret-readers.md`'s and `SECURITY.md`'s sentences corrected with it.

#### B0.4: Check 190 written

- **Files:** `docs/gcp-live-checklist.md` (row and section 190); `scripts/live-checks/check190.sh` (new); `scripts/live-checks/check190_test.sh` (new); `scripts/live-checks/check190/main.go` (new: a throwaway reverse proxy to `bitbucket.org` that attaches the token from its own secret and logs request counts and sizes, never a header or a body); `docs/secrets-brokering/check190-steps.md` (new); `docs/secrets-brokering/README.md` (index row).
- **What Check 190 measures** (F13, F14, U2, U7, U8): through a throwaway Cloud Run service on
  installation `fugaro` (project hard-coded, as `check91.sh` does), against a sandbox Bitbucket
  repository: a full clone and a blobless clone (request count, bytes, time), a push of a pack
  above 32 MiB on HTTP/1.1 (chunked) and the front end's answer, a small push, the cold start of the
  first request after idle, and the 3600 s timeout on a slow push. The throwaway service is
  deployed **with IAM required and no public invoker** (the script asserts it with
  `gcloud run services get-iam-policy` before it sends a request), and the script deletes
  everything it created.
- **Tests:** `check190_test.sh` (the hard-coded project, a token file that is not `0600` refused,
  a deploy line without `--no-allow-unauthenticated` refused, `--dry-run` creates nothing); the
  register's docs pins.
- **Command:** `bash scripts/live-checks/check190_test.sh && go build ./scripts/live-checks/check190/ && go test ./internal/cli/ -run Docs -count=1`
- **Red step:** `check190_test.sh`'s public-invoker case before the guard exists: it fails.
- **Depends on:** none (but after docs/bitbucket-workspace-token's T1.3 if that is scheduled in
  0.13.0: both write the register). **Reviewer tier:** opus + mandatory independent review (it
  handles a real token at run time).
- **Commit:** `live-checks: Check 190, git fetch and push through a Cloud Run proxy, written`

#### B0.5: Check 190 — OWNER-RUN

- **What the owner types,** on their own machine, installation `fugaro` only:
  1. `umask 077 && pbpaste > ~/check190.token` (the sandbox repository's access token on the
     clipboard).
  2. `CHECK190_REPO=<workspace>/<sandbox repository> CHECK190_TOKEN_FILE=~/check190.token bash scripts/live-checks/check190.sh --dry-run`
  3. The same without `--dry-run`; paste the `CHECK 190 RESULT` block back; `rm ~/check190.token`.
- **Depends on:** B0.4. No agent runs any step; an agent records the block in the register.
- **Gate:** if the 32 MiB push passes on HTTP/1.1, S1 is built on HTTP/1.1; otherwise B1.5 and the
  service serve h2c end to end, and B1.5 records that before it starts.

#### B0.6: Check 190's result recorded

- **Files:** `docs/gcp-live-checklist.md` (section 190's run record, the owner's block as pasted).
- **Tests:** the register's docs pins. **Command:** `go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (a record); the docs pins must stay green.
- **Depends on:** B0.5. **Reviewer tier:** standard.
- **Commit:** `docs(live-checks): Check 190's run, as the owner reported it`

### S1 (0.14.0's inert and data-only pieces, then 0.15.0's proxy)

#### B1.0: h2c end to end — CONDITIONAL, only if Check 190 fails the large push — SECURITY-SENSITIVE

- **Files:** `deploy/terraform/gcp/modules/installation/credentials.tf` (the container port named
  `h2c`), `internal/cli/credentials_serve.go` (an h2c server), `internal/cli/credentials_serve_test.go`,
  `internal/infra/tf/testdata/golden/*`.
- **Tests:** `TestCredentialsServeSpeaksH2C`; the model path's existing `internal/credsvc` streaming
  tests re-run over h2c.
- **Command:** `terraform fmt -check -recursive deploy/terraform && go test ./internal/cli/ -run 'TestCredentialsServe' -count=1 && go test ./internal/credsvc/ -count=1 && go test -tags terraform ./internal/infra/... -count=1`
- **Red step:** `TestCredentialsServeSpeaksH2C` against today's HTTP/1.1 server.
- **Depends on:** B0.6 recording a failed large push; then B1.5 depends on it. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `credentials service: serve h2c end to end, so a large git push is not cut at the HTTP/1 limit`

#### B1.1: The branch and pull-request claims (D497a as amended, F28, G6) — SECURITY-SENSITIVE

- **Files:** `internal/budget/token/token.go` (`Claims.Branch` as `fb`, `Claims.PRs` as `fpr`, at
  most four), `internal/budget/token/mint.go`, `internal/budget/token/exchange.go` (`Session.Claims`
  reads `fb` and `fpr` back, for B1.7), `internal/budget/token/token_test.go`;
  `internal/cli/launch_budget.go` (`mintBudgetToken` passes the task's branch — `fugaro/<run id>`
  for a first run, `task.Spec.Branch` for a follow-up, **none** for a review or artifact run — and
  `PR` or `ReviewPRs`),
  `internal/cli/launch_budget_test.go`; `internal/credsvc/auth.go` (`Identity.Branch`,
  `Identity.PRs`, G6), `internal/credsvc/auth_test.go`.
- **Interfaces produced:** `Identity.Branch string` and `Identity.PRs []int` (empty for an identity
  minted without the claims).
- **Tests:** `TestMintCarriesBranchAndPRClaims`, `TestSessionClaimsReadsBranchAndPRs`,
  `TestLaunchMintsTheTaskBranchAndPRs`, `TestLaunchMintsNoBranchForReviewOrArtifact`,
  `TestAuthBranchMustMatchBranchRE`, `TestAuthBranchOwnOrEarlierRun`, `TestAuthIdentityWithoutBranchClaim`.
- **Command:** `go test ./internal/budget/token/ ./internal/credsvc/ -run 'Branch|PRClaim|SessionClaims' -count=1 && go test ./internal/cli/ -run 'TestLaunchMints' -count=1`
- **Red step:** `TestAuthBranchMustMatchBranchRE` with `fb = main`: accepted before the check.
- **Depends on:** **the owner's answer to O5** (the claims as amended after review); none in S1
  otherwise (L1 only to stay off BW-T2.1's fixture sweep). B1.4's `fpr` rules wait on O5 too.
  **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `token, credsvc: the run identity names its branch and pull requests, and the branch is always a Fugaro branch`

#### B1.2: The Bitbucket token in the bundle (D506) — SECURITY-SENSITIVE

- **Files:** `internal/credsvc/bundle.go`, `internal/credsvc/bundle_test.go`; `schemas/creds-bundle.schema.json`; `internal/cli/creds.go` (`creds set bitbucket-token`, `creds ls`), `internal/cli/creds_test.go`.
- **Interfaces produced:** a new entry kind (`KindGit`), so `Bundle.Credentials()` (model kinds only) and the cascade never see it; `Bundle.BitbucketToken()` selects by that kind, never by name, and returns the value and its name for audit, never logged.
- **Tests:** `TestBundleBitbucketTokenStrict` (unknown keys, oversize, empty), `TestBundleGitKindNeverACascadeCandidate`, `TestBundleModelAccountNamedBitbucketTokenRefused`, `TestCredsSetBitbucketToken` (stdin only, refused in an agent's session), `TestCredsLsNamesBitbucketToken`.
- **Command:** `go test ./internal/credsvc/ -run 'TestBundleBitbucketToken|TestBundleGitKind|TestBundleModelAccountNamedBitbucketToken' -count=1 && go test ./internal/cli/ -run 'TestCredsSetBitbucketToken|TestCredsLsNamesBitbucketToken' -count=1`
- **Red step:** the strict-schema test with a `bitbucket-token` key: refused as unknown today.
- **Depends on:** none in S1 (L1, as B1.1). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `credsvc, creds: the bundle holds the Bitbucket token, written by creds set`

#### B1.3: The receive-pack request parser (G2, G13) — SECURITY-SENSITIVE

- **Files:** `internal/gitproxy/pktline.go` (new), `internal/gitproxy/pktline_test.go` (new).
- **Interfaces produced:** `ReadUpdateRequest(r io.Reader, max int) (cmds []RefUpdate, consumed []byte, err error)`:
  parses the update request up to its flush packet — `shallow <oid>` lines first, then each
  `old new ref`, the first carrying capabilities after a NUL, which is split off before the ref is
  compared — and returns the bytes read so the caller forwards them unchanged before the pack.
  Refuses an oversized section, a malformed length, a missing flush and any `push-cert` (the runner
  never signs). An update request of only a flush packet (git's probe before a large chunked push,
  `http.postBuffer`) returns an empty list, which the rules pass as a no-op. The caller refuses a
  `Content-Encoding` other than identity before calling it.
- **Tests:** table tests from git's documented update-request examples (with and without
  `shallow`), `FuzzReadUpdateRequest`, `TestReadUpdateRequestBoundedRead`, `TestPushCertRefused`,
  `TestRefComparedAfterNUL`, `TestUpdateRequestFlushOnlyProbe`.
- **Command:** `go test ./internal/gitproxy/ -run 'UpdateRequest|PushCert|RefCompared' -count=1 && go test ./internal/gitproxy/ -run '^$' -fuzz FuzzReadUpdateRequest -fuzztime 60s`
- **Red step:** `TestReadUpdateRequestBoundedRead` with a 1 GiB declared section: no bound exists yet.
- **Depends on:** none. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `gitproxy: read a push's update request, and nothing past it, before the pack`

#### B1.4: The proxy's rules (D496, G2–G4, G12) — SECURITY-SENSITIVE

- **Files:** `internal/gitproxy/rules.go` (new), `internal/gitproxy/rules_test.go` (new).
- **Interfaces consumed:** B1.3; BW-T2.1's amended `Policy.BitbucketToken` and
  `projectpolicy.MatchRepo`. **Produced:** `Classify(method, path string, query url.Values) (Request, error)`
  (git info/refs, upload-pack, receive-pack, or a named REST call, with the workspace and the
  repository; dot segments, encoded separators and case are refused or canonicalised exactly as
  `task.CanonicalRepo` does); `Decide(req Request, id Identity, repo string, block *projectpolicy.BitbucketToken) Verdict`, where
  `repo` is the calling job account's `JobAccount.Repo`; `CheckPRWrite(pr PRFacts, body []byte, id Identity, repo string) Verdict`
  for G12 (source repository and branch, destination unchanged), which the service calls with facts
  it fetched itself.
- **Rules:** git fetch of the identity's repository; git push only after B1.3, each update a
  `refs/heads/<fb>` update (force allowed), no zero new id, no `refs/tags/`, none at all without
  `fb`; REST only the calls `bitbucket.Provider` makes (G4), for that repository, with G12 on every
  pull-request write; the repository admitted by the
  block with `proxy: true`; everything else refused with a reason name.
- **Tests:** `TestRulesFetchOtherRepositoryRefused`, `TestRulesPushOnlyRunBranch`, `TestRulesPushDeleteRefused`,
  `TestRulesTagRefused`, `TestRulesRESTAllowList` (one case per `bitbucket.Provider` method,
  `GET /user` allowed, `/workspaces` and pipelines refused), `TestRulesPRWriteBoundToRun` (another
  pull request's update, comment and ready refused; a create naming another source refused; a fork
  with the same branch name refused; an update changing `destination` refused; merge, approve and
  decline refused), `TestRulesReviewRunCommentsOnlyOnFPR`, `TestRulesNoBranchNoPush`,
  `TestRulesNotAdmittedByBlock`, `TestRulesProxyFalse`, `TestRulesPathTricks`.
- **Command:** `go test ./internal/gitproxy/ -run 'TestRules' -count=1`
- **Red step:** `TestRulesPushOnlyRunBranch` with a `refs/heads/main` update: no rule yet.
- **Depends on:** B1.3, BW-T2.1. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `gitproxy: one repository, the run's branch and pull requests, and only the runner's REST calls`

#### B1.5: The service's Bitbucket route (R1, R2, G7, G8, G12, G13) — SECURITY-SENSITIVE

- **Files:** `internal/credsvc/bitbucket.go` (new), `internal/credsvc/bitbucket_test.go` (new),
  `internal/credsvc/server.go` (`ServeHTTP`'s method-and-path gate admits `GET` and `POST` under the
  Bitbucket prefix only; `routeAct`), `internal/credsvc/wire.go` (the prefix), `internal/credsvc/audit.go`
  (purposes `git` and `bitbucket-api`), `internal/cli/credentials_serve.go` (wiring).
- **Behaviour:** after `RequestFromHTTP`, `Authenticator.Verify`, the job-account binding
  (`s.jobs`, `JobAccount.Repo`) and the kill switches, `Classify` and `Decide`; for a pull-request write, a `GET` of that pull request with the bundle's token and
  `CheckPRWrite`; on allow, the upstream URL rebuilt from the classified parts on `bitbucket.org` or
  `api.bitbucket.org/2.0`, the caller's credentials dropped, the bundle's token attached
  (`x-token-auth` Basic for git, Bearer for REST), both directions streamed, no redirect followed,
  no pack buffered, under a git-route upstream timeout of 60 minutes (the model path's
  `upstreamTimeout` is 30). One audit line per request (G7). An owner-listed API root is not served
  (O4).
- **Tests:** `TestBitbucketRouteForwardsFetch`, `TestBitbucketRoutePushRefusedBeforeAnyPackByte` (the
  fake upstream sees zero bytes), `TestBitbucketRouteGzipPushRefused`, `TestBitbucketRouteStreamsLargePack`,
  `TestBitbucketRouteUpstreamURLRebuilt`, `TestBitbucketRouteNoRedirect`, `TestBitbucketRoutePRWriteChecksSource`, `TestBitbucketRouteBindsJobAccountRepo`, `TestBitbucketRouteAuditNeverEchoesRef`,
  `TestBitbucketRouteOneAuditLinePerRequest`, `TestBitbucketRouteKillSwitch`, `TestBitbucketRouteEndedRun`,
  `TestBitbucketRouteNoTokenInErrors`; `TestServiceHasNoAdminRoutes` stays green (its paths are
  outside the prefix).
- **Command:** `go test ./internal/credsvc/ -run 'TestBitbucketRoute|TestServiceHasNoAdminRoutes' -count=1 && go test ./internal/cli/ -run 'TestCredentialsServe' -count=1`
- **Red step:** `TestBitbucketRoutePushRefusedBeforeAnyPackByte`: the route does not exist.
- **Depends on:** B1.1, B1.2, B1.4, and B0.5's gate. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `credsvc: the Bitbucket route, git and REST, under the run's own rules`

#### B1.6: The runner's loopback forwarder (D495, G5, G11) — SECURITY-SENSITIVE

- **Files:** `internal/gateway/gitforward.go` (new), `internal/gateway/gitforward_test.go` (new),
  `internal/gateway/service_contract_test.go` (`TestGitForwardMirrorsCredsvc`: the forwarder's copy
  of the prefix and headers held to `internal/credsvc/wire.go`, beside the existing
  `TestServiceConstantsMirrorCredsvc` and `TestServiceRulesMirrorCredsvc`).
- **Behaviour:** listens on `127.0.0.1` only; for each request attaches a fresh job ID token
  (Cloud Run IAM), the current run identity and the job name (`X-Fugaro-Run-Job`), drops any
  `Authorization` the caller sent, forwards to the service under the Bitbucket prefix, streams.
  Holds no Bitbucket credential.
- **Tests:** `TestGitForwardLoopbackOnly`, `TestGitForwardAttachesFreshTokens`, `TestGitForwardDropsCallerAuthorization`,
  `TestGitForwardStreams`, the contract test.
- **Command:** `go test ./internal/gateway/ -run 'TestGitForward|MirrorsCredsvc|TestServiceRulesMirrorCredsvc' -count=1`
- **Red step:** `TestGitForwardDropsCallerAuthorization`: no forwarder yet.
- **Depends on:** B1.5 (the prefix in the wire contract). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `gateway: a loopback forwarder for git and Bitbucket that holds nothing and signs every request fresh`

#### B1.7: The runner on the proxy (G1, G9) — SECURITY-SENSITIVE

- **Files:** `internal/gitprov/providers/providers.go` (a Bitbucket job whose published block says
  `proxy: true` and whose installation carries `FUGARO_CREDENTIALS_SERVICE_URL`: no
  `FUGARO_BITBUCKET_TOKEN` read; the adapter keeps `BaseURL = https://api.bitbucket.org/2.0` and is
  given an `http.Client` whose transport sends every request to the forwarder, so
  `httpjson.Client.PagePath` keeps accepting Bitbucket's `next` links), `internal/gitprov/providers/providers_test.go`;
  `internal/gitprov/bitbucket/bitbucket.go` (a proxied adapter with no token); `internal/runner/runner.go`
  (start the forwarder, rewrite `https://bitbucket.org/` to the forwarder with
  `url.<forwarder>.insteadOf` in the runner's and the agent's git environment, credential helper
  variables carrying no token; refuse at bootstrap, with a message naming the launcher's version,
  an identity without `fb` on a first run or a follow-up; a review or artifact run needs none), `internal/runner/project.go` (`remoteHost` and `admissionCheck` accept
  the forwarder's endpoint for a proxied Bitbucket run); `internal/runner/gitproxy_test.go` (new);
  `internal/cli/launch_budget.go` and `internal/cli/recipes_skew.go` (`proxySince`: the launcher
  refuses to launch a proxied repository's run on an image whose base predates S1, as
  `checkCloneImageSince` does for `sealSince`, and on `backend: cloud-batch`, G14), `internal/cli/launch_budget_test.go`.
- **Tests:** `TestProxiedBitbucketReadsNoToken`, `TestProxiedBitbucketAgentEnvHasNoToken`,
  `TestProxiedRemoteRewrite`, `TestProxiedPagingThroughForwarder`, `TestProxiedRefusesIdentityWithoutBranch`,
  `TestProxiedReviewRunNeedsNoBranch`, `TestLaunchRefusesProxiedRunOnOldImage`, `TestLaunchRefusesProxiedRunOnBatch`, `TestProxyFalseUnchanged`, and the existing `TestBitbucketUnchanged`
  (`internal/runner/git_service_internal_test.go`) stays green.
- **Command:** `go test ./internal/gitprov/... -run 'Proxied' -count=1 && go test ./internal/runner/ -run 'TestProxied|TestProxyFalseUnchanged|TestBitbucketUnchanged' -count=1 -timeout 20m && go test ./internal/cli/ -run 'TestLaunchRefusesProxiedRun' -count=1`
- **Red step:** `TestProxiedBitbucketReadsNoToken` with the switch on: the token is still read.
- **Depends on:** B1.6, BW-T2.8 (same `runner.go`), BW-T2.1 (the `proxy` field). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `runner: a proxied Bitbucket run reaches Bitbucket only through the service`

#### B1.8: The Bitbucket flip list, inert (D500)

- **Files:** `internal/config/providers.go` (`ProxiedGitSecretNames = []string{}`);
  `internal/infra/spec.go` (a per-repository `bitbucket_proxied` fact: the installation opted in
  and the **published node's** block admits the repository with `proxy: true`; a local
  `project_secrets.bitbucket-token` entry that disagrees with the node is refused, G15),
  `internal/infra/tfvars.go`, `deploy/terraform/gcp/modules/workflow/variables.tf`,
  `deploy/terraform/gcp/modules/repo/workflows.tf`, `deploy/terraform/gcp/modules/repo/variables.tf`,
  `deploy/terraform/gcp/roots/repo/variables.tf` (the fact's path, as `credentials_service` travels),
  `internal/infra/readiness.go` (its loop over the service-held names learns the proxied list),
  `internal/infra/spec_test.go`; `.claude/rules/credentials-service.md` (paths gain
  `internal/gitproxy/**` and `internal/gateway/gitforward*.go`; a rule that `ProxiedGitSecretNames`
  stays empty until the owner's go after Check 191);
  `deploy/terraform/gcp/modules/workflow/sa.tf` (`proxied_git_secret_names`, mirrored);
  `deploy/terraform/terraform_test.go` (the mirror guard); `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`;
  `internal/infra/tf/testdata/golden/*` (regenerated by `scripts/gen-golden-plans.sh`).
- **Split:** B1.8 ran as two tasks, one per side of the list, because the Terraform half has to be in
  place before Go can write a fact into it. **B1.8a** is the list and the module's side, merged as
  #1338 (0c973faa5): `internal/config/providers.go` (`ProxiedGitSecretNames`, empty),
  `deploy/terraform/gcp/modules/workflow/sa.tf` (`proxied_git_secret_names`, `is_proxied_git`,
  `job_secret_names`, `job_mounts`), the three `variables.tf` files and `modules/repo/workflows.tf`
  (the `bitbucket_proxied` input, `optional(bool, false)`), `deploy/terraform/terraform_test.go` and
  `roots/repo/tests/repo.tftest.hcl` (the mirror guard), and
  `.claude/rules/credentials-service.md`. **B1.8b** is the Go side, this run: `internal/infra/spec.go`
  (`Inputs.Policy`, `RepoSpec.BitbucketProxied`, `resolveBitbucketProxied` and its refusal,
  `mountsServiceHeldSecret`'s proxied clause), `internal/infra/readiness.go` (the drop loop),
  `internal/infra/spec_test.go`, `internal/cli/init.go` (the read of the published node, handed in as
  `Inputs.Policy`, in the same memoized read Readiness's D11 gate makes) and `internal/config/providers.go`
  (the comment on `ProxiedGitSecretNames`, whose "the fact has no producer yet" is no longer true).
  `internal/infra/tfvars.go` needed no change: `repoVars.Repo` is the whole `RepoSpec`, so the fact
  travels as B1.8a's input, and the golden plans do not move (`bitbucket_proxied` is `omitempty` and
  the module's default is false).
- **Behaviour:** with the list empty nothing changes. Filled, only a repository whose
  `bitbucket_proxied` is true loses its job's accessor on and mount of `bitbucket-token`; every other
  Bitbucket repository, opted-in installation or not, keeps both (G9); the build account keeps its own
  (exception, exit S4). `ServiceHeldSecretNames` is not touched.
- **Tests:** `TestProxiedGitSecretNamesMatchTheModule`, `TestEmptyProxiedListChangesNothing`,
  `TestFilledProxiedListDropsMountAndAccessor` (the list overridden in the test),
  `TestFilledProxiedListKeepsNonProxiedRepositories`, `TestProxiedFactFromThePublishedNode`,
  `TestProxiedFactFalseWithoutOptIn`, `TestProxiedFactFalseWhenProxyFalse`,
  `TestLocalEntryDisagreeingWithNodeRefused`, the tftest run.
- **Command:** `terraform fmt -check -recursive deploy/terraform && for r in installation repo; do d=$(mktemp -d); TF_DATA_DIR=$d terraform -chdir=deploy/terraform/gcp/roots/$r init -backend=false -lockfile=readonly && TF_DATA_DIR=$d terraform -chdir=deploy/terraform/gcp/roots/$r validate && TF_DATA_DIR=$d terraform -chdir=deploy/terraform/gcp/roots/$r test || exit 1; done && tflint --chdir=deploy/terraform && sh deploy/terraform/scan.sh && go test ./internal/infra/ ./deploy/terraform/ -run 'ProxiedGitSecret|ProxiedList' -count=1 && go test -tags terraform ./internal/infra/... -count=1` (as `.claude/rules/terraform.md` lists, one `TF_DATA_DIR` per root outside the tree)
- **Red step:** `TestProxiedGitSecretNamesMatchTheModule`: neither side exists.
- **Depends on:** BW-T2.1 (the `proxy` field and the block). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `config, infra, terraform: the Bitbucket token's flip list, empty`

#### B1.9: Doctor's S1 checks (D503b–d)

- **Files:** `internal/cli/doctor_brokering.go` (new), `internal/cli/doctor_brokering_test.go` (new),
  `internal/cli/doctor.go` (registration and the opt-in `--check-bitbucket-token` flag),
  `internal/localcfg/localcfg.go` (the operator's `secret_rotation_days`), `internal/localcfg/shared.go`
  (`Shared()` drops both), `internal/cli/sharedcfg.go` (`sharedForbidden` keeps it local).
- **Checks:** `credentials-service-deployed` (the service and every job account's invoker grant,
  through B0.3's Run services fake and client); `bundle-bitbucket-token` (only with `--check-bitbucket-token`,
  refused in an agent's session: `creds ls` reads the bundle's value, so it is never read by default); `bitbucket-direct` (on any opted-in installation: a
  Bitbucket repository that is not proxied, info; after the flip, a proxied repository whose job
  still mounts a token, warning); `secret-rotation-age` (the latest version's age against
  `secret_rotation_days`, warning only; and "near expiry" against an expiry date the operator records
  per name in `secret_expires`, D503c); `bitbucket-token-valid` (only
  with `--check-bitbucket-token`: reads the bundle with the owner's own credentials, in memory,
  refused in an agent's session, and makes one `GET /repositories/<repo>` per proxied repository,
  as `--check-github-app` does for the App); `model-key-in-agent` (budget off, or no service, for a
  workflow whose `auth` carries a key).
- **Tests:** one positive and one negative per check, on `internal/gcpfake`; `TestSharedConfigDropsRotationDays` (new).
- **Command:** `go test ./internal/cli/ -run 'TestDoctorBrokering|TestSharedConfigDropsRotationDays' -count=1 && go test ./internal/localcfg/ -count=1`
- **Red step:** `TestDoctorBrokeringMountedTokenOnFlippedInstallation`: no check.
- **Depends on:** B1.2, B1.8, B0.3 (the fake). **Reviewer tier:** standard, except the
  `--check-bitbucket-token` read: opus + mandatory independent review.
- **Commit:** `doctor: the credentials service, the bundle's Bitbucket token, rotation age and model keys in reach`

#### B1.10: SECURITY.md and the docs (R3, F22, D501, D504) — SECURITY-SENSITIVE (security claims)

- **Files:** `SECURITY.md`; `website/content/security.md` (regenerated); `docs/git-providers.md`
  ("What the agent gets", Bitbucket); `docs/credentials-service/runbook.md` (rotating the Bitbucket
  token with `creds set` and, while S4 is open, the build's copy too; a cold start and an outage of
  the proxy); `docs/secrets-brokering/secret-readers.md` (the Bitbucket rows' state);
  `internal/cli/docs_security_test.go`, `internal/cli/docs_website_security_test.go`,
  `internal/cli/docs_secrets_brokering_test.go`.
- **What they say:** the agent can act as the run at the proxy: one repository, the run's branch and
  pull requests, the run's life (R3, O1); D462's reach ends for a repository whose installation
  publishes `proxy: true` and fills the flip; the build account still holds the token until S4; cold
  starts and no redundancy (D501); revocation is the kill switch and `fugaro cancel` (D504), and
  until the run's outcome is written (by the runner or the sweeper) an identity and a job ID token
  taken before a cancel keep working, for at most the ID token's hour; a proxied run fetches only
  its own repository, so submodules and private git dependencies in other repositories are refused
  (O6).
- **Command:** `go run ./internal/clidoc && go test ./internal/cli/ -run Docs -count=1 && go test ./deploy/terraform/ -run 'TestEverySecretReader|TestEveryListedException' -count=1`
- **Red step:** a pin for the R3 sentence in `docs_security_test.go`, before the text: it fails.
- **Depends on:** B1.5, B1.7. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `SECURITY, docs: what a proxied Bitbucket run can reach, and what it no longer holds`

#### B1.11: Checks 191 and 192 written

- **Files:** `docs/gcp-live-checklist.md` (rows and sections 191, 192); `scripts/live-checks/check191.sh`
  (new, with a `--after-flip` mode that runs Check 192), `scripts/live-checks/check191_test.sh` (new);
  `docs/secrets-brokering/check191-steps.md` (new); `docs/secrets-brokering/README.md` (index row).
- **Check 191** (before the flip): on installation `fugaro`, opted in, a sandbox Bitbucket
  repository published with `proxy: true`: a run clones, pushes checkpoints and opens its pull
  request through the service; requests made **from inside a run**
  (a throwaway run whose task tells the agent to attempt each one and report the answers; the
  identity is sealed to the execution, so the owner's machine cannot make them) to another repository,
  to `main`, a tag, a delete, another pull request's update, a merge and `/workspaces` are refused
  and audited; a review-only run's push is refused; after the run ends its identity is refused.
- **Check 192** (`--after-flip`): the job no longer mounts `bitbucket-token` and its account has
  no accessor on it; a run still succeeds; `fugaro doctor` reports `bitbucket-direct` clean for the repository; a non-proxied Bitbucket
  repository of the same installation still mounts its token and runs.
- **Tests:** `check191_test.sh` (the project guard, `--dry-run`, and that `--after-flip` asserts
  the mount and the accessor are gone).
- **Command:** `bash scripts/live-checks/check191_test.sh && go test ./internal/cli/ -run Docs -count=1`
- **Red step:** `check191_test.sh`'s project-guard case before the guard exists.
- **Depends on:** B1.5, B1.7, B1.9 (the doctor finding Check 192 reads). **Reviewer tier:** standard.
- **Commit:** `live-checks: Checks 191 and 192, the Bitbucket proxy end to end and the flip, written`

#### B1.12: Check 191 — OWNER-RUN

- **The build:** the **released 0.15.0** (`brew upgrade dimipaun/tap/fugaro`): the credentials
  stage refuses a development build (`init_credentials.go` `resolveCredentialsImage`), so this check
  runs after the release carrying the proxy ships — 0.15.0 under the 2026-10-30 move (O7).
- **What the owner types,** installation `fugaro` only, never `belong`:
  1. `fugaro update --provision-credentials` (installation-wide; at a real terminal; on an
     installation already opted in it re-applies the installation root, so the service runs the S1
     image).
  2. `fugaro creds set bitbucket-token < <file holding the sandbox token>`
  3. Add to the local config's `project_secrets` (the file `fugaro update` reads), exactly:
     `bitbucket-token: {allow_data_to: [<workspace>/<sandbox repository>], proxy: true}`.
  4. `fugaro project policy publish --delete`, then `fugaro project policy publish`, then re-add any
     `fugaro project repos add` entries the delete removed (the v4 lockstep order
     `release-0.14.0-operators.md` states).
  5. From the sandbox repository's checkout: `fugaro update --repo`.
  6. `CHECK191_REPO=<workspace>/<sandbox repository> bash scripts/live-checks/check191.sh`; paste
     the `CHECK 191 RESULT` block back.
- **Depends on:** B1.10, B1.11, B1.13 part one, BW-T2.3, and the 0.15.0 release.

#### B1.13: The operator notes, then the flip (D500, G1) — SECURITY-SENSITIVE, owner-gated

- **Files, part one (0.14.0 L2):** `docs/secrets-brokering/release-0.14.0-operators.md`
  (the v4 lockstep publish, which is every installation's own step in 0.14.0, and — as the proxy
  chain's later release needs them — storing the token with `creds set`, setting `proxy: true`,
  re-applying each proxied repository from its checkout with `fugaro update --repo`, rebuilding
  images below `proxySince`, rotating both copies while S4 is open, and the cross-repository fetch
  consequence, O6; after the 2026-10-30 move the draft says the proxy itself arrives in 0.15.0 and
  that a 0.14.0 Bitbucket run's job still holds its own token). **Part two (after B1.12 PASS and the owner's go):**
  `internal/config/providers.go` (`ProxiedGitSecretNames = []string{"bitbucket-token"}`);
  `deploy/terraform/gcp/modules/workflow/sa.tf` (the mirror); `deploy/terraform/terraform_test.go`;
  `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`; `internal/infra/tf/testdata/golden/*`;
  `docs/secrets-brokering/secret-readers.md` (the job row's `what` loses the Bitbucket token for
  proxied repositories); `.claude/rules/credentials-service.md` (the "stays empty" rule B1.8 wrote
  becomes "holds `bitbucket-token`; change it only with the owner's go"); the release's
  `### For operators` entry, in the flip's own PR, with the bundle-first order (store the token with
  `creds set` and run Check 191's step 6 before any `fugaro update --repo`).
- **Tests:** part one: `TestDocsSecretsBrokeringOperatorSteps` (in `docs_secrets_brokering_test.go`,
  as `docs_bitbucket_release_steps_test.go` pins T2.11's draft), which holds the draft to the
  release the level table gives the proxy chain, with
  `TestDocsSecretsBrokeringReadmeReleaseTable` holding the README's release table and the plan's own
  S1 row to the same release. Part two: B1.8's
  `TestFilledProxiedListDropsMountAndAccessor`, now on the real list; the tftest.
- **Command:** B1.8's command, plus `go test ./deploy/terraform/ -run 'TestEverySecretReader' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- **Red step:** a copy of `TestFilledProxiedListDropsMountAndAccessor` that reads the real list: it
  fails while the list is empty.
- **Red step, part one:** `TestDocsSecretsBrokeringOperatorSteps` before the draft exists (it was
  red on the claim the 2026-10-30 move changed, too).
- **Commit, part one:** `docs(secrets-brokering): the 0.14.0 operator steps, drafted for the release notes`
- **Depends on:** part one: BW-T2.1 and BW-T2.3 (the v4 step the lockstep publishes, and the pieces
  0.14.0 ships; neither the draft nor its release notes wait for the proxy chain now that the chain
  ships later); part two: B1.12 PASS on the released 0.15.0 and the owner's go; it ships in the
  release after that (O7). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `config, terraform: a proxied installation's Bitbucket jobs give up the token`

#### B1.14: Check 192 — OWNER-RUN

- **What the owner types:** from the sandbox repository's checkout, `fugaro update --repo`; then
  `CHECK191_REPO=<workspace>/<sandbox repository> bash scripts/live-checks/check191.sh --after-flip`;
  paste the `CHECK 192 RESULT` block back.
- **Depends on:** B1.13 part two, on the release that carries it; B1.9.

#### B1.15 and B1.16: Checks 191 and 192 recorded

- **Files:** `docs/gcp-live-checklist.md` (each section's run record, the owner's block as pasted).
- **Command:** `go test ./internal/cli/ -run Docs -count=1`. **Red step:** none (a record).
- **Depends on:** B1.12 (B1.15), B1.14 (B1.16). **Reviewer tier:** standard.
- **Commit:** `docs(live-checks): Check 191's run` / `docs(live-checks): Check 192's run`

#### BW-T2.1, BW-T2.3, BW-T2.8

As written in [../bitbucket-workspace-token/implementation-plan.md](../bitbucket-workspace-token/implementation-plan.md),
moved to 0.14.0 and, for T2.1 and T2.3, amended as the re-planned table above says (O2).

### Later slices (unscheduled; each planned in detail when the owner schedules it)

- **B2.1 (S2, D498):** wire `api-key`, `oauth` and the provider routes through the service, so an
  opted-in job of any `agent.auth` has a model credential again; Vertex stays out.
- **B3.1 (S3, D491):** a vault fetch: the runner gets the workflow's `secrets:` from the service at
  bootstrap; the job loses its accessors; a flip list and an owner-run check, as S1.
- **B4.1 (S4, D491, U4):** builds and the image check fetch their credential from the service under
  a build caller class; the build rows leave the table.
- **B5.1 (S5, D505):** shared project jobs admitted by profile at the service (C3.9); then
  docs/bitbucket-workspace-token's T2.2, T2.4, T2.6 and T2.7 against the proxy.

## Decisions to tasks

| Decision | Tasks |
| --- | --- |
| D490 | Slices table; B0.1–B0.5 in 0.13.0; S1's proxy chain in 0.15.0 (0.14.0 ships its inert and data-only pieces, owner, 2026-10-30); T1.1's pointer (the other plan) |
| D491 | B0.1's exit column; B3.1; B4.1 |
| D492 | T1.1, T1.2 unchanged; B1.10 (D462 ends per repository) |
| D493 | The re-planned S2 table; BW-T2.1, BW-T2.3 (amended), BW-T2.8; B1.4 replaces T2.9; B1.13 replaces T2.11 |
| D494 | B1.1, B1.5, B1.6 (no token store task) |
| D495 | B1.6, B1.7 |
| D496 | B1.3, B1.4, B1.5 |
| D497 | (a) B1.1 (as amended, O5); (b) none, deferred |
| D498 | B2.1; B1.9's `model-key-in-agent` |
| D499 | B0.2 |
| D500 | B1.8, B1.13, B1.14 |
| D501/D502 | No minimum instances or probe in any task; B0.3's deployer read; B1.10 states the consequence; B1.7 (opt-in and `proxy: true` only) |
| D503 | B0.3 (a); B1.9 (b)–(d) (`bitbucket-direct` on any opted-in installation) and the spec's "token invalid" |
| D504 | B1.5 (G8); B1.10 (and the cancel window) |
| D505 | B1.7 (per-repository only); B5.1 |
| D506 | B1.2; B1.12 step 2 |

## Parallel execution

**Edges** (from the "Depends on" lines): B0.1→B0.2; B0.4→B0.5→B0.6; (BW-T1.3→B0.4 if both are in
0.13.0). BW-T2.1→{BW-T2.3, BW-T2.8, B1.4, B1.7, B1.8}; (B0.6 failed push→B1.0→B1.5); O5→B1.1; B1.3→B1.4; {B1.1, B1.2, B1.4}→B1.5; B1.5→B1.6;
{B1.2, B1.8, B0.3}→B1.9; {B1.6, BW-T2.8}→B1.7; {B1.5, B1.7}→B1.10; {B1.5, B1.7, B1.9}→B1.11;
B1.10→B1.13 part one; {B1.10, B1.11, B1.13 part one, BW-T2.3}→B1.12; B1.12→B1.13 part two→B1.14;
B1.9→B1.14; B1.12→B1.15; B1.14→B1.16. B0.5's result gates B1.5's start.

| Release | Level | Tasks (concurrent) |
| --- | --- | --- |
| 0.13.0 | L0 | B0.1, B0.3, B0.4 |
| 0.13.0 | L1 | B0.2, B0.5 (OWNER-RUN) |
| 0.13.0 | L2 | B0.6 |
| 0.14.0 | L0 | BW-T2.1, B1.3 (and B1.0 if Check 190 failed) |
| 0.14.0 | L1 | B1.1 (after O5), B1.2, B1.4, B1.8, BW-T2.3, BW-T2.8 |
| 0.14.0 | L2 | B1.13 part one |
| 0.15.0 | L3 | B1.5, B1.9 |
| 0.15.0 | L4 | B1.6 |
| 0.15.0 | L5 | B1.7 |
| 0.15.0 | L6 | B1.10, B1.11 |
| after 0.15.0 ships | L7 | B1.12 (OWNER-RUN, on the released 0.15.0) |
| next release (0.16.0) | L8 | B1.13 part two, B1.15 |
| next release (0.16.0) | L9 | B1.14 (OWNER-RUN, on that release) |
| next release (0.16.0) | L10 | B1.16 |

B1.1 and B1.2 sit at L1 rather than L0 only because BW-T2.1 rewrites policy fixtures in
`internal/credsvc` and `internal/cli` tests.

**Critical path:** S0: B0.4 → B0.5 (the owner's run gates S1's design). S1: B1.3 → B1.4 → B1.5 →
B1.6 → B1.7 → B1.10 → B1.13 part one → B1.12 (owner) → B1.13 part two → B1.14 (owner) → B1.16,
with BW-T2.1 → B1.4 joining it. Under the 2026-10-30 move three levels (L0–L2, the inert and
data-only pieces and the operator draft) ship in 0.14.0 and the proxy chain takes L3–L6 of 0.15.0;
Check 191 runs on the 0.15.0 release, and the flip and Check 192 take the release after it (O7).

**Shared files** (every file written by more than one task, with the levels; no level holds two
writers):

| File | Writers (level) |
| --- | --- |
| `docs/secrets-brokering/README.md` | B0.4 (0.13.0 L0), B0.2 (0.13.0 L1), B1.11 (0.15.0 L6) |
| `docs/secrets-brokering/secret-readers.md` | B0.1 (0.13.0 L0), B1.10 (0.15.0 L6), B1.13 part two (L8) |
| `internal/cli/docs_secrets_brokering_test.go` | B0.1 (0.13.0 L0), B1.13 part one (0.14.0 L2), B1.10 (0.15.0 L6) |
| `internal/cli/doctor.go` | B0.3 (0.13.0 L0), B1.9 (0.15.0 L3) |
| `docs/gcp-live-checklist.md` | B0.4 (0.13.0 L0; after BW-T1.3 if scheduled), B0.6 (0.13.0 L2), B1.11 (0.15.0 L6), B1.15 (L8), B1.16 (L10) |
| `internal/config/providers.go` | B1.8 (L1), B1.13 part two (L8) |
| `internal/infra/spec.go`, `readiness.go`, `tfvars.go`; the repo and workflow modules' variables | B1.8 (L1) only |
| `deploy/terraform/gcp/modules/workflow/sa.tf`, `deploy/terraform/terraform_test.go`, `roots/repo/tests/repo.tftest.hcl`, the golden plans | B1.0 (conditional, before B1.5: golden plans only), B1.8 (L1), B1.13 part two (L8) |
| `internal/cli/credentials_serve.go` | B1.0 (conditional), B1.5 (0.15.0 L3) |
| `internal/credsvc/auth.go`, `auth_test.go` | BW-T2.1's fixture sweep (L0, `auth_test.go` at most), B1.1 (L1) |
| `internal/credsvc/wire.go`, `server.go`, `audit.go` | B1.5 (0.15.0 L3) only; B1.6's contract test reads `wire.go` |
| `internal/runner/runner.go` | BW-T2.8 (L1), B1.7 (0.15.0 L5) |
| `internal/cli/launch_budget.go`, `launch_budget_test.go` | BW-T2.1's fixture sweep (L0, the test at most), B1.1 (L1), B1.7 (0.15.0 L5) |
| `internal/cli/recipes_skew.go` | B1.7 (0.15.0 L5) only |
| `internal/config/project_secrets.go` | BW-T2.1 amended (L0) only |
| `.claude/rules/credentials-service.md` | B1.8 (L1), B1.13 part two (L8) |
| `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go` | BW-T2.3 (L1), B1.9 (0.15.0 L3) |
| `internal/cli/sharedcfg.go` | BW-T2.3 (L1, if it touches `sharedForbidden`), B1.9 (0.15.0 L3) |
| `internal/gcpfake/runservices.go` | B0.3 (0.13.0 L0) only |

## Check numbers

| Check | What it measures | Written by | Run by |
| --- | --- | --- | --- |
| 190 | git fetch, blobless clone and a >32 MiB push through a throwaway, IAM-only Cloud Run proxy; cold start; timeout (F13, F14, U2, U7, U8) | B0.4 | the owner, B0.5 |
| 191 | S1 end to end before the flip; every refusal of G2–G4 and G12, audited; G8 | B1.11 | the owner, B1.12 |
| 192 | After the flip: no mount, no accessor, runs still work | B1.11 | the owner, B1.14 |
| 193–199 | Reserved and unused | — | — |

## Risks

- **Cold starts on every first request after idle** (D501, as ruled). Check 190 records their size.
- **The build account keeps the Bitbucket token until S4**, so a repository's own Dockerfile may
  still reach it at build time (the workspace-token work's U8), and rotation writes two copies.
- **The agent acts as the run** (O1): the proxy narrows the run, not the agent.
- **A trusted launcher can mint an identity for any Fugaro branch and pull request of a repository
  it may launch** (D475, F28); it cannot name a non-Fugaro branch (G6).
- **A cancelled run's identity works until its outcome is written** (D504; B1.10 states it).
- **HTTP/1 body limits** (U2): if Check 190 fails the large push, the service needs h2c end to end
  before S1 starts.
- **The policy v4 bump is a lockstep upgrade** (D477), now in 0.14.0 (O2); O3's "an older launcher
  is refused" holds only with v4 in 0.14.0 (under O2's alternative, B1.7's bootstrap refusal is the
  only guard).
- **Cross-repository fetches are refused for a proxied run** (submodules, private git
  dependencies): O6.
- **The operator steps live in a draft file** until the release PR copies them into
  `### For operators`, as T2.11 of the Bitbucket plan does; the release PR must not forget it.
