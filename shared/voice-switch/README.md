# `shared/voice-switch` — the voice switch and its indicator, as one pure function

Task **P5.3.4** of [the mobile plan](../../docs/mobile/implementation-plan.md), decided by **D123**
and **D119**, and the design is
[voice-evaluation-switch.md](../../docs/mobile/design/voice-evaluation-switch.md) §4.2 (the
indicator), §4.3 (the policy node) and §4.5 (the failure table). One pure function,
`indicator`, over the four inputs the app actually holds. Nothing here reads a clock, a file, a
database, a network or a random source — the inputs are the whole input, which is what D126 asks
of everything that can be a pure function.

```js
import { indicator } from './index.js';

const where = indicator({
  direction: 'stt',
  settings: { stt: 'remote', tts: 'local' },      // the device's two settings
  consent: [{ provider: 'deepgram', at: 1_700_000_000_000 }],  // what the user agreed to
  status: {                                       // the hop's last project-status answer
    ok: true,
    providers: ['deepgram'],
    direction: { stt: true, tts: true },
    reason: null,
  },
  lastCallInDirection: null,                      // this direction's last call, if any
});
// ok: true, engine: 'remote', provider: 'deepgram', state: 'remote',
// inFlight: false, reason: null, text: 'remote (deepgram)'
```

## The rule, in one paragraph

The switch is read per direction — remote STT sends raw audio and remote TTS sends text the
redactor has already reached, so they are two privacy propositions and the owner may answer them
differently (§3). **Remote needs both halves**: the project's own admission, which reaches the
phone only in the hop's project-status answer because the app holds no grant on
`config/policy/**` (§2, §4.4), and the user's consent **for that provider name**, recorded on the
device (§4.3). Either half missing is local. **Every failure lands on local**, which is the one
direction the whole table fails in (§4.5): an expired ID token, a revoked device node, an
unreachable hop, a failed query, a provider that returned an error, an unreadable policy node, no
consent. The one exception in wording is `local — checking`, which is the honest state when the
project's setting is not yet known.

## The states, and the branch order that produces them

**The order of these branches is the rule.** Each row is one test in `index.test.js` and at least
one case in `vectors.json`.

| # | The branch | The state | The badge | `reason` |
|---|---|---|---|---|
| 1 | a call in this direction is **in flight** | the call's | `remote (<name>) — in flight`, or `local — in flight` for a local call | the decision's, untouched |
| 2 | a call in this direction **failed** | `local` | `local` | the call's own failure, or `provider-error` for a remote call and `local-error` for a local one |
| 3 | the switch is not `remote` (unset included) | `local` | `local` | `switch-local` |
| 4 | no consent is recorded at all | `local` | `local` | `no-consent` |
| 5 | no project-status answer yet, or a query that failed | `checking` | `local — checking` | `no-answer` / `query-failed` |
| 6 | the hop's answer refuses this direction — `force-local` outranks the two lists beside it | `blocked` | `blocked by project policy` | the hop's own reason, or `no-admission` |
| 7 | no admitted name has a consent | `local` | `local` | `no-consent` |
| 8 | otherwise | `remote` | `remote (<the first admitted name consented to>)` | `null` |

**Steps 3 and 4 run before 5 and 6, and step 7 follows them, and that order is this module's
reading of the plan's state table.** When the device's own half already answers local — the switch
is off, or no consent exists — local is a fact about the device rather than a guess about the
project, so it is neither `checking` (there is nothing to wait for) nor `blocked` (the device is
what refused).
`checking` is therefore reached only when the device's half asks for remote and the project's
half is what is missing. A project that forces local is reported as `blocked` while the switch is
on, which is the moment the user is asking for remote and the refusal is what they need to see;
with the switch off, the same project answers `local`/`switch-local`.

