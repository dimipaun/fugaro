# The workflow's Cloud Run job. fugaro init --repo leaves deploy_job off
# until the job's image exists and every secret it mounts has a version
# (or the job already exists), so a new job is never pointed at nothing.
resource "google_cloud_run_v2_job" "this" {
  count = var.workflow.deploy_job ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.workflow.job
  labels = {
    fugaro          = "managed"
    fugaro_repo     = var.repo_label
    fugaro_workflow = var.name
  }
  deletion_protection = !var.allow_job_delete

  template {
    task_count = 1

    template {
      # The provider's default is 3; a run is never retried behind the
      # launcher's back.
      max_retries           = 0
      timeout               = "${var.workflow.task_timeout_s}s"
      service_account       = google_service_account.job.email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image = var.workflow.image

        resources {
          limits = {
            cpu    = var.workflow.cpu
            memory = var.workflow.memory
          }
        }

        # The plain variables, then the secrets. env is a set, which the
        # provider keeps sorted by name, so the order here plans no diff.
        dynamic "env" {
          for_each = var.workflow.env
          content {
            name  = env.key
            value = env.value
          }
        }

        # The secret's ID, the same-project short form the live jobs hold,
        # never its projects/… path, which would plan an update.
        dynamic "env" {
          for_each = var.workflow.secret_env
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = var.secrets[env.value]
                version = "latest"
              }
            }
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]
  }

  # The job runs as its account and reads its secrets from the start.
  depends_on = [google_secret_manager_secret_iam_member.job]
}

# Launchers start runs with overrides, on this job only.
resource "google_cloud_run_v2_job_iam_member" "launcher" {
  for_each = var.workflow.deploy_job ? toset(var.launchers) : toset([])

  project  = var.project
  location = google_cloud_run_v2_job.this[0].location
  name     = google_cloud_run_v2_job.this[0].name
  role     = var.job_runner_role
  member   = each.value
}
