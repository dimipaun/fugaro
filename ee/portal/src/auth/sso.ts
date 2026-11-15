// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The sign-in methods the portal offers, and the rule that decides which of
// them an organisation of each type accepts (portal plan P1.4; D2207, spec
// §Auth).
//
// **The rule is the API's, and this is only the courtesy that keeps a person
// from being refused after they have signed in.** `ee/keyservice` re-reads the
// token's `sign_in_provider` on every request and refuses an email-link
// identity for an operations or reseller membership (P1.3's
// `TestEmailLinkIsRefusedForOperations`, P2.1's
// `TestSSORuleFollowsTheActorsMembership`); nothing here is enforcement, and a
// caller that changed this file would still be refused by the API. What it
// buys is that the sign-in screen offers the methods that will work: an
// operations address is offered Google and not the email link, so the refusal
// the API would send is a screen nobody ever sees.
//
// The two organisation types are the rule's whole vocabulary (D2207):
// operations accepts Google only; a reseller accepts Google, or Microsoft only
// when operations has pinned at least one tenant for it -- with no tenant on
// the list a Microsoft sign-in is one the API cannot check, so it is not
// offered. A customer organisation is under no such rule and is offered every
// method the project enables (P0.1: Google, Apple, Microsoft and email link).

import type { OrganisationType } from '../api/types/orgs'

/** The sign-in providers Firebase reports in a token's `sign_in_provider`. */
export type SignInProvider =
  | 'google.com'
  | 'apple.com'
  | 'microsoft.com'
  | 'password'
  | 'emailLink'

/** The methods the sign-in screen can offer. One per provider but email link. */
export type SignInMethod = 'google' | 'apple' | 'microsoft' | 'emailLink'

/** The methods that are a federated provider rather than a link in the mail. */
export type FederatedMethod = Exclude<SignInMethod, 'emailLink'>

export const PROVIDER_OF: Record<SignInMethod, SignInProvider> = {
  google: 'google.com',
  apple: 'apple.com',
  microsoft: 'microsoft.com',
  emailLink: 'emailLink',
}

const FEDERATED: readonly FederatedMethod[] = ['google', 'apple', 'microsoft']

/**
 * True when an organisation of this type is one whose members must arrive
 * through a work identity: signing in by email link is refused for them.
 */
export function requiresSSO(type: OrganisationType): boolean {
  return type === 'operations' || type === 'reseller'
}

/**
 * The methods that will work for an organisation of this type, in the order
 * the screen offers them. `microsoftTenants` is operations' pinned list (P5.1):
 * an empty list means Microsoft is not offered to a reseller, because the API
 * would refuse a tenant it cannot check.
 */
export function signInMethods(
  type: OrganisationType,
  microsoftTenants: readonly string[] = [],
): readonly SignInMethod[] {
  if (!requiresSSO(type)) {
    return ['google', 'apple', 'microsoft', 'emailLink']
  }
  if (type === 'reseller' && microsoftTenants.length > 0) {
    return ['google', 'microsoft']
  }
  return ['google']
}

/** True when this method is a federated provider and not the email link. */
export function isFederated(method: SignInMethod): method is FederatedMethod {
  return FEDERATED.includes(method as FederatedMethod)
}
