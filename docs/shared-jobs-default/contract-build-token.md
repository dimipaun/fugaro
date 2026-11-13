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
| `Request.Provider` | the build | the provider the credential is minted at, and therefore which of the two answers comes back |
| `Request.Repositories` | the build | the repositories the credential must cover, by canonical name — the image map's own key space |
| `Request.TTLSeconds` | the build | how long the build needs the credential to live |
| `Response.Token` | the service | the GitHub answer: one read-only installation token, with the user, the host and the expiry it is valid to |
| `Response.Proxy` | the service | the Bitbucket answer: the proxy URL, and the repositories paired to the build's identity |

`Response` is **exactly one of the two**, never both and never neither. Which one is the request's provider's
(R6), and the client never asks twice for a shaped answer it did not get: a build that is answered nothing has
nothing to clone with and fails.

## The answer

- **GitHub: a narrowed installation token.** Read-only (`contents: read`, `metadata: read`), covering exactly the
  request's repositories of the caller's own project, expiring at GitHub's own instant. The request cannot widen
  it: a repository the entry's map does not put on the entry is refused by the service, naming the repository, and
  the mint is audited (B1). Its reach is one repository per token only because the token is narrowed, which is the
  same thing a run's is (spec decision 4).
- **Bitbucket: a proxy grant, no token.** The build holds no token at all (D2302, R6): the service pairs the
  build's identity to the entry's repositories, and the build's git talks to the credentials service's proxy, as a
  job's does. The grant is not a secret and carries no expiry: what every call presents is the build identity's
  own ID token, which the generated clone step fetches for itself and which reaches **that step alone** — never the
  entry's build.

## The rules (F19)

1. **Read-only.** Nothing a build is answered with can write to a repository, and no request can ask for a write:
   the permission set is the service's constant and the request has no field for it.
2. **Per build, per entry.** The credential is minted for one build of one entry, covers that entry's
   repositories, and is revoked or expires with the build. Nothing is reused between builds and nothing is
   cached: a long-lived credential here would reach every repository the build's project has.
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
   entry copied (F19).
8. **One project identity, and the union.** A catalog build runs as the project's one build identity (B5, B6),
   which can reach the union of every catalog entry's repositories of its project. What keeps that from being the
   compromise it looks like is not the identity's reach but the request's: a build asks for its own entry's
   repositories, the service refuses anything outside them by name, and every mint is audited (B1). **This is the
   contract's sharpest edge and the reason the rules above are the fence**: the identity is the project's, so the
   narrowing lives in the route and in the generated step, never in the identity's grants.
9. **The route is the build's alone.** Every other caller class is refused on the build route, and the build
   identity is refused on every other route of the service (B1). A request the service refuses is passed to the
   caller as it is; a client that retried wider, or fell back to a credential of its own, would undo rule 8.

## What each side may rely on

- **The build (B2, B8)** may rely on: the two answer shapes above and nothing else; a refusal naming the thing
  that was refused (the repository, the line, the host); and no credential of its own to hold — for Bitbucket it
  holds none at all. It may not read anything out of a `Stub` in a shipped path (I2 deletes the stub) and may not
  fall back to a wider credential.
- **The service (B1)** may rely on: a request that names an entry, one provider and the canonical names of the
  entry's repositories; a build identity it can verify; and a caller that does no narrowing of its own, so every
  rule is the service's to apply.
- **Neither side may rely on** the other's internals: the route's path, its body beyond these fields, and the way
  the service mints are not this contract. A field's meaning never changes
  ([CLAUDE.md](../../CLAUDE.md), "a field never changes meaning"): a new field is additive, and a meaning that has
  to change gets a new name.

## What this contract does not decide

- **The time-to-live cap** (how long a build may ask for, and what the service does with a request that asks for
  longer). It is B1's, and CR2 moves it into the request if B1's review finds it belongs there.
- **The route's path and its body's encoding** are B1's to name; the two sides share these fields and nothing
  else about the wire.
- **Whether a catalog entry may keep a credential of its own**: no, it may not (D2311, rule 4), and the refusal is
  B9's.

## Review

Reviewer tier **opus + independent review** before B1 and B2 launch
([implementation-plan.md](implementation-plan.md), Conventions): this is a contract over credentials, and the
change it fences — one credential over several repositories — is the reason F19 exists.
