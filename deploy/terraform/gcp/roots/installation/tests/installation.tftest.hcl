# Plans the installation against a mock provider: no credentials, no network
# beyond the provider download. The names are the ones fugaro update writes
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
    ghcr_registry                = "fugaro-ghcr"
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
    title      = "fugaro-launchers-runs-tokens"
    expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
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
    condition     = output.base_registry == "fugaro-base" && output.legacy_registry == "fugaro" && output.ghcr_registry == "fugaro-ghcr"
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
    condition     = keys(output.role_ids) == ["build_submitter", "cloud_asset_reader", "job_runner", "launcher", "tag_mover"]
    error_message = "the root must output every custom role's full name, the tag mover's and the Cloud Asset reader's included"
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
    condition     = google_artifact_registry_repository.ghcr.mode == "REMOTE_REPOSITORY" && google_artifact_registry_repository.ghcr.remote_repository_config[0].docker_repository[0].custom_repository[0].uri == "https://ghcr.io"
    error_message = "the remote repository must proxy https://ghcr.io"
  }
  assert {
    condition     = length(google_artifact_registry_repository.legacy[0].cleanup_policies) == 0
    error_message = "the legacy registry holds the rollback's images and must have no cleanup policy"
  }
  assert {
    condition     = output.legacy_registry == "fugaro" && output.base_registry == "fugaro-base" && output.ghcr_registry == "fugaro-ghcr"
    error_message = "the registry outputs must be the IDs"
  }
  assert {
    condition     = output.registry_host == "us-east5-docker.pkg.dev/proj-1234"
    error_message = "registry_host must be <region>-docker.pkg.dev/<project>"
  }
}

# The shared image registry: one repository per installation, holding every
# repository's shared images (design
# docs/forge-authorship-images/shared-images.md §2.6). It is named by Go
# (gcp.SharedRegistryRepoID, guarded against this literal by
# TestSharedRegistryRepoIDMatchesTheModule), and nobody is granted on it here:
# each repository root grants its own build account writer and its own job
# accounts reader, so this module holds no member that would widen it to every
# repository at once.
run "shared_registry" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition = (google_artifact_registry_repository.shared.repository_id == "fugaro-shared-images-0829826e4aa4" &&
      google_artifact_registry_repository.shared.format == "DOCKER" &&
      google_artifact_registry_repository.shared.location == "us-east5" &&
    google_artifact_registry_repository.shared.project == "proj-1234")
    error_message = "the shared image registry must be a Docker registry in the installation's region, under the name gcp.SharedRegistryRepoID derives"
  }
  assert {
    condition     = google_artifact_registry_repository.shared.labels == tomap({ fugaro = "managed" })
    error_message = "the shared registry must carry fugaro=managed"
  }
  assert {
    condition = alltrue([for p in google_artifact_registry_repository.shared.cleanup_policies :
      p.action == "KEEP" || (length(p.condition) == 1 && (one(p.condition).tag_state == "UNTAGGED" ||
    one(p.condition).tag_prefixes == tolist(["candidate-"])))])
    error_message = "the shared registry's only delete rules must be for untagged and candidate- versions, as a repository's own registry's are"
  }
  assert {
    condition     = length(google_artifact_registry_repository.shared.cleanup_policies) == 4
    error_message = "the shared registry must keep every version tagged latest and the 3 newest, and delete untagged and candidate- versions"
  }
  assert {
    condition     = output.shared_registry == "fugaro-shared-images-0829826e4aa4"
    error_message = "the shared registry's ID must be an output: every repository root grants its build and job accounts on it"
  }
  assert {
    condition = alltrue(concat(
      [for m in google_artifact_registry_repository_iam_member.base_writer : m.member != "allUsers" && m.member != "allAuthenticatedUsers"],
      [for m in google_artifact_registry_repository_iam_member.ghcr_reader : m.member != "allUsers" && m.member != "allAuthenticatedUsers"],
    ))
    error_message = "no member this module grants on a registry may be public. It grants none on the shared registry at all: that registry's writer is each repository's build account and its readers are each repository's job accounts, granted by that repository's own root (modules/repo/registry.tf), which TestSharedRegistryRepoIDMatchesTheModule pins over this module's text."
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
    condition     = length(google_artifact_registry_repository.shared.cleanup_policies) == 0
    error_message = "with cleanup disabled, the shared image registry must have no cleanup policy either: the switch governs this installation's own repositories, and the shared one is one of them"
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
    error_message = "launchers who are not operators write runs/ and tokens/"
  }
  assert {
    condition = alltrue([for m in google_storage_bucket_iam_member.runs_launcher :
      m.role == "roles/storage.objectUser" && m.bucket == "fugaro-runs-proj-1234" &&
      one(m.condition).title == "fugaro-launchers-runs-tokens" &&
    one(m.condition).expression == "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"])
    error_message = "the launchers' write grant is objectUser on runs/ and tokens/ only"
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

# A condition that is not runs/ and tokens/ of this bucket fails the plan.
run "launcher_condition_must_be_this_buckets_runs" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "fugaro-launchers-runs-tokens"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/\")"
    }
  }

  expect_failures = [google_storage_bucket_iam_member.runs_launcher]
}

# The pre-seal condition, runs/ alone under its old title, fails the plan: the
# launcher writes the sealed budget token under tokens/
# (docs/design/budget-token-channel.md section 7.2), and the precondition is
# where the Terraform and gcp.LauncherBucketCondition must agree.
run "launcher_condition_must_cover_tokens" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "fugaro-launchers-runs"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"
    }
  }

  expect_failures = [google_storage_bucket_iam_member.runs_launcher]
}

# Widened past tokens/ (a third prefix) fails the plan too.
run "launcher_condition_not_past_tokens" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "fugaro-launchers-runs-tokens"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/\")"
    }
  }

  expect_failures = [google_storage_bucket_iam_member.runs_launcher]
}

# The right expression under the wrong title still fails the plan: the
# precondition checks both, not only the expression.
run "launcher_condition_title_must_match" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "other"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
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
      "iam.googleapis.com", "cloudresourcemanager.googleapis.com", "cloudasset.googleapis.com",
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

