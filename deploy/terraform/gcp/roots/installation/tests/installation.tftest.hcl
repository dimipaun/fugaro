# Plans the installation against a mock provider: no credentials, no network
# beyond the provider download. The names are the ones fugaro init writes
# into terraform.tfvars.json.
#
# An assertion can't reach into a child module's resources, so the first run
# plans the root (checking its wiring through the outputs) and the others
# plan the module itself.

mock_provider "google" {}

variables {
  project        = "proj-1234"
  fugaro_project = "aurora"
  region         = "us-east5"
  runs_bucket    = "fugaro-runs-proj-1234"
  state_bucket   = "fugaro-tfstate-proj-1234"
  names = {
    legacy_registry              = "fugaro"
    base_registry                = "fugaro-base"
    scheduler_service_account_id = "fugaro-scheduler"
    role_ids = {
      launcher        = "fugaroLauncher"
      job_runner      = "fugaroJobRunner"
      build_submitter = "fugaroBuildSubmitter"
      tag_mover       = "fugaroTagMover"
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
  launcher_bucket_condition = {
    title      = "fugaro-launchers-runs"
    expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"
  }
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
    condition     = output.project_name == "aurora"
    error_message = "the root must pass fugaro_project to the module and output it as project_name"
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
  assert {
    condition     = keys(output.role_ids) == ["build_submitter", "job_runner", "launcher", "tag_mover"]
    error_message = "the root must output every custom role's full name, the tag mover's included"
  }
}

run "bucket_is_marked" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = google_storage_bucket.runs.labels == tomap({ fugaro = "managed", fugaro_project = "aurora" })
    error_message = "the runs bucket must carry exactly fugaro=managed and the project's name"
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
    condition     = google_project_iam_custom_role.tag_mover.permissions == toset(["artifactregistry.tags.delete"])
    error_message = "fugaroTagMover must hold exactly artifactregistry.tags.delete, which moving an existing :latest needs and writer lacks"
  }
  assert {
    condition     = google_project_iam_custom_role.tag_mover.title == "Fugaro tag mover"
    error_message = "fugaroTagMover's title is its ownership mark, which discovery matches"
  }
  assert {
    condition = (google_project_iam_custom_role.launcher.role_id == "fugaroLauncher" &&
      google_project_iam_custom_role.job_runner.role_id == "fugaroJobRunner" &&
      google_project_iam_custom_role.build_submitter.role_id == "fugaroBuildSubmitter" &&
    google_project_iam_custom_role.tag_mover.role_id == "fugaroTagMover")
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
    condition     = keys(google_storage_bucket_iam_member.runs) == ["user:operator@example.com"]
    error_message = "only operators get objectAdmin on the runs bucket"
  }
  assert {
    condition     = alltrue([for m in google_storage_bucket_iam_member.runs : m.role == "roles/storage.objectAdmin" && m.bucket == "fugaro-runs-proj-1234" && length(m.condition) == 0])
    error_message = "operators' runs bucket grants must be unconditioned objectAdmin"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs_reader) == ["user:launcher@example.com"]
    error_message = "launchers who are not operators read the runs bucket"
  }
  assert {
    condition     = alltrue([for m in google_storage_bucket_iam_member.runs_reader : m.role == "roles/storage.objectViewer" && m.bucket == "fugaro-runs-proj-1234" && length(m.condition) == 0])
    error_message = "the launchers' read grant is unconditioned objectViewer: listing needs it"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs_launcher) == ["user:launcher@example.com"]
    error_message = "launchers who are not operators write runs/"
  }
  assert {
    condition = alltrue([for m in google_storage_bucket_iam_member.runs_launcher :
      m.role == "roles/storage.objectUser" && m.bucket == "fugaro-runs-proj-1234" &&
      one(m.condition).title == "fugaro-launchers-runs" &&
    one(m.condition).expression == "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"])
    error_message = "the launchers' write grant is objectUser on runs/ only"
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

# A member on both lists gets the operator grant only (H4).
run "launcher_who_is_an_operator" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launchers = ["user:both@example.com", "user:launcher@example.com"]
    operators = ["user:both@example.com"]
  }

  assert {
    condition     = keys(google_storage_bucket_iam_member.runs) == ["user:both@example.com"]
    error_message = "the operator keeps objectAdmin"
  }
  assert {
    condition     = keys(google_storage_bucket_iam_member.runs_reader) == ["user:launcher@example.com"] && keys(google_storage_bucket_iam_member.runs_launcher) == ["user:launcher@example.com"]
    error_message = "an operator who is also a launcher gets no launcher grant"
  }
}

