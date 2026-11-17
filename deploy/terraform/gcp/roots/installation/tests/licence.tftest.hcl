# The licence renewal job (plan K4.2 of docs/licence-key-v2), planned inside the
# module: the account, the empty container, the four calls the job holds on that
# container alone, the two conditioned bucket grants and the schedule.
#
# The switch is the coordinator's own, so the first two runs are the pair the
# task turns on: a licence block on an installation whose coordinator job is not
# deployed plans NOTHING, and the block plus deploy_job plans the whole of it.
# The refusals after them are the module's own variable checks — the account ID
# the plan review reads as this job's kind of principal, the job name `fugaro
# runs ls` would count as a workflow job, and the two project names a container
# with no checkout has instead of a config file.

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
    title      = "fugaro-launchers-runs-tokens-issues"
    expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/issues/\")"
  }

  # The block `fugaro update` writes, name for name and byte for byte:
  # internal/infra.Licence's names and internal/backend/gcp's two condition
  # builders, which are the bytes the plan review compares (a grant whose
  # expression is not those is refused by internal/infra/tf).
  licence = {
    account_id       = "fugaro-licence"
    job              = "fugarolicence"
    image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
    scheduler_job    = "fugaro-licence-renew"
    scheduler_region = "us-east4"
    renewal_secret   = "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd"
    env = {
      FUGARO_PROJECT     = "aurora"
      FUGARO_GCP_PROJECT = "proj-1234"
      FUGARO_BUCKET      = "gs://fugaro-runs-proj-1234"
      FUGARO_REGION      = "us-east5"
      FUGARO_BACKEND     = "cloud-run"
    }
    read_condition = {
      title      = "fugaro-licence-read-fugaro-licence"
      expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/project.json\" || resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/config.yaml\" || resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
    }
    write_condition = {
      title      = "fugaro-licence-write-fugaro-licence"
      expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
    }
  }
}

# An installation whose coordinator is opted in but whose job is not deployed —
# no tagged image yet — gets no licence job: the job runs the coordinator's
# image, so creating it there would be a Cloud Run job whose container does not
# exist, failing once a day with nothing to renew.
run "licence_off_while_the_coordinators_job_is_not_deployed" {
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
    }
  }

  assert {
    condition = (length(google_service_account.licence) == 0 &&
      length(google_project_iam_custom_role.licence_renewal) == 0 &&
      length(google_secret_manager_secret.licence_renewal) == 0 &&
      length(google_secret_manager_secret_iam_member.licence_renewal) == 0 &&
      length(google_storage_bucket_iam_member.licence_read) == 0 &&
      length(google_storage_bucket_iam_member.licence_write) == 0 &&
      length(google_cloud_run_v2_job.licence) == 0 &&
      length(google_cloud_run_v2_job_iam_member.licence_invoker) == 0 &&
    length(google_cloud_scheduler_job.licence) == 0)
    error_message = "a licence block on an installation whose coordinator job is not deployed plans nothing: the switch is the coordinator's own (local.deploy_coordinator), so a second one that disagreed with it would create a job whose image does not exist"
  }
}

