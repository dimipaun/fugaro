// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The job list's one door (mobile plan, Task P3.3; resolved 2026-10-17).
//
// A screen imports this file and gets the list, the row, the heartbeat line and
// the vocabulary they are written in -- not `row.js`'s field judgements or
// `heartbeat.js`'s, which are this directory's own business.
//
// **What is deliberately NOT on the door.**
//
//   * **A port, and a database client.** `createJobList` takes a port and hands
//     it to P3.2's reader; nothing here constructs one, imports a `firebase`
//     module or names a path. P3.1's boundary scan and P3.6's write-path scan
//     both walk this directory, and the app's one adapter stays
//     `mobile/index.js` (`src/mailbox/index.js`'s rule, applied here).
//   * **The feed's reader, its view and its availability vocabulary.** They
//     have their own door (`src/feed/index.js`), and a screen that needs a
//     figure's text or a liveness word imports it there. Re-exporting them here
//     would give the app two ways to reach one module, which is how two screens
//     come to disagree about what a state is called.
//   * **P0.3's URL validator.** It is merged (`shared/url/index.js`) and it is
//     the gate for any link the app ever opens; this task draws no link, so
//     nothing here imports it. The dependency stands: the first task with a
//     link is the one that calls it.
//   * **A repository row.** `config/caps/repos/$slug` and `spend/$day/repos/$slug`
//     are granted, and the resolution of 2026-10-17 names repositories among the
//     things the list does not show: `scope.test.js` refuses the token in every
//     file here, and D2797 amended the ban on **run** rows without touching that
//     one.
//   * **A run's own record.** The runs bucket still carries no read for a user
//     claim, so nothing here names `runs/$slug/$run` or `agents/**`. What a
//     running row reads is the one `outcomes` node J1 opened, and `running.js`
//     is the only file `scope.test.js` allows that family in.

export {
  HEARTBEAT_STATES,
  JOB_FAMILIES,
  JOB_FIELDS,
  KILL_STATES,
  LIST_CODES,
  NOTHING,
  ROW_FIELDS,
} from './fields.js';

export { killState, projectRow } from './row.js';

export { READ_STATES, heartbeatRow } from './heartbeat.js';

export { JobList, createJobList, jobList } from './list.js';

export { RUN_STATES, RUNNING_CODES, runningJobs } from './running.js';

export { NO_REASON, NOT_SHOWN, SERIES, lines, text } from './lines.js';
