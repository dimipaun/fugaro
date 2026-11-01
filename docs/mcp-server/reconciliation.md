# The hosted MCP server: reconciliation and decisions

What the owner's spec ([spec.md](spec.md) §0) says, checked one claim at a time against the code as
it is at this work's base, and then the decisions the planning found.

**Base.** `origin/main` at `3d6ed04d`, read on 2026-11-01, with the repository's own clone; every
citation below is a path and a line at that commit. The open pull requests that touch the same
ground were read too, in particular #1472 (`docs/cloud-senior-review`, D880–D899), #1481
(`docs/run-without-checkout`, D901–D919) and #1483 (`internal/rigor/rounds.go`).

**How to read it.** Each finding is **Exists**, **Missing** or **Conflicts**, and each carries what
follows from it — a finding with no consequence is not in this document. §4 is the decisions; the
design in [spec.md](spec.md) §1 onward is written to every **recommendation**, and an owner who
answers the other way is answering a different document, not making an edit.

## 1. What exists

| # | The claim, and where it already holds |
| --- | --- |
| F1 | **The front door's home is decided**: the coordinator has **its own resident service** — D196 in [../coordinator/reconciliation.md](../coordinator/reconciliation.md) (the row headed D196 says the owner ruled against the recommendation), and [../coordinator/README.md](../coordinator/README.md):26 says P7 and P8 "do not start yet: they wait on the front-door design review". **Exists**, as a decision; **not built** (F24). |
| F2 | **A Cloud Run *service* is already admissible in the installer's Terraform cover**: `internal/infra/tf/cover.go:33-34` admits `google_cloud_run_v2_service` and `google_cloud_run_v2_service_iam_member`, added for the credentials service. **Exists** — the new resource type needs no cover change. |
| F3 | **The coordinator's accounts and grants exist, and they are a job's**: `deploy/terraform/gcp/modules/installation/coordinator.tf:39` (its service account), `:60` (`roles/storage.objectViewer` on the runs bucket — read only), `:74` (the app key secret), `:90` (`google_cloud_run_v2_job`), `:208` (the sweep schedule). There is **no** `google_cloud_run_v2_service` for the coordinator, no Cloud Run *launch* grant, and no write grant on the runs bucket. |
| F4 | **The coordinator's writer holds the project's database admin**: `deploy/terraform/gcp/modules/firebase/iam.tf:138` grants `roles/firebasedatabase.admin` to the coordinator's writer account. |
| F5 | **Firebase ID-token verification exists, and it is exact**: `internal/credsvc/auth.go:538` compares the audience to the **bare project id**, `:541` builds the issuer as `https://securetoken.google.com/<projectId>`, and `:842` is the public signing-key endpoint the owner's spec names. Its refusals name the failed check and never the value; `internal/credsvc/audit.go` writes one structured line per act and per refusal. **Exists** — the design's §4 adds no verification of its own. |
| F6 | **Identity Platform is initialized with no sign-in providers, and a public sign-up path is refused**: `internal/cli/init_firebase.go:563` records "initialized Identity Platform (no sign-in providers)", `:690-698` refuses a project whose config enables any public sign-up path, and `internal/infra/identityplatform.go:32` is the step's own name. **Exists** — and it is what F26 collides with. |
| F7 | **The database rules already fence a coordinator subtree**: `internal/budget/rules/rules.json.tmpl:10-13` — `"coordinator": { ".read": "USER", ".write": false }`, with `USER` defined at `internal/budget/rules/rules.go:517` as a signed-in, device-allow-listed person. |
| F8 | **The project's people lists exist and are its only permission model**: `internal/cli/user.go:32-33` writes `terraform.launchers` / `terraform.operators` into the machine's own project config, named together at `:66`, applied by the next update. |
| F9 | **The rigor vocabulary, its order and its one off-axis member exist**: `internal/rigor/mode.go:34` is the closed vocabulary, `:46` is `Default = Pro`, `:75` ranks the four on-axis modes and gives Hotfix **0**, and `internal/rigor/resolve.go:116` states why ("off the axis and ranks below every on-axis mode"). |
| F10 | **The run's ceiling and the enforcer exist**: `internal/runner/spend.go:20-44` is the environment the run reads its ceiling from, and `internal/runner/gateway.go:138` hands that same cap to the gateway — "the real credential stays here". |
| F11 | **Cancelling is a marker, not a kill**: `internal/cli/cancel.go:72-100`. |
| F12 | **A run's launch facts are already recorded**: `internal/runstore/launch.go:18` (`LogURL`). |
| F13 | **Reading a run's logs goes through the Cloud Logging API today**: `internal/backend/gcp/logs.go:29`, on `google.golang.org/api/logging/v2`. |
| F14 | **A run's transcripts land in the runs bucket**, which the coordinator already reads: `internal/runner/session.go:185` writes `session/<id>.jsonl`, and F3's read-only object viewer reaches it. |
| F15 | **`golang.org/x/oauth2` is already a dependency** (`go.mod:16`). There is **no** MCP SDK and **no** OIDC library in `go.mod`. |
| F19 | **The launcher can already set an environment variable for one execution.** `internal/backend/backend.go:83-103`: `Env` and `SecretEnv` are "extra `NAME=value` variables **for this execution only**, merged into the job's own environment (the Cloud Run v2 API merges override env into the job's)", with the budget-token channel as the precedent that already uses one. **Exists** — D924's mechanism needs no new plumbing, only the value and the rule that reads it. |

