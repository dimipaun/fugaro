# The deployer account of D75 (design managed-projects.md, answered
# 2026-10-16; Task 1.5 of docs/plans/2026-10-16-managed-projects.md): the
# identity a managed apply runs as, created by this owner-run apply and by
# nothing else.
#
# Why a separate identity at all: the creating apply is owner-run and the agent
# holds no project IAM authority, so the thing that applies an agent-authored
# plan has to be assumed rather than be the operator's own identity. The two
# custom roles below are what it holds, and the IAM condition on the second is
# the whole fence around it: fugaroDeployerScoped's permissions — every update,
# delete, run and pause permission, every *.setIamPolicy, actAs and, on an
# existing bucket, storage.buckets.get/update/delete/setIamPolicy and the
# objects in it — are held only where the resource's name carries the managed
# prefix (fmgd-), so the deployer cannot reach the runs bucket (where the
# marker and the owner's delete list are), Fugaro's own accounts, jobs,
# scheduler jobs, secrets, repositories, API key or state buckets, or the
# project's default compute account, which holds Editor by default.
#
# Two roles and not one bound twice: a binding grants its whole role, so a
# condition on one binding cannot fence permissions an unconditioned binding
# of the same role already grants project-wide.
#
# It exists in every installation, belong included (C8's answer, 2026-10-16:
# the installation module, not a fourth root). That is the owner's accepted
# D75 cost — one service account per installation — and it is an owner-run
# apply either way: nothing can use the account unless an operator impersonates
# it, and it holds only the permissions below.
#
# GATED OFF since 0.8.6 (owner's ruling, 2026-10-18: "Gate it off by default;
# redesign for 0.9.0" — moved to 0.9.5 by the owner's ruling of 2026-10-19, so
# 0.9.0 ships with the role still gated off, as 0.8.6 and 0.8.7 do). The scoped
# role and its conditioned binding exist only when var.managed_deployer_scoped
# is true, and nothing sets it: the condition
# below cannot be accepted by IAM as written. It joins 9 clauses with 17
# logical operators where IAM allows 12, it calls resource.name.contains(),
# which is not a supported function (startsWith, endsWith, extract, == and !=
# are), and resource.name is not supported at all for Artifact Registry
# repositories, Cloud Run jobs, Cloud Scheduler jobs, IAM service accounts or
# API keys. setIamPolicy would reject the binding and stop every fugaro init.
# Until the fence is redesigned and measured live (0.9.5), the account, the
# unconditioned create-and-read role and the operators' impersonation grant
# are created as before, and every command that would use the deployer
# refuses first (internal/cli's managedDeployerShipped).

resource "google_service_account" "fugaro_deployer" {
  project      = var.project
  account_id   = "fugaro-deployer"
  display_name = "Fugaro managed deployer"

  depends_on = [google_project_service.this]
}

