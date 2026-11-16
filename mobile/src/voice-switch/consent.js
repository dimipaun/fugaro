// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The consent record: one entry per named provider (mobile plan, Task P5.3.6;
// D123, D127; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.3).
//
// **A consent is for a NAME, and it is dated.** The design records the user's
// agreement per provider name (§4.3), and `shared/voice-switch` refuses a
// record with no date because "a consent recorded without a date is a claim
// with no date, which this module refuses rather than treats as a flag". This
// file holds the list the app has agreed to; the switch (`settings.js`) stores
// it and the badge (`indicator.js`) reads it, and neither of them judges a
// name.
//
// **Every name, date and bound is judged by asking `shared/voice-switch`,
// never by restating it.** The name pattern, the 64-name cap, the epoch bound
// on `at` and "one name carries one consent" are that module's rules, and they
// are the rules that will judge this list again at egress time. A second
// pattern here would be a second answer to the same question -- the app could
// accept a name the egress path then refuses, or refuse one the hop admits --
// and this module's whole job is to hold exactly one reading of "where may this
// utterance go". So `judge` below builds the one input that isolates the
// consent half and hands it to the shared function: a refusal from there is a
// refusal here, with the shared module's own code, and nothing in this file
// spells a provider name's shape.
//
// **A refusal carries no list.** A caller handed `{ ok: false }` has changed
// nothing, and the sheet it came from shows the refusal rather than a record
// it did not write.

import { DIRECTION, MAX_CONSENT, indicator } from '../../../shared/voice-switch/index.js';

/** The fields a consent record has, exactly -- `shared/voice-switch`'s own two. */
export const CONSENT_FIELDS = Object.freeze(['provider', 'at']);

/**
 * The most names one account may have consented to. Re-exported from the shared
 * module rather than declared here: the sheet that stops offering names past the
 * cap and the reader that refuses a list past it must be the same number.
 */
export const CONSENT_MAX = MAX_CONSENT;

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * The list a caller handed in, as a list.
 *
 * An explicit `null` is the shared module's own "no consent" -- it reads the
 * same value as an absent one, and a record that spells its empty list that way
 * is a record it accepts -- so it is edited here as the empty list rather than
 * refused. `judgeConsent` above has already refused everything that is neither
 * a list nor that one value.
 */
function asList(consent) {
  return consent ?? [];
}

/** A frozen list, for the consent records a caller is handed. */
function freeze(list) {
  return Object.freeze(list.map((entry) => Object.freeze({ provider: entry.provider, at: entry.at })));
}

/**
 * Judge a consent list by the one rule that will judge it at egress time.
 *
 * The probe reads a `direction` and nothing else, so the answer is decided by
 * the consent half alone: the shared function reads and validates its `consent`
 * input before it decides anything, and a list it cannot read comes back as a
 * refusal rather than as a decision. `DIRECTION.STT` names no side of this
 * question -- consent is per name and not per direction (`shared/voice-switch`'s
 * own honest limit) -- it is only the input the reader requires.
 *
 * Exported because `settings.js` validates a whole stored record through it, and
 * because a test can then assert the refusal code a bad name produces without
 * writing that name's pattern down anywhere.
 */
export function judgeConsent(consent) {
  const probe = indicator({ direction: DIRECTION.STT, consent });
  return probe.ok ? null : refuse(probe.code, probe.field);
}

/**
 * The date the probe below carries. It judges a NAME, and the shared module
 * judges a name only inside a dated record, so the probe supplies a date of its
 * own: one epoch millisecond that is a date to the shared reader (it is not `0`,
 * which that reader refuses as "no date") and that is written nowhere the app
 * stores. It never reaches a record -- only `withConsent`'s own `at` does.
 */
const PROBE_AT = 1;

/**
 * Judge one provider name by the rule that judges the list, for the edit that
 * carries no date of its own.
 *
 * A name that is not a name is refused **here** exactly as it is refused while
 * recording, so the sheet cannot take a name back that it would never have
 * agreed to: an empty string, a URL-shaped entry or something that is not a
 * string at all is a caller bug in both directions, and answering `ok` with an
 * unchanged list would report an edit that did not happen.
 */
function judgeName(provider) {
  const probe = indicator({ direction: DIRECTION.STT, consent: [{ provider, at: PROBE_AT }] });
  return probe.ok ? null : refuse(probe.code, probe.field);
}

/** Does this list record an agreement for `name`? */
export function hasConsent(consent, provider) {
  return Array.isArray(consent) && consent.some((entry) => entry !== null && typeof entry === 'object' && entry.provider === provider);
}

/**
 * The list with `provider` agreed to at `at`.
 *
 * **Re-recording a name re-dates it and keeps one entry**, because the design's
 * unit is "one entry per named provider": the sheet's second agreement for the
 * same name is a later date for the same fact, and appending it would leave a
 * list two readers could read two ways -- the shared module refuses exactly that
 * (`duplicate-consent`), so the edit is the replacement rather than the append.
 *
 * The list handed in is judged **before** it is edited: a caller that passes
 * something this app never recorded gets the refusal instead of a new record
 * built on top of it.
 */
export function withConsent(consent, provider, at) {
  const bad = judgeConsent(consent);
  if (bad !== null) return bad;
  const kept = asList(consent).filter((entry) => entry.provider !== provider);
  const next = [...kept, { provider, at }];
  const refused = judgeConsent(next);
  if (refused !== null) return refused;
  return Object.freeze({ ok: true, consent: freeze(next) });
}

/**
 * The list with `provider` no longer agreed to.
 *
 * A name the list does not hold is **not** an error: the caller asked for a
 * state rather than for a change, and the state is already the one it asked
 * for -- the same rule `SettingsStore.delete` states for an absent key. A name
 * that is not a name is an error, and `judgeName` above is where it is caught.
 */
export function withoutConsent(consent, provider) {
  const bad = judgeConsent(consent);
  if (bad !== null) return bad;
  const notAName = judgeName(provider);
  if (notAName !== null) return notAName;
  return Object.freeze({ ok: true, consent: freeze(asList(consent).filter((entry) => entry.provider !== provider)) });
}
