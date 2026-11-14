// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The workspace screen (portal plan P3.4; spec §Workspaces, keys and licences).
//
// **A workspace is what a person pays for, and the project is what they set up
// afterwards.** The plan's whole reason for this screen's shape is F25's split:
// a customer buys before `fugaro setup` has created anything, so a paid
// entitlement that names no Firebase project is an ordinary state and not an
// error -- it is the state this screen exists to walk somebody out of, which is
// why the unbound licences get their own section with the one instruction that
// finishes the job (D2196, D2197, D2205).
//
// **The reveal is the only credential this app ever holds, and it holds it for
// one mount.** `POST /keys/reveal` rotates and answers the key and its renewal
// secret once (D2182 as amended); this screen keeps them in React state, draws
// them, and does nothing else with them -- no storage, no refetch on the way
// back, no copy kept beside the request. A key that a screen could redraw is a
// key the app has stored, which is the thing D2182 refuses. An air-gapped
// entitlement is the opposite case and the same rule: it has no secret at all
// (D2206), so it is offered as a file to download instead of a reveal.
//
// **Nothing about a failure is invented here.** A refusal the API wrote is
// shown as the API wrote it -- its one error shape is a sentence for a person
// (P1.0) -- and anything else is answered with one sentence this screen owns,
// for the same reason the sign-in screen gives.
//
// The client is handed in rather than read from the session here: the route
// builds it (`route.tsx`), so this file is a view over one seam and a test
// mounts it with a fake and no provider at all.

