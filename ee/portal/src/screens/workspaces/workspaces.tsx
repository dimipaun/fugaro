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
// **The reveals are the only credentials this app ever holds, and it holds each
// for one mount.** `POST /keys/reveal` rotates and answers the key and its
// renewal secret once (D2182 as amended); this screen keeps them in React state,
// draws them on the workspace they belong to, and does nothing else with them --
// no storage, no refetch on the way back, no copy kept beside the request. A key
// that a screen could redraw is a key the app has stored, which is the thing
// D2182 refuses. An air-gapped
// entitlement is the opposite case and the same rule: it has no secret at all
// (D2206), so it is offered as a file to download instead of a reveal -- and it
// is offered no rebind either, because its key is never revoked and a rebind
// would mint a second copy of it for good.
//
// **What a card offers is read off the entitlement and never off the project.**
// A workspace keeps its Firebase project after its licence lapses, which is the
// ordinary shape of a lapsed licence and not an error, so a card that offered a
// reveal whenever a project was named would offer a key for a licence that is
// no longer there -- a button whose only answer is the API's refusal.
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

// The two asks a workspace with no licence offers, as the API registers them:
// the trial's own door and the open-source programme's (ee/keyservice's
// `RouteTrial` and `RouteAPIOpenSource`). They are named here rather than
// written at the button because the paths and the bodies are one contract and a
// typo in either is a request the API answers 404 `no_route` about.
const TRIAL_PATH = '/trial'
const OPEN_SOURCE_PATH = '/open-source'

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

/**
 * The licences that are granted and not yet bound (D2196, D2199, F25).
 *
 * An entitlement names a workspace or names none: sign-up's free seat is
 * granted to the organisation before any workspace exists, which is the state
 * the schema calls "granted outside any workspace" and exactly the state that
 * needs binding (F6, D2184). So this asks the entitlement and not a card --
 * filtering on a workspace would drop the one licence a person has and leave
 * them no way to bind it, while a card's own entitlement is found through
 * `workspace.entitlement_id` anyway.
 */
function unbound(entitlements: readonly Entitlement[]): Entitlement[] {
  return entitlements.filter(
    (entitlement) => entitlement.bound_at === null && entitlement.status === 'active',
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
  // The bundles are the only credentials this app ever holds, and there is one
  // per workspace rather than one for the screen: a reveal rotates the secret
  // and answers once per workspace per day (P3.2), so the copy on the screen is
  // the only one there is until tomorrow. A single slot would mean revealing a
  // second workspace silently threw the first workspace's new key away, and
  // left a key drawn with no workspace said on it.
  const [bundles, setBundles] = useState<Record<string, KeyReveal>>({})
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
      setBundles((current) => ({ ...current, [workspaceId]: answer }))
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

  /** The trial ask names the **Firebase project id** and never the workspace's
   * own id (K2.7's `TrialCall`): what a trial licenses is an installation, and
   * the portal's workspace id — the 43-character one this screen draws — means
   * nothing to the intake. */
  function trial(projectId: string): Promise<void> {
    return run(async () => {
      await client.request(TRIAL_PATH, { method: 'POST', body: { workspace_id: projectId } })
      await reload()
    })
  }

  /** The open-source ask names the repositories the licence would cover, one
   * entry each (`OpenSourceCall`), split here the way the API splits them: a
   * pasted list arrives with blank lines in it, and a blank line is not a
   * repository. */
  function openSource(repositories: string): Promise<void> {
    const lines = repositories
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line !== '')
    return run(async () => {
      await client.request(OPEN_SOURCE_PATH, { method: 'POST', body: { repositories: lines } })
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
            bundle={bundles[workspace.id] ?? null}
            onBind={bind}
            onReveal={reveal}
            onDownload={download}
            onTrial={trial}
            onOpenSource={openSource}
            onCopy={copy}
          />
        ))
      )}
    </main>
  )
}

