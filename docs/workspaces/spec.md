# Workspaces: the spec

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** §0 is the owner's spec as this run recorded it; the design that follows (§1 onward) is
written to the answer of each decision in [reconciliation.md](reconciliation.md) §4 (**D1290–D1305**,
the block this design was renumbered into on 2026-11-04), and **where the two disagree the
reconciliation wins** — §0 is the record of what was asked, not of what this design proposes.
**The owner answered all sixteen decisions on 2026-11-04, every one as recommended**, so every
sentence below is the design the owner answered and not a recommendation awaiting one. That is still
**not an approval**: no task is approved, the owner approves each task by name, and §8's independent
SECURITY review gates the merge.

**This file was amended on 2026-11-04, after the design merged.** The design landed as **#1492**,
and the mandatory independent SECURITY review of §8 ran afterwards. It returned **seven findings**;
the owner answered all seven on 2026-11-04, and the answers are applied here. Two of them amend a
decision, and both are marked where they bite: **D1299** (the advisory headroom is no longer written
into a member's database, and the service holds **no write** on a member at all — §5) and **D1301**
(the cap is metered at the proxy, and the rollup becomes a reconciliation alarm — §5).
[reconciliation.md](reconciliation.md) §4 carries both amended answers and §8 records them as
answered; [README.md](README.md)'s answers table shows them. **D1301's amendment is not the option
the reconciliation had recommended**, and it is the one deviation this folder records.

**Every decision this file cites is answered, on 2026-11-04, and each as recommended except D1301's
amendment.** A parenthetical like "(D1293)" below is therefore a **settled** decision and not an open
one; [README.md](README.md) tabulates the sixteen with a one-line why for each, and
[reconciliation.md](reconciliation.md) §4 carries the owner's answer beside the options and the cost
of the alternative. The design's three load-bearing answers are stated where they bite: §3 (D1290,
D1293, D1294, D1295), §4 (D1297, D1298) and §5 (D1299, D1300).

Every repository, person and workspace this folder's own text names is a placeholder: `owner/repo-a`,
`owner/repo-b`, `person-1`, `workspace-alpha` (and not `workspace-a`, which is already a Bitbucket
workspace name in this repository's own examples — [README.md](README.md) says why). No file here
contains a credential, a key, a token, a price or anything shaped like one.
[README.md](README.md) says what in §0 is the owner's own words and what was not recovered.

Two rules bind everything below and are not up for a decision:

1. **A workspace never widens a member.** Every member keeps its own policy, its own caps, its own
   allow-lists and its own launcher/operator lists, and nothing a workspace holds can make a call
   possible in a member that the member's own configuration forbids. A workspace *groups*; it never
   grants. §9 states it as a prohibition.
2. **No agent handles a credential.** The workspace's services never hand a model credential or a
   forge credential to a run, to a CLI, or to a person — not as a tool result, not in an error, not
   in a log line. A run's credential reaches the run the way it reaches every run today: the
   platform mounts it into the job, or the credentials service proxies the call and never discloses
   the key. §9 states this too.

## 0. The owner's spec

**The verbatim text was not recovered.** What follows is the recorded summary of the owner's spec of
2026-10-24, exactly as this run received it; it is what this folder reconciles and designs against,
and [README.md](README.md) records what was and was not recovered. The owner approved a design PR
for this work on 2026-10-31 and **answered the sixteen decisions on 2026-11-04**, every one as
recommended ([README.md](README.md) tabulates them).

- **Workspaces:** an optional grouping of Fugaro projects.
- **Single sign-on** via a workspace Firebase token exchange.
- **Shared credentials** across the workspace's projects.
- **A workspace-wide cap** (spend).

The original's worked example named a real organisation. It is `workspace-alpha` everywhere in this
folder, and the projects in it are `owner/repo-a` and `owner/repo-b`. This design states no currency
figure of any kind: a cap is a number the owner writes into a config, and §5 says why the design
carries the *shape* of the number and not a number.

## 1. What a workspace is, and the rule this serves

A **workspace** is an optional, named grouping of Fugaro projects that share three things and
nothing else:

- **one identity** — the people who may reach the group, signed in once at the workspace's own
  Firebase and exchanged per member (§3);
- **one pool of model credentials** — the workspace's credentials, held by the workspace's
  credentials service and served to every member's runs through a per-credential allow-list (§4);
- **one spend cap** — the ceiling on what the workspace's own credentials pay for, enforced where
  those calls are paid for (§5).

It is **optional**: a project with no workspace is exactly what a project is today, and nothing in
this design changes its behaviour, its config, its database or its rules. A workspace is a
**grouping over projects that already exist**, not a new kind of project: it owns no repositories,
no runs bucket, no job, no image and no policy.

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). So:

- The `workspace` commands **edit local files and read** (D1303). None of them writes the workspace
  node, a member's database, a secret or anything else in the cloud.
- Every change they make reaches the cloud through the next `fugaro update`, which already
  re-converges the installation and, with the policy stage the update-converge work adds, publishes
  the policy (D1292). Each write command ends by printing that one command and nothing else.
- A change `fugaro update` cannot carry is a gap in `update`, not a step for the user (F13).

## 2. The workspace record

A workspace is declared in **two** places, and the second is derived from the first:

1. **In each member's own project config** (`~/.config/fugaro/projects/<name>.yaml`,
   `internal/localcfg.Config`): the membership key `workspace: <name>` (D1291), and beside it the
   member's **pin** — the workspace's Firebase project id, **the issuer the member's own config pins**
   for the workspace's signer endpoint, **the workspace's assertion-signing key** the member verifies
   a member-bound assertion against, and the workspace service account the member's own `fugaro update`
   binds §5's grant to.
   The membership is a scalar; the pin is the member's own record of *which* workspace it trusts,
   *which* key speaks for it there, and *which* identity acts for it there. It is the member's and
   never the workspace's (W1 writes it, W4, W5 and W7 read it): a record that could supply the issuer,
   the key or the service account would be a record that could aim a member's exchange and a member's
   grant at a party of its choosing, which is the direction rule 1 does not survive. The membership
   key is read by every command that reads a project config, and it is what makes a project a member.
   A project with no key is not a member of anything, which is the state of every installation today.
