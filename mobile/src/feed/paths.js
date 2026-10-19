// The four families the phone may read, and the gate every read passes
// (mobile plan, Task P3.2; D112 answered 2026-10-17, tension (b1)).
//
// **The grant is exactly four families and nothing else** (the plan's P2.1,
// "Resolved 2026-10-17 (tension (b1))"): `config/caps/**`, `spend/$day/**`,
// `config/kill/**`, and the phone's own mailbox (`outbox` and `heartbeat`).
// `agents/$slug/$run` has **no** read for a user claim and `outcomes/**` has
// none either, so the job list has no run titles -- and the reader must not
// *try*: a request the rules would deny is a request this module refuses
// before any port is called, which is what the plan's added test case asks for
// ("no request is ever made for `agents/**` or `outcomes/**`").
//
// **Why the gate is here and not in the reader.** The reader's paths are
// composed from a slug list and an account's uid, and a composed path is a
// string somebody can be made to compose wrongly: a slug carrying a `/`, a uid
// carrying `..`, a day read off a clock and formatted with a locale. So the
// composition and the judgement are one module, the judgement is by **exact
// shape** rather than by prefix, and the reader calls `request` for every path
// it is about to hand to the database.
//
// **What is deliberately not granted, though it is inside a granted family.**
// `config/caps/repos` (the parent of `$slug`) carries no grant -- the rules
// grant `config/caps/repos/$slug` -- so the phone **cannot enumerate
// repositories** from the database. That is the same limitation as the missing
// run titles, seen from the other side, and it is why the reader is given the
// owner's slug list rather than discovering one (D114's rule for accounts: "a
// list the owner maintains, not a discovery result"). `config/limits`
// (`budget.PathLimits`, a sibling of `caps` and not a child of it),
// `config/mode`, `config/policy/**`, `spend/$day/meta` and
// `spend/$day/runs/$slug/$run`'s siblings are refused for the same reason:
// only the shapes listed in `GRANTED` are granted.
//
// The day is the tree's own key -- `/spend/<day>` with `day` the UTC epoch day
// number (`internal/budget/model.go`'s `Day`/`DayKey`) -- and `dayDate` is the
// contract's `YYYY-MM-DD` spelling of it (`DayDate`), which is what the
// document carries.

/** The four granted families, in the order the reader subscribes to them. */
export const FAMILIES = Object.freeze(['config/caps', 'config/kill', 'spend', 'mailbox']);

/** The families the grant does **not** name and the reader must never ask for.
 * Named here so the refusal can say which one was attempted, and so a test can
 * assert the reader never composes one. */
export const DENIED_FAMILIES = Object.freeze(['agents', 'outcomes', 'runs', 'fugaro']);

/** The refusal codes. `DENIED_FAMILY` is the one the plan's test case matches
 * on: a path inside a family the grant does not name. */
export const PATH_CODES = Object.freeze({
  NOT_A_PATH: 'not-a-path',
  BAD_SEGMENT: 'bad-segment',
  BAD_DAY: 'bad-day',
  BAD_SLUG: 'bad-slug',
  BAD_UID: 'bad-uid',
  BAD_RUN: 'bad-run',
  NOT_GRANTED: 'not-granted',
  DENIED_FAMILY: 'denied-family',
});

/** A slug's shape, read off `internal/task/task.go`'s `Slug`: the readable part
 * is `[a-z0-9-]` and a slug is at most 63 characters. */
const SLUG_RE = /^[a-z0-9-]{1,63}$/;

/** A Firebase Auth uid, bounded and alphanumeric: it is what the rules compare
 * against `auth.uid` and what names the mailbox node. */
const UID_RE = /^[A-Za-z0-9]{1,128}$/;

/** A run id, as `internal/runstore` writes it. */
const RUN_RE = /^[A-Za-z0-9_-]{1,64}$/;

/** The epoch day, as `/spend/<day>` spells it: digits, no sign, no padding. */
const DAY_RE = /^(0|[1-9][0-9]{0,9})$/;

/** The longest a composed path may be, so nothing unbounded reaches a port. */
export const PATH_MAX = 512;

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field, extra) {
  return Object.freeze({ ok: false, code, field, ...extra });
}

/** The epoch day of an instant: `internal/budget/model.go`'s `Day`, which
 * floors a negative instant rather than truncating towards zero. */
export function dayKey(nowMs) {
  if (typeof nowMs !== 'number' || !Number.isFinite(nowMs)) throw new TypeError('dayKey needs an instant in ms');
  return Math.floor(nowMs / 86_400_000);
}

/** The day as `YYYY-MM-DD` (`internal/budget/model.go`'s `DayDate`), which is
 * what the contract's document carries in `day`. */
export function dayDate(day) {
  if (typeof day !== 'number' || !Number.isInteger(day)) throw new TypeError('dayDate needs an epoch day');
  return new Date(day * 86_400_000).toISOString().slice(0, 10);
}

/** The epoch day of a `YYYY-MM-DD` string, or `null` if it is not one. */
export function dayOf(date) {
  if (typeof date !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(date)) return null;
  const ms = Date.parse(`${date}T00:00:00.000Z`);
  if (!Number.isFinite(ms)) return null;
  // A string the Date parser accepted but that is not the same date back (a
  // day that does not exist in that month, say) is not a date.
  return dayDate(ms / 86_400_000) === date ? ms / 86_400_000 : null;
}

/** The paths of the two global cap nodes and one per repository. */
export function capsPaths(repositories) {
  return Object.freeze([
    'config/caps/global',
    'config/caps/defaults',
    ...repositories.map((slug) => `config/caps/repos/${slug}`),
  ]);
}

