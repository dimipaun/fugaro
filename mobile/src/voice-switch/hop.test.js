// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The hop client's tests (mobile plan, Task P5.3.7 -- SECURITY-SENSITIVE; D111,
// D118, D123, D124; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.4 and §4.5).
//
// What is pinned here is the **request**: one call is one request, its URL is a
// string this app checked against the allow-list, its headers are the three the
// hop's own contract names, its only credential is the Firebase ID token, and
// its body is the payload and nothing else. The other half -- *whether* a turn
// may go -- is `egress.js`'s, and `egress.test.js` is where the ten rules are.
//
// The allow-list here is a fixture host and never the hop's: the host is the
// owner's to name (P5.3, item 2), and this directory ships no host of its own.

import assert from 'node:assert/strict';
import test from 'node:test';

import { DEFAULT_HOST_ALLOW_LIST } from '../../../shared/url/index.js';
import { indicator } from '../../../shared/voice-switch/index.js';
import { HOP_CODES, HOP_HEADER, HOP_PATHS, HOP_REASON, MAX_ANSWER_BYTES, createVoiceHop } from './hop.js';

const HOST = 'voice.hop.test';
const BASE = `https://${HOST}`;
const HOSTS = Object.freeze([HOST]);
const PROJECT = 'acme-0123456789abcdef';
const ENGINE = 'deepgram';
const AUDIO = Object.freeze({ bytes: 4 });
const TEXT = 'the run ended green';

/** A response, in the shape `fetch` answers with and nothing more. */
function response({ status = 200, body = '', contentType = 'application/json', claims = {} } = {}) {
  const headers = new Map([
    ['content-type', contentType],
    ...Object.entries(claims),
  ]);
  return {
    status,
    headers: { get: (name) => headers.get(name.toLowerCase()) ?? null },
    arrayBuffer: async () => new TextEncoder().encode(body).buffer,
    text: async () => body,
  };
}

/** A transport that answers `answers` in order and counts its calls. */
function transport(...answers) {
  const calls = [];
  const fetch = async (url, init) => {
    calls.push({ url, init });
    const answer = answers[Math.min(calls.length - 1, answers.length - 1)];
    if (answer instanceof Error) throw answer;
    return answer;
  };
  return { fetch, calls };
}

/** A token port that answers the tokens it was given, one per read. */
function tokens(...values) {
  const reads = [];
  return {
    reads,
    token: async () => {
      reads.push(Date.now());
      return values[Math.min(reads.length - 1, values.length - 1)];
    },
  };
}

/** A client over one transport, with a fixture token. */
function client(port, over = {}) {
  return createVoiceHop({
    baseUrl: BASE,
    hosts: HOSTS,
    fetch: port.fetch,
    token: async () => 'id-token-1',
    ...over,
  });
}

test('one call is one request, and its URL is the hop’s own path on the configured host', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = client(port);

  await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });
  await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  await hop.status({ project: PROJECT });

  assert.equal(port.calls.length, 3);
  assert.deepEqual(
    port.calls.map((call) => call.url),
    [`${BASE}${HOP_PATHS.STT}${ENGINE}`, `${BASE}${HOP_PATHS.TTS}${ENGINE}`, `${BASE}${HOP_PATHS.STATUS}`],
  );
  for (const call of port.calls) {
    assert.equal(call.init.method, 'POST');
    // The three of §4.4's request: no cache, no ambient credential, and a
    // redirect is a failed call rather than a call to a host nothing checked.
    assert.equal(call.init.cache, 'no-store');
    assert.equal(call.init.credentials, 'omit');
    assert.equal(call.init.redirect, 'error');
  }
});

