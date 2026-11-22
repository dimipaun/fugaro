// Regenerates internal/mailbox/testdata/parity.json, the mailbox state
// machine's parity corpus (mobile plan, Task P4.1 T8).
//
//   node shared/mailbox/parity.generate.mjs [output.json]
//
// The Go consumer binds `shared/mailbox`'s rules — it cannot import a
// JavaScript module — so the binding is held to the module by **committed
// data**: this script drives the module (and, for the seeded half, the
// simulator the plan's verification names) and writes the answers it gave to
// the corpus, which `internal/mailbox/parity_test.go` replays with no Node, no
// clock and no network. Ruling M9 of `docs/mobile/mailbox-consumer-plan.md`:
// parity is proven by committed vectors where they exist — the MAC's bytes and
// the action record are `shared/mailbox-schema/vectors.json` and
// `shared/confirm/vectors.json`, both merged and replayed by T1/T2 and P6.7 —
// and by a data corpus where they do not. `shared/mailbox` carries no vector
// file, so its **outcomes and codes** are what this corpus adds.
//
// **This file is not a test and is not run by `npm test`** (which matches
// `**/*.test.js`) — the convention `shared/mailbox-schema/vectors.generate.mjs`
// and `shared/lease/parity.generate.mjs` set for their own artifacts. The plan
// names `shared/mailbox/parity.test.js` as the writer; it is not, and the split
// is deliberate: `shared/mailbox/mutation-proof.sh` runs this module's test
// files against a **mutated** `index.js` and restores `index.js` and nothing
// else, so a test that regenerated the corpus would commit the mutated answers
// and leave the Go replay red for a reason nobody could see. The corpus
// therefore has exactly one writer, this script, and `parity.test.js` reads it
// and re-derives every answer from the live module.
//
// To change the corpus, change this file and run
// `node shared/mailbox/parity.generate.mjs`; a stale corpus is a failing test,
// never a silent rewrite.
//
// **What the corpus carries.**
//
//   * `scenarios`: the plan's verification, spelled out — **120 seeded
//     schedules with three messages, two consumers and twelve passes each**
//     (`seed` 0..119, the simulator's own property sweep), plus the cases the
//     sweep cannot reach. For each: the events, the state it starts from, the
//     outcome or refusal code of every event, and the state the module
//     produced. A seeded scenario carries its `seed` and its expanded
//     `scheduleText`, so a failing case is replayable from the corpus alone.
//   * `options`: the five numbers the module is judged with (the command
//     window, the prompt TTL, the cap, the sender's share, the future
//     tolerance), so a bound that moves on one side is a failing test on the
//     other. A scenario may carry its own `options` where the rule it pins
//     needs a cap the production numbers never reach.
//   * `outcomes` and `codes`: the module's vocabularies, by their own spelling
//     (`OUTCOME`, `REFUSAL`).
//   * `hash`: SHA-256 of the file with the hash's own field zeroed, so a
//     **hand edit** of the artifact is a failing test rather than a corpus the
//     Go replay silently agrees with. It detects a hand edit and nothing else:
//     what detects a **changed module** is regeneration, which
//     `TestParityCorpusIsCurrent` performs where `node` is on `PATH` and skips
//     with a recorded reason where it is not (the plan's decision 4, §16).
//
// **What it deliberately does not carry, and why.**
//
//   * The refusal's `field`. The corpus carries the DECISION — the outcome, the
//     code — and never the wording or the location: a refusal whose field moved
//     is T4's own test's business, not a protocol change the Go side must
//     agree with (the rule `shared/lease/parity.generate.mjs` states for its
//     own corpus).
//   * The message's `mac`, and its `text` is carried only as the input record
//     the sweep wrote. The ledger reads the pair `(sender, id)`, the `at` and
//     the kind, and holds no text (T4's `TestLedgerFileHoldsNoText`); the tag
//     is T2's and the ledger never looks at it. Both sides rebuild a
//     schema-valid message from the four fields the corpus carries, and any
//     valid tag gives the same answers.
//   * The **watermark** refusal. `at <= evictedThrough` can only be reached by
//     a message the window has already refused — an evicted record is strictly
//     older than the horizon, and the horizon is at least every kind's window —
//     so no case asks for it; what the corpus does carry is the watermark
//     **moving**, in the eviction case below.
//   * The outcomes the binding's fold cannot answer: `accepted` (a delivery is
//     acted on in the same call, so no event is ever answered with it),
//     `nothing-to-act` (the act is never asked for on its own) and `closed`
//     (nothing in this half closes a mailbox). They are in `outcomes`, the
//     module's vocabulary, and no event carries them: the corpus records what
//     the Go binding can be asked, and the module's own tests cover the rest.
//   * The state that is not an object, or whose `seen` is not an array. Go's
//     records are typed, so no Go caller can hand the ledger one, and a
//     `seen` of the wrong type is the file's own `invalid-ledger` rather than
//     the module's `invalid-seen` — the corpus asks for neither. The reader
//     cases it does ask for are the ones both ends answer with the same code.
//
// Everything here is injected data: no clock is read (every event carries its
// `now`), no file is written except the corpus itself, and nothing is recorded
// from a live project.

