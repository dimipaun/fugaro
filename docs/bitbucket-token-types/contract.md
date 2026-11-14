# The token-store contract (C1)

**Package:** `internal/bbtokens`. **Node shape:** `internal/projectpolicy/tokens_shape.go`. Built by task C1 of
[implementation-plan.md](implementation-plan.md); the decisions it restates are [reconciliation.md](reconciliation.md) D2350–D2356 and
R1–R14. Everything here is what a task written against C1 may rely on, and nothing else in this package is promised.

The package holds no policy, no bundle, no state and no token. It states the shapes, so that the eight edges the plan breaks by
contract can be written on both sides at once and re-joined in I1.

## What each side owes

| Side | Owes | Task |
| --- | --- | --- |
| `Selector` | the narrowest **usable** covering token, or a `*Refusal` | T4 |
| `ProjectResolver` | which project a repository is in, asked with the candidate project token, cached per instance | T5 |
| `StatusStore` | a Bitbucket token's own status, in its own subtree of the creds state store | T6 |
| `BundleTokens` | the bundle's token names and each entry's value accessor | T1 |
| the node key and the identity set | `projectpolicy.BitbucketTokensKey`, `BitbucketTokens`, `BitbucketTokenIdentities` | T3 |
| the verifier | the add-time check `creds set` runs | T7 |
| the proxy | the one snapshot, the selection step, the audit name | T8 |
| I1 | the real store wired end to end, `internal/bbtokens/stub.go` and `stub_test.go` deleted | I1 |

## The record

`Record` is the operator's own statement about one token: `Name` (the document key), `Type`, `Coverage`, `ProjectUUID`,
`ProjectKey` and `Expires`. It holds no value; the value is the bundle's and is found by `Name`.

**One spelling of coverage.** A coverage entry is written in the **policy node's** spelling and not the local config's:
provider-qualified, as in `bitbucket/workspace-a/repo-a` or `bitbucket/workspace-a/*`, which is what `MatchRepo` compares
against a canonical repository. It is held to **`projectpolicy.ValidBitbucketTokenCoverage`**, the node's own rule for one
Bitbucket credential's reach: `ValidPattern` (a provider segment that is a real git provider kind, at least three segments,
lower-case repository characters, no `.git` suffix, at most four wildcards, `MaxPatternLen`) plus the three narrowings the
node's `bitbucket_token` block applies to an entry of `allow_repos` — not the bare `*`, the first segment `bitbucket`, a
**literal owner segment** (a wildcard owner would let one credential reach every workspace, the widening D464 refuses) and
at most the two wildcards dc73's own bound allows.

It is the node's spelling and not the config's on purpose. The credential is forwarded by the credentials service, which
holds the node and never the operator's local config: the record the legacy `bitbucket-token` key becomes **is** that
node's `bitbucket_token.allow_repos` list (R3), so a record spelled the config's two-segment `allow_data_to` way (D464)
would be one the service could neither build the legacy record in nor match a canonical repository with. The operator's
`allow_data_to` entry keeps its own two-segment spelling in the local config; the node's derivation (T3) qualifies it with
the provider on the way, and the record travels in that qualified form.

`Validate` refuses a record whose type and coverage disagree (R1) — the same three forms R1 names, read in that spelling,
since a Bitbucket canonical repository is `bitbucket/<workspace>/<repo>`:

- **`repo`** — every coverage entry names one repository (`bitbucket/workspace-a/repo-a`), with no `*` anywhere: a
  repository token covers the repositories it was created for, and a wildcard is a workspace token's form. No project UUID.
- **`workspace`** — every entry is `bitbucket/workspace-a/*`: the literal workspace and a bare `*` name segment. No
  project UUID.
- **`project`** — the same `bitbucket/workspace-a/*` form **plus** a non-empty `ProjectUUID`. The key is display only; a
  UUID cannot be renamed and a key can.
- **untyped** (`TypeUntyped`, the empty string) — the legacy `bitbucket-token` key, whose coverage is the node's
  `allow_repos` list as written: any entry the shared rule accepts, including a wildcard inside the name segment
  (`bitbucket/workspace-a/api-*`), and no project UUID. An **empty** list is the one case only this type accepts: the
  record is implicit, a node with no `bitbucket_token` block holds nothing, and an installation that named no repository
  has to keep working with no step (F14) — so it reads as a token that reaches nothing rather than as a load error. Every
  typed record refuses an empty coverage: a token the operator named and gave no reach is not a token.

`Type` is redundant for matching and is stored for display and for the add-time check. Coverage states which token **can**
serve a repository; it never replaces `allow_data_to`, which stays the operator's authorization (D2354).

`MatchesProject(uuid)` is the whole of project matching: a `project` record against the UUID a lookup answered. **An empty
UUID on either side never matches** — two unknowns are not the same project, or a token that names no project would serve
every repository its lookup failed for.

## The status

`Status` is a token's non-secret and non-operator half: `State`, `Until`, `VerifiedAt`, `Identity`, `Count`. It lives in the
creds state store rather than in the local config, so a hand-edited file cannot add an identity (D2351, R12).

