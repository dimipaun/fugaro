// The voice switch and its indicator's tests (mobile plan, Task P5.3.4).
//
// SECURITY-SENSITIVE. The plan's verification list is the spine of this file, and
// each of its items is a test named after it:
//
//   * the state table walks the four inputs the app actually holds
//   * an unset switch is local, on both directions
//   * before the first project-status answer the state is `local — checking`,
//     and never a guess
//   * `remote (<provider name>)` is reachable only with a recorded consent for
//     that name
//   * `blocked by project policy` comes from the hop's answer, and never from a
//     policy the app cannot read
//   * every failure input -- an expired token, a revoked device, an unreachable
//     hop, a failed query, a provider error -- lands on local, which is the one
//     direction the whole table fails in
//   * the exported surface is pinned as an exact set, and a new export is a
//     decision that goes into that test
//
// The first mutation in mutation-proof.sh is the guard this task turns on: a
// badge that trusts the switch while a remote call is already in flight, so that
// the indicator can be made to say `local` while audio is leaving the device. The
// tests it turns red are "a switch turned off mid-call does not make the badge
// claim local" and "the badge and the egress decision answer two questions".
//
// Nothing here reads a clock, a network or a device, and nothing here waits on
// anything: the module is a pure function of the four inputs, so every case below
// is one call whose whole result is compared field for field. No test value here
// is a secret, and no literal is secret-shaped -- the provider names are the
// design's register names (design section 7, lowercase) and the timestamps are
// fixed constants from 2023, not "now".

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import * as surface from './index.js';
import {
  CALL_FAILURE,
  CALL_STATE,
  DIRECTION,
  ENGINE,
  MAX_CONSENT,
  MAX_PROVIDERS,
  POLICY_REASON,
  PROVIDER_MAX,
  REASON,
  REFUSAL,
  STATE,
  STATUS_FAILURE,
  indicator,
} from './index.js';
// The time bound the module reads a consent's date with, from the module it
// reads it from: the probe past the bound has to be past *that* bound, or a
// widened one would leave the test asserting yesterday's number.
import { MAX_TIMESTAMP } from '../lease/index.js';

const vectors = JSON.parse(readFileSync(new URL('./vectors.json', import.meta.url), 'utf8'));

/** A fixed time, so nothing here depends on the day the suite runs. */
const AT = 1_700_000_000_000;

/** A consent record, in the app's own stored shape. */
const consentOf = (names) => names.map((provider, index) => ({ provider, at: AT + index }));

/** An answer the hop gave: the admitted names, the admitted directions, a reason. */
const admitted = (names, direction = { stt: true, tts: true }, reason = null) => ({
  ok: true,
  providers: names,
  direction,
  reason,
});

/** An answer the hop could not give. */
const failed = (code) => ({ ok: false, code });

/** The last call's route, in the shape the app records it. */
const route = (direction, engine, provider, state, reason = null) => ({
  direction,
  engine,
  provider,
  state,
  reason,
});

/** The input that admits remote in both directions, for the tests that vary one thing. */
const PERMISSIVE = Object.freeze({
  allowed: admitted(['deepgram']),
  consent: consentOf(['deepgram']),
});

// ---------------------------------------------------------------------------
// The exported surface
// ---------------------------------------------------------------------------

test('the exported surface is an exact set: a new export is a decision', () => {
  assert.deepEqual(Object.keys(surface).sort(), [
    'CALL_FAILURE',
    'CALL_STATE',
    'DIRECTION',
    'ENGINE',
    'MAX_CONSENT',
    'MAX_PROVIDERS',
    'POLICY_REASON',
    'PROVIDER_MAX',
    'REASON',
    'REFUSAL',
    'STATE',
    'STATUS_FAILURE',
    'indicator',
  ]);
});

test('every vocabulary is closed and frozen, with its members pinned here', () => {
  const vocabularies = [
    [DIRECTION, ['stt', 'tts']],
    [ENGINE, ['local', 'remote']],
    [STATE, ['local', 'checking', 'blocked', 'remote']],
    [CALL_STATE, ['in-flight', 'done', 'failed']],
    [POLICY_REASON, ['force-local', 'no-admission', 'no-register-row']],
    [STATUS_FAILURE, ['expired-token', 'revoked-device', 'unreachable', 'query-failed']],
    [
      CALL_FAILURE,
      ['expired-token', 'revoked-device', 'unreachable', 'provider-error', 'local-error'],
    ],
  ];
  for (const [vocabulary, members] of vocabularies) {
    assert.ok(Object.isFrozen(vocabulary));
    assert.deepEqual(Object.values(vocabulary).sort(), [...members].sort());
    for (const member of Object.values(vocabulary)) {
      assert.equal(typeof member, 'string');
    }
  }
  // REASON is the union of the three code sets and this module's own three, and
  // nothing else: a new code has to be added to one of them on purpose.
  assert.deepEqual(
    Object.values(REASON).sort(),
    [
      ...new Set([
        'switch-local',
        'no-consent',
        'no-answer',
        ...Object.values(POLICY_REASON),
        ...Object.values(STATUS_FAILURE),
        ...Object.values(CALL_FAILURE),
      ]),
    ].sort(),
  );
});

test('the module reads nothing of its own: no clock, no file, no network, no random source', () => {
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');
  // The import list is one line of the module, and it may name nothing but the
  // timestamp bound the whole package reads a time with. A module that reached
  // for a clock or a client would have to name it here.
  const imports = source.match(/^import .*$/gm) ?? [];
  assert.deepEqual(imports, ["import { MAX_TIMESTAMP } from '../lease/index.js';"]);
  for (const forbidden of ['Date.now', 'new Date', 'Math.random', 'fetch(', 'process.', 'require(']) {
    assert.ok(!source.includes(forbidden), `index.js must not use ${forbidden}`);
  }
});