# The condition of the scoped role's one binding, clause by clause — one clause
# per service, each with the resource type it admits, because a clause with no
# type term applies to every type the role reaches (a bucket named fmgd- is not
# a service account).
#
# Cloud Storage names a bucket projects/_/buckets/<bucket> and an object
# projects/_/buckets/<bucket>/objects/<object>, so its prefix is tested at the
# front of the name; the other services put the resource's own name at the end
# of a path whose middle carries the project, the location and the parent, so
# their prefix is tested with the segment's leading slash and the trailing dash
# ("/jobs/fmgd-"), which a name such as x-fmgd- does not match. Secret Manager
# names a secret with the project NUMBER and a version under the same secret, so
# one prefix covers both its types (no permission below reaches a version
# today; the clause is there so that adding one cannot outrun the fence).
# tf.DeployerConditionExpression is this string, byte for byte, and
# internal/infra/tf refuses a binding whose expression is not exactly it.
#
# NOT VERIFIED LIVE, and recorded as such rather than assumed: a storage
# object-prefix condition is proved (Check 32, RUN 2026-10-14), a Secret
# Manager name-prefix condition is Check 52's U3 and is still NOT RUN, and
# whether resource.name is honoured for an IAM service account, a Cloud Run
# job, a Cloud Scheduler job or an API key is what the plan's Check 68 has to
# measure. If a service does not honour it, the permission that needs it
# cannot be fenced, so Task 1.1 drops the matching type; none of these
# permissions is ever moved into fugaroDeployer (the unconditioned role) or
# into a binding with no condition.
locals {
  deployer_condition_title = "fugaro-managed-prefix"
  deployer_condition_expression = join(" || ", [
    "(resource.type == \"storage.googleapis.com/Bucket\" && resource.name.startsWith(\"projects/_/buckets/fmgd-\"))",
    "(resource.type == \"storage.googleapis.com/Object\" && resource.name.startsWith(\"projects/_/buckets/fmgd-\"))",
    "(resource.type == \"secretmanager.googleapis.com/Secret\" && resource.name.contains(\"/secrets/fmgd-\"))",
    "(resource.type == \"secretmanager.googleapis.com/SecretVersion\" && resource.name.contains(\"/secrets/fmgd-\"))",
    "(resource.type == \"artifactregistry.googleapis.com/Repository\" && resource.name.contains(\"/repositories/fmgd-\"))",
    "(resource.type == \"run.googleapis.com/Job\" && resource.name.contains(\"/jobs/fmgd-\"))",
    "(resource.type == \"cloudscheduler.googleapis.com/Job\" && resource.name.contains(\"/jobs/fmgd-\"))",
    "(resource.type == \"iam.googleapis.com/ServiceAccount\" && resource.name.contains(\"/serviceAccounts/fmgd-\"))",
    "(resource.type == \"apikeys.googleapis.com/Key\" && resource.name.contains(\"/keys/fmgd-\"))",
  ])
}

# fugaroDeployer: create permissions, read permissions and serviceusage
# .services.enable, bound to the account with NO condition. One permission per
# line, each with the resource type it serves; never a wildcard (GCP custom
# roles reject one). Nothing here updates, deletes, runs, pauses, sets a policy
# or acts as anything, and the only storage permission is storage.buckets
# .create: creating a bucket cannot touch an existing one, and everything an
# existing bucket's contents would need is in the other role, conditioned.
resource "google_project_iam_custom_role" "fugaro_deployer" {
  project = var.project
  role_id = "fugaroDeployer"
  title   = "Fugaro managed deployer (create and read)"
  permissions = [
    "apikeys.keys.create",                        # google_apikeys_key: create a managed project's own key, unconditioned, and update or delete only a prefixed one (the scoped role)
    "apikeys.keys.get",                           # the read the provider makes after creating the key
    "apikeys.keys.list",                          # ... and the read behind an import or a refresh
    "artifactregistry.repositories.create",       # google_artifact_registry_repository: a managed project's own images need a repository of their own
    "artifactregistry.repositories.get",          # the read the provider makes after creating the repository
    "artifactregistry.repositories.getIamPolicy", # the read the provider makes before the conditioned setIamPolicy of the same repository
    "artifactregistry.repositories.list",         # ... and the read behind a refresh
    "cloudscheduler.jobs.create",                 # google_cloud_scheduler_job: the schedule a managed project runs
    "cloudscheduler.jobs.get",                    # the read the provider makes after creating the job
    "cloudscheduler.jobs.list",                   # ... and the read behind a refresh
    "iam.serviceAccounts.create",                 # google_service_account: a managed project's workloads need accounts of their own; update and delete are conditioned
    "iam.serviceAccounts.get",                    # the read the provider makes after creating the account
    "iam.serviceAccounts.getIamPolicy",           # the read the provider makes before the conditioned setIamPolicy of the same account
    "iam.serviceAccounts.list",                   # ... and the read behind a refresh
    "run.jobs.create",                            # google_cloud_run_v2_job: the workload a managed project runs; update, delete and run are conditioned
    "run.jobs.get",                               # the read the provider makes after creating the job
    "run.jobs.getIamPolicy",                      # the read the provider makes before the conditioned setIamPolicy of the same job
    "run.jobs.list",                              # ... and the read behind a refresh
    "secretmanager.secrets.create",               # google_secret_manager_secret: the container, never a version — no secretmanager.versions.* anywhere
    "secretmanager.secrets.get",                  # the read the provider makes after creating the secret
    "secretmanager.secrets.getIamPolicy",         # the read the provider makes before the conditioned setIamPolicy of the same secret
    "secretmanager.secrets.list",                 # ... and the read behind a refresh
    "serviceusage.services.enable",               # google_project_service: a managed plan may enable an API. Not disable: a delete is the owner's to approve, and the deployer could not apply it
    "serviceusage.services.get",                  # the read the provider makes after enabling the service
    "serviceusage.services.list",                 # ... and the read behind a refresh
    "storage.buckets.create",                     # google_storage_bucket: a managed project's own storage. The only storage permission here; get, update, delete and setIamPolicy are conditioned in the other role
  ]

  depends_on = [google_project_service.this]
}

