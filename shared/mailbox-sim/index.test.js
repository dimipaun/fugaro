// Tests for the deterministic mailbox simulator (mobile plan, Task P1.5).
//
// The suite is in three parts. The **scenario** tests are the plan's
// end-to-end verification: a phone writes, a consumer leases, acts, acks and
// replies, the phone reads the reply, and then the wire duplicates a delivery,
// reorders two of them, and drops an ack — with the outcome unchanged in each
// case. The **property** tests run the same script over many seeded schedules
// and assert what the modules promise (at most once acting, at least once
// delivery, one lease holder, the safe path for an expired prompt, a refused
// replay) plus the positive controls that keep those properties from passing
// vacuously. The **fail-closed** tests pin the refusals, the determinism and
// the compare-and-swap.
//
// A failing property prints its seed and its schedule, which is what makes the
// failure replayable: `parseScheduleText` reads back exactly what was printed.

import test from 'node:test';
import assert from 'node:assert/strict';

import { isMessage } from '../mailbox-schema/index.js';
import { KIND } from '../mailbox/index.js';
import { CLAIM_TTL, MAX_CLOCK_SKEW, emptyLeaseState, takeLease } from '../lease/index.js';
import {
  DEFAULT_START_MS,
  FAULT,
  MAX_SKEW_MS,
  MAX_TICKS,
  PHONE,
  REFUSAL,
  WIRE_OPS,
  casCommit,
  createSimulator,
  createStore,
  emptySchedule,
  faultFor,
  makeRng,
  parseScheduleText,
  readSnapshot,
  replyId,
  scheduleFromSeed,
  scheduleText,
} from './index.js';

const SECOND = 1_000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;

/** D118's prompt TTL, the owner's number: 72 hours. */
const PROMPT_TTL = 72 * HOUR;

/** The mailbox's default replay window: 6 hours. */
const WINDOW = 6 * HOUR;

/** The actors the sweeps put on the wire. */
const ACTORS = Object.freeze([PHONE, 'consumer-1', 'consumer-2']);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Build a simulator, failing loudly rather than quietly on a refusal. */
function simulator(options = {}) {
  const made = createSimulator({ promptTtlMs: PROMPT_TTL, ...options });
  assert.equal(made.ok, true, `the simulator refused: ${JSON.stringify(made)}`);
  return made;
}

/** The script the property tests drive: three messages, two consumers, ticks. */
function sweep(schedule, options = {}) {
  const run = simulator({ schedule, ...options });
  run.consumer('consumer-1');
  run.consumer('consumer-2');
  for (let index = 0; index < 3; index += 1) {
    run.phone.writeInbox({ id: `m${index}`, text: `turn ${index} on` });
    run.advance(2 * SECOND);
  }
  run.run({ ticks: 12, intervalMs: SECOND });
  return run.report();
}

/** The plan's end-to-end script: one message, one consumer. */
function scenario(schedule) {
  const run = simulator({ schedule });
  run.consumer('consumer-1');
  run.phone.writeInbox({ id: 'm1', text: 'turn on the kitchen light' });
  run.run({ ticks: 4, intervalMs: SECOND });
  return run.report();
}

/** What a run *did*, as opposed to how it got there: the outcome, not the path. */
function outcomeOf(report) {
  return {
    actings: report.actings.map((acting) => [acting.id, acting.outcome, acting.action]),
    replies: report.store.outbox.map((message) => [message.id, message.text]),
    ledger: report.store.mailbox.seen.map((entry) => [entry.id, entry.at, entry.kind, entry.status]),
    spoken: report.phone.spoken,
    // By id, not by arrival: the order the store learned of a message in is
    // the network's, and it is exactly what a reorder is allowed to change.
    inbox: report.store.inbox
      .map((message) => [message.id, message.acked === true])
      .sort((left, right) => (left[0] < right[0] ? -1 : 1)),
  };
}

/** What a failure prints: the seed, the schedule, and what the run did. */
function failure(report) {
  return [
    `seed: ${report.seed}`,
    `schedule: ${report.scheduleText}`,
    `actings: ${JSON.stringify(report.actings)}`,
    `deliveries: ${JSON.stringify(report.deliveries)}`,
    `lease: ${JSON.stringify(report.lease)}`,
    `faults: ${JSON.stringify(report.faults)}`,
  ].join('\n');
}

function seeded(seed, options = {}) {
  const schedule = scheduleFromSeed(seed, { actors: ACTORS, ...options });
  assert.equal(schedule.ok, undefined, `the schedule was refused: ${JSON.stringify(schedule)}`);
  return schedule;
}

// ---------------------------------------------------------------------------
// The end-to-end run, and the three faults it must survive
// ---------------------------------------------------------------------------

