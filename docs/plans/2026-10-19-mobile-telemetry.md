# Mobile telemetry: Firebase Crashlytics and Firebase Analytics in the app — implementation plan

**Status: a plan, and the owner has passed the approval gate for it.** Nothing here is built,
and no code, no configuration and no workflow *of the app, the website, the CLI or CI* is
written by this change — the change is this document, the entries that register it, and one Go
test that pins them (`internal/cli/docs_mobile_telemetry_test.go`, stated in full in §13). This
document is the deliverable: it writes down how Firebase Crashlytics and Firebase Analytics
reach the Expo app at `mobile/`. It implements nothing, it changes no command, and it touches no
rules file, no Terraform and no workflow.

**The owner passed the gate on 2026-10-22, and the tasks may start once this plan is merged.**
The gate is [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s: this change
writes the date into it — Phase 7's heading and its step **7b** — so **P7.1–P7.5 are approved**,
on the one condition the gate carries, **that this plan is merged first**. The tasks are still
**unbuilt**: approval is permission to start, and nothing below is a claim that anything
exists. **Amended 2026-10-30: P7.1 is built** (P7.1a's wiring and P7.1b's guard and ad-ID
walk) and **P7.2–P7.5 are not**; the sentence above is the state of 2026-10-22, kept as
written. **This document replaces the earlier draft on `PR #693`** — a branch 151 commits behind
`main`, superseded rather than merged — which asked for **D193–D200** and **checks 111–113**,
numbers the coordinator and the wizard-and-website work had already taken. **This plan takes
D253–D260 and checks 120–122 instead**, and §2 shows the reading that chose them.

**The rulings it is built on, and the date.** On **2026-10-19** the owner decided that the
mobile app **integrates Firebase Crashlytics and Firebase Analytics**, reporting to a
**Firebase project that Fugaro (the project) operates** and **not** the user's own
installation — the owner answered **YES** to *"shared with the developer is true: is that
intended"*. That ruling is recorded here as **D253** (answered) and it is the premise of
every task below.

**The owner answered the rest of the block on the same day, and this document now records
the answers rather than the questions.** §3's *Owner's answer* column carries them one by
one: **D254** (the project is **`fugaro-prod`**, the project that already hosts the
website), **D255** (**always on** — no consent switch and no first-launch sheet, **against
this plan's opt-in recommendation**, which stays visible as the recommendation it was),
**D256**, **D257**, **D258** and **D260** (each as recommended), and **D259**, which the
owner **accepted** — the *telemetry* project's two native config files are **built into the
app** as EAS file secrets, while **each installation's own** Firebase connection cannot be,
because it differs per installation and is unknown at build time: it arrives in the
**encrypted pairing payload** and lives in the phone's **secure storage**. **That acceptance
was amended on 2026-10-29** — the owner ruled that D232 stands, so D111's record stays two items (idToken, macKey) plus the pairing-delivered configuration (the installation's database URL and web API key, ordinary configuration, not secret) — and §4.4 states the record
item by item. §5, §6, §8.3, §9, P7.2 and §12 are rewritten to the answers.

**The answers and the approval are two different acts, and this document keeps them apart.**
The owner answered the eight rows on **2026-10-19**; the owner passed the **gate** on
**2026-10-22**, and the gate is what lets P7.1–P7.5 start — **once this plan is merged**, the
condition the gate carries. The answers alone approved nothing: every task below is still
**gated on the decision rows its "Depends on" line names**, and every one of those rows is
answered. [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s Phase 7 heading
now reads **APPROVED 2026-10-22 (on merge of this plan)**, and its step **7b** records the same
date and the same condition.

**Three things this document deliberately does not claim.**

- **It is not an implementation.** The gate is passed (above), and the tasks below are
  **approved and unbuilt** — **amended 2026-10-30: P7.1 is built, P7.2–P7.5 are not.** Each is still **gated on the decision rows its "Depends on" line
  names** — every one of them is answered — exactly as Phase 6's tasks were gated on D160–D173
  before the owner answered them.
- **It is not built, and no test here holds a behaviour.** Nothing this plan *adds* exists:
  there is no `@react-native-firebase` dependency in `mobile/package.json`, no
  `mobile/firebase.json`, no `mobile/src/telemetry/`, no `shared/telemetry-consent/`, no
  `google-services.json` and no `GoogleService-Info.plist` anywhere in the tree. **Two files
  this plan edits do exist**: `mobile/app.config.js` and `mobile/eas.json` are P4.4's, merged
  to `main` by **PR #675**, and P7.1 **modifies** them (§1's third table row, P7.1's "Files").
  The one Go test this change adds
  (`internal/cli/docs_mobile_telemetry_test.go`) pins **this document's own claims** and
  nothing else. **The bullet above is kept as written: it describes 2026-10-19. Amended
  2026-10-30 (Task P7.1a, the first half of P7.1):** the three `@react-native-firebase`
  packages and `expo-build-properties` are now in `mobile/package.json`, `mobile/firebase.json`
  exists, and `mobile/app.config.js` registers the config plugins; the native config files are
  still absent from the tree, and are ignored by `mobile/.gitignore` so they stay absent.
  **Amended again 2026-10-30 (Task P7.1b, the second half of P7.1)**: the
  build-time guard for the native config files (**D260**) and the ad-ID walk
  are in the tree, so **P7.1 is built** and this document's status lines say so;
  the native config files themselves are still absent, and are ignored by
  `mobile/.gitignore` so they stay absent. **Still to come, in P7.2–P7.5**:
  `mobile/src/telemetry/`, the always-on
  statement and the link, the closed event allow-list and the scan that holds
  it, and the store labels and the website's privacy paragraph. **Amended
  2026-10-31: `shared/telemetry-consent/`, named here in the first version of
  this sentence, is dropped — §6 removes it with the consent gate D255's answer
  dropped, so it is nothing a task builds.**
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

Every fact below was read on **`main` at `4d619273`** — this change's base, and the tree it
edits — and each one names the file it was read from. Nothing is
inferred from a plan. **Where the file a fact comes from is one this plan does not own, it is
cited by symbol or by phrase rather than by line** — the convention
[coordinator/README.md](../coordinator/README.md) states, and for the reason it gives: those
documents gain rows and paragraphs while this one sits in review, and a line number moves under
the sentence it was written for without anything looking wrong.

| Fact | Where it was read | What it means here |
|---|---|---|
| The app is an Expo app at `mobile/`, its own artifact outside the Go module, with **no Firebase SDK wired at all** | `mobile/package.json` (`engines: node ">=22 <23"`, `npm ">=10"`, `test: node --test "src/**/*.test.js"`); `mobile/index.js`, whose imports were read in full: `@react-native-async-storage/async-storage`, `expo` (`registerRootComponent`), `expo-secure-store`, `react`, `react-native`, and this app's own modules under `src/` — **no Firebase import of any kind**. The storage package is `@react-native-async-storage/async-storage` and not `expo-async-storage`: that name is the app's **own port module** at `mobile/src/accounts/expo-async-storage.js`, which the entry imports as `./src/accounts/expo-async-storage.js` | The telemetry work is the **first** thing that would put a Firebase SDK in the app. It therefore meets the app's unbuilt Firebase wiring head-on, and §3's **D259** is that question — **answered 2026-10-19, and the answer is §4.4's three-way split.** |
| `mobile/app.json` carries the **EAS project id** and nothing Firebase-shaped | `mobile/app.json` (`expo.extra.eas.projectId = "70e67d4a-77d3-4110-9db6-e600266db345"`) | The EAS project the native config files would be delivered into already exists. |
| **`mobile/app.config.js` and `mobile/eas.json` are on `main`** — P4.4's, merged by **PR #675** for the store path (check 89) | `ls mobile/`: `README.md`, `app.config.js`, `app.json`, `eas.json`, `index.js`, `package-lock.json`, `package.json`, `src/`; `mobile/eas.json` carries three build profiles — **`development`**, **`preview`** and **`internal`** — over the EAS environments `development`, `preview` and `production` | P7.1 **modifies `app.config.js`** — the plugin entry and the build-time guard — **and the Firebase entries in `mobile/.gitignore`**. **`eas.json` is not modified: retired 2026-10-30.** The planned two `env` entries in each of its three build profiles are the wrong mechanism; the files are delivered by **EAS environment variables with file visibility**, set in the EAS environments the profiles name through `eas.json`'s `environment` key, and an `env` block there would override them (§4.3 step 4, §14 item 2). **Neither file is created by this change, and this plan no longer waits on #675.** |
| The app's account tuple is **four fields and holds no connection value** | `mobile/src/accounts/account.js` — `ACCOUNT_FIELDS = ['id', 'firebaseProjectId', 'authUid', 'fugaroProject']` | There is no `rtdbURL` and no web API key in an account today. **D259 (accepted 2026-10-19) is the row that puts them in secure storage rather than in the tuple**, and §3 and §4.4 explain why. |
| The settings store **refuses an `AIza…` value outright**, and refuses the *word* `apiKey` as a key segment | `mobile/src/accounts/settings.js` — `SECRET_SHAPES` (whose Google entry is `['google-api-key', /^AIza[0-9A-Za-z_-]{35}$/]`), `judge` (which returns `SECRET_VALUE` for a match) and **`FORBIDDEN_KEY_SEGMENTS`**, the frozen list `apiKey` is a member of. **Cited by symbol, not by line**: a line number into a file this plan does not edit goes stale silently, and the symbols are what a reader greps for. | **This is the repository rule the brief names**: the Firebase web API key appears nowhere the app stores or the app ships. It is why D259's accepted answer puts the installation's connection values in **secure storage**, not in the tuple's settings entry — and why the store's fence is left exactly as it is (§4.4). |
| The app's own config test already holds that rule for `app.json`, **with a planted `AIza…` value as its positive control** | `mobile/src/app-config.test.js` — the planted `expo.extra.firebase.apiKey` in its sample config, the `['$.expo.extra.firebase.apiKey', 'google-api-key']` entry in its walk's list, and the two assertions that `bundleIdentifier` and `package` are absent from `app.json`. **Cited by symbol, not by line**: P7.1 edits this file in this phase, so its line numbers move under anything written against them | The fence exists and is mutation-proven. P7.1 extends it to the two **native** config files and to `app.config.js`; it does not invent a new rule. |
| The app's source is already scanned for a `firebase` SDK import, and the scan's pattern **does not match `@react-native-firebase/…`** | `mobile/src/mailbox/write-path.test.js` — rule 2, whose `pattern` is `/['"]@?firebase\/[a-z-]+['"]/` with `allow: ['index.js']` | A screen could import `@react-native-firebase/analytics` today and no fence would catch it. P7.3 adds that rule; it is a **real gap**, not a hypothetical one. |
| The app writes to the database in exactly one file, and the fence is a scan with a positive control | `mobile/src/mailbox/write-path.test.js` (rules 1–3), `mobile/src/boundary.test.js` | The shape P7.3's allow-list scan copies, deliberately. |
| The app's first screen says **"No account yet. Add an installation to begin."** | `mobile/src/app/root.js` — `NO_ACCOUNT` | The first launch is an empty account list, and **D255's always-on answer leaves it exactly as it is**: there is no first-launch sheet to design in, and **the app has no settings screen for the statement to live in**: the statement goes in the shell, as `SHELL.NOTICE` (§6). |
| A redactor already exists and is shared | `shared/digest/index.js` (P1.3, merged) — `redact`, `REDACTED`, `redactFunc`, `redactForms` | §7's breadcrumb rule **binds** it; it does not write a second one. |
| **No CI job runs `mobile/`'s tests, and none runs `shared/`'s** | `.github/workflows/ci.yml` has four jobs — `changes`, `test`, `rules`, `terraform` — and none of them runs `npm test` in `mobile/` or `shared/`; `docs/pending-workflows/shared-tests.yml` is the staged job for `shared/` (P4.3, owner-made) | Every mobile-side verification below is a **local** `npm test`, and this plan says so rather than pretending CI holds it. A job for `mobile/` would be a **staged workflow**, and this change stages none (§13). |
| The store path is owner-run, and check 89 is the check that proves it | `docs/gcp-live-checklist.md` — the register's `| 89 |` row and its `## Check 89` section, cited by those anchors rather than by line: one row per check and one section per check, and both carry the number | Telemetry changes what check 89's installation step can see, and it adds labels the stores ask for **before** a build is uploaded (§8). |
| The site's analytics statement is a **flag with two branches**, and the flag is ON | `website/src/pages/privacy.astro` — the `site.facts.analytics.enabled` constant, the on-branch that names Google Analytics 4, one page view, no event, no user id and the consent banner, and the off-branch that says *"This site has no analytics at all"*; and `website/data/site.yaml`'s `analytics` block (D93's). **Cited by symbol and by phrase, not by line**: the page is edited by the website work, and P7.4 lands against it | **The page already carries the app's paragraph, and it is in this tree** — PR **#689** split it into *The website* and *The software* and added **The phone app**, and PR **#705** wrote D255's answer into it (*"There is no switch inside the app to turn them off"*, *"Earlier builds of the app send nothing at all"*); **both are merged to `main` at this change's base** (§8.3) |
| The site's own "nothing phones home" check is written and run by the owner | `docs/gcp-live-checklist.md` — the register's `| 69b |` row and its `## Check 69b` section, cited the same way | It is about the **page**, not the app; the app's counterpart is a new check (§12), not an amendment of 69b. |

**`dev.fugaro.app`.** The brief calls the first MVP *"voice-less internal testing on
`dev.fugaro.app` for both stores"*. That host is **not in this tree** — the only `fugaro.app`
string is the app shell's root element name (`mobile/src/app/root.js` — `SHELL.ROOT`), which
is an element-type name and not a host — so the plan treats `dev.fugaro.app` as the owner's
name for the first internal build's environment and **does not build anything on it**
(§13, UNVERIFIED).

---

## 2. The numbers this plan takes

### 2.1 Decisions: **D253–D260**, and why those

**The block is read from the tree, not remembered.** The register of this work's own
allocations is [mobile/README.md](../mobile/README.md)'s table, and the rule both this plan and
that table use is the coordinator's own: *"whichever of two open claims merges second renumbers
itself, to the number free at its own merge"* — **the highest number written anywhere in
`docs/` decides, and an unused number is not a free number.** [coordinator/README.md](../coordinator/README.md)'s
*"The numbers this work holds"* table is a dated reading of that rule and not a fact about the
tree: it still ends *"**D198 onward is free.**"*, which was true on 2026-10-21 and is not true
now. So the block is read the way the five works after the coordinator read it — from the
documents that took numbers:

| Block | Holder | State |
|---|---|---|
| D1–D20 · D21–D38 · D39–D54b · D55–D78 · D90–D109 · D110–D129 · D130–D159 · D160–D179 | console, console v2, voice, managed projects, website, mobile, the credentials service, the functional spec v0.1 | taken; D174–D179 reserved and unused |
| **D180–D192** | the website redesign (the redesign's own reconciliation says *"Decisions are numbered `D180` onward"*) | proposed |
| **D193–D197** | the coordinator ([coordinator/reconciliation.md](../coordinator/reconciliation.md) §6) | **answered 2026-10-21** — D196 against its recommendation |
| **D198–D200** | — | **unwritten**: three numbers, and no document has written a decision in them. **This plan takes none of them**, because it needs eight contiguous numbers and the next one up is spoken for |
| **D201–D220** | the voice-session work ([voice-session/reconciliation.md](../mobile/voice-session/reconciliation.md)) | written; **all twenty open** |
| **D221–D236** | the mobile-auth work ([mobile-auth/reconciliation.md](../mobile-auth/reconciliation.md)) | written |
| **D237–D252** | the website zoom-diagrams work ([../website/zoom-diagrams/reconciliation.md](../website/zoom-diagrams/reconciliation.md)) | written; **all sixteen answered 2026-10-21**, and **D252 is the highest number written anywhere in `docs/`** |
| **D253–D260** | **this plan** | **taken by this document, and no decision number is taken twice** |

**D253–D260 is the next free block, and this document is where it is taken.** It takes
**eight** numbers — eight because this work has eight questions (§3) — and it takes them
**starting at the first free number**, not at a round one. It renumbers nothing and it reuses
nothing: D180–D192 keep the numbers the redesign proposed, D174–D179 stay reserved and unused,
**D79–D89 stay deliberately unused** (`docs/website/redesign/implementation-plan.md`: *"an
unused number is not a free number"*), and **D198–D200 stay unwritten** rather than being split
off from the block this work needs. **The earlier draft on PR #693 asked for D193–D200** — six
of the eight already answered by the coordinator, and the last two inside a range two later
works have read differently — which is why the block moved rather than the collision being
argued.

**All eight rows are answered**, by the owner on 2026-10-19, and each answer is written into
the row that carries it. The block reads, row by row:

- **D253: answered, owner 2026-10-19** — the ruling the plan is built on.
- **D254: answered, owner 2026-10-19** — the project is **`fugaro-prod`**.
- **D255: answered, owner 2026-10-19** — **always on**, against this plan's recommendation.
- **D256: answered, owner 2026-10-19** — as recommended.
- **D257: answered, owner 2026-10-19** — as recommended.
- **D258: answered, owner 2026-10-19** — the second build carries the SDKs.
- **D259: accepted, owner 2026-10-19** — the telemetry project's native config is **built
  into the app**; each installation's own Firebase connection arrives in the **pairing
  payload** and lives in **secure storage** (§4.4).
- **D260: answered, owner 2026-10-19** — as recommended.

**An answered row approves no task on its own** — §3 says so; the gate was passed separately,
on **2026-10-22**, and only for a plan that is merged.

### 2.2 Checks: **120, 121 and 122**, and why those

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
- **111, 112 and 113 are written into the register and are not free.** **111** is the setup
  wizard's — the billing picker's failure modes — and **112** and **113** are the website
  zoom-diagrams plan's (the four zoom levels, and the phone app's page): each has a summary row
  and a `## Check` section in [gcp-live-checklist.md](../gcp-live-checklist.md), each is
  owner-run, and **none has been run**. **This plan takes none of them** — and the draft on
  **PR #693** did, which is half of why it is replaced rather than merged.
- **114–118 are numbered by the voice-session work** ([voice-session/reconciliation.md](../mobile/voice-session/reconciliation.md),
  its **D219**, written and open — no answer of the owner's settles it) **and not written**: the
  register carries no `## Check 114` … `## Check 118` section. 113 overlaps the zoom-diagrams
  plan's written section, which is the older claim and stands, so the voice-session block is
  **114–118** and **this plan takes none of it** either.
- **119 is held by two works, and one of them takes it.** [../rigor-modes/implementation-plan.md](../rigor-modes/implementation-plan.md)
  **takes** it — *"the next free number is **119**, and this plan takes it"*, its **T8.1** writes
  the check and its T8.2 runs it — which is a taken claim and not a proposal. The
  console-on-Windows work ([../console-windows/README.md](../console-windows/README.md)) **proposes**
  the same number and claims nothing (its D12(a): *"proposed as **Check 119** and **claimed by
  nobody**"*). Both are claims and neither is written: the register carries no `## Check 119`
  section. A proposal is not a reservation — but it is a claim, and taking a proposed number is
  how two works end up writing the same check. **This plan takes neither**, and the stronger
  claim is named here because reading only the weaker one would understate what 119 is held by.

**So the first free number is 120**, and it is 120 **before and after #667 merges**: 105–109 stay
reserved, 110 stays claimed, 111–113 stay written and 114–119 stay claimed, so the block does not
move. **This plan takes 120, 121 and 122** — three numbers, one per check, and **nothing above
them** — leaving **123 onward free** (amended 2026-10-29: the register has since written
Checks 123 and 124, the console dependency graph and the Cloud Batch backend; 120–122 are still
unused there). All three are **owner-run**, none is a CI job, and **none is
run by an agent**. §12 writes their text. **Check numbers 80–89 stay the mobile plan's**: this
work adds no row to that block, because a reserved number is never reused.

**The highest check section written is a reading, and it is read rather than remembered.** The
highest `## Check N` section in the register at this reading is **`## Check 113`** — the
zoom-diagrams plan's phone-app page — above the setup wizard's `## Check 111`, that plan's
`## Check 112`, and P4.2a's `## Check 81`, `## Check 82` and `## Check 89`. *(An earlier draft of
this plan read the highest written section as `## Check 70c` and said none of 80–89 had a section.
That was false, and it is the reading this paragraph exists to replace.)* **P6.11 (#667) appends
`## Check 100`–`## Check 104`**, which does not raise that reading — 104 is below 113 — and
**moves nothing this plan takes**: the block is chosen from the claims above, not from the
register's highest written number, so the reading can move without the block moving.

**The open pull requests this plan lands against.** Two, and each one edits lines this plan
edits: **#632** (the orchestrator's check 110 — this section and the coordinator's numbers table)
and **#667** (P6.11: it writes `## Check 100`–`## Check 104` and their summary rows into the
register, and edits the same check rows in `docs/coordinator/README.md` and `docs/mobile/README.md`
that this plan edits). **#667 is the reason this plan takes nothing out of 100–109** — that block
is the one its own pull request writes — and naming it beside the other is what keeps the two
changes from colliding whichever merges second.

**Three pull requests this plan named have merged, and all three are in this change's base.**
**#675** (`mobile/app.config.js` and `mobile/eas.json` — P4.4's store path) is merged, so **P7.1
modifies `app.config.js` in place** rather than waiting on it: the plugin entry, the build-time
guard and the ignore entries (**the two `env` entries in each of `eas.json`'s three build profiles
are retired 2026-10-30; `eas.json` is not modified at all, see §4.3 step 4**) (§1's third table
row, §4.3, P7.1), and no "Depends on" line below names
#675. **#689** (the website's privacy statement, now in two parts — the site and the software)
and **#705** (*the app's analytics and crash reporting are always on*, the owner's ruling written
into that page) are merged too, so **the page is in this tree and not a reading of another
branch**: it already carries the app's paragraph, it already says the reports are always on and
that there is no switch, and it already says *"Earlier builds of the app send nothing at all."*
**The page is ahead of the SDKs by design and says so**, and §8.3 and P7.4 hold the live page's
sentences true of the build that ships rather than adding them.

---

## 3. Owner decisions (D253–D260)

Eight rows, and the owner answered **all eight** on **2026-10-19**. **D253** is the ruling
the plan is built on; **D254**, **D255**, **D256**, **D257**, **D258**, **D259** and
**D260** carry the owner's answers in the right-hand column — **D255 against this plan's
recommendation**, **D259 accepted** (the owner asked *"firebase config should be built into
the app, no?"* and then took the recommendation), the rest as recommended, with each
superseded recommendation and each question left visible in the column it was written in
(D129's habit) rather than deleted. **Answering a row does not approve a task** — the owner approved the phase separately, on
**2026-10-22** and on the condition that this plan is merged, and [mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
step **7b** records that date beside the answers.

| # | Question | Recommendation | The alternative, and what it costs | Owner's answer |
|---|---|---|---|---|
| **D253** | **Does the app carry Crashlytics and Analytics at all, and to whose Firebase project do they report?** The app serves **several installations** (D114), each with its own Firebase project, and the app signs in to that installation's Firebase with a **user** credential (D112, D111). "The app's analytics" is therefore ambiguous: the installation's project, or one the Fugaro project operates? | **Yes to both SDKs, and to one Firebase project that Fugaro (the project) operates** — not an installation's, not a user's. Reason: the question the telemetry answers is *"does the app work for people who are not the owner"*, and an installation's project can only answer it for that installation's owner; and no agent, and not the Fugaro owner, has any business reading a user's own project. | **In the installation's project.** Cost: the owner (and nobody else) can see the crashes, the data is the user's own project's, and *"shared with the developer"* is false for every install — the telemetry is useless for exactly the population it exists to measure. **Or no telemetry at all.** Cost: the app ships to two stores with no crash visibility, and the first real crash is reported by a user in prose, if at all. | **Owner answered 2026-10-19: yes to both SDKs, reporting to a Firebase project that Fugaro operates — not the user's own installation.** The owner answered **YES** to *"shared with the developer is true: is that intended"*. *Why:* the app is measured as one product, not per installation. **This row approves no task and allocates no phase.** |
| **D254** | **Who creates and operates the Fugaro-operated Firebase project, and who may be a member of it?** A Firebase project is a Google Cloud project under an organization; its data leaves the user's machine and lands in a project with a membership list. | **The owner creates it, alone, by hand, and the only member is the owner.** One project (suggested id `fugaro-telemetry`, under the same organization as the installations, with its own billing link), one Firebase **Analytics** property and one **Crashlytics** app per platform, **two members at most — the owner's own account** — and **no service account, no CI identity and no agent access**. The app id it registers reads: *the app reports crashes and a closed set of events to a Fugaro-operated project; nobody else can read them; nothing from the mailbox or a message is in them.* The project is created **before** P7.1 can produce a config file, because the config file is the project's own. | **Put telemetry in an installation's project** (`fugaro-dev`, say). Cost: crashes from every tester land in the development installation's project, mixed with its own budgets and runs, readable by every launcher and budget admin of that installation (D113's grants) — telemetry readable by a *run token* is not telemetry a user can be told about. **Or a third-party crash service.** Cost: a second vendor, a second DPA, a second retention question, and the owner has already ruled for Crashlytics (D253). **Or let an agent create it.** Cost: an agent holding project-creation authority in the owner's organization. | **Owner answered 2026-10-19: the project is `fugaro-prod`** — the owner's own name for it, **not this plan's suggested `fugaro-telemetry`** — and it is **the same Firebase / Google Cloud project that already hosts the website**. *Three facts the answer carries, which §5 states plainly because they are facts about this repository and not preferences:* the owner **creates the two Crashlytics and Analytics apps in it by hand**; **the only human member is the owner**; **no service account may be granted any Analytics or Crashlytics read**; and **the website's deploy service account does hold Firebase Hosting roles in that same project** — Hosting, and no read of analytics or crash data — which **the owner accepted** when answering. §5's owner steps are rewritten for `fugaro-prod`. **This row approves no task and allocates no phase.** |
| **D255** | **Consent and opt-out: is analytics collection OFF until the user opts in on first launch, or ON with an in-app switch and a link to `/privacy`? And is Crashlytics deferred the same way?** | **Collection defaults OFF until the user opts in — recommended: opt-in — and Crashlytics deferred the same way** — one switch, both SDKs, nothing collected before it is on. **The recommendation, not a ruling**: the owner has not yet answered opt-in against opt-out, and this row is where that choice is made. **One thing is not contingent on the answer**: the advertising identifier is collected under **neither** option — §4.2's switches are `false` in the build, Android declares no `AD_ID`, iOS links no `AdSupport`, and §7.4's test fails if that moves — because the website's privacy page promises *the app carries no advertising identifier* and a promise on the owner's own domain does not depend on a consent default. The gate is a **first-launch sheet** carrying the switch, the sentence *"Fugaro sends anonymous usage and crash reports to Fugaro's own servers. No message, no mailbox and no account is ever sent. You can turn this off in Settings."*, and a link to `https://fugaro.dev/privacy/`. The choice is stored **on the device** only, and the sheet is shown again only from Settings. Reason: the user is in the EU (the owner's own brief says so), where reading or writing anything on a device that is not strictly necessary needs consent, and the SDKs write an app-instance id to the device the moment they start; and the site's own analytics already carries exactly this shape (D93 — the same page's consent-banner branch, cited by symbol), so the app and the site say the same thing in the same words. | **ON by default with a switch to turn it off** — the common shape and the one the stores expect. Cost: collection begins **before** the sheet is ever shown, so the first event, the app-instance id and any crash in the first seconds are collected without consent; in the EU that is the shape the site's own consent banner exists to avoid, and having the site ask and the app not is a contradiction a reader will notice. **Or opt-in for Analytics and always-on for Crashlytics** (crash reports are widely treated as a legitimate interest). Cost: a second consent story, a second paragraph in §8, and two switches where the brief asks for one. | **Owner answered 2026-10-19: ALWAYS ON.** Collection is **on in the builds that carry the SDKs**; there is **no consent switch, no first-launch sheet, no stored choice and no `telemetry.consent` key**, and the app has nothing the user can turn off short of uninstalling it. **The answer SUPERSEDES the recommendation in the column to its left**, which stays visible as the recommendation it was; §6, P7.2, §9, §12 and every dependent sentence are rewritten to the answer. *Two things the answer does not change:* the **advertising-identifier ban holds under always-on exactly as it held under opt-in** — §4.2's two ad switches stay `false`, Android declares no `AD_ID`, iOS links no `AdSupport`, and §7.4's build-time test is what keeps all three true — and the **collection switches are now the collection**, so §4.2's table requires them `true` and §7.4 refuses the runtime calls that could act as a switch this ruling says does not exist. **The website's privacy statement already states both halves of the answer in its own words** — *"The app versions that carry these reports send them. There is no switch inside the app to turn them off"* and *"Earlier builds of the app send nothing at all."* (read on `main` at this change's base, PR **#705**, merged) — so the page and the ruling agree, ahead of the build. **This row approves no task and allocates no phase.** |
| **D256** | **What is sent, what is never sent, and what holds that line?** | **A closed allow-list of event names and parameter values, and a ban list that is checked by a scan** (§7). Nothing that names a message, a mailbox, an account, an installation or a repository is ever sent; the crash path is redacted through `shared/digest`'s `redact`; `setUserId`, `setUserProperty` and free-text custom keys are refused **outright** and a test fails on the call. Reason: the app's whole security model (D111–D113) is that a phone holds two items and writes two paths; a telemetry SDK that can carry free text is a third channel, and it is the only one that leaves the user's own infrastructure. | **Send everything the SDK collects by default and rely on the ban list being respected.** Cost: Firebase Analytics' default events carry an app-instance id and screen names, and Crashlytics' default reports carry the device model, the OS, log output and — unless it is switched off — the advertising id; "we never send message content" becomes an assertion about a library's defaults rather than about our code. **Or no allow-list, only review.** Cost: the first screen that logs a node path ships it, and no test notices. | **Owner answered 2026-10-19: as recommended.** The closed allow-list of §7.2, the ban list of §7.1, the redaction rule through `shared/digest`'s `redact` (§7.3), and `setUserId`, `setUserProperty`, `setAttributes`/`setAttribute` and free-text custom keys **refused outright** by the scan of §7.4. **This row approves no task and allocates no phase.** |
| **D257** | **The store labels and the privacy page: which categories, and when do the website's words change?** The owner has already ticked Play's Data safety form: **Crash logs · Diagnostics · Other app performance data · Device or other IDs · User IDs · Other in-app messages**. The site's privacy page (`website/src/pages/privacy.astro`) describes the website's GA4 and says nothing about an app; PR **#689** (merged) keeps the site free of an analytics SDK claim. | **Answer the labels from what the SDKs actually collect, and change the website's page in the same release that ships the SDKs — not before, and not after.** The ticked set stays as it is (§8 explains, category by category, which of the six the app's own traffic justifies), the **App Store** label is answered from the same list, and the site's privacy page gains **the app's paragraph** in the same commit as the first build that carries a telemetry SDK. Reason: a label written before the traffic has been read is a guess, and a privacy page that promises "no analytics SDK" while a store build carries one is a false statement on the owner's own domain. | **Change the website page first, then ship the SDKs.** Cost: between the two commits the site says the app sends telemetry and the app sends none — the safe direction, but the page must then be honest about a build that does not exist. **Or ship the SDKs and change the page later.** Cost: the exact falsehood the brief warns about, published on `fugaro.dev/privacy/` for as long as the gap lasts. **Or narrow the ticked set before shipping.** Cost: a form re-answered twice, against a policy risk in the direction that matters (under-declaring). | **Owner answered 2026-10-19: as recommended** — the two store forms are answered from what the SDKs actually collect (§8.1, §8.2), and the website's words move with the release that ships the SDKs. **One fact has moved under that answer, and the plan records it rather than smoothing it over:** the privacy statement **already carries the app's paragraph and already says the reports are always on** (read on `main` at this change's base, PRs **#689** and **#705**, both merged), so **the page is AHEAD of the SDKs by design and says so itself** (*"Earlier builds of the app send nothing at all."*). §8.3 is therefore rewritten from *"change the page in the same release"* to *"hold the live page's sentences true of the build that ships"*, and P7.4 follows §8.3. **This row approves no task and allocates no phase.** |
| **D258** | **Which build carries the SDKs: the first, voice-less internal build on both stores, or the second?** | **The second build.** The first internal build ships **neither SDK**: its job is to prove the store path (P4.4, check 89), the development build, and the app's own wiring on a real device, and every one of those is easier to read without a native config plugin in the same change. The consent sheet, the allow-list scan, the redaction rule, the labels and the privacy page ship as **one unit** with the SDKs, and a first build that carried the SDKs without the website's paragraph would be D257's own failure mode, one release early. | **Crashlytics alone in the first build, Analytics in the second.** Cost: the labels and the privacy page are answered twice, and the first build still needs the consent gate (D255 defers both SDKs the same way), so the "one unit" is already split. **Or both in the first build.** Cost: the store path, the native project and the telemetry contract all change in one release, and a failure in any of the three is hard to attribute; it also puts the labels and the website paragraph under the first build's deadline. | **Owner answered 2026-10-19: the SECOND build carries the SDKs; the first internal build ships neither**, as recommended. §9 says what that means now that D255 is always-on: the first build's job is still the store path, and it carries **no** telemetry and **no** native config plugin. **This row approves no task and allocates no phase.** |
| **D259** | **Which Firebase project do the app's native config files name, and where does the app's *other* Firebase configuration come from?** A build carries **one** `google-services.json` and **one** `GoogleService-Info.plist`, and whichever project they name is the app's **default** Firebase app. The app serves several installations (D114), and the tuple it stores carries **no** connection values (`ACCOUNT_FIELDS`), while the settings store **refuses an `AIza…` value** — `SECRET_SHAPES`'s `google-api-key` entry, applied by `judge`'s `SECRET_VALUE` branch. | **The native files name the Fugaro-operated telemetry project** (D254), so the **default app is the telemetry app** — the only project that can be compiled into one build serving every installation. **Every installation's Firebase becomes a named app** (`firebase.app(<account id>)`), built at runtime from the account. **The consequence was the owner's to accept, and it is the point of this row — the owner accepted it on 2026-10-19:** the tuple must gain the two connection values it does not carry today — the installation's Realtime Database URL and its web API key (`init_firebase.go` records both; G18 already reads them for the QR) — and they **cannot** live in the settings entry, because `SettingsStore` refuses that value shape. They therefore go to **secure storage**, which widens **D111**'s list from two items to **four**. D111's own cell says why this is a row and not a detail: *"D54b's precedent is to list every item held, not only the obvious one."* | **Name an installation's project in the native files.** Cost: one build serves one installation, which is exactly what D114 refuses (*"several, non-overlapping"*), and a second installation needs a second build and a second store submission. **Or exempt the `AIza…` shape in the settings store** so the tuple can carry it. Cost: it weakens a fence (`settings.js`) that protects the *whole* app, to hold a value the repository already documents as *not* a secret — the wrong trade: move the value, not the fence. **Or keep the default app as the installation's and give telemetry a named app** built from a JS config object. Cost: the JS config object is where the apiKey would have to be written — which is D253's rule broken in the one place the repository's own test (the planted `apiKey` control in `mobile/src/app-config.test.js`) exists to catch. | **Owner answered 2026-10-19: ACCEPTED.** The row was first answered with a question — *"firebase config should be built into the app, no?"* — and the question and its clarification are kept visible below (D129's habit); **the owner then accepted the recommendation**, consequence and all, and this cell is the record. **What the answer settles, in three parts — §4.4 states it item by item, and D111's record carries the amendment:** **(1) Built into the app:** the *telemetry* project's two native config files, naming **`fugaro-prod`** (D254), delivered as **EAS file secrets** and applied at prebuild (§4.3, D260), so the default Firebase app **every** build carries **is** the Fugaro project's. **(2) In the pairing payload:** **each installation's own** Firebase connection — its Realtime Database URL and its web API key. They differ per installation and **are not known at build time**, so no build can carry them: they arrive **inside the encrypted pairing payload from the user's own installation** (the CLI records both, `init_firebase.go`; G18 already reads them for the QR) and travel under **D113b**'s pairing key. **(3) In secure storage:** those same two values, per account, joined to D111's **ID token** and **per-pairing MAC key** — **four items per account**, never in git, never a fixture, never the settings entry, never a log line. **The consequence the owner accepted with the answer:** **D111's *two items per account* is now four**, and the settings store's refusal of API-key-shaped values protects **less than it did** — `SECRET_SHAPES` is **not** relaxed and a settings entry may still never carry an `AIza…` value, but this plan's stronger sentence, that the app stores no such value *anywhere*, no longer holds once the installation's own key lives in secure storage by design. **What the answer does not do:** it approves no task, and it does not settle whether the *product* uses the value — the app still wires **no** Firebase SDK until a task is approved. **The row is ANSWERED, and P7.1's *Depends on* line names a ruling rather than a question.** **Amended 2026-10-29 — the owner chose D232 over D259's four-item list.** The answer above stays visible as written, and its part (3), the four items in secure storage, is superseded: the phone's secure-store items stay **two** (idToken, macKey), and the installation's database URL and web API key are **ordinary configuration, not secret**, delivered in the pairing payload. The code still allows only the two `SECRET_ITEMS` in `mobile/src/accounts/secret.js`, and where the configuration is stored is the choice of task P6.8 of [2026-10-22-mobile-pairing-cli.md](2026-10-22-mobile-pairing-cli.md). Parts (1) and (2) stand. |
| **D260** | **Expo Go, the development build, and how the native config files reach a build.** `@react-native-firebase`'s config plugin changes the **native** project, which Expo Go cannot carry. | **The app's development workflow becomes a development build; Expo Go stops being a supported way to run it, and the change is stated in `mobile/README.md` in the same commit that adds the plugin.** The native config files are **never committed** and reach a build as **EAS file secrets** (§4.3), named in `eas.json` by environment variable; a CI/EAS build that cannot see them **fails loudly** rather than building an app with no Firebase. Reason: silently building without the config produces an app whose crash reporting is dead in a way nobody notices until a crash is missing — the worst possible failure for a telemetry feature. | **Keep Expo Go working by making the plugin optional.** Cost: two native projects, two code paths, and a plugin whose presence depends on how the app was started — the configuration matrix is where telemetry bugs live. **Or commit the native config files.** Cost: forbidden. They name the project, and `.gitignore` plus the scan in §4.3 exist to make that structural rather than a matter of discipline. | **Owner answered 2026-10-19: yes, as recommended.** The app's development workflow becomes a **development build** and **Expo Go stops being a supported way to run the app**, stated in `mobile/README.md` in the same commit that adds the plugin; the two native config files are **never committed** and reach a build as **EAS file secrets**; and a build that cannot see them **fails loudly** rather than shipping an app with no Firebase. **This row approves no task and allocates no phase.** |

---

## 4. How Firebase reaches an Expo app (Task P7.1)

### 4.1 The mechanism, named

`@react-native-firebase` is the supported path for Crashlytics in an Expo app, and it is a
**config plugin**: it is listed in the app's Expo config (`plugins`), it is applied when the
native project is generated (`npx expo prebuild`, or EAS's own prebuild), and it reads the
project's own `google-services.json` (Android) and `GoogleService-Info.plist` (iOS) at that
moment. The three consequences that decide half this plan:

1. **A development build is required.** Expo Go is a fixed binary that cannot carry the
   plugin, the native SDKs or the config files. **D260 is that row, and the owner answered it
   yes on 2026-10-19** — the app's development workflow becomes a development build, and
   `mobile/README.md` says so in the same commit that adds the plugin.
2. **The native config files are what names the project.** No `apiKey` is written in
   JavaScript anywhere: the JS side asks the native module, which read the file. **That is
   why D259's accepted answer is possible at all** — and why the alternative (a JS config
   object for the named app) would put an `AIza…` value in the app's source, which
   the planted `apiKey` control in `mobile/src/app-config.test.js` refuses today.
3. **The native files are per-build artifacts, not source.** They are generated in the
   Firebase console for a specific project, they name that project, and they carry its API
   key. §4.3 is how they are kept out of git and how a build gets them. **They ARE built into
   the app** — that is what the plugin does, and it is the answer to the owner's question on
   D259: the *telemetry* project's config is compiled in. **An installation's own** Firebase
   connection is the one thing that cannot be, and D259 is where that was decided — **the
   owner accepted it on 2026-10-19**, and §4.4 states the resulting record item by item.

### 4.2 The flags, named exactly

The plan names every flag it depends on. The table below is **one requirement**, and the
grouping that describes it is **not** "what" against "whether" — that was the shape of the
opt-in table this plan recommended and the owner did not take, and reading this one that way
has an implementer build four collection switches `false`, which is the table §7.4's
build-time test refuses. What this table holds is: **four native collection switches that
D255's always-on answer puts on** (`true`), **two `firebase.json` auto-collection switches**,
also `true`, and **two ad-ID switches thrown to `false` and left there** — the ad-ID pair is
thrown to `false` under D255's answer exactly as it was thrown to `false` under the opt-in
recommendation, and it is not one of the four native collection switches. Alongside the
switches the table holds **two rows that are not switch values at all** — on iOS the app links
no `AdSupport` framework, and on Android the `AD_ID` permission is absent from the merged
manifest — and **two runtime calls that are never made**. Read down the value column that is
**`true` six times** and **`false` twice**, which is the arithmetic the paragraph under the
table also states, and the table below is the whole of it.

The half that changed when the owner answered is the collection half: this table used to
require all four collection switches `false`, so that "nothing is collected before consent"
was true of the *native* SDK and not merely of our JavaScript, and **there is no consent to
wait for now**, so the app's builds collect from the first launch and the table says so. The
ad-ID half is unaffected, and §7.4's build-time test holds both halves.

| What it gates | Platform | The exact name | Value the plan requires |
|---|---|---|---|
| Analytics collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`firebase_analytics_collection_enabled`** | `true` — **D255's always-on answer**: the build that carries the SDK collects |
| Analytics collection, native | iOS, `Info.plist` | **`FIREBASE_ANALYTICS_COLLECTION_ENABLED`** | `true`, the same answer |
| Crashlytics collection, native | Android — **not a manifest requirement, corrected 2026-10-30**: the Crashlytics library's own library manifest hardcodes `firebase_crashlytics_collection_enabled` to `false`, and RNFirebase's init provider turns collection on from `firebase.json` (the row below) | **`crashlytics_auto_collection_enabled`**, read from `firebase.json`; the manifest name is the library's and is not written by this app | `true`, the same answer — carried by `firebase.json`, not by an `AndroidManifest.xml` `<meta-data>` entry |
| Crashlytics collection, native | iOS, `Info.plist` | **`FirebaseCrashlyticsCollectionEnabled`** | `true`, the same answer |
| Advertising-id collection, native | Android, `AndroidManifest.xml` `<meta-data>` | **`google_analytics_adid_collection_enabled`** | `false` |
| Ad-personalisation signals, native | Android, `AndroidManifest.xml` `<meta-data>` | **`google_analytics_default_allow_ad_personalization_signals`** | `false` |
| Ad-ID support, linked at all | iOS, the Analytics pod `@react-native-firebase/analytics` makes CocoaPods link | **`ios.withoutAdIdSupport`** — the Analytics config plugin's option, which writes `$RNFirebaseAnalyticsWithoutAdIdSupport = true` into the Podfile so the pod links `FirebaseAnalyticsCore` and **no `AdSupport` framework**. **RESOLVED 2026-10-30** against the pinned `27.1.0` source (`plugin/src/pluginConfig.ts`, `plugin/src/ios/podfile.ts`, `RNFBAnalytics.podspec`) | ad-ID support **not linked** |
| Advertising-id permission, declared at all | Android, `AndroidManifest.xml` `<uses-permission>` | **`com.google.android.gms.permission.AD_ID`** — **removed** from the merged manifest at prebuild (`tools:node="remove"`) | absent (not declared) |
| Analytics auto-collection, the React Native layer | `firebase.json` at the app root, under `react-native` | **`analytics_auto_collection_enabled`** | `true`, the same answer |
| Crashlytics auto-collection, the React Native layer | `firebase.json` at the app root, under `react-native` | **`crashlytics_auto_collection_enabled`** | `true`, the same answer |
| Analytics collection, runtime | JS, `@react-native-firebase/analytics` | **`setAnalyticsCollectionEnabled(…)`** | **never called** — and **refused by §7.4's scan**. A call that can set collection state *is* the switch D255 says the app does not have, whether it is called with `true` or with `false` |
| Crashlytics collection, runtime | JS, `@react-native-firebase/crashlytics` | **`setCrashlyticsCollectionEnabled(…)`** | **never called**, refused the same way, for the same reason |

**These names are read from memory of the SDKs' own documentation and not from a page read
today; P7.1's first commit confirms each against the SDK's documentation and the generated
native project, and corrects this table in the same commit if any is wrong** (§14). What the
plan commits to is the *shape*, and D255's answer is what fixes it: the app's builds collect,
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
   through Expo's own `android.blockedPermissions` — **RESOLVED 2026-10-30**: the pinned
   `@react-native-firebase` config plugins have **no** option for it, so prebuild is the site
   of the removal, per §14).
3. **iOS, no ad-ID support linked at all.** The app links the Firebase Analytics variant that
   **does not support the advertising identifier**, so the `AdSupport` framework is not in the
   binary and there is no id for a later misconfiguration to read. The pod or plugin option
   that selects it is **`ios.withoutAdIdSupport`**, the `@react-native-firebase/analytics`
   config plugin's option — **RESOLVED 2026-10-30 against the pinned `27.1.0` source**
   (`plugin/src/pluginConfig.ts:6`, `plugin/src/ios/podfile.ts:11`, `RNFBAnalytics.podspec:59-64`)
   rather than from memory (§14), and P7.1a applies it. **How it is verified**, on a machine
   that prebuilds: read the generated `ios/Podfile.lock` for `FirebaseAnalytics` and for the
   **absence** of `AdSupport` (and read the Android manifest for the absent `AD_ID` permission,
   point 2). No run of this repository prebuilds — the generated files are not committed — so
   that read is the owner's, at the first store build.

**The build-time test (§7.4's third test, P7.1).** A Node test in the app fails the build if
**either** the generated native config **or** the plugin options enable ad-ID collection: the
two Android ad switches must be `false` in `mobile/firebase.json` (the library's build step
turns them into manifest placeholders), the merged manifest must not declare the `AD_ID`
permission, and the iOS side must not link `AdSupport` — **corrected 2026-10-30**: it does not
assert a manifest value for Crashlytics collection, whose switch the library's own manifest
hardcodes off and RNFirebase's init provider turns on from `firebase.json`. It is written the
way the app's other
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

1. **`.gitignore`, the app's own.** Both paths are ignored at **`mobile/.gitignore`** — the
   app's own ignore file, which P4.4 left in place and which P7.1 **appends to** so the rule
   travels with the app. **The repository root's `.gitignore` is not touched, and does not
   need to be**: both paths are under `mobile/`, where the app's own file governs them, so a
   second entry at the root would be a rule written twice and corrected once. **Neither path
   is ignored anywhere today** — the two entries are P7.1's to add, and this is a plan, not a
   statement about the tree — and **a path under `mobile/` that names `google-services` or
   `GoogleService-Info` is never tracked once P7.1 lands.**
2. **A scan with a positive control.** P7.1 extends `mobile/src/app-config.test.js` (whose
   "no credential, no installation" walk already exists, with a planted `AIza…` value as its
   control) with a **tracked-files** rule: for each of the two paths, the test fails if the
   file is tracked by git, and fails if it is **not** ignored. A planted entry in a temp
   index proves the rule can fail.
3. **A build-time guard that fails loudly.** `app.config.js` resolves the two file paths
   **from the environment** — `process.env.GOOGLE_SERVICES_JSON` and
   `process.env.GOOGLE_SERVICE_INFO_PLIST` — and a build whose **resolved config carries no
   readable file** for a platform it is building is a **stop**, not a build. **Amended
   2026-10-30 (P7.1b): the earlier sentence here, "a profile that carries neither", was the
   check as planned and is retired with the `eas.json` wiring** — the guard reads the resolved
   config, not a profile's `env` key, so a path that arrives as a static `googleServicesFile`
   or under any variable name is a file like any other, and `EAS_BUILD_PLATFORM` narrows the
   check to the platform EAS named. The guard's shape is check 89's own (`jq` reads the submit
   track *before* anything is built; a profile naming a public track is a stop the owner takes
   there), and its text says what is missing by **name** and never prints a value.
4. **EAS file secrets deliver them** — **renamed 2026-10-30 (P7.1b): what delivers them is
   EAS environment variables with file visibility**; the commands below are one way to create
   such a file-valued variable, and the paragraph after them is the resolution. The owner
   creates them once, at their own terminal,
   with their own Expo account — **no agent runs this and no agent reads either file**:

   ```
   eas secret:create --scope project --name GOOGLE_SERVICES_JSON \
       --type file --value ./google-services.json
   eas secret:create --scope project --name GOOGLE_SERVICE_INFO_PLIST \
       --type file --value ./GoogleService-Info.plist
   ```

   **RESOLVED 2026-10-30 (P7.1b): the mechanism is EAS environment variables with file
   visibility**, set once in the **EAS environments** each build profile names through
   `eas.json`'s `environment` key — on the worker each variable's value is the **path** EAS
   wrote the file to, which is what the plugin's `googleServicesFile` wants and what the
   owner's builds already do. **The `eas.json` `env` entries the sentence above names are
   retired**: an `env` block there would *override* the EAS environment and replace that path
   with a literal string, breaking the owner's builds, so `mobile/eas.json` carries **no**
   `env`, `mobile/src/eas-config.test.js` refuses one per profile, and `eas.json` is not in
   P7.1's diff at all (§14 item 2, P7.1's Files line).

**Why the file-secret route rather than a committed template.** A committed template either
carries the project's real apiKey (refused: `SECRET_SHAPES`'s `google-api-key` entry and the planted `apiKey` control in `mobile/src/app-config.test.js`) or
carries placeholders and produces a build whose crash reporting is silently dead (refused:
D260). The EAS file secret is the one route where the file exists **only** on the build
machine, is scoped to the project's own build profiles, and is never in a checkout, a
fixture, a log or a pull request — the same property `docs/pending-workflows/README.md`
describes for the jobs that must not hold material on every pull request.

### 4.4 What is built in, what arrives in the pairing payload, and what is in secure storage (D259, accepted)

**D259 is the row that asked where the app's Firebase configuration comes from, and the owner
accepted the recommendation on 2026-10-19.** The answer splits the configuration in two, and
the split is the whole of the row: **what is the same for every installation is compiled in;
what is different for every installation cannot be, and travels the pairing's own encrypted
path into the phone's secure storage.**

| | What it is | How it gets there | Where it is written down |
|---|---|---|---|
| **Built into the build** | **The telemetry project's two native config files** — `mobile/google-services.json` and `mobile/GoogleService-Info.plist`, naming **`fugaro-prod`** (D254). The plugin applies them at prebuild, so the app's **default** Firebase app is the Fugaro telemetry project for every installation, and no `apiKey` is ever written in JavaScript (§4.1). | **EAS environment variables with file visibility** — the mechanism P7.1b chose on 2026-10-30 — set once in the **EAS environments** `eas.json`'s profiles name through its `environment` key and applied on the build machine; **not** an `env` block in `eas.json`, which would override the EAS environment (§4.3 step 4). §4.3's four-part rule: ignored at `mobile/.gitignore`, the two paths untracked and ignored, refused by `app.config.js`'s D260 guard when a built platform's resolved file is missing, and never read by an agent. | **Nowhere in this repository.** Never committed, never a fixture, never a log line, never a pull request. |
| **Inside the pairing payload** | **The installation's own Firebase connection** — its **Realtime Database URL** and its **web API key**. They differ per installation and **are not known at build time**, so no build can carry them. | **The encrypted pairing payload** the user's own installation hands over when an account is added by hand. The CLI already records both (`init_firebase.go`), and **G18** already reads them for the QR. They travel under **D113b**'s pairing key — the same key D111's item (2) is. | **Nowhere in this repository**, and nowhere on the wire unencrypted. |
| **In the phone's secure storage** | **Two items per account (D232 stands)** — **(1)** the Firebase Auth **ID token** and **(2)** the **per-pairing MAC key** (D113b's option (a)) — **plus the pairing-delivered configuration**: the installation's **Realtime Database URL** and **web API key**, which are ordinary configuration and not secret. | **Platform secure storage** (Keychain / Keystore) for the two items. **Where the configuration is stored is task P6.8's choice** ([2026-10-22-mobile-pairing-cli.md](2026-10-22-mobile-pairing-cli.md)). Never logged, never a fixture — `SettingsStore`'s `SECRET_SHAPES` and `FORBIDDEN_KEY_SEGMENTS` are unchanged. | **D111's row** in [mobile/reconciliation.md](../mobile/reconciliation.md) and **Global Constraint 4** in [mobile/implementation-plan.md](../mobile/implementation-plan.md), each carrying a **dated amendment of 2026-10-22** that records the owner's acceptance of 2026-10-19 and keeps the original *two items* text visible, and a second of **2026-10-29** that records the owner's choice of D232. |

**Why the connection values cannot be built in.** A build carries **one**
`google-services.json` and **one** `GoogleService-Info.plist`, and they name **one** project.
The app serves several installations (**D114**, *"several, non-overlapping"*), and each
installation's database URL and web API key are its own; a build that named one installation's
would serve that installation and no other — which is the alternative D259 refused, at the
cost of a build and a store submission per installation.

**What this changes, and what it does not.** It **leaves D111's record at *two items per
account***, because the owner ruled on 2026-10-29 that D232 stands over D259's four-item list; D111's own cell says why the list is exhaustive rather than illustrative
(*"D54b's precedent is to list every item held, not only the obvious one"*). It **narrows what
the settings store's refusal of API-key-shaped values protects**: `SECRET_SHAPES`'s
`google-api-key` entry and `judge`'s `SECRET_VALUE` branch are **not relaxed**, a settings
entry may still never carry an `AIza…` value, and the store's own fence is exactly as it was;
what is open is this plan's **stronger** sentence, that the app stores no such value
*anywhere*, because where the installation's own key is stored is task P6.8's choice. **No
value is written into this repository by that**: this section names the two values' *kinds*
and never a value, and nothing here is a secret-shaped literal. **And no task is approved by
it**: D259's acceptance is a ruling, not a green light — the gate was passed separately, on
2026-10-22 — and the app **still carries no telemetry module**, because P7.2–P7.5 are unbuilt
(**P7.1's wiring is built**, as of 2026-10-30).

**The second fence, and the owner's ruling leaves it shut.** `SECRET_SHAPES` is not
the only list D111's item count is written into. **`SECRET_ITEMS`** — `mobile/src/accounts/secret.js`,
`Object.freeze(['idToken', 'macKey'])`, whose own comment reads *"The two items of D111, and the
whole list"* — is an **exhaustive allow-list**, and `isSecretItem` refuses any other name:
`secure-store.js` refuses the write, the read and the delete of an item not on it
(`SECURE_STORE_CODES.UNKNOWN_ITEM`), `store.js` refuses the same, `secret.test.js` asserts
*"isSecretItem knows the list and nothing else"*, and **`website/test/privacy.mjs` fails unless
the list is exactly `idToken,macKey`**, because the live privacy page describes the phone as
holding D111's two. **The owner ruled on 2026-10-29 that D232 stands, so `SECRET_ITEMS` is not
widened**, and D259's four-item list is not built.

**Where the pairing-delivered configuration is stored is task P6.8's choice, and no Phase 7 task writes a secure-storage item.** Every task of
P7.1–P7.5 builds a build, a sentence, a redaction rule or a check, so none touches
`SECRET_ITEMS`. The pairing plan's task P6.8 ([2026-10-22-mobile-pairing-cli.md](2026-10-22-mobile-pairing-cli.md))
is where the phone first stores what the pairing delivers, and it chooses where the two values
go; a choice that needed a third `SECRET_ITEMS` name would be a new owner question, because D232
is ruled. **The code allows only the two `SECRET_ITEMS` (`idToken`, `macKey`), and `website/test/privacy.mjs` pins exactly that.**

---

## 5. The Fugaro-operated project (D254), as owner steps

**The project is `fugaro-prod` — the owner's answer of 2026-10-19, and the project that already
hosts the website.** The plan's suggested id, `fugaro-telemetry`, is **not** what the owner
chose; the recommendation stays visible in D254's row and this section is rewritten for the
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
- **The owner accepted this arrangement when answering D254**, with the sharing stated: one
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
   setting — the plan requires the shortest retention the console offers, and **this step is
   where the owner notes which it is**: **no decision records it today**, D254's row answers
   which project and says nothing about retention, and §14's item 8 says so in as many words.
   *Owner step.*
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

## 6. Consent and opt-out (D255): there is none

**The owner's answer, in one sentence:** *collection is on in the builds that carry the SDKs —
there is no switch, no first-launch sheet and nothing in the app to turn off.*

**What that answer supersedes, and what stays visible.** This plan **recommended opt-in** —
*nothing is collected until the user says yes on the first launch, one switch for both SDKs* —
and it argued for it: the user is in the EU, the SDKs write an app-instance id to the device
the moment they start, and reading or writing anything on a device that is not strictly
necessary is the thing the site's own consent banner exists for. **The owner answered always-on
on 2026-10-19**, and the recommendation stays in D255's row as the recommendation it was. What
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
statement is what the app shows and **check 120's link half cannot pass**; §12 says so inside
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
that device are sent from the first launch. The alternative — opt-in — is D255's recommendation
and it is not what the owner chose; its cost (nothing is reported about the population that
never answers the sheet) is what the owner weighed against this one. **The plan records the
trade and does not re-argue it.**

**The device holds no consent value, so consent adds nothing to D111.** The key this section
used to reserve — `telemetry.consent`, in the settings store and deliberately not a `secret.`
key — **is not reserved any more**: nothing is stored and nothing is read back. **D111's list
is not widened by anything here**: D259 was accepted on 2026-10-19 with a four-item list, and the owner ruled on 2026-10-29 that D232 stands, so D111 stays *two items per account* (§4.4) — the installation's Realtime Database URL and its web API key arrive in the **pairing payload** as ordinary configuration, not from consent.
Consent itself still stores nothing.

## 7. What is and is NOT sent (D256)

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
that recorded which way the sheet was answered. **D255's always-on answer removes the sheet, so
the event goes with it** — there is nothing to record — and the allow-list is one name shorter
rather than carrying a parameter nothing can produce.

| Event | Parameters (closed sets) | Why it exists |
|---|---|---|
| `app_launch` | `first: bool` | Does the app start on a real device at all, and does it start again. |
| `account_added` | `kind: 'manual' \| 'pairing'` | Whether an account is added at all, and by which path. |
| `account_switched` | — | Whether more than one account is ever used. |
| `screen_shown` | `screen: 'overview' \| 'jobs' \| 'mailbox' \| 'voice' \| 'text'` | Which screens are reached; the enum is the whole screen set and a screen with no name in it cannot be reported. **`'settings'` is deliberately not in it**: the app has no settings screen and no phase builds one (§6), so a value for it would name a screen that cannot be reached. |
| `command_refused` | `reason: 'offline' \| 'unconfirmed' \| 'not_understood' \| 'refused'` | The only signal about voice that may leave, because it names no command. |
| `pairing_finished` | `outcome: 'ok' \| 'code_mismatch' \| 'expired' \| 'denied'` | Whether pairing works, and how it fails. |
| `crash_reported` | — | A marker event, so Crashlytics' own count and Analytics' count can be compared. |

**Nothing else.** No screen *title*, no account *name*, no agent *name*, no count of anything
(screens, messages, prompts, runs), no duration, no currency figure and no error string.

**The automatic events are not ours, and the plan says which ones it accepts.** Firebase
Analytics collects a set of events by itself (`first_open`, `session_start`, `app_update`, and
the app-instance id) and the plan **accepts them**, because they carry no Fugaro data.
**D255's always-on answer makes that acceptance load-bearing rather than incidental**: the
auto-collection switch is `true` (§4.2), so these events are collected from the first launch
along with ours, and the page's paragraph names them by category (*which screens were opened*,
*app-instance identifier*) rather than leaving the reader to infer them. The one switch that
still trims the set is the **ad-ID** one, `google_analytics_adid_collection_enabled = false` —
which removes the advertising identifier and nothing else. **The rejected alternative is listed
in D256**: turning the allow-list off and relying on the ban list.

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
  stack) and that is the set D257's labels are answered from.

### 7.4 The test that holds it

P7.3 adds **two** tests in `mobile/src/telemetry/`, and P7.1 adds a **third** — the ad-ID test,
whose subject is the build rather than the app's code. **Amended 2026-10-30 (P7.1b): the first
sentence here said P7.1 "extends the app's own `mobile/src/app-config.test.js`"; the test is the
new `mobile/src/ad-id.test.js`** (item 3 below is the record). All three carry positive controls,
all three in the shape the tree already uses (`mobile/src/mailbox/write-path.test.js`,
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
     — **D255's answer is always-on and there is no switch**, so a call that sets collection
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
3. **The ad-ID test (`mobile/src/ad-id.test.js`, new at P7.1b).** The website promises
   the app carries no advertising identifier (§4.2, §8.3). **Written 2026-10-30, and the two
   corrections below are this walk's own record of itself**: it is a **new** test in the app,
   not a third walk inside `mobile/src/app-config.test.js` (that file owns `app.json`'s
   credential fence and its planted `AIza…` control, and this walk's subject is the build), and
   it reads the **resolved config and `mobile/firebase.json`** rather than the generated native
   project:
   - the two Android ad switches `google_analytics_adid_collection_enabled` and
     `analytics_default_allow_ad_personalization_signals` — **the unprefixed personalisation name
     is deliberate**: it is the key the library's own build step reads, while the prefixed
     `google_analytics_default_allow_ad_personalization_signals` is the *AndroidManifest*
     meta-data name and, written in `firebase.json`, leaves the switch unreachable and ad
     personalisation on (the asymmetry PR #1326 found and fixed) — must be **`false`** in
     `mobile/firebase.json` (the library reads them at build time and writes them as manifest
     placeholders), and `android/app/src/main/AndroidManifest.xml` must **not** declare
     `com.google.android.gms.permission.AD_ID` — **corrected 2026-10-30**: the walk does not
     assert a manifest value for Crashlytics, whose collection the library's own manifest
     hardcodes off and RNFirebase's init provider turns on from `firebase.json` (§4.2);
   - the Analytics plugin entry must carry the ad-ID-free iOS option, `ios.withoutAdIdSupport`
     (§4.2's **VERIFY** item, resolved 2026-10-30) — **and the walk does not open the generated
     `ios/Podfile.lock`**: nothing in this repository prebuilds, so that read is the owner's at
     the first store build (§4.2, check 120);
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

## 8. The store labels and the privacy page (D257)

### 8.1 Play — Data safety

The owner has already ticked six categories. Answering them from §7's allow-list, one by one:

| Play category (ticked) | What in this plan justifies it | Note |
|---|---|---|
| **Crash logs** | Crashlytics, by definition | Required; sent from the app's first launch (D255's always-on answer — there is no consent to wait for). |
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
what holds it**, and check **122** is where the owner confirms the submitted form matches what
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
| **Usage Data** → *Product Interaction* | Not Linked | the **seven** events of §7.2 — Analytics ships with the second build (**D258**, answered), and the list lost `consent_set` when D255 was answered always-on. If Analytics does not ship, this row is **not ticked**. |

**The label and Play's form are answered from one document** — this section — so the two
stores cannot be answered differently, which is the failure a reader would never catch.

### 8.3 The website — what the live page says, and what holds it

**The page is ahead of the SDKs, by design, and it says so itself.** This section used to read
*"what changes, in the same release that ships the SDKs"* — D257's recommendation, made when
the page said nothing about the app. **Two pull requests have since put the app's paragraph on
the page, and both are merged into this change's base** (`main` at `4d619273`):

- **PR #689** split the statement into **two parts** — *The website* and *The software* — so
  that the one URL the two store listings point at covers the software as well as the site, and
  added **The phone app** under the software part.
- **PR #705** wrote the owner's ruling into it: *"The app versions that carry these reports send
  them. There is no switch inside the app to turn them off: uninstalling the app stops them, and
  not installing it avoids them."* — **D255's answer, in the page's own words, published before
  the build exists.**

**So the sequencing sentence is replaced by the truth, and the truth is in the page's last
paragraph of that section:** *"Earlier builds of the app send nothing at all."* The page is
honest about a build a reader may be holding, and it is **ahead** rather than behind — the
failure mode D257's recommendation was written to avoid was the opposite one (a store build
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
reports. **This change does not run it** — it is the website work's gate and runs in `website/`'s
own `npm test`, which no CI job here runs (UNVERIFIED 6).

**What must therefore change, in this plan's terms — and what must not.**

1. **The page's sentences must stay true of the build that ships.** §7's ban list and allow-list
   are the app's side of that bargain, and P7.4's job is to hold the two together at the moment
   the SDKs land: if the app sends something the page does not name, or the page names something
   the app does not send, **the page changes in that commit or the build does not ship**. It is
   the same rule D257 answered, with the direction reversed.
2. **The paragraph's advertising-identifier sentence is the one it must keep** — *the app carries
   no advertising identifier* — because that claim is held by the **build** rather than by the
   app's code: §4.2's two ad switches `false`, the removed `AD_ID` permission, the unlinked
   `AdSupport` and §7.4's ad-ID test are what make it true, and it survives D255's answer
   unchanged. **The two stores' answers carry the same fact** (§8.1's no Advertising ID type,
   §8.2's unticked Advertising Data).
3. **The page's "no switch" sentence is a promise this plan can keep now and could not keep
   under its own recommendation.** §6 makes it structural — the native switches are on, so
   there is no state to toggle, and §7.4's scan refuses the runtime calls that would be one. **A
   page saying there is no switch and an app carrying one is the contradiction §6's fence exists
   to prevent.**
4. **The page's word ceiling is D188's** (a page's lead at most 40 words, a page at most 250,
   with `/docs/`, `/security/` and `/changelog/` excepted). **UNVERIFIED here:** #689
   re-derived the privacy statement's own ceiling — it is the one page that is not the site's
   pitch — so P7.4 reads `website/README.md` and `website/test/budget.mjs` when it starts rather
   than trusting this paragraph. The trade is still the same: **a sentence added here
   costs a sentence traded out**, in the same commit, by whoever owns the page then.
5. **`website/data/site.yaml`'s `analytics` block is not touched** unless the paragraph needs a
   new fact from it: the block describes the *site's* GA4, and the app's telemetry is not a
   site fact.

**The website change is a task in this plan (P7.4), owned by whoever owns `website/` then**,
because the page's gates (`website/test/*.mjs`, the origin gate, the byte budgets) are the
website work's and this plan does not fork them.

---

## 9. Interaction with the voiceless MVP (D258)

The first MVP is **voice-less internal testing on both stores**. **The owner answered D258 on
2026-10-19: the second build carries the SDKs and the first internal build ships neither.** The
reason is structural rather than cautious:

- The first internal build's purpose is the **store path** — a signed EAS build reaching an
  internal track (P4.4, check 89) — and, per the brief, the *voice-less* app. A native config
  plugin changes the native project that path is proving; a telemetry failure and a store-path
  failure would land in the same release, and the store path is the one that cannot be redone
  cheaply.
- The first build carries **no native config plugin at all**, which is the cleanest form of the
  point above: with D255 answered always-on, there is no sheet to design and nothing to gate,
  so the first build's only new firsts are the store path and the app's own wiring.
- **D257 makes it strictly better to wait:** the labels are answered from what the SDKs actually
  collect, and the first build collects nothing to read. The website's paragraph is the one
  piece that did **not** wait — it is already live and already says which version carries the
  reports (§8.3) — and that is D257's own answer working as intended rather than against it.

**The alternative is D258's own second option** — *Crashlytics alone in the first build,
Analytics in the second* — and the owner did not take it. Its cost is stated there: the labels
are answered twice, and the first build would carry a native config plugin, which is the thing
this row exists to keep out of the store path's release.

**What D255's answer changed here.** The paragraph that stood in this section said the consent
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
runs with no device, no live cloud and no network. **The phase is approved**: the owner passed
[mobile/implementation-plan.md](../mobile/implementation-plan.md)'s
approval gate on **2026-10-22**, on the condition that this plan is merged first, so **P7.1 may
start the day this plan lands**. **Every row the tasks wait on is answered — D253 to D260, all
eight, on 2026-10-19, D259's acceptance last of all** — so **no "Depends on" line below names an
open question**; each names a ruling. **The answers approved no task on their own**: the gate is
what did, and it is dated 2026-10-22.*

### Task P7.1: The config plugin, the native files, and the development build

**Files.** `mobile/app.config.js` (**modified** — the plugin entry, the two `googleServicesFile`
paths read from the environment, the build-time guard), `mobile/eas.json` (**not modified** —
**retired 2026-10-30**: the planned two `env` entries in each of its three build profiles,
`development`, `preview` and `internal`, are the wrong mechanism; the files are delivered by
EAS environment variables with file visibility, and an `env` block would override them — §4.3
step 4), `mobile/firebase.json` (new — the app-root
`react-native` block of §4.2), `mobile/.gitignore`, `mobile/package.json` (the three
`@react-native-firebase` dependencies), `mobile/README.md` (the Expo Go sentence D260 asks
for), `mobile/src/app-config.test.js` (the tracked-file and ignore rules of §4.3, and the
extension of the existing credential walk to `app.config.js`), **`mobile/native-config-guard.js`
(new — D260's guard) and its tests `mobile/src/native-config-guard.test.js` and
`mobile/src/ad-id.test.js`; **the ad-ID test of §7.4 is the new `mobile/src/ad-id.test.js`**, not
an extension of `app-config.test.js`)**. The ad-ID walk reads the resolved config, the plugin
options and `mobile/firebase.json` — **corrected 2026-10-30: it does not read
`ios/Podfile.lock`**, because nothing in this repository prebuilds; only the generated
`AndroidManifest.xml` is read, and only when the machine has one (§7.4, §4.2, check 120).
**Depends on.** **D259 — accepted 2026-10-19**, the row that decides which project the native
files name, which app is the default, and where an installation's own connection comes from
(§4.4). **It was the last row P7.1 waited on, and it is answered**, so this line names a ruling
rather than a question — as does every other dependency: D260 answered yes, and **D254 answered
`fugaro-prod`**, the project that already exists, so the "project before its file" half is done
the day the owner registers the two apps in it. **PR #675 is not a dependency**: it is merged, and P7.1 modifies `app.config.js` where it stands.
**Verification.** `cd mobile && npm test` — the extended `app-config.test.js`, the new
`native-config-guard.test.js` and the ad-ID walk: both native
paths are untracked and ignored; `app.config.js` contains no `AIza…`-shaped value (planted
control); **a build whose resolved native config file is missing is refused (D260) — corrected
2026-10-30: the guard checks the resolved *file*, not a profile's `env` key, so the planned
"no `GOOGLE_SERVICES_JSON` in its `env`" sentence is retired with the `eas.json` wiring above**;
the existing "no credential, no installation" walk still passes; and **the ad-ID
walk passes** — the two Android ad switches are `false`, the manifest declares no `AD_ID`
permission, and the Analytics plugin entry carries the ad-ID-free iOS option, each with a
planted `true` control proved to fail the walk. §4.2's **VERIFY** items (the exact iOS pod/plugin
option, and whether the Android permission removal is the plugin's own option) were **resolved
at P7.1a (2026-10-30)** by reading the pinned `@react-native-firebase` source — `ios.withoutAdIdSupport`,
and Expo's own `android.blockedPermissions` because no plugin option exists — and §4.2's table
is corrected in the same change.
**Reviewer tier.** **SECURITY-SENSITIVE — independent opus review.** It is the change that
decides which project the app's default Firebase app is (D259, accepted 2026-10-19), and it is the change that
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
and the `telemetry.consent` settings key are all gone from this task**: D255's answer is
always-on, so there is no choice machine, no sheet and no stored value (§6).
**Depends on.** P7.1; **D255 (answered: always-on)**; and **D124's host list, which the owner
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
**Depends on.** P7.1, D256.
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
**Depends on.** P7.3 (the list the labels are answered from), **D257 (answered: labels from
what the SDKs collect, the page's words with the release that ships the SDKs)**, and the live
privacy statement — **PRs #689 and #705, both merged and in this base**: the task
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

### Task P7.5: Checks 120–122, written into the register

**Files.** `docs/gcp-live-checklist.md`.
**Depends on.** P7.1, P7.2, P7.3 (a check is never run before its text is in the register).
**Verification.** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, and the pin
`internal/cli/docs_mobile_telemetry_test.go` (this change) held to the new section's summary
rows.
**Reviewer tier.** standard for the writing; **owner** for the running. **No agent runs any
step of 120, 121 or 122.**

### 10.1 Order and levels

```
D253, D254, D259, D260 (answered) ─┐   D255, D256, D257 (answered)
                                    ┴─► P7.1 ──┬─► P7.2 ──┐
P0.3 (shared/url, merged) ──────────────┘          ├─► P7.4
                                       P7.3 ───────┴─► P7.5
#675 (merged: app.config.js, eas.json) ──► P7.1
#689, #705 (merged: the privacy statement,
           the app's paragraph and the always-on sentence) ──► P7.4
```

**All eight rows are answered — D259's acceptance on 2026-10-19 was the last — so every edge
above is a ruling rather than a question. The gate is passed too**, on **2026-10-22** and on the
condition that this plan is merged ([mobile/implementation-plan.md](../mobile/implementation-plan.md),
Phase 7's heading and step **7b**).

| Level | Tasks | Reviewer | Files touched |
|---|---|---|---|
| **0** | **P7.1** the plugin, the native files, the development build | **SECURITY-SENSITIVE** | `mobile/app.config.js`, `mobile/native-config-guard.js` and `mobile/src/native-config-guard.test.js`, `mobile/firebase.json`, `mobile/.gitignore`, `mobile/package.json`, `mobile/README.md`, `mobile/src/app-config.test.js`, `mobile/src/ad-id.test.js` — **and not `mobile/eas.json`, amended 2026-10-30: the files reach a build as EAS environment variables, so no `env` entry is written there** |
| **1** | **P7.2** the always-on statement and the link · **P7.3** the allow-list and the scan | **both SECURITY-SENSITIVE** | `mobile/src/telemetry/notice.js`, `mobile/src/app/root.js` and `mobile/src/app/root.test.js` · `mobile/src/telemetry/` (the event module and its two tests), one entry in `mobile/src/mailbox/write-path.test.js` |
| **2** | **P7.4** the labels and the website's paragraph | standard (website) / **owner** (the forms) | `website/src/pages/privacy.astro`, the website's gates |
| **3** | **P7.5** checks 120–122 written | standard | `docs/gcp-live-checklist.md` |
| **owner** | the checks **run** — P7.5 writes 120–122 and the owner runs them. **This is not a task and has no number**: running a check merges nothing, so the phase is five tasks and not six | owner | none |

**P7.2 and P7.3 are parallel** — different directories, and the only shared file between them
is none. **P7.4 waits on P7.3** because the labels are answered from the allow-list, not from
this plan's prose. **P7.5 waits on all three code tasks.** **No level contains two writers of
one file:** `mobile/src/telemetry/` is written by P7.2 (`notice.js`, `notice.test.js`, and the
one child P7.2 adds to `mobile/src/app/root.js` and its test) and P7.3 (everything else —
`consent.js` went with D255's answer, and §6 lists it among what the answer removes), and the
plan splits them into **two files with two owners** rather than one directory with two — the
same rule the mobile plan's shared-files table states.

**The critical path is P7.1 → P7.3 → P7.4 → P7.5**, four deep, and it is short on purpose:
this phase is additive and nothing in the app's existing tasks waits on it.

### 10.2 What each task can and cannot verify

| The task | Can be verified without a device | Cannot |
|---|---|---|
| P7.1 | the tracked-file rules, the ignore rules, the `AIza…` walk, the guard's pure part | that the plugin applies, that a development build is produced, that the SDKs initialise — **`npx expo prebuild` and `eas build` need a toolchain this repository's CI does not have** |
| P7.2 | the statement's facts, the link's validator path, and the absence of a switch (the scan) | that the statement renders in the app, and that the native SDK is collecting — **only check 120 can show that**, on a device |
| P7.3 | the scan, the never-send test, the existing fences | that the *native* SDK collects what the allow-list says and nothing else — **only checks 120 and 121 can show that**, on a device |
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
- **No Terraform, no `fugaro` command, no Cloud Run job, no workflow.** The only live actions
  in the whole phase are the owner **registering the two Firebase apps in the project that
  already exists** — `fugaro-prod`, D254's answer, the project the website is deployed into,
  so **no project is created for telemetry** (§5 step 1) — and **uploading two file secrets**
  (§4.3). **D254's answer is what makes the first a registration and not a creation**: the
  project is the one the website already uses, and §5's opening paragraph states the
  arrangement the owner accepted with that answer.
- **No credential in the app beyond D111's list — two items per account (idToken, macKey), and no more**, plus the pairing-delivered configuration (the installation's database URL and web API key, ordinary configuration, not secret). The two items stay in platform secure storage, and neither them nor any value of the configuration is ever in a fixture, a log line or this repository (§4.4); the consent boolean is deliberately **not** one of them
  (§6).
- **Nothing is widened.** `SECRET_ITEMS` — `mobile/src/accounts/secret.js`'s exhaustive allow-list of D111's two items — stays as it is (§4.4): the owner ruled on 2026-10-29 that D232 stands, and `website/test/privacy.mjs`'s assertion that the list is exactly `idToken,macKey` stands with it. Where the pairing-delivered configuration is stored is task P6.8's choice; no task of P7.1–P7.5 writes a secure-storage item.
- **No `belong` anywhere.** No fixture, no project, no read.
- **No change to any existing mobile task's ordering.** Phase 3, Phase 6 and Phase 7 are
  independent, and the voiceless MVP is unaffected (D258).

---

## 12. Checks this plan takes (120, 121, 122)

**Three numbers out of the free block after 119 — the first free number after every claim
(§2.2) — owner-run, none of them a CI job, and no
agent runs any step of any of them.** They are **written by P7.5 and run by the owner**, and
**a check is never run before its text is in the register**.

| # | The check | Task | Why it cannot be a test |
|---|---|---|---|
| **120** | **The reports arrive, they arrive from the first launch, and they arrive at `fugaro-prod` and no other project.** Install a second-build app on a device, launch it, **touch nothing** — there is no sheet, no switch and no prompt, which is D255's answer — and watch the Firebase console's debug view: the app's own `app_launch` and `screen_shown` events appear **for `fugaro-prod` and for no other project**, and they appear **at the first launch** rather than at any later moment. Then the two negative halves, which are what this check is for: **the app's shell says the reports are on and carries no control that stops them — the link beside that sentence is the `/privacy/` URL once the owner's D124 host list names `fugaro.dev` (it renders as text until then, §6), so this half of the check is run after that owner step and not before it** (D255, §6, and §7.4's scan is the static half of the same fact); and **the submitted build declares no `AD_ID` permission** — read from the built artifact's merged manifest, not from the source — so the identifier the page promises is absent is absent in the build a store holds. | P7.5 writes it; **the owner runs it** | Needs a real device, a real store build, a real network and the Firebase console. No hermetic test can see a native SDK's own socket, and no Go test can read a store's build. |
| **121** | **A real crash reaches `fugaro-prod`, with nothing that names anything in it.** Crash the app deliberately from the debug menu on a freshly installed device — **nothing has been consented to, because there is nothing to consent to**: the report appears in Crashlytics under `fugaro-prod` on the app's own first run, which is the always-on half of D255 proved end to end. **Its breadcrumbs, its custom keys, its logs and its device fields contain no message text, no mailbox key, no account id, no `authUid`, no project id, no token and no MAC**; `setUserId` is absent from the report; and **no advertising identifier is in it**. Then the control, and it is a different control from the one this check used to carry: **the same crash on the FIRST build — the one that carries neither SDK (D258) — produces no report at all**, which is what proves the reporting is the SDKs' and not something else in the app. | P7.5 writes it; **the owner runs it** | Needs a real device, a real crash, two builds and the Crashlytics console — the redaction is a property of a report that only exists after a crash. |
| **122** | **The two store forms, and the live privacy statement, match what the app sends.** The Play **Data safety** form and the App Store **privacy label** are the answers of §8.1 and §8.2, category by category, and **each ticked category is justified by traffic observed in 120 and 121** — with the two over-declared categories (`User IDs`, `Other in-app messages`) recorded **as** over-declared, in the owner's own words. And the page: `https://fugaro.dev/privacy/` — **the URL both store listings point at as the privacy policy** — is read against the build sentence by sentence, because **it is already live and already ahead of the build** (§8.3): every provider and every category it names must be one the app actually sends, and **every category the app sends must be one it names**. A page that names something the app does not send, or an app that sends something the page does not name, is the failure this check exists to catch — and since the page cannot be changed by the build, **the build does not ship until the two agree**. | P7.5 writes it; **the owner runs it** | Needs the two consoles, the live site and the owner's own accounts. |

**120, 121 and 122 are owner-run, and this plan takes nothing else. 105–109 are reserved and not
free; 110 is claimed by the orchestrator design; 111, 112 and 113 are written in the register;
114–118 are the voice-session work's, and 119 is **taken** by the rigor-modes plan (its T8.1) and
**proposed** by the console-on-Windows work (its D12(a)) — none of those is taken by this plan
either, and 123 onward is left free.**

---

## 13. What this change edits, and what it does not

**This change is docs-only, plus one Go test that pins the documents.**

**Created:**

| Path | What it is |
|---|---|
| `docs/plans/2026-10-19-mobile-telemetry.md` | **This document — the plan, and the only deliverable of the work the brief asks for.** |
| `internal/cli/docs_mobile_telemetry_test.go` | The pin, seven tests: this document's own claims — the D-block it takes (D253–D260) and the statement that it is the next free block, the check numbers it takes (120–122), the reserved block it takes none of (105–109) and 110 left alone, the flag names it states exactly, the never-commit rule for the two native config files, the registration of this plan in `docs/mobile/`'s two documents, **that every relative link in the four documents this change edits resolves from its own directory**, and **that `SECRET_ITEMS` is still the two-item list D232's standing keeps**. It writes no code and asserts no behaviour of an app that does not exist: the app-side facts it reads (`mobile/package.json`, `mobile/src/accounts/secret.js`, `mobile/src/accounts/secure-store.js`, `website/test/privacy.mjs`) are read **as text**, and the two it tests for absence are **committed paths**, not files on disk. |

**Edited:**

| Path | The edit |
|---|---|
| `docs/mobile/README.md` | The index: one row for this plan in the file table, and a status sentence saying the plan exists, that it is **plan only**, that **the owner answered all eight rows on 2026-10-19** — **D253 to D260**, **D259 accepted last** — that the owner **passed the gate on 2026-10-22**, and that this work takes **D253–D260** and **checks 120–122**. The "numbers this work holds" table gains the two rows, and the two rows that read the check register (**Checks 100–109** and **Check 110**) gain the dated note that **111–113 are written on `main`** and that this plan read them and took **120–122** instead. |
| `docs/mobile/implementation-plan.md` | **Phase 7 — APPROVED by the owner on 2026-10-22, on merge of this plan**: the phase summary row, the five tasks with their files, dependencies, verification and reviewer tier, the level table, and the check rows; a step **7b** in the approval gate, which records the date, the condition (*on merge*) and that **every row it names is answered, D259's acceptance last**; an open item; and the cross-task dependency rows. **Re-edited 2026-10-22**: **Global Constraint 4**'s *two items per account* carries a dated amendment of 2026-10-22 and one of 2026-10-29, which leaves the two items and adds the pairing-delivered configuration (§4.4). **P7.2 is rewritten to D255's answer** (the always-on statement and the link, in place of the consent gate), and the check rows to **120/121/122**'s new text. |
| `docs/mobile/reconciliation.md` | **D111's own row**, amended 2026-10-22 for **D259's acceptance**: the recommendation column's *"Exactly two items, per account"* keeps its original text, and a **dated note** under it records that the owner accepted D259 on 2026-10-19, that the list was recorded as four, and a dated note of 2026-10-29 records the owner's ruling that D232 stands: the list stays **two** items plus the pairing-delivered configuration (the installation's **Realtime Database URL** and its **web API key**, ordinary configuration). The two later sentences that repeat *"two items per account"* (the tension-(b2) note and the summary line) carry the same dated amendment. |
| `docs/mobile/implementation-plan.md` (**Global Constraint 4**) | The constraint that says the app holds **exactly two items per account**, amended 2026-10-22 in the style **Global Constraint 12** already uses: the original sentence stays, and a dated note records D259's acceptance and then the owner's ruling of 2026-10-29 that D232 stands, with §4.4 of this plan as the record and the pairing payload named as where the configuration comes from. |
| `docs/mobile/design/push-notifications.md` | The two sentences that say the phone holds *"exactly two items per account 'in these words and no wider ones'"*, each amended 2026-10-22 with a dated note: the words are D111's and the list they close stays **two** items plus the pairing-delivered configuration, D232 standing, and nothing in that document widens it. |
| `docs/mobile/design/voice-evaluation-switch.md` | The sentence that quotes Global Constraint 4's *"exactly two credential items per account"*, amended 2026-10-22 and 2026-10-29 with dated notes pointing at the same record. |
| `docs/mobile/functional-spec-reconciliation.md` | The row that cites **D111 / tension (b2)** for pairing-token lifetime and revocation, amended 2026-10-22 with a dated note: the *two items per account* it repeats stays two, plus the pairing-delivered configuration, under the owner's ruling that D232 stands. |
| `docs/rigor-modes/reconciliation.md` | **One census row**, and one number in it: the count of `internal/cli/docs_*_test.go` pins, **53 → 54**, because this change adds the fifty-fourth. The row reproduces its own command (`ls internal/cli/docs_*_test.go \| wc -l`), and `TestDocsRigorModesCensusStatesItsCommands` reads it, so the row moved with the tree rather than being left at a number the tree contradicts. **This row is the reason this document is in this table at all**: it edits a document that is not one of this work's own, and a table that declared only the mobile files would make the walk in `docs_mobile_telemetry_test.go` treat this file as one this plan never touched — the silence that pin exists to refuse. |
| `docs/ee-reorg/README.md` | Names D260 once, as the top of the highest open decision block when that work took its own numbers: a read-only reference, not an edit of this plan's block |
| `docs/licensing/README.md` | Names D260 once, as the top of the highest open decision block when that work took its own numbers: a read-only reference, not an edit of this plan's block |
| `docs/licensing/reconciliation.md` | Names D260 once, as the top of the highest open decision block when that work took its own numbers: a read-only reference, not an edit of this plan's block |
| `docs/task-governance/README.md` | Names D260 once, as the top of the highest open decision block when that work took its own numbers: a read-only reference, not an edit of this plan's block |
| `docs/review-levels-ab-jobmax/reconciliation.md` | Cites D253, D255, D257 and D259 as the owner's earlier answers on crash reports and analytics, which the owner's ruling of 2026-10-30 (D731) supersedes: read-only references to this plan's block, not an edit of it |
| `docs/review-levels-ab-jobmax/implementation-plan.md` | Cites D255 for the same superseded ruling: a read-only reference, not an edit of this plan's block |

**Not touched by this change:** `internal/cli/docs_mobile_plan_test.go` — **and that is the point
of the approval.** Its Phase 6 pin bans `NOT APPROVED` anywhere in the plan, as a stand-in for
*"no approved phase may still claim to be unapproved"*. The draft on **PR #693** had to rescope
that ban per line, because its Phase 7 was written and genuinely unapproved. **Phase 7 is
approved on 2026-10-22**, so the ban holds as written, **this change moves no existing pin**, and
the property is held by the test that already held it. Also `docs/coordinator/README.md` — its numbers table is the
coordinator's own reading of 2026-10-21, and it stops at that work's D193–D197: the six blocks
taken after it (**D198–D252**) are not registered there either, so this plan registers its block
where its own work registers one, in [mobile/README.md](../mobile/README.md), rather than being
the single later work to reopen a table thirteen tests pin; every file under `mobile/`,
`shared/`, `website/`, `docs/design/`, and the `docs/website/` documents; `docs/gcp-live-checklist.md` (checks 120–122
are **written by P7.5**, after approval, and no row or section for them exists today — the pin
asserts that); and **anything under `.github/workflows/`** — no workflow is written by this
change and none is staged in `docs/pending-workflows/`, because **no part of this plan's first
release needs one** (a CI job for `mobile/`'s tests would be a later, separately-approved
change, and §10.2 says so).

**On the Go pin, against the brief's "docs only".** The brief asks for a plan and no code, and
this change adds a **test** — `internal/cli/docs_mobile_telemetry_test.go` — and **moves no
existing pin**: the Phase 7 approval is what lets the mobile plan's own status pin stand
unchanged. That is a judgment call and it is stated
here rather than hidden: this repository's convention is that a planned document is pinned by a
Go test (over forty such pins exist, and both the mailbox plan and the push design landed with
theirs), and a plan whose claims nothing holds is a plan whose numbers can drift silently. The
new file asserts **nothing about an app's behaviour** — every assertion is about the documents
this change edits, the register, the two native config files' committed absence, and the two
fences D232's standing keeps shut, each read as text or as a path git lists and never
run. **Four mutations were run and
restored**, each recorded in the comment of the test it mutates rather than in a header above
them: two on the headline test (widening the block to D252, and deleting a row from this
document's own §13 table), one on the never-committed test (`git add -f` on an empty
`mobile/google-services.json`), and one on the link test (pointing a link back at the wrong
prefix — the mistake this change made five times).

---

## 14. UNVERIFIED

The repository's habit is to say what could not be read rather than to smooth it over. Each
line below is a claim this plan makes **without** a source in this tree:

1. **The flag names of §4.2 — including the two that keep the advertising identifier out.** Read
   from memory of the SDKs' documentation, not from a page read on 2026-10-19. P7.1 confirms
   each against the SDK's own documentation and the generated native project, and corrects §4.2
   in the same commit if any name is wrong. The *shape* the plan requires is independent of the
   spelling, and D255's answer is what fixes it: **every collection switch is on, every ad-ID
   switch is off, and no runtime call turns collection on or off.**
   **Two specifics are marked VERIFY in §4.2 and both are about the ad-ID promise**, which is
   why they are called out here rather than left inside that table: (a) the **exact pod or plugin
   option** that makes `@react-native-firebase/analytics` link the Firebase Analytics variant
   with **no ad-ID support** — **RESOLVED 2026-10-30: `ios.withoutAdIdSupport`**, read from the
   pinned package's config-plugin source and podspec; and (b) whether
   removing `com.google.android.gms.permission.AD_ID` from the merged manifest is **the config
   plugin's own option** or a `tools:node="remove"` entry this plan has to add — **RESOLVED
   2026-10-30: no plugin option exists**, so Expo's own `android.blockedPermissions` writes the
   removal. **The generated-project half of each read is still the owner's**: nothing in this
   repository prebuilds, so `ios/Podfile.lock`'s absence of `AdSupport` and the merged
   `AndroidManifest.xml`'s absent permission are read at the first store build that carries the
   SDKs (check 120), not here. Neither is a live action and neither
   reads a credential.
2. **EAS file secrets.** The `eas secret:create --type file` mechanism and the `eas.json`
   `env` reference of §4.3 are described from memory. **RESOLVED 2026-10-30 (P7.1b): the
   mechanism actually chosen is EAS environment variables with file visibility**, set in the
   EAS environments each build profile names through `eas.json`'s `environment` key; the
   `eas secret:create` commands stay as one way to create them, and **§4.3's `eas.json` `env`
   sentences and P7.1's "modifies `eas.json`" lines are retired** — an `env` block there would
   override the EAS environment rather than deliver it. The plan's requirement is unchanged
   either way (the file exists only on the build machine).
3. **`@react-native-firebase`'s support for a second Firebase project in one binary.** D259's
   **accepted** answer needs the **default** app to be the telemetry project and each
   installation to be a **named** app (`firebase.app(<account id>)`). The plugin's config, and
   how a named app is given a project the binary does not carry a native file for, are **not
   verified here**. P7.1's first commit reads the SDK's own documentation for it, and if the
   named-app route turns out to be unavailable, **the plan's alternative is a new question for
   the owner** — D259 is answered, and re-opening it is the owner's to do, not P7.1's — the plan
   states both routes rather than assuming the mechanism.
4. **`mobile/app.config.js` and `mobile/eas.json`.** They are P4.4's, merged to `main` by **PR
   #675** and read here at `4d619273`. §4.3's guard and §10's P7.1 are written against the shape
   that PR landed — the dynamic `({ config }) => …` export, the three-state `EAS_BUILD` /
   `FUGARO_REQUIRE_IDS` guard, and the three build profiles — and P7.1 re-reads both files when
   it starts, since a later change to either moves under this plan's sentences.
5. **`dev.fugaro.app`.** The brief's name for the first internal build's environment. It is
   not in this tree. Nothing in this plan is built on it, and no task names it as a file.
6. **The website's privacy statement is quoted, not pinned.** PRs **#689** (`89fecab4`) and
   **#705** (`190b8476`) are merged and the page is read here on `main` at `4d619273`. §8.3
   quotes the two sentences it turns on and describes the page by its parts and its headings
   rather than by line, so a later edit to the page moves nothing in this document — **but P7.4
   reads the page as it then stands**, and a **third** change to it between this reading and
   P7.4's merge is not something this plan can see. `website/test/privacy.mjs` is named by its
   path only, and **this change does not run it**: it runs in `website/`'s own `npm test`, and
   no CI job in this repository runs that (UNVERIFIED 10).
7. **The two store forms' current contents.** The six Play categories are the owner's own
   description of what was ticked, not a reading of the console. §8.1 answers them from §7's
   allow-list and says which two are over-declared; **only Check 122 confirms what the console
   actually holds.**
8. **No latency, cost, retention or volume number.** Firebase Analytics and Crashlytics are
   free at this scale, and the plan states no figure it has not read. **D254 is answered and the
   retention setting is still not recorded** — §5 step 4 is where the owner notes it, and no
   figure in this document is a measurement.
9. **`fugaro-prod`'s own state.** The facts §5 states about it — that it is the project the
   website is deployed into, that the website's deploy service account holds Firebase Hosting
   roles in it, that it holds no Analytics or Crashlytics read, and that the owner accepted the
   arrangement — are **the owner's answer of 2026-10-19**, recorded here. **Nothing in this
   change reads the project's IAM**, no live action of any kind is taken, and **no service
   account, key or credential is read or named**. If the project's membership differs from the
   answer, §5 is what is wrong and the owner's correction is what fixes it.
10. **Nothing under `mobile/`, `shared/` or `website/` was run for this change.** The machine
   this plan was rebuilt on has **no Node** (`node --version` → `command not found`; the
   repository's `mise.toml` pins `go` and `terraform` and no Node toolchain), so no `npm test`
   ran in any of the three, no `npm ci` ran, no `expo prebuild` ran, no `eas build` ran, and no
   Firebase project exists. The plan's own pin
   (`internal/cli/docs_mobile_telemetry_test.go`) is the only executable thing this change adds,
   and it is run by the Go suite; **it asserts nothing about an app, because there is no app
   behaviour yet to assert.** Two consequences a reader should carry: **`website/test/privacy.mjs`
   was read and not run** — landing this plan flips its `planExists` branch, which *relaxes*
   direction B rather than tightening it, and the guarded `EARLIER_BUILDS` assertion is skipped
   once the file is in the tree — and a reader who wants the app's tests run before P7.1 starts
   should run `cd mobile && npm ci && npm test`, the command §10.2 gives every task, because
   **no CI job here runs it**.

---

*Status of this document: a plan, **and the owner has passed the gate for it — 2026-10-22, on
merge of this plan**. Nothing is built, and no code,
configuration or workflow *of the app, the website, the CLI or CI* is written by the change
that adds it — it adds this document, the entries registering it, and the one Go test that pins
them (§13). **The owner answered all eight rows on
2026-10-19**: **D253** (the ruling the plan is built on — Crashlytics and
Analytics, reporting to **`fugaro-prod`**, a Firebase project Fugaro operates), **D254**,
**D255** (**always on** — no switch, no sheet, against this plan's opt-in recommendation,
which stays visible in the row), **D256**, **D257** and **D258** (each as
recommended), **D259 — accepted** (the telemetry project's two native config files are
**built into the app** as EAS file secrets; **each installation's own** Firebase connection
cannot be, so it arrives in the **encrypted pairing payload** and lives in **secure storage**,
which the owner's ruling of 2026-10-29 left at two items per account, D232 standing — §4.4 states the record), and
**D260** (as recommended). **The owner then passed the approval gate on 2026-10-22**, on the
condition this document states — **that the plan is merged first** — so **P7.1–P7.5 are approved
and may start when this plan lands**; step **7b** of
[mobile/implementation-plan.md](../mobile/implementation-plan.md) carries the date. This work
takes **D253–D260** — the next free decision block, read from the tree (D252 is the highest
number written anywhere in `docs/`) — and **checks 120, 121 and 122**, the first free numbers
after every claim (105–109 are reserved, 110 is claimed, **111–113 are written on `main`**, and
114–119 are claimed by other works — 119 **taken** by the rigor-modes plan and **proposed** by
console-on-Windows), leaving **123 onward free**. **PR #693 is
superseded**: it asked for D193–D200 and checks 111–113, and this document takes the numbers
that were free at its own merge instead. Cheers: none of the three checks has
been run. Related: [mobile/README.md](../mobile/README.md) ·
[mobile/implementation-plan.md](../mobile/implementation-plan.md) ·
[gcp-live-checklist.md](../gcp-live-checklist.md).*
