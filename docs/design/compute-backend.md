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
VM boundary, not the container one. Startup is bounded by image streaming
or a preloaded boot image — the base image served from the in-region Artifact
Registry, or baked into the VM's Docker cache — rather than a 3.8 GB pull on
every run; **C4 makes streaming the recommendation and the preload its
fallback, and Check 57a steps 2 and 3 decide between them**, with a cold-start budget
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
`options` is UNVERIFIED — Check 57a step 6 records the FACT. What
`TaskGroup.runAsNonRoot` does to the *container's* uid, and whether the image's
own `USER` survives it, is UNVERIFIED too: the REST reference says only that
Batch "uses the root user to execute runnables" when the field is unset or
false, and "a non-root user generated by OS Login" when it is true, and never
mentions the container's uid (§2.2). The fallbacks, in order, are: the gid
baked into the image (the only one left with image streaming on, §1.2; and the
one that needs no `options` at all), `--group-add <gid>` through `options`, a
systemd drop-in on the boot image that relaxes the socket's group, or a tiny
socket-proxy sidecar.

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
     job's own location — and that prerequisite is already met, by the
     installation's own base repository: `fugaro-base` is an Artifact Registry
     repository in the installation's region (the one `infra.BaseRegistry`
     names, §7.2's `IMG`), which is where every release image is pushed today.
     The **remote** repository of
     [registry-remote-repository.md](registry-remote-repository.md) is a
     different thing — a proxy that removes the ghcr.io copy step — and it is
     **PR #362, not merged**: streaming does not depend on it, and §7.2 points
     its probe at the direct repository for that reason. The second prerequisite
     is the `containerfilesystem.googleapis.com` API (`same page`; §1.9, the
     Batch provisioning task, 6). **The catch is twofold.** It switches the container runtime
     to **containerd, not Docker**, and with streaming "container runnables
     only support the following fields: `imageUri`, `commands`, `entrypoint`,
     `volumes`, `enableImageStreaming`" — every other option is disregarded
     (same page). `options` is disregarded too, which is the field §1.1's
     `--group-add`/`--user` socket-permission path needs: **streaming and that
     path are mutually exclusive**, so a streamed run has exactly one way left to
     make the socket usable — the group baked into the image (§2.2); there is no
     `options` to pass and no `--user` to set. Streaming still suits the socket design in principle —
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
| **C2** | Where Docker comes from | **The VM's host daemon, via the bind-mounted socket** — noting that **image streaming disregards `options`** (§1.2), so with streaming (C4) the socket can only be made usable by the group baked into the image (§2.2), not by `--group-add` and not by `--user` | **Rootless/Podman inside the container** or **privileged DinD**: same host-level power, more moving parts, doubles storage, needs an unverified `--privileged`/user-namespace path in a Batch runnable. |
| **C3** | Isolation unit | **One VM per run; a VM is never reused** | Reuse a pool of warm VMs: rejected — two runs on one VM can read each other's secrets and Docker state, and Docker makes each root on the host. |
| **C4** | Cold start | **Image streaming first** (Artifact Registry in-region, the `containerfilesystem` API, no custom image to maintain), **preloaded boot image as the fallback** | The preload is not the primary choice because Batch does not officially support custom images and a Batch image's supported lifetime can end **60 days** after its starting date, so it is an image to rebuild on Google's schedule as well as Fugaro's, and it costs the `options` field that one socket-permission path needs. Plain pull every run is the cheapest and slowest. **Check 57a steps 2 and 3 measure all three, and the recommendation follows the measurement.** |
| **C5** | Spot | **Off by default**, opt-in per profile | Spot by default: up to 91% cheaper, but a preemption kills the stage mid-flight, the notice is **0 s by default** (120 s only if the job asks, and the shutdown after it is best effort, up to 30 s), so checkpointing has no room at all. |
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
  `backend.MemoryGiB` and `backend.TaskTimeoutSlack`). It does **not** use
  `backend.ExecID`: the canonical name reaches it as a plain string
  (`runner.Deps.Execution`, produced by the CLI), and the only mention of
  `backend.ExecID` in the package is that field's doc comment
  (`internal/runner/runner.go:74`). The gateway
  (`runner/gateway.go`), the budget session (`runner/budget_session.go`,
  which is RTDB, not `internal/backend`), the cache
  (`runner/lockcache.go`) and `finalize` (`runner/runner.go`) touch only the
  process, the RTDB and the runs bucket — all of which behave identically on
  a VM. **Nothing in those four paths changes.**
- The job env is nearly the same set the launch path already writes, and it
  comes from three places, not one. `platformEnv` (`internal/infra/spec.go`
  :522-529) writes exactly five: `FUGARO_BUCKET`, `FUGARO_BACKEND`,
  `FUGARO_GCP_PROJECT`, `FUGARO_PROJECT` and `FUGARO_REGION`. `budgetEnv`
  (`spec.go`:732) writes the budget variables, the workflow spec writes the
  state/checkout/remote ones and the model-provider variable (`spec.go`:691),
  and **`FUGARO_RUN` is written by none of them**: the launch path sets it per
  execution, as a Cloud Run container override
  (`internal/backend/gcp/run.go`:110) — which is the same per-run name the
  Batch launcher has to write for itself (§2.3). On Batch the job env goes in
  **`taskSpec.environment.variables`** (Batch REST reference, `TaskSpec` and
  `Environment`, checked 2026-10-15) instead of Cloud Run's per-execution
  `env` override; the names and values are unchanged.

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

The three ways to get that permission are ordered in §1.1.

**`runAsNonRoot` says nothing about the container's uid, and nothing about the
image's `USER`.** Batch's `TaskGroup.runAsNonRoot` — it is a **`TaskGroup`**
field, not a `TaskSpec` one: `TaskSpec` has `computeResource`, `environment`,
`environments`, `lifecyclePolicies`, `maxRetryCount`, `maxRunDuration`,
`runnables` and `volumes`, and no `runAsNonRoot` (discovery document,
`batch.googleapis.com/$discovery/rest?version=v1`, schemas `TaskGroup` and
`TaskSpec`, checked 2026-10-15) — is documented, in
full, as: unset or false, "Batch uses the root user to execute runnables"; true,
"Batch runs the runnables using a non-root user. Currently, the non-root user
Batch used is generated by OS Login." (Batch REST reference, `TaskGroup`,
checked 2026-10-15 against the discovery document). That sentence is about the
**runnables' execution user**; it does not say the container's uid is 0, it does
not say the image's `USER` is read, and it does not say the field **overrides**
one. A container's uid is therefore **not** something this document can state: it
is **UNVERIFIED**, and it is an input to the design, not a default to assume.
Step 57a-6 is what
settles it: it runs `id` in every variant and reads the uid back. The
unverified-ness **is** the reason the socket question is hard, so it must not be
papered over with a plausible-sounding default.

The way to get the socket's group into the container without changing the uid
is the runnable's `options`, documented only as a **string** of "arbitrary
additional options … for example, `--network host`" (`Runnable.Container`;
`"options": { "type": "string" }` in the discovery document, checked
2026-10-15 — it is **not** an array, and an array is rejected) and therefore
UNVERIFIED for this use. The variants to try, in order (each is a committed file
of §7.2 — `check57/job.json` with exactly these two fields replaced — and 57a
step 6 walks all four submissions: these three and the no-`options` baseline,
then the two it builds from the winner):

```json
"options": "--group-add __DOCKER_GID__"   // (a) keep the image's uid, join the socket's group
"options": "--user 0"                     // (b) root by docker's own flag, whatever the image's USER says
"options": "--privileged"                 // (c) the host's devices and capability sets -- and NOT a uid change, so it is expected to be refused like the baseline (§2.2)
```

The gid is **numeric, and read off the VM** (`stat -c %g /var/run/docker.sock`,
57a-6's first line), because `--group-add docker` is resolved by the runtime
**inside the container's own `/etc/group`** — and the base image has no `docker`
group, so the name form fails with `Unable to find group docker` before docker
is ever reached. A gid that does not exist in the container is what
`--group-add` takes; a name has to be looked up, and this one is not there.

(c) is **not** a second way to root, and it is not a way to test (a) either.
`--privileged` gives the container the host's devices and the full capability
**sets**, but it does not change the **uid**: the container still starts as the
image's own user, and a uid-1000 process starts with an **empty effective
capability set** — Linux clears permitted→effective at `execve` for a non-root
uid with no file capabilities and no ambient set (`capabilities(7)`,
`execve(2)`) — so `CAP_DAC_OVERRIDE` is not there to open a mode-`0660`
`root:docker` socket, and (c) is **expected to be refused exactly like the
no-`options` baseline**. Whether the runtime sets ambient capabilities anyway is
what 57a-6's `id` and error lines measure; if (c) *does* get in, then the uid is
not 1000 or the runtime is granting what the kernel would not, and either way
that is the finding. Two
things must be checked alongside them, and 57a step 6 records both: `options`
are **dropped altogether when image streaming is on** (§1.2 — streaming honours
only `imageUri`, `commands`, `entrypoint`, `volumes` and `enableImageStreaming`),
so **with streaming (C4) the only socket path left is the group baked into the
image** — there is no `options` to pass and no `--user` to set. That sentence is
an argument from the five-subfield list, and it is **UNVERIFIED as behaviour**
until 57a-6's combination submission runs the variant that worked with streaming
on and the socket probe's own commands; and whether a
`docker` group **exists on the COS boot image**, and with which gid, is
**UNVERIFIED** — if it does, baked-in membership is the cleanest ordering of
all, because it needs no `options` and survives streaming. Check 57a step 6
records which variant works, and Restore removes the VM the test left.

**Running as root is not free.** The base
image builds a `fugaro` user at **uid 1000** and runs as it
(`images/base/Dockerfile`); on a VM whose container ends up as root — which is
what 57a-6 measures, not what this document assumes — every file
the run creates in `/work/repo`, in the caches and in the mise store is owned
by root instead of that user. That changes what a *follow-up* run can do in
the same checkout, it makes the cache archives the runner uploads carry root
ownership, and it is a direct conflict with generic-tool's **D1** ("no root at
run time"). It is acceptable **only** because §3 already says the run is
root-equivalent on its VM whatever the container's uid — the socket makes
`--privileged` a single command away — so treating the uid as a boundary would
be theatre; but it must be a **recorded** decision, not a side effect of
choosing the socket. The cleanest ordering is therefore: (1) bake the socket's
gid into the image and add the container user to it, so nothing else changes and
streaming cannot drop it; (2) `--group-add <gid>` through `options` if Batch
honours it; (3) root, with the ownership consequence written into SECURITY.md.
Whether the host's `docker` gid can be baked into the image without a rebuild on
every COS change is **UNVERIFIED** — Check 57a step 6 records the gid, and which
of these orderings the build takes is §8.1's task 7 (the socket, with the image
side in task 6), not this check's.

Security consequence, repeated because it is the whole of §3: **a run that has
this socket is root on the VM.** The mount is not a privilege boundary and is
not treated as one.

### 2.3 The backend interface, mapped

Every method of `internal/backend.Backend` maps onto Batch:

| Seam method | Batch | Notes |
|---|---|---|
| `Launch(LaunchSpec)` | `projects.locations.jobs.create` | One Batch job, one task group, one task. Name derived per run (§2.4). The `FUGARO_RUN` env and the timeout are set in the job definition, not in an override — Batch has no execution-override concept like Cloud Run's `Overrides`. Returns `ExecutionRef{Name, Job, LogURL}`. |
| `Execution(name)` | `jobs.get` | Maps Batch's state enum to `backend.State`, for **every** value the API documents: `STATE_UNSPECIFIED`/`QUEUED`/`SCHEDULED` → `StatePending`, `RUNNING` → `StateRunning`, `SUCCEEDED` → `StateSucceeded`, `FAILED` → `StateFailed`, and `CANCELLATION_IN_PROGRESS`/`CANCELLED` → `StateCancelled`. That list is the complete `JobStatus.State` enum — `STATE_UNSPECIFIED`, `QUEUED`, `SCHEDULED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `DELETION_IN_PROGRESS`, `CANCELLATION_IN_PROGRESS`, `CANCELLED`, in that order and **no others**: Batch v1 has no `SUCCEEDING` and no `FAILING` (checked 2026-10-15 against the REST reference, the discovery document and `google/cloud/batch/v1/job.proto`'s `JobStatus.State`, which carry the same eight plus `STATE_UNSPECIFIED`). So no documented value falls through untyped. `DELETION_IN_PROGRESS` → `StateCancelled` too, but it should only ever be seen for a job the **reaper** is deleting (§3.4) — a user cancel is `jobs.cancel`, which ends `CANCELLED` (§1.4). An unrecognised value is treated as **not terminal** and logged — the same defensive shape as `stateOf` in `internal/backend/gcp/run.go`, which reads Cloud Run's counts rather than trusting one field. `Created`/`Started`/`Completed` from the job's and its task's status times; `CPU`/`MemoryGiB` from the machine type; `Run` from the `FUGARO_RUN` the job was created with (Batch reports the task's `environment.variables`, so unlike Cloud Run this one is always readable — still a hint, never evidence). |
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
  `gcloud compute instances list --filter='labels.fugaro_chk=57'`
  find nothing, which reads as "no survivors" precisely when a VM *has*
  leaked. (The check's filter is this design's own second label for exactly
  that reason: filtered by the installation's `fugaro=managed`, which only
  `AllocationPolicy.labels` put on a VM, it would list nothing and look like a
  clean teardown.) So the Fugaro set goes in **both**: `fugaro=managed`,
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
(`internal/backend/backend.go:86` as this was written; Task 1 moved it to
`Codec.On`, `internal/backend/codec.go`). A Batch job's resource name is
`projects/<p>/locations/<r>/jobs/<j>` — six fields, not eight — so the two
cannot share a parser, and on a Batch VM `CLOUD_RUN_EXECUTION` is absent, so
`OnCloudRun` is false and the runner would treat itself as a local run.

