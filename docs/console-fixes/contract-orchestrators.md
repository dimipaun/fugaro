# The orchestrator contract (C-ORCH)

**Status: written with the plan's task C-ORCH. The names, the document's shape and the readings below are frozen;
everything else in [implementation-plan.md](implementation-plan.md) is still unapproved.**

This is the contract [implementation-plan.md](implementation-plan.md) asks C-ORCH for, and it is what the tasks that
follow read instead of each other's code. The names live in
[`internal/orchestrator/naming.go`](../../internal/orchestrator/naming.go), the document's shape in
[`internal/orchestrator/mapdoc.go`](../../internal/orchestrator/mapdoc.go), the answer the command gives until the
builder exists in [`internal/orchestrator/map_stub.go`](../../internal/orchestrator/map_stub.go), and the runnable form
of the same claim is [`internal/orchestrator/naming_test.go`](../../internal/orchestrator/naming_test.go).

**What this contract is not.** It is the shape and the readings, not the behaviour. The builder that reads a project's
leases, its shards and the coordinator's heartbeat is CG6's (`ee/orchestrator/mapbuild.go`), the command that prints
the document is CG6's (`ee/orchestrator/cli/map.go`), the console's decoder and bridge method are CG7a's, and the
switch of the id checks this package's own paths are built through is CG1a's. Each of those reads this document and
none of them reads another's code.

## The orchestrator id

An orchestrator id is the name of one orchestrator of one project. The default orchestrator's id is **the project's
name**; a named one's is **`<project>~<name>`** (D3207, D3218):

| Part | Rule | Where it is written |
| --- | --- | --- |
| project | `config.ProjectNameRE`: 1 to 40 characters of `a-z`, `0-9` and `-`, starting and ending with a letter or a digit | `internal/config/project.go` |
| separator | `~` (`orchestrator.NameSeparator`) | `naming.go` |
| name | `^[a-z0-9][a-z0-9-]{0,39}$`: 1 to 40 characters of `a-z`, `0-9` and `-`, starting with a letter or a digit | `naming.go` |

The longest id the rule admits is `orchestrator.MaxOrchestratorIDLength` (81: 40 + `~` + 40), and
`MaxOrchestratorNameLength` is 40. **There is no cap on how many orchestrators a project has** (D3207): a project may
have the default and as many named ones as it likes.

Three readings are worth stating, because each is a place two readers could differ:

- **The project half is the project's own name rule, not a copy of it.** The default orchestrator's id *is* the
  project's name, so an id must admit exactly the names a project may carry — no more, or a project could name an
  orchestrator that no project can be; no less, or a project could not name its own orchestrator. `naming.go` holds
  `config.ProjectNameRE` itself rather than a second pattern that agrees with it today.
- **The name half admits a trailing `-` where a project name does not.** That asymmetry is D3207's: a slug may end in a
  hyphen, a project's name may not.
- **An id is a key as written.** None of its characters is one the database escapes (`internal/budget.Key`), so the
  node a writer names and the node O2's rules grant are the same string.

`ParseOrchestratorID(id)` returns `(project, name, ok)`; the name is `""` for the default orchestrator. `ValidOrchestratorID(id)`
is the rule, `DefaultOrchestratorID(project)` and `NamedOrchestratorID(project, name)` build the two spellings and
refuse a half the rule refuses. **This rule is separate from `agentRE`** (F36): a work item's id is
`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` — case-sensitive, `_` and no `~` — and widening it to admit `~` would let a work
item's id and an orchestrator's be the same string in one keyspace.

## The graph scope

The graph's scope grammar is this rule and not a second one (F40). `orchestrator:<id>` and `local:<id>` carry an
orchestrator id, and `ValidScopeID(id)` is `ValidOrchestratorID(id)`, so:

- CG8 widens `ee/graph/key.go`'s `scopeIDPattern` and `ParseScope` to read a named id (today's
  `^[a-z0-9][a-z0-9-]{0,39}$` refuses `~`);
- CG7a widens the console's `GRAPH_SCOPE_RE` and `MAX_GRAPH_SCOPE_LENGTH` (`console/src/main/ipc.ts`) to the same
  grammar, behind the version floor below.

