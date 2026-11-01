# The EAS project, the signing keys and the store entries — the owner's runbook

**Status: written 2026-10-18, refreshed 2026-10-22 against the tree. The page
is the instructions, not a record.** It is the runbook for **Task P4.4** of
[implementation-plan.md](implementation-plan.md) — *"The EAS project, the
signing keys and the store entries — owner-run"* — and for the check that
verifies it, **Check 89** in the live register
([../gcp-live-checklist.md](../gcp-live-checklist.md)).

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-18; refreshed
2026-10-22.

**What has happened since the first writing, and what has not.** The repository
side of P4.4 has landed: `mobile/eas.json`, `mobile/app.config.js` and their
tests are in the tree (**PR #675**, **#738**, **#745**), the submit profile
carries the App Store Connect app id the owner's first submission produced
(**#759**), the export-compliance answer is declared in the build's config, and
the app's release workflow, staged by **#777**, was **copied in on 2026-10-23**
on the owner's approval and runs as `.github/workflows/mobile-release.yml`. The owner's own record
in [mobile/README.md](../../mobile/README.md) — the one submission of
**2026-10-20** that created the App Store Connect record, and the
export-compliance declaration of the same day — is the whole of what this
repository records about the run itself.
**What has not happened is Check 89**: its row in the register stays **NOT
RUN**, no `FACT` line has been pasted back, and the plan's open item 5 — *"Checks
80–89 are owner-run and none has been run"* — stands.

**The rules of this run, in one place, because each is a step an agent could
otherwise take:**

- **No agent performs any step of this runbook.** Every step below is the
  owner's, **at your own terminal**, with your own Expo, Apple and Google
  accounts, your own signing keys and your own EAS project.
- **No agent runs `eas build` or `eas submit`**, and **no agent touches a signing
  key or a store credential**: **no signing key or store credential ever enters
  an agent's environment** (the plan's **Global Constraint 9**, decision
  **D120**).
- **No agent has run any of it.** No agent has `eas` on a `PATH`, no agent has
  logged into an Expo account, and no agent has opened the Apple or Google
  consoles. The `mobile/` files above were committed as code and were exercised
  only as code (their own tests, and Expo's loader on a shell's **test** values —
  [mobile/README.md](../../mobile/README.md), "What is not verified here").

**Where this page disagrees with a file on the tree, the file wins.** The five
places the refresh found a mismatch are recorded under [Recorded tensions, and
how each was settled](#14-recorded-tensions-and-how-each-was-settled) rather
than smoothed over here: **three are settled and this page follows the ruling**
(Tensions 1–3); **Tension 4 is a numbering mismatch, still open on the
register's own text**; and **Tension 5 is recorded without being settled** —
the plan's D120 sentence says CI at most builds a test artifact and never
submits, and the owner's own staged workflow does more than that, by the
owner's later ruling of 2026-10-19/20.

**The plan already says where this work sits, and this page does not move it.**
P4.4's own line reads *"**Files.** `mobile/eas.json` … and `mobile/app.config.js`
… **The EAS project itself, the signing keys, the store entries and the two
identity values are the owner's**… **Owner-run.** **Depends on.** P3.1, P4.2a
(check 89's text must be in the register before it is run). **Verification.**
Check 89: a signed build reaches the store's internal track."* This document is
the runbook for that verification; it is prose, and the only repository files
that participate in the run are the two the plan names and the staged workflow
of §10 — all of them read, none of them written by the run.

---

## 1. What this runbook is, and what it is not

| | |
|---|---|
| **It is** | the setup the owner performs **once** so that Check 89 can run at all: the Expo/EAS project, the Apple and Google prerequisites, the signing credentials and where they live, the two repository settings they need, and the rollback. |
| **It is not** | the check. **This runbook does not restate Check 89. The register is its authority.** Check 89's steps, its guard block, its `FACT` lines, its pass and fail conditions and its restore step are written into [../gcp-live-checklist.md](../gcp-live-checklist.md) by **task P4.2a**, merged into `main` as **PR #580** on **2026-10-19** (that pull request's own branch was `fugaro/20261018-130217-49c7`), so the section is in the register you read today; **the register's text, not this one, is what you follow** when the two describe the same thing. Where they differ, the register wins and this page has a defect to fix. |
| **It is not** | a workflow. **This runbook describes a workflow and writes none.** No file is created under `.github/workflows/` by this task or by any agent (Global Constraint 8). The app's release workflow **is** written (**#777**) and, since the owner's approval of 2026-10-23, **applied** — `.github/workflows/mobile-release.yml`, one job per platform, behind the protected environment of §9 — and §10 is this page's account of it, read off the file rather than proposed. **No agent copies a workflow file into `.github/workflows/` on its own**: that copy-in is the owner's decision, per [the copy procedure](../pending-workflows/README.md). |
| **It is not** | a claim that anything works. The register's row 89 stays **NOT RUN**, no `FACT` line has been pasted back, and the plan's open item 5 stands: *"Checks 80–89 are owner-run and none has been run."* What the owner's own record shows — the App Store Connect record and the first submission of 2026-10-20 — is setup, not this check, and it proves no store track, no install and no version. |

**Three things this runbook does not own, and must not be read as owning.**
`mobile/` and its `mobile/app.json` are **P3.1**'s (the plan's Phase 3, already
merged; see [the app's README](../../mobile/README.md)). The register's text is
**P4.2a**'s. The rules the app reads under are **P2.1**/**P2.2**'s. This page
says what the owner does at their terminal to make Check 89 runnable; it decides
no application behaviour and no database grant.

---

## 2. What is verified against this repository, and what is not

The distinction matters more here than anywhere else in this work, because
almost every line of the run is about **Expo's, Apple's and Google's** consoles
and CLIs — three products this repository cannot check, and three sets of
documentation that move.

**Verified here, against files on this tree (or against the plan and the
register):**

- the task's scope, files, verification and reviewer tier, from
  [implementation-plan.md](implementation-plan.md) (Task P4.4, and Check 89's
  summary-table row);
- the constraint that makes every step the owner's: **Global Constraint 9**,
  **D120** ("owner-run EAS and store path, always") and **D125** (the app is its
  own artifact, outside the CLI's release gate);
- the app's placement and its version rule: **C2** (the app lives at `mobile/`,
  outside the Go module) and **C6**'s question, answered the other way by the
  owner's ruling of 2026-10-22: the app is versioned with **its own** tag
  (`mobile-vX.Y.Z`), not the CLI's, and it is released on the stores' cadence
  ([../release.md](../release.md), "Mobile releases");
- the check's own guard block, its file names and its stop conditions, from the
  register's Check 89 text, including `mobile/app.json`, `mobile/eas.json`,
  `mobile/app.config.js`, `node`, `git describe --tags --exact-match`,
  `eas whoami`, `eas project:info`, `distribution: store`,
  `submit.internal.android.track` and the two identity variables the guard
  exports;
- **P4.4's own two files, read off the tree rather than off this page**:
  `mobile/eas.json` (the three profiles, `cli.requireCommit`,
  `cli.appVersionSource`, the submit track and the App Store Connect app id the
  submit profile carries) and `mobile/app.config.js` (the two variable names, the
  two patterns, the export-compliance declaration and what it refuses), both
  merged as **PR #675** and extended by **#738**, **#745** and **#759** — §8 is
  this page's account of them, and where they differ the files win;
- **the app's release workflow, read off the file rather than off a
  plan**: `.github/workflows/mobile-release.yml` (**#777**, applied 2026-10-23, revised
  2026-10-21 after that pull request's security review) — its trigger, its two
  jobs, the protected environment each job declares, the one secret it reads and
  the variables that turn a platform off. §10 is this page's account of it, and
  where they differ the file wins;
- the decision that the internal track and no public release is what is being
  proved: **C8**;
- what the app may hold, which is **not** a store credential: **D111** (exactly
  two items per account, in platform secure storage);
- the repository-side precedent, read off the tree's own workflow files rather
  than off a plan: **`.github/workflows/website-promote.yml`** carries
  `environment: website-live` in its `promote` job, and
  **`.github/workflows/console-release.yml`** carries `environment:
  console-release` in its `release` job — the two protected environments this
  repository **installs**, both declared on a **job**, not in a comment
  (`.github/workflows/release.yml` signs the CLI **keyless**, with no
  environment at all). The console's signing pipeline is also staged twice over
  (`docs/pending-workflows/console-release.yml` and `console-release-linux.yml`,
  the second superseding the first) and superseded once more by the change that
  gives the console its own `console-vX.Y.Z` release stream
  (`console-release-tag.yml`, applied on 2026-10-23 over both copies of
  `console-release.yml` and then removed), and each declares the same `environment: console-release` in its own
  `release` job. Every workflow this page cites, it
  cites by **content** — the `environment:` key and the job that declares it —
  and never by line number, so the citation cannot rot when a line above it
  grows. §9 names them, says what each one gates, and says which one
  `mobile-release` is.

**Not verified here, and to be checked at run time against the vendor's current
documentation** (each step below names the page): every exact console **label
and menu path**, every CLI **flag and subcommand name**, the current form of
Expo's organisation and project model, Apple's enrolment and TestFlight
vocabulary, and Google's Play Console and app-signing vocabulary. **This page
deliberately does not state a UI label as certain.** Nothing below is a
transcript of a console anybody in this repository has seen.

---

## 3. Before anything: the terminal, the tag and the checkout

**Check 89 owns this step and this runbook does not repeat it.** The register's
guard block **is** the precondition: run it, from the checkout you intend to
build from, and re-run it **before every step** of the check. It stops unless
`eas` is on your `PATH`, unless **`node`** is on your `PATH` — the guard reads
the store identity by running `mobile/app.config.js` through it, and the
register says why that test is there: a missing interpreter is *"a Python
FileNotFoundError -- a traceback, not the sentence this guard promises"* —
unless it is inside a git checkout, unless `mobile/app.json`,
`mobile/eas.json` **and `mobile/app.config.js`** are all present, unless `HEAD`
is exactly a tag (`git describe --tags --exact-match`), unless the working tree
is clean — because a store build records the commit it was made from, and a
build from a dirty tree is not the artifact the release is — and unless the
**store identity resolves**: the guard reads `FUGARO_IOS_BUNDLE_ID` and
`FUGARO_ANDROID_PACKAGE` out of your shell, resolves `mobile/app.config.js`
with them, and stops with that file's own reason when they are unset or
malformed — and if the resolved config still carries no `ios.bundleIdentifier`
and no `android.package`, that is the guard's **store identity** stop: there is
nothing to submit a build under, and it says so in its own words rather than
failing later.

**The identity is not in `mobile/app.json`, and never will be.** P3.1's
boundary test refuses a release identity in the committed config, and D125
makes the identity the owner's; it reaches a build from the two environment
variables through `mobile/app.config.js`, the dynamic config the build is
actually made from. §8 says where the two values are set, §14's Tension 1
records how that was settled, and **§3's third bullet below is the one thing
this page adds to the guard**: exporting them before you run it.

What this page adds is only the shape of the shell's prerequisites, so the guard
can pass its own first checks:

- **a Node and npm you control** — `mobile/package.json` pins `engines` to
  `node >=22 <23` and `npm >=10`, and the same manifest's `test` script is the
  whole of the app's verified surface today (see
  [mobile/README.md](../../mobile/README.md)). The guard needs `node` itself,
  for the reason above: the `eas-cli` you install with npm brings one;
- **the EAS CLI**, installed by you: `npm i -g eas-cli` (or `npx eas-cli@latest`
  if you prefer not to install it globally), then `eas login` **as yourself**.
  `eas whoami` must print **your** Expo account — the guard's own first line
  (`command -v eas`, which stops when the CLI is not on your `PATH`) and Check
  89's step 1 (`eas whoami`, which reads the account) both start there;
- **a checkout at a tag**, fetched with `git fetch --tags`, with nothing
  uncommitted;
- **the two identities, exported in the shell you run the guard from** —
  `FUGARO_IOS_BUNDLE_ID` and `FUGARO_ANDROID_PACKAGE`, one public reverse-DNS
  name each (§8). `eas-cli` resolves `mobile/app.config.js` **locally**, before
  it uploads anything, and the guard block reads the same two variables, so
  exporting them once is what lets both pass. §8 has the commands; the values
  are public identifiers and are **never pasted back** (§11).

**FACT for §3 (owner):** the tag `git describe --tags --exact-match` printed, the
output of `eas whoami`, and **that both identity variables were exported** (the
names, never the values, and never a credential). The tag comes back in §11 with
the build's `gitCommitHash`.

---

## 4. The Expo account, the organisation and the EAS project

**Nothing in this section is a secret.** The Expo account name, the
organisation, the project's **slug** and its **id** are public identifiers: they
reach the built app and EAS's own build pages. The one secret this system has is
the access token of §7, and **CI at most** would hold that, never this step.

1. **The account.** An Expo account of your own (`https://expo.dev/`). If the
   project should live under an **organisation** rather than under your personal
   account — which is what lets a build later be handed to someone else, or to a
   robot account, without moving the project — create the organisation from the
   account's own settings on the Expo website. **Check the current label there
   and at `https://docs.expo.dev/`**: EAS has changed how organisations are
   created and who may own a project more than once, and this page does not
   pretend to know today's wording.
2. **The project, which already exists.** `mobile/app.json` carries
   `expo.extra.eas.projectId` — `70e67d4a-77d3-4110-9db6-e600266db345`, a
   **public identifier** that reaches the built app — so a project is already
   bound to this checkout and `eas project:init` is **not** a step you need.
   Confirm it rather than creating anything:

   ```sh
   cd mobile
   eas whoami                 # your account, per §3
   eas project:info           # the slug and id this checkout resolves to
   ```

   `eas project:info` prints the slug and the id; **Check 89's step 1 names both
   commands** and reads exactly these two identifiers. If the id it prints is not
   the one in `app.json`, stop: two projects under one account is how one ends up
   with two build histories and one of them stale, and the fix is to decide which
   project the app is, here, before anything is built. If your CLI of the day
   spells the subcommand differently, that is a rename, not a different step:
   **check `https://docs.expo.dev/` for the current name and use it**, and note
   the rename in your `FACT` line.
3. **The `slug`.** `mobile/app.json` already carries `expo.slug: "fugaro"`. The
   slug and the project id are what EAS resolves a build profile from, and a
   project created with a different slug under the same account is a **second
   project**, which is how one ends up with two build histories and one of them
   stale. Register the project the config names — the one the id above points
   at.

**FACT for §4 (owner):** your Expo account; the project's **slug** and **id**;
whether it sits under your personal account or an organisation; whether
`eas project:init` or another spelling of the subcommand was what created it.
**No token, and nothing from the account's security page, is pasted.**

---

## 5. Apple: the developer account, the app record and the internal testers

**No step below needs an agent, and none of it is in this repository.** The
Apple Developer Program membership, the certificates, the identifiers and the
App Store Connect account are yours, held under your Apple ID and your team.

1. **Membership.** An **Apple Developer Program** membership, as an individual or
   as an organisation — the organisation path asks for more (a legal entity and
   a D-U-N-S number, at the time of writing), so it is a decision made before
   the enrolment rather than during it. **Enrolment and its current requirements:
   `https://developer.apple.com/programs/enroll/`.**
2. **The bundle identifier.** The app's iOS identity — a reverse-DNS string, for
   example something under your own domain. **Two places must agree on it**, and
   neither of them is a committed file:
   - **the EAS project's `production` environment**, as the value of
     `FUGARO_IOS_BUNDLE_ID`, exported in your shell for a local run — the
     identity reaches the build through `mobile/app.config.js`, and Check 89's
     guard prints what that file resolved, not a field of `app.json` (§8, §14's
     Tension 1);
   - **the Apple developer account**, where the identifier is registered under
     **Certificates, Identifiers & Profiles** → **Identifiers**
     (`https://developer.apple.com/account`). The page's menu labels have moved
     between releases; **the page is the authority, not this sentence.**
3. **The app record and the internal testers.** In **App Store Connect**
   (`https://appstoreconnect.apple.com`): your **Apps** list → the app record
   for the bundle identifier above → **TestFlight** → **Internal Testing**, where
   you add the internal tester group. **The record exists**: EAS created it when
   the owner first submitted the app, on **2026-10-20**, and the id it printed —
   `ASC App ID: 6824584935` — is committed in the submit profile
   (`submit.internal.ios.ascAppId`, §8) precisely so EAS is never asked for it
   again. Re-read the record's own state there rather than from this page. The
   help root is `https://developer.apple.com/help/app-store-connect/`; **read it
   for the current labels** for the internal group and for how a build moves from
   *Processing* to *Ready to test* — Check 89's step 3 records exactly that
   transition, and step 4 installs from the internal track.
4. **The App Store Connect API key** (or the Apple ID and app-specific password
   alternative) — the credential `eas submit` uses on your behalf. Where it is
   created and where it lives: §7. **An Apple ID password, an app-specific
   password, the API key's `.p8` file, its key id and its issuer id are never
   pasted, never committed, and never handed to an agent.**

**FACT for §5 (owner):** that the membership is active and which kind (individual
or organisation); the **bundle identifier** (it is a public identifier, and it
reaches the built app); that the app record exists and that an internal testing
group exists; the build's state in App Store Connect at the moment Check 89's
step 3 reads it. **Not the key's contents, not the key id, not a password.**

---

## 6. Google: the Play account, the app and the internal-testing track

**The Android half, and the same rule: all of it is yours.**

1. **The Play Console account.** A Google Play developer account
   (`https://play.google.com/console/signup`), which carries a one-time
   registration fee and an identity check at the time of writing. **Check
   `https://support.google.com/googleplay/android-developer/` for the current
   requirements before you start**, because they change and they are not a
   detail this page can settle.
2. **The package name.** The app's Android identity — a reverse-DNS string, and
   **it is chosen when the app is created and cannot be changed afterwards**.
   Like the bundle identifier it must agree in two places, and again neither is a
   committed file:
   - **the EAS project's `production` environment**, as the value of
     `FUGARO_ANDROID_PACKAGE` (§8; the ruling of 2026-10-19 is **one name for
     both stores**, so this value is the same string as the bundle identifier
     above);
   - **the Play Console app record**, at creation (`https://play.google.com/console`).
   The identity must be a string **you own**: a package name you do not control is
   a package name you cannot publish later.
3. **The internal-testing track.** In the Play Console: your app →
   **Test and release** → **Testing** → **Internal testing** → create a release
   and add your testers, then use the internal-testing **opt-in link** to install
   from it. This is the track Check 89's step 3 requires and its
   `submit.internal.android.track` guard value pins — and the whole content of
   **C8** is that one value, because **an internal testing release is not a
   public release**, and it can be halted (§12) in a way a production release
   cannot.
   **Play's own rules for *production* access** (closed testing before a first
   production release, and how long it must run) are real, they change, and
   **this runbook does not depend on them** — C8 stops at internal testing. If
   you ever want production, check `https://support.google.com/googleplay/android-developer/`
   first and take it as its own task.
4. **Play app signing, and the upload key.** Google holds the **app signing key**
   for an app distributed through Play, and it is Google's, not yours. What you
   hold is the **upload key**, which signs the AAB you submit — Check 89's step 2
   records that the Android artifact *is* an AAB signed with the upload key, and
   `eas credentials` shows which keystore was used. Where the upload key lives:
   §7.
5. **The Play service account** — the credential `eas submit` uses on the
   Android side. It is created in **Google Cloud** as a service account, then
   linked in the Play Console under the console's own **API access** page, with
   the release permissions EAS's documentation asks for, and its **JSON key** is
   downloaded once. **Its contents are never read, printed, pasted or committed**
   (§7), and the file Check 89's step 5 looks for by name is
   `google-play-service-account.json` — because that is the name someone would
   otherwise add "so CI can submit", which is the finding that step exists to
   produce.

**FACT for §6 (owner):** that the developer account is active; the **package
name** (public, like the bundle identifier); that the internal-testing track and
the tester list exist; the release's state on the internal track as the console
shows it when Check 89's step 3 reads it. **Not the service-account JSON, not its
key, not the upload keystore.**

---

## 7. Where each signing credential lives

**This is the section Global Constraint 9 is about.** Every credential below is
either **EAS-managed** (EAS's servers hold it, under your Expo account, and your
build machine never sees the material) or **owner-held** (it is on your machine
and nowhere else). **None of them is in this repository, none is in a CI
variable an agent can read, and none is in a run environment** — and that is not
a stylistic preference:

- **A key in this repository is a key in every clone, every backup, every log and
  every paste.** The repository has a rule against credential-shaped literals in
  source (see §15's files), and Check 89's step 5 reads the tracked file list
  under `mobile/` for exactly the names a key file would have.
- **A key in CI is a key any later workflow change can read.** This repository
  **does** run one signing job that reads signing secrets, and it is worth
  copying its shape and its refusals even though the mobile path needs neither:
  `.github/workflows/console-release.yml` — the console's signing,
  notarisation and stapling job — reaches all six Apple secrets **only** through
  `environment: console-release` in its `release` job, only as `${{ secrets.NAME }}` in the
  `env:` of the single step that signs, never at workflow or job level, never
  on a command line, never in an action's `with:`, and with the key material
  removed on that step's own exit. The rest of the repository's CI holds no
  signing credential at all: `.github/workflows/release.yml` signs the CLI's own
  artifacts **keyless** (cosign asks GitHub's OIDC issuer for a short-lived
  certificate, so there is no signing secret anywhere in that job; the app is
  outside that gate anyway, D125), and `.github/workflows/website-promote.yml`'s
  `website-live` holds repository **variables** — a project id, a provider name,
  a service-account address — and no secret, because the site's deploy
  authenticates by Workload Identity Federation. **No workflow in this
  repository has ever held a store credential**: Apple's six are the console's
  own signing material, not a store credential. The app's staged release
  workflow (§10) is the closest anything here comes, and §14's Tension 5 records
  it: it holds **one** secret, `EXPO_TOKEN`, and because EAS holds the app's
  distribution credentials on your account, that token is what would let the
  approved job build and **submit**. It is not a store credential and it is not a
  signing key — it is a token, held as an environment secret behind your own
  approval click — but it is a credential CI may read, and the reason §10 states
  its placement rather than assuming it.
- **A key in an agent's environment is the thing D120 was answered against.**
  A Fugaro run's container is not a safe place for a store credential: the
  agent's egress is not restricted and it can read the token from the runner's
  `/proc` (see [SECURITY.md](../../SECURITY.md), "Trust model and known limits").
  That sentence is about Fugaro's own token; the reason it generalises to yours
  is that the same container, and the same reach, would hold it.

| Credential | Where it lives | Who may read it | Where it must never be |
|---|---|---|---|
| **iOS distribution certificate** | **EAS-managed** (EAS generates and stores it for your account) or **owner-held** (your keychain, via Xcode or `security`) | EAS's build servers, or your machine | **never in this repository**, never in CI, never in an agent's environment |
| **iOS provisioning profile** | **EAS-managed**, or owner-held (a `.mobileprovision` on your machine) | as above | as above — a profile is a credential-shaped file, and Check 89's step 5 greps the tracked list for `.mobileprovision` |
| **Android upload key (keystore)** | **EAS-managed** (EAS generates and stores it) or owner-held (a `.jks`/`.keystore` on your machine) | EAS's build servers, or your machine | **never in this repository**, never in CI, never in an agent's environment |
| **Android app signing key** | **Play app signing** — **Google's**, not yours, and not something you can export | Google | nowhere: there is no copy of it to place. This is the one credential whose location is not a choice |
| **App Store Connect API key** (a `.p8` file, a key id and an issuer id) | **owner-held**, on your machine, or in your password manager as the file | you, and `eas submit` reading it | **never in this repository**, **never in CI** for this check, never pasted |
| **Play service account** (the JSON key) | **owner-held**, on your machine — and **not at a path this repository names**: `mobile/eas.json` carries **no** `serviceAccountKeyPath` and no other credential path, and its own test (`mobile/src/eas-config.test.js`) refuses a key whose name suggests one. You give EAS the file when it asks (the first `eas submit` after P4.4 does), or let the EAS project hold it as a credential | you, and `eas submit`, which reads the file wherever you keep it | **never in this repository** (the file Check 89's step 5 looks for is `google-play-service-account.json`), never in CI. Its **contents** are never read by Check 89 |
| **Expo access token** (the one name CI may hold) | your Expo account's access tokens, on `https://expo.dev/`, under the account's own settings; stored as the **environment secret `EXPO_TOKEN`** **on the `mobile-release` environment** (§9), which is where the staged `mobile-release.yml` reads it — **never a repository secret** | you, and the job that names `environment: mobile-release` and no other job in the repository, after your approval | **never in this repository**, never on a build machine that does not need it, never as a repository secret |

**The secret *names* the staged workflow would read, and nothing else.** This
page names them and holds no value of any of them; a value here would be the
defect. The names, and whether CI may hold each:

| Name | What it is | CI may hold it? |
|---|---|---|
| `EXPO_TOKEN` | an Expo access token that lets a build run under your account | **yes, and only this one — and only as the `mobile-release` environment secret**, never a repository secret (§9): a repository secret is readable by every workflow in the repository, an environment secret only by a job that names the environment. The staged workflow's jobs build **and submit** with it (§10, §14's Tension 5) |
| `ASC_API_KEY_ID` | the App Store Connect API key's **identifier** (not a secret in itself, but half of the pair a key needs) | **never in CI** — the store path is owner-run |
| `PLAY_SERVICE_ACCOUNT_JSON` | the Play service-account JSON's contents | **never in CI**, and never in this repository |
| `APPLE_DISTRIBUTION_CERTIFICATE` | an owner-held iOS distribution certificate (only relevant if you ever chose to sign in CI rather than let EAS hold it) | **never in CI** |
| `ANDROID_UPLOAD_KEYSTORE` | an owner-held upload keystore (the same caveat) | **never in CI** |

**A store credential in CI is the finding Check 89's step 5 exists to produce.**
That step lists the repository's secret **names** — *the names only, never a
value* — and asks you to record any whose name suggests a store or signing key
and to say what it is for. EAS needs **no** store credential in this repository's
CI for Check 89: the one secret CI is allowed is `EXPO_TOKEN`, the signing keys
stay on your EAS project, and nothing here pastes one. P4.3's CI job — **the
shared package's tests, staged as `docs/pending-workflows/shared-tests.yml` for
the owner to copy in and not yet running** — holds no credential either, and the
one signing job this repository does run (`console-release.yml`) reads the
console's own Apple material and no store credential at all.

**FACT for §7 (owner):** whether each credential is EAS-managed or owner-held —
which is the same question Check 89's step 2 records as the build's **credentials
source**; the **names** of any credential files the profiles reference — today
**none**, and a `serviceAccountKeyPath`, an `ascApiKeyPath` or a `keystorePath`
appearing in `mobile/eas.json` is itself the finding, because that file's own
test refuses one; and the secret **names** CI holds. **Never
paste a signing key, a provisioning profile's contents, an App Store Connect API
key, a Play service-account JSON, a password, an Expo token, or any credential
value.**

---

## 8. `mobile/eas.json` and `mobile/app.config.js` — the profiles, the environments and the identities

**Both files are in the tree, and both are P4.4's** (PR **#675**; `app.config.js`
extended by **#745**, `eas.json`'s submit profile by **#759**): `eas.json`
carries the build and submit profiles, and `app.config.js` is the dynamic config
the store identity reaches a build through. The register's guard block refuses
to start without **`mobile/eas.json`** and **`mobile/app.config.js`**, and Check
89's step 1 reads the profile **before** using it, because the profile is where
a mistake becomes a public release rather than a failed build. Those reads are
the check's, and this page does not restate them; what this page settles is
**what the values have to be**:

**The three build profiles, as `mobile/eas.json` has them.** Only the first
reaches a store:

| Profile | What it is | Reaches a store? |
|---|---|---|
| **`internal`** | **the store profile, and the one Check 89 uses**: `distribution: "store"`, an Android `app-bundle` (Play's app-signing path), `autoIncrement: true`, `environment: "production"` | **yes** — this is the profile of steps 2 and 3 |
| **`preview`** | an **internal-distribution** build (`distribution: "internal"`), `environment: "preview"`, and **no `submit` section at all** | no |
| **`development`** | a **dev client** (`developmentClient: true`) on internal distribution, `environment: "development"` | no |

- the build profile the check uses is named **`internal`** — Check 89's step 1
  calls it **the `internal` profile** — and its **`distribution` must be
  `store`**.
  `distribution: "internal"` is an ad-hoc build with no store submission at all:
  it would build successfully and make every later step meaningless. **Check 89's
  guard does not read this value, and cannot stop on it**: the guard block tests
  that the three files exist, that `eas` and `node` are on your `PATH`, that
  `HEAD` is the tag, that the tree is clean and that the store identity
  resolves, and no line of it opens `mobile/eas.json`. The read is **step 1's**,
  which you run by hand (`jq '.build.internal.distribution' eas.json`), and the
  register says exactly why the stop is yours: *"`jq` is a read, so the guard
  cannot stop on a value only the run's owner can judge"*;
- the submit profile's Android track must be **`internal`** — Check 89's step 1
  reads `submit.internal.android.track` and requires `internal`, and **never
  `production`, never anything beta-shaped**: C8's whole content is that one
  value;
- the submit profile's **iOS** entry names **exactly one** thing, and it is the
  app's own record on Apple's side: `submit.internal.ios.ascAppId` is
  **`6824584935`** — the app id EAS printed (`ASC App ID: 6824584935`) when the
  owner's first submission created the record on **2026-10-20** — so EAS is never
  asked for the app again. It is a **public number** on Apple's side of the
  submission: not a credential, not one of the two identities, and the one key
  `mobile/src/eas-config.test.js` admits into that section. Check 89's step 1
  reads it (`jq -r '.submit.internal.ios.ascAppId // "unset"' eas.json`) and
  records it;
- **`autoIncrement: true` is on `internal` alone, and it is not a convenience.**
  `cli.appVersionSource` is `"remote"`, so the **build number**
  (`expo.ios.buildNumber`, `expo.android.versionCode`) lives on the EAS project
  rather than in a file here; App Store Connect rejects a build whose number
  repeats one it already has, and Play rejects a duplicate version code, so a
  store profile with `remote` and no `autoIncrement` builds once and then stops.
  `development` and `preview` reach no store, so incrementing for them would
  spend this release's build numbers on artifacts nobody ever submits. **The
  build number is not the version the stores show and not the version step 4
  compares with the tag** — that one is `expo.version` in `app.json`, the line
  the app's own `mobile-vX.Y.Z` tag names (and `mobile/package.json`'s `version`
  with it: the two are one number, §14, Tension 3);
- **`cli.requireCommit: true`** is what makes a store build record the commit it
  was made from — the `gitCommitHash` step 2 compares with `$SHA`;
- **each profile names the EAS environment it builds with**: `development`,
  `preview`, and `production` for `internal`. Those are **Expo's** environments
  — sets of EAS environment variables on the EAS project — and they are **not**
  GitHub's protected environments of §9. Same word, different system: §9 is
  about a repository setting, this is about the EAS project, and a profile that
  named none would resolve EAS's default environment instead;
- **the store identity is two EAS environment variables, not a file and not a
  credential.** `FUGARO_IOS_BUNDLE_ID` and `FUGARO_ANDROID_PACKAGE` — one public
  reverse-DNS name each, the same name for both stores — are set **once per
  environment** with `eas env:set --visibility plaintext` (they are public
  identifiers: `plaintext` gains nothing by hiding and loses the readability
  that makes a wrong one obvious), and **exported in the shell** for a local
  `eas build`/`eas submit`, because `eas-cli` resolves `mobile/app.config.js`
  locally before it uploads anything. `app.config.js` validates each against
  that store's own rule — Apple allows no underscore, Play allows no hyphen and
  no segment starting with a digit — and its refusal names the variable and the
  fix while **never repeating the value**. `mobile/README.md`, "Building for
  internal testing", is where the commands are written out;
- **no identity value is committed** — not in `app.json` (P3.1's boundary test
  refuses it there), not in `eas.json`, not in `app.config.js`. That is D125's
  "the identity is the owner's" made mechanical, and it is why the guard reads
  the identity out of **your shell** rather than out of a file;
- **the `preview` profile is the one CI could build without a store account**, if
  that is ever wanted: internal distribution, no `submit` section, no credential
  of any kind. It is not the profile Check 89 uses, and it is **not** the one the
  staged release workflow builds (§10 — that one builds and submits `internal`,
  under the protected environment and your approval);
- **the export-compliance answer is declared in the config, not asked again.**
  `mobile/app.config.js` merges
  `ios.infoPlist.ITSAppUsesNonExemptEncryption: false` into every resolution —
  the owner's declaration of **2026-10-20** ("write it, it's OK": the app uses
  only standard/exempt encryption), made after `eas build` printed that it could
  not write that key into a dynamic config. It is a fact about the app and not a
  credential, so it is a constant in the file rather than a third environment
  variable, and **the owner answers Apple's other export questions in App Store
  Connect** ([mobile/README.md](../../mobile/README.md), "The export-compliance
  answer is declared, not asked again");
- **A profile whose submit section names a public track is a stop** — edit it and
  re-run, rather than submitting and trying to halt a release afterwards. That
  sentence is Check 89's own, and it is repeated here only because it is the one
  instruction in the whole run that has no recovery.

**FACT for §8 (owner):** the `internal` profile's `distribution`; its submit
track; whether an `ios` submit entry exists; and the **names** of any credential
files the profiles reference — today **none** (`mobile/eas.json` names no path,
and a path added to it is a finding, not a setting). **Never their contents.**
The two identity values are public, and they are still not pasted back: §11's
list is Check 89's, and it asks for the profile's values, not the identity's.

---

## 9. The repository settings: the protected environment

The environment is the repository-side lock, and it is the owner's to create.
**Three protected environments are declared on this tree today:** two are
**declared on a job of an installed workflow** — not in a comment and not in a
plan — and the third is declared by the release workflow of §10, which was
staged by **#777** and copied in on 2026-10-23. This page's precedent claim is about
the declarations themselves, and it cites each by content rather than by line
number:

| Environment | The workflow that reads it | What it gates |
|---|---|---|
| **`website-live`** | `.github/workflows/website-promote.yml` (`environment: website-live`, in its `promote` job) | the site's **live promotion**: the job copies an already-reviewed preview channel onto the live channel. `workflow_dispatch`-only, the actor is guarded to you, and its deployment branch policy is **`main`**. It holds repository **variables** — a project id, a provider name, a service-account address — and no secret, because the deploy authenticates by Workload Identity Federation. Its one-time setup is [../website/preview-promote.md](../website/preview-promote.md). |
| **`console-release`** | `.github/workflows/console-release.yml`, and its staged twin `docs/pending-workflows/console-release.yml` (which `console-release-linux.yml` and then `console-release-tag.yml` superseded, both now applied and removed, and which `console-release-feed.yml` supersedes and is still staged, as `console-release-symbols.yml` supersedes it for the crash reporter's symbol upload) — each declaring `environment: console-release` in its own `release` job | the desktop console's **signing, notarisation and stapling job**, on a `console-vX.Y.Z` tag push, publishing the release of that name. **This is the closer precedent**: it is a signing job, and it reads its six Apple secrets only through the environment, only as `secrets.NAME` in the one step that signs. |
| **`mobile-release`** | `.github/workflows/mobile-release.yml` — **applied 2026-10-23** (staged by **#777**) — declaring `environment: mobile-release` in both its `ios` job and its `android` job | the mobile app's **build and store submission** on the app's own release tag (`mobile-vX.Y.Z`): one job per platform, building on EAS and submitting to TestFlight's internal testers and to Play's internal-testing track, behind your approval. §10 has the rest of it. |

**And two things that are not environments, because a reader will expect them to
be.** `.github/workflows/release.yml` — the CLI's own release — declares **no**
`environment:` at all and signs **keyless**; and `console-tests.yml`, the
console's test workflow, holds no environment and no secret, which is why the
console's two files are two files: the job that reads the signing material must
never sit in the file that runs on every pull request.

**`mobile-release` is the third**, and the shape it copies is `console-release`'s
for the lock and `website-live`'s for the branch policy — §10 pins which refs the
trigger offers, and the policy has to admit them: the app's own release tag
(`mobile-v*`) and `main`. **The one thing this section adds is that the
environment has to exist before the file is copied in**: GitHub
creates a missing environment on first use, and the environment it creates that
way has **no required reviewer**, so the first run would go straight through,
holding the Expo token, with nobody's click in between. Create it, then copy the
file (the staged file's own header says the same thing, first).

**One difference from `console-release` is deliberate and should not be lost.**
The console's job holds Apple signing material in a protected environment because
a console release is signed in CI. The mobile path's store credentials stay where
they have always been — on your EAS project, under your account — and the only
credential the staged workflow reads is `EXPO_TOKEN`, behind your approval
(§14's Tension 5 records what that token reaches). If a **store credential or a
signing key** ever appears in this repository's settings, that is the finding
Check 89's step 5 exists to produce.

**The name the runbook and the staged file both use is `mobile-release`.** The
name in the staged file's `environment:` lines and the name in the repository's
settings must be **the same string**; if you rename it, both §10 and this section
have to be edited together, and so does the file.

In the repository's settings → **Environments** → **New environment**
(`https://github.com/dimipaun/fugaro/settings/environments`, and the general
documentation at `https://docs.github.com/`):

- **Name:** `mobile-release`.
- **Required reviewers:** **you, and only you**. This is what makes the
  environment a lock rather than a label: a job carrying `environment:
  mobile-release` waits for your approval before a runner exists, and a
  credential stored in the environment is unreadable without it.
- **Deployment branches and tags:** restricted to the refs that may deploy into
  it — and **this list has to name every ref §10's trigger offers, or the
  environment is unreachable.** GitHub refuses a deployment to an environment
  from a ref its policy excludes, so an environment restricted to `v*` **cannot**
  be reached by a `workflow_dispatch` run from `main`, whatever the workflow's
  own `on:` says — and since the owner's ruling of 2026-10-22 it cannot be reached
  by the app's own release tag either, because a `v*` policy admits the CLI's tags
  and not one `mobile-vX.Y.Z` tag. The staged file offers both triggers — the
  app's release tag, and a dispatch that defaults to a dry run — so **the policy
  must name `main` as well** as the app's tags (`mobile-v*` plus `main`). The two
  settings are one decision and have to be made together; the staged file's own
  header states the same pair. A policy
  that excludes the trigger's ref fails closed, but it fails at the wrong moment,
  on the day you need the build.
- **Environment secrets:** **exactly one, and only when you are ready to use
  it**: the **environment secret `EXPO_TOKEN`**, which is what the staged
  workflow's jobs read, and it is **never a repository secret**: §7's table and
  §10's account say the same thing, and the difference is the whole reason the
  environment exists. A **repository** secret is readable by every workflow in
  the repository, whatever job it runs in; an **environment** secret is readable
  only by a job that names `environment: mobile-release`, and then only after the
  required reviewer has approved it. A repository secret under the same name
  would be the weaker fence — which is what §7's "a key in CI is a key any later
  workflow change can read" is about. `ASC_API_KEY_ID`,
  `PLAY_SERVICE_ACCOUNT_JSON`, `APPLE_DISTRIBUTION_CERTIFICATE` and
  `ANDROID_UPLOAD_KEYSTORE` do **not** belong here: the store credentials and the
  signing keys are the EAS project's, and Check 89's step 5 reads the secret
  **names** precisely so that one appearing is noticed. The staged workflow holds
  the token on the steps that talk to EAS rather than at job level.

**Check 89 never reads that token**, and nothing about the environment changes
Check 89's own shape: the check is yours, at your terminal, and the environment
exists for the workflow **#777** staged.

**FACT for §9 (owner):** the environment's **name**; that its only required
reviewer is you; the branch restriction you set; and the **names** of any
secrets on it. **Never a value, and never a screenshot of a value.**

---

## 10. The build workflow, described and not written

**Global Constraint 8 forbids an agent to create or edit anything under
`.github/workflows/`, and GitHub refuses Fugaro's push of such a change anyway.**
So **this runbook describes a workflow and writes none.** The app's release
workflow **is written and applied**, and what follows is this page's account of
the file as it is — read off `.github/workflows/mobile-release.yml`
(**#777**, staged 2026-10-20, revised 2026-10-21 after that pull request's
security review, copied in on the owner's approval of 2026-10-23 per
[the copy procedure](../pending-workflows/README.md), with **the environment of
§9 created first**).

**It is not needed for Check 89**, which is entirely owner-run at your terminal:
the workflow is what a *release of the app* would use, and Check 89 is the check
that a build can reach a store track at all. Where the file and this page
disagree, **the file wins** — and then this page is the thing to fix.

| | |
|---|---|
| **Trigger** | the **`push` of a tag matching `mobile-v[0-9]+.[0-9]+.[0-9]+`** — the app's own stream, and not the CLI's `v[0-9]+.[0-9]+.[0-9]+` the file carried until the owner's ruling of 2026-10-22 (the CLI's filter started this workflow on every CLI release, so the app was built and submitted whenever the CLI moved); a bare `mobile-*` would also start a release on `mobile-vtest` or on a `mobile-v1.2.3-rc1` tag, which is not a release of anything. The other trigger is **`workflow_dispatch`**, whose one input `dry_run` **defaults to `true` and stops before the submission** — so a person trying the workflow gets a build, a summary and no store upload. The app is versioned with **its own** tag (C6's "versioned with the CLI's tag" is superseded), and a tag is the one ref that means "this commit is a release": the tag's version, `mobile/app.json`'s `expo.version` and `mobile/package.json`'s `version` are the same number, which the file's first step enforces. **Refused: `pull_request`, `pull_request_target`, `workflow_run` and every scheduled trigger** — a job that builds on EAS and holds an Expo token must never run on a pull request from any branch, which is the defect the console's two files exist to prevent: `.github/workflows/console-tests.yml` runs on every pull request and holds no secret, while the job that holds the signing material runs only on a tag push. |
| **The two jobs** | `ios` and `android`, one per platform, independent of each other — neither needs the other, so a platform that fails never blocks the other's release. Each runs on a **pinned** `ubuntu-24.04` with a 90-minute timeout, and **`concurrency`** is keyed on the workflow's own name rather than the ref, so two releases queue instead of submitting at once and a release mid-submission is never cancelled. |
| **Locks** | **two, on each job.** `environment: mobile-release` — whose only required reviewer is you (§9) — **and the release gate**: on a tag push each job runs `scripts/release-gate.sh` (the app's `mobile-vX.Y.Z` tag, which the script accepts under that one prefix), which is what every publishing workflow here does; a dispatch that would submit runs a second gate step, so a dispatch naming an off-main branch or a `mobile-vtest` tag cannot build and submit behind nothing but the click. **Before either gate, on any tag ref, the version rule runs**: the tag's version is compared with `mobile/app.json`'s `expo.version` **and** `mobile/package.json`'s `version`, and a tag that disagrees with either stops the job — EAS reports the config's version, so the alternative is a store record under a version no tag names, and the manifest is compared because the app's own test pins the two lines to each other and no workflow here runs that suite. This is why the jobs carry `checks: read` beside `contents: read`, and why the checkout is full-depth. |
| **What it does** | builds on EAS with the **`internal`** profile of §8 and **submits the build it just made** — `eas build --json` is captured and its id is passed to `eas submit --id`, not `--latest`, which would take whoever's latest build of the profile. iOS goes to TestFlight's **internal** testers and Android to Play's **internal-testing** track: the same C8-bounded tracks Check 89 checks, reached from CI rather than from your terminal. |
| **Which platforms run** | both are enabled by design (the owner's ruling of **2026-10-20**: both platforms ship from here), and one **repository variable** — `MOBILE_RELEASE_DISABLED_PLATFORMS`, naming `ios`, `android` or `ios,android` — is a **skip** list that turns one off. An unset variable names neither, so both run. |
| **Permissions** | declared explicitly and minimally, at the workflow level **and** on each job: **`permissions:`** with **`contents: read`**, plus `checks: read` because the release gate reads the commit's check-runs. No write scope of any kind — no `id-token: write`, no `packages: write`, never `permissions: write-all`. |
| **Actions** | pinned, every one, to a **full 40-character commit SHA** with a `# vX.Y.Z` comment beside it, as every other workflow in this repository is (a tag is mutable; a commit is not). Before copying the file in, compare the pins against `main`'s, so the copy cannot downgrade one. |
| **Secrets** | **one**, reached **only** through the job-level `environment:` and bound on the steps that talk to EAS rather than at job level: `secrets.EXPO_TOKEN`, which is the **`mobile-release` environment secret of §9 and never a repository secret** (a repository secret is readable by any workflow in the repository; an environment secret only by a job that names the environment, after your approval) — and no other secret, and **no literal** beside any reference: no value, no long base64 blob, no `password:` with something after it. |
| **The store identity** | **not in the file.** The two identities live in the EAS project's own `production` environment, and every `eas` command there runs through **`eas env:exec production`**, which loads them; the two **variable names** appear once, in the mask step, which passes each resolved value to `::add-mask::` so a config dump or a submit report cannot print a bundle identifier into a **public** repository's log. A mask read that fails or comes back empty stops the job before anything is built. |
| **What the summary carries** | the EAS build page's id and nothing else: the artifact download URL is never read, because it is a link to a signed binary whose `Info.plist` carries the bundle identifier, in a public run summary. |
| **The gate the app is outside** | the app is outside the **CLI tag's** release gate in the sense D125 means: no `vX.Y.Z` release step builds or submits it, and the gate holds no store credential. Each job *runs* `scripts/release-gate.sh` as its own step (after the version rule), and the script's one edit since 2026-10-22 is the change this row records: it strips the app's `mobile-` prefix and holds the version under it to the same strict `vX.Y.Z` shape, so the app's own tag is gated exactly as the CLI's is. Nothing else about the gate changed — the required-check set, the on-main check and the identical-tree fallback are the same — and no store credential, signing key or identity was added to it. |

**Two things this page adds to the file, because they are the ones that bite.**
**The environment must exist before the copy-in** (§9), or GitHub creates it
without a required reviewer and the first run submits with nobody's click. And
**the mask is a claim about the CLI, not a proof**: whether `::add-mask::`
really keeps the identity out of `eas`'s output is on the owner's checklist in
[docs/pending-workflows/README.md](../pending-workflows/README.md)'s
`## mobile-release.yml` section — to be confirmed on the first **dry run**,
which is what `dry_run`'s default is for.

**FACT for §10 (owner):** the file's staging commit; that the `mobile-release`
environment existed before the copy-in; the first dry run's result, and whether
the mask held. **Never a token value, and never a paste of the identity.**

---

## 11. Running the check, and what to paste back

**The steps are Check 89's.** Re-read them in the register
([../gcp-live-checklist.md](../gcp-live-checklist.md)) rather than from here:
its guard block, its five steps, its pass and fail conditions and its restore
step are the check, and each is written to be re-run from a fresh shell. Its
five steps are, in outline: the EAS project and the profile; the signed build;
the internal track and no public release; the install and the version; and no
store credential anywhere it should not be.

**The three commands steps 2 and 3 run**, with the two names in them that this
page settles in §8 — the profile `internal` and the environment the identities
were set in:

```bash
export FUGARO_IOS_BUNDLE_ID='<the reverse-DNS identity>'      # §8: public, never pasted back
export FUGARO_ANDROID_PACKAGE="$FUGARO_IOS_BUNDLE_ID"        # one name for both stores
eas build --platform all --profile internal --non-interactive
eas submit --platform ios --profile internal --latest
eas submit --platform android --profile internal --latest
```

**`--profile internal` on every line is the whole of it.** `eas submit` **without
a profile** is exactly the failure step 3's negative is about — its default is
not the internal track — and the register's own instruction is to stop and edit
rather than submit and try to halt a release afterwards. `--non-interactive` is
what makes a build fail instead of stopping to ask, and the register says to
**drop it on the first run** if EAS has to ask you about a credential the project
does not hold yet, which the first build after P4.4 usually does. The two
`export`s are the guard's own precondition (§3) and `eas-cli`'s local
resolution of `mobile/app.config.js` (§8), not a convenience: without them the
build is refused before it starts.

| Step | What it proves | How it can be wrong |
|---|---|---|
| 1 — the project and the profile | that the profile the check is about to use is the **store** one submitting to the **internal** track (**C8**), read *before* it is used | a profile copied from another project that still says a public track |
| 2 — the build | that a build was **signed with distribution credentials** and records **the tagged commit** | a successful build with an ad-hoc or simulator profile, which proves nothing about signing |
| 3 — the submission | that the artifact reached the **internal testing** tracks and that **nothing public was created** | a submission with no profile, whose default is not the internal track |
| 4 — the install | that the artifact **installs and runs** on a device, and reports the release's version (the app's own `mobile-vX.Y.Z` tag's; C6's "versioned with the CLI's tag" is superseded) | an install from a stale build, or a version that is not the tag's |
| 5 — the credential check | that no credential is tracked under `mobile/` and none is in CI | a key file added "for CI" and committed; an Expo token handed to an agent |

**What to paste back — Check 89's own list, and it is the whole of the record:**
the Expo account and the project id/slug; the `internal` profile's
`distribution`, its submit track, the **`ascAppId`** its iOS entry carries
(§8 — the app id read back, not pasted from here) and the **names** of any
credential files it references; both builds' ids, statuses, **credentials source**
and **gitCommitHash**; **both submission ids** and the tracks as the stores show
them; the install's device, OS and reported version; and the file list and
**the secret names** from step 5. **Never paste a signing key, a provisioning
profile's contents, an App Store Connect API key, a Play service-account JSON, a
password, an Expo token, or any credential value.**

**Where the paste goes.** The register's row 89 stays **NOT RUN** until it has
been run, and the `FACT` lines are the evidence that it was; the plan's rule is
that no task claims a live behaviour works until its check has been run. **The
owner's first submission of 2026-10-20 is not that record**: it is what created
the App Store Connect record and produced the app id now committed in the submit
profile (§5, §8), and it proves nothing about the internal track, the install, the
version or Check 89's step 5.

---

## 12. The rollback: pulling a bad internal build

**Nothing in this repository changes**, which is Check 89's own restore
conclusion: the check makes no commit, no file and no configuration change, so
there is nothing here to undo. What is left is on the stores' side, and the
following is how to take it back. **Check 89's restore step is the authority for
the check; this section is about the artifacts, and it overlaps it only where it
has to.**

- **A bad Android build.** The Play Console can **halt a rollout** and the
  internal-testing release can be replaced or removed from the track's own
  actions. An internal release reaches only your testers, so "pulling" it is a
  console action rather than a withdrawal. **The label moves: use the release's
  own actions menu on the internal-testing track, and read
  `https://support.google.com/googleplay/android-developer/` if it has been
  renamed.**
- **A bad iOS build.** A TestFlight build can be removed from testing, its
  testers removed, and it **expires on Apple's own schedule** if you do nothing.
  Internal testers are the only audience at that point. **The current vocabulary
  is at `https://developer.apple.com/help/app-store-connect/`.**
- **Nothing public was created, so there is no public release to withdraw.**
  That is C8's point and the reason the internal track is the check's target. If
  Check 89's step 3 ever lands a submission on a public track, the restore is the
  **halt** it names, and the incident is recorded with what was published and for
  how long — a public release is not reversible in the way an internal one is.
- **A build you simply want gone from EAS.** Delete it from the project's build
  list on `https://expo.dev/`. That removes the artifact, not the history of
  what was built from which commit.
- **A credential you believe leaked.** This is **not** a rollback of a build, and
  no step of Check 89 does any of it — its restore step creates, rotates,
  exports and deletes nothing. If you ever need it: revoke or reset the **Play
  upload key** (Play Console → the app's app-signing page, where a key reset is
  requested), revoke the **distribution certificate** and issue a new one in the
  Apple developer account, **revoke the App Store Connect API key**, **revoke the
  Play service account's key** in Google Cloud and delete the service account,
  and **revoke the Expo access token** in your Expo account. Each is the vendor's
  page, and each is a change to your accounts rather than to this repository —
  check the vendor's documentation for the current path before you start, and
  record which one you did.

**What a rollback cannot do** is un-publish something that reached a public
track, or un-see a key that was pasted into a place the repository does not
control: a credential-shaped literal in a repository, a log or a chat is in every
copy of it, which is why §7's rule is "never", not "not yet".

---

## 13. What is NOT verified here

- **The app's behaviour.** Installing is as far as Check 89 goes. The
  microphone ask, the hands-free audio, Bluetooth and barge-in are check **86**;
  multi-account switching is check **87**; and the on-device STT/TTS bake-off is
  check **85** (the plan's checks table, which is where these numbers are
  allocated). **The wake word is not in that block at all** — it is check
  **102**, in Phase 6, which the owner **approved on 2026-10-18**. Every one of them
  needs hardware this check does not use. The register's Check 89 step 4 folds
  the wake word into "checks 85, 86 and 87", which is the same mismatch; the fix
  belongs to P4.2a in the register's own text, and until it is made this page
  gives the plan's numbers rather than repeating the register's (see §14,
  Tension 4).
- **The public release.** Not made, not attempted, not implied (C8). Nothing
  here proves a production release would work, and Google's own production-access
  rules are not exercised.
- **CI.** **P4.3**'s CI job — the shared package's tests — is **staged for the
  owner to copy in** as `docs/pending-workflows/shared-tests.yml` and is **not
  yet running**: `.github/workflows/ci.yml` has no shared-package job, and the
  plan's open item 4 says what that means — *"Until it exists, the shared
  package's tests run only where someone runs them, and that is a real gap in
  this plan's coverage."* When it lands it holds no store credential. The app's
  release workflow of §10 is **applied (2026-10-23) and not yet run**: it has run
  nowhere, in no repository, and its first **dry run** is what would verify it — the mask
  step, the environment, the pins and the gate are all unexercised claims about
  GitHub and `eas-cli` until then.
- **The owner's own two runs, and what this repository records of them.** The
  first submission of **2026-10-20** and the export-compliance declaration of the
  same day are in [mobile/README.md](../../mobile/README.md) as the owner's
  record; **what they left on Apple's side** — whether the build reached *Ready
  to test*, whether it was installed, what version it reports — is not recorded
  here and is not Check 89. Treat the README's two dates as the owner's account,
  not as this page's evidence.
- **The CLI's release gate.** The app is outside it (D125), and no part of this
  runbook changes `scripts/release-gate.sh`. The script's one edit since is not
  this runbook's: it strips the app's `mobile-` prefix so the app's own tag is
  held to the same gate (2026-10-22), and §10 records it with the staged file it
  belongs to.
- **The rules the app reads under.** P2.1/P2.2's grant and its emulator denials
  are another task's; Check 89 never touches the database.
- **The toolchain, on any machine in this repository.** No `expo start`, no
  `expo lint`, no `eas build` and no device or simulator run has happened here.
  `mobile/package.json`'s `test` script is the whole of the verified surface, and
  [mobile/README.md](../../mobile/README.md) says so in its own words.
- **`belong`.** Never touched by any step of this runbook or of Check 89
  (Global Constraint 11). No fixture is recorded from it, and no account of it
  is configured here.
- **Everything about Expo, Apple and Google that this page hedged.** Every
  console label, CLI flag and menu path above is a pointer to a vendor's page,
  not a transcription of one. If a step's instructions do not match what you see,
  the vendor's page is right and this page is stale — and the fix is an edit
  here, in a pull request, not a step performed differently in private.

---

## 14. Recorded tensions, and how each was settled

**This section records mismatches between files on this tree, and — for the ones
that have since been ruled — how. The rulings are not this runbook's: **the
owner rules**, and these were settled by P4.4's own files (**PR #675**, **#745**,
**#759**), by P4.2a's amendment of the register's Check 89, and by the owner's
rulings of 2026-10-19/20 that the staged release workflow (**#777**) carries.
**Tension 4 is still open** and **Tension 5 is recorded without being settled** —
both say so below, and each names the amendable text and whose it is; the three
settled ones say what changed on this page because of them.**

**Tension 1 — the bundle identifier and the Android package: SETTLED (PR #675),
and this page follows the ruling.** It was one file with two things asked of it.
P3.1's own test (`mobile/src/app-config.test.js`, merged) asserts that
`mobile/app.json` carries **no** `bundleIdentifier`, **no** Android `package`
and no reverse-DNS string at all, with the reason written into the test:
*"the release identities (the iOS bundle identifier and the Android package)
are absent too: those are the owner's, and they differ per installation
(D125's release path, check 89 is owner-run)"*. Check 89's first guard block read
**both fields out of that same file** and stopped without them. The ruling keeps
P3.1's fence and moves the read — the second of the two ways this section used to
list:

- **the identities do not go into `mobile/app.json`.** They reach a build from
  two EAS environment variables — `FUGARO_IOS_BUNDLE_ID` and
  `FUGARO_ANDROID_PACKAGE`, one public reverse-DNS name each — through
  `mobile/app.config.js`, the dynamic config P4.4 added (Expo's
  `({ config }) => ({ ...config, ... })` form). Neither `eas.json` nor the
  config file holds a value: **no committed file carries an identity** (D125),
  the value is set once per EAS environment, and it is exported in the shell for
  a local build (§8);
- **Check 89's guard reads them the way a build does**, and the register's text
  was amended to match. Its own words, verbatim, from
  `docs/gcp-live-checklist.md` section **"Check 89"**: a variable missing from
  your shell stops the guard with its own sentence naming the variable and the
  fix; and if the config resolves with neither field, it exits with
  `sys.exit("Check 89 stopped: the config resolved through app.config.js
  carries no ios.bundleIdentifier or android.package, so there is nothing to
  submit a build under — a build made now could not reach any track")`, followed
  by a bare `|| exit 1`, whose comment in the same block reads *"the store-identity
  stop has to be a real stop: sys.exit inside a command substitution only sets a
  status, so the guard acts on it here -- otherwise it would print the reason and
  read on"*. The guard reads the two fields with `.get()` and tests
  `if not bid or not pkg`, so an absent field is **not** an error it trips over:
  it is the stop it was written to make, and it takes it with its own message.

**What that changes for you:** §3's prerequisites gain the two `export`s, and §8
says where the values are set. **FACT for §14 (owner):** that both variables were
set in the EAS project's three environments and exported for the run — **the
names, never a value**.

**Tension 2 — `mobile/eas.json`, and what P4.4's `Files` line meant: SETTLED,
and both files are in the tree.** The plan's P4.4 line used to say *"Files. None
in this repository."* while Check 89's guard attributed `mobile/eas.json` to
P4.4 (*"P4.4 makes it; this check reads it, it does not create it"*) — prose and
a register that both claimed to be the authority for the same file. It is no
longer a tension: **the plan's line now names both files** —
*"**Files.** `mobile/eas.json` … and `mobile/app.config.js` …"* — and
**`mobile/eas.json` is committed**, with the three profiles and the submit
profile §8 describes. The guard's file test still exists and still stops, and its
own words, verbatim, from the same section, are: *"Check 89 stopped: there is no
mobile/eas.json in this checkout, so there is no build or submit profile to read
(P4.4 makes it; this check reads it, it does not create it)"*. The plain reading
of that message is the one that stays true: a checkout **older** than P4.4 has no
profiles, and the guard stops there rather than building against defaults nobody
chose.

**Tension 3 — the version the app reports: SETTLED, and it is `expo.version`.**
C6 said the app is versioned with the CLI's tag, and the owner's ruling of
2026-10-22 supersedes that half of it: the app has its own tag
(`mobile-vX.Y.Z`), and the tag's version, this line and `mobile/package.json`'s
`version` are the same number, which the staged workflow's first step enforces on
all three. What C6 settled does not change: `mobile/app.json`'s `version` is
`0.0.0` today and P3.1's test pins it to `mobile/package.json`'s — which is why
the workflow compares the manifest too, and why a release sets both lines in one
commit: nothing this repository runs executes the app's suite, so the pinned pair
would otherwise only be checked by hand. The rule
that reconciles them is the one `mobile/eas.json` and `mobile/README.md` now
carry together: the **build number** (`expo.ios.buildNumber`,
`expo.android.versionCode`) is EAS's, kept on the project by
`cli.appVersionSource: "remote"` and raised by `autoIncrement: true` on
`internal` (§8) — **the build number is not the version the stores show** — and
the version the stores show, and the one Check 89's step 4 compares with `$TAG`,
is **`expo.version` in `mobile/app.json`**, which a release sets to the version
the tag names — and `mobile/package.json`'s `version` with it, in the same
commit, because the app's own test pins the two to each other and the workflow's
first step refuses a tag that disagrees with either. `0.0.0` is the placeholder
that says no release has been cut yet. **FACT for §14 (owner):** that
`expo.version` and the manifest's `version` were set to the release's version
before step 4, and the commit that did it.

**Tension 4 — three check numbers, and the register's step 4 has them wrong.**
This is a numbering mismatch rather than a rule conflict, and it is recorded
here because **§13 must not repeat it and the register should not keep it either**:

- The plan's checks table allocates **85** to the on-device STT/TTS bake-off,
  **86** to hands-free audio (the background mode, the microphone ask, Bluetooth
  and barge-in), and **87** to multi-account switching.
- **The wake word is check 102** — the plan's 100–109 block, Phase 6, written as
  *"the wake word's recognition rate on the oldest supported device"*. **That
  block is approved**: the plan's Phase 6 heading reads *"APPROVED by the owner
  on 2026-10-18"*, its checks note reads *"Checks 100–109 (approved
  2026-10-18)"*, and **P6.11** is the task that writes 100–104 into the
  register. An earlier revision of this page carried the status the plan held
  before that date — the block proposed and awaiting the owner — which was true
  then and is not true now; the correction is dated here rather than silently
  dropped.
- **Check 89's step 4** (P4.2a, **PR #580**) says instead that *"the microphone
  ask, the wake word, the hands-free audio, multi-account switching"* belong to
  *"checks 85, 86 and 87"* — folding the wake word into the 80–89 block, where
  no row is its, and leaving 85 (the bake-off) with behaviours that are 86's.

The plan's table is the allocation, so this runbook uses its numbers (§13), and
**the amendment is P4.2a's, in the register's text**: the fix is to drop the wake
word from step 4's sentence and point it at check 102 — **approved on
2026-10-18**, and still not run — and to let the microphone ask and barge-in rest
on 86. **FACT for §14 (owner):**
that the register's step 4 was amended, and the commit that did it.

**Tension 5 — D120's ceiling sentence, and the release workflow the owner staged.
Recorded, not resolved here, and the ruling is already made.** D120 (the owner's
answer of **2026-10-17**) says, in the plan's own words, that *"CI at most builds
a **test** artifact with no store credential"*. The workflow
**#777** staged, applied on 2026-10-23 as `.github/workflows/mobile-release.yml`, does more than that: it
builds the **`internal`** profile and **submits** it to TestFlight and to Play's
internal-testing track, reading `EXPO_TOKEN` behind the `mobile-release`
environment. No sentence was ever amended — the file was staged on the owner's
rulings of **2026-10-19/20** ("both platforms ship from here"), which is later
than D120 and is the owner's own decision about their own accounts, and the file
carries it in its header. What D120 forbids is unaffected and still holds: **no
agent runs `eas build` or `eas submit`**, no signing key or store credential
enters an agent's environment, and nothing here or in the staged file is built or
submitted by an agent. What is **not** settled is D120's one sentence, which now
reads as a ceiling the owner has gone past deliberately: the plan still says it,
this page records the difference rather than smoothing it, and the amendment —
if the plan's sentence is to be rewritten — is the plan's, in a change of its own.
**FACT for §14 (owner):** whether D120's sentence is amended to the staged
workflow's shape, and the commit that did it.

---

## 15. Sources, and the files this page answers to

**The repository files this runbook is answerable to** (each is the authority for
what it says, and this page paraphrases none of them as certain):

| File | What it settles |
|---|---|
| [implementation-plan.md](implementation-plan.md) | Task **P4.4**, its dependencies, its verification and its reviewer tier; **Global Constraint 9**; checks 80–89 and open item 5; **C2**, **C6**, **C8** |
| [../gcp-live-checklist.md](../gcp-live-checklist.md) | **Check 89** — the steps, the guard block, the `FACT` lines, the pass and fail conditions and the restore step (**P4.2a**, merged as PR **#580** on 2026-10-19) |
| [reconciliation.md](reconciliation.md) | **D111**, **D120**, **D125** and the findings behind them (F24, U6) |
| [../../mobile/app.json](../../mobile/app.json) and [../../mobile/package.json](../../mobile/package.json) | the app's Expo config and its `engines` (**P3.1**) |
| [../../mobile/eas.json](../../mobile/eas.json) and [../../mobile/app.config.js](../../mobile/app.config.js) | the three build profiles, the submit track, the **App Store Connect app id** the iOS submit entry carries, and `cli`'s `appVersionSource`/`requireCommit`; and the two identity variable names, the reverse-DNS patterns, the export-compliance declaration and what the dynamic config refuses (**P4.4**, merged as PR **#675** and extended by **#745** and **#759**) — §8 is this page's account of both, and where they differ **the files win** |
| [../../mobile/README.md](../../mobile/README.md) | what the app is, where its secrets live, the internal-testing path (the two `env:set` commands and the submit), the export-compliance declaration, the owner's first submission of **2026-10-20**, and what is not verified |
| [../../.github/workflows/mobile-release.yml](../../.github/workflows/mobile-release.yml) | the app's release workflow — **applied 2026-10-23** (staged by **#777**): its trigger, its two jobs, the `mobile-release` environment each declares, the `EXPO_TOKEN` it reads and the one variable that skips a platform. §10 is this page's account of it, and where they differ **the file wins** |
| [../../.github/workflows/website-promote.yml](../../.github/workflows/website-promote.yml) and [../../.github/workflows/console-release.yml](../../.github/workflows/console-release.yml) | the protected environments this repository installs, read off the files themselves: `environment: website-live` in `website-promote.yml`'s `promote` job and `environment: console-release` in `console-release.yml`'s `release` job, each declared on a job (the console's is declared again by its three staged twins, the last of which moves it to the console's own `console-vX.Y.Z` tag). [../../.github/workflows/release.yml](../../.github/workflows/release.yml) is there too, as the counter-example: no environment, keyless signing (§2, §9) |
| [../pending-workflows/README.md](../pending-workflows/README.md) | how a staged workflow is copied in, and why an agent may not copy it |
| [../website/preview-promote.md](../website/preview-promote.md) | the same owner-only shape worked through end to end for `website-live` |
| [../../SECURITY.md](../../SECURITY.md) | the trust model and its known limits — the reason a credential does not go in an agent's environment |

**The vendors' pages to check at run time**, which is what every hedge above
points at:

| Page | What to check there |
|---|---|
| `https://docs.expo.dev/` | the current EAS CLI subcommands, the app config's `extra.eas.projectId`, organisations and accounts |
| `https://docs.expo.dev/app-signing/managed-credentials/` | what **EAS-managed** credentials are, what EAS holds for you, and `eas credentials` |
| `https://expo.dev/` | the account, its organisations, its access tokens, and the build list |
| `https://developer.apple.com/programs/enroll/` | membership kinds and current enrolment requirements |
| `https://developer.apple.com/account` | where identifiers, certificates and profiles are registered |
| `https://appstoreconnect.apple.com` and `https://developer.apple.com/help/app-store-connect/` | the app record, TestFlight, internal testing, and the App Store Connect API keys |
| `https://play.google.com/console/signup` and `https://play.google.com/console` | the developer account, the app record, internal testing, and app signing |
| `https://support.google.com/googleplay/android-developer/` | Play's rules for testing tracks, production access and key resets |
| `https://docs.github.com/` and `https://github.com/dimipaun/fugaro/settings/environments` | protected environments, required reviewers, deployment branch policies |

---

*Status of this document: written 2026-10-18, **refreshed 2026-10-22** against
the tree, and **never run**. It is the runbook for Task P4.4 of
[implementation-plan.md](implementation-plan.md); the check it serves is **Check
89** in [../gcp-live-checklist.md](../gcp-live-checklist.md), written by P4.2a
and merged as PR **#580** on 2026-10-19, and **NOT RUN**. No agent performs any
step of it. The refresh holds the page to what has landed since: P4.4's two files
(**#675**, **#745**, **#759**), the App Store Connect app id in the submit
profile, and the staged release workflow (**#777**).
Related: [README.md](README.md) · [reconciliation.md](reconciliation.md) ·
[../../mobile/README.md](../../mobile/README.md) ·
[../pending-workflows/README.md](../pending-workflows/README.md).*