test('end to end: a phone writes, a consumer leases, acts, acks and replies, the phone reads the reply', () => {
  const report = scenario(emptySchedule({ seed: 1 }));

  // The phone wrote one message and read one reply.
  assert.deepEqual(
    report.phone.written.map((message) => message.id),
    ['m1'],
  );
  assert.deepEqual(report.phone.spoken, [replyId('m1')]);
  assert.deepEqual(report.phone.pending, []);

  // The consumer leased, acted, acked and replied.
  assert.ok(
    report.lease.some((entry) => entry.consumer === 'consumer-1' && entry.held === true),
    'the consumer never held the lease',
  );
  assert.deepEqual(
    report.actings.map((acting) => [acting.id, acting.outcome, acting.action]),
    [['m1', 'acted', 'reply']],
  );
  assert.deepEqual(
    report.acks.map((ack) => [ack.id, ack.outcome]),
    [['m1', 'acked']],
  );

  // The reply is in the outbox, the message is acked in the inbox, and the
  // ledger has moved the record all the way to acked.
  assert.deepEqual(
    report.store.outbox.map((message) => message.id),
    [replyId('m1')],
  );
  assert.deepEqual(
    report.store.inbox.map((message) => [message.id, message.acked]),
    [['m1', true]],
  );
  assert.deepEqual(
    report.store.mailbox.seen.map((entry) => [entry.id, entry.status]),
    [['m1', 'acked']],
  );

  // The heartbeat is the holder's write and carries no id of its own.
  assert.ok(report.store.heartbeat !== null, 'the holder wrote no heartbeat');

  // And nothing was faulted: this is the base case the faulted runs compare to.
  assert.deepEqual(report.faults, []);
});

test('the simulator duplicates a delivery and the outcome is unchanged', () => {
  const clean = scenario(emptySchedule({ seed: 1 }));
  const duplicated = scenario({
    ...emptySchedule({ seed: 1 }),
    faults: [{ frame: 1, op: FAULT.DUPLICATE }],
  });

  // The duplicate really happened, and the store's id key absorbed it: two
  // deliveries of one message are one message.
  assert.equal(duplicated.faults.length, 1);
  assert.equal(duplicated.faults[0].op, FAULT.DUPLICATE);
  assert.equal(duplicated.store.inbox.length, 1);
  assert.equal(clean.store.inbox.length, 1);

  assert.deepEqual(outcomeOf(duplicated), outcomeOf(clean), failure(duplicated));
});

test('the simulator reorders two deliveries and the outcome is the same', () => {
  const script = (schedule) => {
    const run = simulator({ schedule });
    run.consumer('consumer-1');
    run.phone.writeInbox({ id: 'm1', text: 'first' });
    run.phone.writeInbox({ id: 'm2', text: 'second' });
    run.run({ ticks: 4, intervalMs: SECOND });
    return run.report();
  };

  const clean = script(emptySchedule({ seed: 2 }));
  const reordered = script({ ...emptySchedule({ seed: 2 }), faults: [{ frame: 1, op: FAULT.REORDER }] });

  // The two messages reached the store the other way round...
  assert.deepEqual(
    clean.store.inbox.map((message) => message.id),
    ['m1', 'm2'],
  );
  assert.deepEqual(
    reordered.store.inbox.map((message) => message.id),
    ['m2', 'm1'],
  );

  // ...and the outcome is the same, because the ledger's order is the
  // message's own (at, sender, id) and never the order the network delivered.
  assert.deepEqual(outcomeOf(reordered), outcomeOf(clean), failure(reordered));
  assert.deepEqual(
    reordered.actings.map((acting) => acting.id),
    ['m1', 'm2'],
  );
});

test('the simulator drops the ack and the consumer re-delivers, recognises the duplicate and re-acks without re-acting', () => {
  const clean = scenario(emptySchedule({ seed: 3 }));
  const ackFrame = clean.frames.find((frame) => frame.op === 'ack-write');
  assert.ok(ackFrame !== undefined, 'the clean run wrote no ack to drop');

  const run = simulator({
    schedule: { ...emptySchedule({ seed: 3 }), faults: [{ frame: ackFrame.seq, op: FAULT.DROP }] },
  });
  run.consumer('consumer-1');
  run.phone.writeInbox({ id: 'm1', text: 'turn on the kitchen light' });
  run.run({ ticks: 4, intervalMs: SECOND });
  const report = run.report();

  assert.equal(report.faults.length, 1);
  assert.equal(report.faults[0].op, FAULT.DROP);

  // It acted exactly once: the redelivery was answered, not acted on again.
  assert.equal(report.actings.filter((acting) => acting.outcome === 'acted').length, 1);
  assert.equal(report.actings.filter((acting) => acting.action !== null).length, 1);

  // It was redelivered, the redelivery was recognised as a duplicate, and the
  // ack was written again — after which the ledger could ack it.
  assert.ok(
    report.deliveries.some((delivery) => delivery.id === 'm1' && delivery.outcome === 'duplicate'),
    'the dropped ack produced no redelivery',
  );
  assert.ok(report.acks.some((ack) => ack.id === 'm1' && ack.outcome === 'acked'));
  assert.equal(report.store.inbox.find((message) => message.id === 'm1').acked, true);

  // One reply, not two: a re-ack is not a second answer.
  assert.equal(report.store.outbox.length, 1);
  assert.deepEqual(
    report.store.mailbox.seen.map((entry) => [entry.id, entry.status]),
    [['m1', 'acked']],
  );
});