// ---------------------------------------------------------------------------
// The state table, as data, replayed verbatim
// ---------------------------------------------------------------------------

test('the vectors file replays exactly: every state and every refusal', () => {
  let accepts = 0;
  let refusals = 0;
  for (const vector of vectors.cases) {
    const result = indicator(vector.input);
    assert.deepEqual(result, vector.expect, `vector: ${vector.name}`);
    if (vector.expect.ok) accepts += 1;
    else refusals += 1;
  }
  // The counts are pinned so that a case deleted from the table is a failure
  // rather than a smaller table nobody notices.
  assert.equal(vectors.cases.length, 36);
  assert.equal(accepts, 29);
  assert.equal(refusals, 7);
  assert.equal(vectors.cases.length, accepts + refusals);
});

test('the vectors file declares the states and the fail-closed rule it is a table of', () => {
  assert.deepEqual(vectors.states, Object.values(STATE));
  assert.deepEqual(vectors.directions, Object.values(DIRECTION));
  assert.deepEqual(vectors.engines, Object.values(ENGINE));
  assert.equal(vectors.task, 'P5.3.4');
});

// ---------------------------------------------------------------------------
// The plan's verification list
// ---------------------------------------------------------------------------

test('the state table walks the four inputs the app actually holds', () => {
  // Every combination of the four inputs, in both directions. This is the table
  // the plan asks for: the two settings, the recorded consent, the hop's last
  // project-status answer and the last call's route -- and nothing else, which
  // is why the walk can be exhaustive and why every answer below is checked
  // against the invariants rather than only against itself.
  const settings = [
    undefined,
    null,
    {},
    { stt: ENGINE.REMOTE },
    { tts: ENGINE.REMOTE },
    { stt: ENGINE.REMOTE, tts: ENGINE.REMOTE },
  ];
  const consent = [undefined, [], consentOf(['deepgram']), consentOf(['elevenlabs'])];
  const status = [
    undefined,
    admitted(['deepgram']),
    admitted(['deepgram'], { stt: true, tts: false }),
    admitted([], { stt: false, tts: false }, POLICY_REASON.FORCE_LOCAL),
    admitted(['deepgram'], { stt: false, tts: false }, POLICY_REASON.NO_REGISTER_ROW),
    { ok: true },
    failed(STATUS_FAILURE.QUERY_FAILED),
    failed(STATUS_FAILURE.EXPIRED_TOKEN),
    failed(STATUS_FAILURE.REVOKED_DEVICE),
    failed(STATUS_FAILURE.UNREACHABLE),
  ];

  let probes = 0;
  const reasons = new Set();
  const states = new Set();
  for (const direction of Object.values(DIRECTION)) {
    const other = direction === DIRECTION.STT ? DIRECTION.TTS : DIRECTION.STT;
    const calls = [
      undefined,
      route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT),
      route(direction, ENGINE.LOCAL, null, CALL_STATE.IN_FLIGHT),
      route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.FAILED),
      route(direction, ENGINE.LOCAL, null, CALL_STATE.FAILED),
      route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.DONE),
      route(other, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT),
    ];
    for (const settingsCase of settings) {
      for (const consentCase of consent) {
        for (const statusCase of status) {
          for (const callCase of calls) {
            const input = { direction };
            if (settingsCase !== undefined) input.settings = settingsCase;
            if (consentCase !== undefined) input.consent = consentCase;
            if (statusCase !== undefined) input.status = statusCase;
            if (callCase !== undefined) input.lastCallInDirection = callCase;
            const result = indicator(input);
            probes += 1;
            assert.equal(result.ok, true, `probe ${JSON.stringify(input)}`);
            assert.ok(Object.values(STATE).includes(result.state));
            assert.ok(result.reason === null || Object.values(REASON).includes(result.reason));
            assert.ok(Object.values(ENGINE).includes(result.engine));
            assert.equal(typeof result.text, 'string');
            assert.equal(result.direction, direction);
            assert.equal(typeof result.inFlight, 'boolean');
            // The egress triple's invariant, over every probe of the walk.
            assert.equal(result.engine === ENGINE.REMOTE, result.provider !== null);
            assert.equal(result.engine === ENGINE.REMOTE, result.reason === null);
            reasons.add(result.reason);
            states.add(result.state);
            // The input is not mutated by reading it, which the whole app
            // depends on: it holds one settings object and one call record.
            assert.deepEqual(result, indicator(structuredClone(input)));
          }
        }
      }
    }
  }
  // The walk's size is pinned exactly, not loosely: the README states the number,
  // and a factor quietly dropped here (a call route, a consent state) would
  // otherwise shrink the walk with nothing to say so.
  assert.equal(probes, 3360, `the walk is ${probes} probes`);
  assert.deepEqual([...states].sort(), Object.values(STATE).sort());
  for (const reason of [
    REASON.SWITCH_LOCAL,
    REASON.NO_CONSENT,
    REASON.NO_ANSWER,
    ...Object.values(POLICY_REASON),
    ...Object.values(STATUS_FAILURE),
    ...Object.values(CALL_FAILURE),
  ]) {
    assert.ok(reasons.has(reason), `${reason} is reached by the walk`);
  }
});

test('an unset switch is local, on both directions', () => {
  for (const direction of Object.values(DIRECTION)) {
    for (const settings of [undefined, null, {}, { [direction]: undefined }]) {
      const input = {
        direction,
        consent: PERMISSIVE.consent,
        status: PERMISSIVE.allowed,
      };
      if (settings !== undefined) input.settings = settings;
      const result = indicator(input);
      assert.deepEqual(result, {
        ok: true,
        direction,
        engine: ENGINE.LOCAL,
        provider: null,
        state: STATE.LOCAL,
        inFlight: false,
        reason: REASON.SWITCH_LOCAL,
        text: 'local',
      });
    }
  }
});

