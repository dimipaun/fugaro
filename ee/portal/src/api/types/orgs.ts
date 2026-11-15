// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Organisations, the roles people hold in them, and a sign-up (portal plan
// P1.4; F20). `ee/portal/schemas/orgs.schema.json` is the same shapes for the
// Go side and `ee/portal/schemas/fixtures/orgs.*.json` are samples both sides
// are held to: `test/types.test.ts` validates every fixture against the schema
// with ajv and type-checks it against the declarations below, so a field one
// side grows and the other does not is a failing test rather than a screen
// drawing nothing.
//
// Hand-written rather than generated, which is the design (F20): the schemas
// are small, and a generator is a build step whose output nobody reads.

/** What kind of tenant an organisation is, set once and never moved. */
export type OrganisationType = 'operations' | 'customer' | 'reseller'

/**
 * What a person may decide in one organisation. It lives in the membership and
 * never in the sign-in token: a role in a token is a role only the identity
 * provider can take away.
 */
export type Role = 'owner' | 'admin' | 'member'

/** One acceptance of the terms of service, recorded at sign-up. */
export interface Terms {
  /** The version as the terms themselves spell it -- not a date. */
  version: string
  accepted_at: string
}

/** One organisation, as the API answers it to a member of it. */
export interface Organisation {
  /** Opaque, minted by the API: not a Firebase project id and not a domain. */
  id: string
  name: string
  type: OrganisationType
  /** Null says there is no record, which is not a record with no version. */
  terms: Terms | null
  /** The reseller that created this customer on its behalf, or null. */
  reseller_id: string | null
  created_at: string
}

/** One person's place in one organisation. */
export interface Membership {
  organisation_id: string
  /** The Firebase Auth uid, written at the invited address's first sign-in. */
  uid: string
  email: string
  role: Role
  created_at: string
  /** Null while this person has never worked in this organisation. */
  last_used_at: string | null
}

/**
 * A sign-up's request. The type is customer and is not in the body: only
 * operations creates a reseller organisation.
 */
export interface OrganisationCreate {
  name: string
}

/** Everything `orgs.schema.json` describes. */
export type Orgs = Organisation | Membership | OrganisationCreate