test('the request carries the ID token, and no credential of its own', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = createVoiceHop({
    baseUrl: BASE,
    hosts: HOSTS,
    fetch: port.fetch,
    token: async () => 'id-token-1',
  });

  await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });

  const [call] = port.calls;
  // Exactly the three headers the hop's contract names: the ID token, the
  // content type, and the project the call is for. A fourth header is how a
  // second credential -- a MAC key, a provider key, an account token -- would
  // reach a third party, and there is no branch here that writes one.
  assert.deepEqual(Object.keys(call.init.headers).sort(), [
    HOP_HEADER.AUTHORIZATION,
    HOP_HEADER.CONTENT_TYPE,
    HOP_HEADER.PROJECT,
  ]);
  assert.equal(call.init.headers[HOP_HEADER.AUTHORIZATION], `${HOP_HEADER.SCHEME}id-token-1`);
  assert.equal(call.init.headers[HOP_HEADER.PROJECT], PROJECT);
  assert.equal(call.init.headers[HOP_HEADER.CONTENT_TYPE], 'audio/wav');
  // The status route names the project in its body and not in a header,
  // because that request is *about* the project rather than *for* it.
  const asked = transport(response({ status: 200, body: JSON.stringify({ project: PROJECT, remote: false }) }));
  await client(asked).status({ project: PROJECT });
  assert.equal(HOP_HEADER.PROJECT in asked.calls[0].init.headers, false);
});

test('the body is the payload and nothing else', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = client(port);

  await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });
  assert.equal(port.calls[0].init.body, AUDIO);

  await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  assert.equal(port.calls[1].init.body, TEXT);

  await hop.status({ project: PROJECT });
  // The one body this client composes itself, and it is the slug and nothing
  // else: `StatusRequest` is `{project}` and decodes strictly.
  assert.deepEqual(JSON.parse(port.calls[2].init.body), { project: PROJECT });
});

test('an empty allow-list refuses every URL, and the request is never opened', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = createVoiceHop({ baseUrl: BASE, fetch: port.fetch, token: async () => 'id-token-1' });

  assert.deepEqual(DEFAULT_HOST_ALLOW_LIST, []);
  const answer = await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  assert.equal(answer.ok, false);
  assert.equal(answer.code, HOP_CODES.BAD_URL);
  // `shared/url`'s own word for it, carried rather than re-spelled.
  assert.equal(answer.detail, 'allow-list-empty');
  assert.equal(port.calls.length, 0);
});

test('a host that is not on the list is refused, in the same breath', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = client(port, { baseUrl: 'https://somewhere.else.test' });
  const answer = await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  assert.equal(answer.code, HOP_CODES.BAD_URL);
  assert.equal(answer.detail, 'host-not-allowed');
  assert.equal(port.calls.length, 0);

  // A status query carries the same local answer in the badge's own vocabulary
  // -- the app's answer to "no host is allowed yet" is the local engine either
  // way (§4.5) -- and it carries it in **exactly** the two fields a badge's
  // `status` input has. `shared/url`'s `host-not-allowed`, which the forwarding
  // routes carry in `detail`, is not one of them: `shared/voice-switch` refuses
  // a status answer with a field it does not name, and a refused status is the
  // fail-closed `local -- checking` badge rather than the row the answer drew.
  const asked = await hop.status({ project: PROJECT });
  assert.equal(asked.code, 'unreachable');
  assert.deepEqual(Object.keys(asked).sort(), ['code', 'ok']);
  assert.equal(port.calls.length, 0);
});

test('the engine’s name is checked before it can reach the URL', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = client(port);
  const refused = ['', '../status', 'status/', 'Deepgram', '-deepgram', 'a'.repeat(21), 'deep gram', 'الأصوات'];

  for (const engine of refused) {
    const answer = await hop.transcribe({ project: PROJECT, engine, audio: AUDIO, contentType: 'audio/wav' });
    assert.equal(answer.ok, false, engine);
    assert.equal(answer.code, HOP_CODES.BAD_ANSWER, engine);
    assert.equal(answer.field, 'engine', engine);
  }
  assert.equal(port.calls.length, 0);
});

