# The installation's licence renewal job (plan K4.2 of docs/licence-key-v2):
# ONE scheduled Cloud Run job that runs `fugaro licence renew --job`, whose code
# is the Subscription Edition's (ee/register/licence_renew.go, K4.1), and the
# account, the empty container and the schedule it needs. Its name starts with
# fugarolicence and not fugaro-, for the reason fugarorpt- exists: `fugaro runs
# ls` and max_parallel count the fugaro- jobs as workflow jobs, and the alert
# policies match the prefix.
#
# UNDER THE COORDINATOR'S CONDITION, and that is the whole switch. The job's
# image is the coordinator's (one CLI binary serves the sweep, the front door
# and this job — internal/infra.Licence's spec takes the history image), so an
# installation that has not opted into the coordinator has no tagged image to
# run and no job, and `fugaro licence status` says renewal is by hand there
# (TN5). It reads local.deploy_coordinator, the same fact coordinator.tf
# computes, so a switch of its own cannot drift from it.
#
# WHERE THE CONDITION HOLDS THE JOB IS ALWAYS APPLIED, and that is D524 rather
# than an omission: the core never reads a key, so `fugaro update` cannot know
# whether this installation bought a licence, and it must not ask. What it
# creates is a container with no version in it, an account with the grants below
# and a job that no-ops and says so when the container is empty or the key is
# offline (licence_renew.go's third difference). K3.2's `fugaro licence set` is
# what puts the first value in, and it refuses before this container exists with
# one line naming `fugaro update`.
#
# Nothing here costs anything while it is idle, mounts no secret and carries no
# git, model or API credential: the account's whole reach is the three-object
# read, the one-object write and the four Secret Manager calls below.

locals {
  # The switch is coordinator.tf's own local, read and not recomputed.
  licence_on = local.deploy_coordinator && var.licence != null
}

# The account the job runs as and every grant below is written for. It holds no
# project role: the two conditioned bucket grants, the four calls on the one
# container and roles/run.invoker on nothing (Scheduler invokes as its own
# account, not this one) are its whole reach.
resource "google_service_account" "licence" {
  count = local.licence_on ? 1 : 0

  project      = var.project
  account_id   = var.licence.account_id
  display_name = "Fugaro licence renewal"

  depends_on = [google_project_service.this]
}

# The job's Secret Manager role, and a CUSTOM one rather than a predefined set,
# because no predefined set is exactly these four calls:
# roles/secretmanager.secretVersionManager carries versions.destroy,
# versions.enable and versions.disable beside add and access (the job would be
# able to destroy the very key it renews, and a destructive permission in a
# role is a permission a plan has to justify), and the pair
# roles/secretmanager.secretAccessor + roles/secretmanager.secretVersionAdder
# is two bindings that still do not carry secrets.get or versions.list. The
# custom role names the four and nothing else.
#
# The four are the whole of what `fugaro licence renew --job` does with the
# container (K4.1): get its metadata to see whether it exists and what its
# labels say, list its versions to find the newest, access that version's
# payload — the per-key renewal secret — and add the next one after a
# successful renewal. It never destroys, enables or disables a version, and it
# never touches another container of the project: what fences it to this one is
# the binding below, which is at the SECRET and not the project.
resource "google_project_iam_custom_role" "licence_renewal" {
  count = local.licence_on ? 1 : 0

  project = var.project
  role_id = "fugaroLicenceRenewal"
  title   = "Fugaro licence renewal (one container, four calls)"
  permissions = [
    "secretmanager.secrets.get",     # whether the container exists and what its labels say
    "secretmanager.versions.list",   # which version is the newest one
    "secretmanager.versions.access", # the payload: the per-key renewal secret
    "secretmanager.versions.add",    # the next secret, after a renewal that succeeded
  ]
}

# The container the renewal secret lives in, and it is created EMPTY: nothing in
# this file, and nothing in the core, ever writes a value into it (D524).
# `fugaro licence set` is the one writer (K3.2), running in a build that holds
# the key, and its ID is derived from the Fugaro project's name by
# internal/infra.LicenceRenewalSecretID — a container ID with a hash in it
# cannot be computed here, so the spec carries it and this file writes what it
# is given.
#
# The lifecycle is secrets.tf's, for a licence's reason rather than a project
# secret's: deleting the container destroys every version, and the renewal
# secret in it is what a licensed installation renews with. A plan that turns
# the coordinator off after licensing therefore fails here rather than losing
# the key, which is the state the owner has to decide about rather than one an
# apply takes for them (and D1551's rule refuses such a delete anyway).
resource "google_secret_manager_secret" "licence_renewal" {
  count = local.licence_on ? 1 : 0

  project   = var.project
  secret_id = var.licence.renewal_secret
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
  }

  # A container the CLI created is replicated in the installation's region, and
  # replication is creation-time only: a differing value here would plan a
  # replacement, which prevent_destroy refuses and which deletes every version
  # first. So the field is ignored below, and this block decides only a
  # container Terraform itself creates.
  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [replication]
  }
}

