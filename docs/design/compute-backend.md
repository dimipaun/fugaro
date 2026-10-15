# A Docker-capable compute backend: workloads on Compute Engine VMs

Status: design for review (2026-10-15), written to the owner's ruling of
2026-10-14 (restored in the generic-tool plan, PR #374): the Docker-capable
execution backend, generic-tool section 1, was "gen-2 first, a VM backend as
the fallback" ([generic-tool.md](generic-tool.md) §1,
[plans/2026-10-08-generic-tool.md](../plans/2026-10-08-generic-tool.md)
Task 28). Cloud Run gen-2 cannot run Docker — the owner measured it — so
gen-2 failed its own first tier and **the VM backend is the route**. The gen-2
Docker probe (Task 26 — the plan numbered it Check 31, a number
`docs/gcp-live-checklist.md` had already given to `fugaro upgrade`) and Task 27
(Docker on gen-2) are dropped; Task 28 is restored and is no longer conditional
on the probe. Nothing here is
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

The decisions are C1 to C11 in §1.10, C12 to C17 inline in §2 to §5, C18 to
C20 in §8.2, and C21 and C22 inline in §3.1 and §3.7; §8.2's table collects all
twenty-two, each with its recommendation and the trade-off of the alternative.

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
  on-host setup required";
  cloud.google.com/container-optimized-os/docs/concepts/features-and-benefits,
  checked 2026-10-15). The runner container is
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
  are *plausible* but not documented; the check that settles it is Check 57a
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
`options` is UNVERIFIED — Check 57a step 6 records the FACT. The fallbacks, in
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
  are UNVERIFIED — Check 57a measures launch-to-first-log-line directly.
- **The image pull**: a 3.8 GB pull over the VM's network is the thing to
  avoid repeating. Three mitigations, in order of how much they are expected to
  help — each with a caveat that undercuts it, and **the measurement decides,
  not this document** (Check 57a steps 2 and 3):
  1. **A preloaded boot image.** Build a Compute Engine image once per
     `fugaro-base` version whose Docker data root already contains the base
     image, and point Batch's `InstancePolicyOrTemplate` at it as the boot
     disk. The container runnable then starts with a local image and no pull.
     Expected to be the strongest mitigation, and the **least supported**:
     Batch documents that "Custom images can be used with Batch but might not
     be supported", recommends "building your custom image on top of a public
     image that Batch supports" (`batch-cos` is the default for container-only
     jobs), and notes that a Batch image's "minimum supported lifetime ... is
     60 days after the image's starting date" or the base OS version's
     deprecation date, whichever is earlier
     (cloud.google.com/batch/docs/vm-os-environment-overview, checked
     2026-10-15). So a per-base-version image built on `batch-cos` goes stale
     on Google's schedule as well as Fugaro's, and the fleet's oldest VM is a
     base image that may be past its supported lifetime. How a Docker data
     root is baked into a COS-derived image at all is **UNVERIFIED** here:
     COS has no package manager, and this design cites no supported procedure
     for it (Check 57a step 3 is where that is settled or abandoned).
  2. **Image streaming.** Batch container runnables support it: "the runnable
     can initialize without waiting for the entire container image to
     download, which can significantly reduce startup time for large
     container images" (cloud.google.com/batch/docs/use-image-streaming,
     checked 2026-10-15). It requires the image in **Artifact Registry** in the
     job's own location — which the registry work provides, since `fugaro-base`
     is served from an in-region Artifact Registry remote repository
     ([registry-remote-repository.md](registry-remote-repository.md)) — and it
     requires the `containerfilesystem.googleapis.com` API enabled (same page;
     §1.9, Task 3). **The catch is twofold.** It switches the container runtime
     to **containerd, not Docker**, and with streaming "container runnables
     only support the following fields: `imageUri`, `commands`, `entrypoint`,
     `volumes`, `enableImageStreaming`" — every other option is disregarded
     (same page). `options` is disregarded too, which is the field §1.1's
     `--group-add`/`--user` socket-permission path needs: **streaming and that
     path are mutually exclusive**, so a streamed run must make the socket
     usable another way (the group baked into the image, or the container
     running as root). Streaming still suits the socket design in principle —
     the runner image is pulled by containerd while the host Docker daemon is
     what the agent uses — but that combination is unproven and Check 57a
     steps 3 and 4 are what prove it.
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
  is acceptable; five is not. Check 57a measures it with and without a
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
  preempt Spot VMs at any time to reclaim the resources". Preemption is
  documented as a metadata signal (the VM's `preempted` metadata value becomes
  `TRUE`), then a **preemption notice duration** — "0 seconds (default)" or
  120 s, the only two values — then an ACPI soft-off that starts a shutdown
  period that "is best effort and up to 30 seconds" before the VM is stopped
  or deleted (cloud.google.com/compute/docs/instances/spot, checked
  2026-10-15, and cloud.google.com/compute/pricing). **The default notice is
  zero, not thirty seconds**: with no interval configured, the soft-off follows
  the metadata signal immediately and the workload's only warning is the
  30 s shutdown period. For a Fugaro run a preemption is a hard kill
  mid-stage: the model spend for that stage is gone, the run does not reach
  `finalize`, and recovery is a follow-up run. Recommendation: **spot off by
  default**, opt-in per profile for workloads that tolerate it (a nightly
  batch, a test-only workflow). If it is ever turned on, the run wants the
  120 s notice set explicitly, because the runner's SIGTERM path
  (`cmd/fugaro/main.go` cancels the context so the task still finalizes) has
  only the shutdown period to checkpoint otherwise. Whether Batch's
  `provisioningModel: SPOT` even exposes the notice duration is UNVERIFIED —
  Check 57a step 7 records what actually happens.
- **Batch itself.** Batch jobs "run on Compute Engine resources, so Batch is
  subject to Compute Engine quotas and limits"
  (cloud.google.com/batch/quotas, checked 2026-10-15), and the charge is
  documented and settled: "There is no additional cost for using Batch. You
  are only charged for the cost of the underlying resources required to
  execute your jobs" (cloud.google.com/batch/docs/get-started, checked
  2026-10-15). So there is no separate Batch line to model, and the compute
  estimate is the VMs' alone (§5.2). Check 57a step 5 still reads the billing
  export, because what *is* unverifiable from the docs is which of the
  underlying lines (vCPU, memory, disk, egress) the export actually shows for
  one short run.
- **Extras.** A boot disk (per GB-month, prorated), an in-use external IP if
  used (**charged**, §1.7), Cloud NAT if used, and the preloaded boot image's
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

- **Batch cancels with `jobs.cancel`, not `jobs.delete`.** The API has both,
  and they are not the same operation:
  - `jobs.cancel` is `POST /v1/{name=projects/*/locations/*/jobs/*}:cancel`,
    "Cancel a Job" (cloud.google.com/batch/docs/reference/rest/v1/projects.locations.jobs/cancel;
    cloud.google.com/batch/docs/cancel-job, both checked 2026-10-15). A job
    being cancelled passes through **`CANCELLATION_IN_PROGRESS`** and ends in
    **`CANCELLED`** — the two states that exist for exactly this call — and
    "like succeeded and failed jobs, a cancelled job remains in your project
    for up to 60 days" (cloud.google.com/batch/docs/cancel-job, checked
    2026-10-15). Its record survives, which is what `fugaro ls`, `diagnose`,
    `watch` and the sweeper read.
  - `jobs.delete` is a different method, and "when a job is deleted, the
    job's details and history that appear when you view a job and its tasks
    are removed from Batch"; Google then "automatically deletes a job 60 days
    after it is cancelled, succeeds, or fails"
    (cloud.google.com/batch/docs/delete-job, checked 2026-10-15). Deleting is
    therefore **destructive**: after it completes, `jobs.get` answers
    `NOT_FOUND`, and a `Cancel` implemented as a delete would make
    `backendtest`'s cancel case fail, because that suite reads the execution
    back and requires a terminal state with no error
    (`internal/backend/backendtest/backendtest.go`).

  **So `Cancel` is `jobs.cancel`**, and `jobs.delete` belongs only to the
  reaper and to cleanup of a job whose 60-day window should not be waited out
  (§3.4). C8 changes with it (§8.2).
- **Timeouts.** Batch gives a job `maxRunDuration` per task and `timeout` per
  runnable (Batch REST reference, checked 2026-10-15), and `maxRunDuration`
  bounds a task **once it is running**. Its ceiling is generous — 14 days in
  `RUNNING` before automatic failure — so Fugaro's own timeouts (a workflow's
  `timeouts.total` plus `backend.TaskTimeoutSlack`) bind long before it.