test('a retry that lands after the ack does not clear it', () => {
  // The store's ack is not the phone's field to clear. The phone re-sends its
  // own message until the outbox answers it, so a retry can land *after* a
  // consumer has acked that very record — and a write that replaced the record
  // would leave the store saying "unacked" while the ledger holds it as
  // consumed, which nothing repairs: the ledger's ack step only acks a record
  // it has acted on. The message would then be redelivered for ever, and the
  // store would disagree with the ledger about whether it was ever consumed.
  const run = simulator();
  run.consumer('consumer-1');
  run.phone.writeInbox({ id: 'm1', text: 'hello' });
  run.tick();
  assert.equal(run.store.inbox.get('m1').acked, true);

  // The same message again, `at` included: the retry the caller's duty allows.
  run.phone.writeInbox({ id: 'm1', text: 'hello' });
  run.drain();
  assert.equal(run.store.inbox.get('m1').acked, true, 'the retry cleared the ack');

  // And the outcome is what it was: one action, one answer, one record.
  run.advance(SECOND);
  run.tick();
  const report = run.report();
  assert.equal(report.actings.filter((acting) => acting.outcome === 'acted').length, 1);
  assert.equal(report.store.outbox.length, 1);
  assert.deepEqual(
    report.store.mailbox.seen.map((entry) => [entry.id, entry.status]),
    [['m1', 'acked']],
  );
});

// ---------------------------------------------------------------------------
// The properties, over many seeded schedules
// ---------------------------------------------------------------------------

test('at most once acting per (sender,id), under any duplicate, reorder or drop schedule', () => {
  for (let seed = 0; seed < 120; seed += 1) {
    const report = sweep(seeded(seed));
    const acted = new Map();
    for (const acting of report.actings) {
      if (acting.outcome !== 'acted' && acting.outcome !== 'stale-pending') continue;
      const key = `${acting.sender} ${acting.id}`;
      acted.set(key, (acted.get(key) ?? 0) + 1);
    }
    for (const [key, count] of acted) {
      assert.equal(count, 1, `acted ${count} times on ${key}\n${failure(report)}`);
    }
  }
});

test('at least once delivery: every message the phone wrote reaches the ledger', () => {
  for (let seed = 0; seed < 120; seed += 1) {
    const report = sweep(seeded(seed));
    for (const message of report.phone.written) {
      // The delivery was accepted or answered as a duplicate — an outcome the
      // *ledger* gave, not a count the harness kept — so a message lost on the
      // wire, or one the consumer never got round to delivering, fails here.
      assert.ok(
        report.deliveries.some(
          (delivery) =>
            delivery.id === message.id &&
            (delivery.outcome === 'accepted' || delivery.outcome === 'duplicate'),
        ),
        `${message.id} was never delivered\n${failure(report)}`,
      );
      // And it is in the ledger, which is where "delivered" is recorded.
      assert.ok(
        report.store.mailbox.seen.some((entry) => entry.id === message.id),
        `${message.id} never reached the ledger\n${failure(report)}`,
      );
    }
  }
});

test('a holder delivers every unacked message it can see, and never one that is already acked', () => {
  // Driven a pass at a time, because this is a fact about one pass and the
  // sweep only reports the end of the run. Three messages arrive; the holder
  // delivers all three in its first pass, and by its second every one of them
  // is acked — so a loop that delivered an acked message, or that stopped at
  // the oldest, would fail here.
  const run = simulator();
  run.consumer('consumer-1');
  for (const id of ['m1', 'm2', 'm3']) run.phone.writeInbox({ id, text: `turn ${id} on` });
  run.tick();
  run.advance(SECOND);
  run.tick();
  const report = run.report();

  const deliveriesOf = (pass) =>
    report.deliveries.filter((delivery) => delivery.pass === pass).map((delivery) => delivery.id).sort();
  assert.deepEqual(deliveriesOf(1), ['m1', 'm2', 'm3'], failure(report));
  assert.deepEqual(deliveriesOf(2), [], failure(report));
  assert.ok(report.store.inbox.every((message) => message.acked === true));
  // And each of them was acted on exactly once, in the pass that delivered it.
  assert.deepEqual(
    report.actings.map((acting) => [acting.id, acting.outcome]),
    [['m1', 'acted'], ['m2', 'acted'], ['m3', 'acted']],
  );
});