**This is not cosmetic: four of those call sites are security checks that fail
open when `OnCloudRun` is false.** Every caller, read on `main`:

| Site | What it does | With `OnCloudRun` false (a Batch VM) |
|---|---|---|
| `internal/runner/gateway.go:288` (`routingKnob`) | refuses the routing-refusal escape knob (`FUGARO_TEST_ALLOW_ROUTING_SETTINGS`, `allowRoutingSettingsEnv`, `internal/runner/gateway.go:39`) on Cloud Run | **honoured**: a job env the launcher controls can switch the routing refusal off |
| `internal/cli/exec.go:195` (`RequireProject`) | requires the job to name its Fugaro project | **not required**: a run with no `FUGARO_PROJECT` proceeds |
| `internal/cli/exec.go:264` (`refuseOldJobEnv`) | refuses an old-style job env on Cloud Run | **skipped**: the old env is accepted |
| `internal/cli/exec.go:102` (`runner.BackendFromEnv`) | forbids a local (loopback) budget backend in the cloud | **allowed**: a run may point the budget at a local database |
| `internal/cli/exec.go:134` (`ExecutionFromEnv`) | the canonical execution name | `""`, so the run has no name to record or to compare |
| `internal/cli/exec.go:369-371` (`execPrices`) | the prices the run estimates compute with: `FUGARO_COMPUTE_PRICES`, else the region's list price | **nil**, so the run records no compute cost at all (`FUGARO_BACKEND` must equal `cloud-run` for prices to exist, and on a Batch VM it does not) |
| `internal/cli/skillwarn.go:43`, `internal/cli/updatewarn.go:155,186,260` | quiet the skill/update nudges in the cloud | nudges appear (cosmetic) |
| `internal/cli/imagecheck.go:548` | the image check's Cloud Run detection | already keyed on `CLOUD_RUN_JOB`/`CLOUD_RUN_EXECUTION`; needs the same treatment if the VM backend ever runs a check job |

**Recommendation (C12): generalise the seam, and make "in the cloud" fail
closed.** Move the name codec into each backend — a small interface
(`Parse(name)`, `Format(id)`, `FromEnv(env)`, `IsOwn(name)/ValidateRun(name,
slug, workflow)`) carried by the `Backend`, used by every caller instead of
the package-level functions — and replace `OnCloudRun` with a question each
backend answers from a **signal the platform injects, never one Fugaro sets**.
`backend.go`:82-85 (as this was written; Task 1 moved the doc to
`Codec.On`/`Codec.FromEnv`, `internal/backend/codec.go`) states the rule this
follows: it is "the one signal for 'in the cloud'; callers use it, not the
job name or a Fugaro variable". The Batch backend's signal is therefore
**`BATCH_TASK_INDEX`/`BATCH_TASK_COUNT`**, which Batch injects into every task
(Batch REST reference, `TaskGroup`, checked 2026-10-15), with a
metadata-server probe (`http://metadata.google.internal/…` answering with
`Metadata-Flavor: Google`) as the fallback for a VM whose vars were stripped;
`OnCloudRun` keeps its Cloud Run meaning for
the Cloud Run backend; the Batch backend answers "am I on you?" from its own
marker; and the answer for a process **no backend claims** is "in the cloud"
(`true`), so the four security checks above fail *closed* on an unrecognised
platform instead of open. That last part is the point: today the function's
name invites "am I on Cloud Run?" while every caller reads it as "am I
somewhere a launcher controls?", and the two are not the same question.

**Local mode is an explicit invocation, never "no cloud marker", and that is
what keeps the local path working.** A true-by-default answer has one dangerous
reading — a local `fugaro exec` classified as in-cloud, which would make
`RequireProject` true (`internal/runner/project.go`:22 then fails a run whose
environment has no `FUGARO_PROJECT`) and pass `loopbackOK = false` to
`runner.BackendFromEnv` (`internal/cli/exec.go`:102), breaking the local
`--task-file` path, the local Docker checks (20, 21) and the e2e suite
(`internal/e2e/e2e_test.go`:97 launches a local `fugaro exec --task-file` with
no `FUGARO_PROJECT`). So the design names the local case rather than inferring
it. There is exactly one local invocation, and the CLI already recognises it:
`fugaro exec` with `--task-file` ("`--task-file` is for local runs; on Cloud
Run the run comes from `FUGARO_RUN`", `cli/exec.go`:139-142). `exec` therefore
resolves the platform **first** — **local** when `--task-file` was given (or a
hidden test hook put it there), else whichever backend claims the process,
else "in the cloud" — and every check reads that one resolved value:

- **Local**: `RequireProject` is false, `loopbackOK` is true, `refuseOldJobEnv`
  and `execPrices` keep their local behaviour. This is today's behaviour for
  every local run, which is why Task 1 must leave the e2e suite and checks 20
  and 21 green (it is a requirement of that task, not a nicety).
- **In the cloud**: the platform's marker names the backend, and an
  unrecognised or stripped marker is **in the cloud**, so the checks fail
  closed.
- The flag is a CLI argument, so **nothing in a job's environment can ask for
  local mode**, and a `--task-file` on a process that is a Cloud Run execution
  stays refused (`cli/exec.go`:139-142 already reports that contradiction): the
  two rules cannot both apply to one run.

**`FUGARO_BACKEND` is not that signal, and must not become it.** Three things
break if it is used as one. It is a Fugaro variable: `internal/infra/spec.go`:88 defines it and
`platformEnv` writes it into the job definition, so a local `fugaro exec` run
inherits whatever the owner's shell holds and would be told it is a
launcher-controlled job — the exact confusion `refuseOldJobEnv`
(`cli/exec.go`:263) exists to prevent, and the opposite of what `backend.go`
:82-85 asks callers to key on. It already means something else and something
narrower: `execPrices` (`cli/exec.go`:369-371) returns **nil prices unless the
value is exactly `cloud-run`**, so a Batch VM carrying `FUGARO_BACKEND=cloud-batch`
would silently stop estimating compute — §5.2's third site, and a task of its
own, not a consequence of the marker. And detection that a *job env can set* is
not detection: on Cloud Run the launcher controls the job definition, so the
value is only as trustworthy as the launch path. The platform-injected signal
above is the one the VM cannot be confused about; `FUGARO_BACKEND` stays what
it is — the launcher's record of *which* backend it chose, for pricing and for
`launch.json` (`internal/backend/backend.go`:17-19). **Check 57a step 12** is
what tests the remaining gap: whether a job's own environment can override
`BATCH_TASK_INDEX`/`BATCH_TASK_COUNT`, i.e. whether the injected signal is
itself forgeable by whoever can submit a job.

Trade-off: it touches every caller (`cli/cancel.go`, `logs.go`, `diagnose.go`,
`run.go`, `watch*`, `exec.go`, and the runner's `SameExecution` uses) and is a
change to the shared package, not a new sibling package — a bigger than
average first task, and it must land **before** any Batch backend can run a
container: Task 1 before the job-definition task (7) and the enable task (9),
§8.1. Two things the recommendation has to respect:
`CheckRunExecution` lives in **`internal/backend/gcp`**
(`run.go:92`), not in `internal/backend`, and it is called from
`internal/cli/cloud.go`; and the runner holds the canonical name as a
**string** (`runner.Deps.Execution`, set by the CLI from
`backend.ExecID.String()` — `internal/runner/runner.go:74-77` is that field's
doc comment, not a use), so what the codec has to keep stable is the string
the CLI stores, which `result.json` and `backend.SameExecution` are read
against. Veto
alternative: keep the Cloud-Run shape and encode a Batch job as a synthetic
"execution" under it (`.../jobs/<fugaro job>/executions/<run id>`), which lies
in the stored data model and would confuse `fugaro ls`, `cancel` and the
sweeper the first time someone reads a raw name. **C12 is an owner decision by
size, not by uncertainty.**

**How the runner learns its own name on Batch.** Cloud Run injects
`CLOUD_RUN_EXECUTION`/`CLOUD_RUN_JOB`;
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

**A VM per run does not, by itself, undo that loss.** What a VM adds over a
shared job is
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
  `account`) — project-centric §4.1's granularity — unless the owner takes
  C21's per-repository alternative. **That binding does not exist today, and
  this design must not read as if it does.** The account is chosen by the
  *deployment*: today that is one account per repository, and §6.2 says which
  model applies before phase 3 and after it. And **nothing in the runner checks
  that the slug a run works under equals `task.Slug(provider, spec.Repo)`** —
  the run's slug comes from the `FUGARO_RUN` override, and a mismatch is caught
  only because the per-repository job's bucket condition cannot read another
  slug's `task.json` (`docs/design/project-centric.md` §2.2, which says so in
  those words). The equality is the **slug binding** that project-centric phase
  1's plan builds (`docs/plans/2026-10-14-project-centric.md` Task 1.5, "the
  slug binding's first half"), and it is the precondition this design's account
  choice rests on: a run whose slug and repository disagree would be bound to
  the wrong (`profile`, `account`). Until it lands, the VM backend uses the
  per-repository account (§6.2) and inherits today's behaviour rather than an
  invariant. Its grants:
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
**The estimate is computed in two places, a third site decides whether there
are any prices to compute it with at all, and none of the three survives a VM
unchanged:**

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

3. **And a gate in front of both** (`internal/cli/exec.go`:369-371): the CLI
   hands the runner its `Prices` only when `FUGARO_BACKEND` is exactly
   `cloud-run`, and `nil` otherwise. A Batch VM that does not set that
   variable — or sets it to `cloud-batch`, which nothing reads today — gets
   **no prices at all**: `r.d.Prices` is nil, the runner-side estimate of site
   1 does not run, and the run's record carries no compute cost even though
   `runview`'s site 2 could still price the execution later. Today the gate is
   harmless (only Cloud Run sets the variable); on a second cloud backend it is
   the first thing to fix, and it is part of C12's task rather than a
   consequence of it: `execPrices` must ask the *backend*, not a string the
   job env happens to carry, and the Batch answer is the Batch price table.

On a Batch VM all of it breaks, in four ways, not one:

- **The prices may not exist at all.** Site 3 above. This is the one that
  fails *quiet* rather than *open*: no error, no cost, and a `runview` line
  that falls back to the empty-region table of the next bullet.
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
service account, and **the account's granularity is C21's decision, not the
VM's**: it is the (`profile`, `account`) account the design keeps (§3.1), which
before phase 3 is today's per-repository account (§3.2), so the loss applies on
the VM backend exactly as it does on a shared job. What it removes is
the *shared job*, and with it the reason Cloud Run had to share. Consequences:

- **Before phase 3**, a VM backend uses the current model: a per-repository
  job service account and an installation-level Batch stage. It works, but it
  carries the per-repository SA weight project-centric is removing.
- **After phase 3**, the same backend uses a project job service account per
  (`profile`, `account`) and profiles select the VM class (§4.3) — the intended
  end state.
- The **seam generalisation (C12)** is independent of both and can land first.
- Recommendation: the first cloud-free task is the seam's name codec (§8.1,
  Task 1), and the first that needs neither a cloud **nor an owner step** is
  the conformance suite against a fake (Task 2), which needs neither phase 3
  nor any cloud, while the live measurement (Check 57a, Task 0) gates the rest.

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
`main` (`2be38e1`, read 2026-10-15), the table has rows for checks 1–23, 26–38
and 40–47; **24, 25 and 39 are reserved with no row** (24 and 25 carry their own
`## Check` sections in the file; 39 (39a–39f) is the
**retire-image-mirror plan's** Task 7 — the
checklist's own numbering ruling assigns it that way,
`docs/gcp-live-checklist.md`:187-189, and the retire-image-mirror plan says so
itself at `docs/plans/2026-10-13-retire-image-mirror.md`:581 — with its row
written when that plan lands, so "checks 1–47 have rows" is not quite true,
though nothing about the numbering here turns on it). Check 38, the
**generic-tool plan's** Task 18 clone-workflow check, had no row when this
design first read the register and has one now (`4e3de61`) — there is a
`## Check 38` section and a row 38 on `main`, and this paragraph is what a
later reader should check first if the two disagree again. The
provider-price-catalog design's paragraph
names 45 as "the next free number" — true when that paragraph was written, and
out of date since: **45 and 46 are now the OpenRouter senior-reviewer plan's
own checks and their rows *are* in the table (`2be38e1`, PR #359), 47 is the
subscription-OAuth-token plan's (PR #363, merged as `ba03ab5` — its row is in
the table too), and 48–56 are reserved by the project-centric plan (PR #366,
merged as `c86eb92`) with no rows yet** — so the numbers between 47 and 57 are
reserved-and-unwritten, not free. A number
reserved in a plan is reserved
whether or not its row is written, which is why nothing sits between 47 and 57
today, and why 45 was not available to this design. So this design claims
**Check 57**, and its row and its reservation
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

