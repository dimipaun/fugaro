# The hosted MCP server: reconciliation and decisions

**Status: design answered by the owner 2026-11-04; M1, M2, M3, M6 and M4's first half are built and
merged on `main`; M5 is built; the rest is not started; this folder approves no
task.**

What the owner's spec ([spec.md](spec.md) §0) says, checked one claim at a time against the code as
it is at this work's base, and then the decisions the planning found.

**Base.** `origin/main` at `3d6ed04d`, read on 2026-11-01, with the repository's own clone; the
findings below were **re-read on 2026-11-04 at `b41709ea`**, and every citation is a path and a line
at that later commit (§7 records what `main` had overtaken and what was fixed in place). The open
pull requests that touch the same ground were read too, in particular #1472
(`docs/cloud-senior-review`, D880–D899), #1481 (`docs/run-without-checkout`, D901–D919) and #1483
(`internal/rigor/rounds.go`).

**How to read it.** Each finding is **Exists**, **Missing** or **Conflicts**, and each carries what
follows from it — a finding with no consequence is not in this document. §4 is the decisions, and
**all fifteen were answered by the owner on 2026-11-04**; the design in [spec.md](spec.md) §1 onward
is now written to those answers. **Every original answer took the recommendation**, and §4's answers
table records the one place where that was briefly not so (D922 was first answered the other way and
restated the same day).

**Amended the same day, after the mandatory independent security review.** The design of **#1487**
merged before the independent SECURITY review of [spec.md](spec.md) §8 that the folder's own rules
require, and that review ran on **2026-11-04**.
Its findings and the owner's answers to them are **§4a**; every one of them is written into the
decision it names as a **dated amendment note beside the original answer** — the original stays, the
note says what moved and why — and the one that is a decision of its own rather than an amendment
takes the **next free number in this folder's block, D936** (§5), while the other decision-shaped
finding, **H3**, is the **D935** the owner answered on 2026-11-05 (§4). **One amendment is
explicitly not the recommendation**: H1, where the owner **accepted and documented** the residual
risk instead. The rest keep the recommendation, or defer a recommendation the owner judged not yet
safe to build.

## 1. What exists

| # | The claim, and where it already holds |
| --- | --- |
| F1 | **The front door's home is decided**: the coordinator has **its own resident service** — D196 in [../coordinator/reconciliation.md](../coordinator/reconciliation.md) (the row headed D196 says the owner ruled against the recommendation), and [../coordinator/README.md](../coordinator/README.md):26 says P7 and P8 "do not start yet: they wait on the front-door design review". **Exists**, as a decision; **not built** (F24). |
| F2 | **A Cloud Run *service* is already admissible in the installer's Terraform cover**: `internal/infra/tf/cover.go:33-34` admits `google_cloud_run_v2_service` and `google_cloud_run_v2_service_iam_member`, added for the credentials service. **Exists** — the new resource type needs no cover change. |
| F3 | **The coordinator's accounts and grants exist, and they are a job's**: `deploy/terraform/gcp/modules/installation/coordinator.tf:71` (its service account), `:92` with `role = "roles/storage.objectViewer"` at `:96` (the runs bucket — read only), `:113` (the app key secret), `:129` (`google_cloud_run_v2_job`), `:275` (the sweep schedule). There is no Cloud Run *launch* grant and no write grant on the runs bucket. **The lines moved and one claim stopped being true after this folder was written**: the M4 task of [implementation-plan.md](implementation-plan.md) landed its first half as #1716, which added `google_service_account.mcp_server` (`:320`) and the `google_cloud_run_v2_service` front door that runs as it, so "there is **no** `google_cloud_run_v2_service` for the coordinator" is no longer a fact about this tree — the service is the front door's and not the sweep's, and the sweep above is unchanged. |
| F4 | **The coordinator's writer holds the project's database admin**: `deploy/terraform/gcp/modules/firebase/iam.tf:138` grants `roles/firebasedatabase.admin` to the coordinator's writer account. |
| F5 | **Firebase ID-token verification exists, and it is exact**: `internal/credsvc/auth.go:1069` compares the audience to the **bare project id**, `:1072` builds the issuer as `https://securetoken.google.com/<projectId>`, and `:1373` (`GoogleKeysURL`) is the public signing-key endpoint the owner's spec names. Its refusals name the failed check and never the value; `internal/credsvc/audit.go` writes one structured line per act and per refusal. **Exists** — the design's §4 adds no verification of its own. |
| F6 | **Identity Platform is initialized with no sign-in providers, and a public sign-up path is refused**: `internal/cli/init_firebase.go:582` records "initialized Identity Platform (no sign-in providers)", `:686` calls `refusePublicSignUp` (`:709`) on a project whose config enables any public sign-up path, and `internal/infra/identityplatform.go:32` is the step's own name. **Exists** — and it is what F26 collides with. |
| F7 | **The database rules already fence a coordinator subtree**: `internal/budget/rules/rules.json.tmpl:10-13` — `"coordinator": { ".read": "USER", ".write": false }`, with `USER` defined at `internal/budget/rules/rules.go:517` as a signed-in, device-allow-listed person. |
| F8 | **The project's people lists exist and are its only permission model**: `internal/cli/user.go:32-33` writes `terraform.launchers` / `terraform.operators` into the machine's own project config, named together at `:66`, applied by the next update — and **the door already admits one person at a time**: `internal/cli/user.go:266` refuses `domain:`, `group:`, `serviceAccount:`, `allUsers`, `allAuthenticatedUsers` and wildcards, and takes `user:<email>` only, for the reason the comment at `:254-257` gives (the launcher and operator roles carry the token signer's own role, so a `domain:` member would hand a whole domain that role). **Exists** — D923's `user:`-only rule is this rule read at the front door. |
| F9 | **The rigor vocabulary, its order and its one off-axis member exist**: `internal/rigor/mode.go:36` (`var Modes = []Mode{POC, Startup, Pro, Critical, Hotfix}`, the members at `:21-30`) is the closed vocabulary, `:46` is `Default = Pro`, `:75` ranks the four on-axis modes and gives Hotfix **0**, and `internal/rigor/resolve.go:116` states why ("off the axis and ranks below every on-axis mode"). |
| F10 | **The run's ceiling and the enforcer exist**: `internal/runner/spend.go:21-34` is the environment the run reads its ceiling from (`FUGARO_MAX_RUN_USD` at `:23`, `Spend` at `:34`), and `internal/runner/gateway.go:141` is "The real credential stays here" — the gateway holds the cap and the agent never sees the key. |
| F11 | **Cancelling is a marker, not a kill**: `internal/cli/cancel.go:114` (`cancelRun`), writing the marker at `:139`. |
| F12 | **A run's launch facts are already recorded**: `internal/runstore/launch.go:18` (`LogURL`). |
| F13 | **Reading a run's logs goes through the Cloud Logging API today**: `internal/backend/gcp/logs.go:29`, on `google.golang.org/api/logging/v2`. |
| F14 | **A run's transcripts land in the runs bucket**, which the coordinator already reads: `internal/runner/session.go:185` writes `session/<id>.jsonl`, and F3's read-only object viewer reaches it. |
| F15 | **`golang.org/x/oauth2` is already a dependency** (`go.mod:16`). There is **no** MCP SDK and **no** OIDC library in `go.mod`. |
| F19 | **The launcher can already set an environment variable for one execution.** `internal/backend/backend.go:124-144`: `Env` (`:133`) and `SecretEnv` (`:144`) are "extra `NAME=value` variables **for this execution only**, merged into the job's own environment (the Cloud Run v2 API merges override env into the job's)", with the budget-token channel as the precedent that already uses one. **Exists** — D924's mechanism needs no new plumbing, only the value and the rule that reads it. |

## 2. What is missing

| # | What is not there |
| --- | --- |
| F16 | **There is no MCP surface at all**: no endpoint, no JSON-RPC handler, no MCP SDK, no tool schema. `ee/coordinator` reads state through `internal/runstore` and writes it through its own sinks; nothing in it answers a protocol. |
| F17 | **There is no authorization server**: no metadata document, no registration, no authorize, no token, no revocation, no PKCE. `go.mod` carries no OIDC library (F15), so the library the owner's open item asks to pick is a **new dependency** the implementation task adds. |
| F18 | **The coordinator cannot launch a run.** F3 lists its grants, and none of them reaches the job: no `roles/run.developer`, and no call from `ee/coordinator` into `internal/backend` (the launch seam `fugaro run` uses). `spawn_task` therefore needs a grant that does not exist. |
| F20 | **Nothing carries a token's limits.** `internal/task`'s `Overrides` (`internal/task/task.go:854-856`) holds review rounds, a budget, a model and a timeout, and `:1324-1325` applies the budget to the agent — but there is no rigor window, no Hotfix flag and no scope vocabulary outside the rigor package's own mode. |
| F21 | **There is no configuration home for the front door's own knobs** — the redirect allowlist of D928 — and no CLI surface to mint, list or revoke an MCP token. |
| F22 | **There is no audit record a person can read.** F5's audit is a structured **log line**; a project operator reading the console or the database gets nothing. |

## 3. What conflicts

| # | The conflict, and what follows |
| --- | --- |
| F23 | **P8's front door is authorized by Cloud Run IAM; the owner's spec is authorized by an OAuth bearer.** `docs/coordinator/implementation-plan.md` (P8) chose the IAM invoker deliberately, recorded as a departure from the MCP specification (**Q16 A / U1**), and wrote it before the MCP authorization model was read closely. The two cannot both be true of one endpoint: an IAM-invoked endpoint never reaches the handler without a Google identity, so the 401-then-metadata dance of the owner's §"Auth flow" never happens. **→ D920.** |
| F24 | **P8 is not started and waits on this design**, so the front door has no implementation to amend: D196's service (F1) is decided but unbuilt (F3 has no `google_cloud_run_v2_service`). The design is therefore a **precondition** of P7/P8 rather than a follow-up to them. |
| F25 | **The owner's "the authorize step redirects the user to Firebase sign-in" conflicts with the project's Identity Platform state.** F6: there are no sign-in providers, and a public sign-up path is refused by the install itself. `docs/mobile-auth/reconciliation.md` found the identical conflict, classified it **Wrong** (`:129`) and ruled on it as **D221**: no provider session reaches a project's database; sign-in stays custom tokens only (`:474` onward). **→ D923.** |
| F26 | **"Store only hashes" conflicts with the coordinator's subtree being readable by every signed-in person.** F7: `coordinator/` is `.read: USER`. Hashes, client registrations, revocation state and the audit trail would all be readable by anyone the installation admits. **→ D922.** |
| F27 | **A token spend limit cannot ride the agent's own budget flag.** `internal/task/task.go:1324-1325` copies an override into `Agent.MaxBudgetUSD`, and `internal/agent/agent.go:115-116` turns it into `--max-budget-usd`. That figure is the agent's own cost estimate, and the repository's own note records how wrong it is for non-Anthropic models (`docs/dogfooding.md:61`). A spend limit enforced there is a limit enforced by the wrong number, and **the gateway is the only place the real stop lives** (F10, and `SECURITY.md`'s "the real credential stays here"). **→ D924.** |
| F28 | **The owner's "Hotfix is a separate opt-in flag" conflicts with reading Hotfix as a point on the axis.** F9: `Rank()` gives Hotfix **0**, deliberately, so that a Hotfix rule can never win a comparison against a Pro or Critical one. A window expressed only as `min`/`max` over `Rank()` therefore admits Hotfix at every minimum — the precise opposite of "off by default". **→ D925.** |
| F29 | **The coordinator's spec §4 says "whoever can reach the project's coordinator is in the project; no extra permission model in v1"** (`docs/coordinator/spec.md:25`), and that sentence is true while reaching the coordinator means holding `roles/run.invoker`. D920 removes exactly that, so the sentence stops describing anything and an authorization rule has to replace it. **→ D933.** |
| F30 | **The console's graph write is blocked on "the front door (coordinator P8, not started)"** — `docs/console-dependency-graph/reconciliation.md:330`, with GV3.1 and GV3.2 blocked in `docs/console-dependency-graph/implementation-plan.md:457-489,511,528`. A front door that authenticates a person *could* carry the write, which is why the design has to say whether it does. **→ D927.** |
| F31 | **The coordinator's spec §7 wants MCP tools that overlap the owner's, with different names** (`docs/coordinator/spec.md:46-53`: `report_intent`, `check_conflicts`, `get_project_state`, `record_override`). Two tool sets on one endpoint is a naming decision the implementation cannot leave open: a client sees one `tools/list`. **→ D931's note.** |
| F32 | **A closed subtree in the project's database is still reachable by everyone the installation lists.** The database grants are **project-level**, so every instance and every node in the project inherits them and the rules do not bind their holders: `deploy/terraform/gcp/modules/firebase/iam.tf:44` gives `roles/firebasedatabase.viewer` to every launcher and operator, `deploy/terraform/gcp/modules/firebase/iam.tf:53` gives `roles/firebasedatabase.admin` to every admin and budget admin, and `deploy/terraform/gcp/modules/firebase/iam.tf:81` gives it to the history account (`docs/design/roles.md:104`: "Any confidential thing placed in RTDB is readable by everyone on either list"). F26's conflict was answered by closing the subtree (D922), and **closing it is not enough**: the conflict stands against any node in this database, whoever writes the rule. **→ D935**, found by the independent review of PR #1715's M3. |

