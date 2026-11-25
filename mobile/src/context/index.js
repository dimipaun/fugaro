// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The context module's one door (mobile identity and pairing plan, tasks Z1-Z3;
// D2789).
//
// A screen imports `src/context/index.js` and draws `contextGauge` per paired
// session, printing `gaugeLabel`. Everything else here is the vocabulary that
// answer is written in.
//
// **The door is Z1's today, and Z2 and Z3 add to it.** Z2's compaction and Z3's
// usage meter are the same screen's other two readings; each mounts its own
// file beside `gauge.js` and re-exports it here, which is why this file exists
// before there is a second thing in it.
//
// **What is deliberately NOT here.** A source of readings: `/context`'s output
// is what a fullness reading would be parsed from, and Z0 found no other
// reporter, so nothing this module exports reads a session, starts a turn or
// holds a cache. A screen hands the gauge what its own source produced, and
// with no source -- the app's state today -- the gauge says `unavailable`. No
// port either, for `src/jobs/index.js`'s reason: a session arrives as C2's
// value, and a module that could build one could open a second door to a
// project.

export { GAUGE_CODES, GAUGE_REASONS, GAUGE_STATES, contextGauge, gaugeLabel, isGauge } from './gauge.js';
