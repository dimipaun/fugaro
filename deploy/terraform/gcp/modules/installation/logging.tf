# Log isolation: Fugaro jobs' logs (the workflow jobs and the check jobs,
# both labelled fugaro=managed) go to their own log bucket, and only people
# granted the bucket's view can read them there. In a shared project this
# can't take roles/logging.viewer away from anyone, so the same lines are
# also kept out of _Default. Owners and editors still read everything.
#
# Cloud Build logs stay in _Default: they hold the repository's build
# output, never a secret value.

locals {
  log_bucket_path = "projects/${var.project}/locations/global/buckets/${var.names.log.bucket}"
  fugaro_logs     = "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\""
}

resource "google_logging_project_bucket_config" "fugaro" {
  count = var.log_isolation ? 1 : 0

  project        = var.project
  location       = "global"
  bucket_id      = var.names.log.bucket
  retention_days = 30

  depends_on = [google_project_service.this]
}

# A sink to a log bucket in the same project is authorized automatically,
# so its writer identity needs no grant.
resource "google_logging_project_sink" "fugaro" {
  count = var.log_isolation ? 1 : 0

  project                = var.project
  name                   = var.names.log.sink
  destination            = "logging.googleapis.com/${local.log_bucket_path}"
  filter                 = local.fugaro_logs
  unique_writer_identity = true

  depends_on = [google_logging_project_bucket_config.fugaro]
}

# The exclusion applies to the _Default sink, which Terraform never manages;
# the dedicated sink above is unaffected by it. Log-based alerts don't
# operate on excluded logs, so the two kinds of line the alerts match stay
# in _Default as well: the check jobs' own decision lines and the Cloud Run
# system log. Neither carries agent output.
resource "google_logging_project_exclusion" "fugaro_from_default" {
  count = var.log_isolation ? 1 : 0

  project = var.project
  name    = var.names.log.exclusion
  filter  = "${local.fugaro_logs} AND NOT (resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"image-check\") AND NOT logName:\"run.googleapis.com%2Fvarlog%2Fsystem\""

  depends_on = [google_logging_project_sink.fugaro]
}

# The view has no filter: everything in the bucket is Fugaro's.
resource "google_logging_log_view" "runs" {
  count = var.log_isolation ? 1 : 0

  name   = var.names.log.view
  bucket = local.log_bucket_path

  depends_on = [google_logging_project_bucket_config.fugaro]
}

resource "google_logging_log_view_iam_member" "runs" {
  for_each = var.log_isolation ? toset(concat(var.launchers, var.operators)) : toset([])

  parent   = "projects/${var.project}"
  location = "global"
  bucket   = var.names.log.bucket
  name     = google_logging_log_view.runs[0].name
  role     = "roles/logging.viewAccessor"
  member   = each.value
}