test('one lease holder at a time: one consumer wins the commit per pass, and only a winner acts', () => {
  for (let seed = 0; seed < 120; seed += 1) {
    const report = sweep(seeded(seed));
    const winners = new Map();
    const holders = new Set();
    for (const commit of report.commits) {
      if (commit.phase !== 'deliver' || !commit.ok) continue;
      winners.set(commit.pass, (winners.get(commit.pass) ?? 0) + 1);
      holders.add(`${commit.pass} ${commit.consumer}`);
    }
    for (const [pass, count] of winners) {
      assert.equal(count, 1, `${count} consumers won the lease in pass ${pass}\n${failure(report)}`);
    }
    for (const acting of report.actings) {
      assert.ok(
        holders.has(`${acting.pass} ${acting.consumer}`),
        `${acting.consumer} acted in pass ${acting.pass} without winning the lease\n${failure(report)}`,
      );
    }
    // And no pass has two consumers acting in it.
    const byPass = new Map();
    for (const acting of report.actings) {
      if (!byPass.has(acting.pass)) byPass.set(acting.pass, new Set());
      byPass.get(acting.pass).add(acting.consumer);
    }
    for (const [pass, consumers] of byPass) {
      assert.equal(consumers.size, 1, `${consumers.size} consumers acted in pass ${pass}\n${failure(report)}`);
    }
  }
});

test('a generated schedule skews a clock only inside the tolerance the lease and the mailbox name', () => {
  for (let seed = 0; seed < 200; seed += 1) {
    const schedule = seeded(seed);
    for (const actor of Object.keys(schedule.clocks)) {
      assert.ok(
        Math.abs(schedule.clocks[actor]) <= MAX_CLOCK_SKEW,
        `seed ${seed} skewed ${actor} by ${schedule.clocks[actor]}ms`,
      );
    }
  }
});

test('a skew inside the tolerance is absorbed; one millisecond beyond it the mailbox refuses the delivery', () => {
  // One tick, so the clock has not moved and the skew is exactly what the
  // schedule says: the simulated clock advancing is a second variable, and
  // this test is about the boundary and nothing else.
  const inside = simulator({
    schedule: { ...emptySchedule({ seed: 4 }), clocks: { [PHONE]: MAX_CLOCK_SKEW } },
  });
  inside.consumer('consumer-1');
  inside.phone.writeInbox({ id: 'm1', text: 'hello' });
  inside.run({ ticks: 1 });
  const accepted = inside.report();
  assert.equal(accepted.deliveries.filter((d) => d.code === 'future-timestamp').length, 0);
  assert.equal(accepted.actings.length, 1);

  const beyond = simulator({
    schedule: { ...emptySchedule({ seed: 4 }), clocks: { [PHONE]: MAX_CLOCK_SKEW + 1 } },
  });
  beyond.consumer('consumer-1');
  beyond.phone.writeInbox({ id: 'm1', text: 'hello' });
  beyond.run({ ticks: 1 });
  const refused = beyond.report();
  assert.ok(refused.deliveries.some((d) => d.id === 'm1' && d.code === 'future-timestamp'));
  assert.equal(refused.actings.length, 0);
  assert.equal(refused.store.outbox.length, 0);
});

test('a consumer whose plan is ready but whose lease is gone acts on nothing', () => {
  // The takeover is arranged between one consumer's delivery and its turn to
  // act, so it is holding a plan of its own when it loses the lease. The
  // compare-and-swap does not save it here: its snapshot is still the current
  // generation, so the lease is the only thing between it and a second action
  // on a message it has already acted on.
  const run = simulator();
  const first = run.consumer('consumer-1');
  const second = run.consumer('consumer-2');
  run.phone.writeInbox({ id: 'm1', text: 'hello' });
  run.drain();

  first.deliverPlan();
  first.deliverCommit(); // consumer-1 takes the lease and delivers
  // The claim goes stale: the clock moves past the lease's whole TTL, which is
  // how a takeover happens between two consumers that agree on the time.
  run.advance(CLAIM_TTL + MINUTE);
  second.deliverPlan();
  second.deliverCommit(); // consumer-2 takes it over and commits
  first.actPlan(); // its own plan, and no longer its own lease
  first.actCommit();

  const report = run.report();
  assert.equal(report.actings.length, 0, failure(report));
  assert.deepEqual(
    report.store.mailbox.seen.map((entry) => entry.status),
    ['accepted'],
  );
  assert.equal(report.store.outbox.length, 0);
});

test('a consumer that takes the lease over does not act on what was already acted on', () => {
  // consumer-1's clock lags the phone's by the whole tolerance and
  // consumer-2's leads it, so consumer-1's claim is already stale when
  // consumer-2 reads it: a takeover inside the tolerance, every pass.
  const report = sweep({
    ...emptySchedule({ seed: 5 }),
    clocks: { [PHONE]: 0, 'consumer-1': -MAX_CLOCK_SKEW, 'consumer-2': MAX_CLOCK_SKEW },
  });

  assert.ok(
    report.lease.some((entry) => entry.consumer === 'consumer-2' && entry.held === true),
    'the second consumer never took the lease over, so nothing was proved',
  );
  const acted = report.actings.filter((acting) => acting.outcome === 'acted');
  const keys = new Set(acted.map((acting) => `${acting.sender} ${acting.id}`));
  assert.equal(acted.length, keys.size, `a takeover re-acted a message\n${failure(report)}`);
});

