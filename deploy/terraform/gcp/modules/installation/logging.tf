# Log isolation: Fugaro jobs' logs (the workflow jobs and the check jobs,
# both labelled fugaro=managed) go to their own log bucket, and only people
# granted the bucket's view can read them there. In a shared project this
# can't take roles/logging.viewer away from anyone, so the same lines are
# also kept out of _Default. Owners and editors still read everything.
#
# What produces a line decides its resource.type -- cloud_run_job on a Cloud
# Run job, cloud_run_revision on a Cloud Run service, and
# batch.googleapis.com/Job on Cloud Batch (the VM backend) -- so the filter
# below has one arm per kind, and all three are pinned by the same label.
#
# Cloud Build logs stay in _Default: they hold the repository's build
# output, never a secret value.

locals {
  log_bucket_path = "projects/${var.project}/locations/global/buckets/${var.names.log.bucket}"

  # The Cloud Run arm: every Fugaro-managed Cloud Run job's lines (the
  # workflow jobs and the check jobs). It is byte-for-byte what it was before
  # the Batch arm below existed -- an installation's sink and exclusion
  # already filter on it, and an arm that drifted would move lines this
  # installation never meant to route. A test pins it.
  fugaro_cloud_run_logs = "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\""

  # The Batch arm (design docs/design/compute-backend.md §1.5, §8.1 task 4):
  # a Batch job's lines carry resource.type="batch.googleapis.com/Job", and
  # its Job.labels reach the entry it generates, so the installation's
  # fugaro=managed marker selects them the same way. ADDITIVE and inert for a
  # project with no Batch jobs: no Batch job means no entry with that
  # resource type, so this arm matches nothing and moves nothing. Without it,
  # a Batch run's lines would sit in _Default, where every project viewer
  # reads them, while `fugaro logs` (which reads the view below) found
  # nothing.
  fugaro_batch_logs = "resource.type=\"batch.googleapis.com/Job\" AND labels.\"fugaro\"=\"managed\""

  # The Cloud Run service arm: the credentials service (credentials.tf, D146)
  # is a Cloud Run *service*, and a service's lines carry
  # resource.type="cloud_run_revision", not cloud_run_job. Without this arm
  # the one identity that holds the project's model credentials logs into
  # _Default, where every project viewer reads it, instead of into the
  # installation's own bucket and view. ADDITIVE and inert for an installation
  # that has not opted in: no Fugaro-managed Cloud Run service means no entry
  # with this resource type and this label, so the arm matches nothing and
  # moves nothing. The label is the service's own (credentials.tf's labels
  # block), the marker the other two arms select on as well.
  #
  # That a revision's entries carry the service's labels is not something a
  # plan can check — the Cloud Run job arm rests on the same fact, which the
  # owner's log-isolation check confirmed live — so the owner's Check 94/95 is
  # where this arm is confirmed for a service.
  fugaro_cloud_run_service_logs = "resource.type=\"cloud_run_revision\" AND labels.\"fugaro\"=\"managed\""

  # Every arm, as one group, with each arm's own AND clauses parenthesized
  # inside it. Every parenthesis here is load-bearing, because Cloud
  # Logging's query language is NOT the usual one: NOT has the highest
  # precedence, "followed by OR and AND in that order" -- AND binds
  # *loosest*, where most languages bind it tightest (Logging query
  # language, "Boolean operators", checked 2026-10-15; the doc's own example
  # makes `"a" OR NOT "b" AND NOT "c" OR "d"` mean
  # `("a" OR (NOT "b")) AND ((NOT "c") OR "d")`). So an unparenthesized
  # `A1 AND A2 OR B1 AND B2` would be read as `A1 AND (A2 OR B1) AND B2`,
  # which matches only the first arm -- the others would route and exclude
  # nothing, leaving their lines in _Default where every project viewer reads
  # them, which is the outcome this whole file exists to prevent. Written out,
  # the filter means what it says whichever order the reader assumes. A test
  # pins it.
  fugaro_logs = "((${local.fugaro_cloud_run_logs}) OR (${local.fugaro_batch_logs}) OR (${local.fugaro_cloud_run_service_logs}))"
}

resource "google_logging_project_bucket_config" "fugaro" {
  count = var.log_isolation ? 1 : 0

  project        = var.project
  location       = "global"
  bucket_id      = var.names.log.bucket
  retention_days = 30
  # The ownership mark discovery checks before adopting the bucket.
  description = var.log_bucket_description

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
# system log. Neither carries agent output. The job alert's other log, the
# audit system events, is in _Required, which no exclusion applies to.
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
