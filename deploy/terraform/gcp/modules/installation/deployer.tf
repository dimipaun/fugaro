# The deployer account of D75 (design managed-projects.md, answered
# 2026-10-16; Task 1.5 of docs/plans/2026-10-16-managed-projects.md): the
# identity a managed apply runs as, created by this owner-run apply and by
# nothing else.
#
# Why a separate identity at all: the creating apply is owner-run and the agent
# holds no project IAM authority, so the thing that applies an agent-authored
# plan has to be assumed rather than be the operator's own identity. The eight
# custom roles below are what it holds, and the IAM conditions on the seven
# conditioned ones are the whole fence around it: every update, delete, run and
# pause permission, every *.setIamPolicy, actAs and, on an existing bucket, the
# nine storage permissions D75 names are held only where the resource's name
# carries the managed prefix (fmgd-), so the deployer cannot reach the runs
# bucket (where the marker and the owner's delete list are), Fugaro's own
# accounts, jobs, scheduler jobs, secrets, repositories, API key or state
# buckets, or the project's default compute account, which holds Editor by
# default.
#
# Two halves and not one role bound twice: a binding grants its whole role, so a
# condition on one binding cannot fence permissions an unconditioned binding of
# the same role already grants project-wide.
#
# ONE BINDING PER SERVICE (0.10.0 redesign, docs/design/managed-deployer-fence.md).
# Until 0.10.0 the conditioned half was ONE role, fugaroDeployerScoped, under one
# condition, and that condition could not be applied: it joined 9 clauses with
# 17 logical operators where IAM allows 12, it called resource.name.contains(),
# which is not a supported function (startsWith, endsWith, extract, == and !=
# are), and resource.name is not supported at all for Artifact Registry
# repositories, Cloud Run jobs, Cloud Scheduler jobs, IAM service accounts or
# API keys. setIamPolicy would have rejected the binding and stopped every
# fugaro init. The fix is the split: one custom role per service, each holding
# that service's slice of the 31 scoped permissions and each bound once under
# its own condition, which needs one or two clauses and so one `||` at most. The
# seven permission lists partition D75's 31 exactly — none added, none dropped,
# none moved into the unconditioned role.
#
# What none of them holds, and why: no resourcemanager.projects.* at all
# (project-level IAM authority is the authority that could grant this account
# object access to the runs bucket and forge the marker), no iam.roles.* at all
# (an account that can edit a role can widen itself, and the owner-run setup
# creates the roles — no managed plan does), no cloudbuild/secretmanager
# .versions/keys permission, and nothing server-named that a prefix cannot fence
# (a Cloud Run operation, for instance: a poll the provider makes may need one,
# and it fails closed rather than being granted unfenced).
#
# GATED OFF (owner's ruling, 2026-10-18: "Gate it off by default; redesign for
# 0.9.0"). The seven conditioned roles and their seven bindings exist only when
# var.managed_deployer_scoped is true, and nothing sets it, because the
# redesigned fence has not been measured live yet. Until it is (Check 68 of
# docs/gcp-live-checklist.md, which is written and NOT RUN), the account, the
# unconditioned create-and-read role and the operators' impersonation grant are
# created as before, and every command that would use the deployer refuses
# first (internal/cli's managedDeployerShipped).
#
# It exists in every installation, belong included (C8's answer, 2026-10-16:
# the installation module, not a fourth root). That is the owner's accepted
# D75 cost — one service account per installation — and it is an owner-run
# apply either way: nothing can use the account unless an operator impersonates
# it, and it holds only the permissions below.

resource "google_service_account" "fugaro_deployer" {
  project      = var.project
  account_id   = "fugaro-deployer"
  display_name = "Fugaro managed deployer"

  depends_on = [google_project_service.this]
}

