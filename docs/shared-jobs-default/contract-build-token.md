# The build-token contract (C2)

Task **C2** of [implementation-plan.md](implementation-plan.md). The rules it states are **F19** of
[reconciliation.md](reconciliation.md) and the two answers are **R6**; the decisions behind them are D2301 (one
build may bake several repositories) and D2311 (no catalog entry carries a credential of its own). The shapes are
`internal/buildtoken`: `Request`, `Response`, `Token`, `ProxyGrant` and `Client`, and `internal/buildtoken/stub.go`
returns the shape and does nothing else.

**B1** (the credentials service's build route), **B2** (the catalog build bakes several repositories) and **B8**
(the generated clone step) are written against this document in parallel; **I2** deletes the stub and wires the
build to the route. A side that finds this contract wrong or incomplete raises a change request
([docs/contract-first-decoupling](../contract-first-decoupling/spec.md)) rather than working around it; **CR2** is
the one already anticipated, if B1's review finds the time-to-live cap belongs in the request.

## The shape

| Field | Who writes it | What it means |
| --- | --- | --- |
| `Request.Entry` | the build | the catalog entry being built; the service reads the repositories a build may be given from the **published image map for this entry** |
| `Request.BuildID` | the build | the build the request is made for (the id it runs under), which the service keys the Bitbucket pairing on beside the identity — bookkeeping, not a bound (rule 2) |
| `Request.Provider` | the build | the provider the credential is minted at, and therefore which of the two answers comes back; the entry's map is the authority, and a request naming another provider is refused |
| `Request.Repositories` | the build | the repositories the credential must cover, by canonical name — the image map's own key space |
| `Request.TTLSeconds` | the build | how long the build needs the credential to live |
| `Response.Token` | the service | the GitHub answer: one read-only installation token, with the user, the host and the expiry it is valid to |
| `Response.Proxy` | the service | the Bitbucket answer: the proxy URL, the repositories paired to **this build's** identity, and when that pairing ends |

`Response` is **exactly one of the two**, never both and never neither. Which one comes back is the request's
provider's (R6), and a build that is answered neither has nothing to clone with and fails rather than asking again
wider.

## The answer

- **GitHub: a narrowed installation token.** Read-only (`contents: read`, `metadata: read`), covering exactly the
  request's repositories of the caller's own project, expiring at GitHub's own instant. The request cannot widen
  it: a repository the entry's map does not put on the entry is refused by the service, naming the repository, and
  the mint is audited (B1). Nor can it choose the host: the token is minted at the entry's own provider, and a
  request that names the other one is refused, because owner/name is not unique across hosts and the same name on
  the other one is another repository. Its reach is the request's repositories and no others: a build over
  several repositories is one token covering exactly those, and it is the narrowing — not the token's kind — that
  bounds it, the same way a run's token is bounded (spec decision 4).
- **Bitbucket: a proxy grant, no token.** The build holds no token at all (D2302, R6): the service pairs the
  build's identity to the entry's repositories **for this build** — the pairing is keyed on the identity *and* the
  build the request names (rule 2) — and the build's git talks to the credentials service's proxy, as a job's does.
  The grant is not a secret: what every call presents is the build identity's own ID token, which the generated
  clone step fetches for itself and which reaches **that step alone** — never the entry's build. It carries an
  expiry all the same, on the service's own instant: the pairing is what stands between the build's git and the
  entry's repositories, so it is bounded by its build instead of left standing for the next one. Two builds of one
  project at once are not isolated from each other, and this contract does not say they are
  ([below](#what-this-contract-cannot-promise)).

## The rules (F19)

1. **Read-only.** Nothing a build is answered with can write to a repository, and no request can ask for a write:
   the permission set is the service's constant and the request has no field for it.
2. **Per build, per entry.** The credential is minted for one build of one entry, covers that entry's
   repositories, and is revoked or expires with the build. Nothing is reused between builds and nothing is
   cached: a long-lived credential here would reach every repository the build's project has. The build is named
   in the request (`Request.BuildID`) so that two concurrent builds of one project do not share or overwrite one
   pairing, and so that each pairing ends with the build that asked for it. **That key is bookkeeping, not an
   authorization bound, and this contract does not pretend otherwise**: the id is the caller's own word — a
   project has one build identity (rule 8), so every build of the project can name any id — and the proxy
   authenticates that identity's ID token, which is the same for all of them, so it cannot tell two builds apart.
   What bounds what a build may clone is the entry its request names and the route's refusal of anything outside
   it (rules 8 and 9), never the build id. What the key cannot do, and what closing it would take, is
   [below](#what-this-contract-cannot-promise).
3. **The clone runs in a step Fugaro generates entirely.** The generated step is a Dockerfile of the engine's own,
   built from the pinned base digest with `GIT_CONFIG_NOSYSTEM=1`, and it alone holds the credential (B8). It is a
   **separate build step**, not a stage of the entry's Dockerfile: BuildKit hands a build's secret to every `RUN`
   that asks for it by id, so a stage inside the entry's build is readable by the entry's own steps, however the
   clone is scoped (F19, review round 2). The checkout reaches the entry's build as a **named build context**.
4. **The entry's build holds no secret and no identity.** No `--secret` argument, no secret mount, no service
   account of its own: a catalog entry may not ask for either, and one that does is refused by name, with the line
   it is written on (B9, D2311). The entry's `RUN` steps cannot read the credential and cannot alter the clone's
   tools, because they never share a stage with them.
5. **The credential is written per host, and offered to that host alone.** A clone, fetch, push or LFS URL on any
   other host — a URL written in `.gitmodules` or `.lfsconfig` included — is refused **before any credential is
   offered**, and `protocol.ext` and `protocol.file` are set to never (B8). A repository that names another host
   cannot be handed this credential.
6. **Submodules and LFS objects of a baked repository are fetched in the generated step**, under the rules above,
   so a nested fetch inherits the same narrowing and the same refusal (spec, "Submodules and LFS"; N1).
7. **The credential ends with the step.** It is revoked at the step's end where the provider allows and otherwise
   expires within the build's timeout, and no token or credential file survives in any layer: the build scans the
   image for git config credentials, a `.netrc`, a credential-helper store and a token in the environment before it
   promotes (B3, R2). That scan is the second fence, not the first: it looks for credentials, not for source an
   entry copied (F19). The Bitbucket answer leaves no credential of the build's to revoke; what ends there is the
   pairing, at the grant's expiry and no later (rule 2).
8. **One project identity, and the union.** A catalog build runs as the project's one build identity (B5, B6),
   which can reach the union of every catalog entry's repositories of its project. What keeps that from being the
   compromise it looks like is not the identity's reach but the request's: a build asks for its own entry's
   repositories, the service refuses anything outside them by name, and every mint is audited (B1). **This is the
   contract's sharpest edge and the reason the rules above are the fence**: the identity is the project's, so the
   narrowing lives in the route and in the generated step, never in the identity's grants.
9. **The route is the build's alone.** Every other caller class is refused on the build route, and the build
   identity is refused on every other route of the service (B1). A request the service refuses is passed to the
   caller as it is; a client that retried wider, or fell back to a credential of its own, would undo rule 8.

## What this contract cannot promise

**Two Bitbucket builds of one project at once are not isolated from each other.** The grant's URL and the ID
token a call presents are the project's build identity's, so the proxy attributes a call to the identity and not
to a build: whichever pairing is live for that identity is what a call is served against. The per-build key
(rule 2) keeps two builds from colliding over one pairing and bounds each pairing's life; it does not separate
them, and a contract over a shape cannot. The key is not what protects a project's repositories either: the
identity can already ask for any of the project's entries by name, and the service answers from that entry's map
(rule 8), so a build that wanted another entry's repositories would name that entry rather than take a pairing
over. What keeps the credential out of an entry's own hands is the fence F19 builds around the clone (rules 3 to
7).

Closing the gap is a decision, and there are three ways to take it:

1. **The proxy serves an identity only while exactly one pairing is live for it**, refusing otherwise. Fail
   closed: the second concurrent build's clone fails. That is a behaviour B1 has to choose and implement.
2. **One Bitbucket catalog build per project at a time**, a rule on how builds are launched — B2's or the
   launcher's, not this contract's.
3. **A per-build credential at the proxy** — the Fugaro-issued token D2302 rejected, which would have to be
   reopened, and which F19 exists to avoid.

Until one of them is taken, the answer this contract gives is that the two builds are not isolated, and nothing
in it says they are.

## What each side may rely on

- **The build (B2, B8)** may rely on: the two answer shapes above and nothing else; a refusal naming the thing
  that was refused (the repository, the line, the host); a credential or pairing keyed to its own build and not
  to a neighbour's, though not isolation from that neighbour (see above); and no credential of its own to hold —
  for Bitbucket it holds none at all. It may not read anything out of a `Stub` in a shipped path (I2 deletes the
  stub) and may not fall back to a wider credential.
- **The service (B1)** may rely on: a request that names an entry, **the build it is made for**, one provider and
  the canonical names of the entry's repositories; a build identity it can verify; and a caller that does no
  narrowing of its own, so every rule is the service's to apply. The build id and the provider are the caller's
  word, so the service checks both against what it holds — the build id's shape, and the entry's own provider —
  rather than trusting either.
- **Neither side may rely on** the other's internals: the route's path, its body beyond these fields, and the way
  the service mints are not this contract. A field's meaning never changes
  ([CLAUDE.md](../../CLAUDE.md), "a field never changes meaning"): a new field is additive, and a meaning that has
  to change gets a new name.

## What this contract does not decide

- **The time-to-live cap** (how long a build may ask for, and what the service does with a request that asks for
  longer). It is B1's, and CR2 moves it into the request if B1's review finds it belongs there.
- **The route's path and its body's encoding** are B1's to name; the two sides share these fields and nothing
  else about the wire.
- **How a pairing is ended before its expiry** — a build event, or a release the build has to ask for. This
  contract bounds the pairing by the build and says it is keyed per build; a release call would be a method on
  `Client`, and so a change request of its own if B1's review wants one.
- **Whether a catalog entry may keep a credential of its own**: no, it may not (D2311, rule 4), and the refusal is
  B9's.

## Review

Reviewer tier **opus + independent review** before B1 and B2 launch
([implementation-plan.md](implementation-plan.md), Conventions): this is a contract over credentials, and the
change it fences — one credential over several repositories — is the reason F19 exists.