## 4. The decisions

Fifteen. Every one is written **recommendation first**, and every alternative carries its cost. The
owner answers them; an answer picks an option and approves no work. **The owner answered all fifteen
on 2026-11-04** — the answers are recorded under the table, in the owner's own terms, and the
design is now written to them.

| D | The question | The recommendation | The alternative, and its cost | Blocks | Amendment |
| --- | --- | --- | --- | --- | --- |
| **D920** | Is the MCP endpoint public, with the bearer token as the only gate, or does it keep P8's Cloud Run IAM invoker? | **Public.** `allUsers` may invoke, and the handler is the gate. Every unauthenticated path is a metadata document or an authorization endpoint with no work behind it; every refusal is audited. It is the only shape a standard MCP client can use. | **Keep IAM invoker** (P8's recorded departure): every caller — including a chat client — needs a Google identity with the project's invoker role and a way to mint an ID token for it. No standard MCP client can, so the feature is reachable only by software already written against Fugaro; the OAuth flow the owner's spec asks for becomes dead code. Cost of the recommendation: the endpoint is on the public internet and the handler is the whole perimeter (spec.md §8). | Everything. |
| **D921** | Which OAuth library? | **zitadel/oidc** for the protocol machinery, with storage and Dynamic Client Registration supplied by the coordinator (the owner's spec already expects DCR to be hand-written). **Apache 2.0 and OpenID-certified are the library's own claims and this container cannot check either** — the owner answered with the licence **UNCONFIRMED**, which is why confirming it is the first owner check (§6) and a gate on M5. No new runtime. | **ory/fosite**: the same missing DCR, plus a heavier plugin surface for a flow this small. **A standalone third-party server**: a second resident service in a self-hosted product whose point is one service per project, on a licence the owner has not confirmed — the owner's own open item, which the answer does not close for the library it did pick either. Cost of the recommendation: a new dependency in `go.mod`, a library whose storage interfaces must be fitted to a database it has never seen, and **a licence check that has to pass before any code is written**. | Every endpoint in D921's own scope; the implementation plan's first code task — **and M5 waits on the licence**. |
| **D922** | Where do token records, client registrations and the audit trail live? | **A separate top-level subtree with reads and writes refused to everything but the coordinator's service account.** Only hashes are stored, and a database read yields no presentable value. | **Reuse the `coordinator/` subtree.** It is `.read: USER` today (F7), so every person the installation admits may read the client list, the redirect URIs, the revocation state and the whole tool-call audit. The "store only hashes" rule protects the token values and nothing else: the client list, the redirect URIs, the revocation state and the whole tool-call audit have no protection at all. Cost of the recommendation: one more subtree in the rules, one more account able to write it, and a census test to keep both honest. | D920's audit requirement, D929's client secrets, F22. |
| **D923** | How does the person sign in at the authorize step? | **The custom-token flow the installation already has.** The authorize step hands the person to Fugaro's existing signer, and the coordinator verifies the resulting Firebase ID token exactly as F5 already does. No provider page. | **Enable a sign-in provider** so Firebase can host the page the owner's spec describes: it lifts the install's refusal of any public sign-up path (F6) for the whole project, which is the change `docs/mobile-auth` D221 exists to prevent and which that work's reconciliation classifies **Wrong**. Cost of the recommendation: the authorize step is a redirect to a Fugaro-owned page rather than to a provider's, so the flow is one hop longer than a client's most familiar shape, and a project that has *already* enabled a provider keeps it (the design neither enables nor disables one). | The whole auth flow. |
| **D924** | How is a token's spend limit enforced? | **The coordinator passes the token's limit in its own variable on the execution override, and the run's ceiling becomes the tighter of that and the job's own.** The override mechanism already exists (F19) and the gateway already enforces whatever ceiling the run resolves (F10), so the limit is enforced where every other cap is. A variable of its own rather than a lower value written over the job's is what makes "only narrows" **structural**: the run takes the tighter side itself, so a wrong number at the coordinator cannot widen a ceiling. | **Overwrite the job's own cap variable with `min(...)`.** It works, and it puts the whole "only narrows" property in the launcher's arithmetic rather than in the run — one launcher bug away from a widened ceiling. **Through the agent's `--max-budget-usd`** (F27): the limit would be enforced by the agent's own estimate, which the repository's own notes record as wrong by roughly an order of magnitude for non-Anthropic models. **Or: not enforced, merely recorded** — which is no limit at all. Cost of the recommendation: a small core change (one variable in the run's ceiling, `internal/runner/spend.go`) that is not this work's to make unilaterally, plus the launch grant of F18. | D931; the spend half of spec.md §6. | **Amended 2026-11-04** (M-b) — see §4a |
| **D925** | Is Hotfix part of the rigor window or a flag beside it? | **A separate flag, off by default**, gating Hotfix by itself; the window's `min`/`max` read only the four on-axis modes. | **Fold Hotfix into the axis.** F9's `Rank()` is 0 for Hotfix on purpose, so a `min: pro` token would admit a Hotfix request — the opposite of "off by default". Ranking it high instead reverses the fail-open direction `internal/rigor` chose deliberately. Cost of the recommendation: two things to check instead of one, and a client that asks for Hotfix without the flag is refused rather than downgraded. | The rigor half of spec.md §6. |
| **D926** | What is the window's shape, and what if it admits nothing? | **An optional `min` and an optional `max` over the closed mode vocabulary; unset means the project's policy alone.** A window with no member — `max` below `min` — is refused **at mint**, so a token that could never start a run cannot be issued. | **Allow it and refuse every call.** The operator mints a token, hands it to an agent, and every call refuses for a reason neither of them can see; the failure surfaces in the agent's retry loop rather than at the person's keyboard. Cost of the recommendation: a mint-time validation that has to be tested, and a refusal an operator may find pedantic. | D925. |
| **D927** | Which scopes exist in v1? | **`tasks:read` and `tasks:write`, exactly as the owner lists them.** An unknown scope is refused, never ignored. `roadmap:write` is **not** in v1. | **Add `roadmap:write` now** so the console's blocked write (F30) can use the front door. A scope is a wire contract on a self-hosted product: it exists the moment a token can hold it, it cannot be withdrawn without breaking tokens, and the console's own roadmap write design — which has not been written — is what should decide the name and the permission. Cost of the recommendation: the console's GV3.1/GV3.2 stay blocked, and that work comes back to ask for a scope. | The console's graph write. |
| **D928** | Who owns the redirect-URI allowlist? | **The project's own configuration**, with loopback always allowed for native clients; the coordinator holds no vendor URL as a constant. Validated at registration **and** at authorize, exact match, no wildcards, no prefix matching, no custom schemes. | **Hard-code the known client callbacks** the owner's spec names as examples. A vendor's callback URL baked into a self-hosted product goes stale in somebody else's release cycle, and it refuses every MCP client the owner has not heard of. Cost of the recommendation: an operator who wants a hosted client allowlisted writes one configuration line, and there is no zero-config path for a hosted client. | DCR (D928's own scope) and the code path of D923. |
| **D929** | What is an unattended caller? | **A pre-registered confidential client** using the client credentials grant, with limits that are **mandatory**: no rigor window and no spend limit means no token. | **The owner's option B** (exchange a project API key for a token). It contradicts the owner's own non-goal that the keys are not replaced: every place a key is stored grows the whole token lifecycle, and revoking "the key" stops being one act. Cost of the recommendation: a script that already holds a key needs a client id and a secret instead, which is a change to that script, and the owner's spec lists the keys as a deliberate fallback. | The unattended half of spec.md §7. | **Amended 2026-11-04** (M-f) — see §4a |
| **D930** | Lifetimes, rotation, revocation. | **Short-lived access tokens; refresh tokens that rotate on every use with reuse detection, a replayed one revoking the family; revocation as one delete**, reachable from the API and from a person's own surface. | **Long-lived access tokens with no rotation** — what most simple servers do: a stolen token lives its whole life and nothing distinguishes theft from use. Cost of the recommendation: a client that loses a rotated refresh token must sign in again, and the family revocation on a false positive logs a legitimately-working client out. | Every call. | **Amended 2026-11-04** (M-d) — see §4a |
| **D931** | How does `submit_task` start a run, and what does the coordinator see besides the six tools? | **Through the same seam `fugaro run` uses** (`internal/backend`), with the token's ceiling of D924 attached to that execution, and the coordinator's service account gaining the launch role on the project's own job — a **new grant**, flagged for independent review. The coordinator's own tool set (F31) is **served on the same endpoint under its existing names**, so one `tools/list` shows both, and its tools keep their meanings. | **Write a pending-task node and let the coordinator's scheduled sweep launch it** (the pattern P5 already uses): a launch delayed by the sweep interval, a second writer racing the first, and a run that can silently never start while the caller sees success. Cost of the recommendation: a new IAM grant on a public-facing service, which is the one new privilege this design asks for. | `submit_task`, `cancel_task`, and the coordinator's own tool set. | **Amended 2026-11-04** (H2) — see §4a and **D936** |
| **D932** | Where does `stream_logs` read from? | **The run's own recorded artefacts in the runs bucket** (F14), which the coordinator may already read (F3): the answer is the same material the console and `get_result` draw on, it needs no new grant, and it cannot read a log line outside the run. | **Stream Cloud Logging live** (F13): one more grant, this one able to read **every log line in the project**, mitigated only by a filter — the widest privilege in the design, for a tail that `fugaro logs` already serves a person. Cost of the recommendation: the tail lags the run, and a caller that wants live output waits for the next recorded stage rather than watching characters arrive. | `stream_logs`; the coordinator's grant list. | **Amended 2026-11-04** (M-g) — see §4a |
| **D933** | Who is a person, once reaching the endpoint no longer means holding a Google identity? | **The project's own two lists** (F8): a launcher may call the task tools, an operator may also read the audit record. **F29's sentence in the coordinator's spec is replaced by this**, and the audit record of F22 becomes the operator's. | **Any signed-in identity of the project.** It is what F29 says today, and it was defensible only while the platform did the filtering; with a public endpoint it means every Firebase user of the project is a launcher, and the two lists `fugaro user` maintains would govern nothing. **Or: a new permission model** — a role per scope, a table, an admin surface: a second model beside the lists the project already has, for one front door. Cost of the recommendation: an identity that is signed in but on neither list gets a clear refusal, and a project that admits nobody with `fugaro user` has a coordinator nobody can use. | D920's premise; the whole authorization story. | **Amended 2026-11-04** (M-f) — see §4a |
| **D934** | Is the front door **on** for every installation that has a coordinator, or does the owner turn it on? | **Off until the owner turns it on, on the coordinator's own opt-in.** An installation that never opted into the coordinator has no service for anything to reach; one that did gets the endpoint **off** until a person turns it on, and a request to a front door that is off is refused before the handler reads a token. | **On wherever the coordinator is.** An installation that opted in for a *read-only aggregator* would acquire a **public write endpoint** — `submit_task` spends money — as a side effect of an upgrade, which is exactly the surprise the repository's rule about `fugaro update` exists to prevent. Cost of the recommendation: one more switch, one more state on the live checklist, and a client that reaches an installation which has not turned it on is refused rather than redirected. | M4, and the operator story. | **Amended 2026-11-04** (M-e) — see §4a |

### The owner's answers, 2026-11-04

Every decision above was answered on **2026-11-04**. The table is the record in the owner's own
terms: the answer, whether it is the recommendation, and the one-line reason the design carries for
it. **The design in [spec.md](spec.md) and the tasks in
[implementation-plan.md](implementation-plan.md) are written to these answers**, and **no row is
marked "not the recommendation"** — every answer took it, so the design is the recommended shape
throughout. D922 is the one row whose answer was **restated**: it is marked as such there and the
note under the table says what moved.

| D | The answer, 2026-11-04 | Why, in one line | Amendment |
| --- | --- | --- | --- |
| **D920** | **The recommendation** — the MCP endpoint is **public**, the bearer token is the gate, and the six unauthenticated paths are the metadata documents and the authorization endpoints. **Amended 2026-11-04** (M-e) — see §4a | A standard MCP client on a laptop or a phone cannot mint a Google ID token, so P8's IAM invoker makes the feature reachable only by software already written against Fugaro — and spec.md §8 is the price the owner accepts for that. |
| **D921** | **The recommendation** — **zitadel/oidc** for the protocol machinery, with storage and Dynamic Client Registration supplied by the coordinator — **and the licence is UNCONFIRMED.** **The owner or counsel must confirm the licence before any build.** **Amended 2026-11-04** (M-c) — see §4a | Apache 2.0 is the library's own claim and nothing in this container can check it, and a dependency a self-hosted product cannot ship would sink the whole design — so the licence becomes a gate of its own, and it is the first owner check in §6. |
| **D922** | **The recommendation**, in the owner's words of 2026-11-04: the **separate top-level subtree** — **not under `coordinator/`** — is the **`mcpauth`** node, **not readable by any client** (`.read: false`) and with **`.write: false`**; **only the coordinator's account writes it**, because that account holds `roles/firebasedatabase.admin` and the rules do not bind it **Amended 2026-11-04** (H3) — see §4a | The separate subtree is what keeps the coordinator's shared view and the token records off one node, and the closed read is what keeps F26's finding closed against every client the rules bind — **and the amendment is what keeps it closed against the accounts IAM exempts from them**. The separate subtree, the closed read, the keyed hashes, the MAC and the public service's viewer-only grant are now **five independent barriers** rather than two, which is the honest answer to a review that showed the first two were not enough on their own. **This answer was restated the same day** — see the note under the table — and the restatement plus this amendment are the ones every document is written to. spec.md §6 and §8 state both, and M3's red step is the "named and denied to every client" one. |
| **D923** | **The recommendation**, with the mechanism named: **a PAIRED DEVICE approves the sign-in**, and **the coordinator never mints a custom token**; **v1 admits only exact `user:` launcher entries, and refuses `group:`, `domain:` and `serviceAccount:` entries**; **the Firebase ID token is consumed only at `/oauth/authorize` and is never a grant at `/oauth/token`** **Amended 2026-11-04** (H1, not the recommendation) and again (M-a) — see §4a | The project's Identity Platform is initialized with no sign-in providers and refuses every public sign-up path (F6), so there is no hosted page to redirect to and enabling one is the exact reversal `docs/mobile-auth` D221 refused — and the pairing the repository already has (`fugaro pair`, D165) is the signer that does not make the coordinator one. A `user:` entry is one person (F8's lists), where `domain:`, `group:` and `serviceAccount:` are refused **at `fugaro user` already** (`internal/cli/user.go:266`). Consuming the ID token at authorize is what keeps it out of the token endpoint's grant list. |
| **D924** | **The recommendation** — the token's limit travels in **a variable of its own** on the execution override, and the run's ceiling is the tighter of it and the job's own — and **spend is counted across the whole token family**, so a rotated refresh token is the same budget and not a new one | The override mechanism already exists (F19) and the gateway is already the place the real stop lives (F10, F27); a variable of its own is what makes "only narrows" structural rather than a launcher's arithmetic, and a per-family counter is what stops rotation from being a way to ask for the limit twice. | **Amended 2026-11-04** (M-b) — see §4a |
| **D925** | **The recommendation** — Hotfix is a **separate flag, off by default**, beside the window | `Rank()` gives Hotfix **0** deliberately (F9, F28), so folding it into a `min`/`max` window admits Hotfix at every minimum — the opposite of "off by default". |
| **D926** | **The recommendation** — an optional `min` and an optional `max`, unset meaning the project's policy alone; a window that admits nothing is refused **at mint** | A token that could never start a run should not be mintable: the failure has to surface at the person's keyboard rather than in an agent's retry loop. |
| **D927** | **The recommendation** — `tasks:read` and `tasks:write` only; `roadmap:write` is **not** in v1 | A scope is a wire contract: it exists the moment a token can hold it, and the console's own roadmap-write design — which does not exist yet — is what should decide its name and its permission (F30). |
| **D928** | **The recommendation**, tightened by the owner: the redirect-URI allowlist is the **project's own configuration**, with loopback always allowed and validated at registration and at authorize, exact match — and **registration refuses a non-loopback URI that is not on the allowlist**, **the consent page shows the redirect host**, and **the flow is CSRF-bound** | A vendor's callback URL baked into a self-hosted product goes stale in somebody else's release cycle and refuses every client the owner has not heard of. The three tightenings are the owner's: refusing at registration is the earlier door, a person consents to a host they were shown rather than to a client id, and a flow with no CSRF binding lets an attacker's client finish a sign-in the person started for another. |
| **D929** | **The recommendation** — a pre-registered confidential client using the client credentials grant, with **mandatory** limits | The owner's option B would make every stored API key an OAuth client by another name, and revocation would stop being one act — against the owner's own non-goal that the keys are not replaced. | **Amended 2026-11-04** (M-f) — see §4a |
| **D930** | **The recommendation** — **short-lived access tokens and refresh tokens that rotate on every use**, with reuse detection, a replayed token revoking the family, and revocation as one delete | Rotation is what makes a theft detectable rather than merely survivable: a long-lived token that is stolen lives its whole life and nothing distinguishes theft from use. | **Amended 2026-11-04** (M-d) — see §4a |
| **D931** | **The recommendation**, with the owner's three tightenings: the tool is **`spawn_task`** (the owner's name for the `submit_task` of §0), it starts the run through the same seam `fugaro run` uses (`internal/backend`), the coordinator gains one **new launch grant**, and both tool sets are served on one endpoint under their existing names — and **there is a single launch path**: concurrency, rate, idempotency and the kill switch all go through the **same pre-launch code as `fugaro run`** (`budget.Precheck`, `internal/budget/precheck.go:58`, and the launch claim, `internal/runstore/launch.go:112`) | The sweep alternative delays a launch by the sweep interval, races a second writer, and can leave a caller seeing success for a run that never starts. A second pre-launch path beside `fugaro run`'s is the one that drifts: a front door that skipped the kill switch or the claim would spend money a person's launch could not, and would let one submit become two runs. | **Amended 2026-11-04** (H2) — see §4a and **D936** |
| **D932** | **The recommendation** — `stream_logs` reads the run's own recorded artefacts in the runs bucket | The coordinator may already read them (F3, F14), the answer is the same material the console and `get_result` draw on, and it cannot read a log line outside the run — where streaming Cloud Logging live needs a grant that reaches every log line in the project. | **Amended 2026-11-04** (M-g) — see §4a |
| **D933** | **The recommendation** — **only the project's launcher and operator lists may call the front door**: a launcher may call the task tools, an operator may also read the audit record — and the entries those lists admit are D923's answer, **exact `user:` entries only** | With a public endpoint, "any signed-in identity of the project" (F29's sentence) would make every Firebase user of the project a launcher, and the two lists `fugaro user` maintains would govern nothing. Admitting only `user:` entries is the same rule `fugaro user` already applies at its own door, so the front door cannot be the way a `domain:` entry gets in. | **Amended 2026-11-04** (M-f) — see §4a |
| **D934** | **The recommendation** — the front door is **OFF** on every installation until a person turns it on, on the coordinator's own opt-in | An installation that opted into the coordinator for its read-only aggregator must not acquire a **public write endpoint** — `spawn_task` spends money — as a side effect of an upgrade. | **Amended 2026-11-04** (M-e) — see §4a |

**The four the owner read first.** **D920** decided whether the owner's spec is implementable at
all, and it reverses a departure the coordinator's own plan recorded deliberately — the answer took
the recommendation, so the reason was judged worth it and spec.md §8 is the price. **D923** was not a
design choice but a collision with a decision already taken once (`docs/mobile-auth` D221) on the
identical question; the answer keeps that decision and names the signer — a paired device, with the
coordinator never minting a token. **D922** is the one place where "store only hashes" is not enough
by itself; the answer took the recommendation, so the subtree is closed and nothing about the
metadata is exposed at all. **D924** is the one place where the owner's own words, read literally,
would produce a limit that does not hold; the answer takes the recommendation, so the limit holds.

**What each answer changes.** A different answer to D920 or D923 is a different document: the flow,
the endpoint's shape and §8 all move. A different answer to D927, D928, D930, D932 or D934 is a
parameter and an edit. A different answer to D922, D924, D929 or D931 is an edit plus a task in the plan that
would otherwise not exist — the plan's task list names which.

**The answer D922 was restated, and the restatement is the record.** D922 was first answered the
other way — a closed-to-writers subtree the owner made **readable by any signed-in user of the
project** — and the owner restated it on the same day, **2026-11-04**, as the recommendation itself:
**`.read: false`, `.write: false`, and only the coordinator's account writes it.** The folder is
written to the restatement, because it is the later answer and the tighter one: the exposure the
first answer accepted is not in this design at all, §6 and §8 state a closed subtree, M3's red step
is the "named and denied to every client" one, and **every sentence the first answer put into these
documents has been removed rather than left standing** — a reader who takes the folder's wording as
it now is cannot arrive at the exposure the first answer accepted. Nothing about the
recommendation's own reasoning changes; only which answer is in force.

**What the answers of 2026-11-04 actually changed**, now that they are all in: **no answer deviates
from the recommendation**, so the design in spec.md §1 onward is the recommended shape throughout —
and what the day's answers added is detail rather than a different design. Four of them are
load-bearing:

- **D922's restatement** closed the subtree, so spec.md §6 and §8 carry a refusal rather than an
  accepted exposure, and M3 asserts the denial.
- **D923's mechanism** — a paired device, no custom token minted by the coordinator, the ID token
  consumed only at authorize — moved §3's flow, §2's token-shape paragraph and §8's Firebase-token
  paragraph, and added the `user:`-only entry rule (spec.md §4).
- **D931's single launch path** named `spawn_task` and put concurrency, rate, idempotency and the
  kill switch on `fugaro run`'s own pre-launch code, which is what M9 builds against.
- **D928's three tightenings** — refusal at registration, the redirect host on the consent page, and
  a CSRF-bound flow — are in §3's registration rules, §3's flow and §8's redirect-URI paragraph.

**D921's licence, unconfirmed when the answers were given,** added the first owner check in §6 and a
build gate in the plan — **a gate the owner has since answered himself** (§4a and §6, check 1): he
confirmed the library on 2026-11-06, and what the check still names as facts rather than as gates are
**counsel's clearance of the transitive dependencies** and **the gate file the check describes**.

**The owner answered the licence check himself on 2026-11-06: `zitadel/oidc` is Apache 2.0 and
usable.** That is the answer to §6's first check, given for M5's build, and it is what M5 takes
its dependency against — so **the check is answered rather than unmet, and the gate it named is
answered by his word rather than by the file the sentence had presumed.** The check's sentence was
written before he answered, which is why it names a file a person writes. **The facts it named stay
facts**: no gate file is in the tree, and counsel has not been asked for the transitive set this
branch brings — `zitadel/schema`, `muhlemmer/gu` and `kr/text`, which are new, and the version
bumps `go mod tidy` made beside the library: `go-jose/v4` v4.1.4→v4.1.6, **`go-logr/logr`**
v1.4.3→v1.4.4, `doublestar/v4` v4.10.2→v4.10.4, `otel` v1.44.0→v1.46.0 with `/metric` and
`/trace`, `x/text` v0.42.0→v0.43.0 and `x/sync` v0.23.0→v0.24.0. **The owner's answer is the later
word and the one M5 follows**: the dependency is taken against the library he confirmed, and a
licence that had turned out to be unusable would still send D921 back to its alternatives — the
library out of `go.mod` and M5 rebuilt without it.

The
**status of the whole folder** moved from "design only, nothing approved" to the line every file
now carries — **design answered by the owner 2026-11-04; M1, M2, M3, M6 and M4's first half are
built and merged on `main`; M5 is built; the rest is not started; this folder approves no task.**

### The answer of 2026-11-05

**One question was put to the owner after that day, and it moves where the records live rather than
what they are.** The **independent SECURITY review of PR #1715's M3** — the rules change that closed
the `mcpauth` node — found that the closed node cannot do what D922 asked of it, and the reason is a
fact about the installation rather than about the rule: **F32**. The findings above were read again
for it, and the owner answered on **2026-11-05**, again as recommended. **The answer carried one
thing over: which separate store.** The plan's §8 put three candidates to the owner — a second
Realtime Database instance, Firestore, and a bucket of its own — and **the owner answered that too,
the same day, again as recommended: the bucket** — `fugaro-mcpauth-<gcp-project>`, the records under
`mcpauth/`, one IAM binding for the front door's own account `mcp_server` and nobody else. That
closes the Open item §8 carried, and the one-line why is D935's own ruling read onto the three:
D935 answered **"a store of their own"**, and the bucket is the only candidate whose IAM can name
**exactly one account** — the second instance inherits the project-level database grants until four
sets of people are re-scoped, and Firestore's data roles cannot be narrowed to a database or a
collection at all. **So M2 and M4 wait on nothing**, and the two corrections the two reviews of this
work produced are in §6's check 8 and in the plan's §3 (M4).

| D | The answer, 2026-11-05 | Why, in one line |
| --- | --- | --- |
| **D935** | **The recommendation: the `mcpauth` records — the client list, the redirect URIs, the token hashes, the tool-call audit and the revocation state — leave the project's shared Realtime Database** for **a store of their own, whose IAM names the front door's own account `mcp_server` and no other member**, and **the `mcpauth` node stays in the rules as a defence in depth**, with `.read: false` and `.write: false`. **Which store was left open, and the owner answered it the same day**: the plan's §8 compares a second Realtime Database instance, Firestore and a bucket of its own, and **the answer of 2026-11-05 is the bucket** — the recommendation, `fugaro-mcpauth-<gcp-project>` with the records under `mcpauth/` — because it is the only candidate whose IAM can name exactly one account (the §4 note below, and §6's check 8). | Every launcher and operator holds `roles/firebasedatabase.viewer` on the whole project and every budget admin and the history account holds `roles/firebasedatabase.admin` (F32), so **no rule can keep them out of the project's database** — the records have to leave it, and the store that holds them has to be one whose IAM can name exactly one account. |

**What it moves, and what it does not.** [implementation-plan.md](implementation-plan.md) is written
to it: M2's store, M3's node and the comment it carries, M4's store and its single grant, M5's writes
and M10's audit record and revocation. **The store's own choice moves two more sentences and no
task**: M2's backend file is the bucket, and M4 creates it — see the plan's §8, whose Open item this
answer closes. **Nothing else moves.** D922's rule stands — only hashes, shown
once at mint, never again — D930's rotation and its one-delete revocation stand, and D933's operator
read of the audit record stands, because a tool serves it and no node does. **spec.md is not edited**:
§6 and §8 there still describe the records as living in the `mcpauth` subtree, because that file
carries the owner's spec and the answers of 2026-11-04, and the plan wins where the two disagree.

**Why it is not a contradiction of D922, which answers the same question.** D922 answered *where the
records live* and its answer was the closed subtree; D935 answers it again, one day and one review
later, because the premise D922's answer rested on — that a closed subtree is reachable by the
coordinator's account and by nobody else — is false in the project's database. The two rows differ in
exactly one thing: **the store**. D922's other half, `.read: false` and `.write: false` on the node, is
kept by D935 as the second barrier rather than as the barrier, and the record of the 2026-11-04 answer
above is left standing rather than rewritten, so a reader can see what moved and why.

**And the same finding caught the recommendation once (corrected 2026-11-05).** The plan's §8 first
named the recommended bucket under the managed prefix `fmgd-`, so that the managed deployer's
conditioned storage role would reach it — and that role is a **project-level** binding
(`deploy/terraform/gcp/modules/installation/deployer.tf:379`, conditioned on the prefix at `:142`),
while every operator holds `roles/iam.serviceAccountTokenCreator` on the deployer's own account
(`deployer.tf:501`). A bucket so named would have been readable, rewritable and re-grantable by every
operator and by every managed apply: **F32 again, one resource over**, found by this work's own
review rather than by the installation. The store's name is therefore outside every conditioned
prefix, and the owner's `fugaro update` — which runs with the owner's own rights — is what creates it.

**And the floor under every candidate is wider than "the project Owner" (corrected 2026-11-05, the
second correction this work's review produced).** The sentence above said, and §8's table used to
say, that the project Owner is the one identity besides the store's own member that reaches a bucket
of its own. `SECURITY.md:88` says otherwise in the installation's own words: **"Project Owners,
Editors and anyone with a project-level storage role bypass the bucket's own policy regardless,
hardened or not"**, and `docs/design/bucket-iam.md:47` gives the second half — GCS attaches
**convenience bindings** to a new bucket, `projectOwner:`/`projectEditor:` as
`roles/storage.legacyBucketOwner` and `projectViewer:` as `roles/storage.legacyBucketReader` plus
`roles/storage.legacyObjectReader` (`internal/gcpfake/gcs.go` models them, `ConvenienceBindings`),
so **a basic project Viewer reads the bucket until somebody strips those bindings** — which is
exactly what `fugaro setup` does to the state bucket (`internal/infra/statebucket.go:140`,
`RemoveProjectViewers`, from `CreateStateBucket`). **The floor is therefore: Owners and Editors and
any project-level storage role, plus the Viewers' convenience grant until it is removed.** It is a
floor under every candidate of §8 — a second database instance and Firestore are reached by the
project-level grants instead — and what D935 buys is unchanged: the store names **nobody else**, and
every member a bucket brings with it that Terraform did not put there is M4's to remove or refuse.
**UNVERIFIED, and it is M4's to close with the owner's live check: whether GCS still attaches these
bindings to a bucket created by Terraform rather than by the `fugaro setup` call above.** What is
verified is that the design assumes it does (`internal/gcpfake/gcs.go`, `docs/design/bucket-iam.md`)
and that M4's check reads the created bucket's policy as a basic project Viewer rather than trusting
the assumption.


## 4a. The independent security review of 2026-11-04, and the owner's answers

**The review ran on 2026-11-04**, after the design of **#1487** had already merged, and it is the
review [README.md](README.md) and §6 record as the precondition of every task in
[implementation-plan.md](implementation-plan.md): it read **[spec.md](spec.md) §8** in full, every
**SEC**-marked task of the plan, and the one new privilege the design asks for. The owner answered
its findings the same day, and **each answer is written beside the decision it names as a dated
amendment note** — the original answer stays in the table above, unedited, because **a decision's
number is never reused for a new meaning**. One finding is a decision in its own right and takes the
**next free number in this folder's block, D936**; another, **H3**, is the finding **D935** already
answers — §4's answer of 2026-11-05 (F32), reached from the installation's own grants by the
independent review of PR #1715's M3 and from **§8** by this review; and three are low, and they land
in the tasks rather than in a decision.

**One answer is not the recommendation.** **H1** — the paired-device approval against a launcher who
can mint a device token — is **ACCEPT AND DOCUMENT**. The review's recommendation (a device-held
signing key) is **not taken, not built and in no task**, and what stands in its place is a stated
**accepted residual risk** with its consequences and with the change that would close it. Every other
answer takes the recommendation, or defers a recommendation the owner judged not yet safe to build
(H2, D936).

**What the amendment moves, in one line each.** D920 gains the per-route rule an `allUsers` service
needs; D921 gains four constraints its defaults would otherwise reverse, with its licence still
**UNCONFIRMED**; D922 gains the records' marks — keyed hashes, a MAC over each — and loses the
assumption that a closed rules node is closed to IAM; D923 gains an accepted residual risk and a hardened device approval; D924 gains an atomic
family reservation; D929 gains a switch, off until it has an owner; D930 gains a generation-conditioned
rotation; D931 loses `spawn_task` to D936; D932 gains redaction; D933 gains the lists' source;
D934 gains the meaning of "off".

### D920 — amended (finding M-e)

`allUsers` on a Cloud Run **service** publishes **every route the service serves**, not only the MCP
endpoint. The coordinator's own P8 tools — `report_intent`, `check_conflicts`, `get_project_state`,
`record_override` (`docs/coordinator/spec.md:46-53`, F31) — would be exactly as reachable as
`list_tasks`. The amended answer, which [spec.md](spec.md) §2, §5 and §8 state:

- **Every tool maps to a scope, and a tool with no mapping is refused.** A tool registered without a
  scope is not served, which is what makes "the six tools are public" true of the *source* and not
  merely of the documentation.
- **The service serves no route that is not the MCP endpoint or one of the six unauthenticated
  paths** (§2).
- **`max-instances` is capped and the request body size is capped**, so an unauthenticated caller
  cannot make the public service scale or read.
- **An unauthenticated refusal is logged and is never written to the database**, so a stranger with a
  loop cannot grow the project's storage.

M4 and M7 carry the rules; §8 states the risk.

### D921 — amended (finding M-c)

**The licence was UNCONFIRMED when this amendment was written, and the owner answered it himself on
2026-11-06** (§4a, the paragraph on it): `zitadel/oidc` is Apache 2.0 and usable, and that answer is
the build gate for M5. **The answer is the owner's own word**, given for this task and recorded
here; a transitive set counsel has not been asked about and a gate file no one has written are still
absent from the tree, and neither of them held the build once he answered. **What 2026-11-06 settled
is the licence** — the library's and, by his word, its usability — **and the alternative it displaced
is the one that still stands if that word is wrong**: D921 back to its alternatives. The review
settles the library's *behaviour* against this design, which is the paragraph below, and not its
licence.

The review also found that **the library's defaults are the opposite of this design's rules**, and
named them: PKCE is **optional** (`op/token_request.go` line 168) and an empty challenge method reads
as `plain` (`auth_request.go` lines 471-480); **an unknown scope is silently dropped**
(`auth_request.go` line 310) against D927; extra grants (jwt-bearer, token-exchange, device code,
implicit) are on by default where this design wants two; the library needs a `SigningKey`
(`op/storage.go` line 67) that the design gave **no home**; and its discovery document is
`openid-configuration`, not the RFC 8414 shape §2 names. **The version the review read is
3.51.17.** The amended answer is a **thin constraint layer ahead of the library**, stated in §3 and
§6 and built in M5:

- **Refuse a missing challenge and a challenge that is not `S256` before the library sees the
  request**, and **refuse an unknown scope before the library sees it** — a default that drops what
  it does not know cannot be allowed to decide what a token may hold.
- **Allow only the authorization code and refresh grants**; the others are not enabled, not
  filtered after the fact.
- **Hold the signing key in Secret Manager**, or issue opaque tokens; there is no third answer that
  does not leave key material somewhere this folder did not design.
- **Publish the discovery metadata the design names** rather than inheriting the library's.

**UNVERIFIED.** The library citations above are the **review's**, read from the module outside this
container: this container cannot fetch the module, and `go.mod` carries no OIDC library (F15), so
neither the line numbers nor the version's contents could be re-checked here. They are recorded as
the review gave them, and M5's first act is to re-read them against the version it pins.

### D922 — amended (finding H3)

**Amended 2026-11-04** (finding H3): the review found that **IAM bypasses the rules** — launchers and
operators hold `roles/firebasedatabase.viewer` (`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`),
admins and budget admins hold `roles/firebasedatabase.admin` (`:53-58`), and the history job holds it
too (`:81-84`) — so a node the rules close is still **readable by every launcher** and writable by
several accounts, and a public service holding `firebasedatabase.admin` would hold it over the
**whole** database (`:138-142`). The amended answer:

- **Token records store HMAC-keyed hashes with a pepper held in Secret Manager**, and **every
  record's identity, its limits and its revocation state are MAC'd**, so a reader who is not the
  coordinator learns nothing and cannot forge a record even where IAM lets them write one.
- **The public service holds no `firebasedatabase.admin` over anything**: the store's IAM names it,
  and of the project's database grants it holds only the **viewer** the mint pre-check needs and
  never the admin grant that would carry the whole database (M4, D935).
- **The records do not live in the main database at all** — **D935** says where and why (§4's answer
  of 2026-11-05: the bucket `fugaro-mcpauth-<gcp-project>`, whose only member is the front door's own
  account `mcp_server`).
- **The `mcpauth` node the rules name and deny stays** (M3, unchanged): with the records elsewhere it
  is a **tripwire**, not a store — an MCP record written into the main database is a failing test
  rather than a quiet widening, which is exactly what M3's red step asserts.

The original answer's **`.read: false` / `.write: false`** rule is kept and is now the *first* of five
independent barriers rather than one of two. §6 and §8 state all of this.

### D923 — amended (findings H1 and M-a)

**Amended 2026-11-04, and this is the one amendment that is NOT the recommendation.** Finding
**H1**: a device token is a **custom token** — uid `m~…`, claim `mu` = uid
(`internal/budget/token/device.go:176` `MintDevice`; `internal/cli/pair.go:353` writes the device
node) — and **every launcher and operator holds `signJwt` on the project's signer**
(`deploy/terraform/gcp/modules/firebase/iam.tf:35-41`) and can read `config/admin/devices` through
`roles/firebasedatabase.viewer` (`:44-50`). So **any launcher can mint a device token and sign in as
an operator's device** at the authorize endpoint's approve step (`POST /oauth/authorize`, M-a's
single-use POST). The repository's own rules comment says the same thing from the other side: the
`mu` claim "binds nothing against a minter"
(`internal/budget/rules/rules.go:482-487`). The review's recommendation was a **device-held signing
key registered at pairing**; **the owner accepted the residual risk and asked for it documented
instead** — not built, not in any task. It stands in §3 and §8 as an **ACCEPTED residual risk with
its consequences**:

- a launcher gains **operator scope** at the front door;
- **audit attribution can be forged**, so the audit record is evidence of what a device *did* rather
  than proof of who was behind it;
- a launcher can obtain a **rotating refresh family that outlives the ID token's hour**, so the
  exposure does not end when the sign-in token expires.

**The later change that would close it** is named here and in §3 and §11 rather than built: a
**device-held signing key, registered at pairing**, signing `{request id, client, redirect host,
code_challenge, expiry}`; the authorize step admits **only `m~` tokens with a live device node** and
refuses `r~`/`p~`; and a paired device carries a **viewer role that reaches `tasks:read` only**.

**Amended again 2026-11-04** (finding M-a): the device approval is weak for a second, independent
reason — the "loopback is the attacker's machine" argument in §8 **fails when the approval happens on
a phone**, where a person cannot see which machine is asking. The owner's answer, written into §3:

- approval requires **QR proximity or number matching**;
- the phone shows **the client, marked unverified, and the redirect host**;
- approval is **a single-use POST on a named endpoint**, and **an ID token never travels in a GET
  URL**;
- **nothing is pushed without a person starting it.**

**M5 and M6 build three of those four, and the first is built only as far as the page.** The approve
step is a single-use POST on a named endpoint, an ID token never travels in a GET URL, nothing is
pushed unstarted, and the page shows the client unverified beside the redirect host. But **the number
is rendered on the same page and posted back by the same form**: the paired device never sees it, so
a page rendering a flow of its own displays its own number and collects the number and the ID token
together from whoever fills that form — which is the approval this check is meant to tell apart from
the real one. **A check on the page is therefore not M-a's device-side approval**, and the half that
would be — the number shown on, or entered into, the paired device, so the approval is bound there —
**is not built and is in no task**: it is new behaviour and an owner's decision, and it is raised as
a finding rather than added by a task that did not ask for it. The token has the same kind of limit
beside it: nothing spends an ID token, so one that leaks approves flows until it expires.

### D924 — amended (finding M-b)

The family spend limit was **not enforceable across concurrent runs**: two runs started from one
token family would each read the same remaining figure and each be allowed it. The amended answer,
which M2 and M9 build:

- at spawn, **reserve `min(the job's cap, the family's remaining)` atomically against the family
  counter**, with a conditional write **on the same read** — the generation-matched replace
  `internal/blobx` gives the bucket the records live in (`internal/blobx/blobx.go:1`, M2);
- **settle at finalize**, so the reservation returns to the family whatever the run did not spend;
- **for a `client_credentials` token the family is the client**, which is the only family such a
  caller has (D929, amended).

M9 is **deferred** (D936), so the reservation lands with it; the counter's shape lands in M2.

### D929 — amended (finding M-f)

**Two things the answer left open, and the owner closed them the same day.** First, **an unattended
client is not a `user:` entry**, so D933 — which reads exact `user:` entries and nothing else —
**refuses every client-credentials caller**: the mechanism has a shape and no admission path.
Second, **no task owns pre-registration** — the client id and secret an unattended caller needs. The
amended answer:

- **`client_credentials` stays OFF until it has an owner**, and the switch is not turned on by
  anything in the first slice; the plan's tasks do not build the registration surface.
- **Pre-registration is refused inside a coding agent's session**, the same speed bump `fugaro user`
  makes at its own door (`internal/cli/user.go:56`) and for the same reason: an agent holds the
  operator's credentials and could write the file itself, so this is a speed bump rather than a
  boundary — and it is enough to keep an agent from quietly creating a long-lived caller.

M5 and M7 carry the refusal; **neither builds the grant**.

### D930 — amended (finding M-d)

Rotation is a **check-then-write race**: two requests presenting the same refresh token both read it
as live and both rotate, so a genuine double-submit looks like theft and a theft looks like a
double-submit. The amended answer, built by M2:

- **condition the rotation on the generation of the same read**, so exactly one rotation wins — the
  store D935 moved the records to is a bucket, and `internal/blobx` gives it the generation-matched
  replace an `ETag` condition gave a store in the database (`internal/blobx/blobx.go:1`, M2);
- keep a **small grace window** in which the loser of that race is answered with the same new pair
  rather than read as a replayed token, so a client on a slow link is not logged out by its own
  retry;
- a replayed token **outside** the window still revokes the family, exactly as the original answer
  said.

### D931 — amended (finding H2) — see D936

The original answer stands as the **shape `spawn_task` must take when it is built**, and the
finding is that it must not be built in the first slice. **D936** records the deferral and what a
later design item has to answer. **M9 is out of the first slice**, M6's
`TestTheCoordinatorMintsNoCustomToken` keeps its meaning, and no task in
[implementation-plan.md](implementation-plan.md) starts a run.

### D932 — amended (finding M-g)

`stream_logs` and `get_result` read the run's own recorded artefacts (`session/<id>.jsonl`,
`internal/runner/session.go:185`), and **those files can hold a credential the agent printed** — a
log line, an echoed environment, a command it ran. Serving them raw would make a tool that must never
return a credential into the one that does. The amended answer, built by M8: the front door **serves
the progress record redacted through `internal/safetext`** (`internal/safetext/safetext.go:17`), never
the raw transcript file, and the tool's own test is the one §5 already names.