Both ends read **one** rule, so a scope the console admits is a scope the command reads, and the two cannot drift.
`MAX_GRAPH_SCOPE_LENGTH` becomes `'orchestrator:'.length + MaxOrchestratorIDLength`.

## The driver node

`orchestrator/<id>/driver` names who is driving the orchestrator: the principal of the credential that holds the
lease (D3218 as amended, F18). It is a **node of its own** and not a field, because the state and the lease are decoded
strictly and gain no field (F35) — a field added to either would be a document the previous release refuses, and D3218
as amended is exactly the decision not to do that.

| Field | Kind | Reading |
| --- | --- | --- |
| `principal` | string | the credential's principal, in the address spelling the rest of the tree uses for a person: a person's account or a service account's address. Never empty in a node this release writes. |
| `at` | RFC 3339 | the **server** time the holder wrote the node (`rtdb.ServerNow`), so a reader can tell a driver written before a takeover from one written after it without reading the lease beside it. |

`DecodeDriver` is strict, like `DecodeState` and the lease's own reader: an unknown field, or a node naming no
principal, is a refusal rather than a driver of nobody. `PathDriver(id)` is the path, a sibling of `PathState` and
`PathLease`. **CG1a** is the task that makes the lease holder write it.

## An item reference

`<orchestrator>/<item>` is how one orchestrator's item is named from outside it (D3208), which is what a `depends_on`
that crosses orchestrators carries. `ParseItemRef(ref)` returns `ItemRef{Orchestrator, Item}` and `ItemRef.String()`
writes it back. The split is at the **first** `/`: an orchestrator id carries none, so everything after it is the item
— the `<plan>/<task>` a plan item is keyed by included, which is why a reference may carry two separators
(`fugaro~night/docs.plan/T-1`).

The item half is one rule shared with the claim below: a work item id (`idRE`, the shape a state document's own items
carry) **or** a plan task (`task.PlanTaskProblem`). The two things that name an item across orchestrators agree on what
an item is, so an item a reference admits is an item a claim can be taken on. Note that an item is *not* an
orchestrator id: `fugaro/Fugaro` is a valid reference — a work item id is case-sensitive — where `Fugaro/G1` is not.

## The claim path

`ClaimPath(ref)` takes an `ItemRef` and is one node under `orchestrator/~claims`, keyed by the **kind of item** —
because a plan task and a work item id are not the same kind of identity:

| Item | Path | Why |
| --- | --- | --- |
| a plan task (`<plan>/<task>`) | `orchestrator/~claims/<plan task>` | its identity is the plan's folder and the task, and the folder is the one the import read (D2704) — so two orchestrators of one project that imported the same plan mean **the same task** when they name it. That is what makes the claim **project-wide** (D3209): the ETag decides which of them launches the task, and the others skip it and may depend on it. |
| a work item id | `orchestrator/~claims/<orchestrator>/<item>` | a work item id is minted **inside one node** — a backlog node mints `<day>-<n>` off its own work list (`backlogItemID`, D26) — so the same id in two orchestrators is **two different items**. Keying it project-wide would let one orchestrator's backlog item block another's, and the two would then read as one item. |

The item half is escaped the way every key of this tree is (`budget.Key`), so a plan task's `/` and `.` reach the
database as **one key** rather than as a path; an orchestrator id needs no escaping and none is left to chance.
Neither shape can take the other's key: a plan task's key is **one** segment under the claims node, with its separator
escaped, and a work item's is **two**, under the orchestrator that minted it — and a plan task's elements never carry
a `~` (`task.PlanTaskElemRE`), so no plan task can spell an orchestrator's own segment.

`~claims` (`orchestrator.ClaimsKey`, with `ClaimsRoot` the node itself) is a child of `orchestrator/` that **no
orchestrator id can take**: an id begins with a lower-case letter or a digit. That is the whole of what keeps the
claims apart from the orchestrators in one subtree — a reader listing the subtree's ids skips it (CG1a's
`TestNodeIDsSkipsClaims`), and no name can ever collide with it. **CG4** is the task that writes a claim.

## The map document