**Where this text lives, and the guard every other row has.** Row 57 is in the
checklist this design adds; its **body is here**, not in
`docs/gcp-live-checklist.md`. That is deliberate while this is a design — the
full plan is written only after the owner approves it and 57a answers (§8.1) —
but it makes 57 **the one row in the file whose check has no `## Check 57`
section**, so the convention that goes with the numbering does not hold for it
yet. That convention, read off `main`: every check task writes **its row, its
`## Check NN` section in the file** (the section, not the design, is what the
owner runs from) and a **`TestChecklistHasCheckNN`** in the `docs_*_test.go`
convention `TestChecklistHasCheck33` and `TestChecklistHasCheck40` follow — with
**`TestChecklistChecksAreGuarded`** (project-centric Task 6.3,
`internal/cli/docs_project_centric_test.go`) as the backstop that asserts
Check 43's guard block inside **every `## Check 48`-onward section**, over
checks 48–56 as a property no single section can state. **Nothing asserts 57's
guard today** — the setup block below is what enforces it, and a test cannot see
a block that is not in the file it reads. This design names that gap rather than
papering over it, and its own task outline closes it: **Task 10 (§8.1) writes
`## Check 57` into `docs/gcp-live-checklist.md` from this section**, 57a and 57b
as they stand here, **keeps the row this PR adds**, adds **`TestChecklistHasCheck57`**
(asserting the section, the row, and Check 43's guard block inside the section),
and re-points the row at its own section instead of at this design. Until then
57a is run **from here**, and the row's pointer to §7.2 is the honest one.

**Two parts, and the first one gates the build.** A check whose own first steps
need the thing it gates cannot gate it, so this one is split:

- **57a — the pre-build probe.** Fifteen steps — 0, 0b, 0c (the check's own
  prerequisites: its account and registry grant, its egress, and streaming's
  API) and 1–12 — **`gcloud` and a committed job
  JSON, no Fugaro change whatsoever**, runnable today against the sandbox
  project (step 1 alone uses the *existing* Cloud Run backend, for the
  baseline the 120 s budget is measured against). It is the design's **go/no-go**, and the plan
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
          "imageUri": "__IMG__",
          "commands": ["bash", "-lc", "id && echo first-line"],
          "volumes": ["/var/run/docker.sock:/var/run/docker.sock"]
        }
      }],
      "computeResource": {"cpuMilli": "2000", "memoryMib": "4096"},
      "maxRetryCount": 0,
      "maxRunDuration": "1200s"
    }
  }],
  "allocationPolicy": {
    "instances": [{
      "policy": {"machineType": "e2-standard-4", "bootDisk": {"image": "batch-cos", "sizeGb": "50"}},
      "installGpuDrivers": false,
      "blockProjectSshKeys": true
    }],
    "serviceAccount": {"email": "__SA_EMAIL__"},
    "network": {"networkInterfaces": [{"network": "global/networks/default", "noExternalIpAddress": true}]},
    "labels": {"fugaro": "managed", "fugaro_chk": "57"}
  },
  "labels": {"fugaro": "managed", "fugaro_chk": "57"},
  "logsPolicy": {"destination": "CLOUD_LOGGING"}
}
```

**`__IMG__`, `__SA_EMAIL__`, `__DOCKER_GID__`, `__QUOTA_MT__` and
`__PRELOAD_IMG__` are the five places the JSON and the terminal have to agree,
so the JSON does not fix them.** They are placeholders and the block above is
not valid JSON until they are substituted: the setup block below renders them
from its own variables into `/tmp/check57*.json`, and **every** submission in
57a and 57b then uses those files and never the committed ones — so a run of the
check cannot send an image, a project, a region, an account, a socket gid, a
machine type or a boot image the rest of the check is not using.

`IMG` is the installation's own base image, and the setup block **reads it out
of the installation** (`fugaro doctor --json`'s `project.base_images.base`)
rather than naming a tag by hand; its shape is
`<region>-docker.pkg.dev/<project>/fugaro-base/fugaro-base:<version>` — the
**base repository the installation creates**, named by
`infra.BaseRegistry = "fugaro-base"` (`internal/infra/tfvars.go`:23).
`infra.LegacyRegistry` (`:24`) is the older single repository, named `fugaro`,
that a pre-0.7.5 installation has and discovery adopts
(`internal/infra/discover.go`:394-421) — a different repository name, and not a
path this check pulls from. It is
**not** the `fugaro-ghcr/dimipaun/fugaro-base` remote-repository path of
`docs/design/registry-remote-repository.md`: that work is **PR #362, not
merged**, and this check does not depend on it. Should #362 land, pointing the
probe at the proxy is a change to `IMG` alone — which is what the placeholder
is for — but the committed check uses the direct repository.

`batch-cos` is one of the **three short names Batch documents for
`Disk.image`** (`batch-cos`, `batch-debian`, `batch-hpc-rocky`), the URL forms
`projects/{project}/global/images/family/{family}` and
`projects/{project}/global/images/{version}` being the alternatives (Batch REST
reference, `Disk`, checked 2026-10-15). `batch-cos-stable` is **not** a
documented value, and a family name has to be written as the family URL: the
short names carry no version. `machineType`, the region and the account are the
check's variables, named in the setup block below.

**The probe runs as the check's own service account, on the design's own
network posture: 57a has to test what the build will do.** Three fields say so,
and each would otherwise take a default that contradicts the design.

- `allocationPolicy.serviceAccount.email`. Unset, the job runs as the **default
  Compute Engine service account** — `roles/editor` on the entire project —
  and §3.1's C21 exists precisely to avoid that account. A cold-start median
  measured under it would not carry over to the job account the build uses.
  Step **57a-0** therefore creates `$SA` and grants it before the first job is
  submitted, and Restore deletes it; nothing below may run without it.
- `network.networkInterfaces[0].noExternalIpAddress: true`. The documented
  default is **false** (Batch REST reference, `NetworkInterface`), i.e. an
  external IP, where §1.10's C7 has the run without one. The `network` block
  names `global/networks/default` only because the sandbox is a bare project;
  an installation with its own network sets the network and the subnetwork
  here. **If the job cannot pull its image or write its logs with no external
  IP, that is the finding**, not a reason to add one back: it means the Private
  Google Access or Cloud NAT §1.7 assumes is missing. Step 0b settles the route
  *before* the first submission, and step 2's first line is where a missing one
  would still show.
- `allocationPolicy.instances[0].blockProjectSshKeys: true`, which closes
  project-wide SSH keys on the instance (§3.2). It is a field of
  `InstancePolicyOrTemplate` — **beside** `policy`, not inside it (Batch REST
  reference, checked 2026-10-15).

**Step 6's four socket variants are committed files too** — `check57/job-socket-none.json`,
`check57/job-socket-groupadd.json`, `check57/job-socket-user0.json` and
`check57/job-socket-priv.json` — because a described variant is a variant nobody
runs twice the same way, and the point of step 6 is that the four are
comparable. (The step's fifth and sixth submissions — the streaming combination
and the timing run — are **built at the terminal from whichever of these four
worked**, since which one that is cannot be known when the files are committed.)
**Each is the committed `check57/job.json` above with exactly two
fields replaced — `options` and `commands` — and nothing else**, so the socket
mount, the network posture, the account, the boot disk and the labels are the
same in all four and in 57a-4. The replacements, verbatim:

```jsonc
// check57/job-socket-none.json -- the baseline: no "options" field at all.
// It is also what reads the socket's gid for the next file ("stat"), and it is
// the submission whose expected outcome on a uid-1000 container is a refusal.
"commands": ["bash", "-lc", "id; stat -c %g /var/run/docker.sock; docker version; docker run --rm docker.io/library/hello-world; echo chk57-socket-done"]

// check57/job-socket-groupadd.json -- §2.2's (a). __DOCKER_GID__ is filled in
// from the baseline's "stat" line, not from this document: it is the host COS
// image's gid, and a hard-coded one would be a guess.
"options": "--group-add __DOCKER_GID__"
"commands": ["bash", "-lc", "id; stat -c %g /var/run/docker.sock; docker version; docker run --rm docker.io/library/hello-world; echo chk57-socket-done"]

// check57/job-socket-user0.json -- §2.2's (b)
"options": "--user 0"
"commands": ["bash", "-lc", "id; stat -c %g /var/run/docker.sock; docker version; docker run --rm docker.io/library/hello-world; echo chk57-socket-done"]

// check57/job-socket-priv.json -- §2.2's (c). Expected to be refused exactly
// like the no-"options" baseline: `--privileged` hands over the host's devices
// and capability sets but does not change the uid, and a uid-1000 process's
// effective set is cleared at execve, so it has no CAP_DAC_OVERRIDE for the
// socket. Its "id" line is what would show that it is not 1000 after all.
"options": "--privileged"
"commands": ["bash", "-lc", "id; stat -c %g /var/run/docker.sock; docker version; docker run --rm docker.io/library/hello-world; echo chk57-socket-done"]
```

The commands are identical in all four, because the only variable under test is
the uid and the supplementary groups; `docker run --rm .../hello-world` is the
smallest thing that proves a container actually ran against the **host** daemon
rather than that the CLI merely printed its client version (which needs no
daemon and no permission at all). They are joined with `;` and not `&&`: a
refusal at `docker version` must not hide the lines after it, since the log is
the measurement — and it is why the runnable still exits 0 when the socket is
refused, which is the outcome expected of the baseline. `id` comes first in every
one of them because §2.2's uid question is open until then, and the gid on its
own line because that is the number the (a) variant needs.

**The remaining step-specific files are committed the same way** — each is
`check57/job.json` with only the fields below changed, the paths naming where
they go in that structure, and the setup block renders each into
`/tmp/check57-<name>.json` in its loop:

```jsonc
// check57/job-streaming.json -- 57a-3(a): image streaming, §1.2's prerequisite.
"taskGroups[0].taskSpec.runnables[0].container.enableImageStreaming": true

// check57/job-preload.json -- 57a-3(b): the boot disk 57a-3b built, instead of
// the `batch-cos` family. __PRELOAD_IMG__ is rendered from $P and $SA, so the
// job cannot point at an image the build step did not create; `sizeGb` stays at
// the base file's 50, which a disk created from the image is grown to.
"allocationPolicy.instances[0].policy.bootDisk.image": "__PRELOAD_IMG__"

// check57/job-teardown-fail.json -- 57a-8, the failure path.
"taskGroups[0].taskSpec.runnables[0].container.commands": ["bash", "-lc", "echo chk57-teardown-fail; exit 3"]

// check57/job-teardown-cancel.json -- 57a-8, the cancel path: long enough that
// the cancel lands while it is still RUNNING.
"taskGroups[0].taskSpec.runnables[0].container.commands": ["bash", "-lc", "echo chk57-teardown-cancel; sleep 600"]

// check57/job-teardown-crash.json -- 57a-8, the crashed-runner path. SIGKILL to
// the container's own process, which is a different exit than `exit 3`: this is
// the one that asks whether Batch's teardown survives an abnormal death.
"taskGroups[0].taskSpec.runnables[0].container.commands": ["bash", "-lc", "echo chk57-teardown-crash; sleep 2; kill -9 $$"]

// check57/job-signal-job.json -- 57a-12(a): the index/count claim set at the
// TaskSpec's own `environment`, which is the job-level place.
"taskGroups[0].taskSpec.environment": {"variables": {"BATCH_TASK_INDEX": "99", "BATCH_TASK_COUNT": "99"}}
"taskGroups[0].taskSpec.runnables[0].container.commands": ["bash", "-lc", "echo \"IDX=${BATCH_TASK_INDEX:-unset} COUNT=${BATCH_TASK_COUNT:-unset}\"; curl -s -m 3 -H \"Metadata-Flavor: Google\" http://metadata.google.internal/computeMetadata/v1/instance/id || echo no-metadata"]

// check57/job-signal-task.json -- 57a-12(b): the same claim set in
// `taskEnvironments`, the per-task place. Two files, not one: a single job that
// set both could not say which place the container's value came from.
"taskGroups[0].taskEnvironments": [{"variables": {"BATCH_TASK_INDEX": "99", "BATCH_TASK_COUNT": "99"}}]
"taskGroups[0].taskSpec.runnables[0].container.commands": ["bash", "-lc", "echo \"IDX=${BATCH_TASK_INDEX:-unset} COUNT=${BATCH_TASK_COUNT:-unset}\"; curl -s -m 3 -H \"Metadata-Flavor: Google\" http://metadata.google.internal/computeMetadata/v1/instance/id || echo no-metadata"]