# A condition that is not runs/ of this bucket fails the plan.
run "launcher_condition_must_be_this_buckets_runs" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "fugaro-launchers-runs"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/\")"
    }
  }

  expect_failures = [google_storage_bucket_iam_member.runs_launcher]
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
    condition     = toset(keys(google_monitoring_alert_policy.image)) == toset(["check", "job"])
    error_message = "the alert must be two policies, one per log-match condition: a log-match policy can have only one condition"
  }
  assert {
    condition     = alltrue([for p in google_monitoring_alert_policy.image : length(p.conditions) == 1])
    error_message = "a policy with a log-match condition can have only that one condition"
  }
  assert {
    condition     = one(google_monitoring_alert_policy.image["check"].conditions[0].condition_matched_log).filter == "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"image-check\" AND severity>=ERROR"
    error_message = "the alert must match every check-job decision line logged at ERROR: a failed check, a backed-off failed rebuild, and a rebuild after a failed one"
  }
  assert {
    condition     = one(google_monitoring_alert_policy.image["job"].conditions[0].condition_matched_log).filter == "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND (logName:\"run.googleapis.com%2Fvarlog%2Fsystem\" OR logName:\"cloudaudit.googleapis.com%2Fsystem_event\") AND severity>=ERROR"
    error_message = "the alert must match a failed check-job execution in both logs: the audit log's system events (a failure at container start, such as an unreadable secret) and the Cloud Run system log (a failure after start)"
  }
  assert {
    condition = alltrue([for p in google_monitoring_alert_policy.image :
      one(p.alert_strategy).notification_rate_limit[0].period == "3600s" && one(p.alert_strategy).auto_close == "604800s"
    ])
    error_message = "a log-match policy needs a notification rate limit, and each closes after a week"
  }
  assert {
    condition     = alltrue([for p in google_monitoring_alert_policy.image : length(p.notification_channels) == 1])
    error_message = "each policy must email the notification channel"
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

run "project_marker_object" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = google_storage_bucket_object.project_marker.name == "fugaro/project.json"
    error_message = "the marker must be fugaro/project.json in the runs bucket"
  }
  assert {
    condition     = google_storage_bucket_object.project_marker.content_type == "application/json"
    error_message = "the marker is JSON"
  }
  assert {
    condition     = jsondecode(google_storage_bucket_object.project_marker.content) == { version = 1, name = "aurora", gcp_project = "proj-1234" }
    error_message = "the marker must hold the version, the project's name and the GCP project ID"
  }
  assert {
    condition     = output.project_name == "aurora"
    error_message = "the module must output project_name"
  }
}

run "bad_project_name" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    fugaro_project = "Not A Name"
  }

  expect_failures = [var.fugaro_project]
}

run "budget_off_has_no_history" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = length(google_service_account.history) == 0 && length(google_cloud_run_v2_job.history) == 0 && length(google_cloud_scheduler_job.history_sweep) == 0 && length(google_cloud_scheduler_job.history_rollover) == 0 && length(google_storage_bucket_iam_member.history_runs_reader) == 0
    error_message = "without enable_budget there is no history account, job, Scheduler job or bucket grant"
  }
  assert {
    condition     = output.history_service_account == null && output.history_job == null
    error_message = "without enable_budget the history outputs are null"
  }
}

run "budget_token_objects_expire" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  assert {
    condition     = length(google_storage_bucket.runs.lifecycle_rule) == 4
    error_message = "with enable_budget the runs bucket has the bootstrap's three lifecycle rules and one for untaken budget tokens"
  }
  assert {
    condition = anytrue([for r in google_storage_bucket.runs.lifecycle_rule :
      one(r.action).type == "Delete" && one(r.condition).age == 1 &&
      one(r.condition).matches_prefix == tolist(["runs/"]) &&
    one(r.condition).matches_suffix == tolist(["/budget-token"])])
    error_message = "runs/*/*/budget-token must be deleted after a day, and only that suffix"
  }
  assert {
    condition = length([for r in google_storage_bucket.runs.lifecycle_rule :
    r if one(r.condition).age == 90 && length(coalesce(one(r.condition).matches_suffix, [])) == 0]) == 1
    error_message = "the 90-day runs/ rule keeps matching every object under runs/"
  }
}

