# The hosted MCP server: the spec

**Status: design answered by the owner 2026-11-04; M1, M2, M3, M6 and M4's first half are built and
merged on `main`; M5 is built and M5b is built with it — the paired-device approval the owner ruled
a task of its own on 2026-11-07, so the approval this front door takes is the paired device's,
except the delivery of a flow to that device — no QR of the flow's binding is rendered and no
launcher reads the page — which nothing builds and no task of this plan carries; M7 is built except
the reading of the installation's published state its lists come from, which no task of this plan
carries; M10 is built except the store's own bucket and its one grant, which are M4's store half and
not started; M8 is built except the wiring that would serve its four read tools, which no binary has
and no task of this plan carries; M9 is not started; this folder approves no task.** The owner's
spec of 2026-10-24 is §0, copied **verbatim**; the design that follows (§1 onward) is written to the
**answers the owner gave on 2026-11-04** to the fifteen decisions in
[reconciliation.md](reconciliation.md) §4 (D920–D934) — **every one of them the recommendation at
the time** — with the owner's own detail written into §2, §3, §4, §5, §6 and §8. **Where §0 and this
design disagree the reconciliation wins** — §0 is the record of what was asked, not of what this
design proposes, and the one name that moved is the tool §0 calls `submit_task` and this design
calls **`spawn_task`** (§5). Implementation is **not** approved: the owner decides, task by task,
after [implementation-plan.md](implementation-plan.md) has been reviewed.

**Amended 2026-11-04, after the mandatory independent security review.** The design merged as
**#1487** before the SECURITY review of §8 that this folder's own rules require; that review ran on
**2026-11-04** and the owner answered its findings the same day. Every answer is recorded as a
**dated amendment note beside the decision it names** in [reconciliation.md](reconciliation.md) §4a,
with the original answer kept and no number reused, and the one finding that is a decision of its own
is **D936** ([reconciliation.md](reconciliation.md) §4b) — the other decision-shaped finding, **H3**,
is the **D935** the owner answered on 2026-11-05: the store the records move to. **This section is written
to the amended answers.** Where a paragraph below carries an amendment it says so and points at the
finding that made it; **the one amendment that is not the recommendation is H1** — an **accepted
residual risk** with its consequences stated (§3 step 5, §8) and the change that would close it
(§11), not a fix and not a task. The two findings that change this design's shape most are **H2**
(`spawn_task` is **out of the first slice**, §5, D936) and **H3** (the token records **leave the main
database**, §6, D935).

Repository names in this folder are placeholders (`owner/repo-a`, `owner/repo-b`), people are
`person-1`, and a workspace is `workspace-a`; [README.md](README.md) says which names in §0 are the
owner's own words and which are placeholders.

Two rules bind everything below and are not up for a decision:

1. **No agent handles a credential.** The coordinator never hands a model credential or a forge
   credential to an MCP caller — not as a tool result, not in an error message, not in a log line
   the caller can read. The credential the run uses reaches the run the way it reaches every run
   today: the platform mounts it into the job. §9 states this as the front door's one prohibition.
2. **A token limit only narrows.** A token's rigor window, its Hotfix flag, its spend limit and its
   scopes can only make a call *less* possible than the project's own policy, never more. §6 states
   the merge rule; it is the same only-tightens rule `internal/policy` already applies between
   layers.

## 0. The owner's spec (verbatim)

# Fugaro MCP Server — Design Spec

## Summary

Add a hosted MCP server to the Fugaro coordinator agent so that MCP clients (Claude Code, Claude Desktop, other agents) can submit and manage Fugaro tasks directly, without the CLI. Authentication follows the MCP authorization spec (OAuth 2.1 + PKCE), with each Fugaro project's existing Firebase project acting as the identity provider. No third-party auth vendor and only open-source libraries: everything stays self-hostable.

## Goals

- Expose the full Fugaro task lifecycle over MCP.
- Host it inside the coordinator (Go), where task state and project auth already live.
- Comply with the current MCP authorization spec (2026-07-28 revision) so standard clients connect without custom config.
- Reuse each project's Firebase identities; no new vendor or bill.
- Support unattended agent callers as well as human users.

## Non-goals

- A local stdio MCP server (may come later as a thin wrapper around the CLI).
- Replacing project API keys for scripts and CI.
- Any global or shared coordinator. Fugaro is open source and self-hosted; every project runs its own.

## MCP tools

- **submit_task**: start a task on a repo with a prompt, recipe and rigor level.
- **get_task_status**: current state of a task.
- **stream_logs**: tail logs from the running Cloud Run job.
- **get_result**: fetch the output, diff or pull request link.
- **cancel_task**: stop a running task.
- **list_tasks**: recent tasks for the project, with filters.

Candidates for later: list recipes, list repos, promote a task between rigor levels.

## Architecture

- Transport: Streamable HTTP endpoint on the coordinator, implemented with the official MCP Go SDK. The 2026-07-28 spec removed protocol-level sessions, so every request carries its own bearer token.
- One authorization server per Fugaro project, tied to that project's Firebase.
- The coordinator plays two roles:
  - **Resource server**: the MCP endpoint itself; validates access tokens on every request.
  - **Authorization server**: a minimal OAuth 2.1 server embedded in the coordinator using an open-source Go library (see Library options), with Firebase as the upstream login.

## Auth flow

1. Client calls the MCP endpoint with no token and receives 401 with a `WWW-Authenticate` header.
2. Client fetches `/.well-known/oauth-protected-resource` (RFC 9728), which points to the coordinator's authorization server.
3. Client registers via Dynamic Client Registration (RFC 7591), or uses a pre-registered client ID.
4. Authorization code + PKCE flow; the authorize step redirects the user to Firebase sign-in for the project.
5. After Firebase login, the coordinator verifies the Firebase ID token, maps the user to the project and their permissions, and issues its own short-lived access token plus refresh token, scoped to the MCP resource URL.
6. Client calls MCP tools with the bearer token; the coordinator validates issuer, audience, expiry, scopes and token limits on each call.

## Firebase specifics

- Firebase ID tokens are signed by Google; verify them against the public securetoken JWKS. Issuer is `https://securetoken.google.com/<projectId>`.
- Gotcha: Firebase sets the audience to the bare project ID, not a URL, so its tokens fail standard MCP resource checks. This is why the coordinator issues its own tokens instead of passing Firebase tokens through.
- Firebase handles whatever sign-in methods the project already supports (Google, email, etc.).

## Client registration

- Dynamic Client Registration stays open, because MCP clients register before any user has signed in.
- Redirect URIs are restricted to an allowlist: localhost (e.g. Claude Code) and known client callbacks (e.g. Claude Desktop / claude.ai). The allowlist is per client app, not per user or network location; no VPN involved.
- Every new client still needs a user to sign in through Firebase and consent before it gets a token.

## Unattended agents

- Agents with no human behind them get their own identity, not a borrowed user's.
- Option A: OAuth client credentials grant against the coordinator's authorization server.
- Option B: keep project API keys as a fallback for scripts and CI, exchanged for short-lived tokens.
- Align with the unattended-agent options in the 2026-07-28 spec.

## Scopes (initial)

- `tasks:read`: status, logs, results, list.
- `tasks:write`: submit and cancel.
- Possibly per-repo restrictions later.

## Token limits: rigor and spending

- A token can carry optional limits: minimum rigor level, maximum rigor level, and maximum spend.
- Token limits only ever narrow what is allowed, never widen it. Project-level caps and policy always apply on top. Example: a token allowing $500 while the project has $100 left in its budget is held to $100.
- Example use: a CI agent's token sets minimum rigor to Pro, so it can never run at POC or Startup.
- Hotfix sits outside the POC → Startup → Pro → Critical scale, so it is a separate opt-in flag on the token, off by default. Any developer whose token allows Hotfix can start one; no admin role required.
- A task request outside the token's limits is rejected with a clear error naming the limit.

## Security notes

- Short-lived access tokens, rotating refresh tokens, revocation endpoint.
- Store only hashes of issued tokens.
- Strict redirect URI validation against the allowlist.
- Audit log of tool calls per identity.

## Library options (open source only)

- **zitadel/oidc**: Apache 2.0, certified by the OpenID Foundation. Dynamic Client Registration likely needs to be hand-written (a few hundred lines).
- **ory/fosite**: extensible OAuth 2.0 / OIDC SDK. No built-in Dynamic Client Registration.
- **Authplane**: self-hosted OAuth 2.1 server built for MCP, single Go binary, plus Go token-validation middleware with MCP adapters. Licence to confirm before use.

## Open items

- Pick the OAuth library from the options above.
- Confirm Authplane's licence.
- Re-verify the latest MCP auth spec and Firebase / Identity Platform features before implementation.

## 1. What this is, and where it lives

The MCP server is **the coordinator's public face**. The coordinator of
[docs/coordinator/spec.md](../coordinator/spec.md) is one resident service per Fugaro project, and
**D196 answered that the service is the coordinator's own** — built by the coordinator work, not by
the central-orchestrator plan that recommended otherwise. This design does not revisit that: the MCP
endpoint is a route on that service, so there is no second service to run, no second identity to
grant, and no second place a project's state is read from.

That placement is also what makes the front door's authorization model arguable at all. §2 states
the one part of it that departs from both the owner's spec and the coordinator's P8, and **the owner
approved that departure on 2026-11-04** (D920); §8 is the threat model that has to hold, and the
independent review of it is a precondition of every task in the plan.

