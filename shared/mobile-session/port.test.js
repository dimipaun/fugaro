// The mobile session port and its stub's tests (mobile identity and pairing
// plan, Task C2).
//
// Written BEFORE the modules they import, on purpose: that is this task's red
// step, and it is the plan's own words -- "the shape test fails with no port".
// `npm test` from `shared/` discovers this file through P0.1's glob and fails
// with ERR_MODULE_NOT_FOUND until `shared/mobile-session/port.js` exists, so
// the run is red for the right reason and not because no test was found -- the
// one way this package's runner can lie (see `shared/README.md`).
//
// What the tests pin, from the plan's C2 and the documents it reads:
//
//   * the port names what an app surface may rely on and nothing else: the
//     project's opaque id, its endpoint, the roster's role, the three policy
//     values C1's session response carries (J3 reads `costly_modes` and
//     `biometric_on_costly`, Z2 reads `compaction_threshold`), an `idToken()`
//     that refreshes, a `signMessage(bytes)` that signs with the device key
//     (O6), and the one event a surface may rely on, `signedOut`;
//   * the member list is CLOSED and that is the security property: a session
//     carrying a refresh token -- the Firebase refresh token D2783 forbids the
//     app to keep -- is refused rather than read, so no surface can reach one
//     through the port;
//   * a role here is a display claim and never an authorisation (F14, D2791):
//     the port carries the roster's three roles and nothing that could be read
//     as a grant;
//   * fail closed: every way a value can fail to be this session has its own
//     code, and a refusal names the member, never the value;
//   * the entry point imports nothing, so React Native's bundler (which
//     resolves no `node:` builtin) can load it;
//   * the stub returns a FIXED fake session that conforms to the port, signs
//     nothing, never touches a key, and is driven by `stubSignOut` -- the one
//     thing a surface test needs and a page of fakes otherwise has to invent;
//   * after `signedOut` the session refuses its accessors, which is the shape
//     M3's red step ("a refresh with a rotated-out token must fail and sign the
//     project out first") leaves behind;
//   * `docs/mobile-identity-pairing/session-port.md` says all of it: every
//     member, role, policy value and code is checked against the document, so
//     the prose cannot drift from the module.
//
// No value here is a secret, and no literal is secret-shaped. The stub's
// "signature" is a fixed 64-byte vector that signs nothing.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  PORT_CODES,
  SESSION_EVENTS,
  SESSION_MEMBERS,
  SESSION_POLICY_FIELDS,
  SESSION_ROLES,
  SessionPortError,
  assertSessionPort,
} from './port.js';
import {
  STUB_ACCESS_TOKEN,
  STUB_COMPACTION_THRESHOLD,
  STUB_COSTLY_MODES,
  STUB_CRASH_REPORTS,
  STUB_ENDPOINT,
  STUB_PROJECT_ID,
  stubSession,
  stubSignOut,
} from './stub.js';

/** A session built here rather than by the stub, so the port's own shape is
 * pinned without the stub in the picture. */
function conforming(overrides = {}) {
  return {
    project_id: 'prj-opaque-7',
    endpoint: 'https://pairing.fugaro.example',
    role: SESSION_ROLES.OPERATOR,
    policy: {
      costly_modes: ['pro', 'critical'],
      biometric_on_costly: false,
      compaction_threshold: 0.4,
      crash_reports: true,
    },
    idToken: async () => 'a-project-id-token',
    signMessage: async () => new Uint8Array(64),
    on: () => () => {},
    ...overrides,
  };
}

/** The code and member a refusal carries, or null when nothing was thrown. */
function refusalOf(build) {
  try {
    assertSessionPort(build());
  } catch (error) {
    assert.ok(error instanceof SessionPortError, `expected a SessionPortError, got ${error}`);
    return { code: error.code, member: error.member };
  }
  return null;
}

test('the port names the roster three roles', () => {
  assert.deepEqual(SESSION_ROLES, { VIEWER: 'viewer', OPERATOR: 'operator', ADMIN: 'admin' });
  assert.ok(Object.isFrozen(SESSION_ROLES), 'the roles are data, and frozen');
});

