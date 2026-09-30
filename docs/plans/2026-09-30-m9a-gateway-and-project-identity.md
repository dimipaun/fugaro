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
   bootstrap: claim · budget env error? infra_error · clone · checkout · fetch base
     · project: on origin/<base> (and <ref>) == FUGARO_PROJECT, else infra_error
     · vertex + enforce → infra_error · api-key, enforce, no cap → halted/none, exit 0
     · pins valid for the budget · no settings file reroutes Claude Code   (all before the lock)
     · lock · … · start gateway 127.0.0.1:<port> (api-key/vertex, budget on) · agent env: gateway URL + per-run token, no real key
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

**Tech stack:** Go 1.27. No new Go module: the gateway is plain `net/http` with its own forwarding (no `httputil.ReverseProxy`, whose buffering and header rewriting we'd have to undo); Vertex tokens use `golang.org/x/oauth2/google` (an indirect dependency today; T5's `go mod tidy` makes it direct), and compressed replies are decoded with the standard library and `github.com/klauspost/compress`, already a direct dependency. Terraform: one new variable (`fugaro_project`) in the installation and repository roots and modules, one bucket label, one bucket object and one output. One base-image change: `/etc/claude-code` owned by the `fugaro` user.

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
- CI runs `gofmt -l`, `go vet` (plain, `-tags docker`, `-tags live`, `-tags terraform`, and, from T11, `-tags 'live docker'` for the live gateway test, which neither tag alone compiles), `go test -race ./...`, and, for the Terraform, `terraform fmt -check`, each root's `validate` and `test`, `tflint`, `deploy/terraform/scan.sh` (Trivy) and `go test -tags terraform ./internal/infra/...`. All of them pass after every task.

## Rulings on the questions

Each ruling says what it costs if it's wrong.

**R1. How a command picks its project config** (design §2.4), in `localcfg.Select`. **Explicit selectors never lose to ambient environment.** Precedence, first match wins:
1. `--config <file>` (explicit).
2. `--project <name>` (explicit).
3. The checkout's `project:` (the checkout the command acts on, below).
4. `$FUGARO_PROJECT` (ambient).
5. `$FUGARO_CONFIG` (ambient; the environment form of `--config`, kept because tests and scripts use it).
6. Exactly one `projects/*.yaml`: that one.
7. None: "no project config; see `fugaro init --config-only`" (and, when `config.yaml` exists, "the old config.yaml isn't read any more: see §13.1"). Several and nothing above selecting: exit 1, listing them.

**Conflicts are refused, never resolved silently** (exit 1, naming both sources):
- `--config` together with `--project`, when the file's `name:` differs from `--project` ("--config names project aurora, but --project says borealis");
- `--project` disagreeing with the checkout's `project:` ("this checkout belongs to project aurora; --project says borealis");
- `--config` disagreeing with the checkout's `project:`;
- `$FUGARO_PROJECT` disagreeing with the checkout's `project:` (the design's rule, kept).
- `$FUGARO_CONFIG` loses to everything above it: when something higher selected a different project, it is ignored with one stderr note ("ignoring FUGARO_CONFIG (project aurora): --project selects borealis"), never used.

**The checkout and its rules:**
- The checkout is the one the command acts on: the working directory's `git rev-parse --show-toplevel` for every command, except `init --repo [PATH]`, where it is PATH's toplevel (`checkoutProject(ctx, dir)` takes the directory; `loadRepoConfig` passes PATH). It counts only when that toplevel holds a `fugaro.yaml`.
- Its `fugaro.yaml` without `project:` → refuse ("this checkout's fugaro.yaml has no `project:`; add `project: <name>` (fugaro config example shows it)"). A named project without `projects/<name>.yaml` → refuse with the design's message ("this checkout belongs to project aurora; there is no project config for aurora (run `fugaro init --config-only --gcp-project <id>`)").
- **`fugaro init` is the exception to "no config":** `init` and `init --config-only`, which create that config, skip the "no project config" refusal, but still require the project they create or name to equal the checkout's `project:` (`fugaro init --name borealis` in a checkout of project aurora is refused).
- A `fugaro.yaml` that fails to parse still yields its `project:` (read leniently by `config.ProjectOf`), so a broken config never hides which project a checkout belongs to.
- `projects/<name>.yaml` must hold `name: <name>`; a file whose `name:` differs from its basename is refused.

**Which commands select a project:** every cloud command (the ones with `addCloudFlags`). `fugaro validate`, `fugaro config example` and `fugaro image build --local` never refuse for want of a project config: they use one when it is selectable, and otherwise go on without it (so onboarding a new repository with no project config keeps working; the onboard skill then asks the user for the name).

*Cost if wrong:* one pure function with a table test.

**R2. `--gcp-project` and `--region`.** `--gcp-project` is accepted only when it equals the selected config's `gcp_project` (a no-op), or when `fugaro init` creates a project config that doesn't exist yet. There is no force flag. `--region` keeps its meaning (a read-only override). The `log_view` note in `openCloud`, which existed only for `--project` pointing elsewhere, is removed as dead code. `checkRegistryHostProject` **stays**: it checks the file's own consistency (`registry_host` against `gcp_project`); only its comment about `--project` goes.

**R3. Where the cloud keeps the name, and how the CLI checks it.** The design says the bucket label and the outputs. Launchers hold `roles/storage.objectAdmin` on the runs bucket, which lacks `storage.buckets.get`, so they can't read a label, and they can't read the Terraform state either. So:
- The installation's Terraform writes **`gs://<runs bucket>/fugaro/project.json`** (`google_storage_bucket_object`), `{"version": 1, "name": "<name>", "gcp_project": "<id>"}`, beside the label `fugaro_project=<name>` and the output `project_name`. No job account can write it (the jobs' bucket condition covers `runs/`, `cache/` and `locks/` only; builds cover `builds/`).
- `openCloud`, for every cloud command but `init`, reads that object and refuses a project config whose `name:` or `gcp_project:` differs ("project config aurora points at a GCP project whose Fugaro project is borealis"). A missing object: exit 1, "project aurora's installation has no project name yet; an operator runs `fugaro init --name aurora`". A read error: exit 2.
- The check is cached for a day in `$XDG_CACHE_HOME/fugaro/project-check/<name>.json` (`{gcp_project, runs_bucket, checked_at}`); any field differing re-checks.
- `fugaro init` compares the label, the output and the object with `--name` and refuses any disagreement (immutability).
- *Cost if wrong:* a launcher who overwrites the object can make their own commands refuse. It is a safety label, not a boundary (design §2.5).

**R4. The runner's project check** (design §2.5).
- **One Cloud Run signal:** `backend.OnCloudRun(getenv)` is true when `CLOUD_RUN_EXECUTION` is set (the variable `ExecutionFromEnv` already keys off). `fugaro exec` passes `FUGARO_PROJECT` as `Deps.Project`, and `Deps.RequireProject = backend.OnCloudRun(os.Getenv)`. A local `fugaro exec` without `FUGARO_PROJECT` skips the check and logs one line saying so.
- **An old job environment fails cleanly, before anything can say "mismatch"** (no compatibility, D18). On Cloud Run, `exec` checks the job's environment **first, before `ExecutionFromEnv`**: `FUGARO_GCP_PROJECT` missing (the pre-M9a shape, whose `FUGARO_PROJECT` holds the GCP ID) or `FUGARO_PROJECT` missing → `exec` writes a minimal `result.json` for the run (create-if-absent, as the runner's `createRecord` does: `status: infra_error`, `outcome: none`, `stage: bootstrap`, `reason: "job environment lacks FUGARO_GCP_PROJECT (it was set up before M9a): run fugaro init --repo from the repository's checkout; see docs/design/m9-budget-and-dashboard.md §13.1"`), and exits 2. The runner and its project check are never reached, so the old `FUGARO_PROJECT` (a GCP ID) is never compared with a name. The record carries **no `execution`**: without the GCP project the name wouldn't pass `backend.ParseExecution`, and a malformed one would confuse `ls`; the reason names the job and execution as plain text. The launch freeze in Task 12 still applies: this path exists so a slip is visible in `ls`, not so it is safe.
- **Where:** in bootstrap, after the checkout and the base fetch, **before the lock**. For a first run the base fetch moves from after the lock to here (it is a read, so the lock still comes before anything that changes remote state). It reads `project:` with `git show origin/<base>:fugaro.yaml` (`gitops.ShowFile`, from M6) through `config.ProjectOf`, and, when `spec.Ref` isn't the base, the ref's own `fugaro.yaml` too. A follow-up reads only the base, which it reads anyway.
- Missing or different: `infra_error`, outcome `none`, exit 2, reason "project mismatch: fugaro.yaml on main names project borealis; this job belongs to project aurora" (or "… names no project").
- *Cost if wrong:* a run refused before it did anything: one container start.

**R5. The gateway's shape.**
- **Per run, in-process, loopback.** `gateway.Start` listens on `127.0.0.1:0`. It starts in bootstrap after the config, pins and settings checks, and closes after the agent loop, before finalize (finalize makes no model calls).
- **Who can call it.** Upstream `anthropic`: every request must carry the per-run token (32 random bytes, hex) as `x-api-key`, else 401. Upstream `vertex`: Claude Code sends no credential when `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, so the gateway accepts loopback requests without a token. Either way anything in the container can reach it; D2 already accepts that the container is not a boundary against its agent.
- **Routing matches `URL.Path` only;** the raw query string (Claude Code's SDK posts `/v1/messages?beta=true`) is forwarded unchanged.
- **Endpoints** (design §5.1): `POST /v1/messages`, `POST /v1/messages/count_tokens` (free, no reservation), `HEAD /api/hello` (a local 200); on Vertex `POST /v1/projects/<p>/locations/<l>/publishers/anthropic/models/<m>:rawPredict`, `:streamRawPredict` and `…/models/count-tokens:rawPredict`, where `<p>` must be the job's `ANTHROPIC_VERTEX_PROJECT_ID` (the gateway's token must not work on another project) and `<l>` one of the **allowed locations**: `CLOUD_ML_REGION` plus the value of every `VERTEX_REGION_*` the runner passes to the agent (Claude Code names a per-model region in the path, commonly for the background model). The upstream host is derived from the path's `<l>`, `global` included. Anything else: 404. (Vertex gateway runs are refused in M9a anyway, R11; this keeps the code right for when they are allowed.)
- **Forwarding.** The incoming `x-api-key` and `authorization` are dropped, the real credential is set, **`Accept-Encoding` is replaced by `identity`** (so the usage tee reads plain SSE or JSON), and every other request header and the body pass unchanged. Response status, headers (`retry-after`, `x-should-retry`, `anthropic-ratelimit-unified-*`, `request-id`) and body pass unchanged, flushed after every write. **If a reply arrives compressed anyway,** the gateway decodes `gzip` and `deflate` (standard library) and `zstd` (`github.com/klauspost/compress`, already a direct dependency: no new module), drops `Content-Encoding` and `Content-Length`, and forwards and tees the plain bytes. Any other encoding (`br` included) is forwarded as is and settles as unparsed usage (R6).
- **Request parsing.** The body is read whole (at most 32 MiB, the API's own limit; more is a 413 from the gateway) to find `model` (on Vertex from the path), `max_tokens`, `stream`, any `cache_control` and its `ttl`, every `tools[].type`, `speed`, `inference_geo`, `service_tier`, `mcp_servers`, `container`, and every content block's `type` and `source.type` (images counted). A body that isn't JSON or lacks `max_tokens` on `/v1/messages`: 400 from the gateway, never forwarded.
- **Request shapes the gateway refuses** (400 `invalid_request_error`, a stage violation: D8, the stage fails naming the parameter), because the table can't price them or bytes don't bound them (I9, C2):
  - `speed` present and not `"standard"` (fast mode is billed at a multiple of the table's rates) — "fugaro: speed \"fast\" is not allowed (it isn't priced by the budget)";
  - `inference_geo` present and not the default (`""` or absent), and `service_tier` other than absent, `"auto"` or `"standard_only"`;
  - **tools by allow-list:** a `tools[]` entry is allowed only when its `type` is absent or `"custom"` (plain client tools, which is how Claude Code declares its own); **any other type is refused, naming it**: every server tool (`web_search_*`, `web_fetch_*`, `code_execution_*`, tool search, anything added later) and every Anthropic-defined typed tool alike. A server tool's results are billed as input the body doesn't bound, so in M9a none is allowed;
  - `mcp_servers` or `container` present;
  - a `document` or `image` block whose `source.type` is `file` or `url` (their size isn't in the body);
  - `max_tokens` above the model's `pricing.Model.MaxOutputTokens` (1,000,000 when the table has none), so the worst case can't overflow (R6).
  - Base64 **PDF** `document` blocks are allowed but reserved as the model's whole context window of input (`pricing.Model.ContextTokens`), since a few KB of PDF can be thousands of tokens.
  - Base64 **images** are allowed only when the model has a per-image ceiling in the table (`pricing.Model.ImageTokens`, a conservative constant, A-N9) and are reserved at that ceiling each, whatever their byte size; for a model without one, image blocks are refused ("fugaro: images aren't priced for model X").
- **Claude Code's own web tools are denied too:** with the gateway on, the managed settings carry `"permissions": {"deny": ["WebSearch", "WebFetch"]}`, so the agent isn't steered into tools that would only be refused (WebSearch is a server tool; WebFetch is client-side but denied with it in M9a).
- **The upstream base** is `https://api.anthropic.com`, or `https://<location>-aiplatform.googleapis.com` (`https://aiplatform.googleapis.com` for `global`). Tests set it only through `exec`'s hidden `--gateway-upstream`, which accepts only `http://127.0.0.1:<port>`, so no environment variable can send the real key elsewhere.
- **No proxy for the gateway.** With the gateway on, the agent's `NO_PROXY` and `no_proxy` gain `127.0.0.1,localhost` (appended to the inherited value), so the gateway token never goes through an outbound proxy.
- *Cost if wrong:* the handler is one package with its own fake upstream.