test('before the first project-status answer the state is local — checking, and never a guess', () => {
  for (const direction of Object.values(DIRECTION)) {
    for (const status of [undefined, null, failed(STATUS_FAILURE.QUERY_FAILED)]) {
      const input = { direction, settings: { [direction]: ENGINE.REMOTE }, consent: PERMISSIVE.consent };
      if (status !== undefined) input.status = status;
      const result = indicator(input);
      assert.equal(result.state, STATE.CHECKING, `direction ${direction}`);
      assert.equal(result.text, 'local — checking');
      assert.equal(result.engine, ENGINE.LOCAL);
      // Never a guess, in either direction: not local (the project may forbid
      // it), and certainly not remote (it may not admit it).
      assert.notEqual(result.state, STATE.REMOTE);
      assert.notEqual(result.state, STATE.BLOCKED);
    }
  }
  // And no absence of an answer is ever remote, over the whole table.
  for (const direction of Object.values(DIRECTION)) {
    for (const settings of [{ [direction]: ENGINE.REMOTE }, {}]) {
      for (const consent of [[], PERMISSIVE.consent, consentOf(['elevenlabs'])]) {
        const result = indicator({ direction, settings, consent });
        assert.notEqual(result.engine, ENGINE.REMOTE, 'no answer is never remote');
        assert.notEqual(result.state, STATE.REMOTE);
        assert.notEqual(result.state, STATE.BLOCKED);
        // `checking` is reached only when the device's own half already asks for
        // remote: with the switch off, or with no consent, local is the device's
        // answer and there is nothing to wait for.
        const asksRemote = settings[direction] === ENGINE.REMOTE && consent.length > 0;
        assert.equal(result.state, asksRemote ? STATE.CHECKING : STATE.LOCAL);
      }
    }
  }
});

test('remote (<provider name>) is reachable only with a recorded consent for that name', () => {
  for (const direction of Object.values(DIRECTION)) {
    for (const switchValue of [undefined, ENGINE.LOCAL, ENGINE.REMOTE]) {
      for (const consent of [[], consentOf(['deepgram']), consentOf(['elevenlabs'])]) {
        for (const allowed of [['deepgram'], ['elevenlabs'], ['deepgram', 'elevenlabs'], []]) {
          const input = {
            direction,
            settings: switchValue === undefined ? {} : { [direction]: switchValue },
            consent,
            status: admitted(allowed),
          };
          const result = indicator(input);
          if (result.state !== STATE.REMOTE) continue;
          // The three conditions, and none of them optional: the switch is on,
          // the project admits the direction, and the device recorded a consent
          // for **that** name.
          assert.equal(switchValue, ENGINE.REMOTE);
          assert.ok(allowed.includes(result.provider));
          assert.ok(consent.some((entry) => entry.provider === result.provider));
          assert.equal(result.text, `remote (${result.provider})`);
        }
      }
    }
  }
});

test('blocked by project policy comes from the hop\'s answer, never from a policy the app cannot read', () => {
  for (const direction of Object.values(DIRECTION)) {
    for (const status of [
      admitted([]),
      // An answer that admits the direction and names no provider has nothing to
      // call, so it is a refusal too: remote is a name and a direction together.
      admitted([], { stt: true, tts: true }),
      admitted([], { stt: false, tts: false }, POLICY_REASON.FORCE_LOCAL),
      { ok: true },
    ]) {
      const result = indicator({
        direction,
        settings: { [direction]: ENGINE.REMOTE },
        consent: PERMISSIVE.consent,
        status,
      });
      assert.equal(result.state, STATE.BLOCKED);
      assert.equal(result.text, 'blocked by project policy');
      assert.equal(result.engine, ENGINE.LOCAL);
    }
    // The reverse: with no answer, or an answer that could not be given, the
    // badge is never `blocked`. There is no other source of it.
    for (const status of [undefined, null, failed(STATUS_FAILURE.QUERY_FAILED), failed(STATUS_FAILURE.EXPIRED_TOKEN), failed(STATUS_FAILURE.REVOKED_DEVICE), failed(STATUS_FAILURE.UNREACHABLE)]) {
      const input = { direction, settings: { [direction]: ENGINE.REMOTE }, consent: PERMISSIVE.consent };
      if (status !== undefined) input.status = status;
      assert.notEqual(indicator(input).state, STATE.BLOCKED);
    }
  }
  // And a project that forces local is reported as blocked only while the device
  // asks for remote: with the switch off, local is the device's own answer.
  const off = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.LOCAL },
    consent: PERMISSIVE.consent,
    status: admitted([], { stt: false, tts: false }, POLICY_REASON.FORCE_LOCAL),
  });
  assert.equal(off.state, STATE.LOCAL);
  assert.equal(off.reason, REASON.SWITCH_LOCAL);
});

test('an answer that names no direction admits neither: a name alone is no admission', () => {
  // §4.3's default deny, at the one place it is reachable: an answer that carries
  // admitted names and no `direction` map. The device's half is permissive here --
  // the switch asks for remote and the consent names the provider the answer
  // admits -- so the absent direction map is the only thing refusing, and it must
  // refuse. A default of "admitted" would answer `remote (deepgram)` on the
  // strength of a policy the app never read; mutation-proof.sh turns this
  // default and this test is what kills it.
  for (const direction of Object.values(DIRECTION)) {
    const result = indicator({
      direction,
      settings: { [direction]: ENGINE.REMOTE },
      consent: PERMISSIVE.consent,
      status: { ok: true, providers: ['deepgram'] },
    });
    assert.equal(result.state, STATE.BLOCKED, direction);
    assert.equal(result.reason, POLICY_REASON.NO_ADMISSION, direction);
    assert.equal(result.text, 'blocked by project policy', direction);
    assert.equal(result.engine, ENGINE.LOCAL, direction);
    assert.equal(result.provider, null, direction);
  }
});

