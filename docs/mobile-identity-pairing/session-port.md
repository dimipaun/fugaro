# The app session port

Task **C2** of [implementation-plan.md](implementation-plan.md). It builds the port the plan's C2
names, in `shared/mobile-session/`, and the four documents of this folder are the spec, what the
repository holds, the plan and this one. Where this page disagrees with the
[plan](implementation-plan.md), the plan wins; where either disagrees with
[reconciliation.md](reconciliation.md), the reconciliation wins.

**Status: built; nothing consumes it yet.** The module, its stub and its test exist and pass, and
no app surface reads them. C2 depends on nothing, so this is a contract rather than the first
consumer's code -- which is the point of writing it first: a port drawn next to its implementation
would carry whatever that implementation happened to expose, and J2, J3, J4, X1, Z1 and J5b would
all inherit it.

## What the port is

One paired project's session, as every surface in the app may see it. The module is
`shared/mobile-session/port.js`; it is **a shape and a validator**, not an implementation. It has
no transport, no refresh loop, no clock, no store, no key and no import at all, and the phone can
load it because React Native's bundler resolves no `node:` builtin.

The plan names these files rather than a module `index.js`: C2's file list is `port.js`, `stub.js`
and `port.test.js`, so `port.js` is the one a consumer imports and the stub imports it too.

**M3 implements it** in `mobile/src/session/index.js`: the device key, the hourly refresh through
the project, the rotating refresh token and the sign-out everywhere of D2783 and D2794.
**`shared/mobile-session/stub.js` stands in for it** until then, and I2 deletes the stub once the
surfaces mount the real session.

What a surface may read, and nothing else -- an unknown member is refused, not ignored:

| Member | What it is |
| --- | --- |
| `project_id` | the project's **opaque** id, the one C1's paired-list entry carries; a readable name stays on the device (D2785's list shape) |
| `endpoint` | the project's pairing endpoint, where a refresh is presented; it must spell `https://`, because a device-bound refresh token sent over a cleartext scheme is a token disclosed |
| `role` | the roster's role, one of `viewer`, `operator` and `admin` (`internal/access`'s own names) |
| `policy` | the three values C1's session response carries (below) |
| `idToken()` | the project's access token, refreshed when the held one is near its hour |
| `signMessage(bytes)` | a signature over `bytes` made with the device key (O6) |
| `on(event, listener)` | subscribes to the one event a surface may rely on; it returns the function that unsubscribes |

`assertSessionPort(value)` answers whether a value is such a session: it throws a `SessionPortError`
on a refusal and otherwise returns the value unchanged, so a surface mounts with
`const session = assertSessionPort(raw)` and never holds a wrapper this page does not describe.

## The role is a display claim, never an authorisation

F14 and D2791 leave enforcement where it was: the **roster** is the one source of membership and
role (`config/admin/access`, read by `internal/access`), the device node's role is rewritten from
it at every refresh, and what a role may do is decided by the P5.1 consumer with `ActionLaunch` and
`ActionCancel` -- never by a role carried in a message. The port carries the three role names so a
surface can hide what its session cannot do, and carries nothing that could be read as a grant.

## The policy values

The values are the only policy a surface may read, and their names are the project policy's
`mobile.*` fields (P6) with the `mobile.` prefix stripped, so the session response and the policy
file cannot drift into two spellings of one value:

* `costly_modes` -- a list of rigor mode names. J3's confirmation policy needs a read-back for a
  start in one of them, and with `biometric_on_costly` the biometric path too (D2786).
* `biometric_on_costly` -- the project's switch, off by default, for that biometric re-auth.
* `compaction_threshold` -- **a fraction of the session's context window**, or `null` when the
  project sets no threshold; Z2 renders it as a percentage. The unit is fixed here rather than left
  to whichever surface formats it first.
