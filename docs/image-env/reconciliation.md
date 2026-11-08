# An image's `ENV`: the issue and this design against the code

**Status: reconciliation for the owner — the scope and the release are answered (2026-11-05, O1 and
O2), and O3, O4 and O5 are answered (2026-11-06, in §8), each the way its decision recommended: D1376
and D1377 are what the code will do and `BATCH_` joins D1372's list in IE2. The rest of D1370–D1383
are still recommendations. Of the plan's four tasks, IE1 alone is implemented
(docs/image-env/implementation-plan.md).**
Checked against `origin/main` at **`c52d40f9`** (v0.20.2, the commit the fix round of 2026-11-06
merged into this branch; the first reading was at `57f7d871`, the commit the branch was cut from, and
every line citation into a file that changed between the two readings was re-read). The open pull
requests were read again on **2026-11-06 — twelve of them, down to #620** — by ref rather than with
`gh`, which was not authenticated in that container (§7 carries the commands). Every line below was
read in this checkout; a citation by symbol is held where a line would move.

[spec.md](spec.md) is issue **#1755** verbatim. Nothing in this file is a claim about what the issue
*asks* — that is [spec.md](spec.md)'s — and everything in it is a claim about the code.

## 1. Headline table: what the issue describes, and what the repo has

| The issue's claim | What the repo has | Class |
| --- | --- | --- |
| "The agent's environment … is built from an allowlist (`internal/agent/env.go`'s `baseEnv`: `PATH`, `HOME`, `JAVA_HOME`, locale, temp and cache variables, proxies and CA bundles, and four base-image variables)" | True, and complete: `baseEnv` is exactly those names plus `USER`, `LOGNAME`, `SHELL`, `TERM`, `TZ`, `GRADLE_USER_HOME`, `NODE_OPTIONS`, `npm_config_cache`, `PNPM_HOME`, the `XDG_*` three, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`, `NODE_EXTRA_CA_CERTS` and four base-image variables (`internal/agent/env.go:14-27`) | Confirmed |
| "Every other variable the job's image sets with `ENV` is silently dropped" | Confirmed: `BuildEnv` reads the runner's environment and copies only `baseEnv`, the auth variables, the declared secrets and `spec.Set` (`internal/agent/env.go:78-110`, `:204-220`). Nothing warns | Confirmed |
| "The image is operator-owned (the catalog lives in the published policy, D1140)" | Confirmed: the published `image_catalog` node is `config.Catalog` on the project node (`internal/projectpolicy/policy.go:345`), the local config wins over it (`internal/localcfg/localcfg.go:1422-1431`), and the deploy picks the image from it (`internal/infra/spec.go:836-854`, `:1063-1074`) | Confirmed |
| "Our build CLI also reads `NEXUS_BASEURL` (`require_var`) in its artifact-lookup helpers" | Not this repository's code — it is the reporter's own build tooling inside their image, and nothing here can confirm or refute it | Out of reach |
| "Nothing warned … it took a failed run and a transcript search to find" | Confirmed, and worse than the issue says: **no command reads a job image's environment** and `doctor`, which reads the installation, never reads a job's container env (F14) | Confirmed |
| "The only hint in the docs is a clause in `docs/base-image.md`" | Confirmed, and it is **two** sentences: `docs/base-image.md:45` and the reason it points at, `docs/design/base-image.md:167` (F2) | Confirmed |
| "Fugaro already has the right denylist … as in `schemas/fugaro.schema.json`" | Confirmed, and it is **Go code**, not only a schema: `config.ReservedEnv` (F3). The issue's list is nearly the list; it omits `GOOGLE_`, `GCE_`, `GRPC_` and `STORAGE_EMULATOR_`, and it writes `VERTEX_REGION` for `VERTEX_REGION_` | Confirmed with corrections |
| "per-repository images could follow, since SECURITY.md already assumes the repository is trusted" | True about the assumption (`SECURITY.md:6`, `:53`), and the owner declined the extension — **and** another design is removing per-repository images, on the 0.20.0 slot it has held since #1709 merged (F17) | Declined, O1 |

## 2. How the agent's environment is built, and what the image's `ENV` has to do with it

The chain, end to end, because the whole design turns on where the image's `ENV` is and is not:

1. **The image's `ENV` is in the job's environment.** A container's environment is the image's `ENV`
   with the platform's and the deployer's entries merged over it (`internal/infra/spec.go:282`'s
   `WorkflowSpec.Env` is written into the container's `env`). So every name the image sets reaches
   the **runner process** — and nothing else.
2. **The runner inherits the process environment whole**: `internal/cli/exec.go:114` (`env :=
   os.Environ()`) and `:191` (`Env: env`) are the runner's own environment, which becomes `r.d.Env`.
3. **`BuildEnv` narrows it** to `baseEnv` ∩ the runner's environment, plus the auth variables
   (`internal/agent/env.go:137-186`), plus `spec.Secrets` (the workflow's declared secret names,
   `:187-203`), then `maps.Copy(out, spec.Set)` (`:216`), then `GatewayVars` — **"Last, so nothing
   declared or inherited can override the routing"** (`:218-223`), then the compression override,
   then `PathPrepend` (`:231`).
4. **One more arm exists and is the precedent for a second one**: `DOCKER_HOST` is added only when
   `EnvSpec.Docker` is true, and the comment says why it is gated on the *run's own ask* and not on
   the variable being present (`internal/agent/env.go:99-116`). The design's passthrough is the same
   shape: gated on a fact, never on a name happening to be there.
5. **The redaction that follows** is over **values**, registered by `run.addSecret`
   (`internal/runner/runner.go:934-941`) and applied by `agent.NewRedactor` to the transcript and the
   stage's stderr (`internal/runner/runner.go:3488-3508`), by `agent.Redact` to the PR title and body
   (`internal/runner/runner.go:4544-4561`), and by `RedactFunc` to logs, reasons and reports (F11).

## 3. Findings

**F1. The allow-list is the whole mechanism, and it has no per-image arm.** `baseEnv` is a fixed
list (`internal/agent/env.go:14-27`); `BuildEnv` copies it from `parent` and never looks at anything
else the parent holds (`:79-90` — the parent map and the `baseEnv` copy loop). A name the image sets is
in `parent` and is dropped with no log line, no warning and no reason. The issue's reproduction is
exactly this.

**F2. The rule is stated in two places, both of them one sentence, neither of them where an operator
writing `ENV` lines would look.** `docs/base-image.md:45`: *"The agent's own environment is an
allowlist; none of these variables is added to it by this doc. The design's reasoning is in
[design/base-image.md](design/base-image.md) section 6."* — that sentence is about the **harness**
table above it, in a file whose §"Bring your own" (`:130`) is where an operator deciding what their
image carries reads. And `docs/design/base-image.md:167`: *"The agent's environment is an allowlist
(`internal/agent/env.go`); none of these is added to it by this design."* §6 is *"Agent harnesses"*
(heading at `:121`) — the reasoning the first sentence promises is a list of harness variables, not
a statement about the image's `ENV` at all.

**F3. The denylist the issue asks for already exists, in Go, and is stricter than the issue's
sketch.** `config.ReservedEnvPrefixes` and `config.ReservedEnvNames` (`internal/config/validate.go:62-80`)
with `func ReservedEnv(name string) bool` (`:85-88`), which is case-insensitive over both: the
prefixes are `FUGARO_`, `ANTHROPIC_`, `CLAUDE_CODE_`, `GIT_`, `LD_`, `DYLD_`, `PYTHON`, `BASH`,
`LC_`, `DOCKER_`, `BUILDKIT_`, `CLOUDSDK_`, `CLOUD_RUN_`, `VERTEX_REGION_`, `GOOGLE_`, `GCE_`,
`GRPC_`, `STORAGE_EMULATOR_`; the single names include `PATH`, `HOME`, `SHELL`, `IFS`, `ENV`,
`GODEBUG`, `GOFLAGS`, `NODE_OPTIONS`, `NODE_PATH`, `JAVA_TOOL_OPTIONS`, `_JAVA_OPTIONS`,
`JDK_JAVA_OPTIONS`, the four proxy names (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `ALL_PROXY`),
`SSL_CERT_FILE`, `SSL_CERT_DIR`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`,
`GOOGLE_APPLICATION_CREDENTIALS`, `GCE_METADATA_HOST`, `CLOUD_ML_REGION` and the
image build's own four. It is enforced at `internal/config/validate.go:303-313` on every resolve, and
mirrored in `schemas/fugaro.schema.json:473` (the pattern) and `:474` (the enum), whose description
names the two Go lists as its source.

