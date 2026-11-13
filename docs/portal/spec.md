# Fugaro Portal Spec

The Fugaro portal is the signed-in web app behind the marketing site: accounts, organizations, workspaces, keys, licence refresh, sales records and reports for customers, resellers and the Fugaro operations team.

## Architecture

- **Frontend**: React single-page app in TypeScript, built with Vite, compiled to static assets, deployed to Firebase Hosting on its own subdomain (e.g. app.fugaro.dev). Astro stays for the marketing site; the two share design tokens and components (e.g. Tailwind) so they look like one product. Responsive: works well on desktop and mobile.
- **Auth**: Firebase Auth with Google, Apple, Microsoft and passwordless email link. Enterprise SSO (SAML/OIDC via Identity Platform) deferred. Members of operations and reseller organizations must sign in with Google or Microsoft SSO; email link is not allowed for them. Enforced after sign-in: the backend and Firestore rules check the ID token's `sign_in_provider` on every request and deny operations/reseller access unless it is Google or Microsoft. In the UI those organizations stay selectable in the switcher; selecting one while signed in by email opens a friendly dialog explaining that SSO is required, with Google and Microsoft sign-in buttons. Invitations to operations and reseller organizations can only be accepted via SSO. Later: TOTP second factor via Identity Platform for email sign-in (check compatibility with email-link sign-in).
- **Backend**: Cloud Run service, scale to zero, in TypeScript on Node with a lightweight framework (Hono or Fastify); fast cold starts, and types shared with the frontend. Java considered and rejected as overkill with slow cold starts.
- **Database**: Firestore, in its own database, separate from the RTDB used elsewhere in Fugaro.
- **Analytics**: Google Analytics via Firebase.
- **Error monitoring**: Sentry integrated in both the frontend and the backend.
- **Projects**: production in fugaro-prod; staging in a separate project (e.g. fugaro-staging).

## Tenancy model

- **Organization** is the tenant. Types: operations (Fugaro itself), customer, reseller.
- A person has one identity and can belong to many organizations (Play Console model), with an organization switcher; they land in the last one used.
- Membership roles per organization: owner, admin, member. Roles live in Firestore membership records, not auth custom claims.
- Invitations by email link.
- Members of the operations organization get admin views across all organizations; every operations action is audit-logged.
- Resellers are organizations linked to the customer organizations they sold to.

## Workspaces, keys and licences

- An organization has one or more workspaces; one key per workspace.
- A key carries a seat count (e.g. 25). No assignment of people to seats.
- Seat enforcement is local only: read-only viewers are free; users who can act count as seats; beyond the limit, extra users are forced read-only. Nothing about usage is reported to Fugaro.
- The key identifies the licence. The installation periodically exchanges it for a fresh signed licence; the refresh policy (e.g. weekly) is embedded in the key.
- Backend exposes a licence-refresh endpoint.
- New CLI command `fugaro license refresh` forces an immediate refetch; the portal shows it after any purchase or seat change.
- Downloadable licence file for air-gapped installations.
- Revocation takes effect at next refresh (up to one refresh interval of grace); accepted.
- Licences are signed with a key held in Cloud KMS; the backend can request signatures but never export the key.

## Free seat

- Signing up automatically mints a permanent one-seat key, one per organization.
- Intended for prototyping; production use requires a paid licence. Enforced by the licence terms, not technically.

## Customer experience (self-serve)

- Account first: the user signs in before checkout.
- Paddle checkout overlay opens tied to their organization (organization ID passed as custom data), so webhooks map reliably to customers.
- After payment the user lands on the workspace page with the key ready to copy. Keys are only shown in the portal, so customers can always return to recover them.
- Customer views: workspaces with key, seat count, renewal date; buy more seats; add a workspace; members and invitations; organization settings.
- Invoices, payment methods and cancellation link out to Paddle's customer portal; the backend mirrors state from webhooks.
- Enterprise deals are sold both directly and through resellers.

## Operations

- **Keys**: mint with any seat count, expiry and refresh policy; extend, change seats, revoke.
- **Customers**: look up any organization, its workspaces, keys, purchase history and members.
- **Sales**: every Paddle webhook stored as received, with a readable sales ledger derived from it.
- **Reports**: monthly per customer, by channel (direct or reseller), and amounts owed to resellers.
- **Audit log** of all operations actions.
- Affiliates are out of scope; they use a separate hosted affiliate site.

## Resellers

- See the customers they sold to, those customers' keys and renewal dates, and monthly statements.
- Can mint keys for their customers. Each minted key or seat change is charged at their wholesale price, prorated by days, and invoiced monthly by the portal.
- Operations sets a credit limit per reseller; minting is blocked beyond it. No prepaid credit.

## Paddle webhooks

- Verify Paddle signatures on every request.
- Store each event exactly as received.
- Process idempotently so retries never double-count.

## Email

- Firebase Auth sends sign-in links.
- Resend for invitations, renewal reminders and reseller statements, starting on the free tier, behind a small email interface so the provider can be swapped (e.g. to Postmark).

## Staging

- Separate Firebase project wired to Paddle sandbox and Resend test mode.
- Its own KMS signing key, so staging licences never validate in production.

## Legal and privacy

- Terms of service accepted at sign-up.
- Licence terms, including the production-use rule, accepted at checkout.
- Self-serve account deletion for GDPR, retaining only sales records legally required.
