// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// `fugaro config list --json` as the console reads it (console/docs/json-contract.md
// §6.8).
//
// The document is the only source of the picker's rows: the console opens no
// config file itself (design section 3.4 rule 7), so everything the first run
// offers is what this parse keeps. Each property below is one a mutation can
// break, and the mutation is named where it is not obvious.

import { describe, expect, it } from 'vitest'

import {
  MAX_DISCOVERED_INSTALLATIONS,
  MAX_DOCUMENT_STRING,
  MAX_WARNING_LINES,
  isCliProjectName,
  parseInstallationsDocument,
} from '../../src/main/installlist'

/** document is the fixture from `console/docs/json-contract.md` §6.8: two
 * installations read off a machine, one with a shared config merged, and one
 * that could not be read at all -- listed by name, with its fields empty, and a
 * warning that says why. */
const DOCUMENT = {
  installations: [
    {
      name: 'sandbox',
      gcp_project: 'fugaro-sandbox-482301',
      region: 'europe-west4',
      shared_merged: true,
      shared_checked_at: '2026-10-19T08:11:04Z',
    },
    { name: 'acme', gcp_project: '', region: '', shared_merged: false, shared_checked_at: null },
  ],
  warnings: ['/Users/owner/.config/fugaro/projects/acme.yaml could not be read: permission denied'],
}

function parsed(text: unknown): ReturnType<typeof parseInstallationsDocument> {
  return parseInstallationsDocument(JSON.stringify(text))
}

