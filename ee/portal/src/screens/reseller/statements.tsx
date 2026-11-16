// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// A reseller's monthly statements (portal plan P6.4; spec §Resellers, D2189).
//
// **The direction is the one D2190 settled.** A statement is what the reseller
// owes Fugaro for a month of accrual -- days times seats times the wholesale
// rate -- and not what Fugaro owes the reseller: there is no commission here,
// and the word for an unpaid statement is "Open" rather than "due", because the
// money moves one way.
//
// **Accrued and paid are drawn as two figures.** A statement is issued before
// it is paid (P6.3) and a partial payment leaves it open, so collapsing them
// into one balance would lose the half a reseller is reconciling -- which is
// what the difference between them is for.
//
// **A line is named by its customer when the customers list knows it.** The
// statement's line carries an organisation id and the screen has just read the
// names; a reseller reads the month by customer, and the id is what is shown
// when a line names an organisation the list did not carry -- a customer linked
// after the month closed, say -- because the figure is still owed and an empty
// cell would not say so.

import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'
import { day, usd } from './format'

export interface StatementsSectionProps {
  statements: readonly Statement[]
  /** The reseller's customers, read for the names the lines are drawn with. */
  customers: readonly ResellerCustomer[]
}

export function StatementsSection({ statements, customers }: StatementsSectionProps) {
  const names = new Map(customers.map((customer) => [customer.organisation_id, customer.name]))

  return (
    <section aria-labelledby="reseller-statements">
      <h2 id="reseller-statements">Monthly statements</h2>
      {statements.length === 0 ? (
        <p>No statements yet.</p>
      ) : (
        <ul className="reseller__statements">
          {statements.map((statement) => (
            <li key={statement.id} className="reseller__statement">
              <h3>{statement.month}</h3>
              <p className="reseller__state">{stateWord(statement.state)}</p>
              <dl className="reseller__amounts">
                <div>
                  <dt>Accrued</dt>
                  <dd>{usd(statement.accrued_micros)}</dd>
                </div>
                <div>
                  <dt>Paid</dt>
                  <dd>{usd(statement.paid_micros)}</dd>
                </div>
                <div>
                  <dt>Issued</dt>
                  <dd>{day(statement.issued_at)}</dd>
                </div>
                {statement.paid_at === null ? null : (
                  <div>
                    <dt>Paid on</dt>
                    <dd>{day(statement.paid_at)}</dd>
                  </div>
                )}
              </dl>
              <table className="reseller__table">
                <caption className="reseller__caption">What each customer accrued that month.</caption>
                <thead>
                  <tr>
                    <th scope="col">Customer</th>
                    <th scope="col">Accrued</th>
                  </tr>
                </thead>
                <tbody>
                  {statement.lines.map((line) => (
                    <tr key={line.organisation_id}>
                      <th scope="row">{names.get(line.organisation_id) ?? line.organisation_id}</th>
                      <td>{usd(line.amount_micros)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/**
 * The state in a person's word. A word this build does not know is shown as the
 * API spelled it: the screen's own reading of a state is what a later release
 * keeps in step, and a reader is better served by the wire's word than by an
 * empty cell.
 */
function stateWord(state: string): string {
  switch (state) {
    case 'open':
      return 'Open'
    case 'paid':
      return 'Paid'
    default:
      return state
  }
}
