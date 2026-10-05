# Fugaro M9 — Budget guardrails, live dashboard and spend history

*Status: design, all decisions settled 2026-09-30 (§16). Not an implementation plan. Source spec: [m9-spec-source.md](m9-spec-source.md) (v2); the user's updated v3 is [m9-spec-source-v3.md](m9-spec-source-v3.md), reconciled in [m9-spec-v3-reconciliation.md](m9-spec-v3-reconciliation.md) (2026-10-01). Base design: [v1.md](v1.md) (sections cited as §n).*

This document maps the source spec onto Fugaro as it stands after M6. The user has made these decisions, and they bind the design:

1. **Firebase holds the state.** Live state (budget counters, the agent registry, kill switches) goes in the **Realtime Database (RTDB)**. History (daily spend records) goes in **Firestore**.
2. **A model gateway in the container enforces spend on every model call.** It reserves the worst-case cost first, then makes the call, then reconciles to the actual cost. It covers **API-key and Vertex** auth only, so dollar caps are enforced only where billing is real. `oauth` is never routed through a proxy (§5.8).
3. **The redundant (N-attempt) mode is out of scope.**
4. **M6 is done.** [v1.md](v1.md) is the base design.
5. **The settled decisions D1–D15 in §16:**
   - **Budget writes.** Jobs write directly to RTDB with per-run Firebase tokens that the launcher mints, and database rules bound what each token can do. There is no budget service.
   - **Caps are a guardrail for now.** Making them hold against a compromised agent is deferred.
   - **Firebase regions.** RTDB goes in `us-central1`. Firestore goes in `us-east5` (M9d built no `nam5` fallback: if `us-east5` is refused the step fails and a different location is a deliberate decision, because a database location can never change).
   - **The budget day is UTC**, and caps count model dollars only.

---

## Terminology

- **Fugaro project** (the word users see; for example *Aurora*). A semantic project made of many repositories, worked on by several people. It is:
  - one GCP project, plus one Firebase project (FP);
  - its repositories;
  - its people lists (`launchers`, `operators`, `budget_admins`);
  - one local project config (§2.4).

  A different project (say *Borealis*) has its own GCP project (or projects), its own FP, its own config and its own people. Nothing is shared between the two.
- **Installation.** The technical name for a project's cloud-side setup: what `fugaro init` creates, and the name M5's docs and the Terraform root `installation` use. It appears in this document only in that sense.
- **Repository.** A member of a project, for example `aurora-server`, `aurora-android`, `aurora-ios` and `aurora-web`, on Bitbucket or GitHub. It is what the source spec calls `project-a` or `project-b`, and it is the unit of budget caps and kill switches below the project.
- **Global** (in the spec and in cap names) means **the Fugaro project**. There is no live state spanning projects.
- **One word, one meaning** (D17). *Project* always means the Fugaro project: in `fugaro.yaml` (`project:`), on the CLI (`--project <name>`), in the environment (`FUGARO_PROJECT=<name>`), in the local config (`projects/<name>.yaml`, `name:`) and in the docs. The GCP project is always written in full, as `--gcp-project`, `gcp_project:` and `FUGARO_GCP_PROJECT`. There is no separate "profile" concept.

**Example.**

| Project | Budget controls |
|---|---|
| **Aurora** | One daily cap for the whole project (`global.dailyUsd`), a cap per repository (`repos/<slug>.dailyUsd`) and per run, one project kill switch (`kill/global`), and one kill switch per repository (`kill/repos/<slug>`) |
| **Borealis** | Its own caps, switches, FP, dashboard and history. Aurora's people and tools never touch it unless they select project Borealis and hold rights there |

## 0. Summary

- **Budget scope.** A *project* in the spec's sense is one Fugaro **repository**. Caps nest: repository inside global, and each level has a per-day and a per-run cap. There is a global kill switch and one per repository. The day is UTC. "Global" is the Fugaro project: each project has its own dedicated Firebase project (D3, §6.0).
- **The gateway runs inside the runner** (`fugaro exec`, part of the base image's binary) and listens on `127.0.0.1`.
  - Claude Code reaches it through `ANTHROPIC_BASE_URL` (API key) or `ANTHROPIC_VERTEX_BASE_URL` with `CLAUDE_CODE_SKIP_VERTEX_AUTH=1` (Vertex).
  - The gateway holds the real credential; the agent's environment no longer does.
  - It rejects any model the stage didn't pin.
- **Reservation happens at two levels.**
  - Each call reserves its worst-case cost against a **local lease**, exactly and in-process.
  - Leases are reserved against the RTDB counters, in one atomic multi-path write per lease.
  - A call never waits on Firebase unless its lease is exhausted.
- **Per-run Firebase identity.**
  - `fugaro run` (the launcher) mints a Firebase **custom token** for the run by signing it through IAM `signJwt` as a signer service account that holds no roles.
  - A job can never mint its own token: no job account holds `signJwt`.
  - Database rules tie every change to a shared counter to an equal change of the caller's own run ledger, bounded per write and by the caps. §6.4 shows the rules and the attack cases they deny.
- **The worst case for a compromised run token** is that it pushes its repository's and the global day counters up by at most its per-run cap, in steps no larger than the per-write bound. It cannot lower anyone else's counters, cannot exceed a daily cap, cannot touch caps or kill switches, and does nothing once the token has expired.
- **Counters are integer micro-dollars in dated day nodes**, so no rollover race is possible.
- **Kill switches reach jobs through an RTDB event-stream listener**, within seconds.
- **Halted runs.** `halted` is a new run status. It is additive to the `result.json` schema. A halted run gets a draft PR with a halted report, or no PR at all if it stopped before its branch existed (exit 0).
- **`fugaro watch`** is a Bubble Tea TUI fed by RTDB's event stream. **History** is written by an end-of-day **history job** with its own small image. That job also sweeps stale registry entries against Cloud Run executions. **`fugaro report`** derives weeks, months and years on read.
- **M9 is opt-in.** With no `budget:` block, behaviour is exactly as it is today. An `observe` mode calibrates caps before `enforce` turns them on.
- **Slices.**
  - **M9a:** gateway, pinned models, per-run cap in-process, and `halted`, with no Firebase.
  - **M9b:** Firebase counters, daily caps, kill switches and `fugaro budget`.
  - **M9c:** `fugaro watch`.
  - **M9d (built):** Firestore history and `fugaro report`. The live bring-up is check 23 in [gcp-live-checklist.md](../gcp-live-checklist.md); until it is run, the facts it lists are assumptions.
  - **M9e:** draft PR at the first push (D15, revised 2026-10-01).
  - **M9f (optional):** changes to the task loop (the verify gate and structured findings).
  - **M10 (later, own design):** multi-model work from spec v3.

**The residual risk (D2, accepted).** The container is not a trust boundary against its own agent (§6.1). A prompt-injected agent can read the real credential from the runner's `/proc`, or on Vertex use the metadata server's token, and call the model **around** the gateway. The caps bound a runaway or honest agent absolutely. A compromised one is bounded only by provider-side limits. §11 describes the later hardening: an external gateway.

---

## 1. Goals and non-goals

**Goals**

- **Hard caps on model spend,** per run and per day, for each repository and globally. Caps are enforced before each call and hold across any number of concurrent runs.
- **Kill switches:** a global one and one per repository, each effective within seconds.
- **Fail closed:** after a 3-minute grace without the budget backend, runs halt, whatever their auth mode (D14).
- **Pinned models** for every stage. The gateway refuses any unpinned or unpriced model.
- **A live terminal view** of spend, caps, burn rate and running agents, grouped by repository.
- **A durable daily history,** with weekly, monthly and yearly figures derived on read.
- **One consistent account of cost** across `result.json`, `ls`, the PR report, the live counters and the history.

**Non-goals (M9)**

- **Deferred parallelism controls.** The redundant mode and per-batch concurrency are out. `max_parallel` stays.
- **Non-Claude models** (D10). Anthropic's gateway guide says it doesn't support Claude Code routed to non-Claude models.
- **Per-round PR comments** (D15). The draft PR opens at the first push, in M9e, not in M9a-d.
- **A web dashboard.**
- **Dollar caps, or any proxy, for `oauth` runs.**
- **Capping compute cost** (D7).
- **Caps that hold against a compromised agent in the container** (D2: later).

---

## 2. How the spec maps onto Fugaro

| Spec concept | Fugaro today | M9 |
|---|---|---|
| `project` | Nothing. A repository (slug §3.2) is the unit of jobs, IAM and state | **A repository**, keyed by its slug. Grouping repositories is a later `group` field on caps |
| `taskId` | Run ID `YYYYMMDD-HHMMSS-<hex>` | The run ID. Nodes are keyed `<slug>/<run-id>` |
| `coder` / `reviewer` | One `agent.model` for every stage | `agent.models.coder` (implement and fix) and `agent.models.reviewer` (review) (§2.1) |
| `provider` | Implied by `agent.auth` | Unchanged: the auth mode is the provider. Claude models only (D10) |
| `maxTokensPerCall` | None | `agent.max_output_tokens` per role, passed as `CLAUDE_CODE_MAX_OUTPUT_TOKENS`. The gateway refuses any request above it |
| Pinned IDs and a price table | Claude Code's `total_cost_usd` | An embedded price table plus owner overrides. Unpinned or unpriced models are rejected (§5.2) |
| `maxReviewRounds` | `agent.review_rounds` | Unchanged |
| Deterministic compile, test and lint | The agent runs `fugaro verify`, and §4.2 gates readiness on it | Kept. An optional verify gate before review is M9f |
| APPROVE / REJECT | `ship` / `changes` | Fugaro's names are kept |
| Draft PR from the start | PR opened at finalize | **A draft PR after the first verified push, from M9e** (D15, revised). Until then finalize-time PRs |
| A structured comment per round | Findings go to the fix prompt and the final report | **The spec's structured format goes into the final report**, one section per round (D15) |
| HALTED | None | New status `halted` (§5.9) |
| RTDB `budget` / `runs` / `agents` | GCS only | Caps, kill switches, dated counters, run ledgers and the registry (§6.2) |
| `onDisconnect` | n/a | Not available over REST. Heartbeats plus a sweeper, cross-checked with Cloud Run executions (D12) |
| Firestore `spendDaily` | None | As the spec describes (§9) |

### 2.1 Per-stage models: the minimal config change

The repository's `fugaro.yaml`, which its writers control, says *which* models to use. It can never *loosen* how much may be spent: caps and prices belong to the owner. Otherwise a repository could lift its own limit. (M9a.1 refines this: `fugaro.yaml` on the default branch may *tighten* the owner's ceiling with a `budget:` block, and a run's branch may tighten further; the effective value is the tightest of the three layers. Prices stay owner-only. See v1.md §5.1.)

```yaml
agent:
  auth: api-key                  # vertex | api-key | oauth (unchanged)
  model: claude-sonnet-5-5       # existing; the default for every role
  models:                        # new, optional; each falls back to agent.model
    coder: claude-sonnet-5-5     # implement and fix
    reviewer: claude-opus-5-5    # review
    background: <pinned-small-model>   # Claude Code's background requests
  max_output_tokens: { coder: 16000, reviewer: 8000 }   # new, optional, per call
  max_run_tokens: 0              # new, optional; the token cap for oauth runs (§5.8)
  review_rounds: 3
  max_budget_usd: 25             # unchanged: per stage, passed as claude --max-budget-usd
```

- **When the budget is on, every role must name an explicit model ID.** `validate` and the runner refuse any role that doesn't. They also refuse aliases such as `sonnet` or `opus`, and IDs that aren't in the effective price table. With the budget off, today's behaviour is unchanged.
- **The runner pins everything Claude Code might pick.** For each stage it passes `--model <role>` and sets:
  - `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` to the role's model;
  - `ANTHROPIC_DEFAULT_HAIKU_MODEL` to `background`.

  A `model: opus` in a repository's `.claude/agents/*.md` therefore still resolves to a pinned ID. These variables are already reserved (§5.1), so no workflow secret can override them.
- **The `model` override in `task.json`** means the coder model. It must be in the price table.

### 2.2 Why a gateway rather than Claude Code's cost figure

Claude Code reports `total_cost_usd` only when a stage ends, and `--max-budget-usd` applies to one process. Neither can refuse a call because of spend elsewhere in the fleet. The gateway sees every request and its streamed usage. Claude Code's figure becomes a cross-check: the runner warns when the two differ by more than 5% in a stage.

### 2.3 Lifecycle: what M9 adopts from the spec

| Spec rule | M9 |
|---|---|
| Open the PR as a draft at the start | **Adopted in M9e, at the first push** (D15, revised 2026-10-01); not at bootstrap. The original objection (D15, superseded): an early PR needs an empty commit and a push at bootstrap, it would leave empty drafts behind when a run crashes, and it reworks finalize and the M6 follow-up paths that were checked live. `fugaro watch` gives the live view instead. Opening after the first verified push avoids the empty commit and the empty drafts of early crashes; the sweeper marks stale drafts |
| A structured findings comment per round | **The format is adopted, in the final report**: one section per round, with `<!-- fugaro:findings {...} -->`, and the findings go into `result.json` too |
| The reviewer never sees code that doesn't compile | Optional M9f: before review, check for a passing, clean `verify test` on HEAD. Without one, run a fix stage that doesn't count as a review round, capped by `agent.verify_retries` (default 3) |
| Give up after the last round: FAILED, draft PR | Already the case (§4.2) |
| HALTED: draft PR with a halted comment | Adopted (§5.9) |

---

### 2.4 Working across Fugaro projects (a prerequisite, task 0)

**Today one local config holds one project.** `--config` and `FUGARO_CONFIG` pick another file, but nothing says which project a command is acting on. Once each project has its own kill switches and caps, that is a safety problem: a launch or a kill must never hit the wrong project.

- **The canonical name** (D16). `fugaro init --name <slug>` sets the project's name once, as a slug of 1 to 40 characters, lower case, `[a-z0-9-]`, starting and ending with a letter or digit.
  - It is stored in the cloud setup:
    - the installation's Terraform output `project_name`;
    - the label `fugaro_project=<slug>` on the runs bucket;
    - the job environment variable `FUGARO_PROJECT` on every workflow and check job (D17: renamed from the GCP ID, which moves to `FUGARO_GCP_PROJECT`, §2.6);
    - `/fugaro/project` in the project's RTDB, and a `project` field on every Firestore document.
  - It is **immutable**:
    - init refuses to change it: a different `--name`, or a bucket label that disagrees with the state;
    - discovery refuses a runs bucket labelled for another project.
- **Each repository names its project.** A top-level `project: <slug>` goes in `fugaro.yaml` (§2.5).
- **Project configs.** Each project's local config is `$XDG_CONFIG_HOME/fugaro/projects/<name>.yaml`: the local config of §5.4, with `gcp_project:` (renamed from `project:`, D17) and `name: <slug>`.
  - `fugaro init` and `fugaro init --config-only` write it, and copy the canonical name from the outputs. Each team member runs `--config-only` against the same GCP project, so everyone gets **the same name**.
  - A project config whose `name:` differs from the cloud's `project_name` (checked on the first cloud call and cached for a day) is refused: "project config aurora points at a GCP project whose Fugaro project is borealis".
- **How a command picks its project,** first match wins:
  1. `--config <file>` (unchanged);
  2. **inside a checkout, the `project:` of its `fugaro.yaml`**, which must have a project config. **A checkout naming a project with no config is refused, never guessed:** "this checkout belongs to project aurora; there is no project config for aurora (run `fugaro init --config-only --gcp-project <id>`)". A `--project` or `FUGARO_PROJECT` naming a different project is refused too;
  3. outside a checkout, `--project <name>`, else `FUGARO_PROJECT`;
  4. exactly one project config exists: that one;
  5. no project config at all: refuse (exit 1) with "no project config; see `fugaro init --config-only`". The old single `config.yaml` isn't read (D18).
- **No `fugaro use`, no `--profile`, no `FUGARO_PROFILE`** (D16, D17). Remembered state that silently points later commands at a project is the failure we're preventing. Inside a checkout the repository decides. Outside one, `--project` or `FUGARO_PROJECT` is explicit, and a shell alias or `direnv` covers habitual use.
- **Refusing ambiguity.** Outside a checkout, with several project configs and none named, commands refuse (exit 1) and list the projects.
- **You always see the project.**
  - Every cloud command prints `project: aurora (GCP <id>)` as its first line on stderr, and its `--json` output gains `project`.
  - The watch header, `budget show` and `report` show it too.
  - `budget kill --all` asks you to type the canonical project name.
- **Later:** `fugaro ls --all-projects` (read-only, one section per project), and the organization-wide read-only roll-up (§18).
- **Several people per project.**
  - Membership is the project's lists: `launchers` and `operators` (applied by `fugaro init`), and `budget_admins` together with the GCP project's owners and editors (D6).
  - Every person has their own identity in the records. `requested_by` (task.json) is also minted into the run token as the claim `rb`, taken from the launcher's own credential. The registry and outcomes must carry the same value (a rules check), so attribution can't be forged by the run.
  - Kill and cap changes record `by`.
  - `fugaro report --by person` and `watch` show who launched what, and `spendDaily` gains `byPerson`.

### 2.5 Each repository names its project (D16)

```yaml
version: 1
project: aurora        # new, top level: the Fugaro project this repository belongs to; immutable
git: …
```

- **This isn't a cloud resource path or a GCP project ID,** which is why it doesn't conflict with v1 §5.1's rule. It is the logical name that `fugaro init --name` set.
- **Enforcement.**
  - **`fugaro init --repo` and `fugaro validate`** require it, and `init --repo` refuses when it differs from the installation's `project_name`. The error: "fugaro.yaml has no `project:`; add `project: aurora`".
  - **The schema, the config loader, `fugaro config example` and the `/fugaro:setup` skill** all learn it. The skill takes the name from the selected project config, and asks when there isn't one. It never invents a name.
- **The runner checks it at bootstrap** (step 4, before the lock and before any remote change). Every job carries `FUGARO_PROJECT` (the name) and `FUGARO_GCP_PROJECT` (§2.6). A job missing either fails at bootstrap with "job environment lacks FUGARO_PROJECT; run fugaro init --repo". The runner reads `project:` from the **base branch's** `fugaro.yaml` (`git show origin/<base>:fugaro.yaml`; for a follow-up, that's the configuration it reads anyway). For a first run at another ref, it also checks the ref's `fugaro.yaml`. It refuses when the key is missing or different:
  - status `infra_error`, outcome `none`, exit 2;
  - reason: "project mismatch: fugaro.yaml on main names project borealis; this job belongs to project aurora" (or "… names no project").

  This catches repositories that were copied, forked, mis-onboarded, or pointed at the wrong project's jobs.
