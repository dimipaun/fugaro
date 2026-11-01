# The hosted MCP server: the spec

**Status: design only.** Nothing here is approved and no line of it is implemented. The owner's spec
of 2026-10-24 is §0, copied **verbatim**; the design that follows (§1 onward) is written to the
**recommended** answer of each decision in [reconciliation.md](reconciliation.md) §4 (D920–D934), and
**where the two disagree the reconciliation wins** — §0 is the record of what was asked, not of what
this design proposes. Implementation is **not** approved: the owner decides, task by task, after
[implementation-plan.md](implementation-plan.md) has been reviewed.

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
the one part of it that departs from both the owner's spec and the coordinator's P8, and §8 is the
threat model that has to hold if the departure is approved.

The service is `ee/coordinator`'s, on the licence boundary the repository already keeps: the
coordinator is the subscription half of the product. This design adds **no** licence notice to any
file and no licence text anywhere, because the folder it is written in is core documentation and the
ruling that binds every 0.15.0 task says so.

## 2. The front door: transport, endpoint, and who may reach it

**Transport.** Streamable HTTP, one endpoint, no protocol-level session: the 2026-07-28 revision of
the MCP authorization specification removed them, so every request carries its own bearer token and
the server holds no per-connection state. That is what lets the service scale to zero, which is what
the coordinator's own §7 already required ("plain HTTP requests mean scale-to-zero is no problem").

**The endpoint is public, and the bearer token is the gate** (D920). No token, no reachable path that
does anything. What answers a caller who has not yet got one is the 401 that starts the flow, the
two metadata documents the flow needs, and the authorization server's own four endpoints — all of
them work nobody is charged for and none of them reaches the project's state.

This is a **departure from the coordinator's P8**, which chose Cloud Run IAM invoker as the
authorization for the front door — a deliberate departure from the MCP specification recorded there
as **Q16 A / U1**. P8's choice predates this spec, and it cannot survive contact with it: an MCP
client on a laptop or on a phone is a standard MCP client, and no standard MCP client can mint a
Google ID token for the project, nor is it reasonable to ask a person to install `gcloud` and hold
`roles/run.invoker` before a chat client may list their own tasks. The reconciliation records the
cost of each side; the owner decides.

**The front door is off until the owner turns it on** (D934). An installation that never opted into
the coordinator has no service for anything to reach; one that did gets the endpoint **off** until a
person turns it on, and a request to a front door that is off is refused before the handler reads a
token. A project that asked for the coordinator's read-only view does not thereby acquire a public
write endpoint.

**What the service answers without a token**, and nothing else:

- `/.well-known/oauth-protected-resource` — RFC 9728, naming the resource and pointing at the
  coordinator's own authorization server.
- `/.well-known/oauth-authorization-server` — the authorization server's metadata.
- `POST /oauth/register` — Dynamic Client Registration (RFC 7591).
- `GET /oauth/authorize`, `POST /oauth/token`, `POST /oauth/revoke`.

**Every other request** is the MCP endpoint, and it is the resource server: it reads the bearer
token, validates it (D920), and only then looks at the JSON-RPC body.

**The metadata document's shape.** A Firebase ID token's audience is the bare project id rather than
a URL, which is the gotcha the owner's spec names. The design answers it exactly as the spec says it
should: the coordinator issues its own tokens and never passes a Firebase token through as a bearer.
A Firebase ID token is accepted at **one** endpoint, `POST /oauth/token`, as the proof of the
sign-in step, and the access token the coordinator mints in exchange carries a real resource
audience — the coordinator's own URL — so a client's standard check works.

## 3. The authorization server

**Library** (D921). The authorization server is built on **zitadel/oidc** — Apache 2.0, certified by
the OpenID Foundation, the first option the owner listed — with the coordinator supplying two things
the library does not: the storage over the project's database, and Dynamic Client Registration,
which the owner's spec already expects to be a few hundred hand-written lines. The alternatives and
their costs are in the reconciliation; the short version is that ory/fosite brings the same missing
piece and a heavier plugin API for a flow this small, and a standalone third-party server is a
second resident service in a self-hosted product whose whole point is one service per project, on a
licence the owner has not confirmed.

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
  coordinator**. The owner's spec names a chat client's hosted callbacks as the example of the
  second kind; this design keeps the example out of the code, because a vendor's callback URL baked
  into a self-hosted product is a URL that goes stale in somebody else's release cycle.