// check57/job-quota.json -- 57a-11: raised until the sandbox's regional quota
// cannot place it, which is the point of the step. The type is a placeholder
// because c2-standard-60 is NOT offered in every region (us-east5 among them):
// the setup block discovers the largest vCPU count the region's own zones offer
// (__QUOTA_MT__), so the step cannot fail at submit for a type the region does
// not have -- a 400 that reads like the quota answer the step is after.
"allocationPolicy.instances[0].policy.machineType": "__QUOTA_MT__"
```

**Two things about that JSON are deliberate.** First, the runnable carries the **socket bind mount**
(`volumes: ["/var/run/docker.sock:/var/run/docker.sock"]`) — Batch documents
`volumes` as a bind mount from the host, and §1.1 needs it — because the base
image ships the Docker **CLI only, with no daemon** (`docs/base-image.md`:11:
"the Docker CLI (client only: a Cloud Run job has no Docker daemon)"); a
`docker` command in this container reaches the COS host's daemon **through this
mount**, and without it the CLI fails and any `&&` chain behind it never runs.
Second, the committed command is `id && echo first-line` and **not** a `docker
run`: with the mount present but unused, steps 2 and 3 measure the cold start
to the run's own first line — the number G2's 120 s budget is about — instead
of timing a Docker Hub pull for `postgres:17` on top of it, which would make
the medians that decide C4 and the go/no-go a measurement of something else.
**Step 4 is where a Docker command runs**, and it submits this same JSON with
one field changed — the plan commits both, `check57/job.json` and
`check57/job-docker.json`, and the setup block renders each into
`/tmp/check57.json` and `/tmp/check57-docker.json` through the same
substitutions — the replacement being exactly:

```json
"commands": ["bash", "-lc", "docker run --rm docker.io/library/postgres:17 postgres --version && docker run -d --name pg -e POSTGRES_PASSWORD=x docker.io/library/postgres:17 && until docker exec pg pg_isready -U postgres; do sleep 1; done && echo first-line"]
```

So every 57a submission carries the socket mount, 57a-4 and 57a-6 exercise the
path it opens, and the cold-start medians of 57a-2 and 57a-3 are measured with
a mount whose use begins only after the first line they time to.

**Guarded setup block (every step re-runs it; it hard-stops otherwise).** The
shape is Check 43's, because the traps are Check 43's: an empty `$P` makes
`gcloud` fall back to its ambient `core/project` — for the owner a `*-prod`
project — while `fugaro` falls back to the checkout's `fugaro.yaml`
(`internal/cli/cloud.go:53`), and this check **creates and deletes cloud
resources**.

```bash
# === Check 57: setup + guard. RE-RUN THIS WHOLE BLOCK at the start of every
# step (57a-0, 0b, 0c, 1..12, 57b-1..6, Restore). It is idempotent, and nothing
# below the three hard stops runs unless all three pass. ===
P=fugaro-dev                 # HARD-CODED sandbox project id; never belong, and never read from gcloud
L=us-east5                   # the sandbox installation's region
SA=fugaro-chk57              # the account THIS CHECK creates; NOT the installation's job account
J=fugaro-chk57               # the job-name prefix THIS CHECK uses; not the `fugaro-<slug>-<workflow>` shape
NET=default                  # the sandbox's network; a project with its own sets it here, with its subnetwork
SA_EMAIL=${SA}@${P}.iam.gserviceaccount.com   # what allocationPolicy.serviceAccount takes
IMG=                         # resolved below, from the installation's own config: never a hand-written tag

# ${VAR:?} everywhere: an empty variable is a hard error, never an empty
# argument gcloud would resolve from its ambient configuration.
[ "$P" = fugaro-dev ] && [ -n "$L" ] && [ -n "$SA" ] && [ -n "$J" ] && [ -n "$NET" ] \
  || { echo "Check 57 stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L SA=$SA J=$J NET=$NET must not be empty)" >&2; exit 1; }
DOCTOR=/tmp/chk57-doctor.json   # deliberately NOT check57*.json: the render check below globs those
fugaro doctor --project fugaro --gcp-project "${P:?}" --json > "${DOCTOR}" || true   # a failing check still prints the object; the parses below are the guard
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); p=d.get("project") or {}; sys.exit(0 if p.get("name")=="fugaro" and p.get("gcp_project")=="fugaro-dev" else 1)' "${DOCTOR}" \
  || { echo "Check 57 stopped: this is not the fugaro-dev installation (fugaro doctor --json names project $(python3 -c 'import json,sys; p=json.load(open(sys.argv[1])).get("project") or {}; print(p.get("name"), p.get("gcp_project"))' "${DOCTOR}" 2>/dev/null), or doctor refused the checkout: --project disagreeing with the checkout's project: is refused)" >&2; exit 1; }

# IMG is the installation's own base image, read out of that same object
# (`project.base_images.base`: what `fugaro image refresh` records for this
# installation, doctorProject in internal/cli/doctor.go:350 -- the BASE
# repository, infra.BaseRegistry, not the #362 proxy path). Nothing here pins
# a version by hand: a hand-written tag is a tag that can stop existing, and
# the probe would then fail for a setup reason on every submission.
IMG=$(python3 -c 'import json,sys; print(((json.load(open(sys.argv[1])).get("project") or {}).get("base_images") or {}).get("base",""))' "${DOCTOR}")
[ -n "$IMG" ] \
  || { echo "Check 57 stopped: the installation records no base image (fugaro doctor --json's project.base_images.base is empty); run fugaro image refresh in the sandbox checkout first" >&2; exit 1; }
gcloud artifacts docker images describe "${IMG:?}" --project="${P:?}" >/dev/null 2>&1 \
  || { echo "Check 57 stopped: the installation's base image ${IMG} cannot be described in ${P} (gcloud artifacts docker images describe). Fix that before submitting: otherwise every job below fails to pull for a setup reason, and the go/no-go measures nothing" >&2; exit 1; }
# BASE_REPO is the repository that image lives in, taken from IMG's own third
# path component (`<region>-docker.pkg.dev/<project>/<repository>/<image>:<tag>`)
# rather than written out again: 57a-0 grants the probe reader ON THIS
# repository, and a second hand-kept copy of the name is a second thing to drift.
BASE_REPO=$(printf '%s\n' "${IMG}" | cut -d/ -f3)
[ -n "$BASE_REPO" ] \
  || { echo "Check 57 stopped: could not read the repository out of IMG=${IMG}" >&2; exit 1; }
git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$' \
  || { echo "Check 57 stopped: this checkout's origin is not the sandbox (edgeappinc/fugarosandbox)" >&2; exit 1; }

# The committed JSON's five placeholders, resolved from the variables above;
# every submission in 57a and 57b uses a /tmp/check57*.json, never a committed
# file. Three are resolved rather than typed here:
#   DOCKER_GID   the socket's gid on the COS boot image -- 57a-6's baseline
#                submission reads it off the VM ("stat -c %g
#                /var/run/docker.sock"), after which it is exported and this
#                block re-run, so the (a) variant can be rendered with it.
#   QUOTA_MT     57a-11's machine type, discovered from the region because
#                c2-standard-60 is NOT offered in every region: the largest
#                vCPU count the region's own zones do offer. --zones is a
#                server-side query (a bare list is every type in every zone);
#                if the region has no -a/-b/-c the command fails and the
#                fallback below fills the placeholder anyway, which 57a-11's
#                FACT records as "not discovered".
#   PRELOAD_IMG  57a-3b's boot image, named after $SA like everything else this
#                check creates, in Batch's `Disk.image` path form.
DOCKER_GID=${DOCKER_GID:-}
QUOTA_MT=$(gcloud compute machine-types list --project="${P:?}" \
             --zones="${L:?}-a,${L:?}-b,${L:?}-c" --format='value(name,guestCpus)' 2>/dev/null | \
             sort -k2,2nr | head -1 | cut -f1)
QUOTA_MT=${QUOTA_MT:-c2-standard-60}
PRELOAD_IMG="projects/${P}/global/images/${SA}-preload"
render() { sed -e "s|__IMG__|${IMG:?}|g" \
              -e "s|__SA_EMAIL__|${SA_EMAIL:?}|g" \
              -e "s|__DOCKER_GID__|${DOCKER_GID}|g" \
              -e "s|__QUOTA_MT__|${QUOTA_MT:?}|g" \
              -e "s|__PRELOAD_IMG__|${PRELOAD_IMG:?}|g" \
              -e "s|global/networks/default|global/networks/${NET:?}|g" "$1"; }
render check57/job.json > /tmp/check57.json          # the probe's own template
for F in docker streaming preload socket-none socket-user0 socket-priv \
         teardown-fail teardown-cancel teardown-crash signal-job signal-task quota; do
  render "check57/job-${F}.json" > "/tmp/check57-${F}.json"
done
# The (a) variant only exists once the gid is known; 57a-6 renders it there.
[ -n "$DOCKER_GID" ] && render check57/job-socket-groupadd.json > /tmp/check57-socket-groupadd.json
grep -lq __ /tmp/check57*.json \
  && { echo "Check 57 stopped: a placeholder survived rendering (a __…__ field with no value). If it is __DOCKER_GID__, export DOCKER_GID=<gid> from 57a-6's baseline log and re-run this block" >&2; exit 1; }
for F in /tmp/check57.json /tmp/check57-docker.json /tmp/check57-streaming.json /tmp/check57-preload.json \
         /tmp/check57-signal-job.json /tmp/check57-signal-task.json \
         /tmp/check57-quota.json /tmp/check57-socket-none.json /tmp/check57-socket-user0.json \
         /tmp/check57-socket-priv.json /tmp/check57-teardown-fail.json /tmp/check57-teardown-cancel.json \
         /tmp/check57-teardown-crash.json; do
  [ -s "$F" ] || { echo "Check 57 stopped: rendering ${F} failed (run this block from the sandbox checkout, where the committed check57/ files are)" >&2; exit 1; }
done
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

0. **Create the check's service account, before anything submits a job.** Without
   this the first submission runs as the **default Compute Engine service
   account**, and every number below is then a number for an account the build
   will not use (§3.1's C21). Idempotent, so re-running it is safe:

   ```bash
   gcloud iam service-accounts describe "${SA_EMAIL:?}" --project="${P:?}" >/dev/null 2>&1 || \
     gcloud iam service-accounts create "${SA:?}" --project="${P:?}" --display-name="Check 57 probe (deleted by its Restore)"
   for ROLE in roles/logging.logWriter roles/batch.agentReporter; do
     gcloud projects add-iam-policy-binding "${P:?}" --member="serviceAccount:${SA_EMAIL:?}" \
       --role="${ROLE}" --condition=None --quiet >/dev/null || exit 1
   done
   # The base image is private: without this the agent's pull is denied and the
   # job fails for a setup reason on every submission. Granted ON THE
   # REPOSITORY (infra.BaseRegistry), never at project level: reading one
   # repository must not imply reading the project's others.
   gcloud artifacts repositories add-iam-policy-binding "${BASE_REPO:?}" \
     --project="${P:?}" --location="${L:?}" \
     --member="serviceAccount:${SA_EMAIL:?}" --role="roles/artifactregistry.reader" --quiet >/dev/null || exit 1
   ```

   `roles/logging.logWriter` is what lets the job write its own lines (step 10
   reads them back); `roles/batch.agentReporter` is §3.2's other grant, the one
   Batch needs to report task state to the agent; and the repository reader
   binding is what lets the agent pull the private base image at all — a grant
   the probe cannot run without and the first thing to check when a job fails
   before its first line. **FACT:** the bindings, read
   back with `gcloud projects get-iam-policy` and
   `gcloud artifacts repositories get-iam-policy "${BASE_REPO:?}"
   --location="${L:?}"`, and that the *job* — not the
   owner's account — is what `allocationPolicy.serviceAccount` names (step 1
   checks the same thing from the job's side: the run's `id` must not be the
   default account). Step 9 adds the design's other grants under a Condition and
   is where C21's granularity is examined; this step is only what the probe
   itself needs.

