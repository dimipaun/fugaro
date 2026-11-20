# The credential-route wire contract (C2)

The Go half is [`internal/creds/route.go`](../../internal/creds/route.go): `Route`, `RouteReader`, `Kind`, `Holder` and
the five names. This document is the prose half, and it is the half the sides are held to: it says what each side may
rely on, what none of them may, and what changing the contract costs.

C2 is a contract and not an implementation ([implementation-plan.md](implementation-plan.md) §3). Nothing here
computes: it holds a record's shape, the two functions that read and write it, a schema, and a stub reader
(`internal/creds/route_stub.go`, `StubRoute`) that answers "every repository and kind is the project's" and decides
nothing. I1 deletes exactly that file when it wires the sides. It exists because five tasks are written against the
same decision at once and cannot wait for each other: **WS11** (the run routes each kind) is the reader; **WS5**
(workspace mode in `fugaro credentials serve`), **WS14** (`fugaro doctor`), **WS20** (the live checks) and **WS22**
(the route's rule) spell its names and its path; and **WS3** (the pin's address field), **WS6** (the workspace root
deploys the service) and **WS9** (the member publishes the route) build its other side.

It rests on the owner's rulings of 2026-11-19 ([reconciliation.md](reconciliation.md)): the run calls the workspace's
service directly (D2981); the route is decided once at the run's start (D2982), by kind, and an exhausted project
credential never falls through to the workspace (D2989); the member's own copy wins when both exist (D2973); every
credential kind moves at once, not models first (D2984, and D2988 for a member's runs only); the address is a field of
the member's own pin and is never read from anything the workspace publishes (R3, F32); and the route is one
policy-side record per repository and kind, which a run may read and no token but the member's admins' may write (R4,
F23, F34).

## Why the run is told rather than left to work it out

A member's run takes each kind of credential from one of two services — its own project's or the workspace's. It
cannot work that choice out for itself: what the member's policy admits and what its bundle holds are two lists only
the member's own machine reads (F38: a member reads nothing the workspace publishes), and the workspace's entries are
not visible to it at all. A run that guessed would either spend the workspace's money on a project that had chosen its
own credential, or wait on a project that holds none. So the member's own `fugaro update` computes the answer and
publishes it (**WS9** does the computing; this contract only carries the answer), and the run reads it back.

The answer is per repository **and** per kind. Per kind, because a model call names no credential at all — the choice
of service is the only place the kind is visible — and because D2984 serves models, Bitbucket and GitHub together.
Per repository, because admission is per repository (R5): a project may hold a credential for one repository and none
for another, and a single "this project is on the workspace" statement would send every repository of a project to the
workspace the moment one of them is uncovered (F34).

## The shapes

| Shape | Carries | Decided by |
| --- | --- | --- |
| `Route` | `Version`, `Address` — the workspace's service — and `Model`, `Bitbucket`, `GitHub`, each a `Holder` | `DecodeRoute` off the published record; the zero `Route` is "the project serves everything and there is no second service" |
| `Route.Holder(kind)` | the service the given kind is served from: `HolderWorkspace` only when the record says so, `HolderProject` otherwise — including for a kind this code does not know | this package |
| `Holder` | `project` or `workspace`, and nothing else | the member's `fugaro update` (WS9) |
| `Kind` | `model`, `bitbucket`, `github` — the closed vocabulary, `gitprov.KindBitbucket` and `KindGitHub` for the two forges | this package |
| `RouteReader` | `Route(ctx, slug) (Route, error)`: one repository's route | the reader's own side — the Cloud read in the run (WS11), the stub before it |
| `ErrRouteRecord` | "this record is not one a run may be served from": every refusal `DecodeRoute` and `RouteRecord` make wraps it | this package |

**A kind the record does not name is the project's.** `Holder` answers `HolderProject` for a kind the record leaves
out, for a kind spelled `project`, and for a kind this code has never heard of — a kind no record moved is served
where every kind was served before the record existed. That is why the zero `Route` is a safe answer and why the stub
may carry it: a record that is truncated, mistyped or half-understood cannot send a call anywhere but the project.
`Address` is empty exactly when no kind is served from the workspace.

**`RouteReader` answers the zero `Route` and no error for a repository with no record**, which is today's run and not
a failure. An error means the record could not be read or is one this code refuses; a caller that treated an error as
"the project's" would serve a credential from a service an unreadable record may have moved. Nothing in this contract
says what a caller does with a record it refuses — WS11 halts and names the fix.

## The record

One repository's record is one JSON object:

```json
{"version": 1, "address": "https://fugaro-workspace-credentials-abc123-uc.a.run.app",
 "model": "workspace", "bitbucket": "project", "github": "workspace"}
```

- **`version`** is `RouteVersion`, and it is `1`. A reader reads it first and refuses a version it does not know
  **by name**, rather than reading what it recognises: a run would otherwise send a member's credential to a service
  chosen by a document it half-read. Adding a field to the record is a new version, not a new key under this one.
- **`address`** is the workspace's credentials service, from the member's own pin (R3). It is a scheme and a host
  only — no user, path, query or fragment, and never `http` off loopback — because the run mints an identity token
  whose audience is this address and sends the member's own token to it. The rule is one function in this package,
  shared with the merge mint and the label client, because a credential rule respelled per caller is a rule that
  drifts.
- **`model`, `bitbucket`, `github`** are each kind's `Holder`. A record names a kind it moves to the workspace, and
  leaving a kind out is the same answer as spelling it `project`.
- **The record is closed, and `DecodeRoute` decodes it strictly**: a key this code does not model is a refusal, not a
  key decoded past. The version is read first, so a record a later release wrote is refused as a version this code
  does not know rather than as one unknown field.
- **A node that is not there at all is not a refusal**: `DecodeRoute(null)` answers `found == false` and no error.
- **One route has one spelling.** `RouteRecord` leaves out a kind the route does not move rather than writing the
  default, so two encodings of one route are equal bytes — the publish is conditional on the bytes, and a writer that
  spelled the default two ways would rewrite the node forever. `RouteRecord` refuses a route it could not read back
  (a version outside `RouteVersion`, a holder outside the vocabulary, an address a run may not be sent to), so a
  member's own `fugaro update` cannot publish a node every run of it then refuses.

The record's home is a policy-side leaf, `config/policy/route/<slug>`, spelled by `RoutePath`. It is there and not
beside the credential state because a run cannot read that subtree at all: the rules hold
`"creds": {".read": false, ".write": false}` (F23). The leaf is per repository for the same reason
`config/policy/repos/<slug>` is — a read granted on the parent would cascade to every repository's record — and its
segment is `budget.Key` of the raw slug, which is the spelling the rules compare an RTDB token's `fs` claim with. WS22
adds the rule (`TOKEN && auth.token.fs == $slug`), bumps `RulesVersion` and converges an installation through
`fugaro update`; this contract names the path and the escaping and no rule.

`schemas/credential-route.schema.json` is the published half of the record's shape, and
`TestRouteRecordRoundTripsAndMatchesItsSchema` holds the two together: the schema refuses what `DecodeRoute` refuses
and accepts what it reads.

## What each side may rely on

**The member's pin (WS3, WS9).** `WorkspacePinAddressField` is the pin field that carries the address, spelled
`address`. WS3 writes it in `fugaro workspace add` and refuses an address that is not `https` with no query; WS9 reads
it to build the record. A pin without it is valid and means "this member takes no workspace credential", so a pin an
earlier release wrote converges with no edit, and a member with no address publishes nothing and removes a record it
published before.

**The member's publisher (WS9).** It may rely on `RouteRecord` for the bytes and on `ErrRouteRecord` for what a run
will refuse, so a record it publishes is one WS11 reads. It derives the holder per repository and kind from the
member's own bundle and policy (the contract carries no such rule), keeps `HolderProject` for a kind the project's own
copy serves (D2973), and reports that a workspace credential of that kind is not used. It publishes one record per
repository, at `RoutePath(slug)`, and it cannot name the workspace's entries — a member cannot read them (F38).

**The workspace's deployer (WS6, WS7).** `WorkspaceServiceName` is the workspace's own credentials service — the
second Cloud Run service in the workspace's project, the same image as the installation's, running as the existing
workspace account (D2980). The workspace's project is an ordinary installation, so its own service keeps
`internal/infra`'s name and this one is the workspace half of the pair; the two are told apart by their identity and
their admission, never by their code. `WorkspaceModeEnv` is the environment entry that selects workspace mode: the
process is exactly today's without it, and with it it serves one workspace's members through the same routes. Its
**value names the workspace being served** — the node the process reads its member table from — so one name selects
the mode and says which workspace's table is the answer.

**Workspace mode in the service (WS5).** `WorkspaceModeEnv` and `WorkspaceServiceName` are the two names it is
configured by. It serves the routes R14 names and refuses the rest; what a routed call still passes (the workspace
entry's allow-list, the member's policy, the repository's admission) is R5's and is not this contract's.

**The run (WS11, and the beat in WS13).** It reads `RouteReader.Route` once at bootstrap for its own repository and
keeps the answer: a model call goes to the service the model route names and never to the other, exhausted or not
(D2982, D2989). The address it calls is the one `Route.Address` carries, never one from the run's environment (R3,
F32) — an address a launcher supplies is an address a launcher can move, and the run mints an identity token for
whatever address it reads. `WorkspaceBeatSetting` is the setting that turns the keep-warm beat off; it is on by
default, and its key is an `agent.` one because the beat is the run's.

**`fugaro doctor` (WS14).** It reads the same record and reports it, and it may rely on `Holder` for what to say about
each kind. Flagging a route the bundle no longer matches is WS9's derivation, not a second one.

**The live checks (WS20) and the rule (WS22).** They name `RoutePath` and the record's members; WS22 spells the rule
over that path and moves `RulesVersion` with it.

## What no side may rely on

- **A stub's answer.** `StubRoute` admits nothing, moves nothing and reads no record. Nothing it returns is a
  decision any side may act on, and I1 deletes the file rather than promoting it.
- **A kind this code does not know moving anywhere.** `Kinds` is the closed vocabulary of this contract and `Holder`
  has no third value. A side that added a kind moves it in a new version of the record, in this package, and not by
  spelling it into a record and hoping.
- **The record as a decision about the workspace.** It says where each kind is *served from*; it says nothing about
  whether the workspace admits the repository or the member. That verdict is the workspace's own (R5, D1298) and is
  made again where the call arrives.
- **A second implementation of any of this.** The names, the path, the vocabulary and the two functions are here, and
  a side that respelled one — the environment name in Terraform *and* in Go, the escape of a slug in the writer *and*
  in the reader — would be two answers that drift. WS3, WS6 and WS9 call these; they do not keep their own copy.
- **A credential in the record.** There is no token, no secret value and no credential identifier here, and there
  never will be: the record is a document a member's admins write and a member's runs read, and a field carrying a
  value would put a credential in it.
- **A refusal as retryable or as "the project's".** A refused record is a run that halts and names the fix (WS11),
  not one that carries on with today's answer.

## Changing the contract

The record is a wire format, so a field never changes meaning: adding a member is a new `RouteVersion` with a reader
that still reads this one, and removing or retyping a member is the same bump — a reader that guessed at a reused
name would send a member's credential to a service nobody chose. A **name** here is also a wire format: the
environment entry is a deployment, the service name is a Cloud Run name, the setting key is a user's config and the
pin field is a document on a member's disk, so changing one is a change to this contract and not to a caller.

Amending C2 means editing both halves in one sweep — the constant or the shape in `internal/creds/route.go`, the
schema, this document, and the pin (`TestRouteNamesArePinned`) — and the plan anticipates one such change: §7's
**R-route-home**, raised by WS9, if what is left of U1 shows the route's leaf must sit elsewhere under the policy,
which reaches WS11, WS14 and WS22. No side works around a shape it finds short: the shape and the "what each side may
rely on" line change together, in the review that finds it.