- **In the budget data** (§6.2):
  - `/fugaro/project` holds the name, and the launcher mints it into each run token as the claim `fp`. The rules require `auth.token.fp == root.child('fugaro/project').val()` on every write. That's a defensive check: the FP is already per project, so a token from another project's signer isn't valid there anyway.
  - Firestore documents carry `project`.
  - `report`, `watch` and `budget` show it.
  - `budget kill --all` asks you to type it.
- **Renaming isn't supported in M9.** The name lives in the bucket label, the Terraform outputs, every job's environment, RTDB, Firestore history, every repository's `fugaro.yaml` and every person's project config. A rename would be a deliberate operation of several steps, deferred:
  1. add an alias to the installation;
  2. merge the new `project:` into every repository;
  3. re-run init for the installation and every repository;
  4. rewrite the Firestore `project` fields;
  5. every member re-runs `--config-only`, which renames their `projects/<name>.yaml`.

  Until then init refuses a change, and a new name means a new project.
- **Safety, not security.** The name is a label: it isn't a secret, and it isn't an authorization boundary. IAM, the per-repository service accounts and the per-run tokens remain the boundaries. Anyone who can edit `fugaro.yaml` on the base branch can write any name, but that only makes their runs refuse. The check prevents mistakes (the wrong project config, a copied repository, a kill switch sent to the wrong project), not attacks.

### 2.6 One word for "project": the renames (D17)

Freeing *project* for the Fugaro project means renaming everything that means the **GCP project ID** today. The codebase search behind this list was done at `850726c`.

