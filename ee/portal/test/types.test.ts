// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Every fixture validates with ajv, and every fixture is the type this app
// declares (portal plan P1.4; F20).
//
// **Two oracles, because neither alone is enough.** The schema files under
// `ee/portal/schemas/` are what the Go side writes and reads, and the fixtures
// beside them are samples the two sides share: ajv validates each fixture
// against its own `$defs` entry, values and unknown keys included
// (`additionalProperties: false` is what refuses a field the schema does not
// declare, and every schema requires all of its properties, so a fixture's keys
// are the schema's). The hand-written types under `src/api/types/` are what the
// screens read, and each row below carries the two assertions that hold them to
// that fixture, both run by the compiler rather than by vitest: the fixture
// `satisfies` the type, so a field the type declares and the fixture has not, or
// a value of a kind the type cannot hold, does not compile; and the type's keys
// are `Identical` to the fixture's, so a field the fixture has and the type has
// not fails too. The second reading is the one worth naming -- it is the drift
// that reaches a screen as an `undefined` with nothing to catch it.
//
// **The set of fixtures is not a list somebody maintains.** The rows below are
// held against the directory and the schemas: every `<area>.<definition>.json`
// file must appear, and every `$defs` entry of every schema must have one. A
// new area or definition that arrives without a fixture and a type fails here,
// which is the point -- a fixture nobody checks is a shape whose two sides have
// never been compared.
//
// The fixtures carry no secret (the plan's rule for this directory): the shape
// that could is a licence key, and its fixtures are the metadata around one.
// Nothing here reads a key, a token or a bundle.

import Ajv2020 from 'ajv/dist/2020'
import { describe, expect, it } from 'vitest'

import auditSchema from '../schemas/audit.schema.json'
import auditEntry from '../schemas/fixtures/audit.entry.json'
import { type AuditEntry } from '../src/api/types/audit'
import billingSchema from '../schemas/billing.schema.json'
import billingSeats from '../schemas/fixtures/billing.seats.json'
import billingSubscription from '../schemas/fixtures/billing.subscription.json'
import { type Subscription, type SubscriptionSeats } from '../src/api/types/billing'
import checkoutSchema from '../schemas/checkout.schema.json'
import checkoutCreate from '../schemas/fixtures/checkout.create.json'
import checkoutSession from '../schemas/fixtures/checkout.session.json'
import { type CheckoutCreate, type CheckoutSession } from '../src/api/types/checkout'
import entitlementsSchema from '../schemas/entitlements.schema.json'
import entitlementsEntitlement from '../schemas/fixtures/entitlements.entitlement.json'
import entitlementsMint from '../schemas/fixtures/entitlements.mint.json'
import { type Entitlement, type EntitlementMint } from '../src/api/types/entitlements'
import errorSchema from '../schemas/error.schema.json'
import errorRefusal from '../schemas/fixtures/error.refusal.json'
import { type ErrorRefusal } from '../src/api/types/error'
import invitesSchema from '../schemas/invites.schema.json'
import invitesCreate from '../schemas/fixtures/invites.create.json'
import invitesInvite from '../schemas/fixtures/invites.invite.json'
import { type Invite, type InviteCreate } from '../src/api/types/invites'
import keysSchema from '../schemas/keys.schema.json'
import keysLicence from '../schemas/fixtures/keys.licence.json'
import keysMetadata from '../schemas/fixtures/keys.metadata.json'
import keysReveal from '../schemas/fixtures/keys.reveal.json'
import { type KeyLicence, type KeyMetadata, type KeyReveal } from '../src/api/types/keys'
import ledgerSchema from '../schemas/ledger.schema.json'
import ledgerEntry from '../schemas/fixtures/ledger.entry.json'
import ledgerEvent from '../schemas/fixtures/ledger.event.json'
import { type LedgerEntry, type LedgerEvent } from '../src/api/types/ledger'
import opsSchema from '../schemas/ops.schema.json'
import opsEntitlementAction from '../schemas/fixtures/ops.entitlement_action.json'
import opsEventResolution from '../schemas/fixtures/ops.event_resolution.json'
import opsLists from '../schemas/fixtures/ops.lists.json'
import opsProjectTransfer from '../schemas/fixtures/ops.project_transfer.json'
import opsQueueDecision from '../schemas/fixtures/ops.queue_decision.json'
import {
  type OpsEntitlementAction,
  type OpsEventResolution,
  type OpsLists,
  type OpsProjectTransfer,
  type OpsQueueDecision,
} from '../src/api/types/ops'
import orgsSchema from '../schemas/orgs.schema.json'
import orgsCreate from '../schemas/fixtures/orgs.create.json'
import orgsMembership from '../schemas/fixtures/orgs.membership.json'
import orgsOrganisation from '../schemas/fixtures/orgs.organisation.json'
import {
  type Membership,
  type Organisation,
  type OrganisationCreate,
} from '../src/api/types/orgs'
import reportsSchema from '../schemas/reports.schema.json'
import reportsCustomer from '../schemas/fixtures/reports.customer.json'
import reportsMonth from '../schemas/fixtures/reports.month.json'
import { type ReportCustomer, type ReportMonth } from '../src/api/types/reports'
import resellersSchema from '../schemas/resellers.schema.json'
import resellersCustomer from '../schemas/fixtures/resellers.customer.json'
import resellersRelink from '../schemas/fixtures/resellers.relink.json'
import resellersReseller from '../schemas/fixtures/resellers.reseller.json'
import {
  type Reseller,
  type ResellerCustomer,
  type ResellerRelink,
} from '../src/api/types/resellers'
import statementsSchema from '../schemas/statements.schema.json'
import statementsPayment from '../schemas/fixtures/statements.payment.json'
import statementsStatement from '../schemas/fixtures/statements.statement.json'
import { type Statement, type StatementPayment } from '../src/api/types/statements'
import workspacesSchema from '../schemas/workspaces.schema.json'
import workspacesBind from '../schemas/fixtures/workspaces.bind.json'
import workspacesWorkspace from '../schemas/fixtures/workspaces.workspace.json'
import { type Workspace, type WorkspaceBind } from '../src/api/types/workspaces'