- **But nothing bounds the queue.** A job may sit in **`QUEUED` for 2 days**
  before Batch fails it (cloud.google.com/batch/quotas, checked 2026-10-15),
  and §1.8 names the likely cause: the project's regional CPU quota is what
  refuses the next concurrent run. Such a run is *pending*, not running: it
  counts against `max_parallel`, it shows as pending in `watch`, and it can
  start hours after the launcher's budget precheck and after the person who
  asked for it has stopped expecting it. `maxRunDuration` does not touch it.
  **Recommendation: a queue deadline**, checked in the same place the reaper
  and `fugaro watch` already look — a job that has not reached `SCHEDULED` or
  `RUNNING` within N minutes (a plan value; the natural default is the
  launcher's own task timeout) is cancelled with `jobs.cancel`, and the run
  record shows a `queue-timeout` outcome naming the wait, the quota-derived
  reason in the job's status message, and what to raise. Without it, "Fugaro's
  timeouts bind long before Google's" is false for the one state a user is
  most likely to be staring at.
- **A plain instance** cancels by `instances.delete`; the timeout is ours to
  enforce (a watchdog or the startup script's `timeout`), which is another
  small piece of machinery Batch already has.
- **Cancel semantics** must survive the race Cloud Run already has: a run can
  finish between the read and the cancel (live check 5), and cancelling an
  already-terminal Batch job must be tolerated as a terminal state, not an
  error.

**Substrate verdict.** Batch wins: timeouts and cancel are declarative.

### 1.5 Logs

The runner writes structured lines to stdout/stderr; on Cloud Run they land in
Cloud Logging and `fugaro logs` reads them through the installation's log
view (internal/backend/gcp/logs.go filters on
`labels."run.googleapis.com/execution_name"`). Two things must hold on a VM:

1. **The lines reach Cloud Logging.** Batch writes logs to Cloud Logging
   when a job's `logsPolicy.destination` is `CLOUD_LOGGING`; it is
   `DESTINATION_UNSPECIFIED` — "(Default) Logs are not preserved" — and must
   be set explicitly on an API-created job (Batch REST reference and
   cloud.google.com/batch/docs/analyze-job-using-logs, both checked
   2026-10-15). A job has **two log streams** under Cloud Logging:
   `batch_task_logs` ("logs for any data that you've configured a job's
   runnables to write to the standard output (`stdout`) stream or standard
   error (`stderr`) stream") and `batch_agent_logs` ("logs for activities from
   the Batch service agent", which "Batch automatically writes ... for every
   job that has enabled logging") (cloud.google.com/batch/docs/analyze-job-using-logs,
   checked 2026-10-15). Fugaro's lines — the runner's stdout/stderr — are
   `batch_task_logs`; `batch_agent_logs` is Batch's own account of scheduling
   and teardown and is **not** the run's log. Which of the two the Fugaro view
   should keep, and whether the agent stream is useful for `diagnose`, is a
   plan question; the default is to keep task logs only.
2. **The filter.** Google documents exactly one way to select one job's
   entries: **`labels.job_uid=JOB_UID`** (a *user label on the entry*, set to
   the job's **UID** — not its name — which means a `jobs.get` to learn it),
   optionally with `logName="projects/<project>/logs/batch_task_logs"` to
   select the stream
   (cloud.google.com/batch/docs/analyze-job-using-logs, checked 2026-10-15).
   The monitored resource `batch.googleapis.com/Job` also has a `job_id`
   label, documented only as "The identifier of the Job"
   (cloud.google.com/logging/docs/api/v2/resource-list, checked 2026-10-15) —
   **whether that is the job's name or its UID is UNVERIFIED**, so this
   design does not build `fugaro logs` on it; Check 57a step 10 reads a raw
   entry and settles it, after which the resource-based filter may be the
   cheaper one (no `jobs.get` first). The filter Fugaro ships is
   `labels.job_uid="<uid>" AND logName="projects/<p>/logs/batch_task_logs"`,
   pinned further by the Fugaro label where the entry carries it.
3. **They land in the installation's bucket, not `_Default`.** The
   installation's log sink filters on
   `resource.type="cloud_run_job" AND labels."fugaro"="managed"`
   (`deploy/terraform/gcp/modules/installation/logging.tf`); a Batch line's
   `resource.type` is `batch.googleapis.com/Job`, so the filter must gain a
   second arm (`resource.type="batch.googleapis.com/Job" AND
   labels."fugaro"="managed"`), and the matching exclusion from `_Default`
   too. The Fugaro labels do reach the entry — "custom labels to apply to the
   job and any Cloud Logging LogEntry that it generates" (`Job.labels`, Batch
   REST reference, checked 2026-10-15) — so the arm works, unlike the VM-label
   case of §2.4. Without it, `fugaro logs` (which reads the Fugaro view)
   finds nothing and the lines sit in `_Default` where every project viewer
   can read them — a worse outcome than the Cloud Run path. This is an
   installation-level Terraform change (§8).

A plain instance has no such integration: a startup script would have to ship
container output to Cloud Logging itself (the Ops Agent, or an explicit
`gcloud logging write`), which is exactly the machinery Batch supplies.

**Substrate verdict.** Batch wins.

### 1.6 Secrets

Cloud Run mounts a secret as an env var from a `secret_key_ref` in the job
definition (`modules/workflow/job.tf`). **Batch has the same idea, and the
design uses it.** A task's `Environment` has three fields, and one of them is
exactly Cloud Run's:

- **`secretVariables`** — "A map of environment variable names to Secret
  Manager secret names. The VM will access the named secrets to set the value
  of each environment variable" (Batch REST reference, `Environment`, checked
  2026-10-15). The job therefore stores **names, not values**, with the
  resolution done by the VM as the job's service account — so a reader of the
  job object (`batch.jobs.get`) learns which secrets a run uses but not their
  contents.
- `variables` — plain values, for the non-secret env of §2.1.
- `encryptedVariables` — an encrypted JSON blob, an alternative this design
  does not use.

So the **secrets bootstrap is not a component**: the VM backend writes the
workflow's secret names into `secretVariables` and the platform mounts them,
the way the Cloud Run job's `secret_key_ref` mount does. What remains is IAM:
the job's service account needs `secretAccessor` on **exactly** those secrets
(§3.2), and the launcher needs to be able to name them (§3.7). A runner-side
fetch from Secret Manager survives only as a fallback, for the cases the
declarative path cannot express — a secret whose name is only known at
run time, or a second secret materialised after `jobs.create` — and if a plan
task finds no such case, that task is dropped rather than built (Task 11).

- The VM (Batch-created) runs as a service account; omit the field and it is
  the default Compute Engine service account, set it and it is the one named
  (Batch REST reference, `AllocationPolicy.serviceAccount`, checked
  2026-10-15). Recommendation: **one service account per run class**, reusing
  the project-centric model — the job service account of
  (`profile`, `account`) (project-centric §4.1) — never the default Compute
  Engine service account, which is project-wide and broad. Whether that
  granularity is right is **C21** (§3.1), because it is what decides whether
  project-centric's D4 loss applies on this backend.

**Substrate verdict.** Batch wins on this axis: `secretVariables` is a
declarative mount, where a plain instance would need the runner-side bootstrap
this design has just deleted.

### 1.7 Networking

Requirements: **egress** to Cloud Logging, Secret Manager, the runs bucket,
Artifact Registry/ghcr.io, the git provider, the model providers (Anthropic,
Vertex, an OpenRouter-style endpoint) and package registries; **no inbound**.

- **External IP.** An external IPv4 address **is charged while it is in
  use** — $0.005/h on a standard VM and $0.0025/h on a spot VM
  (cloud.google.com/vpc/pricing-announce-external-ips, checked 2026-10-15; a
  static address costs more). It lets the VM reach the internet directly, and
  it puts the VM on the public internet: with no ingress firewall rule (the
  default VPC's `default-allow-ssh` is the thing to remove or avoid) nothing
  can reach it, but "no inbound" then rests on a firewall rule list rather
  than on the absence of a route. A Fugaro agent is untrusted code with a
  Docker daemon, so a mis-set rule is a reachable host, not a paper risk.
- **Cloud NAT.** No external IP on the VM; egress goes through a Cloud NAT
  gateway on its subnet (cloud.google.com/nat/docs/overview, checked
  2026-10-15). Nothing is reachable from outside at the network layer; the
  cost is a NAT gateway (hourly + per-GB processed) that exists for the
  installation, not per run, plus a router and subnet.
- **Private Google Access is not a substitute.** Batch documents that a job
  whose VMs have external access blocked "must use Cloud NAT or Private
  Google Access to allow access to the domains for the APIs and services that
  your job uses" — and Private Google Access reaches only Google's own APIs
  (Batch, Compute Engine, Logging), not GitHub or the model providers
  (cloud.google.com/batch/docs/job-without-external-access, checked
  2026-10-15). It can cheaply serve the Google half, but the egress Fugaro's
  runs actually need (git hosts, model providers, package registries)
  requires NAT, so NAT is what the design provisions and Private Google
  Access is at most an optimisation on top.
- **Batch's own fields.** `NetworkPolicy` and `InstancePolicyOrTemplate`
  accept a network interface and, with an instance template, the network
  settings can be pinned in the template (Batch REST reference, checked
  2026-10-15). The boot-image choice interacts here too: when a job's VMs have
  no external access and no NAT or Private Google Access, "only Batch images"
  are supported (cloud.google.com/batch/docs/vm-os-environment-overview,
  checked 2026-10-15) — one more reason the design keeps NAT rather than
  omitting egress entirely.

Recommendation: **no external IP, Cloud NAT for egress**, because it makes
"no inbound" a property of the network rather than of a firewall rule list,
and because the NAT is an installation-level one-time setup, matching the
"nothing per repository" goal. Trade-off: it is more setup than an external
IP and adds a per-GB egress charge (while the IP itself is not free either, so
the saving is smaller than it looks); a single-person installation that
prefers simplicity can take the external IP and a deny-all ingress firewall
instead (**C7**, §8.2).

**Substrate verdict.** Identical.

### 1.8 Quotas and limits

Verified from Google's public documentation (checked 2026-10-15):

- **Batch limits** (cloud.google.com/batch/quotas), which "cannot be
  changed": 100,000 tasks per task group; 5,000 parallel tasks per job;
  2,000 concurrent VMs per single-zone job (4,000 multi-zone); 1 MB job
  definition; 2 days `QUEUED` before automatic failure; 14 days `RUNNING`
  before automatic failure (7 days under `FLEX_START`).
- **Compute Engine allocation quotas**
  (cloud.google.com/compute/docs/resource-quotas, checked 2026-10-15): VM
  quotas are **regional**; the **VM instances** quota is regional and is
  automatically set to **ten times your regular CPU quota** ("You don't need
  to request this quota. If you need quota for more VM instances, request more
  CPUs"); **CPU quota** is regional (with per-family pools such as
  `CPUS`/`E2`), and there is additionally a global **CPUs (All Regions)**
  quota. Addresses split both ways: **"In-use IP addresses"** is the number of
  "static and ephemeral **regional** external IP addresses that you can use in
  your project simultaneously" — a VM's external IP is a regional address and
  counts there — while the *global* in-use-IP quota is for global addresses
  such as load balancers' (same page). Disks are regional. The *default
  numeric values* per project are not on that page and depend on the project's
  history — **UNVERIFIED here**; the owner reads the project's own quota page
  (Check 57a step 11 records the FACT).

What one run consumes: one VM, its vCPUs against the regional (and global)
CPU quota and one instance against the instances quota, one boot disk, and —
with NAT — no IP quota but NAT egress; with an external IP, one in-use IP
against the **regional** in-use-IP quota. A team running N concurrent Fugaro runs therefore
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
  creates it on first use); **`containerfilesystem.googleapis.com` if image
  streaming is used** (cloud.google.com/batch/docs/use-image-streaming,
  checked 2026-10-15); a Cloud NAT + router + subnet (§1.7); a preloaded boot
  image per `fugaro-base` version **or** reliance on image streaming — with
  the caveats §1.2 lists, i.e. a custom image Batch does not officially
  support and an image whose supported lifetime Google may end at 60 days from
  its starting date, which makes the preload a thing to **rebuild on a
  schedule**, not build once; the log sink/exclusion change (§1.5); and — with
  project-centric — a `profile` naming the VM class. There is **no per-run and
  no per-repository resource**: it is one-time, installation level, *except*
  the boot image, which recurs.
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

### 1.10 Decisions C1 to C11

| # | Decision | Recommendation | Veto alternative / trade-off |
|---|---|---|---|
| **C1** | Substrate | **Cloud Batch**, one job with one task per run, Container-Optimized OS boot disk, `fugaro-base` as a container runnable | **Plain Compute Engine instances** (full control of the startup script and the socket, no Batch container-runnable quirks; but Fugaro implements instance lifecycle, timeouts, log shipping and a reaper). **MIG**: rejected — homogeneous fleets, not per-run ephemeral VMs; you either pay for idle capacity or re-implement Batch. |
| **C2** | Where Docker comes from | **The VM's host daemon, via the bind-mounted socket** — noting that **image streaming disregards `options`** (§1.2), so with streaming (C4) the socket must be made usable by the image-baked group or by running the container as root, not by `--group-add` | **Rootless/Podman inside the container** or **privileged DinD**: same host-level power, more moving parts, doubles storage, needs an unverified `--privileged`/user-namespace path in a Batch runnable. |
| **C3** | Isolation unit | **One VM per run; a VM is never reused** | Reuse a pool of warm VMs: rejected — two runs on one VM can read each other's secrets and Docker state, and Docker makes each root on the host. |
| **C4** | Cold start | **Image streaming first** (Artifact Registry in-region, the `containerfilesystem` API, no custom image to maintain), **preloaded boot image as the fallback** | The preload as the primary choice was this design's first draft; it is demoted because Batch does not officially support custom images and a Batch image's supported lifetime can end **60 days** after its starting date, so it is an image to rebuild on Google's schedule as well as Fugaro's, and it costs the `options` field that one socket-permission path needs. Plain pull every run is the cheapest and slowest. **Check 57a steps 2 and 3 measure all three, and the recommendation follows the measurement.** |
| **C5** | Spot | **Off by default**, opt-in per profile | Spot by default: up to 91% cheaper, but a preemption kills the stage mid-flight and the 30 s notice makes checkpointing marginal. |
| **C6** | Service account | **One per run class (profile, account), reused from the project-centric model**; never the default Compute Engine SA | Default Compute Engine SA: project-wide and over-broad. |
| **C7** | Egress | **No external IP; Cloud NAT** | Ephemeral external IP + deny-all ingress: simpler, cheaper per run, but "no inbound" then rests on a firewall list. |
| **C8** | Timeouts and cancel | **`jobs.cancel` for cancel** (the job ends `CANCELLED` and its record survives), **`maxRunDuration` for the running timeout**, and **a queue deadline** for a job stuck in `QUEUED` behind the CPU quota | `jobs.delete` as cancel: destructive, erases the history `ls`/`diagnose`/`watch` read, and fails the conformance suite's cancel case. Fugaro's own watchdog (needed only on plain instances). |
| **C9** | Logs | **`logsPolicy.destination=CLOUD_LOGGING`**, and extend the installation's sink/exclusion to `batch.googleapis.com/Job` | Leave logs in `_Default`: `fugaro logs` finds nothing and every viewer reads them. |
| **C10** | Secrets | **The job's `secretVariables`**: names in the job definition, values resolved by the VM as the job's service account | A runner-side Secret Manager fetch as the primary path: more code for the same result, kept only as a fallback for a name known only at run time. Baking values in: rejected — anyone with `batch.jobs.get` could read them. |
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
(§2.2), the **name/parse and cloud-detection seam** (§2.5), the
**provisioning stage** (§4), the **cancel, queue-deadline and reaper
machinery** (§1.4, §3.4), and the **compute estimate** (§5.2). The secrets
bootstrap is *not* on this list any more: `secretVariables` does what Cloud
Run's `secret_key_ref` mount does (§1.6).

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
checked 2026-10-15); pass a supplementary group through the runnable's
`options` (**UNVERIFIED**, documented only as "arbitrary additional options",
e.g. `--network host` — and unavailable altogether if image streaming is used,
§1.2); or adjust the socket's group in the boot image. Check 57a step 6
records which works.