test('an answer that says the project forces local is blocked, whatever its lists say', () => {
  // `force-local` is the policy node's own switch (§4.3), so it outranks the two
  // lists beside it: an answer that lists this direction and a name the device
  // consented to, and then says the project forces local, contradicts itself, and
  // the reading that cannot send audio is the one the operator's switch states.
  // Read only where the lists refuse, that answer would be served remote.
  for (const direction of Object.values(DIRECTION)) {
    const result = indicator({
      direction,
      settings: { [direction]: ENGINE.REMOTE },
      consent: PERMISSIVE.consent,
      status: {
        ok: true,
        providers: ['deepgram'],
        direction: { stt: true, tts: true },
        reason: POLICY_REASON.FORCE_LOCAL,
      },
    });
    assert.equal(result.state, STATE.BLOCKED, direction);
    assert.equal(result.reason, POLICY_REASON.FORCE_LOCAL, direction);
    assert.equal(result.engine, ENGINE.LOCAL, direction);
    assert.equal(result.provider, null, direction);
    assert.equal(result.text, 'blocked by project policy', direction);
  }
});

test('every failure input lands on local, which is the one direction the whole table fails in', () => {
  const failures = [
    ['an expired token', failed(STATUS_FAILURE.EXPIRED_TOKEN)],
    ['a revoked device', failed(STATUS_FAILURE.REVOKED_DEVICE)],
    ['an unreachable hop', failed(STATUS_FAILURE.UNREACHABLE)],
    ['a failed query', failed(STATUS_FAILURE.QUERY_FAILED)],
    ['no answer at all', undefined],
    ['a project that forbids remote', admitted([], { stt: false, tts: false }, POLICY_REASON.FORCE_LOCAL)],
    ['a provider with no register row', admitted(['deepgram'], { stt: false, tts: false }, POLICY_REASON.NO_REGISTER_ROW)],
  ];
  for (const direction of Object.values(DIRECTION)) {
    for (const [name, status] of failures) {
      const input = { direction, settings: { [direction]: ENGINE.REMOTE }, consent: PERMISSIVE.consent };
      if (status !== undefined) input.status = status;
      const result = indicator(input);
      assert.equal(result.engine, ENGINE.LOCAL, `${name} must not be served remotely`);
      assert.equal(result.provider, null, name);
    }
    // The device's own failure halves.
    for (const [name, extra] of [
      ['no consent recorded', { consent: [] }],
      ['a consent for another name', { consent: consentOf(['elevenlabs']) }],
      ['a provider that failed', { consent: PERMISSIVE.consent, lastCallInDirection: route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.FAILED, CALL_FAILURE.PROVIDER_ERROR) }],
      ['the local engine itself failing', { settings: { [direction]: ENGINE.LOCAL }, lastCallInDirection: route(direction, ENGINE.LOCAL, null, CALL_STATE.FAILED) }],
    ]) {
      const result = indicator({ direction, settings: { [direction]: ENGINE.REMOTE }, status: PERMISSIVE.allowed, ...extra });
      assert.equal(result.engine, ENGINE.LOCAL, `${name} must not be served remotely`);
      assert.equal(result.provider, null, name);
    }
  }
});

// ---------------------------------------------------------------------------
// The two questions, and the one case where they differ
// ---------------------------------------------------------------------------

test('engine is remote exactly when provider is a name and reason is null', () => {
  const cases = [
    { direction: DIRECTION.STT },
    { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: PERMISSIVE.consent, status: PERMISSIVE.allowed },
    { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: PERMISSIVE.consent, status: failed(STATUS_FAILURE.UNREACHABLE) },
    { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: PERMISSIVE.consent, status: admitted([], { stt: false, tts: false }, POLICY_REASON.FORCE_LOCAL) },
    { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: PERMISSIVE.consent, status: PERMISSIVE.allowed, lastCallInDirection: route(DIRECTION.STT, ENGINE.LOCAL, null, CALL_STATE.IN_FLIGHT) },
    { direction: DIRECTION.STT, settings: { stt: ENGINE.LOCAL }, consent: PERMISSIVE.consent, status: PERMISSIVE.allowed, lastCallInDirection: route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT) },
    { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: PERMISSIVE.consent, status: PERMISSIVE.allowed, lastCallInDirection: route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.FAILED, CALL_FAILURE.PROVIDER_ERROR) },
  ];
  for (const input of cases) {
    const result = indicator(input);
    assert.equal(result.engine === ENGINE.REMOTE, result.provider !== null, JSON.stringify(input));
    assert.equal(result.engine === ENGINE.REMOTE, result.reason === null, JSON.stringify(input));
  }
});

test('a switch turned off mid-call does not make the badge claim local', () => {
  // The one case where the badge and the egress decision part: the call is still
  // running remotely, so the badge says so -- and the app is still not allowed to
  // start another one, so `engine` is local and `provider` names nothing.
  const result = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.LOCAL },
    consent: PERMISSIVE.consent,
    status: PERMISSIVE.allowed,
    lastCallInDirection: route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT),
  });
  assert.deepEqual(result, {
    ok: true,
    direction: DIRECTION.STT,
    engine: ENGINE.LOCAL,
    provider: null,
    state: STATE.REMOTE,
    inFlight: true,
    reason: REASON.SWITCH_LOCAL,
    text: 'remote (deepgram) — in flight',
  });
});