`State` speaks the vocabulary the creds marks already use (F5): `available`, `exhausted` (Bitbucket's 429, until `Until`),
`backed_off` (a 429 that named no `Retry-After`, until `Until`) and `broken` (a 401 a covered-repository read confirmed;
an operator clears it). A token nothing has recorded is `available`, which is why `StatusStore.Read` answers the zero
status rather than an error — and `Status.Available()` is the one place the two spellings of "holds nothing" (the zero
`State` and `StateAvailable`) are reconciled, exactly as `creds.Mark.Available()` does for the model marks. No other reader
compares them itself.

`Identity` is the account UUID `GET /2.0/user` answered, and it is empty for a repository token, which that endpoint
refuses (R14) — the set is partial by design. `Count` is advisory: nothing queues or refuses a call on it (D2356), and
`CountRequest` is best effort, so a failed write under-reports a number no decision is made on.

## The snapshot

`Snapshot{Policy, Bundle}` is one request's read of the two documents it is decided from. `Decide`, the selection and the
forward all use the same one, so a request cannot be admitted against one policy and served under another and a policy
published mid-request is never half-applied. `Policy` is the whole shared node — the legacy untyped record's coverage **is**
that node's `bitbucket_token.allow_repos` list (R3).

`BundleTokens` is the bundle's tokens as the snapshot holds them: `Names()` in a stable order, and `Value(name)` for the
one path about to attach a token to a call. Nothing else hands a value out and nothing renders one.

**A token has to be in both documents to serve anything.** A bundle key with no node record, and a node record with no
bundle key, are **skipped**, not selected (C1's acceptance, R14, R5). Neither is an error and neither is a refusal class of
its own: a half-published token is not a candidate, so a publish that lands the two sides of a token in either order reads
the same way, and no caller serves a call with a token it cannot forward or selects a name whose value it would then not
find. The same holds for a record a reader refuses (`Validate`) and for a record named `bitbucket-token` inside the
`bitbucket_tokens` key — the legacy key is the implicit record's own name and never an entry of the key — so a
half-published or hand-broken token serves nothing at all (R14, fail closed). Every one of these is a skip and not a
refusal: what the selector does with the candidates that are left is T4's, and no skip is ever reported as a token that
was tried.

## Selection

`Selector.Select(ctx, snap, canonical, write) (Choice, error)`. `canonical` is the canonical repository of the **pairing**,
not of the request: `GET /user` carries no repository, and the pairing fixes it from the calling job account for every call
(U3). `write` is true for a request that changes something upstream.

The contract owns neither the order nor the skipping — that is T4's — but it owns what a caller may rely on: the answer is a
`Choice` naming one token's document key, or a `*Refusal`, never a bare error, and `Choice.Name` is the name the audit line
writes (R9) and never the token.

`ProjectOf` answers an empty UUID with a nil error for "this token does not cover this repository" (Bitbucket's 403 or 404),
which is an answer and not a failure. It is asked with the candidate project token itself, never with a wider or a narrower
one (R2). `Evict` drops one cached answer, so the next call asks again.

## Refusals

`RefusalClass` is one closed vocabulary: `token_not_covered`, `token_broken`, `token_throttled`, `write_scope_missing`,
`no_bitbucket_token`. A `*Refusal` carries the class and the canonical repository it is about, and its message names the
repository — **never a token**, which an operator reads off the audit line and a caller has no business learning. A class is
read off the error with `errors.As`, never off its text.

## The node's key

The node gains one top-level key, `projectpolicy.BitbucketTokensKey` (`bitbucket_tokens`), whose value is
`BitbucketTokens{Entries: []BitbucketTokenNode{...}}`: per token, the document key, the type, the coverage patterns — in
the same provider-qualified spelling a `Record` carries, held to the same `ValidBitbucketTokenCoverage`, so a record read
off the node validates as it stands — and the identity UUID, and never a value and never a secret version. A project that
names no typed token publishes no key at all.

The rule lives in `projectpolicy` with the shape, and not in the package that holds the record, so that one rule holds both
sides of R3: a rule the node accepted and the record refused would drop the implicit legacy record of an installation whose
node is fine, and one the record accepted and the node refused would publish a node the next run of the project cannot read
back. `bitbucket_token`'s own reader states the same narrowings for its list today; that list is the legacy record's
coverage, so the two spellings and the two rules are one thing stated in two places, and T3 owns that file.

`bitbucket_token` and `Version` are untouched (R5). A top-level key is the right level because every unknown **member** of
the `bitbucket_token` block is refused by its reader while every unknown **top-level** key is tolerated by a runner of the
release behind and refused by its CLI — the same skew the `image_catalog` key set (D1156). `BitbucketTokenIdentities` is
the set of account UUIDs that key carries, which the Bitbucket adapter compares a comment's author against (F20).

Both are declared in `internal/projectpolicy` rather than here so that the runner and the adapter, which merely read them,
import no code that holds, checks or forwards a token.

## What this contract deliberately does not do

- It reads nothing and writes nothing: the selector, the resolver, the verifier and the store are T4, T5, T7 and T6.
- It does not decide the selection order, the cache TTL or the backoff, which are the tasks' own constants.
- `stub.go` is a placeholder with set answers. I1 deletes `internal/bbtokens/stub.go` and
  `internal/bbtokens/stub_test.go` and wires the real implementations.
