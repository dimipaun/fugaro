// The severity ladder (mobile plan, Task P1.3).
//
// The plan states the order in one sentence: "a kill is the highest severity;
// a failed finish is next; a fast burn is next; nothing else interrupts". The
// sources are the fields the contract already carries
// (`internal/watch/json.go`'s `JSONKill`, `JSONFinished.Failed`/`Status`,
// `JSONBurn.FastAlert`), which is voice D48's ruling: severity comes from the
// fields that exist, and the digester does not recompute a figure the CLI has
// already computed.
//
// This module is the order and nothing else -- the classification of a
// document against it is `index.js`. Keeping the two apart is what lets the
// order be tested as an order (strict, total, transitive) rather than through
// a document.

import { DigestError, ERROR } from './errors.js';

/**
 * The four severities. The values are the wire spelling, and they are ordered
 * most severe first in `SEVERITY_ORDER`.
 */
export const SEVERITY = Object.freeze({
  kill: 'kill',
  failed: 'failed',
  fast_burn: 'fast_burn',
  info: 'info',
});

/**
 * The ladder, most severe first. It is exhaustive by construction: `SEVERITY`
 * and this array are the only two places a severity is written down, and the
 * tests assert they agree.
 */
export const SEVERITY_ORDER = Object.freeze([
  SEVERITY.kill,
  SEVERITY.failed,
  SEVERITY.fast_burn,
  SEVERITY.info,
]);

/**
 * The rank of a severity: larger is more severe, and `kill` is the largest.
 * Refuses a value that is not on the ladder rather than returning a default --
 * a severity nobody defined must not silently rank as `info`.
 */
export function severityRank(severity) {
  const i = SEVERITY_ORDER.indexOf(severity);
  if (i < 0) {
    throw new DigestError(ERROR.SEVERITY_UNKNOWN, 'the severity ladder');
  }
  return SEVERITY_ORDER.length - i;
}

/** True when a is strictly more severe than b. Both must be on the ladder. */
export function moreSevere(a, b) {
  return severityRank(a) > severityRank(b);
}

/**
 * The most severe of a list of severities. A digest with no items at all is
 * `info`: an empty document is not an alarm. An unknown severity in the list
 * is refused, not skipped.
 */
export function mostSevere(severities) {
  let worst = SEVERITY.info;
  for (const s of severities) {
    if (severityRank(s) > severityRank(worst)) {
      worst = s;
    }
  }
  return worst;
}