`fugaro orchestrator map --json` prints one document: the project's **active** orchestrators, the connections between
them, and the coordinator's health (D3221). It is the read the console's Coordinator tab draws.

| Field | Kind | Reading |
| --- | --- | --- |
| `version` | int | `MapDocVersion` (1). A field never changes meaning under it: a new one is additive, and a removed or retyped one is a new version. |
| `project` | string | the project whose orchestrators these are. Every node belongs to it; one map is one project. |
| `generated_at` | RFC 3339 | when the document was assembled, on the reader's clock. It is a clock and not an as-of — how old one node is, is `lease_age_seconds`. |
| `nodes` | array | the active orchestrators, and never `null`. Empty is a fact — no orchestrator of this project is being driven. |
| `nodes[].id` | string | the orchestrator id. |
| `nodes[].name` | string | the id's name half, `""` for the project's default orchestrator. The two cannot disagree: the document is refused when `name` is not `ParseOrchestratorID(id)`'s own name. |
| `nodes[].driver` | string | the principal the driver node names, `""` when that node is absent or unreadable. |
| `nodes[].active_tasks` | int | how many of the orchestrator's items are being worked, counted over its shards. |
| `nodes[].running_jobs` | int | how many runs the orchestrator has going. |
| `nodes[].lease_age_seconds` | number or `null` | how long ago the holder last beat, in seconds; `null` when the lease node carries no readable stamp. |
| `edges` | array | the connections between nodes, and never `null`. |
| `edges[].from`, `edges[].to` | string | orchestrator ids, **both nodes of this document**. A dependency points from the orchestrator that waits to the one it waits on. |
| `edges[].kind` | string | `dependency` (`MapEdgeDependency`) or `overlap` (`MapEdgeOverlap`). |
| `edges[].reason` | string | what the edge is, in words, for an overlap — the collision the coordinator stored, or the pull request two orchestrators share (D3211, D3222). `""` for a dependency, which the two ids already say. |
| `health.sweep` | bool | whether the coordinator's sweep is on for this project. |
| `health.started_at`, `health.ok_at`, `health.at` | RFC 3339 or `null` | when the last sweep started, when one last finished reading everything, and the server time the heartbeat was written; all `null` when there is no heartbeat to read (CG5). |
| `health.note` | string | why the health is what it is, `""` when there is nothing to say. |

`EncodeMapDoc` and `DecodeMapDoc` are the two doors, and both apply `ValidateMapDoc`, so the document a writer
publishes and the document a reader accepts are held to one rule: the version, the project's name, each node's id
against the project and its name against its id, two nodes with one id, negative counts, an edge kind that is neither
of the two, an edge joining an orchestrator to itself, an edge naming an orchestrator the document does not draw, a
heartbeat carried with the sweep off, and the four fields the contract always writes — `nodes`, `edges`, `health` and
`generated_at` — carried neither absent nor `null` (`DecodeMapDoc` checks the raw value for those two, because a
struct decode reads either as a zero value: a nil list, a zero health, a zero time). `Sanitise` puts the free text
(`driver`, `reason`, `note`) through `safetext.Field`; the ids, the names and the kinds are **identity and not prose**
— a sanitised id would name another orchestrator — so they are left as the builder wrote them and `ValidateMapDoc` is
what refuses one that is not an id.

**The read.** `orchestrator map --json` is registered from `ee/` the way `ee/graph/cli` registers `graph` (CG6), so
this release's core CLI has no such command: `BuildMap` in `map_stub.go` is the answer the command gives until I-ORCH
points it at the builder. `BuildMap(project, now)` returns the empty document — no node, no edge, and a health that
says the sweep is off — which is what a build with no builder can honestly say.

## The read's surface in the console

| Piece | Name | Where |
| --- | --- | --- |
| spawn row | `orchestrator.map`, words `['orchestrator', 'map', '--json']`, `takesProject: true`, no argument | `console/src/main/spawn.ts` |
| operation | `orchestrator.map` | `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS`) |
| bridge method | **`orchestratorMap`** | `console/src/source/cli.ts` |
| decoder | a strict reader of the table above | `console/src/shared/schema.ts` |
| version floor | the CLI minor that first carries the command | `console/src/shared/version.ts` |

