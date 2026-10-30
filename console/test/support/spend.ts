// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The parsed rows of a Spend model, read back the way the screen draws them.
//
// `SpendModel` carries no `rows` list of its own: the redesign's chart and table
// draw the document's rows a key at a time, through `SpendChart.groups` and
// `SpendChart.outside`, and a second list on the model would be a field no
// drawing reads. Three suites want the parsed rows themselves -- to hold the
// readers, to check which rows a control lets through, and to hold the
// screenshots' alt text to the fixture -- so they read them where the screen
// does, in one place rather than three.

import type { SpendModel, SpendRow } from '../../src/renderer/screens/spend'

/** drawnRows is the document's rows as the screen draws them: the row behind
 * each group of the chart, in the chart's own order, then the rows the range
 * does not cover. A group with no row behind it is a day the history does not
 * hold, and it is not a row. */
export function drawnRows(model: SpendModel): SpendRow[] {
  const rows: SpendRow[] = []
  for (const group of [...model.chart.groups, ...model.chart.outside]) {
    if (group.row !== null) rows.push(group.row)
  }
  return rows
}
