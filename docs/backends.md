# Backends

Fugaro runs a workflow as a container execution. `internal/backend.Backend` is the seam between the CLI and the compute platform that runs it; `internal/backend/gcp` implements it on Cloud Run, today's only 1.0 implementation. This is the small, deliberately narrow place a second cloud (AWS, Azure) attaches (see the README's [Other clouds](../README.md#other-clouds-help-wanted)).

## What the seam covers, and what it does not

`Backend` has five lifecycle methods (`Launch`, `Execution`, `List`, `Logs`, `Cancel`) and two timeout methods (`LongestTaskTimeout`, `TaskTimeout`): starting, inspecting, listing, reading the logs of, and cancelling an execution, plus the task-timeout bookkeeping the sweeper and the run horizon need. Storage is not part of the seam: every backend uses `gocloud.dev/blob`.

**The reaper is not part of the seam, and it is the one piece with power of its own.** On Cloud Batch, a run's VM is normally torn down by Batch itself seconds after the job ends (the owner measured 3 to 26 s), so the reaper (`internal/backend/batch/reap.go`, design `design/compute-backend.md` §3.4) is a safety net for the VM Batch did not clean up — and it deletes things, which is why it is job-first, why it deletes only what carries both this installation's `fugaro=managed` marker and a `fugaro_run` label, why it refuses (and reports) everything it cannot attribute — a VM is deleted only when the run behind it is one the same pass positively classified as finished or found no job for, and a job the pass refused to explain keeps its VMs — and why it runs as a **dedicated project-wide service account** rather than the launcher's or a job's. That account needs exactly `batch.jobs.list`, `batch.jobs.get`, `batch.jobs.delete`, `compute.instances.list` and `compute.instances.delete` — nothing else, and **not `batch.jobs.cancel`, which Google does not define**: the note below this one used to name it as a sixth, and a role naming it is a role GCP refuses to create. A job stuck in `QUEUED` past the queue deadline is **deleted** instead (it never ran a task, so there is no execution record to keep), and as a **custom role** (`fugaroReaper`, the naming the installation's other custom roles use), not as `roles/batch.jobsEditor` plus `roles/compute.instanceAdmin.v1`: those two built-in roles also carry `batch.jobs.create` and `compute.instances.create`/`setMetadata`, i.e. the power to run containers in the project and to run code on a VM, which a janitor must not have. IAM cannot scope a delete by label, so nothing in the grant confines the reaper to this installation's resources: only `reap.go` does, by refusing everything it cannot attribute. `fugaro init`'s Batch stage (compute-backend Task 6) is what creates the role and grants it.

Not behind it, and out of scope for a contributor's first pull request: Secret Manager (`gcp.Secrets`, used directly by `cli/secrets.go`), image builds (`backend/gcp/build.go`, Cloud Build), the registry, log isolation (the per-project log view and bucket `fugaro init` provisions, `internal/infra`, `docs/gcp-setup.md`), and provisioning (`fugaro init`'s stages, Terraform for GCP). `fugaro init`'s stage interface (`Check`, `Plan`, `Apply`, `Left`) is the per-backend provisioning unit: a second backend brings its own stages for what Terraform does today for GCP. **Firebase stays GCP-bound regardless of compute backend**: it is the control plane (budget, dashboard, stats) the brief assumes, so an AWS or Azure compute backend still needs a Firebase project.

Three optional sub-interfaces are named here as the likely next step, not introduced now: `Secrets` (Set, List), `ImageBuilder` (Build, Status), `CostReporter` (compute cost of an execution). Add one only where a second backend actually needs it.

## Opening a backend by name

A project config's `backend:` key names the backend (`cloud-run` is the default and, today, the only valid value). `backend.Open(ctx, name, openers)` picks the right constructor from a map the caller builds after resolving its own backend-specific options (GCP project, region, endpoints, ...); the `backend` package never imports a backend implementation, which would cycle back to it. `internal/cli/cloud.go`'s `openCloudFor` is the one production call site.

## The conformance suite

`internal/backend/backendtest.Run(t, factory)` exercises the five lifecycle methods and the two timeout methods against any `Backend`. `factory` builds a fresh backend per subtest, already able to launch a job under `backendtest.Slug` / `backendtest.Workflow` (as `fugaro init` would have created it). A contributor's pull request for a new backend is "passes `backendtest.Run`"; `internal/backend/gcp/conformance_test.go` runs it against the GCP backend on the existing Cloud Run and Logging fakes (`internal/gcpfake`).

No live service is touched: CI never runs against a real cloud.

## The control plane seam

Compute is not the only thing a backend needs. The `Backend` interface above is the compute seam; there is a second, unabstracted dependency on Firebase as the **control plane**: the budget's atomic counters, the dashboard and the run history. A replacement control plane would have to provide what Firebase provides today:

- **Atomic multi-path conditional updates (leases):** the budget's reservations are read-modify-write across several paths at once, refused if any of them changed since the read (`internal/budget`'s leases).
- **Server-evaluated rules that tie counters together:** the Realtime Database's rules enforce the budget invariants (a session's spend never exceeds its reservation, no reservation while a kill switch is on) without trusting the client that writes them (`internal/rtdb`).
- **Event streams:** `fugaro watch`'s live updates and the kill switches react to a value changing, not to polling (`internal/rtdb`, `internal/watch`).
- **A document store for history:** run records, spend history and reporting live in a queryable store across runs (`internal/firestore`).

**Firebase stays GCP-bound regardless of compute backend** (see above): a second compute backend (AWS, Azure) still talks to the same Firebase project for budget, dashboard and history. The seam is not abstracted, and will not be before a second control plane is actually needed: `internal/budget`, `internal/rtdb`, `internal/firestore` and `internal/watch` depend on Firebase's REST APIs directly. Neither `internal/rtdb` nor `internal/firestore` uses a Firebase SDK — both are small, hand-rolled REST clients (the Realtime Database Admin SDK cannot authenticate as a single run's own ID token, and the Firestore SDK is more than the handful of calls Fugaro needs), but the dependency on Firebase's own HTTP APIs, and on their specific behaviour (ETags, if-match, multi-path PATCH, Server-Sent-Event streams), is just as direct. This section is documentation only; no interface exists here yet.

## Your own image

A project that needs more than `image.apt`, `image.tools` and `image.setup` can give a repository Dockerfile built `FROM ${FUGARO_BASE}`, or point the installation at a base image of its own. [base-image.md](base-image.md)'s "Bring your own" section covers both, and the contract a custom base must keep.

## Live checks

The offline suites above prove a backend's logic, not the real service. The GCP backend's provisioning stages (project creation, the images stage, the secrets and plugin-wiring stages, `doctor`) are exercised against real Google, ghcr.io and Artifact Registry by [Check 27](gcp-live-checklist.md) of the live checklist: user-run on a throwaway project, with exact steps, expected results and what to paste back. **It has not been run**; until it is, those stages' claims about Google and registry behaviour are from documentation, as the M11 design's §14 says. A second backend brings its own checklist entry for the stages it adds.