**F4. Today the only writer of a non-reserved name into a job's environment is the image itself —
and, for a repository that builds its own image, the repository.** A repository's `fugaro.yaml`
cannot set an agent environment variable at all: `docs/design/agent-env.md` designs `agent.env:` and
its §1 says the allow-list **is** Fugaro's own Go code and never a `fugaro.yaml` field
(`docs/design/agent-env.md:305-314`), and **nothing of it is built** (its own status line, `:3-4`).
But a repository's **Dockerfile** is repository-authored text: `image:`/`dockerfile:`
(`internal/config/config.go:639-640`) make a per-repository job whose image sets whatever names the
repository wrote. That is the fact O1's scope answers, and F5 is what makes it enforceable. **One
thing the stamp does not claim:** a repository names *which* operator-owned entry it runs
(`image_name:`, `internal/config/config.go:641-655`, added in 0.17.0), so the stamp proves the image
came **from the operator's catalog**, never that the operator chose that entry for that repository —
the trust a repository already extends by writing `image_name:` today, over entries every one of
which the operator published.

**F5. The load-bearing finding: nothing at run time says where the image came from.** The runner
receives `os.Environ()` and nothing else about itself (`internal/cli/exec.go:114`, `:191`); the
deploy's knowledge of the entry (`WorkflowSpec.CatalogEntry`, `CatalogPrebuilt`,
`internal/infra/spec.go:1103-1104`) is marked `json:"-"` (`:325-326`) and **never written into the
container's environment**; the run record's `Image` is `runstore.ImageInfo{BuiltAt, BakedCommit}`
only (`internal/runstore/runstore.go:1006-1013`), read from `/etc/fugaro/image.json`
(`internal/runner/image.go:34-74`), which the derived template writes with a timestamp and a commit
and no reference (`images/derived/Dockerfile.tmpl:141-142`). `FUGARO_BASE_IMAGE` is baked by that
same template (`:22`) and read at `internal/cli/exec.go:193` — and a catalog entry built from an
arbitrary Dockerfile need not set it. `FUGARO_REMOTE` is written for a **prebuilt** catalog entry
(`internal/infra/spec.go:1130-1135`) but that is the prebuilt sub-case, not a catalog marker: **a
catalog image built from a Dockerfile is indistinguishable from a repository's own derived image.**
`CatalogImageEntry` (`internal/backend/gcp/catalog.go:141-159`) can read an entry back out of a
reference, and `JobImage` (`:171-187`) can read a deployed job's image — both are **launcher-side**
reads. Without the stamp D1371 adds, this design cannot be implemented as ruled; with it, it can.

