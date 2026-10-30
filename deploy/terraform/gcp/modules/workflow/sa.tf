# The job's service account and everything it is granted. These exist even
# when the job itself isn't deployed yet, so its secrets and image can be
# prepared first.

resource "google_service_account" "job" {
  project      = var.project
  account_id   = var.workflow.service_account.account_id
  display_name = var.workflow.service_account.display_name
}

# Deploying or updating the job acts as its account.
resource "google_service_account_iam_member" "operator" {
  for_each = toset(var.operators)

  service_account_id = google_service_account.job.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}

# The runs bucket, conditioned on the repository's runs/, cache/ and locks/
# prefixes. The condition is the input byte for byte: one that differs is a
# different binding, which would sit next to the bootstrap's. The bootstrap
# set no description, so none is set here.
resource "google_storage_bucket_iam_member" "job" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectUser"
  member = google_service_account.job.member

  condition {
    title      = var.workflow.bucket_condition.title
    expression = var.workflow.bucket_condition.expression
  }
}

# The repository's own budget token objects, tokens/<slug>/, read-only: the
# launcher writes the sealed token there and the run reads it, and a run can
# neither delete nor replace a queued run's token (objectViewer has no
# delete or create; docs/design/budget-token-channel.md section 7.2, D16).
# The runs/, cache/ and locks/ grant above never covers tokens/. The
# precondition re-checks the bucket and the prefix, beside the variable's own
# validation of the shape and the slug.
resource "google_storage_bucket_iam_member" "job_tokens" {
  bucket = var.runs_bucket
  role   = "roles/storage.objectViewer"
  member = google_service_account.job.member

  condition {
    title      = var.workflow.token_condition.title
    expression = var.workflow.token_condition.expression
  }

  lifecycle {
    precondition {
      condition     = startswith(var.workflow.token_condition.expression, "resource.name.startsWith(\"projects/_/buckets/${var.runs_bucket}/objects/tokens/") && !strcontains(var.workflow.token_condition.expression, "||")
      error_message = "workflow.token_condition must name tokens/ of this runs bucket, in one clause (gcp.TokenBucketCondition)."
    }
  }
}

