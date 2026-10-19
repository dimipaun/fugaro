// The feed reader: the four granted families, subscribed, and the document
// they add up to (mobile plan, Task P3.2; D112's answer, tension (b1)).
//
// **This is the module the grant's fence is about.** The plan's added test case
// for P3.2 is "no request is ever made for `agents/**` or `outcomes/**`", and
// that is a statement about *requests*, not about refusals: a read the rules
// would deny must never be attempted. So every path goes through `paths.js`'s
// gate **before** the port is called, and the test proves it by watching a fake
// port that records every path it is handed -- a denied path would be a
// recorded request, not a returned error.
//
// **The phone reads nodes, not a child's stdout.** D112's answer is direct
// RTDB reads, so the document is assembled here from the granted nodes and then
// judged by `document.js`, which is the contract's shape. That is the one place
// P3.2 departs from the console's transport, and it is a departure the plan's
// resolution names: D124 is kept as the reader rules and the document shapes,
// not as the `watch --json` transport.
//
// **What the reader does not do.** It does not discover repositories: the
// grant is per `$slug` and not on `config/caps/repos` itself, so the list is
// the owner's (`paths.js`). It does not read a run, so no run title is here. It
// does not decide presence: the heartbeat node is read and passed on, and what
// staleness means is check 84 (P4.2)'s.
//
// **The connection is the phone's own.** The CLI's `State.conn` is "the worst
// of the four streams": a permission error, any other error, a stream that has
// not answered yet, and `StaleAfter`. The phone's four streams are its granted
// paths, so the same rule applies to them in the same order -- a refusal, then
// a read that failed, then the paths that have not answered, then the oldest
// node that has -- and the `age_seconds` that comes out is what `view.js` reads
// as silence. `State.conn` never leaves `stale` for a silence, and neither does
// this: `watch.LostAfter` is a property of a **run**, and the phone has none.
// The reasons are static sentences: an error message from the database is not
// something this app puts on a screen.

import { isAccountId, validateAccount } from '../accounts/account.js';
import { validateDocument } from './document.js';
import { BurnSampler, recompute } from './recompute.js';
import { MAX_INSTANT_MS, readCaps, readCounters, readKill } from './nodes.js';
import {
  capsPaths,
  checkRepositories,
  dayDate,
  dayKey,
  killPaths,
  mailboxPaths,
  request,
  spendPaths,
} from './paths.js';
import { FeedView, STALE_AFTER_MS } from './view.js';

/** The reader's refusals. */
export const READER_CODES = Object.freeze({
  BAD_PORT: 'bad-port',
  BAD_ACCOUNT: 'bad-account',
  BAD_REPOSITORIES: 'bad-repositories',
  BAD_NOW: 'bad-now',
  TOO_MANY_PATHS: 'too-many-paths',
  NOT_GRANTED: 'not-granted',
  NOT_OPEN: 'not-open',
  BAD_LISTENER: 'bad-listener',
  ALREADY_STARTED: 'already-started',
  NOT_STARTED: 'not-started',
});

/** The most paths one account will hold open. Every path is a listener, and a
 * repository list is owner-typed: a bound is what keeps a mistake in that list
 * from being an unbounded number of sockets. */
export const MAX_PATHS = 64;

/** The static reasons a connection carries. Never a raw error: a database's
 * message is not this app's sentence. `refused` and `failed` are the two ways
 * a read can go wrong, and they are different facts: `State.conn` calls a
 * **permission** error `refused` and every other error a connection that is
 * not up, and a phone that called a dropped socket "access refused" would send
 * its owner to look at a token that is fine. */
export const CONNECTION_REASONS = Object.freeze({
  refused: 'access refused',
  failed: 'the read failed',
  connecting: 'connecting',
  quiet: 'no update',
  polling: 'polling',
});

/**
 * The error codes a Realtime Database client uses for a **rules** refusal. The
 * Firebase SDK spells it `PERMISSION_DENIED`; the Admin SDK's `ErrPermission`
 * is what `State.conn` matches with `errors.Is`, and this is the phone's side
 * of the same distinction.
 */