test('the badge and the egress decision answer two questions, and agree everywhere else', () => {
  for (const direction of Object.values(DIRECTION)) {
    const calls = [
      undefined,
      route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT),
      route(direction, ENGINE.LOCAL, null, CALL_STATE.IN_FLIGHT),
      route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.DONE),
      route(direction, ENGINE.REMOTE, 'deepgram', CALL_STATE.FAILED, CALL_FAILURE.PROVIDER_ERROR),
    ];
    for (const switchValue of [ENGINE.LOCAL, ENGINE.REMOTE]) {
      for (const status of [undefined, PERMISSIVE.allowed, failed(STATUS_FAILURE.UNREACHABLE)]) {
        const input = {
          direction,
          settings: { [direction]: switchValue },
          consent: PERMISSIVE.consent,
          status,
          lastCallInDirection: calls[1],
        };
        const result = indicator(input);
        // A call in flight is the badge's business and the only thing that can
        // make the two disagree.
        if (result.state !== STATE.REMOTE) continue;
        const egressRemote = result.engine === ENGINE.REMOTE;
        const decisionRemote = switchValue === ENGINE.REMOTE && result.reason !== REASON.SWITCH_LOCAL
          && status !== undefined && status.ok === true;
        assert.equal(egressRemote, decisionRemote, JSON.stringify(input));
      }
    }
  }
});

test('the route read is the asked direction\'s, and a call in the other direction leaves this one\'s badge alone', () => {
  // The field is `lastCallInDirection` and the badge is per direction, so the
  // caller passes the record for the direction it is asking about -- that is the
  // contract, and this test is its pin. It is what keeps the RED-STEP guard's
  // promise: a remote `stt` stream in flight is not hidden from the `stt` badge
  // by a later `tts` call, because the `tts` call is not what the `stt` badge is
  // told about; a caller that passed it would have read the field's name wrong.
  // A record naming the other direction is therefore read as no call here, and
  // the four inputs' decision stands untouched.
  const decision = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    consent: PERMISSIVE.consent,
    status: PERMISSIVE.allowed,
  });
  assert.equal(decision.state, STATE.REMOTE);
  for (const lastCallInDirection of [
    undefined,
    null,
    route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.DONE),
    route(DIRECTION.TTS, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT),
    route(DIRECTION.TTS, ENGINE.LOCAL, null, CALL_STATE.FAILED, CALL_FAILURE.LOCAL_ERROR),
  ]) {
    const result = indicator({
      direction: DIRECTION.STT,
      settings: { stt: ENGINE.REMOTE },
      consent: PERMISSIVE.consent,
      status: PERMISSIVE.allowed,
      lastCallInDirection,
    });
    assert.deepEqual(result, decision);
  }
});

test('a failed call falls back to local, with its own reason or the default for its engine', () => {
  const base = {
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    consent: PERMISSIVE.consent,
    status: PERMISSIVE.allowed,
  };
  assert.equal(
    indicator({ ...base, lastCallInDirection: route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.FAILED) }).reason,
    CALL_FAILURE.PROVIDER_ERROR,
  );
  assert.equal(
    indicator({ ...base, lastCallInDirection: route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.FAILED, CALL_FAILURE.UNREACHABLE) }).reason,
    CALL_FAILURE.UNREACHABLE,
  );
  assert.equal(
    indicator({
      direction: DIRECTION.TTS,
      settings: { tts: ENGINE.LOCAL },
      lastCallInDirection: route(DIRECTION.TTS, ENGINE.LOCAL, null, CALL_STATE.FAILED),
    }).reason,
    CALL_FAILURE.LOCAL_ERROR,
  );
});

// ---------------------------------------------------------------------------
// The refusals: one probe per code, all of them reached
// ---------------------------------------------------------------------------

/** The field paths this module chose. A refusal never names a caller's key. */
const FIELDS = [
  null,
  'input',
  'direction',
  'settings',
  'settings.stt',
  'settings.tts',
  'consent',
  'consent.provider',
  'consent.at',
  'status',
  'status.ok',
  'status.code',
  'status.providers',
  'status.direction',
  'status.direction.stt',
  'status.direction.tts',
  'status.reason',
  'lastCallInDirection',
  'lastCallInDirection.direction',
  'lastCallInDirection.engine',
  'lastCallInDirection.provider',
  'lastCallInDirection.state',
  'lastCallInDirection.reason',
];

/**
 * Two hostile shapes a literal cannot express, so that the table below covers
 * every code: an input read through a getter, and one that throws while it is
 * being read.
 */
function withAGetter() {
  const input = { direction: DIRECTION.STT };
  Object.defineProperty(input, 'settings', {
    enumerable: true,
    get() {
      return { stt: ENGINE.REMOTE };
    },
  });
  return input;
}

/** A revoked or throwing Proxy: reading any of its traps throws. */
function throwingInput() {
  return new Proxy(
    {},
    {
      ownKeys() {
        throw new Error('no keys for you');
      },
    },
  );
}

/**
 * An array that is not a plain array. A subclass passes `Array.isArray`, so the
 * reader's second half (`getPrototypeOf` is `Array.prototype`) is the only thing
 * between it and being read as the app's own storage.
 */
function arraySubclass(items) {
  const Sub = class extends Array {};
  const value = new Sub();
  for (const item of items) value.push(item);
  return value;
}

/**
 * A plain array whose only element sits behind a getter that throws. Reading it
 * would be an `internal-error`; the probe below expects `invalid-providers`, so a
 * reader that called the getter on its way to the element fails the table -- the
 * getter is the proof that it was never read, not a comment claiming so.
 */
function providersBehindAGetter() {
  const value = [];
  Object.defineProperty(value, '0', {
    enumerable: true,
    get() {
      throw new Error('readProviders must not call a getter');
    },
  });
  return value;
}