test('the port names the one event a surface may rely on', () => {
  assert.equal(SESSION_EVENTS.SIGNED_OUT, 'signedOut');
  assert.ok(Object.isFrozen(SESSION_EVENTS));
});

test("the port names the policy values C1's session response carries", () => {
  assert.deepEqual(SESSION_POLICY_FIELDS, [
    'costly_modes',
    'biometric_on_costly',
    'compaction_threshold',
    'crash_reports',
  ]);
  assert.ok(Object.isFrozen(SESSION_POLICY_FIELDS));
});

test('the port names its members, and only its members', () => {
  assert.deepEqual(SESSION_MEMBERS, [
    'project_id',
    'endpoint',
    'role',
    'policy',
    'idToken',
    'signMessage',
    'on',
  ]);
  assert.ok(Object.isFrozen(SESSION_MEMBERS));
});

test('every refusal code is a distinct token', () => {
  const codes = Object.values(PORT_CODES);
  assert.equal(new Set(codes).size, codes.length, 'two refusals share a code');
  for (const code of codes) assert.equal(typeof code, 'string');
  assert.ok(Object.isFrozen(PORT_CODES));
});

test('a conforming session passes, and is returned as it was given', () => {
  const session = conforming();
  assert.equal(assertSessionPort(session), session, 'the port validates, it does not copy or wrap');
});

test('the stub conforms to the port', () => {
  assert.doesNotThrow(() => assertSessionPort(stubSession()));
});

test('a value that is not a plain object is refused', () => {
  const notSessions = [null, undefined, 'a session', 42, true, [], () => {}, new Date(0)];
  class Session {}
  notSessions.push(new Session());
  for (const value of notSessions) {
    let thrown = null;
    try {
      assertSessionPort(value);
    } catch (error) {
      thrown = error;
    }
    assert.ok(thrown instanceof SessionPortError, `${String(value)} was not refused`);
    assert.equal(thrown.code, PORT_CODES.NOT_A_SESSION);
  }
});

test('a member outside the port is refused, so a refresh token cannot travel through it', () => {
  const refused = refusalOf(() => conforming({ refresh_token: 'a-device-bound-refresh-token' }));
  assert.deepEqual(refused, { code: PORT_CODES.UNKNOWN_MEMBER, member: 'refresh_token' });
});

test('a symbol-keyed member is refused too', () => {
  const session = conforming();
  session[Symbol('id_token')] = 'a-project-id-token';
  const refused = refusalOf(() => session);
  assert.deepEqual(refused, { code: PORT_CODES.UNKNOWN_MEMBER, member: '$' });
});

test('a member the port does not name is refused even when it is not enumerable', () => {
  // `Object.keys` sees enumerable own string keys only, so a member hidden from
  // it would pass a check built on it while still being a member the session
  // carries and a surface can read. Both objects the port closes -- the session
  // and the policy -- are checked on this.
  const session = conforming();
  Object.defineProperty(session, 'refresh_token', { value: 'a-device-bound-refresh-token' });
  assert.deepEqual(refusalOf(() => session), { code: PORT_CODES.UNKNOWN_MEMBER, member: 'refresh_token' });

  const policy = conforming();
  Object.defineProperty(policy.policy, 'refresh_token', { value: 'a-device-bound-refresh-token' });
  assert.deepEqual(refusalOf(() => policy), {
    code: PORT_CODES.UNKNOWN_POLICY_FIELD,
    member: 'policy.refresh_token',
  });
});

test('a symbol-keyed policy member is refused too', () => {
  const session = conforming();
  session.policy[Symbol('refresh_token')] = 'a-device-bound-refresh-token';
  assert.deepEqual(refusalOf(() => session), { code: PORT_CODES.UNKNOWN_POLICY_FIELD, member: 'policy' });
});

test('each member the port names is required', () => {
  for (const member of SESSION_MEMBERS) {
    const refused = refusalOf(() => {
      const session = conforming();
      delete session[member];
      return session;
    });
    assert.deepEqual(refused, { code: PORT_CODES.MISSING_MEMBER, member }, `${member} was not required`);
  }
});