run "history_account_first" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  assert {
    condition     = google_service_account.history[0].email == "fugaro-history@proj-1234.iam.gserviceaccount.com" && google_service_account.history[0].display_name == "Fugaro history"
    error_message = "the history account's ID is the input and its display name is the mark"
  }
  assert {
    condition     = output.history_service_account == "fugaro-history@proj-1234.iam.gserviceaccount.com"
    error_message = "the history account's email is an output, for the Firebase root"
  }
  assert {
    condition     = length(google_project_iam_custom_role.history) == 1 && !contains(google_project_iam_custom_role.history[0].permissions, "run.executions.cancel") && contains(google_project_iam_custom_role.history[0].permissions, "run.executions.list")
    error_message = "the history account has its own narrow role: it lists executions and cannot cancel them"
  }
  assert {
    condition     = length(google_cloud_run_v2_job.history) == 0 && length(google_cloud_scheduler_job.history_sweep) == 0 && length(google_cloud_run_v2_job_iam_member.history_runner) == 0
    error_message = "the first apply deploys only the account: the job and its Scheduler job wait for deploy_job"
  }
}

run "TestHistoryAccountGrants" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target          = google_project_iam_custom_role.history[0]
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroHistory"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  assert {
    condition     = google_project_iam_member.history_launcher[0].role == "projects/proj-1234/roles/fugaroHistory" && google_project_iam_member.history_launcher[0].member == "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    error_message = "the history account holds its own narrow fugaroHistory role on the project, for run.executions.list"
  }
  assert {
    condition     = length(google_service_account_iam_member.scheduler_user) == 1 && !contains(keys(google_service_account_iam_member.scheduler_user), "user:launcher@example.com")
    error_message = "the only actAs grants are the scheduler account's, to operators"
  }
  assert {
    condition     = !contains(keys(google_storage_bucket_iam_member.runs), "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com")
    error_message = "the history account is not among the runs bucket's object admins (its read-only grant is history_runs_reader)"
  }
}

run "TestSweepSchedulerJob" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.job_runner
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroJobRunner"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
    }
  }

  assert {
    condition     = length(google_cloud_scheduler_job.history_sweep) == 1 && one(google_cloud_scheduler_job.history_sweep[0].http_target).body == null
    error_message = "the sweep job sends no body: the job's own --sweep args run"
  }
  assert {
    condition     = google_cloud_scheduler_job.history_sweep[0].schedule == "*/15 * * * *" && google_cloud_scheduler_job.history_sweep[0].time_zone == "Etc/UTC"
    error_message = "the sweep runs every 15 minutes"
  }
  assert {
    condition     = google_cloud_scheduler_job.history_sweep[0].region == "us-east1" && google_cloud_scheduler_job.history_sweep[0].name == "fugaro-history-sweep"
    error_message = "the sweep's region and name are the inputs"
  }
  assert {
    condition     = one(google_cloud_scheduler_job.history_sweep[0].http_target).uri == "https://run.googleapis.com/v2/projects/proj-1234/locations/us-east5/jobs/fugarohist:run"
    error_message = "the sweep starts the history job, in the job's own region"
  }
  assert {
    condition     = one(one(google_cloud_scheduler_job.history_sweep[0].http_target).oauth_token).service_account_email == "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    error_message = "the sweep runs as fugaro-scheduler"
  }
  assert {
    condition     = google_cloud_run_v2_job_iam_member.history_runner[0].role == "projects/proj-1234/roles/fugaroJobRunner" && google_cloud_run_v2_job_iam_member.history_runner[0].role != "roles/run.invoker" && google_cloud_run_v2_job_iam_member.history_runner[0].member == "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com" && google_cloud_run_v2_job_iam_member.history_runner[0].name == "fugarohist"
    error_message = "fugaro-scheduler holds fugaroJobRunner (run and runWithOverrides) on the history job only: the rollover's overrides need runWithOverrides, which roles/run.invoker lacks (live HTTP 403, check 23 step 8)"
  }
  assert {
    condition     = contains(google_project_iam_custom_role.job_runner.permissions, "run.jobs.run") && contains(google_project_iam_custom_role.job_runner.permissions, "run.jobs.runWithOverrides")
    error_message = "the role the scheduler holds on the history job must carry run.jobs.run (the sweep) and run.jobs.runWithOverrides (the rollover)"
  }
}