2. **In the workspace's own database**, at `workspaces/<name>`, published by the workspace owner's
   `fugaro update`: the member list (each member's project name and its canonical `owner/repo` set)
   and the workspace's cap (D1292). **It holds no identity provider, no key and no service account**
   — all are the member's pin, so which identity a member trusts, and by what key, is never something
   the workspace states about a member. **It is read by the workspace's own service and by the
   workspace owner's CLI, and by nothing else.** No member reads it: a member's machine holds no grant
   on the workspace's project at all (§5), and the member's exchange verifies the pin **in its own
   config** and reads **its own** launcher/operator lists (§3). The record is the workspace's own
   statement about the group; it is not an input any member trusts. **It also does not say how to
   reach a member** — the job accounts, the Firebase project and `fp` and the member's exchange URL
   are the service's own per-member table, from the workspace owner's config (§5), and the record
   only names the members.

The workspace name reuses the project-name grammar (`config.ProjectNameRE`, F21) rather than a second
one. A workspace's record is validated strictly and strictly decoded, like every other config and
wire format here: an unknown field is an error, and the shape has a JSON Schema in `schemas/`.

**Membership is at most one workspace per project** (D1291). The key is a scalar, not a list: the cap
and the credential pool both need a unique owner for a project, and "which of N workspaces' headroom
applies" has no answer a reader can act on.

## 3. Single sign-on: the workspace identity and the token exchange

**The workspace has its own Firebase project**, created for the workspace and holding nothing else
(D1290, **answered 2026-11-04**: a designated member's Firebase would silently make that member's
launcher and operator lists the workspace's admission lists). It is the workspace's identity
provider, and it is not any member's: a member's own Firebase continues to issue that member's own
tokens, unchanged.

**A member's Firebase refuses public sign-up, and the workspace's is the one that does not.** An
earlier draft of this section said the workspace's sign-in was "the same Firebase sign-in methods the
member projects already support (Google, email)"; **that was false, and the security review of
2026-11-04 found it.** A member's Identity Platform is initialized with **no sign-in provider at
all** (`internal/infra/identityplatform.go`:103), and a configuration that has one is refused, because
the web API key is public and only custom tokens signed by the member's own signer may work
(`refusePublicSignUp`, `internal/cli/init_firebase.go`:720). The workspace's own Firebase is the
opposite **by design**: it is the one Firebase in this design where a person signs in with a real
provider, so the providers are enabled on it — an owner-run step beside `fugaro setup`, which the
owner settled on 2026-11-04 and [implementation-plan.md](implementation-plan.md) W3 owns — and the
guarded statement is not "no provider" but **which** providers, the pinned set below.

**What a person does.** They sign in to the workspace's Firebase once, and what they hold afterwards
is a **short-lived workspace token** — a Firebase ID token of the workspace's project. It is a bearer
for exactly one thing: **the workspace's own signer endpoint**, which is the only place that token is
ever presented. The endpoint verifies it and mints the **member-bound assertion** a member accepts
(§3, *the member-bound assertion*).

**The workspace token is a Firebase ID token, and three facts about those are load-bearing here.**

1. **Its audience is the bare project id**, not a URL (`docs/mcp-server/spec.md` "Firebase
   specifics"), so it verifies against the workspace's project at the workspace's signer endpoint and
   nowhere else. D1293's answer is *one sign-in at the workspace and one exchange per member*, and
   what makes its second half true is that **no member accepts a workspace Firebase token at all**:
   the assertion below is the only value a member verifies, and it is bound to that one member. The
   containment is still the **member's own lists** — the member refuses unless its launcher/operator
   lists name the person — so a stolen token, and the assertion it can be turned into, reach only the
   members the person was already admitted to.
2. **A caller must present a verified address.** `email_verified` is required to be true, and a
   claim that is absent or false is a refusal — exactly the rule every class of caller to the
   credentials service already carries (`internal/credsvc/auth.go`:1018, `checkCallerToken`). An
   unverified address is an address nobody proved they own, and admitting one would let anybody who
   can register it with any identity provider the workspace's Firebase trusts become a listed
   person.
3. **A Firebase refresh token does not rotate and Firebase does not detect a replay.** The MCP
   design's D930 — rotating refresh tokens, a replayed one revoking the whole family — is a property
   of **the MCP server's own** tokens, which that design mints because a Firebase token's audience
   fails its resource checks. It does **not** apply here: the workspace's sign-in is Firebase's, and
   Firebase's refresh tokens are neither rotating nor replay-detecting. The workspace's theft
   detection is therefore the token lifetime and the member's own lists, not rotation, and §8 says so
   rather than borrowing a property this design does not have.

**The workspace token is accepted nowhere but the workspace's own signer endpoint**, which refuses a
token whose signature is not **Google's published Firebase signing keys** (`GoogleKeysURL`,
`internal/credsvc/auth.go`:1371-1373 — one global set, the same document for every Firebase project),
whose `aud` is not the workspace's own project id, whose `iss` is not that project's securetoken
issuer (`:1072`), whose `email_verified` is not true (fact 2), or whose **`firebase.sign_in_provider`
is not one of the pinned providers** — the next paragraph. That endpoint is the workspace's, deployed
in the workspace's project, and **no member verifies a Firebase token at all**.

**The provider pin** (finding 7 of the security review of 2026-11-04). The workspace's signer
endpoint — and the member, which re-checks the same claim in the assertion — accepts only a **pinned
set of `firebase.sign_in_provider` values**: the providers the workspace's Firebase actually carries,
`google.com` and `password`. It **refuses `custom` and `anonymous`**. The reason is the finding:
anyone who can mint a custom token for the workspace's project — `signBlob` or `TokenCreator` on any
service account in it — can set `email` and `email_verified` to whatever they like, and an anonymous
sign-in proves no address at all, so a provider set that admitted either would make the whole
admission chain a decoration. A pinned list is what turns "the workspace's Firebase said so" into a
statement a member can hold (§8, residual risk 6). The set is configuration, and a provider the
workspace's project does not carry is a provider the exchange never sees.

**The member-bound assertion** (finding 1 of the security review, D1293 and D1294 standing as
answered). The signer endpoint's answer is a **short-lived JWT**, and it is the only value a member
accepts from the workspace:

- **its `aud` is that one member's exchange URL** — the member's own `/oauth/authorize`, a value the
  workspace owner declares for the member in the per-member table (§5) — so an assertion minted for
  `owner/repo-a` is refused at `owner/repo-b` for the audience alone, whatever else it carries;
- **its `jti` is single-use**: the member records every `jti` it has consumed, for the assertion's
  lifetime, in a subtree no client of the member's database may read (D1296's rule — hashes only),
  and a second presentation of the same assertion at the same member is refused;
- it carries the person's address, the provider that signed them in, and the workspace's project —
  and no member token, no scope and no credential.

**The exchange happens at the member's `/oauth/authorize`, as the sign-in proof** (finding 2 of the
security review). That is the same place the MCP design accepts a Firebase ID token and for the same
reason — `docs/mcp-server/spec.md` §3 says a Firebase token "is the proof of the *sign-in* step, which
is the authorize step, and it is never a grant at `POST /oauth/token`" — so this design adds **no
token-exchange grant at `/oauth/token`**, and it does not invent a second endpoint. Everything else
about that flow is the MCP design's and unchanged: **PKCE with S256, required and never optional**
(its D923), the **consent page** bound to the flow, and the **paired-device approval** of the member's
own `fugaro pair` (D923; `docs/mobile-auth`'s D221 rules out a hosted provider sign-in at a member,
which is exactly why the member's Firebase carries no provider). The member's authorization server —
the one the MCP design puts inside the coordinator (`docs/mcp-server/spec.md` §3; that design is
**on `main`** now, merged as #1487 on 2026-11-04, though none of its code is written) — then:

1. verifies the **assertion**'s signature against **the workspace key the member pins in its own
   config** (§2, W1), and its `aud` against **its own exchange URL**, its expiry, and its `jti`
   against the ones it has consumed. All of those values are the *member's* pin and never values read
   from the workspace's record: §2's record carries none of them, so a workspace cannot move a
   member's trust by publishing a different one;
2. re-checks the provider the assertion names against the pinned set above, and requires the address
   it names to be a verified one (fact 2): the member does not take the signer endpoint's word for
   either, because the assertion is what it holds;
3. maps the identity to the member's own permission lists (`internal/cli/user.go`: the launchers and
   operators `fugaro user` writes), and **refuses when the member's lists do not name the person**;
4. issues the authorization code, and the exchange at the member's own `/oauth/token` mints **the
   member's own token**, short-lived, with the member's own scopes and limits, exactly as it mints one
   for a sign-in that started at the member (D1295).

**The member stays the authority over what the identity may do there** (D1294's answer, unchanged),
and the assertion is what makes that true at the wire rather than only in prose: it is bound to one
member and to one use, so the workspace's signature is not a bearer a member honours twice or
elsewhere. The workspace mints nothing a member's API accepts.

**Nothing is shared but the sign-in.** No session is shared, no member accepts another member's
token as a bearer, and the workspace token is never a bearer for a member's API. A member that is
down does not stop a person reaching another member; a member that is removed from the workspace
stops being reachable through it, and its own sign-in keeps working.

**Removal takes both sides, like membership itself, and the member's side is the one that bites.**
The exchange verifies the assertion against **the pin in the member's own config** — its key, its
project, its own exchange URL — and reads **the member's own** lists (§3 steps 1–3, §2), so the owner
removing the member from `workspaces/<name>` changes nothing at that member's exchange: a person
still reaches it with a fresh assertion, because the exchange never reads the record and the record
was never an input. What ends it is the **member's own `workspace rm`** (W1), which drops the
membership key and the pin, and then there is no pinned key and no pinned project left to verify
against and the exchange refuses. The honest statement, which the design
states rather than implies: **a workspace removal is complete only when both sides have run** — the
owner's record no longer lists the member and the member's own config no longer names the workspace —
and until then the two sides disagree, which is exactly what `workspace show`'s drift report names
(§6, W2). A workspace cannot drop a member unilaterally, and the design does not pretend otherwise.

**What is stored** (D1296). The workspace mints no *token* of its own — what it mints is the
assertion above, which is short-lived and member-bound — so it stores no token of its own: the sign-in
record is **Firebase's** (the workspace's Auth user list, which is what a Firebase identity provider
is), and the members' tokens are the **members'**, each in the member's own token record exactly as
today. The one new thing stored anywhere is the **member's list of consumed `jti` values**, in the
member's own database, for the assertion's lifetime. D1296's answer binds whatever a task does store —
**hashes only**, never a token value, in a subtree no client of the workspace's database may read (the
MCP design's D922 applied to the workspace, and to the member's own subtree here) — so the `jti`
values are held as hashes and nothing in this design stores a token value at all.

