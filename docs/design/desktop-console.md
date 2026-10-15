# The Fugaro desktop console

Status: **draft, for the owner's approval — no code, no scaffolding, nothing to run.** This is the design the owner's spec asks for in its section 6, "Forward-looking: Fugaro desktop console" ([project-centric-spec-source.md](project-centric-spec-source.md) §6: a cross-platform Electron app, essentially a richer graphical `fugaro watch` — live task monitoring, spending monitoring with graphs, project-level views consistent with the project-centric model, with the task manager at [tmog.org](https://tmog.org) as visual inspiration). It is written against `origin/main` at `b9d5c5d`; every claim about the code names the file and the symbol it was read from, and the plan ([docs/plans/2026-10-15-desktop-console.md](../plans/2026-10-15-desktop-console.md)) re-checks each citation in the task that touches it, because line numbers drift as PRs merge. What could not be settled from the code is listed in §12.

Related: [project-centric.md](project-centric.md) (§9 is the one paragraph that says this work is unplanned and names the missing views), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (the budget dashboard and Firestore data design — the spend half of this console's data), [v1.md](v1.md) §6 (the security model), [SECURITY.md](../../SECURITY.md) (the same model, owner-facing), [watch-queued.md](watch-queued.md), [config-transitions.md](config-transitions.md), [compute-backend.md](compute-backend.md).

## 1. Summary and recommendation

The console is **a graphical `fugaro watch` that adds spending graphs and project views, and it gets every byte of its data from the CLI it already has.** It has no backend, no service account, no database client and no credential of its own. It spawns the installed `fugaro` binary as a child process, reads that process's JSON output, and draws it. Nothing in the console may write to the cloud: the writes an operator wants (kill, resume, cancel) are the CLI's own commands, with the CLI's own ETag-guarded compare-and-set and its own typed confirmations.

That single decision — **the CLI is the data plane** — answers most of the questions the spec's section 6 leaves open, and it is the reason the console can be built at all without a new backend:

1. **It reuses exactly what `fugaro watch` and the budget dashboard read.** The four RTDB event streams, the Firestore `spendDaily` documents and the runs bucket are read by the CLI, under the operator's own identity, through the code `watch`, `budget` and `report` already ship. The console adds no path, no field and no reader (§2).
2. **It never sees a secret.** No command the console invokes copies a secret value to the machine (`SECURITY.md`, "Cloud keys stay in the cloud": "No command copies a secret value back to your machine, and `fugaro secrets` never prints one"), and the console's spawn allow-list excludes every subcommand that touches Secret Manager (§3).
3. **Its access is exactly `fugaro watch`'s access**, because it *is* `fugaro watch`, one layer up: the same operator, the same gcloud/Firebase identity, the same read-only role — `roles/firebasedatabase.viewer` and `roles/datastore.viewer` on the Firebase project, granted to launchers and operators by `deploy/terraform/gcp/modules/firebase/iam.tf` (`google_project_iam_member` for both roles) (§3).

The one place this design disagrees with the repo's own instincts is worth stating up front. **tmog.org markets itself on not being Electron** — its own "why" page says, in its list of facts, "No Electron, no web view, no embedded browser", and its three hosts are Swift/AppKit, C++/Win32 and C++/Qt 6 over one shared C++20 core. The owner's spec asks for Electron and names tmog.org as the *visual* inspiration, so this design takes the information design and the engineering honesty from tmog.org and none of its technology — and it takes one of its principles as a hard requirement of our own (§6.1). If the owner wants tmog.org's actual posture (native shells over a shared core), that is a different, much larger design, and it is decision **D8**.

## 2. Data sources: exactly what the CLI already reads

The console reads **no cloud service directly**. Its inputs are the JSON of the CLI's own read-only commands. What follows is therefore (a) the commands the console consumes, and (b) — because the owner asked for it — the underlying RTDB/Firestore/bucket paths those commands read, with who may read each under the rules.

### 2.1 The four commands the console consumes

| Command | What it gives the console | Emitted by |
|---|---|---|
| `fugaro watch --json` | The live feed: one JSON document per change — project, totals, per-repository caps and burn, runs with stage/round/models/spend/age, kill state, connectivity, finished runs | `internal/cli/watch.go` `newWatchCmd` (owns the `--json`, `--plain`, `--once`, `--interval`, `--repo` flags), `internal/watch/json.go` `BuildJSON(project string, v View) JSONDoc` |
| `fugaro budget show --json` | Caps (global, defaults, per-repository), today's `counted` and `spent`, notional spend, kill switches, headroom, active runs, **and the effective per-run cap with the scope that binds it** (repository, defaults or the global fallback) | `internal/cli/budget.go`; the source-of-the-cap work is `internal/cli/budget_preview.go` (project-centric plan Task 0.3 step 7 / #373) |
| `fugaro report --json` | Spend over time: `--by day\|week\|month\|year\|repo\|model\|person`, `--since`, `--until`, `--repo`, with `NOTIONAL~`, compute and model dollars kept separate | `internal/cli/report.go`, `internal/report/report.go` `Build` |
| `fugaro runs ls --json` (and the `runs` verb it hangs off) | The run list with its totals and warnings — the run-detail screen, and the whole of the no-Firebase degraded mode, whose document this is | `internal/cli/runs.go` `newRunsCmd`/`newLsCmd`, `runLs`, `lsDoc` |
| `fugaro diagnose RUN --json` | The per-run diagnostic view | `internal/cli/diagnose.go` `newDiagnoseCmd` (`Use: "diagnose RUN"`) |
| `fugaro logs RUN --json` | One JSON object per log line (`time`, `severity`, `stage`, `stream`, `event`, `message`), plus `--url` for the console link — the run-detail screen's log panel reads this, and never a transcript body | `internal/cli/logs.go` `newLogsCmd`, `logLine` |
| `fugaro version`, `fugaro doctor` | The handshake (§8.3) and the environment view | `internal/cli/root.go`, `internal/cli/doctor.go` |

**`watch` is not only a database reader.** Besides the four RTDB streams it scans the **runs bucket** for the two lists a live view needs and no database has: the runs that are queued or claiming and the runs that have finished in the last 24 hours. `internal/cli/watch_queued.go` (`openQueueBucket`, `scan`, `scanRepo`, `scanRun`, `readRun`) lists `runs/<slug>/<run>/` through `blobx.Open(lc.BucketURL())` and reads `task.json`, the `launching` claim, `launch.json` and `result.json` (plus the `cancel` marker), folding them into `watch.QueuedRun`, `watch.FinishedRun`. Those become `queued`/`stuck` on a run row and the `finished[]` and `ready[]` arrays of the JSON document. **That means the console inherits a bucket dependency as well as a database one** — and it is the reason the console's run list can show "queued but not started" and "ready for your review" at all, two rows the RTDB alone cannot produce. It also explains a fact the console's install story must state: a `fugaro` that can read the database but not the bucket shows a live view with those sections empty rather than wrong.

The JSON shapes are a real contract, not an accident: `internal/watch/json.go` declares them as named types — `JSONDoc`, `JSONTotal`, `JSONRepo`, `JSONRun`, `JSONKill`, `JSONBurn`, `JSONConn`, `JSONFinished` — and `BuildJSON` is their single constructor. `internal/cli/json_everywhere_test.go` asserts that the CLI is JSON-capable across its surface, which is why this design can rely on `--json` existing rather than inventing flags. **The console pins the schema version it was built against and refuses to guess** (§8.3): a field it does not know is ignored, a field it needs and does not find is `unavailable`, never a zero (the same rule tmog.org states and `watch` already follows — the registry's live shape has no `round` or `spent` until a run reports them, and `watch` shows `-` for an absent field, `m9-budget-and-dashboard.md` §7).

### 2.2 The RTDB paths behind `watch`, and who may read them

The tree, its fields and the rule that governs each node, from `m9-budget-and-dashboard.md` §6.2 and §6.4, whose generator is `internal/budget/rules/rules.go` over the template `internal/budget/rules/rules.json.tmpl` (golden file `internal/budget/rules/testdata/rules.golden.json`; parity in `internal/budget/rules/parity_test.go`; emulator cases in `emulator_test.go`, `lease_emulator_test.go`, `harness_test.go`).

| RTDB path | Fields | A run token may read | A launcher/operator may read | Note |
|---|---|---|---|---|
| `config/caps/global` | `dailyMicros`, `perRunMicros` | yes (any valid run token) | yes | The global cap |
| `config/caps/defaults` | `repoDailyMicros`, `repoPerRunMicros` | yes | yes | The default per-repository cap |
| `config/caps/repos/<slug>` | `dailyMicros`, `perRunMicros`, `repo` | only with `auth.token.fs == <slug>` | yes | Per-repository cap |
| `config/mode` | `observe` / `enforce` | yes | yes | Whether the caps are advisory or binding |
| `config/limits` | `maxReserveMicros` | yes | yes | The per-write reservation bound |
| `config/kill/global` | `on`, `by`, `at`, `reason` | yes | yes | The kill switch, with who and why |
| `config/kill/repos/<slug>` | `on`, `by`, `at`, `reason` | only own slug | yes | |
| `spend/<day>/global` | `counted`, `spent`, `notional`, `calls` | yes | yes | One of `watch`'s four streams |
| `spend/<day>/repos/<slug>` | those, plus `byModel/<m>/{micros,in,out,cr,cw}` | yes | yes | One of `watch`'s four streams |
| `spend/<day>/runs/<slug>/<run>` | `reserved`, `released`, `spent` | only the run's own | yes | **Not needed by the console**: `watch` deliberately does not subscribe to the per-run day shares |
| `agents/<slug>/<run>` | `repo`, `workflow`, `title`, `stage`, `round`, `verify`, `coder`, `reviewer`, `auth`, `prUrl`, `startedAt`, `stageStartedAt`, `stageDeadline`, `updatedAt`, `spent`, `halted` | writes its own; reads its own | yes | One of `watch`'s four streams; the field list is what the console's run table draws |
| `outcomes/<day>/<slug>/<run>` | `status`, `requestedBy` | **no — the node carries no `.read` at all**, so no token reads it, not even the run's own | yes | The outcome the history job counts. Easy to miss, and a trap for anyone who builds a viewer on a *Firebase* token instead of the operator's own credentials: this node, and every other repository's rows, would be invisible |
| `runs/<slug>/<run>` | `reserved`, `released`, `spent`, `notional`, `overrun`, `tokens`, `exp` | only the run's own | yes | The lifetime ledger |
| `fugaro/project`, `fugaro/mark` | the project id and the installation's mark | **no** (`.read: false` at the node) | yes | The rules deny every token read and write of `fugaro/*`; IAM bypasses rules. The operator reads `fugaro/project` to confirm the database belongs to the selected project before drawing anything |
| `config/policy/**` (project-centric phase 1, **not built**) | `shared`, `repos/<slug>`, `admin` | per child: `shared` yes, `repos/$slug` only own slug | yes | `admin` is admin-only and holds no secret-adjacent data (project-centric §5, §12a item 7) |

**Two different notions of "who may read", and the console must not confuse them.** A **run token** is what a run holds (`auth.token.fs`/`fr`/`fx`, minted by the launcher, `m9-budget-and-dashboard.md` §6.4); the rules bound it per node as the table shows. A **launcher or operator** holds no run token at all: they hold IAM — `roles/firebasedatabase.viewer` and `roles/datastore.viewer` on the Firebase project, plus the `fugaroTokenMinter` custom role on the signer account and nothing else (the grant table, `m9-budget-and-dashboard.md` §6.1, and `deploy/terraform/gcp/modules/firebase/iam.tf`). **IAM is not subject to the database rules**, so a viewer sees every node the table's third column marks "yes" — including every repository's counters and registry entries, which is exactly what `fugaro watch` shows today. The console inherits that reach and nothing more.

The console must also be honest about what a launcher can reach *besides* the database, because the projects screen shows inventory: `SECURITY.md` records that the `fugaroLauncher` custom role "additionally lets any launcher list every secret in the project (names, labels and version counts; reading a value still needs `secretAccessor`, which only the job and build accounts hold)". A names-only inventory is therefore something the operator can already obtain; **the console shows secret names and versions and never a value**, and it does not add a reader for them either — it never calls `fugaro secrets` at all (§3.3).

### 2.3 Firestore, the runs bucket and what `report` reads

- **Firestore `(default)` database, collection `spendDaily`**, documents `<YYYY-MM-DD>_<slug>`: `repo`, `slug`, `date`, `spentMicros`, `notionalMicros`, `computeMicros`, `unreconciledMicros`, `overrunMicros`, `runHours`, `calls`, `runs`, `outcomes{succeeded,failed,halted,cancelled,infra_error}`, `byModel{<model>:{micros,in,out,cr,cw}}`, `byPerson{<requested_by>:{micros,notionalMicros,runs}}`, `capDailyMicros`, `final`, `version`, `archivedAt`, `writtenAt`, and the mark document `meta/installation` (`m9-budget-and-dashboard.md` §6.2). **Read by the operator's IAM** (`roles/datastore.viewer`) — the Firestore rules deny everything and only IAM principals read or write. This is the console's spend history, reached through `fugaro report --json`.
- **The runs bucket** (`fugaro/` objects, `runs/<slug>/…`): `task.json`, `result.json`, `launch.json`, the log references, `check.json`. Launchers hold `roles/storage.objectViewer` on the whole bucket (and a `runs/`-conditioned `roles/storage.objectUser` since the 0.7.0 hardening, `SECURITY.md` and [bucket-iam.md](bucket-iam.md) §10). The console reaches this through `fugaro runs ls --json` and `fugaro logs`, not directly.
- **`byPerson` and identity.** Firestore keeps the requesters' addresses indefinitely and only IAM viewers can read them; `report --by person` shows them. This is the one place the console displays something personal, so it is the one place with a switch: **§6.4, the person view is off by default in the console** even though the CLI prints it, because a shared screen is not the same as a terminal.
- **Live logs.** The console links a run to its log stream (`fugaro logs`) and does not embed a transcript: logs are per-run, large, and redaction is best effort.

## 3. Authentication: three options, priced

The requirement is that the app acts **as the signed-in operator, with the same read-only access `fugaro watch` has**, that it **never reads or stores a secret**, and that it **never writes**.

### 3.1 Option A — the CLI as a sidecar (recommended)

The Electron main process spawns the installed `fugaro` binary and reads its JSON. The child process inherits the operator's own environment and uses whatever identity `fugaro` already uses for that project (the same path `fugaro watch` takes to `roles/firebasedatabase.viewer`).

- **Credential story:** the console holds none. The CLI holds none either — it presents the operator's ambient cloud identity, and no Fugaro command copies a secret to the machine. Nothing about the operator's identity is stored in the app: no token, no key, no config copy.
- **Read surface:** identical to `fugaro watch` by construction, because it *is* `fugaro watch`.
- **Write surface:** none. The spawn allow-list is an explicit list of subcommands, all read-only (§3.3).
- **Cost:** the console needs a `fugaro` on `PATH` (or a configured path); the JSON schema becomes a compatibility contract (§8.3); a child process per data source is more overhead than one HTTP client; and the console's release train must stay near the CLI's.
- **Why it wins:** it is the only option that satisfies "reuse exactly what `fugaro watch` and the budget dashboard read, no new backend" literally rather than approximately. Every future improvement to a data source (the phase-1 policy object, the compute-backend cost rows, the `budget show` cap source) reaches the console with a CLI release, and **no reader is written twice**.

### 3.2 Option B — direct RTDB/Firestore from the app (rejected)

The app embeds the operator's credentials (Application Default Credentials, or a Firebase token) and speaks to RTDB's REST API and Firestore itself.

- **Cost:** the app must reimplement the RTDB REST client with its streaming and its ETag semantics, the Firestore query, the burn-rate computation, and — critically — the **sanitization of job-written text** (`watch`'s rule that every job-written field is stripped of control and escape characters and clipped, `m9-budget-and-dashboard.md` §7). A missed sanitization in a *renderer* is a script-injection, not a mangled terminal line.
- **The disqualifier:** it puts a cloud credential inside a Chromium process whose entire purpose is to render untrusted, job-written strings. That contradicts this design's own hard rule (§5.1), contradicts the "never read or store a secret" requirement in spirit (an OAuth token in an app's userData *is* stored material), and buys only latency.
- **Verdict:** rejected. If it is ever revisited it would have to be as Option C, not as this.

### 3.3 Option C — a local read-only API served by the CLI (the designed evolution)

`fugaro console serve --listen 127.0.0.1:<port>` serves the same JSON over loopback, and the app is a thin client. It is Option A generalised: the same reader, the same identity, one process instead of one per screen.

- **What it must have to be safe:** loopback-only binding; a per-launch bearer token the CLI prints and the app must present; `Origin`/`Host` checks; no CORS; and the read-only subcommand allow-list of §3.3, unchanged.
- **Why it is not v1:** it adds a listening socket to the CLI, which is a new attack surface for a program that today only makes outbound calls. That is a real cost for a benefit (less spawn churn) that v1 does not need.
- **Why it is designed now:** so that v1 puts its data access behind one interface (§8.2) and C can replace A without touching a single screen.

### 3.4 The recommendation

**Option A for v1, behind an interface that makes Option C a drop-in replacement; Option B never (D1).** Concretely, and enforceable in review:

1. The console spawns **one** binary, from a **fixed list of read-only subcommands**, with `shell: false` and arguments passed as an array — never a shell string, never a command built from a job-written field.
2. The allow-list is: `watch --json`, `budget show --json`, `report --json`, `runs ls --json`, `diagnose --json`, `logs --json` (`--url` or a line stream, never `--follow` unbounded), `version`, `doctor`. **Not** `budget set`, not `budget kill|resume` (they have no `--json` and they write), not `cancel`, not `run`, not `secrets`, not `init`, not `upgrade`, not `exec`, and not the hidden `budget history` job command. A command that is not on the list cannot be reached from the renderer at all: the IPC channel takes a *name*, and the name is looked up in the list.
3. The console never sets a cloud credential, never passes `--config`, `--project` or any credential-shaped flag from renderer input, and never reads `~/.config/fugaro`. Whatever `fugaro` resolves ambiently is what it gets, exactly as in a terminal.
4. `fugaro version` is the handshake (§8.3): the console knows which CLI it is driving, and says so in its header.

## 4. v1 scope

### 4.1 In

1. **Live runs** — the default screen: the project line, each repository's cap bar and burn rate, and each run's stage, round, models, spend and age, with `silent` (>60 s without a heartbeat) and `lost` (>3 min) flags, and the stage-deadline amber past 80%. This is `fugaro watch`'s live view, at a window size a terminal cannot have.
2. **Spend graphs** — over time (`day`/`week`/`month`), by repository, by model, and with `NOTIONAL~` kept visibly separate from real dollars and compute kept separate from both (the "one account of cost" rules, `m9-budget-and-dashboard.md` §10: the three never sum). Caps shown as a default-versus-override table with the binding scope named.
3. **Project views** — the project-centric screen: the project's caps and kill state; the enrolled repositories and the ones the operator has *seen* run; the policy object and the routing switch **when they exist** (project-centric phases 1 and 1b; not built today); project jobs; and a names-and-versions inventory of secrets and accounts.
4. **Degraded and offline modes** — the no-Firebase mode (`watch` polls `ls` every 10 s, runs grouped the same way, each run's cost from `result.json`, no caps and the kill keys disabled), the live-data-stale banner after 45 s without an event, and the CLI-absent/CLI-skew screens (§6.5).
5. **Packaging** — macOS, signed and notarised, with auto-update (§7).

### 4.2 Out, explicitly

- **Any write.** No kill, no resume, no cancel, no launch, in v1. The app may *show* the exact command (copy to clipboard), and §3.4's allow-list makes an accidental write unreachable. **D2** asks the owner whether v1.1 adds the three write buttons, and if so, always by invoking the CLI's own command so the confirmation, the admin check and the ETag compare-and-set stay where they are.
- **Secrets values, ever** — not in v1, not later. Names, labels and version counts only.
- **A new backend, a new service account, a new RTDB node, a rules change.** This design adds none. If a view needs data no command emits, the answer is a CLI `--json` field, not a console reader.
- **Streaming transcripts and log bodies inline.** Logs are linked, not embedded.
- **Multi-project aggregation.** The m9 design calls cross-project views "later, read-only" (`m9-budget-and-dashboard.md` §6.0); the console has one installation at a time in v1.
- **Windows and Linux as *packaged* deliverables** — the code stays cross-platform and CI runs on Linux (§9.4), but signing and distributing two more platforms is a later phase (**D10**).
- **The backing-repository history view** (project-centric phase 6 — `fugaro project export` does not exist yet), **OAuth quota** (there is no per-person budget to show; OAuth spend is notional), **the recipe/config editors**, and **any telemetry** (tmog.org's own posture: "TMOG sends no usage statistics, hardware inventory, process information, or identifiers. Its only routine request is an update check"; ours mirrors it, §7.4).

## 5. Electron security posture

The console's threat is unusual and worth naming precisely: its renderer displays **strings written by a coding agent running with `--dangerously-skip-permissions` in a cloud container**, plus task text copied from issues and pull requests. `watch` already treats every job-written field as hostile and sanitizes it. In a browser engine, the same strings are a script-injection surface, so the posture below is not boilerplate — it is the load-bearing part of the design.

### 5.1 The hard rules

1. **The renderer is untrusted by construction.** `contextIsolation: true`, `nodeIntegration: false`, `sandbox: true`, `webviewTag: false`, `nodeIntegrationInWorker: false`, `allowRunningInsecureContent: false`, `experimentalFeatures: false`. The renderer gets no `require`, no `process`, no filesystem, no network.
2. **No remote content, ever.** The window loads a bundled `file://` document. `will-navigate` and `setWindowOpenHandler` **deny** every navigation and every window; an external link goes to the OS browser through a single `openExternal` path that validates the URL is `https:` and on an allow-list of hosts (the repository, the provider's console, the docs). No `loadURL` to a remote origin, no remote `<script>`, no remote font, no remote image.
3. **A deny-by-default CSP**, delivered as a header on the bundled document and, belt-and-braces, as a `<meta http-equiv>`: `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'`. **`connect-src 'none'` is the important one**: the renderer never talks to the network, because every byte arrives over IPC. The console's only network traffic is the CLI child process's — an outbound call the CLI already makes and the app never sees.
4. **An IPC allow-list, not an IPC bridge.** The preload exposes a small, frozen, versioned object of named operations (`subscribeLive`, `runReport`, `openExternal`, …). Each channel's handler validates its arguments against a closed schema, takes no path, takes no command, takes no URL other than the validated one, and returns already-rendered data. No generic `invoke(channel, …)`, no channel name from the renderer, no dynamic `require`.
5. **Job-written text is text.** The UI renders it through text nodes (or an escaping template), never `innerHTML`, never a `dangerouslySetInnerHTML`-equivalent, never a `href` built from it. The console re-applies `watch`'s sanitization at the display layer rather than trusting the transport to have done it, because the transport is a subprocess boundary the app does not own.
6. **No new privileges for the child.** The spawned CLI runs with the operator's own environment and nothing added: no injected `GOOGLE_APPLICATION_CREDENTIALS`, no token in an argv (argv is visible in the process table), no secret in an environment variable the app writes.
7. **Deny every web permission.** `session.setPermissionRequestHandler` refuses camera, microphone, geolocation, notifications, clipboard-read, everything. No permission is ever granted.

### 5.2 Supply chain and dependency pinning

The repo already has the right precedent, in the one Node dependency it ships: `internal/budget/rules/emulator/package.json` pins `firebase-tools` to an exact version (`15.19.0`), commits a `package-lock.json`, declares `"private": true` and `engines.node >= 20`, and says in its own description that it should be bumped "on purpose, with the rules tests". The console follows that rule, at a larger scale:

- **Exact versions only** — no `^`, no `~`, no `latest`, in `dependencies` and `devDependencies` alike; a committed lockfile; `npm ci` (never `npm install`) in CI, with `--ignore-scripts` on the install step and an allow-list for the few packages whose postinstall is genuinely required (Electron's own binary download being the one this design expects to need).
- **Electron pinned exactly**, with the version recorded in the design's implementation notes and bumped as a deliberate, reviewed change; Electron's release cadence means a floating pin is a monthly silent change to the most privileged dependency in the repo.
- **A small dependency tree is a feature.** The renderer bundles TypeScript and no framework runtime beyond what the screens need (D9); the charts are drawn with the platform's own 2D canvas or SVG rather than pulled in.
- **The update feed is signed and verified** (§7.3), and the app refuses an update it cannot verify.
- **Reviewers get a "what changed in the tree" note**: any PR that touches the lockfile says which package moved and why.
- **The repo's own pinning regime is followed, not reinvented.** CI installs the rules emulator with `npm ci --ignore-scripts --no-audit --no-fund` (`ci.yml`'s `rules` job, over `internal/budget/rules/emulator/package-lock.json`); GitHub Actions are pinned to **full commit SHAs** with a `# vX.Y.Z` comment; and `.github/dependabot.yml` tracks **only `github-actions`**, weekly, from `/`. The console adds a second thing Dependabot should watch — and one it cannot see at all: **Electron's own binary is not an ordinary npm dependency** (its postinstall downloads a large prebuilt runtime), so the pin, the download and the checksum need the same hand-bumped, documented treatment the repo already gives the Trivy digest in `images/scan.sh` and the Dockerfile frontend digest.

## 6. The UI

### 6.1 What to take from tmog.org

tmog.org was read for this design (2026-10-15) and its own stated principles are the most useful part of it. Six transfers, concretely:

1. **"A measuring instrument should never make up an answer."** tmog.org: "TMOG distinguishes zero from unavailable and leaves gaps when samples stop. Every reading carries an explicit availability state, so a missing sensor cannot quietly become a plausible-looking value", and "A stopped measurement leaves a gap. A gap is the truth. Drawing a smooth line through missing samples would not be." **This becomes the console's first rule.** Every number on every screen carries one of: **measured**, **notional** (`NOTIONAL~`, for OAuth runs and any spend the gateway did not price), **estimated** (compute, which is an estimate by design), **unavailable** (`n/a` — never 0), or **gap** (the stream stopped; the graph breaks and the bar goes hollow). Fugaro hands us exactly these cases: compute is `n/a` when no run was estimated, `report` marks days with no final document `(partial)`, a crashed run's outstanding amount is `unreconciledMicros`, RTDB days older than 8 days are pruned, and `watch` already draws `-` for an absent registry field. The console must not smooth any of that away.
2. **Density as the default, not a setting.** tmog.org's screens are "Twelve destinations, one glance apiece". For us: a compact row (~24 px) with tabular figures in a monospaced numeral style, right-aligned numerics, no whitespace padding between related figures, and the project/repository/run hierarchy legible as *one* tree rather than three tables. tmog.org's "real screens, not simulated dashboards" is the standard: every screen must be readable at a glance and dense enough that scrolling is the exception.
3. **Live graphics that behave like instruments**: a rolling sparkline per repository (burn rate over a 5-minute window — `watch`'s own definition — because `counted` jumps by whole leases and its slope is lumpy), a stage-progress meter per run, and a cap bar that changes colour only at a defined threshold (tmog.org's amber/green discipline), never decoratively.
4. **"Replay the slow hour."** tmog.org's Flight Recorder records, saves, scrubs and replays the same telemetry panels in one interchangeable file. Ours is the same idea on the JSON feed: the console keeps a bounded in-memory ring of the last N minutes of the feed, can scrub back through it, and can export the window to a file. This is nearly free (the feed is already one JSON document per change), it is the single best debugging aid for "the run was slow at 14:20", and it doubles as the fixture format for tests (§9.2).
5. **Themes as a phosphor set, "Instrument, not ornament".** tmog.org offers Light, Dark and the classic phosphor palettes (green, amber, blue, mono) with a saturation control, and says the meters are drawn by the app so the chrome belongs to the product. Ours: a light, a dark, and a small set of low-saturation palettes, with all colour choices from a single token file — which is also what makes the screens readable for colour-blind users, a constraint tmog.org does not state and we should.
6. **One glance answers "what is responsible"**, not "here is a table of everything". tmog.org's sales line — "One glance at TMOG shows which process and which subsystem are responsible—right now" — is the console's top-level question restated: *which run, and which stage, is spending the money right now*. The default screen answers that before anything else is drawn.

**What we do not take:** the native-technology posture ("No Electron, no web view, no embedded browser"; three separate native hosts over a shared C++20 core). That is the owner's **D8**. We also do not take tmog.org's twelve equal destinations — Fugaro has four screens, and a fifth would be a concession, not a feature.

### 6.2 The screens

1. **Live** (default). Header: project, installation, CLI version, connectivity (`live` / `stale` after 45 s / `polling` in the no-Firebase mode), and the global cap bar with today's `counted`. Body: one row per repository with its cap bar, burn sparkline and kill state (a killed repository shows `KILLED by <who> <time> "<reason>"`); under each, its runs with stage, round, verify step, coder/reviewer models, spend and age, flagged `silent`/`lost`/amber. A run with `auth: oauth` shows its spend as notional.
2. **Spend.** Time series with a range picker (`--since`/`--until`), a breakdown switch (`day|week|month|repo|model|person`), three series that are **never summed** (model, `NOTIONAL~`, compute), and the caps panel: global, defaults, per-repository overrides, with the *binding scope* named for each effective per-run cap. Partial days are drawn hollow with a `(partial)` marker. The person breakdown is **off by default** (D6.4 above, §2.3).
3. **Projects.** The project-centric screen, and the one that grows as [project-centric.md](project-centric.md) ships: caps and kill switches; the enrolled repositories (`allow_repos`) and the *seen* ones; the policy object with its version, and the `routing.claude` switch, when they exist; the project jobs and profiles; the secrets and accounts inventory **by name, label and version count only**, with owners where the policy carries them. Today it renders what exists and says plainly what is not built.
4. **Run detail.** One run: its ledger, stage timeline with deadlines, models and per-stage spend, outcome, PR link, the `logs` link, and the diagnostic view (`fugaro diagnose`). This is where "replay" is most useful, so the scrubber lives here too.

### 6.3 How it degrades offline

There is no "online" mode in the ordinary sense, because the renderer never touches the network (§5.1). The console is always a client of a local process, so the degradations are:

| Condition | What the user sees |
|---|---|
| RTDB unreachable / no live events for 45 s | The `stale` banner in the header; graphs **break** rather than interpolate; the last-known values stay with their timestamps; nothing is silently zeroed |
| No Firebase configured (budget mode off, or never set up) | The `polling` indicator: `watch`'s no-Firebase mode — runs from `ls`, cost from `result.json`, **caps shown as `unavailable` and the kill/resume affordances absent** (as the CLI disables them) |
| A day older than 8 days, or a day with no final document | `(partial)`/hollow rendering; the range shows what Firestore has |
| No `fugaro` on `PATH` | A single explanatory screen: what the console needs, the install command, and a "choose the binary" affordance. No partially drawn dashboard |
| CLI older/newer than the console's supported range | A banner naming both versions and the fix, with the console running in the reduced mode it can support (**D7**) |
| CLI refuses a read (permission, wrong project) | The CLI's own message, verbatim, plus the one-line remedy the CLI prints (for `report`, the permission error names `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`; the console shows that text unedited) |

## 7. Packaging: macOS first

### 7.1 What ships, and what "alongside the CLI" means

**The repo's release rule is that one tag releases everything.** `docs/release.md` (its "Releasing Fugaro" section) states it as the single source of truth: a strict SemVer `vX.Y.Z` tag releases the binary, the base images, the Terraform modules at the tag, the schemas at the tag and the plugin. One `vX.Y.Z` push fires two independent workflows — `release.yml` (GoReleaser: the `fugaro` binaries for darwin/linux amd64+arm64, `checksums.txt`, per-archive syft SBOMs, a **keyless cosign** signature of `checksums.txt`, the GitHub Release and the Homebrew cask) and `images.yml` (build, scan, publish ghcr.io images, then `verify-public`) — and both run `scripts/release-gate.sh` first, which refuses unless the tag is on `origin/main` and the `test`, `terraform` and `rules` checks passed on it. Neither workflow runs the test suite; CI already did, on `main`.

Two consequences the console must live with, and they shape D11:

- **Today's macOS binaries are not Apple-signed or notarised.** `docs/release.md`'s "Verifying a release" section is explicit that the checksums are signed but the binaries are not, and the Homebrew cask works around Gatekeeper with a `postflight` hook that runs `xattr -dr com.apple.quarantine`. That workaround is tolerable for a `fugaro` you run in a terminal. **It is not tolerable for a GUI application** — an unsigned `.app` is quarantined and refused without a trip through System Settings, and an updater refuses an unsigned bundle. So for the console, signing and notarisation are not a nicety the CLI skipped; they are the first thing that has to exist, and they are a new capability for this repo (**D4**, Check 58).
- **The signature mechanism the repo already has is keyless.** Every existing signature is cosign over GitHub OIDC — `id-token: write`, no stored private key anywhere in the tree, scripts or a run. The only signing-adjacent secret in the whole release path is `HOMEBREW_TAP_GITHUB_TOKEN` (a fine-grained PAT for `dimipaun/homebrew-tap`), and its absence merely skips the cask with a `::warning::`. **The console's Apple signing is the first time this repo would hold a private key**, which is precisely why §7.5 puts every step that touches it in the owner's hands.

So: the console is **its own artifact, published from the CLI's own `vX.Y.Z` tag rather than from a second tag stream** (D11) — one release train, two artifacts, which is what "one tag releases everything" means once a second deliverable exists. The console declares the CLI version it was built and tested against, and **the version-skew rule is the load-bearing part of this section**: the console's JSON inputs are a contract, so it carries a **supported CLI range** (the minor it was built against, plus the previous one) and checks it at startup with `fugaro version` (the subcommand — the root command sets no cobra `Version` field, so `fugaro --version` fails). Outside the range: a banner naming both versions and a reduced mode; below a hard floor the console refuses to draw a live dashboard rather than misreport spend (**D7**).

### 7.2 macOS: signing and notarisation

1. **Certificate and identity.** A `Developer ID Application` certificate, in the owner's keychain and, for CI, in the signing machine's keychain only. The app is signed with the **hardened runtime** enabled, with the minimum entitlements: no `com.apple.security.cs.disable-library-validation` unless Electron's own signing story requires it (it must be justified in the entitlements file's comment if it is), no `get-task-allow` outside debug builds, and no entitlement the app does not demonstrably use.
2. **Notarisation.** `xcrun notarytool submit --wait` with an App Store Connect API key (or an app-specific password) held as a CI secret, then `xcrun stapler staple` on the `.app` and on the `.dmg`, then verification: `codesign --verify --deep --strict`, `spctl --assess --type execute`, and `stapler validate`. **These verification commands are the fact the release records**, the same way the CLI's release records its checksums.
3. **Gatekeeper's real constraint.** An unsigned or unstapled build does not run on a normal Mac without the user disabling Gatekeeper. That makes signing a *release blocker*, not a polish item: an unsigned console is not shippable to anyone but the owner.
4. **Distribution.** A direct download (`.dmg`/`.zip`) from the GitHub release, plus a Homebrew cask mirroring the CLI's existing cask. **The Mac App Store is not the path** (**D4**): sandboxing an app whose job is to spawn another installed executable fights the platform, and it would put the console behind an App Review process the CLI does not have.

### 7.3 Auto-update

Electron's Squirrel.Mac update path, over a **signed** feed: `latest-mac.yml` and the archives published on the release, so the OS verifies the new bundle's signature before it is installed (an update the app cannot verify is refused). Cadence: check on launch and then on a long interval; never silently install without telling the user; never downgrade; and the whole feature has a visible off switch.

**This would be the repo's first self-updater, and the CLI's existing machinery is deliberately not one.** `fugaro` never replaces its own binary: `docs/design/upgrade.md` and the `upgrade` skill make the sequence `brew upgrade dimipaun/tap/fugaro && fugaro upgrade`, where the package manager does the replacing and `fugaro upgrade` does the pinning, the plugin update and the image refresh. What the CLI *does* have is a **notice**: `internal/updatecheck/updatecheck.go` does one background `GET` a day to the GitHub `releases/latest` endpoint, caches under `$XDG_CACHE_HOME/fugaro`, and prints at most one nag a day (`WarnTTL`, `CheckTTL`, `FetchTimeout`), disabled by `FUGARO_NO_UPDATE_CHECK=1`, by `update_check: false` in the local config, and automatically in CI, under `exec`/`help`/`version`, on a non-terminal stderr and on Cloud Run; the fix line is the constant `updatecheck.BrewCommand`. So the design says: **the console's update check should read the same release metadata and honour the same opt-out**, and the *installation* of the new bundle is the one genuinely new mechanism — which is why it is `D5` (recommended: **on by default, with the toggle visible**) and why Check 58 exercises the round trip on a real Mac.

### 7.4 "Your machine stays your business"

tmog.org's promise — no usage statistics, no hardware inventory, no process information, no identifiers, "its only routine request is an update check" — is the right default here for a stronger reason than taste: the console displays a team's work, spend and repository names. **No telemetry, no crash reporting to a third party, no analytics, and no update check that carries an identifier.** The only outbound requests are the CLI's own (to the cloud the operator already owns) and the update check.

### 7.5 Which steps are owner-run, and why no agent does them

Signing and notarisation **cannot** be done by an agent, on this repo's existing terms and for a concrete reason: the certificate, the App Store Connect key and the notarisation credentials are secrets, and the rules under which this work is done are that no agent handles a secret and that releases are owner-gated. So:

| Step | Who |
|---|---|
| Writing the entitlements file, the builder configuration, the update-feed metadata, and the CI workflow's **text** | an agent may write the file, and **the workflow only as a staged change under `docs/pending-workflows/` for the owner to copy** — the repo already does exactly this (its README is the only thing in that directory today), and `docs/release.md`'s "Before you tag" names copying a staged workflow in as an owner step |
| Creating the certificate, storing it, storing the API key as a CI secret | **owner only** |
| The first signed, notarised, stapled build and its verification | **owner only** (Check 58) |
| Cutting the tag that publishes it | **owner only**, as with the CLI |
| The auto-update round trip on a real Mac | **owner only** (Check 58) |
| Windows and Linux signing | **owner only**, later (**D10**) |

## 8. Where it lives, and its interaction with the Go module and CI

### 8.1 Recommendation: this repo, under `console/` (**D3**)

Arguments for keeping it here, in order of weight:

1. **The contract lives here.** The JSON shapes the console consumes are `internal/watch/json.go`'s types and the CLI's `--json` output. A separate repository makes that contract cross-repo, which means it needs a published schema, a compatibility policy and its own versioning — all of which this repo already has, in the form of the JSON tests it already runs (`internal/cli/json_everywhere_test.go`).
2. **The rules the console must obey are enforced here.** The docs tests (`internal/cli/docs_*_test.go`), the release process and the owner-gated release rule are in this repo. A console in a second repo would have to be *told* about them.
3. **Version skew is easier to manage in one place.** The console's supported-CLI-range check (§7.1) is a fact about this repo's releases; keeping the two version streams in one git history makes the mapping checkable, not aspirational.
4. **The Go module is untouched.** `console/` contains no Go package, so `go build ./...`, `go vet ./...` and `gofmt -l .` take no notice of it; `go.mod` and `go.sum` do not change; no Go package may import anything from `console/`, and nothing under `console/` may be imported by the module. That is stated as a rule, not a hope: a Go test asserts it (§9.5).

The honest cost, which the design states rather than argues away: **the console brings a Node toolchain, a TypeScript build and by far the largest dependency tree in the repository.** There is precedent but no scale: the repo already has three small npm projects — `internal/budget/rules/emulator/` (the pinned `firebase-tools`, installed in CI with `npm ci --ignore-scripts`), `images/base/harnesses/` (the agent harnesses baked into the base image) and `deploy/sandbox/` (a zero-dependency sandbox helper) — and none of them has a runtime, a bundler or a lockfile measured in megabytes. The console has all three, plus CI time. Both are mitigations, not eliminations: a path-filtered workflow so Go and console jobs do not run each other (§9.4), exact pins and a lockfile (§5.2), and `npm ci --ignore-scripts`.

A separate repository (**D3**'s alternative) is defensible if the owner wants the CLI's CI and supply chain untouched, and the design notes what it costs: a cross-repo contract, two release processes, and the console's rules restated somewhere they are not enforced.

### 8.2 Shape inside the repo

```
console/
  package.json, package-lock.json      # exact pins, private, engines.node
  tsconfig.json
  src/main/                            # the Electron main process: window, IPC allow-list, spawn
  src/preload/                         # the frozen, versioned IPC object
  src/renderer/                        # the four screens; no network, no node
  src/source/                          # FugaroSource: cli (v1) | fixture (tests); Option C slots in here
  src/shared/                          # schema types, availability states, sanitization
  test/unit/                           # schema, availability, sanitization, spawn allow-list
  test/e2e/                            # Playwright + Electron, against fixtures
  test/fixtures/                       # recorded, redacted NDJSON feeds (never from belong)
  tools/record-fixture/                # the recorder/redactor (dev-only; the CLI gains nothing)
```

**Nothing here is created by this document.** The structure is the plan's, and the plan's first task is the one that decides it against the real code.

### 8.3 The interface that makes Option C a drop-in

`FugaroSource` is the only thing the screens may talk to: it yields `JSONDoc`-shaped snapshots and emits change events, and it knows nothing about how they were produced. v1's implementation spawns the CLI. A fixture implementation replays recorded NDJSON. Option C's implementation will speak HTTP to a loopback port. **This one interface is why the auth decision (§3) is cheap to revisit**, and it is why the tests need no cloud.

## 9. Testing without a live cloud

The repo's existing discipline is the template: `watch`'s golden frames via `teatest`, the in-process RTDB fake `internal/gcpfake`, the Firebase RTDB **emulator** behind the `firebase` build tag with a pinned `firebase-tools` (`internal/budget/rules/emulator/package.json`), and Terraform plans against a mock provider. The console follows it, with one addition — Playwright driving the real Electron binary.

### 9.1 The fake data source is a recording, not an invention

The primary test double is a **recorded `fugaro watch --json` transcript**, replayed through the fixture `FugaroSource`. A hand-written fixture would only prove the app handles the shapes its author imagined; a recording proves it handles the shapes the CLI emits.

- **Where recordings come from.** The sandbox installation and the sandbox repository, never `belong` — the same rule every live check in this repo carries. The recorder (`console/tools/record-fixture/`) spawns the CLI, captures the NDJSON, and **redacts**: task titles clip and redact in `watch` already (`title` is "the task's first line, clipped to 80 characters and redacted", `m9-budget-and-dashboard.md` §6.2), and the recorder applies the same treatment to anything else repository-specific (repository names, PR URLs, requester addresses). A fixture that fails the redaction check is not committed.
- **What a fixture must contain to be worth having**: a normal run; a run that goes `silent` then `lost`; a kill switch flipping mid-feed; a repository at its cap; a run with `auth: oauth` (notional spend); an absent field (`round`/`spent` before the run reports them); a **gap** in the feed long enough to trip the 45 s stale banner; and a **hostile title** — the `watch` render tests already carry hostile-title cases, and the console's must too.
- **The recorder is console-side.** It adds no CLI command, no flag and no backend: the CLI gains nothing from this design.

### 9.2 Unit tests (no Electron)

The schema types, the availability-state rendering rules, the sanitization re-application, the burn-rate computation (checked against `watch`'s own 5-minute rolling window so the two cannot drift), the version-skew range check, and **the spawn allow-list** (a test that iterates the allow-list and asserts every entry is read-only, and that a table of write subcommands is *not* reachable — the mutation-proof form, so adding `budget kill` to the list fails a test rather than passing review by accident).

### 9.3 Playwright + Electron end-to-end

Playwright drives the packaged app (`electron.launch`) with the fixture source, and asserts:

- **Behaviour**: the Live screen renders the fixture's runs with the right flags; the Spend screen keeps model, `NOTIONAL~` and compute separate; a `(partial)` day is hollow; the Projects screen shows what is not built as not built.
- **Screenshot goldens**, in the `teatest` golden-frame tradition: one per screen per theme, with a documented, deliberate update command. Golden images catch the density and layout regressions that DOM assertions cannot.
- **The security assertions, which are the ones that must not be allowed to rot** (§5.1): a remote navigation is refused; a `window.open` is refused and does not open an internal window; **any renderer-initiated network request fails** — enforced both by the CSP (`connect-src 'none'`) and by a Playwright route handler that fails every request, so a CSP regression is caught too; a hostile title renders as text; and the IPC surface rejects an unknown channel name and a malformed argument.

### 9.4 How this runs in CI

- **Two runners**: `macos-latest` (the platform we ship first, and the only one where the app's real packaging works) and `ubuntu-latest` with `xvfb-run` for the fast Linux pass. Windows joins when D10 says so.
- **The Node toolchain is installed only for the console jobs**: `actions/setup-node` with the version `console/package.json`'s `engines` names, `npm ci --ignore-scripts`, then the console's own test command. This mirrors how the rules tests gate the Firebase emulator behind a `firebase` build tag rather than making every job pay for it.
- **Path filters so the two halves do not run each other**: the console job runs on changes under `console/**`; the Go jobs run on everything else. A change to `console/` must not slow the Go suite, and a change to `internal/` must not rebuild Electron.
- **A workflow file is described, never written.** This repo refuses to let an agent create or edit anything under `.github/workflows/` (GitHub will not accept Fugaro's push of a workflow change at all, so a run that edits one cannot be delivered). So the console workflow's text is written **in the plan and by the owner**, as `docs/pending-workflows/README.md` already does for another staged workflow. The plan's CI task says this explicitly.
- **No cloud credentials in these jobs.** Not one: they run against fixtures.

### 9.5 The Go side of the contract

Two Go tests hold the boundary in place, both in the `docs_*_test.go` convention:

1. **The docs test.** `docs/design/desktop-console.md` and this plan are prose, so a Go test asserts **presence** of the design's load-bearing strings (the auth decision, the allow-list, the security posture's rules, the availability states, the owner-run table reducible to Check 58), in the same spirit as `TestDesignCarriesThePlanAmendments` in the project-centric plan. It is a presence test, not a fidelity test — a reviewer reads the diff for fidelity.
2. **The boundary test.** A test asserting that the Go module and `console/` do not touch: no Go file imports or references a path under `console/`, and `go.mod` gains no dependency from this work. This is what keeps the "the Go module is untouched" claim true a year later.

## 10. Decisions for the owner

Each with this design's recommendation. The namespaces are local: **D1 to D12 below are this console design's**, and are unrelated to project-centric's D1a to D12.

| # | Question | Recommendation |
|---|---|---|
| **D1** | How the console reaches the data: the CLI as a sidecar (A), a local read-only API on loopback served by the CLI (C), or direct RTDB/Firestore with the operator's credentials (B)? | **A for v1, behind the `FugaroSource` interface so C is a drop-in later; B never.** A is the only option that reuses the readers exactly and holds no credential; C is the right evolution once spawning is the bottleneck; B puts a cloud token inside the process that renders hostile agent text. |
| **D2** | Is v1 strictly read-only, with kill/resume/cancel deferred? | **Yes, strictly read-only.** The three commands are shown copyable. When they arrive (v1.1), the app invokes the CLI's own command so the typed confirmation, the admin check and the ETag compare-and-set stay in the CLI. |
| **D3** | Does the console live in this repo under `console/`, or in its own repository? | **This repo, under `console/`** — one contract, one release rule set, one version-skew mapping, and the Go module provably untouched (two tests assert it). The cost is a Node toolchain and CI time here, mitigated by path filters and exact pins. |
| **D4** | macOS distribution: direct download plus a Homebrew cask, or the Mac App Store? And is the owner willing to introduce **Apple signing into this repo's release path**, which today has none (the CLI's checksums are keyless-cosign signed, its binaries are not Apple-signed, and the cask papers over Gatekeeper with an `xattr -dr com.apple.quarantine` hook)? | **Direct download plus a cask**, and signing is a prerequisite, not an option: an unsigned `.app` cannot be shipped and cannot be updated. Accept that this repo then holds its first private key (§7.5 keeps it in the owner's hands). The App Store is rejected: its sandbox fights an app whose purpose is to spawn an installed executable, and it adds an App Review the CLI does not have. |
| **D5** | Auto-update: on by default, or off? | **On by default, with a visible off switch**, and no telemetry of any kind (tmog.org's own posture). The CLI already notifies about new versions; the console should read the same release metadata rather than invent a second mechanism. |
| **D6** | Test fixtures: recorded from the sandbox and redacted, or hand-written? | **Recorded and redacted**, from the sandbox installation and repository only, never `belong`; a fixture that fails the redaction check is not committed. Hand-written fixtures cannot prove the app handles the shapes the CLI really emits. |
| **D7** | CLI version skew: warn and degrade, or refuse? | **Warn and run in a reduced mode above a hard floor; refuse below it.** A dashboard that misreports spend because it read a shape it did not understand is worse than one that says it cannot read it. |
| **D8** | tmog.org fidelity: borrow the information design, or its native-technology posture too? | **Borrow the information design only** (availability states, gaps, density, the replay buffer, the theme set, "instrument not ornament"). The native posture means three shells over a shared core and is a different, much larger design; note that tmog.org itself markets against Electron. |
| **D9** | Renderer stack: TypeScript with no framework (platform canvas/SVG for charts), or a framework plus a charting library? | **TypeScript, no framework runtime, charts on the platform.** Keeps the dependency tree — the console's largest security surface — small, and makes `connect-src 'none'` trivially true. |
| **D10** | Windows and Linux: package them now, or later? | **Later.** Keep the code cross-platform and run the Linux CI pass so it does not rot, but sign and distribute macOS first; two more signing identities is owner work with no v1 user. |
| **D11** | Does the console ship inside the CLI's release or as its own artifact, and from which tag? | **Its own artifact, published from the CLI's own `vX.Y.Z` tag** — one release train, one gate (`scripts/release-gate.sh`), one Highlights section, which is what "one tag releases everything" means once a second deliverable exists. It carries a declared supported CLI range, and the version-skew rule of §7.1 enforces it. |
| **D12** | The owner-run live check: what is it, and what does it assert? | **One check, Check 58** (see the plan): the first signed, notarised and stapled build, the Gatekeeper verification, the auto-update round trip on a real Mac, and — the safety assertion — that the console is pointed at the sandbox installation `fugaro` and can reach nothing in `belong`. |

## 11. What is out of this design's remit

- **Any CLI change.** The console needs no new flag and no new command in v1. If a screen needs a field no `--json` emits, that is a CLI task in a CLI plan.
- **Any change to the RTDB rules or the Firebase IAM.** The console reads what launchers and operators already read.
- **Any change to `.github/workflows/`.** Described for the owner, never written by an agent.
- **The `belong` installation.** Never touched, by any step, agent-run or owner-run.

## 12. UNVERIFIED, to settle in the plan's first tasks

1. **The exact field names of every JSON document the console consumes.** The authoritative reader is `internal/watch/json.go` (`JSONDoc`, `JSONTotal`, `JSONRepo`, `JSONRun`, `JSONKill`, `JSONBurn`, `JSONConn`, `JSONFinished`, `BuildJSON`), and it is verified. The others are verified only as far as their doc symbols go: `budget show`'s `budgetShowDoc` (`{project, gcp_project, mode, day, max_reserve_usd, global, defaults, repos, running, warnings}`), `report`'s `report.JSON`, `runs ls`'s `lsDoc` (`{project, runs, totals, warnings}`), `cancel`'s `cancelResult`, `logs`'s `logLine`. **Task 1.1 reads each one and pins it as the console's schema**, which is the point at which this stops being an assumption.
2. **What `watch --json` guarantees to a long-lived child process.** The emission side is verified: one compact JSON document per state change, `json.NewEncoder(d.Out).Encode`, gated on `ready && dirty && since >= gap` with a 1-second gap, and always unfiltered. **Not verified**: backpressure and bounded memory when the parent is slow to read; what the child does when the console dies; whether it exits on a closed stdout or lingers; and the restart behaviour after `connRefused`. Task 1.2 establishes each, because they decide whether Option A needs a supervisor.
3. **`fugaro doctor --json` and `fugaro version`'s machine-readable form** for the handshake of §7.1. `fugaro version` is a subcommand (`internal/cli/root.go`) and the root command sets no cobra `Version` field, so `fugaro --version` fails; whether `version` or `doctor` has a `--json` form is not verified. If neither does, the handshake parses `version`'s human line, which is a fact the plan must pin with a test.
4. **Electron's current signing requirements on macOS** — specifically whether the hardened runtime needs `com.apple.security.cs.disable-library-validation` for the pinned Electron version. Check 58 records the fact; the entitlements file must justify whatever it needs.
5. **Whether tmog.org's screens can be reproduced or referenced** — the six transfers in §6.1 come from the site's own statements (read 2026-10-15), not from an inspection of its rendered meters. If the owner wants closer fidelity, that is a design task of its own.
6. **The size and cold-start of an Electron app** with the dependency tree D9 implies. If it is unacceptable, the choice is between a smaller runtime and D8.
7. **How a recorded fixture's redaction is verified** beyond a reviewer's eye — a testable redactor is a task, not an assumption.
