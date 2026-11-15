// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Looking a customer up (portal plan P5.3; spec §Operations "Customers").
//
// **A read, and reads are audited too.** The operations organisation reads
// across tenants, so every request this section makes is an action the customer
// is entitled to see accounted for (D456), which is why it is the API that
// answers it and never the database (D2188).
//
// **The ids are drawn, and that is not decoration.** Every action the other
// sections send names an entitlement, a workspace or a project id, and this is
// where an operator finds them; a screen that hid the ids would leave the
// actions unusable.
//
// **A key is metadata here and nowhere else.** Operations sees a key's id, its
// seats, its tier, when it was issued and when it last refreshed, and never the
// key itself (F5): the shape this reads is `keys.metadata`, which has no room
// for one.

import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import type { Entitlement } from '../../api/types/entitlements'
import type { KeyMetadata } from '../../api/types/keys'
import type { Membership, Organisation } from '../../api/types/orgs'
import type { Workspace } from '../../api/types/workspaces'
import type { OpsApi } from './api'
import { sentence } from './state'

interface Records {
  organisation: Organisation
  workspaces: Workspace[]
  entitlements: Entitlement[]
  members: Membership[]
  keys: KeyMetadata[]
}

export function LookupSection({ api }: { api: OpsApi }) {
  const [term, setTerm] = useState('')
  const [found, setFound] = useState<Organisation[] | null>(null)
  const [records, setRecords] = useState<Records | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    // A second lookup replaces the first: what the panel below draws is one
    // organisation's records, and leaving the last one's on screen while a new
    // search is in flight would draw two customers as one.
    setRecords(null)
  }, [found])

  async function look(event: FormEvent): Promise<void> {
    event.preventDefault()
    setBusy(true)
    setFailure(null)
    try {
      setFound(await api.lookup(term.trim()))
    } catch (error: unknown) {
      setFailure(sentence(error))
      setFound(null)
    } finally {
      setBusy(false)
    }
  }

  async function show(organisation: Organisation): Promise<void> {
    setBusy(true)
    setFailure(null)
    try {
      const [workspaces, entitlements, members] = await Promise.all([
        api.workspaces(organisation.id),
        api.entitlements(organisation.id),
        api.members(organisation.id),
      ])
      const keys = await Promise.all(workspaces.map((workspace) => api.keys(workspace.id)))
      setRecords({ organisation, workspaces, entitlements, members, keys: keys.flat() })
    } catch (error: unknown) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="ops-lookup">
      <h2 id="ops-lookup">Look up a customer</h2>
      <form
        onSubmit={(event) => {
          void look(event)
        }}
      >
        <label htmlFor="ops-lookup-term">Organisation, workspace or project id</label>
        <input
          id="ops-lookup-term"
          value={term}
          onChange={(event) => {
            setTerm(event.target.value)
          }}
          required
        />
        <button type="submit" disabled={busy}>
          Look up
        </button>
      </form>

      {failure === null ? null : <p role="alert">{failure}</p>}

      {found === null ? null : found.length === 0 ? (
        <p>No organisation matches that.</p>
      ) : (
        <ul>
          {found.map((organisation) => (
            <li key={organisation.id}>
              <p>
                {organisation.name} · {organisation.id} · {organisation.type}
              </p>
              <button
                type="button"
                disabled={busy}
                onClick={() => {
                  void show(organisation)
                }}
              >
                Show the organisation
              </button>
              {records === null || records.organisation.id !== organisation.id ? null : (
                <CustomerRecords records={records} />
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function CustomerRecords({ records }: { records: Records }) {
  return (
    <div>
      <h3>Workspaces</h3>
      <ul>
        {records.workspaces.map((workspace) => (
          <li key={workspace.id}>
            {workspace.id} · {workspace.name} · project{' '}
            {workspace.project_id ?? 'none, so nothing is bound to it'}
          </li>
        ))}
      </ul>

      <h3>Terms</h3>
      <ul>
        {records.entitlements.map((entitlement) => (
          <li key={entitlement.id}>
            {entitlement.id} · {entitlement.row} · {entitlement.tier} · {entitlement.kind} ·{' '}
            {entitlement.seats} seats · {entitlement.status} · ends {entitlement.ends_at}
            {entitlement.air_gapped ? ' · issued as a file, never rebound' : ''}
          </li>
        ))}
      </ul>

      <h3>Keys</h3>
      {records.keys.length === 0 ? (
        <p>No key is issued for this organisation.</p>
      ) : (
        <ul>
          {records.keys.map((key) => (
            <li key={key.key_id}>
              {key.key_id} · {key.tier} · {key.kind} · {key.seats} seats · paid through{' '}
              {key.paid_through} · last refreshed {key.last_seen ?? 'never'}
            </li>
          ))}
        </ul>
      )}

      <h3>Members</h3>
      <ul>
        {records.members.map((member) => (
          <li key={member.uid}>
            {member.email} · {member.role}
          </li>
        ))}
      </ul>
    </div>
  )
}
