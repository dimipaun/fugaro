# The mobile session contract (C1)

The Go half is [`internal/mobilesession`](../../internal/mobilesession/contract.go): the claims, the routes, the refusal
codes, the wire shapes, the secure-store item names and the interfaces. That directory also holds
[`stub.go`](../../internal/mobilesession/stub.go), the scaffolding I1 deletes when it wires the sides. The data half is
[`shared/mobile-session/vectors.json`](../../shared/mobile-session/vectors.json), which both languages read; its JS
reader is the test beside it. **This document is the prose half, and it is the half the sides are held to**: it says
what each side may rely on, what none of them may, and what changing the contract costs.

C1 is a contract and not an implementation (plan C1, spec [mobile identity and pairing](spec.md)). It exists because
the rest of the plan is written in parallel against two ends that must agree byte for byte — the assertion service and
the key set (A1, A2, A4, K1), the verifier and the project's routes (P1-P4, P7, K2), the app's identity, pairing and
sessions (M1-M3, K1, K3) — and none of them can be written against a phone. The shapes in `internal/mobilesession` are
what they call; the stub returns each of them and decides nothing, so no side can pass on a decision no side has made.

The contract rests on the owner's answers of 2026-11-18 ([reconciliation.md](reconciliation.md)): an EdDSA JWT
assertion carrying the claims below and a root-signed key-set envelope (D2792, O7); a rotating, device-key-bound
refresh token with an hourly Firebase custom token (D2783); the first `(email, provider, sub)` a project accepted as
the member record, with the roster as the one source of membership and role (D2785, D2791, O14); the accepted-provider
policy (D2785); the challenge-and-`jti` compare-and-set store that makes single use hold across the public service's
several instances (F39); and the consumer public key the phone pins (O12, O16).

## The routes

