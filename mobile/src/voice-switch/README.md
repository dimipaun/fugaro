<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->
<!-- See mobile/LICENSE.md for the terms. -->
# `voice-switch/` — the app's two voice settings, the indicator and the egress path

The phone's half of where an utterance may go (mobile plan, tasks **P5.3.6** and
**P5.3.7**; decisions **D111**, **D114**, **D118**, **D123**, **D127**; the
design is `docs/mobile/design/voice-evaluation-switch.md` §4.1–§4.5 and §5). Two
settings per account — `stt` and `tts`, each `local` by default — and the list of
provider names the user has agreed to; both are **device-local** and go through
the app's non-secret settings store. The badge is rendered by
`shared/voice-switch`'s one pure function, whose table this module does not
restate. And the sending is here with it: `hop.js` is the client, `egress.js` is
§5's ten never-send rules, and no other file in the app opens the network.

```js
import { createVoiceSwitch } from './index.js';

const voice = createVoiceSwitch({
  settings,          // src/accounts/settings.js's SettingsStore, over expo-async-storage
  accountId,         // D114's account id (the tuple's `id`)
});

await voice.read();                                   // { ok, settings, consent } or a refusal
await voice.set('stt', 'remote');                     // one direction; the other is left alone
await voice.recordConsent('deepgram', Date.now());    // one entry per named provider, dated
await voice.revokeConsent('deepgram');                // a name it does not hold is already revoked

const badge = await voice.indicator({
  direction: 'stt',                                   // required: which of the two switches
  status,                                             // the hop's last project-status answer
  lastCallInDirection,                                // THIS direction's last call
});
// { direction, state, text, inFlight, engine, provider, reason, send,
//   persistent: true, dismissible: false, refusal }
```

## What it stores, and where

One key per account, under the settings namespace:

| Key | Value |
| --- | --- |
| `fugaro.settings.voice-<accountId>` | `{ settings: { stt, tts }, consent: [{ provider, at }] }` |

`voice-<id>` is **one segment** on purpose. `src/accounts/settings.js` refuses a
settings key whose `.`-separated segment names a credential, and the
`account.<id>` branch exists because that fence once refused an installation the
owner had named `token`; a key composed as `voice.<id>` would put the owner's own
word back in front of the list, so an installation called `token` could not turn
its switch on. Two accounts' keys cannot collide: the prefix is fixed and an
account id carries no `.` (`src/accounts/account.js`).

**Never the database.** The whole write set of this module is that one key: it
calls `SettingsStore.get`/`set` and nothing else, and
`src/mailbox/write-path.test.js` still finds the mailbox client as the app's only
writer of the database (D113).

**Nothing secret, and no third item.** The record is a name, a date and one of
two words; the store's own value fence judges it, and `SECRET_ITEMS` is still
`['idToken', 'macKey']` (D111, Global Constraint 4). **No provider key is named
anywhere in this directory**: the design's point 5 puts the provider credential
in the hop's Secret Manager, and the app never holds it.

## The table is `shared/voice-switch`'s, and the app does not restate it

`renderIndicator` hands the shared function `direction`, `settings`, `consent`,
`status` and `lastCallInDirection` **unchanged** and renders its answer. Every
name, date, bound and branch is the shared module's: `consent.js` judges a
provider name by *asking* the shared function about a one-entry list, so the app
and the egress path cannot hold two readings of what a name is. What
`indicator.js` adds is the three things a screen needs and a pure rule has no
opinion about:

| Added | Why |
| --- | --- |
| `send` | the one field P5.3.7 reads before it puts a byte on the wire: true only for a live, admitted, consented route with **no call already in flight** |
| `persistent: true`, `dismissible: false` | the design §4.2: the indicator is not a toast and cannot be dismissed |
| the fail-closed badge | a refusal is not an answer, and a badge is still owed |

## The egress path