/**
 * One probe per refusal code. The point of the table is that **every code is
 * reached**: a guard nothing exercises is a guard nothing defends, and the count
 * assert below fails the day a code is added to `REFUSAL` without a probe here.
 */
const REFUSALS = [
  ['the input is not a plain object', null, REFUSAL.NOT_AN_OBJECT, 'input'],
  ['the input is a string', 'stt', REFUSAL.NOT_AN_OBJECT, 'input'],
  ['the input is read through a getter', withAGetter(), REFUSAL.ACCESSOR_PROPERTY, 'input'],
  ['the input throws while it is read', throwingInput(), REFUSAL.INTERNAL_ERROR, null],
  ['the input is an instance', new (class Settings {})(), REFUSAL.NOT_AN_OBJECT, 'input'],
  ['the input carries a field this module does not name', { direction: DIRECTION.STT, egress: 'x' }, REFUSAL.UNKNOWN_FIELD, 'input'],
  ['the direction is absent', {}, REFUSAL.MISSING_FIELD, 'direction'],
  ['the direction is not one of the two', { direction: 'microphone' }, REFUSAL.UNKNOWN_DIRECTION, 'direction'],
  ['the settings carry a field this module does not name', { direction: DIRECTION.STT, settings: { stt: 'local', mic: true } }, REFUSAL.UNKNOWN_FIELD, 'settings'],
  ['a switch is neither local nor remote', { direction: DIRECTION.STT, settings: { stt: 'REMOTE' } }, REFUSAL.UNKNOWN_ENGINE, 'settings.stt'],
  ['a switch is not a string at all', { direction: DIRECTION.TTS, settings: { tts: 1 } }, REFUSAL.UNKNOWN_ENGINE, 'settings.tts'],
  ['the consent is not an array', { direction: DIRECTION.STT, consent: { deepgram: true } }, REFUSAL.INVALID_CONSENT, 'consent'],
  ['the consent is an array subclass, which is not a plain array', { direction: DIRECTION.STT, consent: arraySubclass([{ provider: 'deepgram', at: AT }]) }, REFUSAL.INVALID_CONSENT, 'consent'],
  ['the consent carries a field this module does not name', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: AT, name: 'x' }] }, REFUSAL.UNKNOWN_FIELD, 'consent'],
  ['a consent names no provider', { direction: DIRECTION.STT, consent: [{ at: AT }] }, REFUSAL.MISSING_FIELD, 'consent.provider'],
  ['a consent is undated', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram' }] }, REFUSAL.MISSING_FIELD, 'consent.at'],
  ['a provider name is a URL', { direction: DIRECTION.STT, consent: [{ provider: 'https://stt.example.com', at: AT }] }, REFUSAL.BAD_PROVIDER, 'consent.provider'],
  ['a provider name is empty', { direction: DIRECTION.STT, consent: [{ provider: '', at: AT }] }, REFUSAL.BAD_PROVIDER, 'consent.provider'],
  ['a provider name is past its bound', { direction: DIRECTION.STT, consent: [{ provider: 'd'.repeat(PROVIDER_MAX + 1), at: AT }] }, REFUSAL.BAD_PROVIDER, 'consent.provider'],
  ['a provider name carries an invisible character', { direction: DIRECTION.STT, consent: [{ provider: 'deep​gram', at: AT }] }, REFUSAL.BAD_PROVIDER, 'consent.provider'],
  ['a consent time is the shape of an unset counter', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: 0 }] }, REFUSAL.INVALID_AT, 'consent.at'],
  ['a consent time is negative zero', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: -0 }] }, REFUSAL.INVALID_AT, 'consent.at'],
  ['a consent time is a string', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: String(AT) }] }, REFUSAL.INVALID_AT, 'consent.at'],
  ['a consent time is a fraction of a millisecond', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: 1.5 }] }, REFUSAL.INVALID_AT, 'consent.at'],
  ['a consent time is past the bound the package reads a time with', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: MAX_TIMESTAMP + 1 }] }, REFUSAL.INVALID_AT, 'consent.at'],
  ['one name carries two consents', { direction: DIRECTION.STT, consent: [{ provider: 'deepgram', at: AT }, { provider: 'deepgram', at: AT + 1 }] }, REFUSAL.DUPLICATE_CONSENT, 'consent.provider'],
  ['the status carries no ok at all', { direction: DIRECTION.STT, status: { providers: ['deepgram'] } }, REFUSAL.MISSING_FIELD, 'status.ok'],
  ['the status ok is not a boolean', { direction: DIRECTION.STT, status: { ok: 'true' } }, REFUSAL.WRONG_TYPE, 'status.ok'],
  ['the status is not a plain object', { direction: DIRECTION.STT, status: 'ok' }, REFUSAL.NOT_AN_OBJECT, 'status'],
  ['a failed status names no code', { direction: DIRECTION.STT, status: { ok: false } }, REFUSAL.MISSING_FIELD, 'status.code'],
  ['a failed status names a code outside the set', { direction: DIRECTION.STT, status: { ok: false, code: 'timeout' } }, REFUSAL.UNKNOWN_CODE, 'status.code'],
  ['an answer carries a field of the failed shape', { direction: DIRECTION.STT, status: { ok: true, code: 'unreachable' } }, REFUSAL.UNKNOWN_FIELD, 'status'],
  ['the admitted names are not an array', { direction: DIRECTION.STT, status: { ok: true, providers: 'deepgram' } }, REFUSAL.INVALID_PROVIDERS, 'status.providers'],
  ['the admitted names are an array subclass, which is not a plain array', { direction: DIRECTION.STT, status: { ok: true, providers: arraySubclass(['deepgram']) } }, REFUSAL.INVALID_PROVIDERS, 'status.providers'],
  ['an admitted name sits behind a getter, which is never called', { direction: DIRECTION.STT, status: { ok: true, providers: providersBehindAGetter() } }, REFUSAL.INVALID_PROVIDERS, 'status.providers'],
  ['an admitted name is not a name', { direction: DIRECTION.STT, status: { ok: true, providers: ['https://stt.example.com'] } }, REFUSAL.BAD_PROVIDER, 'status.providers'],
  ['one admitted name appears twice', { direction: DIRECTION.STT, status: { ok: true, providers: ['deepgram', 'deepgram'] } }, REFUSAL.DUPLICATE_PROVIDER, 'status.providers'],
  ['the direction map is not an object', { direction: DIRECTION.STT, status: { ok: true, direction: [true] } }, REFUSAL.NOT_AN_OBJECT, 'status.direction'],
  ['the direction map carries a field this module does not name', { direction: DIRECTION.STT, status: { ok: true, direction: { stt: true, mic: true } } }, REFUSAL.UNKNOWN_FIELD, 'status.direction'],
  ['a direction admission is not a boolean', { direction: DIRECTION.STT, status: { ok: true, direction: { stt: 'yes' } } }, REFUSAL.WRONG_TYPE, 'status.direction.stt'],
  ['the policy reason is outside the set', { direction: DIRECTION.STT, status: { ok: true, reason: 'because' } }, REFUSAL.UNKNOWN_CODE, 'status.reason'],
  ['the call carries a field this module does not name', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'local', provider: null, state: 'done', reason: null, at: AT } }, REFUSAL.UNKNOWN_FIELD, 'lastCallInDirection'],
  ['the call names no direction', { direction: DIRECTION.STT, lastCallInDirection: { engine: 'local', provider: null, state: 'done', reason: null } }, REFUSAL.MISSING_FIELD, 'lastCallInDirection.direction'],
  ['the call direction is not one of the two', { direction: DIRECTION.STT, lastCallInDirection: { direction: 'mic', engine: 'local', provider: null, state: 'done', reason: null } }, REFUSAL.UNKNOWN_DIRECTION, 'lastCallInDirection.direction'],
  ['the call names no engine', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, provider: null, state: 'done', reason: null } }, REFUSAL.MISSING_FIELD, 'lastCallInDirection.engine'],
  ['the call engine is neither local nor remote', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'cloud', provider: null, state: 'done', reason: null } }, REFUSAL.UNKNOWN_ENGINE, 'lastCallInDirection.engine'],
  ['the call names no state', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'local', provider: null, reason: null } }, REFUSAL.MISSING_FIELD, 'lastCallInDirection.state'],
  ['the call state is outside the three', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'local', provider: null, state: 'pending', reason: null } }, REFUSAL.UNKNOWN_CALL_STATE, 'lastCallInDirection.state'],
  ['a remote call names no provider', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'remote', provider: null, state: 'in-flight', reason: null } }, REFUSAL.MISSING_FIELD, 'lastCallInDirection.provider'],
  ['a local call names a provider', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'local', provider: 'deepgram', state: 'done', reason: null } }, REFUSAL.UNEXPECTED_PROVIDER, 'lastCallInDirection.provider'],
  ['a call provider is not a name', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'remote', provider: 'https://x.example', state: 'done', reason: null } }, REFUSAL.BAD_PROVIDER, 'lastCallInDirection.provider'],
  ['a call reason is outside the set', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'remote', provider: 'deepgram', state: 'failed', reason: 'timeout' } }, REFUSAL.UNKNOWN_CODE, 'lastCallInDirection.reason'],
  ['a call that did not fail carries a failure reason', { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: 'remote', provider: 'deepgram', state: 'done', reason: 'provider-error' } }, REFUSAL.UNEXPECTED_REASON, 'lastCallInDirection.reason'],
];