**F6. The entry is known at deploy, which is where the stamp goes.** `repoCtx.catalogJobImage`
(`internal/infra/spec.go:836-854`) chooses the entry from the effective catalog and the workflow;
`catalogImage(e)` computes its reference (`:797-802` — the shared `catalog-<entry>:latest` for a
Dockerfile or inline entry, the entry's own reference for a prebuilt one); `entry.Name` is then
carried on the spec (`:1103-1104`). `WorkflowSpec.Env` (`:282`) is the map the deploy already writes
into the container, beside the five platform names `platformEnvFor` sets (`:776-784`) — so the stamp
is one assignment in a function that already knows the answer.

**F7. A job's image is fixed when the job is deployed, so a stamp cannot drift from it.** Cloud Run
cannot vary an image per execution, which is why the launch path reads the **deployed** job's image
and refuses to re-run the choice (`internal/backend/gcp/catalog.go:161-170`; `internal/cli/run.go`'s
`catalogDockerAsk`, `:3233-3304`, and `catalogEntryOf`, `:3321-3335`). A stamp written at deploy is
therefore a fact about the image the execution actually runs, not a guess.

**F8. The job's environment also carries every secret name of the job's workflow, not only the ones
a task declares.** `FUGARO_SECRET_ENVS` (`internal/infra/spec.go:1188`; the reader is
`internal/runner/runner.go:943-948`) holds the comma-separated names of every secret the job mounts,
and its own comment says the values "are in the runner's environment either way, where the agent can
read them". A passthrough that copied the parent would hand the agent a secret the task did not
declare — the narrowing SECURITY.md states as a property (`SECURITY.md:66`: "A job can read only the
secrets its own workflow declares"). D1373 subtracts them.

**F9. What else is in the parent, and whether the denylist catches it.** The deploy writes the five
platform names, `FUGARO_CHECKOUT`, `FUGARO_REMOTE`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID`,
`FUGARO_GITHUB_APP_ID`, `FUGARO_COMPUTE_PRICES`, the eight budget variables, `FUGARO_MODEL_PROVIDERS`
and `FUGARO_SECRET_ENVS` (`internal/infra/spec.go:1127-1188`) — all `FUGARO_` except
`CLOUD_ML_REGION`, which is a reserved single name. The launcher adds `FUGARO_RUN`,
`FUGARO_BUDGET_TOKEN`, `FUGARO_CREDENTIALS_SERVICE_URL` and `FUGARO_REVIEW_JOB`
(`internal/backend/gcp/run.go:156-171`, `internal/cli/run.go:3544`, `:3553`, `:3560`). **Cloud Run's
own** are `CLOUD_RUN_JOB`, `CLOUD_RUN_EXECUTION` and the task variables, all under the reserved
`CLOUD_RUN_` prefix. **The exception is the other backend**: Batch's task variables (`BATCH_*`, `batch`
design in `docs/backends.md`) are on **no** list, so on Batch the passthrough would hand the agent a
cluster's own variables — O4.

**F10. The reserved single names settle the worst of the probe by construction.** `PATH`, `HOME`,
`SHELL`, `IFS`, `ENV`, `CDPATH`, `PS4`, the shell names (`PYTHON`, `BASH`, `LC_`, `LD_`, `DYLD_` by
prefix), the four proxies (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `ALL_PROXY`), `SSL_CERT_FILE`,
`SSL_CERT_DIR`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE`, `NODE_OPTIONS`, `NODE_PATH`,
`JAVA_TOOL_OPTIONS`, `_JAVA_OPTIONS` and `JDK_JAVA_OPTIONS` are
`config.ReservedEnvNames` (`internal/config/validate.go:68-80`), so **an image cannot set any of them
through this path**, and the ones a run needs (`PATH`, `HOME`, the proxies, the CA bundles) keep the
values the runner already had. **`GIT_SSL_CAINFO` is the one CA path that is not settled by a name**,
and the distinction is worth stating because an earlier draft of this document got it wrong: it is in
**no** reserved-name list — neither `config.ReservedEnvNames`
(`internal/config/validate.go:68-80`) nor the launcher's `backend.ReservedEnvNames`, which is a
backend's own names plus the five `PlatformEnvNames` and nothing else
(`internal/backend/launchenv.go:31`, `:50-54`) — and what denies it is the **`GIT_` prefix**
(`internal/config/validate.go:63`), read case-insensitively by `config.ReservedEnv` (`:85-88`). It is
also on `baseEnv` (`internal/agent/env.go:22`), which is what carries the runner's own value to the
agent: an image's `GIT_SSL_CAINFO` is dropped by the prefix, and the run keeps the one it had.
What is **not** on any list is the wider toolchain-hook family — `MAVEN_OPTS`,
`GRADLE_OPTS`, `GRADLE_USER_HOME` (on `baseEnv`, so it passes as today), `SBT_OPTS`, `CARGO_*`,
`PIP_*`, `NPM_CONFIG_*`, `MIX_*`, `RUBYOPT`/`RUBYLIB`/`PERL5OPT`/`PERL5LIB` (these four single names
*are* reserved) — and those **do** execute inside `fugaro verify`: that is the trust statement, §4.

**F11. Redaction is over values, never names, and it is best-effort.** Values are registered by
`addSecret` (`internal/runner/runner.go:934-941`), gated at `minSecretLen = 4` (`:950-952`), and
applied to the transcript and stderr (`:3408-3428`), the PR title and body
(`:4329-4346`, `agent.Redact`), log lines (`internal/runner/budget_session.go:556-601`, and
`fugaro logs` on read, `internal/cli/logs.go:83`), reasons (`internal/runner/gateway.go`'s
`redactErr`, `runner.go:913-925`) and every other published surface. `internal/agent/redact.go`'s own
doc says what it cannot catch: a secret split across fields, sliced, reversed, or in an encoding not
listed (`:19-36`). **Names are printed on purpose** where a secret is skipped —
`"a mounted secret is too short to redact", "env", name` (`internal/runner/runner.go:990`) — which is
the shape D1375 and D1377 keep. An image's `ENV` value is registered with **none** of this today, so
a secret-shaped name in an operator's image is unredacted in the transcript the moment the agent
prints its environment: D1376 and O3.

**F12. The gateway's credential cannot be reached through this path, and never was in the
environment.** The agent gets `ANTHROPIC_BASE_URL` and the gateway's per-run token as
`ANTHROPIC_API_KEY` (`internal/agent/env.go:302-344`); both names are reserved, so an image cannot
set, shadow or redirect them, and `GatewayVars` is copied **last** (`:218-223`). The real key is in
the runner and is registered for redaction only (`internal/agent/env.go:148-163`; the gateway
attaches it upstream, `internal/gateway/forward.go:1163-1171`).

**F13. `doctor`'s one-screen rule is pinned, so the report cannot go on the default screen.** The
default view is capped at **25 lines** (`internal/cli/doctor_render.go:23-26`) by
`TestDoctorDefaultIsOneScreen` (`internal/cli/doctor_render_test.go:233-253`), the note that shipped
it says so (`docs/releases/v0.14.1.md:4`, re-read on 2026-11-06: *"`fugaro doctor` answers on one
screen … the audits stay behind `--all` and `--security`"*), and the owner's rule of 2026-10-31 is in
the renderer's own comment: an installation with nothing wrong prints one line and no pointer
(`internal/cli/doctor_render.go:391-396`). `--all` is the view that already prints one line per check
(`internal/cli/doctor.go:305`, `:1839-1861`), and `--json` carries the structure. A per-job list of
dropped names is a **measurement**, not a finding (nothing is wrong: the image is fine, the rule is
Fugaro's), so it belongs where measurements already print — D1377.

**F14. Nothing reads a job image's environment, and `doctor` does not read a job's either.** The two
`docker inspect … .Config.Env` reads in the tree are the history and credentials-service image smokes
(`images/smoke-history.sh:31`, `images/smoke-credentials.sh:48-57`), unrelated to jobs. The only
reader of a deployed job's whole environment is the converge, `upgrade_cloud.go:278`
(`JobFullEnv`, `internal/backend/gcp/run.go:377`); `doctor_launcher.go:83-99` lists jobs for names
and IAM and reads no container. So option 3's command needs one new read (D1377) — and that read is
`Jobs.Get`, which `doctor` does not make today (it lists jobs and reads their IAM); the launcher role already holds `run.jobs.get`.

**F15. One stale comment, noted and not taken.** `internal/backend/backend.go:131` says "No launcher
sets an entry here today", and two do: `internal/cli/run.go:3563`
(`FUGARO_CREDENTIALS_SERVICE_URL`) and `:3560` (`FUGARO_REVIEW_JOB`) set `LaunchSpec.Env`. It is a
one-line comment fix in a file this plan does not otherwise touch; the plan does not take it, and
whoever takes IE1 may fix it in the same commit if they touch that file.

**F16. The harness design's safety argument does not transfer, and that is the honest core of this
one.** `docs/design/agent-env.md` §2 and §5 rest on *"Values are never secret by construction"* and
*"Names only is safe by construction, not by redaction: every value on the allow-list is … a
non-secret literal"* (`:471-473`, `:936-950`) — because **Fugaro** chooses the names. Here the
**operator's image** chooses them, and an operator can put a token in an image (`docs/base-image.md:28`
already forbids it: "never bake one into your image either"). So this design states the trust
statement instead of inheriting one (§4) and puts the redaction question to the owner (D1376, O3).

**F17. Per-repository images are on their way out.**
`docs/repo-image-removal` (a **design**, owner's ruling 2026-11-04, its tasks R0–R2 still in open pull
requests #1693, #1694, #1698, and **none of it built** on `main`) **removes** the per-repository image
path and makes the catalog the only image path; its slot is **0.20.0**
(`docs/repo-image-removal/README.md:5`, `:9`, its plan §7 at `implementation-plan.md:169`), which is
where **#1709** moved it — **that pull request merged**, it was a docs-only re-slot, and it is not an
open claim any more. So O1's narrowing ("catalog images only") is true of today's tree and converges
with where the tree is going: after the removal, "the catalog image" is the only image there is.
Nothing here depends on the removal, and the removal does not depend on this design; if they land
together, the code is `ReservedEnv` on one path instead of two.

**F18. The reach this widens is smaller than it looks, and SECURITY.md already says why.** A run's
agent "shares the container and its user with the runner, so it can also read the runner's
environment through `/proc`" (`SECURITY.md:141`), and the same is said of the launch's seal key
(`:155`). The image's `ENV` is in the runner's environment today (F1) and is therefore already
readable by the agent through `/proc`; what the passthrough changes is (a) what a command the run
executes **inherits** by default — the issue's whole point — and (b) that the value is now in the
`claude` process's own environment, so a transcript can carry it where before it took a `/proc`
read. That is why redaction (F11, D1376) is the only new exposure this design creates.

## 4. The attacker probe

The questions the design had to answer before it could be recommended, each answered from the code
above. "The image" is an operator-owned catalog image; "the repository" is untrusted text
(`SECURITY.md:6`, `:53`).

| Probe | Answer |
| --- | --- |
| **Can a repository's agent steer which names pass?** | No. The set is computed in Go from the runner's own environment, gated on the deploy's stamp (D1371) and filtered by `config.ReservedEnv` (F3); a repository's `fugaro.yaml` writes no agent environment name at all today (F4, `docs/design/agent-env.md:305-314`). The agent can *read* the values through `/proc` (F18) but cannot add, remove or alter a name in its own environment — nothing in the loop rebuilds the environment from agent-controlled text |
| **Can a repository-built image in a project that also has catalog images influence it?** | Not through this path: such a job carries an **empty stamp** (the key is written on every workflow job, so an image's own `ENV` of that name cannot supply one), so it passes nothing (D1371). This is the scope ruling made enforceable, and F5 is why it needed a new fact rather than a check |
| **Can an image `ENV` override `PATH` or `HOME`?** | No: both are `config.ReservedEnvNames` (`internal/config/validate.go:72`), so the image's values are dropped and the runner's own stand. Nothing else writes `PATH` except `spec.PathPrepend`, which prepends to whatever `out["PATH"]` holds (`internal/agent/env.go:224-231`) — i.e. to the runner's value |
| **Proxies and CA bundles?** | No — with one name that is reserved by a **prefix** rather than by a name, and the distinction is stated because an earlier draft of this table got it wrong. The four proxy names (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `ALL_PROXY`) and `SSL_CERT_FILE`, `SSL_CERT_DIR`, `NODE_EXTRA_CA_CERTS`, `CURL_CA_BUNDLE` are `config.ReservedEnvNames` (`internal/config/validate.go:70-71`), and the CA bundles are on `baseEnv` as well (`internal/agent/env.go:22`), so a run keeps the value it already had. **`GIT_SSL_CAINFO` is not on any reserved-name list** — not `config.ReservedEnvNames` (`internal/config/validate.go:68-80`) and not the launcher's `backend.ReservedEnvNames`, which is a backend's own names plus the five `PlatformEnvNames` and nothing else (`internal/backend/launchenv.go:31`, `:50-54`); what denies it is the **`GIT_` prefix** (`internal/config/validate.go:63`) through `config.ReservedEnv` (`:85-88`). It is on `baseEnv` too (`internal/agent/env.go:22`), which is what carries the runner's own value to the agent. An image that needs a TLS-inspecting proxy for its own build sets it at build time, not in the job |
| **Toolchain hooks that execute inside `verify` — `MAVEN_OPTS`, `GRADLE_OPTS`, `PIP_*`, `NPM_CONFIG_*`, `SBT_OPTS`, `CARGO_*`?** | **They pass, and they execute.** This is the widening the owner accepted in O1 and it is stated, not hidden: an operator-owned image that sets `MAVEN_OPTS=-javaagent:/opt/x.jar` is configuring its own toolchain, and the agent's `fugaro verify test` inherits it. The trust statement: *the operator's image is trusted exactly as the operator is, the same trust `SECURITY.md` extends to an image the operator published; this design gives the repository no new way to reach that set, and the four names that would let a **repository** inject code through a language runtime (`NODE_OPTIONS`, `NODE_PATH`, `JAVA_TOOL_OPTIONS`, `_JAVA_OPTIONS`) are reserved names and cannot pass.* `PERL5LIB`/`PERL5OPT`/`RUBYOPT`/`RUBYLIB`/`PYTHON*`/`BASH_ENV` stay out for the same reason — `PYTHON` and `BASH` by prefix, the rest by name |
| **A variable that looks like a secret (`*_TOKEN`, `*_PASSWORD`, `*_KEY`)?** | It passes, because the denylist is about *Fugaro's* names and no list can tell a registry URL from a registry token. Nothing registers it for redaction today (F11), so the transcript and the PR body could carry it once the agent prints it. Two honest answers, one recommended (D1376) and one put to the owner (O3): **(recommended)** names only, values never printed by Fugaro, plus the doc sentence that a credential in an image is already forbidden and already readable by the agent through `/proc`; **(alternative)** register every passed value with the redactor, which covers the transcript and the PR body with the existing best-effort machinery and costs readability — a URL the operator wanted to see in Maven output becomes `[REDACTED]` |
| **The gateway's credential?** | Not in the environment at all, and the two names that would matter are reserved (F12) |
| **Logging of the passed names?** | Names only, never values: the report lists names (D1375, D1377), the skip-a-short-value warning keeps the shape `internal/runner/runner.go:990` already uses, and no line, reason, transcript or PR body quotes a value the passthrough carried |

## 5. Decisions (recommendations; nothing here is approved)

Each is a recommendation with its cost, in the shape `docs/project-workflow-secrets/reconciliation.md`
§4 uses. **O1 and O2 are the owner's answers of 2026-11-05**; D1382 restates O2 as the decision it
settles.

- **D1370. Scope: the passthrough exists only for a job whose image the operator's catalog chose.**
  *Recommendation (O1).* A per-repository image, `checkout: clone`'s base image and a `checkout:
  shared` job pass nothing. *Alternative:* every image (the issue's "per-repository images could
  follow") — declined, O1, and the removal in F17 makes it moot. *Cost:* two paths to keep distinct,
  and a repository that genuinely needs a toolchain variable must be moved to a catalog entry — which
  is where F17 is going anyway.
- **D1371. The discriminator is a stamp the deploy writes into the job's environment: the catalog
  entry's name, under a new `FUGARO_`-prefixed variable.** *Recommendation.* `internal/infra` writes
  it where `entry.Name` is already in hand (`internal/infra/spec.go:1103-1104`, F6); the runner
  passes image variables **only when it is present**, so a job deployed by an older `fugaro update`
  passes nothing (fail closed, and it converges when the operator next runs **`fugaro update
  --repo .`** — the repository stage, which a plain `fugaro update` does not run and which is already
  today's door for a changed job, so nothing new is asked of them).
  *Alternative:* read the image's own config at run time — refused: it needs a registry call and a
  credential inside the job, and it cannot answer for a prebuilt entry (F5). *Cost:* a job whose
  image is a catalog entry built **before** this change passes nothing until it is redeployed; the
  report (D1377) is what makes that visible. **A reserved name is reused for no part of this**: the
  stamp is a new name, and no existing wire field changes meaning.
- **D1372. The filter is `config.ReservedEnv`, case-insensitively, plus the name-shape rule
  `^[A-Z_][A-Z0-9_]*$` (`internal/config/validate.go:32`, `:303-313`).** *Recommendation.* One list,
  already reviewed, already mirrored in `schemas/fugaro.schema.json:473`; a name the shape rule
  refuses is dropped rather than made into an environment entry a shell cannot express. *Alternative:*
  a new passthrough-specific list — refused: two lists drift, and the second one is never reviewed as
  carefully as the first. **Implemented 2026-11-06, both halves:** IE1's shared subtraction applies
  the list and the shape (`config.EnvNameRE`, the pattern at `:32` exported for it at the end of
  `validate.go`, the file's own convention, so no citation into it moves), so IE2 adds no rule of its
  own.
- **D1373. Beyond the denylist, the passthrough subtracts every name Fugaro writes: the five platform
  names and the launch-reserved names (`internal/backend/launchenv.go:32-54`), every `FUGARO_*` (a
  consequence of D1372, stated because the stamp depends on it) and every name in
  `FUGARO_SECRET_ENVS` that this run's own workflow did not declare (F8).** *Recommendation.* A
  declared secret still reaches the agent through the existing path only
  (`internal/runner/runner.go:2314-2316`). *Alternative:* let the workflow's own declarations decide
  (i.e. pass an undeclared secret name only if the image also set it) — refused: at run time nothing
  can tell the image's value from the mounted one, and the mounted one wins in the merged
  environment. *Cost:* an operator who needs a secret-shaped value must use `workflows.*.secrets`,
  which is the rule `docs/design/agent-env.md` §2 already states.
- **D1374. Order: the image's values are written before the auth variables, the declared secrets,
  `spec.Set` and `GatewayVars`.** *Recommendation.* O1's ruling, and it is already the code's shape —
  `maps.Copy(out, spec.Set)` at `internal/agent/env.go:216` and `GatewayVars` "last, so nothing
  declared or inherited can override the routing" (`:218-223`). *Cost:* none; the constant is that
  the new write happens at one place, and a test holds it (IE1's mutation).
- **D1375. Names are reported and logged; values are not.** *Recommendation.* No log line, reason,
  transcript field or report carries a value the passthrough carried. *Cost:* an operator debugging
  a passthrough sees a name and must look at their own image for the value.
- **D1376. Values the passthrough carries are not registered with the redactor by default.**
  *Recommendation (put to the owner as O3; **answered 2026-11-06: this one**, so no value the
  passthrough carries is registered and IE2 adds no `addSecret` call).* The reasons are F11, F16 and F18: redaction is
  best-effort, the agent can already read the runner's environment through `/proc`, and the doc
  already forbids a credential in an image (`docs/base-image.md:28`). *Alternative:* register every
  passed value as a redaction secret (`internal/runner/runner.go:934-941`, ≥4 bytes), which covers
  the transcript, the PR body and the logs — *cost:* configuration values disappear from transcripts
  wherever they appear, which is a product change the owner should own.
- **D1377. The report is `fugaro doctor --all` (and `--json`), not the default screen.** 
  *Recommendation (put to the owner as O5; **answered 2026-11-06: this one**, so the report is
  IE3's task on that surface).* Doctor is the one command that reads the installation
  and already lists jobs and reads their IAM (`internal/cli/doctor_launcher.go:83-99`); the names it would
  print need one more read, a `Jobs.Get` per job, `JobFullEnv` (`internal/backend/gcp/run.go:377`), which the converge
  already uses (`upgrade_cloud.go:278`). It prints, per job whose image is catalog-owned, the names
  that will not reach runs — **names only**, in the shape `--all` already uses. *Alternatives:*
  (a) the default screen — refused: 25 lines are pinned by `TestDoctorDefaultIsOneScreen` and an
  installation with nothing wrong prints one line (`internal/cli/doctor_render.go:23-26`, `:391-396`);
  (b) a new `fugaro image env` command — refused as the *smallest* answer: a new command surface, its
  own `--json` shape and its own docs for a list `--all` can carry; (c) `fugaro image build` — refused:
  it builds the image in Cloud Build and reads the built config nowhere (F14), so it would report the
  `ENV` lines of a **local** build only; (d) `fugaro update --repo` — refused: the deploy knows the
  entry but not the image's own environment (F5), so it could only guess from the Dockerfile it does
  not read. *Cost:* an operator must run `doctor --all` (or read the doc sentence) to see it.
- **D1378. The report's subtraction is the passthrough's own.** *Recommendation.* The names printed
  are computed by the same function IE1 uses, so the report and the behaviour can never disagree;
  a name the report calls dropped is a name the passthrough dropped. *Cost:* the report needs the
  same stamp, so a job deployed before D1371 is reported as "not catalog-stamped" rather than guessed
  at.
- **D1379. `docs/base-image.md` states the rule next to "Bring your own", and
  `docs/design/base-image.md` §6's allowlist sentence points at it.** *Recommendation.* The issue's
  own ask 3, and the two sentences F2 found are the whole of today's documentation. *Cost:* two
  pinned docs tests to move in the same commit (`go test ./internal/cli/ -run Docs`), which is the
  normal cost of a doc sentence here.
- **D1380. One clause in `plugin/skills/working/SKILL.md`'s catalog bullet.** *Recommendation.* The
  skill tells a user's own agent how to drive the CLI; the catalog bullet is where it says what a
  catalog entry is. The clause names the rule and `fugaro doctor --all`, both of which exist in the
  CLI, so `plugin/`'s `skillcli_test.go` pin holds. *Cost:* the plugin tests run in the same commit.
- **D1381. The live check is Check 291, owner-run.** *Recommendation.* It is the number free above
  the register's highest written section (290); the register's row and section are written by the
  task that implements the check, under the number free at that merge
  (docs/cloud-senior-review/README.md's convention). *Cost:* the owner's own time, on the sandbox,
  and the check is the gate the release waits on.
- **D1382. The release is 0.20.0.** *Recommendation (O2, the owner's answer).* The design note and
  option 3 land in 0.20.0; the code only if this design's review is clean in time. *Cost:* 0.20.0
  is also where `docs/repo-image-removal` is slotted (F17) and where the cloud senior review's
  and workspaces designs point — this change is small and independent of all of them, and its
  `### For operators` entry names no new command: the stamp arrives with the repository's own
  `fugaro update --repo .`, which a plain `fugaro update` does not run, if it ships at all. **The answer stands as the answer of 2026-11-05, and `main` has moved past it:**
  0.20.0 is tagged (0.20.2 at `c52d40f9`, this fix round's base) and neither this work nor the
  removal shipped in it, so the slot the answer picked is a number that has passed — which release
  carries the code is the owner's to say, and no task in this plan waits on the number.
- **D1383. If the passthrough slips, option 3 ships anyway.** *Recommendation.* The doc sentence
  (D1379), the skill clause (D1380) and the report (D1377) are useful with no passthrough at all —
  they turn today's silent drop into a stated rule and a printable list — so the release is never
  worse than the tree it replaces, and the passthrough is the only task that can fall out of it.
  *Cost:* none.

**Reserved and unused: D1384–D1389.**

## 6. Considered and declined, and why

- **Option 2 — a plain, non-secret `env:` map on a workflow (`fugaro.yaml`) and on a project-layer
  profile.** *Declined by the owner (O1).* Two reasons beyond the ruling, both from the tree: the
  mechanism already exists as a **design** for `agent.env:` (`docs/design/agent-env.md`, nothing
  built, `:3-4`), whose §2 rule is that the allow-list is Fugaro's Go code and never a config field
  (`:305-314`) — a config map would be a second, weaker answer to the same need, and it would need
  the same review that design asks for; and a catalog image needs no new key at all, because the
  image **is** the configuration file the operator already writes. If the owner later wants a value
  without a rebuild, that design is the place to rule it, not this one.
- **Option 1 extended to per-repository images.** *Declined by the owner (O1).* The trust argument
  in the issue is real but it stops at the wrong place: SECURITY.md trusts the repository **as an
  agent-driver**, not as the author of the job's environment, and a repository-authored Dockerfile
  is repository text that decides what the run executes — the passthrough would
  turn a build file into a channel for the toolchain hooks in §4 with no operator in the loop. F17
  is the second answer: that path is being removed.
- **Reading the image's config at run time** (registry `Config.Env`), as the honest alternative to
  the stamp. *Declined.* It needs a registry credential in the job, it makes the runner's
  environment depend on a network call at bootstrap, and it cannot answer for a prebuilt entry at
  all (F5).
- **A new `fugaro image env` command.** *Declined* as the smallest answer — D1377's alternative (b).
- **Registering every passed value with the redactor as a default.** *Not declined — deferred to the
  owner* (D1376, O3), because it changes what every transcript shows in a way only the owner can
  weigh.
- **Adding `BATCH_` to `config.ReservedEnvPrefixes`.** *Answered 2026-11-06 (O4): yes, with D1372's
  filter and in IE2*, because on the Batch backend the task variables are the job's environment and
  no list covers them today (F9): without it, the passthrough hands a run the cluster's own variables
  on one backend and none on the other. The cost is a new refusal a repository's workflow-secret
  `env` sees — a tightening, so it is legal under the layering rule, but it is a change to an
  existing refusal and a decision of its own, which is why it was put to the owner rather than taken
  here. **It is IE2's, and it is three edits** — `internal/config/validate.go`'s prefix list,
  `schemas/fugaro.schema.json`'s `env` pattern (`:473`, the mirror `TestFugaroSchemaReservesTheSameEnv`
  holds to the Go list, so a Go-side-only change reddens `main`) and `docs/design/v1.md`'s enumeration
  (`:616`) — not IE1's.
- **The one-line stale comment in `internal/backend/backend.go:131`.** *Noted as F15, not taken:*
  it is not this design's file and a drive-by edit to a comment is exactly the kind of change this
  repository keeps out of a design PR.

## 7. The number block, and how it was read

Decisions, **re-read on 2026-11-06** (the fix round that moved this block) against `origin/main` at
**`c52d40f9`** (v0.20.2) and against every open pull request's own head, fetched by ref — `gh` was not
authenticated in that container, so the reading is `git ls-remote origin 'refs/pull/*/head'`, the API's
open list and `git grep` over each fetched head:

```sh
git grep -ohE '\bD[0-9]{3,4}\b' origin/main | sed 's/D//' | sort -n -u | tail   # main's highest in use
git ls-remote origin 'refs/pull/*/head'                                        # every PR head, merged or not
git fetch origin pull/<n>/head:pr/<n> && git grep -ohE '\bD1[0-9]{3}\b' pr/<n> # every open PR's numbers
```

What the reading found, and why this block is where it is:

- **`main`'s highest written decision is `docs/workspaces`' D1305** (`docs/workspaces/README.md:141-142`:
  its block is **D1290–D1309**, sixteen written and four reserved). The **D1310** that
  `docs/mcp-server/README.md:111`, `docs/workspaces/reconciliation.md:665` and
  `docs/periodic-reviews/README.md:125` name as the next free number is **still free on `main`** — no
  decision is written there.
- **The overlap this round fixes was not with `main`: it is with a claim.**
  `docs/stateless-orchestrator/reconciliation.md:24` records that "`D1320-D1349`, the block the brief
  named, is therefore free" — the block that design **considered and did not take** (it takes
  D1220–D1249, `docs/stateless-orchestrator/README.md:55`). A block another document has named, even in
  the sentence that declines it, is not a block to write in when a free one exists above it, so this
  design moves rather than arguing the point.
- **The highest block claimed anywhere, on `main` or in an open pull request, is open PR #1780's
  D1350–D1369** (`docs/cli-output-console-graph/README.md:22-25`: "D1350-D1362 written and answered,
  **D1363-D1369 reserved and unused**"), and that pull request says so itself: *"open #1772
  (`docs/image-env`) takes D1310-D1329, which overlaps `docs/stateless-orchestrator`'s D1320-D1349. That
  overlap is not this work's to fix; it is why this block starts above D1349."* (**Quoted as that
  reading found it**: the D1310–D1329 it names is this design's old block, the one this round moved to
  D1370–D1389; nothing in this folder reads that string as a live number.) It took the first block
  above the claimed ceiling, and this round moves this design's own block to the first one above
  **that**: the block below is now written by another work, so the next free block is **D1370**.
- **Nothing else claims D1370 or above**: a `git grep` for `D13[7-9][0-9]` and `D14[0-9]{2}` over
  `origin/main` and over every fetched open pull request head returns nothing.

**This work therefore takes D1370–D1389**, writes **D1370–D1383** and reserves **D1384–D1389** unused.
**Checks:** `main`'s register still has **290** as its highest **written** `## Check` section
(`docs/gcp-live-checklist.md`'s `## Check 290`, the mixed-credentials run), so **291** is free and this
work claims it (D1381) — the checks
claimed above it in open work start at **300** (`docs/stateless-orchestrator/implementation-plan.md:155`,
Checks 300–305 with 306–309 reserved, and #1780's 311–314), and the register's rule is that whichever of
two claims merges second renumbers itself. The earlier reading said "no open pull request claims 291 or
above"; that is no longer true of the neighbours, and the claim is unchanged because no open work writes
291.

## 8. Open items — what is the owner's

- **O1. The scope — ANSWERED 2026-11-05.** Option 3 plus option 1 for operator-owned catalog images
  only; per-repository images and a plain `env:` map stay out, recorded with their reasons in §6.
- **O2. The release — ANSWERED 2026-11-05.** **0.20.0**, the design note and option 3 in it, the code
  only if this design's review is clean in time (D1382).
- **O3. May this work register every value it passes with the redactor? — ANSWERED 2026-11-06: no.**
  **Names only, no value this work passes is registered with the redactor** (D1376 as recommended): the
  trust statement in §4 and the existing "never bake a credential into your image" rule
  (`docs/base-image.md:28`) carry it, and nothing in IE2 calls `addSecret` for a passed value. The
  answer is the recommendation's, so IE2's body is unchanged by it; the alternative is declined and
  its cost (`NEXUS_BASEURL` disappearing from Maven output and from every transcript) is not paid.
- **O4. Should `BATCH_` join `config.ReservedEnvPrefixes`? — ANSWERED 2026-11-06: yes.**
  **`BATCH_` joins the prefix list**, in the same change as D1372's filter: on the Batch backend the
  task variables *are* the job's environment and no list covers them today (F9), so without it the
  passthrough hands a run the cluster's own variables on one backend and none on the other. The cost
  accepted is the new refusal a repository's workflow-secret `env` sees — a tightening, legal under
  the layering rule. **The edits are IE2's** and they are three, not one: the file O4 names
  (`internal/config/validate.go`), the schema pattern that mirrors it (`schemas/fugaro.schema.json:473`,
  held to the Go list by `TestFugaroSchemaReservesTheSameEnv`, `schemas/schemas_test.go:1543`) and the
  doc enumeration of both (`docs/design/v1.md:616`). None is in IE1's diff; the subtraction IE1 spells
  reads the list, so it follows the change with no edit of its own.
- **O5. Is `fugaro doctor --all` the right surface for the report? — ANSWERED 2026-11-06: yes.**
  **`fugaro doctor --all` (and `--json`) is the report** (D1377 as recommended): the smallest surface,
  the pinned one-screen default untouched, and one read the converge already makes. The default
  screen, a new `fugaro image env` command and a note from `fugaro update --repo` are declined with
  the costs D1377 records. The report is IE3's task.
- **O6. The live check is yours to run — not a question.** **Check 291**
  (implementation-plan.md §7): a catalog entry on the sandbox whose Dockerfile sets a marker
  variable, a run on it, and the value observed inside the run and its name in the report. It is
  owner-run by the rule every live check in this repository follows, and it is the gate the release
  waits on.
