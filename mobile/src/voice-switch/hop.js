// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The voice hop's client: the one place this app puts a spoken sentence on a
// wire (mobile plan, Task P5.3.7; D118, D123, D124; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.4 and §4.5).
//
// **What the hop is, and why the app talks to one at all.** Point 5 of the
// owner's ruling: the remote provider's credential never ships inside the app
// binary, so the app calls a Fugaro-hosted service (`internal/voicesvc`, the
// hop) which holds the key and makes the provider call. What the app presents
// is the **Firebase Auth ID token it already holds** -- item (1) of D111's two
// items -- and **no credential of its own**: no provider key (there is none on
// the device), no service-account JSON, no Fugaro admin credential, and the
// pairing MAC key is not this service's business and is never sent here.
//
// **The three routes, and the two that carry speech.** `POST
// <baseUrl>/v1/voice/status` carries a slug and nothing else and answers what
// the project's policy is for this device; `POST <baseUrl>/v1/voice/stt/<engine>`
// carries the audio; `POST <baseUrl>/v1/voice/tts/<engine>` carries the text to
// speak. The path, the engine's name pattern and the header names are
// `internal/voicesvc/wire.go`'s, `forward.go`'s and `register.go`'s, restated
// here so that a change to either side is a failing test rather than a call
// that 404s.
//
// **The URL is checked against the allow-list on every call, and the list ships
// empty.** D124's answer names no host, so `shared/url`'s
// `DEFAULT_HOST_ALLOW_LIST` is empty and every URL is refused `allow-list-empty`
// -- which is the correct behaviour for a gate whose list nobody has written:
// the app serves locally, says so, and no byte leaves. The hop's host is the
// owner's to name (the plan's P5.3, item 2), and the caller passes the list it
// was configured with; this file names no host of its own.
//
// **What this file deliberately does NOT do.** It does not decide *whether* a
// turn may go -- that is `egress.js`, the ten rules, and this client is the
// transport the rules hand a decision to. It does not retry, redirect, queue,
// spool, buffer or cache anything: one call is one request, a failure is a
// failure, and the answer is handed back as it arrived. It does not read the
// provider's answer into a shape of its own -- the hop streams the provider's
// own answer through (`internal/voicesvc/forward.go`), so what an engine's
// answer *says* is that engine's business and the app's engine table is where
// it would be read; this client hands back the bytes and the content type. What
// it *does* read is the hop's own `StatusAnswer`, which is this contract and not
// a provider's, and it reads it into the badge's own input (§4.2) so that the
// wire's vocabulary is translated in one place rather than wherever a screen
// happens to render it.
// And it logs nothing: the app has no logger at all (the boundary scan's rule),
// and a request line carrying a project slug or a transcript is exactly what
// must not appear in one.

import { POLICY_REASON, STATUS_FAILURE } from '../../../shared/voice-switch/index.js';
import { DEFAULT_HOST_ALLOW_LIST, validateExternalUrl } from '../../../shared/url/index.js';

/**
 * The hop's three paths, exactly as `internal/voicesvc` declares them
 * (`wire.go`'s `StatusPath`, `forward.go`'s `STTPathPrefix` and
 * `TTSPathPrefix`). The direction's word is the service's own and the engine's
 * name follows it, so the URL this client builds is the only URL it can build.
 */
export const HOP_PATHS = Object.freeze({
  STATUS: '/v1/voice/status',
  STT: '/v1/voice/stt/',
  TTS: '/v1/voice/tts/',
});

/**
 * The header names, exactly as the service reads them (`wire.go`'s
 * `HeaderAuth` and `authScheme`, `forward.go`'s `HeaderProject`).
 *
 * `SCHEME` includes its trailing space on purpose: the service cuts the scheme
 * off the header and refuses anything else, so a scheme assembled here without
 * the space would be a call the hop refuses.
 */
export const HOP_HEADER = Object.freeze({
  AUTHORIZATION: 'Authorization',
  PROJECT: 'X-Fugaro-Project',
  SCHEME: 'Bearer ',
  CONTENT_TYPE: 'Content-Type',
});

