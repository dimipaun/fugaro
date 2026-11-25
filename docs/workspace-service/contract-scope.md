# The scope contract (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). The rule it states is **R1** (the seam),
and the two questions it answers for the route half are **R14** (what workspace mode serves) and **R18**
(a call to the workspace's service carries no member secret), with **R5**, **R6**, **R8**, **R13**,
**R17** and **R19** ([reconciliation.md](reconciliation.md)) behind the readers. The decisions are
D2980 (a second Cloud Run service on the same binary), D2981 (a member's run calls it directly),
D2982 (the route is decided once at the run's start), D2988 (a member's runs only), D2992 (shared
project jobs are served) and D1299 as amended (the service holds no write on a member). The shapes
are `internal/credsvc/scope.go`: `Scope`, `ScopeResolver`, `ScopeFault`, `StateStore`, `RepoAuthReader`,
`OfferingReader`, `ForgeReader`, `WorkspaceNodeReader`, `WorkspaceScopeConfig`,
`WorkspaceScopeResolverFunc`, `NewWorkspaceScopeResolver` and `WorkspaceKeepWarmPath`. The stub this
package shipped while the sides were built (`scope_stub.go`, with its test) was deleted by **I1**;
what stands in its place is WS1's case for a scope that serves nothing
(`unservedResolver`, `TestARouteTheScopeDoesNotServeIsRefused`), which is a scope inside a test
rather than a resolver a deployment can be built with.

**WS1** (the installation's scope), **WS2** (the workspace's scope) and **WS5** (the process) are
written against this document in parallel; **WS13** reads the keep-warm path from it, and **WS16**
and **WS17** read the forge readers. What hands the scope to a running service — `Server.ServeHTTP`
asking the resolver, and the gate that refuses a route the scope does not serve — is **WS1**'s, which
edits `server.go`; what the workspace's readers are built from is **WS2**'s, over **C3**'s node and
**WS15**'s pool. A side that finds this contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it; **R-scope-forge** is the one already anticipated, and it is named [below](#the-forge-surface-is-this-contracts-best-reading-and-r-scope-forge-is-anticipated).

## The shape

The scope is one value per **call**, not per process, because the member a call belongs to is a fact
only the call carries: its run identity verified it (`Workspace.Verify`), and it decides which
database the readers read, which repository table the pairing resolves through and which pool the call
may be served from.

| Accessor | Who answers it | What it means |
| --- | --- | --- |
| `Caller() Identity` | the resolver's verification | the run identity the call's token proved; the zero `Identity` on a route answered ahead of the shared parse (a person, a daemon, a launcher or a build holds none) |
| `Policy() PolicyReader` | the installation's one node, or the member's own published policy | the project's shared node and one repository's own admission node, for **this** caller |
| `Kills() KillReader` | the installation's database, or whatever the workspace's resolver chooses | the operator's stop switches the call is held to, read before the bundle |
| `State() StateStore` | the installation's database, or the **workspace's own** | the marks, the leases, a run's placement and the tie-breaker — the cascade's own state, and **never** the per-repository auth record. `StateStore` is `creds.CascadeState`, the very interface `creds.Selector.Store` is typed as, so `&creds.Selector{Store: scope.State()}` compiles as it stands: no adapter and no cast back to `creds.Store` (rule 2) |
| `RepoAuth() RepoAuthReader` | the installation's database, or the **member's** | what one repository's own configuration says its runs go out on (`creds/repoauth/<slug>`, D2070 (a)) — written by that repository's own `fugaro update`, so it is read where it was written and not out of the workspace's state |
| `Outcomes() OutcomeReader` | the installation's database, or the member's | whether a run of this caller's repository has already ended (W2) |
| `Repos() map[string]string` | the installation's repository table, or the **calling job account's** repositories | the slug a run identity carries to the canonical name the policy speaks; a slug it does not hold is refused at the pairing, never guessed (D1066) |
| `Offering() OfferingReader` | the bundle's own credentials, or the intersection over the workspace's pool | the pool the call may be served from — a narrowing, never a widening |
| `Forge() ForgeReader` | the installation's token store and its table, or the workspace's | the Bitbucket token a proxied call is served with, and the coverage a forge call is admitted by; a nil token store refuses the call in both modes (rule 10) |
| `Meter() *WorkspaceMeter` | nil on an installation; the workspace's own meter | the workspace's cap, taken where a call is paid for (D1301 as amended) — the one accessor whose zero answer is a declared absence and not a refusal (rule 9) |
| `Serves(path string) bool` | the deployment's route set | whether this deployment serves that route at all (R14) |
| `AcceptsSlip() bool` | the deployment's caller rule | whether a call that presents a slip is served under it (R18) |

`ScopeResolver.Resolve(ctx, r)` is the whole of what a deployment is parameterised by: it verifies the
caller the request carries and answers the scope it is served under. It is handed the `*http.Request`
and not a parsed `credsvc.Request`, because the routes answered ahead of `RequestFromHTTP` carry no
run identity to parse and because **which** verification a route needs is a property of the route: the
resolver decides it, so no route can reach a reader it was not admitted to.

`Resolve` answers one of **three** things, and the three are not interchangeable. A route the
deployment does not serve is **not** an error: `Resolve` answers a scope whose `Serves` is false, so
that a route can be told apart from a caller, and so that the one gate that refuses an unserved route
stays in one place. An error is a refusal of the **caller** — the verifier's own, naming the field that
failed and never its value — which `ServeHTTP` answers exactly as it answers the verification it makes
itself today. And **`*ScopeFault`** is the third: the scope could not be built for a reason that is not
the caller's at all — the workspace's node could not be re-read (R17), a member's database could not be
reached — which `ServeHTTP` answers as the **service's own failure** (`StatusUnavailable` with
`ErrorStateUnavailable`, the refusal `refuseIdentity`'s default arm already gives a state that could not
be read) and never as a verdict about the caller. Confusing the two is not cosmetic: a 403 halts a run's
gateway as a missing invoke grant (R16), and a table that could not be re-read is not a caller who is
not admitted. The type's own comment carries the whole of it, the audit line and `Caller` included.

`NewWorkspaceScopeResolver` is a **variable** of the constructor's signature
(`WorkspaceScopeResolverFunc`) and not a function, and that is the contract's own shape rather than an
accident: WS5 built a workspace-mode process through the constructor while WS2 — which owns its body,
in `scope_workspace.go` — was still being written, and Go has no way to declare a function in one file
of a package and define it in another. The two sides therefore assigned it from `init` while the
scaffold was up: `scope_stub.go` assigned a resolver whose scope refused every call, and WS2's file
assigns the real one, which I1 is what left as the single assignment. A caller does not have to know
any of that: it calls the constructor and gets the workspace's resolver, never a nil.

## The rules

1. **One route, one rule (R1).** A route reads every reader it needs through the scope and never
   through a second derivation of the member. That is the whole point of the seam: a workspace's
   service and an installation's are one binary with one set of routes, and what differs between them
   is the resolver they are built with. **A second implementation of a route is the thing this
   contract exists to prevent** — the rules here decide whether a credential is issued at all, and two
   spellings of one rule drift.

2. **The workspace's readers are the member's, and the state is the workspace's own (R1, R8, D1299 as
   amended).** The policy, the outcomes, the repository table and the per-repository auth record are
   read where the member's own administrator — or the member's own `fugaro update` — wrote them: the
   member's database, through the verifier's `iss` (finding 6). The auth record is the sharpest case
   of it, and it is why it is a reader of its own (`RepoAuth()`) and not a method of the state store:
   it is what narrows a repository's reach to the auth its own configuration names (D2070 (a), R7),
   `fugaro update` writes it into that repository's own project's database, and a workspace scope that
   read it out of its own database would find nothing — an absent record the cascade reads as "the
   cascade, unchanged" (D2070 (a)), which would serve a member's run the whole of the workspace's
   pool. The **state is not** the member's: a cooldown, a rotation position and a spend figure for a
   workspace credential are facts about the workspace's pool, so they live in the workspace's own
   database — which is why `State()` is `StateStore`, `creds.Store` without the auth-record method, so
   that the wrong database cannot be reached by habit. **The split has to hold where the state is
   used, and it does**: `creds.CascadeState` is declared in `internal/creds` and `creds.Selector.Store`
   is typed as it — a selection reads the six methods and never the record — so a route passes
   `scope.State()` straight to a selection, and an implementation that has to cast back to `creds.Store`
   or write an adapter supplying `RepoAuth` is a change request against this contract and not a local
   workaround. **The one call site that reads the record is WS1's to move, and it is named here
   because the types do not cover it**: `cascade.go` reads it today as
   `s.cfg.State.RepoAuth(ctx, id.Slug)`, and `Config.State` stays the wide `creds.Store` (`server.go`,
   which also builds `creds.Selector` from it) — so in a workspace-mode process that line answers out
   of the workspace's own database, and `narrowToRepoAuth`'s reading of an absent record is exactly
   the failure this rule describes. WS1 changes that line to read `Scope.RepoAuth()`, the caller's
   reader, and leaves `Config.State` wide so the installation's own store goes on answering the state
   and the record. **WS1's acceptance names the scope's readers and not this call site, and its two
   tests pass whichever database the record is read from**, so this sentence is what holds it until
   the route reads through the scope. The service holds no write on any member at all, which is a
   property of this split and not a promise about it.

