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
// **An attach waits for the term it acts on.** The field is empty until a person
// names one, and an empty field is not the `null` the shape means: a null is a
// dismissal, which attaches nothing (`wire_ops.go`), while the empty string is a
// term id nobody has, sent as though it were one. So the button that would send
// it is not offered until the field is filled, which is the same courtesy the
// change form keeps (a refusal the form in front of the operator already shows
// is a sentence about nothing).
//
// **A decision is by the id and never by the body.** Attaching one creates the
// entitlement the event sold if it sold one (P4.2), so the id is what makes a
// redelivery of the same event change nothing.
//
// **A refused read is an answer, and it carries its own way to ask again.** The
// park is read once when the section mounts, and a section that drew the
// refusal and stopped there would leave a queue nobody can decide until they
// reloaded the page: the sentence is drawn with a button that reads it again,
// and it is drawn instead of "Loading…", because a read that came back refused
// has finished.

import { useEffect, useState } from 'react'
import type { LedgerEvent } from '../../api/types/ledger'
import type { OpsEventResolution } from '../../api/types/ops'
import type { OpsApi } from './api'
import type { OpsActions } from './state'
import { sentence, useSubmission } from './state'

export function QueueSection({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const [events, setEvents] = useState<LedgerEvent[] | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [entitlement, setEntitlement] = useState('')
  const { busy, failure: refused, submit } = useSubmission(actions)

  useEffect(() => {
    let live = true
    // A load that starts clears the last refusal, so the retry is drawn as a
    // load and not under the sentence it is answering.
    setFailure(null)
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
  }, [api, attempt])

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

  const attachable = entitlement.trim() !== ''

  return (
    <section aria-labelledby="ops-queue">
      <h2 id="ops-queue">The queue</h2>
      <p>
        Events the API could not place wait here. The body Paddle sent is kept as received and is
        not drawn: decide from the event's own facts.
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
      {refused === null ? null : <p role="alert">{refused}</p>}

      {failure === null && events === null ? <p role="status">Loading…</p> : null}
      {events === null ? null : events.length === 0 ? (
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
                    disabled={busy || !attachable}
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