# The installation the job IS for: the coordinator opted in and its job
# deployed, which is every installation `fugaro update` reaches once the owner
# has turned the sweep on. The account, the empty container, its four calls, the
# two conditioned grants, the job and the tick are all planned, and the job is
# applied whether or not a licence was bought (D524).
run "licence_job" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.licence[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-licence@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-licence@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-licence@proj-1234.iam.gserviceaccount.com"
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
    condition     = length(google_service_account.licence) == 1 && google_service_account.licence[0].account_id == "fugaro-licence"
    error_message = "the renewal job runs as an account of its own, whose ID is the input (tf.LicenceAccountID reads it as this job's own kind of principal)"
  }
  assert {
    condition     = length(google_secret_manager_secret.licence_renewal) == 1 && google_secret_manager_secret.licence_renewal[0].secret_id == "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd"
    error_message = "the renewal container is created under the ID internal/infra.LicenceRenewalSecretID derives: `fugaro licence set` writes into the container that ID names and refuses before it exists with one line naming `fugaro update`"
  }
  assert {
    condition = (length(google_secret_manager_secret_iam_member.licence_renewal) == 1 &&
      google_secret_manager_secret_iam_member.licence_renewal[0].role == "projects/proj-1234/roles/fugaroLicenceRenewal" &&
    google_secret_manager_secret_iam_member.licence_renewal[0].secret_id == "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd")
    error_message = "the four calls are bound AT the one container and not at the project (a project binding would hand the job every container of the installation), and the role is the module's custom one"
  }
  assert {
    condition     = length(google_project_iam_custom_role.licence_renewal) == 1 && length(google_project_iam_custom_role.licence_renewal[0].permissions) == 4
    error_message = "no predefined role is exactly the four calls the job makes (secretVersionManager adds destroy, enable and disable; the accessor/adder pair carries neither secrets.get nor versions.list), so the module defines its own"
  }
  assert {
    condition = (length(google_storage_bucket_iam_member.licence_read) == 1 &&
      google_storage_bucket_iam_member.licence_read[0].role == "roles/storage.objectViewer" &&
      length(google_storage_bucket_iam_member.licence_write) == 1 &&
    google_storage_bucket_iam_member.licence_write[0].role == "roles/storage.objectUser")
    error_message = "the account reads the runs bucket through its condition and writes the licence object through the other: the condition is what makes either grant narrow, and objectUser is what a bucket with versioning needs for a second renewal of an object that is already there"
  }
  assert {
    condition = (google_cloud_run_v2_job.licence[0].name == "fugarolicence" &&
    google_cloud_run_v2_job.licence[0].template[0].template[0].containers[0].args == tolist(["licence", "renew", "--job"]))
    error_message = "the job runs the mode ee/register registers, and its name does not start with fugaro- (fugaro runs ls and max_parallel count those as workflow jobs)"
  }
  assert {
    condition = (length(google_cloud_run_v2_job_iam_member.licence_invoker) == 1 &&
      google_cloud_run_v2_job_iam_member.licence_invoker[0].role == "roles/run.invoker" &&
      length(google_cloud_scheduler_job.licence) == 1 &&
    google_cloud_scheduler_job.licence[0].retry_config[0].retry_count == 0)
    error_message = "the scheduler account may run this job only, and a failed renewal logs its failure rather than retrying the same night"
  }
}

run "licence_account_must_be_the_installations_own" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    licence = {
      account_id       = "fugaro-licence-renewal"
      job              = "fugarolicence"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-licence-renew"
      scheduler_region = "us-east4"
      renewal_secret   = "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd"
      env = {
        FUGARO_PROJECT     = "aurora"
        FUGARO_GCP_PROJECT = "proj-1234"
      }
      read_condition = {
        title      = "fugaro-licence-read-fugaro-licence"
        expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
      }
      write_condition = {
        title      = "fugaro-licence-write-fugaro-licence"
        expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
      }
    }
  }

  expect_failures = [var.licence]
}

run "licence_job_name_must_not_look_like_a_workflow_job" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    licence = {
      account_id       = "fugaro-licence"
      job              = "fugaro-licence-renew"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-licence-renew"
      scheduler_region = "us-east4"
      renewal_secret   = "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd"
      env = {
        FUGARO_PROJECT     = "aurora"
        FUGARO_GCP_PROJECT = "proj-1234"
      }
      read_condition = {
        title      = "fugaro-licence-read-fugaro-licence"
        expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
      }
      write_condition = {
        title      = "fugaro-licence-write-fugaro-licence"
        expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
      }
    }
  }

  expect_failures = [var.licence]
}

run "licence_env_must_name_both_projects" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    licence = {
      account_id       = "fugaro-licence"
      job              = "fugarolicence"
      image            = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest"
      scheduler_job    = "fugaro-licence-renew"
      scheduler_region = "us-east4"
      renewal_secret   = "fugaro-project-aurora-licence-renewal-0e382f732fd21bbd"
      env = {
        FUGARO_PROJECT = "aurora"
      }
      read_condition = {
        title      = "fugaro-licence-read-fugaro-licence"
        expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
      }
      write_condition = {
        title      = "fugaro-licence-write-fugaro-licence"
        expression = "resource.name == \"projects/_/buckets/fugaro-runs-proj-1234/objects/fugaro/licence.json\""
      }
    }
  }

  expect_failures = [var.licence]
}