**Running as root is not free, and the earlier draft waved at it.** The base
image builds a `fugaro` user at **uid 1000** and runs as it
(`images/base/Dockerfile`); on a VM whose container runs as root, every file
the run creates in `/work/repo`, in the caches and in the mise store is owned
by root instead of that user. That changes what a *follow-up* run can do in
the same checkout, it makes the cache archives the runner uploads carry root
ownership, and it is a direct conflict with generic-tool's **D1** ("no root at
run time"). It is acceptable **only** because §3 already says the run is
root-equivalent on its VM whatever the container's uid — the socket makes
`--privileged` a single command away — so treating the uid as a boundary would
be theatre; but it must be a **recorded** decision, not a side effect of
choosing the socket. The cleanest ordering is therefore: (1) bake the socket's
`docker` group id into the image and add the container user to it, so nothing
else changes; (2) `--group-add` through `options` if Batch honours it; (3)
root, with the ownership consequence written into SECURITY.md. Whether the
`docker` gid on this backend's boot image matches anything the container can
join is **UNVERIFIED** — Check 57a step 6 records it.

Security consequence, repeated because it is the whole of §3: **a run that has
this socket is root on the VM.** The mount is not a privilege boundary and is
not treated as one.

### 2.3 The backend interface, mapped

Every method of `internal/backend.Backend` maps onto Batch:

| Seam method | Batch | Notes |
|---|---|---|
| `Launch(LaunchSpec)` | `projects.locations.jobs.create` | One Batch job, one task group, one task. Name derived per run (§2.4). The `FUGARO_RUN` env and the timeout are set in the job definition, not in an override — Batch has no execution-override concept like Cloud Run's `Overrides`. Returns `ExecutionRef{Name, Job, LogURL}`. |
| `Execution(name)` | `jobs.get` | Maps Batch's nine-value state enum to `backend.State`: `STATE_UNSPECIFIED`/`QUEUED`/`SCHEDULED` → `StatePending`, `RUNNING` → `StateRunning`, `SUCCEEDED` → `StateSucceeded`, `FAILED` → `StateFailed`, and `CANCELLATION_IN_PROGRESS`/`CANCELLED` → `StateCancelled`. `DELETION_IN_PROGRESS` → `StateCancelled` too, but it should only ever be seen for a job the **reaper** is deleting (§3.4) — a user cancel is `jobs.cancel`, which ends `CANCELLED` (§1.4). An unrecognised value is treated as **not terminal** and logged — the same defensive shape as `stateOf` in `internal/backend/gcp/run.go`, which reads Cloud Run's counts rather than trusting one field. `Created`/`Started`/`Completed` from the job's and its task's status times; `CPU`/`MemoryGiB` from the machine type; `Run` from the `FUGARO_RUN` the job was created with (Batch reports the job's `environmentVariables`, so unlike Cloud Run this one is always readable — still a hint, never evidence). |
| `List(filter)` | `jobs.list` | Filter by the Fugaro labels and `CreateTime`; the `fugaro-` name prefix is replaced by the label predicate. |
| `Logs(query, fn)` | Cloud Logging, through the same installation log view | Filter `labels.job_uid="<uid>" AND logName="projects/<p>/logs/batch_task_logs"`, the form Google documents for one job's task logs (cloud.google.com/batch/docs/analyze-job-using-logs, checked 2026-10-15; §1.5). It needs the job's **UID**, so `Logs` does a `jobs.get` first — one extra read per call, cached for the follow loop. The alternative `resource.labels.job_id` is **UNVERIFIED** (the resource list says only "The identifier of the Job", which may be the name or the UID), which is why it is not the shipped filter; Check 57a step 10 settles it and may allow the cheaper form later. The `fugaro=managed` marker does reach the entry (`Job.labels` applies to the job "and any Cloud Logging LogEntry that it generates") and pins the installation's arm of the sink (§1.5). |
| `Cancel(name)` | `jobs.cancel` | The job passes through `CANCELLATION_IN_PROGRESS` to `CANCELLED`; `Execution` keeps working afterwards, which is what `backendtest`'s cancel case requires. Tolerate a job already terminal (the same race live check 5 already documents for Cloud Run). `jobs.delete` is **not** `Cancel` — it is the reaper's and the restore's (§3.4). |
| `LongestTaskTimeout()` | The longest configured workflow timeout | See §2.5: on Batch there is no deployed job to read it from. |
| `TaskTimeout(slug, workflow)` | The workflow's configured timeout | Same: a config/policy read, `ErrNotFound` for an unknown workflow. |

Two properties make this work: a Batch job is created **per run** (there is no
reusable job definition to update), so `Launch` is a create rather than a
`run`; and a Batch job is **single-use** — once it reaches `SUCCEEDED` or
`FAILED` it cannot be run again — which matches Fugaro's "one execution per
run" exactly. `maxRetryCount` stays **0** ("The default, 0, which means never
retry", Batch REST reference, checked 2026-10-15): a Fugaro run is not
idempotent, the same reason Cloud Run's job sets `max_retries = 0`.

Two things the mapping adds that Cloud Run needed nothing for: **the job name
is not an execution name** (§2.5), and a job that never gets resources stays
`QUEUED` until the queue deadline cancels it (§1.4) — `StatePending` with a
reason, not a hang.

### 2.4 Naming, labels and adoption

The naming and marking conventions are the same as every other Fugaro
resource (`internal/backend/gcp/names.go`), so discovery and the guard keep
working:

- **Job id**: `fugaro-<slug>-<workflow>-<12 hex of the run id>`. The existing
  `JobName(slug, workflow)` (`internal/backend/gcp/names.go`) takes no run id,
  so this needs a **new deriving function** in the Batch backend, not a reuse
  of that one; the run id in it is what makes the name unique per
  project+region. Batch's own constraint on the id is documented and easy to
  satisfy: "at most 63 characters, must start with lowercase characters, only
  lowercase characters, numbers and `-` are accepted, and `-` cannot be the
  first or the last character" (Batch REST reference, `jobs.create`'s `jobId`,
  checked 2026-10-15) — the slug/workflow pair the existing scheme already
  sanitizes, plus 12 hex. The name is the identity the run stores in
  `launch.json`/`result.json` and that `fugaro logs`, `cancel` and `diagnose`
  follow.