import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

import {
  DEFAULT_FUTURE_TOLERANCE_MS,
  DEFAULT_MAX_SEEN,
  DEFAULT_WINDOW_MS,
  KIND,
  OUTCOME,
  REFUSAL,
  STATE,
  TIME_MAX,
  defaultMaxPerSender,
  initialState,
  step,
} from './index.js';
import { PROMPT_TTL_MS } from '../prompts/index.js';
import { DEFAULT_START_MS, PHONE, createSimulator, scheduleFromSeed } from '../mailbox-sim/index.js';

// The corpus the Go replay and the test read. It lives in the Go package that
// binds the state machine, beside the vectors a later task's corpus uses,
// because that is where the reader is.
export const CORPUS = new URL('../../internal/mailbox/testdata/parity.json', import.meta.url);

export const REGENERATE = 'node shared/mailbox/parity.generate.mjs';

const SECOND = 1_000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;

/** The plan's verification: seeds 0..119, three messages, two consumers, twelve passes. */
const SEEDS = 120;
const MESSAGES = 3;
const PASSES = 12;

/** The actors the sweeps put on the wire, skewed clocks and all. */
const ACTORS = Object.freeze([PHONE, 'consumer-1', 'consumer-2']);

/** The numbers the sweeps run the module with: the module's own defaults. */
const OPTIONS = Object.freeze({
  windowMs: DEFAULT_WINDOW_MS,
  promptTtlMs: PROMPT_TTL_MS,
  maxSeen: DEFAULT_MAX_SEEN,
  maxPerSender: defaultMaxPerSender(DEFAULT_MAX_SEEN),
  futureToleranceMs: DEFAULT_FUTURE_TOLERANCE_MS,
});

// A valid tag, and only that: the ledger never reads a message's mac, so its
// value is not part of the input and the corpus does not carry it. Written as
// a repeat rather than as 64 characters, the way the simulator writes its own
// placeholder.
const MAC = 'f'.repeat(64);

/** A fixed instant the hand-built cases are judged against. */
const T = 1_800_000_000_000;

const HASH_ZEROS = '0'.repeat(64);

// ---------------------------------------------------------------------------
// The state, in the shape both ends read
// ---------------------------------------------------------------------------

/** The ledger's own three fields: what the Go side writes to its file. */
function stateOf(state) {
  return {
    now: state.now,
    evictedThrough: state.evictedThrough,
    // A ledger that is not an array is carried as it is: it is one of the
    // reader cases below, and the two ends must refuse it for the same reason.
    seen: Array.isArray(state.seen)
      ? state.seen.map((entry) => ({
          sender: entry.sender,
          id: entry.id,
          at: entry.at,
          kind: entry.kind,
          status: entry.status,
        }))
      : state.seen,
  };
}

/** Whether two states are the same state, field by field. */
function sameState(left, right) {
  if (left.now !== right.now || left.evictedThrough !== right.evictedThrough) return false;
  const leftSeen = left.seen;
  const rightSeen = right.seen;
  if (leftSeen === null || rightSeen === null) return leftSeen === rightSeen;
  if (leftSeen.length !== rightSeen.length) return false;
  return leftSeen.every((entry, index) => {
    const other = rightSeen[index];
    return (
      entry.sender === other.sender &&
      entry.id === other.id &&
      entry.at === other.at &&
      entry.kind === other.kind &&
      entry.status === other.status
    );
  });
}

