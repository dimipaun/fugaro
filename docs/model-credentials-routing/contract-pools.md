# The pools contract (C2)

Task **C2** of [implementation-plan.md](implementation-plan.md). What it fixes is the vocabulary of a
pool: the rule a pool is served by (**R5**, **D3177**), who a pool belongs to (**R7**, **D3179** as
amended, **D3181**, **D3183**, **D3191**), the optional threshold (**D3178**, **U1**), the record
that says which pool a launcher's runs are served from (**R26**), the two lookups the service calls
(**D3176**, **D3180**), the two members a grant gains (**R21**, **CS14**) and the document
`fugaro accounts pool ls --json` prints (**F19**). **F55** (a chain cannot span two services) is why
a personal pool lives where it does, **F51** and **F61** are the two things the shapes must not
pretend to be able to check, and **R9** is the one rule about who may put a token in a pool
([reconciliation.md](reconciliation.md), [spec.md](spec.md)). The Go is
`internal/routing/pools.go` (the pool's own shapes, the designation, the lookup and the listing) and
`internal/creds/slip_chain.go` (the grant's two new members and the picker's signature); the stubs
it ships are `internal/credsvc/launcherpool_stub.go` ("no pool designated") and
`internal/creds/poolrule_stub.go` (the pool's first member). The tests are
`internal/routing/pools_test.go` and `internal/creds/slip_chain_test.go`
(`TestPoolShapesAreStable` in each), which pin every name and the JSON of every shape — the pool's
own shapes in the first, the grant's two members and the picker's signature in the second, which is
the package that declares them and the only one the import boundary admits there
(`internal/creds/boundary_test.go`) — and `internal/creds/poolrule_stub_test.go` and
`internal/credsvc/launcherpool_stub_test.go`, which are the two stubs' own and go with them.

**PL1** (the config's members), **PL2** (the account commands and the JSON), **PL3** (the rule),
**PL4** (the launcher's pool), **PL5** (designating, and contributing to the shared pool), **PL6**
(a service account's pool), **PL8** (the threshold), **PL9** (a personal pool), **PL10** (usage and
headroom), **CS1** (the walk), **CS12** (`--pool`), **CS14** (the grant), **MG1**, **MG2** and
**MG5** (the migration), **VS2** (the console's pools screen) and **RT7** (doctor) are written
against this document; **I1** deletes the two stubs and wires the sides. A side that finds this
contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it.

## What the contract is

A pool is a set of credentials a call may be served from. The table names pools as sources of a
model's chain, a run's launcher designates the pool its own runs are served from, and the service
picks inside a pool by the pool's rule. This file is the handful of names the sides of that have to
agree on, and nothing that computes: no picker, no store, no policy read and no command lives in the
contract.

| Name | Value | What it is |
| --- | --- | --- |
| `routing.Rule` | `ordered`, `round-robin`, `failover` | how the service picks among a pool's usable members (R5) |
| `routing.PrincipalKind` | `person`, `service-account`, `project` | what a pool's principal is (D3181) |
| `routing.Threshold` | a number greater than 0 and at most 1 | the pool's optional usage threshold (D3178) |
| a pool's new config members | `rule`, `principal`, `threshold` | PL1 adds them to `config.Pool`; `owner` and `strategy` keep their meanings — an absent `rule` resolves to `strategy` — and `subscription_spread` is still decoded (R21) |
| a designation's members | `principal`, `project`, `pool` | one principal's pool for one project (R26) |
| a pool reference's members | `project`, `pool`, `rule`, `threshold`, `principal` | the pool a run may be served from, as the lookup answers it; `rule` is always resolved (never `""`) |
| a grant's new members | `sources`, `named_pool` | the sources the grant covers and the pool `--pool` named (CS14) |
| the listing's members | `project`, `pools`, `name`, `rule`, `principal`, `threshold`, `members`, `state`, `next_reset`, `usage`, `calls`, `tokens`, `headroom` | the JSON of `fugaro accounts pool ls --json` (PL2, PL10) |
| `model_routing` | the policy key C1 adds | what tells the service the installation is migrated (PL3) |

**Why the shapes live in `internal/routing`.** A pool's rule and principal are read by the config
that decodes them (`internal/config`, PL1), the selector that serves them (`internal/creds`, PL3),
the service that resolves them (`internal/credsvc`, PL4) and the commands that print them
(`internal/cli`, PL2, PL5, PL9), so one package has to hold them and every one of those can import
it. What that package must not do is import any of them back — a cycle is one line away, because
`internal/creds` already imports `internal/config` and `internal/routing`, and `internal/config` is
where a pool's own members are written — so `internal/routing/pools.go` imports no package of this
tree at all. The
grant's two members are the other side of the same line: they are members of a record the service
owns (`internal/creds/slips.go`), so they are declared in `internal/creds/slip_chain.go`, which is
the one file of the two C2 adds there that survives I1.

## A pool's rule (R5, D3177)

`rule` is a pool's how, and the whole vocabulary is `ordered`, `round-robin` and `failover`:

- **`ordered`** is the spec's fixed order: the first listed member that is usable, and no preference
  beyond the order the pool's owner wrote it in. It is new in this plan.
- **`round-robin`** steps through the usable members in the listed order, one run at a time, skipping
  the ones a mark or a cap holds. It keeps its meaning and is what `internal/creds`' selection
  already does within a tier.
- **`failover`** is the member used least recently — the one exhausted longest ago — with the
  round-robin counter breaking an exact tie. It keeps its meaning, and it is the rule a pool that names
  neither a rule nor a strategy is served by. A value that is not one of the three is refused rather
  than read as one of them.

**An absent `rule` resolves to the pool's own `strategy`** (PL1): `rule` is a NEW member (R21) and
`strategy` is still read and still means what it means, so a pool published with `strategy:
round-robin` and no `rule` is served round-robin: a pool moves because a rule arrived only where its
owner wrote one. `failover`
is the answer only where a pool names neither, which is `strategy`'s own default read one step
further. That resolution is one function, `routing.Rule.RuleOrDefault(strategy)`; whoever fills a
pool reference's `rule` reads the member through it, **so `PoolRef.Rule` always carries the resolved
rule and never `""`**, and the picker acts on one answer without reading the pool's configuration a
second time.

`D3177` retires `subscription_spread`: a pool of several subscription tokens serves all of them,
under every rule, and the switch no longer decides whether a second token may be used. The member is
still decoded (PL1) so that a policy published by a release behind still loads; nothing reads it to
decide anything.

## Who a pool belongs to (R7, D3179 as amended, D3181, D3183, D3191)

A pool's `principal` is a kind and an id:

- **`person`** — the id is the person's **Google subject**, the stable numeric account id (`sub`) the
  secret-name grammar and the intake are already keyed on. It is the same identity a slip records as
  its owner, which is what makes a person's pool resolvable from that person's own runs and nobody
  else's.
- **`service-account`** — the id is the account's own address, the one its runs' tokens carry. Such a
  run's pool is a project pool an admin fills (D3181); it never draws on a person's pool, except for
  the one case D3191 adds.
- **`project`** — the project itself: the shared pool, which every account the migration wrapped
  became (D3183) and which takes the tokens people contribute to it deliberately (D3194).

**The principal is the verified principal on the run's slip, and never display text** (R7): a
person's Google subject or a verified service account, not `requested_by` or `launched_by`, which a
caller writes and which no reader may trust as an identity. A forged requester therefore resolves to
its own pool or to none, never to somebody else's (PL4's `TestAForgedRequesterGetsNothing`). What
that does not close is a project's own database administrator, who can forge the record itself, as
for any project state: that risk is accepted and said in `SECURITY.md` (F51, R23).

**`owner` keeps its meaning and the principal is a new member** (R21). `owner` stays exactly what it
is today — a person's email address, display text an operator reads — and it decides nothing about
which pool a run is served from. The principal is the enforcing half, it is new, and it is a member
of the pool's own block rather than a second reading of `owner`.

**A personal pool's tokens live in the project's per-user containers** (D3179 as amended), not at the
workspace. The reason is F55: a chain cannot span two services, and the workspace's service and the
project's are two, so a model whose chain names a personal pool and a project source would need one
walk across both. In the project, one service walks the whole chain, a person's tokens sit in the
containers the project already keeps for them, and the cost is stated rather than softened: a person
stores a token once per project, and the project's database admin can forge a grant, as for any
project secret today. `workspace-credentials` is left unchanged by this.

## The threshold (D3178, U1)

`threshold` is a pool's optional number between 0 and 1: the fraction of a member's limit at which the
pool moves off that member, before the limit is hit rather than after it (PL8). The contract's bound
is greater than 0 and at most 1 — a threshold of 0 would move off every member always, which serves
nothing and is a written mistake rather than a rule, and 1 admits a threshold that fires exactly at
the limit. PL1's validation and PL8's picker read that one answer, so a value the config accepts is a
value the picker acts on.

It is **optional**, and its absence travels as an absent member (null in JSON, nil in Go): a pool that
names none does nothing beyond its rule, and no default is invented for it. What a threshold needs to
work is a utilisation figure the provider reported for the member (U1); where there is none the rule
does nothing and says so once in the audit line, and the figure is never guessed — which is also why
the listing's headroom is null rather than an estimate (PL10).

## The designation (R26, F51, F61)

A **designation** is one principal's statement about itself: the pool that principal's own runs are
served from, for one project. It is keyed by the principal — a person's Google subject, a service
account's address — and it holds that principal, the project and the pool's name.

It is written only through a route of the service that verifies the caller and refuses a pool the
caller does not own and a designation in another principal's name (PL4, PL5). **The database rules
cannot hold that** (F61): people reach the database by IAM, which bypasses the rules, and every
launcher holds the viewer role; only a route keyed on the verified subject can. Two consequences an
implementer must not get backwards:

- the route is the only writer that matters, and the store is otherwise closed;
- what the designation node holds is **display data**. Every launcher of the project can read the
  designations and the pool figures beside them, whether the rules admit them or not, so nothing
  there is ever read as an authorization input by anything but the service (and the orchestrator, for
  a resume, R26). The service reads the designation against the principal it verified on the run's
  slip, never against anything the job said about itself.

## "The launcher's pool" (D3176, D3180)

`LauncherPool(ctx, principal, named) (PoolRef, bool, error)` is the lookup the service calls, and it
answers which pool a run is served from for one project:

- `principal` is the **verified** principal on the run's slip (R7).
- `named` is the pool the launch asked for with `--pool`, or **empty** when the launch named none
  (D3176). The flag does not reorder a chain: it says which pool "the launcher's pool" resolves to for
  this run, and the implementation checks the launcher's entitlement to that name, so naming a pool
  never widens what a run may be served from. A resumed run keeps the pool its launch named (D3191).
- `found` false means **"no pool designated"**, and it is not an error: no designation, an empty
  pool, and a named pool the launcher is not entitled to all answer it, and the walk moves on to the
  chain's next source with the step flagged a fallback (D3180). A chain of only the launcher's pool,
  with no pool designated, is refused with the fix rather than served by nothing.
- An **error** means the store could not be read, and a caller must never read it as "none": that
  would serve a fallback source where a pool exists.

The `PoolRef` it answers carries the project, the pool's name, the rule and the threshold that pool
was written with, and the pool's own principal. The pool's principal is not necessarily the caller's:
a run may name a shared pool that belongs to the project, and a run that resumes a paused one draws
on the pool its original launch named (D3191). Who may be served from a pool is the lookup's decision
and never a property of the record.

The stub this contract ships (`internal/credsvc/launcherpool_stub.go`) answers `found` false for every
principal and every name, with no error: that is a project nothing has designated yet, which is
exactly the state a project that has not migrated is in, and it is why merging the sides alone moves
nothing.

## The grant's two new members (CS14)

A slip records one pool and the secrets a run may be served. It gains two members, under new names,
and `Pool` keeps its meaning and is not reused:

- **`sources`** — the sources the grant covers, each a kind and, where the kind names one, a pool's
  name. The kinds are the table's (`launcher-pool`, `pool`, `anthropic`, `provider`, `vertex`); the
  provider, the model and the spelling a call is made in are the table's at each call and are not
  properties of a grant. An empty list is a grant that covers no source of the new kinds — what a
  release-behind launcher asks for — and never a grant that covers everything.
- **`named_pool`** — the pool the launch asked for with `--pool`, empty when it named none. It is not
  `Pool`: `Pool` is the pool the granted credentials belong to (the one whose owner and allowed users
  the grant checked), which a launch that named no pool still has.

The members are one embedded shape (`creds.SlipChain`), which is what declares `sources` and
`named_pool` once; CS14 embeds it into the slip, and the record is strict-decoded like the rest of it,
so a member a release does not know is refused rather than carried along. `creds.PickInPool` is the
picker the walk calls inside one pool: it takes the reference the lookup answered (so the rule and the
threshold travel with the pool), the run and the pool's members, and it is an interface rather than a
function because the stub the sides are written against until PL3 lands is a file I1 deletes — a
declaration there would have to be moved by the deletion rather than deleted. Its stub answers the
pool's first member, and `ErrNoCandidates` when it is handed no members at all.

What the picker answers is the selection path's own language (`Selector.Select`), not a second one.
**The `time.Duration` it returns is the wait until a member is usable in every shape and never a hold
time** — a lease's TTL has no place in this signature, and a rule that answered one would be handing
CS1 a duration to read as a wait. Shape for shape:

- **a member and a zero wait** — serve the call there. There is nothing to wait for on that path, just
  as `Select` answers it beside a credential.
- **`*ExhaustedUntil`** — no member is usable now and one becomes usable at a known time, which the
  error carries and the wait beside it repeats. The walk applies `Select`'s own wait-or-switch rule to
  it, exactly as it does to a selection.
- **`ErrNoCredential` and a zero wait** — no member is usable and none becomes usable by waiting:
  every member is broken, or holds a mark with no time to wait for. It is a **refusal and never a
  fallback** (D143), as it is on the selection path, where a dead token is the case the difference
  matters most.
- **`ErrNoCandidates`** is none of those. It is the refusal of being handed **no members at all** — a
  caller's mistake, a pool read that came back empty — and the walk must not move past it. Answering
  it for an exhausted pool would hide the mistake behind a wait or a fallback to the chain's next
  source, which may be a paid key (D143). It is not the empty pool's answer either: the lookup already
  answers `found` false for a pool that holds no member (D3180), so a walk handed an empty list has
  read something other than the pool it was pointed at.

## `fugaro accounts pool ls --json` (PL2, PL10, VS2)

The document is the project, its pools, and each pool's members:

```json
{
  "project": "fugaro",
  "pools": [
    {
      "name": "team",
      "rule": "ordered",
      "principal": {"kind": "project", "id": "fugaro"},
      "threshold": 0.8,
      "members": [
        {
          "name": "claude-work",
          "state": "available",
          "next_reset": null,
          "usage": {"calls": 12, "tokens": 3456},
          "headroom": 0.42
        }
      ]
    }
  ]
}
```

- **`name`** is a member's name — a container's, in the project's namespace or the principal's own —
  and **never its value**.
- **`rule`** is the pool's own, as its owner wrote it, and absent where the pool names none — the
  listing resolves nothing. An absent rule is read exactly as every pool's rule is, as the pool's
  `strategy` and as `failover` only where it names neither (`routing.Rule.RuleOrDefault`, R21). The
  document carries no `strategy`, so a reader that needs the resolved rule reads the pool's own
  config, where the strategy is (PL1), rather than treating an empty `rule` as failover.
- **`state`** is what holds the member, in `internal/creds`' own words, which `fugaro accounts ls
  --json` already prints: `available` for a member nothing holds, and otherwise the mark's state.
- **`next_reset`** is when the member becomes usable again — a mark's own instant, or the time its
  leases lapse — and null when nothing holds it.
- **`usage`** is what the member has spent in the window the service counts, as calls and tokens
  (PL10), and null where the service counts nothing for it.
- **`headroom`** is the fraction of the member's limit that is left, where the provider reported a
  utilisation figure, and **null otherwise: it is never guessed** (D3178, U1). It is the figure PL8's
  threshold moves a pool off a member by, read back as display.

**It carries names and figures and never a value** (F19): no token, no key and no secret appears
anywhere in it, for anyone, an admin included. And it is **display data**: every launcher of the
project can read these figures and the designations in the database whether the rules admit them or
not, so no command, no console and no run may read any of it as an authorization input. PL2 prints
the document (the command lands before the figures do, with the figures absent), PL10 fills the
usage and the headroom, and VS2 draws whichever are there.

## What tells the service an installation is migrated (PL3)

An installation is migrated **exactly when its published policy carries `model_routing`**, the one new
top-level key C1 adds: a policy without it is a project whose pools are inert, and the service serves
such a project's calls exactly as it does today. That is the marker PL3 reads, it is why merging PL3
alone moves nothing, and it is what makes the migration's order safe — `fugaro update` publishes the
key with the table, the admissions and the pools in their new shape (R21), and until it does, a pool a
person has written into a config decides nothing.

## The stubs, and what they decide

Both files say the same thing in the same shape: **a stub returns the shape it is named for and
decides nothing.**

| Stub | Its answer | What it must not do |
| --- | --- | --- |
| `internal/credsvc/launcherpool_stub.go` | no pool designated, for every principal and every name, with no error | read a designation, a pool, a policy or an entitlement, or answer an error that a caller could tell from "none" |
| `internal/creds/poolrule_stub.go` | the pool's first member; `ErrNoCandidates` when it is handed no members at all | read or write a mark, a lease, a placement, a counter or a utilisation figure, take a lease on the member it answers, or answer `ExhaustedUntil`/`ErrNoCredential` (which only a rule that reads a member's state can) |

I1 deletes both — and nothing a side keeps is declared in them, and each stub's own test goes with
it — and points the service at the real lookup (PL4) and the real rule (PL3).
