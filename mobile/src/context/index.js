// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The context module's one door (mobile identity and pairing plan, tasks Z1-Z3;
// D2789).
//
// A screen imports `src/context/index.js` and draws `contextGauge` per paired
// session, printing `gaugeLabel`; the same screen's compact button and automatic
// compaction are `compactButton` and `autoCompaction`, and everything else here
// is the vocabulary those three are written in.
//
// **The door is Z1's and Z2's today, and Z3 adds to it.** Z3's usage meter is
// the same screen's third reading; it mounts its own file beside `gauge.js` and
// re-exports it here, which is why this file exists before there is a last thing
// in it.
//
// **What is deliberately NOT here.** A source of readings: `/context`'s output
// is what a fullness reading would be parsed from, and Z0 found no other
// reporter, so nothing this module exports reads a session, starts a turn or
// holds a cache. A screen hands the gauge what its own source produced, and with
// no source -- the app's state today -- the gauge says `unavailable`. No send
// either: the compaction answers the command a screen sends with, and the app's
// one write path stays the mailbox client (D113). No port, for `src/jobs/index.js`'s
// reason: a session arrives as C2's value, and a module that could build one
// could open a second door to a project.

export { GAUGE_CODES, GAUGE_REASONS, GAUGE_STATES, contextGauge, gaugeLabel, isGauge } from './gauge.js';

export {
  AGENT_STATES,
  COMPACT_COMMAND,
  COMPACTION_CODES,
  COMPACTION_REASONS,
  COMPACTION_STATES,
  autoCompaction,
  compactButton,
  compactionCommand,
  isCompaction,
} from './compaction.js';