- **Labels, in two places, because they do different things.** Batch's
  `Job.labels` are "custom labels to apply to the job and any Cloud Logging
  LogEntry that it generates", and **they do not reach the VMs**;
  `AllocationPolicy.labels` are "custom labels to apply to the job and all the
  Compute Engine resources that both are created by this allocation policy and
  support labels" (Batch REST reference, checked 2026-10-15). A design that
  sets only `Job.labels` leaves every VM unlabelled — and then the reaper and
  Check 57a step 8's
  `gcloud compute instances list --filter='labels.fugaro=managed'`
  find nothing, which reads as "no survivors" precisely when a VM *has*
  leaked. So the Fugaro set goes in **both**: `fugaro=managed`,
  `fugaro_repo=<slug>`, `fugaro_workflow=<variant>`, plus the per-run
  `fugaro_run=<slug>-<run id>`, sanitized to the label charset and truncated
  to 63 characters with a short hash of the run id when the slug and id
  together are too long (a plain truncation could collide two runs of a long
  slug, and the reaper's whole job is telling one run's VM from another's).
  That per-run label is what lets the reaper and a human find a leaked VM
  without reading every job. The Task-28 outline in the generic-tool plan
  already names "the labels used for adoption"; these are them.
- **Adoption** (`internal/infra/discover.go`, `ForeignError`) refuses to adopt
  a resource whose labels do not match exactly; a VM backend brings the same
  rule to Batch jobs, to Batch VMs and to any preloaded boot image (labelled
  `fugaro=managed`, like the base-image registry resources).
- **Finished jobs linger for 60 days.** Google deletes a job automatically
  "60 days after it is cancelled, succeeds, or fails"
  (cloud.google.com/batch/docs/delete-job, checked 2026-10-15), so
  `jobs.list` keeps returning finished jobs long after their VMs are gone.
  `List` therefore filters on the job's state and `CreateTime` rather than
  assuming the listing is all live work, and the reaper must not mistake a
  retained record for a live job (it deletes the job and, only for a job with
  no live record, an orphaned instance — §3.4).

**C13 (naming)**: reuse the Fugaro label set verbatim, in both label fields,
and add the per-run
label. Veto alternative: mark only the job and rely on the job id's prefix,
which is fragile for a VM that outlives its job.

### 2.5 The seam's Cloud-Run-shaped names (a required change)

`internal/backend` is not fully backend-neutral at its edges:
`ParseExecution`, `SameExecution`, `ExecutionFromEnv` and
`CheckRunExecution` all assume Cloud Run's
`projects/<p>/locations/<r>/jobs/<j>/executions/<e>` shape, and `OnCloudRun`
is literally "`CLOUD_RUN_EXECUTION` is set"
(`internal/backend/backend.go:86`). A Batch job's resource name is
`projects/<p>/locations/<r>/jobs/<j>` — six fields, not eight — so the two
cannot share a parser, and on a Batch VM `CLOUD_RUN_EXECUTION` is absent, so
`OnCloudRun` is false and the runner would treat itself as a local run.

**This is not cosmetic: four of those call sites are security checks that fail
open when `OnCloudRun` is false.** Every caller, read on `main`:

| Site | What it does | With `OnCloudRun` false (a Batch VM) |
|---|---|---|
| `internal/runner/gateway.go:288` (`routingKnob`) | refuses the routing-refusal escape knob (`FUGARO_ALLOW_ROUTING_SETTINGS`) on Cloud Run | **honoured**: a job env the launcher controls can switch the routing refusal off |
| `internal/cli/exec.go:195` (`RequireProject`) | requires the job to name its Fugaro project | **not required**: a run with no `FUGARO_PROJECT` proceeds |
| `internal/cli/exec.go:264` (`refuseOldJobEnv`) | refuses an old-style job env on Cloud Run | **skipped**: the old env is accepted |
| `internal/cli/exec.go:102` (`runner.BackendFromEnv`) | forbids a local (loopback) budget backend in the cloud | **allowed**: a run may point the budget at a local database |
| `internal/cli/exec.go:134` (`ExecutionFromEnv`) | the canonical execution name | `""`, so the run has no name to record or to compare |
| `internal/cli/skillwarn.go:43`, `internal/cli/updatewarn.go:155,186,260` | quiet the skill/update nudges in the cloud | nudges appear (cosmetic) |
| `internal/cli/imagecheck.go:548` | the image check's Cloud Run detection | already keyed on `CLOUD_RUN_JOB`/`CLOUD_RUN_EXECUTION`; needs the same treatment if the VM backend ever runs a check job |

**Recommendation (C12): generalise the seam, and make "in the cloud" fail
closed.** Move the name codec into each backend — a small interface
(`Parse(name)`, `Format(id)`, `FromEnv(env)`, `IsOwn(name)/ValidateRun(name,
slug, workflow)`) carried by the `Backend`, used by every caller instead of
the package-level functions — and replace `OnCloudRun` with a
**backend-supplied marker the runner can trust**: the platform's own value,
written by the launcher into the job definition (`FUGARO_BACKEND` already
exists for this and is set by `platformEnv`, `internal/infra/spec.go`), never
a variable a container can forge. `OnCloudRun` keeps its Cloud Run meaning for
the Cloud Run backend; the Batch backend answers "am I on you?" from its own
marker; and the **default when neither matches is "in the cloud"**
(`true`), so the four security checks above fail *closed* on an unrecognised
platform instead of open. That last part is the point: today the function's
name invites "am I on Cloud Run?" while every caller reads it as "am I
somewhere a launcher controls?", and the two are not the same question.

Trade-off: it touches every caller (`cli/cancel.go`, `logs.go`, `diagnose.go`,
`run.go`, `watch*`, `exec.go`, and the runner's `SameExecution` uses) and is a
change to the shared package, not a new sibling package — a bigger than
average first task, and it must land **before** any Batch backend can run a
container (Task 1 before Task 3, §8.1). Two corrections to the earlier draft
of this section: `CheckRunExecution` lives in **`internal/backend/gcp`**
(`run.go:92`), not in `internal/backend`, and it is called from
`internal/cli/cloud.go`; and the runner also uses `backend.ExecID` directly
(`internal/runner/runner.go:74`), which the codec has to keep working. Veto
alternative: keep the Cloud-Run shape and encode a Batch job as a synthetic
"execution" under it (`.../jobs/<fugaro job>/executions/<run id>`), which lies
in the stored data model and would confuse `fugaro ls`, `cancel` and the
sweeper the first time someone reads a raw name. **C12 is an owner decision by
size, not by uncertainty.**

**How the runner learns its own name on Batch (missing from the earlier
draft).** Cloud Run injects `CLOUD_RUN_EXECUTION`/`CLOUD_RUN_JOB`;
`FUGARO_GCP_PROJECT`/`FUGARO_REGION` come from the job definition. On Batch
none of the first two exist, so `Launch` writes the job's own resource name
into the job env (it is known before `jobs.create` returns, because Fugaro
chooses the id, §2.4). That value is **launcher-forgeable**, exactly like
`FUGARO_RUN`, so it is a hint — good enough to record, to show and to compare
against the backend's own answer, never evidence to act on
(`backend.Execution.Run`'s comment is the same rule). The backend's
`Execution(name)` is what a caller with a name to check should trust, and
`CheckRunExecution`'s job-binding rule carries over.

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

**A VM per run does not, by itself, undo that loss — and an earlier draft of
this section wrongly said it did.** What a VM adds over a shared job is
*kernel and Docker-state* isolation: separate VM, separate kernel, separate
containers, separate Docker store, no neighbour to reach at the OS level. What
it does **not** change is **IAM isolation**, and IAM is what D4 is about. With
C6's service account — the job account of (`profile`, `account`),
project-centric §4.1's own granularity — every repository on the profile runs
under the **same** account, so on the VM backend just as on a shared job a run
can read every object and secret that account reaches, poison another
repository's cache and forge its locks (project-centric §4.1: "Isolation
granularity is the profile … no IAM boundary between repositories sharing a
job"; the bucket condition collapses to "one condition on each prefix"). D4's
loss therefore applies **unchanged** here. Consistent with that, §3.2's grants
are the **profile's** prefixes, not `runs/<slug>/` alone.

**The real decision is the service account's granularity, and it is a new one
this backend creates.** Cloud Run could not vary a job's identity per
execution, which is *why* shared jobs exist; Batch can: every job names its own
`allocationPolicy.serviceAccount` and its own `secretVariables`, so a
**per-repository** service account is expressible on the VM backend — and
would restore 0.7.0's cross-repository bucket and secret isolation
(bucket-iam.md §3) for the projects that choose it.

- **Recommendation (C21): keep the per-(profile, account) account, and accept
  D4 unchanged.** The per-repository account is the per-repository IAM weight
  that project-centric exists to remove (one account, one set of grants, one
  bucket condition per repository), and an installation that picks the VM
  backend for one Docker-needing repository would pay that weight for every
  repository on the profile.
- **The alternative, and its cost:** a per-repository account **on the VM
  backend only**, opted into per repository. It buys back the 0.7.0 boundary
  for exactly the repositories that want it, at the price of a resource per
  repository — which is the weight project-centric's D-series removed, and it
  makes "which backend" a decision with a security consequence rather than a
  pure infrastructure one. If the owner takes it, §3.2's grants narrow to
  `runs/<slug>/`, `cache/<slug>/`, `locks/<slug>/` for that repository and the
  rest of the policy is unchanged.

Either way, **the document does not claim the VM backend sidesteps D4**, and
SECURITY.md says which granularity an installation has:

- **What a VM per run does give:** kernel and Docker-state isolation between
  runs, and the disappearance of the *shared job* as a thing (nothing is
  shared, so nothing has to be re-scoped). That is C3, and it is real — it is
  just not IAM.
- **And what it removes:** on Cloud Run the job is a container with no daemon
  and no root. On a VM the run is root on the host (§3.5). The isolation
  people must rely on moves from "the container" to "the VM", which is
  *stronger against other runs* and *weaker against the host* than Cloud Run.
  Both directions are stated in SECURITY.md.

### 3.2 The VM's service account (the run's identity)

- **Never the default Compute Engine service account.** Batch uses it when
  `allocationPolicy.serviceAccount` is omitted (Batch REST reference, checked
  2026-10-15), and Google's own guidance is blunt about what that means: the
  default "is granted the Editor role (`roles/editor`) by default" and "using
  the Editor role is **not recommended for production environments**"
  (cloud.google.com/batch/docs/get-started, checked 2026-10-15). Omitting the
  field is therefore forbidden here: it would make every run, and every
  container a run starts, an Editor on the project.
- **Which account, and its grants.** The job account of (`profile`,
  `account`) — project-centric §4.1's granularity, and the one the runner
  already binds to (`slug == task.Slug(...)`) — unless the owner takes C21's
  per-repository alternative. Its grants:
  - `objectUser` (with `objectViewer` where the launcher grants already exist,
    bucket-iam.md §11) conditioned by `gcp.BucketCondition` — on the
    **profile's** prefixes (`runs/`, `cache/`, `locks/` for the repositories it
    serves), not on one slug, because C21's recommendation keeps the profile
    account. §3.1 says exactly what that costs.
  - `secretAccessor` on the secrets its `secretVariables` name, and no others.
  - `logging.logWriter`, and **`batch.agentReporter`** — the role Batch's own
    documentation requires of a job's service account: "Batch Agent Reporter
    (`roles/batch.agentReporter`) on the project", alongside "Logs Writer
    (`roles/logging.logWriter`) on the project"
    (cloud.google.com/batch/docs/get-started, checked 2026-10-15). Without
    `agentReporter` the VM cannot report task state and the job cannot reach a
    terminal state, which would leave every run pending until the queue
    deadline fires.
  - `artifactregistry.reader` on the base image's registry (the same grant the
    image build already needs), and no project-level grants beyond the Vertex
    `aiplatform.user` a Vertex workflow already needs.
- **The service account's power is the blast radius.** Root on a VM is
  survivable only because the account under it is narrow: it can read its
  profile's objects and its workflows' secrets and nothing else. That bound,
  not the VM boundary, is the control that matters — which is why §3.1 refuses
  to call the VM backend "more isolated" without saying at what granularity.
- **One more Batch knob worth taking:** `allocationPolicy.instances[0]
  .blockProjectSshKeys` ("Set this field to `true` if you want Batch to block
  project-level SSH keys from accessing this job's VMs", Batch REST reference,
  checked 2026-10-15). Nobody should SSH into a run's VM — the design is
  API-only and the VMs have no external IP (§1.7) — so the project's SSH keys
  are blocked from the fleet rather than left as a back door that bypasses
  everything above. UNVERIFIED: whether Batch also leaves its own OS Login or
  serial-console path open (Check 57a step 9 records the fleet's bindings, and a
  VPC-SC-free sandbox project shows what else reaches them).

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
secrets and SA token). Batch is expected to do most of this for free — a
finished or deleted job takes its VMs with it — but the design does not rely
on that alone, because "most exit paths" is not "every exit path" and because
the teardown is assumed rather than read:

- **Success, failure, cancelled:** Batch job terminal states. `SUCCEEDED` and
  `FAILED` are the job's own; a **cancel** runs `jobs.cancel`, which takes the
  job through `CANCELLATION_IN_PROGRESS` to `CANCELLED` (§1.4) and leaves its
  record in place for 60 days, which is what `fugaro ls`, `diagnose` and
  `watch` need. **Deleting a job erases its history**
  (cloud.google.com/batch/docs/delete-job, checked 2026-10-15), so
  `jobs.delete` appears here only as the reaper's and the restore's tool,
  never as `Cancel`. The VMs are expected to be torn down by the service —
  that teardown is assumed, not documented on the pages read, and Check 57a
  step 8 measures it.
- **A crashed runner:** if `fugaro exec` exits non-zero or panics, the task
  fails, the job fails, the VMs go — provided `maxRetryCount` stays 0 (§2.3),
  since a retry would start a second run of a non-idempotent task.
- **A hung job:** Batch itself fails a job that runs longer than 14 days
  (cloud.google.com/batch/quotas, checked 2026-10-15), but Fugaro's own
  `maxRunDuration` binds first and is far shorter.
- **A job stuck in `QUEUED`:** the queue deadline of §1.4 cancels it, so it
  does not sit against `max_parallel` for two days.
- **The residual, and the reaper.** A stuck `DELETION_IN_PROGRESS`, an orphaned
  VM after a Batch-side fault, or (on the plain-instance alternative) a
  startup script that never ran: for these the design keeps **a reaper** — the
  existing scheduled sweeper pattern (project-centric §8, `history.tf`)
  extended to Batch jobs and Compute Engine instances.

  **Its order matters, and it is job-first.** The reaper deletes the *job*
  (`jobs.delete`, which needs `batch.jobsEditor`/`batch.jobs.delete`, §3.7)
  and only then looks for an instance with no live job behind it, because
  deleting a Batch-created instance underneath its own job races Batch's
  management: Batch is mid-teardown, sees an instance vanish, and reports the
  task as failed for a reason that has nothing to do with the run — or, worse,
  leaves the job in a state Fugaro then misreads as a real failure. The
  instance sweep is the *second* pass, for an instance whose job is already
  gone (a `fugaro=managed` VM with no `fugaro_run` job in `jobs.list`), and it
  deletes that instance directly with `compute.instances.delete`
  (`compute.instanceAdmin.v1`). Age is the other filter: older than the
  longest workflow timeout plus `backend.TaskTimeoutSlack` plus a margin — and
  on this backend also older than a plausible **queue** wait, or a job that is
  legitimately still waiting gets deleted by its own reaper.
  Batch's optional Pub/Sub job-state notifications (Batch REST reference,
  checked 2026-10-15) are a cheaper trigger than polling and are a plan
  option. **C14**: keep a reaper even though Batch mostly self-cleans; the
  veto alternative is to rely on Batch teardown alone and accept a rare
  orphan, which the owner may prefer to save the machinery — the trade-off is
  an occasional VM billing until someone notices. What it costs is in §3.7:
  project-wide `batch.jobs.delete` and `compute.instances.delete`.

### 3.5 What a Docker-capable agent can do to the host

Stated plainly, for SECURITY.md:

- **Root on the VM.** Through the socket, `docker run --privileged -v /:/host`
  gives the agent the VM's filesystem, its SA token, and its network. It can
  read the runner's own environment — including the secrets `secretVariables`
  mounted and the model-provider keys — but it holds those anyway, in-process,
  so this is not a new secret exposure *within* the run.
- **It can outlive its own runner.** Root can stop or kill the Batch agent's
  reporting on the VM, and the VM keeps billing until `maxRunDuration` or the
  reaper ends it; it can also leave containers running that no Fugaro code
  knows about. Both are reasons the VM is the unit that gets reaped, not the
  runner process.
- **It can spend money past the caps.** The gateway's per-run and daily caps
  are enforced by `fugaro exec` in-process (m9 §4–§5) and count **model**
  dollars; an agent that reads the provider key out of its own `/proc/<pid>/
  environ` (or out of the environment `secretVariables` set) can call the
  provider directly, off the gateway, and nothing halts it. SECURITY.md
  already documents this residual ("What one run's agent can reach", "Money")
  for the Cloud Run path; on a VM it is unchanged and no worse, except that
  the VM's own compute keeps billing after the runner is gone.
