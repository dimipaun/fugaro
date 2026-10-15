# A Docker-capable compute backend: workloads on Compute Engine VMs

Status: design for review (2026-10-15), written to the owner's ruling of
2026-10-14 (restored in the generic-tool plan, PR #374): the Docker-capable
execution backend, generic-tool section 1, was "gen-2 first, a VM backend as
the fallback" ([generic-tool.md](generic-tool.md) §1,
[plans/2026-10-08-generic-tool.md](../plans/2026-10-08-generic-tool.md)
Task 28). Cloud Run gen-2 cannot run Docker — the owner measured it — so
gen-2 failed its own first tier and **the VM backend is the route**. Check 31
(the gen-2 Docker probe, Task 26) and Task 27 (Docker on gen-2) are dropped;
Task 28 is restored and is no longer conditional on Check 31. Nothing here is
built: this document decides the substrate, the execution model and the
security envelope, and leaves the implementation plan to be written after it
is approved.

Scope: **docs only.** No code, no Terraform, no `fugaro.yaml`, no live check
run. Every factual claim about Google's services is cited to a public page
with the date it was read (2026-10-15 unless stated); anything not verified
is written **UNVERIFIED** with the check that would settle it.

Related: [generic-tool.md](generic-tool.md) §1 (the original tiers, the
startup risk, the `fugaro-services` mitigation), [base-image.md](base-image.md)
(the one base image, its Docker CLI, its size), [v1.md](v1.md) §3.4 and §14
(the backend seam), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md)
§4–§5 (the gateway and the caps), [project-centric.md](project-centric.md)
§4–§5 (shared jobs, profiles, policy as data),
[bucket-iam.md](bucket-iam.md) (the runs bucket), and
[registry-remote-repository.md](registry-remote-repository.md) (the base
image served from an in-region Artifact Registry remote repository, which
the startup mitigation below depends on).

## 0. Summary and recommendation

Fugaro already runs every job as a container, and the runner (`fugaro exec`)
is backend-agnostic: the gateway, the budget session, the cache and
`finalize` are all in-process or go through the runs bucket, never through
`internal/backend/gcp`. The only thing Cloud Run cannot give a run is a
Docker daemon. The whole design is therefore "run the same image and the same
runner somewhere a daemon exists", plus the consequences of that daemon being
root on its host.

Recommendation, in one paragraph: **Cloud Batch, one job with one task per
run**, on a Container-Optimized OS VM, running the unchanged `fugaro-base`
image as a Batch container runnable, with the VM's own Docker daemon made
available to the run through the bind-mounted socket. **One VM per run, one
run per VM, never reused**: because a run with Docker is root-equivalent on
its VM, the VM is the isolation unit, and that unit is created and destroyed
for exactly one run. Docker comes from the host daemon (the socket), not from
a rootless daemon inside the container, because the base image ships only the
CLI and because the security argument does not change either way — the agent
is root on the VM in both cases, so the boundary people rely on must be the
VM boundary, not the container one. Startup is bounded by a preloaded boot
image (the base image baked into the VM's Docker cache or served by image
streaming) rather than a 3.8 GB pull on every run, with a cold-start budget
of the same 120 s the original design allowed the VM tier (generic-tool §1,
G2). The cost is an installation-level, one-time setup (`fugaro init` gains a
Batch stage) and nothing per repository — so it composes with the
project-centric goal of "no heavy per-repository setup" rather than fighting
it.

The decisions are C1 to C11 in §1.10 and C12 to C20 inline in §2 to §5; §8.2
collects every one the owner should weigh, each with its recommendation and
the trade-off of the alternative.

## 1. Choice of substrate

Three candidates, compared below: **Cloud Batch** (managed Compute Engine VMs
with container runnables), **plain Compute Engine instances** created and
deleted per run, and a **managed instance group (MIG)**.

### 1.1 Docker availability to the agent

Every candidate ends in the same place — a real Linux kernel where `dockerd`
runs normally — so the question is not *whether* Docker works but *how the
container gets at it*, and what that costs in isolation.

Two ways to give the run a daemon:

- **The host daemon through the socket.** The VM runs the OS's own Docker
  (Container-Optimized OS ships Docker and containerd preinstalled, "no
  on-host setup required", cloud.google.com/container-optimized-os/docs/
  concepts/features-and-benefits, checked 2026-10-15). The runner container is
  started with `/var/run/docker.sock` bind-mounted from the host; the agent's
  `docker` CLI talks to the host daemon. Batch supports exactly this: a
  container runnable's `volumes` field takes `--volume`-formatted bind mounts
  from the host, e.g. `/var/run/docker.sock:/var/run/docker.sock`
  (cloud.google.com/batch/docs/reference/rest/v1/projects.locations.jobs,
  Container, checked 2026-10-15).
- **A daemon inside the container (Docker-in-Docker).** Either a privileged
  `dockerd` inside the runner container, or rootless Docker/Podman. Rootless
  needs user namespaces and `newuidmap`/`newgidmap`; whether a Batch container
  runnable can be privileged or can set those up is UNVERIFIED (the `options`
  field documents "arbitrary additional options to include in the `docker
  run` command", e.g. `--network host`, so `--privileged` and `--group-add`
  are *plausible* but not documented; the check that settles it is Check 57
  step 6). Docker-in-Docker also doubles the storage (two Docker stores) on a
  3.8 GB base.

The decisive point is that **neither option changes the isolation story**:
with the socket, the agent can `docker run -v /:/host --privileged` and be
root on the VM; with privileged DinD, it is root in a container that is root
on the VM. A run that needs Docker is root-equivalent on its host whichever
way the daemon is reached. So the recommendation is the *simpler* one — the
host socket — and the security budget is spent on the **VM being the
isolation unit**, not on the container being a boundary. This is stated
plainly in §3.

Consequences to carry: the base image ships the Docker CLI and no daemon
(base-image.md §9), so nothing in the image changes; the agent's environment
gains `DOCKER_HOST` (or relies on the default `unix:///var/run/docker.sock`),
which is the first addition to the env allowlist since M9 and must be reviewed
as such (generic-tool §1 already flags this); and the container user must be
able to open the socket, which is `root:docker` mode `0660` on COS. Whether
Batch can pass `--group-add` (or `--user root`) in a container runnable's
`options` is UNVERIFIED — Check 57 step 6 records the FACT. The fallbacks, in
order, are: run the container as root (`runAsNonRoot` unset; Batch's default,
per the REST reference), a systemd drop-in on the boot image that relaxes the
socket's group, or a tiny socket-proxy sidecar.

**Substrate verdict on this axis.** Cloud Batch and a plain instance are
equivalent: both give the host daemon and the socket mount. A MIG is
equivalent too, but only by running a container on each instance, i.e. by
re-implementing Batch.

### 1.2 Startup latency

Cold start is VM boot plus the image. The 3.8 GB `fugaro-base` (base-image.md
§13: 3812 MB measured on amd64) is the dominant term if it is pulled on every
run, and it is pulled over the network from wherever the registry lives.

What the numbers are:

- **VM boot**: a Compute Engine VM starts in tens of seconds; Batch adds
  scheduling and agent setup before the first runnable starts. Exact figures
  are UNVERIFIED — Check 57 measures launch-to-first-log-line directly.
- **The image pull**: a 3.8 GB pull over the VM's network is the thing to
  avoid repeating. Three mitigations, in order of how much they help:
  1. **A preloaded boot image.** Build a custom Compute Engine image once per
     `fugaro-base` version whose Docker data root already contains the base
     image (`docker pull` into `/var/lib/docker`, snapshot the disk), and
     point Batch's `InstancePolicyOrTemplate` at it as the boot disk. The
     container runnable then starts with a local image and no pull. This is
     the strongest mitigation; it is also the heaviest operationally (one
     image build and one stored image per base version, per project).
  2. **Image streaming.** Batch container runnables support it: "the runnable
     can initialize without waiting for the entire container image to
     download, which can significantly reduce startup time for large
     container images" (cloud.google.com/batch/docs/use-image-streaming,
     checked 2026-10-15). It requires the image to live in **Artifact
     Registry** and the VMs to be in the same location as the image — which
     the registry work provides, since `fugaro-base` is served from an
     in-region Artifact Registry remote repository
     ([registry-remote-repository.md](registry-remote-repository.md)). The
     catch: it switches the container runtime to **containerd, not Docker**,
     and accepts only `imageUri`, `commands[]`, `entrypoint` and `volumes[]`
     (same page). It therefore cannot itself give the agent the host daemon;
     it only speeds the *runner* image. That is compatible with the socket
     design: the runner image is pulled by containerd, the host Docker daemon
     is what the agent uses.
  3. **The regional registry.** Serving the base from an in-region Artifact
     Registry (the registry-remote-repository work) removes the long haul from
     ghcr.io even when a plain pull happens. This is the cheapest mitigation
     and a prerequisite for 2.
