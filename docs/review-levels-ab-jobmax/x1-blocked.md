# X1 — Checks 270, 271 and 272 — is blocked: T3.1 is not built

**Kept as the record of 2026-11-15; superseded in part on 2026-11-21.** The re-plan of T3.1, V1.3 and X1 (the
owner's instruction of 2026-11-21) re-words 271 against the console's shipped per-user switch and 272 against
T3.1's policy member, and drops the two checks' reliance on T1.2's retired `crash_reports: false`. What blocks X1
now is build order alone — T2.1 and T3.1 are not built — and the plan's X1 section is the current text. This note's
facts about the register (below) are as they were on 2026-11-15.

**Nothing was written into `docs/gcp-live-checklist.md` for this task, and this note is the reason.**
X1's own **Depends on** line is `L4.2, T2.1, T3.1` (plan line 540), and T3.1 — the app's
Crashlytics following the project switch, with Analytics removed — is not on `main`. Check 272 reads
exactly the behaviour T3.1 would build, so the register cannot carry it yet. This note is the
evidence, and it is this pull request's whole change: no register row, no register section, no
script, and `docs/status.md` untouched.

## The numbers are free

`grep -n '^## Check 27[0-3]\|^| 27[0-3] ' docs/gcp-live-checklist.md` prints nothing on
`ab2735b4`, so **270, 271, 272 and 273 are all still free**. Nothing about the numbering blocks X1,
and the register is not in the state #620 would leave it in: 273 is free too, so X1 and X2 both have
their numbers when they run.

## X1's dependencies on `main` (`ab2735b4`)

| X1's dependency | state |
| --- | --- |
| **L4.2** — the runner runs level 3, skips L2 after an L3 bounce, and decides by the highest level | **built.** `docs/review-levels-ab-jobmax/implementation-plan.md`'s own **Done (PR #1578)** line; `internal/runner`'s `applyLevels`, `decidingReview` and the `adversarial` stage are on `main` |
| **T2.1** — the console reporter | **built**, as T2.1a–T2.1d: `8618febc` (#1431), `bfa06b84` (#1445), `7eeb754c` (#1448) and `356bf00c` (#1449). `console/src/main/crash.ts`, `crash-dsn.ts`, `crash-electron.ts`, `crashstore.ts` and `console/test/unit/crash.test.ts` are on `main`, wired from `console/src/main/index.ts` |
| **T3.1** — the app: opt-out Crashlytics, no Analytics | **not built.** Nothing of it is on `main`; the evidence is below |

## T3.1: what was checked, and what came back

The commit X1's sibling task is written against — the plan's own **Commit** line,
`mobile: Crashlytics follows the project switch; Analytics removed` — does not exist:

- `git log origin/main --oneline --grep='T3\.1'` returns `ec43c55b` (mobile P7.5), `f69b15da`
  (mobile P7.3), `0f0820a8` (mobile P7.2), `db672fee` (this plan's own docs commit) and tasks of
  other plans (`c9023e64`, `e071253b`, `b23de678`, …). Every one of them is another task's work or a
  body that mentions T3.1; none of them is T3.1's build.
- `git log origin/main --oneline -- mobile/src/telemetry/crash.js` is empty: the module T3.1's
  **Files** line names has never existed on any commit reachable from `main`.
- `grep -rin 'crash_reports\|crashReports' mobile/src mobile/index.js mobile/package.json` prints
  nothing: the app reads no switch, from the installation's `settings` node or anywhere else.
- `mobile/package.json:19` still depends on `@react-native-firebase/analytics` (pinned `27.1.0`),
  and `mobile/app.config.js:324` still lists its config plugin: D731's "Analytics dropped from the
  app" has not landed.
- `mobile/firebase.json:4` still reads `"crashlytics_auto_collection_enabled": true`, where T3.1's
  **Files** line requires `false`.

The tree says the same thing in its own words, so this is not a reading of commit subjects alone:
`docs/mobile/implementation-plan.md`'s status paragraph of 2026-11-14 records "the owner's ruling of
2026-10-30 (D731…) is **unlanded** — Firebase Analytics dropped from the app,
crash reports on by default and opt-out, amended by **T0.1**, built by **T3.1**…", and the
reconciliation's **F27** says the §7.4 scan and the opt-out build are T0.1's and T3.1's and have not
landed. The plan itself orders X1 behind T3.1: T3.1 is level 2 and X1 is level 6.

## Why it blocks X1 rather than merely delaying it

X1's three checks do not stand on the same footing, and only the third is the blocker:

- **270** (a Critical run whose L3 rejects once; the PR body shows L2's head older than L3's, and
  `result.json` records tiers `first`, `senior` and `adversarial`) reads L4.2, which is built.
- **271** (a console crash forced with a debug build's test trigger arriving at the processor with
  type and frames only, and nothing arriving with the switch off) reads T2.1, which is built.
- **272** (the same for the app on a development build, before sign-in and after sign-in with the
  switch off) reads T3.1, which is not. There is no build to read: the app has no switch, still
  carries the package D731 drops, and still sends Analytics' own automatic events.

**One thing the plan's own text no longer matches, recorded rather than fixed.** X1's 271 as the
plan writes it says "with `crash_reports: false`, none arrives", which is T2.1 as the plan described
it: a switch decoded from `project show --json`'s `settings.crash_reports`. What shipped is not
that. The owner's ruling of 2026-10-31 (and D768's own shape) makes the console's switch its own
per-user, local file — `<userData>/crash.json`, one boolean, **off when it is absent**, moved by the
application menu's **Send Crash Reports** item and applied at the next launch
(`console/src/main/crashstore.ts`, `docs/desktop-console/spec.md` §5.1 rule 10 and §7.4, both
amended). The plan marks only its **DSN** bullet superseded, not the switch, so the wording X1 would
otherwise copy has to be read against the console as built when X1 runs. This note reports it; it
changes nothing here.

## What unblocks X1

**T3.1 built** — `mobile/src/telemetry/crash.js` following the `settings` node, Analytics gone from
`mobile/package.json`, `mobile/app.config.js` and `mobile/firebase.json`, and the statement and the
privacy page moved with it. **#620, which edits the same register, merges or rebases first**, as
X1's own **Depends on** line says. After that X1 is a docs-only change — three summary rows and
three `## Check N` sections, in the register's own form, **OWNER-RUN** and **NOT RUN**, no script —
and `go test ./internal/cli -run Docs -count=1` is its whole command.
