// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's crash-reporting DSN, as one constant in one file (desktop-console
// plan Task 2.1b; the owner's ruling of 2026-10-31, which supersedes the
// build-time design the earlier rounds of this slice carried).
//
// **A Sentry DSN is not a credential.** It is a public client key, and it is
// public by design: all it can do is *send* events to the project it names, and
// the value therefore ships in every copy of the console that reports and is
// visible to anyone who opens the bundle. What it cannot do is read anything --
// an event, an issue, an organisation, a setting -- which needs a token that is
// not in this repository and never will be. So it belongs in the source, where a
// reader can see which project the console reports to and check it, rather than
// in a build secret: the owner's words are that "we don't need this in a secret,
// we can build it inside the app, it's easy to extract anyway", and a value
// anybody can extract from a shipped app is a value a repository can hold.
//
// The one thing that does not follow from that is anywhere else: the DSN appears
// in exactly this file, and `test/unit/secret-hygiene.test.ts` holds `console/src`
// to that -- one DSN-shaped literal, in this file, and a second one anywhere
// under `src/` is a source that has grown its own endpoint.
//
// What the DSN buys is a *destination*, not a permission: nothing is sent because
// this file exists. The switch is the console's own, per-user and local to it, it
// is off by default and it is applied at the next launch (D734), and the slice
// that reads it is the next one -- so a console built from this tree reports
// nothing.

/** CONSOLE_CRASH_DSN is the Sentry project the console reports to: org `fugaro`,
 * the EU region, project `fugaro-console` (the owner's ruling of 2026-10-31). The
 * region is in the host, and it is the one the project was created in rather than
 * a preference -- an event sent to the wrong region is an event nobody reads, so
 * `test/unit/secret-hygiene.test.ts` asserts the `.ingest.de.sentry.io` suffix
 * rather than trusting the shape. */
export const CONSOLE_CRASH_DSN =
  'https://a8c50d042d8d93a4379c473ea3b26113@o4512350264557568.ingest.de.sentry.io/4512350313840720'
