# Mobile telemetry: Firebase Crashlytics and Firebase Analytics in the app — implementation plan

**Status: a plan. Nothing here is built, nothing here is approved, and no code, no
configuration and no workflow is written by this change.** This document is the only
deliverable: it writes down how Firebase Crashlytics and Firebase Analytics reach the Expo
app at `mobile/`, so the owner can approve or refuse it in one pass. It implements nothing,
it changes no command, and it touches no rules file, no Terraform and no workflow.

**The ruling it is built on, and the date.** On **2026-10-19** the owner decided that the
mobile app **integrates Firebase Crashlytics and Firebase Analytics**, reporting to a
**Firebase project that Fugaro (the project) operates** and **not** the user's own
installation — the owner answered **YES** to *"shared with the developer is true: is that
intended"*. That ruling is recorded here as **D193** (answered) and it is the premise of
every task below. **It approves no task**, it allocates no phase, and it changes no other
approval in [mobile/implementation-plan.md](../mobile/implementation-plan.md).

**Three things this document deliberately does not claim.**

- **It is not an approval.** [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
  OWNER APPROVAL GATE is not passed for anything it names. The tasks below are **proposed**
  and each is **gated on the decision rows its "Depends on" line names**, exactly as Phase 6's
  tasks were gated on D160–D173 before the owner answered them.
- **It is not built, and no test here holds a behaviour.** Nothing in this plan exists: there
  is no `@react-native-firebase` dependency in `mobile/package.json`, no `mobile/app.config.js`,
  no `mobile/eas.json`, no `google-services.json` and no `GoogleService-Info.plist` anywhere in
  the tree. The one Go test this change adds
  (`internal/cli/docs_mobile_telemetry_test.go`) pins **this document's own claims** and
  nothing else.
- **It runs no live step and it holds no secret.** The Firebase project is created by the
  owner, the native config files are delivered by EAS from the owner's own secrets, and **no
  agent creates a Firebase project, runs `eas build`, `eas secret:create` or `eas submit`, or
  reads any credential** (Global Constraints 9 and 11 of the mobile plan, D120).

Related: [mobile/implementation-plan.md](../mobile/implementation-plan.md) (the plan this one
registers into, and Global Constraints 9, 10, 11 and 14) ·
[mobile/reconciliation.md](../mobile/reconciliation.md) §3 (D110–D129, in particular **D111**,
**D112**, **D120**, **D124**, **D125**) · [mobile/functional-spec-reconciliation.md](../mobile/functional-spec-reconciliation.md)
§3 (D160–D173, and **G18** — the Firebase configuration `fugaro init --firebase` already
records) · [design/push-notifications.md](../mobile/design/push-notifications.md) (the other
work that puts a *project the owner owns* in front of the phone, and the one whose §6 states
the privacy rule this plan inherits) · [design/voice-evaluation-switch.md](../mobile/design/voice-evaluation-switch.md)
§7 (the other place a third party would receive anything from the app) ·
[gcp-live-checklist.md](../gcp-live-checklist.md) (the check register) ·
[website/src/pages/privacy.astro](../../website/src/pages/privacy.astro) and
[website/data/site.yaml](../../website/data/site.yaml) (the site's analytics statement, D93) ·
[SECURITY.md](../../SECURITY.md).

---

## 1. What is in the tree today, and what this plan therefore builds on

Every fact below was read on this branch, whose base is `main` at `132374c7`. Nothing is
inferred from a plan.

| Fact | Where it was read | What it means here |
|---|---|---|
| The app is an Expo app at `mobile/`, its own artifact outside the Go module, with **no Firebase SDK wired at all** | `mobile/package.json` (`engines: node ">=22 <23"`, `npm ">=10"`, `test: node --test "src/**/*.test.js"`); `mobile/index.js` (the entry wires `expo-secure-store`, `expo-async-storage`, `react`, `react-native` — and nothing else) | The telemetry work is the **first** thing that would put a Firebase SDK in the app. It therefore meets the app's unbuilt Firebase wiring head-on, and §3's **D199** is that question. |
| `mobile/app.json` carries the **EAS project id** and nothing Firebase-shaped | `mobile/app.json` (`expo.extra.eas.projectId = "70e67d4a-77d3-4110-9db6-e600266db345"`) | The EAS project the native config files would be delivered into already exists. |
| **`mobile/app.config.js` and `mobile/eas.json` are not on `main`** — they are PR #675's, which the owner holds for the store path (P4.4, check 89) | `ls mobile/` on this base: `README.md`, `app.json`, `index.js`, `package-lock.json`, `package.json`, `src/` | Every task below that edits `app.config.js` or `eas.json` **depends on #675 merging first**, and says so. Neither file is written by this change. |
| The app's account tuple is **four fields and holds no connection value** | `mobile/src/accounts/account.js` — `ACCOUNT_FIELDS = ['id', 'firebaseProjectId', 'authUid', 'fugaroProject']` | There is no `rtdbURL` and no web API key in an account today. D199 is the row that has to add them, and §3 explains why. |
| The settings store **refuses an `AIza…` value outright**, and refuses the *word* `apiKey` as a key segment | `mobile/src/accounts/settings.js:107-114` (`SECRET_SHAPES`, `['google-api-key', /^AIza[0-9A-Za-z_-]{35}$/]`), `:147-150` (`judge` → `SECRET_VALUE`), `:245-260` (`FORBIDDEN_KEY_SEGMENTS`, `apiKey` among them) | **This is the repository rule the brief names**: the Firebase web API key appears nowhere the app stores or the app ships. It is why D199's recommendation puts the installation's connection values in **secure storage**, not in the tuple's settings entry. |
| The app's own config test already holds that rule for `app.json`, **with a planted `AIza…` value as its positive control** | `mobile/src/app-config.test.js:66` (the planted `expo.extra.firebase.apiKey`), `:71` (`['$.expo.extra.firebase.apiKey', 'google-api-key']`), `:98-99` (no `bundleIdentifier`, no `package`) | The fence exists and is mutation-proven. P7.1 extends it to the two **native** config files and to `app.config.js`; it does not invent a new rule. |
| The app's source is already scanned for a `firebase` SDK import, and the scan's pattern **does not match `@react-native-firebase/…`** | `mobile/src/mailbox/write-path.test.js:82-97`: rule 2 is `pattern: /['"]@?firebase\/[a-z-]+['"]/`, `allow: ['index.js']` | A screen could import `@react-native-firebase/analytics` today and no fence would catch it. P7.3 adds that rule; it is a **real gap**, not a hypothetical one. |
| The app writes to the database in exactly one file, and the fence is a scan with a positive control | `mobile/src/mailbox/write-path.test.js` (rules 1–3), `mobile/src/boundary.test.js` | The shape P7.3's allow-list scan copies, deliberately. |
| The app's first screen says **"No account yet. Add an installation to begin."** | `mobile/src/app/root.js` — `NO_ACCOUNT` | The first launch is an empty account list. A consent sheet has to be designed into it; §6 says where. |
| A redactor already exists and is shared | `shared/digest/index.js` (P1.3, merged) — `redact`, `REDACTED`, `redactFunc`, `redactForms` | §7's breadcrumb rule **binds** it; it does not write a second one. |
| **No CI job runs `mobile/`'s tests, and none runs `shared/`'s** | `.github/workflows/ci.yml` has four jobs — `changes`, `test`, `rules`, `terraform` — and none of them runs `npm test` in `mobile/` or `shared/`; `docs/pending-workflows/shared-tests.yml` is the staged job for `shared/` (P4.3, owner-made) | Every mobile-side verification below is a **local** `npm test`, and this plan says so rather than pretending CI holds it. A job for `mobile/` would be a **staged workflow**, and this change stages none (§13). |
| The store path is owner-run, and check 89 is the check that proves it | `docs/gcp-live-checklist.md:511` and `## Check 89` (`:5258`) — the internal track, no public release (C8, D120) | Telemetry changes what check 89's installation step can see, and it adds labels the stores ask for **before** a build is uploaded (§8). |
| The site's analytics statement is a **flag with two branches**, and the flag is ON | `website/src/pages/privacy.astro:50` (`site.facts.analytics.enabled`), `:61-84` (the on-branch: Google Analytics 4, one page view, no event, no user id, consent banner), `:118-120` (the off-branch: *"This site has no analytics at all"*); `website/data/site.yaml:78-99` (D93's block) | The page describes **the website**. It says nothing about the app today. §8 (D197) is what it must say, and when. |
| The site's own "nothing phones home" check is written and run by the owner | `docs/gcp-live-checklist.md` `## Check 69b` (`:505`, `:4067`) | It is about the **page**, not the app; the app's counterpart is a new check (§12), not an amendment of 69b. |

**`dev.fugaro.app`.** The brief calls the first MVP *"voice-less internal testing on
`dev.fugaro.app` for both stores"*. That host is **not in this tree** — the only `fugaro.app`
string is the app shell's root element name (`mobile/src/app/root.js` — `SHELL.ROOT`), which
is an element-type name and not a host — so the plan treats `dev.fugaro.app` as the owner's
name for the first internal build's environment and **does not build anything on it**
(§13, UNVERIFIED).

---

## 2. The numbers this plan takes

### 2.1 Decisions: **D193–D200**, and why those

The register of allocations is [mobile/README.md](../mobile/README.md)'s table. Read against
this base and against the tree's own last word on the subject
(`docs/coordinator/README.md:49-50`, which reads *"the highest number written anywhere in
`docs/`"* as **D192**, and *"**D193 onward** … **free**"*):

| Block | Holder | State |
|---|---|---|
| D1–D20 · D21–D38 · D39–D54b · D55–D78 · D90–D109 · D110–D129 · D130–D159 · D160–D179 | console, console v2, voice, managed projects, website, mobile, the credentials service, the functional spec v0.1 | taken; D174–D179 reserved and unused |
| **D180–D192** | the website redesign (`docs/website/redesign/reconciliation.md:48` — *"Decisions are numbered `D180` onward"*) | proposed; **D192 is the highest number written anywhere in `docs/`** |
| **D193–D200** | **this plan** | **taken by this document, and no decision number is taken twice** |

**D193–D200 is the next free block, and this document is where it is taken.** It takes
**eight** numbers — eight because this work has eight questions (§3) — and it takes them
**starting at the first free number**, not at a round one. It renumbers nothing: D180–D192
keep the numbers the redesign proposed, D174–D179 stay reserved and unused, and
**D79–D89 stay deliberately unused** (`docs/website/redesign/implementation-plan.md`: *"an
unused number is not a free number"*).

**D193 is already answered**, by the owner on 2026-10-19, and the row says so. **D194–D200
are recommendations with an empty answer column**, and the owner can approve all seven in one
pass (§3 is written to be read that way).

### 2.2 Checks: **105, 106 and 107**, and why those

The brief's instruction is *"new check numbers from the free block after 104"*. Read from the
register ([gcp-live-checklist.md](../gcp-live-checklist.md)):

- The **highest check section written** is `## Check 70c` (`:4708`). Checks **80–89** have
  summary rows in the register (80–89, written by mobile tasks P4.2a and P4.2) and **none of
  them has a section**.
- **90–99** are the credentials service's, **100–104** are the functional spec v0.1's
  (approved 2026-10-18, to be written by P6.11), and **105–109** are **reserved and unused**
  ([mobile/README.md](../mobile/README.md): *"**105–109 reserved and unused**"*).
- **110 is claimed by another open design** — the always-on orchestrator
  (`docs/coordinator/README.md:52`) — and the register's own rule is that *whichever of two
  open claims merges second renumbers itself*. **This plan does not touch 110.**

So this work takes **three** numbers out of the free block after 104 — **105, 106 and 107** —
and **leaves 108 and 109 free**. All three are **owner-run**, none is a CI job, and **none is
run by an agent**. §12 writes their text. **Check numbers 80–89 stay the mobile plan's**: this
work adds no row to that block, because a reserved number is never reused.

---

## 3. Owner decisions (D193–D200)

Eight rows. **D193 is the owner's answer of 2026-10-19**; the other seven are **this plan's
recommendations**, each with the alternative and what it costs, and each waits on the owner.
**Answering a row does not approve a task** — the plan's approval gate still stands.

| # | Question | Recommendation | The alternative, and what it costs | Owner's answer |
|---|---|---|---|---|
| **D193** | **Does the app carry Crashlytics and Analytics at all, and to whose Firebase project do they report?** The app serves **several installations** (D114), each with its own Firebase project, and the app signs in to that installation's Firebase with a **user** credential (D112, D111). "The app's analytics" is therefore ambiguous: the installation's project, or one the Fugaro project operates? | **Yes to both SDKs, and to one Firebase project that Fugaro (the project) operates** — not an installation's, not a user's. Reason: the question the telemetry answers is *"does the app work for people who are not the owner"*, and an installation's project can only answer it for that installation's owner; and no agent, and not the Fugaro owner, has any business reading a user's own project. | **In the installation's project.** Cost: the owner (and nobody else) can see the crashes, the data is the user's own project's, and *"shared with the developer"* is false for every install — the telemetry is useless for exactly the population it exists to measure. **Or no telemetry at all.** Cost: the app ships to two stores with no crash visibility, and the first real crash is reported by a user in prose, if at all. | **Owner answered 2026-10-19: yes to both SDKs, reporting to a Firebase project that Fugaro operates — not the user's own installation.** The owner answered **YES** to *"shared with the developer is true: is that intended"*. *Why:* the app is measured as one product, not per installation. **This row approves no task and allocates no phase.** |
| **D194** | **Who creates and operates the Fugaro-operated Firebase project, and who may be a member of it?** A Firebase project is a Google Cloud project under an organization; its data leaves the user's machine and lands in a project with a membership list. | **The owner creates it, alone, by hand, and the only member is the owner.** One project (suggested id `fugaro-telemetry`, under the same organization as the installations, with its own billing link), one Firebase **Analytics** property and one **Crashlytics** app per platform, **two members at most — the owner's own account** — and **no service account, no CI identity and no agent access**. The app id it registers reads: *the app reports crashes and a closed set of events to a Fugaro-operated project; nobody else can read them; nothing from the mailbox or a message is in them.* The project is created **before** P7.1 can produce a config file, because the config file is the project's own. | **Put telemetry in an installation's project** (`fugaro-dev`, say). Cost: crashes from every tester land in the development installation's project, mixed with its own budgets and runs, readable by every launcher and budget admin of that installation (D113's grants) — telemetry readable by a *run token* is not telemetry a user can be told about. **Or a third-party crash service.** Cost: a second vendor, a second DPA, a second retention question, and the owner has already ruled for Crashlytics (D193). **Or let an agent create it.** Cost: an agent holding project-creation authority in the owner's organization. | *(empty — owner)* |
| **D195** | **Consent and opt-out: is analytics collection OFF until the user opts in on first launch, or ON with an in-app switch and a link to `/privacy`? And is Crashlytics deferred the same way?** | **OFF until the user opts in, and Crashlytics deferred the same way** — one switch, both SDKs, nothing collected before it is on. The gate is a **first-launch sheet** carrying the switch, the sentence *"Fugaro sends anonymous usage and crash reports to Fugaro's own servers. No message, no mailbox and no account is ever sent. You can turn this off in Settings."*, and a link to `https://fugaro.dev/privacy/`. The choice is stored **on the device** only, and the sheet is shown again only from Settings. Reason: the user is in the EU (the owner's own brief says so), where reading or writing anything on a device that is not strictly necessary needs consent, and the SDKs write an app-instance id to the device the moment they start; and the site's own analytics already carries exactly this shape (D93, `website/src/pages/privacy.astro:61-84`), so the app and the site say the same thing in the same words. | **ON by default with a switch to turn it off** — the common shape and the one the stores expect. Cost: collection begins **before** the sheet is ever shown, so the first event, the app-instance id and any crash in the first seconds are collected without consent; in the EU that is the shape the site's own consent banner exists to avoid, and having the site ask and the app not is a contradiction a reader will notice. **Or opt-in for Analytics and always-on for Crashlytics** (crash reports are widely treated as a legitimate interest). Cost: a second consent story, a second paragraph in §8, and two switches where the brief asks for one. | *(empty — owner)* |
| **D196** | **What is sent, what is never sent, and what holds that line?** | **A closed allow-list of event names and parameter values, and a ban list that is checked by a scan** (§7). Nothing that names a message, a mailbox, an account, an installation or a repository is ever sent; the crash path is redacted through `shared/digest`'s `redact`; `setUserId`, `setUserProperty` and free-text custom keys are refused **outright** and a test fails on the call. Reason: the app's whole security model (D111–D113) is that a phone holds two items and writes two paths; a telemetry SDK that can carry free text is a third channel, and it is the only one that leaves the user's own infrastructure. | **Send everything the SDK collects by default and rely on the ban list being respected.** Cost: Firebase Analytics' default events carry an app-instance id and screen names, and Crashlytics' default reports carry the device model, the OS, log output and — unless it is switched off — the advertising id; "we never send message content" becomes an assertion about a library's defaults rather than about our code. **Or no allow-list, only review.** Cost: the first screen that logs a node path ships it, and no test notices. | *(empty — owner)* |
| **D197** | **The store labels and the privacy page: which categories, and when do the website's words change?** The owner has already ticked Play's Data safety form: **Crash logs · Diagnostics · Other app performance data · Device or other IDs · User IDs · Other in-app messages**. The site's privacy page (`website/src/pages/privacy.astro`) describes the website's GA4 and says nothing about an app; PR **#689** (not on this base) keeps the site free of an analytics SDK claim. | **Answer the labels from what the SDKs actually collect, and change the website's page in the same release that ships the SDKs — not before, and not after.** The ticked set stays as it is (§8 explains, category by category, which of the six the app's own traffic justifies), the **App Store** label is answered from the same list, and the site's privacy page gains **the app's paragraph** in the same commit as the first build that carries a telemetry SDK. Reason: a label written before the traffic has been read is a guess, and a privacy page that promises "no analytics SDK" while a store build carries one is a false statement on the owner's own domain. | **Change the website page first, then ship the SDKs.** Cost: between the two commits the site says the app sends telemetry and the app sends none — the safe direction, but the page must then be honest about a build that does not exist. **Or ship the SDKs and change the page later.** Cost: the exact falsehood the brief warns about, published on `fugaro.dev/privacy/` for as long as the gap lasts. **Or narrow the ticked set before shipping.** Cost: a form re-answered twice, against a policy risk in the direction that matters (under-declaring). | *(empty — owner)* |
| **D198** | **Which build carries the SDKs: the first, voice-less internal build on both stores, or the second?** | **The second build.** The first internal build ships **neither SDK**: its job is to prove the store path (P4.4, check 89), the development build, and the app's own wiring on a real device, and every one of those is easier to read without a native config plugin in the same change. The consent sheet, the allow-list scan, the redaction rule, the labels and the privacy page ship as **one unit** with the SDKs, and a first build that carried the SDKs without the website's paragraph would be D197's own failure mode, one release early. | **Crashlytics alone in the first build, Analytics in the second.** Cost: the labels and the privacy page are answered twice, and the first build still needs the consent gate (D195 defers both SDKs the same way), so the "one unit" is already split. **Or both in the first build.** Cost: the store path, the native project and the telemetry contract all change in one release, and a failure in any of the three is hard to attribute; it also puts the labels and the website paragraph under the first build's deadline. | *(empty — owner)* |
| **D199** | **Which Firebase project do the app's native config files name, and where does the app's *other* Firebase configuration come from?** A build carries **one** `google-services.json` and **one** `GoogleService-Info.plist`, and whichever project they name is the app's **default** Firebase app. The app serves several installations (D114), and the tuple it stores carries **no** connection values (`ACCOUNT_FIELDS`), while the settings store **refuses an `AIza…` value** (`settings.js:111`, `:150`). | **The native files name the Fugaro-operated telemetry project** (D194), so the **default app is the telemetry app** — the only project that can be compiled into one build serving every installation. **Every installation's Firebase becomes a named app** (`firebase.app(<account id>)`), built at runtime from the account. **The consequence is the owner's to accept, and it is the point of this row:** the tuple must gain the two connection values it does not carry today — the installation's Realtime Database URL and its web API key (`init_firebase.go` records both; G18 already reads them for the QR) — and they **cannot** live in the settings entry, because `SettingsStore` refuses that value shape. They therefore go to **secure storage**, which widens **D111**'s list from two items to **four**. D111's own cell says why this is a row and not a detail: *"D54b's precedent is to list every item held, not only the obvious one."* | **Name an installation's project in the native files.** Cost: one build serves one installation, which is exactly what D114 refuses (*"several, non-overlapping"*), and a second installation needs a second build and a second store submission. **Or exempt the `AIza…` shape in the settings store** so the tuple can carry it. Cost: it weakens a fence (`settings.js`) that protects the *whole* app, to hold a value the repository already documents as *not* a secret — the wrong trade: move the value, not the fence. **Or keep the default app as the installation's and give telemetry a named app** built from a JS config object. Cost: the JS config object is where the apiKey would have to be written — which is D193's rule broken in the one place the repository's own test (`app-config.test.js:66-71`) exists to catch. | *(empty — owner)* |
| **D200** | **Expo Go, the development build, and how the native config files reach a build.** `@react-native-firebase`'s config plugin changes the **native** project, which Expo Go cannot carry. | **The app's development workflow becomes a development build; Expo Go stops being a supported way to run it, and the change is stated in `mobile/README.md` in the same commit that adds the plugin.** The native config files are **never committed** and reach a build as **EAS file secrets** (§4.3), named in `eas.json` by environment variable; a CI/EAS build that cannot see them **fails loudly** rather than building an app with no Firebase. Reason: silently building without the config produces an app whose crash reporting is dead in a way nobody notices until a crash is missing — the worst possible failure for a telemetry feature. | **Keep Expo Go working by making the plugin optional.** Cost: two native projects, two code paths, and a plugin whose presence depends on how the app was started — the configuration matrix is where telemetry bugs live. **Or commit the native config files.** Cost: forbidden. They name the project, and `.gitignore` plus the scan in §4.3 exist to make that structural rather than a matter of discipline. | *(empty — owner)* |

---

## 4. How Firebase reaches an Expo app (Task P7.1)

### 4.1 The mechanism, named

`@react-native-firebase` is the supported path for Crashlytics in an Expo app, and it is a
**config plugin**: it is listed in the app's Expo config (`plugins`), it is applied when the
native project is generated (`npx expo prebuild`, or EAS's own prebuild), and it reads the
project's own `google-services.json` (Android) and `GoogleService-Info.plist` (iOS) at that
moment. The three consequences that decide half this plan:

1. **A development build is required.** Expo Go is a fixed binary that cannot carry the
   plugin, the native SDKs or the config files. D200 is that row.
2. **The native config files are what names the project.** No `apiKey` is written in
   JavaScript anywhere: the JS side asks the native module, which read the file. **That is
   why D199's recommendation is possible at all** — and why the alternative (a JS config
   object for the named app) would put an `AIza…` value in the app's source, which
   `mobile/src/app-config.test.js:66-71` refuses with a planted value today.
3. **The native files are per-build artifacts, not source.** They are generated in the
   Firebase console for a specific project, they name that project, and they carry its API
   key. §4.3 is how they are kept out of git and how a build gets them.

### 4.2 The flags, named exactly

The plan names every flag it depends on. Two of them are **compile-time** and two are the
**runtime** API; the compile-time pair is what makes "nothing is collected before consent"
true of the *native* SDK and not merely of our JavaScript.

| What it gates | Platform | The exact name | Value the plan requires |
|---|---|---|---|
| Analytics collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`firebase_analytics_collection_enabled`** | `false` |
| Analytics collection, native | iOS, `Info.plist` | **`FIREBASE_ANALYTICS_COLLECTION_ENABLED`** | `false` |
| Crashlytics collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`firebase_crashlytics_collection_enabled`** | `false` |
| Crashlytics collection, native | iOS, `Info.plist` | **`FirebaseCrashlyticsCollectionEnabled`** | `false` |
| Advertising-id collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`google_analytics_adid_collection_enabled`** | `false` |
| Analytics auto-collection, the React Native layer | `firebase.json` at the app root, under `react-native` | **`analytics_auto_collection_enabled`** | `false` |
| Crashlytics auto-collection, the React Native layer | `firebase.json` at the app root, under `react-native` | **`crashlytics_auto_collection_enabled`** | `false` |
| Analytics collection, runtime | JS, `@react-native-firebase/analytics` | **`setAnalyticsCollectionEnabled(true)`** | called **only** after the stored choice is on |
| Crashlytics collection, runtime | JS, `@react-native-firebase/crashlytics` | **`setCrashlyticsCollectionEnabled(true)`** | called **only** after the stored choice is on |

**These names are read from memory of the SDKs' own documentation and not from a page read
today; P7.1's first commit confirms each against the SDK's documentation and the generated
native project, and corrects this table in the same commit if any is wrong** (§14). What the
plan commits to is the *shape* — every switch that can gate collection is thrown to **off**
natively, and the app turns collection on at runtime and never off-and-on again.

### 4.3 The native config files are never committed, and how a build gets them

**The two paths are `mobile/google-services.json` and `mobile/GoogleService-Info.plist`** —
that is where the Firebase console's own output goes and where the plugin looks for it by
default. They are **never** committed. The mechanism is four parts, and every one of them is
a test or a guard rather than a sentence:

1. **`.gitignore`.** Both paths are ignored at the repository root's `.gitignore` and at
   `mobile/.gitignore` (whichever PR #675 leaves the app using — P7.1 adds the entries to the
   app's own ignore file so the rule travels with the app). **A path under `mobile/` that
   names `google-services` or `GoogleService-Info` is never tracked.**
2. **A scan with a positive control.** P7.1 extends `mobile/src/app-config.test.js` (whose
   "no credential, no installation" walk already exists, with a planted `AIza…` value as its
   control) with a **tracked-files** rule: for each of the two paths, the test fails if the
   file is tracked by git, and fails if it is **not** ignored. A planted entry in a temp
   index proves the rule can fail.
3. **A build-time guard that fails loudly.** `app.config.js` resolves the two file paths
   **from the environment** — `process.env.GOOGLE_SERVICES_JSON` and
   `process.env.GOOGLE_SERVICE_INFO_PLIST` — and a profile that carries neither is a **stop**,
   not a build. The guard's shape is check 89's own (`jq` reads the submit track *before*
   anything is built; a profile naming a public track is a stop the owner takes there), and
   its text says what is missing by **name** and never prints a value.
4. **EAS file secrets deliver them.** The owner creates them once, at their own terminal,
   with their own Expo account — **no agent runs this and no agent reads either file**:

   ```
   eas secret:create --scope project --name GOOGLE_SERVICES_JSON \
       --type file --value ./google-services.json
   eas secret:create --scope project --name GOOGLE_SERVICE_INFO_PLIST \
       --type file --value ./GoogleService-Info.plist
   ```

   and `eas.json`'s profiles name both in `env`, which is how the file secret becomes a path
   on the build machine. **Both mechanisms — file secrets, and EAS's newer environment
   variables with file visibility — are candidates; P7.1 picks the one EAS documents on the
   day it is written and records which** (§14).

**Why the file-secret route rather than a committed template.** A committed template either
carries the project's real apiKey (refused: `settings.js:111`, `app-config.test.js:66`) or
carries placeholders and produces a build whose crash reporting is silently dead (refused:
D200). The EAS file secret is the one route where the file exists **only** on the build
machine, is scoped to the project's own build profiles, and is never in a checkout, a
fixture, a log or a pull request — the same property `docs/pending-workflows/README.md`
describes for the jobs that must not hold material on every pull request.

---

## 5. The Fugaro-operated project (D194), as owner steps

Named plainly, because every one of them is the owner's and none is an agent's:

1. **Create the project.** A Firebase project (Google Cloud project) under the owner's
   organization, suggested id **`fugaro-telemetry`**, with its own billing link, created by
   hand in the console. *Owner step.*
2. **Register the two apps.** One Android app (the package id PR #675's `app.config.js`
   gives the store build) and one iOS app (its bundle identifier). *Owner step.*
3. **Download the two native config files.** They are the artifacts §4.3 delivers. They go to
   the EAS project's file secrets and **nowhere else**. *Owner step.*
4. **Turn on Analytics and Crashlytics** in the project, and note the property's **retention**
   setting — the plan requires the shortest retention the console offers, and D194's answer
   records which. *Owner step.*
5. **Membership: the owner alone.** No service account, no CI identity, no agent. The
   project's IAM is read by nobody else, and a run of this repository has no path to it. *Owner
   step.*
6. **Record the answer** — the project id, the property id, the two app ids, the retention —
   in the owner's own notes. **No id of this project is written into this repository**: the
   app gets it from the native config file at build time, and the repository does not need it
   (and a committed project id would be an invitation to commit the file beside it).

**What the app does *not* do with this project.** It never authenticates to it, never reads
it, never writes it and never lists it: it hands the native SDK a config at build time and
nothing more. **No RTDB rule, no Terraform and no `fugaro` command in this repository is
changed by any part of this plan** — the telemetry project is not an installation, holds no
Fugaro data, and no rules file mentions it.

---

## 6. Consent and opt-out (D195)

**The recommendation, in one sentence:** *nothing is collected until the user says yes on the
first launch, and one switch governs both SDKs.*

**Where the sheet is, in the tree.** `mobile/src/app/root.js` shows **"No account yet. Add an
installation to begin."** on a fresh install, and it is deliberately the only thing the shell
renders. The consent sheet is **a sibling of that screen**, not a modal over the mailbox: it
is shown **once**, before the account list is usable, its choice is stored **on the device**,
and after that it is reachable from the app's settings. Its logic is a **pure module**
(`shared/telemetry-consent/`, P7.2) with the same shape as every other shared module: the
stored choice and the two SDK flags are a **total function** of (stored value, platform), and
it is tested with no device.

**Why one switch and not two.** D195's recommendation gates **both** SDKs identically. The
alternative — Crashlytics always on, Analytics opt-in — costs a second consent story and a
second paragraph in §8's labels, and it is the shape that makes "we defer Crashlytics the
same way" (the brief's own words) untrue.

**The consequence the plan states rather than hides.** With the flags thrown to `false`
natively (§4.2), **a crash in the first seconds of the very first launch, before the sheet is
answered, is not reported** — the SDK is not collecting, so there is nothing to send. That is
the price of the recommendation and it is the honest one: the alternative collects it without
consent. After the user says yes, collection begins and later crashes on that device are
reported.

**The device's stored value is one boolean and it is not a credential.** It goes in the
**settings store** (`SettingsStore`), not secure storage: it is not secret, it is not
account-scoped, and it must be readable before any account exists. The **key** it is stored
under is `telemetry.consent` in the app's own namespace, and a test asserts it is **not** a
`secret.*` key, because that is the one way it could leak into D111's secure storage.

**The link.** The sheet and the settings screen both carry a link to
**`https://fugaro.dev/privacy/`**, opened **only** through P0.3's URL validator
(`shared/url/`, whose host allow-list is D124's answer) — never by string concatenation and
never as a raw `Linking.openURL` of a literal.

---

## 7. What is and is NOT sent (D196)

### 7.1 Never sent — the ban list

**No message content, no mailbox data, no installation identifiers, no secrets.** Written as
a list a test can hold:

- **No mailbox anything.** Not an inbox or outbox message's `text`, `id`, `sender`, `at`,
  `kind`, `agent`, `options`, `reply_to`, `mac` or `acked` value; not the heartbeat, not the
  presence state, not a prompt and not an answer.
- **No account or installation identifier.** Not `id`, not `firebaseProjectId`, not `authUid`,
  not `fugaroProject`; not the telemetry project's own id either (it is in the native config
  file, and an event parameter naming it would be a second place it lives).
- **No repository, run, slug, path or URL** from the feed — no `spend`, no cap, no kill state,
  no run id, no PR link, no file path, no host.
- **No secret and no secret-shaped value.** Not the Auth token, not the pairing MAC key, not
  the installation's web API key, not an EAS or store credential. The scan reuses
  `secretShaped` (`settings.js:123`), so the telemetry scan and the config scan cannot
  disagree about what a credential looks like.
- **No free text at all.** Every parameter value is a **boolean or an enum**, and every enum's
  members are in this plan's list.

### 7.2 Sent — the closed allow-list

**The event names.** Eight, and no ninth without a change to this list:

| Event | Parameters (closed sets) | Why it exists |
|---|---|---|
| `app_launch` | `first: bool` | Does the app start on a real device at all, and does it start again. |
| `consent_set` | `granted: bool` | Whether the sheet is answered and which way — the one number that says whether the sheet is understood. |
| `account_added` | `kind: 'manual' \| 'pairing'` | Whether an account is added at all, and by which path. |
| `account_switched` | — | Whether more than one account is ever used. |
| `screen_shown` | `screen: 'overview' \| 'jobs' \| 'mailbox' \| 'voice' \| 'text' \| 'settings'` | Which screens are reached; the enum is the whole screen set and a screen with no name in it cannot be reported. |
| `command_refused` | `reason: 'offline' \| 'unconfirmed' \| 'not_understood' \| 'refused'` | The only signal about voice that may leave, because it names no command. |
| `pairing_finished` | `outcome: 'ok' \| 'code_mismatch' \| 'expired' \| 'denied'` | Whether pairing works, and how it fails. |
| `crash_reported` | — | A marker event, so Crashlytics' own count and Analytics' count can be compared. |

**Nothing else.** No screen *title*, no account *name*, no agent *name*, no count of anything
(screens, messages, prompts, runs), no duration, no currency figure and no error string.

**The automatic events are not ours, and the plan says which ones it accepts.** Firebase
Analytics collects a set of events by itself (`first_open`, `session_start`, `app_update`, and
the app-instance id) and the plan **accepts them**, because they carry no Fugaro data — with
`google_analytics_adid_collection_enabled` and `analytics_auto_collection_enabled` (§4.2)
thrown to `false` so that nothing *additional* is collected automatically. **The rejected
alternative is listed in D196**: turning auto-collection on and relying on the ban list.

### 7.3 The redaction rule for breadcrumbs and custom keys

- **`crashlytics().setUserId`, `setUserProperty`, `setAttributes`, `setAttribute` with any
  free value: refused.** The scan in §7.4 fails on the call.
- **Custom keys are a closed set** — `screen` and `outcome`, with the same enum members as
  §7.2 — and **`setAttribute` is called only with a value read from that set**. A value that
  is not in the set is refused at the call site by the telemetry module, not merely by review.
- **Breadcrumbs are constants or they are redacted.** `crashlytics().log(...)` is called with
  a **constant string from the module's own list**; anything built from a runtime value passes
  through **`shared/digest`'s `redact` first** (P1.3, merged) — the same function the mailbox
  client uses before it writes or speaks anything (D118), so the app has **one** redaction rule
  and not two.
- **The crash report's own fields.** No `setUserId`, no account name, no installation id; the
  native report carries what the SDK carries by default (the device model, the OS version, the
  stack) and that is the set D197's labels are answered from.

### 7.4 The test that holds it

P7.3 adds **two** tests in `mobile/src/telemetry/`, both with positive controls, both in the
shape the tree already uses (`mobile/src/mailbox/write-path.test.js`,
`mobile/src/boundary.test.js`):

1. **The source scan.** Every `.js` under `mobile/` is walked (comments stripped, test files
   exempt, the scanner exempt from itself — the existing rule). The scan refuses:
   - any `logEvent(` whose **first string argument is not a name in §7.2's list**;
   - any `setUserId(`, `setUserProperty(`, or `setAttribute(`/`setAttributes(` call at all;
   - any `@react-native-firebase/…` import outside the telemetry module and the entry —
     **a rule the tree does not have today** (`write-path.test.js:82-97`'s pattern is
     `/['"]@?firebase\/[a-z-]+['"]/`, which does not match `@react-native-firebase/analytics`),
     and which P7.3 adds to `write-path.test.js`'s `RULES` array as **one deliberate
     allow-list entry**, the way P3.1's boundary test takes one;
   - any `crashlytics().log(` whose argument is not a constant in the module's list.
   Each rule is proved against **a planted line in real code** before the tree is judged, and
   the allow-list is proved non-vacuous the way `write-path.test.js`'s last test does it.
2. **The never-send test.** The telemetry module is driven with a **fixture account, fixture
   mailbox and fixture token**, and the events it produces are captured by a fake port: the
   test asserts that **not one value from the fixture** appears in any captured event, that
   every parameter is a boolean or a member of its enum, and that `secretShaped` returns null
   for every string the capture holds. A planted run that logs a message's `text` fails it.

**A lint that fails on any other analytics call** is exactly rule 1, and it is a test rather
than a lint config on purpose: the repository's fences are tests with positive controls
(`boundary.test.js`, `write-path.test.js`, `settings.test.js`), and a lint rule that lives in
a config file is a rule nobody proves can fail.

---

## 8. The store labels and the privacy page (D197)

### 8.1 Play — Data safety

The owner has already ticked six categories. Answering them from §7's allow-list, one by one:

| Play category (ticked) | What in this plan justifies it | Note |
|---|---|---|
| **Crash logs** | Crashlytics, by definition | Required; sent after consent only. |
| **Diagnostics** | Crashlytics' device and OS fields | Required. |
| **Other app performance data** | Crashlytics' performance traces and the `crash_reported` marker | Required. |
| **Device or other IDs** | The SDKs' own app-instance / installation id, **and nothing of Fugaro's** | Required; `google_analytics_adid_collection_enabled = false` removes the **advertising id**, not the SDK's own installation id, and the plan does not claim otherwise. |
| **User IDs** | **Nothing in this plan sends a user id** — `setUserId` is refused outright (§7.3) | **Over-declared on purpose.** The tick is kept: it is the safe direction, it costs nothing, and narrowing it is a change the owner may make once the first real traffic has been read (§8.3). |
| **Other in-app messages** | **Nothing in this plan sends one** — the mailbox is on the ban list (§7.1) | **Over-declared on purpose**, for the same reason. It is also the tick that matters most: if it is ever *justified* by real traffic, §7.1 has been broken. |

**The honest sentence for the plan to carry:** the ticked set is **wider** than what this plan
sends, in **two** categories, and that is a deliberate choice — under-declaring is the policy
risk, over-declaring is not — and no test can hold it, because no test can read the Play
console.

### 8.2 App Store — the privacy label

Answered from the same list, as **Data Not Linked to You** unless stated:

| Apple category | Type | Justification |
|---|---|---|
| **Crash Data** | Not Linked | Crashlytics |
| **Performance Data** | Not Linked | Crashlytics' performance data |
| **Diagnostics** → *Other Diagnostic Data* | Not Linked | the SDK's installation id |
| **Identifiers** → *Device ID* | Not Linked | the SDK's app-instance id |
| **Identifiers** → *User ID* | — | **not ticked**: `setUserId` is refused. Apple's form is answered per data type collected, and the app collects no user id. |
| **Usage Data** → *Product Interaction* | Not Linked | the eight events of §7.2, if the owner ships Analytics (D198's second build). If Analytics does not ship, this row is **not ticked**. |

**The label and Play's form are answered from one document** — this section — so the two
stores cannot be answered differently, which is the failure a reader would never catch.

### 8.3 The website — what changes, and when

**Today** `website/src/pages/privacy.astro` describes the **website**: with
`site.facts.analytics.enabled` on (`website/data/site.yaml:99`) it names Google Analytics 4,
one page view, no event and no user id, behind the site's consent banner; with the flag off it
says *"This site has no analytics at all"* (`:118-120`). **Neither branch says anything about
the app.** The site's "nothing phones home" check (`## Check 69b`) is a check about the
**page's network panel**, and it stays true: the page itself loads nothing new.

**What must change, in the same release that ships the SDKs** (D197's recommendation):

1. **The page gains the app's paragraph**, in both branches, saying what the app sends and
   what it never sends — §7.1's ban list in the page's own words, not in this plan's — and
   pointing at the in-app switch. In the flag-off branch the paragraph must **not** be hidden:
   the app's telemetry and the site's analytics are two different switches, and the reason the
   page has two branches today is exactly that they are not the same fact.
2. **The page's word ceiling is D188's** (a page's lead at most 40 words, a page at most 250,
   with `/docs/`, `/security/` and `/changelog/` excepted) and the privacy page is **at** its
   ceiling already (`docs/website/implementation-plan.md:653`: *"The page is at the ceiling: a
   sentence added there costs a sentence traded out"*). So the paragraph is paid for by trading
   words out, and the trade is made in the same commit by whoever owns the page then.
3. **PR #689 is not on this base.** If it lands first, this paragraph lands against *its* text
   and its own "no analytics SDK" claim is the sentence that changes. If it lands after, the
   owner applies the same paragraph to the merged page. **Either way the app's paragraph and
   the SDKs ship in one release, and the plan does not guess which PR carries it.**
4. **`website/data/site.yaml`'s `analytics` block is not touched** unless the paragraph needs
   a new fact from it: the block describes the *site's* GA4, and the app's telemetry is not a
   site fact.

**The website change is a task in this plan (P7.4), owned by whoever owns `website/` then**,
because the page's gates (`website/test/*.mjs`, the origin gate, the byte budgets) are the
website work's and this plan does not fork them.

---

## 9. Interaction with the voiceless MVP (D198)

The first MVP is **voice-less internal testing on both stores**. D198 recommends **the second
build** carries the SDKs, and the reason is structural rather than cautious:

- The first internal build's purpose is the **store path** — a signed EAS build reaching an
  internal track (P4.4, check 89) — and, per the brief, the *voice-less* app. A native config
  plugin changes the native project that path is proving; a telemetry failure and a store-path
  failure would land in the same release, and the store path is the one that cannot be redone
  cheaply.
- The consent sheet is a **first-launch screen**, and the first build's first launch is the
  screen `root.js` already defines. Designing the sheet *and* the store path *and* the app's
  first native plugin in one build is three firsts at once.
- **D197 makes it strictly better to wait:** the labels and the website paragraph must ship
  with the SDKs. In the first build they would have to be written before a single event has
  been observed, which is the guess D197 refuses.

**The alternative is D198's own second option** — *Crashlytics alone in the first build,
Analytics in the second* — and it is a reasonable owner answer if crash visibility on the
first testers matters more than the "one unit" property. Its cost is stated there: the labels
and the paragraph are written twice, and the consent gate is needed in the first build anyway.

**What does not change either way.** No rules file, no Terraform, no `fugaro` command and no
existing mobile task moves. Telemetry is **additive**: it is a new phase, and the voiceless
MVP's tasks keep their own ordering.

---

## 10. The tasks

*Five tasks, each independently mergeable, each with a reviewer tier and a verification that
runs with no device, no live cloud and no network. The phase is **proposed**: no task here may
start before the owner passes [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
approval gate for it, and each waits on the D-rows its line names.*

### Task P7.1: The config plugin, the native files, and the development build

**Files.** `mobile/app.config.js` (the plugin entry, the two `googleServicesFile` paths read
from the environment, the build-time guard), `mobile/eas.json` (the two `env` entries per
build profile — **the file arrives from PR #675**), `mobile/firebase.json` (the app-root
`react-native` block of §4.2), `mobile/.gitignore`, `mobile/package.json` (the three
`@react-native-firebase` dependencies), `mobile/README.md` (the Expo Go sentence D200 asks
for), and `mobile/src/app-config.test.js` (the tracked-file and ignore rules of §4.3, and the
extension of the existing credential walk to `app.config.js`).
**Depends on.** D199, D200, D194 (the project exists before its file can), and PR #675 (for
`app.config.js` and `eas.json`).
**Verification.** `cd mobile && npm test` — the extended `app-config.test.js`: both native
paths are untracked and ignored; `app.config.js` contains no `AIza…`-shaped value (planted
control); a build profile with no `GOOGLE_SERVICES_JSON` in its `env` is refused by the guard's
pure part; and the existing "no credential, no installation" walk still passes.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the change that
decides which project the app's default Firebase app is (D199), and it is the change that
would put a native config file in the tree if it were wrong.

### Task P7.2: The consent gate, the switch and the link

**Files.** `shared/telemetry-consent/` (new: the pure choice machine, the platform flag
table of §4.2 as data, and its tests), `mobile/src/telemetry/consent.js` (the app's sheet
logic, as a function of its ports), `mobile/src/app/root.js` (the sheet as the first screen's
sibling), the settings key `telemetry.consent`.
**Depends on.** P7.1, D195, D124 (the host allow-list P0.3's validator needs for the
`/privacy/` link).
**Verification.** `npm test` in `shared/` (the glob picks the module up) and `cd mobile && npm
test`: with no stored choice the two flags are **off** and no collection call is made; with
the choice on both runtime calls are made **once**; the choice is stored under a
**non-`secret.`** key and the store's own fence accepts it; the link is built through
`shared/url`'s validator and a host the allow-list does not name renders as text.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the only place
that turns collection on, and the only place a link leaves the app.

### Task P7.3: The allow-list, the redaction rule, and the scan

**Files.** `mobile/src/telemetry/` (new: the event module, the closed enum sets, the constant
breadcrumbs), `mobile/src/telemetry/analytics-calls.test.js` (the scan of §7.4 and its
controls), `mobile/src/telemetry/events.test.js` (the never-send test), and **one deliberate
entry** in `mobile/src/mailbox/write-path.test.js`'s `RULES` for the
`@react-native-firebase/…` import.
**Depends on.** P7.1, D196.
**Verification.** `cd mobile && npm test` — every rule of §7.4 with its planted positive
control, plus the existing `write-path.test.js` and `boundary.test.js` still green, which is
the proof that the one new allow-list entry did not widen anything else.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the fence that
makes "nothing that names a message leaves the phone" a test rather than a promise, and it is
the task that widens an existing scan's allow-list.

### Task P7.4: The labels and the website's paragraph

**Files.** `docs/plans/2026-10-19-mobile-telemetry.md` (§8 is its text — **this document is
its source**, and the task's deliverable is `website/src/pages/privacy.astro` and whatever
`docs/` records the two store answers), plus `website/`'s own gates if the paragraph's word
count moves (`website/test/budget.mjs` is the authority for the ceilings, D192's rule).
**Depends on.** P7.3 (the list the labels are answered from), D197, and PR #689 (whichever way
it lands).
**Verification.** `node --test` in `website/` (the site's own gates: the word ceilings, the
origin gate, the copy gate) — **and no Go test can see the Play console or App Store Connect,
so the two forms are answered by the owner and recorded in the owner's notes, not asserted
here.**
**Reviewer tier.** standard for the website change; **owner** for the two store forms, which
no agent fills in.

### Task P7.5: Checks 105–107, written into the register

**Files.** `docs/gcp-live-checklist.md`.
**Depends on.** P7.1, P7.2, P7.3 (a check is never run before its text is in the register).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, and the pin
`internal/cli/docs_mobile_telemetry_test.go` (this change) held to the new section's summary
rows.
**Reviewer tier.** standard for the writing; **owner** for the running. **No agent runs any
step of 105, 106 or 107.**

### 10.1 Order and levels

```
D193 (answered) ─┐
D199, D200 ──────┴─► P7.1 ──┬─► P7.2 ──┐
D194 (project) ─────────────┘          ├─► P7.4 ──► P7.6 (owner-run)
D195, D196 ────────────────────────────┤
P0.3 (shared/url, merged) ──► P7.2     │
                             P7.3 ─────┴─► P7.5
D197 ──────────────────────────────────────┘
PR #675 ──► P7.1            PR #689 ──► P7.4
```

| Level | Tasks | Reviewer | Files touched |
|---|---|---|---|
| **0** | **P7.1** the plugin, the native files, the development build | **SECURITY-SENSITIVE** | `mobile/app.config.js`, `mobile/eas.json`, `mobile/firebase.json`, `mobile/.gitignore`, `mobile/package.json`, `mobile/README.md`, `mobile/src/app-config.test.js` |
| **1** | **P7.2** the consent gate · **P7.3** the allow-list and the scan | **both SECURITY-SENSITIVE** | `shared/telemetry-consent/`, `mobile/src/telemetry/consent.js`, `mobile/src/app/root.js` · `mobile/src/telemetry/` (the event module and its two tests), one entry in `mobile/src/mailbox/write-path.test.js` |
| **2** | **P7.4** the labels and the website's paragraph | standard (website) / **owner** (the forms) | `website/src/pages/privacy.astro`, the website's gates |
| **3** | **P7.5** checks 105–107 written | standard | `docs/gcp-live-checklist.md` |
| **owner** | **P7.6** the checks run | owner | none |

**P7.2 and P7.3 are parallel** — different directories, and the only shared file between them
is none. **P7.4 waits on P7.3** because the labels are answered from the allow-list, not from
this plan's prose. **P7.5 waits on all three code tasks.** **No level contains two writers of
one file:** `mobile/src/telemetry/` is written by P7.2 (`consent.js`) and P7.3 (everything
else), and the plan splits them into **two files with two owners** rather than one directory
with two — the same rule the mobile plan's shared-files table states.

**The critical path is P7.1 → P7.3 → P7.4 → P7.5**, four deep, and it is short on purpose:
this phase is additive and nothing in the app's existing tasks waits on it.

### 10.2 What each task can and cannot verify

| The task | Can be verified without a device | Cannot |
|---|---|---|
| P7.1 | the tracked-file rules, the ignore rules, the `AIza…` walk, the guard's pure part | that the plugin applies, that a development build is produced, that the SDKs initialise — **`npx expo prebuild` and `eas build` need a toolchain this repository's CI does not have** |
| P7.2 | the choice machine, the flag table, the link's validator path | that the sheet renders, that the runtime enable calls reach the native SDK |
| P7.3 | the scan, the never-send test, the existing fences | that the *native* SDK collects nothing before consent — **only Check 105 can show that**, on a device |
| P7.4 | the website's own gates | the two store forms (owner) |
| P7.5 | the register's own docs tests | the three checks (owner) |

**`mobile/` has no CI job.** Every `npm test` above runs **where someone runs it** — the same
gap `docs/pending-workflows/README.md` records for `shared/` (whose job is staged and not
applied). A job for `mobile/` would be a **new staged workflow**, and **this change stages
none** (§13): the owner decides whether one is wanted when the first task lands.

---

## 11. What this plan does not change

- **No rules file.** `internal/budget/rules/rules.json.tmpl`, its golden and
  `internal/projectpolicy/version.go` are untouched: the telemetry project is not an
  installation, holds no Fugaro data, and no RTDB rule mentions it. **There is no rules change
  in this phase and no `RulesVersion` bump.**
- **No Terraform, no `fugaro` command, no Cloud Run job, no workflow.** The only live action
  in the whole phase is the owner creating the Firebase project (D194) and uploading two file
  secrets (§4.3).
- **No credential in the app beyond D111's list, widened only by D199 and only if the owner
  answers it as recommended.** The app's D111 items stay in platform secure storage; the
  consent boolean is deliberately **not** one of them (§6).
- **No `belong` anywhere.** No fixture, no project, no read.
- **No change to any existing mobile task's ordering.** Phase 3, Phase 6 and Phase 7 are
  independent, and the voiceless MVP is unaffected (D198).

---

## 12. Checks this plan takes (105, 106, 107)

**Three numbers out of the free block after 104, owner-run, none of them a CI job, and no
agent runs any step of any of them.** They are **written by P7.5 and run by the owner**, and
**a check is never run before its text is in the register**.

| # | The check | Task | Why it cannot be a test |
|---|---|---|---|
| **105** | **Nothing is collected before consent.** On a device, with both stores' internal builds, the app's first launch shows the consent sheet and **no request to any Firebase or Crashlytics origin appears** in the device's network panel (or in the Firebase console's realtime view) until the switch is turned on; after it is on, the app's own `app_launch` and `consent_set` events appear, and the console's debug view shows them arriving for **the Fugaro-operated project and no other**. The negative control is the one that matters: **turn the switch off again and no further event is collected.** | P7.5 writes it; **P7.6 runs it** | Needs a real device, a real store build, a real network and the Firebase console. No hermetic test can see a native SDK's own socket. |
| **106** | **A real crash reaches the Fugaro-operated project, with nothing that names anything in it.** Crash the app deliberately from the debug menu on a device whose consent is on; the report appears in Crashlytics under the Fugaro-operated project; **its breadcrumbs, its custom keys, its logs and its device fields contain no message text, no mailbox key, no account id, no `authUid`, no project id, no token and no MAC**; and `setUserId` is absent from the report. Then the second half: **with consent off, the same crash produces no report at all.** | P7.5 writes it; **P7.6 runs it** | Needs a real device, a real crash and the Crashlytics console — the redaction is a property of a report that only exists after a crash. |
| **107** | **The two store forms match what the app sends, and the page says it too.** The Play **Data safety** form and the App Store **privacy label** are the answers of §8.1 and §8.2, category by category, and **each ticked category is justified by traffic observed in 105 and 106** — with the two over-declared categories (`User IDs`, `Other in-app messages`) recorded **as** over-declared, in the owner's own words; and `https://fugaro.dev/privacy/` carries the app's paragraph (§8.3) **on the same day the first SDK-carrying build is submitted**. | P7.5 writes it; **P7.6 runs it** | Needs the two consoles, the live site and the owner's own accounts. |

**105, 106 and 107 are owner-run. 108 and 109 stay free and are not taken by this plan. 110
is another work's claim and is not touched.**

---

## 13. What this change edits, and what it does not

**This change is docs-only, plus one Go test that pins the documents.**

**Created:**

| Path | What it is |
|---|---|
| `docs/plans/2026-10-19-mobile-telemetry.md` | **This document — the plan, and the only deliverable of the work the brief asks for.** |
| `internal/cli/docs_mobile_telemetry_test.go` | The pin: this document's own claims — the D-block it takes (D193–D200) and the statement that it is the next free block, the check numbers it takes (105–107) and 108/109/110 left alone, the flag names it states exactly, the never-commit rule for the two native config files, and the registration of this plan in `docs/mobile/`'s two documents. It writes no code and asserts no behaviour of an app that does not exist. |

**Edited:**

| Path | The edit |
|---|---|
| `docs/mobile/README.md` | The index: one row for this plan in the file table, and a status sentence saying the plan exists, that it is **plan only**, that **D193 is answered** and **D194–D200 are open**, and that this work takes **D193–D200** and **checks 105–107**. The "numbers this work holds" table gains the two rows. |
| `docs/mobile/implementation-plan.md` | **Phase 7 — PROPOSED, not approved**: the phase summary row, the five tasks with their files, dependencies, verification and reviewer tier, the level table, and the check rows. |

**Not touched by this change:** every file under `mobile/`, `shared/`, `website/`, `internal/`
other than the new pin, every `docs/design/` document, `docs/gcp-live-checklist.md`, the
`docs/website/` documents, and **anything under `.github/workflows/`** — no workflow is
written by this change and none is staged in `docs/pending-workflows/`, because **no part of
this plan's first release needs one** (a CI job for `mobile/`'s tests would be a later,
separately-approved change, and §10.2 says so).

---

## 14. UNVERIFIED

The repository's habit is to say what could not be read rather than to smooth it over. Each
line below is a claim this plan makes **without** a source in this tree:

1. **The flag names of §4.2.** Read from memory of the SDKs' documentation, not from a page
   read on 2026-10-19. P7.1 confirms each against the SDK's own documentation and the
   generated native project, and corrects §4.2 in the same commit if any name is wrong. The
   *shape* the plan requires is independent of the spelling: every native switch is off and
   the runtime call is what turns collection on.
2. **EAS file secrets.** The `eas secret:create --type file` mechanism and the `eas.json`
   `env` reference of §4.3 are described from memory. **EAS also offers environment variables
   with file visibility**; P7.1 picks the one EAS documents on the day it is written, records
   which, and the plan's requirement is the same either way (the file exists only on the build
   machine).
3. **`@react-native-firebase`'s support for a second Firebase project in one binary.** D199's
   recommendation needs the **default** app to be the telemetry project and each installation
   to be a **named** app (`firebase.app(<account id>)`). The plugin's config, and how a named
   app is given a project the binary does not carry a native file for, are **not verified
   here**. P7.1's first commit reads the SDK's own documentation for it, and if the named-app
   route is unavailable, **D199's alternative is what the owner is asked to weigh** — the plan
   states both rather than assuming the mechanism.
4. **`mobile/app.config.js` and `mobile/eas.json`.** They are PR #675's and are **not on this
   base**; §4.3's guard and §10's P7.1 are written against the shape that PR is described to
   have, and P7.1 re-reads them at its own merge.
5. **`dev.fugaro.app`.** The brief's name for the first internal build's environment. It is
   not in this tree. Nothing in this plan is built on it, and no task names it as a file.
6. **The website's privacy page as PR #689 leaves it.** #689 is not on this base. §8.3 says
   what the paragraph must say and when it must land, and does not claim to know #689's final
   text.
7. **The two store forms' current contents.** The six Play categories are the owner's own
   description of what was ticked, not a reading of the console. §8.1 answers them from §7's
   allow-list and says which two are over-declared; **only Check 107 confirms what the console
   actually holds.**
8. **No latency, cost, retention or volume number.** Firebase Analytics and Crashlytics are
   free at this scale, and the plan states no figure it has not read. **D194's answer records
   the retention setting**; nothing else in this document is a measurement.
9. **Nothing here was verified by running anything.** No `npm test` was run for this change
   (the runner image has no Node: `node --version` → `command not found`), no `expo prebuild`
   ran, and no Firebase project exists. The plan's own pin
   (`internal/cli/docs_mobile_telemetry_test.go`) is the only executable thing this change
   adds, and it is run by the Go suite.

---

*Status of this document: a plan. Nothing is built, nothing is approved, and no code,
configuration or workflow is written by the change that adds it. **D193 is the owner's ruling
of 2026-10-19** (Crashlytics and Analytics, reporting to a Fugaro-operated Firebase project);
**D194–D200 are open for the owner, each with a recommendation**; this work takes
**D193–D200** — the next free decision block — and **checks 105, 106 and 107** out of the free
block after 104, leaving 108 and 109 free and 110 alone. Cheers: none of the three checks has
been run. Related: [mobile/README.md](../mobile/README.md) ·
[mobile/implementation-plan.md](../mobile/implementation-plan.md) ·
[gcp-live-checklist.md](../gcp-live-checklist.md).*