The service is `ee/coordinator`'s, on the licence boundary the repository already keeps: the
coordinator is the subscription half of the product. This design adds **no** licence notice to any
file and no licence text anywhere, because the folder it is written in is core documentation and the
repository's own rule for a documentation folder says so. **The one licence this folder does turn
on is a dependency's**: D921 was answered with `zitadel/oidc`'s licence **UNCONFIRMED**, and
**nothing the independent security review of 2026-11-04 read could settle it** (finding M-c,
[reconciliation.md](reconciliation.md) §4a) — the library's Apache 2.0 claim is the library's own.
**The owner settled it himself on 2026-11-07.** He ruled that he would clear the set by reading one
table — every module this branch adds or bumps, its licence as the module's own file reads it,
whether it is linked into the fugaro binary, and a column for anything not clearly permissive — that
table is [licence-table.md](licence-table.md), and **he read it the same day and cleared the set,
recording no more than one line: "Cleared: all permissive."** That record is
[licence-gate.md](licence-gate.md), the gate file the check describes, and **the table is evidence
and not the gate: it claims no clearance of its own, and counsel was never asked for the transitive
dependencies' clearance**, so what the tree carries is the owner's clearance and no other
([reconciliation.md](reconciliation.md) §6, check 1). **So the first owner check is met, and the
licence holds nothing back** — a licence that turned out to be unusable would still send D921 back to
its own alternatives, with the library out of `go.mod` and M5 rebuilt without it — **and the gate
that kept M5 dormant was M5b**, the paired-device approval (§3, §8), **which is built**: the
approval the front door takes is the paired device's, and what is left between this package and a
binary is the wiring, which no task of this plan carries.

## 2. The front door: transport, endpoint, and who may reach it

**Transport.** Streamable HTTP, one endpoint, no protocol-level session: the 2026-07-28 revision of
the MCP authorization specification removed them, so every request carries its own bearer token and
the server holds no per-connection state. That is what lets the service scale to zero, which is what
the coordinator's own §7 already required ("plain HTTP requests mean scale-to-zero is no problem").

**The endpoint is public, and the bearer token is the gate** (D920, **accepted by the owner on
2026-11-04** — the public OAuth 2.1 endpoint is the answer). No token, no reachable path that
does anything. What answers a caller who has not yet got one is the 401 that starts the flow, the
two metadata documents the flow needs, and the authorization server's own four endpoints — all of
them work nobody is charged for and none of them reaches the project's state.

This is a **departure from the coordinator's P8**, which chose Cloud Run IAM invoker as the
authorization for the front door — a deliberate departure from the MCP specification recorded there
as **Q16 A / U1**. P8's choice predates this spec, and it cannot survive contact with it: an MCP
client on a laptop or on a phone is a standard MCP client, and no standard MCP client can mint a
Google ID token for the project, nor is it reasonable to ask a person to install `gcloud` and hold
`roles/run.invoker` before a chat client may list their own tasks. **The owner took the departure on
2026-11-04**: the public endpoint is the answer, and §8 is the price it accepts.

**The front door is off until the owner turns it on** (D934, answered **OFF by default** on
2026-11-04 — the recommendation, **amended the same day** — finding M-e). An installation that never
opted into the coordinator has no service for anything to reach; one that did gets the endpoint
**off** until a person turns it on, and a request to a front door that is off is refused before the
handler reads a token. A project that asked for the coordinator's read-only view does not thereby
acquire a public write endpoint.

**"Off" means the binding is absent, not a flag the handler reads** (the amendment of
2026-11-04). A public service with a switch inside it is a public service whose whole handler is the
perimeter; the switch is therefore **the presence or absence of the `allUsers` invoker binding** on
the service, and `fugaro update` is what adds it. That collides with the installer's own cover rule
and the collision is recorded rather than worked around: `internal/infra/tf/cover.go:913` refuses
`allUsers` in a plan, so the plan that turns the front door on is **not covered** and its stage asks
its **own typed confirmation** (`internal/infra/tf/cover.go:911`). The task that builds it must
**not** widen that cover rule to make its own plan quiet, and its convergence test expects the
confirmation rather than a silent apply.

**What the service answers without a token**, and nothing else:

- `/.well-known/oauth-protected-resource` — RFC 9728, naming the resource and pointing at the
  coordinator's own authorization server.
- `/.well-known/oauth-authorization-server` — the authorization server's metadata.
- `POST /oauth/register` — Dynamic Client Registration (RFC 7591).
- `GET /oauth/authorize` — the authorization request, which renders the page a person reads — and
  `POST /oauth/authorize`, which takes two acts and never one that is both: **the paired device's
  approval** (the flow's id, the number the page shows and the ID token of that device's own
  session, M5b) and **the page's answer** (the flow's binding and the decision, and no credential).
  One path, two methods: M-a's approval is a POST, and the GET verifies nothing.
- `POST /oauth/token`, `POST /oauth/revoke`.

**Every other request** is the MCP endpoint, and it is the resource server: it reads the bearer
token, validates it (D920), and only then looks at the JSON-RPC body.

**A public service is public per route, not per feature** (D920, amended on 2026-11-04 — finding
M-e). `allUsers` on a Cloud Run *service* publishes **everything the service serves**, and the
coordinator's own P8 tools — `report_intent`, `check_conflicts`, `get_project_state`,
`record_override` (`docs/coordinator/spec.md:46-53`) — are served here too (§5, D931). Three rules
therefore bound the service, and they are the service's own, not the handler's:

- **The service serves no route that is not the MCP endpoint or one of the six unauthenticated
  paths** listed above. A second route is a finding, not a convenience.
- **Every tool maps to a scope, and a tool with no mapping is refused** (§5). A tool registered
  without a scope is not served at all, so "the six tools are reachable" is true of the source and
  not only of this document.
- **`max-instances` is capped and the request body size is capped**, so an unauthenticated caller
  cannot make the public service scale, and an **unauthenticated refusal is logged and never written
  to the database** — a stranger with a loop must not be able to grow the project's storage.

**The metadata document's shape.** A Firebase ID token's audience is the bare project id rather than
a URL, which is the gotcha the owner's spec names. The design answers it exactly as the spec says it
should: the coordinator issues its own tokens and never passes a Firebase token through as a bearer.
A Firebase ID token is accepted at **one** endpoint, the **approve step's `POST /oauth/authorize`** —
the owner's answer of 2026-11-04, amended the same day (M-a) so that the token travels in that POST's
body and **never in a GET URL** — and not at `POST /oauth/token` as an earlier draft of this design
had it: it is the proof of the *sign-in* step, which is the authorize step, and it is never a grant
the token endpoint accepts. The access token the coordinator mints afterwards carries a real resource
audience — the coordinator's own URL — so a client's standard check works.

## 3. The authorization server

**Library** (D921, answered 2026-11-04, **amended the same day** — finding M-c). The authorization
server is built on **zitadel/oidc** — the first option the owner listed, and the owner's answer —
with the coordinator supplying two things the library does not: the storage over the project's own
store (§6), and Dynamic Client Registration, which the owner's spec already expects to be a few
hundred hand-written lines. **The library's licence was answered UNCONFIRMED**, and the review
settles the library's *behaviour* against this design and not its licence, so the owner confirmed it
himself: **he read [licence-table.md](licence-table.md) on 2026-11-07 and cleared the dependency set
in one line — "Cleared: all permissive" — recorded in [licence-gate.md](licence-gate.md)**
([reconciliation.md](reconciliation.md) §6, check 1). **Counsel was never asked, and no line here is
counsel's clearance.** So the check that gated M5 is met, and **the paired-device approval the owner
ruled a task of its own (§8) is built with it**: the front door approves through the paired device,
and what still holds the package off a binary is the wiring rather than the licence — a licence that
had turned out to be unusable would send D921 back to its alternatives rather than to a
workaround. The alternatives and their costs stay in the reconciliation; the short version is that
ory/fosite brings the same missing piece and a heavier plugin API for a flow this small, and a
standalone third-party server is a second resident service in a self-hosted product whose whole
point is one service per project.

**The library's defaults are not this design's rules, and the review said so.** Read at 3.51.17, the
version the review looked at, it makes **PKCE optional** and reads an empty challenge method as
`plain`, **silently drops an unknown scope**, enables **grants this design does not want**
(jwt-bearer, token exchange, device code, implicit), **wants a `SigningKey`** this design never gave
a home, and serves its discovery document as `openid-configuration` rather than the RFC 8414 shape
§2 names. A general-purpose authorization server's defaults would therefore reverse four of this
section's rules at once. The answer is a **thin constraint layer the coordinator applies before the
library sees a request**:

- **A missing challenge, or one that is not `S256`, is refused at `/oauth/authorize`**, and so is an
  **unknown scope** — both ahead of the library, so neither the `plain` fallback nor the
  drop-what-you-do-not-know default can decide what a client gets.
- **Only the authorization code and refresh grants are enabled.** The rest are off, not filtered.
- **The signing key lives in Secret Manager**, or the server issues **opaque tokens**; there is no
  third answer that leaves key material somewhere this design did not plan for.
- **The metadata document published is the one §2 names**, not the library's own shape.

The four citation points and the version are the **review's**, read outside this container, so they
are marked **UNVERIFIED** in [reconciliation.md](reconciliation.md) §4a and M5 re-reads them against
the version it pins.

**What the server is not.** It is not an OpenID provider for the project. It issues no ID token of
its own, it holds no user directory, and it will never be the thing a project's other software
authenticates against. It answers one question — *may this client, on behalf of this identity, call
this MCP endpoint with these limits* — and it is small enough to be read in one sitting, which is the
point.

**Dynamic Client Registration** (D928) stays open, as the owner's spec requires, because a standard
MCP client registers before any person has signed in. Openness is bound by four rules:

- A registration grants **nothing**. A registered client holds no token and reaches no tool until a
  person signs in and consents.
- Registration is **rate-limited** per source and **audited**: one record per registration, naming
  the client and the redirect URIs, never a secret.