test('a payload the hop would refuse is refused here, before it goes', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const hop = client(port);

  // The audio direction's content type is the capture's own, and the hop
  // refuses anything that is not an `audio/` type; the check is the same rule
  // one hop earlier rather than a second opinion about it.
  for (const contentType of ['text/plain', 'application/json', '']) {
    const answer = await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType });
    assert.equal(answer.code, HOP_CODES.BAD_ANSWER, contentType);
    assert.equal(answer.field, 'contentType', contentType);
  }
  assert.equal((await hop.transcribe({ project: PROJECT, engine: ENGINE, contentType: 'audio/wav' })).field, 'audio');
  assert.equal((await hop.speak({ project: PROJECT, engine: ENGINE, text: '' })).field, 'text');
  assert.equal((await hop.status({ project: '' })).field, 'project');
  assert.equal(port.calls.length, 0);
});

test('an unreachable hop is one failure and no second attempt', async () => {
  const port = transport(new Error('the network is down'));
  const hop = client(port);
  const answer = await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });
  assert.equal(answer.code, HOP_CODES.UNREACHABLE);
  assert.equal(port.calls.length, 1);
  // What a transport rejects with is not this tree's text and is never carried.
  assert.equal(answer.detail, null);
});

test('a refusal names the hop’s reason, and a body that names none invents none', async () => {
  const port = transport(
    response({ status: 403, body: JSON.stringify({ error: HOP_REASON.DEVICE_UNKNOWN }) }),
    response({ status: 500, body: '<html>not ours</html>', contentType: 'text/html' }),
    response({ status: 401, body: JSON.stringify({ error: 'something_else_entirely' }) }),
  );
  const hop = client(port);

  const known = await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });
  assert.equal(known.code, HOP_CODES.REFUSED);
  assert.equal(known.detail, HOP_REASON.DEVICE_UNKNOWN);
  assert.equal(known.status, 403);

  const opaque = await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  assert.equal(opaque.code, HOP_CODES.REFUSED);
  assert.equal(opaque.detail, null);

  // A reason this client's list does not hold is not adopted: the app branches
  // on a reason it knows, and anything else is no reason at all.
  const unknown = await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  assert.equal(unknown.detail, null);
});

test('the answer is handed back as it arrived, bounded', async () => {
  const port = transport(
    response({ status: 200, body: '{"text":"hello"}', contentType: 'application/json; charset=utf-8' }),
    response({ status: 200, body: 'x', claims: { 'content-length': String(MAX_ANSWER_BYTES + 1) } }),
  );
  const hop = client(port);

  const answer = await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });
  assert.equal(answer.ok, true);
  assert.equal(answer.engine, ENGINE);
  assert.equal(answer.contentType, 'application/json; charset=utf-8');
  assert.equal(new TextDecoder().decode(answer.body), '{"text":"hello"}');

  // The claim is refused before the body is read: a phone is not the place to
  // hold an answer it never asked for.
  const huge = await hop.transcribe({ project: PROJECT, engine: ENGINE, audio: AUDIO, contentType: 'audio/wav' });
  assert.equal(huge.code, HOP_CODES.BAD_ANSWER);
});

test('the ID token is read per call, and an unusable one is not sent', async () => {
  const port = transport(response({ status: 200, body: 'ok' }));
  const seen = tokens('id-token-1', 'id-token-2');
  const hop = createVoiceHop({ baseUrl: BASE, hosts: HOSTS, fetch: port.fetch, token: seen.token });

  await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  await hop.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
  assert.equal(seen.reads.length, 2);
  assert.equal(port.calls[0].init.headers[HOP_HEADER.AUTHORIZATION], 'Bearer id-token-1');
  assert.equal(port.calls[1].init.headers[HOP_HEADER.AUTHORIZATION], 'Bearer id-token-2');

  const before = port.calls.length;
  for (const token of [async () => '', async () => undefined, async () => null, async () => 7]) {
    const unreadable = createVoiceHop({ baseUrl: BASE, hosts: HOSTS, fetch: port.fetch, token });
    const answer = await unreadable.speak({ project: PROJECT, engine: ENGINE, text: TEXT });
    assert.equal(answer.code, HOP_CODES.NO_TOKEN);
    assert.equal(answer.field, 'token');
  }
  const throwing = createVoiceHop({
    baseUrl: BASE,
    hosts: HOSTS,
    fetch: port.fetch,
    token: async () => {
      throw new Error('the refresh failed');
    },
  });
  assert.equal((await throwing.speak({ project: PROJECT, engine: ENGINE, text: TEXT })).code, HOP_CODES.NO_TOKEN);
  assert.equal(port.calls.length, before);
});

