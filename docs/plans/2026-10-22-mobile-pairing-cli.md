# `fugaro pair`, the consumer's enforcement and the sweeper's prompt rule — the mobile P6.6 / P6.7 / P6.13 CLI plan

**Status: a plan. Nothing here is approved beyond the approval the mobile plan already records,
nothing here is built, and this change writes no code.**
This document is the **CLI plan console D19 requires** for the three tasks
[mobile/implementation-plan.md](../mobile/implementation-plan.md) names as "written in a CLI plan",
and it is the only deliverable of this change: it writes down what `fugaro pair` will be, what the
consumer's enforcement of the D128 amendment will be, and what the sweeper's rule for waiting
prompts will be, so the owner can approve or refuse each part. It implements nothing, it changes no
command that exists, and it ships **no test** (owner decision 10 states that cost).

**What the mobile plan already approves, and what it does not.** The owner approved all of Phase 6
on 2026-10-18, so **P6.6, P6.7 and P6.13 are approved tasks with no approval of their *shape*** —
and this plan is that shape. Nothing below is a new approval: where this document diverges from the
task text (owner decisions 1 and 2 do), it says so and asks.

**Amended 2026-10-29: re-read against `main` after the ee/ re-organisation; the text below is kept
as written on 2026-10-22, and each place a later change reaches carries its own dated note.** What
moved under it, in one list: (1) **P4.1 has partly landed** — `internal/mailbox/mac.go` (T2, T2a),
`internal/mailbox/keychain/`, `lease.go` (T3), `ledger.go` (T4) and `outbox.go` are merged, and the
`mailbox:` block of `internal/localcfg` holds **one** `keychain_item`; `internal/cli/mailbox.go`,
`internal/mailbox/expiry.go`, the hidden sweeper and the parity corpus are still unbuilt.
(2) **The CLI reference is generated from the tagged tree** — `go run ./ee/clidoc`, with
`TestCliReferenceIsCurrent` in `ee/clidoc/cliref_test.go` and the core's subset half in
`internal/clidoc/coreref_test.go`. (3) **The rules generation is 4** (`projectpolicy.RulesVersion`,
the coordinator's read grant); the pairing and device nodes this plan quotes are unchanged, so M14
still holds. (4) **`fugaro pair` is placed in the MIT core** (`internal/cli`) by this plan; the
licence split since (LICENSING.md, `ee/README.md`, D637's split for `fugaro user`, the Subscription
Terms §5.2 "the Command-line tool is licensed under the MIT licence") is consistent with that, but
subscriptions task S5.1 lists "Pairing: `fugaro pair`" inside the roles design (D446) without
placing it, so the placement was the owner's to confirm (§18, decision 12: **answered 2026-10-29, core**). (5) **The role and
the per-pairing keychain item** met later work — the Terms' viewer, operator and administrator
roles (§5.3), D628's minter, and the merged one-item config — and are decisions 9 and 13, **both
answered 2026-10-29** (one keychain item per pairing; the role inherited from the pairing user).
Nothing in this list approves a task or changes one.

**Merged on `main` today, and this plan is written against it — every one of them read, not
recalled:** **P6.4** (rules generation 3: the `pairing/$uid` subtree, the
`config/admin/devices/$uid` node `USER` now requires, `RulesVersion = 3`) with
`internal/budget/rules/emulator_pairing_test.go`; **P6.3**'s schema (`mac` on every message of every
kind and on the heartbeat, the action record, `canonicalBytes`, `vectors.json`); **P6.2**
(`shared/confirm` and its `vectors.json`); **P6.5** (`shared/prompts`, `PROMPT_TTL_MS`); and
**P4.1's `internal/mailbox/message.go`** — the message binding and its vectors, which is T1 of the
merged P4.1 plan. **Not built, and this is the whole of what these three tasks build:**
`internal/cli/pair.go`, `internal/budget/token/device.go`, `internal/budget/sweep_device_test.go`,
`internal/cli/mailbox.go`, and every file under `internal/mailbox/` except `message.go`. So the
rules **can** hold a pairing session today and **nothing can start one**: `p~` is a prefix the
template and `pairingClaim` admit and no program mints. *(Amended 2026-10-29: of `internal/mailbox/`,
`mac.go`, `keychain/`, `lease.go`, `ledger.go` and `outbox.go` have since merged as P4.1 T2, T2a, T3
and T4; `internal/cli/pair.go`, `internal/budget/token/device.go`,
`internal/budget/sweep_device_test.go`, `internal/cli/mailbox.go` and `internal/mailbox/expiry.go`
still do not exist, and nothing can start a pairing session.)*

**The three tasks, and their file boundaries are not this plan's to draw — they are already drawn.**
`docs/plans/2026-10-18-mobile-mailbox-consumer.md` (the merged P4.1 plan) §8.4 is a table assigning
every row of the MAC work to P4.1 or P6.7, and its owner decision 8 assigns the two store-side rows
wholesale: **P6.13** adds the stored-prompt deletion to `internal/cli/mailbox.go` "and nothing
more", **P6.6** adds the `m~` rule to `internal/budget/sweep.go`. This plan **respects both** and
takes nothing the table assigns away; where the tree makes the `m~` rule insufficient it says so as
an owner decision (decision 1) rather than widening the task in silence.

**The mobile-auth folder is accepted as recommended (D221–D236), and this plan absorbs the parts
that land on these three tasks.** The owner's ruling of **2026-10-22** — **D221–D236 accepted as
recommended**, with **P0/P1 approved** and **P3–P5 ordered after P6.6/P6.8** — was relayed to this
change and is **recorded in the tree by this change**:
`docs/mobile-auth/README.md`'s dated note of 2026-10-22 carries it, with the provenance, because the
folder did not carry it before (its three status lines of 2026-10-21 are kept as the state they
described, each with a one-line pointer to that note). **This plan cites that note and asserts
nothing of its own** — a plan that treated an unrecorded ruling as an approval would build on a
decision the tree cannot show, and the reviewer's job here is to check the note rather than this
sentence. So P6.6 is the task the folder's P3 extends, and nothing in the folder is built. Three of
its rulings reach this plan and are written into it: **D226** (pairing refuses an unverified email,
using the check `internal/cli/init_members.go`'s `userEmail` already performs), **D224** (the email
is resolved once, in the CLI, at the write) and **D225** (the role is a field on the node that no
rule reads in v1).
**#787's amendment of 2026-10-20 is the frame the whole document sits in** — *pairing is discovery,
not identity* — and it is quoted in §4.4 and §12 rather than paraphrased.

**Owner ruling of 2026-10-22, recorded here for the first time: D199 — the installation's Firebase
config travels in the encrypted pairing payload.** It sits in the free block
`docs/coordinator/README.md` reserves ("D198 onward is free"); D200 alone may be held. §4.4 states
it, states the tension with the mobile functional spec's "the QR carries the Firebase configuration
and a short-lived pairing token" (#787's own edit 1 keeps that sentence), and resolves it: **the QR
is the discovery pointer, the delivery is the stored record.** Owner decision 5 is the resolution
written as a choice.

**The export-compliance revisit is real and this plan carries it** (§13). The owner's declaration of
2026-10-20 — `mobile/app.config.js` merges `{"ios": {"infoPlist":
{"ITSAppUsesNonExemptEncryption": false}}}` and the owner's own word was "write it, it's OK" — is an
answer about the app **as it stands**, and pairing is what changes the facts under it: the phone
generates an X25519 key pair and opens an AEAD envelope. §13 says what has to be revisited and by
whom, and states that the memory file the task named
(`mobile-telemetry-and-store-forms.md`) **could not be read in this container** — the memory
directory is empty here — so §13 is grounded in the tree's own files instead, each cited.