# fugaroDeployerScoped: every update, delete, run and pause permission, every
# *.setIamPolicy, iam.serviceAccounts.actAs, and on an existing bucket or
# object exactly the nine storage permissions D75 names. Bound to the account
# ONCE, under the condition above, and to nothing else — no third binding of
# this role exists, so no permission below is held unconditioned.
#
# What is not here and why: no resourcemanager.projects.* at all (project-level
# IAM authority is the authority that could grant this account object access to
# the runs bucket and forge the marker), no iam.roles.* at all (an account that
# can edit either role can widen itself, and the owner-run setup creates the
# roles — no managed plan does), no cloudbuild/secretmanager.versions/keys
# permission, and nothing server-named that a prefix cannot fence (a Cloud Run
# operation, for instance: a poll the provider makes may need one, and it fails
# closed rather than being granted unfenced).
resource "google_project_iam_custom_role" "fugaro_deployer_scoped" {
  # Off unless the installation opts in; see the header (0.8.6 gate).
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerScoped"
  title   = "Fugaro managed deployer (prefixed resources only)"
  permissions = [
    "apikeys.keys.delete",                        # google_apikeys_key: delete a key of the plan's own, prefixed, never Fugaro's own fugaro-web
    "apikeys.keys.update",                        # ... and update one, which is how an agent changes its key's restrictions
    "artifactregistry.repositories.delete",       # google_artifact_registry_repository: delete a prefixed repository, never Fugaro's fugaro-base or fugaro-ghcr
    "artifactregistry.repositories.setIamPolicy", # google_artifact_registry_repository_iam_member: the grant on a repository the plan itself created
    "artifactregistry.repositories.update",       # ... and update it (labels, cleanup policy)
    "cloudscheduler.jobs.delete",                 # google_cloud_scheduler_job: delete a prefixed job
    "cloudscheduler.jobs.pause",                  # ... pause it
    "cloudscheduler.jobs.enable",                 # ... and resume it (GCP names resume "enable"; cloudscheduler.jobs.resume does not exist): the pair of pause, so a job the plan paused cannot be stranded
    "cloudscheduler.jobs.run",                    # ... and run it once
    "cloudscheduler.jobs.update",                 # ... and update it, which is what an apply of a changed schedule does
    "iam.serviceAccounts.actAs",                  # a job or a schedule runs as an account of the plan's own, prefixed — so it can never act as the default compute account or Fugaro's own
    "iam.serviceAccounts.delete",                 # google_service_account: delete a prefixed account
    "iam.serviceAccounts.setIamPolicy",           # google_service_account_iam_member: the grant on an account the plan itself created
    "iam.serviceAccounts.update",                 # ... and update one (display name); iam.serviceAccounts.disable/enable are not granted
    "run.jobs.delete",                            # google_cloud_run_v2_job: delete a prefixed job, never Fugaro's own history or check jobs
    "run.jobs.run",                               # ... run it
    "run.jobs.runWithOverrides",                  # ... and run it with overrides, which is how a run passes its arguments
    "run.jobs.setIamPolicy",                      # google_cloud_run_v2_job_iam_member: the invoker grant on a job the plan itself created
    "run.jobs.update",                            # ... and update it, which is what an apply of a changed job does
    "secretmanager.secrets.delete",               # google_secret_manager_secret: delete a prefixed container, never Fugaro's own secrets
    "secretmanager.secrets.setIamPolicy",         # google_secret_manager_secret_iam_member: the grant on a secret the plan itself created
    "secretmanager.secrets.update",               # ... and update it (labels, rotation); no versions.access, so no value is ever readable
    "storage.buckets.delete",                     # google_storage_bucket: delete a prefixed bucket, never the runs bucket or a state bucket
    "storage.buckets.get",                        # ... read one, which is the refresh every bucket change makes
    "storage.buckets.setIamPolicy",               # google_storage_bucket_iam_member: the grant on a bucket the plan itself created
    "storage.buckets.update",                     # ... and update one (labels, lifecycle, access settings)
    "storage.objects.create",                     # google_storage_bucket_object: an object in a bucket the plan itself created (the prefix is the bucket's, so an object in the runs bucket is out of reach)
    "storage.objects.delete",                     # ... delete one
    "storage.objects.get",                        # ... read one
    "storage.objects.list",                       # ... list them, which needs the bucket's own name to carry the prefix
    "storage.objects.update",                     # ... and update one
  ]

  depends_on = [google_project_service.this]
}