* `crash_reports` -- whether the app may send crash reports to the project's Firebase project
  (`mobile_crash_reports`, the published policy's top-level key beside the `mobile` block, on by
  default and opt-out; D731, D738 -- the key is deliberately not a member of the strictly-decoded
  block, so an older reader tolerates it into `UnknownKeys` rather than refusing the policy). It is
  a **required boolean** and never an absent member: nothing is sent before the switch has been read
  (D734), so a session that did not carry it would be one the app could not tell from `false` -- and
  the reader answers an absent member off for exactly that reason
  (`internal/projectpolicy.MobileCrashReportsOn`). The
  switch decides the app's one collection call site and nothing else; Firebase Analytics is not in
  the app at all (D731).

## `idToken()` and the refresh

`idToken()` resolves to the access token this project's session holds, refreshing through the
project when the held token is near the end of its hour (D2783: a Fugaro refresh token, rotating
and device-bound, with a one-hour token minted per refresh). It is a promise, because a surface that
asked for a token must be able to wait for the refresh behind it.

Two things the port does **not** carry, and they are the reason the member list is closed:

* the **refresh token**: it lives in M3's store and is reached only through `idToken()`, so no
  surface can read it, log it or copy it;
* the **Firebase refresh token**: D2783 forbids the app to keep one at all, and a session smuggling
  one through the port would be the same leak with a different name.

## `signMessage(bytes)` and the device key

`signMessage(bytes)` resolves to a 64-byte Ed25519 signature over exactly the bytes it was given --
the port neither canonicalises them nor copies them, because the message's canonical bytes are the
mailbox schema's (P6.3) and the gate that verifies the signature is J5b's. O6 replaced D171's shared
MAC with this per-message device signature, and the device node's public key, stored at pairing, is
what verifies it. The key never leaves the session: a surface holds a function, never key material.

## `signedOut`

`signedOut` is the one event a surface may rely on. M3 fires it when the project's session can no
longer be recovered -- the case the plan names is a refresh whose token another device rotated out,
which fails and signs the project out first (D2794). A surface listening for it stops using the
session; there is no "signed back in" event, because a new session is a new port value.

`signedOut` is fired **once**: a signed-out session stays signed out.

## Fail closed

Every way a value can fail to be this session has its own code, and a refusal names the member and
never the value -- a session's fields include tokens, and a refusal that echoed one would be a path
for it to travel.

| Code | Refused |
| --- | --- |
| `not-a-session` | the value is not a plain object at all |
| `unknown-member` | a member the contract does not name is present |
| `missing-member` | a member the contract names is absent |
| `bad-type` | a member is present as the wrong kind of value |
| `bad-project-id` | a project id that is empty, padded, non-ASCII or oversize |
| `bad-endpoint` | an endpoint that does not spell `https://`, or is padded or oversize |
| `unknown-role` | a role the roster does not have |
| `unknown-policy-field` | a policy field the contract does not name |
| `bad-policy-value` | a policy value outside its shape |
| `unknown-event` | a surface subscribed to an event the port does not carry (thrown by `on`) |
| `bad-listener` | a surface subscribed something that is not a listener (thrown by `on`) |
| `signed-out` | a session was used after `signedOut` (rejected by `idToken` and `signMessage`) |

## The stub, and its driver

`stubSession()` returns a **fixed fake session**: the same project id, endpoint, role, policy values
and token on every call, and a 64-byte signature that signs nothing and does not look at its input.
`assertSessionPort` runs it, so a stub that drifts from the port fails at once rather than teaching
three surfaces a shape M3 will not have. It touches no key, no store, no clock and no network, and
it proves nothing about M3: a test that passes against it says a surface reads the port, not that a
refresh or a signature is real.

A surface subscribes to `signedOut` and stops, and a test has to be able to make that happen -- but
the listener belongs to the surface, so the test cannot reach it. `stubSignOut(session)` is that
seam: it fires the event once, and it refuses a session the stub did not build. It is not part of
the port, no surface calls it, and it is deleted with the file.

## What reads this

`shared/mobile-session/port.js` is the contract and `shared/mobile-session/port.test.js` is the pin:
it checks every member, role, policy value, event name and code above against this page, so the
prose cannot drift from the module. `session-port.md` is the same page C1's `contract.md` is for the
Go side; a value a consumer finds missing is a contract change (CR2), not a local addition.
