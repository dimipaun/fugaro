# M9a — Project Identity and the Model Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every repository, job and command names the Fugaro project it belongs to, so nothing can act on the wrong one. On top of that, `api-key` and `vertex` runs send every model call through a gateway inside the runner. The gateway allows only the models pinned for the stage, prices each call from an embedded table, reserves the worst case before the call and settles it from the stream afterwards, and halts the run at a per-run dollar cap. The agent's environment no longer holds the real API key. `oauth` runs are never proxied: they get pinned models and a token cap checked at stage boundaries. A new run status, `halted`, carries the stop into `result.json`, the report, `ls`, `diagnose` and the exit code. There is no Firebase in M9a.

**Architecture:**

```
 laptop                                                          cloud (one Fugaro project)
 ─────────────────────────────────────────────────────────────   ──────────────────────────────────────────
 fugaro <cmd> [--project N | FUGARO_PROJECT=N] [--config F]      runs bucket  fugaro/project.json {name, gcp_project}
   pick the project config (checkout's project: wins):              labels     fugaro=managed, fugaro_project=<name>
     ~/.config/fugaro/projects/<name>.yaml {name, gcp_project, …}  installation outputs  project_name
   stderr: "project: aurora (GCP <id>)"                          every job's env  FUGARO_PROJECT=<name>
   name == fugaro/project.json (cached one day)                                   FUGARO_GCP_PROJECT=<id>
                                                                                  [FUGARO_BUDGET_MODE,
 fugaro init --name <slug>   (once; immutable)                                     FUGARO_MAX_RUN_USD,
 fugaro init --repo          (fugaro.yaml project: must match)                     FUGARO_MODEL_PRICES]

 fugaro exec (Cloud Run job)
   bootstrap: claim · budget env parsed · clone · checkout · fetch base
     · project: on origin/<base> (and <ref>) == FUGARO_PROJECT, else infra_error
     · api-key/vertex, enforce, no cap → halted/none, exit 0 (nothing pushed or locked)
     · lock · … · pins valid for the budget · repo .claude settings don't reroute
     · start gateway 127.0.0.1:<port> (api-key/vertex, budget on) · agent env: gateway URL + per-run token, no real key
   each stage: pins for its role (--model, ANTHROPIC_DEFAULT_*, CLAUDE_CODE_SUBAGENT_MODEL, max output)
     → /etc/claude-code/managed-settings.json and the process env · gateway.BeginStage
     claude -p ──► gateway ──► api.anthropic.com | Vertex AI
                   unpinned model → 400, stage failed (D8)
                   reserve worstCase ─ cap hit → 403 x-should-retry:false, halt (run_cap)
                   stream through unbuffered, tee usage → settle actual
   gateway halt → wait for claude to exit at the call boundary (≤ 60 s) → else SIGTERM
   oauth: after each stage, Σ result.usage ≥ agent.max_run_tokens → halt (token_cap)
   finalize: halted → draft PR with the halted report (never ready); stopped before a push → outcome none
```

- **Task 0 of the design comes first, and in full.** The design's §15 puts project identity before M9a. This plan makes it Tasks 1–3 (lane A) and runs the gateway's new packages (Tasks 4–5, lane B) beside them, since they share no file.
- **No compatibility layer (D18).** The CLI reads only `projects/<name>.yaml` with `gcp_project:` and `name:`. The runner reads only `FUGARO_GCP_PROJECT` and `FUGARO_PROJECT`. `fugaro.yaml` without `project:` is invalid. Old shapes fail loudly, and the one-time migration (Task 12, §13.1) moves the two existing installs.
- **The gateway is off unless the owner turns it on.** The budget lives in the project config (`budget.mode`, `budget.per_run_usd`, `model_prices`), reaches the jobs through `fugaro init --repo`, and is off by default. With it off, runs behave as today, apart from the pins.