test('a member of the wrong kind is refused before it is used', () => {
  for (const member of ['project_id', 'endpoint', 'role']) {
    const refused = refusalOf(() => conforming({ [member]: 42 }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_TYPE, member });
  }
  for (const member of ['idToken', 'signMessage', 'on']) {
    const refused = refusalOf(() => conforming({ [member]: 'not-a-function' }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_TYPE, member });
  }
  assert.deepEqual(refusalOf(() => conforming({ policy: [] })), {
    code: PORT_CODES.BAD_TYPE,
    member: 'policy',
  });
  assert.deepEqual(refusalOf(() => conforming({ policy: 'pro' })), {
    code: PORT_CODES.BAD_TYPE,
    member: 'policy',
  });
});

test('a project id that cannot be one is refused', () => {
  for (const id of ['', ' ', 'has a space', 'a'.repeat(129), 'projéct', 'proj\nect']) {
    const refused = refusalOf(() => conforming({ project_id: id }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_PROJECT_ID, member: 'project_id' });
  }
});

test('an endpoint that is not an https URL is refused', () => {
  const long = `https://${'a'.repeat(2048)}`;
  for (const endpoint of ['http://pairing.fugaro.example', 'pairing.fugaro.example', 'https://', 'https://a b', long]) {
    const refused = refusalOf(() => conforming({ endpoint }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_ENDPOINT, member: 'endpoint' });
  }
});

test('a role outside the roster three is refused', () => {
  for (const role of ['owner', 'Operator', '', 'guest']) {
    const refused = refusalOf(() => conforming({ role }));
    assert.deepEqual(refused, { code: PORT_CODES.UNKNOWN_ROLE, member: 'role' });
  }
});

test('a policy with a field the port does not carry is refused', () => {
  const refused = refusalOf(() =>
    conforming({
      policy: {
        costly_modes: [],
        biometric_on_costly: false,
        compaction_threshold: null,
        crash_reports: true,
        model: 'the-policy-file-is-not-the-session',
      },
    }),
  );
  assert.deepEqual(refused, { code: PORT_CODES.UNKNOWN_POLICY_FIELD, member: 'policy.model' });
});

test('each policy value the port names is required', () => {
  for (const field of SESSION_POLICY_FIELDS) {
    const refused = refusalOf(() => {
      const session = conforming();
      delete session.policy[field];
      return session;
    });
    assert.deepEqual(refused, { code: PORT_CODES.MISSING_MEMBER, member: `policy.${field}` });
  }
});

test('a policy value outside its shape is refused', () => {
  const badModes = ['pro', ['pro', 'pro'], [''], ['pro', 7], ['pro', 'a'.repeat(65)], new Array(33).fill('m')];
  for (const costly_modes of badModes) {
    const refused = refusalOf(() => conforming({ policy: { ...conforming().policy, costly_modes } }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_POLICY_VALUE, member: 'policy.costly_modes' });
  }
  for (const biometric_on_costly of ['yes', 1, null]) {
    const refused = refusalOf(() => conforming({ policy: { ...conforming().policy, biometric_on_costly } }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_POLICY_VALUE, member: 'policy.biometric_on_costly' });
  }
  for (const compaction_threshold of [0, 1, 1.5, -0.1, '0.4', Number.NaN]) {
    const refused = refusalOf(() => conforming({ policy: { ...conforming().policy, compaction_threshold } }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_POLICY_VALUE, member: 'policy.compaction_threshold' });
  }
  // The crash-report switch is the one policy value with no closed set and no
  // range: it is a boolean, and a value that is neither is refused rather than
  // read as the fail-safe, because a surface that saw "on" for a stray string
  // would send a report the project did not admit.
  for (const crash_reports of ['yes', 1, null]) {
    const refused = refusalOf(() => conforming({ policy: { ...conforming().policy, crash_reports } }));
    assert.deepEqual(refused, { code: PORT_CODES.BAD_POLICY_VALUE, member: 'policy.crash_reports' });
  }
});