run "history_job" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
    }
  }

  assert {
    condition     = google_cloud_run_v2_job.history[0].name == "fugarohist" && !startswith(google_cloud_run_v2_job.history[0].name, "fugaro-")
    error_message = "the history job's name must not start with fugaro-, which ls and max_parallel count as workflow jobs"
  }
  assert {
    condition     = one(one(google_cloud_run_v2_job.history[0].template).template).service_account == "fugaro-history@proj-1234.iam.gserviceaccount.com"
    error_message = "the history job runs as the history account"
  }
  assert {
    condition = (one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).command == tolist(["/usr/local/bin/fugaro"]) &&
    one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).args == tolist(["budget", "history", "--sweep"]))
    error_message = "command replaces the image's ENTRYPOINT: it must be the binary's absolute path, not a PATH lookup"
  }
  assert {
    condition     = one(one(google_cloud_run_v2_job.history[0].template).template).max_retries == 0
    error_message = "the history job is never retried behind the Scheduler's back"
  }
  assert {
    condition = (toset([for e in one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).env : "${e.name}=${e.value}"]) == toset([
      "FUGARO_PROJECT=aurora", "FUGARO_GCP_PROJECT=proj-1234", "FUGARO_REGION=us-east5",
      "FUGARO_FIREBASE_PROJECT=fp-1234", "FUGARO_RTDB_URL=https://fp-1234-default-rtdb.firebaseio.com",
      "FUGARO_RUNS_BUCKET=fugaro-runs-proj-1234", "FUGARO_FIRESTORE_DB=(default)",
    ]))
    error_message = "the history job's environment is exactly the project, the region, the FP's identifiers, the runs bucket and the Firestore database, and holds no credential"
  }
  assert {
    condition     = one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).args == tolist(["budget", "history", "--sweep"])
    error_message = "the job runs fugaro budget history --sweep"
  }
  assert {
    condition     = output.history_job == "fugarohist"
    error_message = "the history job's name is an output"
  }
}

run "history_job_name_must_not_be_a_workflow_job" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugaro-history"
      image            = "x"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  expect_failures = [var.history]
}

run "history_deploy_needs_firebase_outputs" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "x"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
    }
  }

  expect_failures = [var.history]
}

run "enable_budget_needs_history" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    enable_budget = true
  }

  expect_failures = [google_service_account.history]
}

# The history account holds firebasedatabase.admin on the FP: granting anyone
# actAs on it would let them deploy a job as it and lift caps (design D6).
# The module has no resource that could grant it, which the Go text check
# also pins; this plan asserts the grants that do exist.
run "history_account_has_no_actas_grants" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "x"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  assert {
    condition = alltrue([for m in google_service_account_iam_member.scheduler_user :
    m.service_account_id != "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"])
    error_message = "no operator or launcher may act as the history account"
  }
}

run "bad_history_account_id" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "X"
      job              = "fugarohist"
      image            = "x"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  expect_failures = [var.history]
}

run "TestRolloverSchedulerJob" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
    }
  }

  assert {
    condition     = length(google_cloud_scheduler_job.history_rollover) == 1 && google_cloud_scheduler_job.history_rollover[0].name == "fugaro-history-rollover" && google_cloud_scheduler_job.history_rollover[0].region == "us-east1"
    error_message = "one rollover Scheduler job, fugaro-history-rollover, in the scheduler region"
  }
  assert {
    condition     = google_cloud_scheduler_job.history_rollover[0].schedule == "30 0 * * *" && google_cloud_scheduler_job.history_rollover[0].time_zone == "Etc/UTC"
    error_message = "the rollover runs at 00:30 UTC"
  }
  assert {
    condition     = one(google_cloud_scheduler_job.history_rollover[0].http_target).uri == "https://run.googleapis.com/v2/projects/proj-1234/locations/us-east5/jobs/fugarohist:run" && one(google_cloud_scheduler_job.history_rollover[0].http_target).http_method == "POST"
    error_message = "the rollover starts the same history job, as the sweep does"
  }
  # Unverified assumption A4: jobs.run honours overrides.containerOverrides[].args.
  # This pins exactly what is sent, so the live runbook has one body to prove.
  assert {
    condition     = jsondecode(base64decode(one(google_cloud_scheduler_job.history_rollover[0].http_target).body)) == { overrides = { containerOverrides = [{ args = ["budget", "history", "--rollover"] }], timeout = "1800s" } }
    error_message = "the rollover body is exactly {overrides:{containerOverrides:[{args:[budget,history,--rollover]}],timeout:1800s}}"
  }
  assert {
    condition     = one(google_cloud_scheduler_job.history_rollover[0].retry_config).retry_count == 0 && one(google_cloud_scheduler_job.history_sweep[0].retry_config).retry_count == 0
    error_message = "both history Scheduler jobs declare retry_config retry_count 0 (the server default), so a plan shows no drift on it"
  }
  assert {
    condition     = google_cloud_scheduler_job.history_rollover[0].paused == true
    error_message = "the rollover job is created paused: the first prune is a person's decision (resume after the manual run)"
  }
  assert {
    condition     = one(google_cloud_scheduler_job.history_rollover[0].http_target).headers["Content-Type"] == "application/json"
    error_message = "the rollover body is JSON"
  }
  assert {
    condition     = one(one(google_cloud_scheduler_job.history_rollover[0].http_target).oauth_token).service_account_email == "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    error_message = "the rollover runs as fugaro-scheduler, which holds fugaroJobRunner on the history job only"
  }
  assert {
    condition     = one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).args == tolist(["budget", "history", "--sweep"])
    error_message = "the job's own args stay --sweep: the rollover only overrides them per run"
  }
  assert {
    condition     = google_cloud_scheduler_job.history_sweep[0].schedule == "*/15 * * * *"
    error_message = "the 15-minute sweep is unchanged"
  }
}

