# Plans the installation against a mock provider: no credentials, no network
# beyond the provider download. The names are the ones fugaro init writes
# into terraform.tfvars.json.
#
# An assertion can't reach into a child module's resources, so the first run
# plans the root (checking its wiring through the outputs) and the others
# plan the module itself.

mock_provider "google" {}

variables {
  project      = "proj-1234"
  region       = "us-east5"
  runs_bucket  = "fugaro-runs-proj-1234"
  state_bucket = "fugaro-tfstate-proj-1234"
  names = {
    legacy_registry              = "fugaro"
    base_registry                = "fugaro-base"
    scheduler_service_account_id = "fugaro-scheduler"
    role_ids = {
      launcher        = "fugaroLauncher"
      job_runner      = "fugaroJobRunner"
      build_submitter = "fugaroBuildSubmitter"
    }
    log = {
      bucket    = "fugaro"
      view      = "fugaro-runs"
      sink      = "fugaro-jobs"
      exclusion = "fugaro-jobs-from-default"
    }
  }
  log_bucket_description = "Fugaro job logs (managed by fugaro)"
  launchers              = ["user:launcher@example.com"]
  operators              = ["user:operator@example.com"]
}

run "root_passes_inputs" {
  command = plan

  variables {
    adopt_legacy_registry = true
    registry_cleanup      = { dry_run = false }
  }

  assert {
    condition     = output.runs_bucket == "fugaro-runs-proj-1234"
    error_message = "the root must pass runs_bucket to the module"
  }
  assert {
    condition     = output.base_registry == "fugaro-base" && output.legacy_registry == "fugaro"
    error_message = "the root must pass the registry names and adopt_legacy_registry to the module"
  }
  assert {
    condition     = output.registry_host == "us-east5-docker.pkg.dev/proj-1234"
    error_message = "the root must pass project and region to the module"
  }
  assert {
    condition     = output.launchers == tolist(["user:launcher@example.com"]) && output.operators == tolist(["user:operator@example.com"])
    error_message = "the root must pass launchers and operators to the module"
  }
  assert {
    condition     = output.registry_cleanup_dry_run == false
    error_message = "the root must pass registry_cleanup to the module"
  }
  assert {
    condition     = output.log_view != null
    error_message = "the root must pass log_isolation (default on) to the module and output the view"
  }
}

run "bucket_is_marked" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = google_storage_bucket.runs.labels == tomap({ fugaro = "managed" })
    error_message = "the runs bucket must carry exactly fugaro=managed"
  }
  assert {
    condition     = google_storage_bucket.runs.uniform_bucket_level_access == true
    error_message = "the runs bucket must use uniform bucket-level access"
  }
  assert {
    condition     = google_storage_bucket.runs.public_access_prevention == "enforced"
    error_message = "the runs bucket must enforce public access prevention"
  }
  assert {
    condition     = google_storage_bucket.runs.location == "US-EAST5"
    error_message = "the runs bucket's location must be the region, as the API spells it"
  }
  assert {
    condition     = google_storage_bucket.runs.force_destroy == false
    error_message = "the runs bucket must never be force-destroyed"
  }
  assert {
    condition     = length(google_storage_bucket.runs.lifecycle_rule) == 3
    error_message = "the runs bucket must have exactly the bootstrap's three lifecycle rules"
  }
  assert {
    condition = anytrue([for r in google_storage_bucket.runs.lifecycle_rule :
    one(r.action).type == "Delete" && one(r.condition).age == 90 && one(r.condition).matches_prefix == tolist(["runs/"])])
    error_message = "runs/ must be deleted after 90 days"
  }
  assert {
    condition = anytrue([for r in google_storage_bucket.runs.lifecycle_rule :
    one(r.action).type == "Delete" && one(r.condition).days_since_custom_time == 30 && one(r.condition).matches_prefix == tolist(["cache/"])])
    error_message = "cache/ must be deleted 30 days after its custom time"
  }
  assert {
    condition = anytrue([for r in google_storage_bucket.runs.lifecycle_rule :
    one(r.action).type == "Delete" && one(r.condition).age == 180 && one(r.condition).matches_prefix == tolist(["cache/"])])
    error_message = "cache/ must be deleted 180 days after creation"
  }
  assert {
    condition = alltrue([for r in google_storage_bucket.runs.lifecycle_rule :
    !contains(one(r.condition).matches_prefix, "builds/")])
    error_message = "builds/ holds the build records and must have no lifecycle rule"
  }
  assert {
    condition     = output.runs_bucket == "fugaro-runs-proj-1234"
    error_message = "the runs_bucket output must be the bucket's name"
  }
}