/**
 * The phase the ledger says the mailbox is in — the module's `derivePhase`,
 * written out again because the corpus stores the ledger's three fields and
 * the module wants all nine. A wrong phase is not a silent difference: the
 * module refuses the state (`invalid-state`) and every scenario that uses it
 * fails here.
 */
function phaseOf(seen) {
  const entries = Array.isArray(seen) ? seen : [];
  if (entries.some((entry) => entry.status === 'accepted')) return STATE.PENDING;
  if (entries.some((entry) => entry.status === 'acted')) return STATE.ACTED;
  return STATE.IDLE;
}

/** The module's own state, rebuilt from the three fields the corpus stores. */
function moduleState(snapshot, options) {
  return {
    phase: phaseOf(snapshot.seen),
    now: snapshot.now,
    windowMs: options.windowMs,
    maxSeen: options.maxSeen,
    maxPerSender: options.maxPerSender,
    promptTtlMs: options.promptTtlMs,
    futureToleranceMs: options.futureToleranceMs,
    evictedThrough: snapshot.evictedThrough,
    seen: snapshot.seen,
  };
}

/** The state a scenario starts from: an empty ledger at `now`. */
function emptyState(now) {
  return { now, evictedThrough: 0, seen: [] };
}

/** A ledger record, as the module stores one. */
function record(sender, id, at, kind, status) {
  return { sender, id, at, kind, status };
}

/** A message, as the ledger reads one: the pair, the time, and the text it holds none of. */
function message(id, sender, at, text) {
  return { id, sender, at, text, mac: MAC };
}

// ---------------------------------------------------------------------------
// Driving the module the way the Go binding drives it
// ---------------------------------------------------------------------------

/**
 * One event of the Go binding, replayed through the module: `perform` is
 * `Ledger.Perform` — the module's `deliver` and, for a delivery it takes, the
 * `act` that follows it in the same pass, which is the fold the binding is —
 * and `ack` is `Ledger.Ack`. The answer is the decision: the outcome, or the
 * refusal's code.
 */
function apply(state, event, options) {
  if (event.op === 'ack') {
    const result = step(state, { type: 'ack', now: event.now });
    if (!result.ok) return { state, answer: { code: result.code } };
    return { state: result.state, answer: { outcome: result.outcome, id: result.id, sender: result.sender } };
  }

  const delivered = step(state, {
    type: 'deliver',
    now: event.now,
    message: message(event.message.id, event.message.sender, event.message.at, event.message.text),
    kind: event.kind,
  });
  if (!delivered.ok) return { state, answer: { code: delivered.code } };
  if (delivered.outcome !== OUTCOME.ACCEPTED) {
    return {
      state: delivered.state,
      answer: { outcome: delivered.outcome, id: delivered.id, sender: delivered.sender },
    };
  }
  const acted = step(delivered.state, { type: 'act', now: event.now });
  if (!acted.ok) throw new Error(`the act after an accepted delivery was refused: ${acted.code}`);
  return { state: acted.state, answer: { outcome: acted.outcome, id: acted.id, sender: acted.sender } };
}

/** One scenario, replayed: every event's answer and the state the module produced. */
function replay(scenario) {
  const options = scenario.options === undefined ? OPTIONS : scenario.options;
  let state = moduleState(scenario.state, options);
  const answers = [];
  for (const event of scenario.events) {
    const next = apply(state, event, options);
    state = next.state;
    answers.push(next.answer);
  }
  return { answers, state: stateOf(state) };
}

// ---------------------------------------------------------------------------
// The seeded sweeps
// ---------------------------------------------------------------------------

/** Build a simulator, failing loudly rather than quietly on a refusal. */
function simulator(options) {
  const made = createSimulator({ promptTtlMs: PROMPT_TTL_MS, ...options });
  if (made.ok !== true) throw new Error(`the simulator refused: ${JSON.stringify(made)}`);
  return made;
}

/**
 * The script the plan's verification names, driven by hand rather than through
 * `run()` for one reason: the clock each pass runs at is then known, and it is
 * what every event of that pass is judged against. It is
 * `shared/mailbox-sim/index.test.js`'s own sweep, tick for tick.
 */