- **What it reaches, on this backend:** the run's service account (whatever
  C21's granularity gives it, §3.1 — **not** necessarily one slug), the secrets
  its workflow mounts, its own run's bucket objects, and (with the log sink
  fixed, §1.5) no other run's logs. The SA's scope is the control that matters;
  it is **not** per-slug unless the owner takes C21's alternative, and saying
  "per-slug" here would contradict §3.1.
- **Not the neighbouring run.** Separate VM, separate kernel, separate
  process, separate Docker store: nothing in one run reaches another at the OS
  level. IAM is the exception §3.1 states.
- **Every container it starts can reach the metadata server** (§3.3) and,
  through the host daemon, the host's own network namespace: `--network none`
  is the agent's choice to make, not Fugaro's.
- **Egress is open**, exactly as on Cloud Run (generic-tool §9): the agent can
  send whatever it can read to anywhere the network allows. Cloud NAT does not
  change this; it only removes inbound reachability.

### 3.6 SECURITY.md additions

The VM backend adds a section to SECURITY.md stating: the run is root on its
VM; the VM is the isolation unit and is never reused; the run's service account,
its granularity (C21: the profile's account unless the installation opts into
per-repository ones) and what D4's accepted loss means here; that the metadata
server is reachable by the agent and by every container it starts; that root
can outlive the runner and keep spending; that the caps are model-only and
bypassable by an agent that holds the provider key; that a leaked VM is reaped;
and that the egress posture is unchanged. Veto alternative: leave SECURITY.md
and let the design document carry it — rejected, because a trust model an
adopter cannot find in SECURITY.md is not a trust model (the same ruling the
generic-tool work made, G21).

### 3.7 IAM: the launcher, the job and the reaper

Batch's IAM model is **coarser than Cloud Run's** for the launching side, and
that is a trust-model change worth stating rather than discovering in a
provisioning PR. Google documents what a job creator needs: to create a job
that names a custom service account, "Service Account User
(`roles/iam.serviceAccountUser`)" **on that service account**, plus "Batch Job
Editor (`roles/batch.jobsEditor`)" **on the project**
(cloud.google.com/batch/docs/create-run-job-custom-service-account, checked
2026-10-15); the job's account then needs `batch.agentReporter` and
`logging.logWriter` (§3.2). The job-level roles are the same project-level
roles a cancel needs ("Batch Job Editor (`roles/batch.jobsEditor`) on the
project", cloud.google.com/batch/docs/cancel-job, checked 2026-10-15), and a
delete needs them too (cloud.google.com/batch/docs/delete-job, checked
2026-10-15).

What follows, plainly:

- **A launcher with `batch.jobsEditor` on the project can create a job with
  any image, any command and any `secretVariables`** it can name, running as
  the service account it has `serviceAccountUser` on. It is not confined to
  Fugaro's repositories, workflows or images. Today's Cloud Run path is
  narrower: the launcher calls `jobs.runWithOverrides` on **one named job**
  per (repository, workflow), so the image, the command and the environment
  are the job's, not the caller's.
- **The same launcher can cancel or delete any Batch job in the project**,
  including another launcher's, another repository's or a teammate's run.
- So **the launcher is trusted to a greater degree than before**, which is a
  different claim from project-centric D10's "launchers are trusted" — D10
  trusted them with their own repositories' runs, and this trusts them with
  everyone's plus arbitrary compute under a job account.
- **The reaper's identity** needs project-wide `batch.jobs.delete` and
  `compute.instances.delete` (`compute.instanceAdmin.v1`), i.e. the most
  destructive grants in the whole design. It should be a **dedicated
  service account** used only by the scheduled sweeper, not the launcher's and
  not the job's.

**Recommendation (C22): give the launcher `batch.jobsEditor`,
`serviceAccountUser` scoped to the run-class accounts, and nothing else; if
the project can carry it, put the launcher in its own account so "who can
launch" and "who can cancel anything" stay distinguishable; and make the
reaper a separate account.** The stronger alternative — an IAM Condition
narrowing `batch.jobsEditor` to Fugaro-shaped job names, and a
`serviceAccountUser` only on `fugaro-*` accounts — is worth attempting at
provisioning time: Conditions on `batch.googleapis.com/Job` resource names
are **UNVERIFIED** (Check 57a step 9 reads what the sandbox's IAM actually
allows, and the plan task checks it before shipping). Veto alternative: one
account for everything, simpler to provision and much harder to reason about —
rejected, because it makes the reaper's power ambient.

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
pay for a VM or expose the socket. generic-tool §1 proposed the shape for this:
a workflow key `docker: true` (scopes: project via a profile, and repository).
It was a **conditional proposal, not a decision** — it sits under "**If gen-2
is suitable**", whose branch the owner's measurement has since closed
(generic-tool §1; Task 28's restore). So the name is this design's to choose,
and **Recommendation (C16): adopt `docker: true` anyway**, because it names the
capability rather than the mechanism, and because a second name for one idea
(`runtime: docker`) is worse than a name inherited from a branch that did not
happen.

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
billed duration × (vCPU × price per vCPU-second + GiB × price per GiB-second)).
**The earlier draft described a single estimate computed from the workflow's
requested resources; the code computes it in two places, and neither survives
a VM unchanged:**

1. **In the runner, live** (`internal/runner/cost.go:55-66`): with a gateway
   up, `r.d.Prices.ComputeUSD(r.wf.Resources.CPU, MemoryGiB(
   r.wf.Resources.Memory), r.d.Now().Sub(r.rec.StartedAt))` — **the workflow's
   requested** vCPU/memory, from the record's `StartedAt` (the runner's own
   first line). The prices come from `FUGARO_COMPUTE_PRICES`
   (`internal/runner/prices.go`), which the job env carries from the local
   config's `compute_prices`, so the runner's figure matches `fugaro ls`'s.
2. **In the CLI** (`internal/runview/runview.go:351-363`): when the backend
   reports an execution, `prices(region).ComputeUSD(e.CPU, e.MemoryGiB,
   e.Billed(now))`, where `region` is `backend.ParseExecution(e.Name)`'s — and
   `e.CPU`/`e.MemoryGiB` are the **execution's own limits** as the backend
   reports them.

On a Batch VM both break, in three ways, not one:

- **The region does not parse.** A Batch job name has six fields, so
  `ParseExecution` returns `ok == false`, `region` stays `""` and the CLI
  prices the run against whatever the empty-region table is. The fix belongs
  with C12's codec: each backend resolves its own region, and `runview` asks
  the backend rather than parsing the name.
- **The billed window is wrong at the front.** A VM is billed from **boot**,
  and boot, Batch's own setup and a possible 3.8 GB pull (§1.2) all happen
  before the runner's first line and before the task's start time. Neither
  estimate counts them, and on a slow cold start they can be most of a short
  run's cost. The VM estimate therefore starts its window at the job's
  `SCHEDULED` (or the VM's creation) time, not at `StartedAt`, and says so in
  the source line.
- **The quantities are the machine type's, not the workflow's.** Compute bills
  the VM: an `e2-standard-4` is 4 vCPU / 16 GiB whatever `resources:` says. So
  the runner-side estimate (which uses `wf.Resources`) is replaced on this
  backend by the **machine type's** vCPU and memory — which is what
  `Execution.CPU`/`MemoryGiB` already carry on the Cloud Run side, and what
  Batch's `Execution` maps to (§2.3). The local config's `compute_prices`
  (`FUGARO_COMPUTE_PRICES`) is then seeded from the **Batch price table** for
  the installation's region, so the runner and the CLI still agree.

So the estimate's shape is unchanged (duration × vCPU × price + duration × GiB
× price), and what changes is which duration and which vCPU/GiB go into it.
On top of that, two Compute Engine facts (cloud.google.com/compute/pricing,
checked 2026-10-15):

- **A one-minute minimum.** "All vCPUs, GPUs, and GB of memory are charged a
  minimum of 1 minute." The estimate floors the billed duration at 60 s, or
  every short run is under-reported. `Execution.Billed` keeps returning the raw
  duration — other callers use it for other purposes — and the floor is
  applied where the VM backend computes `compute_usd`.
- **A different price table, plus extras.** Compute Engine vCPU/GiB list prices
  differ from Cloud Run's per region, and a run also consumes a boot disk and
  (with C7) Cloud NAT. Recommendation (C17): `compute_usd` covers the VM's
  vCPU/memory — the same shape as today, so `report` and the history need no
  new column — and the disk/NAT costs are **named as not in the figure** in
  the report's source line, because pretending they are in the per-run figure
  would be a worse lie than naming them out. The exact list prices are
  UNVERIFIED here and belong in the backend's price table with its own
  `Source` string and check date, the way Cloud Run's does. `Batch` itself
  adds nothing (§1.3).

**Compute stays reported, never capped** (m9 D7): the per-run and daily caps
are model dollars only. This matters more on a VM, where a Docker-heavy run
can be long and compute-heavy — and it matters twice as much because the
estimate is now a *floor* on real spend (boot and pull are in the bill and,
until the window is fixed above, not in the figure): the caps will not stop a
run that is only burning VM time, and §3.5 says an agent can spend model money
off the gateway entirely. The design keeps the m9 rule rather than inventing a
compute cap, and says so in SECURITY.md. The daily history's `computeMicros`
(m9 §549, §776) carries the VM estimate unchanged in shape.

**Check 57b step 5 compares the estimate with the billing export** for one
real run, which is the only thing that settles the three corrections above.

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
backend creates a fresh job per run, so it never needs to share — but **it
does not sidestep the isolation loss D4 accepted**: IAM isolation follows the
service account, and C6's account is the profile's, so the loss applies on the
VM backend exactly as it does on a shared job (§3.1, C21). What it removes is
the *shared job*, and with it the reason Cloud Run had to share. Consequences:

- **Before phase 3**, a VM backend uses the current model: a per-repository
  job service account and an installation-level Batch stage. It works, but it
  carries the per-repository SA weight project-centric is removing.
- **After phase 3**, the same backend uses a project job service account per
  (`profile`, `account`) and profiles select the VM class (§4.3) — the intended
  end state.
- The **seam generalisation (C12)** is independent of both and can land first.
- Recommendation: the plan's first cloud-free task is the conformance suite
  against a fake (§8.1, task 2), which needs neither phase 3 nor any cloud, and
  the live measurement (Check 57a) gates the rest.

### 6.3 Out of scope

- **Docker on Cloud Run** (gen-1 or gen-2): measured impossible by the owner;
  the gen-2 probe (Task 26) and Docker-on-gen-2 (Task 27) are dropped.
- **Rootless Docker / Podman**: not the chosen mechanism (§1.1); revisitable
  only if the socket path fails Check 57a's permission step.
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
gen-2 Docker probe (generic-tool Task 26; the plan numbered it Check 31, but
that number belongs to `fugaro upgrade` in the live checklist), in the format
of Checks 30 and 32.

### 7.1 The number

