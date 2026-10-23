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
#   - the REAPER: five permissions in a custom role of its own, and nothing
#     else. NOT roles/batch.jobsEditor and NOT roles/compute.instanceAdmin.v1:
#     both are far wider than the five, and a reaper account that can create
#     jobs and VMs is a second launcher, not a janitor (§3.7). The list used
#     to name six, one of them batch.jobs.cancel -- a permission Google does
#     not define (the Batch API lists jobs.create, delete, get and list), so
#     the role could never be created and --provision-batch was refused until
#     it was fixed. The queue deadline deletes now: see the role below.
#
# No account here is the default Compute Engine service account, and none is
# the installation's history account.
#
# THE TWO CUSTOM ROLES (the reaper's and the preload builder's) have a
# teardown property this module cannot express and an operator has to know:
# `terraform destroy` (or dropping this root) deletes them, and a deleted
# custom role is NOT gone — "You have 7 days to undelete the custom role.
# After 7 days ... The custom role is permanently deleted and cannot be
# recovered", and until then "A deleted custom role still counts toward the
# custom role limit"
# (cloud.google.com/iam/docs/reference/rest/v1/projects.roles/delete, checked
# 2026-10-16). So a destroy-then-re-apply inside those 7 days is a re-apply
# against a role id that still exists, which is a plan-time or apply-time
# failure rather than a fresh create — and the fix is either
# `gcloud iam roles undelete <role_id> --project=<project>` (documented on
# cloud.google.com/iam/docs/creating-custom-roles, same date) or a different
# role id. Whether a create with the SAME id inside the window is refused
# (ALREADY_EXISTS) is **UNVERIFIED**: the pages this run read state that the
# role still exists and counts, and do not state the create's answer. The
# module deletes nothing itself and has no `prevent_destroy`, deliberately:
# the roles are re-creatable, and a root that refuses to be destroyed is worse
# for an operator than this note is.

