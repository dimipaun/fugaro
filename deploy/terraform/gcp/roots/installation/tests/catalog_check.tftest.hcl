# The project's catalog check job (PIC4, D1148 task (ii)) against a mock
# provider: no credentials, no network beyond the provider download.
#
# What this file is for is the job's WHOLE grant surface, and it is the reason
# the job exists at all: a scheduled job that decides every entry of a
# project's image catalog and submits a build per entry that fires is a job
# that can move every repository's images, so the set of things its account may
# reach is the thing to read. The second run below pins that set as an exact
# set — the five grants this module makes (the sixth, the actAs that lets a
# build run as the entry's own repository's account, is the repository root's:
# roots/repo/tests/catalog_check.tftest.hcl) — and a sixth grant of any role,
# on any of these four types, to this account fails it.
#
# The names, the argv, the two IAM condition titles and the alert filter are
# the ones internal/infra writes (gcp.CatalogCheckJobName, gcp.CatalogRead-
# ConditionTitle, gcp.CatalogRecordsConditionTitle, the mode's argv): the
# run that asserts them is the run that would have to move if any of those
# changed, and deploy/terraform's own tests pair the titles with the functions
# that build them.

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
  alert_email = "ops@example.com"
}

run "no_catalog_check_job_without_one" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  # An installation whose tfvars carry no catalog_check is byte for byte the
  # installation it was: no account, no job, no grant, no alert. This is what
  # keeps the change unplannable for a project that publishes no catalog.
  assert {
    condition = (
      length(google_service_account.catalog_check) == 0 && length(google_cloud_run_v2_job.catalog_check) == 0 &&
      length(google_cloud_scheduler_job.catalog_check) == 0 &&
      length(google_project_iam_member.catalog_check_log_writer) == 0 && length(google_project_iam_member.catalog_check_build_submitter) == 0 &&
      length(google_storage_bucket_iam_member.catalog_check_definitions) == 0 &&
      length(google_storage_bucket_iam_member.catalog_check_records) == 0 &&
      length(google_cloud_run_v2_job_iam_member.catalog_check_invoker) == 0
    )
    error_message = "without catalog_check the installation must declare no catalog check resource at all"
  }
  assert {
    condition     = output.catalog_check_service_account == null && output.catalog_check_job == null
    error_message = "without catalog_check the two outputs must be null, which is what a repository root reads as `grant nobody actAs`"
  }
  assert {
    condition     = toset(keys(google_monitoring_alert_policy.image)) == toset(["check", "job"])
    error_message = "without catalog_check the alert policies must be the two image check ones, and no catalog check one"
  }
}