locals {
  # D145: the credentials the credentials service holds once an installation
  # opts in, and which the job's own account therefore keeps no accessor on.
  # There are three families, and they are identified differently:
  #
  #   * the MODEL credentials, by the VARIABLE each one is mounted as. The
  #     three names are internal/config's (ReservedSecrets' Claude OAuth token
  #     and Anthropic key, and ProviderKeyEnvPrefix), which is also where
  #     config.ReservedEnvPrefixes refuses a workflow secret any variable that
  #     could impersonate one — so a workflow's secret_env entry names a model
  #     credential exactly when its variable is one of these. A provider's key
  #     has no fixed logical name (it is whatever the owner's local config
  #     calls it), so the variable is the only thing that identifies it.
  #   * the GITHUB APP'S PRIVATE KEY, by its LOGICAL NAME. It is a reserved
  #     name with exactly one mount, so the name identifies it — and matching
  #     the name is what leaves Bitbucket's token and every workflow's own
  #     secret alone. D145 leaves Bitbucket untouched: a Bitbucket credential
  #     is a repository access token, issued for one repository, which no
  #     service can mint per job (F7).
  #   * a PROXIED REPOSITORY'S GIT CREDENTIAL, by its LOGICAL NAME too, through
  #     local.is_proxied_git below. That one is held for ONE repository rather
  #     than for the installation, which is why it has a list of its own
  #     (proxied_git_secret_names) and a per-repository input beside it (G9,
  #     G15).
  #
  # TestServiceHeldSecretsMatchTheCredentialsService pins these four literals
  # and both expressions below to Go, and
  # TestProxiedGitSecretNamesMatchTheModule pins the proxied list, the local it
  # feeds and the third clause both expressions carry.
  model_secret_env_names  = ["ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]
  model_secret_env_prefix = "FUGARO_PROVIDER_KEY_"

  # The App key's name, and it is EMPTY until the release that also stops the
  # job reading the key (C2.2, review finding F1): config.ServiceHeldSecretNames
  # is the switch, this literal is held to it by the guard test, and Go's
  # internal/infra mirrors the same list. With the name in it and the runner
  # still reading FUGARO_GITHUB_APP_PRIVATE_KEY, this module would take the
  # job's accessor away from a container that still mounts the key — a job
  # Cloud Run refuses to create, so every GitHub run of an installation that
  # opted in would fail. Until then the job keeps the accessor and the mount,
  # and a GitHub run works exactly as it did before this file knew about the
  # service.
  service_held_secret_names = []

  # The Bitbucket token's own flip list (B1.8, D500): the logical names of the
  # git credentials the credentials service brokers for a repository the
  # published node admits with `proxy: true`. A list of its own, beside
  # service_held_secret_names, because its condition is the repository's rather
  # than the installation's (G15): a name here drops the accessor and the mount
  # of a PROXIED repository's job alone, and every other Bitbucket repository —
  # of an installation that opted in or not — keeps both (G9).
  #
  # It is EMPTY until the flip, which is B1.13 part two, after the owner's go
  # on Check 191: config.ProxiedGitSecretNames is the switch, this literal is
  # held to it by deploy/terraform's TestProxiedGitSecretNamesMatchTheModule,
  # and the flip fills both in one commit. Filling it early would take the
  # accessor off a job whose runner still reads the token from its own mount
  # (B1.7 is what moves that read to the service), and a mount the account
  # cannot resolve is a job Cloud Run refuses to create — every run of a
  # proxied repository would fail. Until then nothing moves, whatever the node
  # admits.
  proxied_git_secret_names = []

  # One entry per mount: this mount is a proxied git credential exactly when
  # the node admits the repository AND its logical name is one the list names.
  # Both halves are needed — the list alone would take the token from a
  # repository nobody proxied, the input alone from every Bitbucket repository
  # of an installation that opted in — and both clauses below read this one
  # local, so the two cannot drift apart.
  is_proxied_git = {
    for env, logical in var.workflow.secret_env :
    env => var.bitbucket_proxied && contains(local.proxied_git_secret_names, logical)
  }

  # One entry per mount, so the drop below is per mount and never per name.
  is_service_held = {
    for env, logical in var.workflow.secret_env :
    env => contains(local.model_secret_env_names, env) || startswith(env, local.model_secret_env_prefix) || contains(local.service_held_secret_names, logical) || local.is_proxied_git[env]
  }

  # The secrets the job's own account may read, by logical name: every secret
  # the job mounts while the credentials service is off — D147's "with it off
  # nothing changes", and the same set (and the same instance keys) as before
  # this file knew about the service — and, once it is on, every mounted
  # secret that is not one the service holds (C1.11's bundle holds the model
  # credentials, C2.1 mints installation tokens from the App key, and
  # C1.20/C2.2 drop the job's mount of each). One member per mounted secret,
  # never the project's, never another workflow's.
  job_secret_names = toset([
    for env, logical in var.workflow.secret_env :
    logical if var.credentials_service == null || !local.is_service_held[env]
  ])

  # The secrets the job's CONTAINER still declares, by the variable each is
  # mounted as: every mount while the credentials service is off — byte for
  # byte the map the module declared before it knew about the service (D147) —
  # and, once it is on, every mount except the GitHub App's private key. A
  # mount the job's account has no accessor on is not a weaker mount, it is a
  # job Cloud Run refuses to create, so the mount has to go with the accessor
  # (C2.3, review finding F1). Go's internal/infra drops the same mount from
  # the spec this module's tfvars are generated from, which is the drop that
  # fires in practice; this local is the rule written down where the accessor
  # is decided, so a tfvars that still names the key cannot be applied into a
  # job with no way to read it.
  #
  # Only the App key goes here, and not the model credentials the accessor
  # drop above covers as well, because the two are removed by different tasks
  # at different times: C1.18 drops the job's accessor on the model
  # credentials and C1.20 the mount, together with the runner's switch to
  # reading them from the service (C1.15) — a mount this module removed for a
  # runner that cannot read the service yet would break every run. The App
  # key needs no such pairing: no run reads it at all once C2.1/C2.2 make the
  # service mint the installation tokens, so C2.3 drops accessor and mount
  # together. service_held_secret_names is the App key alone, which is
  # exactly the difference; is_service_held would take the model mounts too.
  #
  # Like service_held_secret_names, this is inert while the list is empty
  # (review finding F1): with the opt-in on and the list filled, the App key's
  # mount goes in the same plan as its accessor.
  #
  # Both lists are read here, and the proxied one only for the repository the
  # node admitted: a proxied repository's git credential is mounted no more
  # than the App key is, which is the half of the drop the runner's own read
  # cannot replace — a mount the account cannot resolve is a job Cloud Run
  # refuses to create (B1.8, G9).
  #
  # The check job is not this module: it runs as the build account, which
  # keeps its accessor on the App key and clones with it (D148, F3).
  job_mounts = {
    for env, logical in var.workflow.secret_env :
    env => logical if var.credentials_service == null || !contains(local.service_held_secret_names, logical) && !local.is_proxied_git[env]
  }
}

# Each secret the job mounts and may read, at the secret level, never the
# project's.
resource "google_secret_manager_secret_iam_member" "job" {
  for_each = local.job_secret_names

  project   = var.project
  secret_id = var.secrets[each.value]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.job.member
}

# The credentials service, once the installation has one (D135): the job's
# account may invoke it, and nothing else may — one member, the job's own
# account, roles/run.invoker, on that one service. It is the runner's only way
# to reach a model credential, and an unreachable service halts the run rather
# than falling back (D143).
resource "google_cloud_run_v2_service_iam_member" "credentials" {
  count = var.credentials_service == null ? 0 : 1

  project  = var.project
  location = var.credentials_service.region
  name     = var.credentials_service.name
  role     = "roles/run.invoker"
  member   = google_service_account.job.member
}

# Only a workflow whose agent authenticates through Vertex AI.
resource "google_project_iam_member" "vertex" {
  count = var.workflow.vertex ? 1 : 0

  project = var.project
  role    = "roles/aiplatform.user"
  member  = google_service_account.job.member
}