run "registries_are_marked" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    adopt_legacy_registry = true
  }

  assert {
    condition     = google_artifact_registry_repository.base.labels == tomap({ fugaro = "managed" })
    error_message = "fugaro-base must carry fugaro=managed"
  }
  assert {
    condition     = google_artifact_registry_repository.legacy[0].labels == tomap({ fugaro = "managed" })
    error_message = "the legacy registry must carry fugaro=managed"
  }
  assert {
    condition     = google_artifact_registry_repository.base.repository_id == "fugaro-base" && google_artifact_registry_repository.legacy[0].repository_id == "fugaro"
    error_message = "the registry IDs must be the inputs"
  }
  assert {
    condition     = google_artifact_registry_repository.base.format == "DOCKER" && google_artifact_registry_repository.legacy[0].format == "DOCKER"
    error_message = "both registries are Docker registries"
  }
  assert {
    condition     = length(google_artifact_registry_repository.legacy[0].cleanup_policies) == 0
    error_message = "the legacy registry holds the rollback's images and must have no cleanup policy"
  }
  assert {
    condition     = output.legacy_registry == "fugaro" && output.base_registry == "fugaro-base"
    error_message = "the registry outputs must be the IDs"
  }
  assert {
    condition     = output.registry_host == "us-east5-docker.pkg.dev/proj-1234"
    error_message = "registry_host must be <region>-docker.pkg.dev/<project>"
  }
}

run "legacy_only_when_adopting" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = length(google_artifact_registry_repository.legacy) == 0
    error_message = "a fresh installation must not get an empty legacy registry"
  }
  assert {
    condition     = output.legacy_registry == null
    error_message = "legacy_registry must be null when nothing was adopted"
  }
}

run "cleanup_keeps_latest" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition = anytrue([for p in google_artifact_registry_repository.base.cleanup_policies :
    p.action == "KEEP" && length(p.condition) == 1 && contains(coalesce(one(p.condition).tag_prefixes, []), "latest")])
    error_message = "fugaro-base must keep every version tagged latest"
  }
  assert {
    condition = anytrue([for p in google_artifact_registry_repository.base.cleanup_policies :
    p.action == "KEEP" && length(p.most_recent_versions) == 1 && one(p.most_recent_versions).keep_count == 3])
    error_message = "fugaro-base must keep the 3 most recent versions"
  }
  assert {
    condition = alltrue([for p in google_artifact_registry_repository.base.cleanup_policies :
    p.action == "KEEP" || (length(p.condition) == 1 && one(p.condition).tag_state == "UNTAGGED")])
    error_message = "fugaro-base's only delete rule must be for untagged versions"
  }
  assert {
    condition = anytrue([for p in google_artifact_registry_repository.base.cleanup_policies :
    p.action == "DELETE" && one(p.condition).tag_state == "UNTAGGED" && one(p.condition).older_than == "1209600s"])
    error_message = "fugaro-base must delete untagged versions after 14 days"
  }
}

run "cleanup_keeps_dev" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition = anytrue([for p in google_artifact_registry_repository.base.cleanup_policies :
    p.action == "KEEP" && length(p.condition) == 1 && contains(coalesce(one(p.condition).tag_prefixes, []), "dev-")])
    error_message = "fugaro-base must keep every version with a dev- tag"
  }
}

run "cleanup_dry_run_default" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = google_artifact_registry_repository.base.cleanup_policy_dry_run == true
    error_message = "cleanup must start in dry-run"
  }
  assert {
    condition     = output.registry_cleanup_dry_run == true
    error_message = "the dry-run setting must be an output, for the repository registries"
  }
}

run "cleanup_off" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    registry_cleanup = { enabled = false, dry_run = false }
  }

  assert {
    condition     = length(google_artifact_registry_repository.base.cleanup_policies) == 0
    error_message = "with cleanup disabled, fugaro-base must have no cleanup policy"
  }
  assert {
    condition     = output.registry_cleanup_dry_run == false
    error_message = "the dry-run output must follow the input"
  }
}

run "roles" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = contains(google_project_iam_custom_role.job_runner.permissions, "run.jobs.runWithOverrides")
    error_message = "fugaroJobRunner must allow runWithOverrides, which every launch uses"
  }
  assert {
    condition     = google_project_iam_custom_role.job_runner.permissions == toset(["run.jobs.run", "run.jobs.runWithOverrides"])
    error_message = "fugaroJobRunner must hold exactly run and runWithOverrides"
  }
  assert {
    condition = google_project_iam_custom_role.launcher.permissions == toset([
      "run.jobs.get", "run.jobs.list", "run.executions.get", "run.executions.list", "run.executions.cancel", "run.operations.get",
      "secretmanager.secrets.list", "secretmanager.versions.list",
    ])
    error_message = "fugaroLauncher's permissions are wrong: fugaro secrets ls needs the project's secret list and each secret's version list (metadata only, never a value)"
  }
  assert {
    condition     = google_project_iam_custom_role.build_submitter.permissions == toset(["cloudbuild.builds.create", "cloudbuild.builds.get"])
    error_message = "fugaroBuildSubmitter's permissions are wrong"
  }
  assert {
    condition = (google_project_iam_custom_role.launcher.role_id == "fugaroLauncher" &&
      google_project_iam_custom_role.job_runner.role_id == "fugaroJobRunner" &&
    google_project_iam_custom_role.build_submitter.role_id == "fugaroBuildSubmitter")
    error_message = "the role IDs must be the inputs"
  }
}

