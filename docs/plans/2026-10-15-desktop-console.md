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
| **1. The shell** | the hardened Electron main process, the frozen IPC allow-list, `FugaroSource` with its `cli` and `fixture` implementations, the recorder/redactor, and **the console's CI** | **the seven fixture recordings** (Task 1.4) and **the copy-in of the staged tests workflow** (Task 1.5) | phase 0 |
| **2. The screens** | Live, Spend, Projects, Run detail — with the availability states and the replay buffer | nothing | phase 1 |
| **3. When things are missing** | the no-Firebase and CLI-absent modes, the stale/gap rendering, the version-skew handshake | nothing | phase 2 |
| **4. macOS packaging** | the builder config and entitlements; the staged signing/notarisation workflow (a file of its own, tag-only); the update feed; **Check 59** | **all of it**: the certificate, the API key, the first signed build, the update round trip (Check 59); the protected GitHub environment the signing secrets live in; and Task 4.5's copy-in of the staged **signing** workflow | phase 3 |
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
7. **Nothing under `.github/workflows/` is created or edited by an agent.** The console's workflow text is written as **two staged files under `docs/pending-workflows/`** for the owner to copy — **`console-tests.yml`** (staged by Task 1.4, copied in by the owner as Task 1.5) and **`console-release.yml`** (the signing pipeline, staged by Task 4.2, copied in as Task 4.5) — which is the mechanism this repo already uses (`docs/pending-workflows/README.md`) and names as an owner step in `docs/release.md`'s "Before you tag". **The two are separate files because they must have different triggers**: the tests job runs on every pull request and every `main` push and holds no secret, the signing job runs only on a version tag and reads its secrets from a protected environment (design §9.4). One file carrying both would put the signing key's job on every pull-request run.
8. **The Go module is untouched.** `console/` holds no Go package, `go.mod` and `go.sum` do not change, and no Go file references a path under `console/`. Task 0.3's test is what makes this a fact rather than an intention.
9. **Security-sensitive tasks are flagged `SECURITY-SENSITIVE — independent opus review`** in their own heading and are not merged on a self-review. **This list is the headings, and nothing else is on it**: 1.1, 1.2, 1.3, 2.0, 4.1, 4.2 and 4.4, plus 5.2 when it is scheduled. (An earlier revision listed 2.1 and 4.3 and omitted 2.0, 4.4 and 4.5; the headings are the authority, and Task 2.1 is not flagged — it draws the states Task 2.0 defines rather than handling hostile text itself.) **A separate flag, `OWNER-RUN`, marks the tasks no agent performs at all** — 1.4's recordings, **Task 1.5's copy-in**, 4.3, and 4.5's copy-in — and a task may be neither, either, or (never) both.
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
docs/pending-workflows/console-tests.yml       # Task 1.4 stages it; the OWNER copies it in as Task 1.5
docs/pending-workflows/console-release.yml     # Task 4.2 stages it; the OWNER copies it in as Task 4.5
docs/pending-workflows/README.md               # Tasks 1.4, 1.5, 4.2, 4.5: a row per staged file, and its applying commit
scripts/release-gate.sh                        # Task 4.6 only: names="test terraform rules console"
.github/workflows/console-tests.yml            # Task 1.5 only, and the OWNER creates it: no agent touches it
.github/workflows/console-release.yml          # Task 4.5 only, and the OWNER creates it: no agent touches it
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
| C2 | **1.5** | **OWNER-RUN**: the owner copies the staged tests workflow in — no agent performs any step of it |
| D | 2.0 | **SECURITY-SENSITIVE** (the text path) |
| E | 2.1, 2.2, 2.3 | |
| F | 3.1, 3.2 | |
| G | 4.1, 4.2, 4.4 | **SECURITY-SENSITIVE** (supply chain) |
| H | 4.3, 4.5 | **OWNER-RUN**, no agent performs any step of either; 4.5 copies in the **signing** workflow |
| I | 4.6 | the release gate; **merges only after C2**, and refuses a tag until the console job runs |

---

## Phase 0 — The ground

### Task 0.1: The JSON contract, pinned

