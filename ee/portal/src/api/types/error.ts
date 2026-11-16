// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The API's one error shape (portal plan P1.4; F20; P1.0's uniform refusal).
// The shape is `ee/portal/schemas/error.schema.json` and its sample is
// `ee/portal/schemas/fixtures/error.refusal.json`, held to each other by
// `test/types.test.ts`.
//
// **Every failure the API answers is this, and nothing else**: one code a
// screen can switch on and one sentence a person can read. `src/api/client.ts`
// decodes it for every request, so no screen ever sees a status number, an
// HTML error page or a stack trace -- a body that is not this shape is a
// transport failure and is answered as one, rather than being shown to a
// reader as if the API had said it.

/** One refusal: a code for the app, and a sentence for the person. */
export interface Fault {
  code: string
  message: string
}

/** The body of every non-success answer. */
export interface ErrorRefusal {
  error: Fault
}

/**
 * Everything `error.schema.json` describes. It is named apart from the
 * `ApiError` class in `src/api/client.ts` on purpose: the class is what a
 * caller catches, and this is what the wire carries.
 */
export type ApiRefusal = ErrorRefusal