## 2. What is missing

| # | What is not there |
| --- | --- |
| F16 | **There is no MCP surface at all**: no endpoint, no JSON-RPC handler, no MCP SDK, no tool schema. `ee/coordinator` reads state through `internal/runstore` and writes it through its own sinks; nothing in it answers a protocol. |
| F17 | **There is no authorization server**: no metadata document, no registration, no authorize, no token, no revocation, no PKCE. `go.mod` carries no OIDC library (F15), so the library the owner's open item asks to pick is a **new dependency** the implementation task adds. |
| F18 | **The coordinator cannot launch a run.** F3 lists its grants, and none of them reaches the job: no `roles/run.developer`, and no call from `ee/coordinator` into `internal/backend` (the launch seam `fugaro run` uses). `submit_task` therefore needs a grant that does not exist. |
| F20 | **Nothing carries a token's limits.** `internal/task`'s `Overrides` (`internal/task/task.go:753-758`) holds review rounds, a budget, a model and a timeout, and `:1182-1183` applies the budget to the agent — but there is no rigor window, no Hotfix flag and no scope vocabulary outside the rigor package's own mode. |
| F21 | **There is no configuration home for the front door's own knobs** — the redirect allowlist of D928 — and no CLI surface to mint, list or revoke an MCP token. |
| F22 | **There is no audit record a person can read.** F5's audit is a structured **log line**; a project operator reading the console or the database gets nothing. |

## 3. What conflicts

| # | The conflict, and what follows |
| --- | --- |
| F23 | **P8's front door is authorized by Cloud Run IAM; the owner's spec is authorized by an OAuth bearer.** `docs/coordinator/implementation-plan.md` (P8) chose the IAM invoker deliberately, recorded as a departure from the MCP specification (**Q16 A / U1**), and wrote it before the MCP authorization model was read closely. The two cannot both be true of one endpoint: an IAM-invoked endpoint never reaches the handler without a Google identity, so the 401-then-metadata dance of the owner's §"Auth flow" never happens. **→ D920.** |
| F24 | **P8 is not started and waits on this design**, so the front door has no implementation to amend: D196's service (F1) is decided but unbuilt (F3 has no `google_cloud_run_v2_service`). The design is therefore a **precondition** of P7/P8 rather than a follow-up to them. |
| F25 | **The owner's "the authorize step redirects the user to Firebase sign-in" conflicts with the project's Identity Platform state.** F6: there are no sign-in providers, and a public sign-up path is refused by the install itself. `docs/mobile-auth/reconciliation.md` found the identical conflict, classified it **Wrong** (`:129`) and ruled on it as **D221**: no provider session reaches a project's database; sign-in stays custom tokens only (`:474` onward). **→ D923.** |
| F26 | **"Store only hashes" conflicts with the coordinator's subtree being readable by every signed-in person.** F7: `coordinator/` is `.read: USER`. Hashes, client registrations, revocation state and the audit trail would all be readable by anyone the installation admits. **→ D922.** |
| F27 | **A token spend limit cannot ride the agent's own budget flag.** `internal/task/task.go:1182-1183` copies an override into `Agent.MaxBudgetUSD`, and `internal/agent/agent.go:102-103` turns it into `--max-budget-usd`. That figure is the agent's own cost estimate, and the repository's own note records how wrong it is for non-Anthropic models (`docs/dogfooding.md:61`). A spend limit enforced there is a limit enforced by the wrong number, and **the gateway is the only place the real stop lives** (F10, and `SECURITY.md`'s "the real credential stays here"). **→ D924.** |
| F28 | **The owner's "Hotfix is a separate opt-in flag" conflicts with reading Hotfix as a point on the axis.** F9: `Rank()` gives Hotfix **0**, deliberately, so that a Hotfix rule can never win a comparison against a Pro or Critical one. A window expressed only as `min`/`max` over `Rank()` therefore admits Hotfix at every minimum — the precise opposite of "off by default". **→ D925.** |
| F29 | **The coordinator's spec §4 says "whoever can reach the project's coordinator is in the project; no extra permission model in v1"** (`docs/coordinator/spec.md:25`), and that sentence is true while reaching the coordinator means holding `roles/run.invoker`. D920 removes exactly that, so the sentence stops describing anything and an authorization rule has to replace it. **→ D933.** |
| F30 | **The console's graph write is blocked on "the front door (coordinator P8, not started)"** — `docs/console-dependency-graph/reconciliation.md:330`, with GV3.1 and GV3.2 blocked in `docs/console-dependency-graph/implementation-plan.md:457-489,511,528`. A front door that authenticates a person *could* carry the write, which is why the design has to say whether it does. **→ D927.** |
| F31 | **The coordinator's spec §7 wants MCP tools that overlap the owner's, with different names** (`docs/coordinator/spec.md:46-53`: `report_intent`, `check_conflicts`, `get_project_state`, `record_override`). Two tool sets on one endpoint is a naming decision the implementation cannot leave open: a client sees one `tools/list`. **→ D931's note.** |