**Steps 1 and 2 are the asked direction's last call, and that is what the input's name says.** A
call in flight is what the badge shows, whatever the switch now says: a switch turned off mid-call
must not make the indicator say `local` while the audio is still going out — that is the one lie
the badge exists to prevent. The field is `lastCallInDirection` and not "the last call", because
the badge is per direction and the app holds one route per direction: each direction is its own
pipeline, so the caller passes the record for the direction it is asking about. A route that names
the **other** direction is read as no call in this one — a badge asked about `stt` can never be
made to show a `tts` call in flight — and the contract is pinned by a test
(`the route read is the asked direction's …`) rather than only by this sentence. A failed call is
§4.5's fallback: the app answers from the local engine and the badge says `local`.

**Step 6 reads `force-local` before its two lists, because the policy node's own switch outranks
them** (§4.3): an answer that lists this direction and an admitted name while saying the project
forces local is read as a refusal rather than as an admission — `blocked`, with `force-local` as
the reason. `no-admission` and `no-register-row` are consequences of the lists, so they are read
where the lists refuse.

## The two triples, and the two cases where they differ

| The question | The fields |
|---|---|
| what the indicator shows **right now** | `state`, `inFlight`, `text` |
| where a new utterance in this direction goes | `engine`, `provider`, `reason` |

The egress triple holds a **fixed invariant in every answer**: `engine` is `remote` exactly when
`provider` is a name and `reason` is `null`. A caller that reads those three before sending is
never told remote by anything but a live, admissible decision.