3. **The repository table is the caller's, and it is where R19 lands (R6, D2992, R19, F28).** An
   installation's table is every repository it serves. A workspace's holds the repositories of the
   **calling job account** and no other — one for a repository's own job, several for a shared project
   job — so a run whose repository is not in that list is refused at the pairing with a typed reason
   that names the workspace owner's `fugaro update`. A repository the workspace does not list is never
   resolved by guessing it from the slug's text (D1066).

4. **The offering is a narrowing, never a widening (R5).** Whatever the offering reader leaves out is
   never served; what it answers is still held to the legs the route applies itself — the repository's
   own admission, the crafts a node publishes and the repository's own auth record (read through
   `RepoAuth()`, from the member's database — rule 2). A workspace answers the intersection of the
   workspace's pool with the **member's** policy and the repository's admission in the member's own
   database, which is the same intersection R5 fixes: a member's run is never served a credential its
   own administrator did not admit.

5. **Workspace mode serves a closed set of routes (R14, D2988, A2).** The model routes
   (`/v1/messages`, `/v1/messages/count_tokens`); the forge routes a run calls — the git proxy under
   `/v1/bitbucket/proxy/`, a run's own token mint (`/v1/github/installation-token`) and its context
   tokens (`/v1/github/context-tokens`); and the keep-warm route. **Every other route is refused with
   a typed refusal**: review signing (`/v1/review/sign`) and verification (`/v1/review/verify`), the
   slips (`/v1/slip`, `/v1/slip/claim`), the workflow secrets (`/v1/workflow-secrets`), the build
   tokens (`/v1/build/token`, `/v1/build/credentials`), the read mints (`/v1/bitbucket/read-token`,
   `/v1/github/read-token`), the merge mint (`/v1/github/merge-token`), the label route
   (`/v1/github/labels`) and the issue route (`/v1/github/issues`). A member's run keeps calling its
   **own project's** service for those, and an installation serves all of them as it does today.