test('the policy values a project may really carry are accepted', () => {
  const accepted = [
    { costly_modes: [], biometric_on_costly: true, compaction_threshold: null, crash_reports: false },
    { costly_modes: ['critical'], biometric_on_costly: false, compaction_threshold: 0.01, crash_reports: true },
    { costly_modes: ['pro', 'critical'], biometric_on_costly: true, compaction_threshold: 0.99, crash_reports: false },
  ];
  for (const policy of accepted) {
    assertSessionPort(conforming({ policy }));
  }
});

test('the port imports nothing, so the phone can load it', () => {
  // React Native is one of this package's two designated consumers (D119), and
  // its bundler resolves no `node:` builtin. The port is pure data and pure
  // functions over values, so it needs no import at all -- and a static import
  // added here would make the module unloadable on the phone rather than merely
  // cost it something.
  const source = readFileSync(new URL('./port.js', import.meta.url), 'utf8');
  assert.equal(/^\s*import\b/m.test(source), false, 'no import statement in port.js');
});

test('the stub reaches the port and nothing else', () => {
  const source = readFileSync(new URL('./stub.js', import.meta.url), 'utf8');
  const specifiers = [...source.matchAll(/\bfrom\s+['"]([^'"]+)['"]/g)].map((match) => match[1]);
  assert.deepEqual(specifiers, ['./port.js'], 'the stub conforms to the port, and imports nothing else');
  assert.equal(/\bnode:/.test(source), false, 'no node builtin in stub.js');
});

test("the stub's session is the fixed fake session the surfaces mount on", () => {
  const session = stubSession();
  assert.equal(session.project_id, STUB_PROJECT_ID);
  assert.equal(session.endpoint, STUB_ENDPOINT);
  assert.equal(session.role, SESSION_ROLES.OPERATOR);
  assert.deepEqual([...session.policy.costly_modes], [...STUB_COSTLY_MODES]);
  assert.equal(session.policy.biometric_on_costly, false);
  assert.equal(session.policy.compaction_threshold, STUB_COMPACTION_THRESHOLD);
  assert.equal(session.policy.crash_reports, STUB_CRASH_REPORTS);
  assert.equal(typeof session.idToken, 'function');
  assert.equal(typeof session.signMessage, 'function');
  assert.equal(typeof session.on, 'function');
});

test('the stub can be asked for another role, and a policy field at a time', () => {
  const viewer = stubSession({ role: SESSION_ROLES.VIEWER, policy: { costly_modes: [] } });
  assert.equal(viewer.role, SESSION_ROLES.VIEWER);
  assert.deepEqual([...viewer.policy.costly_modes], []);
  assert.equal(viewer.policy.biometric_on_costly, false, 'the fields the override did not name stay');
  assert.equal(viewer.policy.compaction_threshold, STUB_COMPACTION_THRESHOLD);
  assert.equal(viewer.policy.crash_reports, STUB_CRASH_REPORTS, 'the fields the override did not name stay');

  const quiet = stubSession({ policy: { compaction_threshold: null, crash_reports: false } });
  assert.equal(quiet.policy.compaction_threshold, null);
  assert.equal(quiet.policy.crash_reports, false, 'a test can ask for a project that turned the reports off');
  assert.deepEqual([...quiet.policy.costly_modes], [...STUB_COSTLY_MODES]);

  assert.ok(Object.isFrozen(viewer), 'a test cannot make a surface pass by mutating the session');
});

test("the stub's idToken resolves to the same fixed token every call", async () => {
  const session = stubSession();
  const first = session.idToken();
  assert.equal(typeof first.then, 'function', 'idToken refreshes, so it is awaited');
  assert.equal(await first, STUB_ACCESS_TOKEN);
  assert.equal(await session.idToken(), STUB_ACCESS_TOKEN);
});

