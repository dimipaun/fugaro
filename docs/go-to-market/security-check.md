# G3.1 — every secrets-flow claim in the security text, checked against the code

**Status: done 2026-11-11, verification only. Four gaps, each with wording for the owner to approve;
`SECURITY.md` is untouched.** Plan: [implementation-plan.md](implementation-plan.md) G3.1.
Reconciliation: [reconciliation.md](reconciliation.md) R4 ("the SECURITY work is a verification task
(F3), and the owner approves any wording change").

Base: `main` at `c964639f60d9ec2ef9f2b9ac0c4fb07d64f644fc` (2026-11-11). Every `file:line` below is
that commit's: a bare `:NNN` in a row is `SECURITY.md`'s line, a `path:NNN` is the repository's, and
every quoted fragment is a substring of the line it is cited beside — so a reader can check a row
with one `sed -n 'NNNp' SECURITY.md` rather than trusting the paraphrase.

## What this checks, and what it does not

`SECURITY.md`'s claims about **how secrets flow**: the model credential and the gateway, the
credentials service (the switch, the bundle, its grants, its database role, the advisory caps, the
stop flag), the git credentials (GitHub installation tokens, the Bitbucket token, the git proxy,
the read mint), and what one run's job and agent can reach (the environment, `/proc`, the metadata
server, redaction, the bucket grants, the Terraform that mounts and grants them).

It does **not** re-derive the threat model, and it does not restate the accepted limits — those are
the owner's rulings and they are quoted in the document itself. It does not touch `SECURITY.md`:
where a sentence is wrong or incomplete, the gap below carries replacement wording for the owner to
approve. `website/content/security.md` is generated (`go run ./internal/clidoc`) and is not edited
by hand here; a wording change lands once, in `SECURITY.md`, and is regenerated in the same PR.

## The site's security page makes no claim of its own

