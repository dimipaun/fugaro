# Console auto-update: the implementation plan for desktop-console Task 4.4

**Status: shipped in console 0.116.0 (release #1452).** The owner's go of 2026-10-30 approved the
auto-update task chain (Task 4.4 of [2026-10-15-desktop-console.md](2026-10-15-desktop-console.md),
"L1") for console 0.116.0, and no task was approved beyond it. It did not approve any change to the
console's other surfaces, a new IPC operation, a new spawn row, or any redesign step. Decisions
D760 to D772 are this plan's; the owner answered all of them on 2026-10-30, as recommended, and
answered **D762 again** the same day, in the opposite direction — that one is amended below. The
whole chain is on `main` today but for the first console release after 0.116.0, which D771 leaves
to a later cut.

**Where the code stands.** The updater is in the tree and in the release. **U1 (#1378)** is the
policy — `console/src/main/updates.ts` and `console/test/unit/updates.test.ts`, with no electron
import and no dependency. **U2 (#1379)** stages the feed and **#1390** copies it in, so the applied
`console-release.yml` now writes `latest-mac.yml` in the macOS `release` job, lists it in
`console-sha256sums.txt` and attaches it (D765). **U3 (#1392)** puts `electron-updater` **6.8.10**
in `console/package.json`'s runtime `dependencies` — the only entry it has — adds the adapter
`console/src/main/updater-electron.ts`, and starts the controller from `console/src/main/index.ts`
after `whenReady`. **U3b (#1438)** hardens the install path: `autoInstallOnAppQuit` is always
false, and an update installs **only through the notice's "Restart to update"**, only the download
that notice named, and only while no launch flow is active. **U4 (#1439)** is the off switch —
**Check for Updates Automatically** and **Check for Updates Now** in the application menu, whose
state is the one boolean `<userData>/updates.json` — and **U4b (#1444)**, **U4c (#1446)** and
**U4d (#1455)** are its follow-ups: a failed switch write is caught, the switch file is read and
opened without following a link, and the tests U4c's review asked for. **U5 (#1443)** is the docs:
`docs/design/desktop-console.md` §5.1 rule 10, §7.3 and §7.4, Task 4.4's text in
`docs/plans/2026-10-15-desktop-console.md`, Check 59b's preconditions in `docs/gcp-live-checklist.md`
and `console/README.md`.

Two pins that forbade the updater now pin it instead: `console/test/unit/pinning.test.ts` holds the
runtime allow-list at `electron-updater` and imports it from `updater-electron.ts` and nowhere
else, and `console/test/unit/fuses.test.ts` still pins `build.publish` to `null`, which is what
D765 keeps true. Check 59b is written and still **NOT RUNNABLE**: it installs `0.116.0` by hand
and needs the first console release after it, which is not cut yet (D771).

## 1. Decisions

| # | Question | Answer | Date, and why |
|---|---|---|---|
| **D760** | Update channel | **Stable console releases only**: tags `console-vX.Y.Z`, not draft, not prerelease. No beta channel. | Owner, 2026-10-30. One release line keeps the feed and Check 59b simple. |
| **D761** | Check frequency | **At launch, then every 6 hours while the app is open.** | Owner, 2026-10-30. Matches design §7.3 ("on launch and then on a long interval"). |
| **D762** | Install behaviour | **AMENDED 2026-10-30, with what shipped in U3b (#1438): `autoInstallOnAppQuit` is always false, and an update installs only through the notice's "Restart to update", only the download that notice named. Nothing is ever installed on quit.** The answer as planned was: **Download quietly, then a non-blocking "Restart to update" prompt; if ignored, install on quit. Never restart on its own.** | Owner, 2026-10-30; **amended by the owner on 2026-10-30, after U1's review, and built in U3b (#1438).** §7.3's "never silently install without telling the user" reads literally: a "Later" nobody comes back to is not a telling, so installing on quit would install a version the operator was never shown; the prompt is the only install path, and the version it names is the only one an answer may install. |
| **D763** | A launch in progress | **Never install or restart while the launch flow is active** (a launch in flight, or its confirm window open). The updater defers the prompt and any install until the flow is idle. | Owner, 2026-10-30. A restart mid-launch would kill the process that holds the launch write path. |
| **D764** | How the client finds the newest release | The console's releases share `dimipaun/fugaro` with the CLI's and are published `--latest=false`, so electron-updater's GitHub provider (which reads the repository's releases and would meet `vX.Y.Z` CLI releases with no `latest-mac.yml`) and GitHub's `releases/latest` both point at the wrong thing. **The updater lists the repository's releases itself** (one unauthenticated `GET https://api.github.com/repos/dimipaun/fugaro/releases`, a fixed `User-Agent` with no version or machine data), keeps only strict `console-vX.Y.Z` tags per D760, takes the highest, refuses anything not above the running version, and only then hands electron-updater a **generic** feed at `https://github.com/dimipaun/fugaro/releases/download/console-vX.Y.Z/`. | Plan, 2026-10-30; **confirmed by the owner, 2026-10-30.** The only design that satisfies D760 with the release layout the owner ruled on 2026-10-17. |
| **D765** | Who writes `latest-mac.yml` | **The release workflow** (macOS `release` job, after verification, before the sums are written), so `build.publish` stays `null`, no `app-update.yml` enters the bundle, and the manifest is listed in `console-sha256sums.txt` and so covered by the keyless cosign signature. | Plan, 2026-10-30; **confirmed by the owner, 2026-10-30.** Setting a `publish` provider would widen the pinned build keys and put a feed URL in the bundle that D764 replaces anyway. |
| **D766** | The dependency | `electron-updater` **6.8.10** exact (published 2026-09-26; 6.8.11 and 6.8.12 are under two weeks old). Its dependencies: `builder-util-runtime` 9.7.0, `semver`, `js-yaml`, `fs-extra`, `lazy-val` (already in the lockfile through electron-builder, moving from dev to runtime), and three new packages, `lodash.isequal`, `lodash.escaperegexp`, `tiny-typed-emitter`. `lodash.isequal` is deprecated upstream; recorded, not blocking. Every one is packed into the asar, so `THIRD_PARTY_NOTICES.md` gains their licences and Dependabot (already watching `console/`) sees them. | Plan, 2026-10-30; **confirmed by the owner, 2026-10-30.** The owner's go approves the dependency; this records the exact pin. |
| **D767** | What the app trusts | **(a) Recommended:** HTTPS to the two allow-listed hosts (`api.github.com` for the listing, `github.com` for the feed; the asset redirect target is `*.githubusercontent.com` and is not under the app's control), electron-updater's SHA-512 check of the zip against `latest-mac.yml`, and Squirrel.Mac's code-signature check that the new bundle satisfies the running app's designated requirement (same Developer ID team). **(b)** Also verify the Sigstore bundle over `console-sha256sums.txt` inside the app. | (b) costs a large new runtime dependency (sigstore-js and its tree) in the process that installs code, for a check Squirrel's signature test already makes the last word. **Owner, 2026-10-30: (a).** Squirrel's same-team check is the last word; no Sigstore code in the installer process. |
| **D768** | Where the off switch is, and where its state lives | **(a) Recommended:** a checkbox item **Check for Updates Automatically** (and **Check for Updates Now**) in the application menu, its state in a new `<userData>/updates.json` holding one boolean, written only when the switch changes; §5.1 rule 10 is amended to name the file. **(b)** The same item, the state as a field in `installations.json`. **(c)** A settings panel in the renderer. | (b) couples the update switch to the installations store and its strict decoder. (c) needs a new screen, a styling-doc section and the shared stylesheets that redesign steps 4b-2 to 7 hold, so it could not start before step 7 merges. **Owner, 2026-10-30: (a).** A menu item needs no screen and no shared stylesheet. |
| **D769** | The CLI's `update_check: false` | Task 4.4 says the console honours it, but §3.4 rule 7 forbids the console reading the CLI's config directory. **(a) Recommended:** the console honours `FUGARO_NO_UPDATE_CHECK=1` and its own switch, and Task 4.4's text is amended. **(b)** `fugaro config list --json` gains the field and the console reads it there. | (b) is a CLI change, a JSON-contract change and a version-skew case for an opt-out the console's own switch already gives. **Owner, 2026-10-30: (a).** Rule 7 stands; Task 4.4's text is amended in U5. |
| **D770** | The updater's own disk writes | electron-updater caches the download under `~/Library/Caches/<app>-updater/` and Squirrel.Mac stages under its ShipIt cache. **(a) Recommended:** accept, and amend §5.1 rule 10 and §7.4 to name both as the updater's, removed by the install. **(b)** Notify only: no download, a menu item opens the release page. | (b) drops auto-install, which D5 ruled in. **Owner, 2026-10-30: (a).** Rule 10 and §7.4 are amended in U5. |
| **D771** | The first update-capable release cannot update itself | 0.116.0 is the first build with an updater, so 0.110.0 to 0.115.0 never see it, and 0.116.0 has nothing to update to until a later release. **(a) Recommended:** say it once in the 0.116.0 notes ("install this one by hand; later ones arrive by themselves") and run Check 59b against the first console release after 0.116.0. **(b)** Cut 0.116.1 soon after 0.116.0 so 59b can run at once. | (b) is a release made only to test the updater. **Owner, 2026-10-30: (a).** No release is cut only for the check. |
| **D772** | Differential downloads | **(a) Recommended:** no `.blockmap`; every update is a full zip download. **(b)** Publish the blockmap too. | (b) adds a file the workflow has to produce, sum and attach, for a smaller download of an app that updates rarely. **Owner, 2026-10-30: (a).** Full downloads only. |

**Never, whatever the answers:** no update on Linux or Windows (D10, design §7.6), none in the
unpackaged dev build (`app.isPackaged` false), none in CI, no request that carries an identifier
(§7.4), no downgrade, no feed URL outside the allow-list, no prerelease, no telemetry.

## 2. Tasks

Every task: `console/` files carry the two-line licence notice of their neighbours; `npm run
typecheck` and `npm run test:unit -- --maxWorkers=2 --testTimeout=60000`; $5 cap; one slice per
run. **Reviewer tier:** U1, U2, U3 and U4 are SECURITY-SENSITIVE and take an **independent opus
review**; U5 takes the standard review.

### U1. The update policy, with a fake updater (level 1)

**Files:** `console/src/main/updates.ts` (new), `console/test/unit/updates.test.ts` (new).
**Interfaces:** `UpdaterPort` (the subset of electron-updater's `AppUpdater` the console uses:
`setFeedURL`, `checkForUpdates`, `downloadUpdate`, `quitAndInstall`, the `autoDownload`,
`autoInstallOnAppQuit`, `allowDowngrade`, `allowPrerelease` and `requestHeaders` properties, and
`on` for `update-available`, `update-downloaded`, `error`); `UpdatePolicyDeps` (platform,
`isPackaged`, env, current version, the switch's state, a clock and timer, an injected
`fetchReleases`, `isLaunchFlowIdle`, `onReadyToRestart`); `createUpdateController(deps)` with
`start()`, `stop()`, `checkNow()`, `restartRequested()`, `launchFlowIdle()`; pure helpers
`newestStableConsoleRelease(json, current)` and `feedUrlFor(tag)`, plus the host allow-list.
No electron import, no dependency.
**Tests:** gate (darwin only, packaged only, never with `CI` set, never with
`FUGARO_NO_UPDATE_CHECK=1`, never with the switch off); check at start and every 6 h on a fake
clock, and not before; stable-only filter (drafts, prereleases, `vX.Y.Z` CLI tags, malformed tags
dropped); downgrade and same-version refused; feed URL built only for the allow-listed host and a
strict tag; no identifier in any request (headers asserted on what the fake receives); the
prompt and `quitAndInstall` never run while the launch flow is busy and run once it goes idle;
`autoInstallOnAppQuit` true in U1's own slice and **false from U3b on, with no install on quit
(D762, amended)**, and `quitAndInstall` only after the user's restart.
**Red step:** the launch-flow test against a controller that installs on `update-downloaded`
regardless of the flow, and the no-identifier test against a request carrying a machine id.
**Depends on:** nothing (D760 to D763 answered; D764 vetoable).

### U2. The feed in the release workflow, staged (level 1)

**Files:** `docs/pending-workflows/console-release-feed.yml` (new: the **complete** intended
`console-release.yml`), `docs/pending-workflows/README.md` (its section, its line in the opening
list, and its copy-in block), `internal/cli/docs_desktop_console_test.go` (a test for the feed
file, written so it passes before and after the copy-in, on the `console-release-tag.yml` and
`console-release-linux.yml` precedent).
**Change, and nothing else:** the macOS `release` job writes `latest-mac.yml` (D765: `version`,
`files[{url, sha512, size}]`, `path`, `sha512`, `releaseDate`, the url being the zip's **asset
name as GitHub stores it**) after verification and lists it in its sums; `publish` checks it and
attaches it. No new secret, no new action, every pin unchanged, no change to the environment, the
approvers, the triggers or the Linux leg.
**Copy-in (the coordinator, on the owner's approval of 2026-10-30, after the independent
review):** `cp` the feed file over `docs/pending-workflows/console-release.yml` and
`.github/workflows/console-release.yml`, `git rm` the feed file, mark the README section applied.
The staging PR writes every test and doc edit so the copy-in is those commands and no more.
**Red step:** the new test against today's `console-release.yml` (no `latest-mac.yml` written,
listed or attached).
**Depends on:** nothing.

### U3. Wire electron-updater (level 2)

**Files:** `console/package.json` (`dependencies`: `electron-updater` 6.8.10), `console/package-lock.json`,
`console/test/unit/pinning.test.ts` (the allow-list gains it; the "no update client" test becomes
"the update client is imported by `updater-electron.ts` only"), `console/test/unit/fuses.test.ts` (`publish`
stays `null` per D765; its comment says the feed is decided), `console/THIRD_PARTY_NOTICES.md`,
`console/src/main/updater-electron.ts` (new: the adapter from electron-updater to `UpdaterPort`,
and the switch read), `console/src/main/index.ts` (start the controller after `whenReady`),
`console/src/main/launch.ts` (an `isIdle()` read of its in-flight and pending state),
`console/test/unit/updater-electron.test.ts` (new), and U1's `console/src/main/updates.ts` and
`updates.test.ts` for the four controller fixes below.
**Found by U1's review (2026-10-30), and part of U3:** electron-updater 6.8.10 sends an
`x-user-staging-id` header on every feed request and writes a random UUID to
`<userData>/.updaterId` (`AppUpdater.js` ~387 and ~501-524); `requestHeaders` cannot remove it,
because it is merged on top. That breaks §7.4 (no identifier) and §5.1 rule 10 (no other file).
The adapter replaces the staging id with a constant shared by every install, so no file is
written, and drops the header from what `computeFinalHeaders` returns. A test checks the headers
electron-updater itself computes, which a fake updater cannot see. Controller fixes: the listing
reads `?per_page=100`; turning the switch off after a download sets `autoInstallOnAppQuit` to
false; a downloaded version that differs from the chosen tag is refused and not installed on quit
(the zip's SHA-512 comes only from `latest-mac.yml`); `requestHeaders` is a plain object, not
frozen, so a later `addAuthHeader` cannot throw.
**Tests:** the adapter sets `autoDownload`, `allowDowngrade = false`, `allowPrerelease = false`,
empty `requestHeaders`; the real header computation carries no staging id and `userData` holds no
`.updaterId`; `isIdle()` is false with a confirm open and with a launch in flight; the
lockfile test still passes on the new entries.
**Red step:** the pinning test with the dependency added and the allow-list unchanged, and the
staging-id test against an unhardened `autoUpdater`.
**Depends on:** U1 merged (D767, D769, D770 answered 2026-10-30). Before the 0.116.0 cut.

### U4. The off switch (level 3)

**Files (D768 (a), owner 2026-10-30):** `console/src/main/updatestore.ts` (new), `console/src/main/picker.ts`
(the menu template gains the two items), `console/test/unit/updatestore.test.ts` (new), the menu
test in `console/test/unit/`.
No renderer file, so U4 does not wait for any redesign step.
**Tests:** default on; off persists across a restart; the file holds exactly one boolean; the
item is visible on macOS only; off stops the timer at once.
**Red step:** the persistence test against a switch held only in memory.
**Depends on:** U3 (D768 answered 2026-10-30).

### U5. Docs and Check 59b (level 4)

**Files:** `docs/design/desktop-console.md` (§5.1 rule 10, §7.3, §7.4, the §5.2 runtime-dependency
line), `docs/plans/2026-10-15-desktop-console.md` (Task 4.4's text per D769), `docs/gcp-live-checklist.md`
(Check 59b's preconditions and `OLD`/`NEW`, per D771), `console/README.md`.
**Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1 -timeout 40m`.
**Red step:** the Check 59b docs test against a section still naming `0.110.0`/`0.111.0`.
**Depends on:** U4 (D771 answered 2026-10-30). No agent runs any step of Check 59b.

### Owner-run (no agent does these)

- The independent review of U2, then the copy-in (delegated to the coordinator on 2026-10-30).
- Tagging console 0.116.0 with U1 to U5 merged, and the next console release after it.
- **Check 59b**, the round trip on a real Mac, against the release after 0.116.0 (D771). This
  plan takes **no new check number**: 59b is the check, and Checks 290 to 299 stay unused.

## 3. Levels and shared files

| Level | Tasks | Runs in parallel with |
|---|---|---|
| 1 | U1, U2 | each other; redesign 4b-2 (#1371) and steps 5 to 7 (renderer only) |
| 2 | U3 | redesign steps 5 to 7 |
| 3 | U4 | redesign steps 5 to 7 (D768 (a): no renderer file) |
| 4 | U5 | — |

| File | U1 | U2 | U3 | U4 | U5 | Other holders |
|---|---|---|---|---|---|---|
| `console/src/main/updates.ts`, `updates.test.ts` | W | | W (review fixes; U1 merged first) | | | — |
| `docs/pending-workflows/console-release-feed.yml`, `README.md` | | W | | | | — |
| `internal/cli/docs_desktop_console_test.go` | | W | | | | — |
| `console/package.json`, `package-lock.json`, `THIRD_PARTY_NOTICES.md` | | | W | | | — |
| `console/test/unit/pinning.test.ts`, `fuses.test.ts` | | | W | | | — |
| `console/src/main/updater-electron.ts`, `index.ts`, `launch.ts` | | | W | | | — |
| `console/src/main/updatestore.ts`, `picker.ts` | | | | W | | — |
| `docs/design/desktop-console.md`, `docs/gcp-live-checklist.md`, `console/README.md` | | | | | W | — |
| `flight-deck.css`, `screens.css`, `console-styling.md`, `screens/*.ts`, `entry.ts` | | | | | | redesign 4b-2 (#1371), steps 5 to 7 |

No level has two writers of one file. `updates.ts` has two writers, U1 then U3, in different levels, and U3 starts only after U1 has merged.

**Launched 2026-10-30:** U1 as run `20261030-161924-1755` and U2 as run `20261030-161930-84ca`,
both from `main`, both file-disjoint from every open PR and running task; merged as #1378 and #1379, and the feed workflow was copied in as #1390. U3 launched after U1 merged.

**And the rest of the chain, in the order it merged.** U3 (**#1392**), the dependency and the
adapter; U3b (**#1438**), the install path hardened — the notice's "Restart to update", the named
download, no launch flow active, `autoInstallOnAppQuit` false, which is D762 as amended; U4
(**#1439**), the two menu items and `updates.json`; U5 (**#1443**), the docs and Check 59b's
preconditions; then U4b (**#1444**), a failed switch write caught, U4c (**#1446**), the switch file
read and opened without following a link, and U4d (**#1455**), the switch tests U4c's review asked
for — tests only, and the one PR of the chain that merged after the tag below.

**Released as console 0.116.0 "Peach" (#1452)**, the first build that carries an updater, cut with
U1 to U5, U3b, U4b and U4c merged; U4d followed it and changed no shipped behaviour. 0.116.0
cannot update itself (D771): an operator installs it by hand, and Check 59b then runs on that
installation against the **first console release after it**, which is still to be cut.