run "people" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = keys(google_project_iam_member.launcher) == ["user:launcher@example.com"]
    error_message = "only the launchers get fugaroLauncher"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs) == ["user:launcher@example.com", "user:operator@example.com"]
    error_message = "launchers and operators get objectAdmin on the runs bucket"
  }
  assert {
    condition     = alltrue([for m in google_storage_bucket_iam_member.runs : m.role == "roles/storage.objectAdmin" && m.bucket == "fugaro-runs-proj-1234"])
    error_message = "the runs bucket grants must be objectAdmin on the runs bucket"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.state) == ["user:operator@example.com"] && google_storage_bucket_iam_member.state["user:operator@example.com"].bucket == "fugaro-tfstate-proj-1234"
    error_message = "only operators get objectAdmin on the state bucket"
  }
  assert {
    condition     = keys(google_artifact_registry_repository_iam_member.base_writer) == ["user:operator@example.com"] && google_artifact_registry_repository_iam_member.base_writer["user:operator@example.com"].role == "roles/artifactregistry.writer"
    error_message = "only operators write fugaro-base"
  }
  assert {
    condition     = keys(google_service_account_iam_member.scheduler_user) == ["user:operator@example.com"]
    error_message = "operators need actAs on the scheduler account"
  }
  assert {
    condition     = google_service_account.scheduler.account_id == "fugaro-scheduler" && google_service_account.scheduler.display_name == "Fugaro scheduler"
    error_message = "the scheduler account's ID is the input and its display name is the mark"
  }
}

run "apis" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition = toset(keys(google_project_service.this)) == toset([
      "run.googleapis.com", "storage.googleapis.com", "secretmanager.googleapis.com", "artifactregistry.googleapis.com",
      "cloudbuild.googleapis.com", "cloudscheduler.googleapis.com", "logging.googleapis.com", "monitoring.googleapis.com",
      "iam.googleapis.com", "cloudresourcemanager.googleapis.com",
    ])
    error_message = "the enabled APIs are wrong"
  }
  assert {
    condition     = alltrue([for s in google_project_service.this : s.disable_on_destroy == false && s.disable_dependent_services == false])
    error_message = "no API may be disabled on destroy"
  }
}

run "apis_vertex_and_unmanaged" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    enable_vertex = true
  }

  assert {
    condition     = contains(keys(google_project_service.this), "aiplatform.googleapis.com")
    error_message = "enable_vertex must enable Vertex AI"
  }
}

run "apis_unmanaged" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    manage_apis = false
  }

  assert {
    condition     = length(google_project_service.this) == 0
    error_message = "manage_apis = false must leave the APIs alone"
  }
}

run "budget_optional" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = length(google_billing_budget.this) == 0
    error_message = "no budget unless asked"
  }
}

run "budget_set" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    budget = { billing_account = "012345-6789AB-CDEF01", amount = 70, currency_code = "CAD" }
  }

  assert {
    condition     = length(google_billing_budget.this) == 1
    error_message = "a budget must be planned when asked"
  }
  assert {
    condition     = google_billing_budget.this[0].amount[0].specified_amount[0].units == "70" && google_billing_budget.this[0].amount[0].specified_amount[0].currency_code == "CAD"
    error_message = "the budget's amount is wrong"
  }
  assert {
    condition     = [for r in google_billing_budget.this[0].threshold_rules : r.threshold_percent] == [0.5, 0.9, 1]
    error_message = "the budget must alert at 50%, 90% and 100%"
  }
  assert {
    condition     = contains(keys(google_project_service.this), "billingbudgets.googleapis.com")
    error_message = "a budget needs the Budget API"
  }
}

run "alert_optional" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = length(google_monitoring_alert_policy.image) == 0 && length(google_monitoring_notification_channel.email) == 0
    error_message = "no alert unless an email is given"
  }
}