describe('the installation list document', () => {
  it('reads the rows the CLI printed, in its order, with its warnings', () => {
    const answer = parsed(DOCUMENT)
    expect(answer.ok).toBe(true)
    if (!answer.ok) return
    expect(answer.document.installations.map((row) => [row.name, row.gcpProject, row.region])).toEqual([
      ['sandbox', 'fugaro-sandbox-482301', 'europe-west4'],
      // The one whose config could not be read is still a row, with its fields
      // empty: "never a silent omission" is the CLI's own rule and the picker
      // must not turn it into one by dropping the row.
      ['acme', '', ''],
    ])
    expect([...answer.document.warnings]).toEqual([
      '/Users/owner/.config/fugaro/projects/acme.yaml could not be read: permission denied',
    ])
  })

  it('ignores a field it does not know, so a newer CLI cannot break the parse', () => {
    // The contract's §2 rule. `shared_merged` and `shared_checked_at` are the
    // CLI's own fields today and are precisely the ones the console does not
    // draw; a field added after this console was built is the same case.
    const answer = parsed({
      installations: [
        { name: 'sandbox', gcp_project: 'p', region: 'r', shared_merged: 'yes please', invented: { a: 1 } },
      ],
      warnings: [],
      and_a_top_level_one: [1, 2, 3],
    })
    expect(answer.ok).toBe(true)
    expect(answer.ok && answer.document.installations[0]).toMatchObject({ name: 'sandbox', gcpProject: 'p' })
  })

  it('tolerates an absent checkouts array, and reads one when it is there', () => {
    // The field is a later addition (a sibling change adds it): a CLI that does
    // not have it must not be refused, and the entries that are there are kept
    // verbatim -- the console never resolves, joins or stats a path from a
    // document.
    const without = parsed({ installations: [{ name: 'sandbox', gcp_project: 'p', region: 'r' }], warnings: [] })
    expect(without.ok && [...(without.document.installations[0]?.checkouts ?? [])]).toEqual([])

    const with_ = parsed({
      installations: [
        {
          name: 'sandbox',
          gcp_project: 'p',
          region: 'r',
          checkouts: [
            { repository: 'github/o/fugaro', path: '/Users/owner/src/fugaro', enrolled: true, valid: true },
            { repository: 'github/o/fugaro', path: '/Users/owner/src/fugaro-work', enrolled: true, valid: true },
            // never used for anything, whatever the reason (§6.8)
            { repository: 'github/o/gone', path: '/Users/owner/src/gone', enrolled: true, valid: false, reason: 'missing' },
            { repository: 'github/o/x', path: 'relative/dir', enrolled: true, valid: true },
            { repository: 'github/o/x', path: '', enrolled: true, valid: true },
            { repository: 'github/o/x', path: '/bad\u0000path', enrolled: true, valid: true },
            { repository: 'github/o/x', path: 42, valid: true },
            '/Users/owner/src/a-bare-string',
            42,
          ],
        },
      ],
      warnings: [],
    })
    expect(with_.ok && [...(with_.document.installations[0]?.checkouts ?? [])]).toEqual([
      '/Users/owner/src/fugaro',
      '/Users/owner/src/fugaro-work',
    ])
    // A `checkouts` that is not an array is empty, not a refusal: it carries
    // nothing the console draws, and refusing the whole read over it would hide
    // every installation on the machine.
    const wrong = parsed({ installations: [{ name: 'sandbox', checkouts: 'yes' }], warnings: [] })
    expect(wrong.ok && [...(wrong.document.installations[0]?.checkouts ?? [])]).toEqual([])
  })

  it('reads the recorded base branch, of the installation and of each repository', () => {
    // Rule 11's read (§6.8): the branch the launch window shows, and the branch
    // the run is cut from, are the same one, and the app's working directory is
    // empty -- so the installation's own value and each repository's own value
    // are the only two the console can have.
    const answer = parsed({
      installations: [
        {
          name: 'sandbox',
          gcp_project: 'p',
          region: 'r',
          base_branch: 'trunk',
          checkouts: [
            { repository: 'github/o/app', path: '/src/app', base_branch: 'dev', enrolled: true, valid: true },
            // No branch of its own: the installation's value is the layer below
            // it, and '' is what says so.
            { repository: 'github/o/other', path: '/src/other', base_branch: '', enrolled: true, valid: true },
            // A stale row records its repository's branch just the same: which
            // branch a repository runs against is a fact of the project config
            // and not of the directory that path named.
            { repository: 'github/o/gone', path: '/src/gone', base_branch: 'release', enrolled: true, valid: false, reason: 'missing' },
            // A row that names no repository is not a repository record, and
            // its path is still offered on its own terms.
            { path: '/src/anon', base_branch: 'dev', valid: true },
          ],
        },
      ],
      warnings: [],
    })
    expect(answer.ok).toBe(true)
    if (!answer.ok) return
    const row = answer.document.installations[0]
    expect(row?.baseBranch).toBe('trunk')
    expect(row?.repositories).toEqual([
      { repository: 'github/o/app', baseBranch: 'dev' },
      { repository: 'github/o/other', baseBranch: '' },
      { repository: 'github/o/gone', baseBranch: 'release' },
    ])
    // The paths are the list they always were: only a row the CLI re-validated
    // is offered as a working directory.
    expect([...(row?.checkouts ?? [])]).toEqual(['/src/app', '/src/other', '/src/anon'])
  })

  it('keeps an absent base branch apart from one that records nothing', () => {
    // **Two different facts, and §6.8 forbids merging them.** The key absent is
    // an older CLI, which does not report base branches at all; the key present
    // with `""` is a config that records none. Reading the first as the second
    // would tell an operator to set a setting they have already set, so it is
    // kept as `null` all the way to the launch's own refusal.
    const older = parsed({ installations: [{ name: 'sandbox', gcp_project: 'p', region: 'r' }], warnings: [] })
    expect(older.ok && older.document.installations[0]?.baseBranch).toBeNull()
    expect(older.ok && [...(older.document.installations[0]?.repositories ?? [])]).toEqual([])
    const rowWithout = parsed({
      installations: [{ name: 'sandbox', checkouts: [{ repository: 'github/o/app', path: '/src/app', valid: true }] }],
    })
    expect(rowWithout.ok && rowWithout.document.installations[0]?.repositories).toEqual([
      { repository: 'github/o/app', baseBranch: null },
    ])
    // And the two are not each other: a key that is there and empty stays `''`.
    const empty = parsed({
      installations: [{ name: 'sandbox', base_branch: '', checkouts: [{ repository: 'github/o/app', base_branch: '' }] }],
    })
    const held = empty.ok ? empty.document.installations[0] : undefined
    expect(held?.baseBranch).toBe('')
    expect(held?.repositories).toEqual([{ repository: 'github/o/app', baseBranch: '' }])

    // Present and not a string is a document this console does not understand:
    // the value is shown as the branch the run is cut from, so reading it as
    // "unrecorded" would move that branch in silence.
    const wrongInstallation = parsed({ installations: [{ name: 'sandbox', base_branch: 7 }] })
    expect(wrongInstallation.ok).toBe(false)
    const wrongRow = parsed({
      installations: [
        { name: 'sandbox', checkouts: [{ repository: 'github/o/app', path: '/src/app', base_branch: { branch: 'main' } }] },
      ],
    })
    expect(wrongRow.ok).toBe(false)
    expect(wrongRow.ok === false && wrongRow.reason).toContain('sandbox')
    expect(wrongRow.ok === false && wrongRow.reason).toContain('base_branch')
  })

  it('refuses a recorded base branch past the document bound rather than cutting it', () => {
    // The bound is a refusal here and not a truncation: this string is what the
    // run is cut from and what the confirmation window shows, so a silently
    // shortened one would name a branch the run does not use -- the same lie the
    // warning lines below avoid by being display text and nothing else.
    const long = 'b'.repeat(MAX_DOCUMENT_STRING + 1)
    const installation = parsed({ installations: [{ name: 'sandbox', base_branch: long }] })
    expect(installation.ok).toBe(false)
    expect(installation.ok === false && installation.reason).toContain('sandbox')
    const row = parsed({
      installations: [
        { name: 'sandbox', checkouts: [{ repository: 'github/o/app', path: '/src/app', base_branch: long }] },
      ],
    })
    expect(row.ok).toBe(false)
    expect(row.ok === false && row.reason).toContain('base_branch')
  })

  it('refuses a row whose checkouts run past the bound, rather than drawing a shortened row', () => {
    // **The bound is named, not applied behind the screen's back.**
    // `docs/design/console-styling.md` §2.2 says the picker's buttons are the
    // installation's own `checkouts` and that there is no per-row cap -- every
    // checkout the document carried is drawn. `checkoutsOf` used to slice the
    // array at `MAX_DISCOVERED_INSTALLATIONS`, so a row with more than that lost
    // its tail with nothing said: the picker drew a shorter list than the CLI
    // printed, and no screen, warning or sentence mentioned the ones that went.
    // That is the silent omission rule 3 forbids, so the document is refused
    // instead -- the same bound, the same idiom as the installation count above.
    const one = (checkouts: readonly string[]): unknown => ({
      installations: [
        {
          name: 'sandbox',
          gcp_project: 'p',
          region: 'r',
          checkouts: checkouts.map((path) => ({ repository: 'github/o/r', path, enrolled: true, valid: true })),
        },
      ],
      warnings: [],
    })
    const at = Array.from({ length: MAX_DISCOVERED_INSTALLATIONS }, (_unused, index) => `/src/checkout-${String(index)}`)
    const kept = parsed(one(at))
    expect(kept.ok).toBe(true)
    expect(kept.ok && kept.document.installations[0]?.checkouts).toHaveLength(MAX_DISCOVERED_INSTALLATIONS)

    const past = parsed(one([...at, '/src/one-too-many']))
    expect(past.ok).toBe(false)
    // The sentence names the field and the bound, the way the installation
    // count's refusal does, so the operator is told what happened rather than
    // shown a shorter row.
    expect(past.ok === false && past.reason).toContain('checkouts')
    expect(past.ok === false && past.reason).toContain(String(MAX_DISCOVERED_INSTALLATIONS))
    // ...and the row is named, so a document with this on one of fifty rows says
    // which one the console would not draw.
    expect(past.ok === false && past.reason).toContain('sandbox')
  })

  it('offers a dropdown only the rows that are re-validated, enrolled and paired with a repository', () => {
    // **This is the filter the whole dropdown rests on** (design §3.4 rule 7,
    // §6.8): a path reaches the chooser only from a row carrying **both**
    // `valid: true` and `enrolled: true` and naming the repository it belongs
    // to. Everything else -- a row the CLI refused (`missing`, `other_project`,
    // `not_representable`, whatever the reason), a row whose project does not
    // enroll that repository, a row with no repository to label it by, a
    // relative or control-bearing path -- is not offered, and the console then
    // has nothing to send back.
    //
    // Mutation (run, restore): drop the `|| row['enrolled'] !== true` half of the
    // condition in `checkoutsOf`, and the `not-enrolled` row below appears in
    // `checkoutChoices`; drop the `valid` half and the `missing` row does.
    const answer = parsed({
      installations: [
        {
          name: 'sandbox',
          gcp_project: 'p',
          region: 'r',
          checkouts: [
            { repository: 'github/o/app', path: '/src/app', enrolled: true, valid: true },
            { repository: 'github/o/app', path: '/src/app-two', enrolled: true, valid: true },
            // The project's config does not enroll it: `valid` is already false,
            // and the row says so twice over.
            { repository: 'github/o/other', path: '/src/other', enrolled: false, valid: false, reason: 'not_enrolled' },
            // A contradiction a CLI should never print, and the one case where
            // the two fields disagree: without the `enrolled` half it would be
            // offered.
            { repository: 'github/o/other', path: '/src/other-two', enrolled: false, valid: true },
            // Stale rows of every kind are never offered.
            { repository: 'github/o/gone', path: '/src/gone', enrolled: true, valid: false, reason: 'missing' },
            { repository: 'github/o/gone', path: '/src/gone-two', enrolled: true, valid: false, reason: 'not_representable' },
            // Nothing to label a menu entry with.
            { path: '/src/anonymous', enrolled: true, valid: true },
            { repository: '', path: '/src/empty-repository', enrolled: true, valid: true },
            // A path that is not a directory the console may hand over.
            { repository: 'github/o/relative', path: 'src/relative', enrolled: true, valid: true },
            { repository: 'github/o/control', path: '/src/with\u0000nul', enrolled: true, valid: true },
          ],
        },
      ],
      warnings: [],
    })
    expect(answer.ok).toBe(true)
    if (!answer.ok) return
    expect(answer.document.installations[0]?.checkoutChoices).toEqual([
      { repository: 'github/o/app', path: '/src/app' },
      { repository: 'github/o/app', path: '/src/app-two' },
    ])
    // The older `checkouts` list is untouched by it: the picker window's own
    // buttons are drawn from that one, and this filter is the dropdown's.
    expect([...(answer.document.installations[0]?.checkouts ?? [])]).toContain('/src/anonymous')
  })

  it('refuses text that is not the document, rather than drawing part of it', () => {
    // Each of these is a mutation of the one above: a marshalled struct instead
    // of the named document, a document with no array, a row that is not an
    // object, a row with no name, and a row whose project id is a number.
    for (const [what, text] of [
      ['not JSON at all', () => parseInstallationsDocument('fugaro: this CLI has no config command\n')],
      ['not an object', () => parsed([1, 2, 3])],
      ['no installations array', () => parsed({ warnings: [] })],
      ['installations that is not an array', () => parsed({ installations: {} })],
      ['a row that is not an object', () => parsed({ installations: ['sandbox'] })],
      ['a row with no name', () => parsed({ installations: [{ gcp_project: 'p' }] })],
      ['a row with an empty name', () => parsed({ installations: [{ name: '' }] })],
      ['a project id that is a number', () => parsed({ installations: [{ name: 'sandbox', gcp_project: 7 }] })],
      ['a region that is an object', () => parsed({ installations: [{ name: 'sandbox', region: {} }] })],
    ] as const) {
      const answer = text()
      expect(answer.ok, what).toBe(false)
      expect(answer.ok === false && answer.reason, what).not.toBe('')
    }
  })

  it('bounds what one document may make the picker hold', () => {
    const tooMany = {
      installations: Array.from({ length: MAX_DISCOVERED_INSTALLATIONS + 1 }, (_value, index) => ({
        name: `installation-${String(index)}`,
      })),
      warnings: [],
    }
    const answer = parsed(tooMany)
    expect(answer.ok).toBe(false)

    const manyWarnings = parsed({
      installations: [{ name: 'sandbox' }],
      warnings: Array.from({ length: MAX_WARNING_LINES + 10 }, (_value, index) => `warning ${String(index)}`),
    })
    expect(manyWarnings.ok).toBe(true)
    if (!manyWarnings.ok) return
    // The bound is kept and the *omission is named*: every line the console can
    // still draw, then one more saying how many it did not keep. A document
    // longer than the bound that lost its tail silently would be exactly the
    // "silent omission" the CLI's own rule forbids.
    expect(manyWarnings.document.warnings.slice(0, MAX_WARNING_LINES)).toEqual(
      Array.from({ length: MAX_WARNING_LINES }, (_unused, index) => `warning ${String(index)}`),
    )
    expect(manyWarnings.document.warnings).toHaveLength(MAX_WARNING_LINES + 1)
    expect(manyWarnings.document.warnings[MAX_WARNING_LINES]).toContain('10')

    // A warning that is not a string is not a sentence this console can draw:
    // it is dropped, and dropping it does not refuse the read. It is not a
    // warning the CLI printed -- the CLI's `Warnings` is a `[]string` -- so
    // rule 3's "never dropped" is not about it.
    const hostile = parsed({ installations: [{ name: 'sandbox' }], warnings: [{ toString: 'x' }, 3, 'real'] })
    expect(hostile.ok && [...hostile.document.warnings]).toEqual(['real'])
  })

  it('refuses a `warnings` that is there and is not an array, and reads an absent one as none', () => {
    // Rule 1: a field the console draws that arrives as the wrong type is a
    // refusal. `warnings` is drawn (rule 3), so a string where the array belongs
    // is a document this console does not understand -- treating it as "no
    // warnings" would hide whatever the CLI put there.
    const wrong = parsed({ installations: [{ name: 'sandbox' }], warnings: 'the CLI said so' })
    expect(wrong.ok).toBe(false)
    expect(wrong.ok === false && wrong.reason).toContain('warnings')
    // Absent is the other case, and it is the benign one: a CLI that did not send
    // the field sent no warning lines, so there is nothing to hide.
    const absent = parsed({ installations: [{ name: 'sandbox' }] })
    expect(absent.ok).toBe(true)
    expect(absent.ok && absent.document.warnings.length).toBe(0)
  })

  it('seeds both arrays, so "none" is a fact and never a null', () => {
    // The console's availability rule (the contract's §1): a document the CLI
    // seeded with `[]` means "there are none", and this is the state the
    // "set up your first installation" screen is drawn for.
    const answer = parsed({ installations: [], warnings: [] })
    expect(answer.ok).toBe(true)
    expect(answer.ok && answer.document.installations.length).toBe(0)
    expect(answer.ok && answer.document.warnings.length).toBe(0)
    expect(answer.ok && Object.isFrozen(answer.document.installations)).toBe(true)
  })
})