# The conditions, one per service, each the whole fence around exactly one
# binding below. Every clause carries the resource type it admits, because a
# clause with no type term applies to every type the role reaches (a bucket
# named fmgd- is not a service account), and every clause names the reserved
# managed prefix (fmgd-) in the place that service's own resource name carries
# it. Two forms, and which service takes which is not a preference:
#
#   - startsWith tests the front of the whole resource name. Cloud Storage names
#     a bucket projects/_/buckets/<bucket> and an object
#     projects/_/buckets/<bucket>/objects/<object>, so the prefix is
#     projects/_/buckets/fmgd-; this is the one form the tree has measured live
#     (Check 32, RUN 2026-10-14). An IAM service account's name is
#     projects/<project>/serviceAccounts/<account>@<project>
#     .iam.gserviceaccount.com, and var.project is a plan-time literal, so that
#     service takes this form too.
#   - extract tests the resource's own name, not a "last segment" reading: the
#     text after the FIRST occurrence of the template's literal prefix. The
#     other five services need it because the segment before their resource's
#     own name carries a value this module does not have at plan time — Secret
#     Manager names a secret with the project NUMBER rather than the id, and
#     Artifact Registry, Cloud Run and Cloud Scheduler carry the location, which
#     a managed plan chooses and which is not even constant inside one
#     installation (this installation's scheduler runs in another region from
#     its jobs). A name that does not match the template makes the condition
#     false, on the reading this module follows rather than on a citation: the
#     design's C6 and C7 record that reading as UNVERIFIED — no source in this
#     tree quotes the reference's text for extract's grammar — and the reading is
#     that extract returns the text the template's ONE {identifier} placeholder
#     matched, so a name the template does not match extracts the empty string,
#     and "".startsWith("fmgd-") is false. The design's C7 is recorded that way
#     and modelled by internal/infra/tf/cover_test.go's extractSegment. Check 68
#     measures the two directions that can be constructed — a prefixed name and
#     one that does not carry the prefix, including a name that contains it
#     without starting with it and one the template names — but it cannot
#     construct the mismatch itself: every conditioned type's real resource name
#     matches its own template, so no name of that type can be made to miss it.
#     What remains open, and what the design's §8 point 2 asks the owner, is C3
#     — whether resource.name is honoured per type at all — and how much fence
#     there is if it is not.
#
# tf.deployerServices(project) is these strings, byte for byte, and
# internal/infra/tf refuses a binding whose expression is not exactly its own
# role's. The local's name is deployer_condition_<lowercased role suffix>:
# fugaroDeployerStorage is deployer_condition_storage, and so on.
#
# NOT VERIFIED LIVE, and recorded as such rather than assumed: a storage
# object-prefix condition is proved (Check 32, RUN 2026-10-14) and so is a
# Secret Manager name-prefix condition (Check 52's U3, RUN 2026-10-16: the
# condition is honoured for secretmanager.versions.access), while whether
# resource.name is honoured at all — let alone with extract — for an IAM service
# account, a Cloud Run job, a Cloud Scheduler job, an Artifact Registry
# repository or an API key is what Check 68 has to measure (its steps 4 and 5,
# per service and in both directions). If a service does
# not honour its form, the permission that needs it cannot be fenced, so the
# matching type is dropped; none of these permissions is ever moved into
# fugaroDeployer (the unconditioned role) or into a binding with no condition.
locals {
  # The title of all seven conditions. It names the fence rather than the
  # service, so a project's policy listing shows one fence around the deployer;
  # the role says which service a binding fences. The modules never set a
  # condition description.
  deployer_condition_title = "fugaro-managed-prefix"

  deployer_condition_storage = join(" || ", [
    "(resource.type == \"storage.googleapis.com/Bucket\" && resource.name.startsWith(\"projects/_/buckets/fmgd-\"))",
    "(resource.type == \"storage.googleapis.com/Object\" && resource.name.startsWith(\"projects/_/buckets/fmgd-\"))",
  ])

  # One clause, the container's own type: a version's name does not carry this
  # clause's type, and no permission of this role acts on a version. The
  # template is literal text and ONE {identifier} placeholder — the reading this
  # repository follows, and NOT a citation: no source in the tree quotes the
  # reference's text for resource.name.extract's grammar, which is why the
  # design's C6 carries that reading as UNVERIFIED — and it anchors at the
  # collection segment the resource's own name follows. The multi-placeholder
  # form ("projects/{project}/secrets/{secret}") is not that form, and a
  # condition IAM refuses stops the whole apply (the 0.8.6 outage); the project
  # in a secret's name is its NUMBER, which this module does not know at plan
  # time, so the anchor is the segment and not the front.
  deployer_condition_secret = join(" || ", [
    "(resource.type == \"secretmanager.googleapis.com/Secret\" && resource.name.extract(\"/secrets/{secret}\").startsWith(\"fmgd-\"))",
  ])

  deployer_condition_account = join(" || ", [
    "(resource.type == \"iam.googleapis.com/ServiceAccount\" && resource.name.startsWith(\"projects/${var.project}/serviceAccounts/fmgd-\"))",
  ])

  deployer_condition_apikey = join(" || ", [
    "(resource.type == \"apikeys.googleapis.com/Key\" && resource.name.extract(\"/keys/{key}\").startsWith(\"fmgd-\"))",
  ])

  deployer_condition_repository = join(" || ", [
    "(resource.type == \"artifactregistry.googleapis.com/Repository\" && resource.name.extract(\"/repositories/{repository}\").startsWith(\"fmgd-\"))",
  ])

  deployer_condition_runjob = join(" || ", [
    "(resource.type == \"run.googleapis.com/Job\" && resource.name.extract(\"/jobs/{job}\").startsWith(\"fmgd-\"))",
  ])

  deployer_condition_schedulerjob = join(" || ", [
    "(resource.type == \"cloudscheduler.googleapis.com/Job\" && resource.name.extract(\"/jobs/{job}\").startsWith(\"fmgd-\"))",
  ])
}

