// The demo's transport: the app's in-memory database double, with the OTHER
// end of the mailbox already in it (owner ruling of 2026-10-19).
//
// **What the demo is showing, and why it is not a mock-up.** A reviewer must
// see the app's own behaviour, not a picture of it, so nothing here answers a
// call the app makes: the client is P3.6's real `createMailboxClient`, the
// store is P3.6's real `MemoryMailboxStore` (the port's double, with its read
// semantics, its subtree shapes and the two rule models a re-send meets), the
// speaker is P3.6's real `MemorySpeaker`, and the words the simulant writes
// are tagged by the app's own MAC code (`../mailbox/mac.js`, the implementation
// the client verifies with -- the same one, so a wrong tag is dropped by the
// real verifier rather than by a check written for the demo).
//
// **The simulant is the consumer, and it writes first.** Everything the
// consumer would have written is delivered into the store before the client
// runs: its replies, the question it is holding, and the orchestrator's
// heartbeat (D113b). What the demo then does is what the app does -- send,
// poll, verify, speak, ack -- so the reviewer's screen shows the tags being
// checked and the acks landing, not a transcript written by hand.
//
// **The key is a placeholder** (`keys.js`), and it is here for the same reason
// it is anywhere: the tags must be real tags, made by the real code, or the
// client's verification would prove nothing. It signs `sample.js`'s fixed
// records and nothing else.
//
// **Why the simulant is not P1.5's simulator.** `shared/mailbox-sim` is the
// repository's other simulator, and it is the wrong tool HERE for the reason
// `src/mailbox/README.md` already records: it signs nothing (`PLACEHOLDER_MAC`)
// and sets no answering id at all, so the app's client would verify nothing it
// wrote and speak nothing it received. The demo would be a screen full of
// refusals. What is reused is the pair the tests already use for the same
// purpose -- the port double `MemoryMailboxStore` and the fake `MemorySpeaker`
// -- with the tags made by this app's own `mac.js`, which is exactly how
// `src/mailbox/client.test.js` builds the outbox fixtures it speaks.
//
// **What is deliberately absent.** No network, no filesystem, no clock, no
// random source and no platform storage: every value is either a fixed sample
// value, a counter (`demoIds`), or the output of a pure function. The demo's
// clock is `sample.js`'s `DEMO_NOW`, injected into the client like any other
// clock, so the demo is the same run every time it is entered -- which is what
// lets `index.test.js` assert that a reply was verified rather than hope it
// was.

import {
  MemoryMailboxStore,
  MemorySpeaker,
  mailboxPaths,
} from '../mailbox/index.js';
import { macOf } from '../mailbox/mac.js';
import { validateHeartbeat, validateMessage } from '../../../shared/mailbox-schema/index.js';
import { DEMO_ACCOUNT, DEMO_HEARTBEAT, DEMO_IDS, DEMO_NOW, DEMO_PROMPT, DEMO_REPLIES } from './sample.js';
import { DEMO_PAIRING_KEY } from './keys.js';

/** The direction a record is tagged for: the node it is written to (P6.3). */
const OUTBOX = 'outbox';
const HEARTBEAT = 'heartbeat';

/**
 * The demo's id source: a fixed counter, so the ids the client mints are
 * `demo-utterance-1`, `demo-utterance-2`, ... and the sample replies -- which
 * name those ids as their `reply_to` -- are answers to the sentences this demo
 * sends.
 *
 * **This is NOT the app's id source.** D117/P3.2's is what the real app uses
 * and it must be fresh across restarts (`src/mailbox/README.md` records the
 * contract); the demo's is a counter because the demo needs the ids to be
 * predictable, and nothing but a demo may use it.
 *
 * @returns {() => string} a fresh id per call
 */
export function demoIds() {
  let n = 0;
  return () => `${DEMO_IDS.UTTERANCE_PREFIX}${++n}`;
}

/**
 * Build the demo's transport: the port's double with the consumer's side of the
 * mailbox already delivered into it, and the speaker the client will speak
 * through.
 *
 * @param {object} [options]
 * @param {number} [options.now] the demo's clock (`sample.js`'s by default)
 * @param {string} [options.key] the pairing key to tag with (the placeholder)
 * @returns {{ store: object, speaker: object, paths: object, messages: ReadonlyArray<object>,
 *             prompt: object, heartbeat: object }}
 *   `messages` is every message the consumer wrote (its replies and the
 *   question), as delivered; `prompt` and `heartbeat` are handed back so the
 *   caller can put the question in P6.5's queue and read the presence rule
 *   without reading the store it just wrote.
 */
export function createDemoTransport(options = {}) {
  const now = options.now === undefined ? DEMO_NOW : options.now;
  const key = options.key === undefined ? DEMO_PAIRING_KEY : options.key;
  if (typeof key !== 'string' || key === '') {
    throw new TypeError('createDemoTransport: the pairing key must be a string');
  }

  const paths = mailboxPaths(DEMO_ACCOUNT.authUid);
  const store = new MemoryMailboxStore();
  const speaker = new MemorySpeaker();

  // The tag, made by the app's own code over the record WITHOUT its `mac`
  // (`canonicalBytes` omits the tag), so the client -- which recomputes it from
  // the bytes it reads -- verifies what this wrote or nothing at all.
  const tag = (record, direction) => Object.freeze({
    ...record,
    mac: macOf(record, key, direction),
  });

  const messages = [];
  for (const reply of DEMO_REPLIES) {
    // Every message in this mailbox is `sender`'s own (P2.1's template holds
    // the receiver to `sender == $mb` on both families), and the phone checks
    // it too.
    messages.push(tag({
      id: reply.id,
      sender: DEMO_ACCOUNT.authUid,
      kind: reply.kind,
      text: reply.text,
      at: reply.at,
      reply_to: reply.replyTo,
    }, OUTBOX));
  }
  const prompt = tag({
    id: DEMO_PROMPT.id,
    sender: DEMO_ACCOUNT.authUid,
    kind: DEMO_PROMPT.kind,
    text: DEMO_PROMPT.text,
    options: [...DEMO_PROMPT.options],
    at: DEMO_PROMPT.at,
  }, OUTBOX);
  messages.push(prompt);

  const heartbeat = tag({
    at: DEMO_HEARTBEAT.at === undefined ? now : DEMO_HEARTBEAT.at,
    boot: DEMO_HEARTBEAT.boot,
    agents: DEMO_HEARTBEAT.agents.map((agent) => ({ id: agent.id, name: agent.name })),
  }, HEARTBEAT);

  // The consumer's side is delivered FIRST, and by `deliver` rather than by a
  // phone write: this is the other end's work, and the ack the phone will write
  // for each of these messages needs their parent nodes to be there (the
  // template's `data.parent().exists()`).
  for (const message of messages) {
    store.deliver(paths.outboxMessage(message.id), message);
  }
  store.deliver(paths.heartbeat, heartbeat);

  // The sample data is validated by the schema the client will read it with,
  // here rather than later: a sample the schema would refuse would be a demo
  // that shows the validator's refusals and calls them the demo.
  for (const message of messages) {
    const verdict = validateMessage(message);
    if (!verdict.ok) {
      throw new TypeError(`createDemoTransport: the sample message ${message.id} is ${verdict.code}`);
    }
  }
  const beat = validateHeartbeat(heartbeat);
  if (!beat.ok) throw new TypeError(`createDemoTransport: the sample heartbeat is ${beat.code}`);

  return Object.freeze({
    store,
    speaker,
    paths,
    messages: Object.freeze(messages),
    prompt,
    heartbeat,
  });
}