describe('the names the console will drive (review m3)', () => {
  it('skips a row whose name is not a CLI project name, and says so, so no command is built from it', () => {
    const result = parsed({
      installations: [
        { name: 'aurora', gcp_project: 'aurora-482301', region: 'europe-west4' },
        { name: 'orbit;rm -rf ~', gcp_project: '', region: '' },
        { name: 'Orbit', gcp_project: '', region: '' },
        { name: '$(orbit)', gcp_project: '', region: '' },
      ],
      warnings: ['a line the CLI printed'],
    })
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.document.installations.map((row) => row.name)).toEqual(['aurora'])
    expect(result.document.warnings[0]).toBe('a line the CLI printed')
    expect(result.document.warnings.slice(1)).toEqual([
      'the CLI listed "orbit;rm -rf ~", which is not a project name, so the console will not use it',
      'the CLI listed "Orbit", which is not a project name, so the console will not use it',
      'the CLI listed "$(orbit)", which is not a project name, so the console will not use it',
    ])
  })

  it('takes exactly the CLI’s own grammar', () => {
    expect(isCliProjectName('a')).toBe(true)
    expect(isCliProjectName('aurora-2')).toBe(true)
    expect(isCliProjectName('a'.repeat(40))).toBe(true)
    expect(isCliProjectName('a'.repeat(41))).toBe(false)
    expect(isCliProjectName('-aurora')).toBe(false)
    expect(isCliProjectName('aurora-')).toBe(false)
    expect(isCliProjectName('aurora_2')).toBe(false)
    expect(isCliProjectName('')).toBe(false)
  })
})
