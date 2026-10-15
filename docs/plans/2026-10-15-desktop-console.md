# The Fugaro desktop console: implementation plan

**Goal:** build the console the owner's spec asks for in its section 6 — a cross-platform Electron application that is a richer, graphical `fugaro watch`: live task monitoring, spending monitoring with graphs, and project-level views consistent with the project-centric model — **without a new backend, without the app ever holding a credential, and without the app ever writing anything**. The reconciled design is [docs/design/desktop-console.md](../design/desktop-console.md). **No code in this plan has been written or run; this is a design-to-code plan.** **This PR (the one that adds this plan) is docs-only: it touches exactly two files — this plan and the design — and nothing else at all**, not `docs/gcp-live-checklist.md` (the check number this plan reserves, 59, is *named* here and its row and section are written by Task 4.3 when the packaging phase ships), not `go.mod`, not any Go, Terraform or product-behavior file under the "Files" lists below. Tasks 0.1 onward are future work, gated on the owner approving the design and this plan.

**Spec:** [docs/design/desktop-console.md](../design/desktop-console.md), written from the owner's verbatim draft [project-centric-spec-source.md](../design/project-centric-spec-source.md) §6 ("Forward-looking: Fugaro desktop console") and from `docs/design/project-centric.md` §9, which says of this work, correctly, "**No work in the plan.**" The design's decisions are **D1 to D19** (its §10), each with a recommendation; this plan defers to them and adds its own **C1 onward**, each independently vetoable before its phase starts.

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

**Releases.** The console is **its own artifact published from the CLI's own `vX.Y.Z` tag** (design D11): one release train, one gate (`scripts/release-gate.sh`), one Highlights section — which is what `docs/release.md`'s "one tag releases everything" means once a second deliverable exists. Its first shippable version is whatever release carries phase 4. **This changes one thing in this repo's release path and the plan says it plainly: until now every signature here has been keyless cosign over GitHub OIDC, and the console's Apple signing would be the first private key the repo has ever held** (`docs/release.md`: the checksums are signed, the binaries are not Apple-signed, and the Homebrew cask papers over Gatekeeper with an `xattr -dr com.apple.quarantine` postflight hook). Every step that touches that key is owner-run (Tasks 4.3 and 4.5 and Check 59), and no agent-run step in this plan or any other produces, stores, reads or transmits it.

## Phase summary

| Phase | What ships | Owner-run steps | Depends on |
|---|---|---|---|
| **0. The ground** | the JSON contract written down and pinned (`console/docs/json-contract.md`); the child-process contract settled; the Go/`console` boundary with three tests holding it (imports, `go:embed`, and the one-entry read exception) | nothing | nothing |
| **1. The shell** | the hardened Electron main process, the frozen IPC allow-list, `FugaroSource` with its `cli` and `fixture` implementations, the recorder/redactor | **the seven fixture recordings** (Task 1.4) | phase 0 |
| **2. The screens** | Live, Spend, Projects, Run detail — with the availability states and the replay buffer | nothing | phase 1 |
| **3. When things are missing** | the no-Firebase and CLI-absent modes, the stale/gap rendering, the version-skew handshake | nothing | phase 2 |
| **4. macOS packaging** | the builder config and entitlements; the staged signing/notarisation workflow; the update feed; **Check 59** | **all of it**: the certificate, the API key, the first signed build, the update round trip (Check 59); and Task 4.5's copy-in of the staged workflow | phase 3 |
| **5. Later, unscheduled** | Windows and Linux packaging; the v1.1 write actions | each, when the owner decides | phase 4 |

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
| **59** | **this plan, phase 4**: the console's first signed, notarised and stapled macOS build, and its update round trip | **reserved here; this PR takes 59, not 58** |

**How 59 was chosen, and the rule that stops two open PRs from colliding, stated so a later reader can re-check both.** "The next free number" cannot be read off `main`'s section headings alone, because a number is taken three different ways — a written section, a written summary row whose section lives in another document, and a reservation in a PR or plan whose phase has not shipped — and because **two open PRs can both correctly believe they take the same next free number**. What settles it is which one lands first; a number is chosen at the moment of merge, not at the moment of writing. So, in this order, all of it on `origin/main` at `b9d5c5d`:

1. **Every `## Check N` section written in `docs/gcp-live-checklist.md` runs 1 to 47**, consecutively through 43a-43f: 45 and 46 are there (added by `2be38e1`, "plan: a senior reviewer through a provider route, non-Claude now and Claude by opt-in (#359)"), and so is 47 — **#359 and #363 are merged, not open**, and an earlier revision of this table said otherwise.
2. **The summary table's last rows are 47 and 57.** Check 57's row is on `main` (added by `21abc49`, "design: a Docker-capable execution backend on Compute Engine VMs (generic-tool Task 28) (#375)", so **#375 is merged too**), but its *section* is not in the checklist: the row points at `docs/design/compute-backend.md` §7.2, which is where 57a and 57b are written. A number whose section lives in another document is still taken.
3. **`docs/plans/2026-10-14-project-centric.md` reserves 48 to 56** and says so in a table with a `State` column, writing each row with its own phase rather than up front — so those nine are spoken for even though `main` carries none of them yet.
4. **Two open PRs reserve the two numbers above 57.** **#393 takes 58 and merges first; #393 does not use 59**, which leaves **59** free for this plan. Neither number is on `main` yet, so greps for `Check 58`, `Check 59` and every higher number across `docs/` return nothing outside the reserving PR — which is exactly why a grep alone can never answer this question, and why the two reservations are recorded in this table.

So 1-57 are all accounted for — 1-47 written, 48-56 reserved in a plan, 57 written as a row with its section elsewhere — and 58 is #393's and **59 is this plan's**. **The rule, so neither PR has to guess at review time: whichever of the two merges second renumbers**, checking `main`'s sections and summary rows before it writes anything, because the loser's number is the one that was never free. If #393 is renumbered, abandoned or closed instead, this plan keeps 59 regardless: a number already written into a summary table and a task heading is expensive to move and free to leave alone.

**This plan takes one number, not a range**, because it has exactly one owner-run live step. **Following the project-centric plan's convention, neither this PR nor any docs-only task here writes the row or the section**: Task 4.3 writes both when phase 4 ships, so exactly one writer owns the number and a rebase cannot produce two.

**The re-check this section invites is a `git log` away, and the plan says which commands, so a reader does not have to trust the prose:** `git show origin/main:docs/gcp-live-checklist.md | grep -n '^## Check 4[5-9]'` for step 1, `grep -n '^| 4[4-9] \|^| 5[0-9] ' docs/gcp-live-checklist.md` for step 2, a grep of `docs/plans/` and `docs/design/` for step 3, and the two open PRs' own headings for step 4. **An earlier revision of this table got steps 1 and 2 wrong** — it repeated the project-centric plan's table (written while #359 was still open) instead of reading `main`. The correction to 58 is recorded rather than quietly applied, and the same review that corrected it is what found that 58 was already taken by #393; the error was in the *reasoning*, which is the one thing this section exists to get right.

**Check 59 is owner-run, on the sandbox installation and the sandbox repository only, and never touches the `belong` project** (`belong` holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`: no agent touches it, ever). It is written with FACT lines, a restore step, and **Check 43's full guard, verbatim** — the three hard stops, `--project fugaro` on every `fugaro` command and `--project "${P:?}"` on every `gcloud` command, in a block re-run from a fresh shell before every step (`docs/gcp-live-checklist.md`, Check 43's guard block, and the rule the project-centric plan spells once and requires of every check 48 onward). That requirement is not optional for a check that looks local: the future `TestChecklistChecksAreGuarded` (project-centric plan Task 6.3) asserts that **every `## Check 48`-onward section carries Check 43's three hard stops and its project pinning, not merely a `:?` guard**, and Check 59 is a `## Check 59` section. The console is *built* from this repository; every `fugaro` command it drives is run **with the sandbox checkout as the working directory**, under the guard.

## Global Constraints

These hold for every task below, and the plan's own review checks them:

1. **No new backend, ever.** No service account, no RTDB node, no Firestore collection, no rules change, no Cloud Run service, no Terraform resource. The console reads what `fugaro watch` and the budget dashboard already read, through the CLI. If a screen needs a field no `--json` emits, the fix is a CLI task in a CLI plan, not a console reader.
2. **No credential is ever read, held or stored by the app.** Not an OAuth token, not a service-account key, not a Firebase API key, not a copy of the operator's config. The app spawns `fugaro` and lets it resolve the operator's ambient identity exactly as a terminal does.
3. **The app never writes.** The spawn allow-list is read-only commands only, and the mutation-proof test of Task 1.2 is what keeps it that way: adding a write subcommand must fail a test, not pass a review.
4. **The renderer is untrusted by construction and gets no network.** `connect-src 'none'`, no remote content, no navigation, no permission, no `nodeIntegration`, no generic IPC.
5. **Every number carries an availability state.** Measured, notional, estimated, unavailable or a gap — never an implicit zero. This is the design's first UI rule (its §6.1) and it is a review criterion, not a nicety.
6. **`belong` is never touched** by any step, agent-run or owner-run, and no fixture is ever recorded from it.
7. **Nothing under `.github/workflows/` is created or edited by an agent.** The console's workflow text is written as a **staged file under `docs/pending-workflows/`** for the owner to copy (Task 4.2), which is the mechanism this repo already uses (`docs/pending-workflows/README.md`) and names as an owner step in `docs/release.md`'s "Before you tag".
8. **The Go module is untouched.** `console/` holds no Go package, `go.mod` and `go.sum` do not change, and no Go file references a path under `console/`. Task 0.3's test is what makes this a fact rather than an intention.
9. **Security-sensitive tasks are flagged `SECURITY-SENSITIVE — independent opus review`** in their own heading and are not merged on a self-review. **This list is the headings, and nothing else is on it**: 1.1, 1.2, 1.3, 2.0, 4.1, 4.2 and 4.4, plus 5.2 when it is scheduled. (An earlier revision listed 2.1 and 4.3 and omitted 2.0, 4.4 and 4.5; the headings are the authority, and Task 2.1 is not flagged — it draws the states Task 2.0 defines rather than handling hostile text itself.) **A separate flag, `OWNER-RUN`, marks the tasks no agent performs at all** — 1.4's recordings, 4.3, and 4.5's copy-in — and a task may be neither, either, or (never) both.
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
  src/main/updates.ts             # Task 4.4: the update check and the signed feed
  src/preload/index.ts            # the frozen, versioned object the renderer sees
  src/renderer/index.html         # the bundled document, the CSP meta as belt-and-braces
  src/renderer/app.ts
  src/renderer/screens/{live,spend,projects,run}.ts
  src/renderer/theme/tokens.css
  src/source/source.ts            # the FugaroSource interface - the whole data plane
  src/source/cli.ts               # v1: spawn the installed fugaro
  src/source/fixture.ts           # tests: replay a recorded NDJSON feed
  src/shared/{schema,availability,sanitize,version}.ts
  test/unit/*.test.ts
  test/e2e/*.spec.ts              # Playwright + Electron
  test/e2e/__screenshots__/       # the golden frames
  test/fixtures/*.ndjson          # recorded and redacted; never from belong
  tools/record-fixture/{index,redact}.ts
```

Edited by this plan:

```
internal/cli/docs_desktop_console_test.go      # Task 0.1/2.4: the design's load-bearing strings
internal/cli/docs_console_boundary_test.go     # Task 0.3: the Go/console boundary
docs/pending-workflows/console.yml             # Tasks 1.4, 4.2: staged, never copied by an agent
scripts/release-gate.sh                        # Task 4.6 only: names="test terraform rules console"
.github/workflows/console.yml                  # Task 4.5 only, and the OWNER creates it: no agent touches it
docs/gcp-live-checklist.md                     # Task 4.3 only: Check 59's row and section
docs/design/desktop-console.md                 # Task 5.0 only, if the owner's decisions differ
```

## PR groups

Each row is one mergeable PR, green on its own:

| PR | Tasks | Flag |
|---|---|---|
| A | 0.1, 0.2, 0.3 | docs + Go tests only |
| B | 1.0, 1.1, 1.2 | **SECURITY-SENSITIVE** |
| C | 1.3, 1.4 | **SECURITY-SENSITIVE** (spawn and the redactor); **OWNER-RUN** for 1.4's recordings |
| D | 2.0 | **SECURITY-SENSITIVE** (the text path) |
| E | 2.1, 2.2, 2.3 | |
| F | 3.1, 3.2 | |
| G | 4.1, 4.2, 4.4 | **SECURITY-SENSITIVE** (supply chain) |
| H | 4.3, 4.5 | **OWNER-RUN**, no agent performs any step of either |
| I | 4.6 | the release gate; **merges only after H**, and refuses a tag until the console job runs |

---

## Phase 0 — The ground

### Task 0.1: The JSON contract, pinned

**Files:** `console/docs/json-contract.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Read, on `origin/main`, every document the console will consume, and write down each one's real shape: `internal/watch/json.go` (`JSONDoc`, `JSONConn`, `JSONTotal`, `JSONBurn`, `JSONKill`, `JSONRepo`, `JSONRun`, `JSONFinished`, `BuildJSON`, `jsonBurn`, `jsonKill`, `jsonFinished`, `usdPtr`) for `watch --json`; `budgetShowDoc` for `budget show --json`; `internal/report/render.go`'s `JSONDoc`, `JSONLine` and `JSONTokens` for `report --json` (**there is no `report.JSON`**, and an earlier revision of this step named one); `lsDoc` for `runs ls --json` (and, separately, `watchDegradedDoc`, which embeds `lsDoc`); `cancelResult`; `logLine`; and whatever `diagnose --json` and `version` emit. For each: the field, its type, its unit (dollars as floats; micros as integers; day keys as UTC epoch days), whether it is `omitempty`, and **which of the design's five availability states it needs**. Record the flags each command actually has, from the cobra command rather than the docs. Note the three facts the console's reader and its rendering depend on and that nothing else records: (i) the document is emitted one per change, compact, on a 1-second gap; (ii) **`--repo` narrows the JSON too** — it reaches `watch.FilterRepo(v View, key string)` in `internal/cli/watch.go`, so an earlier revision's "`--json` is always unfiltered" was wrong, and only the finished/ready axis is unfiltered; and (iii) **the synthetic and degraded modes emit different things**: `watchDegradedDoc` is `lsDoc` plus `degraded` and `notice` (a second document shape the console must handle), and `runWatchDegraded`'s error path writes a **plain, non-JSON sentence to stdout** — `"%s\nrun records unavailable (retrying): %s\n"` — on the same stream as the documents, so the reader must skip any line that is not a JSON object (design §2.1). The contract document is not finished until all three are written down with the symbol that proves each.

**And it settles the design's §4.3 CLI gaps against the code** — five of them, one at a time, because the console ships without any of them: the degraded path's stray stdout line; `version` having no `--json` (and the root command setting no cobra `Version` field, so `fugaro --version` fails); `project show --json` not existing on this tree (#390); `watch --json` carrying no schema version; and **nothing enumerating installations** — `fugaro config` is `example|show|layer|init|publish` (`internal/cli/configcmd.go`) and there is no `fugaro project` command at all. Each is recorded with the symbol that proves the gap, so D19 is a list of facts rather than an impression; the last one is also design §12 item 8, and it is what D13's add-an-installation form stands in for.

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

**Steps.** The design's §5.1 rule 5, built so it cannot be widened by accident: the preload exposes a **frozen, versioned object of named operations** — no `invoke(channel, …)`, no channel name from the renderer, no dynamic `require`. Each channel has a closed argument schema validated in the main process, takes no path, no command and no URL beyond the validated external-link one, and returns data that is already rendered. Then the mutation-proof half: a table of operations the console must **never** reach — every write subcommand, the `secrets` commands, the `init`, `upgrade` and `exec` verbs, and the hidden `budget history` job command — asserted unreachable by iterating the allow-list itself, so that adding one of them fails a test named for it. Freeze the exposed object and assert `Object.isFrozen`.

**Tests:** `ipc.test.ts` — the allow-list's read-only invariant, the never-reachable table, the argument schemas against malformed input, `Object.isFrozen`, and the absence of a generic invoke.

**Command:** `npm run test:unit`

**Depends on:** Task 1.1.

**Commit:** `console: an IPC allow-list, not a bridge, and the table of things it cannot reach`

**Red step — `Expected: FAIL`** — the never-reachable test against `budget kill` added to the list.

### Task 1.3: `FugaroSource`, and the one child process — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/source/source.ts`, `console/src/source/cli.ts`, `console/src/main/spawn.ts`, `console/test/unit/spawn.test.ts`

**Steps.** The interface every screen talks to, so that design D1's Option C can replace Option A without touching a screen: `FugaroSource` yields `JSONDoc`-shaped snapshots and emits change events and knows nothing about how they were produced. Then the v1 implementation, which is where four of the design's §3.4 rules stop being prose:

- **The binary is resolved explicitly, never through the app's `PATH` alone** (rule 4): the operator's configured path first, then `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and only then `PATH`; the resolved absolute path and the version it reports are shown in the header, and a binary whose version cannot be read is refused rather than run.
- **The project is passed explicitly on every spawn** (rule 3): `--project <name>` from the installation the operator chose (D13), plus `--config <path>` when that installation is not the one the ambient environment would select. This is not optional and it is not a credential — a Finder-launched app has cwd `/`, launchd's environment and the system `PATH`, so `selectProject`/`selectWith` in `internal/cli/project.go` would otherwise pick the wrong installation or none.
- **The child's environment is built, not inherited** (rule 5, §5.1 rule 8): a fixed, minimal set — the resolved prefix on `PATH`, `HOME`, `TMPDIR`, `LANG`/`LC_ALL`, the `XDG_*` variables, and the chosen installation's `FUGARO_PROJECT` — and **nothing else from the app's own environment**. An earlier revision of this task said the child inherits the operator's environment unchanged; that is wrong on two counts, because it hands the child anything the app's own environment carries and it leaves `FUGARO_PROJECT` to chance.
- **argv is an array, `shell: false`, and never built from a job-written field** (rule 1).

Implement what Task 0.2 settled: restart behaviour, a closed-stdout exit, the UTC-midnight day rollover, and the reader rule that skips a stdout line which is not a JSON object (Task 0.1's fact (iii)).

**Tests:** `spawn.test.ts` against a stub executable — argv is an array and never a shell string; a subcommand outside the table is refused before spawn; **`--project` is present on every spawn and comes from the chosen installation, not from the ambient environment**; **the environment is exactly the allow-list** — asserted by diffing the keys the stub reports against the declared set, so an added variable fails a named test, and a planted `GOOGLE_APPLICATION_CREDENTIALS`/`FUGARO_PROJECT` in the app's own environment is asserted **not** to reach the child; the binary resolver finds a stub at a configured path, then at each well-known prefix in order, and refuses a candidate that reports no version; a hostile string from the feed is never interpolated into argv; a non-JSON stdout line is skipped rather than parsed; a child that dies is restarted with backoff and the UI is told.

**Command:** `npm run test:unit`

**Depends on:** Task 1.2, Task 0.2.

**Commit:** `console: FugaroSource, and the one child process it is allowed to spawn`

**Red step — `Expected: FAIL`** — the environment-diff test against a spawn that injects `GOOGLE_APPLICATION_CREDENTIALS` (and against one that inherits `process.env` wholesale), the `--project` test against a spawn that omits it, and the argv test against a template-string command.

### Task 1.4: Recorded, redacted fixtures, and the recorder — the tool is agent-written, **the recordings are OWNER-RUN**

**Files:** `console/src/source/fixture.ts`, `console/tools/record-fixture/{index,redact}.ts`, `console/test/fixtures/*.ndjson`, `console/test/unit/redact.test.ts`, `console/test/unit/fixture.test.ts`, `docs/pending-workflows/console.yml`

**Steps.** The recorder spawns `fugaro watch --json` (from the sandbox checkout, **never `belong`**), captures the NDJSON, and redacts: `watch` already clips and redacts a run title, and the recorder applies the same treatment to repository names, PR URLs, requester addresses and anything else installation-specific. Import the fixture renderer that makes the recording usable: a `FugaroSource` implementation that replays a file, including its timestamps, so a gap in the recording is a gap in the test. **Stage the CI workflow here**, because the e2e tests of phase 2 need it: `docs/pending-workflows/console.yml`, with the two runners, the node setup and **a job named exactly `console` that runs unconditionally** on every pull request and every push to `main` — **no `paths:`, no `paths-ignore:`, no `if:`** — for the owner to copy (Task 4.5). The design's §9.4 says why a path filter here would break the release gate, and the test below is what keeps it from creeping back.

**The seven recordings are OWNER-RUN, and this is the correction that matters here.** The design's §9.1 asks for the normal run, the **`silent`-then-`lost`** run, the **kill flipping mid-feed**, the **repository at its cap**, the OAuth notional run, the absent field, the gap, and a hostile title. Three of those cannot be produced without a live sandbox: a run that stops heartbeating and then goes `lost` needs a real stalled run, a kill switch flipping needs a real `budget kill` against the sandbox, and a repository at its cap needs real spend to accumulate. **An agent performs none of it** — no live action, and never against `belong` (Global Constraints 2 and 6). So the task splits cleanly: the **recorder, the redactor and the replay source are agent-written and agent-tested against a synthetic frame** committed with them, and the **recordings themselves are a step the owner runs** with a documented command from `console/README.md`. Until the owner records them, the fixtures directory carries one synthetic, obviously-fake frame so the harness is exercisable and no screen has ever claimed to be tested against real output. The recordings are re-made whenever `watch --json` changes (C3).

**Tests:** `redact.test.ts` — a redactor that is *tested*, not trusted: it must remove a known address, a known repository name and a known PR URL from a recorded frame, must be idempotent, and must **fail closed** on a frame it cannot parse. `fixture.test.ts` — the replay source reproduces the recording's timestamps and **a gap in the recording is a gap in the replayed stream** (the assertion that makes the `stale` tests of Task 3.1 mean anything). Plus a test that every committed fixture passes the redactor unchanged, so a hand-edited fixture is caught — it is what will catch the first owner-recorded file the redactor should have altered.

**Command:** `npm run test:unit`

**Depends on:** Task 1.3.

**Commit:** `console: recorded, redacted fixtures and the recorder that makes them`

**Red step — `Expected: FAIL`** — the redaction test against a recorder that passes a title through untouched, and the fixtures-are-clean test against a fixture with a planted address.

**Owner-run step, named so it is not mistaken for agent work:** `console/README.md` documents one command — `npm run record-fixture -- --out test/fixtures/<name>.ndjson` — run **by the owner**, from the sandbox checkout, with the sandbox installation selected, under this repo's usual guard. The seven recordings are attached to the task's PR by the owner, or in a follow-up commit, and nothing in this plan's agent-run steps produces them.

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

**Files:** `console/src/renderer/screens/live.ts`, `console/test/unit/live.test.ts`, `console/test/e2e/live.spec.ts`, `console/test/e2e/__screenshots__/live-*.png`

**Steps.** The design's §6.2 screen 1, drawn from `watch --json` alone: the project line with mode, day and the connection state; the global cap bar with `counted` and the cap; one row per repository with its bar, its 5-minute rolling burn sparkline and its kill banner (with by, at and reason); and under each repository its runs — stage, round, verify, models, recipe, auth, spend, age — flagged `silent` (>60 s), `lost` (>3 min) and amber past 80% of the stage deadline, with `queued`/`stuck` rows from the bucket side and an OAuth run's spend marked notional. The density rules are the design's §6.1 rules 2 and 3: ~24 px rows, tabular numerals, right-aligned figures, no decorative colour. The burn window must match `watch`'s own (`BurnWindow`, 5 minutes) **and a test asserts the two constants cannot drift**.

**Tests:** `live.test.ts` — the row model from a fixture frame, each flag's boundary at exactly its threshold, the burn window against the constant. `live.spec.ts` — the screen renders the fixture, a killed repository shows its banner, a queued row is present; and a screenshot golden per theme.

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

**Steps.** The design's §6.2 screens 3 and 4. **Projects** renders what exists today — caps, kill state, the enrolled and the seen repositories, project jobs — and says plainly what is not built (`config/policy` is project-centric phase 1; the enrollment allow-list is phase 1 and 4), rather than drawing an empty box that looks like zero. The secrets and accounts inventory is **names, labels and version counts only**, never a value. **Run detail** shows the ledger, the stage timeline with deadlines, per-stage spend, the outcome, the PR link, the `logs` link, the `diagnose` view, and **the replay buffer** — the design's §6.1 rule 4, taken from tmog.org's Flight Recorder: a bounded in-memory ring of the last N minutes of the feed, scrubbable, exportable to a file, which is also the debugging aid for "the run was slow at 14:20".

**Tests:** `projects.test.ts` — an unbuilt section renders as not-built and never as a zero; the inventory contains no value-shaped string. `replay.test.ts` — the ring's bound is enforced, an export round-trips through the fixture source, and scrubbing to a moment reproduces that moment's view. `projects.spec.ts`/`run.spec.ts` — goldens, and the log link is validated before it can be opened.

**Command:** `npm run test:unit && npm run test:e2e`

**Depends on:** Task 2.2.

**Commit:** `console: projects, run detail, and the replay buffer`

**Red step — `Expected: FAIL`** — the unbuilt-section test before the not-built state exists, and the ring-bound test before the bound is enforced.

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

**Files:** `docs/pending-workflows/console.yml`, `docs/pending-workflows/README.md`, `console/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Write the workflow's text as a **staged file** under `docs/pending-workflows/` — this repo's mechanism for a change only the owner may push (its README is the directory's only occupant today, and `docs/release.md`'s "Before you tag" names copying a staged workflow in as an owner step). It builds on the CLI's tag, runs the console's tests, signs with the hardened runtime, notarises with `xcrun notarytool submit --wait`, staples both the app and the dmg, verifies with `codesign --verify --deep --strict`, `spctl --assess --type execute` and `stapler validate`, and **records those verification outputs** the way the CLI's release records its checksums. Actions are pinned to full commit SHAs with `# vX.Y.Z` comments, as everywhere else in this repo, and the secrets it needs are named but never valued. Extend the directory's README with the console's row: what it is, why it is staged, and the one command that applies it.

**Tests:** the workflow's *behaviour* cannot run here — it has never run, and the directory's README already carries a "Not verified" section that this adds a console row to. But its **text** is testable, and an earlier revision of this task left that as "the review substitutes", which is not a test:

- `TestConsoleWorkflowIsStagedAndPinned` (in `internal/cli/docs_desktop_console_test.go`, the file Task 0.3 allows to read a `console/` path, plus the `docs/pending-workflows/` path every docs test may read) asserts, over `docs/pending-workflows/console.yml`: **every `uses:` is pinned to a full 40-hex commit with a `# vX.Y.Z` comment**; **no `pull_request_target`, no `workflow_run` and no `permissions: write-all`**; every job declares `permissions:` explicitly and only what it needs (`contents: read` for the test job); **`npm ci --ignore-scripts --no-audit --no-fund` is the install step** and there is no `npm install`, no `npm test` and no unpinned `npx`; **the file does not exist under `.github/workflows/`** — the assertion that an agent has not copied it in; and **no literal that could be a secret** appears (no `-----BEGIN`, no long base64 blob, no `password:`/`token:` with a value), only `secrets.NAME` references.
- `TestConsoleWorkflowJobIsGateable` — **the assertion this revision added, because without it Task 4.6 silently breaks every tag on this repository.** Over the same staged file it asserts: **a job whose key is exactly `console`** (the string in `release-gate.sh`'s `names`, so a rename must be a deliberate edit to this test *and* to Task 4.6); **the workflow triggers on `push` to `main` and on `pull_request`**; **the `console` job has no `if:` guard**, in particular none on a `changes`/`docs_only` output; and **the workflow has no `paths:` or `paths-ignore:` at the trigger level and the `console` job has none at the job level**. Each is a named sub-assertion. The reason is not style: `release-gate.sh` requires a check named `console` on the tag commit and, in its `identical_tree_checks` fallback, on the pull request head too — **a check that does not exist is not a success**, so a `console` job filtered to `console/**` would make `names="test terraform rules console"` refuse every release, including the ones that never touch the console. Ordering Task 4.5 before 4.6 does not help; the filter is the bug.
- `TestPendingWorkflowsReadmeNamesTheConsoleRow` asserts the README carries the console's row: what the file is, that it is staged because no agent may push a workflow here, and the one command that applies it.
- **The review criterion on top of the tests**: a reviewer reads the pinned SHAs against upstream, and confirms the workflow would fail closed on a missing secret rather than silently skipping the signing step.

**And an agent still may not copy it into `.github/workflows/`** — Task 4.5 is the owner's step, and the `TestConsoleWorkflowIsStagedAndPinned` assertion above is what makes an accidental copy a red test rather than a review miss.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.1.

**Commit:** `console: the signing and notarisation pipeline, staged for the owner`

**Red step — `Expected: FAIL`** — `TestConsoleWorkflowIsStagedAndPinned` before the staged file exists and against a planted `uses: actions/checkout@v5`; `TestPendingWorkflowsReadmeNamesTheConsoleRow` before the README row is added; and `TestConsoleWorkflowJobIsGateable` against a `console` job renamed to `console-tests` and against one carrying `paths: ['console/**']`. **The last two mutations are the ones to run first**, because they are the failure that is invisible until a tag is refused.

### Task 4.3: Check 59, the first signed build — OWNER-RUN, no agent performs any step

**Files:** `docs/gcp-live-checklist.md`

**Steps.** Write **Check 59** into the checklist, with its summary-table row, its FACT lines, its restore step and **Check 43's guard block verbatim** — the three hard stops, `--project fugaro` on every `fugaro` command, `--project "${P:?}"` on every `gcloud` command, re-run from a fresh shell before each step, and the sandbox checkout as the working directory for every `fugaro` command the console drives. The check is the owner's, and it asserts: (1) the certificate and the App Store Connect API key exist and are stored where only the owner can reach them; (2) a signed, notarised, stapled build is produced and **passes `spctl --assess` on a machine that has never seen it**; (3) the update round trip: an installed older build sees the new release and installs a bundle whose signature the OS verifies; (4) the safety assertion — with the console pointed at the **sandbox** installation, nothing it can reach belongs to `belong`, and the console's child process is the installed `fugaro` and nothing else. The section says in its first line that no agent performs any step of it.

**The restore step restores only what the check created, and this is a hard rule of the check, not a courtesy.** A restore step that "removes the keychain item it was signed with" would delete **the owner's own signing certificate** — the identity the whole pipeline depends on, which predates the check by design and is not the check's to destroy. So: the check's first step **records what exists before it starts** — `security find-identity -v -p codesigning` and the list of the keychain items it can see — as a FACT line; the restore step then **removes only what is not in that record**: the installed build from `/Applications`, the downloaded dmg and zip, the notarisation submission artifacts, any temporary keychain or key material the check itself created, and any file it wrote under a temporary directory. **A signing identity that was present before the check is never deleted, never re-imported and never locked**, and the check says so in its own text so a later reader cannot mistake a follow-the-recipe restore for a licence to clear the keychain. (The one permitted deletion is of an identity the *check itself* created for the purpose, which is why the pre-check record is a FACT line rather than an assumption.)

**Tests:** the checklist's own docs tests, plus the guard test the project-centric plan schedules (Task 6.3's `TestChecklistChecksAreGuarded`), which Check 59 must satisfy, plus a docs test asserting the section's restore step **does not name `security delete-identity`, `security delete-certificate` or `security delete-keychain` against anything present before the check** — the mutation-proven form: a restore step that clears the owner's identity fails a named test rather than reading as thoroughness in review.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.2.

**Commit:** `checklist: Check 59, the console's first signed build`

**Red step — `Expected: FAIL`** — the guard test against a Check 59 without the three hard stops, and the restore-step test against a restore that clears a pre-existing signing identity.

### Task 4.4: Auto-update — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/main/updates.ts`, `console/test/unit/updates.test.ts`, `docs/pending-workflows/console.yml`

**Steps.** The design's §7.3: the update check reads the same release metadata the CLI's `internal/updatecheck` reads and honours the same opt-out (`FUGARO_NO_UPDATE_CHECK=1`, a local `update_check: false`, never in CI), and the installation path is Squirrel.Mac over a **signed** feed — an update whose signature does not verify is refused, never installed. No telemetry of any kind: the only outbound requests are the CLI's own and this one update check (design §7.4). Default on, with the toggle visible (D5).

**Tests:** `updates.test.ts` — the opt-out is honoured (all three ways); an unsigned or mis-signed bundle is refused; no identifier is sent (asserted on the request the fake feed receives); a downgrade is refused; the check never runs in CI.

**Command:** `npm run test:unit`

**Depends on:** Task 4.2.

**Commit:** `console: the update check, with no telemetry and a verified feed`

**Red step — `Expected: FAIL`** — the mis-signed-refusal test against an updater that installs anyway, and the no-identifier test against a request carrying a machine id.

### Task 4.5: The staged workflow, copied in — OWNER-RUN, no agent copies it

**Files:** `.github/workflows/console.yml` (**created and pushed by the owner — no agent creates, edits or copies this file**), `docs/pending-workflows/README.md` (the agent-run follow-up that records it as applied)

**Steps.** Task 4.2 stages the console's workflow; **this task is the step that makes CI run the console's tests at all**, and it is the owner's because this repository refuses an agent's workflow change outright (GitHub will not accept it, so a run that edits one cannot be delivered — Global Constraint 7). The owner copies `docs/pending-workflows/console.yml` to `.github/workflows/console.yml`, commits and pushes it; from that commit the console's unit and e2e tests run on **every pull request and every push to `main`** — not only on changes under `console/**`, because a check that exists only sometimes cannot be required by the release gate (Task 4.6, design §9.4) — and that is the first CI that has ever run them: **nothing before this point executes a single console test, and the plan says so rather than implying a green pipeline.** An agent then makes one docs-only follow-up: the README's console row gains the date and the commit that applied it, and the "Not verified" note becomes "applied, and the first run is on `main`".

**Tests.** The real test is the workflow's own first run on `main`, which the owner reads. The agent-run follow-up is covered by a docs test asserting the README row names the applying commit, so the record cannot be left as "to do".

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.3 — the check is what proves the signing pipeline works, and a workflow that gates merges before the pipeline has ever been proved would gate on an unverified thing.

**Commit:** the owner's own, for the copy-in; the agent's follow-up is `docs: record the console workflow as applied`

**Red step — `Expected: FAIL`** — the README-row test before the applying commit is recorded.

### Task 4.6: The release gate requires the console check

**Files:** `scripts/release-gate.sh`, `internal/cli/docs_desktop_console_test.go`

**Steps.** `scripts/release-gate.sh` requires exactly the checks in `names="test terraform rules"`, so today a console job that fails would not stop a tag. Add `console` to that list — **and only after Task 4.5 has put a workflow in place that produces a check of exactly that name, running unconditionally.** Ordered this way deliberately (D18): a gate that requires a check no workflow runs refuses every tag on this repository, including the ones that ship none of this, which is a worse failure than the one being fixed. **The ordering is necessary and not sufficient**: the check must also exist on *every* `main` commit and on every pull request head, because `identical_tree_checks` demands the same names there — which is Task 4.2's `TestConsoleWorkflowJobIsGateable` and the reason the console job carries no `paths:` filter (design §9.4). The comment at the top of the script and the `identical_tree_checks` doc comment both name the required set, so they are edited with it. **This is the only change in the whole plan to a non-test file outside `docs/` and `console/`** — an earlier revision called it "the one change to a file outside `docs/` and `console/`", which was false: Tasks 0.1, 0.2, 0.3 and 4.2 create and edit `internal/cli/docs_desktop_console_test.go` and `internal/cli/docs_console_boundary_test.go`.

**Tests:** a Go docs-style test, `TestReleaseGateRequiresTheConsoleCheck`, that reads `scripts/release-gate.sh` and asserts `names` contains `console` — the mutation-proven form, so a later edit that drops it fails a named test. It also asserts the script's own comment lists the same names, so prose and variable cannot drift; and **that `console` matches the job key Task 4.2's `TestConsoleWorkflowJobIsGateable` pins in the staged workflow**, so the two files cannot agree on a name that no job produces.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.5 (**hard**: this must not merge before the check exists).

**Commit:** `release: the gate requires the console check`

**Red step — `Expected: FAIL`** — `TestReleaseGateRequiresTheConsoleCheck` against `names="test terraform rules"`.

---

## Phase 5 — Later, unscheduled

### Task 5.0: The design follows the owner's decisions

**Files:** `docs/design/desktop-console.md`

**Steps.** If the owner rules against any recommendation in the design's §10 — the eleven the first revision carried (**D1** the sidecar, **D2** read-only v1, **D3** this repository, **D4** direct-plus-cask and Apple signing, **D5** updates on, **D6** recorded fixtures, **D7** warn-and-degrade, **D8** information-design-only fidelity, **D9** no framework, **D10** macOS first, **D11** its own artifact from the CLI's tag) — **or against any of the eight this revision added** (**D12** the owner-run check's content, **D13** choosing and switching between installations, **D14** how an admin's credentials are treated, **D15** who may see cost and per-person data, **D16** the interaction with project-centric phase 3 and the VM/Batch backend, **D17** Apple-signing the CLI too, **D18** a console check in the release gate, **D19** which CLI changes v1 truly needs) — this task rewrites the design to the ruled answer **before any code task starts** and records the ruling in the design's decisions table, as `docs/design/project-centric.md` §12 does. A docs-only task, and the only edit this plan makes to the design. **D13 is the one that can change the plan rather than the design**: if the owner rules that the console may *not* pass `--project`, Task 1.3's spawn contract and §3.4 rule 3 have to be rewritten with it, and Task 0.2 (which reads `selectProject`/`selectWith` in `internal/cli/project.go`) is where the consequence is worked out.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** the owner's ruling.

**Commit:** `console: the design, at the owner's decisions`

**Red step — `Expected: FAIL`** — the docs-presence test before the ruled strings are in.

### Task 5.1: Windows and Linux packaging (unscheduled)

**Steps.** Two more signed deliverables (**D10**), each with its own identity and its own owner-run check (`windows-latest` signing via a code-signing certificate; Linux via the distro channels this repo would have to choose). **Not scheduled**: the design recommends later, the code stays cross-platform, and Task 1.4's staged workflow already runs the Linux e2e pass under `xvfb-run` so the platform does not rot.

### Task 5.2: The v1.1 write actions — SECURITY-SENSITIVE, independent opus review (unscheduled)

**Steps.** Kill, resume and cancel, if the owner says so (**D2**). Each is implemented **by invoking the CLI's own command**, never by writing to RTDB from the app: the typed confirmation, the admin check and the ETag compare-and-set stay in `internal/watch/keys.go` and `internal/cli/budget.go`, where they already are, and where a `budget kill` prints "you are not a budget admin" when the operator is not one. Rejected outright: any design in which the console writes.

## Cross-task dependencies

Every task's "Depends on" is a *build* dependency, and none of them is a merge dependency beyond its own phase: phases 1, 2 and 3 each leave a working artifact even if the next phase never ships, and phase 4's two owner-run steps (Tasks 4.3 and 4.5) sit after the agent-written packaging work, so 4.1/4.2/4.4 merge as ordinary reviewed PRs whether or not the owner ever runs the rest. The two places a reader might expect a dependency that does not exist: **phase 2 does not depend on project-centric phases 1 or 4** — it renders what exists and says so when a section is not built (Task 2.3) — and **phase 4 does not depend on the CLI's release process being changed**; it adds an artifact to the existing tag, which is what D11 decided.

**Three dependencies that are real and are *merge* dependencies, not build ones**, listed here because a reader checking the "nothing in phase *n* is required by phase *n-1*'s merge" claim would otherwise read them as exceptions it missed:

- **Task 4.5 (the owner's copy-in) must merge before Task 4.6 (the gate edit).** Merged the other way round, `scripts/release-gate.sh` requires a check that no workflow produces and **every tag on this repository is refused** — including the releases that ship none of the console. This is the one ordering in the plan whose inversion breaks other people's releases, and it is why 4.6 is its own PR group (I) rather than sharing G's. **The ordering alone is not enough, and the plan says so where it matters**: the gate names a check, so the check must exist on *every* `main` commit and on every pull request head for the `identical_tree_checks` fallback — which is why the console job carries no `paths:` filter and why Task 4.2 asserts its name and triggers (`TestConsoleWorkflowJobIsGateable`). A path-filtered, correctly-ordered console job would still refuse every tag.
- **Task 4.2 (the staged workflow) must merge before Task 4.5**, trivially — the owner has nothing to copy otherwise.
- **The recordings of Task 1.4 do not gate any merge at all.** They are owner-run data, they land when the owner runs the recorder, and the fixture harness is green without them (the synthetic frame, C7). A stale or absent recording is a test failure, not a broken build.

**And the fact a reader should not have to infer: until Task 4.5, no CI runs a single console test.** The console's tests exist from phase 1 onward and are run by the task commands above and by whoever is working on them; the workflow is staged (Task 4.2), not applied, and the gate does not require it (Task 4.6). That is a deliberate, stated gap rather than a claim that phase 1 through 4 are CI-covered.

## Decisions this plan defers to the owner

Its own, continuing the design's D-series with **C1 onward**, each vetoable before its phase starts:

| # | Question | Recommendation |
|---|---|---|
| **C1** | Is phase 0's boundary test (Task 0.3) enough to keep the Go module untouched, or does the owner want `console/` excluded from the module by a build constraint too? | **The test is enough.** `console/` holds no Go file, so `go build ./...`, `go vet ./...` and `gofmt -l .` never see it; a build constraint would add a mechanism where a test already gives the guarantee. The boundary it enforces is **imports and `go:embed`, not bytes**: the one-entry allow-list (Task 0.3) is what lets the docs tests read `console/docs/json-contract.md` as a string, and any second entry is a deliberate edit to a test. |
| **C2** | Does the console need its own `docs/release.md` section, or does it belong in the CLI's? | **Its own section in the existing file**, so the two artifacts' gates and Highlights are read in one place — `docs/release.md` is the single source of truth for the tag. |
| **C3** | Who owns the recorded fixtures' refresh, and when? | **The owner, at each release that changes `watch --json`**, with the redactor failing closed (Task 1.4) so a stale fixture surfaces as a test failure rather than a plausible screen. |
| **C4** | Does the console need a Homebrew cask of its own, and does it keep the CLI cask's `xattr -dr com.apple.quarantine` postflight? | **A cask of its own, and no quarantine hook**: the hook exists because the CLI's binaries are not notarised, and a notarised, stapled console does not need it. Removing the hook is the visible proof the notarisation works. |
| **C5** | Does the console ship a `--json` mode of its own (a headless `console --json`) as the CLI does? | **No.** The CLI already has the machine view; a second one would be a second contract to version. The console is a renderer. |
| **C6** | Does the release gate gain the console check (Task 4.6) in the same release that first ships the console, or the one after? | **The same release.** Waiting a release means the first console build ships ungated, which is the hole Task 4.6 exists to close; by then the check has run on every `console/**` pull request since Task 4.5, so the gate is requiring a check with a track record. |
| **C7** | Task 1.4's seven recordings are owner-run. Does phase 1's PR merge with the synthetic fixture and the recordings follow, or does it wait for them? | **Merge with the synthetic fixture** — the recorder, the redactor and the replay source are the reviewable artifacts, and the recordings are data that lands in their own commit. Blocking the shell on a live sandbox session would put owner work on the critical path of an agent-runnable PR, which is the thing this plan avoids everywhere else. |

## OWNER APPROVAL GATE

**No code before the owner approves the design and this plan.** Concretely: `docs/design/desktop-console.md` and this file are the deliverable of the PR that adds them; Tasks 0.1 onward are future work; and **no task in this plan starts until the owner has ruled on the design's D1 to D19** (its §10) and, if any recommendation is rejected, Task 5.0 has rewritten the design to the ruled answer. The three that gate the most: **D1** (the sidecar, which decides everything about the data plane), **D3** (this repository, which decides the CI and the module), and **D4** (Apple signing, which would be the first private key this repo's release path has ever held, and which every step of phase 4 and all of Check 59, and the copy-in of Task 4.5, is owner-run because of).
