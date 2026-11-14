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
| the real store wired end to end, `internal/bbtokens/stub.go` and `stub_test.go` deleted | I1 | I1 |

## The record

`Record` is the operator's own statement about one token: `Name` (the document key), `Type`, `Coverage`, `ProjectUUID`,
`ProjectKey` and `Expires`. It holds no value; the value is the bundle's and is found by `Name`.

`Validate` refuses a record whose type and coverage disagree (R1):

- **`repo`** — every coverage entry is exactly `workspace/repo`: two non-empty segments, no `*`, no empty project UUID.
- **`workspace`** — every entry is `workspace/*`: a literal owner and a bare `*` segment, no project UUID.
- **`project`** — the same `workspace/*` form **plus** a non-empty `ProjectUUID`. The key is display only; a UUID cannot
  be renamed and a key can.
- **untyped** (`TypeUntyped`, the empty string) — the legacy `bitbucket-token` key, whose coverage is the operator's
  `allow_data_to` list as written: either of the two forms above, and no project UUID.

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
status rather than an error.

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
`BitbucketTokens{Entries: []BitbucketTokenNode{...}}`: per token, the document key, the type, the coverage patterns
(provider-qualified as `allow_repos` entries are) and the identity UUID — never a value and never a secret version. A
project that names no typed token publishes no key at all.

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