**Revocation is bounded by two short lifetimes, and that is stated rather than implied.** "One delete
at the workspace and the next exchange is refused" would hold only if the member read the workspace's
revocation state on each call, and it does not: the member verifies the assertion against the key and
the project it pins in its **own** config and against its own lists, and reads **no node of the
workspace's project** (§5 states the grants this design makes, and the reverse one is not among
them). So the honest statement is: a workspace revocation — disabling the person in the workspace's
Firebase — takes effect when the **workspace token** they hold expires (the workspace's signer
endpoint refuses to mint once the token is gone), and an assertion already minted stops being
accepted when **its own** lifetime ends; the member's own revocation is unchanged, independent, and
bounded the same way. Both windows are the length of a short token, which bounds the exposure rather
than closing it, and §8 carries that as a residual risk.

**What a workspace identity may do in a member is what the member already grants** (D1295,
**answered 2026-11-04**: option (A) — a workspace grants **nothing beyond the member's own launcher
and operator lists**, because the only-tighten rule would not survive a workspace role that maps to
a member role). The workspace cannot add a launcher, cannot make an operator, cannot admit an
address a member's lists do not name, and cannot widen a member's policy, caps or allow-lists. That
is rule 1 of this folder stated as a mechanism: the exchange reads the member's lists and never
writes them.

## 4. Shared credentials

**One workspace bundle, one workspace service** (D1297, **answered 2026-11-04** and **re-read
against main on that date**: N bundles would be N rotations, N leak sites and no single revocation —
and no one hop that sees the total, which the cap needs). The credentials service
(`docs/credentials-service/`) is deployed **once, in the workspace's project**, and holds the
workspace's model credentials in its one bundle secret — the same shape the owner ruled for an
installation (one Secret Manager secret, one `secretAccessor` grant). Every member's runs reach it
the way a run reaches the service today: the job's own service account presents a Google ID token,
the service verifies the signature and the `email` claim, and the run is proved by its budget
identity.

**It is not true that "the only thing that changes is the admitted set", and the design does not say
it.** The admitted set does change — it becomes the **workspace's** member job service accounts —
but the checks around it are pinned to one installation today, and they have to move with it:

- **The caller's member comes from the verified service account and from nowhere else.** The service
  already resolves a caller by looking the token's folded `email` up in its own list of the
  installation's job accounts (`internal/credsvc/auth.go`:727, in `Verify`), and it returns **the
  configured spelling** of the account it found (`:738`), never the claim's. That is the whole of
  what names the member. **A member name in a request body, a header or a query parameter is never
  identity** — a job's own launcher can write any header, so a member read off one would be a member
  a caller chooses.
- **The token's own pins have to admit the member.** `verifyRun` (`:1047`) refuses a token whose
  `aud` is not this service's Firebase project (`:1069`), whose `iss` is not that project's
  securetoken issuer (`:1072`), or whose `fp` is not this service's Fugaro project (`:1105`). A
  workspace-deployed service therefore has to accept **each member's** Firebase project as the issuer
  and **each member's** Fugaro project as `fp`, and hold the member it found to both: the account's
  member, the issuer's member and the `fp`'s member must be the **same** member, or the call is
  refused. That is a change to the check, not only to the list it looks the account up in, and W6
  owns it.
- **The member's database is derived from the verified `iss`, and never declared** (finding 6 of the
  security review of 2026-11-04). The service reads each member's state — the **policy** the
  intersection needs, the run **ledger** the reconciliation reads, a run's **outcome** nodes — and
  today it reads them from one database URL and one Firebase project held together by a check that
  they are the same installation's (`internal/cli/credentials_serve.go`:494: the database is
  `<Firebase project>-default-rtdb`, so "the URL must be the database of the Firebase project a run's
  identity is verified against"). **A workspace deployment must keep that rule per member rather than
  take a URL from its own config table.** An owner who declared member B's job accounts beside member
  A's database URL would otherwise pair B's callers with A's — possibly looser — policy, and the
  splice would be invisible: the account resolves, the tokens verify, and the policy that answers is
  another member's. So the database is **derived from the Firebase project the caller's verified
  `iss` names**, and a member whose declared values and verified `iss` disagree is refused before any
  read. `checkLive` (`internal/credsvc/auth.go`:1196) and its outcomes reader
  (`RTDBOutcomes`, `:217`) are **per member** and chosen by the member the call resolved to, not by a
  process-wide setting: one deployment serves N members, so one configured database is one member's
  state answering for all of them. W6 owns the derivation and the test.