`website/src/pages/security.astro` renders two whole sections of `SECURITY.md` — "In short" (all six
bullets) and "Reporting a vulnerability" (`sectionLines`, `:165`; the summary list, `:727-729`) — and
its four diagram facts and two diagram lines are each proved against the document by `quoted()`
(`:370`), which fails the build for any phrase `SECURITY.md` does not carry. Three Go tests
(`internal/cli/docs_website_security_test.go`: byte-identity at `:81`, the headings at `:106`, "adds
no sentence of its own" at `:142`) and `website/test/security.mjs` hold the page to that. **So every
secret-flow claim the site makes is a `SECURITY.md` claim**, and the table below covers the site by
covering the document. One presentation fact, not a gap: the diagram's facts are quotes cut short
(the "what is not on by default" fact stops at "the real model key sits in the agent's environment"),
so a reader of the picture alone does not see the credentials-service half of that bullet — which the
page's own summary section, four lines below, prints in full.

## The table

`Holds` means: the code on this base commit does what the sentence says, at the line cited. Every
sentence of the sections named above is in the table; a claim that is only a restatement of another
row is folded into it.

### The model credential, and where it sits

| Claim (`SECURITY.md`) | Holds | Evidence |
| --- | --- | --- |
| `:8` "What is not on by default: the budget gateway, so with no `budget:` block the real model key sits in the agent's environment" | holds | With no gateway the real key is added to the agent's secret names — `internal/agent/env.go:173-174`; with one, the agent gets the gateway token instead — `internal/agent/env.go:350-351` (`out["ANTHROPIC_API_KEY"] = g.Token`) |
| `:8` "Vertex has no dollar cap in any mode" | holds | Vertex takes the OAuth token path with no ledger; `vertex` under `enforce` is refused outright — `internal/runner/gateway.go:789-791` (`VertexBudgetRefusal`) |
| `:69` the gateway is on for `observe`/`enforce` with `api-key`, and for `vertex` under `observe`; it gets a gateway token instead of the real key | holds | `internal/runner/routing.go:292-298` (`GatewayOn`: `api-key`, `vertex`, `cascade`; `observe`/`enforce`) |
| `:69` "the gateway pins the models and charges every call against the caps" | holds | The pin and the ledger are the gateway's own request path; the pin is also exported and reused by the service (`internal/credsvc/request.go:107`, `gateway.RequestRefusal`) |
| `:69` "**With the credentials service on, the keys are out of the job entirely**" | holds | The mount and the accessor drop together — `deploy/terraform/gcp/modules/workflow/sa.tf:188-191` (mounts) and `:148-151` (accessors), both keyed off `local.is_service_held` (`:137`, model names `:89-90`) |
| `:93` job → gateway → service → upstream on a switched-on repository | holds | The launch sets `FUGARO_CREDENTIALS_SERVICE_URL` — `internal/cli/run.go:4593-4618`; the runner reads it and builds a service upstream — `internal/runner/gateway.go:100`, `:367`, `:392-396` (`Kind: gateway.KindService`) |
| `:93` the deploy alone changes no run: the switch needs the bundle to hold a model credential **and** the repository's root re-applied with the switch on, and a non-`cascade` repository is never switched on | holds | The three conditions are one function — `internal/infra/spec.go:287-289` (`CredentialsServiceServes(installed, ready, auth)`); the record `ready` is written only by a reading that parsed the bundle and found a credential — `internal/cli/init_credentials.go:191-199`; the launcher re-reads all three live — `internal/cli/run.go:4601-4614` |
| `:95` "the only Fugaro runtime identity with access to model credentials", with the build account, operators and Project Owners named as the others | holds | The service account's one secret grant is the bundle — `deploy/terraform/gcp/modules/installation/credentials.tf:169-181`; the build account's is `repo.build_secrets` — `deploy/terraform/gcp/modules/repo/build.tf:15-20`; operators add and list versions (`roles/storage.objectAdmin` on the runs bucket, `deploy/terraform/gcp/modules/installation/iam.tf:268`) and read no value |
| `:97` one secret, strict schema, oversize and unknown key refused, read at `latest` with a short cache, one `secretAccessor`, no prefix condition over the model secrets, never printed | holds | `internal/credsvc/bundle.go:858-870` (empty and oversize refused before the parse); the single grant is `credentials.tf:174-181`; `fugaro creds set`/`creds ls` are owner-only and print no value — `internal/cli/creds.go:103-124` (`credsOwnerOnly`) |
| `:103` "The drop is also not per workflow … so a switched-on repository mounts no model credential in any of its jobs" | holds | The mount predicate is auth-blind: `sa.tf:137` drops every `model_secret_env_names` entry and every `FUGARO_PROVIDER_KEY_*` mount whenever `var.credentials_service` is set |
| `:105` the OAuth-token invariant (an OAuth token only to Anthropic's own endpoint) | holds | `internal/gateway/oauth_test.go:206` (`TestOAuthNeverSentToProviderRoute`) and `internal/gateway/export_test.go:53` (`TestExportedAttachRefusesOAuthOnProviderRoute`); the service's tier A rides the same rule — nothing in `internal/credsvc` builds a provider base URL of its own |
| `:107` unreachable halts, never falls back; no fallback path in the gateway | holds | The cascade upstream is built only from the service URL (`internal/runner/gateway.go:367-396`); no in-job key is reachable on a switched-on repository, which is the mount drop above |
| `:121` "the service is wired as of 0.10.1", `FUGARO_CREDENTIALS_SERVICE_URL` is the one name the runner's cascade upstream and the keyless GitHub path both read | holds | `internal/runner/gateway.go:100`; `internal/gitprov/providers/providers.go:37`, `:117` (the keyless open is `if vars[EnvCredentialsServiceURL] != ""`) |
| `:122` the GitHub App key does not move off the job in this release: `config.ServiceHeldSecretNames` is empty until Check 96 | holds | `internal/config/providers.go:575` (`= []string{}`); the module mirrors it — `sa.tf:102`; the two spellings are held together by `deploy/terraform/terraform_test.go:4841` |
| `:123` the five launch refusals for a `cascade` launch | holds | `internal/cli/run.go:3123` (`cascadeLaunchRefusal`), including "publishes no `budget.allowed_models`", which is the service's own fail-closed rule — `internal/credsvc/request.go:71-76` |
| `:205` a `cascade` run has no model key in `/proc` to read, so its calls all go through the gateway | holds | As `:70`/`:103` above: nothing is mounted and no accessor is held |

### The credentials service's database reach and its refusals

| Claim (`SECURITY.md`) | Holds | Evidence |
| --- | --- | --- |
| `:99` reads the run's write-once outcome, the two kill switches, the published policy and another revision's `creds/` state; writes its own `creds/` nodes by compare-and-set | holds | The role is exactly the database's two data permissions — `deploy/terraform/gcp/modules/installation/credentials.tf:241-246` (`firebasedatabase.instances.get`/`.update`), declared in the Firebase project (`:240`, `:274`), one member and no condition |
| `:99` "so the role is a custom one, `fugaroCredentialsDatabase`, declared in the Firebase project the database lives in, bound to that one account and to no other, with **no condition**" | holds | Same resource, `:274-278`; the tftest is `run "credentials_service_on"` — `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl:2584-2593` |
| `:101` an installation whose deployment predates 0.10.1 has no binding and its service refuses every call with *"the run's outcome could not be read"* under `state_unavailable` | holds | The outcome read is the identity check's own — `internal/credsvc/auth.go:200-214` (`OutcomeReader`: `outcomes/$day/$slug/$run` and nothing else) — and it comes first, so a missing binding fails there rather than at a switch or a policy read |
| `:101` the two permissions let anything running as that account write `config/kill/*`, `config/policy/*` and `outcomes/` | holds | Realtime Database IAM has no path scope, which is why the role is a custom one and the comment at `credentials.tf:224-236` says exactly this; the fence is the service's own behaviour |
| `:109` the operator's stop flag is the existing kill switch, read before the bundle, and an unreadable switch is a refusal rather than an off switch | holds | The two nodes are `config/kill/global` and `config/kill/repos/<slug>` — `internal/budget/model.go:42`, `:59`, the same pair `fugaro budget kill --all`/`--repo` writes; the service answers a typed refusal and issues nothing |
| `:111` a call is admitted only for an installation job service account, checked against Google's certificates, and liveness reads the write-once outcome, never the registry entry | holds | `internal/credsvc/auth.go:700` → `:767` (`verifyJob`), whose refusal for a shared project job's account is `:795` (Global Constraint 6, C3.9) |
| `:111` "Until that outcome exists, the identity's own `fx` claim (at most 30 days past the mint) is **the only bound on a replayed identity**" | holds | The accepted replay bound, stated at `:115` and unchanged by the code |
| `:113` "**The service's own cap check is ADVISORY.** … the binding cap stays the in-job gateway's ledger" | **partly — gap G3** | The advisory half is right and the binding-cap half is right. What the sentence also promises — "the service may read a cap and refuse a call over it" — is not built: no code path in `internal/credsvc` reads a spend figure or a per-run cap. The service's own per-call rule is the project's model allow-list pin, and it fails closed for a project that publishes none — `internal/credsvc/request.go:71-76`, `:135-139` (`requestModels` returns `p.Budget.AllowedModels` and nothing else) |
| `:115` the launcher's reach and the replay bounds are accepted limits | holds | Stated as limits, and no code claims to close them |
| `:117` egress is unchanged; what changes is the size of what egress can take | holds | The job's environment on a switched-on repository carries no model credential (the mount drop), and the identity is the run's own |
| `:185` the runner mints no in-job token on a switched-on repository, and the App key's own copy stays mounted until the flip | holds | `internal/gitprov/providers/providers.go:117` (keyless open); the mount survives because the list is empty — `internal/config/providers.go:575` + `sa.tf:188-191` |
| `:131` on a proxied repository the runner reads no token at all and the credential helper answers with none, while the mount survives until the flip | holds | `internal/config/providers.go:637` (`ProxiedGitSecretNames = []string{}`); `deploy/terraform/gcp/modules/workflow/sa.tf:121`, `:131`; the two spellings are held together by `deploy/terraform/terraform_test.go:4967` |

### The git credentials, the proxy and the read mint

| Claim (`SECURITY.md`) | Holds | Evidence |
| --- | --- | --- |
| `:70` a GitHub run gets an installation token for its one repository — contents and pull requests write, issues and metadata read — expiring within about an hour | holds | `internal/gitprov/github/apptoken.go:91-93` (`tokenPermissions`), exported as `RunTokenPermissions` at `:152` |
| `:70` the App has no `workflows` permission | holds | The permission map above has four entries and none of them is `workflows` |
| `:127` the proxy is a route of the service, a repository is proxied only when the installation opted in **and** the published `bitbucket_token` block admits it with `proxy: true` | holds | The local half is `proxy: true` in the operator's own config (`internal/cli/doctor_brokering.go:663-668` states the rule as the code applies it) |
| `:127` the runner opens its provider with no token, starts a loopback forwarder carrying the job's ID token and the run's identity and **no credential of its own**, and rewrites `https://bitbucket.org/` to it | holds | `internal/runner/runner.go:1579` (`startProxiedGit`), `:1701` (`gitops.CredentialVars(r.credURL, "", "")`), `:1707-1708` (the `url.<forwarder>.insteadOf` rewrite), `:1726` (`bitbucketOrigin`); the forwarder is `internal/gateway/gitforward.go:144`, listening on `127.0.0.1:0` at `:149` |
| `:129` the run reaches one repository (the request must equal the calling job account's own), the run's own branch (`refs/heads/<fb>`), and the run's own pull requests | holds | `internal/gitproxy/rules.go:97-99` (`ReasonOtherRepository`, refused at `:647`), `:190`/`:666-677` (the one ref, `want := "refs/heads/" + id.Branch`), `:114-117`/`:727` (`ReasonOtherPullRequest`) |
| `:129` `/workspaces`, Pipelines, a merge, an approve and a decline are refused | holds | The same rules file carries the reason set those paths land on; nothing else in `internal/gitproxy` grants one |
| `:133` the build account still holds the token, and the proxy does not serve a build | holds | The build clones with the build account's own copy of the git secret (`deploy/terraform/gcp/modules/repo/build.tf:15-20`); the proxy is reached only through a job's environment (`providers.go:117`) |
| `:135` the service scales to zero, no minimum instance count, no redundancy, and a run that cannot reach it fails or halts named | holds | `deploy/terraform/gcp/modules/installation/credentials.tf:325-327` (`scaling_mode = "AUTOMATIC"`, `min_instance_count = 0`); the runner builds its upstream from the URL alone and has no second path (`internal/runner/gateway.go:367-396`) |
| `:137` the kill switch stops the service, and `fugaro cancel` is not that switch | holds | `:109` above; the cancel marker is the runner's, and finalize opens a draft through the proxy |
| `:139` a proxied run fetches only its own repository, so a submodule or private dependency in another Bitbucket repository is refused | holds | As `:129`: the repository must equal the job account's own (`rules.go:647`) |
| `:143` the read mint is `POST /v1/bitbucket/read-token`, mints from the bundle's `bitbucket-read-consumer`, and the CLI never reads the bundle for it | holds | `internal/credsvc/bitbucket.go:76` (`BitbucketReadPath`) |
| `:145` two caller classes (the installation's operators by bare `email` claim; the coordinator job's own service account), both empty by default, a run is neither | holds | The classes and their refusals are the service's own; the shared-job and non-job refusals are `internal/credsvc/auth.go:795` |
| `:147` the mint refuses a repository in another workspace by name, and the consumer's key and secret go only to Bitbucket's own OAuth root | holds | `internal/credsvc/bitbucket.go:129` (`ErrReadOtherWorkspace`, the refusal by name); the endpoint the consumer is presented to is the service's own constant and not a repository's configured API root — `:82-83` |
| `:151` one audit line per mint, carrying names and never a value | holds | `internal/credsvc/audit.go`'s rule, and `internal/credsvc/server.go:1023`/`:1047` (`refuse`, `refuseIdentity`) are the two paths that write no line because the identity is not yet verified — the case the rule excepts |

### What one run's job and agent can reach

| Claim (`SECURITY.md`) | Holds | Evidence |
| --- | --- | --- |
| `:66` one container per run, discarded; the agent runs as an unprivileged user with no sudo and no setuid escape, checked by the image self-test on every build | holds | `images/smoke.sh:114-115` (runs as `fugaro`), `:126` (passwordless sudo fails the check) |
| `:67` per-repository identities; a job reads only the secrets its own workflow declares and only its own repository's bucket prefixes | holds | The workflow module's per-job accessor (`deploy/terraform/gcp/modules/workflow/sa.tf:196-201`); the bucket conditions are the installation's (`deploy/terraform/gcp/modules/installation/iam.tf:287-317`) |
| `:67` a project job is not one of those identities: the profile's account, the profile's secrets, and a listed Claude account's OAuth token | holds | `internal/infra/projectjobs.go:56-68` (one job and one account per (profile, provider, account)); `:208` of the document states the mount in full |
| `:68` no command copies a secret value back to the machine; `fugaro secrets` never prints one and nothing writes one to disk | holds | `internal/cli/creds.go:103-124`; the reads are in-memory and refused in an agent's session |
| `:68` the GitHub App pre-check and `fugaro image build <entry>` read a credential on the machine, announced in one line before the read | holds | Both are the operator's own terminal paths, and the announcement is the `warn` the run prints |
| `:68` "The rest read the credentials service's bundle document … `fugaro creds set`, `fugaro creds ls` and `fugaro doctor --check-bitbucket-token` are the commands that maintain and report on it" | **partly — gap G1** | `fugaro doctor --check-bitbucket-token` is gated and parses the document — `internal/cli/doctor.go:534-536` → `internal/cli/doctor_brokering.go:669-708`. But `fugaro setup` and `fugaro update` also read the document, and they are not in the list — `internal/cli/init_credentials.go:141-199` (`credentialsBundleReady`: `store.access` then `credsvc.ParseBundle`), called from the repository door (`internal/cli/init.go:4163`) and the installation-wide backfill (`internal/cli/init.go:2661`, `internal/cli/init_firebase.go:448`) |
| `:68` none of those reads happens in a coding agent's session, and they are refused there | holds | `internal/cli/init_credentials.go:142-149` refuses on the agent marker and on the run marker; the same fence has its own test (`TestTheBundleIsNeverOpenedInAnAgentsSessionOrARunsProcess`, `internal/cli/managed_test.go:898`) |
| `:69` with the gateway on, model keys stay out of the agent's environment | holds | `internal/agent/env.go:173-174` and `:350-351` |
| `:71` logs are isolated in their own bucket and redacted of every mounted secret, including common encodings, and redaction is best effort | holds | `internal/redact/redact.go:808-817` (the std, raw-std, URL and raw-URL base64 encodings of the secret); `internal/agent/redact.go:17-22` states the best-effort boundary in the code's own words |
| `:60` the render step runs with the run's own environment, secrets included, and the runner builds those names only when `!r.review` | holds | `internal/runner/runner.go:2786-2791`; the render command comes from the runner's in-memory workflow and `runRender` is deliberately not a `verify.LoadSettings` caller — `internal/runner/artifact.go:237-256` |
| `:184` the agent shares the container and its user with the runner and can read the runner's environment through `/proc`, including the App key, the real model and provider keys, and the job's service account through the metadata server | holds | `images/smoke.sh:120-123` pins PID 1 for the same reason the reach exists; the metadata server is reachable from the container |
| `:189` on a switched-on repository the model-key item is empty for every workflow, whatever its `agent.auth`; the one exception is a proxied repository's git token, whose mounted copy is still reachable until the flip | holds | The mount drop is auth-blind (`sa.tf:137`) and the proxied list is empty (`internal/config/providers.go:637`) |
| `:195` "With budget off there is no managed file and no routing check at all" | **partly — gap G4** | The routing check is gateway-gated (`internal/runner/runner.go:4365-4369`, and `checkSettingsRouting` returns early — `internal/runner/agentenv.go:459-462` with `internal/runner/gateway.go:754-766`), so "no routing check" holds. "No managed file" does not: a **review-mode** run writes a posture-only managed settings file with the gateway off — `internal/runner/agentenv.go:384-389` (`if gw == nil && !r.review && account == "" { return nil }`) |
| `:197` a launcher can mint a budget identity for any repository | holds | Every launcher and operator mints on the signer account alone — `deploy/terraform/gcp/modules/firebase/iam.tf:34-41`, whose role is the custom one carrying exactly `iam.serviceAccounts.signJwt` (`:18-24`) |
| `:198` a launcher can override a job's environment, including `FUGARO_RTDB_URL` and `FUGARO_FIREBASE_API_KEY` | holds | A launch carries extra `NAME=value` variables merged into the execution's environment — `internal/backend/backend.go:124-133`; the mitigations are the ones the bullet names |
| `:203` the base branch is protected only by the operator's branch protection | holds | The runner pushes only `fugaro/<run-id>` (`internal/gitops/checkpoint.go:54`), while the token itself carries contents write (`internal/gitprov/github/apptoken.go:91-93`) |
| `:207` a shared project job's bucket grant cannot name one slug: the condition covers the whole of `runs/`, `cache/` and `locks/`, and read-only `tokens/` (`objectViewer`, never `objectUser`) | holds | `internal/infra/projectjobs.go:48-53` (`BucketCondition`, `TokenCondition`); the installation's launcher/reader conditions are `iam.tf:283` and `:299-317` |
| `:208` a project job holds its Claude account's OAuth token from the start of every run, and each account job's service account holds `secretAccessor` on its own account's secret and no other | holds | `internal/infra/projectjobs.go:56-68` (one job and account per (profile, provider, account)); the static mount is `projectAccountMount` — `:667`, the account's own container `:687-700`, the platform variable `:701` |
| `:212` the swap to the credentials service changes the mount and nothing else; it is not done in this release | holds | The phase-3 module carries no switch input at all — no `credentials_service` variable in `deploy/terraform/gcp/modules/project/*.tf` — which is why `:103`'s shared-job exception is stated |
| `:224` the metadata server is reachable, by the agent and by every container it starts | holds | The runner reads the job's own token from it — `internal/runner/gateway.go:111` (`defaultMetadataHost`); the container is not fenced from it |
| `:228` the budget caps stay model-only and bypassable on the VM backend | holds | The caps are enforced in-process by the run's own gateway (`internal/runner/gateway.go:787`, `checkBudget`), so a container holding the provider key can call around them |

## The gaps

Four. Each is a sentence the code does not back as written, with replacement wording for the owner
to approve; the third and fourth are wording only, the first two are completeness. Nothing here is a
hole in the design: none of the four makes a run reach a credential it should not.

### G1 — the list of commands that read the bundle document is short by two

**The sentence** (`SECURITY.md:68`, in "Cloud keys stay in the cloud"):

> The rest read the credentials service's bundle document, on an installation that runs the service:
> `fugaro creds set`, `fugaro creds ls` and `fugaro doctor --check-bitbucket-token` are the commands
> that maintain and report on it.

**What the code does.** Five commands read the document today, not the three the sentence names.
`fugaro doctor --check-bitbucket-token` reads it behind its flag
(`internal/cli/doctor.go:534-536` → `doctorBundleChecks`, `internal/cli/doctor_brokering.go:669-708`).
`fugaro setup` and `fugaro update` read it too, through the one function that decides the switch's
record: `credentialsBundleReady` reads the newest version and parses the document with
`credsvc.ParseBundle` (`internal/cli/init_credentials.go:191-199`), and it is called from the
repository door (`internal/cli/init.go:4163`) and from the installation-wide backfill
(`internal/cli/init.go:2661`, `internal/cli/init_firebase.go:448`). The read is the ordinary kind —
the switch cannot be decided without knowing whether the bundle holds a model credential — and it
carries the same two refusals as the rest (`init_credentials.go:142-149`: no read in a coding
agent's session, no read inside a run), which is why this is a gap in the list and not in the
protection. `fugaro doctor`'s *unconditional* service check reads the bundle's version metadata
only (`doctorCredentialsServiceServing`, `internal/cli/doctor_brokering.go:346-379`: `newest`, the
version's state), not the document, so it does not belong in the list.

**Proposed wording** (replacing the sentence's second half, keeping the first):

> The rest read the credentials service's bundle document, on an installation that runs the service:
> `fugaro creds set` and `fugaro creds ls` maintain and report on it, `fugaro doctor
> --check-bitbucket-token` reports on it, and `fugaro setup` and `fugaro update` read it once per
> apply to decide the switch's record for a repository — whether the bundle holds a model credential
> at all. (`fugaro doctor`'s own service check reads the bundle's versions, not the document.)

### G2 — "Once shared project jobs exist" is stale: they exist

**The sentence** (`SECURITY.md:35`, "Inside a shared job there is no IAM boundary between
repositories — the profile is the boundary"):

> Today each repository still has its own job and its own service account, and a project secret is
> mounted on that job only for a name the repository may use … **Once shared project jobs exist**,
> one job carries runs for several repositories …

**What the code does.** Shared project jobs exist on this base commit. `internal/infra`'s
`ProjectJobs` builds one Cloud Run job and one job service account per (profile, provider, Claude
account) of the local config's profiles (`internal/infra/projectjobs.go:56-68`), and an installation
whose local config names a profile gets them from an ordinary `fugaro setup`. The same document says
so four times over in its own limits section — `:103` ("a shared project job is the one job it does
not reach"), `:207` ("a shared project job widens that from one repository to every repository in
the runs bucket, not to a profile, and that regression is stated here"), `:208` (the account's
OAuth token), `:212` ("the phase-3 module has no switch input") — so `:35` contradicts the rest of
the file rather than the code.

**Proposed wording** (the two clauses that date the sentence; the rest of the paragraph stands):

> **Inside a shared job there is no IAM boundary between repositories — the profile is the
> boundary.** A repository's own job and its own service account are the ordinary case, and a
> project secret is mounted on that job only for a name the repository may use … **In a shared
> project job** — one job carrying runs for several repositories, which an installation whose local
> config names a profile already has — the boundary between them is the profile the run is admitted
> under, not an IAM grant, and a project secret in that job's environment is readable by every one
> of those runs.

### G3 — the service's own cap check is described as built; it is not

**The sentence** (`SECURITY.md:113`):

> **The service's own cap check is ADVISORY.** The service may read a cap and refuse a call over it,
> but the binding cap stays the in-job gateway's ledger (D143) …

**What the code does.** The advisory framing and the binding cap are both right, and they are the
owner's ruling of 2026-10-19 (`docs/credentials-service/implementation-plan.md:177`, and
`docs/credentials-service/README.md`). What no code backs is the middle clause: no path in
`internal/credsvc` reads a run's spend or a per-run cap. The service's own per-call rule is the
**model pin** — the project's published `budget.allowed_models` — and it fails closed when a project
publishes none (`internal/credsvc/request.go:71-76`; the only reader of the field is `requestModels`
at `:135-139`, which returns `p.Budget.AllowedModels` and nothing else). The service also applies
the gateway's own request check through the exported symbol rather than a copy (`:102-112`), so the
shape, server-tool, variant and `max_tokens` refusals are the gateway's, unchanged. No other place
in the package mentions a spend figure.

**Proposed wording** — two shapes, because the owner may prefer to keep the ruling visible or to
state the mechanism:

**(a) state what exists, keep the ruling as the reason there is nothing else to state**

> **The service's own cap check is ADVISORY, and today it is not built.** The owner ruled
> (2026-10-19) that the service's per-run caps are advisory, and no code path in the service reads a
> run's spend or a per-run cap: the service's own per-call rule is the project's published
> `budget.allowed_models` pin, which it applies with the gateway's own request check and fails
> closed on a project that publishes no allow-list. The binding cap stays the in-job gateway's
> ledger (D143): nothing relies on a figure the service does not have, and a run's real spend is
> bounded where it always was — by the gateway's caps, by the token cap, and by the provider
> account's own limit (see "Money" below).

**(b) keep the permissive reading and mark it as a possibility, not a behaviour**

> **The service's own cap check is ADVISORY.** The service may in a later release read a run's spend
> and refuse a call it believes is over the run's cap, and that check would be a courtesy rather
> than a boundary; today it reads no spend figure at all, and its own per-call rule is the
> project's published `budget.allowed_models` pin. The binding cap stays the in-job gateway's ledger
> (D143) …

**(a)** is the one I would ship: it is what the code is, and it keeps the ruling's sentence intact.
**(b)** is available if the owner wants the sentence to keep reading as a description of the
service's latitude rather than of its code.

### G4 — "no managed file" is not true for a review-mode run

**The sentence** (`SECURITY.md:195`):

> With budget off there is no managed file and no routing check at all, so a committed
> `.claude/settings.json` can send the agent's calls, and the key, anywhere.

**What the code does.** "No routing check" holds — the check is gateway-gated twice over
(`internal/runner/runner.go:4365`, and `checkSettingsRouting` returns early on `r.routingKnob()`,
`internal/runner/agentenv.go:459-462`, `internal/runner/gateway.go:754-766`). "No managed file" does
not: `writeManagedSettings` is called before every stage whatever the budget is, and its early
return needs the gateway **and** a non-review stage **and** no stage account —
`internal/runner/agentenv.go:384-389`. A review-mode run with the budget off therefore does write a
managed settings file: the review posture (managed hooks, no project MCP server, settings sources
from the user and managed levels alone, `:407-412`), with none of the gateway's routing variables in
it. The file is real and it outranks the repository's own settings, so the sentence's claim that a
committed `.claude/settings.json` can send the calls "anywhere" is still true — but not because the
file is absent.

**Proposed wording** (the one clause):

> With budget off there is no routing check at all and the managed file carries no routing variables
> — a review-mode run still gets one, for the review posture alone — so a committed
> `.claude/settings.json` can send the agent's calls, and the key, anywhere.

### What each wording has to move with it

The docs tests pin sentences, so a wording change is not one edit. Checked one gap at a time:

| Gap | Pinned string that has to move | What else the same PR runs |
| --- | --- | --- |
| G1 (`:68`) | none — no test carries a phrase from that sentence | `go run ./internal/clidoc` (below) |
| G2 (`:35`) | the paragraph's heading sentence, verbatim — `internal/cli/docs_project_wide_git_test.go:119` (`TestSecurityMdStatesTheProjectScopeDefault`, `:104`); the wording below keeps it word for word and rewrites only the two clauses beside it, which no test carries | `go run ./internal/clidoc` |
| G3 (`:113`) | the word `ADVISORY` — `internal/cli/docs_security_test.go:450` (in `TestSecurityMdStatesTheCredentialsService`, `:371`); both wordings above keep it | `go run ./internal/clidoc` |
| G4 (`:195`) | the sentence itself, verbatim — `internal/cli/docs_security_test.go:40` (in `TestSecurityMdStatesTheModelAndItsLimits`, `:17`), so the pinned string changes in the same PR | `go run ./internal/clidoc` |

Two rules hold for all four:

- **`website/content/security.md` is regenerated whatever the section is.** It is `SECURITY.md`
  byte for byte below a generated header, and a test fails on any drift
  (`internal/cli/docs_website_security_test.go:81`), so `go run ./internal/clidoc` runs in the
  same PR even for a "Known limits" sentence the page does not render.
- **`website/test/security.mjs` is not in the way.** The page renders two sections, "In short" and
  "Reporting a vulnerability", and none of the four gaps is in either — so no website test pins
  the changed text. The one website-side consequence is the regenerated byte copy above.
- **A new backticked name has to be a real one.** `TestSecurityMdStatesTheModelAndItsLimits` checks
  every `` `[A-Z][A-Z0-9_]{2,}` `` token the document carries against the environment variables the
  code actually sets or passes (`internal/cli/docs_security_test.go:79-86`), so a wording that
  introduces one has to name a variable that exists. None of the four wordings above adds one.

## Checked, and not a gap

Two readings that look like gaps and are not, recorded so a later reader does not re-open them.

- **`.claude/settings.json` in `:195`.** The sentence names the repository's own committed settings
  file as the thing that cannot reroute the calls *while the gateway is on*, and says the runner
  *writes* the managed file. Both are what the code does: `writeManagedSettings` writes the managed
  path (`internal/agent/settings.go:16`, `/etc/claude-code/managed-settings.json`), and the check
  refuses a settings file that sets a routing key (`internal/runner/agentenv.go:501-507`). The
  document does not claim the repository's file *is* the managed file.
- **The site's diagram facts are cut short.** They are quotations of `SECURITY.md`, not claims, and
  `quoted()` fails the build for any phrase the document does not carry. The full bullet is on the
  same page, in the summary section.

## Adjacent observation, outside this task's scope

`deploy/terraform/gcp/modules/installation/credentials.tf:234` says
"`TestCredentialsServiceDatabaseRoleIsTheTwoDataPermissions` is what holds every part of this
comment together". No symbol of that name exists anywhere in the tree (`grep` over `*.go`, `*.tf`,
`*.hcl` finds the comment and nothing else). The holder is the Terraform test's own assertion —
`deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl:2584-2593`, in
`run "credentials_service_on"`. This is a comment, not a `SECURITY.md` claim, so no wording is
proposed here; it is a one-line change to that comment, and it belongs to whoever next touches the
file (or to the owner as a trivial commit).

## Open questions

1. **G3's shape.** (a) or (b) above — the owner's call, because both say the same thing about the
   binding cap and differ in how much of the service's latitude they describe. My recommendation is
   (a). Either keeps the pinned word `ADVISORY`.
2. **G1 and G2 need no decision, only an approval of wording** — both are completeness. The G2
   wording keeps the paragraph's pinned heading sentence word for word, and neither rewrites a
   clause any test carries, so landing them is one edit to `SECURITY.md` plus
   `go run ./internal/clidoc` for the page, in one PR.
3. **G4 is the one wording that moves a pinned string**: the sentence it rewrites is pinned verbatim
   in `internal/cli/docs_security_test.go:40`, so that want-list entry changes in the same PR (the
   pinned fact stays — a run with the budget off still cannot have its calls rerouted — and only the
   wording moves). `website/test/security.mjs` is unaffected: the page renders "In short" and
   "Reporting a vulnerability", and the sentence is in neither.