/**
 * The one content type the text direction sends. The audio direction's is the
 * caller's own -- the capture knows which container it recorded -- and the hop
 * refuses anything that is not an `audio/` type, so the check below is the same
 * rule one hop earlier rather than a second opinion about it.
 */
export const HOP_TEXT_TYPE = 'text/plain; charset=utf-8';

/**
 * The engine's name, as `internal/voicesvc`'s `registerNameRE` spells it. A
 * name is what the hop's register holds and never a path: this pattern is what
 * keeps a caller's string from reaching the URL as anything else.
 */
const ENGINE_NAME = /^[a-z][a-z0-9-]{0,19}$/;

/**
 * How this client refuses. A refusal is never a boolean and never an exception:
 * it is `{ ok: false, code, field, detail }`, where `field` names the input
 * that was wrong and `detail` carries the vocabulary of whoever refused --
 * `shared/url`'s own code for a URL, and the hop's wire reason for a call the
 * service answered with one.
 */
export const HOP_CODES = Object.freeze({
  /**
   * The composed URL is not one this app may open: it failed `shared/url`'s
   * validator, and `detail` is that validator's own code (`allow-list-empty`
   * while the owner has named no host, `host-not-allowed` for another host, and
   * the rest of its set). It is a refusal and never a fallback to another host.
   */
  BAD_URL: 'bad-url',
  /** The token port answered nothing this client can present. */
  NO_TOKEN: 'no-token',
  /** The request never reached the hop, or the transport failed. */
  UNREACHABLE: 'unreachable',
  /**
   * The hop answered, and refused. `detail` is its wire reason
   * (`token_unverified`, `device_unknown`, `force_local`, `provider_failed`,
   * ...) or `null` for a refusal whose body this app could not read, which is
   * the case `internal/voicesvc`'s own `ReasonOf` answers `state_unavailable`
   * for.
   */
  REFUSED: 'refused',
  /** The hop answered with something this client will not read as its contract. */
  BAD_ANSWER: 'bad-answer',
});

/**
 * The hop's wire reasons, as `internal/voicesvc/wire.go` declares them. They
 * are the service's vocabulary and not this file's: a reason not on this list
 * is read as `state_unavailable` (the app falls back to local either way), and
 * the list exists so that the app branches on a reason it knows rather than on
 * whatever a body happened to carry.
 */
export const HOP_REASON = Object.freeze({
  FORCE_LOCAL: 'force_local',
  NO_ADMISSION: 'no_admission',
  POLICY_UNREADABLE: 'policy_unreadable',
  NOT_ADMITTED: 'not_admitted',
  NOT_A_DEVICE: 'not_a_device',
  DEVICE_UNKNOWN: 'device_unknown',
  TOKEN_UNVERIFIED: 'token_unverified',
  STATE_UNAVAILABLE: 'state_unavailable',
  UNKNOWN_ENGINE: 'unknown_engine',
  PROVIDER_FAILED: 'provider_failed',
  BAD_REQUEST: 'bad_request',
  NOT_FOUND: 'not_found',
});

/** Every reason above, for the membership check a refusal body's is put to. */
const REASONS = Object.freeze(Object.values(HOP_REASON));

/**
 * The most answer this client will hold in memory, in bytes.
 *
 * The hop sets no bound on the answer it streams (the provider's own end of it
 * is what says a call is over), and a phone is not the place to discover that a
 * provider answered with something enormous. 4 MiB is generous for a spoken
 * turn's text and for a synthesised sentence, and it is a bound rather than a
 * measurement -- what it stops is this app holding an answer it never asked
 * for. A `Content-Length` over it is refused **before** the body is read, and
 * the count is kept **while the body arrives**, chunk by chunk, because the
 * header is a claim and a chunked answer makes none: a bound applied after
 * `arrayBuffer()` has resolved is a report on a body this phone is already
 * holding, which is the thing it exists to prevent.
 */
export const MAX_ANSWER_BYTES = 4 << 20;