test('a lease stamped ahead by more than the skew tolerance is treated as expired; behind it is held until its TTL', () => {
  // The lease's skew rule, exercised through the harness rather than restated:
  // consumer-1 takes the lease with its clock skewed by `skew`, and consumer-2
  // (no skew) tries to take it at the same simulated instant. A lease stamped
  // ahead of the reader by at most MAX_CLOCK_SKEW is fresh; one ahead by more
  // is treated as expired, so no clock can hold the lease for ever; and one
  // stamped *behind* the reader is fresh until its TTL, whatever the skew —
  // so the direction of the comparison is pinned as well as its bound.
  const takeoverAt = (skew) => {
    const run = simulator({ schedule: { ...emptySchedule({ seed: 6 }), clocks: { 'consumer-1': skew } } });
    const first = run.consumer('consumer-1');
    const second = run.consumer('consumer-2');
    first.pass();
    assert.equal(run.store.lease.claim.holder, 'consumer-1');
    second.pass();
    const report = run.report();
    return {
      holder: run.store.lease.claim.holder,
      second: report.lease.find((entry) => entry.consumer === 'consumer-2'),
    };
  };

  const inside = takeoverAt(MAX_CLOCK_SKEW);
  assert.equal(inside.holder, 'consumer-1');
  assert.equal(inside.second.held, false);
  assert.equal(inside.second.code, 'already-claimed');

  const beyond = takeoverAt(MAX_CLOCK_SKEW + 1);
  assert.equal(beyond.holder, 'consumer-2', 'a lease stamped beyond the tolerance held the lease');
  assert.equal(beyond.second.held, true);

  const behind = takeoverAt(-(MAX_CLOCK_SKEW + 1));
  assert.equal(behind.holder, 'consumer-1', 'a lease stamped behind the reader was taken before its TTL');
  assert.equal(behind.second.code, 'already-claimed');
});

test('the ledger acks each record at most once, and only once the store shows it acked', () => {
  for (let seed = 0; seed < 120; seed += 1) {
    const report = sweep(seeded(seed));
    const acked = new Map();
    for (const ack of report.acks) {
      assert.equal(ack.outcome, 'acked', `a ledger ack answered ${ack.outcome}\n${failure(report)}`);
      acked.set(ack.id, (acked.get(ack.id) ?? 0) + 1);
    }
    for (const [id, count] of acked) {
      assert.equal(count, 1, `the ledger acked ${id} ${count} times\n${failure(report)}`);
      assert.equal(
        report.store.inbox.find((message) => message.id === id)?.acked,
        true,
        `the ledger acked ${id}, which the store does not show acked\n${failure(report)}`,
      );
    }
  }
});

test('an expired prompt takes the safe path, and the harness invents no expiry rule of its own', () => {
  // Delivered after its TTL: refused, and never delivered at all.
  const late = simulator();
  late.consumer('consumer-1');
  late.phone.writeInbox({ id: 'q1', text: 'are you up?', kind: KIND.PROMPT });
  late.advance(PROMPT_TTL + 1);
  late.tick();
  const lateReport = late.report();
  assert.ok(lateReport.deliveries.some((d) => d.id === 'q1' && d.code === 'expired'));
  assert.equal(lateReport.actings.length, 0);
  assert.equal(lateReport.store.outbox.length, 0);

  // Delivered in time, acted on too late. The ledger consumes it exactly once
  // and says so — `stale-pending` — and the harness performs **nothing** with
  // it: what a late prompt *delivers* is P6.5's and P6.7's rule, both proposed
  // and NOT APPROVED, so no answer is invented for it. The store is settled
  // (every write the consumer owed has landed) before it is read, because a
  // half-flushed wire is not the run's outcome.
  const stale = simulator();
  const consumer = stale.consumer('consumer-1');
  stale.phone.writeInbox({ id: 'q2', text: 'are you up?', kind: KIND.PROMPT });
  stale.drain();
  consumer.deliverPlan();
  consumer.deliverCommit();
  assert.equal(stale.report().deliveries.at(-1).outcome, 'accepted');
  stale.advance(PROMPT_TTL + 1);
  consumer.actPlan();
  consumer.actCommit();
  stale.drain();
  const staleReport = stale.report();
  assert.deepEqual(
    staleReport.actings.map((acting) => [acting.id, acting.outcome, acting.action]),
    [['q2', 'stale-pending', null]],
  );
  assert.deepEqual(staleReport.store.outbox, [], 'the harness answered a prompt it has no rule for');
  // The ack is not the work: the ledger holds the record as `acted` — "an ack
  // is still owed" — so the consumer writes it, and the ledger's own ack step
  // runs on the next pass, once the store shows it.
  assert.equal(staleReport.store.inbox.find((message) => message.id === 'q2').acked, true);
  assert.deepEqual(
    staleReport.store.mailbox.seen.map((entry) => [entry.id, entry.status]),
    [['q2', 'acted']],
  );
  assert.deepEqual(staleReport.phone.pending.map((message) => message.id), ['q2']);

  consumer.pass();
  stale.drain();
  const ackedReport = stale.report();
  assert.deepEqual(
    ackedReport.store.mailbox.seen.map((entry) => [entry.id, entry.status]),
    [['q2', 'acked']],
  );
  assert.deepEqual(ackedReport.store.outbox, []);
});