const PERMISSION_CODES = Object.freeze(['permission-denied', 'permission_denied']);

/**
 * Is this port error a permission refusal -- the one error that means "this
 * token may not read this node"?
 *
 * Everything else is a connection that is not up: a dropped socket, a timeout,
 * a DNS failure, a client that has not authenticated yet. Those are not
 * credential problems, and this app must not report them as one. The
 * judgement is deliberately narrow -- an error object with a `code` that names
 * permission and nothing else -- because the cost of the two mistakes is not
 * symmetric: calling a network failure "access refused" sends the owner to
 * check a token that is fine, and calling a refusal a network failure hides
 * the one fact that says which node the token cannot read.
 */
export function isPermissionError(error) {
  if (error === null || typeof error !== 'object') return false;
  const code = error.code;
  if (typeof code !== 'string') return false;
  return PERMISSION_CODES.includes(code.trim().toLowerCase());
}

/**
 * A counter node's three figures, or three `null`s when the node is there but
 * is not a counter at all.
 *
 * `readCounters` refuses a value that is not an object -- a node holding text,
 * a number, an array. A refusal must not become a `0` here: §10.13 rule 5 says a
 * missing field is `unavailable` and never `0`, and "the day has counted
 * nothing" is exactly the claim a `0` would make about a node this reader could
 * not read. So the three figures come back `null`, which `document.js` judges
 * into `unavailable` figures, and the rest of the screen -- the other nodes, the
 * other repositories -- is untouched.
 */
function countersOf(node) {
  const read = readCounters(node);
  if (read.ok) return read.counters;
  return Object.freeze({ counted: null, spent: null, notional: null });
}

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/**
 * A read kill node as the **document** carries it: `{ by, at, reason }`, or
 * `null` for "not killed".
 *
 * The tree's node has an `on` boolean; the document does not -- `jsonKill`
 * (`internal/watch/json.go`) returns a nil `*JSONKill` when the switch is off,
 * so **presence is the on-ness** and the contract's `kill` field is "null = not
 * killed" (§2). Handing the node's `on` through would be an unknown field, and
 * `document.js` would refuse the whole document for it -- which is exactly the
 * kind of mistake this function exists to make impossible.
 */
function killOfDocument(kill) {
  if (kill === null || kill === undefined) return null;
  return Object.freeze({ by: kill.by, at: kill.at, reason: kill.reason });
}

function isNow(nowMs) {
  if (typeof nowMs !== 'number' || !Number.isFinite(nowMs)) return false;
  // A finite number is not necessarily an instant: `new Date(1e300)` is an
  // invalid date, and `toISOString()` on it **throws**. The document carries
  // `now` as RFC 3339, so an instant this app cannot render is not an instant
  // it can assemble a document from -- and refusing it here is what keeps a
  // bad clock from being a feed that never assembles again.
  return Math.abs(nowMs) <= MAX_INSTANT_MS;
}

/** The slug of a granted spend or cap path, or `null`: `spend/<day>/repos/<slug>`,
 * `config/caps/repos/<slug>`, `config/kill/repos/<slug>`. */
function slugOf(path, family, kind) {
  const segments = path.split('/');
  if (family === 'spend' && segments.length === 4 && segments[2] === 'repos') return segments[3];
  if (family === 'config' && segments.length === 4 && segments[1] === kind && segments[2] === 'repos') return segments[3];
  return null;
}

/**
 * The reader of one account's feed.
 *
 * One instance per open account, built by the account store, and its listeners
 * registered with that account's `ResourceRegistry` so a switch tears them
 * down (D114's "no cross-leakage"). The registry is how a subscription cannot
 * outlive the account it was opened for; `stop()` is for the app's own
 * shutdown.
 */
export class FeedReader {
  #port;
  #registry;
  #account;
  #repositories;
  #project;
  #now;
  #view;
  #sampler;
  #nodes = new Map();
  #delivered = new Set();
  #lastEvent = new Map();
  /** The paths whose read was **refused** (a permission error), and the paths
   * whose read **failed** for any other reason. Two sets, because they are two
   * facts with two sentences and two different things for the owner to do. */
  #refused = new Set();
  #errored = new Set();
  #handles = new Map();
  #day = null;
  #paths = Object.freeze([]);
  #started = false;