test("the stub's signMessage returns a fixed 64-byte signature that signs nothing", async () => {
  const session = stubSession();
  const signature = await session.signMessage(new Uint8Array([1, 2, 3]));
  assert.ok(signature instanceof Uint8Array);
  assert.equal(signature.length, 64, "Ed25519's signature length, which is the device key's");
  assert.deepEqual([...signature], [...(await session.signMessage(new Uint8Array([9])))]);

  const first = await session.signMessage(new Uint8Array([1]));
  first[0] ^= 0xff;
  const second = await session.signMessage(new Uint8Array([1]));
  assert.notEqual(second[0], first[0], 'a caller cannot corrupt the next signature');
});

test('the stub refuses bytes that are not a Uint8Array', async () => {
  const session = stubSession();
  for (const bytes of ['a message', [1, 2, 3], null, new ArrayBuffer(4)]) {
    await assert.rejects(session.signMessage(bytes), { code: PORT_CODES.BAD_TYPE });
  }
});

test('the stub fires the signedOut listener once, and the unsubscriber stops it', () => {
  const session = stubSession();
  let fired = 0;
  const off = session.on(SESSION_EVENTS.SIGNED_OUT, () => {
    fired += 1;
  });
  assert.equal(typeof off, 'function', 'on returns its own unsubscriber');
  assert.equal(fired, 0);

  stubSignOut(session);
  assert.equal(fired, 1);
  stubSignOut(session);
  assert.equal(fired, 1, 'the event is once: a signed-out session stays signed out');

  const second = stubSession();
  const unsubscribe = second.on(SESSION_EVENTS.SIGNED_OUT, () => {
    fired += 1;
  });
  unsubscribe();
  stubSignOut(second);
  assert.equal(fired, 1, 'an unsubscribed listener is not called');
});

test('the stub refuses an event it does not carry, and a listener that is not one', () => {
  const session = stubSession();
  assert.throws(() => session.on('tokenRefreshed', () => {}), { code: PORT_CODES.UNKNOWN_EVENT });
  assert.throws(() => session.on(SESSION_EVENTS.SIGNED_OUT, 'not-a-listener'), {
    code: PORT_CODES.BAD_LISTENER,
  });
});

test('a signed-out session refuses its accessors rather than returning a stale credential', async () => {
  const session = stubSession();
  stubSignOut(session);
  await assert.rejects(session.idToken(), { code: PORT_CODES.SIGNED_OUT });
  await assert.rejects(session.signMessage(new Uint8Array(1)), { code: PORT_CODES.SIGNED_OUT });
});

test('the stub refuses to sign out a session it did not make', () => {
  assert.throws(() => stubSignOut(conforming()), TypeError);
});

test('an override that breaks the shape is refused when the stub is built', () => {
  assert.throws(() => stubSession({ project_id: '' }), { code: PORT_CODES.BAD_PROJECT_ID });
  assert.throws(() => stubSession({ role: 'owner' }), { code: PORT_CODES.UNKNOWN_ROLE });
  assert.throws(() => stubSession({ refresh_token: 'a-device-bound-refresh-token' }), {
    code: PORT_CODES.UNKNOWN_MEMBER,
  });
});

test('session-port.md documents every member, role, policy value and code', () => {
  const doc = readFileSync(
    new URL('../../docs/mobile-identity-pairing/session-port.md', import.meta.url),
    'utf8',
  );
  for (const member of SESSION_MEMBERS) {
    assert.ok(doc.includes(`\`${member}`), `session-port.md does not document the member ${member}`);
  }
  for (const role of Object.values(SESSION_ROLES)) {
    assert.ok(doc.includes(`\`${role}\``), `session-port.md does not document the role ${role}`);
  }
  for (const field of SESSION_POLICY_FIELDS) {
    assert.ok(doc.includes(`\`${field}\``), `session-port.md does not document the policy value ${field}`);
  }
  for (const [name, code] of Object.entries(PORT_CODES)) {
    assert.ok(doc.includes(`\`${code}\``), `session-port.md does not document the code ${name} (${code})`);
  }
  assert.ok(doc.includes(SESSION_EVENTS.SIGNED_OUT), 'session-port.md does not name the signedOut event');
  assert.ok(doc.includes('stubSession'), 'session-port.md does not name the stub');
});