**R6. Worst case, reservation and settlement.**
- `worstCase = ceil(inputBound × inputRate × cacheMult + maxTokens × outputRate)` in µ$ (design §5.4, without web search, which M9a refuses). `inputBound` is the body's bytes, plus `imageCount × ImageTokens`, or the model's context window when the body holds a base64 PDF block (R5). `cacheMult` uses the **effective** rates, owner overrides included: `max(1, CacheRead, CacheWrite5m, CacheWrite1h)` with any `"ttl": "1h"`, `max(1, CacheRead, CacheWrite5m)` with any other `cache_control`, else `max(1, CacheRead)` (an override may price cache reads above fresh input). The long-context tier's rates apply when `inputBound` is past its threshold.
- **Overflow:** every product and sum uses saturating `int64` arithmetic (`pricing.satMul`, `satAdd`), and R5 already refuses `max_tokens` above the model's maximum, so no reservation can wrap negative; a property test feeds values near `MaxInt64`.
- **This is an upper bound only for the shapes R5 lets through** (text, plain client tools, base64 images at their per-image ceiling, base64 PDFs at the context window). Every other shape is refused before it can cost anything. The property test's premise is exactly that: usage whose `Input + cache tokens ≤ BodyBytes + imageCount × ImageTokens` (or `≤ ContextTokens` with a PDF) and `Output ≤ MaxTokens` costs at most the worst case; refused shapes have their own table test.
- **Output limits are per pinned model** (I8): `agent.max_output_tokens.<role>` bounds `max_tokens` only on requests for **that role's model**; background-model requests (Claude Code's compaction, titles, summaries, which choose their own `max_tokens`) are not bounded by the role's limit and are reserved from their own `max_tokens`. When the role model and the background model are the same ID, the role's limit applies to both. Above the limit: 400 `invalid_request_error` "fugaro: max_tokens N for model X is above the stage's limit M", a stage violation (D8).
- **The effective cap is lower than the nominal one** by up to one worst-case call: reservation is pessimistic (bytes over-count tokens roughly 3–4×, the full `max_tokens` is reserved, cache reads are reserved as fresh input), so a run can halt with, say, $19 of a $20 cap spent. A dropped stream is charged its full reserved output (for example 64 k output tokens on a $20/M model is $1.28). The report line, `gcp-setup.md` and the `budget:` example say so and recommend a week of `observe` to calibrate the cap.
- **One mutex, one ledger:** `{granted, used, reserved}`. `enforce`: `granted` is the per-run cap. A call reserves `w` if `used + reserved + w ≤ granted`, else it is refused: 403 `permission_error` "fugaro: budget halted: run cap $X reached ($Y spent)", `x-should-retry: false`, and the gateway **halts**: this and every later request is refused the same way, and `Halted()` fires once. `observe`: `granted` is unbounded; a call that `enforce` would refuse is logged ("budget: would halt (observe)") and counted, never refused.
- **Settlement** (design §5.5), by upstream status first:
  - **Non-2xx upstream status: 0** (A3).
  - **No upstream status at all** (a transport error, a connection reset, a client cancel before the response headers, `Close` cancelling a call that hasn't answered): **0 only when the connection failed before any byte of the request was written upstream**; once any byte was written, the **full reservation**, `settled: reserved`, counted as unreconciled. The gateway knows which from a counting wrapper around the request body. These two are the only cases charged nothing.
  - **2xx with usage parsed** from a completed stream or JSON body: that usage at the **serving** model's rates (the `message_start.message.model`, else the body's `model`); `settled: usage`. `message_delta.usage` is cumulative: the last value of each field wins. `input_tokens` excludes cache reads and writes; the tier's threshold compares their sum. A `cache_creation_input_tokens` the response doesn't split by TTL is priced at the highest write multiplier the request allowed (`CacheWrite1h` when it asked for a 1-hour TTL, else `CacheWrite5m`).
  - **2xx, stream cut, errored or cancelled by the client after `message_start` was parsed:** `message_start`'s input **and cache** tokens (reads and writes) plus the **reserved** output part; the difference from reported usage is `unreconciled`; `settled: partial`.
  - **2xx whose usage the gateway could not parse** (malformed or truncated before `message_start`, an unreadable encoding, a non-JSON body, a format change): the **full reservation**, `settled: reserved`, counted as `unreconciled`, and the stage report's `UsageUnparsed` count goes up; the runner notes `usage_unparsed: N` in the stage's log line and in the record's cost (`cost.usage_unparsed`). Failing toward the cap is the rule whenever the gateway can't tell.
  - **An unknown serving model:** the table's maximum rates, `priced_as: max`.
  - **Surprising pricing dimensions in the response** (C2): `usage.speed` present and not `"standard"`; `usage.service_tier` present and not `"standard"` (so `"priority"`, which a Priority-Tier organization's `auto` request can get on older models, is a surprise: such organizations pin models Priority Tier excludes, or keep the budget off; the docs say so); `usage.inference_geo` present and not in `gateway.DefaultGeos` (the values a default request returns, pinned from recorded fixtures: absent, `""`, `"global"`; check 20 records the live value, A-N8): the call is charged at the table's **maximum rates × `pricing.SurpriseMultiplier` (2, fast mode's premium on the current models)**, flagged `priced_as: surprise`, logged, and counted as a stage violation (the stage fails, D8), since the request should never have produced it.
- **Overrun.** If `actual > w` (an unknown serving model, a server-side fallback, or a surprise), the whole `actual` is charged, the excess is logged and counted as `overrun`, and `used` may pass `granted` by that call's excess; the next reservation then halts. The property test allows exactly this case.
- **Retries are new requests** (Claude Code retries 429, 529 and dropped streams): each reserves and settles on its own; the gateway never retries.
- *Cost if wrong:* golden and property tests pin the arithmetic.

**R7. Halting at a call boundary.** A run-cap refusal happens **before** a call is forwarded, so Claude Code gets the 403 while it is waiting on the model, not while a tool is writing a file. On `Halted()` the runner **records the halt at once** (R8) and then gives `claude` up to `haltGrace` (60 s) to exit on its own (A-N1); if it hasn't, the runner cancels the stage context with the halt as its cause, which SIGTERMs the process group (10 s grace, as today). Whether `claude` exits by itself (with an error, or even with `subtype: success`), or is killed, the stage ends `halted` and no later stage starts. Calls already in flight when the cap is hit were reserved and finish normally. Finalize commits any leftovers as `fugaro: uncommitted work at halt (the stage was stopped; files may be incomplete)`, and the report says so. *Cost if wrong:* a parallel subagent's tool can still be mid-write when the grace ends; the draft PR says the files may be incomplete, and a halted run is never ready.

**R8. `halted` in the runner** (design §5.9).
- `runner.HaltError{Halt runstore.Halt}`, `errors.Is(err, runner.ErrHalted)`.
- **One precedence rule: whichever of halt and cancel is recorded first in time wins,** decided under `r.mu` (a `sync.Mutex` guarding `r.halt`, `r.cancelled`, `r.haltStage` and `r.failReason`). Both sides record through the lock:
  - `haltNow(h)`: if no cancel was recorded, sets `r.halt` and the reason (via `fail`, so it is the first failure reason); the running stage is cancelled later, after R7's grace, from the watcher.
  - `markCancelled()`: if no halt was recorded, sets `r.cancelled`. **`WatchCancel` gains an `onCancel func() bool` hook** (`r.markCancelled`), called before it cancels with `ErrCancelled`, so a cancel is recorded the moment the watcher sees the marker, not when a stage returns. Bootstrap's cancel branch and `Run`'s post-loop `context.Cause(runCtx)` check go through `markCancelled` too; neither sets `r.cancelled` directly any more.
  - `context.Cause` no longer decides anything. `finalize` asserts that at most one of `r.halt` and `r.cancelled` is set (a panic in tests, a logged error in production that keeps the halt).
- **After every agent return, before the existing error switch,** `stage` checks, under the lock: (1) `r.halt` set → the stage ends halted: `(res, false, &HaltError{…})`, no log tail, whatever the agent's `is_error`, `subtype` or exit code; (2) the gateway's `EndStage().Violations` non-empty → `r.fail("stage review: model claude-x is not pinned for stage review")` (the first violation, naming the model or parameter) and `(res, false, …)`; (3) then today's switch. `agentLoop` checks `r.halt` again after every stage, so a review that "succeeded" never leads to a fix.
- **Mid-run:** the loop stops, finalize runs normally: status `halted`, outcome `draft` once the branch is pushed; a follow-up that stops before its push (M6's no-push paths) ends `halted`/`none`. `ready` is forced false. `reason` is "halted: <reason>: <detail>".
- **At bootstrap** (D9; in M9a only `no_cap`: an `api-key` run with `FUGARO_BUDGET_MODE=enforce` and no positive `FUGARO_MAX_RUN_USD`): the auth mode is known only once `fugaro.yaml` is read, so the check runs right after the project check and **before the lock**. The order is deliberate: a malformed budget env (R11) and Vertex with `enforce` (an `infra_error`, R11) are checked **before** `no_cap`, so a misconfiguration is never reported as a policy halt with exit 0. The run branch exists only in the local checkout then: nothing is locked, pushed or opened. Status `halted`, outcome `none`, no PR, **`Run` returns a nil error**, so `exec` exits 0.
- **The token cap** (§5.8): after every stage the runner adds the stage's tokens (input, cache creation, cache read, output) to `r.tokens`; when `agent.max_run_tokens > 0` and the sum reaches it, the run halts with `token_cap`, scope `run`. It applies to every auth mode and is checked only at stage boundaries. **A stage's tokens are `max(gateway count, result-event count)` where both exist.** The result-event count is the larger of Σ`usage` and Σ`modelUsage[*]` (the latter covers subagent and background models); the gateway count is its own per-stage sum. **`oauth` has no gateway,** so its cap rests on the result event alone: if that under-reports (A-N2), the cap is soft by the difference. `gcp-setup.md` and §11's table say so, together with the fact that `max_run_tokens` lives in `fugaro.yaml`, so a repository writer can lift the only `oauth` guard (decided: as designed).
- **The report** keeps its first line, `### Fugaro run \`<id>\`` (M6's `FugaroRun` recognizes it), and adds, right below, `**Halted:** <reason text> at <time> — this run spent $X (cap $Y).` and how to continue: raise `budget.per_run_usd` in the project config and run `fugaro init --repo`, then `fugaro run --pr N`.
- `result.json` (version 1, additive): `status` gains `halted`; `halt: {reason, scope, at, detail}` with the eight reasons of §5.9; `cost` gains `model_source` (`gateway` | `claude-code`), `model_by` (model → USD) and `unreconciled` (USD).
- *Cost if wrong:* the status is additive; old records parse.

**R9. Pins** (design §2.1).
- The role of a stage: `implement` and `fix` → `coder`; `review` → `reviewer`. Its model: for `coder`, the task's `overrides.model`, else `agent.models.coder`, else `agent.model`; for `reviewer`, `agent.models.reviewer`, else `agent.model`. (`task.Apply` writes the override into `agent.models.coder`, so `Agent.ModelFor` needs no task.) The background model: `agent.models.background`.
- **Every stage, whatever the budget mode,** when a role model is set: `--model <model>`, and `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` = the model, `ANTHROPIC_DEFAULT_HAIKU_MODEL` = the background model when set, `CLAUDE_CODE_MAX_OUTPUT_TOKENS` = the role's `max_output_tokens` when set. The gateway enforces that limit per model (R6): on the role model's requests (main agent and subagents, which are pinned to the same model), never on the background model's.
- **With the budget on** (`observe` or `enforce`), bootstrap refuses (`infra_error`) a role without an explicit model ID, an alias (`sonnet`, `opus`, `haiku`, `opusplan`, `default`, any `[1m]` suffix, any ID not in the effective table), or a missing background model; `fugaro validate` applies the same rules when the selected project config's budget is on.
- **The gateway's allow-list per stage:** `{role model, background model}` and their table aliases. Anything else: 400 `invalid_request_error` "fugaro: model X is not pinned for stage review", a stage violation, and the stage fails (`failed`, D8) once it returns, even if Claude Code carried on; the stage's failure reason names the model (R8's ordering).

**R10. Managed settings and the repository's own settings** (design §5.1, A6).
- When the gateway is on **or** any pin applies, before every stage the runner writes the managed settings file: `{"env": {…}}` holding the gateway variables of §5.1 and the stage's pins. It replaces the file atomically (write, fsync, rename in the same directory) and refuses a symlink in its place. The same variables go into the stage's process environment.
- **Where the file is:** `Deps.ManagedSettingsPath`, set by `exec` from its hidden `--managed-settings <path>` flag, else `$FUGARO_MANAGED_SETTINGS`, else `/etc/claude-code/managed-settings.json` (the real image's path). The flag and the variable are test hooks: they are honoured only together with a loopback `--gateway-upstream`, or when the budget is off, so a job with a real upstream always writes the real path. The hermetic cloud rig (`internal/e2e/cloud_test.go`) passes both flags and never depends on `CLOUD_RUN_JOB` being unset.
- The base image creates `/etc/claude-code` owned by `fugaro` (uid 1000), mode 0755 (the runner is not root). `fugaro image selftest` checks, **as uid 1000** (part of selftest runs as root, so it tests with `access(2)`-equivalent checks on owner and mode for uid 1000, not the current uid), that the directory is writable, and that it is **empty** at build time (the runner writes `managed-settings.json` at run time, so a baked one is refused too): any entry (`managed-mcp.json`, a `managed-settings.d/` drop-in, anything an `image.setup` step wrote with its build-time sudo) fails the selftest. With the gateway on and the directory not writable, bootstrap fails (`infra_error`, "the image can't hold Claude Code's managed settings; rebuild it on an M9a base image"); with only pins, a warning.
- **What the managed file guards against:** committed configuration only (the repository's `.claude` settings and whatever the image baked in). It is written by, and writable by, the agent's own user, so a compromised agent can rewrite it mid-stage; that is D2's residual, the runner rewrites it before each stage, and §6.1 and `gcp-setup.md` say exactly this.
- **Defense in depth (decided):** with the gateway on, **before every stage** (not only at bootstrap: implement can write `.claude/settings.local.json`, and a follow-up checks out an agent-written branch), the runner refuses a stage when any of these sets `apiKeyHelper` or an `env` key among `ANTHROPIC_BASE_URL`, `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_FOUNDRY`, `CLAUDE_CODE_SKIP_VERTEX_AUTH`, `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` (any case): the checkout's `.claude/settings.json` and `.claude/settings.local.json`, the user's `~/.claude/settings.json` (and `$CLAUDE_CONFIG_DIR/settings.json` when that is set), and any file in the managed settings directory other than the runner's own. At bootstrap the refusal is an `infra_error`; before a later stage it fails that stage (`failed`, reason naming the file and key) and the run finalizes as usual.
- *Cost if wrong:* if A6 is false, the refusal is the only guard against the committed settings, and **the runner fails closed**: A6.1 below.

**R11. The budget in the project config and the job** (design §5.6, §5.7, §6.7).
- Project config: `budget: {mode: off|observe|enforce, per_run_usd: N}` and top-level `model_prices: {<model>: {input_per_m, output_per_m, cache_write_5m, cache_write_1h, cache_read, web_search_per_1k}}`. No `budget:` block means `off`. `enforce` needs `0 < per_run_usd ≤ 100000`; `observe` accepts 0 (account only). In M9a the owner edits the file and re-runs `fugaro init --repo`; `--budget-mode` is M9b's.
- `fugaro init --repo` puts, with the mode not `off`, `FUGARO_BUDGET_MODE`, `FUGARO_MAX_RUN_USD` (when > 0) and `FUGARO_MODEL_PRICES` (compact JSON, when set) into every **workflow** job's env (not the check job's: it calls no model). They are plain env map entries in the workflow spec, so no Terraform variable changes for them.
- `exec` parses them once (`runner.SpendFromEnv`) and hands the result **and its error** to the runner (`Deps.Spend`, `Deps.SpendErr`); the runner fails bootstrap right after claiming the record (`infra_error`, the parse error as the reason). This is the one place a malformed budget env is reported: `exec` writes no record of its own for it (only R4's old-job-env case does). With `auth: oauth` the mode doesn't start a gateway and `no_cap` doesn't apply (there is no dollar cap for `oauth`, A1); pins and the token cap still apply.
- **Vertex with the budget in `enforce` is refused in M9a** until A-N3 and A9 are verified live: `fugaro validate` (with a selectable project config), `fugaro init --repo` and the runner's bootstrap (`infra_error`) all say "Vertex budgets are not supported yet: use budget.mode observe or off for this repository (see docs/gcp-live-checklist.md, check 20)". `observe` and `off` are allowed. With the gateway on for Vertex (`observe`), `VERTEX_REGION_*` overrides are accepted and each named region is added to the gateway's allowed locations (R5).

**R12. Cost.** With the gateway: the record's `cost_usd` and `cost.model_usd` are the gateway's settled µ$ (`model_source: gateway`), taken from `Ledger().Used` after `Close` (so a call that settles after `EndStage`'s 30 s wait is not dropped; stage figures are `StageReport.Used`, and the run's total is never their sum), `model_by` its per-model split and `unreconciled` its estimated share; Claude Code's `total_cost_usd` per stage is kept only for a cross-check, and the runner logs a warning when the two differ by more than 5% in a stage. Without it: as today, `model_source: claude-code`.

## Review Focus

These are the failure modes that are easiest to miss, most likely first. Each is pinned by a named test in the task that owns the code.

1. **Leaking the real key to the agent, a log, a transcript or the managed settings file.** Pinned by T9 `TestBuildEnvGatewayDropsRealKey` (api-key and vertex), `TestManagedSettingsHoldNoRealKey`; T5 `TestGatewayLogsNoSecrets` (an upstream 401 whose body echoes the key's prefix; the log holds neither the key nor the token), `TestGatewayStripsClientCredentials`; T10 `TestGatewayRunSecretScan` (a full run: the planted key is in no bucket object, log line, transcript, `calls.jsonl` env or settings file) and the e2e `TestCloudGatewayRunSecretScan`.
2. **A 2xx charged $0, or a call priced below what it cost (fail open).** Pinned by T5 `TestCompressedResponseSettledFromUsage` (the fake answers gzip, deflate or zstd even though the gateway asked for `identity`: settled from the decoded usage, the client got plain bytes; `br` is `TestUnparsed2xxChargesReservation`'s), `TestUpstreamAskedForIdentity`, `TestUnparsed2xxChargesReservation` (malformed JSON, a stream cut before `message_start`, an unknown `Content-Encoding`: each `settled: reserved`, `UsageUnparsed` counted), `TestNon2xxChargesZero`, `TestNoStatusAfterBodyWrittenChargesReservation`, `TestNoStatusBeforeWriteChargesZero` (the only zeros), `TestClientCancelBeforeHeadersChargesReservation`, `TestCloseBeforeHeadersChargesReservation`, `TestPartialChargesInputAndCache`; `TestServiceTierPriorityIsSurprise`; `TestFastModeRefused`, `TestInferenceGeoRefused`, `TestServiceTierRefused`, `TestUsageSpeedPricedAsSurprise` (a 2xx whose `usage.speed` is `fast`: charged at 2× the maximum rates, a violation); T10 `TestUsageUnparsedNotedInRecord`.
3. **Under-reserving, so the cap is exceeded.** Pinned by T4 `TestWorstCaseBoundsActual` (property over the allowed shapes only: text, plain client tools, base64 images at their ceiling, base64 PDFs at the context window; any usage whose token counts fit the bound costs at most the worst case, for every model, cache TTL, tier and owner-overridden cache multiplier), `TestWorstCaseSaturates`, `TestWorstCaseImagesAtCeiling`, `TestWorstCaseCacheWrite1h`, `TestWorstCaseUsesOverrideCacheMultipliers`, `TestWorstCaseCacheReadOverrideAboveOne`, `TestWorstCasePDFAtContextWindow`; T5 `TestRefusedShapes`, `TestToolTypeAllowList` (every server tool and every typed tool refused by allow-list, a future type included), `TestImagesRefusedWithoutCeiling`, `TestLedgerNeverExceedsGranted` (property: 50 concurrent calls with random allowed shapes, `used + reserved ≤ granted` after every step, overrun only from `priced_as: max` or `surprise`), `TestMaxTokensLimitPerModel` (the role limit refuses the role model's larger request and lets the background model's pass).
4. **The gateway bypassed by configuration.** Pinned by T9 `TestRoutingKeysRefused` (table over both repository settings files, the user settings file, `CLAUDE_CONFIG_DIR`, and key cases), `TestManagedSettingsBeforeEveryStage`, `TestSelftestManagedSettingsDirExtraEntries`, `TestSelftestManagedSettingsDirUID1000`, `TestNoProxyCoversLoopback`, `TestManagedSettingsDenyWebTools`, `TestSettingsWrittenByImplementRefusedBeforeReview` (implement writes `.claude/settings.local.json` with `ANTHROPIC_BASE_URL`: review never starts, the stage fails naming the file); T10 `TestRepoSettingsRerouteRefused`, `TestManagedSettingsWrittenBeforeFirstStage`, `TestManagedSettingsPathHookNeedsLoopbackUpstream`; T11 `TestLiveGateway` step 1 (A6, live).
5. **A halted run that ends `succeeded`, ready, or `failed` with the wrong reason.** Pinned by T7 `TestHaltNeverReady` (a halt after a `ship` verdict and a passing verify), `TestHaltAfterCancelKeepsCancelled`, `TestCancelAfterHaltKeepsHalted` (a cancel during the halt grace: still `halted`), `TestHaltWinsOverAgentError` (the agent exits with `is_error` after the 403: the reason is the halt's), `TestHaltRaceFree` (`-race`: halt and cancel from two goroutines while `stage` runs), `TestCancelRecordedAtWatchTime`, `TestPostLoopCancelDoesNotOverrideHalt`; T10 `TestRunCapHaltOpensDraft`, `TestHaltAgentExitsSuccessStopsLoop` (after the 403 fakeclaude exits 0 with `is_error:false`: `halted`, no review stage starts).
6. **Double counting on retries, and prompt-cache pricing.** Pinned by T4 `TestCostCacheReadPerModel` (golden: Opus-class 0.05×, Sonnet-class 0.1×), `TestCostInputExcludesCache`, `TestCostTierUsesTotalInput`; T5 `TestRetryAfter529ChargesOnce`, `TestNon2xxChargesZero`, `TestDisconnectAfterStartChargesReservedOutput`, `TestMessageDeltaCumulativeLastWins`.
7. **Wrong-project launches, a project check that can be bypassed, or a misleading one.** Pinned by T1 `TestSelect` (every precedence step and conflict of R1, `FUGARO_CONFIG` never beating `--project`, `FUGARO_PROJECT` or the checkout), `TestInitInCheckoutWithoutConfig`, `TestGCPProjectFlagMustAgree`, `TestCloudCommandsPrintProjectHeader`, `TestInitRepoSelectsFromPath`, `TestInitGCPCallsUseID`; T2 `TestExecOldJobEnvSaysInitRepo` (an old job env: a minimal `infra_error` record pointing at `init --repo`, exit 2, never "project mismatch"), `TestImageBuildResolvesSpecWithoutOutputs`, `TestCheckJobResolvesSpec`; T2 `TestOpenCloudRefusesNameMismatch`, `TestInitNameImmutable`, `TestDiscoverRefusesForeignProjectLabel`; T3 `TestProjectCheckReadsBaseNotBranch` (the run's ref sets `project: aurora` on a branch whose base says `borealis`: refused), `TestProjectCheckRefAlsoChecked`, `TestProjectCheckMissingEnvOnCloudRun`, `TestInitRepoRefusesOtherProject`; T10 `TestTestKnobIgnoredOnCloudRun` (the live test's routing-settings knob does nothing in a job).
8. **Halting mid-write.** Pinned by T10 `TestHaltWaitsForCallBoundary` (the agent exits on its own after the 403; no SIGTERM), `TestHaltGraceThenKill`, `TestInFlightCallsFinishAfterHalt`; T7 `TestHaltLeftoversCommitMessage`.
9. **An unpinned model counted as a halt, or allowed; Vertex budgets shipped unverified.** Pinned by T5 `TestUnpinnedModelRefused400`, `TestVertexRegionOverrideAllowed`, `TestVertexUnknownLocationRefused`; T7 `TestViolationReasonNamesModel` (the violation is handled before the agent-error switch); T10 `TestUnpinnedModelFailsStage` (`failed`, not `halted`; reason names the model and stage), `TestBackgroundModelPinned`, `TestVertexEnforceRefused`; T6 `TestValidateRefusesVertexEnforce`.
10. **A bootstrap halt reported as an infrastructure error, or an `oauth` run halted for lacking a dollar cap it can never have.** Pinned by T7 `TestBootstrapHaltIsHaltedExitZero` (runner) and `TestExecBootstrapHaltExitsZero` (`internal/cli`); T10 `TestNoCapHaltAtBootstrap`, `TestOAuthEnforceWithoutCapIsNotNoCap`.
11. **A token cap that counts the wrong thing.** Pinned by T7 `TestTokenCapUsesModelUsageWhenLarger` (subagent tokens only in `modelUsage`); T10 `TestTokenCapUsesGatewayWhenLarger`; T11 `TestLiveGateway` step 5 (A-N2, live, including a resumed fix stage).
12. **Secrets or prompt text in the gateway's event logs.** Pinned by T5 `TestGatewayLogFields` (exactly the allowed fields; no body bytes, no header values beyond the two Claude Code IDs).

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
| T8 the budget in the project config and the job env | S | B | T3, T4 | `localcfg`, `infra/spec.go` after T2; `init.go`'s `runInitRepo` after T3; `pricing.Overrides` |
| T6 per-stage models and the pin rules | M | A→B | T3, T4, T8 | `internal/config` (hot, after T3); the table; `validate` reads `lc.BudgetMode()` |
| T7 `halted`, the token cap | M | B | T6 | `agent.max_run_tokens`; `runner.go` after T3 |
| T9 the agent's environment, pins, managed settings, the base image | M | B | T5, T6, T7 | the gateway's URL and token; roles; `runner.go` after T7 |
| T10 the runner drives the gateway | L | B | T5, T7, T8, T9 | everything |
| T11 docs and the live gateway test | M | — | T1–T10 | documents and exercises everything |
| T12 migration and live verification (controller) | S | — | all | |

```
Lane A:  T1 ──► T2 ──► T3 ──► T8 ──┐
                                   ├──► T6 ──► T7 ──► T9 ──► T10 ──► T11 ──► T12
Lane B:  T4 ───────────────────────┘                 ▲        ▲
         T4 ──► T5 ──────────────────────────────────┴────────┘
```

**Two lanes until they join.** T1–T3 (project identity) and T4–T5 (new packages only) touch disjoint files and may run at the same time in two worktrees, each merged into `m9a` when its reviewer passes it. T8 comes after T3: both edit `runInitRepo` in `init.go` and its tests. From T8 on, the tasks are sequential. Review, not typing, is the bottleneck: a single lane in the order T1, T4, T2, T5, T3, T8, T6, T7, T9, T10, T11, T12 is equally valid.

**Hot files,** and how they're kept safe:
- `internal/localcfg/localcfg.go`: T1 (shape, `Name`, `GCPProject`, paths), then T2 (only `checkcache.go` beside it), then T8 (`Budget`, `ModelPrices`). In that order.
- `internal/cli/cloud.go`: T1, then T2 (the name check call in `openCloud`). `internal/cli/init.go` and `init_test.go`: T1 (flags, `loadInitConfig`, `loadRepoConfig`), T2 (`--name`, outputs, immutability, `runInitRepo`'s empty-name refusal), T3 (`init --repo`'s project rule), T8 (`init --repo`'s Vertex-enforce refusal). In that order, each on top of the last.
- `internal/infra/spec.go`: T1 (`lc.Project` → `lc.GCPProject`), T2 (`platformEnv`, `ProjectName`, `withDefaults`, `RepoSpec.GCPProject`), then T8 (the budget env in `workflow`).
- `internal/cli/imagecheck.go`: T1 (`lc.GCPProject`), T2 (`FUGARO_GCP_PROJECT`, `Name` from `FUGARO_PROJECT`, `e.gcpProject`).
- `internal/backend/backend.go`: T2 only (`ExecID.GCPProject`, `OnCloudRun`).
- `internal/gitops/gitops.go`: T9 only (the model credentials stripped from git's env).
- `internal/runner/budget.go`: T7 only (`WatchCancel`'s `onCancel`).
- `internal/config/*`, `schemas/fugaro.schema.json`, `testdata/config/**`: T1 adds only the new file `project.go`; T3 (`project:`), then T6 (`agent` fields). T6 starts after T3 has landed.
- `internal/runner/runner.go`: T3 (the check in `bootstrap`, the base fetch moved), T7 (halt plumbing in `Run`, `stage`, `agentLoop`, `finalize`), T9 (the per-stage env and settings in `stage`), T10 (the gateway's start, close and events). Strictly in that order; each adds its logic in its own new file (`project.go`, `halt.go`, `gateway.go`) and keeps its `runner.go` edits to call sites.
- `internal/agent/env.go`: T9 only. `internal/agent/stream.go`, `agent.go`: T7 only (result `usage`).
- `internal/runstore/*`, `schemas/result.schema.json`, `schemas/schemas_test.go`: T7 only.
- `internal/cli/exec.go`: T2 (the old-job-env record), then T3 (`Deps.Project`, `RequireProject`), then T10 (`Spend`, `SpendErr`, `--gateway-upstream`, `--managed-settings`). T7 edits only `exec_test.go`.
- `internal/e2e/cloud_test.go`: T1 (project configs, `--gcp-project`), T2 (env names), T3 (`project:` in its fixtures), T10 (the gateway run). In that order.
- `Deps.ManagedSettingsPath` is declared by T9; T10 only sets it from `exec`.
- `internal/cli/{ls,diagnose,cancel,run}.go`: T1 (the JSON `project` field), then T7 (the halted views in `ls` and `diagnose`).
- `internal/task/task.go`: T6 only (`overrides.model` → `agent.models.coder`).
- `internal/agent/fakeclaude/main.go`: T10 only.
- `docs/**` (but the onboard skill), `internal/e2e/live_*`: T11 only, except T2's rename of `FUGARO_LIVE_PROJECT` in the two live test files.

---

### Task 1: Project configs, selection, and `--gcp-project`

**Files:**
- Create: `internal/config/project.go`, `internal/config/project_test.go`
- Modify: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`; Create: `internal/localcfg/select.go`, `internal/localcfg/select_test.go`
- Modify: `internal/cli/cloud.go` (`cloudOptions`, `addCloudFlags`, `openCloud`, drop the `log_view` note); Create: `internal/cli/project.go` (`checkoutProject`, `selectProject`, `printProjectHeader`), `internal/cli/project_test.go`
- Modify: `internal/cli/init.go`:
  - `loadInitConfig`: create `projects/<name>.yaml` from `--gcp-project`, `--region` and `--name`;
  - `loadRepoConfig` (`init --repo [PATH]`): select the project from **PATH's** checkout, not the working directory's (R1);
  - `initRun`: the field `project` is renamed **`gcpProject`** and keeps holding `lc.GCPProject`, which every GCP call uses (`infra.ProjectNumber`, `EnableService`, the `gcloud services enable … --project` hint, the workdirs); a new field **`projectName`** holds the name, for display and for the typed confirmation, which now asks for the **project name** (design §2.4: you type the canonical name) instead of the GCP ID, and says both ("type aurora to apply to GCP project proj-1234");
  - `initResult`: `Project` is now the name and a new `GCPProject` holds the ID (a `--json` shape change, accepted under D18 and listed in the docs);
- Modify: the JSON result types of `ls.go` (`lsDoc`), `run.go`, `cancel.go`, `diagnose.go`, `image.go`, `imagecheck.go`, `secrets.go` (`secrets set`), each gaining `Project string \`json:"project"\``
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
	Config     string    // --config
	Project    string    // --project
	Checkout   *Checkout // nil outside a checkout (no git toplevel, or no fugaro.yaml there)
	EnvProject string    // $FUGARO_PROJECT
	EnvConfig  string    // $FUGARO_CONFIG
	Creating   bool      // fugaro init: skip "no project config" (R1), keep the checkout rule
	Getenv     func(string) string
}
type Selection struct {
	Path, Name string
	From       string   // "--config", "--project", "checkout", "FUGARO_PROJECT", "FUGARO_CONFIG", "only project config"
	Notes      []string // e.g. "ignoring FUGARO_CONFIG (project aurora): --project selects borealis"
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
func checkoutProject(ctx context.Context, dir string) (*localcfg.Checkout, error) // dir's git toplevel; "" = the working directory
func selectProject(ctx context.Context, o cloudOptions) (localcfg.Selection, *localcfg.Config, error) // applies --gcp-project and --region (R2)
func printProjectHeader(w io.Writer, lc *localcfg.Config) // "project: aurora (GCP proj-1234)\n"
```

- `fugaro init` with no project config yet takes `--gcp-project`, `--region` and **`--name`** (required then) and writes `projects/<name>.yaml`; with one, `--name` must equal its `name:` (T2 adds the cloud side). `init --config-only --gcp-project <id>` without `--name` is refused until T2 lets it take the name from the outputs ("pass --name" for now).
- Every cloud command prints the header first on stderr, before any other output, including errors after selection. `--json` object outputs gain `project`; `secrets ls --json`, an array, stays as it is (decided).

- [ ] **Step 1: Write the failing tests.**
  - `internal/config`: `TestProjectNameRE` (`a`, `aurora`, `a-1`, 40 characters ok; empty, `-a`, `a-`, `A`, `a_b`, 41 characters refused); `TestProjectOf` (present; absent → ""; a file with unknown fields and a bad `version` still gives the name; non-YAML → error).
  - `internal/localcfg`: `TestParseOldProjectKey` (the message names `gcp_project:` and §13.1); `TestParseRequiresName`; `TestLoadProjectNameMustMatchFile`; `TestProjectsListsYAMLOnly` (ignores `*.bak`, directories, `config.yaml`); `TestOverrideGCPProjectMustAgree`; `TestSelect` (table, one row per precedence step and conflict of R1: `--config` alone; `--project` alone; checkout alone; `FUGARO_PROJECT` alone; `FUGARO_CONFIG` alone; one config; none, with and without an old `config.yaml`; several and nothing selecting lists them; `--config` + `--project` other refused; `--config` + `--project` same ok; `--project` + checkout other refused; `--config` + checkout other refused; `FUGARO_PROJECT` + checkout other refused; checkout + `--project` same ok; `--project` beats `FUGARO_PROJECT`; `--project borealis` + `FUGARO_CONFIG=aurora.yaml`: borealis, with the note; `FUGARO_PROJECT=borealis` + `FUGARO_CONFIG=aurora.yaml`: borealis, with the note; checkout aurora + `FUGARO_CONFIG=borealis.yaml`: aurora, with the note; checkout without `project:` refused; checkout naming a project with no config refused with the `--config-only` hint; the same with `Creating`: selected, no refusal; `Creating` with `--project borealis` in an aurora checkout refused; unknown name lists names and mentions `--gcp-project`).
  - `internal/cli`: `TestCloudCommandsPrintProjectHeader` (each of `ls`, `run --retry`, `cancel`, `logs`, `diagnose`, `secrets ls`, `image status`, `check`: the first stderr line is the header); `TestGCPProjectFlagMustAgree` (exit 1 on another ID; ok on the same); `TestProjectFlagNamesNoProject` (exit 1, lists names, mentions `--gcp-project`); `TestJSONOutputsCarryProject` (`ls --json`, `run --json`, `cancel --json`, `diagnose --json`); `TestInitCreatesProjectConfig` (`--gcp-project`, `--region`, `--name` → `projects/aurora.yaml` with `name` and `gcp_project`); `TestInitWithoutNameRefused`; `TestCheckoutProjectFromBrokenFugaroYAML`; `TestInitRepoSelectsFromPath` (run from outside any checkout with two project configs, `init --repo ../app` picks `app`'s project; run from a `borealis` checkout, `init --repo ../aurora-app` picks `aurora`); `TestInitGCPCallsUseID` (the fake resource manager and service usage see the GCP ID, never the name); `TestInitConfirmationTypesName` (typing the GCP ID is refused, the name accepted); `TestSelectConfigWithOtherProjectFlag` (`--config aurora.yaml --project borealis`: refused); `TestInitInCheckoutWithoutConfig` (`init --config-only --gcp-project <id>` in an aurora checkout with no project config: proceeds; `init --name borealis` there: refused); `TestValidateWithoutProjectConfig` (no project config at all: `validate` and `config example` still work).
  - `internal/e2e`: `cloud_test.go` and `init_test.go` write `projects/<name>.yaml` under a temporary `XDG_CONFIG_HOME` and pass `--gcp-project` where they passed `--project`; their assertions are otherwise unchanged.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/localcfg/ ./internal/cli/ -run 'ProjectNameRE|ProjectOf|OldProjectKey|RequiresName|NameMustMatch|ProjectsLists|OverrideGCP|Select|ProjectHeader|GCPProjectFlag|ProjectFlagNames|CarryProject|CreatesProjectConfig|WithoutName|BrokenFugaroYAML|InitRepoSelectsFromPath|InitGCPCallsUseID|ConfirmationTypesName|WithoutProjectConfig|InitInCheckout'`. Expected: FAIL (undefined names).
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
- Modify: `internal/backend/backend.go` (`ExecutionFromEnv` reads `FUGARO_GCP_PROJECT`; `ExecID.GCPProject`; new `OnCloudRun`), `internal/backend/backend_test.go`, `internal/cli/exec.go` (the old-job-env record, R4), `internal/cli/imagecheck.go` (`FUGARO_GCP_PROJECT`; the check job's `localcfg.Config` gets `Name` from `FUGARO_PROJECT`; `e.gcpProject`), `internal/cli/image.go` (unchanged logic; its test proves `infra.Repo` still resolves), `internal/cli/imagecheck_test.go`, `internal/cli/exec_test.go`, `internal/cli/run_test.go`, `internal/cli/image_cloud_test.go`, `internal/e2e/cloud_test.go` (env names)
- Modify: `deploy/terraform/gcp/modules/installation/{variables,bucket,outputs}.tf`, `deploy/terraform/gcp/roots/installation/{variables,main,outputs}.tf`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`
- Modify: `deploy/terraform/gcp/modules/repo/{variables,check}.tf`, `deploy/terraform/gcp/modules/workflow/{variables,job}.tf` (the preconditions sit on the job resources), `deploy/terraform/gcp/modules/repo/workflows.tf` (passes `fugaro_project` to each workflow), `deploy/terraform/gcp/roots/repo/{variables,main}.tf`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `deploy/terraform/gcp/roots/repo/tests/testdata/{bitbucket-oauth,github-vertex}.tfvars.json`
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
// withDefaults fills an empty ProjectName from lc.Name, so every caller of
//   infra.Repo without installation outputs (fugaro image build, the local
//   fugaro check, the in-cloud check job) still resolves a spec.
// runInitRepo (not infra.Repo) refuses when the outputs it read have no
//   project_name: "the installation has no project name; an operator runs
//   fugaro init --name <name> first".
// D17 leftovers renamed here: backend.ExecID.Project → GCPProject,
//   infra.RepoSpec.Project → GCPProject (its JSON tag stays "project": it is
//   the Terraform root's own variable), imagecheck's e.project → e.gcpProject.
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

- **Terraform.** Installation module: `variable "fugaro_project"` (string, validated with the same regexp), `labels = { fugaro = "managed", fugaro_project = var.fugaro_project }` on the runs bucket, `resource "google_storage_bucket_object" "project_marker"` (`name = "fugaro/project.json"`, `content = jsonencode({version = 1, name = var.fugaro_project, gcp_project = var.project})`, `content_type = "application/json"`), `output "project_name"`. The root passes it through. Repository module and root: `variable "fugaro_project"` and a `lifecycle { precondition }` on each workflow job and on the check job that its `env["FUGARO_PROJECT"]` equals it and `env["FUGARO_GCP_PROJECT"]` equals `var.project` (a precondition fails the plan; a `check` block would only warn). Both sides of the comparison are generated by the same Go code, so this is a consistency check against drift, not a guard. Terraform's `project` variable is unchanged (design §2.6).
- **`fugaro init --name`** (R3): the name is `--name`, else the state's `project_name` output, else the project config's `name:`; all that are set must agree, else refuse: "the installation's project name is aurora; renaming isn't supported (design §2.5)". Discovery refuses a runs bucket whose `fugaro_project` label names another project. An installation with no name anywhere refuses without `--name`. After the apply, `writeConfig` keeps `name:` and checks it against the output.
- **`fugaro init --config-only --gcp-project <id>`** reads the outputs; the file is `projects/<project_name>.yaml`; no `project_name` → refuse "the installation has no project name yet; an operator runs fugaro init --name <name>"; an existing file for that name with another `gcp_project` → refuse.

- [ ] **Step 1: Write the failing tests.**
  - `internal/infra`: `TestPlatformEnvNames` (both variables; no GCP ID under `FUGARO_PROJECT`); `TestRepoRefusesInstallationWithoutName`; `TestInstallationVarsCarryFugaroProject`; `TestRepoVarsCarryFugaroProject`; `TestDiscoverRefusesForeignProjectLabel`; `TestDiscoverAdoptsUnlabelledBucket` (the plan adds the label); `TestM4GoldenEnvRenamed` (the golden is regenerated and its README note is present).
  - `internal/localcfg`: `TestNameCheckCacheFreshForADay`, `TestNameCheckCacheIgnoresOtherBucket`.
  - `internal/cli`: `TestOpenCloudRefusesNameMismatch` (the object names `borealis`: exit 1 with the design's message); `TestOpenCloudRefusesGCPProjectMismatch`; `TestOpenCloudMissingMarker` (exit 1, the `init --name` hint); `TestOpenCloudMarkerReadErrorIsRemote` (exit 2); `TestOpenCloudUsesCache` (a second command in the day reads no object); `TestInitNameImmutable` (state output `aurora`, `--name borealis`: refused before any plan); `TestInitNameRequiredWhenUnnamed`; `TestInitConfigOnlyTakesNameFromOutputs`; `TestInitConfigOnlyWithoutProjectName`; `TestExecutionFromEnvReadsGCPProject` (`internal/backend`); `TestCheckJobReadsGCPProject`; `TestExecOldJobEnvSaysInitRepo` (on Cloud Run with `CLOUD_RUN_EXECUTION`, `CLOUD_RUN_JOB`, `FUGARO_REGION` and an old `FUGARO_PROJECT=proj-1234` but no `FUGARO_GCP_PROJECT`: `result.json` is created with `infra_error`, outcome `none` and the `init --repo` message, `exec` exits 2, the runner never starts, and no message contains "mismatch"); `TestExecOldJobEnvKeepsExistingRecord` (a `result.json` already there is left alone); `TestOnCloudRunOneSignal` (`backend.OnCloudRun` keys off `CLOUD_RUN_EXECUTION`); `TestInitRepoRefusesUnnamedInstallation` (outputs without `project_name`: refused in `runInitRepo`); `TestWithDefaultsNameFromLocalConfig` (`internal/infra`); `TestImageBuildResolvesSpecWithoutOutputs` (`fugaro image build` with a project config and no outputs still builds its spec, `FUGARO_PROJECT` from `name:`); `TestLocalCheckResolvesSpec` (`fugaro check` locally); `TestCheckJobResolvesSpec` (the in-cloud check job with `FUGARO_PROJECT` and `FUGARO_GCP_PROJECT`: its spec's env carries the name).
  - Terraform (`-tags terraform`, `terraform test`): `installation.tftest.hcl` asserts the label, the marker object's decoded content and the `project_name` output, and that a bad name fails validation; `repo.tftest.hcl` asserts the env key list now holds `FUGARO_GCP_PROJECT` and `FUGARO_PROJECT`, and that a workflow env whose `FUGARO_PROJECT` differs from `fugaro_project` fails.
- [ ] **Step 2:** `go test ./internal/infra/... ./internal/localcfg/ ./internal/cli/ ./internal/backend/ -run 'PlatformEnv|WithoutName|FugaroProject|ProjectLabel|Unlabelled|M4Golden|NameCheck|OpenCloud|InitName|ConfigOnly|ExecutionFromEnv|CheckJobReads|OldJobEnv|OnCloudRun|UnnamedInstallation|WithDefaultsName|ResolvesSpec'`, and for each root `r` in `installation repo`: `TF_DATA_DIR=$TMPDIR/tf-$r terraform -chdir=deploy/terraform/gcp/roots/$r init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/$r test` (as CI runs it). Expected: FAIL.
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
- Modify: `internal/cli/exec.go` (`FUGARO_PROJECT`; `RequireProject = backend.OnCloudRun(os.Getenv)`), `internal/cli/exec_test.go`
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
//   RequireProject bool   // backend.OnCloudRun: a missing Project fails bootstrap
func (r *run) checkProject(ctx context.Context, cfg *config.Config) error // R4
```

- **`fugaro init --repo`** requires `project:` and refuses when it differs from the installation's `project_name` ("fugaro.yaml names project borealis, but this installation is project aurora"); a missing key: "fugaro.yaml has no `project:`; add `project: aurora`". It checks the working tree's `fugaro.yaml`, while the runner checks `origin/<base>`'s, so it also reads `git show origin/<base>:fugaro.yaml` (after a fetch) and **warns** when the base lacks the key or names another project ("runs will refuse until main's fugaro.yaml says project: aurora"), for example when onboarding from a feature branch.
- **`fugaro validate`** reports the missing key with the selected project's name in the hint when a project config is selectable (no cloud call), else the generic hint.
- **`fugaro config example`** prints `project: <name>` for the selected project config, else `project: example` with the comment line `# the Fugaro project; fugaro init --config-only writes your project config`.
- **The onboard skill** runs `fugaro config example` to learn the project name, asks the user when the example shows `example`, and never invents one.

- [ ] **Step 1: Write the failing tests.**
  - `internal/config`: `TestProjectRequired`, `TestProjectBadName` (through the new invalid fixtures; the corpus tests `TestCorpus` and `TestFugaroSchemaCorpus` glob them), `TestExampleForFillsProject`, `TestExampleIsValid` (unchanged, still passes).
  - `internal/runner`: `TestProjectCheckPasses`; `TestProjectCheckReadsBaseNotBranch` (a first run at `--ref feature` whose `fugaro.yaml` says `aurora` while `main`'s says `borealis`: `infra_error`, outcome `none`, the reason names `main` and both projects, no lock object was written); `TestProjectCheckRefAlsoChecked` (the base says `aurora`, the ref says `borealis`: refused); `TestProjectCheckBaseNamesNone`; `TestProjectCheckFollowUpUsesBase`; `TestProjectCheckMissingEnvOnCloudRun` (`RequireProject` and no `Project`: the design's message); `TestProjectCheckSkippedLocally` (no `Project`, not required: passes, one log line); `TestProjectCheckBeforeLock` (the lock object never appears on a refusal); `TestProjectCheckBaseUnparseable` (a base `fugaro.yaml` with an unknown field still yields its `project:`).
  - `internal/cli`: `TestExecPassesProject` (`FUGARO_PROJECT` and `CLOUD_RUN_EXECUTION` reach `Deps` as `Project` and `RequireProject`); `TestInitRepoWarnsWhenBaseLacksProject`; `TestInitRepoRefusesOtherProject`; `TestInitRepoRequiresProject`; `TestValidateHintNamesProject`; `TestConfigExampleUsesSelectedProject`.
  - `internal/cli`: `TestSkillCommandsExist` keeps passing with the onboard skill's new `fugaro config example` line.
- [ ] **Step 2:** `go test ./internal/config/ ./schemas/ ./internal/runner/ ./internal/cli/ -run 'ProjectRequired|ProjectBadName|ExampleFor|Corpus|ProjectCheck|ExecPassesProject|InitRepo.*Project|InitRepoWarns|ValidateHint|ConfigExample'`. Expected: FAIL.
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
	ID            string
	Aliases       []string // the other spellings providers serve it under (e.g. Vertex "<id>@<date>")
	ContextTokens   int64 // the context window, for reserving PDF blocks (R5, R6)
	MaxOutputTokens int64 // the model's output maximum; 0: 1,000,000 (R5 refuses more)
	ImageTokens     int64 // per-image token ceiling (A-N9, conservative); 0: images refused
	Rates           Rates
}
const SurpriseMultiplier = 2 // R6: a response priced in a dimension the request didn't allow
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
	BodyBytes  int64
	HasPDF     bool   // a base64 PDF block: input is bounded by ContextTokens, not bytes
	ImageCount int64  // base64 image blocks, each reserved at ImageTokens (A-N9)
	MaxTokens  int64  // already refused above MaxOutputTokens by the gateway (R5)
	CacheTTL   string // "", "5m" or "1h": the longest TTL any cache_control asks for
}
// WorstCase is R6's bound, rounded up, in saturating int64 arithmetic (never
// negative, never wrapping); cache multipliers from m.Rates (overrides included).
// Server tools are refused before it is called (R5), so it has no web-search term.
func (m Model) WorstCase(q Request) Micros

type Overrides map[string]Rates
func ParseOverrides(s string) (Overrides, error) // FUGARO_MODEL_PRICES: compact JSON; keys are model IDs
func (o Overrides) Env() (string, error)
func IsAlias(model string) bool                  // sonnet, opus, haiku, opusplan, default, best, any "[1m]" suffix, and other non-ID names
```

- **The embedded table** starts from Anthropic's pricing page as cached on 2026-09-25 and is **re-checked against the live page when this task is implemented** (A11), with `CheckedAt` set to that day. Per-model cache-read multipliers differ (for example $0.20 on a $4 input is 0.05×; on a $2 input 0.1×); write multipliers are 1.25× (5 minutes) and 2× (1 hour). Rows: `claude-opus-5-5` ($4 / $20, read 0.05), `claude-opus-5` ($5 / $25), `claude-sonnet-5-5` ($2 / $10, read 0.1), `claude-sonnet-5` ($2 / $10), `claude-haiku-4-5` ($1 / $5, read 0.1), `claude-fable-5-1` ($10 / $50, read 0.025); web search $10 per 1,000 (kept in the table for M9b, unused in M9a, which refuses server tools); `ImageTokens` a conservative constant per model, above the documented per-image ceiling for the model's maximum resolution (marked `// unverified: A-N9` beside each value); long-context tiers only where the page lists one. Vertex spellings are aliases; where Vertex's own price differs, owners use `model_prices` (A11). Each row cites its source in a comment.
- **Validation** of rates and overrides: every rate finite and ≥ 0, `InputPerM` and `OutputPerM` > 0 and ≤ 1000, multipliers ≤ 10, tier thresholds > 0.
- Tests never depend on the embedded numbers except `TestEmbeddedTableSane`: they build their own tables.
- Models still served but not in the table (Fable 5, Opus 4.6–4.8, Sonnet 4.6) fail `CheckPins` with the budget on unless the owner adds them to `model_prices`; T11's docs say so.

- [ ] **Step 1: Write the failing tests.** `TestCostGolden` (table over the goldens: plain input and output; 5-minute and 1-hour cache writes; cache reads; web searches; a tier crossed only by adding cache tokens); `TestCostCacheReadPerModel`; `TestCostInputExcludesCache` (`Input` is uncached input only); `TestCostTierUsesTotalInput`; `TestWorstCaseBoundsActual` (property, `testing/quick` or a seeded loop of 10,000 cases: over the shapes the gateway allows, with random owner-overridden cache multipliers: for any `Usage` with `Input + CacheWrite* + CacheRead ≤ BodyBytes + ImageCount × ImageTokens` (≤ `ContextTokens` when `HasPDF`), `Output ≤ MaxTokens`, `WebSearches = 0`, and cache writes only when `CacheTTL` allows them, `Cost ≤ WorstCase`; the premise is exactly R6's bound, and the refused shapes (server tools included) are T5's `TestRefusedShapes`, not this property); `TestWorstCaseSaturates` (property: `MaxTokens`, `BodyBytes` and rates near their limits never give a negative or wrapped result); `TestWorstCaseImagesAtCeiling`; `TestWorstCaseCacheReadOverrideAboveOne` (an override with `cache_read: 3` reserves at 3×); `TestCostUnsplitCacheCreation` (an unsplit `cache_creation_input_tokens` priced at the highest write multiplier the request allowed); `TestWorstCaseCacheWrite1h`; `TestWorstCaseUsesOverrideCacheMultipliers` (an override with `cache_write_1h: 3` reserves at 3×); `TestWorstCasePDFAtContextWindow`; `TestWorstCaseTierFromBytes`; `TestLookupExactAndAliasOnly` (`claude-sonnet-5` never matches `claude-sonnet-5-5`); `TestMaxRates`; `TestOverridesRoundTrip`; `TestOverridesRejectBad` (NaN, negative, zero input, multiplier 11); `TestWithOverridesReplacesModel`; `TestIsAlias`; `TestFromUSD`; `TestEmbeddedTableSane` (every row validates, `Source` and `CheckedAt` set, no alias shared by two models).
- [ ] **Step 2:** `go test ./internal/pricing/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./internal/pricing/`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "pricing: the model price table, costs and the worst-case estimate"`

---

### Task 5: `internal/gateway`: the proxy, the ledger and settlement

**Files:**
- Create: `internal/gateway/gateway.go` (`Start`, `Server`, routing, auth), `internal/gateway/ledger.go` (reserve, settle, halt), `internal/gateway/forward.go` (request parsing, upstream call, unbuffered copy), `internal/gateway/sse.go` (the usage tee), `internal/gateway/vertex.go` (paths, upstream host, token), `internal/gateway/log.go` (the per-call log line), and `*_test.go` for each
- Create: `internal/gateway/anthropicfake/fake.go` and its test: an `httptest` upstream scripted per request (Anthropic and Vertex paths)
- Modify: `go.mod`, `go.sum` (`go mod tidy`: `golang.org/x/oauth2` becomes a direct dependency; it is indirect today)

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
	VertexProject   string   // the only project a Vertex path may name
	VertexLocations []string // CLOUD_ML_REGION plus every VERTEX_REGION_* value (R5)
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
	Name            string // implement, review, fix
	Model           string // the role's model (main agent and subagents)
	Background      string // the background model; "" when unset
	MaxOutputTokens int64  // bounds max_tokens on Model's requests only (R6); 0: none
}
func (s *Server) BeginStage(st Stage)
func (s *Server) EndStage() StageReport // waits for the stage's in-flight calls to settle, at most 30 s

type StageReport struct {
	Calls        int
	Used         pricing.Micros            // settled this stage
	ByModel      map[string]pricing.Micros // by serving model
	Unreconciled pricing.Micros            // charged from reservations, not from reported usage
	Overrun      pricing.Micros
	WouldHalt     int                      // observe: calls enforce would have refused
	UsageUnparsed int                      // 2xx calls settled at their reservation (R6)
	Violations    []string                 // "model X is not pinned for stage review", "max_tokens N for model X is above the stage's limit M", "speed \"fast\" is not allowed", "server tool web_fetch_20260209 is not allowed", …
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

var DefaultGeos = []string{"", "global"} // R6: allowed usage.inference_geo values (absent counts as ""); check 20 records the live one
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
	Encoding   string        // "gzip", "deflate", "zstd" (decoded) or "br" (not decoded): compress the body whatever the request asked
	DropBeforeHeaders bool   // read the whole request body, then close the connection without a status line
}
type Fake struct {
	Script []Reply           // one per request, in order
	Seen   []*http.Request   // with their bodies, for byte-equality checks
}
func New(t testing.TB, script ...Reply) (*Fake, *httptest.Server)
func StreamOK(model string, u pricing.Usage) Reply // message_start … message_delta … message_stop
```

- The log line per call (`log.go`): `msg: "model call"`, `stage`, `model`, `serving_model`, `status`, `stream`, `in`, `cache_write_5m`, `cache_write_1h`, `cache_read`, `out`, `web_searches`, `reserved_micros`, `charged_micros`, `priced_as` (`table` | `max` | `surprise`), `settled` (`usage` | `partial` | `reserved` | `zero`), `session_id`, `agent_id`, and, on an upstream error, `error_type` (the `error.type` field only). Nothing else.

- [ ] **Step 1: Write the failing tests** (each against `anthropicfake`, the gateway's client pointed at it).
  - Routing and auth: `TestHelloIsLocal`, `TestUnknownPath404`, `TestTokenRequiredAnthropic` (401 without, with a wrong one), `TestVertexNoTokenLoopback`, `TestVertexOtherProjectRefused`, `TestVertexRegionOverrideAllowed` (a path naming a `VERTEX_REGION_*` location goes to that location's host), `TestVertexUnknownLocationRefused`, `TestCountTokensFreeAndForwarded`.
  - Forwarding: `TestUpstreamAskedForIdentity` (Claude Code's `Accept-Encoding: gzip, br` reaches the upstream as `identity`), `TestCompressedResponseSettledFromUsage` (table: `gzip`, `deflate`, `zstd`: each decoded, settled from its usage, the client gets plain bytes without `Content-Encoding`), `TestBodyForwardedByteForByte`, `TestQueryStringForwarded` (`POST /v1/messages?beta=true` routes as `/v1/messages` and reaches the upstream with `?beta=true`), `TestHeadersPassThrough` (`anthropic-version`, `anthropic-beta` in; `retry-after`, `x-should-retry`, `anthropic-ratelimit-unified-reset`, `request-id` out), `TestGatewayStripsClientCredentials` (the upstream sees the real key, never the token), `TestStreamNotBuffered` (with `EventDelay` 200 ms, the client reads the first event before the second is sent), `TestPingsPassThrough`, `TestErrorBodyPassThrough` (429 and 529 with their bodies and headers), `TestNonStreamingUsage`, `TestRequestTooLarge413`, `TestMissingMaxTokens400NotForwarded`.
  - Pins and shapes: `TestUnpinnedModelRefused400` (the message names the model and stage; nothing forwarded; a violation reported), `TestBackgroundModelAllowed`, `TestAliasOfPinnedAllowed`, `TestMaxTokensLimitPerModel`, `TestVertexModelFromPath`, `TestFastModeRefused`, `TestInferenceGeoRefused`, `TestServiceTierRefused`, `TestRefusedShapes` (table: `file` and `url` sources, `mcp_servers`, `container`, `max_tokens` above the model's maximum; each a 400 naming the parameter, nothing forwarded, a violation), `TestToolTypeAllowList` (table: absent and `custom` allowed; `web_search_20260209`, `web_fetch_20260209`, `code_execution_20260521`, `tool_search_tool_regex_20251119`, `bash_20250124`, `text_editor_20250728`, `memory_20250818` and a made-up `future_tool_20990101` each refused naming the type), `TestPDFBlockReservedAtContext`, `TestImagesReservedAtCeiling` (a 300-byte base64 PNG is reserved at `ImageTokens`), `TestImagesRefusedWithoutCeiling`.
  - Money: `TestSettleCompleteStream`, `TestMessageDeltaCumulativeLastWins`, `TestNon2xxChargesZero` (400, 429, 500, 529: 0), `TestUnparsed2xxChargesReservation` (malformed JSON body; a 200 stream cut before `message_start`; `Encoding: "br"`, which the gateway doesn't decode: each settles at the full reservation, `settled: reserved`, `UsageUnparsed` counted), `TestNoStatusAfterBodyWrittenChargesReservation` (`DropBeforeHeaders`: the full reservation, `settled: reserved`), `TestNoStatusBeforeWriteChargesZero` (the upstream refuses the connection: 0), `TestClientCancelBeforeHeadersChargesReservation`, `TestCloseBeforeHeadersChargesReservation`, `TestPartialChargesInputAndCache` (a stream cut after `message_start` with cache reads and writes: those tokens plus the reserved output), `TestServiceTierPriorityIsSurprise`, `TestInferenceGeoDefaultAllowed`, `TestUsageSpeedPricedAsSurprise`, `TestDisconnectAfterStartChargesReservedOutput` (`CutAfter`: input plus reserved output, counted as `unreconciled`), `TestClientCancelCancelsUpstream` (the fake sees its request context end), `TestRetryAfter529ChargesOnce`, `TestServingModelPricesCall`, `TestUnknownServingModelPricedAtMax` (overrun counted), `TestCacheControl1hReservesAt2x`.
  - Caps: `TestEnforceRefusesPastCap` (403, `x-should-retry: false`, `permission_error`, the message), `TestHaltIsSticky`, `TestHaltedFiresOnce`, `TestObserveNeverRefuses` (`WouldHalt` counted, a log line), `TestLedgerNeverExceedsGranted` (property: 50 goroutines, random allowed shapes and usages from `StreamOK`, `-race`; after every settle `Used + Reserved ≤ Granted` unless a `priced_as: max` or `surprise` overrun happened), `TestInFlightCallsFinishAfterHalt`.
  - Logs: `TestGatewayLogFields` (exactly the listed attributes), `TestGatewayLogsNoSecrets`.
  - Lifecycle: `TestEndStageWaitsForInFlight`, `TestCloseCancelsInFlight`, `TestStartListensOnLoopbackOnly`.
- [ ] **Step 2:** `go test ./internal/gateway/...`. Expected: FAIL.
- [ ] **Step 3: Implement.** Use `http.NewResponseController(w).Flush()` after every write. The usage tee parses SSE lines from a copy of what it has already written, so a slow parser never delays the client; a line over 1 MiB is skipped (usage events are small). The Vertex token source is called per request (it caches).
- [ ] **Step 4:** `go mod tidy`, then `git diff go.mod go.sum`: the only change is `golang.org/x/oauth2` moving from the indirect to the direct block (anything else fails the step); commit that change with the task; then `go mod tidy && git diff --exit-code go.mod go.sum` passes. `go test -race ./internal/gateway/... ./internal/pricing/`. Expected: PASS.
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
- Validation without a budget: `max_output_tokens.*` 0 to 128,000; `max_run_tokens` ≥ 0; model strings non-empty when present, at most 100 characters, no whitespace. With a budget (only `fugaro validate` with a selected project config whose `budget.mode` isn't `off`, and the runner in T10): `CheckPins` too, and, when `budget.mode` is `enforce` and `agent.auth` is `vertex`, the problem "Vertex budgets are not supported yet: use budget.mode observe or off for this repository" (R11).

- [ ] **Step 1: Write the failing tests.** `TestModelForFallsBack`, `TestStageRole`, `TestMaxOutputFor`, `TestAgentModelsValidation` (through the fixtures), `TestCheckPinsAliasRefused`, `TestCheckPinsUnknownIDRefused`, `TestCheckPinsBackgroundRequired`, `TestCheckPinsOverrideTable` (an ID only in overrides passes), `TestTaskOverrideModelIsCoder` (`internal/task`), `TestValidateAppliesPinsWhenBudgetOn`, `TestValidateSkipsPinsWhenBudgetOff`, `TestValidateRefusesVertexEnforce` (and allows Vertex with `observe`) (`internal/cli`), `TestExampleIsValid`.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/task/ ./schemas/ ./internal/cli/ -run 'ModelFor|StageRole|MaxOutputFor|AgentModels|CheckPins|OverrideModel|Validate.*Pins|VertexEnforce|Example|Corpus'`. Expected: FAIL.
- [ ] **Step 3: Implement.** The example documents `agent.models`, `max_output_tokens` and `max_run_tokens`, commented out, with one line each on what the budget requires.
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "config: models per stage, output and run token limits, and the pin rules"`

---

### Task 7: The `halted` status, halts in the runner, and the token cap

**Files:**
- Modify: `internal/runstore/runstore.go` (`StatusHalted`, `Halt`, `HaltReason`, `Record.Halt`), `internal/runstore/cost.go` (`ModelSource`, `ModelBy`, `Unreconciled`), `internal/runstore/runstore_test.go`, `schemas/result.schema.json`, `schemas/schemas_test.go`
- Modify: `internal/agent/agent.go` (`Result.Usage`), `internal/agent/stream.go` (the result event's `usage`), `internal/agent/agent_test.go`, `internal/agent/testdata/stream-success.jsonl`
- Create: `internal/runner/halt.go`, `internal/runner/halt_test.go`; Modify: `internal/runner/runner.go` (`Run`'s bootstrap-halt branch, `stage`'s cause-aware context, `agentLoop`'s token check, `finalize`'s status and leftover message), `internal/runner/budget.go` (`StageError`'s halted case; `WatchCancel(parent, check, every, onCancel func() bool)`: `onCancel` is called before cancelling, and a `false` return (a halt came first) still cancels the context but records nothing), `internal/runner/budget_test.go`, `internal/runner/report.go` (the halted line), `internal/runner/followup.go` (M6's no-push endings keep `halted`)
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
//   UsageUnparsed int               `json:"usage_unparsed,omitempty"` // calls settled at their reservation (R6)

// internal/agent
type Usage struct{ Input, CacheCreation, CacheRead, Output int64 }
func (u Usage) Total() int64
// Result gains:
//   Usage      Usage            // the result event's "usage": input_tokens,
//                               // cache_creation_input_tokens, cache_read_input_tokens, output_tokens
//   ModelUsage map[string]Usage // the result event's "modelUsage" (inputTokens, outputTokens,
//                               // cacheReadInputTokens, cacheCreationInputTokens), by model
// StageTokens is R8's result-event count: max(Usage.Total(), Σ ModelUsage[*].Total()).
func (r Result) StageTokens() int64

// internal/runner
var ErrHalted = errors.New("run halted")
type HaltError struct{ Halt runstore.Halt }
func (e *HaltError) Error() string // "halted: <reason>: <detail>"
func (e *HaltError) Unwrap() error // ErrHalted
// run gains (every field below, and cancelled and failReason, guarded by mu):
//   mu         sync.Mutex
//   halt       *runstore.Halt
//   tokens     int64                   // Σ stage tokens, for the token cap
//   haltStage  context.CancelCauseFunc // the running stage's cancel, nil between stages
//   stageExtra func(stage string) (violations []string, tokens int64) // T10 sets it from the gateway's EndStage; nil until then
func (r *run) haltNow(h runstore.Halt) bool // under mu: records h and its reason unless a cancel or an earlier halt came first; reports whether it did
func (r *run) cancelHaltedStage(h runstore.Halt) // cancels the running stage with &HaltError{h} (T10 calls it after R7's grace)
func (r *run) markCancelled() bool          // under mu: the cancel watcher's side of R8's rule
```

- **`stage`** builds its context with `context.WithCancelCause` under the time budget's deadline and stores the cancel in `r.haltStage` (under `mu`). When the agent returns it applies R8's order **before** today's switch: (1) `r.halt` set → halted, `(res, false, &HaltError{…})`, no log tail, whatever `is_error`, `subtype`, exit code or `Cause`; (2) `stageExtra`'s violations → `r.fail("stage <name>: " + violations[0])`, `(res, false, …)`; (3) today's switch, where `ErrCancelled` means cancelled only if `markCancelled` wins.
- **`agentLoop`** after every stage: `r.tokens += max(res.StageTokens(), gatewayTokens)` (the second from `stageExtra`, 0 until T10); `MaxRunTokens > 0 && r.tokens >= MaxRunTokens` → `haltNow({token_cap, run, now, "run used N tokens of M"})`, stop; and it stops whenever `r.halt` is set, whatever the stage returned.
- **`finalize`**: with `r.halt` set, the leftover commit message is R7's, `ready` is false, status `halted`, outcome `draft` (pushed) or `none` (not pushed), and the record's `Halt` is set. `StageError` gains "halted during <stage>: <detail>".
- **`Run`**: `bootstrap` returning an `ErrHalted` error sets status `halted`, outcome `none`, `Halt`, reason, and returns `(rec, nil)`. T7 gives bootstrap no halt of its own (T10 adds `no_cap`); `halt_test.go` drives the branch through an unexported test hook in `export_test.go` (`SetBootstrapHaltForTest`).
- **The report** adds R8's line under the heading; `ls` shows `halted` with the reason; `diagnose` prints `Halted:   <reason> (<scope>) at <time>: <detail>` and `--json` carries `halt`; `cancel` on a halted run says it already finished (existing logic, now tested); a halted run on a PR is a valid `previous_run` for `run --pr` when it pushed (M6's rule, now tested).

- [ ] **Step 1: Write the failing tests.**
  - `internal/runstore`, `schemas`: `TestRecordHaltRoundTrip` (and an M6 record still parses), `TestCostModelFieldsRoundTrip`, the schema accepts `status: halted` with a `halt` block and each of the eight reasons, and refuses `reason: "other"`.
  - `internal/agent`: `TestParseStreamUsage` (the fixture's result event gains `usage` and a two-model `modelUsage`), `TestParseStreamNoUsage`, `TestStageTokensTakesLarger`.
  - `internal/runner`: `TestHaltMidImplementOpensDraft` (the hook halts during implement: `halted`/`draft`, the report's second line, the leftover commit's message), `TestHaltLeftoversCommitMessage`, `TestHaltNeverReady`, `TestHaltAfterCancelKeepsCancelled`, `TestCancelAfterHaltKeepsHalted` (a cancel marker lands during the halt grace: still `halted`), `TestHaltWinsOverAgentError`, `TestHaltRaceFree` (under `-race`: `haltNow` and the cancel watcher from two goroutines while `stage` runs, 100 iterations; exactly one of halted or cancelled, never both), `TestCancelRecordedAtWatchTime` (a cancel marker seen at t0, then a halt at t1 while the agent is still exiting under SIGTERM: `cancelled`), `TestPostLoopCancelDoesNotOverrideHalt` (a halt in the last stage, then the post-loop `Cause` check: still `halted`), `TestBootstrapCancelThroughMarkCancelled`, `TestWatchCancelOnCancelHook` (`budget_test.go`), `TestFinalizeAssertsOneOfHaltAndCancel`, `TestViolationReasonNamesModel` (`stageExtra` returns a violation while the agent reports `is_error`: the reason is the violation's, naming the model), `TestTokenCapUsesModelUsageWhenLarger`, `TestTokenCapHaltsAtStageBoundary` (implement uses 60 of 100, review 50: halted after review, before fix; detail names both numbers), `TestTokenCapZeroMeansNone`, `TestBootstrapHaltIsHaltedExitZero` (`Run` returns a nil error, outcome `none`, no branch pushed, no lock taken), `TestFollowUpHaltBeforePushIsNone`, `TestHaltRecordedInResult`.
  - `internal/cli`: `TestExecBootstrapHaltExitsZero`, `TestLsShowsHalted`, `TestDiagnoseHaltBlock`, `TestCancelHaltedRunAlreadyFinished`, `TestRunPRAfterHaltedRun`.
- [ ] **Step 2:** `go test ./internal/runstore/ ./schemas/ ./internal/agent/ ./internal/runner/ ./internal/runview/ ./internal/cli/ -run 'Halt|TokenCap|Usage|CostModelFields|Violation|LsShowsHalted|DiagnoseHalt|CancelHalted|RunPRAfterHalted'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Every existing runner, `ls`, `diagnose` and `cancel` test passes unchanged.
- [ ] **Step 4:** `go test -race ./...` (the race detector is required here). Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: the halted status, halts at stage and call boundaries, and the token cap"`

---

### Task 8: The budget in the project config and the job's environment

**Files:**
- Modify: `internal/localcfg/localcfg.go` (`Budget`, `ModelPrices`, validation), `internal/localcfg/localcfg_test.go`
- Modify: `internal/infra/spec.go` (`workflow`: the budget env), `internal/infra/spec_test.go`
- Create: `internal/runner/spend.go`, `internal/runner/spend_test.go`
- Modify: `internal/cli/init.go` (`runInitRepo`: the Vertex-enforce refusal), `internal/cli/init_test.go`

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

- **`fugaro init --repo`** refuses a workflow with `agent.auth: vertex` when the project's `budget.mode` is `enforce` (R11's message), before any plan.

- [ ] **Step 1: Write the failing tests.** `TestInitRepoRefusesVertexEnforce` (`internal/cli`; `observe` passes), `TestBudgetDefaultsOff`, `TestBudgetValidation` (unknown mode; enforce without a cap; a negative or NaN cap; a cap over $100,000; bad model prices; an unknown field), `TestModelPricesDefaults` (`internal/localcfg`); `TestWorkflowEnvBudgetOff` (none of the three variables), `TestWorkflowEnvBudgetEnforce`, `TestWorkflowEnvBudgetObserveNoCap`, `TestCheckJobHasNoBudgetEnv` (`internal/infra`); `TestSpendFromEnv` (table: unset → off; enforce and cap; observe; bad mode; bad cap; bad prices JSON; an override replacing a model) (`internal/runner`).
- [ ] **Step 2:** `go test ./internal/localcfg/ ./internal/infra/ ./internal/runner/ -run 'Budget|ModelPrices|WorkflowEnvBudget|CheckJobHasNoBudget|SpendFromEnv'` and `go test ./internal/cli/ -run InitRepoRefusesVertexEnforce`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "budget: mode, per-run cap and price overrides from the project config to the jobs"`

---

### Task 9: The agent's environment, pins per stage, managed settings, and the base image

**Files:**
- Modify: `internal/agent/env.go` (`EnvSpec.Gateway`), `internal/agent/agent_test.go` (or a new `env_test.go`); Create: `internal/agent/settings.go`, `internal/agent/settings_test.go`
- Modify: `internal/runner/runner.go` (`Deps.ManagedSettingsPath`, declared here only; `stage` writes the settings, adds the stage's pins to the env and runs the settings check before every stage when the gateway is on), `internal/runner/runner_test.go`
- Modify: `internal/gitops/gitops.go` (`Repo.StripEnv`), `internal/gitops/gitops_test.go`
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
//   with a Gateway, NO_PROXY and no_proxy gain "127.0.0.1,localhost" (R5).
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
// RoutingKeys lists what a settings file sets that would reroute
// Claude Code (R10): apiKeyHelper, and env keys of the R10 list, any case.
func RoutingKeys(settings []byte) ([]string, error)
// SettingsFiles are the files R10 checks: <workdir>/.claude/settings.json and
// settings.local.json, $HOME/.claude/settings.json, $CLAUDE_CONFIG_DIR/settings.json
// when set, and every entry of dir(managedPath) other than managedPath itself.
func SettingsFiles(workdir, home, claudeConfigDir, managedPath string) ([]string, error)
```

- **The runner, per stage** (`stage`): `role := config.StageRole(name)`; `model := r.cfg.Agent.ModelFor(role)`; `req.Model = model`; the stage env is `r.env` plus `PinVars(model, background, maxOut)`; when the gateway is on or any pin applies, `WriteManagedSettings(r.d.ManagedSettingsPath, gatewayVars ∪ pins)` before the agent starts. A write failure: with the gateway, the stage fails ("writing Claude Code's managed settings: …"); with pins only, a warning.
- **The runner, with the gateway on, at bootstrap and before every stage:** `RoutingKeys` of every file in `SettingsFiles` (absent files are fine; a file over 1 MiB, a symlink, or any entry other than the runner's own file in the managed directory is refused); any key → at bootstrap `infra_error`, before a later stage that stage fails, with the reason "<file> sets ANTHROPIC_BASE_URL, which would route Claude Code around Fugaro's gateway; remove it". T9 adds the check and its call guarded by `r.gatewayOn()` (a method T10 implements; until then it returns false and a test hook forces it).
- **The base image:** as root, `install -d -o fugaro -g fugaro -m 0755 /etc/claude-code`. `fugaro image selftest` adds `managed-settings-dir`: `/etc/claude-code` exists, is a directory, not a symlink, is writable **by uid 1000** (checked from the owner, group and mode bits against uid/gid 1000, because part of selftest runs as root), and is **empty** (the runner writes `managed-settings.json` at run time; a baked one is refused too). The derived image's build runs the selftest, so an `image.setup` step that wrote any managed file fails the build.
- **The managed settings deny the web tools** with the gateway on: `"permissions": {"deny": ["WebSearch", "WebFetch"]}` beside `env` (R5).
- **Defense in depth for the runner's own git** (review 2, minor 21): `gitops.(*Repo).gitRaw` builds its env from `os.Environ()`, which holds the real key; an agent-written `.git/config` (`core.fsmonitor`, a `filter.*` driver) would run under it at finalize. `gitops.Repo` gains `StripEnv []string`, and the runner sets it to the model credential names (`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_AUTH_TOKEN`) so they never reach git or anything git runs. Still within D2's `/proc` residual; it is cheap.

- [ ] **Step 1: Write the failing tests.**
  - `internal/agent`: `TestBuildEnvGatewayDropsRealKey` (api-key: env has the token and the URL, not the key; `secretValues` has the key), `TestBuildEnvGatewayVertex` (the base URL ends in `/v1`, skip-auth set, the parent's base URL dropped), `TestBuildEnvGatewayOAuthRefused`, `TestBuildEnvNoGatewayUnchanged` (every existing `BuildEnv` test still passes), `TestPinVars` (table: model only; with background; with max output; empty), `TestWriteManagedSettingsAtomic`, `TestWriteManagedSettingsRefusesSymlink`, `TestManagedSettingsHoldNoRealKey`, `TestRoutingKeysRefused` (table: each key, lower case, `apiKeyHelper`, nested `env` only, invalid JSON → error), `TestRoutingKeysAllowsModelSettings` (`ANTHROPIC_MODEL`, `permissions`, hooks are not routing keys), `TestSettingsFiles` (the user file, `CLAUDE_CONFIG_DIR`, a `managed-mcp.json` and a `managed-settings.d/` beside the managed file are all listed), `TestNoProxyCoversLoopback` (an inherited `NO_PROXY=example.invalid` becomes `example.invalid,127.0.0.1,localhost`).
  - `internal/runner`: `TestStagePinsPerRole` (implement and fix get the coder pins; review the reviewer's; `--model` follows), `TestManagedSettingsBeforeEveryStage` (the scripted agent reads the file at its start: it holds the stage's pins), `TestPinsWithoutBudgetWarnOnWriteFailure`, `TestRepoSettingsRerouteRefused` (hook forces the gateway on; `.claude/settings.json` sets `ANTHROPIC_BASE_URL`: `infra_error` before the lock), `TestSettingsWrittenByImplementRefusedBeforeReview`, `TestUserSettingsRerouteRefused` (`$HOME/.claude/settings.json`), `TestRepoSettingsIgnoredWithoutGateway`.
  - `internal/image`: `TestSelftestManagedSettingsDir` (present and writable; missing; a symlink), `TestSelftestManagedSettingsDirUID1000` (owned by root 0755: refused even when the test runs as root), `TestSelftestManagedSettingsDirExtraEntries` (a `managed-mcp.json`: refused; a baked `managed-settings.json`: refused too).
  - `internal/agent`: `TestManagedSettingsDenyWebTools`. `internal/gitops`: `TestGitEnvStripsModelCredentials` (a `core.fsmonitor` hook in `.git/config` prints its env: no `ANTHROPIC_API_KEY`).
  - `images` (`-tags docker`): `TestBaseImageManagedSettingsDir`.
- [ ] **Step 2:** `go test ./internal/agent/ ./internal/runner/ ./internal/image/ -run 'BuildEnvGateway|NoGatewayUnchanged|PinVars|ManagedSettings|RoutingKeys|SettingsFiles|NoProxy|StagePins|PinsWithoutBudget|RepoSettings|SettingsWritten|UserSettings|Selftest|DenyWebTools'` and `go test ./internal/gitops/ -run GitEnvStrips`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `go test -race ./...` and `go vet -tags docker ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "agent: gateway environment without the real key, per-stage pins, managed settings"`

---

### Task 10: The runner drives the gateway

**Files:**
- Create: `internal/runner/gateway.go`, `internal/runner/gateway_test.go`; Modify: `internal/runner/runner.go` (`Deps.Spend`, `Deps.GatewayUpstream`, `Deps.VertexTokens`; bootstrap's `no_cap` halt, pin check, gateway start; `stage`'s `BeginStage`/`EndStage`, halt watcher and cost; close after the loop), `internal/runner/cost.go` (`model_source`, `model_by`, `unreconciled`), `internal/runner/runner_test.go` (the scripted agent can make model calls)
- Modify: `internal/cli/exec.go` (`SpendFromEnv`; hidden `--gateway-upstream`, loopback only; hidden `--managed-settings` and `$FUGARO_MANAGED_SETTINGS`, R10's guard), `internal/cli/exec_test.go`
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
//   SpendErr        error              // SpendFromEnv's error; bootstrap fails with it after the claim (R11)
// (Deps.ManagedSettingsPath is T9's; exec sets it from --managed-settings or $FUGARO_MANAGED_SETTINGS, R10)
var haltGrace = 60 * time.Second // a var so TestHaltGraceThenKill can shorten it (export_test.go)
func (r *run) gatewayOn() bool // Spend.On() and auth is api-key or vertex
func (r *run) startGateway(ctx context.Context) error
func (r *run) watchHalt(stageCtx context.Context, done <-chan struct{}) // R7: haltNow at once, cancelHaltedStage after haltGrace unless done
// stageExtra (T7) returns the gateway's EndStage violations and its token count

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

- **Bootstrap order** (T10's additions in bold; everything before the lock is local and read-only): read the task; claim the record; **`Deps.SpendErr` → `infra_error`**; clone and check out; read `fugaro.yaml`; the project check (T3); **Vertex with `enforce` → `infra_error` (R11)**; **the `no_cap` check (R8: `api-key`, `enforce`, `Spend.Cap == 0` → `haltNow` and return the `HaltError`)**; **with `gatewayOn()`: `CheckPins` against `Spend.Prices` (`infra_error`), T9's settings check (`infra_error`)**; lock; provider; **with `gatewayOn()`: `startGateway`, the gateway's token registered with the redactor**; the agent env, **built with `EnvSpec.Gateway` when the gateway runs**. T9's `TestRepoSettingsRerouteRefused` ("`infra_error` before the lock") and this order agree.
- **The live test's knob** (for T11): `FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1` skips the `RoutingKeys` refusal, and only when `backend.OnCloudRun` is false and `Deps.GatewayUpstream` is empty (a local run against the real API). It exists so check 20 can measure A6 itself; on Cloud Run it is ignored with a warning.
- **Per stage:** `BeginStage({name, model, background, maxOut})`; the halt watcher: on `Halted()`, `haltNow` records the halt at once, waits for the agent to exit or `haltGrace`, then `cancelHaltedStage`; after the agent returns: `EndStage()`, handed to T7's `stageExtra` (violations first, before the agent-error switch, R8); `rec.CostUSD += report.Used.USD()` (not `res.CostUSD`); `model_by`, `unreconciled` and `usage_unparsed` accumulate, and a stage with `UsageUnparsed > 0` logs `usage_unparsed: N`; when `|res.CostUSD − report.Used| > 5%` of the larger, one warning log line (`msg: "cost cross-check"`, both figures).
- **Cost:** `rec.CostUSD` for a gateway run is set from `Ledger().Used` after `Close` (R12); per-stage figures are for the log and the cross-check only. `TestRecordCostFromLedger` has a call that settles after `EndStage`'s 30 s wait (shortened in the test) and still counts.
- **After the loop:** `Close`, with a 10 s timeout. `model_source` is `gateway` for the whole run whenever the gateway ran.
- **`exec`:** `Spend` from the environment; a malformed value is an `infra_error` record, exit 2; `--gateway-upstream` refuses anything but `http://127.0.0.1:<port>` (exit 1).

- [ ] **Step 1: Write the failing tests** (the runner harness with the scripted agent, which now can call the gateway through `req.Env`, and `anthropicfake` as the upstream).
  - `TestNoCapHaltAtBootstrap` (api-key, enforce, no cap: `halted`/`none`, `no_cap`, nil error, no lock, no push); `TestVertexEnforceRefused` (`infra_error`, R11's message; `observe` starts the gateway with the `VERTEX_REGION_*` locations allowed); `TestOAuthEnforceWithoutCapIsNotNoCap` (oauth runs normally, no gateway started); `TestOAuthNeverProxied` (oauth with the budget on: the agent env has no `ANTHROPIC_BASE_URL`, and the managed settings file holds only pins).
  - `TestGatewayCostReplacesClaudeCost` (`cost_usd` is the gateway's; `model_source: gateway`; `model_by` per model); `TestCostCrossCheckWarns`; `TestUnreconciledRecorded`; `TestUsageUnparsedNotedInRecord`; `TestTokenCapUsesGatewayWhenLarger`; `TestRecordCostFromLedger`; `TestSpendErrIsInfraErrorAfterClaim`; `TestVertexEnforceBeforeNoCap` (Vertex, `enforce`, no cap: `infra_error`, not `halted`).
  - `TestUnpinnedModelFailsStage` (`failed`, not `halted`; the reason names the model and stage; the run still opens a draft); `TestBackgroundModelPinned`; `TestPinsInvalidForBudgetIsInfraError`.
  - `TestRunCapHaltOpensDraft` (a cap of $0.10 and calls of $0.04: the third call is refused; `halted`/`draft`, `run_cap`, the report's line names spend and cap); `TestHaltWaitsForCallBoundary` (the agent exits by itself after the 403: no SIGTERM was sent, the transcript is complete); `TestHaltGraceThenKill` (an agent that ignores the 403: killed after `haltGrace`, shortened in the test through an unexported variable); `TestInFlightCallsFinishAfterHalt`; `TestHaltAgentExitsSuccessStopsLoop` (after the 403 the agent exits 0 with `is_error:false`: `halted`, the reason is the halt's, no review stage starts); `TestHaltRaceWithGateway` (`-race`: the gateway halts while a cancel marker lands); `TestObserveModeNeverHalts` (same script, observe: `succeeded`, `WouldHalt` logged).
  - `TestManagedSettingsWrittenBeforeFirstStage` (the file exists with the gateway URL before the first agent start; it never holds the real key); `TestRepoSettingsRerouteRefused` (now with the real `gatewayOn`); `TestTestKnobIgnoredOnCloudRun` (the knob with `CLOUD_RUN_EXECUTION` set: still refused, one warning); `TestTestKnobLocal` (without it: the run proceeds); `TestManagedSettingsPathHookNeedsLoopbackUpstream` (`--managed-settings` with a real upstream and the budget on: refused, exit 1).
  - `TestGatewayRunSecretScan` (a planted key `sk-ant-TESTKEY…`: absent from every bucket object, every log line, every transcript, the scripted agent's env, and the settings file; present in the upstream's `Seen` headers only).
  - `TestGatewayClosedBeforeFinalize` (finalize's provider calls happen after `Close`; a model call during finalize would be refused with connection refused).
  - `internal/cli`: `TestExecSpendFromEnv`, `TestExecPassesSpendErr` (a malformed env reaches `Deps.SpendErr`; `exec` writes no record of its own), `TestExecGatewayUpstreamLoopbackOnly`, `TestExecManagedSettingsFlag`.
  - `internal/e2e`: `TestCloudGatewayRunSecretScan` (a hermetic cloud run through `gcpfake` with `auth: api-key`, a planted key secret, `fakeclaude` calling the gateway, `anthropicfake` via `--gateway-upstream` and a temporary directory via `--managed-settings` (the rig keeps its Cloud Run variables): the run ends `succeeded`; a second run with a cap below one call ends `halted`, `run_cap`, draft; the key is in no bucket object and no log).
- [ ] **Step 2:** `go test ./internal/runner/ ./internal/cli/ ./internal/e2e/ -run 'NoCap|VertexEnforce|OAuthEnforce|OAuthNever|GatewayCost|CrossCheck|Unreconciled|UsageUnparsed|TokenCapUsesGateway|Unpinned|BackgroundModel|PinsInvalid|RunCap|HaltWaits|HaltGrace|InFlight|HaltAgentExits|HaltRace|ObserveMode|ManagedSettings|RepoSettingsReroute|TestKnob|SecretScan|GatewayClosed|ExecSpend|ExecPassesSpendErr|GatewayUpstream|RecordCostFromLedger|SpendErr|VertexEnforceBeforeNoCap'` (with `-race`). Expected: FAIL.
- [ ] **Step 3: Implement.** Every existing runner test passes unchanged: with `Spend` zero, no gateway starts and nothing in the record changes but `model_source: claude-code`.
- [ ] **Step 4:** `go test -race ./...` and `go vet -tags docker ./... && go vet -tags live ./...`. Expected: PASS.
- [ ] **Step 5: Commit.** `git commit -m "runner: route api-key and vertex runs through the gateway, halt at the per-run cap"`

---

### Task 11: Docs and the live gateway test

**Files:**
- Modify: `docs/design/v1.md` (§1's `project` note becomes the current rule; §3.4 the execution identity's `FUGARO_GCP_PROJECT`; §4.1 bootstrap order with the project check and the gateway; §4.5 halts; §4.6 `halted`, `halt`, the cost fields; §5.1 `project:`, `agent.models`, `max_output_tokens`, `max_run_tokens`; §5.4 project configs, `gcp_project`, `name`, `budget`, `model_prices`; §6.1 the gateway, the real key, D2's residual; §9.1 `--project`, `--gcp-project`, the header, `init --name`; §10 and §10.1 `model_source`)
- Modify: `docs/gcp-setup.md` (every `--project` → `--gcp-project`; `init --name`; project configs; turning the budget on), `docs/gcp-live-checklist.md` (`FUGARO_LIVE_GCP_PROJECT`, the project configs in the preconditions, check 20 below, a "Results of the fifth live run (M9a)" section to fill in)
- Modify: `plugin/skills/followup/SKILL.md` (a halted PR can be followed up after the cap is raised)
- The docs must say, in `v1.md` §6.1 and §11's table (in this repository's copy of the threat model, v1 §6.1) and in `gcp-setup.md`:
  - the managed settings file guards against **committed** configuration only; it is writable by the agent's own user and is rewritten before every stage, so a compromised agent can still route around the gateway (D2);
  - `oauth` has no gateway: its token cap rests on the result event's `usage`/`modelUsage` alone, and `agent.max_run_tokens` lives in `fugaro.yaml`, so a repository writer can lift it;
  - the effective dollar cap is lower than the nominal one by up to one worst-case call, a dropped stream is charged its full reserved output, and a week of `observe` calibrates the cap;
  - Vertex budgets in `enforce` are refused until check 20's Vertex FACTs are recorded; on Vertex only the basic web search exists and there is no web fetch;
  - the request shapes the gateway refuses (fast mode, `inference_geo`, `service_tier`, every server tool and typed tool, MCP servers, `file`/`url` sources, images for a model without a ceiling) and why, and that WebSearch and WebFetch are denied to the agent while the gateway is on;
  - Priority-Tier organizations: a `priority` service tier in a response fails the stage, so pin models Priority Tier excludes or keep the budget off;
  - models still served but not in the embedded table need a `model_prices` entry to be pinned with the budget on;
  - `fugaro init --json`'s `project` is now the name and `gcp_project` the ID (D18).
- Create: `internal/e2e/live_gateway_test.go` (`//go:build live && docker`)
- Modify: `.github/workflows/ci.yml` (add `go vet -tags 'live docker' ./...` beside the single-tag vets, so the live gateway test at least compiles in CI)

**Interfaces:**
- Consumes: everything.
- Produces: `TestLiveGateway` (check 20). Inputs: `FUGARO_LIVE_BASE_IMAGE` (a locally built base image from this branch), `FUGARO_LIVE_ANTHROPIC_API_KEY` (the user's own key; the test refuses to run without `FUGARO_LIVE_SPEND_OK=1`). It runs `fugaro exec` **locally in that image** (`docker run`, no GCP), against a `file://` bucket, the fake git provider and a small fixture repository with `auth: api-key`, **different pinned models per role** so a role-pin mismatch is caught (coder `claude-sonnet-5`, reviewer `claude-sonnet-5-5`, background `claude-haiku-4-5`; `max_output_tokens` 4,000 for each role), `review_rounds: 1` and a task that makes the review ask for one fix (so the fix stage resumes implement's session) and invites a subagent, budget `enforce` with a **$2.00 cap**, chosen so one call's worst case fits well inside it (a 150 KB body at $2/M input with a 1-hour cache write is $0.60, plus 4,000 output tokens at $10/M, $0.04), while the run's real spend stays around $0.30, and a repository `.claude/settings.json` that sets `ANTHROPIC_BASE_URL` to `http://127.0.0.1:9`, with T10's `FUGARO_TEST_ALLOW_ROUTING_SETTINGS=1` set inside the container so the refusal steps aside and A6 itself is measured. It then asserts, logging each answer as a `FACT`:
  1. the run reached the model through the gateway (the gateway logged calls; the run didn't fail on connection refused): **A6**;
  2. every Claude Code call the gateway saw used the model pinned for its role in its stage (the main agent and subagents the role model, the rest the background model), and nothing else was attempted (no violations): **A-N6**;
  3. the gateway's settled cost and Claude Code's `total_cost_usd` agree within 5%: **A10, A11**;
  4. per model, `max_tokens` never exceeded that role's `max_output_tokens`, and background requests chose their own: **A9** (Anthropic side), recorded per model;
  5. per stage, the result event's Σ`usage`, its Σ`modelUsage` and the gateway's own token count, including the resumed fix stage and the subagent's calls, logged side by side; the test fails if the result-event count is more than 5% below the gateway's for any stage (the token cap would under-count for `oauth`): **A-N2**;
  6. a second run with a $0.002 cap (below any call's worst case) halts `run_cap` on its first call, and `claude` exited on its own within `haltGrace` after the 403: **A-N1**; the run ends `halted`, not `failed`: R8;
  7. the planted real key is in no transcript, log or bucket object;
  8. the task gives the agent one small PNG to look at: the gateway's reservation for that call is at least what it was charged for the image (A-N9), and the observed `usage.inference_geo`, `usage.service_tier` and every `tools[].type` are logged as `FACT`s (A-N8).
  The key is read from the environment of the person running the test at their own terminal; the test never logs it, and the controller never holds it (Task 12, step 8).

- [ ] **Step 1:** Write the docs. Then `go test ./internal/cli/ -run TestSkillCommandsExist`, `go test ./internal/config/ -run Example`, and `grep -rn -- '--project ' docs/gcp-setup.md docs/gcp-live-checklist.md` shows only `--project <name>` uses.
- [ ] **Step 2:** Write `live_gateway_test.go` and the CI line. `go vet -tags 'live docker' ./...`. Expected: PASS (the test isn't run here).
- [ ] **Step 3: Commit.** `git commit -m "docs: project identity, the gateway and halted runs; the live gateway check"`

---

### Task 12: The one-time migration and live verification (controller-run, with user confirmations)

This task writes no code. The controller runs it one step at a time and asks the user before every **⚠ CONFIRM** step. Each step is its own question: one approval never covers the next. Record every outcome and `FACT` in the M9a PR. Anything that fails goes back to its task as a bug, with a hermetic test first.

**Freeze launches for the whole task.** Between the base image rebuild and each repository's `init --repo`, a new runner meets an old job environment and ends at once with an `infra_error` record pointing at `init --repo` (R4), wasting the run. **The scheduled image checks are paused too** (step 0b): between step 2 and each repository's `init --repo`, the old runner's strict decoder refuses the new `project:` key, and a check or rebuild on the new base meets the old job env; a check-triggered rebuild would also move `:latest` behind the migration's back. The only `:latest` moves during the task are step 5's own builds, each right before that repository's `init --repo`.

**Preconditions (read-only):**
- With the **old** binary (from `main` before M9a), `fugaro ls --since 1d` shows no active run in either repository.
- `FUGARO` is built from `m9a`. `gcloud config get project` is not relied on (memory: never rely on the gcloud default project); every `gcloud` command passes `--project <gcp-id>`.
- The user has chosen the canonical project name `<slug>` (decided: the user picks it; it is permanent).

**Steps (design §13.1):**
0. **Snapshot, read-only, before anything changes:** save to the scratchpad the old `config.yaml`, the current `base_image` tag, `gcloud run jobs describe <job> --region <region> --project <gcp-id> --format json` for every job and check job, and the Scheduler job list below. The rollback restores from these.
0b. ⚠ CONFIRM **Pause each repository's image-check Scheduler job, before step 1** (the old binary still reads `config.yaml` then; labels and `--print-vars` are not used: the Scheduler jobs carry no labels, and the new binary refuses before step 2):
   - list them with `gcloud scheduler jobs list --location <scheduler_region> --project <gcp-id> --format 'value(name)' --filter 'name~/jobs/fugarochk-'` (read-only), and print the list;
   - **require a non-empty list with one job per onboarded repository** (the old local config's `repos:`); anything else stops the task;
   - `gcloud scheduler jobs pause <name> --location <scheduler_region> --project <gcp-id>` for each, and record the names.
1. **The local config, by hand** (local file only; no confirmation needed, but show the diff): `mkdir -p ~/.config/fugaro/projects`; copy `config.yaml` to `projects/<slug>.yaml`; rename `project:` to `gcp_project:`; add `name: <slug>`; move `config.yaml` to `config.yaml.bak`. Check: `$FUGARO validate` in a checkout fails only on the missing `project:`, and `$FUGARO ls` outside a checkout fails only on the missing `fugaro/project.json` (R3), which proves the file parses.
2. **`project: <slug>` in each repository's `fugaro.yaml`,** on its base branch:
   - **the sandbox:** ⚠ CONFIRM the controller commits `project: <slug>` to `acme/sandbox`'s base branch, as in earlier milestones;
   - **the web repository:** the **user** opens and merges their own PR with that one line; the controller waits until the user says it's merged, then reads it back (`git show origin/<base>:fugaro.yaml` in a fresh fetch).
3. **The base image:** ⚠ CONFIRM build and push the new base from `m9a`: `images/build-base.sh web-node <region>-docker.pkg.dev/<gcp-id>/fugaro-base/fugaro-web-node:dev-<commit>`, then `docker push`.
4. **The installation:** ⚠ CONFIRM `$FUGARO init --name <slug> --base-image <that tag>` (shows the plan: the bucket label, the marker object, the output, `base_image`; the user confirms the apply). Check: `projects/<slug>.yaml` still has `name: <slug>`; `gsutil cat gs://<runs bucket>/fugaro/project.json` (read-only) shows the name and GCP ID.
5. **Each repository, from its checkout,** in turn: ⚠ CONFIRM `$FUGARO image build` (the derived image on the new base, about $0.05); then ⚠ CONFIRM `$FUGARO init --repo` (the plan shows `FUGARO_PROJECT=<slug>` and `FUGARO_GCP_PROJECT=<id>` on every job and the check job, and nothing budget-related: both repositories use `oauth`, and no `budget:` block is set). Then ⚠ CONFIRM `gcloud scheduler jobs resume <that repository's fugarochk- job> --location <scheduler_region> --project <gcp-id>` (the apply may already show `paused` going back to `false`; the resume is then a no-op), and `gcloud scheduler jobs describe` shows `ENABLED`.
6. **Verification (read-only):**
   - `$FUGARO ls` prints `project: <slug> (GCP <id>)` first, and lists both repositories' runs;
   - `gcloud run jobs describe <one job per repository> --region <region> --project <id> --format json` shows both variables;
   - `gcloud storage buckets describe gs://<runs bucket> --project <id> --format 'value(labels)'` shows `fugaro_project=<slug>`;
   - `$FUGARO validate` passes in both checkouts;
   - `gcloud scheduler jobs describe <each check job> --location <scheduler_region> --project <id> --format 'value(state)'` shows `ENABLED` for every repository;
   - outside a checkout, with a second, dummy project config present in a temporary `XDG_CONFIG_HOME`, a command without `--project` refuses and lists both; with exactly one, it works.
7. ⚠ CONFIRM **A sandbox run** (live check 13, `TestLiveSandboxRun`): it ends with a ready PR; its record has `model_source: claude-code` and no `halt`. This checks the project check and the pins path (no pins set) on a real job.
8. **The live gateway check** (check 20, `TestLiveGateway`). No Anthropic API key is known to us, and Vertex `enforce` is refused in M9a (R11), so there are two paths (the open question):
   - **The user has an API key:** ⚠ CONFIRM the user runs, **at their own terminal**, `FUGARO_LIVE_ANTHROPIC_API_KEY=… FUGARO_LIVE_SPEND_OK=1 FUGARO_LIVE_BASE_IMAGE=<local base tag> go test -tags 'live docker' -run TestLiveGateway -v ./internal/e2e/` (about $0.30, local Docker, no GCP). The key is typed or pasted by the user and never passed to, printed for, or stored by the controller; the controller reads only the test's `FACT` lines afterwards. Record the `FACT`s for A6, A9, A10, A11, A-N1, A-N2, A-N6 and A-N7. A false A6 means the settings refusal (R10) is the only guard for committed settings: say so in the PR and in §6.1.
   - **The user has none:** the check is **deferred**. The PR says so, the gateway ships with every blocking assumption unverified, and the docs tell owners to run check 20 before turning the budget on for an `api-key` repository. Nothing in the two existing (oauth) repositories depends on it.
9. **The web repository** gets a real run only with the user's explicit go-ahead and task text (⚠ CONFIRM, its own question).
10. Fill in "Results of the fifth live run (M9a)" in `gcp-live-checklist.md` with the `FACT`s, in the M9a PR.

**Rollback, from step 0's snapshot:** pause the Scheduler jobs again first (as in step 0b), then re-run the old binary with `config.yaml.bak` restored (the old CLI reads it), point `base_image` back at the M6 tag (⚠ CONFIRM `init --base-image <M6 tag> --yes` with the old binary), rebuild both repositories' images (⚠ CONFIRM each), and ⚠ CONFIRM `init --repo` with the old binary in each checkout, which writes the old `FUGARO_PROJECT` (the GCP ID). **This is not free:** the old binary's `init` doesn't know the new variable, so its plan deletes the marker object `fugaro/project.json` and the bucket's `fugaro_project` label and drops the `project_name` output (the operator confirms that plan); migrating again later needs `init --name <slug>` again. Compare each job with its step-0 `describe` output afterwards. The `project:` line in each `fugaro.yaml` must be reverted too (the old validator refuses the unknown key): the user's PR for the web repository, a controller commit for the sandbox with the user's OK. Resume the Scheduler jobs only after each repository's old `init --repo`.

---

## Decisions recorded (formerly open)

- **Project configs and selection:** R1. `$FUGARO_CONFIG` stays as `--config`'s environment form and is refused inside a checkout of another project (user, 2026-09-30).
- **The existing installation's project name** is permanent, and the user picks it before T12 (user).
- **Refusing configuration that reroutes Claude Code** when the gateway is on: yes, before every stage and over every settings location (R10; user).
- **`agent.max_run_tokens` stays in `fugaro.yaml`,** as designed; an owner-side cap comes with M9b (user). The docs say that a repository writer can lift it.
- **Array-typed `--json` outputs are unchanged;** the stderr header names the project (user).
- **Setting the budget in M9a:** edit the project config and re-run `fugaro init --repo` (user).
- **Where the name lives and how launchers check it:** R3 (a Terraform-managed marker object beside the label and the output), because launchers can't read bucket labels or the state.
- **The runner's check:** R4 (the base, and the ref when it differs; before the lock; required on Cloud Run, keyed off one signal). An old job env fails in `exec` with an `infra_error` record, never a "mismatch".
- **The gateway fails toward the cap** whenever it can't tell what a 2xx cost (R6, ruling C1), asks upstream for `identity` and decompresses anyway.
- **Unpriced dimensions are refused** (fast mode, `inference_geo`, `service_tier`; ruling C2), as are request shapes whose cost the body doesn't bound (R5).
- **Vertex with `enforce` is refused in M9a** (R11, ruling I7).
- **Halts:** at call boundaries, with a 60 s grace before a kill (R7); the first of halt and cancel in time wins, under a mutex, and a halt beats the agent's own error and any later stage (R8, ruling I4).
- **The report keeps its first line** and adds the halted line below it (R8), so M6's marker and heading recognizers keep working.
- **Pins apply whatever the budget mode;** explicit priced IDs are required only with the budget on (R9). Output limits apply per pinned model (R6, ruling I8).
- **Managed settings are written before every stage** at a path tests can move only alongside a loopback upstream (R10, ruling I5); they guard against committed configuration only (ruling I6).
- **The budget env is plain job env from `infra/spec.go`,** not new Terraform variables (R11).
- **`init` uses the GCP ID for every GCP call and asks the operator to type the project name** to confirm (T1, ruling I1).

## Open questions for the user

1. **How the gateway is verified live** (still open). Both existing repositories use `oauth`, which never goes through the gateway, and Vertex `enforce` is refused for now (R11), so a Vertex sandbox workflow can't verify `enforce` either. No Anthropic API key is known to us. *Default:* check 20, a local Docker run on the **user's own** API key, supplied at the user's terminal and never to the controller, a different model per role, about $0.30, gated by `FUGARO_LIVE_SPEND_OK=1` (T12 step 8, first path). *If the user has no key:* the check is deferred and M9a ships with the blocking assumptions below unverified, documented, and every repository's budget off (T12 step 8, second path).

## Unverified assumptions to check live

The design's A-list items that bear on M9a, and the new ones this plan relies on (A-N*). **Blocks M9a** means a false answer changes code, or keeps a feature off, before M9a can be relied on.

| # | Assumption | Blocks M9a? | Where it's checked | If false |
|---|---|---|---|---|
| A3 | An upstream **non-2xx** reply isn't billed; an interrupted 2xx stream bills the tokens generated | No (R6 charges 0 only for non-2xx and the reservation for anything it can't parse) | Documentation; check 20 cannot provoke it cheaply | Charge input tokens on non-2xx replies too (one line in `ledger.go`) |
| A6 | Managed settings' `env` overrides the repository's `.claude/settings.json` `env` | **Yes** (the bypass guard) | Check 20, step 1 | A6.1 |
| A6.1 | *If A6 is false,* the runner fails closed: R10's refusal before every stage stays the only guard for committed settings, and the budget stays refused for `api-key` until a stronger guard exists | — | — | A follow-up change (hermetic test first) makes the runner refuse to start the gateway for `api-key` when the checkout has any `.claude/settings*.json` with an `env` block; until then owners keep the budget off, and §6.1 says why |
| A9 | Per model, `max_tokens` never exceeds that role's `CLAUDE_CODE_MAX_OUTPUT_TOKENS`; background requests choose their own; usage fields are as documented through Vertex `streamRawPredict` | **Yes** (Anthropic side); Vertex `enforce` stays refused until the Vertex side is checked (R11) | Check 20, step 4 (Anthropic, per model); Vertex only with a live Vertex run | Reserve the model's maximum output |
| A10 | Every billed call Claude Code makes goes through the base URL | **Yes** (the cap's meaning) | Check 20, step 3 | Budget the difference as per-stage overhead, documented |
| A11 | The embedded prices are current, including per-model cache-read multipliers and any long-context tiers, and Vertex bills the same | No (overrides cover it) | T4 re-reads the pricing page; check 20, step 3 | Update the table; owners set `model_prices` for Vertex |
| A-N1 | In `-p` mode Claude Code ends the run, without retrying, on a 403 with `x-should-retry: false` | No (R8 records the halt at once; the grace then kills it) | Check 20, step 6 | Halts always take `haltGrace`; lower it |
| A-N2 | The result event's `usage` and `modelUsage` together count every token the stage used, subagents, background calls and a resumed session included, without double counting | **Yes** for the `oauth` token cap | Check 20, step 5 (compared with the gateway per stage, a resumed fix stage and a subagent included); the sandbox run's result events (T12 step 7) | The cap takes the larger count already (R8); if both under-count, read the per-message `assistant` events' usage instead, and document the gap for `oauth` |
| A-N3 | With `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, Claude Code sends Vertex-shaped paths to `ANTHROPIC_VERTEX_BASE_URL` with the pinned model and an allowed location in the path | **Yes** for Vertex `enforce`, which is refused until then (R11) | Only a live Vertex run | Vertex stays at `observe` or `off` |
| A-N4 | `roles/storage.objectAdmin` lacks `storage.buckets.get`, so launchers can't read bucket labels | No (R3 works either way) | Documentation | None needed |
| A-N5 | The pinned Claude Code version reads `/etc/claude-code/managed-settings.json` on Linux and no drop-in beside it that the selftest doesn't know | **Yes** (with A6) | Check 20, step 1 | Find the version's path; the file's location is one constant, and the selftest's allow-list grows with it |
| A-N6 | Claude Code's background requests honour `ANTHROPIC_DEFAULT_HAIKU_MODEL`, and subagents `CLAUDE_CODE_SUBAGENT_MODEL` | **Yes** (D8 would fail every stage) | Check 20, step 2, with a different model per role | Pin the missing variable, once found in the violations log |
| A-N7 | Claude Code accepts a plain `http://127.0.0.1:<port>` base URL | **Yes** | Check 20, step 1 | Serve TLS on loopback with a per-run CA in `NODE_EXTRA_CA_CERTS` |
| A-N8 | Claude Code 2.1.283 doesn't send `speed`, `inference_geo` or `service_tier` by default, declares its tools with no `type` (or `custom`) and no server tool once WebSearch and WebFetch are denied, and api.anthropic.com honours `Accept-Encoding: identity`; the `usage.inference_geo` and `usage.service_tier` a default request returns are in `gateway.DefaultGeos` and `standard` | **Yes** (a typed tool or a default fast mode would fail every stage, D8) | Check 20 (no violations; the values logged as `FACT`s; no compressed replies logged) | Add the observed tool type or value to the allow-list, with a test, or tell owners how to turn the feature off |
| A-N9 | The per-image `ImageTokens` ceilings in the table are at least what the API bills for one image at the model's maximum resolution | No (images are rarely sent by the agent; a low ceiling shows as `overrun`) | Documentation; check 20 sends one image and compares | Raise the constant; until then an image can overrun by the difference |

A-list items that don't bear on M9a (A2, A4, A5, A7, A8, A12–A15) belong to M9b and later.

## Conflicts between the design and the code (resolved by this plan)

- **§2.4 has the CLI check the name against the bucket label or the outputs,** but launchers can read neither (R3, A-N4). Resolved by the marker object `fugaro/project.json`.
- **§2.5 puts the runner's check before the lock,** but a first run fetches its base after the lock today. The fetch moves before the lock (R4); it changes nothing remote.
- **§2.5's "a job missing either variable fails at bootstrap"** can't happen in the runner: `exec` needs `FUGARO_GCP_PROJECT` before the runner starts. Resolved by R4's old-job-env record in `exec`.
- **§2.6 mentions the skills `fugaro:launch` and `fugaro:status`,** which don't exist yet (M7). Only `fugaro:onboard` (T3) and `fugaro:followup` (T11) change.
- **§5.1 gives the agent a per-run gateway token on Vertex too,** but `CLAUDE_CODE_SKIP_VERTEX_AUTH=1` sends none. Resolved by R5 (loopback only on Vertex).
- **§5.1 accepts one Vertex region,** but `BuildEnv` passes `VERTEX_REGION_*` overrides that Claude Code puts in the path. Resolved by R5's allowed locations.
- **§5.1 says the runner writes `/etc/claude-code/managed-settings.json`,** but the runner runs as the non-root `fugaro` user. Resolved by the base image's directory (T9) and a selftest check as uid 1000.
- **§5.2 and §5.4 price only model, tokens, cache and web search,** but a request can ask for fast mode or a geography, and some shapes' size isn't in the body. Resolved by R5's refusals and R6's settlement rules.
- **§5.5 charges 0 for "an upstream error before `message_start`",** which would also let an unparseable 2xx through free. Resolved by R6: 0 only for non-2xx.
- **§5.9's report "starts `## Fugaro — Halted`",** but every report starts `### Fugaro run \`<id>\``, which M6's `FugaroRun` recognizes as a legacy heading. Resolved by R8: the heading stays and the halted line follows it.
- **§5.9's "a halt before the branch exists: exit 0"** needs `Run` to return a nil error from a bootstrap failure; every bootstrap error exits 2 today. Resolved in T7.
- **§6.6 and §15 task 7 route the budget env "through the tfvars",** but job env is built in Go (`infra.platformEnv`, `workflow`) and passed as a map, so no Terraform variable is needed (R11). The repository root's new `fugaro_project` variable is used only for its consistency check.
- **The word "budget" is already taken twice:** `runner.Budget` (the run's time budget) and `fugaro init --budget` / `infra.Budget` (a GCP billing budget). Both stay; the new code uses `Spend`, `gateway` and `pricing`, and only the project config's `budget:` block (the design's name) uses the word.
- **`--json` outputs gain `project` (§2.4),** but `secrets ls --json` is an array (decision above).
- **`gcp.Options.Project`, `localcfg.Config.Project` and `initRun.project`** meant the GCP ID; they become `GCPProject` / `gcpProject` in T1, so no identifier keeps two meanings (D17).
- **The `log_view` note in `openCloud`** exists for `--project` pointing at another GCP project, which R2 forbids; it is removed in T1. `checkRegistryHostProject` checks the file's own consistency and stays; only its comment about `--project` goes.
- **§2.1 says `validate` refuses aliases "when the budget is on",** but `validate` is a local command and today knows nothing of the budget. Resolved in T6: it reads the selected project config, with no cloud call.

## Review disposition (round 1, 2026-09-30)

Review: `.superpowers/sdd/m9a-plan-review.md` (2 Critical, 11 Important, 12 Minor), with the coordinator's binding rulings.

- **C1:** fixed as ruled. `Accept-Encoding: identity` upstream and decompression anyway (R5); 0 only for non-2xx; an unparsed 2xx settles at its full reservation, `usage_unparsed` in the stage log and the record (R6). Tests: `TestUpstreamAskedForIdentity`, `TestCompressedResponseSettledFromUsage`, `TestUnparsed2xxChargesReservation`, `TestNon2xxChargesZero`, `TestUsageUnparsedNotedInRecord`.
- **C2:** fixed as ruled. `speed` other than standard, a non-default `inference_geo` and `service_tier` are D8 violations; a surprise in the response's `usage` is charged at 2× the maximum rates and fails the stage (R5, R6). Tests: `TestFastModeRefused`, `TestInferenceGeoRefused`, `TestServiceTierRefused`, `TestUsageSpeedPricedAsSurprise`.
- **I1:** fixed as ruled. `initRun.gcpProject` keeps the GCP ID for every GCP call; `projectName` is displayed and typed at the confirmation (T1). Tests: `TestInitGCPCallsUseID`, `TestInitConfirmationTypesName`.
- **I2:** fixed. `init --repo PATH` selects from PATH's `fugaro.yaml`; `loadRepoConfig` is in T1. Test: `TestInitRepoSelectsFromPath`.
- **I3:** fixed as ruled. `exec` writes a minimal `infra_error` record pointing at the migration when the new env variables are missing, before the runner (R4, T2). Tests: `TestExecOldJobEnvSaysInitRepo`, `TestExecOldJobEnvKeepsExistingRecord`. The launch freeze stays.
- **I4:** fixed as ruled. `haltNow` records at once under `r.mu`; the first of halt and cancel in time wins; a halt beats the agent's error and stops the loop; violations are handled before the agent-error switch and name the model (R8, T7, T10). Tests: `TestHaltWinsOverAgentError`, `TestHaltRaceFree`, `TestViolationReasonNamesModel`, `TestHaltAgentExitsSuccessStopsLoop`, `TestHaltRaceWithGateway`, all under `-race`.
- **I5:** fixed as ruled. `exec --managed-settings` / `$FUGARO_MANAGED_SETTINGS`, honoured only with a loopback upstream or the budget off; the rig doesn't depend on `CLOUD_RUN_JOB`; the selftest checks uid 1000 (R10, T9, T10). Tests: `TestExecManagedSettingsFlag`, `TestManagedSettingsPathHookNeedsLoopbackUpstream`, `TestSelftestManagedSettingsDirUID1000`.
- **I6:** fixed as ruled. The settings check runs before every stage over the repository files, `~/.claude/settings.json`, `$CLAUDE_CONFIG_DIR` and extra files in `/etc/claude-code`; the selftest refuses extra managed files; the docs say the managed file guards committed configuration only (R10, T9, T11). Tests: `TestSettingsWrittenByImplementRefusedBeforeReview`, `TestUserSettingsRerouteRefused`, `TestSettingsFiles`, `TestSelftestManagedSettingsDirExtraEntries`.
- **I7:** fixed as ruled. Vertex `enforce` refused by `validate`, `init --repo` and the runner; `VERTEX_REGION_*` locations accepted with the gateway (R5, R11, T6, T8, T10). Tests: `TestValidateRefusesVertexEnforce`, `TestInitRepoRefusesVertexEnforce`, `TestVertexEnforceRefused`, `TestVertexRegionOverrideAllowed`, `TestVertexUnknownLocationRefused`.
- **I8:** fixed as ruled. `max_output_tokens` bounds only the role model's requests (R6, R9); check 20 pins a different model per role. Test: `TestMaxTokensLimitPerModel`.
- **I9:** fixed. Refused: `file`/`url` sources, `mcp_servers`, `container`, server tools other than `web_search_*` (R5). Reserved at the context window: base64 PDFs (R6). The property test covers allowed shapes only; refused shapes have their own table test. Tests: `TestRefusedShapes`, `TestPDFBlockReservedAtContext`, `TestWorstCasePDFAtContextWindow`.
- **I10:** fixed as ruled. The Scheduler check jobs are paused from before step 2 until each repository's `init --repo`, the only `:latest` moves are step 5's, and the rollback pauses them too (T12).
- **I11:** fixed as ruled. The token cap uses `max(gateway count, max(Σusage, ΣmodelUsage))`; check 20 compares all three per stage across a resumed fix stage and a subagent; the `oauth` limitation is documented (R8, T7, T11). Tests: `TestTokenCapUsesModelUsageWhenLarger`, `TestTokenCapUsesGatewayWhenLarger`, `TestStageTokensTakesLarger`.
- **Minor, applied:** M1 (cache multipliers from the effective rates, overrides included; `TestWorstCaseUsesOverrideCacheMultipliers`); M2 (hot files: `cloud_test.go` in T1, T2, T3 and T10; `Deps.ManagedSettingsPath` T9 only; the Terraform preconditions on the job resources, with `modules/workflow` and `modules/repo/check.tf` in T2's list, stated as a consistency check, not a guard); M3 (`checkRegistryHostProject` stays; the Conflicts line fixed); M4 (the effective cap and the dropped-stream charge stated in R6 and the docs); M5 (`NO_PROXY` gains loopback; `TestNoProxyCoversLoopback`); M6 (one Cloud Run signal, `backend.OnCloudRun`; `TestOnCloudRunOneSignal`); M7 (`init --repo` warns when the base's `project:` is missing or differs; `TestInitRepoWarnsWhenBaseLacksProject`); M8 (a `TestSelect` row for `--config` with another `--project`; `validate`, `config example` and `image build --local` never need a project config; `TestValidateWithoutProjectConfig`); M9 (`go mod tidy` in T5 makes `golang.org/x/oauth2` direct); M10 (the uid-1000 selftest check); M12 (§11 and the docs say a repository can lift the `oauth` token cap). The price-table note (models not in the table need `model_prices`) is in T4 and T11.
- **Minor, rejected:** M11 (reading `FUGARO_PROJECT` from a job's env as a second name check): the marker object already answers R3, a second source adds a `run.jobs.get` per command for no new guarantee, and a project with no jobs yet would have nothing to read.

## Review disposition (round 2, 2026-09-30)

Review: `.superpowers/sdd/m9a-plan-review2.md` (0 Critical, 10 Important, 21 Minor), with the coordinator's binding rulings.

- **I-A:** fixed as ruled. `runInitRepo` refuses an unnamed installation; `withDefaults` fills `ProjectName` from `lc.Name`; the check job's `localcfg.Config` gets `Name` from `FUGARO_PROJECT` (T2). Tests: `TestInitRepoRefusesUnnamedInstallation`, `TestWithDefaultsNameFromLocalConfig`, `TestImageBuildResolvesSpecWithoutOutputs`, `TestLocalCheckResolvesSpec`, `TestCheckJobResolvesSpec`.
- **I-B:** fixed as ruled. T8 depends on T3; the `init.go`/`init_test.go` hot-file line lists T1, T2, T3, T8; the graph and lanes are redrawn.
- **I-C:** fixed as ruled. Precedence `--config` > `--project` > the checkout's `project:` > `FUGARO_PROJECT` > `FUGARO_CONFIG` > the only config; conflicting explicit selectors are refused; `FUGARO_CONFIG` never beats anything above it (R1). `TestSelect` has a row for each.
- **I-D:** fixed as ruled. `init` and `init --config-only` skip "no project config" but still require the checkout's name (R1). Test: `TestInitInCheckoutWithoutConfig`, and `Creating` rows in `TestSelect`.
- **I-E:** fixed as ruled. `WatchCancel` gets `onCancel` (`markCancelled`); bootstrap's and the post-loop cancel checks go through it; whichever is recorded first wins; `finalize` asserts one of the two (R8, T7). Tests: `TestCancelRecordedAtWatchTime`, `TestPostLoopCancelDoesNotOverrideHalt`, `TestBootstrapCancelThroughMarkCancelled`, `TestWatchCancelOnCancelHook`, `TestFinalizeAssertsOneOfHaltAndCancel`, `TestHaltRaceFree`.
- **I-F:** fixed as ruled. Every server tool is refused in M9a (web search included), WebSearch and WebFetch are denied in the managed settings, images are reserved at a per-model ceiling (A-N9) or refused; the property test's premise is R6's bound (R5, R6, T4, T5, T9). Tests: `TestToolTypeAllowList`, `TestImagesReservedAtCeiling`, `TestImagesRefusedWithoutCeiling`, `TestWorstCaseImagesAtCeiling`, `TestManagedSettingsDenyWebTools`.
- **I-G:** fixed as ruled. Tools by allow-list: absent `type` or `custom` only; anything else refused naming the type (R5). Test: `TestToolTypeAllowList` (a future type included).
- **I-H:** fixed as ruled. `identity` requested; `gzip`, `deflate` (standard library) and `zstd` (`klauspost/compress`, already direct) decoded; `br` and anything else is unparsed usage at the full reservation; no new module (R5, T5). Tests: `TestCompressedResponseSettledFromUsage` (three encodings), `TestUnparsed2xxChargesReservation` (`br`).
- **I-I:** fixed as ruled. No upstream status: 0 only when nothing was written upstream, else the full reservation; `Close` and a cancel before headers follow the same rule (R6). Tests: `TestNoStatusAfterBodyWrittenChargesReservation`, `TestNoStatusBeforeWriteChargesZero`, `TestClientCancelBeforeHeadersChargesReservation`, `TestCloseBeforeHeadersChargesReservation`.
- **I-J:** fixed as ruled. Step 0b pauses the Scheduler jobs before step 1 through `gcloud scheduler jobs list … --filter 'name~/jobs/fugarochk-'`, requires one per onboarded repository and prints them; each is resumed after its repository's `init --repo` (T12).
- **Minor, applied:** 1 (the settings check and the `CheckPins` check sit before the lock in both T9 and T10, and the diagram agrees); 2 (`haltGrace` is a `var`); 3 (a malformed budget env reaches the runner as `Deps.SpendErr` and fails bootstrap after the claim: one place; `TestSpendErrIsInfraErrorAfterClaim`, `TestExecPassesSpendErr`); 4 (routing on `URL.Path`, the query string forwarded; `TestQueryStringForwarded`); 5 (`cacheMult` includes `CacheRead`; `TestWorstCaseCacheReadOverrideAboveOne`); 6 (an unsplit cache write priced at the highest allowed multiplier; `TestCostUnsplitCacheCreation`); 7 (`partial` charges input and cache tokens; `TestPartialChargesInputAndCache`); 8 (`service_tier` must be `standard`, `priority` is a surprise, geos pinned in `DefaultGeos`, check 20 records the live values; `TestServiceTierPriorityIsSurprise`, `TestInferenceGeoDefaultAllowed`); 9 (saturating arithmetic and a `max_tokens` ceiling; `TestWorstCaseSaturates`); 10 (the old-env record has no `execution`); 11 (`checkoutProject(ctx, dir)`); 12 (the selftest wants an empty `/etc/claude-code`); 13 (the rollback states what the old `init` deletes, and restores from step 0's snapshot); 14 (the record's cost from `Ledger().Used` after `Close`; `TestRecordCostFromLedger`); 15 (check 20's cap is $2.00 with 4,000-token role limits); 16 (Vertex `enforce` is checked before `no_cap`; `TestVertexEnforceBeforeNoCap`); 17 (test attribution fixed; `spec.go`'s hot-file line includes T1); 18 (the `go mod tidy` step is a real check); 19 (CI vets `-tags 'live docker'`, T11); 20 (`ExecID.GCPProject`, `RepoSpec.GCPProject`, `e.gcpProject` renamed in T2); 21 (`gitops.Repo.StripEnv` keeps the model credentials out of git's env; `TestGitEnvStripsModelCredentials`).
- **Minor, rejected:** none.

## After M9a

- **M9b:** Firebase counters, daily caps, kill switches, `fugaro budget`, `init --firebase`, `--budget-mode`, fail-closed (D14), the halt reasons the schema already lists.
- **M9c–M9e:** `fugaro watch`, history and `fugaro report`, the verify gate and structured findings.
- **Later:** an owner-side token cap for `oauth`, `ls --all-projects`, renaming a project, the external gateway (D2).

## Execution

Subagent-driven is recommended, with a fresh reviewer per task. Lane A (T1–T3) and lane B's first two tasks (T4, T5) are independent and may run in two worktrees; from T6 on the tasks build on each other's interfaces. T5 (the ledger and settlement), T9 (the agent's environment), T10 (the halt path and the secret scan) and T1 (selection) are where a shipped mistake spends past a cap, hands the agent the real key, lets a repository route around the gateway, or launches in the wrong project; they deserve the closest review. Task 12 is controller-only: it changes real repositories and cloud resources and spends money, the user confirms every ⚠ step, the user alone merges the web repository's change, and the web repository gets a run only on the user's explicit go-ahead.