test('every refusal code is reached by a probe, and every probe is one of them', () => {
  const reached = new Set();
  for (const [name, input, code, field] of REFUSALS) {
    const result = indicator(input);
    assert.deepEqual(result, { ok: false, code, field }, `probe: ${name}`);
    reached.add(code);
  }
  assert.deepEqual([...reached].sort(), Object.values(REFUSAL).sort());
  assert.equal(Object.keys(REFUSAL).length, 18);
});

test('the README\'s refusal table is exactly REFUSAL, read out of the file', () => {
  // The README says a test fails if its table and REFUSAL ever drift apart, so
  // this is that test: it reads the file rather than trusting the two to have
  // been written in the same sitting. A reviewer reads the refusals from the
  // README, and a table that has drifted is a reader being told a code exists
  // that the module cannot return -- or one it can that no reader is warned of.
  const readme = readFileSync(new URL('./README.md', import.meta.url), 'utf8');
  const section = readme.match(/^## Refusals[^\n]*\n([\s\S]*?)(?=\n## )/m);
  assert.ok(section, 'README.md carries a `## Refusals` section');
  const codes = [...section[1].matchAll(/^\|\s*`([a-z][a-z0-9-]*)`\s*\|/gm)].map((row) => row[1]);
  // Both ways: a row for a code the module cannot return fails as loudly as a
  // code with no row. The set-size check catches a row listed twice, which the
  // comparison alone would read as one row.
  assert.deepEqual(codes.sort(), Object.values(REFUSAL).sort());
  assert.equal(new Set(codes).size, codes.length, 'no refusal code is listed twice');
});

test('an array input is not a plain object, and neither is a Map or a class instance', () => {
  for (const input of [[], new Map(), new Set(), new (class Input {})()]) {
    const result = indicator(input);
    assert.equal(result.ok, false);
    assert.equal(result.code, REFUSAL.NOT_AN_OBJECT);
  }
});

test('a refusal is frozen, carries no answer, and never echoes what it was handed', () => {
  // Not a name and not a key: what the module refuses to read must not come back
  // out through the refusal, or the refusal itself would be a channel.
  const marker = 'marker/value with space';
  const inputs = [
    { direction: DIRECTION.STT, secret: marker },
    { direction: DIRECTION.STT, consent: [{ provider: marker, at: AT }] },
    { direction: DIRECTION.STT, lastCallInDirection: { direction: DIRECTION.STT, engine: marker, provider: null, state: 'done' } },
    { direction: marker },
  ];
  for (const input of inputs) {
    const result = indicator(input);
    assert.equal(result.ok, false);
    assert.ok(Object.isFrozen(result));
    assert.deepEqual(Object.keys(result).sort(), ['code', 'field', 'ok']);
    assert.ok(FIELDS.includes(result.field), `${result.field} is a path this module chose`);
    assert.ok(!JSON.stringify(result).includes(marker), 'a refusal never echoes a value');
  }
  // An unknown key is reported by the container this module named -- the input --
  // and never by the key itself, which is the caller's choice and may be anything.
  const unknown = indicator({ direction: DIRECTION.STT, [marker]: true });
  assert.deepEqual(unknown, { ok: false, code: REFUSAL.UNKNOWN_FIELD, field: 'input' });
  assert.ok(!JSON.stringify(unknown).includes(marker));
});

test('a getter is refused without ever being called', () => {
  let called = 0;
  const input = { direction: DIRECTION.STT };
  Object.defineProperty(input, 'status', {
    enumerable: true,
    get() {
      called += 1;
      return PERMISSIVE.allowed;
    },
  });
  const result = indicator(input);
  assert.deepEqual(result, { ok: false, code: REFUSAL.ACCESSOR_PROPERTY, field: 'input' });
  assert.equal(called, 0, 'the getter was never called');
});

test('an input that throws while it is being read is refused, not thrown', () => {
  assert.deepEqual(indicator(throwingInput()), { ok: false, code: REFUSAL.INTERNAL_ERROR, field: null });
  const revoked = Proxy.revocable({ direction: DIRECTION.STT }, {});
  revoked.revoke();
  assert.deepEqual(indicator(revoked.proxy), { ok: false, code: REFUSAL.INTERNAL_ERROR, field: null });
});

test('null where the device has recorded nothing is read as absent, and as nothing else', () => {
  const absent = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    consent: PERMISSIVE.consent,
    status: PERMISSIVE.allowed,
  });
  const withNulls = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    consent: PERMISSIVE.consent,
    status: PERMISSIVE.allowed,
    lastCallInDirection: null,
  });
  assert.deepEqual(withNulls, absent);
  const device = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
  });
  const deviceNulls = indicator({
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    consent: null,
    status: null,
    lastCallInDirection: null,
  });
  assert.deepEqual(deviceNulls, device);
  assert.equal(device.reason, REASON.NO_CONSENT);
});

