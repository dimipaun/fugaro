# The Fugaro desktop console: implementation plan

**Goal:** build the console the owner's spec asks for in its section 6 — a cross-platform Electron application that is a richer, graphical `fugaro watch`: live task monitoring, spending monitoring with graphs, and project-level views consistent with the project-centric model — **without a new backend, without the app ever holding a credential, and without the app ever writing anything**. The reconciled design is [docs/design/desktop-console.md](../design/desktop-console.md). **No code in this plan has been written or run; this is a design-to-code plan.** **This PR (the one that adds this plan) is docs-only: it touches exactly two files — this plan and the design — and nothing else at all**, not `docs/gcp-live-checklist.md` (the check number this plan reserves, 59, is *named* here and its row and section are written by Task 4.3 when the packaging phase ships), not `go.mod`, not any Go, Terraform or product-behavior file under the "Files" lists below. Tasks 0.1 onward are future work, gated on the owner approving the design and this plan.

**Status: decisions answered; implementation awaits the owner's explicit go on the plan.** The owner answered the design's D1 to D20 on 2026-10-15 — the design's §10 carries them in its last column: **D1** the CLI sidecar behind `FugaroSource` (direct database access never), **D3** this repository under `console/`, **D4** direct download plus a Homebrew cask, signed, notarised and stapled, with Apple signing entering this repo's release path tag-only behind the release gate and a protected environment (the owner holds the certificate and runs every signing step; the Mac App Store is out), **D11** its own artifact published from the CLI's `vX.Y.Z` tag with a declared supported CLI range — and the other sixteen (D2, D5 to D10, D12 to D20) **accepted as recommended**, none objected to. **Answering a decision is not approving the tasks**: no task below starts until the owner gives the explicit go the OWNER APPROVAL GATE at the end of this file describes.

**Spec:** [docs/design/desktop-console.md](../design/desktop-console.md), written from the owner's verbatim draft [project-centric-spec-source.md](../design/project-centric-spec-source.md) §6 ("Forward-looking: Fugaro desktop console") and from `docs/design/project-centric.md` §9, which says of this work, correctly, "**No work in the plan.**" The design's decisions are **D1 to D20** (its §10), each with a recommendation and now with the owner's answer of 2026-10-15 beside it; this plan defers to them and adds its own **C1 onward**, each independently vetoable before its phase starts. Task 5.0's table mirrors the answers, and the OWNER APPROVAL GATE at the end says what they do and do not authorise. **Successor, 2026-10-15: the owner's second console spec** ([desktop-console-spec-v2.md](../design/desktop-console-spec-v2.md), verbatim) is reconciled in [desktop-console-v2.md](../design/desktop-console-v2.md), and its later phases are [2026-10-15-desktop-console-v2.md](2026-10-15-desktop-console-v2.md). **Nothing in v2 is approved or built, and nothing in this plan changes**: phases 0–5, the check-number register (58 for #393, 59 for this plan's 59a/59b) and the gate below stand exactly as written.

**Architecture — what the six phases build, in one paragraph.** Phase 0 pins the ground: it reads every `--json` document the console will consume and writes the contract down (`console/docs/json-contract.md`), establishes what `fugaro watch --json` guarantees to a long-lived child process, and puts the Go boundary in place so that a console under `console/` can never be imported by, or import, the Go module. Phase 1 builds the shell: a hardened Electron main process (contextIsolation, sandbox, no nodeIntegration, `connect-src 'none'`), a preload that exposes a frozen list of named operations rather than a bridge, and the one interface every screen talks to — `FugaroSource`, implemented in v1 by spawning the installed `fugaro` and reading its JSON, and implemented in tests by replaying recorded fixtures. Phase 2 draws the four screens: Live, Spend, Projects and Run detail. Phase 3 makes the console honest when things are missing — the availability states and gaps the design takes from tmog.org, the no-Firebase and CLI-absent modes, and the version-skew handshake that stops a console from misreporting spend it did not understand. Phase 4 packages it for macOS: the builder configuration and entitlements (agent-written), the signing and notarisation pipeline (staged under `docs/pending-workflows/` and applied by the owner), the auto-update feed, and **Check 59**, the owner's first signed-and-stapled build and its round trip. Phase 5 is the part this plan explicitly does not schedule: Windows and Linux packaging, and the three write actions of v1.1, each gated on an owner decision. **Nothing in phase *n* is required by phase *n-1*'s merge**: each phase is a working artifact, and the last section lists every cross-task dependency so a reviewer can check that claim.

**Builds on merged code (what each phase stands on).** Verified on `origin/main` at **`b9d5c5d`**, which is also this plan's writing baseline (the branch this was written on was identical to it, working tree clean):

| Phase | Merged work it builds on |
|---|---|
| 0 | `internal/watch/json.go` (`JSONDoc`, `JSONTotal`, `JSONRepo`, `JSONRun`, `JSONKill`, `JSONBurn`, `JSONConn`, `JSONFinished`, `BuildJSON`, `FilterRepo`), `internal/cli/watch.go` (`newWatchCmd`, the `--json`/`--plain`/`--once`/`--interval`/`--repo` flags), `internal/cli/{budget,report,runs,diagnose,logs}.go`, `internal/cli/json_everywhere_test.go`, the `docs_*_test.go` conventions |
| 1 | `internal/cli/cloud.go` (`newBudgetClient`, `budgetScopes`), `internal/cli/watch_queued.go` (`openQueueBucket`), `internal/updatecheck/updatecheck.go`, the Node pinning precedent `internal/budget/rules/emulator/package.json` |
| 2 | `internal/watch/state.go` (the pure model and its constants), `internal/watch/render.go` (`Render`, the width tiers), `internal/report/report.go` (`Build`), `deploy/terraform/gcp/modules/firebase/iam.tf` |
| 3 | `internal/watch/source.go` (the degradation phases), `internal/cli/runs.go` (`lsDoc`), `docs/design/watch-queued.md` §6 |
| 4 | `docs/release.md` (one tag releases everything; `scripts/release-gate.sh`; the cask), `.goreleaser.yaml`, `.github/workflows/{ci,images,release}.yml`, `docs/pending-workflows/README.md`, `.github/dependabot.yml` |
| 5 | everything above |
| 6 (styling, **APPROVED by the owner on 2026-10-19**) | `console/src/main/protocol.ts` (`SERVED_FILES`, `mediaTypeFor`), `console/tools/bundle-renderer/index.ts`, `console/src/renderer/{index,installations,refusal}.html`, `console/src/renderer/entry.ts` (`CONSOLE_THEME`, the nav), the four screens' class and `data-*` vocabulary, `console/test/unit/{bundle,csp-document,build-files,screen-literals}.test.ts`, `website/src/styles/{tokens,palette-logo}.css` |

**Releases.** The console is **its own artifact published from the CLI's own `vX.Y.Z` tag** (design D11): one release train, one gate (`scripts/release-gate.sh`), one Highlights section — which is what `docs/release.md`'s "one tag releases everything" means once a second deliverable exists. Its first shippable version is whatever release carries phase 4. **This changes one thing in this repo's release path and the plan says it plainly: until now every signature here has been keyless cosign over GitHub OIDC, and the console's Apple signing would be the first private key the repo has ever held** (`docs/release.md`: the checksums are signed, the binaries are not Apple-signed, and the Homebrew cask papers over Gatekeeper with an `xattr -dr com.apple.quarantine` postflight hook). Every step that touches that key is owner-run (Tasks 4.3 and 4.5 and Check 59), and no agent-run step in this plan or any other produces, stores, reads or transmits it.

## Phase summary

| Phase | What ships | Owner-run steps | Depends on |
|---|---|---|---|
| **0. The ground** | the JSON contract written down and pinned (`console/docs/json-contract.md`); the child-process contract settled; the Go/`console` boundary with three tests holding it (imports, `go:embed`, and the one-entry read exception) | nothing | nothing |
| **1. The shell** | the hardened Electron main process, the frozen IPC allow-list, `FugaroSource` with its `cli` and `fixture` implementations, the recorder/redactor, and **the console's CI — which begins with Task 1.5's copy-in, so Tasks 1.0 to 1.4 have none** | **the eight fixture recordings** (Task 1.4) and **the copy-in of the staged tests workflow** (Task 1.5) | phase 0 |
| **2. The screens** | Live, Spend, Projects, Run detail — with the availability states and the replay buffer | nothing | phase 1 |
| **3. When things are missing** | the no-Firebase and CLI-absent modes, the stale/gap rendering, the version-skew handshake (Task 3.2) **and its wiring at startup (Task 3.3)**; **the renderer entry — the preload, the handlers and the installation picker (Task 3.4, approved by the owner 2026-10-18; 3.4a implemented)** | nothing required (3.4's header and picker goldens are optional and owner-run) | phase 2 |
| **4. macOS packaging** | the builder config and entitlements; the staged signing/notarisation workflow (a file of its own, tag-only); the update feed; **Checks 59a and 59b** | **all of it**: the certificate, the API key, the first signed build, the update round trip (Check 59); the protected GitHub environment the signing secrets live in; and Task 4.5's copy-in of the staged **signing** workflow | phase 3 |
| **5. Later, unscheduled** | Windows and Linux packaging; the v1.1 write actions | each, when the owner decides | phase 4 |
| **6. The console's style — APPROVED by the owner on 2026-10-19** | the token layer, the stylesheet's wiring into the served bundle, the layout and the screens' rules, the window background and the keyboard (*Task 6.5 as the ruling left it; the theme switch it was named for is removed — Q1, dark only*), and the golden frames re-recorded (`docs/design/console-styling.md` is the design) | **the eight golden baselines re-recorded** (Task 6.6), after the stylesheet lands | phase 3 (Task 3.4a's bundler and served root); **approved by the owner on 2026-10-19** — **Q1 is answered: dark only, no light theme ships**, and Q2 to Q6 stand as the design's §8 writes them (*as first written, "**not approved** — see its six open questions"*) |

**Nothing in this table depends on a later row.** Phase 5 is deliberately empty of work: it lists the two things a reader will otherwise wonder about, and each is gated on a decision this plan does not presume.

## Check numbers this plan takes (59)

`docs/gcp-live-checklist.md` on `main` (`b9d5c5d`) has its highest written `## Check N` section at **47**, and its summary table's highest rows at **47** and **57**. The numbers above 47 are spoken for:

| Number | Claimed by | State |
|---|---|---|
| 42 | config-transitions design §6 and plan Task 8, merged as #344 | reserved row on `main`; written by that plan's own task |
| 43 (43a-43f) | registry remote repository, merged as #353 | written on `main`, not run |
| 44 | provider price catalog, merged as #354 | written on `main`, not run |
| 45, 46 | senior reviewer through a provider route, **PR #359, merged** (`2be38e1`) | **written on `main`, not run** (`## Check 45`, `## Check 46`) |
| 47 | OAuth reviewer stage, merged as #363 | written on `main`, not run |
| **48-56** | **`docs/plans/2026-10-14-project-centric.md`**, §"Check numbers this plan takes (48 to 56)" | **reserved in that plan**, one number per phase, each written by its own phase's task |
| 57 | the compute-instance Docker backend, **PR #375, merged** (`21abc49`) | **the summary row is on `main`; the section is not** — it lives in `docs/design/compute-backend.md` §7.2, not in the checklist |
| **58** | **PR #393**, which is open against `main` and **merges before this PR does** | **reserved in #393**; `main` does not carry it yet, and **#393 does not use 59** |
| **59**, written as **59a** and **59b** (the 43a-43f sub-lettering this checklist already uses) | **this plan, phase 4**: **59a** the console's first signed, notarised and stapled macOS build, its Gatekeeper verification and the spawn-log safety assertion; **59b** its auto-update round trip, which needs a published feed and therefore follows the first tag | **reserved here; this PR takes 59, not 58** |

**How 59 was chosen, and the rule that stops two open PRs from colliding, stated so a later reader can re-check both.** "The next free number" cannot be read off `main`'s section headings alone, because a number is taken three different ways — a written section, a written summary row whose section lives in another document, and a reservation in a PR or plan whose phase has not shipped — and because **two open PRs can both correctly believe they take the same next free number**. What settles it is which one lands first; a number is chosen at the moment of merge, not at the moment of writing. So, in this order, all of it on `origin/main` at `b9d5c5d`:

1. **Every `## Check N` section written in `docs/gcp-live-checklist.md` runs 1 to 47**, consecutively through 43a-43f: 45 and 46 are there (added by `2be38e1`, "plan: a senior reviewer through a provider route, non-Claude now and Claude by opt-in (#359)"), and so is 47 — **#359 and #363 are merged, not open**, and an earlier revision of this table said otherwise.
2. **The summary table's last rows are 47 and 57.** Check 57's row is on `main` (added by `21abc49`, "design: a Docker-capable execution backend on Compute Engine VMs (generic-tool Task 28) (#375)", so **#375 is merged too**), but its *section* is not in the checklist: the row points at `docs/design/compute-backend.md` §7.2, which is where 57a and 57b are written. A number whose section lives in another document is still taken.
3. **`docs/plans/2026-10-14-project-centric.md` reserves 48 to 56** and says so in a table with a `State` column, writing each row with its own phase rather than up front — so those nine are spoken for even though `main` carries none of them yet.
4. **Two open PRs reserve the two numbers above 57.** **#393 takes 58 and merges first; #393 does not use 59**, which leaves **59** free for this plan. Neither number is on `main` yet, so greps for `Check 58`, `Check 59` and every higher number across `docs/` return nothing outside the reserving PR — which is exactly why a grep alone can never answer this question, and why the two reservations are recorded in this table.

So 1-57 are all accounted for — 1-47 written, 48-56 reserved in a plan, 57 written as a row with its section elsewhere — and 58 is #393's and **59 is this plan's**. **The rule, so neither PR has to guess at review time: whichever of the two merges second renumbers**, checking `main`'s sections and summary rows before it writes anything, because the loser's number is the one that was never free. If #393 is renumbered, abandoned or closed instead, this plan keeps 59 regardless: a number already written into a summary table and a task heading is expensive to move and free to leave alone.

**This plan takes one number, not a range**, because it has exactly one owner-run live step; **that number is written as two sections, 59a and 59b**, which is the 43a-43f convention rather than a second reservation. **Following the project-centric plan's convention, neither this PR nor any docs-only task here writes the row or the section**: Task 4.3 writes both when phase 4 ships, so exactly one writer owns the number and a rebase cannot produce two.

**The re-check this section invites is a `git log` away, and the plan says which commands, so a reader does not have to trust the prose:** `git show origin/main:docs/gcp-live-checklist.md | grep -n '^## Check 4[5-9]'` for step 1, `grep -n '^| 4[4-9] \|^| 5[0-9] ' docs/gcp-live-checklist.md` for step 2, a grep of `docs/plans/` and `docs/design/` for step 3, and the two open PRs' own headings for step 4. **An earlier revision of this table got steps 1 and 2 wrong** — it repeated the project-centric plan's table (written while #359 was still open) instead of reading `main`. The correction to 58 is recorded rather than quietly applied, and the same review that corrected it is what found that 58 was already taken by #393; the error was in the *reasoning*, which is the one thing this section exists to get right.

**Check 59 — both its sections, 59a and 59b — is owner-run, on the sandbox installation and the sandbox repository only, and never touches the `belong` project** (`belong` holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`: no agent touches it, ever). It is written with FACT lines, a restore step, and **Check 43's full guard, verbatim** — the three hard stops, `--project fugaro` on every `fugaro` command and `--project "${P:?}"` on every `gcloud` command, in a block re-run from a fresh shell before every step (`docs/gcp-live-checklist.md`, Check 43's guard block, and the rule the project-centric plan spells once and requires of every check 48 onward). That requirement is not optional for a check that looks local: the future `TestChecklistChecksAreGuarded` (project-centric plan Task 6.3) asserts that **every `## Check 48`-onward section carries Check 43's three hard stops and its project pinning, not merely a `:?` guard**, and Checks 59a and 59b are `## Check 59a` and `## Check 59b` sections. The console is *built* from this repository; every `fugaro` command it drives is run **with the sandbox checkout as the working directory**, under the guard.

## Global Constraints

These hold for every task below, and the plan's own review checks them:

1. **No new backend, ever.** No service account, no RTDB node, no Firestore collection, no rules change, no Cloud Run service, no Terraform resource. The console reads what `fugaro watch` and the budget dashboard already read, through the CLI. If a screen needs a field no `--json` emits, the fix is a CLI task in a CLI plan, not a console reader.
2. **No credential is ever read, held or stored by the app.** Not an OAuth token, not a service-account key, not a Firebase API key, not a copy of the operator's config. The app spawns `fugaro` and lets it resolve the operator's ambient identity exactly as a terminal does.
3. **The app never writes.** The spawn allow-list is read-only commands only, and the mutation-proof test of Task 1.2 is what keeps it that way: adding a write subcommand must fail a test, not pass a review.
4. **The renderer is untrusted by construction and gets no network.** `connect-src 'none'`, no remote content, no navigation, no permission, no `nodeIntegration`, no generic IPC.
5. **Every number carries an availability state.** Measured, notional, estimated, unavailable or a gap — never an implicit zero. This is the design's first UI rule (its §6.1) and it is a review criterion, not a nicety.
6. **`belong` is never touched** by any step, agent-run or owner-run, and no fixture is ever recorded from it.
7. **Nothing under `.github/workflows/` is created or edited by an agent.** The console's workflow text is written as **two staged files under `docs/pending-workflows/`** for the owner to copy — **`console-tests.yml`** (staged by Task 1.4, copied in by the owner as Task 1.5) and **`console-release.yml`** (the signing pipeline, staged by Task 4.2, copied in as Task 4.5) — which is the mechanism this repo already uses (`docs/pending-workflows/README.md`) and names as an owner step in `docs/release.md`'s "Before you tag". **The two are separate files because they must have different triggers**: the tests job runs on every pull request and every `main` push and holds no secret, the signing job runs only on a version tag and reads its secrets from a protected environment (design §9.4). One file carrying both would put the signing key's job on every pull-request run.
8. **The Go module is untouched.** `console/` holds no Go package, `go.mod` and `go.sum` do not change, and no Go file references a path under `console/`. Task 0.3's test is what makes this a fact rather than an intention.
9. **Security-sensitive tasks are flagged `SECURITY-SENSITIVE, independent opus review`** in their own heading and are not merged on a self-review. **This list is the headings, and nothing else is on it**: 1.1, 1.2, 1.3, 2.0, 3.3, 4.1, 4.2 and 4.4, plus **2.4 (added 2026-10-17)**, plus 3.4 (approved by the owner 2026-10-18; each of its three parts, 3.4a, 3.4b and 3.4c, carries the flag), **6.2 (added 2026-10-19 with the styling design; its subject is the CSP's own tests, the served-file list and the served documents)** and 5.2 when it is scheduled. (An earlier revision listed 2.1 and 4.3 and omitted 2.0, 4.4 and 4.5; the headings are the authority, and Task 2.1 is not flagged — it draws the states Task 2.0 defines rather than handling hostile text itself.) **A separate flag, `OWNER-RUN`, marks the tasks no agent performs at all** — 1.4's recordings, **Task 1.5's copy-in**, 4.3, and 4.5's copy-in — and the two flags are independent: a task may carry neither, either, or both.
10. **Docs-only tasks keep the docs suite green**: every task that touches a `.md` file under `docs/` runs `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, the command this repository's docs tests are reached by.

## Review Focus

What a reviewer of each phase should attack, in the design's own terms:

- **Phase 0**: is the contract *read* or *assumed*? Every field in `console/docs/json-contract.md` must be traceable to a struct field or a `Encode` call on `origin/main` at the time of writing. A field invented from the design doc rather than read from the code is a defect.
- **Phase 1**: the two hard questions — **can the renderer reach anything it should not** (a channel, a spawn, a file, a URL), and **can a job-written string become code** (an `innerHTML`, an unescaped attribute, a URL built from a title)? A reviewer should try to break both, not read for style.
- **Phase 2**: does any screen ever draw a number it did not measure? Look for a `0` that should be `n/a`, a line that interpolates across a gap, and a `NOTIONAL~` summed into a real dollar total.
- **Phase 3**: what happens at the boundary — the CLI is a version the console does not know, the CLI is absent, the stream stops. A crash is worse than a banner; a wrong number is worse than both.
- **Phase 4**: the supply chain, and the key. Exact pins, a committed lockfile, `npm ci --ignore-scripts`, no workflow edited by an agent, no key anywhere an agent-run step can see it.
- **Every phase**: the console adds no reader, the module does not grow, and `belong` is untouched.

## File Structure

Created by this plan (nothing here exists yet, and **none of it is created by this PR**):

```
console/
  package.json                    # private, exact pins, engines.node, no ^ or ~
  package-lock.json               # committed; npm ci --ignore-scripts in CI
  tsconfig.json
  README.md                       # how to run it, and what it may and may not do
  docs/json-contract.md           # Task 0.1: every --json document, field by field
  src/main/window.ts              # the BrowserWindow, its webPreferences, navigation denial
  src/main/ipc.ts                 # the IPC allow-list and each handler's argument schema
  src/main/spawn.ts               # the one child process, the read-only subcommand table
  src/main/spawnlog.ts            # Task 1.3: the opt-in JSONL spawn log of §5.1 rule 10 (D20)
  src/main/updates.ts             # Task 4.4: the update check and the signed feed
  src/preload/index.ts            # the frozen, versioned object the renderer sees
  src/preload/installations.ts    # Task 3.4b (approved by the owner 2026-10-18): the picker window's own frozen object
  src/main/installations.ts       # Task 3.4b (approved by the owner 2026-10-18): the installation store in userData, and its validation
  src/renderer/index.html         # the bundled document, the CSP meta as belt-and-braces
  src/renderer/app.ts
  src/renderer/entry.ts           # Task 3.4a (approved by the owner 2026-10-18, implemented): the script the dashboard document loads; startConsole first
  src/renderer/installations.{html,ts}  # Task 3.4b (approved by the owner 2026-10-18): the picker document
  src/renderer/screens/{live,spend,projects,run}.ts
  src/renderer/theme/tokens.css      # Task 6.1: the vocabulary - spacing, type, radius, motion; names no colour (the tree promised this file and no task created it)
  src/renderer/theme/palette.css     # Task 6.1: the value set - every colour token, once; the dark set alone (Q1 ruling 2026-10-19)
  src/renderer/theme/base.css        # Task 6.3: reset, document, the three bands, the nav, focus, responsive, forced colours
  src/renderer/theme/screens.css     # Task 6.4: the per-screen rules, in the order docs/design/console-styling.md §2 lists the screens
  src/source/source.ts            # the FugaroSource interface - the whole data plane
  src/source/cli.ts               # v1: spawn the installed fugaro
  src/source/fixture.ts           # tests: replay a recorded NDJSON feed
  src/shared/{schema,availability,sanitize,version}.ts
  test/unit/*.test.ts
  test/e2e/*.spec.ts              # Playwright + Electron
  test/e2e/__screenshots__/       # the golden frames
  test/fixtures/*.ndjson          # recorded and redacted; never from belong
  tools/record-fixture/{index,redact}.ts
  tools/bundle-renderer/index.ts  # Task 3.4a (approved by the owner 2026-10-18, implemented): the dependency-free renderer bundle step
```

Edited by this plan:

```
internal/cli/docs_desktop_console_test.go      # Task 0.1/2.4: the design's load-bearing strings and the JSON contract; Task 5.0: the owner's answers
internal/cli/docs_console_boundary_test.go     # Task 0.3: the Go/console boundary
docs/pending-workflows/console-tests.yml       # Task 1.4 stages it; the OWNER copies it in as Task 1.5
docs/pending-workflows/console-release.yml     # Task 4.2 stages it; the OWNER copies it in as Task 4.5
docs/pending-workflows/README.md               # Tasks 1.4, 1.5, 4.2, 4.5: a row per staged file, and its applying commit
scripts/release-gate.sh                        # Task 4.6 only: names=(test terraform rules "console (ubuntu-latest)" "console (macos-latest)")
.github/workflows/console-tests.yml            # Task 1.5 only, and the OWNER creates it: no agent touches it
.github/workflows/console-release.yml          # Task 4.5 only, and the OWNER creates it: no agent touches it
docs/gcp-live-checklist.md                     # Task 4.3 only: Check 59's rows and sections (59a, 59b)
docs/design/desktop-console.md                 # Task 5.0 only: §10's answers of 2026-10-15, and the record that 2026-10-16's do not reach it
```

## PR groups

Each row is one mergeable PR, green on its own:

| PR | Tasks | Flag |
|---|---|---|
| A | 0.1, 0.2, 0.3 | docs + Go tests only |
| B | 1.0, 1.1, 1.2 | **SECURITY-SENSITIVE** |
| C | 1.3, 1.4 | **SECURITY-SENSITIVE** (spawn and the redactor); **OWNER-RUN** for 1.4's recordings |
| C2 | **1.5** | **OWNER-RUN**: the owner copies the staged tests workflow in — no agent performs any step of it |
| D | 2.0 | **SECURITY-SENSITIVE** (the text path) |
| E | 2.1, 2.2, 2.3 | |
| E2 | 2.4 | **SECURITY-SENSITIVE** (edits the IPC allow-list's schema); added 2026-10-17 |
| F | 3.1, 3.2 | |
| F2 | **3.3** | **SECURITY-SENSITIVE** (the startup gate that decides whether a dashboard is drawn); **approved by the owner 2026-10-17**, see the task |
| G | 4.1, 4.2, 4.4 | **SECURITY-SENSITIVE** (supply chain) |
| H | 4.3, 4.5 | **OWNER-RUN**, no agent performs any step of either; 4.5 copies in the **signing** workflow |
| I | 4.6 | the release gate; **merges only after C2**, and refuses a tag until the console job runs |
| F3 | **3.4a**, **3.4b**, **3.4c** (three PRs) | **SECURITY-SENSITIVE** (the preload, the handlers and the installation store); **approved by the owner 2026-10-18** — 3.4a is implemented, 3.4b and 3.4c are not; merges after F2 and before the console MVP ships. Listed last only because it was added last |
| J | **6.1** | **APPROVED by the owner on 2026-10-19** (added 2026-10-19 with `docs/design/console-styling.md`); the token layer only — inert until J2 lands, and its PR says so. Implemented in this round |
| J2 | **6.2** | **SECURITY-SENSITIVE** (the CSP's tests, the protocol handler's served-file list and the served documents); **APPROVED by the owner on 2026-10-19** |
| J3 | **6.3**, **6.4** (two PRs) | **APPROVED by the owner on 2026-10-19**; ordinary stylesheet review |
| J4 | **6.5** | **APPROVED by the owner on 2026-10-19**; **6.3 and 6.5 are the two tasks that touch the renderer entry** — 6.3 appends the rail's elements, 6.5 adds the attributes and the keys |
| J5 | **6.6** | **APPROVED by the owner on 2026-10-19**; the eight baselines are **OWNER-RUN** (recorded by the owner, once per platform), the assertions are agent-written |

---

## Phase 0 — The ground

### Task 0.1: The JSON contract, pinned

**Files:** `console/docs/json-contract.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Read, on `origin/main`, every document the console will consume, and write down each one's real shape: `internal/watch/json.go` (`JSONDoc`, `JSONConn`, `JSONTotal`, `JSONBurn`, `JSONKill`, `JSONRepo`, `JSONRun`, `JSONFinished`, `BuildJSON`, `jsonBurn`, `jsonKill`, `jsonFinished`, `usdPtr`) for `watch --json`; `budgetShowDoc` for `budget show --json`; `internal/report/render.go`'s `JSONDoc`, `JSONLine` and `JSONTokens` for `report --json`, plus **`report.JSON` (`render.go:235`), which is the exported function that writes them** — an earlier revision said "there is no `report.JSON`", which is false about the symbol, and the correction matters because §12's contract is that every named symbol exists; `lsDoc` for `runs ls --json` (and, separately, `watchDegradedDoc`, which embeds `lsDoc`); `cancelResult`; `logLine`; and whatever `diagnose --json` and `version` emit. For each: the field, its type, its unit (dollars as floats; micros as integers; day keys as UTC epoch days), whether it is `omitempty`, and **which of the design's five availability states it needs**. Record the flags each command actually has, from the cobra command rather than the docs. Note the three facts the console's reader and its rendering depend on and that nothing else records: (i) the document is emitted one per change, compact, on a 1-second gap; (ii) **`--repo` narrows the JSON too** — it reaches `watch.FilterRepo(v View, key string)` in `internal/cli/watch.go`, so an earlier revision's "`--json` is always unfiltered" was wrong, and only the finished/ready axis is unfiltered; and (iii) **the synthetic and degraded modes emit different things**: `watchDegradedDoc` is `lsDoc` plus `degraded` and `notice` (a second document shape the console must handle), and `runWatchDegraded`'s error path writes a **plain, non-JSON sentence to stdout** — `"%s\nrun records unavailable (retrying): %s\n"` — on the same stream as the documents, so the reader must skip any line that is not a JSON object (design §2.1). The contract document is not finished until all three are written down with the symbol that proves each.

**And it settles the design's §4.3 CLI gaps against the code** — **seven** of them, one at a time, because the console ships without any of them: the degraded path's stray stdout line; `version` having no `--json` (**and no flags at all**: `newVersionCmd` is `cobra.NoArgs` with no `Flags()`, which is also why the root command sets no cobra `Version` field and `fugaro --version` fails); `project show --json` not existing on this tree (project-centric phase 1, #390, which ships `project show`, `project policy publish` and `project repos ls|add|rm` — and **no `project ls`**, which is the command an earlier revision of the design's gap 5 named); `watch --json` carrying no schema version; and **nothing enumerating installations** — `fugaro config` is `example|show|layer|init|publish` (`internal/cli/configcmd.go`), there is no `fugaro project` command at all, and that phase's commands describe the *selected* project's policy and repositories rather than the machine's installations; and **no PR field on a run that has not finished** — `JSONRun` (`internal/watch/json.go:54`, built at `:148`) carries `repo` and no `pr_url` and no `pr_number`, which exist only on `JSONFinished` (`:91-92`), while the run shape behind `runs ls --json` and `diagnose --json` is `runview.Row` (`internal/runview/runview.go:57`) and spells the same pair `pr_url` and `pr` (`:86`, `:109`) — **and no shape anywhere has a `repository` field**, which is the field name an earlier revision of the design's §5.1 rule 2 and this plan's Task 2.3 used for the rebuild; and **no provider on any of the three shapes** — `JSONRun`, `JSONFinished` and `runview.Row` each carry `repo` and no provider or origin host, and `doctor --json` (`internal/cli/doctor.go`'s `doctorOutput`) does not report one either, while the provider kinds are `github`, `bitbucket` and `fake` (`internal/task/task.go`'s `Slug`, which hashes provider and repository together one way, so a slug cannot be inverted) and the sandbox repository `edgeappinc/fugarosandbox` is on **Bitbucket** (`docs/gcp-live-checklist.md`) — so a link rebuilt as `https://github.com/<repo>/pull/<n>` is wrong for it and manufactures an off-site link that passes the host allow-list. Each is recorded with the symbol that proves the gap, so D19 is a list of facts rather than an impression; the installation one is also design §12 item 8, and it is what D13's add-an-installation form stands in for.

**And it writes down the one flag fact the whole spawn contract turns on**: which of the allow-listed commands accept `--project`/`--config`. The answer, read off each cobra command (`addCloudFlags`, `internal/cli/cloud.go:51`, called from `watch`, `budget show`, `report`, `runs ls`, `diagnose`, `logs`, `secrets ls`, `accounts ls` and `doctor`) is **all of them but `version`**, which takes no flags at all. That inventory is Task 1.3's table and design §5.1's, and `docs/gcp-live-checklist.md`'s Check 43 guard already states the exception ("**`fugaro version` is the one exception** — it takes no flags at all"). An earlier revision of the design said the console passes `--project` "on every spawn", which would have made the version handshake fail with an unknown-flag error.

**Tests:** `TestDocsDesktopConsoleDesignCarriesTheDecisions` (a docs-presence test in the `readDoc(t, path)` convention) asserting the design holds the auth decision, the spawn allow-list, the five availability states, the security posture's hard rules and the owner-run table; plus a Go test asserting `console/docs/json-contract.md` names every field a curated list of `internal/watch/json.go`'s struct tags contains — so a later rename in `json.go` fails a test here rather than silently breaking a screen; plus the three facts of the step above (the 1-second gap, `--repo` narrowing, and the degraded document and its non-JSON stdout line), each asserted by name so none can be dropped in a later edit.

**This is the one Go file permitted to name a path under `console/`, and Task 0.3 says so.** The test reads `console/docs/json-contract.md` **as bytes** — `os.ReadFile` on a path built from the repository root, in the `readDoc` convention this repo's docs tests already use — and it **imports nothing from it, embeds nothing from it, and adds no `go.mod` dependency to reach it**. Task 0.3's boundary test carries this file's name as a one-entry allow-list and asserts both halves of that sentence, so the exception is declared in the test that grants it rather than assumed.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** nothing.

**Commit:** `console: pin the JSON contract the app reads, field by field`

**Red step — `Expected: FAIL`** — the docs-presence test before the design's strings are added to it, and the field test before `console/docs/json-contract.md` exists.

### Task 0.2: What `watch --json` guarantees a long-lived child process

**Files:** `console/docs/json-contract.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Settle the design's UNVERIFIED item 2 by reading, not assuming: what `watchStream` does when its reader is slow; whether a closed stdout ends the process; what happens after `connRefused`; whether the day streams re-open at UTC midnight in a way that produces a gap (they do: `streamPhase`/`rollDay`); and what `degradedSince` (`internal/cli/watch.go`: the 24-hour window the degraded view reads runs over — an earlier revision of this task named it `degradeSince`, which is not a symbol in this repository), `StaleAfter`, `SilentAfter` and `LostAfter` mean for a consumer that is not a terminal. Write the consumption contract into the same document: how many documents arrive per minute in each mode, what a gap looks like from outside, how the degraded path's plain stdout line is told apart from a document, and whether Option A needs a supervisor process. This is the task that decides whether Task 1.3 spawns one child or supervises several.

**Tests:** a reading task still has to be pinned, or its four facts are prose a later edit can quietly drop. `TestDocsDesktopConsoleContractSettlesTheFeed` — in the same `readDoc(t, path)` convention as Task 0.1, in `internal/cli/docs_desktop_console_test.go`, which Task 0.3 names as the one Go file allowed to read a `console/` path — asserts that `console/docs/json-contract.md` states all four settled facts **and cites the symbol for each**: backpressure and what a slow reader does to the child; what a closed stdout does; the restart behaviour after `connRefused`; and the UTC-midnight day rollover. Each is a named sub-assertion, so dropping one fails a test rather than shrinking a document. The review criterion on top of it is that every sentence in the document cites a symbol.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** nothing.

**Commit:** `console: what the watch feed guarantees to a child process`

**Red step — `Expected: FAIL`** — the docs-presence test asserting the consumption contract's four settled facts.

### Task 0.3: The boundary — `console/` and the Go module never touch

**Files:** `internal/cli/docs_console_boundary_test.go`, `console/README.md`

**Steps.** Create `console/` as a directory holding only its `README.md`, and write the two assertions that keep the design's §8.1 claim true a year later: (a) no Go file in the module **builds against** a path under `console/`; (b) `go.mod`'s require block is unchanged from a recorded baseline, so the console cannot smuggle a Go dependency in. The README states the module's rule in one line: nothing under `console/` is importable by, or imports, the Go module.

**The boundary is about code, not about bytes, and this is the reconciliation that makes Tasks 0.1 and 0.3 agree.** Task 0.1's `docs_desktop_console_test.go` and its sibling here read `console/docs/json-contract.md` **as a string** — that is what `readDoc(t, path)` has always done for `docs/` — and an earlier revision of this step forbade *any* Go file referencing a path under `console/`, which made Task 0.1 impossible to write. So the test asserts three things instead of one: **(a1) no Go file has an `import` whose path is under `console/`** — an import path is a module path, so this is the real boundary; **(a2) no file, test or otherwise, uses `go:embed` over a `console/` path, and `go.mod`/`go.sum` gain nothing** (b, below); and **(a3) a literal allow-list of files permitted to *mention* a `console/` path contains exactly one entry today — `internal/cli/docs_desktop_console_test.go` — and every file on it is asserted to contain no `console/…` import and no `go:embed`**. The list is a slice in the test with a named test for a second entry appearing, so widening the exception is a deliberate edit to a test rather than a quiet new reference in some future file. Comments that merely *name* `console/` are excluded from (a1) by construction (the match is on import lines), and the test's own doc comment says why the distinction exists.

**Tests:** `TestConsoleIsOutsideTheGoModule` (a1 and a3, the import check and the one-entry allow-list), `TestGoModHasNoConsoleDependency` (b), and `TestNoGoFileEmbedsFromConsole` (a2).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint|Console' -count=1`

**Depends on:** nothing.

**Commit:** `console: the directory, its rule, and the two tests that hold it`

**Red step — `Expected: FAIL`** — `TestConsoleIsOutsideTheGoModule` against a planted `import` of a `console/` path **and** against a second file added to the allow-list; `TestGoModHasNoConsoleDependency` against a planted require line; and `TestNoGoFileEmbedsFromConsole` against a planted `go:embed console/…`. Every mutation is named in the PR body, as this repo does for a mutation-proven test.

---

## Phase 1 — The shell

### Task 1.0: The toolchain, pinned

**Files:** `console/package.json`, `console/package-lock.json`, `console/tsconfig.json`, `console/.npmrc`

**Steps.** Exactly as `internal/budget/rules/emulator/package.json` does for `firebase-tools`, and at the scale the design's §5.2 argues for: `private: true`, `engines.node`, **exact** versions with no `^` or `~` anywhere, a committed lockfile, TypeScript and the test tooling as the only `devDependencies` beyond Electron, and the renderer bundling **no framework runtime and no charting library** (design D9) — charts are drawn on the platform. Record Electron's exact version in `console/README.md` together with the sentence that it is bumped on purpose, as the emulator's package description does for its own pin. Write the two scripts every later task's command uses: `test:unit` and `test:e2e`.

**Tests:** a unit test that reads `package.json` and fails on any dependency specifier containing `^`, `~`, `*`, `latest` or a range — the pinning rule enforced, not documented; and one that asserts the lockfile exists and is in sync (`npm ci` in the command does this for the tree, the test does it for the file).

**Command:** `npm ci --ignore-scripts --no-audit --no-fund && npm run test:unit`

**Depends on:** Task 0.3.

**Commit:** `console: the toolchain, pinned exactly, with the pinning rule as a test`

**Red step — `Expected: FAIL`** — the specifier test against a `^` added to one dependency.

### Task 1.1: The hardened window — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/main/window.ts`, `console/src/main/protocol.ts`, `console/src/renderer/index.html`, `console/test/unit/window.test.ts`, `console/test/e2e/security.spec.ts`

**Steps.** Implement the design's §5.1 rules 1, 2, 3 and 9 — the numbering in the design as it now stands, after this revision inserted the `app://` scheme rule (3) and the Electron Fuses rule (4) and moved the rest down: `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`, `webviewTag: false`, `allowRunningInsecureContent: false`; **the bundled document served over a registered, standard, secure `app://` scheme through `protocol.handle('app', …)` in `console/src/main/protocol.ts`, never `file://`** — because a `file://` document is not served over HTTP, so `onHeadersReceived` never fires for it and a CSP **header** cannot be attached to it, leaving only the weaker and later-parsed `<meta>` form; `will-navigate` and `setWindowOpenHandler` denying every navigation and every window, with one validated `https:`-only `openExternal` path behind an allow-list of hosts; the CSP of rule 3 delivered **as a real response header on the `app://` response**, with the `<meta>` copy kept as belt-and-braces, **including `connect-src 'none'`**; and `setPermissionRequestHandler` refusing every permission (rule 9). `protocol.registerSchemesAsPrivileged` must run before `app.whenReady()`, and the scheme is declared `standard: true, secure: true` with no `bypassCSP`.

**Tests:** unit — the window options object, the navigation handler's refusals, and the URL validator against `javascript:`, `file:`, `data:` and an off-allow-list host; **and the protocol handler itself: a request for a path outside the bundle's root is refused, a traversal attempt (`app://…/../../etc/passwd`) is refused, and the served response carries the CSP header** — asserted on the `Response` the handler returns, so the header's existence is a test rather than a claim about a browser. E2E (Playwright + Electron) — the loaded document's origin is `app://` and not `file://`; a renderer-initiated request to a remote origin fails **twice over** (once by the CSP, once by a Playwright route handler that fails every request, so a CSP regression is still caught); `window.open` is refused; a `will-navigate` to a remote URL leaves the app on its own document.

**Command:** `npm run test:unit && npm run test:e2e -- security`

**Depends on:** Task 1.0.

**Commit:** `console: the hardened window, and the tests that try to leave it`

**Red step — `Expected: FAIL`** — `security.spec.ts`'s network assertion against a window with `connect-src 'self'`, and its navigation assertion against a handler that returns `{ action: 'allow' }`.

### Task 1.2: The IPC allow-list — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/main/ipc.ts`, `console/src/preload/index.ts`, `console/test/unit/ipc.test.ts`

**Steps.** The design's §5.1 rule 5, built so it cannot be widened by accident: the preload exposes a **frozen, versioned object of named operations** — no `invoke(channel, …)`, no channel name from the renderer, no dynamic `require`. Each channel has a closed argument schema validated in the main process, takes no path, no command and no URL beyond the validated external-link one, and returns data that is already rendered. Then the mutation-proof half: a table of operations the console must **never** reach — every write subcommand, the `secrets` commands, the `init`, `upgrade` and `exec` verbs, the hidden `budget history` job command, **and the project-centric phase 1 commands that write or publish: `project repos add`, `project repos rm`, `project policy publish` and `project sync`** (`docs/plans/2026-10-14-project-centric.md`; PR #390 does not exist on this tree yet, so the names are pinned here against that plan rather than against a merged command, and the test fails the day one of them appears in an allow-list entry) — asserted unreachable by iterating the allow-list itself, so that adding one of them fails a test named for it. Freeze the exposed object and assert `Object.isFrozen`.

**Tests:** `ipc.test.ts` — the allow-list's read-only invariant, the never-reachable table, the argument schemas against malformed input, `Object.isFrozen`, and the absence of a generic invoke.

**Command:** `npm run test:unit`

**Depends on:** Task 1.1.

**Commit:** `console: an IPC allow-list, not a bridge, and the table of things it cannot reach`

**Red step — `Expected: FAIL`** — the never-reachable test against `budget kill` added to the list.

### Task 1.3: `FugaroSource`, and the one child process — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/source/source.ts`, `console/src/source/cli.ts`, `console/src/main/spawn.ts`, `console/src/main/spawnlog.ts`, `console/test/unit/spawn.test.ts`, `console/test/unit/spawnlog.test.ts`, `console/test/unit/configread.test.ts`

**Steps.** The interface every screen talks to, so that design D1's Option C can replace Option A without touching a screen: `FugaroSource` yields `JSONDoc`-shaped snapshots and emits change events and knows nothing about how they were produced. Then the v1 implementation, which is where four of the design's §3.4 rules stop being prose:

- **The binary is resolved explicitly, never through the app's `PATH` alone** (rule 4): the operator's configured path first, then `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and only then `PATH`; the resolved absolute path and the version it reports are shown in the header, and a binary whose version cannot be read is refused rather than run.
- **The project is passed explicitly — on every command that takes the flag** (rule 3): `--project <name>` from the installation the operator chose (D13), plus `--config <path>` when that installation is not the one the ambient environment would select. This is not optional and it is not a credential — a Finder-launched app has cwd `/`, launchd's environment and the system `PATH`, so `selectProject`/`selectWith` in `internal/cli/project.go` would otherwise pick the wrong installation or none. **The table is per-command, and `version` is the exception**: the nine that take it are `watch`, `budget show`, `report`, `runs ls`, `diagnose`, `logs`, `secrets ls`, `accounts ls` and `doctor` (`addCloudFlags`, `internal/cli/cloud.go:51`, called from each of those constructors), and `newVersionCmd` (`internal/cli/root.go`) is `cobra.NoArgs` with no `Flags()` at all, so **the handshake runs `fugaro version` bare** and a console that appended `--project` to it would fail on every machine with `unknown flag: --project`. An earlier revision of this step said "on every spawn", which is the defect this bullet now names; the checklist's Check 43 guard says the same thing about `version` and is where the console's table gets it from. **And the installation is verified before it is stored, because the CLI refuses every command for a mismatched one** (round 3; design §3.4 rule 7 and D13): with a checkout directory chosen, the add-an-installation screen runs **one allow-listed read** — `doctor --json`, with the installation's `--project`, its `--config` when it is not the ambient one, and **the checkout as the child's working directory**, following this task's own rules 3 and 5 — and if the checkout's `fugaro.yaml` names another project, `localcfg.Select` refuses with `this checkout belongs to project %s; --project says %s` (`internal/localcfg/select.go`, reached from `checkoutProject`/`selectProject` in `internal/cli/project.go`). The screen shows that sentence verbatim, as the CLI's own text, and **does not store the installation**: storing it would leave every screen refused later, one refusal per command, with nothing on screen explaining why. The app still does not read the file itself (rule 7) — **the CLI is the reader**, and the test asserts the refusal path: a stub checkout whose `fugaro.yaml` names another project must leave the installation unsaved, and a matching one must save it.
- **The child's working directory is set explicitly** (rule 5): the installation's checkout directory when it has one, and otherwise a private, empty directory the app owns — never the app's own default of `/`, which is nobody's checkout. `doctor` is called with `--dir <that directory>` and **never with `--strict`**, because outside a checkout doctor's `plugin-pin` check is a warning that `--strict` turns into a failure, and that failure is about the checkout rather than about the environment the console draws (`internal/cli/doctor.go`).
- **The child's environment is built, not inherited** (rule 5, §5.1 rule 8): a fixed set — **`PATH`, `HOME`, `TMPDIR`, `LANG`/`LC_ALL`, the `XDG_*` variables, and the chosen installation's `FUGARO_PROJECT`** — and **nothing else from the app's own environment**. An earlier revision of this task said the child inherits the operator's environment unchanged; that is wrong on two counts, because it hands the child anything the app's own environment carries and it leaves `FUGARO_PROJECT` to chance. **And "the resolved prefix on `PATH`" was the second wrong version of this bullet** (round 4): the `PATH` the child gets is the resolved binary's own directory **first, then every well-known prefix the console itself resolves against** — `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin` — because **the CLI looks up external tools itself**: `doctor` resolves its SDK tools with `exec.LookPath` (`internal/cli/doctor.go:147`, used by the terraform check and the cosign problems), and `terraform`, `cosign` and the rest do not sit beside the `fugaro` binary even under Homebrew. The console's own inherited `PATH` still never reaches the child. **A tool outside that fixed list is invisible to the child**, and the environment view says exactly that — *not on the `PATH` the console gives the CLI*, listing the prefixes and the remedy (install it under one, or link it there) — rather than "not installed", because the console cannot know what the operator's shell has; the view's source is `doctor --json`'s checks and their `fix` strings (`doctorCheck`), rendered as the CLI worded them.
- **argv is an array, `shell: false`, and never built from a job-written field** (rule 1).
- **The spawn log exists here, because this is the task that spawns** (design §5.1 rule 10, **D20**). An earlier revision of the plan referred to a "session log" in two later tasks as if it already existed — Check 59's whole negative assertion was built on a file with no defined path, no defined schema and no defined retention, so its `jq` and `grep` commands could not have run. It is specified here instead: **`spawnlog.ts` writes nothing unless `FUGARO_CONSOLE_SPAWN_LOG` names a path**; when it does, it appends one JSON object per line to that path, created with mode `0600`, one line per spawn, with **exactly these four keys and no others**:

  ```json
  {"ts":"2026-10-15T14:22:31Z","argv":["/opt/homebrew/bin/fugaro","watch","--json","--project","fugaro"],"cwd":"/Users/owner/src/fugaro","project":"fugaro"}
  ```

  `ts` is RFC 3339 UTC; `argv` is the array as spawned, binary path first; `cwd` is absolute; `project` is the installation's project name — the value passed as `--project`, so **the bare `version` handshake still records which installation it was for**. It holds no command output, no environment, no credential and no job-written text. It is bounded at **1000 records**, after which the writer appends one `{"truncated":true,"limit":1000}` line and stops rather than rotating or truncating in place. The app never reads it back, never deletes it, never rotates it and never transmits it; it is the operator's file at the operator's path, and `console/README.md` documents the variable and what the app writes to disk (rule 10, amended 2026-10-18 by C8 to add Task 3.4b's installations file as the second and last write). **This is what Check 59's assertion (4) reads, and Task 4.3's FACT commands use these key names.**

Implement what Task 0.2 settled: restart behaviour, a closed-stdout exit, the UTC-midnight day rollover, and the reader rule that skips a stdout line which is not a JSON object (Task 0.1's fact (iii)).

**Tests:** `spawn.test.ts` against a stub executable — argv is an array and never a shell string; a subcommand outside the table is refused before spawn; **`--project` is present on exactly the argv that accept it and comes from the chosen installation, not from the ambient environment**: the test iterates the spawn table and asserts the flag on every entry except the `version` handshake, and asserts the handshake's argv is exactly `["version"]` — the two halves matter equally, because a console that put `--project` on `version` fails on launch and one that left it off `watch` shows another installation's numbers; **the environment is exactly the allow-list** — asserted by diffing the keys the stub reports against the declared set, so an added variable fails a named test, and a planted `GOOGLE_APPLICATION_CREDENTIALS`/`FUGARO_PROJECT` in the app's own environment is asserted **not** to reach the child; **the working directory is the installation's, and is never `/`** — the stub reports its own `cwd`, and an installation with no directory is asserted to spawn from the app's private one, with `doctor`'s argv carrying `--dir` and never `--strict`; the binary resolver finds a stub at a configured path, then at each well-known prefix in order, and refuses a candidate that reports no version; **the child's `PATH` is the declared list and not one directory** — asserted against the stub's own environment, with the mutation being a spawn whose `PATH` is the resolved binary's directory alone, which must fail a named test (a `doctor` run under it would report `terraform` and `cosign` missing on a machine that has them, which is the false-environment defect this bullet exists to prevent); a hostile string from the feed is never interpolated into argv; a non-JSON stdout line is skipped rather than parsed; a child that dies is restarted with backoff and the UI is told.

`spawnlog.test.ts` is the other half, and it is written against the schema Task 4.3's check reads rather than against the implementation:

- **Unset means nothing on disk**: with `FUGARO_CONSOLE_SPAWN_LOG` unset, a full session of stub spawns creates no file anywhere the test can see, and the writer's open is never reached.
- **One line per spawn, exactly four keys**: after N stub spawns the file has N lines, each parsing as one JSON object, and **the key set of every line is exactly `{ts, argv, cwd, project}`** — asserted as a set, so an added `env` or `output` field fails a named test rather than shipping a log that quietly holds more than the design promised.
- **The values are the ones the spawn used**: `argv` equals the array handed to the stub, `cwd` equals the directory the stub reported, and `project` is the chosen installation's name **including for the bare `version` handshake**, which is the case the check's `sort -u` depends on.
- **The bound holds**: at record 1001 the writer appends `{"truncated":true,"limit":1000}` and writes no further line, so a long session cannot grow an unbounded file.
- **The app never reads it back**: a grep-style test over `console/src/` asserting the only reader of the path is the writer — no screen, no source, no IPC handler imports it, which is what keeps it evidence rather than application state.

**And rule 7's third prohibition gets the same kind of test, because it is the one on that rule with none** (round 4). §3.4 rule 7 says the app never reads the operator's config, and nothing in this plan asserted it — while the config is read in the **main** process (where the installation store lives), outside the IPC surface Task 1.2 covers, and reading it is exactly the shortcut D13 forbids: it would answer "where do the installations come from" the one way the design says it must not. So `configread.test.ts` is a grep-style test over `console/src/` in the style of the spawn-log reader test: **no file there opens the operator's config** — no `~/.config/fugaro`, no `XDG_CONFIG_HOME` join, no `FUGARO_CONFIG`-derived path, no `readFile`/`readFileSync`/`createReadStream` of any of them — with the distinction the rule turns on written into the test as its two halves: **the config's path may be a *string* the installation stores and the console passes to the child as `--config`**, and **the app itself never opens it**. The mutation that proves it: a `readFileSync(path.join(os.homedir(), '.config', 'fugaro', …))` anywhere under `console/src/` must make it red. The escape hatch is named in the test's own message: **the CLI is the reader** (which is also why the add-an-installation check of the `--project` bullet above is a spawn, not a file read).
- **It is never transmitted**: the log path appears in no fetch, no IPC payload and no URL construction (the same shape of test the update check uses in Task 4.4).

**Command:** `npm run test:unit`

**Depends on:** Task 1.2, Task 0.2.

**Commit:** `console: FugaroSource, and the one child process it is allowed to spawn`

**Red step — `Expected: FAIL`** — the environment-diff test against a spawn that injects `GOOGLE_APPLICATION_CREDENTIALS` (and against one that inherits `process.env` wholesale), the `--project` test in **both** directions (a spawn that omits it from `watch`, and one that appends it to `version`), the working-directory test against a spawn that leaves `cwd` unset, and the argv test against a template-string command. For the spawn log: the unset-means-nothing test against a writer that logs unconditionally, the four-key set test against a line carrying an extra `env` field, and the bound test against a writer that appends past 1000.

### Task 1.4: Recorded, redacted fixtures, and the recorder — the tool is agent-written, **the recordings are OWNER-RUN**

**Files:** `console/src/source/fixture.ts`, `console/tools/record-fixture/{index,redact}.ts`, `console/test/fixtures/*.ndjson`, `console/test/unit/redact.test.ts`, `console/test/unit/fixture.test.ts`, `docs/pending-workflows/console-tests.yml`, `docs/pending-workflows/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** The recorder spawns `fugaro watch --json` (from the sandbox checkout, **never `belong`**), captures the NDJSON, and redacts: `watch` already clips and redacts a run title, and the recorder applies the same treatment to repository names, PR URLs, requester addresses and anything else installation-specific. Import the fixture renderer that makes the recording usable: a `FugaroSource` implementation that replays a file, including its timestamps, so a gap in the recording is a gap in the test. **Stage the tests workflow here, and this is a correction of where it used to sit.** An earlier revision staged one `console.yml` in phase 4 and copied it in as Task 4.5, which meant **phases 1 to 3 merged with no CI running a single console test at all** — the task that stages is now in the phase that first needs it, and the owner's copy-in is Task 1.5, immediately after. So: `docs/pending-workflows/console-tests.yml` — **tests only, no secret of any kind** — with the two runners (`macos-latest`, and `ubuntu-latest` under `xvfb-run`), `actions/setup-node` pinned to a full SHA and taking its version from `console/package.json`'s `engines.node` (which mirrors the emulator precedent's `"node": ">=20"`), `npm ci --ignore-scripts --no-audit --no-fund`, `npm run test:unit`, and the e2e script — which is written to run whatever specs exist and to pass when none do — **Task 1.1's Playwright harness is the one that exists by then** (`console/test/e2e/security.spec.ts`; an earlier revision said the harness arrived with Task 2.0, which is wrong, since Task 1.1 already runs an e2e spec under this very script) — so the file can land in this phase and gain later specs without a second copy-in — and **a job named exactly `console` that runs unconditionally** on every pull request and every push to `main` — **no `paths:`, no `paths-ignore:`, no `if:`**. The design's §9.4 says why a path filter here would break the release gate, and the tests below are what keep it from creeping back. **The signing, notarising and stapling workflow is not this file**: it is Task 4.2's `console-release.yml`, and it must not share a file with a job that runs on every pull request.

**The eight recordings are OWNER-RUN, and this is the correction that matters here.** The design's §9.1 asks for the normal run, the **`silent`-then-`lost`** run, the **kill flipping mid-feed**, the **repository at its cap**, the OAuth notional run, the absent field, the gap, and a hostile title — **eight cases, one recording each — the eight the design's §9.1 lists** (an earlier revision of this task said "seven" in four places while enumerating eight cases; the list, not the numeral, is the authority, and the numeral is now eight everywhere). Three of those cannot be produced without a live sandbox: a run that stops heartbeating and then goes `lost` needs a real stalled run, a kill switch flipping needs a real `budget kill` against the sandbox, and a repository at its cap needs real spend to accumulate. **An agent performs none of it** — no live action, and never against `belong` (Global Constraints 2 and 6). So the task splits cleanly: the **recorder, the redactor and the replay source are agent-written and agent-tested against a synthetic frame** committed with them, and the **recordings themselves are a step the owner runs** with a documented command from `console/README.md`. Until the owner records them, the fixtures directory carries one synthetic, obviously-fake frame so the harness is exercisable and no screen has ever claimed to be tested against real output. The recordings are re-made whenever `watch --json` changes (C3).

**Tests:** `redact.test.ts` — a redactor that is *tested*, not trusted: it must remove a known address, a known repository name and a known PR URL from a recorded frame, must be idempotent, and must **fail closed** on a frame it cannot parse. `fixture.test.ts` — the replay source reproduces the recording's timestamps and **a gap in the recording is a gap in the replayed stream** (the assertion that makes the `stale` tests of Task 3.1 mean anything). Plus a test that every committed fixture passes the redactor unchanged, so a hand-edited fixture is caught — it is what will catch the first owner-recorded file the redactor should have altered.

**And the staged workflow's text is tested here, not in phase 4**, because this is the task that writes it — these two tests moved with the file:

- `TestConsoleTestsWorkflowIsStagedAndPinned` (in `internal/cli/docs_desktop_console_test.go`, the one Go file Task 0.3 allows to read a `console/` path, plus the `docs/pending-workflows/` path every docs test may read) asserts, over `docs/pending-workflows/console-tests.yml`: **every `uses:` is pinned to a full 40-hex commit with a `# vX.Y.Z` comment**; **the file contains no `secrets.` reference at all** — the tests job holds no secret, and this is the assertion that keeps the signing material in the other file; **no `pull_request_target`, no `workflow_run` and no `permissions: write-all`**; every job declares `permissions:` explicitly and only what it needs (`contents: read`); **`npm ci --ignore-scripts --no-audit --no-fund` is the install step** and there is no `npm install`, no `npm test` and no unpinned `npx`; **if a file exists under `.github/workflows/` at that name at all, it is byte-identical to the staged copy** — the assertion that a workflow under `.github/workflows/` was copied from `docs/pending-workflows/` rather than hand-written or edited by an agent. **An earlier revision asserted the file's *absence* there, which was a bug rather than a stronger check**: Task 1.5's whole point is that the owner copies the file in, so the old assertion would have turned `main`'s `test` red the moment they did it. Byte-identity is what the assertion can honestly hold before *and* after the copy-in, and it is the stronger of the two: absence only says nobody wrote a file, identity says the file that exists is the reviewed one; and **no literal that could be a secret** appears (no `-----BEGIN`, no long base64 blob) — trivially true here, since the file names no secret at all, and asserted anyway because the two files were one file in an earlier revision and this is the test that would catch them being merged again.
- `TestConsoleWorkflowJobIsGateable` — **the assertion that keeps Task 4.6 from silently breaking every tag on this repository.** Over the same staged file it asserts: **a job whose key is exactly `console`** (the string in `release-gate.sh`'s `names`, so a rename must be a deliberate edit to this test *and* to Task 4.6); **the workflow triggers on `push` to `main` and on `pull_request`**; **the `console` job has no `if:` guard**, in particular none on a `changes`/`docs_only` output; and **the workflow has no `paths:` or `paths-ignore:` at the trigger level and the `console` job has none at the job level**. Each is a named sub-assertion. The reason is not style: `release-gate.sh` requires this job's check runs on the tag commit and, in its `identical_tree_checks` fallback, on the pull request head too — **a check that does not exist is not a success**, so a `console` job filtered to `console/**` would refuse every release, including the ones that never touch the console. Ordering the gate edit after the copy-in does not help; the filter is the bug. **The job is a matrix, and that is why the gate names its two check runs and not the job key**: GitHub appends the matrix values to the check-run name, so what the checks API reports is `console (ubuntu-latest)` and `console (macos-latest)`, never a bare `console` — a correction the owner made on 2026-10-18 after checking a real check-runs response, and the reason Task 4.6's edit is the array it is (see that task below).
- `TestPendingWorkflowsReadmeNamesTheConsoleTestsRow` asserts the README carries **this** file's row: what the file is, that it is staged because no agent may push a workflow here, and the one command that applies it. **The name is singular and row-specific on purpose** — Task 4.2 adds a second row and a second test, `TestDocsPendingWorkflowsReadmeNamesTheConsoleRows` (plural), which asserts **both** rows; a reader should not have to work out whether two similarly-named tests in two tasks are the same test, which is what the earlier `…Row`/`…Rows` pair left ambiguous.

**The review criterion on top of the tests**: a reviewer reads the pinned SHAs against upstream and confirms the tests job would run green on a tree where `console/` and nothing else has changed.

**Command:** `npm run test:unit`

**Depends on:** Task 1.3.

**Commit:** `console: recorded, redacted fixtures and the recorder that makes them`

**Red step — `Expected: FAIL`** — the redaction test against a recorder that passes a title through untouched, and the fixtures-are-clean test against a fixture with a planted address; `TestConsoleTestsWorkflowIsStagedAndPinned` before the staged file exists and against a planted `uses: actions/checkout@v5` and a planted `secrets.APPLE_CERTIFICATE`; and `TestConsoleWorkflowJobIsGateable` against a `console` job renamed to `console-tests` and against one carrying `paths: ['console/**']`. **Those last two mutations are the ones to run first**, because they are the failure that is invisible until a tag is refused.

**Owner-run step, named so it is not mistaken for agent work:** `console/README.md` documents one command — `npm run record-fixture -- --out test/fixtures/<name>.ndjson` — run **by the owner**, from the sandbox checkout, with the sandbox installation selected, under this repo's usual guard. The **eight** recordings are attached to the task's PR by the owner, or in a follow-up commit, and nothing in this plan's agent-run steps produces them. **If the owner can produce only some of the eight in a sitting, the fixture set lands with those and `console/README.md` names the missing cases and why** — a case that cannot be recorded from the sandbox yet is recorded as missing, not dropped silently, and the design's hostile-title case is covered by the renderer tests in the meantime (design §9.1).

---

### Task 1.5: The tests workflow, copied in — OWNER-RUN, no agent copies it

**Status: applied by the owner at 8a17d941.**

**Files:** `.github/workflows/console-tests.yml` (created by the owner, never by an agent), `console/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** This task exists because of a real hole in the previous revision of this plan: the staged tests workflow used to be copied in as Task 4.5, in phase 4, which would have meant **phases 1 through 3 — every task that writes console code — merged with no CI running a single console test.** The console's own tests would have been written, reviewed and merged on the strength of the reviewer's laptop. **What this task does not fix, and the plan says it instead of claiming more than it can: Tasks 1.0 to 1.4 merge before this copy-in, so the pinned toolchain, the hardened window, the IPC allow-list, `FugaroSource` and the fixtures have no console CI on their own pull requests.** The gap is four tasks wide and it closes here; "the console has CI from its first pull request" is false in this repository's history and is not claimed anywhere in this plan or the design. So the copy-in moves to the phase that first produces console code, and it is a separate task from the staging (Task 1.4) precisely because they have different authors: **an agent stages the file; only the owner applies it.**

The owner copies `docs/pending-workflows/console-tests.yml` to `.github/workflows/console-tests.yml` unchanged, commits it, and confirms on the PR that the console's two check runs (`console (ubuntu-latest)`, `console (macos-latest)`) appear and run green on the first console pull request. **No agent in any later task copies, edits, renames or deletes this file** — Global Constraint 7 (nothing under `.github/workflows/` is created or edited by an agent) and the rule that a workflow change cannot be delivered by Fugaro's push both put it out of reach, and `TestConsoleTestsWorkflowIsStagedAndPinned` asserts the staged copy still exists under `docs/pending-workflows/` so a later edit to one file without the other is caught.

**The agent-run part of this task** is the record, and it is small on purpose: `console/README.md` gains a line naming when the workflow was applied (the applying commit), so the plan's reader can tell which console commits had CI and which did not — the gap above is a fact about history, and after this task there should be exactly one such gap, closed at this point. `TestPendingWorkflowsReadmeNamesTheConsoleTestsRow` (Task 1.4's, singular, named for this file's row) is extended to assert that line is present and that it names a commit, not a date.

**Tests:** `TestPendingWorkflowsReadmeNamesTheConsoleTestsRow` (extended as above) plus the two workflow-text tests of Task 1.4, unchanged and still passing — the tests read the **staged** file, which is exactly why they keep working after the copy-in.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1`

**Depends on:** Task 1.4 (the staged file) and **the owner** — this is the one task in phase 1 an agent cannot complete, and phase 1's later tasks may proceed while it waits, since the copy-in is not a dependency of any code they write.

**Commit:** `docs: the console tests workflow is applied, and the README says when`

**Red step — `Expected: FAIL`** — the extended README test against the README before the line is added.

**What this task is not:** it is not the signing workflow. That is Task 4.2's `console-release.yml`, staged under a different name, copied in by a different owner task (Task 4.5, now in phase 4 where it belongs, because packaging is what needs it). The two files are never the same file, and Task 4.2 says why: one runs on every pull request and must therefore hold no secret, and the other holds the Apple signing material and must therefore never run on a pull request.

---

## Phase 2 — The screens

### Task 2.0: Availability, sanitization and the text path — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/shared/{availability,sanitize,schema}.ts`, `console/src/renderer/app.ts`, `console/test/unit/{availability,sanitize,schema}.test.ts`

**Steps.** The design's first UI rule, made into code every screen must go through: one type carrying the five states (measured, notional, estimated, unavailable, gap), one renderer that turns each into its presentation, and no screen allowed to format a bare number. Then the design's §5.1 rule 7: the sanitization is **re-applied at the display layer** rather than trusted to have survived the subprocess boundary, and job-written text is rendered through text nodes — never `innerHTML`, never a `dangerouslySetInnerHTML`-equivalent, never a URL built from it. Third, `schema.ts` is where **both document shapes** are admitted and nothing else is: an ordinary `JSONDoc`, and `watchDegradedDoc` (`lsDoc` plus `degraded` and `notice`), decoded by a discriminated check on the `degraded` key, with an unrecognised shape surfacing as the skew state of Task 3.2 rather than as an empty screen. A feed line that is not a JSON object never reaches this layer at all — Task 1.3 drops it.

**Tests:** `availability.test.ts` — a missing field renders `n/a` and never `0`; a notional value renders with its marker; a gap breaks a series rather than interpolating. `sanitize.test.ts` — the hostile-title corpus (control characters, escape sequences, `<script>`, a `javascript:` URL, a very long line) renders as text; and a lint-style test that fails on `innerHTML` appearing anywhere under `console/src/renderer/`. `schema.test.ts` — both document shapes decode, a `degraded` document is not mistaken for a normal one, an unknown shape yields the skew state and not an empty screen, and a document missing a field the screens read fails decoding by name.

**Command:** `npm run test:unit`

**Depends on:** Task 1.4.

**Commit:** `console: every number carries its state, every agent string stays a string`

**Red step — `Expected: FAIL`** — the `innerHTML` test against a planted assignment, and the availability test against a formatter that returns `0` for a missing field.

### Task 2.1: The Live screen

**Files:** `console/src/renderer/screens/live.ts`, `console/test/unit/live.test.ts`, `console/test/e2e/live.spec.ts`, `console/test/e2e/__screenshots__/live-*-{darwin,linux}.png`

**Steps.** The design's §6.2 screen 1, drawn from `watch --json` alone: the project line with mode, day and the connection state; the global cap bar with `counted` and the cap; one row per repository with its bar, its 5-minute rolling burn sparkline and its kill banner (with by, at and reason); and under each repository its runs — stage, round, verify, models, recipe, auth, spend, age — flagged `silent` (>60 s), `lost` (>3 min) and amber past 80% of the stage deadline, with `queued`/`stuck` rows from the bucket side and an OAuth run's spend marked notional. The density rules are the design's §6.1 rules 2 and 3: ~24 px rows, tabular numerals, right-aligned figures, no decorative colour. The burn window must match `watch`'s own (`BurnWindow`, 5 minutes) **and a test asserts the two constants cannot drift**.

**Tests:** `live.test.ts` — the row model from a fixture frame, each flag's boundary at exactly its threshold, the burn window against the constant. `live.spec.ts` — the screen renders the fixture, a killed repository shows its banner, a queued row is present; and a screenshot golden per theme.

**Goldens (owner-run step).** The Task 2.1 PNGs are not committed: pixels differ between macOS and Linux, and CI runs both legs (`console (ubuntu-latest)` and `console (macos-latest)`, the two check runs Task 4.6's gate requires), so `console/playwright.config.ts` names each golden per platform (`snapshotPathTemplate` `{arg}-{platform}{ext}`: `live-light-darwin.png`, `live-light-linux.png`) and each leg compares against its own recording. It sets `updateSnapshots: 'none'`, so a missing baseline fails the golden tests rather than writing one and passing. To record, on EACH platform: `cd console && npm run build && npx playwright test --update-snapshots` (under `xvfb-run --auto-servernum` on Linux), then commit `console/test/e2e/__screenshots__/` with both platforms' files (the tests "has a golden for each theme" fail unless the running platform's file exists for every theme). Until then the only way to run the rest of the e2e pass is the explicit `FUGARO_CONSOLE_GOLDENS=skip`, which skips exactly the golden tests; the staged `docs/pending-workflows/console-tests.yml` sets it on the job so the job's checks are green when copied in, and the owner deletes that `env:` block (in both copies) once both platforms' goldens are committed.

**Command:** `npm run test:unit && npm run test:e2e -- live`

**Depends on:** Task 2.0.

**Commit:** `console: the live screen, dense and honest`

**Red step — `Expected: FAIL`** — the boundary test at exactly 60 s and 180 s.

### Task 2.2: The Spend screen

**Files:** `console/src/renderer/screens/spend.ts`, `console/test/unit/spend.test.ts`, `console/test/e2e/spend.spec.ts`

**Steps.** The design's §6.2 screen 2, from `report --json`: a time series with `--since`/`--until` and a breakdown switch (`day`, `week`, `month`, `repo`, `model`, `person`), with the three accounts that must never be summed kept visibly apart — model spend, `NOTIONAL~`, and compute — following `docs/design/m9-budget-and-dashboard.md` §10's "one account of cost". Partial days (`final` false, or no document) draw hollow with a `(partial)` marker; a day beyond the retained window is a gap, not a zero. The caps panel shows global, defaults and per-repository overrides with **the binding scope named** for each effective per-run cap, from `budget show --json`. **The person breakdown is off by default** (design §2.3 and §6.2): the fixture still contains the data, and a test asserts it is not on screen until the operator turns it on.

**Tests:** `spend.test.ts` — no screen ever adds a notional to a real total (asserted on the series model, mutation-proven by summing them); a partial day is hollow; the person view is hidden by default. `spend.spec.ts` — the three series render separately, the caps panel names a binding scope, and a golden.

**Command:** `npm run test:unit && npm run test:e2e -- spend`

**Depends on:** Task 2.1.

**Commit:** `console: the spend screen, with the three accounts kept apart`

**Red step — `Expected: FAIL`** — the notional-sum test against a model that adds them, and the person-view test against a default that shows it.

### Task 2.3: The Projects and Run detail screens, and the replay buffer

**Files:** `console/src/renderer/screens/{projects,run}.ts`, `console/src/shared/replay.ts`, `console/test/unit/{projects,replay}.test.ts`, `console/test/e2e/{projects,run}.spec.ts`

**Steps.** The design's §6.2 screens 3 and 4. **Projects** renders what exists today — caps, kill state, the enrolled and the seen repositories, project jobs — and says plainly what is not built (`config/policy` is project-centric phase 1; the enrollment allow-list is phase 1 and 4), rather than drawing an empty box that looks like zero. The secrets and accounts inventory is **names, labels and version counts only**, never a value. **Run detail** shows the ledger, the stage timeline with deadlines, per-stage spend, the outcome, **the pull request as text — repository, number and `pr_url`, with the reason no link is offered until design §4.3's gap 7 lands** — the `logs` link, the `diagnose` view, and **the replay buffer** — the design's §6.1 rule 4, taken from tmog.org's Flight Recorder: a bounded in-memory ring of the last N minutes of the feed, scrubbable, exportable to a file, which is also the debugging aid for "the run was slow at 14:20".

**Tests:** `projects.test.ts` — an unbuilt section renders as not-built and never as a zero; the inventory contains no value-shaped string. `replay.test.ts` — the ring's bound is enforced, an export round-trips through the fixture source, and scrubbing to a moment reproduces that moment's view. `projects.spec.ts`/`run.spec.ts` — goldens.

**Two of these are worth naming, because round 2 found both of them stated and untested in the design, and round 4 found the first of them wrong rather than merely untested:**

- **The pull request is shown, and no link is built — not until a document says which provider the repository is on.** This is round 4's correction, and it replaces a rule that was wrong in two ways at once. **What the shapes do carry**: the repository is **`repo`** everywhere (`JSONRun` and `JSONFinished`, `internal/watch/json.go:54`, `:83`; `runview.Row`, `internal/runview/runview.go:57`) and the number is **`pr_number`** on `JSONFinished` (`json.go:92`) but **`pr`** on `runview.Row` (`runview.go:109`), with `pr_url` beside it on both — and an earlier revision named `repository` for the first of those, which no shape has. **What no shape carries is the provider**: Fugaro opens pull requests on **GitHub and Bitbucket Cloud** (`docs/git-providers.md`; provider kinds `github`/`bitbucket`/`fake`, `internal/task/task.go`'s `Slug`, which hashes provider and repository together one way, so a slug cannot be inverted), and neither `JSONRun`, nor `JSONFinished`, nor `runview.Row`, nor `doctor --json` reports one. So §5.1 rule 2's construction **cannot be built for every run**, and the failure is not a missing link but a wrong one: on `edgeappinc/fugarosandbox` — the sandbox repository this repository's checks use, and **Bitbucket** (`docs/gcp-live-checklist.md`) — a rebuild as `https://github.com/<repo>/pull/<n>` passes the design's own host allow-list while naming a repository the operator does not own, and it would be opened by design. **The implementation therefore opens no PR link at all** (design §4.3's gap 7): it renders the run's repository, its number and its **`pr_url` as text, never handed to `openExternal`**, with the one-line reason.
  **The tests, per source, and with a Bitbucket repository rather than only a GitHub one**: a `runview.Row` with `repo` + `pr` and a `JSONFinished` with `repo` + `pr_number` **both render the pull request as text and produce no navigable link**, asserted on the open-external spy rather than on the DOM; **a Bitbucket-shaped run whose `pr_url` is `https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/<n>` is the named case** — the console must not turn it into a `github.com` URL, and must not open it either while the provider is unknown; a `pr_url` on an allowed host but the wrong path for this run's `repo`/number opens nothing; a `pr_url` on a disallowed host opens nothing and shows the refusal text; and **a live `watch --json` entry, which has no `pr_url`, no `pr_number` and no `pr` at all, renders the text form with no number invented**. **And the construction itself is a unit test with both providers supplied** — `github` → `https://github.com/<repo>/pull/<n>`, `bitbucket` → `https://bitbucket.org/<workspace>/<name>/pull-requests/<n>` — so that the day gap 7 lands, the per-provider construction is already written and tested and the screen only has to start offering the link; a test asserting the console **refuses to build a link from a `pr_url` alone** (no provider, no number → text) is what keeps the inference from creeping back in.
- **The log panel is bounded.** The design's §2.3 bullet: the console runs a **capped** `logs RUN --json` window and **links** to the full transcript via `logs RUN --url` rather than following it — `--follow` would be an unbounded child process writing an unbounded amount of installation data into a renderer that cannot read it. The test asserts the panel renders exactly the cap, that a document longer than the cap is truncated rather than scrolled into a full transcript, and that **no spawn in the spawn log passes `--follow`** (the same spawn-log assertion Task 1.3's `spawnlog.test.ts` and its contract test use).

**Command:** `npm run test:unit && npm run test:e2e`

**Depends on:** Task 2.2.

**Commit:** `console: projects, run detail, and the replay buffer`

**Red step — `Expected: FAIL`** — the unbuilt-section test before the not-built state exists, and the ring-bound test before the bound is enforced.

### Task 2.4: Wire the Spend screen's controls — SECURITY-SENSITIVE, independent opus review

**Added 2026-10-17 at the owner's approval** (the owner approved adding this task; nothing else about it is approved, and the plan's approval gate stands). **Why it exists:** Task 2.2 draws a range picker, a breakdown switch and a person-view knob, and **nothing wires them** — Task 2.2's steps name `--since`/`--until` and the switch, but no task makes a control change what is read. Numbered 2.4, after 2.3, and **no other task is renumbered**.

**Files:** `console/src/renderer/screens/spend.ts`, `console/src/main/ipc.ts`, `console/src/preload/index.ts`, `console/test/unit/spend-controls.test.ts`, `console/test/unit/ipc.test.ts`, `console/test/e2e/spend.spec.ts`

**Steps.**

1. **Wire the range picker and the breakdown switch to `report --json` reads**, through the **existing** `runReport` operation (the design's §5.1 rule 5 names it) and `FugaroSource`'s allow-listed read — **no new write, no new spawn-table entry**: the spawn table's `report --json` row (design §5.1 rule 6: `--by …`, `--since`, `--until`, `--repo`, never `--csv`) already admits exactly these flags, and `internal/cli/report.go` defines them (`--since` is `YYYY-MM-DD` UTC or `Nd`, `--until` is `YYYY-MM-DD`, `--by` is `day|week|month|year|repo|model|person`). The screen sends `--since`/`--until` from the picker and `--by` from the switch (`day`, `week`, `month`, `repo`, `model`, `person`, the six Task 2.2 draws) and re-renders from the new document; a range beyond the retained window renders as a gap, not a zero (Task 2.2's rule).
2. **The IPC allow-list's one documented extension, spelled out, and nothing beyond it.** **No new channel name is added to the preload object** (Task 1.2's frozen, versioned object keeps exactly its operations). The **existing `runReport` operation's closed argument schema**, validated in the main process, gains **three** fields and no others: `since` (a string matching `^\d{4}-\d{2}-\d{2}$` or `^\d+d$`), `until` (a string matching `^\d{4}-\d{2}-\d{2}$`) and `by` (an enum of exactly the six values above). **`--repo` is unchanged. There is no `--csv`, no path, no free-form string, no command and no URL.** The main process builds the argv itself from the validated values, in the table's order; the renderer never supplies argv. This is the only edit to `ipc.ts`/`preload/index.ts`, and the **never-reachable table of Task 1.2 is re-run unchanged**.
3. **The person breakdown stays OFF by default, with an explicit operator switch** (design §2.3 and §6.2, as Task 2.2 states). The person option is not offered by the breakdown switch until the operator turns the separate switch on; **turning it on is a renderer-side view choice that sends the same `runReport` with `by` = `person` and nothing more**, the setting is per session unless the owner decides otherwise, and **the fixture still contains the person data** so the test is meaningful.

**Tests.**

- `spend-controls.test.ts` — changing the range changes the `--since`/`--until` of the next `report --json` spawn and nothing else; changing the switch changes `--by` only; **the person view is not on screen, and no spawn carries `--by person`, until the operator turns the switch on** (asserted on the screen model and on the spawn log, with the mutation being a default of `on`); a range or `by` value outside the schema is refused before spawn; the spend model still never adds `NOTIONAL~` to a real total after a re-read.
- `ipc.test.ts` — the `runReport` schema accepts exactly the three fields and refuses a fourth (`csv`, `path`, `repo` as an array, an unknown `by`); the preload object is still frozen and exposes **no channel that was not there before this task** (a diff against the frozen operation list); the never-reachable table still passes.
- `spend.spec.ts` — the person view is absent in the default golden and present after the switch is turned on; a range change redraws the series.

**Command:** `npm run test:unit && npm run test:e2e -- spend`

**Depends on:** Task 2.2 (the screen that draws the controls), and Task 1.2 (the allow-list it extends) already merged.

**Commit:** `console: wire the spend screen's range and breakdown controls through the allow-listed report read`

**Reviewer tier:** the same as Task 2.2 (a standard review of the screen), **plus: this task edits the IPC allow-list's schema (`ipc.ts`, `preload/index.ts`), so it is SECURITY-SENSITIVE and gets an independent opus review** on the diff, by a reviewer that did not write it (Global Constraint 9). The reviewer should try to widen the schema and to reach a command through `by`/`since`/`until`.

**Red step — `Expected: FAIL`** — the person-view test against a default that shows it, and the schema test against a `runReport` that accepts a fourth field.

---

## Phase 3 — When things are missing

### Task 3.1: Degraded, stale and absent

**Files:** `console/src/source/cli.ts`, `console/src/renderer/screens/*.ts`, `console/test/e2e/degraded.spec.ts`, `console/test/fixtures/degraded.ndjson`

**Steps.** Implement the design's §6.3 table in full. **Stale**: no live event for 45 s (the design's threshold, matching `watch`'s `StaleAfter`) puts the banner up, breaks the graphs rather than interpolating, and keeps the last-known values with their timestamps. **No Firebase**: the CLI's own no-Firebase mode, which polls `ls` every 10 s with each run's cost from `result.json`, no caps and the kill keys disabled — the console shows caps as **unavailable** and hides the kill affordances, and never draws them as zero. **No `fugaro`**: one explanatory screen naming what is needed, the install command and a binary picker — not a partially drawn dashboard. **A refused read**: the CLI's own message verbatim, including the one-line remedy `report` prints (which names `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`).

**Tests:** `degraded.spec.ts` — each of the four conditions renders as designed; the caps are `n/a` and not `0`; a permission refusal shows the CLI's text unchanged; the binary-absent screen is reachable with no `fugaro` on `PATH`.

**Command:** `npm run test:e2e -- degraded`

**Depends on:** Task 2.3.

**Commit:** `console: what it shows when a source is missing, and what it refuses to invent`

**Red step — `Expected: FAIL`** — the caps-are-not-zero assertion against a renderer that defaults to `0`.

### Task 3.2: The version skew handshake

**Files:** `console/src/shared/version.ts`, `console/src/main/window.ts`, `console/test/unit/version.test.ts`, `console/test/e2e/skew.spec.ts`

**Steps.** The design's §7.1 and D7: the console carries the CLI version it was built and tested against plus the previous minor as its supported range; at startup it runs `fugaro version` (the **subcommand** — the root command sets no cobra `Version` field, so `fugaro --version` fails) and either proceeds, proceeds in a reduced mode with a banner naming both versions, or — below a hard floor — refuses to draw a live dashboard rather than misreport spend. If `version` turns out to have no `--json` form, parse its human line and **pin that parse with a test against the real output**, which is why this task also settles the design's UNVERIFIED item 3.

**Tests:** `version.test.ts` — the range check's boundaries (the built minor, the previous one, one below the floor, one above the ceiling), and the human-line parse against a captured `fugaro version` output. `skew.spec.ts` — the banner appears, the reduced mode hides the views it cannot support, and the below-floor case shows the refusal screen and no dashboard.

**Command:** `npm run test:unit && npm run test:e2e -- skew`

**Depends on:** Task 3.1.

**Commit:** `console: the version skew handshake`

**Red step — `Expected: FAIL`** — the below-floor test against a console that draws the dashboard anyway.

### Task 3.3: The handshake wired at startup, and the mode told to the renderer — SECURITY-SENSITIVE, independent opus review

**Why this task exists.** Task 3.2 (#514, `8ee5d4e0` on `console`) built the handshake and left it uncalled, as its review found: `console/src/main/index.ts` still calls `createConsoleWindow` with no handshake; nothing under `src/` calls `startupWindow`; `createRefusalWindow(deps, compatibility)` delegates to the same `buildConsoleWindow`, which loads `APP_ENTRY_URL`, so a refusal window would load the dashboard document; and no channel tells the renderer the mode. D7 (**refuse below the floor rather than misreport spend**) is therefore not enforced anywhere a user can see. **Owner ruling 2026-10-17: the handshake must be wired at startup before the console MVP ships with 0.8.5.** This task is that wiring. It adds **no new policy and no new model**: the range, the floor, the parse and the type are Task 3.2's, and this task builds on them rather than replacing them.

**What Task 3.2 already provides and this task reuses unchanged** (`console/src/shared/version.ts` and `console/src/main/window.ts`): `CliCompatibility` (`mode: 'ok' | 'reduced' | 'refused'`, `skew`, `cliVersion`, `consoleVersion`, `supported`, `floor`, `hidden: ReducedView[]`, optional `because`), `REDUCED_VIEWS` (`'spend' | 'caps' | 'kill'`), `hidesView(compatibility, view)`, `classifyCliVersion`, `unreadableCompatibility`, `renderVersionSkew`, `runVersionHandshake(deps)`, `startupWindow(deps): Promise<{ window: BrowserWindow | null; compatibility }>` (`window` is `null` when refused: no window is constructed) and `createRefusalWindow(deps, compatibility): Promise<BrowserWindow>`. **There is no `shared/mode.ts`, no `StartupMode`, no `parseStartupMode`, no `isViewHidden` and no `hiddenViews`**: an earlier draft of this task invented them; `CliCompatibility.hidden` and `hidesView` are the same facts and stay the only vocabulary.

**Files.**

| File | New or existing | What changes |
|---|---|---|
| `console/src/main/index.ts` | existing | `main()` runs `startupWindow` (with a `readVersion` built on `resolveBinary` and `probeVersion` from `spawn.ts`) as its first step after `serveBundle()`; `ok` and `reduced` keep the window `startupWindow` returned; `refused` calls `createRefusalWindow` and **never `createConsoleWindow`**. The `activate` handler reuses the stored compatibility instead of re-running a dashboard path. |
| `console/src/main/window.ts` | existing | `createRefusalWindow` loads a **distinct** refusal document (a second `app://` entry, `REFUSAL_ENTRY_URL` in `protocol.ts`, served by the same handler under the same CSP and navigation rules) and no longer `APP_ENTRY_URL`; `buildConsoleWindow` takes the URL as a parameter. |
| `console/src/main/protocol.ts` | existing | exports `REFUSAL_ENTRY_URL`; the root check still serves nothing outside the bundle root. |
| `console/src/renderer/refusal.html` | **new** | the refusal document: no dashboard root, no feed script. |
| `console/src/main/ipc.ts` | existing | one new allow-list row, `startup.compatibility`, read-only, no arguments; its handler returns the stored `CliCompatibility` after a **closed-schema check** (unknown `mode`, a `hidden` entry that is not in `REDUCED_VIEWS`, a non-string `cliVersion` or a `mode`/`hidden` pair that disagrees with `hidesView` returns `unreadableCompatibility` with the console's own sentence, never the raw value). |
| `console/src/preload/index.ts` | existing | one more named member of the frozen object, `getCompatibility()`; no generic invoke. |
| `console/src/renderer/app.ts` | existing (the renderer entry; **there is no `renderer/index.ts`**, and `index.html` is the document) | before it mounts any screen or subscribes to any feed it awaits `getCompatibility()`: `refused` mounts nothing but the refusal screen (`renderVersionSkew`), `reduced` mounts the banner and **routes every `hidesView` id to the banner's explanation, not to the view**, `ok` mounts as today. A rejected or malformed answer is treated as `refused`. |
| `console/src/shared/version.ts` | existing | **no behaviour change**; `renderVersionSkew` stays here (Task 3.2's rule: `shared/` draws the skew state and depends on nothing in `renderer/`). The refusal document's script calls it; no `renderer/screens/refusal.ts` is created. |
| `console/test/unit/startup.test.ts` | **new** | the wiring (below). |
| `console/test/unit/ipc.test.ts` | existing | the allow-list extension and its mutation-proof half. |
| `console/test/unit/window.test.ts` | existing | **extended**, not duplicated: the existing `startupWindow` and `createRefusalWindow` describes keep their assertions, and a new one pins that the refusal window's URL is `REFUSAL_ENTRY_URL` and never `APP_ENTRY_URL`. |
| `console/test/e2e/startup-skew.spec.ts` | **new** | the real app, started the way a user starts it. It differs from the existing `skew.spec.ts` (which loads the compiled renderer and calls `renderVersionSkew` through `executeJavaScript`, so it proves the *drawing*) and from `version.test.ts` (the parse and range): this one launches the Electron app with a stub `fugaro` and proves the *wiring*, that a refused version never produces a dashboard. |
| `console/test/fixtures/bin/fugaro` | **new** | a stub executable that prints the line the spec asks for. **There is no `FUGARO_BIN` and this task does not add one**: `spawn.ts`'s `binDirectories` resolves `fugaro` from a configured path, the well-known prefixes and then each absolute `PATH` entry, so the spec launches Electron with `PATH` set to the stub's directory (and an empty configured path). An override env var would be a new way to choose which binary the console runs and is **not** part of this task; if the owner wants one it is its own SECURITY-SENSITIVE change with its own review note. |

**Steps.**

1. **The handshake runs before any dashboard window exists.** As in the `index.ts` row. A handshake that cannot run (no binary, a spawn error, no parsable line) yields `unreadableCompatibility`, which is `refused`, never `ok`; `runVersionHandshake` already guarantees this and the startup path adds no other branch.
2. **The mode reaches the renderer by one read-only, argument-free, schema-checked channel in the frozen allow-list**, per Task 1.2's rules (`Object.isFrozen`, no generic invoke, no channel from the renderer). The renderer asks once at load and draws nothing until it has an answer.
3. **The refusal screen is console-owned text.** It draws `renderVersionSkew`'s output for a `refused` compatibility: the console's own sentence and remedy. The only CLI-chosen strings, the version, pass through `sanitizeText` inside `version.ts` already; a stub printing control characters, an ANSI escape, markup or a 10 000-character line must leave none of them live on the screen, and the raw unparsable line is never carried (Task 3.2's rule).
4. **The renderer independently refuses to draw hidden views and refuses to draw at all without an answer.** In `refused` it subscribes to no feed, so even a refusal window that regressed to the dashboard document draws no spend. The defence is in both places on purpose.

**The renderer entry is Task 3.4's, not this task's (added 2026-10-17).** This task builds steps 2 and 4 as library code and wires the startup handshake. Attaching them to the real windows is **Task 3.4**: the preload on the dashboard window, `registerConsoleHandlers` after the handshake (never in `refused`), the script the document loads, and the installation the handlers read from. Any sentence elsewhere in this task that calls that wiring "owned by no task" (the review-round-2 note of PR #541) is answered by Task 3.4.

**Tests.**

- `startup.test.ts`: the startup path calls `startupWindow` before any `createConsoleWindow` (call order on spies); on `refused`, `createConsoleWindow` is never called and `createRefusalWindow` is; a reader that throws or returns nothing parsable ends `refused`.
- `ipc.test.ts` (mutation-proof half): `startup.compatibility` is in the allow-list, read-only and takes no argument; the never-reachable table still holds; **adding it with an argument, or any write verb, fails a named test**; the schema check rejects an unknown mode, an unknown view id and a `mode`/`hidden` pair that disagrees with `hidesView`; the preload object stays frozen.
- `window.test.ts`: as in the Files table.
- `startup-skew.spec.ts` (fails first): with the stub's directory as the only `PATH`, **a stub printing an old version line (`0.5.0`) shows the refusal screen and never the dashboard** (no `live` root in any window, exactly one window), **so do stubs printing an odd line** (`banana`, an empty line, a hostile line with an escape sequence and markup, a very long line); a stub inside the range shows the dashboard; a stub in the degraded band shows the banner and none of the `hidesView` screens.

**Command:** `npm run test:unit && npm run test:e2e -- startup-skew`

**Depends on:** Task 3.2 (**hard**: #514 merged, which it now is on `console` at `8ee5d4e0`; this task edits the `startupWindow` and `createRefusalWindow` it introduced) and Task 1.2 (the allow-list it extends).

**Commit:** `console: the version handshake runs at startup, and a refused version never draws the dashboard`

**Red step - `Expected: FAIL`** - `startup-skew.spec.ts`'s old-version case against the tree as Task 3.2 leaves it (`index.ts` with no handshake: the dashboard draws); and `ipc.test.ts`'s allow-list test against an entry with an argument.

**Not in this task, and owned by no task yet (review round 2, 2026-10-17).** Steps 2 and 4 are built and tested as library code — `startup.compatibility` in the allow-list, `startupCompatibility` and its closed-schema check in `src/main/ipc.ts`, `getCompatibility` on the frozen preload object, and `startConsole`'s gate in `src/renderer/app.ts` — but **nothing attaches them to the real windows**: `consoleWebPreferences()` sets no `preload`, nothing under `src/main/` calls `registerConsoleHandlers` or `startupCompatibility`, and neither `index.html` nor `refusal.html` loads a script, so `startConsole` runs only in the unit tests and in the e2e specs that inject the compiled renderer. **The production app today draws a static document in either window: no screen is mounted and no spend figure is drawn anywhere**, which is why D7 holds at the document level (a refused console opens `REFUSAL_ENTRY_URL`, which has no dashboard root) while the renderer half of the defence is not yet live. No task in this plan owns that wiring: Task 1.1 defers the preload to Task 1.2; Task 1.2's files are `ipc.ts`, the preload and its test, and it neither sets `preload` on the window nor registers the handlers; Tasks 2.1 to 2.4 each leave "the entry point" to "a later task" without naming one; Task 4.1 decides where a packaged console keeps its renderer files, not how a document loads a script. It is not done here because this task's Files table names none of it, `registerConsoleHandlers` needs all thirteen handlers (twelve of them reads that need a `FugaroSource` bound to an installation, which D13's chooser has not built), and a document that loads no script gives a preload nothing to serve. **Obligation, for a task the owner must add before the console MVP ships (proposed: Task 3.4, "the renderer entry", SECURITY-SENSITIVE):** a document-loadable renderer entry (the bundling-or-ESM decision `tsconfig.build.json` defers, under the pinning rule); the preload set on the dashboard window only; `registerConsoleHandlers` called only after the handshake answered `ok` or `reduced`, **never in `refused`**; `startConsole` as the entry's first call; and, as defence in depth, the main process refusing the spend, caps and kill channels in `reduced` mode — with the CSP and the hardened flags unchanged.

**Status: approved by the owner 2026-10-17.** The owner's go ("yes, start") came on 2026-10-17, so the line this replaces -- implementation waiting for approval -- no longer holds; nothing else in this task changed with it. It remains a **release gate for the console MVP** (Task 4.6 and "What ships with the MVP"): the console does not ship with 0.8.5 without it.

---

### Task 3.4: The renderer entry: the preload, the handlers, the installation picker — SECURITY-SENSITIVE, independent opus review

**Status: approved by the owner 2026-10-18.** The owner's go ("go on task 3.4") came on 2026-10-18, so the line this replaces -- implementation waiting for approval -- no longer holds; nothing else in this task changed with it. **3.4a is implemented** (the preload, the handlers and the document's script); 3.4b and 3.4c are approved and not yet written. C8, C9 and C10 are answered (the decisions table below). It remains a **release gate for the console MVP**, like Tasks 3.2 and 3.3 (Task 4.6 and "What ships with the MVP"): without it the console has nothing to show in production.

**Why this task exists.** As of `console` with PR #541 (Task 3.3) on top, the console cannot show a real dashboard. An independent review found this, and the Task 3.3 fixer confirmed it:

- **No window sets a preload.** `consoleWebPreferences()` (`console/src/main/window.ts`) has no `preload` key, so `window.fugaro` never exists in a real window.
- **Nothing in `console/src/main/` calls `registerConsoleHandlers` or `startupCompatibility`.** Every `fugaro:console/*` channel is unanswered.
- **Neither `index.html` nor `refusal.html` loads a script.** `startConsole` (`console/src/renderer/app.ts`) runs only in unit tests and in e2e specs that inject the compiled renderer with `executeJavaScript`. The CommonJS `dist/` that `tsc` emits cannot be loaded by a sandboxed document anyway.
- **No task owns any of it.**
  - Task 1.1 defers the preload to Task 1.2.
  - Task 1.2's files are `ipc.ts`, the preload and its test. It neither sets `preload` nor registers handlers.
  - Tasks 2.1 to 2.3 (and the spend-controls follow-up, #516) each leave "the entry point" to an unnamed later task (`console/src/renderer/screens/spend.ts`'s header says so in as many words).
  - Tasks 3.1 to 3.3 build states and a gate that nothing mounts.
  - Task 4.1 (#540) packs `src/renderer/index.html` and records that "whichever task does it has to put the compiled file inside the served root".
- **The handlers have no installation to read from.** `registerConsoleHandlers` needs all thirteen handlers, and twelve of them need a `FugaroSource` bound to an installation (D13). `createInstallationStore` (`console/src/source/cli.ts`) is in memory only, and no UI calls it.

The app as built therefore draws a static sentence in either window. D7 holds at the document level, but no spend figure, run or cap is ever drawn from a real CLI. This task is that wiring, and it is the owner of everything Task 3.3's own text calls "not in this task".

**What it reuses unchanged.** It adds no new policy:

- the IPC allow-list and its argument schemas: `CONSOLE_OPERATIONS`, `validateArguments`, `registerConsoleHandlers`, `assertReachableSurface` and `isConsoleSender` (`src/main/ipc.ts`);
- the frozen bridge: `EXPOSED_OPERATIONS`, `createConsoleBridge` and `exposeConsoleBridge` (`src/preload/index.ts`);
- `handlersFor` and `CliBackedHandlers` (`src/source/source.ts`);
- `CliSource`, `verifyCandidate`, `NoInstallation` and `noInstallationState` (`src/source/cli.ts`);
- the spawn table, `resolveBinary`, `probeVersion`, `isExecutableFile` and `privateWorkingDirectory` (`src/main/spawn.ts`);
- the handshake: `runVersionHandshake`, `openWindowFor` and `createConsoleLifecycle` (`src/main/window.ts`);
- the compatibility type: `CliCompatibility`, `REDUCED_VIEWS` and `hidesView` (`src/shared/version.ts`);
- the gate: `startConsole` and `compatibilityFrom` (`src/renderer/app.ts`);
- the screens' `build*Model` and `render*` functions;
- the CSP and the `app://` handler (`src/main/protocol.ts`).

**The CSP does not change.** Both the header and the meta copy already carry `script-src 'self'`, which is exactly what a bundled script file served from `app://console/` needs. **The CSP delta of this task is none.** A test pins that (3.4a), so a later "just add `'unsafe-inline'`" fails a test rather than passing a review.

**Decisions this task needs from the owner** (rows C8 to C10 of "Decisions this plan defers to the owner"):

- **C8: may the installation list be persisted?** D13 says "the choice is the app's own setting" and "stores the tuples". Design §5.1 rule 10 and §7.4 say the app writes nothing to disk but the opt-in spawn log, which is why `createInstallationStore` is in memory today. The two cannot both stand.
  - **Recommendation: persist.** Use one JSON file in Electron's `userData` directory, written only when the operator adds, chooses or removes an installation. 3.4b's first step amends §5.1 rule 10, §7.4 and D20's "the only thing the app ever writes" to name it.
  - If the owner rules against, 3.4b keeps the in-memory store and the picker opens on every launch. Nothing else in this task changes.
- **C9: where does switching happen?** D13 says "a picker in the header". This task keeps the dashboard's preload read-only, so it proposes otherwise:
  - the header **names** the active installation (project, binary path, the version the handshake read, checkout);
  - **choosing, adding and removing** happen in a separate picker window opened from the application menu (`Installations…`, built in the main process). That window has its own document and its own preload.
  - **Recommendation: accept.** It is the only shape in which the dashboard's bridge carries no write to the app's own state.
- **C10: the kill fields in reduced mode.** In `reduced` mode `hidden` is all three views (`classifyCliVersion`). `spend` and `caps` have channels of their own (`spend.report`, `budget.show`) that the main process can refuse. `kill` does not: the kill state also arrives inside `live.watch`'s document (`JSONKill`).
  - **Recommendation: do not strip.** Rewriting a CLI document in the main process would be a second schema. The renderer's gate (`drawScreen('kill', …)` answering `withheld`) stays the one defence for the kill banner inside the Live screen. That limit is stated here rather than discovered later.

**The split: three PRs, and the files each one touches.** Size estimates count source and tests together, from the size of comparable merged tasks (#541 is 2,800 lines across 29 files):

| Part | What | Estimate | Depends on |
|---|---|---|---|
| **3.4a** | preload set on the dashboard window, handlers registered after the handshake, reduced-mode refusal, the renderer bundle and its document wiring, CSP and preload-surface tests | ~1,100 to 1,400 lines, ~14 files | Task 3.3 (**hard**: #541 merged) |
| **3.4b** | the installation store, its validation, the per-installation handshake, the picker window and its preload, the header's `installation.current` read, the design amendment | ~1,600 to 2,000 lines, ~16 files | Task 3.3 (**hard**). C8 and C9 answered. Task 3.4a (**hard** for the picker document, its bundle and the sender narrowing). The store, validation and handshake commits may be written **in parallel with 3.4a** and rebased onto it. |
| **3.4c** | the screens wired to the real source, live polling, the stub CLI extended, the end-to-end proof | ~1,200 to 1,500 lines, ~10 files | Tasks 3.4a and 3.4b (**hard**) |

**Shared files.** An `x` marks a file the part edits; `new` marks a file it creates. Rows touched by more than one part are the serialization points, and the open PRs that touch the same file are named, so the merge order is visible before anyone starts:

| File | 3.4a | 3.4b | 3.4c | Also touched by an open PR |
|---|---|---|---|---|
| `console/src/main/index.ts` | x | x | x | #541 |
| `console/src/main/window.ts` | x | x | | #541 |
| `console/src/main/ipc.ts` | x | x (sender narrowing, `installation.current`) | | #540, #541, #548 |
| `console/src/main/protocol.ts` | x (served root, served-file list) | x (picker entry URL) | | #541 |
| `console/src/main/installations.ts` | | new | | |
| `console/src/preload/index.ts` | x (comment only, the surface is unchanged) | x (`installation.current`) | | #541 |
| `console/src/preload/installations.ts` | | new | | |
| `console/src/renderer/entry.ts` | new | | x | |
| `console/src/renderer/index.html` | x (one script tag) | | | |
| `console/src/renderer/refusal.html` | x (comment only: still script-free) | | | #541 |
| `console/src/renderer/installations.{html,ts}` | | new | | |
| `console/src/renderer/app.ts` | | | x (only if `startConsole`'s deps need widening) | #541 |
| `console/src/source/cli.ts` | | x (the store's persistence seam) | x (the source factory) | |
| `console/tools/bundle-renderer/index.ts` | new | x (second entry) | | |
| `console/package.json` | x (`build` script, `build.files`) | x (`build.files`) | | #540 |
| `console/test/fixtures/bin/fugaro`, `console/test/support/cliStub.ts` | | | x | #541 |
| `console/test/unit/*` | new `preload-surface`, `csp-document`, `bundle`; extended `startup`, `window`, `ipc` | new `installations-store`, `installations-ipc`; extended `configread`, `ipc` | new `entry-wiring` | #540, #541, #548 |
| `console/test/e2e/entry.spec.ts` | new | x | x | |
| `docs/design/desktop-console.md` | | x (§5.1 rule 10, §7.4, D13, D20; only after C8 is answered) | | |

#### 3.4a: The preload, the handlers and the document's script

**Files:** `console/src/main/{index,window,ipc,protocol}.ts`, `console/src/renderer/entry.ts`, `console/src/renderer/index.html`, `console/src/renderer/refusal.html` (comment only), `console/tools/bundle-renderer/index.ts`, `console/tsconfig.build.json`, `console/package.json` (`build` script and `build.files`, never `dependencies`), `console/test/unit/{preload-surface,csp-document,bundle,startup,window,ipc}.test.ts`, `console/test/e2e/entry.spec.ts`

**Steps.**

1. **The preload is set on the dashboard window and nowhere else.** `buildConsoleWindow`'s dashboard path uses `dashboardWebPreferences(preloadPath)`: `consoleWebPreferences()` plus exactly one key, `preload`, an absolute path inside `app.getAppPath()` (`dist/preload/index.js`).
   - `createRefusalWindow` keeps `consoleWebPreferences()` with no `preload` key at all.
   - The bridge surface is **exactly what Task 3.3 ships**: the thirteen `EXPOSED_OPERATIONS` (eleven CLI reads, the validated `link.openExternal` of design §5.1 rule 2, and `startup.compatibility`), plus `version` and `operations`. The object is frozen and has no generic invoke.
   - **No event emitter crosses the bridge.** There is no `on`, `once`, `addListener`, `send` or callback-taking method. Live updates are polls of `live.watch` (3.4c), and a push channel would be a SECURITY-SENSITIVE task of its own.
   - Electron's sandboxed preload can `require` only `electron` and a handful of built-ins, never a relative path. So the compiled `dist/preload/index.js` must require nothing but `electron`. Today it imports only types, and a test keeps it that way.
2. **The handlers are registered after the handshake, never in `refused`.** `createConsoleLifecycle` gains an `onDecided(compatibility)` step that runs **after** `runVersionHandshake` answers and **before** the dashboard window's `loadURL`.
   - For `ok` and `reduced` it calls `registerConsoleHandlers(ipcMain, gated)` **once per process**. Electron's `ipcMain.handle` throws on a second registration, so an `activate` must not repeat it.
   - For `refused` it registers nothing, ever, including on a later `activate`.
   - `gated` is `{ ...handlersFor(activeSource, deps), 'startup.compatibility': startupCompatibility(() => current) }`. `activeSource` is an indirection that answers the console's own `NO_ACTIVE_INSTALLATION` refusal until 3.4b supplies an installation. The surface is complete from the first registration, as `registerConsoleHandlers` demands.
3. **Reduced mode is refused in the main process as well as in the renderer** (defence in depth). `src/main/ipc.ts` gains:
   ```ts
   export const REDUCED_VIEW_OPERATIONS: Readonly<Record<ReducedView, readonly OperationName[]>> =
     Object.freeze({ spend: ['spend.report'], caps: ['budget.show'], kill: [] })
   export const REDUCED_REFUSAL = 'this console withholds that view for the fugaro it is driving'
   export function reducedModeHandlers(handlers: ConsoleHandlers, current: () => CliCompatibility): ConsoleHandlers
   ```
   A wrapped handler whose operation belongs to a view in `current().hidden` answers `REDUCED_REFUSAL` **without calling the source**.
   - `kill: []` is C10's recorded limit, and a test names it, so filling it in later is a deliberate edit.
   - Every wrapped handler also refuses outright when `current().mode` is `refused`. This is a second guard behind the registration rule.
4. **The document loads one bundled script from its own origin, with no inline script and no CSP change.**
   - **The bundle.** `tsc` stays the compiler (the pinning rule allows no bundler dependency, `test/unit/pinning.test.ts`). `console/tools/bundle-renderer/index.ts` is a dependency-free TypeScript step run by `npm run build` after `tsc`. It walks the compiled CommonJS closure of `dist/renderer/entry.js` by its **string-literal relative** `require` calls only, and fails the build on any other `require` (a bare specifier, a computed one, `electron`, a Node built-in). It wraps each module in a function inside one registry and writes `dist/bundle/console.js`.
   - This is the shape the e2e specs' hand-built `MODULES` registries already use, now produced by the build rather than by each spec. It contains no `eval` and no `new Function`, and none would run anyway, because the CSP has no `'unsafe-eval'`.
   - **The served root.** The step copies `index.html` and `refusal.html` verbatim into `dist/bundle/`, and `rendererRoot` (`src/main/index.ts`) becomes `dist/bundle`, so the served root holds **exactly** the files named in a `SERVED_FILES` list in `protocol.ts` and nothing compiled beside them. `build.files` (Task 4.1, #540) names those files in place of `src/renderer/index.html`. Note that #540's list today omits `refusal.html`, which #541 adds; whichever of the two merges second must add it.
   - **The document.** `index.html` gains exactly `<script src="./console.js" defer></script>` and nothing inline. `refusal.html` stays script-free.
   - **The rejected alternative.** Native ES modules (`<script type="module">`) would need every relative import in `src/shared/` and `src/renderer/` rewritten with an extension, and a second `tsc` configuration. That is recorded here as rejected rather than left for the implementer to rediscover.
5. **The entry's first call is `startConsole`.** `src/renderer/entry.ts` reads `globalThis.fugaro`, hands it to `startConsole({ bridge, mount, factory, theme })` and **draws nothing before that promise settles**.
   - A missing bridge (`window.fugaro === undefined`) is treated as `refused`, exactly as `compatibilityFrom` treats a rejected call.
   - In 3.4a the entry stops at the gate: the refusal or the banner, then an empty dashboard frame that names no figure. The screens are 3.4c's.

**Interfaces.**

- `dashboardWebPreferences(preloadPath: string): WebPreferences` in `window.ts`.
- `ConsoleLifecycleDeps` gains `onDecided(compatibility: CliCompatibility): void`.
- `REDUCED_VIEW_OPERATIONS`, `REDUCED_REFUSAL` and `reducedModeHandlers` in `ipc.ts`.
- `SERVED_FILES: readonly string[]` in `protocol.ts`.
- `bundleRenderer(options: { entry: string; out: string }): void` in `tools/bundle-renderer/index.ts`.
- `bootDashboard(scope: { readonly fugaro: unknown; readonly document: Document }): Promise<void>` in `entry.ts`.

**Tests.**

- **`preload-surface.test.ts`** (mutation-proof). `exposeConsoleBridge` is run against a recording `contextBridge` and a sentinel `ipcRenderer`, and the test asserts:
  - exactly one key is exposed, `fugaro`;
  - the object is frozen, and its own keys equal the `SHIPPED_SURFACE` literal plus `version` and `operations`, no more and no fewer;
  - a deep walk of the exposed value never reaches the sentinel (so leaking `ipcRenderer`, or any object holding it, fails);
  - no member is named `invoke`, `send`, `sendSync`, `on`, `once`, `addListener` or `removeListener`;
  - every method's reply goes through the envelope check (`isConsoleReply`) before the renderer sees it.

  Separately, it reads the **built** `dist/preload/index.js` and asserts its only `require` is `require('electron')`.
- **`csp-document.test.ts`.** The test asserts:
  - `CONTENT_SECURITY_POLICY` equals a pinned literal, Task 1.1's string byte for byte (**the delta is none**);
  - its `script-src` is exactly `'self'`, with no `'unsafe-inline'`, `'unsafe-eval'`, `'wasm-unsafe-eval'`, `'strict-dynamic'`, nonce, hash, `data:`, `blob:`, `*` or scheme source;
  - every served HTML file's meta copy is byte-identical to the header;
  - each document's script elements are exactly its expected list: `index.html` has `./console.js` alone, and `refusal.html` has none;
  - no document has inline script text, an `on*=` attribute or a `javascript:` URL.
- **`bundle.test.ts`.** The bundle exists after `npm run build`, holds the entry's whole relative closure, and contains no `eval(`, `new Function` or `require('electron')`. The bundler refuses a planted computed `require` and a bare specifier. The served root's listing equals `SERVED_FILES`.
- **`startup.test.ts`** (extended), with an `IpcMainLike` spy and a source spy:
  - for `ok` and `reduced`, the handshake resolves, then `handle` is called for every channel, then the window loads (call order on spies);
  - for `refused`, `handle` is never called, including after `activate`;
  - two `activate`s register nothing twice;
  - in `reduced`, `spend.report` and `budget.show` answer `REDUCED_REFUSAL` and the source spy is not called, while `live.watch` reaches the source.
- **`window.test.ts`** (extended). The refusal window's `webPreferences` has no `preload` key. The dashboard's keys are exactly `consoleWebPreferences()`'s plus `preload`, and the path is absolute and inside the app path.
- **`entry.spec.ts`** (e2e, the stub CLI of Task 3.3):
  - **In range:** the dashboard window's URL is `APP_ENTRY_URL`, `console.js` was requested from `app://console/`, `typeof window.fugaro === 'object'` and `Object.isFrozen(window.fugaro)`, its keys equal `SHIPPED_SURFACE`, `window.require`, `window.process` and `window.ipcRenderer` are all undefined, and an inline `<script>` added through the DOM does not run and raises a `securitypolicyviolation`.
  - **Refused** (stub prints `0.5.0`): exactly one window, `REFUSAL_ENTRY_URL`, `window.fugaro === undefined`, no `<script>` element, and the spawn log (`FUGARO_CONSOLE_SPAWN_LOG` in a scratch directory) holds `version` spawns only. That is the evidence that no handler ran a read.
  - **Reduced:** `await window.fugaro.runReport({})` called directly from the page answers `REDUCED_REFUSAL`, and the spawn log holds no `report` argv. This proves the main-process refusal independently of the renderer's gate.

**Command:** `npm run build && npm run test:unit && npm run test:e2e -- entry`

**Depends on:** Task 3.3 (**hard**: #541 merged; this task edits its lifecycle, its refusal document and its stub harness). Task 4.1 (#540) is **soft**: whichever merges second edits `build.files`.

**Commit:** `console: the dashboard gets its preload, its handlers and its script, and a refused console gets none of them`

**Red step — `Expected: FAIL`.** Run each test against the tree as Task 3.3 leaves it, then against a mutation:

- `entry.spec.ts`'s in-range case against the tree as Task 3.3 leaves it (no preload: `window.fugaro` is undefined);
- `preload-surface.test.ts` against a bridge with an added `ipcRenderer` member, and against a fourteenth channel;
- `csp-document.test.ts` against `'unsafe-inline'` added to `script-src`, against an inline `<script>` in `index.html`, and against a `<script>` added to `refusal.html`;
- `startup.test.ts` against a lifecycle that registers handlers before the handshake, and against one that registers them in `refused`.

#### 3.4b: The installation store and the picker

**Files:** `console/src/main/installations.ts`, `console/src/main/{index,window,ipc,protocol}.ts`, `console/src/preload/installations.ts`, `console/src/preload/index.ts`, `console/src/renderer/installations.{html,ts}`, `console/src/source/cli.ts`, `console/tools/bundle-renderer/index.ts`, `console/package.json` (`build.files`), `console/test/unit/{installations-store,installations-ipc,configread,ipc,startup}.test.ts`, `console/test/e2e/entry.spec.ts`, `docs/design/desktop-console.md` (only once C8 is answered)

**Steps.**

1. **Where the tuples live (C8).** In `<userData>/installations.json`, where `<userData>` is `app.getPath('userData')`.
   - The file is mode `0600` in a `0700` directory and written atomically (a temporary file in the same directory, then `rename`).
   - It is written **only** when the operator adds, chooses or removes an installation. It is read once at startup and never sent anywhere.
   - Its schema is closed and versioned:
     ```ts
     export interface StoredInstallation {
       readonly id: string            // 16 random hex characters, the app's own
       readonly project: string       // isSafeInstallationValue, at most 200 characters
       readonly binaryPath: string    // absolute, at most 1024 characters
       readonly configPath?: string   // absolute; a STRING passed as --config, never opened
       readonly checkout?: string     // absolute directory, the child's cwd (§3.4 rule 5)
     }
     export interface InstallationFile {
       readonly version: 1
       readonly active: string | null // an id in installations, or null
       readonly installations: readonly StoredInstallation[]
     }
     ```
   - It is bounded: at most `MAX_INSTALLATIONS = 16`, and a read is refused past 64 KiB.
   - Any other key, wrong type, duplicate id or dangling `active` refuses **the whole file**. Then the console starts with no installation, says so in its own sentence naming the path, **does not overwrite or delete the file**, and refuses an add until the operator has moved it.
   - **It never holds a credential, an environment or anything the CLI printed.** The version is not stored either: it is re-read by the handshake at every launch and every choose.
   - **It is never inside the operator's CLI config.** On Linux `userData` is `~/.config/<app name>`, so an app named `fugaro` would land in `~/.config/fugaro`, the CLI's own directory. The store therefore refuses a path under `~/.config/fugaro`, `$XDG_CONFIG_HOME/fugaro` or `$FUGARO_CONFIG`'s directory, and a test pins the app name (`fugaro-console`, `package.json`'s `name`) to a `userData` outside all three.
   - `createInstallationStore` gains a persistence seam (`load`/`save` dependencies) so its verification logic stays the one in `cli.ts`.
2. **How a tuple is validated**, in this order, stopping at the first refusal and storing nothing:
   1. the schema;
   2. the binary: absolute, exists, a regular file, executable (`isExecutableFile`);
   3. the config path, when given: absolute and an existing file, checked with `stat` only and **never opened** (§3.4 rule 7);
   4. the checkout, when given: absolute and an existing directory;
   5. **`fugaro version` for that binary** (`probeVersion`, then `classifyCliVersion`). `refused` is not stored, and the operator sees the console's own skew sentence. `reduced` is stored, and the picker marks it;
   6. **`verifyCandidate`**: `doctor --json` with `--project` and `--config`, with the checkout or the private directory as the child's working directory. A CLI refusal, including `this checkout belongs to project %s; --project says %s`, is shown verbatim through `sanitizeText` and nothing is stored.

   Every spawn goes through `spawn.ts`, so the spawn log records each one with the candidate's project.
3. **How `--project` and `--config` are passed (D13).** They go on every allow-listed command except `version`, from the **active** installation, through `SpawnTarget`. `spawn.ts`'s table already enforces this (`COMMANDS_WITHOUT_PROJECT`), and this task adds no spawn row. The startup handshake's `SpawnTarget` carries the active installation's project, so the bare `version` spawn records it in the spawn log (Task 1.3's schema). Task 3.3's entry passes `{ project: '' }`, which would give Check 59a's `jq -r '.project' | sort -u` two lines instead of one.
4. **The ambient installation of a Finder-launched app: there is none.** At startup the console uses the stored `active` installation:
   - its `binaryPath` is the resolver's configured path (rule 4 puts it first);
   - its handshake decides the window, as Task 3.3 does.

   With no stored or active installation, the console makes **no guess**: not from `FUGARO_PROJECT`, not from the working directory's checkout, and never by reading the CLI's config (§3.4 rules 3 and 7).
   - The handshake runs against rule 4's resolution exactly as Task 3.3 does.
   - The dashboard shows a "no installation chosen" state that names no figure.
   - The picker opens once, automatically, on that first run.
   - The CLI-backed handlers answer `NO_ACTIVE_INSTALLATION`.

   This is also the binary picker Task 3.1's no-`fugaro` screen names.
5. **The picker (C9).** It is a separate window, opened only from the application menu's `Installations…` item (main process, available in every mode, including `refused`, since that is how an operator fixes a refused console). Its document is `installations.html` with `installations.js` from the same bundle step, under the same CSP header and meta. Its preload, `src/preload/installations.ts`, is a single file that requires only `electron` and exposes a frozen `fugaroInstallations` object:
   ```ts
   list(): Promise<ConsoleReply>                        // display fields only: id, project, binary, config, checkout, active, mode
   pickBinary(): Promise<ConsoleReply>                  // main shows dialog.showOpenDialog; answers a pick token, not a path
   pickConfig(): Promise<ConsoleReply>
   pickCheckout(): Promise<ConsoleReply>
   add(args: { project: string; binary: string; config?: string; checkout?: string }): Promise<ConsoleReply>  // tokens, not paths
   choose(args: { id: string }): Promise<ConsoleReply>
   remove(args: { id: string }): Promise<ConsoleReply>
   ```
   - **No path ever comes from the renderer.** A pick token is a random id that the main process maps to the path the native dialog returned. Tokens are single-use and dropped when the picker closes, with at most 8 outstanding.
   - Its channels use their own prefix, `fugaro:installations/`, with their own closed argument schemas in `installations.ts`. **`isConsoleSender` is narrowed** so `fugaro:console/*` answers only a frame on `APP_ENTRY_URL` and `fugaro:installations/*` answers only the picker's URL.
   - **Choose** runs the version handshake for that installation.
     - `refused`: not chosen, and the console's sentence is shown.
     - Otherwise: `active` is persisted, the previous `CliSource` is closed (its children stopped), a new one is bound, the stored compatibility is replaced, the handlers are registered if they were not yet, and the window is reopened through `openWindowFor`. So a refused-to-ok switch replaces the refusal window with the dashboard, never the reverse.
   - **Remove** of the active installation leaves none active.
6. **The header's read.** The dashboard's bridge gains one argument-free read row, `installation.current`. It returns the active installation's project, binary path, handshake version, checkout and mode, or the no-installation state. It is the fourteenth `EXPOSED_OPERATIONS` entry and is added to `SHIPPED_SURFACE` deliberately. It is a read: no add, choose or remove is on the dashboard's bridge.
7. **The design follows C8 and C9** (once answered). §5.1 rule 10, §7.4 and D20's "the only thing the app ever writes" name `installations.json` as the second file and say when it is written. D13 records C9's header-names, menu-switches shape.

**Interfaces:** `StoredInstallation`, `InstallationFile`, `MAX_INSTALLATIONS`, `installationStorePath(userData: string, env): string`, `readInstallationFile(raw: string): InstallationFile | { refused: string }`, `validateInstallation(candidate, deps): Promise<{ ok: true; compatibility: CliCompatibility } | { ok: false; sentence: string }>` and `INSTALLATION_OPERATIONS` (the picker's closed allow-list) in `installations.ts`. Also `NO_ACTIVE_INSTALLATION` in `ipc.ts`.

**Tests.**

- **`installations-store.test.ts`:**
  - the round trip;
  - mode `0600` and the atomic write (no partial file after a simulated crash between write and rename);
  - each refusal (an unknown key, a 17th entry, an oversized file, a dangling `active`, a relative path, a project with a space) refuses the whole file and leaves its bytes unchanged;
  - the key set of a stored entry is exactly the five above, so an added `token` or `env` field fails a named test;
  - the path refusal under `~/.config/fugaro` and `$XDG_CONFIG_HOME/fugaro`.
- **Validation, against stub binaries:** a non-executable file, a missing file, a `0.5.0` stub (refused, not stored), a reduced-band stub (stored, marked), and a stub checkout whose `doctor` refuses with the pairing sentence (not stored, sentence shown) each behave as step 2 says.
- **`installations-ipc.test.ts`:** the picker's allow-list is closed and frozen; `add` refuses a string that is not an outstanding token, so a renderer cannot name a path; a token is single-use; the dashboard sender is refused on `fugaro:installations/*` and the picker sender on `fugaro:console/*`.
- **`configread.test.ts`** (extended): `installations.ts` may `stat` a config path and never `open`, `readFile` or `createReadStream` it. The mutation is a `readFileSync(configPath)` in `installations.ts`.
- **`startup.test.ts`** (extended):
  - with an active installation, the handshake's `configuredPath` is its binary and the spawn-log target carries its project;
  - with none, nothing is read from `FUGARO_PROJECT` (planted in the app's environment) and the picker opens once;
  - a choose of a refused installation leaves the active one and the window unchanged.
- **`entry.spec.ts`** (extended): with `--user-data-dir` set to a scratch directory seeded with one installation naming the stub (`binaryPath` is the configured path, so the stub wins even on a machine with a real `fugaro` in `/usr/local/bin`, and these cases need no skip), the picker opened through the menu lists it, and the header names its project and binary.

**Command:** `npm run build && npm run test:unit && npm run test:e2e -- entry`

**Depends on:** Task 3.3 (**hard**). Task 3.4a (**hard** for steps 5 and 6; steps 1 to 4 may be written in parallel and rebased). C8 and C9 answered.

**Commit:** `console: the operator's installations, stored outside the CLI's config, checked before they are kept, and chosen in a picker of their own`

**Red step — `Expected: FAIL`.**

- the key-set test against an entry with a planted `token` field;
- the path test against an app named `fugaro` on Linux;
- the validation test against a store that keeps a `0.5.0` stub;
- `installations-ipc.test.ts` against an `add` that accepts a literal path;
- `configread.test.ts` against a planted `readFileSync` of the config path.

#### 3.4c: The screens, wired to the real source

**Files:** `console/src/renderer/entry.ts`, `console/src/main/index.ts` (the source factory), `console/src/source/cli.ts` (only if the factory needs a seam), `console/src/renderer/app.ts` (only if `startConsole`'s deps must widen), `console/test/fixtures/bin/fugaro`, `console/test/support/cliStub.ts`, `console/test/unit/entry-wiring.test.ts`, `console/test/e2e/entry.spec.ts`

**Steps.**

1. **The real source.** The main process binds a `CliSource` to the active installation (`sourceFor(installation)`). That is the only source the shipped app can construct.
   - **The fixture source (`src/source/fixture.ts`) is reachable only through the unit tests' injected `sourceFor`.** No environment variable, argv or setting selects it. It is the same rule as Task 3.3's "no `FUGARO_BIN`": a variable that chose the console's data would be a new way to make it show numbers nobody measured.
   - A grep-style test asserts that `src/main/index.ts` imports nothing from `fixture.ts`, and `build.files` already excludes `dist/source/fixture.js` (#540).
2. **The screens are mounted only through the gate.** After `startConsole`, the entry builds a navigation listing only views that `drawScreen` would draw, and mounts:
   - **Live** (the default): polls `subscribeLive` every 1,000 ms, `watch`'s own document gap. At most one call is in flight; polling stops while the view is hidden or the window is not visible.
   - **Spend:** `readSpend` and its controls (#516).
   - **Projects.**
   - **Run detail:** reached from a run row.
   - **The header:** `installation.current`.

   Every bridge reply is decoded through `src/shared/schema.ts` before a `build*Model` sees it. A refusal or an undecodable reply draws `renderUnavailable` or `refusedState` with the sentence, never a zero (Global Constraint 5).
3. **The stub CLI answers enough for the dashboard, and still refuses the rest.** `test/fixtures/bin/fugaro` answers:
   - `version`;
   - `doctor --json --dir D --project P` with a minimal document;
   - `watch --json --project P …` with the committed synthetic frame (`test/fixtures/synthetic.ndjson`).

   It **exits 2 on any other argv**, and on any argv but `version` that lacks `--project <the seeded project>`. That turns D13's flag rule into an end-to-end assertion. It never answers a never-reachable verb.

**Tests.**

- **`entry-wiring.test.ts`:**
  - with a fake bridge, a `refused` answer mounts no screen and makes no read call;
  - a `reduced` answer never calls `runReport` or `showBudget`;
  - an undecodable `live.watch` reply draws the unavailable state, not a zero;
  - at most one poll is in flight;
  - the fixture-source grep test.
- **`entry.spec.ts`** (extended):
  - with the seeded stub installation, the Live screen renders the synthetic frame's repository row and its run, with no `executeJavaScript` used to load code (the spec asserts it injects none);
  - the spawn log shows `watch --json` carrying `--project` and the stub's absolute path as `argv[0]`;
  - in refused mode the window carries no preload and the spawn log holds no read;
  - in reduced mode no Spend entry is in the navigation and the spawn log holds no `report`.

**Command:** `npm run build && npm run test:unit && npm run test:e2e -- entry`

**Depends on:** Tasks 3.4a and 3.4b (**hard**).

**Commit:** `console: the screens draw what the chosen installation's fugaro says, and nothing when it says nothing`

**Red step — `Expected: FAIL`.**

- `entry.spec.ts`'s Live case against 3.4b's tree (no screen mounted);
- `entry-wiring.test.ts`'s reduced case against an entry that mounts Spend regardless;
- the stub's `--project` refusal against a source that omits the flag from `watch`.

**Owner-run additions.** None is required.

- The new e2e assertions read the DOM and the spawn log, not pixels, so no golden gates this task.
- The synthetic frame suffices, and Task 1.4's eight recordings are not needed for it.
- **Optional:** goldens for the header and the picker, recorded by the owner on both platforms by Task 2.1's procedure (`snapshotPathTemplate` per platform, `--update-snapshots`, then commit). If the owner wants them, they land as a follow-up commit and do not gate this task.

**What this task is not.**

- It adds no spawn row, no write verb and no CLI change. Design §4.3's gap 5 (`fugaro config list --json`, the v2 plan's task) would turn 3.4b's typed project name into a pick from a list. This task does not wait for it.
- It adds no push channel and no environment variable that selects a binary or a source.
- It does not change the CSP.

---

## Phase 4 — macOS packaging

### Task 4.1: The builder configuration and the entitlements — SECURITY-SENSITIVE, independent opus review

**Files:** `console/entitlements.mac.plist`, `console/package.json`, `console/src/main/fuses.ts`, `console/test/unit/{entitlements,fuses}.test.ts`, `console/README.md`

**Steps.** The packaging configuration for macOS: the app bundle, the `dmg` and `zip` targets, the hardened runtime, and **the minimum entitlements** — no `com.apple.security.cs.disable-library-validation` unless the pinned Electron genuinely requires it, and if it does, the reason is a comment in the plist itself; no `get-task-allow` outside a debug build; no entitlement the app does not demonstrably use. **The Electron Fuses are flipped here too**, in an `afterPack` step over `@electron/fuses`, and they are the design's §5.1 rule 4: **`RunAsNode` disabled**, **`EnableNodeOptionsEnvironmentVariable` disabled**, **`EnableNodeCliInspectArguments` disabled**, **`EnableEmbeddedAsarIntegrityValidation` enabled**, **`OnlyLoadAppFromAsar` enabled**, **`GrantFileProtocolExtraPrivileges` disabled** — all other fuses left at their upstream default, listed in the test so "left alone" is also an assertion. These are build-time properties of the shipped binary, not settings: `ELECTRON_RUN_AS_NODE=1` must not turn the console into a Node interpreter for anyone who can execute it, and `NODE_OPTIONS`/`--inspect` must not reach its own process. The signing identity is read from the environment and is **never** written into a file in this repository. Document, in the README, the exact commands the owner will run, so Tasks 4.3 and 4.5 have nothing to invent.

**Tests:** `entitlements.test.ts` — parses the plist and asserts the minimum set exactly: any added entitlement fails a named test, and `get-task-allow` fails one on its own. This is the mutation-proof form: a widening must be a deliberate edit to a test, not a quiet addition to a plist. `fuses.test.ts` — reads the fuse configuration (and, in CI on Linux, the `--fuse` flags the packer will pass) and asserts **each of the six fuses above has the required value and every other fuse is `null`/default**, with a named test per fuse so re-enabling `RunAsNode` cannot pass review as a packaging detail.

**Command:** `npm run test:unit`

**Depends on:** Task 3.2.

**Commit:** `console: the macOS bundle, with the entitlements it can justify and no more`

**Red step — `Expected: FAIL`** — the minimum-set test against a planted `disable-library-validation`, and the `RunAsNode` fuse test against a configuration that leaves it on.

### Task 4.2: The signing and notarisation pipeline, staged — SECURITY-SENSITIVE, independent opus review

**Files:** `docs/pending-workflows/console-release.yml`, `docs/pending-workflows/README.md`, `console/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Write the **signing** workflow as a staged file under `docs/pending-workflows/` — this repo's mechanism for a change only the owner may push (its README is the directory's only occupant today, and `docs/release.md`'s "Before you tag" names copying a staged workflow in as an owner step). It builds on the CLI's tag, signs with the hardened runtime, notarises with `xcrun notarytool submit --wait`, staples both the app and the dmg, verifies with `codesign --verify --deep --strict`, `spctl --assess --type execute` and `stapler validate`, and **records those verification outputs** the way the CLI's release records its checksums. Actions are pinned to full commit SHAs with `# vX.Y.Z` comments, as everywhere else in this repo, and the Apple secrets it needs are named (`secrets.APPLE_CERTIFICATE`, `secrets.APPLE_CERTIFICATE_PASSWORD`, `secrets.APPLE_ID`, `secrets.APPLE_TEAM_ID`, `secrets.APPLE_APP_PASSWORD` or the App Store Connect API key triple, whichever D4 settles) but never valued. Extend the directory's README with this file's row: what it is, why it is staged, and the one command that applies it.

**Split from the tests workflow, and the split is a security boundary rather than tidiness — this is the correction round 2 asked for.** An earlier revision staged a single `console.yml` holding both the always-on pull-request test job (Tasks 1.4/4.2) and this signing job with the Apple secrets. That would have put **the signing key's job definition next to a job that runs on every pull request from any branch**, where the only thing between a fork's pull request and the signing material is GitHub's own rule that fork pull requests get no secrets — a rule about *forks*, not about branches of this repository, and a single `workflow_dispatch` or a future `if:` away from being the whole defence. Two files instead:

| Staged file | Task | Triggers | Secrets | Job key |
|---|---|---|---|---|
| `console-tests.yml` | 1.4 | `pull_request`, `push` to `main` | **none at all** | `console` |
| `console-release.yml` | 4.2 (this task) | `push` of a `v*` tag only | Apple signing material, via an `environment:` | `release` |

So this file: **triggers only on a tag push** — no `pull_request`, no `pull_request_target`, no `workflow_run`, no `workflow_dispatch`; **takes its secrets through a job-level `environment:`** naming a **protected GitHub environment** (required reviewers on the owner, deployment branches restricted to tags), which is the mechanism that makes the secrets unreachable from anywhere but the owner's approval; **runs `scripts/release-gate.sh` as the first step** — the same release gate the CLI's own tag is subject to, so a console release cannot bypass the checks the CLI must pass (C5, D18); and names the secrets **only as `secrets.NAME` references**, never as literals. The reasoning is written into the file's own comment header, and the test below asserts each clause of it, because a comment nobody tests is how the two files become one file again.

**Tests:** the workflow's *behaviour* cannot run here — it has never run, and the directory's README already carries a "Not verified" section that this adds a console row to. But its **text** is testable, and an earlier revision of this task left that as "the review substitutes", which is not a test:

- `TestDocsConsoleReleaseWorkflowIsStagedAndPinned` (in `internal/cli/docs_desktop_console_test.go`, the file Task 0.3 allows to read a `console/` path, plus the `docs/pending-workflows/` path every docs test may read) asserts, over `docs/pending-workflows/console-release.yml`: **every `uses:` is pinned to a full 40-hex commit with a `# vX.Y.Z` comment**; **the trigger is a `v*` tag push and nothing else** — in particular **no `pull_request`, no `pull_request_target`, no `workflow_run` and no `workflow_dispatch`**; **the secrets are reached through a job-level `environment:`** naming a protected environment, and **every secret reference is a `secrets.` reference with no literal beside it** (no `-----BEGIN`, no long base64 blob, no `password:`/`token:` with a value); **`scripts/release-gate.sh` is the first step** of the signing job; **no `permissions: write-all`**, and the job's `permissions:` is declared explicitly; **if a file exists at `.github/workflows/console-release.yml` at all, it is byte-identical to the staged copy** — the assertion that a workflow under `.github/workflows/` was copied in rather than written or edited by an agent (an earlier revision asserted the file's *absence*, which Task 4.5's owner-run copy-in would have turned red; byte-identity holds before and after it, and says more). Task 1.5 copies the *other* file, and this assertion is over this one.
- `TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger` is the named sub-assertion the security finding turns on, split out so the failure sentence names the bug: it greps the trigger block of the release file for `pull_request` and fails, with the message quoting the reason ("the signing job must not run on a pull request"). **The mutation that proves it**: adding `pull_request:` to the release file's `on:` must make this test red, and the red step below runs exactly that.
- `TestDocsPendingWorkflowsReadmeNamesTheConsoleRows` (plural — Task 1.4's `TestPendingWorkflowsReadmeNamesTheConsoleTestsRow` is its singular, row-specific counterpart and continues to pass) asserts the README carries **both** console rows — this file's and Task 1.4's — each saying what it is, that it is staged because no agent may push a workflow here, and the one command that applies it.
- **The review criterion on top of the tests**: a reviewer reads the pinned SHAs against upstream, reads the protected-environment name against the repository's actual environment settings, and confirms the workflow would **fail closed on a missing secret** rather than silently skipping the signing step — a signing job that skips when a secret is absent ships an unsigned build under a signed release's name.

**And an agent still may not copy it into `.github/workflows/`** — Task 4.5 is the owner's step, and the "if present, byte-identical to the staged copy" assertion above is what makes a hand-edited or invented workflow a red test rather than a review miss.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.1.

**Commit:** `console: the signing and notarisation pipeline, staged for the owner`

**Red step — `Expected: FAIL`** — `TestDocsConsoleReleaseWorkflowIsStagedAndPinned` before the staged file exists and against a planted `uses: actions/checkout@v5`; `TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger` against a release file whose `on:` carries `pull_request:` — **run this one first**, because it is the mutation that reproduces the finding this task exists to fix; and `TestDocsPendingWorkflowsReadmeNamesTheConsoleRows` before the README rows are added.

### Task 4.3: Checks 59a and 59b, the first signed build and its round trip — OWNER-RUN, no agent performs any step

**Files:** `docs/gcp-live-checklist.md`

**Steps.** Write **Check 59** into the checklist as **two sections of one number — `## Check 59a` and `## Check 59b`**, following the sub-lettering Check 43 already uses (43a-43f), each with its summary-table row, its FACT lines, its restore step and **Check 43's guard block verbatim** — the three hard stops, `--project fugaro` on every `fugaro` command, `--project "${P:?}"` on every `gcloud` command, re-run from a fresh shell before each step, and the sandbox checkout as the working directory for every `fugaro` command the console drives. Each section says in its first line that **no agent performs any step of it**.

**Why two sections and not one, because round 1 and round 2 both wrote it as a single check and the reason it cannot be one is a fact about ordering rather than tidiness.** The two halves cannot run at the same moment, and the four assertions keep round 1's numbers so a reader can follow where each went:

- **59a — the first signed build.** (1) the certificate and the App Store Connect API key exist and are stored where only the owner can reach them; (2) a signed, notarised, stapled build is produced and **passes `spctl --assess` on a machine that has never seen it**; (4) the safety assertion below, which needs a running build and nothing else. It runs **before any tag**, on the first build the packaging pipeline produces.
- **59b — the update round trip.** (3) an installed older build sees the new release and installs a bundle whose signature the OS verifies. **It needs a published feed**: an installed older build can only see a newer one once a tagged release has published the console's artifact and its `latest-mac.yml`, so 59b is run in a later session, after the first tag that ships the console, and its section says so in its first line. **Its guard and restore step are its own**: it installs a build into `/Applications` too, and the restore step of 59a never runs for it.

Splitting them is what makes the safety assertion runnable at all: as one check, the negative proof sat behind a release, and a check whose first run needs a tag is a check that does not run.

**How the app is launched is part of the check, and it is stated because assertion (4) cannot work otherwise.** A Finder or Dock launch inherits launchd's environment and **not** the shell's, so an app opened that way never sees `FUGARO_CONSOLE_SPAWN_LOG` and writes no spawn log at all — the check would then fail at its first FACT line with the right conclusion for the wrong reason. So 59a launches the built bundle **from a terminal that has exported the variable**, in the foreground, by its executable path:

```
export FUGARO_CONSOLE_SPAWN_LOG="${SPAWNLOG}"
"/Applications/Fugaro Console.app/Contents/MacOS/Fugaro Console" &
```

The bundle name and the executable name are the ones Task 4.1's builder configuration pins, and `open` is deliberately **not** used: it does not forward the shell's environment to the launched app. The check drives the app long enough to spawn its handshake and at least one read, then quits it, and only then reads the log — the app never reads it back (§5.1 rule 10).

**Assertion (4) is written as FACT lines with commands and expected output, because round 2 found it unchanged from round 1 — a sentence asserting a safety property with nothing under it — and because round 3 found the artifact it was built on undefined.** The check needs a file to read, so it names one that Task 1.3 actually builds: **`SPAWNLOG="${LOGDIR}/spawn.jsonl"`**, passed to the console as **`FUGARO_CONSOLE_SPAWN_LOG`** before it is launched, holding the JSONL of one line per spawn with exactly the four keys `ts`, `argv`, `cwd` and `project` (design §5.1 rule 10, D20; Task 1.3's `spawnlog.ts`). **Without that variable the file does not exist and not one of these commands can run** — which is why the launch above says so, and why the check's first FACT line is `test -s "${SPAWNLOG}"`. Four steps, each FACT-lined, each re-run from a fresh shell with the guard applied, and **none of them touching `belong` at all** — which is what makes it testable without doing the forbidden thing:

1. **The console's own record of what it ran.** FACT: `test -s "${SPAWNLOG}"` → exit `0` (the log exists and is non-empty), then `grep -c belong "${SPAWNLOG}"` with expected output **`0`**, and `grep -c edgeappinc "${SPAWNLOG}"` with expected output **`0`**. The unit is a **line**, not a byte: one JSON object per line, so a count of zero means *no spawn record mentions it*, which is the statement being proved. The mutation control below shows the same command finding a hit when there is one.
2. **Every spawned argv resolves to the sandbox installation.** FACT: `jq -r '.argv[0]' "${SPAWNLOG}" | sort -u` with expected output exactly the installed sandbox `fugaro` path — **one line, and not the `belong` installation's path**, which the check names as a string it must *not* see. This is the assertion behind "the console's child process is the installed `fugaro` and nothing else": **one binary, and it is the sandbox's.**
3. **Every spawn was for the sandbox installation, from the sandbox checkout.** FACT: `jq -r '.project' "${SPAWNLOG}" | sort -u` with expected output **one line**, the guard block's own `fugaro` — one line and not two, which is also what proves the bare `version` handshake recorded its installation (Task 1.3's schema gives every line a `project`, including the handshake's). Then `jq -r '.cwd' "${SPAWNLOG}" | sort -u` with expected output **one line**, the sandbox checkout directory — the design's §3.4 rule 5, checked rather than assumed. An earlier revision of this step asserted `.gcp_project`, **which is not a field the console has** (D13's installation tuple is a project name, a config path, a binary path and a directory), so it could never have printed anything.
4. **The mutation control, and this is the step that makes the other three mean something.** With the console closed, the owner appends a single line naming `belong` to **a copy of the log** — `cp "${SPAWNLOG}" "${SPAWNLOG}.test"`, then one `printf` appending a syntactically valid record whose `project` is `belong` — and re-runs step 1's two greps against the copy. Expected output **`1`** for each: the command that must say `0` says `1` when the thing it looks for is there. Then the copy is deleted, the real log is untouched, and step 1 is re-run to confirm `0` again. **No step of this check contacts, reads, or writes the `belong` installation** — the string `belong` appears only as text in a scratch file, which is the only way to prove a negative of this kind without performing it. **`SPAWNLOG` is deleted by 59a's restore step**, since the check created it.

**And the assertion is worded as what those lines prove, not as what a reader might wish they proved** (round 3): the claim is **"with the console pointed at the sandbox installation it spawned nothing for `belong` — every spawn record names the sandbox installation's `fugaro`, the sandbox project and the sandbox checkout, and none mentions `belong` or `edgeappinc`"**. An earlier revision wrote it as "nothing it can reach belongs to `belong`", which is a claim about a reachability that nothing in the check tests: the FACT lines are about **spawns**, and the console's only egress is a spawn of one allow-listed argv.

59a also FACT-lines `security find-identity -v -p codesigning` before and after, so "the console left no key material behind" is checked the same way: **the two outputs are identical**, and the restore step never deletes an identity it did not create (which is Assertion (1)'s record, above).

**The restore step restores only what the check created, and this is a hard rule of the check, not a courtesy.** A restore step that "removes the keychain item it was signed with" would delete **the owner's own signing certificate** — the identity the whole pipeline depends on, which predates the check by design and is not the check's to destroy. So: 59a's first step **records what exists before it starts** — `security find-identity -v -p codesigning` and the list of the keychain items it can see — as a FACT line; its restore step then **removes only what is not in that record**: the installed build from `/Applications`, the downloaded dmg and zip, the notarisation submission artifacts, the spawn log it created, any temporary keychain or key material the check itself created, and any file it wrote under a temporary directory. 59b's restore step does the same for what it installed and downloaded, against its own pre-check record. **A signing identity that was present before the check is never deleted, never re-imported and never locked**, and both sections say so in their own text so a later reader cannot mistake a follow-the-recipe restore for a licence to clear the keychain. (The one permitted deletion is of an identity the *check itself* created for the purpose, which is why the pre-check record is a FACT line rather than an assumption.)

**Tests:** the checklist's own docs tests, plus the guard test the project-centric plan schedules (Task 6.3's `TestChecklistChecksAreGuarded`), which both sections must satisfy, plus four named docs tests over the written sections:

- `TestCheck59RestoreStepSparesPreexistingIdentities` asserts the sections' restore steps **do not name `security delete-identity`, `security delete-certificate` or `security delete-keychain` against anything present before the check** — the mutation-proven form: a restore step that clears the owner's identity fails a named test rather than reading as thoroughness in review.
- `TestCheck59AssertionFourIsFactLined` — **the test that makes Assertion (4) an assertion rather than a sentence**, and the answer to round 2's "unchanged from round 1": it asserts 59a carries the four steps above, each as a `FACT` line with a command **and an expected output** — in particular that the `test -s "${SPAWNLOG}"` line is present, that the two zero-count greps are present with `0` as the expected output, that **the `jq -r '.argv[0]'` and `jq -r '.project'` and `jq -r '.cwd'` lines are all present** — those three key names being the ones Task 1.3's `spawnlog.test.ts` pins the schema to, so the check and the writer cannot drift — that the mutation control is present and names the expected output `1`, and that **the launch step above is present and sets `FUGARO_CONSOLE_SPAWN_LOG` before the app is started**, since assertion (4) is unrunnable without it. It also asserts the section does **not** call `jq` on a key the schema does not define (`.gcp_project`, `.spawns`, `.repository`), which is the defect that made round 2's version of this assertion unrunnable. A section that says "nothing it can reach belongs to `belong`" without these lines fails this test. **The mutation that proves it**: deleting the mutation-control step from a copy of the section must make the test red, per this task's red step.
- `TestCheck59NeverNamesBelongAsATarget` asserts the string `belong` is never an **argument to a command the check runs** — the machine-checkable half of Global Constraint 6 for these sections, and the test a later edit must not be able to satisfy by "verifying" the negative the forbidden way.
- `TestCheck59IsSplitIntoAAndB` asserts the number is written as two sections — `## Check 59a` and `## Check 59b` — that **59b's first lines say it needs a published feed and runs after the first tag**, and that the summary table carries both rows. The split is D12's content and this is what keeps a later edit from folding them back into one section that cannot run.

**`TestCheck59NeverNamesBelongAsATarget` is the test round 3 asked to reconcile, and this is the reconciliation.** Round 2's version asserted that `belong` appears only in a prose prohibition and in a `grep -c belong` FACT line — **which forbids the one line Check 59's mutation control must write**: step 4 appends a record whose `project` is `belong` to a copy of the log, and a test that forbids it makes the check unsatisfiable by its own test. The test instead asserts, over 59a's text, exactly three permitted shapes and no others:

1. a prose sentence stating the prohibition — the check's own "no step touches `belong`" lines and step 2's naming of the `belong` installation's path as a string it must **not** see (the string in a line that runs nothing);
2. the `grep -c belong "${SPAWNLOG}"` FACT line of step 1;
3. **the single mutation-control line** of step 4 — a `printf` (or `echo`) whose output is redirected into `${SPAWNLOG}.test`, which is where the recorded project name is written into a scratch copy.

and it fails on everything else, most importantly: **no line that invokes a command names `belong`** (`fugaro`, `gcloud`, `sudo`, `brew` and the rest — so `fugaro --project belong budget show` is red, which is the mistake the test exists to catch), **no `--project belong` outside that one mutation-control line**, and **no path, URL or file name containing it** (the scratch copy is `${SPAWNLOG}.test`, so it does not need `belong` in its name). The mutation control stays in the check, and the test permits precisely the three lines that make it work.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.2, **and Task 1.3** — assertion (4) reads the spawn log Task 1.3 writes, with the key names Task 1.3's schema pins, so the check cannot be written before that file exists and its shape is fixed. (Phase 1 merges long before phase 4, so this dependency is satisfied in practice; it is named because a reader should not have to infer that a live check reads an artifact from four phases earlier.) **59b additionally depends on a release that publishes the feed**, which is why it is a separate section with its own job.

**Commit:** `checklist: Check 59a and 59b, the console's first signed build and its round trip`

**Red step — `Expected: FAIL`** — the guard test against a Check 59a without the three hard stops; the restore-step test against a restore that clears a pre-existing signing identity; `TestCheck59AssertionFourIsFactLined` against the round-1 text — the bare sentence "nothing it can reach belongs to `belong`" with no FACT lines under it — and again against a section whose mutation-control step, or whose launch step, has been deleted; `TestCheck59NeverNamesBelongAsATarget` against a section that "verifies" the negative with something like `fugaro --project belong budget show`; and `TestCheck59IsSplitIntoAAndB` against the single-section version this task replaces.

### Task 4.4: Auto-update — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/main/updates.ts`, `console/test/unit/updates.test.ts`, `docs/pending-workflows/console-release.yml`

**Steps.** The design's §7.3: the update check reads the same release metadata the CLI's `internal/updatecheck` reads and honours the same opt-out (`FUGARO_NO_UPDATE_CHECK=1`, a local `update_check: false`, never in CI), and the installation path is Electron's `autoUpdater` (Squirrel.Mac) driven by **`electron-updater`** (the `electron-builder` companion that supplies the feed client and the `latest-mac.yml` manifest — a format of electron-updater's, not of Squirrel) over a **signed** feed: an update whose signature does not verify is refused, never installed. The pinned `electron-updater` version is part of Task 1.0's exact pins, since it is the component that downloads and installs code. No telemetry of any kind: the only outbound requests are the CLI's own and this one update check (design §7.4). Default on, with the toggle visible (D5).

**Amended 2026-10-30, with what shipped (auto-update U5): the opt-out above is one thing the console does *not* do, and the rest of it is built.** The console honours `FUGARO_NO_UPDATE_CHECK=1` and **its own switch** — the application menu's `Check for Updates Automatically`, whose state is the one boolean `<userData>/updates.json` holds — and it reads **neither** a local `update_check: false` nor anything else in the operator's CLI config: §3.4 rule 7 stands, and the owner ruled this on 2026-10-30 (**D769**). Everything else here is on `main`: **U1 (#1378)** the policy, **U2 (#1379**, its feed copy-in **#1390)** the release feed, **U3 (#1392)** `electron-updater` pinned to exactly `6.8.10` with the controller wired in, **U3b (#1438)** the restart path, and **U4 (#1439)** the switch and its menu items. The client is hardened by **`hardenUpdater`**, which keeps the library from minting or writing `.updaterId` under `userData` and from sending `x-user-staging-id` on a request; the check runs **at launch and every six hours**; a download is quiet and the restart notice is non-blocking; an update installs **only through the notice's "Restart to update"**, never on quit, and only the download that notice named; the two locations the updater itself writes — its download cache `~/Library/Caches/fugaro-console-updater/` and Squirrel.Mac's ShipIt staging — appear only after a download and hold no identifier (design §5.1 rule 10, §7.3 and §7.4, **D768** and **D770**). The first build that carries all of it is **0.116.0** and it cannot update itself, so Check 59b runs against `0.116.0` installed by hand and the first console release after it (**D771**).

**Tests:** `updates.test.ts` — the opt-out is honoured (the two the console ships: `FUGARO_NO_UPDATE_CHECK=1` and its own switch, per the amendment above); an unsigned or mis-signed bundle is refused; no identifier is sent (asserted on the request the fake feed receives); a downgrade is refused; the check never runs in CI.

**Command:** `npm run test:unit`

**Release (owner, 2026-10-17, "Separate console release"):** the update feed and the console's Homebrew cask point at the console's own `console-vX.Y.Z` release that Task 4.2's workflow publishes, never at the CLI's `vX.Y.Z` release; the cask wiring is not Task 4.2's.

**Depends on:** Task 4.2.

**Commit:** `console: the update check, with no telemetry and a verified feed`

**Red step — `Expected: FAIL`** — the mis-signed-refusal test against an updater that installs anyway, and the no-identifier test against a request carrying a machine id.

### Task 4.5: The staged signing workflow, copied in — OWNER-RUN, no agent copies it

**Files:** `.github/workflows/console-release.yml` (**created and pushed by the owner — no agent creates, edits or copies this file**), `docs/pending-workflows/README.md` (the agent-run follow-up that records it as applied)

**Steps.** Task 4.2 stages the console's **signing** workflow; this is the step that puts it in place, and it is the owner's because this repository refuses an agent's workflow change outright (GitHub will not accept it, so a run that edits one cannot be delivered — Global Constraint 7). The owner copies `docs/pending-workflows/console-release.yml` to `.github/workflows/console-release.yml`, commits and pushes it, and **creates the protected GitHub environment the file names** — required reviewers, deployment branches restricted to tags — since the workflow is only as safe as the environment it reads its secrets from; a staging file cannot create an environment, and this is the step where the two must exist together. **The tests workflow is not this task and does not wait for it**: Task 1.5 copied `console-tests.yml` in during phase 1, so by now the console's unit and e2e tests have been running on every pull request and every push to `main` for three phases. This file adds the tag-time job and nothing else.

An agent then makes one docs-only follow-up: the README's console-release row gains the commit that applied it and the name of the environment that was created, and the "Not verified" note becomes "applied; unverified until the first tag".

**Tests.** The real test is the workflow's own first tag run, which is **Check 59b's** step (Task 4.3) — and the ordering below is why 59a is already done by then, while 59b, which needs the feed this tag publishes, is the run that follows it. The agent-run follow-up is covered by a docs test asserting the README row names the applying commit and the environment, so the record cannot be left as "to do".

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.3 — **59a** is what proves the signing pipeline works on a real Mac (certificate, notarisation, stapling, Gatekeeper and the spawn-log safety assertion), and a workflow that signs a release before the pipeline has ever been proved would ship an unverified bundle. **59b is the tag's own verification**, and it is written in the same task but run after this workflow has fired once.

**Commit:** the owner's own, for the copy-in and the environment; the agent's follow-up is `docs: record the console release workflow as applied`

**Red step — `Expected: FAIL`** — the README-row test before the applying commit is recorded.

### Task 4.6: The release gate requires the console check

**Files:** `scripts/release-gate.sh`, `internal/cli/docs_desktop_console_test.go`

**Steps.** `scripts/release-gate.sh` requires exactly the checks in its `names` list (`test`, `terraform`, `rules`), so before this task a console job that fails would not stop a tag. Add the console's checks to that list — **and only after a workflow is in place that produces checks of exactly those names, running unconditionally** — which is **Task 1.5**, three phases earlier: the tests workflow's `console` job. (An earlier revision ordered this gate edit after Task 4.5, the *signing* copy-in, because a single staged file carried both jobs; the split of Tasks 1.4/4.2 moved the dependency with the job, and the gate now depends on the copy-in that happened first, not the one that happens last.) Ordered this way deliberately (D18): a gate that requires a check no workflow runs refuses every tag on this repository, including the ones that ship none of this, which is a worse failure than the one being fixed. **The ordering is necessary and not sufficient**: the checks must also exist on *every* `main` commit and on every pull request head, because `identical_tree_checks` demands the same names there — which is Task 1.4's `TestConsoleWorkflowJobIsGateable` and the reason the console job carries no `paths:` filter (design §9.4). The comment at the top of the script and the `identical_tree_checks` doc comment both name the required set, so they are edited with it. **This is the only change in the whole plan to a non-test file outside `docs/` and `console/`** — an earlier revision called it "the one change to a file outside `docs/` and `console/`", which was false: Tasks 0.1, 0.2, 0.3 and 4.2 create and edit `internal/cli/docs_desktop_console_test.go` and `internal/cli/docs_console_boundary_test.go`.

**The names are the matrix legs, not the job key** (owner's correction, 2026-10-18, taken from a real check-runs response). An earlier revision of this task said "Add `console` to that list" and assumed the job's static `name: console` made both matrix legs report as one check. GitHub does not do that: **for a matrix job the check-run name carries the matrix values**, so `.github/workflows/console-tests.yml` reports `console (ubuntu-latest)` and `console (macos-latest)` and never a bare `console`. `check_state` matches check runs by **exact** name, so a required bare `console` would be a check the gate can never find — and a missing check is not a success — making the gate refuse **every** tag on this repository, the CLI-only releases included. So the gate requires **both** real names: `names` is a bash array,

    names=("test" "terraform" "rules" "console (ubuntu-latest)" "console (macos-latest)")

which it must be, since two of the names carry a space and the old whitespace-split string cannot hold them. Every loop over it reads `"${names[@]}"`, and the "first pending check" is remembered as a name and a state rather than one `"$name $state"` string, whose `${pending% *}`/`${pending#* }` would split a console leg at the wrong space. `check_state`'s own rule — every completed run of a name must be successful — is what makes the two legs one requirement rather than two.

**Tests:** a Go docs-style test, `TestReleaseGateRequiresTheConsoleCheck`, that reads `scripts/release-gate.sh` and asserts its `names` array holds exactly the three CLI checks **plus the two console check runs derived from `.github/workflows/console-tests.yml`'s single job key and its `os` matrix** — derived rather than repeated, so a renamed job, an added platform or a second matrix key fails a named test instead of a release tag. It also asserts the script's own comment lists the same names, so prose and variable cannot drift; that the bare `console` is not required; that both loops are array-shaped; and, by running the real gate against a fake `gh` that pipes check runs through the gate's own `--jq` expression with a real `jq`, that the gate passes only when both legs are green and refuses when either is missing, pending, failed, cancelled, green only from another app, or absent from the identical-tree head. **The red step is the old list**: `TestReleaseGateRequiresTheConsoleCheck` against `names="test terraform rules"` fails.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 1.5 (**hard**: this must not merge before the check exists — and it has, by three phases).

**Release gate, in addition: Task 3.3 must be merged before the console MVP is released.** (Approved by the owner 2026-10-17: the task is no longer waiting for a go, and the gate below is unchanged by that.) The gate edit above is about CI; this one is about content. The console that carries phase 4 must be one whose startup runs the version handshake (D7), so the release checklist for the release that first ships the console (0.8.5) lists Task 3.3 as a precondition, and a console tree on which `index.ts` does not call `startupWindow` does not ship. The test for that is `TestConsoleMVPRequiresTheStartupHandshake` in `internal/cli/docs_desktop_console_test.go` (added with Task 3.3's plan entry, not by Task 4.6): it reads this plan, asserts the Task 3.3 heading itself carries `SECURITY-SENSITIVE, independent opus review`, that Task 3.3 depends on 3.2, that Task 4.6 names Task 3.3, that the policy list in Global Constraints rule 9 includes 3.3, and asserts the "What ships with the MVP" list below names Task 3.3.

**Release gate, in addition: Task 3.4 must be merged before the console MVP is released** (approved by the owner 2026-10-18). All three parts, 3.4a, 3.4b and 3.4c, are covered. Without them the shipped console draws a static sentence in either window: no preload, no handlers, no script and no installation. So the release that first ships the console (0.8.5) lists Task 3.4 as a precondition next to Task 3.3. The test for that is `TestConsoleMVPRequiresTheRendererEntry` in `internal/cli/docs_desktop_console_test.go`, a sibling of the Task 3.3 test added with Task 3.4's plan entry. It asserts:

- the Task 3.4 heading carries `SECURITY-SENSITIVE, independent opus review`;
- Task 3.4 depends on Task 3.3 and carries its status line;
- this paragraph names Task 3.4;
- "What ships with the MVP" names Task 3.4;
- the phase summary and the cross-task dependencies name it;
- Task 3.3's own section names Task 3.4 as the owner of the wiring.

**The console's release is its own** (owner, 2026-10-17, "Separate console release"): `console-vX.Y.Z`, published by the console workflow from the CLI's tag behind this same gate; the Homebrew cask for the console, when it is wired (a later task), points at it.

**Commit:** `release: the gate requires the console check`

**Red step — `Expected: FAIL`** — `TestReleaseGateRequiresTheConsoleCheck` against `names="test terraform rules"`.

---

## Phase 5 — Later, unscheduled

### Task 5.0: The design follows the owner's decisions

**Files:** `docs/design/desktop-console.md`, `internal/cli/docs_desktop_console_test.go` (the docs-presence test of the red step below — the design's own presence test's file, so Task 0.3's one-entry allow-list is not widened)

**Steps.** **First run, 2026-10-16: the design already carried every answer of 2026-10-15.** §10's last column and the prose around it were written with the answers in the PR that added the design (#401), so this task **added only what was still missing** rather than writing them for the first time: the presence test of the red step, which pins the four ruled cells and the sixteen acceptances; the two lines recording that the owner's answers of **2026-10-16** do **not** touch this design and are recorded in [desktop-console-v2.md](../design/desktop-console-v2.md)'s D21–D38 (not duplicated here); and the design's own §9.5 item 1, which now names the answers among the strings its presence test holds. **The red step was run for real**: the test was written first, failed on the 2026-10-16 strings the design did not yet carry, and passed once they were added. **This task's trigger has changed, and the change is the point of this revision.** On 2026-10-15 the owner answered every decision in the design's §10: **D1** the CLI sidecar, behind `FugaroSource` (direct database access never), **D3** this repository, under `console/`, **D4** direct download plus a Homebrew cask, signed, notarised and stapled — with Apple signing entering this repository's release path **tag-only**, behind `scripts/release-gate.sh` and a **protected GitHub environment**, **the owner holding the certificate and running every signing step**, and the Mac App Store ruled out — and **D11** its own artifact published from the CLI's `vX.Y.Z` tag, with a declared supported CLI range. **The other sixteen — D2 and D5 to D10 from the first revision, D12 to D20 from the second — were accepted as recommended, none objected to.** The answers are recorded in the design's §10 (its last column) and in the table below, so **this task no longer waits on a ruling and no longer writes the answers for the first time**: it exists now for the case the answers open — a later ruling that **reverses** one of them. If that happens, this task rewrites the design's row and its answer cell to the ruled answer **before any code task that depends on it starts**, and records the reversal, exactly as `docs/design/project-centric.md` §12 records its own. **A docs task apart from the Go test that pins it** — which is why its Files line names `internal/cli/docs_desktop_console_test.go` — and it is the only task in this plan whose edit to the design is its whole deliverable.

**D13's answer is the one that could have changed the plan rather than the design, and it did not.** The owner answered it as recommended — the operator chooses the installation in the UI, and the console passes `--project` (and `--config` where the installation is not the ambient one) **on every command that takes the flag** — so Task 1.3's spawn contract and §3.4 rule 3 stand exactly as written, and the add-an-installation check Task 1.3 now carries (a checkout whose `fugaro.yaml` names another project is refused before the installation is stored) stands with them. Had the owner ruled that the console may *not* pass `--project`, Task 1.3's spawn contract and rule 3 would have been rewritten with it, and Task 0.2 (which reads `selectProject`/`selectWith` in `internal/cli/project.go`) is where the consequence would have been worked out.

**Every answer, in the plan's own list** (the design's §10 is the authority, this is the mirror a reader of the plan should not have to open the design to find):

| Decision | Owner's answer, 2026-10-15 |
|---|---|
| **D1** — how the console reaches the data | **CLI sidecar (option A), behind `FugaroSource`; direct RTDB/Firestore access with the operator's credentials (option B) never.** |
| **D3** — where it lives | **This repository, under `console/`.** |
| **D4** — distribution and Apple signing | **Direct download plus a Homebrew cask; signed, notarised and stapled.** Apple signing enters this repo's release path **tag-only**, behind `scripts/release-gate.sh` and a **protected GitHub environment**; **the owner holds the certificate and runs every signing step**; **the Mac App Store is out.** |
| **D11** — its own artifact, and from which tag | **Its own artifact, published from the CLI's `vX.Y.Z` tag, with a declared supported CLI range.** |
| **D2, D5 to D10, D12 to D20** (sixteen) | **Accepted as recommended**, and none objected to. |

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** nothing, now that the owner has answered — it becomes live only if a later ruling reverses an answer.

**Commit:** `console: the design, at the owner's decisions`

**Red step — `Expected: FAIL`** — the docs-presence test before the ruled strings are in.

### Task 5.1: Windows and Linux packaging (unscheduled)

**Steps.** Two more signed deliverables (**D10**), each with its own identity and its own owner-run check (`windows-latest` signing via a code-signing certificate; Linux via the distro channels this repo would have to choose). **Not scheduled**: the design recommends later, the code stays cross-platform, and Task 1.4's staged workflow already runs the Linux e2e pass under `xvfb-run` so the platform does not rot.

**Note added 2026-10-20.** The owner ruled: *"yes, Linux for 0.10.0, unsigned"* — recorded where the answer is, in the design's D10 cell, with its own section (§7.6) and the amendments D10's old wording needed (§4.1 item 5, §4.2, §7.5, §9.4). **So the Linux half of this task is no longer unscheduled.** What lands for 0.10.0 is `build.linux` in `console/package.json` (AppImage and `.deb`, x64, `Fugaro.Console-<version>-linux-<arch>.<ext>`, the desktop-entry fields and the deb's `maintainer`), the per-platform fuse target in `console/src/main/fuses.ts` so the same six fuses go into the Linux executable, the per-platform resolution and child-`PATH` lists in `console/src/main/spawn.ts` (§3.4 rules 4 and 5), the build-only CI staged as `docs/pending-workflows/console-linux.yml` plus the Linux legs inside the staged `console-release-linux.yml`, and the website's optional per-platform block — which stays **absent** from `downloads.json` until a release actually carries a Linux asset. **Two phrases of this task's text are withdrawn**: "two **signed** deliverables" and Linux "via the distro channels this repo would have to choose" — Linux is **not signed at all** (no key, no certificate, no owner-run signing step; an AppImage is not code-signed), and it is distributed from the console's own `console-vX.Y.Z` release like the macOS assets. **The Windows half stays exactly where this task put it**, unsigned and unscheduled, as the same amendment says.

### Task 5.2: The v1.1 write actions — SECURITY-SENSITIVE, independent opus review (unscheduled)

**Steps.** Kill, resume and cancel, if the owner says so (**D2**). Each is implemented **by invoking the CLI's own command**, never by writing to RTDB from the app: the typed confirmation, the admin check and the ETag compare-and-set stay in `internal/watch/keys.go` and `internal/cli/budget.go`, where they already are, and where a `budget kill` fails with `AdminDeniedText` — "you are not a budget admin" — when the operator is not one. **That text is the refusal of a write, and round 2 is why the plan says so here**: it is the only place in the CLI that tells a caller anything about the caller's role, it arrives *after* a denied write, and no read-only command reports the role at all. So the console treats **every identity as potentially admin** and its read-only guarantee rests on the exact-argument allow-list alone — it does not detect the role, does not grey out a kill button because the operator looks like a non-admin, and shows this text only when the CLI returned it. The console's own copy of the message is never composed from anything else. Rejected outright: any design in which the console writes.

## Phase 6 — The console's style — APPROVED by the owner on 2026-10-19

**Added 2026-10-19 with [`docs/design/console-styling.md`](../design/console-styling.md),
which is the authority for everything below.** Read that document first: it carries the
evidence (the console ships no stylesheet, `index.html` is script-only, no `.css` exists
under `console/`, `SERVED_FILES` has five entries and no stylesheet, the bundler copies
`.html` only), the screen inventory every rule is written against (the class hooks and
`data-*` attributes a rule needs, and every state variant, read from the code — **the hooks
a rule needs and not every attribute the screens emit**, which is the design's own rule:
an emitted-but-unlisted hook is not a licence to select it, and a rule that wants one adds
it to the inventory in the PR that wants it, design §2), the token set that ships — the
dark one, the owner's Q1 ruling of 2026-10-19, with the light set kept as the design's §3.3
record — with every WCAG ratio computed, the layout, and the wiring in full. This phase is the plan's
*ordering* of that design and nothing more.

**These six tasks are `APPROVED by the owner on 2026-10-19`.** The OWNER APPROVAL GATE at
the foot of this file keeps what it said while the design was `UNAPPROVED`, as history;
this paragraph is the approval. The styling design's six questions — **Q1** does a light
theme ship and is the OS the default; **Q2** where does the owner's override live; **Q3**
does the "phosphor set" ship; **Q4** is PR #629's darwin baseline set superseded; **Q5** is
the forced-colors smoke a gate; **Q6** does the styling work gate the console's first
release — were put to the owner with their options, their recommendation and the reason in
the design's §8, and **the approval answers exactly one of them**: the ruling of 2026-10-19
took **Q1 option (b), so the console ships a dark theme only** — one value set, matching the
`CONSOLE_THEME` the screens are drawn with today, with the design's §3.3 light set deferred
and that table kept as its record. **Q2 to Q6 are not answered here and stand as the design's
§8 writes them**: the approval is the go for Phase 6, not a second ruling on where an
override lives or whether this work gates the first release, and a task that depends on one
of them says so where it is written (Task 6.6's Q4, the MVP list's Q6 below). Every place in
the design and this file that assumed two value sets is marked with the ruling and is a
change a later round must carry, not a licence to build the light theme. **No D-number and no
C-number is allocated to any of them**, deliberately: a number is the owner's to give. The
work starts now.

**Dependencies, all soft but the first.** Phase 6 needs Task 3.4a's bundle step and served
root (`console/tools/bundle-renderer/index.ts` and `SERVED_FILES` do not exist before it),
so it is scheduled after phase 3. It needs nothing else: it touches no Go, no workflow, no
IPC channel, no preload and no spawn. **The goldens are the one hard ordering inside the
phase** (Task 6.6 last, and owner-run) and **Task 6.2 before any visible change** — until
the `<link>` lands, the stylesheet is a file nothing reads and every screen looks exactly
as it does today.

**Two orderings inside the phase are not preferences but consequences, and each is why a
task is shaped the way it is.**

- **`THEME_STYLESHEETS` grows one file per task, and no task may land the whole list
  early.** The list is the theme directory's *whole contents* and the bundler refuses a
  listed file that is **missing** (design §5.2), so a Task 6.2 that named all four files
  would turn `npm run build` red the moment it merged — two of the four do not exist until
  6.3 and 6.4. Task 6.2 therefore lands the list with the two files Task 6.1 created, and
  **6.3 appends `base.css` and 6.4 appends `screens.css`, each in the commit that creates
  the file**. Every intermediate state is a real build of the files that exist; design
  §5.2's table states what the list holds after each task, and its `bundle.test.ts` case
  reads the list rather than four hard-coded names, so it does not need rewriting either.
- **A test that asserts a literal lands with the task that writes the literal.** The
  `--border`-is-decorative case reads `base.css` and `screens.css` and is Task 6.4's; the
  window-background case compares `consoleWindowOptions`' **one** literal, `#0A0E16`,
  against the one `--ground` and is Task 6.5's, because `backgroundColor` does not exist
  before it (*as first written, "two literals against the two `--ground` values": the
  owner's Q1 ruling of 2026-10-19 left one set, so there is one literal and one ground);
  the computed-background e2e assertion is Task 6.6's, because it reads a computed
  background off a screen root and lands with the recording it guards — its dependency is
  the **stylesheet** (6.1 to 6.4), not Task 6.5's theme choice, which the four golden specs
  never read (design §6.2); and
  the forced-colors smoke lands in halves with the rules it tests, its chrome half in 6.3
  and its per-screen half in 6.4. No task's Files, Tests or Command line names a later
  task's artefact.

**The golden tests are skipped while the stylesheets land** (`FUGARO_CONSOLE_GOLDENS=skip`,
which the staged `docs/pending-workflows/console-tests.yml` already sets on the job — Task
1.5's copy-in): a layout change is a picture the committed baselines do not have, and the
recording belongs to Task 6.6, after the last stylesheet. Tasks 6.3 to 6.5 therefore run
the rest of the e2e pass and say so; 6.6 is where the goldens stop being skipped.

**Six PRs, in the file's own numbering** (this section, six `### Task 6.n:` headings, one
row in the Phase summary, five rows in the PR groups table — J to J5 — and four lines in the
File Structure tree, one per `theme/*.css` file):
**6.1** the tokens; **6.2** the wiring, SECURITY-SENSITIVE; **6.3** the layout; **6.4** the
screens; **6.5** the theme switch, the window background and the keyboard; **6.6** the
golden frames, re-recorded. Each is independently mergeable and green on its own; 6.1 is
inert until 6.2, which its PR says; 6.3 and 6.4 each grow `THEME_STYLESHEETS` by their own
file, which is what keeps them green.

### Task 6.1: The token layer

**Scope.** `src/renderer/theme/tokens.css` (the vocabulary — spacing, radii, type sizes,
leading, weights, `--row-height`, the focus-ring width and offset, the motion durations;
**no colour**) and `src/renderer/theme/palette.css` (the value set — every colour token,
each written once, derived from the same logo hue as the website's
[`palette-logo.css`](../../website/src/styles/palette-logo.css), whose existing values are
reused verbatim so the console and the site are one product; the design's §3.2 and §3.3 are
the literal tables). **One of those two tables is the set that ships and the other is a
record, and the owner's ruling of 2026-10-19 is what sets them apart**: the owner took §8's
**Q1 option (b)**, so `palette.css` holds the **dark set alone** — §3.3's light table is
kept as the record of a set no task owns, and the token test fails by name on the four
spellings that would let a light theme back in.

**Files.** the two above, new; `console/README.md` (a note on the theme directory, the way
its `build.files` list is already kept true); `console/test/unit/theme-tokens.test.ts`,
new.

**Tests.** `theme-tokens.test.ts` reads `palette.css` for its colour numbers, so the
literals it checks are the literals the window paints: every pair of the design's §3.4 with
its required ratio (4.5:1 for body text and every state colour on the three surfaces, 3:1
for the accent and the control border, 4.5:1 for a label on a filled control); the
vocabulary complete, by name, with **no colour in it and no step in the palette** — the
design's "identical in both sets" case compares a set with itself under the owner's Q1
ruling of 2026-10-19, so this task replaces it with that pair of directions; and a
**deliberately failing fixture**, asserted to report
**`--text-muted on --surface is 3.1:1, needs 4.5:1`** — one decimal, because that is the
string `website/test/a11y.mjs` actually formats (`ratio.toFixed(1)`, and its committed
fixture prints `3.1:1`), and a **fixture pair named in the case** (the precedent's is
`#646464` on `#14141F`), because the console's real `--text-muted on --surface` is 8.02:1
dark and 6.27:1 light, so no real value produces that line.

**Command:** `cd console && npm run test:unit`

**Depends on:** nothing. It writes two new files and their test, and nothing reads them yet —
the task after this one is what makes the stylesheet a file the console serves. The phase as
a whole is scheduled after Task 3.4a, whose bundle step and served root it extends; that
dependency is the next task's, and its paragraph carries it.

**Commit:** `console: the theme's tokens, and the palette they are painted in`

**What this task does not own, and why the cases land later.** **Three cases of the design's
§6.3 are not in the file this task writes**, and two different reasons put them elsewhere.
The first is *replaced* rather than deferred — the design's **"vocabulary identical in both
sets"** case has one set to compare under the owner's Q1 ruling of 2026-10-19, so this task
writes the two directions that case was meant to catch (every vocabulary token declared, no
colour in `tokens.css`, no step in `palette.css`) and the case as designed is not written at
all. The other two read a file this task does not write, so neither can be written here
without asserting something that does not exist yet:

- **the `--border`-is-decorative case** is Task 6.4's. It is a scan of the CSS text of
  `base.css` and `screens.css`, and both of those are created later, by Tasks 6.3 and 6.4.
  Task 6.4 adds that case, and `theme-tokens.test.ts` is in its Files for it.
- **the window-background case** is Task 6.5's. It compares the background literal
  `consoleWindowOptions` paints against `--ground`, and `backgroundColor` is a key Task 6.5
  adds to `console/src/main/window.ts` — there is nothing to compare before then. Task 6.5
  adds that case, and `theme-tokens.test.ts` is in its Files for it too. **Under the Q1
  ruling it is one literal against one `--ground`**, not the two the two-set design asked
  for, and Task 6.5 says so in its own text.

So this task writes the file with its palette cases; the file's name is honest about the
part of it that is here, the design's §6.3 says which task each of the other two arrives
with, and the one it replaces is named above rather than dropped silently.

**Reviewer tier:** ordinary — it touches no CSP, no protocol and no document.

**Red step — `Expected: FAIL`** — the token test before the palette satisfies it, and one
palette value edited to break one ratio (named by the test, then reverted).

### Task 6.2: The wiring — SECURITY-SENSITIVE, independent opus review

**Scope.** The bundler's stylesheet step (`THEME_STYLESHEETS` — **the two files that exist
at this point**, `'tokens.css'` and `'palette.css'`, with the order rule, a refusal for a
`.css` outside the list, a refusal for a missing one, a refusal for an `@import` or a
remote `url()`, the `dist/bundle/console.css` artefact with its generated-file banner, and
ownership of it in the output directory); **one** new `SERVED_FILES` entry, `/console.css`,
giving six where there were five; one `<link rel="stylesheet"
href="./console.css">` in the `<head>` of each of the three documents, beside the existing
CSP `<meta>`; `build.files` in `console/package.json` and the README's list of served
files; and `bundlerOutputs` in the build-files test, which is what makes a packaged app
without its stylesheet fail rather than ship.

**The list is not four names here, and must not be.** The bundler refuses a *listed* file
that is missing, so a task that named `base.css` and `screens.css` before Tasks 6.3 and 6.4
create them would leave `npm run build` red on merge. The list is the theme directory's
whole contents, so it is introduced with what is in the directory — Task 6.1's two files —
and **Task 6.3 appends `'base.css'` and Task 6.4 appends `'screens.css'`, each in the commit
that creates the file** (design §5.2's table). The test case reads `THEME_STYLESHEETS`
rather than four hard-coded names, so it holds in every intermediate state: at this task
`dist/bundle/console.css` is two files concatenated in list order, served, linked and
asserted, and each later task's build grows by the file it adds. **Verified per task**:
`npm run build` plus the theme-directory-versus-list equality, which fails in *both*
directions, so neither a listed file that is absent nor an unlisted `.css` that is present
can pass.

**Files.** `console/tools/bundle-renderer/index.ts`, `console/src/main/protocol.ts`,
`console/src/renderer/{index,installations,refusal}.html`, `console/package.json`,
`console/README.md`, `console/test/unit/{bundle,csp-document,build-files,screen-literals}.test.ts`.

**Tests, and the mutation each is proved against.** `SERVED_FILES.length` 5 → 6 and the
directory equality in both directions (`bundle.test.ts`); the concatenation and the order
of `console.css`, checked against `THEME_STYLESHEETS` **as a list** rather than against
four hard-coded names, so the case is true here with two files in it and stays true when
6.3 and 6.4 append theirs (`bundle.test.ts`); exactly one `<link>` per document,
same-origin,
relative, with no `media`, `onload` or `integrity` (`csp-document.test.ts`); the `style`
scan of `screen-literals.test.ts` extended so that a `.style` write, a
`setAttribute('style', …)` and a `style` entry in the attribute vocabulary are offences in
any file under `src/renderer/`. **The mutations, one per named failure:** drop
`/console.css` from `SERVED_FILES`; change a `<link>`'s `href`; add `'unsafe-inline'` to
`style-src` (which `window.test.ts` and `csp-document.test.ts` both refuse); put
`style="…"` in a served document; write `el.style.color` in `entry.ts`; remove
`dist/bundle/console.css` from `build.files`.

**The security claim, and the whole reason this task is flagged.** **The CSP is unchanged
and is not loosened.** `style-src 'self'` is already the policy's third directive
(`console/src/main/protocol.ts`, read it), and a `<link>` to `app://console/console.css` is
a stylesheet from this origin — so the directive admits it, `default-src 'none'` is not
consulted, `font-src 'self'` is not consulted (no web font is loaded) and `connect-src
'none'` is not consulted (a `<link>` is not a fetch `connect-src` governs). No inline style
attribute, no `<style>` element, no `'unsafe-inline'` and no `bypassCSP` is introduced, and
three tests fail if any appears. `mediaTypeFor` already maps `.css`, so the protocol
handler's change is the allow-list entry and **nothing else**.

**Command:** `cd console && npm run test:unit && npm run build`, then
`npm run test:e2e -- entry` (the live CSP assertion), then
`GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` — this task edits no Go,
but that is the command this repository reaches a doc change by (rule 10).

**Depends on:** Task 6.1 (the two stylesheets `THEME_STYLESHEETS` opens with — the list may
not name a file that does not exist, so the task that creates them comes first) and Phase
3's Task 3.4a (the bundle step and the served root this task extends; the phase is
scheduled after it).

**Commit:** `console: the stylesheet is bundled, served and linked`

**Reviewer tier:** **SECURITY-SENSITIVE, independent opus review.** This is the task to
attack: can the served root grow a file nothing vouches for, can `style-src` be widened by
one token, can a document gain a second stylesheet or an inline style.

**Red step — `Expected: FAIL`** — each mutation above, run for real.

### Task 6.3: The layout

**Scope.** `src/renderer/theme/base.css`: the reset, the document as the **two-column grid**
the design's §4.1 draws, **the installation rail** and the rules for it — its fixed 216 px
expanded width and its 56 px collapsed width, its header row and its collapse control, its
rows with their name, project, state
glyph **and** state word and spend spans, its selected row's 3 px accent border, its foot
row and its hint — the three bands the entry appends (`section.console-installation`,
`nav.console-views`, `div.console-panel`, held in that order by `test/e2e/entry.spec.ts`,
now the grid's second column), the nav styled as the tab list its `aria-current` already
makes it, the chrome fixed and the panel scrolling, the installation line's three states,
the focus ring as one `:focus-visible` rule off the tokens, the three responsive layers of
the design's §4.2, and **the chrome's** `@media (forced-colors: active)` block of its §4.4.
**The rail's elements are drawn by the entry, and the rules that select them are this
task's, so `entry.ts` is in this task's Files** — the same rule that puts `degraded-num` in
Task 6.4's, one task later: a hook and the rule that selects it land in one commit, because
a stylesheet written against a tree nothing draws is the defect Task 6.4's selector scan
exists to catch. The rail's **keys** are not here; they are Task 6.5's, which is already the
task that gives the nav its own. **And `THEME_STYLESHEETS` gains `'base.css'` in this
commit** — the file and its list entry move together, because the bundler refuses a listed
file that is missing (design §5.2). After this task the artefact is three files: the
vocabulary, the values, and the first rules that read them.

**Files.** `src/renderer/theme/base.css`, new; `THEME_STYLESHEETS` in
`console/tools/bundle-renderer/index.ts` (one entry); `console/src/renderer/entry.ts` (the
rail's elements — the header row and its collapse control, the list and its rows, the foot
row and the hint); `console/test/e2e/entry.spec.ts` (the forced-colors smoke's chrome half,
and the body-children assertion the rail moves); `console/README.md`.

**Tests.** The forced-colors smoke's chrome half — the current view still marked and the
chrome's boundary still visible under `emulateMedia({ forcedColors: 'active' })`, written
first, which is this task's red step, and the rail's own boundary with it, since the rail is
chrome and its rows are the surface the smoke reads; `entry.spec.ts`'s body-children
assertion, which gains `console-rail` as the first of the four shapes it lists, because the
entry appends it before the installation line and the rail is what a reader sees first; a
case that the rail draws **one row per installation the dashboard can read** — the active
installation's, from `installation.current` — with its state word and its glyph, **and draws
no row for an installation it cannot read**: the dashboard's surface has no list read and
this task does not add one to it, so a case that expects a row per stored installation would
be a case against a read no file in this commit brings (design §4.1, §11);
and **the §2-inventory selector scan over `base.css`** — the rail's twelve hooks are the
chrome's and the design's §2.1 lists them (twelve hooks on thirteen elements), so the list the scan checks against is the
inventory's tables (the screens' class literals and the chrome's hooks), not the screens'
literals alone: a scan of the screens' literals sees no rule written against a hook the
entry draws, which is the one shape of rule this task adds. **Not the computed-background assertion**: it reads a *computed* background off a
screen root, so it can pass only once a rule paints one, and the rules that paint a screen
root are the stylesheets this phase lands. It goes with the recording it guards, because
that is where the golden baselines are re-made — **what it waits for is the stylesheet, not
the theme choice**: the four golden specs never read `CONSOLE_THEME`, each draws its run
with the theme it names, and the light run's root already says `data-theme='light'` today
(design §6.2) — **and under the owner's Q1 ruling of 2026-10-19 that run must paint the same
ground as the dark one, so what it waits for is the stylesheet all the more.**

**Command:** `cd console && npm run test:unit && FUGARO_CONSOLE_GOLDENS=skip npm run
test:e2e -- entry` — the four golden tests are skipped, as they are in 6.4 and 6.5: this
task changes pixels the committed baselines do not have, and re-recording them before the
last stylesheet lands is exactly what Task 6.6 exists to stop. The staged console-tests
workflow already sets that variable on the job, so CI is green through the phase.

**Depends on:** Task 6.2. The rules here are the first visible change the phase makes, and
they are visible only once the stylesheet is bundled, served and linked — before that the
file is one nothing reads, and Task 6.2 is the ordering the phase's own header calls "before
any visible change".

**Commit:** `console: the document, the installation rail and the focus rules`

**Reviewer tier:** ordinary, against the design's §5.5 rule explicitly (no style write, no
CSP loosening), because it is one of the two tasks that touch the entry — it draws the
rail's elements, and Task 6.5 adds their attributes and keys.

**Red step — `Expected: FAIL`** — the forced-colors case before the rules exist.

### Task 6.4: The screens

**Scope.** `src/renderer/theme/screens.css`: the rules for Live, Spend, Projects, Run, the
degraded and unavailable screens, the refusal document and the picker, in the order the
design's §2 inventories them, including the tabular-figure and right-alignment rule for the
five number hooks, the density rules of its §3.8, and the state colours of its §3.5 — where
**no state is carried by colour alone**, because every state already has its word in the
DOM (`KILLED by`, `n/a`, `—`, `(partial)`, `silent`, `lost`, `queued`, `NOTIONAL~`,
`degraded`, `polling`, and the cap's own percentage beside its bar).

**The fifth number hook does not exist yet, and this task adds it.** Live, Spend, Projects
and Run each have a `numberCell` that prepends `live-num` / `spend-num` / `projects-number`
/ `run-number`; the degraded screen's `numberCell` (`src/renderer/screens/degraded.ts`)
prepends **no** class, so its figures — the feed age, the counts, the money — have no
tabular-figures and no right-align hook and a rule written for the four existing hooks
cannot reach them (the design's §2.10). That is why `degraded.ts` is in this task's Files:
**one line, adding `degraded-num` to that one helper**, and then the same rule the other
four get. It is the only code change in the styling work, and it is deliberately a class
and not an attribute rule, so the styling stays a stylesheet problem.

**Files.** `src/renderer/theme/screens.css`, new; `THEME_STYLESHEETS` in
`console/tools/bundle-renderer/index.ts` (one entry — **after this task the list is the
whole directory and stays still**); `console/src/renderer/screens/degraded.ts`
(the one-line `degraded-num` above); `console/README.md`;
`console/test/unit/theme-tokens.test.ts` (the `--border`-is-decorative case the design's
§6.3 places here, because this is the first task at which both `base.css` and `screens.css`
exist — Task 6.1 writes the file and leaves that case out for exactly that reason);
`console/test/e2e/{live,spend,projects,run,degraded}.spec.ts` (the forced-colors smoke's
per-screen half).

**Tests.** A case that every selector in `screens.css` names a hook the design's §2
inventory lists — checked against a list derived from the inventory's tables, the screens'
own class literals **and the chrome's hooks of §2.1** (the rail's twelve, which Task 6.3
draws and this scan must see), so a rule that styles a class nothing emits fails; the `--border`-is-decorative scan of
`base.css` and `screens.css`; the forced-colors smoke per screen — every state word still in
the DOM and the row boundary still visible. **Not the computed-background assertion**,
which belongs with the recording it guards, for the reason Task 6.3 gives: it waits on the
stylesheets that paint a screen root, not on the theme choice, which the four golden specs
never read.

**Command:** `cd console && npm run test:unit && FUGARO_CONSOLE_GOLDENS=skip npm run test:e2e`

**Depends on:** Task 6.3 (the reset, the document and the bands these per-screen rules are
written on top of) — and Task 6.1 for the tokens every rule reads, which 6.3 already
depends on.

**Commit:** `console: the screen rules, and the fifth number hook`

**Reviewer tier:** ordinary.

**Red step — `Expected: FAIL`** — a selector planted against a class no screen emits.

### Task 6.5: The theme switch, the window background and the keyboard

**The owner's Q1 ruling of 2026-10-19 removes the switch this task is named for, and the
scope below is the two-set design it was written from.** One value set ships, dark, so there
is nothing to switch between: `CONSOLE_THEME` stays the literal it is today, no
`matchMedia` read and no `nativeTheme` query is added, and the window paints **one**
background literal, `#0A0E16`, against the one `--ground`. What is left of this task is
therefore **the window background and the keyboard**, and both are still needed — a window
with no `backgroundColor` flashes the platform's default before the first paint whatever the
theme, and the nav has no keydown handling at all. **A later task that wants the switch back
needs a light set to switch to and a new ruling to ship it**, and the heading above is kept
as the record of what this task was scheduled to do. *The scope as designed, before the
ruling:*

**Scope.** `CONSOLE_THEME` stops being the literal `'dark'` and becomes the answer of a
`themeChoice()` that reads `matchMedia('(prefers-color-scheme: dark)')` and subscribes to
its `change` event — `:root`'s `color-scheme: light dark` is the OS default, and the
`data-theme` attribute the screens already emit is the single switch, which also covers
the script-free `refusal.html` (the OS alone themes it, which is the strongest argument for
the OS default); the entry stamps `data-theme` on the mount it draws the chrome into; the
picker's script stamps its document; `consoleWindowOptions` gains a `backgroundColor`
**equal to the `--ground` of the theme it is about to paint**, read from
`nativeTheme.shouldUseDarkColors` — `#0A0E16` when true, `#F2F6FA` when false — and
re-set from `nativeTheme.on('updated', …)`, so an OS appearance change while the window is
open does not leave the other theme's ground behind the next paint (the design's §4.1:
a single pinned literal would flash **dark** on a light-OS machine, which is the very
flash the background colour exists to remove). `nativeTheme` is core Electron, needs no
dependency and touches no CSP; the main process still cannot read the CSS, so the token
test holds **both** literals equal to their set's `--ground`. And the nav gains the
WAI-ARIA tablist behaviour it lacks — `role`,
`aria-selected`, a roving `tabindex` and Arrow/Home/End handling that calls the existing
`show(id)` — **and the installation rail gains its own**, because the rail is a tablist in
its own right (design §4.1): the same roving `tabindex` over its rows, Arrow/Home/End
between them, Enter to select, and the **shortcut keys** that select by position
(Command+digit on macOS, Control+digit elsewhere), since `entry.ts` has no keydown handling
at all today. **The owner's Q1 ruling of 2026-10-19 removed the switch this task is named
for and left both keyboards**: the scope above is kept as the record of what the task was
written to do, and what it rules in is the paragraph above plus the nav's keys and the
rail's.

**Files.** `console/src/renderer/entry.ts`, `console/src/main/window.ts`,
`console/test/unit/{entry-wiring,theme-tokens}.test.ts`. *As designed:* the same list with
`console/src/renderer/installations.ts` (the picker's script, which the ruling leaves alone)
and `console/test/unit/window.test.ts` — the two files the ruling drops, and the design's
§7.5 keeps the same pair as the same record.

**The CSP and `ElementLike` are untouched**, and this is checked rather than asserted: a
`matchMedia` read is not a DOM write and not a network call, `data-theme` is a `data-*`
attribute, and `role`, `aria-*` and `tabindex` are attributes — so `ElementLike` needs no
widening and no style is written. *The `matchMedia` half of that sentence is the two-set
design's, which the owner's Q1 ruling of 2026-10-19 removed; the ruled task writes
attributes only, and none of them is `data-theme`.*

**Tests.** `CONSOLE_THEME` is the literal `'dark'` and reads no media query; **the
background literal drifting from `--ground`** — one literal, one direction, the ruled
scope. *The two-set form of the same case:* the literal for each theme drifting from that
theme's `--ground` (both directions, since there are two literals, with
`shouldUseDarkColors` deciding which is set). **That case is this
task's and cannot be Task 6.1's**, which the plan said before this round: the two literals
are added to `console/src/main/window.ts` here, so before this task there is no
`backgroundColor` to compare with anything (design §6.3, §7.5). And a roving-tabindex
regression **in either tablist — the nav's and the rail's — plus a shortcut key that
selects the row at the wrong position**, which is the case the rail's keys are proved by
(design §4.1). **The two-set wording in this paragraph is the record of what the case was
written as, and the owner's Q1 ruling of 2026-10-19 removed it**: one literal, one
direction, as the paragraph's first sentence says.

**Command:** `cd console && npm run test:unit && FUGARO_CONSOLE_GOLDENS=skip npm run test:e2e`

**Depends on:** Task 6.2 (the served, linked stylesheet — under the ruling the window's
background is a duplicate of a `--ground` that only exists once a stylesheet is served, and
under option (a) it would have been a choice about a document that has one) and Task 6.1
(the `--ground` value its case compares the window's literal against). It needs nothing later in the
phase: this task's own run skips the goldens, and the re-recording is the phase's last
task — which is a sequence, not a dependency: the last task's assertion waits on the
stylesheets, not on this task's theme choice (design §6.2).

**Commit:** `console: the window paints its ground first, and the nav and the rail take the keyboard` —
what this task commits under the ruling: one background literal, **the nav's tablist
behaviour** (its `role`, its `aria-selected`, its roving `tabindex` and its Arrow/Home/End
handling) **and the rail's** (the same over its rows plus Enter to select and the shortcut
keys that select by position — the rail is a tablist in its own right, design §4.1). *The message as written for the two-set design was `console: the OS chooses the
theme, and the window paints its ground first`*, and the switch that half of it names is
the one the owner's Q1 ruling of 2026-10-19 removed.

**Reviewer tier:** ordinary, against the design's §5.5 rule explicitly (no style write, no
loosening).

**Red step — `Expected: FAIL`** — each of the three above.

### Task 6.6: The golden frames, re-recorded

**Scope.** The eight baselines per platform that the four golden specs already name, and
the per-spec assertion that the **computed background colour differs between the schemes**
— the assertion the suite is missing today, since every spec reads `data-theme` off the
screen root and passes with no stylesheet at all. **This task owns it as the guard on the
recording it re-makes**, and its precondition is the stylesheet rather than the theme
switch: the assertion reads a *computed* background off a screen root, and the rules that
paint one are Tasks 6.1 to 6.4's. The four golden specs draw their screens directly with the
theme they name — `mount(theLastFrame(), 'light')`, `renderSpend(factory, model, 'light')`
and their two siblings — and every screen stamps that argument on its own root, so the light
run has said `data-theme='light'` since the screens were written and `CONSOLE_THEME` appears
in none of them. The frames are identical today only because nothing paints: no stylesheet
is served or linked, so every computed background is `rgba(0, 0, 0, 0)` (design §1.3,
§6.2). **Under the owner's Q1 ruling that assertion cannot read "differs between the
schemes"**: one value set ships, so the `'light'` and `'dark'` runs stamp different
attributes and must paint the *same* ground, and what the case asserts becomes — in the
task that lands it — **that both runs compute the shipped `--ground` and therefore agree**,
which is the same guard on the same recording (a tree with no stylesheet still fails it,
since both would read `rgba(0, 0, 0, 0)`). The baselines themselves are unchanged: still
eight, still one set of pictures, still owner-run. The forced-colors smoke is **not** in this
task: it landed with
the rules it tests, its chrome half in Task 6.3 and its per-screen half in Task 6.4.

**Files.** `console/test/e2e/{live,spend,projects,run}.spec.ts`; the committed
`console/test/e2e/__screenshots__/**` PNGs.

**The recording is owner-run**, once per platform (`updateSnapshots: 'none'` makes a
missing baseline fail rather than be written), and it **supersedes the draft PR #629's
darwin set**, which is a picture of the unstyled tree — pending the owner's Q4.

**Command:** `cd console && npm run test:e2e` after the owner records.

**Depends on:** Task 6.4 (**hard**). The computed-background assertion it lands reads a
computed background off a screen root, and the last rules that paint one land in Task 6.4's
`screens.css`; before that the reading is `rgba(0, 0, 0, 0)` in both schemes and the case
could only be skipped. It does **not** depend on Task 6.5: the four golden specs draw their
screens with the theme they name and never read `CONSOLE_THEME`, so the theme switch changes
no picture these specs take (design §6.2). It is still the phase's last task — the baselines
are a picture of every stylesheet the phase writes, and the recording is the owner's to run —
which is why Task 6.5 precedes it, as sequence rather than as dependency.

**Commit:** `console: the golden frames, re-recorded in both themes` — the message as
written for the two-set design; under the ruling the frames are one set of pictures with
both runs painting the same ground, and the message should say so when this task runs.

**Reviewer tier:** the assertions ordinary; the recording is the owner's.

**Red step — `Expected: FAIL`** — the computed-background assertion with the `<link>`
removed from `index.html`.

**What this phase does not do.** It changes no CSP directive, no protocol refusal, no
preload surface, no IPC channel and no spawn contract (design §5.5, §11). It allocates no
check number: nothing here is a live step and nothing touches
`docs/gcp-live-checklist.md`. It creates no workflow.

## Cross-task dependencies

Every task's "Depends on" is a *build* dependency, and none of them is a merge dependency beyond its own phase: phases 1, 2 and 3 each leave a working artifact even if the next phase never ships, and phase 4's two owner-run steps (Tasks 4.3 and 4.5) sit after the agent-written packaging work, so 4.1/4.2/4.4 merge as ordinary reviewed PRs whether or not the owner ever runs the rest. The two places a reader might expect a dependency that does not exist: **phase 2 does not depend on project-centric phases 1 or 4** — it renders what exists and says so when a section is not built (Task 2.3) — and **phase 4 does not depend on the CLI's release process being changed**; it adds an artifact to the existing tag, which is what D11 decided.

**What ships with the MVP.** The first console release (0.8.5) ships phases 0 to 4 and **requires**: (1) the hardened shell (Tasks 1.1 to 1.3); (2) the four screens (phase 2); (3) the honest-when-missing states (Task 3.1); (4) the version-skew handshake **and its startup wiring (Tasks 3.2 and 3.3: both, since 3.2 alone builds a check nothing calls)**; (5) **the renderer entry (Task 3.4: 3.4a, 3.4b and 3.4c, approved by the owner 2026-10-18; 3.4a is implemented). Without it no window has a preload, no handler is registered, no document loads a script and no installation is chosen, so nothing is drawn from a real CLI**; (6) the signed, notarised build and its round trip (Tasks 4.1 to 4.5, Check 59, owner-run); and (7) the release gate (Task 4.6). A console build missing item 4 or item 5 does not ship, whatever else is green. **Phase 6 — the console's style — is not on this list**, which is the design's recommendation and the owner's to overturn (its Q6): the MVP list does not name a stylesheet, and a release held by a design review of colours is held by its least load-bearing part. A console that ships unstyled is a console whose screens say the true thing in the wrong shape; a console held for six styling PRs is a console nobody has.

**Five dependencies that are real and are *merge* dependencies, not build ones**, listed here because a reader checking the "nothing in phase *n* is required by phase *n-1*'s merge" claim would otherwise read them as exceptions it missed:

- **Task 1.5 (the owner's copy-in of the tests workflow) must merge before Task 4.6 (the gate edit).** Merged the other way round, `scripts/release-gate.sh` requires a check that no workflow produces and **every tag on this repository is refused** — including the releases that ship none of the console. This is the one ordering in the plan whose inversion breaks other people's releases, and it is why 4.6 is its own PR group (I) rather than sharing G's. **The ordering alone is not enough, and the plan says so where it matters**: the gate names a check, so the check must exist on *every* `main` commit and on every pull request head for the `identical_tree_checks` fallback — which is why the console job carries no `paths:` filter and why Task 1.4 asserts its name and triggers (`TestConsoleWorkflowJobIsGateable`). A path-filtered, correctly-ordered console job would still refuse every tag. **Round 2 is why this bullet names Task 1.5 and not Task 4.5**: the copy-in of the tests workflow moved to phase 1, the signing workflow's copy-in stayed in phase 4, and the gate follows the job it names.
- **Task 4.2 (the staged signing workflow) must merge before Task 4.5**, trivially — the owner has nothing to copy otherwise. Task 4.2 does **not** gate Task 4.6, and the signing job is not the check the gate names.
- **Task 3.3 (the handshake wired at startup) must merge before the console MVP is released, and after Task 3.2 (#514).** It gates the release rather than any other task's merge: Task 4.6's release gate section and "What ships with the MVP" both name it.
- **Task 3.4 (the renderer entry, approved by the owner 2026-10-18) must merge before the console MVP is released, and after Task 3.3.** Its three parts form a small DAG:
  - **3.4a** (the preload, the handlers and the document's script) depends on 3.3;
  - **3.4b** (the installation store and the picker) depends on 3.3 and on C8 and C9 (answered 2026-10-18). Its store, validation and handshake commits may be written in parallel with 3.4a, and its picker window waits for 3.4a's bundle step and sender narrowing;
  - **3.4c** (the screens wired to the real source) depends on both.

  Task 3.4's shared-files table names the files more than one part touches, and the open PRs (#540, #541 and #548) that touch them too.
- **The recordings of Task 1.4 do not gate any merge at all.** They are owner-run data, they land when the owner runs the recorder, and the fixture harness is green without them (the synthetic frame, C7). A stale or absent recording is a test failure, not a broken build.

**And the fact a reader should not have to infer — corrected in round 2, because the previous revision had this wrong by three phases: no CI runs a single console test until Task 1.5.** The console's tests exist from Task 1.4 onward; until the owner copies `console-tests.yml` in, they are run by the task commands above and by whoever is working on them, and nothing else runs them. Task 1.5 closes that gap **inside phase 1**, so from Task 1.5 onward — the whole of phases 2, 3 and 4 — every pull request and every push to `main` runs the console's tests, and the only uncovered window is Tasks 1.0 through 1.4: the toolchain, the window, the IPC allow-list, the source, and the fixtures. **Those four pull requests have no console CI, and the plan says so rather than claiming CI "from the first pull request"** — a claim round 2 made in the design's §9.4 and §7.5 and round 3 corrected, because the copy-in is a task, not a merge hook, and an owner-run one at that. The signing workflow is still staged and unapplied until Task 4.5, which is deliberate (it holds the Apple material) and is not a testing gap: what it adds is a tag-time job, and the tests it would run have been running since Task 1.5.

## Decisions this plan defers to the owner

Its own, continuing the design's D-series with **C1 onward**, each vetoable before its phase starts. **The owner's answers of 2026-10-15 cover this list too**: the design's D1 to D20 were answered (the table in Task 5.0 mirrors them) and nothing on this list was objected to, so every row below stands as recommended. **C8, C9 and C10 were added on 2026-10-17 with Task 3.4 and were answered by the owner on 2026-10-18**; their rows carry the answer in the last column.

| # | Question | Recommendation | Owner's answer 2026-10-15 |
|---|---|---|---|
| **C1** | Is phase 0's boundary test (Task 0.3) enough to keep the Go module untouched, or does the owner want `console/` excluded from the module by a build constraint too? | **The test is enough.** `console/` holds no Go file, so `go build ./...`, `go vet ./...` and `gofmt -l .` never see it; a build constraint would add a mechanism where a test already gives the guarantee. The boundary it enforces is **imports and `go:embed`, not bytes**: the one-entry allow-list (Task 0.3) is what lets the docs tests read `console/docs/json-contract.md` as a string, and any second entry is a deliberate edit to a test. | **Accepted as recommended.** |
| **C2** | Does the console need its own `docs/release.md` section, or does it belong in the CLI's? | **Its own section in the existing file**, so the two artifacts' gates and Highlights are read in one place — `docs/release.md` is the single source of truth for the tag. | **Accepted as recommended.** |
| **C3** | Who owns the recorded fixtures' refresh, and when? | **The owner, at each release that changes `watch --json`**, with the redactor failing closed (Task 1.4) so a stale fixture surfaces as a test failure rather than a plausible screen. | **Accepted as recommended.** |
| **C4** | Does the console need a Homebrew cask of its own, and does it keep the CLI cask's `xattr -dr com.apple.quarantine` postflight? | **A cask of its own, and no quarantine hook**: the hook exists because the CLI's binaries are not notarised, and a notarised, stapled console does not need it. Removing the hook is the visible proof the notarisation works. | **Accepted as recommended.** |
| **C5** | Does the console ship a `--json` mode of its own (a headless `console --json`) as the CLI does? | **No.** The CLI already has the machine view; a second one would be a second contract to version. The console is a renderer. | **Accepted as recommended.** |
| **C6** | Does the release gate gain the console check (Task 4.6) in the same release that first ships the console, or the one after? | **The same release.** Waiting a release means the first console build ships ungated, which is the hole Task 4.6 exists to close; by then the check has run on **every pull request and every push to `main` since Task 1.5**, three phases of track record rather than one task's worth, because round 2 moved the copy-in of the tests workflow into phase 1. | **Accepted as recommended.** |
| **C7** | Task 1.4's eight recordings are owner-run. Does phase 1's PR merge with the synthetic fixture and the recordings follow, or does it wait for them? | **Merge with the synthetic fixture** — the recorder, the redactor and the replay source are the reviewable artifacts, and the recordings are data that lands in their own commit. Blocking the shell on a live sandbox session would put owner work on the critical path of an agent-runnable PR, which is the thing this plan avoids everywhere else. | **Accepted as recommended.** |
| **C8** | (Task 3.4b, proposed 2026-10-17.) May the installation list be persisted? D13 says the choice is "the app's own setting". §5.1 rule 10 and §7.4 say the app writes nothing but the opt-in spawn log. | **Persist it**: one `installations.json` in Electron's `userData` directory, `0600`, schema-closed, bounded, never a credential and never under the CLI's own config directory, written only when the operator adds, chooses or removes one. 3.4b amends rule 10, §7.4 and D20 to name it. The alternative is the in-memory store `createInstallationStore` has today, with the picker on every launch. | **Answered by the owner 2026-10-18: yes.** Persist it as recommended: one bounded, schema-checked `installations.json` in `userData`, mode `0600`, tuples only (project name, config path, binary path, optional checkout directory), never a credential, and never read from the operator's own CLI config. The design is amended with it: §5.1 rule 10 and §7.4 become "the app writes the spawn log, when asked, and this installations file, and nothing else". |
| **C9** | (Task 3.4b, proposed 2026-10-17.) Where does switching happen? D13 says "a picker in the header". | **The header names the installation, and a separate picker window opened from the application menu adds, chooses and removes.** The dashboard's bridge then stays read-only (plus `startup.compatibility`). The picker gets its own document, preload and channel prefix, and takes no path from the renderer: native dialogs answer pick tokens. | **Answered by the owner 2026-10-18: yes.** As recommended: the installation picker is a **separate small picker window** opened from the application menu. |
| **C10** | (Task 3.4a, proposed 2026-10-17.) In `reduced` mode the main process refuses `spend.report` and `budget.show`. The kill state also rides inside `live.watch`. Should the main process strip it? | **No.** Rewriting a CLI document in the main process would be a second schema. The renderer's gate is the one defence for the kill banner inside the Live screen, and `REDUCED_VIEW_OPERATIONS.kill` is `[]`, named by a test. | **Answered by the owner 2026-10-18: no.** In reduced mode the kill-switch fields that arrive inside `live.watch` **stay visible**; the main process does not strip them. |

## Open items, listed rather than fixed

Review round 4 raised five findings, all of them fixed in place: the PR-link rule no longer hardcodes GitHub (it builds **no** link until a document names the run's provider, which is the new **gap 7** of design §4.3 — the reference `https://github.com/<repo>/pull/<n>` was wrong for the Bitbucket sandbox repository every live check here uses, and worse, it passed the host allow-list while naming a repository the operator does not own); the fixture count is **eight** everywhere, matching the design's own list; the child's `PATH` is now stated exactly and includes every prefix `doctor`'s `exec.LookPath` needs, with the environment view labelling a tool it cannot see as *not on the PATH the console gives the CLI* rather than "not installed"; §3.4 rule 7's config-read prohibition now has the named grep-style test the spawn log already had (`configread.test.ts`); and §12 item 1 no longer denies that `report.JSON` exists — it is the exported encoder of `JSONDoc`/`JSONLine`/`JSONTokens`. Review round 3 raised a set of findings, and this is what happened to each — the fixes are in place, and the list below carries what is still open. **Fixed in round 3**: the staged-workflow tests in Tasks 1.4 and 4.2 no longer assert the file's *absence* under `.github/workflows/` (they assert byte-identity to the staged copy if it is there at all, so main's `test` does not go red the day the owner copies one in); `TestCheck59NeverNamesBelongAsATarget` no longer forbids the mutation-control line Check 59 has to write, and the add-an-installation screen now checks the checkout's project against `--project` before storing an installation; Check 59 is split into **59a** and **59b** with the launch step that makes the spawn log possible written down; the "CI from its first pull request" claim is withdrawn wherever it appeared (Tasks 1.0 to 1.4 merge with no console CI); Task 1.2's never-table gained `project repos add|rm`, `project policy publish` and `project sync`; the Playwright harness is Task 1.1's, not Task 2.0's; assertion (4) is worded as what the FACT lines prove ("spawned nothing for `belong`"); and the cheap lows below were fixed where they were: the `repository` versus `repo` field name (there is no `repository` field on any shape), Global Constraint 9's garbled "neither, either, or (never) both" clause, Task 1.5's citation of Global Constraint 7 rather than 9, the README test's `…Row`/`…Rows` naming, the "four grants" count (five for a budget admin), `engines.node` rather than `engines` in the design's CI section, `latest-mac.yml` named as electron-updater's manifest format rather than Squirrel's, and the owner's answers themselves. Review round 2 raised a set of small findings. What was cheap to fix is fixed and said so where it lives (the `--project` exception in Task 1.3 and §3.4 rule 3; the admin-role claim in Task 5.2 and D14; the `D6.4` citation, which is now D15, in §6.2; `readRun`'s real file, `internal/cli/runs.go` rather than `watch_queued.go`, in §2.1; the log panel bounded-and-linked in §2.3 and Task 2.3; and `pr_url` **host-checked and not opened as-is**, in §5.1 rule 2 and Task 2.3 — where round 4 then withdrew the rebuild itself, because no document says which provider the repository is on). **Round 3's findings are all fixed rather than listed**, so this section carries **no new item from that round**: everything it raised is in place where it lives, and the round-3 paragraph above names each one. What remains below is the set of round-2 items that are still open, each with the reason it is left open rather than answered by assertion:

1. **The unit-test runner is still unnamed.** The plan pins `actions/setup-node` to `console/package.json`'s `engines.node` and mirrors the emulator precedent's `"node": ">=20"`, which is the toolchain *version* story; it does not name whether `npm run test:unit` is vitest, `node:test`, or something else, nor the e2e invocation behind `xvfb-run`. That is Task 1.0's decision — it is the task whose whole subject is pinning the toolchain and whose review criterion is that every pin is exact — and writing a runner name here that Task 1.0 may reasonably change would be a plan asserting a version of a task it does not own. **What the plan does fix is the shape**: `npm ci --ignore-scripts --no-audit --no-fund`, a committed lockfile, and two scripts the CI file calls by name.
2. **The `logs` line cap has no number.** §2.3 says "bounded", §5.1 says "a hard console-side line cap and a timeout", and Task 2.3 tests that the panel renders exactly the cap and truncates beyond it — but the cap itself is not a number in either document. It belongs with the JSON contract Task 0.1 pins, because it is a property of how much of a run's log the console will hold, and a number chosen here would be a guess reviewed as a decision.
3. **The version handshake's parser tolerance is stated, not pinned — settled by the owner on 2026-10-22, and now pinned by tests rather than by prose.** The design says the handshake runs `fugaro version` bare and compares a supported range, and that a build reports `dev` when it is not built with the release `-ldflags` (`internal/cli/root.go`). **What a `dev` build did to the range check was not decided**: refusing would make the console unusable for anyone building the CLI from source, and accepting would make the handshake silent exactly where it is most needed. **The owner's answer of 2026-10-22 takes the second door and closes it against silence: a dev CLI build — the version string `dev`, which is not a version at all — is accepted, in the *reduced* mode, with the banner, and it is not a refusal.** That is what the tree does and now what its tests hold: `parseCliVersion` recognises the literal `dev` as its own kind (`console/src/shared/version.ts`), `classifyCliVersion` places it `reduced`/`unplaceable` with every `REDUCED_VIEWS` entry withheld, and `startupWindow` builds the dashboard window for it (`console/test/unit/version.test.ts`, `console/test/unit/window.test.ts`: the parse, the placement and the window, the last two through the main process's own `runVersionHandshake`/`startupWindow`). **The skew rules for released versions are unchanged** — the range, the floor and the three placements of a real version are the same rules as before — and **a line that is not a version and is not `dev` is still the refusal design rule 4 asks for**: `dev` is the CLI's own constant (`var Version = "dev"`), so recognising it is reading the CLI's grammar rather than guessing at a word, and the acceptance is deliberately not widened to "any non-semver line", which would place a string the console cannot read. The item stays listed here rather than deleted because the underlying gap does: gap 2 (a machine-readable `version --json`) is what makes the whole parse disappear, and until it exists the tolerance is a decision rather than a fact about the CLI.
4. **Whether the Projects screen gains the policy source inside this plan's lifetime** depends on project-centric phase 1 (PR #390) landing and on D16's ruling; the plan's answer is the explicit *not readable with this CLI* state, and nothing more is promised.
5. **Dependabot's scope is not extended here — settled by the owner on 2026-10-22, and staged for them to apply rather than applied.** §5.2 notes that the console adds a second thing Dependabot should watch — `console/package.json` and its lockfile — and that Electron's own binary is not an ordinary npm dependency at all. `.github/dependabot.yml` is not under `.github/workflows/`, so an agent may edit it, and this plan deliberately did not: it belongs with Task 1.0's pins, and the owner may prefer to keep Dependabot's scope where it is rather than have a weekly PR stream behind an approval gate. **The owner's answer of 2026-10-22 takes the extension and keeps the decision: the file is staged, with its copy-in step, and they apply it.** So `.github/dependabot.yml` is unchanged in this tree, and the whole of what it should read once the console is watched — the `github-actions` entry that runs today, unchanged, plus one `npm` entry for `/console`, weekly and **grouped** over minor and patch so a week's bumps arrive as one pull request while a major (Electron's above all) arrives on its own — is `docs/pending-workflows/dependabot-console.yml`, whose copy-in is a `cp` **over** `.github/dependabot.yml` rather than a copy to a path named after it. `TestDependabotConsoleStagingIsPinned` (`internal/cli/docs_dependabot_console_test.go`) pins the two entries, the group, the copy-in command and the staging state in both directions, and `docs/pending-workflows/README.md` carries the step. **Applied on 2026-10-23 on the owner's approval:** the staged file was copied over `.github/dependabot.yml` and its staged copy removed, so the console entry is in the file that runs; what no test can observe is what Dependabot does with it.
6. **Whether `watch --json` should keep emitting `pr_url`.** Now that the console checks the host and **builds no link at all until the provider is known** — the rebuild from the run's own repository and number waits on gap 7 — `repo` + `pr_number` on `JSONFinished`, `repo` + `pr` on `runview.Row`, and **no `repository` field on any shape** (Task 2.3) — the field is a convenience the console never trusts. Removing it would be a CLI change in a CLI plan, and §4.3's gap 6 is the neighbouring one (it *adds* the pair to `JSONRun` rather than removing `pr_url` from anything); keeping the field is harmless as long as it is **never opened as-is**, and gap 7 is what decides whether there is a rebuild to stand — which is why that rule is a test in Task 2.3 and the field's fate is not decided here.
7. **The update check is duplicated by construction.** Task 4.4 says the console "reads the same release metadata the CLI's `internal/updatecheck` reads and honours the same opt-out" — it cannot import that package, so the TypeScript side re-implements the comparison. Two implementations of one rule is a drift risk this plan names and does not solve; the mitigation is Task 4.4's test that the opt-out is honoured all three ways and that a downgrade is refused, which is the behaviour both implementations must agree on.
8. **Check 59's number is contended, and now carries two sections.** PR #393 takes 58; whichever of the two merges second renumbers its own section, summary row and references — and for this plan that means renumbering **both** `## Check 59a` and `## Check 59b` together, since they are one number with two sections (Task 4.3's `TestCheck59IsSplitIntoAAndB` asserts the pairing, so a renumber that moves one and not the other fails a named test rather than shipping a checklist whose rows point at sections that do not exist). That is this repo's convention and not a defect, but it is a merge-order dependency between two PRs that this plan cannot resolve from here.

## OWNER APPROVAL GATE

**Phase 6, and only Phase 6, is `APPROVED by the owner on 2026-10-19`.** The owner approved
the console styling design (`docs/design/console-styling.md`, merged as #634) and the six
tasks of Phase 6 — 6.1 to 6.6 — on that date, and Task 6.1 is the first of them to be built.
The approval is the go this gate's own text asks for, and it is scoped: it says nothing
about phases 0 to 5, whose tasks stay unapproved below, and nothing about the styling
design's other five questions beyond what the phase's section records. The rest of this
section is the gate **as it was written on 2026-10-15, kept as history**.

*The gate as first written:* **The decisions are answered. The tasks are not approved, and
this gate stands.** On 2026-10-15 the owner answered the design's D1 to D20 — D1, D3, D4 and D11 as the design's §10 records them and Task 5.0's table mirrors, the other sixteen accepted as recommended — so the first half of this gate is behind us: **answering a decision is not approving the tasks**, and the owner said so. Concretely: `docs/design/desktop-console.md` and this file are the deliverable of the PR that adds them; Tasks 0.1 onward are future work; and **no task in this plan starts until the owner gives an explicit go on the plan itself**, which is a separate act from answering D1 to D20 and has not happened. If a later ruling reverses an answer, Task 5.0 rewrites the design to it first. The three that gate the most: **D1** (the sidecar, which decides everything about the data plane), **D3** (this repository, which decides the CI and the module), and **D4** (Apple signing, which would be the first private key this repo's release path has ever held, and which every step of phase 4 and all of Check 59, and both copy-ins (Tasks 1.5 and 4.5), is owner-run because of).