function sweep(seed) {
  const schedule = scheduleFromSeed(seed, { actors: ACTORS });
  if (schedule.ok === false) throw new Error(`seed ${seed}: the schedule was refused`);
  const run = simulator({ schedule });
  run.consumer('consumer-1');
  run.consumer('consumer-2');
  for (let index = 0; index < MESSAGES; index += 1) {
    run.phone.writeInbox({ id: `m${index}`, text: `turn ${index} on` });
    run.advance(2 * SECOND);
  }
  const passes = [];
  for (let tick = 0; tick < PASSES; tick += 1) {
    if (tick > 0) run.advance(SECOND);
    passes.push(run.now());
    run.tick();
  }
  return { run, report: run.report(), passes, seed };
}

/**
 * The events one sweep committed, in the order the store committed them, with
 * each taken delivery folded into the `act` the binding runs inside the same
 * call.
 *
 * The order is the store's, not the log's: within a pass the deliveries come
 * first (the winner of the compare-and-swap is the only consumer whose
 * deliveries are logged) and the ack comes after the acts the pass folded into
 * them. The fold is checked against the simulator rather than assumed — every
 * answer below is compared with what the sweep logged, and the state the
 * replay produces with the state the store ended in — so an ordering this file
 * has wrong is a failure here, not a corpus nobody can trust.
 */
function committed(sweepRun) {
  const { report, passes, seed, run } = sweepRun;
  const written = new Map(report.phone.written.map((entry) => [entry.id, entry]));
  const actings = new Map();
  for (const acting of report.actings) {
    if (actings.has(acting.id)) throw new Error(`seed ${seed}: two actings for ${acting.id}`);
    actings.set(acting.id, acting);
  }

  const claimed = new Set();
  const events = [];
  for (let pass = 1; pass <= PASSES; pass += 1) {
    const now = passes[pass - 1];
    for (const delivery of report.deliveries.filter((entry) => entry.pass === pass)) {
      const writtenMessage = written.get(delivery.id);
      if (writtenMessage === undefined) throw new Error(`seed ${seed}: a delivery of ${delivery.id}, never written`);
      const kind = run.store.kinds.get(delivery.id);
      events.push({
        op: 'perform',
        now,
        kind,
        message: {
          id: writtenMessage.id,
          sender: writtenMessage.sender,
          text: writtenMessage.text,
          at: writtenMessage.at,
        },
      });
      if (delivery.outcome === OUTCOME.ACCEPTED) {
        const acting = actings.get(delivery.id);
        if (acting === undefined) throw new Error(`seed ${seed}: ${delivery.id} was taken and never acted on`);
        if (acting.pass !== pass) throw new Error(`seed ${seed}: ${delivery.id} was acted on in another pass`);
        claimed.add(delivery.id);
      }
    }
    for (const ack of report.acks.filter((entry) => entry.pass === pass)) {
      events.push({ op: 'ack', now, id: ack.id });
    }
  }
  for (const acting of report.actings) {
    if (!claimed.has(acting.id)) throw new Error(`seed ${seed}: ${acting.id} was acted on and never taken`);
  }

  const scenario = {
    name: `seed ${seed}`,
    seed,
    scheduleText: report.scheduleText,
    state: stateOf({ now: DEFAULT_START_MS, evictedThrough: 0, seen: [] }),
    events,
  };
  const { answers, state } = replay(scenario);
  answers.forEach((answer, index) => {
    Object.assign(scenario.events[index], answer);
  });

  // The simulator's own answers, event by event, and the state it ended in:
  // what makes the fold above a check rather than a claim. The sweep logs a
  // delivery's outcome and the pair it is about, and an ack's outcome and the
  // record it retired — the same fields the module's answer carries, which is
  // what the comparison is made of.
  const logged = [];
  for (const delivery of report.deliveries) {
    logged.push(
      delivery.outcome === 'refused'
        ? { code: delivery.code }
        : { outcome: delivery.outcome, id: delivery.id, sender: delivery.sender },
    );
  }
  let seenDeliveries = 0;
  let seenAcks = 0;
  answers.forEach((answer, index) => {
    const event = events[index];
    const expected = event.op === 'ack' ? report.acks[seenAcks++] : logged[seenDeliveries++];
    const acting = event.op === 'perform' ? actings.get(event.message.id) : undefined;
    // A delivery the sweep took is folded into the act that follows it: the
    // acting is the one whose id is the delivered message's, so a fold that
    // acted on another record is a disagreement here rather than a silent
    // difference in what the corpus pins.
    const folded =
      event.op === 'perform' && expected.outcome === OUTCOME.ACCEPTED
        ? { outcome: acting.outcome, id: acting.id, sender: acting.sender }
        : expected;
    const same =
      answer.code !== undefined
        ? answer.code === folded.code
        : answer.outcome === folded.outcome &&
          answer.id === folded.id &&
          (event.op === 'ack' || answer.sender === folded.sender);
    if (!same) {
      throw new Error(
        `seed ${seed}: the replay and the sweep disagree on event ${index}\n` +
          `replay: ${JSON.stringify(answer)}\nsweep:  ${JSON.stringify(folded)}\n${report.scheduleText}`,
      );
    }
  });
  if (seenDeliveries !== report.deliveries.length || seenAcks !== report.acks.length) {
    throw new Error(`seed ${seed}: the fold lost an event`);
  }
  const stored = stateOf(report.store.mailbox);
  if (!sameState(state, stored)) {
    throw new Error(
      `seed ${seed}: the replay ended in a state the store never held\n` +
        `replay: ${JSON.stringify(state)}\nstore:  ${JSON.stringify(stored)}\n${report.scheduleText}`,
    );
  }
  // The state the pass left, recorded the same way a hand-built case records
  // it: the answers alone would let a replay that ends in the wrong state — a
  // duplicate that advanced nothing, an eviction that took the wrong record —
  // pass, and this is the one check the sweep can make that the log cannot.
  scenario.final = state;
  return scenario;
}