test('an undoable answer: the same input twice returns equal, frozen answers', () => {
  const input = {
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    consent: PERMISSIVE.consent,
    status: PERMISSIVE.allowed,
    lastCallInDirection: route(DIRECTION.STT, ENGINE.REMOTE, 'deepgram', CALL_STATE.IN_FLIGHT),
  };
  const first = indicator(input);
  const second = indicator(structuredClone(input));
  assert.deepEqual(first, second);
  assert.ok(Object.isFrozen(first));
  assert.notEqual(first, second, 'a fresh frozen record each time, not a shared one');
});

test('the provider name bounds are exact at the edge', () => {
  const atBound = { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: [{ provider: 'd'.repeat(PROVIDER_MAX), at: AT }] };
  assert.equal(indicator(atBound).ok, true);
  const pastBound = { direction: DIRECTION.STT, settings: { stt: ENGINE.REMOTE }, consent: [{ provider: 'd'.repeat(PROVIDER_MAX + 1), at: AT }] };
  assert.equal(indicator(pastBound).code, REFUSAL.BAD_PROVIDER);
  const many = {
    direction: DIRECTION.STT,
    settings: { stt: ENGINE.REMOTE },
    status: { ok: true, providers: Array.from({ length: MAX_PROVIDERS + 1 }, (_, i) => `p${i}`) },
  };
  assert.equal(indicator(many).code, REFUSAL.INVALID_PROVIDERS);
  const manyConsents = {
    direction: DIRECTION.STT,
    consent: Array.from({ length: MAX_CONSENT + 1 }, (_, i) => ({ provider: `p${i}`, at: AT })),
  };
  assert.equal(indicator(manyConsents).code, REFUSAL.INVALID_CONSENT);
});

test('a name is a name: the register\'s own spellings pass, and nothing URL-shaped does', () => {
  for (const name of ['deepgram', 'google-speech', 'ElevenLabs', 'eleven_labs', 'gcp.stt.v1', 'd1']) {
    const result = indicator({
      direction: DIRECTION.STT,
      settings: { stt: ENGINE.REMOTE },
      consent: [{ provider: name, at: AT }],
      status: admitted([name]),
    });
    assert.equal(result.state, STATE.REMOTE, name);
    assert.equal(result.provider, name);
    assert.equal(result.text, `remote (${name})`);
  }
  for (const name of ['https://stt.example.com', 'stt.example.com:443', 'a/b', 'a b', 'stt\n', 'ümlaut']) {
    const result = indicator({
      direction: DIRECTION.STT,
      settings: { stt: ENGINE.REMOTE },
      consent: [{ provider: name, at: AT }],
    });
    assert.equal(result.code, REFUSAL.BAD_PROVIDER, name);
  }
});