test('a command older than the window is refused as a replay, and nothing is acted on', () => {
  const run = simulator();
  run.consumer('consumer-1');
  run.phone.writeInbox({ id: 'm1', text: 'turn it on' });
  run.advance(WINDOW + 1);
  run.tick();
  const report = run.report();
  assert.ok(report.deliveries.some((d) => d.id === 'm1' && d.code === 'replay'));
  assert.equal(report.actings.length, 0);
  assert.equal(report.store.outbox.length, 0);
});

test('the sweep is not vacuous: the seeded schedules really do fault, drop and act', () => {
  let faulted = 0;
  let dropped = 0;
  let acted = 0;
  for (let seed = 0; seed < 120; seed += 1) {
    const report = sweep(seeded(seed));
    if (report.faults.length > 0) faulted += 1;
    if (report.faults.some((fault) => fault.op === FAULT.DROP)) dropped += 1;
    if (report.actings.some((acting) => acting.outcome === 'acted')) acted += 1;
  }
  assert.ok(faulted >= 60, `only ${faulted} of 120 schedules faulted anything`);
  assert.ok(dropped >= 10, `only ${dropped} of 120 schedules dropped a frame`);
  assert.equal(acted, 120, `only ${acted} of 120 schedules acted on anything`);
});

// ---------------------------------------------------------------------------
// The schedule, as data
// ---------------------------------------------------------------------------

test('the same seed draws the same schedule, and different seeds draw different ones', () => {
  assert.deepEqual(seeded(7), seeded(7));
  const skews = new Set();
  for (let seed = 0; seed < 50; seed += 1) skews.add(seeded(seed).clocks['consumer-1']);
  assert.ok(skews.size > 10, `only ${skews.size} distinct skews over 50 seeds`);
});

test('faultFor is a pure function of the schedule and the frame number', () => {
  const schedule = seeded(11, { rate: 0.5 });
  const forward = [];
  for (let frame = 1; frame <= 40; frame += 1) forward.push(faultFor(schedule, frame));
  const backward = [];
  for (let frame = 40; frame >= 1; frame -= 1) backward.push(faultFor(schedule, frame));
  backward.reverse();
  assert.deepEqual(backward, forward);

  // And the vocabulary really is exercised: over the seeds the sweep runs,
  // every fault the model names is drawn at least once.
  const drawn = new Set();
  for (let seed = 0; seed < 200; seed += 1) {
    const drawnSchedule = seeded(seed, { rate: 0.5 });
    for (let frame = 1; frame <= 8; frame += 1) {
      const fault = faultFor(drawnSchedule, frame);
      if (fault !== null) drawn.add(fault.op);
    }
  }
  assert.deepEqual([...drawn].sort(), [...WIRE_OPS].sort());
});

test('a delay holds a frame until the clock reaches it, and the frame\'s at does not move', () => {
  const run = simulator({
    schedule: { ...emptySchedule({ seed: 1 }), faults: [{ frame: 1, op: FAULT.DELAY, delayMs: 30 * SECOND }] },
  });
  run.phone.writeInbox({ id: 'm1', text: 'hello' });
  assert.equal(run.store.inbox.size, 0);
  run.advance(30 * SECOND - 1);
  assert.equal(run.store.inbox.size, 0, 'the frame arrived before its delay was up');
  run.advance(1);
  assert.equal(run.store.inbox.size, 1);
  assert.equal(run.store.inbox.get('m1').at, DEFAULT_START_MS, 'the delay moved the message\'s own time');
});

test('a dropped write is recovered by a retry that is the same message, its at included', () => {
  const run = simulator({
    schedule: { ...emptySchedule({ seed: 1 }), faults: [{ frame: 1, op: FAULT.DROP }] },
  });
  run.consumer('consumer-1');
  run.phone.writeInbox({ id: 'm1', text: 'hello' });
  // The clock moves before the retry, so a retry that stamped a fresh `at`
  // would be a *different* message and the mailbox would refuse it as a
  // conflict: the same `at` is what the duty is about, not the same text.
  run.advance(SECOND);
  run.tick();
  const report = run.report();
  assert.equal(report.faults.length, 1);
  const writes = report.frames.filter((frame) => frame.op === 'inbox-write');
  assert.ok(writes.length >= 2, 'the phone did not retry the dropped write');
  assert.equal(writes[0].at, writes[1].at, 'the retry carried a new at, which the mailbox forbids');
  assert.equal(report.store.inbox.length, 1);
  assert.equal(report.actings.length, 1);
});

test('a run is reproducible from its seed and from the schedule a failure prints', () => {
  const first = sweep(seeded(7, { rate: 0.5 }));
  const second = sweep(seeded(7, { rate: 0.5 }));
  assert.deepEqual(second, first);

  assert.equal(typeof first.scheduleText, 'string');
  const replayed = sweep(parseScheduleText(first.scheduleText));
  assert.deepEqual(replayed, first);

  // The printed form is the run's own faults written out by frame number,
  // which is what makes it a replay rather than a re-draw.
  const printed = JSON.parse(first.scheduleText);
  assert.deepEqual(printed.faults, first.faults.map((fault) => fault.applied));
  assert.equal(scheduleText(parseScheduleText(first.scheduleText)), first.scheduleText);
});