| Route | Answers | Called by |
| --- | --- | --- |
| `/mobile/v1/pair` | the session (first pairing: assertion + QR nonce + device public key) | the app, M2 |
| `/mobile/v1/refresh` | the session (successor refresh token + custom token) | the app, M3, hourly |
| `/mobile/v1/restore/challenge` | a challenge and its life (no body) | the app, K2 |
| `/mobile/v1/restore` | the session (assertion + that challenge + device public key) | the app, K3 |
| `/mobile/v1/revoke-self` | nothing (the phone's own sign-out everywhere) | the app, M3 |

The audience of every assertion is derived from the endpoint the call names and never from a project id: a QR carries
the endpoint, the assertion's `aud` is that endpoint canonicalised, and a service serves a claim only when the derived
string is both its own audience and the assertion's. That is what refuses a QR relayed to another project (F4, F39),
and it is why `aud` is a field of the pair request.

## The shapes

| Shape | Carries | Decided by |
| --- | --- | --- |
| `Claims` | the assertion's `iss`, `aud`, `sub`, `email`, `email_verified`, `provider`, `auth_time`, `jti`, `iat`, `exp` and `challenge` | the signer (A1), read by the verifier (P1) |
| `PairRequest` | the assertion, the endpoint it is for, the invite's nonce, the device's public key and, on a re-pair, the prior device's id token | the app (M2), checked by the pair route (P3) |
| `SessionResponse` | the project id, the endpoint, the database URL and API key, the role, the session policy, the consumer public key, the refresh token, the custom token and its life | the pair and restore routes (P3, K2) |
| `RefreshRequest` / `RefreshResponse` | the presented refresh token and a signature by the device key over it; back come the successor token, a custom token, its life, the role and the policy again | the app (M3) and the refresh route (P4) |
| `RestoreChallengeRequest` / `RestoreChallengeResponse` | an empty request; back come a challenge and its life | the app (K2) and the restore route (K2) |
| `RevokeSelfRequest` / `RevokeSelfResponse` | the token and its signature; nothing comes back | the app (M3) and the revoke route (P7) |
| `Invite` | the nonce, the email, the role, the accepted providers and the expiry | `fugaro pair` writes it (P2); the pair route spends it |
| `Member` | the first `(email, provider, sub)` this project accepted | the member store (P3), read by the policy |
| `PairedEntry` | the project's opaque id, its endpoint, the identity used and the consumer key's fingerprint | the app's list (M1, K3) |
| `KeySet` / `KeySetHeader` / `JWKS` | the payload's format, `seq`, `iat` and `exp`, the JWKS, and the JWS header naming the root key | the key-set signer (A4), verified by the verifier's fetcher (P1) |
| `Error` | one of the codes below, the accepted-provider list when the code asks for it, and a sentence | every route |

`SessionPolicy` is what the phone has to know about the project to behave: the modes the project treats as costly,
whether it wants a biometric there, and where it compacts. It is carried on a session response and again on every
refresh response, because a project that turns a costly mode on must not need every phone to pair again (P6, Z2).

The secure-store item names are prefixes — `fugaro.identity-session.`, `fugaro.device-key.`,
`fugaro.project-session.` and `fugaro.consumer-key.` — one item per signed-in identity and per paired project, and the
identity's id follows the prefix (D2790). They are in the contract because two sides write them: an item written under
one spelling and read under another is a session that disappears at the next launch.

The prefix ends in a dot, and `IdentityID` joins with one, because these names are keychain keys: `expo-secure-store`
accepts only letters, digits, `.`, `-` and `_` and **throws** on any other byte rather than reporting a miss, so a name
outside that alphabet is a session the phone cannot write at all. `IdentityID` therefore refuses a `provider` or a `sub`
carrying a byte outside it instead of composing one, and only the alphabet is restricted: a dot inside a part is fine,
which is what an Apple subject id needs.

## The refusals

| Code | Answered when | By |
| --- | --- | --- |
| `provider_not_accepted` | the provider the assertion names is not in the project's accepted list; the response carries the list | the policy |
| `auth_too_old` | the assertion's `auth_time` is older than the accepted sign-in age | the policy |
| `not_a_member` | the email is not on the roster, or the assertion's `sub` is not the one this project first recorded for it | the policy |
| `nonce_expired` | the invite's nonce is unknown, already spent, or past its life | the pair route, off the invite store |
| `strict_new_device_needs_qr` | the project is in strict mode and the device is not one it knows | the restore route |
| `device_revoked` | the refresh family or the device was revoked | the refresh and restore routes |
| `mobile_disabled` | mobile is off for this project | the policy |

The set is closed: the app renders one sentence per code, and a code the service sends and the phone does not know
would be a refusal shown as a crash. A refusal is never a partial session — no role, no endpoint, no token — so a
caller that ignored `Allowed` still gets nothing that admits anybody.

## What each side may rely on

**The app (M1-M3, K1, K3).** That an assertion it holds is one of these shapes and nothing else; that its refresh
token rotates and that the successor is the only one that works; that a custom token lives an hour and is exchanged
for an ID token the Firebase rules read; that the consumer public key in a session response is the one to pin against
the fingerprint the QR carried; and that the policy values on the response are the project's current ones. It may
rely on the audience derivation being the one in the vectors — a phone that spelled an endpoint differently from the
service would sign an assertion for an audience nobody serves.

**The assertion service (A1, A2, A4, K1, P2).** That the claims, the envelope's header and payload fields, the
format tag and the sequence are these, in this order, serialised as the vectors show: the envelope's signing input is
the base64url of the header and of the payload exactly as the shapes serialise them, and the vectors carry one real
Ed25519 signature over one. It may rely on the app verifying that signature against the pinned root key, and on a
fetched key set being accepted only inside such an envelope.

**The verifier and the project's routes (P1-P4, P7).** That the app sends what these shapes say and reads what they
say; that the device key in a pair request is the one that will sign every later refresh and revoke (O6); that the
policy is asked once, the same way, by pair, refresh and restore; that the compare-and-set store is what makes a
`jti` and a challenge single-use across the service's instances; and that a project's refresh families live one node
per device under `config/admin/refresh`, keyed by the device's uid (`RefreshRoot` and `RefreshFamilyPath`, so P4's
store and P7's admin revoke agree on where a family is). The routes answer a refusal with one of the codes above and
never with a sentence alone. The family path is in the contract rather than in either side because a store that kept
its families elsewhere would make the admin revoke delete nothing, report no error and leave a stolen phone
refreshing (D2794, F32).

**The stub decides nothing, and no side may rely on it.** `stub.go` returns each shape's fail-closed zero value: no
claims, no admission, no record, no token, no mint, `false` from every store that reports whether something happened,
and no credential in a session response. A side written in parallel injects its own double; a side that read an
answer out of the stub would be reading a decision nobody made. I1 deletes that file and wires the real sides.

## What no side may rely on

**Nothing in these shapes is a credential.** The vectors' values are readable placeholders, the keys in them are
public, and the one signature was made with a throwaway key whose private half was never written down. A new fixture
that looks like a credential is a bug: a value that reads like one gets copied into a test, then into a report.

**Nothing here decides who may sign in.** The membership rule, the accepted-provider rule, the authentication age,
whether mobile is on and the recorded `sub` are P6's and the policy's; this package fixes what a policy is asked and
what its answer looks like, never the answer.

**The role in a custom token is display only.** The Firebase rules read the device node and the consumer resolves the
role from the roster again at every action (D2791, O14); no side may treat a claim as an authorization.

**The strict switch's state is not in a session response.** The pair response does not say whether the project is
strict, and no shape here has a field for it. CR1 in the plan's *Contract changes anticipated* is the amendment that
would add one, if P3 finds the phone needs to know; until then a side that needs it asks, and does not read a field
that is not there.

## Changing the contract

These files belong to the contract and its changes alone. **A wire field never changes meaning**: adding a field is
fine, removing or retyping one bumps this contract, and a field whose meaning must change gets a new name — the
console, the mobile app, the history and the plugin read these documents, and a reused name draws a wrong figure with
no error. A change lands with all of it: the shapes, the vectors and both readers (the Go round-trip test and the JS
loader test), the prose here, and the sides it reaches in the same sweep.

CR1 and CR2 in the plan are the two amendments its own tasks are expected to want — the strict-switch state on the
pair response, and a policy value the phone turns out to need. Either reaches the sides named there; a launched task
gets a follow-up run on its pull request naming the changed file by path. A change no reader is told about is a phone
and a service that disagree in the field rather than in a test.
