# Log isolation: Fugaro jobs' logs (the workflow jobs and the check jobs,
# both labelled fugaro=managed) go to their own log bucket, and only people
# granted the bucket's view can read them there. In a shared project this
# can't take roles/logging.viewer away from anyone, so the same lines are
# also kept out of _Default. Owners and editors still read everything.
#
# The bucket's location is var.log_bucket_location: the installation's region
# where Cloud Logging offers a bucket there, else the place's multi-region,
# else global (place.For's answer for a new installation, D3082). A bucket an
# installation already has keeps its own -- fugaro update's discovery reads it
# and the resource ignores the field -- so the sink's destination, the view
# and its grants are all built from this one variable (F10, R5).
#
# A job's platform decides its resource.type -- cloud_run_job on Cloud Run,
# batch.googleapis.com/Job on Cloud Batch (the VM backend) -- so the filter
# below has one arm per platform, and both are pinned by the same label.
#
# Cloud Build logs stay in _Default: they hold the repository's build
# output, never a secret value.

locals {
  log_bucket_path = "projects/${var.project}/locations/${var.log_bucket_location}/buckets/${var.names.log.bucket}"

  # The Cloud Run arm: every Fugaro-managed Cloud Run job's lines (the
  # workflow jobs and the check jobs). It is byte-for-byte what it was before
  # the Batch arm below existed -- an installation's sink and exclusion
  # already filter on it, and an arm that drifted would move lines this
  # installation never meant to route. A test pins it.
  fugaro_cloud_run_logs = "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\""

  # The Batch arm (design docs/compute-backend/spec.md §1.5, §8.1 task 4):
  # a Batch job's lines carry resource.type="batch.googleapis.com/Job", and
  # its Job.labels reach the entry it generates, so the installation's
  # fugaro=managed marker selects them the same way. ADDITIVE and inert for a
  # project with no Batch jobs: no Batch job means no entry with that
  # resource type, so this arm matches nothing and moves nothing. Without it,
  # a Batch run's lines would sit in _Default, where every project viewer
  # reads them, while `fugaro logs` (which reads the view below) found
  # nothing.
  fugaro_batch_logs = "resource.type=\"batch.googleapis.com/Job\" AND labels.\"fugaro\"=\"managed\""

  # Both arms, as one group, with each arm's own AND clauses parenthesized
  # inside it. Every parenthesis here is load-bearing, because Cloud
  # Logging's query language is NOT the usual one: NOT has the highest
  # precedence, "followed by OR and AND in that order" -- AND binds
  # *loosest*, where most languages bind it tightest (Logging query
  # language, "Boolean operators", checked 2026-10-15; the doc's own example
  # makes `"a" OR NOT "b" AND NOT "c" OR "d"` mean
  # `("a" OR (NOT "b")) AND ((NOT "c") OR "d")`). So an unparenthesized
  # `A1 AND A2 OR B1 AND B2` would be read as `A1 AND (A2 OR B1) AND B2`,
  # which matches only the Cloud Run arm -- the Batch arm would route and
  # exclude nothing, leaving a Batch run's task lines in _Default where every
  # project viewer reads them, which is the outcome this whole file exists to
  # prevent. Written out, the filter means what it says whichever order the
  # reader assumes. A test pins it.
  fugaro_logs = "((${local.fugaro_cloud_run_logs}) OR (${local.fugaro_batch_logs}))"
}

resource "google_logging_project_bucket_config" "fugaro" {
  count = var.log_isolation ? 1 : 0

  project        = var.project
  location       = var.log_bucket_location
  bucket_id      = var.names.log.bucket
  retention_days = 30
  # The ownership mark discovery checks before adopting the bucket.
  description = var.log_bucket_description

  # A bucket's location is permanent: Cloud Logging refuses to move one and a
  # replacement would delete the run output it holds. So the field is IGNORED
  # on a bucket that exists, exactly as a secret container's replication is
  # (LP5a): discovery reads where an installation's bucket already is, the
  # spec is given that location, and this is what keeps the plan of an
  # installation whose bucket is `global` a no-op even under a mapping that
  # now points elsewhere (R5, F11). An installation with no bucket gets the
  # one this variable names.
  lifecycle {
    ignore_changes = [location]
  }

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

# The viewers read the same view (D684): a run's own output is most of what a
# run is, and roles.md §4 gives the viewer the logs of the runs it may read.
# A viewer who is also a launcher or an operator is one member of this set, so
# no one holds the grant twice.
resource "google_logging_log_view_iam_member" "runs" {
  for_each = var.log_isolation ? toset(concat(var.launchers, var.operators, var.viewers)) : toset([])

  parent   = "projects/${var.project}"
  location = var.log_bucket_location
  bucket   = var.names.log.bucket
  name     = google_logging_log_view.runs[0].name
  role     = "roles/logging.viewAccessor"
  member   = each.value

  depends_on = [google_logging_project_bucket_config.fugaro]
}