- **Acceptable budget for an interactive agent job.** The original design set
  it: a VM backend is worth building if its median launch-to-first-line is at
  most **120 s above today's Cloud Run baseline** (generic-tool §1, G2). The
  0.7.0 baseline is small — Check 41 measured a 10.5 s median cold start on
  `fugaro-base` — so the VM tier's budget is roughly "under about two
  minutes to the runner's first line". An agent run is interactive-ish but
  long (minutes to an hour), so two minutes of setup against a Docker daemon
  is acceptable; five is not. Check 57 measures it with and without a
  preloaded image so the owner can decide before any code depends on it.

**Substrate verdict.** Batch's container runnables and image streaming are
ready-made; a plain instance needs a startup script and either a baked image
(the same preload work) or a manual `docker pull`. A MIG has the same
per-instance problem and no per-run lifecycle.

### 1.3 Cost

Instances are billed differently from Cloud Run, and the difference matters
for estimates:

- **Per-second after a one-minute minimum.** "All vCPUs, GPUs, and GB of
  memory are charged a minimum of 1 minute. ... After 1 minute, your instance
  is billed on a per-second basis" (cloud.google.com/compute/pricing, checked
  2026-10-15). A 40-second run therefore costs a full minute; Cloud Run's own
  jobs billing has no such floor. `cost.compute_usd` must model the floor
  (§5).
- **Spot / preemptible.** Spot VMs are up to 91% off but Compute Engine "can
  preempt Spot VMs at any time to reclaim the resources"; on preemption it
  either stops (the default) or deletes the VM, after a preemption notice
  whose default is 30 seconds and which can be extended to 120
  (cloud.google.com/compute/docs/instances/spot, checked 2026-10-15, and
  cloud.google.com/compute/pricing). For a Fugaro run a preemption is a hard
  kill mid-stage: the model spend for that stage is gone, the run does not
  reach `finalize`, and recovery is a follow-up run. Recommendation: **spot
  off by default**, opt-in per profile for workloads that tolerate it (a
  nightly batch, a test-only workflow); the runner's SIGTERM path
  (`cmd/fugaro/main.go` cancels the context so the task still finalizes) has
  30 s to checkpoint on a spot preemption, which the 30 s default notice makes
  marginal — Check 57 step 7 records what actually happens.
- **Batch itself.** Batch jobs "run on Compute Engine resources, so Batch is
  subject to Compute Engine quotas and limits"
  (cloud.google.com/batch/quotas, checked 2026-10-15); no separate Batch
  charge is documented. Whether the Batch service agent adds any billable
  line beyond the VMs is UNVERIFIED; Check 57 step 5 reads the billing
  export for one run.
- **Extras.** A boot disk (per GB-month, prorated), a static or in-use
  external IP if used, Cloud NAT if used, and the preloaded boot image's
  storage. All are small per run but nonzero; §1.7 decides NAT vs external IP.
- **The machine type.** Batch container jobs use a machine type with enough
  vCPUs/memory for the task's `computeResource`; a Fugaro run's needs come
  from the workflow's `resources` (Cloud Run-shaped today, §4). An
  `e2-standard-4`-class VM (4 vCPU / 16 GiB) is the likely default. The exact
  list price is UNVERIFIED here and belongs in the backend's own price table,
  not in this document.

**Substrate verdict.** Identical: all three bill Compute Engine VMs. Batch
does not add a charge.

### 1.4 Cancel and timeouts

- **Batch** gives a job `maxRunDuration` per task and `timeout` per runnable
  (cloud.google.com/batch/docs/reference/rest/v1/projects.locations.jobs,
  checked 2026-10-15), and cancels by deleting the job (`jobs.delete`); the
  job leaves `RUNNING` for `DELETION_IN_PROGRESS`. That the VMs are torn down
  at that point is what Batch is documented to do and what the reaper assumes;
  it is not spelled out on the pages read here, so Check 57 step 8 verifies it
  rather than this document asserting it.
  Batch's own ceiling is generous: a job may wait 2 days in `QUEUED` and run
  14 days in `RUNNING` before automatic failure (cloud.google.com/batch/
  quotas, checked 2026-10-15), so Fugaro's own timeouts (a workflow's
  `timeouts.total` plus `backend.TaskTimeoutSlack`) bind long before Google's.