# launcher_bucket_condition's own validation fires even when no launcher
# grant exists to carry the precondition (a one-person installation plans
# no runs_launcher instance at all), so it is not redundant with the
# resource precondition above and needs its own coverage.
run "bad_launcher_bucket_condition" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    launcher_bucket_condition = {
      title      = "fugaro-launchers-runs-tokens"
      expression = "true"
    }
  }

  expect_failures = [var.launcher_bucket_condition]
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
    condition     = google_logging_project_sink.fugaro[0].filter == "((resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\") OR (resource.type=\"batch.googleapis.com/Job\" AND labels.\"fugaro\"=\"managed\"))"
    error_message = "the sink must route every Fugaro-managed job's logs, on both platforms, with each arm parenthesized: Cloud Logging's query language binds AND loosest (NOT, then OR, then AND), so an unparenthesized mix routes the Batch arm nowhere"
  }
  # The Batch arm is additive: an installation with no Batch jobs has no
  # entry with that resource type, so it moves nothing. What must NOT change
  # is the Cloud Run arm every existing sink and exclusion already filters
  # on, so it is pinned byte-for-byte inside both filters (design
  # docs/design/compute-backend.md §1.5, §8.1 task 4).
  assert {
    condition     = strcontains(google_logging_project_sink.fugaro[0].filter, "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\"") && strcontains(google_logging_project_exclusion.fugaro_from_default[0].filter, "resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\"")
    error_message = "the Cloud Run arm must stay byte-for-byte what it was: every installation's sink and exclusion already filter on it"
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
  # the Cloud Run system log, so the exclusion leaves both in _Default. The
  # pair of arms is one parenthesized group with each arm's AND clauses
  # inside its own parentheses, and the NOT clauses hang off the group.
  # Cloud Logging's query language binds AND *loosest* (NOT, then OR, then
  # AND), so the grouping is explicit rather than left to precedence.
  assert {
    condition     = google_logging_project_exclusion.fugaro_from_default[0].filter == "((resource.type=\"cloud_run_job\" AND labels.\"fugaro\"=\"managed\") OR (resource.type=\"batch.googleapis.com/Job\" AND labels.\"fugaro\"=\"managed\")) AND NOT (resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"image-check\") AND NOT logName:\"run.googleapis.com%2Fvarlog%2Fsystem\""
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
      one(r.condition).matches_prefix == tolist(["runs/", "tokens/"]) &&
    one(r.condition).matches_suffix == tolist(["/budget-token"])])
    error_message = "tokens/*/*/budget-token (and a pre-seal runs/*/*/budget-token) must be deleted after a day, and only that suffix"
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

# The coordinator is the owner's opt-in (P5): a budget installation with the
# history job deployed and no coordinator block — every installation until its
# owner runs fugaro update --firebase-project --coordinator — plans no
# coordinator account, grant, job or schedule, and outputs no account for the
# Firebase root to grant the database's admin to.
run "coordinator_off_by_default" {
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
    condition = (length(google_service_account.coordinator) == 0 &&
      length(google_storage_bucket_iam_member.coordinator_runs_reader) == 0 &&
      length(google_secret_manager_secret_iam_member.coordinator_app_key) == 0 &&
      length(google_cloud_run_v2_job.coordinator) == 0 &&
      length(google_cloud_run_v2_job_iam_member.coordinator_runner) == 0 &&
    length(google_cloud_scheduler_job.coordinator_sweep) == 0)
    error_message = "without the coordinator opt-in there is no coordinator account, grant, job or schedule"
  }
  assert {
    condition     = output.coordinator_service_account == null && output.coordinator_job == null
    error_message = "without the coordinator opt-in the coordinator outputs are null"
  }
}

# The coordinator's account, which the Firebase root grants the FP's database
# admin from (coordinator plan P5, D194): the first apply creates it and its
# read of the runs bucket, and nothing else — the job, the App key's grant and
# the schedule wait for deploy_job, the history account's own shape. A run that
# records no coordinator plans neither (coordinator_off_by_default), which is
# what keeps every installation that has not opted in byte for byte where it
# was.
run "coordinator_account_first" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.coordinator[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
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
    coordinator = {
      account_id       = "fugaro-coordinator"
      job              = "fugarocoord"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-coordinator-sweep"
      scheduler_region = "us-east1"
    }
  }

  assert {
    condition     = google_service_account.coordinator[0].account_id == "fugaro-coordinator" && google_service_account.coordinator[0].display_name == "Fugaro coordinator"
    error_message = "the coordinator account's ID is the input and its display name is the mark"
  }
  assert {
    condition     = output.coordinator_service_account == "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
    error_message = "the coordinator account's email is an output, for the Firebase root"
  }
  assert {
    condition     = length(google_storage_bucket_iam_member.coordinator_runs_reader) == 1 && google_storage_bucket_iam_member.coordinator_runs_reader[0].role == "roles/storage.objectViewer"
    error_message = "the account reads the runs bucket, read-only: the view is computed from the runs, and the coordinator writes no object"
  }
  assert {
    condition     = length(google_cloud_run_v2_job.coordinator) == 0 && length(google_cloud_scheduler_job.coordinator_sweep) == 0 && length(google_secret_manager_secret_iam_member.coordinator_app_key) == 0
    error_message = "the first apply deploys only the account: the job, the App key's grant and the Scheduler job wait for deploy_job"
  }
  assert {
    condition     = length(google_service_account_iam_member.scheduler_user) == 1 && !contains(keys(google_service_account_iam_member.scheduler_user), "user:launcher@example.com")
    error_message = "the only actAs grants stay the scheduler account's, to operators: nobody may act as the coordinator's writer, which holds the FP's database admin"
  }
}