0b. **Egress, before anything submits a job — without it nothing below runs at
   all.** The job JSON sets `noExternalIpAddress: true` (the design's network
   posture, §1.10's C7), and Batch supplies no egress of its own: with neither a
   Cloud NAT nor Private Google Access behind the subnet, the VM has no route
   out, the agent's image pull is denied, and **every** submission below fails
   before its first line — a probe that measures nothing and a go/no-go with no
   data. Private Google Access alone would reach Artifact Registry (a Google
   API), but not step 4's `docker.io` pull, which is not one; the check
   therefore needs NAT. Like the installation, which puts NAT at the
   installation level (§1.7), it **borrows** a NAT where one already exists and
   creates its own only when there is none — named `${SA}-*`, which is how the
   Restore finds it and the reason it can never find the project's own. It
   borrows a NAT only when that NAT is **on the probe's own network in the
   probe's region** *and* its own rules say it covers every subnet: a NAT on
   another network cannot carry this VM's egress at all, and editing the
   project's router to add the subnet
   (`gcloud compute routers nats update … --nat-custom-subnet-ip-ranges`) would
   be a change to something the Restore may not undo, so a NAT elsewhere, an
   unreadable one, or one that lists its subnets by name sends the check to its
   own `${SA}-router`/`${SA}-nat` instead:

   ```bash
   ROUTER=${SA}-router
   NAT=${SA}-nat
   # A borrow is a borrow only when the NAT certainly covers ${NET}'s ${L}
   # subnetwork: sourceSubnetworkIpRangesToNat ALL_SUBNETWORKS_ALL_IP_RANGES or
   # ALL_SUBNETWORKS_ALL_PRIMARY_IP_RANGES (the VM's egress uses its primary
   # address). A NAT that lists its subnets by name, or one whose rules cannot
   # be read, is NOT borrowed -- editing the project's router is a change the
   # Restore below cannot undo, so this check makes its own pair instead.
   #
   # There is no project-wide NAT list: `gcloud compute routers nats list` takes
   # --router and nothing else (checked 2026-10-15), so the search is routers
   # first, then the NATs of each. And the routers are the ones on ${NET} in
   # ${L}: a NAT on another network cannot carry this VM's egress, so borrowing
   # one would send every submission below out with no route while the check
   # read it as a job failure.
   borrowed=""
   for R in $(gcloud compute routers list --project="${P:?}" --regions="${L:?}" \
                --filter="network~global/networks/${NET:?}\$" --format='value(name)'); do
     if gcloud compute routers nats list --project="${P:?}" --region="${L:?}" --router="${R}" \
          --format='value(sourceSubnetworkIpRangesToNat)' 2>/dev/null | \
          grep -Eq '^ALL_SUBNETWORKS_ALL_(IP|PRIMARY_IP)_RANGES$'; then
       borrowed="${R}"
       break
     fi
   done

   if [ -n "${borrowed}" ]; then
     echo "57a-0b: borrowing ${borrowed}'s NAT on ${NET}/${L} (its rules cover every subnet); creating none (this check deletes only ${SA}-*)"
   elif gcloud compute routers describe "${ROUTER}" --project="${P:?}" --region="${L:?}" \
          --format='value(network)' 2>/dev/null | grep -q "global/networks/${NET:?}\$" && \
        gcloud compute routers nats describe "${NAT}" --project="${P:?}" --region="${L:?}" \
          --router="${ROUTER}" --format='value(sourceSubnetworkIpRangesToNat)' 2>/dev/null | \
          grep -Eq '^ALL_SUBNETWORKS_ALL_(IP|PRIMARY_IP)_RANGES$'; then
     # A re-run of this step. The ROUTER ALONE IS NOT THE PAIR: an earlier run
     # that died between the two creates leaves a router with no NAT, and every
     # submission below would then fail for want of egress -- a setup failure
     # the check would read as a job failure. Both halves are required, and the
     # router has to be on ${NET}.
     echo "57a-0b: this check's own ${ROUTER}/${NAT} is already in place; reusing it"
   else
     # Named ${SA}-*, so this is a change to nothing but this check's own
     # leftovers. NAT first: a router that still has a NAT on it cannot be
     # deleted.
     gcloud compute routers nats delete "${NAT}" --project="${P:?}" --region="${L:?}" \
       --router="${ROUTER}" --quiet >/dev/null 2>&1 || true
     gcloud compute routers delete "${ROUTER}" --project="${P:?}" --region="${L:?}" \
       --quiet >/dev/null 2>&1 || true
     gcloud compute routers create "${ROUTER}" --project="${P:?}" --region="${L:?}" --network="${NET:?}" || exit 1
     gcloud compute routers nats create "${NAT}" --project="${P:?}" --region="${L:?}" --router="${ROUTER}" \
       --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges || exit 1
   fi
   ```

   (Neither `gcloud compute routers create` nor `gcloud compute routers nats
   create` takes labels in the pinned gcloud — the `${SA}-*` name is the marker,
   exactly as it is for `${SA}-preload` in the Restore below.)

   **FACT:** which of the three outcomes ran — borrowed an existing NAT, reused
   this check's own pair, or created one — the name of the router it borrowed
   from and the `sourceSubnetworkIpRangesToNat` value it accepted, and, on the
   reuse branch, that the **NAT** was there and not only the router. A NAT that
   exists but is not a borrow candidate (it is on another network, its rules
   list subnets by name, or none can be read) is **not** a setup failure and not
   a reason to touch the project: the check creates its own
   `${SA}-router`/`${SA}-nat`, which the Restore deletes. **Private Google
   Access is not the remedy** — it reaches Google APIs only, so steps 4 and 6's
   `docker.io/library/postgres:17` and `hello-world` pulls fail behind it and
   the go/no-go measures nothing — and it is the *project's* subnet setting,
   which the Restore (deleting only `${SA}-*`) must leave alone rather than turn
   back off by hand.

0c. **Image streaming's prerequisite, before step 3a needs it.** Streaming is
   not a Batch-side flag alone: it is served by the
   **`containerfilesystem.googleapis.com`** API, which a project need not have
   enabled (§1.2, §1.9's provisioning task). Without it step 3a's submission is
   rejected, and the (a) half of the cold-start comparison — the half C4 prefers
   — measures nothing. Enabled here, and *recorded* here, because whether this
   check may turn it back off depends on whether the check turned it on:

   ```bash
   if gcloud services list --project="${P:?}" --enabled \
        --filter='config.name=containerfilesystem.googleapis.com' --format='value(config.name)' | grep -q .; then
     echo "57a-0c: containerfilesystem.googleapis.com was ALREADY enabled; this check leaves it on"
     rm -f /tmp/chk57-api-enabled-by-check
   else
     gcloud services enable containerfilesystem.googleapis.com --project="${P:?}" || exit 1
     touch /tmp/chk57-api-enabled-by-check   # Restore step 7 disables it only because of this file
     echo "57a-0c: enabled containerfilesystem.googleapis.com; Restore step 7 disables it again"
   fi
   ```

   The marker is a file, not a variable, because the setup block is re-run from a
   fresh shell at every step: what happened in 57a-0c has to survive that.
   **FACT:** whether the API was already enabled, and — if it was — that the
   installation enables it deliberately (§1.2's prerequisite), because that is
   the difference between a raw project and a provisioned one.

1. **Baseline (Cloud Run).** On the sandbox, `fugaro run --project fugaro
   --gcp-project "${P:?}"` a no-change task (Check 41's own prompt), and time
   launch-to-first-log-line (`fugaro logs RUN`, the first `started` line), five
   times. **This is Check 41's measurement in shape and deliberately not its
   number**, and the two must not be subtracted from one another:
   - **The endpoint differs.** Check 41
     (`docs/gcp-live-checklist.md`, "Check 41") times from the run id's launch
     timestamp to **the first Cloud Logging entry of the execution**; this check
     times to the first line of the run's own log view, which is a later entry
     and a larger number.
   - **The spacing differs.** Check 41 spaces its runs **at least 35 minutes
     apart**, so each starts on an idle container; this check does not need that,
     because what the 120 s budget is measured against (steps 2 and 3) gets a
     fresh VM every time, and 57a-1 exists only to anchor those numbers to the
     backend Fugaro runs today.
   **FACT:** the five times, their median, and the note that the endpoint is this
   one — Check 41's published 10.5 s median is not comparable with it.
2. **Cold start, plain pull.** Submit five fresh `$J` jobs from the committed
   JSON, each with its own index and timestamp in the **name** and record
   submission-to-first-line for each. The committed file's labels are fixed
   (`fugaro=managed`, `fugaro_chk=57`) and the check does **not** add §2.4's
   per-run `fugaro_run` label: that label exists so the reaper can tell one
   *real* run's VM from another's, and the check's own `fugaro_chk=57` is what
   tells its jobs and VMs from everyone's — it is what step 8's listing and the
   Restore filter on, so nothing here needs a per-run label:

   ```bash
   for i in 1 2 3 4 5; do
     gcloud batch jobs submit "${J:?}-plain-${i}-$(date +%s)" --project="${P:?}" \
       --location="${L:?}" --config /tmp/check57.json
     # record the submission time, then the run's first log line, before the next
   done
   ```

   A Batch job is single-use once it reaches
   `SUCCEEDED` or `FAILED` (§2.3), so five samples are five jobs — not five
   submissions of one.
   **FACT:** the five times, the median, and the delta over step 1.
3. **Cold start, the two alternatives.** (a) **Image streaming**: submit
   `check57/job-streaming.json` — the committed JSON with `enableImageStreaming:
   true` on the runnable, and nothing else — as
   `--config /tmp/check57-streaming.json`; the `containerfilesystem` API is
   enabled by 57a-0c and the image is in the job's own region (both prerequisites
   are on the design's list, §1.2). Five fresh jobs, as in step 2.
   (b) **The preloaded boot disk**: the boot image **3b builds below**, with
   `fugaro-base` already in the Docker data root, submitted as
   `--config /tmp/check57-preload.json` — the committed JSON whose only change
   is `bootDisk.image`, pointing at the image 3b creates and the Restore
   deletes. Five fresh jobs, as in step 2.

   **3b — build the preloaded image.** It is a check artefact, not a build
   change (C19 puts the real one in `fugaro init`'s Batch stage), so the check
   builds and deletes it itself, named `${SA}-preload` like everything else it
   creates. The base is the image Batch's own `Disk.image: batch-cos` resolves
   to, so the result differs from the default boot disk **only** by the Docker
   store's contents — which is what makes the (b) comparison a comparison:

   ```bash
   # The instance runs as THIS CHECK's account ($SA, which 57a-0 gave
   # roles/artifactregistry.reader on the base repository), never the default
   # Compute Engine SA; it carries fugaro_chk=57 so the Restore's VM sweep finds
   # it if anything below fails halfway.
   #
   # PRELOAD_OK gates (b)'s five submissions: a build that does not finish
   # leaves no image, and a job whose bootDisk.image does not exist would
   # measure a setup failure instead of a cold start.
   PRELOAD_OK=""
   PRELOAD_BASE=$(gcloud compute images describe-from-family batch-cos --project="${P:?}" --format='value(name)' 2>/dev/null) \
     || echo "Check 57: could not resolve the batch-cos family; the (b) half has no base image" >&2
   if [ -n "${PRELOAD_BASE}" ]; then
     gcloud compute instances create "${SA:?}-preload-build" --project="${P:?}" --zone="${L:?}-b" \
       --image="${PRELOAD_BASE:?}" --machine-type=e2-medium --no-address \
       --service-account="${SA_EMAIL:?}" --scopes=cloud-platform --labels=fugaro_chk=57 \
       --metadata=startup-script="$(printf '%s\n' \
           '#!/bin/bash' 'set -e' \
           "docker-credential-gcr configure-docker --registries=${L}-docker.pkg.dev" \
           "docker pull '${IMG:?}'" \
           "docker image inspect '${IMG:?}' >/dev/null" \
           'echo chk57-preload-built')" \
       || echo "Check 57: could not create ${SA}-preload-build" >&2
     # The pull is what this waits for, and the instance has no external IP, so
     # the serial port is where its startup script's last line is read from.
     # Bounded at ten minutes: a pull that fails never prints the marker, and
     # this loop must end anyway.
     for _ in $(seq 1 60); do
       gcloud compute instances get-serial-port-output "${SA:?}-preload-build" --project="${P:?}" \
         --zone="${L:?}-b" --port=1 2>/dev/null | grep -q chk57-preload-built && { PRELOAD_OK=yes; break; }
       sleep 10
     done
     gcloud compute instances stop "${SA:?}-preload-build" --project="${P:?}" --zone="${L:?}-b" >/dev/null 2>&1 || true
     if [ -n "${PRELOAD_OK}" ]; then
       gcloud compute images create "${SA:?}-preload" --project="${P:?}" \
         --source-disk="${SA:?}-preload-build" --source-disk-zone="${L:?}-b" \
         --labels=fugaro_chk=57 || PRELOAD_OK=""
     fi
     gcloud compute instances delete "${SA:?}-preload-build" --project="${P:?}" --zone="${L:?}-b" --quiet >/dev/null 2>&1 || true
   fi
   [ -n "${PRELOAD_OK}" ] \
     || echo "Check 57: 57a-3b built no image -- run (a)'s five submissions and SKIP (b)'s: a job whose bootDisk.image does not exist measures a setup failure, not a cold start. The FACT is this build's own error text" >&2
   ```

   A build that fails is **not** a failed check and not a licence to skip the
   question: it is a FACT ("the preloaded-image half has no number, because the
   image could not be built"), and it is the same question Google hedges on
   ("might not be supported"), so record the failure's own text.

   **FACT:** the five times, the median and the delta over steps 1 and 2 for
   **each** alternative; whether streaming accepted the job at all; whether the
   custom boot image was **accepted** by Batch and, if it was, whether the
   container's start was actually faster — the two halves of Google's hedge; and
   for 3b, the base family's image name, whether the startup script's pull
   succeeded, and the image's own id. Note what this step does **not** settle:
   neither job runs Docker, so streaming's compatibility with the socket path of
   step 6 is **not** measured here — step 6's combination submission is what
   measures it, and §2.2's claim stays UNVERIFIED until it does. **The
   recommendation for C4 follows these two numbers, not this document** (§1.2,
   §1.9).
4. **Docker inside a run.** Submit `check57/job-docker.json` — the committed
   JSON above with only `commands` replaced by the Docker chain quoted after
   it, the socket mount unchanged and **no `options` field** — as
   `--config /tmp/check57-docker.json`, so the container's Docker CLI talks to the
   host daemon: `docker run --rm docker.io/library/postgres:17 postgres
   --version`, then a small Postgres container, then `pg_isready` until it
   answers. **This is also the no-`options` baseline**: with nothing supplying a
   supplementary group, a container left as the image's own `fugaro` (uid 1000)
   **cannot** open the socket, and the expected result is the CLI's `permission
   denied` — a job that fails at its first `docker run`. That refusal is a
   result, not a failed check: it is the number step 6's three variants have to
   beat, and a job here that *succeeds* is the finding that the uid is not 1000
   (§2.2's open question, answered by step 6's baseline `id`). **FACT:** whether the
   socket was reachable at all (the CLI's own error if not); which permission
   path let the container open it (container as
   root, a supplementary group via the runnable's `options`, or a socket group
   baked into the boot image — §2.2's three orderings). It does **not** measure
   the pull time or the time to `pg_isready`: a job refused at its first
   `docker run` never reaches either, and asking a refused job for those two
   numbers is asking for numbers it cannot have. Step 6 runs this same Docker
   chain again under the variant that worked, and those two times come from
   there.
5. **The cost of one run.** Read the billing export (or the console's cost
   table) for one step-2 job and compare it with what the run's own record says
   (`cost.compute_usd` on a real run, 57b-5; on the probe, compute the estimate
   by hand from the machine type and the elapsed time). **FACT:** the actual
   billed vCPU/memory seconds, the disk, the in-use external IP if the job has
   one, NAT if used, the estimate, and how much the **one-minute minimum** added
   (a Compute Engine instance bills a one-minute minimum and then per second,
   §5.2).
6. **The socket's permission path, in detail — six submissions, and this is
   where the go/no-go gets a socket result rather than a uid.** Submit the four
   committed `check57/job-socket-*.json` files of §7.2, each with its own run id
   the way step 2 does, in this order:

   ```bash
   # (1) the baseline. No "options" at all: it reads the uid and the socket's gid
   #     off the VM, and it is expected to be refused.
   gcloud batch jobs submit "${J:?}-none-$(date +%s)" --project="${P:?}" --location="${L:?}" \
     --config /tmp/check57-socket-none.json
   # read that run's log lines and take the gid off the "stat" line, then:
   #   export DOCKER_GID=<the gid stat printed>
   #   re-run the setup block          # it renders the (a) variant with the gid
   # (2) the three ordered variants of §2.2.
   for V in groupadd user0 priv; do
     gcloud batch jobs submit "${J:?}-${V}-$(date +%s)" --project="${P:?}" --location="${L:?}" \
       --config "/tmp/check57-socket-${V}.json"
   done
   ```

   Then two more — submissions (5) and (6) of the six, the loop above being (1)
   to (4). They are **built here from the rendered files** rather than
   committed, because which variant worked is only known at this point. Set `V`
   to the variant that got a container in — `groupadd`, `user0`, `priv`, or
   `none` if none did (with `none`, submission (5) is still worth its answer:
   streaming over the baseline's options-less container, which is a refusal):

   ```bash
   # (5) the combination that decides §2.2's streaming claim: the variant that
   #     worked, with streaming ON. Streaming honours only five subfields, and
   #     `options` is not one of them, so this also says whether the socket
   #     survives streaming -- which is the whole of §2.2's "with streaming the
   #     only socket path left is the baked-in group". Its commands are the
   #     socket probe's own, so "it ran docker" is what is read back.
   # (6) the timing run: the same variant's `options` with STEP 4's Docker
   #     chain, which is where the postgres:17 pull time and the time to
   #     `pg_isready` are measured. 57a-4 cannot measure them (it is expected to
   #     be refused at its first `docker run`), so this submission is what the
   #     FACT lines for both steps come from.
   python3 - "${V:?}" <<'PY'
   import json, sys
   v = sys.argv[1]
   src = f"/tmp/check57-socket-{v}.json"
   j = json.load(open(src))
   c = j["taskGroups"][0]["taskSpec"]["runnables"][0]["container"]
   c["enableImageStreaming"] = True
   json.dump(j, open("/tmp/check57-socket-streaming.json", "w"), indent=2)
   j = json.load(open(src))
   j["taskGroups"][0]["taskSpec"]["runnables"][0]["container"]["commands"] = [
       "bash", "-lc",
       "t0=$(date +%s); docker pull docker.io/library/postgres:17; t1=$(date +%s); "
       "echo \"chk57-pull-seconds=$((t1-t0))\"; "
       "docker run -d --name pg -e POSTGRES_PASSWORD=x docker.io/library/postgres:17; t2=$(date +%s); "
       "until docker exec pg pg_isready -U postgres; do sleep 1; done; t3=$(date +%s); "
       "echo \"chk57-ready-seconds=$((t3-t2))\"; echo chk57-docker-timed"]
   json.dump(j, open("/tmp/check57-socket-timed.json", "w"), indent=2)
   PY
   for F in streaming timed; do
     gcloud batch jobs submit "${J:?}-${V}-${F}-$(date +%s)" --project="${P:?}" --location="${L:?}" \
       --config "/tmp/check57-socket-${F}.json"
   done
   ```

   **FACT, per submission**, read out of its log lines, never inferred: the uid
   `id` reports (§2.2's open question — this is what settles it); the gid `stat`
   reports (and whether it is the same number on every VM); whether `docker
   version` reached the **daemon** or only printed the client's own version;
   whether `docker run --rm docker.io/library/hello-world` actually ran; and the
   CLI's exact error when it did not. Then, separately: whether Batch **accepted**
   each `options` string at all — a rejected one fails at `jobs.submit` with a
   400 and never reaches a VM, which is itself the answer to "does `options`
   carry `--group-add`". And for the two built-here submissions: whether the
   streaming combination ran a container (the answer to §2.2's streaming claim,
   which is UNVERIFIED until this line exists), and the pull and `pg_isready`
   times from the timing run. The go/no-go is: **at least one of (a) or (b)
   gives a running container via the host socket, or the socket design has no
   uid-preserving path** — and if only (b) works, §2.2's ordering drops to
   `--user 0` with the ownership consequence recorded; (c) is expected to be
   refused like the baseline, so "only (c) works" is not an outcome this step
   can produce (if it did, see §2.2). It is also why streaming (which discards
   `options`) is only combinable with the socket if the **baked-in group** path
   works — and submission (5) is what says whether it is: with streaming on,
   none of (a), (b) or (c) exists, and that is the result step 3's streaming
   runs and this step's number have to agree on.
7. **(Optional) Preemption.** Run one `$J` job with a spot provisioning model
   and preempt it (or let it happen). The notice period is 0 seconds by default,
   or 120 seconds if the job asks for it, and the shutdown afterwards is best
   effort and can take up to 30 seconds (MIG/spot docs, §1.3). **FACT:** whether
   the job asked for the 120-second notice, whether the runner received SIGTERM,
   whether it reached `finalize`, and the time from the preemption notice to the
   VM's death.
8. **Teardown on every exit path.** For each of: success; failure (a runnable
   that exits non-zero); **cancel** (`gcloud batch jobs cancel` on the job the
   cancel iteration submitted — `jobs.cancel`, **not**
   `jobs.delete`: delete erases the job and its history, which is why §1.4 makes
   cancel a `cancel` and the reaper a `delete`); and a crashed runner (the
   committed `check57/job-teardown-crash.json`, which SIGKILLs the container's own
   process) — **a fresh job each, since one job runs once** — submit each, then list
   the surviving resources:

   ```bash
   # Four fresh jobs, since one job runs once (§2.3): check57/job.json's own
   # command is the success path and needs no variant file.
   #
   # N is the name THIS iteration submitted -- the cancel below needs it, and
   # nothing else in the block knows it (a bare ${NAME} would be an empty
   # argument, which is the "gcloud falls back to its ambient config" trap the
   # setup block's ${VAR:?} rule exists for).
   for V in "" fail cancel crash; do
     N="${J:?}-teardown${V:+-$V}-$(date +%s)"
     gcloud batch jobs submit "${N}" --project="${P:?}" --location="${L:?}" \
       --config "/tmp/check57${V:+-teardown-$V}.json"
     if [ "${V}" = cancel ]; then
       # Cancel a RUNNING job, which is the path being tested: wait for the
       # state rather than racing the submission. A job that never reaches
       # RUNNING is itself a FACT.
       until gcloud batch jobs describe "${N}" --project="${P:?}" --location="${L:?}" \
               --format='value(status.state)' | grep -q '^RUNNING$'; do sleep 5; done
       gcloud batch jobs cancel "${N}" --project="${P:?}" --location="${L:?}"
     fi
   done

   # ... then, per path, the surviving resources:
   gcloud compute instances list --project="${P:?}" --filter='labels.fugaro_chk=57'
   gcloud batch jobs list --project="${P:?}" --location="${L:?}" --filter='labels.fugaro_chk=57'
   ```

   The instance filter is `labels.fugaro_chk=57`, **not** `labels.fugaro=managed`
   and not `job_id`: `Job.labels` never reach a VM, only
   `allocationPolicy.labels` do (§2.4, Batch REST reference, checked
   2026-10-15), so a design or a check that sets only `Job.labels` lists
   nothing and reads it as "no survivors" precisely when a VM has leaked.
   **FACT** per path: the job's final state (and, for cancel, what
   `gcloud batch jobs describe` reports afterwards and that `jobs.get` **still
   returns the job** — a cancel, not a delete, §1.4; the Fugaro-side reading of
   that, `Execution` returning a terminal state, is 57b-3 and needs the
   backend), whether any VM survived, and for how long after the job reached
   its terminal state. This is the answer to "does the reaper delete
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

   # C22's Condition question, asked for real: add a binding whose Condition
   # narrows Batch job editing to Fugaro-shaped names, read it back, then remove
   # it. An accepted-and-returned Condition is the answer this step exists for;
   # a 400 or an error from add-iam-policy-binding means Conditions on this
   # role/resource are not available and C22's "worth attempting" stays
   # UNVERIFIED. The binding is on the check's own account, and it is removed
   # before the step ends, so nothing of it survives into Restore.
   # Double quotes, NOT single: the expression has to carry this project's and
   # this region's real ids, and a single-quoted string would hand gcloud the
   # literal text `${P:?}` — a Condition that names a project nobody has, which
   # would read as "accepted" and mean nothing (§9's citation of this step).
   COND="title=chk57-fugaro-jobs,expression=resource.name.startsWith(\"projects/${P:?}/locations/${L:?}/jobs/fugaro-\")"
   gcloud projects add-iam-policy-binding "${P:?}" \
     --member="serviceAccount:${SA_EMAIL:?}" --role="roles/batch.jobsEditor" \
     --condition="${COND}" --quiet
   gcloud projects get-iam-policy "${P:?}" --format=json | \
     python3 -c 'import json,sys; [print(b["role"], b.get("condition")) for b in json.load(sys.stdin)["bindings"] if "fugaro-chk57" in str(b["members"])]'
   gcloud projects remove-iam-policy-binding "${P:?}" \
     --member="serviceAccount:${SA_EMAIL:?}" --role="roles/batch.jobsEditor" \
     --condition="${COND}" --quiet
   ```
10. **The log filter, read raw.** Read one raw entry of a `$J` job — **filtered
    to that job**, not to the whole project, so the entry this step records is
    the check's own: the resource type alone returns whatever else the sandbox
    logs, and `limit=1` would then read someone else's entry. Two reads, because
    the step asks two questions: one by the job's UID (does `labels.job_uid`
    exist and equal it), one by this check's label (do the job's own labels
    reach the entry):

    ```bash
    NAME=$(gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
      --filter="labels.fugaro_chk=57" --format='value(name)' --limit=1)
    # JOB_UID, never UID: UID is a READONLY bash variable (the shell's own user
    # id), and assigning to it aborts a non-interactive script -- which is how
    # this block is run (`bash check57.sh`), so `UID=$(...)` would end the check
    # on that line rather than measure anything.
    JOB_UID=$(gcloud batch jobs describe "${NAME:?}" --project="${P:?}" --location="${L:?}" --format='value(uid)')
    gcloud logging read "resource.type=\"batch.googleapis.com/Job\" AND labels.\"job_uid\"=\"${JOB_UID:?}\"" \
      --project="${P:?}" --limit=1 --format=json
    gcloud logging read 'resource.type="batch.googleapis.com/Job" AND labels.fugaro_chk=57' \
      --project="${P:?}" --limit=1 --format=json
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
11. **(Optional, quota.) Queueing, and what a queued job does.** Submit
    `check57/job-quota.json` — the committed JSON whose machine type is the
    largest vCPU count **the region's own zones offer** (`__QUOTA_MT__`, resolved
    by the setup block; not a hard-coded `c2-standard-60`, which is not offered
    in every region — us-east5 among them — and a type the region does not have
    fails at `jobs.submit` with a 400 that reads like the quota answer this step
    is after) — as `--config /tmp/check57-quota.json` (or fill the quota with
    step-2 jobs) and watch it: the state should be
    `QUEUED`, and Batch holds a job in `QUEUED` for at most **2 days** before
    failing it (`cloud.google.com/batch/quotas`, §1.4). If even that type is
    placed, record it: the regional quota was not the constraint, and that is an
    answer too. **FACT:** the state observed, how long it sat there, what the API
    said, and whether the machine type came from the discovery or from the
    fallback. Batch has no
    queue-deadline *field* — the design's queue deadline (§1.4, C8) is a
    Fugaro-side reaper action, so this step measures the state it acts on, not
    a Google knob. Cancel it and continue.
12. **Is the platform's own signal forgeable by a job?** C12's recommendation
    (§2.5) makes the Batch backend answer "am I on you?" from
    `BATCH_TASK_INDEX`/`BATCH_TASK_COUNT`, which Batch injects into every task.
    If a job's own environment can set or overwrite them, that signal is only
    as good as the launch path — still better than a Fugaro variable, but the
    design has to say so. **Two submissions, one per place Batch documents
    environment**, because a single job that set both could not say which place
    the container's value came from: `check57/job-signal-job.json`, whose
    `taskSpec.environment` sets `BATCH_TASK_INDEX=99` and
    `BATCH_TASK_COUNT=99`, and `check57/job-signal-task.json`, which sets the
    same pair in `taskEnvironments[0].variables` and nothing at TaskSpec level.
    Both carry the same container command:

    ```bash
    bash -lc 'echo "IDX=${BATCH_TASK_INDEX:-unset} COUNT=${BATCH_TASK_COUNT:-unset}"; \
              curl -s -m 3 -H "Metadata-Flavor: Google" \
                http://metadata.google.internal/computeMetadata/v1/instance/id || echo "no-metadata"'
    ```

    — as `--config /tmp/check57-signal-job.json` and
    `--config /tmp/check57-signal-task.json`, one at a time. **FACT:** the values
    the container saw — the injected ones (index 0 of a one-task job, count 1),
    the job's `99`, or nothing (a var Batch strips) — **named per place**, since
    each submission answers for exactly one of them. The metadata line is
    **not** a Private Google Access result: the metadata server is the link-local
    `169.254.169.254`, which no external IP, NAT or PGA setting affects (§3.3),
    so what it records is whether a job can read its own VM's identity from the
    metadata server — the mechanism §3.3's token comes from — from a VM with no
    external IP. PGA's own question is step 0b's, and its answer is the pulls in
    steps 2 to 4.

### 57b. The post-build acceptance run (after the enable task)

These six need `backend: cloud-batch` selectable, so they run **last**
(§8.1's task 9 is the enable task and task 10 is this section's second half).
They are the design's acceptance criteria, not its go/no-go.

**These runs are the installation's jobs, and their labels are the
installation's** — `fugaro=managed`, `fugaro_repo=<the sandbox repository>`,
`fugaro_workflow=<variant>` and the per-run `fugaro_run` (§2.4), written by the
launcher when it creates the job. They **cannot** carry `fugaro_chk=57`: the
check does not create these jobs,
and Batch v1 has no method that adds a label to an existing job (create, get,
list, cancel, delete — no patch). Restore therefore sweeps them **by the names
this section records**, not by the repository label and not by the check's (its
step 1): a filter on the installation's labels would also match every other run
of the same repository — the owner's, a scheduled one, an earlier check's — and
the restore deletes only what this check created. So **record each 57b run's job
name in the FACT lines**: that name is both how the restore finds the job and
how the assertion counts it, and a name missing from the list is a job left
behind. **Record each run's `fugaro_run` label value as well**, read back off
the job rather than reconstructed
(`gcloud batch jobs describe RUN --format='value(labels.fugaro_run)'`): a job's
name never reaches its VMs — `Job.labels` do not, only `allocationPolicy.labels`
do (§2.4) — so that label value is the **only** handle the Restore's VM sweep
has on the VMs these runs leave, and it cannot be derived from the name (the
name ends in a 12-hex hash of the run id, while the label carries the slug and
the run id, truncated with a short hash when the two are too long for 63
characters — §2.4). A run whose label value is missing from the list is a VM the
restore cannot find.

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
6. **The fail-closed marker.** On a VM, an environment with no
   **platform-injected** marker (no `BATCH_TASK_INDEX`/`BATCH_TASK_COUNT`, and a
   metadata server that does not answer) must leave §2.5's four checks **closed**:
   with nothing claiming the process, the resolved answer is "in the cloud", so
   the launcher-controlled checks apply — not "then it must be local", which is
   what falling open would mean. A `FUGARO_*` variable must **not** be able to
   stand in for the platform's marker: it is not a signal, it is input the run
   itself can write (§2.5, and 57a-12 for whether the injected pair can be
   overwritten). A VM's
   process is never in **local mode** — that is the CLI's `--task-file`
   invocation, which no job uses — so the markerless case here is always the
   cloud one: the design's §2.5 fix exists because four call sites today read
   `OnCloudRun` and treat "not Cloud Run" as "not in the cloud". **FACT:**
   whether all four checks were closed without the marker, and which of them the
   run visibly hit.

**FACT lines to record:** every `FACT:` above; the `fugaro version` and the
date; the machine type and boot-image version; the median launch-to-first-line
for 57a-1, -2 and -3 (both alternatives, and whether 3b's image was built at
all); whether Docker worked and by which permission path, and whether it still
worked with streaming on (57a-6's combination submission); the postgres pull
time and the time to `pg_isready` (57a-6's timing run, not 57a-4); the
billed cost of one run and the estimate; the final state and the survivor list
for each 57a-8 and 57b-4 path; the IAM bindings of 57a-9; the raw log entry of
57a-10; the injected-signal values and the metadata answer of 57a-12; the job
name **and the `fugaro_run` label value** of every 57b run (the Restore needs
both: the name for the job, the label value for its VMs); which branch 57a-0b
took for egress; and whether `fugaro logs` found the Batch run's lines.

**Paste back:** the FACT lines, verbatim, plus any correction the run found and
**57a's go/no-go: build the VM backend only if 57a-2 or 57a-3's median is at
most 120 s above 57a-1's** (generic-tool §1, G2), *and* the socket path of
57a-4/57a-6 worked. If the cold-start budget is missed on every variant the
documented answer stays `fugaro-services` plus `image.apt`; if the socket path
failed, the design's C2 (§1.1, §1.10) is what reopens.

**Restore.** ⚠ CONFIRM, sandbox project only. This deletes **only what the
check created**, and its last step is an assertion that nothing of the check's
is left. The account it deletes is the one this check created, never the
installation's job account and never the default Compute Engine SA — deleting
either would break the installation the check must leave untouched.

**Re-run the setup block first** — this is a new shell, and the deletes below
would otherwise interpolate an empty `$P`, `$L` or `$SA`, which is exactly how
`gcloud` falls back to its ambient `*-prod` configuration.

```bash
# The job names 57b recorded (its FACT line lists one per run, e.g.
# fugaro-<slug>-<workflow>-<12 hex>), space-separated. EMPTY IS CORRECT for a
# restore after 57a alone: nothing of 57b's was created. Set it before running
# the block, and do not put anything in it that this check did not start.
RUNS=""
# The `fugaro_run` label values of those same runs, read back off each job with
# `gcloud batch jobs describe RUN --format='value(labels.fugaro_run)'` (57b's
# FACT line records one per run), space-separated. These are the ONLY handle on
# 57b's VMs: a job's name never reaches its VMs (Job.labels do not, only
# allocationPolicy.labels do, §2.4), and the label value cannot be derived from
# the name -- the name ends in a 12-hex hash of the run id, the label carries
# the slug and the run id, truncated with a short hash when they are too long
# for 63 characters (§2.4). EMPTY IS CORRECT only when RUNS is empty too.
RUN_LABELS=""

# 1. Every job the check submitted: 57a-2, -3 (both alternatives), -4, -6 (its
#    four socket variants and the two submissions built from them), -7, -8, -11
#    and -12 (both places) -- the probe's -- and 57b-1, -3 and -4 (real runs).
#    jobs.delete -- not cancel: the check is finished with them.
#
#    TWO SETS, and neither is "every job of the sandbox repository", which is
#    what this restore must never delete. 57a submits its own JSON, so it sets
#    fugaro_chk=57 itself and a label filter names exactly its jobs.
gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
  --filter='labels.fugaro_chk=57' --format='value(name)' | \
  xargs -r -n1 gcloud batch jobs delete --project="${P:?}" --location="${L:?}" --quiet
# 57b's jobs are created by the *installation's* launcher, whose label set is
# fixed by the job definition (fugaro=managed, fugaro_repo, fugaro_workflow and
# the per-run fugaro_run, §2.4) -- and Batch v1 has no way to add a label to a
# job afterwards: the REST methods are create, get, list, cancel and delete,
# with no patch (checked 2026-10-15). A 57b job therefore CANNOT carry
# fugaro_chk=57, and the build does not add a knob to let it. A repository-label
# filter is NOT the fallback: every other run of the sandbox repository carries
# those same labels -- a run the owner started, a scheduled run, an earlier
# check's -- and filtering on them would delete runs this check never created,
# which is the one thing the restore promises not to do. The check knows its own
# runs' names (it launched them, and 57b records them), so it deletes by name:
if [ -n "$RUNS" ]; then
  for N in ${RUNS}; do
    gcloud batch jobs delete "${N:?}" --project="${P:?}" --location="${L:?}" --quiet
  done
else
  echo "Restore: RUNS is empty; nothing of 57b's was created (correct after a 57a-only run, and a gap after 57b unless the names were recorded)"
fi

# 1b. jobs.delete is ASYNCHRONOUS: it returns while the job is still in
#     DELETION_IN_PROGRESS and its VMs are still running. Step 2 must not race
#     it -- a VM it deletes first is a VM whose job teardown then has nothing to
#     do, and a VM it misses is the leak this sweep exists to catch. Wait for
#     every deleted name and the probe's listing to drain (five minutes is
#     generous; a stuck job is a FACT).
for _ in $(seq 1 60); do
  LEFT=$(gcloud batch jobs list --project="${P:?}" --location="${L:?}" \
           --filter='labels.fugaro_chk=57' --format='value(name)' | wc -l)
  for N in ${RUNS}; do
    gcloud batch jobs describe "${N:?}" --project="${P:?}" --location="${L:?}" >/dev/null 2>&1 \
      && LEFT=$((LEFT + 1))
  done
  [ "${LEFT:?}" = 0 ] && break
  sleep 5
done
[ "${LEFT:?}" = 0 ] \
  || { echo "Check 57 stopped: ${LEFT} job(s) still present after 5 minutes (DELETION_IN_PROGRESS). Record them as a FACT and re-run this block; do not delete their VMs." >&2; exit 1; }

# 2. Any VM still running. Delete only instances carrying THIS check's label --
#    and only after step 1, so a job that is still live cannot lose its VM.
#    The ZONE COMES FROM EACH ROW: Batch places a job's VMs in any zone of the
#    region, so a hard-coded "${L}-b" leaves a leaked VM in ${L}-a or -c
#    undeleted -- each delete errors and the loop moves on -- which is exactly
#    the VM this sweep and the assertion below exist to catch.
gcloud compute instances list --project="${P:?}" --filter='labels.fugaro_chk=57' \
  --format='csv[no-heading](name,zone.basename())' | \
  while IFS=, read -r NAME ZONE; do
    gcloud compute instances delete "${NAME:?}" --project="${P:?}" --zone="${ZONE:?}" --quiet
  done

# 2b. The VMs 57b's runs left, if any. Same sweep, different handle, because a
#     57b job's own name never reaches its VM (§2.4): the handle is the per-run
#     fugaro_run label value this check read back off each job (RUN_LABELS, and
#     57b's FACT line). It is an exact match on a value this check recorded, so
#     it cannot reach another run's VM -- a filter on fugaro=managed or on the
#     repository label could, which is why neither is used (step 1 says the
#     same about jobs). A leaked VM here is 57b-4's finding, so record what this
#     deletes: the FACT lines are the evidence, this sweep is the cleanup.
for RL in ${RUN_LABELS}; do
  gcloud compute instances list --project="${P:?}" --filter="labels.fugaro_run=${RL:?}" \
    --format='csv[no-heading](name,zone.basename())' | \
    while IFS=, read -r NAME ZONE; do
      gcloud compute instances delete "${NAME:?}" --project="${P:?}" --zone="${ZONE:?}" --quiet
    done
done
[ -n "${RUN_LABELS}" ] \
  || echo "Restore: RUN_LABELS is empty; 57b's VMs were not swept (correct after a 57a-only run, and a gap after 57b unless the label values were recorded)"

# 3. The IAM this check added, BEFORE deleting the account. Deleting a service
#    account does NOT remove its bindings: the policy keeps the member as
#    `deleted:serviceAccount:...`, which is a grant nobody meant to leave and a
#    policy that no longer reads as clean. The repository binding goes first
#    (57a-0), removal of the account's own bindings second.
gcloud artifacts repositories remove-iam-policy-binding "${BASE_REPO:?}" \
  --project="${P:?}" --location="${L:?}" \
  --member="serviceAccount:${SA_EMAIL:?}" --role="roles/artifactregistry.reader" --quiet
for ROLE in roles/logging.logWriter roles/batch.agentReporter; do
  gcloud projects remove-iam-policy-binding "${P:?}" --member="serviceAccount:${SA_EMAIL:?}" \
    --role="${ROLE}" --condition=None --quiet >/dev/null
done
# 57a-9's conditioned grants (roles/batch.jobsEditor and the rest), if it added
# them: same removal, one per role, then read the policy back and confirm no
# member of this check's remains.

# 4. The account THIS CHECK created. Never the installation's job account, and
#    never the default Compute Engine SA.
gcloud iam service-accounts delete "${SA:?}@${P:?}.iam.gserviceaccount.com" --project="${P:?}" --quiet

# 5. The router and NAT, only if 57a-0b created them (named after $SA). The
#    project's own NAT is not touched: it does not carry this name.
gcloud compute routers nats delete "${SA:?}-nat" --router="${SA:?}-router" --project="${P:?}" --region="${L:?}" --quiet 2>/dev/null || true
gcloud compute routers delete "${SA:?}-router" --project="${P:?}" --region="${L:?}" --quiet 2>/dev/null || true

# 6. The preloaded boot image, if 57a-3b built one (named after $SA).
gcloud compute images list --project="${P:?}" --filter="name~^${SA}-preload" --format='value(name)' | \
  xargs -r -n1 gcloud compute images delete --project="${P:?}" --quiet

# 7. The streaming API, ONLY if 57a-0c enabled it -- the marker file is what
#    makes that conditional. A project that already had
#    containerfilesystem.googleapis.com keeps it: disabling an API this check did
#    not enable can break workloads the check never looked at.
if [ -f /tmp/chk57-api-enabled-by-check ]; then
  gcloud services disable containerfilesystem.googleapis.com --project="${P:?}" --force --quiet
  rm -f /tmp/chk57-api-enabled-by-check
else
  echo "Restore: containerfilesystem.googleapis.com was not enabled by this check; leaving it as it was"
fi
```

The NAT/router/subnet note, if the check had to create egress of its own: it
**borrows** the installation's NAT where one exists **on the probe's own network
in the probe's region, and only when its rules cover every subnet** (the design
puts NAT at the installation level, §1.7, and 57a-0b prints
which branch it took, creating its own pair rather than borrowing a NAT on
another network or editing a NAT it cannot read or that names its subnets), and
names
anything it creates `${SA}-*` — step 5 above — so this restore never reaches
resources the check did not create, and never by `fugaro=managed`, which is the
installation's label and reaches resources this check never touched. The log sink
arm, if 57a-10 applied it, is reverted with the Terraform apply that reverses it
(`fugaro init` from the sandbox's config), and nothing else. Nothing in `belong`
is touched at any step.

**The restore's assertion — run it and paste the output; every line must read
`0`.** These are counts, so a line that looks empty is a `gcloud` that failed or
a filter that matched nothing for the wrong reason, not a clean project. `RUNS`
and `RUN_LABELS` are the same two lists step 1 and step 2b used: set them again
here, or the `jobs (57b)` and `vms (57b)` lines count nothing and say so:

```bash
RUNS="${RUNS:-}"            # the names 57b recorded; empty only if 57b did not run
RUN_LABELS="${RUN_LABELS:-}"  # their fugaro_run label values, same rule
LEFT57B=0; for N in ${RUNS}; do
  gcloud batch jobs describe "${N:?}" --project="${P:?}" --location="${L:?}" >/dev/null 2>&1 && LEFT57B=$((LEFT57B + 1))
done
LEFT57B_VMS=0; for RL in ${RUN_LABELS}; do
  LEFT57B_VMS=$((LEFT57B_VMS + $(gcloud compute instances list --project="${P:?}" \
    --filter="labels.fugaro_run=${RL:?}" --format='value(name)' | wc -l)))
done
echo "jobs (57a): $(gcloud batch jobs list --project="${P:?}" --location="${L:?}" --filter='labels.fugaro_chk=57' --format='value(name)' | wc -l)"
echo "jobs (57b): ${LEFT57B}"
echo "vms (57a):  $(gcloud compute instances list --project="${P:?}" --filter='labels.fugaro_chk=57' --format='value(name)' | wc -l)"
echo "vms (57b):  ${LEFT57B_VMS}"
echo "image:      $(gcloud compute images list --project="${P:?}" --filter="name~^${SA}-preload" --format='value(name)' | wc -l)"
echo "account:    $(gcloud iam service-accounts list --project="${P:?}" --filter="email~^${SA}@" --format='value(email)' | wc -l)"
echo "iam:        $(gcloud projects get-iam-policy "${P:?}" --flatten='bindings[].members' \
                 --format='value(bindings.members)' | grep -c "${SA_EMAIL:?}" || true)"
echo "router:     $(gcloud compute routers list --project="${P:?}" --regions="${L:?}" --filter="name~^${SA}-" --format='value(name)' | wc -l)"
```

The `iam` line is the one the account line cannot answer: **deleting a service
account does not remove its bindings** (step 3 says why), so a `deleted:
serviceAccount:` member left in the policy reads as `0` on the `account` line and
non-zero here — which is exactly the leak this line exists to catch. It counts
the **project** policy; the repository-level `artifactregistry.reader` binding
step 3 removed is the same read against
`gcloud artifacts repositories get-iam-policy "${BASE_REPO:?}" --location="${L:?}"`
with the same `--flatten`, and it is worth pasting too, since a repository policy
is not part of the project's.

**Eight zeroes, one per line** — the probe's jobs and 57b's named runs (two
different questions, so two lines: a label filter for the first, the recorded
names for the second, step 1), their VMs (two more, for the same reason: the
check's own label for the probe's, the recorded `fugaro_run` values for 57b's,
step 2b), the preload image, the account, the probe account's remaining
bindings, and the
`${SA}-*` router — and the check left nothing behind. The
`containerfilesystem.googleapis.com` API is deliberately **not** a line here: it
is not a resource that can be counted, and whether it stays enabled depends on
whether the check enabled it (step 7 says so when it runs). A non-zero line names
the resource still to delete, and **a surviving VM is the check's own finding**
(§3.4): record it, do not quietly delete it and move on.

#### What this check settles, and what stays open

Four questions this section's steps exist to answer, so a reader of the FACT
lines knows which claim each one moves:

- **The state enum is nine values, and nothing else.** Batch v1's
  `JobStatus.State` has `STATE_UNSPECIFIED`, `QUEUED`, `SCHEDULED`, `RUNNING`,
  `SUCCEEDED`, `FAILED`, `DELETION_IN_PROGRESS`, `CANCELLATION_IN_PROGRESS`
  and `CANCELLED` — the same list in the REST reference
  (`cloud.google.com/batch/docs/reference/rest/v1/projects.locations.jobs`),
  the discovery document (`batch.googleapis.com/$discovery/rest?version=v1`,
  and v1alpha) and `google/cloud/batch/v1/job.proto`'s `State` enum
  (`googleapis/googleapis`), all read 2026-10-15. §2.3 names the full enum
  inline, so no implementer codes a mapping for a value the API cannot return;
  the not-terminal fallback stays for anything Google adds later.
- **`resource.labels.job_id` is UNVERIFIED.** Nothing public says whether it
  holds the job's name or its UID, so the shipped filter is the documented
  `labels.job_uid` form and 57a-10 settles name-vs-UID rather than assuming
  one.
- **Batch has no queue-deadline field.** The only Google-side bound is the
  documented 2-day `QUEUED` ceiling, so §1.4's and C8's queue deadline is a
  Fugaro-side reaper action (cancel a job that has sat in `QUEUED` past the
  deadline), not a knob.
- **Boot and pull are billed and not counted.** §5.2 states the window
  honestly rather than inventing a second estimate; until 57b-5 compares the
  estimate with the billing export, C17 keeps disk and NAT named as excluded
  instead of guessing them.

## 8. Task outline

### 8.1 The tasks

Not the full plan (that is written after the owner approves this design and
the pre-build probe, Check 57a, answers). Ordered so **each task merges
alone**, the **first one delivers value**, and — the ordering rule that follows
from both — **no task can be enabled before the machinery it needs exists**:
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
   cloud" answer keyed on the **platform's** signal (never a `FUGARO_*`
   variable, §2.5) **with local mode resolved from the invocation rather than
   from a missing marker**, and keep Cloud Run's and the local path's
   behaviour byte-for-byte unchanged — the e2e suite's local
   `exec --task-file` run and checks 20 and 21 are part of that requirement
   (§2.5). Fixing `execPrices`' `cloud-run`-only gate (§5.2's site 3) belongs
   here too, since it is the same question asked by a different caller. Merges alone; no new backend; every existing test green. Value: the
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
   This task also owns the call sites that still reach for Cloud Run's own
   codec rather than the seam's, so the enable task (Task 9) does not inherit
   them: `internal/cli/cloud.go`'s `checkExecution` calls
   `gcp.CheckRunExecution` directly instead of the selected backend's
   `Codec.ValidateRun` (C12), and `internal/cli/run.go` writes
   `Backend: backend.CloudRun` into two launch records — the one backfilled
   from result.json and the one written after a fresh launch. Both are correct
   only while Cloud Run is the only name that reaches them, and each carries a
   `TODO(compute-backend Task 8)` at the site.
9. **The enable task**: config validation starts **accepting**
   `backend: cloud-batch`, and `fugaro doctor` refuses the installation until
   the provisioning, IAM, logs, cost and reaper checks all pass (§4.4). This is
   the only task that lifts the refusal of Tasks 2 and 8, and it comes after
   3, 4, 5 and 6 — by construction, because `doctor` is what gates it.
10. **Check 57b, the docs, the skills and the release bullet** — the last task,
    as in every other group: the post-build acceptance run on the real backend
    (§7.2, 57b). **It also owns where Check 57 is written down**, which is the
    one thing Check 57 does not share with checks 48–56 today: it writes the
    **`## Check 57` section into `docs/gcp-live-checklist.md`** from §7.2 — both
    halves, with **Check 43's guard block verbatim**, the same block 57a's setup
    block is built from — keeps the row this design adds and **re-points it at
    that section** rather than at §7.2, and adds **`TestChecklistHasCheck57`**
    (`internal/cli/docs_project_centric_test.go`, the `docs_*_test.go`
    convention of `TestChecklistHasCheck33` and `TestChecklistHasCheck40`) so
    that 57's guard is asserted the way every other check's is and the
    project-centric plan's `TestChecklistChecksAreGuarded` backstop has a
    section to hold it to (§7.2). Until this task lands, the body lives in the
    design and the row says so; nothing else in the outline may run 57b's steps
    before it.
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
| C18 | When to build | After the pre-build probe, **Check 57a** (which needs no Fugaro change); the fake-conformance task (Task 2) and the cost and log tasks may land before it answers, but the enable task is last (§8.1) | Build before the measurement (risks building the wrong substrate); or build the backend before logs, cost and the reaper exist (a run whose output lands in `_Default`, uncharted and unbilled) |
| C19 | The preloaded image | **Only if C4's measurement says so**: a script in `fugaro init`'s Batch stage, one image per base version | Rely on image streaming alone (no image build, but depends on Artifact Registry + containerd) — **C4's own recommendation, and the preload is what 57a-3b builds if streaming loses** |
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
  switching the runtime to containerd with only **five** subfields honoured —
  `imageUri`, `commands`, `entrypoint`, `volumes` and `enableImageStreaming`
  itself (§1.2 and §2.2 quote the same five from the same page)
  (Batch REST reference; `cloud.google.com/batch/docs/use-image-streaming`).
- Batch job fields used here: `logsPolicy.destination`, `allocationPolicy.
  serviceAccount`, `maxRetryCount` (default 0), the complete `State` enum —
  `STATE_UNSPECIFIED`, `QUEUED`, `SCHEDULED`, `RUNNING`, `SUCCEEDED`,
  `FAILED`, `DELETION_IN_PROGRESS`, `CANCELLATION_IN_PROGRESS`, `CANCELLED`
  and nothing else, per the REST reference, the discovery document and
  `google/cloud/batch/v1/job.proto` (`JobStatus.State`), all checked
  2026-10-15 —
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
- Batch's boot disk: `Disk.image` takes one of the three short names
  `batch-debian`, `batch-cos`, `batch-hpc-rocky`, or a full image
  (`projects/{project}/global/images/family/{family}`,
  `projects/{project}/global/images/{version}`) — there is no
  `batch-cos-stable`, and the short names carry no version (Batch REST
  reference, `Disk`, checked 2026-10-15).
- Batch's no-external-IP and SSH postures:
  `NetworkInterface.noExternalIpAddress` **defaults to false**, so a job with
  no `network` block gets an external IP; `InstancePolicyOrTemplate`'s
  `blockProjectSshKeys` is what closes project-wide SSH keys (Batch REST
  reference, checked 2026-10-15).
- `TaskGroup.runAsNonRoot`'s **text**: unset or false, Batch "uses the root user
  to execute runnables"; true, it runs them as "a non-root user … generated by
  OS Login" (Batch REST reference, `TaskGroup`, checked 2026-10-15). That is the
  whole of the documented sentence, and it is what this document claims — the
  field is a **runnable execution user**, and the text says nothing about the
  container's uid.
- Batch injects `BATCH_TASK_INDEX` and `BATCH_TASK_COUNT` into every task, in
  addition to the job's own environment (`TaskGroup.taskEnvironments`, Batch
  REST reference, checked 2026-10-15), which is the platform signal C12's
  detection uses (§2.5).
- Batch v1's job methods are `create`, `get`, `list`, `cancel` and `delete` —
  there is **no `patch`/`update`**, so a job's labels and environment are fixed
  at creation (`batch.googleapis.com/$discovery/rest?version=v1`, checked
  2026-10-15).
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
(`internal/backend/gcp/names.go`); the base registry's id
(`internal/infra/tfvars.go`:23, `BaseRegistry = "fugaro-base"`); the job
environment's own names and who writes them (`internal/infra/spec.go`:85-94 for
the names, `platformEnv` at `:522` for the writer); the compute-price gate and
its `cloud-run`-only comparison
(`internal/cli/exec.go`:369-371, §5.2's third site); the base image's CLI-only
Docker and its 3812 MB size (`images/base/Dockerfile`;
`docs/design/base-image.md` §3 for the CLI-only contents and §13 for the
measurement, which is the design file, not `docs/base-image.md`). The live
checklist's own numbering is verified the same way: check 31 there is
`fugaro upgrade` with the real `claude` CLI (`docs/gcp-live-checklist.md`), not
the dropped gen-2 Docker probe the generic-tool plan numbered 31 — the reason
§7.1 reads the registry before claiming a number.

**Unverified, each with what settles it:**

- **What uid the container process actually gets**, and whether the image's
  `USER` survives `TaskGroup.runAsNonRoot`: the REST text documents the
  runnables' execution user and never the container's uid, so none of "the
  container runs as root", "the image's `USER` is ignored" or "Batch overrides
  `USER`" is documented. Step 57a-6 runs `id` in each variant and reads the uid
  back — and a variant's `id` output is what decides the socket path (§2.2).
- Whether `Container.options` (a **string**, not an array) reaches the runtime
  as `--group-add <gid>`, and whether `--privileged` is accepted there at all —
  and, if it is, whether it changes the outcome at all for a uid-1000 process,
  which §2.2 says it does not — Check 57a step 6.
- Whether a `docker` group exists on the COS boot image, and with which gid —
  Check 57a step 6 reads the gid off the mounted socket
  (`stat -c %g /var/run/docker.sock`), because the name form is looked up inside
  the container and cannot be assumed.
- Whether the base image's container user can open the COS docker socket, and
  which permission path works — Check 57a step 6.
- VM boot and Batch scheduling times, with and without a preloaded image —
  Check 57a steps 2 and 3.
- Whether Batch adds any billable line beyond the VMs — Check 57a step 5.
- Whether Batch truly tears down every VM on `DELETION_IN_PROGRESS` and on a
  crashed runnable — Check 57a step 8.
- Whether image streaming works for a base image served through an Artifact
  Registry **remote** repository — Check 57a step 3, **if `IMG` is pointed at
  the proxy**: the committed check pulls the direct repository (pairs with
  Check 43).
- Whether Batch accepts a **custom boot image** at all ("might not be
  supported" is Google's own hedge) — Check 57a step 3b, which builds it.
- Whether a Batch job's queue deadline can be expressed as a Google-side field
  — it cannot, as far as the pages read show, so the design's queue deadline
  is a Fugaro-side reaper action; and how long a job really sits in `QUEUED` —
  Check 57a step 11, whose machine type is discovered from the region rather
  than named.
- Whether an IAM Condition narrowing `batch.jobsEditor` to Fugaro-shaped job
  names is accepted — Check 57a step 9.
- Whether a job's own environment can **override** the injected
  `BATCH_TASK_INDEX`/`BATCH_TASK_COUNT` (a signal the job can set is only as
  good as the launch path) — Check 57a step 12, once per place Batch documents
  environment (TaskSpec and `taskEnvironments`); and, separately, whether a job
  can read its own VM's identity from the metadata server at
  `169.254.169.254` from a VM with no external IP, which is **not** a Private
  Google Access result (the address is link-local) but the mechanism §3.3's
  token comes from.
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
