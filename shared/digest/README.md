# `shared/digest` — the digest, the severity rule and the redactor

Task **P1.3** of [the mobile implementation plan](../../docs/mobile/implementation-plan.md),
in the shared pure-logic package (decision **D119**). It reads the documents
`fugaro watch --json` emits and answers three questions: what happened, how
severe the worst of it is, and what of it is safe to say out loud.

Three files, and one of them is the entry point:

| File | What it is |
|---|---|
| `index.js` | The digest over the contract's documents, and the refusals |
| `severity.js` | The ladder: four severities and their total order |
| `redact.js` | The redactor, ported from `internal/agent/redact.go` |
| `errors.js` | `DigestError` and the register of refusal codes |

```sh
cd shared && npm test   # no install, no network, no device
```

## What it is for

The plan's words are the specification, and each sentence below is a behaviour
a test pins:

- **"a kill is the highest severity; a failed finish is next; a fast burn is
  next; nothing else interrupts"** — `severity.js`'s order, asserted over an
  exhaustive table of the three triggers (`index.test.js`) and as a strict,
  total, transitive order (`severity.test.js`).
- **"a missing field is `unavailable` and never spoken as `0`"** — the
  contract's own reader rule (`console/docs/json-contract.md` §10.13). A field
  the document does not carry, and a field it carries as `null` (the Go
  structs' `*float64` pointers marshal that way), both become the word
  `unavailable`. A `0` the document really carries stays `0`.
- **"the redactor removes token-shaped and key-shaped strings, and its tests
  include the shapes this tree actually uses"** — `redact.js`, ported whole
  from the CLI's own redactor with the CLI's own test vectors.
- **Fail closed** — an unknown field, a wrong type, an oversize string, a
  value outside an enumeration, a finish with nothing to classify it by and a
  document that contradicts itself are all refusals with their own code. This
  module is the last place a malformed document can be caught before it
  becomes something a person hears.

## The severity rule, and where its fields come from

Severity is read from fields the document already carries — never recomputed,
which is voice **D48**'s ruling and the reason the digest cannot disagree with
the CLI about the burn rate:

| Severity | Field | Source |
|---|---|---|
| `kill` | `JSONKill` on `JSONTotal` and on `JSONRepo` | `internal/watch/json.go`'s `jsonKill` |
| `failed` | `JSONFinished.Failed` / `.Status` on the `finished` and `ready` lists | `internal/watch/finished.go`'s `isFailure` |
| `fast_burn` | `JSONBurn.FastAlert` on `JSONTotal.Burn` and on `JSONRepo.Burn` | `internal/watch/json.go`'s `jsonBurn` |
| `info` | everything else, including every live `JSONRun` | — |

A finish is a failure when `status` is `failed`, `halted` or `infra_error` —
the CLI's own truth table, so `cancelled` is **not** a failure here either. A
status outside that set, a `failed` and a `status` that disagree, and a finish
carrying neither are all refusals: the digest does not get to guess a
severity, because a guessed severity is an alarm nobody's document supports.

The digest's item order is fixed and documented, so no consumer has to sort:
the project's own triggers, then each repository's in the document's order,
then the `ready` entries, then the `finished` ones, then the live runs.
Nothing here sorts by time — **the digest holds no clock**, which is what
makes the interrupt policy above it (voice V2.2, mobile P3.5) a pure function
too.

## The refusals

One code per class, in `errors.js`, and a test asserts the exact code — "it
threw" is not the property the plan asks for.

| Code | When |
|---|---|
| `E_NOT_A_DOCUMENT` | the digest was handed something that is not a document |
| `E_UNKNOWN_FIELD` | a field the contract does not name is present |
| `E_TYPE` | a field the contract names carries the wrong JSON type |
| `E_OVERSIZE` | a string field is longer than `MAX_FIELD_BYTES` (4096) |
| `E_ENUM_UNKNOWN` | `status`, `health` or `deadline` carries a value outside its set |
| `E_UNCLASSIFIED` | a finish carries neither `failed` nor `status` |
| `E_CONTRADICTION` | a finish's `failed` and `status` disagree |
| `E_SEVERITY_UNKNOWN` | a value outside the ladder was ranked or compared |
| `E_SECRETS_NOT_A_LIST` | the redactor was handed a secret list that is not a list of strings |

An oversize field is **refused, not clipped**: clipping drops silently what
was past the cut, and the redactor is the thing allowed to shorten text,
deliberately. A refusal never echoes the value it refuses, because the
documents this module reads carry agent-written text — `DigestError#path`
names the field instead.

## The redactor is a port, on purpose

`redact.js` is a port of `internal/agent/redact.go` — the redactor `fugaro
logs` and the agent relay already run agent-written text through — with its
own test vectors, not a new design. **D118** requires redaction before
anything is written to the mailbox and **D119** puts the redactor in this
package "so that the console and the phone cannot disagree about what is safe
to say out loud"; a second implementation of what is safe is a second answer
to that question.

- **Ported**: the raw form, the JSON-escaped form, each line of a multi-line
  secret, the four base64 alphabets, the alignment-independent base64 cores
  (a secret at any byte offset inside a longer encoded input), the wrapped
  line pieces at 76 and 64 columns, the query and path percent-escapes, and
  the two numeric rules — 4 bytes to be redacted at all, 8 to register an
  encoded form. The vectors in `redact.test.js` are the CLI's own, ported from
  `internal/agent/redactfunc_test.go` and `TestRelayRedactsEncodedSecrets`.
- **Not ported, deliberately**: the streaming `Redactor` writer. Both
  consumers redact text they already hold, which is the case the Go file
  itself prescribes `Redact` for, and a line-buffered writer is a stream's
  shape.
- **The limitation is ported too**, word for word from the Go header: this is
  **best-effort, not a guarantee**. A secret split across two fields, blocks
  or events; one the agent slices, truncates or reverses; one embedded
  mid-stream inside wrapped base64; and any encoding not in the list (hex,
  gzip, encryption) survive it. A caller that believes otherwise is worse off
  than one that never redacted.

**No secret-shaped literal appears in this directory's tests**, including the
redactor's: every fixture is built at run time by concatenation, as
`internal/agent/relay_test.go` builds its own, and the expected encodings are
computed with Node's own `Buffer` rather than with the module's encoder — so
the hand-rolled base64 has to agree with a reference instead of with itself.

## What is deliberately not here

- **The interrupt policy and its three modes** (`quiet`, `important`, `all`)
  are voice V2.2's and the mobile voice screen's (P3.5). This module classifies
  a document; it does not decide whether to speak.
- **The spoken sentence.** Items carry fields, not prose.
- **Anything with a runtime**: no clock, no randomness, no network, no
  filesystem, no state that survives a call, no third-party dependency —
  `node:test` in the tests and nothing else, and the base64 and the
  percent-escapes are written out here rather than imported.
- **The presence rule** (`JSONConn`, the heartbeat's staleness) is P1.4's;
  the connection block is validated here only so that an unknown field inside
  it is still refused.
- **The message schema** is P0.2's and the URL validator is P0.3's; the
  digest's redactor is what those two call, not the other way round.

## Honest gaps

- **This directory's tests run only where someone runs them.** The CI job that
  would run them is P4.3 and it is **owner-made** (Global Constraint 8: no
  agent edits `.github/workflows/`).
- **No live behaviour is claimed.** The plan's checks 80–89 are owner-run; none
  has been run, and nothing here asserts that a phone speaks anything
  correctly. This module is a pure function with tests, which is all the plan
  asks of it.
- **The package's own README still says "the skeleton, and nothing else"**
  (`shared/README.md`). That line is P0.1's file, which this task may not
  edit; the modules beside this one are the plan's other phase-1 tasks.

Where the reasoning lives: [implementation-plan.md](../../docs/mobile/implementation-plan.md)
(P1.3, and the "shared files" table that gives this package's manifest to P0.1
alone), [reconciliation.md](../../docs/mobile/reconciliation.md) (D118, D119),
[voice.md](../../docs/design/voice.md) (D48's severity table, D49 as amended
by D110), and the CLI's own redactor,
[`internal/agent/redact.go`](../../internal/agent/redact.go).