/**
 * How a declared type looks from the fixture's side: a union of string literals
 * is a `string` here, because a JSON module reads a value and not the intent
 * behind it, and a number is a `number`. Everything else -- which keys, which
 * nesting, which of them may be null -- is what the two sides have to agree on,
 * and that is what survives. It goes one way only: the fixture `satisfies` this,
 * and the reverse direction is the key comparison below, because a field the
 * fixture holds a non-null value for is `string` to a reader and `string | null`
 * to the schema.
 */
type JsonShape<T> = T extends string
  ? string
  : T extends number
    ? number
    : T extends boolean
      ? boolean
      : T extends readonly (infer U)[]
        ? JsonShape<U>[]
        : T extends object
          ? { [K in keyof T]: JsonShape<T[K]> }
          : T

/**
 * `true` only where each type is the other's, and `never` everywhere else --
 * which is what makes it usable as an assertion: `true` does not satisfy
 * `never`, so the rows below compile only where the pair matches, in both
 * directions.
 */
type Identical<A, B> = [A] extends [B] ? ([B] extends [A] ? true : never) : never

interface Fixture {
  /** The fixture's file name without `.json`: `<area>.<definition>`. */
  name: string
  schema: object
  /** The `$defs` entry the fixture is an instance of. */
  definition: string
  /**
   * The compiler's proof that the type declares exactly the fields this fixture
   * carries; always `true` at runtime, and the test below asserts that as well,
   * so a row cannot be added with the check left out.
   */
  same: true
  /** The fixture, held to the type by the `satisfies` on every row. */
  fixture: unknown
}

