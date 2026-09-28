# deploy/bootstrap: throwaway M4 GCP bootstrap (M5's Terraform replaces it)

`gcp-m4.sh` creates, one confirmed step at a time, the GCP resources Fugaro's
M4 live tests need. The runbook is [docs/gcp-bootstrap.md](../../docs/gcp-bootstrap.md).

- Every step is a dry run unless you pass `--apply`. A dry run prints each
  command with a leading `+` and runs only the read-only `fugaro gcp job-spec`.
- Every step that enables an API, creates or deletes a resource, or grants
  IAM prints a `⚠ CONFIRM (project …)` banner first. With `--apply` it then
  asks you to type the project ID, or takes `--yes`.
- The script refuses a `PROJECT` other than the local config's `project:`,
  and passes `--project` on every `gcloud` call.
- The job names, service accounts, secrets, env and IAM condition come from
  `fugaro gcp job-spec` (hidden, development-only), so the script never
  re-implements the naming contract.
- `teardown` removes one repository's workflow and keeps the shared
  resources. `teardown-all --all` removes the bucket, the registry and
  `fugaro-build`, and refuses while any `fugaro-*` job remains.

`sandbox/` is the fixture committed to the live-test sandbox repository: a
tiny npm project with no dependencies and two passing tests.
