# Every grant is a member resource, never a binding or policy, so nothing
# here takes away access someone else granted.

resource "google_project_iam_custom_role" "launcher" {
  project = var.project
  role_id = var.names.role_ids.launcher
  title   = "Fugaro launcher"
  permissions = [
    "run.jobs.get",
    "run.jobs.list",
    "run.executions.get",
    "run.executions.list",
    "run.executions.cancel",
    "run.operations.get",
    # fugaro secrets ls: the project's secret list and each secret's
    # versions. Metadata only: reading a value needs secretAccessor, which
    # only the jobs and build accounts hold, per secret.
    "secretmanager.secrets.list",
    "secretmanager.versions.list",
  ]

  depends_on = [google_project_service.this]
}

# Granted on each workflow job, by the repository module.
resource "google_project_iam_custom_role" "job_runner" {
  project     = var.project
  role_id     = var.names.role_ids.job_runner
  title       = "Fugaro job runner"
  permissions = ["run.jobs.run", "run.jobs.runWithOverrides"]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_custom_role" "build_submitter" {
  project     = var.project
  role_id     = var.names.role_ids.build_submitter
  title       = "Fugaro build submitter"
  permissions = ["cloudbuild.builds.create", "cloudbuild.builds.get"]

  depends_on = [google_project_service.this]
}

# Granted to each repository's build account on that repository's own
# registry only, by the repository module. Moving an existing :latest
# (gcloud artifacts docker tags add) deletes the old tag first, and writer
# lacks tags.delete. No version or package delete: a build can remove a
# tag, never an image.
resource "google_project_iam_custom_role" "tag_mover" {
  project     = var.project
  role_id     = var.names.role_ids.tag_mover
  title       = "Fugaro tag mover"
  permissions = ["artifactregistry.tags.delete"]

  depends_on = [google_project_service.this]
}

# The role is a literal path and not the resource's name attribute: this grant
# is planned by the same apply that creates the deployer's eight roles, and a
# project grant whose role is computed cannot be told from a deployer one at
# plan time (Task 1.5 step 5; internal/infra/tf refuses a computed project role
# while a deployer role is being created). var.project is known at plan time,
# and the role ID is the Go constant the tfvars file always carries
# (infra.RoleLauncher).
resource "google_project_iam_member" "launcher" {
  for_each = toset(var.launchers)

  project = var.project
  role    = "projects/${var.project}/roles/fugaroLauncher"
  member  = each.value

  depends_on = [google_project_iam_custom_role.launcher]
}

# Operators submit image builds while onboarding. A literal role, for the
# reason iam.tf's launcher grant above gives.
resource "google_project_iam_member" "operator_build_submitter" {
  for_each = toset(var.operators)

  project = var.project
  role    = "projects/${var.project}/roles/fugaroBuildSubmitter"
  member  = each.value

  depends_on = [google_project_iam_custom_role.build_submitter]
}

# The whole-project inventory of a managed project reads the project through
# the Cloud Asset API, and the operator running a managed apply is the identity
# that makes that read (design D59: a read-only query at apply time).
#
# The role is the installation's own custom role and not the predefined
# cloudasset viewer role (the owner's ruling of 2026-10-17, Task 4.1a): it
# holds the one permission the read's SearchAllResources call needs and nothing
# else, so holding it adds no write and no other Cloud Asset permission --
# viewer also carries cloudasset.assets.listResource, which this read never
# calls. internal/infra/tf pins the list (tf.RolePermissions): a plan that
# creates or updates the role with any other permission is refused, and
# tf.FugaroRoles names it, so a grant of it is covered and a grant of any other
# Cloud Asset role is not.
#
# The managed apply's own credential is the impersonated deployer account
# (D75, Task 1.5), and that account does NOT hold this role: it is created
# with its own deployer roles and nothing else (deployer.tf), and the managed
# gate grants this role to people only (tf grantRules), so a plan cannot grant
# it to the deployer either. The read is therefore made on the operator's own
# credentials -- the identity this grant is for -- and a read made under the
# deployer is the role-missing failure, which falls the run back to its
# confirmations rather than reading a project as clean.
resource "google_project_iam_custom_role" "cloud_asset_reader" {
  project     = var.project
  role_id     = "fugaroCloudAssetReader"
  title       = "Fugaro Cloud Asset reader"
  permissions = ["cloudasset.assets.searchAllResources"]

  depends_on = [google_project_service.this]
}