const FIXTURES: Fixture[] = [
  {
    name: 'audit.entry',
    schema: auditSchema,
    definition: 'entry',
    same: true satisfies Identical<keyof JsonShape<AuditEntry>, keyof typeof auditEntry>,
    fixture: auditEntry satisfies JsonShape<AuditEntry>,
  },
  {
    name: 'billing.seats',
    schema: billingSchema,
    definition: 'seats',
    same: true satisfies Identical<keyof JsonShape<SubscriptionSeats>, keyof typeof billingSeats>,
    fixture: billingSeats satisfies JsonShape<SubscriptionSeats>,
  },
  {
    name: 'billing.subscription',
    schema: billingSchema,
    definition: 'subscription',
    same: true satisfies Identical<keyof JsonShape<Subscription>, keyof typeof billingSubscription>,
    fixture: billingSubscription satisfies JsonShape<Subscription>,
  },
  {
    name: 'checkout.create',
    schema: checkoutSchema,
    definition: 'create',
    same: true satisfies Identical<keyof JsonShape<CheckoutCreate>, keyof typeof checkoutCreate>,
    fixture: checkoutCreate satisfies JsonShape<CheckoutCreate>,
  },
  {
    name: 'checkout.session',
    schema: checkoutSchema,
    definition: 'session',
    same: true satisfies Identical<keyof JsonShape<CheckoutSession>, keyof typeof checkoutSession>,
    fixture: checkoutSession satisfies JsonShape<CheckoutSession>,
  },
  {
    name: 'entitlements.entitlement',
    schema: entitlementsSchema,
    definition: 'entitlement',
    same: true satisfies Identical<keyof JsonShape<Entitlement>, keyof typeof entitlementsEntitlement>,
    fixture: entitlementsEntitlement satisfies JsonShape<Entitlement>,
  },
  {
    name: 'entitlements.mint',
    schema: entitlementsSchema,
    definition: 'mint',
    same: true satisfies Identical<keyof JsonShape<EntitlementMint>, keyof typeof entitlementsMint>,
    fixture: entitlementsMint satisfies JsonShape<EntitlementMint>,
  },
  {
    name: 'error.refusal',
    schema: errorSchema,
    definition: 'refusal',
    same: true satisfies Identical<keyof JsonShape<ErrorRefusal>, keyof typeof errorRefusal>,
    fixture: errorRefusal satisfies JsonShape<ErrorRefusal>,
  },
  {
    name: 'invites.create',
    schema: invitesSchema,
    definition: 'create',
    same: true satisfies Identical<keyof JsonShape<InviteCreate>, keyof typeof invitesCreate>,
    fixture: invitesCreate satisfies JsonShape<InviteCreate>,
  },
  {
    name: 'invites.invite',
    schema: invitesSchema,
    definition: 'invite',
    same: true satisfies Identical<keyof JsonShape<Invite>, keyof typeof invitesInvite>,
    fixture: invitesInvite satisfies JsonShape<Invite>,
  },
  {
    name: 'keys.licence',
    schema: keysSchema,
    definition: 'licence',
    same: true satisfies Identical<keyof JsonShape<KeyLicence>, keyof typeof keysLicence>,
    fixture: keysLicence satisfies JsonShape<KeyLicence>,
  },
  {
    name: 'keys.metadata',
    schema: keysSchema,
    definition: 'metadata',
    same: true satisfies Identical<keyof JsonShape<KeyMetadata>, keyof typeof keysMetadata>,
    fixture: keysMetadata satisfies JsonShape<KeyMetadata>,
  },
  {
    name: 'keys.reveal',
    schema: keysSchema,
    definition: 'reveal',
    same: true satisfies Identical<keyof JsonShape<KeyReveal>, keyof typeof keysReveal>,
    fixture: keysReveal satisfies JsonShape<KeyReveal>,
  },
  {
    name: 'ledger.entry',
    schema: ledgerSchema,
    definition: 'entry',
    same: true satisfies Identical<keyof JsonShape<LedgerEntry>, keyof typeof ledgerEntry>,
    fixture: ledgerEntry satisfies JsonShape<LedgerEntry>,
  },
  {
    name: 'ledger.event',
    schema: ledgerSchema,
    definition: 'event',
    same: true satisfies Identical<keyof JsonShape<LedgerEvent>, keyof typeof ledgerEvent>,
    fixture: ledgerEvent satisfies JsonShape<LedgerEvent>,
  },
  {
    name: 'ops.entitlement_action',
    schema: opsSchema,
    definition: 'entitlement_action',
    same: true satisfies Identical<keyof JsonShape<OpsEntitlementAction>, keyof typeof opsEntitlementAction>,
    fixture: opsEntitlementAction satisfies JsonShape<OpsEntitlementAction>,
  },
  {
    name: 'ops.event_resolution',
    schema: opsSchema,
    definition: 'event_resolution',
    same: true satisfies Identical<keyof JsonShape<OpsEventResolution>, keyof typeof opsEventResolution>,
    fixture: opsEventResolution satisfies JsonShape<OpsEventResolution>,
  },
  {
    name: 'ops.lists',
    schema: opsSchema,
    definition: 'lists',
    same: true satisfies Identical<keyof JsonShape<OpsLists>, keyof typeof opsLists>,
    fixture: opsLists satisfies JsonShape<OpsLists>,
  },
  {
    name: 'ops.project_transfer',
    schema: opsSchema,
    definition: 'project_transfer',
    same: true satisfies Identical<keyof JsonShape<OpsProjectTransfer>, keyof typeof opsProjectTransfer>,
    fixture: opsProjectTransfer satisfies JsonShape<OpsProjectTransfer>,
  },
  {
    name: 'ops.queue_decision',
    schema: opsSchema,
    definition: 'queue_decision',
    same: true satisfies Identical<keyof JsonShape<OpsQueueDecision>, keyof typeof opsQueueDecision>,
    fixture: opsQueueDecision satisfies JsonShape<OpsQueueDecision>,
  },
  {
    name: 'orgs.create',
    schema: orgsSchema,
    definition: 'create',
    same: true satisfies Identical<keyof JsonShape<OrganisationCreate>, keyof typeof orgsCreate>,
    fixture: orgsCreate satisfies JsonShape<OrganisationCreate>,
  },
  {
    name: 'orgs.membership',
    schema: orgsSchema,
    definition: 'membership',
    same: true satisfies Identical<keyof JsonShape<Membership>, keyof typeof orgsMembership>,
    fixture: orgsMembership satisfies JsonShape<Membership>,
  },
  {
    name: 'orgs.organisation',
    schema: orgsSchema,
    definition: 'organisation',
    same: true satisfies Identical<keyof JsonShape<Organisation>, keyof typeof orgsOrganisation>,
    fixture: orgsOrganisation satisfies JsonShape<Organisation>,
  },
  {
    name: 'reports.customer',
    schema: reportsSchema,
    definition: 'customer',
    same: true satisfies Identical<keyof JsonShape<ReportCustomer>, keyof typeof reportsCustomer>,
    fixture: reportsCustomer satisfies JsonShape<ReportCustomer>,
  },
  {
    name: 'reports.month',
    schema: reportsSchema,
    definition: 'month',
    same: true satisfies Identical<keyof JsonShape<ReportMonth>, keyof typeof reportsMonth>,
    fixture: reportsMonth satisfies JsonShape<ReportMonth>,
  },
  {
    name: 'resellers.customer',
    schema: resellersSchema,
    definition: 'customer',
    same: true satisfies Identical<keyof JsonShape<ResellerCustomer>, keyof typeof resellersCustomer>,
    fixture: resellersCustomer satisfies JsonShape<ResellerCustomer>,
  },
  {
    name: 'resellers.relink',
    schema: resellersSchema,
    definition: 'relink',
    same: true satisfies Identical<keyof JsonShape<ResellerRelink>, keyof typeof resellersRelink>,
    fixture: resellersRelink satisfies JsonShape<ResellerRelink>,
  },
  {
    name: 'resellers.reseller',
    schema: resellersSchema,
    definition: 'reseller',
    same: true satisfies Identical<keyof JsonShape<Reseller>, keyof typeof resellersReseller>,
    fixture: resellersReseller satisfies JsonShape<Reseller>,
  },
  {
    name: 'statements.payment',
    schema: statementsSchema,
    definition: 'payment',
    same: true satisfies Identical<keyof JsonShape<StatementPayment>, keyof typeof statementsPayment>,
    fixture: statementsPayment satisfies JsonShape<StatementPayment>,
  },
  {
    name: 'statements.statement',
    schema: statementsSchema,
    definition: 'statement',
    same: true satisfies Identical<keyof JsonShape<Statement>, keyof typeof statementsStatement>,
    fixture: statementsStatement satisfies JsonShape<Statement>,
  },
  {
    name: 'workspaces.bind',
    schema: workspacesSchema,
    definition: 'bind',
    same: true satisfies Identical<keyof JsonShape<WorkspaceBind>, keyof typeof workspacesBind>,
    fixture: workspacesBind satisfies JsonShape<WorkspaceBind>,
  },
  {
    name: 'workspaces.workspace',
    schema: workspacesSchema,
    definition: 'workspace',
    same: true satisfies Identical<keyof JsonShape<Workspace>, keyof typeof workspacesWorkspace>,
    fixture: workspacesWorkspace satisfies JsonShape<Workspace>,
  },
]