### D933 — amended (finding M-f)

**Where the lists are read from was unspecified.** Today they live in **one machine's own local
config** (`terraform.launchers` / `terraform.operators`, `internal/cli/user.go:32-34`) plus the
installation's Terraform outputs, so a front door that read "the lists" would be reading one
operator's file. The amended answer: **the front door reads a source only an operator can write** —
the installation's own published state, reachable through the coordinator's account and not through
anything a caller or a run can reach, which [spec.md](spec.md) §4 states and M7 builds. The rest of
the answer is unchanged: exact `user:` entries only, and `group:`, `domain:` and `serviceAccount:`
refused.

### D934 — amended (finding M-e)

**"Off" has to mean the binding is absent.** A flag the handler reads leaves the service publicly
invokable and the whole handler in the perimeter. The amended answer, built by M4: **off is the
absence of the `allUsers` binding** on the service, and turning the front door on is what adds it.
That collides with the installer's own cover rule, and the collision is recorded rather than
worked around: **`internal/infra/tf/cover.go:913` refuses `allUsers`**, so a plan carrying the
binding is **not covered** and its stage asks its **own typed confirmation**
(`internal/infra/tf/cover.go:911`). **M4 must not widen `tf.Cover`** to make its own plan covered,
and its convergence test must **expect the typed confirmation** rather than a silent apply.

