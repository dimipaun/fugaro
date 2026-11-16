<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->
<!-- See mobile/LICENSE.md for the terms. -->
# `voice-switch/` — the app's two voice settings, the consent record and the indicator

The phone's half of where an utterance may go (mobile plan, task **P5.3.6**,
decisions **D111**, **D114**, **D123**, **D127**; the design is
`docs/mobile/design/voice-evaluation-switch.md` §4.1–§4.3). Two settings per
account — `stt` and `tts`, each `local` by default — and the list of provider
names the user has agreed to; both are **device-local** and go through the app's
non-secret settings store. The badge is rendered by
`shared/voice-switch`'s one pure function, whose table this module does not
restate.

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
- **`send: true` is not a send.** Nothing in this app acts on it yet — the
  egress path is P5.3.7 — and a caller that read it as permission today would be
  reading a field the app does not consult.
- **The badge is not drawn here.** `src/voice/screen.js` renders it, and this
  task deliberately did not touch that file (P5.3.7 wires the hop and the
  screen together).

## The mutation proof

```sh
mobile/src/voice-switch/mutation-proof.sh    # 25 guards, one planted bug each
```

Every mutation disables exactly one guard and must make that guard's own suite
fail: `switch.js` with `switch.test.js` for the consent and per-direction
guards, `consent.js` with `consent.test.js` (one entry per name, the date the
caller gave, the list judged before it is edited, and the name that is not a
name), `settings.js` with `settings.test.js` (the default, the key that cannot
be composed, the field set, and the round-trip) and `indicator.js` with
`indicator.test.js` (`send`, the two words about the badge, and the fail-closed
draw). It finds a working node rather than assuming one (the base carries none,
`docs/base-image.md`), proves the pristine suite green and a planted
always-succeeding refusal killed before any real mutation is trusted, and
restores each file with `git checkout`.

## The one file outside this directory this task touched

| File | Change |
| --- | --- |
| `src/boundary.test.js` | `src/voice-switch/index.js` added to the walk's **list** (the new directory, named on purpose). No rule and no pattern was weakened. |

`src/accounts/settings.js` and `src/accounts/secret.js` were **not** touched: the
store, its fences and `SECRET_ITEMS` are as they were, which
`settings.test.js` asserts rather than assumes. `src/voice/screen.js`,
`shared/url/index.js` and everything on the egress path were not touched either:
that is P5.3.7, and this task stops at the answer about where an utterance
*would* go.