- The registration response returns the client secret **once**, and only the hash is kept (§6).
- A client's redirect URIs are validated at registration **and** at authorize, against the same
  rule (D928): a loopback address (`http://127.0.0.1:*`, `http://localhost:*`) is always allowed,
  because that is how a native client receives a code; any other URI must match the project's own
  allowlist, which is a line in the project's configuration and **not a constant in the
  coordinator**. **Registration refuses a non-loopback URI that is not on the allowlist** — the
  owner's answer of 2026-11-04 — so a client that could never receive a code here is refused at the
  earlier door with a message naming the rule, rather than registering and failing at a person's
  sign-in. The owner's spec names a chat client's hosted callbacks as the example of the allowed
  kind; this design keeps the example out of the code, because a vendor's callback URL baked into a
  self-hosted product is a URL that goes stale in somebody else's release cycle.

**The rate limit and the expiry are not built.** The registration endpoint that ships with M5
refuses nothing by rate and sweeps nothing: it writes one record per registration, so the audit
holds, but there is no limiter in front of it, no sweep, and no read-time bound either — a
registration carries the time it was made and nothing compares that against anything, so a
registration stays a client for as long as the store holds it. **Both bounds belong to no task of
the plan**, M5 builds the endpoint and neither bound, and this paragraph names the gap and raises it
to the owner rather than closing it here ([reconciliation.md](reconciliation.md) §4a). The flow's
own two records, the pending request and the code, are the pair M5 does bound on read: each refuses
its record once its TTL has passed, and the sweep all three shapes wait for is what is missing.

**The flow, end to end** (the owner's §"Auth flow", with the places this design changes it — none
of them the endpoints, all of them what happens at them):

1. A client calls the MCP endpoint with no token; it gets 401 and a `WWW-Authenticate` header naming
   the resource metadata URL.
2. It reads `/.well-known/oauth-protected-resource` and finds the authorization server.
3. It registers (RFC 7591), or uses a client id a project operator pre-registered for an unattended
   caller (§7).
4. Authorization code + **PKCE with S256, required and never optional**. The code is single-use,
   short-lived, and bound to the client, the redirect URI and the code challenge. **A missing
   challenge and a challenge that is not `S256` are refused by the coordinator before the library
   sees the request** (finding M-c), because the library instead reads a missing method as `plain`.