test('the printed schedule is canonical: equal schedules print equal text, faults in frame order', () => {
  // The same schedule, written two ways. A failure message prints this text, so
  // two runs that differ only in how their schedule was typed must print the
  // same line — otherwise a printed schedule would not be a replay of the run
  // that failed.
  const forward = {
    ...emptySchedule({ seed: 2 }),
    faults: [
      { frame: 1, op: FAULT.DROP },
      { frame: 3, op: FAULT.DUPLICATE },
      { frame: 4, op: FAULT.DELAY, delayMs: 5 * SECOND },
    ],
  };
  const backward = { ...forward, faults: [...forward.faults].reverse() };
  assert.equal(scheduleText(backward), scheduleText(forward));
  assert.deepEqual(
    JSON.parse(scheduleText(backward)).faults.map((fault) => fault.frame),
    [1, 3, 4],
  );
  // And a schedule this module refuses prints nothing rather than half of it.
  assert.equal(scheduleText({ ...forward, rate: 0.5 }), '');
});

test('a report survives JSON, so a failure message can print it', () => {
  const report = sweep(seeded(5));
  const round = JSON.parse(JSON.stringify(report));
  assert.equal(round.seed, report.seed);
  assert.equal(round.scheduleText, report.scheduleText);
  assert.deepEqual(round.store.mailbox.seen, report.store.mailbox.seen);
  assert.deepEqual(round.actings, report.actings);
});

test('the seeded generator is deterministic, bounded, and refuses a seed it could not replay', () => {
  const first = makeRng(42);
  const second = makeRng(42);
  assert.equal(first.ok, true);
  const left = [];
  const right = [];
  for (let index = 0; index < 64; index += 1) {
    left.push(first.next());
    right.push(second.next());
  }
  assert.deepEqual(left, right);
  assert.ok(left.every((value) => value >= 0 && value < 1));
  const other = makeRng(43);
  const third = [];
  for (let index = 0; index < 64; index += 1) third.push(other.next());
  assert.notDeepEqual(third, left);
  assert.ok(left.every((value) => Number.isInteger(first.nextInt(7)) && first.nextInt(7) >= 0 && value >= 0));

  assert.deepEqual(makeRng(-1), { ok: false, code: REFUSAL.BAD_SEED, field: 'seed' });
  assert.deepEqual(makeRng(1.5), { ok: false, code: REFUSAL.BAD_SEED, field: 'seed' });
  assert.deepEqual(makeRng('7'), { ok: false, code: REFUSAL.BAD_SEED, field: 'seed' });
});

// ---------------------------------------------------------------------------
// Fail closed: the refusals, the store and the compare-and-swap
// ---------------------------------------------------------------------------

test('the simulator refuses a configuration the modules refuse, with their own codes', () => {
  assert.deepEqual(createSimulator({}), { ok: false, code: REFUSAL.MISSING_FIELD, field: 'promptTtlMs' });
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, nope: 1 }).code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(createSimulator({ promptTtlMs: 31 * 24 * HOUR }).code, 'invalid-prompt-ttl');
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, windowMs: 10 }).code, 'invalid-window');
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, maxSeen: 0 }).code, 'invalid-max-seen');
  // The prompt TTL, the start time and the lease TTL are the modules' rules,
  // and their refusals are the modules' codes: this harness composes them
  // rather than restating them.
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, startMs: 0 }).code, 'invalid-now');
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, clock: {} }).code, REFUSAL.NO_CLOCK);
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, leaseTtl: 25 * HOUR }).code, 'bad-ttl');
  assert.equal(createSimulator({ promptTtlMs: PROMPT_TTL, leaseTtl: 0 }).code, 'bad-ttl');
});

