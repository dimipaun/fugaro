# Mobile telemetry: Firebase Crashlytics and Firebase Analytics in the app — implementation plan

**Status: a plan. Nothing here is built, nothing here is approved, and no code, no
configuration and no workflow *of the app, the website, the CLI or CI* is written by this
change — the change is this document, the entries that register it, and one Go test that pins
them (`internal/cli/docs_mobile_telemetry_test.go`, stated in full in §13).** This document is
the deliverable: it writes down how Firebase Crashlytics and Firebase Analytics reach the Expo
app at `mobile/`, so the owner can approve or refuse it in one pass. It implements nothing,
it changes no command, and it touches no rules file, no Terraform and no workflow.

**The rulings it is built on, and the date.** On **2026-10-19** the owner decided that the
mobile app **integrates Firebase Crashlytics and Firebase Analytics**, reporting to a
**Firebase project that Fugaro (the project) operates** and **not** the user's own
installation — the owner answered **YES** to *"shared with the developer is true: is that
intended"*. That ruling is recorded here as **D193** (answered) and it is the premise of
every task below.

**The owner answered the rest of the block on the same day, and this document now records
the answers rather than the questions.** §3's *Owner's answer* column carries them one by
one: **D194** (the project is **`fugaro-prod`**, the project that already hosts the
website), **D195** (**always on** — no consent switch and no first-launch sheet, **against
this plan's opt-in recommendation**, which stays visible as the recommendation it was),
**D196**, **D197**, **D198** and **D200** (each as recommended), and **D199**, which the
owner **accepted** — the *telemetry* project's two native config files are **built into the
app** as EAS file secrets, while **each installation's own** Firebase connection cannot be,
because it differs per installation and is unknown at build time: it arrives in the
**encrypted pairing payload** and lives in the phone's **secure storage**. **That acceptance
amends D111's record** from two items per account to **four**, and §4.4 states the new record
item by item. §5, §6, §8.3, §9, P7.2 and §12 are rewritten to the answers.

**Answering a row is not approving a task, and this document says so wherever it could be
misread.** The answers approve nothing: no phase is allocated, no task of P7.1–P7.5 may
start, and the approval gate in
[mobile/implementation-plan.md](../mobile/implementation-plan.md) is unchanged — Phase 7
is still **PROPOSED, not approved**. **Every row it names is answered** — D199's acceptance
was the last one — and its step **7b** records that, while approving nothing.

**Three things this document deliberately does not claim.**

- **It is not an approval.** [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
  OWNER APPROVAL GATE is not passed for anything it names. The tasks below are **proposed**
  and each is **gated on the decision rows its "Depends on" line names**, exactly as Phase 6's
  tasks were gated on D160–D173 before the owner answered them.
- **It is not built, and no test here holds a behaviour.** Nothing this plan *adds* exists:
  there is no `@react-native-firebase` dependency in `mobile/package.json`, no
  `mobile/firebase.json`, no `mobile/src/telemetry/`, no `shared/telemetry-consent/`, no
  `google-services.json` and no `GoogleService-Info.plist` anywhere in the tree. **Two files
  this plan edits do exist**: `mobile/app.config.js` and `mobile/eas.json` are P4.4's, merged
  to `main` by **PR #675**, and P7.1 **modifies** them (§1's third table row, P7.1's "Files").
  The one Go test this change adds
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

Every fact below was read on this branch, whose base is `main` at **`8e3e810a`** — the branch's
**merge base** with `origin/main`, which is where the merge of 2026-10-19 left it. Nothing is
inferred from a plan. **Where the file a fact comes from is one this plan does not own, it is
cited by symbol or by phrase rather than by line** — the convention
[coordinator/README.md](../coordinator/README.md) states, and for the reason it gives: those
documents gain rows and paragraphs while this one sits in review, and a line number moves under
the sentence it was written for without anything looking wrong.

| Fact | Where it was read | What it means here |
|---|---|---|
| The app is an Expo app at `mobile/`, its own artifact outside the Go module, with **no Firebase SDK wired at all** | `mobile/package.json` (`engines: node ">=22 <23"`, `npm ">=10"`, `test: node --test "src/**/*.test.js"`); `mobile/index.js` (the entry wires `expo-secure-store`, `expo-async-storage`, `react`, `react-native` — and nothing else) | The telemetry work is the **first** thing that would put a Firebase SDK in the app. It therefore meets the app's unbuilt Firebase wiring head-on, and §3's **D199** is that question — **answered 2026-10-19, and the answer is §4.4's three-way split.** |
| `mobile/app.json` carries the **EAS project id** and nothing Firebase-shaped | `mobile/app.json` (`expo.extra.eas.projectId = "70e67d4a-77d3-4110-9db6-e600266db345"`) | The EAS project the native config files would be delivered into already exists. |
| **`mobile/app.config.js` and `mobile/eas.json` are on `main`** — P4.4's, merged by **PR #675** for the store path (check 89) | `ls mobile/`: `README.md`, `app.config.js`, `app.json`, `eas.json`, `index.js`, `package-lock.json`, `package.json`, `src/`; `mobile/eas.json` carries three build profiles — **`development`**, **`preview`** and **`internal`** — over the EAS environments `development`, `preview` and `production` | P7.1 **modifies both existing files**: the plugin entry and the build-time guard in `app.config.js`, the two `env` entries in each of `eas.json`'s three build profiles, and the Firebase entries in `mobile/.gitignore`. **Neither file is created by this change, and this plan no longer waits on #675.** |
| The app's account tuple is **four fields and holds no connection value** | `mobile/src/accounts/account.js` — `ACCOUNT_FIELDS = ['id', 'firebaseProjectId', 'authUid', 'fugaroProject']` | There is no `rtdbURL` and no web API key in an account today. **D199 (accepted 2026-10-19) is the row that puts them in secure storage rather than in the tuple**, and §3 and §4.4 explain why. |
| The settings store **refuses an `AIza…` value outright**, and refuses the *word* `apiKey` as a key segment | `mobile/src/accounts/settings.js` — `SECRET_SHAPES` (whose Google entry is `['google-api-key', /^AIza[0-9A-Za-z_-]{35}$/]`), `judge` (which returns `SECRET_VALUE` for a match) and **`FORBIDDEN_KEY_SEGMENTS`**, the frozen list `apiKey` is a member of. **Cited by symbol, not by line**: a line number into a file this plan does not edit goes stale silently, and the symbols are what a reader greps for. | **This is the repository rule the brief names**: the Firebase web API key appears nowhere the app stores or the app ships. It is why D199's accepted answer puts the installation's connection values in **secure storage**, not in the tuple's settings entry — and why the store's fence is left exactly as it is (§4.4). |
| The app's own config test already holds that rule for `app.json`, **with a planted `AIza…` value as its positive control** | `mobile/src/app-config.test.js` — the planted `expo.extra.firebase.apiKey` in its sample config, the `['$.expo.extra.firebase.apiKey', 'google-api-key']` entry in its walk's list, and the two assertions that `bundleIdentifier` and `package` are absent from `app.json`. **Cited by symbol, not by line**: P7.1 edits this file in this phase, so its line numbers move under anything written against them | The fence exists and is mutation-proven. P7.1 extends it to the two **native** config files and to `app.config.js`; it does not invent a new rule. |
| The app's source is already scanned for a `firebase` SDK import, and the scan's pattern **does not match `@react-native-firebase/…`** | `mobile/src/mailbox/write-path.test.js` — rule 2, whose `pattern` is `/['"]@?firebase\/[a-z-]+['"]/` with `allow: ['index.js']` | A screen could import `@react-native-firebase/analytics` today and no fence would catch it. P7.3 adds that rule; it is a **real gap**, not a hypothetical one. |
| The app writes to the database in exactly one file, and the fence is a scan with a positive control | `mobile/src/mailbox/write-path.test.js` (rules 1–3), `mobile/src/boundary.test.js` | The shape P7.3's allow-list scan copies, deliberately. |
| The app's first screen says **"No account yet. Add an installation to begin."** | `mobile/src/app/root.js` — `NO_ACCOUNT` | The first launch is an empty account list, and **D195's always-on answer leaves it exactly as it is**: there is no first-launch sheet to design in, and the app's statement about the reports lives in its settings surface instead (§6). |
| A redactor already exists and is shared | `shared/digest/index.js` (P1.3, merged) — `redact`, `REDACTED`, `redactFunc`, `redactForms` | §7's breadcrumb rule **binds** it; it does not write a second one. |
| **No CI job runs `mobile/`'s tests, and none runs `shared/`'s** | `.github/workflows/ci.yml` has four jobs — `changes`, `test`, `rules`, `terraform` — and none of them runs `npm test` in `mobile/` or `shared/`; `docs/pending-workflows/shared-tests.yml` is the staged job for `shared/` (P4.3, owner-made) | Every mobile-side verification below is a **local** `npm test`, and this plan says so rather than pretending CI holds it. A job for `mobile/` would be a **staged workflow**, and this change stages none (§13). |
| The store path is owner-run, and check 89 is the check that proves it | `docs/gcp-live-checklist.md` — the register's `| 89 |` row and its `## Check 89` section, cited by those anchors rather than by line: one row per check and one section per check, and both carry the number | Telemetry changes what check 89's installation step can see, and it adds labels the stores ask for **before** a build is uploaded (§8). |
| The site's analytics statement is a **flag with two branches**, and the flag is ON | `website/src/pages/privacy.astro` — the `site.facts.analytics.enabled` constant, the on-branch that names Google Analytics 4, one page view, no event, no user id and the consent banner, and the off-branch that says *"This site has no analytics at all"*; and `website/data/site.yaml`'s `analytics` block (D93's). **Cited by symbol and by phrase, not by line**: the page is edited by the website work, and P7.4 lands against it | **On this branch's base the page describes the website and says nothing about the app.** **On `origin/main` it does** — PR **#689** split it into *The website* and *The software* and added **The phone app**, and PR **#705** wrote D195's answer into it (*"There is no switch inside the app to turn them off"*, *"Earlier builds of the app send nothing at all"*) — **neither is on this base** (§2.2, §8.3, UNVERIFIED 6) |
| The site's own "nothing phones home" check is written and run by the owner | `docs/gcp-live-checklist.md` — the register's `| 69b |` row and its `## Check 69b` section, cited the same way | It is about the **page**, not the app; the app's counterpart is a new check (§12), not an amendment of 69b. |

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
(`docs/coordinator/README.md`'s *"The numbers this work holds"* table, which reads *"the
highest number written anywhere in `docs/`"* as **D192** and, at this base, *"**D193 onward** —
**free**"* — the row this change moves to **D201 onward**, since the block it gave away is the
one below):

| Block | Holder | State |
|---|---|---|
| D1–D20 · D21–D38 · D39–D54b · D55–D78 · D90–D109 · D110–D129 · D130–D159 · D160–D179 | console, console v2, voice, managed projects, website, mobile, the credentials service, the functional spec v0.1 | taken; D174–D179 reserved and unused |
| **D180–D192** | the website redesign (the redesign's own reconciliation says *"Decisions are numbered `D180` onward"*) | proposed; **D192 is the highest number written anywhere in `docs/`** |
| **D193–D200** | **this plan** | **taken by this document, and no decision number is taken twice** |

**D193–D200 is the next free block, and this document is where it is taken.** It takes
**eight** numbers — eight because this work has eight questions (§3) — and it takes them
**starting at the first free number**, not at a round one. It renumbers nothing: D180–D192
keep the numbers the redesign proposed, D174–D179 stay reserved and unused, and
**D79–D89 stay deliberately unused** (`docs/website/redesign/implementation-plan.md`: *"an
unused number is not a free number"*).

**All eight rows are answered**, by the owner on 2026-10-19, and each answer is written into
the row that carries it. The block reads, row by row:

- **D193: answered, owner 2026-10-19** — the ruling the plan is built on.
- **D194: answered, owner 2026-10-19** — the project is **`fugaro-prod`**.
- **D195: answered, owner 2026-10-19** — **always on**, against this plan's recommendation.
- **D196: answered, owner 2026-10-19** — as recommended.
- **D197: answered, owner 2026-10-19** — as recommended.
- **D198: answered, owner 2026-10-19** — the second build carries the SDKs.
- **D199: accepted, owner 2026-10-19** — the telemetry project's native config is **built
  into the app**; each installation's own Firebase connection arrives in the **pairing
  payload** and lives in **secure storage** (§4.4).
- **D200: answered, owner 2026-10-19** — as recommended.

**An answered row approves no task** — §3 says so, and the approval gate is unchanged.

### 2.2 Checks: **111, 112 and 113**, and why those

**The rule is the one §2.1 already applies to the decision numbers: an *unused* number is not a
free number.** A block another document records as **reserved** — even with nothing written in
it — is already spoken for, and the next writer takes the first number **after every claim**, not
the first gap in the register. The number is read from the register and from the two allocation
documents rather than remembered:

- **1–89 are taken.** 1–18 carry summary rows, 19–47 have written sections, 48–58 are the
  project-centric, artifact-runs and compute-backend blocks, 59 is the console's, 60–61 are
  owner-allocated (the budget-token channel and the monorepo folder export), 62–63 the console v2
  pair, 64–66 this voice work's, 67–68 managed projects', 69a–69c and 70a–70c the website's,
  **71–79 sit inside the website work's held range** with no check written in them, and **80–89
  are the mobile plan's, fully allocated**.
- **90–99 are the credentials service's** and **100–104 the functional spec v0.1's**, approved by
  the owner on 2026-10-18. **P6.11 writes 100–104**, and P6.11 is open as **PR #667**.
- **105–109 are *reserved and unused*** ([mobile/README.md](../mobile/README.md): *"**105–109
  reserved and unused**"*). **This plan takes none of them.** They are reserved, so "an unused
  number is not a free number" applies to them exactly as it applies to D79–D89 and D174–D179 —
  a plan that took 105 because the register has no `## Check 105` section would be reusing a
  number another work holds.
- **110 is claimed** by the always-on orchestrator design (open **PR #632**;
  `docs/coordinator/README.md`, its *"Check 110"* row), and the register's own rule is that
  *whichever of two open claims merges second renumbers itself*. **This plan does not touch 110.**

**So the first free number is 111**, and it is 111 **before and after #667 merges**: 105–109 stay
reserved and 110 stays claimed either way, so the block does not move. **This plan takes 111, 112
and 113** — three numbers, one per check, and **nothing above them** — leaving **114 onward
free**. All three are **owner-run**, none is a CI job, and **none is run by an agent**. §12 writes
their text. **Check numbers 80–89 stay the mobile plan's**: this work adds no row to that block,
because a reserved number is never reused.

**The highest check section written is a reading, and it is read rather than remembered.** The
highest check section written is `## Check 89` **at this reading** — `## Check 81`, `## Check 82`
and `## Check 89` are P4.2a's, and all of 80–89 carry summary rows — which is why the last written
section is 89 and not 70c. **P6.11 (#667) appends `## Check 100`–`## Check 104`**, which raises
that reading to 104 the day it merges, and **moves nothing this plan takes**: the block is chosen
from the claims above, not from the register's highest written number, so the reading can move
without the block moving. *(This paragraph said "the highest check section written is
`## Check 70c`" and that none of 80–89 had a section. That was false: `## Check 81`, `## Check 82`
and `## Check 89` were all written by P4.2a before this plan, and the coordinator's own table
carried the same stale reading, which this change corrects there too.)*

**The open pull requests this plan lands against.** Two, and each one edits lines this plan
edits: **#632** (the orchestrator's check 110 — this section and the coordinator's numbers table)
and **#667** (P6.11: it writes `## Check 100`–`## Check 104` and their summary rows into the
register, and edits the same check rows in `docs/coordinator/README.md` and `docs/mobile/README.md`
that this plan edits). **#667 is the reason this plan takes nothing out of 100–109** — that block
is the one its own pull request writes — and naming it beside the other is what keeps the two
changes from colliding whichever merges second.

**Two pull requests this plan named have merged since it was written, and one of them changes
what §8.3 and P7.4 have to say.** **#689** (the website's privacy statement, now in two parts —
the site and the software) and **#705** (*the app's analytics and crash reporting are always
on*, the owner's ruling written into that page) are **on `origin/main` and not on this
branch's base**: `origin/main` is at `66bbcf8d` and this branch's merge base with it is still
**`8e3e810a`**. So the sentence §8.3 carried — *the page changes in the same release that ships
the SDKs, not before* — is **no longer the fact**: the page **already carries the app's
paragraph**, it already says the reports are always on and that there is no switch, and it
already says *"Earlier builds of the app send nothing at all."* **The page is ahead of the
SDKs by design and says so**, and §8.3 and P7.4 are rewritten to hold the live page's
sentences true of the build that ships rather than to add them.

**One pull request this plan named has merged since it was written.** **#675**
(`mobile/app.config.js` and `mobile/eas.json` — P4.4's store path) is on `main` at this branch's
head, so **P7.1 modifies those two files in place** rather than depending on them: the plugin
entry, the build-time guard and the ignore entries in `app.config.js`, the two `env` entries in
each of `eas.json`'s three build profiles (§1's third table row, §4.3, P7.1). No task below waits
on #675, and the "Depends on" lines that used to say so now name the decision rows alone.

---

## 3. Owner decisions (D193–D200)

Eight rows, and the owner answered **all eight** on **2026-10-19**. **D193** is the ruling
the plan is built on; **D194**, **D195**, **D196**, **D197**, **D198**, **D199** and
**D200** carry the owner's answers in the right-hand column — **D195 against this plan's
recommendation**, **D199 accepted** (the owner asked *"firebase config should be built into
the app, no?"* and then took the recommendation), the rest as recommended, with each
superseded recommendation and each question left visible in the column it was written in
(D129's habit) rather than deleted. **Answering a row does not approve a task** — the plan's
approval gate still stands, Phase 7 is still proposed, and [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
step **7b** records that every row it names is answered while approving nothing.

| # | Question | Recommendation | The alternative, and what it costs | Owner's answer |
|---|---|---|---|---|
| **D193** | **Does the app carry Crashlytics and Analytics at all, and to whose Firebase project do they report?** The app serves **several installations** (D114), each with its own Firebase project, and the app signs in to that installation's Firebase with a **user** credential (D112, D111). "The app's analytics" is therefore ambiguous: the installation's project, or one the Fugaro project operates? | **Yes to both SDKs, and to one Firebase project that Fugaro (the project) operates** — not an installation's, not a user's. Reason: the question the telemetry answers is *"does the app work for people who are not the owner"*, and an installation's project can only answer it for that installation's owner; and no agent, and not the Fugaro owner, has any business reading a user's own project. | **In the installation's project.** Cost: the owner (and nobody else) can see the crashes, the data is the user's own project's, and *"shared with the developer"* is false for every install — the telemetry is useless for exactly the population it exists to measure. **Or no telemetry at all.** Cost: the app ships to two stores with no crash visibility, and the first real crash is reported by a user in prose, if at all. | **Owner answered 2026-10-19: yes to both SDKs, reporting to a Firebase project that Fugaro operates — not the user's own installation.** The owner answered **YES** to *"shared with the developer is true: is that intended"*. *Why:* the app is measured as one product, not per installation. **This row approves no task and allocates no phase.** |
| **D194** | **Who creates and operates the Fugaro-operated Firebase project, and who may be a member of it?** A Firebase project is a Google Cloud project under an organization; its data leaves the user's machine and lands in a project with a membership list. | **The owner creates it, alone, by hand, and the only member is the owner.** One project (suggested id `fugaro-telemetry`, under the same organization as the installations, with its own billing link), one Firebase **Analytics** property and one **Crashlytics** app per platform, **two members at most — the owner's own account** — and **no service account, no CI identity and no agent access**. The app id it registers reads: *the app reports crashes and a closed set of events to a Fugaro-operated project; nobody else can read them; nothing from the mailbox or a message is in them.* The project is created **before** P7.1 can produce a config file, because the config file is the project's own. | **Put telemetry in an installation's project** (`fugaro-dev`, say). Cost: crashes from every tester land in the development installation's project, mixed with its own budgets and runs, readable by every launcher and budget admin of that installation (D113's grants) — telemetry readable by a *run token* is not telemetry a user can be told about. **Or a third-party crash service.** Cost: a second vendor, a second DPA, a second retention question, and the owner has already ruled for Crashlytics (D193). **Or let an agent create it.** Cost: an agent holding project-creation authority in the owner's organization. | **Owner answered 2026-10-19: the project is `fugaro-prod`** — the owner's own name for it, **not this plan's suggested `fugaro-telemetry`** — and it is **the same Firebase / Google Cloud project that already hosts the website**. *Three facts the answer carries, which §5 states plainly because they are facts about this repository and not preferences:* the owner **creates the two Crashlytics and Analytics apps in it by hand**; **the only human member is the owner**; **no service account may be granted any Analytics or Crashlytics read**; and **the website's deploy service account does hold Firebase Hosting roles in that same project** — Hosting, and no read of analytics or crash data — which **the owner accepted** when answering. §5's owner steps are rewritten for `fugaro-prod`. **This row approves no task and allocates no phase.** |
| **D195** | **Consent and opt-out: is analytics collection OFF until the user opts in on first launch, or ON with an in-app switch and a link to `/privacy`? And is Crashlytics deferred the same way?** | **Collection defaults OFF until the user opts in — recommended: opt-in — and Crashlytics deferred the same way** — one switch, both SDKs, nothing collected before it is on. **The recommendation, not a ruling**: the owner has not yet answered opt-in against opt-out, and this row is where that choice is made. **One thing is not contingent on the answer**: the advertising identifier is collected under **neither** option — §4.2's switches are `false` in the build, Android declares no `AD_ID`, iOS links no `AdSupport`, and §7.4's test fails if that moves — because the website's privacy page promises *the app carries no advertising identifier* and a promise on the owner's own domain does not depend on a consent default. The gate is a **first-launch sheet** carrying the switch, the sentence *"Fugaro sends anonymous usage and crash reports to Fugaro's own servers. No message, no mailbox and no account is ever sent. You can turn this off in Settings."*, and a link to `https://fugaro.dev/privacy/`. The choice is stored **on the device** only, and the sheet is shown again only from Settings. Reason: the user is in the EU (the owner's own brief says so), where reading or writing anything on a device that is not strictly necessary needs consent, and the SDKs write an app-instance id to the device the moment they start; and the site's own analytics already carries exactly this shape (D93 — the same page's consent-banner branch, cited by symbol), so the app and the site say the same thing in the same words. | **ON by default with a switch to turn it off** — the common shape and the one the stores expect. Cost: collection begins **before** the sheet is ever shown, so the first event, the app-instance id and any crash in the first seconds are collected without consent; in the EU that is the shape the site's own consent banner exists to avoid, and having the site ask and the app not is a contradiction a reader will notice. **Or opt-in for Analytics and always-on for Crashlytics** (crash reports are widely treated as a legitimate interest). Cost: a second consent story, a second paragraph in §8, and two switches where the brief asks for one. | **Owner answered 2026-10-19: ALWAYS ON.** Collection is **on in the builds that carry the SDKs**; there is **no consent switch, no first-launch sheet, no stored choice and no `telemetry.consent` key**, and the app has nothing the user can turn off short of uninstalling it. **The answer SUPERSEDES the recommendation in the column to its left**, which stays visible as the recommendation it was; §6, P7.2, §9, §12 and every dependent sentence are rewritten to the answer. *Two things the answer does not change:* the **advertising-identifier ban holds under always-on exactly as it held under opt-in** — §4.2's two ad switches stay `false`, Android declares no `AD_ID`, iOS links no `AdSupport`, and §7.4's build-time test is what keeps all three true — and the **collection switches are now the collection**, so §4.2's table requires them `true` and §7.4 refuses the runtime calls that could act as a switch this ruling says does not exist. **The website's privacy statement already states both halves of the answer in its own words** — *"The app versions that carry these reports send them. There is no switch inside the app to turn them off"* and *"Earlier builds of the app send nothing at all."* (read on `origin/main`, PR **#705**, **not on this base**) — so the page and the ruling agree, ahead of the build. **This row approves no task and allocates no phase.** |
| **D196** | **What is sent, what is never sent, and what holds that line?** | **A closed allow-list of event names and parameter values, and a ban list that is checked by a scan** (§7). Nothing that names a message, a mailbox, an account, an installation or a repository is ever sent; the crash path is redacted through `shared/digest`'s `redact`; `setUserId`, `setUserProperty` and free-text custom keys are refused **outright** and a test fails on the call. Reason: the app's whole security model (D111–D113) is that a phone holds two items and writes two paths; a telemetry SDK that can carry free text is a third channel, and it is the only one that leaves the user's own infrastructure. | **Send everything the SDK collects by default and rely on the ban list being respected.** Cost: Firebase Analytics' default events carry an app-instance id and screen names, and Crashlytics' default reports carry the device model, the OS, log output and — unless it is switched off — the advertising id; "we never send message content" becomes an assertion about a library's defaults rather than about our code. **Or no allow-list, only review.** Cost: the first screen that logs a node path ships it, and no test notices. | **Owner answered 2026-10-19: as recommended.** The closed allow-list of §7.2, the ban list of §7.1, the redaction rule through `shared/digest`'s `redact` (§7.3), and `setUserId`, `setUserProperty`, `setAttributes`/`setAttribute` and free-text custom keys **refused outright** by the scan of §7.4. **This row approves no task and allocates no phase.** |
| **D197** | **The store labels and the privacy page: which categories, and when do the website's words change?** The owner has already ticked Play's Data safety form: **Crash logs · Diagnostics · Other app performance data · Device or other IDs · User IDs · Other in-app messages**. The site's privacy page (`website/src/pages/privacy.astro`) describes the website's GA4 and says nothing about an app; PR **#689** (not on this base) keeps the site free of an analytics SDK claim. | **Answer the labels from what the SDKs actually collect, and change the website's page in the same release that ships the SDKs — not before, and not after.** The ticked set stays as it is (§8 explains, category by category, which of the six the app's own traffic justifies), the **App Store** label is answered from the same list, and the site's privacy page gains **the app's paragraph** in the same commit as the first build that carries a telemetry SDK. Reason: a label written before the traffic has been read is a guess, and a privacy page that promises "no analytics SDK" while a store build carries one is a false statement on the owner's own domain. | **Change the website page first, then ship the SDKs.** Cost: between the two commits the site says the app sends telemetry and the app sends none — the safe direction, but the page must then be honest about a build that does not exist. **Or ship the SDKs and change the page later.** Cost: the exact falsehood the brief warns about, published on `fugaro.dev/privacy/` for as long as the gap lasts. **Or narrow the ticked set before shipping.** Cost: a form re-answered twice, against a policy risk in the direction that matters (under-declaring). | **Owner answered 2026-10-19: as recommended** — the two store forms are answered from what the SDKs actually collect (§8.1, §8.2), and the website's words move with the release that ships the SDKs. **One fact has moved under that answer, and the plan records it rather than smoothing it over:** the privacy statement **already carries the app's paragraph and already says the reports are always on** (read on `origin/main`, PRs **#689** and **#705**, neither on this base), so **the page is AHEAD of the SDKs by design and says so itself** (*"Earlier builds of the app send nothing at all."*). §8.3 is therefore rewritten from *"change the page in the same release"* to *"hold the live page's sentences true of the build that ships"*, and P7.4 follows §8.3. **This row approves no task and allocates no phase.** |
| **D198** | **Which build carries the SDKs: the first, voice-less internal build on both stores, or the second?** | **The second build.** The first internal build ships **neither SDK**: its job is to prove the store path (P4.4, check 89), the development build, and the app's own wiring on a real device, and every one of those is easier to read without a native config plugin in the same change. The consent sheet, the allow-list scan, the redaction rule, the labels and the privacy page ship as **one unit** with the SDKs, and a first build that carried the SDKs without the website's paragraph would be D197's own failure mode, one release early. | **Crashlytics alone in the first build, Analytics in the second.** Cost: the labels and the privacy page are answered twice, and the first build still needs the consent gate (D195 defers both SDKs the same way), so the "one unit" is already split. **Or both in the first build.** Cost: the store path, the native project and the telemetry contract all change in one release, and a failure in any of the three is hard to attribute; it also puts the labels and the website paragraph under the first build's deadline. | **Owner answered 2026-10-19: the SECOND build carries the SDKs; the first internal build ships neither**, as recommended. §9 says what that means now that D195 is always-on: the first build's job is still the store path, and it carries **no** telemetry and **no** native config plugin. **This row approves no task and allocates no phase.** |
| **D199** | **Which Firebase project do the app's native config files name, and where does the app's *other* Firebase configuration come from?** A build carries **one** `google-services.json` and **one** `GoogleService-Info.plist`, and whichever project they name is the app's **default** Firebase app. The app serves several installations (D114), and the tuple it stores carries **no** connection values (`ACCOUNT_FIELDS`), while the settings store **refuses an `AIza…` value** — `SECRET_SHAPES`'s `google-api-key` entry, applied by `judge`'s `SECRET_VALUE` branch. | **The native files name the Fugaro-operated telemetry project** (D194), so the **default app is the telemetry app** — the only project that can be compiled into one build serving every installation. **Every installation's Firebase becomes a named app** (`firebase.app(<account id>)`), built at runtime from the account. **The consequence was the owner's to accept, and it is the point of this row — the owner accepted it on 2026-10-19:** the tuple must gain the two connection values it does not carry today — the installation's Realtime Database URL and its web API key (`init_firebase.go` records both; G18 already reads them for the QR) — and they **cannot** live in the settings entry, because `SettingsStore` refuses that value shape. They therefore go to **secure storage**, which widens **D111**'s list from two items to **four**. D111's own cell says why this is a row and not a detail: *"D54b's precedent is to list every item held, not only the obvious one."* | **Name an installation's project in the native files.** Cost: one build serves one installation, which is exactly what D114 refuses (*"several, non-overlapping"*), and a second installation needs a second build and a second store submission. **Or exempt the `AIza…` shape in the settings store** so the tuple can carry it. Cost: it weakens a fence (`settings.js`) that protects the *whole* app, to hold a value the repository already documents as *not* a secret — the wrong trade: move the value, not the fence. **Or keep the default app as the installation's and give telemetry a named app** built from a JS config object. Cost: the JS config object is where the apiKey would have to be written — which is D193's rule broken in the one place the repository's own test (the planted `apiKey` control in `mobile/src/app-config.test.js`) exists to catch. | **Owner answered 2026-10-19: ACCEPTED.** The row was first answered with a question — *"firebase config should be built into the app, no?"* — and the question and its clarification are kept visible below (D129's habit); **the owner then accepted the recommendation**, consequence and all, and this cell is the record. **What the answer settles, in three parts — §4.4 states it item by item, and D111's record carries the amendment:** **(1) Built into the app:** the *telemetry* project's two native config files, naming **`fugaro-prod`** (D194), delivered as **EAS file secrets** and applied at prebuild (§4.3, D200), so the default Firebase app **every** build carries **is** the Fugaro project's. **(2) In the pairing payload:** **each installation's own** Firebase connection — its Realtime Database URL and its web API key. They differ per installation and **are not known at build time**, so no build can carry them: they arrive **inside the encrypted pairing payload from the user's own installation** (the CLI records both, `init_firebase.go`; G18 already reads them for the QR) and travel under **D113b**'s pairing key. **(3) In secure storage:** those same two values, per account, joined to D111's **ID token** and **per-pairing MAC key** — **four items per account**, never in git, never a fixture, never the settings entry, never a log line. **The consequence the owner accepted with the answer:** **D111's *two items per account* is now four**, and the settings store's refusal of API-key-shaped values protects **less than it did** — `SECRET_SHAPES` is **not** relaxed and a settings entry may still never carry an `AIza…` value, but this plan's stronger sentence, that the app stores no such value *anywhere*, no longer holds once the installation's own key lives in secure storage by design. **What the answer does not do:** it approves no task, and it does not settle whether the *product* uses the value — the app still wires **no** Firebase SDK until a task is approved. **The row is ANSWERED, and P7.1's *Depends on* line names a ruling rather than a question.** |
| **D200** | **Expo Go, the development build, and how the native config files reach a build.** `@react-native-firebase`'s config plugin changes the **native** project, which Expo Go cannot carry. | **The app's development workflow becomes a development build; Expo Go stops being a supported way to run it, and the change is stated in `mobile/README.md` in the same commit that adds the plugin.** The native config files are **never committed** and reach a build as **EAS file secrets** (§4.3), named in `eas.json` by environment variable; a CI/EAS build that cannot see them **fails loudly** rather than building an app with no Firebase. Reason: silently building without the config produces an app whose crash reporting is dead in a way nobody notices until a crash is missing — the worst possible failure for a telemetry feature. | **Keep Expo Go working by making the plugin optional.** Cost: two native projects, two code paths, and a plugin whose presence depends on how the app was started — the configuration matrix is where telemetry bugs live. **Or commit the native config files.** Cost: forbidden. They name the project, and `.gitignore` plus the scan in §4.3 exist to make that structural rather than a matter of discipline. | **Owner answered 2026-10-19: yes, as recommended.** The app's development workflow becomes a **development build** and **Expo Go stops being a supported way to run the app**, stated in `mobile/README.md` in the same commit that adds the plugin; the two native config files are **never committed** and reach a build as **EAS file secrets**; and a build that cannot see them **fails loudly** rather than shipping an app with no Firebase. **This row approves no task and allocates no phase.** |

---

## 4. How Firebase reaches an Expo app (Task P7.1)

### 4.1 The mechanism, named

`@react-native-firebase` is the supported path for Crashlytics in an Expo app, and it is a
**config plugin**: it is listed in the app's Expo config (`plugins`), it is applied when the
native project is generated (`npx expo prebuild`, or EAS's own prebuild), and it reads the
project's own `google-services.json` (Android) and `GoogleService-Info.plist` (iOS) at that
moment. The three consequences that decide half this plan:

1. **A development build is required.** Expo Go is a fixed binary that cannot carry the
   plugin, the native SDKs or the config files. **D200 is that row, and the owner answered it
   yes on 2026-10-19** — the app's development workflow becomes a development build, and
   `mobile/README.md` says so in the same commit that adds the plugin.
2. **The native config files are what names the project.** No `apiKey` is written in
   JavaScript anywhere: the JS side asks the native module, which read the file. **That is
   why D199's accepted answer is possible at all** — and why the alternative (a JS config
   object for the named app) would put an `AIza…` value in the app's source, which
   the planted `apiKey` control in `mobile/src/app-config.test.js` refuses today.
3. **The native files are per-build artifacts, not source.** They are generated in the
   Firebase console for a specific project, they name that project, and they carry its API
   key. §4.3 is how they are kept out of git and how a build gets them. **They ARE built into
   the app** — that is what the plugin does, and it is the answer to the owner's question on
   D199: the *telemetry* project's config is compiled in. **An installation's own** Firebase
   connection is the one thing that cannot be, and D199 is where that was decided — **the
   owner accepted it on 2026-10-19**, and §4.4 states the resulting record item by item.

### 4.2 The flags, named exactly

The plan names every flag it depends on. There are **two groups** and they answer different
questions: the **four switches that decide *what*** the SDKs may collect — the two ad-ID ones
are thrown to `false` and stay there under D195's always-on answer, exactly as they were
thrown to `false` under the opt-in recommendation this plan made and the owner did not take —
and the **two switches that decide *whether*** they collect at all, which D195's answer puts
**on**. The second group is the half that changed when the owner answered: this table used to
require all four collection switches `false` so that "nothing is collected before consent" was
true of the *native* SDK and not merely of our JavaScript, and **there is no consent to wait
for now**, so the app's builds collect from the first launch and the table says so. The ad-ID
half is unaffected, and §7.4's build-time test holds both halves.

| What it gates | Platform | The exact name | Value the plan requires |
|---|---|---|---|
| Analytics collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`firebase_analytics_collection_enabled`** | `true` — **D195's always-on answer**: the build that carries the SDK collects |
| Analytics collection, native | iOS, `Info.plist` | **`FIREBASE_ANALYTICS_COLLECTION_ENABLED`** | `true`, the same answer |
| Crashlytics collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`firebase_crashlytics_collection_enabled`** | `true`, the same answer |
| Crashlytics collection, native | iOS, `Info.plist` | **`FirebaseCrashlyticsCollectionEnabled`** | `true`, the same answer |
| Advertising-id collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`google_analytics_adid_collection_enabled`** | `false` |
| Ad-personalisation signals, native | Android, `AndroidManifest.xml` `<meta-data>` | **`google_analytics_default_allow_ad_personalization_signals`** | `false` |
| Ad-ID support, linked at all | iOS, the Analytics pod `@react-native-firebase/analytics` makes CocoaPods link | **the Analytics variant with no ad-ID support** — the app links **no `AdSupport` framework** at all. **The pod/plugin option's exact name is to VERIFY** (§14, and the paragraph after this table) | ad-ID support **not linked** |
| Advertising-id permission, declared at all | Android, `AndroidManifest.xml` `<uses-permission>` | **`com.google.android.gms.permission.AD_ID`** — **removed** from the merged manifest at prebuild (`tools:node="remove"`) | absent (not declared) |
| Analytics auto-collection, the React Native layer | `firebase.json` at the app root, under `react-native` | **`analytics_auto_collection_enabled`** | `true`, the same answer |
| Crashlytics auto-collection, the React Native layer | `firebase.json` at the app root, under `react-native` | **`crashlytics_auto_collection_enabled`** | `true`, the same answer |
| Analytics collection, runtime | JS, `@react-native-firebase/analytics` | **`setAnalyticsCollectionEnabled(…)`** | **never called** — and **refused by §7.4's scan**. A call that can set collection state *is* the switch D195 says the app does not have, whether it is called with `true` or with `false` |
| Crashlytics collection, runtime | JS, `@react-native-firebase/crashlytics` | **`setCrashlyticsCollectionEnabled(…)`** | **never called**, refused the same way, for the same reason |

**These names are read from memory of the SDKs' own documentation and not from a page read
today; P7.1's first commit confirms each against the SDK's documentation and the generated
native project, and corrects this table in the same commit if any is wrong** (§14). What the
plan commits to is the *shape*, and D195's answer is what fixes it: the app's builds collect,
so **every collection switch is on and no runtime call turns collection on or off**, while
**every ad-ID switch is off** and stays off. The two halves are held by the same test (§7.4),
which is why a table that reads `true` six times — the four native collection switches and the
two `firebase.json` auto-collection switches — and `false` twice, for the two ad-ID switches,
is one requirement and not two.

**The app carries no advertising identifier, and that is a requirement here, not a hope.** The
website's privacy page promises it — *the app carries no advertising identifier* is the sentence
the owner's brief gives for the page (§8.3, and PR **#689**'s paragraph applied there) — and a
promise on the owner's own domain is only true while something holds it. So the plan makes it
structural, three ways, and none of them is a sentence in a policy document:

1. **Android, the switch.** `google_analytics_adid_collection_enabled` and
   `google_analytics_default_allow_ad_personalization_signals` are **`false`** in the app's
   manifest meta-data, written by the config plugin (P7.1), so Analytics neither reads the
   advertising id nor uses it to personalise ads.
2. **Android, the permission.** The switches stop the SDK *reading* the id; they do not by
   themselves stop the merged manifest from **declaring**
   `com.google.android.gms.permission.AD_ID`, which is what the Play Console reads as an
   advertising-id app. P7.1 therefore **removes the permission at prebuild**
   (`<uses-permission android:name="com.google.android.gms.permission.AD_ID" tools:node="remove"/>`,
   or the plugin's own option for it if the plugin has one — **VERIFY** which, per §14).
3. **iOS, no ad-ID support linked at all.** The app links the Firebase Analytics variant that
   **does not support the advertising identifier**, so the `AdSupport` framework is not in the
   binary and there is no id for a later misconfiguration to read. The exact pod or plugin option
   that selects it — the `@react-native-firebase/analytics` podspec's variant, or the config
   plugin's iOS option — is **to VERIFY against `@react-native-firebase`'s own documentation**,
   and the plan says so rather than naming one from memory (§14). **How it is verified**, at
   P7.1's first commit and before the first store build: read the pinned version's
   `node_modules/@react-native-firebase/analytics` podspec and `node_modules/@react-native-firebase/app`
   config-plugin source for the option, apply it, run `npx expo prebuild`, and then read the
   generated `ios/Podfile.lock` for `FirebaseAnalytics` and for the **absence** of `AdSupport`
   (and read the Android manifest for the absent `AD_ID` permission, point 2). If the SDK turns
   out to offer no such option, the fallback is the pod the docs name for the ad-ID-free
   variant, and if there is none, **the promise D195 keeps is not buildable as written** — the
   advertising-identifier ban survives the owner's always-on answer and is not contingent on it
   — which is a finding for the owner, not a sentence to soften here.

**The build-time test (§7.4's third test, P7.1).** A Node test in the app fails the build if
**either** the generated native config **or** the plugin options enable ad-ID collection: the
Android manifest must carry the two collection switches as `true`, the two ad switches as
`false` and must not declare the `AD_ID` permission, and the iOS side must not link
`AdSupport`. It is written the way the app's other
fences are — with a **planted control**, a copy of the generated files with the switch flipped
to `true`, so the rule is proved able to fail before the real files are judged. **On a machine
with no prebuilt native project the test checks the plugin options and `mobile/firebase.json`
and says which half it could not see**, rather than passing quietly: the same rule §4.3's
tracked-file test follows.

### 4.3 The native config files are never committed, and how a build gets them

**The two paths are `mobile/google-services.json` and `mobile/GoogleService-Info.plist`** —
that is where the Firebase console's own output goes and where the plugin looks for it by
default. They are **never** committed. The mechanism is four parts, and every one of them is
a test or a guard rather than a sentence:

1. **`.gitignore`.** Both paths are ignored at the repository root's `.gitignore` and at
   `mobile/.gitignore` — the app's own ignore file, which P4.4 left in place and which P7.1
   **appends to** so the rule travels with the app. **A path under `mobile/` that
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
carries the project's real apiKey (refused: `SECRET_SHAPES`'s `google-api-key` entry and the planted `apiKey` control in `mobile/src/app-config.test.js`) or
carries placeholders and produces a build whose crash reporting is silently dead (refused:
D200). The EAS file secret is the one route where the file exists **only** on the build
machine, is scoped to the project's own build profiles, and is never in a checkout, a
fixture, a log or a pull request — the same property `docs/pending-workflows/README.md`
describes for the jobs that must not hold material on every pull request.

### 4.4 What is built in, what arrives in the pairing payload, and what is in secure storage (D199, accepted)

**D199 is the row that asked where the app's Firebase configuration comes from, and the owner
accepted the recommendation on 2026-10-19.** The answer splits the configuration in two, and
the split is the whole of the row: **what is the same for every installation is compiled in;
what is different for every installation cannot be, and travels the pairing's own encrypted
path into the phone's secure storage.**

| | What it is | How it gets there | Where it is written down |
|---|---|---|---|
| **Built into the build** | **The telemetry project's two native config files** — `mobile/google-services.json` and `mobile/GoogleService-Info.plist`, naming **`fugaro-prod`** (D194). The plugin applies them at prebuild, so the app's **default** Firebase app is the Fugaro telemetry project for every installation, and no `apiKey` is ever written in JavaScript (§4.1). | **EAS file secrets**, named by `eas.json`'s profiles and applied on the build machine — §4.3's four-part rule: ignored at both `.gitignore`s, scanned by a tracked-files test with a planted control, refused by `app.config.js` when a profile carries neither, and never read by an agent. | **Nowhere in this repository.** Never committed, never a fixture, never a log line, never a pull request. |
| **Inside the pairing payload** | **The installation's own Firebase connection** — its **Realtime Database URL** and its **web API key**. They differ per installation and **are not known at build time**, so no build can carry them. | **The encrypted pairing payload** the user's own installation hands over when an account is added by hand. The CLI already records both (`init_firebase.go`), and **G18** already reads them for the QR. They travel under **D113b**'s pairing key — the same key D111's item (2) is. | **Nowhere in this repository**, and nowhere on the wire unencrypted. |
| **In the phone's secure storage** | **Four items per account** — D111's two, plus D199's two: **(1)** the Firebase Auth **ID token**, **(2)** the **per-pairing MAC key** (D113b's option (a)), **(3)** the installation's **Realtime Database URL**, **(4)** the installation's **web API key**. | **Platform secure storage** (Keychain / Keystore), the same store D111's two already use. Never logged, never a fixture, never a settings entry — `SettingsStore`'s `SECRET_SHAPES` and `FORBIDDEN_KEY_SEGMENTS` are unchanged. | **D111's row** in [mobile/reconciliation.md](../mobile/reconciliation.md) and **Global Constraint 4** in [mobile/implementation-plan.md](../mobile/implementation-plan.md), each carrying a **dated amendment of 2026-10-20** that records the owner's acceptance of 2026-10-19 and keeps the original *two items* text visible. |

**Why the connection values cannot be built in.** A build carries **one**
`google-services.json` and **one** `GoogleService-Info.plist`, and they name **one** project.
The app serves several installations (**D114**, *"several, non-overlapping"*), and each
installation's database URL and web API key are its own; a build that named one installation's
would serve that installation and no other — which is the alternative D199 refused, at the
cost of a build and a store submission per installation.

**What this changes, and what it does not.** It changes **D111's record** from *two items per
account* to **four**; D111's own cell says why the list is exhaustive rather than illustrative
(*"D54b's precedent is to list every item held, not only the obvious one"*). It **narrows what
the settings store's refusal of API-key-shaped values protects**: `SECRET_SHAPES`'s
`google-api-key` entry and `judge`'s `SECRET_VALUE` branch are **not relaxed**, a settings
entry may still never carry an `AIza…` value, and the store's own fence is exactly as it was;
what no longer holds is this plan's **stronger** sentence, that the app stores no such value
*anywhere*, because the installation's own key now lives in secure storage **by design**. **No
value is written into this repository by that**: this section names the two values' *kinds*
and never a value, and nothing here is a secret-shaped literal. **And no task is approved by
it**: D199's acceptance is a ruling, not a green light, and the app still wires **no** Firebase
SDK until Phase 7 is approved and P7.1 is implemented.

---

## 5. The Fugaro-operated project (D194), as owner steps

**The project is `fugaro-prod` — the owner's answer of 2026-10-19, and the project that already
hosts the website.** The plan's suggested id, `fugaro-telemetry`, is **not** what the owner
chose; the recommendation stays visible in D194's row and this section is rewritten for the
answer, because four of the facts below change when the two things share one project.

**One project, two tenants, and the plan states the sharing plainly** rather than leaving it
to be discovered:

- The **website's deploy service account** — the identity the site's own deploy uses — **holds
  Firebase Hosting roles in `fugaro-prod`**, and it keeps them. Those roles are Hosting's:
  they publish the site's files.
- **It may not hold, and must not be granted, any read of Analytics or Crashlytics data.** The
  Firebase Hosting roles do not carry one; the requirement is that nothing added later grants
  it, because a service account that can read the app's crash reports is a credential a
  website deploy can leak.
- **The owner accepted this arrangement when answering D194**, with the sharing stated: one
  project, the site's deploy identity inside it, and the analytics and crash data behind a
  membership the deploy identity is not part of.

Named plainly, because every step below is the owner's and none is an agent's:

1. **Use the project that exists** — `fugaro-prod`, the owner's own Firebase / Google Cloud
   project, the one the website is already deployed into. **Nothing is created for telemetry**:
   no new project, no new billing link, and no second organization membership list. *Owner
   step.*
2. **Register the two apps** in it. One Android app (the package id the app's `app.config.js`,
   merged as PR #675, gives the store build) and one iOS app (its bundle identifier). *Owner
   step.*
3. **Download the two native config files.** They are the artifacts §4.3 delivers. They go to
   the EAS project's file secrets and **nowhere else**. *Owner step.*
4. **Turn on Analytics and Crashlytics** in the project, and note the property's **retention**
   setting — the plan requires the shortest retention the console offers, and D194's answer
   records which. *Owner step.*
5. **Membership: the owner alone.** **No service account, no CI identity and no agent** may be
   granted any **Analytics or Crashlytics read**. The **website's deploy service account is the
   one identity already inside the project**, and it holds **Firebase Hosting roles only** —
   none of them reads analytics or crash data (§5's opening paragraph, and the fact the owner
   accepted). Anything the deploy identity is granted later is a change to this paragraph.
   *Owner step.*
6. **Record the answer** — the project id, the Analytics property id, the two app ids and the
   retention — in the owner's own notes. **What must not be written into this repository is
   what is new**: the property id and the two app ids are the project's own, the app reads them
   from the native config file at build time (§4.3), and the repository does not need them (a
   committed config file's contents would be an invitation to commit the file beside it).
   **The project id itself is already in the repository and this plan uses the committed
   spelling** — `fugaro-prod` is **D91's** project, the one the website is deployed into, and
   it is named in `scripts/deploy-website.sh`, the website's preview workflow and
   `docs/website/preview-promote.md`, as §5's opening paragraph says. An earlier draft of this
   step said *"no id of this project is written into this repository"*, which was false when it
   was written; this step states the half that is not.

**What the app does *not* do with this project.** It never authenticates to it, never reads
it, never writes it and never lists it: it hands the native SDK a config at build time and
nothing more. **The app never uses the website's credentials and the website never uses the
app's** — they meet in the project's membership list and nowhere else. **No RTDB rule, no
Terraform and no `fugaro` command in this repository is changed by any part of this plan** —
`fugaro-prod` is not an installation, holds no Fugaro data, and no rules file mentions it.
**The website's own deploy is unchanged by this plan**: it deploys as it does today, with the
Hosting roles it has today.

---

## 6. Consent and opt-out (D195): there is none

**The owner's answer, in one sentence:** *collection is on in the builds that carry the SDKs —
there is no switch, no first-launch sheet and nothing in the app to turn off.*

**What that answer supersedes, and what stays visible.** This plan **recommended opt-in** —
*nothing is collected until the user says yes on the first launch, one switch for both SDKs* —
and it argued for it: the user is in the EU, the SDKs write an app-instance id to the device
the moment they start, and reading or writing anything on a device that is not strictly
necessary is the thing the site's own consent banner exists for. **The owner answered always-on
on 2026-10-19**, and the recommendation stays in D195's row as the recommendation it was. What
the plan does **not** drop with it is the disclosure half: §8.3's paragraph and the live
privacy page say what is sent and what is never sent, whether or not a reader can turn it off.

**What the answer removes from the tree.** There is **no** `shared/telemetry-consent/`, **no**
`mobile/src/telemetry/consent.js`, **no** sheet, **no** stored choice and **no**
`telemetry.consent` key in the settings store. **Nothing is shown at first launch as a gate**:
no sheet, no prompt and no question — the `NO_ACCOUNT` line `mobile/src/app/root.js` renders
today is still what a new install sees — and P7.2 is rewritten as the always-on statement and
the link rather than as a gate.

**What the app says instead, and where — and the surface it does not have.** **The app has no
settings screen.** Read from the tree: `mobile/src/app/root.js` builds a shell out of a title
and one body `Text` (`APP_TITLE`, `NO_ACCOUNT`), and **no phase of the mobile plan** — Phase 3
through Phase 7 — builds a settings screen for a statement to live in; earlier drafts of this plan
wrote as if one existed, and it does not. The
statement therefore goes in **the only surface the app has**: the shell, as **one more child**
of it. `mobile/src/telemetry/notice.js` builds the statement, `mobile/src/app/root.js` renders
it as a third element (`SHELL.NOTICE`, named in the same file so a test can name it too), and
**`mobile/src/app/root.test.js` moves in the same commit** — it pins the shell's children to
exactly `[SHELL.TITLE, SHELL.BODY]` and the rendered text of both states, so it is the file
that would fail on the statement's first line. The statement is **fixed text**, in the page's
own words — *the reports are on, there is no switch, uninstalling the app stops them* — and it
carries **no control**.

**The link, and the host list the owner still owes it.** The statement carries the link to
**`https://fugaro.dev/privacy/`**, opened **only** through P0.3's URL validator (`shared/url/`)
and never as a raw `Linking.openURL` of a literal. **It cannot be a link yet.**
`shared/url`'s `DEFAULT_HOST_ALLOW_LIST` ships **empty** — the module's own header says the
list *"is not this module's to choose … it is the owner's answer to D124, and it has not been
given"* (read from the tree) — and **D124's answer names no hosts**: reconciliation U3 and
finding F27's gap 7 are where that is recorded. So `https://fugaro.dev/privacy/` is refused
with **`allow-list-empty`** and renders as **text**, which is exactly what P7.2's own
verification expects of a host the allow-list does not name. **This is an owner step, and the
plan names it here rather than leaving it to be discovered on a device:** *before P7.2 can
render a link, the owner gives D124's host list, with `fugaro.dev` in it — one exact entry, no
suffix and no wildcard, which is the validator's own rule.* Until that step is taken the
statement is what the app shows and **check 111's link half cannot pass**; §12 says so inside
the check as well, so a reader running it is not left to conclude the app is broken. **The
website's privacy statement is the authority for what a reader is owed in prose**, it already
carries the app's paragraph, and §8.3 is where the plan holds the two to each other.

**"There is no switch" is structural, not a sentence.** Two things hold it, and neither is
prose: §4.2's native switches are **on** — so there is no state for the app to toggle — and
**§7.4's scan refuses `setAnalyticsCollectionEnabled(` and `setCrashlyticsCollectionEnabled(`**,
because a call that can set collection state *is* the switch this answer says does not exist,
whether it is called with `true` or with `false`. The one thing that stops the reports is
**uninstalling the app**, which is what the privacy page tells a reader.

**What the answer costs, stated rather than hidden.** A user who does not want the reports has
**no way to stop them inside the app**: the first event, the app-instance id and every crash on
that device are sent from the first launch. The alternative — opt-in — is D195's recommendation
and it is not what the owner chose; its cost (nothing is reported about the population that
never answers the sheet) is what the owner weighed against this one. **The plan records the
trade and does not re-argue it.**

**The device holds no consent value, so consent adds nothing to D111.** The key this section
used to reserve — `telemetry.consent`, in the settings store and deliberately not a `secret.`
key — **is not reserved any more**: nothing is stored and nothing is read back. **D111's list
widens by D199's acceptance (2026-10-19) and by nothing here**: the owner took the
recommendation, so D111's *two items per account* becomes **four** — the installation's
Realtime Database URL and its web API key join the ID token and the per-pairing MAC key
(§4.4) — and those two new items arrive in the **pairing payload**, not from consent.
Consent itself still stores nothing.

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
  `secretShaped` — the walk over `SECRET_SHAPES` in `mobile/src/accounts/settings.js` — so the
  telemetry scan and the config scan cannot disagree about what a credential looks like.
- **No free text at all.** Every parameter value is a **boolean or an enum**, and every enum's
  members are in this plan's list.
- **No advertising identifier — and nothing that could start collecting one.** Android throws
  `google_analytics_adid_collection_enabled` and
  `google_analytics_default_allow_ad_personalization_signals` to `false` and declares **no**
  `AD_ID` permission; iOS links **no** `AdSupport` framework (§4.2). This is the ban list's one
  entry that is held by the **build** rather than by the app's own code, so §7.4's third test
  holds the generated files, and the Play Data safety form's **no Advertising ID** answer
  (§8.1) is true only while that test is green.

### 7.2 Sent — the closed allow-list

**The event names.** **Seven**, and no eighth without a change to this list. The list had eight
while this plan recommended opt-in: the eighth was `consent_set` (`granted: bool`), the event
that recorded which way the sheet was answered. **D195's always-on answer removes the sheet, so
the event goes with it** — there is nothing to record — and the allow-list is one name shorter
rather than carrying a parameter nothing can produce.

| Event | Parameters (closed sets) | Why it exists |
|---|---|---|
| `app_launch` | `first: bool` | Does the app start on a real device at all, and does it start again. |
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
the app-instance id) and the plan **accepts them**, because they carry no Fugaro data.
**D195's always-on answer makes that acceptance load-bearing rather than incidental**: the
auto-collection switch is `true` (§4.2), so these events are collected from the first launch
along with ours, and the page's paragraph names them by category (*which screens were opened*,
*app-instance identifier*) rather than leaving the reader to infer them. The one switch that
still trims the set is the **ad-ID** one, `google_analytics_adid_collection_enabled = false` —
which removes the advertising identifier and nothing else. **The rejected alternative is listed
in D196**: turning the allow-list off and relying on the ban list.

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

P7.3 adds **two** tests in `mobile/src/telemetry/`, and P7.1 extends the app's own
`mobile/src/app-config.test.js` with a **third** — the ad-ID test, whose subject is the build
rather than the app's code. All three carry positive controls, all three in the
shape the tree already uses (`mobile/src/mailbox/write-path.test.js`,
`mobile/src/boundary.test.js`):

1. **The source scan.** Every `.js` under `mobile/` is walked (comments stripped, test files
   exempt, the scanner exempt from itself — the existing rule). The scan refuses:
   - any `logEvent(` whose **first string argument is not a name in §7.2's list**;
   - any `setUserId(`, `setUserProperty(`, or `setAttribute(`/`setAttributes(` call at all;
   - any `@react-native-firebase/…` import outside the telemetry module and the entry —
     **a rule the tree does not have today** (`write-path.test.js`'s rule 2, whose pattern is
     `/['"]@?firebase\/[a-z-]+['"]/`, does not match `@react-native-firebase/analytics`),
     and which P7.3 adds to `write-path.test.js`'s `RULES` array as **one deliberate
     allow-list entry**, the way P3.1's boundary test takes one;
   - any `crashlytics().log(` whose argument is not a constant in the module's list;
   - **any `setAnalyticsCollectionEnabled(` or `setCrashlyticsCollectionEnabled(` call at all**
     — **D195's answer is always-on and there is no switch**, so a call that sets collection
     state is either a switch this plan says the app does not have or dead code pretending to
     be one. **This is the fence that makes "no switch" a fact about the source rather than a
     sentence in a policy page** (§6).
   Each rule is proved against **a planted line in real code** before the tree is judged, and
   the allow-list is proved non-vacuous the way `write-path.test.js`'s last test does it.
2. **The never-send test.** The telemetry module is driven with a **fixture account, fixture
   mailbox and fixture token**, and the events it produces are captured by a fake port: the
   test asserts that **not one value from the fixture** appears in any captured event, that
   every parameter is a boolean or a member of its enum, and that `secretShaped` returns null
   for every string the capture holds. A planted run that logs a message's `text` fails it.
3. **The ad-ID test (`mobile/src/app-config.test.js`, extended by P7.1).** The website promises
   the app carries no advertising identifier (§4.2, §8.3), so the app's own config test — which
   already walks `app.json` for a credential with a planted `AIza…` control — gains a third walk
   over the **generated native config and the plugin options**:
   - `android/app/src/main/AndroidManifest.xml` must carry
     `firebase_analytics_collection_enabled` and `firebase_crashlytics_collection_enabled` as
     **`true`** — D195's answer, and the half that changed when it was given — and
     `google_analytics_adid_collection_enabled` and
     `google_analytics_default_allow_ad_personalization_signals` as **`false`**, and must
     **not** declare `com.google.android.gms.permission.AD_ID`;
   - the iOS project must not link `AdSupport` (read from the generated `Podfile.lock`), and the
     plugin options that select the ad-ID-free Analytics variant must be present (§4.2's
     **VERIFY** item);
   - the plugin options in `app.config.js` and the switches in `mobile/firebase.json` are checked
     **always**, because they are source; the generated files are checked **when they exist**,
     and the test **fails on a machine that has them and a `true`**, not one that has neither —
     where it names the half it could not see instead, the rule §4.3's tracked-file test follows.
   Each branch is proved to be able to fail: a planted copy of each generated file with the
   switch flipped to `true`, and one with the `AD_ID` permission added back.

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
| **Crash logs** | Crashlytics, by definition | Required; sent from the app's first launch (D195's always-on answer — there is no consent to wait for). |
| **Diagnostics** | Crashlytics' device and OS fields | Required. |
| **Other app performance data** | Crashlytics' performance traces and the `crash_reported` marker | Required. |
| **Device or other IDs** | The SDKs' own app-instance / installation id, **and nothing of Fugaro's** | Required; `google_analytics_adid_collection_enabled = false` removes the **advertising id**, not the SDK's own installation id, and the plan does not claim otherwise. |
| **User IDs** | **Nothing in this plan sends a user id** — `setUserId` is refused outright (§7.3) | **Over-declared on purpose.** The tick is kept: it is the safe direction, it costs nothing, and narrowing it is a change the owner may make once the first real traffic has been read (§8.3). |
| **Other in-app messages** | **Nothing in this plan sends one** — the mailbox is on the ban list (§7.1) | **Over-declared on purpose**, for the same reason. It is also the tick that matters most: if it is ever *justified* by real traffic, §7.1 has been broken. |

**The honest sentence for the plan to carry:** the ticked set is **wider** than what this plan
sends, in **two** categories, and that is a deliberate choice — under-declaring is the policy
risk, over-declaring is not — and no test can hold it, because no test can read the Play
console.

**And one category is narrower, on purpose: Play's Data safety form is answered with no
Advertising ID type at all.** *Device or other IDs* covers the SDKs' own app-instance id — the
table above says so — and the advertising identifier is a **different data type on that form**,
which the app must not send because the website's privacy page promises that it carries none
(§4.2, §8.3, PR **#689**). That answer is true only while §4.2's switches stay `false`, the
`AD_ID` permission stays removed and iOS keeps linking no `AdSupport`; **§7.4's ad-ID test is
what holds it**, and check **113** is where the owner confirms the submitted form matches what
the app sends. **If that test ever has to be weakened, the Play answer and the website's
sentence change in the same commit** — an advertising id collected under a form that says it is
not is the one failure here that is a false statement to a regulator rather than a bug.

### 8.2 App Store — the privacy label

Answered from the same list, as **Data Not Linked to You** unless stated:

| Apple category | Type | Justification |
|---|---|---|
| **Crash Data** | Not Linked | Crashlytics |
| **Performance Data** | Not Linked | Crashlytics' performance data |
| **Diagnostics** → *Other Diagnostic Data* | Not Linked | the SDK's installation id |
| **Identifiers** → *Device ID* | Not Linked | the SDK's app-instance id |
| **Identifiers** → *User ID* | — | **not ticked**: `setUserId` is refused. Apple's form is answered per data type collected, and the app collects no user id. |
| **Identifiers** → *Advertising Data* | — | **not ticked**, and this one is a promise rather than an answer: the app links no `AdSupport` and reads no advertising id (§4.2), the same fact the website's privacy page states in prose and Play's form carries as **no Advertising ID type** (§8.1). §7.4's ad-ID test is what keeps the two stores' answers true together. |
| **Usage Data** → *Product Interaction* | Not Linked | the **seven** events of §7.2 — Analytics ships with the second build (**D198**, answered), and the list lost `consent_set` when D195 was answered always-on. If Analytics does not ship, this row is **not ticked**. |

**The label and Play's form are answered from one document** — this section — so the two
stores cannot be answered differently, which is the failure a reader would never catch.

### 8.3 The website — what the live page says, and what holds it

**The page is ahead of the SDKs, by design, and it says so itself.** This section used to read
*"what changes, in the same release that ships the SDKs"* — D197's recommendation, made when
the page said nothing about the app. **Two pull requests have since put the app's paragraph on
the page, and neither is on this branch's base** (read on `origin/main` at `66bbcf8d`; this
branch's merge base with it is still `8e3e810a`, and this change merges no base in):

- **PR #689** split the statement into **two parts** — *The website* and *The software* — so
  that the one URL the two store listings point at covers the software as well as the site, and
  added **The phone app** under the software part.
- **PR #705** wrote the owner's ruling into it: *"The app versions that carry these reports send
  them. There is no switch inside the app to turn them off: uninstalling the app stops them, and
  not installing it avoids them."* — **D195's answer, in the page's own words, published before
  the build exists.**

**So the sequencing sentence is replaced by the truth, and the truth is in the page's last
paragraph of that section:** *"Earlier builds of the app send nothing at all."* The page is
honest about a build a reader may be holding, and it is **ahead** rather than behind — the
failure mode D197's recommendation was written to avoid was the opposite one (a store build
carrying an SDK while the page promised none).

**What the page says today, and what each sentence is held to.** The paragraph names
**Firebase Crashlytics** and **Firebase Analytics**, says what a crash report holds (the crash's
logs, diagnostics and app performance data), says which screens were opened, names the
app-instance identifier, states the never-sent list (message contents, the installation and its
mailbox, credentials and keys) and states **no advertising identifier** and **no switch inside
the app**. Its own gate — `website/test/privacy.mjs`, on `origin/main` — reads that page against
**this plan file** in both directions: a telemetry package the app's dependency list declares
and the page does not name is refused, and the page naming a provider that neither this plan nor
`mobile/package.json` holds is refused unless it says which version of the app carries the
reports (**UNVERIFIED here**: the gate is not on this base, and this change does not run it).

**What must therefore change, in this plan's terms — and what must not.**

1. **The page's sentences must stay true of the build that ships.** §7's ban list and allow-list
   are the app's side of that bargain, and P7.4's job is to hold the two together at the moment
   the SDKs land: if the app sends something the page does not name, or the page names something
   the app does not send, **the page changes in that commit or the build does not ship**. It is
   the same rule D197 answered, with the direction reversed.
2. **The paragraph's advertising-identifier sentence is the one it must keep** — *the app carries
   no advertising identifier* — because that claim is held by the **build** rather than by the
   app's code: §4.2's two ad switches `false`, the removed `AD_ID` permission, the unlinked
   `AdSupport` and §7.4's ad-ID test are what make it true, and it survives D195's answer
   unchanged. **The two stores' answers carry the same fact** (§8.1's no Advertising ID type,
   §8.2's unticked Advertising Data).
3. **The page's "no switch" sentence is a promise this plan can keep now and could not keep
   under its own recommendation.** §6 makes it structural — the native switches are on, so
   there is no state to toggle, and §7.4's scan refuses the runtime calls that would be one. **A
   page saying there is no switch and an app carrying one is the contradiction §6's fence exists
   to prevent.**
4. **The page's word ceiling is D188's** (a page's lead at most 40 words, a page at most 250,
   with `/docs/`, `/security/` and `/changelog/` excepted). **UNVERIFIED at this base:** #689
   re-derived the privacy statement's own ceiling — it is the one page that is not the site's
   pitch — so P7.4 reads `website/README.md` and `website/test/budget.mjs` at its own merge
   rather than trusting this paragraph. The trade is still the same: **a sentence added here
   costs a sentence traded out**, in the same commit, by whoever owns the page then.
5. **`website/data/site.yaml`'s `analytics` block is not touched** unless the paragraph needs a
   new fact from it: the block describes the *site's* GA4, and the app's telemetry is not a
   site fact.

**The website change is a task in this plan (P7.4), owned by whoever owns `website/` then**,
because the page's gates (`website/test/*.mjs`, the origin gate, the byte budgets) are the
website work's and this plan does not fork them.

---

## 9. Interaction with the voiceless MVP (D198)

The first MVP is **voice-less internal testing on both stores**. **The owner answered D198 on
2026-10-19: the second build carries the SDKs and the first internal build ships neither.** The
reason is structural rather than cautious:

- The first internal build's purpose is the **store path** — a signed EAS build reaching an
  internal track (P4.4, check 89) — and, per the brief, the *voice-less* app. A native config
  plugin changes the native project that path is proving; a telemetry failure and a store-path
  failure would land in the same release, and the store path is the one that cannot be redone
  cheaply.
- The first build carries **no native config plugin at all**, which is the cleanest form of the
  point above: with D195 answered always-on, there is no sheet to design and nothing to gate,
  so the first build's only new firsts are the store path and the app's own wiring.
- **D197 makes it strictly better to wait:** the labels are answered from what the SDKs actually
  collect, and the first build collects nothing to read. The website's paragraph is the one
  piece that did **not** wait — it is already live and already says which version carries the
  reports (§8.3) — and that is D197's own answer working as intended rather than against it.

**The alternative is D198's own second option** — *Crashlytics alone in the first build,
Analytics in the second* — and the owner did not take it. Its cost is stated there: the labels
are answered twice, and the first build would carry a native config plugin, which is the thing
this row exists to keep out of the store path's release.

**What D195's answer changed here.** The paragraph that stood in this section said the consent
sheet was a first-launch screen and that designing *it* and the store path and the first native
plugin together was three firsts at once. **There is no sheet any more**, so that argument is
gone — and the decision it was supporting is unchanged, because the two remaining firsts are
still two too many for one release.

**What does not change either way.** No rules file, no Terraform, no `fugaro` command and no
existing mobile task moves. Telemetry is **additive**: it is a new phase, and the voiceless
MVP's tasks keep their own ordering.

---

## 10. The tasks

*Five tasks, each independently mergeable, each with a reviewer tier and a verification that
runs with no device, no live cloud and no network. The phase is **proposed**: no task here may
start before the owner passes [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
approval gate for it. **Every row the tasks wait on is answered — D193 to D200, all eight, on
2026-10-19, D199's acceptance last of all** — so **no "Depends on" line below names an open
question**; each names a ruling. **Answering a row approved no task**: the gate is unchanged,
and Phase 7 stays PROPOSED.*

### Task P7.1: The config plugin, the native files, and the development build

**Files.** `mobile/app.config.js` (**modified** — the plugin entry, the two `googleServicesFile`
paths read from the environment, the build-time guard), `mobile/eas.json` (**modified** — the two
`env` entries in each of its three build profiles, `development`, `preview` and `internal`),
`mobile/firebase.json` (new — the app-root
`react-native` block of §4.2), `mobile/.gitignore`, `mobile/package.json` (the three
`@react-native-firebase` dependencies), `mobile/README.md` (the Expo Go sentence D200 asks
for), and `mobile/src/app-config.test.js` (the tracked-file and ignore rules of §4.3, the
extension of the existing credential walk to `app.config.js`, and **the ad-ID test of §7.4**,
which reads the plugin options, `mobile/firebase.json` and — when they exist — the generated
`AndroidManifest.xml` and `Podfile.lock`).
**Depends on.** **D199 — accepted 2026-10-19**, the row that decides which project the native
files name, which app is the default, and where an installation's own connection comes from
(§4.4). **It was the last row P7.1 waited on, and it is answered**, so this line names a ruling
rather than a question — as does every other dependency: D200 answered yes, and **D194 answered
`fugaro-prod`**, the project that already exists, so the "project before its file" half is done
the day the owner registers the two apps in it. **PR #675 is not a dependency**: it is merged, and P7.1 modifies the two files it added
where they stand.
**Verification.** `cd mobile && npm test` — the extended `app-config.test.js`: both native
paths are untracked and ignored; `app.config.js` contains no `AIza…`-shaped value (planted
control); a build profile with no `GOOGLE_SERVICES_JSON` in its `env` is refused by the guard's
pure part; the existing "no credential, no installation" walk still passes; and **the ad-ID
walk passes** — the two Android ad switches are `false`, the manifest declares no `AD_ID`
permission, and the iOS side links no `AdSupport`, each with a planted `true` control proved to
fail the walk. §4.2's **VERIFY** item (the exact iOS pod/plugin option, and whether the Android
permission removal is the plugin's own option) is resolved at this task's first commit by
reading the pinned `@react-native-firebase` source, and §4.2's table is corrected in that commit
if a name is wrong.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the change that
decides which project the app's default Firebase app is (D199, accepted 2026-10-19), and it is the change that
would put a native config file in the tree if it were wrong.

### Task P7.2: The always-on statement, and the link

**Files.** `mobile/src/telemetry/notice.js` (new: the statement the app shows and the
`/privacy/` URL it links, both as constants in one place), `mobile/src/app/root.js` (the shell
renders the statement as **a third child**, `SHELL.NOTICE`, because **the app has no settings
screen for it to live in** — §6 reads that from the tree — and the shell is the only surface
the app has today; **not a gate, not a modal and not a first-launch prompt**, and the statement
carries **no control** — and the file's own header, which says what the shell is allowed to
show and forbids widening it silently, is updated in the same commit, because this is the
widening its rule asks to be recorded), **`mobile/src/app/root.test.js`** (it pins the shell's children to
exactly `[SHELL.TITLE, SHELL.BODY]` and the rendered text of both states, so it moves in the
same commit — without it this task's own `cd mobile && npm test` fails on the statement's first
line), and `mobile/src/telemetry/notice.test.js`. **`shared/telemetry-consent/`, `mobile/src/telemetry/consent.js`
and the `telemetry.consent` settings key are all gone from this task**: D195's answer is
always-on, so there is no choice machine, no sheet and no stored value (§6).
**Depends on.** P7.1; **D195 (answered: always-on)**; and **D124's host list, which the owner
still owes** — `shared/url` refuses every host today (`DEFAULT_HOST_ALLOW_LIST` ships **empty**)
and D124's answer names none, so **until the owner names `fugaro.dev` the `/privacy/` URL is
refused with `allow-list-empty` and renders as text**: this task then ships the statement
**without a link**, and that is the expected result, not a failure (§6).
**Verification.** `cd mobile && npm test`: the statement carries the page's own three facts —
the reports are on, there is no switch, uninstalling stops them (§8.3) — the shell renders it
as its third child with `root.test.js` moved in the same commit, the URL is built through
`shared/url`'s validator, and a host the allow-list does not name renders as **text** rather
than being opened — which, until the owner's host list names `fugaro.dev`, is the assertion
that passes rather than one that fails. **And the negative half, which is what this task is now for:** the
app's source carries **no** `telemetry.consent` key, no stored choice, no toggle, and **no
`setAnalyticsCollectionEnabled(`/`setCrashlyticsCollectionEnabled(` call** — §7.4's scan is
what proves it, with a planted call as its control.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the only place a
link leaves the app, and it is the task that must not grow a switch back.

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
count moves (`website/test/budget.mjs` is the authority for the ceilings, **D188's rule** —
§8.3 says which decision the word ceiling belongs to; **D192** is the budget's byte ceilings and
is not the number to change here).
**Depends on.** P7.3 (the list the labels are answered from), **D197 (answered: labels from
what the SDKs collect, the page's words with the release that ships the SDKs)**, and the live
privacy statement — **PRs #689 and #705, merged to `origin/main`, not on this base**: the task
starts from a page that already carries the app's paragraph rather than from a page that has to
gain it (§8.3).
**Verification.** `node --test` in `website/` (the site's own gates: the word ceilings, the
origin gate, the copy gate, and **`website/test/privacy.mjs`, which reads the live page against
this plan file in both directions** — a provider the page names that neither this plan nor
`mobile/package.json` holds is refused, and so is the app carrying one the page does not name).
**The page's sentences must be true of the build that ships, checked in the commit that ships
it** (§8.3). **No Go test can see the Play console or App Store Connect, so the two forms are
answered by the owner and recorded in the owner's notes, not asserted here.**
**Reviewer tier.** standard for the website change; **owner** for the two store forms, which
no agent fills in.

### Task P7.5: Checks 111–113, written into the register

**Files.** `docs/gcp-live-checklist.md`.
**Depends on.** P7.1, P7.2, P7.3 (a check is never run before its text is in the register).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, and the pin
`internal/cli/docs_mobile_telemetry_test.go` (this change) held to the new section's summary
rows.
**Reviewer tier.** standard for the writing; **owner** for the running. **No agent runs any
step of 111, 112 or 113.**

### 10.1 Order and levels

```
D193, D194, D199, D200 (answered) ─┐   D195, D196, D197 (answered)
                                    ┴─► P7.1 ──┬─► P7.2 ──┐
P0.3 (shared/url, merged) ──────────────┘          ├─► P7.4
                                       P7.3 ───────┴─► P7.5
#675 (merged: app.config.js, eas.json) ──► P7.1
#689, #705 (merged to origin/main: the privacy statement,
           the app's paragraph and the always-on sentence) ──► P7.4
```

**All eight rows are answered — D199's acceptance on 2026-10-19 was the last — so every edge
above is a ruling rather than a question. None of them approves a task** — the gate in
[mobile/implementation-plan.md](../mobile/implementation-plan.md) still has to be passed.

| Level | Tasks | Reviewer | Files touched |
|---|---|---|---|
| **0** | **P7.1** the plugin, the native files, the development build | **SECURITY-SENSITIVE** | `mobile/app.config.js`, `mobile/eas.json`, `mobile/firebase.json`, `mobile/.gitignore`, `mobile/package.json`, `mobile/README.md`, `mobile/src/app-config.test.js` |
| **1** | **P7.2** the always-on statement and the link · **P7.3** the allow-list and the scan | **both SECURITY-SENSITIVE** | `mobile/src/telemetry/notice.js`, `mobile/src/app/root.js` and `mobile/src/app/root.test.js` · `mobile/src/telemetry/` (the event module and its two tests), one entry in `mobile/src/mailbox/write-path.test.js` |
| **2** | **P7.4** the labels and the website's paragraph | standard (website) / **owner** (the forms) | `website/src/pages/privacy.astro`, the website's gates |
| **3** | **P7.5** checks 111–113 written | standard | `docs/gcp-live-checklist.md` |
| **owner** | the checks **run** — P7.5 writes 111–113 and the owner runs them. **This is not a task and has no number**: running a check merges nothing, so the phase is five tasks and not six | owner | none |

**P7.2 and P7.3 are parallel** — different directories, and the only shared file between them
is none. **P7.4 waits on P7.3** because the labels are answered from the allow-list, not from
this plan's prose. **P7.5 waits on all three code tasks.** **No level contains two writers of
one file:** `mobile/src/telemetry/` is written by P7.2 (`notice.js`, `notice.test.js`, and the
one child P7.2 adds to `mobile/src/app/root.js` and its test) and P7.3 (everything else —
`consent.js` went with D195's answer, and §6 lists it among what the answer removes), and the
plan splits them into **two files with two owners** rather than one directory with two — the
same rule the mobile plan's shared-files table states.

**The critical path is P7.1 → P7.3 → P7.4 → P7.5**, four deep, and it is short on purpose:
this phase is additive and nothing in the app's existing tasks waits on it.

### 10.2 What each task can and cannot verify

| The task | Can be verified without a device | Cannot |
|---|---|---|
| P7.1 | the tracked-file rules, the ignore rules, the `AIza…` walk, the guard's pure part | that the plugin applies, that a development build is produced, that the SDKs initialise — **`npx expo prebuild` and `eas build` need a toolchain this repository's CI does not have** |
| P7.2 | the statement's facts, the link's validator path, and the absence of a switch (the scan) | that the statement renders in the app, and that the native SDK is collecting — **only check 111 can show that**, on a device |
| P7.3 | the scan, the never-send test, the existing fences | that the *native* SDK collects what the allow-list says and nothing else — **only checks 111 and 112 can show that**, on a device |
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
- **No credential in the app beyond D111's list, as D199's acceptance widens it — four items
  per account, and no more.** The app's D111 items and the installation's two connection values
  all stay in platform secure storage, and none of them is ever in a settings entry, a fixture,
  a log line or this repository (§4.4); the consent boolean is deliberately **not** one of them
  (§6).
- **No `belong` anywhere.** No fixture, no project, no read.
- **No change to any existing mobile task's ordering.** Phase 3, Phase 6 and Phase 7 are
  independent, and the voiceless MVP is unaffected (D198).

---

## 12. Checks this plan takes (111, 112, 113)

**Three numbers out of the free block after 104, owner-run, none of them a CI job, and no
agent runs any step of any of them.** They are **written by P7.5 and run by the owner**, and
**a check is never run before its text is in the register**.

| # | The check | Task | Why it cannot be a test |
|---|---|---|---|
| **111** | **The reports arrive, they arrive from the first launch, and they arrive at `fugaro-prod` and no other project.** Install a second-build app on a device, launch it, **touch nothing** — there is no sheet, no switch and no prompt, which is D195's answer — and watch the Firebase console's debug view: the app's own `app_launch` and `screen_shown` events appear **for `fugaro-prod` and for no other project**, and they appear **at the first launch** rather than at any later moment. Then the two negative halves, which are what this check is for: **the app's shell says the reports are on and carries no control that stops them — the link beside that sentence is the `/privacy/` URL once the owner's D124 host list names `fugaro.dev` (it renders as text until then, §6), so this half of the check is run after that owner step and not before it** (D195, §6, and §7.4's scan is the static half of the same fact); and **the submitted build declares no `AD_ID` permission** — read from the built artifact's merged manifest, not from the source — so the identifier the page promises is absent is absent in the build a store holds. | P7.5 writes it; **the owner runs it** | Needs a real device, a real store build, a real network and the Firebase console. No hermetic test can see a native SDK's own socket, and no Go test can read a store's build. |
| **112** | **A real crash reaches `fugaro-prod`, with nothing that names anything in it.** Crash the app deliberately from the debug menu on a freshly installed device — **nothing has been consented to, because there is nothing to consent to**: the report appears in Crashlytics under `fugaro-prod` on the app's own first run, which is the always-on half of D195 proved end to end. **Its breadcrumbs, its custom keys, its logs and its device fields contain no message text, no mailbox key, no account id, no `authUid`, no project id, no token and no MAC**; `setUserId` is absent from the report; and **no advertising identifier is in it**. Then the control, and it is a different control from the one this check used to carry: **the same crash on the FIRST build — the one that carries neither SDK (D198) — produces no report at all**, which is what proves the reporting is the SDKs' and not something else in the app. | P7.5 writes it; **the owner runs it** | Needs a real device, a real crash, two builds and the Crashlytics console — the redaction is a property of a report that only exists after a crash. |
| **113** | **The two store forms, and the live privacy statement, match what the app sends.** The Play **Data safety** form and the App Store **privacy label** are the answers of §8.1 and §8.2, category by category, and **each ticked category is justified by traffic observed in 111 and 112** — with the two over-declared categories (`User IDs`, `Other in-app messages`) recorded **as** over-declared, in the owner's own words. And the page: `https://fugaro.dev/privacy/` — **the URL both store listings point at as the privacy policy** — is read against the build sentence by sentence, because **it is already live and already ahead of the build** (§8.3): every provider and every category it names must be one the app actually sends, and **every category the app sends must be one it names**. A page that names something the app does not send, or an app that sends something the page does not name, is the failure this check exists to catch — and since the page cannot be changed by the build, **the build does not ship until the two agree**. | P7.5 writes it; **the owner runs it** | Needs the two consoles, the live site and the owner's own accounts. |

**111, 112 and 113 are owner-run. 105–109 are reserved, not free, and this plan takes none of
them; 114 onward is free and is not taken either. 110 is another work's claim and is not
touched.**

---

## 13. What this change edits, and what it does not

**This change is docs-only, plus one Go test that pins the documents.**

**Created:**

| Path | What it is |
|---|---|
| `docs/plans/2026-10-19-mobile-telemetry.md` | **This document — the plan, and the only deliverable of the work the brief asks for.** |
| `internal/cli/docs_mobile_telemetry_test.go` | The pin: this document's own claims — the D-block it takes (D193–D200) and the statement that it is the next free block, the check numbers it takes (111–113), the reserved block it takes none of (105–109) and 110 left alone, the flag names it states exactly, the never-commit rule for the two native config files, and the registration of this plan in `docs/mobile/`'s two documents. It writes no code and asserts no behaviour of an app that does not exist. |

**Edited:**

| Path | The edit |
|---|---|
| `docs/mobile/README.md` | The index: one row for this plan in the file table, and a status sentence saying the plan exists, that it is **plan only**, that **the owner answered all eight rows on 2026-10-19** — **D193 to D200**, **D199 accepted last** — and that this work takes **D193–D200** and **checks 111–113**. The "numbers this work holds" table gains the two rows. **Re-edited 2026-10-20 for D199's acceptance**; the status block is now dated to that answer. |
| `docs/mobile/implementation-plan.md` | **Phase 7 — PROPOSED, not approved**: the phase summary row, the five tasks with their files, dependencies, verification and reviewer tier, the level table, and the check rows; a step **7b** in the approval gate, which now records that **every row it names is answered, D199's acceptance last**, and approves nothing; an open item; and the cross-task dependency rows. **Re-edited 2026-10-20**: **Global Constraint 4**'s *two items per account* carries a dated amendment to **four** (§4.4). **P7.2 is rewritten to D195's answer** (the always-on statement and the link, in place of the consent gate), and the check rows to **111/112/113**'s new text. |
| `docs/mobile/reconciliation.md` | **D111's own row**, amended 2026-10-20 for **D199's acceptance**: the recommendation column's *"Exactly two items, per account"* keeps its original text, and a **dated note** under it records that the owner accepted D199 on 2026-10-19, that the list is now **four** — the ID token, the per-pairing MAC key, the installation's **Realtime Database URL** and its **web API key** — and that the two new items arrive in the **pairing payload** and live in the same secure storage. The two later sentences that repeat *"two items per account"* (the tension-(b2) note and the summary line) carry the same dated amendment. |
| `docs/mobile/implementation-plan.md` (**Global Constraint 4**) | The constraint that says the app holds **exactly two items per account**, amended 2026-10-20 in the style **Global Constraint 12** already uses: the original sentence stays, and a dated note records D199's acceptance and the **four** items, with §4.4 of this plan as the record and the pairing payload named as where the two new ones come from. |
| `docs/mobile/design/push-notifications.md` | The two sentences that say the phone holds *"exactly two items per account 'in these words and no wider ones'"*, each amended 2026-10-20 with a dated note: the words are D111's and the list they close is now **four**, widened by D199's acceptance and by nothing in that document. |
| `docs/mobile/design/voice-evaluation-switch.md` | The sentence that quotes Global Constraint 4's *"exactly two credential items per account"*, amended 2026-10-20 with a dated note pointing at the same **four**-item record. |
| `docs/mobile/functional-spec-reconciliation.md` | The row that cites **D111 / tension (b2)** for pairing-token lifetime and revocation, amended 2026-10-20 with a dated note: the *two items per account* it repeats is now **four** under D199's acceptance. |
| `docs/coordinator/README.md` | The **numbers table** — the one table in this repository that lists every work's block. The telemetry plan's block is registered there the way the website redesign's is, and the row that read *"**D193 onward** — free"* now reads *"**D201 onward**"*, keeping the original words visible in a dated parenthetical (D129's habit). Its checks row gains the dated note that **111, 112 and 113** were taken on 2026-10-19 from the first free number after every claim — **105–109 are reserved and 110 is claimed**, so none of either is taken and **114 onward stays free**. |
| `internal/cli/docs_mobile_plan_test.go` | The Phase 6 status pin, **rescoped rather than dodged**. Its flag-word half banned `NOT APPROVED` anywhere in the plan, which was a stand-in for *"no approved phase may still claim to be unapproved"* and held only while Phase 6 was the last phase written. Phase 7 is written and genuinely unapproved, so the ban is now applied **per line** to the lines naming `Phase 6` or `P6.`, and the **converse** is asserted for Phase 7 (its heading carries `PROPOSED 2026-10-19` and `NOT APPROVED`; each `P7.n` row carries its own tag). All four mutations were run. |
| `internal/cli/docs_coordinator_test.go` | The coordinator's block table gains the `D193–D200` entry, read from `docs/mobile/README.md` the way the redesign's is read from its own README. |

**Not touched by this change:** every file under `mobile/`, `shared/`, `website/`,
`docs/design/`, and the `docs/website/` documents; `docs/gcp-live-checklist.md` (checks 111–113
are **written by P7.5**, after approval, and no row or section for them exists today — the pin
asserts that); and **anything under `.github/workflows/`** — no workflow is written by this
change and none is staged in `docs/pending-workflows/`, because **no part of this plan's first
release needs one** (a CI job for `mobile/`'s tests would be a later, separately-approved
change, and §10.2 says so).

**On the Go pin, against the brief's "docs only".** The brief asks for a plan and no code, and
this change adds a **test** — `internal/cli/docs_mobile_telemetry_test.go` — as well as the two
existing pins it has to move to register a new phase. That is a judgment call and it is stated
here rather than hidden: this repository's convention is that a planned document is pinned by a
Go test (over forty such pins exist, and both the mailbox plan and the push design landed with
theirs), and a plan whose claims nothing holds is a plan whose numbers can drift silently. The
new file asserts **nothing about an app** — every assertion is about the documents this change
edits, the register, and the two native config files' absence. Its four mutations were run and
are recorded in the file's own header.

---

## 14. UNVERIFIED

The repository's habit is to say what could not be read rather than to smooth it over. Each
line below is a claim this plan makes **without** a source in this tree:

1. **The flag names of §4.2 — including the two that keep the advertising identifier out.** Read
   from memory of the SDKs' documentation, not from a page read on 2026-10-19. P7.1 confirms
   each against the SDK's own documentation and the generated native project, and corrects §4.2
   in the same commit if any name is wrong. The *shape* the plan requires is independent of the
   spelling, and D195's answer is what fixes it: **every collection switch is on, every ad-ID
   switch is off, and no runtime call turns collection on or off.**
   **Two specifics are marked VERIFY in §4.2 and both are about the ad-ID promise**, which is
   why they are called out here rather than left inside that table: (a) the **exact pod or plugin
   option** that makes `@react-native-firebase/analytics` link the Firebase Analytics variant
   with **no ad-ID support** — verified by reading the pinned package's podspec and config-plugin
   source, then the generated `ios/Podfile.lock` for the absence of `AdSupport`; and (b) whether
   removing `com.google.android.gms.permission.AD_ID` from the merged manifest is **the config
   plugin's own option** or a `tools:node="remove"` entry this plan has to add — verified in the
   same plugin source and then in the generated `AndroidManifest.xml`. **If neither exists, the
   plan cannot make the website's sentence true as written** and that is a finding for the owner
   at P7.1's first commit, not something to phrase away. Neither is a live action and neither
   reads a credential.
2. **EAS file secrets.** The `eas secret:create --type file` mechanism and the `eas.json`
   `env` reference of §4.3 are described from memory. **EAS also offers environment variables
   with file visibility**; P7.1 picks the one EAS documents on the day it is written, records
   which, and the plan's requirement is the same either way (the file exists only on the build
   machine).
3. **`@react-native-firebase`'s support for a second Firebase project in one binary.** D199's
   **accepted** answer needs the **default** app to be the telemetry project and each
   installation to be a **named** app (`firebase.app(<account id>)`). The plugin's config, and
   how a named app is given a project the binary does not carry a native file for, are **not
   verified here**. P7.1's first commit reads the SDK's own documentation for it, and if the
   named-app route turns out to be unavailable, **the plan's alternative is a new question for
   the owner** — D199 is answered, and re-opening it is the owner's to do, not P7.1's — the plan
   states both routes rather than assuming the mechanism.
4. **`mobile/app.config.js` and `mobile/eas.json`.** They are P4.4's, merged to `main` by **PR
   #675** since this plan was written. §4.3's guard and §10's P7.1 are written against the shape
   that PR landed — the dynamic `({ config }) => …` export, the three-state `EAS_BUILD` /
   `FUGARO_REQUIRE_IDS` guard, and the three build profiles — and P7.1 re-reads both files at its
   own merge, since a later change to either moves under this plan's sentences.
5. **`dev.fugaro.app`.** The brief's name for the first internal build's environment. It is
   not in this tree. Nothing in this plan is built on it, and no task names it as a file.
6. **The website's privacy statement is read from `origin/main`, not from this branch.** PRs
   **#689** (`89fecab4`) and **#705** (`190b8476`) are on `origin/main` at `66bbcf8d` and **not
   on this branch's base** (`8e3e810a`, still this branch's merge base with `origin/main`, and
   this change merges no base in). §8.3 quotes the two sentences it turns on and describes the
   page by its parts and its headings rather than by line, so a later edit to the page moves
   nothing here — **but P7.4 reads the page as it then stands**, and a **third** change to it
   between this reading and P7.4's merge is not something this plan can see. `website/test/privacy.mjs`
   is named from that commit and **is not on this base**, so this change does not run it.
7. **The two store forms' current contents.** The six Play categories are the owner's own
   description of what was ticked, not a reading of the console. §8.1 answers them from §7's
   allow-list and says which two are over-declared; **only Check 113 confirms what the console
   actually holds.**
8. **No latency, cost, retention or volume number.** Firebase Analytics and Crashlytics are
   free at this scale, and the plan states no figure it has not read. **D194 is answered and the
   retention setting is still not recorded** — §5 step 4 is where the owner notes it, and no
   figure in this document is a measurement.
9. **`fugaro-prod`'s own state.** The facts §5 states about it — that it is the project the
   website is deployed into, that the website's deploy service account holds Firebase Hosting
   roles in it, that it holds no Analytics or Crashlytics read, and that the owner accepted the
   arrangement — are **the owner's answer of 2026-10-19**, recorded here. **Nothing in this
   change reads the project's IAM**, no live action of any kind is taken, and **no service
   account, key or credential is read or named**. If the project's membership differs from the
   answer, §5 is what is wrong and the owner's correction is what fixes it.
10. **Nothing here was verified by running anything.** No `npm test` was run for this change
   (the runner image has no Node: `node --version` → `command not found`), no `expo prebuild`
   ran, and no Firebase project exists. The plan's own pin
   (`internal/cli/docs_mobile_telemetry_test.go`) is the only executable thing this change
   adds, and it is run by the Go suite.

---

*Status of this document: a plan. Nothing is built, nothing is approved, and no code,
configuration or workflow *of the app, the website, the CLI or CI* is written by the change
that adds it — it adds this document, the entries registering it, and the one Go test that pins
them (§13). **The owner answered all eight rows on
2026-10-19**: **D193** (the ruling the plan is built on — Crashlytics and
Analytics, reporting to **`fugaro-prod`**, a Firebase project Fugaro operates), **D194**,
**D195** (**always on** — no switch, no sheet, against this plan's opt-in recommendation,
which stays visible in the row), **D196**, **D197** and **D198** (each as
recommended), **D199 — accepted** (the telemetry project's two native config files are
**built into the app** as EAS file secrets; **each installation's own** Firebase connection
cannot be, so it arrives in the **encrypted pairing payload** and lives in **secure storage**,
which amends D111 from two items per account to **four** — §4.4 states the record), and
**D200** (as recommended). **Answering
a row approves no task**: Phase 7 stays PROPOSED, its step **7b** records that every row it
names is answered while approving nothing, and no task of P7.1–P7.5 may
start. This work takes **D193–D200** — the next free decision block — and **checks 111, 112
and 113**, the first free numbers after every claim (105–109 are reserved and 110 is
claimed), leaving 114 onward free. Cheers: none of the three checks has
been run. Related: [mobile/README.md](../mobile/README.md) ·
[mobile/implementation-plan.md](../mobile/implementation-plan.md) ·
[gcp-live-checklist.md](../gcp-live-checklist.md).*