run "coordinator_job" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.coordinator[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
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
    target          = google_project_iam_custom_role.job_runner
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroJobRunner"
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
    project_secrets = {
      "github-app-key" = "fugaro-project-aurora-github-app-key-440b21d6e639db12"
    }
    coordinator = {
      account_id       = "fugaro-coordinator"
      job              = "fugarocoord"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-coordinator-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
      github_app_id    = "123456789"
      repos = [
        { provider = "github", repo = "acme/webapp" },
        { provider = "bitbucket", repo = "acme/sandbox" },
      ]
    }
  }

  assert {
    condition     = google_cloud_run_v2_job.coordinator[0].name == "fugarocoord" && !startswith(google_cloud_run_v2_job.coordinator[0].name, "fugaro-")
    error_message = "the coordinator job's name must not start with fugaro-, which ls and max_parallel count as workflow jobs"
  }
  assert {
    condition     = one(one(google_cloud_run_v2_job.coordinator[0].template).template).service_account == "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
    error_message = "the coordinator job runs as the coordinator account, the one identity that writes the coordinator's subtree"
  }
  assert {
    condition = (one(one(one(google_cloud_run_v2_job.coordinator[0].template).template).containers).command == tolist(["/usr/local/bin/fugaro"]) &&
    one(one(one(google_cloud_run_v2_job.coordinator[0].template).template).containers).args == tolist(["coordinator", "sweep"]))
    error_message = "command replaces the image's ENTRYPOINT: it must be the binary's absolute path, and the args are the sweep"
  }
  assert {
    condition     = one(one(google_cloud_run_v2_job.coordinator[0].template).template).max_retries == 0
    error_message = "the sweep is never retried: the next tick is the retry, so two passes of one repository cannot run at once"
  }
  assert {
    condition     = one(one(google_cloud_run_v2_job.coordinator[0].template).template).timeout == "600s"
    error_message = "the sweep's own deadline is 600s, above the 8 minutes internal/cli stops a tick at"
  }
  assert {
    condition = (toset([for e in one(one(one(google_cloud_run_v2_job.coordinator[0].template).template).containers).env : e.name if length(coalesce(e.value_source, [])) == 0]) == toset([
      "FUGARO_PROJECT", "FUGARO_FIREBASE_PROJECT", "FUGARO_RTDB_URL", "FUGARO_RUNS_BUCKET",
      "FUGARO_COORDINATOR_REPOS", "FUGARO_GITHUB_APP_ID",
    ]))
    error_message = "the job's whole configuration is those six environment variables and the App key's reference, and no other entry"
  }
  assert {
    condition = (toset([for e in one(one(one(google_cloud_run_v2_job.coordinator[0].template).template).containers).env : "${e.name}=${e.value}" if length(coalesce(e.value_source, [])) == 0]) == toset([
      "FUGARO_PROJECT=aurora", "FUGARO_FIREBASE_PROJECT=fp-1234",
      "FUGARO_RTDB_URL=https://fp-1234-default-rtdb.firebaseio.com",
      "FUGARO_RUNS_BUCKET=fugaro-runs-proj-1234", "FUGARO_GITHUB_APP_ID=123456789",
      "FUGARO_COORDINATOR_REPOS=[{\"provider\":\"github\",\"repo\":\"acme/webapp\"},{\"provider\":\"bitbucket\",\"repo\":\"acme/sandbox\"}]",
    ]))
    error_message = "the six plain entries carry the project, the FP, the database, the runs bucket, the App's ID and the repositories one tick reconciles"
  }
  assert {
    condition = (length([for e in one(one(one(google_cloud_run_v2_job.coordinator[0].template).template).containers).env : e if length(coalesce(e.value_source, [])) == 1]) == 1 &&
    one([for e in one(one(one(google_cloud_run_v2_job.coordinator[0].template).template).containers).env : e if length(coalesce(e.value_source, [])) == 1]).name == "FUGARO_GITHUB_APP_PRIVATE_KEY")
    error_message = "the App's private key is the one entry taken from a source, so no plan and no job describe carries it"
  }
  assert {
    condition = (google_secret_manager_secret_iam_member.coordinator_app_key[0].secret_id == var.project_secrets["github-app-key"] &&
      google_secret_manager_secret_iam_member.coordinator_app_key[0].role == "roles/secretmanager.secretAccessor" &&
    google_secret_manager_secret_iam_member.coordinator_app_key[0].member == "serviceAccount:fugaro-coordinator@proj-1234.iam.gserviceaccount.com")
    error_message = "the App key's grant is the accessor on that one container, to the coordinator account and nobody else"
  }
  assert {
    condition = (google_cloud_run_v2_job_iam_member.coordinator_runner[0].role == "projects/proj-1234/roles/fugaroJobRunner" &&
    google_cloud_run_v2_job_iam_member.coordinator_runner[0].member == "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com")
    error_message = "the scheduler account may run this job and no other identity may: fugaroJobRunner on the job alone, never roles/run.invoker on the project"
  }
  assert {
    condition     = google_cloud_scheduler_job.coordinator_sweep[0].schedule == "*/15 * * * *" && one(google_cloud_scheduler_job.coordinator_sweep[0].retry_config).retry_count == 0
    error_message = "the sweep ticks every 15 minutes and is not retried: a failed tick logs its failure and the next tick is the retry"
  }
  assert {
    condition     = google_cloud_scheduler_job.coordinator_sweep[0].http_target[0].uri == "https://run.googleapis.com/v2/projects/proj-1234/locations/us-east5/jobs/fugarocoord:run"
    error_message = "the Scheduler job starts the coordinator job through the Cloud Run Admin API, naming the job's own region"
  }
  assert {
    condition     = output.coordinator_job == "fugarocoord"
    error_message = "the coordinator job's name is an output"
  }
}

run "coordinator_deploy_needs_firebase_outputs" {
  command = plan

  module {
    source = "../../modules/installation"
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
    coordinator = {
      account_id       = "fugaro-coordinator"
      job              = "fugarocoord"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-coordinator-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      github_app_id    = "123456789"
      repos            = [{ provider = "github", repo = "acme/webapp" }]
    }
  }

  expect_failures = [var.coordinator]
}

run "coordinator_job_name_must_not_be_a_workflow_job" {
  command = plan

  module {
    source = "../../modules/installation"
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
    coordinator = {
      account_id       = "fugaro-coordinator"
      job              = "fugaro-coordinator"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-coordinator-sweep"
      scheduler_region = "us-east1"
    }
  }

  expect_failures = [var.coordinator]
}

run "coordinator_job_needs_a_repository" {
  command = plan

  module {
    source = "../../modules/installation"
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
    coordinator = {
      account_id       = "fugaro-coordinator"
      job              = "fugarocoord"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-coordinator-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
      github_app_id    = "123456789"
    }
  }

  expect_failures = [var.coordinator]
}

run "coordinator_app_key_must_be_a_container_this_run_creates" {
  command = plan

  module {
    source = "../../modules/installation"
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
    coordinator = {
      account_id       = "fugaro-coordinator"
      job              = "fugarocoord"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-coordinator-sweep"
      scheduler_region = "us-east1"
      deploy_job       = true
      firebase_project = "fp-1234"
      rtdb_url         = "https://fp-1234-default-rtdb.firebaseio.com"
      github_app_id    = "123456789"
      repos            = [{ provider = "github", repo = "acme/webapp" }]
    }
  }

  expect_failures = [google_secret_manager_secret_iam_member.coordinator_app_key]
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
# build-submitter custom roles and the whole-project inventory's own Cloud Asset
# read role (fugaroCloudAssetReader, holding cloudasset.assets.searchAllResources
# alone, Task 4.1a), the one service account with a project role is
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
    error_message = "the installation grants people only the fugaroLauncher and fugaroBuildSubmitter custom roles on the project"
  }
  assert {
    condition     = alltrue([for m in google_project_iam_member.operator_cloud_asset_reader : m.role == "projects/proj-1234/roles/fugaroCloudAssetReader" && !startswith(m.member, "serviceAccount:")])
    error_message = "the operators hold the whole-project inventory's read role, this installation's own fugaroCloudAssetReader, and no service account holds it through this resource"
  }
  assert {
    condition     = keys(google_project_iam_member.operator_cloud_asset_reader) == ["user:operator@example.com"]
    error_message = "the whole-project inventory's read role is granted to the operators, and to nobody else"
  }
  assert {
    condition     = google_project_iam_custom_role.cloud_asset_reader.role_id == "fugaroCloudAssetReader" && google_project_iam_custom_role.cloud_asset_reader.title == "Fugaro Cloud Asset reader"
    error_message = "the whole-project inventory's read role is this installation's own custom role, not a predefined one"
  }
  assert {
    condition     = google_project_iam_custom_role.cloud_asset_reader.permissions == toset(["cloudasset.assets.searchAllResources"])
    error_message = "fugaroCloudAssetReader holds exactly cloudasset.assets.searchAllResources, the one permission the read's SearchAllResources call needs, and nothing else"
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
      [for r in [google_project_iam_custom_role.launcher, google_project_iam_custom_role.job_runner, google_project_iam_custom_role.build_submitter, google_project_iam_custom_role.tag_mover, google_project_iam_custom_role.cloud_asset_reader, google_project_iam_custom_role.history[0]] :
      [for p in r.permissions : !can(regex("^(firebase|datastore|identitytoolkit|iam\\.|resourcemanager|serviceusage|apikeys)", p))]],
    ]))
    error_message = "no installation custom role holds a permission on the budget backend, identities or IAM"
  }
}