run "exact_grants" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.catalog_check[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target          = google_service_account.scheduler
    override_during = plan
    values = {
      email  = "fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    catalog_check = {
      account_id       = "fugaro-catalog-check"
      job              = "fugarochk-catalog-1a2b3c4d"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/base:0.18.0"
      scheduler_job    = "fugarochk-catalog"
      scheduler_region = "us-east5"
      schedule         = "17 4 * * *"
      paused           = true
      env = {
        FUGARO_PROJECT            = "aurora"
        FUGARO_GCP_PROJECT        = "proj-1234"
        FUGARO_REGION             = "us-east5"
        FUGARO_BUCKET             = "gs://fugaro-runs-proj-1234"
        FUGARO_CATALOG_CHECK_SPEC = "{\"version\":1,\"repositories\":[]}"
      }
      definitions_condition = {
        title      = "fugaro-catalog-read-fugaro-catalog-check"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/catalog/\")"
      }
      repositories = [{
        slug = "acme-app-0123456789abcdef"
        records_condition = {
          title      = "fugaro-catalog-records-acme-app-0123456789abcdef"
          expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/acme-app-0123456789abcdef/catalog/\")"
        }
      }]
    }
  }

  assert {
    condition = toset(concat(
      [for r in google_project_iam_member.catalog_check_log_writer : "google_project_iam_member ${r.role} ${r.member}"],
      [for r in google_project_iam_member.catalog_check_build_submitter : "google_project_iam_member ${r.role} ${r.member}"],
      [for r in google_storage_bucket_iam_member.catalog_check_definitions : "google_storage_bucket_iam_member ${r.role} ${one(r.condition).title} ${r.member}"],
      [for r in google_storage_bucket_iam_member.catalog_check_records : "google_storage_bucket_iam_member ${r.role} ${one(r.condition).title} ${r.member}"],
      [for r in google_cloud_run_v2_job_iam_member.catalog_check_invoker : "google_cloud_run_v2_job_iam_member ${r.role} ${r.member}"],
      )) == toset([
      "google_project_iam_member roles/logging.logWriter serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com",
      "google_project_iam_member projects/proj-1234/roles/fugaroBuildSubmitter serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com",
      "google_storage_bucket_iam_member roles/storage.objectViewer fugaro-catalog-read-fugaro-catalog-check serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com",
      "google_storage_bucket_iam_member roles/storage.objectUser fugaro-catalog-records-acme-app-0123456789abcdef serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com",
      "google_cloud_run_v2_job_iam_member roles/run.invoker serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com",
    ])
    error_message = "the catalog check job's grants must be exactly these five: its own log lines and the two build submissions on the project, the copied definitions read-only, one repository's build records and check state, and run.invoker on its own job for the scheduler account. A sixth grant — any role, on any of these four types — fails this assertion, and there is no secret accessor, no actAs and no key here: the actAs on a repository's build account is the repository root's grant, and the build reads the secrets."
  }

  assert {
    condition     = google_cloud_run_v2_job.catalog_check[0].template[0].template[0].containers[0].args == tolist(["image", "check", "--job", "--catalog"])
    error_message = "the job's argv must be the mode D1148 task (i) added: --catalog without --job is refused by the CLI, and vice versa"
  }
  assert {
    condition = (
      google_cloud_run_v2_job.catalog_check[0].template[0].template[0].service_account == google_service_account.catalog_check[0].email &&
      google_cloud_run_v2_job.catalog_check[0].template[0].template[0].service_account != "serviceAccount:${google_service_account.scheduler.email}"
    )
    error_message = "the job must run as its own account and never as the scheduler's or a build account's"
  }
  assert {
    condition     = length([for e in google_cloud_run_v2_job.catalog_check[0].template[0].template[0].containers[0].env : e.name if e.name == "FUGARO_CATALOG_CHECK_SPEC"]) == 1
    error_message = "the job's environment must carry the run's assignment: without it the mode refuses to start, so the job could only fail"
  }
  assert {
    condition = (
      google_cloud_scheduler_job.catalog_check[0].paused == true &&
      google_cloud_scheduler_job.catalog_check[0].http_target[0].oauth_token[0].service_account_email == google_service_account.scheduler.email
    )
    error_message = "the catalog check schedule must start paused (a first run would find no record anywhere and submit a build for every entry) and Scheduler must call it as the installation's scheduler account, which is the account the invoker grant names"
  }
  assert {
    condition     = one(one(google_monitoring_alert_policy.image["catalog_check"].conditions).condition_matched_log).filter == "resource.type=\"cloud_run_job\" AND resource.labels.job_name=~\"^fugarochk-\" AND jsonPayload.event=\"catalog-check\" AND severity>=ERROR"
    error_message = "the catalog check's alert must match the job's own event (task (i)'s catalog-check line) under the fugarochk- job-name pattern the image check alerts use"
  }
}

run "job_name_must_be_fugarochk" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    catalog_check = {
      account_id       = "fugaro-catalog-check"
      job              = "catalog-check"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/base:0.18.0"
      scheduler_job    = "fugarochk-catalog"
      scheduler_region = "us-east5"
      env = {
        FUGARO_CATALOG_CHECK_SPEC = "{\"version\":1}"
      }
      definitions_condition = {
        title      = "fugaro-catalog-read-fugaro-catalog-check"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/catalog/\")"
      }
      repositories = [{
        slug = "acme-app-0123456789abcdef"
        records_condition = {
          title      = "fugaro-catalog-records-acme-app-0123456789abcdef"
          expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/acme-app-0123456789abcdef/catalog/\")"
        }
      }]
    }
  }

  # The prefix is load-bearing both ways: fugaro runs ls and max_parallel count
  # the fugaro- jobs, so a fugaro-catalog-check job would be counted as a
  # repository's workflow job, and the alert policies match ^fugarochk-.
  expect_failures = [var.catalog_check]
}

run "env_must_carry_the_spec" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    catalog_check = {
      account_id       = "fugaro-catalog-check"
      job              = "fugarochk-catalog-1a2b3c4d"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/base:0.18.0"
      scheduler_job    = "fugarochk-catalog"
      scheduler_region = "us-east5"
      env = {
        FUGARO_PROJECT = "aurora"
      }
      definitions_condition = {
        title      = "fugaro-catalog-read-fugaro-catalog-check"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/catalog/\")"
      }
      repositories = [{
        slug = "acme-app-0123456789abcdef"
        records_condition = {
          title      = "fugaro-catalog-records-acme-app-0123456789abcdef"
          expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/builds/acme-app-0123456789abcdef/catalog/\")"
        }
      }]
    }
  }

  expect_failures = [var.catalog_check]
}