## 4. The decisions

Fifteen. Every one is written **recommendation first**, and every alternative carries its cost. The
owner answers them; an answer picks an option and approves no work.

| D | The question | The recommendation | The alternative, and its cost | Blocks |
| --- | --- | --- | --- | --- |
| **D920** | Is the MCP endpoint public, with the bearer token as the only gate, or does it keep P8's Cloud Run IAM invoker? | **Public.** `allUsers` may invoke, and the handler is the gate. Every unauthenticated path is a metadata document or an authorization endpoint with no work behind it; every refusal is audited. It is the only shape a standard MCP client can use. | **Keep IAM invoker** (P8's recorded departure): every caller — including a chat client — needs a Google identity with the project's invoker role and a way to mint an ID token for it. No standard MCP client can, so the feature is reachable only by software already written against Fugaro; the OAuth flow the owner's spec asks for becomes dead code. Cost of the recommendation: the endpoint is on the public internet and the handler is the whole perimeter (spec.md §8). | Everything. |
| **D921** | Which OAuth library? | **zitadel/oidc** for the protocol machinery, with storage and Dynamic Client Registration supplied by the coordinator (the owner's spec already expects DCR to be hand-written). Apache 2.0, OpenID-certified, no new runtime. | **ory/fosite**: the same missing DCR, plus a heavier plugin surface for a flow this small. **A standalone third-party server**: a second resident service in a self-hosted product whose point is one service per project, on a licence the owner has not confirmed — the owner's own open item. Cost of the recommendation: a new dependency in `go.mod`, and a library whose storage interfaces must be fitted to a database it has never seen. | Every endpoint in D921's own scope; the implementation plan's first code task. |
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

**The four the owner should read first.** **D920** decides whether the owner's spec is
implementable at all, and it reverses a departure the coordinator's own plan recorded deliberately —
so the reason has to be worth it, and spec.md §8 is the price. **D923** is not a design choice but a
collision with a decision already taken once (`docs/mobile-auth` D221) on the identical question.
**D922** is the one place where "store only hashes" is not enough by itself. **D924** is the one
place where the owner's own words, read literally, would produce a limit that does not hold.

**What each answer changes.** A different answer to D920 or D923 is a different document: the flow,
the endpoint's shape and §8 all move. A different answer to D927, D928, D930, D932 or D934 is a
parameter and an edit. A different answer to D922, D924, D929 or D931 is an edit plus a task in the plan that
would otherwise not exist — the plan's task list names which.

## 5. The number block, and why it is D920

Read at launch, by hand rather than from a neighbour's line:

- **On `origin/main`** (`3d6ed04d`) the highest number written anywhere in `docs/` is **D900**, in
  `docs/rigor-command/README.md:117`, whose own row says "a later task that needs a number takes
  D901 and re-checks this line's facts rather than its conclusion".
- **In the open pull requests** the highest is **D899** (`docs/cloud-senior-review`, #1472, whose
  block is D880–D899).
- **But a second design PR was opened the same morning**: #1481 (`docs/run-without-checkout`) records
  in its own README that it takes **D901–D919**. That block is therefore not free, and taking it
  would collide with a concurrent work.

So this work takes **D920–D934** — the fifteen decisions above — and **reserves D935–D939**, which
nothing here uses. A later task that needs a number takes **D940** and re-checks this section's facts
rather than its conclusion.

`grep -rhoE 'D[0-9]{3,4}' docs` also returns `D1000`, which is a package-lock integrity fragment and
not a decision; it is recorded here because a reader running the same grep will meet it.

## 6. Owner checks, before any implementation

These are not decisions — no answer changes the design. They are the things this design **cannot**
verify hermetically, and they belong on the live checklist, in the owner's hands, before the first
code task starts:

1. **The endpoint really is reachable from the public internet** once D920's answer says so: a
   request from outside the project reaches the handler and gets the 401, rather than being refused
   by the platform. spec.md §8 says what follows if it is not.
2. **The current MCP authorization specification's text is re-read** against §3 and §6 — the owner's
   own open item. The design is written to the 2026-07-28 revision and its shape is small enough
   that a revision is an edit, not a rewrite.
3. **The project's Identity Platform configuration** is what F6 says of an installation created by
   the current CLI, and what it says of one created earlier; D923's answer is the same in both
   cases, but the authorize step's first message differs.
4. **The Firebase ID token's issuer and audience** for the project at hand match F5's two
   comparisons, from a token minted by the installation's own signer.
5. **One client-credentials caller, end to end**, against a real installation: D929's shape is the
   one this design is least able to exercise hermetically, because a client secret and a token
   rotation are both things a fake proves nothing about.
