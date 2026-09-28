# deploy/bootstrap: throwaway M4 GCP bootstrap (M5's Terraform replaces it)

`gcp-m4.sh` creates, one confirmed step at a time, the GCP resources Fugaro's
M4 live tests need. The runbook is [docs/gcp-bootstrap.md](../../docs/gcp-bootstrap.md).

- **`config` runs first.** It writes the local config that binds this
  machine to `PROJECT`, `REGION` and `BUCKET`. Every other step refuses
  unless that config exists and names the same values, and `PROJECT` must be
  a well-formed project ID.
- **Dry run by default.** Without `--apply`, a step prints each command with
  a leading `+` and runs only the read-only `fugaro gcp job-spec`.
- **Every step confirms first.** Each prints a `⚠ CONFIRM (project …)`
  banner, and with `--apply` asks you to type the project ID, or takes
  `--yes`.
- **The project is always explicit.** Every `gcloud` call passes
  `--project`, and gcloud prompts are disabled.
- **The bucket is pinned.** Bucket names are global, so before any bucket
  write, IAM change or deletion, a read-only describe checks that the bucket
  belongs to `PROJECT`.
- **Steps can be rerun.**
  - Creates skip a resource that already exists.
  - A bucket is skipped only when it is in the same project and region.
  - Deletes skip what is already gone.
- **`teardown` removes one repository's workflow.**
  - It deletes the job and its service account.
  - It removes that account's bindings.
  - It keeps the repository's secrets, which the repository's other
    workflows share. `--secrets` deletes them too, and refuses while another
    job of the repository exists.
  - It checks the `fugaro_repo` and `fugaro_workflow` labels before deleting
    anything.
- **`teardown-all --all` removes the shared resources.**
  - It deletes the bucket, the registry, `fugaro-build` and its project
    binding.
  - It refuses while any Fugaro job remains in the region.
- **The names come from job-spec.** Job names, service accounts, secrets,
  env, labels and the IAM condition all come from `fugaro gcp job-spec`
  (hidden, development-only), so the script never re-implements the naming
  contract.

`sandbox/` is the fixture committed to the live-test sandbox repository: a
tiny npm project with no dependencies and two passing tests.
