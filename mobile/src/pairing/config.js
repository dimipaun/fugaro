// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The installation's Firebase configuration, judged the same way twice
// (mobile plan, Task P6.8; the pairing plan's §4.4 and D199).
//
// **Why this is one module and not two checks.** The same four fields arrive
// twice: in the QR, as the **bootstrap** the phone needs to sign in at all,
// and inside the delivery envelope, as the **record** the app stores and uses
// afterwards (D199, §4.4). The phone must refuse a delivery whose contents
// disagree with what the QR said the project is, and it must hold the
// delivery's copy to the same rule it held the QR's -- so a payload cannot
// name a database the scan would have refused. Two implementations of "is this
// project `fp`'s database" would be two answers, and the whole of D199 is that
// there is one record.
//
// **Why the project decides the hosts.** `internal/infra/firebase.go`'s
// `CheckFirebaseOutputsFor` is the CLI's own rule for "this is project `fp`'s
// database": exactly `https://<fp>-default-rtdb.firebaseio.com`, or the
// regional `https://<fp>-default-rtdb.<region>.firebasedatabase.app`. The
// region is a free-form label and neither carrier holds it, so an exact
// allow-list cannot spell the second shape -- and `shared/url` implements no
// wildcards by design (D124's one validator, P0.3). The composition below is
// therefore two calls of that validator: the first with the project's legacy
// host, and, only when the first answers `host-not-allowed`, a second with the
// project's regional host derived from the candidate. The derivation is safe
// because of where that refusal sits: `host-not-allowed` is the validator's
// **last**, so by the time it is returned the string has passed every other
// check (https, no userinfo, no port, no encoded host, no punycode, no
// trailing dot) and `new URL(...).host` is the host a browser would dial.
//
// **The fields' own shapes are the CLI's, mirrored.** A project name is
// `internal/config`'s `ProjectNameRE` and a GCP project id is
// `internal/infra`'s `fpIDRE`, so a name the phone accepts is one the CLI
// could have written -- and, for the GCP project, one that cannot carry the
// `/`, `.` or `@` that would let a name spell a host this project does not
// own.

import { URL_REFUSAL, validateExternalUrl } from '../../../shared/url/index.js';

/** The configuration's four fields, in the CLI's names -- `DeliveryConfig`'s
 * JSON, and `pairQR`'s fields for the scanned copy. */
export const CONFIG_FIELDS = Object.freeze(['project', 'gcp_project', 'database_url', 'api_key']);

/** The longest the web API key may be. A Firebase web API key is 39
 * characters; 256 leaves room without letting the field be a container. */
export const API_KEY_MAX_LENGTH = 256;

/** How a configuration is refused. */
export const CONFIG_CODES = Object.freeze({
  NOT_AN_OBJECT: 'not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  MISSING_FIELD: 'missing-field',
  BAD_TYPE: 'bad-type',
  BAD_TEXT: 'bad-text',
  BAD_DATABASE_URL: 'bad-database-url',
});

// The Fugaro project's name: `internal/config`'s `ProjectNameRE`.
const PROJECT_NAME = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/;

// The GCP project id: `internal/infra`'s `fpIDRE`.
const GCP_PROJECT = /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/;

// Every field the CLI writes is printable ASCII: a project id is letters,
// digits and hyphens, a key is base64url, a URL is ASCII. Anything else is a
// look-alike -- a Cyrillic `а` in a project name, a zero-width space in a key
// -- so the rule is ASCII-only rather than `Printable` plus a list of
// exceptions. It is also why nothing here needs an NFC check.
const PRINTABLE_ASCII = /^[!-~]+$/;

// The regional database host's shape, for this configuration's project.
const REGIONAL_HOST = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?-default-rtdb\.[a-z0-9-]+\.firebasedatabase\.app$/;

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/**
 * Judge an installation's configuration: `{ project, gcpProject, databaseUrl,
 * apiKey }` with the field names already mapped, or a refusal.
 *
 * `origin` is the field name a refusal reports, and it is what lets the two
 * carriers say where the fault is: a refusal about the QR's copy names
 * `database_url` (the string in the payload a person can look at), and one
 * about the delivery's names `config.database_url` (the field inside the
 * sealed record).
 */
export function judgeConfig({ project, gcpProject, databaseUrl, apiKey }, origin) {
  const fields = [
    ['project', project, PROJECT_NAME, null],
    ['gcp_project', gcpProject, GCP_PROJECT, null],
    ['api_key', apiKey, PRINTABLE_ASCII, API_KEY_MAX_LENGTH],
  ];
  for (const [name, value, shape, max] of fields) {
    if (typeof value !== 'string') return refuse(CONFIG_CODES.BAD_TYPE, `${origin}${name}`);
    if (max !== null && value.length > max) return refuse(CONFIG_CODES.BAD_TEXT, `${origin}${name}`);
    if (!shape.test(value)) return refuse(CONFIG_CODES.BAD_TEXT, `${origin}${name}`);
  }
  if (typeof databaseUrl !== 'string') return refuse(CONFIG_CODES.BAD_TYPE, `${origin}database_url`);
  const url = validateDatabaseUrl(databaseUrl, gcpProject);
  if (!url.ok) return refuse(CONFIG_CODES.BAD_DATABASE_URL, `${origin}database_url`);
  return Object.freeze({
    ok: true,
    config: Object.freeze({
      project,
      gcpProject,
      databaseUrl: url.url,
      databaseHost: url.host,
      apiKey,
    }),
  });
}

