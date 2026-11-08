// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The short run ID: the one rule this console names a run by.
//
// A run ID is `YYYYMMDD-HHMMSS-` plus four lower-case hex digits
// (`20261108-115248-3bb2`), so its **short ID** is the last four characters,
// and that is what every list view draws and what a person types back into the
// CLI (docs/short-run-ids/spec.md, part 1). The CLI keeps the same rule in Go
// (`internal/task/shortid.go`, `task.ShortRun` and `task.DisplayRuns`, the
// plan's other half of it: SR1) and the two must not drift: this console's list
// views and `fugaro watch` name a run the same way, and a row that shows four
// characters on one side and five on the other is two answers to "which run is
// this" (reconciliation F2, D1706, and the parity test SR10 adds reads this
// module's table against the Go one).
//
// **Why a module and not a `slice(-4)` at each call site** -- which is what the
// tree had (`run.slice(-4)` in `screens/graph.ts`) -- is the collision rule:
// two runs printed together that share their last four characters are shown
// with as many extra characters as it takes to tell them apart, extending one
// character at a time from the end, the dash included (spec part 3; the CLI's
// `task.DisplayRuns`). One helper means one rule; three copies of it were the
// finding that started this work (F2).
//
// **Nothing here is document-shaped.** A run ID reaches the console as
// untrusted text, but this module never draws: it answers a string a renderer
// hands to its own text path, so there is no `literal` and no sanitizer here.
// A run ID that is not the CLI's shape is not refused either -- an ID cut to
// four characters is a display, not a decision, and refusing one would hide a
// row over a shape the *renderer* has no business policing.

/**
 * MIN_ENDING is the fewest characters an ending is shown with, and it is
 * `task.MinEnding`'s number: the CLI refuses an ending shorter than this
 * (`ErrTooShort`, D1701), so a shorter one shown here would be an input the
 * console draws and the CLI would not accept. Four is also exactly what is
 * drawn when nothing collides.
 */
export const MIN_ENDING = 4

/** RunRef is one run a list view prints: the repository it belongs to and its
 * full run ID. Both are needed and neither is optional -- the repository is
 * what separates two runs that share a run ID across repositories (the CLI's
 * `Locate` already calls that pair ambiguous, F15), and the run ID is what the
 * ending is cut from. */
export interface RunRef {
  readonly slug: string
  readonly runId: string
}

/** pointsOf is a run ID as code points, so `shortRun` and `displayRuns` cut it
 * the way Go's `task.ShortRun` does. A run ID is ASCII and this could be
 * `slice(-4)`; counting code points instead keeps the console's answer equal to
 * the CLI's for any other string a renderer might pass -- an unpaired surrogate
 * split in half is the one way `slice` would answer a different string than Go,
 * and the two helpers may not disagree about the same input (F2, D1706). */
function pointsOf(id: string): readonly string[] {
  return Array.from(id)
}

/** endingAt is the last `length` code points of a run ID, or the whole ID when
 * it has fewer -- so an extension never runs off the front of a short ID, and
 * the answer for one is the ID itself. */
function endingAt(points: readonly string[], length: number): string {
  return points.slice(Math.max(0, points.length - length)).join('')
}

/**
 * shortRun is a run's short ID: its last `MIN_ENDING` characters, or the whole
 * string when it is shorter (`task.ShortRun`, F2).
 *
 * The whole-string answer is the CLI's too, and it is not a special case worth
 * hiding: an ID short enough to fall inside four characters cannot be
 * abbreviated further, and the alternative -- padding or refusing -- would draw
 * a value no reader typed.
 */
export function shortRun(id: string): string {
  const points = pointsOf(id)
  if (points.length <= MIN_ENDING) return id
  return endingAt(points, MIN_ENDING)
}

/**
 * displayRuns is the ending to show for each of `refs`, in the order given.
 *
 * Each ending is the shortest suffix that is **unique among the runs passed**
 * and never shorter than `MIN_ENDING`, grown one character at a time from the
 * end until it tells its run apart from every other run *of a different ID*
 * (`task.DisplayRuns`, spec part 3). A renderer passes exactly the rows it
 * prints, so the ending is distinguishing for the display and nowhere else
 * (F15): the same run drawn alone shows four characters and drawn beside a
 * colliding run shows more.
 *
 * Two refs that carry the **same run ID in different repositories** cannot be
 * told apart by any suffix -- they end identically at every length -- so both
 * are shown as `<slug>/<ending>`, the repository in front of the ending, which
 * is the one thing that does separate them (F15, `task.DisplayRuns`). A run ID
 * that appears under two or more repositories is the trigger, not a repeated
 * row: two rows of one repository's run are the same run drawn twice, and the
 * CLI's rule words the case as "different repositories".
 *
 * **The answer is an array aligned with `refs`, not a map keyed by run ID.** A
 * map is what the Go helper keeps (`map[Ref]string`), but its key is the pair;
 * keyed by the run ID alone it could not hold the cross-repository case, and
 * keyed by a stringified pair it would be a second, private spelling of a
 * `RunRef` that every caller would have to reproduce. The callers are renderers
 * that walk their own rows in order (SR9 `live.ts`, SR10's list views), and for
 * them the ending belongs to the row, by position.
 */
export function displayRuns(refs: readonly RunRef[]): readonly string[] {
  const entries = refs.map((ref) => ({ ref, points: pointsOf(ref.runId) }))

  const slugsByRunId = new Map<string, Set<string>>()
  for (const { ref } of entries) {
    const slugs = slugsByRunId.get(ref.runId) ?? new Set<string>()
    slugs.add(ref.slug)
    slugsByRunId.set(ref.runId, slugs)
  }

  return entries.map((entry, index) => {
    let length = Math.min(MIN_ENDING, entry.points.length)
    while (length < entry.points.length) {
      const candidate = endingAt(entry.points, length)
      const collides = entries.some(
        (other, otherIndex) =>
          otherIndex !== index &&
          other.ref.runId !== entry.ref.runId &&
          endingAt(other.points, length) === candidate,
      )
      if (!collides) break
      length += 1
    }
    const ending = endingAt(entry.points, length)
    const repos = slugsByRunId.get(entry.ref.runId)
    const acrossRepos = repos !== undefined && repos.size > 1
    return acrossRepos ? `${entry.ref.slug}/${ending}` : ending
  })
}
