// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The audit log (portal plan P5.3; spec §Operations, D456).
//
// **Every operations action is here, the reads included.** The operations
// organisation reads across tenants and the customer is entitled to see that
// accounted for, so a lookup is an entry like a mint is (D456); the log is the
// only record of what operations did, because the actions themselves are
// requests and carry no actor (`src/api/types/ops.ts`).
//
// **An actor's email is drawn, and that is the point of the screen.** It is the
// personal data that makes this shape operations-only, and a log an operator
// cannot attribute an action to is a log nobody can answer a customer with.
//
// **The last page only, and it says so.** The API answers the newest entries
// (`api.audit`), and there is no cursor on this shape: a log with more in it
// than the API answers would be silently truncated, which is worse than a
// screen that names what it is showing.
//
// **A refused read is an answer, and this one is a read of the record.** The
// log is read once when the section mounts, so the refusal is drawn with a way
// to read it again: an operator asking who did something cannot reload the page
// with half a form filled in on it, and the audit log is the only account of
// what operations did (D456). A read that came back refused has finished, so it
// is never drawn as "Loading…".

import { useEffect, useState } from 'react'
import type { AuditEntry } from '../../api/types/audit'
import type { OpsApi } from './api'
import { sentence } from './state'

export function AuditSection({ api }: { api: OpsApi }) {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let live = true
    // A load that starts clears the last refusal, so the retry is drawn as a
    // load and not under the sentence it is answering.
    setFailure(null)
    api.audit().then(
      (log) => {
        if (live) {
          setEntries(log)
        }
      },
      (error: unknown) => {
        if (live) {
          setFailure(sentence(error))
        }
      },
    )
    return () => {
      live = false
    }
  }, [api, attempt])

  return (
    <section aria-labelledby="ops-audit">
      <h2 id="ops-audit">The audit log</h2>
      <p>
        Reads are recorded here as well as changes (D456), and each entry names the person who
        made it. What is drawn here is the newest page the API answers and nothing older: this
        shape carries no cursor, so an entry that has fallen off the page cannot be asked for.
      </p>

      {failure === null ? null : (
        <div role="alert">
          <p>{failure}</p>
          <button
            type="button"
            onClick={() => {
              setAttempt((tried) => tried + 1)
            }}
          >
            Try again
          </button>
        </div>
      )}
      {failure === null && entries === null ? <p role="status">Loading…</p> : null}
      {entries === null ? null : entries.length === 0 ? (
        <p>Nothing has been recorded yet.</p>
      ) : (
        <ul>
          {entries.map((entry) => (
            <li key={entry.id}>
              {entry.at} · {entry.actor_email} · {entry.action} ·{' '}
              {entry.read ? 'a read' : 'a change'} ·{' '}
              {entry.organisation_id ?? 'across organisations'} ·{' '}
              {entry.target_id ?? 'no single target'}
              {entry.detail === null ? '' : ` · ${entry.detail}`}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
