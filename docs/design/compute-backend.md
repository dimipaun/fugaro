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

The decisions are C1 to C11 in §1.10 and C12 to C20 in §8.4; each carries a
recommendation and the trade-off the owner can weigh instead.

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
  job leaves `RUNNING` for `DELETION_IN_PROGRESS` and its VMs are torn down.
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
(**C7**, §8.4).

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
