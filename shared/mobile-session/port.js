// The mobile session port (mobile identity and pairing plan, Task C2).
//
// **What the port is.** One paired project's session, as every surface in the
// app may see it: the project's opaque id and endpoint, the role the roster
// resolved, the three policy values C1's session response carries
// (`internal/mobilesession/contract.go`), an `idToken()` that refreshes, a
// `signMessage(bytes)` that signs with the device key (O6), and the one event a
// surface may rely on, `signedOut`. M3 implements it in
// `mobile/src/session/index.js`; J2's running-jobs view, J3's confirmation
// policy, J4's verbs, X1's transcript, Z1's gauge and J5b's message signing are
// written against it, which is why the plan breaks M3's edge to each of them
// into this task; `./stub.js` is the fixed fake those surfaces mount on until
// I2 deletes it.
//
// **What the port is not.** It is a shape and a validator, not an
// implementation: no transport, no refresh loop, no clock, no store, no key and
// no import at all. C2 depends on nothing, and that is what makes it a contract
// rather than the first consumer's convenience -- a port written next to its
// implementation would carry whatever that implementation happened to expose.
// What M3 owes beyond the members below is stated in prose here and pinned by
// `docs/mobile-identity-pairing/session-port.md`: `idToken()` refreshes through
// the project when the held token is near its hour, `signMessage(bytes)` signs
// with the device key and the key never leaves the session, and after
// `signedOut` both refuse rather than return a stale credential.
//
// **A role here is a display claim, never an authorisation** (F14, D2791). The
// roster is the one source of membership and role, the device node's role is
// rewritten from it at every refresh, and what a role may do is enforced at the
// consumer with `internal/access` (`ActionLaunch`, `ActionCancel`) -- never by
// a role carried in a message. The port carries the roster's three role names
// so a surface can hide what its session cannot do, and carries nothing that
// could be read as a grant.
//
// **The member list is closed, and that is the security property.** A session
// that carried its own refresh token -- or the Firebase refresh token D2783
// forbids the app to keep -- would hand every surface a credential it has no
// business holding, and a surface is exactly where a token gets logged. So an
// unknown member is refused, not ignored: `assertSessionPort` fails closed on
// one, and the refresh token stays in M3's store, reached only through
// `idToken()`.
//
// **Fail closed, everywhere.** A value that is not a plain object, a member
// outside the list, a missing member, a member of the wrong kind, a project id
// or endpoint that cannot be one, a role outside the three, and a policy field
// or policy value outside its shape are each a refusal with its own code, and a
// refusal names the member and never the value -- a session's fields include
// things that must not travel into a log line. `assertSessionPort` returns the
// value it was given, so a surface can mount with
// `const session = assertSessionPort(raw)`.
//
// **The endpoint must be `https`, and that is the port's own narrow rule.** The
// full validation of a project endpoint is the pairing payload's (M2), which
// parses a QR as hostile input; what this module refuses is the shape that
// cannot be a project endpoint at all -- `https` with a host behind it, and
// nothing else. The `https` half is the one that matters: the endpoint is where
// a device-bound refresh token is presented, and a credential sent over a
// cleartext scheme is a credential disclosed.

/** The three roles the roster resolves, which are the names `internal/access` uses. */
export const SESSION_ROLES = Object.freeze({
  VIEWER: 'viewer',
  OPERATOR: 'operator',
  ADMIN: 'admin',
});

/**
 * The one event a surface may rely on. It fires once, when the project's
 * session can no longer be recovered -- M3's case is a refresh whose token was
 * rotated out by another device, which fails and signs the project out first
 * (D2794). A surface listening for it stops using the session; there is no
 * "signed back in" event, because a new session is a new port value.
 */
export const SESSION_EVENTS = Object.freeze({
  SIGNED_OUT: 'signedOut',
});

/**
 * The policy values C1's session response carries to the phone, which are the
 * only ones a surface may read: J3's confirmation policy reads `costly_modes`
 * (a start in one of them needs the read-back, and with `biometric_on_costly`
 * the biometric path too), Z2's auto-compaction reads
 * `compaction_threshold`, and the app's one collection switch reads
 * `crash_reports` (D731, D738, the app's name for the project policy's top-level
 * `mobile_crash_reports` key). The names are this list's own, fixed here so the
 * session response and the policy file cannot drift into two spellings of one value.
 */
export const SESSION_POLICY_FIELDS = Object.freeze([
  'costly_modes',
  'biometric_on_costly',
  'compaction_threshold',
  'crash_reports',
]);