test('the status answer is read into the badge’s own input, in its own words', async () => {
  const answers = {
    [`{"project":"${PROJECT}","remote":true,"providers":["deepgram"],"direction":{"stt":true,"tts":false}}`]: {
      ok: true, providers: ['deepgram'], direction: { stt: true, tts: false }, reason: null,
    },
    [`{"project":"${PROJECT}","remote":false,"providers":["x"],"direction":{"stt":true,"tts":true},"reason":"force_local"}`]: {
      ok: true, providers: ['x'], direction: { stt: true, tts: true }, reason: 'force-local',
    },
    [`{"project":"${PROJECT}","remote":false,"direction":{"stt":false,"tts":false},"reason":"no_admission"}`]: {
      ok: true, providers: [], direction: { stt: false, tts: false }, reason: 'no-admission',
    },
    // `providers` is `omitempty` on the wire, and absent is the empty list.
    [`{"project":"${PROJECT}","remote":false,"direction":{"stt":false,"tts":false}}`]: {
      ok: true, providers: [], direction: { stt: false, tts: false }, reason: 'no-admission',
    },
  };

  for (const [body, wanted] of Object.entries(answers)) {
    const port = transport(response({ status: 200, body }));
    const answer = await client(port).status({ project: PROJECT });
    // The answer **is** the badge's input, unwrapped: exactly the four fields
    // `shared/voice-switch` names, because that module refuses a status answer
    // carrying a field it does not know. The detail sentence the wire carries,
    // and this client's own reading of the body, are dropped rather than handed
    // on, and a caller hands this object over as it stands.
    assert.deepEqual(answer, wanted, body);
    assert.deepEqual(Object.keys(answer).sort(), ['direction', 'ok', 'providers', 'reason'], body);
  }
});

test('a status query that did not answer says so in the badge’s four failures', async () => {
  const cases = [
    [new Error('offline'), 'unreachable'],
    [response({ status: 401, body: JSON.stringify({ error: HOP_REASON.TOKEN_UNVERIFIED }) }), 'expired-token'],
    [response({ status: 403, body: JSON.stringify({ error: HOP_REASON.DEVICE_UNKNOWN }) }), 'revoked-device'],
    [response({ status: 403, body: JSON.stringify({ error: HOP_REASON.NOT_A_DEVICE }) }), 'query-failed'],
    [response({ status: 500, body: JSON.stringify({ error: HOP_REASON.STATE_UNAVAILABLE }) }), 'query-failed'],
    [response({ status: 200, body: JSON.stringify({ project: PROJECT, remote: false, reason: 'policy_unreadable' }) }), 'query-failed'],
    [response({ status: 200, body: JSON.stringify({ project: 'other-0123456789abcdef', remote: true }) }), 'query-failed'],
    [response({ status: 200, body: 'not json' }), 'query-failed'],
    [response({ status: 200, body: JSON.stringify({ project: PROJECT, remote: 'yes' }) }), 'query-failed'],
    [response({ status: 200, body: JSON.stringify({ project: PROJECT, remote: true, providers: [7] }) }), 'query-failed'],
    [response({ status: 200, body: JSON.stringify({ project: PROJECT, remote: true, reason: 'made_up' }) }), 'query-failed'],
  ];
  for (const [answer, expected] of cases) {
    const port = transport(answer);
    const got = await client(port).status({ project: PROJECT });
    assert.equal(got.ok, false, expected);
    assert.equal(got.code, expected, expected);
    // And that is the whole answer: a `field` or a `detail` here would be a
    // field `shared/voice-switch` does not know, which is the fail-closed
    // `local -- checking` badge instead of §4.5's revoked-device or
    // expired-token row.
    assert.deepEqual(Object.keys(got).sort(), ['code', 'ok'], expected);
  }
});

