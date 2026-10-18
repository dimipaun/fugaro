# Launchers start runs with overrides, on this job only: the same role and
# the same run.jobs.runWithOverrides permission the per-repository path
# grants, per project job instead of per (repository, workflow). It widens
# nothing — every launcher already holds every job — and the repository
# root's own grant stays until the per-repository jobs are dropped (task
# 4.4), because a repository's workflows still run on them.
resource "google_cloud_run_v2_job_iam_member" "launcher" {
  for_each = toset(var.launchers)

  project  = var.project
  location = google_cloud_run_v2_job.this.location
  name     = google_cloud_run_v2_job.this.name
  role     = var.job_runner_role
  member   = each.value
}