### The three low findings

They are recorded here and land in the tasks they name:

- **M1's `SpendUSD` could not tell unset from zero.** A `float64` limit of *no limit* and a limit of
  *zero* are the same value, and the difference is the whole of D924's "only narrows". **The fix is
  M9's**, and the review of 2026-11-05 found the same defect from the other end: `tighterSpend`
  (`ee/coordinator/mcpauth/limits.go:298`) treats `+Inf` as a value, so a token minted with
  `spend_usd: +Inf` under a project policy naming no cap resolves to an unbounded ceiling. M9 refuses
  a spend limit that is not a finite, non-negative number where a limit is read and where the run's
  ceiling is resolved; **the optional integer in micros this finding asked for is the alternative**,
  and whether the type itself moves is that fix's decision rather than a second one. An unknown
  rigor-mode string is **refused** rather than ignored.
- **A dynamically registered client has a name the client chose.** M5's consent page shows it
  **marked unverified**, beside the redirect host it already shows (D928), so a client that names
  itself after the product is not thereby trusted.
- **The bounded per-identity audit record can be flushed by spam.** M10 therefore treats the
  **Cloud Logging line as the durable copy** and the per-identity record as **the readable view**,
  so a caller filling the view cannot erase the evidence.

### The workspaces dependency

[spec.md](spec.md) §10 notes it, and **this folder does not edit `docs/workspaces`**. The parallel
workspaces work now requires a **member-bound assertion** and an **exchange at `/oauth/authorize`
that keeps the paired-device approval** — which means the MCP front door's authorize step and the
workspace exchange meet at the same endpoint and must not disagree about what approval is. Two
things follow, and both are stated in §10 rather than decided here: the dependency runs **one way**
(the MCP design adapts to the workspaces decision, not the reverse), and there is an **identity
mismatch** — a workspace identity is a **verified email**, while the MCP front door's identity is a
**device uid read back to an email** — which is the same mismatch H1 is about and is why the
workspaces amendment and this one have to be read together.

