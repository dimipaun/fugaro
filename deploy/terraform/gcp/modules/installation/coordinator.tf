# The coordinator's aggregation job (coordinator plan P5, D194): a scheduled
# Cloud Run job that re-reads GitHub through the project-wide read (P2),
# recomputes the repository views (P1) and writes them into the coordinator's
# subtree (P3), recording what it could not reconcile instead of half a view.
# It is the deterministic half of the coordinator, and the account below is
# the only identity that writes the coordinator's state.
#
# None of this exists unless the owner opts in (fugaro update
# --firebase-project <id> --coordinator); var.coordinator is null otherwise.
#
# The account is created in the first of fugaro update's applies, so the
# Firebase root can grant it roles/firebasedatabase.admin on the FP — that
# root grants nothing to an account that does not exist yet (P3). The job, the
# App key's grant and the schedule arrive in the third, once the FP's outputs
# exist (coordinator.deploy_job), the way the history job's do.
#
# What this account may write is not IAM's to say: firebasedatabase.admin is a
# project-wide role (IAM cannot express a path, D194), so the fence is
# internal/coordinator's own subtree check (Store's ErrOutsideSubtree) and the
# rules' floor. Everything else here is the narrowest grant the job needs:
# objectViewer on the runs bucket, secretAccessor on the App key's one
# container, and the job_runner role on this job alone.

locals {
  # coordinator is the owner's opt-in, and it is the whole switch: fugaro
  # writes the block only after fugaro update --firebase-project --coordinator
  # (recorded as terraform.coordinator in the local config), so an
  # installation that never asked — every installation by default — plans
  # exactly what it planned before, and the Firebase root's own gate is the
  # same fact (its coordinator_account is empty). The history job hangs off
  # enable_budget alone because every budget backend has one; the coordinator
  # needs both, and it is internal/cli's initFirebase that refuses the one
  # combination this cannot see (an opt-in with no account in the
  # installation's outputs).
  coordinator_on     = var.enable_budget && var.coordinator != null
  deploy_coordinator = local.coordinator_on && try(var.coordinator.deploy_job, false)
}

resource "google_service_account" "coordinator" {
  count = local.coordinator_on ? 1 : 0

  project      = var.project
  account_id   = var.coordinator.account_id
  display_name = "Fugaro coordinator"

  depends_on = [google_project_service.this]
}

# Nobody is granted actAs on this account, operators included: it holds
# firebasedatabase.admin on the FP, so anyone who could deploy a job as it
# could rewrite every view the coordinator holds (design D6, the history
# account's comment). Executing the existing job needs no actAs; only the
# applier that creates or updates the job acts as it.

# The runs bucket, read-only: a sweep reads every run a repository holds, which
# is what the view is computed from. objectViewer on this bucket and never
# objectAdmin, with no condition (unverified assumption A3, the history
# account's grant): the coordinator writes no object. Granted whenever the
# account exists, like the account itself.
resource "google_storage_bucket_iam_member" "coordinator_runs_reader" {
  count = local.coordinator_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.coordinator[0].member
}

# The App key, and the job is its only reader: the sweep mints the
# coordinator's own read-only installation token from it (P2), so this grant
# arrives with the job and not with the account. It is granted at the secret
# level and to this one account — never project-wide, never to a group — and
# the container's own env entry is what names the container, one grant and one
# mount, as the project module's jobs have it.
resource "google_secret_manager_secret_iam_member" "coordinator_app_key" {
  count = local.deploy_coordinator ? 1 : 0

  project   = var.project
  secret_id = var.project_secrets[var.coordinator.app_key_secret]
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.coordinator[0].member

  lifecycle {
    precondition {
      condition     = contains(keys(var.project_secrets), var.coordinator.app_key_secret)
      error_message = "coordinator.app_key_secret names a project-wide secret container this installation does not create: project_secrets comes from the local config's own provider secrets (fugaro update's discovery unions the live ones in), so an installation whose config records no GitHub provider has no container to grant."
    }
  }
}