**The person class is the one the service already has, and who fills it, and who is refused, is the
one thing this deployment fixes.** Main's credentials service admits a **person** as a class of its
own beside the job classes —
`PersonAccounts` (`internal/credsvc/auth.go`:375) with `VerifyPersonCaller` (`:968`) for the GitHub
read mint and `VerifyReadCaller` (`:890`) for the Bitbucket read mint, both taking a token a plain
`gcloud auth print-identity-token` may have minted (`GcloudAudience`, `:258`, the owner's ruling of
2026-11-04). **Whose people they are, and what a person may read, does change here.** The
workspace's person list is neither the installation's nor the **members'**: it is the **workspace
owner's** people, which the workspace's own `fugaro update` writes from the workspace's own config
the way an installation's list is written today. Every route of the workspace's service therefore
admits **no member's person** — none is a *person of one member* here, because the list that names a
member's people is that member's own launcher/operator list, the member's own file, which the
workspace's service holds no grant to read and no member's `update` publishes into the workspace
(§2, §5). An admission the service cannot check is not an admission. The consequence is stated
rather than left to be discovered: **the workspace's own figures are the workspace owner's to read**
— by that owner's installation, whose CLI holds a credential for the workspace's project (§2, W2),
and by the owner's people through this class, and by nobody else. One member's people are therefore
never handed another member's spend, and a member has no view of its own share of the pool through
the workspace's service at all: a member-readable share is a surface this design does not add, and
[reconciliation.md](reconciliation.md) F31 records that it is not one. A person is never a way to a
credential's value (rule 2), the class rule that a person may not also be a job
(`NewAuthenticator`) holds unchanged, and W6 states the refusals route by route as tests rather than
leaving the deployment one person list.

**Which credentials may be shared, and with whom** (D1298, **answered 2026-11-04** and **re-read
against main on that date**: a workspace credential available to every member would be a policy
bypass, so the intersection is the only shape that keeps rule 1). Every shared credential carries a
**per-project allow-list** — the pattern `allow_data_to` already uses for a model route — and the
effective set is the **intersection** of:

- the workspace credential's allow-list;
- the member's own policy, which may refuse a provider or a credential the workspace holds.

The member's policy is always the tighter side, because that is what "only tighten" means
(`internal/policy/merge.go`). A credential the workspace holds and the member's policy excludes is
**not served**, and the refusal names the member's own policy as the reason, so an operator is never
sent to the workspace for a refusal the member made.

**What a run sees.** Nothing new. The run's gateway is the loopback proxy it is today; it calls the
workspace's credentials service, which holds the key and proxies the call. No run, no CLI and no
person is ever handed a shared credential's value (rule 2).

**A member may keep its own credentials.** A project that holds model credentials of its own —
today's state for every installation — keeps them, and its runs keep using them. Sharing is
additive: the workspace's pool is a set a member *may* draw on, subject to the intersection above.

## 5. The workspace cap

**What it bounds** (D1300, **answered 2026-11-04**: calling it a bound on a member's total spend
would be false for a member that holds credentials of its own). The workspace cap bounds **the spend
of the workspace's own credentials**.
It is *not* a bound on a member's total spend, and the design refuses to call it one: a member may
hold credentials of its own whose calls never pass the workspace. For a member that routes *every*
model call through the workspace, the cap does bound that member's whole spend — but that is a
property of how the member is configured, not a property of the cap, and the console and the docs
must say so.

**Where it is enforced** (D1299, **answered 2026-11-04** and **amended 2026-11-04 after the security
review**: a member cannot be the fence over its own spend). At the **workspace's credentials
service**, on the calls it pays for. That is the one hop every shared-credential call passes, so a
member cannot bypass it by choosing a different gateway, and it is the only place that sees the total.

**The service meters each call it pays for, and that is the amendment** (D1301, **answered
2026-11-04 and amended the same day after the security review** — the amendment is **not the option
the reconciliation had recommended**, and it is the one deviation this folder records). The service
**reserves** before it makes a call and **settles** on the response's usage, **atomically in the
workspace's own database**: one transaction that takes the room, and one that records what the call
actually cost. So the workspace's figure is taken where the money is spent rather than reconstructed
afterwards, and the two failures the review named are gone with the old basis:

- **an old rollup can no longer fail open.** The rollup basis read the *last good* rollup, so a
  member's spend between two rollups was invisible and the cap could be passed in the gap; a
  reservation is taken **before** the call, so there is no gap to spend in.
- **a member's own image can no longer defeat the cross-check.** The old basis compared each member's
  ledger with its own runs bucket, and the member's operator controls both — the review's finding 5.
  What the service compares now is its **own** count against the member's ledger, and the member
  cannot write the service's side.

**The member's ledger stays authoritative for the member's own caps.** These are **two different
quantities, each governing its own cap**: the member's gateway keeps enforcing the member's own
budget from `runs/<slug>/<run>` under the rules that already constrain it (`NEW >= OLD`,
`spent <= reserved`, `internal/budget/rules/rules.json.tmpl`:60), and the service's meter governs the
**workspace's** cap. Neither is renamed into the other and neither replaces the other.