// ---------------------------------------------------------------------------
// The cases the sweep cannot reach
// ---------------------------------------------------------------------------

/**
 * A case with no simulator behind it: a state, the events, and the answers the
 * module gave. `stateRefusal` marks the reader cases — a state the module
 * refuses to read at all, where the Go side's answer comes from `OpenLedger`
 * rather than from a step, with the same code.
 */
function scenario(name, { options, state, events = [], stateRefusal }) {
  const built = {
    name,
    seed: null,
    scheduleText: null,
    ...(options === undefined ? {} : { options }),
    state: stateOf(state),
    events,
  };
  const { answers, state: final } = replay(built);
  answers.forEach((answer, index) => {
    Object.assign(built.events[index], answer);
  });
  if (stateRefusal !== undefined) {
    const read = step(moduleState(built.state, options === undefined ? OPTIONS : options), {
      type: 'ack',
      now: built.state.now,
    });
    if (read.ok || read.code !== stateRefusal) {
      throw new Error(
        `${name}: the state was expected to be refused ${stateRefusal}, and the module answered ` +
          `${read.ok ? 'a state' : read.code}`,
      );
    }
    built.stateRefusal = stateRefusal;
    return built;
  }
  built.final = final;
  return built;
}

/** One event, as the corpus records it: the input and the answer it earned. */
function perform(now, id, sender, at, text, kind = KIND.COMMAND) {
  return { op: 'perform', now, kind, message: { id, sender, at, text } };
}

function ack(now) {
  return { op: 'ack', now };
}

/**
 * The cases, in the order the rules are stated: what the reader refuses, what
 * a delivery is refused for, and the outcomes a pass can answer with.
 */
