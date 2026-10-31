# Who reads Secret Manager today (reconciliation §3, audited against the code)

**Status: the S0 audit, as a tested doc; it changes no behaviour.** One row per reader of a Secret
Manager value — an account class or a Go file — with the decision that keeps it reading and the slice
that ends it. The IDs are the key B0.2's guard uses: its exceptions table lists exactly these IDs, so
a hit with no row, and a row with no hit, both fail the build.

How to read a row:

- **Kind** is `runtime` (an identity a Fugaro component runs as) or `operator` (a human identity, or
  the CLI acting with the owner's own credentials). Every `operator` row exits `never`: nothing in
  this work takes the owner's own Secret Manager access away.
- **Exit slice** is the slice of the plan's Slices table that ends the read, or `never` when no slice
  of this plan does — the service's own grant, operator code, and the coordinator's App key read,
  which the coordinator plan owns. Where one row covers secret classes that leave in different
  slices, the row carries the earliest of them and the **what** cell names each class's slice.
- **Decision** is the decision that keeps the row a listed exception: D145 and D491 for the staged
  classes, D506 for the Bitbucket token living in the bundle, D499 for the rows the guard itself
  keeps listed.
- The rows are reconciliation §3, plus the Batch backend, the build request built in Go, the managed
  projects' `fmgd-` secrets, and every non-test Go file under `internal/` and `cmd/` that imports the
  Secret Manager API or names its host — a file the guard would otherwise find with no row to map it
  to. A row's location — the module, the resource or the file it keys on — is in its **how** cell.

| ID | Kind | Reader | What it reads | How | Decision | Exit slice |
| --- | --- | --- | --- | --- | --- | --- |
| E1 | runtime | Per-repository job accounts, one per repository, workflow and Claude account variant | The git credential (`bitbucket-token` or `github-app-key`), the model credentials (`claude-oauth-token*`, `anthropic-api-key`, provider keys) unless the installation opted into the service, and the workflow's own `secrets:`; the git credential leaves at S1, the model credentials at S2, the workflow's `secrets:` at S3 | Cloud Run `secret_key_ref` env mounts, resolved by the platform as the job account at execution start; the grant is `modules/workflow/sa.tf` `google_secret_manager_secret_iam_member.job`, the mounts are `modules/workflow/job.tf` and `internal/infra/spec.go` `SecretMounts` | D145, D491, D506 | S1 |
| E2 | runtime | Shared project job accounts, one per profile, provider and Claude account | The GitHub App key, the profile's provider keys and its Claude account token; all three leave when shared project jobs move to the service | The same `secret_key_ref` mounts, `modules/project/job.tf`, with the grant in `modules/project/sa.tf` `google_secret_manager_secret_iam_member.job` | D145, D505 | S5 |
| E3 | runtime | The repository's build account, and the daily image-check job, which runs as it | The git credential and the workflows' build secrets, during Cloud Build | Cloud Build `availableSecrets` and BuildKit `--secret id=git-credentials` (`modules/repo/build.tf`, `images/derived/cloudbuild.yaml`); the image-check job's own `secret_key_ref` (`modules/repo/check.tf`); `internal/cli/gitcred.go` (`fugaro image git-credential`) and `internal/cli/imagecheck.go` are how the build reads them | D491 | S4 |
| E4 | runtime | The coordinator job's account | The GitHub App key, from which the coordinator mints its own read-only installation token; no slice of this plan ends it, because the read belongs to the coordinator plan and the App key's move off jobs belongs to the credentials-service plan | `secret_key_ref` (`modules/installation/coordinator.tf` `google_secret_manager_secret_iam_member.coordinator_app_key`) | D499 | never |
| E5 | runtime | Batch VM accounts, the compute backend, when the installation selected it | The secrets the installation's `batch.secrets` lists, the workflow's own `secrets:` among them; a Batch VM is not a job account the credentials service admits, so these stay VM-resolved | Batch `secretVariables`, resolved as the VM's account (`internal/backend/batch` `Options.Secrets`, `checkSecrets`) | D491, D499 | S3 |
| E6 | runtime | The build request built in Go | The git credential and each workflow build secret, added to `availableSecrets` | Cloud Build, as the build account (`internal/backend/gcp/build.go`) | D491 | S4 |
| E7 | runtime | The credentials service's account | The bundle: every credential the service holds, in one secret | The Secret Manager API (`internal/credsvc/bundle.go`, whose client `internal/cli/credentials_serve.go` builds), through the one `secretmanager.secretAccessor` grant of `modules/installation/credentials.tf` | Q6, D147, D506 | never |
| E8 | operator | The owner, on their machine | The bundle, read-modify-write, and the App key for `doctor --check-github-app`; and, with `doctor --check-bitbucket-token`, the bundle's Bitbucket token, which the flag's refusal keeps out of a coding agent's session | Their own credentials, through the Secret Manager API (`internal/cli/creds.go`, `internal/cli/doctor.go`, `internal/cli/doctor_brokering.go`) | Q6, D147 | never |
| E9 | operator | Operators | Names and metadata only: they add a version and list them; never a value | The `secretVersionAdder` and `viewer` grants (`modules/repo/secrets.tf`, `modules/installation/secrets.tf`) | D145, D147 | never |
| E10 | operator | Launchers | Names, labels and version counts; never a value | The `fugaroLauncher` custom role's `secretmanager.secrets.list` and `secretmanager.versions.list` (`modules/installation/iam.tf`) | D499 | never |
| E11 | operator | Managed projects' accounts (`fmgd-`) | The `fmgd-` secrets a managed plan creates, and the readers that plan grants on them | The managed deployer's own `secretmanager.secrets.setIamPolicy`, conditioned on its `fmgd-` prefix (`modules/installation/deployer.tf`) | D499, D503 | never |
| E12 | operator | Project Owners | Anything: `roles/owner` carries `secretmanager.versions.access` and bypasses secret-level IAM | Project IAM, outside every grant Fugaro writes (`SECURITY.md`, "The credentials service") | D148 | never |
| E13 | operator | `internal/backend/gcp/secrets.go`, the CLI's Secret Manager client | Containers, versions, labels and IAM policies, for provisioning and doctor; one value, the App key `doctor --check-github-app` reads | The Secret Manager API, as the operator's own identity | D147 | never |
| E14 | operator | `internal/infra/discover.go` | Secret names, labels and IAM policies, to adopt live state into the plan; never a value | The Secret Manager API | D147 | never |
| E15 | operator | `internal/infra/grants.go` | A secret's IAM policy, to find the member that holds `secretAccessor` on it | The Secret Manager API | D147 | never |
| E16 | operator | `internal/infra/readiness.go` | A secret's ID, and whether it has an enabled version; never a value | The Secret Manager API | D147 | never |
| E17 | operator | `internal/infra/retire.go` | Secret names and labels, to retire a container the configuration no longer computes | The Secret Manager API | D147 | never |
| E18 | operator | `internal/cli/initsecrets.go` | Nothing: the API host appears only in the refusal an operator reads when the API is disabled | No call: the string `secretmanager.googleapis.com` in that refusal's text | D499 | never |
| E19 | operator | `internal/infra/apis.go` | Nothing: the API host is the service `fugaro setup` enables | No call: the host as the service name to enable | D499 | never |
| E20 | operator | `internal/infra/tf/cover.go` | Nothing: the API host appears in the resource-type and role lists the managed deployer's coverage is pinned against | No call: the host as a resource-type prefix in those lists | D499 | never |
| E21 | operator | `internal/managed/stateaccounts.go` | Nothing: the API host appears in the mapping of Terraform resource types to the managed state accounts | No call: the host as a resource-type prefix in that mapping | D499 | never |

An **Editor is deliberately not a row**, though this audit named it as one until 2026-10-29:
`roles/editor` carries `secretmanager.secrets.getIamPolicy` and delete but not
`secretmanager.versions.access`, so an Editor reads a container **directly** only through a
`secretAccessor` grant of its own — a grant of the kind every row above holds, and one the B0.3
sweep reports per container. Naming Editors as readers would be a claim `roles/editor` does not
support, and it would hide exactly those grants. An Editor also reads one **indirectly**: it carries
`iam.serviceAccounts.actAs` and `run.services.update`, so it can deploy code that runs as an account
holding a grant — a deploy rather than a read of its own, and the reason the
`credentials-service-deployers` line names it. The owner ruled the wording on 2026-10-29;
[reconciliation.md](reconciliation.md) F3 and `SECURITY.md` carry the same correction.