const names = (): string[] => FIXTURES.map((fixture) => fixture.name).sort()

/** Every fixture file in the directory, by its name without `.json`. */
const onDisk = Object.keys(import.meta.glob('../schemas/fixtures/*.json'))
  .map((path) => (path.split('/').pop() ?? '').replace(/\.json$/, ''))
  .sort()

/** Every schema file, by the area it describes. */
const SCHEMAS = Object.fromEntries(
  Object.entries(
    import.meta.glob<{ $defs?: Record<string, unknown> }>('../schemas/*.schema.json', {
      eager: true,
    }),
  ).map(([path, schema]) => [(path.split('/').pop() ?? '').replace(/\.schema\.json$/, ''), schema]),
)

/** Every `<area>.<definition>` every schema declares in its `$defs`. */
const declared = Object.entries(SCHEMAS)
  .flatMap(([area, schema]) =>
    Object.keys(schema.$defs ?? {}).map((definition) => `${area}.${definition}`),
  )
  .sort()

/** Every definition a value refers to, following `#/$defs/<name>`. */
function references(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.flatMap(references)
  }
  if (value === null || typeof value !== 'object') {
    return []
  }
  return Object.entries(value).flatMap(([key, child]) => {
    if (key === '$ref' && typeof child === 'string' && child.startsWith('#/$defs/')) {
      return [child]
    }
    return references(child)
  })
}