**Files:** `console/docs/json-contract.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Read, on `origin/main`, every document the console will consume, and write down each one's real shape: `internal/watch/json.go` (`JSONDoc`, `JSONConn`, `JSONTotal`, `JSONBurn`, `JSONKill`, `JSONRepo`, `JSONRun`, `JSONFinished`, `BuildJSON`, `jsonBurn`, `jsonKill`, `jsonFinished`, `usdPtr`) for `watch --json`; `budgetShowDoc` for `budget show --json`; `internal/report/render.go`'s `JSONDoc`, `JSONLine` and `JSONTokens` for `report --json` (**there is no `report.JSON`**, and an earlier revision of this step named one); `lsDoc` for `runs ls --json` (and, separately, `watchDegradedDoc`, which embeds `lsDoc`); `cancelResult`; `logLine`; and whatever `diagnose --json` and `version` emit. For each: the field, its type, its unit (dollars as floats; micros as integers; day keys as UTC epoch days), whether it is `omitempty`, and **which of the design's five availability states it needs**. Record the flags each command actually has, from the cobra command rather than the docs. Note the three facts the console's reader and its rendering depend on and that nothing else records: (i) the document is emitted one per change, compact, on a 1-second gap; (ii) **`--repo` narrows the JSON too** — it reaches `watch.FilterRepo(v View, key string)` in `internal/cli/watch.go`, so an earlier revision's "`--json` is always unfiltered" was wrong, and only the finished/ready axis is unfiltered; and (iii) **the synthetic and degraded modes emit different things**: `watchDegradedDoc` is `lsDoc` plus `degraded` and `notice` (a second document shape the console must handle), and `runWatchDegraded`'s error path writes a **plain, non-JSON sentence to stdout** — `"%s\nrun records unavailable (retrying): %s\n"` — on the same stream as the documents, so the reader must skip any line that is not a JSON object (design §2.1). The contract document is not finished until all three are written down with the symbol that proves each.

**And it settles the design's §4.3 CLI gaps against the code** — five of them, one at a time, because the console ships without any of them: the degraded path's stray stdout line; `version` having no `--json` (**and no flags at all**: `newVersionCmd` is `cobra.NoArgs` with no `Flags()`, which is also why the root command sets no cobra `Version` field and `fugaro --version` fails); `project show --json` not existing on this tree (project-centric phase 1, #390, which ships `project show`, `project policy publish` and `project repos ls|add|rm` — and **no `project ls`**, which is the command an earlier revision of the design's gap 5 named); `watch --json` carrying no schema version; and **nothing enumerating installations** — `fugaro config` is `example|show|layer|init|publish` (`internal/cli/configcmd.go`), there is no `fugaro project` command at all, and that phase's commands describe the *selected* project's policy and repositories rather than the machine's installations. Each is recorded with the symbol that proves the gap, so D19 is a list of facts rather than an impression; the last one is also design §12 item 8, and it is what D13's add-an-installation form stands in for.

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
- **The project is passed explicitly — on every command that takes the flag** (rule 3): `--project <name>` from the installation the operator chose (D13), plus `--config <path>` when that installation is not the one the ambient environment would select. This is not optional and it is not a credential — a Finder-launched app has cwd `/`, launchd's environment and the system `PATH`, so `selectProject`/`selectWith` in `internal/cli/project.go` would otherwise pick the wrong installation or none. **The table is per-command, and `version` is the exception**: the nine that take it are `watch`, `budget show`, `report`, `runs ls`, `diagnose`, `logs`, `secrets ls`, `accounts ls` and `doctor` (`addCloudFlags`, `internal/cli/cloud.go:51`, called from each of those constructors), and `newVersionCmd` (`internal/cli/root.go`) is `cobra.NoArgs` with no `Flags()` at all, so **the handshake runs `fugaro version` bare** and a console that appended `--project` to it would fail on every machine with `unknown flag: --project`. An earlier revision of this step said "on every spawn", which is the defect this bullet now names; the checklist's Check 43 guard says the same thing about `version` and is where the console's table gets it from.
- **The child's working directory is set explicitly** (rule 5): the installation's checkout directory when it has one, and otherwise a private, empty directory the app owns — never the app's own default of `/`, which is nobody's checkout. `doctor` is called with `--dir <that directory>` and **never with `--strict`**, because outside a checkout doctor's `plugin-pin` check is a warning that `--strict` turns into a failure, and that failure is about the checkout rather than about the environment the console draws (`internal/cli/doctor.go`).
- **The child's environment is built, not inherited** (rule 5, §5.1 rule 8): a fixed, minimal set — the resolved prefix on `PATH`, `HOME`, `TMPDIR`, `LANG`/`LC_ALL`, the `XDG_*` variables, and the chosen installation's `FUGARO_PROJECT` — and **nothing else from the app's own environment**. An earlier revision of this task said the child inherits the operator's environment unchanged; that is wrong on two counts, because it hands the child anything the app's own environment carries and it leaves `FUGARO_PROJECT` to chance.
- **argv is an array, `shell: false`, and never built from a job-written field** (rule 1).

Implement what Task 0.2 settled: restart behaviour, a closed-stdout exit, the UTC-midnight day rollover, and the reader rule that skips a stdout line which is not a JSON object (Task 0.1's fact (iii)).

**Tests:** `spawn.test.ts` against a stub executable — argv is an array and never a shell string; a subcommand outside the table is refused before spawn; **`--project` is present on exactly the argv that accept it and comes from the chosen installation, not from the ambient environment**: the test iterates the spawn table and asserts the flag on every entry except the `version` handshake, and asserts the handshake's argv is exactly `["version"]` — the two halves matter equally, because a console that put `--project` on `version` fails on launch and one that left it off `watch` shows another installation's numbers; **the environment is exactly the allow-list** — asserted by diffing the keys the stub reports against the declared set, so an added variable fails a named test, and a planted `GOOGLE_APPLICATION_CREDENTIALS`/`FUGARO_PROJECT` in the app's own environment is asserted **not** to reach the child; **the working directory is the installation's, and is never `/`** — the stub reports its own `cwd`, and an installation with no directory is asserted to spawn from the app's private one, with `doctor`'s argv carrying `--dir` and never `--strict`; the binary resolver finds a stub at a configured path, then at each well-known prefix in order, and refuses a candidate that reports no version; a hostile string from the feed is never interpolated into argv; a non-JSON stdout line is skipped rather than parsed; a child that dies is restarted with backoff and the UI is told.

**Command:** `npm run test:unit`

**Depends on:** Task 1.2, Task 0.2.

**Commit:** `console: FugaroSource, and the one child process it is allowed to spawn`

**Red step — `Expected: FAIL`** — the environment-diff test against a spawn that injects `GOOGLE_APPLICATION_CREDENTIALS` (and against one that inherits `process.env` wholesale), the `--project` test in **both** directions (a spawn that omits it from `watch`, and one that appends it to `version`), the working-directory test against a spawn that leaves `cwd` unset, and the argv test against a template-string command.

### Task 1.4: Recorded, redacted fixtures, and the recorder — the tool is agent-written, **the recordings are OWNER-RUN**

**Files:** `console/src/source/fixture.ts`, `console/tools/record-fixture/{index,redact}.ts`, `console/test/fixtures/*.ndjson`, `console/test/unit/redact.test.ts`, `console/test/unit/fixture.test.ts`, `docs/pending-workflows/console-tests.yml`, `docs/pending-workflows/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** The recorder spawns `fugaro watch --json` (from the sandbox checkout, **never `belong`**), captures the NDJSON, and redacts: `watch` already clips and redacts a run title, and the recorder applies the same treatment to repository names, PR URLs, requester addresses and anything else installation-specific. Import the fixture renderer that makes the recording usable: a `FugaroSource` implementation that replays a file, including its timestamps, so a gap in the recording is a gap in the test. **Stage the tests workflow here, and this is a correction of where it used to sit.** An earlier revision staged one `console.yml` in phase 4 and copied it in as Task 4.5, which meant **phases 1 to 3 merged with no CI running a single console test at all** — the task that stages is now in the phase that first needs it, and the owner's copy-in is Task 1.5, immediately after. So: `docs/pending-workflows/console-tests.yml` — **tests only, no secret of any kind** — with the two runners (`macos-latest`, and `ubuntu-latest` under `xvfb-run`), `actions/setup-node` pinned to a full SHA and taking its version from `console/package.json`'s `engines.node` (which mirrors the emulator precedent's `"node": ">=20"`), `npm ci --ignore-scripts --no-audit --no-fund`, `npm run test:unit`, and the e2e script — which is written to pass with no specs until Task 2.0's Playwright harness exists, so the file can land in this phase and gain its first spec without a second copy-in — and **a job named exactly `console` that runs unconditionally** on every pull request and every push to `main` — **no `paths:`, no `paths-ignore:`, no `if:`**. The design's §9.4 says why a path filter here would break the release gate, and the tests below are what keep it from creeping back. **The signing, notarising and stapling workflow is not this file**: it is Task 4.2's `console-release.yml`, and it must not share a file with a job that runs on every pull request.

**The seven recordings are OWNER-RUN, and this is the correction that matters here.** The design's §9.1 asks for the normal run, the **`silent`-then-`lost`** run, the **kill flipping mid-feed**, the **repository at its cap**, the OAuth notional run, the absent field, the gap, and a hostile title. Three of those cannot be produced without a live sandbox: a run that stops heartbeating and then goes `lost` needs a real stalled run, a kill switch flipping needs a real `budget kill` against the sandbox, and a repository at its cap needs real spend to accumulate. **An agent performs none of it** — no live action, and never against `belong` (Global Constraints 2 and 6). So the task splits cleanly: the **recorder, the redactor and the replay source are agent-written and agent-tested against a synthetic frame** committed with them, and the **recordings themselves are a step the owner runs** with a documented command from `console/README.md`. Until the owner records them, the fixtures directory carries one synthetic, obviously-fake frame so the harness is exercisable and no screen has ever claimed to be tested against real output. The recordings are re-made whenever `watch --json` changes (C3).

**Tests:** `redact.test.ts` — a redactor that is *tested*, not trusted: it must remove a known address, a known repository name and a known PR URL from a recorded frame, must be idempotent, and must **fail closed** on a frame it cannot parse. `fixture.test.ts` — the replay source reproduces the recording's timestamps and **a gap in the recording is a gap in the replayed stream** (the assertion that makes the `stale` tests of Task 3.1 mean anything). Plus a test that every committed fixture passes the redactor unchanged, so a hand-edited fixture is caught — it is what will catch the first owner-recorded file the redactor should have altered.

**And the staged workflow's text is tested here, not in phase 4**, because this is the task that writes it — these two tests moved with the file:

- `TestConsoleTestsWorkflowIsStagedAndPinned` (in `internal/cli/docs_desktop_console_test.go`, the one Go file Task 0.3 allows to read a `console/` path, plus the `docs/pending-workflows/` path every docs test may read) asserts, over `docs/pending-workflows/console-tests.yml`: **every `uses:` is pinned to a full 40-hex commit with a `# vX.Y.Z` comment**; **the file contains no `secrets.` reference at all** — the tests job holds no secret, and this is the assertion that keeps the signing material in the other file; **no `pull_request_target`, no `workflow_run` and no `permissions: write-all`**; every job declares `permissions:` explicitly and only what it needs (`contents: read`); **`npm ci --ignore-scripts --no-audit --no-fund` is the install step** and there is no `npm install`, no `npm test` and no unpinned `npx`; **neither staged file exists under `.github/workflows/`** when this task merges — the assertion that an agent has not copied it in; and **no literal that could be a secret** appears (no `-----BEGIN`, no long base64 blob) — trivially true here, since the file names no secret at all, and asserted anyway because the two files were one file in an earlier revision and this is the test that would catch them being merged again.
- `TestConsoleWorkflowJobIsGateable` — **the assertion that keeps Task 4.6 from silently breaking every tag on this repository.** Over the same staged file it asserts: **a job whose key is exactly `console`** (the string in `release-gate.sh`'s `names`, so a rename must be a deliberate edit to this test *and* to Task 4.6); **the workflow triggers on `push` to `main` and on `pull_request`**; **the `console` job has no `if:` guard**, in particular none on a `changes`/`docs_only` output; and **the workflow has no `paths:` or `paths-ignore:` at the trigger level and the `console` job has none at the job level**. Each is a named sub-assertion. The reason is not style: `release-gate.sh` requires a check named `console` on the tag commit and, in its `identical_tree_checks` fallback, on the pull request head too — **a check that does not exist is not a success**, so a `console` job filtered to `console/**` would make `names="test terraform rules console"` refuse every release, including the ones that never touch the console. Ordering the gate edit after the copy-in does not help; the filter is the bug.
- `TestPendingWorkflowsReadmeNamesTheConsoleRow` asserts the README carries this file's row: what the file is, that it is staged because no agent may push a workflow here, and the one command that applies it.

**The review criterion on top of the tests**: a reviewer reads the pinned SHAs against upstream and confirms the tests job would run green on a tree where `console/` and nothing else has changed.

**Command:** `npm run test:unit`

**Depends on:** Task 1.3.

**Commit:** `console: recorded, redacted fixtures and the recorder that makes them`

**Red step — `Expected: FAIL`** — the redaction test against a recorder that passes a title through untouched, and the fixtures-are-clean test against a fixture with a planted address; `TestConsoleTestsWorkflowIsStagedAndPinned` before the staged file exists and against a planted `uses: actions/checkout@v5` and a planted `secrets.APPLE_CERTIFICATE`; and `TestConsoleWorkflowJobIsGateable` against a `console` job renamed to `console-tests` and against one carrying `paths: ['console/**']`. **Those last two mutations are the ones to run first**, because they are the failure that is invisible until a tag is refused.

**Owner-run step, named so it is not mistaken for agent work:** `console/README.md` documents one command — `npm run record-fixture -- --out test/fixtures/<name>.ndjson` — run **by the owner**, from the sandbox checkout, with the sandbox installation selected, under this repo's usual guard. The seven recordings are attached to the task's PR by the owner, or in a follow-up commit, and nothing in this plan's agent-run steps produces them.

---

### Task 1.5: The tests workflow, copied in — OWNER-RUN, no agent copies it

**Files:** `.github/workflows/console-tests.yml` (created by the owner, never by an agent), `console/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** This task exists because of a real hole in the previous revision of this plan: the staged tests workflow used to be copied in as Task 4.5, in phase 4, which would have meant **phases 1 through 3 — every task that writes console code — merged with no CI running a single console test.** The console's own tests would have been written, reviewed and merged on the strength of the reviewer's laptop. So the copy-in moves to the phase that first produces console code, and it is a separate task from the staging (Task 1.4) precisely because they have different authors: **an agent stages the file; only the owner applies it.**

The owner copies `docs/pending-workflows/console-tests.yml` to `.github/workflows/console-tests.yml` unchanged, commits it, and confirms on the PR that the `console` check appears and runs green on the first console pull request. **No agent in any later task copies, edits, renames or deletes this file** — Global Constraint 9 and the rule that a workflow change cannot be delivered by Fugaro's push both put it out of reach, and `TestConsoleTestsWorkflowIsStagedAndPinned` asserts the staged copy still exists under `docs/pending-workflows/` so a later edit to one file without the other is caught.

**The agent-run part of this task** is the record, and it is small on purpose: `console/README.md` gains a line naming when the workflow was applied (the applying commit), so the plan's reader can tell which console commits had CI and which did not — the gap above is a fact about history, and after this task there should be exactly one such gap, closed at this point. `TestPendingWorkflowsReadmeNamesTheConsoleRow` is extended to assert that line is present and that it names a commit, not a date.

**Tests:** `TestPendingWorkflowsReadmeNamesTheConsoleRow` (extended as above) plus the two workflow-text tests of Task 1.4, unchanged and still passing — the tests read the **staged** file, which is exactly why they keep working after the copy-in.

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

**Tests:** `projects.test.ts` — an unbuilt section renders as not-built and never as a zero; the inventory contains no value-shaped string. `replay.test.ts` — the ring's bound is enforced, an export round-trips through the fixture source, and scrubbing to a moment reproduces that moment's view. `projects.spec.ts`/`run.spec.ts` — goldens.

**Two of these are worth naming, because round 2 found both of them stated and untested in the design:**

- **The PR link is rebuilt, not trusted.** The document carries `pr_url`, and the console still does not open it: it **checks the host** (Task 2.0's allow-list), then **rebuilds** the URL from the run's own `repository` and `pr_number` fields and opens the rebuild. A frame whose `pr_url` points somewhere else — a redirector, a shortener, a typo'd or hostile host — fails the host check and is not opened at all. The test plants a `pr_url` on an allowed-looking-but-wrong path for the run's repository and asserts the opened URL is the rebuilt one, not the document's; a second case plants a `pr_url` on a disallowed host and asserts nothing is opened and the reviewer sees the refusal text.
- **The log panel is bounded.** The design's §2.3 bullet: the console runs a **capped** `logs RUN --json` window and **links** to the full transcript via `logs RUN --url` rather than following it — `--follow` would be an unbounded child process writing an unbounded amount of installation data into a renderer that cannot read it. The test asserts the panel renders exactly the cap, that a document longer than the cap is truncated rather than scrolled into a full transcript, and that **no spawn in the session log passes `--follow`** (the same session-log assertion Task 1.3's spawn-contract test uses).

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

**Files:** `docs/pending-workflows/console-release.yml`, `docs/pending-workflows/README.md`, `console/README.md`, `internal/cli/docs_desktop_console_test.go`

**Steps.** Write the **signing** workflow as a staged file under `docs/pending-workflows/` — this repo's mechanism for a change only the owner may push (its README is the directory's only occupant today, and `docs/release.md`'s "Before you tag" names copying a staged workflow in as an owner step). It builds on the CLI's tag, signs with the hardened runtime, notarises with `xcrun notarytool submit --wait`, staples both the app and the dmg, verifies with `codesign --verify --deep --strict`, `spctl --assess --type execute` and `stapler validate`, and **records those verification outputs** the way the CLI's release records its checksums. Actions are pinned to full commit SHAs with `# vX.Y.Z` comments, as everywhere else in this repo, and the Apple secrets it needs are named (`secrets.APPLE_CERTIFICATE`, `secrets.APPLE_CERTIFICATE_PASSWORD`, `secrets.APPLE_ID`, `secrets.APPLE_TEAM_ID`, `secrets.APPLE_APP_PASSWORD` or the App Store Connect API key triple, whichever D4 settles) but never valued. Extend the directory's README with this file's row: what it is, why it is staged, and the one command that applies it.

**Split from the tests workflow, and the split is a security boundary rather than tidiness — this is the correction round 2 asked for.** An earlier revision staged a single `console.yml` holding both the always-on pull-request test job (Tasks 1.4/4.2) and this signing job with the Apple secrets. That would have put **the signing key's job definition next to a job that runs on every pull request from any branch**, where the only thing between a fork's pull request and the signing material is GitHub's own rule that fork pull requests get no secrets — a rule about *forks*, not about branches of this repository, and a single `workflow_dispatch` or a future `if:` away from being the whole defence. Two files instead:

| Staged file | Task | Triggers | Secrets | Job key |
|---|---|---|---|---|
| `console-tests.yml` | 1.4 | `pull_request`, `push` to `main` | **none at all** | `console` |
| `console-release.yml` | 4.2 (this task) | `push` of a `v*` tag only | Apple signing material, via an `environment:` | `release` |

So this file: **triggers only on a tag push** — no `pull_request`, no `pull_request_target`, no `workflow_run`, no `workflow_dispatch`; **takes its secrets through a job-level `environment:`** naming a **protected GitHub environment** (required reviewers on the owner, deployment branches restricted to tags), which is the mechanism that makes the secrets unreachable from anywhere but the owner's approval; **runs `scripts/release-gate.sh` as the first step** — the same release gate the CLI's own tag is subject to, so a console release cannot bypass the checks the CLI must pass (C5, D18); and names the secrets **only as `secrets.NAME` references**, never as literals. The reasoning is written into the file's own comment header, and the test below asserts each clause of it, because a comment nobody tests is how the two files become one file again.

**Tests:** the workflow's *behaviour* cannot run here — it has never run, and the directory's README already carries a "Not verified" section that this adds a console row to. But its **text** is testable, and an earlier revision of this task left that as "the review substitutes", which is not a test:

- `TestConsoleReleaseWorkflowIsStagedAndPinned` (in `internal/cli/docs_desktop_console_test.go`, the file Task 0.3 allows to read a `console/` path, plus the `docs/pending-workflows/` path every docs test may read) asserts, over `docs/pending-workflows/console-release.yml`: **every `uses:` is pinned to a full 40-hex commit with a `# vX.Y.Z` comment**; **the trigger is a `v*` tag push and nothing else** — in particular **no `pull_request`, no `pull_request_target`, no `workflow_run` and no `workflow_dispatch`**; **the secrets are reached through a job-level `environment:`** naming a protected environment, and **every secret reference is a `secrets.` reference with no literal beside it** (no `-----BEGIN`, no long base64 blob, no `password:`/`token:` with a value); **`scripts/release-gate.sh` is the first step** of the signing job; **no `permissions: write-all`**, and the job's `permissions:` is declared explicitly; **the file does not exist under `.github/workflows/`** — the assertion that an agent has not copied it in (Task 1.5 copies the *other* file, and this assertion is over this one).
- `TestConsoleReleaseWorkflowHoldsNoPullRequestTrigger` is the named sub-assertion the security finding turns on, split out so the failure sentence names the bug: it greps the trigger block of the release file for `pull_request` and fails, with the message quoting the reason ("the signing job must not run on a pull request"). **The mutation that proves it**: adding `pull_request:` to the release file's `on:` must make this test red, and the red step below runs exactly that.
- `TestPendingWorkflowsReadmeNamesTheConsoleRows` asserts the README carries **both** console rows — this file's and Task 1.4's — each saying what it is, that it is staged because no agent may push a workflow here, and the one command that applies it.
- **The review criterion on top of the tests**: a reviewer reads the pinned SHAs against upstream, reads the protected-environment name against the repository's actual environment settings, and confirms the workflow would **fail closed on a missing secret** rather than silently skipping the signing step — a signing job that skips when a secret is absent ships an unsigned build under a signed release's name.

**And an agent still may not copy it into `.github/workflows/`** — Task 4.5 is the owner's step, and the "does not exist under `.github/workflows/`" assertion above is what makes an accidental copy a red test rather than a review miss.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.1.

**Commit:** `console: the signing and notarisation pipeline, staged for the owner`

**Red step — `Expected: FAIL`** — `TestConsoleReleaseWorkflowIsStagedAndPinned` before the staged file exists and against a planted `uses: actions/checkout@v5`; `TestConsoleReleaseWorkflowHoldsNoPullRequestTrigger` against a release file whose `on:` carries `pull_request:` — **run this one first**, because it is the mutation that reproduces the finding this task exists to fix; and `TestPendingWorkflowsReadmeNamesTheConsoleRows` before the README rows are added.

### Task 4.3: Check 59, the first signed build — OWNER-RUN, no agent performs any step

**Files:** `docs/gcp-live-checklist.md`

**Steps.** Write **Check 59** into the checklist, with its summary-table row, its FACT lines, its restore step and **Check 43's guard block verbatim** — the three hard stops, `--project fugaro` on every `fugaro` command, `--project "${P:?}"` on every `gcloud` command, re-run from a fresh shell before each step, and the sandbox checkout as the working directory for every `fugaro` command the console drives. The check is the owner's, and it asserts: (1) the certificate and the App Store Connect API key exist and are stored where only the owner can reach them; (2) a signed, notarised, stapled build is produced and **passes `spctl --assess` on a machine that has never seen it**; (3) the update round trip: an installed older build sees the new release and installs a bundle whose signature the OS verifies; (4) the safety assertion — with the console pointed at the **sandbox** installation, nothing it can reach belongs to `belong`, and the console's child process is the installed `fugaro` and nothing else. The section says in its first line that no agent performs any step of it.

**Assertion (4) is written as FACT lines with commands and expected output, because round 2 found it unchanged from round 1 — a sentence asserting a safety property with nothing under it.** It becomes four steps, each FACT-lined, each re-run from a fresh shell with the guard applied, and **none of them touching `belong` at all** — which is what makes it testable without doing the forbidden thing:

1. **The console's own record of what it ran.** The console writes a session log of every child process it spawned (Task 1.3's spawn contract, the same artifact its tests assert over). The FACT line is `grep -c belong "${LOGDIR}/session.jsonl"` with expected output **`0`**, and `grep -c edgeappinc "${LOGDIR}/session.jsonl"` with expected output **`0`**. A count of zero is a real assertion: the mutation control below shows the same command finding a hit when there is one.
2. **Every spawned argv resolves to the sandbox installation.** The FACT line is `jq -r '.spawns[].argv[0]' "${LOGDIR}/session.jsonl" | sort -u` with expected output exactly the installed sandbox `fugaro` path — one line, and not the `belong` installation's path, which the check names as a string it must *not* see. This is the assertion behind "the console's child process is the installed `fugaro` and nothing else": **one binary, and it is the sandbox's.**
3. **The installation the console read from is the sandbox's, by the console's own fields.** The FACT line is `jq -r '.project, .gcp_project' "${LOGDIR}/session.jsonl" | sort -u` with expected output the guard block's own sandbox values, `fugaro` and `${P}`'s project — so "it only saw the sandbox" is checked against the same variables the guard sets, not against a remembered name.
4. **The mutation control, and this is the step that makes the other three mean something.** With the console closed, the owner appends a single frame naming `belong` to **a copy of the log** — `${LOGDIR}/session.jsonl.test` — and re-runs step 1's command against the copy. Expected output **`1`**: the command that must say `0` says `1` when the thing it looks for is there. Then the copy is deleted, the real log is untouched, and step 1 is re-run to confirm `0` again. **No step of this check contacts, reads, or writes the `belong` installation** — the string `belong` appears only as text in a scratch file, which is the only way to prove a negative of this kind without performing it.

The check also FACT-lines `security find-identity -v -p codesigning` before and after, so "the console left no key material behind" is checked the same way: **the two outputs are identical**, and the check's restore step never deletes an identity it did not create (which is Assertion (1)'s record, above).

**The restore step restores only what the check created, and this is a hard rule of the check, not a courtesy.** A restore step that "removes the keychain item it was signed with" would delete **the owner's own signing certificate** — the identity the whole pipeline depends on, which predates the check by design and is not the check's to destroy. So: the check's first step **records what exists before it starts** — `security find-identity -v -p codesigning` and the list of the keychain items it can see — as a FACT line; the restore step then **removes only what is not in that record**: the installed build from `/Applications`, the downloaded dmg and zip, the notarisation submission artifacts, any temporary keychain or key material the check itself created, and any file it wrote under a temporary directory. **A signing identity that was present before the check is never deleted, never re-imported and never locked**, and the check says so in its own text so a later reader cannot mistake a follow-the-recipe restore for a licence to clear the keychain. (The one permitted deletion is of an identity the *check itself* created for the purpose, which is why the pre-check record is a FACT line rather than an assumption.)

**Tests:** the checklist's own docs tests, plus the guard test the project-centric plan schedules (Task 6.3's `TestChecklistChecksAreGuarded`), which Check 59 must satisfy, plus three named docs tests over the written section:

- `TestCheck59RestoreStepSparesPreexistingIdentities` asserts the section's restore step **does not name `security delete-identity`, `security delete-certificate` or `security delete-keychain` against anything present before the check** — the mutation-proven form: a restore step that clears the owner's identity fails a named test rather than reading as thoroughness in review.
- `TestCheck59AssertionFourIsFactLined` — **the test that makes Assertion (4) an assertion rather than a sentence**, and the answer to round 2's "unchanged from round 1": it asserts the section carries the four steps above, each as a `FACT` line with a command **and an expected output** — in particular that the two zero-count greps are present with `0` as the expected output, that the `jq` uniq-argv line is present, that the `jq '.project, .gcp_project'` line is present, **and that the mutation control is present and names the expected output `1`**. A section that says "nothing it can reach belongs to `belong`" without these lines fails this test. **The mutation that proves it**: deleting the mutation-control step from a copy of the section must make the test red, per Task 4.3's red step.
- `TestCheck59NeverNamesBelongAsATarget` asserts the section mentions `belong` **only in the prohibition** — as the string it must not see and the string it plants in a scratch file — and never as an argument to a command. Concretely: the string `belong` appears in exactly two kinds of line — a prose sentence stating the prohibition, and a `grep -c belong` FACT line over the console's own session log — and in no other command, argument, path or `--project` value. This is the machine-checkable half of Global Constraint 6 for this check, and it is what stops a later edit from "verifying" the negative the forbidden way.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.2.

**Commit:** `checklist: Check 59, the console's first signed build`

**Red step — `Expected: FAIL`** — the guard test against a Check 59 without the three hard stops; the restore-step test against a restore that clears a pre-existing signing identity; `TestCheck59AssertionFourIsFactLined` against the round-1 text — the bare sentence "nothing it can reach belongs to `belong`" with no FACT lines under it — and again against a section whose mutation-control step has been deleted; and `TestCheck59NeverNamesBelongAsATarget` against a section that "verifies" the negative with something like `fugaro --project belong budget show`, which is the mistake the test exists to catch.

### Task 4.4: Auto-update — SECURITY-SENSITIVE, independent opus review

**Files:** `console/src/main/updates.ts`, `console/test/unit/updates.test.ts`, `docs/pending-workflows/console-release.yml`

**Steps.** The design's §7.3: the update check reads the same release metadata the CLI's `internal/updatecheck` reads and honours the same opt-out (`FUGARO_NO_UPDATE_CHECK=1`, a local `update_check: false`, never in CI), and the installation path is Squirrel.Mac over a **signed** feed — an update whose signature does not verify is refused, never installed. No telemetry of any kind: the only outbound requests are the CLI's own and this one update check (design §7.4). Default on, with the toggle visible (D5).

**Tests:** `updates.test.ts` — the opt-out is honoured (all three ways); an unsigned or mis-signed bundle is refused; no identifier is sent (asserted on the request the fake feed receives); a downgrade is refused; the check never runs in CI.

**Command:** `npm run test:unit`

**Depends on:** Task 4.2.

**Commit:** `console: the update check, with no telemetry and a verified feed`

**Red step — `Expected: FAIL`** — the mis-signed-refusal test against an updater that installs anyway, and the no-identifier test against a request carrying a machine id.

### Task 4.5: The staged signing workflow, copied in — OWNER-RUN, no agent copies it

**Files:** `.github/workflows/console-release.yml` (**created and pushed by the owner — no agent creates, edits or copies this file**), `docs/pending-workflows/README.md` (the agent-run follow-up that records it as applied)

**Steps.** Task 4.2 stages the console's **signing** workflow; this is the step that puts it in place, and it is the owner's because this repository refuses an agent's workflow change outright (GitHub will not accept it, so a run that edits one cannot be delivered — Global Constraint 7). The owner copies `docs/pending-workflows/console-release.yml` to `.github/workflows/console-release.yml`, commits and pushes it, and **creates the protected GitHub environment the file names** — required reviewers, deployment branches restricted to tags — since the workflow is only as safe as the environment it reads its secrets from; a staging file cannot create an environment, and this is the step where the two must exist together. **The tests workflow is not this task and does not wait for it**: Task 1.5 copied `console-tests.yml` in during phase 1, so by now the console's unit and e2e tests have been running on every pull request and every push to `main` for three phases. This file adds the tag-time job and nothing else.

An agent then makes one docs-only follow-up: the README's console-release row gains the commit that applied it and the name of the environment that was created, and the "Not verified" note becomes "applied; unverified until the first tag".

**Tests.** The real test is the workflow's own first tag run, which is Check 59's step (Task 4.3) — and the ordering below is why that check is already done. The agent-run follow-up is covered by a docs test asserting the README row names the applying commit and the environment, so the record cannot be left as "to do".

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.3 — the check is what proves the signing pipeline works, and a workflow that signs a release before the pipeline has ever been proved would ship an unverified bundle.

**Commit:** the owner's own, for the copy-in and the environment; the agent's follow-up is `docs: record the console release workflow as applied`

**Red step — `Expected: FAIL`** — the README-row test before the applying commit is recorded.

### Task 4.6: The release gate requires the console check

**Files:** `scripts/release-gate.sh`, `internal/cli/docs_desktop_console_test.go`

**Steps.** `scripts/release-gate.sh` requires exactly the checks in `names="test terraform rules"`, so today a console job that fails would not stop a tag. Add `console` to that list — **and only after a workflow is in place that produces a check of exactly that name, running unconditionally** — which is **Task 1.5**, three phases earlier: the tests workflow's `console` job. (An earlier revision ordered this gate edit after Task 4.5, the *signing* copy-in, because a single staged file carried both jobs; the split of Tasks 1.4/4.2 moved the dependency with the job, and the gate now depends on the copy-in that happened first, not the one that happens last.) Ordered this way deliberately (D18): a gate that requires a check no workflow runs refuses every tag on this repository, including the ones that ship none of this, which is a worse failure than the one being fixed. **The ordering is necessary and not sufficient**: the check must also exist on *every* `main` commit and on every pull request head, because `identical_tree_checks` demands the same names there — which is Task 1.4's `TestConsoleWorkflowJobIsGateable` and the reason the console job carries no `paths:` filter (design §9.4). The comment at the top of the script and the `identical_tree_checks` doc comment both name the required set, so they are edited with it. **This is the only change in the whole plan to a non-test file outside `docs/` and `console/`** — an earlier revision called it "the one change to a file outside `docs/` and `console/`", which was false: Tasks 0.1, 0.2, 0.3 and 4.2 create and edit `internal/cli/docs_desktop_console_test.go` and `internal/cli/docs_console_boundary_test.go`.

**Tests:** a Go docs-style test, `TestReleaseGateRequiresTheConsoleCheck`, that reads `scripts/release-gate.sh` and asserts `names` contains `console` — the mutation-proven form, so a later edit that drops it fails a named test. It also asserts the script's own comment lists the same names, so prose and variable cannot drift; and **that `console` matches the job key Task 1.4's `TestConsoleWorkflowJobIsGateable` pins in the staged tests workflow**, so the two files cannot agree on a name that no job produces.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 1.5 (**hard**: this must not merge before the check exists — and it has, by three phases).

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

**Steps.** Kill, resume and cancel, if the owner says so (**D2**). Each is implemented **by invoking the CLI's own command**, never by writing to RTDB from the app: the typed confirmation, the admin check and the ETag compare-and-set stay in `internal/watch/keys.go` and `internal/cli/budget.go`, where they already are, and where a `budget kill` fails with `AdminDeniedText` — "you are not a budget admin" — when the operator is not one. **That text is the refusal of a write, and round 2 is why the plan says so here**: it is the only place in the CLI that tells a caller anything about the caller's role, it arrives *after* a denied write, and no read-only command reports the role at all. So the console treats **every identity as potentially admin** and its read-only guarantee rests on the exact-argument allow-list alone — it does not detect the role, does not grey out a kill button because the operator looks like a non-admin, and shows this text only when the CLI returned it. The console's own copy of the message is never composed from anything else. Rejected outright: any design in which the console writes.

## Cross-task dependencies

Every task's "Depends on" is a *build* dependency, and none of them is a merge dependency beyond its own phase: phases 1, 2 and 3 each leave a working artifact even if the next phase never ships, and phase 4's two owner-run steps (Tasks 4.3 and 4.5) sit after the agent-written packaging work, so 4.1/4.2/4.4 merge as ordinary reviewed PRs whether or not the owner ever runs the rest. The two places a reader might expect a dependency that does not exist: **phase 2 does not depend on project-centric phases 1 or 4** — it renders what exists and says so when a section is not built (Task 2.3) — and **phase 4 does not depend on the CLI's release process being changed**; it adds an artifact to the existing tag, which is what D11 decided.

**Three dependencies that are real and are *merge* dependencies, not build ones**, listed here because a reader checking the "nothing in phase *n* is required by phase *n-1*'s merge" claim would otherwise read them as exceptions it missed:

- **Task 1.5 (the owner's copy-in of the tests workflow) must merge before Task 4.6 (the gate edit).** Merged the other way round, `scripts/release-gate.sh` requires a check that no workflow produces and **every tag on this repository is refused** — including the releases that ship none of the console. This is the one ordering in the plan whose inversion breaks other people's releases, and it is why 4.6 is its own PR group (I) rather than sharing G's. **The ordering alone is not enough, and the plan says so where it matters**: the gate names a check, so the check must exist on *every* `main` commit and on every pull request head for the `identical_tree_checks` fallback — which is why the console job carries no `paths:` filter and why Task 1.4 asserts its name and triggers (`TestConsoleWorkflowJobIsGateable`). A path-filtered, correctly-ordered console job would still refuse every tag. **Round 2 is why this bullet names Task 1.5 and not Task 4.5**: the copy-in of the tests workflow moved to phase 1, the signing workflow's copy-in stayed in phase 4, and the gate follows the job it names.
- **Task 4.2 (the staged signing workflow) must merge before Task 4.5**, trivially — the owner has nothing to copy otherwise. Task 4.2 does **not** gate Task 4.6, and the signing job is not the check the gate names.
- **The recordings of Task 1.4 do not gate any merge at all.** They are owner-run data, they land when the owner runs the recorder, and the fixture harness is green without them (the synthetic frame, C7). A stale or absent recording is a test failure, not a broken build.

**And the fact a reader should not have to infer — corrected in round 2, because the previous revision had this wrong by three phases: no CI runs a single console test until Task 1.5.** The console's tests exist from Task 1.4 onward; until the owner copies `console-tests.yml` in, they are run by the task commands above and by whoever is working on them, and nothing else runs them. Task 1.5 closes that gap **inside phase 1**, so from Task 1.5 onward — the whole of phases 2, 3 and 4 — every pull request and every push to `main` runs the console's tests, and the only uncovered window is Tasks 1.0 through 1.4: the toolchain, the window, the IPC allow-list, the source, and the fixtures. The signing workflow is still staged and unapplied until Task 4.5, which is deliberate (it holds the Apple material) and is not a testing gap: what it adds is a tag-time job, and the tests it would run have been running since Task 1.5.

## Decisions this plan defers to the owner

Its own, continuing the design's D-series with **C1 onward**, each vetoable before its phase starts:

| # | Question | Recommendation |
|---|---|---|
| **C1** | Is phase 0's boundary test (Task 0.3) enough to keep the Go module untouched, or does the owner want `console/` excluded from the module by a build constraint too? | **The test is enough.** `console/` holds no Go file, so `go build ./...`, `go vet ./...` and `gofmt -l .` never see it; a build constraint would add a mechanism where a test already gives the guarantee. The boundary it enforces is **imports and `go:embed`, not bytes**: the one-entry allow-list (Task 0.3) is what lets the docs tests read `console/docs/json-contract.md` as a string, and any second entry is a deliberate edit to a test. |
| **C2** | Does the console need its own `docs/release.md` section, or does it belong in the CLI's? | **Its own section in the existing file**, so the two artifacts' gates and Highlights are read in one place — `docs/release.md` is the single source of truth for the tag. |
| **C3** | Who owns the recorded fixtures' refresh, and when? | **The owner, at each release that changes `watch --json`**, with the redactor failing closed (Task 1.4) so a stale fixture surfaces as a test failure rather than a plausible screen. |
| **C4** | Does the console need a Homebrew cask of its own, and does it keep the CLI cask's `xattr -dr com.apple.quarantine` postflight? | **A cask of its own, and no quarantine hook**: the hook exists because the CLI's binaries are not notarised, and a notarised, stapled console does not need it. Removing the hook is the visible proof the notarisation works. |
| **C5** | Does the console ship a `--json` mode of its own (a headless `console --json`) as the CLI does? | **No.** The CLI already has the machine view; a second one would be a second contract to version. The console is a renderer. |
| **C6** | Does the release gate gain the console check (Task 4.6) in the same release that first ships the console, or the one after? | **The same release.** Waiting a release means the first console build ships ungated, which is the hole Task 4.6 exists to close; by then the check has run on **every pull request and every push to `main` since Task 1.5**, three phases of track record rather than one task's worth, because round 2 moved the copy-in of the tests workflow into phase 1. |
| **C7** | Task 1.4's seven recordings are owner-run. Does phase 1's PR merge with the synthetic fixture and the recordings follow, or does it wait for them? | **Merge with the synthetic fixture** — the recorder, the redactor and the replay source are the reviewable artifacts, and the recordings are data that lands in their own commit. Blocking the shell on a live sandbox session would put owner work on the critical path of an agent-runnable PR, which is the thing this plan avoids everywhere else. |

## Open items, listed rather than fixed

Review round 2 raised a set of small findings. What was cheap to fix is fixed and said so where it lives (the `--project` exception in Task 1.3 and §3.4 rule 3; the admin-role claim in Task 5.2 and D14; the `D6.4` citation, which is now D15, in §6.2; `readRun`'s real file, `internal/cli/runs.go` rather than `watch_queued.go`, in §2.1; the log panel bounded-and-linked in §2.3 and Task 2.3; and `pr_url` **host-checked and then rebuilt from the run's repository and PR number** rather than opened as-is, in §5.1 rule 2 and Task 2.3). These are the ones that are not fixed here, with the reason each is left open rather than answered by assertion:

1. **The unit-test runner is still unnamed.** The plan pins `actions/setup-node` to `console/package.json`'s `engines.node` and mirrors the emulator precedent's `"node": ">=20"`, which is the toolchain *version* story; it does not name whether `npm run test:unit` is vitest, `node:test`, or something else, nor the e2e invocation behind `xvfb-run`. That is Task 1.0's decision — it is the task whose whole subject is pinning the toolchain and whose review criterion is that every pin is exact — and writing a runner name here that Task 1.0 may reasonably change would be a plan asserting a version of a task it does not own. **What the plan does fix is the shape**: `npm ci --ignore-scripts --no-audit --no-fund`, a committed lockfile, and two scripts the CI file calls by name.
2. **The `logs` line cap has no number.** §2.3 says "bounded", §5.1 says "a hard console-side line cap and a timeout", and Task 2.3 tests that the panel renders exactly the cap and truncates beyond it — but the cap itself is not a number in either document. It belongs with the JSON contract Task 0.1 pins, because it is a property of how much of a run's log the console will hold, and a number chosen here would be a guess reviewed as a decision.
3. **The version handshake's parser tolerance is stated, not pinned.** The design says the handshake runs `fugaro version` bare and compares a supported range, and that a build reports `dev` when it is not built with the release `-ldflags` (`internal/cli/root.go`). **What a `dev` build does to the range check is not decided**: refusing would make the console unusable for anyone building the CLI from source, and accepting would make the handshake silent exactly where it is most needed. This is the one open item that could reasonably become a task; it is left for the owner's ruling alongside D19, since gap 2 (a machine-readable `version --json`) is what makes it disappear.
4. **Whether the Projects screen gains the policy source inside this plan's lifetime** depends on project-centric phase 1 (PR #390) landing and on D16's ruling; the plan's answer is the explicit *not readable with this CLI* state, and nothing more is promised.
5. **Dependabot's scope is not extended here.** §5.2 notes that the console adds a second thing Dependabot should watch — `console/package.json` and its lockfile — and that Electron's own binary is not an ordinary npm dependency at all. `.github/dependabot.yml` is not under `.github/workflows/`, so an agent may edit it, and this plan deliberately does not: it belongs with Task 1.0's pins, and the owner may prefer to keep Dependabot's scope where it is rather than have a weekly PR stream behind an approval gate. **Listed so that the omission is a decision, not an oversight.**
6. **Whether `watch --json` should keep emitting `pr_url`.** Now that the console checks the host and rebuilds the link from `repository` and `pr_number`, the field is a convenience the console never trusts. Removing it would be a CLI change in a CLI plan (§4.3's gap list does not carry it), and keeping it is harmless as long as the rebuild stands — which is why the rebuild is a test in Task 2.3 and the field's fate is not decided here.
7. **The update check is duplicated by construction.** Task 4.4 says the console "reads the same release metadata the CLI's `internal/updatecheck` reads and honours the same opt-out" — it cannot import that package, so the TypeScript side re-implements the comparison. Two implementations of one rule is a drift risk this plan names and does not solve; the mitigation is Task 4.4's test that the opt-out is honoured all three ways and that a downgrade is refused, which is the behaviour both implementations must agree on.
8. **Check 59's number is contended.** PR #393 takes 58; whichever of the two merges second renumbers its own section, summary row and references. That is this repo's convention and not a defect, but it is a merge-order dependency between two PRs that this plan cannot resolve from here.

## OWNER APPROVAL GATE

**No code before the owner approves the design and this plan.** Concretely: `docs/design/desktop-console.md` and this file are the deliverable of the PR that adds them; Tasks 0.1 onward are future work; and **no task in this plan starts until the owner has ruled on the design's D1 to D19** (its §10) and, if any recommendation is rejected, Task 5.0 has rewritten the design to the ruled answer. The three that gate the most: **D1** (the sidecar, which decides everything about the data plane), **D3** (this repository, which decides the CI and the module), and **D4** (Apple signing, which would be the first private key this repo's release path has ever held, and which every step of phase 4 and all of Check 59, and both copy-ins (Tasks 1.5 and 4.5), is owner-run because of).
