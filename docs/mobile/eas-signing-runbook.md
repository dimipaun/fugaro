# The EAS project, the signing keys and the store entries — the owner's runbook

**Status: written, never run. This page is the instructions, not a record.**
It is the runbook for **Task P4.4** of
[implementation-plan.md](implementation-plan.md) — *"The EAS project, the
signing keys and the store entries — owner-run"* — and for the check that
verifies it, **Check 89** in the live register
([../gcp-live-checklist.md](../gcp-live-checklist.md)).

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-18.

**The rules of this run, in one place, because each is a step an agent could
otherwise take:**

- **No agent performs any step of this runbook.** Every step below is the
  owner's, **at your own terminal**, with your own Expo, Apple and Google
  accounts, your own signing keys and your own EAS project.
- **No agent runs `eas build` or `eas submit`**, and **no agent touches a signing
  key or a store credential**: **no signing key or store credential ever enters
  an agent's environment** (the plan's **Global Constraint 9**, decision
  **D120**).
- **Nothing here has been run.** No agent has `eas` on a `PATH`, no agent has
  logged into an Expo account, and no agent has opened the Apple or Google
  consoles.

**The plan already says where this work sits, and this page does not move it.**
P4.4's own line reads *"Files. None in this repository. **Owner-run.** **Depends
on.** P3.1, P4.2a (check 89's text must be in the register before it is run).
**Verification.** Check 89: a signed build reaches the store's internal track."* This document is the runbook for that verification; it is prose, and no
repository file participates in the run. (One exception is recorded under
[Recorded tensions, and how each was settled](#14-recorded-tensions-and-how-each-was-settled):
`mobile/eas.json`, which Check 89's guard block attributes to P4.4 while the
plan's `Files` line says none — P4.4's files landed in a change of their own,
**PR #675**, and §8 is this page's account of them.)

---

## 1. What this runbook is, and what it is not

| | |
|---|---|
| **It is** | the setup the owner performs **once** so that Check 89 can run at all: the Expo/EAS project, the Apple and Google prerequisites, the signing credentials and where they live, the two repository settings they need, and the rollback. |
| **It is not** | the check. **This runbook does not restate Check 89. The register is its authority.** Check 89's steps, its guard block, its `FACT` lines, its pass and fail conditions and its restore step are written into [../gcp-live-checklist.md](../gcp-live-checklist.md) by **task P4.2a**, merged into `main` as **PR #580** on **2026-10-19** (that pull request's own branch was `fugaro/20261018-130217-49c7`), so the section is in the register you read today; **the register's text, not this one, is what you follow** when the two describe the same thing. Where they differ, the register wins and this page has a defect to fix. |
| **It is not** | a workflow. **This runbook describes a workflow and writes none.** No file is created under `.github/workflows/` by this task or by any agent (Global Constraint 8), and §10 describes the shape a later staged file under `docs/pending-workflows/` would have rather than writing it. **No agent copies a workflow file into `.github/workflows/`**: that copy-in is the owner's step, per [the copy procedure](../pending-workflows/README.md). |
| **It is not** | a claim that anything works. Until Check 89 runs, the app has no signed artifact and no store presence at all, the register's row 89 stays **NOT RUN**, and the plan's open item 5 stands: *"Checks 80–89 are owner-run and none has been run."* |

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
  outside the Go module) and **C6** (the app is versioned with the CLI's tag but
  released on the stores' cadence);
- the check's own guard block, its file names and its stop conditions, from the
  register's Check 89 text, including `mobile/app.json`, `mobile/eas.json`,
  `mobile/app.config.js`, `node`, `git describe --tags --exact-match`,
  `eas whoami`, `eas project:info`, `distribution: store`,
  `submit.internal.android.track` and the two identity variables the guard
  exports;
- **P4.4's own two files, read off the tree rather than off this page**:
  `mobile/eas.json` (the three profiles, `cli.requireCommit`,
  `cli.appVersionSource` and the submit track) and `mobile/app.config.js` (the
  two variable names, the two patterns and what it refuses), both merged as
  **PR #675** — §8 is this page's account of them, and where they differ the
  files win;
- the decision that the internal track and no public release is what is being
  proved: **C8**;
- what the app may hold, which is **not** a store credential: **D111** (exactly
  two items per account, in platform secure storage);
- the repository-side precedent, read off the tree's own workflow files rather
  than off a plan: **`.github/workflows/website-promote.yml`** carries
  `environment: website-live` in its `promote` job, and
  **`.github/workflows/console-release.yml`** carries `environment:
  console-release` in its `release` job — the two protected environments this
  repository has, both installed and both declared on a **job**, not in a
  comment (`.github/workflows/release.yml` signs the CLI **keyless**, with no
  environment at all). Every workflow this page cites, it cites by **content** —
  the `environment:` key and the job that declares it — and never by line
  number, so the citation cannot rot when a line above it grows. §9 names them,
  says what each one gates, and says which one `mobile-release` would be.

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
2. **The project.** From inside `mobile/`, with `mobile/app.json` already on the
   tree (P3.1's file):

   ```sh
   cd mobile
   eas whoami                 # your account, per §3
   eas project:info           # what the checkout resolves to today, if anything
   eas project:init           # only if no project exists yet
   ```

   `eas project:init` creates the project under your account (or the
   organisation) and records the project id **in the app config**, under
   `expo.extra.eas.projectId` — a public identifier, and the reason the built app
   can talk to EAS at all. Afterwards, `eas project:info` is the command that
   prints the slug and the id; **Check 89's step 1 names both commands** and
   reads exactly these two identifiers. If your CLI of the day spells the
   subcommand differently, that is a rename, not a different step: **check
   `https://docs.expo.dev/` for the current name and use it**, and note the
   rename in your `FACT` line.
3. **The `slug`.** `mobile/app.json` already carries `expo.slug: "fugaro"`. The
   slug and the project id are what EAS resolves a build profile from, and a
   project created with a different slug under the same account is a **second
   project**, which is how one ends up with two build histories and one of them
   stale. Register the project the config names.

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
   they are the two places this runbook cannot write for you:
   - **`mobile/app.json`**, under `expo.ios.bundleIdentifier` — the field Check
     89's guard block prints. **See §14: this field is the subject of a recorded
     tension and is not on the tree today.**
   - **The Apple developer account**, where the identifier is registered under
     **Certificates, Identifiers & Profiles** → **Identifiers**
     (`https://developer.apple.com/account`). The page's menu labels have moved
     between releases; **the page is the authority, not this sentence.**
3. **The app record and the internal testers.** In **App Store Connect**
   (`https://appstoreconnect.apple.com`): your **Apps** list → the app record
   for the bundle identifier above → **TestFlight** → **Internal Testing**, where
   you add the internal tester group. The help root is
   `https://developer.apple.com/help/app-store-connect/`; **read it for the
   current labels** for creating an app record, for the internal group, and for
   how a build moves from *Processing* to *Ready to test* — Check 89's step 3
   records exactly that transition, and step 4 installs from the internal track.
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
   Like the bundle identifier it must agree in two places:
   - **`mobile/app.json`**, under `expo.android.package` — the field Check 89's
     guard block prints (and, again, §14);
   - **the Play Console app record**, at creation (`https://play.google.com/console`).
   Note that the two stores' identities need not be the same string, but that
   keeping them recognisably related is what makes a support question tractable.
   Both must be strings **you own**: a package name you do not control is a
   package name you cannot publish later.
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
  authenticates by Workload Identity Federation. **A store credential in a mobile
  workflow would still be the first this repository has ever held**: Apple's six
  are the console's own signing material, not a store credential, and no
  workflow holds an App Store Connect **or** Play submission credential. That
  is why §10 puts none there and D120's ceiling is a build with no store
  credential at all.
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
| **Play service account** (the JSON key) | **owner-held**, on your machine at the path `mobile/eas.json` names — `serviceAccountKeyPath` | you, and `eas submit` reading that path | **never in this repository** (the file Check 89's step 5 looks for is `google-play-service-account.json`), never in CI. Its **contents** are never read by Check 89 |
| **Expo access token** (the one name CI may hold) | your Expo account's access tokens, on `https://expo.dev/`, under the account's own settings; stored as the **environment secret `EXPO_TOKEN`** **on the `mobile-release` environment** (§9) if and only if a workflow is added — **never a repository secret** | you, and — if and only if a workflow is added — the one job that names `environment: mobile-release` and no other job in the repository | **never in this repository**, never on a build machine that does not need it, never as a repository secret |

**The secret *names* a future build workflow would read, and nothing else.** This
page names them and holds no value of any of them; a value here would be the
defect. The names, and whether CI may hold each:

| Name | What it is | CI may hold it? |
|---|---|---|
| `EXPO_TOKEN` | an Expo access token that lets a build run under your account | **yes, and only this one — and only as the `mobile-release` environment secret**, never a repository secret (§9): a repository secret is readable by every workflow in the repository, an environment secret only by a job that names the environment. For a build profile with **no store credential** (D120's ceiling, §10) |
| `ASC_API_KEY_ID` | the App Store Connect API key's **identifier** (not a secret in itself, but half of the pair a key needs) | **never in CI** — the store path is owner-run |
| `PLAY_SERVICE_ACCOUNT_JSON` | the Play service-account JSON's contents | **never in CI**, and never in this repository |
| `APPLE_DISTRIBUTION_CERTIFICATE` | an owner-held iOS distribution certificate (only relevant if you ever chose to sign in CI rather than let EAS hold it) | **never in CI** |
| `ANDROID_UPLOAD_KEYSTORE` | an owner-held upload keystore (the same caveat) | **never in CI** |

**A store credential in CI is the finding Check 89's step 5 exists to produce.**
That step lists the repository's secret **names** — *the names only, never a
value* — and asks you to record any whose name suggests a store or signing key
and to say what it is for. EAS needs **no** credential in this repository's CI for
Check 89: nothing here builds the app in CI, P4.3's CI job — **the shared
package's tests, staged for the owner to copy in (PR #582) and not yet running**
— holds no store credential either, and the one signing job this repository does
run (`console-release.yml`) reads the console's own Apple material and no store
credential at all.

**FACT for §7 (owner):** whether each credential is EAS-managed or owner-held —
which is the same question Check 89's step 2 records as the build's **credentials
source**; the **names** of any credential files the profiles reference, e.g. the
value of `serviceAccountKeyPath`; and the secret **names** CI holds. **Never
paste a signing key, a provisioning profile's contents, an App Store Connect API
key, a Play service-account JSON, a password, an Expo token, or any credential
value.**

---

## 8. `mobile/eas.json` and `mobile/app.config.js` — the profiles, the environments and the identities

**Both files are in the tree, and both are P4.4's** (PR **#675**): `eas.json`
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
  value. The `ios` submit entry **exists and names nothing** (`"ios": {}`): EAS
  prompts for the App Store Connect app at submit time, and step 1 records that
  the entry is there;
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
  C6 ties to the release tag (§14, Tension 3);
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
- **the `preview` profile is the one CI could ever build**, if a workflow is
  added (§10): internal distribution, no `submit` section, no credential of any
  kind. It is not the profile Check 89 uses;
- **A profile whose submit section names a public track is a stop** — edit it and
  re-run, rather than submitting and trying to halt a release afterwards. That
  sentence is Check 89's own, and it is repeated here only because it is the one
  instruction in the whole run that has no recovery.

**FACT for §8 (owner):** the `internal` profile's `distribution`; its submit
track; whether an `ios` submit entry exists; and the **names** of any credential
files the profiles reference (`serviceAccountKeyPath`). **Never their contents.**
The two identity values are public, and they are still not pasted back: §11's
list is Check 89's, and it asks for the profile's values, not the identity's.

---

## 9. The repository settings: the protected environment

The environment is the repository-side lock, and it is the owner's to create.
**There are two protected environments in this repository today:** both are
**declared on a job of an installed workflow** — not in a comment, not in a plan
and not staged — and this page's precedent claim is about them and not about a
plan:

| Environment | The workflow that reads it | What it gates |
|---|---|---|
| **`website-live`** | `.github/workflows/website-promote.yml` (`environment: website-live`, in its `promote` job) | the site's **live promotion**: the job copies an already-reviewed preview channel onto the live channel. `workflow_dispatch`-only, the actor is guarded to you, and its deployment branch policy is **`main`**. It holds repository **variables** — a project id, a provider name, a service-account address — and no secret, because the deploy authenticates by Workload Identity Federation. Its one-time setup is [../website/preview-promote.md](../website/preview-promote.md). |
| **`console-release`** | `.github/workflows/console-release.yml` (`environment: console-release`, in its `release` job) | the desktop console's **signing, notarisation and stapling job**, on a `v*` tag push, publishing `console-vX.Y.Z`. **This is the closer precedent**: it is a signing job, and it reads its six Apple secrets only through the environment, only as `secrets.NAME` in the one step that signs. |

**And two things that are not environments, because a reader will expect them to
be.** `.github/workflows/release.yml` — the CLI's own release — declares **no**
`environment:` at all and signs **keyless**; and `console-tests.yml`, the
console's test workflow, holds no environment and no secret, which is why the
console's two files are two files: the job that reads the signing material must
never sit in the file that runs on every pull request.

**So `mobile-release` would be this repository's third protected environment**,
and the shape to copy is `console-release`'s for the lock and `website-live`'s
for the branch policy — §10 pins which refs each trigger offers, and the policy
has to admit them. What the EAS signing runbook itself adds is **nothing yet**:
no mobile workflow exists (§10), the store path is owner-run at your terminal
(§7, D120), and this section records the setting so that a staged file would
have somewhere to read from **before** it is written rather than after.

**One difference from `console-release` is deliberate and should not be lost.**
The console's job holds Apple signing material in a protected environment
because a console release is signed in CI. The mobile path does the opposite:
Check 89's build and submission are yours, at your terminal, and the only
credential a mobile workflow may ever read is `EXPO_TOKEN` for a build profile
with **no** store credential (§10, D120's ceiling). If a store credential ever
appears in this repository's settings, that is the finding Check 89's step 5
exists to produce.

**The name this runbook uses is `mobile-release`.** It is a proposal, and the
owner may name it otherwise — but the name a staged workflow carries in its
`environment:` line and the name in the repository's settings must be **the same
string**, and §10 and this section must be edited together if you choose another.

In the repository's settings → **Environments** → **New environment**
(`https://github.com/dimipaun/fugaro/settings/environments`, and the general
documentation at `https://docs.github.com/`):

- **Name:** `mobile-release`.
- **Required reviewers:** **you, and only you**. This is what makes the
  environment a lock rather than a label: a job carrying `environment:
  mobile-release` waits for your approval before a runner exists, and a
  credential stored in the environment is unreadable without it.
- **Deployment branches:** restricted to the refs that may deploy into it — and
  **this list has to name every ref §10's trigger offers, or the environment is
  unreachable.** GitHub refuses a deployment to an environment from a ref its
  policy excludes, so an environment restricted to `v*` tags **cannot** be
  reached by a `workflow_dispatch` run from `main`, whatever the workflow's own
  `on:` says. If you take §10's tag trigger (the recommendation, and C6's
  shape), the policy is the release **tags** (`v*`) and nothing else, and a
  workflow dispatched from an arbitrary branch cannot reach the environment at
  all. **If you take §10's `workflow_dispatch` alternative, the policy must name
  `main` as well** (`v*` plus `main`), or every dispatch stops at the
  environment instead of at the build; the two settings are one decision and
  have to be made together. A policy that excludes the trigger's ref fails
  closed, but it fails at the wrong moment, on the day you need the build.
- **Environment secrets:** **none today**. If a workflow is ever added, the only
  secret that belongs here is **the environment secret `EXPO_TOKEN`**, and it is
  **never a repository secret**: §7's table and §10's shape say the same thing,
  and the difference is the whole reason the environment exists. A **repository**
  secret is readable by every workflow in the repository, whatever job it runs
  in; an **environment** secret is readable only by a job that names
  `environment: mobile-release`, and then only after the required reviewer has
  approved it. A repository secret under the same name would be the weaker
  fence — which is what §7's "a key in CI is a key any later workflow change can
  read" is about. `ASC_API_KEY_ID`,
  `PLAY_SERVICE_ACCOUNT_JSON`, `APPLE_DISTRIBUTION_CERTIFICATE` and
  `ANDROID_UPLOAD_KEYSTORE` do **not** belong here: the store path is owner-run
  (§7), and Check 89's step 5 reads the secret **names** precisely so that one
  appearing is noticed.

**This environment has no purpose yet if no workflow exists**, and it is
recorded here so that the workflow §10 describes has somewhere to read from
**before** it is written rather than after.

**FACT for §9 (owner):** the environment's **name**; that its only required
reviewer is you; the branch restriction you set; and the **names** of any
secrets on it. **Never a value, and never a screenshot of a value.**

---

## 10. The build workflow, described and not written

**Global Constraint 8 forbids an agent to create or edit anything under
`.github/workflows/`, and GitHub refuses Fugaro's push of such a change anyway.**
So **this runbook describes a workflow and writes none.** If a CI job is ever
wanted for the app — and it is **not needed for Check 89**, which is entirely
owner-run — the file is staged under `docs/pending-workflows/` and copied in by
you, exactly as the website's two workflows were
([the copy procedure](../pending-workflows/README.md)).

The shape a later staged file (a name such as `mobile-test-build.yml`) would
have, and the reasons each line is what it is:

| | |
|---|---|
| **Trigger** | the **`push` of a `v*` tag, and nothing else** — the app is versioned with the CLI's tag (C6), and a tag is the one ref that means "this commit is a release". If you prefer to build on demand, the alternative is **`workflow_dispatch` from `main`** with the same locks — **except for one setting: §9's deployment-branch policy must then name `main` as well as `v*`**, or GitHub refuses the deployment from a ref the environment excludes and the run never reaches the build. What the alternative costs is that a build is then not tied to a tag, so its `gitCommitHash` has to be compared to the tag by hand. **Refused either way: `pull_request`, `pull_request_target`, `workflow_run` and every scheduled trigger** — a signing job that runs on a pull request from any branch of this repository is the defect the console's two files exist to prevent, and those two files are **installed**: `.github/workflows/console-tests.yml` runs unconditionally on every pull request and push to `main` and holds no secret, while `.github/workflows/console-release.yml` holds the signing material and runs only on a `v*` tag push. One file carrying both would put the signing job on every pull-request run, and a fork's pull request is not the only way in. |
| **Locks** | **two**, as the website's promote job has: `environment: mobile-release` (its only required reviewer is you, §9 — and its deployment-branch policy has to admit the trigger's own ref, which the row above pins down) **and** a guard on the actor, `if: github.actor == 'dimipaun'`, so a dispatch by anybody else is skipped before a runner or an approval request exists. |
| **What it may do** | build a **test artifact** — the store-credential-free **`preview`** profile of §8, and never the `internal` one — and nothing else. **Never `eas submit`.** D120's answer is explicit that *"CI at most builds a **test** artifact with no store credential"*, and that sentence is the ceiling: not a store build, not a submission, not a store credential. |
| **Permissions** | declared explicitly and minimally, at the workflow level **and** on the job: **`permissions:`** with **`contents: read`** and nothing else — no `id-token: write`, no `packages: write`, and never `permissions: write-all`. A build job needs the checkout and a token, not a write scope over the repository. |
| **Actions** | pinned, every one, to a **full 40-character commit SHA** with a `# vX.Y.Z` comment beside it, as every other workflow in this repository is (a tag is mutable; a commit is not). Before copying the file in, compare the pins against `main`'s, so the copy cannot downgrade one. |
| **Secrets** | reached **only** through the job-level `environment:` — `secrets.EXPO_TOKEN`, which is the **`mobile-release` environment secret of §9 and never a repository secret** (a repository secret is readable by any workflow in the repository; an environment secret only by a job that names the environment) — and no other secret, and **no literal** beside any reference: no value, no long base64 blob, no `password:` with something after it. |
| **The gate** | nothing here is in `scripts/release-gate.sh`, and it must not be: the app is outside the CLI tag's release gate (D125), and a gate that required a check no workflow runs would refuse every tag. |

**What is not in the shape, deliberately:** no `eas submit` step at any point
(this is Check 89's and your terminal's, always); no store credential, in any
form; no `mobile/eas.json` edit — the workflow reads the profiles, it does not
write them; and no `fugaro` command (nothing about the app is the CLI's).

**FACT for §10 (owner):** nothing, until the day you stage the file. When you
do, the file's staging commit, the environment it names and the pin comparison
are its own record, in `docs/pending-workflows/README.md`'s rows.

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
| 4 — the install | that the artifact **installs and runs** on a device, and reports the release's version (C6) | an install from a stale build, or a version that is not the tag's |
| 5 — the credential check | that no credential is tracked under `mobile/` and none is in CI | a key file added "for CI" and committed; an Expo token handed to an agent |

**What to paste back — Check 89's own list, and it is the whole of the record:**
the Expo account and the project id/slug; the `internal` profile's
`distribution`, its submit track and the **names** of any credential files it
references; both builds' ids, statuses, **credentials source** and
**gitCommitHash**; **both submission ids** and the tracks as the stores show
them; the install's device, OS and reported version; and the file list and
**the secret names** from step 5. **Never paste a signing key, a provisioning
profile's contents, an App Store Connect API key, a Play service-account JSON, a
password, an Expo token, or any credential value.**

**Where the paste goes.** The register's row 89 stays **NOT RUN** until it has
been run, and the `FACT` lines are the evidence that it was; the plan's rule is
that no task claims a live behaviour works until its check has been run. Until
then the app has no signed artifact and no store presence at all.

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
  owner to copy in** (**PR #582**) and is **not yet running**: `.github/workflows/ci.yml`
  has no shared-package job, and the plan's open item 4 says what that means —
  *"Until it exists, the shared package's tests run only where someone runs
  them, and that is a real gap in this plan's coverage."* When it lands it holds
  no store credential. §10's workflow is **described and does not exist**; if it is
  ever staged and copied in, its first tag run is what would verify it, and
  nothing before that does.
- **The CLI's release gate.** The app is outside it (D125), and no part of this
  runbook changes `scripts/release-gate.sh`.
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

**This section records mismatches between files on this tree, and — for the two
that have since been ruled — how. The rulings are not this runbook's: **the
owner rules**, and the first two were settled by P4.4's own files (**PR #675**)
and by P4.2a's amendment of the register's Check 89. The two that are still open
say so, and the ones that are settled say what changed on this page because of
them.**

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

**Tension 2 — `mobile/eas.json`, and what P4.4's `Files` line meant: SETTLED
(PR #675), and the file is in the tree.** The plan's P4.4 line says *"Files. None
in this repository."*, and Check 89's guard says `mobile/eas.json` is P4.4's
(*"P4.4 makes it; this check reads it, it does not create it"*). Read together
they were a contradiction — prose and a register that both claim to be the
authority for the same file. They are not one now: **`mobile/eas.json` is
committed**, with the three profiles §8 describes, and the guard's file test no
longer describes a checkout that lacks it as *today's* stop. Its own words,
verbatim, from the same section: *"Check 89 stopped: there is no mobile/eas.json
in this checkout, so there is no build or submit profile to read (P4.4 makes it;
this check reads it, it does not create it)"*. What remains true is the plain
reading of the guard's message: a checkout **older** than P4.4 has no profiles,
and the guard stops there rather than building against defaults nobody chose.
The plan's `Files` line is the plan's, and P4.4's files landed in a change of
their own; **this page does not amend the plan**, and §15 lists the file.

**Tension 3 — the version the app reports: SETTLED, and it is `expo.version`.**
C6 says the app is versioned with the CLI's tag; `mobile/app.json`'s `version`
is `0.0.0` today and P3.1's test pins it to `mobile/package.json`'s. The rule
that reconciles them is the one `mobile/eas.json` and `mobile/README.md` now
carry together: the **build number** (`expo.ios.buildNumber`,
`expo.android.versionCode`) is EAS's, kept on the project by
`cli.appVersionSource: "remote"` and raised by `autoIncrement: true` on
`internal` (§8) — **the build number is not the version the stores show** — and
the version the stores show, and the one Check 89's step 4 compares with `$TAG`,
is **`expo.version` in `mobile/app.json`**, which a release sets to the version
the tag names. `0.0.0` is the placeholder that says no release has been cut yet.
**FACT for §14 (owner):** that `expo.version` was set to the release's version
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
| [../../mobile/eas.json](../../mobile/eas.json) and [../../mobile/app.config.js](../../mobile/app.config.js) | the three build profiles, the submit track and `cli`'s `appVersionSource`/`requireCommit`; and the two identity variable names, the reverse-DNS patterns and what the dynamic config refuses (**P4.4**, merged as PR **#675**) — §8 is this page's account of both, and where they differ **the files win** |
| [../../mobile/README.md](../../mobile/README.md) | what the app is, where its two secrets live, and what is not verified |
| [../../.github/workflows/website-promote.yml](../../.github/workflows/website-promote.yml) and [../../.github/workflows/console-release.yml](../../.github/workflows/console-release.yml) | the two protected environments this repository has, read off the files themselves: `environment: website-live` in `website-promote.yml`'s `promote` job and `environment: console-release` in `console-release.yml`'s `release` job, each declared on a job. [../../.github/workflows/release.yml](../../.github/workflows/release.yml) is there too, as the counter-example: no environment, keyless signing (§2, §9) |
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

*Status of this document: written 2026-10-18, **never run**. It is the runbook
for Task P4.4 of [implementation-plan.md](implementation-plan.md); the check it
serves is **Check 89** in [../gcp-live-checklist.md](../gcp-live-checklist.md),
written by P4.2a and merged as PR **#580** on 2026-10-19, and **NOT RUN**. No agent performs any step of it.
Related: [README.md](README.md) · [reconciliation.md](reconciliation.md) ·
[../../mobile/README.md](../../mobile/README.md) ·
[../pending-workflows/README.md](../pending-workflows/README.md).*