They differ only while a call is in flight, in the two cases the vectors name, and they differ in
the safe direction. The first is **a remote call in flight whose switch (or project) has since
stopped admitting it**: the badge says `remote (<name>) — in flight`, because that is what is
happening, while `engine` is `local` and `provider` is `null`, because nothing new may be sent. The
second is **a local call in flight over a decision that admits remote** (the vectors' "a local call
in flight shows local in flight and leaves the decision alone"): the badge says
`local — in flight`, because that is where the audio went, while `engine` is still `remote` and
`reason` is `null`, because the project and the device both still admit one. `inFlight` is what
tells the caller not to start a second call at all (P5.3.7's egress path), and the egress triple is
the one a caller reads before sending.

## The badge's words

`local`, `local — checking`, `blocked by project policy` and `remote (<provider name>)` are the
design's own words (§4.2), returned verbatim as `text` — including the em dash. A caller renders
`text`; the state and the name are in the fields for anything else. A badge that renders a string
this module does not return is rendering something that did not come from here.

## Refusals: fail closed, never coerce

A refusal is `{ ok: false, code, field }`, frozen, and carries **no badge and no engine** — a
caller that cannot read its own input serves locally, which is the direction every failure in
this module falls. An **absent** field is its default, and every default is the local one; a
**present but unreadable** field is a refusal, because a value this module could not read is a
bug in the caller rather than a fact about the world. A `null` in a place an object belongs (the
app's storage returns `null` for a key it has never written) is read as absent.

Every code the module can return is here, and a test fails if this table and `REFUSAL` ever
drift apart — the test's own probe table has one probe per code, so a new code without a probe is
a failure too:

| Code | When |
|---|---|
| `not-an-object` | the input, or the `status`, is not a plain object (`null`, an array, a primitive, an instance) |
| `unknown-field` | a field this module does not name is present — the field set **is** the version |
| `missing-field` | a required field is absent or explicitly `undefined` |
| `accessor-property` | a field read through a getter — refused **without ever calling it** |
| `internal-error` | the input threw while being read (a revoked or throwing `Proxy`); what was thrown is never echoed |
| `wrong-type` | `status.ok` is not a boolean, or a direction admission is not one |
| `unknown-direction` | a `direction` that is neither `stt` nor `tts` |
| `unknown-engine` | a switch or a call's engine that is neither `local` nor `remote` |
| `unknown-call-state` | a call's `state` outside `in-flight` / `done` / `failed` |
| `unknown-code` | a `status.code`, a `status.reason` or a `lastCallInDirection.reason` outside its closed set |
| `bad-provider` | a provider name that is not a name: empty, past 64 characters, or URL-shaped |
| `invalid-at` | a consent's `at` that is not an epoch-millisecond integer in `1 .. MAX_TIMESTAMP` |
| `invalid-consent` | a `consent` that is not a plain array, or is longer than 64 |
| `duplicate-consent` | two consent records for one provider name |
| `invalid-providers` | an answer's `providers` that is not a plain array, or is longer than 64 |
| `duplicate-provider` | two entries for one name in an answer's `providers` |
| `unexpected-provider` | a call whose engine is local and which names a provider |
| `unexpected-reason` | a call that did not fail and carries a failure reason |

`not-an-object`, `unknown-field`, `missing-field`, `accessor-property`, `internal-error` and
`wrong-type` are spelled exactly as [`shared/confirm`](../confirm/README.md) spells them, so a
reader of both does not have to learn two vocabularies for the same failures. No refusal echoes a
value, and **no refusal names a key the caller chose**: an unknown key is reported by the
container this module named (`input`, `settings`, `status`, `lastCallInDirection`), never by the key itself — a
refusal that repeated the input would make the refusal itself a channel.

## The numbers

| Name | Value | Why |
|---|---|---|
| `PROVIDER_MAX` | 64 | a name is an operator's key in the policy node and reaches a screen |
| `MAX_PROVIDERS` | 64 | a list past the cap is refused rather than rendered |
| `MAX_CONSENT` | 64 | one record per consented name |

The timestamp bound is **`shared/lease`'s `MAX_TIMESTAMP`, imported**: two modules must not
disagree about what a time is. `0` and `-0` are refused (`at: 0` is the shape of an *unset*
counter, and silently reading it as 1970 would hide a writer bug behind a date that looks real).

## What a provider name is

**A name, never a URL** (§4.3: "an entry is a provider name, never a URL — a policy-carried URL
is a policy-carried redirection"). One pattern, `/^[A-Za-z0-9][A-Za-z0-9._-]*$/`, settles empty,
oversize, control characters, lone surrogates, invisible format characters and URL-shaped entries
at once: `/`, `:` and everything else outside the class are refused, so `https://…` cannot be a
name and neither can a string that renders as one thing and reads as another. The design's
register names (§7) are lowercase and fit.

## What this module is *not*

- **Not the hop, and not the policy.** It never reads `config/policy/**` and never decides what
  the project allows: the project's half arrives only as the input named `status`, one answer the
  app has already received. `blocked by project policy` is a report of that answer, never a
  conclusion this module reached about a policy.
- **Not the consent sheet, the settings store, the hop client or the egress path.** Storing the
  two settings, asking for a consent, calling the hop and applying §5's ten never-send rules are
  P5.3.6's and P5.3.7's. This module answers one question about one direction; a caller that
  skips those tasks has not built the switch.
- **Not a chooser among several providers.** When the project admits more than one consented
  name, the first in the hop's own order is named, so the badge is deterministic and the
  operator's order is kept. Choosing between two admitted providers on latency or price is the
  register's and Check 85's business, not this module's.
- **Not the clock.** It has none, and it needs none: `at` is validated and dropped, and nothing
  in the answer depends on the time.
- **Not a redactor, and not a claim about content.** Remote TTS is text the redactor has already
  reached; remote STT sends raw audio, and this module has no opinion about what is in it — it
  reports where it would go, which is the whole of §3's argument.

## Evidence, and how to reproduce it

```sh
cd shared && npm test                       # 514 tests in the package, 514 pass, 0 fail (Node 22)
shared/voice-switch/mutation-proof.sh       # 46 guards: 46 killed, 0 survived, 0 not applied
```

The suite pins the plan's verification list, each item as its own test: the state table walks the
four inputs; an unset switch is local on both directions; before the first project-status answer
the state is `local — checking` and never a guess; `remote (<provider name>)` is reachable only
with a recorded consent for **that** name; `blocked by project policy` comes from the hop's answer
and never from a policy the app cannot read; every failure input lands on local; and the exported
surface is pinned as an exact set, so **a new export is a decision that goes into that test**.
Beyond the plan's list the suite checks the invariant `engine === 'remote' ⟺ provider is a name ⟺
reason === null` over every probe of a walk of 3 360 combinations (2 directions x 6 settings x 4 consent states x 10 answers x 7 call routes), that the input is never
mutated, that every answer is frozen and the same input twice returns equal answers, and that
every declared state and reason is reachable in both directions.

`mutation-proof.sh` disables one guard at a time and requires the suite to fail. It starts with
the guard this task turns on — **a badge that trusts the switch while a remote call is already in
flight**, the one way the indicator can be made to say `local` while audio leaves the device — and
it refuses to report anything unless the pristine suite passes with tests actually run and a
`refuse()` that always succeeds is caught first.

`vectors.json` carries the state table **as data** (`cases: [{ name, input, expect }]`, 36 cases:
29 accepts and 7 refusals) and the suite replays it verbatim, so a row that stops being the rule is
a failure rather than a table nobody re-reads. The counts are pinned in the test: deleting a case
is a failure too. It is the only reader of that file today; a later task's tests that want the
table should read this file rather than copy it.

## Honest limits

- **Which record is "this direction's last call" is the caller's to pick, and this module cannot
  check it.** It reads `lastCallInDirection` and compares the record's own `direction` with the one
  it was asked about, so a route for the other direction can never enter this direction's answer —
  but a caller that keeps one call record for the whole app and passes it for both directions will
  still be told `local`, `inFlight: false` for the direction whose stream is actually live. Holding
  one route per direction is the caller's half of the contract; a shape carrying both routes at
  once (`calls: { stt, tts }`) would make it the module's, and is a later decision rather than a
  fix round's.
- **The badge and the egress decision can disagree, by design, while a call is in flight** — in the
  two cases the vectors name: a remote call that is no longer admissible, and a local call over a
  decision that still admits remote. Both fields are right about their own question, and a caller
  that reads only `state` will believe the wrong thing about where a *new* utterance goes. Reading
  `engine` before sending, and `inFlight` before starting anything, is what the split is for.
- **A refusal is not an answer, and the fallback is the caller's.** This module refuses rather
  than guess, so an app handed a bug in its own settings must still draw a badge:
  `local — checking` is the correct stand-in, and no test here can make the app draw it.
- **A provider name outside `[A-Za-z0-9._-]` is refused**, even if a register row spells it that
  way — a name with a character this module cannot carry is a name that would reach a screen
  through a string it did not check. The register's names (§7) are lowercase ASCII and fit.
- **Consent is per provider name, not per direction.** The design's §4.3 records consent for a
  name; narrowing it per direction is the switch's job (steps 3 and 7), so a user who consented
  to a name for reading aloud has not thereby consented to it for their microphone unless the
  `stt` switch says so — which is what the switch defaults closed for.
- **Nothing here is wired to a hop, a device or an app.** No task of the mobile plan is approved
  beyond P5.3, and this module is not evidence that one is: it is logic with tests, and its
  consumers — P5.3.6's rendering and P5.3.7's egress path — are still to come. Until they land,
  the switch is the answer and not the behaviour.
- **`vectors.json` has no second implementation to agree with.** Unlike `shared/confirm`'s, whose
  vectors wait on a Go parity test, this module is the app's only implementation of the table, so
  its vectors' job is to be the table rather than to prove two implementations equal.
- **`shared/README.md` is not edited by this task**, and the module therefore has no line in the
  package's own contract yet. That file is P0.1's by the plan's shared-files table ("no level
  holds two writers of one file"), so the line belongs to a later sweep of P0.1's — recorded here
  rather than taken.
- **The owner's open items are reported, not decided.** P5.3's decision list (items 1–12, above
  its sub-task list) is not touched by this task and none of it gates it: no number here depends
  on Check 85, on the hop's host, on the register's rows or on whether the hop is kept warm.