# fugaroDeployer: create permissions, read permissions and serviceusage
# .services.enable, bound to the account with NO condition. One permission per
# line, each with the resource type it serves; never a wildcard (GCP custom
# roles reject one). Nothing here updates, deletes, runs, pauses, sets a policy
# or acts as anything, and the only storage permission is storage.buckets
# .create: creating a bucket cannot touch an existing one, and everything an
# existing bucket's contents would need is in the conditioned half.
resource "google_project_iam_custom_role" "fugaro_deployer" {
  project = var.project
  role_id = "fugaroDeployer"
  title   = "Fugaro managed deployer (create and read)"
  permissions = [
    "apikeys.keys.create",                        # google_apikeys_key: create a managed project's own key, unconditioned, and update or delete only a prefixed one (the conditioned half)
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
    "storage.buckets.create",                     # google_storage_bucket: a managed project's own storage. The only storage permission here; get, update, delete and setIamPolicy are conditioned
  ]

  depends_on = [google_project_service.this]
}

# The conditioned half, one role per service. Each holds exactly the permissions
# of the service it names — nothing that reaches another service's API — and is
# bound by the one binding below that carries that service's condition.

# The nine storage permissions D75 names for an existing bucket and its
# objects. storage.buckets.create stays in the unconditioned role, where
# creating a bucket cannot touch one that exists.
resource "google_project_iam_custom_role" "fugaro_deployer_storage" {
  # Off unless the installation opts in; see the header (0.8.6 gate).
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerStorage"
  title   = "Fugaro managed deployer (prefixed storage only)"
  permissions = [
    "storage.buckets.delete",       # google_storage_bucket: delete a prefixed bucket, never the runs bucket or a state bucket
    "storage.buckets.get",          # ... read one, which is the refresh every bucket change makes
    "storage.buckets.setIamPolicy", # google_storage_bucket_iam_member: the grant on a bucket the plan itself created
    "storage.buckets.update",       # ... and update one (labels, lifecycle, access settings)
    "storage.objects.create",       # google_storage_bucket_object: an object in a bucket the plan itself created (the prefix is the bucket's, so an object in the runs bucket is out of reach)
    "storage.objects.delete",       # ... delete one
    "storage.objects.get",          # ... read one
    "storage.objects.list",         # ... list them, which needs the bucket's own name to carry the prefix
    "storage.objects.update",       # ... and update one
  ]

  depends_on = [google_project_service.this]
}

# The container, never a version: no secretmanager.versions.* anywhere, so no
# value is ever readable by the deployer.
resource "google_project_iam_custom_role" "fugaro_deployer_secret" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerSecret"
  title   = "Fugaro managed deployer (prefixed secrets only)"
  permissions = [
    "secretmanager.secrets.delete",       # google_secret_manager_secret: delete a prefixed container, never Fugaro's own secrets
    "secretmanager.secrets.setIamPolicy", # google_secret_manager_secret_iam_member: the grant on a secret the plan itself created
    "secretmanager.secrets.update",       # ... and update it (labels, rotation)
  ]

  depends_on = [google_project_service.this]
}

