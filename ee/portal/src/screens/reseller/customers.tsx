// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The customers a reseller sold to, and their keys (portal plan P6.4; spec
// §Resellers, F5).
//
// **What a customer block is.** The link, the organisation and the day it was
// made, and then that customer's keys -- because "see the customers they sold
// to, those customers' keys and renewal dates" is what the spec promises a
// reseller, and a screen that stopped at the link would leave the renewal date
// of a key a reseller is invoiced for nowhere in the portal.
//
// **A key is drawn as F5's metadata and nothing else.** Key id, the workspace
// it is bound to, the row, the seats, the renewal date and the last refresh:
// that is the list F5 gives a reseller and operations, and it is the whole of
// what `KeyMetadata` carries. No column here could hold a key or a renewal
// secret, and no action here rotates one -- a reveal belongs to the customer
// organisation's own owners and admins (P3.2).
//
// The organisation id is shown beside the name because it is the id a reseller
// quotes when it asks operations about a customer: a name is what a person
// recognises and an id is what anybody else acts on.
//
// The two empty lists are sentences and not empty tables, for the cases the
// screen exists for: a reseller that has created no customer yet, and a
// customer that has bound no key yet -- a sale whose installation has not run
// `fugaro setup`, which is a thing a reseller chases.

import type { KeyMetadata } from '../../api/types/keys'
import type { ResellerCustomer } from '../../api/types/resellers'
import { day } from './format'

export interface CustomersSectionProps {
  customers: readonly ResellerCustomer[]
  /** Each customer's keys, by organisation id: what was read for the ones shown. */
  keys: ReadonlyMap<string, readonly KeyMetadata[]>
}

export function CustomersSection({ customers, keys }: CustomersSectionProps) {
  return (
    <section aria-labelledby="reseller-customers">
      <h2 id="reseller-customers">Customers</h2>
      {customers.length === 0 ? (
        <p>No customers yet.</p>
      ) : (
        <ul className="reseller__customers">
          {customers.map((customer) => (
            <li key={customer.organisation_id} className="reseller__customer">
              <h3>{customer.name}</h3>
              <p className="reseller__id">{customer.organisation_id}</p>
              <p className="reseller__linked">Linked {day(customer.linked_at)}</p>
              <CustomerKeys keys={keys.get(customer.organisation_id) ?? []} />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function CustomerKeys({ keys }: { keys: readonly KeyMetadata[] }) {
  if (keys.length === 0) {
    return <p>No keys yet.</p>
  }
  return (
    <table className="reseller__table">
      <caption className="reseller__caption">
        This customer's keys, as metadata: what a reseller reads of them, never what they open.
      </caption>
      <thead>
        <tr>
          <th scope="col">Key</th>
          <th scope="col">Workspace</th>
          <th scope="col">Row</th>
          <th scope="col">Seats</th>
          <th scope="col">Renews</th>
          <th scope="col">Last refresh</th>
        </tr>
      </thead>
      <tbody>
        {keys.map((key) => (
          <tr key={key.key_id}>
            <th scope="row">{key.key_id}</th>
            <td className="reseller__id">{key.workspace_id}</td>
            <td>{key.tier}</td>
            <td>{key.seats}</td>
            <td>{day(key.paid_through)}</td>
            {/* A key that has never been refreshed is telling a reseller that
                its customer's installation has not run, which is a fact with a
                word rather than an empty cell. */}
            <td>{key.last_seen === null ? 'Never' : day(key.last_seen)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