/** A frozen refusal, in the shape this repository's modules use. */
function refuse(code, field, detail = null) {
  return Object.freeze({ ok: false, code, field, detail });
}

/** Whether a value is a plain object this module will read. */
function isPlainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/**
 * Build the hop's client.
 *
 * Every argument is required and checked here rather than trusted, for the
 * reason `createVoiceSwitch` and `createVoiceScreen` check theirs: a client
 * built without a transport or a token is a client that fails at the first
 * thing the owner says.
 *
 * @param {object} options
 * @param {string} options.baseUrl the hop's own address, with no trailing slash
 * @param {string[]} [options.hosts] the allow-list this installation was
 *   configured with; `DEFAULT_HOST_ALLOW_LIST` (empty) until the owner names
 *   the hop's host, and an empty list refuses every URL
 * @param {Function} options.fetch the transport: `fetch(url, init)`
 * @param {() => (string | Promise<string>)} options.token the Firebase ID token
 *   (D111's item 1), read per call so an expired one can be refreshed
 * @returns {object} the client: `status`, `transcribe` and `speak`
 * @throws {TypeError} for a port, an address or a value that cannot be used
 */
export function createVoiceHop({ baseUrl, hosts = DEFAULT_HOST_ALLOW_LIST, fetch, token } = {}) {
  if (typeof baseUrl !== 'string' || baseUrl.length === 0) {
    throw new TypeError('baseUrl is the hop’s own address: a non-empty string');
  }
  // The hop matches its two forwarding routes on an exact path and the status
  // route on an exact path too (server.go), so an address ending in a slash
  // would build `//v1/...` and reach no route at all. It is refused where it is
  // written rather than discovered as a 404 on the owner's first sentence.
  if (baseUrl.endsWith('/')) {
    throw new TypeError('baseUrl is the hop’s address with no trailing slash');
  }
  if (!Array.isArray(hosts)) {
    throw new TypeError('hosts is the allow-list of hosts this app may open: an array, empty until the hop’s host is named');
  }
  if (typeof fetch !== 'function') throw new TypeError('fetch is the transport');
  if (typeof token !== 'function') throw new TypeError('token reads the Firebase ID token');

  /**
   * The token to present, or a refusal.
   *
   * The port answers the token itself or refuses; a value this client cannot
   * present as one -- an empty string, a non-string, a rejection -- is refused
   * as `no-token` rather than sent. Nothing here reads, stores or logs the
   * token: it is read per call, it goes into one header, and no branch below
   * puts it anywhere else.
   */
  async function present() {
    let value;
    try {
      value = await token();
    } catch {
      return refuse(HOP_CODES.NO_TOKEN, 'token');
    }
    if (typeof value !== 'string' || value.length === 0) return refuse(HOP_CODES.NO_TOKEN, 'token');
    return Object.freeze({ ok: true, token: value });
  }

  /**
   * One request, and exactly one.
   *
   * The URL is the address the app was configured with plus a constant path
   * (plus an engine name that has already been matched against
   * `registerNameRE`'s shape), and it is put through `shared/url` **whole** --
   * so the scheme, the authority, the absence of userinfo and of a port, the
   * absence of an encoded or internationalised host, and the exact allow-list
   * are all checked on the string that is actually opened.
   *
   * What is in the request is the whole of what this app sends: one
   * `Authorization` header carrying the ID token, one `Content-Type`, the
   * project header on the two routes that carry speech, and the payload as the
   * body. `credentials: 'omit'`, `redirect: 'error'` and `cache: 'no-store'`
   * are stated rather than defaulted: no cookie or ambient credential is
   * attached to this request, a redirect is a failed call rather than a call to
   * a host the allow-list never saw, and no intermediary keeps the answer.
   */
  async function call({ path, project, body, contentType, signal }) {
    const target = validateExternalUrl(baseUrl + path, hosts);
    if (!target.ok) return refuse(HOP_CODES.BAD_URL, 'baseUrl', target.code);
    const presented = await present();
    if (!presented.ok) return presented;

    const headers = {
      [HOP_HEADER.AUTHORIZATION]: HOP_HEADER.SCHEME + presented.token,
      [HOP_HEADER.CONTENT_TYPE]: contentType,
    };
    if (project !== null) headers[HOP_HEADER.PROJECT] = project;

    let response;
    try {
      response = await fetch(target.url, {
        method: 'POST',
        headers,
        body,
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
        signal,
      });
    } catch {
      // A transport failure is never carried: what a fetch rejects with is not
      // this tree's text, and the app's answer to it is the local engine either
      // way (§4.5). Nothing is retried, and no second host is tried.
      return refuse(HOP_CODES.UNREACHABLE, 'hop');
    }

    const status = readStatus(response);
    if (status === null) return refuse(HOP_CODES.BAD_ANSWER, 'answer');
    if (status < 200 || status >= 300) return refusedBy(response, status);
    return accept(response);
  }

  /** The response's own status line, or `null` for an answer that has none. */
  function readStatus(response) {
    if (!isPlainObject(response)) return null;
    return Number.isInteger(response.status) ? response.status : null;
  }

  /** What the hop refused with: its wire reason when the body carries one. */
  async function refusedBy(response, status) {
    let reason = null;
    try {
      const body = await readText(response, MAX_ANSWER_BYTES);
      const parsed = JSON.parse(body);
      if (isPlainObject(parsed) && typeof parsed.error === 'string' && REASONS.includes(parsed.error)) {
        reason = parsed.error;
      }
    } catch {
      // A refusal body this app cannot read is a refusal with no reason, which
      // internal/voicesvc's own ReasonOf answers state_unavailable for -- and
      // the app's answer to any of them is the local engine (§4.5).
      reason = null;
    }
    return Object.freeze({ ok: false, code: HOP_CODES.REFUSED, field: 'hop', detail: reason, status });
  }

  /** The answer body, bounded, whatever the hop streamed through. */
  async function accept(response) {
    const contentType = readContentType(response);
    const bounded = await readBytes(response);
    if (!bounded.ok) return bounded;
    return Object.freeze({ ok: true, contentType, body: bounded.body });
  }

  /** The answer's content type, or `null` when it named none. */
  function readContentType(response) {
    const headers = response.headers;
    if (headers === null || typeof headers !== 'object' || typeof headers.get !== 'function') return null;
    const value = headers.get('content-type');
    return typeof value === 'string' ? value : null;
  }

  /** A `Content-Length` claim over the bound, refused before the body is read. */
  function claimed(response, max) {
    const headers = response.headers;
    if (headers === null || typeof headers !== 'object' || typeof headers.get !== 'function') return false;
    const value = Number(headers.get('content-length'));
    return Number.isFinite(value) && value > max;
  }

  /** The response's own readable body, where the transport offers one. */
  function readerFor(response) {
    const body = response.body;
    if (body === null || typeof body !== 'object' || typeof body.getReader !== 'function') return null;
    try {
      return body.getReader();
    } catch {
      return null;
    }
  }

  /**
   * The body's bytes, held to `max` **while they arrive**.
   *
   * A count taken after `arrayBuffer()` has resolved is a report rather than a
   * bound: by then the whole body is already in this phone's memory, which is
   * the one thing the bound exists to prevent. And `Content-Length` is a claim
   * -- a chunked answer makes none, and a body may make a false one -- so where
   * the transport hands back a readable body the chunks are read one at a time
   * and the reader is **cancelled** the moment the running total passes `max`:
   * nothing further is pulled, and the chunks in hand are dropped. A response
   * with no readable body (a plain object, and what a transport that predates
   * streams answers with) is read in one piece and counted after, which is the
   * same answer one allocation later.
   *
   * @returns {Promise<{ok: true, bytes: Uint8Array} | {ok: false}>}
   */
  async function readBounded(response, max) {
    if (claimed(response, max)) return { ok: false };
    const reader = readerFor(response);
    if (reader === null) {
      let buffer;
      try {
        buffer = await response.arrayBuffer();
      } catch {
        return { ok: false };
      }
      const bytes = new Uint8Array(buffer);
      return bytes.byteLength > max ? { ok: false } : { ok: true, bytes };
    }
    const chunks = [];
    let size = 0;
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        const chunk = value instanceof Uint8Array ? value : new Uint8Array(value);
        size += chunk.byteLength;
        if (size > max) {
          await cancel(reader);
          return { ok: false };
        }
        chunks.push(chunk);
      }
    } catch {
      await cancel(reader);
      return { ok: false };
    }
    const bytes = new Uint8Array(size);
    let at = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, at);
      at += chunk.byteLength;
    }
    return { ok: true, bytes };
  }

  /** Let go of a body this client has stopped reading. */
  async function cancel(reader) {
    try {
      await reader.cancel();
    } catch {
      // A reader that will not cancel is a body already abandoned: the answer is
      // a refusal either way, and what a cancel rejects with is not this tree's.
    }
  }

  /** The answer's bytes, held to `MAX_ANSWER_BYTES` on the way in. */
  async function readBytes(response) {
    const bounded = await readBounded(response, MAX_ANSWER_BYTES);
    if (!bounded.ok) return refuse(HOP_CODES.BAD_ANSWER, 'answer');
    return Object.freeze({ ok: true, body: bounded.bytes });
  }

  /** The answer's text, held to `max` bytes. It throws, and its caller's `catch` is the answer. */
  async function readText(response, max) {
    const bounded = await readBounded(response, max);
    if (!bounded.ok) throw new RangeError('the answer is over this client’s bound');
    return new TextDecoder().decode(bounded.bytes);
  }

  return Object.freeze({
    /**
     * Ask what the project's policy is for this device (§4.4 point 7).
     *
     * What comes back is **the badge's own input and nothing else**, so the
     * answer is handed to `shared/voice-switch`'s `indicator` as its `status`
     * argument **as it stands**, with no unwrapping and no field to strip:
     * `{ ok: true, providers, direction, reason }` with the reason in the
     * badge's own kebab vocabulary, or `{ ok: false, code }` with one of its
     * four status failures. That second shape is what draws §4.5's rows -- a
     * revoked device, an expired token -- and it is why this client keeps its
     * own `field` and `detail` to itself: an answer carrying either is refused
     * by that module as a field it does not know, and a refused status is the
     * fail-closed `local -- checking` badge rather than the row the answer was
     * meant to draw. The translation is one table (below) and it lives here
     * because the hop's wire vocabulary is this client's business and nobody
     * else's.
     *
     * The body is read strictly enough that the app cannot mistake one it does
     * not understand for an admission: `project` must be the slug that was asked
     * about (a cached answer read against another project is the one thing that
     * answer must never be -- `wire.go` echoes it for exactly this reason), the
     * members this app reads must have their types, and anything else is the
     * local answer. `providers` is **absent** from the wire when it is empty
     * (`StatusAnswer`'s `omitempty`), which is the empty list and not a defect.
     *
     * A `project` that is not a slug is the caller's own mistake and not an
     * answer about the policy: it comes back as this client's own `bad-answer`
     * refusal, the same one `transcribe` and `speak` give for the same mistake,
     * and a caller holding one knows it has a bug rather than a badge.
     *
     * @param {{project: string, signal?: object}} input
     * @returns {Promise<object>} `{ ok: true, providers, direction, reason }` or
     *   `{ ok: false, code }` -- both of them a badge's `status` input as they
     *   stand -- or this client's refusal for a `project` that is not a slug
     */
    async status({ project, signal } = {}) {
      if (typeof project !== 'string' || project.length === 0) return refuse(HOP_CODES.BAD_ANSWER, 'project');
      const called = await call({
        path: HOP_PATHS.STATUS,
        project: null,
        body: JSON.stringify({ project }),
        contentType: 'application/json',
        signal,
      });
      if (!called.ok) return statusFailed(statusFailure(called));
      return readStatusAnswer(called.body, project);
    },

    /**
     * Send one turn's audio to the hop (§4.4 point 1).
     *
     * `contentType` is the capture's own container and is checked here as the
     * hop checks it, so a turn whose audio cannot be typed is refused before
     * the bytes go rather than after they arrived. `audio` is the payload
     * itself: this client sends what it is handed, adds nothing to it, and
     * keeps no copy.
     *
     * @param {{project: string, engine: string, audio: object, contentType: string, signal?: object}} input
     * @returns {Promise<object>} `{ ok: true, contentType, body }` or a refusal
     */
    async transcribe({ project, engine, audio, contentType, signal } = {}) {
      return forward({ direction: 'stt', project, engine, audio, contentType, signal });
    },

    /**
     * Send one turn's text to the hop to be spoken (§4.4 point 1).
     *
     * The text arriving here has already been through the redactor (D118's
     * rule, `egress.js`): this client does not redact, because a transport that
     * also decided what may leave would be a second place for that decision to
     * be wrong.
     *
     * @param {{project: string, engine: string, text: string, signal?: object}} input
     * @returns {Promise<object>} `{ ok: true, contentType, body }` or a refusal
     */
    async speak({ project, engine, text, signal } = {}) {
      if (typeof text !== 'string' || text.length === 0) return refuse(HOP_CODES.BAD_ANSWER, 'text');
      return forward({ direction: 'tts', project, engine, audio: text, contentType: HOP_TEXT_TYPE, signal });
    },
  });

  /**
   * The two forwarding routes, which differ in their payload and in nothing
   * else: the same checks, the same path shape, the same one request.
   */
  async function forward({ direction, project, engine, audio, contentType, signal }) {
    if (typeof project !== 'string' || project.length === 0) return refuse(HOP_CODES.BAD_ANSWER, 'project');
    if (typeof engine !== 'string' || !ENGINE_NAME.test(engine)) return refuse(HOP_CODES.BAD_ANSWER, 'engine');
    if (direction === 'stt' && (typeof contentType !== 'string' || !contentType.startsWith('audio/'))) {
      return refuse(HOP_CODES.BAD_ANSWER, 'contentType');
    }
    if (audio === undefined || audio === null) return refuse(HOP_CODES.BAD_ANSWER, 'audio');
    const called = await call({
      path: HOP_PATHS[direction === 'stt' ? 'STT' : 'TTS'] + engine,
      project,
      body: audio,
      contentType,
      signal,
    });
    if (!called.ok) return called;
    return Object.freeze({ ok: true, engine, contentType: called.contentType, body: called.body });
  }
}

