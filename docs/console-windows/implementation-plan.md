# The console on Windows — the implementation plan

**Status: approved to build on 2026-10-22 — target release 0.11.0, pull requests target `main`; the
twelve decisions are answered and the OWNER APPROVAL GATE in §5 is passed.** Parts land in the order
§3 gives, and each merges alone. **Part A is built** (T-A1 to T-A3, the fake `wsl.exe`): it touches no
production behaviour, which is why it is first and why it is safe first.

**The D7 ruling (2026-10-20): the Windows pack runs at release time only.** It is started by the
release tag (`on: push: tags: ["v[0-9]+.[0-9]+.[0-9]+"]`) or by a `workflow_dispatch` before it, and
**never on a pull request**, not even path-filtered. There is no advisory pack check on a pull request,
and nothing is added to `console-tests.yml`. **The accepted risk: a packaging break shows at release
time**, and it is fixed then, because the `win32` fuse row and the `win` builder target still land
**together, in one part** (F35) and nothing else exercises the Windows pack between releases. The
mitigation is the by-hand dry run the release checklist carries
([docs/release.md](../release.md), "Before you tag"). Everything below that described the pack as a
per-pull-request job has been re-cut for this ruling.

**Owner:** Dimi Paun. **Date:** 2026-10-20. **Base:** `main` at `ccc406b2`.
**Source:** [reconciliation.md](reconciliation.md) — findings F1–F47, corrections C1–C6, decisions
D1–D12, items O1–O7, unverified U1–U19. Every task below names the findings it discharges.

---

## 0. How to read a task

Each task has:

- **Files** — the files it creates, edits or deletes. Nothing else is in scope for the task.
- **Red step** — the test or command that must fail *before* the change and pass after. If a task has
  no red step it is a `Rule` task: it exists to keep something true, and its verification is the guard
  that already owns it.
- **Verification** — the exact command an agent runs. Five recur and are abbreviated afterwards:
  - **`[unit]`** = `cd console && npm run test:unit` — `vitest run --dir test/unit`, over the
    **57** `.ts` files under `console/test/unit`, **54** of them `*.test.ts` (the other three are
    `stub.ts`, `wslstub.ts` and `synthetic.ts`; the count is `find console/test/unit -name '*.ts' |
    wc -l`, and the 75 `find console/test -name '*.ts'` returns is the whole `test/` tree, e2e and
    fixtures included). A task's own run is `npm run test:unit -- <file>`.
  - **`[types]`** = `cd console && npm run typecheck`.
  - **`[pins]`** = `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint|Console' -count=1 -timeout 40m`
    — the Go guards over this folder, `docs/pending-workflows/` and `console/README.md`.
  - **`[site]`** = `cd website && npm run build && node --test test/*.mjs`.
  - **`[verify]`** = `fugaro verify build` then `fugaro verify test` — this repository's own two
    commands, and the only ones that record a result.
  Two more are used once each and are named where they appear: **`[vet]`** =
  `GOFLAGS=-p=2 go vet ./...` plus `gofmt -l .`, run before a part is offered for review; and
  **`[pack]`** = **a manual dispatch** of the staged `console-windows.yml` on a `windows-latest`
  runner — the job is a release-time one, so a dispatch is the only way a task can start it, and it
  **cannot run at all** until Parts B and C have landed (F35). A tag would run it too, and a task does
  not tag.
- **Tier** — who can accept it as *done*:
  - **Tier A** — an owner or agent with no Windows machine: the change is source-readable and its
    evidence is `[unit]`, `[types]`, `[pins]` or `[site]`.
  - **Tier B** — needs a Windows machine or the `windows-latest` runner to accept. The code is
    reviewable anywhere; the *claim* is not.
  - **Tier C** — owner-only: a decision, a secret, a certificate, a paid service or an external
    account.
- **Depends on** — the tasks and parts that must have landed.

**Every task is mutation-proven.** A new test is not finished when it passes; it is finished when the
bug it names has been planted, the test has been seen to fail, and the bug has been removed. The
planted bug is stated in the task's red step, and for the tasks whose subject is a guard the planted
bug is a one-line edit to the guard's own subject.

**Every part merges alone and leaves `main` green on macOS and Linux.** That is the constraint the
order in §3 exists for. No part changes a POSIX code path, and every part's `[unit]`, `[types]` and
`[pins]` are green before it merges. A part that cannot be made green on Linux is not mergeable and
must be split until it is.

**The commit-message convention** is the one this repository uses: a conventional-commit subject
naming the module, and a body that says what the change does and what it does not. The commit
messages are given per task so that a reader can see the shape of the history before writing it.

---

## 1. The one thing this plan does not do

**It does not build a Windows `fugaro`.** Under the owner's ruling the console drives the **Linux**
binary inside a distribution, so this plan adds no Go, no `goos: windows` row, no `.goreleaser.yaml`
change and no Windows archive. `website/data/install.yaml`'s Windows entry — *"Windows is untested:
this repository builds no Windows binary."* — **stays exactly as it is** (F47), and a future edit that
read this work's installer as satisfying it would be false: the site's sentence is about the **CLI**,
and this work ships a console.

The Windows-native CLI is the other track, [docs/windows-cli/](../windows-cli/README.md), and its
plan is not approved. Where this plan touches something that track will also need (the `win32` row in
`PLATFORM_LOCATIONS`, the host `PATH` separator, the native `childPath` join), it says so and keeps
the change **additive**: the WSL route is a resolution path beside the host one, never a replacement
of it, so the native track lands on top rather than through this work.

---

## 2. The parts, and what each discharges

| Part | Brief item | Discharges | Merges alone |
|---|---|---|---|
| **A** — the seam and the fake `wsl.exe` | 6 (unit half) | F33, D11 | yes, first |
| **B** — the packer and the fuse table | 5 (build half) | F35, F36, F37, F38, C3 | yes, independent of A |
| **C** — the resolver, the wrapper and the distro | 1 | F1, F2, F3, F4, F5, F6, F7, F8, F9, F34, C1, C4, C6 | after A |
| **D** — paths | 2 | F12, F13, F14, F15, F16, F17, C5 | after C |
| **E** — the credential boundary | 3 | F18, F19, F20, F21, F22 | after C |
| **F** — spawning, streaming, cancellation, exit codes | 4 | F23, F24, F25, F26, F27, F28, F29 | after C |
| **G** — the first-run pages and the distro picker | 1 (pages) | F10, F11, F30, F31, F32, C2 | after C and F |
| **H** — the release-time pack job, the release text and the staging | 5, 6 (CI half) | F39, F40, F41, F42, F43, F44 | after B and C |

---

## 3. The order

```
A ──► C ──┬─► D ──┐
          ├─► E   ├─► G ──► H
          └─► F ──┘
B ─────────────────────────► H
```

- **A and B have no dependencies and may land in either order.** A is first only because C's tests are
  written against it.
- **C, then D, E, F in any order among themselves.** They touch different surfaces of the same two
  modules, and each is independently green.
- **G after C and F** (its states read C's resolution and F's cancellation residual).
- **H after B and C** — the staged workflow cannot be copied in, and cannot even run, until the packer
  produces an artifact and the unit suite is green on a Windows runner. H is also where the D7(c)
  ruling lands: the staged job is a release-time one (a tag or a manual dispatch, never a pull
  request), and the release checklist gains the by-hand dry run that stands in for the pull-request
  check there is not.
- **The owner's manual checklist (§4) runs after H is copied in and a manual dispatch has produced a
  real installer.** It is the only part of this work that needs a Windows machine, and it is the reason
  Check 119 is *proposed* rather than claimed.

