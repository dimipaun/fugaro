# The project job's service account and everything it is granted: one per
# (profile, provider, account), because the secrets a job mounts are readable
# by every run in that job (design §4.1, §7).

resource "google_service_account" "job" {
  project      = var.project
  account_id   = var.job.service_account.account_id
  display_name = var.job.service_account.display_name
}

# Deploying or updating the job acts as its account.
resource "google_service_account_iam_member" "operator" {
  for_each = toset(var.operators)

  service_account_id = google_service_account.job.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# The runs bucket, conditioned on runs/, cache/ and locks/ — the whole
# prefixes, with no slug (gcp.ProjectBucketCondition, plan task 3.2 step 2).
#
# This is a widening of the 0.7.0 property that a job account's objects are
# its own repository's: one project job serves every repository admitted to
# its profile, so the object it writes is no longer inside one repository's
# prefix. SECURITY.md and docs/bucket-iam/spec.md state the regression in
# full (D4), including another queued run's budget token (D13, accepted
# 2026-10-15) and another run's launch.json/result.json (task 3.4). Every
# project job gets this same condition whatever its profile, so a profile
# separates secrets, not bucket objects: the mitigation D4 named (separate
# profiles) does not hold for the bucket, and the owner ruled the question on
# 2026-10-17 — the installation-wide reach is ACCEPTED, on the terms
# SECURITY.md states. A profile segment in the object layout is a later
# breaking change, not phase 3's.
#
# The condition is the input byte for byte: one that differs is a different
# binding, and the review screen (internal/infra/tf) accepts it only for an
# account this module creates. The bootstrap set no description, so none is
# set here.
resource "google_storage_bucket_iam_member" "job" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.job.member

  condition {
    title      = var.job.bucket_condition.title
    expression = var.job.bucket_condition.expression
  }
}

# The whole of tokens/, read-only (gcp.ProjectTokenBucketCondition, plan task
# 3.2 step 2; budget-token-channel §7.2). The widened objectUser grant above is
# on runs/, cache/ and locks/ and never covers tokens/; this one is
# objectViewer, so the account can read and can neither delete nor replace. The
# token it reads is ciphertext — sealed under a key only the execution its
# launch minted it for receives — but tokens/ is not all ciphertext: a person's
# messages to a running run live there in plaintext (docs/run-messages, D2941,
# F2), so on a shared project job every repository's agent can read every
# repository's messages. That is the reach it already has over runs/, whose
# task.json, the brief itself, it reads the same way. The widening is the same
# one D4 documents for the other three prefixes, for the same reason: one job
# serves every repository admitted to its profile, so its read cannot name one
# repository's prefix.
resource "google_storage_bucket_iam_member" "job_tokens" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectViewer"
  member = google_service_account.job.member

  condition {
    title      = var.job.token_condition.title
    expression = var.job.token_condition.expression
  }

  lifecycle {
    precondition {
      condition     = startswith(var.job.token_condition.expression, "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/") && !strcontains(var.job.token_condition.expression, "||")
      error_message = "job.token_condition must name tokens/ of this runs bucket, in one clause (gcp.ProjectTokenBucketCondition)."
    }
  }
}