# The project-wide secret containers (design docs/design/project-secrets.md
# §4). What this run pins is the shape a config edit must not quietly change:
# the labels discovery adopts one by, the ID the plan creates or imports it
# at, automatic replication (any other replication would replace an adopted
# container, deleting every version first), and the operator grant -- an
# operator stores versions and reads metadata, so secretVersionAdder and
# viewer, never secretAccessor: the value is readable by the jobs that mount
# it, not by the people who store it.
run "project_secrets" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    project_secrets = {
      "anthropic-api-key" = "fugaro-aurora-anthropic-api-key-0f0f0f0f0f0f0f0f"
      "bitbucket-token"   = "fugaro-aurora-bitbucket-token-1e1e1e1e1e1e1e1e"
    }
  }

  assert {
    condition     = length(google_secret_manager_secret.project) == length(var.project_secrets)
    error_message = "there must be exactly one container per project-wide input"
  }
  assert {
    condition = alltrue([for k, s in google_secret_manager_secret.project :
    s.labels == tomap({ fugaro = "managed", fugaro_project = "aurora", fugaro_secret = k })])
    error_message = "each project-wide container must carry fugaro=managed, fugaro_project and its own fugaro_secret, the labels fugaro secrets set checks before adopting it"
  }
  assert {
    condition     = tomap({ for k, s in google_secret_manager_secret.project : k => s.secret_id }) == var.project_secrets
    error_message = "each container's secret_id must be the input ID (gcp.ProjectSecretID)"
  }
  assert {
    condition = alltrue([for s in google_secret_manager_secret.project :
    length(s.replication) == 1 && length(s.replication[0].auto) == 1])
    error_message = "every project-wide container must be replicated automatically"
  }
  assert {
    condition = toset([for m in google_secret_manager_secret_iam_member.operator : "${m.secret_id} ${m.role} ${m.member}"]) == toset(flatten([
      for k, id in var.project_secrets : [
        "${id} roles/secretmanager.secretVersionAdder user:operator@example.com",
        "${id} roles/secretmanager.viewer user:operator@example.com",
      ]
    ]))
    error_message = "an operator gets secretVersionAdder and viewer on each project-wide container, and nothing else"
  }
  assert {
    condition     = alltrue([for m in google_secret_manager_secret_iam_member.operator : m.role != "roles/secretmanager.secretAccessor"])
    error_message = "no operator may read a project-wide secret's value"
  }
}

# The shared project job's three grants, which the installation golden plan
# cannot show: its tfvars name no launcher, no operator and no Vertex profile,
# so the planned project module holds none of them and a change to any of the
# three passes every plan-level test in the tree. This run plans the module
# itself, the way the runs above it do, with a launcher, an operator and a
# Vertex profile set.
#
# What each grant is: the launchers may start THIS job with overrides
# (fugaroJobRunner, the same role and permission the per-repository path
# grants); the operators may act as the job's account, which deploying the job
# needs; and a Vertex profile's job account may call aiplatform. None of the
# three is a project-level widening — the launcher's role is on the job, and
# the actAs is on the account.
run "project_job_grants" {
  command = plan

  module {
    source = "../../modules/project"
  }

  variables {
    project         = "proj-1234"
    fugaro_project  = "aurora"
    region          = "us-east5"
    runs_bucket     = "fugaro-runs-proj-1234"
    job_runner_role = "projects/proj-1234/roles/fugaroJobRunner"
    launchers       = ["user:launcher@example.com"]
    operators       = ["user:operator@example.com"]
    secrets         = {}
    job = {
      profile  = "standard"
      provider = "github"
      account  = ""
      job      = "fugaro-p-standard-github-0ca96e64b636"
      service_account = {
        account_id   = "fugaro-p-standard-git-0ca96e64"
        display_name = "Fugaro project job standard github"
      }
      image          = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc"
      cpu            = "2"
      memory         = "8Gi"
      task_timeout_s = 21720
      # FUGARO_GITHUB_APP_ID: a github shared job's precondition (job.tf,
      # owner ruling 2026-10-17) refuses the plan without it.
      env = {
        FUGARO_BUCKET        = "gs://fugaro-runs-proj-1234"
        FUGARO_GCP_PROJECT   = "proj-1234"
        FUGARO_GITHUB_APP_ID = "123456789"
        FUGARO_PROJECT       = "aurora"
        FUGARO_REGION        = "us-east5"
      }
      secret_env = {}
      bucket_condition = {
        title      = "fugaro-fugaro-p-standard-git-0ca96e64"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/cache/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/locks/\")"
      }
      token_condition = {
        title      = "fugaro-project-tokens-fugaro-p-standard-git-0ca96e64"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
      }
      vertex = true
    }
  }

  assert {
    condition = (keys(google_cloud_run_v2_job_iam_member.launcher) == ["user:launcher@example.com"] &&
      google_cloud_run_v2_job_iam_member.launcher["user:launcher@example.com"].role == "projects/proj-1234/roles/fugaroJobRunner" &&
      google_cloud_run_v2_job_iam_member.launcher["user:launcher@example.com"].member == "user:launcher@example.com" &&
    google_cloud_run_v2_job_iam_member.launcher["user:launcher@example.com"].name == "fugaro-p-standard-github-0ca96e64b636")
    error_message = "each launcher gets fugaroJobRunner on this job alone, and never on the project"
  }
  # The grants below are written from this account's computed member, so the
  # mock provider has to give it one during the plan for them to be readable.
  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-p-standard-git-0ca96e64@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-p-standard-git-0ca96e64@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-p-standard-git-0ca96e64@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = google_service_account.job.account_id == "fugaro-p-standard-git-0ca96e64"
    error_message = "the job's own account is the one the profile names, so the grants below can be read against it"
  }
  assert {
    condition = (keys(google_service_account_iam_member.operator) == ["user:operator@example.com"] &&
      google_service_account_iam_member.operator["user:operator@example.com"].role == "roles/iam.serviceAccountUser" &&
      google_service_account_iam_member.operator["user:operator@example.com"].member == "user:operator@example.com" &&
    google_service_account_iam_member.operator["user:operator@example.com"].service_account_id == google_service_account.job.name)
    error_message = "each operator may act as this job's own account, which deploying the job needs"
  }
  assert {
    condition = (length(google_project_iam_member.vertex) == 1 &&
      google_project_iam_member.vertex[0].role == "roles/aiplatform.user" &&
    google_project_iam_member.vertex[0].member == google_service_account.job.member)
    error_message = "a Vertex profile's job account, and only it, may call aiplatform"
  }
}