Related: [mobile/implementation-plan.md](../mobile/implementation-plan.md) (P6.6, P6.7, P6.13 — this
plan's authority — and the dependency edges `P4.1 → P6.6`, `P4.1 → P6.7`, `P6.7 → P6.13`),
[plans/2026-10-18-mobile-mailbox-consumer.md](2026-10-18-mobile-mailbox-consumer.md) (the merged
P4.1 plan: §8.4's split, §13's ownership table, decision 8),
[mobile/functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md) §3 (D165,
D166, D169, D171 and the D166 note of 2026-10-20) and §4,
[mobile/functional-spec-v0.1.md](../mobile/functional-spec-v0.1.md) §4.1–§5 (the seven amendment
notes, "pairing is discovery, not identity"),
[mobile-auth/reconciliation.md](../mobile-auth/reconciliation.md) (D221–D236, accepted as
recommended), [mobile/reconciliation.md](../mobile/reconciliation.md) (D110–D129, D118's TTL),
[design/v1.md](../design/v1.md) §9.1 (the command table, `--json`, the exit codes),
[design/voice.md](../design/voice.md) (F38).

---

## 1. What is built, and what it reuses

**Built, in one sentence each.**

- **`fugaro pair`** (P6.6) is a **visible** CLI command an operator runs beside the phone: it mints
  a one-shot pairing token for a fresh `p~<random>` uid, shows a QR holding the project's bootstrap
  configuration and that token, waits for the phone's public key in `pairing/<uid>/request`
  and for the operator to type the code the phone shows, and then — only then — mints the device
  token (`m~<random>`, `mu = uid`), writes the device node `config/admin/devices/$uid`, generates
  the per-pairing MAC key into the OS keychain, and delivers the token, the key and the
  installation's configuration **encrypted to the phone's key**. `fugaro pair ls` lists the paired
  devices; `fugaro pair revoke` deletes a device node and the keychain item.
- **The consumer's enforcement** (P6.7) is **not a new command**: it is the inbound half of
  `internal/cli/mailbox.go`, the command P4.1 builds. It verifies every inbox message's MAC before
  anything else, re-validates every action record against `shared/confirm`'s rules with a Go
  implementation held to `shared/confirm/vectors.json`, keeps a restart-surviving ledger of used
  message, action and confirmation ids and of prompts expired or answered, persists its pending
  prompts locally, and expires them on its own clock.
- **The sweeper's rule for waiting prompts** (P6.13) is **one delete set** in the same file: the
  real, owner-run sweeper deletes a stored prompt past D118's TTL, and does nothing else. It never
  answers a conversation.

**Reused, and where it already is.**

| What | Where it already is | What this plan does with it |
| --- | --- | --- |
| The pairing nodes and their grants | `internal/budget/rules/rules.json.tmpl` (generation 3) | **reads** them, §2 (a); adds no generation |
| The device node and `USER`'s existence test | `rules.json.tmpl`'s `config/admin/devices`, `internal/budget/rules/rules.go`'s `userClaim`, `PathDevices` | **reads** them; P6.6 writes the node over IAM |
| The `p~` / `mu` claims and the `PAIRING` macro | `rules.go`'s `pairingClaim`, the template's `PAIRING` uses | **reads** them; P6.6 mints both claims |
| Custom-token minting | `internal/budget/token`'s `Mint`/`MintAt`, `Lifetime`, `UID` | **reuses** the signing path, the token encoding and `parseJWT`. *(Amended 2026-10-29: not `Mint` itself — `Claims.validate` needs a `Slug` and a `Run` that `ValidSegment` refuses a `~` in, plus `FP` and `RB` a pairing token does not carry — so T1a's `MintPairing` sits beside it in `internal/budget/token/pairing.go` and adds the `p~` shape; the `m~` shape and the delivery envelope are T2's and T3's.)* |
| A versioned AEAD envelope with AAD | `internal/budget/token/seal.go` (`Seal`/`Unseal`, `KeySize`, `EnvelopeVersion`, `nonceSize`, `tagSize`, `aad`) | **reuses the shape and the reasoning**, not the key-based API — §8 |
| The sweep's user deletion and its report | `internal/budget/sweep.go`'s `deleteUsers`, `uidPrefix`, `SweepReport`, `RunUID` | **extends** the prefix test and the report — T4, decisions 1 and 6 |
| The message binding and its vectors | `internal/mailbox/message.go` (P4.1 T1, merged) | **reused as-is**; the MAC function itself is P4.1 T2/T2a, unbuilt *(amended 2026-10-29: merged — `mac.go`'s `MacOf`, `VerifyMAC` and `NewOutboxSigner`)* |
| The confirmation machine and its vectors | `shared/confirm` (`index.js`, `vectors.json`), merged (P6.2) | **read** by P6.7's Go re-validation; never edited |
| The prompt TTL and the queue | `shared/prompts`' `PROMPT_TTL_MS`, merged (P6.5) | **read**; the Go side declares no second number (§11) |
| The mailbox consumer | `internal/cli/mailbox.go`, P4.1 T5–T7, **unbuilt** (still, 2026-10-29) | **extended** by P6.7 and P6.13, per §8.4's table |
| The hidden-subcommand precedent | `internal/cli/history.go`'s `newBudgetHistoryCmd` (`Hidden: true`) | **followed** for `fugaro mailbox sweep`; `fugaro pair` is **visible** |
| The retired-verb rename | `internal/cli/retired.go` (`fugaro init` is now `fugaro setup` / `fugaro update`) | **obeyed**: this plan names the live doors |

---

## 2. What the merged rules and the merged tree already decide

Everything below is **quoted from the tree on this branch** and is not this plan's to change. A
task that needs one of these edited is a finding, not an edit.

**(a) The pairing subtree, `internal/budget/rules/rules.json.tmpl` (generation 3):**

```json
"pairing": {
  "$uid": {
    "request": {
      ".read": false,
      ".write": "PAIRING && newData.exists() && !data.exists()",
      ".validate": "newData.hasChildren(['key', 'at'])",
      "key": { ".validate": "PUBKEY" },
      "at": { ".validate": "TIMESTAMP" },
      "$other": { ".validate": false }
    },
    "delivery": {
      ".read": "PAIRING",
      ".write": false
    }
  }
},
```

Four consequences, each load-bearing below.

1. **The request node is write-once and closed to every field but two.** `!data.exists()` makes the
   first write win, and `"$other": { ".validate": false }` means a phone that also writes the code
   it is showing, or its device name, is **refused by the deployed document** — so the code is
   **never written to the database** (decision 7).
2. **`.read: false` on the request node** means **no token can read it**, including the pairing
   token. The CLI reads it with **database admin** (§6, §7.1 step 4).
3. **`.write: false` on the delivery node** means **no token can write it**, including the pairing
   token. The CLI writes it with **database admin** too — the phone only reads.
4. **The delivery node has no `.validate` and no `$other`.** Its payload's shape is therefore
   **unconstrained by the rules**: D199's Firebase config, the device token and the MAC key fit
   without a rules change, and **no generation is forced** (§12). It also means nothing in the rules
   would stop a *secret* being written to it — which is why the envelope is the only thing standing
   between the payload and every launcher who can read the node's ciphertext (§8).

**(b) The device node, `rules.json.tmpl`:**

```json
"admin": {
  ".read": false,
  ".write": false,
  "devices": {
    "$uid": { ".read": false, ".write": false }
  }
}
```

Read with **(c)**: `internal/budget/rules/rules.go`

```go
const userClaim = "(auth != null && auth.token.mu != null && auth.token.mu == auth.uid && root.child('config/admin/devices').child(auth.uid).exists())"
```

**The device node's existence is the whole of what the rules know about a device.** The node is
client-unreadable and client-unwritable, it has **no `.validate` and no `$other`**, so its children
— D226's administered `email`, D225's `role`, whatever a name field is called — are unconstrained
and need **no rules generation** (mobile-auth §F29; §12). It also means the rules **cannot** apply
D223's domain rule: an RTDB key may not contain `.` and the rules language has no string rewrite, so
the domain half is a predicate over `auth.token.email` whose dynamic form is unverified
(mobile-auth §F4) and the check is made **at pairing, in the CLI** (D226).

**(d) The pairing claim, `rules.go`:**

```go
const pairingClaim = "(auth != null && auth.token.pu != null && auth.token.pu == auth.uid && auth.uid.beginsWith('p~'))"
```

Its own comment is the ruling this plan inherits: *"D165's binding is the operator-typed fingerprint
of the key in the request, and a launcher that cannot open the ciphertext learns nothing from it."*
**That sentence is why the fingerprint's exact shape is a decision and not a detail** (decision 3):
the rules hand the binding to a human comparison, so the comparison has to be unambiguous.

**(e) The key's bound and alphabet, `rules.go`:**

```go
// MaxPublicKey bounds the public key a pairing request carries (the encoded
// X25519 public key is ~44 characters; 4096 is generous and still a bound).
MaxPublicKey = 4096
...
// patternPublicKey is the pairing request's key: base64 or base64url text.
patternPublicKey = `/^[A-Za-z0-9+\/=_-]+$/`
```

**Two things follow.** The rules accept **base64 *or* base64url, padded or not** — several
spellings of one key — so the CLI must pin **one** (unpadded base64url of the raw 32 bytes, 43
characters) and derive the fingerprint from the **decoded bytes**, never from the text, exactly as
`internal/budget/token/seal.go`'s `keyEncoding` pins one spelling for the seal key and argues why
("three other characters decode to the same 32 bytes … and a marker's value would have three
spellings the redactor never learned"). And the key is **X25519** by the rules' own comment, so the
key agreement is X25519 and not a new invention.

**(f) The device allowlist, `rules.go`:**

```go
const PathDevices = "config/admin/devices"
```

Its comment names P6.6 as the writer and states the whole revocation argument: *"deleting this node
revokes at once … Deleting an Auth user does not revoke on its own — an issued ID token keeps
passing until it expires — which is the whole reason D166 chose a node."* P6.6 **reads this
constant rather than spelling the path again**.

**(g) The sweep's user deletion, `internal/budget/sweep.go`:**

```go
const uidPrefix = "r~"
...
if strings.HasPrefix(u.UID, uidPrefix) && !keep[u.UID] && now.Sub(u.Created) > maxAge {
```

and its header: *"It deletes only Firebase users named r~<slug>~<run> that the Identity Toolkit says
were created more than UserMaxAge ago…"*. `SweepReport` is `{Removed []string; Kept int;
UsersDeleted int}` and `RunUID` is `"r~" + slug + "~" + run`. **The sweeper is the history job's
`--sweep` mode and "holds database admin rights"** — and `iam.tf`'s `history_auth` binds
`roles/firebaseauth.admin` to the history job's account and to nobody else in Terraform. That fact
is the whole of owner decision 1.

**(h) The token package, `internal/budget/token/token.go`:**

```go
Lifetime = time.Hour
...
func UID(slug, run string) string { return "r~" + slug + "~" + run }
```

`Lifetime` is *"how long a custom token is valid (Firebase's maximum)"* — and, because a phone
exchanges its `p~` token within the five minutes D165 allows and then holds an **ID token** for up
to an hour, `Lifetime` and not the five minutes is the real bound on a pairing session (decision 1).

**(i) The existing envelope, `internal/budget/token/seal.go`:**

```go
KeySize = 32
EnvelopeVersion byte = 1
nonceSize = 12
tagSize = 16
minEnvelope = 1 + nonceSize + tagSize
```

with the envelope `version byte || nonce || ciphertext || tag`, AES-256-GCM, and AAD = the object's
name followed by a zero byte and the version byte. §8 reuses this shape and argues why the pairing
delivery needs a **different key** (agreed, not shared) and so a different API.

**(j) The one `root.AddCommand` line, `internal/cli/root.go`:**

```go
root.AddCommand(
    newVersionCmd(), …, newProjectCmd(), newAllowDeleteCmd(), newManagedCmd(), newRestoreCmd(), newPlanCmd(),
)
```

§5 says what P6.6's addition drags with it. *(Amended 2026-10-29: the list now continues past
`newPlanCmd()` — `newReaperCmd()`, `newBacklogCmd()` and the hidden `newCredentialsCmd()` — and
`internal/cli/extend.go`'s `RegisterCommand` is the seam an `ee/` command joins the tree through
instead; a core command is still one line in this list.)*

---

## 3. Rulings (settled here; each says what it costs if wrong)

### M1 — Three tasks, one plan, one owner of every file, and this document ships no pin

The three tasks are one plan because they are one wire: P6.6 generates the key P4.1 signs with and
P6.7 verifies, and P6.13 deletes the prompts P6.7 expires. §16's table gives each file one writer.
**This document ships no test.** The merged P4.1 plan shipped
`internal/cli/docs_mobile_mailbox_plan_test.go`, a pin that derives its citation list from the plan
at test time; the owner's ruling for this change is **docs only, no code**, so nothing reads this
document and its citations are a duty of the review rather than a red test. **Cost if wrong:** the
citations and the §8.4 references can rot silently, and a reviewer is the only guard. Decision 10
records it.

### M2 — The pairing session is one-shot by construction and **not** by time; two leftovers, two owners

The request node is write-once and the delivery node is read-once **in practice** (one payload, one
reader), but **nothing in the rules puts a clock on the session**: the `p~` uid may read its
delivery node *"for as long as its Auth user exists"*, and the Auth user outlives the five-minute
token because the exchange yields an hour-long ID token (D165's `exp` bounds the exchange, not the
session). So a pairing session that is never completed leaves **an Auth user and a node** behind, and
nothing else removes either. **Two leftovers, two owners, and the CLI owns the node alone:** on
success the CLI deletes the **`pairing/$uid` node** (§7.1 step 9) and **never the `p~` Auth user** —
the role that takes, `roles/firebaseauth.admin`, is the history job's alone, which is decision 1 —
and the **sweep** deletes the Auth user, and the node too when the session was abandoned
(decision 2). **Cost if wrong:** orphaned `p~` Auth users accumulate for the life of the project —
invisible, unbilled, and each one a uid that `pairingClaim` would still admit if its ID token
survived (it does not, but nothing in this plan should rest on that).

### M3 — The typed code is a fingerprint of the **decoded key**, compared by a human; it is not a proof

Both ends derive the code from the same input: the raw 32 bytes of the phone's X25519 public key.
The CLI prints **the code it computed from what it read in the request node** beside a prompt for
the typed one, and refuses on any mismatch **before any side effect** — no device node, no token, no
key, no delivery. The code is **never written to the database** (the template's `$other` refuses it).
It is **not an identity** and it authorises nothing by itself (#787: pairing is discovery, not
identity); what it defeats is a photographed QR being paired in place of the real phone, because a
copier cannot make the operator's phone display the copier's key's code. **Cost if wrong:** if the
code is too short or too ambiguous to compare, the one binding `pairingClaim`'s comment names is
weakened to a formality. Decision 3 fixes the shape.

### M4 — The device token's claim is `mu`; the allowlist is the node; revocation is node first

P6.6 mints the device token with `uid = mu = m~<random>` (never `r~`, which is the runs bucket's
prefix — `RunUID`'s and `token.UID`'s), writes `config/admin/devices/$uid`, and **writes the node
before it delivers the token**, so no token exists that `USER` does not yet admit. `revoke` deletes
the **node first** (immediate, D166/F5), then the laptop's keychain item, and **does not** delete
the Auth user (decision 1). **Cost if wrong:** delivering the token first leaves a window in which a
paired phone's every read and write fails `USER` for a reason the operator cannot see.

### M5 — The MAC key is generated by P6.6, held in the OS keychain, and never printed

One key per pairing (D171, tension (b2)), 32 bytes from `crypto/rand` — the same draw and the same
`EncodeKey`/`ParseKey` discipline `seal.go` already has, because it is the same kind of value. It is
written to the OS keychain under an item name the project's local config holds (P4.1's one
`internal/localcfg` field; the settings hold the **name**, never the value, Voice D42(2)). It travels
to the phone **inside the sealed delivery payload** and **never in the QR** (D171). It appears in no
log line, no `--json` document, no error message and no test fixture. **Cost if wrong:** the key
authenticates what the phone speaks aloud; a key on screen for the seconds the QR is shown is D171's
rejected alternative.

### M6 — The delivery needs an **agreed** key, so it is a new envelope and not `seal.go`'s API

`seal.go`'s envelope takes a **shared** key the launcher generated and carried in the marker. The
pairing delivery has no shared key: the phone's public key is the only thing on the wire, so the
sender performs X25519 with an ephemeral key pair and derives the AEAD key with HKDF-SHA-256. The
result is a **second envelope in the same package**, sharing `seal.go`'s shape (version byte first,
12-byte nonce, 16-byte tag, AES-256-GCM, AAD binding the object's name and the version) and **not**
sharing its `Seal(key, …)` signature. Both halves come from the **standard library** (`crypto/ecdh`,
`crypto/hkdf`; `go.mod` requires no new module — `golang.org/x/crypto` stays indirect). §8 has the
argument and the AAD's contents. **Cost if wrong:** a second AEAD written without the first one's
reasoning is exactly how a version byte stops being checked.

### M7 — P6.7 extends P4.1's consumer; there is no second command and no second MAC implementation

`internal/cli/mailbox.go` is P4.1's file and P6.7 adds to it — the per-kind clauses, the action
record, the ledger's new key spaces, the pending prompts, the expiry, the revoke, the heartbeat's
`agents` list — and **`internal/mailbox/mac.go` keeps one function** (`sign`/`verify` over
`canonicalBytes`), which P4.1 lands and P6.7 calls. The split is §8.4 of the merged plan, row by
row, and this plan adds nothing to either side of it. **Cost if wrong:** two implementations of a
MAC is two chances to disagree about what is covered. *(Amended 2026-10-29: the merged function's
names are `MacOf` and `VerifyMAC`, with `Signer`/`NewOutboxSigner` for the outbound half; "sign" and
"verify" above are the P4.1 plan's words for them.)*

### M8 — The sweeper's remit grows by exactly one delete set, and P6.13 writes it

P6.13's own words are "**and nothing more**": the sweeper deletes stored prompts past the TTL and
**never answers the conversation**. At P4.1 the sweeper's delete set is the consumer's own state; at
P6.13 it is that plus `mailbox/$mb/**` prompts past `PROMPT_TTL_MS`. **The consumer, not the
sweeper, expires a prompt and delivers its safe answer** (M9), because a sweep can run while the
consumer is down. **Cost if wrong:** a sweeper that answers is a second writer of the conversation,
and the "exactly once" guarantee becomes two writers racing.

### M9 — Expiry is the consumer's, on the consumer's clock, recorded in the restart-surviving ledger

A prompt's expiry is a pure function of its `at`, D118's TTL and the consumer's clock (D168 as
amended), and the delivery of its safe answer happens **exactly once**, recorded in the same ledger
that survives a restart — so a sweep that deleted the stored prompt while the consumer was down
changes nothing about what the orchestrator is told. `PROMPT_TTL_MS` is **`shared/prompts`' number**,
bound here and not declared here (P4.1's M3): if the Go side declares a duration, a test pins that it
equals the module's number. **Cost if wrong:** a second number for the TTL is a silent divergence
that only shows up as a prompt answered after it should have expired.

### M10 — The D128 amendment is enforced as a **table in code**, held to `shared/confirm`'s vectors

P6.7 re-validates every action record with a Go implementation of `shared/confirm`'s rules, held to
them by a parity test over the merged `shared/confirm/vectors.json` (there is no Node in CI —
Ruling M9 of the P4.1 plan — so the contract is replayed from committed vectors). The consumer
**first** verifies the MAC of every inbox message of every kind, and refuses before anything else.
`internal/mailbox/action.go`'s refusal table is a value; an unrecognised verb is refused by default.
**Cost if wrong:** the amendment becomes prose, and prose is what D169 exists to replace.

### M11 — The exit codes and the `--json` rule are P4.1's, unchanged

`fugaro pair` and its subcommands report with `--json` and use the CLI's own codes; **M12's
four-clause "nothing happened" predicate is the consumer's and does not apply to `pair`**, which
performs a single act and either performed it or did not. §4.6 states `pair`'s own three rows.
**Cost if wrong:** importing the consumer's predicate into a command that reads no mailbox invents a
refusal that cannot happen.

### M12 — Every one of the three tasks is SECURITY-SENSITIVE, and P6.13 is the highest tier

The mobile plan's Global Constraint 14 lists P6.6, P6.7 and P6.13 among the tasks that take
independent review, and gives P6.13 "**opus + independent review**" because *it decides the default
answer the orchestrator receives*. §15 carries the tier on every task; the reviewer assignments are
the owner's.

### M13 — The device node carries no secret, and that is a rule of the writer, not of the rules

`config/admin/devices/$uid` is client-unreadable, and its children are unconstrained (§2 (b)) — so
**nothing in the deployed document would stop P6.6 writing the MAC key or the device token into it.**
The rule is therefore in the writer: the node holds the **administered email** (D226), the **role**
(D225) and what the CLI needs to render `pair ls` — a name and the pairing time — and **no token, no
key, no fingerprint, no delivery**. A reviewer checks this against the writer, because no test in
this repository can. **Cost if wrong:** a secret in a node that every database-admin principal can
read, which is every launcher-level principal (`rules.go`'s `userClaim` comment says so).

### M14 — No rules generation is forced by anything in this plan

P6.6 writes `pairing/$uid/delivery` (no `.validate`), the device node (no `.validate`) and
deletes both; P6.7 writes only the mailbox's inbox/outbox/heartbeat, whose fields P6.4 already
validates; P6.13 deletes. **So no part of these three tasks changes `rules.json.tmpl`, the golden or
`RulesVersion`, and no owner-run deploy is forced by them.** This is the claim mobile-auth §F29 makes
for the node's new fields, extended to the delivery payload. A task that finds itself editing the
template is a finding. **Cost if wrong:** a generation is an owner-run deploy on every installation,
and forcing one for a payload the rules deliberately do not model would be a cost paid for nothing.

---

## 4. The command surface

### 4.1 `fugaro pair`

```
fugaro pair [--name NAME] [--email ADDRESS] [--role ROLE] [--timeout D] [--json] [--project NAME | --gcp-project ID]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--name` | string | `""` | the device's name, for `pair ls` and the node's own field |
| `--email` | string | `""` | **D226's administered email**; refused unless it is a verified address of the pairing operator (§12) |
| `--role` | string | **the pairing user's own role** | **D225's role**, a field on the node, read by no rule in v1. **Inherited, not free** *(amended 2026-10-29, decision 13 answered)*: the device's role defaults to the role of the user doing the pairing, and `--role` may only **lower** it (`admin` > `operator` > `viewer`), never raise it above that user's role; a word above it is refused. Until the roles design (D446/S5.1) provides verified roles, the pairing user's role is read from the CLI's existing lists (`internal/cli/roles.go`: launchers and operators), launcher mapping to the device word `operator`; the lists hold no viewer or admin, so a `viewer` device is reached only by lowering |
| `--timeout` | duration | `2m` | how long to wait for the phone's key and the typed code; **refused above `4m30s`** |

**`--timeout` is capped below D165's token, and the token is sized to the wait.** The pairing token's
`exp` is **5 minutes at most** (D165), so a wait longer than that would outlive the credential the
phone is holding and surface as an opaque token error instead of this command's own refusal. The
command therefore does both: it **mints the token to cover the wait** —
`exp = iat + min(5m, timeout + 30s)`, §7.1 step 3 — and it **refuses `--timeout` above `4m30s`**,
which is the five-minute ceiling less the 30-second exchange slack the phone needs after the operator
types. The default `2m` leaves room for a slow scan without approaching either bound.
| `--json` | bool | `false` | one JSON document instead of lines |
| `--project`, `--gcp-project` | string | `""` | the existing project selectors, `addCloudFlags`'s, unchanged |

*(Amended 2026-10-29, owner: `--json` streams newline-delimited events (`qr`, `waiting`, `code`,
`done`, `refused`), `v: 1`; `done` carries this document; the typed code is read as one stdin line;
the console's pairing screen is a later console release. **Amended again 2026-10-29:** the `qr`
event carries its payload **in cleartext**, so the one-shot pairing token and the Firebase web API
key do appear on stdout in it. That is the exception this section's "a token never does" rule needs
said out loud rather than assumed: the payload is the code's own content, the operator's screen
shows it anyway, the token is one-shot and dead in five minutes, and the API key is the restricted
web key the app's config already carries — while the MAC key, the device token, the keychain item
and the typed code stay out of every event, `qr` included.)*

`Args: cobra.NoArgs`. It prints `project: <name> (GCP <id>)` first, as every cloud command does,
then the QR, then the fingerprint prompt, then what it wrote. **Visible** in the CLI reference: the
operator has to be able to find it, and D166 makes it the only way to add or revoke a device (T3, CLI
only).

**Long text** states, in this order: what the command is and that it is an **admin act** (§6's two
role sets, named); that pairing is **discovery, not identity** and grants no access by itself
(#787); that the QR carries **no key** (D171) and that the final credentials go encrypted to the
phone's key; that **the typed code is the binding** and nothing is written before it matches (M3);
that **the MAC key never leaves the OS keychain in clear**, is never printed, and is not in the QR;
that the command writes the device node and the delivery node **with database admin** and mints
tokens **with the minter role**; and where its own state is.

### 4.2 `fugaro pair ls`

```
fugaro pair ls [--json] [--project NAME | --gcp-project ID]
```

Lists the children of `PathDevices`: for each, the uid (a key, and therefore printable), the
**name**, the **administered email**, the **role** and the pairing time. It reads with database
admin. **It never prints a token, a key or a keychain item's value** — and the item's *name* is not
in the node (M13), so `ls` prints what the node holds and nothing else. A node with no readable
children prints as its uid alone, because the node's children are unconstrained and an older or
hand-written entry may hold only the uid.

**It does not report whether the device's Auth user still exists** (decision 11). That column cannot
be answered with the credential this command has: the Auth user is **not in the database**, so
database admin does not reach it — it needs `firebaseauth.users.get`, and
`roles/firebaseauth.admin`'s only Terraform-granted holder is the history job's account (`iam.tf`'s
`history_auth`, §6). The alternative — binding that role to the same principals as `token_minter` —
widens the blast radius past what this command is for, because the role creates and deletes users and
updates their custom attributes, which is a **second way to set a claim** beside the signer's
(`rules.go`'s `userClaim` comment treats claim-setting as the launcher-equivalent power). What the
operator needs is on the node, and **the node's existence is what the rules read**; an orphaned Auth
user whose node is gone is the **sweep's** business (decisions 1 and 2) and is invisible to the rules
by construction.

### 4.3 `fugaro pair revoke <uid|name>`

```
fugaro pair revoke (--uid UID | --name NAME) [--json] [--project NAME | --gcp-project ID]
```

**The order is the command** (M4): delete the device node (**immediate** revocation, D166/F5), then
delete the laptop's keychain item for that pairing's MAC key, then delete `pairing/$uid` if it
survives. It **does not** delete the Auth user (decision 1) and says so in its output, naming the
sweep as what removes the orphan — because no Terraform-granted role the operator holds can delete an
Auth user, and D166 already chose "a sweep change, not a new human grant" for exactly this problem.
A name that matches more than one node is refused, listing the uids, rather than revoked
ambiguously.

### 4.4 The pairing session, and what each carrier holds (D199 and D233 together)

**#787's amendment, quoted:** *"pairing is discovery, not identity. Scanning the QR code only tells
the client … which project and orchestrator endpoint to connect to. It grants no access by itself."*
(`docs/central-orchestrator/spec.md`, kept as the mobile spec's edit 1.)

**The tension this section resolves, stated rather than smoothed over.** The mobile functional
spec's §4.1 says the QR carries *"the project's Firebase configuration and a short-lived, one-time
pairing token"*, and #787's edit 1 **keeps that sentence** while adding that the token grants no
access. The owner's ruling of 2026-10-22 (**D199**) puts the installation's Firebase configuration
in the **encrypted pairing payload**. Both can hold, and here is how:

| Carrier | Holds | Does not hold | Why |
| --- | --- | --- | --- |
| **The QR** (on screen, photographable) | the project id, the database URL and the Identity Toolkit API key — **the bootstrap configuration the phone needs to exchange a custom token at all** — and the one-shot `p~` pairing token | the MAC key (D171), the device token, **the identity** (#787), the code (M3) | the phone cannot sign in before it knows where to sign in; a photograph of the QR yields a token that reaches two nodes and nothing else |
| **`pairing/$uid/request`** (written by the phone) | `key` (the phone's X25519 public key, one pinned spelling) and `at` | everything else — the template's `$other` refuses it | §2 (a); the code is derived by both ends and never written |
| **`pairing/$uid/delivery`** (written by the CLI, read once by the phone) | **the authoritative configuration the app stores** (D199), the device token (`m~`, `mu = uid`), and the per-pairing MAC key — all inside the sealed envelope | anything in clear | §2 (a) 4: no `.validate`, so no generation (M14); D199 |
| **`config/admin/devices/$uid`** (written by the CLI) | the name, the administered email (D226), the role (D225), the pairing time | any token, any key, any fingerprint (M13) | §2 (b) |

**The resolution in one sentence:** the QR's copy of the configuration is a **bootstrap** — what the
phone needs in order to be reachable during the pairing — and the copy in the delivery is the
**record** the app stores and uses afterwards, so D199 is satisfied without contradicting the
sentence #787 kept, and the app must not treat the scanned copy as the account's configuration.
Which of the two the app writes to secure storage is P6.8's, and this plan states the contract it
consumes (T3).

**What the app must refuse, stated here because the payload is the only place it can be stated.**
A delivery whose envelope does not open under the phone's private key, whose version byte is not the
one this build writes, whose AAD does not name this pairing uid **and** this public key, or whose
contents disagree with what the QR said the project is, is **refused whole** — no partial store, no
"the token looked fine". That is the phone's half of §8's envelope; P6.8 tests it, and T3 pins the
bytes.

### 4.5 The JSON document

One object per act, always with `project`:

```jsonc
{
  "project": "aurora",
  "at": "2026-10-22T13:02:21Z",
  "pairing": { "uid": "p~…", "state": "completed", "expires_at": "…" },
  "request": { "key_fingerprint": "K7QM-3X2B", "at": "…" },
  "device": { "uid": "m~…", "name": "…", "email": "…", "role": "admin" },
  "keychain_item": "aurora-BBBBBBBBBBBBBBBBBBBBBB",
  "wrote": ["config/admin/devices/m~…", "pairing/p~…/delivery"],
  "refused": [],
  "warnings": []
}
```

**The rule for every field, and it is P4.1's rule extended:** a uid may appear (it is a key); a
**token never does**, a **key never does**, the keychain item's **value** never does, and the
**typed code never does** — the fingerprint in `request` is the one the CLI computed, which is the
same value the operator saw on the phone, and it is not a secret (it is a hash of a public key).
`refused[]` carries a code and never a paraphrase. `warnings` is present and empty, like `ls`'s.

*(Amended 2026-10-29, owner: `--json` streams newline-delimited events (`qr`, `waiting`, `code`,
`done`, `refused`), `v: 1`; `done` carries this document; the typed code is read as one stdin line;
the console's pairing screen is a later console release. **Amended again 2026-10-29:** the `qr`
event carries its payload **in cleartext**, so the one-shot pairing token and the Firebase web API
key do appear on stdout in it. That is the exception this section's "a token never does" rule needs
said out loud rather than assumed: the payload is the code's own content, the operator's screen
shows it anyway, the token is one-shot and dead in five minutes, and the API key is the restricted
web key the app's config already carries — while the MAC key, the device token, the keychain item
and the typed code stay out of every event, `qr` included. **Amended 2026-10-30 (T3b):**
`keychain_item` is a field of the document, and it carries the item's **name** and never its value.
The two rules above can both hold — "the item's value never does" is a rule about the value only if
the name may appear — and the field is what the operator records in the local config and what a
console screen shows: nothing else tells them what `mailbox.keychain_item` should say (§6), since
nothing writes that file. The item's name is derived (decision 9) and is not a secret (M5), so the
`done` document is where it belongs.)*

### 4.6 Exit codes

| Code | When |
| --- | --- |
| 0 | the act was performed — a completed pairing, a printed `ls`, a completed `revoke` |
| 1 | a **refusal the operator can fix**: no project selectable; no signer or no database reachable as this operator; **the minter role missing** (named); **database admin missing** (named); an unverified or absent email (D226); an invalid `--role`; a `--timeout` above `4m30s`; a fingerprint mismatch; a request node already written by another key; a `revoke` naming nothing, or naming two nodes; a keychain that cannot be written |
| 2 | the database or the Identity Toolkit could not be **reached** — an outage or a transport error, `dbErr`'s `remote` branch |

**Both role refusals are exit 1 and both name the missing one**, before any side effect (T1, T4):
`iam.tf`'s `token_minter` on the signer to mint either token, and
`roles/firebasedatabase.admin` to write or delete the device node, the delivery node or the pairing
node. They are **two separate tests** because they are two separate role sets, and the mobile plan
says so. The nuance `rules.go`'s `userClaim` comment adds is kept rather than hidden: a launcher-level
principal may hold database admin beside the signer, so "pairing is an admin act" is the fence this
command enforces, not a boundary the IAM layout guarantees.

---

## 5. The one `root.AddCommand` line, and everything it drags with it

P6.6 adds **`newPairCmd()`** to `NewRootCmd`'s one `root.AddCommand` list — **one line, no other
edit**. P4.1's line (`newMailboxCmd()`) is its own, and the two are independent. What that line drags
with it, verified against the tree:

1. **`go run ./internal/clidoc` must be re-run.** `internal/clidoc/cliref_test.go`'s
   `TestCliReferenceIsCurrent` compares the committed `website/content/cli-reference.md` with
   `Generate(cli.NewRootCmd())`, and the failure message carries the regenerating command.
   `internal/cli/docs_website_cliref_test.go` holds the other direction. `fugaro pair` is **visible**,
   so it appears; `pair ls` and `pair revoke` appear as its subcommands. *(Amended 2026-10-29: the
   generator is now `go run ./ee/clidoc`, rendering the tagged tree; `TestCliReferenceIsCurrent`
   is in `ee/clidoc/cliref_test.go`, and `internal/clidoc/coreref_test.go`'s
   `TestCoreCommandsAreInTheReference` is the core's subset half. A core `fugaro pair` is held by
   both.)*
2. **`docs/design/v1.md` §9.1 gains a `fugaro pair` row**, and its `--json` paragraph covers it.
   **No test enforces this** — the same gap the P4.1 plan states for its own row — so it is a duty of
   the commit and a line in T8's review checklist.
3. **Nothing hidden.** P4.1's `fugaro mailbox sweep` is the hidden one, and it is P4.1's; P6.6 hides
   nothing.
4. **No `docs_bucket_iam_test.go` pressure.** That test's globs are `../../docs/*.md` and
   `../../docs/design/*.md`, which stop at `docs/` and never reach `docs/plans/*.md` — so this
   document adds no IAM sentence anywhere near the bucket as a **duty**, and nothing would go red if
   it did. §6 states the two role sets in prose because the reader needs them, not because a test
   asks.
5. **`website/content/cli-reference.md` is generated — never hand-edited.**

---

## 6. Where state lives

| State | Where | Who may write it | Written how |
| --- | --- | --- | --- |
| The pairing token | nowhere (minted, shown in the QR, discarded) | — | `token.MintPairing` with `uid = pu = p~<random>`, `exp = iat + min(5m, timeout + 30s)` *(amended 2026-10-29: §1's row says why it is not `Mint`; T1a landed it)* |
| The phone's public key | `pairing/$uid/request` = `{key, at}` | **the phone**, under the `p~` token | once only; the template's write-once rule and `$other` fence it (§2 (a)) |
| The pairing payload | `pairing/$uid/delivery` | **the CLI**, with **database admin** | sealed to the phone's key; read once by the phone (§8) |
| The device node | `config/admin/devices/$uid` = name, administered email, role, at | **the CLI**, with **database admin** | written **before** the token is delivered (M4); no secret (M13) |
| The device token | nowhere on the laptop (minted, sealed, delivered) | — | `uid = mu = m~<random>`; `token.Lifetime` |
| The phone's copy of the device token | the phone's **secure store** (P3.1's rule) | the phone | out of this plan's remit; the contract is §4.4 |
| The **pairing key** (the MAC key) | the **OS keychain**, one item per pairing; the item's **name** in the project's local config (`internal/localcfg`, P4.1's field) | P6.6 writes it; the consumer reads it | `crypto/rand`, 32 bytes, `EncodeKey`/`ParseKey`'s one spelling; never printed (M5) |
| The pairing's own record on the laptop | the local config's mailbox block: the item name, and nothing secret | the operator | plain config, **not secret**, printable (M5) |
| The dedupe ledger, the used action and confirmation ids, the window state, the expiry records | `$XDG_STATE_HOME/fugaro/mailbox/<project>/ledger.json` | the consumer | P4.1's tmp+rename, `0o600`; P6.7 extends the key spaces (M7) |
| Prompts waiting for an answer | `$XDG_STATE_HOME/fugaro/mailbox/<project>/pending.json` | the consumer | P4.1's; the entry's shape is P6.5's |
| A stored prompt past the TTL | `mailbox/$mb/**` | **the owner-run sweeper** — P6.13's delete set (M8) | deleted, never answered |
| An orphaned `p~` Auth user and its `pairing/$uid` node | the Identity Toolkit and `pairing/$uid` | **the history sweep**, which holds `roles/firebaseauth.admin` (`iam.tf`'s `history_auth`) | decision 1; the CLI's own deletion needs a role no Terraform grant gives the operator |
| An orphaned `m~` Auth user whose device node is gone | the Identity Toolkit | **the history sweep** | D166's own answer; P6.6's `m~` rule (T4) |
| The installation's Firebase configuration | the QR (bootstrap) and the delivery (the stored record) | — | D199; §4.4 |

**The two role sets, named, because §4.6 refuses on them:** minting either token needs
`iam.tf`'s `token_minter` custom role (`iam.serviceAccounts.signJwt`) on the signer account, bound to
`var.launchers` and `var.operators`; writing or deleting the device node, the delivery node or the
pairing node needs `roles/firebasedatabase.admin`, which `iam.tf` grants to `admins` and
`budget_admins`. Deleting an **Auth user** needs `roles/firebaseauth.admin`, whose only
Terraform-granted holder is the history job's account — which is why decisions 1 and 2 hand the
orphans to the sweep.

**Nothing here is stored in the runs bucket, and the sweeper's `keep` set holds no `m~` or `p~` key
today.** `internal/budget/sweep.go`'s `deleteUsers` filters on `uidPrefix = "r~"` (§2 (g)), and
extending it is **P6.6's** — the one file the mobile plan gives it for this. If the sweeper deletes an
`m~` or `p~` user before T4 lands, that is a **finding, not a silent fix**.

---

## 7. The pairing loop

### 7.1 The CLI's steps, in order

1. **Resolve the project and the operator's credentials** (`openBudget`'s path). If the minter role
   or database admin is missing, refuse here, naming it, before anything exists.
2. **Resolve the email** (D226, D224): the `--email` value, or the operator's own verified address by
   the check `internal/cli/init_members.go`'s `userEmail` already performs for the CLI. An address
   that is not verified is a refusal. The domain rule (D223) is applied here, at the write, because
   the rules cannot express it (§2 (b)).
3. **Mint the pairing token**: a fresh `p~<random>` uid, claims `pu = uid` (and nothing else),
   `exp = iat + min(5m, timeout + 30s)` — D165's five-minute ceiling, **sized to the wait** so the
   phone's exchange still lands before the token dies (the 30 s is the exchange's slack, and it is
   why `--timeout`'s own ceiling is `4m30s`; §4.1). **Draw the seal key and the ephemeral key pair
   only after this**, so a refusal above has no side effect at all.
4. **Show the QR** and wait, with `--timeout`, for **both**: the phone's key in
   `pairing/$uid/request` (read with database admin, `.read: false`) and the operator's typed
   code.
5. **On the typed code**: decode the key from its one pinned spelling (M3, §2 (e)), derive the
   fingerprint from the **decoded bytes**, compare, and **refuse on any mismatch with no side
   effect**. A request node already written by a key whose fingerprint does not match is the
   photographed-QR case and is refused as such, not retried.
6. **Mint the device token**: a fresh `m~<random>` uid, claims `mu = uid`. Generate the **MAC key**
   (32 bytes, `crypto/rand`) and write it to the OS keychain under the project's item name.
7. **Write `config/admin/devices/$uid`** — the name, the administered email, the role, `at` — with
   database admin. **Before the delivery** (M4).
8. **Seal and write `pairing/$uid/delivery`** (§8) with database admin.
9. **Delete `pairing/$uid`** — the whole subtree — so the payload does not sit readable for the
   hour the `p~` ID token lives. **The Auth user is not deleted here and cannot be** (decision 1):
   the CLI's credential does not carry `roles/firebaseauth.admin`, and the sweep is what removes the
   orphan (decision 2). The command says so in its output.
10. **Print what it wrote**, the device uid, the name and the fingerprint, and **never** the token, the
    key or the item's value.

### 7.2 The phone's half, stated as a contract (P6.8 consumes it)

Scan → parse and **validate before anything is stored** → generate an X25519 key pair → show the code
derived from the public key → write `{key, at}` to `pairing/$uid/request` once → sign in with
the pairing token → read `pairing/$uid/delivery` → **refuse the whole payload unless the
envelope opens, the version byte is this build's, and the AAD names this uid and this key** (§4.4) →
store the device token and the MAC key **in secure storage only** (P3.1's rule) → store the
configuration from the payload, not the scan (D199). The code is derived per pairing and never
stored.

**"Read" means a listener attached at sign-in, and the reason is §7.1 step 9.**
*(Amended 2026-10-30, T3b.)* The CLI's wait ends when the **operator** types the code; the delivery
is written in that instant and the whole `pairing/$uid` subtree is deleted in the next one, because
the payload must not sit readable for the hour the `p~` ID token lives (§7.3.3, decision 2). The
phone is not in that instant and cannot be: it signs in as soon as it has written its request — it
holds the pairing token from the QR and needs nothing from the CLI to exchange it — and then has no
signal for when the code will be typed. A one-shot `get` issued *after* sign-in therefore races a
window microseconds wide and loses it. What the contract requires is a **listener on
`pairing/$uid` (a `value` observer) attached as soon as the token is exchanged**, holding the
delivery out of the first snapshot that carries one: Firebase dispatches the write and the later
delete as two events, so a client that is watching receives the payload, and a client that asks
afterwards is told the session is over. A read that arrives late is **not** a reason to re-pair
without an operator: the phone says the pairing expired and shows the code path again.

**What this costs, stated rather than smoothed over:** the delivery's lifetime is the delete's
latency, so a phone that is offline, backgrounded or whose listener fails to attach at that moment
gets nothing and must be paired again. The alternative — the CLI waiting for a receipt before it
deletes — needs a node the phone may **write**, and the merged rules give the pairing uid exactly
two children: `request`, write-once by the pairing token, and `delivery`, which is `.write: false`
(§2 (a) 4, M14). Adding an ack node is a rules change and a second wire contract, so it is a
decision for the owner and a task of its own, not something this plan can assume: until then the
listener above is the contract P6.8 implements, and the sweep's own rule (decision 1) is what
clears a session nobody completed.

### 7.3 The three orderings that are load-bearing

1. **Node before token** (M4). A delivered token with no device node is a phone that fails `USER`
   on every request with nothing on screen to explain it.
2. **Match before any side effect** (M3). No device node, no token, no key, no delivery before the
   typed code matches — so a refused pairing leaves the project exactly as it was.
3. **Delivery before the pairing node's deletion** (step 8 before step 9), and **the deletion before
   the CLI exits**. Deleting the subtree first would make a delivered token unusable; leaving it
   behind keeps the payload readable for the life of the `p~` ID token.
   *(Amended 2026-10-30, T3b: this ordering is what makes §7.2's "read" a listener rather than a
   fetch. The deletion follows the write immediately, so the only reliable way for the phone to see
   the payload is to be watching for it — and that is the phone's half of the ordering, not the
   CLI's, which is why §7.2 states it as part of its contract rather than here.)*

---

## 8. The two envelopes, and why the pairing one is not `seal.go`'s

`internal/budget/token/seal.go` already writes a versioned AES-256-GCM envelope — version byte,
12-byte nonce, ciphertext, 16-byte tag, AAD = the object's name, a zero byte, and the version byte —
and its comments carry the reasoning: the version byte first so a later format is refused rather than
misread; the name bound so a sealed blob cannot be opened as another object's; and the honest claim
that *"this is a capability, not a boundary."*

**The pairing delivery needs the same shape with a different key, and that is the whole of the
difference.** `seal.go`'s key is **shared**: the launcher generates it and carries it in the marker.
The pairing delivery's key is **agreed**: the only thing on the wire is the phone's public key, so
the sender draws an **ephemeral** X25519 key pair, performs ECDH with the phone's key, and derives
the AEAD key with HKDF-SHA-256. `crypto/ecdh` and `crypto/hkdf` are standard library and `go.mod`
requires no new module (T3 asserts it). So:

```go
// device.go's envelope, stated:
//   version byte (1) || ephemeral public key (32) || nonce (12) || ciphertext || tag (16)
//   key  = HKDF-SHA-256(ECDH(ephemeral_private, phone_public), salt=nil, info="fugaro-pairing-v1")
//   aad  = "pairing/" + uid + 0x00 + phone_public_key_bytes + 0x00 + version byte
```

**Why the AAD carries the uid *and* the key.** The version byte is bound as well as checked, for
`seal.go`'s reason. The uid is bound so a payload cannot be moved to another pairing's node. The
**key** is bound so a payload sealed for one phone cannot be opened by another phone that shares the
node — which is not reachable today, since one node has one reader, but is exactly the kind of thing
that stops being true when a later change adds a second device to a pairing.

**What makes the AAD canonical is the field layout, not the absence of a byte — and the distinction
matters, because the key is raw.** The uid is the only variable-width part, so the first zero byte
after it is its terminator: a uid cannot contain one (`p~` followed by unpadded base64url, §2 (e)).
**The key can, and routinely does**: these are 32 raw public-key bytes, and roughly one in eight
such values contains a `0x00`. So the rule is **not** "no field contains a zero" — an implementer who
believes that may think a zero inside the key is a separator, or that a separator can be dropped.
What makes it unambiguous is that **everything after the uid's terminator is fixed-width and in a
fixed order**: the key's 32 bytes, then the version byte. A zero inside the key is therefore
positional data and nothing else, and no length prefix is needed. The consequence for a reviewer:
**an implementation must not reorder, truncate, re-encode or length-prefix those two trailing
fields** — each of those changes the AAD, and the mutation in T3 (`TestEnvelopeBindsThePairingUID`)
is what shows it. The second zero byte, before the version, is `seal.go`'s shape kept for
consistency; with a fixed-width key it is redundant, and dropping it would be a *different* AAD
rather than a bug — which is exactly why the layout is pinned here instead of left to taste.

**What the envelope is not.** It is **not** a boundary against the operator, the launcher, or anyone
holding database admin: the ephemeral private key lives in the CLI's memory and the plaintext in its
process, and `rules.go`'s own comment says a launcher-level principal holds database admin beside the
signer. It removes the value of what a peer can reach — a photographed QR, a copied payload — not the
reach. That sentence is `seal.go`'s and it is repeated here rather than implied, because the review
tier on T3 turns on it.

**No second implementation of `canonicalBytes`, no second MAC.** The MAC key's envelope and the
message MAC are different things: the MAC is P4.1's one function over P6.3's `canonicalBytes`
(§2, M7). This envelope carries the key; it does not sign anything.

---

## 9. The MAC and the key: who generates, who signs, who enforces

| The work | Whose | The task text it comes from |
| --- | --- | --- |
| **Generating the per-pairing key**, one per pairing, into the OS keychain, delivered sealed | **P6.6** | P6.6: *"generates the per-pairing MAC key into the OS keychain … and delivers token and key encrypted to the phone's key"*; D171 |
| **The one MAC function** — canonical bytes in Go, HMAC-SHA-256, `sign`/`verify` — held to `shared/mailbox-schema`'s `vectors.json` | **P4.1** (T2/T2a), extended by P6.7 | the merged plan's §8.4, row 2 |
| **The inbound gate** — a message that does not verify is never acted on | **P4.1 wires it; P6.7 owns it as enforcement** | §8.4, row 3 |
| **The per-kind clauses and the action record** — kind vocabulary, `options`/`reply_to` per kind, the verb–target agreement, the confirmation record, the merge head SHA, the fence's allowance | **P6.7** | §8.4, row 4; D169 |
| **The ledger** — P4.1 lands the delivery ledger; P6.7 extends it to action and confirmation ids and to expiry | **P4.1 then P6.7** | §8.4, row 5 |
| **The pending prompts, the TTL, the safe answer exactly once, the queue cap, the revoke** | **P6.7** (the module is P6.5's) | §8.4, row 6 |
| **The heartbeat's `agents` list** | **P6.7**; P4.1 writes the heartbeat without it | §8.4, row 7 |
| **The store-side deletion of a stored prompt** | **P6.13**, "and nothing more" | the merged plan's **decision 8**, which assigns the two store-side rows wholesale — **§8.4's own table carries no store-side row** (its rows are the MAC function, the inbound gate, the per-kind clauses, the ledger, the prompts, the heartbeat's `agents` and the `acked` read/presence/launch), which is why decision 8 exists |
| **The `m~` rule in `internal/budget/sweep.go`** | **P6.6** | the merged plan's decision 8 |

**What this plan adds to that split: nothing.** The table is quoted so that the two documents cannot
drift on which task owns a row, and so that a task here which takes a row the table assigns away is a
finding rather than an edit. Its one row that §8.4 does not carry — the store-side deletion — is
assigned by the merged plan's **decision 8**, not by a §8.4 row, and is cited from there. The one
place the tree makes the merged plan's own words insufficient — the `m~`-only sweep rule leaving `p~`
orphans — is owner decision 1, and it is asked, not assumed.

**The interval the table leaves open, stated:** between P4.1 and P6.6 the consumer verifies inbound
and signs outbound with a key it reads from the keychain **and there is no pairing to have written
one** — so the command is inert (P4.1's §8.3 first row: it stops at startup). P6.6 is what makes the
merged MAC code reachable in production, and that is why the edge `P4.1 → P6.6` exists in the mobile
plan.

---

## 10. D128: the fence P6.7 closes

The merged P4.1 plan's §9 states the fence as a **seam**: the code path exists, a test builds a
**synthetic** confirmation record, and **it cannot be reached from a real message today**, because
nothing in the merged tree writes a confirmation record. **P6.7 is the half that reads the record**,
and this section is the change of state, not a new fence:

| The message asks for | P4.1's answer | P6.7's answer |
| --- | --- | --- |
| `fugaro cancel RUN` | refused | refused unless a MAC-verified action record names the same action id, inside the 6-hour command window from the confirmation time, with the confirmation not already used, and the target resolved |
| `fugaro budget kill` | refused | **still refused, confirmed or not** |
| `gh pr merge` / an approve | refused | allowed **then**, and the consumer re-reads the PR's head and refuses if it moved from the head SHA in the record |
| Anything not in the allow-list | refused | refused; an unrecognised verb is refused by default |
| A launch (`fugaro run`) | allowed | allowed **without** a confirmation record (D162), the caps applying |
| A read-only report (`ls`, `diagnose`, `report`) | allowed | allowed |
| Free text that reads like a destructive command | refused | refused, and **an answer to a destructive prompt is accepted only as a MAC-verified action record with a confirmation** — never as plain text or a plain `answer` |

**The residual, quoted rather than softened:** D169's answer accepts *"the residual risk that an
agent can still choose to [act on voice-originated text itself] (F38), which no argv table can
remove"*. P6.7's fence binds **the consumer**, not the orchestrator's own judgement, and this plan
claims no more.

**Where the confirmation's own timeout is checked:** on the phone (P6.2), because the pending time is
phone-local state. The consumer checks the **6-hour command window** from the confirmation time, the
5-minute future-timestamp tolerance, and that the confirmation has not been used. P6.7's task text
says exactly this split and T5 keeps it.

---

## 11. Expiry and the windows

| Question | Answer | Source |
| --- | --- | --- |
| How long is a prompt valid? | **72 hours** from `at`, computed by the consumer on its own clock; there is no expiry field, and the schema refuses every name for one | D118, O5; D168 as amended 2026-10-17; `shared/prompts`' `PROMPT_TTL_MS` (259 200 000 ms) |
| What is a command's window? | **6 hours** (`shared/mailbox`'s `DEFAULT_WINDOW_MS`), at most 7 days | §3.6, D173 |
| What does expiring a prompt yield? | its **non-destructive answer**, or a **stop** when it has none — delivered **exactly once**, by the consumer | D168 as amended; `shared/prompts`; M9 |
| What happens at the queue cap? | the prompt past the cap is **refused, never evicted** ("queue full") | `shared/prompts` (P6.5) |
| How long is a pairing session? | bounded by the `p~` **ID token's hour** (`token.Lifetime`), not by the five-minute custom token | §2 (h), M2 |
| How old must an orphaned `p~` user be before the sweep deletes it? | **`token.Lifetime` plus one sweep interval** — a session cannot outlive the ID token, and the grace keeps a sweep from racing a pairing in flight | decision 1 |
| Delivered after the TTL | refused `expired`, never delivered at all | `shared/mailbox` |
| A timestamp more than 5 minutes in the future | refused `future-timestamp` | §3.5 |

**Two windows, named apart, and neither is declared twice.** D118's TTL is `shared/prompts`' number
and the Go side binds it (a test pins equality if the Go side declares a duration — M9). D173's
6-hour window is `shared/mailbox`'s. **The pairing session's hour is a third and it is `token.Lifetime`,
which the tree already has** — so decision 1's number is a sum of two existing values, not a new one.

---

## 12. The device node, the administered email, and what needs no rules generation

**The node's fields, and who writes them.** `config/admin/devices/$uid` holds, at v1:

| Field | Who writes it | Read by | Note |
| --- | --- | --- | --- |
| (the node's existence) | `fugaro pair` | **the rules**, in `userClaim` | the whole of the allowlist (D166) |
| `email` | `fugaro pair --email`, or the operator's own verified address (D224, D226) | `pair ls`, and the operator | the **administered** email — a field on the entry, never a key (#787's amendment, mobile-auth D222) |
| `role` | the pairing user's role, lowered by `fugaro pair --role` (D225, decision 13) | `pair ls`, and later work | **no rule reads it in v1** (D225); never a token claim |
| `name`, `at` | `fugaro pair` | `pair ls` | what `ls` needs to be legible |

**The refusals D226 requires, and where they happen.** An address that is not **verified** is
refused at pairing — the check is the one `internal/cli/init_members.go`'s `userEmail` already
performs for the CLI (D226), reused rather than reinvented. A domain rule (D223) is applied **at the
write**, in the CLI, because the rules cannot express it (§2 (b), mobile-auth §F4): the rules
language has no string rewrite and an RTDB key may not contain `.`, so an exact address cannot be
looked up and the domain half is a predicate over `auth.token.email` whose dynamic form is
**unverified**. The CLI is therefore the only place the rule exists, and T2 tests it there.

**What needs no rules generation, stated as a claim a task can check (M14):**

1. **The node's new fields** — no `.validate`, no `$other`, so any children are accepted (mobile-auth
   §F29).
2. **The delivery payload** — no `.validate`, no `$other` (§2 (a) 4), so D199's configuration, the
   device token and the MAC key fit.
3. **Every write P6.7 makes** — the mailbox's inbox, outbox and heartbeat fields, which P6.4 already
   validates.
4. **Every deletion P6.13 makes** — deletions need no `.validate`.

So `rules.json.tmpl`, `testdata/rules.golden.json`, `RulesVersion` and the owner-run deploy are all
**untouched** by these three tasks, and a task that finds itself editing the template is a finding.

---

## 13. The export-compliance and store-form revisit

**Why this is in a CLI plan.** The owner's export-compliance declaration is about the app, and
pairing is the change that alters the facts under it: the phone generates an **X25519 key pair** and
**opens an AEAD envelope** — key agreement and authenticated decryption, which the app does not do
today. The declaration is in the tree:

- `mobile/app.config.js` merges `{"ios": {"infoPlist": {"ITSAppUsesNonExemptEncryption": false}}}`
  into every state, and its comment records the provenance: *"`eas build` printed that it cannot
  write `ITSAppUsesNonExemptEncryption` into a **dynamic** config … The owner ruled on 2026-10-20:
  'write it, it's OK' -- the app uses only standard/exempt encryption"*, with *"The owner answers
  Apple's other export questions in App Store Connect"*.
- `mobile/src/app-config.test.js` and `mobile/src/app-config.ids.test.js` pin the merge and the
  key's presence.
- `mobile/README.md`'s TestFlight step 3 says the answer is *"declared, not asked again"*.
- `docs/design/desktop-console.md` §7.4 states the telemetry posture the store forms rest on: *"No
  telemetry, no crash reporting to a third party, no analytics, and no update check that carries an
  identifier."*

**What has to be revisited, and by whom.** The declaration's *answer* is unlikely to change —
X25519 and AES-GCM are the standard primitives such declarations are normally made about — but the
**basis** changes, and two store artifacts describe the app's data handling rather than its crypto:

1. **The export declaration itself.** The declaration was made for an app whose only cryptography was
   the platform's own TLS. Pairing adds key agreement and AEAD **the app implements** (through the
   platform's crypto library). The owner should re-affirm the answer against the new facts, and P6.8
   should say in the app's own notes which primitives the app now uses — `mobile/README.md`'s
   declaration section is where that belongs, and it is P6.8's file.
2. **Play's Data safety form and Apple's App privacy answers.** Pairing **transmits** the phone's
   public key, the project's configuration and a device token, and **stores** the device token and the
   MAC key in the phone's secure store. Whether any of that is "collected" in either store's sense is
   the owner's answer, not an agent's; what this plan contributes is the list of facts the answer has
   to be given against. §4.4 and §6 are that list.
3. **The mailbox's own data, which the forms already have to describe.** The mailbox holds redacted
   text with a 72-hour TTL (D118), and the app reads the database directly (D112's answer). Pairing
   adds no new category of stored content, but it does add the **configuration** the app keeps.

**What could not be verified here, stated plainly.** The task named a memory file,
`mobile-telemetry-and-store-forms.md`, as the source for this revisit. **That file does not exist in
this container** — the memory directory
(`/home/fugaro/.claude/projects/-work-repo/memory/`) is **empty**, checked directly — so this section
is grounded in the tree's own files, each cited above, rather than in that memory. If the memory
holds a ruling that goes beyond them, this section is incomplete and the owner's own answer governs.
**Nothing in this plan is submitted to either store**, no form is filled, and no `eas` or store
command is run: the revisit is a list of what the owner must re-answer, and it is also in §18's
unverified list.

---

## 14. How it is tested

Everything runs as `go test`, on the fakes this repository already has
(`internal/gcpfake`'s Realtime Database and identitytoolkit fakes), with **no Node, no network, no
real keychain, no clock and no real key** — the pass takes `now` as a value, exactly as P4.1's plan
requires, and the keychain and the signer are injected. `shared/`'s tests are **not** run by these
tasks (no task here edits `shared/`), and CI does not run them in any case — which is why every
contract is replayed from a **committed vectors file** (P4.1's M9). The commands named below are the
ones a reviewer runs; each task repeats its own.

| Concern | How it is exercised |
| --- | --- |
| The pairing token's shape and lifetime | a fake signer; `uid`/`pu` begin with `p~`; `exp − iat ≤ 5m` **and** `exp ≥ iat + --timeout`; never `r~` |
| The device token's claim | a fake signer; `uid == mu == m~…`; never `r~` |
| The typed code | a fake key pair; a wrong code writes nothing (no node, no token, no key, no delivery) |
| The fingerprint's derivation | both spellings of one key yield one code (the spelling is pinned, M3) |
| The delivery envelope | a fake key pair; the payload does not open without the phone's private key; a version byte, a uid or a key that differs is refused; the AAD's three bindings each fail a mutation |
| The two role refusals | two fakes, each refusing one role, each asserting the message names it and no side effect happened |
| The device node | written before the delivery; holds no token, no key and no fingerprint (M13) |
| `revoke` | the node is deleted before the keychain item; the Auth user is not deleted |
| The sweep's `m~` and `p~` rules | a fake Identity Toolkit and a fake database; an `m~` user whose node exists survives, one whose node is gone is deleted; a `p~` user younger than `token.Lifetime` plus one sweep interval survives |
| The consumer's gate | P4.1's simulator, plus `shared/confirm/vectors.json` replayed (T5) |
| The ledger and the expiry | a restart between two passes; a sweep between two passes; exactly-once, both times |
| The store-side deletion | a fake clock; older than the TTL is deleted, younger survives; the sweeper writes no answer |

**The mutation proof, and it is one.** The owner's ruling allows **one** mutation-proven headline
test. It is **T5's**: remove the MAC verification from the consumer's inbound path and watch the
"a forged action record is refused and nothing runs" test fail. Every other Red step is a written
step, not a mutation proof.

---

## 15. Tasks

Eight ordered parts. Each names the mobile task it is, its files, its dependencies, a verification an
agent can run, a Red step, and its reviewer tier. **A task that needs a file outside its own row is
a finding.**

### Task T1: The pairing token, the one-shot session and the QR — SECURITY-SENSITIVE, independent opus review

**Mobile task.** P6.6, part 1. **Files.** `internal/cli/pair.go` (new) + `internal/cli/pair_test.go`
(new); `internal/cli/root.go` (**one line**); `website/content/cli-reference.md` (regenerated);
`docs/design/v1.md` §9.1 (one row and the `--json` list). **These four move together in one commit**
(§5). **Depends on.** P6.4 (merged); **D165**; §2 (a), §2 (e); M3; §5.
*(Amended 2026-10-29: T1 is **split**, because three of the four files cannot move with a command
that is not registered yet. **T1a** landed the command's surface, the one-shot pairing token, the QR
payload and the `--json` event stream as six new files — `internal/cli/pair.go` +
`internal/cli/pair_test.go`, `internal/cli/pair_events.go`, `internal/cli/testdata/pair-events.golden.jsonl`,
`internal/budget/token/pairing.go` + `pairing_test.go`, all now in §16's table — and deliberately
adds **no** `root.go` line, regenerates no reference and touches `docs/design/v1.md` nowhere, so the
command is in the tree and on nobody's `fugaro --help` yet. **T1b** is the other three: the
`root.go` line, the regenerated reference and the §9.1 row, which T4 lands with `pair ls` and
`pair revoke` — one registration for one command, and the reference cannot name a command the root
does not have.)*
**Verification.** `go run ./internal/clidoc` then
`GOFLAGS=-p=2 go test ./internal/clidoc ./internal/cli -run 'CliRef|Docs|Lint|Pair' -count=1`, plus
`gofmt -l internal/cli`: the QR holds the bootstrap configuration and the token and **no key**; the
token's `uid` and `pu` begin with `p~` and never `r~`; `exp − iat ≤ 5m`; **`exp ≥ iat + --timeout`**,
so the token always outlives the wait; `--timeout` above `4m30s` is refused; the command is
**visible** in the reference and the hidden sweeper is
not. *(Amended 2026-10-29: the reference commands are now `go run ./ee/clidoc` and
`go test ./ee/clidoc/ ./internal/clidoc/... -count=1` beside the `./internal/cli` run, §5 item 1.)*
**Red step.** Add the command without regenerating the reference and watch
`TestCliReferenceIsCurrent` fail with the regenerating command in the message.
**`--role` (amended 2026-10-29, decision 13).** The flag's default is the pairing user's own role,
read from the CLI's launcher and operator lists (`internal/cli/roles.go`) and mapped to the device
words (launcher to `operator`); `--role` only **lowers** it. Tests: `--role` above the pairing
user's role **is refused** (exit 1, before any side effect); the default **inherits** the pairing
user's role; a `viewer` device is reached **by lowering** an operator's role; a caller the lists
cannot place has no role to inherit and is refused. Pairing counts **no seat** (decision 13).
**Reviewer tier.** SECURITY-SENSITIVE — independent opus review. What a reviewer must confirm: that
**nothing in the QR is a key or an identity**, and that the token's claims are `pu` alone.

### Task T2: The phone's key, the typed fingerprint and the device token — SECURITY-SENSITIVE, independent opus review

**Mobile task.** P6.6, part 2. **Files.** `internal/budget/token/device.go` (new) +
`internal/budget/token/device_test.go` (new); `internal/cli/pair.go`'s wait and compare.
**Depends on.** T1; **D165, D166, D226** (the email check), D224, D225; §2 (c), §2 (e); M3, M4, M13.
**Verification.** `GOFLAGS=-p=2 go test ./internal/budget/token ./internal/cli -run 'Pair|Device|Fingerprint' -count=1`
with a fake key pair and a fake signer: a wrong typed code writes **nothing** — no device node, no
token, no keychain write, no delivery; **no device node is written before the typed code matches**;
the device token's `uid == mu` and begins `m~`, never `r~`; the code is derived from the **decoded
key bytes**, so the two spellings of one key give one code; an unverified email is refused; an
invalid `--role`, or one above the pairing user's role, is refused (decision 13); the node holds no token, no key and no fingerprint (M13); the minter
role and database admin are each refused with a message naming the missing one and no side effect
(**two tests**).
**Red step.** Write the device node before the comparison and watch
`TestNoNodeBeforeTheCodeMatches` fail.
**Reviewer tier.** SECURITY-SENSITIVE — independent opus review. What a reviewer must confirm: the
fingerprint's input, and that the failure path has **no** side effect.

### Task T3: The delivery — the key agreement, the token, the MAC key and D199's configuration — SECURITY-SENSITIVE, independent opus review

**Mobile task.** P6.6, part 3. **Files.** `internal/budget/token/device.go`'s envelope (same file as
T2, one writer per task — T2 lands the file, T3 extends it in a dependent task, exactly as P4.1's
T2/T2a do for `mac.go`); `internal/cli/pair.go`'s step 8 and step 9; **`internal/localcfg/localcfg.go`** (and its test file) *(added
2026-10-29, decision 9)*: the `mailbox.keychain_item` block widens from one item name to **one item
per pairing uid**, validated by `keychain.ParseItem` as today, with the shared-config publish and
merge rules kept.
**Depends on.** T2; **D171, D199**; M5, M6, M14; §4.4, §8.
**Verification.** `GOFLAGS=-p=2 go test ./internal/budget/token ./internal/cli -run 'Delivery|Envelope|Pair' -count=1`:
the payload does not open without the phone's private key; a wrong version byte, a different uid and
a different public key are each refused (three mutations, one per AAD part); the MAC key appears in
no output, no log line, no `--json` document and no fixture; the payload carries D199's
configuration; `go.mod` gains **no module** (`crypto/ecdh` and `crypto/hkdf` only); the pairing node
is deleted after the delivery is written; the widened block parses, round-trips and merges with
**two** pairings holding two items, refuses an item name of the wrong shape, and a project that
holds the old single name is migrated by re-running init (no compatibility shim, pre-1.0); the
consumer reads each pairing's own item.
**Red step.** Drop the uid from the AAD and watch `TestEnvelopeBindsThePairingUID` fail.
**Reviewer tier.** SECURITY-SENSITIVE — independent opus review. What a reviewer must confirm: that
the envelope is claimed as **a capability, not a boundary** (§8), and that the key is never printed.

### Task T4: `pair ls`, `pair revoke`, and the sweep's two prefixes — SECURITY-SENSITIVE, independent opus review

**Mobile task.** P6.6, part 4. **Files.** `internal/cli/pair.go`'s `ls` and `revoke`;
`internal/budget/sweep.go` (the prefix rule and the report); `internal/budget/sweep_device_test.go`
(new). **Depends on.** T1–T3; **D166**; M4, M13; §6; decisions 1, 2 and 6.
**Verification.** `GOFLAGS=-p=2 go test ./internal/budget ./internal/cli -run 'Pair|Sweep|Revoke' -count=1`
with the fakes: `revoke` deletes the **node before** the keychain item; the Auth user is **not**
deleted; the sweeper never deletes an `m~` user whose device node exists and does delete one whose
node is gone; a `p~` user younger than `token.Lifetime` plus one sweep interval survives and an older
one is deleted; a
`pairing/$uid` node whose user is deleted is deleted; `ls` prints no token, no key and no item value,
and **no Auth-user column** (decision 11);
a `revoke` naming nothing, or two nodes, is refused.
**Red step.** Delete the keychain item before the node and watch
`TestRevokeDeletesTheNodeFirst` fail.
**Reviewer tier.** SECURITY-SENSITIVE — independent opus review. What a reviewer must confirm: the
ordering (M4) and that the sweep's delete set is exactly the two prefixes and their nodes.

### Task T5: The consumer's inbound gate and the action record — SECURITY-SENSITIVE, independent opus review

**Mobile task.** P6.7, part 1. **Files.** `internal/cli/mailbox.go`'s inbound path and runner (P4.1's
file); `internal/mailbox/action.go` (new) + `action_test.go` (new); `internal/cli/mailbox_test.go`.
**Depends on.** P4.1 (T1–T6), P6.2 (merged), P6.3 (merged), P6.4 (merged); **D169**, D162, D168,
D128 and its amendment; M7, M10; §9, §10.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/mailbox -run 'Mailbox|Action|Refus|Confirm' -count=1`
over the simulator, including `TestConfirmParityWithShared` over every vector in
`shared/confirm/vectors.json`: an action with no confirmation record is refused and nothing runs; a
forged record — unsigned, or signed with the wrong key — is refused and nothing runs; an unsigned
`message`, an unsigned `answer` and a plain-text answer to a destructive prompt are each refused; a
validly signed record with a tampered target or head SHA is refused; a signed record re-sent under a
new message id is refused; an `at` more than 5 minutes ahead is refused; a merge whose PR head moved
is refused; a replayed confirmation id and a confirmation for a different target are each refused; a
launch with no confirmation record is **not** refused for lacking one (D162); the kill switch and
budget writes are refused even when confirmed.
**Red step.** **The headline mutation proof (the one the owner's ruling allows):** remove the MAC
verification from the inbound path and watch `TestForgedActionRecordIsRefused` fail.
**Reviewer tier.** SECURITY-SENSITIVE — independent opus review. This is the task that makes D128's
amendment a behaviour.

### Task T6: The ledger, the pending prompts, the expiry and the revoke — SECURITY-SENSITIVE, independent opus review

**Mobile task.** P6.7, part 2. **Files.** `internal/cli/mailbox.go`'s state and expiry;
`internal/mailbox/expiry.go` (new) + `expiry_test.go` (new) — P4.1's T7 already names this file for
its own half; **T6 extends it**, one writer per task.
**Depends on.** T5; P6.5 (merged), P4.1's T4 and T7; **D161, D168 as amended, D118, D173**; M9.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/mailbox -run 'Mailbox|Expiry|Ledger|Prompt' -count=1`
with a fake clock and a restart between two passes: a restart after a prompt expired does not deliver
its safe answer a second time; a stored prompt deleted by the sweeper is still expired from the local
copy with its safe answer; a message signed with a revoked pairing's key is refused; after
`pair revoke` the pairing's pending prompts are dropped with their safe answer **once**; a prompt
answer older than 6 hours but within the TTL is accepted while a command older than 6 hours is
refused; an expired prompt gives the orchestrator its non-destructive answer, never a destructive
one; the heartbeat carries the `agents` list and its `mac`; the queue cap refuses the next prompt at
the boundary.
**Red step.** Answer a prompt from the sweeper's deletion instead of the consumer's clock and watch
`TestConsumerDownStillAnswersOnce` fail.
**Reviewer tier.** SECURITY-SENSITIVE — independent opus review. What a reviewer must confirm: that
the expiry's "exactly once" is the ledger's, and that the safe answer is never destructive.

### Task T7: The sweeper's stored-prompt deletion — SECURITY-SENSITIVE, **opus + independent review**

**Mobile task.** P6.13, whole. **Files.** `internal/cli/mailbox.go`'s hidden `sweep` (P4.1's file);
`internal/cli/mailbox_sweep_test.go`. **Depends on.** T6; **D118, D168 as amended**; M8; §11.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'MailboxSweep' -count=1` with a fake
clock: a stored prompt older than the TTL is deleted by the sweep; a prompt younger than the TTL
survives; **a sweep run while the consumer is down deletes the stored prompt, and the consumer on
restart still delivers the non-destructive answer (or a stop) exactly once** from its own clock and
ledger; **the sweeper itself never writes an answer**; the delete set holds no path outside
`mailbox/$mb/**` prompts past the TTL and the consumer's own state; a full queue plus a sweep never
exceeds the cap.
**Red step.** The consumer-down test, against a consumer that only answers when it sees the sweeper
delete.
**Reviewer tier.** **SECURITY-SENSITIVE — opus + independent review**, because it decides the default
answer the orchestrator receives.

### Task T8: The parity corpus, the review checklist and the surface's drag

**Mobile task.** all three, as the review's own work. **Files.**
`internal/mailbox/testdata/parity.json` (generated by `shared/mailbox/parity.test.js`, replayed by
Go — P4.1's T8 writes it; **T8 extends the corpus's use, not its generation**);
`docs/mobile/pairing-review-checklist.md` (new, short: the lines a reviewer must confirm, each marked
proved or unprovable here); the `docs/design/v1.md` §9.1 row and the regenerated
`website/content/cli-reference.md` are **T1's**, and T8 checks them rather than writing them.
**Depends on.** T1–T7.
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/mailbox ./internal/budget -run 'Docs|Lint|Parity' -count=1`,
`gofmt -l internal/`, and `GOFLAGS=-p=2 go vet ./...`.
**Reviewer tier.** standard. The checklist carries, at least: the QR holds no key and no identity;
the fingerprint's input and the no-side-effect rule; the envelope is a capability and not a boundary;
the key is never printed; the device node holds no secret; the two role sets are refused separately;
the sweeper never answers; and — the line no test can hold — that §8.4's table and this plan still
agree on every row.

---

## 16. File ownership, levels and PR groups

| File | Tasks | Note |
| --- | --- | --- |
| `internal/cli/root.go` | **T1** | **one line**; P4.1's line (`newMailboxCmd()`) is its own and the two are independent — the mobile plan reserves one line for each |
| `internal/localcfg/localcfg.go` + `mailbox_test.go` | **T3** | *(added 2026-10-29, decision 9)* the `mailbox` block widened to one keychain item per pairing; P4.1 owns the merged one-item form, so this is a dependent edit |
| `internal/cli/pair.go` | **T1a**, T2, T3, T4 | one writer per task: T1a the surface and the QR, T2 the wait and the compare, T3 the delivery, T4 `ls` and `revoke` |
| `internal/cli/pair_test.go` | **T1a** | new; the tests §15 T1 names, plus T2's to extend |
| `internal/cli/pair_events.go` | **T1a** | new *(added 2026-10-29)*: the `--json` stream's encoder, its version and the whole event set — `qr`, `waiting`, `code`, `done`, `refused` — so T2b and T3 emit into one shape instead of each inventing one |
| `internal/cli/testdata/pair-events.golden.jsonl` | **T1a** | new *(added 2026-10-29)*: the stream, one line per event, decoded strictly on every run |
| `internal/budget/token/pairing.go` + `pairing_test.go` | **T1a** | new *(added 2026-10-29)*: `MintPairing`, `PairingLifetime`, `PairingMaxLifetime`, `PairingSlack`, `PairingPrefix`; §1's row says why `Mint` cannot mint a `p~` token, and T1a is its only writer — T2's `device.go` is the same package's other new file and shares no name with it |
| `internal/budget/token/device.go` | **T2 and T3** | the one deliberate two-writer file: T2 lands the device token and the fingerprint, T3 extends it with the delivery envelope in a dependent task — the precedent is P4.1's `mac.go` (T2 `verify`, T2a `sign`) |
| `internal/budget/sweep.go` | **T4** | the prefix rule and the report field; **P6.13's delete set is not here** — the merged plan's decision 8 keeps the two apart, and a task that puts a `mailbox/` path in this file is a finding |
| `internal/budget/sweep_device_test.go` | T4 | new |
| `internal/cli/mailbox.go` | T5, T6, T7 | P4.1's file. **P6.7 owns T5 and T6; P6.13 owns T7** — the merged plan's §8.4 and decision 8 assign every row, and a task here that takes a row the table assigns away is a finding, not an edit |
| `internal/mailbox/action.go` | T5 | new; the refusal table and the Go re-validation of `shared/confirm` |
| `internal/mailbox/expiry.go` | T6 | new here; P4.1's T7 names the same file for its own half, so **P4.1 lands it first and T6 extends it** |
| `internal/mailbox/mac.go` | **nobody here** | P4.1's one MAC function; this plan **calls** it and edits nothing in it |
| `shared/` | **nobody** | every module these tasks read is merged; a task that needs to edit one is a finding |
| `internal/budget/rules/` | **nobody** | M14: no generation |
| `website/content/cli-reference.md` | T1 | **generated** — never hand-edited |
| `docs/design/v1.md` | T1 | one §9.1 row and the `--json` paragraph; **not test-enforced**, and T8's checklist carries it |
| `docs/mobile/pairing-review-checklist.md` | T8 | new, short |
| `docs/plans/2026-10-22-mobile-pairing-cli.md` | **this plan, not a task** | this document. **No pin ships with it** (M1, decision 10) |
| `.github/workflows/**` | **nobody** | Global Constraint 8; nothing here needs a workflow |

**Levels** (the mobile plan's): T1–T4 are P6.6's and follow in one lane, one writer each, because each
builds on the one before; T5 and T6 are P6.7's and **cannot start before P4.1 lands** (they extend a
file that does not exist); T7 is P6.13's and cannot start before T6; T8 is the review's. **The
dependency edges the mobile plan states explicitly** — `P4.1 → P6.6` (the `root.go` line and the
keychain entry), `P4.1 → P6.7` (this file), `P6.7 → P6.13` — are respected: nothing here touches
`shared/`, the rules template, or the MAC function, and T5/T6/T7 are deliberately *extensions* of
P4.1's command rather than a second one.

**PR groups.** T1–T4 together (P6.6 is one act with four orderings, and reviewing the QR apart from
the delivery reviews half a binding), **or** T1+T2 and T3+T4 separately if the owner prefers smaller
diffs; T5 and T6 together (one file, one ledger, one expiry — reviewing them apart reviews half a
gate); T7 alone, at its higher tier; T8 with whichever PR lands last, or alone. **P6.6, P6.7 and
P6.13 are each SECURITY-SENSITIVE** (the mobile plan's group P-F and Global Constraint 14).

---

## 17. What this plan does not do

1. **It writes no code.** Not one file outside this document is created or edited by this change —
   including no test, which M1 and decision 10 state as a cost.
2. **It approves nothing.** The mobile plan's Phase 6 approval of 2026-10-18 stands on its own; this
   plan is the shape those three tasks will take, and the owner approves or refuses it.
3. **It does not build P4.1.** The consumer's surface, the MAC function, the lease, the ledger and
   the hidden sweeper are P4.1's and are **unbuilt**; T5, T6 and T7 extend them. *(Amended
   2026-10-29: the MAC function, the lease and the ledger have merged; the consumer's surface and
   the hidden sweeper have not.)* If the owner wants
   the pairing CLI without the consumer, T1–T4 stand alone and are useful — they are what makes the
   merged MAC code reachable in production.
4. **It does not build the app's half.** P6.8 (the app's pairing and overview) and P3.1's secure-store
   rule are the app's; §4.4 and §7.2 state the contract P6.8 consumes and nothing more.
5. **It does not change the rules, the golden or `RulesVersion`** (M14), and it forces no owner-run
   deploy.
6. **It does not touch `shared/`**, and it adds no dependency: `crypto/ecdh` and `crypto/hkdf` are
   the standard library (T3 asserts `go.mod` is unchanged).
7. **It does not decide the app's store forms.** §13 lists what the owner must re-answer and states
   that the named memory could not be read here; it fills in no form and runs no store command.
8. **It adds no new command beyond `fugaro pair` and its two subcommands.** The consumer is P4.1's
   one command; the sweeper is P4.1's hidden one.
9. **It touches `belong`, EdgeWeb and EdgeServer nowhere** (Global Constraint 11), and it neither
   reads nor writes any secret, token, key or certificate — the MAC key is a value P6.6 **generates**
   at runtime and never prints.

---

## 18. Owner decisions

Each with a recommendation and the cost of being wrong. **Decisions 1 and 2 are the two where this
plan diverges from the mobile plan's task text**, and they are the ones that hold P6.6 back.

1. **The `p~` Auth user: does P6.6 delete it, or the sweep?**
   P6.6's task text says it *"deletes the `p~` Auth user and the `pairing/<uid>` node once the
   delivery has been read"*. Deleting an Auth user needs `roles/firebaseauth.admin`, whose **only**
   Terraform-granted holder is the history job's account (`iam.tf`'s `history_auth`), and mobile
   reconciliation G13 says outright that *"no person holds a Terraform-granted role to delete a
   phone's Auth user"*; D166's answer already chose *"a sweep change, not a new human grant"* for the
   `m~` orphan. **Recommended: the sweep.** P6.6's rule in `internal/budget/sweep.go` covers **both**
   prefixes — an `m~` user whose device node is gone, and a `p~` user older than **`token.Lifetime`
   plus one sweep interval** (a session cannot outlive the ID token, §11) — and the CLI deletes only
   the **node** on success, which database admin allows. **Cost:** P6.6's file list says "the `m~`
   rule"; this makes it a two-prefix rule, which is a divergence from the task text and is why it is
   asked. **The alternative** — P6.6 deleting the user under the operator's ADC — needs a role no
   Terraform grant gives the operator (an installation's owners or editors may hold it through basic
   roles, §19), so in practice it would refuse or be skipped and the orphans would accumulate.
2. **The abandoned `pairing/$uid` node: who deletes it?**
   Nothing in the rules puts a clock on a pairing session (§2 (a), M2). **Recommended: the CLI
   deletes it on success (T1's step 9), and the sweep deletes it when it deletes the `p~` user** —
   one rule, one place, and the node cannot outlive the uid it belongs to. **Cost:** the sweeper
   gains a **second** node family to delete (it deletes only under `runs/` today), which is a real
   widening of its remit and needs its own test. **The alternative** — leaving it — leaves a payload
   node readable by a `p~` uid for the hour its ID token lives and, if the session is abandoned,
   forever.
3. **The typed code's exact shape.**
   **Recommended: 8 characters of Crockford base32 (40 bits) over the first 5 bytes of
   SHA-256(decoded key bytes), printed as `XXXX-XXXX`**, derived by both ends from the **decoded**
   key so a spelling cannot change it (§2 (e)), never written to the database (§2 (a) 1), and
   compared by the CLI against what it read, refusing on mismatch with no side effect. Crockford's
   alphabet excludes `I`, `L`, `O` and `U`, and the code is read aloud and typed by a human.
   **Cost:** 40 bits is far more than the comparison needs, so a shorter code would do; the reason
   for the length is that the code's job is to make a *mismatch* visible, and a shorter code makes a
   coincidence likelier. **The alternative** — a full hex digest — is untypeable.
4. **Is the delivery a second envelope in `internal/budget/token`?**
   **Recommended: yes** — a new X25519 + HKDF-SHA-256 + AES-256-GCM envelope in `device.go`, sharing
   `seal.go`'s shape (version byte first, nonce, tag, AAD) and **not** its `Seal(key, …)` API,
   because the delivery's key is agreed and not shared (§8). **Cost:** a second envelope is a second
   thing to keep right; the mitigation is that it copies the first one's reasoning and both halves
   come from the standard library. **The alternative** — a third-party crypto module — adds a
   dependency to a package whose `go.mod` currently has `golang.org/x/crypto` as an indirect only.
5. **D199 and D233: what does each carrier hold?**
   **Recommended: §4.4's table** — the QR carries the **bootstrap** configuration and the one-shot
   token and no key and no identity (#787/D233), and the **delivery** carries the configuration the
   app **stores** (D199) plus the device token and the MAC key. **Cost:** the same configuration
   travels twice, and the app has two copies that could disagree; the rule that resolves it is that
   the delivery's copy is authoritative and a disagreement is a refusal (§4.4). **The alternative** —
   the QR carrying only the token — is not available: the phone cannot exchange a custom token before
   it knows the project's API key.
6. **The sweep's report fields, and the `p~` age.**
   **Recommended:** `SweepReport`'s `UsersDeleted` counts every Auth user deleted (its doc comment
   changes to say `r~`, `m~` and `p~`), and one new count names the deleted pairing nodes. The `p~`
   age is `token.Lifetime` plus one sweep interval, and a test pins that the Go constant equals
   `token.Lifetime`. **Cost:** two rows in a struct the CLI prints, and a number that is a sum of two
   existing values rather than a fresh one. **The alternative** — reporting nothing — leaves the
   operator unable to see that a sweep removed a pairing.
7. **Is the code ever written to the database?**
   **Recommended: no, and it cannot be** — the template's `$other: { ".validate": false }` on the
   request node refuses any field but `key` and `at` (§2 (a) 1), so a phone that writes its code is
   refused by the deployed document. **Cost:** the phone must derive the code before it writes, and
   the CLI must derive it from the key it read, so a disagreement about the derivation is a pairing
   that cannot complete rather than one that completes wrongly. **The alternative** — writing the
   code — needs a rules generation (M14) and puts the binding's input where a reader can see it.
8. **`fugaro pair`'s exit codes: its own, or the consumer's?**
   **Recommended: its own three rows (§4.6)** — 0 performed, 1 an operator-fixable refusal, 2 an
   outage — and **not** the consumer's four-clause predicate (M11), which asks whether a *pass* read
   messages and acted on none. **Cost:** one more exit table to keep right, in a command that reads
   no mailbox. **The alternative** — reusing M12 — invents a refusal that cannot happen.
9. **One keychain item per pairing, or one per project?**
   **Recommended: one per pairing** (D171, D232: the two secure-store items stay two), written by
   P6.6 under the item name the project's local config holds (P4.1's one `internal/localcfg` field).
   **Cost:** `revoke` must name the pairing whose item it deletes, and the item name must therefore
   be derivable from the device uid — which is why the local config holds the name **and** the
   pairing's uid. **The alternative** — one item per project — is D232's rejected shape.
   *(Amended 2026-10-29: two corrections. D232 is about the **phone's** secure store — two items,
   `idToken` and `macKey` — and does not rule on the laptop's keychain; the per-pairing ground is
   D171 alone. And the merged P4.1 config holds **one** item name per project
   (`internal/localcfg`'s `Mailbox.KeychainItem`, validated by `keychain.ParseItem`), which the
   consumer reads once. So "one per pairing" with more than one paired phone needs that block to
   hold one name per pairing uid — a change to `internal/localcfg` that §16's table
   gives no task. The owner chooses: one paired device per project in v1, which fits the merged
   config unchanged, or the widened block, added to T3's files. **Recommended: the widened block in
   T3**, since D171's per-pairing key is what makes `revoke` revoke one phone.)*
    **ANSWERED 2026-10-29 (owner): one keychain item PER PAIRING (amended).** Widen the
    `mailbox.keychain_item` config block in `internal/localcfg` and add it to T3's files (§15, §16):
    a per-pairing key lets `revoke` revoke exactly one phone.
    **Amended 2026-10-30 (T3b): the name, and its arithmetic.** T3b derives the name as
    `<project>-<uid without its m~ prefix>` — the project's name, one separator, and the uid's
    22-character body — and it is **the whole of the name** because the ceiling is
    `keychain.ParseItem`'s 64 characters, the uid's body is 22 of them, and a project name may be 40
    (1–40 of `[a-z0-9-]`). A fixed prefix of any length — the obvious `fugaro-mailbox-` — is
    therefore a name that cannot be built for the longest project names the local config accepts,
    and a pairing that cannot name its item is one that cannot store a key at all. The two
    constraints pull in opposite directions and the ceiling wins: the project's name
    must be in the name (M5's `ParseItem` rule, so a config cannot name another project's item) and
    the uid's body must be in it (so `revoke` finds the item from the uid, this decision's own
    requirement), which leaves exactly one
    character of slack. **Cost:** the name is less obviously ours in a keychain UI. **The
    alternative** — a shorter uid body — is a change to the uid's shape (§2 (e)) for the sake of a
    label.
10. **Does this plan ship a pin?**
    **Recommended: no.** The owner's ruling for this change is **docs only, no code**, and a pin is a
    Go test. **Cost, stated plainly:** nothing reads this document, so its citations, its §8.4
    references and its claim that the three tasks' files are absent can rot silently, and only a
    reviewer catches it. The merged P4.1 plan's pin
    (`internal/cli/docs_mobile_mailbox_plan_test.go`) is the precedent and the machinery exists
    (a derived citation list, a negative control) if the owner wants one as a **separate, small
    task** — it is not part of P6.6, P6.7 or P6.13, and building it here would be code.
11. **Does `pair ls` report whether the device's Auth user still exists?**
    **Recommended: no — the column is dropped (§4.2).** The Auth user is not in the database, so the
    database admin `ls` reads with cannot reach it; answering the column needs
    `firebaseauth.users.get`, and `roles/firebaseauth.admin` is bound by `iam.tf` to the history job's
    account alone. **Cost:** the operator loses a diagnostic — an orphaned `m~` user is invisible in
    `pair ls` and shows up only in the sweep's report (T4, decision 6). **The alternative** — adding
    the role to the same binding set as `token_minter` — is a **wider** grant than the minting role
    it would sit beside: `firebaseauth.admin` creates and deletes users and updates their custom
    attributes, which is a second way to set a claim, and the rules' `userClaim` comment already
    treats claim-setting as the launcher-equivalent power. So the recommendation is to drop the
    column rather than hand every launcher and operator that role for a column in a listing.
12. **Added 2026-10-29: is `fugaro pair` core (MIT) or an `ee/` command?**
    **Recommended: core**, as this plan has it — `internal/cli/pair.go` and one `root.AddCommand`
    line. The Subscription Terms §5.2 put the Command-line tool under the MIT licence; the merged
    P4.1 consumer is core; the device node and the rules are core mechanism (ee-reorg D521's
    reading); and D637 places `fugaro user` over the IAM lists in the core while "roles enforcement
    beyond the lists" goes to `ee/`. **Cost:** subscriptions S5.1 lists "Pairing: `fugaro pair`"
    under the roles design (D446) without placing it, so this is the owner's word to give. **The
    alternative** — an `ee/` command — moves T1–T4's files under `ee/`, registers through
    `ee/register` and `cli.RegisterCommand`, adds a core stub through the paid-command list, and
    leaves an untagged build with no way to pair a phone.
    **ANSWERED 2026-10-29 (owner): `fugaro pair` is a CORE (MIT) command**, as the plan has it.
13. **Added 2026-10-29: which words may `--role` take, and is a pairing a seat?**
    **Recommended:** v1 accepts `viewer`, `operator` and `admin` — `docs/mobile-auth/spec.md`'s
    table and the Subscription Terms §5.3's viewer, operator and administrator — stored on the node
    as D225 says and read by no rule; the roles design (D446, subscriptions S5.1) replaces the word
    set when it lands. The role stays a **node field** and not a claim: D628's "role as a claim"
    concerns a person's sign-in through a minter the ranked people cannot call, which pairing does
    not use, and the device token carries `mu` alone. **Cost:** a word set chosen before the roles
    design; the Terms make an operator or administrator a User needing a seat (§5.1, §5.3), and
    pairing records the role but counts nothing — the seat estimate (§5.5) is the console's.
    **The alternative** — no `--role` until the roles design — leaves D225's field unwritten.
    **ANSWERED 2026-10-29 (owner), and it is NOT the recommendation above** (the recommendation was
    the fixed words `viewer`, `operator` and `admin`, free to choose; that text is kept visible and
    is **superseded**). The role is **inherited from the user doing the pairing**: the paired device's
    role defaults to the pairing user's own role, and `--role` may only **lower** it, never raise it
    above the pairing user's role. Until the roles design (D446/S5.1) provides verified roles, the
    pairing user's role comes from the CLI's existing role lists (`internal/cli/roles.go`:
    launcher and operator lists) mapped to the device words (launcher to `operator`; the lists hold
    no viewer or admin yet, so a `viewer` device is reachable only by lowering). The role stays a
    **field on the device node** (D225), not a token claim. Pairing counts **no seats**: the seat
    estimate (Terms 5.5) is the console's.
    **Open point.** The exact source of the inherited role depends on the roles design (D446/S5.1)
    landing: the lists are a stand-in, and when verified roles exist T1's lookup is the one place
    that changes; the rule (inherit, lower only) does not.

---

## 19. Unverified, and what cannot be verified here

1. **No Node, no network, no emulator, no real keychain, no real Firebase project.** This container
   has none of them, so nothing in §14 has been run: every verification above is a command a reviewer
   runs, not a result. The rules' behaviour under a real deploy is the owner's check (P6.4's checks
   are in `docs/gcp-live-checklist.md`), and the deployed generation is the owner's deploy.
2. **The named memory could not be read.** `mobile-telemetry-and-store-forms.md` is not in this
   container's memory directory (it is empty), so §13 is grounded in `mobile/app.config.js`,
   `mobile/src/app-config.test.js`, `mobile/src/app-config.ids.test.js`, `mobile/README.md` and
   `docs/design/desktop-console.md` §7.4 instead. **If that memory holds a ruling beyond those
   files, §13 is incomplete and the owner's own answer governs.**
3. **The real IAM layout of a live project is not visible here.** §6 and §4.6 state what `iam.tf`
   grants; whether a given installation's operators hold `roles/firebasedatabase.admin` or
   `roles/firebaseauth.admin` through basic roles or outside Terraform is not readable from this
   tree (mobile reconciliation G13 says owners or editors may). Decisions 1 and 2 are written to need
   **neither**.
4. **`crypto/hkdf` and `crypto/ecdh` are asserted from the standard library, not exercised.** The
   toolchain here is Go 1.27 per `go.mod`, and no file in the tree imports either — so T3's first
   test is what proves the claim, and it has not been run.
5. **The fingerprint's human factors are unmeasured.** Whether 8 Crockford characters read off a
   phone screen are typed correctly by an operator under time pressure is a usability question no
   test here answers; decision 3 fixes the shape and P6.8 is where it can be measured.
6. **The store forms are unfilled and the export declaration is unreaffirmed** (§13). No `eas`
   command, no store console, no submission.
7. **D199's provenance is an owner ruling relayed to this change, not a document in the tree.** §2's
   header says so and `docs/coordinator/README.md`'s "D198 onward is free" is the block it sits in.
   If the owner's ruling was narrower than "the installation's Firebase config in the encrypted
   pairing payload", §4.4 and decision 5 are the two places to correct.
8. **The `p~` session's real lifetime against a live Firebase is inferred, not measured.** §11's hour
   is `token.Lifetime` and the ID-token persistence is UNVERIFIED U2 of the mobile reconciliation —
   so decision 1's age is derived from a documented behaviour, and a real project is what would
   confirm it.
9. **The OS keychain's storing side exists on Linux and on macOS.** *(Rewritten 2026-10-30, T3c;
   added the same day as T3b, which is when darwin still refused.)* The reader has a `security` row
   and a `secret-tool` row, and so does the writer. M11 keeps the key out of every argv on both:
   `secret-tool store` reads it from standard input, and darwin's `security` is run as `security
   -i` — the whole argv is the single element `-i` — with one command per run on its standard
   input, `add-generic-password -U -s "<item>" -a "<item>" -w <key>` to store and
   `delete-generic-password -s "<item>" -a "<item>"` to remove — the removal names the account as
   well, so that it is the exact inverse of the store rather than a match on the service alone. The
   item's name is not a secret (M5) and is quoted into that command; the key has one spelling
   (D171: `EncodeKey`'s unpadded base64url), whose alphabet holds no byte a command line's parser
   can read as anything but part of the value, so nothing is ever escaped, and a key outside that
   alphabet is refused rather than escaped — a key the parser read as something else would be
   stored as something else while the store reported success. **`security -i` answers 0 for a
   command it could not carry out**, so its exit status alone would read a refused store as a
   successful one and `fugaro pair` would deliver a key that is not in the keychain: the writer
   reads the tool's output and maps it to `ErrBackend` in this package's own sentence, never
   repeating what the tool said, because that text can carry the command line it was given — and
   that line carries the key. The reading fails closed: a successful add or delete prints nothing,
   so every non-blank line is a failure, wherever in the line the message sits (a prompt or an echo
   before it must not hide it) and whether or not this package recognises it. **What is verified where.** The invocation, the
   quoting, the refusals and the failure reading are hermetic: `keychain_test.go` puts a stand-in
   `security` first on `PATH` and runs the writer against it as a real child process, which is
   where "no argv element and no environment variable carries the key" is observed at all. The real
   tool is reached only by `internal/mailbox/keychain/keychain_darwin_live_test.go`, behind the
   `darwin && keychain_live` tags, and **the owner runs it himself** in his own terminal — M5's
   rule is that no agent reads a keychain — with `go test -tags keychain_live
   ./internal/mailbox/keychain/ -run Live -count=1 -v`; without the tags the file is not compiled,
   so a plain `go test ./...` on a Mac never touches a keychain.

---

## 20. Risks

| Risk | What it costs | Where it is held |
| --- | --- | --- |
| The two role sets are not what an installation holds | `fugaro pair` refuses for everyone, and pairing has no other door (D166: CLI only) | §4.6's two named refusals, and T2's two tests; decision 1 removes the need for the third role |
| A photographed QR | the copier writes its own key into the request node first and the real phone cannot | the write-once rule (§2 (a)) plus **the operator-typed fingerprint** (M3) — the binding `pairingClaim`'s comment names |
| The fingerprint is ambiguous or too short | the one human check becomes a formality | decision 3's alphabet and length; T2's mutation on the derivation |
| The MAC key leaks into a log, a `--json` document or a fixture | the key authenticates what the phone speaks aloud (D171) | M5's never-printed rule, T2's and T3's tests, and T8's checklist line |
| The device node is written **after** the token | a paired phone fails `USER` on everything, with nothing on screen to explain it | M4, §7.3's first ordering, T4's Red step |
| The delivery node outlives its session | a payload readable by a `p~` uid for the hour its ID token lives | T1's step 9 and decision 2's sweep rule |
| An orphaned `p~` user or `pairing/$uid` node | invisible, unbilled, and a uid `pairingClaim` would still admit | M2, decisions 1 and 2, T4's tests |
| The sweeper answers a conversation | two writers of the "exactly once" answer | M8, T7's test that it writes no answer, and T7's higher tier |
| A second MAC implementation | two chances to disagree about what is covered | M7: `internal/mailbox/mac.go` keeps one function, and T8's checklist line |
| The envelope is read as a boundary | a reviewer relaxes because "it is encrypted" | §8's closing paragraph, `seal.go`'s own words, T3's reviewer note |
| A rules generation is forced by a field | an owner-run deploy on every installation | M14 and §12's four claims; a task that edits the template is a finding |
| The plan rots, because it ships no pin | its citations and its file-absence claims are unguarded | M1, decision 10, and T8's checklist — stated as a cost rather than hidden |
| The store forms are answered against the wrong facts | a submission that describes an app the user does not have | §13's three artifacts, each cited; the owner's answer, not an agent's |

---

## 21. Execution

1. **The owner answers decisions 1, 2, 3 and 5** — the four that hold P6.6 back (1 and 2 are the two
   divergences from the task text) — and 4 and 6–10 if convenient. Answering them approves no task:
   P6.6, P6.7 and P6.13 are already approved, and this document is their shape. *(Amended
   2026-10-29: decisions 9, 12 and 13 also came before T1; the owner **answered all three on
   2026-10-29**, §18. No task is approved by this plan: T1–T8 stay unapproved.)*
2. **P4.1 must land before T5, T6 or T7.** They extend `internal/cli/mailbox.go` and
   `internal/mailbox/expiry.go`, neither of which exists. **T1–T4 do not depend on P4.1** beyond the
   `root.go` list they both append to, and they are what makes the merged MAC code reachable in
   production — so if the owner wants pairing first, T1–T4 stand alone.
3. Then T1, T2, T3 and T4 in that order, one writer each, SECURITY-SENSITIVE, each with its
   independent opus review; then T5 and T6; then T7 at its higher tier; then T8.
4. **Every task:** commit, then `fugaro verify build` and `fugaro verify test` in the foreground with
   the working tree clean, plus the Go packages the task touches
   (`go test ./internal/<pkg>/ -run …` — **never `go test ./...`**), `gofmt -l`, and
   `GOFLAGS=-p=2 go vet ./...`. No task here touches JS, so no `npm` run is owed by any of them.
5. **The one mutation proof is T5's** (the inbound gate), and every other Red step is written, not
   mutated.
6. Nothing in this plan merges without a passing verification, its Red step, and its reviewer tier —
   and **no PR is opened, commented on or reviewed by an agent**: Fugaro owns the pull request.
