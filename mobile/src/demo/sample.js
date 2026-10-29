// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The demo's sample data (owner ruling of 2026-10-19: the built-in demo mode
// Google Play's reviewers reach everything the app shows through).
//
// **What this file is.** Every value a reviewer sees in demo mode, in one
// place, fixed: two installations, three jobs, one question a consumer is
// holding, one destructive command with the words that confirm it, and the
// replies the consumer sends back. Nothing here is read from a device, a
// network, a file or an environment variable, and nothing here is derived from
// any real installation: the names say what they are ("Demo installation",
// `demo-repo`), which is what makes a screenshot of demo mode obviously a
// screenshot of sample data.
//
// **What this file is NOT.** It is not a credential and not an installation:
// the tuple below is the *shape* D114's account has and no more -- a project
// that does not exist, a uid that is nobody, a Fugaro project that is a name.
// The one key the demo signs with is `keys.js`'s, and it is a placeholder this
// file's own tests assert is one.
//
// **Nothing here is signed.** A message's `mac` is computed at run time, by
// the app's own tag code, over these fixed records (`transport.js`) -- so the
// data below stays readable, and a reviewer can still see that the tag a
// reply carries is the tag its own bytes produce. The one exception is
// deliberate: the *time* is fixed too (`DEMO_AT`), so two runs of the demo are
// the same run, and every assertion about it is about the demo rather than
// about the machine's clock.

/**
 * The demo's clock, in ms since the epoch: a fixed instant, so two runs of the
 * demo produce the same messages, the same tags and the same presence.
 *
 * It is **the demo's** clock and nobody else's: the real app takes D117's
 * server-anchored `now` from the app, and nothing here is that.
 */
export const DEMO_NOW = 1_766_000_000_000;

/** D117's heartbeat interval, inside P1.4's band (its floor is 30 s). */
export const DEMO_INTERVAL_MS = 60_000;

/** The one installation the demo's client is built for. D114's tuple, fictional. */
export const DEMO_ACCOUNT = Object.freeze({
  id: 'demo-phone',
  firebaseProjectId: 'demo-project',
  authUid: 'demo-uid-0001',
  fugaroProject: 'demo-repo',
});

/** A second installation, listed and never opened: D114's list is several. */
export const DEMO_SECOND_ACCOUNT = Object.freeze({
  id: 'demo-tablet',
  firebaseProjectId: 'demo-project-two',
  authUid: 'demo-uid-0002',
  fugaroProject: 'demo-notes',
});

/**
 * What the demo's screen lists as installations. The label is the demo's own
 * (the owner types the label of a real installation; D114's tuple has no
 * `label` field, and this is display data rather than a second tuple shape).
 */
export const DEMO_INSTALLATIONS = Object.freeze([
  Object.freeze({ label: 'Demo installation', account: DEMO_ACCOUNT }),
  Object.freeze({ label: 'Demo installation (second phone)', account: DEMO_SECOND_ACCOUNT }),
]);

/**
 * The jobs the demo lists. **The job list is not on `main`** -- it is P3.3 --
 * so these are sample rows this screen renders itself, and the task that lands
 * P3.3's list takes them over. The states are the CLI's own three (`running`,
 * `succeeded`, `failed`), because a row whose state is a word nothing else in
 * the product uses would be a demo of a product that does not exist.
 */
export const DEMO_JOBS = Object.freeze([
  Object.freeze({ runId: 'demo-run-7', repo: 'demo-repo', title: 'Docs refactor', state: 'running' }),
  Object.freeze({ runId: 'demo-run-6', repo: 'demo-repo', title: 'Nightly tests', state: 'succeeded' }),
  Object.freeze({ runId: 'demo-run-5', repo: 'demo-notes', title: 'Release notes', state: 'failed' }),
]);