# The managed deployer's seven conditioned roles and their seven conditioned
# bindings are gated off by default (0.10.0: the fmgd- fence is one condition per
# service and has not been measured live yet; deployer.tf's header, Check 68 of
# docs/gcp-live-checklist.md). By default none of the fourteen exists, while the
# account, the unconditioned role and its binding stay.
run "deployer_scoped_off_by_default" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition = alltrue([
      for r in [
        google_project_iam_custom_role.fugaro_deployer_storage,
        google_project_iam_custom_role.fugaro_deployer_secret,
        google_project_iam_custom_role.fugaro_deployer_account,
        google_project_iam_custom_role.fugaro_deployer_apikey,
        google_project_iam_custom_role.fugaro_deployer_repository,
        google_project_iam_custom_role.fugaro_deployer_run_job,
        google_project_iam_custom_role.fugaro_deployer_scheduler_job,
      ] : length(r) == 0
    ])
    error_message = "the seven conditioned deployer roles must not exist unless managed_deployer_scoped is set"
  }
  assert {
    condition = alltrue([
      for m in [
        google_project_iam_member.fugaro_deployer_storage,
        google_project_iam_member.fugaro_deployer_secret,
        google_project_iam_member.fugaro_deployer_account,
        google_project_iam_member.fugaro_deployer_apikey,
        google_project_iam_member.fugaro_deployer_repository,
        google_project_iam_member.fugaro_deployer_run_job,
        google_project_iam_member.fugaro_deployer_scheduler_job,
      ] : length(m) == 0
    ])
    error_message = "the seven conditioned deployer bindings must not exist unless managed_deployer_scoped is set"
  }
  assert {
    condition     = google_service_account.fugaro_deployer.account_id == "fugaro-deployer" && google_project_iam_custom_role.fugaro_deployer.role_id == "fugaroDeployer"
    error_message = "the deployer account and its unconditioned role are created whatever the gate says"
  }
  assert {
    condition     = google_project_iam_member.fugaro_deployer.role == "projects/proj-1234/roles/fugaroDeployer" && length(google_project_iam_member.fugaro_deployer.condition) == 0
    error_message = "binding (1) is the unconditioned role, with no condition"
  }
}

# The opt-in plans the gated half exactly as the 0.10.0 redesign writes it: one
# role per service, each holding that service's slice of D75's 31 permissions
# (cloudscheduler.jobs.enable, never the .resume that does not exist), each
# bound exactly once under its own service's fence and no other service's.
run "deployer_scoped_opt_in" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    managed_deployer_scoped = true
  }

  assert {
    condition = [
      google_project_iam_custom_role.fugaro_deployer_storage[0].role_id,
      google_project_iam_custom_role.fugaro_deployer_secret[0].role_id,
      google_project_iam_custom_role.fugaro_deployer_account[0].role_id,
      google_project_iam_custom_role.fugaro_deployer_apikey[0].role_id,
      google_project_iam_custom_role.fugaro_deployer_repository[0].role_id,
      google_project_iam_custom_role.fugaro_deployer_run_job[0].role_id,
      google_project_iam_custom_role.fugaro_deployer_scheduler_job[0].role_id,
      ] == [
      "fugaroDeployerStorage",
      "fugaroDeployerSecret",
      "fugaroDeployerAccount",
      "fugaroDeployerApiKey",
      "fugaroDeployerRepository",
      "fugaroDeployerRunJob",
      "fugaroDeployerSchedulerJob",
    ]
    error_message = "with managed_deployer_scoped the seven conditioned roles exist, one per service"
  }
  assert {
    condition = sum([
      for r in [
        google_project_iam_custom_role.fugaro_deployer_storage,
        google_project_iam_custom_role.fugaro_deployer_secret,
        google_project_iam_custom_role.fugaro_deployer_account,
        google_project_iam_custom_role.fugaro_deployer_apikey,
        google_project_iam_custom_role.fugaro_deployer_repository,
        google_project_iam_custom_role.fugaro_deployer_run_job,
        google_project_iam_custom_role.fugaro_deployer_scheduler_job,
      ] : length(r[0].permissions)
    ]) == 31
    error_message = "the seven roles partition D75's 31 conditioned permissions, none added and none dropped (9 storage, 3 secret, 4 account, 2 apikey, 3 repository, 5 run, 5 scheduler)"
  }
  assert {
    condition     = contains(google_project_iam_custom_role.fugaro_deployer_scheduler_job[0].permissions, "cloudscheduler.jobs.enable") && !contains(google_project_iam_custom_role.fugaro_deployer_scheduler_job[0].permissions, "cloudscheduler.jobs.resume")
    error_message = "the scoped role resumes a Scheduler job with cloudscheduler.jobs.enable; cloudscheduler.jobs.resume does not exist"
  }
  assert {
    condition = alltrue([
      for b in [
        [google_project_iam_member.fugaro_deployer_storage[0], "fugaroDeployerStorage", "storage.googleapis.com/Bucket"],
        [google_project_iam_member.fugaro_deployer_secret[0], "fugaroDeployerSecret", "secretmanager.googleapis.com/Secret"],
        [google_project_iam_member.fugaro_deployer_account[0], "fugaroDeployerAccount", "iam.googleapis.com/ServiceAccount"],
        [google_project_iam_member.fugaro_deployer_apikey[0], "fugaroDeployerApiKey", "apikeys.googleapis.com/Key"],
        [google_project_iam_member.fugaro_deployer_repository[0], "fugaroDeployerRepository", "artifactregistry.googleapis.com/Repository"],
        [google_project_iam_member.fugaro_deployer_run_job[0], "fugaroDeployerRunJob", "run.googleapis.com/Job"],
        [google_project_iam_member.fugaro_deployer_scheduler_job[0], "fugaroDeployerSchedulerJob", "cloudscheduler.googleapis.com/Job"],
      ] :
      b[0].role == "projects/proj-1234/roles/${b[1]}" &&
      b[0].condition[0].title == "fugaro-managed-prefix" &&
      strcontains(b[0].condition[0].expression, b[2]) &&
      strcontains(b[0].condition[0].expression, "fmgd-") &&
      !strcontains(b[0].condition[0].expression, "contains(")
    ])
    error_message = "each conditioned deployer role is bound exactly once, under its own service's fmgd- fence and no other's"
  }
}

# The credentials service is OFF by default (D147): with
# var.credentials_service null this module creates nothing at all, so an
# installation that has not opted in plans exactly what it planned before
# C1.18 existed — no service, no account, no container, no grant.
run "credentials_service_off_by_default" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  assert {
    condition     = length(google_cloud_run_v2_service.credentials) == 0 && length(google_service_account.credentials) == 0
    error_message = "the credentials service and its account must not exist unless credentials_service is set (D147)"
  }
  assert {
    condition     = length(google_secret_manager_secret.credentials_bundle) == 0 && length(google_secret_manager_secret_iam_member.credentials_bundle) == 0
    error_message = "the bundle container and its one accessor grant must not exist unless credentials_service is set"
  }
  assert {
    condition     = length(google_project_iam_custom_role.credentials_database) == 0 && length(google_project_iam_member.credentials_database) == 0
    error_message = "the database role and its binding must not exist unless credentials_service is set: without the service's account there is nobody to bind them to"
  }
}