run "alert_set" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    alert_email = "ops@example.com"
  }

  assert {
    condition     = google_monitoring_notification_channel.email[0].type == "email" && google_monitoring_notification_channel.email[0].labels["email_address"] == "ops@example.com"
    error_message = "the notification channel must email the input"
  }
  assert {
    condition     = length(google_monitoring_alert_policy.image[0].conditions) == 2
    error_message = "the alert must have its two conditions"
  }
  assert {
    condition = anytrue([for c in google_monitoring_alert_policy.image[0].conditions :
    one(c.condition_matched_log).filter == "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"image-check\" AND severity>=ERROR"])
    error_message = "the alert must match every check-job decision line logged at ERROR: a failed check, a backed-off failed rebuild, and a rebuild after a failed one"
  }
  assert {
    condition = anytrue([for c in google_monitoring_alert_policy.image[0].conditions :
    one(c.condition_matched_log).filter == "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND logName:\"run.googleapis.com%2Fvarlog%2Fsystem\" AND severity>=ERROR"])
    error_message = "the alert must match a failed check-job execution"
  }
}

run "bad_bucket_name" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    runs_bucket = "runs-foo"
  }

  expect_failures = [var.runs_bucket]
}

run "bad_member" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launchers = ["launcher@example.com"]
  }

  expect_failures = [var.launchers]
}

run "log_isolation_on" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = google_logging_project_bucket_config.fugaro[0].bucket_id == "fugaro" && google_logging_project_bucket_config.fugaro[0].location == "global" && google_logging_project_bucket_config.fugaro[0].retention_days == 30
    error_message = "the log bucket must be the named one, global, with 30 days of retention"
  }
  assert {
    condition     = google_logging_project_bucket_config.fugaro[0].description == "Fugaro job logs (managed by fugaro)"
    error_message = "the log bucket must carry the description it is given: it is the bucket's ownership mark, since a log bucket has no labels"
  }
  assert {
    condition     = google_logging_project_sink.fugaro[0].name == "fugaro-jobs" && google_logging_project_sink.fugaro[0].unique_writer_identity == true
    error_message = "the sink must carry its name and a unique writer identity"
  }
  assert {
    condition     = google_logging_project_sink.fugaro[0].filter == "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\""
    error_message = "the sink must route every Fugaro-managed job's logs"
  }
  assert {
    condition     = google_logging_project_sink.fugaro[0].destination == "logging.googleapis.com/projects/proj-1234/locations/global/buckets/fugaro"
    error_message = "the sink must write to the project's own log bucket"
  }
  assert {
    condition     = google_logging_project_exclusion.fugaro_from_default[0].name == "fugaro-jobs-from-default"
    error_message = "the exclusion must carry its name"
  }
  # Log-based alerts must keep seeing the check's own decision lines and
  # the Cloud Run system log, so the exclusion leaves both in _Default.
  assert {
    condition     = google_logging_project_exclusion.fugaro_from_default[0].filter == "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\" AND NOT (resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"image-check\") AND NOT logName:\"run.googleapis.com%2Fvarlog%2Fsystem\""
    error_message = "the exclusion must leave the alerts' log lines in _Default, and only the check jobs' decision lines"
  }
  assert {
    condition     = google_logging_log_view.runs[0].name == "fugaro-runs" && google_logging_log_view.runs[0].bucket == "projects/proj-1234/locations/global/buckets/fugaro"
    error_message = "the view must be the named one, on the log bucket"
  }
  assert {
    condition     = sort(keys(google_logging_log_view_iam_member.runs)) == tolist(["user:launcher@example.com", "user:operator@example.com"])
    error_message = "launchers and operators must be able to read through the view"
  }
  assert {
    condition     = alltrue([for m in google_logging_log_view_iam_member.runs : m.parent == "projects/proj-1234" && m.location == "global" && m.bucket == "fugaro" && m.name == "fugaro-runs"])
    error_message = "the view grant must name the view by its project path, not the bare project ID"
  }
  assert {
    condition     = alltrue([for m in google_logging_log_view_iam_member.runs : m.role == "roles/logging.viewAccessor"])
    error_message = "the view is read with roles/logging.viewAccessor"
  }
  assert {
    condition     = output.log_view == "projects/proj-1234/locations/global/buckets/fugaro/views/fugaro-runs"
    error_message = "log_view must be the view's full resource name"
  }
}

run "bad_log_bucket_description" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    log_bucket_description = ""
  }

  expect_failures = [var.log_bucket_description]
}

run "log_isolation_off" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    log_isolation = false
  }

  assert {
    condition     = length(google_logging_project_bucket_config.fugaro) == 0 && length(google_logging_project_sink.fugaro) == 0 && length(google_logging_project_exclusion.fugaro_from_default) == 0 && length(google_logging_log_view.runs) == 0 && length(google_logging_log_view_iam_member.runs) == 0
    error_message = "no logging resource without log isolation"
  }
  assert {
    condition     = output.log_view == null
    error_message = "log_view is null without log isolation"
  }
}
