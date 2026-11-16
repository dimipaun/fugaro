// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// What the two `licence login` screens share (portal plan P3.5; spec §11.3,
// §11.4; K2.4 and K3.3 as the portal's reconciliation amends them).
//
// **The pages are the SPA's and the flow is the service's.** K2.4's routes are
// what mints, what holds the codes and what sends the redirect to the
// terminal's listener; the two screens here are the pages a person reads on the
// way through, and this file is the part of them that is neither screen: the
// one shape of a licence to choose from, the one reading of which licences may
// be bound, and the one place a refusal becomes a sentence.
//
// **A refusal is the API's own sentence, and nothing else is invented.** The
// portal's API answers every failure with one error shape, which is a sentence
// written for a person (P1.0, `src/api/client.ts`), so a screen shows that and
// adds nothing to it -- an operator's message assembled from what a screen
// guessed about the failure is a person told something nobody wrote.
//
// **Nothing a caller sent is ever drawn as markup.** The installation's name
// and the Firebase project id are a stranger's text (K2.4's `untrustedName`
// bounds them and says so): React escapes a text node, and `Untrusted` is that
// text node named once, so the one place either screen renders such a string is
// a place a reader can look at. A page that built a link, a form action or an
// element out of one would be a page a caller chooses the shape of.

import type { ApiClient } from '../../api/client'
import { ApiError } from '../../api/client'
import type { Entitlement } from '../../api/types/entitlements'

/** This screen family's own sentence for a failure that is not the API's. */
export const GENERIC_FAILURE = 'The portal could not reach the licence site. Try again.'

/**
 * K2.4's sentence for a sign-in the service cannot verify, used here for the
 * page a terminal opened with parameters this flow cannot work with: it is the
 * same answer for the same reason, and one wording for it means a person who
 * sees it twice is not left wondering whether the two meant different things.
 */
export const UNVERIFIABLE =
  'This sign-in could not be verified. Return to your terminal and try again.'

/** The sentence a person is shown for a failure. */
export function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

// Pinned to a locale and to UTC, as P3.4's screen pins it: the API answers
// instants, and a term's end read off the machine's own locale is a date that
// moves for somebody else.
const DAY = new Intl.DateTimeFormat('en-GB', {
  day: 'numeric',
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
})

/** One licence as a line a person reads, without its id or its source. */
export function licenceLine(entitlement: Entitlement): string {
  return `${entitlement.tier} licence, ${entitlement.seats} seats, runs to ${DAY.format(
    new Date(entitlement.ends_at),
  )}`
}

/**
 * The licences the person may hand to the installation: granted, running, and
 * not bound to one yet (D2196, D2199, K3.3).
 *
 * It reads the entitlement and never a workspace, which is P3.4's own reading
 * of the same rule and is repeated rather than shared because the two screens
 * are separate directories: an entitlement granted before any workspace exists
 * is exactly the one a login has to bind, and filtering on a workspace would
 * drop it.
 */
export function unbound(entitlements: readonly Entitlement[]): Entitlement[] {
  return entitlements.filter(
    (entitlement) => entitlement.bound_at === null && entitlement.status === 'active',
  )
}

/** The organisation's licences, as P3.1's route answers them. */
export async function readLicences(client: ApiClient): Promise<Entitlement[]> {
  const answer = await client.request<{ entitlements: Entitlement[] }>('/entitlements')
  return answer.entitlements
}

/**
 * Binding the chosen licence to the installation is the one act either screen
 * takes (D2196, D2205): it is P3.1's route and P3.4's own call, so a login binds
 * the same way the workspace screen does and the two cannot disagree about what
 * a binding is.
 *
 * **What a binding is not is the choice of key.** K2.4's mint reads the
 * account's own subscription -- the newest uncancelled one recorded for the
 * verified address, `accountLicence` in the service -- and neither page sends a
 * licence with its handover, so an account holding two subscriptions is handed a
 * key for the newer one whichever entitlement the person bound here. What
 * decides the key is the entitlement model's to do (a mint asked for the licence
 * bound to a project id, P3.1's binding and P4.2's ledger: the gap recorded in
 * `docs/licence-key-v2/reconciliation.md`), and until it does, what the person
 * is choosing is which licence the installation holds on the portal's side.
 */
export function bind(client: ApiClient, entitlementId: string, projectId: string): Promise<void> {
  return client.request<void>('/workspaces/bind', {
    method: 'POST',
    body: { entitlement_id: entitlementId, project_id: projectId },
  })
}

/**
 * The caller's text, in the one element either screen draws it with.
 *
 * It is a `<code>` rather than a styled `<span>` because what it holds is an
 * identifier a person compares against a terminal: a name that wraps and is
 * read character by character is what the comparison needs, and the class is
 * what says it is not a value this portal chose.
 */
export function Untrusted({ value }: { value: string }) {
  return <code className="cli__untrusted">{value}</code>
}

/**
 * The callback the service will send the code to, built from the port the
 * terminal named.
 *
 * §11.3 step 4 is that address and never a choice this page makes: the service's
 * own `loopbackRedirect` validates it again and refuses anything that is not the
 * loopback on that port, so a `port` that is not a port number builds nothing --
 * and the caller of this is the page's own guard, which then refuses the sign-in
 * here rather than drawing a form whose only answer is the service's refusal.
 * The service's own page builds the same string from the same parameter, which
 * is the whole of why doing it here is safe: this page decides nothing the
 * service does not decide again.
 *
 * The rule is the service's, spelling for spelling: digits, in range, and no
 * padding, because what a padded port would normalise to is an address the
 * terminal's own listener is not on.
 */
export function loopbackCallback(port: string): string | null {
  if (!/^[0-9]{1,5}$/.test(port)) {
    return null
  }
  const number = Number(port)
  return number >= 1 && number <= 65535 && `${number}` === port
    ? `http://127.0.0.1:${port}/callback`
    : null
}

/**
 * Which licence the installation is bound to: the organisation's unbound ones,
 * or the sentence that says there are none.
 *
 * A single licence is preselected, because there is nothing to decide; two or
 * more are left for the person to choose, because a default among equals is a
 * licence bound by a click somebody did not mean. With none, the page still
 * hands a licence over -- the account's own, which is the one K2.4's mint reads
 * (`bind` above) -- because an organisation whose licences are all bound is the
 * ordinary repeat login, not a dead end.
 */
export function LicenceChoice({
  licences,
  chosen,
  onChoose,
}: {
  licences: readonly Entitlement[]
  chosen: string | null
  onChoose: (id: string) => void
}) {
  if (licences.length === 0) {
    return (
      <p className="cli__none">
        Every licence this organisation holds is bound to an installation already, so this login
        hands over the one your account runs on.
      </p>
    )
  }
  return (
    <fieldset className="cli__choice">
      <legend>Which licence does it get?</legend>
      {licences.map((entitlement) => (
        <label key={entitlement.id} className="cli__licence">
          <input
            type="radio"
            name="entitlement_id"
            value={entitlement.id}
            checked={chosen === entitlement.id}
            onChange={() => {
              onChoose(entitlement.id)
            }}
          />
          <span>{licenceLine(entitlement)}</span>
        </label>
      ))}
    </fieldset>
  )
}
