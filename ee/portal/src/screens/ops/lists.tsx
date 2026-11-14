// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The two lists the operations door is configured with (portal plan P5.3; D2207
// as amended on 2026-11-13, F40).
//
// **These are a change, and so they go through the same gate as a mint.** They
// are read by the sign-in rule rather than by a screen (F40): the domains are
// the only ones operations may invite somebody at (F9), and the directories are
// the Microsoft ones a reseller's sign-in is accepted from. Widening either one
// is an action on the operations team's own door, so a member is shown them and
// offered no way to save them.
//
// **An empty list is a real answer and not a missing one.** Either list empty
// refuses every sign-in it would have admitted (the schema says so), so saving
// nothing is not the same as never having set anything: the screen draws both
// lists as they are, empty ones included, and never overrides one that is empty
// with a default.
//
// **The text being edited is the text, and the list is read off it only on
// save.** A field that turned its own contents into a list after each keystroke
// would eat the newline that starts the next entry, so an operator could never
// type a second domain. Splitting on lines and commas, trimming and dropping
// the blanks a paste leaves is the whole of the reading, and nothing is
// lower-cased or completed: a domain the schema will not take is the API's
// refusal to make, and it says why in a sentence this screen shows rather than
// guesses at.

import { useEffect, useState } from 'react'
import type { OpsLists } from '../../api/types/ops'
import type { OpsApi } from './api'
import type { OpsActions } from './state'
import { sentence, useSubmission } from './state'

function entries(field: string): string[] {
  return field
    .split(/[\n,]/)
    .map((entry) => entry.trim())
    .filter((entry) => entry !== '')
}

function text(list: string[]): string {
  return list.join('\n')
}

export function ListsSection({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const [domains, setDomains] = useState<string | null>(null)
  const [directories, setDirectories] = useState('')
  const [readFailure, setReadFailure] = useState<string | null>(null)
  const { busy, failure: refused, submit } = useSubmission(actions)

  useEffect(() => {
    let live = true
    api.lists().then(
      (lists: OpsLists) => {
        if (live) {
          setDomains(text(lists.email_domains))
          setDirectories(text(lists.microsoft_directories))
        }
      },
      (error: unknown) => {
        if (live) {
          setReadFailure(sentence(error))
        }
      },
    )
    return () => {
      live = false
    }
  }, [api])

  function save(): void {
    void submit(async () => {
      const saved = await api.saveLists({
        email_domains: entries(domains ?? ''),
        microsoft_directories: entries(directories),
      })
      setDomains(text(saved.email_domains))
      setDirectories(text(saved.microsoft_directories))
    })
  }

  return (
    <section aria-labelledby="ops-lists">
      <h2 id="ops-lists">The lists</h2>
      <p>
        These are the operations team's own door: the domains its invitations may go to, and the
        Microsoft directories a reseller's sign-in is accepted from. An empty list admits nobody.
      </p>

      {readFailure === null ? null : <p role="alert">{readFailure}</p>}
      {refused === null ? null : <p role="alert">{refused}</p>}

      {domains === null ? (
        <p role="status">Loading…</p>
      ) : (
        <>
          <label htmlFor="ops-lists-domains">Email domains</label>
          <textarea
            id="ops-lists-domains"
            value={domains}
            readOnly={!actions.mayAct}
            onChange={(event) => {
              setDomains(event.target.value)
            }}
          />

          <label htmlFor="ops-lists-directories">Microsoft directories</label>
          <textarea
            id="ops-lists-directories"
            value={directories}
            readOnly={!actions.mayAct}
            onChange={(event) => {
              setDirectories(event.target.value)
            }}
          />

          {actions.mayAct ? (
            <button type="button" disabled={busy} onClick={save}>
              Save the lists
            </button>
          ) : null}
        </>
      )}
    </section>
  )
}