**Tech stack:** Go 1.27. No new Go module: the gateway is plain `net/http` with its own forwarding (no `httputil.ReverseProxy`, whose buffering and header rewriting we'd have to undo); Vertex tokens use `golang.org/x/oauth2/google`, already a dependency. Terraform: one new variable (`fugaro_project`) in the installation and repository roots and modules, one bucket label, one bucket object and one output. One base-image change: `/etc/claude-code` owned by the `fugaro` user.

**Spec:** [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md). Read these first:
- §2.1 (per-stage models), §2.2, §2.4–§2.6 (project identity, `project:` in `fugaro.yaml`, the D17 renames), §5 (the gateway: §5.1 agent environment and managed settings, §5.2 pinning and prices, §5.3 level 1 only, §5.4 worst case, §5.5 reconciliation, §5.6, §5.7 modes, §5.8 `oauth`, §5.9 `halted`), §10, §11, §12 (the rows that don't need Firebase), §13 and §13.1 (the migration), §14, §15 (tasks 0–8 and 22), §16 (D1–D18, A1), §17 (A3, A6, A9–A11), §18.
- [v1.md](../design/v1.md): §3.4 (execution identity), §4.1 (bootstrap order), §4.2 (the PR outcome rule), §4.5 (cancel), §4.6 (the run record), §5.1, §5.4, §6.1 (the trust boundary), §9.1, §10.1 (cost).
- [gcp-live-checklist.md](../gcp-live-checklist.md) (guardrails, check 13) and [gcp-setup.md](../gcp-setup.md).

The M6 plan ([2026-09-30-m6-follow-up-runs.md](2026-09-30-m6-follow-up-runs.md)) explains the follow-up bootstrap that Task 3 extends, and the M5 plan ([2026-09-29-m5-infrastructure.md](2026-09-29-m5-infrastructure.md)) `fugaro init`, its discovery and its Terraform roots.

**What M6 left ready, and what is missing:**

| Area | Ready | Missing |
|---|---|---|
| Local config | one `config.yaml` (`$FUGARO_CONFIG`, `$XDG_CONFIG_HOME`), strict decoding, `project:` = GCP ID, `Override(project, region)` | `projects/<name>.yaml`, `name:`, `gcp_project:`, selection, `budget:`, `model_prices:` |
| CLI | `--config`, `--project` (GCP ID), `--region` on every cloud command (`addCloudFlags`) | `--gcp-project`, `--project <name>`, `FUGARO_PROJECT`, the header, `project` in `--json` |
| Installation | outputs, bucket label `fugaro=managed`, discovery by marks | `--name`, `fugaro_project` variable, label, `fugaro/project.json`, `project_name` output, immutability |
| Job env | `FUGARO_PROJECT` = GCP ID (`infra.platformEnv`), read by `backend.ExecutionFromEnv` and the check job | `FUGARO_GCP_PROJECT`, `FUGARO_PROJECT` = name, the budget env |
| `fugaro.yaml` | `agent.model`, `agent.max_budget_usd`, strict decoding, schema, example | `project:`, `agent.models`, `agent.max_output_tokens`, `agent.max_run_tokens` |
| Runner | `ErrCancelled` cause pattern, `stage`, finalize's outcome rule, `readBaseConfig` for follow-ups, `Budget` (time), cost from `total_cost_usd` | the project check, `halted`, the gateway, pins, managed settings, the token cap |
| Agent | `BuildEnv` with the real credential in the agent's env, `ParseStream` (no `usage`), `fakeclaude` | gateway env, `PinVars`, managed settings, result `usage`, fake model calls |
| Images | `web-node` base, runner user `fugaro` (non-root) | a `fugaro`-writable `/etc/claude-code` |
| Fakes | `gcpfake`, `faketerraform`, `fakeclaude`, the fake provider | a fake Anthropic and Vertex upstream with scripted streams |

**Decisions already made (user):**
- **D1–D18 and A1 of the design are binding.** In particular: D8 (an unpinned model fails the stage, `failed`, not `halted`; no request is rewritten), D9 (a halt before the branch exists is `halted`, no PR, exit 0), D11 (the gateway is in the `fugaro` binary in the base image), D16–D18 (project identity, one word, no compatibility), A1 (`oauth` is never proxied).
- **The user runs anything that costs money or changes real resources or repositories.** Every such step is **⚠ CONFIRM**, each its own question.
- **The sandbox is `acme/sandbox` on Bitbucket, workflow `web`, `auth: oauth`.** Live runs go to the sandbox only. The web repository gets a real run only with the user's go-ahead and task text.
- **Hermetic tests are the default:** no test needs a network, GCP, a provider or a model.

**Out of scope for M9a (M9b and later):**
- Firebase, RTDB, Firestore, per-run tokens, rules, leases against shared counters, heartbeats, the registry, the sweeper, the history job.
- Daily caps, kill switches, fail-closed on an unreachable backend (D14), the launch pre-check, `--no-budget-check`, `fugaro budget`, `fugaro watch`, `fugaro report`, `init --firebase`, `--budget-mode`, `--budget-admin`.
- The `notional` ledger for `oauth` runs, `requested_by` in tokens, `byPerson`.
- The verify gate before review and the structured findings (M9e).
- `fugaro ls --all-projects`, renaming a project.
- The halt reasons that need Firebase (`kill_switch`, `repo_daily_cap`, `global_daily_cap`, `budget_unavailable`, `budget_token_expired`) are **in the schema's enum** now, so M9b adds no schema change, but nothing emits them.

## Global Constraints

- **Exit codes:** 0 ok, 1 a user error or a refusal (no or ambiguous project config, a name mismatch, `--gcp-project` disagreeing, a checkout without `project:`), 2 a remote failure. `fugaro exec`: a halt at bootstrap (D9) exits **0** with status `halted`, outcome `none`; a project mismatch is `infra_error`, outcome `none`, exit 2; every other bootstrap failure is unchanged.
- **One word, one meaning (D17).** *Project* means the Fugaro project in every new identifier, flag, env variable, message and doc line. The GCP project is spelled `gcp_project`, `--gcp-project`, `FUGARO_GCP_PROJECT`, `GCPProject`. The one exception is Terraform's `project` variable, which stays.
- **Project names** match `^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$` (1 to 40 characters), one regexp, `config.ProjectNameRE`, used everywhere.
- **No compatibility layer (D18).** No code reads `config.yaml`, a `project:` key in a local config, `FUGARO_PROJECT` as a GCP ID, or a `fugaro.yaml` without `project:`. Each refusal names what to do and points at the design's §13.1.
- **The real model credential never reaches the agent.** With the gateway on, the agent's environment, the managed settings file, every log line the runner or the gateway writes, every transcript and every bucket object hold neither `ANTHROPIC_API_KEY`'s value nor a Vertex access token. The value is still registered with the redactor. (D2's residual stands: the runner's own `/proc/<pid>/environ` and the metadata server remain reachable, and §11 says so.)
- **The gateway never rewrites a body,** never buffers a stream, never retries, and never logs a header or a body. It logs per call: stage, model, status, token counts, µ$ charged, `x-claude-code-session-id` and `x-claude-code-agent-id`.
- **Money is integer micro-dollars (µ$)** from the price table to the ledger. `float64` appears only at the edges: the local config, `result.json`'s dollar fields, reports.
- **Reservation is exact and in-process.** `used + reserved ≤ granted` holds after every operation, under any number of concurrent calls, in `enforce` mode.
- **A halted run is never ready,** and its report says it halted. A halt never turns into `succeeded`.
- **`oauth` traffic never passes through the gateway or any proxy (A1).**
- **Repositories choose models; owners choose money.** `fugaro.yaml` can name models and token limits; caps and prices come only from the project config through the job's environment.
- **No company- or repo-specific values** in code, tests, fixtures or docs outside this plan and the live runbook: `acme/app`, `acme/sandbox`, `acme/webapp`, projects `aurora` and `borealis`, GCP IDs such as `proj-1234` and `aurora-gcp-1`, `example.invalid`.
- **TDD.** Each task writes its failing tests first and runs them to see them fail. `go test ./...` needs no Docker, network or credentials.
- **Subprocesses** use `exec.CommandContext` with `cmd.WaitDelay = 5 * time.Second`, as everywhere.
- CI runs `gofmt -l`, `go vet` (plain, `-tags docker`, `-tags live`, `-tags terraform`), `go test -race ./...`, and, for the Terraform, `terraform fmt -check`, each root's `validate` and `test`, `tflint`, `deploy/terraform/scan.sh` (Trivy) and `go test -tags terraform ./internal/infra/...`. All of them pass after every task.

## Rulings on the questions

Each ruling says what it costs if it's wrong.

**R1. How a command picks its project config** (design §2.4), in `localcfg.Select`, first match wins:
1. `--config <file>`, else `$FUGARO_CONFIG` (its environment form, kept: tests and scripts use it). The file's `name:` must be a valid name. **Inside a checkout whose `fugaro.yaml` names a project, the file's `name:` must equal it** ("--config names project borealis, but this checkout belongs to project aurora").
2. Inside a checkout (the working directory's `git rev-parse --show-toplevel` holds a `fugaro.yaml`): its `project:`. No `project:` → refuse ("this checkout's fugaro.yaml has no `project:`; add `project: <name>` (fugaro config example shows it)"). A named project without `projects/<name>.yaml` → refuse with the design's message. `--project` or `FUGARO_PROJECT` naming another project → refuse.
3. Outside a checkout: `--project <name>`, else `FUGARO_PROJECT`. A name without a config → exit 1, listing the configured names and "(the GCP project is `--gcp-project`)".
4. Exactly one `projects/*.yaml`: that one.
5. None: "no project config; see `fugaro init --config-only`" (and, when `config.yaml` exists, "the old config.yaml isn't read any more: see §13.1"). Several and none named: exit 1, listing them.
- A `fugaro.yaml` that fails to parse still yields its `project:` (read leniently by `config.ProjectOf`), so a broken config never hides which project a checkout belongs to.
- `projects/<name>.yaml` must hold `name: <name>`; a file whose `name:` differs from its basename is refused.
- *Cost if wrong:* one pure function with a table test.

**R2. `--gcp-project` and `--region`.** `--gcp-project` is accepted only when it equals the selected config's `gcp_project` (a no-op), or when `fugaro init` creates a project config that doesn't exist yet. There is no force flag. `--region` keeps its meaning (a read-only override). The `log_view` note in `openCloud`, which existed only for `--project` pointing elsewhere, is removed as dead code; so is `checkRegistryHostProject`'s comment about it.

**R3. Where the cloud keeps the name, and how the CLI checks it.** The design says the bucket label and the outputs. Launchers hold `roles/storage.objectAdmin` on the runs bucket, which lacks `storage.buckets.get`, so they can't read a label, and they can't read the Terraform state either. So:
- The installation's Terraform writes **`gs://<runs bucket>/fugaro/project.json`** (`google_storage_bucket_object`), `{"version": 1, "name": "<name>", "gcp_project": "<id>"}`, beside the label `fugaro_project=<name>` and the output `project_name`. No job account can write it (the jobs' bucket condition covers `runs/`, `cache/` and `locks/` only; builds cover `builds/`).
- `openCloud`, for every cloud command but `init`, reads that object and refuses a project config whose `name:` or `gcp_project:` differs ("project config aurora points at a GCP project whose Fugaro project is borealis"). A missing object: exit 1, "project aurora's installation has no project name yet; an operator runs `fugaro init --name aurora`". A read error: exit 2.
- The check is cached for a day in `$XDG_CACHE_HOME/fugaro/project-check/<name>.json` (`{gcp_project, runs_bucket, checked_at}`); any field differing re-checks.
- `fugaro init` compares the label, the output and the object with `--name` and refuses any disagreement (immutability).
- *Cost if wrong:* a launcher who overwrites the object can make their own commands refuse. It is a safety label, not a boundary (design §2.5).

**R4. The runner's project check** (design §2.5).
- `fugaro exec` passes `FUGARO_PROJECT` as `Deps.Project`, and `Deps.RequireProject` is true on Cloud Run (`CLOUD_RUN_JOB` set). On Cloud Run a missing `FUGARO_PROJECT` or `FUGARO_GCP_PROJECT` fails bootstrap: "job environment lacks FUGARO_PROJECT; run fugaro init --repo". A local `fugaro exec` without `FUGARO_PROJECT` skips the check and logs one line saying so.
- **Where:** in bootstrap, after the checkout and the base fetch, **before the lock**. For a first run the base fetch moves from after the lock to here (it is a read, so the lock still comes before anything that changes remote state). It reads `project:` with `git show origin/<base>:fugaro.yaml` (`gitops.ShowFile`, from M6) through `config.ProjectOf`, and, when `spec.Ref` isn't the base, the ref's own `fugaro.yaml` too. A follow-up reads only the base, which it reads anyway.
- Missing or different: `infra_error`, outcome `none`, exit 2, reason "project mismatch: fugaro.yaml on main names project borealis; this job belongs to project aurora" (or "… names no project").
- *Cost if wrong:* a run refused before it did anything: one container start.

**R5. The gateway's shape.**
- **Per run, in-process, loopback.** `gateway.Start` listens on `127.0.0.1:0`. It starts in bootstrap after the config, pins and settings checks, and closes after the agent loop, before finalize (finalize makes no model calls).
- **Who can call it.** Upstream `anthropic`: every request must carry the per-run token (32 random bytes, hex) as `x-api-key`, else 401. Upstream `vertex`: Claude Code sends no credential when `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, so the gateway accepts loopback requests without a token. Either way anything in the container can reach it; D2 already accepts that the container is not a boundary against its agent.
- **Endpoints** (design §5.1): `POST /v1/messages`, `POST /v1/messages/count_tokens` (free, no reservation), `HEAD /api/hello` (a local 200); on Vertex `POST /v1/projects/<p>/locations/<l>/publishers/anthropic/models/<m>:rawPredict`, `:streamRawPredict` and `…/models/count-tokens:rawPredict`, where `<p>` and `<l>` must be the job's `ANTHROPIC_VERTEX_PROJECT_ID` and `CLOUD_ML_REGION` (the gateway's token must not work on another project). Anything else: 404.
- **Forwarding.** The incoming `x-api-key` and `authorization` are dropped, the real credential is set, every other request header and the body pass unchanged; response status, headers (`retry-after`, `x-should-retry`, `anthropic-ratelimit-unified-*`, `request-id`) and body pass unchanged, flushed after every write.
- **Request parsing.** The body is read whole (at most 32 MiB, the API's own limit; more is a 413 from the gateway) to find `model` (on Vertex from the path), `max_tokens`, `stream`, any `cache_control` and its `ttl`, and a `web_search` tool with its `max_uses`. A body that isn't JSON or lacks `max_tokens` on `/v1/messages`: 400 from the gateway, never forwarded.
- **The upstream base** is `https://api.anthropic.com`, or `https://<region>-aiplatform.googleapis.com` (`https://aiplatform.googleapis.com` for `global`). Tests set it only through `exec`'s hidden `--gateway-upstream`, which accepts only `http://127.0.0.1:<port>`, so no environment variable can send the real key elsewhere.
- *Cost if wrong:* the handler is one package with its own fake upstream.

**R6. Worst case, reservation and settlement.**
- `worstCase = ceil(bodyBytes × inputRate × cacheMult + maxTokens × outputRate + webSearchCap × perSearch)` in µ$ (design §5.4): `cacheMult` 2 with any `"ttl": "1h"`, else the table's 5-minute write multiplier with any `cache_control`, else 1; the long-context tier's rates when `bodyBytes` is past its threshold; `webSearchCap` is the tool's `max_uses`, else 20 (`gateway.DefaultWebSearchCap`).
- The role's `max_output_tokens`, when set, bounds `max_tokens`: above it, a 400 `invalid_request_error` "fugaro: max_tokens N is above the stage's limit M" and a stage violation (D8's rule: a configuration error fails the stage).
- **One mutex, one ledger:** `{granted, used, reserved}`. `enforce`: `granted` is the per-run cap. A call reserves `w` if `used + reserved + w ≤ granted`, else it is refused: 403 `permission_error` "fugaro: budget halted: run cap $X reached ($Y spent)", `x-should-retry: false`, and the gateway **halts**: this and every later request is refused the same way, and `Halted()` fires once. `observe`: `granted` is unbounded; a call that `enforce` would refuse is logged ("budget: would halt (observe)") and counted, never refused.
- **Settlement** (design §5.5): a completed stream or JSON body: its usage at the **serving** model's rates (the `message_start.message.model`, else the body's `model`); an upstream error before `message_start`: 0 (A3); an error, disconnect or client cancel after `message_start`: input tokens plus the **reserved** output part, the difference from actual counted as `unreconciled`; an unknown serving model: the table's maximum rates, logged `priced_as: max`. `message_delta.usage` is cumulative: the last value of each field wins. `input_tokens` excludes cache reads and writes; the tier's threshold compares their sum.
- **Overrun.** If `actual > w` (an unknown serving model, or a server-side fallback), the whole `actual` is charged, the excess is logged and counted as `overrun`, and `used` may pass `granted` by that call's excess; the next reservation then halts. The property test allows exactly this case.
- **Retries are new requests** (Claude Code retries 429, 529 and dropped streams): each reserves and settles on its own; the gateway never retries.
- *Cost if wrong:* golden and property tests pin the arithmetic.

**R7. Halting at a call boundary.** A run-cap refusal happens **before** a call is forwarded, so Claude Code gets the 403 while it is waiting on the model, not while a tool is writing a file. The runner does not kill the stage at once: after `Halted()` it waits up to `haltGrace` (60 s) for `claude` to exit on its own (A-N1), then cancels the stage context with the halt as its cause, which SIGTERMs the process group (10 s grace, as today). Calls already in flight when the cap is hit were reserved and finish normally. Finalize commits any leftovers as `fugaro: uncommitted work at halt (the stage was stopped; files may be incomplete)`, and the report says so. *Cost if wrong:* a parallel subagent's tool can still be mid-write when the grace ends; the draft PR says the files may be incomplete, and a halted run is never ready.

**R8. `halted` in the runner** (design §5.9).
- `runner.HaltError{Halt runstore.Halt}`, `errors.Is(err, runner.ErrHalted)`. A halt cancels the stage context with it as the cause (the `ErrCancelled` pattern). `r.halt` holds the first halt; `r.cancelled` and `r.halt` are set only by whichever cause came first (`context.Cause` of the stage), so the other is ignored.
- **Mid-run:** the loop stops, finalize runs normally: status `halted`, outcome `draft` once the branch is pushed; a follow-up that stops before its push (M6's no-push paths) ends `halted`/`none`. `ready` is forced false. `reason` is "halted: <reason>: <detail>".
- **At bootstrap** (D9; in M9a only `no_cap`: an `api-key` or `vertex` run with `FUGARO_BUDGET_MODE=enforce` and no positive `FUGARO_MAX_RUN_USD`): the auth mode is known only once `fugaro.yaml` is read, so the check runs right after the project check and **before the lock**. The run branch exists only in the local checkout then: nothing is locked, pushed or opened. Status `halted`, outcome `none`, no PR, **`Run` returns a nil error**, so `exec` exits 0.
- **The token cap** (§5.8): after every stage, the result event's `usage` (input, cache creation, cache read, output) is added up; when `agent.max_run_tokens > 0` and the sum reaches it, the run halts with `token_cap`, scope `run`. It applies to every auth mode, gateway or not, and it is checked only at stage boundaries.
- **The report** keeps its first line, `### Fugaro run \`<id>\`` (M6's `FugaroRun` recognizes it), and adds, right below, `**Halted:** <reason text> at <time> — this run spent $X (cap $Y).` and how to continue: raise `budget.per_run_usd` in the project config and run `fugaro init --repo`, then `fugaro run --pr N`.
- `result.json` (version 1, additive): `status` gains `halted`; `halt: {reason, scope, at, detail}` with the eight reasons of §5.9; `cost` gains `model_source` (`gateway` | `claude-code`), `model_by` (model → USD) and `unreconciled` (USD).
- *Cost if wrong:* the status is additive; old records parse.

**R9. Pins** (design §2.1).
- The role of a stage: `implement` and `fix` → `coder`; `review` → `reviewer`. Its model: for `coder`, the task's `overrides.model`, else `agent.models.coder`, else `agent.model`; for `reviewer`, `agent.models.reviewer`, else `agent.model`. (`task.Apply` writes the override into `agent.models.coder`, so `Agent.ModelFor` needs no task.) The background model: `agent.models.background`.
- **Every stage, whatever the budget mode,** when a role model is set: `--model <model>`, and `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` = the model, `ANTHROPIC_DEFAULT_HAIKU_MODEL` = the background model when set, `CLAUDE_CODE_MAX_OUTPUT_TOKENS` = the role's `max_output_tokens` when set.
- **With the budget on** (`observe` or `enforce`), bootstrap refuses (`infra_error`) a role without an explicit model ID, an alias (`sonnet`, `opus`, `haiku`, `opusplan`, `default`, any `[1m]` suffix, any ID not in the effective table), or a missing background model; `fugaro validate` applies the same rules when the selected project config's budget is on.
- **The gateway's allow-list per stage:** `{role model, background model}` and their table aliases. Anything else: 400 `invalid_request_error` "fugaro: model X is not pinned for stage review", a stage violation, and the stage fails (`failed`, D8) once it returns, even if Claude Code carried on.

**R10. Managed settings and the repository's own settings** (design §5.1, A6).
- When the gateway is on **or** any pin applies, before every stage the runner writes `/etc/claude-code/managed-settings.json` (`Deps.ManagedSettingsPath` in tests): `{"env": {…}}` holding the gateway variables of §5.1 and the stage's pins. It replaces the file atomically (write, fsync, rename in the same directory) and refuses a symlink in its place. The same variables go into the stage's process environment.
- The base image creates `/etc/claude-code` owned by `fugaro`, mode 0755 (the runner is not root). `fugaro image selftest` checks it. With the gateway on and the directory not writable, bootstrap fails (`infra_error`, "the image can't hold Claude Code's managed settings; rebuild it on an M9a base image"); with only pins, a warning.
- **Defense in depth (Open question 4):** with the gateway on, bootstrap refuses (`infra_error`) a checkout whose `.claude/settings.json` or `.claude/settings.local.json` sets `apiKeyHelper`, or an `env` key among `ANTHROPIC_BASE_URL`, `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_FOUNDRY`, `CLAUDE_CODE_SKIP_VERTEX_AUTH`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` (any case). The agent can still write those files during the run; that is D2's residual, and A6 is what holds then.
- *Cost if wrong:* if A6 is false, the refusal is the only guard against the committed settings; the live check (Task 11) tells.

**R11. The budget in the project config and the job** (design §5.6, §5.7, §6.7).
- Project config: `budget: {mode: off|observe|enforce, per_run_usd: N}` and top-level `model_prices: {<model>: {input_per_m, output_per_m, cache_write_5m, cache_write_1h, cache_read, web_search_per_1k}}`. No `budget:` block means `off`. `enforce` needs `0 < per_run_usd ≤ 100000`; `observe` accepts 0 (account only). In M9a the owner edits the file and re-runs `fugaro init --repo`; `--budget-mode` is M9b's.
- `fugaro init --repo` puts, with the mode not `off`, `FUGARO_BUDGET_MODE`, `FUGARO_MAX_RUN_USD` (when > 0) and `FUGARO_MODEL_PRICES` (compact JSON, when set) into every **workflow** job's env (not the check job's: it calls no model). They are plain env map entries in the workflow spec, so no Terraform variable changes for them.
- The runner parses them once (`runner.SpendFromEnv`); a malformed value is an `infra_error` at bootstrap. With `auth: oauth` the mode doesn't start a gateway and `no_cap` doesn't apply (there is no dollar cap for `oauth`, A1); pins and the token cap still apply.

**R12. Cost.** With the gateway: the record's `cost_usd` and `cost.model_usd` are the gateway's settled µ$ (`model_source: gateway`), `model_by` its per-model split and `unreconciled` its estimated share; Claude Code's `total_cost_usd` per stage is kept only for a cross-check, and the runner logs a warning when the two differ by more than 5% in a stage. Without it: as today, `model_source: claude-code`.

## Review Focus

These are the failure modes that are easiest to miss, most likely first. Each is pinned by a named test in the task that owns the code.

1. **Leaking the real key to the agent, a log, a transcript or the managed settings file.** Pinned by T9 `TestBuildEnvGatewayDropsRealKey` (api-key and vertex), `TestManagedSettingsHoldNoRealKey`; T5 `TestGatewayLogsNoSecrets` (an upstream 401 whose body echoes the key's prefix; the log holds neither the key nor the token), `TestGatewayStripsClientCredentials`; T10 `TestGatewayRunSecretScan` (a full run: the planted key is in no bucket object, log line, transcript, `calls.jsonl` env or settings file) and the e2e `TestCloudGatewayRunSecretScan`.
2. **Under-reserving, so the cap is exceeded.** Pinned by T4 `TestWorstCaseBoundsActual` (property: any usage whose token counts fit the body's bytes and `max_tokens` costs at most the worst case, for every model, cache TTL and tier), `TestWorstCaseCacheWrite1h`; T5 `TestLedgerNeverExceedsGranted` (property: 50 concurrent calls with random sizes, `used + reserved ≤ granted` after every step, overrun only from `priced_as: max`), `TestMaxTokensAboveRoleLimitRefused`, `TestWebSearchReservedWithoutMaxUses`.
3. **The gateway bypassed by the repository's config.** Pinned by T9 `TestRoutingKeysRefused` (table over both settings files and key cases), `TestManagedSettingsBeforeEveryStage`; T10 `TestRepoSettingsRerouteRefused`, `TestManagedSettingsWrittenBeforeFirstStage`; T11 `TestLiveGateway` step 1 (A6, live).
4. **A halted run that ends `succeeded`, or ready.** Pinned by T7 `TestHaltNeverReady` (a halt after a `ship` verdict and a passing verify), `TestHaltAfterCancelKeepsCancelled`, `TestCancelAfterHaltKeepsHalted`; T10 `TestRunCapHaltOpensDraft`.
5. **Double counting on retries, and prompt-cache pricing.** Pinned by T4 `TestCostCacheReadPerModel` (golden: Opus-class 0.05×, Sonnet-class 0.1×), `TestCostInputExcludesCache`, `TestCostTierUsesTotalInput`; T5 `TestRetryAfter529ChargesOnce`, `TestErrorBeforeMessageStartChargesZero`, `TestDisconnectAfterStartChargesReservedOutput`, `TestMessageDeltaCumulativeLastWins`.
6. **Wrong-project launches, and a project check that can be bypassed.** Pinned by T1 `TestSelect` (every row of R1), `TestGCPProjectFlagMustAgree`, `TestCloudCommandsPrintProjectHeader`; T2 `TestOpenCloudRefusesNameMismatch`, `TestInitNameImmutable`, `TestDiscoverRefusesForeignProjectLabel`; T3 `TestProjectCheckReadsBaseNotBranch` (the run's ref sets `project: aurora` on a branch whose base says `borealis`: refused), `TestProjectCheckRefAlsoChecked`, `TestProjectCheckMissingEnvOnCloudRun`, `TestInitRepoRefusesOtherProject`; T10 `TestTestKnobIgnoredOnCloudRun` (the live test's routing-settings knob does nothing in a job).
7. **Halting mid-write.** Pinned by T10 `TestHaltWaitsForCallBoundary` (the agent exits on its own after the 403; no SIGTERM), `TestHaltGraceThenKill`, `TestInFlightCallsFinishAfterHalt`; T7 `TestHaltLeftoversCommitMessage`.
8. **An unpinned model counted as a halt, or allowed.** Pinned by T5 `TestUnpinnedModelRefused400`; T10 `TestUnpinnedModelFailsStage` (`failed`, not `halted`; reason names the model and stage), `TestBackgroundModelPinned`.
9. **A bootstrap halt reported as an infrastructure error, or an `oauth` run halted for lacking a dollar cap it can never have.** Pinned by T7 `TestBootstrapHaltIsHaltedExitZero` (runner) and `TestExecBootstrapHaltExitsZero` (`internal/cli`); T10 `TestNoCapHaltAtBootstrap`, `TestOAuthEnforceWithoutCapIsNotNoCap`.
10. **Secrets or prompt text in the gateway's event logs.** Pinned by T5 `TestGatewayLogFields` (exactly the allowed fields; no body bytes, no header values beyond the two Claude Code IDs).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/project.go` (new) | `ProjectNameRE`, `ProjectOf` (lenient read of `project:`) | 1 |
| `internal/localcfg/localcfg.go`, `internal/localcfg/select.go` (new), `internal/localcfg/checkcache.go` (new, T2) | `Name`, `GCPProject`, project paths, `Select`, the old-key message; the name-check cache | 1, 2, 8 |
| `internal/cli/cloud.go`, `internal/cli/project.go` (new) | `--project`, `--gcp-project`, selection, the header, `project` in `--json`; the cloud name check (T2) | 1, 2 |
| `internal/cli/{init,ls,run,cancel,logs,diagnose,secrets,image,imagecheck,image_record,validate,configcmd}.go` | flags, the header, JSON `project`; `init --name` (T2); `init --repo`'s project rule, `config example` (T3); `validate`'s budget rules (T6) | 1, 2, 3, 6 |
| `internal/infra/{spec,tfvars,discover}.go`, `internal/infra/tf/faketerraform/main.go`, `internal/infra/testdata/m4-jobspec-sandbox.*` | `FugaroProject`, `ProjectName`, the env renames, discovery's label rule; the budget env (T8) | 2, 8 |
| `internal/backend/backend.go`, `internal/cli/imagecheck.go` | `FUGARO_GCP_PROJECT` | 2 |
| `deploy/terraform/gcp/{modules,roots}/{installation,repo}/*.tf`, `…/tests/**` | `fugaro_project`, the label, the marker object, `project_name`, the repo root's env check | 2 |
| `internal/e2e/live_gcp_test.go`, `internal/backend/gcp/live_test.go` | `FUGARO_LIVE_GCP_PROJECT` | 2 |
| `internal/config/{config,validate,example.yaml}.go`, `schemas/fugaro.schema.json`, `testdata/config/**`, `testdata/fixture-repo/fugaro.yaml`, `deploy/sandbox/fugaro.yaml` | `project:` (T3); `agent.models`, `max_output_tokens`, `max_run_tokens`, pin rules (T6) | 3, 6 |
| `internal/runner/project.go` (new), `internal/runner/runner.go`, `internal/cli/exec.go` | the bootstrap project check, `Deps.Project`, `RequireProject` | 3 |
| `plugin/skills/onboard/SKILL.md` | `project:` from the selected project config | 3 |
| `internal/pricing/` (new: `pricing.go`, `table.go`, `worst.go`, `overrides.go`) | the table, aliases, tiers, costs, worst case, overrides | 4 |
| `internal/gateway/` (new: `gateway.go`, `ledger.go`, `forward.go`, `sse.go`, `vertex.go`, `log.go`); `internal/gateway/anthropicfake/` (new) | the proxy and its fake upstream | 5 |
| `internal/runstore/{runstore,cost}.go`, `schemas/result.schema.json`, `schemas/schemas_test.go` | `StatusHalted`, `Halt`, the cost fields | 7 |
| `internal/runner/halt.go` (new), `internal/runner/{runner,budget,report}.go`, `internal/agent/{agent,stream}.go`, `internal/runview/runview.go`, `internal/cli/{ls,diagnose,cancel}.go` | `ErrHalted`, `HaltError`, the token cap, result `usage`, the halted report, the views | 7 |
| `internal/runner/spend.go` (new) | `SpendFromEnv`, `Spend` | 8 |
| `internal/agent/{env,settings}.go` (`settings.go` new), `images/web-node/Dockerfile`, `internal/image/selftest.go` | gateway env, `PinVars`, managed settings, `RoutingKeys`; `/etc/claude-code` | 9 |
| `internal/runner/gateway.go` (new), `internal/runner/{runner,cost}.go`, `internal/cli/exec.go`, `internal/agent/fakeclaude/main.go`, `internal/e2e/cloud_test.go` | starting the gateway, halts, violations, cost, the cross-check; fake model calls; the hermetic cloud run | 10 |
| `docs/design/v1.md`, `docs/gcp-setup.md`, `docs/gcp-live-checklist.md`, `internal/e2e/live_gateway_test.go` (new) | the docs and the live gateway test | 11 |
| — (controller-run) | the one-time migration (§13.1) and the live checks | 12 |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on | Why |
|---|---|---|---|---|
| T1 project configs, selection, `--gcp-project` | L | A | — | |
| T2 the name in the cloud, the env renames, Terraform | L | A | T1 | `Config.Name`, `GCPProject`, `cloudOptions` |
| T3 `project:` in `fugaro.yaml`, the runner's check, `init --repo` | M | A | T2 | `FUGARO_PROJECT` is the name; `InstallationOutputs.ProjectName` |
| T4 `internal/pricing` | M | B | — | |
| T5 `internal/gateway` | L | B | T4 | the table, worst case, costs |
| T8 the budget in the project config and the job env | S | B | T2, T4 | `localcfg`, `infra/spec.go` after T2; `pricing.Overrides` |
| T6 per-stage models and the pin rules | M | A→B | T3, T4, T8 | `internal/config` (hot, after T3); the table; `validate` reads `lc.BudgetMode()` |
| T7 `halted`, the token cap | M | B | T6 | `agent.max_run_tokens`; `runner.go` after T3 |
| T9 the agent's environment, pins, managed settings, the base image | M | B | T5, T6, T7 | the gateway's URL and token; roles; `runner.go` after T7 |
| T10 the runner drives the gateway | L | B | T5, T7, T8, T9 | everything |
| T11 docs and the live gateway test | M | — | T1–T10 | documents and exercises everything |
| T12 migration and live verification (controller) | S | — | all | |

```
Lane A:  T1 ──► T2 ──┬──► T3 ──────────┐
                     └──► T8 ──────────┼──► T6 ──► T7 ──► T9 ──► T10 ──► T11 ──► T12
Lane B:  T4 ─────────┬─────────────────┘                 ▲        ▲
                     └──► T5 ────────────────────────────┴────────┘
```

**Two lanes until they join.** T1–T3 (project identity) and T4–T5 (new packages only) touch disjoint files and may run at the same time in two worktrees, each merged into `m9a` when its reviewer passes it. T3 and T8 touch disjoint files (T8 needs T4 too) and may run side by side. From T6 on, the tasks are sequential. Review, not typing, is the bottleneck: a single lane in the order T1, T4, T2, T5, T3, T8, T6, T7, T9, T10, T11, T12 is equally valid.

**Hot files,** and how they're kept safe:
- `internal/localcfg/localcfg.go`: T1 (shape, `Name`, `GCPProject`, paths), then T2 (only `checkcache.go` beside it), then T8 (`Budget`, `ModelPrices`). In that order.
- `internal/cli/cloud.go`: T1, then T2 (the name check call in `openCloud`). `internal/cli/init.go`: T1 (flags, `loadInitConfig`), T2 (`--name`, outputs, immutability), T3 (`init --repo`'s project rule). In that order, each on top of the last.
- `internal/infra/spec.go`: T2 (`platformEnv`, `ProjectName`), then T8 (the budget env in `workflow`).
- `internal/config/*`, `schemas/fugaro.schema.json`, `testdata/config/**`: T1 adds only the new file `project.go`; T3 (`project:`), then T6 (`agent` fields). T6 starts after T3 has landed.
- `internal/runner/runner.go`: T3 (the check in `bootstrap`, the base fetch moved), T7 (halt plumbing in `Run`, `stage`, `agentLoop`, `finalize`), T9 (the per-stage env and settings in `stage`), T10 (the gateway's start, close and events). Strictly in that order; each adds its logic in its own new file (`project.go`, `halt.go`, `gateway.go`) and keeps its `runner.go` edits to call sites.
- `internal/agent/env.go`: T9 only. `internal/agent/stream.go`, `agent.go`: T7 only (result `usage`).
- `internal/runstore/*`, `schemas/result.schema.json`, `schemas/schemas_test.go`: T7 only.
- `internal/cli/exec.go`: T3 (`Deps.Project`), then T10 (`Spend`, `--gateway-upstream`). T7 edits only `exec_test.go`.
- `internal/cli/{ls,diagnose,cancel,run}.go`: T1 (the JSON `project` field), then T7 (the halted views in `ls` and `diagnose`).
- `internal/task/task.go`: T6 only (`overrides.model` → `agent.models.coder`).
- `internal/agent/fakeclaude/main.go`, `internal/e2e/cloud_test.go`: T10 only (T3 only edits the fixtures' `fugaro.yaml` strings in `cloud_test.go`, before T10).
- `docs/**` (but the onboard skill), `internal/e2e/live_*`: T11 only, except T2's rename of `FUGARO_LIVE_PROJECT` in the two live test files.

---

### Task 1: Project configs, selection, and `--gcp-project`

**Files:**
- Create: `internal/config/project.go`, `internal/config/project_test.go`
- Modify: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`; Create: `internal/localcfg/select.go`, `internal/localcfg/select_test.go`
- Modify: `internal/cli/cloud.go` (`cloudOptions`, `addCloudFlags`, `openCloud`, drop the `log_view` note); Create: `internal/cli/project.go` (`checkoutProject`, `selectProject`, `printProjectHeader`), `internal/cli/project_test.go`
- Modify: `internal/cli/init.go` (`loadInitConfig`: create `projects/<name>.yaml` from `--gcp-project`, `--region` and `--name`; `r.project` is the name; `initResult.Project` is the name and `GCPProject` the ID), and the JSON result types of `ls.go` (`lsDoc`), `run.go`, `cancel.go`, `diagnose.go`, `image.go`, `imagecheck.go`, `secrets.go` (`secrets set`), each gaining `Project string \`json:"project"\``
- Modify every test that writes a local config or passes `--project`: `internal/cli/*_test.go` (`cloud_test.go`, `init_test.go`, `init_apis_test.go`, `ls_test.go`, `run_test.go`, `followup_test.go`, `cancel_test.go`, `diagnose_test.go`, `image_test.go`, `image_cloud_test.go`, `imagecheck_test.go`, `secrets_test.go`, `logs` tests, `lifecycle_test.go`), `internal/e2e/cloud_test.go`, `internal/e2e/init_test.go`, `internal/gcpfake/*_test.go` where they build a local config, `internal/infra/*_test.go` (`lc.Project` → `lc.GCPProject`)
- Modify every reader of `lc.Project`: `internal/infra/{spec,tfvars,discover,readiness,repo,apis,statebucket,workdir}.go`, `internal/cli/{init,image,cloud}.go`, `internal/backend/gcp/*` options (field `Project` → `GCPProject` in `gcp.Options`)

**Interfaces:**
- Produces:

```go
// internal/config/project.go
var ProjectNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
// ProjectOf reads fugaro.yaml's top-level project: leniently (unknown and
// invalid fields ignored), so a broken config still says which project it
// belongs to. "" when absent. An error only for YAML that doesn't decode.
func ProjectOf(data []byte) (string, error)

// internal/localcfg
type Config struct {
	Version    int    `yaml:"version"`
	Name       string `yaml:"name"`        // the Fugaro project (design §2.4); ProjectNameRE
	GCPProject string `yaml:"gcp_project"` // the GCP project ID
	Region     string `yaml:"region"`
	// … every other field unchanged
}
// Parse: a "project" key gives "local config: `project:` is now `gcp_project:`,
// and the file lives at projects/<name>.yaml with name: <name>; see
// docs/design/m9-budget-and-dashboard.md §13.1". name is required.
func ProjectsDir(getenv func(string) string) (string, error)      // $XDG_CONFIG_HOME/fugaro/projects, else ~/.config/fugaro/projects
func ProjectPath(getenv func(string) string, name string) (string, error)
func Projects(getenv func(string) string) ([]string, error)       // names of projects/*.yaml, sorted
func LoadProject(getenv func(string) string, name string) (*Config, string, error) // refuses name: ≠ basename
func (c *Config) Override(gcpProject, region string) error         // R2: gcpProject must equal c.GCPProject

type Checkout struct {
	Root    string // git toplevel
	Project string // its fugaro.yaml's project:, "" when absent
}
type SelectInput struct {
	Config      string    // --config, else $FUGARO_CONFIG
	Project     string    // --project
	EnvProject  string    // $FUGARO_PROJECT
	Checkout    *Checkout // nil outside a checkout (no git toplevel, or no fugaro.yaml there)
	Getenv      func(string) string
}
type Selection struct {
	Path, Name string
	From       string // "--config", "FUGARO_CONFIG", "checkout", "--project", "FUGARO_PROJECT", "only project config"
}
// SelectError is a refusal (exit 1); its message is R1's.
type SelectError struct{ Msg string; Projects []string }
func Select(in SelectInput) (Selection, *Config, error)

// internal/cli
type cloudOptions struct {
	config, project, gcpProject, region string
	stderr func() io.Writer
}
// addCloudFlags registers --config, --project (a Fugaro project name), --gcp-project and --region.
func checkoutProject(ctx context.Context) (*localcfg.Checkout, error)
func selectProject(ctx context.Context, o cloudOptions) (localcfg.Selection, *localcfg.Config, error) // applies --gcp-project and --region (R2)
func printProjectHeader(w io.Writer, lc *localcfg.Config) // "project: aurora (GCP proj-1234)\n"
```

- `fugaro init` with no project config yet takes `--gcp-project`, `--region` and **`--name`** (required then) and writes `projects/<name>.yaml`; with one, `--name` must equal its `name:` (T2 adds the cloud side). `init --config-only --gcp-project <id>` without `--name` is refused until T2 lets it take the name from the outputs ("pass --name" for now).
- Every cloud command prints the header first on stderr, before any other output, including errors after selection. `--json` object outputs gain `project`; `secrets ls --json`, an array, stays as it is (Open question 6).

- [ ] **Step 1: Write the failing tests.**
  - `internal/config`: `TestProjectNameRE` (`a`, `aurora`, `a-1`, 40 characters ok; empty, `-a`, `a-`, `A`, `a_b`, 41 characters refused); `TestProjectOf` (present; absent → ""; a file with unknown fields and a bad `version` still gives the name; non-YAML → error).
  - `internal/localcfg`: `TestParseOldProjectKey` (the message names `gcp_project:` and §13.1); `TestParseRequiresName`; `TestLoadProjectNameMustMatchFile`; `TestProjectsListsYAMLOnly` (ignores `*.bak`, directories, `config.yaml`); `TestOverrideGCPProjectMustAgree`; `TestSelect` (table, one row per rule of R1: `--config` alone; `FUGARO_CONFIG` alone; `--config` in a checkout of another project refused; checkout with a config; checkout without `project:` refused; checkout naming a project with no config refused with the `--config-only` hint; checkout + `--project` other refused; checkout + `FUGARO_PROJECT` other refused; checkout + `--project` same ok; outside + `--project`; outside + `FUGARO_PROJECT`; `--project` beats `FUGARO_PROJECT`; unknown name lists names and mentions `--gcp-project`; one config; none, with and without an old `config.yaml`; several and none named lists them).
  - `internal/cli`: `TestCloudCommandsPrintProjectHeader` (each of `ls`, `run --retry`, `cancel`, `logs`, `diagnose`, `secrets ls`, `image status`, `check`: the first stderr line is the header); `TestGCPProjectFlagMustAgree` (exit 1 on another ID; ok on the same); `TestProjectFlagNamesNoProject` (exit 1, lists names, mentions `--gcp-project`); `TestJSONOutputsCarryProject` (`ls --json`, `run --json`, `cancel --json`, `diagnose --json`); `TestInitCreatesProjectConfig` (`--gcp-project`, `--region`, `--name` → `projects/aurora.yaml` with `name` and `gcp_project`); `TestInitWithoutNameRefused`; `TestCheckoutProjectFromBrokenFugaroYAML`.
  - `internal/e2e`: `cloud_test.go` and `init_test.go` write `projects/<name>.yaml` under a temporary `XDG_CONFIG_HOME` and pass `--gcp-project` where they passed `--project`; their assertions are otherwise unchanged.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/localcfg/ ./internal/cli/ -run 'ProjectNameRE|ProjectOf|OldProjectKey|RequiresName|NameMustMatch|ProjectsLists|OverrideGCP|Select|ProjectHeader|GCPProjectFlag|ProjectFlagNames|CarryProject|CreatesProjectConfig|WithoutName|BrokenFugaroYAML'`. Expected: FAIL (undefined names).
- [ ] **Step 3: Implement.** The rename of `Config.Project` is mechanical: `gopls rename` (or the compiler) finds every reader. `gcp.Options.Project` becomes `GCPProject` in the same commit, so no GCP call site is left meaning two things.
- [ ] **Step 4:** `go test -race ./...` and `go vet -tags live ./... && go vet -tags docker ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "localcfg, cli: per-project configs, project selection, --gcp-project"`

---

### Task 2: The name in the cloud, the environment renames, and Terraform

**Files:**
- Modify: `internal/infra/tfvars.go` (`InstallationSpec.FugaroProject`, `repoVars.FugaroProject`), `internal/infra/spec.go` (`InstallationOutputs.ProjectName`, `RepoInstallation.ProjectName`, `platformEnv`, `withDefaults`), `internal/infra/discover.go` (the bucket's `fugaro_project` label), and their tests
- Modify: `internal/infra/tf/faketerraform/main.go` (outputs `project_name`), `internal/infra/testdata/m4-jobspec-sandbox.{json,fields.tsv}` and `internal/infra/testdata/README.md` (a note: "the env names changed in M9a: FUGARO_PROJECT is the project name, the GCP ID is FUGARO_GCP_PROJECT")
- Modify: `internal/cli/init.go` (`--name`, the immutability checks, `configOnly` naming the file from the outputs, `writeConfig` keeping `name`), `internal/cli/init_test.go`
- Create: `internal/localcfg/checkcache.go` and its test; Modify: `internal/cli/cloud.go` (`openCloud` calls the check), `internal/cli/project.go` (`checkCloudName`)
- Modify: `internal/backend/backend.go` (`ExecutionFromEnv` reads `FUGARO_GCP_PROJECT`), `internal/backend/backend_test.go`, `internal/cli/imagecheck.go` (`FUGARO_GCP_PROJECT`), `internal/cli/imagecheck_test.go`, `internal/cli/exec_test.go`, `internal/cli/run_test.go`, `internal/cli/image_cloud_test.go`, `internal/e2e/cloud_test.go` (env names)
- Modify: `deploy/terraform/gcp/modules/installation/{variables,bucket,outputs}.tf`, `deploy/terraform/gcp/roots/installation/{variables,main,outputs}.tf`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`
- Modify: `deploy/terraform/gcp/modules/repo/variables.tf`, `deploy/terraform/gcp/roots/repo/{variables,main}.tf`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `deploy/terraform/gcp/roots/repo/tests/testdata/{bitbucket-oauth,github-vertex}.tfvars.json`
- Modify: `internal/e2e/live_gcp_test.go`, `internal/backend/gcp/live_test.go` (`FUGARO_LIVE_GCP_PROJECT`)

**Interfaces:**
- Consumes: T1's `Config.Name`, `GCPProject`, `cloudOptions`, `ProjectNameRE`.
- Produces:

```go
// internal/infra
// InstallationSpec gains: FugaroProject string `json:"fugaro_project"`
// InstallationOutputs gains: ProjectName string `json:"project_name"` // "" for an installation applied before M9a
// RepoInstallation gains: ProjectName string `json:"-"` (from the outputs); repoVars gains FugaroProject string `json:"fugaro_project"`
// platformEnv: FUGARO_BUCKET, FUGARO_BACKEND, FUGARO_REGION,
//   FUGARO_GCP_PROJECT = lc.GCPProject, FUGARO_PROJECT = the installation's ProjectName.
// Repo refuses an empty ProjectName: "the installation has no project name;
//   an operator runs fugaro init --name <name> first".
const ProjectMarkerObject = "fugaro/project.json"
type ProjectMarker struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	GCPProject string `json:"gcp_project"`
}
const LabelProject = "fugaro_project" // in internal/backend/gcp beside LabelManaged

// internal/localcfg/checkcache.go
type NameCheck struct {
	GCPProject string    `json:"gcp_project"`
	RunsBucket string    `json:"runs_bucket"`
	CheckedAt  time.Time `json:"checked_at"`
}
func CachedNameCheck(getenv func(string) string, name string, now time.Time) (NameCheck, bool) // fresh for 24 h
func SaveNameCheck(getenv func(string) string, name string, c NameCheck) error                 // best effort; 0600

// internal/cli
func checkCloudName(ctx context.Context, b *blobx.Bucket, lc *localcfg.Config, getenv func(string) string, now time.Time) error // R3
```

- **Terraform.** Installation module: `variable "fugaro_project"` (string, validated with the same regexp), `labels = { fugaro = "managed", fugaro_project = var.fugaro_project }` on the runs bucket, `resource "google_storage_bucket_object" "project_marker"` (`name = "fugaro/project.json"`, `content = jsonencode({version = 1, name = var.fugaro_project, gcp_project = var.project})`, `content_type = "application/json"`), `output "project_name"`. The root passes it through. Repository module and root: `variable "fugaro_project"` and a `lifecycle { precondition }` on each workflow job and on the check job that its `env["FUGARO_PROJECT"]` equals it and `env["FUGARO_GCP_PROJECT"]` equals `var.project` (a precondition fails the plan; a `check` block would only warn). Terraform's `project` variable is unchanged (design §2.6).
- **`fugaro init --name`** (R3): the name is `--name`, else the state's `project_name` output, else the project config's `name:`; all that are set must agree, else refuse: "the installation's project name is aurora; renaming isn't supported (design §2.5)". Discovery refuses a runs bucket whose `fugaro_project` label names another project. An installation with no name anywhere refuses without `--name`. After the apply, `writeConfig` keeps `name:` and checks it against the output.
- **`fugaro init --config-only --gcp-project <id>`** reads the outputs; the file is `projects/<project_name>.yaml`; no `project_name` → refuse "the installation has no project name yet; an operator runs fugaro init --name <name>"; an existing file for that name with another `gcp_project` → refuse.

- [ ] **Step 1: Write the failing tests.**
  - `internal/infra`: `TestPlatformEnvNames` (both variables; no GCP ID under `FUGARO_PROJECT`); `TestRepoRefusesInstallationWithoutName`; `TestInstallationVarsCarryFugaroProject`; `TestRepoVarsCarryFugaroProject`; `TestDiscoverRefusesForeignProjectLabel`; `TestDiscoverAdoptsUnlabelledBucket` (the plan adds the label); `TestM4GoldenEnvRenamed` (the golden is regenerated and its README note is present).
  - `internal/localcfg`: `TestNameCheckCacheFreshForADay`, `TestNameCheckCacheIgnoresOtherBucket`.
  - `internal/cli`: `TestOpenCloudRefusesNameMismatch` (the object names `borealis`: exit 1 with the design's message); `TestOpenCloudRefusesGCPProjectMismatch`; `TestOpenCloudMissingMarker` (exit 1, the `init --name` hint); `TestOpenCloudMarkerReadErrorIsRemote` (exit 2); `TestOpenCloudUsesCache` (a second command in the day reads no object); `TestInitNameImmutable` (state output `aurora`, `--name borealis`: refused before any plan); `TestInitNameRequiredWhenUnnamed`; `TestInitConfigOnlyTakesNameFromOutputs`; `TestInitConfigOnlyWithoutProjectName`; `TestExecutionFromEnvReadsGCPProject` (`internal/backend`); `TestCheckJobReadsGCPProject`.
  - Terraform (`-tags terraform`, `terraform test`): `installation.tftest.hcl` asserts the label, the marker object's decoded content and the `project_name` output, and that a bad name fails validation; `repo.tftest.hcl` asserts the env key list now holds `FUGARO_GCP_PROJECT` and `FUGARO_PROJECT`, and that a workflow env whose `FUGARO_PROJECT` differs from `fugaro_project` fails.
- [ ] **Step 2:** `go test ./internal/infra/... ./internal/localcfg/ ./internal/cli/ ./internal/backend/ -run 'PlatformEnv|WithoutName|FugaroProject|ProjectLabel|Unlabelled|M4Golden|NameCheck|OpenCloud|InitName|ConfigOnly|ExecutionFromEnv|CheckJobReads'`, and for each root `r` in `installation repo`: `TF_DATA_DIR=$TMPDIR/tf-$r terraform -chdir=deploy/terraform/gcp/roots/$r init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/$r test` (as CI runs it). Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...`, `go vet -tags live ./...`, `go test -tags terraform ./internal/infra/...`, both roots' `terraform validate` and `terraform test` as in Step 2, `terraform fmt -check -recursive deploy/terraform`, `tflint --chdir=deploy/terraform/gcp --recursive`, `sh deploy/terraform/scan.sh`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "init --name: the project name in the cloud; FUGARO_PROJECT is the name, FUGARO_GCP_PROJECT the ID"`

---

### Task 3: `project:` in `fugaro.yaml`, the runner's check, and `init --repo`

**Files:**
- Modify: `internal/config/config.go` (`Config.Project`), `internal/config/validate.go`, `internal/config/example.yaml`, `internal/config/example.go`, `internal/config/config_test.go`, `schemas/fugaro.schema.json`
- Modify: every fixture under `testdata/config/valid/` (add `project: aurora`); Create: `testdata/config/invalid/project-missing.yaml`, `testdata/config/invalid/project-bad-name.yaml`
- Modify: `testdata/fixture-repo/fugaro.yaml`, `deploy/sandbox/fugaro.yaml` (`project: sandbox`, this repository's copy only), every test that writes a `fugaro.yaml` string (`internal/runner/*_test.go`, `internal/cli/*_test.go`, `internal/e2e/*_test.go`, `internal/image/*_test.go`, `internal/infra/*_test.go`)
- Create: `internal/runner/project.go`, `internal/runner/project_test.go`; Modify: `internal/runner/runner.go` (`Deps.Project`, `Deps.RequireProject`, the call in `bootstrap`, the first run's base fetch moved before the lock)
- Modify: `internal/cli/exec.go` (`FUGARO_PROJECT`, `CLOUD_RUN_JOB`), `internal/cli/exec_test.go`
- Modify: `internal/cli/init.go` (`runInitRepo`: the project rule), `internal/cli/validate.go` (the hint names the selected project), `internal/cli/configcmd.go` (`config example` fills `project:`), and their tests
- Modify: `plugin/skills/onboard/SKILL.md`

**Interfaces:**
- Consumes: T1's `ProjectNameRE`, `ProjectOf`, `Select`; T2's `InstallationOutputs.ProjectName` and the env names.
- Produces:

```go
// internal/config
// Config gains: Project string `yaml:"project"` (top level, after version)
// Validate: missing → Problem{Path: "project", Message: "is required: the Fugaro project
//   this repository belongs to (fugaro config example shows it)"}; bad → "must be a project
//   name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit".
func ExampleFor(project string) []byte // the example with project: <project> (ProjectNameRE), else "example"

// internal/runner
// Deps gains:
//   Project        string // FUGARO_PROJECT: the project this job belongs to
//   RequireProject bool   // on Cloud Run: a missing Project fails bootstrap
func (r *run) checkProject(ctx context.Context, cfg *config.Config) error // R4
```

- **`fugaro init --repo`** requires `project:` and refuses when it differs from the installation's `project_name` ("fugaro.yaml names project borealis, but this installation is project aurora"); a missing key: "fugaro.yaml has no `project:`; add `project: aurora`".
- **`fugaro validate`** reports the missing key with the selected project's name in the hint when a project config is selectable (no cloud call), else the generic hint.
- **`fugaro config example`** prints `project: <name>` for the selected project config, else `project: example` with the comment line `# the Fugaro project; fugaro init --config-only writes your project config`.
- **The onboard skill** runs `fugaro config example` to learn the project name, asks the user when the example shows `example`, and never invents one.

- [ ] **Step 1: Write the failing tests.**
  - `internal/config`: `TestProjectRequired`, `TestProjectBadName` (through the new invalid fixtures; the corpus tests `TestCorpus` and `TestFugaroSchemaCorpus` glob them), `TestExampleForFillsProject`, `TestExampleIsValid` (unchanged, still passes).
  - `internal/runner`: `TestProjectCheckPasses`; `TestProjectCheckReadsBaseNotBranch` (a first run at `--ref feature` whose `fugaro.yaml` says `aurora` while `main`'s says `borealis`: `infra_error`, outcome `none`, the reason names `main` and both projects, no lock object was written); `TestProjectCheckRefAlsoChecked` (the base says `aurora`, the ref says `borealis`: refused); `TestProjectCheckBaseNamesNone`; `TestProjectCheckFollowUpUsesBase`; `TestProjectCheckMissingEnvOnCloudRun` (`RequireProject` and no `Project`: the design's message); `TestProjectCheckSkippedLocally` (no `Project`, not required: passes, one log line); `TestProjectCheckBeforeLock` (the lock object never appears on a refusal); `TestProjectCheckBaseUnparseable` (a base `fugaro.yaml` with an unknown field still yields its `project:`).
  - `internal/cli`: `TestExecPassesProject` (`FUGARO_PROJECT` and `CLOUD_RUN_JOB` reach `Deps`); `TestInitRepoRefusesOtherProject`; `TestInitRepoRequiresProject`; `TestValidateHintNamesProject`; `TestConfigExampleUsesSelectedProject`.
  - `internal/cli`: `TestSkillCommandsExist` keeps passing with the onboard skill's new `fugaro config example` line.
- [ ] **Step 2:** `go test ./internal/config/ ./schemas/ ./internal/runner/ ./internal/cli/ -run 'ProjectRequired|ProjectBadName|ExampleFor|Corpus|ProjectCheck|ExecPassesProject|InitRepo.*Project|ValidateHint|ConfigExample'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Every existing runner test must pass with only its `fugaro.yaml` gaining `project:`.
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "fugaro.yaml names its project; the runner refuses a job of another project"`

---

### Task 4: `internal/pricing`: the price table, costs and the worst case

**Files:**
- Create: `internal/pricing/pricing.go` (types, `Cost`, `Lookup`, `Max`), `internal/pricing/table.go` (the embedded table), `internal/pricing/worst.go` (`WorstCase`), `internal/pricing/overrides.go` (`Overrides`, `ParseOverrides`, `Env`), `internal/pricing/alias.go` (`IsAlias`), and `*_test.go` for each; `internal/pricing/testdata/cost_*.golden`

**Interfaces:**
- Produces:

```go
package pricing

// Micros are integer micro-dollars (µ$). A price of $X per million tokens
// is exactly X µ$ per token, so rates are µ$ per token.
type Micros int64
func (m Micros) USD() float64
func FromUSD(usd float64) (Micros, error) // finite, ≥ 0, ≤ $100,000; rounds to the nearest µ$

type Rates struct {
	InputPerM, OutputPerM                   float64 // USD per million tokens = µ$ per token
	CacheWrite5m, CacheWrite1h, CacheRead   float64 // multipliers of InputPerM
	LongContext                             *Tier   // nil: one flat rate
	WebSearchPer1k                          float64 // USD per 1,000 searches
}
type Tier struct {
	AboveInputTokens      int64   // applies when input + cache writes + cache reads exceed it
	InputPerM, OutputPerM float64
}
type Model struct {
	ID      string
	Aliases []string // the other spellings providers serve it under (e.g. Vertex "<id>@<date>")
	Rates   Rates
}
type Table struct {
	Source    string // the pricing page's URL
	CheckedAt string // YYYY-MM-DD
	Models    map[string]Model
}
func Embedded() *Table
func (t *Table) Lookup(model string) (Model, bool) // exact ID or alias; never a prefix match
func (t *Table) Max() Rates                        // the highest of every rate, for an unknown serving model
func (t *Table) With(o Overrides) (*Table, error)  // replaces or adds whole models

type Usage struct {
	Input, CacheWrite5m, CacheWrite1h, CacheRead, Output, WebSearches int64
}
func (r Rates) Cost(u Usage) Micros // rounded to the nearest µ$

type Request struct {
	BodyBytes    int64
	MaxTokens    int64
	CacheTTL     string // "", "5m" or "1h": the longest TTL any cache_control asks for
	WebSearchCap int64  // 0 when the request has no web_search tool
}
func (r Rates) WorstCase(q Request) Micros // R6, rounded up

type Overrides map[string]Rates
func ParseOverrides(s string) (Overrides, error) // FUGARO_MODEL_PRICES: compact JSON; keys are model IDs
func (o Overrides) Env() (string, error)
func IsAlias(model string) bool                  // sonnet, opus, haiku, opusplan, default, best, any "[1m]" suffix, and other non-ID names
```

- **The embedded table** starts from Anthropic's pricing page as cached on 2026-09-25 and is **re-checked against the live page when this task is implemented** (A11), with `CheckedAt` set to that day. Per-model cache-read multipliers differ (for example $0.20 on a $4 input is 0.05×; on a $2 input 0.1×); write multipliers are 1.25× (5 minutes) and 2× (1 hour). Rows: `claude-opus-5-5` ($4 / $20, read 0.05), `claude-opus-5` ($5 / $25), `claude-sonnet-5-5` ($2 / $10, read 0.1), `claude-sonnet-5` ($2 / $10), `claude-haiku-4-5` ($1 / $5, read 0.1), `claude-fable-5-1` ($10 / $50, read 0.025); web search $10 per 1,000; long-context tiers only where the page lists one. Vertex spellings are aliases; where Vertex's own price differs, owners use `model_prices` (A11). Each row cites its source in a comment.
- **Validation** of rates and overrides: every rate finite and ≥ 0, `InputPerM` and `OutputPerM` > 0 and ≤ 1000, multipliers ≤ 10, tier thresholds > 0.
- Tests never depend on the embedded numbers except `TestEmbeddedTableSane`: they build their own tables.

- [ ] **Step 1: Write the failing tests.** `TestCostGolden` (table over the goldens: plain input and output; 5-minute and 1-hour cache writes; cache reads; web searches; a tier crossed only by adding cache tokens); `TestCostCacheReadPerModel`; `TestCostInputExcludesCache` (`Input` is uncached input only); `TestCostTierUsesTotalInput`; `TestWorstCaseBoundsActual` (property, `testing/quick` or a seeded loop of 10,000 cases: for any `Usage` with `Input + CacheWrite* + CacheRead ≤ BodyBytes`, `Output ≤ MaxTokens`, `WebSearches ≤ WebSearchCap`, and cache writes only when `CacheTTL` allows them, `Cost ≤ WorstCase`); `TestWorstCaseCacheWrite1h`; `TestWorstCaseTierFromBytes`; `TestLookupExactAndAliasOnly` (`claude-sonnet-5` never matches `claude-sonnet-5-5`); `TestMaxRates`; `TestOverridesRoundTrip`; `TestOverridesRejectBad` (NaN, negative, zero input, multiplier 11); `TestWithOverridesReplacesModel`; `TestIsAlias`; `TestFromUSD`; `TestEmbeddedTableSane` (every row validates, `Source` and `CheckedAt` set, no alias shared by two models).
- [ ] **Step 2:** `go test ./internal/pricing/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/pricing/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "pricing: the model price table, costs and the worst-case estimate"`

---

### Task 5: `internal/gateway`: the proxy, the ledger and settlement

**Files:**
- Create: `internal/gateway/gateway.go` (`Start`, `Server`, routing, auth), `internal/gateway/ledger.go` (reserve, settle, halt), `internal/gateway/forward.go` (request parsing, upstream call, unbuffered copy), `internal/gateway/sse.go` (the usage tee), `internal/gateway/vertex.go` (paths, upstream host, token), `internal/gateway/log.go` (the per-call log line), and `*_test.go` for each
- Create: `internal/gateway/anthropicfake/fake.go` and its test: an `httptest` upstream scripted per request (Anthropic and Vertex paths)

**Interfaces:**
- Consumes: T4's `pricing.Table`, `Rates`, `Request`, `Usage`, `Micros`.
- Produces:

```go
package gateway

type Mode string
const (Observe Mode = "observe"; Enforce Mode = "enforce")

type Upstream struct {
	Kind          string // "anthropic" or "vertex"
	BaseURL       string // R5
	APIKey        string // anthropic: the real key
	Token         oauth2.TokenSource // vertex
	VertexProject string // the only project a Vertex path may name
	VertexRegion  string // the only location a Vertex path may name
}
type Options struct {
	Upstream Upstream
	Prices   *pricing.Table
	Mode     Mode
	Cap      pricing.Micros // enforce: > 0
	Log      *slog.Logger   // the runner's, which redacts
	Client   *http.Client   // nil: a default with no overall timeout (streams are long)
}
func Start(ctx context.Context, o Options) (*Server, error) // 127.0.0.1:0

func (s *Server) URL() string   // "http://127.0.0.1:<port>"
func (s *Server) Token() string // 64 hex characters

type Stage struct {
	Name            string   // implement, review, fix
	Models          []string // the role's model and the background model
	MaxOutputTokens int64    // 0: no bound but the request's own
}
func (s *Server) BeginStage(st Stage)
func (s *Server) EndStage() StageReport // waits for the stage's in-flight calls to settle, at most 30 s

type StageReport struct {
	Calls        int
	Used         pricing.Micros            // settled this stage
	ByModel      map[string]pricing.Micros // by serving model
	Unreconciled pricing.Micros            // charged from reservations, not from reported usage
	Overrun      pricing.Micros
	WouldHalt    int                       // observe: calls enforce would have refused
	Violations   []string                  // "model X is not pinned for stage review", "max_tokens N is above the stage's limit M"
}

type Halt struct {
	Reason string // "run_cap"
	Detail string // "run cap $20.00 reached ($19.84 spent, $1.30 needed)"
	At     time.Time
}
func (s *Server) Halted() <-chan Halt // delivers once, then closes

type Ledger struct{ Granted, Used, Reserved pricing.Micros }
func (s *Server) Ledger() Ledger
func (s *Server) Close(ctx context.Context) error // cancels in-flight upstream requests, then stops listening

const DefaultWebSearchCap = 20
const MaxRequestBytes = 32 << 20

// internal/gateway/anthropicfake
type Event struct{ Name string; Data string } // one SSE event, sent as written
type Reply struct {
	Status     int
	Header     http.Header
	Body       string        // a non-streaming body
	Events     []Event       // a streaming body
	CutAfter   int           // > 0: drop the connection after this many events
	EventDelay time.Duration // between events, for the no-buffering test
}
type Fake struct {
	Script []Reply           // one per request, in order
	Seen   []*http.Request   // with their bodies, for byte-equality checks
}
func New(t testing.TB, script ...Reply) (*Fake, *httptest.Server)
func StreamOK(model string, u pricing.Usage) Reply // message_start … message_delta … message_stop
```

- The log line per call (`log.go`): `msg: "model call"`, `stage`, `model`, `serving_model`, `status`, `stream`, `in`, `cache_write_5m`, `cache_write_1h`, `cache_read`, `out`, `web_searches`, `reserved_micros`, `charged_micros`, `priced_as` (`table` | `max`), `settled` (`usage` | `reserved` | `zero`), `session_id`, `agent_id`, and, on an upstream error, `error_type` (the `error.type` field only). Nothing else.

- [ ] **Step 1: Write the failing tests** (each against `anthropicfake`, the gateway's client pointed at it).
  - Routing and auth: `TestHelloIsLocal`, `TestUnknownPath404`, `TestTokenRequiredAnthropic` (401 without, with a wrong one), `TestVertexNoTokenLoopback`, `TestVertexOtherProjectRefused`, `TestCountTokensFreeAndForwarded`.
  - Forwarding: `TestBodyForwardedByteForByte`, `TestHeadersPassThrough` (`anthropic-version`, `anthropic-beta` in; `retry-after`, `x-should-retry`, `anthropic-ratelimit-unified-reset`, `request-id` out), `TestGatewayStripsClientCredentials` (the upstream sees the real key, never the token), `TestStreamNotBuffered` (with `EventDelay` 200 ms, the client reads the first event before the second is sent), `TestPingsPassThrough`, `TestErrorBodyPassThrough` (429 and 529 with their bodies and headers), `TestNonStreamingUsage`, `TestRequestTooLarge413`, `TestMissingMaxTokens400NotForwarded`.
  - Pins: `TestUnpinnedModelRefused400` (the message names the model and stage; nothing forwarded; a violation reported), `TestBackgroundModelAllowed`, `TestAliasOfPinnedAllowed`, `TestMaxTokensAboveRoleLimitRefused`, `TestVertexModelFromPath`.
  - Money: `TestSettleCompleteStream`, `TestMessageDeltaCumulativeLastWins`, `TestErrorBeforeMessageStartChargesZero`, `TestDisconnectAfterStartChargesReservedOutput` (`CutAfter`: input plus reserved output, counted as `unreconciled`), `TestClientCancelCancelsUpstream` (the fake sees its request context end), `TestRetryAfter529ChargesOnce`, `TestServingModelPricesCall`, `TestUnknownServingModelPricedAtMax` (overrun counted), `TestWebSearchReservedWithoutMaxUses`, `TestCacheControl1hReservesAt2x`.
  - Caps: `TestEnforceRefusesPastCap` (403, `x-should-retry: false`, `permission_error`, the message), `TestHaltIsSticky`, `TestHaltedFiresOnce`, `TestObserveNeverRefuses` (`WouldHalt` counted, a log line), `TestLedgerNeverExceedsGranted` (property: 50 goroutines, random bodies and usages from `StreamOK`, `-race`; after every settle `Used + Reserved ≤ Granted` unless a `priced_as: max` overrun happened), `TestInFlightCallsFinishAfterHalt`.
  - Logs: `TestGatewayLogFields` (exactly the listed attributes), `TestGatewayLogsNoSecrets`.
  - Lifecycle: `TestEndStageWaitsForInFlight`, `TestCloseCancelsInFlight`, `TestStartListensOnLoopbackOnly`.
- [ ] **Step 2:** `go test ./internal/gateway/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** Use `http.NewResponseController(w).Flush()` after every write. The usage tee parses SSE lines from a copy of what it has already written, so a slow parser never delays the client; a line over 1 MiB is skipped (usage events are small). The Vertex token source is called per request (it caches).
- [ ] **Step 4:** `go test -race ./internal/gateway/... ./internal/pricing/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "gateway: pinned models, worst-case reservation, stream settlement and a per-run cap"`

---

### Task 6: Per-stage models and the pin rules in `fugaro.yaml`

**Files:**
- Modify: `internal/config/config.go` (`Agent.Models`, `Agent.MaxOutputTokens`, `Agent.MaxRunTokens`, `ModelFor`, `StageRole`), `internal/config/validate.go`, `internal/config/example.yaml`, `internal/config/config_test.go`; Create: `internal/config/pins.go`, `internal/config/pins_test.go`
- Modify: `schemas/fugaro.schema.json`; Create: `testdata/config/valid/agent-models.yaml`, `testdata/config/invalid/agent-{models-unknown-role,max-output-negative,max-run-tokens-negative}.yaml`
- Modify: `internal/task/task.go` (`Apply`: `overrides.model` sets `Agent.Models.Coder`), `internal/task/task_test.go`
- Modify: `internal/cli/validate.go` (the budget rules when the selected project's budget is on), `internal/cli/validate_test.go`

`validate`'s budget-on check reads `lc.BudgetMode()` from T8, which lands first.

**Interfaces:**
- Consumes: T4's `pricing.Table`, `IsAlias`; T3's `Config.Project`; T8's `localcfg.Config.BudgetMode()` and `Overrides()` (for `validate`).
- Produces:

```go
// internal/config
type ModelRoles struct {
	Coder      string `yaml:"coder"`      // implement and fix
	Reviewer   string `yaml:"reviewer"`   // review
	Background string `yaml:"background"` // Claude Code's small background requests
}
type RoleTokens struct {
	Coder    int64 `yaml:"coder"`
	Reviewer int64 `yaml:"reviewer"`
}
// Agent gains:
//   Models          ModelRoles `yaml:"models"`
//   MaxOutputTokens RoleTokens `yaml:"max_output_tokens"` // per call; 0 = no limit
//   MaxRunTokens    int64      `yaml:"max_run_tokens"`    // 0 = none (design §5.8)
type Role string
const (RoleCoder Role = "coder"; RoleReviewer Role = "reviewer")
func StageRole(stage string) Role                // implement, fix → coder; review → reviewer
func (a Agent) ModelFor(r Role) string            // models.<role>, else agent.model (task overrides are applied to Models.Coder by task.Apply)
func (a Agent) MaxOutputFor(r Role) int64
// CheckPins is R9's budget-on rule: every role and the background model an
// explicit ID in prices, no alias. Problems carry paths such as agent.models.reviewer.
func CheckPins(a Agent, prices *pricing.Table) []Problem
```

- `task.Apply` (`internal/task/task.go`) sets `Agent.Models.Coder` from `overrides.model` (it sets `Agent.Model` today): the override means the coder model (design §2.1). Modify `internal/task/task.go` and its test accordingly.
- Validation without a budget: `max_output_tokens.*` 0 to 128,000; `max_run_tokens` ≥ 0; model strings non-empty when present, at most 100 characters, no whitespace. With a budget (only `fugaro validate` with a selected project config whose `budget.mode` isn't `off`, and the runner in T10): `CheckPins` too.

- [ ] **Step 1: Write the failing tests.** `TestModelForFallsBack`, `TestStageRole`, `TestMaxOutputFor`, `TestAgentModelsValidation` (through the fixtures), `TestCheckPinsAliasRefused`, `TestCheckPinsUnknownIDRefused`, `TestCheckPinsBackgroundRequired`, `TestCheckPinsOverrideTable` (an ID only in overrides passes), `TestTaskOverrideModelIsCoder` (`internal/task`), `TestValidateAppliesPinsWhenBudgetOn`, `TestValidateSkipsPinsWhenBudgetOff` (`internal/cli`), `TestExampleIsValid`.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/task/ ./schemas/ ./internal/cli/ -run 'ModelFor|StageRole|MaxOutputFor|AgentModels|CheckPins|OverrideModel|Validate.*Pins|Example|Corpus'`. Expected: FAIL.
- [ ] **Step 3: Implement.** The example documents `agent.models`, `max_output_tokens` and `max_run_tokens`, commented out, with one line each on what the budget requires.
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "config: models per stage, output and run token limits, and the pin rules"`

---

### Task 7: The `halted` status, halts in the runner, and the token cap

**Files:**
- Modify: `internal/runstore/runstore.go` (`StatusHalted`, `Halt`, `HaltReason`, `Record.Halt`), `internal/runstore/cost.go` (`ModelSource`, `ModelBy`, `Unreconciled`), `internal/runstore/runstore_test.go`, `schemas/result.schema.json`, `schemas/schemas_test.go`
- Modify: `internal/agent/agent.go` (`Result.Usage`), `internal/agent/stream.go` (the result event's `usage`), `internal/agent/agent_test.go`, `internal/agent/testdata/stream-success.jsonl`
- Create: `internal/runner/halt.go`, `internal/runner/halt_test.go`; Modify: `internal/runner/runner.go` (`Run`'s bootstrap-halt branch, `stage`'s cause-aware context, `agentLoop`'s token check, `finalize`'s status and leftover message), `internal/runner/budget.go` (`StageError`'s halted case), `internal/runner/report.go` (the halted line), `internal/runner/followup.go` (M6's no-push endings keep `halted`)
- Modify: `internal/runview/runview.go` (a `Halt` field on `Row`), `internal/runview/runview_test.go`, `internal/cli/ls.go` (the reason column shows the halt), `internal/cli/diagnose.go` (a `Halt` block), `internal/cli/{ls,diagnose,cancel,followup,exec}_test.go`

**Interfaces:**
- Consumes: T6's `Agent.MaxRunTokens`; T3's runner shape.
- Produces:

```go
// internal/runstore
const StatusHalted Status = "halted"
type HaltReason string
const (
	HaltKillSwitch HaltReason = "kill_switch"; HaltRunCap = "run_cap"; HaltRepoDailyCap = "repo_daily_cap"
	HaltGlobalDailyCap = "global_daily_cap"; HaltNoCap = "no_cap"; HaltTokenCap = "token_cap"
	HaltBudgetUnavailable = "budget_unavailable"; HaltBudgetTokenExpired = "budget_token_expired"
)
type Halt struct {
	Reason HaltReason `json:"reason"`
	Scope  string     `json:"scope"` // run | repo | global
	At     time.Time  `json:"at"`
	Detail string     `json:"detail,omitempty"`
}
// Record gains: Halt *Halt `json:"halt,omitempty"`
// Cost gains:
//   ModelSource  string             `json:"model_source,omitempty"` // gateway | claude-code
//   ModelBy      map[string]float64 `json:"model_by,omitempty"`     // USD by model
//   Unreconciled float64            `json:"unreconciled,omitempty"` // USD charged from reservations

// internal/agent
type Usage struct{ Input, CacheCreation, CacheRead, Output int64 }
func (u Usage) Total() int64
// Result gains: Usage Usage (the result event's "usage": input_tokens,
// cache_creation_input_tokens, cache_read_input_tokens, output_tokens)

// internal/runner
var ErrHalted = errors.New("run halted")
type HaltError struct{ Halt runstore.Halt }
func (e *HaltError) Error() string // "halted: <reason>: <detail>"
func (e *HaltError) Unwrap() error // ErrHalted
// run gains:
//   halt       *runstore.Halt
//   tokens     int64                   // Σ stage usage, for the token cap
//   haltStage  context.CancelCauseFunc // the running stage's cancel, nil between stages
func (r *run) haltNow(h runstore.Halt) // records h unless a cancel or an earlier halt came first; cancels the running stage with &HaltError{h}
```

- **`stage`** builds its context with `context.WithCancelCause` under the time budget's deadline, stores the cancel in `r.haltStage`, and, when the agent returns, checks `context.Cause(stageCtx)`: `ErrCancelled` → as today; `ErrHalted` → `r.fail(reason)`, no log tail kept, `(res, false, err)`.
- **`agentLoop`** after every stage: `r.tokens += res.Usage.Total()`; `MaxRunTokens > 0 && r.tokens >= MaxRunTokens` → `haltNow({token_cap, run, now, "run used N tokens of M"})`, stop.
- **`finalize`**: with `r.halt` set, the leftover commit message is R7's, `ready` is false, status `halted`, outcome `draft` (pushed) or `none` (not pushed), and the record's `Halt` is set. `StageError` gains "halted during <stage>: <detail>".
- **`Run`**: `bootstrap` returning an `ErrHalted` error sets status `halted`, outcome `none`, `Halt`, reason, and returns `(rec, nil)`. T7 gives bootstrap no halt of its own (T10 adds `no_cap`); `halt_test.go` drives the branch through an unexported test hook in `export_test.go` (`SetBootstrapHaltForTest`).
- **The report** adds R8's line under the heading; `ls` shows `halted` with the reason; `diagnose` prints `Halted:   <reason> (<scope>) at <time>: <detail>` and `--json` carries `halt`; `cancel` on a halted run says it already finished (existing logic, now tested); a halted run on a PR is a valid `previous_run` for `run --pr` when it pushed (M6's rule, now tested).

- [ ] **Step 1: Write the failing tests.**
  - `internal/runstore`, `schemas`: `TestRecordHaltRoundTrip` (and an M6 record still parses), `TestCostModelFieldsRoundTrip`, the schema accepts `status: halted` with a `halt` block and each of the eight reasons, and refuses `reason: "other"`.
  - `internal/agent`: `TestParseStreamUsage` (the fixture's result event gains a `usage` object), `TestParseStreamNoUsage`.
  - `internal/runner`: `TestHaltMidImplementOpensDraft` (the hook halts during implement: `halted`/`draft`, the report's second line, the leftover commit's message), `TestHaltLeftoversCommitMessage`, `TestHaltNeverReady`, `TestHaltAfterCancelKeepsCancelled`, `TestCancelAfterHaltKeepsHalted`, `TestTokenCapHaltsAtStageBoundary` (implement uses 60 of 100, review 50: halted after review, before fix; detail names both numbers), `TestTokenCapZeroMeansNone`, `TestBootstrapHaltIsHaltedExitZero` (`Run` returns a nil error, outcome `none`, no branch pushed, no lock taken), `TestFollowUpHaltBeforePushIsNone`, `TestHaltRecordedInResult`.
  - `internal/cli`: `TestExecBootstrapHaltExitsZero`, `TestLsShowsHalted`, `TestDiagnoseHaltBlock`, `TestCancelHaltedRunAlreadyFinished`, `TestRunPRAfterHaltedRun`.
- [ ] **Step 2:** `go test ./internal/runstore/ ./schemas/ ./internal/agent/ ./internal/runner/ ./internal/runview/ ./internal/cli/ -run 'Halt|TokenCap|Usage|CostModelFields|LsShowsHalted|DiagnoseHalt|CancelHalted|RunPRAfterHalted'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Every existing runner, `ls`, `diagnose` and `cancel` test passes unchanged.
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: the halted status, halts at stage and call boundaries, and the token cap"`

---

### Task 8: The budget in the project config and the job's environment

**Files:**
- Modify: `internal/localcfg/localcfg.go` (`Budget`, `ModelPrices`, validation), `internal/localcfg/localcfg_test.go`
- Modify: `internal/infra/spec.go` (`workflow`: the budget env), `internal/infra/spec_test.go`
- Create: `internal/runner/spend.go`, `internal/runner/spend_test.go`

**Interfaces:**
- Consumes: T2's `localcfg` and `infra` shapes; T4's `pricing.Overrides`, `FromUSD`.
- Produces:

```go
// internal/localcfg
type Budget struct {
	Mode      string  `yaml:"mode"`        // off | observe | enforce; "" is off
	PerRunUSD float64 `yaml:"per_run_usd"` // enforce: 0 < x ≤ 100000; observe: ≥ 0
}
type ModelPrice struct {
	InputPerM      float64 `yaml:"input_per_m"`
	OutputPerM     float64 `yaml:"output_per_m"`
	CacheWrite5m   float64 `yaml:"cache_write_5m"`   // default 1.25
	CacheWrite1h   float64 `yaml:"cache_write_1h"`   // default 2
	CacheRead      float64 `yaml:"cache_read"`       // default 0.1
	WebSearchPer1k float64 `yaml:"web_search_per_1k"` // default 10
}
// Config gains:
//   Budget      *Budget               `yaml:"budget,omitempty"`
//   ModelPrices map[string]ModelPrice `yaml:"model_prices,omitempty"`
func (c *Config) BudgetMode() string                      // "off" when absent
func (c *Config) Overrides() (pricing.Overrides, error)

// internal/infra: workflow env, when BudgetMode() != "off":
//   FUGARO_BUDGET_MODE, FUGARO_MAX_RUN_USD (strconv 'f', -1; only when > 0),
//   FUGARO_MODEL_PRICES (pricing.Overrides.Env(); only when set)
const (BudgetModeEnv = "FUGARO_BUDGET_MODE"; MaxRunUSDEnv = "FUGARO_MAX_RUN_USD"; ModelPricesEnv = "FUGARO_MODEL_PRICES")

// internal/runner
type Spend struct {
	Mode   string         // off | observe | enforce
	Cap    pricing.Micros // 0: none
	Prices *pricing.Table // Embedded().With(overrides)
}
func SpendFromEnv(getenv func(string) string) (Spend, error) // malformed → error (bootstrap infra_error)
func (s Spend) On() bool
```

- [ ] **Step 1: Write the failing tests.** `TestBudgetDefaultsOff`, `TestBudgetValidation` (unknown mode; enforce without a cap; a negative or NaN cap; a cap over $100,000; bad model prices; an unknown field), `TestModelPricesDefaults` (`internal/localcfg`); `TestWorkflowEnvBudgetOff` (none of the three variables), `TestWorkflowEnvBudgetEnforce`, `TestWorkflowEnvBudgetObserveNoCap`, `TestCheckJobHasNoBudgetEnv` (`internal/infra`); `TestSpendFromEnv` (table: unset → off; enforce and cap; observe; bad mode; bad cap; bad prices JSON; an override replacing a model) (`internal/runner`).
- [ ] **Step 2:** `go test ./internal/localcfg/ ./internal/infra/ ./internal/runner/ -run 'Budget|ModelPrices|WorkflowEnvBudget|CheckJobHasNoBudget|SpendFromEnv'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "budget: mode, per-run cap and price overrides from the project config to the jobs"`

---

### Task 9: The agent's environment, pins per stage, managed settings, and the base image

**Files:**
- Modify: `internal/agent/env.go` (`EnvSpec.Gateway`), `internal/agent/agent_test.go` (or a new `env_test.go`); Create: `internal/agent/settings.go`, `internal/agent/settings_test.go`
- Modify: `internal/runner/runner.go` (`Deps.ManagedSettingsPath`; `stage` writes the settings and adds the stage's pins to the env; `bootstrap` checks the repository's settings when the gateway is on), `internal/runner/runner_test.go`
- Modify: `images/web-node/Dockerfile` (`/etc/claude-code`), `internal/image/selftest.go` (the check), `internal/image/selftest_test.go` (or its existing test file), `images/base_docker_test.go` (`-tags docker`)

**Interfaces:**
- Consumes: T5's `Server.URL()`, `Token()`; T6's `StageRole`, `ModelFor`, `MaxOutputFor`; T7's `stage` shape.
- Produces:

```go
// internal/agent
type Gateway struct{ BaseURL, Token string }
// EnvSpec gains: Gateway *Gateway
//   api-key: ANTHROPIC_BASE_URL=BaseURL, ANTHROPIC_API_KEY=Token; the runner's
//            ANTHROPIC_API_KEY is returned in secretValues and kept out of env.
//   vertex:  CLAUDE_CODE_USE_VERTEX=1, ANTHROPIC_VERTEX_BASE_URL=BaseURL+"/v1",
//            CLAUDE_CODE_SKIP_VERTEX_AUTH=1, CLOUD_ML_REGION, ANTHROPIC_VERTEX_PROJECT_ID;
//            the parent's ANTHROPIC_VERTEX_BASE_URL is never passed through.
//   oauth:   Gateway must be nil (an error otherwise).
func GatewayVars(auth string, g Gateway, parent map[string]string) (map[string]string, error) // the same variables, for the settings file

// PinVars are a stage's pins (design §2.1): ANTHROPIC_DEFAULT_OPUS_MODEL,
// ANTHROPIC_DEFAULT_SONNET_MODEL, CLAUDE_CODE_SUBAGENT_MODEL = model;
// ANTHROPIC_DEFAULT_HAIKU_MODEL = background (when set);
// CLAUDE_CODE_MAX_OUTPUT_TOKENS = maxOutput (when > 0). Empty model: nil.
func PinVars(model, background string, maxOutput int64) map[string]string

const ManagedSettingsPath = "/etc/claude-code/managed-settings.json"
// WriteManagedSettings replaces path with {"env": env}, 0644, atomically;
// it refuses a symlink or a non-regular file at path.
func WriteManagedSettings(path string, env map[string]string) error
// RoutingKeys lists what a repository settings file sets that would reroute
// Claude Code (R10): apiKeyHelper, and env keys of the R10 list, any case.
func RoutingKeys(settings []byte) ([]string, error)
```

- **The runner, per stage** (`stage`): `role := config.StageRole(name)`; `model := r.cfg.Agent.ModelFor(role)`; `req.Model = model`; the stage env is `r.env` plus `PinVars(model, background, maxOut)`; when the gateway is on or any pin applies, `WriteManagedSettings(r.d.ManagedSettingsPath, gatewayVars ∪ pins)` before the agent starts. A write failure: with the gateway, the stage fails ("writing Claude Code's managed settings: …"); with pins only, a warning.
- **The runner, at bootstrap, with the gateway on:** `RoutingKeys` of `.claude/settings.json` and `.claude/settings.local.json` in the checkout (absent files are fine; a file over 1 MiB or a symlink is refused); any key → `infra_error` "the repository's .claude/settings.json sets ANTHROPIC_BASE_URL, which would route Claude Code around Fugaro's gateway; remove it". T9 adds the check function and its call guarded by `r.gatewayOn()` (a method T10 implements; until then it returns false and a test hook forces it).
- **The base image:** as root, `install -d -o fugaro -g fugaro -m 0755 /etc/claude-code`. `fugaro image selftest` adds `managed-settings-dir`: `/etc/claude-code` exists, is a directory, not a symlink, and is writable by the runner's user.

- [ ] **Step 1: Write the failing tests.**
  - `internal/agent`: `TestBuildEnvGatewayDropsRealKey` (api-key: env has the token and the URL, not the key; `secretValues` has the key), `TestBuildEnvGatewayVertex` (the base URL ends in `/v1`, skip-auth set, the parent's base URL dropped), `TestBuildEnvGatewayOAuthRefused`, `TestBuildEnvNoGatewayUnchanged` (every existing `BuildEnv` test still passes), `TestPinVars` (table: model only; with background; with max output; empty), `TestWriteManagedSettingsAtomic`, `TestWriteManagedSettingsRefusesSymlink`, `TestManagedSettingsHoldNoRealKey`, `TestRoutingKeysRefused` (table: each key, lower case, `apiKeyHelper`, nested `env` only, invalid JSON → error), `TestRoutingKeysAllowsModelSettings` (`ANTHROPIC_MODEL`, `permissions`, hooks are not routing keys).
  - `internal/runner`: `TestStagePinsPerRole` (implement and fix get the coder pins; review the reviewer's; `--model` follows), `TestManagedSettingsBeforeEveryStage` (the scripted agent reads the file at its start: it holds the stage's pins), `TestPinsWithoutBudgetWarnOnWriteFailure`, `TestRepoSettingsRerouteRefused` (hook forces the gateway on; `.claude/settings.json` sets `ANTHROPIC_BASE_URL`: `infra_error` before the lock), `TestRepoSettingsIgnoredWithoutGateway`.
  - `internal/image`: `TestSelftestManagedSettingsDir` (present and writable; missing; a symlink; not writable).
  - `images` (`-tags docker`): `TestBaseImageManagedSettingsDir`.
- [ ] **Step 2:** `go test ./internal/agent/ ./internal/runner/ ./internal/image/ -run 'BuildEnvGateway|NoGatewayUnchanged|PinVars|ManagedSettings|RoutingKeys|StagePins|PinsWithoutBudget|RepoSettings|Selftest'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...` and `go vet -tags docker ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "agent: gateway environment without the real key, per-stage pins, managed settings"`

---

### Task 10: The runner drives the gateway

**Files:**
- Create: `internal/runner/gateway.go`, `internal/runner/gateway_test.go`; Modify: `internal/runner/runner.go` (`Deps.Spend`, `Deps.GatewayUpstream`, `Deps.VertexTokens`; bootstrap's `no_cap` halt, pin check, gateway start; `stage`'s `BeginStage`/`EndStage`, halt watcher and cost; close after the loop), `internal/runner/cost.go` (`model_source`, `model_by`, `unreconciled`), `internal/runner/runner_test.go` (the scripted agent can make model calls)
- Modify: `internal/cli/exec.go` (`SpendFromEnv`; hidden `--gateway-upstream`, loopback only), `internal/cli/exec_test.go`
- Modify: `internal/agent/fakeclaude/main.go` (scripted model calls), `internal/e2e/cloud_test.go` (a hermetic `api-key` cloud run with a fake upstream)

**Interfaces:**
- Consumes: T5's `gateway`; T7's `haltNow`, `HaltError`, the cost fields; T8's `Spend`; T9's `Gateway`, `GatewayVars`, `PinVars`, `WriteManagedSettings`, `RoutingKeys`, `ManagedSettingsPath`; T6's `CheckPins`.
- Produces:

```go
// internal/runner
// Deps gains:
//   Spend           Spend              // from SpendFromEnv; zero value is off
//   GatewayUpstream string             // tests only: http://127.0.0.1:<port>; "" = the real API
//   VertexTokens    oauth2.TokenSource // nil: google.DefaultTokenSource (the metadata server)
//   ManagedSettingsPath string         // "" = agent.ManagedSettingsPath (T9)
const haltGrace = 60 * time.Second
func (r *run) gatewayOn() bool // Spend.On() and auth is api-key or vertex
func (r *run) startGateway(ctx context.Context) error
func (r *run) watchHalt(stageCtx context.Context, done <-chan struct{}) // R7

// internal/agent/fakeclaude: a call gains
//   API []apiCall `json:"api"`
// type apiCall struct {
//   Model      string `json:"model"`
//   MaxTokens  int64  `json:"max_tokens"`
//   BodyBytes  int    `json:"body_bytes"`   // padded prompt size
//   CacheTTL   string `json:"cache_ttl"`    // "", "5m", "1h"
//   Stream     bool   `json:"stream"`
//   Parallel   int    `json:"parallel"`     // > 1: that many at once
// }
// Each goes to $ANTHROPIC_BASE_URL/v1/messages with x-api-key $ANTHROPIC_API_KEY,
// or to $ANTHROPIC_VERTEX_BASE_URL's streamRawPredict path. A response with
// x-should-retry: false and status ≥ 400 ends fakeclaude as Claude Code does:
// an error result event, exit 1. calls.jsonl records each status.
```

- **Bootstrap order** (T10's additions in bold): read the task; claim the record; clone and check out; read `fugaro.yaml`; the project check (T3); **the `no_cap` check (R8: `api-key` or `vertex`, `enforce`, `Spend.Cap == 0` → `haltNow` and return the `HaltError`)**; lock; provider; **with `gatewayOn()`: `CheckPins` against `Spend.Prices` (`infra_error` on a problem), `RoutingKeys` (T9), `startGateway`, the gateway's token registered with the redactor**; the agent env, **built with `EnvSpec.Gateway` when the gateway runs**.
- **The live test's knob** (for T11): `FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1` skips the `RoutingKeys` refusal, and only when `CLOUD_RUN_JOB` is unset and `Deps.GatewayUpstream` is empty (a local run against the real API). It exists so check 20 can measure A6 itself; on Cloud Run it is ignored with a warning.
- **Per stage:** `BeginStage({name, [model, background], maxOut})`; the halt watcher: on `Halted()`, `haltNow` records the halt at once, waits for the agent to exit or `haltGrace`, then cancels the stage with the halt as its cause; after the agent returns: `EndStage()`; `rec.CostUSD += report.Used.USD()` (not `res.CostUSD`); `model_by` and `unreconciled` accumulate; when `|res.CostUSD − report.Used| > 5%` of the larger, one warning log line (`msg: "cost cross-check"`, both figures); `Violations` → `r.fail("stage review: " + v)` and `failed` (D8), unless a halt came first.
- **After the loop:** `Close`, with a 10 s timeout. `model_source` is `gateway` for the whole run whenever the gateway ran.
- **`exec`:** `Spend` from the environment; a malformed value is an `infra_error` record, exit 2; `--gateway-upstream` refuses anything but `http://127.0.0.1:<port>` (exit 1).

- [ ] **Step 1: Write the failing tests** (the runner harness with the scripted agent, which now can call the gateway through `req.Env`, and `anthropicfake` as the upstream).
  - `TestNoCapHaltAtBootstrap` (api-key, enforce, no cap: `halted`/`none`, `no_cap`, nil error, no lock, no push); `TestOAuthEnforceWithoutCapIsNotNoCap` (oauth runs normally, no gateway started); `TestOAuthNeverProxied` (oauth with the budget on: the agent env has no `ANTHROPIC_BASE_URL`, and the managed settings file holds only pins).
  - `TestGatewayCostReplacesClaudeCost` (`cost_usd` is the gateway's; `model_source: gateway`; `model_by` per model); `TestCostCrossCheckWarns`; `TestUnreconciledRecorded`.
  - `TestUnpinnedModelFailsStage` (`failed`, not `halted`; the reason names the model and stage; the run still opens a draft); `TestBackgroundModelPinned`; `TestPinsInvalidForBudgetIsInfraError`.
  - `TestRunCapHaltOpensDraft` (a cap of $0.10 and calls of $0.04: the third call is refused; `halted`/`draft`, `run_cap`, the report's line names spend and cap); `TestHaltWaitsForCallBoundary` (the agent exits by itself after the 403: no SIGTERM was sent, the transcript is complete); `TestHaltGraceThenKill` (an agent that ignores the 403: killed after `haltGrace`, shortened in the test through an unexported variable); `TestInFlightCallsFinishAfterHalt`; `TestObserveModeNeverHalts` (same script, observe: `succeeded`, `WouldHalt` logged).
  - `TestManagedSettingsWrittenBeforeFirstStage` (the file exists with the gateway URL before the first agent start; it never holds the real key); `TestRepoSettingsRerouteRefused` (now with the real `gatewayOn`); `TestTestKnobIgnoredOnCloudRun` (the knob with `CLOUD_RUN_JOB` set: still refused, one warning); `TestTestKnobLocal` (without it: the run proceeds).
  - `TestGatewayRunSecretScan` (a planted key `sk-ant-TESTKEY…`: absent from every bucket object, every log line, every transcript, the scripted agent's env, and the settings file; present in the upstream's `Seen` headers only).
  - `TestGatewayClosedBeforeFinalize` (finalize's provider calls happen after `Close`; a model call during finalize would be refused with connection refused).
  - `internal/cli`: `TestExecSpendFromEnv`, `TestExecRejectsBadSpendEnv`, `TestExecGatewayUpstreamLoopbackOnly`.
  - `internal/e2e`: `TestCloudGatewayRunSecretScan` (a hermetic cloud run through `gcpfake` with `auth: api-key`, a planted key secret, `fakeclaude` calling the gateway, `anthropicfake` via `--gateway-upstream`: the run ends `succeeded`; a second run with a cap below one call ends `halted`, `run_cap`, draft; the key is in no bucket object and no log).
- [ ] **Step 2:** `go test ./internal/runner/ ./internal/cli/ ./internal/e2e/ -run 'NoCap|OAuthEnforce|OAuthNever|GatewayCost|CrossCheck|Unreconciled|Unpinned|BackgroundModel|PinsInvalid|RunCap|HaltWaits|HaltGrace|InFlight|ObserveMode|ManagedSettingsWritten|RepoSettingsReroute|SecretScan|GatewayClosed|ExecSpend|ExecRejectsBadSpend|GatewayUpstream'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Every existing runner test passes unchanged: with `Spend` zero, no gateway starts and nothing in the record changes but `model_source: claude-code`.
- [ ] **Step 4:** `go test -race ./...` and `go vet -tags docker ./... && go vet -tags live ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: route api-key and vertex runs through the gateway, halt at the per-run cap"`

---

### Task 11: Docs and the live gateway test

**Files:**
- Modify: `docs/design/v1.md` (§1's `project` note becomes the current rule; §3.4 the execution identity's `FUGARO_GCP_PROJECT`; §4.1 bootstrap order with the project check and the gateway; §4.5 halts; §4.6 `halted`, `halt`, the cost fields; §5.1 `project:`, `agent.models`, `max_output_tokens`, `max_run_tokens`; §5.4 project configs, `gcp_project`, `name`, `budget`, `model_prices`; §6.1 the gateway, the real key, D2's residual; §9.1 `--project`, `--gcp-project`, the header, `init --name`; §10 and §10.1 `model_source`)
- Modify: `docs/gcp-setup.md` (every `--project` → `--gcp-project`; `init --name`; project configs; turning the budget on), `docs/gcp-live-checklist.md` (`FUGARO_LIVE_GCP_PROJECT`, the project configs in the preconditions, check 20 below, a "Results of the fifth live run (M9a)" section to fill in)
- Modify: `plugin/skills/followup/SKILL.md` (a halted PR can be followed up after the cap is raised)
- Create: `internal/e2e/live_gateway_test.go` (`//go:build live && docker`)

**Interfaces:**
- Consumes: everything.
- Produces: `TestLiveGateway` (check 20). Inputs: `FUGARO_LIVE_BASE_IMAGE` (a locally built base image from this branch), `FUGARO_LIVE_ANTHROPIC_API_KEY` (the user's own key; the test refuses to run without `FUGARO_LIVE_SPEND_OK=1`). It runs `fugaro exec` **locally in that image** (`docker run`, no GCP), against a `file://` bucket, the fake git provider and a small fixture repository with `auth: api-key`, pinned `claude-haiku-4-5` for every role (the cheapest model), `review_rounds: 1`, budget `enforce` with a $0.30 cap, and a repository `.claude/settings.json` that sets `ANTHROPIC_BASE_URL` to `http://127.0.0.1:9`, with T10's `FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1` set inside the container so the refusal steps aside and A6 itself is measured. It then asserts, logging each answer as a `FACT`:
  1. the run reached the model through the gateway (the gateway logged calls; the run didn't fail on connection refused): **A6**;
  2. every Claude Code call the gateway saw used a pinned model, and nothing else was attempted (no violations): **A-N6**;
  3. the gateway's settled cost and Claude Code's `total_cost_usd` agree within 5%: **A10, A11**;
  4. `max_tokens` never exceeded `CLAUDE_CODE_MAX_OUTPUT_TOKENS`: **A9** (Anthropic side);
  5. a second run with a $0.002 cap halts `run_cap`, `claude` exited on its own within `haltGrace` after the 403: **A-N1**; its result event carried `usage`: **A-N2**;
  6. the planted real key is in no transcript, log or bucket object.

- [ ] **Step 1:** Write the docs. Then `go test ./internal/cli/ -run TestSkillCommandsExist`, `go test ./internal/config/ -run Example`, and `grep -rn -- '--project ' docs/gcp-setup.md docs/gcp-live-checklist.md` shows only `--project <name>` uses.
- [ ] **Step 2:** Write `live_gateway_test.go`. `go vet -tags 'live docker' ./internal/e2e/`. Expected: PASS (it isn't run here).
- [ ] **Step 3: Commit.** `git commit -m "docs: project identity, the gateway and halted runs; the live gateway check"`

---

### Task 12: The one-time migration and live verification (controller-run, with user confirmations)

This task writes no code. The controller runs it one step at a time and asks the user before every **⚠ CONFIRM** step. Each step is its own question: one approval never covers the next. Record every outcome and `FACT` in the M9a PR. Anything that fails goes back to its task as a bug, with a hermetic test first.

**Freeze launches for the whole task.** Between the base image rebuild and each repository's `init --repo`, a new runner meets an old job environment and refuses at bootstrap, wasting the run.

**Preconditions (read-only):**
- With the **old** binary (from `main` before M9a), `fugaro ls --since 1d` shows no active run in either repository.
- `FUGARO` is built from `m9a`. `gcloud config get project` is not relied on (memory: never rely on the gcloud default project); every `gcloud` command passes `--project <gcp-id>`.
- The user has chosen the canonical project name `<slug>` (Open question 2).

**Steps (design §13.1):**
1. **The local config, by hand** (local file only; no confirmation needed, but show the diff): `mkdir -p ~/.config/fugaro/projects`; copy `config.yaml` to `projects/<slug>.yaml`; rename `project:` to `gcp_project:`; add `name: <slug>`; move `config.yaml` to `config.yaml.bak`. Check: `$FUGARO validate` in a checkout fails only on the missing `project:`, and `$FUGARO ls` outside a checkout fails only on the missing `fugaro/project.json` (R3), which proves the file parses.
2. **`project: <slug>` in each repository's `fugaro.yaml`,** on its base branch:
   - **the sandbox:** ⚠ CONFIRM the controller commits `project: <slug>` to `acme/sandbox`'s base branch, as in earlier milestones;
   - **the web repository:** the **user** opens and merges their own PR with that one line; the controller waits until the user says it's merged, then reads it back (`git show origin/<base>:fugaro.yaml` in a fresh fetch).
3. **The base image:** ⚠ CONFIRM build and push the new base from `m9a`: `images/build-base.sh web-node <region>-docker.pkg.dev/<gcp-id>/fugaro-base/fugaro-web-node:dev-<commit>`, then `docker push`.
4. **The installation:** ⚠ CONFIRM `$FUGARO init --name <slug> --base-image <that tag>` (shows the plan: the bucket label, the marker object, the output, `base_image`; the user confirms the apply). Check: `projects/<slug>.yaml` still has `name: <slug>`; `gsutil cat gs://<runs bucket>/fugaro/project.json` (read-only) shows the name and GCP ID.
5. **Each repository, from its checkout,** in turn: ⚠ CONFIRM `$FUGARO image build` (the derived image on the new base, about $0.05); then ⚠ CONFIRM `$FUGARO init --repo` (the plan shows `FUGARO_PROJECT=<slug>` and `FUGARO_GCP_PROJECT=<id>` on every job and the check job, and nothing budget-related: both repositories use `oauth`, and no `budget:` block is set).
6. **Verification (read-only):**
   - `$FUGARO ls` prints `project: <slug> (GCP <id>)` first, and lists both repositories' runs;
   - `gcloud run jobs describe <one job per repository> --region <region> --project <id> --format json` shows both variables;
   - `gcloud storage buckets describe gs://<runs bucket> --project <id> --format 'value(labels)'` shows `fugaro_project=<slug>`;
   - `$FUGARO validate` passes in both checkouts;
   - outside a checkout, with a second, dummy project config present in a temporary `XDG_CONFIG_HOME`, a command without `--project` refuses and lists both; with exactly one, it works.
7. ⚠ CONFIRM **A sandbox run** (live check 13, `TestLiveSandboxRun`): it ends with a ready PR; its record has `model_source: claude-code` and no `halt`. This checks the project check and the pins path (no pins set) on a real job.
8. ⚠ CONFIRM **The live gateway check** (check 20, `TestLiveGateway`), only if the user answers Open question 3 with a key: about $0.10 on the user's own API key, run locally in Docker. Record the `FACT`s for A6, A9, A10, A11, A-N1, A-N2, A-N6. A false A6 means the settings refusal (R10) is the only guard for committed settings: say so in the PR and in §6.1.
9. **The web repository** gets a real run only with the user's explicit go-ahead and task text (⚠ CONFIRM, its own question).
10. Fill in "Results of the fifth live run (M9a)" in `gcp-live-checklist.md` with the `FACT`s, in the M9a PR.

**Rollback:** re-run the old binary with `config.yaml.bak` restored (the old CLI reads it), point `base_image` back at the M6 tag (⚠ CONFIRM `init --base-image <M6 tag> --yes` with the old binary), rebuild both repositories' images (⚠ CONFIRM each), and ⚠ CONFIRM `init --repo` with the old binary in each checkout, which writes the old `FUGARO_PROJECT` (the GCP ID). The bucket label and marker object are harmless to the old code. The `project:` line in each `fugaro.yaml` must be reverted too (the old validator refuses the unknown key): the user's PR for the web repository, a controller commit for the sandbox with the user's OK.

---

## Decisions recorded (formerly open)

- **Project configs and selection:** R1, with `$FUGARO_CONFIG` kept as `--config`'s environment form and bound to the checkout's project.
- **Where the name lives and how launchers check it:** R3 (a Terraform-managed marker object beside the label and the output), because launchers can't read bucket labels or the state.
- **The runner's check:** R4 (the base, and the ref when it differs; before the lock; required on Cloud Run only).
- **The gateway's auth on Vertex:** loopback only, no token (R5).
- **Halts happen at call boundaries,** with a 60 s grace before a kill (R7).
- **The report keeps its first line** and adds the halted line below it (R8), so M6's marker and heading recognizers keep working.
- **Pins apply whatever the budget mode;** explicit priced IDs are required only with the budget on (R9).
- **Managed settings are written before every stage,** with the stage's pins (R10).
- **The budget env is plain job env from `infra/spec.go`,** not new Terraform variables (R11).

## Open questions for the user

Each has a recommended default, which the plan implements unless the user rules otherwise.

1. **`$FUGARO_CONFIG`.** The design's selection list names only `--config`. *Default:* keep `FUGARO_CONFIG` as its environment form (tests and scripts rely on it), and refuse it inside a checkout of another project, like `--config`.
2. **The canonical name of the existing installation.** *Default:* none; the controller asks for `<slug>` before Task 12 starts. The name is immutable in M9 (design §2.5), so it is worth choosing with care.
3. **How the gateway is verified live.** Both existing repositories use `oauth`, which never goes through the gateway, so no sandbox run exercises it. *Default:* check 20, a local Docker run on the user's own Anthropic API key, cheapest model, about $0.10, gated by `FUGARO_LIVE_SPEND_OK=1`. Alternative: a Vertex workflow in the sandbox, which needs Claude enabled in the GCP project's Model Garden and changes the sandbox's `fugaro.yaml`.
4. **Refusing repositories whose `.claude` settings reroute Claude Code** (R10) in addition to managed settings. *Default: yes,* with the gateway on only. It costs nothing when A6 holds and is the only guard for committed settings when A6 fails.
5. **`agent.max_run_tokens` lives in `fugaro.yaml`** (design §2.1, §5.8), which lets a repository relax its own token cap, against §2.1's "owners choose money". *Default:* as designed, repository-side, since `oauth` has no owner-side cap in M9a; an owner-side `budget.max_run_tokens` can come with M9b.
6. **`--json` outputs that are arrays** (`secrets ls --json`) can't gain a `project` field without changing their shape. *Default:* leave them as they are; the stderr header names the project.
7. **Setting the budget in M9a.** *Default:* the owner edits `budget:` in the project config and re-runs `fugaro init --repo` in each checkout; M9b's `init --budget-mode` replaces this.

## Unverified assumptions to check live

The design's A-list items that bear on M9a, and the new ones this plan relies on (A-N*). **Blocks M9a** means a false answer changes code before M9a can ship.

| # | Assumption | Blocks M9a? | Where it's checked | If false |
|---|---|---|---|---|
| A3 | A request that fails before `message_start` isn't billed; an interrupted stream bills the tokens generated | No (settlement errs high after `message_start`) | Documentation; check 20 cannot provoke it cheaply | Charge input tokens on pre-start failures (one line in `ledger.go`) |
| A6 | Managed settings' `env` overrides the repository's `.claude/settings.json` `env` | **Yes** (the bypass guard) | Check 20, step 1 | R10's refusal is the only guard for committed settings; §6.1 says so; consider refusing any `.claude/settings*.json` `env` |
| A9 | `max_tokens` never exceeds `CLAUDE_CODE_MAX_OUTPUT_TOKENS`; usage fields are as documented through Vertex `streamRawPredict` | **Yes** for Vertex runs | Check 20, step 4 (Anthropic side); Vertex only when a Vertex run exists | Reserve the model's maximum output; hold Vertex runs back until checked |
| A10 | Every billed call Claude Code makes goes through the base URL | **Yes** (the cap's meaning) | Check 20, step 3 | Budget the difference as per-stage overhead, documented |
| A11 | The embedded prices are current, including per-model cache-read multipliers and any long-context tiers, and Vertex bills the same | No (overrides cover it) | T4 re-reads the pricing page; check 20, step 3 | Update the table; owners set `model_prices` for Vertex |
| A-N1 | In `-p` mode Claude Code ends the run, without retrying, on a 403 with `x-should-retry: false` | No (the grace then kills it) | Check 20, step 5 | Halts always take `haltGrace`; lower it |
| A-N2 | The result event carries `usage` with the four token fields | **Yes** for the token cap | Check 20, step 5; also the sandbox run's transcript (step 7) | Read usage from the per-message `assistant` events instead |
| A-N3 | With `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, Claude Code sends Vertex-shaped paths to `ANTHROPIC_VERTEX_BASE_URL` with the pinned model in the path | **Yes** for Vertex runs | Only a live Vertex run (Open question 3's alternative) | Vertex stays behind `budget.mode: off` until checked |
| A-N4 | `roles/storage.objectAdmin` lacks `storage.buckets.get`, so launchers can't read bucket labels | No (R3 works either way) | Documentation | None needed |
| A-N5 | The pinned Claude Code version reads `/etc/claude-code/managed-settings.json` on Linux | **Yes** (with A6) | Check 20, step 1 | Find the version's path; the file's location is one constant |
| A-N6 | Claude Code's background requests honour `ANTHROPIC_DEFAULT_HAIKU_MODEL`, and subagents `CLAUDE_CODE_SUBAGENT_MODEL` | **Yes** (D8 would fail every stage) | Check 20, step 2 | Pin the missing variable, once found in the violations log |
| A-N7 | Claude Code accepts a plain `http://127.0.0.1:<port>` base URL | **Yes** | Check 20, step 1 | Serve TLS on loopback with a per-run CA in `NODE_EXTRA_CA_CERTS` |

A-list items that don't bear on M9a (A2, A4, A5, A7, A8, A12–A15) belong to M9b and later.

## Conflicts between the design and the code (resolved by this plan)

- **§2.4 has the CLI check the name against the bucket label or the outputs,** but launchers can read neither (R3, A-N4). Resolved by the marker object `fugaro/project.json`.
- **§2.5 puts the runner's check before the lock,** but a first run fetches its base after the lock today. The fetch moves before the lock (R4); it changes nothing remote.
- **§2.6 mentions the skills `fugaro:launch` and `fugaro:status`,** which don't exist yet (M7). Only `fugaro:onboard` (T3) and `fugaro:followup` (T11) change.
- **§5.1 gives the agent a per-run gateway token on Vertex too,** but `CLAUDE_CODE_SKIP_VERTEX_AUTH=1` sends none. Resolved by R5 (loopback only on Vertex).
- **§5.1 says the runner writes `/etc/claude-code/managed-settings.json`,** but the runner runs as the non-root `fugaro` user. Resolved by the base image's directory (T9) and a selftest check.
- **§5.9's report "starts `## Fugaro — Halted`",** but every report starts `### Fugaro run \`<id>\``, which M6's `FugaroRun` recognizes as a legacy heading. Resolved by R8: the heading stays and the halted line follows it.
- **§5.9's "a halt before the branch exists: exit 0"** needs `Run` to return a nil error from a bootstrap failure; every bootstrap error exits 2 today. Resolved in T7.
- **§6.6 and §15 task 7 route the budget env "through the tfvars",** but job env is built in Go (`infra.platformEnv`, `workflow`) and passed as a map, so no Terraform variable is needed (R11). The repository root's new `fugaro_project` variable is used only for its consistency check.
- **The word "budget" is already taken twice:** `runner.Budget` (the run's time budget) and `fugaro init --budget` / `infra.Budget` (a GCP billing budget). Both stay; the new code uses `Spend`, `gateway` and `pricing`, and only the project config's `budget:` block (the design's name) uses the word.
- **`--json` outputs gain `project` (§2.4),** but `secrets ls --json` is an array (Open question 6).
- **`gcp.Options.Project` and `localcfg.Config.Project`** meant the GCP ID; both become `GCPProject` in T1, so no identifier keeps two meanings (D17).
- **`checkRegistryHostProject` and the `log_view` note in `openCloud`** exist for `--project` pointing at another GCP project, which R2 forbids; they are simplified and removed in T1.
- **§2.1 says `validate` refuses aliases "when the budget is on",** but `validate` is a local command and today knows nothing of the budget. Resolved in T6: it reads the selected project config, with no cloud call.

## Review disposition (round 1)

*To be filled in after the plan's review: each finding, fixed or rejected with the reason.*

## After M9a

- **M9b:** Firebase counters, daily caps, kill switches, `fugaro budget`, `init --firebase`, `--budget-mode`, fail-closed (D14), the halt reasons the schema already lists.
- **M9c–M9e:** `fugaro watch`, history and `fugaro report`, the verify gate and structured findings.
- **Later:** an owner-side token cap for `oauth` (Open question 5), `ls --all-projects`, renaming a project, the external gateway (D2).

## Execution

Subagent-driven is recommended, with a fresh reviewer per task. Lane A (T1–T3) and lane B's first two tasks (T4, T5) are independent and may run in two worktrees; from T6 on the tasks build on each other's interfaces. T5 (the ledger and settlement), T9 (the agent's environment), T10 (the halt path and the secret scan) and T1 (selection) are where a shipped mistake spends past a cap, hands the agent the real key, lets a repository route around the gateway, or launches in the wrong project; they deserve the closest review. Task 12 is controller-only: it changes real repositories and cloud resources and spends money, the user confirms every ⚠ step, the user alone merges the web repository's change, and the web repository gets a run only on the user's explicit go-ahead.