# The opt-in (C1.18, and the owner's Q6 ruling of 2026-10-19). This run is the
# whole security claim of the task, so it says what it pins:
#
#   * ONE secret container — the bundle — and ONE accessor grant on it, to the
#     service's own account, and the grant carries NO condition: no
#     secretmanager.googleapis.com/secretId filter and no name-prefix fence,
#     which is what Q6 replaced. A condition added here fails this run.
#   * The database role and its one binding — the service's second and last
#     grant in this module (ledger #903). RTDB reads were denied without it;
#     it holds firebasedatabase.instances.get and .update and nothing else,
#     and it is bound on the FIREBASE project (var.credentials_service
#     .firebase_project — aurora-fp in this run), which is where the database
#     lives and which this run sets apart from the installation project
#     (proj-1234), so the account holds no project-level grant in the
#     installation project here. The assertions below pin the permission set
#     exactly, the binding's literal role path (a custom role's name is not
#     known at plan time) and the absence of a condition.
#   * The service's account holds nothing ELSE in this module: not a member of
#     the project's other secret containers (the run sets two of them, the two
#     model credentials, so the assertion has something to be false about),
#     nothing on the runs bucket, and no grant here that would let anyone act
#     as it. What this run cannot see is a grant written in another file — a
#     per-secret grant to the service's account added to secrets.tf, say, is
#     not a resource of this module and no assertion here can reach it. That
#     direction is the Go scan's: deploy/terraform's
#     TestCredentialsServiceGrantsAreTheOneBundle reads credentials.tf, and
#     internal/infra/managed's TestRunAccountsHaveNoWritePathTo… walks every
#     module's IAM resources. (Independent review n1: this comment used to
#     claim the run guarded that too, which it does not.)
#   * The service requires IAM: nothing in this module grants invoker to
#     anyone, and the container reads no credential from its environment.
run "credentials_service_on" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    # Two project-wide model-credential containers, so "the service's account
    # holds no member over any other secret" has something to be false about.
    project_secrets = {
      "claude-oauth-token" = "fugaro-project-aurora-claude-oauth-token-3f1c0a9d2b7e4451"
      "anthropic-api-key"  = "fugaro-project-aurora-anthropic-api-key-8c2d5f6a1e9b3074"
    }
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials:0.15.0@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
      url = "https://fugaro-credentials-123456789012.us-east5.run.app"
    }
  }

  # The grants below are written from the account's computed member, so the
  # mock provider has to give it one during the plan for them to be readable.
  override_resource {
    target          = google_service_account.credentials[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-credentials@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-credentials@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-credentials@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = google_service_account.credentials[0].account_id == "fugaro-credentials"
    error_message = "the service's account is the one credentials_service.account_id names, and the grants below are read against it"
  }
  assert {
    condition = (length(google_secret_manager_secret.credentials_bundle) == 1 &&
      google_secret_manager_secret.credentials_bundle[0].secret_id == "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358" &&
    google_secret_manager_secret.credentials_bundle[0].project == "proj-1234")
    error_message = "the bundle container's Secret Manager ID must be the one the variable carries — the name the loader reads — in this project"
  }
  assert {
    condition = google_secret_manager_secret.credentials_bundle[0].labels == tomap({
      fugaro         = "managed"
      fugaro_project = "aurora"
    })
    error_message = "the bundle container carries fugaro=managed and the installation's name, and deliberately no fugaro_secret: that label is what discovery reads to decide which logical secret of the project's set a container holds, and this one is not one of them"
  }
  assert {
    condition = (length(google_secret_manager_secret.credentials_bundle[0].replication) == 1 &&
    length(google_secret_manager_secret.credentials_bundle[0].replication[0].auto) == 1)
    error_message = "the bundle container is replicated automatically, like every container the CLI adopts"
  }
  assert {
    condition = (length(google_secret_manager_secret_iam_member.credentials_bundle) == 1 &&
      google_secret_manager_secret_iam_member.credentials_bundle[0].role == "roles/secretmanager.secretAccessor" &&
      google_secret_manager_secret_iam_member.credentials_bundle[0].secret_id == google_secret_manager_secret.credentials_bundle[0].secret_id &&
    google_secret_manager_secret_iam_member.credentials_bundle[0].member == google_service_account.credentials[0].member)
    error_message = "Q6: the service's account holds roles/secretmanager.secretAccessor on the one bundle container, and on nothing else"
  }
  assert {
    condition     = length(google_secret_manager_secret_iam_member.credentials_bundle[0].condition) == 0
    error_message = "Q6: the bundle grant carries NO condition — a secretId filter or a name-prefix fence is exactly what the ruling replaced"
  }
  assert {
    condition = length([
      for k, g in google_secret_manager_secret_iam_member.operator : k
      if g.member == google_service_account.credentials[0].member
    ]) == 0
    error_message = "the service's account must hold no member over the project's other secret containers: one container, one grant (Q6)"
  }
  assert {
    condition = length([
      for k, g in google_storage_bucket_iam_member.runs : k
      if g.member == google_service_account.credentials[0].member
    ]) == 0
    error_message = "the service's account must hold nothing on the runs bucket"
  }
  # The database role (ledger #903). The service reads the run's outcome, the
  # stop switches, the policy and its own creds/ state out of the installation's
  # RTDB, and writes its own creds/ nodes by compare-and-set; without this role
  # every one of those calls is denied in production. RTDB permissions have no
  # path scope, so the choice is this pair or a predefined role that can do
  # more (viewer adds list, admin adds the whole instance lifecycle, including
  # delete), and get alone would leave every write denied.
  assert {
    condition = (length(google_project_iam_custom_role.credentials_database) == 1 &&
      google_project_iam_custom_role.credentials_database[0].role_id == "fugaroCredentialsDatabase" &&
      google_project_iam_custom_role.credentials_database[0].project == "aurora-fp" &&
      google_project_iam_custom_role.credentials_database[0].permissions == toset([
        "firebasedatabase.instances.get", "firebasedatabase.instances.update",
    ]))
    error_message = "the service's database role is this installation's own fugaroCredentialsDatabase, on the Firebase project the variable names, holding exactly firebasedatabase.instances.get and .update: get for every read the service makes, update for its compare-and-set writes under creds/, and no instance lifecycle permission (no list, create, delete, disable, reenable or undelete)"
  }
  assert {
    condition = (length(google_project_iam_member.credentials_database) == 1 &&
      google_project_iam_member.credentials_database[0].role == "projects/aurora-fp/roles/fugaroCredentialsDatabase" &&
      google_project_iam_member.credentials_database[0].project == "aurora-fp" &&
      google_project_iam_member.credentials_database[0].member == google_service_account.credentials[0].member &&
    length(google_project_iam_member.credentials_database[0].condition) == 0)
    error_message = "the database role is bound once, to the service's own account, on the Firebase project (not the installation project), with the literal role path a custom role needs at plan time and with no condition (a per-path fence is not expressible: RTDB IAM has no path scope, so the role's permission pair is the whole fence)"
  }
  assert {
    condition = length(concat([
      for k, g in google_service_account_iam_member.scheduler_user : k
      if g.member == google_service_account.credentials[0].member || g.service_account_id == google_service_account.credentials[0].name
      ], [
      for k, g in google_service_account_iam_member.fugaro_deployer_token_creator : k
      if g.member == google_service_account.credentials[0].member || g.service_account_id == google_service_account.credentials[0].name
    ])) == 0
    error_message = "nobody may act as the service's account: anyone who could deploy a revision as it could read every model credential the installation holds"
  }
  assert {
    condition = (length(google_cloud_run_v2_service.credentials) == 1 &&
      google_cloud_run_v2_service.credentials[0].name == "fugaro-credentials" &&
      google_cloud_run_v2_service.credentials[0].location == "us-east5" &&
    google_cloud_run_v2_service.credentials[0].project == "proj-1234")
    error_message = "the service is the one credentials_service names, in the installation's region"
  }
  assert {
    condition = (google_cloud_run_v2_service.credentials[0].template[0].service_account == google_service_account.credentials[0].email &&
      google_cloud_run_v2_service.credentials[0].template[0].containers[0].image == "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials:0.15.0@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3" &&
    google_cloud_run_v2_service.credentials[0].template[0].timeout == "3600s")
    error_message = "the service runs as its own account, on the image the variable carries, with the longest timeout Cloud Run allows for a streaming proxy"
  }
  # This run's image is 0.15.0, the first release whose process serves HTTP/2
  # cleartext (internal/cli/credentials_serve.go), so the container's port is
  # NAMED h2c — the name is the whole of what makes Cloud Run speak HTTP/2 to
  # the process instead of HTTP/1.1, which is the hop the plan's B1.0 is for:
  # it is conditional on the owner's Check 190 failing a large push, and an
  # unnamed port is the HTTP/1 path whose body limit that check exercises.
  # Nothing here is a measurement of it — the checklist still records Check 190
  # as not run, and the run further down is the image the gate keeps unnamed.
  # The number is the port the process itself falls back to when the platform's
  # PORT is unset (servePort) and Cloud Run's own default, so the name and the
  # listener cannot point at different ports. The release this run pins and the
  # one the run below pins are the pair the module's own threshold is written
  # from (credentials.tf's credentials_image_serves_h2c): a release that moves
  # the first h2c-serving version moves this image, that one and the threshold
  # together.
  assert {
    condition = (length(google_cloud_run_v2_service.credentials[0].template[0].containers[0].ports) == 1 &&
      google_cloud_run_v2_service.credentials[0].template[0].containers[0].ports[0].name == "h2c" &&
    google_cloud_run_v2_service.credentials[0].template[0].containers[0].ports[0].container_port == 8080)
    error_message = "an image whose release serves h2c must have its container port named h2c and numbered 8080: the name is what makes the front end speak HTTP/2 to this process"
  }
  # The environment is the seven names C1.14's `fugaro credentials serve`
  # reads and internal/infra/tf's credentialsServiceEnv allows, and it is held
  # as an exact map: an eighth entry, a missing one, a name the process does
  # not read, or a value that is not the installation's own fails here. What
  # the service does with a value (a database URL that is not the Firebase
  # project's, an audience that is not a URL, a job table that binds nothing)
  # is refused by the process itself, by name, and is C1.14's tests.
  assert {
    condition = tomap({
      for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.name => e.value
      }) == tomap({
      FUGARO_PROJECT                 = "aurora"
      FUGARO_GCP_PROJECT             = "proj-1234"
      FUGARO_REGION                  = "us-east5"
      FUGARO_RTDB_URL                = "https://aurora-fp-default-rtdb.firebaseio.com"
      FUGARO_FIREBASE_PROJECT        = "aurora-fp"
      FUGARO_CREDENTIALS_SERVICE_URL = "https://fugaro-credentials-123456789012.us-east5.run.app"
      FUGARO_JOB_ACCOUNTS            = "[{\"provider\":\"github\",\"repo\":\"acme/webapp\",\"workflow\":\"web\"}]"
    })
    error_message = "the container's environment is the seven names the service reads and nothing else: no secret ID, no credential value, and no eighth entry"
  }
  # The service's own URL is the audience its callers' ID tokens are minted
  # for, and it is the one entry the module cannot fill on the apply that
  # CREATES the service: Cloud Run assigns it, and no expression inside this
  # resource can read its own uri (Terraform refuses a self-referential
  # block), so the variable carries it and the module's output reads it back
  # for the apply that follows in the same init stage. Unset, the name carries
  # the module's unassigned placeholder instead (an .invalid host, which no ID
  # token is ever minted for) so the first revision starts and admits nobody;
  # the run below this one is the unset case.
  # The job table is the one name whose value is a document, and it is the
  # module's own jsonencode of the triples: the service derives the job
  # accounts from them (credsvc.JobAccountsFor) rather than reading emails,
  # because the account's name and the repository's slug are both hashes of
  # these three fields.
  assert {
    condition = jsondecode([
      for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.value
      if e.name == "FUGARO_JOB_ACCOUNTS"
      ][0]) == [{
      provider = "github"
      repo     = "acme/webapp"
      workflow = "web"
    }]
    error_message = "the job table must be the variable's triples as JSON, which is what the service decodes (all three fields present, no others)"
  }
  assert {
    condition = (length(google_cloud_run_v2_service.credentials[0].template[0].containers) == 1 &&
    google_cloud_run_v2_service.credentials[0].template[0].containers[0].resources[0].limits == tomap({ cpu = "1", memory = "512Mi" }))
    error_message = "the service runs one container with the pinned resources"
  }
  assert {
    condition = (length(google_cloud_run_v2_service.credentials[0].traffic) == 1 &&
      google_cloud_run_v2_service.credentials[0].traffic[0].percent == 100 &&
    google_cloud_run_v2_service.credentials[0].traffic[0].type == "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST")
    error_message = "all traffic goes to the latest revision"
  }
  assert {
    condition     = google_cloud_run_v2_service.credentials[0].ingress == "INGRESS_TRAFFIC_ALL"
    error_message = "the ingress is ALL because the caller is a Cloud Run job of this project, which reaches the service over its public run.app URL; the fence is the IAM check, not the network"
  }
  assert {
    condition     = google_cloud_run_v2_service.credentials[0].invoker_iam_disabled == false
    error_message = "the service must require IAM: invoker_iam_disabled false is stated rather than implied, so a change of the provider's default cannot open the service silently"
  }
  assert {
    condition     = google_cloud_run_v2_service.credentials[0].deletion_protection == false
    error_message = "the service holds no data of its own: the bundle container is what must survive, and it carries prevent_destroy"
  }
  assert {
    condition = google_cloud_run_v2_service.credentials[0].labels == tomap({
      fugaro      = "managed"
      fugaro_role = "credentials"
    })
    error_message = "the service carries fugaro=managed and its own role label"
  }
  assert {
    condition = length([
      for g in google_project_iam_member.launcher : g.member
      if g.member == google_service_account.credentials[0].member
    ]) == 0
    error_message = "the service's account holds no project-level role: it reads one secret and nothing else"
  }
}

