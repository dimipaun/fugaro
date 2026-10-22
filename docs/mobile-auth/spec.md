# Fugaro Mobile App Auth Spec

Oct 20, 2026 · @Dimi

The app asks people to sign in at first launch using an account already on the phone, then they scan a project QR code to connect, and the allowlist checks their verified email.

## Flow

Sign-in comes first because people expect an app to ask for it at launch. Pairing comes second.

1. First launch: the app prompts sign-in with an account already on the phone. No Fugaro username or password.
2. The app keeps the provider credential (Google or Microsoft ID token, refreshable) on the device.
3. The user scans a project QR code. It carries the project's Firebase config and pairing info only, not an identity.
4. The app exchanges the stored credential for a Firebase Auth session in that project.
5. The project's allowlist checks the verified email (exact address or domain). Not allowed means a clear "ask your admin" screen.
6. Later projects repeat steps 3 to 5 with no new sign-in.

## Providers

Three providers, offered in this order.

| Order | Provider | How it works | Friction |
| --- | --- | --- | --- |
| 1 | Google | Android: Credential Manager account picker on device accounts. iOS: Google Sign-In SDK. | One tap on Android; a consent sheet on iOS |
| 2 | Microsoft | Firebase Microsoft provider (Entra ID / Microsoft 365), browser OAuth | Short browser sign-in, once per device |
| 3 | Email link | Firebase passwordless email link | Type an email, tap the link in the inbox |

Sign in with Apple is also supported on iOS, since App Store rules generally require it when Google or Microsoft login is offered. Apple's ID token is short-lived and can't be silently refreshed on the client, so pairing a new project shows a quick Face ID prompt to sign in with Apple again.

## Per-project Firebase sign-in

Each Fugaro project is its own Firebase project, so one device sign-in has to become a separate Firebase Auth session in each paired project.

- Google and Microsoft: the app passes the stored provider ID token to `signInWithCredential` on a Firebase app instance initialised from the QR config. Each project must list the Fugaro app's OAuth client ID as an allowed client, which project setup handles once.
- Email link (passwordless login link): bound to one Firebase project, so it runs per project. At launch the user picks "Use email" and enters an address, which is not yet verified. On scan, that project sends the sign-in link, and the app saves the pending pairing and shows "Check your email". Tapping the link deep-links into the app and completes pairing, with no rescan. The link must be opened on the same phone. Later option: the security service mints custom tokens so one email login covers all projects.
- The app holds one Firebase app instance per paired project, each with its own session and RTDB access under that project's rules.

**Token refresh.** Once a project is paired, its Firebase session persists and refreshes on its own, so the provider token is only needed when pairing a new project. At pairing, the app silently refreshes the Google or Microsoft token. If that fails, it shows one re-sign-in prompt and then continues pairing. Revoking access is separate: removing someone from the allowlist takes effect immediately through the security rules.

## Allowlist and roles

The allowlist lives in the project's RTDB. Security rules check the token's verified email against it. Roles are in the data model from day one, even if v1 only checks admin and everyone else.

| Role | Can do |
| --- | --- |
| Viewer | Hear summaries, see job status and spending |
| Operator | Viewer, plus talk to agents, start, stop and steer jobs |
| Admin | Operator, plus manage the allowlist, roles and caps |

- Entries are an exact email or a whole domain, each with a role. An exact email overrides its domain's role.
- Managed from the CLI (for example `fugaro allow add <email|domain> --role operator`), later from the console.
- The project creator becomes the first admin.
- Allowlist changes are saved to the project Git repo along with other project state.

## Open questions

- [x] Token refresh: decided, silent refresh at pairing with a re-sign-in fallback (see above).
- [x] Email link: decided, per-project login link at pairing for v1 (see above).
- [x] Sign in with Apple: decided, supported on iOS (see Providers).
- [x] Allowlist format: decided, exact emails and domains, each with a role.
