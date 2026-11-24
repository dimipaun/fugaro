# The person scope contract (C2)

Task **C2** of [implementation-plan.md](implementation-plan.md). It is the second half of
workspace-service's scope contract: what a scope that serves **people** adds to **C1**'s `Scope`. The
rules it states are **R3** (the workspace's launcher class, and the roster it admits against), **R4**
(which slip a call may carry) and **R7** (the candidate), with **R8** (a person with no token), **R9**
(the member's switch), **R11** (update with runs in flight) and **R13** behind the shape
([reconciliation.md](reconciliation.md)); the decisions are D2969 (the person's own prefix, after the
threat model), D2970 (the workspace's own grant route and launcher class), D2971 (the roster's people
may store a token at the workspace), D2974 (a missing personal token refuses and never falls to a
bundle entry), D2976 (a personal token is never lent), D3144 (an exhausted token is skipped) and
D3146 (a removed person's run halts) ([spec.md](spec.md)). The shapes are
`internal/credsvc/scope_person.go`: `PersonScope`, `LauncherVerifier`, `RosterReader`,
`PersonalReader`, `PersonalReaderFunc`, `NewPersonalReader`, `PersonScopeConfig` and
`PersonScopeConfigForServe`. The stub this package ships while the sides are built
(`scope_person_stub.go`, with its test) is deleted by **I1**.

**WC4** (the workspace's scope, which implements `PersonScope`), **WC5** (the personal reader) and
**WC14** (the wiring `fugaro credentials serve` does) are written against this document in parallel,
all three at level 1. **`internal/credsvc/scope.go` is not edited**: it is workspace-service's
contract file (C1), and a second interface leaves it alone ([implementation-plan.md](implementation-plan.md),
§10). A side that finds this contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it; **R-person-scope** is the one already anticipated, and it is named
[below](#the-candidates-interface-is-this-contracts-best-reading-and-r-person-scope-is-anticipated).

## The shape

`Scope` (C1) is the per-call seam every route is served out of. A workspace-mode deployment whose
members may route a run's model traffic to the workspace also serves the workspace's **own** grant and
claim (R3, D2970) and the personal candidate a workspace slip names (R7), and that half is
`PersonScope`: a **second interface a scope may also implement**, not four more methods on `Scope`.

| Name | Who answers it | What it means |
| --- | --- | --- |
| `PersonScope.Launchers() LauncherVerifier` | the workspace's own launcher class | the verifier a person's Google ID token is admitted by when the workspace's service is asked for a slip, against the **workspace's** roster — never the member's project's (R3, D2970) |
| `PersonScope.Slips() creds.SlipStore` | the workspace's own database | the slip store the call's slip lives in: the grant creates one, the claim claims one, and a slip read through it is a slip this service granted (R3, R4) |
| `PersonScope.OwnsSlip(ctx, id) (bool, error)` | the workspace's own slips | whether a presented slip id is this service's own — R4's fence on the service's side, asked of the id before a slip is read for its binding, its claim or its allowance (F13) |
| `PersonScope.Personal() PersonalReader` | WC5's reader | the reader that answers the personal candidate of a call's slip (R7) |
| `LauncherVerifier` | `*Launchers` (WC14) and `*Authenticator` | one method, `VerifyLauncher`, and nothing else: the class holds no run identity, no job account and no reader of an installation's state |
| `RosterReader` | `*workspace.RosterStore` as it stands | the workspace's own roster node: the document, its absence, and a read that failed — the node and not its meaning |
| `PersonalReader` | WC5 | one method, `Personal(ctx, creds.Slip)`, answering the candidate, the absence of one, or the refusal class |
| `PersonalReaderFunc`, `NewPersonalReader` | WC5 supplies the body | the constructor the workspace's scope builds its reader through, a **variable** so the signature exists before the body does (C1's own reason) |
| `PersonScopeConfig` | WC14 fills it in | the three things a person scope is built from: the roster's reader, the launcher verifier and the slip store |
| `PersonScopeConfigForServe` | WC14 sets it, WC4 reads it | the configuration **this process's** person scope is built from; its zero value serves no grant, as today |

**Two package-level variables are how the sides meet, and neither could be a parameter of C1's.**
The configuration the person scope is built from is the CLI's to build — `fugaro credentials serve`
holds the workspace's database client, its roster node's name and its launcher list (WC14) — and it
cannot travel through C1's `WorkspaceScopeConfig`, because that type belongs to `scope.go` and this
task does not edit it. So it travels as `PersonScopeConfigForServe`: written once by the process's own
serve path, before the resolver is built, and read once by the workspace's scope when it builds its
reader — never per call, and never by a route. The reader itself travels the same way for C1's own
reason: **WC4** (the scope that calls the reader) and **WC5** (the reader) are written in parallel, so
the constructor has to answer before its body exists, and `NewPersonalReader` is a variable of the
constructor's signature that WC5's file assigns and `scope_person_stub.go` answers until WC5 lands.

**The file-name order runs the other way from C1's, and the stub is what yields.** The toolchain
presents a package's files in name order, so C1's stub (`scope_stub.go`) is initialized before its own
side (`scope_workspace.go`) and assigns the constructor unconditionally. WC5's file is
`personal_workspace.go`, which sorts **before** `scope_person_stub.go`, so an unconditional assignment
by the stub would be the one that stood and WC5's reader would never be reached: the stub therefore
assigns only a nil constructor. A caller does not have to know any of that — it calls the constructor
and gets a reader or a refusal, never a nil.

## The rules

1. **The personal half is additive, and that is a property of the shape (WC5).** An installation's
   scope implements `Scope` and not `PersonScope`, so nothing about an installation's path changes:
   there is no flag to check and no branch to get wrong. A route of the personal half asks the scope
   for `PersonScope` and refuses where it is not implemented, exactly as `Scope.Serves` refuses a
   route a deployment does not serve. A scope that answers `PersonScope` and serves no grant — the
   zero `PersonScopeConfigForServe` — is the state every deployment is in until WC14 wires one.

2. **The workspace's own class, and never the member's project's (R3, D2970).** The grant a
   workspace's service answers is admitted against the **workspace's** roster, over Google's ID-token
   keys: a member's admins can forge their own project's launcher list and can mint their own
   project's identities (D1943), so a slip of the workspace's that they could obtain would be a slip
   over the workspace's money signed by the party spending it (D2970). What the class admits is R3's: a
   verified person whose address is a `human` of the roster — a service account is never a launcher
   (F12), and a person added since the workspace owner's last `fugaro update` waits for the next one
   (D2971).

3. **The roster is re-read as the published node is (R3, F25, D3146).** A copy older than 60 seconds
   is re-read, and a roster that cannot be re-read **refuses the call as unavailable** — never as a
   caller refusal, so a run waits for a service it cannot reach instead of halting on a verdict
   nobody made (D2983). The same check is made under a slip and at the claim, so a person removed from
   the roster stops being served within a minute whether or not the sweep of their containers
   succeeded, and a run in flight halts with a reason that names the removal rather than moving on to
   the shared entries (D3146, R11). **The mechanism and the clock are WC4's**; this contract fixes
   only that the roster reaches the scope as the node does (`RosterReader`) and that a read which
   failed is an error and never "no roster".

4. **A call carries only this service's own slip (R4, F13).** The workspace's service serves the
   grant and the claim routes, so a call to it may present a slip — but only one **it** granted, and
   never a member's. The gateway keeps the two apart by upstream (a member's slip goes to the member's
   service), and `OwnsSlip` is the service's own fence beside it, asked of the **id** before anything
   is read for the slip's binding, its claim or its allowance: false is a call presenting a slip this
   service never granted, and a read that failed refuses the call, because an id whose ownership could
   not be established is not one this service may serve under.

5. **The candidate is the person's token, put first and never fallen through (R7, D2974, D3144).** For
   a call under a workspace slip naming the person's token, with the member's switch on, the offering
   puts that candidate **ahead of** the shared entries the call is admitted to. A container that is
   missing, or has no version, **refuses** the call and names `fugaro secrets user set`; a read that
   failed refuses; and neither ever falls to a bundle entry of the same name — a fall-through would
   serve the shared, paid credential without asking its allow-list and count it as unbilled, which is
   the whole of what D2974 refuses. A token that is present and exhausted is skipped by the cascade's
   existing rule and the next admitted candidate is tried (D3144). A personal token is never lent and
   a run launched by a service account never holds one (D2976), and its use does not count against the
   workspace cap, because the cap follows the credential actually served (D2965).

6. **The switch is the member's, and this shape only carries the question (R9, D2972).** Whether a
   personal token may be served is one key at the top level of the **member's** published policy, off
   when absent, read by the workspace's service as the only enforcer. Its name and its home are
   **C1**'s (`PersonSwitchReader`, WC6) and are deliberately not here; what this contract fixes is the
   answer class — a switch that is off **refuses** a call under a slip (R11: a member turning it off
   reaches a run in flight at the service's next read), while a run that never had a slip is served
   the shared entries exactly as before (R8).

7. **Names, and never values (G9).** `PersonalReader.Personal` answers a **candidate**: a name, a tier
   and a provider, spelled in the one name grammar (`internal/secretname`). The value is read by the
   route that serves the call — `dynamicsecrets.go`'s per-secret read, which WC5 edits — and no field
   of this shape carries one. A refusal names the class and the fix, never a name the caller could use
   to reach anything.

8. **Nothing here computes (implementation-plan.md §3).** This contract declares types, the stub and
   this document. It reads no policy, no switch, no roster and no container; it fixes no sentence, no
   tier, no clock and no caching; and its stub decides nothing — the reader it answers offers no
   personal candidate, which is the state every call is in today (R8), rather than refusing, which
   would stop a member's runs over a credential the service was never asked to serve.

## What this contract cannot promise

### The candidate's interface is this contract's best reading, and R-person-scope is anticipated

`PersonalReader.Personal` is handed the call's `creds.Slip` and nothing else, because the slip is the
whole of what a call of this half carries: it names whose token it is (its `Owner`, the verified `sub`
of the launcher that asked) and which credential is served (its `Allowed` list). That is this
contract's reading of what WC5's reader needs, written before WC5 exists, and **U9** is the open
question beside it — whether the crafts a member's repository declares are honoured for a call routed
to the workspace, where no account header is sent. If WC5 (or WC4 through it) finds that a candidate
needs something of the call the slip does not carry, the answer the plan already names is
**R-person-scope**: a contract change to this method's signature, and both sides in the same sweep —
never a second reader reached around the scope, which would be C1's rule 1's second derivation under
another name.

### The roster is keyed by address, and a slip records an account id

The roster's people are keyed by **address** (F11) and a slip records its owner as the verified
**`sub`** (D1938). R3's check on every call under a slip — "the slip's owner is no longer on the
roster" — therefore has to get from one to the other, and this contract does not decide how: the
`LauncherVerifier` answers both facts for a **presented token** (the configured spelling of the
address and the token's `sub`), which is what the grant path has, and a call under a slip presents no
identity token at all. Whether that gap is closed by the grant recording more, by the roster gaining
an id, or by a stricter reading of what "the owner is on the roster" means, is **WC4's**, and it is
the most likely source of the R-person-scope change above. What this contract fixes is that the scope
holds both readers it would need to make the check.

### The sentences, the classes and the mechanism belong to the sides

This contract declares questions and answer classes, not sentences: the refusal a run is told for a
missing container (D2974), the reason a halt names for a removed person (D3146), the message a
workspace's grant refusal carries (one class that names no account, repository or person), and the
audit line each of them writes are **WC4's and WC5's**. How `OwnsSlip` is answered — what the real
side reads to tell a member's slip from the workspace's own — is WC4's too, as the node's caching
and its clock are. What this contract will not let a side do is answer an absence where a refusal is
owed: rules 3, 4 and 5 fix which of the two a call is given.

## What each side may rely on

- **The workspace's scope (WC4)** may rely on: `PersonScope` being the four answers above and nothing
  more; `NewPersonalReader` being non-nil whatever state the sides are in (a nil one would be a panic
  at startup rather than a refusal); `PersonScopeConfigForServe` having been set by the process's own
  serve path before the resolver is built, so reading it at construction is safe and reading it per
  call is unnecessary; and `RosterReader` being satisfied by `*workspace.RosterStore` **as it
  stands**, so its configuration needs no adapter. It implements `PersonScope` and rewrites
  `Scope.AcceptsSlip`'s meaning for workspace mode in `scope_workspace.go` (its own file, not
  `scope.go`). It may not edit `scope.go`, and it may not reach a member's database: the roster, the
  slips and the meter are the workspace's own (R8 of workspace-service).
- **The personal reader (WC5)** may rely on: the constructor's signature as written here, the three
  fields of `PersonScopeConfig` and their meaning, and the three answer classes of `Personal`. Its own
  `init` in `personal_workspace.go` **is what stands**: the stub yields to it because that file sorts
  first (the shape section says why). It answers a candidate and never a value (rule 7), and it
  refuses where R7 and D2974 refuse rather than answering an absence (rule 5).
- **The wiring (WC14)** may rely on: `PersonScopeConfig`'s shape being the three readers and no
  fourth, and `PersonScopeConfigForServe` being the one variable it sets. Without them — a workspace
  with no roster node, no launcher list or no slip store — the scope serves no grant and no personal
  candidate, which is today's behaviour and not a failure to start.
- **I1** may rely on: `scope_person_stub.go` and its test being the whole of what this contract adds
  that goes away, and on the deletion being a deletion and not an edit — the stub's guarded `init` is
  what makes that true.
- **The threat model (WC0)** may rely on: this shape closing nothing by itself. What it fences is one
  layer of R3, R4 and R7's rules — the class, the ownership of a slip, and the refusal classes — and
  the residues WC0 states beside them (F14's spending window and F24's replay) are not touched by a
  type.

## What this contract does not decide

- **The switch's name and its home** — C1's `PersonSwitchReader` and WC6's (U8). This contract
  carries only the answer class (rule 6).
- **The name grammar, the tier and the container read** — `internal/secretname` and WC5's
  `personal_workspace.go`, with `dynamicsecrets.go` beside it. This contract fixes the candidate and
  not the credential (rule 7).
- **The roster's freshness, its clock and its decode** — WC4's. R3 and R13 fix the behaviour;
  `RosterReader` hands over the node and `workspace.DecodeRoster` is what reads it.
- **The pool, the shared entries and their allow-list** — workspace-service's C3 node and WS15's
  pool. The personal candidate is put first among the candidates the call was already admitted to
  (rule 5), and which those are is not this contract's.
- **The routes** — which route of the personal half serves what, the grant's request and response
  shapes (C1's `creds.PersonGrant`) and the typed refusal of a route the deployment does not serve.
  WC4, WC7 and WC8.
- **What the workspace's service may read in the workspace's project** — the prefix-conditioned
  accessor over the person's container (R6): IAM's, and WC2's, and not a shape.

## Review

Reviewer tier **opus + independent review** before WC4, WC5 and WC14 launch
([implementation-plan.md](implementation-plan.md), Conventions): this is the contract the personal
half of the workspace's service is built on, and what it fences — whose token may be served, under
whose slip, and what happens when it is not there — is where a mistake would serve one person's
subscription to another person's run, or serve a shared, paid credential where the person's own was
meant to be refused (D2974).