/**
 * One workspace's revealed bundle, drawn on that workspace's own card. It is
 * inside the card rather than above the list so the key is never read without
 * the workspace it belongs to: two of them can be on the screen at once, and a
 * bundle named only "your key" is one a person has to guess the target of.
 */
function Revealed({
  bundle,
  workspace,
  onCopy,
}: {
  bundle: KeyReveal
  workspace: Workspace
  onCopy: (value: string) => void
}) {
  return (
    <section className="workspaces__bundle" aria-label={`The key for ${workspace.name}`}>
      <h3>The key for {workspace.name}</h3>
      <p>
        This is shown once and nothing keeps it. Copy the key and its renewal secret into the
        installation bound to {workspace.project_id} now.
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
  bundle,
  onBind,
  onReveal,
  onDownload,
  onTrial,
  onOpenSource,
  onCopy,
}: {
  workspace: Workspace
  entitlement: Entitlement | null
  licence: KeyLicence | null
  bundle: KeyReveal | null
  onBind: (entitlementId: string, projectId: string) => Promise<void>
  onReveal: (workspaceId: string) => Promise<void>
  onDownload: (workspaceId: string) => Promise<void>
  onTrial: (projectId: string) => Promise<void>
  onOpenSource: (repositories: string) => Promise<void>
  onCopy: (value: string) => void
}) {
  const [projectId, setProjectId] = useState('')
  // The trial's own field and the ask's own lines. They are separate from the
  // binding's `projectId` above because they mean different things: the trial's
  // id is what the licence is asked for, and the binding's is what the licence
  // is attached to afterwards.
  const [trialProjectId, setTrialProjectId] = useState('')
  const [repositories, setRepositories] = useState('')
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

      {bundle === null ? null : (
        <Revealed bundle={bundle} workspace={workspace} onCopy={onCopy} />
      )}

      {entitlement === null || workspace.project_id === null ? null : entitlement.air_gapped ? (
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

      {/* The binding is offered to a licence whose workspace has a key behind it
          but names no project, as well as to one that has moved project: a
          trial's licence is granted for a project and claims none of it — the
          trial route writes no project id, because only a binding may, and only
          after it probes the installation (D2204, D2205, TN-P1) — so the form is
          what attaches it. A licence whose workspace has no key behind it yet is
          the "Not bound yet" section's, which carries the one instruction that
          finishes that job; offering it here as well would put two bindings for
          one licence on one screen. */}
      {entitlement === null || entitlement.air_gapped || workspace.bound_at === null ? null : (
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
          <button type="submit">{workspace.project_id === null ? 'Bind' : 'Rebind'}</button>
        </form>
      )}

      {entitlement === null ? (
        <>
          {/* The id the trial is asked for is the Firebase project id and not
              this workspace's own: what a trial licenses is an installation. */}
          <form
            onSubmit={(event) => {
              event.preventDefault()
              void onTrial(trialProjectId.trim())
            }}
          >
            <label htmlFor={`trial-${workspace.id}`}>Firebase project id</label>
            <input
              id={`trial-${workspace.id}`}
              name="workspace_id"
              required
              value={trialProjectId}
              onChange={(event) => {
                setTrialProjectId(event.target.value)
              }}
            />
            <button type="submit">Start a trial</button>
          </form>

          {/* The repositories the licence would cover, one to a line, as the
              API's own body reads them (`OpenSourceCall`). */}
          <form
            onSubmit={(event) => {
              event.preventDefault()
              void onOpenSource(repositories)
            }}
          >
            <label htmlFor={`opensource-${workspace.id}`}>Repositories</label>
            <textarea
              id={`opensource-${workspace.id}`}
              name="repositories"
              value={repositories}
              onChange={(event) => {
                setRepositories(event.target.value)
              }}
            />
            <button type="submit">Request an open-source licence</button>
          </form>
        </>
      ) : null}
    </section>
  )
}