| Today | After task 0 | Where |
|---|---|---|
| Flag `--project` (the GCP ID) | **`--gcp-project`**. `--project` now takes a project **name** | `internal/cli/cloud.go` (`addCloudFlags`, `openCloud`, the log-view check), help text in `internal/cli/secrets.go`, and `internal/cli/image.go` and `init.go`, which read it |
| Local config key `project:` | **`gcp_project:`**, plus the new `name:` | `internal/localcfg/localcfg.go` (`Config.Project`, `Override`, validation), and everything that reads `lc.Project`: `internal/cli/init.go`, `image.go`, `cloud.go`; `internal/infra/spec.go`, `discover.go`, `readiness.go`, `repo.go`, `apis.go`, `tfvars.go`; `internal/backend/gcp/*` (through its options) |
| Job and runner env `FUGARO_PROJECT` = the GCP ID | **`FUGARO_GCP_PROJECT`** = the GCP ID. **`FUGARO_PROJECT`** = the project **name** (was `FUGARO_PROJECT_NAME` in D16) | Written by `internal/infra/spec.go` (`platformEnv`). Read by `internal/backend/backend.go` (the execution identity) and `internal/cli/imagecheck.go` (the check job). Also the Terraform tests' fixtures (`deploy/terraform/gcp/roots/repo/tests/testdata/*.tfvars.json`, `repo.tftest.hcl`) and the M4 golden (`internal/infra/testdata/m4-jobspec-sandbox.*`). The golden is updated deliberately, with a note that the env names changed after M4 |
| Test-only `FUGARO_LIVE_PROJECT` | **`FUGARO_LIVE_GCP_PROJECT`** (renamed for consistency; it's test-only, so there are no users to migrate) | `internal/e2e/live_gcp_test.go`, `internal/backend/gcp/live_test.go`, and `docs/gcp-live-checklist.md` |
| Docs using `--project` or `FUGARO_PROJECT` | Updated | `docs/gcp-setup.md` and `docs/gcp-live-checklist.md` (13 uses of `--project`), v1.md §3.4, §5.4 and §9.1, and the plugin skills (none use `--project` today; the new project header is mentioned in the `working` skill). **The M4 and M5 plans are left as they are:** they are historical records |
| Terraform variables `project` (the GCP ID) in `deploy/terraform/gcp/{modules,roots}/*/variables.tf` | **Unchanged**, a deliberate exception. `project` is the Google provider's own convention, and a module input that external Terraform users consume. The name arrives as a new variable, `fugaro_project` | The modules and roots |

**No transition layer** (D18). The runner reads the GCP ID only from `FUGARO_GCP_PROJECT`, and the name only from `FUGARO_PROJECT`. The local config decodes strictly, so an old `project:` key is an unknown-field error: "`project:` is now `gcp_project:`; see §13.1".

- **`--project <value>` that names no project** exits 1, listing the project names and a one-line hint: "(the GCP project is `--gcp-project`)".
- **`--gcp-project` that disagrees with the selected project's `gcp_project` is refused, with no force flag.** Its old use, pointing one config at another GCP project, now means acting on a *different Fugaro project* through the wrong project's config: exactly the confusion D16 and D17 exist to prevent. The legitimate uses remain:
  - **creating** a project config: `fugaro init` and `init --config-only` with no config yet;
  - agreeing with the selected config, which does nothing.

## 3. Architecture

```
 local ─ fugaro run ── signJwt (signer SA) ──► custom token {slug, run, exp} ──► runs/<slug>/<run>/budget-token (GCS)
       ─ fugaro watch / budget / report ──(user IAM, REST + SSE)──► RTDB, Firestore
                                                                    ▲         ▲
 Cloud Run job ─ fugaro exec ── gateway 127.0.0.1 ──(run's ID token)┘         │
                     │            │                                             │
                     └─ claude -p ┘   ──► api.anthropic.com | Vertex AI          │
 Cloud Scheduler ─► history job (own image): rollover RTDB → Firestore, sweep ──┘
```

- **The gateway** is a `net/http` handler inside `fugaro exec`. It starts at bootstrap when `FUGARO_BUDGET_MODE` is set, and holds the lease, the price table and the real credential. It forwards requests byte for byte and streams responses back without buffering.
- **The run's Firebase identity** comes from a custom token that the launcher minted. The runner exchanges it for an ID token at bootstrap and keeps the ID token and refresh token only in memory.
- **The history job** is a Cloud Run job in the project's GCP project with its own distroless image (D11), running `fugaro budget history`. Cloud Scheduler starts it daily for the rollover and every 15 minutes for the sweep. **It is a scheduled, admin-privileged job, not a long-running service.** It is the one exception to "no budget service": it holds `firebasedatabase.admin` on the FP. The optional budget service remains a documented later hardening (§11).
- **RTDB and Firestore** live in the project's own Firebase project (FP), which is either a GCP project of its own or the installation's own project (§6.0). The runs bucket, jobs, the history job and Scheduler stay in the installation project.

---

## 4. The budget model

| Cap | Set with | Bounds |
|---|---|---|
| `global.dailyUsd` | `fugaro budget set --global --daily` | The sum over all of the project's repositories of reserved minus released per UTC day |
| `global.perRunUsd` | `--global --per-run` | Each run's lifetime reserved minus released, and the default for every repository |
| `repos/<slug>.dailyUsd` | `--repo R --daily` | This repository per day. A repository without one uses `defaults.repoDailyUsd`. With neither, it has **no budget and halts** (fail closed) |
| `repos/<slug>.perRunUsd` | `--repo R --per-run` | Per run. The effective cap is `min(repo, global)` |
| `limits.maxReserveMicros` | `--max-reserve` | The most one write may reserve (default $5). This bounds each step of a compromised token |

- **Kill switches:** `kill/global` and `kill/repos/<slug>`, each `{on, by, at, reason}`.
- **What a cap counts:** reserved minus released, in integer **micro-dollars (µ$)**. That is spent plus outstanding. Rules compare exact integers, so floating-point equality never matters.
- **The day** is the UTC epoch day number, `floor(now / 86 400 000)`, with a human date stored alongside. A lease belongs to the day it was granted on.
- **What counts toward caps:** model dollars on `api-key` and `vertex`. `oauth` notional dollars are recorded as `notional` and never capped. Compute cost is not capped (D7).

---

## 5. Enforcement: the gateway

### 5.1 What the agent sees

| Auth | Agent environment | Gateway upstream and credential |
|---|---|---|
| `api-key` | `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>` and `ANTHROPIC_API_KEY=<random per-run gateway token>`. **The real key is removed from the agent's environment** | `https://api.anthropic.com`, with the runner's key |
| `vertex` | `CLAUDE_CODE_USE_VERTEX=1`, `ANTHROPIC_VERTEX_BASE_URL=http://127.0.0.1:<port>/v1`, `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, `CLOUD_ML_REGION` and `ANTHROPIC_VERTEX_PROJECT_ID` | The regional or global Vertex endpoint, with a token from the metadata server |
| `oauth` | Unchanged. **Never proxied** (§5.8) | n/a |

- **Allowed endpoints.** Per Anthropic's gateway guide:
  - `POST /v1/messages` and `/v1/messages/count_tokens`;
  - on Vertex, `:rawPredict`, `:streamRawPredict` and `count-tokens:rawPredict`;
  - `HEAD /api/hello` gets a local 200.

  Anything else returns 404. `count_tokens` is free and needs no reservation.
- **Forwarding follows the guide.**
  - `anthropic-version`, `anthropic-beta` and the body pass through unchanged, and **bodies are never rewritten**.
  - Responses stream without buffering, including keep-alive pings.
  - `retry-after`, `x-should-retry`, `anthropic-ratelimit-unified-*` and error bodies are passed through.
- **What the gateway parses.** From requests: `model` (on Vertex, from the URL), `max_tokens` and any 1-hour `cache_control`. From the stream: the `message_start`, `message_delta` and `error` events.
- **Attribution.** `x-claude-code-session-id` and `x-claude-code-agent-id` are logged with each call's usage.
- **Stopping the repository's settings from bypassing the gateway.** The repository's `.claude/settings.json` could set `env.ANTHROPIC_BASE_URL`, or on Vertex point Claude Code straight at Vertex. The runner therefore writes a **managed settings file** (`/etc/claude-code/managed-settings.json`) before the first stage, holding the gateway variables at the highest precedence (A6).

### 5.2 Model pinning and the price table

- **An allow-list per stage:** `{role model, background model}`. Any other model gets `400 invalid_request_error: "fugaro: model X is not pinned for stage review"`, and **the stage fails** (`failed`, a configuration error, not `halted`), per D8. Requests are never rewritten.
- **The price table** is embedded (`internal/pricing`), with its source and check date. Per model it holds:
  - `inputPerM` and `outputPerM`;
  - the multipliers `cacheWrite5m` (1.25×), `cacheWrite1h` (2×) and `cacheRead`. The read multiplier is per model: the docs give 0.1× with per-model exceptions such as 0.05×.
  - an optional long-context tier, `webSearchPer1k`, and aliases for each provider.
- **Owner overrides:** `model_prices` in the local config, passed to jobs and the history job as `FUGARO_MODEL_PRICES`, the same way `FUGARO_COMPUTE_PRICES` is. Repositories can't set prices.
- **An unknown serving model** (for example after a server-side fallback) is charged at the table's highest rates, flagged `priced_as: max`.

### 5.3 Two-level reservation

**Level 1: each call, in-process.** The gateway holds `lease{day, granted, used, reserved}`, and `free = granted − used − reserved`.

```
on request(model, body, maxTokens):
  if killed or halted: 403 "fugaro: budget halted: <reason>", x-should-retry: false
  w := worstCase(model, len(body), maxTokens, wants1hCache)          # §5.4
  while free < w: topUp(max(w − free, leaseSize()))                   # level 2; may halt
  reserved += w                                                       # mutex; parallel calls are independent
  stream upstream → client, tee-parsing usage
  actual := price(usage, servingModel)                                # §5.5
  reserved −= w; used += actual; queue usage for the next report
```

**Level 2: leases against RTDB, with the run's own token.** One atomic multi-path `PATCH` at the root (§6.3) makes four writes:

- **The run's ledger for the day:** `reserved += L`.
- **The run's lifetime ledger:** `reserved += L`.
- **The repository's day counter:** `counted += L`.
- **The global day counter:** `counted += L`.

The rules (§6.4) accept the write only if all of these hold:

- the token is valid and names this run;
- neither kill switch is on;
- `L ≤ maxReserve`;
- the run's lifetime `reserved − released ≤ perRun`;
- the new repository counter is within its daily cap;
- the new global counter is within the global daily cap;
- both counter deltas equal `L` exactly.

A refused write is re-read and evaluated locally, which tells a stale read (retry) apart from a real refusal (halt with the reason). See §6.3.

- **Lease size shrinks near a cap:** `clamp(5% of the smallest headroom, $0.25, $2.00)`, and never less than the call's worst case. When `L` doesn't fit but `w` does, the gateway asks for `w`. Before halting on a refusal, it retries once after 20 s, since other runs' releases may have landed.
- **Usage reports.** Every 15 s (the heartbeat), and at the end of each stage and of the run, the gateway writes `spent += actual`, `byModel` and the call counts. Moving spend from outstanding to spent doesn't change `counted`.
- **Releases.** At run end, and on a lease the gateway gives up, `released += unused`, and the repository and global `counted` fall by exactly that amount in the same write. The rules bound this by the run's own outstanding amount: `spent + released ≤ reserved`.
- **Crashes.** A crashed run releases nothing. Its outstanding amount stays counted, which errs high and never low. The sweeper marks the run `crashed` (§6.5).
- **Traffic.** Take 20 concurrent runs, each making a call every 5 s at $0.05. With $2 leases that is about 0.1 lease writes/s, plus 1.3 heartbeat writes/s. The spec's per-call transactions would need about 24/s.

### 5.4 The worst-case estimate

`worstCase = bodyBytes × inputRate × cacheMult + maxTokens × outputRate + (web search enabled ? searchCap × perSearch : 0)`

- **Body bytes bound the input tokens from above:** a token is at least one byte, and JSON and base64 only make bytes over-count more. This over-reserves by roughly 3–4×; the lease absorbs it and reconciliation refunds it.
- **`cacheMult`** is 2× if the request asks for a 1-hour cache TTL, else 1.25× if there is any `cache_control`, else 1.
- **The long-context tier** applies when the byte bound crosses its threshold.
- **`maxTokens`** comes from the body, and is refused if it's above the role's `max_output_tokens`. Thinking tokens are output tokens and fall within `max_tokens`.

### 5.5 Reconciling against the stream

- **Where usage comes from.** `message_start.message.usage` gives `input_tokens`, `cache_creation_input_tokens` (split into `cache_creation.ephemeral_5m_input_tokens` and `…_1h_…`), `cache_read_input_tokens` and `output_tokens`. `message_delta.usage` is **cumulative** and may update the input fields and add `server_tool_use.web_search_requests`. The last value of each field wins.
- **What sets the price.** Calls are priced at the **serving** model's rates and the tier for the input total.

| Case | Charge |
|---|---|
| The stream completes | Actual usage |
| An upstream error before `message_start` | 0 (A3) |
| An error or disconnect after `message_start` | Input tokens plus the **reserved** output tokens |
| The client or a stage kill cancels the call | As above, and the upstream request is cancelled |
| The runner dies | Nothing is released, so the outstanding amount stays counted |
| Claude Code retries (429, 529, a dropped stream) | Each attempt is a new request with its own reservation. The gateway never retries |
| Parallel subagent calls | Each reserves independently. Top-ups are serialized |

### 5.6 Other ties between the stages and the gateway

- **Per-stage `--max-budget-usd` stays** as defence in depth.
- **Where the per-run cap is checked.** In M9a it's in-process, against `FUGARO_MAX_RUN_USD` from the local config. From M9a.1 it is the lowest positive of that ceiling, the default branch's `fugaro.yaml` `budget.per_run_usd` and the run's branch (a branch can only tighten; a looser value is ignored with a warning and recorded in `result.json` `policy`). From M9b the rules check the minimum of that and the RTDB caps (`min(RTDB, committed policy)`); `budget.per_day_usd` is a real `fugaro.yaml` key from M9b: tightening-only, enforced client-side against the repository's own day counter (R1), and the lower RTDB cap wins.
- **`fugaro verify` tells the gateway when it starts and ends** (loopback, with the per-run gateway token). The registry then shows `implement · test`. This is cosmetic.

### 5.7 Budget modes

The local config's `budget.mode`:

- **`off`**: no gateway, the same as having no `budget:` block;
- **`observe`** (M9b, correction R2: the mode is **project-wide**, stored at `config/mode` in the database and set by `init --firebase --budget-mode` or `fugaro budget set --global --mode`; the job's `FUGARO_BUDGET_MODE` is only the on/off gate and a committed or ceiling `off` means no backend at all; `budget show` prints both, since an env `observe` under a database `enforce` halts on cap refusals): account and lease in RTDB, but **caps are advisory**. Would-be cap halts are logged and never refused; the observe rules variant drops only the cap checks. **Kill switches and fail-closed still apply:** an observe run halts on a kill, and halts when the backend is unreachable after the 3-minute grace (D14). So observe relaxes caps only, which is why it keeps its name. For behaviour with no budget at all, use `off`.
- **`enforce`**: everything in this document.

### 5.8 What `oauth` runs get (A1: the user accepted the risk)

`oauth` stays as it is today. Its traffic never passes through the gateway or any proxy.

| Mechanism | `oauth` runs |
|---|---|
| Gateway, reservation, per-call allow-list | **None** |
| Dollar caps (per run and daily) | **Not enforced.** Advisory only: at each stage end the runner records Claude Code's notional `total_cost_usd` as `notional` on the run and the repository and global day nodes (the rules allow increases only, with no cap check), and `modelUsage` for `byModel` |
| Per-stage `--max-budget-usd` | **Enforced by Claude Code**, in notional dollars (as today) |
| Token cap | **Enforced at stage boundaries:** `agent.max_run_tokens`, summed from each stage's result `usage` (input, cache and output tokens). A run can overshoot by at most one stage. Crossing it halts the run (`halt.reason: token_cap`) |
| Time caps | Stage and total timeouts, as today |
| Model pinning | `--model` and the pin variables (§2.1). There is no gateway backstop, so a model name that escapes pinning isn't blocked |
| Kill switches | **Enforced**, through the same RTDB listener |
| Registry, history, watch | Yes, with a `notional` label |
| Fail closed (D14) | Yes: halt after the 3-minute grace |

### 5.9 Halted: the runner, `result.json` and readiness (§4.2)

- **How a run halts.** A refusal, a kill or a token cap cancels the stage context with `ErrHalted{reason}`. This is the same pattern as `ErrCancelled`. The process group is killed, the loop ends, and **finalize runs normally**: it makes no model calls, so a kill during finalize or writeback is ignored.
- **Status `halted`,** with one of two outcomes:
  - `draft`: the branch exists. The run pushes and opens a draft PR.
  - `none`: the run stopped at bootstrap before the lock and branch (a kill switch, no cap, an expired token, the backend unreachable after the grace), or it was a follow-up that stopped before its push. **No PR is opened, and the exit code is 0** (D9).
- **Precedence.** Whichever cause cancelled the stage first wins, cancel or halt. A halted run is never ready.
- **The report** starts `## Fugaro — Halted: <reason>`, followed by the cap or switch, the time, this run's spend, and how to continue (`fugaro budget …`, then `fugaro run --pr N`). A halted run pushed its work, so follow-ups work as in §4.4.
- **Schema changes** (`result.json` stays at version 1, and every change is additive):
  - `status` gains `halted`;
  - a new `halt: {reason, scope, at, detail}`, where `reason` is one of `kill_switch`, `run_cap`, `repo_daily_cap`, `global_daily_cap`, `no_cap`, `token_cap`, `budget_unavailable` or `budget_token_expired`;
  - `cost` gains `model_source: gateway|claude-code`, `model_by` and `unreconciled`.
- **Launch pre-check.** `fugaro run` refuses (exit 1) when the repository is killed, has no cap, or has less than $0.25 of daily headroom. When it can't read RTDB it exits 2, since it fails closed. `--no-budget-check` skips only this client-side check.

### 5.10 How the kill switch reaches a run

- **Listening.** The runner holds one REST event stream on `config/kill` (its rules allow reading `kill/global` and its own repository's switch), authenticated with the run's ID token. It reconnects when it sees `auth_revoked` or when the token expires, about hourly.
- **Stopping.** On `on: true`, the runner halts the stage and the gateway cancels its in-flight streams. Latency is seconds, plus the 10-second SIGTERM grace.
- **Polling as a backstop.** The heartbeat write re-reads the switch every 15 s, in case the stream is silently stale.


### 5.11 What M9b built (implementation notes)

These record where the build settled something the design left open. The authoritative description of the trust model is v1.md §6.1 ("The shared budget").

- **The budget session** (`internal/budget`) is one object per run: it exchanges the token, reads the caps and kill switches, creates the registry entry, runs the heartbeat (15 s, carrying the usage report in the same multi-path write) and the two kill streams, and provides the gateway's `Lease` source (`Grant`, `Report`, `Release`). Leases are tops-ups of the run's ledger and both day counters in one atomic multi-path `PATCH`; the rules are the compare-and-set and `Evaluate` classifies a denial (stale: retry up to 8 times, otherwise a refusal with the local predicate's reason).
- **Grace per source.** A source's clock starts at its first failure. Any successful database call stops every database source's clock ("any success proves the database reachable"); the Firebase Auth sources (`exchange`, `refresh`) stop only by their own success. A kill stream's error counts only while the REST poll also fails. The window is 3 minutes, shortened only by `FUGARO_BUDGET_GRACE` (5 s to 3 m).
- **Two kill streams**, `config/kill/global` and `config/kill/repos/<slug>`, each reconnecting on `auth_revoked` and token expiry; the heartbeat re-reads both.
- **Rules** are generated, leaf-only for writes, and tie the run's ledger and the counters to each other in both directions (see v1.md §6.1). Cap comparisons read `config/mode`; everything else is enforced in every mode.
- **Committed `budget.per_day_usd`** is client-enforced and repository-scoped (R1): `Evaluate` takes the effective repository cap; the project's day cap is the database's alone.
- **`oauth`** records `notional` only (R7) and is bound by the kill switches, the token cap and the grace.
- **`fugaro budget`** (`show`, `set`, `kill`, `resume`, `prices`): admins write caps with ETag-guarded single-node `PUT`s that show the old and new value; raising a cap, setting one that was unset, lowering the mode to observe and `resume` ask for the project's name (or `--yes`); `kill --all` asks for it too. `show` needs the Viewer role only. `--repo` is `owner/name`.
- **`init --firebase <id>`** adopts a Firebase project the user created and linked to billing. It refuses a project that is missing, has no billing, or whose database holds data but no Fugaro mark; it runs three confirmed applies, deploys the rules, the mark, the project name, the mode and `maxReserve` over REST, and **refuses `--budget-mode enforce` unless the global daily and per-run caps exist** (an enforcing budget with no global caps would refuse every lease). Data is kept on a rollback (`prevent_destroy`).
- **Sweeper** (`fugaro budget history --sweep`) as in v1.md §6.1; the rollover (`--rollover`) is M9d, built (§9).

---

## 6. Firebase

### 6.0 Where Firebase lives (D3, revised by the user's ruling of 2026-10-04)

**Each Fugaro project has exactly one Firebase project (FP), shared by all its repositories and all its people. The FP is either a GCP project of its own or the installation's own GCP project: both layouts are supported, and the same-project one is the simplest.** `fugaro init --firebase <id>` takes either; nothing else in the setup changes.

> **Revision (2026-10-04).** D3 originally required a dedicated FP and refused the installation's own project ("V2" below). The user ruled that wrong: a Fugaro project must be able to run on **one** project that holds both the installation (Cloud Run jobs, the runs bucket, registries, secrets, Scheduler, the Terraform state bucket) and the budget backend. The old rationale is kept below because it was a security argument, and the new boundary says how it is met without a second project.

**The old rationale (what a second project bought).** A compromised job's service account sat in a project that had no IAM on the budget at all, so even a broad grant added to the workload project by mistake could not reach the database. It also gave separate billing for the backend.

**The boundary in one project.** What keeps the budget out of a job's reach is that **no job, build or scheduler account is granted, by this repository's Terraform, a role that reaches the backend**, and the database's own rules decide what a run's token can do. That is weaker than a project boundary: the next section lists what it does not cover, including one pivot (Cloud Build) that a second project used to close.

- A job account holds, at project level, only `roles/aiplatform.user` (a Vertex workflow); a build account only `roles/logging.logWriter` and `fugaroBuildSubmitter` (`cloudbuild.builds.create` and `get`); the scheduler account nothing at project level (`roles/run.invoker` on the check job, which is started with no body; `fugaroJobRunner`, which adds `run.jobs.runWithOverrides`, on the history job, whose rollover call carries overrides). **No** role on RTDB, Firestore, Identity Platform, API keys or IAM credentials, and never a primitive role (`owner`, `editor`, `viewer`).
- The token signer (`fugaro-token-signer`) holds no roles. Its one IAM grant is `fugaroTokenMinter` (`iam.serviceAccounts.signJwt` only) **on the signer account**, for launchers and operators. No project-level grant names it.
- The budget admins are unchanged: the project's `roles/owner` and `roles/editor` users and groups, plus `terraform.budget_admins`, get `roles/firebasedatabase.admin` (and `datastore.viewer`, `serviceUsageConsumer`). The history account's four roles are unchanged (`firebasedatabase.admin`, `firebaseauth.admin`, `datastore.user`, `serviceUsageConsumer`).
- A run holds a Firebase ID token, which is not an IAM identity; the rules (§6.4) bound what it can read and write.
- Pinned by tests (what **this repository's Terraform grants**, not what people or Google grant in the project): the Terraform text checks in `deploy/terraform/terraform_test.go` (`TestJobAccountsProjectRolesAreExactly` lists the roles any job, build or scheduler account holds; `TestIAMResourcesUseKnownExpressions` (every IAM resource of every type must use allowlisted role and member expressions; backend roles only in the Firebase module); `TestNoAuthoritativeIAM`; `TestCustomRolesNeverReachTheBackend`; `TestSignerIAMIsMinterOnly`; `TestHistoryAccountBackendRolesAreExactly`) and the mock-provider plans `same_project_apis` and `same_project_grants` (Firebase root) and `same_project_resolved_iam` (installation module) that assert the resolved grants and custom-role permissions.

**Residual risk in one project (be honest about it).**

- **Cloud Build pivot (the main new risk).** Every build account holds `fugaroBuildSubmitter` (`cloudbuild.builds.create` and `get`) on the project, which `fugaro image build` and the daily check need. A hostile Dockerfile step (a repository's image build runs repository code) can submit a **new build without `serviceAccount`**, and that build runs as the project's default Cloud Build or Compute Engine service account. Where that account holds `roles/editor` (older projects, or an organization without the policy `iam.automaticIamGrantsForDefaultServiceAccounts`), it reaches the RTDB, Firestore and Identity Toolkit. With two projects the pivot ended in the installation project and never reached the Firebase project. **Mitigation, to do in the project:** remove the primitive roles from the default Compute, Cloud Build and App Engine accounts and set that organization policy. `init --firebase` in the same-project layout reads the project's IAM policy and **warns** (it never refuses or changes it) when one of those accounts holds `roles/editor` or `roles/owner`. If you cannot, use a Firebase project of its own.
- **Run tokens are accepted per project, so the forgery boundary is who can sign as a service account, not who holds the minter role.** Firebase accepts a custom sign-in token signed by *any* service account of the project, not only by `fugaro-token-signer` (live Check 26, step 7, 2026-10-05: a token signed by `fugaro-scheduler` was accepted, HTTP 200). `fugaroTokenMinter` on the signer is the designed, narrow path for launchers and operators, but it is **not a restriction**: whoever can sign as *any* service account of the Firebase project (`iam.serviceAccounts.signJwt`, `signBlob` or `getAccessToken` on it, `iam.serviceAccountKeys.create` to make a key and sign locally, which `roles/iam.serviceAccountTokenCreator`, `roles/iam.serviceAccountKeyAdmin` and any custom role holding them grant; or `iam.serviceAccounts.setIamPolicy`, as `roles/iam.serviceAccountAdmin` has, to grant itself one of those) can mint a token with any `fs`, `fr`, `fx` and `rb`, which is to act as any run against the budget database, within what the rules (§6.4) allow a run. In one project that includes anyone holding such a role at project level, on any account of the project, and a default account that was granted one. With two projects only the Firebase project's accounts count. Every role is resolved through the IAM API (primitive ones included), and a principal is reported when its role's resolved permissions include one of those, with the matching permissions printed; nothing is claimed about a role beyond what the API returns. In Check 26 the owner did not hold `signJwt` (it needed an explicit grant); whoever administers the project's IAM can always grant themselves the right. What the design expects: the Firebase Admin SDK account (`firebase-adminsdk-*@<fp>.iam.gserviceaccount.com`, Firebase's own) and `fugaroTokenMinter` on the signer account alone (the minter role must still be `signJwt` and nothing else). **`fugaro doctor` checks it** (`token-signers`, read-only, with a budget backend): it lists from the Firebase project's IAM policy, and from the policy of each of its service accounts, every principal holding such a role, treats those two cases as information, and warns about every other one, naming the principal, the role, the scope and the one-line command that removes the binding. It reads that project's policy only, and says so on every run: roles granted by a folder or an organization above it, deny policies and Google's service agents are not read, and a role it cannot read is a warning (unknown, never safe). (Check 26 verifies the acceptance.)
- **Project-wide grants now share the project with the jobs.** Launchers, operators and admins hold `roles/datastore.viewer`, `roles/serviceusage.serviceUsageConsumer` and `roles/firebasedatabase.viewer` project-wide, and the history account holds `roles/datastore.user`, `roles/firebaseauth.admin` and `roles/firebasedatabase.admin` project-wide. In two projects these applied to the FP only; in one project they apply to the workload project (Datastore and Firestore documents, Identity Toolkit users, the usage-consumer permission on every API). They give no access to Cloud Run, Secret Manager or the buckets beyond what the same people and account already had.
- **Project Owners and Editors** can already read and write the RTDB (and are budget admins by D6). With two projects the same was true of the *installation's* Owners and Editors, who were made admins of the FP; one project removes the case where someone is an owner of the workload project but not of the FP.
- **The workloads share a project with the database.** A *new* grant that someone adds by hand to a job account (a primitive role, `roles/firebase.admin`, `roles/firebasedatabase.*`, `roles/datastore.*`) would now reach the backend directly, where with two projects it would have had to be made in the FP. Terraform in this repository never does it, and the tests above fail if it ever tries; a hand-made grant is outside what they can see. Google's default Compute and App Engine accounts may hold `roles/editor` in an older project: no Fugaro job runs as them, but a person who deploys something as one of them in that project gets that reach.
- **The boundary is therefore per-resource roles and the database rules**, not the project. A compromised agent still cannot lift caps (the rules; admins only over IAM) or use the backend through its own identity (no role).
- **Quota and billing are shared.** The backend's cost is on the installation's billing account, and a job that exhausts a project-wide quota (Identity Toolkit, Secure Token) can slow sign-ins of its siblings; the 3-minute grace (D14) covers it.
- The Firestore and RTDB marks, `init`'s refusal of a database that holds unmarked data, and the Identity Platform public-sign-up refusal apply in both layouts.

**What creating the FP takes (two-project layout).** With one project, steps 1 and 2 are already done (the project exists and has billing, because the installation needs it), and `--firebase <the installation's project>` adopts it.

- **"Global" means the Fugaro project,** and its repositories are the spec's "projects".
- **There is no live state across Fugaro projects.** Aggregating across an organization comes later and is read-only: for example, each project's history job exports its daily `spendDaily` roll-up to one shared place.

**What creating the FP takes.**

1. **The user creates it,** as a new GCP project.
   - This needs `resourcemanager.projects.create` on the parent. We recommend the same organization or folder as the project's GCP project, so the same org policies apply. A user without an organization creates it without a parent.
   - Only people who hold that right can do this. `fugaro init` never creates projects.
2. **The user links a billing account** (Blaze plan). This needs `billing.resourceAssociations.create` on the billing account (Billing Account User) and on the project.
   - `fugaro init` never enables billing (§8.2), and that rule stays.
   - Blaze is needed because Spark's hard cap of 100 concurrent RTDB connections would make a busy fleet fail closed.
   - The user adds a billing budget alert.
3. **`fugaro init --firebase <fp-id>` adopts that project.**
   - Discovery checks the project exists, has billing, and is empty or carries our mark (§6.7).
   - A Terraform root then enables Firebase on it (`google_firebase_project`, google-beta) and creates everything in §6.6.

   We rejected having `init` create the project itself through `google_project`. That would need project-creation and billing rights on the person running init, and it would break init's rule of never enabling billing.
4. **Org policies.** Nothing in the design creates service-account keys: signing goes through `signJwt`, so a "disable key creation" policy is fine. A domain-restricted-sharing policy must allow the installation's members.

**Considered and rejected:**

- **V2: Firebase inside the project's own GCP project.** Originally rejected because the user wanted a separate project; **accepted on 2026-10-04** (revised D3). Both layouts are supported.
- **V3: one FP shared by several Fugaro projects.** Rejected because it adds a shared blast radius, a project level in the rules, and cross-project admin ambiguity. Cross-project views come later, read-only, as described above.

### 6.1 APIs, region and IAM

- **APIs on the FP:** `firebase`, `firebasedatabase`, `firestore`, `firebaserules` (for Firestore rules) and `identitytoolkit` plus `securetoken` (for custom-token sign-in). `iamcredentials` must be enabled where the signer account lives.
- **Regions (D4).** RTDB goes in `us-central1`: RTDB offers only `us-central1`, `europe-west1` and `asia-southeast1`, and a location can't be changed later. Firestore goes in `us-east5` (check 23 confirms the location is offered; there is no `nam5` fallback in the code, see D4) and can't be changed either.
- **IAM.** Each grant is an `*_iam_member` in the Firebase root (§6.6). Every grant to a principal of the project is cross-project, onto the FP.

| Principal | Grants |
|---|---|
| `fugaro-token-signer` (a new account in the FP, display name `Fugaro token signer`) | **No roles at all.** It exists only as the key that signs custom tokens |
| Launchers and operators | `roles/firebasedatabase.viewer` and `roles/datastore.viewer` on the FP, plus the custom role **`fugaroTokenMinter`** (`iam.serviceAccounts.signJwt` only) **on the signer account**. That is narrower than `serviceAccountTokenCreator`, which would also let them mint access tokens |
| `fugaro-history` (the history job's account) | `roles/firebasedatabase.admin`, `roles/datastore.user` and `roles/firebaseauth.admin` (to delete expired run users) on the FP. In the project's GCP project, its own narrow custom role `fugaroHistory` (`run.executions.list` and `get`, `run.jobs.get` and `list`, `run.operations.get`, for the cross-check) |
| `fugaro-scheduler` | `fugaroJobRunner` (`run.jobs.run` and `run.jobs.runWithOverrides`) on the history job (the rollover's overrides need the second; `roles/run.invoker` gave a live 403) |
| Job accounts | **Nothing on the FP.** They hold a Firebase ID token, which is not an IAM identity |
| Budget admins (D6) | `roles/firebasedatabase.admin` and `roles/datastore.viewer` on the FP, granted by the Firebase root to: the GCP project's `roles/owner` and `roles/editor` members, which `init --firebase` reads from the GCP project's IAM policy and passes as tfvars; and `terraform.budget_admins`. The FP's own owners and editors (at least the person who created it) hold admin implicitly |
| **Who can't** change caps or kill switches | Launchers and operators who aren't listed (they get the viewer role and the minter role only); job accounts; `fugaro-scheduler`. The history account holds `firebasedatabase.admin` for its sweep and rollover. Its code never writes `/config`, but IAM can't enforce that: a compromise of the history image or its account could lift caps (§11) |

### 6.2 Data model

**RTDB.** Amounts are integers in µ$. Days are UTC epoch day numbers. Keys are escaped (`.` becomes `%2E`).

```
/config                                    # admins only (IAM REST); run tokens read own slice
  caps/global          {dailyMicros, perRunMicros}
  caps/defaults        {repoDailyMicros, repoPerRunMicros}
  caps/repos/<slug>    {dailyMicros, perRunMicros, repo}
  limits               {maxReserveMicros}
  kill/global          {on, by, at, reason}
  kill/repos/<slug>    {on, by, at, reason}
/runs/<slug>/<run>                         # lifetime ledger; the run's token only
  {reserved, released, spent, notional, overrun, tokens, exp}
/spend/<day>/global                        # {counted, spent, notional, calls}
/spend/<day>/repos/<slug>                  # {counted, spent, notional, calls, byModel/<m>/{micros,in,out,cr,cw}}
/spend/<day>/runs/<slug>/<run>             # {reserved, released, spent}: the run's share of the day
/spend/<day>/meta                          # {date: "YYYY-MM-DD", archived}  (the history job)
/agents/<slug>/<run>                       # the registry, written by its run
  {repo, workflow, title, stage, round, verify, coder, reviewer, auth, prUrl,
   startedAt, stageStartedAt, stageDeadline, updatedAt, spent, halted}
/outcomes/<day>/<slug>/<run>               # {status}: written once at run end; counted by the history job
```

- **`title`** is the task's first line, clipped to 80 characters and redacted. Launchers already read every `task.json`.
- **Day nodes older than 8 days** are deleted by the rollover only after the day is final, equal to its Firestore documents and read back (§9). `spend/<day>/meta` is not written by M9d (nothing reads it); the Firestore document, not a flag in RTDB, says a day is archived.

**Firestore** (the `(default)` database):

`spendDaily/<YYYY-MM-DD>_<slug>` holds one repository's day (no project prefix: the Firebase project is the Fugaro project's own, D3): `{repo, slug, date, spentMicros, notionalMicros, computeMicros, unreconciledMicros, overrunMicros, runHours, calls, runs, outcomes{succeeded, failed, halted, cancelled, infra_error}, byModel{<escaped model>: {micros, in, out, cr, cw}}, byPerson{<escaped requested_by>: {micros, notionalMicros, runs}}, capDailyMicros, final, version: 1, archivedAt, writtenAt}`. Amounts are the RTDB's integer µ$ (USD is formatting; the earlier `...Usd` floats are dropped), map keys use the RTDB's `Key` escaping, and `meta/installation {managed_by, project, gcp_project, firebase_project, version, fugaro_version}` is the Firestore mark (written once, never replaced; a foreign or unreadable mark refuses every write and read). A day is written provisionally after it ends and **final** once `now >= start(D+2)`, because a run may still write to the previous day until then (rules `DAYOK`); a final document is never rewritten without `--day D --force`. Plan: [2026-10-03-m9d-history-and-report.md](../plans/2026-10-03-m9d-history-and-report.md).

The Firestore database is created by an idempotent REST step in `init --firebase`, not by Terraform: its location is permanent and a Terraform create does not adopt an existing database. Its rules deny everything; only IAM principals read or write. History keeps the requesters' addresses (`byPerson`) indefinitely, readable by IAM viewers only. The history account also needs `roles/storage.objectViewer` on the runs bucket (for `compute_usd`).

The global figure is computed on read. There is no composite index: `report` queries the `date` field with a range and filters repository, person and model client-side.

### 6.3 Transactions over REST

- **What the Go SDK can't do.** The Firebase Admin Go SDK's RTDB client is REST-only, with no `onDisconnect`, and it authenticates as an admin. Runs need their own ID token, so Fugaro ships a small REST client of its own (`internal/rtdb`, about 350 lines). It does:
  - `GET` (with `X-Firebase-ETag: true` when an ETag is wanted);
  - `PUT` with `if-match`;
  - multi-path `PATCH` at the root;
  - streaming (`Accept: text/event-stream`, with the events `put`, `patch`, `keep-alive`, `cancel` and `auth_revoked`);
  - authentication with `auth=<ID token>` for runs, or an OAuth `access_token` for people and the history job.
- **Where ETags fit.** ETag conditional writes work on **one location**, and need that whole location read first. A lease touches four nodes, and reading their common ancestor would expose other repositories' counters. So ETags serve only single-node admin writes (`budget set` on a cap node).
- **The lease write uses rules as its compare-and-set.** The write carries **absolute** new values, computed from what the client just read. The rules demand `N(counter) − O(counter) == N(own run) − O(own run)`, where `O` is the stored value and `N` the value after the write. If any other run changed the shared counter in between, the delta check fails and the write is denied, so a stale write can never land. One multi-path update commits or fails as a whole (A4).
- **Telling a stale read from a refusal.** Both come back as `401 Permission denied`. The client then re-reads the five nodes involved (its run, the repository and global day counters, its caps, the kill switches) and evaluates the same predicates locally:
  - if they pass, the read was stale: retry, up to 8 times with jitter;
  - if they fail, it's a refusal, with the local reason;
  - after 8 stale retries, treat it as `budget_unavailable` (contention).
- **Contention.** Every lease in the fleet writes the global counter. That is about 0.1–1 writes/s at the expected scale, well within what optimistic retry handles.

### 6.4 Security: per-run tokens and database rules (D1)

**Minting (the launcher only).**

- `fugaro run` and `run --retry`, after the launch claim and before `jobs.run`, build a custom-token JWT:
  - `iss` and `sub` are the signer's email; `aud` is Identity Toolkit's audience;
  - `uid` is `r~<slug>~<run-id>`;
  - `claims` is `{fs: <slug>, fr: <run-id>, fx: <expiry in ms>}`;
  - `exp` is `iat + 1h`.
- **Who signs.** It is signed through the IAM Credentials `signJwt` call **as `fugaro-token-signer`**, which needs `fugaroTokenMinter` on that account. **No job account holds `signJwt`**, so a job can never mint a token. This is the designed path, not the boundary: Firebase accepts a token signed by any service account of the project, so what matters is who can sign as any of them (see "Run tokens are accepted per project"; `fugaro doctor` lists them).
- **Lifetime.** Firebase custom tokens are valid for **one hour at most** (A12), so the run deadline is enforced by the `fx` claim instead. `fx = launch time + task timeout (jobs.get: total + 2m) + 1h queueing allowance + 5m slack`. The rules require `auth.token.fx > now`. An ID token refreshes itself, but it is useless after `fx`, and the history job deletes the run's user two days later.
- **Delivery.** The token is written as `runs/<slug>/<run>/budget-token`, create-if-absent, before the launch. **It is not an env override,** because `jobs.run` request bodies can reach audit logs that every project log viewer can read.
  - At bootstrap the runner reads the object, **deletes it**, and exchanges the token through `accounts:signInWithCustomToken`. That needs the FP's web API key, which isn't secret but should be restricted to Identity Toolkit and Secure Token. The job gets it in its environment through the tfvars.
  - An exchange attempted more than an hour after minting fails: the run halts, `budget_token_expired`, outcome `none`, and the message says to launch again.
  - The token and ID token are registered with the redactor.
- **Refresh versus re-minting.** The run legitimately keeps its identity alive with the Firebase **refresh token** (Secure Token API). Refreshing keeps the same uid and claims, so the run can outlive the one-hour custom token without minting anything.
  - **Mint-on-start was considered and rejected.** A run can't mint its own custom token at bootstrap. `signJwt` can't be scoped by claims, so a job account holding it could mint tokens for any run, any repository, or any `fx`. Making that safe needs a minting endpoint that checks the caller's ID token and execution, which is exactly the budget service D1 rejected.
  - **What queued runs cost.** A run queued for more than an hour halts (`budget_token_expired`), and `fugaro run --retry` can't relaunch it, because it has launched. Launching again costs a new run ID.
- **Who can reach the token.** Launchers, and other runs of the same repository during the queue window, can read the object. §6.1 already accepts the repository as the boundary between runs. A token taken this way acts only as *that* run, within its caps.

**The rules.** The rules file is generated from a Go template (`internal/budget/rules`) and deployed by `fugaro init` with `PUT /.settings/rules.json`. Terraform doesn't manage RTDB rules. The fragments below use four macros; the generator expands each into `root.child(…)` and `newData.parent()…` chains:

- **`O(p)`**: the stored value at `p`, or 0 if absent.
- **`N(p)`**: the value at `p` after the write.
- **`RUN`**: `auth != null && auth.token.fs == $slug && auth.token.fr == $run && auth.token.fx > now`.
- **`TODAY`**: `$day == '' + ((now - now % 86400000) / 86400000)`.

**Rules in outline:**

```
/config/…            .read: caps/global, caps/defaults, limits, kill/global → any valid run token;
                      caps/repos/$slug, kill/repos/$slug → a token with fs == $slug.  .write: false (admins use IAM, which bypasses rules)
/runs/$slug/$run     .read: RUN   .write: RUN && newData.exists()
  day        .validate: the day node this write touches (today, or yesterday for spent/released only); $day below = N(day)
  reserved   .validate: N ≥ O  &&  N − O ≤ limits.maxReserveMicros
                         && (N == O || (TODAY-lease && kill/global.on != true && kill/repos/$slug.on != true))
                         && N − N(released) ≤ min(caps/repos/$slug.perRunMicros ?? defaults, caps/global.perRunMicros)
                         && N − O == N(spend/$day/runs/$slug/$run/reserved) − O(same)      # moves with the day share
  released   .validate: N ≥ O && N + N(spent) ≤ N(reserved)
                         && N − O == N(spend/$day/runs/$slug/$run/released) − O(same)
  spent      .validate: N ≥ O && N + N(released) ≤ N(reserved)
  notional, overrun, tokens   .validate: N ≥ O && N − O ≤ limits.maxReserveMicros
/spend/$day/runs/$slug/$run   .write: RUN && ($day is today or yesterday) && newData.exists()
  reserved   .validate: N ≥ O && (N == O || TODAY)
  released   .validate: N ≥ O && N + N(spent) ≤ N(reserved)
  (both)     .validate: let d = (N(reserved) − O(reserved)) − (N(released) − O(released));
                        N(../../repos/$slug/counted) − O(same) == d  &&  N(../../global/counted) − O(same) == d
                        # the share can't move unless both shared counters move by exactly d, and vice versa below
/spend/$day/repos/$slug/counted   .write: auth.token.fs == $slug && auth.token.fx > now
  .validate: N − O == (N(../../runs/$slug/<fr>/reserved) − O(same)) − (N(../../runs/$slug/<fr>/released) − O(same))
             && (N ≤ O || N ≤ caps/repos/$slug.dailyMicros ?? caps/defaults.repoDailyMicros)
/spend/$day/global/counted        .write: auth.token.fx > now
  .validate: the same delta equation, using the caller's fs/fr, && (N ≤ O || N ≤ caps/global.dailyMicros)
/spend/$day/{global,repos/$slug}/{spent,notional,calls,byModel/…}
  .validate: N ≥ O && N − O ≤ limits.maxReserveMicros × (the calls-field limit for calls)
/agents/$slug/$run   .write: RUN   .validate: every string ≤ 200 characters, and the fields and types fixed
/outcomes/$day/$slug/$run   .write: RUN && !data.exists()   (written once)
```

Where a counter's cap is missing, the value comes back `null`. `N ≤ null` is false, so the write is denied: the rules fail closed.

**What the rules deny.** Take a run A of repository X, with caps global daily $150, X daily $60, per-run $20 and `maxReserve` $5:

| # | Attempt by A's token | Why the rules deny it |
|---|---|---|
| 1 | Lower `spend/d/global/counted` by $5, changing nothing else | The global counter's delta must equal A's own `reserved − released` delta, which here is 0 |
| 2 | Release $5 and lower global and X by $5, with only $2 outstanding | The `released` rule: `released + spent ≤ reserved` fails. **Refunds are bounded by the run's own outstanding reservation** |
| 3 | Write run B's ledger, or `repos/Y/counted` | `.write` needs `fr == B`, or `fs == Y` |
| 4 | Reserve $6 in one write | `N − O ≤ maxReserve` |
| 5 | Reserve past $20 over the run's life | The per-run check on `/runs/X/A/reserved` |
| 6 | Reserve with X at $59 of $60 | The repository counter would exceed the daily cap |
| 7 | Reserve against tomorrow's or yesterday's node, to dodge today's cap | Only `TODAY` allows an **increase** of `reserved`. Old days allow only `spent` and `released` |
| 8 | Raise its own `reserved` without raising the shared counters | The run's `reserved` must move with its day share, and each shared counter's delta is checked against that share, so a write that leaves them unchanged fails. In the other direction, a counter can only change by exactly the run's delta |
| 9 | Set `config/kill/*/on` to false, or raise a cap | `/config` has `.write: false`. Only admins write it, over IAM REST |
| 10 | Reserve after the kill switch is on | The `reserved` rule checks both switches |
| 11 | Use the token after its deadline | `auth.token.fx > now` fails everywhere |
| 12 | Delete its own ledger to reset the per-run check | `.write` requires `newData.exists()` (validation rules don't run on a delete) |

**The worst case for a compromised run token, in numbers.** With the caps above:

- **Raise its repository's and the global day counters by at most $20** over its lifetime (the per-run cap), in steps of at most $5, and never past $60 for X or $150 globally.
- **Lower a shared counter only by releasing its own outstanding reservation.** Its net contribution is `reserved − released ≥ spent ≥ 0`, so it can never take a counter below where it found it.
- **Read** the global and X day counters, X's and the global caps and switches, and its own nodes. It reads nothing of other repositories.
- **Write** its own registry entry (the text is sanitized in watch) and `notional`, `overrun` and `tokens`, which are increase-only and bounded per write. These fields feed no cap.
- **Nothing** after `fx`.

**Limits of the rules:**

- **Denial of service.** k compromised concurrent runs can fill at most `k × perRun` of the global day.
- **Reports aren't checked against real usage.** The rules can't verify that `spent` matches real usage, which is D2's residual.
- **Launchers can mint tokens for arbitrary run IDs.** Each can fill its per-run cap, so a launcher can exhaust the counters (denial of service). A launcher can't spend real money, though: launchers hold no model credential.

**A budget service stays documented as later hardening.** A Cloud Run service that owns every write would remove all token and rules logic from jobs, and could host the external gateway (§11).

### 6.5 The agent registry without `onDisconnect`

- **The registry entry's life.**
  - At bootstrap, after the token exchange, the runner creates its entry.
  - A heartbeat every 15 s updates the stage, round, verify step, spend and `updatedAt`.
  - At run end the runner writes `outcomes/<day>/<slug>/<run>` and deletes its entry.
- **How watch flags a run.** It shows an entry as **silent** after 60 s without a heartbeat, and as **lost** after 3 minutes.
- **The sweeper.** The history job runs every 15 minutes (D12). It lists the project's Cloud Run executions (as `ls` does) and removes the registry entries of executions that have ended or no longer exist. It records `crashed` on their `/runs` ledgers, whose outstanding amounts stay counted, and writes an `infra_error` outcome. It also deletes the auth users of runs older than 2 days.

### 6.6 Terraform

- **The Firebase root's content:**
  - the APIs (§6.1);
  - `google_firebase_project` and `google_firebase_database_instance`, both **google-beta** (A5);
  - the `firestore` and `firebaserules` APIs. The Firestore database (Native, delete protection on) and its deny-all rules are **not** Terraform resources: `init --firebase` ensures them over REST (§6.7), with a Go test that no `google_firestore_database` exists;
  - the signer account, the `fugaroTokenMinter` role and its grants;
  - the history job's account, its job (its own image, D11) and two Scheduler jobs (`30 0 * * *` for the rollover, `*/15 * * * *` for the sweep), running as `fugaro-scheduler` (they are in the installation root, below; the rollover job posts `overrides.containerOverrides[].args = [budget history --rollover]` to the run API, assumption A4 of check 23);
  - the grants in §6.1;
  - ~~in the repository module, the jobs' environment~~ (correction R10, M9b): the jobs' environment is plain env set by `fugaro init --repo` from `internal/infra/spec.go` (`FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY`, with `FUGARO_BUDGET_MODE`, `FUGARO_MODEL_PRICES` and `FUGARO_MAX_RUN_USD` as in M9a), not Terraform variables. The history job's environment is the one Terraform-defined environment. M9b created no Firestore database and no rollover Scheduler job; M9d adds the Scheduler job `fugaro-history-rollover` (below) and the IAM of §9.
- **Marks.** The instance can't carry labels, so `init` writes `/fugaro/mark` into the database and discovery checks it. Everything else carries `fugaro=managed`, or its account display name.
- **A third Terraform root, `roots/firebase`,** with its state at `fugaro/firebase` in the installation's state bucket, which is operator-only as in §8.1.
  - **Providers.** Its providers (`google` and `google-beta`, pinned) target the FP, with `project`, `billing_project` and `user_project_override`.
  - **Inputs.** It receives, as tfvars, the installation's outputs: launchers, operators, `budget_admins`, the discovered owners and editors, the history account's email and the FP ID. There is no remote-state coupling, as in §8.1.
  - **What it holds.** Everything that lives in the FP: the APIs, the Firebase project, RTDB, Firestore, Firestore rules, the signer and the minter role, and every grant onto the FP.
- **The installation root** (gated by `enable_budget`) holds the history account, the history job and its two Scheduler jobs, and the history account's `fugaroHistory` role and its grant. Its inputs include the Firebase root's outputs (`rtdb_url`, `firebase_api_key`, `token_signer`, `firestore_database`).
- **The repository root** passes the jobs' budget environment from the same outputs. Job accounts get **no** grant on the FP.

### 6.7 `fugaro init`

- **Adoption and marks:**
  - **Discovery adopts** an existing FP's default RTDB instance and `(default)` Firestore database when they're empty or carry our mark (an unmarked database is adopted only if it has no root collection at all). It **refuses** unmarked data, and refuses a Firestore database whose location isn't the one D4 chose or that isn't Native.
  - **The Firestore step (M9d).** Read-only planning runs before the first apply (so refusals happen with nothing applied, and `--plan-only` shows it). If there is no database, the real run, after the shared confirmation, tells the person the location is permanent and requires typing `us-east5` before it creates the database (`--yes` confirms it). It then writes the Firestore mark, then deploys deny-all rules and verifies them by reading back; it never replaces an existing non-equivalent release (it refuses with instructions) and never deletes anything.
  - **After the apply,** init deploys the RTDB rules and the mark.
  - **`--budget-mode observe|enforce`** sets the mode. `--budget-admin` repeats, and fills `terraform.budget_admins`.
  - **The guard** adds the database and instance to its `prevent_destroy` list.
- **`fugaro init --firebase <fp-id>`** runs three applies, each with its own plan and confirmation:
  1. **The installation root,** which creates the history account, so the account exists before it is granted anything.
  2. **The Firebase root,** then the RTDB rules deployment and the mark.
  3. **The installation root again,** which deploys the history job with the Firebase outputs.

  Then init writes the local config. Each repository then needs `fugaro init --repo` to pick up the job environment.
- **Who runs it.** It needs the same rights as `fugaro init` on the installation, plus IAM administration on the FP (Owner, or Firebase Admin with Project IAM Admin, Service Account Admin and Role Admin).
- **Discovery on the FP** refuses a project that:
  - doesn't exist;
  - has no billing;
  - already has Firebase with unmarked RTDB data or a non-empty unmarked Firestore database;
  - has a Firestore location other than D4's.
- **Changing owners.** Re-run `init --firebase` after changing the GCP project's owners or editors, so the FP's admin grants follow them. The guard lists a removed admin grant under "⚠ Review these first".
- **Local config:**

  ```yaml
  budget:
    mode: enforce                    # off | observe | enforce
    firebase_project: my-fugaro-fp   # the Firebase project: a project of its own, or the installation's gcp_project (D3, revised)
    rtdb_url: https://<instance>.firebaseio.com
    firebase_api_key: <web-api-key>  # not a secret; restricted to identitytoolkit and securetoken
    token_signer: fugaro-token-signer@<fp>.iam.gserviceaccount.com
    unreachable_grace: 3m            # 5s to 3m: it can only shorten the default
    per_run_usd: 20                  # M9a in-process cap; with M9b the effective cap is min(this, RTDB caps)
  model_prices: {}
  ```

### 6.8 Cost at expected scale

These estimates assume 20 repositories, about 100 runs a day of about 45 minutes each, and 5 people watching for 2 hours a day.

- **RTDB.** Under 1 MB stored. Downloads come from the jobs' kill-switch streams (nearly idle), their re-reads, and the watch streams: under 2 GB a month, about $2. Concurrent connections are about 20 jobs plus a few viewers. The Spark plan's hard limit of 100 connections would make a busy fleet fail closed, so **use Blaze**, with a billing budget alert.
- **Firestore.** About 600 writes a month, which is within the free tier. The history keeps requesters' addresses indefinitely.
- **Firebase Auth.** Custom-token sign-in is free at this volume (about 3,000 users a month, deleted after two days).
- **History job.** About 3,000 short executions a month, a few dollars at most.
- **Scheduler.** Two more jobs, $0.20 a month.
- **Total:** under $10 a month, billed to the FP's billing account; the history job's executions bill to the project's GCP project. A new project costs nothing by itself.

---

## 7. `fugaro watch`

- **Bubble Tea** (D13), with `lipgloss`, `bubbles` and `teatest` for golden frames. These are pinned and reviewed as new dependencies.
- **Data comes from four event streams** under the viewer's IAM token (A2): `/config`, `/spend/<today>/global`, `/spend/<today>/repos` (both re-subscribed at UTC midnight; the per-run day shares are not needed) and `/agents`. After more than 45 s without any event, keep-alives included (the server sends one about every 30 s, so 30 s would flap), the header shows `⚠ live data stale`. *(Amended 2026-10-03: was three streams and 30 s.)*
- **Layout.** This follows the spec: a global line, then each repository with its cap bar and burn rate, then its runs with stage, round, models, spend and age.
  - A killed repository shows `KILLED by <who> <time> "<reason>"`.
  - `oauth` runs show their spend as notional.
- **Burn rate** is computed client-side over a rolling 5-minute window of `spent` (heartbeats report it every 15 s; `counted` jumps by whole leases, so its slope is lumpy; *amended 2026-10-03*), and highlighted above `watch.burn_alert` (default: the daily cap spread over 8 hours).
- **Stuck runs.** A run is flagged silent or lost (§6.5), or amber past 80% of its stage deadline. `l` cross-checks a lost run against Cloud Run executions.
- **Keys.**
  - `k` kills everything, after you type `kill`.
  - `K` kills the selected repository, after you confirm with `y`.
  - `r` and `R` resume, after you type the repository name, or `resume` for global.
  - Writes use the viewer's IAM. Without admin rights, the key shows "you are not a budget admin".
- **Untrusted text.** Every job-written field is stripped of control and escape characters and clipped, so a compromised run can't inject terminal escapes.
- **M9c's settled details** (2026-10-03) are in [the M9c plan](../plans/2026-10-03-m9c-fugaro-watch.md): reconnect and polling fallback, the `stale` and `outage` displays, stuck thresholds, narrow terminals, `--json` and plain output, the kill and resume flow, the model structure and tests. `fugaro ls --watch` stays. The registry's live shape has no `round` or `spent` until a run reports them; watch shows `-` for an absent field. `/config` is readable by viewers over IAM (the rules deny every token read of `fugaro/*`, but IAM bypasses rules).
- **Without Firebase,** watch polls `ls` data every 10 s: runs grouped the same way, with each run's cost from `result.json`, no caps, and the kill keys disabled.

## 8. `fugaro budget`

| Command | Behaviour | Needs |
|---|---|---|
| `budget show [--repo R \| --all] [--json]` | Caps, today's counted and spent, notional spend, kill switches, headroom and active runs | Viewer |
| `budget set (--global \| --defaults \| --repo R) [--daily USD] [--per-run USD] [--max-reserve USD] [--clear]` | An ETag-guarded `PUT` of the cap node, showing old and new values. **Raising** a cap needs typed confirmation (or `--yes`); lowering one doesn't | Admin (D6) |
| `budget kill (--all \| --repo R) [--reason TEXT]` | Sets the switch, recording who, when and why. Takes effect within seconds | Admin |
| `budget resume (--all \| --repo R)` | Clears the switch, after typed confirmation | Admin |
| `budget prices [--json]` | The effective price table, with sources and check dates. Warns when a check date is over 90 days old | None |
| `budget history --rollover \| --sweep` | The history job's modes (hidden) | History account |

Caps must be finite, non-negative and at most $100,000, and a per-run cap can't exceed the daily cap in the same scope. An admin is someone Terraform granted `roles/firebasedatabase.admin` on the FP (§6.1): the GCP project's owners and editors, plus `terraform.budget_admins`.

## 9. History and `fugaro report`

**Built in M9d** (plan: [2026-10-03-m9d-history-and-report.md](../plans/2026-10-03-m9d-history-and-report.md); v1.md §6.1 and §9.1 describe the behaviour). Unverified against the real services until check 23 in [gcp-live-checklist.md](../gcp-live-checklist.md) is run.

- **The rollover** is `fugaro budget history --rollover`, run at 00:30 UTC by the Scheduler job `fugaro-history-rollover` against the history Cloud Run job (the 15-minute job stays the sweep). It handles every day before today that is still in the database, oldest first (so a long outage strands nothing), and per day:
  1. derives each repository's record from `spend/<day>`, `outcomes/<day>` and the next day's outcomes (a run started on D-1 is counted on its start day), the run ledgers and the `result.json` objects of runs started that day (for compute, an estimate; unreadable ones are `n/a`, not 0);
  2. writes `spendDaily/<date>_<slug>` conditionally on the document's `updateTime` (`MustNotExist` on create, up to 4 re-reads on a conflict), **provisional** while D is today or yesterday and **final** from 00:00 UTC of D+2; an identical provisional document is not rewritten; a final one is never rewritten, and never made provisional; `--rollover --day D --force` rewrites one (a backfill);
  3. prunes the day's RTDB nodes only under the preconditions in v1.md §6.1: older than 8 days, final, the previous day already gone, the global counter equal to the sum of the documents, each document equal to a fresh derivation, each node equal to its snapshot; one atomic leaf-null `PATCH`, with every path checked. A refusal is exit 1 and leaves RTDB untouched.
  A failure on one day skips that day's prune and the others continue; exit 1 refusal, 2 backend failure. With no Firestore database it warns, exits 0 and touches nothing (the job exists before `init --firebase` creates the database).
- **What the history account may do:** read and write Firestore (`roles/datastore.user`, the Firebase project only), call the API on that project's behalf (`serviceUsageConsumer`), read the runs bucket (`storage.objectViewer`, no condition), plus its M9b rights on the database. Nothing else is new: no `actAs`, no domain or wildcard member.
- **Unreconciled spend.** Crashed runs' outstanding amounts go into `unreconciledMicros`, and gateway `overrun` into `overrunMicros`, both on the ledger's last-share day.
- **`fugaro report [--by day|week|month|year|repo|model|person] [--since D|Nd] [--until D] [--repo R] [--csv | --json]`.**
  - It reads Firestore with the viewer's IAM (a single-field `date` range, no composite index; the repository filter is client-side), and computes the days that have no final document from RTDB (within its 8-day window, plus today), marked `(partial)`.
  - Weeks are ISO weeks in UTC. Totals keep model dollars, `NOTIONAL~` and compute separate and never sum them; compute is `n/a` when no run was estimated.
  - `--by model` shows the models (the coder/reviewer split is not stored); `--by person` the requester address, `unknown` for unattributed spend. There is no `--all`, `--week`/`--month` shortcut or person/model filter; the default range is 30 days.
  - Without Firestore history it falls back to run-record totals from the runs bucket and says so (degraded); a permission error names `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`.
- **Not built:** an organization-wide export of `spendDaily` (see §18).

## 10. One account of cost

| Figure | Source of truth | Also shown in |
|---|---|---|
| A run's model dollars (`api-key`, `vertex` with the budget on) | The gateway's per-run ledger | `result.json` `cost.model_usd` (`model_source: gateway`), the PR report, `ls`, and `/runs/<slug>/<run>.spent` |
| Model dollars without a gateway (`oauth`, or the budget off) | Claude Code's `total_cost_usd` | The same places, with `model_source: claude-code`. `oauth` stays `model_basis: subscription` (notional) |
| Dollars counted against caps | RTDB `counted` (spent plus outstanding) | watch, `budget show` |
| History | Firestore, copied from RTDB | `report` |
| Compute | An estimate, as in §10.1 | Unchanged, plus `computeUsd` in the history. Never capped |

`ls` totals and `report` differ only by unreconciled and overrun amounts, which `report` shows separately. `cost_usd` remains the model figure.

## 11. Threat model

The attacker is a compromised agent in run A. It runs as the same user as the runner, can reach the metadata server, and has unrestricted network egress. It can read A's Firebase ID token and refresh token from the runner's process, and the custom token too if it gets there before the runner deletes it at bootstrap.

| Question | Answer |
|---|---|
| Read other repositories' budgets or registry entries? | **No.** The rules scope reads to its own slug, global and itself (§6.4) |
| Lift a cap or clear a kill switch? | **No.** `/config` is IAM-admin only |
| Lower shared counters or other runs' counters? | **No.** It can only release its own outstanding reservation (denial table rows 1–3 and 8) |
| Exhaust the daily caps (denial of service)? | **By at most its per-run cap** (e.g. $20 of a $150 global day) |
| Mint tokens for other runs? | **No.** No job account holds `signJwt` |
| Steal another run's token? | Only a queued run of the **same repository**, during its queue window (the bucket object). The same per-repository boundary as §6.1 |
| Spend money around the gateway? | **Yes. This is D2's accepted residual.** The API key is in the runner's environment; on Vertex, the metadata server's token works directly |
| Inject into watch? | No. The text is sanitized |
| Claim another project's name in `fugaro.yaml` | Its own runs then refuse at bootstrap. The name gives no access: IAM and the tokens do (§2.5) |
| A compromised history job, or its image | It holds `firebasedatabase.admin` on the FP, so it **could** lift caps or clear switches. This is mitigated by: its own distroless image, pinned by digest in the base registry (which only operators write); no model credential; and no agent code in the job. It is the most privileged new identity M9 adds |

**Backstops now:**

- **`api-key`:** one Anthropic Console workspace per repository, each with its own spend limit (A7), which the provider enforces.
- **`vertex`:** per-model quotas plus the existing billing-budget alerts.

**Later hardening (D2):**

1. A gateway sidecar for `api-key`, so the agent's container never holds the key (A8).
2. **An external gateway:** a budget service that proxies model traffic and owns every Firebase write. It replaces the per-run tokens and would make the caps hard for both API-key and Vertex, since job accounts would then need neither the key nor `aiplatform.user`.

## 12. Failure modes

| Failure | Behaviour |
|---|---|
| RTDB unreachable with runs in flight (the "budget backend", D14) | The gateway spends the lease it already holds, which is already counted. A top-up or heartbeat retries with backoff for **3 minutes**, then the run **halts** (`budget_unavailable`) with a draft PR. This applies to **all auth modes**: `oauth` runs halt too |
| Unreachable at bootstrap | The token exchange or the first registry write retries for 3 minutes, then the run halts with outcome `none`, exit 0 |
| Unreachable at launch | `fugaro run` exits 2. `--no-budget-check` leaves it to the runner |
| Identity Toolkit unreachable at bootstrap | The same as RTDB being unreachable |
| Custom token older than 1 h at bootstrap (queued too long) | Halted, `budget_token_expired`, outcome `none`. The message says to launch again |
| **Image checks and rebuilds, `verify`, finalize, writeback** | **Unaffected.** None of them makes model calls or uses the budget |
| Contention (8 stale retries) | Treated like an unreachable backend, with the same grace |
| Hard kill (a task timeout or out-of-memory) | Nothing is released. The sweeper records `crashed` and `infra_error`, and the amount stays counted, erring high |
| Firestore down during the rollover | The rollover exits 2 and prunes nothing for the day; the next night redoes every day still in the database (RTDB keeps each day until its documents are final, equal and 8 days old) |
| Clock skew | The rules use the server's `now`. The client takes the server's time from the `Date` header |
| Price table stale | Check dates, a warning after 90 days, and the Claude Code cross-check |
| Watch loses its stream | A header warning and reconnection. Nothing in the cloud is affected |

## 13. Rollout: no compatibility layer

**M9 carries no backward compatibility, by design** (D18). Fugaro is pre-release, and its only deployment is the user's own project: a sandbox repository and one web application repository in one GCP project. Adjusting those two once costs far less than dual-shape code, transition releases and the tests that would pin them. So:

- **The runner and CLI understand only the new shapes.** Strict decoding rejects old local configs, job environments and `fugaro.yaml` files, each with a message that points at §13.1.
- **The additions to `result.json` are optional fields.** Records already in the bucket still decode as they are, with no special code.
- **The budget modes are features, not compatibility.** `off` (no `budget:` block) runs without the gateway or Firebase, and `observe` calibrates caps. Fail-closed behaviour (D14) is unchanged.
- **Budget rollout,** once M9b ships:
  1. Create the FP and link billing (§6.0), then run `fugaro init --firebase <fp-id> --budget-mode observe`.
  2. Set generous caps.
  3. Watch a week of `report --by model` and `budget show`.
  4. Set the real caps and switch to `enforce`.
- **Rollback.** `--budget-mode off` and re-init. The data is kept (`prevent_destroy`).

### 13.1 One-time migration of the existing installs

This lands with task 0. The controller runs it, and takes the user's confirmation before each step that changes the cloud or a repository. **Freeze launches for the duration.** Between an image rebuild and the matching `init --repo`, a new runner meets an old job environment. It refuses at bootstrap without writing anything, but the run is wasted.

1. **The local config, by hand.** This is the simplest route: the new binary can't read the old file, and the cloud has no name yet.
   - `mkdir -p ~/.config/fugaro/projects`.
   - Copy `config.yaml` to `projects/<slug>.yaml`.
   - Rename its `project:` key to `gcp_project:`, add `name: <slug>`, and keep the old file as `config.yaml.bak`.
2. **`project: <slug>` in each repository's `fugaro.yaml`,** on its base branch. `init --repo` needs it, so this comes before the re-init.
   - **The sandbox:** the controller commits it, as before, with the user's OK.
   - **The web application repository:** the user's own PR, merged.
3. **The base image.** Build and push the new base with the operator's image scripts, into `fugaro-base`, and set `base_images.<kind>` (`fugaro init --base-image <tag>`).
4. **The installation.** Run `fugaro init --name <slug>` against the project. It sets the canonical name (output, bucket label, `fugaro_project` tfvar) and rewrites `projects/<slug>.yaml` from the outputs. Check that its `name:` is unchanged.
5. **Each repository, from its checkout:** `fugaro image build`, which builds the derived image on the new base, then `fugaro init --repo`. That writes the new job environment (`FUGARO_PROJECT=<slug>`, `FUGARO_GCP_PROJECT=<id>`) and points the jobs at the new `:latest`. The check job follows.
6. **Verification.**
   - `fugaro ls` prints `project: <slug> (GCP <id>)`, and both repositories' runs are listed.
   - `gcloud run jobs describe` on one job of each repository shows the two variables.
   - The runs bucket carries `fugaro_project=<slug>`.
   - `fugaro validate` passes in both checkouts.
   - Outside a checkout with no `--project`, a command refuses when there are several project configs, and works when there's exactly one.
   - A sandbox run ends with a ready PR (live check 13).
   - The web application repository gets a real run only with the user's go-ahead and task text.

## 14. Testing strategy

- **Gateway.** Tested against an `httptest` upstream with scripted server-sent events:
  - every row of §5.5, plus pings, errors mid-stream, 429 and 529, non-streaming responses, Vertex paths, `count_tokens` and fallback models;
  - byte equality (no rewriting) and no buffering;
  - a property test that `used ≤ granted` at every step.
- **Pricing.** Golden tests per model: cache multipliers, tiers, web search, the maximum-rate fallback for unknown models, and overrides.
- **Rules.** Tested against the **Firebase RTDB emulator** in CI, pinned `firebase-tools` behind a `firebase` build tag, and driven from Go over REST with unsigned emulator ID tokens that carry `fs`, `fr` and `fx` (A13):
  - every row of the denial table (§6.4) as a named test;
  - the allowed paths: reserve, report, release, notional, heartbeat, registry;
  - the observe-mode rules;
  - `TODAY` at midnight, using the emulator's clock where it has one, else checked through the generated expression;
  - a concurrency test: 50 goroutines leasing against 3 caps never exceed any of them, and stale writes are denied and retried.

  The generated rules also have a golden file.
- **Client and runner logic.** Tested against an **in-process RTDB fake** in `internal/gcpfake`. It implements the REST subset, ETags, multi-path atomicity and server-sent events, but not rules: rules are the emulator's job. The runner cases:
  - a halt mid-implement leaves a draft PR, `halted` status and the report;
  - a halt at bootstrap gives `none` and exit 0;
  - a kill during review is seen through the stream;
  - a kill during finalize is ignored;
  - an unpinned model makes the stage fail;
  - an `oauth` token cap is enforced;
  - an expired custom token halts the run;
  - the token object is deleted after it's read;
  - the agent's environment has no real key (secret scan);
  - managed settings are in place before the first stage.
- **Minting.** `signJwt` goes through `gcpfake/iam`. The tests check the claims, and that `--retry` mints a fresh token.
- **Firestore.** Behind a `SpendStore` interface, with an in-memory fake. The emulator runs under the `firebase` tag.
- **CLI.** `budget` against the fake. `watch` golden frames: grouping, burn rate, silent and lost runs, kill confirmation, hostile titles, degraded mode. `report`. `ls` and `diagnose` with `halted` runs.
- **Terraform and init.** `validate` and `tflint` with google-beta, tfvars golden files, and discovery and adoption against `gcpfake`.
- **Live checks** (additions to `gcp-live-checklist.md`):
  - a week of observe mode;
  - a $0.50 per-run cap halting a real run;
  - a global kill during a run;
  - a token expiry test;
  - a rollover document written;
  - watch against the real RTDB;
  - a rules smoke test, in which a real token is denied a write to another run's ledger.

## 15. First-cut task breakdown

Sizes: **S** is up to a day, **M** a few days, **L** about a week.

**Prerequisite (before M9a, or as its first task)**

0. Project identity, project configs and the D17 renames (§2.4–§2.6, D16–D18). **M** (was L before D18 removed the transition layer)
   - The canonical name: `init --name`, the output, the bucket label, `FUGARO_PROJECT` (the name), and the immutability checks.
   - `project:` in `fugaro.yaml`, required: the schema, loader, `validate`, example and the setup skill, and `init --repo`.
   - The runner's base-branch check.
   - Project configs: `projects/<name>.yaml` with `name:` and `gcp_project:`, the name checked against the cloud, selection from the checkout, `--project` and `FUGARO_PROJECT`, refusing ambiguity, the project header on every command and in `--json`, and no old-config reading.
   - The D17 renames (§2.6), a straight replacement with strict decoding: `--gcp-project`, `gcp_project:`, `FUGARO_GCP_PROJECT` and `FUGARO_PROJECT`, `FUGARO_LIVE_GCP_PROJECT`, the Terraform test fixtures and the M4 golden, and the docs (gcp-setup, the live checklist, v1 §3.4, §5.4 and §9.1, the skills).

**M9a: gateway, pinned models, per-run cap, halted (no Firebase)**

1. `internal/pricing`: the table, aliases, tiers and overrides. **M**
2. `internal/gateway`: the proxy, the usage tee, the worst-case estimate, a static local lease from `FUGARO_MAX_RUN_USD`, the allow-list and the Vertex paths. **L**
3. The agent environment for each auth mode with the gateway, and managed settings (A6). **M**
4. `fugaro.yaml`: `agent.models`, `max_output_tokens`, `max_run_tokens`, pinning validation and the pin variables. **M**
5. The runner: `ErrHalted`, the `halted` status and `halt` block, the report, the exit code, `runview`, the schema, and the `oauth` token cap. **M**
6. The `cost` fields `model_source` and `model_by`, and the cross-check. **S**
7. The local config's `budget.mode` and `per_run_usd`, and `model_prices`, through the tfvars into the jobs' environment. **S**
8. Tests and docs (v1 §4.5, §4.6, §5, §10). **M**

**M9b: Firebase counters, daily caps, kill switches, `fugaro budget`**

9. `internal/rtdb`: the REST client (ETags, multi-path `PATCH`, server-sent events, both kinds of authentication) and the `gcpfake` RTDB. **M**
10. Minting (`signJwt` through the signer), the token object, the exchange and refresh at bootstrap, and redaction. **M**
11. The rules generator and its templates, and the emulator test suite with the denial table. **L**
12. Lease top-ups with rules as compare-and-set, the local classification of refusals, usage reports, releases, the kill-switch stream, heartbeats and the registry. **L**
13. `fugaro budget show/set/kill/resume/prices`, and the launch pre-check. **M**
14. Terraform for the Firebase root and state (§6.6). The `init --firebase` flow: discovery and adoption of the FP, the owner and editor discovery, the three applies, the rules deployment, the mark, and the local config. **L**
15. The history job's image (D11), its sweep mode, and its Scheduler job. **M**
16. Live checks and docs. **M**

**M9c: `fugaro watch`**

17. The TUI, its streams, burn rate, stuck detection, kill keys, sanitizing, degraded mode and golden frames. **L**

**M9d: history and reports**

18. The rollover mode (Firestore writes, pruning, compute from `result.json`) and its Scheduler job. **M** (built, M9d)
19. `fugaro report`. **M** (built, M9d)

**M9f: task loop (optional)**

20. The verify gate before review and `agent.verify_retries`. **M**
21. Structured findings per round in the final report and `result.json`. **S**

**Last: the one-time migration** (lands with task 0)

22. Migrate the existing installs (§13.1), run by the controller with the user's confirmations: the local config, `project:` in both repositories, the base and derived images, `init --name`, `init --repo`, and verification. **S**

---

## 16. Decisions (settled 2026-09-30)

| # | Decision | Chosen | Consequences |
|---|---|---|---|
| D1 | Who writes budget state | **Per-run Firebase custom tokens, minted by the launcher, bounded by database rules.** No budget service; only a scheduled, admin-privileged history and sweeper job | Rules carry the security (§6.4) and need the emulator in CI. Launchers need `signJwt` on the signer. A compromised run can fill up to its per-run cap (denial of service). A budget service stays as later hardening |
| D2 | How hard the caps are | **A guardrail now. An external gateway later** | A compromised agent can spend around the gateway. The backstops are provider-side (§11) |
| D3 | Where Firebase lives (**revised, user ruling 2026-10-04**) | **One FP per Fugaro project, and it may be the installation's own GCP project or a project of its own.** The user creates the project (if separate) and links billing; `fugaro init --firebase` adopts it. V3 (one FP for several Fugaro projects) stays rejected. *Superseded text:* "a dedicated FP per Fugaro project, never the installation's GCP project (V1; V2 rejected)" | "Global" is the Fugaro project. A third Terraform state either way; the two roots enable no API twice (`skip_apis`). **Old rationale, kept:** a second project meant a compromised job's account had no IAM at all on the budget. **New boundary:** this repository's Terraform grants no job, build or scheduler account a role that reaches the backend (tested), the signer has no roles, and the rules bound a run's token; it does not cover default service accounts with `roles/editor` (the Cloud Build pivot), per-project token acceptance or hand-made grants: residual risk in §6.0. Organization-wide aggregation comes later and read-only |
| D4 | Regions | **RTDB `us-central1`. Firestore `us-east5`** (to be confirmed supported by check 23; no `nam5` fallback is built) | RTDB can't be moved later. About 25 ms from `us-east5` jobs, which is negligible next to model latency |
| D5 | The budget day | **UTC** | Epoch-day keys. No days of 23 or 25 hours |
| D6 | Budget admins | **The GCP project's owners and editors, plus an optional `budget_admins` list.** Terraform grants them `firebasedatabase.admin` on the FP | Launchers, operators and job accounts can't change caps or switches. The owner list is discovered at `init`, so re-run `init --firebase` after changing owners |
| D7 | What caps count | **Model dollars only** | Compute is reported, never capped |
| D8 | Unpinned models | **Rejected. The stage fails** | No rewriting of request bodies. A misconfiguration shows up as `failed`, not `halted` |
| D9 | A halt before the branch exists | **`halted`, no PR, exit 0** | Keeps policy stops out of the infrastructure-error signals |
| D10 | Coder models | **Claude models only.** Non-Claude coders are deferred | A small Claude model can serve as a cheap coder |
| D11 | Packaging | **The gateway is in the `fugaro` binary in the base image. The history job has its own small image** | The gateway reaches a repository through a derived-image rebuild. The history image is built by `init` until M7 publishes images |
| D12 | The registry | **Heartbeats, plus a sweeper cross-checked against Cloud Run executions** | The history job sweeps every 15 minutes |
| D13 | The TUI library | **Bubble Tea** | The first TUI dependency |
| D14 | Budget backend unreachable | **Halt after a 3-minute grace, for every auth mode and every budget mode, `observe` included.** Image checks and rebuilds are unaffected | An outage of RTDB or Identity Toolkit stops even `oauth` runs and observe runs. `off` is the only mode that ignores the backend |
| D15 | The PR flow (**revised 2026-10-01**) | **A DRAFT PR opens after the first implement stage passes verification (the first push); its description is updated at each stage boundary; it flips to ready at the end.** A halt before the branch exists still opens no PR (D9). A halt after the first push leaves the draft with a `halted` comment. The sweeper marks the stale drafts of crashed runs. The structured review format still goes into the final report (no per-round comments) | Milestone M9e touches `internal/runner` (finalize, `EnsurePR` by number) and the `internal/gitprov` adapters. A live test must confirm that a Bitbucket draft does not notify the assigned reviewer. Was: PRs opened at finalize |
| D16 | Project identity | **Each repository's `fugaro.yaml` names its project (`project: <slug>`)**, required by `init --repo` from M9 on and checked by the runner against the job's `FUGARO_PROJECT`. `fugaro init --name` sets the name once, in the cloud setup; project configs copy it; `fugaro use` is dropped. Renaming is unsupported in M9 | Mistakes can't cross projects. Existing repositories need one PR each, plus `init --repo`. The name is a label, not a boundary |
| D17 | One word | **"Project" means only the Fugaro project, everywhere.** `--project <name>`, `FUGARO_PROJECT=<name>`, `projects/<name>.yaml` with `name:`; no profiles. The GCP ID becomes `--gcp-project`, `gcp_project:` and `FUGARO_GCP_PROJECT`. Terraform's `project` variable is the one exception | Breaking CLI, config and environment renames, applied directly (D18). `--gcp-project` can't override a selected project |
| D18 | Compatibility | **None.** The runner and CLI understand only the new shapes, and strict decoding rejects the old ones with a clear message. The two existing installs are migrated once (§13.1) | No dual-shape code, no transition releases. Old configs and jobs fail loudly until migrated. Launches freeze during the migration |
| D19 | Where the `belong` installation lives (**planned, user, 2026-10-02; simplified 2026-10-04**) | **Move everything under `fugaro-belong`.** Today the installation (Cloud Run jobs, runs bucket, registries, secrets, Scheduler, Terraform state) is in the shared dev project `edge-devel-dimi`, and the budget backend is in the Firebase project `fugaro-belong` (created 2026-10-02, billing linked). The intent is for the Belong Fugaro project to have its own GCP project, `fugaro-belong`, for everything, and for `edge-devel-dimi` to go back to being a throwaway dev project | **Not scheduled; M9b ships with the two-project layout, which stays supported.** The D3 question that blocked this is settled (revised 2026-10-04): the same-project layout is supported, so **no second project (`fugaro-belong-run`) is needed**. Doing it needs: (1) a fresh `fugaro init --name belong --gcp-project fugaro-belong` (no migration code, D18): new bucket, registries, secrets (Bitbucket tokens, the oauth token), image builds, and re-onboarding EdgeWeb and the sandbox (`init --repo`, a PR each if `fugaro.yaml` changes), then `init --firebase fugaro-belong`; (2) retiring the resources in `edge-devel-dimi` once the new installation has run. **Caveat for belong's existing backend:** the Firebase root's resources (signer account, `fugaroTokenMinter`, the API key, the RTDB instance) already exist in `fugaro-belong` under the old state in `edge-devel-dimi`'s state bucket, and the database's and Firestore's marks name `gcp_project: edge-devel-dimi`, which init refuses to change. A move therefore has to carry the `fugaro/firebase` state to the new bucket (`terraform state` copy) and rewrite those two marks by hand, or start from an empty backend. Natural moment: with the M7 release or a first real Belong rollout |
| A1 | `oauth` and Anthropic's terms | **The user accepts the risk.** `oauth` stays as it is and is never proxied. Dollar caps apply to API-key and Vertex only | §5.8 lists exactly what `oauth` runs get |

## 17. Assumptions to verify before building

| # | Assumption | Blocks | If false |
|---|---|---|---|
| A2 | RTDB REST accepts a user's ADC token (cloud-platform scope), or needs the `firebase.database` and `userinfo.email` scopes | M9b (`budget`), M9c | Document a login with those scopes, or use `gcloud auth print-access-token --scopes` |
| A3 | Requests that fail before `message_start` are not billed, and interrupted streams bill the tokens already generated | M9a (reconcile table) | Charge input tokens on failures |
| A4 | A REST multi-path `PATCH` at the root is atomic, and its rules are checked against the fully merged `newData` (reachable through `parent()`) | **M9b** (the whole lease design) | Rolling per-day nodes with a single-location ETag `PUT`, and repository-partitioned counters. The global cap would then need a budget service |
| A5 | `google_firebase_project` and `google_firebase_database_instance` are google-beta only | M9b (Terraform) | Create the instance through its REST API in `init` |
| A6 | Claude Code's managed settings `env` overrides the project's `.claude/settings.json` `env` | **M9a hardening** (the Vertex bypass) | Refuse repositories whose settings set `ANTHROPIC_*` or `CLAUDE_CODE_*` |
| A7 | Anthropic Console offers a spend limit per workspace | M9b docs (the backstop) | Document alerts only |
| A8 | Cloud Run jobs support sidecars with a secret mounted into one container only | Later hardening | Drop the sidecar option |
| A9 | `max_tokens` never exceeds `CLAUDE_CODE_MAX_OUTPUT_TOKENS`, and usage fields are as documented through Vertex `streamRawPredict` | M9a | Reserve the model's maximum output |
| A10 | Every billed call Claude Code makes goes through the base URL. The documented direct calls (fast-mode availability, the WebFetch domain check) are free | M9a | Budget the difference as per-stage overhead |
| A11 | The embedded prices are current (per-model cache-read multipliers, long-context tiers) | M9a | Update them. Overrides cover the gap |
| A12 | Custom tokens are valid for at most 1 h, their custom claims appear in `auth.token`, any account in the FP can sign them, and ID tokens refresh until the user is deleted | **M9b** | Only the design of `fx` and delivery changes |
| A13 | The RTDB emulator enforces rules like production, and accepts unsigned tokens with custom claims over REST | M9b (the rules tests) | Run the rules tests against a dedicated test FP instead |
| A14 | `'' + <integer arithmetic>` in the rules yields the plain decimal day number, so `TODAY` works | **M9b** | Use a rolling current-day node whose rollover must archive the old values (enforced by rules) |
| A15 | Identity Toolkit sign-in with custom tokens needs no sign-in provider enabled, and costs nothing at this volume | M9b | Enable the provider, and check whether Identity Platform charges per monthly active user |

## 18. Suggested milestone split

- **Prerequisite: project identity, project configs and the renames** (§2.4–§2.6, task 0, D16, D17), so every repository, job and command names the project it belongs to before caps and kill switches exist.
- **M9a: gateway, pinned models, per-run cap, `halted` (no Firebase).** Exact accounting per call, pinned models per stage, no real key in the agent's environment, a per-run cap enforced in-process, the `oauth` token cap, and the new status. It is useful on its own.
- **M9b: Firebase counters, daily caps, kill switches, `fugaro budget`.** The dedicated FP per Fugaro project (`init --firebase`), per-run tokens and rules, leases, observe mode, the registry and the sweeper.
- **Later, read-only: organization-wide roll-up.** Each project's history job exports `spendDaily` to one shared place. There is no shared live state.
- **M9c: `fugaro watch`.** Only needs M9b's data.
- **M9d: Firestore history and `fugaro report`.** Built; independent of M9c. Live bring-up pending (check 23).
- **M9e: draft PR at the first push (D15, revised).** Small. Finalize and the provider adapters; see the reconciliation doc.
- **M9f (optional): the verify gate and structured findings.**
- **Later:** the external gateway or budget service (D2 hardening), non-Claude coders (M10, [reconciliation](m9-spec-v3-reconciliation.md)), per-round comments, the redundant mode, per-batch concurrency, and letting launchers use the kill switch.