locals {
  # The preload account's email, as the rebuild's Cloud Build spec and the
  # Scheduler job's OAuth token take it. The reaper's own email has no reader:
  # every binding on it names the account resource that creates it
  # (google_service_account.reaper), which is also what orders the binding
  # after the account. Ordering is not the propagation wait of §7.2 (a fresh
  # account is not visible to binding calls for a while): that wait is the
  # compute stage's, which retries an apply that fails with "Service account
  # ... does not exist" (internal/cli/init_compute.go, computeStage.apply).
  preload_email = "${var.names.preload_account_id}@${var.project}.iam.gserviceaccount.com"
  # The service agents that must be able to act as an account this module
  # creates: Cloud Build runs the preload build as the preload account, and
  # Cloud Scheduler calls the Cloud Build API as it. Both are Google's own
  # per-project agents, named from the project NUMBER, and both grants are
  # actAs on one named account rather than a role on the project.
  #
  # The Cloud Build principal is its SERVICE AGENT, service-<number>@gcp-sa-
  # cloudbuild.iam.gserviceaccount.com (cloud.google.com/build/docs/securing-
  # builds/configure-access-for-cloud-build-service-account, checked
  # 2026-10-16). It is NOT service-<number>@cloudbuild.gserviceaccount.com:
  # that is no principal at all. The bare-number form that does exist,
  # <number>@cloudbuild.gserviceaccount.com, is the LEGACY default build
  # service account -- a build identity an organization may choose, not the
  # agent that acts for Cloud Build, and granting actAs to it would hand a
  # build identity this module never uses the power to run as the preload
  # account (cloud.google.com/build/docs/cloud-build-service-account, checked
  # 2026-10-16). The agent is created when the Cloud Build API is enabled --
  # and the file that enables it is modules/installation/apis.tf
  # (cloudbuild.googleapis.com), NOT this module's apis.tf, which enables
  # compute and batch only: the builds this backend's preload runs are the
  # installation's, so an installation applied as the docs prescribe has the
  # agent in place by the time this binding is applied. On the
  # manage_apis = false path (set on BOTH roots) NEITHER module enables it, so
  # there is no agent for this binding to name and the scheduled rebuild has
  # no principal to run as; that path is the operator's to satisfy. What GCP
  # answers for a binding whose member does not exist is not asserted here.
  cloud_build_agent     = "serviceAccount:service-${var.project_number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
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
#
# The holder is the run accounts and only the run accounts, and that is what
# the preload's own pull uses: the builder VM boots as a run-class account
# (preload.sh's --service-account=$RUN_ACCOUNT) and the docker pull runs in
# its startup script, so the credential that reads this repository is the run
# account's. The preload account pulls nothing from it -- the build step's own
# image comes from gcr.io -- so a grant naming it would have no reader at all.
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

# EXACTLY the five permissions the reaper's code uses, and no others
# (internal/backend/batch/reap.go, design §3.7): list and get every decision
# comes from, delete for the queue deadline, for a stuck job and for a stuck
# DELETION_IN_PROGRESS, and the project-wide aggregated instance list and
# delete for the instance pass. The role carries no create permission of any
# kind: a reaper that can create jobs and VMs is a second launcher.
#
# NO batch.jobs.cancel, and there must never be one again. It is not a
# permission Google has: the Batch API defines jobs.create, jobs.delete,
# jobs.get and jobs.list and nothing else, so a custom role naming it cannot
# be created at all (that is what kept --provision-batch refused). The queue
# deadline -- a job that has sat in QUEUED past its deadline, which counts
# against max_parallel and can wait out Batch's own 2-day ceiling -- is
# cleared with jobs.delete instead, which this role already holds and which
# is strictly the more destructive verb. What a cancel would have kept is the
# job's record, and a job that never left QUEUED never ran a task: the run's
# own launch.json and result.json in the runs bucket are what fugaro ls,
# diagnose and watch read for it. internal/backend/batch's reaper reads a
# queued job back before it deletes it, so a job that started in the meantime
# -- a run whose record DOES matter -- is left to the next pass. The get
# above is that read-back's permission.
resource "google_project_iam_custom_role" "reaper" {
  project     = var.project
  role_id     = var.names.reaper_role_id
  title       = "Fugaro reaper"
  description = "The scheduled sweeper's five permissions: read and end Batch jobs and the VMs of a run whose job is gone (design compute-backend §3.7)."

  permissions = [
    "batch.jobs.list",
    "batch.jobs.get",
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
# it, images its disk and deletes the VM (57a-3b's procedure, §7.2). The list
# is what those gcloud calls need, which is a good deal more than
# instances.create: Google's own table checks each of the create call's
# arguments separately, so a boot disk (compute.disks.create), a network
# interface on the named subnetwork (compute.subnetworks.use), a startup
# script (compute.instances.setMetadata), labels
# (compute.instances.setLabels), the run account the VM runs as
# (compute.instances.setServiceAccount) and the image it boots from
# (compute.images.useReadOnly) are each their own permission. Imaging the
# stopped VM reads its disk (compute.disks.useReadOnly), labels the image
# (compute.images.setLabels), and the stale sweep lists the project's images
# (compute.images.list).
#
# What this is NOT: an instance or an image admin role. There is no
# compute.disks.delete — the boot disk is the instance's own and goes with it
# — and no permission anywhere to create a job or a second VM.
#
# One permission here is wider than this build uses and cannot be dropped:
# compute.instances.setMetadata is how a startup script is set and it is also
# how SSH keys reach a VM. A builder VM with no startup script pulls nothing,
# so the build has no version of itself that does not carry it; what keeps it
# small is that exactly one account holds it — the preload builder's, which
# nothing but the scheduled rebuild and its Cloud Build step runs as — and
# that the grant is this custom role, not a predefined one that would carry
# the same permission plus a hundred more.
resource "google_project_iam_custom_role" "preload_builder" {
  project     = var.project
  role_id     = var.names.preload_role_id
  title       = "Fugaro preload builder"
  description = "The preloaded boot image's build: boot a COS VM, read its startup script's serial output, stop it, image its disk, delete it (design compute-backend §1.2, C19)."

  permissions = [
    # The builder VM, and what its creation checks.
    "compute.instances.create",
    "compute.instances.setMetadata",
    "compute.instances.setLabels",
    "compute.instances.setServiceAccount",
    "compute.instances.get",
    "compute.instances.list",
    "compute.instances.getSerialPortOutput",
    "compute.instances.stop",
    "compute.instances.delete",
    # Its boot disk, the network interface, the image it boots from, and the
    # zone operation every one of these calls waits on.
    "compute.disks.create",
    "compute.subnetworks.use",
    "compute.images.useReadOnly",
    "compute.zoneOperations.get",
    # Imaging the stopped VM, and the stale sweep that follows it. Images are
    # GLOBAL resources, so their operations are global ones: images.create and
    # images.delete are authorized by their own permission, but the call then
    # WAITS on projects/<project>/global/operations/<id> and polling it needs
    # compute.globalOperations.get ("Operations can be global, regional or
    # zonal. For global operations, use the globalOperations resource" —
    # cloud.google.com/compute/docs/reference/rest/v1/images/insert, checked
    # 2026-10-16). Without it the generation IS created and the call still
    # exits non-zero, so `set -e` fails every build that makes one; the same
    # missing permission makes step 8's delete fail, and its `|| true` swallows
    # it, so the sweep would never happen. Every predefined role that can
    # create an image carries this next to compute.images.create (for example
    # roles/compute.storageAdmin, cloud.google.com/iam/docs/roles-permissions/
    # compute, same date); the zone operation below is the same requirement for
    # the zonal calls (the instance and its disk).
    "compute.disks.useReadOnly",
    "compute.images.create",
    "compute.images.setLabels",
    "compute.images.get",
    "compute.images.getFromFamily",
    "compute.images.list",
    "compute.images.delete",
    "compute.globalOperations.get",
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
# Build's own agent needs actAs on it: the agent is what Cloud Build acts as
# for the project, and it is the principal that starts the step running as
# the build's service account.
resource "google_service_account_iam_member" "preload_cloud_build_agent" {
  service_account_id = google_service_account.preload.name
  role               = "roles/iam.serviceAccountUser"
  member             = local.cloud_build_agent
}

# The preload account holds actAs on ITSELF, and that is what makes the
# scheduled call work at all: Cloud Scheduler calls the Cloud Build API with
# an OAuth token minted from the preload account (preload.tf), and the build
# it creates names the preload account as its `serviceAccount`, so the caller
# and the build's identity are the same account. Google's note on that case
# is that the account "must still have the iam.serviceAccounts.actAs
# permission" (cloud.google.com/build/docs/cloud-build-service-account,
# checked 2026-10-16), and a service account does not hold actAs on itself by
# default. Without it both the weekly rebuild and the first build the stage
# starts fail with PERMISSION_DENIED before any step runs.
#
# Narrow by construction: it is actAs on this one account, held by that same
# account, so it grants no one anything they did not already have. It is not
# the actAs-through-the-agent chain the run-account comment below states;
# this is the account's own.
resource "google_service_account_iam_member" "preload_self_user" {
  service_account_id = google_service_account.preload.name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.preload.member
}

# The builder VM the build boots runs as a run-class account: that is the
# account whose reader grant lets it pull the base image. Creating an instance
# that names an account needs actAs on it, and the caller here is the BUILD
# STEP — which runs as the preload account, not as Cloud Build's own agent —
# so the preload account is who holds it. actAs on each named account, never a
# role on the project, and never on the Cloud Build agent.
#
# The "never on the Cloud Build agent" above is about THIS grant, not a claim
# that no build path reaches a run account: naming the preload account as a
# build's `serviceAccount` takes actAs on that account — which the operators
# hold (preload_operator) and the account holds on itself (preload_self_user)
# — and such a build boots a builder VM as a run account. So an operator can
# run code as a run account through one build and one VM, which is the chain
# §3.7's separation of launcher and operator is about and why that grant is
# actAs on the preload account and never a role on the project. What this
# binding refuses is the shorter path: the agent acting on a run account
# directly, with no build step and no operator in between.
resource "google_service_account_iam_member" "preload_run_account_user" {
  for_each = var.run_accounts

  service_account_id = "projects/${var.project}/serviceAccounts/${trimprefix(each.value, "serviceAccount:")}"
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.preload.member
}

# The installation's operators, who apply this root. Creating the Scheduler job
# of preload.tf — whose OAuth token is minted from the preload account — needs
# actAs on that account (cloud.google.com/scheduler/docs/http-target-auth,
# checked 2026-10-15), and it is the same grant the installation module makes on
# its own scheduler account for the same reason
# (modules/installation/scheduler.tf). It is actAs on this one account, never a
# role on the project: an operator who may run a rebuild must not thereby be
# able to run code as every account in it.
resource "google_service_account_iam_member" "preload_operator" {
  for_each = var.operators

  service_account_id = google_service_account.preload.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}