test('a schedule this module could not replay is refused rather than run', () => {
  const bad = [
    { seed: -1, rate: 0, ops: [], clocks: {}, maxDelayMs: 1, faults: [] },
    { seed: 1, rate: 2, ops: [], clocks: {}, maxDelayMs: 1, faults: [] },
    { seed: 1, rate: 0.5, ops: ['nope'], clocks: {}, maxDelayMs: 1, faults: [] },
    { seed: 1, rate: 0.5, ops: ['drop', 'drop'], clocks: {}, maxDelayMs: 1, faults: [] },
    { seed: 1, rate: 0, ops: [], clocks: { a: MAX_SKEW_MS + 1 }, maxDelayMs: 1, faults: [] },
    { seed: 1, rate: 0, ops: [], clocks: { a: 1.5 }, maxDelayMs: 1, faults: [] },
    { seed: 1, rate: 0.5, ops: ['drop'], clocks: {}, maxDelayMs: 1, faults: [{ frame: 1, op: 'drop' }] },
    { seed: 1, rate: 0, ops: [], clocks: {}, maxDelayMs: 1, faults: [{ frame: 0, op: 'drop' }] },
    { seed: 1, rate: 0, ops: [], clocks: {}, maxDelayMs: 1, faults: [{ frame: 1, op: 'delay' }] },
    { seed: 1, rate: 0, ops: [], clocks: {}, maxDelayMs: 1, faults: [{ frame: 1, op: 'drop', extra: 1 }] },
    { seed: 1, rate: 0, ops: [], clocks: {}, maxDelayMs: 1, faults: [{ frame: 1, op: 'delay', delayMs: 2 }] },
  ];
  for (const schedule of bad) {
    const made = createSimulator({ promptTtlMs: PROMPT_TTL, schedule });
    assert.equal(made.ok, false, `accepted a schedule it cannot replay: ${JSON.stringify(schedule)}`);
    assert.equal(typeof made.code, 'string');
  }
  assert.equal(emptySchedule({ seed: -1 }).ok, false);
  assert.equal(parseScheduleText('{').code, REFUSAL.BAD_SCHEDULE_TEXT);
  assert.equal(parseScheduleText('{"seed":1}').code, REFUSAL.MISSING_FIELD);
  assert.equal(parseScheduleText(7).code, REFUSAL.BAD_SCHEDULE_TEXT);
  assert.equal(scheduleText({ seed: 1 }), '');
  assert.equal(faultFor({ seed: 1 }, 1), null);
});

test('two consumers that read the same state cannot both write: the loser\'s commit is refused', () => {
  const store = createStore({ promptTtlMs: PROMPT_TTL });
  assert.equal(store.ok, true);
  const first = readSnapshot(store);
  const second = readSnapshot(store);

  // Both reads of one empty lease succeed, which is exactly the lease's
  // documented hazard: the take is not the fence, the write is.
  const taken = emptyLeaseState();
  const byFirst = takeLease(taken, { holder: 'consumer-1', at: DEFAULT_START_MS });
  const bySecond = takeLease(taken, { holder: 'consumer-2', at: DEFAULT_START_MS });
  assert.equal(byFirst.ok, true);
  assert.equal(bySecond.ok, true);

  const won = casCommit(store, first.generation, { lease: byFirst.state, mailbox: first.mailbox });
  assert.equal(won.ok, true);
  const lost = casCommit(store, second.generation, { lease: bySecond.state, mailbox: second.mailbox });
  assert.deepEqual(lost, { ok: false, code: REFUSAL.CAS_CONFLICT, field: 'generation' });
  assert.equal(store.lease.claim.holder, 'consumer-1');
});

test('the harness refuses a run it cannot finish, an actor it cannot name, and a message the schema refuses', () => {
  const run = simulator();
  run.consumer('consumer-1');
  assert.equal(run.run({ ticks: MAX_TICKS + 1 }).code, REFUSAL.TOO_MANY_TICKS);
  assert.equal(run.advance(-1).code, REFUSAL.BAD_MILLISECONDS);
  assert.equal(run.run({ intervalMs: -1 }).code, REFUSAL.BAD_MILLISECONDS);
  assert.equal(run.consumer(PHONE).code, REFUSAL.DUPLICATE_ACTOR);
  assert.equal(run.consumer('consumer-1').code, REFUSAL.DUPLICATE_ACTOR);
  assert.equal(run.consumer('').code, REFUSAL.BAD_TYPE);
  assert.equal(run.phone.writeInbox({ text: '' }).code, 'empty-string');
  assert.equal(run.phone.writeInbox({ text: 'hi', kind: 'note' }).code, REFUSAL.BAD_TYPE);
  assert.equal(run.phone.writeInbox({ text: 'hi', nope: 1 }).code, REFUSAL.UNKNOWN_FIELD);
  assert.equal(run.phone.writeInbox({ text: 'hi', id: 'a/b' }).code, 'bad-key');
});

test('the harness never writes a field into a message that the schema does not name', () => {
  const report = sweep(seeded(2));
  const messages = [...report.store.inbox, ...report.store.outbox];
  assert.ok(messages.length > 0);
  for (const message of messages) {
    assert.equal(Object.hasOwn(message, 'kind'), false, 'the kind leaked into the message');
    assert.equal(isMessage(message), true, JSON.stringify(message));
  }
});

test('a whole run reads neither the wall clock nor the platform random source', () => {
  const realNow = Date.now;
  const realRandom = Math.random;
  let touched = 0;
  Date.now = () => {
    touched += 1;
    return realNow();
  };
  Math.random = () => {
    touched += 1;
    return 0.5;
  };
  let acted = 0;
  try {
    const report = sweep(seeded(3, { rate: 0.5 }));
    acted = report.actings.length;
  } finally {
    Date.now = realNow;
    Math.random = realRandom;
  }
  assert.equal(touched, 0, 'the simulator read the machine instead of the schedule');
  assert.ok(acted > 0, 'the run acted on nothing, so it proved nothing');
});
