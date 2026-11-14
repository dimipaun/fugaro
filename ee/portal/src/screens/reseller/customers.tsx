// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The customers a reseller sold to (portal plan P6.4; spec §Resellers).
//
// **What a row is.** The link, the organisation and the day it was made: the
// shape a reseller is served is `resellers.schema.json`'s customer, and it
// carries nothing of the customer's own -- not a member, not a workspace and
// not a key. That is D2191's link drawn as it is rather than as it could be, and
// it is why this file has no column to hide and no action to offer.
//
// The organisation id is shown beside the name because it is the id a reseller
// quotes when it asks operations about a customer: a name is what a person
// recognises and an id is what anybody else acts on.
//
// The empty list is a sentence and not an empty table, for the case the screen
// exists for: a reseller that has created no customer yet reads that it has
// none, rather than reading a header over nothing.

import type { ResellerCustomer } from '../../api/types/resellers'
import { day } from './format'

export function CustomersSection({ customers }: { customers: readonly ResellerCustomer[] }) {
  return (
    <section aria-labelledby="reseller-customers">
      <h2 id="reseller-customers">Customers</h2>
      {customers.length === 0 ? (
        <p>No customers yet.</p>
      ) : (
        <table className="reseller__table">
          <caption className="reseller__caption">
            The organisations you created, and the day each was linked to you.
          </caption>
          <thead>
            <tr>
              <th scope="col">Customer</th>
              <th scope="col">Organisation</th>
              <th scope="col">Linked</th>
            </tr>
          </thead>
          <tbody>
            {customers.map((customer) => (
              <tr key={customer.organisation_id}>
                <th scope="row">{customer.name}</th>
                <td className="reseller__id">{customer.organisation_id}</td>
                <td>{day(customer.linked_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