/**
 * Whether `value` is a plain object whose fields are exactly `fields`, in the
 * CLI's own names, with none missing and none unknown -- or a refusal.
 *
 * The field set is judged before any field is: a carrier with a field this
 * build does not know is refused whole, because the fields it does know could
 * be a subset of a document written for another purpose. `origin` prefixes
 * every field name a refusal reports (`config.`), which is how a reader of the
 * delivery's refusal is told which of its two objects was at fault.
 *
 * The answer's `value` is a frozen shallow copy with the wire's own keys, so
 * the caller maps the names and this function never has to know them.
 */
export function judgeFieldSet(value, fields, origin) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return refuse(CONFIG_CODES.NOT_AN_OBJECT, origin === '' ? undefined : origin.replace(/\.$/, ''));
  }
  for (const key of Object.keys(value)) {
    if (!fields.includes(key)) return refuse(CONFIG_CODES.UNKNOWN_FIELD, `${origin}${key}`);
  }
  for (const field of fields) {
    if (!Object.prototype.hasOwnProperty.call(value, field)) return refuse(CONFIG_CODES.MISSING_FIELD, `${origin}${field}`);
  }
  return Object.freeze({ ok: true, value: Object.freeze({ ...value }) });
}

/**
 * Is `url` the database of project `gcpProject`, in one of the two spellings
 * the CLI accepts?
 *
 * It answers the validator's `{ ok: true, url, host }` -- the **candidate
 * verbatim**, never a re-serialisation, so the string that was checked is the
 * string the app hands to the database client -- or a refusal carrying the
 * validator's own code, which is what makes this module's refusals
 * diagnosable without a second vocabulary.
 */
export function validateDatabaseUrl(url, gcpProject) {
  if (typeof url !== 'string' || typeof gcpProject !== 'string') {
    return Object.freeze({ ok: false, code: URL_REFUSAL.NOT_A_STRING });
  }
  const legacy = `${gcpProject.toLowerCase()}-default-rtdb.firebaseio.com`;
  const first = validateExternalUrl(url, [legacy]);
  if (first.ok) return bareAuthority(first);
  if (first.code !== URL_REFUSAL.HOST_NOT_ALLOWED) return first;
  const host = hostOf(url);
  if (host === null || !REGIONAL_HOST.test(host)) return first;
  // The regional spelling is this project's only if its database label is the
  // project's own: `<project>-default-rtdb`. One character changed is another
  // database, however well formed it looks.
  if (!host.startsWith(`${gcpProject.toLowerCase()}-default-rtdb.`)) return first;
  return bareAuthority(validateExternalUrl(url, [host]));
}

/**
 * The hosts this configuration's project may name, for a caller that wants to
 * say why rather than only that it refused: the legacy spelling, and the
 * regional one when `url` is one. It is the same derivation
 * `validateDatabaseUrl` makes, so the app's diagnostics and its validation
 * cannot drift.
 */
export function databaseHostsFor(url, gcpProject) {
  const hosts = [`${String(gcpProject).toLowerCase()}-default-rtdb.firebaseio.com`];
  const host = hostOf(url);
  if (host !== null && REGIONAL_HOST.test(host)) hosts.push(host);
  return Object.freeze(hosts);
}

/** The host of a string that has already passed `shared/url`'s shape checks,
 * or `null` when it does not parse at all. */
function hostOf(url) {
  try {
    const parsed = new URL(url);
    return parsed.host === '' ? null : parsed.host.toLowerCase();
  } catch {
    return null;
  }
}

/**
 * The validator answers "may this string be opened", which a link may with a
 * path, a query or a fragment. A Realtime Database URL may not: it is the
 * database's address and nothing else, which is `internal/rtdb`'s
 * `ValidateURL` rule ("must be just the database's address, with no
 * credentials, path, query or fragment") and what stops a carrier from naming
 * a path the phone would then read as part of the database.
 *
 * The check is a length because the validator has already done the work: it
 * guarantees the string spells `https://`, that the authority carries no
 * userinfo, port, `%` or trailing dot, and it hands back the candidate
 * **verbatim** with its lower-cased host. So "the string is exactly `https://`
 * and the host" is exactly "there is nothing after the authority". It is
 * stricter than the Go rule in one place, deliberately: a bare trailing `/` is
 * refused here and accepted there, and the CLI writes neither.
 */
function bareAuthority(judged) {
  if (!judged.ok) return judged;
  if (judged.url.length !== 'https://'.length + judged.host.length) {
    return Object.freeze({ ok: false, code: URL_REFUSAL.NOT_A_URL });
  }
  return judged;
}