/**
 * A status query that did not produce an answer, in the badge's four reasons.
 *
 * The mapping is the whole point of this function: `shared/voice-switch` reads
 * `{ ok: false, code }` with the code in its own set, and the hop refuses with
 * twelve wire reasons. Two of the twelve are answers the app holds and says
 * (a token that will not verify, a device whose node is gone); a hop that could
 * not be reached is the third; and everything else -- a policy node this service
 * could not read, a body this app could not read, a path that is not there -- is
 * `query-failed`, which §4.5 reads as no answer at all.
 */
function statusFailure(called) {
  if (called.code === HOP_CODES.UNREACHABLE || called.code === HOP_CODES.BAD_URL) {
    return STATUS_FAILURE.UNREACHABLE;
  }
  if (called.code === HOP_CODES.NO_TOKEN) return STATUS_FAILURE.EXPIRED_TOKEN;
  if (called.code === HOP_CODES.REFUSED) {
    if (called.detail === HOP_REASON.TOKEN_UNVERIFIED) return STATUS_FAILURE.EXPIRED_TOKEN;
    // `device_unknown` is revocation itself -- "the device node is gone" -- and
    // the app says so. `not_a_device` is deliberately **not** folded into it: a
    // token that verifies and is not a device's is a fact about the identity
    // this app presented, and telling the user their device was revoked would be
    // a false sentence about the world. It is a query that failed.
    if (called.detail === HOP_REASON.DEVICE_UNKNOWN) return STATUS_FAILURE.REVOKED_DEVICE;
  }
  return STATUS_FAILURE.QUERY_FAILED;
}