`docs/gcp-live-checklist.md` reserves a number once and never reuses it. On
`main`, checks 1–44 have rows and the provider-price-catalog design's paragraph
names 45 as "the next free number" — true when that paragraph was written.
Since then the numbers have been claimed by plans that have not landed here:
**45 and 46 by the OpenRouter senior-reviewer plan (PR #359), 47 by the
subscription-OAuth-token plan (PR #363, merged as `ba03ab5` — its row *is* in
the table), and 48–56 by the project-centric plan (PR #366)**; a number reserved in a plan is reserved
whether or not its row is written, which is why nothing sits between 47 and 57
today. So this design claims **Check 57**, and its row and its reservation
paragraph are additive. If those reservations turn out not to exist, the number
is still valid — the ruling is "never reuse or renumber", not "always use the
lowest". The narrow escape is
not repeated either: the plan this check comes from numbered the dropped probe
Check 31, which the registry had already given to `fugaro upgrade`, so this one
reads the registry first (§9).

### 7.2 The check

**Check 57: the compute-instance Docker backend (sandbox only, run by you;
USER-RUN, NOT RUN).**

Nothing below has been run; nothing here is a claim that it works. It measures
what the steps below measure. **Sandbox installation only** — the owner's `fugaro`
installation and its sandbox repository, the same one Checks 40, 41, 43 and 44
use. **NEVER the `belong` project.** **No agent runs any apply:** every
`gcloud`/`fugaro` step is typed by the owner.

**Two parts, and the first one gates the build.** An earlier draft of this
section was one list of ten steps whose first two ran `fugaro logs`/`fugaro
cancel` against the Batch backend and whose row nevertheless said it "gates the
build" — a check that needs the thing it gates, written as the last task of the
plan. It is split:

- **57a — the pre-build probe.** Eleven steps, **`gcloud` and a committed job
  JSON, no Fugaro change whatsoever**, runnable today against the sandbox
  project (steps 1, 5 and 8 use the *existing* Cloud Run backend for their
  baseline and their comparison). It is the design's **go/no-go**, and the plan
  makes it Task 0 (§8.1): if it fails, nothing below it is built.
- **57b — the post-build acceptance run.** Six steps that only make sense once
  `backend: cloud-batch` can be enabled, i.e. after the last task of §8.1. They
  are the check's *acceptance* half, and the design's §4.4 `doctor` gate is what
  stops anyone running them early.

The job JSON the probe submits is **committed with this design**, so 57a is
reproducible and reviewable rather than improvised at the terminal (the
design's own text is the source; the plan task writes it to
`check57/job.json` in the sandbox checkout):

```json
{
  "taskGroups": [{
    "taskCount": "1",
    "parallelism": "1",
    "taskSpec": {
      "runnables": [{
        "container": {
          "imageUri": "us-east5-docker.pkg.dev/fugaro-dev/fugaro/fugaro-base:0.7.0",
          "commands": ["bash", "-lc", "docker run --rm docker.io/library/postgres:17 postgres --version && id && echo first-line"]
        }
      }],
      "computeResource": {"cpuMilli": "2000", "memoryMib": "4096"},
      "maxRetryCount": 0,
      "maxRunDuration": "1200s"
    }
  }],
  "allocationPolicy": {
    "instances": [{
      "policy": {"machineType": "e2-standard-4", "bootDisk": {"image": "batch-cos-stable", "sizeGb": "50"}},
      "installGpuDrivers": false
    }],
    "labels": {"fugaro": "managed", "fugaro_chk": "57"}
  },
  "labels": {"fugaro": "managed", "fugaro_chk": "57"},
  "logsPolicy": {"destination": "CLOUD_LOGGING"}
}
```

(The `imageUri` is the sandbox's own base image; steps 2 and 3 vary the boot
disk and the `imageUri` and nothing else. `machineType`, the regions and the
account are the check's variables, named in the setup block below.)

**Guarded setup block (every step re-runs it; it hard-stops otherwise).** The
shape is Check 43's, because the traps are Check 43's: an empty `$P` makes
`gcloud` fall back to its ambient `core/project` — for the owner a `*-prod`
project — while `fugaro` falls back to the checkout's `fugaro.yaml`
(`internal/cli/cloud.go:53`), and this check **creates and deletes cloud
resources**.

```bash
# === Check 57: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every
# step (57a-1..57a-10, 57b-1..57b-5, Restore). It is idempotent, and nothing
# below the three hard stops runs unless all three pass. ===
P=fugaro-dev                 # HARD-CODED sandbox project id; never belong, and never read from gcloud
L=us-east5                   # the sandbox installation's region
SA=fugaro-chk57              # the account THIS CHECK creates; NOT the installation's job account
J=fugaro-chk57               # the job-name prefix THIS CHECK uses; not the `fugaro-<slug>-<workflow>` shape
IMG=${L}-docker.pkg.dev/${P}/fugaro/fugaro-base:0.7.0   # the sandbox's base image, for 57a's job JSON

# ${VAR:?} everywhere: an empty variable is a hard error, never an empty
# argument gcloud would resolve from its ambient configuration.
[ "$P" = fugaro-dev ] && [ -n "$L" ] && [ -n "$SA" ] && [ -n "$J" ] && [ -n "$IMG" ] \
  || { echo "Check 57 stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L SA=$SA J=$J IMG=$IMG must not be empty)" >&2; exit 1; }
fugaro doctor --project fugaro --gcp-project "${P:?}" | grep -q '^project: .*(GCP fugaro-dev,' \
  || { echo "Check 57 stopped: this is not the fugaro-dev installation (fugaro doctor's project: line is not fugaro-dev, or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)" >&2; exit 1; }
git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$' \
  || { echo "Check 57 stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)" >&2; exit 1; }
```

**Every `gcloud` command gets `--project="${P:?}"`** (and `${L:?}` where it
takes a location). **Every `fugaro` command that takes them gets both
`--project fugaro` and `--gcp-project "${P:?}"`** — the first is the
installation's *name*, the second the project ID, and `--gcp-project` must be
the selected config's own `gcp_project` (`internal/cli/cloud.go:54`), so the
pair pins the command to this installation and refuses a foreign checkout with
`this checkout belongs to project <x>; --project says fugaro`
(`internal/localcfg/select.go:163`). `fugaro version` is the one exception: it
takes no flags (`internal/cli/root.go:82-88`).

**Paste the block into a file, not straight into an interactive zsh.** As in
Check 43: an interactive zsh does not honour `#` comments without
`setopt interactive_comments`, and a block whose comments become arguments is a
block whose `P=fugaro-dev # …` line leaves `P` unset — which the first hard
stop catches before anything reaches `gcloud`, but the run is over. Save it and
run `bash check57.sh`.

**Why the account is the check's own.** `SA` is created by this check and
deleted by its Restore. It must **not** be the installation's job account
(`fugaro-<profile>-<account>`, §3.2) or the default Compute Engine SA: the
restore below deletes `$SA`, and deleting an account the installation uses
would break the installation this check is meant to leave untouched. The
`fugaro_chk=57` label is on the job and on `allocationPolicy.labels` for the
same reason (§2.4): it is what the Restore's assertions list, and it is how a
leaked VM of *this check* is told from a real run's.

### 57a. The pre-build probe (no Fugaro change; the go/no-go)

1. **Baseline (Cloud Run).** On the sandbox, `fugaro run --project fugaro
   --gcp-project "${P:?}"` a no-change task and time launch-to-first-log-line
   (`fugaro logs RUN`, the first `started` line), five times. This is Check 41's
   measurement, reused. **FACT:** the five times and their median.
2. **Cold start, plain pull.** Submit five fresh `$J` jobs from the committed
   JSON, each with its own per-run label (§2.4) and its own run id in the name
   (`gcloud batch jobs submit "${J:?}-$(date +%s)-$i" --project="${P:?}"
   --location="${L:?}" --config check57/job.json`), and record
   submission-to-first-line for each. A Batch job is single-use once it reaches
   `SUCCEEDED` or `FAILED` (§2.3), so five samples are five jobs — not five
   submissions of one, which is what an earlier draft of this check said.
   **FACT:** the five times, the median, and the delta over step 1.
3. **Cold start, the two alternatives.** (a) **Image streaming**: the same job
   with `enableImageStreaming: true` on the runnable, the `containerfilesystem`
   API enabled and the image in the job's own region (both prerequisites are on
   the design's list, §1.2). (b) **The preloaded boot disk**: a boot image with
   `fugaro-base` already in the Docker data root, built by the check as
   `${SA:?}-preload` and deleted by the Restore. Five fresh jobs each, as in
   step 2. **FACT:** the five times, the median and the delta over steps 1 and 2
   for **each**, whether streaming accepted the job at all (it is documented as
   ignoring `options`, §1.2, so this also says whether it is compatible with the
   socket path of step 6), and whether the custom boot image was accepted — an
   image Batch "might not be supported" is exactly the question. **The
   recommendation for C4 follows these two numbers, not this document** (§1.2,
   §1.9).
4. **Docker inside a run.** In a `$J` job, `docker run --rm
   docker.io/library/postgres:17 postgres --version`, then start a small
   Postgres container and wait for `pg_isready`. **FACT:** whether the socket
   was reachable; which permission path let the container open it (container as
   root, a supplementary group via the runnable's `options`, or a socket group
   baked into the boot image); the pull time; and the time to `pg_isready`.
5. **The cost of one run.** Read the billing export (or the console's cost
   table) for one step-2 job and compare it with what the run's own record says
   (`cost.compute_usd` on a real run, 57b-5; on the probe, compute the estimate
   by hand from the machine type and the elapsed time). **FACT:** the actual
   billed vCPU/memory seconds, the disk, the in-use external IP if the job has
   one, NAT if used, the estimate, and how much the **one-minute minimum** added
   (a Compute Engine instance bills a one-minute minimum and then per second,
   §5.2).
6. **The socket's permission path, in detail.** Record whether a Batch
   container runnable's `options` accepts `--group-add`, `--user` and
   `--privileged`, and which of them the design needs. **FACT** for each. This
   is the step that decides between §2.2's three orderings, and it is why
   streaming (which discards `options`) may not be combinable with the
   `--group-add` path.
7. **(Optional) Preemption.** Run one `$J` job with a spot provisioning model
   and preempt it (or let it happen). The notice period is 0 seconds by default,
   or 120 seconds if the job asks for it, and the shutdown afterwards is best
   effort and can take up to 30 seconds (MIG/spot docs, §1.3). **FACT:** whether
   the job asked for the 120-second notice, whether the runner received SIGTERM,
   whether it reached `finalize`, and the time from the preemption notice to the
   VM's death.
8. **Teardown on every exit path.** For each of: success; failure (a runnable
   that exits non-zero); **cancel** (`gcloud batch jobs cancel "${NAME:?}"
   --project="${P:?}" --location="${L:?}"` — `jobs.cancel`, **not**
   `jobs.delete`: delete erases the job and its history, which is why §1.4 makes
   cancel a `cancel` and the reaper a `delete`); and a crashed runner (kill the
   container's process) — **a fresh job each, since one job runs once** — list
   the surviving resources:

   ```bash
   gcloud compute instances list --project="${P:?}" --filter='labels.fugaro_chk=57'
   gcloud batch jobs list --project="${P:?}" --location="${L:?}" --filter='labels.fugaro_chk=57'
   ```

   The instance filter is `labels.fugaro_chk=57`, **not** `labels.fugaro=managed`
   and not `job_id`: `Job.labels` never reach a VM, only
   `allocationPolicy.labels` do (§2.4, Batch REST reference, checked
   2026-10-15), so a design or a check that sets only `Job.labels` lists
   nothing and reads it as "no survivors" precisely when a VM has leaked.
   **FACT** per path: the job's final state (and, for cancel, whether
   `Execution` after the cancel returned a terminal state rather than
   not-found — §1.4), whether any VM survived, and for how long after the job
   reached its terminal state. This is the answer to "does the reaper delete
   the instance" for every way a run can end, and it is also what tells whether
   the job's own teardown is enough (§3.4, C14).
9. **IAM, read raw.** Read the sandbox's IAM and answer, with the actual
   bindings: what a **launcher** needs to create a `$J` job (the design says
   `roles/batch.jobsEditor` on the project plus `roles/iam.serviceAccountUser`
   on the job's account, §3.7); what the **job's account** needs to run and to
   report (the design says `roles/batch.agentReporter` plus
   `roles/logging.logWriter`, §3.2); what the **reaper** needs to delete a job
   and an instance; and whether an IAM Condition narrowing `batch.jobsEditor`
   to `fugaro-*` job names is **accepted at all** — that is UNVERIFIED and this
   step settles it. **FACT:** each binding, verbatim, and whether the Condition
   was accepted.

   ```bash
   gcloud projects get-iam-policy "${P:?}" --format=json | \
     python3 -c 'import json,sys; [print(b["role"], b["members"]) for b in json.load(sys.stdin)["bindings"]]'
   ```
10. **The log filter, read raw.** Read one raw entry of a `$J` job and record
    which label identifies the job:

    ```bash
    gcloud logging read 'resource.type="batch.googleapis.com/Job"' --project="${P:?}" --limit=1 --format=json
    ```

    **FACT:** whether `labels.job_uid` is present and equals the job's UID
    (`gcloud batch jobs describe … --format='value(uid)'`), whether
    `resource.labels.job_id` is present, and — the part that is **UNVERIFIED** —
    whether that value is the job's *name* or its *UID*; the resource list says
    only "The identifier of the Job" (checked 2026-10-15). The shipped filter is
    the documented `labels.job_uid=… AND
    logName="projects/<p>/logs/batch_task_logs"` (§1.5); this step is what would
    allow the cheaper `resource.labels.job_id` form later. Also record whether
    the job's own `labels.fugaro_chk=57` reaches the `LogEntry` (it should:
    `Job.labels` apply to the job "and any Cloud Logging LogEntry that it
    generates"), and whether the **sink/exclusion arm** of §1.5 catches the
    entry — apply it if the owner wants the answer now, and revert it in the
    Restore if so.
11. **(Optional, quota.) Queueing, and what a queued job does.** Submit one
    `$J` job that asks for more vCPUs than the sandbox region's quota leaves
    (or fill the quota with step-2 jobs) and watch it: the state should be
    `QUEUED`, and Batch holds a job in `QUEUED` for at most **2 days** before
    failing it (`cloud.google.com/batch/quotas`, §1.4). **FACT:** the state
    observed, how long it sat there, and what the API said. Batch has no
    queue-deadline *field* — the design's queue deadline (§1.4, C8) is a
    Fugaro-side reaper action, so this step measures the state it acts on, not
    a Google knob. Cancel it and continue.

### 57b. The post-build acceptance run (after the enable task)

These six need `backend: cloud-batch` selectable, so they run **last**
(§8.1's task 9 is the enable task and task 10 is this section's second half).
They are the design's acceptance criteria, not its go/no-go.

1. **A real run with Docker.** `fugaro run` on the sandbox with the backend
   set to `cloud-batch` and the workflow's `docker: true` (§4.2, C16), where the
   task's script runs a container. **FACT:** the run reached a terminal state,
   the container ran, and the runner's own uid and group (a root container
   changes them, §2.2).
2. **`fugaro logs`.** The run's lines come back through the Fugaro log view
   (the sink arm of step 10 applied), and the same lines are **absent** from
   `_Default`. **FACT:** entries found, and the `_Default` result.
3. **`fugaro cancel` on a live run.** `fugaro cancel RUN` leaves the job
   **cancelled, not deleted** — so `fugaro diagnose RUN` and `runs ls` still
   find the run's execution and report a terminal state, which is the behaviour
   the conformance suite's cancel case requires (§1.4). **FACT:** the job's
   state after the cancel, and what `diagnose` reported.
4. **The reaper, on all four exit paths** (success, failure, cancel, a crashed
   runner): a VM survives none of them for longer than the reaper's interval.
   **FACT:** per path, the VM's survival time and what the reaper deleted, and —
   the ordering rule of §3.4 — that the reaper deletes the **job first** and
   then only an instance with no live job.
5. **The cost.** One real run's `cost.compute_usd`, compared with the billing
   export for the same window (step 5's method, now against the estimate the
   code actually wrote). **FACT:** the estimate, the bill, and the difference —
   including whether the boot-and-pull window and the one-minute minimum were
   counted (§5.2, C17).
6. **The fail-closed marker.** On a VM, an unmarked environment (no
   `FUGARO_BACKEND`-style marker) must make the binary refuse rather than fall
   open: the design's §2.5 fix exists because four call sites today read
   `OnCloudRun` and treat "not Cloud Run" as "not in the cloud". **FACT:**
   whether the run without the marker refused.

**FACT lines to record:** every `FACT:` above; the `fugaro version` and the
date; the machine type and boot-image version; the median launch-to-first-line
for 57a-1, -2 and -3; whether Docker worked and by which permission path; the
billed cost of one run and the estimate; the final state and the survivor list
for each 57a-8 and 57b-4 path; the IAM bindings of 57a-9; the raw log entry of
57a-10; and whether `fugaro logs` found the Batch run's lines.

**Paste back:** the FACT lines, verbatim, plus any correction the run found and
**57a's go/no-go: build the VM backend only if 57a-2 or 57a-3's median is at
most 120 s above 57a-1's** (generic-tool §1, G2), *and* the socket path of
57a-4/57a-6 worked. If the cold-start budget is missed on every variant the
documented answer stays `fugaro-services` plus `image.apt`; if the socket path
failed, the design's C2 (§1.1, §1.10) is what reopens.

**Restore.** ⚠ CONFIRM, sandbox project only. This deletes **only what the
check created**, and its last step is an assertion that nothing of the check's
is left — the earlier draft's restore deleted "the service account", which was
the installation's own, and that is the failure this rewrite exists to prevent.

**Re-run the setup block first** — this is a new shell, and the deletes below
would otherwise interpolate an empty `$P`, `$L` or `$SA`, which is exactly how
`gcloud` falls back to its ambient `*-prod` configuration.

```bash
# 1. Every job the check submitted (57a-2, -3, -4, -7, -8, -11; 57b-1, -3, -4).
#    jobs.delete -- not cancel: the check is finished with them. Filtered by the
#    check's own label, never by name prefix alone.
gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
  --filter='labels.fugaro_chk=57' --format='value(name)' | \
  xargs -r -n1 gcloud batch jobs delete --project="${P:?}" --location="${L:?}" --quiet

# 2. Any VM still running. Delete only instances carrying THIS check's label --
#    and only after step 1, so a job that is still live cannot lose its VM.
gcloud compute instances list --project="${P:?}" --filter='labels.fugaro_chk=57' \
  --format='value(name)' | \
  xargs -r -n1 gcloud compute instances delete --project="${P:?}" --zone="${L:?}-b" --quiet

# 3. The account THIS CHECK created. Never the installation's job account, and
#    never the default Compute Engine SA.
gcloud iam service-accounts delete "${SA:?}@${P:?}.iam.gserviceaccount.com" --project="${P:?}" --quiet

# 4. The preloaded boot image, if 57a-3b built one (named after $SA).
gcloud compute images list --project="${P:?}" --filter="name~^${SA}-preload" --format='value(name)' | \
  xargs -r -n1 gcloud compute images delete --project="${P:?}" --quiet
```

The NAT/router/subnet, if the check had to create egress of its own (the
design puts NAT at the installation level, §1.7, so the check should **borrow**
the installation's where one exists and name anything it creates
`${SA}-*`), is deleted the same way, by that name — **not** by
`fugaro=managed`, which is the installation's label and reaches resources this
check never touched. The log sink arm, if 57a-10 applied it, is reverted with
the Terraform apply that reverses it (`fugaro init` from the sandbox's config),
and nothing else. Nothing in `belong` is touched at any step.

**The restore's assertion — run it and paste the output; it must be empty:**

```bash
echo "jobs:      $(gcloud batch jobs list --project="${P:?}" --location="${L:?}" --filter='labels.fugaro_chk=57' --format='value(name)' | wc -l)"
echo "vms:       $(gcloud compute instances list --project="${P:?}" --filter='labels.fugaro_chk=57' --format='value(name)' | wc -l)"
echo "image:     $(gcloud compute images list --project="${P:?}" --filter="name~^${SA}-preload" --format='value(name)' | wc -l)"
echo "account:   $(gcloud iam service-accounts list --project="${P:?}" --filter="email~^${SA}@" --format='value(email)' | wc -l)"
```

Four zeroes and the check left nothing behind. A non-zero line names the
resource still to delete, and **a surviving VM is the check's own finding**
(§3.4): record it, do not quietly delete it and move on.

#### Review notes on the round-1 findings

No finding in the round-1 review was rejected. Three corrections of our own,
for the record, where the review's phrasing was looser than the code:

- **B6, "add a queue deadline".** Batch exposes no queue-deadline field; the
  recommendation in §1.4 and C8 is a Fugaro-side reaper action (cancel a job
  that has sat in `QUEUED` past the deadline), and the only Google-side bound is
  the documented 2-day `QUEUED` ceiling. The design says that, rather than
  implying a knob that does not exist.
- **B12, `resource.labels.job_id`.** We could not verify it either way, so it
  stays **UNVERIFIED** and the shipped filter is the documented `labels.job_uid`
  form; 57a-10 is written to settle name-vs-UID rather than to assume one.
- **B7's "billed but not counted" for boot and pull.** The observation is right
  and is now in §5.2, but what the code can do about it before 57b-5's
  billing-export comparison is state the window honestly, not invent a second
  estimate; C17 keeps disk and NAT named as excluded rather than guessing them.

## 8. Task outline

### 8.1 The tasks

Not the full plan (that is written after the owner approves this design and
the pre-build probe, Check 57a, answers). Ordered so **each task merges
alone**, the **first one delivers value**, and — the ordering rule the earlier
draft broke — **no task can be enabled before the machinery it needs exists**:
`backend: cloud-batch` stays refused by config validation until the enable
task, and the enable task is after logs, cost and the reaper, because a Batch
run without log routing leaves its output in `_Default` where every project
viewer reads it (§1.5), and a Batch run without a reaper leaks VMs (§3.4).

**Task 0 is the owner's, and it is first: Check 57a** (§7.2). It needs no
Fugaro change at all — committed job JSON and `gcloud` — and it is the
go/no-go for the rest (G2's 120 s budget, the socket, the log filter, the
teardown). Nothing below it should be planned past Task 2 before its FACT
lines exist.

Tasks marked **[review]** need a security-focused review by the owner or a
senior reviewer before merge, not just the normal one.

1. **The seam's name codec and cloud detection (C12).** **[review]** Move
   `ParseExecution`, `SameExecution`, `ExecutionFromEnv`, `OnCloudRun` and the
   job-binding check behind a per-backend codec, add the fail-closed "in the
   cloud" marker, and keep Cloud Run's behaviour byte-for-byte unchanged
   (§2.5). Merges alone; no new backend; every existing test green. Value: the
   seam is honest about names before a second name shape exists, and the four
   call sites that currently fail open on an unknown platform stop failing
   open.
2. **The Batch fake and the conformance suite, behind a flag.** Add a Batch
   fake to `internal/gcpfake` and an `internal/backend/batch` that passes
   `backendtest.Run` against it — including the cancel case, which is what
   makes `jobs.cancel` rather than `jobs.delete` a tested requirement (§1.4) —
   with `backend: cloud-batch` still **refused** everywhere. The first task a
   contributor can do, needing no cloud and no owner step.
3. **Cost**: the VM price table, the machine type's quantities, the boot-window
   start, the one-minute floor and the report/history source line, plus the
   `runview` region fix (§5.2). Independent of the backend being selectable.
4. **Logs**: the `batch.googleapis.com/Job` arm of the sink and exclusion, and
   the `labels.job_uid` filter for `fugaro logs` (§1.5, §2.3).
5. **The reaper, the queue deadline and the teardown paths.** **[review]** The
   job-first reaper, its dedicated project-wide account, and the queue deadline
   (§1.4, §3.4). Destructive: the review is on the permissions and on what it
   refuses to delete.
6. **The Batch provisioning stage in `fugaro init`** (owner-run live step):
   the Batch API, `containerfilesystem` if streaming is used, the service
   accounts and the launcher/reaper IAM (§3.2, §3.7), the boot image or
   streaming, the NAT/router, and the sink arm of Task 4. **[review]**
7. **The job definition, `Launch` and the socket.** **[review]** The job JSON
   (labels in both fields, `secretVariables`, `maxRetryCount: 0`,
   `maxRunDuration`, `logsPolicy`, `blockProjectSshKeys`), the injectable
   execution name (§2.5), the socket mount and the env allowlist' `DOCKER_HOST`
   addition, and the SECURITY.md section (§1.1, §2.2–§2.4, §3.6). This is the
   task that makes root-on-a-VM real, hence the review.
8. **Config, `validate`, `doctor`, `backends ls`** — the plumbing for §4, with
   `cloud-batch` still refused.
9. **The enable task**: config validation starts **accepting**
   `backend: cloud-batch`, and `fugaro doctor` refuses the installation until
   the provisioning, IAM, logs, cost and reaper checks all pass (§4.4). This is
   the only task that lifts the refusal of Tasks 2 and 8, and it comes after
   3, 4, 5 and 6 — by construction, because `doctor` is what gates it.
10. **Check 57b, the docs, the skills and the release bullet** — the last task,
    as in every other group: the post-build acceptance run on the real backend
    (§7.3).
11. **The secrets fallback (only if needed).** The runner-side Secret Manager
    fetch for a secret whose name is known only at run time (§1.6). It is a
    task only if Task 7 finds such a case; otherwise it is dropped, not built.

### 8.2 Open decisions for the owner

Every decision in this document, C1 to C22, in one place: the ones taken above
and the ones that were only ever recommendations (§2, §3, §4.3, §5.2), each
with the alternative and what it costs. The sections above are where the
reasoning lives; this table is the checklist.

| # | Decision | Recommendation | The alternative, and its cost |
|---|---|---|---|
| C1 | Substrate | Cloud Batch, one job with one task per run | Plain Compute Engine instances (full control, but Fugaro owns lifecycle/timeouts/logs/reaper); MIG rejected |
| C2 | Docker source | The host daemon via the socket | Rootless/DinD (same host power, more parts, needs an unverified `--privileged` path) |
| C3 | Isolation unit | One VM per run, never reused | A warm pool (breaks run-to-run isolation) |
| C4 | Cold start | **Image streaming first** (in-region Artifact Registry, the `containerfilesystem` API, no custom image to maintain); a preloaded boot image as the fallback | Plain pull every run (simplest, slowest, no API to enable); the preload (a custom image Batch "might not support", with a Batch image's 60-day lifetime to rebuild against, and it costs the `options` field one socket-permission path needs). **Check 57a steps 2 and 3 measure all three and the recommendation follows the measurement** |
| C5 | Spot | Off by default, opt-in per profile | Spot by default (up to 91% cheaper, kills a stage mid-flight) |
| C6 | Service account | One per run class — the (`profile`, `account`) job account, never the default Compute Engine SA | The default SA (project-wide blast radius, and Google's own docs call it Editor and "not recommended for production"); a per-repository account is **C21**'s question, not this one |
| C7 | Egress | No external IP; Cloud NAT | An ephemeral IP + deny-all ingress (simpler, cheaper per run, "no inbound" rests on a firewall list) |
| C8 | Timeouts and cancel | Batch `maxRunDuration` + **`jobs.cancel`** (not `jobs.delete`, which erases the record a cancel must still be readable from), plus a queue deadline for a job stuck in `QUEUED`; tolerate a cancel that races a finished job | Fugaro's own watchdog (needed only on plain instances); `jobs.delete` as the cancel (cheapest, and it fails the conformance suite's cancel case — §1.4) |
| C9 | Logs | `logsPolicy.destination=CLOUD_LOGGING`, and extend the installation's sink/exclusion to `batch.googleapis.com/Job` | Leave logs in `_Default` (`fugaro logs` finds nothing, every viewer reads them) |
| C10 | Secrets | **`Environment.secretVariables`** — names in the job, values resolved by the VM as the job's service account | Bake values into the job definition (anyone with `batch.jobs.get` reads them); a runner-side fetch is a fallback only for a secret name known only at run time (§1.6) |
| C11 | `fugaro init` weight | One installation-level Batch stage, no per-repository resource | Per-repository VM setup (reintroduces the weight project-centric removes) |
| C12 | The name seam | Generalise `Backend` (§2.5) | Encode a Batch job as a synthetic Cloud Run execution (lies in stored data) |
| C13 | Naming | Reuse the Fugaro label set verbatim, plus a per-run label (§2.4) | Mark only the job and rely on the job-id prefix (fragile for a VM outliving its job) |
| C14 | The reaper | Keep it even though Batch self-cleans | Rely on Batch teardown alone (an occasional orphan VM bills) |
| C15 | Where the backend is chosen | Per project (`backend:`), not per workflow | Per workflow (two platforms in one project); or per profile inside the policy (correct after phase 3) |
| C16 | The Docker key | `docker: true` (generic-tool §1's name) | `runtime: docker` (a second name for the same thing); or no key (every workflow gets a socket) |
| C17 | What `compute_usd` covers | The VM's vCPU/memory; disk and NAT named as excluded | Fold disk/NAT in (a bigger change to the report shape) |
| C18 | When to build | After the pre-build probe, **Check 57a** (which needs no Fugaro change); the fake-conformance task (Task 2) and the cost and log tasks may land before it answers, but the enable task is last (§8.1) | Build before the measurement (risks building the wrong substrate); or build the backend before logs, cost and the reaper exist (a run whose output lands in `_Default`, uncharted and unbilled — the ordering the first draft had) |
| C19 | The preloaded image | A script in `fugaro init`'s Batch stage, one image per base version | Rely on image streaming alone (no image build, but depends on Artifact Registry + containerd) |
| C20 | Phase order | Independent of the project-centric phases; profiles (§4.3) adopted when phase 3 lands | Wait for phase 3 (cleaner, but delays a capability the owner needs) |
| C21 | The service account's granularity | Keep the (`profile`, `account`) account and accept project-centric D4's cross-repository loss **unchanged** on the VM backend too (§3.1) | A per-repository account, expressible on Batch because every job names its own `allocationPolicy.serviceAccount`: it buys back 0.7.0's bucket and secret boundary for the repositories that opt in, at the per-repository weight project-centric removed, and it makes "which backend" a decision with a security consequence |
| C22 | Launcher and reaper IAM | The launcher gets `batch.jobsEditor` plus `serviceAccountUser` on the run-class accounts and nothing else, in its own account if the project can carry one; the reaper gets a **separate** account with project-wide `batch.jobs.delete` and `compute.instances.delete` (§3.7) | One account for everything (simpler to provision, and it makes the reaper's destructive power ambient — rejected); an IAM Condition narrowing `batch.jobsEditor` to Fugaro-shaped job names is worth attempting, but Conditions on Batch job names are **UNVERIFIED** (Check 57a step 9) |

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
  serviceAccount`, `maxRetryCount` (default 0), the nine-value State enum
  (`STATE_UNSPECIFIED`, `QUEUED`, `SCHEDULED`, `RUNNING`, `SUCCEEDED`,
  `FAILED`, `DELETION_IN_PROGRESS`, `CANCELLATION_IN_PROGRESS`, `CANCELLED`),
  and that `Job.labels` apply to the job "and any Cloud Logging LogEntry that
  it generates" while only `AllocationPolicy.labels` reach the Compute Engine
  resources (Batch REST reference, checked 2026-10-15). Which of those states a cancelled or a preempted job
  actually passes through, and how long it sits in each, is **UNVERIFIED** —
  Check 57a steps 7 and 8.
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
  preempted at any time, stops or deletes the VM, with a preemption notice of
  **0 seconds by default** (a workload may ask for 120 seconds instead), after
  which the shutdown is best effort and can take up to 30 seconds
  (`cloud.google.com/compute/docs/instances/spot`,
  `cloud.google.com/compute/docs/instances/create-start-preemptible-instance`,
  checked 2026-10-15).
- Compute Engine quota model: VM quotas are regional, the VM-instances quota
  is auto-set to 10× the CPU quota, CPU quota is regional with a global
  CPUs (All Regions) quota, and the **"In-use IP addresses" quota is regional**
  (the global IP quota is for load-balancer addresses, not VM external IPs)
  (`cloud.google.com/compute/docs/resource-quotas`, checked 2026-10-15).
- Batch's job methods: `jobs.create/get/list/delete` and
  **`jobs.cancel`** (`POST /v1/{name=projects/*/locations/*/jobs/*}:cancel`),
  with `jobs.delete` erasing the job's details and history
  (Batch REST reference and `cloud.google.com/batch/docs/cancel-job`, checked
  2026-10-15).
- Batch secrets: `Environment.secretVariables` is "a map of environment
  variable names to Secret Manager secret names", so the **values** never
  enter the job definition (Batch REST reference).
- Batch finishes a job's life 60 days after it completes by deleting the job
  and its metadata, and `InstancePolicyOrTemplate`'s `blockProjectSshKeys`
  blocks project-wide SSH keys on the VMs (Batch REST reference and
  `cloud.google.com/batch/docs/create-run-job`, checked 2026-10-15).
- **"There is no additional cost for using Batch."**
  (`cloud.google.com/batch/docs/get-started`, checked 2026-10-15; §1.3), while
  an **in-use external IPv4 address is charged** at $0.005/h standard /
  $0.0025/h spot (`cloud.google.com/vpc/pricing`, checked 2026-10-15).
- Batch IAM: a job's creator needs `roles/batch.jobsEditor` plus
  `roles/iam.serviceAccountUser` on the job's service account, the job's
  account needs `roles/batch.agentReporter` and (for logs)
  `roles/logging.logWriter`, and the default Compute Engine service account is
  Editor and "not recommended for production"
  (`cloud.google.com/batch/docs/create-run-job`;
  `cloud.google.com/batch/docs/reference/rest/v1/projects.locations.jobs`,
  checked 2026-10-15).
- Cloud NAT is the no-external-IP egress path
  (`cloud.google.com/nat/docs/overview`), and Private Google Access reaches
  **only Google APIs**, not arbitrary endpoints
  (`cloud.google.com/vpc/docs/private-google-access`, checked 2026-10-15).

**Verified by reading the code (this repository):** the `Backend` interface
and its Cloud-Run-shaped names (`internal/backend/backend.go`); the
conformance suite (`internal/backend/backendtest`); the Cloud Run log filter's
use of `resource.labels.location`/`resource.labels.job_name` and the entry
label `labels."run.googleapis.com/execution_name"`
(`internal/backend/gcp/logs.go`), which §2.3's Batch filter mirrors in the
monitored-resource label kind; the log sink filter
(`deploy/terraform/gcp/modules/installation/logging.tf`); the runner's
backend-agnosticism (`internal/runner`); the launch path
(`internal/cli/run.go`, `launch_budget.go`); the naming/labels
(`internal/backend/gcp/names.go`); the base image's CLI-only Docker and its
3812 MB size (`images/base/Dockerfile`, base-image.md §9, §13). The live
checklist's own numbering is verified the same way: check 31 there is
`fugaro upgrade` with the real `claude` CLI (`docs/gcp-live-checklist.md`), not
the dropped gen-2 Docker probe the generic-tool plan numbered 31 — the reason
§7.1 reads the registry before claiming a number.

**Unverified, each with what settles it:**

- Batch container runnables accepting `--privileged`, `--user` or
  `--group-add` in `options` — Check 57a step 6.
- Whether the base image's container user can open the COS docker socket, and
  which permission path works — Check 57a step 6.
- VM boot and Batch scheduling times, with and without a preloaded image —
  Check 57a steps 2 and 3.
- Whether Batch adds any billable line beyond the VMs — Check 57a step 5.
- Whether Batch truly tears down every VM on `DELETION_IN_PROGRESS` and on a
  crashed runnable — Check 57a step 8.
- Whether image streaming works for a base image served through an Artifact
  Registry **remote** repository — Check 57a step 3 (pairs with Check 43).
- Whether Batch accepts a **custom boot image** at all ("might not be
  supported" is Google's own hedge) — Check 57a step 3b.
- Whether a Batch job's queue deadline can be expressed as a Google-side field
  — it cannot, as far as the pages read show, so the design's queue deadline
  is a Fugaro-side reaper action; and how long a job really sits in `QUEUED` —
  Check 57a step 11.
- Whether an IAM Condition narrowing `batch.jobsEditor` to Fugaro-shaped job
  names is accepted — Check 57a step 9.
- Whether `resource.labels.job_id` on a `batch.googleapis.com/Job` entry is the
  job's **name** or its **UID** (the resource list says only "The identifier of
  the Job"), which is why the shipped filter uses the documented
  `labels.job_uid` — Check 57a step 10.
- The project's own default quota numbers — Check 57a step 11.
- Whether a workflow's Cloud Run `resources` values map cleanly onto a
  Compute Engine machine type — a plan task (§4.4 item 3).
- Cloud Run gen-2's inability to run Docker is the **owner's measurement**
  (the premise of this design, restated in the generic-tool plan's 2026-10-14
  ruling); it is not independently reproduced by this document, and it does
  not need to be — the measurement is why the VM route exists.