# Binding (1): the unconditioned role, on the account, and nothing else holds
# it. The role is a LITERAL path rather than a reference to the resource above,
# and so is binding (2)'s: a role known only after apply cannot be told from
# the scoped one at plan time, and internal/infra/tf refuses a project grant
# whose role is computed while a deployer role is being created (Task 1.5
# step 5). var.project is known at plan time, so the literal is.
resource "google_project_iam_member" "fugaro_deployer" {
  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployer"
  member  = google_service_account.fugaro_deployer.member

  depends_on = [google_project_iam_custom_role.fugaro_deployer]
}

# Binding (2): the only binding of the scoped role, and the condition is the
# whole fence. A plan that creates this binding with the condition removed or
# widened is refused by internal/infra/tf, byte for byte.
resource "google_project_iam_member" "fugaro_deployer_scoped" {
  # Off unless the installation opts in, together with the role it binds.
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerScoped"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_expression
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_scoped]
}

# The one-time impersonation setup: the operators, and nobody else, may assume
# the deployer account. It is a grant on the account (not on the project), so
# it reaches no other identity, and it is made once per installation rather
# than per run.
#
# What the owner has to run, and what to check — the two reads that show the
# grant is there, printed by the same code that uses the credential
# (internal/managed/deployer.go's refusals name them):
#
#   gcloud iam service-accounts get-iam-policy fugaro-deployer@<project>.iam.gserviceaccount.com \
#     --format='value(bindings[].role)'
#   gcloud projects get-iam-policy <project> \
#     --flatten='bindings[].members' --filter='bindings.members:fugaro-deployer@<project>.iam.gserviceaccount.com' \
#     --format='table(bindings.role)'
#
# The first shows roles/iam.serviceAccountTokenCreator for the operators (this
# resource); the second shows the two custom roles D75 gives the account, the
# scoped one under its fmgd- condition, or, while var.managed_deployer_scoped
# is off (every installation in 0.8.6), fugaroDeployer alone.
resource "google_service_account_iam_member" "fugaro_deployer_token_creator" {
  for_each = toset(var.operators)

  # The account is a literal path built from var.project, not the account
  # resource's own name attribute: that attribute is unknown at plan time (the
  # account is created by this same apply), and a grant whose account nobody
  # can name is a grant nobody can check. internal/infra/tf covers this role
  # only on the deployer account (deployerGrant), so the plan has to say which
  # account it is on — and the ID is fixed (tf.DeployerAccountID,
  # "fugaro-deployer"), so the literal cannot name anything but the account
  # above.
  service_account_id = "projects/${var.project}/serviceAccounts/fugaro-deployer@${var.project}.iam.gserviceaccount.com"
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = each.value

  depends_on = [google_service_account.fugaro_deployer]
}