# ... and the job's binding of it, at the SECRET and not the project: a project
# binding of this role would hand the renewal job its four calls on EVERY
# container of the installation, and the container is the narrowest scope IAM
# offers for Secret Manager. The role is spelled LITERALLY and not read from the
# role resource's own name attribute: a custom role's name is computed during
# apply, so the reference reaches the plan as an UNKNOWN role and
# internal/infra/tf's cover cannot then say which role the grant is
# (intake.tf's rule for the same reason, and deployer.tf's before it). The cover
# reads the target too, and this is the one secret grant it does that for
# (licenceSecretGrant): four calls are a read of a VALUE, where its other rows
# are reads of a key the job already holds.
#
# The container is named by the same kind of literal — the ID the run's own
# licence spec derived — and a literal reference is no edge in the graph:
# nothing in this block tells Terraform the binding has to wait for the
# container, so an apply that creates both could call setIamPolicy on a
# container that does not exist yet and fail the update with a 404. The edge is
# written out instead, beside the role's: these two are the resources whose
# names this binding cannot reference, and both have to exist first.
resource "google_secret_manager_secret_iam_member" "licence_renewal" {
  count = local.licence_on ? 1 : 0

  project   = var.project
  secret_id = var.licence.renewal_secret
  role      = "projects/${var.project}/roles/fugaroLicenceRenewal"
  member    = google_service_account.licence[0].member

  depends_on = [
    google_project_iam_custom_role.licence_renewal,
    google_secret_manager_secret.licence_renewal,
  ]
}

# The job's read: THREE objects of the runs bucket, by name, joined by " || " in
# the input's own bytes (gcp.LicenceReadCondition) — the project marker, the
# published config and the licence.
#
# It is three and not one because the job runs in a container: it has no
# checkout and no config file, so the marker and the published config are how it
# learns WHICH installation it renews for, the same two reads every unconfigured
# machine makes (internal/cli's selectWith). A grant naming the licence object
# alone would be a job that fails before it reaches the object it exists for.
#
# What it does not carry is any listing: a List request names no object, so no
# clause of a name condition matches one, and the account therefore stays out of
# the run records, the prompts and the transcripts the bucket holds. The
# widened bucket-wide viewer a listing would need is NOT planned: it would read
# tokens/ and cache/ as well.
resource "google_storage_bucket_iam_member" "licence_read" {
  count = local.licence_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectViewer"
  member = google_service_account.licence[0].member

  condition {
    title      = var.licence.read_condition.title
    expression = var.licence.read_condition.expression
  }
}

# The licence object and nothing else, read-write: the one object the job exists
# to renew, replaced in place by a write it computed from the read.
#
# objectUser rather than objectCreator: the bucket is versioned and `fugaro/` is
# deliberately excluded from the noncurrent-version purge (bucket.tf), so a
# renewal archives the previous object rather than destroying it, and a create-
# only role would fail on the second renewal of a key that is already there.
# objectUser is also what every other prefix's writer holds here, and the
# condition is the input byte for byte (gcp.LicenceWriteCondition): the same
# bytes on another object — a run record, a prompt, a token — are a grant no
# module writes, and internal/infra/tf refuses them.
resource "google_storage_bucket_iam_member" "licence_write" {
  count = local.licence_on ? 1 : 0

  bucket = google_storage_bucket.runs.name
  role   = "roles/storage.objectUser"
  member = google_service_account.licence[0].member

  condition {
    title      = var.licence.write_condition.title
    expression = var.licence.write_condition.expression
  }
}