**Read, not assumed.** The workspaces amendment is a **parallel run's**, merged as **#1711** — and it
is in this branch's own tree, so the sentences above are checked against its text rather than
restating what the review reported: the assertion **carries the person's address, the provider that
signed them in and the workspace's project** (`docs/workspaces/spec.md:209-210`), and the exchange at
the member's `/oauth/authorize` **keeps the paired-device approval** of the member's own `fugaro
pair` (`docs/workspaces/spec.md:217-219`, its D923). Both agree with §3 and §10 of [spec.md](spec.md).

### What the running tasks do

- **M1 and M3 may continue**, and neither is cancelled. M1's spend-type finding is **M9's to close**
  (the paragraph above); M3 is unchanged, and its tripwire red step is what the amendment relies
  on.
- **M4 and M6 were cancelled** and **were to be rerun from this amended plan**: M4's draft predates
  the D934 and M-e answers (the cover rule and the meaning of "off"), and M6's predates M-a and H1.
- **What has happened since, as of the commit this file is read at.** **M1 (#1713), M2 (#1767), M3
  (#1715), M6 (#1714) and M4's first half (#1716) are merged on `main`** — M4's half being the
  account, the front door service and its public invoker binding
  (`deploy/terraform/gcp/modules/installation/coordinator.tf:320`, `:382`, `:527`) — **because the
  owner approved them one at a time, which is what "may continue" turned into.** **M5 is built** —
  the two metadata documents, the four endpoints and
  the approve step of M-a — and **the rest is not**: M4's store half (`mcpauth_store.tf`, the bucket
  and its one grant) and M7–M10; **M3's node is in with its parity test while the comment D935 asks
  for above it is not written**; and **what merged as M6 is the draft M-a predates** — no approve-step
  POST and no `TestAnIDTokenInAQueryStringIsRefused`, because M5, which defines the approve step, was
  built after M6 rather than with it. **And one row of an already-merged task
  is not in the tree either**: M2's entry, and §6's second barrier below, state the **keyed** hash and
  the **MAC over every record** as what the store does, while the store hashes with a plain unkeyed
  sha256 (`ee/coordinator/mcpauth/store.go:387`) and writes no MAC at all — so a reader who is not the
  store's member reads an identity in the clear, and a record written by anybody else is not refused
  by a mark. That barrier is M2's own row, it is on `main` already and not in this branch, and it is
  raised as a finding rather than closed by an edit here.

### The two bounds no task carries (raised 2026-11-07)

**spec.md §3 and §8 bound the open registration endpoint by two things that are built nowhere**, and
M5's endpoint and the record it writes say so in their own words: **a per-source rate limit** and
**registration records being expirable**, with the sweep behind the second one reaching all three of
the unauthenticated shapes the store holds — the registration, the pending authorize request and the
code. **M5 builds the endpoint and neither bound. M10 does not carry them either**: its entry is the
audit record and revocation, and §4's shared-files table names `store.go` as M10's at L6 for
revocation and nothing else. **So no task of the plan owns either bound.** An earlier draft of this
branch's M10 row had tried to place them there, with a limiter file and its tests added to that row's
scope; **that was an unauthorised re-scope of an owner-answered plan and is withdrawn**, because
which task carries a bound is scope and scope is the owner's. **This folder therefore raises it as a
finding rather than inventing a task**: what is owed is his decision — them inside M10's entry, or a
task of their own — and until it comes the growth §8 accepts is accepted unbounded, with a
registration that never stops being a client. The red step such a task would carry is
`TestRegistrationIsRateLimitedPerSource` and `TestARegisteredClientIsExpiredAndSwept`. **What keeps
the exposure latent rather than reachable is a guard M5 does carry, and it is a test rather than a
behaviour**: `TestNoBinaryLinksTheFrontDoorYet` fails if any file a binary builds names this package,
so the task that wires the front door is the task that carries the two bounds and deletes the guard,
and no release mounts an open registration endpoint because a wiring arrived unnoticed.

## 4b. The decision the amendment added

**D936 is D920–D934's own block continued**, taken from the numbers this folder reserved (D935–D939,
§5), and it is the one finding of the security review that is a decision rather than an amendment.
**The other decision-shaped finding, H3, is D935** — §4's answer of 2026-11-05, which the independent
review of PR #1715's M3 reached from the installation's own IAM and this folder's review from §8, and
whose store is the one every document here now names.