locals {
  # The classes of secret the credentials service SERVES to a shared project
  # job (B5.7, the S5 flip; D505, D2078): a class named here is served, so a
  # switched profile's job mounts none of that class's credentials — the
  # accessor below and the mount in job.tf go together, because a mount the
  # job's account keeps no accessor on is a job Cloud Run refuses to create
  # (C2.3) — and the run's own calls are the service's for this profile's
  # repositories (B5.1, B5.2).
  #
  # The one member is config.SharedJobClassModel, and this literal is held to
  # config.SharedJobServedClasses by deploy/terraform's
  # TestSharedJobServedClassesMatchTheModule, the way the module's other flip
  # lists are held to theirs: Terraform cannot ask Go.
  #
  # The empty list is the inert state and the first of the flip's two commits:
  # with it empty local.is_shared_job_served_model is false for every mount, so
  # this file and job.tf plan exactly what they planned before they knew about
  # a served class (D147). Filled, what it moves is the model mounts of a
  # profile the switch serves and nothing else — an unswitched profile keeps
  # every accessor and every mount, before and after (G9, G17).
  #
  # The GitHub App's key is deliberately not a class here: its drop is
  # config.ServiceHeldSecretNames' own flip (#907, C2.3), and a shared job that
  # gave the key up before the runner stopped reading it would fail every
  # GitHub run it starts (G22). A provider's key is not dropped either, though
  # the class is filled whole: its drop waits for the service to serve one, and
  # that is the keys-through-service plan's KS16 (see model_secret_env_names
  # below).
  shared_job_served_secret_classes = ["model"]

  # The service the account may invoke: the served profile's, or the one a
  # grant every shared job holds with every mount kept.
  credentials_grant = var.job.credentials_service != null ? var.job.credentials_service : var.job.credentials_service_grant

  # The model credentials the class's drop reaches, identified the way
  # internal/config identifies them (config.ModelSecretEnvs): the two reserved
  # variables, so a mount is one of them exactly when its VARIABLE is — and a
  # provider's key is NOT here, because nothing serves one yet, so its drop is
  # the keys-through-service plan's KS16 (config.IsSharedJobServedModelEnv, R6,
  # D1634). A provider's key would need the namespace rather than a name
  # (config.ProviderKeyEnvPrefix), which is why this list is variables where
  # the class list above is class names.
  model_secret_env_names = ["ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]

  # One entry per mount: this mount is a model credential the credentials
  # service serves, exactly when the class list carries "model" AND the
  # profile's own switch is on. Both halves are needed — the list alone would
  # take the credential from a profile the service does not serve, the switch
  # alone from a profile whose class is not filled yet — and both clauses below
  # read this one local, so the accessor and the mount cannot drift apart.
  is_shared_job_served_model = {
    for env, logical in var.job.secret_env :
    env => var.job.credentials_service != null && contains(local.shared_job_served_secret_classes, "model") && contains(local.model_secret_env_names, env)
  }

  # The secrets the job's account may still read, by logical name: every mount
  # while the credentials service is off — D147's "with it off nothing
  # changes", and the same set this module granted before it knew about the
  # service — and, once it is on, every mount except those two.
  # toset, not the list of mounts: two variables may mount the same logical
  # name, and a repeated name in a for_each is an error rather than a second
  # binding.
  job_secret_names = toset([
    for env, logical in var.job.secret_env :
    logical if !local.is_shared_job_served_model[env]
  ])

  # The secrets the job's CONTAINER still declares, by the variable each is
  # mounted as: the same set, read where the mount is declared (job.tf). Go's
  # internal/infra drops the same entries from the spec this module's tfvars
  # are generated from (projectJobMounts), which is the drop that fires in
  # practice; this local is the rule written down where the accessor is
  # decided, so a tfvars that still names a served model credential cannot be
  # applied into a job with no way to read it.
  job_mounts = {
    for env, logical in var.job.secret_env :
    env => logical if !local.is_shared_job_served_model[env]
  }
}

# Each project-wide secret the profile mounts, at the secret level, never the
# project's. The value is the logical name, so the grant waits for the
# container the installation module creates or adopts.
#
# One grant per secret the profile names, and none by name prefix: the
# design's §4.5 runtime fetch, which would grant a prefix of Secret Manager
# names through an IAM condition, is not adopted. That decision was taken
# while Check 52's U3 was unanswered and is unchanged now that it is answered
# — RUN 2026-10-16, the condition *is* honoured for
# secretmanager.versions.access — so the prefix form is expressible and
# nothing here depends on it either way.
#
# The set is local.job_secret_names and not the job's own mounts, so a model
# credential a profile the service serves gives up leaves this account's
# accessor with its mount (B5.7, C2.3).
resource "google_secret_manager_secret_iam_member" "job" {
  for_each = local.job_secret_names

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.job.member
}

# The credentials service, for a profile it serves (B5.2): the job's account
# may invoke it, and nothing else may — one member, the job's own account,
# roles/run.invoker, on that one service, exactly as a repository's own job is
# granted it (modules/workflow/sa.tf). What the service then serves that
# account is its own decision (a shared caller is admitted for the identity's
# repository when that repository is one of its profile's, B5.1/G21), so the
# grant is the front door and not the permission.
#
# Null for a profile the service does not serve, which is the whole of the
# switch on this side (infra.ProjectJobs' projectJobServesTheCredentialsService):
# the job is otherwise untouched, and everything it mounts stays until B5.7's
# flip (G17).
resource "google_cloud_run_v2_service_iam_member" "credentials" {
  count = local.credentials_grant == null ? 0 : 1

  project  = var.project
  location = local.credentials_grant.region
  name     = local.credentials_grant.name
  role     = "roles/run.invoker"
  member   = google_service_account.job.member
}

# The shared project job's release-1 Vertex grant (CS11), planned the same way
# the workflow module's is and for the same reason: var.job.vertex is the
# project reaching Vertex AND infra.VertexGrantForJob saying the project's
# budget mode allows a job the permission, so an enforcing project's jobs are
# planned no aiplatform permission at all — no run in flight can hold one there,
# because a `vertex` run is refused at bootstrap under `enforce` — while an
# `observe` project keeps it until release 2 (X8). A review variant of one of
# these jobs is planned none whatever this says (reviewVariant's own ruling),
# and the service's own call is its one-permission custom role's
# (modules/installation/credentials.tf, D3174).
resource "google_project_iam_member" "vertex" {
  count = var.job.vertex ? 1 : 0

  project = var.project
  role    = "roles/aiplatform.user"
  member  = google_service_account.job.member
}