# The job itself. Its argv is the mode's own (`fugaro licence renew --job`, the
# command ee/register registers), it mounts no secret — the renewal secret is
# read through the account's IAM grant, not through a mounted file — and its
# environment carries the platform variables every job has (var.licence.env,
# written by internal/infra), FUGARO_PROJECT and FUGARO_GCP_PROJECT among them:
# the container has no checkout and no config file, so those two are how it
# finds the installation whose published config it reads (internal/cli's
# selectWith), and the preconditions below check them against this root's own.
#
# max_retries = 0, the history sweep's rule: a renewal is not idempotent in the
# sense a retry wants — it talks to the licence server and adds a version — and
# a failed execution logs its failure where the next night's tick is the retry.
resource "google_cloud_run_v2_job" "licence" {
  count = local.licence_on ? 1 : 0

  project  = var.project
  location = var.region
  name     = var.licence.job
  labels = {
    fugaro      = "managed"
    fugaro_role = "licence"
  }
  deletion_protection = true

  template {
    labels = {
      fugaro      = "managed"
      fugaro_role = "licence"
    }
    task_count = 1

    template {
      max_retries = 0
      # One key's renewal is a handful of API calls: the coordinator sweep's
      # 600s is generous rather than tight, and it is deliberately not the
      # reports job's 1800s, which covers a whole project's records.
      timeout               = "600s"
      service_account       = google_service_account.licence[0].email
      execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

      containers {
        image   = var.licence.image
        command = ["/usr/local/bin/fugaro"]
        args    = ["licence", "renew", "--job"]

        resources {
          limits = {
            cpu    = "1"
            memory = "512Mi"
          }
        }

        dynamic "env" {
          for_each = var.licence.env
          content {
            name  = env.key
            value = env.value
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [client, client_version]

    # Both sides are generated by the same Go code, so this checks for drift (a
    # hand-edited tfvars), not for an attacker. A precondition fails the plan; a
    # check block would only warn.
    precondition {
      condition     = lookup(var.licence.env, "FUGARO_PROJECT", "") == var.fugaro_project
      error_message = "the licence job's FUGARO_PROJECT must be the Fugaro project ${var.fugaro_project}; run fugaro update"
    }
    precondition {
      condition     = lookup(var.licence.env, "FUGARO_GCP_PROJECT", "") == var.project
      error_message = "the licence job's FUGARO_GCP_PROJECT must be the GCP project ${var.project}; run fugaro update"
    }
  }
}

# roles/run.invoker holds run.jobs.run, which Scheduler's call needs, on this
# job only, held by the installation's scheduler account — the same grant and
# the same reason as the reports job's and the repository check job's.
resource "google_cloud_run_v2_job_iam_member" "licence_invoker" {
  count = local.licence_on ? 1 : 0

  project  = var.project
  location = google_cloud_run_v2_job.licence[0].location
  name     = google_cloud_run_v2_job.licence[0].name
  role     = "roles/run.invoker"
  member   = google_service_account.scheduler.member
}

# Scheduler isn't offered in every Cloud Run region, so it runs in the scheduler
# region and the URI names the job's own.
#
# A fixed DAILY tick, and the job decides from the key whether this tick has
# anything to do: `check_interval` is the key's own field and P1D is the finest
# value it takes (ee/licence/key.go reads two ISO 8601 forms), so one tick a day
# is what a key's terms can ask for and a finer schedule would only ask the
# licence server the same question more often.
#
# It ships RUNNING, unlike the reports job: the job exists only on an
# installation whose coordinator is deployed, it costs nothing while the key is
# absent (the command no-ops and exits 0) and a paused schedule would be a
# licence that quietly stops renewing. A renewal that fails is on the job's own
# alert policy below.
resource "google_cloud_scheduler_job" "licence" {
  count = local.licence_on ? 1 : 0

  project   = var.project
  region    = var.licence.scheduler_region
  name      = var.licence.scheduler_job
  schedule  = "23 4 * * *"
  time_zone = "Etc/UTC"

  # A failed run logs its failure; the next day's is the retry.
  retry_config {
    retry_count = 0
  }

  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project}/locations/${google_cloud_run_v2_job.licence[0].location}/jobs/${google_cloud_run_v2_job.licence[0].name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  depends_on = [google_cloud_run_v2_job_iam_member.licence_invoker]
}

# The job's own alert policy, the reports job's shape: more than one day without
# a successful renewal is a licence that will lapse at its `expires_at`, and a
# renewal that logged its failure and exited cleanly is silent everywhere else on
# an installation with log isolation on (the reports job's comment explains what
# the filter can and cannot see: it is Cloud Run's own record of a dead
# execution, which the job's exit code does produce). A project that named no
# alert address gets no policy.
resource "google_monitoring_alert_policy" "licence" {
  count = local.licence_on && var.alert_email != null ? 1 : 0

  project      = var.project
  display_name = "Fugaro licence renewal failed"
  combiner     = "OR"

  conditions {
    display_name = "A licence renewal run failed"
    condition_matched_log {
      filter = "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarolicence\" AND severity>=ERROR"
    }
  }

  notification_channels = [google_monitoring_notification_channel.email[0].id]

  # Log-match conditions require a notification rate limit.
  alert_strategy {
    notification_rate_limit {
      period = "3600s"
    }
    auto_close = "604800s"
  }
}