### D936 — What is the first MCP slice, and what happens to `spawn_task`?

| D | The question | The answer, 2026-11-04 | Why |
| --- | --- | --- | --- |
| **D936** | Is `spawn_task` in the first MCP slice? | **No — deferred out of it.** The first slice is **the authorization server plus the read tools**: `get_task_status`, `stream_logs`, `get_result` and `list_tasks` — and `cancel_task` goes with `spawn_task` into the later design item, because the marker it writes is an object in the runs bucket (`internal/runstore/runstore.go:1315`) and that is the same write the coordinator does not hold. **`spawn_task` is its own later design item**, and no task in [implementation-plan.md](implementation-plan.md) starts a run (H2, owner's answer of 2026-11-04 — **DEFER**, not the review's recommendation) | `spawn_task` needs **three privileges, not one**, and the review found the design had priced only the third: `signJwt` to mint the run's custom token; **write to the runs bucket**, which the coordinator does not hold (F3 is `roles/storage.objectViewer`); and **`run.jobs.runWithOverrides`**, which can override **container args** — the base image's entrypoint is `tini --` (`images/base_test.go:59`), so an override can run **any command beside the job's mounted git and model credentials**. That contradicts the answer's own "the coordinator mints nothing" (D923) and M6's `TestTheCoordinatorMintsNoCustomToken`. The first slice keeps that sentence true by not needing `spawn_task` at all. |