**The rollup becomes a reconciliation alarm** (D1301's amendment). It is not the basis any more; it
is a **check**. On the history job's schedule the service compares, per member, **its own count**
against the **member's ledger**, and a disagreement beyond a stated bound is **reported** — never
silently averaged, and never silently trusted in either direction. The alarm is what makes a member's
under-reporting visible rather than free: the cap no longer depends on the member's honesty, and the
member's ledger no longer decides the workspace's figure.

**The overshoot is bounded by the reservation, and the bound is stated.** The cap can be passed by at
most **the reservation in flight** — the amount taken for calls that have been made but not yet
settled — and the design says so rather than claiming an exact ceiling: a reservation is taken before
the call, so a run of calls that all settle at the reservation size is the worst case, and the bound
is a number the operator can read rather than an adjective. **A settle that fails refuses**: if the
usage cannot be written back, the call is refused rather than served, because a call whose cost the
service cannot record is spending against a cap it cannot see (the fail-closed rule below, applied to
the meter).

**The advisory headroom comes from the service, and the service holds no write on a member**
(finding 1's consequence for the cap, and the owner's answer of 2026-11-04, **O1 adopted**). Each
member's own gateway receives the workspace's remaining room so a run stops early rather than being
cut off mid-stage, and it receives it **from the workspace service itself** — as a response header on
every call the service already proxies, or from a small route that answers **only the calling
member's job service account** and no one else. What the gateway does **not** do is read a node of
the member's own database that the service wrote: **nothing is written into a member's database at
all.** An earlier draft put the headroom at a node in the member's database, which is why the service
held `instances.update` on every member; the review's finding 3 showed what that write reached, and
the owner answered it by dropping the write (below). The advisory number is a courtesy to the run,
never the enforcement.

**The service keeps one table of members, and every value in it comes from the workspace owner's
config.** To resolve a caller, to check its tokens and to mint a member-bound assertion (§3) the
service needs, per member, its **job service accounts**, its **Firebase project id and `fp`**, and
its **exchange URL** — and each is the workspace owner's own declaration, made once per member on the
owner's machine (`workspace member add`, W1) and converged by the owner's `fugaro update` (W2's stage
writes the record; W3's root of the workspace's own project and W6's service carry the table). **The
member's database URL is not in the table at all**: it is **derived from the Firebase project the
verified `iss` names**, so an owner cannot splice member B's accounts onto member A's database (§4,
finding 6). **None of it is in
`workspaces/<name>`**: §2's record names the members and carries the cap, and it carries no identity
provider and no service account, so the member's Firebase project is not something the record states
about a member any more than the issuer is. The two directions stay separate — the **member's own**
config supplies the pin (what the member trusts), and the **workspace owner's** config supplies the
table (how the workspace reaches each member) — and neither side's value is ever taken from the
other's.

**A member the table does not bind is refused at the front door.** The record can list a member the
table has no entry for, or an entry with a value missing — a member added to the record whose owner
has not yet declared how to reach it, or one whose project moved. Such a member can no longer be
resolved as a caller at all (its job account is not in the table), so its **shared-credential calls
are refused**, and the reconciliation of the paragraph above has nothing to compare for it — which it
**reports as an alarm** rather than recording a zero for it. **A `stale` member fails closed.** An
unbound member is an unreadable one — the service cannot resolve it, so it cannot read its ledger,
meter its spend, or compare anything against it — and the refusal names the member as unreadable; it
lifts when the owner's table binds it. That rule is the one the old `stale` marker carried, and it
survives the amendment, even though the marker's original job — recording a member whose *ledger*
could not be read, in place of a zero — does not, because the meter no longer reads the ledger to
enforce.

**What the service holds on a member, and what it does not.** Enforcement, the meter and the
reconciliation need exactly **one** thing on a member, and the design grants exactly that:

| On | Grant | What it reaches |
| --- | --- | --- |
| Each member's Firebase project | A **custom role holding exactly `firebasedatabase.instances.get`** — the read half of the shape `fugaroCredentialsDatabase` already uses (`deploy/terraform/gcp/modules/installation/credentials.tf`:242-246) | Whole-database **read**: the caller's policy for the intersection (§4), the run ledger the reconciliation reads, and the outcome nodes `checkLive` reads. It writes nothing |
| The workspace's own project | The bundle secret's `secretAccessor`, and nothing else | §4 |

**The service's second grant, on each member's runs bucket, is gone** (O1 adopted, 2026-11-04). It
existed as the rollup's second read — "what catches a member whose ledger and its bucket disagree" —
and the review found that check defeated (a member's operator controls both its ledger and its
bucket, finding 5). With the basis metered at the proxy the comparison that matters is the service's
own count against the member's ledger, which needs no bucket, so `roles/storage.objectViewer` on a
member's runs bucket buys nothing and is **not** bound. The service holds no read of a member's run
artifacts or logs at all.

**What the member's own `fugaro update` applies, and to which identity.** The member's **own**
`fugaro update` applies the one binding — the custom role on the member's Firebase project — and
binds it to the workspace service account **named in the member's own config**, beside the issuer and
the workspace key it pins (§2). It is the member that decides which account is handed access to its
own database, and it takes the account from no other place: §2's record names no service account (it
names no issuer either), so a record that could name one would be a record that could aim a member's
database access at a party of its choosing — the direction rule 1 does not survive. Nobody else can
create the binding: the member owns the project, and only its `fugaro update` reaches it. W7 owns the
file and the test.

**That permission reaches the whole database, and this design will not pretend otherwise.**
`credentials.tf`:201-210 is the authoritative statement: Realtime Database IAM has **no path-level
scope**, so a binding is per instance and reaches the whole database — `instances.get` is "read any
node of the instance", the only read permission there is. A condition cannot fence it either: RTDB
"cannot express a path prefix in IAM" (`credentials.tf`:251-253). So the grant is **whole-database
read on every member**, and "the ledger, the policy and the outcomes" is what the service's **code**
does with it.

**What that read reaches, stated node by node, and what the write would have reached** (finding 3 of
the security review). An earlier draft of this section said "the service holds no grant on a member's
**policy**", and **that was false**: `instances.get` reads every node, and the member's policy is at
`config/policy` in the member's own database (`internal/budget/rules/rules.json.tmpl`:20-23,
`internal/projectpolicy/paths.go`:20-21), which the role's own comment names in as many words —
`get` reads "the kill switches, the policy, a run's outcome node, and the credential state another
revision wrote" (`credentials.tf`:245). So the honest statement of the **read** is: the whole
database, which on a member is at least its `config/policy`, its `config/caps`, its run ledgers and
its outcome nodes. The honest statement of the **write** the design used to hold is what the owner
answered, and it is the reason O1 was adopted: a grant of `instances.update` would have reached
**every** node a whole-database write reaches, and on a member those are the ones that decide what
that member may do —

- `config/policy` — the member's own policy, and the node the shared credential's intersection reads
  (§4). A write here is a member's policy rewritten by another project.
- `config/caps` — including `config/caps/global`, the member's own ceiling, which is what the
  withdrawn claim **the workspace service's grant can lift a member's cap** was about. **This is the
  exposure O1 removed**, and §8's residual risk 5 is withdrawn with it.
- `config/kill` — the member's own stop switches, which a workspace reaching them is a workspace
  stopping a member's work (§9).
- `config/limits` — the member's limits, and the node the policy's own rules marker lives under
  (`internal/projectpolicy/paths.go`:19).
- `config/mode` — the member's mode.
- `config/admin/devices` — the member's paired devices, which is an authentication surface rather
  than a spend one.

**The narrow role is right, and `admin` would be worse than either.** `roles/firebasedatabase.admin`
carries `instances.list` and the instance **lifecycle** —
`create`, `delete`, `disable`, `reenable` and `undelete` (`credentials.tf`:215-219, and
`deploy/terraform/testdata/custom-role-permissions.txt` is the whole set a custom role may hold). A
component that holds every model credential must not be able to **delete or disable a member's
database**. The role this design binds carries one read permission and no lifecycle, and with O1's
answer it carries **no write**, so rule 1's write direction is now a property of **IAM** and not of
the service's code: `deploy/terraform/gcp/modules/firebase/iam.tf`:75 says of the history job's own
write to `/config` that "its code never writes /config, **which IAM can't enforce**" — here IAM does
enforce it, because there is no write permission to enforce against.

**The service holds no grant on a member's secrets, a member's job or a member's project IAM**, and
its read of a member's database is the whole-database read the table above states — which is why
§8's cross-project read is stated at its real size rather than denied. And **a member holds no grant
at all on the workspace's project and reads no node of its database** — which is why §3's revocation
is bounded by the token's lifetime rather than by a read of the workspace's state, and why §2's
record is read by the workspace's own service and the owner's CLI and by nobody else.

**Where the headroom lives was an owner decision, and the owner took it on 2026-11-04.** The design
had kept it in the **member's** database, which is why the service needed `instances.update` on every
member and why rule 1 was a property of the code. The alternative — one's own headroom read by one's
own gateway — was stated as a cost, because a member's gateway would need a read of the **workspace's**
database, that read is whole-database too, and every member would read the workspace's member list
and its rollup. **The owner answered that this was a false choice** — ([reconciliation.md](reconciliation.md) §8, **O1**, answered 2026-11-04): the headroom is **delivered by the workspace service on the calls it already answers**, so
neither side reads the other's database, the service's grant on a member drops to `instances.get`
alone, and §9's "never writes a member's database" is now a rule about **any** node and not about
one.