# The first apply of an installation that has just opted in: the tfvars carry
# no credentials_service.url yet, because the URL does not exist until this
# apply creates the service (and Terraform refuses a resource that reads its
# own uri). The audience must still be a name the service takes, and a value
# no caller's ID token can carry — the placeholder is under the reserved
# .invalid TLD, so the first revision starts, is healthy, and admits nobody
# until the init stage reads the real URL back from this module's output and
# applies again. An empty value here would be the deadlock the local's comment
# describes; a run.app-shaped guess would be a service that trusts a token
# minted for someone else's.
run "credentials_service_first_apply_has_no_audience_yet" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
    }
  }

  assert {
    condition = tomap({
      for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.name => e.value
    })["FUGARO_CREDENTIALS_SERVICE_URL"] == "https://credentials-service-url-unassigned.invalid"
    error_message = "with no credentials_service.url the audience is the module's unassigned placeholder, so the first revision starts without admitting a caller rather than failing the apply that would have produced the URL"
  }
  assert {
    condition     = !strcontains(tomap({ for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.name => e.value })["FUGARO_CREDENTIALS_SERVICE_URL"], "run.app")
    error_message = "the placeholder must not look like a Cloud Run URL: a run.app-shaped audience would be a guess at another service's name"
  }
  assert {
    condition     = length(google_cloud_run_v2_service.credentials[0].template[0].containers[0].env) == 7
    error_message = "the unset case sets the same seven names, the audience among them: a name the module drops is a service the process refuses by a different message"
  }
}