# Granted at the project to the operators, who run the managed apply that makes
# the read. The role is a literal path and not the resource's name attribute,
# for the reason the launcher grant above gives: this grant is planned by the
# same apply that creates the deployer's eight roles, and a project grant whose
# role is computed cannot be told from a deployer one at plan time.
#
# The role ID is a literal in both places, like the history role's, and not a
# names.role_ids entry: nothing outside this module names it, and the pin in
# internal/infra/tf is by the same literal ID (tf.CloudAssetRole), so the
# tfvars plumbing the four older roles carry would be a name with no reader.
resource "google_project_iam_member" "operator_cloud_asset_reader" {
  for_each = toset(var.operators)

  project = var.project
  role    = "projects/${var.project}/roles/fugaroCloudAssetReader"
  member  = each.value

  depends_on = [google_project_iam_custom_role.cloud_asset_reader]
}

# The runs bucket (design bucket-iam.md §3). Operators read and write all of
# it: fugaro/ (the project layer, the shared config, recipes, the marker),
# builds/, cache/, locks/ and runs/.
resource "google_storage_bucket_iam_member" "runs" {
  for_each = toset(var.operators)

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectAdmin"
  member = each.value
}

locals {
  # A launcher who is also an operator needs nothing more (H4).
  launchers_only = setsubtract(toset(var.launchers), toset(var.operators))
}

# Launchers read the whole bucket. Listing runs/ needs storage.objects.list,
# which a condition on an object's name can't grant, so this one has none.
resource "google_storage_bucket_iam_member" "runs_reader" {
  for_each = local.launchers_only

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = each.value
}

# Launchers write runs/ and tokens/ only. objectUser, not objectCreator: the
# launch claim's takeover overwrites and its release deletes, and the sealed
# budget token in tokens/ is replaced on a retry and deleted after a launch
# that did not start (docs/design/budget-token-channel.md section 7.2, D16:
# the deliberate widening that came with moving the token out of runs/, still
# one condition and not a role on the bucket). The condition is the
# input byte for byte, the same for every launcher, so IAM keeps them in one
# binding; no description, like the job accounts' conditions.
resource "google_storage_bucket_iam_member" "runs_launcher" {
  for_each = local.launchers_only

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = each.value

  condition {
    title      = var.launcher_bucket_condition.title
    expression = var.launcher_bucket_condition.expression
  }

  lifecycle {
    precondition {
      condition     = var.launcher_bucket_condition.title == "fugaro-launchers-runs-tokens" && var.launcher_bucket_condition.expression == "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/\")"
      error_message = "launcher_bucket_condition must be runs/ and tokens/ of the runs bucket, titled fugaro-launchers-runs-tokens (gcp.LauncherBucketCondition)."
    }
  }
}

# The state bucket isn't managed here (fugaro init creates it before any
# Terraform runs), but operators added later need to read and lock the state.
resource "google_storage_bucket_iam_member" "state" {
  for_each = toset(var.operators)

  bucket = var.state_bucket
  role   = "roles/storage.objectAdmin"
  member = each.value
}

# Only operators write the base images. Build accounts get reader, granted
# by the repository module.
resource "google_artifact_registry_repository_iam_member" "base_writer" {
  for_each = toset(var.operators)

  project    = var.project
  location   = google_artifact_registry_repository.base.location
  repository = google_artifact_registry_repository.base.name
  role       = "roles/artifactregistry.writer"
  member     = each.value
}

# The remote repository is read, never written: it proxies the public
# ghcr.io packages this installation's jobs and builds run (design §3.2,
# §6). The operators' own credentials are what fugaro init uses to read
# the verified digest back through it before it pins anything.
resource "google_artifact_registry_repository_iam_member" "ghcr_reader" {
  for_each = toset(var.operators)

  project    = var.project
  location   = google_artifact_registry_repository.ghcr.location
  repository = google_artifact_registry_repository.ghcr.name
  role       = "roles/artifactregistry.reader"
  member     = each.value
}