resource "google_cloud_run_v2_job" "coordinator" {
  count = local.deploy_coordinator ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.coordinator.job
  labels = {
    fugaro      = "managed"
    fugaro_role = "coordinator"
  }
  deletion_protection = true

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "coordinator"
    }
    task_count = 1

    template {
      max_retries           = 0
      timeout               = "600s"
      service_account       = google_service_account.coordinator[0].email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.coordinator.image
        command = ["/usr/local/bin/fugaro"]
        args    = ["coordinator", "sweep"]

        resources {
          limits = {
            cpu    = "1"
            memory = "512Mi"
          }
        }

        # The job's whole configuration is the environment
        # internal/cli/coordinator.go's sweep reads: the project, the Firebase
        # project and database it writes, the runs bucket it reads, the
        # repositories one tick reconciles, and the App credential.
        env {
          name  = "FUGARO_PROJECT"
          value = var.fugaro_project
        }
        env {
          name  = "FUGARO_FIREBASE_PROJECT"
          value = var.coordinator.firebase_project
        }
        env {
          name  = "FUGARO_RTDB_URL"
          value = var.coordinator.rtdb_url
        }
        env {
          name  = "FUGARO_RUNS_BUCKET"
          value = var.runs_bucket
        }
        # The repositories one tick reconciles, as the JSON list the command
        # decodes ([{"provider":"github","repo":"owner/name"}, ...]): the
        # installation's own, because the job has no checkout to read a
        # provider out of, and a repository the config does not name a
        # provider for is not in it.
        env {
          name  = "FUGARO_COORDINATOR_REPOS"
          value = jsonencode(var.coordinator.repos)
        }
        env {
          name  = "FUGARO_GITHUB_APP_ID"
          value = var.coordinator.github_app_id
        }
        # The one credential, and the only place it ever appears: a
        # secret_key_ref, so no plan, no `terraform show` and no job describe
        # carries the App's private key.
        env {
          name = "FUGARO_GITHUB_APP_PRIVATE_KEY"
          value_source {
            secret_key_ref {
              secret  = var.project_secrets[var.coordinator.app_key_secret]
              version = "latest"
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  depends_on = [google_secret_manager_secret_iam_member.coordinator_app_key]
}

# fugaroJobRunner (run.jobs.run) on this job only, for the scheduler account
# below: the same role and the same job-scoped grant the history job's sweep
# has, and never roles/run.invoker, which the history root's comment explains
# (a body with overrides needs runWithOverrides, and this tree grants one role
# for both).
resource "google_cloud_run_v2_job_iam_member" "coordinator_runner" {
  count = local.deploy_coordinator ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_job.coordinator[0].location
  name     = google_cloud_run_v2_job.coordinator[0].name
  role     = google_project_iam_custom_role.job_runner.name
  member   = google_service_account.scheduler.member
}

# The sweep, every 15 minutes: the coordinator is early warning (spec §6), and
# a tick is cheap — one read of each repository's runs and of the pull requests
# they name. Scheduler isn't offered in every Cloud Run region, so it runs in
# the scheduler region and the URI names the job's own.
#
# A failed sweep logs its failure and the next tick is the retry (retry_count
# 0, the history sweep's rule): the job's own timeout is 600s and the code
# stops a tick at 8 minutes (internal/cli's coordinatorSweepTimeout), so a
# sweep that ran out of time ends cleanly and the next one continues rather
# than two passes of one repository running at once.
resource "google_cloud_scheduler_job" "coordinator_sweep" {
  count = local.deploy_coordinator ? 1 : 0

  project   = var.project
  region    = var.coordinator.scheduler_region
  name      = var.coordinator.scheduler_job
  schedule  = "*/15 * * * *"
  time_zone = "Etc/UTC"

  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.coordinator[0].location}/jobs/${google_cloud_run_v2_job.coordinator[0].name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.coordinator_runner]
}