/** The kill switches: the project's and one per repository. */
export function killPaths(repositories) {
  return Object.freeze(['config/kill/global', ...repositories.map((slug) => `config/kill/repos/${slug}`)]);
}

/** The day's spend: the project's counter and one per repository. */
export function spendPaths(day, repositories) {
  return Object.freeze([`spend/${day}/global`, ...repositories.map((slug) => `spend/${day}/repos/${slug}`)]);
}

/** The phone's own mailbox: what it reads of the consumer's side of it. The
 * inbox carries no read grant -- the consumer, not the phone, collects what the
 * phone wrote -- so it is not here. */
export function mailboxPaths(uid) {
  return Object.freeze([`mailbox/${uid}/outbox`, `mailbox/${uid}/heartbeat`]);
}

/**
 * Is this path one of the granted shapes, exactly?
 *
 * By shape and not by prefix: `config/caps/repos` is *not* granted (only
 * `config/caps/repos/$slug` is), `spend/1/global/extra` is not, and a path
 * with an empty segment, a `..`, a `.` or a percent-escaped slash is not.
 */
export function isGranted(path) {
  return request(path).ok;
}

/**
 * The gate. Returns `{ ok: true, path }` for a path the grant names, and a
 * refusal otherwise, with `family` set to the top-level family the path named
 * so a caller can tell "the grant does not cover this" from "this is not a
 * path at all".
 *
 * The reader calls this for **every** path before it hands it to the port, so
 * a denied family is refused here rather than by the database: a read the
 * rules would deny is a read that must never be attempted, and the test that
 * pins it watches the port, not the refusal.
 */
export function request(path) {
  if (typeof path !== 'string' || path.length === 0) return refuse(PATH_CODES.NOT_A_PATH, 'path');
  if (path.length > PATH_MAX) return refuse(PATH_CODES.NOT_A_PATH, 'path');
  const family = path.split('/')[0];
  if (path.startsWith('/')) return refuse(PATH_CODES.NOT_A_PATH, 'path', { family });
  const segments = path.split('/');
  if (segments.some((segment) => segment.length === 0 || segment === '.' || segment === '..')) {
    return refuse(PATH_CODES.BAD_SEGMENT, 'path', { family });
  }
  if (DENIED_FAMILIES.includes(family)) {
    return refuse(PATH_CODES.DENIED_FAMILY, 'path', { family });
  }

  const [, a, b, c, d] = segments;
  if (segments.length === 3 && family === 'config' && a === 'caps' && (b === 'global' || b === 'defaults') && c === undefined) {
    return Object.freeze({ ok: true, path });
  }
  if (segments.length === 3 && family === 'config' && a === 'kill' && b === 'global') {
    return Object.freeze({ ok: true, path });
  }
  if (segments.length === 4 && family === 'config' && a === 'caps' && b === 'repos') {
    return SLUG_RE.test(c) ? Object.freeze({ ok: true, path }) : refuse(PATH_CODES.BAD_SLUG, 'path', { family });
  }
  if (segments.length === 4 && family === 'config' && a === 'kill' && b === 'repos') {
    return SLUG_RE.test(c) ? Object.freeze({ ok: true, path }) : refuse(PATH_CODES.BAD_SLUG, 'path', { family });
  }
  if (segments.length === 3 && family === 'spend' && b === 'global') {
    return DAY_RE.test(a) ? Object.freeze({ ok: true, path }) : refuse(PATH_CODES.BAD_DAY, 'path', { family });
  }
  if (segments.length === 4 && family === 'spend' && b === 'repos') {
    if (!DAY_RE.test(a)) return refuse(PATH_CODES.BAD_DAY, 'path', { family });
    return SLUG_RE.test(c) ? Object.freeze({ ok: true, path }) : refuse(PATH_CODES.BAD_SLUG, 'path', { family });
  }
  if (segments.length === 5 && family === 'spend' && b === 'runs') {
    if (!DAY_RE.test(a)) return refuse(PATH_CODES.BAD_DAY, 'path', { family });
    if (!SLUG_RE.test(c)) return refuse(PATH_CODES.BAD_SLUG, 'path', { family });
    return RUN_RE.test(d) ? Object.freeze({ ok: true, path }) : refuse(PATH_CODES.BAD_RUN, 'path', { family });
  }
  if (segments.length === 3 && family === 'mailbox') {
    if (!UID_RE.test(a)) return refuse(PATH_CODES.BAD_UID, 'path', { family });
    return b === 'outbox' || b === 'heartbeat'
      ? Object.freeze({ ok: true, path })
      : refuse(PATH_CODES.NOT_GRANTED, 'path', { family });
  }
  return refuse(PATH_CODES.NOT_GRANTED, 'path', { family });
}

/** The family a path names, granted or not: `path.split('/')[0]`, or `null`. */
export function familyOf(path) {
  if (typeof path !== 'string' || path.length === 0) return null;
  return path.split('/')[0];
}

/** The slugs of a caller's repository list, judged once. A list is
 * owner-typed (D114's rule for accounts, applied here): a slug this module
 * cannot compose a path from is refused rather than dropped, because a list
 * that quietly lost an entry is a repository the phone would never show. */
export function checkRepositories(repositories) {
  if (!Array.isArray(repositories)) return refuse(PATH_CODES.BAD_SLUG, 'repositories');
  const seen = new Set();
  for (const slug of repositories) {
    if (typeof slug !== 'string' || !SLUG_RE.test(slug)) return refuse(PATH_CODES.BAD_SLUG, 'repositories');
    if (seen.has(slug)) return refuse(PATH_CODES.BAD_SLUG, 'repositories');
    seen.add(slug);
  }
  return Object.freeze({ ok: true, repositories: Object.freeze([...repositories]) });
}
