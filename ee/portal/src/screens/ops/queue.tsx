// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The queue a person decides (portal plan P5.3; F15, F16).
//
// **The items here are the parked Paddle events, and that is the queue whose
// items have a shape.** A notification whose transaction and subscription did
// not begin with a transaction the API created is stored as received and parked
// rather than applied (F15); `ledger.event` is the shape it is read as, and
// `state: parked` is what makes the queue the events nobody has answered. The
// other queue the operations API decides -- the applications `queue.go` holds
// and `ops.queue_decision` answers -- has no wire shape yet, so no screen can
// draw one without inventing a shape the Go side and this app would then hold
// separately (F20); it is reported rather than guessed at.
//
// **The stored body is never drawn.** An event is kept exactly as Paddle sent
// it, buyer fields included, and it is the sales record counsel's retention item
// is about (D457). Deciding one takes its id, the decision and the entitlement
// it is attached to; a screen that printed the body would be reading a
// customer's records out to whoever is at the desk.
//
// **A decision is by the id and never by the body.** Attaching one creates the
// entitlement the event sold if it sold one (P4.2), so the id is what makes a
// redelivery of the same event change nothing.

import { useEffect, useState } from 'react'
import type { LedgerEvent } from '../../api/types/ledger'
import type { OpsEventResolution } from '../../api/types/ops'
import type { OpsApi } from './api'
import type { OpsActions } from './state'
import { sentence, useSubmission } from './state'

export function QueueSection({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const [events, setEvents] = useState<LedgerEvent[] | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [entitlement, setEntitlement] = useState('')
  const { busy, failure: refused, submit } = useSubmission(actions)

  useEffect(() => {
    let live = true
    api.parkedEvents().then(
      (parked) => {
        if (live) {
          setEvents(parked)
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
  }, [api])

  function decide(id: string, decision: OpsEventResolution['decision']): void {
    void submit(async () => {
      const resolved = await api.resolveEvent({
        id,
        decision,
        entitlement_id: decision === 'attach' ? entitlement.trim() : null,
      })
      setEvents((held) =>
        held === null ? held : held.filter((event) => event.id !== resolved.id),
      )
    })
  }

  return (
    <section aria-labelledby="ops-queue">
      <h2 id="ops-queue">The queue</h2>
      <p>
        Events the API could not place wait here. The body Paddle sent is kept as received and is
        not drawn: decide from the event's own facts.
      </p>

      {failure === null ? null : <p role="alert">{failure}</p>}
      {refused === null ? null : <p role="alert">{refused}</p>}

      {events === null ? (
        <p role="status">Loading…</p>
      ) : events.length === 0 ? (
        <p>Nothing is waiting.</p>
      ) : (
        <ul>
          {events.map((event) => (
            <li key={event.id}>
              <p>
                {event.id} · {event.notification} · received {event.received_at} · {event.state}
                {event.entitlement_id === null ? '' : ` · ${event.entitlement_id}`}
              </p>
              {actions.mayAct ? (
                <div>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      decide(event.id, 'attach')
                    }}
                  >
                    Attach
                  </button>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      decide(event.id, 'dismiss')
                    }}
                  >
                    Dismiss
                  </button>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      )}

      {actions.mayAct && events !== null && events.length > 0 ? (
        <>
          <label htmlFor="ops-queue-entitlement">Entitlement to attach to</label>
          <input
            id="ops-queue-entitlement"
            value={entitlement}
            onChange={(changed) => {
              setEntitlement(changed.target.value)
            }}
          />
        </>
      ) : null}
    </section>
  )
}