/** The longest opaque project id, in characters: an id is short by construction. */
export const MAX_PROJECT_ID_LENGTH = 128;

/** The longest endpoint, in characters: `shared/url`'s own bound for a link. */
export const MAX_ENDPOINT_LENGTH = 2048;

/** The most costly modes a project may name: the rigor modes are a handful. */
export const MAX_COSTLY_MODES = 32;

/** The longest mode name, in characters: a rigor mode is a short word. */
export const MAX_MODE_LENGTH = 64;

/**
 * The refusal codes, one per class of refusal. A caller switches on these, and
 * a test asserts the exact code, because "it threw" is not the property the
 * plan asks for.
 */
export const PORT_CODES = Object.freeze({
  /** The value handed to `assertSessionPort` is not a plain object at all. */
  NOT_A_SESSION: 'not-a-session',
  /** A member the contract does not name is present. Fail closed, not ignore. */
  UNKNOWN_MEMBER: 'unknown-member',
  /** A member the contract names is absent. */
  MISSING_MEMBER: 'missing-member',
  /** A member is present as the wrong kind of value. */
  BAD_TYPE: 'bad-type',
  /** A project id that is empty, padded, non-ASCII or oversize. */
  BAD_PROJECT_ID: 'bad-project-id',
  /** An endpoint that does not spell `https://`, or is padded or oversize. */
  BAD_ENDPOINT: 'bad-endpoint',
  /** A role the roster does not have. */
  UNKNOWN_ROLE: 'unknown-role',
  /** A policy field the contract does not name. */
  UNKNOWN_POLICY_FIELD: 'unknown-policy-field',
  /** A policy value outside its shape: unsigned, unusable or out of range. */
  BAD_POLICY_VALUE: 'bad-policy-value',
  /** A surface subscribed to an event the port does not carry (thrown by `on`). */
  UNKNOWN_EVENT: 'unknown-event',
  /** A surface subscribed something that is not a listener (thrown by `on`). */
  BAD_LISTENER: 'bad-listener',
  /** A session was used after `signedOut` (rejected by `idToken`/`signMessage`). */
  SIGNED_OUT: 'signed-out',
});

/**
 * A refusal. `code` is one of `PORT_CODES` and `member` names the field that was
 * refused (`policy.costly_modes`), so a caller can act on the code and point at
 * the defect without the message carrying the value -- a session holds tokens,
 * and a refusal that echoed one would be a path for it to travel.
 */
export class SessionPortError extends Error {
  constructor(code, member = '$') {
    super(`${code} at ${member}`);
    this.name = 'SessionPortError';
    this.code = code;
    this.member = member;
  }
}

// The members, exactly, with the kind each must be. This table is the port: a
// member is added here or it is not a member, and `SESSION_MEMBERS` below is
// derived from it rather than written twice.
const MEMBER_KINDS = Object.freeze({
  project_id: 'string',
  endpoint: 'string',
  role: 'role',
  policy: 'policy',
  idToken: 'function',
  signMessage: 'function',
  on: 'function',
});

/** The members every session carries, in the order the table above names them. */
export const SESSION_MEMBERS = Object.freeze(Object.keys(MEMBER_KINDS));

/** An id, an endpoint and a mode are printable ASCII with no space. */
const PRINTABLE_RE = /^[\x21-\x7e]+$/;

