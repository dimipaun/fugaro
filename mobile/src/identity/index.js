// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The identity module's one door (task M1; D2790, D2793, D2785).
//
// Everything a screen needs from this directory, re-exported here and nowhere
// else, so that a screen imports `src/identity/index.js` and gets the store --
// not `providers.js`'s request builders or parsers, which take a token and a
// URL and are the store's business (M1's own test asserts they are not here).
//
// **What is deliberately NOT exported.** `signInWithIdp`, `refreshTokens`,
// `buildSignInWithIdp`, `buildRefresh`, `parseSignIn` and `parseRefresh`: a
// screen that could build a request could hand a provider token to a URL of its
// own, which is the one egress the boundary scan allows a single file. The
// store opens it. `IDENTITIES_KEY` is not here either: how the records are
// written down is the store's storage detail, and a caller that could name the
// document is a caller one step from reading it as settings.
//
// The relay question is here because a screen asks it before it offers a
// pairing, and because an identity's own flag (`list()`) is derived from it:
// one spelling of the rule (D2785), in `providers.js`, beside the domain.

export {
  APPLE_RELAY_DOMAIN,
  PROVIDER_CODES,
  PROVIDERS,
  isProvider,
  isRelayAddress,
} from './providers.js';

export { IDENTITY_CODES, IdentityStore } from './store.js';