function cases() {
  return [
    // The reader. Each state here is one the module refuses to read, and the
    // Go side refuses the same state at `OpenLedger` with the same code — the
    // half of T4's parse that is `shared/mailbox`'s readState, field for field.
    scenario('a state whose now is zero is refused', {
      state: emptyState(0),
      stateRefusal: REFUSAL.INVALID_NOW,
    }),
    scenario('a state whose now is past the ceiling is refused', {
      state: emptyState(TIME_MAX + 1),
      stateRefusal: REFUSAL.INVALID_NOW,
    }),
    scenario('a watermark past what the windows allow is refused', {
      state: { now: T, evictedThrough: T, seen: [] },
      stateRefusal: REFUSAL.INVALID_EVICTED_THROUGH,
    }),
    scenario('a ledger with no ledger in it is refused', {
      state: { now: T, evictedThrough: 0, seen: null },
      stateRefusal: REFUSAL.INVALID_SEEN,
    }),
    scenario('a ledger longer than its cap is refused', {
      options: { ...OPTIONS, maxSeen: 2, maxPerSender: 1 },
      state: {
        now: T,
        evictedThrough: 0,
        seen: [
          record(PHONE, 'm1', T - 3 * MINUTE, KIND.COMMAND, 'accepted'),
          record(PHONE, 'm2', T - 2 * MINUTE, KIND.COMMAND, 'accepted'),
          record(PHONE, 'm3', T - MINUTE, KIND.COMMAND, 'accepted'),
        ],
      },
      stateRefusal: REFUSAL.INVALID_SEEN,
    }),
    scenario('a record with a zero at is refused', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm1', 0, KIND.COMMAND, 'accepted')],
      },
      stateRefusal: REFUSAL.INVALID_SEEN_ENTRY,
    }),
    scenario('a record with a status the module does not define is refused', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm1', T - MINUTE, KIND.COMMAND, 'done')],
      },
      stateRefusal: REFUSAL.INVALID_SEEN_ENTRY,
    }),
    scenario('two records for one pair are refused', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [
          record(PHONE, 'm1', T - 2 * MINUTE, KIND.COMMAND, 'accepted'),
          record(PHONE, 'm1', T - MINUTE, KIND.COMMAND, 'accepted'),
        ],
      },
      stateRefusal: REFUSAL.DUPLICATE_SEEN_ENTRY,
    }),
    scenario('a ledger out of its canonical order is refused', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [
          record(PHONE, 'm1', T - MINUTE, KIND.COMMAND, 'accepted'),
          record(PHONE, 'm2', T - 2 * MINUTE, KIND.COMMAND, 'accepted'),
        ],
      },
      stateRefusal: REFUSAL.UNSORTED_SEEN,
    }),

    // The event. Its own `now` is read before anything else it says, and its
    // kind before the two `now` guards — the order T4's deliver is written in.
    scenario('an event whose now is zero is refused', {
      state: emptyState(T),
      events: [perform(0, 'm1', PHONE, T, 'turn on')],
    }),
    scenario('an event whose now is past the ceiling is refused', {
      state: emptyState(T),
      events: [perform(TIME_MAX + 1, 'm1', PHONE, T, 'turn on')],
    }),
    scenario('a now earlier than the ledger’s own is refused', {
      state: emptyState(T),
      events: [perform(T - 1, 'm1', PHONE, T - 1, 'turn on')],
    }),
    scenario('a now further ahead than a year is refused', {
      state: emptyState(T),
      events: [perform(T + 366 * 24 * HOUR + 1, 'm1', PHONE, T, 'turn on')],
    }),
    scenario('a kind that is not a kind is refused before the now guards', {
      state: emptyState(T),
      events: [perform(T - 1, 'm1', PHONE, T - 1, 'turn on', 'note')],
    }),
    scenario('an event whose now is zero is refused before its kind', {
      state: emptyState(T),
      events: [perform(0, 'm1', PHONE, T, 'turn on', 'note')],
    }),

    // The delivery: the message's own time, then the ledger.
    scenario('a message with a zero at is refused', {
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, 0, 'turn on')],
    }),
    scenario('a command older than the window is a replay', {
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, T - OPTIONS.windowMs - 1, 'turn on')],
    }),
    scenario('a command exactly a window old is a delivery', {
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, T - OPTIONS.windowMs, 'turn on')],
    }),
    scenario('a prompt older than the TTL has expired', {
      state: emptyState(T),
      events: [perform(T, 'q1', PHONE, T - PROMPT_TTL_MS - 1, 'are you up?', KIND.PROMPT)],
    }),
    scenario('a prompt the command window would have refused is still a delivery', {
      state: emptyState(T),
      events: [perform(T, 'q1', PHONE, T - OPTIONS.windowMs - 1, 'are you up?', KIND.PROMPT)],
    }),
    scenario('a message past the future tolerance is refused', {
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, T + OPTIONS.futureToleranceMs + 1, 'turn on')],
    }),
    scenario('a message exactly the future tolerance ahead is a delivery', {
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, T + OPTIONS.futureToleranceMs, 'turn on')],
    }),
    scenario('a message at the ceiling is refused', {
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, TIME_MAX, 'turn on')],
    }),
    scenario('a held pair with another at is an id conflict', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm1', T - MINUTE, KIND.COMMAND, 'acted')],
      },
      events: [perform(T, 'm1', PHONE, T - 2 * MINUTE, 'turn on')],
    }),
    scenario('a held pair under another kind is an id conflict', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm1', T - MINUTE, KIND.COMMAND, 'acted')],
      },
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on', KIND.PROMPT)],
    }),
    scenario('a pair the ledger holds and has acked is still a duplicate', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm1', T - MINUTE, KIND.COMMAND, 'acked')],
      },
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on')],
    }),

    // The cap and the sender's share, which the production numbers never
    // reach: the sweep runs with the module's own cap of 1,024.
    scenario('a full ledger refuses the delivery that would grow it', {
      options: { ...OPTIONS, maxSeen: 1, maxPerSender: 1 },
      state: emptyState(T),
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on'), perform(T, 'm2', PHONE, T - MINUTE, 'turn off')],
    }),
    scenario('a sender over its share is refused, acked records and all', {
      options: { ...OPTIONS, maxSeen: 4, maxPerSender: 1 },
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm0', T - MINUTE, KIND.COMMAND, 'acked')],
      },
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on')],
    }),
    scenario('another sender is not held to the first one’s share', {
      options: { ...OPTIONS, maxSeen: 4, maxPerSender: 1 },
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm0', T - MINUTE, KIND.COMMAND, 'acked')],
      },
      events: [perform(T, 'm1', 'phone-2', T - MINUTE, 'turn on')],
    }),

    // The outcomes a pass answers with, and the state it leaves behind.
    scenario('a delivery the window has already passed is acted on late', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm0', T - OPTIONS.windowMs - 1, KIND.COMMAND, 'accepted')],
      },
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on')],
    }),
    scenario('an ack with nothing acted is nothing to ack', {
      state: emptyState(T),
      events: [ack(T)],
    }),
    scenario('an acked record past the horizon is evicted by the next delivery', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm0', T - 80 * HOUR, KIND.COMMAND, 'acked')],
      },
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on'), ack(T)],
    }),
    scenario('an acked record inside the horizon is not evicted', {
      state: {
        now: T,
        evictedThrough: 0,
        seen: [record(PHONE, 'm0', T - HOUR, KIND.COMMAND, 'acked')],
      },
      events: [perform(T, 'm1', PHONE, T - MINUTE, 'turn on')],
    }),
  ];
}