- **A plain instance** cancels by `instances.delete`; the timeout is ours to
  enforce (a watchdog or the startup script's `timeout`), which is another
  small piece of machinery Batch already has.
- **Cancel semantics** must survive the race Cloud Run already has: a run can
  finish between the read and the cancel (live check 5), and deleting an
  already-finished Batch job must be tolerated as a terminal state, not an
  error.

**Substrate verdict.** Batch wins: timeouts and cancel are declarative.

### 1.5 Logs

The runner writes structured lines to stdout/stderr; on Cloud Run they land in
Cloud Logging and `fugaro logs` reads them through the installation's log
view (internal/backend/gcp/logs.go filters on
`labels."run.googleapis.com/execution_name"`). Two things must hold on a VM:

1. **The lines reach Cloud Logging.** Batch writes task logs to Cloud
   Logging when a job's `logsPolicy.destination` is `CLOUD_LOGGING`; when a
   job is created through the API it "is disabled by default" and must be set
   explicitly (cloud.google.com/batch/docs/analyze-job-using-logs, checked
   2026-10-15). Batch emits them under the monitored resource
   `batch.googleapis.com/Job` with labels `resource_container`, `location`
   and `job_id` (cloud.google.com/logging/docs/api/v2/resource-list, checked
   2026-10-15), and "custom labels apply to the job and any Cloud Logging
   LogEntry that it generates" (the Batch REST reference, checked 2026-10-15),
   so the Fugaro labels travel with the lines.
2. **They land in the installation's bucket, not `_Default`.** The
   installation's log sink filters on
   `resource.type="cloud_run_job" AND labels."fugaro"="managed"`
   (`deploy/terraform/gcp/modules/installation/logging.tf`); a Batch line's
   `resource.type` is `batch.googleapis.com/Job`, so the filter must gain a
   second arm (`resource.type="batch.googleapis.com/Job" AND
   labels."fugaro"="managed"`), and the matching exclusion from `_Default`
   too. Without it, `fugaro logs` (which reads the Fugaro view) finds
   nothing and the lines sit in `_Default` where every project viewer can
   read them — a worse outcome than the Cloud Run path. This is an
   installation-level Terraform change (§8).

A plain instance has no such integration: a startup script would have to ship
container output to Cloud Logging itself (the Ops Agent, or an explicit
`gcloud logging write`), which is exactly the machinery Batch supplies.

**Substrate verdict.** Batch wins.

### 1.6 Secrets

Cloud Run mounts a secret as an env var from a `secret_key_ref` in the job
definition (modules/workflow/job.tf). **Batch has no equivalent** for arbitrary
secrets: its container runnable's `username`/`password` fields take Secret
Manager references, but only for private-registry auth (Batch REST reference,
checked 2026-10-15). So on a VM the runner must obtain the workflow's secrets
itself, from the VM's service account:

- The VM (Batch-created) runs as a service account; omit the field and it is
  the default Compute Engine service account, set it and it is the one named
  (Batch REST reference, `AllocationPolicy.serviceAccount`, checked
  2026-10-15). Recommendation: **one service account per run class**, reusing
  the project-centric model — the job service account of
  (`profile`, `account`) (project-centric §4.1) — never the default Compute
  Engine service account, which is project-wide and broad.
- The runner (or a thin entrypoint shim in the base image) reads the named
  secrets from Secret Manager at start, as that service account, through the
  metadata server, and exports them before `fugaro exec` runs. Today the
  platform does this mount; on a VM the runner does. This is new code (a
  bootstrap step), and it is the single largest piece the VM backend adds that
  Cloud Run does not need.

**Substrate verdict.** Identical: both use the VM's service account. Batch at
least names it declaratively; a plain instance does too (the instance's
service account).

### 1.7 Networking

Requirements: **egress** to Cloud Logging, Secret Manager, the runs bucket,
Artifact Registry/ghcr.io, the git provider, the model providers (Anthropic,
Vertex, an OpenRouter-style endpoint) and package registries; **no inbound**.

- **External IP.** An ephemeral external IP costs nothing while attached (a
  static one does) and lets the VM reach the internet directly. It also puts
  the VM on the public internet, but with no ingress firewall rule (the
  default VPC's `default-allow-ssh` is the thing to remove or avoid) nothing
  can reach it. Simple, but "no inbound" then rests on firewall configuration
  rather than on the absence of a route.
- **Cloud NAT.** No external IP on the VM; egress goes through a Cloud NAT
  gateway on its subnet (cloud.google.com/nat/docs/overview, checked
  2026-10-15). Nothing is reachable from outside at the network layer; the
  cost is a NAT gateway (hourly + per-GB processed) that exists for the
  installation, not per run, plus a router and subnet.
- **Batch's own fields.** `NetworkPolicy` and `InstancePolicyOrTemplate`
  accept a network interface and, with an instance template, the network
  settings can be pinned in the template (Batch REST reference, checked
  2026-10-15).

Recommendation: **no external IP, Cloud NAT for egress**, because it makes
"no inbound" a property of the network rather than of a firewall rule list,
and because the NAT is an installation-level one-time setup, matching the
"nothing per repository" goal. Trade-off: it is more setup than an ephemeral
IP and adds a per-GB egress charge; a single-person installation that prefers
simplicity can take the external IP and a deny-all ingress firewall instead
(**C7**, §8.2).

**Substrate verdict.** Identical.

### 1.8 Quotas and limits

Verified from Google's public documentation (checked 2026-10-15):

- **Batch limits** (cloud.google.com/batch/quotas), which "cannot be
  changed": 100,000 tasks per task group; 5,000 parallel tasks per job;
  2,000 concurrent VMs per single-zone job (4,000 multi-zone); 1 MB job
  definition; 2 days `QUEUED` before automatic failure; 14 days `RUNNING`
  before automatic failure (7 days under `FLEX_START`).
- **Compute Engine allocation quotas** (cloud.google.com/compute/docs/
  resource-quotas): VM quotas are **regional**; the **VM instances** quota is
  regional and is automatically set to **ten times your regular CPU quota**;
  **CPU quota** is regional (with per-family pools such as `CPUS`/`E2`), and
  there is additionally a global **CPUs (All Regions)** quota; in-use and
  static IP addresses consume **global** quotas; disks are regional. The
  *default numeric values* per project are not on that page and depend on the
  project's history — **UNVERIFIED here**; the owner reads the project's own
  quota page (Check 57 step 8 records the FACT).

What one run consumes: one VM, its vCPUs against the regional (and global)
CPU quota and one instance against the instances quota, one boot disk, and —
with NAT — no IP quota but NAT egress; with an external IP, one in-use IP
against a global quota. A team running N concurrent Fugaro runs therefore
needs CPU quota for N × (vCPU per profile), which is a **new** quota
conversation Fugaro does not have today (Cloud Run has its own, unrelated
quota). This is the practical limit the owner should watch: on a default
project the CPU quota, not Batch, is what refuses the eleventh concurrent
run.

**Substrate verdict.** Identical (Batch is subject to Compute Engine quotas).
A MIG additionally consumes instance-group manager quota.

### 1.9 Operational weight

What `fugaro init` would create for the VM backend, and how heavy that is:

- **Cloud Batch:** enable the Batch API; the Batch service agent's IAM (Batch
  creates it on first use); optionally a Cloud NAT + router + subnet; a
  preloaded boot image per `fugaro-base` version (or reliance on image
  streaming); the log sink/exclusion change (§1.5); and — with
  project-centric — a `profile` naming the VM class. There is **no
  per-run and no per-repository resource**: it is one-time, installation
  level.
- **Plain instances:** everything above **plus** an instance template (or a
  generated `instances.insert`), a startup script, a status/timeout watchdog
  and a **reaper** (§3) — because nothing else deletes a leaked VM.
- **MIG:** everything above **plus** an instance group and its manager, and
  the sizing question a MIG forces (idle capacity you pay for, or cold
  scale-up per run). A MIG is the wrong tool for "one ephemeral VM per run"
  and is rejected (**C1**).

Relative to the project-centric goal of "no heavy per-repository setup"
(project-centric §1, §4.4): the VM backend is **orthogonal and light at the
repository level** — a repository never gets VM infrastructure of its own.
The weight is installation-level and one-time, which is where project-centric
already accepts weight (the base image, the registry, Firebase). It does add
one thing project-centric removed: a base-image artifact to rebuild and store
per GCP project (the preloaded boot image), which is the operational price of
the cold-start mitigation.

**Substrate verdict.** Batch wins: it is the only one of the three that does
not require Fugaro to implement VM lifecycle, timeouts, logging and teardown
itself.

### 1.10 Decision C1: the substrate

| # | Decision | Recommendation | Veto alternative / trade-off |
|---|---|---|---|
| **C1** | Substrate | **Cloud Batch**, one job with one task per run, Container-Optimized OS boot disk, `fugaro-base` as a container runnable | **Plain Compute Engine instances** (full control of the startup script and the socket, no Batch container-runnable quirks; but Fugaro implements instance lifecycle, timeouts, log shipping and a reaper). **MIG**: rejected — homogeneous fleets, not per-run ephemeral VMs; you either pay for idle capacity or re-implement Batch. |
| **C2** | Where Docker comes from | **The VM's host daemon, via the bind-mounted socket** | **Rootless/Podman inside the container** or **privileged DinD**: same host-level power, more moving parts, doubles storage, needs an unverified `--privileged`/user-namespace path in a Batch runnable. |
| **C3** | Isolation unit | **One VM per run; a VM is never reused** | Reuse a pool of warm VMs: rejected — two runs on one VM can read each other's secrets and Docker state, and Docker makes each root on the host. |
| **C4** | Cold start | **Preloaded boot image per base version, with image streaming as the lighter alternative** | Plain pull every run (simplest, slowest); Check 57 measures both before the choice is fixed. |
| **C5** | Spot | **Off by default**, opt-in per profile | Spot by default: up to 91% cheaper, but a preemption kills the stage mid-flight and the 30 s notice makes checkpointing marginal. |
| **C6** | Service account | **One per run class (profile, account), reused from the project-centric model**; never the default Compute Engine SA | Default Compute Engine SA: project-wide and over-broad. |
| **C7** | Egress | **No external IP; Cloud NAT** | Ephemeral external IP + deny-all ingress: simpler, cheaper per run, but "no inbound" then rests on a firewall list. |
| **C8** | Timeouts and cancel | **Batch `maxRunDuration` + `jobs.delete`**, tolerating a cancel that races a finished job | Fugaro's own watchdog (needed only on plain instances). |
| **C9** | Logs | **`logsPolicy.destination=CLOUD_LOGGING`**, and extend the installation's sink/exclusion to `batch.googleapis.com/Job` | Leave logs in `_Default`: `fugaro logs` finds nothing and every viewer reads them. |
| **C10** | Secrets | **The runner resolves named secrets from Secret Manager at start, as the VM's service account** | Bake values into the job definition: rejected — secret values would sit in a job object anyone with `batch.jobs.get` can read. |
| **C11** | `fugaro init` weight | **One installation-level Batch stage; no per-repository resource** | Per-repository VM setup: rejected — reintroduces exactly the weight project-centric removes. |

## 2. How a run executes there

### 2.1 The same image and the same runner

The guiding rule from generic-tool §1 and v1 §14 is kept literally: **the VM
runs the same `fugaro-base` image and the same `fugaro exec`** that Cloud Run
runs, so that the gateway, the budget session, the cache and `finalize` work
unchanged.

- The container image is `fugaro-base` at the installation's pinned version.
  Its `ENTRYPOINT ["/usr/bin/tini","--"]` and `CMD ["fugaro","exec"]`
  (`images/base/Dockerfile`) are exactly what a Batch container runnable uses
  by default; the runner sets its own flags from the job env
  (`internal/cli/exec.go`), so **no `entrypoint`/`commands` override is
  needed** — the same default that the workflow job uses on Cloud Run.
- The runner never imports a backend implementation (`internal/runner` uses
  only `backend.OnCloudRun`, `backend.SameExecution`, `backend.Prices`,
  `backend.MemoryGiB`, `backend.TaskTimeoutSlack`). The gateway
  (`runner/gateway.go`), the budget session (`runner/budget_session.go`,
  which is RTDB, not `internal/backend`), the cache
  (`runner/lockcache.go`) and `finalize` (`runner/runner.go`) touch only the
  process, the RTDB and the runs bucket — all of which behave identically on
  a VM. **Nothing in those four paths changes.**
- The job env is the same set `platformEnv`/`budgetEnv` already write
  (`internal/infra/spec.go`): `FUGARO_GCP_PROJECT`, `FUGARO_REGION`,
  `FUGARO_PROJECT`, `FUGARO_BACKEND`, `FUGARO_BUCKET`, `FUGARO_RUN`, the
  state/checkout/remote variables, the budget variables and the model-provider
  variable. On Batch they are written into the job's `environmentVariables`
  instead of Cloud Run's `env`; the names and values are unchanged.

What is genuinely new, and is the bulk of the work: the **Docker socket**
(§2.2), the **secrets bootstrap** (§1.6), the **name/parse seam**
(§2.5) and the **provisioning stage** (§4).

### 2.2 Where Docker comes from, concretely

The Batch container runnable carries one bind mount from the host:

```
"volumes": ["/var/run/docker.sock:/var/run/docker.sock"]
```

(from the container runnable's `volumes` field, "formatted to match
`--volume` option for the `docker run` command", Batch REST reference, checked
2026-10-15). The VM's Container-Optimized OS already runs Docker, so the
socket exists before the runnable starts. The agent's environment then needs
`DOCKER_HOST` (or the default socket path) — the one env-allowlist addition,
reviewed as such (§4.3) — and the container user needs permission on the
socket (`root:docker`, mode `0660` on COS).

The three ways to get that permission are ordered in §1.1: run the container
as root (Batch's default `runAsNonRoot` is false, Batch REST reference,
checked 2026-10-15 — but that changes the runner's effective user from
`fugaro` to root and must be checked against the base image's expectations);
pass a supplementary group through the runnable's `options`
(**UNVERIFIED**, documented only as "arbitrary additional options", e.g.
`--network host`); or adjust the socket's group in the preloaded boot image.
Check 57 step 6 records which works.

Security consequence, repeated because it is the whole of §3: **a run that has
this socket is root on the VM.** The mount is not a privilege boundary and is
not treated as one.

### 2.3 The backend interface, mapped

Every method of `internal/backend.Backend` maps onto Batch:

| Seam method | Batch | Notes |
|---|---|---|
| `Launch(LaunchSpec)` | `projects.locations.jobs.create` | One Batch job, one task group, one task. Name derived per run (§2.4). The `FUGARO_RUN` env and the timeout are set in the job definition, not in an override — Batch has no execution-override concept like Cloud Run's `Overrides`. Returns `ExecutionRef{Name, Job, LogURL}`. |
| `Execution(name)` | `jobs.get` | Maps Batch's state enum to `backend.State`: `QUEUED`/`SCHEDULED` → `StatePending`, `RUNNING` → `StateRunning`, `SUCCEEDED` → `StateSucceeded`, `FAILED` → `StateFailed`, `DELETION_IN_PROGRESS` → `StateCancelled`. `Created`/`Started`/`Completed` from the job's times; `CPU`/`MemoryGiB` from the machine type; `Run` from the `FUGARO_RUN` the job was created with (Batch reports the job's `environmentVariables`, so unlike Cloud Run this one is always readable — still a hint, never evidence). |
| `List(filter)` | `jobs.list` | Filter by the Fugaro labels and `CreateTime`; the `fugaro-` name prefix is replaced by the label predicate. |
| `Logs(query, fn)` | Cloud Logging, through the same installation log view | Filter `resource.type="batch.googleapis.com/Job" AND labels."batch.googleapis.com/job_id"="<job id>"` (label names from cloud.google.com/logging/docs/api/v2/resource-list, checked 2026-10-15), pinned to the region like the Cloud Run filter is. |
| `Cancel(name)` | `jobs.delete` | Tolerate `NOT_FOUND` and a job already terminal (the same race live check 5 already documents for Cloud Run). |
| `LongestTaskTimeout()` | The longest configured workflow timeout | See §2.5: on Batch there is no deployed job to read it from. |
| `TaskTimeout(slug, workflow)` | The workflow's configured timeout | Same: a config/policy read, `ErrNotFound` for an unknown workflow. |

Two properties make this work: a Batch job is created **per run** (there is no
reusable job definition to update), so `Launch` is a create rather than a
`run`; and a Batch job is **single-use** — once it reaches `SUCCEEDED` or
`FAILED` it cannot be run again — which matches Fugaro's "one execution per
run" exactly. `maxRetryCount` stays **0** ("The default, 0, which means never
retry", Batch REST reference, checked 2026-10-15): a Fugaro run is not
idempotent, the same reason Cloud Run's job sets `max_retries = 0`.

### 2.4 Naming, labels and adoption

The naming and marking conventions are the same as every other Fugaro
resource (`internal/backend/gcp/names.go`), so discovery and the guard keep
working:

- **Job id**: `fugaro-<slug>-<workflow>-<12 hex of the run id>`, built with
  the existing `derive`/`JobName` scheme. It must be unique per
  project+region, which the run id guarantees, and it is the identity the run
  stores in `launch.json`/`result.json` and that `fugaro logs`, `cancel` and
  `diagnose` follow.
- **Labels** on the job (Batch applies job labels to its VMs and to the log
  entries it generates, Batch REST reference, checked 2026-10-15):
  `fugaro=managed`, `fugaro_repo=<slug>`, `fugaro_workflow=<variant>`, plus a
  per-run label `fugaro_run=<slug>-<run id>` (sanitized to the label charset,
  ≤63 chars). The per-run label is what lets the reaper and a human find a
  leaked VM without reading every job. The Task-28 outline in the generic-tool
  plan already names "the labels used for adoption"; these are them.
- **Adoption** (`internal/infra/discover.go`, `ForeignError`) refuses to adopt
  a resource whose labels do not match exactly; a VM backend brings the same
  rule to Batch jobs and to any preloaded boot image (labelled
  `fugaro=managed`, like the base-image registry resources).

**C13 (naming)**: reuse the Fugaro label set verbatim, and add the per-run
label. Veto alternative: mark only the job and rely on the job id's prefix,
which is fragile for a VM that outlives its job.

### 2.5 The seam's Cloud-Run-shaped names (a required change)

`internal/backend` is not fully backend-neutral at its edges:
`ParseExecution`, `SameExecution`, `ExecutionFromEnv` and
`CheckRunExecution` all assume Cloud Run's
`projects/<p>/locations/<r>/jobs/<j>/executions/<e>` shape, and `OnCloudRun`
is literally "`CLOUD_RUN_EXECUTION` is set". A Batch job's resource name is
`projects/<p>/locations/<r>/jobs/<j>` — six fields, not eight — so the two
cannot share a parser, and on a Batch VM `CLOUD_RUN_EXECUTION` is absent, so
`OnCloudRun` is false and the runner would treat itself as a local run.

**Recommendation (C12): generalise the seam.** Move the name codec into each
backend — a small interface (`Parse(name)`, `Format(id)`, `FromEnv(env)`,
`IsOwn(name)/ValidateRun(name, slug, workflow)`) carried by the `Backend`,
used by every caller instead of the package-level functions, and a
backend-supplied "in the cloud" signal replacing `OnCloudRun`. The runner
then asks its backend "am I on you?" rather than checking a Cloud-Run variable.
Trade-off: it touches every caller (`cli/cancel.go`, `logs.go`, `diagnose.go`,
`run.go`, `watch*`, and the runner's `SameExecution` uses) and is a change to
the shared package, not a new sibling package — a bigger than average first
task. Veto alternative: keep the Cloud-Run shape and encode a Batch job as a
synthetic "execution" under it (`.../jobs/<fugaro job>/executions/<run id>`),
which lies in the stored data model and would confuse `fugaro ls`, `cancel`
and the sweeper the first time someone reads a raw name. **C12 is an owner
decision by size, not by uncertainty.**

## 3. Security

The honest statement a reader wants first: **a run that needs Docker is
root-equivalent on its host.** Everything below is the consequence of that,
stated rather than argued away (project-centric §3, principle 5).

### 3.1 VM-per-run versus the shared Cloud Run job

project-centric §4.1 removes boundaries when several repositories share one
Cloud Run job, and D4 **accepts and documents** that loss: inside one shared
project job, every run can read every secret the job mounts, and a run — an
untrusted agent, unlike a launcher — could poison another repository's cache
or locks, because the bucket condition narrows to one prefix.

**A VM per run does not have that loss.** A shared project job brings the
repo-to-repo regression only because Cloud Run cannot vary the image, service
account, CPU or memory per execution, so it has to share one job across
repositories. A VM backend does not share anything: each run gets its own VM,
its own service account, its own process, its own Docker state, and its own
kernel-level boundary from every other run. The bucket condition stays per
slug — there is no reason to broaden it. So:

- **C3 restated as a security property:** the VM per run *restores*, relative
  to a shared Cloud Run job, both the cross-repository bucket isolation of
  0.7.0 (bucket-iam.md §3) and the secret isolation. The VM backend is the one
  place in the project-centric redesign where D4's accepted loss does not
  apply.
- **But it removes a boundary Cloud Run had:** on Cloud Run the job is a
  container with no daemon and no root. On a VM the run is root on the host
  (§3.5). The isolation that people must rely on moves from "the container" to
  "the VM", which is *stronger against other runs* and *weaker against the
  host* than Cloud Run. Both directions are stated in SECURITY.md.

### 3.2 The instance service account

- **One service account per run class, and never the default Compute Engine
  service account.** Batch uses the default Compute Engine SA when
  `allocationPolicy.serviceAccount` is omitted (Batch REST reference, checked
  2026-10-15); the default is project-wide and broadly privileged, so omitting
  it is forbidden. The VM backend names the job service account of
  (`profile`, `account`), exactly the one project-centric §4.1 already defines
  and the one the runner already binds to (`slug == task.Slug(...)`).
- **Scope it to what the run needs and nothing more:** `objectUser`
  conditioned to `runs/<slug>/`, `cache/<slug>/`, `locks/<slug>/`
  (`gcp.BucketCondition`), `secretAccessor` on the mounted secrets only,
  `logging.logWriter`, and `artifactregistry.reader` on the base image's
  registry. No project-level grants beyond the Vertex `aiplatform.user` a
  Vertex workflow already needs.
- **The service account's power is the blast radius.** A run's SA can read
  only its own slug's objects and its own secrets; that bound is what makes
  the root-on-the-VM problem survivable. If the SA were project-wide, root on
  any one VM would be root on the project's data.

### 3.3 The metadata server

The VM's metadata server (`169.254.169.254`) hands any process on the VM a
token for the VM's service account. Two facts follow:

- **The agent can read its own run's SA token.** It already can on Cloud Run
  (the job SA is the container's identity), so this is not new in kind — but on
  a VM the agent can also start its *own* containers, and each of those can
  reach the metadata server unless the container is started with
  `--network none` or a blocked route. So the exposure widens from "the agent"
  to "the agent and anything the agent runs". The mitigation is §3.2's minimal
  scope, and SECURITY.md says so.
- **The container cannot be sealed off from it** while it holds the Docker
  socket: a socket-holding process can `docker run` with host networking and
  reach the metadata server regardless. This is why the VM, not the container,
  is the boundary.

### 3.4 Deleting the instance on every exit path

A leaked VM is both a cost and a security problem (a live host holding a run's
secrets and SA token). Batch does most of this for free — when a job reaches
`SUCCEEDED`, `FAILED` or `DELETION_IN_PROGRESS`, Batch tears down its VMs —
but the design does not rely on that alone, because "most exit paths" is not
"every exit path":

- **Success, failure, cancelled:** Batch job terminal states, with the VMs
  expected to be torn down by the service, and `jobs.delete` (cancel) moving
  the job to `DELETION_IN_PROGRESS` (Batch REST reference, checked 2026-10-15).
  The teardown is assumed, not documented on the pages read, and Check 57
  step 8 measures it.
- **A crashed runner:** if `fugaro exec` exits non-zero or panics, the task
  fails, the job fails, the VMs go — provided `maxRetryCount` stays 0 (§2.3),
  since a retry would start a second run of a non-idempotent task.
- **A hung job:** Batch itself fails a job that runs longer than 14 days
  (cloud.google.com/batch/quotas, checked 2026-10-15), but Fugaro's own
  `maxRunDuration` binds first and is far shorter.
- **The residual, and the reaper.** A stuck `DELETION_IN_PROGRESS`, an orphaned
  VM after a Batch-side fault, or (on the plain-instance alternative) a
  startup script that never ran: for these the design keeps **a reaper** — the
  existing scheduled sweeper pattern (project-centric §8, `history.tf`)
  extended to list Batch jobs and Compute Engine instances labelled
  `fugaro=managed` whose age exceeds the longest workflow timeout plus
  `backend.TaskTimeoutSlack` plus a margin, and delete them, writing a FACT
  line. Batch's optional Pub/Sub job-state notifications (Batch REST
  reference, checked 2026-10-15) are a cheaper trigger than polling and are a
  plan option. **C14**: keep a reaper even though Batch mostly self-cleans;
  the veto alternative is to rely on Batch teardown alone and accept a rare
  orphan, which the owner may prefer to save the machinery — the trade-off is
  an occasional VM billing until someone notices.

### 3.5 What a Docker-capable agent can do to the host

Stated plainly, for SECURITY.md:

- **Root on the VM.** Through the socket, `docker run --privileged -v /:/host`
  gives the agent the VM's filesystem, its SA token, and its network. It can
  read the runner's own environment (the secrets the bootstrap exported) — but
  it holds those secrets anyway, in-process, so this is not a new secret
  exposure *within* the run.
- **Not root on the project.** The SA's IAM (§3.2) is the wall: the agent
  cannot read another repository's runs, another run's secrets, or (with the
  log sink fixed, §1.5) another run's logs. It can, however, do anything the
  run's SA can — so the SA's scope is the security control that matters, and
  it must stay per-slug.
- **Not the neighbouring run.** Separate VM, separate kernel, separate SA:
  nothing in one run reaches another.
- **Egress is open**, exactly as on Cloud Run (generic-tool §9): the agent can
  send whatever it can read to anywhere the network allows. Cloud NAT does not
  change this; it only removes inbound reachability.

### 3.6 SECURITY.md additions

The VM backend adds a section to SECURITY.md stating: the run is root on its
VM; the VM is the isolation unit and is never reused; the run's service
account and its per-slug scope; the VM metadata server is reachable by the
agent and by containers it starts; a leaked VM is reaped; and the egress
posture is unchanged. Veto alternative: leave SECURITY.md and let the design
document carry it — rejected, because a trust model an adopter cannot find in
SECURITY.md is not a trust model (the same ruling the generic-tool work made,
G21).

## 4. Configuration

### 4.1 Where the backend is chosen

The seam already has the knob: a project config's `backend:` key names the
backend, `cloud-run` is the default and today the only accepted value
(`internal/localcfg/localcfg.go` validates the name; `backend.Open` picks from
the openers map in `internal/cli/cloud.go`; generic-tool §5 already anticipates
`fugaro backends ls` "when a second backend exists").

**Recommendation (C15): the choice is per project (installation), not per
workflow.** A backend is infrastructure — a service account, a quota, a log
sink, a boot image — and a per-workflow backend would mean one project paying
for and operating two compute platforms at once, plus a second set of
provisioning and IAM for the same repository. It also fights the project-centric
grain: policy is per project, and "which platform runs my jobs" is policy.
Value: `backend: cloud-batch`. Trade-off: a repository that needs Docker in a
project that otherwise runs on Cloud Run forces the whole project onto the VM
backend. Veto alternative: a per-workflow `backend:` key (finer grained, at the
cost of running two platforms and doubling provisioning); or a per-profile
backend inside the project policy (project-centric §4.1), which is the same
coarse grain as the recommendation but expressed in the new policy object —
likely correct **after** phase 3, and this document's recommendation is the
pre-phase-3 spelling of it.

### 4.2 The per-workflow need for Docker

A project may run on the VM backend while most of its workflows do not need
Docker, and — more importantly — a workflow that never needs Docker should not
pay for a VM or expose the socket. generic-tool §1 already decided the shape
for this: a workflow key `docker: true` (scopes: project via a profile, and
repository). **Recommendation (C16): reuse `docker: true`**, because it names
the capability rather than the mechanism, and it is already the decided name.

- On the VM backend, `docker: true` mounts the socket, adds `DOCKER_HOST` to
  the agent's environment allowlist (scoped so it appears **only** for such
  workflows) and requires the socket-permission path of §1.1.
- On the Cloud Run backend, `docker: true` is **refused by `fugaro validate`**
  with a message naming `backend: cloud-batch` (this is the eventual successor
  of the 0.8.0 "Docker stays out" message).
- Veto alternatives: `runtime: docker` (a second key meaning the same thing;
  `runtime` invites a container-runtime reading it does not have); or no key at
  all, with `backend: cloud-batch` implying Docker everywhere in the project
  (simplest, but every workflow then gets a socket it may not need).

### 4.3 Layered config and profiles

- **Scopes** (layered-config's scope table): `backend:` is installation-level
  (set by `fugaro init`, overridable by the project layer), not a repository
  key — a repository cannot choose the platform. `docker:` follows
  generic-tool §1: repository and project-via-profile.
- **Profiles** (project-centric §4.1) are where the VM class lives: a profile
  `{cpu, memory, task-timeout class, secrets, allow_repos}` gains the
  Docker/VM attributes the VM backend needs (machine type / VM class, spot
  on/off, preloaded-image version). A `docker: true` workflow selects a
  Docker-capable profile; the profile is what the VM is sized and configured
  from. Before phase 3 (no profiles yet), the same values come from the
  existing `resources` and `timeouts` keys, extended with the VM class.
- **The env allowlist** (`internal/agent/env.go`) gains `DOCKER_HOST` only for
  `docker: true` workflows; the review standard for that addition is the one
  generic-tool §1 sets ("the first addition to the env allowlist since M9").

### 4.4 What `fugaro validate` checks

Offline, following the existing model (`gcp.CheckResources` for Cloud Run):

1. **The backend name is known** — a `backend.Open`-style lookup into the
   known names, with the same "known backends: …" message.
2. **`docker: true` requires a Docker-capable backend.** `docker: true` with
   `backend: cloud-run` is an error naming `cloud-batch`.
3. **Resources fit the VM backend.** Cloud Run's `cpuChoices`
   (1/2/4/6/8) and memory/CPU coupling (`internal/backend/gcp/limits.go`) do
   not apply on a VM: Compute Engine machine types are more varied, and a
   custom machine type allows arbitrary vCPU/memory. The VM backend ships its
   own `CheckResources` (generic-tool §2, v1 §3.4: "a Batch backend would
   bring its own of each") that validates the machine type and the disk.
4. **Timeouts fit.** A workflow timeout must be inside Batch's `maxRunDuration`
   and well inside the 14-day RUNNING limit (cloud.google.com/batch/quotas,
   checked 2026-10-15).
5. **Spot is not combined with a workflow that must finalize.** If a profile
   has spot on and the workflow's outcome rules require a finalize/PR, validate
   warns (not errors): a preemption can drop the finalize (§1.3, §3.4).
6. **The socket is not requested without Docker.** No `DOCKER_HOST` in the
   allowlist, no socket mount, for a workflow without `docker: true`.

`fugaro doctor` gains the same checks against the live project (the Batch API
enabled, the SA's grants, the boot image present, the log sink arm) in the
style of the existing provisioning checks, and `fugaro backends ls` lists
`cloud-run` and `cloud-batch` with which one the project uses.

## 5. The budget

### 5.1 Gateway and caps: unchanged inside the VM

The gateway is a `net/http` handler inside `fugaro exec` (`runner/gateway.go`),
enforcing the per-run cap (the lowest positive of the committed policy,
`FUGARO_MAX_RUN_USD` and the RTDB caps) and the daily caps through RTDB leases
(m9-budget-and-dashboard.md §4–§5). None of it touches the compute platform:
it is process-local plus RTDB. So **the gateway, the per-run cap, the daily
caps, the kill switches, the `halted`/`result.json` path and the budget
session all work identically on a VM**, with no change. The launcher-side
precheck (`cli/launch_budget.go`) is likewise unchanged except that
`TaskTimeout` is now resolved from config/policy (§2.5) to size the lease.

### 5.2 Compute dollars: instances are billed differently

`cost.compute_usd` is an estimate of the compute the run consumed (v1 §10.1:
billed duration × (vCPU × price per vCPU-second + GiB × price per GiB-second)),
produced by `backend.Prices` for the backend's region (`internal/backend/gcp/
prices.go` is the Cloud Run table; a Batch backend ships its own). Two
differences to model, both from cloud.google.com/compute/pricing (checked
2026-10-15):

1. **A one-minute minimum.** "All vCPUs, GPUs, and GB of memory are charged a
   minimum of 1 minute." The backend's compute estimate must floor the billed
   duration at 60 s, or every short run is under-reported. `Execution.Billed`
   currently returns the raw duration; the VM backend applies the floor where
   it computes `compute_usd` (not in `Billed`, which other callers use for
   different purposes).
2. **A different price table, plus extras.** Compute Engine vCPU/GiB list
   prices per region differ from Cloud Run's, and a run may also consume a
   boot disk and Cloud NAT. Recommendation (C17): `compute_usd` covers the
   VM's vCPU/memory (the same shape as today, so `report`/history need no new
   column), and the disk/NAT costs are **named as not in the figure** in the
   report's source line — they are small but not zero, and pretending they are
   in the per-run figure would be a worse lie than naming them out. The exact
   list prices are UNVERIFIED here and belong in the backend's price table
   with its own `Source` string and check date, the way Cloud Run's does.

**Compute stays reported, never capped** (m9 D7): the per-run and daily caps
are model dollars only. This matters more on a VM, where a Docker-heavy run
can be long and compute-heavy; the design keeps the m9 rule rather than
inventing a compute cap. The daily history's `computeMicros` (m9 §549, §776)
carries the VM estimate unchanged in shape.

## 6. Migration and scope

### 6.1 Existing Cloud Run users: nothing changes

- `backend:` defaults to `cloud-run`; an installation that never writes
  `cloud-batch` is untouched. No new IAM, no new API, no new quota, no
  Terraform diff for a Cloud-Run-only project.
- The one shared change — the log sink/exclusion gaining a
  `batch.googleapis.com/Job` arm (§1.5) — is additive and inert for a project
  with no Batch jobs. It still needs an owner-run apply (the log bucket is
  installation Terraform).
- The seam generalisation (§2.5) is internal; it changes no stored data for
  existing runs once it formats Cloud Run names exactly as today.

### 6.2 Interplay with the 0.8.0 phases

A VM backend is **orthogonal to shared project jobs** (project-centric phase
3). Shared jobs exist because Cloud Run cannot vary a job per execution; a VM
backend creates a fresh job per run, so it never needs to share, and it
*sidesteps* the isolation loss D4 accepted (§3.1). Consequences:

- **Before phase 3**, a VM backend uses the current model: a per-repository
  job service account and an installation-level Batch stage. It works, but it
  carries the per-repository SA weight project-centric is removing.
- **After phase 3**, the same backend uses a project job service account per
  (`profile`, `account`) and profiles select the VM class (§4.3) — the intended
  end state.
- The **seam generalisation (C12)** is independent of both and can land first.
- Recommendation: the plan's **first task is the conformance suite against a
  fake** (§8.1, task 2), which needs neither phase 3 nor any cloud, and the live
  measurement (Check 57) gates the rest.

### 6.3 Out of scope

- **Docker on Cloud Run** (gen-1 or gen-2): measured impossible by the owner;
  the gen-2 probe (Task 26) and Docker-on-gen-2 (Task 27) are dropped.
- **Rootless Docker / Podman**: not the chosen mechanism (§1.1); revisitable
  only if the socket path fails Check 57's permission step.
- **Windows VMs, GPUs, TPUs.**
- **A compose/orchestration layer**: the agent brings its own
  `docker compose`; Fugaro gives it a daemon and nothing more.
- **A multi-service "service mesh"** and any pre-warming pool of VMs.
- **AWS/Azure backends** (v1 §3.4's seam leaves room; not designed).
- **A production preloaded-image pipeline** (a task, not this design).
- **Spot by default** and **a MIG**.
- **Retiring `fugaro-services`.** The no-Docker path (base-image §5,
  generic-tool §1's mitigation) stays the documented answer for services that
  do not need a daemon; the VM backend is for the ones that do.

## 7. The measurement: Check 57 (OWNER-RUN, NOT RUN)

One user-run live check settles the substrate's two risky claims — that a
Fugaro run gets a working Docker daemon, and that the cold start is bearable —
before any production code depends on them. It is the successor of the dropped
Check 31, in the format of Checks 30 and 32.

### 7.1 The number

`docs/gcp-live-checklist.md` reserves a number once and never reuses it. On
`main`, checks 1–44 are claimed and the checklist names 45 as "the next free
number". This design is told that the numbers **42–47 and 48–56 are claimed by
other plans in flight**, so it claims **Check 57** and its row in the table is
additive. If, when this lands, those reservations turn out not to exist, the
number is still valid — the ruling is "never reuse or renumber", not "always
use the lowest".

### 7.2 The check

**Check 57: the compute-instance Docker backend (sandbox only, run by you;
USER-RUN, NOT RUN).**

Nothing below has been run; nothing here is a claim that it works. It measures
what §7.3 lists. **Sandbox installation only** — the owner's `fugaro`
installation and its sandbox repository, the same one Checks 40, 41, 43 and 44
use. **NEVER the `belong` project.** **No agent runs any apply:** every
`gcloud`/`fugaro` step is typed by the owner.

**Guarded setup block (every step re-runs it; it hard-stops otherwise):**

```sh
P=fugaro-dev                 # HARD-CODED sandbox project id; never belong
[ "$(gcloud config get-value project 2>/dev/null)" = "$P" ] || { echo "STOP: wrong project"; exit 1; }
[ "$(basename "$PWD")" = "fugarosandbox" ] || { echo "STOP: not the sandbox checkout"; exit 1; }
echo "FACT: project=$P pwd=$PWD"
```

(If the sandbox's real project id differs, the owner substitutes it in the
one hard-coded line; the guard's job is to make `belong` unreachable by
accident, not to be clever.)

1. **Baseline (Cloud Run).** On the sandbox, `fugaro run` a no-change task and
   time launch-to-first-log-line (`fugaro logs RUN`, the first `started`
   line), five times. This is Check 41's measurement, reused. **FACT:** the
   five times and their median.
2. **Cold start without a preloaded image.** Create one Batch job in the
   sandbox: a container runnable from the sandbox's `fugaro-base`, a
   Container-Optimized OS boot disk, `logsPolicy.destination=CLOUD_LOGGING`,
   the socket bind mount, a dedicated service account, the same env the Cloud
   Run job sets, and `maxRetryCount: 0`. Run it five times and record
   submission-to-first-line. **FACT:** the five times, the median, and the
   delta over step 1.
3. **Cold start with a preloaded image.** Rebuild the boot disk with
   `fugaro-base` already in the Docker data root (or point the runnable at the
   base served by image streaming from the in-region Artifact Registry), and
   repeat step 2. **FACT:** the five times, the median, and the delta over
   steps 1 and 2.
4. **Docker inside a run.** In a run on the Batch job, `docker run --rm
   docker.io/library/postgres:17 postgres --version`, then start a small
   Postgres container and wait for `pg_isready`. **FACT:** whether the socket
   was reachable; which permission path let the container open it (container
   as root, a supplementary group via the runnable's `options`, or a socket
   group baked into the boot image); the pull time; and the time to
   `pg_isready`.
5. **The cost of one run.** Read the billing export (or the console's cost
   table) for one step-2 run and compare it with the `cost.compute_usd` the
   run recorded. **FACT:** the actual billed vCPU/memory seconds, the disk,
   NAT if used, the estimate, and how much the one-minute minimum added.
6. **The socket's permission path, in detail.** Record whether a Batch
   container runnable's `options` accepts `--group-add`, `--user` and
   `--privileged`, and which of them the design needs. **FACT** for each.
7. **(Optional) Preemption.** Run one job with a spot provisioning model and
   preempt it (or let it happen). **FACT:** whether the runner received
   SIGTERM, whether it reached `finalize`, and the time from the preemption
   notice to the VM's death.
8. **Teardown on every exit path.** For each of: success; failure (a runnable
   that exits non-zero); cancel (`fugaro cancel`, which deletes the job); and
   a crashed runner (kill `fugaro exec`'s process), list the surviving
   resources (`gcloud compute instances list --filter='labels.fugaro=managed'`
   and `gcloud batch jobs list`). **FACT** per path: the job's final state,
   whether any VM survived, and (if a reaper is built) what it deleted and
   when. This is the answer to "does the reaper delete the instance" for every
   way a run can end.
9. **Quotas.** Read the sandbox project's regional CPU quota, CPUs (All
   Regions), VM instances and in-use IP quotas (cloud.google.com/compute/docs/
   resource-quotas is the map; the numbers are the project's own). **FACT:**
   the four numbers and the largest number of concurrent Fugaro VMs they
   allow at the chosen machine type.
10. **Logs.** `fugaro logs RUN` finds the run's lines through the Fugaro log
    view (which needs the sink arm of §1.5, applied as an owner step), and the
    same lines are absent from `_Default`. **FACT:** entries found, and the
    `_Default` result.

**FACT lines to record:** every `FACT:` above; the `fugaro version` and the
date; the machine type and boot-image version; the median launch-to-first-line
for steps 1, 2 and 3; whether Docker worked and by which permission path; the
billed cost of one run and the estimate; the final state and the survivor list
for each step-8 exit path; the four quota numbers; and whether `fugaro logs`
found the Batch run's lines.

**Paste back:** the FACT lines, verbatim, plus any correction the run found and
the check's go/no-go: **build the VM backend only if step 2 or 3's median is at
most 120 s above step 1's** (generic-tool §1, G2). If both are worse, the
documented answer stays `fugaro-services` plus `image.apt`.

**Restore.** Delete the check's Batch jobs, the preloaded boot image, the
NAT/router/subnet, the service account and any VM the check left; if the log
sink arm was applied, revert it (`fugaro init` from the sandbox's config, or
the Terraform apply that reverses it). Nothing in `belong` is touched at any
step.

## 8. Task outline

### 8.1 The tasks

Not the full plan (that is written after the owner approves this design and
Check 57 answers). Ordered so **each task merges alone** and the **first one
delivers value**.

1. **The seam's name codec (C12).** Move `ParseExecution`, `SameExecution`,
   `ExecutionFromEnv`, `OnCloudRun`-as-cloud-detection and `CheckRunExecution`
   behind a per-backend codec on `Backend`, with Cloud Run's behaviour
   byte-for-byte unchanged. Merges alone; no new backend; every existing test
   green. Value: the seam is honest about names before a second name shape
   exists.
2. **The Batch fake and the conformance suite, behind a flag.** Add a Batch
   fake to `internal/gcpfake` and an `internal/backend/batch` that passes
   `backendtest.Run` against it, with `backend: cloud-batch` still **refused**
   by config validation until Check 57 passes. This is the first task a
   contributor can do and the first that proves the seam fits; it needs no
   cloud and no owner step.
3. **The Batch provisioning stage in `fugaro init`** (owner-run live step): the
   Batch API, the service account (reused), the boot image or image streaming,
   the NAT/router or the firewall, and the log sink/exclusion arm (§1.5).
4. **The secrets bootstrap**: the runner (or a base-image entrypoint shim)
   resolves the named secrets from Secret Manager as the VM's service account
   before `fugaro exec`.
5. **The Docker socket, the env allowlist and SECURITY.md** (§1.1, §3.6).
6. **Config**: `backend:` and `docker:`, `fugaro validate`, `fugaro doctor`,
   `fugaro backends ls` (§4).
7. **Cost**: the VM price table, the one-minute floor and the report/history
   source line (§5.2).
8. **Logs**: the Cloud Logging filter for `batch.googleapis.com/Job` and
   `fugaro logs` (§1.5, §2.3).
9. **The reaper and the teardown paths** (§3.4).
10. **Check 57, the docs, the skills and the release bullet** — the last task,
    as in every other group.

### 8.2 Open decisions for the owner

| # | Decision | Recommendation | The alternative, and its cost |
|---|---|---|---|
| C1 | Substrate | Cloud Batch, one job with one task per run | Plain Compute Engine instances (full control, but Fugaro owns lifecycle/timeouts/logs/reaper); MIG rejected |
| C2 | Docker source | The host daemon via the socket | Rootless/DinD (same host power, more parts, needs an unverified `--privileged` path) |
| C3 | Isolation unit | One VM per run, never reused | A warm pool (breaks run-to-run isolation) |
| C4 | Cold start | Preloaded boot image; image streaming as the lighter option | Plain pull every run (simplest, slowest); settled by Check 57 |
| C5 | Spot | Off by default, opt-in per profile | Spot by default (up to 91% cheaper, kills a stage mid-flight) |
| C6 | Service account | One per run class, never the default Compute Engine SA | The default SA (project-wide blast radius) |
| C7 | Egress | No external IP; Cloud NAT | An ephemeral IP + deny-all ingress (simpler, cheaper per run, "no inbound" rests on a firewall list) |
| C12 | The name seam | Generalise `Backend` (§2.5) | Encode a Batch job as a synthetic Cloud Run execution (lies in stored data) |
| C14 | The reaper | Keep it even though Batch self-cleans | Rely on Batch teardown alone (an occasional orphan VM bills) |
| C15 | Where the backend is chosen | Per project (`backend:`), not per workflow | Per workflow (two platforms in one project); or per profile inside the policy (correct after phase 3) |
| C16 | The Docker key | `docker: true` (generic-tool §1's name) | `runtime: docker` (a second name for the same thing); or no key (every workflow gets a socket) |
| C17 | What `compute_usd` covers | The VM's vCPU/memory; disk and NAT named as excluded | Fold disk/NAT in (a bigger change to the report shape) |
| C18 | When to build | After Check 57; the fake-conformance task (Task 2) may land before | Build before the measurement (risks building the wrong substrate) |
| C19 | The preloaded image | A script in `fugaro init`'s Batch stage, one image per base version | Rely on image streaming alone (no image build, but depends on Artifact Registry + containerd) |
| C20 | Phase order | Independent of the project-centric phases; profiles (§4.3) adopted when phase 3 lands | Wait for phase 3 (cleaner, but delays a capability the owner needs) |

## 9. Verified and unverified

**Verified** from Google's public documentation, read 2026-10-15 (each cited
above where used):

- Batch limits: task-group/task/VM counts, 1 MB job definition, the 2-day
  `QUEUED` and 14-day `RUNNING` ceilings (`cloud.google.com/batch/quotas`).
- Batch container runnables: `volumes` bind mounts, `options`, `imageUri`,
  `blockExternalNetwork`, `enableImageStreaming`; and `enableImageStreaming`
  switching the runtime to containerd with only four subfields honoured
  (Batch REST reference; `cloud.google.com/batch/docs/use-image-streaming`).
- Batch job fields used here: `logsPolicy.destination`, `allocationPolicy.
  serviceAccount`, `maxRetryCount` (default 0), the State enum (`QUEUED`,
  `SCHEDULED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `DELETION_IN_PROGRESS`), and
  that labels apply to the job, its VMs and the log entries it generates
  (Batch REST reference).
- Batch logs reach Cloud Logging only when the destination is set, and the
  monitored resource is `batch.googleapis.com/Job` with labels
  `resource_container`/`location`/`job_id`
  (`cloud.google.com/batch/docs/analyze-job-using-logs`;
  `cloud.google.com/logging/docs/api/v2/resource-list`).
- Container-Optimized OS ships Docker and containerd with no on-host setup,
  and is locked down by default
  (`cloud.google.com/container-optimized-os/docs/concepts/features-and-benefits`).
- Compute Engine billing: a one-minute minimum, then per-second
  (`cloud.google.com/compute/pricing`); Spot is up to 91% off, may be
  preempted at any time, stops or deletes the VM, with a 30 s default
  preemption notice (extendable to 120 s)
  (`cloud.google.com/compute/docs/instances/spot`).
- Compute Engine quota model: VM quotas are regional, the VM-instances quota
  is auto-set to 10× the CPU quota, CPU quota is regional with a global
  CPUs (All Regions) quota, and IP quotas are global
  (`cloud.google.com/compute/docs/resource-quotas`).
- Cloud NAT is the no-external-IP egress path
  (`cloud.google.com/nat/docs/overview`).

**Verified by reading the code (this repository):** the `Backend` interface
and its Cloud-Run-shaped names (`internal/backend/backend.go`); the
conformance suite (`internal/backend/backendtest`); the log sink filter
(`deploy/terraform/gcp/modules/installation/logging.tf`); the runner's
backend-agnosticism (`internal/runner`); the launch path
(`internal/cli/run.go`, `launch_budget.go`); the naming/labels
(`internal/backend/gcp/names.go`); the base image's CLI-only Docker and its
3812 MB size (`images/base/Dockerfile`, base-image.md §9, §13).

**Unverified, each with what settles it:**

- Batch container runnables accepting `--privileged`, `--user` or
  `--group-add` in `options` — Check 57 step 6.
- Whether the base image's container user can open the COS docker socket, and
  which permission path works — Check 57 step 6.
- VM boot and Batch scheduling times, with and without a preloaded image —
  Check 57 steps 2 and 3.
- Whether Batch adds any billable line beyond the VMs — Check 57 step 5.
- Whether Batch truly tears down every VM on `DELETION_IN_PROGRESS` and on a
  crashed runnable — Check 57 step 8.
- Whether image streaming works for a base image served through an Artifact
  Registry **remote** repository — Check 57 step 3 (pairs with Check 43).
- The project's own default quota numbers — Check 57 step 9.
- Whether a workflow's Cloud Run `resources` values map cleanly onto a
  Compute Engine machine type — a plan task (§4.4 item 3).
- Cloud Run gen-2's inability to run Docker is the **owner's measurement**
  (the premise of this design, restated in the generic-tool plan's 2026-10-14
  ruling); it is not independently reproduced by this document, and it does
  not need to be — the measurement is why the VM route exists.
