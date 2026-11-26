// The phone's job-control verbs (mobile identity and pairing plan, Task J4;
// D2787, D162, D169; F14, F18).
//
// Start and cancel, and the half of them that is the phone's: whether the
// session's role may send one at all, and the mailbox write each one makes.
// It is pure -- it imports nothing, reads no clock, no store and no network --
// and it sends nothing: what it returns is handed to the app's one write path,
// `mobile/src/mailbox/client.js`, where every byte this app writes is tagged,
// validated and stored (D113, `mobile/src/mailbox/write-path.test.js`).
//
// **Why the composition is here and the write is not.** D2787 answers how the
// phone reaches a project: through the mailbox, consumed by the P5.1
// orchestrator, so a start and a cancel are mailbox messages and there is no
// second way in -- no call to a service, no direct API. The two writes differ
// in KIND, and that difference is the whole module:
//
//   * a **start** is D162's launch, typed since O10 (J5a): `kind: 'action'` with
//     a `launch` verb, carrying the mode it would start in and the repository it
//     starts in. A costly mode can therefore be refused by the consumer without a
//     confirmation, and the mode is a field rather than prose in a body. J4 sent
//     a launch as an ordinary message, because J5a had not landed; J5b switched
//     the phone to the typed record, which is what makes the start fail closed at
//     a reader that predates the verb.
//   * a **cancel** is D169's action record (`kind: 'action'`): the read-back,
//     the fixed phrase and exactly one confirmation are what authorise it, and
//     the record the P6.2 machine built is what travels. This module does
//     **not** build that record, and must not: the machine is where "one
//     confirmation authorises exactly one action" is decided, and the record's
//     id, target and confirmation are its answers, read back from the resolved
//     target rather than parsed from anything a speaker said. The record is an
//     input here, and what this module refuses is a record that is not that
//     verb's action.
//
// **The role is a display claim, and this is the display.** What a role may do
// is the P5.1 consumer's, decided at consume time with `internal/access`'s
// `ActionLaunch` and `ActionCancel` against the roster, and never from a role
// carried in a message (F14, D2791). The other question is the phone's: which
// of the two buttons to draw, and whether a screen may send the write at all.
// Hiding is not authorisation -- a modified client that sends anyway meets the
// consumer's refusal -- and the table below is `internal/access`'s own: both
// actions need `operator`, so a viewer sends neither and an operator and an
// admin send both (the plan's global table, pinned by J3 and J4).
//
// Nothing here is a secret, and no literal is secret-shaped.

/**
 * The closed set of verbs, each with the `internal/access` action it needs and
 * the roster roles that hold it.
 *
 * It is a table rather than branches so that the two facts this module is
 * accountable for are readable together -- what a verb is called on the wire
 * and who may send it -- and so that a test can pin both instead of trusting a
 * reader. The `action` names are `internal/access`'s (`ActionLaunch` and
 * `ActionCancel` are literally `launch` and `cancel`), and the role list is its
 * `MinRoleOperator` for each of the two, spelled as roles rather than as an
 * ordering so that a reader does not have to know how the roster ranks them.
 */
export const JOB_COMMANDS = Object.freeze({
  launch: Object.freeze({ action: 'launch', roles: Object.freeze(['operator', 'admin']) }),
  cancel: Object.freeze({ action: 'cancel', roles: Object.freeze(['operator', 'admin']) }),
});

/** The closed set as a list, for a screen that draws a button per verb. */
export const JOB_VERBS = Object.freeze(Object.keys(JOB_COMMANDS));

/** The roster's three roles: `internal/access`'s names, which the C2 port
 * carries. They are here as a list rather than imported from the port because
 * this module imports nothing at all (its sibling `index.js` states that rule
 * for the module, and a bundler that resolves no `node:` builtin is why). */
export const JOB_ROLES = Object.freeze(['viewer', 'operator', 'admin']);

/**
 * Every way an input can fail to be one of these writes, exported so a screen
 * branches on one vocabulary rather than on strings it typed itself.
 *
 * Their shapes are the write path's, not this module's sibling's: a refusal is
 * `{ ok: false, code, field }` with no `detail`, because `client.js` returns
 * these answers to the caller unchanged beside its own, and a caller that had
 * to read two refusal shapes would branch on the module rather than on the
 * fault. `not-a-session`, `unknown-role` and `bad-text` are therefore spelled
 * as the C2 port's and the client's own codes: one fault is one code wherever
 * it is found.
 */
export const JOB_CODES = Object.freeze({
  /** The session is not a plain object, so its role cannot be read at all. */
  NOT_A_SESSION: 'not-a-session',
  /** The session carries a role the roster does not have. */
  UNKNOWN_ROLE: 'unknown-role',
  /** The role is real and may not send this verb (a viewer sends neither). */
  NOT_PERMITTED: 'not-permitted',
  /** The verb is outside the closed set. */
  UNKNOWN_VERB: 'unknown-verb',
  /** The request carries a field this module does not read. */
  UNKNOWN_FIELD: 'unknown-field',
  /** The message's own text is absent, empty or not a string. */
  BAD_TEXT: 'bad-text',
  /** A `launch` or `cancel` without that verb's action record, or with one that
   * is not of the verb it was handed beside. The record's own field set is the
   * schema's and is validated by the client before the write; this names what
   * this module needs to be sure of. */
  BAD_RECORD: 'bad-record',
});

