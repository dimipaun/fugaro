// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The terms operations grants and changes, and the one thing it may do to a key
// (portal plan P5.3; P5.1's `ops.go`, spec §Operations "Keys").
//
// **Every one of these needs an owner or an admin and a sign-in of the last
// five minutes** (D2187): a member is not offered the section at all, and a
// change under an older sign-in is stopped here, before it is sent, by the
// gate the shell hands down. The API refuses both anyway; what this buys is
// that nobody meets the refusal.
//
// **The shapes are the minter's and the API's, and the screen invents none.**
// A mint names a row and never a policy, because a caller that could name the
// policy could mint a key that never checks in (F28); the seats and the end are
// the operator's and the end is capped at the row's term (F30, P1.8). `null` is
// meaningful in both request shapes -- no new seat count, no end, no reason --
// and in the mint it is one thing only: the check interval of an air-gapped
// row. A connected term carries its own interval, because a key that never
// checks in is one revocation cannot reach (F28).
//
// **A change names the field it needs before it is offered.** The four actions
// share one form and each reads a different field of it, so the button that
// would send an empty one is disabled: the API's refusal would be a sentence
// telling an operator what the form in front of them already shows.
//
// **A key's only action is taking a handover away.** Operations never holds a
// secret (F5): cutting a handover short destroys the bundle a reveal left for an
// installation, which is what an operator does about a secret that may have
// leaked (F32). The route carries the key's id and no body at all, because what
// it does has nothing more to say.

import { useState } from 'react'
import type { FormEvent } from 'react'
import type { EntitlementActionKind, OpsEntitlementAction } from '../../api/types/ops'
import type { OpsApi } from './api'
import { dayInstant } from './format'
import type { OpsActions } from './state'
import { useSubmission } from './state'

/** What a landed change to a term says, in the action's own words. */
const LANDED: Record<EntitlementActionKind, string> = {
  extend: 'The term is extended.',
  seats: 'The seat count is changed.',
  comp: 'The term is comped.',
  revoke: 'The term is revoked, with every key it ever minted (F29).',
}

