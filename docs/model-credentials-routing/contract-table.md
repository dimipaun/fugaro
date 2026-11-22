# The table contract (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). What it states is **R1** (every
model a run calls is served by the credentials service, on the source the project's table names),
**R2** (the run's gateway holds no key and no table), **R4** (the chain's own order replaces the
fixed tier order), **R20** (a source's admission is per repository, and a subscription account keeps
its own `allow_data_to`), **R21** (the table is published in the policy under one new top-level key),
**R22** (a model no entry matches is refused), **R24** (the answer's new members reach only a caller
that said it understands them) and **R25** (a release-behind reader and a release-behind run both
keep working), with **D3172** (the most specific pattern wins), **D3175** (a moved model's spelling
comes from a checked-in map), **D3180** (a source with nothing usable is skipped) and **D3182** (a
key, a provider key and Vertex are admitted per repository) behind the rules
([reconciliation.md](reconciliation.md)). The Go is `internal/routing/table.go`, the wire is
`internal/credsvc/chain_wire.go`, the gateway's half is `internal/gateway/chain_names.go`, and the
stubs the contract ships are `internal/routing/resolve_stub.go`, `internal/pricing/spelling_stub.go`
and `internal/credsvc/chain_stub.go`.

**RT1**–**RT5** (`routing-table`), **CS1**–**CS9** (`chain-serving`), **MG1**–**MG4** (`migration`)
and **VS1** are written against this document; **I1** deletes the stubs and wires the real sides, and
**X4** removes the release-1 bridge. A side that finds this contract wrong or incomplete raises a
change request ([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than
working around it; the changes already anticipated are [below](#the-changes-anticipated).

## What a routed call is

A model call is the call it is today — a Messages request to a model — and what changes is where its
credential comes from. Instead of the job mounting a token and the agent presenting it, the call
reaches the credentials service, the service resolves the model to the project's table, and it
serves the call from the first source of that entry's chain that may serve it (R1, R4).

| What the call carries | Where | Rule |
| --- | --- | --- |
| The model it is for | the request's **body**, its `model` member | the only place the model is read. There is no request header naming a model and there may never be one: a header is written by the caller and read by every hop, while the body's member is the call the agent actually made, which is the only thing the table may be resolved against (CS1 refuses a body that names none) |
| The stage it is for | the `X-Fugaro-Stage` header | the gateway's current stage (`gateway.Stage.Name`), held to `ValidStageName` on both sides. It decides the SHAPE of the answer and nothing else — the run is placed from its verified identity as it always was — which is why a value the service does not accept as a stage name is answered in the pre-contract shape rather than refused |
| Nothing else | — | no credential's value, no table, no repository: the repository is the one the run's verified identity names, and the table is the published policy's |

The request is otherwise unchanged, which is what lets the service stand in front of an agent that
knows nothing about any of this.

## The answer, and the rule that guards it

The service's answer carries three new members. They are **response headers**, and every one of them
is written only to a caller that sent the stage header (R24, F57).

| Member | Header | What it is |
| --- | --- | --- |
| `source` | `X-Fugaro-Source` | the JSON of a `routing.SourceName`: the kind of source that served, and the name of the one that kind names (`pool` and `provider` name one; `launcher-pool` names the pool actually used; `anthropic` and `vertex` are a single credential of the project and name nothing) |
| `fallback` | `X-Fugaro-Fallback` | `1` when this call was served by a source that is not the chain's first, `0` when it was. Both values are written, so no reader has to decide what an absent member means |
| `model` | `X-Fugaro-Model` | the canonical id of the model that served, which the gateway prices the call by |

**Why the guard, and what it is.** A release-behind gateway is one built before this contract: it
drops the answer headers it knows, passes an unknown one through to the agent, and decodes a refusal
body strictly — so a member it has never heard of is either the agent reading the name of a
credential source or the gateway failing on its own service's answer. Sending it a `source` is
therefore not harmless. The stage header is how a caller says "I am this contract's gateway, answer
me in full"; a call that does not send it meets **exactly** the answer it meets today, byte for
byte, and that is what `SentStage` decides in one place for the whole contract. The rule is about the
CALLER and not about the member, so every member this contract gains later — including any new
refusal-body member CS1 adds — is covered by it without a second reading of the header.

**What the guard is not: a boundary between the gateway and the agent.** The service reads the stage
header; it cannot tell a gateway's own from one the agent wrote, because the request-side rule that
makes the header the gateway's own is CS2's. So a gateway built before CS2 — which is exactly the
release-behind gateway this rule keeps working — forwards the agent's own `X-Fugaro-Stage` like any
other header, the service answers it in full, and the gateway passes the three answer headers on to
the agent. An agent that sends the header therefore reads which credential source served, whether the
call fell back and which model served, for every call it makes — and D143 is the rule that says it
must not. Two things bound that and neither removes it: the members carry a name and never a value (a
pool's name, a provider's name, the served model's canonical id), and the window is the interval
between a release-behind gateway meeting a service of this contract and that run's image reaching
CS2. `fugaro update` is the whole of the fix, because CS2's gateway sets the header itself and drops
the caller's copy, so a gateway of this contract is not one the guard has to protect. Until then the
guard buys the ordinary case and only that: a release-behind gateway sends no stage header — none of
them does, unless an agent sends it one — and so meets the answer it meets today. No later task may
assume the disclosure is closed for a run whose gateway predates CS2.

`model` is not always the model the agent asked for: a source entry may name a model (a written
downgrade) and a route's own spelling may differ from the canonical one (CS3). Pricing by the
requested model would charge the wrong model's price, so the served model travels on the answer. The
agent is shown the model it asked for and never this one.

**The agent never sees any of the three through a gateway of this contract.** They are the run's own
accounting, they join the explicit list in `internal/gateway/forward.go`'s `dropResponseHeaders`
(CS2), and they are read into the step's record hook below. The qualifier is "a gateway of this
contract" and not "the gateway" because the drop list is CS2's and a gateway built before CS2 has
neither it nor the request-side rule that makes the stage header the gateway's own — so an agent that
sends the header to one of those reads all three, which *What the guard is not* states in full and
does not soften. CS2 closes it for the gateway it builds; the image update closes it for the rest.

## The gateway's half

The gateway cannot import `internal/credsvc` — the service imports the gateway, so the dependency
cannot run the other way — and the two halves of this contract split accordingly.

- The header **names** are mirrored, in `internal/gateway/chainwire.go` (CS2), and a test there holds
  the mirror to the wire and holds every name of it apart from the names the wire already carried.
  This is the shape the provider wire already uses (`F26`).
- The **shapes** are not mirrored. `routing.SourceName` is declared once, in `internal/routing`, and
  imported by both sides, because two declarations of "which source served this call" is exactly the
  drift this contract exists to prevent. No cycle stands in the way: `internal/routing` depends on
  neither side.
- The **option** is `gateway.ChainServed`, a named bool CS2 adds to `Options`: true when the
  credentials service serves this run's calls, so the gateway sends the stage on every call, reads
  the answer's three members into the hook, and holds no key. It is false in the core and for a
  repository that has not migrated, which is what lets the migration land one repository at a time.
- The **hook** is `gateway.ChainAnswerFunc func(ChainAnswer)`, where `ChainAnswer` is the source, the
  fallback flag and the served model of ONE call. A step's record reads it (CS8): it folds the flag
  over the stage's calls and takes the last call's source. The gateway keeps no per-stage figure — it
  does not know where a stage begins or ends — and a nil hook is not an error.
- **The stage header is the gateway's own.** The gateway sets it from the run's current stage on
  every call, and the caller's copy is dropped before it is set — the same rule, in the same place,
  as `HeaderProvider`: `internal/gateway/service.go`'s `serviceUpstream` clones the caller's request
  headers and deletes the names the gateway owns before setting its own, and the stage header joins
  that list (CS2). The model path drops nothing by prefix — unlike the git paths' forwarding — so a
  header that is not on the list reaches the service exactly as the agent wrote it, which is why
  this is a requirement and not a tidy-up: the R24 guard is worth precisely what it costs an agent
  to pass, and an agent that can send `X-Fugaro-Stage` makes the service answer in full and reads
  the source, the fallback and the served model of its own calls. **The drop ships with the guard, and
  it closes the disclosure for the gateway CS2 builds — it does not reach a gateway built before
  CS2, which is the gateway the guard exists to keep working** (*What the guard is not*). So on a
  release-behind gateway the guard is a compatibility rule and not a boundary: a call that sends no
  stage header meets today's answer, and a call an agent gave a stage header is answered in full and
  read out through that gateway's own pass-through of an unknown answer header. Nothing in this
  contract, and no later task, may assume that window is closed; the image update is what ends it.
  CS2's mirror test carries the request side with the names: the test that pins the wire's names pins
  the rule that the gateway's own headers are its own.

## The shapes, and what each reader may rely on

`internal/routing` holds the types and nothing that computes over them: the resolver is RT1's, the
table's checks are RT3's, the migration is MG1's, and the pool's own members are C2's. The one thing
that looks like logic there, `ValidKind`, is the closed set of kinds stating itself, so that the
decoder and the checks that must both refuse a sixth kind read one list.

| Shape | Members | Who reads it |
| --- | --- | --- |
| `Table` | a list of `Entry`, in the order the project wrote them | the resolver, the checks, the migration, the service |
| `Entry` | `match` (a model or a pattern), `chain` (the sources, first usable one wins) | the same |
| `Source` | `kind`; `pool` or `provider` names which credential of that kind, one member per kind; `model` names the model this source serves when it is not the one the call asked for; `admit` for the kinds that are a single credential of the project | the service's walk, the checks |
| `SourceName` | `kind`, `name` (always written, empty for the two kinds that name nothing) | the answer, the record, the registry entry, the console |

The five kinds are `launcher-pool`, `pool`, `anthropic`, `provider` and `vertex`, and they are a
closed set: a sixth is a change to `internal/routing/table.go` and to every reader's switch.

**Which model is sent, and how it is written.** `model` is a written downgrade and **never a
spelling**: it names the model this source serves when that is not the model the call asked for,
which spec.md requires to be explicit ("then Sonnet via OpenRouter") because nothing is downgraded
silently. It may be set on a source of any kind, and it is orthogonal to `pool` and `provider` — the
one names the credential, the other the model. Which model is sent is `model` when the source sets it
and the requested model otherwise; how **that** model is written on the source is the checked-in
map's answer alone (`pricing.SpellingFor`, D3175), per kind and per model. The precedence is therefore
total and needs no tie-break: the map is the only authority for a spelling, and no member of the table
is ever read as one. That is what keeps CS1's walk, CS4's Vertex translation and RT3's checks from
disagreeing about the model id a live call carries — a source whose model the map does not spell is
refused at validation (RT3) rather than sent as something nobody measured.

**The admissions.** `admit` is a list of repositories, per SOURCE and not per kind, because the same
provider key may be admitted to one repository and not another (D3182). It is carried on the kinds
that are a single credential of the project — `anthropic`, `provider`, `vertex`. An empty list admits
**nobody**: the fail-closed direction, and the same rule a Claude account's own `allow_data_to`
already has. A source with no repository admitted is inert rather than an error, so a project can
publish the shape before it decides the admissions.

A `pool` carries no `admit` list: its admission is its members' own. A subscription account keeps its
`allow_data_to`, and a member the repository is not admitted to is skipped (R20), which is what keeps
two repositories that differ today — one on a subscription, one on the project's key — where they
are (F41). RT2's decoder refuses `admit` on `launcher-pool` and `pool` rather than storing something
nothing reads.

**A source with nothing usable is skipped, not an error** (D3180): an empty launcher's pool, an
exhausted source, a source the repository is not admitted to. The chain is walked to its end, and a
chain with nothing usable answers exhausted with the earliest reset. A model no entry matches is
`routing.ErrNoEntry` and is refused — never served by "whatever credential is around", which is the
behaviour the table replaces (R22).

## The formats this contract fixes

Every one of these is a shape a reader outside the same binary consumes, so a member is **added**,
never retyped or reused, and the existing members keep their meaning.

**The run record** (CS8, `schemas/result.schema.json`):

| Member | Type | Meaning |
| --- | --- | --- |
| `stages[].source` | the `SourceName` object | which source served the stage: kind and name, never a value |
| `stages[].fallback` | bool | true when ANY call of the stage was served by a source that is not the chain's first |
| `cost.fallback_usd` | number | the real money spent on fallback sources — a subscription's list price is notional and is not counted here |

**The live registry entry** (CS9, `internal/budget`): gains `source` and `fallback`, the same two
facts. For a run the table serves, `auth` is written as nothing and the views print `-`, as D3070
settled — `auth` keeps its member and its meaning, and a reader that predates the member sees no
change to it.

**`fugaro watch --json`** (CS9): `runs[].source` carries the source of the running stage, as a
string — the kind, and the name after a colon when the kind has one (`pool:team`, `anthropic`) — and
`-` when the run is not served by a table, exactly as `stage`, `verify` and `mode` already write `-`
for "nothing to say".

**The published policy** (RT4): ONE new top-level key, `model_routing`, an object with two members —
`entries`, which is the `Table` (each source carrying its own `admit` list, which is where the
admissions live), and `pools`, whose shape is C2's. Nothing else moves: `craft_pools`, `credentials`
and every other existing block are left **byte for byte** as a release-behind reader decodes them.

That last rule is the whole of the compatibility story (R21, F49). A release-behind reader **tolerates
an unknown top-level key** and **refuses an unknown member of a block it knows**, so a new fact must
arrive as a new top-level key and never as a member added to an existing block. `model_routing` is
that key, and its contents are free to grow inside it: a member added to `entries` or to `pools` is
read only by a release that knows the key exists, and one that does not never looks inside.

**The one contract the config does not share.** The installation's own `model_routing:` block (RT2) is
the `Table` itself — a list of entries, which is what a person writing it expects to see — and lives
in `internal/localcfg`, not in a repository's `fugaro.yaml` and not in the project layer. A repository
file or a layer that names the key is an unknown-field error, because a repository may not choose its
own routing: the project's table is the project's.

## The release-1 bridge

`credsvc.LegacyBridge` is the interface the release-1 bridge implements (MG3):

```go
type LegacyBridge interface {
	OldSources(repository string) ([]routing.Source, bool)
}
```

It answers the sources the old record `creds/repoauth/<slug>` stands for, and whether the repository
has one at all — two answers, because "no record" and "a record naming nothing" are different
situations and the walk treats them differently. It answers `routing.Source` values rather than a
mode and an account name, because the point of the bridge is that an older run's call lands on the
sources that repository's runs go out on today.

**What the record may hold, and what it may not** (D3196, R25). It is derived from the table on every
write of either — `update`, `routing set`, an admission change — so it never gains a source the table
does not have. It is **not** a subset of the table, and "never wider than the table" is the wrong
rule to write code by: an entry the table's checks REFUSE is in no table at all and is left on the
record, which is exactly what D3196 decides — a Claude model through a provider before Check 50 is
recorded as passed, a Vertex model with no checked-in spelling. Dropping such an entry would move a
run the migration's whole promise is not to move, so the record is wider than the table by precisely
the entries the checks refused, and never wider than the repository's own pre-migration access. What
keeps a call off the old path is therefore **not** the record's width but the walk's rule: it reads
the table first and consults the record only for a run whose image predates it. The switch is the
run's own image, and nothing a call sends may choose the record over the table — least of all the
stage header, since the record is the wider of the two. A call of this release reaches the old path
never, whatever header it sends. **X4** removes the bridge and this seam together.

## Every refusal writes one audit line

This contract adds refusals and changes none of the credentials service's rules about them. Once the
caller's identity is verified, every refusal writes **exactly one** audit line — no path writes zero
and none writes two — and no credential value, token or key appears in an audit line, a log line or
an error: a refusal names the field, never its content
(`.claude/rules/credentials-service.md`).

The refusals this contract's own rules produce are: a body that names no model; a model no entry
matches (`ErrNoEntry`, R22); a source the repository is not admitted to; a chain with nothing usable,
which answers exhausted with the earliest reset; and a model with no mapped spelling for a source
(D3175). The last one is why `pricing.SpellingFor` fails closed: a source with no spelling is a source
that may not serve that model, and a spelling derived by a case fold or a separator swap would be a
call sent to a model nobody measured.

## The stubs, and who removes them

Three stubs ship with this contract, so that every side can be written and compiled before its
implementation exists. Each **returns its shape and does nothing else** — it reads no table, no
record, no map and no policy, and two different arguments get the same answer.

| Stub | Answers | Filled by | Deleted by |
| --- | --- | --- | --- |
| `internal/routing/resolve_stub.go` | the table's FIRST entry, for every model, and `ErrNoEntry` for an empty table | RT1 (`resolve.go`) | I1 |
| `internal/pricing/spelling_stub.go` | "no spelling for that", for everything | CS3 (`canonical.go`) | I1, which moves the `SpellingFunc` declaration and the `SpellingFor` variable into `canonical.go` with the real assignment |
| `internal/credsvc/chain_stub.go` | "no old record", for every repository | MG3 (`legacy_auth.go`) | X4, with the bridge |

The first is deliberately the WRONG answer for a non-empty table, and that is RT1's red step: a stub
that guessed the most specific match would let `TestResolveMostSpecificWins` pass before RT1 existed.
The other two are the fail-closed answers — "no spelling" sends no call anywhere, and "no old record"
refuses rather than serving whatever the record happened to hold — so a side written against this
contract runs its own tests against a real signature and no call can pass on something nothing read.

Each stub's assignment stands only while nothing else has made one (`if X == nil { X = stub }`), so
the real side wins whatever its file is called: the toolchain presents a package's files in name
order, and an unguarded `init` would put a stub back over a real implementation with nothing failing
to compile. `pricing.SpellingFunc` takes the kind as a plain `string` rather than `routing.Kind`, and
that is not sloppiness: `internal/routing`'s checks read `SpellingFor`, so a parameter of that type
would make the two packages import each other.

## What the tests pin

`TestTableShapesAreStable` (`internal/routing`) pins the JSON of every shape — the table a project
publishes, the answer's `SourceName`, and the five kinds' spellings — and not the Go names, because a
member renamed in Go is a compile error at every call site while a member renamed in JSON is silent.

`TestChainWireNames` (`internal/credsvc`) pins the wire's four header names and the fallback member's
two values, holds each of them apart from every name the wire already carried, and pins the two rules:
that no request header carries a model, and that the answer's members reach only a caller that sent
the stage header. The stub's own satisfaction of `LegacyBridge` is a compile-time check in
`chain_stub.go`, which goes with it — a test of what a stub answers is a test that dies with the stub.

## The changes anticipated

**R-table-shape** (raised by C2, reaching RT2, RT4 and CS1): if the pools' own shape turns out not to
fit as `model_routing.pools` — if a pool needs to be declared in more than one place, or the
designation record needs a member the policy cannot carry. This document fixes the key's name and
the fact that the table is its `entries` member, and leaves the pools' contents to C2; a pools shape
that could not be a member of that object would change this document and RT4's writer together, in
the change request's own sweep.

**R-spelling-kind** (raised by CS3, reaching RT3): if `SpellingFor` turns out to need more than the
kind and the model to answer — a location, a project, a route — the signature changes here and in
`canonical.go` together. It takes the kind and the model because that is all the table says about a
source, and a source that needed more would be a source the table cannot describe.
