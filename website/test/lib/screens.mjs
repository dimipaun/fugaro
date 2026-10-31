// website/test/lib/screens.mjs — a screenshots data file, read the way a gate has to read it.
//
// Two files of gates read `website/data/*-screens.yaml`: `home.mjs` holds the homepage's "See it"
// panel to the file row for row (the brief of 2026-10-21), and `landing-pages.mjs` holds the four
// campaign pages' slider to the same rows (D412's finding 3). Both need the same two things — the
// YAML scalar and the rows — and they are written once, here.
//
// **Why a literal reader rather than the build's `loadHomeScreens()`**: the loader is what puts
// these rows on the page, so a gate that read them through it would be asking the page whether it
// agrees with itself. The rows are what an edit to the data file changes, and a gate that reads
// the file itself is what catches the file and the page disagreeing.
//
// It lives under `test/lib/` rather than beside the gates because `npm run check` runs
// `node --test test/*.mjs`: a helper at the top level would be run as a test file of its own.
// Standard library only, and no network (Global Constraint 4).

import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './html.mjs'

/** A YAML scalar as the site's own reader reads it: quoted or plain, and no escapes beyond the
 *  two its reader knows (`\"` and `\\`), which is the subset these rows are written in. */
export function yamlScalar(raw) {
  const value = raw.trim()
  if (value.startsWith('"') && value.endsWith('"')) return value.slice(1, -1).replace(/\\(["\\])/g, '$1')
  return value
}

/**
 * A screenshots data file, read literally: one row per `- file:` line, with the keys under it.
 * `relative` is a path in the checkout. `dir` is read along with the rest because it is what
 * tells the Watch capture — the one row of the homepage's file that lives under `public/images/`
 * rather than under `public/screenshots/` — apart from a console row.
 */
export function readScreenRows(relative) {
  const file = path.join(REPO, relative)
  assert.ok(existsSync(file), `${relative} is missing: this check reads the repository, not the build`)
  const out = []
  let row = null
  for (const line of readFileSync(file, 'utf8').split('\n')) {
    if (line.trim() === '' || line.trim().startsWith('#')) continue
    let match
    if ((match = /^ {2}- file: (\S+)$/.exec(line))) out.push((row = { file: match[1], dir: 'screenshots' }))
    else if (row && (match = /^ {4}(alt|caption|dir|height|width): (.*)$/.exec(line))) {
      row[match[1]] = match[1] === 'width' || match[1] === 'height' ? Number(match[2].trim()) : yamlScalar(match[2])
    }
  }
  return out
}