export function TermsSection({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  if (!actions.mayAct) {
    return null
  }
  return (
    <section aria-labelledby="ops-terms">
      <h2 id="ops-terms">Terms and keys</h2>
      <MintForm api={api} actions={actions} />
      <ChangeForm api={api} actions={actions} />
      <TransferForm api={api} actions={actions} />
      <HandoverForm api={api} actions={actions} />
    </section>
  )
}

function MintForm({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const { busy, failure, landed, submit } = useSubmission(actions)
  const [workspace, setWorkspace] = useState('')
  const [row, setRow] = useState('')
  const [seats, setSeats] = useState('1')
  const [ends, setEnds] = useState('')
  const [interval, setInterval] = useState('')
  const [airGapped, setAirGapped] = useState(false)

  const ready = airGapped || interval.trim() !== ''

  function mint(event: FormEvent): void {
    event.preventDefault()
    void submit(async () => {
      await api.mint({
        workspace_id: workspace.trim(),
        row: row.trim(),
        seats: Number(seats),
        ends_at: dayInstant(ends) ?? '',
        // A null interval is the air-gapped row and nothing else (F28), so an
        // empty field is not it: the operator says which of the two they mean,
        // because a connected key that never checks in is one revocation cannot
        // reach.
        check_interval: airGapped ? null : interval.trim(),
      })
      // A form that kept what it had sent would send the same term again on a
      // second press, and the landed sentence below is what tells the operator
      // the first one arrived.
      setWorkspace('')
      setRow('')
      setSeats('1')
      setEnds('')
      setInterval('')
      setAirGapped(false)
    })
  }

  return (
    <form onSubmit={mint}>
      <fieldset>
        <legend>Mint a term</legend>
        <p>
          The key is minted when the customer binds the workspace, so this grants the term and
          mints nothing now (D2196).
        </p>

        <label htmlFor="ops-mint-workspace">Workspace</label>
        <input
          id="ops-mint-workspace"
          value={workspace}
          onChange={(event) => {
            setWorkspace(event.target.value)
          }}
          required
        />

        <label htmlFor="ops-mint-row">Row</label>
        <input
          id="ops-mint-row"
          value={row}
          onChange={(event) => {
            setRow(event.target.value)
          }}
          placeholder="team-monthly"
          required
        />

        <label htmlFor="ops-mint-seats">Seats</label>
        <input
          id="ops-mint-seats"
          type="number"
          min={1}
          value={seats}
          onChange={(event) => {
            setSeats(event.target.value)
          }}
          required
        />

        <label htmlFor="ops-mint-ends">Ends</label>
        <input
          id="ops-mint-ends"
          type="date"
          value={ends}
          onChange={(event) => {
            setEnds(event.target.value)
          }}
          required
        />

        <label htmlFor="ops-mint-interval">Check interval</label>
        <input
          id="ops-mint-interval"
          value={interval}
          disabled={airGapped}
          onChange={(event) => {
            setInterval(event.target.value)
          }}
          placeholder="P7D"
          required={!airGapped}
        />
        <p>
          How often the installation asks whether its key is still good, as an ISO 8601 duration of
          one to thirty days.
        </p>

        <label htmlFor="ops-mint-air-gapped">
          <input
            id="ops-mint-air-gapped"
            type="checkbox"
            checked={airGapped}
            onChange={(event) => {
              setAirGapped(event.target.checked)
            }}
          />
          The air-gapped row: the installation never checks in
        </label>
        <p>
          An air-gapped term is issued once as a file and is never revealed or rebound (D2206), and
          it is the one thing the API reads an absent check interval as (F28).
        </p>

        <button type="submit" disabled={busy || !ready}>
          Mint
        </button>
      </fieldset>
      {failure === null ? null : <p role="alert">{failure}</p>}
      {landed ? (
        <p role="status">
          The term is granted. The key is minted when the customer binds the workspace (D2196).
        </p>
      ) : null}
    </form>
  )
}

function ChangeForm({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const { busy, failure, landed, submit } = useSubmission(actions)
  const [sent, setSent] = useState<EntitlementActionKind | null>(null)
  const [entitlement, setEntitlement] = useState('')
  const [seats, setSeats] = useState('')
  const [end, setEnd] = useState('')
  const [reason, setReason] = useState('')

  /**
   * Whether this action has everything it reads. `comp` and `revoke` act on the
   * term alone; `seats` and `extend` each read one more field, and an empty one
   * of those is not a zero the API accepts: a seat count is at least one, and an
   * end is an instant rather than the empty string `dayInstant` would have to
   * answer.
   */
  function ready(action: EntitlementActionKind): boolean {
    if (entitlement.trim() === '') {
      return false
    }
    if (action === 'seats') {
      return seats.trim() !== ''
    }
    if (action === 'extend') {
      return end.trim() !== ''
    }
    return true
  }

  function change(action: EntitlementActionKind): void {
    const body: OpsEntitlementAction = {
      entitlement_id: entitlement.trim(),
      action,
      seats: action === 'seats' ? Number(seats) : null,
      // An end is read by `extend` alone: the other three actions carry none,
      // and a body that carried one would be a field the reader ignores or
      // refuses (wire_ops.go).
      ends_at: action === 'extend' ? dayInstant(end) : null,
      reason: reason.trim() === '' ? null : reason.trim(),
    }
    void submit(async () => {
      await api.act(body)
      setSent(action)
      setEntitlement('')
      setSeats('')
      setEnd('')
      setReason('')
    })
  }

  return (
    <fieldset>
      <legend>Change a term</legend>
      <p>
        Revoking ends every key the term ever minted and not only the one an installation is
        running (F29).
      </p>

      <label htmlFor="ops-change-entitlement">Entitlement</label>
      <input
        id="ops-change-entitlement"
        value={entitlement}
        onChange={(event) => {
          setEntitlement(event.target.value)
        }}
        required
      />

      <label htmlFor="ops-change-seats">New seats</label>
      <input
        id="ops-change-seats"
        type="number"
        min={1}
        value={seats}
        onChange={(event) => {
          setSeats(event.target.value)
        }}
      />

      <label htmlFor="ops-change-end">New end</label>
      <input
        id="ops-change-end"
        type="date"
        value={end}
        onChange={(event) => {
          setEnd(event.target.value)
        }}
      />

      <label htmlFor="ops-change-reason">Reason for the change</label>
      <input
        id="ops-change-reason"
        value={reason}
        onChange={(event) => {
          setReason(event.target.value)
        }}
      />

      <div>
        <button
          type="button"
          disabled={busy || !ready('extend')}
          onClick={() => change('extend')}
        >
          Extend
        </button>
        <button
          type="button"
          disabled={busy || !ready('seats')}
          onClick={() => change('seats')}
        >
          Change seats
        </button>
        <button
          type="button"
          disabled={busy || !ready('comp')}
          onClick={() => change('comp')}
        >
          Comp
        </button>
        <button
          type="button"
          disabled={busy || !ready('revoke')}
          onClick={() => change('revoke')}
        >
          Revoke
        </button>
      </div>
      {failure === null ? null : <p role="alert">{failure}</p>}
      {landed && sent !== null ? <p role="status">{LANDED[sent]}</p> : null}
    </fieldset>
  )
}

function TransferForm({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const { busy, failure, landed, submit } = useSubmission(actions)
  const [project, setProject] = useState('')
  const [workspace, setWorkspace] = useState('')
  const [reason, setReason] = useState('')

  function transfer(event: FormEvent): void {
    event.preventDefault()
    void submit(async () => {
      await api.transfer({
        project_id: project.trim(),
        workspace_id: workspace.trim(),
        reason: reason.trim() === '' ? null : reason.trim(),
      })
      setProject('')
      setWorkspace('')
      setReason('')
    })
  }

  return (
    <form onSubmit={transfer}>
      <fieldset>
        <legend>Transfer a project id</legend>
        <p>
          One project id is bound by one workspace across every organisation (D2204), so a
          customer who bound somebody else's id, or the wrong one, is moved by hand. The
          workspace that loses it loses the key bound to it.
        </p>

        <label htmlFor="ops-transfer-project">Project id</label>
        <input
          id="ops-transfer-project"
          value={project}
          onChange={(event) => {
            setProject(event.target.value)
          }}
          required
        />

        <label htmlFor="ops-transfer-workspace">Workspace that keeps it</label>
        <input
          id="ops-transfer-workspace"
          value={workspace}
          onChange={(event) => {
            setWorkspace(event.target.value)
          }}
          required
        />

        <label htmlFor="ops-transfer-reason">Reason for the transfer</label>
        <input
          id="ops-transfer-reason"
          value={reason}
          onChange={(event) => {
            setReason(event.target.value)
          }}
        />

        <button type="submit" disabled={busy}>
          Transfer the project id
        </button>
      </fieldset>
      {failure === null ? null : <p role="alert">{failure}</p>}
      {landed ? (
        <p role="status">The project id is transferred to that workspace.</p>
      ) : null}
    </form>
  )
}

function HandoverForm({ api, actions }: { api: OpsApi; actions: OpsActions }) {
  const { busy, failure, landed, submit } = useSubmission(actions)
  const [key, setKey] = useState('')

  function cut(event: FormEvent): void {
    event.preventDefault()
    const id = key.trim()
    void submit(async () => {
      await api.cutHandover(id)
      setKey('')
    })
  }

  return (
    <form onSubmit={cut}>
      <fieldset>
        <legend>Cut a handover short</legend>
        <p>
          A reveal leaves the successor's bundle for the installation for up to a day, encrypted
          under a key only the API can open (F3). Cutting it short takes that bundle away, which
          is what an operator does about a secret that may have leaked (F32). Key metadata does
          not say whether one is waiting, so this is safe to try and does nothing when none is.
        </p>

        <label htmlFor="ops-handover-key">Key</label>
        <input
          id="ops-handover-key"
          value={key}
          onChange={(event) => {
            setKey(event.target.value)
          }}
          required
        />

        <button type="submit" disabled={busy}>
          Cut the handover short
        </button>
      </fieldset>
      {failure === null ? null : <p role="alert">{failure}</p>}
      {landed ? (
        <p role="status">
          The handover is cut. Metadata does not say whether one was waiting, so a key with none
          changed nothing.
        </p>
      ) : null}
    </form>
  )
}