/** The fields a request may carry: `verb`, `text`, and the verb's own action
 * `record`. A field this module does not name is refused rather than
 * ignored, so a caller that misspelled one hears about it instead of sending
 * something else. */
const REQUEST_FIELDS = Object.freeze(['verb', 'text', 'record']);

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object') return false;
  if (Array.isArray(value)) return false;
  return Object.getPrototypeOf(value) === Object.prototype;
}

/** The session's role, or `null` when the value is not a session this module
 * can read a role from. */
function roleOf(session) {
  if (!isPlainObject(session)) return null;
  return JOB_ROLES.includes(session.role) ? session.role : null;
}

/**
 * Whether this session's role may send this verb -- the question a screen asks
 * to decide whether to draw the control at all.
 *
 * A boolean rather than a refusal, because "no" is the whole of what a screen
 * does with it, and `false` is the answer for every way the question can fail:
 * a value that is not a session, a role the roster does not have, a verb
 * outside the closed set, and a role that is real and too low. A screen that
 * hid a control on a malformed session and one that hid it from a viewer have
 * made the same mistake, and neither has authorised anything.
 *
 * @param {unknown} session one paired project's session (C2's port value)
 * @param {unknown} verb one of `JOB_VERBS`
 * @returns {boolean}
 */
export function maySend(session, verb) {
  const role = roleOf(session);
  if (role === null) return false;
  const command = Object.hasOwn(JOB_COMMANDS, verb) ? JOB_COMMANDS[verb] : null;
  return command !== null && command.roles.includes(role);
}

/**
 * The write one job command makes: what to send, and what the client needs to
 * know about it.
 *
 *     jobWrite(session, { verb: 'launch', text, record })    // a start
 *     jobWrite(session, { verb: 'cancel', text, record })    // a cancel
 *
 * `session` is one paired project's session (C2's port value) and only its
 * `role` is read. `text` is the message's own text: for a start, the owner's
 * own words (D162: a launch travels verbatim, and the caps are what bound it);
 * for a cancel, the sentence the phone shows for the action, which is the
 * caller's and not this module's -- the read-back D47's Rule requires is
 * `shared/confirm`'s sentence, built from the resolved target, and a second
 * copy of it here would be a second spelling of one sentence.
 *
 * `record` is the verb's action record, and it is an input: this module builds
 * no record and mints no id. What it refuses is a record that is not the verb's
 * -- a `launch` needs its `mode` and a `target.repo` and no `target.pr`, a
 * `cancel` a `target.run` (O10) -- and everything else it carries is the
 * schema's and is validated before the write.
 *
 * What comes back is `{ ok: true, verb, text, options }`, where `options` is
 * exactly what `client.send` takes: `{ action: record }`, for both verbs, so the
 * message carries `kind: 'action'` and is never queued. The record is passed
 * through as its builder built it, uncopied: the client's schema validation
 * deep-freezes it before anything is written.
 *
 * @param {unknown} session one paired project's session (C2's port value)
 * @param {unknown} request `{ verb, text, record }`
 * @returns {{ok: true, verb: string, text: string, options: object}
 *   | {ok: false, code: string, field: string|null}}
 */
export function jobWrite(session, request = {}) {
  const role = roleOf(session);
  if (role === null) {
    return isPlainObject(session)
      ? refuse(JOB_CODES.UNKNOWN_ROLE, 'role')
      : refuse(JOB_CODES.NOT_A_SESSION, 'session');
  }
  if (!isPlainObject(request)) return refuse(JOB_CODES.UNKNOWN_FIELD, 'request');
  for (const key of Object.keys(request)) {
    if (!REQUEST_FIELDS.includes(key)) return refuse(JOB_CODES.UNKNOWN_FIELD, key);
  }
  const verb = request.verb;
  if (!Object.hasOwn(JOB_COMMANDS, verb)) return refuse(JOB_CODES.UNKNOWN_VERB, 'verb');
  if (!JOB_COMMANDS[verb].roles.includes(role)) return refuse(JOB_CODES.NOT_PERMITTED, 'verb');
  const text = request.text;
  // The length bound is the schema's and the client enforces it; what is
  // refused here is a text that cannot be this write at all -- a start with
  // nothing to start, or a message whose text is not text.
  if (typeof text !== 'string' || text === '') return refuse(JOB_CODES.BAD_TEXT, 'text');

  const record = request.record;
  if (!isPlainObject(record)) return refuse(JOB_CODES.BAD_RECORD, 'record');
  if (record.verb !== verb) return refuse(JOB_CODES.BAD_RECORD, 'record.verb');
  const target = record.target;
  // P6.3's target is `{ run }` for a cancel and `{ repo }` alone for a launch
  // (`internal/mailbox`'s action record, O10): a start that named no repository
  // and a cancel that named no run are records this module cannot send, and the
  // rest of the record -- its id, its mode, its confirmation, the target's
  // characters -- is the schema's and is validated before the write.
  if (verb === 'launch') {
    if (typeof record.mode !== 'string' || record.mode === '') {
      return refuse(JOB_CODES.BAD_RECORD, 'record.mode');
    }
    if (!isPlainObject(target) || typeof target.repo !== 'string' || target.repo === '') {
      return refuse(JOB_CODES.BAD_RECORD, 'record.target');
    }
  } else if (!isPlainObject(target) || typeof target.run !== 'string' || target.run === '') {
    return refuse(JOB_CODES.BAD_RECORD, 'record.target');
  }
  return Object.freeze({ ok: true, verb, text, options: Object.freeze({ action: record }) });
}
