# The person wire (C1)

Task **C1** of [implementation-plan.md](implementation-plan.md). The two rules it states are **R9**
(the member's switch: one key at the top level of the member's published policy, off when absent) and
**R8** (a person with no token), with **R3** (the launcher class: what a grant is bound to and the one
class a refusal comes back as), **R4** and **F13** (a call carries a slip only when this service
granted it, and never a member's) and **A1** ("a person's token" is `fugaro_u_<id>_claude-oauth-token`)
behind the shapes, and **F14** behind the one thing the grant's answer may never carry. **U8** is the
question it settles, **D2972** is the switch's decision and **D3141**/**D3147** are the two decisions
a reader of the grant needs (a personal token follows the model route; a failed grant starts the run
on the shared entries) ([reconciliation.md](reconciliation.md), [spec.md](spec.md)). The Go is
`internal/creds/person.go`, the stub it ships is `internal/creds/person_stub.go`, and its tests are
`internal/creds/person_test.go`.

**WC5** (the personal candidate), **WC7** (`fugaro run` asks for a grant), **WC8** (the gateway
carries the workspace's slip) and **WC11** (doctor, and update's lines) are written against this
document; **WC4** (the workspace's grant and claim) and **WC6** (the member's switch) build its other
side, and **I1** deletes the stub and wires the two. A side that finds this contract wrong or
incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around
it.

## What the contract is

A person's own Claude token is stored once at the workspace (D2960) and served to the runs that
person launched (the spec's §3 item 2, D2962). This file is the handful of spellings the two halves
of that have to agree on and nothing that computes: where the member's switch lives, what a job reads
a workspace slip's id from, what a launcher asks the workspace for and what it is answered, the two
headers a workspace slip travels in, and the one class a refused grant comes back as.

| Name | Value | What it is |
| --- | --- | --- |
| `PersonSwitchKey` | `personal_workspace_tokens` | the key of R9's switch, at the top level of the member's published policy |
| `PersonTokenName` | `claude-oauth-token` (`config.ClaudeOAuthToken`) | the one personal name this plan serves (A1) |
| `PersonSlipEnv` | `FUGARO_WORKSPACE_SLIP` | the environment entry a job reads the workspace slip's id from |
| `PersonSlipHeader` | `X-Fugaro-Workspace-Slip` | the header a workspace slip's id travels in |
| `PersonSlipClaimHeader` | `X-Fugaro-Workspace-Slip-Claim` | the header the claim's secret travels in, on every call after the claim |
| `ErrPersonGrantRefused` | (a sentinel) | the one class a refused grant is answered with |

**Why the names live in `internal/creds` and not in the service.** `internal/credsvc` is the service
and `internal/gateway` is the run's proxy, and the gateway may not import the service's wire
constants: the service imports the gateway (its provider route forwards with the gateway's helpers and
its `Route`), so the dependency cannot run the other way — F26 of
[keys-through-service](../keys-through-service/contract-provider.md), the same reason the provider
route's names are where they are. Nor does the gateway import this package — `internal/creds` imports
`internal/budget`, which imports the gateway, so that is a cycle as well. The package the sides of
this wire do share is `internal/creds`: `internal/credsvc`, `internal/cli` and `internal/runner` all
import it, so the names a job's environment and a call's headers are spelled with live here, once, and
those three sides respell none of them. The gateway is the one side that will respell them, and that
**mirror is WC8's to write**: a call WC8 builds to the workspace's service carries the slip that
service granted, and the claim secret after the claim (R4), and `internal/gateway` can spell neither
through this package — so WC8 declares the names above a second time under `internal/gateway`, the
shape `internal/gateway/providerwire.go` already has for the provider route and
`internal/gateway/service.go` for the model wire, and holds the two spellings to each other name by
name with an external test (`package gateway_test`, which may import both). Until WC8 lands, this
package is their only declaration. R18 is not the reason it
could be otherwise: the slip that rule leaves off a call is a **member's**, whose claim secret would
be handed to a service outside the member (F13 of [reconciliation.md](reconciliation.md)), and the
slip the workspace's service grants is the workspace's own secret — the model wire's fields in
`internal/gateway/gateway.go` say the same of their own. C1's
`TestPersonGrantDecodesStrictly` pins every value above, as `TestRouteNamesArePinned` pins this
package's route names.

## The switch (R9, U8)

`PersonSwitchKey` is the one place a member turns the personal half on. **Its home is the top level
of the member's published policy** — `projectpolicy.Policy`, the node `config/policy/shared`
(`projectpolicy.PathShared()`) of the member's own project — and **that is the answer to U8**: the
member's *settings layer*, not a plain field of the member's local config. It is a local key the node
is published from, exactly the shape `triage` and `review_against_task` have: WC6 adds its row to
`internal/settings` (owner `fugaro settings set`, which is the generic boolean path), the member's
admin sets it in the member's own config, and the member's `fugaro update` carries it to the node.
The alternative U8 names — a local-config field no settings row and no publish touches — is not the
answer, because the workspace's service is the only enforcer and reads the member's published policy
and nothing on the member's disk: a key with no road to the node would be a switch nothing could
obey. WC6 therefore does not edit `internal/localcfg/localcfg.go`.

| Fact | Value |
| --- | --- |
| Absent | off: a member that never set the key serves no personal token and no run of it is offered one (D2972) |
| The value | a boolean |
| The policy's version | does not move, and an older reader that lists the key as an unknown top-level key and decodes past it loses nothing (R9) |
| Who reads it | the workspace's service, the only enforcer; and, on the member's own machine, the launcher and `fugaro doctor`, which decide with it whether a launch asks for a grant at all (WC7, WC11) |

`PersonSwitchReader` is the seam the two sides are built against:

```go
type PersonSwitchReader interface {
	PersonSwitchOn(ctx context.Context, project string) (bool, error)
}
```

It is asked **by project name**, because the workspace's service serves many members and reads each
member's own policy (`credsvc.Workspace.Policy`), while a launcher and `doctor` on a member's own
machine ask about the project they are in. **An error is a policy that could not be read and never
"off"**: WC5 refuses a call whose member's policy could not be read rather than serving the shared
entries as if the switch were off, and WC7 starts the run without the personal token and says why
(D3147).

**The stub.** `internal/creds/person_stub.go` ships `StubPersonSwitch`, whose `PersonSwitchOn`
answers `false, nil` for every project. That is not a placeholder answer but today's behaviour stated
as a value — absent is off (R9) and no member has the key today — which is what lets WC5, WC7 and
WC11 be written and tested before WC6's reader exists. It is deliberately the *tighter* answer rather
than an error: a stub that refused would make every caller of it test its failure path, and the
failure that path carries is a policy that cannot be read, which is nothing the absent key means.
Nothing on a production path names it, and **I1 deletes the file**. C1's `TestStubSwitchIsOff` pins
its answer.

## The one personal name (A1)

`PersonTokenName` is `config.ClaudeOAuthToken` and not a spelling of its own: the token a person
stores is the one `fugaro secrets user set` and the accounts' own namespace already name, and a
second spelling would be a second name for one container. What this plan adds is not the name but the
fact that it is **served** — under the person's own prefix, `fugaro_u_<sub>_claude-oauth-token`
(`secretname.User`), to that person's runs, at the workspace, and never lent (R6, R7, D2976).

A name in the `claude-oauth-token-<account>` namespace is **not** this name: the workspace's service
reads one container per person, conditioned to the `fugaro_u_` prefix and to this one name (R6), and
the plan serves the default account's token alone. The container's owner is the verified token's own
`sub`, so **the request names no person** (F11) — the name is the whole of what a caller chooses.

## The grant (R3, R8, D3141, D3147)

A launcher asks the workspace's service for a grant at the launch of a run that is to draw on its
launcher's own token — and only when the repository's model route is the workspace (D3141) and the
member's switch is on (R8). The route is the workspace's own service in workspace mode, on the
service's two slip paths: the grant on `credsvc.SlipPath` (`/v1/slip`) and the claim on
`credsvc.SlipClaimPath` (`/v1/slip/claim`), which R3 names. **C1 declares no path**: `internal/creds`
may not import `internal/credsvc`, so the paths stay the service's (`internal/credsvc/slip.go`) and
are written here only so WC4 and WC7 do not drift.

### The request

```go
type PersonGrantRequest struct {
	Slug    string `json:"slug"`
	Run     string `json:"run"`
	Account string `json:"account"`
	Name    string `json:"name"`
}
```

Every field is a name or an id of the calling run and never a value:

- **`slug`** is the repository the run belongs to, as the launcher's own tokens spell it (`task.Slug`).
- **`run`** is the run id the launcher minted (`task.ValidRunID`).
- **`account`** is the job service account the run will call as — one account to one Cloud Run job
  (D1937).
- **`name`** is the personal name the slip is bound to, which for every request this plan writes is
  `PersonTokenName`. It is a field rather than an assumed constant so that a name this plan does not
  serve is **refused here** rather than silently answered about another container.

Together the three ids are the binding the workspace holds the caller to: the account to a member row
of its node and the repository to that account's list, before it writes anything (R3, WC4). Their own
shapes — a slug of `task.Slug`'s form, an account of the job's — are the **route's** check, held to
the same rules the member's own grant's fields are; this contract checks that each is present.

**It is a shape of its own and not `credsvc.SlipGrantRequest`**, though every field of it is the
member's own grant's word for the same thing. The question is different: a member's grant asks for a
slip over a list of the run's resolved access names, and this one asks whether the caller holds ONE
personal token and, if they do, binds the slip to it (A1, R7). And the two must never be readable as
each other: `ParsePersonGrantRequest` refuses a body carrying `credentials` or `max_runtime_seconds`,
and the member's route refuses this shape, so a launcher that pointed a member's grant at the
workspace's service is answered a refusal and never a workspace slip. That is **R4 and F13 held by
the wire** rather than by the address the call was sent to.

### The answer

```go
type PersonGrant struct {
	Held bool   `json:"held"`
	Slip string `json:"slip,omitempty"`
}
```

It has exactly two states:

| State | Body | What the launcher does |
| --- | --- | --- |
| the caller holds a token of the name it asked for | `{"held":true,"slip":"<the slip's id>"}` | hands the job the id as `PersonSlipEnv` and prints one line naming the workspace |
| the caller holds none | `{"held":false}` | starts the run on the shared entries exactly as before, and prints nothing (R8) |

**The two states are one shape and not an error**, because "no token held" is an ordinary beginning
of a run: a person who never stored a token launches the way they launched before this plan. It is
also answered **before the workspace's node is read**, so only a person who really holds a token
learns anything from the route at all (R3).

**The answer carries no claim secret** (F14). The claim secret is answered by the claim route alone,
once, to the run the slip names, and kept out of the environment (`slipHold`,
`internal/runner/budget_session.go`); a grant that carried it would hand a credential to the
launcher's process and to every line that process writes. The strict decode is what makes that a
property of the wire rather than of the service's manners: an answer carrying `claim_secret` — or any
key this contract does not name — is refused. WC7's `TestTheGrantCarriesNoClaimSecret` pins the
launcher's half.

The launcher holds the id to its own bound for one environment entry (`slipIDUsable`,
`internal/cli`), exactly as it holds the member's slip's id, and the run claims it at the workspace's
service for the secret it presents with `PersonSlipClaimHeader` afterwards. The claim's own shapes
are not this contract's: they are `credsvc`'s (`SlipClaimRequest`, `SlipClaimResponse`), on the
service's claim path, and the claim is the member job account's as today (R3).

## The refusal (R3, D3147)

`ErrPersonGrantRefused` is the one class a refused grant is answered with, and every refusal of a
workspace grant comes back as it and nothing finer: the route names no account, no repository and no
person, so a person who holds a token learns nothing from a refusal beyond the fact of it, and the
launcher has one thing to branch on. On the wire it is the service's one request-refusal class —
`StatusRequestRefused` with `ErrorRequestRefused` — and a launcher reads that answer back as this
sentinel.

It is told apart from the two things it is not, because they are three different answers a launch has
to be able to speak about:

| What happened | Which answer | What the launch does |
| --- | --- | --- |
| the caller holds no token of that name | `PersonGrant.Held` is false — an ANSWER | starts as before, prints nothing (R8) |
| the grant is refused | `ErrPersonGrantRefused` | starts on the shared entries and says why (D3147) |
| the workspace's service cannot be reached | an error of the transport, never this class | starts on the shared entries and says why (D3147) |

All three leave the run on the shared entries; a launcher that folded them together could not say
which happened.

## The two headers

`PersonSlipHeader` carries a workspace slip's id — what the grant answered and the launcher handed
the job — on the call that claims it and on every call under it after. It is **not a secret**: it is
bound to the run and admitted by the claim secret, exactly as `creds.Slip` and `credsvc.HeaderSlip`
say of the member's. `PersonSlipClaimHeader` carries the claim secret, the **one value of this
contract**: presented on every call after the claim, kept by the workspace as a hash, and carried by
no line, error or log of either side (F14). It is registered with the call's scrub on the side that
sends it (WC8), as the member's own claim header is.

**They are this contract's own names and not `credsvc`'s `HeaderSlip` and `HeaderSlipClaim`**, and
that is R4 written as a spelling. A run holds two slips at once and they are served by two services a
member's admins control differently: the member's own, which they own, and the workspace's, which
they do not (F13). Two names make the separation structural rather than a routing decision — a
workspace slip presented under the member's own header is a call the member's service reads as one it
granted, and a member's slip under these names is one the workspace's service reads and refuses — so
the gateway's two upstreams cannot be told apart by anything but which service the call goes to
(D2982, WC8). It is also why `PersonSlipEnv` is a second variable beside `runner.SlipEnv`
(`FUGARO_SLIP`) rather than the same one, and why it is plain environment and never a secret entry:
the id is not a credential and the claim secret never reaches the environment at all (F14). Like
`FUGARO_SLIP`, it is the **runner's** variable and not the agent's.

## Strict decoding

`ParsePersonGrantRequest` and `ParsePersonGrant` read the two shapes strictly:

- a key this contract does not name is **refused**, never decoded past — the red step of C1 is
  exactly that, an unknown key accepted until the strict decode lands;
- a body carrying a second JSON value, or a body that is not one JSON object, is refused;
- a request missing or leaving empty any of its four fields is refused, and so is a request whose
  `name` is not `PersonTokenName` (the refusal names the field and never its value);
- an answer whose two fields disagree is refused — `held` true with no slip, or a slip with `held`
  false, or a slip with no `held` beside it at all.

A body that spells `held:false` and a body that spells neither field are **one answer**: no token
held, the state a run starts on as before. That is the only zero value this contract reads, and it is
the benign one — there is no third state for a missing key to fall into, and a caller branching on
`held` treats its zero as "not granted". The request's four fields are refused when absent for the
reason it is not safe there: a request that named no repository would ask about nothing, and its
answer would be a grant nothing could be held to.

What this contract does **not** check is the size of a body and the shape of a slug, a run id or an
account: the service reads a bounded body before this (`credsvc`'s own bound), and those fields'
rules are the route's, held to the same rules the member's grant's fields are. The slip's id is not
held to a length here either — the launcher that puts it in a job's environment is where that is
checked (`slipIDUsable`), because a launcher's bound is its own and a value it will not carry must
not be one this package refused on its behalf.
