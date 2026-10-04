# Backends

Fugaro runs a workflow as a container execution. `internal/backend.Backend` is the seam between the CLI and the compute platform that runs it; `internal/backend/gcp` implements it on Cloud Run, today's only 1.0 implementation. This is the small, deliberately narrow place a second cloud (AWS, Azure) attaches (see the README's [Other clouds](../README.md#other-clouds-help-wanted)).

## What the seam covers, and what it does not

`Backend` has five lifecycle methods (`Launch`, `Execution`, `List`, `Logs`, `Cancel`) and two timeout methods (`LongestTaskTimeout`, `TaskTimeout`): starting, inspecting, listing, reading the logs of, and cancelling an execution, plus the task-timeout bookkeeping the sweeper and the run horizon need. Storage is not part of the seam: every backend uses `gocloud.dev/blob`.

Not behind it, and out of scope for a contributor's first pull request: Secret Manager (`gcp.Secrets`, used directly by `cli/secrets.go`), image builds (`backend/gcp/build.go`, Cloud Build), the registry, and provisioning (`fugaro init`'s stages, Terraform for GCP). `fugaro init`'s stage interface (`Check`, `Plan`, `Apply`, `Left`) is the per-backend provisioning unit: a second backend brings its own stages for what Terraform does today for GCP. **Firebase stays GCP-bound regardless of compute backend**: it is the control plane (budget, dashboard, stats) the brief assumes, so an AWS or Azure compute backend still needs a Firebase project.

Three optional sub-interfaces are named here as the likely next step, not introduced now: `Secrets` (Set, List), `ImageBuilder` (Build, Status), `CostReporter` (compute cost of an execution). Add one only where a second backend actually needs it.

## Opening a backend by name

A project config's `backend:` key names the backend (`cloud-run` is the default and, today, the only valid value). `backend.Open(ctx, name, openers)` picks the right constructor from a map the caller builds after resolving its own backend-specific options (GCP project, region, endpoints, ...); the `backend` package never imports a backend implementation, which would cycle back to it. `internal/cli/cloud.go`'s `openCloudFor` is the one production call site.

## The conformance suite

`internal/backend/backendtest.Run(t, factory)` exercises the five lifecycle methods and the two timeout methods against any `Backend`. `factory` builds a fresh backend per subtest, already able to launch a job under `backendtest.Slug` / `backendtest.Workflow` (as `fugaro init` would have created it). A contributor's pull request for a new backend is "passes `backendtest.Run`"; `internal/backend/gcp/conformance_test.go` runs it against the GCP backend on the existing Cloud Run and Logging fakes (`internal/gcpfake`).

No live service is touched: CI never runs against a real cloud.