function refuse(code, member) {
  throw new SessionPortError(code, member);
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function checkProjectId(id) {
  if (typeof id !== 'string') refuse(PORT_CODES.BAD_TYPE, 'project_id');
  if (id.length > MAX_PROJECT_ID_LENGTH || !PRINTABLE_RE.test(id)) refuse(PORT_CODES.BAD_PROJECT_ID, 'project_id');
}

// The scheme, and then a host: `https://` alone, or `https:///a`, is a string
// that names no project, and the host's own rules (labels, no userinfo, no IP
// literal) belong to the pairing payload's parser (M2), which reads a QR as
// hostile input.
const SCHEME = 'https://';

function checkEndpoint(endpoint) {
  if (typeof endpoint !== 'string') refuse(PORT_CODES.BAD_TYPE, 'endpoint');
  const host = endpoint.startsWith(SCHEME) ? endpoint.slice(SCHEME.length) : '';
  if (host.length === 0 || host.startsWith('/') || endpoint.length > MAX_ENDPOINT_LENGTH || !PRINTABLE_RE.test(endpoint)) {
    refuse(PORT_CODES.BAD_ENDPOINT, 'endpoint');
  }
}

function checkRole(role) {
  if (typeof role !== 'string') refuse(PORT_CODES.BAD_TYPE, 'role');
  if (!Object.values(SESSION_ROLES).includes(role)) refuse(PORT_CODES.UNKNOWN_ROLE, 'role');
}

function checkPolicy(policy) {
  if (!isPlainObject(policy)) refuse(PORT_CODES.BAD_TYPE, 'policy');
  // The same check the top level gets, for the same reason: a member the
  // contract does not name is refused whether it is enumerable or not, and a
  // symbol-keyed one -- which has no name to report -- is refused as the policy
  // itself rather than waved through.
  if (Object.getOwnPropertySymbols(policy).length > 0) refuse(PORT_CODES.UNKNOWN_POLICY_FIELD, 'policy');
  for (const field of Object.getOwnPropertyNames(policy)) {
    if (!SESSION_POLICY_FIELDS.includes(field)) refuse(PORT_CODES.UNKNOWN_POLICY_FIELD, `policy.${field}`);
  }
  for (const field of SESSION_POLICY_FIELDS) {
    if (!Object.hasOwn(policy, field)) refuse(PORT_CODES.MISSING_MEMBER, `policy.${field}`);
  }

  const modes = policy.costly_modes;
  if (!Array.isArray(modes) || modes.length > MAX_COSTLY_MODES) refuse(PORT_CODES.BAD_POLICY_VALUE, 'policy.costly_modes');
  const seen = new Set();
  for (const mode of modes) {
    if (typeof mode !== 'string' || mode.length > MAX_MODE_LENGTH || !PRINTABLE_RE.test(mode) || seen.has(mode)) {
      refuse(PORT_CODES.BAD_POLICY_VALUE, 'policy.costly_modes');
    }
    seen.add(mode);
  }

  if (typeof policy.biometric_on_costly !== 'boolean') refuse(PORT_CODES.BAD_POLICY_VALUE, 'policy.biometric_on_costly');

  // The crash-report switch is a boolean and never absent: an absent member is
  // refused as MISSING_MEMBER above, and the phone must not read a missing
  // value as "on" -- nothing is sent before the switch has been read (D734).
  if (typeof policy.crash_reports !== 'boolean') refuse(PORT_CODES.BAD_POLICY_VALUE, 'policy.crash_reports');

  // A fraction of the session's context window, or null when the project sets
  // no threshold: the phone renders it as a percentage, so the unit is fixed
  // here rather than left to whichever surface formats it first.
  const threshold = policy.compaction_threshold;
  if (threshold !== null && (typeof threshold !== 'number' || !Number.isFinite(threshold) || threshold <= 0 || threshold >= 1)) {
    refuse(PORT_CODES.BAD_POLICY_VALUE, 'policy.compaction_threshold');
  }
}

/**
 * Answer whether `value` is a session this port describes: the members above,
 * each of the kind above, and no others. On refusal a `SessionPortError` is
 * thrown with the code and the member; on success the value itself is returned,
 * unchanged, so a surface mounts with `assertSessionPort(raw)` and never holds a
 * wrapper the port does not describe.
 *
 * The value is read once. What the functions later return is not this module's
 * to check: `idToken()` and `signMessage(bytes)` are promises, and a promise
 * that rejects is the implementation's answer, not a broken port.
 */
export function assertSessionPort(value) {
  if (!isPlainObject(value)) refuse(PORT_CODES.NOT_A_SESSION, '$');
  // A symbol-keyed member is a member: spreading would carry it, `Object.keys`
  // would not see it, and a value smuggled past this check is the one thing this
  // function exists to stop.
  if (Object.getOwnPropertySymbols(value).length > 0) refuse(PORT_CODES.UNKNOWN_MEMBER, '$');

  for (const member of Object.getOwnPropertyNames(value)) {
    if (!Object.hasOwn(MEMBER_KINDS, member)) refuse(PORT_CODES.UNKNOWN_MEMBER, member);
  }
  for (const member of SESSION_MEMBERS) {
    if (!Object.hasOwn(value, member)) refuse(PORT_CODES.MISSING_MEMBER, member);
  }

  checkProjectId(value.project_id);
  checkEndpoint(value.endpoint);
  checkRole(value.role);
  checkPolicy(value.policy);

  for (const member of SESSION_MEMBERS) {
    if (MEMBER_KINDS[member] === 'function' && typeof value[member] !== 'function') refuse(PORT_CODES.BAD_TYPE, member);
  }

  return value;
}