All five are **CG7a**'s, and the floor is the one thing this document deliberately does not number: the floor is the
release that first ships `orchestrator map --json` — the release this contract ships in — and a number written here
would have to be bumped by every release between the two. Below the floor the Coordinator tab says which CLI version it
needs rather than drawing a document it cannot read. The `GRAPH_SCOPE_RE` widening above sits behind the **same**
floor, because a console that admitted a named scope would spawn a child an older CLI refuses. CG7a also updates the
design's §5.1 table (`docs/desktop-console/spec.md`), and I-ORCH adds the document's section to
`console/docs/json-contract.md` and amends its §6.22 — an edge there may join two orchestrators (F17).

## What each side may rely on

These are readings, not conveniences: each is a case a producer and a consumer could otherwise read differently, and
each is a test in `naming_test.go`.

1. **`name` is the id's name half, and `""` for the default orchestrator.** A screen draws the project's name for a
   node whose `name` is empty; it must not draw an empty label, and it must not read `""` as "a named orchestrator
   whose name is blank", which the id rule cannot produce.
2. **`driver: ""` is "the driver node is absent or unreadable", never "nobody drives this".** A node is on the map
   because its lease is fresh — a credential is driving it — so an empty driver is a read that failed, and a screen
   says the driver is unknown rather than inventing one.
3. **`lease_age_seconds: null` is "no readable stamp", never `0`.** Zero would be a claim about the beat that the
   reader does not have.
4. **The two lists are always lists.** An empty `nodes` is "no orchestrator of this project is being driven", not an
   unreadable document; a reader that drew "unavailable" for `[]` would make a quiet project look broken.
5. **Every edge joins two nodes of the same document.** An orchestrator nobody drives is not a node, and neither is an
   edge to it — so a consumer must not look an endpoint up anywhere but `nodes`, and the builder must not draw an edge
   whose other end it left out.
6. **`health.sweep: false` means the map carries no overlap information at all**, and the three times are `null`. A
   reader must not draw the absence of dashed edges as "nothing overlaps": with no sweep nobody looked. `health.note`
   says so in words, and a screen shows it rather than an empty health line.
7. **A document this release does not model is refused, not carried.** The decode is strict and the version is checked
   exactly: an unknown field, a field of the wrong kind, and a version this release does not write are each a refusal,
   and the console draws the decoder's sentence rather than a partial map.
8. **The claim key is reserved, and a claim is keyed by the kind of item.** `~claims` is not an orchestrator id and no
   orchestrator can be named it, so a reader listing the subtree's ids may skip it and a writer may keep a claim there
   without colliding with any id. Under it, a plan task is one project-wide node and a work item id is one node per
   orchestrator: two orchestrators' `20261122-01` are two items and must not share a claim, while one plan task named
   by two orchestrators is one item and must.
9. **An item reference splits at the first `/`.** The item may carry `/` of its own, so a consumer that split at the
   last one would read a plan item's reference as a different item.
10. **The scope grammar and the id rule are one rule.** A scope a console admits is an id the tree accepts, and vice
    versa — neither end carries a second pattern that agrees today.

## What this contract does not yet do

The id rule is defined here and **applied by CG1a**. Until CG1a lands, this package's own paths and readers still hold
named ids to `agentRE`: `checkAgentID` (`store.go`) and `NodeIDs` (`list.go`) — which is why `PathDriver` is pinned
here against the default id and not against `fugaro~night`. The lease's **holder** is not one of them: `lease.go` holds
the holder to `idRE` (`internal/orchestrator/validate.go`), the rule a work item's id is held to, on the way in
(`NewLease`) and on the way back in (the read that decides a claim). A holder is a run id, which both rules admit, so
the two agree on every holder a run writes; `TestLeaseHolderIsHeldToTheIDRule` pins which of them the lease reads.

One thing the plan's CG1a does not list, and must touch all the same: `ValidateState` checks `s.AgentID` with
`agentRE` too (`internal/orchestrator/validate.go`), and `DecodeState` calls it. A named orchestrator's *state
document* is therefore refused by the decoder until that check moves to this rule as well, however the id's path and
the lease behave — so CG1a's file list needs `validate.go` beside `store.go` and `list.go`.