import { useEffect, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { ApiError } from '../../api/client'
import type { ApiClient } from '../../api/client'
import type { Entitlement } from '../../api/types/entitlements'
import type { KeyLicence, KeyReveal } from '../../api/types/keys'
import type { Workspace } from '../../api/types/workspaces'
import './workspaces.css'

const GENERIC_FAILURE = 'The portal could not reach your workspaces. Try again.'

const COPY_FAILURE = 'The portal could not copy that. Select the text and copy it by hand.'

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

// Pinned to a locale and to UTC: the API answers instants, a reader here is
// promised the day the term ends and not the day their own clock is on, and a
// date that moved with the machine's locale is a test that passes here and
// fails on somebody else's laptop.
const DAY = new Intl.DateTimeFormat('en-GB', {
  day: 'numeric',
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
})

function day(value: string): string {
  return DAY.format(new Date(value))
}

// The marker the checkout (P4.1) and the billing screen (P4.4) send a person
// back with: `/workspaces?licence=changed`. A purchase or a seat change reaches
// the installation through `fugaro licence refresh` (the spec's own sentence,
// pinned by P3.6's `TestSeatChangeIsTheNextRenewal`), so the screen a payment
// lands on says so. It is a query parameter rather than a prop because the two
// screens that write it are other tasks' files and this one may not edit them.
const REFRESH_MARKER = 'licence'
const REFRESH_VALUE = 'changed'

/**
 * The query the marker leaves behind, or null when this address carries none.
 *
 * The marker is read from the router's location and replaced through the
 * router, so the address the app holds and the address the browser shows stay
 * one fact: a reload, or a link back here, does not tell somebody their
 * purchase has just landed a second time.
 */
function spendsRefreshMarker(search: string): string | null {
  const params = new URLSearchParams(search)
  if (params.get(REFRESH_MARKER) !== REFRESH_VALUE) {
    return null
  }
  params.delete(REFRESH_MARKER)
  const query = params.toString()
  return query === '' ? '' : `?${query}`
}

interface PortalData {
  workspaces: Workspace[]
  entitlements: Entitlement[]
}

/** The two lists this screen draws, asked for together: a card is a workspace
 * and the entitlement that grants it, so half an answer is no answer. */
async function read(client: ApiClient): Promise<PortalData> {
  const [workspaces, entitlements] = await Promise.all([
    client.request<{ workspaces: Workspace[] }>('/workspaces'),
    client.request<{ entitlements: Entitlement[] }>('/entitlements'),
  ])
  return { workspaces: workspaces.workspaces, entitlements: entitlements.entitlements }
}

/** The workspaces whose licence is granted and not yet bound (D2196, F25). */
function unbound(entitlements: readonly Entitlement[]): Entitlement[] {
  return entitlements.filter(
    (entitlement) =>
      entitlement.workspace_id !== null &&
      entitlement.bound_at === null &&
      entitlement.status === 'active',
  )
}

export interface WorkspacesScreenProps {
  /** Built by the route from the session; a test hands in a fake. */
  client: ApiClient
}

export function WorkspacesScreen({ client }: WorkspacesScreenProps) {
  const location = useLocation()
  const navigate = useNavigate()
  const [data, setData] = useState<PortalData | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [hint, setHint] = useState(false)
  const [bundle, setBundle] = useState<KeyReveal | null>(null)
  const [licence, setLicence] = useState<{ workspaceId: string; file: KeyLicence } | null>(null)

  useEffect(() => {
    const query = spendsRefreshMarker(location.search)
    if (query === null) {
      return
    }
    setHint(true)
    // Replaced rather than pushed: the marker was the way here, not a step of
    // its own, and Back belongs to whatever the person was reading before the
    // payment.
    void navigate(`${location.pathname}${query}`, { replace: true })
  }, [location, navigate])

  useEffect(() => {
    // The screen is unmounted by a navigation in the suite and by the router in
    // the app, and an answer that lands afterwards belongs to a screen nobody is
    // looking at.
    let live = true
    read(client).then(
      (next) => {
        if (live) {
          setData(next)
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
  }, [client])

  /** One act and one failure: everything this screen asks the API for. */
  async function run(action: () => Promise<void>): Promise<void> {
    setFailure(null)
    try {
      await action()
    } catch (error: unknown) {
      setFailure(sentence(error))
    }
  }

  /** The state is read back rather than patched: binding mints a key and moves
   * two records, and a screen that guessed at the result would be the second
   * place the rule lives. */
  async function reload(): Promise<void> {
    setData(await read(client))
  }

  function bind(entitlementId: string, projectId: string): Promise<void> {
    return run(async () => {
      await client.request('/workspaces/bind', {
        method: 'POST',
        body: { entitlement_id: entitlementId, project_id: projectId },
      })
      await reload()
    })
  }

  function reveal(workspaceId: string): Promise<void> {
    return run(async () => {
      const answer = await client.request<KeyReveal>('/keys/reveal', {
        method: 'POST',
        body: { workspace_id: workspaceId },
      })
      setBundle(answer)
    })
  }

  function download(workspaceId: string): Promise<void> {
    return run(async () => {
      const file = await client.request<KeyLicence>(
        `/keys/licence?workspace_id=${encodeURIComponent(workspaceId)}`,
      )
      setLicence({ workspaceId, file })
    })
  }

  function ask(path: string, workspaceId: string): Promise<void> {
    return run(async () => {
      await client.request(path, { method: 'POST', body: { workspace_id: workspaceId } })
      await reload()
    })
  }

  function copy(value: string): void {
    const clipboard = navigator.clipboard
    if (clipboard === undefined) {
      setFailure(COPY_FAILURE)
      return
    }
    clipboard.writeText(value).catch(() => {
      setFailure(COPY_FAILURE)
    })
  }

  if (data === null) {
    return (
      <main className="workspaces">
        <h1>Workspaces</h1>
        {failure === null ? <p role="status">Loading…</p> : <p role="alert">{failure}</p>}
      </main>
    )
  }

  const pending = unbound(data.entitlements)

  return (
    <main className="workspaces">
      <h1>Workspaces</h1>

      {hint ? (
        <p className="workspaces__refresh">
          Your licence changed. Run <code>fugaro licence refresh</code> in the installation to pick
          it up.
        </p>
      ) : null}

      {failure === null ? null : <p role="alert">{failure}</p>}

      {bundle === null ? null : <Revealed bundle={bundle} onCopy={copy} />}

      {pending.length === 0 ? null : (
        <section className="workspaces__pending" aria-label="Licences not bound yet">
          <h2>Not bound yet</h2>
          {pending.map((entitlement) => (
            <Pending key={entitlement.id} entitlement={entitlement} onBind={bind} />
          ))}
        </section>
      )}

      {data.workspaces.length === 0 ? (
        <p>You have no workspaces yet.</p>
      ) : (
        data.workspaces.map((workspace) => (
          <Card
            key={workspace.id}
            workspace={workspace}
            entitlement={
              data.entitlements.find((one) => one.id === workspace.entitlement_id) ?? null
            }
            licence={licence?.workspaceId === workspace.id ? licence.file : null}
            onBind={bind}
            onReveal={reveal}
            onDownload={download}
            onAsk={ask}
          />
        ))
      )}
    </main>
  )
}

function Revealed({ bundle, onCopy }: { bundle: KeyReveal; onCopy: (value: string) => void }) {
  return (
    <section className="workspaces__bundle" aria-label="The key you revealed">
      <h2>Your key</h2>
      <p>
        This is shown once and nothing keeps it. Copy the key and its renewal secret into the
        installation now.
      </p>
      <p className="workspaces__value">{bundle.key}</p>
      <button type="button" onClick={() => onCopy(bundle.key)}>
        Copy the key
      </button>
      <p className="workspaces__value">{bundle.renewal_secret}</p>
      <button type="button" onClick={() => onCopy(bundle.renewal_secret)}>
        Copy the renewal secret
      </button>
      <p>It runs to {day(bundle.paid_through)}.</p>
    </section>
  )
}

function Pending({
  entitlement,
  onBind,
}: {
  entitlement: Entitlement
  onBind: (entitlementId: string, projectId: string) => Promise<void>
}) {
  const [projectId, setProjectId] = useState('')
  return (
    <div className="pending">
      <p>
        {entitlement.tier} licence, {entitlement.seats} seats, renews on {day(entitlement.ends_at)}.
      </p>
      <p>
        This licence is not bound yet. Binding checks that a Fugaro installation answers in the
        project: run <code>fugaro setup</code> there first, then enter its Firebase project id.
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          void onBind(entitlement.id, projectId.trim())
        }}
      >
        <label htmlFor={`bind-${entitlement.id}`}>Firebase project id</label>
        <input
          id={`bind-${entitlement.id}`}
          name="project_id"
          required
          value={projectId}
          onChange={(event) => {
            setProjectId(event.target.value)
          }}
        />
        <button type="submit">Bind</button>
      </form>
    </div>
  )
}

function Card({
  workspace,
  entitlement,
  licence,
  onBind,
  onReveal,
  onDownload,
  onAsk,
}: {
  workspace: Workspace
  entitlement: Entitlement | null
  licence: KeyLicence | null
  onBind: (entitlementId: string, projectId: string) => Promise<void>
  onReveal: (workspaceId: string) => Promise<void>
  onDownload: (workspaceId: string) => Promise<void>
  onAsk: (path: string, workspaceId: string) => Promise<void>
}) {
  const [projectId, setProjectId] = useState('')
  return (
    <section className="workspace" aria-label={workspace.name}>
      <h2>{workspace.name}</h2>
      <p>{workspace.project_id ?? 'No Firebase project yet'}</p>

      {entitlement === null ? null : (
        <p>
          {entitlement.tier} licence, {entitlement.seats} seats, renews on{' '}
          {day(entitlement.ends_at)}.
        </p>
      )}

      {workspace.project_id === null ? null : (
        <>
          {entitlement?.air_gapped === true ? (
            licence === null ? (
              <button type="button" onClick={() => void onDownload(workspace.id)}>
                Get the licence file
              </button>
            ) : (
              // A data URL rather than a fetched object: the file is signed text
              // that holds no secret (D2206), so there is nothing to revoke and
              // nothing to keep alive, and the same bytes are offered every time.
              <a
                download="fugaro-licence.txt"
                href={`data:text/plain;charset=utf-8,${encodeURIComponent(licence.licence)}`}
              >
                Download the licence file
              </a>
            )
          ) : (
            <button type="button" onClick={() => void onReveal(workspace.id)}>
              Reveal the key
            </button>
          )}

          {entitlement === null ? null : (
            <form
              onSubmit={(event) => {
                event.preventDefault()
                void onBind(entitlement.id, projectId.trim())
              }}
            >
              <label htmlFor={`rebind-${workspace.id}`}>Firebase project id</label>
              <input
                id={`rebind-${workspace.id}`}
                name="project_id"
                required
                value={projectId}
                onChange={(event) => {
                  setProjectId(event.target.value)
                }}
              />
              <button type="submit">Rebind</button>
            </form>
          )}
        </>
      )}

      {entitlement === null ? (
        <>
          <button type="button" onClick={() => void onAsk('/trial', workspace.id)}>
            Start a trial
          </button>
          <button type="button" onClick={() => void onAsk('/opensource', workspace.id)}>
            Request an open-source licence
          </button>
        </>
      ) : null}
    </section>
  )
}