  constructor({ port, account, repositories, registry, project, now = () => Date.now(), view, sampler } = {}) {
    this.#port = port;
    this.#registry = registry;
    this.#account = account;
    this.#repositories = repositories;
    this.#project = project ?? account?.fugaroProject ?? null;
    this.#now = now;
    this.#view = view ?? new FeedView();
    this.#sampler = sampler ?? new BurnSampler();
  }

  /** The account this reader belongs to. */
  get accountId() {
    return this.#account?.id ?? null;
  }

  /** The paths this reader is holding open. */
  get paths() {
    return this.#paths;
  }

  /** The day this reader is subscribed to, as the tree spells it. */
  day() {
    return this.#day === null ? null : dayDate(this.#day);
  }

  /**
   * The paths of one day's feed, judged. Nothing here touches the port: a
   * caller can ask what would be requested before anything is.
   */
  plan(nowMs) {
    if (!isNow(nowMs)) return refuse(READER_CODES.BAD_NOW, 'nowMs');
    // The account is judged **as the account module judges it**, and before a
    // single path is composed: its `authUid` is what names the mailbox, and a
    // reader that judged only the id would compose `mailbox/undefined/outbox`
    // out of an account that has no uid -- a path the gate would then **grant**,
    // because `undefined` has exactly the shape of a uid, and a node this
    // reader would subscribe to. The refusal carries the field at fault, so a
    // missing uid is named as one.
    const account = validateAccount(this.#account);
    if (!account.ok) return refuse(READER_CODES.BAD_ACCOUNT, account.field ?? 'accountId');
    const repositories = checkRepositories(this.#repositories);
    if (!repositories.ok) return refuse(READER_CODES.BAD_REPOSITORIES, 'repositories');
    const uid = account.account.authUid;
    const slugs = repositories.repositories;
    const paths = [
      ...capsPaths(slugs),
      ...killPaths(slugs),
      ...spendPaths(dayKey(nowMs), slugs),
      ...mailboxPaths(uid),
    ];
    if (paths.length > MAX_PATHS) return refuse(READER_CODES.TOO_MANY_PATHS, 'paths');
    // The gate, for every path, before anything else can see it. A composed
    // path that the grant does not name is a programming error in this module,
    // and it is caught here rather than by the database.
    for (const path of paths) {
      const judged = request(path);
      if (!judged.ok) return refuse(READER_CODES.NOT_GRANTED, path);
    }
    return Object.freeze({ ok: true, paths: Object.freeze(paths), day: dayKey(nowMs) });
  }

  /**
   * Subscribe to the plan. Every listener is registered with the account's
   * registry, and a registry that refuses one is a start that stops: a
   * half-subscribed reader is a leak with no owner.
   */
  start(nowMs) {
    if (this.#started) return refuse(READER_CODES.ALREADY_STARTED, 'start');
    // The account, the port and the open account are judged **before** the
    // first subscription: a reader that subscribed and then discovered it could
    // not register the handle would have opened a socket nothing will ever
    // close. The account itself is judged in `plan()`, which runs next.
    if (!isAccountId(this.accountId)) return refuse(READER_CODES.BAD_ACCOUNT, 'accountId');
    if (!isPort(this.#port)) return refuse(READER_CODES.BAD_PORT, 'port');
    if (this.#registry !== undefined && this.#registry !== null && !this.#openAccount()) {
      return refuse(READER_CODES.NOT_OPEN, 'accountId');
    }
    const planned = this.plan(nowMs);
    if (!planned.ok) return planned;
    // Before the first subscription, not after: a Realtime Database listener
    // delivers the node's current value the moment it is attached, so the day
    // and the plan must be readable while that first value arrives.
    this.#paths = planned.paths;
    this.#day = planned.day;
    const subscribed = [];
    for (const path of planned.paths) {
      const handle = this.#subscribe(path);
      if (handle === null) {
        for (const stop of subscribed) this.#stopOne(stop);
        return refuse(READER_CODES.BAD_LISTENER, path);
      }
      const registered = this.#registry === undefined || this.#registry === null
        ? Object.freeze({ ok: true })
        : this.#registry.add(this.accountId, handle);
      if (!registered.ok) {
        this.#stopOne(handle);
        for (const stop of subscribed) this.#stopOne(stop);
        return refuse(READER_CODES.NOT_OPEN, path);
      }
      subscribed.push(handle);
      this.#handles.set(path, handle);
    }
    this.#started = true;
    // Assemble once, before anything has arrived: a caller can ask what the
    // feed is doing from the first moment, and the answer is "connecting" --
    // not a document, and not a silence that has not happened yet.
    this.#assemble(nowMs);
    return Object.freeze({ ok: true, paths: planned.paths, day: planned.day, listeners: subscribed.length });
  }

  /** Is the account this reader belongs to open? A registry that cannot answer
   * is not one that can refuse a listener, so the reader proceeds. */
  #openAccount() {
    if (typeof this.#registry.isOpen !== 'function') return true;
    return this.#registry.isOpen(this.accountId);
  }

  /**
   * One node's value, as the port delivers it.
   *
   * A value is taken only for a path this reader actually asked for: it must be
   * one of the granted shapes **and** one of the paths in this reader's plan.
   * The gate alone is not enough. `spend/<a day the reader is not on>/global`
   * and `spend/<day>/runs/<slug>/<run>` are granted by shape and are not in the
   * plan -- no listener was ever attached to them -- and a value for one of
   * them would be stored, counted as a delivery and recorded in `#lastEvent`,
   * which is the map `#connection` reads to decide whether the plan has answered
   * at all. A path nobody asked for cannot answer for the plan, so it is refused
   * and **not stored**, whatever it contains.
   */
  ingest(path, value, nowMs) {
    if (!isNow(nowMs)) return refuse(READER_CODES.BAD_NOW, 'nowMs');
    const judged = request(path);
    if (!judged.ok) return refuse(READER_CODES.NOT_GRANTED, typeof path === 'string' ? path : 'path');
    if (!this.#paths.includes(path)) return refuse(READER_CODES.NOT_GRANTED, path);
    this.#nodes.set(path, value);
    this.#delivered.add(path);
    this.#lastEvent.set(path, nowMs);
    this.#refused.delete(path);
    this.#errored.delete(path);
    this.#sample(path, value, nowMs);
    return this.#assemble(nowMs);
  }

  /** A read the database **refused**: a permission error. The connection says
   * so until that path delivers a value again. */
  refused(path, nowMs) {
    return this.#broken(path, this.#refused, nowMs);
  }

  /**
   * A read that failed for any other reason -- the port's own error, handed in
   * as the port hands it in.
   *
   * `State.conn` calls a permission error `refused` and everything else a
   * connection that is not up, and this is the phone's same split: only
   * `isPermissionError` reaches `refused`. A dropped socket, a timeout and a
   * client that is still authenticating are all "the read failed", which is a
   * different sentence on the screen and a different thing for the owner to
   * do about it.
   */
  failed(path, error, nowMs) {
    if (isPermissionError(error)) return this.refused(path, nowMs);
    return this.#broken(path, this.#errored, nowMs);
  }

  #broken(path, into, nowMs) {
    if (typeof path !== 'string') return refuse(READER_CODES.NOT_GRANTED, 'path');
    if (!this.#paths.includes(path)) return refuse(READER_CODES.NOT_GRANTED, path);
    into.add(path);
    return this.#assemble(isNow(nowMs) ? nowMs : this.#now());
  }

  /**
   * Move to the day the clock is in, if it has moved.
   *
   * §10.7: the rollover is a break, and the burn window restarts with it
   * (`SetDay` empties the windows), so the new day's rate is unknown for
   * `BurnWarmup` rather than zero. The old day's spend nodes are dropped: the
   * store holds the day being shown, not a history of days.
   *
   * **The rollover is a break in the view as well as in the store.** Moving to
   * the new day and leaving the screen alone would leave yesterday's document
   * on it under today's date -- `snapshot()` reports the reader's own `#day`, so
   * the date would say today while the figures said yesterday, and the view
   * would still call them a measurement because the old day's counter had
   * arrived. §10.7 forbids exactly that. The assembly below is the same one a
   * delivery would trigger, with `measured` false, so the new day starts as the
   * gap it is.
   */
  refresh(nowMs) {
    if (!this.#started) return refuse(READER_CODES.NOT_STARTED, 'refresh');
    // The clock is judged exactly as it is at every other entry point: a
    // rollover decided from a `nowMs` that is not an instant would move the
    // reader to a day nobody named.
    if (!isNow(nowMs)) return refuse(READER_CODES.BAD_NOW, 'nowMs');
    const day = dayKey(nowMs);
    if (day === this.#day) return Object.freeze({ ok: true, dayChanged: false, day });
    // The paths are cleared by walking the **plan**, not the store. A spend path
    // that was refused or that failed its read is in `#refused`/`#errored`
    // without ever having delivered, so it is in no `#nodes` key -- and a stale
    // entry there is permanent: the path is not re-subscribed for the new day
    // (its name carries the day), so no delivery can ever clear it, and
    // `#connection` would report the feed refused forever after one permission
    // error on one path. `#paths` is every path this reader asked for, which is
    // exactly the set that must be forgotten.
    for (const path of this.#paths) {
      if (!path.startsWith('spend/')) continue;
      this.#nodes.delete(path);
      this.#delivered.delete(path);
      this.#lastEvent.delete(path);
      this.#refused.delete(path);
      this.#errored.delete(path);
    }
    this.#sampler.reset();
    const planned = this.plan(nowMs);
    if (!planned.ok) return planned;
    // The spend paths are the ones that moved. The cap, kill and mailbox paths
    // are the same nodes on the new day, so they are not re-subscribed and
    // their values are kept.
    const wanted = planned.paths.filter((path) => path.startsWith('spend/'));
    const held = new Set(this.#paths.filter((path) => path.startsWith('spend/')));
    // Before the first subscription, for `start()`'s reason and not a weaker
    // one: a Realtime Database listener delivers the node's current value the
    // moment it is attached, and that delivery runs `ingest` -> `#assemble` with
    // whatever `#day` and `#paths` say at that instant. Setting them afterwards
    // assembles against the day just left, whose counters were deleted two
    // statements ago -- a document about yesterday carrying a zero, which the
    // view would ingest as a break before the new day had said anything.
    this.#paths = planned.paths;
    this.#day = day;
    for (const path of wanted) {
      if (held.has(path)) continue;
      const handle = this.#subscribe(path);
      if (handle === null) return refuse(READER_CODES.BAD_PORT, 'port');
      const registered = this.#registry === undefined || this.#registry === null
        ? Object.freeze({ ok: true })
        : this.#registry.add(this.accountId, handle);
      if (!registered.ok) {
        this.#stopOne(handle);
        return refuse(READER_CODES.NOT_OPEN, path);
      }
      this.#handles.set(path, handle);
    }
    for (const path of held) {
      if (wanted.includes(path)) continue;
      this.#stopOne(this.#handles.get(path));
      this.#handles.delete(path);
    }
    // The break, made visible now rather than at the next delivery.
    this.#assemble(nowMs);
    return Object.freeze({ ok: true, dayChanged: true, day });
  }

  /** What to draw, and which day it is about. */
  snapshot({ nowMs = this.#now() } = {}) {
    return Object.freeze({
      ...this.#view.snapshot({ nowMs }),
      day: this.#day === null ? null : dayDate(this.#day),
      paths: this.#paths,
    });
  }

  /** Stop every listener this reader opened, and forget everything they
   * delivered. The registry's close does this too; this is for a caller that
   * wants it without closing the account.
   *
   * **Nothing a session read survives it.** A stopped reader that kept its
   * nodes and its delivery record would render the previous session's values
   * as freshly delivered on a later `start()` -- a measurement from a session
   * that has ended, presented as this one's, which is the exact confusion
   * `#delivered` exists to prevent. The **view** is not cleared: it belongs to
   * the account rather than to the session, and the screen keeps the last
   * document it drew (silence is not death -- `view.js`). */
  stop() {
    for (const handle of this.#handles.values()) this.#stopOne(handle);
    this.#handles.clear();
    this.#nodes.clear();
    this.#delivered.clear();
    this.#lastEvent.clear();
    this.#refused.clear();
    this.#errored.clear();
    this.#sampler.reset();
    this.#paths = Object.freeze([]);
    this.#day = null;
    this.#started = false;
    return Object.freeze({ ok: true });
  }

  /** Subscribe one path, and deliver its values and its errors to this reader.
   * Returns the handle, or `null` when the port cannot subscribe at all. */
  #subscribe(path) {
    if (this.#port === null || typeof this.#port !== 'object' || typeof this.#port.subscribe !== 'function') return null;
    const handle = this.#port.subscribe(path, {
      onValue: (value) => this.ingest(path, value, this.#now()),
      // The port's error goes to `failed`, which is where the permission
      // judgement is made: a port hands over the error it got, and this reader
      // is the one that knows which errors mean "refused".
      onError: (error) => this.failed(path, error, this.#now()),
    });
    if (handle === null || handle === undefined) return null;
    if (typeof handle !== 'function' && typeof handle.stop !== 'function') return null;
    return handle;
  }

  #stopOne(handle) {
    if (handle === null || handle === undefined) return;
    try {
      if (typeof handle === 'function') handle();
      else handle.stop();
    } catch {
      // A listener that throws on the way out is still a listener that is gone;
      // the registry reports its own failures.
    }
  }

  /**
   * One counter reading, into the burn window of its own series.
   *
   * **The sample is `spent + notional`, not `counted`.** `State.observe`
   * records `g.Spent+g.Notional` and `c.Spent+c.Notional` -- the burn window is
   * about what is being *spent*, and a subscription's list price is spend that
   * no cap counts. `counted` is a different number for a different figure: it
   * is what `bar()` divides by the cap, and it is the one the percent uses.
   * Feeding the window `counted` would make the phone's rate and `fast` flag
   * disagree with the console's for the very same tree, which is the one thing
   * recomputing them here was meant not to do.
   *
   * A node whose `spent` or `notional` could not be read is **not sampled**:
   * `null` is not zero, and a window holding a sum built from a missing half
   * would report a slope nobody measured.
   */
  #sample(path, value, nowMs) {
    if (!path.startsWith('spend/')) return;
    const read = readCounters(value);
    if (!read.ok) return;
    const { spent, notional } = read.counters;
    if (typeof spent !== 'number' || typeof notional !== 'number') return;
    const key = path.endsWith('/global') ? '' : (slugOf(path, 'spend') ?? path);
    this.#sampler.sample(key, nowMs, spent + notional);
  }

  /** The wire document the granted nodes add up to, judged. */
  #assemble(nowMs) {
    const day = this.#day;
    if (day === null) return refuse(READER_CODES.NOT_STARTED, 'assemble');
    // The instant is judged here as well as at the entry points: the document
    // carries `now` as RFC 3339, and a clock that cannot be rendered is not an
    // instant this app can assemble one from.
    if (!isNow(nowMs)) return refuse(READER_CODES.BAD_NOW, 'nowMs');
    const caps = this.#capsNodes();
    const project = readCaps(caps);
    const counters = countersOf(this.#nodes.get(`spend/${day}/global`));
    const slugs = this.#repositories ?? [];
    const document = {
      project: this.#project,
      day: dayDate(day),
      now: new Date(nowMs).toISOString(),
      connection: this.#connection(nowMs),
      total: {
        counted_usd: counters.counted,
        spent_usd: counters.spent,
        notional_usd: counters.notional,
        daily_cap_usd: project.caps.globalDaily,
        ...this.#recompute('', counters.counted, project.caps.globalDaily, nowMs),
        kill: killOfDocument(readKill(this.#nodes.get('config/kill/global')).kill),
      },
      repos: slugs.map((slug) => this.#repo(slug, caps, nowMs)),
    };
    const judged = validateDocument(document);
    // §10.7's "the day's real numbers follow once the new pair delivers its put
    // of `/`" is a **delivery**, not a presence: a Realtime Database listener
    // answers with `null` for a node that is not there, and that answer is the
    // put. So the day is measured once its counter has been heard, and until
    // then the view draws the gap rather than the zero the absent node adds up
    // to.
    const measured = this.#delivered.has(`spend/${day}/global`);
    this.#view.ingest(judged, { nowMs, measured });
    return Object.freeze({
      ok: judged.ok,
      ...(judged.ok ? { document: judged.document } : { code: judged.code, field: judged.field }),
      measured,
    });
  }

  #capsNodes() {
    const defaults = this.#nodes.get('config/caps/defaults');
    const global = this.#nodes.get('config/caps/global');
    return { global, defaults };
  }

  #repo(slug, caps, nowMs) {
    const counters = countersOf(this.#nodes.get(`spend/${this.#day}/repos/${slug}`));
    const cap = readCaps({ global: caps.global, defaults: caps.defaults, repo: this.#nodes.get(`config/caps/repos/${slug}`) });
    return {
      slug,
      repo: null,
      counted_usd: counters.counted,
      spent_usd: counters.spent,
      notional_usd: counters.notional,
      daily_cap_usd: cap.caps.daily,
      ...this.#recompute(slug, counters.counted, cap.caps.daily, nowMs),
      kill: killOfDocument(readKill(this.#nodes.get(`config/kill/repos/${slug}`)).kill),
    };
  }

  /** The recomputed pair, in the wire's spelling: `percent` and `burn`. */
  #recompute(key, counted, cap, nowMs) {
    const out = recompute({ counted, cap, rate: this.#sampler.rate(key, nowMs) });
    return {
      percent: out.percent,
      burn: { known: out.burn.known, usd_per_min: out.burn.usdPerMin, fast: out.burn.fast },
    };
  }

  /**
   * The connection as the granted paths have it: the worst of them, in
   * `State.conn`'s order -- a refusal, then a read that failed, then the paths
   * that have not answered, then the oldest node that has. Silence reaches
   * `stale` and stops there: `State.conn` has no threshold past `StaleAfter`,
   * and `watch.LostAfter` is a property of a run, which the phone does not
   * have.
   */
  #connection(nowMs) {
    // The oldest node that has delivered, and the age that comes off it --
    // computed **before** the state, because `State.conn` sets `Age` whenever
    // any stream has been heard, whatever the state is: a refusal does not
    // reset the clock, and reporting one at an age of zero would tell the
    // screen the feed had just been heard from.
    let oldest = null;
    for (const at of this.#lastEvent.values()) {
      if (oldest === null || at < oldest) oldest = at;
    }
    const age_seconds = this.#ageSeconds(nowMs, oldest);
    if (this.#refused.size > 0) {
      return { state: 'refused', reason: CONNECTION_REASONS.refused, age_seconds };
    }
    if (this.#errored.size > 0) {
      return { state: 'offline', reason: CONNECTION_REASONS.failed, age_seconds };
    }
    if (this.#lastEvent.size < this.#paths.length) {
      return { state: 'offline', reason: CONNECTION_REASONS.connecting, age_seconds };
    }
    const ageMs = oldest === null ? 0 : Math.max(nowMs - oldest, 0);
    if (ageMs > STALE_AFTER_MS) return { state: 'stale', reason: CONNECTION_REASONS.quiet, age_seconds };
    if (this.#port?.polling === true) return { state: 'polling', reason: CONNECTION_REASONS.polling, age_seconds };
    return { state: 'live', reason: null, age_seconds };
  }

  #ageSeconds(nowMs, oldest) {
    return oldest === null ? 0 : Math.max(nowMs - oldest, 0) / 1000;
  }
}

/** The one thing a caller needs to know about a port before handing it over. */
export function isPort(port) {
  return port !== null && typeof port === 'object' && typeof port.subscribe === 'function';
}