5. The user signs in, and **the sign-in is approved by a paired device** (D923, the owner's answer
   of 2026-11-04). **This design does not redirect to a Firebase-hosted sign-in page**: the
   project's Identity Platform is initialized with no sign-in providers and refuses any public
   sign-up path, so there is no hosted page to redirect to, and turning one on is the exact change
   `docs/mobile-auth` ruled against in D221. What approves the sign-in is the mechanism the
   repository already has — the pairing of `fugaro pair` (`internal/cli/pair.go`, D165): the person
   approves from a device paired to the installation, that device's session is the signer, and
   **the coordinator never mints a custom token** and holds no signer role. The coordinator verifies
   the Firebase ID token the approval yields the way `internal/credsvc` already verifies one
   (`internal/credsvc/auth.go:1069`), and it **consumes that token here and nowhere else**: it is
   the proof of the sign-in step, and it is never a grant at `POST /oauth/token`.

   **The approval is the flow's weak step, and the amendment of 2026-11-04 states it plainly rather
   than fixing it** (finding H1, the one amendment that is **not** the recommendation). A device
   token is a **custom token** — uid `m~…` with claim `mu` = uid
   (`internal/budget/token/device.go:176` `MintDevice`, `internal/cli/pair.go:353`) — and **every
   launcher and operator holds `signJwt` on the project's signer**
   (`deploy/terraform/gcp/modules/firebase/iam.tf:35-41`) and can read `config/admin/devices`
   (`:44-50`). **So any launcher can mint a device token and approve a sign-in as an operator's
   device**, and the coordinator, which only verifies what a device's session produced, cannot tell
   the difference — the repository's own rules comment already says the `mu` claim "binds nothing
   against a minter" (`internal/budget/rules/rules.go:482-487`). **This is an ACCEPTED residual
   risk, not an oversight**: it is recorded here and in §8 with its consequences (§8), and the
   change that would close it is named in §11. **It is in no task** — building it is a later
   decision, not a fix smuggled into this slice.

   **The approval is hardened against the other thing a phone cannot see** (findings H1 and M-a, the
   owner's answers of 2026-11-04). §8's "loopback is the attacker's machine" argument **fails when
   the approval happens on a phone**, where the person cannot see which machine is asking:

   - approval requires **QR proximity or number matching**, so a person approves a request they can
     see came from the screen in front of them;
   - the phone shows **the client — marked unverified — and the redirect host**, so a client that
     named itself after the product is not thereby trusted;
   - approval is **a single-use POST on a named endpoint**, and **an ID token never travels in a GET
     URL**, where it would land in history, logs and referrers;
   - **nothing is pushed without a person starting it.**

   **The first of those is built on the server's side and not yet on the device's.** M5's approve
   step rendered the flow's number on the page and posted it back from the same page's form, so the
   number was a binding between that page and the approval and **not the device-side approval this
   bullet asks for**: the paired device never saw the number, and a page rendering a flow of its own
   knows its own number ([reconciliation.md](reconciliation.md) §4a, D923's M-a amendment). **The
   owner answered it on 2026-11-07: it is M5b, a task of its own and a hard prerequisite before
   anything wires the front door** ([implementation-plan.md](implementation-plan.md) §3). **M5b is
   built, and what it built is the half a server can**: the approval is a POST of its own carrying
   the flow's id, the number the person read off the screen and the ID token of the paired device's
   session, the page's answer carries the binding and the decision and no credential, and the code is
   minted only for a flow the device approved. **What is not built is how a flow reaches that
   device** — no QR of the flow's binding is rendered and no launcher reads the page — so the act the
   endpoint accepts is one no client produces yet, and a person who reads the number off the page has
   nothing to type it into. **That half is owed and named in §11**: no task of this plan carries it,
   and the wiring task carries it beside the two bounds the registration endpoint is accepted under
   (`approval.go`'s own header states the same gap where it is read).
6. The coordinator issues the authorization code, and the exchange at `POST /oauth/token` mints a
   short-lived access token and a refresh token, scoped to the MCP resource URL, carrying the
   identity, the client, the scopes and the limits of §6.
7. Every subsequent call is validated on its own: signature, issuer, audience, expiry, revocation,
   scopes, and the tool's own limit check.

**The consent step is a page a person reads, and it is bound to the flow.** Before the code is
issued, the coordinator shows what is being consented to — the client, and **the host the redirect
URI names** (D928, the owner's answer of 2026-11-04), so a person consents to somewhere their code
will be sent rather than to an opaque client id. **The flow is CSRF-bound**: the authorize request
carries a `state` the client generated, the coordinator keeps its own binding between the request
and the consent it answers, a consent that does not carry that binding is refused rather than
honoured, and the `state` is returned on the redirect unaltered. Without that binding an attacker's
client can finish a sign-in the person started for another.

## 4. Firebase as the identity provider

The verification is **not new work**: `internal/credsvc/auth.go` already verifies exactly this token
— the issuer is built as `https://securetoken.google.com/<projectId>`, the audience is compared
against the bare project id, and the signing keys come from the public `securetoken` x509 endpoint.
The design reuses that verification rather than writing a second one, and it reuses the two
properties that make it safe to run in a service that answers strangers:

- **A refusal never echoes the token.** `internal/credsvc`'s refusal messages name the check that
  failed and never the value that failed it. An MCP endpoint that echoes a malformed token is a
  logging leak with a public address.
- **One structured line per act and per refusal**, on the pattern of `internal/credsvc/audit.go`,
  naming the identity and the outcome and never a value.

What is genuinely new is the **mapping from an identity to a project's permissions**, and the answer
is that v1 does not invent one. The project already has two lists — the launchers and the operators
`fugaro user` writes into the project's configuration — and the front door reads the same lists:
**a launcher may call the task tools, an operator may also read the audit record.** The coordinator's
own spec §4 says "whoever can reach the project's coordinator is in the project; no extra permission
model in v1", and that sentence was written when *reaching* the coordinator meant holding a Google
identity with `roles/run.invoker`. Making the endpoint public (D920) removes the thing that sentence
leaned on, so this design puts the project's own two lists in its place — and **the owner answered
D933 on 2026-11-04 exactly that way: only the project's launcher and operator lists may call it.**
That is the one place the design adds an authorization rule the coordinator's spec does not have, and
it adds it because D920 otherwise leaves a public endpoint with no rule at all.

**It is also the shape the repository already has**, which is why it is a list and not a new model:
the credentials service admits the installation's **operators** by their verified Google ID token
email, through the caller class its design calls the **person class** (BB4) —
`internal/credsvc/auth.go:375` (`PersonAccounts`, "the installation's operators" at `:350`), reached
on Bitbucket by `:890` (`VerifyReadCaller`) and on GitHub by `:968` (`VerifyPersonCaller`), with the
class's own row at `docs/bitbucket-credentials/implementation-plan.md:131`. A person reaching a
project-scoped service by a verified email, against a list the installation already maintains, is
therefore the pattern here rather than an invention — and it is the same two lists `fugaro user`
writes (`internal/cli/user.go:66`).

**And the lists are read from somewhere a caller cannot write** (finding M-f, the amendment of
2026-11-04). The design left this unspecified, and it is not a detail: the lists are written today
into **each operator's own local config** (`terraform.launchers` / `terraform.operators`,
`internal/cli/user.go:32-34`) and applied to the installation by that operator's `fugaro update`, so
"the project's own two lists" could mean **one machine's file**. The front door reads the
installation's **published state** — the copy the installation's own account can read and a caller,
a token or a run cannot — and never a local file. What is *in* the lists is unchanged: the same two
lists, and the same entries.

**And v1 reads one entry kind only** (D923, the owner's answer of 2026-11-04): **the lists are read
for exact `user:<email>` entries, and `group:`, `domain:` and `serviceAccount:` entries are refused
at the front door.** That is not a new rule invented here — it is the rule `fugaro user` already
applies at its own door, for the reason its own comment gives: the launcher and operator roles carry
the token signer's own role, so a `domain:` member would hand every account in a domain the right to
mint a run's token (`internal/cli/user.go:254-257`, refused at `internal/cli/user.go:266`). A front
door that admitted a
`domain:` entry would be the way such an entry got in after all, so it refuses them for the same
reason and says which kind it will not read. Nothing here is a rule about the database: §6's subtree
is closed to every client, so the two can no longer differ.

## 5. The tools

The owner's six tools, with the shape each one is allowed to take. Three rules apply to all of them
and are enforced in one place, ahead of every handler:

- **Every tool maps to a scope, and a tool with no mapping is refused** (finding M-e). The service is
  public (D920), so "which tools exist" is a security property and not a documentation one: a tool
  registered without a scope is **not served**, and the registry refuses it rather than defaulting it
  to `tasks:read` or to open.

- **A tool never returns a credential.** Not a model key, not a forge token, not another token's
  value. The coordinator holds neither a model credential nor a forge credential of its own to hand
  over; the run's credential is mounted into the job by the platform, exactly as today. §9.
- **A tool returns data, and only data.** Every answer is plain text or JSON. No answer is a URL the
  caller is invited to fetch, no answer is markup, and no answer quotes a log line as though it were
  an instruction. A task prompt, a branch name and a pull request title are strings somebody else
  wrote; they are returned as values and never composed into a sentence the coordinator appears to be
  speaking.

| Tool | Scope | What it does | What it must not do |
| --- | --- | --- | --- |
| `spawn_task` | `tasks:write` | **DEFERRED — not in the first slice** (D936, finding H2). When it is built: starts a run on a repository with a prompt, a recipe and a rigor mode (D931) | Must not widen the run's ceiling, must not pass a model credential, must not launch anything but the project's own job, must not reach the job except through the launch path below |
| `get_task_status` | `tasks:read` | The run's state, from the run record | Must not read the runs bucket outside the project's own prefix |
| `stream_logs` | `tasks:read` | The run's recorded progress (D932) | Must not stream the project's whole log |
| `get_result` | `tasks:read` | The outcome, the diff or the pull request link | Must not return a body from the forge that carries a token |
| `cancel_task` | `tasks:write` | Writes the cancel marker `fugaro cancel` writes — **deferred with `spawn_task`** (D936: the marker is an object in the runs bucket, the second privilege the coordinator does not hold) | Must not stop a job the project does not own |
| `list_tasks` | `tasks:read` | Recent runs of the project, with filters | Must not list another project's runs |

**The tool is `spawn_task`, not the `submit_task` of §0** — the owner's name for it, answered on
2026-11-04, and the one this design, [reconciliation.md](reconciliation.md) and
[implementation-plan.md](implementation-plan.md) all use. §0 keeps the old name because §0 is copied
verbatim; there is no second tool.

**The first slice is the authorization server and the read tools, and `spawn_task` is not in it**
(**D936**, the owner's answer of 2026-11-04 to finding H2 — **DEFER**). The review found that
`spawn_task` needs **three privileges, not one**, and that the design had priced only the last:
`signJwt` to mint the run's custom token; **write to the runs bucket**, which the coordinator does
not hold (F3 is `roles/storage.objectViewer`); and **`run.jobs.runWithOverrides`**, which can
override **container arguments** — the base image's entrypoint is `tini --`
(`images/base_test.go:59`), so an override can run **any command beside the job's mounted git and
model credentials**. That contradicts D923's own "the coordinator mints nothing" and M6's
`TestTheCoordinatorMintsNoCustomToken`. **So the first slice is the authorization server plus
`get_task_status`, `stream_logs`, `get_result` and `list_tasks`; `cancel_task` goes with `spawn_task`
into the later design item rather than into the first slice**, because the marker it writes is an
object in the runs bucket (`internal/runstore/runstore.go:1581`, `fugaro cancel`'s own write) and so
needs the same runs-bucket write the coordinator does not hold — "without the launch path" is not a
condition it can meet. No task in [implementation-plan.md](implementation-plan.md) starts a run.
**What a later `spawn_task` design item has to answer** is written in
[reconciliation.md](reconciliation.md) §4b (D936): no `runWithOverrides`, per-run input under a
bucket prefix only the coordinator writes, run-token minting moved out of the public service, and an
**argument allowlist** — no `--no-budget-check` (`internal/cli/run.go:325`), no `--token` (`:313`),
no free-form `--run-id`, and `mode = max(requested, the token's minimum)`.

**And when it is built there is one launch path, not two** (D931, the owner's answer of
2026-11-04). `spawn_task` does not reach the run's job by a route of its own: **concurrency, rate, idempotency and the kill
switch all go through the same pre-launch code `fugaro run` uses**, so a run the front door starts is
refused by exactly what refuses a run a person starts. Concretely, the front door takes the same
launch claim in the runs bucket (`internal/runstore/launch.go:112`, `ClaimKey`, with `ClaimTTL` at
`:117`) before it starts anything, which is what makes one submit one run and a repeated submit an
answer about the run already in flight rather than a second one; and it runs the same budget
pre-check of kill switches, caps and headroom — `internal/budget/precheck.go:58` (`budget.Precheck`),
which `internal/cli/launch_budget.go:283` is the CLI's own call to — before it mints anything. The
front door's own concurrency and rate limits are enforced **at that same point**, not beside it. A
second path is the failure this rule exists to prevent: a front door that skipped the kill switch or
the claim would spend money a person's launch could not, and would let a retrying agent turn one
submit into two runs. The tool is a caller of that path and never a second implementation of it.

The "candidates for later" the owner names — list recipes, list repos, promote a task's rigor — are
**not** in v1 and carry no scope (D927). A scope that exists is a scope a token can hold, and a
token that holds one cannot be narrowed without breaking it.

## 6. Tokens: identity, limits, and what is stored

**What an access token carries.** The identity it acts for (a person, or an unattended client), the
client it was issued to, the scopes, its expiry, and the limits below. The access token is
short-lived and is validated on every request; there is no session behind it, which is what the
2026-07-28 revision requires.

**Rigor limits** (D925, D926; both answered as recommended on 2026-11-04). A token carries an
optional minimum rigor mode, an optional maximum
rigor mode, and a **separate Hotfix flag, off unless a person turned it on**. The window is read
against the four on-axis modes; the Hotfix flag gates Hotfix by itself. That split is not a
convenience: `internal/rigor` deliberately ranks Hotfix **0** — below every on-axis mode — precisely
so that a Hotfix rule can never win a comparison against a Pro or Critical one. Folding Hotfix into
the window would make `min: pro` admit a Hotfix request, which is the opposite of what the owner's
"separate opt-in flag, off by default" is for. A request outside the token's limits is refused with
an error **naming the limit** — the mode the token allows and the mode that was asked for — because
the owner's spec asks for exactly that and because an agent that cannot see which limit it hit will
retry the same request.

**And nothing in the endpoint M5 ships turns the flag on.** `Limits.Narrow` takes the tighter side,
so a token can only ever make Hotfix *less* true than the installation already allows, and the
approval page of §3 carries no control that would make it true: a token minted by the flow that
exists today carries `Hotfix: false` whatever the project's own `hotfix:` line says, and the window
it carries is the installation's own, because nothing in an authorization request asks for a
narrower one. Where a person turns the flag on is a product decision no task in the plan has taken,
so this paragraph states both rules and says plainly that the way to *use* them is not built yet.

**The spend limit narrows the run's ceiling** (D924, answered as recommended on 2026-11-04). The
token's spend limit is never sent to the
agent as its own budget figure. It is applied where the run's ceiling is applied: the run's ceiling
is `min(the project's configured cap, the token's limit)`, the same "only tighten" merge the budget
already does between a repository's cap and the installation's. Concretely, a submitted run starts
with an environment override on the job carrying the token's limit in **a variable of its own**, and
the run's ceiling is the tighter of that and the job's own — so **the gateway enforces the token's
spend limit**, the run's ledger charges against it, and a wrong number at the coordinator cannot
widen a ceiling, because the run takes the tighter side itself. The one thing the design refuses
outright is routing the limit through the agent's own `--max-budget-usd` flag: that figure is the
agent's own estimate of cost, and the repository's own dogfooding notes record that it misprices
non-Anthropic models by roughly an order of magnitude. A limit enforced there would be a limit
enforced by the wrong number.

**And spend is counted across the whole token family**, not per access token (the owner's answer of
2026-11-04). A rotated refresh token is the same family spending against the same counter, so the
rotation of D930 is a lifetime mechanism and never a budget mechanism: a client cannot reset its
limit by rotating, and the number the operator set is the number the family gets. This is the reason
the counter is the family's and not the token's — a per-token counter would make the tightest
control in the design trivially optional.

**And the family counter is reserved, not merely read** (finding M-b, the amendment of 2026-11-04).
Read-then-spend is a race: two runs started from one family would each read the same remaining figure
and each be allowed it, so the limit would hold for one run at a time and not across concurrent ones.
The amended answer, built by M2 and M9 (**M9 is deferred** — D936 — so the reservation ships with
it, and M2 carries the counter's shape):

- **At spawn, reserve `min(the job's cap, the family's remaining)` atomically against the family
  counter**, with a conditional write **on the same read** — the generation-matched replace
  `internal/blobx` gives the bucket the records live in (`internal/blobx/blobx.go:1`, M2) — so
  exactly one reservation wins and the loser is told what remains rather than being handed the same
  headroom.
- **At finalize, settle**: the reservation returns to the family whatever the run did not spend, so
  a run that stops early does not hold the family's headroom for its whole lifetime.
- **For a `client_credentials` token the family is the client** (D929, amended), which is the only
  family such a caller has.

**A limit is unset or it is a number, and never a `0.0` that means both** (a low finding, the
amendment of 2026-11-04). The limit is an **optional integer in micros** and not a `float64`: a
floating-point zero cannot tell "no limit of its own" from "a limit of zero", and that difference is
the whole of "only narrows" (D924). An **unknown rigor-mode string is refused**, not ignored — a
token whose window names a mode this build does not know is refused rather than read as unset.

**The micros half of that is decided here and not built.** The shipped field is still
`Limits.SpendUSD float64` (`ee/coordinator/mcpauth/limits.go`), so today an unset spend limit and a
limit of zero are the same value and only the pass-through rule tells them apart; moving the field
to micros is **M9's** own decision rather than a second one taken here
([implementation-plan.md](implementation-plan.md), M9; reconciliation.md §4a).

**What is stored, and where** (D922, answered 2026-11-04 — **the recommendation**, **amended the
same day** by findings H3 and M-c; D935). Only **hashes** — never a token value, never a refresh
token, never a client secret — and the record lives in a **separate top-level subtree of the
project's database, not under `coordinator/`**: the **`mcpauth`** node. The node carries
**`.read: false`** and **`.write: false`**, and **only the coordinator's own account writes it** —
that account holds `roles/firebasedatabase.admin`
(`deploy/terraform/gcp/modules/firebase/iam.tf:138`), which is what makes "the coordinator writes it
and no client does" true in one rule rather than two.

**But the rules are not the perimeter, and the review said so** (finding H3). **IAM bypasses the
database rules**: every launcher and operator holds `roles/firebasedatabase.viewer`
(`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`), admins and budget admins hold
`roles/firebasedatabase.admin` (`:53-58`), and the history job holds it too (`:81-84`). A node the
rules close is therefore still **readable by every launcher** and writable by several accounts. The
amended answer has four parts, and the rules clause above is only the first:

1. **The records do not live in the main database at all** (**D935**). They live in a store the
   **public service alone reaches**, whose **IAM names exactly one account** — **the bucket
   `fugaro-mcpauth-<gcp-project>` of
   [implementation-plan.md](implementation-plan.md) §8, whose one member is the front door's own
   `mcp_server`**, the answer the owner gave on 2026-11-05 to the one thing D935 had left open. It
   is a bucket rather than a second database or a second vendor because it needs no new service and
   no new bill, and the costs it does carry (a second storage system, a second emulator story, a
   `fugaro update` step) are stated rather than hidden. **The `mcpauth` node stays named and
   denied** in the main database's rules as a **tripwire**: with the records elsewhere, an MCP record
   written there is a failing test rather than a quiet widening.
2. **What is stored is keyed, not merely hashed.** Token records hold **HMAC-keyed hashes with a
   pepper held in Secret Manager**, so a reader who is not the coordinator cannot reverse one even
   with a weak token space; and **every record's identity, its limits and its revocation state are
   MAC'd**, so a writer the rules do not bind — a database admin, a launcher with a signer, a
   compromised run — cannot forge one. **This is the design's requirement and it is not what the
   code does**: the store hashes with a plain unkeyed `sha256` and writes no MAC
   (`ee/coordinator/mcpauth/store.go:494`), so a reader who is not the store's own member reads an
   identity in the clear. That gap is M2's own row, merged on `main` and not in this branch, and it
   is carried as a finding in §11 and [reconciliation.md](reconciliation.md) §4a rather than read
   here as a control the tree performs.
3. **The public service holds no `firebasedatabase.admin`.** It holds **viewer only**, and only for
   the pre-check it runs before minting. The account that writes the store is not the account that
   answers strangers.
4. **The value is shown once, at mint, and never again** (§8).

**One half of D935 stays UNVERIFIED**, and it is an owner check
([reconciliation.md](reconciliation.md) §6, check 8): whether **GCS still attaches its convenience
bindings to a Terraform-created bucket**, so that a basic project Viewer reads the store until M4
strips or refuses those members (`SECURITY.md:88`, and what `internal/infra/statebucket.go:140` does
for the state bucket). A live project is the only thing that can show it; the plan's M4 owns
stripping or refusing them either way.

**The node is closed on both sides, and after the amendment it holds nothing to argue about.** **No
client reads it and no client writes it**, and the template's root denies by default:
`mcpauth` carries no read rule for `TOKEN` or `USER` to satisfy
(`internal/budget/rules/rules.json.tmpl:2-4` is the root's own
denial, and `:10-13` is the `coordinator` node whose `USER` read `mcpauth` deliberately does not
take). The rules' two claims are unaffected — `USER` (`internal/budget/rules/rules.go:517`) is what
`coordinator/` still grants and `mcpauth` deliberately does not, so the two subtrees differ in
exactly the way the decision was asked about.

**What the amendment changes about this paragraph is what may be claimed for it.** The node is not
"readable by nobody": every launcher holds `roles/firebasedatabase.viewer` and could read it if
something were written there, which is finding H3 and the reason the records left the database
altogether. So the correct statement is the narrower one: **what the rules close, they close against
every client; what they do not close — IAM — is answered by the records not being there, by the
public service holding viewer and not admin, and by the two controls of §6 that are **designed and
not built**: the HMAC-keyed hashes and the MACs (§11, [reconciliation.md](reconciliation.md) §4a).**
"Store only hashes" is the *second* line of defence and not the only one; the value is shown once at
mint and never again.

**D922 was answered the other way first, and restated the same day.** The first answer kept the
separate subtree but made it readable by any signed-in user of the project; the restatement of
**2026-11-04** closed it, and this design is written to the restatement.
[reconciliation.md](reconciliation.md) §4 records both and why the later one is the record.

**Rotation and revocation** (D930, answered on 2026-11-04 as **rotating refresh tokens and
short-lived access tokens** — the recommendation, **amended the same day** — finding M-d). The
refresh token rotates on every use, and a replayed refresh token revokes the whole family: rotation
is what makes a theft detectable rather than merely survivable. Revocation is a first-class endpoint,
and it is also what an operator does in the console
or from the CLI; either way it is one delete, and the next call with the revoked token is refused.

**The rotation is conditioned on the record it read** (finding M-d). Check-then-write is a race: two
requests presenting the same refresh token both read it as live and both rotate, so a genuine
double-submit reads as theft and a theft reads as a double-submit. The amended answer, built by M2:
**the rotation is conditioned on the generation of the same read** — the generation-matched replace
`internal/blobx` gives the bucket the records live in (`internal/blobx/blobx.go:1`, M2), which is the
condition an `ETag` PUT gave a store inside the database — so exactly one rotation wins. **The
answer's second half is not built**: the loser inside a **small grace window** is meant to be
answered with the winner's own new pair rather than read as a replay, so a client on a slow link is
not logged out by its own retry — and no grace window exists in the tree. What the loser gets
instead is `invalid_grant` (`ee/coordinator/mcpauth/token.go:323`), which is terminal and carries no
invitation to try again, because the retry that a 5xx invites presents the spent token and that
presentation is the theft signal itself: it revokes the family the winner is holding, so one
double-submit would become a logout. A client that re-authorizes instead loses nothing but a page;
the window is what a client that retries the exchange would need. A replay **outside** the window revokes the family, exactly as above.

**Only narrowing** (the second rule of this folder). Scopes, the rigor window, the Hotfix flag and
the spend limit all merge by taking the tighter side against the project's policy, the repository's
configuration and the run's own ceiling. Nothing a token carries can raise a cap, run a mode the
project forbids, or reach a repository the project's policy excludes.

## 7. Unattended agents

An unattended caller is a **client with its own identity and no user behind it** (D929). It is
pre-registered by a project operator — a client id and a secret, the secret stored as a hash — and it
gets its token from the client credentials grant. Its token always carries limits: a client
credentials token with no rigor window and no spend limit is a token that can spend the project's
budget at whatever mode it likes, and the design refuses to mint one.

**And it is off until it has an owner** (finding M-f, the amendment of 2026-11-04). Two things the
answer did not say, and the review found both: **an unattended client is not a `user:` entry**, so
§4's rule — which reads exact `user:` entries and nothing else — **refuses every client-credentials
caller**; and **no task owned pre-registration**, the client id and secret the mechanism needs. The
amended answer:

- **`client_credentials` stays off until it has an owner.** The switch is not turned on by anything
  in the first slice, and the first slice's tasks build no registration surface, so the mechanism is
  designed and not wired.
- **Pre-registration is refused inside a coding agent's session**, the same speed bump `fugaro user`
  makes at its own door (`internal/cli/user.go:56`) and for the same reason: an agent holds the
  operator's credentials and could write the file itself, so the refusal is a speed bump and not a
  boundary — and it is enough to keep an agent from quietly creating a long-lived caller nobody
  reviewed.

The rest of the answer stands: when it is switched on, a pre-registered confidential client, limits
mandatory, and one identity per caller so the audit record means something.

The owner's option B — project API keys exchanged for tokens — is **not** taken, and the reason is
the owner's own non-goal: the keys are not to be replaced. Making a key exchangeable turns every key
into an OAuth client by another name, so every place a key is stored grows the token lifecycle with
it, and revocation stops being a single act. §7 of the reconciliation records the cost of that
choice plainly: a script that already holds a key and wants an MCP token has to be given a client id
and a secret instead, which is a change to that script.

**An unattended caller is where the "no agent handles a credential" rule is load-bearing**, because
the caller is software. Its token reaches the six tools and nothing else. It cannot read another
identity's audit record, it cannot read the project's credential material through any answer, and it
cannot ask the coordinator for a credential on its own behalf — because the coordinator has none to
give (§9).

## 8. Threat model

This section is the one the owner's brief requires an independent review of. **That review ran on
2026-11-04** — after this design had merged, which is why its findings are amendments rather than
answers — **the coordinator ran it**, and its findings and the owner's answers are
[reconciliation.md](reconciliation.md) §4a. **This section is written to those answers**: each
paragraph below that an answer changed says so. **No task in
[implementation-plan.md](implementation-plan.md) starts before it has been read and its findings
answered** — which, for this section, is done; what remains is the re-verification §6 of the
reconciliation names. It is written as the questions a reader should be able to answer from it.

**Who can call the front door.** Everyone on the network (D920, **amended 2026-11-04** — finding
M-e). There is no network gate, no VPN, no allowlist of sources and no mutual TLS; the platform's IAM
is off by design, and the handler is the only gate. That is what "standard clients connect without
custom config" costs, and four things pay for it: every unauthenticated path is a metadata document
or an authorization endpoint with no work behind it, **the service serves no route that is not the
MCP endpoint or one of those six**, every refusal is recorded, and nothing behind the gate is
reachable without a token. **The amendment is that a public service is public per route**: with
`allUsers` on the service, the coordinator's own P8 tools are as reachable as `list_tasks`, so
**every tool maps to a scope and a tool with no mapping is refused** (§2, §5), `max-instances` and
the request body size are capped, and **an unauthenticated refusal is logged and never written to the
database**. **If this premise is
wrong — if the endpoint is not in fact public because the platform refuses the request before it
reaches the handler — the design loses nothing; the GCP live checklist is where that is confirmed,
and the owner owes that check.**

**What a stolen access token reaches.** Exactly the tools its scopes name, on the project's own
repositories, within its rigor window and its spend limit, until it expires. It does not reach the
project's database (only the service account does), it does not reach another project (one
coordinator per project, one database per project), it does not reach a model credential or a forge
credential (there is none to reach), and it cannot mint another token: the token endpoint needs the
refresh token or the client secret, and neither is derivable from an access token. Its blast radius
is therefore bounded by §6's limits, which is why the limits are mandatory on an unattended token.

**What a stolen refresh token reaches.** A new access token, until somebody revokes it — bounded by
the same limits. Rotation is what turns the theft into an event: the next time the legitimate holder
presents the rotated-away token, the family is revoked and the theft is visible in the audit record
rather than silent.

**What a stolen Firebase ID token reaches.** One thing: **the approve step's `POST /oauth/authorize`**
— the single endpoint that consumes one (the owner's answer of 2026-11-04, amended the same day by
M-a; an earlier draft had it at `POST /oauth/token`) — and only while it is unexpired and only if its
issuer and audience match the project. **The last of the bounds this paragraph used to state is built
at one door and not at the other**: an identity has to be one the project's own lists admit, and
**M5's approve step still mints a code and a family for any verified Firebase address** — resolving
an identity through the launcher and operator lists is **M7** (`ee/coordinator/mcpauth/identity.go`,
`TestIdentityOffTheListsIsRefused`, `TestOnlyExactUserEntriesAreRead`), which reads them on every
request to the MCP endpoint — **once something supplies them: the reading of the installation's
published state they live in is not built, and §11 carries it as an open item** — so a verified
Firebase address still gets a token family and **that
token reaches no tool**: an identity on neither list is refused before the request body is read, and
the lists bound what a token buys rather than who may obtain one. It is
never a bearer for the MCP endpoint and it is never a grant at the token endpoint, which is the whole
reason the coordinator mints its own tokens, and it is why the design will not accept a Firebase
token as an access token
"just for convenience".

**What the token's own expiry does not bound is what it buys.** The approval it answers mints a
refresh family, and a refresh token's own expiry is the zero time
(`ee/coordinator/mcpauth/store.go:271`): what bounds a family is **its own two lifetimes**, which
are built (`ee/coordinator/mcpauth/store.go:220`) — an absolute one from the mint, however busy the
family is, and an idle one on a family nothing has extended, both read at the rotation, which is the
only place a family is used. **And the ID token is spent**: the approve step records it as used, in
the store and as the sha256 of its value (D922), before the code is minted, so **a replay of the same
token is refused instead of approving a second flow**. Both were M-a's second and fourth
requirements, and both are built.

**What the device half adds to them is the first requirement, and it too is built.** M5's approve
step rendered the flow's number on the page and took it back from the same browser that answered,
which bound the page to the flow and nothing to a paired device, so a page that rendered a flow of
its own handed over the number and the token together and this server could not tell that approval
from the one M-a asks for. **The owner answered it on 2026-11-07: the device half is M5b, a task of
its own and a hard prerequisite before anything wires the front door**
([implementation-plan.md](implementation-plan.md) §3; [reconciliation.md](reconciliation.md) §4a),
**and it is built with the other two, as far as the server can build it**: the approval is the paired
device's own POST — the flow's id, the number the person read off the screen and the ID token that
device's session produced — and the page's answer carries the binding and the decision and no
credential, so a flow no device approved mints no code. **What is not built is the delivery**:
nothing renders a QR of the flow's binding and no launcher reads the page, so the act the endpoint
takes is one no client produces yet and no task of this plan carries the half that would (§11).

**The coordinator mints no Firebase token of its own** either: it holds no
signer role, so a compromised coordinator cannot answer "this person signed in" for somebody who
did not — that is the paired device's act (§3), and the coordinator only verifies what the device's
session produced.

**The paired device is an ACCEPTED residual risk, and this is where it is stated.** Finding **H1**,
and **the one amendment that is not the recommendation** ([reconciliation.md](reconciliation.md)
§4a): a device token is a **custom token** (`internal/budget/token/device.go:176` `MintDevice`; uid
`m~…`, claim `mu` = uid), and **every launcher and operator holds `signJwt` on the project's signer**
(`deploy/terraform/gcp/modules/firebase/iam.tf:35-41`) and can read `config/admin/devices`
(`:44-50`). **So a launcher can mint a device token and approve a sign-in as an operator's device,
and the coordinator — which only verifies what a device's session produced — cannot tell.** The
repository's own rules comment says the same thing from the other side: the `mu` claim "binds
nothing against a minter" (`internal/budget/rules/rules.go:482-487`). **The consequences are stated
rather than softened:**

- a launcher reaches **operator scope** at the front door — the audit record and the read tools a
  launcher does not otherwise get;
- **audit attribution can be forged**: the record is evidence of what a device *did*, not proof of
  who was behind it;
- a launcher can obtain a **rotating refresh family that outlives the ID token's hour**, so the
  exposure does not end when the sign-in token expires.

The review's recommendation — a **device-held signing key registered at pairing** — is **not taken,
not built and in no task**. **The change that would close it** is named in §11 and in
[reconciliation.md](reconciliation.md) §4a, so the deferral is priced rather than forgotten.
**This is a real limitation of the pairing the repository already has, not one this design
introduces**: any service that trusts a device token is trusting whoever can mint one.

**And the approval is hardened against what a phone cannot see** (finding M-a). §8's "loopback is
the attacker's machine" argument **fails when the approval happens on a phone**: the person is
approving a request they cannot tie to the machine in front of them. So §3 requires **QR proximity or
number matching**, shows **the client marked unverified and the redirect host**, takes approval as
**a single-use POST on a named endpoint** with **an ID token never in a GET URL**, and **pushes
nothing that a person did not start**.

**Token storage.** Only hashes, in a separate subtree of the project's database — **`mcpauth`** —
that is **not readable by any client and writable by no client** (D922, answered 2026-11-04 — **the
recommendation**, **amended the same day** — finding H3; D935). **The amendment is that the rules
are not the perimeter, and the paragraph that used to say "no reader at all" was wrong about IAM.**
Every launcher and operator holds `roles/firebasedatabase.viewer`
(`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`), admins and budget admins hold
`roles/firebasedatabase.admin` (`:53-58`), and the history job holds it too (`:81-84`) — so a node
the rules close is **still readable by every launcher** and writable by several accounts, and a
public service holding `firebasedatabase.admin` would hold it over the **whole** database. What the
design says now, and what §6 states in full:

- **The records do not live in the main database at all** (**D935**): they live in the store whose
  IAM names exactly one account — **the bucket `fugaro-mcpauth-<gcp-project>`, whose one member is
  the front door's own `mcp_server`** — which is what the bucket buys over the other candidates.
  **The `mcpauth` node stays named and denied as a tripwire**, so an MCP record written back into the
  main database is a failing test rather than a quiet widening.
- **The records are keyed and marked**: **HMAC-keyed hashes with a pepper in Secret Manager**, and a
  **MAC over each record's identity, limits and revocation state**, so a reader cannot reverse a
  hash and a writer the rules do not bind cannot forge a record. **Both are designed and not
  built**: the store hashes with a plain unkeyed `sha256` (`ee/coordinator/mcpauth/store.go:494`)
  and writes no MAC, which §11 and [reconciliation.md](reconciliation.md) §4a carry as a finding
  rather than this paragraph reading as a control the tree performs.
- **The public service holds viewer and not admin**, which is the part of the finding a *grant*
  fixes rather than a store.
- **The values are shown once, at mint, and never again** — there is no "show me my token again"
  path, because that path would need the value at rest — so "store only hashes" is the **second**
  line of defence and not the only one.

The rule is still written as **`.read: false` beside `.write: false`** rather than as a grant, and
the coordinator's own account still holds `roles/firebasedatabase.admin`
(`deploy/terraform/gcp/modules/firebase/iam.tf:138`) for the main database it does write. §6 states
the rule; [reconciliation.md](reconciliation.md) §4 records the answer, the earlier one it replaced,
and the amendment.
**One half is UNVERIFIED** ([reconciliation.md](reconciliation.md) §6, check 8): whether GCS still
attaches its convenience bindings to a Terraform-created bucket, so that a basic project Viewer reads
the store until M4 strips or refuses those members.

**Redirect-URI abuse.** The registration endpoint is open, so an attacker can register a client with
a redirect URI it controls — and gains nothing, because a code is delivered to that URI only after a
person signs in and consents, and a person only consents to a client they meant to use. Four things
carry that sentence, and the first three are the owner's answers of 2026-11-04:

- **Registration refuses a non-loopback URI that is not on the allowlist** (D928). An attacker's
  redirect URI is therefore refused before any person is asked anything about it, unless it is a
  loopback address — and a loopback address is the attacker's own machine, which is not where the
  victim's browser is. **The amendment of 2026-11-04 (finding M-a) is that this argument holds only
  while the browser is the thing running the client**: when the approval happens on a **phone**, the
  person cannot see which machine registered the loopback URI, and the "attacker's own machine"
  sentence stops meaning anything. That is why the approval is hardened as §3 describes — **QR
  proximity or number matching**, the client shown **unverified**, **a single-use POST** and nothing
  pushed unstarted — and why the loopback allowance stays: the fix is at the approval, not at the
  registration rule.
- **The consent page names the redirect host** (D928), so the person sees where their code is going
  rather than a client id. Consenting to a name they were never shown is not consent.
- **The flow is CSRF-bound** (D928): the authorize request carries the client's `state`, the
  coordinator holds its own binding between the request and the consent it answers, a consent
  without that binding is refused, and the `state` returns on the redirect unaltered. Without it an
  attacker's client can complete a sign-in the person began for another client.
- **The defences on the code itself** are unchanged and are the last line: exact-match validation of
  the redirect URI at authorize and at token, PKCE S256 required with no plain fallback, a
  single-use code bound to the client and the redirect URI, and a short code lifetime. A custom
  scheme is not accepted; a wildcard or a prefix match is not accepted; a redirect URI is never
  matched case-insensitively or after normalisation that could collapse two different hosts.

**Open Dynamic Client Registration.** Openness is a real property with a real cost: the registration
endpoint is reachable by anyone and writes a record, so it is rate-limited and audited, and a
registration is worthless until a person consents (§3). The residual risk the design accepts is
record growth by an unauthenticated caller, bounded by the rate limit and by registration records
being expirable; the risk it does not accept is a registration that grants a token by itself.
**Neither bound is built in M5** (§3, "The rate limit and the expiry are not built"), and no task of
the plan carries either: until a limiter and a sweep land, the growth this paragraph calls
accepted-by-bounds is accepted unbounded, and a registration has no lifetime at all — nothing yet
bounds how many of them a stranger may leave behind.

**An agent caller.** The hardest case, and the reason §7 refuses a token without limits. An agent
cannot be asked to notice that a tool result looks wrong, and it will happily follow an instruction
that arrives inside a task's own text — a prompt, a branch name, a pull request body. So the surface
is built so that following one gains nothing: the tools return data and never instructions (§5), no
answer carries a credential (§9), the limits are enforced by the coordinator rather than by the
caller's good behaviour, and an agent's token cannot be widened by anything the agent says. The
remaining honest statement is that a compromised agent with `tasks:write` can spend its own token's
limit and start runs, on the repositories its token admits, at the review rigor its token allows.
That is what the limit is for.

**Spend.** **`spawn_task` is out of the first slice (D936, finding H2), so the first slice spends
nothing** — `get_task_status`, `stream_logs`, `get_result` and `list_tasks` read. When it is built,
it is the tool with the least slack: the token's limit, the repository's cap, the installation's cap
and the run's own ceiling all apply, and the gateway is the enforcer, exactly as it is for every
other run. A token cannot raise any of them, and a refusal names which one it hit. **The amendment
of 2026-11-04 (finding M-b) is that the family's counter has to be *reserved*, not read**: two runs
started from one family would otherwise each read the same remaining figure and each be allowed it,
so the reservation is atomic against the family counter with a generation-matched write at spawn and is settled at
finalize (§6). Without it the tightest control in the design holds for one run at a time and not
across concurrent ones.

**A recorded transcript is not a safe tool answer.** The read tools draw on a run's own recorded
artefacts (D932), and one of those — `session/<id>.jsonl` (`internal/runner/session.go:185`) — **can
hold a credential the agent printed**: a log line, an echoed environment, a command it ran. Serving
it raw would make a tool that must never return a credential (§9) into the one that does. **The
amendment of 2026-11-04 (finding M-g)**: `stream_logs` and `get_result` serve the **progress record
redacted through `internal/safetext`** (`internal/safetext/safetext.go:17`) and never the raw
transcript file, so a token that reached a run's output does not reach an MCP caller through this
door.

**Injection into the coordinator.** The front door parses two untrusted formats on every call — a
JSON-RPC body and a bearer token — and it does so in the interview with a stranger. The design's
rules are that a token's content is never echoed, that a tool argument is never concatenated into a
command or a path without validation against the project's own lists, and that the run identifier a
caller supplies is resolved through the project's own run list rather than used as a key.

**A compromised coordinator, and the launch override.** **The first slice does not launch**, which is
part of what the deferral of `spawn_task` (D936, finding H2) buys: the service that answers strangers
holds no launch grant, no signer and no write on the runs bucket, so this whole paragraph is about
**the later slice** rather than about what ships first. When the coordinator is the launcher (D931),
it holds the one thing a launcher has that no token does: **the ability to set a run's environment
for one execution**. On Cloud Run that is `run.jobs.runWithOverrides`, which is in the launcher role
(`internal/cli/doctor_launcher.go:246`) and is why the repository already refuses to let a launch
decide anything important: `internal/backend/gcp/names.go:175` records that **a launcher may override
any variable through `run.jobs.runWithOverrides`, so the provider — which decides which repository
the runner may clone and which git credential the job holds — must come from something only Cloud
Run sets**, and `internal/backend/launchenv.go:91` (`LaunchEnv`) is where the seam refuses an entry
naming a reserved variable at all, with the platform's five (`PlatformEnvNames`, `:32`, whose
reservation `ReservedEnvNames` at `:52` hands both backends) pinned so a platform variable cannot be
silently rewritten. **The owner's requirement of 2026-11-04 is that the runner ignores the override environment except for an
allowlist, and that fence is NOT BUILT.** Today `internal/runner` reads its ceiling and wiring from plain
environment variables that `runWithOverrides` can rewrite (`FUGARO_MAX_RUN_USD`, `FUGARO_BUDGET_MODE`
and `FUGARO_ALLOWED_MODELS` in `internal/runner/spend.go`, `FUGARO_CREDENTIALS_SERVICE_URL` in
`internal/runner/gateway.go`, `FUGARO_GIT_PROVIDER` and `FUGARO_CHECKOUT` in `provision.go`), and
`LaunchEnv` refuses a reserved name only for a caller that goes through the Go seam: a compromised
coordinator calls the Cloud Run API directly. The container sees one merged environment, so an env-only
runner cannot tell a template value from an override value. The intended allowlist is exactly the
variables this design adds (the token's ceiling of D924 and the marker that says a launch carries one),
and the requirement is that nothing else a caller could put there is read. Until a mechanism lets the
runner read its non-allowlisted configuration from something an override cannot set, **a compromised
coordinator can change what a run does, not only narrow its ceiling**, and the privilege the owner is
asked to grant is that of a launcher with an unfenced environment. Building the mechanism is a gate on
M9 and D931, and which mechanism is an owner decision (§11).

**And the workspaces work is the second surface an approval has to agree across** — §10 states the
dependency and the identity mismatch, and it is the reason the accepted risk above is not only this
folder's problem.

## 9. What the coordinator must never do

This is the rule the brief names and the one place it is written down as a prohibition rather than a
description. The coordinator:

- **never hands a model or forge credential to an MCP caller.** Not as a tool result, not in an
  error, not in a log line the caller can read, and not for a run it started on the caller's behalf.
  A run's credential continues to reach the run the only way it reaches any run: the platform mounts
  it into the job, and the job's own service account is the only reader. **This is why a read tool
  never serves a raw transcript** (finding M-g, §8): `session/<id>.jsonl` can hold a credential the
  agent printed, so the answer is the progress record redacted through `internal/safetext`
  (`internal/safetext/safetext.go:17`) and never the file.
- **never exposes the database's contents as an MCP tool.** The coordinator's own §7 already says
  "[l]ocal agents never touch Firestore directly"; the front door is a caller, so the rule is that
  the node layout stays private and a tool answers with a view, never with a path.
- **never launches anything but the project's own job.** No arbitrary command, no arbitrary image,
  no repository outside the project's list (D931).
- **never treats a token limit as advice.** A limit that cannot be enforced is not offered.

## 10. Two other works this design is wired to

**The console's graph write.** The reconciliation was asked to check the MCP front door against the
console's dependency graph, and
the finding is that the graph's **write** route is still blocked: the console's own plan has a
roadmap write command waiting behind "the front door (coordinator P8, not started) or a new
people-write grant on a roadmap node". This design **does not unblock it** (D927, answered as
recommended on 2026-11-04: `tasks:read` and `tasks:write` only, and `roadmap:write` is not in v1).
The front door it
builds is authenticated as a person, so a `roadmap:write` scope is *possible* here — but a scope is
a wire contract, the console's own write design does not exist yet, and the roadmap's shape is
exactly what that design has to decide. Adding the scope now would fix a name and a permission
before the thing it protects is designed. The console's plan is the document that should come back
and ask for it.

**The workspaces exchange** (`docs/workspaces`, **amended in a parallel run** — **this folder does
not edit it**). The amended design requires a **member-bound assertion** and an **exchange at
`/oauth/authorize` that keeps the paired-device approval**, so a workspace exchange and this front
door's authorize step meet at the **same endpoint**, and the two must not disagree about **what
approving a sign-in is**. The mismatch is one sentence: **a workspace identity is a verified email,
while this front door's identity is a device uid read back to an email** — the same fact finding H1
is about, which is why the two amendments have to be read together and why the accepted risk in §8 is
not only this folder's problem. **The dependency runs one way** — this design adapts to the
workspaces decision, not the reverse — and **it is read here rather than assumed**: that folder's
amendment is in this tree (`docs/workspaces/spec.md:209-210`, `:217-219`), and it agrees with §3 and
§8 of this file about what an approval is
([reconciliation.md](reconciliation.md) §4a, the workspaces dependency).

## 11. What this design leaves open

- **`spawn_task`, and the whole write half of the front door** (**D936**, the owner's answer of
  2026-11-04 to finding H2). The tool is **deferred out of the first slice** — which is the
  authorization server and the read tools — because it needs three privileges the coordinator does
  not have and contradicts D923's "the coordinator mints nothing". **What a later design item has to
  answer** is written in [reconciliation.md](reconciliation.md) §4b and summarised in §5: no
  `runWithOverrides`, per-run input under a bucket prefix only the coordinator writes, run-token
  minting moved out of the public service, and an argument allowlist. **Nothing in the current plan
  builds it**, and the launch grant M9 asks for is **out of the first slice** with it.
- **The change that would close H1's accepted residual risk** (§3, §8): **a device-held signing key,
  registered at pairing, signing `{request id, client, redirect host, code_challenge, expiry}`; the
  authorize step admitting only `m~` tokens with a live device node and refusing `r~`/`p~`; and a
  paired device carrying a viewer role that reaches `tasks:read` only.** It is **named and not
  built**, because the owner chose to document the risk rather than fix it in this slice; the
  decision to build it is a later one, and it is the only thing that makes the approval mean what §3
  step 5 says it means.
- **Where the token records' store actually lands** (**D935**): the bucket
  `fugaro-mcpauth-<gcp-project>`, answered by the owner on 2026-11-05 — and **one half of it is
  UNVERIFIED** (§6, [reconciliation.md](reconciliation.md) §6 check 8): whether GCS still attaches
  its convenience bindings to a Terraform-created bucket, so that a basic project Viewer reads the
  store until M4 strips or refuses those members.
- **The keyed hashes and the MACs the store is specified to write** (§6, §8 — the answer to finding
  H3): **designed and not built.** The store hashes with a plain unkeyed `sha256`
  (`ee/coordinator/mcpauth/store.go:494`) and writes no MAC over a record's identity, limits or
  revocation state, so a reader who is not the store's own member reads an identity in the clear and
  a writer the rules do not bind is not refused by a mark. The gap is **M2's own row**, which merged
  on `main` before this branch, and it is carried in [reconciliation.md](reconciliation.md) §4a as a
  finding rather than closed by an edit here. **What it costs is bounded by the value it hashes, and
  that bound holds only where the value is one nobody can guess**: a token record's key is the hash
  of `NewValue`'s 32 random bytes (`ee/coordinator/mcpauth/store.go:502`), which has no weak space to
  walk, so there the missing key buys a reader nothing practical. **It does not hold for the audit
  record** (M10): that one is keyed by the same unkeyed hash of a `user:<email>` entry
  (`ee/coordinator/mcpauth/audit_record.go:164`), and an address is guessable — so a reader who can
  list the store, the project Viewer the bullet above leaves reading it until M4's row lands, can
  walk a dictionary of addresses and **confirm which identities have called here**, the object's own
  `identity` field then naming the person outright. That amendment's keyed hash is specified over
  the **token** records, so the pepper held in Secret Manager has to be put under **this** key too
  when it lands, or the one walkable hash in the store is the one the amendment's own answer does
  not cover. What is wrong either way until the keyed hash and the MAC land is a security
  document promising an integrity control the code does not perform.
- **Who owns the two bounds the open registration endpoint is accepted under** (§3, §8): the
  **per-source rate limit** and the **sweep that makes a registration, a pending authorize request
  and a code expire**. **Neither is built**, M10's `store.go` row is revocation and nothing else, and
  **no task of the plan carries them** ([reconciliation.md](reconciliation.md) §4a, "The two bounds
  no task carries"). **What is owed is the owner's assignment** — them inside M10's entry, or a task
  of their own — and **until it comes the bound is the wiring task's**: the task that mounts this
  package on a binary carries both bounds and deletes `TestNoBinaryLinksTheFrontDoorYet`, so the
  growth §8 accepts is accepted only while nothing mounts the routes. **M5b was owed beside them**
  (the owner's ruling of 2026-11-07) **and is built**, and it closed the half of M-a's device
  requirement a server can close: the guard is relaxed by building the paired-device approval rather
  than by deleting its message. **What it left is the other half, and it is owed now**: **the
  delivery of a flow to the paired device** — no QR of the flow's binding is rendered and no launcher
  reads the page — **which no task of this plan carries**, and which the wiring task therefore
  carries beside the two bounds and the guard. Until it lands, the act the endpoint accepts is one
  no client produces: the approval is decided exactly as M5b built it, and a person who reads the
  number off the page has nothing yet to type it into.
- **Where the front door's own two lists are read from** (§4, D933, the amendment's finding M-f):
  **the design says the installation's published state, and the tree reads nothing there.** §8's
  claim that the lists bound what a token buys turns on it: `ee/coordinator/mcpauth/identity.go`
  holds the `Roster` interface the door resolves an identity through and a server with none set
  refuses every token (fail-closed, `WithRoster`), but **no implementation of that interface over the
  published state is in the tree and no task of this plan carries one** — so what is built is the
  rule the reading must keep (the lists as the source rather than a machine's config, and exact
  `user:` entries only) rather than the reading. **The door is dormant with the rest of the front
  door (D936)**, so no local file is read either, and **the wiring task that mounts this package on a
  binary owes this reading beside the two bounds above**; until it lands, a
  front door wired up without one admits nobody rather than everybody, which is the direction the
  gap fails in ([reconciliation.md](reconciliation.md) §4a, D933's amendment).
- **The four read tools M8 builds are registered on no binary** (D936): nothing outside this
  package's own tests calls `RegisterReadTools` or constructs `NewProjectRuns`, so
  `get_task_status`, `stream_logs`, `get_result` and `list_tasks` are reachable by no client while
  the door is dormant, and `TestNoBinaryLinksTheFrontDoorYet` still holds. **The wiring task that mounts this package on a
  binary owes this beside the two bounds above and M7's reading of the
  published state**, and it is where the project's own repository slugs come from: the
  `FUGARO_COORDINATOR_REPOS` the installation already puts on the front door's own service
  (`deploy/terraform/gcp/modules/installation/coordinator.tf:491`), with a token's own repository
  filter applied on top of it rather than instead of it.
  [reconciliation.md](reconciliation.md) §4a carries what M8 still owes.
- **The library's citations, and the version they were read at.** The review read `zitadel/oidc`
  3.51.17 **outside this container**, so §3's constraint layer rests on findings this folder could
  not re-check; M5 re-reads them against the version it pins.
- **What the workspaces exchange and this front door agree an approval is** (§10), and the **identity
  mismatch** between a workspace's verified email and this front door's device uid: **the amended
  workspaces text is in this tree and read here** (`docs/workspaces/spec.md:209-210`, `:217-219`), so
  what is open is the **implementation** agreement the two designs' tasks have to keep rather than
  the text ([reconciliation.md](reconciliation.md) §4a, the workspaces dependency).
- **The override fence is unbuilt and its mechanism is undecided** (§8): how the runner reads non-allowlisted configuration from something `runWithOverrides` cannot set. It gates M9 and D931; the owner decides the mechanism before either is built.
- The three open items of the owner's spec are answered or narrowed: the library is **zitadel/oidc**
  (§3, D921, answered 2026-11-04 with its licence **UNCONFIRMED** and cleared by the owner on
  2026-11-07, so **§6's first check is met**: [licence-gate.md](licence-gate.md) is the record, and
  counsel was never asked, the clearance of the transitive set being the owner's own); the
  standalone-server option is not taken; and
  re-verification against the live MCP specification and the project's Identity Platform features is
  an owner check before implementation ([reconciliation.md](reconciliation.md) §6). **The
  re-verification could not be done here**: the public specification is not reachable from this
  container, which is the limitation `docs/coordinator/reconciliation.md:794` recorded as U1, so the
  design stays written to the **2026-07-28** revision the owner named.
- **Which of the seventeen decisions the owner answers differently later.** Fifteen were answered
  on 2026-11-04 and the design is written to those answers — **every one of them the recommendation
  at the time** — and **two more, D935 and D936, carry the security review's findings**: D935 is the
  store the owner answered on 2026-11-05, and D936 defers `spawn_task` out of the first slice. **The
  one amendment that is not the recommendation is H1's** (D923), accepted and documented rather than
  fixed. Every alternative stays priced in the
  reconciliation, so a later change of mind is a priced edit
  rather than a rewrite, except for D920 and D923, where it is a different document. **D922 is the
  one decision that was answered twice**: the first answer was read, written into §6 and §8 and
  restated the same day as the recommendation, and what stands here is the restatement **plus the
  amendment of the same day** (finding H3, [reconciliation.md](reconciliation.md) §4a).
- **Who owns the unattended caller, now that the mechanism is off** (finding M-f, §7): the shape is
  decided provisionally — one client per agent, since the identity in the audit record is the point
  — but **`client_credentials` stays off until it has an owner**, because an unattended client is not
  a `user:` entry and D933 therefore refuses it. The plan carries it as an owner check, and **M5 and
  M7 build the refusal and not the grant**.
- **Where the coordinator's own state tool group lives.** The **stateless-orchestrator spec** (open
  as **#1682**, not on `main` when this was written) gives the coordinator a **state tool group**,
  and **this MCP server is what hosts it**: D931 already serves the coordinator's own tools on the
  same endpoint under their existing names, so the state group reaches MCP clients through the front
  door rather than through a second surface. Which tool names that group carries, and whether any of
  them needs a scope of its own (D927 keeps v1 to two), is that design's to say — this folder's job
  is to state that it has a home here, so a reader of either document does not have to guess whether
  the two are connected. If #1682 lands a tool the two scopes do not cover, the scope question comes
  back as D927's.
- **The client-credentials caller's own ergonomics** — how a script stores a client secret, and what
  `fugaro` command (if any) mints one — is left to the implementation tasks rather than settled here,
  because §7 fixes the shape and not the surface.
- **What a `domain:` entry would need before the front door could read it.** D923's answer refuses
  `domain:`, `group:` and `serviceAccount:` entries in v1, which is the same refusal `fugaro user`
  makes at its own door. Whether a later version wants to admit a whole domain, and what would have
  to be true of the signer's role before it safely could, is **not** a question this design answers —
  it is named here so that the refusal reads as a decision rather than an omission.