**The scope of the cap is stated, not implied.** `fugaro budget set --global` today means *the
project* (F10). The workspace's cap is a **fourth scope**, spelled `fugaro workspace cap set
--workspace <name>`, and the two are never spelled with the same word.

**The stop** (D1302). `fugaro budget kill` gains a **workspace scope**: one command stops every
member's runs. It is the same switch the credentials service's own stop flag already is
(`fugaro budget kill --all`, the operator's answer of 2026-10-19), so an operator under pressure has
one command to remember and not two. **Its scope is exactly this:** it stops the runs of every member
of the workspace, and it makes the workspace's credentials service refuse every shared-credential
call. It does **not** touch a member's own credentials, a member's own budget or caps, a member's own
sign-in, or a member's runs that never touch the workspace — a stop that reached any of those would
be a workspace reaching into a member, which §9 forbids.

**The two owner decisions this section reported were answered on 2026-11-04.** Metering the
workspace's spend at the proxy — the service counting each call it pays for instead of reading the
members' ledgers — was the other possible basis and is now **the** basis: the owner took it in the
answer recorded as **O2** ([reconciliation.md](reconciliation.md) §8), amending D1301 and the two
decisions that depend on it (D1299, D1300). And where the advisory headroom lives was recorded as
**O1**; the owner answered that it is delivered by the workspace service and written nowhere in a
member's database. Both are applied throughout this section above, and both are dated in
[README.md](README.md).

## 6. The command surface

`fugaro workspace` (D1303), five verbs:

| Verb | What it does |
| --- | --- |
| `workspace ls [--json]` | Offline. Every workspace this machine's project configs name, with the members it can see **from its own files** — the owner's machine, which holds the record it publishes, sees the whole list, and a member's sees the memberships its own configs name — and which of them this machine has a config for. Versioned JSON with a schema, like every other CLI document |
| `workspace show <name> [--json]` | Offline. One workspace from **this machine's own files**: the configs on it and the pin each carries, its cap as last published here, and the drift between what this machine holds and what it last knew the record to say. It reads no cloud node (§2: a member holds no grant on the workspace's project), so on a member it shows the member's own half and says so rather than printing an empty member list; the workspace owner's machine, which holds the record it publishes, shows the whole of it |
| `workspace add <name>` / `workspace rm <name>` | Edits the **selected local project config** and nothing else: the membership key `workspace: <name>` **and the pin beside it** — the values of §2, which are the one place a member decides what it trusts and which workspace key it verifies an assertion against — on, or off. Prints the diff, backs up the file, writes atomically, and ends by printing `next: fugaro update` |
| `workspace member add <name> <project>` / `member rm …` | The same, for the member list of a workspace this machine holds a config for — the owner's side of a membership. `member add` is also where the owner records the per-member values §5's service table is built from (the member's job accounts, its Firebase project and `fp`, and its exchange URL), because the owner is the side that declares them. **The member's database URL is not among them**: it is derived from the verified `iss` project (§4, §5), so that an owner cannot pair one member's accounts with another's database |
| `workspace cap set --workspace <name> --monthly <amount>` | Records the workspace's cap in the local config; `fugaro update` publishes it |

**`workspace show` reads files and never a cloud node**, and what "drift" means follows from that.
On the **workspace owner's** machine, which holds the record it publishes, drift is W2's two-sided
membership check in full: a project the record lists that does not name the workspace, and a project
that names the workspace and is not listed — both printed, neither half silently winning. On a
**member's** machine the record is not readable at all, so the drift it can report is exactly that:
the member's config names the workspace and carries its pin, and `workspace show` says the group's own
record cannot be read from here rather than printing an empty one or an invented one. A member's half
is the only half a member holds, and the command says so instead of guessing.

None of them writes the cloud. `workspace ls`, `workspace show`, the offline reads and the
`--json` documents run anywhere, including under a coding agent's environment; the writes are local
config edits and behave exactly as `fugaro models`' and `fugaro settings`' writes do — under a
coding agent's marker they print the diff and the command for the user to run, and write nothing
(`internal/cli/managed_gate.go`:181 `readManagedGate`, the one helper the cloud writes on main
already share: `config publish`, `project policy publish`, `image refresh` and `accounts add`).
`fugaro workspace` adds no new caller of the gate, because it has nothing to gate — its writes never
leave the machine. **Two writes** do go to the cloud, and both are stages of `fugaro update`, which
already reads the gate directly for its own stages (`internal/cli/update_managed.go`:217 calls
`readManagedGate`), so neither needs a path of its own: **W2's publish of `workspaces/<name>`**, on
the owner's side, which runs under the gate `update` already opens; and **W7's one grant on a
member** (the read-only database role; the write and the bucket read are gone — O1), which the gate
**refuses** in a managed project — an IAM binding is a loosening, and the
cover never admits one (`internal/infra/tf/cover.go`:860-867) — so that stage falls back to its own
typed confirmation and is run by the member's owner at their own terminal (W7). The distinction is
the whole reason the two are stated apart: a plan can be read for a document, and it cannot be read
for an access grant.

## 7. The console and the mobile app

Neither reads a workspace today, because neither reads a workspace's *inputs* today either: the
console reads `fugaro config list --json` (`internal/cli/config_list.go`), which lists the
installations this machine has, and the mobile app reads the runs and the budget of one project.

**No console or mobile task is in this plan** (D1304). A workspace is a group over the document the
console already reads, so a console screen is a small read of a *versioned* `fugaro workspace ls
--json` — and the design that adds it should be written after the CLI document exists, not before,
because a wire contract fixed before the shape it carries is the mistake the MCP design's §10
refuses for the roadmap scope. The mobile app reads nothing of this.

## 8. Threat model

**This section's independent SECURITY review has run, and this section is amended to its findings.**
The review is the one the owner's brief requires, **the coordinator runs it**, and it is not a task
of this folder's author or of the runs that implement anything. It ran **after the design merged as
#1492** and returned **seven findings**; the owner answered all seven on 2026-11-04, and every one is
applied here — the two that amend a decision (D1299, D1301) are marked in §5, and
[reconciliation.md](reconciliation.md) §2f F33 and §8 carry the findings and the answers.
[implementation-plan.md](implementation-plan.md) §1 still holds the tasks that add a surface this
section describes behind that review's findings having been read and answered. It is written as the
questions a reader should be able to answer from it.

**Who can reach what.** A workspace adds **four** reachable surfaces, and an earlier draft of this
section named only the first — which is the kind of omission a threat model cannot afford:

1. **The member's exchange**, on each member (D1294), where the member accepts the workspace's
   member-bound assertion as the sign-in proof at its **`/oauth/authorize`** (§3). It opens no
   member's API to anyone who is not already in that member's own lists, and the assertion is bound
   to that one member and single-use.
2. **The workspace's own signer endpoint**, in the workspace's project, reached with a **workspace
   Firebase token** (§3). It verifies that token and mints the assertion above. It is the only place
   a workspace token is ever presented, and the only surface the workspace itself adds to the
   identity half.
3. **The workspace's credentials service**, in the workspace's project, reached by every member's job
   service account with a Google ID token (§4), and — for the read mint, not for a credential — by a
   person of the **workspace owner's** people, which is the whole of the class it admits (§4: no
   route admits a member's person, so a member's people reach no figure of the workspace's). This is
   the surface that spends the workspace's money, and it is reachable by anything holding one of the
   member job service accounts.
4. **The workspace's own Firebase**, which is a public sign-in surface: anybody who can reach it may
   attempt to sign in, and Firebase decides whether they get a token. What that token is worth is the
   member's lists (§3), so the sign-in surface itself grants nothing — but it is a surface, and a
   threat model that did not name it would be describing a smaller system than the one being built.

It adds **no endpoint to a member's own API** — the member's `/oauth/authorize` and `/oauth/token`
are the MCP design's endpoints, and the assertion is one more proof the authorize step accepts rather
than a new route — and no endpoint to the workspace's project other than the signer endpoint and the
credentials service. The workspace's database holds the member list, the cap, the service's own
meter and whatever the reconciliation writes; its readers are the workspace's own service and the
workspace owner's CLI, **no member reads
it** (§2), and D1296's rule keeps whatever a task stores in its token subtree shut to every client of
the workspace's own database.

**What a stolen workspace token reaches.** One thing: **the workspace's own signer endpoint**, which
turns it into a member-bound assertion — and only for a member whose lists name the person, and only
while it is unexpired. It is never a bearer for a member's API and never for a member's exchange
directly, which is the whole reason the assertion exists. Its blast radius is therefore the set
of members the person was already admitted to — a theft that grants nothing the thief did not
already have, with one exception stated next. And the assertion is not a second theft: it is
**single-use** (`jti`, §3) and **bound to one member** (`aud`, §3), so a stolen assertion is worth one
sign-in at one member rather than a bearer a thief can replay.

**What a stolen workspace token reaches that the person did not.** Nothing about a member, and
nothing about the group: the **member list** is the one thing a signer endpoint could be built to
disclose, by answering "which members may I reach?" — and the design forbids it. Neither the token nor
the assertion carries **any member enumeration**: the client names the member it wants, and a member
the person is not in is refused with a refusal that is identical in shape and timing to one for a
member that does not exist. An endpoint that answered "which members are there?" would let any stolen
token map the group, and it would also be the one workspace-side reader a member's exchange could use
for the workspace's own record, which §2 says no member has.

**What a stolen member token reaches.** Unchanged from today: one member, its own scopes, its own
caps, its own policy, until it expires. The workspace does not extend it, cannot mint a sibling from
it, and cannot revoke it — revocation at a member stays the member's.

**Rotation and revocation.** **There is no rotation here, and the design does not claim one.** The
MCP design's D930 — a refresh token that rotates on every use, and a replayed one that revokes the
whole family — is a property of the tokens **that design mints**, and it is not a property of
Firebase's: a Firebase refresh token neither rotates nor detects a replay, and this design's
workspace token is Firebase's (§3, fact 3). A theft of the workspace token is therefore **not
detectable by rotation**; what bounds it is that the token is a bearer for the exchange and for
nothing else, that the exchange refuses anyone a member's lists do not name, and that the token is
short-lived.

**Revocation at the workspace ends the person's ability to *exchange*, and nothing else**, and it is
bounded by two short lifetimes rather than instantaneous: the member checks the assertion against the
workspace key and the project it pins in its **own** config and against its own lists, and reads
nothing of the workspace's state, because no member holds a grant on the workspace's project (§5). A
member token already minted keeps working until it expires, which is why member
tokens stay short-lived, and a workspace revocation takes effect when the **workspace token** the
person holds expires (the signer endpoint refuses to mint then) and an already-minted assertion stops
being accepted at the end of **its own** life. The honest statement is that **neither revocation is
instantaneous across members**, and the design buys both back with token lifetime rather than with a
callback into every member or a read of the workspace's revocation state on every exchange.

**Shared credentials: what a compromised member reaches.** A member's job service account can call
the workspace's credentials service, and therefore spend the workspace's money and use the
credentials its allow-list admits. It cannot read a credential's value (rule 2: the service proxies
and never discloses), and it cannot reach a credential another member's allow-list admits and its
own does not (D1298). The residual risk this design accepts, stated plainly: **a member's operator
who controls that member's job image can make the member's jobs call the workspace's service with
any request they like**, within the intersection above. That is the same trust the member's operator
already has over the member's own credentials today; grouping it means one member's compromise
reaches the *pool*, not just that member. It is why the allow-list is mandatory and why a shared
credential's allow-list should name the fewest members that need it.

**The workspace cap's integrity, after the amendment.** The cap is enforced at the service (D1299)
and **metered there too** (D1301 as amended, §5): the service reserves before each call it pays for
and settles on the response's usage, atomically in the workspace's own database. So the accounting no
longer rests on each member's own ledger — which a run writes under rules that forbid lowering it and
do nothing to force it to rise (F12) — and the two failures the review named are closed with it: an
**old rollup can no longer fail open** (there is no window between rollups to spend in, because the
room is taken before the call) and **a member's image can no longer defeat the cross-check** (what
the service compares is its own count against the member's ledger, not a member's ledger against a
member's bucket). What remains accepted, and is stated rather than hidden: the cap can be passed by
at most **the reservation in flight**, a **failed settle refuses** the call rather than serving it
unmetered, the **reconciliation alarm** reports a member whose ledger and the service's count disagree
beyond the stated bound instead of trusting either, and the hard stop is the workspace kill (D1302) —
one command, which does not depend on any member's honesty.

**Cross-project read.** The workspace's service **reads** N members' databases, and its grant is
**wider than the nodes it uses**. That is new in this repository: every client today is built on
one project's database (F20). The honest statement of what the service holds is §5's table, and the
honest statement of what that means is this:

- **IAM on a Realtime Database is whole-database, and it bypasses the rules.**
  `deploy/terraform/gcp/modules/firebase/iam.tf`:52 says of the
  admin grant that it "writes /config over IAM, which bypasses the rules", and
  `credentials.tf`:251-253 says why the shape cannot be narrowed: RTDB "cannot express a path prefix
  in IAM". So the custom role's `firebasedatabase.instances.get` reads **every node** of a member's
  database. "Only the ledger, the policy and the outcomes" is a property of the service's **code**,
  not of its grant, and no IAM policy enforces it.
- **A compromised workspace service therefore reads a member's whole database** — its policy, its
  budget and caps, its run records, its outcomes. It learns each member's *spend* **and a great deal
  more**, and §5 lists the nodes the read reaches by name.
- **It cannot write any of them**, and that is the amendment the owner took on 2026-11-04 (O1,
  §5): the role carries `instances.get` and no write permission, so a whole-database write is not a
  thing the service can do. `config/policy`, `config/caps/global`, `config/kill`, `config/limits`,
  `config/mode` and `config/admin/devices` are all out of its reach, and rule 1's "a workspace never
  widens a member" is now a property of **IAM** and not only of the service's code. §5's list of what
  a write would have reached is the finding that produced this answer, and residual risk 5 below is
  **withdrawn**.
- **What it also cannot do** is what the service's code and the members' own projects keep out of
  reach: it cannot read a credential's **value** (rule 2 — the bundle is in Secret Manager, not the
  database, and the service proxies calls), it cannot read a member's **secrets**, it cannot reach a
  member's **project IAM** or its job, and it cannot **delete or disable a member's database** — that
  needs the instance lifecycle, which the narrow role does not carry and `admin` does (§5).
- **The runs bucket is not read at all**: `roles/storage.objectViewer` on a member's bucket is not
  bound any more (§5). The cross-check it existed for was defeated — a member's operator controls
  both its ledger and its bucket (the review's finding 5) — so the second read went with the rollup
  basis it served, and the service holds no read of a member's run artifacts or logs.

This is the design's largest accepted exposure, it is stated rather than argued away, and it is
carried in the residual risks below. With O1's answer it is a **read** exposure and no longer a write
one. The mitigations that are real are the ones in §5: a custom role carrying one read permission and
no lifecycle, so the service cannot delete or disable a member's database; the metered cap, which no
longer rests on a member's ledger; and a reviewable, small service. A member's own database rules
are **not** a mitigation against this account: IAM bypasses them.

**Injection into the workspace service.** The service parses untrusted text on every call: a member
name, a run identifier, a bearer token. The rules are the ones the MCP design already states — a
token's content is never echoed, a member name is validated against the workspace's own record
rather than concatenated into a path, and a run identifier is resolved through the member's own run
list rather than used as a key.

**An unattended agent as a caller.** An agent's workspace token is admitted exactly as a person's, on
the same lists, and the exchange cannot widen it. The exchange's own limits are a member's, not the
workspace's: an agent that should only ever run in `owner/repo-a` is bounded by that member's own
limits and by nothing the workspace adds. The design therefore has no workspace-level token limit to
get wrong, which is deliberate — a second limit beside the MCP design's would be a second place a
ceiling is decided.

**What this design accepts, as residual risk.**

1. A member's operator can spend the workspace's pool within the intersection of the credential's
   allow-list and the member's policy. Mitigation: the allow-list, the meter (which the member cannot
   write), the reconciliation alarm, the kill. Not elimination.
2. A workspace revocation does not reach a member token already minted. Mitigation: short token
   lifetime, and the short lifetime of the assertion beside it.
3. The workspace's identity provider is a single point of *sign-in*: if it is down, no one signs in
   through the workspace. Mitigation: each member's own sign-in is untouched, so a member remains
   reachable the way it is today.
4. The workspace service is on the critical path of every shared-credential call. Mitigation: the
   advisory headroom lets a run stop early, and the runbook's stop flag is one command.
5. **Withdrawn on 2026-11-04** (O1, [reconciliation.md](reconciliation.md) §8). The risk was that a
   compromised workspace service could **write any node of any member's database**, including
   `config/caps/global`, so that rule 1 rested on the service's **code** rather than on IAM. The
   owner removed the grant: the service holds `firebasedatabase.instances.get` on a member and **no
   write permission at all** (§5), the headroom is delivered on the calls the service already answers,
   and nothing is written into a member's database. The read exposure that remains is the
   cross-project read stated above, and this number is kept rather than reused so a reader who meets
   "residual risk 5" in an earlier release's notes finds where it went.
6. **Anyone who can mint a custom token for the workspace's project can set `email` and
   `email_verified` to anything, and reach every member as anybody.** This is the finding-7
   correction, and it is stated at its real size: it is **not** only a "Firebase Auth administrator".
   Two permissions do it — **`signBlob`** (or `iam.serviceAccounts.signBlob`) and **`TokenCreator`**
   on **any** service account in the workspace's project — and the anonymous sign-in provider is
   another path to a token nobody's address proves. That is what any identity provider is, and it is
   not a widening this design introduces, but it is the trust the workspace's Firebase now carries
   for every member at once, and a member's own Firebase does not carry it for another member.
   Mitigation, and it is a real one: the workspace's signer endpoint and the member's exchange accept
   only the **pinned `firebase.sign_in_provider` set** — `google.com` and `password` — and **refuse
   `custom` and `anonymous`** (§3), so a minted custom token or an anonymous one is refused before
   any list is consulted; the member's lists still decide who is admitted; and a member that will not
   extend that trust to the workspace's project says which identity provider, key and issuer it
   trusts in **the pin §2 puts in its own config**, where the workspace's record carries none of them.
   (The exchange's `email_verified` requirement, §3 fact 2, is not a mitigation against this: setting
   that flag is exactly what a custom token can do. It is there because an unverified address is one
   nobody proved they own, and the provider pin is what answers this risk.)

## 9. What a workspace never does

This is rule 1 and rule 2 written as prohibitions. A workspace:

- **never widens a member.** It cannot add a launcher or an operator, admit an address a member's
  lists do not name, raise a member's cap, run a mode a member's policy forbids, or serve a
  credential a member's policy excludes. It cannot make a repository reachable in a member that the
  member's own policy excludes.
- **never hands out a credential.** No shared credential's value is returned to a run, a CLI, a
  person, an error message or a log line. The service proxies the call; the key stays with the
  service.
- **never owns a repository, a runs bucket, a job or an image.** A workspace is a grouping over
  projects that already exist; it holds no runs and launches nothing.
- **never writes a member's database** — **any** node of it (D1299 as amended on 2026-11-04: the
  advisory headroom is delivered by the workspace service on the calls it answers, so there is no
  headroom node to write and the service holds no write permission on a member at all).
- **never becomes a second permission model.** There is no workspace role that means something in a
  member; the member's own lists are the only thing that admits anyone.
- **never carries a workspace-wide repository allow-list.** The per-repository allow-list is the
  policy's and it is per project (F18); a workspace that widened it would be a policy bypass.

## 10. What this design leaves open

- **The exchange's exact HTTP shape** — the endpoints, the well-known documents and the error
  bodies — is the MCP design's §3 for a member, and this design reuses it rather than writing a
  second one. The assertion of §3 is accepted at that design's `/oauth/authorize` and adds **no** new
  endpoint. If the owner answers that design's D921 (the OAuth library) differently, this design
  moves with it.
- **#1487's authorization server.** The exchange endpoint of §3 *is* the MCP design's authorization
  server. That design **landed on `main` on 2026-11-04** (merged as #1487, `docs/mcp-server/`), so
  the interface this design was written to is now a settled document rather than an open pull
  request — but **none of its code is written**: its plan's tasks M1–M10 are all unbuilt, and its
  M5 is gated on D921's library licence being confirmed. This design's SSO half therefore still
  cannot be built until that work is, and [implementation-plan.md](implementation-plan.md) §2 and §5
  carry that as the dependency that forces the release slot. Nothing here duplicates it: this design
  adds a workspace Firebase, a workspace signer endpoint and a member-bound assertion, and reuses
  that server as the member's exchange.
- **The workspace's own upgrade path.** `fugaro update` converges a workspace's record, its service
  and its rules the way it converges an installation; the plan's W9 carries the release note's
  `### For operators` line, and the convergence test is that plan's, not this document's.