# The port's name follows the image, and this is the other direction of that
# pairing (B1.0): a front end told h2c in front of a process that answers
# HTTP/1.1 only serves nothing at all, so an image that is not one of the
# releases whose server speaks h2c gets no port at all — Cloud Run's own
# HTTP/1.1, which is what the service answers on today.
#
# It is not a hypothetical image. The release's own image is not the only one
# this root can be planned with: an installation whose owner is still on an
# older CLI carries that release's reference forward
# (infra.CarriedCredentialsService), and an operator's own tfvars may name any
# digest they like — 0.14.0's process, before this release re-pinned anything,
# is that shape with no name in front of it. (This release's own update
# re-resolves and re-pins the image for an installation that opted in — the
# credentials stage when the service is switched on, init_stages.go's
# pinCredentialsImage on an ordinary run — so the plan it makes pairs the newer
# image with the name in the run above; this one is the other direction, and it
# is the one that must keep working for a plan whose image is older.) An image
# reference the module cannot read a release out of (a hand-written digest, a
# version that is not three numbers) reads the same way, and so does the
# tagless image of the first-apply run above.
run "credentials_service_port_follows_the_image" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials:0.14.0@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
    }
  }

  assert {
    condition     = length(google_cloud_run_v2_service.credentials[0].template[0].containers[0].ports) == 0
    error_message = "an image whose release does not serve h2c must have no named port: Cloud Run reads the name as the protocol to speak, and h2c in front of an HTTP/1.1 process fails every call the installation makes"
  }
}

# The image is pinned by digest, and a tag is refused at plan time (C1.18,
# independent security review M1). Why this is a security rule and not a
# preference: the service runs as the account that reads every credential in
# the bundle, and modules/installation/iam.tf grants every operator
# roles/artifactregistry.writer on the installation's registry — so a mutable
# `fugaro-credentials:latest` would let an operator push the code that the
# next owner apply runs as that account. A digest cannot be rewritten. The
# run above is the digest form, planned and asserted; this one is the tag
# form, refused by the variable's validation before any resource is planned.
run "credentials_service_image_must_be_a_digest" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials:latest"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
    }
  }

  expect_failures = [var.credentials_service]
}

# A digest that is not 64 lower-case hex digits is not a digest: a truncated
# or upper-case one would not resolve, and `@sha256:` alone would pass a check
# that only looked for the marker. This is the second half of the same rule.
run "credentials_service_image_must_be_a_whole_digest" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
    }
  }

  expect_failures = [var.credentials_service]
}

# The four facts the process is configured with, held to their shape at plan
# time (modules/installation/variables.tf, the second validation). A stale or
# hand-edited terraform.tfvars.json is what that block exists to stop - the
# service reads every one of these from its environment and refuses to serve
# without them, so an installation that planned a bad one would halt every run
# that reached it (D143) while the plan reported success.
#
# The five runs below are the other half of the variable's two image runs
# above: each states one bad value and requires the plan to refuse it, so the
# guard cannot be weakened or deleted without a red test. The Go layer refuses
# the same shapes as they are derived (TestCredentialsRefusesWithoutTheFirebaseFacts),
# but that is the layer this file's inputs never pass through: a tfvars that
# was edited by hand, or written by an older fugaro, reaches Terraform without
# it.

# An empty job table is a service that binds no run to a repository: it admits
# nobody, which an operator would read as healthy while every run halted.
run "credentials_service_job_table_must_not_be_empty" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts     = []
    }
  }

  expect_failures = [var.credentials_service]
}

# A row with a field missing: the account's name is derived from the workflow
# and the repository's slug from the provider and the repository, so an empty
# field is a job whose account cannot be derived at all.
run "credentials_service_job_row_needs_all_three_fields" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = ""
      }]
    }
  }

  expect_failures = [var.credentials_service]
}

# The Firebase project is a project ID, not a display name and not the Fugaro
# project: it is the aud and iss a run's identity is verified against, and a
# revision configured with the wrong one refuses every caller.
run "credentials_service_firebase_project_must_be_a_project_id" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "Aurora Firebase"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
    }
  }

  expect_failures = [var.credentials_service]
}

# A database URL with a path is not the database: the readers are built on the
# URL itself, and a path silently reads the wrong tree (history.tf's own rule,
# restated here rather than trusted to whoever wrote the tfvars).
run "credentials_service_rtdb_url_must_be_a_whole_url" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com/runs"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
    }
  }

  expect_failures = [var.credentials_service]
}

# The audience, once the stage knows it: a non-empty URL that is not the
# https://<host> form Cloud Run serves on admits no ID token at all, so it is
# refused here rather than left to the revision that would silently admit
# nobody. (An ABSENT url is the other case and is admitted: the first apply
# writes credentials.tf's unassigned placeholder, run above.)
run "credentials_service_url_must_be_an_https_host" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = {
      account_id       = "fugaro-credentials"
      service          = "fugaro-credentials"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-credentials@sha256:9f2c7b1e4d6a8305c1b7e9f2a4d6c8b0e3f5a7c9d1b3e5f7a9c1e3d5b7f9a1c3"
      bundle_secret    = "fugaro-project-aurora-creds-bundle-6b0e9a4c7d21f358"
      firebase_project = "aurora-fp"
      rtdb_url         = "https://aurora-fp-default-rtdb.firebaseio.com"
      job_accounts = [{
        provider = "github"
        repo     = "acme/webapp"
        workflow = "web"
      }]
      url = "http://fugaro-credentials.us-east5.run.app"
    }
  }

  expect_failures = [var.credentials_service]
}
