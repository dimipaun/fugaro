# The hosted MCP server: reconciliation and decisions

**Status: design answered by the owner 2026-11-04; no task approved; M5's implementation has landed
ahead of the §8 security review, which is still to be read; every other task is plan only.**

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
is now written to those answers. **Every answer took the recommendation**, so nothing here is marked
**not the recommendation** — and §4's answers table records the one place where that was briefly not
so (D922 was first answered the other way and restated the same day).

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

| D | The question | The recommendation | The alternative, and its cost | Blocks |
| --- | --- | --- | --- | --- |
| **D920** | Is the MCP endpoint public, with the bearer token as the only gate, or does it keep P8's Cloud Run IAM invoker? | **Public.** `allUsers` may invoke, and the handler is the gate. Every unauthenticated path is a metadata document or an authorization endpoint with no work behind it; every refusal is audited. It is the only shape a standard MCP client can use. | **Keep IAM invoker** (P8's recorded departure): every caller — including a chat client — needs a Google identity with the project's invoker role and a way to mint an ID token for it. No standard MCP client can, so the feature is reachable only by software already written against Fugaro; the OAuth flow the owner's spec asks for becomes dead code. Cost of the recommendation: the endpoint is on the public internet and the handler is the whole perimeter (spec.md §8). | Everything. |
| **D921** | Which OAuth library? | **zitadel/oidc** for the protocol machinery, with storage and Dynamic Client Registration supplied by the coordinator (the owner's spec already expects DCR to be hand-written). **Apache 2.0 and OpenID-certified are the library's own claims and this container cannot check either** — the owner answered with the licence **UNCONFIRMED**, which is why confirming it was the first owner check (§6) and a gate on M5, **and the owner confirmed it on 2026-11-06: Apache 2.0, and the dependency may be taken.** No new runtime. | **ory/fosite**: the same missing DCR, plus a heavier plugin surface for a flow this small. **A standalone third-party server**: a second resident service in a self-hosted product whose point is one service per project, on a licence the owner has not confirmed — the owner's own open item, which the answer does not close for the library it did pick either. Cost of the recommendation: a new dependency in `go.mod`, a library whose storage interfaces must be fitted to a database it has never seen, and **a licence check that has to pass before any code is written**. | Every endpoint in D921's own scope; the implementation plan's first code task — **and M5 waits on the licence**. |
| **D922** | Where do token records, client registrations and the audit trail live? | **A separate top-level subtree with reads and writes refused to everything but the coordinator's service account.** Only hashes are stored, and a database read yields no presentable value. | **Reuse the `coordinator/` subtree.** It is `.read: USER` today (F7), so every person the installation admits may read the client list, the redirect URIs, the revocation state and the whole tool-call audit. The "store only hashes" rule protects the token values and nothing else: the client list, the redirect URIs, the revocation state and the whole tool-call audit have no protection at all. Cost of the recommendation: one more subtree in the rules, one more account able to write it, and a census test to keep both honest. | D920's audit requirement, D929's client secrets, F22. |
| **D923** | How does the person sign in at the authorize step? | **The custom-token flow the installation already has.** The authorize step hands the person to Fugaro's existing signer, and the coordinator verifies the resulting Firebase ID token exactly as F5 already does. No provider page. | **Enable a sign-in provider** so Firebase can host the page the owner's spec describes: it lifts the install's refusal of any public sign-up path (F6) for the whole project, which is the change `docs/mobile-auth` D221 exists to prevent and which that work's reconciliation classifies **Wrong**. Cost of the recommendation: the authorize step is a redirect to a Fugaro-owned page rather than to a provider's, so the flow is one hop longer than a client's most familiar shape, and a project that has *already* enabled a provider keeps it (the design neither enables nor disables one). | The whole auth flow. |
| **D924** | How is a token's spend limit enforced? | **The coordinator passes the token's limit in its own variable on the execution override, and the run's ceiling becomes the tighter of that and the job's own.** The override mechanism already exists (F19) and the gateway already enforces whatever ceiling the run resolves (F10), so the limit is enforced where every other cap is. A variable of its own rather than a lower value written over the job's is what makes "only narrows" **structural**: the run takes the tighter side itself, so a wrong number at the coordinator cannot widen a ceiling. | **Overwrite the job's own cap variable with `min(...)`.** It works, and it puts the whole "only narrows" property in the launcher's arithmetic rather than in the run — one launcher bug away from a widened ceiling. **Through the agent's `--max-budget-usd`** (F27): the limit would be enforced by the agent's own estimate, which the repository's own notes record as wrong by roughly an order of magnitude for non-Anthropic models. **Or: not enforced, merely recorded** — which is no limit at all. Cost of the recommendation: a small core change (one variable in the run's ceiling, `internal/runner/spend.go`) that is not this work's to make unilaterally, plus the launch grant of F18. | D931; the spend half of spec.md §6. |
| **D925** | Is Hotfix part of the rigor window or a flag beside it? | **A separate flag, off by default**, gating Hotfix by itself; the window's `min`/`max` read only the four on-axis modes. | **Fold Hotfix into the axis.** F9's `Rank()` is 0 for Hotfix on purpose, so a `min: pro` token would admit a Hotfix request — the opposite of "off by default". Ranking it high instead reverses the fail-open direction `internal/rigor` chose deliberately. Cost of the recommendation: two things to check instead of one, and a client that asks for Hotfix without the flag is refused rather than downgraded. | The rigor half of spec.md §6. |
| **D926** | What is the window's shape, and what if it admits nothing? | **An optional `min` and an optional `max` over the closed mode vocabulary; unset means the project's policy alone.** A window with no member — `max` below `min` — is refused **at mint**, so a token that could never start a run cannot be issued. | **Allow it and refuse every call.** The operator mints a token, hands it to an agent, and every call refuses for a reason neither of them can see; the failure surfaces in the agent's retry loop rather than at the person's keyboard. Cost of the recommendation: a mint-time validation that has to be tested, and a refusal an operator may find pedantic. | D925. |
| **D927** | Which scopes exist in v1? | **`tasks:read` and `tasks:write`, exactly as the owner lists them.** An unknown scope is refused, never ignored. `roadmap:write` is **not** in v1. | **Add `roadmap:write` now** so the console's blocked write (F30) can use the front door. A scope is a wire contract on a self-hosted product: it exists the moment a token can hold it, it cannot be withdrawn without breaking tokens, and the console's own roadmap write design — which has not been written — is what should decide the name and the permission. Cost of the recommendation: the console's GV3.1/GV3.2 stay blocked, and that work comes back to ask for a scope. | The console's graph write. |
| **D928** | Who owns the redirect-URI allowlist? | **The project's own configuration**, with loopback always allowed for native clients; the coordinator holds no vendor URL as a constant. Validated at registration **and** at authorize, exact match, no wildcards, no prefix matching, no custom schemes. | **Hard-code the known client callbacks** the owner's spec names as examples. A vendor's callback URL baked into a self-hosted product goes stale in somebody else's release cycle, and it refuses every MCP client the owner has not heard of. Cost of the recommendation: an operator who wants a hosted client allowlisted writes one configuration line, and there is no zero-config path for a hosted client. | DCR (D928's own scope) and the code path of D923. |
| **D929** | What is an unattended caller? | **A pre-registered confidential client** using the client credentials grant, with limits that are **mandatory**: no rigor window and no spend limit means no token. | **The owner's option B** (exchange a project API key for a token). It contradicts the owner's own non-goal that the keys are not replaced: every place a key is stored grows the whole token lifecycle, and revoking "the key" stops being one act. Cost of the recommendation: a script that already holds a key needs a client id and a secret instead, which is a change to that script, and the owner's spec lists the keys as a deliberate fallback. | The unattended half of spec.md §7. |
| **D930** | Lifetimes, rotation, revocation. | **Short-lived access tokens; refresh tokens that rotate on every use with reuse detection, a replayed one revoking the family; revocation as one delete**, reachable from the API and from a person's own surface. | **Long-lived access tokens with no rotation** — what most simple servers do: a stolen token lives its whole life and nothing distinguishes theft from use. Cost of the recommendation: a client that loses a rotated refresh token must sign in again, and the family revocation on a false positive logs a legitimately-working client out. | Every call. |
| **D931** | How does `submit_task` start a run, and what does the coordinator see besides the six tools? | **Through the same seam `fugaro run` uses** (`internal/backend`), with the token's ceiling of D924 attached to that execution, and the coordinator's service account gaining the launch role on the project's own job — a **new grant**, flagged for independent review. The coordinator's own tool set (F31) is **served on the same endpoint under its existing names**, so one `tools/list` shows both, and its tools keep their meanings. | **Write a pending-task node and let the coordinator's scheduled sweep launch it** (the pattern P5 already uses): a launch delayed by the sweep interval, a second writer racing the first, and a run that can silently never start while the caller sees success. Cost of the recommendation: a new IAM grant on a public-facing service, which is the one new privilege this design asks for. | `submit_task`, `cancel_task`, and the coordinator's own tool set. |
| **D932** | Where does `stream_logs` read from? | **The run's own recorded artefacts in the runs bucket** (F14), which the coordinator may already read (F3): the answer is the same material the console and `get_result` draw on, it needs no new grant, and it cannot read a log line outside the run. | **Stream Cloud Logging live** (F13): one more grant, this one able to read **every log line in the project**, mitigated only by a filter — the widest privilege in the design, for a tail that `fugaro logs` already serves a person. Cost of the recommendation: the tail lags the run, and a caller that wants live output waits for the next recorded stage rather than watching characters arrive. | `stream_logs`; the coordinator's grant list. |
| **D933** | Who is a person, once reaching the endpoint no longer means holding a Google identity? | **The project's own two lists** (F8): a launcher may call the task tools, an operator may also read the audit record. **F29's sentence in the coordinator's spec is replaced by this**, and the audit record of F22 becomes the operator's. | **Any signed-in identity of the project.** It is what F29 says today, and it was defensible only while the platform did the filtering; with a public endpoint it means every Firebase user of the project is a launcher, and the two lists `fugaro user` maintains would govern nothing. **Or: a new permission model** — a role per scope, a table, an admin surface: a second model beside the lists the project already has, for one front door. Cost of the recommendation: an identity that is signed in but on neither list gets a clear refusal, and a project that admits nobody with `fugaro user` has a coordinator nobody can use. | D920's premise; the whole authorization story. |
| **D934** | Is the front door **on** for every installation that has a coordinator, or does the owner turn it on? | **Off until the owner turns it on, on the coordinator's own opt-in.** An installation that never opted into the coordinator has no service for anything to reach; one that did gets the endpoint **off** until a person turns it on, and a request to a front door that is off is refused before the handler reads a token. | **On wherever the coordinator is.** An installation that opted in for a *read-only aggregator* would acquire a **public write endpoint** — `submit_task` spends money — as a side effect of an upgrade, which is exactly the surprise the repository's rule about `fugaro update` exists to prevent. Cost of the recommendation: one more switch, one more state on the live checklist, and a client that reaches an installation which has not turned it on is refused rather than redirected. | M4, and the operator story. |

### The owner's answers, 2026-11-04

Every decision above was answered on **2026-11-04**. The table is the record in the owner's own
terms: the answer, whether it is the recommendation, and the one-line reason the design carries for
it. **The design in [spec.md](spec.md) and the tasks in
[implementation-plan.md](implementation-plan.md) are written to these answers**, and **no row is
marked "not the recommendation"** — every answer took it, so the design is the recommended shape
throughout. D922 is the one row whose answer was **restated**: it is marked as such there and the
note under the table says what moved.

| D | The answer, 2026-11-04 | Why, in one line |
| --- | --- | --- |
| **D920** | **The recommendation** — the MCP endpoint is **public**, the bearer token is the gate, and the six unauthenticated paths are the metadata documents and the authorization endpoints | A standard MCP client on a laptop or a phone cannot mint a Google ID token, so P8's IAM invoker makes the feature reachable only by software already written against Fugaro — and spec.md §8 is the price the owner accepts for that. |
| **D921** | **The recommendation** — **zitadel/oidc** for the protocol machinery, with storage and Dynamic Client Registration supplied by the coordinator — **and the licence is settled: Apache 2.0, confirmed by the owner on 2026-11-06.** | Apache 2.0 is the library's own claim and nothing in this container can check it, and a dependency a self-hosted product cannot ship would sink the whole design — so the licence was a gate of its own, the first owner check in §6, and **the owner's confirmation of 2026-11-06 is what clears it**. |
| **D922** | **The recommendation**, in the owner's words of 2026-11-04: the **separate top-level subtree** — **not under `coordinator/`** — is the **`mcpauth`** node, **not readable by any client** (`.read: false`) and with **`.write: false`**; **only the coordinator's account writes it**, because that account holds `roles/firebasedatabase.admin` and the rules do not bind it | The separate subtree is what keeps the coordinator's shared view and the token records off one node, and the closed read is what keeps F26's finding closed: the client list, the redirect URIs, the revocation state and the whole tool-call audit are read by the coordinator's account and by nobody the rules admit. **This answer was restated the same day** — see the note under the table — and the restatement is the one every document is written to. spec.md §6 and §8 state the closed rule, and M3's red step is the "named and denied to every client" one. |
| **D923** | **The recommendation**, with the mechanism named: **a PAIRED DEVICE approves the sign-in**, and **the coordinator never mints a custom token**; **v1 admits only exact `user:` launcher entries, and refuses `group:`, `domain:` and `serviceAccount:` entries**; **the Firebase ID token is consumed only at `/oauth/authorize` and is never a grant at `/oauth/token`** | The project's Identity Platform is initialized with no sign-in providers and refuses every public sign-up path (F6), so there is no hosted page to redirect to and enabling one is the exact reversal `docs/mobile-auth` D221 refused — and the pairing the repository already has (`fugaro pair`, D165) is the signer that does not make the coordinator one. A `user:` entry is one person (F8's lists), where `domain:`, `group:` and `serviceAccount:` are refused **at `fugaro user` already** (`internal/cli/user.go:266`). Consuming the ID token at authorize is what keeps it out of the token endpoint's grant list. |
| **D924** | **The recommendation** — the token's limit travels in **a variable of its own** on the execution override, and the run's ceiling is the tighter of it and the job's own — and **spend is counted across the whole token family**, so a rotated refresh token is the same budget and not a new one | The override mechanism already exists (F19) and the gateway is already the place the real stop lives (F10, F27); a variable of its own is what makes "only narrows" structural rather than a launcher's arithmetic, and a per-family counter is what stops rotation from being a way to ask for the limit twice. |
| **D925** | **The recommendation** — Hotfix is a **separate flag, off by default**, beside the window | `Rank()` gives Hotfix **0** deliberately (F9, F28), so folding it into a `min`/`max` window admits Hotfix at every minimum — the opposite of "off by default". |
| **D926** | **The recommendation** — an optional `min` and an optional `max`, unset meaning the project's policy alone; a window that admits nothing is refused **at mint** | A token that could never start a run should not be mintable: the failure has to surface at the person's keyboard rather than in an agent's retry loop. |
| **D927** | **The recommendation** — `tasks:read` and `tasks:write` only; `roadmap:write` is **not** in v1 | A scope is a wire contract: it exists the moment a token can hold it, and the console's own roadmap-write design — which does not exist yet — is what should decide its name and its permission (F30). |
| **D928** | **The recommendation**, tightened by the owner: the redirect-URI allowlist is the **project's own configuration**, with loopback always allowed and validated at registration and at authorize, exact match — and **registration refuses a non-loopback URI that is not on the allowlist**, **the consent page shows the redirect host**, and **the flow is CSRF-bound** | A vendor's callback URL baked into a self-hosted product goes stale in somebody else's release cycle and refuses every client the owner has not heard of. The three tightenings are the owner's: refusing at registration is the earlier door, a person consents to a host they were shown rather than to a client id, and a flow with no CSRF binding lets an attacker's client finish a sign-in the person started for another. |
| **D929** | **The recommendation** — a pre-registered confidential client using the client credentials grant, with **mandatory** limits | The owner's option B would make every stored API key an OAuth client by another name, and revocation would stop being one act — against the owner's own non-goal that the keys are not replaced. |
| **D930** | **The recommendation** — **short-lived access tokens and refresh tokens that rotate on every use**, with reuse detection, a replayed token revoking the family, and revocation as one delete | Rotation is what makes a theft detectable rather than merely survivable: a long-lived token that is stolen lives its whole life and nothing distinguishes theft from use. |
| **D931** | **The recommendation**, with the owner's three tightenings: the tool is **`spawn_task`** (the owner's name for the `submit_task` of §0), it starts the run through the same seam `fugaro run` uses (`internal/backend`), the coordinator gains one **new launch grant**, and both tool sets are served on one endpoint under their existing names — and **there is a single launch path**: concurrency, rate, idempotency and the kill switch all go through the **same pre-launch code as `fugaro run`** (`budget.Precheck`, `internal/budget/precheck.go:58`, and the launch claim, `internal/runstore/launch.go:112`) | The sweep alternative delays a launch by the sweep interval, races a second writer, and can leave a caller seeing success for a run that never starts. A second pre-launch path beside `fugaro run`'s is the one that drifts: a front door that skipped the kill switch or the claim would spend money a person's launch could not, and would let one submit become two runs. |
| **D932** | **The recommendation** — `stream_logs` reads the run's own recorded artefacts in the runs bucket | The coordinator may already read them (F3, F14), the answer is the same material the console and `get_result` draw on, and it cannot read a log line outside the run — where streaming Cloud Logging live needs a grant that reaches every log line in the project. |
| **D933** | **The recommendation** — **only the project's launcher and operator lists may call the front door**: a launcher may call the task tools, an operator may also read the audit record — and the entries those lists admit are D923's answer, **exact `user:` entries only** | With a public endpoint, "any signed-in identity of the project" (F29's sentence) would make every Firebase user of the project a launcher, and the two lists `fugaro user` maintains would govern nothing. Admitting only `user:` entries is the same rule `fugaro user` already applies at its own door, so the front door cannot be the way a `domain:` entry gets in. |
| **D934** | **The recommendation** — the front door is **OFF** on every installation until a person turns it on, on the coordinator's own opt-in | An installation that opted into the coordinator for its read-only aggregator must not acquire a **public write endpoint** — `spawn_task` spends money — as a side effect of an upgrade. |

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

**D921's unconfirmed licence** added the first owner check in §6 and a build gate in the plan. The
**status of the whole folder** moved from "design only, nothing approved" to the line every file now
carries — **design answered by the owner 2026-11-04; no task approved; M5's implementation has landed
ahead of the §8 security review, which is still to be read; every other task is plan only.** That
line's last move is this work's: M5's implementation landed on 2026-11-07 while the review was still
unread, and a folder whose status line still said "nothing implemented" would have been describing a
repository that no longer exists.

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
which this work's own reservation had left free and which its review asked for — and **reserves
D936–D939**, which nothing here uses. They do not collide: on `main` the strings "D920", "D934" and
"D935" appear only as *other works' inventories of this pull request's block*
(`docs/managed-agent-converge/reconciliation.md:248`, `docs/run-limit-carry-over/README.md:54`), never
as a decision of their own — D935 was checked against `main` again before it was written rather than
assumed free, and the only files that name it are this folder's own. A later task that needs
a number takes **D1310** — the first number above every allocation listed here — and re-checks this
section's facts rather than its conclusion.

`grep -rhoE 'D[0-9]{3,4}' docs` also returns `D1000`, which is a package-lock integrity fragment and
not a decision; it is recorded here because a reader running the same grep will meet it.

## 6. Owner checks, before any implementation

These are not decisions — no answer changes the design. They are the things this design **cannot**
verify hermetically, and they belong on the live checklist, in the owner's hands, before the first
code task starts:

0. **The launch-override fence is decided and built before M9 or D931.** Spec §8 states the owner's requirement (the runner ignores the override environment except an allowlist) as NOT BUILT: the runner reads its ceiling and wiring from variables `runWithOverrides` can rewrite today, so the mechanism that lets it read non-allowlisted configuration from something an override cannot set is an open owner decision.
1. **`zitadel/oidc`'s licence is confirmed — by the owner or by counsel — before any build. ANSWERED
   by the owner on 2026-11-06: Apache 2.0, and the dependency may be taken.** D921
   was answered with the licence **UNCONFIRMED**: the library's Apache 2.0 claim is the library's own
   and nothing in this container can check it, and a dependency a self-hosted product cannot ship is
   a dependency the whole design rests on by mistake. **This was a gate, not a note** — M5 did not
   start until it was settled — **and it is settled**: the owner's confirmation of 2026-11-06 is what
   clears it, and D921's alternatives are no longer live.
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
   rotation are both things a fake proves nothing about.
7. **The independent SECURITY review of spec.md §8** — the owner's brief requires it, and it is
   **run by the coordinator**, not by this folder's authors: the threat model is the section the
   review reads, and **no task in [implementation-plan.md](implementation-plan.md) starts before it
   has been read and its findings answered.** [README.md](README.md) records who runs it and what it
   covers. **Its first finding has already landed** (F32, the review of PR #1715's M3), and the owner
   answered it on 2026-11-05 (**D935**); the rest of §8 is still to be read, and that review is M4's
   only remaining gate — **the third gate that stood in front of M2 and M4, the store question itself,
   was answered the same day rather than left standing** (§8 of the plan holds the answer: the bucket).
   **M5's implementation is the one task that did not wait for it** (2026-11-07): its five files are
   in the tree and the review is still to be read, which this folder records as a fact rather than
   endorses as a state, and which the status line above carries so that neither file reads as though
   no code existed.
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
6. **0.17.0 "Narwhal" has shipped** (`docs/release-streams.yaml`'s cli stream: `current: "0.17.0"`,
   `next: Ocelot`), which is what §5 of the plan is re-slotted against — see
   [implementation-plan.md](implementation-plan.md) §5.
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