The hop client and the rules that gate it (task **P5.3.7**; §4.4, §4.5 and §5).
The app builds the client once and the egress over it, asks the egress for a
turn, and gets **one record** back — remote when the turn went, local when it did
not, with the reason in it either way.

```js
import { createVoiceHop, createVoiceEgress } from './index.js';

const hop = createVoiceHop({
  baseUrl,           // the hop's address; the app configures it, and no host ships here
  hosts,             // shared/url's allow-list, which the owner names the host in
  fetch,             // the platform's transport
  token: () => idToken(),   // D111's item (1), read per call and never stored
});

const egress = createVoiceEgress({
  hop,
  voiceSwitch: voice,          // the switch above: the badge is read from the record
  projects,                    // the slugs this account may speak of
  secrets,                     // D118: what the redactor must not let out
  onCall,                      // told in flight, then done or failed, so a screen can change back
});

await egress.transcribe({ audio, contentType, project, route, now, state, status, localFailed });
await egress.speak({ text, project, route, now, state, status, localFailed });
await egress.decide({ direction, route, project, now, state, status });   // the gate, without the send
```

**No credential of its own, and no host of its own.** The request is
`Authorization: Bearer <Firebase ID token>`, the audio or the text as the body,
the project in `X-Fugaro-Project`, `credentials: 'omit'`, `redirect: 'error'` and
`cache: 'no-store'` — and nothing else. The ID token is read per call from the
port and goes into one header; a token that cannot be presented is not sent.
**The URL is checked whole against `shared/url` on every call**, and
`shared/url`'s allow-list ships **empty**: until the owner names the hop's host
(the plan's P5.3, item 2), every call fails closed with `allow-list-empty` and is
served locally. That is why this task left `shared/url/index.js` alone.

**The ten rules of §5**, each with a test in `egress.test.js` and each a guard in
the mutation proof: nothing while a confirmation is pending (a rule about a
**state**, `pending(state, now)`, never about a phrase); a destructive route
served entirely locally, with the read-back rebuilt by `confirm`'s `readBack` and
the confirmation transcribed locally; the residual the design states honestly (an
utterance that turns out destructive may have been transcribed remotely before
anything knew it was); the redactor **substitutes and never drops** on the text
path and is **not** claimed for the audio path; no command intended, nothing
goes; only this account's own project goes, and `belong` is refused **by name**
before the configured list is consulted; the hop is the only transport; no
credential but the ID token; and **nothing is queued, spooled, filed or
buffered** — a failure is terminal, answered locally, with no retry and no second
provider.

## The guard this task exists for

**A remote call is not attempted without a recorded consent for that provider
name** (the design §4.3: "the app refuses only what it can know by itself — a
provider for which it holds no recorded consent"). The consent a badge is built
from is the **record**, read from the store on every question: `indicator` takes
`direction`, `status` and `lastCallInDirection`, and a context that also carries
`consent` or `settings` is not consulted. A caller cannot assert an agreement
the device does not hold into `send: true`. Sending is P5.3.7's; the guard that
will stop it is here, and `switch.test.js` is where it is proved.

## The failure rules

- An **unset** setting is `local` on both directions. `absent` is the default
  record; every **other** refusal from the store is a refusal, so "the owner
  never chose" is never confused with "the app could not find out".
- A record that cannot be read is a refusal on `read`, `set`, `recordConsent`
  and `revokeConsent`, and on `indicator` it is the **fail-closed badge**:
  `local — checking`, engine `local`, `send` false, with the refusal in
  `refusal`. The app may not owe the owner silence.
- A stored value the shared rule cannot read — an engine that is not one of the
  two, a consent with no date, a field the record does not have — is refused
  rather than half-read. The field set **is** the version.
- **The three edits take turns, per account.** Each of them reads the record,
  waits for the store and writes the whole record back, so two at once would
  each write from the same reading — and the one holding the consent the other
  removed would put it back, with nothing on screen to say so. They run on one
  queue per account (`serialize` in `switch.js`, keyed by the store and the
  account id, so two switches for one account take turns too); reads
  (`read`, `indicator`) do not queue, because they change nothing.
- A **call in flight** is what the badge shows, and `send` stays false: the
  shared module's `inFlight` is what tells a caller not to start a second call.
  A **failed** call falls to local with the call's own reason (§4.5).

## Honest limits, named

- **The date's unit is not checked.** The shared reader bounds `at` to an
  integer in its epoch range, and it cannot tell a millisecond count from a
  second one; a sheet that passed `Date.now() / 1000` would record a 1970 date
  and no test here would fail. What the app records is `Date.now()`, and
  `consent.test.js` pins the limit rather than leaving it to be found.
- **Consent is per name, not per direction.** `shared/voice-switch` says so
  itself: a name agreed to for speech in is a name agreed to for speech out.
- **The hop's answer is passed through unread.** The client hands the provider's
  own bytes back with its content type; it reads the hop's **own**
  `StatusAnswer`, because that shape is this app's contract, and it does not
  parse a provider's answer shape. A caller that needs a transcript out of an
  STT answer needs that provider's shape, which is not in this tree.
- **No host is named here.** The client takes its address as configuration and
  checks it against `shared/url`'s allow-list, which is empty until the owner
  names the host (the plan's P5.3, item 2). `shared/url/index.js` was not edited
  by this task, and a run of the app today answers **locally** on every turn.
- **The text path is redacted; the audio path is not**, and cannot be: §5 rule 4
  reaches text, which is the TTS and summariser path. Nothing here claims
  otherwise, and `egress.test.js` pins the audio path as unredacted rather than
  leaving the asymmetry to be discovered.
- **The screen holds the badge; it does not build it.** `src/voice/screen.js`
  carries what the switch answered (§4.2) and tags each transcript line with the
  engine the app names, and it makes no decision about where an utterance goes.

## The mutation proof

```sh
mobile/src/voice-switch/mutation-proof.sh    # 57 guards, one planted bug each
```

Every mutation disables exactly one guard and must make that guard's own suite
fail: `switch.js` with `switch.test.js` for the consent, per-direction and
one-at-a-time guards, `consent.js` with `consent.test.js` (one entry per name, the date the
caller gave, the list judged before it is edited, and the name that is not a
name), `settings.js` with `settings.test.js` (the default, the key that cannot
be composed, the field set, and the round-trip), `indicator.js` with
`indicator.test.js` (`send`, the two words about the badge, and the fail-closed
draw), `hop.js` with `hop.test.js` (the allow-list, the token, the ambient
credential, the redirect, the bound, the engine name, an unknown reason, the
status-failure table and the status answer's shape -- the badge's input and
nothing beside it) and `egress.js` with `egress.test.js` (§5's rules, one
mutation each), plus `src/voice/screen.js` with `screen.test.js` for the badge
that is in the snapshot before the switch answers and the engine tag read when
the utterance arrives. It finds a working node rather than assuming one (the base carries none,
`docs/base-image.md`), proves the pristine suite green and a planted
always-succeeding refusal killed before any real mutation is trusted, and
restores each file with `git checkout`.

## The files outside this directory these tasks touched

| File | Change | Task |
| --- | --- | --- |
| `src/boundary.test.js` | `src/voice-switch/index.js`, `hop.js` and `egress.js` added to the walk's **list**; two rules added (the network is opened in `hop.js` only, and the egress half keeps no timer, no file and no store). No rule and no pattern was weakened. | P5.3.6, P5.3.7 |
| `src/voice/screen.js` | the indicator's place (§4.2) and the engine tag on each transcript line. It picks no engine and makes no routing decision. | P5.3.7 |
| `shared/url/index.js` | **not touched.** Its allow-list is still the empty list. | — |

`src/accounts/settings.js` and `src/accounts/secret.js` were **not** touched: the
store, its fences and `SECRET_ITEMS` are as they were, which
`settings.test.js` asserts rather than assumes.