**The flow, end to end** (the owner's §"Auth flow", with the two places this design changes it):

1. A client calls the MCP endpoint with no token; it gets 401 and a `WWW-Authenticate` header naming
   the resource metadata URL.
2. It reads `/.well-known/oauth-protected-resource` and finds the authorization server.
3. It registers (RFC 7591), or uses a client id a project operator pre-registered for an unattended
   caller (§7).
4. Authorization code + **PKCE with S256, required and never optional**. The code is single-use,
   short-lived, and bound to the client, the redirect URI and the code challenge.
5. The user signs in. **This design does not redirect to a Firebase-hosted sign-in page** (D923):
   the project's Identity Platform is initialized with no sign-in providers and refuses any public
   sign-up path, so there is no hosted page to redirect to, and turning one on is the exact change
   `docs/mobile-auth` ruled against in D221. The authorize step hands the person to the sign-in
   Fugaro already has, the custom-token flow, and verifies the resulting Firebase ID token the way
   `internal/credsvc` already verifies one.
6. The coordinator mints a short-lived access token and a refresh token, scoped to the MCP resource
   URL, carrying the identity, the client, the scopes and the limits of §6.
7. Every subsequent call is validated on its own: signature, issuer, audience, expiry, revocation,
   scopes, and the tool's own limit check.

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
leaned on, so this design puts the project's own two lists in its place (D933). That is the one place
the design adds an authorization rule the coordinator's spec does not have, and it adds it because
D920 otherwise leaves a public endpoint with no rule at all.

## 5. The tools

The owner's six tools, with the shape each one is allowed to take. Two rules apply to all of them and
are enforced in one place, ahead of every handler:

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
| `submit_task` | `tasks:write` | Starts a run on a repository with a prompt, a recipe and a rigor mode (D931) | Must not widen the run's ceiling, must not pass a model credential, must not launch anything but the project's own job |
| `get_task_status` | `tasks:read` | The run's state, from the run record | Must not read the runs bucket outside the project's own prefix |
| `stream_logs` | `tasks:read` | The run's recorded progress (D932) | Must not stream the project's whole log |
| `get_result` | `tasks:read` | The outcome, the diff or the pull request link | Must not return a body from the forge that carries a token |
| `cancel_task` | `tasks:write` | Writes the cancel marker `fugaro cancel` writes | Must not stop a job the project does not own |
| `list_tasks` | `tasks:read` | Recent runs of the project, with filters | Must not list another project's runs |

The "candidates for later" the owner names — list recipes, list repos, promote a task's rigor — are
**not** in v1 and carry no scope (D927). A scope that exists is a scope a token can hold, and a
token that holds one cannot be narrowed without breaking it.

## 6. Tokens: identity, limits, and what is stored

**What an access token carries.** The identity it acts for (a person, or an unattended client), the
client it was issued to, the scopes, its expiry, and the limits below. The access token is
short-lived and is validated on every request; there is no session behind it, which is what the
2026-07-28 revision requires.

**Rigor limits** (D925, D926). A token carries an optional minimum rigor mode, an optional maximum
rigor mode, and a **separate Hotfix flag, off unless a person turned it on**. The window is read
against the four on-axis modes; the Hotfix flag gates Hotfix by itself. That split is not a
convenience: `internal/rigor` deliberately ranks Hotfix **0** — below every on-axis mode — precisely
so that a Hotfix rule can never win a comparison against a Pro or Critical one. Folding Hotfix into
the window would make `min: pro` admit a Hotfix request, which is the opposite of what the owner's
"separate opt-in flag, off by default" is for. A request outside the token's limits is refused with
an error **naming the limit** — the mode the token allows and the mode that was asked for — because
the owner's spec asks for exactly that and because an agent that cannot see which limit it hit will
retry the same request.

**The spend limit narrows the run's ceiling** (D924). The token's spend limit is never sent to the
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

**What is stored, and where** (D922). Only **hashes** — never a token value, never a refresh token,
never a client secret. The record lives in a subtree **no client of the project's database may
read**: the coordinator's own subtree is readable by every signed-in person of the project, which is
correct for the coordinator's shared view and wrong for token records, client registrations and the
audit trail. So the design takes a separate top-level subtree with reads and writes refused to
everyone but the coordinator's own service account. A database read — or a project operator who
holds the database admin grant — therefore yields no usable token, and the one moment a value is
readable is the moment it is minted.

**Rotation and revocation** (D930). The refresh token rotates on every use, and a replayed refresh
token revokes the whole family: rotation is what makes a theft detectable rather than merely
survivable. Revocation is a first-class endpoint, and it is also what an operator does in the console
or from the CLI; either way it is one delete, and the next call with the revoked token is refused.

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

This section is the one the owner's brief requires an independent review of, and the review is
mandatory. It is written as the questions a reader should be able to answer from it.

**Who can call the front door.** Everyone on the network (D920). There is no network gate, no VPN,
no allowlist of sources and no mutual TLS; the platform's IAM is off by design, and the handler is
the only gate. That is what "standard clients connect without custom config" costs, and three things
pay for it: every unauthenticated path is a metadata document or an authorization endpoint with no
work behind it, every refusal is recorded, and nothing behind the gate is reachable without a token. **If this premise is
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

**What a stolen Firebase ID token reaches.** One thing: `POST /oauth/token`, and only while it is
unexpired and only if its issuer and audience match the project. It is never a bearer for the MCP
endpoint, which is the whole reason the coordinator mints its own tokens, and it is why the design
will not accept a Firebase token as an access token "just for convenience".

**Token storage.** Only hashes, in a subtree no client of the database may read (D922). A reader of
the database gets a list of clients and a list of when tokens were issued and revoked; it gets no
value it can present. The values are shown once, at mint, and never again — there is no "show me my
token again" path, because that path would need the value at rest.

**Redirect-URI abuse.** The registration endpoint is open, so an attacker can register a client with
a redirect URI it controls — and gains nothing, because a code is delivered to that URI only after a
person signs in and consents, and a person only consents to a client they meant to use. The defences
that matter are on the code itself: exact-match validation of the redirect URI at authorize and at
token, PKCE S256 required with no plain fallback, a single-use code bound to the client and the
redirect URI, and a short code lifetime. A custom scheme is not accepted; a wildcard or a
prefix match is not accepted; a redirect URI is never matched case-insensitively or after
normalisation that could collapse two different hosts.

**Open Dynamic Client Registration.** Openness is a real property with a real cost: the registration
endpoint is reachable by anyone and writes a record, so it is rate-limited and audited, and a
registration is worthless until a person consents (§3). The residual risk the design accepts is
record growth by an unauthenticated caller, bounded by the rate limit and by registration records
being expirable; the risk it does not accept is a registration that grants a token by itself.

**An agent caller.** The hardest case, and the reason §7 refuses a token without limits. An agent
cannot be asked to notice that a tool result looks wrong, and it will happily follow an instruction
that arrives inside a task's own text — a prompt, a branch name, a pull request body. So the surface
is built so that following one gains nothing: the tools return data and never instructions (§5), no
answer carries a credential (§9), the limits are enforced by the coordinator rather than by the
caller's good behaviour, and an agent's token cannot be widened by anything the agent says. The
remaining honest statement is that a compromised agent with `tasks:write` can spend its own token's
limit and start runs, on the repositories its token admits, at the review rigor its token allows.
That is what the limit is for.

**Spend.** `submit_task` costs money, so it is the tool with the least slack: the token's limit, the
repository's cap, the installation's cap and the run's own ceiling all apply, and the gateway is the
enforcer, exactly as it is for every other run. A token cannot raise any of them, and a refusal
names which one it hit.

**Injection into the coordinator.** The front door parses two untrusted formats on every call — a
JSON-RPC body and a bearer token — and it does so in the interview with a stranger. The design's
rules are that a token's content is never echoed, that a tool argument is never concatenated into a
command or a path without validation against the project's own lists, and that the run identifier a
caller supplies is resolved through the project's own run list rather than used as a key.

## 9. What the coordinator must never do

This is the rule the brief names and the one place it is written down as a prohibition rather than a
description. The coordinator:

- **never hands a model or forge credential to an MCP caller.** Not as a tool result, not in an
  error, not in a log line the caller can read, and not for a run it started on the caller's behalf.
  A run's credential continues to reach the run the only way it reaches any run: the platform mounts
  it into the job, and the job's own service account is the only reader.
- **never exposes the database's contents as an MCP tool.** The coordinator's own §7 already says
  "[l]ocal agents never touch Firestore directly"; the front door is a caller, so the rule is that
  the node layout stays private and a tool answers with a view, never with a path.
- **never launches anything but the project's own job.** No arbitrary command, no arbitrary image,
  no repository outside the project's list (D931).
- **never treats a token limit as advice.** A limit that cannot be enforced is not offered.

## 10. The console's graph write

The reconciliation was asked to check the MCP front door against the console's dependency graph, and
the finding is that the graph's **write** route is still blocked: the console's own plan has a
roadmap write command waiting behind "the front door (coordinator P8, not started) or a new
people-write grant on a roadmap node". This design **does not unblock it** (D927). The front door it
builds is authenticated as a person, so a `roadmap:write` scope is *possible* here — but a scope is
a wire contract, the console's own write design does not exist yet, and the roadmap's shape is
exactly what that design has to decide. Adding the scope now would fix a name and a permission
before the thing it protects is designed. The console's plan is the document that should come back
and ask for it.

## 11. What this design leaves open

- The three open items of the owner's spec are answered or narrowed: the library is §3's
  recommendation (D921), the licence question is closed by not taking that option, and
  re-verification against the live MCP specification and the project's Identity Platform features is
  an owner check before implementation, listed in [reconciliation.md](reconciliation.md) §6.
- **Which of the fifteen decisions the owner declines.** The design is written to the recommended
  answer everywhere, and every alternative is priced in the reconciliation, so a different answer is
  a different document rather than an edit.
- **The exact client-credentials shape for unattended callers** — one client per agent, or one per
  workspace — is a product question this design answers provisionally (one per agent, since the
  identity in the audit record is the point) and the plan carries as an owner check.
