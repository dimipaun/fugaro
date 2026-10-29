// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The fake document's `closest` (test/support/fakeDom.ts).
//
// `closest` exists for the one selector the console's listeners ask for -- the
// nearest ancestor-or-self carrying a tag name, which tells a click on a control
// nested in a row from a click on the row. It compares the whole selector to a
// tag name, so a case that asks for anything else (`.row`, `button.x`) used to
// be answered `null` and read that as "not inside": a silent wrong answer is
// worse than no answer, so the fake now refuses a selector it cannot answer.

import { describe, expect, it } from 'vitest'

import { FakeDocument } from '../support/fakeDom'

describe("the fake's closest", () => {
  it('answers the nearest ancestor-or-self carrying the tag', () => {
    const document = new FakeDocument()
    const row = document.createElement('tr')
    const cell = document.createElement('td')
    const button = document.createElement('button')
    row.appendChild(cell)
    cell.appendChild(button)

    expect(button.closest('button')).toBe(button)
    expect(button.closest('td')).toBe(cell)
    expect(button.closest('tr')).toBe(row)
    expect(button.closest('table')).toBeNull()
  })

  it('refuses a selector it cannot answer, naming it', () => {
    const document = new FakeDocument()
    const button = document.createElement('button')

    expect(() => button.closest('.row')).toThrow(/\.row/)
    expect(() => button.closest('button.x')).toThrow(/button\.x/)
  })
})