/**
 * Every `<area>.<definition>` some other definition of the same schema names,
 * which is a shape a fixture carries inside it rather than on its own: a
 * statement's lines, an organisation's terms.
 */
const nested = Object.entries(SCHEMAS)
  .flatMap(([area, schema]) =>
    Object.values(schema.$defs ?? {}).flatMap((shape) =>
      references(shape).map((reference) => `${area}.${reference.replace('#/$defs/', '')}`),
    ),
  )
  .sort()

function validate(fixture: Fixture): { valid: boolean; errors: string } {
  const schema = fixture.schema as { $defs?: Record<string, unknown> }
  // A private instance per fixture, and a wrapper rather than the file itself:
  // a schema file's `oneOf` names every definition it carries, and validating a
  // fixture against all of them would accept one that is merely *another*
  // definition's shape.
  //
  // `validateFormats` is off because the schemas declare `date-time` and this
  // package carries no format definitions: ajv would otherwise refuse the
  // schema itself in strict mode. Nothing here is weaker for it -- an instant's
  // shape is the writer's business, and the fixtures are what the Go side
  // produced.
  const ajv = new Ajv2020({ strict: true, allErrors: true, validateFormats: false })
  const check = ajv.compile({ $defs: schema.$defs, $ref: `#/$defs/${fixture.definition}` })
  const valid = check(fixture.fixture)
  return { valid, errors: ajv.errorsText(check.errors) }
}

describe('the portal API types', () => {
  it('validates every fixture against the definition it is an instance of', () => {
    for (const fixture of FIXTURES) {
      const { valid, errors } = validate(fixture)
      expect(valid, `${fixture.name}: ${errors}`).toBe(true)
    }
  })

  it('checks every fixture against the type the app declares', () => {
    // The check itself is the `same` field, which the compiler runs; this is
    // what keeps the table the whole set rather than the rows somebody
    // remembered to extend.
    expect(FIXTURES.every((fixture) => fixture.same)).toBe(true)
    expect(names()).toEqual(onDisk)
  })

  it('leaves no definition of a schema unchecked', () => {
    // A definition is checked when a fixture is an instance of it, or when it
    // is named inside one: `orgs.terms` has no fixture of its own and is part
    // of every organisation the fixtures carry. What is left over is a shape
    // no fixture has ever been compared against, which is the state this test
    // exists to refuse -- not a count, because the schemas are the Go side's
    // and one of them growing a definition is not this test's business.
    const sampled = new Set(names())
    const carried = new Set(nested)
    expect(declared.filter((name) => !sampled.has(name) && !carried.has(name))).toEqual([])
  })

  it('refuses a fixture that is another definition of the same schema', () => {
    // The counter-example the wrapper above exists for: `orgs.create` is a
    // valid `orgs.schema.json` instance -- it is the `create` arm of the file's
    // `oneOf` -- and it is not an organisation. A test that validated every
    // fixture against the whole file would call this one correct.
    const create = FIXTURES.find((fixture) => fixture.name === 'orgs.create')
    expect(create).toBeDefined()
    expect(validate({ ...create!, definition: 'organisation' }).valid).toBe(false)
  })
})