---

## Part A — The seam and the fake `wsl.exe`

*Goal: the tests that will prove everything else can run on a machine with no Windows, because the
double they need exists. Nothing in `console/src/` changes. **This part is Tier A throughout and
merges first.***

**T-A1 — `wsl.exe` as a fake, built on `installStub`.**
Files: `console/test/unit/wslstub.ts` (new), `console/test/unit/wslstub.test.ts` (new).
Red step: `wslstub.test.ts`'s first case — *"the fake records its own argv and the child it started"* —
against a `wslstub.ts` that does not exist: `npm run test:unit -- wslstub` fails to resolve the module.
Then write it. The fake is a real executable named `wsl.exe` (and, on the POSIX test machine, a real
executable named `wsl.exe` too — the name is data, not a platform fact) that:

1. parses `-d <distro>`, `--cd <dir>`, `--`, and the rest as the child's argv;
2. appends one JSON line to the same out-of-band report file `installStub` already uses
   (`console/test/unit/stub.ts:68` `StubReport`), carrying **its own** argv, its own `cwd`, its own
   environment **keys and values for the eleven `CHILD_ENV_KEYS`**, and the distro it was given;
3. requires `--cd` to be the **distribution's** own spelling and refuses the Windows one, because the
   console translates before it builds the plan (T-C5, T-D3, T-D4) — a Windows path here is a Part C
   or D bug, and starting a child in it would report it as an `ENOENT` that says nothing about which
   side spelled the path wrong;
4. `exec`s the stub `fugaro` with the remaining argv and that cwd, so the `fugaro` report line is
   written by the *second* hop and the test can read **what the Linux side received**;
5. answers `--list --quiet` and `--status` from a fixture, in the encoding the test chooses, so that
   T-F2's decoder has a vector (U9) — and **fails** both when the fixture says `wsl.exe` cannot
   answer, which is T-C4's `absent` outcome and not an empty list;
6. answers `wslpath [-a] -u|-w <path>` from a **recorded** table in both directions, because
   translating a path is a process the console calls rather than a mapping it computes (T-D2, D4(b)):
   the table is the test's own, and a path it does not carry is refused with the path in the sentence
   rather than guessed at;
7. runs the `sh -c` wrapper the CLI goes inside (`sh -c 'echo $$ > <file>; exec fugaro …'`) as the
   distribution's own shell would, which is what makes T-F4's pid capturable — the wrapper's `PATH`
   is the distribution's with the directory the stub was installed in in front of it, the double's
   stand-in for the `/usr/local/bin` a real distribution has the CLI in.

Mutation proof: accept a Windows spelling in `--cd` and the refusal case fails by name. Remove step
4's cwd and the child's `cwd` in the report is the fake's own — the cwd test must fail. Answer a
`wslpath` path the table does not carry with a `/mnt/c/…` prefix instead of refusing — the
translation test must fail. Drop the wrapper's `PATH` and `exec fugaro` inside it never resolves —
the wrapper test must fail.
Verification: `[unit]`. Tier A. Depends on: nothing.