# actAs is here because a job or a schedule runs as an account of the plan's
# own: under this condition it can never act as the default compute account or
# as Fugaro's own.
resource "google_project_iam_custom_role" "fugaro_deployer_account" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerAccount"
  title   = "Fugaro managed deployer (prefixed accounts only)"
  permissions = [
    "iam.serviceAccounts.actAs",        # a job or a schedule runs as an account of the plan's own, prefixed
    "iam.serviceAccounts.delete",       # google_service_account: delete a prefixed account
    "iam.serviceAccounts.setIamPolicy", # google_service_account_iam_member: the grant on an account the plan itself created
    "iam.serviceAccounts.update",       # ... and update one (display name); iam.serviceAccounts.disable/enable are not granted
  ]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_custom_role" "fugaro_deployer_apikey" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerApiKey"
  title   = "Fugaro managed deployer (prefixed API keys only)"
  permissions = [
    "apikeys.keys.delete", # google_apikeys_key: delete a key of the plan's own, prefixed, never Fugaro's own fugaro-web
    "apikeys.keys.update", # ... and update one, which is how an agent changes its key's restrictions
  ]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_custom_role" "fugaro_deployer_repository" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerRepository"
  title   = "Fugaro managed deployer (prefixed repositories only)"
  permissions = [
    "artifactregistry.repositories.delete",       # google_artifact_registry_repository: delete a prefixed repository, never Fugaro's fugaro-base or fugaro-ghcr
    "artifactregistry.repositories.setIamPolicy", # google_artifact_registry_repository_iam_member: the grant on a repository the plan itself created
    "artifactregistry.repositories.update",       # ... and update it (labels, cleanup policy)
  ]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_custom_role" "fugaro_deployer_run_job" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerRunJob"
  title   = "Fugaro managed deployer (prefixed Cloud Run jobs only)"
  permissions = [
    "run.jobs.delete",           # google_cloud_run_v2_job: delete a prefixed job, never Fugaro's own history or check jobs
    "run.jobs.run",              # ... run it
    "run.jobs.runWithOverrides", # ... and run it with overrides, which is how a run passes its arguments
    "run.jobs.setIamPolicy",     # google_cloud_run_v2_job_iam_member: the invoker grant on a job the plan itself created
    "run.jobs.update",           # ... and update it, which is what an apply of a changed job does
  ]

  depends_on = [google_project_service.this]
}

resource "google_project_iam_custom_role" "fugaro_deployer_scheduler_job" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role_id = "fugaroDeployerSchedulerJob"
  title   = "Fugaro managed deployer (prefixed Cloud Scheduler jobs only)"
  permissions = [
    "cloudscheduler.jobs.delete", # google_cloud_scheduler_job: delete a prefixed job
    "cloudscheduler.jobs.enable", # ... and resume it (GCP names resume "enable"; cloudscheduler.jobs.resume does not exist): the pair of pause, so a job the plan paused cannot be stranded
    "cloudscheduler.jobs.pause",  # ... pause it
    "cloudscheduler.jobs.run",    # ... and run it once
    "cloudscheduler.jobs.update", # ... and update it, which is what an apply of a changed schedule does
  ]

  depends_on = [google_project_service.this]
}

# Binding (1): the unconditioned role, on the account, and nothing else holds
# it. The role is a LITERAL path rather than a reference to the resource above,
# and so is every conditioned binding's: a role known only after apply cannot be
# told from a conditioned one at plan time, and internal/infra/tf refuses a
# project grant whose role is computed while a deployer role is being created
# (Task 1.5 step 5). var.project is known at plan time, so the literal is.
resource "google_project_iam_member" "fugaro_deployer" {
  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployer"
  member  = google_service_account.fugaro_deployer.member

  depends_on = [google_project_iam_custom_role.fugaro_deployer]
}

# Bindings (2)-(8): one per service, each the ONLY binding of its role, and each
# carrying that role's own condition and no other. A plan that creates one of
# these with its condition removed, widened, or replaced by another service's is
# refused by internal/infra/tf, byte for byte — which is also why the seven
# bindings are written out rather than driven by a for_each over a map: a
# generated binding is one an operator cannot read off the policy listing, and
# one whose role the module pin test cannot name.
resource "google_project_iam_member" "fugaro_deployer_storage" {
  # Off unless the installation opts in, together with the role it binds.
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerStorage"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_storage
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_storage]
}

resource "google_project_iam_member" "fugaro_deployer_secret" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerSecret"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_secret
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_secret]
}

resource "google_project_iam_member" "fugaro_deployer_account" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerAccount"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_account
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_account]
}

resource "google_project_iam_member" "fugaro_deployer_apikey" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerApiKey"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_apikey
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_apikey]
}

resource "google_project_iam_member" "fugaro_deployer_repository" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerRepository"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_repository
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_repository]
}

resource "google_project_iam_member" "fugaro_deployer_run_job" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerRunJob"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_runjob
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_run_job]
}

resource "google_project_iam_member" "fugaro_deployer_scheduler_job" {
  count = var.managed_deployer_scoped ? 1 : 0

  project = var.project
  role    = "projects/${var.project}/roles/fugaroDeployerSchedulerJob"
  member  = google_service_account.fugaro_deployer.member

  condition {
    title      = local.deployer_condition_title
    expression = local.deployer_condition_schedulerjob
  }

  depends_on = [google_project_iam_custom_role.fugaro_deployer_scheduler_job]
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
# resource); the second shows the custom roles D75 gives the account — the
# unconditioned fugaroDeployer, and, when var.managed_deployer_scoped is on,
# the seven conditioned ones, each under its own fmgd- condition. While the
# flag is off (every installation in 0.8.6), the second shows fugaroDeployer
# alone.
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