/**
 * The demo's id source's prefix. The app mints a fresh id per utterance and
 * the ids are opaque to everyone; the demo mints them from a counter so that
 * the fixed replies below can name the sentences they answer (`reply_to`).
 */
export const DEMO_IDS = Object.freeze({ UTTERANCE_PREFIX: 'demo-utterance-' });

/**
 * The utterances the demo sends when it is entered, in order: each one is a
 * **real signed write** by the app's own client, through the demo's transport.
 *
 * Their ids are the demo's own id source's (`transport.js`'s fixed counter), so
 * `demo-utterance-1` is the first one: the replies below name them, which is
 * what makes the consumer's answers answers to these sentences rather than to
 * nothing.
 */
export const DEMO_UTTERANCES = Object.freeze([
  'Demo: what is running on demo-repo?',
  'Demo: summarise run demo-run-7.',
]);

/**
 * The consumer's replies, one per utterance. `replyTo` is the id the demo's id
 * source mints for that utterance, and it is what makes the phone stop
 * re-delivering it (the client reads a verified `reply_to` as "this message was
 * answered").
 */
export const DEMO_REPLIES = Object.freeze([
  Object.freeze({
    id: 'demo-reply-1',
    kind: 'answer',
    text: 'Demo: two runs on demo-repo, one of them running.',
    replyTo: 'demo-utterance-1',
    at: DEMO_NOW - 20_000,
  }),
  Object.freeze({
    id: 'demo-reply-2',
    kind: 'answer',
    text: 'Demo: run demo-run-7 is on its review stage.',
    replyTo: 'demo-utterance-2',
    at: DEMO_NOW - 10_000,
  }),
]);

/**
 * The question a consumer is holding for this installation: a P6.3 message of
 * kind `prompt`, in the outbox (P6.7's shapes), with the answers it offers.
 * D127 makes its destructiveness phone-local and never part of the message,
 * which is why `DEMO_PROMPT_SAFE_ANSWER` is a value beside it and not a field
 * of it.
 */
export const DEMO_PROMPT = Object.freeze({
  id: 'demo-prompt-1',
  kind: 'prompt',
  text: 'Demo: deploy demo-repo to staging?',
  options: Object.freeze(['yes', 'no']),
  at: DEMO_NOW - 30_000,
});

/** What answering the question the non-destructive way means (D168/D173). */
export const DEMO_PROMPT_SAFE_ANSWER = 'no';

/**
 * The destructive command, its resolved target, and the confirmation the
 * reviewer would speak. The **read-back is not here**: it is built from the
 * resolved target by P6.2's own `readBack`, which is the rule that the second
 * pass hears the run id itself rather than the paraphrase that produced it.
 *
 * **No utterance id is here, and that is deliberate.** The id a command is
 * issued with is the id the app's client MINTS for the sentence it sends
 * (`client.send` answers with it), so the demo reads it from there rather than
 * fixing it here -- and the confirmation is sent as its own utterance, whose id
 * the client mints fresh. That is what keeps P6.2's rule (the utterance which
 * issued a command can never confirm it) true *by construction* in the demo
 * rather than by two fixed strings that happen to differ.
 */
export const DEMO_DESTRUCTIVE = Object.freeze({
  command: 'Demo: cancel run demo-run-7.',
  verb: 'cancel',
  target: Object.freeze({ kind: 'run', runId: 'demo-run-7' }),
  actionId: 'demo-action-1',
  confirmationId: 'demo-confirmation-1',
  confirmation: 'Fugaro, confirm',
});

/**
 * The orchestrator's heartbeat: signed, fresh at `DEMO_NOW`, and advertising
 * one agent -- so the demo's presence is `live` and its agent list is the
 * demo's rather than nothing.
 */
export const DEMO_HEARTBEAT = Object.freeze({
  at: DEMO_NOW,
  boot: 'demo-boot-1',
  agents: Object.freeze([Object.freeze({ id: 'demo-agent', name: 'Demo agent' })]),
});