**T-A2 — the fake's `PATH` is the distro's, not the host's.**
Files: `console/test/unit/wslstub.ts`, `console/test/unit/wslstub.test.ts`.
Red step: a case asserting that the child `fugaro` reports a **Linux** `PATH` (colon-separated, no
`C:\` entry) even when the test process's own `PATH` is Windows-shaped. Plant the bug by having the
fake forward its own `PATH`; the test fails.
Why this is its own task: it is the fake's half of F5, and it is the only place in this repository
where the *two* separators of one spawn are visible side by side.
Verification: `[unit]`. Tier A. Depends on: T-A1.

**T-A3 — the fake refuses to be the thing under test.**
Files: `console/test/unit/wslstub.ts`, `console/test/unit/wslstub.test.ts`.
Red step: a case asserting that the fake **fails** when a test asks it to run something other than the
three children the plan writes — the stub `fugaro`, the `wslpath` query (T-A1 step 6) and the `sh -c`
wrapper (step 7) — the double must not become a general-purpose shell, because a fake that runs
anything makes every assertion about *what was run* meaningless. A `bash -c`, a `sh -x`, a `sh -c`
with no script, `/bin/echo`, `--terminate`, a missing `--` and a missing `-d` are each refused with a
sentence and a non-zero status.
Verification: `[unit]`. Tier A. Depends on: T-A1.
Commit (A1–A3): `console/test: a fake wsl.exe that reports what the Linux side received`

---

## Part B — The packer and the fuse table

*Goal: `electron-builder --win` produces an artifact. Nothing about the WSL route is in this part; it
is the part that makes the route *shippable*, and it is what the installer job waits for. **Tier A to
write, Tier B to accept.***

**T-B1 — the `package:win` script.**
Files: `console/package.json`, `console/test/unit/fuses.test.ts`.
Red step: `fuses.test.ts:793` pins `scripts.package` exactly; add the `package:win` assertion beside it
— expected `npm run build && electron-builder --win --publish never` — and run
`npm run test:unit -- fuses`; it fails because the script is absent. Then add the script. **Then run
it on the Linux host:** `cd console && npm run package:win` must fail **inside `afterPack`** with the
fuse refusal that names `win32` (`console/src/main/fuses.ts:290`), which is the proof that the fuse
hook — and not the builder — is what stands in the way. Record that output in the commit body.
Tier A. Depends on: nothing.
Commit: `console: a package:win script that packs with the lockfile's electron-builder and never publishes`

**T-B2 — the `win32` row in the fuse table.**
Files: `console/src/main/fuses.ts`, `console/test/unit/fuses.test.ts`.
Red step: `fuses.test.ts:467` pins `SUPPORTED_PLATFORMS` to exactly `['darwin','linux']`, and
`:469`–`:474` asserts `fuseTarget` **throws** for a `win32` context. Rewrite those assertions to the
row's expectations first — the list gains `win32`; the win32 context resolves to
`<appOutDir>/<productFilename>.exe`; a platform nobody decided still throws — and run
`npm run test:unit -- fuses`; all three fail. Then add `win32Executable` beside `macExecutable`
(`fuses.ts:331`) and `linuxExecutable` (`fuses.ts:358`) and add the row.
Two details that are decisions rather than details: the **extension** is electron-builder's own
(`fuses.ts:346`'s comment already records `win32: '.exe'`), and the **file name** is `productFilename`
(`Fugaro Console`, with the space), not `productName` — the Linux row's precedent is that this string
is the packer's, not ours.
Mutation proof: remove the row — `fuseTarget` throws and every win32 test fails by name. Replace the
throw for an unknown platform with `?? linuxExecutable` — the unknown-platform case fails, which is
the mutation the row's shape exists to catch.
Verification: `[unit]`, `[types]`. Tier A. Depends on: nothing.
Commit: `console: a win32 row in the fuse table, and the refusal kept for a platform nobody decided`

**T-B3 — the win32-x64 Electron archive's checksum.**
Files: `console/package.json`, `console/test/unit/fuses.test.ts`.
Red step: `fuses.test.ts:775`–`:781` pins the checksum **keys** exactly (three zips) and reads each
hash out of the lockfile-pinned `electron` package's own `checksums.json`. Add
`electron-v44.7.2-win32-x64.zip` to the expected key list first → the test fails on the key set. Then
add the entry, **copying the hash out of `console/node_modules/electron/checksums.json` after
`npm ci --ignore-scripts`** — never from a network fetch, never typed from memory. A hash typed by
hand is a hash nobody verified, and this pin's whole purpose is that the archive is verified against
something in *this* repository (F37).
Verification: `[unit]`. Tier A, and the hash's provenance is the review point. Depends on: nothing.
Commit: `console: pin the win32-x64 Electron archive against the lockfile's own checksums`

**T-B4 — `build.win`, named rather than inherited.**
Files: `console/package.json`, `console/test/unit/fuses.test.ts`.
Red step: the manifest test's platform-table half (the one that asserts `build.mac` and `build.linux`)
gains the `win` block's shape: `target: ['nsis']`, `arch: ['x64']`, and
`artifactName: Fugaro.Console-${version}-win-${arch}.${ext}` following the Linux pattern. Fails on the
absent block.
NSIS is **named** rather than left to the default: the default Windows target is NSIS today, and a
default is a decision nobody wrote down. `x64` only, matching the Linux leg and T-B3's single pinned
archive.
Verification: `[unit]`, `[types]`. Tier A. Depends on: nothing.
Commit: `console: a win block that names its target, its arch and its artifact name`

**T-B5 — the packer's document, and the packing section's own guard.**
Files: `console/README.md`, `console/test/unit/fuses.test.ts`.
Red step: `console/README.md`'s *"The owner's commands, exactly"* section (and the Linux section
#758 added) is where a pack is described, and the manifest test reads the manifest rather than the
document — so this task's red step is a **new assertion** that the document names every `package:*`
script in the manifest, by name. Write it first; it fails because `package:win` is not described. Then
write the paragraph: what the Windows pack produces, that it is **unsigned**, and that
`electron-builder --win` cannot be run on macOS or Linux until T-B2 lands.
This is the task that stops the next reader from believing a `--win` pack works today.
Verification: `[unit]`, `[pins]`. Tier A. Depends on: T-B1, T-B4.
Commit: `console: describe the Windows pack, and pin the document to the manifest's scripts`

---

## Part C — The resolver, the wrapper and the distro

*Goal: on Windows, the console finds a `fugaro` inside a distribution, says which distribution, and
builds an argv that runs it. **This is the part the ruling is about.***

**T-C1 — the wrapper on `SpawnPlan` (D1(a)).**
Files: `console/src/main/spawn.ts`, `console/src/main/spawnlog.ts`, `console/test/unit/spawn.test.ts`,
`console/test/unit/spawnlog.test.ts`.
Red step: `assertSpawnSurface` (`spawn.ts:719`, pinned at `spawn.test.ts:326`) is the table test over
every spawn row. Add the new shape to it first: a plan whose `command` is `wsl.exe` and whose `prefix`
is `['-d','Ubuntu','--cd','/home/dimi','--']` must (i) pass `assertArgvFlags` on the CLI half,
(ii) be refused by `assertArgvFlags` if a `wsl.exe` flag is put in the **CLI** half, and (iii) produce
a `RunResult` whose reported binary is the plan's `command`, not `argv[0]`. Fails today, because the
fields do not exist.
The shape, concretely: `SpawnPlan` gains `command: string` and `prefix: readonly string[]`; `argv`
stays **exactly what the CLI receives**; `planFor` (`spawn.ts:1106`) sets `command = binaryPath` and
`prefix = []` for the host route; `runToCompletion` (`spawn.ts:1165`) spawns `command` with
`[...prefix, ...argv]` and reports `command` where it today reads `plan.argv[0]` (`spawn.ts:1183`);
the spawn log's entry (`spawnlog.ts:81`) gains the two fields (O4).
Mutation proof: make `runToCompletion` read `argv[0]` again — the reported-binary test fails. Make
`assertArgvFlags` assert the prefix — the prefix test fails on the legitimate `-d`.
Verification: `[unit]`, `[types]`. Tier A. Depends on: nothing (A only for later tasks).
Commit: `console: a named wrapper on the spawn plan, so a command and its CLI argv are separate`

**T-C2 — the win32 location row is the WSL route, and it is not a directory list.**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: `spawn.test.ts:976` pins `Object.keys(PLATFORM_LOCATIONS)` to `['darwin','linux']` and `:979`
pins `locationsFor()`'s fallback to Linux. Rewrite `:976` to the new key set and **replace** `:979`
with the refusal (T-C3), then add the `WIN32_LOCATIONS` row and the test that it carries the WSL
marker rather than prefixes: `binaryPrefixes` empty, `childPathPrefixes` **Linux's**
(`locationsFor('linux').childPathPrefixes`, because the child is a Linux process — F5), and
`route: 'wsl'`.
Why the row is not a directory list: 0.10.0 has no Windows `fugaro.exe`, so a list of Windows
directories would be a list of paths that never exist, and the no-binary page would say *"searched 0
places"* — which is a worse screen than the four honest states Part G draws (F3, C6).
Verification: `[unit]`. Tier A. Depends on: T-C1.
Commit: `console: the win32 row is the WSL route, and its child path is Linux's`

**T-C3 — `locationsFor` refuses a platform nobody decided.**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: the test that `locationsFor('freebsd')` and `locationsFor(undefined)` on an unknown platform
**throw** rather than returning Linux's directories (`spawn.ts:235`). Fails today: the fallback answers
Linux. Check first whether anything depends on the fallback — `spawn.test.ts:979` is the one site, and
this task rewrites it.
Why: a fallback that answers "Linux" for a platform nobody decided is the silent substitution design
§3.4 rule 3 refuses everywhere else, and on `win32` it is a Windows machine told to look in
`/usr/local/bin` (F3).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-C2.
Commit: `console: refuse an unknown platform instead of answering with Linux's directories`

**T-C4 — the distro: enumerate, default, store (D2(a)).**
Files: `console/src/main/wsl.ts` (new), `console/src/main/spawn.ts`,
`console/test/unit/wsl.test.ts` (new), `console/test/unit/spawn.test.ts`, and
`console/test/unit/wslstub.ts` — named because this task is the first to drive the fake's **failing
query** (its `distros.queryFailure`, T-A1 step 5): a task that finds the double cannot model the state
it must classify edits it here, in the file that owns the seam, rather than working around it.
Red step: `wsl.test.ts` drives the fake `wsl.exe` (T-A1) through `wslDistributions()`: a fixture with
two distributions and a default returns both, in the order `wsl.exe` gave them, with the default
marked; a fixture where `--status` names no default returns both with **none** marked, and the caller
must ask; a `wsl.exe` that exits non-zero because WSL is not installed returns the `absent` outcome
rather than an empty list. All three fail against a module that does not exist.
`wslDistributions()` is the only new source of truth here, and it is one function so that the picker
(G) and the resolver (C5) cannot disagree about what a distro is.
Verification: `[unit]`. Tier A for the shape; the *decode* is Tier B (U9). Depends on: T-A1, T-C1.
Commit: `console: enumerate the distributions once, and say when there are none`

**T-C5 — the WSL resolution path, and the four refusals.**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: the resolver tests, through the fake:
- a stored installation with a `distro` resolves to a plan whose `command` is the `wsl.exe` the fake
  installed and whose `prefix` is `['-d','Ubuntu','--cd','<linux dir>','--']` and whose `argv[0]` is
  **`fugaro`** (not a path — it is resolved by the distro's own `PATH`, which is the point of running
  it inside);
- **WSL absent** → a `BinaryResolution` refusal whose kind is `no-wsl`;
- **no distribution** → kind `no-distro`;
- **the distribution has no `fugaro`** → kind `no-fugaro-in-distro`;
- **the distribution exists and `fugaro` is there but the version probe fails** → the *probe's*
  refusal, unchanged from today (`spawn.ts:1518`), so a broken CLI is not reported as a missing one.
  All five fail today.
The classification is by the *probe*, not by a second call: the existing `probeVersion`
(`spawn.ts:1490`) is the check, and the extra classification runs **only** when the probe fails, so
the happy path stays one process. That is what keeps a cold-start distribution (U15) out of the
common path.
`resolveInstalledBinary` (`spawn.ts:1400`) keeps its property that exactly one binary is considered
(F7): a stored installation's distro is the only distro tried, so a renamed or removed distribution is
a stated failure rather than a silent switch to another one.
Mutation proof: make the fallback pick the *default* distro when the stored one is missing — the
renamed-distro test fails.
Verification: `[unit]`, `[types]`. Tier A for the shape; Tier B to accept the argv against a real
`wsl.exe`. Depends on: T-C2, T-C4.
Commit: `console: resolve the CLI inside a named distribution, with four honest refusals`

**T-C6 — `isExecutableFile` is not asked about a path the host cannot see (F4).**
Files: `console/src/main/spawn.ts`, `console/src/main/installations.ts`,
`console/test/unit/spawn.test.ts`, `console/test/unit/installations-store.test.ts`.
Red step: a test that a stored installation whose `distro` is set and whose `binaryPath` is
`/home/dimi/.local/bin/fugaro` is **accepted by the store's validation** (`installations.ts:619` calls
`isExecutableFile` there today, and a Linux path fails `statSync` on Windows) — and that the same
installation with **no** `distro` and the same path is still **refused**, because on the host route a
path that cannot be stat'ed is not a binary. The first fails today; the second must keep passing.
The rule this encodes: the mode question is about the *host*, and a path inside a distribution is not
the host's to answer. The distro answers it (T-C5).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-C5.
Commit: `console: ask the host's mode question only about the host's paths`

**T-C7 — the runner's two POSIX repairs (F5, F4's host half).**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: `spawn.test.ts` gains a case that drives `binDirectories` with a `platform: 'win32'` and a
Windows-shaped `appEnv.PATH` (`C:\a;C:\b`) and expects **two** candidates, and a case that drives it
with `platform: 'linux'` and a colon-separated `PATH` and expects two as well. The first fails today
(the whole string is one entry). Then `spawn.ts:825` splits on `separatorFor(platform)`, and
`isExecutableFile` (`spawn.ts:850`) gains a platform argument whose `win32` answer is "a regular file"
— with the POSIX branch **unchanged**, which the existing non-executable-file test still proves.
**`childPath`'s `join(':')` does not change** (`spawn.ts:920`): its platform argument already means
the platform the *child* runs on, and on the WSL leg the child is Linux (F5, T-A2).
Verification: `[unit]`, `[types]`. Tier A. Depends on: nothing in C (this is the runner's repair and
it could land first; it is here because its red step needs T-A2's fake to state the child's `PATH`).
Commit: `console: the host's PATH separator and the host's mode question, both platform-aware`