/**
 * The hop's `StatusAnswer`, read into the badge's own input.
 *
 * `wire.go`'s members are the wire's; what is returned here is
 * `{ ok: true, providers, direction, reason }` -- the four fields
 * `shared/voice-switch`'s `indicator` takes and the only four, because that
 * module refuses a status answer carrying a field it does not name. The detail
 * sentence is dropped here rather than carried: it is written for a person, and
 * a field the badge does not read is a field the app might.
 *
 * **An answer is not a copy of the policy node.** No member here is a policy
 * field the app may read a rule out of, and nothing on this path is a
 * substitute for the hop's own refusal on the call itself (§4.4 point 7): the
 * badge it feeds is what the screen shows, and the hop re-reads the node when
 * the call arrives.
 */
async function readStatusAnswer(body, project) {
  let parsed;
  try {
    parsed = JSON.parse(new TextDecoder().decode(body));
  } catch {
    return statusFailed(STATUS_FAILURE.QUERY_FAILED);
  }
  if (!isPlainObject(parsed)) return statusFailed(STATUS_FAILURE.QUERY_FAILED);
  // The echo is checked and not trusted: this is the one field of the answer
  // that says *which* question it answers, and a cached answer read against
  // another project would be a policy answer about somebody else's project.
  if (parsed.project !== project) return statusFailed(STATUS_FAILURE.QUERY_FAILED);
  if (typeof parsed.remote !== 'boolean') return statusFailed(STATUS_FAILURE.QUERY_FAILED);
  // `omitempty`: absent is the empty list, and the two spellings of "this
  // project admits nobody" are one answer (§4.3).
  const providers = parsed.providers === undefined ? [] : parsed.providers;
  if (!Array.isArray(providers) || providers.some((name) => typeof name !== 'string')) {
    return statusFailed(STATUS_FAILURE.QUERY_FAILED);
  }
  const direction = parsed.direction;
  if (!isPlainObject(direction) || typeof direction.stt !== 'boolean' || typeof direction.tts !== 'boolean') {
    return statusFailed(STATUS_FAILURE.QUERY_FAILED);
  }
  const reason = parsed.reason === undefined || parsed.reason === '' ? null : parsed.reason;
  if (reason !== null && !REASONS.includes(reason)) return statusFailed(STATUS_FAILURE.QUERY_FAILED);

  // The three project-policy answers, in the badge's own vocabulary. A service
  // that answered `remote: false` while naming no reason is the same answer as
  // `no_admission` -- "an absent, empty or all-switches-off policy ... are one
  // answer by design" -- and a node this service could not read is not an
  // admission but a query that failed, so it is the one member of the family
  // that does not come back as an answer.
  let policy = null;
  if (!parsed.remote) {
    if (reason === HOP_REASON.POLICY_UNREADABLE) return statusFailed(STATUS_FAILURE.QUERY_FAILED);
    policy = reason === HOP_REASON.FORCE_LOCAL ? POLICY_REASON.FORCE_LOCAL : POLICY_REASON.NO_ADMISSION;
  }
  return Object.freeze({
    ok: true,
    providers: Object.freeze([...providers]),
    direction: Object.freeze({ stt: direction.stt, tts: direction.tts }),
    reason: policy,
  });
}

/**
 * A status query that did not answer, in the badge's own failure shape: exactly
 * `{ ok: false, code }`, one of `shared/voice-switch`'s four status failures.
 *
 * **The two fields and nothing beside them is the whole point.** That module
 * refuses a status answer carrying a field it does not name, and a refused
 * status is not an error a screen shows -- it is the fail-closed
 * `local -- checking` badge. A revoked device and an expired token are exactly
 * the two rows §4.5 asks the app to say out loud, so this client's own word for
 * what happened (`shared/url`'s `allow-list-empty`, the hop's wire reason, the
 * member of the answer it could not read) stops at this boundary: carried
 * beside the answer it would cost the owner the sentence that names the cause.
 */
function statusFailed(code) {
  return Object.freeze({ ok: false, code });
}