6. **The keep-warm route carries no secret (R14, D2985, F33).** `WorkspaceKeepWarmPath` is where a run
   that routed a kind to the workspace posts its beat, at the slip beat's interval, so that the
   service is not scaled to zero between the calls it pays for. The beat the run already has posts the
   slip's id and its claim secret, and R18 refuses both, so this route takes the job's identity and
   nothing else. The path is C1's to name and both sides read it from here: the service that serves it
   (WS2) and the run that posts it (WS13). It is the one route of the workspace mode that must exist
   before the service serves anything, because a beat posted to a route the deployment does not serve
   is a 404, which a run reads as the service being unreachable.

7. **A call that presents a slip is refused (R18, F40).** A call to the workspace's service carries
   the job's identity and the run's token and nothing else of the member's: no slip id, no claim
   secret, no auth-mode or account header. A call that presents **no** slip is unaffected by the
   answer, in both deployments: the slip-less path is what this service served before the slips
   existed and what it goes on serving (`server.go`'s own rule, which is the slips plan's R9 and not
   this one's).

8. **The node is re-read, and a failed re-read is a service fault (R13, R17, F26).** The workspace's
   scope re-reads the published node when the copy it holds is older than 60 seconds, conditionally, so
   a member added or an entry shared or unshared takes effect within a minute with no new revision.
   When that re-read fails and the copy held is older than the 60 seconds, the scope refuses the call —
   **as a `*ScopeFault` and never as a refusal of the caller** — because it never serves from a stale
   table (a table that cannot be re-read is not one that still admits what it said) and because a 403
   is what a run's gateway reads as a missing invoke grant (R16): the caller did nothing wrong and is
   owed the truth that the service could not answer. A member's **new repository** still needs the
   workspace owner to declare it and run `fugaro update`, for the invoke grant and for the account's
   list.

9. **The meter is declared and not yet wired, and it is the shape's one non-refusing zero.** `Meter()`
   is the scope's answer to "what is the workspace's cap for this call", and the reservation, the
   settle and the advisory headroom the member is answered with are **WS8**'s (R15). Nothing in this
   contract reserves anything: an installation answers nil, a workspace answers its meter, and the
   wiring is a behavior change with a task of its own rather than something half-landed here. **The
   nil itself is the exception to the shape's rule that every accessor's zero answer refuses**: a
   route serves a call UNRESERVED where the meter is nil, so a resolver that holds a cap and answers
   nil has removed the cap rather than refused the call. Nothing in the shape can tell a declared
   absence from a forgotten one at the point the route asks, so the requirement sits on the resolver:
   **WS2 holds, with a test of its own, that a workspace with a cap configured never answers a nil
   meter**, and WS8 holds the other side of it where the reservation is wired.

10. **A nil Bitbucket selector refuses the call, in both modes (T8, D2355).** `Forge().Bitbucket()`
    answers the store a proxied call is served with, and nil where the deployment holds none; the
    proxy refuses rather than forwarding with a token nothing chose. Today the ROUTE reads a nil as
    "the bundle's legacy entry" (`chooseBitbucketToken`'s own fallback), so the contract fixes which
    side answers what: **an installation whose configuration wired no store answers the legacy selector
    explicitly** — the one this package already holds (`legacySelector`, bitbucketproxy.go), which
    serves the bundle's legacy entry and the contract's `no_bitbucket_token` class where the bundle
    holds none — and **WS1 replaces the route's own fallback with the scope's answer** when it takes
    the selector from here. A single-token installation is served exactly as it is now, which is why
    no existing test changes; and a workspace scope that answers nil refuses, instead of being handed
    the workspace bundle's legacy token by a fallback nobody chose.

11. **The node admits the caller, and `Workspace` is the verifier and not the membership (R11, R13,
    F26).** `WorkspaceScopeConfig.Workspace` is a `*Workspace`: the process's front door, built **once**
    from `WorkspaceConfig.Members` at startup (`NewWorkspace`'s `readMembers`) and never refreshed.
    **F26 is that object**, and R13 is its consequence — so the table it resolves against is not the
    authority on who is served today. The re-read node is (rule 8), and it is what says which members
    there are. **A caller the node no longer lists is refused from the service's next read of it
    (R11), whatever the frozen table still resolves** — a refusal of the caller, because a verdict
    about the caller is exactly what it is, and so the one audit line every post-verification refusal
    writes. A resolver that took its members from `Workspace` instead would keep a removed member
    served — and drawing on the workspace's pool — until a new revision, which is the fence R11 names
    and the one this shape would otherwise hand WS2 open. What the frozen table is **for** is what only
    the verifier holds: which member a job account's identity belongs to, that member's own outcome
    reader, and the audience the run's token was minted for. WS2 holds both halves with the two tests
    it already owes — `TestWorkspaceScopeRereadsItsNode` (a member removed is refused at the next read)
    and `TestAFailedRereadRefuses` (the fault, and not a caller refusal) — and WS4 is what publishes
    the node they read.

## What this contract cannot promise

### The forge surface is this contract's best reading, and R-scope-forge is anticipated

`ForgeReader` carries two things — the Bitbucket token store a proxied call is served with, and the
coverage a forge call is admitted by — and that is C1's reading of what WS16 and WS17 need, written
before either of them exists. What the forge routes need **beside** that is deliberately not here: the
App key and the proxy's own configuration come out of the bundle and the `Config` as they do today,
and only what is per **caller** is on the scope. If WS16's or WS17's review finds a reader that is
genuinely per caller and genuinely missing, the answer the plan already names is **R-scope-forge**: a
contract change that adds a method here and to both implementations in the same sweep, reaching WS1
and WS2 — never a second reader reached around the scope, which would be rule 1's second derivation
under another name.

The same is true of `OfferingReader`'s argument: it takes the `*Bundle` rather than holding a loader,
because the route loads the bundle once for the credential **values** and a reader that loaded its own
would be a second read of the one secret this service holds. A forge reader that needs the bundle
would be a change of the same kind.

### The outcome reader has no route reader today

`Outcomes()` is declared because R1 names it and because the scope is the only place that can name the
right database for a caller — the resolver's verification is what reads it today (an ended run's token
is not honoured again, W2), and the workspace builds each member's verifier with that member's own
outcome reader. **No route reads `Scope.Outcomes()` today.** It is carried as the caller's reader, not
as a reader a route is promised to have a use for, and a review that finds it dead is reading a shape
R1 asked for rather than a hole in this contract.

### The refusals are declared here and written by WS1 and WS2

This contract declares the **questions** — `Serves`, `AcceptsSlip`, `Covers`, and the readers that
refuse — and not the sentences. The typed refusal of an unserved route is written where the gate is, in
`ServeHTTP` (WS1, which edits `server.go`), and the workspace's tests for it are WS2's
(`TestWorkspaceModeRefusesEveryOtherRoute`). The one sentence it does fix is the fault's, because it is
the answer that has to be told apart from a refusal of the caller: `StatusUnavailable` with
`ErrorStateUnavailable`, answered by the gate and never by a route (rule 8, and `ScopeFault`'s own
comment). Nothing in `scope.go` refuses anything itself: the stub's refusals are the stub's, and I1
deletes them.

## What each side may rely on

- **The installation's scope (WS1)** may rely on: every accessor answering out of today's `Config` and
  today's verifier, with **no existing test in `internal/credsvc` changing**; `Serves` true for every
  route this service has and `AcceptsSlip` true, which is today's behaviour; `Meter()` nil; `RepoAuth()`
  answered from its own database, which is where its own `fugaro update` wrote every record; and
  `Bitbucket()` answered with the legacy selector where its configuration wired no store (rule 10). It
  also **moves the one read of the auth record to the scope**: `cascade.go`'s
  `s.cfg.State.RepoAuth(ctx, id.Slug)` becomes a read through `Scope.RepoAuth()`, while `Config.State`
  stays the wide `creds.Store` it is today, so the installation's own store goes on answering the state
  and the record and no route reads the record through a state handle (rule 2). It **answers the
  three things `Resolve` can say**, too: the gate refuses a route whose `Serves` is false, answers a
  resolver error as the caller refusal it is today, and answers a `*ScopeFault` as the **service's own
  failure** — `StatusUnavailable` with `ErrorStateUnavailable`, the caller's one audit line written
  where the fault carries a `Caller` and no line where it carries none. Neither of WS1's two named
  tests covers the third, so this sentence and the fault's own type are what hold it. It may not read
  the scope's readers before the resolver has answered a scope, and it may not reach a `Config` field a
  route is supposed to take from the scope.
- **The workspace's scope (WS2)** may rely on: the `MemberCall` `Workspace.Verify` produced as the one
  thing that names the member, its database and its verified slug; C3's node as what the offering and
  the forge coverage are read from — **and as what admits a caller, `Workspace`'s frozen member table
  being the verifier alone (rule 11)** — with `WorkspaceShared`'s signature as WS15 changes it; and the
  constructor's signature as written here, which it supplies the body of by assigning
  `NewWorkspaceScopeResolver`. It answers `RepoAuth()` from the MEMBER's database and never from the
  state store (rule 2), and it answers its meter — never nil — for a workspace whose pool has a cap
  (rule 9). It may not write to a member's database (R8) and may not serve a route outside R14's set.
- **The process (WS5)** may rely on: `NewWorkspaceScopeResolver` being non-nil whatever state the
  sides are in — a nil constructor is a panic at startup rather than a refusal — and on the stub
  answering a refusing scope rather than an error, so that a workspace-mode process starts and refuses
  its calls before WS2 lands.
- **Neither side may rely on** the other's internals: the resolver's route-to-verification mapping,
  the workspace's node caching and the installation's table derivation are not this contract. A
  field's meaning never changes ([CLAUDE.md](../../CLAUDE.md), "a field never changes meaning"): a new
  accessor is additive, and a meaning that has to change gets a new name.

## What this contract does not decide

- **How a route is refused** — the status, the error type and the sentence — with the two exceptions
  this contract does fix, because they are the difference between two answers a run reads differently:
  an unserved route is the gate's own refusal, and a `*ScopeFault` is the service's own failure
  (`StatusUnavailable` with `ErrorStateUnavailable`) and never a refusal of the caller. Everything else
  is WS1's, and WS2's tests read it through the server rather than through this shape.
- **Whose stop switches a workspace-mode call honours** — the member's own operator's, the
  workspace's, or both read together. Nothing in R1 to R19 picks one, and `workspace_cap.go`'s
  comment already names the workspace's own switch as a refusal the workspace's service answers on
  its own account, so the two readings are live. The shape fixes only that the answer is chosen for
  the caller and read before the bundle; a resolver that wants both holds both behind one
  `KillReader`, because the interface answers one reader and a reader may hold two switches. WS2's
  to decide, and the decision belongs in its own comment and in the review.
- **The node's caching and its clock** — where the 60 seconds are measured, how the re-read is made
  conditional, and what the scope does with a node it decoded but does not validate. R13 and R17 fix
  the behaviour; the mechanism is WS2's, and `WorkspaceScopeConfig.Now` is there for its tests.
- **The meter's wiring** — the worst case reserved before a call, the settle on the answer's usage,
  the refusal at the ceiling and the headroom header (R15). WS8's, and see rule 9.
- **The route record and the mode's names** — which repository and kind goes to which service, the
  workspace service's Cloud Run name and the environment name that selects the mode. Those are **C2**
  (`internal/creds/route.go`).
- **The node's shape** — the member rows, the job accounts' repository lists, the pool's entries and
  their forge coverage. That is **C3** (`internal/projectpolicy`, version 2).
- **Whether a workspace's service is deployed at all** for a workspace whose Firebase is another
  project's: it is not (R12, A4).

## Review

Reviewer tier **opus + independent review** before WS1, WS2 and WS5 launch
([implementation-plan.md](implementation-plan.md), Conventions): this is the seam a second deployment
of the credentials service is built on, and the thing it fences — one set of routes serving two
different databases and two different repository tables — is where a mistake would serve one member a
credential another member's administrator admitted. O4 (the run's Firebase token reaches the
workspace's service) is named for that review to confirm or to ask for a narrower token.
