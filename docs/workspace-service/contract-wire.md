# The credential-route wire contract (C2)

The Go half is [`internal/creds/route.go`](../../internal/creds/route.go): `Route`, `RouteReader`, `Kind`, `Holder` and
the five names. This document is the prose half, and it is the half the sides are held to: it says what each side may
rely on, what none of them may, and what changing the contract costs.

C2 is a contract and not an implementation ([implementation-plan.md](implementation-plan.md) §3). Nothing here
computes: it holds a record's shape, the two functions that read and write it and a schema, and it held a stub reader
(`internal/creds/route_stub.go`, `StubRoute`) that answered "every repository and kind is the project's" and decided
nothing — which I1 deleted, exactly that file and no other, once the sides were wired. It existed because five tasks
were written against the same decision at once and could not wait for each other: **WS11** (the run routes each kind) is the reader; **WS5**
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
| `RouteReader` | `Route(ctx, slug) (Route, error)`: one repository's route | the reader's own side — the stub on this tree; WS11's run reads the node itself, through `DecodeRoute`, and implements no reader |
| `ErrRouteRecord` | "this record is not one a run may be served from": every refusal `DecodeRoute` and `RouteRecord` make wraps it | this package |

**A kind the record does not name is the project's.** `Holder` answers `HolderProject` for a kind the record leaves
out, for a kind spelled `project`, and for a kind this code has never heard of — a kind no record moved is served
where every kind was served before the record existed. That is why the zero `Route` is a safe answer and why the stub
may carry it: a record that is truncated, mistyped or half-understood cannot send a call anywhere but the project.

**`Address` is empty exactly when the route came from no record at all** — the zero `Route` a repository with no
published route reads, which is what the stub answers — and it is not a promise in the other direction: every
published record carries an address (a record without one is refused on both sides), and a record may name every kind
the project's, whose address is then never called.

**`RouteReader` answers the zero `Route` and no error for a repository with no record**, which is today's run and not
a failure. An error means the record could not be read or is one this code refuses; a caller that treated an error as
"the project's" would serve a credential from a service an unreadable record may have moved. Nothing in this contract
says what a caller does with a record it refuses — WS11 halts and names the fix.

**One state is on neither side of that line yet, and WS11 writes the arm down here rather than leaving its reader to
find it in the code.** Until WS22 deploys the rule that grants a run token the read of `config/policy/route/<slug>`,
**a node the rules refuse answers as a node that is not there**: Realtime Database answers a read no rule grants with a
denial and a read of an absent path with a not-found, and a member that has not taken the rule is in the first state
for every repository it has. So the run reads a denial as the zero `Route` — today's run, the project for every kind —
rather than as the refusal above, because the other reading stops every run of every project for a feature none of
them uses, and nothing is lost by it: the project's own service is where every kind was served before the record
existed. **The arm is bounded by WS22** (whose own red step is "a run's token is refused the read"): from the release
that carries it, that member's `fugaro update` deploys the rule and publishes the record in one step, so a denial
means the update has not run there yet and the record is not there either — the answer a run already gives for an
absent node — while before it, halting on a denial would halt every run on this tree. It is a denial of the **read**
alone: a record that IS readable and cannot be decoded is refused where the contract refuses it, and the run halts and
names the fix.

## The record

One repository's record is one JSON object:

```json
{"version": 1, "address": "https://fugaro-workspace-credentials-abc123-uc.a.run.app",
 "model": "workspace", "github": "workspace"}
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
  leaving a kind out is the same answer as spelling it `project`: `Holder` answers `HolderProject` for both, which is
  why a reader accepts `project` and a writer never writes it (below). A record may therefore name every kind the
  project's — a member in a workspace whose own bundle covers the repository — and it still carries its address:
  the address is where a *moved* kind is called, and nothing is called there for a kind `Holder` answers
  `HolderProject` for.
- **The record is closed, and `DecodeRoute` decodes it strictly**: a key this code does not model is a refusal, not a
  key decoded past, and a key is matched **exactly and written once** — `MODEL` is not `model`, and a record that
  carries a field twice is refused. `encoding/json` alone matches a key to a field case-insensitively and lets the
  later of two spellings win, and a Realtime Database node holds the two spellings as two children, so a record
  spelled that way would move a credential to the workspace with no key this contract names anywhere in it. The
  version is read first, so a record a later release wrote is refused as a version this code does not know rather
  than as one unknown field.
- **A node that is not there at all is not a refusal**: `DecodeRoute(null)` answers `found == false` and no error.
- **One route has one spelling.** `RouteRecord` writes a kind only when the route moves it to the workspace, so a
  route that spells `project` for a kind and one that leaves the kind out are the same bytes: `HolderProject` is the
  string `project` and not the zero `Holder`, so the omission is the encoder's and not `omitempty`'s, and the two
  spellings a caller could hand it are one record — the publish is conditional on the bytes, and a writer that spelled
  the default two ways would rewrite the node forever. A record an earlier release or a hand wrote with `project`
  spelled out reads the same and is rewritten once into these bytes, after which the publish is a no-op. `RouteRecord`
  refuses a route it could not read back
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
and accepts what it reads, with one thing it cannot say — a JSON object has no way to declare that a key appears
once, so a field written twice is `DecodeRoute`'s refusal alone.

## The import boundary this contract crosses

`internal/creds` is behind an allow-list: `credsImporters` in
[`internal/creds/boundary_test.go`](../../internal/creds/boundary_test.go) names exactly `internal/creds`,
`internal/credsvc` and `internal/cli`, and `TestCredsIsNotReachableFromARun` fails on any other directory that
imports the package. The list is there to keep a **write of the credential state** out of the code a run executes, so
that a later loosening of the database rules is not on its own enough to hand the cascade's marks to a contributor's
process.

**C2 puts the record's Go half inside that boundary, and the run's reader is outside it.** `internal/gateway` (the
in-runner gateway), where WS11's reader and its test live, is off the list today — and so is `internal/runner` if the
half that builds the gateway names a type from here. So consuming C2 means those directories join it: a deliberate
edit in `boundary_test.go`, in both the list and the `want` slice the test asserts against, each new entry with its
reason next to it, made by WS11 in its own change where its review sees it. C2 makes
no such edit now, and no other side of this contract needs one: the sides that read the record in Go are WS5
(`internal/credsvc`), WS9 and WS14 (`internal/cli`) — all three already admitted — and WS11. WS3, WS6 and WS22 care
about the names, not the record: their halves spell the pin's field, the environment entry and the path in a YAML
tag, a Terraform variable and a rules template, none of which can import anything.

**Why that widening is not a hole, and what WS11's entries have to say.** What WS11 imports is where each kind is
served from: a value type and an interface, with no read and no write of the credential state. The state stays where
it is — behind the service, inside `creds/`, denied to every run token by the rules (O1, D136) — and the one Cloud
read its reader makes is of this record's policy leaf, `config/policy/route/<slug>`, which a run's own token is
granted and `creds/` is not. A *guard* that holds one of those non-Go spellings to the constant here — WS3's for the
pin's field, WS6's for the module's, WS22's for the template's — imports this package and therefore needs a directory
the list admits: it lives in `internal/creds` or `internal/cli`, and not in the package whose file it reads.

**If the owner would rather not widen the boundary, the Go half moves rather than the list.** The record's types
would then live in a package the run already imports, and that is a contract change on the plan's §7 (R-route-home,
which reaches WS11, WS14 and WS22), raised before WS11 is written — not something a consuming task decides on its
own.

## What each side may rely on

**The member's pin (WS3, WS9).** `WorkspacePinAddressField` is the pin field that carries the address, spelled
`address`. WS3 writes it in `fugaro workspace add` and refuses an address that is not `https` with no query; WS9 reads
it to build the record. A pin without it is valid and means "this member takes no workspace credential", so a pin an
earlier release wrote converges with no edit, and a member with no address publishes nothing and removes a record it
published before.

**The member's publisher (WS9).** It may rely on `RouteRecord` for the bytes and on `ErrRouteRecord` for what a run
will refuse, so a record it publishes is one WS11 reads. It derives the holder per repository and kind from the
member's own bundle and policy (the contract carries no such rule), answers `HolderProject` for a kind the project's
own copy serves (D2973) — which `RouteRecord` leaves out of the bytes, so the field may hold the constant and the
record is the same as one that named no kind at all — and reports that a workspace credential of that kind is not
used. It publishes one record per repository, at `RoutePath(slug)`, and it cannot name the workspace's entries — a
member cannot read them (F38).

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

**The run (WS11, and the beat in WS13).** It reads the record once at bootstrap for its own repository — the node
itself, off `RoutePath`, with `DecodeRoute`, and not through `RouteReader`, whose only implementer is the stub — and
keeps the answer: a model call goes to the service the model route names and never to the other, exhausted or not
(D2982, D2989). The address it calls is the one `Route.Address` carries, never one from the run's environment (R3,
F32) — an address a launcher supplies is an address a launcher can move, and the run mints an identity token for
whatever address it reads. It builds the second upstream, and starts the beat, on `Holder` and not on `Address`: a
route that moves no kind is one the run never calls, so it adds no upstream and keeps nothing warm. It is the one
side of this contract that reads it from run-side code, which is what the
boundary section above is about: its directories join `credsImporters` in its own change, with their reason.
`WorkspaceBeatSetting` is the setting that turns the keep-warm beat off; it is on by default, and its key is an
`agent.` one because the beat is the run's.

**`fugaro doctor` (WS14).** It reads the same record and reports it, and it may rely on `Holder` for what to say about
each kind. Flagging a route the bundle no longer matches is WS9's derivation, not a second one.

**The live checks (WS20) and the rule (WS22).** They name `RoutePath` and the record's members; WS22 spells the rule
over that path and moves `RulesVersion` with it.

## What no side may rely on

- **A stub's answer.** `StubRoute` admitted nothing, moved nothing and read no record. Nothing it returned was a
  decision any side may act on, and I1 deleted the file rather than promoting it. The reader a run reads its route
  through is WS11's, over the member's own record.
- **A kind this code does not know moving anywhere.** `Kinds` is the closed vocabulary of this contract and `Holder`
  has no third value. A side that added a kind moves it in a new version of the record, in this package, and not by
  spelling it into a record and hoping.
- **An address as a promise.** Every published record carries one, so its presence says nothing about what the
  workspace serves: a record may name every kind the project's and never be called. A run builds its second upstream
  and starts its beat on `Holder`, never on `Address`.
- **The record as a decision about the workspace.** It says where each kind is *served from*; it says nothing about
  whether the workspace admits the repository or the member. That verdict is the workspace's own (R5, D1298) and is
  made again where the call arrives.
- **A second implementation of any of this.** The names, the path, the vocabulary and the two functions are here, and
  a side that respelled one — the environment name in Terraform *and* in Go, the escape of a slug in the writer *and*
  in the reader — would be two answers that drift. WS3, WS6 and WS9 call these; they do not keep their own copy.
- **An import of this package from a run-side package the allow-list has not admitted.** The list is the control,
  and importing around it — the record's shapes copied into the run, or a widening nobody reviewed — is what would
  turn the boundary section above into a claim. The run's reader imports the package, and the directories are
  admitted in WS11's own change, with their reasons.
- **A credential in the record.** There is no token, no secret value and no credential identifier here, and there
  never will be: the record is a document a member's admins write and a member's runs read, and a field carrying a
  value would put a credential in it.
- **A refusal as retryable or as "the project's".** A refused record is a run that halts and names the fix (WS11),
  not one that carries on with today's answer — and a read the rules *deny* is not a refused record: it is the state
  the paragraph above the record reads as "no record", and it expires with WS22's rule.

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