---

## Part D — Paths

*Goal: one thing, one spelling, said out loud — and the console's own store stays on the host.*

**T-D1 — the schema gains `distro`, by name, in one version step (F15, F46).**
Files: `console/src/main/installations.ts`, `console/test/unit/installations-store.test.ts`.
Red step: the store test's `hasExactlyKeys` cases gain `distro` — an entry **with** it is accepted, an
entry with a **misspelled** key is refused by name, and a **pre-`distro`** file on disk is read and
**accepted** (its installations are host installations, which is a state and not an error). The third
fails today, because an unknown-to-the-reader key set is refused.
The version step matters more than the field: a console that wrote the new key and then refuses to
read its own file on the next start loses every remembered installation (F46).
Verification: `[unit]`. Tier A. Depends on: T-C5.
Commit: `console: a distro on a stored installation, read as host when it is absent`

**T-D2 — `wslpath`, both directions, through the same spawner (D4(a)).**
Files: `console/src/main/wsl.ts`, `console/src/main/spawn.ts`, `console/test/unit/wsl.test.ts`, and
`console/test/unit/wslstub.ts` — named because the fake is what answers the translation: its `paths`
table is the fixture this task's four assertions are written against (T-A1 step 6), and the direction
flags, the recorded arguments and the refusal for a path the table does not carry are all asserted
through it.
Red step: against the fake — `toLinux('C:\\Users\\dimi\\src')` returns the fixture's Linux path;
`toWindows('/home/dimi/src')` returns the UNC form; a path the fixture's table does not carry comes
back as a **stated refusal** rather than as a guess; and both calls are recorded in the report file so
a test can assert the *arguments* the console sent, not only the answer it got. All four fail against
a module that does not exist.
The translation is a process, not a function, and the plan says why: a mapping computed in the console
is right for `C:\Users\X` → `/mnt/c/Users/X` and **wrong** for every custom mount, every `--cd` outside
`/mnt`, and every distribution with its own `/etc/wsl.conf` (D4(b)).
Verification: `[unit]`. Tier A for the shape; Tier B for the round trip against a real distribution
(U10). Depends on: T-A1, T-C4.
Commit: `console: translate paths with wslpath, in both directions, and refuse rather than guess`

**T-D3 — which spelling goes where (D3(a)).**
Files: `console/src/main/spawn.ts`, `console/src/renderer/screens/degraded.ts`,
`console/test/unit/spawn.test.ts`, `console/test/unit/degraded.test.ts`.
Red step: a test that a WSL installation's plan carries the **Linux** spelling in `--dir` and
`--config` and that the state the screen is handed carries the Linux spelling for display — with the
UNC spelling appearing only on the field the "reveal" affordance reads. Fails today, because there is
one spelling and it is whatever the operator typed.
The rule, in one line: **the Linux spelling everywhere the console states or passes; the UNC spelling
only where Windows itself opens the path.** The Linux one is the one the operator can paste into a
terminal inside the distribution, which is what makes it the right one to show (F16).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-D2.
Commit: `console: the Linux spelling for what we say and pass, the UNC spelling for what Windows opens`

**T-D4 — the private working directory on a Windows host (F12).**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: a test that `privateWorkingDirectory({ platform: 'win32' })` returns a directory that exists,
is empty, is stable across two calls, and **makes no mode claim** — while every existing POSIX
assertion (`spawn.test.ts:858`'s `mode & 0o777 === 0o700`, `:896`'s symlink refusal, `:910`'s uid
refusal) stays green.
Two things this task must not do: it must not drop the POSIX branch's checks (they are the reason the
directory is private there), and it must not describe the Windows result as `0700` — Windows reports
two file modes and the console sets no ACL (F45, U11).
On the WSL route the directory is created **on the host** and its Linux spelling goes into `--cd`
(T-D2), which is why this task is in Part D rather than Part F.
Verification: `[unit]`, `[types]`. Tier A to write; Tier B to accept (U11). Depends on: T-D2.
Commit: `console: a private working directory on Windows that claims only what it can keep`

**T-D5 — the store stays on the host, and `isInside` keeps its meaning (F13, F14).**
Files: `console/src/main/installations.ts`, `console/test/unit/installations-store.test.ts`.
Red step: an `isInside` case with two Windows-shaped paths where the inner one is a sibling whose name
merely **starts with** the outer one's (`C:\Users\dimi\AppData` and `C:\Users\dimi\AppData2`) — it must
be **outside**, and it is inside today because the appended separator is `'/'`. Then `isInside` uses
`path.sep` (or `path.relative`).
The second half is a `Rule`: a test that `installationStorePath()` is under Electron's `userData` and
that nothing in this work moves it. The temptation this refuses is real and is written down so the
next reader does not take it: putting the store inside the distribution so that both sides spell one
path would put the console's own file where the operator's `fugaro` reads (F13, F14).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-D1.
Commit: `console: the store stays on the host, and the inside-check uses the platform's separator`