test('a status answer is handed to the badge as it stands, so §4.5’s rows are the rows drawn', async () => {
  // The proof is not that the answer *looks* like a badge's input but that the
  // badge reads it: `shared/voice-switch` refuses a status answer carrying a
  // field it does not name, and a refused status is the fail-closed
  // `local -- checking` badge -- so an answer this client decorated with its own
  // `field` or `detail` would cost the owner the sentence §4.5 asks for. Remote
  // is on and the consent is recorded in every row below, which is what makes
  // the local answer the *answer's* doing rather than the settings'.
  const settings = { stt: 'remote', tts: 'local' };
  const consent = [{ provider: ENGINE, at: 1 }];

  for (const [answer, reason] of [
    [response({ status: 403, body: JSON.stringify({ error: HOP_REASON.DEVICE_UNKNOWN }) }), 'revoked-device'],
    [response({ status: 401, body: JSON.stringify({ error: HOP_REASON.TOKEN_UNVERIFIED }) }), 'expired-token'],
    [new Error('offline'), 'unreachable'],
  ]) {
    const asked = await client(transport(answer)).status({ project: PROJECT });
    const badge = indicator({ direction: 'stt', settings, consent, status: asked });
    assert.equal(badge.ok, true, reason);
    assert.equal(badge.reason, reason, reason);
    assert.equal(badge.state, 'local', reason);
    assert.equal(badge.engine, 'local', reason);
  }

  // ... and the same hand-over draws the remote badge when the answer admits the
  // name: one object, handed over as it stands, on both paths.
  const port = transport(response({
    status: 200,
    body: JSON.stringify({ project: PROJECT, remote: true, providers: [ENGINE], direction: { stt: true, tts: false } }),
  }));
  const admitted = await client(port).status({ project: PROJECT });
  const badge = indicator({ direction: 'stt', settings, consent, status: admitted });
  assert.equal(badge.ok, true);
  assert.equal(badge.text, `remote (${ENGINE})`);
});

test('a client built without a transport, a token or an address is refused where it is built', () => {
  const port = transport(response({}));
  assert.throws(() => createVoiceHop({ hosts: HOSTS, fetch: port.fetch, token: async () => 't' }), TypeError);
  assert.throws(() => createVoiceHop({ baseUrl: '', hosts: HOSTS, fetch: port.fetch, token: async () => 't' }), TypeError);
  assert.throws(() => createVoiceHop({ baseUrl: BASE, hosts: HOSTS, fetch: port.fetch }), TypeError);
  assert.throws(() => createVoiceHop({ baseUrl: BASE, hosts: HOSTS, token: async () => 't' }), TypeError);
  assert.throws(() => createVoiceHop({ baseUrl: BASE, hosts: 'not-a-list', fetch: port.fetch, token: async () => 't' }), TypeError);
  // A trailing slash would compose `//v1/voice/...`, which reaches no route on
  // the service at all: refused where the address is written rather than
  // discovered as a 404 on the owner's first sentence.
  assert.throws(() => createVoiceHop({ baseUrl: `${BASE}/`, hosts: HOSTS, fetch: port.fetch, token: async () => 't' }), TypeError);
});

test('the client exports the hop’s own vocabulary and no host of its own', () => {
  assert.deepEqual(
    { ...HOP_PATHS },
    { STATUS: '/v1/voice/status', STT: '/v1/voice/stt/', TTS: '/v1/voice/tts/' },
  );
  assert.deepEqual(
    { ...HOP_HEADER },
    {
      AUTHORIZATION: 'Authorization',
      PROJECT: 'X-Fugaro-Project',
      SCHEME: 'Bearer ',
      CONTENT_TYPE: 'Content-Type',
    },
  );
  // The twelve reasons `internal/voicesvc/wire.go` declares, pinned so that a
  // reason added on one side is a failing test on the other.
  assert.deepEqual(Object.values(HOP_REASON).sort(), [
    'bad_request',
    'device_unknown',
    'force_local',
    'no_admission',
    'not_a_device',
    'not_admitted',
    'not_found',
    'policy_unreadable',
    'provider_failed',
    'state_unavailable',
    'token_unverified',
    'unknown_engine',
  ]);
});
