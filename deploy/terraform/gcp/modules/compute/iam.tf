# The backend's IAM (design §3.2, §3.7). Three parties, kept apart:
#
#   - the RUN accounts (run_accounts): what a job's VM runs as. They hold the
#     two roles Batch's own documentation requires of a job's service account
#     (agentReporter, without which the VM cannot report task state and the
#     job never reaches a terminal state, and logWriter) plus a reader grant
#     on the ONE repository the installation's pin names. They hold nothing
#     else here: that narrowness is the control §3.2 says matters, not the VM
#     boundary.
#   - the LAUNCHERS: Batch Job Editor, narrowed by 57a-9's Condition to
#     Fugaro-shaped job names, and actAs on the run accounts only. This is
#     the coarsest grant in the design (§3.7) and the Condition is what
#     narrows it — measured accepted by GCP on 2026-10-15.
#   - the REAPER: six permissions in a custom role of its own, and nothing
#     else. NOT roles/batch.jobsEditor and NOT roles/compute.instanceAdmin.v1:
#     both are far wider than the six, and a reaper account that can create
#     jobs and VMs is a second launcher, not a janitor (§3.7).
#
# No account here is the default Compute Engine service account, and none is
# the installation's history account.

locals {
  reaper_email  = "${var.names.reaper_account_id}@${var.project}.iam.gserviceaccount.com"
  preload_email = "${var.names.preload_account_id}@${var.project}.iam.gserviceaccount.com"
  # The service agents that must be able to act as an account this module
  # creates: Cloud Build runs the preload build as the preload account, and
  # Cloud Scheduler calls the Cloud Build API as it. Both are Google's own
  # per-project agents, named from the project NUMBER, and both grants are
  # actAs on one named account rather than a role on the project.
  cloud_build_agent     = "serviceAccount:service-${var.project_number}@cloudbuild.gserviceaccount.com"
  cloud_scheduler_agent = "serviceAccount:service-${var.project_number}@gcp-sa-cloudscheduler.iam.gserviceaccount.com"
  job_name_prefix       = "projects/${var.project}/locations/${var.region}/jobs/${var.names.job_prefix}"
}

# --- the run accounts -------------------------------------------------------

# "Batch Agent Reporter (roles/batch.agentReporter) on the project" and "Logs
# Writer (roles/logging.logWriter) on the project", both from
# cloud.google.com/batch/docs/get-started (checked 2026-10-15). A job whose
# account lacks agentReporter never reports task state, so every run would sit
# until the queue deadline fired.
resource "google_project_iam_member" "run_agent_reporter" {
  for_each = var.run_accounts

  project = var.project
  role    = "roles/batch.agentReporter"
  member  = each.value
}

resource "google_project_iam_member" "run_log_writer" {
  for_each = var.run_accounts

  project = var.project
  role    = "roles/logging.logWriter"
  member  = each.value
}

# The reader grant goes on the repository the PIN names, never on a constant:
# on 2026-10-15 the installation's pin named the remote fugaro-ghcr, and a
# grant aimed at fugaro-base would have left every agent unable to pull
# (§7.2's setup findings).
resource "google_artifact_registry_repository_iam_member" "run_registry_reader" {
  for_each = var.run_accounts

  project    = var.project
  location   = var.region
  repository = var.registry_repository
  role       = "roles/artifactregistry.reader"
  member     = each.value
}

# --- the launchers ----------------------------------------------------------

# Creating a job that names a custom service account needs Batch Job Editor on
# the project AND Service Account User on that account
# (cloud.google.com/batch/docs/create-run-job-custom-service-account, checked
# 2026-10-15). The Condition is 57a-9's, read back as written on 2026-10-15:
# it removes the power to create, read, cancel or delete a job whose name is
# not Fugaro-shaped, and it does NOT constrain what a Fugaro-shaped job may
# contain (§3.7 says both, plainly).
resource "google_project_iam_member" "launcher_jobs_editor" {
  for_each = var.launchers

  project = var.project
  role    = "roles/batch.jobsEditor"
  member  = each.value

  condition {
    title       = "fugaro-jobs-only"
    description = "Batch jobs whose name starts with the Fugaro job prefix, in this region."
    expression  = "resource.name.startsWith(\"${local.job_name_prefix}\")"
  }
}

# actAs, as the cross product of the launchers and the run-class accounts:
# a launcher holds Service Account User on the accounts this installation
# named and on no others. It is what creating a job that runs as one of them
# needs (cloud.google.com/batch/docs/create-run-job-custom-service-account,
# checked 2026-10-15), and it is deliberately not actAs on the project.
resource "google_service_account_iam_member" "launcher_user" {
  # The pair is an object rather than a tuple: the member expression then
  # reads each.value.member, the reviewed shape every other grant here uses.
  for_each = {
    for p in setproduct(var.launchers, var.run_accounts) :
    "${p[0]}|${p[1]}" => { member = p[0], run_account = p[1] }
  }

  service_account_id = "projects/${var.project}/serviceAccounts/${trimprefix(each.value.run_account, "serviceAccount:")}"
  role               = "roles/iam.serviceAccountUser"
  member             = each.value.member
}

# --- the reaper -------------------------------------------------------------