**What a later `spawn_task` design item has to answer**, written here so the deferral is a plan and
not a shrug:

- **No `runWithOverrides`.** Use `run.jobs.run` with **per-run input under a bucket prefix only the
  coordinator writes**, keyed by the execution, so the job's own template decides everything a
  caller could otherwise rewrite.
- **Run-token minting moves out of the public service**, so the service that answers strangers holds
  no signer.
- **An argument allowlist**: no `--no-budget-check` (`internal/cli/run.go:325`), no `--token`
  (`:313`), no free-form `--run-id`; and **always pass `mode = max(requested, the token's minimum)`**,
  so a caller cannot ask for less than its token allows.

**What this moves**: M9 and the parts of M10 that depend on it leave the first slice; the shared-files
table (§4 of the plan) and the release slot (§5) are re-cut; and **the plan's own statement of the
tool set is narrower** — the first slice serves the read tools and no run starts from the front door.

### The store: D935's answer, and what this folder's review adds to it

**The records' store is D935's**, and it is written in §4 above: the bucket
`fugaro-mcpauth-<gcp-project>`, with the records under `mcpauth/`, whose IAM names one account — the
front door's own `mcp_server` — and nobody else. **H3 is that finding seen from §8**: the rules are
not the perimeter, because `roles/firebasedatabase.viewer`
(`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`) is an IAM read every launcher and operator
holds and `roles/firebasedatabase.admin` (`:53-58`) is a write two further groups hold, so a record
the rules close is still readable by every launcher. **What this folder's review adds to D935, and
what D922's amendment above states**: **keyed hashes with a pepper held in Secret Manager**, a **MAC
over each record's identity, its limits and its revocation state**, and **no `firebasedatabase.admin`
on the public service** — so a reader who is not the store's one member learns nothing, and a record
somebody else wrote is detectable rather than trusted. **Where the records are at rest, and who
reaches them, is D935's answer**, and this section adds the marks rather than a second store.

## 5. The number block, and why it stays D920

Read at launch (2026-11-01) and **re-read on 2026-11-04 against `b41709ea`**, because the tree had
moved a long way in three days and a block that was free at launch is not free by default:

- **On `origin/main`** the highest *written* decision is now **D1189** — the block
  **D1160–D1189** is `docs/config-management/`'s, of which D1160–D1183 are written and D1184–D1189
  reserved (`docs/config-management/README.md:72`). Everything through **D1189** is therefore taken.
- **Every number from D1000 up is on `main` or in an open work stream**: this design's own launch
  reading (the highest on `main` was D900, in `docs/rigor-command/README.md`) is **superseded**, and
  the *facts* it recorded are wrong rather than its conclusion.
- **Above D1189, the free space is allocated**: **D1190–D1219** belong to #1676 (the repository-image
  removal design), **D1220–D1259** to the stateless-orchestrator work, **D1260–D1289** to the #1678
  design, and **D1290–D1309** are #1492's new block.

**This work keeps D920–D934**, the fifteen decisions above, **and D935** — the answer of 2026-11-05,
which this work's own reservation had left free and which its review asked for, and which says where
the token records live — **and D936**, the one decision the security review's own findings made
(§4b): the first MCP slice, with `spawn_task` deferred out of it. **D937–D939 stay reserved
and unused**; the amendment notes of §4a take **no** number, because they amend a decision rather
than replace it — a number here is never reused for a new meaning, so an amended answer keeps its own
number and gains a dated note beside it. They do not collide: on `main` the strings "D920", "D934"
and "D935" appear only as *other works' inventories of this pull request's block*
(`docs/managed-agent-converge/reconciliation.md`:248, `docs/run-limit-carry-over/README.md`:54),
never as a decision of their own — D935 was checked against `main` again before it was written rather
than assumed free, the only files that name it are this folder's own, and D936 is named nowhere else
at all. A later task that needs a number takes **D1310** — the first number above every allocation
listed here — and re-checks this section's facts rather than its conclusion.

`grep -rhoE 'D[0-9]{3,4}' docs` also returns `D1000`, which is a package-lock integrity fragment and
not a decision; it is recorded here because a reader running the same grep will meet it.

## 6. Owner checks, before any implementation

These are not decisions — no answer changes the design. They are the things this design **cannot**
verify hermetically, and they belong on the live checklist, in the owner's hands, before the first
code task starts:

0. **The launch-override fence is decided and built before M9 or D931.** Spec §8 states the owner's requirement (the runner ignores the override environment except an allowlist) as NOT BUILT: the runner reads its ceiling and wiring from variables `runWithOverrides` can rewrite today, so the mechanism that lets it read non-allowlisted configuration from something an override cannot set is an open owner decision.
1. **`zitadel/oidc`'s licence is confirmed — by the owner or by counsel — before any build**, and
   **the M5 gate file is the owner's to create.** D921 was answered with the licence
   **UNCONFIRMED**: the library's Apache 2.0 claim is the library's own and nothing in this
   container can check it, and a dependency a self-hosted product cannot ship is a dependency the
   whole design rests on by mistake. **The independent security review of 2026-11-04 did not settle
   it either** — what it read is the library's behaviour against this design and not its licence —
   so **the owner or counsel confirms the licence, the transitive dependencies included**. **The
   owner answered this check himself on 2026-11-06: `zitadel/oidc` is Apache 2.0 and usable** — an
   instruction he gave for M5's build, which is the answer M5's `go.mod` entry is taken against.
   **His answer is what closes the check, and it closes it by his word rather than by the file the
   sentence above presumed**: the sentence was written before he answered, no gate file has been
   written into the tree, and counsel has not been asked about the transitive set — and neither of
   those is what M5 was held behind, because the check put the question to the owner and he answered
   it. **What a wrong answer still costs is the alternative this check names**: a licence that turns
   out to be unusable sends D921 back to its own alternatives, with the library taken out of
   `go.mod` and M5 rebuilt against them. **The two things his answer did not come from stay on this
   list as facts rather than as gates** — **counsel's clearance of the transitive dependencies** the
   library brings, and the gate file — so a later reader meets them still named and knows what they
   are.
2. **The endpoint really is reachable from the public internet** once D920's answer says so: a
   request from outside the project reaches the handler and gets the 401, rather than being refused
   by the platform. spec.md §8 says what follows if it is not.
3. **The current MCP authorization specification's text is re-read** against §3 and §6 — the owner's
   own open item. The design is written to the **2026-07-28** revision, the one the owner named, and
   **this container cannot re-verify it**: the public specification is not reachable from here, which
   is the same limitation `docs/coordinator/reconciliation.md:794` recorded as **U1** when the
   coordinator read the same specification. So the re-reading stays an owner check rather than
   something this round could close, and the design's shape is small enough that a revision is an
   edit, not a rewrite.
4. **The project's Identity Platform configuration** is what F6 says of an installation created by
   the current CLI, and what it says of one created earlier; D923's answer is the same in both
   cases, but the authorize step's first message differs.
5. **The Firebase ID token's issuer and audience** for the project at hand match F5's two
   comparisons, from a token minted by the installation's own signer.
6. **One client-credentials caller, end to end**, against a real installation: D929's shape is the
   one this design is least able to exercise hermetically, because a client secret and a token
   rotation are both things a fake proves nothing about. **M-f's amendment decides when**: an
   unattended client is not a `user:` entry, so D933 refuses it, **`client_credentials` stays off
   until it has its own owner**, and pre-registration is refused inside a coding agent's session —
   so this check is about a mechanism the first slice does **not** turn on, and it is here so the
   day somebody turns it on they know what could not be tested hermetically.
7. **The independent SECURITY review of spec.md §8** — the owner's brief requires it, and it is
   **run by the coordinator**, not by this folder's authors: the threat model is the section the
   review reads, and **no task in [implementation-plan.md](implementation-plan.md) starts before it
   has been read and its findings answered.** [README.md](README.md) records who runs it and what it
   covers. **It ran on 2026-11-04**, after the design merged, and **its findings and the owner's
   answers are §4a**, which is the section every document in this folder is now written to: **its
   first finding had already landed** (F32, the review of PR #1715's M3), and the owner answered it on
   2026-11-05 (**D935**). Because it ran after the merge rather than before, its answers are
   amendments to decisions rather than answers, and the one decision they made of their own is
   **D936**. What remains an owner check is the **re-verification**: the review read `zitadel/oidc`
   3.51.17 outside this container, so its library citations are marked **UNVERIFIED** in §4a and M5
   re-reads them against the version it pins. The review is M4's only remaining gate — **the third
   gate that stood in front of M2 and M4, the store question itself, was answered the same day rather
   than left standing** (§8 of the plan holds the answer: the bucket).
8. **The store's IAM is what D935 says it is, and only a live project can show it.** No hermetic test
   stands in for this one: the plan's own test reads the plan, and the plan is not the project's
   policy. With the owner's own credentials, once M4 has been applied by `fugaro update`: **read and
   list the store's prefix as a launcher and as a budget admin, and see a refusal** — the two classes
   F32 found the rules cannot exclude from the database; **impersonate the deployer as an operator
   (`gcloud storage ls` under the deployer's account, or with the token-creator grant the installation
   gives every operator, `deploy/terraform/gcp/modules/installation/deployer.tf:501`), and see a
   refusal too** — the reading the correction of 2026-11-05 adds, and the one that proves the store's
   name is outside the deployer's conditioned prefix rather than merely written to be; **read one
   record as the front door's own account `mcp_server` — the store's single member, the account M4
   creates (`deploy/terraform/gcp/modules/installation/coordinator.tf:320`), never the sweep's — and
   see it work**; **read the same record as a basic project Viewer and see a refusal**, which is the
   second correction of 2026-11-05: a new bucket carries GCS's convenience bindings for
   `projectViewer:` until M4 removes them, so the check reads the created policy rather than assuming
   it is clean (`SECURITY.md:88`, `docs/design/bucket-iam.md:47`,
   `internal/infra/statebucket.go:140`); and **confirm the store's
   bucket is not the runs bucket**, which is the one placement a prefix of an existing bucket would
   have made readable by both lists. It is
   check 2's shape for a second resource: the claim is about who reaches something, and only the
   project can answer it. **Which store** it is checked against is no longer open: the plan's §8
   records the owner's answer of 2026-11-05 — the bucket `fugaro-mcpauth-<gcp-project>` — so this
   check is written to it, and M4 creates the bucket it names.

## 7. What `main` had overtaken, and what was fixed in place

The findings above were written against `3d6ed04d` on 2026-11-01. Re-read on **2026-11-04** at
`b41709ea`, eight things had moved, and each is recorded here rather than left for a reader to
discover:

1. **Managed mode landed.** The gate is `internal/cli/managed_gate.go` (its one helper, "the four
   lines every command in the set makes"), and the set covers `fugaro update`, `config publish`,
   `project policy publish`, `image refresh` and the **settings, rigor and accounts `set` commands**
   (and `unset`/`rm`) — `docs/managed-agent-converge/README.md:20`. M4's `#### For operators` block
   already routes the front door's switch through `fugaro update`, and the gate is what makes that
   route automatic rather than a second command; nothing in this design changes because of it, and
   §7 of the plan now says so.
2. **Person-class reads landed.** The credentials service already admits the installation's
   **operators** by verified Google ID token email, and it does so on **both** forges: the list is
   `internal/credsvc/auth.go:375` (`PersonAccounts`, described at `:350` as "the installation's
   operators"), Bitbucket's read mint verifies through `:890` (`VerifyReadCaller`, called at
   `internal/credsvc/bitbucket.go:337`), and GitHub's read route verifies through `:968`
   (`VerifyPersonCaller`, called from `internal/credsvc/githubread.go:231`). The class itself is
   **BB4** in `docs/bitbucket-credentials/implementation-plan.md:131` (G4). **This is a precedent for
   D933 and not a mechanism this design reuses:** a *person* is already admitted to a project-scoped
   service by a verified email, so D933's answer — the project's own two lists — is the shape the
   repository already has rather than one invented here.
3. **Three commands are now on `main`**: `fugaro settings` (`internal/cli/settings.go:61`),
   `fugaro rigor` (`internal/cli/settings_set.go:45` names `fugaro rigor set` as the owner of the
   rigor keys) and `fugaro accounts` (`internal/cli/accounts.go:25`). F21's gap is unchanged — none
   of them writes an MCP token — but the CLI surface a token-minting command would join exists, and
   F21's own row is the finding that says so.
4. **The project image catalog is on `main`** (`docs/project-image-catalog/`), and the
   repository-image **removal** is now a design of its own, **#1676**, holding D1190–D1219. F15's
   "no MCP SDK and no OIDC library" is unaffected; what changes is only what a future task would
   find beside this one.
5. **`fugaro upgrade` still exists** (`internal/cli/upgrade.go`), and no finding here assumed
   otherwise: M4's operator path is `fugaro update` for the reason CLAUDE.md's HARD RULE gives, and
   `upgrade` is not part of it.
6. **The CLI stream has moved twice since the findings were written.** It was at
   `current: "0.14.1"`, `next: Koala` against `3d6ed04d` on 2026-11-01 and at `current: "0.17.0"`,
   `next: Ocelot` at the re-read of 2026-11-04 — and it stands at `current: "0.20.3"`, `next: Rhino`
   now, with 0.18.0 "Ocelot", 0.19.0 "Panda" and the 0.20.x "Quokka" patches shipped in between.
   This is what §5 of the plan is re-slotted against, and the target there is **0.21.0 "Rhino" or
   later** — see [implementation-plan.md](implementation-plan.md) §5.
7. **`internal/runner/stray.go` (#1476) is on `main`.** Finalize now refuses to commit a binary the
   run left untracked (`internal/runner/stray.go:29`, `strayArtifacts`). Nothing in this design
   reads the run's leftovers, so no finding moves; it is recorded because a reader of F14's "a run's
   transcripts land in the runs bucket" should know what else finalize now decides.
8. **The paired device is on `main`, and it is what D923's answer leans on.** `fugaro pair`
   (`internal/cli/pair.go`) walks an operator through one pairing: a one-shot token, the QR's
   payload, the phone's key, the typed eight-character code, and the device node it writes —
   `internal/budget/token/device.go:176` (`MintDevice`) mints the device's custom token, and
   the pairing session's own claim is `PAIRING` (`internal/budget/rules/rules.go:540`, read at
   `:561`) over the device allowlist `PathDevices` (`:458`) that the `USER` claim already requires
   (`:517`). **This is why D923's answer can name a signer that is not the coordinator:** the
   repository already has a flow where a person approves something from a device that is paired to
   the installation, and the principal that mints a custom token is deliberately **not** a resident
   service — `internal/budget/rules/rules.go:482-487` records that whoever can sign for the project chooses the uid and
   every claim in it, and that such a principal is a launcher or a service-account key holder. The
   coordinator holds neither.