**T-D6 — the CLI's config directory, in the distro's terms (F17).**
Files: `console/src/main/installations.ts`, `console/test/unit/installations-store.test.ts`.
Red step: a test that with a WSL installation stored, the store's refusal set names the **distro's**
`~/.config/fugaro` — spelled the way the distro spells it — beside the host's three
(`installations.ts:261`). Fails today, because all three are computed from the console's own
environment and home.
Tier A for the shape; the distro's home is the owner's to confirm (U10's sibling). Depends on: T-D2.
Commit: `console: name the distribution's own config directory in the store's refusal set`

---

## Part E — The credential boundary

*Goal: prove the property the tree already has, in the one place it is now harder to see — with
`wsl.exe` in the middle — and add nothing to it.*

**T-E1 — the child's environment through the wrapper, asserted from the child's own report (F18).**
Files: `console/test/unit/spawn.test.ts`, `console/test/unit/wslstub.test.ts`.
Red step: a test that spawns a WSL plan through the fake and asserts the **second hop's** reported
environment has exactly the eleven `CHILD_ENV_KEYS` (`spawn.ts:247`) and **no** other key — with a
Windows-shaped `process.env` on the test process carrying a planted sentinel, and the assertion that
the sentinel is absent. Plant the bug by adding `...process.env` to `childEnvironment`
(`spawn.ts:894`); the test fails by naming the sentinel.
This is the whole of D5(a)'s enforcement, and it is a test rather than a review because the property
is one line away from being lost (F18).
Verification: `[unit]`. Tier A. Depends on: T-C5.
Commit: `console: the child's environment, asserted from inside the distribution`

**T-E2 — `WSLENV` is stated empty, not merely unset (F20, D5(a)).**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: a test that a WSL plan's child environment carries `WSLENV` with the **empty string** as its
value, and that a host plan's does not carry the key at all. Fails today: `WSLENV` appears nowhere in
the repository.
Why: `wsl.exe` shares a Windows variable with the distribution only when its name is listed in
`WSLENV`; with the variable absent the list is empty, and with it empty the list is empty *and the
console has said so*. The exact default when it is absent is U4, and the plan sets it rather than
relying on it.
Mutation proof: remove the key — the test fails. Add `'PATH'` to its value — the test fails, which is
the mutation that matters (a shared `PATH` is a Windows path in a Linux child).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-C5.
Commit: `console: WSLENV empty on the WSL leg, so the share list is stated rather than assumed`

**T-E3 — nothing secret on argv, and nothing read (F19, F21, F22) — a `Rule`.**
Files: `console/src/main/spawn.ts` (only if the guard finds something), `console/test/unit/spawn.test.ts`,
`console/test/unit/secret-hygiene.test.ts`.
Red step: extend the existing `SPAWN_FLAGS` (`spawn.ts:272`) and `ARGUMENT_RE` (`spawn.ts:534`) guard
tests to the **prefix** as well — the prefix carries a distro name and a directory, and both must
satisfy the same alphabet a project name does, which today they would not (a distro name is
`[A-Za-z0-9._-]+` and a Linux path is not in `ARGUMENT_RE`'s alphabet, so the check is a **distro-name**
check and the directory is exempt by name with the reason written beside it). Then the two standing
guards stay green: `secret-hygiene.test.ts`'s scan over `console/src/` for secret-shaped literals, and
the test that `stdio` is `['ignore','pipe','pipe']` at both spawn sites (`spawn.ts:1087`,
`spawn.ts:1194`).
This task adds no capability. It is here because Part C widened the argv, and a widened argv is exactly
where the next person would add `--token` (F19, F22, D5(b)).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-C1, T-E2.
Commit: `console: the wrapper's argv obeys the same alphabet, and stdin stays closed`

---

## Part F — Spawning, streaming, cancellation and exit codes

*Goal: `watch` streams, a read finishes, cancel means cancel, and a failure says which failure.*

**T-F1 — the exit status through the wrapper (F26).**
Files: `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`.
Red step: through the fake, a child that exits `7` yields `RunResult.code === 7`; a child that exits
`0` yields `0`; and a `wsl.exe` that fails **before** starting the child yields a result whose
`code` is non-zero **and** whose `stderr` is classified as a WSL failure rather than handed to the
CLI's error path. All three fail today, because there is no wrapper and no classification.
The rule: a `wsl.exe` failure is never read as a `fugaro` exit code (F26, U7, U8).
Verification: `[unit]`, `[types]`. Tier A for the shape; Tier B for the real status (U7). Depends on:
T-C5.
Commit: `console: the wrapper's exit status, and a WSL failure that is not a CLI failure`

**T-F2 — the distro list's encoding (F27).**
Files: `console/src/main/wsl.ts`, `console/test/unit/wsl.test.ts`.
Red step: two fixture vectors for `--list --quiet` — one UTF-8 with `\n` separators, one UTF-16LE with
`\r\n` and a BOM — both decoding to the same two distribution names, with a case that a name
containing a NUL byte is **refused** rather than truncated. Fails today, because the decoder does not
exist. `WSL_UTF8=1` is set on the call and the plan records that the runner's honouring of it is U9.
Verification: `[unit]`. Tier A for the vectors; Tier B for the real bytes (U9). Depends on: T-C4.
Commit: `console: decode the distribution list, both encodings, and refuse a NUL`

**T-F3 — the feed through the wrapper (F28, F29).**
Files: `console/test/unit/spawn.test.ts`, `console/test/unit/spawnlog.test.ts`.
Red step: a `watch` feed driven through the fake, with one line ending in `\r\n` and one **split across
two chunks mid-character**, must produce exactly the same decoded events as the host route does — the
existing `decodeFeedLine` (`spawn.ts:1544`) and `StringDecoder` (`spawn.ts:1043`) behaviour, asserted
against the wrapper rather than assumed to carry over. Plant the bug by making the fake forward the
child's stdout without the second hop's boundary; the split-character case fails.
Verification: `[unit]`. Tier A. Depends on: T-C5.
Commit: `console: the feed is the same feed through the wrapper, asserted rather than assumed`

**T-F4 — cancellation reaches inside the distribution (F24, D6(a)).**
Files: `console/src/main/spawn.ts`, `console/src/main/wsl.ts`,
`console/test/unit/spawn.test.ts`, `console/test/unit/wsl.test.ts`, and
`console/test/unit/wslstub.ts` — named because the wrapper is the fake's (T-A1 step 7): it runs the
`sh -c` script the console writes, so the pid inside the distribution is a live process the test can
find and signal, and the second call this task asserts is one more invocation the report carries.
Red step: through the fake — `abandon` sends `SIGTERM` to the `wsl.exe` child and, when the fake
reports that the child is still alive after `SHUTDOWN_GRACE_MS` (`spawn.ts:298`), issues **one** kill
inside the distribution naming the pid the wrapper recorded; the fake's report shows the pid it was
given is the child's, not the wrapper's; and a cancellation where the child exited on its own sends
**no** second call. All three fail today.
The pid is captured by the wrapper, not guessed: the fake writes it where the real one must
(`wsl.exe -d <d> -- sh -c 'echo $$ > <file>; exec fugaro …'`), and a design that cannot capture it is
D6(c) with its residual **stated on the screen** rather than in a comment. U5 and U6 are the two facts
this task cannot establish from Linux, and the plan's answer is to make the *fallback* honest rather
than to assume the primary works.
`wsl.exe --terminate` is refused (D6(b)): a console that can terminate a distribution can destroy work
it does not own, including the operator's own shells.
Verification: `[unit]`, `[types]`. Tier A for the shape; Tier B to accept (U5, U6). Depends on: T-C5,
T-D2.
Commit: `console: cancel inside the distribution, and state the residual when we cannot`

**T-F5 — the deadline is the backstop, and it is measured through the wrapper (F25).**
Files: `console/test/unit/spawn.test.ts`.
Red step: a fake that holds its stdout open after the child exits — the case `runToCompletion`'s
`close`-not-`exit` rule (`spawn.ts:1274`) exists for — must still settle, via `READ_TIMEOUT_MS`
(`spawn.ts:310`). Assert it settles at the deadline and not before; plant the bug by settling on
`exit` (`spawn.ts:1271`) and watch a truncated read pass, which is the failure the rule prevents.
The point for this plan: the wrapper adds two more ways for the pipe to stay open (the WSL relay and
the distro's session), so the backstop becomes load-bearing rather than theoretical — and U15 asks
whether 90 s is the right budget for a cold-starting distribution.
Verification: `[unit]`. Tier A. Depends on: T-C5.
Commit: `console: the read deadline is the backstop through the wrapper too`

---

## Part G — The first-run pages and the distro picker

*Goal: when it cannot run, the console says which of the four things is wrong and what to do about
it — in the existing no-binary pattern, in the existing styling, with no new vocabulary.*

**T-G1 — the four states (F30, F31, F26).**
Files: `console/src/source/cli.ts`, `console/src/renderer/screens/degraded.ts`,
`console/test/unit/degraded.test.ts`, `console/test/unit/bound-source.test.ts`.
Red step: `degraded.test.ts` gains one case per state — `no-wsl`, `no-distro`, `no-fugaro-in-distro`,
`no-binary` — each asserting the headline, the reason and the remedy, and that **no sidecar is
started** for any of them (the `NoInstallation` refusal path, `cli.ts:203`). All four fail today.
Each state is *stated* in the renderer rather than imported from `src/source/cli.ts`, for the reason
`degraded.ts:37`'s comment gives: the renderer may not import a module that spawns. The existing
no-binary page's shape is the template, including the path-redaction rule — the page shows the
**count** of what was searched and not the paths (F30).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-C5, T-F4.
Commit: `console: four honest first-run states, in the no-binary page's own pattern`

**T-G2 — the install line is per route (F32, D9(a)).**
Files: `console/src/source/cli.ts`, `console/src/renderer/screens/degraded.ts`,
`console/test/unit/degraded.test.ts`, `console/test/unit/screen-literals.test.ts`.
Red step: a test that the WSL route's state carries a remedy that **names**
[docs/windows-cli/wsl.md](../windows-cli/wsl.md) and does **not** carry
`brew install dimipaun/tap/fugaro` (`cli.ts:173`), and that the macOS state still carries it. Fails
today, because the constant is one string shown everywhere.
The remedy **points at** the procedure rather than repeating it: a second copy of a four-command
procedure in a `<code>` block is a copy that goes stale silently, which is the repository's own rule
about duplicated facts (D9(b)).
`screen-literals.test.ts` is the guard that every string a screen writes is the console's own word —
the new remedies are module constants in `degraded.ts`, which is the shape that guard accepts.
Verification: `[unit]`, `[types]`, `[pins]`. Tier A. Depends on: T-G1.
Commit: `console: the install line follows the route, and points at the procedure rather than copying it`

**T-G3 — the distro picker (F10, F11, C2).**
Files: `console/src/main/picker.ts`, `console/src/preload/installations.ts`,
`console/src/renderer/screens/degraded.ts`, `console/test/unit/installations-ipc.test.ts`,
`console/test/unit/preload-surface.test.ts`, `console/test/unit/degraded.test.ts`.
Red step: the IPC test gains one channel — the distro list, requested by the renderer and answered by
the main process from `wslDistributions()` — and the preload-surface test gains it in the allow-list.
Both fail today.
This is the one genuinely new **window-side** surface in this work: it is a list, not a file dialog, so
it reuses `PICKER_WINDOW_SIZE`'s window shape and none of `PICKER_FILE_FILTERS` (F10). The list is
read-only and contains distro names, which are the console's to show (they came from `wsl.exe`, so
they are a document's strings and go through `putText`, not `literal`).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-C4, T-G1.
Commit: `console: a distro picker, in the picker's own window and the IPC's own allow-list`

**T-G4 — the styling's table gains the four states (F30).**
Files: `console/src/renderer/styles/*.css` (whichever file owns the degraded screens),
`console/test/unit/theme-selectors.test.ts`.
Red step: `theme-selectors.test.ts` pins the selectors the screens use; add the four states' selectors
first — they fail because nothing draws them — then draw them. The one table of every state variant is
[docs/design/console-styling.md](../design/console-styling.md) §2.7–§2.9, and the rule that survives
the edit is §3.5's: **never colour alone**.
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-G1.
Commit: `console: the four states' selectors, in the theme's own table`

**T-G5 — the environment view says which side the credentials are on (F21).**
Files: `console/src/renderer/screens/` (the environment view), `console/test/unit/degraded.test.ts` or
the environment view's own test file.
Red step: a test that with a WSL installation the environment view states that the CLI's credentials
are the **distribution's** — the console's own words, no document string through `literal` — and that
the console reads no token in either case.
Why this is a page and not a doc: the operator who has logged in on Windows and not inside the
distribution gets `fugaro doctor`'s "not authenticated", and the sentence that explains it is the
difference between a five-minute fix and an afternoon (F21, `docs/windows-cli/wsl.md`'s second trap).
Verification: `[unit]`, `[types]`. Tier A. Depends on: T-G1.
Commit: `console: say which side of the boundary the credentials are on`

---

## Part H — The release-time pack job, the release text and the staging

*Goal: the Windows artifact is packed by a runner **at release time and nowhere else**, described
honestly in the release, and gated by nothing until the owner says otherwise.*

**T-H1 — stage `docs/pending-workflows/console-windows.yml` and its copy-in (F40, F44, D7(c)).**
Files: `docs/pending-workflows/console-windows.yml` (new), `docs/pending-workflows/README.md`.
Red step: the Go guards over the staging directory — `TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere`
(`internal/cli/docs_desktop_console_test.go:6345`), its `pendingWorkflows(t)` (6455) and the count-and-name
sentences it reads (`consolePendingSentence`, 6500) — fail the moment the file exists and the README's
opening line does not name it, and `TestStagedLinuxFilesSurviveTheCopyIn` (6665) fails if the copy-in is
wrong. Run `GOFLAGS=-p=2 go test ./internal/cli -run 'TestConsoleLinuxStaging|TestStagedLinuxFilesSurviveTheCopyIn' -count=1`
and watch it fail **before** the README edit. The four guards this task lands
(`internal/cli/docs_windows_console_test.go`; a fifth, the release checklist's, is T-H3's) are written
first and fail until the file and the prose exist: `TestWindowsConsoleWorkflowIsReleaseTimeOnly` (the
triggers, the pins, the artifact), `TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere` (the
README's section and the site's sentence), `TestDocsWindowsConsoleReconciliationRecordsTheOwnerAnswers`
and `TestDocsWindowsConsolePlanIsReleaseTimeOnly` (the reconciliation's answer record and this plan's
own re-cut). Each is mutation-proven by the edit named in its own comment.
The file itself is `console-linux.yml` with **four differences D7(c) and the runner force**:
`runs-on: windows-latest`, the pack step is `npm run package:win`, the artifact assertion counts
exactly one `*.exe`, and — the D7(c) difference — the triggers are
`on: push: tags: ["v[0-9]+.[0-9]+.[0-9]+"]` and `workflow_dispatch`, **with no `pull_request` trigger
and no `paths:` filter**. It carries **five more, because this job's second subject is the console's
suite on Windows and a Windows runner's default shell is PowerShell**: `defaults.run.shell: bash`
(named once rather than rewriting four POSIX steps in PowerShell), `timeout-minutes: 45` against the
Linux job's 30, a `typecheck` step, a `run the unit suite` step — the two that make this the only
place the console's suite ever runs on Windows — and the artifact's own name with a
`retention-days: 90` against the Linux job's 7. Anything else that differs between the two files is
a comment, and the two are meant to be comparable by eye. `permissions: contents: read`, actions pinned
by 40-hex SHA, `--publish never` in the script rather than the line, no secrets, and the header's first
paragraph says **in those words** that it must not be copied in until Parts B and C have landed, because
before that the packer refuses and the suite is red — and that it is a release-time job, so nothing on a
pull request ever runs it.
The file uploads the installer as its own build artifact and **does not attach it to any release**: the
release's assets are unchanged, and whether the installer joins `console-vX.Y.Z` is a decision D7 does
not take (its `publish` job would have to fetch this workflow's artifact, a mechanism the console
release does not have). The staging README says so in the same words, because it is the one thing a
reader will otherwise assume.
The four consistency edits this forces are one commit, not four, because any three of them without the
fourth leaves `main` red: `docs/pending-workflows/README.md`'s opening line, `website/README.md`'s
"now holds N pending workflows" sentence, `website/test/readme.mjs`'s words map and its regex, and the
two fenced replacement-sentence blocks under `## console-release-linux.yml` — which must become the
**post-Linux-copy-in** state, naming the two files still staged, because
`TestStagedLinuxFilesSurviveTheCopyIn` runs the suite in a copy where the Linux files are applied.
Verification: `[pins]`, `[site]`, `[vet]`. Tier A to write; **Tier C to copy in**. Depends on: T-B1,
T-B2, T-C5.
Commit: `docs(pending-workflows): stage console-windows.yml, unsigned and outside the release gate`

**T-H2 — the release-time job's status is written down (F41, D8(a)).**
Files: `docs/pending-workflows/console-windows.yml` (its header), `docs/pending-workflows/README.md`.
Red step: `scripts/release-gate.sh`'s `names=(...)` and
`internal/cli/docs_desktop_console_test.go`'s `deriveConsoleCheckNames` (4315) are the guard: a test
asserting that the Windows job's check name is **not** in the gate's list, and that
`TestReleaseGateRequiresTheConsoleCheck` (4429) still passes. Write it first; it fails while the header
claims otherwise or the file is absent.
The reason in one sentence, in the file: the gate's list is derived from `console-tests.yml`'s **single**
job and the derivation refuses a multi-job workflow, so adding the Windows leg to the gate is three
edits in one commit that change which checks gate **every** tag on this repository. The file also says,
in the same header, that the job does not run on a pull request at all (D7(c)) — so the two rulings
that keep it out of the gate and off the pull requests are read together, where a reader will find
them.
Verification: `[pins]`, `[vet]`. Tier A. Depends on: T-H1.
Commit: `docs(pending-workflows): say why the Windows pack does not gate the release`

**T-H3 — the release text (F39, F43, D10(a)).**
Files: `docs/release.md`, `console/README.md`, `docs/design/desktop-console.md`,
`internal/cli/docs_desktop_console_test.go` (the guards that pin those sections).
Red step: `[pins]` — the guards that pin `docs/release.md`'s console paragraphs and
`console/README.md`'s Linux section fail the moment the sentences move, and the edit and the guard move
together. The one this work adds for it is `TestDocsWindowsConsoleReleaseDocsCarryTheDryRun`
(`internal/cli/docs_windows_console_test.go`), which fails until the checklist carries the dry run and
the console paragraph says the installer is not a release asset.
**Edits 1 and 2 below are already written, by the staging pull request of 2026-10-20**: the owner
asked for the by-hand dry run to exist before the pack does, so `docs/release.md`'s console
paragraph and its "Before you tag" checklist carry the release-time note, the unsigned sentence, the
not-an-asset sentence and the dry run now. T-H3 therefore **completes 3 and 4 and re-reads 1 and 2**
rather than rewriting them — and it is why the paragraph says the build is *planned and staged*
rather than shipped: the ruling is a plan, nothing on Windows has been built, and
`docs/design/desktop-console.md` and `console/README.md` still carry the older, narrower sentence
until this task lands (O7).

Four edits, each a **dated note rather than a rewrite** (O7, F43):
1. `docs/release.md`'s console paragraphs gain the Windows sentence: the pack runs **at release time
   only** (a tag or a manual dispatch, **never on a pull request**), it produces an **unsigned**
   NSIS installer, and **the Windows installer is not an asset of `console-vX.Y.Z`** — the release's
   asset list is unchanged until the owner decides otherwise (D7). The "Before you tag" checklist gains
   the by-hand dry run: open Actions → `console-windows` → **Run workflow** on `main` before the tag,
   because nothing else exercises the pack between releases.
2. `docs/release.md` says the Windows installer is **unsigned** and says what SmartScreen will show:
   *"the Windows installer is unsigned: Windows SmartScreen will warn that the publisher is unknown,
   and More info → Run anyway is the way past it"* (U18 — the exact wording is checked against the
   first real download).
3. `console/README.md`'s Linux section gains the Windows section, opening with what the pack produces
   and that it is unsigned.
4. `docs/design/desktop-console.md`'s §4.2, §7.6 rule 3, the owner-run table's Windows-signing row and
   D10's row each gain a dated note saying **which half of "Windows stays out" moved**: the *build* is
   in 0.10.0 by the 2026-10-20 ruling; the *signing* answer is untouched and there is still no
   Authenticode identity.
**`docs/windows-cli/wsl.md`'s F40 line and its console table row are edited in the same commit** — its
console row says the console needs a Windows CLI first, and this work is exactly the reach that removes
that need for the console (O1, F43). The CLI track's F40 stays true of the CLI.
`website/data/install.yaml` is **not touched** (F47).
Verification: `[pins]`, `[site]`. Tier A to write; **Tier C** for the two claims about signing.
Depends on: T-H1.
Commit: `docs: the Windows console in the release, unsigned, and the three notes that say which half moved`

**T-H4 — the pack job's own document, and the mutation-proof discipline.**
Files: `console/README.md`, `docs/pending-workflows/README.md`, this file's §4.
Red step: `[pins]` — the guard that every file staged under `docs/pending-workflows/` is described by a
section of that README, by name.
This is the `Rule` task that keeps the staging convention intact for the *next* file: the copy-in
commands, the STAGED/NOT APPLIED block, and the reason this workflow is its own file rather than a
third matrix leg (D8(c) is refused, and the reason is written where the next person will look).
Verification: `[pins]`. Tier A. Depends on: T-H1, T-H3.
Commit: `docs(pending-workflows): the Windows file's section, its copy-in and why it is its own workflow`

---

## 4. The owner's manual checklist — Check 119 (proposed, not claimed)

**What this is.** The one part of this work no runner can do: a person, a real Windows machine, and an
hour. It is written as a checklist because that is what the register's checks are, and it is
**proposed** as **Check 119** and **claimed by nobody** (D12(a)). The register's written sections run
to 89; 90–99, 100–109, 110, 111, 112 and 113–118 are taken or named; 119 is the next free number after
reading the register name by name. **Nothing writes the row into
[docs/gcp-live-checklist.md](../gcp-live-checklist.md)**: the register's rule is that the phase which
ships the check writes it, and no phase here is approved.

**Prerequisites.** A Windows 10 or 11 machine with WSL 2; one distribution with a Linux `fugaro`
installed by [docs/windows-cli/wsl.md](../windows-cli/wsl.md)'s procedure; a checkout of this
repository **inside** the distribution (`~/src/fugaro`, not `/mnt/c/...` — the mode-bit trap that file
records); **the installer, from a manual dispatch of `console-windows.yml`** — a dispatch, because under
D7(c) the job runs at release time only and a pull request never produces one, which is also why the
release checklist carries the by-hand dry run; and **no** signing certificate.

Each step says what to do, what must be true, and which finding or U-row it settles.

| # | Do this | It passes when | Settles |
|---|---|---|---|
| 1 | Run the installer. | It completes; Windows shows the SmartScreen warning and **More info → Run anyway** gets past it; the publisher reads as unknown. | U18 |
| 2 | Launch the console from the Start menu. | The window opens, the theme is the one macOS draws, and no console error appears. | U13 |
| 3 | With one distribution installed and a `fugaro` in it: open the console. | The header shows the resolved **Linux** path (`/home/<you>/.local/bin/fugaro`), not a Windows path and not a UNC path. | F16, D3(a) |
| 4 | Open the first-run page with WSL **not** installed (a machine or a state without it). | The page says WSL is missing, in the console's own words, and the remedy points at the WSL install. No dashboard is drawn around it. | F30, F31 |
| 5 | With WSL installed and **no** distribution: open the console. | The page says there is no distribution, and offers the distro picker rather than a file dialog. | C2, F31 |
| 6 | With a distribution that has **no** `fugaro`: open the console. | The page says the distribution has no `fugaro`, and the remedy names the WSL install procedure. | C1, F31, F32 |
| 7 | With two distributions, one holding `fugaro`: pick the one that does not. | The failure is stated and the console does **not** silently switch to the other distribution's CLI. | F7, T-C5 |
| 8 | Pick the distribution that does hold `fugaro`. | A read succeeds and the numbers are the ones `fugaro` prints in a terminal inside that distribution. | U7, U14 |
| 9 | Start a `watch`, then cancel it. | The feed stops; **and** `ps aux \| grep fugaro` inside the distribution shows no leftover process within the grace period. | F24, U5, U6 |
| 10 | Start a `watch`, then kill the distribution (`wsl --shutdown`) while it runs. | The screen says the distribution went away, which is a different sentence from the CLI dying. | F26, F29 |
| 11 | Open a project with a remembered checkout under the distribution's home. | The path shown is the Linux one and it is the same directory the CLI read; a reveal-in-Explorer affordance opens the UNC path. | F16, U10 |
| 12 | Run the environment view (`doctor`). | It says the credentials are the **distribution's**; with no login inside the distribution it says so, and does not claim the console has a credential. | F21 |
| 13 | Check the console's store on the host. | `%APPDATA%\Fugaro Console\installations.json` exists, holds the WSL installation, and **nothing** was written inside the distribution. | F14, T-D5 |
| 14 | Open Task Manager while a long read runs, and look at the command lines. | The `wsl.exe` command line carries a distro name, a directory and CLI flags — **no token, no key, no environment dump**. | F19, F22, U16 |
| 15 | Time the first invocation after `wsl --shutdown`. | It completes inside the console's own read deadline, or the screen says it timed out and names the distribution. | U15 |
| 16 | Uninstall the console from Settings → Apps. | It uninstalls cleanly and leaves `%APPDATA%\Fugaro Console` alone (the store survives a reinstall). | D7(c) |

**Not in this checklist, deliberately:** anything about a Windows-native `fugaro` (there is none, and
[docs/windows-cli/](../windows-cli/README.md) owns that track); anything about signing or notarisation
(there is no Authenticode identity and this plan buys none — D10(c) is the owner's and is not
proposed here); and anything about a Windows **release** (D8(a) keeps the job out of the gate, so a
red Windows pack blocks nothing — and under D7(c) a red pack is found at release time rather than
before it, which is the risk the ruling accepts and the dry run above is there to reduce).

---

## 5. OWNER APPROVAL GATE

**Passed on 2026-10-22: approved to build — target release 0.11.0, pull requests target `main`.**
The owner approved **this plan**: the eight parts, the order in §3, the staged workflow in
`docs/pending-workflows/console-windows.yml`, the owner-run checklist in §4, and the risk the D7
ruling accepts (a packaging break shows at release time and is fixed then). Parts land in §3's order,
each merging alone into main, for 0.11.0.

**Answering a decision was not approving the build, and the two dates are separate.** The five
conditions this gate used to list were the twelve decisions, and **the owner answered all twelve on
2026-10-20** — each with its one-line why in [reconciliation.md](reconciliation.md) §4. That removed
the *decisions* from the gate; it did not pass it. The approval of the plan itself is the ruling of
**2026-10-22**, and it is what this section now records.

| Decision | Answered | What it settled |
|---|---|---|
| **D1** | (a) | The shape of T-C1, and therefore every task in Parts C–G: a named wrapper on `SpawnPlan` (`command` + `prefix`), so the spawn log's `argv` line stays the operator's record of what ran. |
| **D2** | (a) | T-C4, T-C5 and T-D1: a named distribution in the store, defaulted from `wsl.exe --status`, with no default at all when that command names none. |
| **D3** | (a) | T-D3 and half of T-G1: the Linux spelling wherever the console states or passes a path, the UNC spelling only where Windows itself opens one. |
| **D4** | (a) | The translation tasks in Part D: one `wslpath -a -u` call per distinct path, through the same spawner and the same deadline, cached for the life of the window. |
| **D5** | (a) | Part E's shape: the existing `childEnvironment` allow-list stays the enforcement, `WSLENV` is set to the empty string, and the child-report test proves it through the wrapper. |
| **D6** | (a) | T-F4 is **scheduled** rather than written as an honest residual: the Linux pid is captured and killed inside the distribution, with the residual window stated. |
| **D7** | (c) | Part H, and the whole shape of the pack: **release time only** — the tag or a manual dispatch, **never on a pull request**, with the accepted risk and the by-hand dry run written down. |
| **D8** | (a) | The gate's `names` list is untouched, and no job is added to `console-tests.yml`: a red Windows pack blocks no release. |
| **D9** | (a) | T-G1's remedy text: it points at [docs/windows-cli/wsl.md](../windows-cli/wsl.md) rather than carrying a second copy of the procedure. |
| **D10** | (a) | T-H3: one SmartScreen sentence in the release notes, `docs/release.md` and the download page, and no claim anywhere that the installer is signed. |
| **D11** | (a) | Part A, and therefore the test strategy of every other part: a fake `wsl.exe` built on `installStub`, asserting what the Linux side received. |
| **D12** | (a) | §4 stays *proposed*: Check 119 is named and unclaimed, and no row is written into the register. |

**What the approval covers, and in what order.** **Part A** — built, and it touches no production
code, no `console/src/` file and no document outside this folder, its only claim being that the tests
that follow can run anywhere — and then the parts in §3's order: B independently of A, C after A, D,
E and F after C in any order among themselves, G after C and F, and H after B and C. Parts B–H never
waited on answers — theirs are above — they waited on the gate, and now on the order in §3.

**What the approval does *not* cover, and what still needs the owner**: copying
`docs/pending-workflows/console-windows.yml` into `.github/workflows/` (that is Tier C, and the file's
own header says what must land first); writing Check 119's row into the register (D12); touching
`website/data/install.yaml` (F47); and anything under `.github/workflows/` by an agent (F44).