run "TestHistoryEnvHasBucket" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
    }
  }

  assert {
    condition = (contains([for e in one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).env : "${e.name}=${e.value}"], "FUGARO_RUNS_BUCKET=fugaro-runs-proj-1234") &&
    contains([for e in one(one(one(google_cloud_run_v2_job.history[0].template).template).containers).env : "${e.name}=${e.value}"], "FUGARO_FIRESTORE_DB=(default)"))
    error_message = "the history job names the runs bucket and the Firestore database"
  }
  assert {
    condition = (google_storage_bucket_iam_member.history_runs_reader[0].role == "roles/storage.objectViewer" && google_storage_bucket_iam_member.history_runs_reader[0].bucket == "fugaro-runs-proj-1234" &&
    google_storage_bucket_iam_member.history_runs_reader[0].member == "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com")
    error_message = "the history account reads the runs bucket (objectViewer), that bucket only"
  }
}

# The same-project layout puts these accounts in the project that also holds
# the budget backend, so the resolved project-level grants and custom roles of
# the installation module are pinned exactly: people get only the launcher and
# build-submitter custom roles, the one service account with a project role is
# the history account (its narrow Cloud Run role), and the scheduler account
# has none. (Mock providers expose a child module's resources only when the
# module itself is the run's target, hence a module-level run here; the repo
# module's and the workflow module's equivalents are in repo.tftest.hcl, and
# the text scan in deploy/terraform_test.go covers every IAM resource.)
run "same_project_resolved_iam" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.history[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-history@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-history@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target          = google_project_iam_custom_role.launcher
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroLauncher"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.job_runner
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroJobRunner"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.build_submitter
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroBuildSubmitter"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.tag_mover
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroTagMover"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.history[0]
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroHistory"
    }
  }

  variables {
    enable_budget = true
    history = {
      account_id       = "fugaro-history"
      job              = "fugarohist"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-history-sweep"
      scheduler_region = "us-east1"
    }
  }

  assert {
    condition = (alltrue([for m in google_project_iam_member.launcher : m.role == google_project_iam_custom_role.launcher.name && !startswith(m.member, "serviceAccount:")]) &&
    alltrue([for m in google_project_iam_member.operator_build_submitter : m.role == google_project_iam_custom_role.build_submitter.name && !startswith(m.member, "serviceAccount:")]))
    error_message = "the installation grants people only fugaroLauncher and fugaroBuildSubmitter on the project"
  }
  assert {
    condition = (google_project_iam_member.history_launcher[0].role == google_project_iam_custom_role.history[0].name &&
    google_project_iam_member.history_launcher[0].member == "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com")
    error_message = "the history account's one project role in the installation is its narrow Cloud Run role"
  }
  assert {
    condition = google_project_iam_custom_role.history[0].permissions == toset([
      "run.executions.list", "run.executions.get", "run.jobs.get", "run.jobs.list", "run.operations.get",
    ])
    error_message = "fugaroHistory holds exactly the Cloud Run read permissions"
  }
  assert {
    condition = alltrue(flatten([
      [for r in [google_project_iam_custom_role.launcher, google_project_iam_custom_role.job_runner, google_project_iam_custom_role.build_submitter, google_project_iam_custom_role.tag_mover, google_project_iam_custom_role.history[0]] :
      [for p in r.permissions : !can(regex("^(firebase|datastore|identitytoolkit|iam\\.|resourcemanager|serviceusage|apikeys)", p))]],
    ]))
    error_message = "no installation custom role holds a permission on the budget backend, identities or IAM"
  }
}