**What the security review of 2026-11-04 named and this design has not settled.** The review's seven
findings and the owner's answers to all of them are [reconciliation.md](reconciliation.md) §2f's
**F33**, and the two answers that amend a decision are that file's ([reconciliation.md](reconciliation.md) §8)
**O1** and **O2**. What follows is the part of the answer this document records as **open** rather
than decided:

- **The refusal's timing has no hermetic test.** §8 requires an exchange's refusal for "a member you
  are not in" and for "a member that does not exist" to be indistinguishable in shape **and timing**;
  W4 has a table test for the shape, and **no hermetic test can hold the timing claim** — it is a
  property of a running server under a real network.
  [reconciliation.md](reconciliation.md) §6 carries it as **U3**, an owner check, and it stays
  unclosed.
- **Which of two credentials a member's run picks, when the member holds its own and the workspace
  holds one too.** §4 says a member's own credentials keep working and that sharing is additive; the
  design does not say **how the two are chosen between** at a run. W6 owns the rule and this document
  does not guess it; [reconciliation.md](reconciliation.md) §7 carries it as **V5**.
- **The old exchange had no single-use identifier, and the new one does.** That was a review finding
  and it is **fixed** rather than open: §3's assertion carries a single-use `jti`, and W4 and W5 own
  the tests. It is named here because the review found it in the *first* exchange, and a reader of a
  release before this amendment should know the shipped design did not have it.