# A dedicated account, used only by the scheduled sweeper (§3.7). Its display
# name is its ownership mark: a service account carries no labels.
resource "google_service_account" "reaper" {
  project      = var.project
  account_id   = var.names.reaper_account_id
  display_name = "Fugaro reaper"

  depends_on = [google_project_service.this]
}

# EXACTLY the six permissions the reaper's code uses, and no others
# (internal/backend/batch/reap.go, design §3.7): list and get every decision
# comes from, cancel for the queue deadline, delete for a stuck job and a
# stuck DELETION_IN_PROGRESS, and the project-wide aggregated instance list
# and delete for the instance pass. The role carries no create permission of
# any kind: a reaper that can create jobs and VMs is a second launcher.
resource "google_project_iam_custom_role" "reaper" {
  project     = var.project
  role_id     = var.names.reaper_role_id
  title       = "Fugaro reaper"
  description = "The scheduled sweeper's six permissions: read and end Batch jobs and the VMs of a run whose job is gone (design compute-backend §3.7)."

  permissions = [
    "batch.jobs.list",
    "batch.jobs.get",
    "batch.jobs.cancel",
    "batch.jobs.delete",
    "compute.instances.list",
    "compute.instances.delete",
  ]
}

resource "google_project_iam_member" "reaper" {
  project = var.project
  role    = google_project_iam_custom_role.reaper.name
  member  = google_service_account.reaper.member
}

# --- the preload builder ----------------------------------------------------

# The account the scheduled rebuild runs Cloud Build as, and the account the
# scheduler calls the Cloud Build API as. It holds the builder role below
# and the installation's build-submitter role (cloudbuild.builds.create/get),
# which is what creating a build needs.
resource "google_service_account" "preload" {
  project      = var.project
  account_id   = var.names.preload_account_id
  display_name = "Fugaro preload builder"

  depends_on = [google_project_service.this]
}

# The build boots one VM from Google's COS family, waits for the pull, stops
# it, images its disk and deletes the VM (57a-3b's procedure, §7.2). These are
# the permissions those four calls need and nothing more: no instance or image
# admin role, no setMetadata (which is how SSH keys reach a VM), no disk
# create or delete — the boot disk is the instance's own, and it goes with it.
resource "google_project_iam_custom_role" "preload_builder" {
  project     = var.project
  role_id     = var.names.preload_role_id
  title       = "Fugaro preload builder"
  description = "The preloaded boot image's build: boot a COS VM, read its startup script's serial output, stop it, image its disk, delete it (design compute-backend §1.2, C19)."

  permissions = [
    "compute.instances.create",
    "compute.instances.get",
    "compute.instances.list",
    "compute.instances.getSerialPortOutput",
    "compute.instances.stop",
    "compute.instances.delete",
    "compute.images.create",
    "compute.images.get",
    "compute.images.getFromFamily",
    "compute.images.delete",
    "compute.zoneOperations.get",
  ]
}

resource "google_project_iam_member" "preload_builder" {
  project = var.project
  role    = google_project_iam_custom_role.preload_builder.name
  member  = google_service_account.preload.member
}

resource "google_project_iam_member" "preload_build_submitter" {
  project = var.project
  role    = var.build_submitter_role
  member  = google_service_account.preload.member
}

# Cloud Build requires the build's service account to be able to write its own
# build logs (cloud.google.com/build/docs/securing-builds/configure-user-specified-service-accounts,
# checked 2026-10-15).
resource "google_project_iam_member" "preload_log_writer" {
  project = var.project
  role    = "roles/logging.logWriter"
  member  = google_service_account.preload.member
}

# Cloud Scheduler mints the OAuth token with which the rebuild job calls the
# Cloud Build API, from the preload account: the scheduler's own agent needs
# actAs on that account for it (the same shape the installation module's
# scheduler account carries).
resource "google_service_account_iam_member" "preload_scheduler_agent" {
  service_account_id = google_service_account.preload.name
  role               = "roles/iam.serviceAccountUser"
  member             = local.cloud_scheduler_agent
}

# The build runs as the preload account — not as the default Cloud Build
# account, which would bring whatever the project granted it — so Cloud
# Build's own agent needs actAs on it.
resource "google_service_account_iam_member" "preload_cloud_build_agent" {
  service_account_id = google_service_account.preload.name
  role               = "roles/iam.serviceAccountUser"
  member             = local.cloud_build_agent
}

# The builder VM the build boots runs as a run-class account: that is the
# account whose reader grant lets it pull the base image. Creating an instance
# that names an account needs actAs on it, and the caller here is the BUILD
# STEP — which runs as the preload account, not as Cloud Build's own agent —
# so the preload account is who holds it. actAs on each named account, never a
# role on the project, and never on the Cloud Build agent: that agent is
# reachable by anyone who can submit a build in this project, and actAs on a
# run account is the power to run code as it.
resource "google_service_account_iam_member" "preload_run_account_user" {
  for_each = var.run_accounts

  service_account_id = "projects/${var.project}/serviceAccounts/${trimprefix(each.value, "serviceAccount:")}"
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.preload.member
}

# The reader grant the builder VM's own pull needs: the same repository the
# run accounts read. One grant, not one per run account: the member is the
# preload account's own.
resource "google_artifact_registry_repository_iam_member" "preload_registry_reader" {
  project    = var.project
  location   = var.region
  repository = var.registry_repository
  role       = "roles/artifactregistry.reader"
  member     = google_service_account.preload.member
}