// ---------------------------------------------------------------------------
// The corpus
// ---------------------------------------------------------------------------

/**
 * The whole corpus, in the one shape the Go replay reads. `hash` is written as
 * zeros here and filled in by `write`, so the hash covers every other byte of
 * the file it travels in.
 */
export function corpus() {
  const scenarios = [];
  for (let seed = 0; seed < SEEDS; seed += 1) scenarios.push(committed(sweep(seed)));
  scenarios.push(...cases());
  return {
    // Where the answers below came from: the Go side names the same file in its
    // own header, and the replay test fails if the two disagree.
    module: 'shared/mailbox/index.js',
    generator: 'shared/mailbox/parity.generate.mjs',
    simulator: 'shared/mailbox-sim/index.js',
    replay: 'internal/mailbox/parity_test.go',
    options: OPTIONS,
    outcomes: Object.values(OUTCOME),
    codes: REFUSAL,
    scenarios,
    hash: HASH_ZEROS,
  };
}

/** The file's own bytes, with the hash field zeroed — what the hash covers. */
export function text(built) {
  return `${JSON.stringify(built, null, 2)}\n`;
}

/** SHA-256 of the corpus with its own hash field zeroed. */
export function hashOf(built) {
  return createHash('sha256').update(text(built), 'utf8').digest('hex');
}

function write() {
  const built = corpus();
  built.hash = hashOf(built);
  const target = process.argv[2] === undefined ? CORPUS : pathToFileURL(resolve(process.argv[2]));
  mkdirSync(new URL('./', target), { recursive: true });
  writeFileSync(target, text(built));
  process.stdout.write(`wrote ${target.pathname}\n`);
}

// Only when run, never when imported: `parity.test.js` imports nothing from
// here — importing this file would run the writer, which is exactly what that
// test exists not to be — but a reader that imports `corpus()` to inspect it
// must not have the corpus rewritten under it.
if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  write();
}
