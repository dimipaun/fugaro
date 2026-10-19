// The mailbox's one door (mobile plan, Task P3.6; D111, D113, D113b, D118).
//
// A screen imports `src/mailbox/index.js`. It builds no store, no redactor and
// no MAC: it gets a client from `createMailboxClient` and calls `send`,
// `flush`, `poll` and `pending` on it. Everything else here is the vocabulary
// those calls answer in -- the paths, the refusal codes, the presence reasons
// -- plus the two fakes (`MemoryMailboxStore`, `MemorySpeaker`) that exist so
// that a screen can be exercised without a database and without a device.
//
// **What is deliberately NOT exported here: the tag functions.** `mac.js` and
// `sha256.js` are exported by their own modules and are used by the client, by
// their own tests, and by nothing else; a screen that could compute a tag is a
// screen that could put a key in a variable. The client is the only caller, and
// it holds the key inside one `SecretStore.use` callback.
//
// **What is deliberately not here either: the ports.** `store.js`'s
// `MemoryMailboxStore` is a fake and belongs here, but the real adapter (the
// Realtime Database SDK) is built in `mobile/index.js` -- the one file that
// knows what a device is -- and passed to `createMailboxClient` as the `store`
// option. A screen that imported an adapter could build a second write path,
// which is the whole of what this task fences.

export {
  CLIENT_CODES,
  EXPIRED,
  MAILBOX_TTL_MS,
  MAC_KEY_ITEM,
  PRESENCE_REASON,
  createMailboxClient,
  toPresenceHeartbeat,
} from './client.js';

export { MAILBOX_ROOT, isDatabaseKey, mailboxPaths } from './paths.js';

export { STORE_CODES, MemoryMailboxStore } from './store.js';

export { SPEAK_CODES, MemorySpeaker } from './speaker.js';

export { REDACT_CODES, outboundRedactor } from './redact.js';
