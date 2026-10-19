# Plans one repository against a mock provider: no credentials, no network
# beyond the provider download. The inputs are the golden tfvars fugaro
# init --repo writes (the Go side of the contract checks it produces
# exactly these files), so a shape Go and Terraform disagree on fails here.
#
# An assertion can't reach into a child module's resources, so the root
# runs check the wiring through the outputs, the repo module runs check its
# own resources (and each workflow's through the workflow module's outputs),
# and the workflow module runs check the per-workflow resources.

mock_provider "google" {}

variables {
  bitbucket = jsondecode(file("tests/testdata/bitbucket-oauth.tfvars.json"))
  github    = jsondecode(file("tests/testdata/github-vertex.tfvars.json"))
  # The same repository with one repo.project_secrets entry: a name Readiness
  # resolved to a project-wide container. Hand-maintained; see testdata/README.md.
  psec = jsondecode(file("tests/testdata/project-secrets.tfvars.json"))
}

run "root_bitbucket" {
  command = plan

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    github_app_id  = var.bitbucket.github_app_id
    repo           = var.bitbucket.repo
  }

  assert {
    condition     = tomap(output.jobs) == tomap({ web = "fugaro-acme-sandbox-web-7815dfafb6f5" })
    error_message = "the root must pass the workflows to the module"
  }
  assert {
    condition     = output.registry == "fugaro-acme-sandbox-6a12f465054d"
    error_message = "the registry output must be the repository's registry ID"
  }
  assert {
    condition     = output.check_job == "fugarochk-acme-sandbox-39759bd68a27"
    error_message = "the check_job output must be the check job's name"
  }
  assert {
    condition     = output.github_app_id == null
    error_message = "a Bitbucket repository has no GitHub App ID"
  }
}

run "root_github" {
  command = plan

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    github_app_id  = var.github.github_app_id
    repo           = var.github.repo
  }

  assert {
    condition = tomap(output.jobs) == tomap({
      api = "fugaro-acme-webapp-api-7daad3225e50"
      web = "fugaro-acme-webapp-web-76331eb3d601"
    })
    error_message = "every workflow's job must be an output"
  }
  assert {
    condition     = output.github_app_id == "123456"
    error_message = "the GitHub App ID must come back as an output, for init --repo to read"
  }
}

run "bad_github_app_id" {
  command = plan

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    github_app_id  = "12a"
    repo           = var.github.repo
  }

  expect_failures = [var.github_app_id]
}

run "names_are_inputs" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition     = module.workflow["web"].job == "fugaro-acme-sandbox-web-7815dfafb6f5"
    error_message = "the job's name must be the input"
  }
  assert {
    condition     = module.workflow["web"].service_account_id == "fugaro-acme-sandbox-w-7815dfaf"
    error_message = "the job account's ID must be the input"
  }
  assert {
    condition     = google_service_account.build.account_id == "fugaro-b-acme-sandbox-78cbc6a5"
    error_message = "the build account's ID must be the input"
  }
  assert {
    condition     = google_service_account.build.display_name == "Fugaro build acme-sandbox-100024cb41873d4e"
    error_message = "the build account's display name must be the input"
  }
  assert {
    condition     = google_secret_manager_secret.this["bitbucket-token"].secret_id == "fugaro-acme-sandbox-bitbucket-token-a9273afb5a2f66ba"
    error_message = "the bitbucket-token secret's ID must be the input"
  }
  assert {
    condition     = google_secret_manager_secret.this["claude-oauth-token"].secret_id == "fugaro-acme-sandbox-claude-oauth-token-d68d08fb66f32201"
    error_message = "the claude-oauth-token secret's ID must be the input"
  }
  assert {
    condition     = google_secret_manager_secret.this["sandbox-probe"].secret_id == "fugaro-acme-sandbox-sandbox-probe-8ed07eaf4b722cd8"
    error_message = "the sandbox-probe secret's ID must be the input"
  }
  assert {
    condition     = length(google_secret_manager_secret.this) == 3
    error_message = "there must be exactly one secret per input"
  }
  assert {
    condition     = google_artifact_registry_repository.images.repository_id == "fugaro-acme-sandbox-6a12f465054d"
    error_message = "the registry's ID must be the input"
  }
  assert {
    condition     = google_cloud_run_v2_job.check[0].name == "fugarochk-acme-sandbox-39759bd68a27"
    error_message = "the check job's name must be the input"
  }
  assert {
    condition     = google_cloud_scheduler_job.check[0].name == "fugarochk-acme-sandbox-5d4307f21198"
    error_message = "the Scheduler job's name must be the input"
  }
}

run "labels" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition = alltrue([for k, s in google_secret_manager_secret.this :
    s.labels == tomap({ fugaro = "managed", fugaro_repo = "acme-sandbox-100024cb41873d4e", fugaro_secret = k })])
    error_message = "each secret must carry fugaro=managed, fugaro_repo and its fugaro_secret"
  }
  assert {
    condition     = google_cloud_run_v2_job.check[0].labels == tomap({ fugaro = "managed", fugaro_repo = "acme-sandbox-100024cb41873d4e", fugaro_role = "check" })
    error_message = "the check job must carry fugaro=managed, fugaro_repo and fugaro_role=check"
  }
  # Only the labels on the job's template reach its log entries, which the
  # log sink and exclusion select on.
  assert {
    condition     = google_cloud_run_v2_job.check[0].template[0].labels == tomap({ fugaro = "managed", fugaro_repo = "acme-sandbox-100024cb41873d4e", fugaro_role = "check" })
    error_message = "the check job's template must carry the same labels, for its log entries"
  }
  assert {
    condition     = google_artifact_registry_repository.images.labels == tomap({ fugaro = "managed", fugaro_repo = "acme-sandbox-100024cb41873d4e" })
    error_message = "the registry must carry fugaro=managed and fugaro_repo"
  }
}

run "labels_job" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  assert {
    condition     = google_cloud_run_v2_job.this[0].labels == tomap({ fugaro = "managed", fugaro_repo = "acme-sandbox-100024cb41873d4e", fugaro_workflow = "web" })
    error_message = "the job must carry fugaro=managed, fugaro_repo and fugaro_workflow"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].labels == tomap({ fugaro = "managed", fugaro_repo = "acme-sandbox-100024cb41873d4e", fugaro_workflow = "web" })
    error_message = "the job's template must carry the same labels, for its log entries"
  }
  assert {
    condition     = google_service_account.job.display_name == "Fugaro job acme-sandbox-100024cb41873d4e web"
    error_message = "the job account's display name is its mark and must be the input"
  }
}

run "max_retries_zero" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition     = google_cloud_run_v2_job.check[0].template[0].template[0].max_retries == 0
    error_message = "the check job must not retry"
  }
  assert {
    condition     = google_cloud_run_v2_job.check[0].template[0].task_count == 1
    error_message = "the check job runs one task"
  }
  assert {
    condition     = google_cloud_run_v2_job.check[0].deletion_protection == true
    error_message = "the check job must be protected from deletion"
  }
}

run "max_retries_zero_job" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].template[0].max_retries == 0
    error_message = "the job must not retry"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].task_count == 1
    error_message = "the job runs one task"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].template[0].timeout == "1320s"
    error_message = "the task timeout must be the input, in seconds"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].template[0].execution_environment == "EXECUTION_ENVIRONMENT_GEN2"
    error_message = "the job must run on the second generation environment, as the live jobs do"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].deletion_protection == true
    error_message = "the job must be protected from deletion by default"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].image == "us-east5-docker.pkg.dev/proj-1234/fugaro-acme-sandbox-6a12f465054d/acme-sandbox-web-7815dfafb6f5bcbb:latest"
    error_message = "the job's image must be the input"
  }
  assert {
    condition     = google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].resources[0].limits == tomap({ cpu = "1", memory = "2Gi" })
    error_message = "the job's limits must be the input's cpu and memory"
  }
  assert {
    condition = [for e in google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].env : e.name] == [
      "CLAUDE_CODE_OAUTH_TOKEN", "FUGARO_BACKEND", "FUGARO_BITBUCKET_TOKEN", "FUGARO_BUCKET",
      "FUGARO_GCP_PROJECT", "FUGARO_PROJECT", "FUGARO_REGION", "FUGARO_SECRET_ENVS", "SANDBOX_PROBE",
    ]
    error_message = "the env must be exactly the plain and secret variables, sorted by name"
  }
  assert {
    condition = alltrue([for e in google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].env :
    e.value == var.bitbucket.repo.workflows.web.env[e.name] if length(e.value_source) == 0])
    error_message = "each plain env value must be the input's"
  }
}

run "allow_job_delete" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project          = var.bitbucket.project
    fugaro_project   = var.bitbucket.fugaro_project
    region           = var.bitbucket.region
    installation     = var.bitbucket.installation
    repo             = var.bitbucket.repo
    allow_job_delete = true
  }

  assert {
    condition     = google_cloud_run_v2_job.check[0].deletion_protection == false
    error_message = "offboarding must lower the check job's deletion protection"
  }
  assert {
    condition     = module.workflow["web"].deletion_protection == false
    error_message = "offboarding must lower each job's deletion protection"
  }
}

run "bucket_condition_exact" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  assert {
    condition     = google_storage_bucket_iam_member.job.condition[0].expression == var.bitbucket.repo.workflows.web.bucket_condition.expression
    error_message = "the job account's bucket condition must be the input, byte for byte"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job.condition[0].title == "fugaro-fugaro-acme-sandbox-w-7815dfaf"
    error_message = "the condition's title must be the input"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job.condition[0].description == null
    error_message = "the bootstrap set no condition description, so none may be planned"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job.role == "roles/storage.objectUser" && google_storage_bucket_iam_member.job.bucket == "fugaro-runs-proj-1234"
    error_message = "the job account gets objectUser on the runs bucket"
  }
}

# The job's read-only grant on its own repository's budget token objects
# (docs/design/budget-token-channel.md section 7.2, D16): objectViewer, never
# objectUser, on exactly tokens/<slug>/ of the runs bucket, and the runs/,
# cache/ and locks/ grant does not reach tokens/.
run "token_condition_exact" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  assert {
    condition     = google_storage_bucket_iam_member.job_tokens.role == "roles/storage.objectViewer" && google_storage_bucket_iam_member.job_tokens.bucket == "fugaro-runs-proj-1234"
    error_message = "the job account reads tokens/ and may not write it: objectViewer, never objectUser"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job_tokens.condition[0].expression == "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/acme-sandbox-100024cb41873d4e/\")"
    error_message = "the job account's token condition is its own repository's tokens/<slug>/, byte for byte"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job_tokens.condition[0].title == "fugaro-tokens-fugaro-acme-sandbox-w-7815dfaf" && google_storage_bucket_iam_member.job_tokens.condition[0].description == null
    error_message = "the token condition's title is the input, with no description"
  }
  assert {
    condition     = !strcontains(google_storage_bucket_iam_member.job.condition[0].expression, "/objects/tokens/")
    error_message = "the job's objectUser grant must not reach tokens/: a peer run could delete a queued token"
  }
}

# A token condition widened past tokens/<slug>/ (a second clause) fails the plan.
run "token_condition_not_past_tokens" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = merge(var.bitbucket.repo.workflows.web, { token_condition = { title = "fugaro-tokens-fugaro-acme-sandbox-w-7815dfaf", expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/acme-sandbox-100024cb41873d4e/\") || resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")" } })
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [var.workflow]
}

# tokens/ of every repository fails the plan: a job reads its own repository's.
run "token_condition_not_all_tokens" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = merge(var.bitbucket.repo.workflows.web, { token_condition = { title = "fugaro-tokens-fugaro-acme-sandbox-w-7815dfaf", expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")" } })
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [var.workflow]
}

# Another repository's tokens/<slug>/ fails the plan: the slug must be the one
# the job's own runs/ condition names.
run "token_condition_not_another_repo" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = merge(var.bitbucket.repo.workflows.web, { token_condition = { title = "fugaro-tokens-fugaro-acme-sandbox-w-7815dfaf", expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/acme-other-0123456789abcdef/\")" } })
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [var.workflow]
}

# Another bucket's tokens/ fails the plan (the resource precondition).
run "token_condition_this_bucket" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = merge(var.bitbucket.repo.workflows.web, { bucket_condition = { title = var.bitbucket.repo.workflows.web.bucket_condition.title, expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-other/objects/runs/acme-sandbox-100024cb41873d4e/\")" }, token_condition = { title = "t", expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-other/objects/tokens/acme-sandbox-100024cb41873d4e/\")" } })
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [google_storage_bucket_iam_member.job_tokens]
}

run "build_bucket_condition_exact" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition     = google_storage_bucket_iam_member.build.condition[0].expression == var.bitbucket.repo.build_bucket_condition.expression
    error_message = "the build account's bucket condition must be the input, byte for byte"
  }
  assert {
    condition     = google_storage_bucket_iam_member.build.condition[0].title == "fugaro-fugaro-b-acme-sandbox-78cbc6a5"
    error_message = "the build condition's title must be the input"
  }
  assert {
    condition     = google_storage_bucket_iam_member.build.role == "roles/storage.objectUser" && google_storage_bucket_iam_member.build.bucket == "fugaro-runs-proj-1234"
    error_message = "the build account gets objectUser on the runs bucket"
  }
}

run "secret_ref_short_form" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition = tomap(module.workflow["web"].secret_refs) == tomap({
      CLAUDE_CODE_OAUTH_TOKEN = "fugaro-acme-sandbox-claude-oauth-token-d68d08fb66f32201"
      FUGARO_BITBUCKET_TOKEN  = "fugaro-acme-sandbox-bitbucket-token-a9273afb5a2f66ba"
      SANDBOX_PROBE           = "fugaro-acme-sandbox-sandbox-probe-8ed07eaf4b722cd8"
    })
    error_message = "the job's secret references must be the secret IDs, not projects/… paths"
  }
  assert {
    condition = [for e in google_cloud_run_v2_job.check[0].template[0].template[0].containers[0].env :
      one(one(e.value_source).secret_key_ref)
    if length(e.value_source) > 0] == [{ secret = "fugaro-acme-sandbox-bitbucket-token-a9273afb5a2f66ba", version = "latest" }]
    error_message = "the check job must mount the provider credential by its secret ID, version latest"
  }
}

run "secret_ref_short_form_job" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = alltrue([for e in google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].env :
      one(one(e.value_source).secret_key_ref).secret == var.bitbucket.repo.secrets[var.bitbucket.repo.workflows.web.secret_env[e.name]] &&
      one(one(e.value_source).secret_key_ref).version == "latest" &&
      !startswith(one(one(e.value_source).secret_key_ref).secret, "projects/")
    if length(e.value_source) > 0])
    error_message = "each secret env must reference its secret's ID, version latest"
  }
  assert {
    condition     = length([for e in google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].env : e if length(e.value_source) > 0]) == 3
    error_message = "the job must mount each of its three secrets"
  }
  assert {
    condition = tomap({ for k, m in google_secret_manager_secret_iam_member.job : k => m.secret_id }) == tomap({
      claude-oauth-token = "fugaro-acme-sandbox-claude-oauth-token-d68d08fb66f32201"
      bitbucket-token    = "fugaro-acme-sandbox-bitbucket-token-a9273afb5a2f66ba"
      sandbox-probe      = "fugaro-acme-sandbox-sandbox-probe-8ed07eaf4b722cd8"
    })
    error_message = "the job account must get accessor on exactly the secrets it mounts"
  }
  assert {
    condition     = alltrue([for m in google_secret_manager_secret_iam_member.job : m.role == "roles/secretmanager.secretAccessor"])
    error_message = "the job account's secret grants must be secretAccessor"
  }
  assert {
    condition = alltrue([for m in google_secret_manager_secret_iam_member.job :
    m.member == "serviceAccount:fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"])
    error_message = "the job account's secret grants must go to its own account, and no other"
  }
}

# The github fixture's repository holds two secrets (github-app-key and
# npm-token) and its "api" workflow mounts only the first, so this run
# separates "the secrets the workflow mounts" from "the secrets the repository
# has": grants widened to every secret in the map it was handed, or addressed
# at any account but the job's own, fail here (the web workflow mounts both, so
# a run against it cannot tell the two apart).
run "job_grants_follow_the_workflow" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.github.project
    fugaro_project  = var.github.fugaro_project
    region          = var.github.region
    name            = "api"
    repo_label      = var.github.repo.label
    workflow        = var.github.repo.workflows.api
    secrets         = var.github.repo.secrets
    runs_bucket     = var.github.installation.runs_bucket
    job_runner_role = var.github.installation.role_ids.job_runner
  }

  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = tomap({ for k, m in google_secret_manager_secret_iam_member.job : k => m.secret_id }) == tomap({
      github-app-key = "fugaro-acme-webapp-github-app-key-35b331db19bcc682"
    })
    error_message = "the job account must get accessor on the secrets its own workflow mounts, not on every secret in the repository"
  }
  assert {
    condition = alltrue([for m in google_secret_manager_secret_iam_member.job :
      m.role == "roles/secretmanager.secretAccessor" &&
    m.member == "serviceAccount:fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"])
    error_message = "the job account's secret grants must be secretAccessor to its own account, and no other"
  }
}

run "cleanup_keeps_latest" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition = anytrue([for p in google_artifact_registry_repository.images.cleanup_policies :
    p.action == "KEEP" && length(p.condition) == 1 && contains(coalesce(one(p.condition).tag_prefixes, []), "latest")])
    error_message = "the registry must keep every version tagged latest"
  }
  assert {
    condition = anytrue([for p in google_artifact_registry_repository.images.cleanup_policies :
    p.action == "KEEP" && length(p.most_recent_versions) == 1 && one(p.most_recent_versions).keep_count == 3])
    error_message = "the registry must keep the 3 most recent versions"
  }
  assert {
    condition = anytrue([for p in google_artifact_registry_repository.images.cleanup_policies :
    p.action == "DELETE" && one(p.condition).tag_state == "UNTAGGED" && one(p.condition).older_than == "1209600s"])
    error_message = "the registry must delete untagged versions after 14 days"
  }
  assert {
    condition = anytrue([for p in google_artifact_registry_repository.images.cleanup_policies :
      p.action == "DELETE" && one(p.condition).tag_state == "TAGGED" &&
    one(p.condition).tag_prefixes == tolist(["candidate-"]) && one(p.condition).older_than == "172800s"])
    error_message = "the registry must delete candidate- versions after 2 days"
  }
  assert {
    condition = alltrue([for p in google_artifact_registry_repository.images.cleanup_policies :
      p.action == "KEEP" || (length(p.condition) == 1 && (one(p.condition).tag_state == "UNTAGGED" ||
    one(p.condition).tag_prefixes == tolist(["candidate-"])))])
    error_message = "the registry's only delete rules must be for untagged and candidate- versions"
  }
  assert {
    condition     = length(google_artifact_registry_repository.images.cleanup_policies) == 4
    error_message = "the registry must have exactly two keep and two delete rules"
  }
  assert {
    condition     = google_artifact_registry_repository.images.format == "DOCKER" && google_artifact_registry_repository.images.location == "us-east5"
    error_message = "the registry must be a Docker registry in the region"
  }
}

run "build_writes_own_registry_only" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo           = var.github.repo
  }

  assert {
    condition = (google_artifact_registry_repository_iam_member.build_images.role == "roles/artifactregistry.writer" &&
    google_artifact_registry_repository_iam_member.build_images.repository == google_artifact_registry_repository.images.repository_id)
    error_message = "the build account must write its own registry"
  }
  assert {
    condition = (google_artifact_registry_repository_iam_member.build_tag_mover.role == "projects/proj-1234/roles/fugaroTagMover" &&
    google_artifact_registry_repository_iam_member.build_tag_mover.repository == google_artifact_registry_repository.images.repository_id)
    error_message = "the build account must move tags on its own registry, and only there"
  }
  assert {
    condition = (google_artifact_registry_repository_iam_member.build_base.role == "roles/artifactregistry.reader" &&
    google_artifact_registry_repository_iam_member.build_base.repository == "fugaro-base")
    error_message = "the build account must only read the base registry"
  }
}

run "build_role_set_on_own_registry" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo           = var.github.repo
  }

  override_resource {
    target          = google_service_account.build
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
    }
  }

  # Writer, plus the tag mover (tags.delete only), which moving an existing
  # :latest needs: no repoAdmin, no admin, so no version is ever deletable.
  assert {
    condition = toset(concat(
      [for m in [google_artifact_registry_repository_iam_member.build_images, google_artifact_registry_repository_iam_member.build_tag_mover] : m.role
      if m.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"],
      [for m in google_artifact_registry_repository_iam_member.operator_images : m.role
      if m.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"],
    )) == toset(["roles/artifactregistry.writer", "projects/proj-1234/roles/fugaroTagMover"])
    error_message = "the build account's roles on its registry must be exactly writer and fugaroTagMover: no repoAdmin, no admin"
  }
  assert {
    condition = alltrue([for m in [google_artifact_registry_repository_iam_member.build_images, google_artifact_registry_repository_iam_member.build_tag_mover] :
    m.repository == google_artifact_registry_repository.images.repository_id && m.location == "us-east5" && m.project == "proj-1234"])
    error_message = "the build account's writer and tag mover grants must be on its own registry only"
  }
  assert {
    condition = tomap({ for k, m in google_artifact_registry_repository_iam_member.operator_images : k => m.role }) == tomap({
      "user:operator@example.com" = "roles/artifactregistry.reader"
    })
    error_message = "operators only read the repository's registry"
  }
}

run "build_iam" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo           = var.github.repo
  }

  override_resource {
    target          = google_service_account.build
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = tomap({ for k, m in google_secret_manager_secret_iam_member.build : k => m.secret_id }) == tomap({
      github-app-key = "fugaro-acme-webapp-github-app-key-35b331db19bcc682"
      npm-token      = "fugaro-acme-webapp-npm-token-bacfa0e910c36bb9"
    })
    error_message = "the build account must read exactly its build secrets"
  }
  assert {
    condition     = alltrue([for m in google_secret_manager_secret_iam_member.build : m.role == "roles/secretmanager.secretAccessor"])
    error_message = "the build account's secret grants must be secretAccessor"
  }
  assert {
    condition = alltrue([for m in google_secret_manager_secret_iam_member.build :
    m.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"])
    error_message = "the build account's secret grants must go to its own account, and no other"
  }
  assert {
    condition = tomap({ for k, m in google_project_iam_member.build : k => m.role }) == tomap({
      log_writer      = "roles/logging.logWriter"
      build_submitter = "projects/proj-1234/roles/fugaroBuildSubmitter"
    })
    error_message = "the build account's project roles must be exactly logWriter and fugaroBuildSubmitter"
  }
  assert {
    condition = (google_service_account_iam_member.build_self.role == "roles/iam.serviceAccountUser" &&
      google_service_account_iam_member.build_self.service_account_id == "projects/proj-1234/serviceAccounts/fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com" &&
    google_service_account_iam_member.build_self.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com")
    error_message = "the build account must be able to act as itself, and only itself"
  }
}

run "operators" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo           = var.github.repo
  }

  assert {
    condition = toset([for m in google_secret_manager_secret_iam_member.operator : "${m.secret_id} ${m.role} ${m.member}"]) == toset([
      "fugaro-acme-webapp-github-app-key-35b331db19bcc682 roles/secretmanager.secretVersionAdder user:operator@example.com",
      "fugaro-acme-webapp-github-app-key-35b331db19bcc682 roles/secretmanager.viewer user:operator@example.com",
      "fugaro-acme-webapp-npm-token-bacfa0e910c36bb9 roles/secretmanager.secretVersionAdder user:operator@example.com",
      "fugaro-acme-webapp-npm-token-bacfa0e910c36bb9 roles/secretmanager.viewer user:operator@example.com",
    ])
    error_message = "operators must add versions to and view each secret, and nothing more"
  }
  assert {
    condition = tomap({ for k, m in google_service_account_iam_member.operator_build : k => m.role }) == tomap({
      "user:operator@example.com" = "roles/iam.serviceAccountUser"
    })
    error_message = "operators must act as the build account"
  }
}

run "people_job" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.github.project
    fugaro_project  = var.github.fugaro_project
    region          = var.github.region
    name            = "web"
    repo_label      = var.github.repo.label
    workflow        = var.github.repo.workflows.web
    secrets         = var.github.repo.secrets
    runs_bucket     = var.github.installation.runs_bucket
    job_runner_role = var.github.installation.role_ids.job_runner
    launchers       = var.github.installation.launchers
    operators       = var.github.installation.operators
  }

  assert {
    # fugaro init puts the operators among the launchers too (they get
    # everything launchers get), so the fixture's operator runs jobs.
    condition = tomap({ for k, m in google_cloud_run_v2_job_iam_member.launcher : k => m.role }) == tomap({
      "user:launcher@example.com" = "projects/proj-1234/roles/fugaroJobRunner"
      "user:operator@example.com" = "projects/proj-1234/roles/fugaroJobRunner"
    })
    error_message = "launchers (operators included) must get fugaroJobRunner on the job"
  }
  assert {
    condition     = google_cloud_run_v2_job_iam_member.launcher["user:launcher@example.com"].name == "fugaro-acme-webapp-web-76331eb3d601"
    error_message = "the launcher grant must be on the workflow's job"
  }
  assert {
    condition = tomap({ for k, m in google_service_account_iam_member.operator : k => m.role }) == tomap({
      "user:operator@example.com" = "roles/iam.serviceAccountUser"
    })
    error_message = "operators must act as the job account, to deploy the job"
  }
}

run "cleanup_dry_run_from_input" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo = merge(var.github.repo, {
      registry = { repository_id = var.github.repo.registry.repository_id, cleanup_dry_run = false }
    })
  }

  assert {
    condition     = google_artifact_registry_repository.images.cleanup_policy_dry_run == false
    error_message = "cleanup_policy_dry_run must follow the input"
  }
}

run "cleanup_dry_run_golden" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo           = var.github.repo
  }

  assert {
    condition     = google_artifact_registry_repository.images.cleanup_policy_dry_run == true
    error_message = "the golden's registry cleanup is a dry run"
  }
}

run "scheduler_region_and_paused" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  assert {
    condition     = google_cloud_scheduler_job.check[0].region == "us-east4"
    error_message = "the Scheduler job must be in the scheduler region"
  }
  assert {
    condition     = strcontains(one(google_cloud_scheduler_job.check[0].http_target).uri, "locations/us-east5/jobs/")
    error_message = "the Scheduler job must start the job in the job's own region"
  }
  assert {
    condition     = one(google_cloud_scheduler_job.check[0].http_target).uri == "https://run.googleapis.com/v2/projects/proj-1234/locations/us-east5/jobs/fugarochk-acme-sandbox-39759bd68a27:run"
    error_message = "the Scheduler job must call the check job's run method"
  }
  assert {
    condition     = google_cloud_scheduler_job.check[0].paused == true
    error_message = "the golden schedule is paused until a build record exists"
  }
  assert {
    condition     = google_cloud_scheduler_job.check[0].schedule == "37 6 * * *" && google_cloud_scheduler_job.check[0].time_zone == "Etc/UTC"
    error_message = "the schedule must be the input, in UTC"
  }
  assert {
    condition     = one(google_cloud_scheduler_job.check[0].retry_config).retry_count == 0
    error_message = "a failed check must not be retried by Scheduler"
  }
  assert {
    condition = (one(one(google_cloud_scheduler_job.check[0].http_target).oauth_token).service_account_email == "fugaro-scheduler@proj-1234.iam.gserviceaccount.com" &&
    one(google_cloud_scheduler_job.check[0].http_target).http_method == "POST")
    error_message = "the Scheduler job must POST as the scheduler account"
  }
  assert {
    condition = (google_cloud_run_v2_job_iam_member.check_invoker[0].role == "roles/run.invoker" &&
      google_cloud_run_v2_job_iam_member.check_invoker[0].member == "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com" &&
    google_cloud_run_v2_job_iam_member.check_invoker[0].name == "fugarochk-acme-sandbox-39759bd68a27")
    error_message = "the scheduler account must be able to run the check job, and only it"
  }
  assert {
    condition = (google_cloud_run_v2_job.check[0].location == "us-east5" &&
      google_cloud_run_v2_job.check[0].template[0].template[0].containers[0].image == "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc" &&
      google_cloud_run_v2_job.check[0].template[0].template[0].containers[0].command == tolist(["fugaro"]) &&
    google_cloud_run_v2_job.check[0].template[0].template[0].containers[0].args == tolist(["image", "check", "--job"]))
    error_message = "the check job must run fugaro image check --job from the base image, in the region"
  }
}

# Cloud Run checks a job's secrets when it creates it, and the check job
# mounts the provider credential at latest, so until fugaro init --repo
# finds a version (or the job already exists) it plans no check job, and
# so no invoker grant or Scheduler job either.
run "check_not_deployed" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = merge(var.bitbucket.repo, { check = merge(var.bitbucket.repo.check, { deploy_job = false }) })
  }

  assert {
    condition     = length(google_cloud_run_v2_job.check) == 0 && length(google_cloud_scheduler_job.check) == 0 && length(google_cloud_run_v2_job_iam_member.check_invoker) == 0
    error_message = "an undeployed check must have no check job, schedule or invoker grant"
  }
  assert {
    condition     = output.check_job == null
    error_message = "check_job is null while the check job isn't deployed"
  }
}

run "check_deployed" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = merge(var.bitbucket.repo, { check = merge(var.bitbucket.repo.check, { deploy_job = true, paused = true }) })
  }

  assert {
    condition     = length(google_cloud_run_v2_job.check) == 1 && length(google_cloud_scheduler_job.check) == 1 && length(google_cloud_run_v2_job_iam_member.check_invoker) == 1
    error_message = "a deployed check must have its check job, schedule and invoker grant"
  }
  assert {
    condition     = google_cloud_scheduler_job.check[0].paused == true
    error_message = "a deployed check's schedule stays paused until a build record exists"
  }
}

run "no_check" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = merge(var.bitbucket.repo, { check = null })
  }

  assert {
    condition     = length(google_cloud_run_v2_job.check) == 0 && length(google_cloud_scheduler_job.check) == 0 && length(google_cloud_run_v2_job_iam_member.check_invoker) == 0
    error_message = "with every check off, there must be no check job, schedule or invoker grant"
  }
}

run "vertex_only_when_asked" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.github.project
    fugaro_project  = var.github.fugaro_project
    region          = var.github.region
    name            = "api"
    repo_label      = var.github.repo.label
    workflow        = var.github.repo.workflows.api
    secrets         = var.github.repo.secrets
    runs_bucket     = var.github.installation.runs_bucket
    job_runner_role = var.github.installation.role_ids.job_runner
  }

  assert {
    condition     = length(google_project_iam_member.vertex) == 1 && google_project_iam_member.vertex[0].role == "roles/aiplatform.user"
    error_message = "a vertex workflow's account must get aiplatform.user"
  }
}

run "vertex_not_asked" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  assert {
    condition     = length(google_project_iam_member.vertex) == 0
    error_message = "a workflow without vertex must not get aiplatform.user"
  }
}

run "deploy_job_false" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = merge(var.bitbucket.repo.workflows.web, { deploy_job = false })
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
    launchers       = ["user:launcher@example.com"]
  }

  assert {
    condition     = length(google_cloud_run_v2_job.this) == 0 && length(google_cloud_run_v2_job_iam_member.launcher) == 0
    error_message = "without deploy_job, no job (and no grant on it) may be planned"
  }
  assert {
    condition     = google_service_account.job.account_id == "fugaro-acme-sandbox-w-7815dfaf"
    error_message = "the job account is planned without the job"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job.condition[0].title == "fugaro-fugaro-acme-sandbox-w-7815dfaf" && length(google_secret_manager_secret_iam_member.job) == 3
    error_message = "the account's grants are planned without the job"
  }
  assert {
    condition     = output.job == null
    error_message = "the job output is null when no job is deployed"
  }
}

run "bad_account_id" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = merge(var.bitbucket.repo.workflows.web, { service_account = { account_id = "Bad_ID", display_name = "x" } })
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [var.workflow]
}

run "unknown_secret" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = merge(var.bitbucket.repo, { build_secrets = ["bitbucket-token", "no-such-secret"] })
  }

  expect_failures = [var.repo]
}

run "job_env_names_the_project" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  assert {
    condition     = var.workflow.env["FUGARO_PROJECT"] == "aurora" && var.workflow.env["FUGARO_GCP_PROJECT"] == "proj-1234"
    error_message = "FUGARO_PROJECT is the project name and FUGARO_GCP_PROJECT the GCP project ID"
  }
}

run "job_env_project_mismatch_fails" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = "borealis"
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [google_cloud_run_v2_job.this]
}

run "job_env_gcp_project_mismatch_fails" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = "other-proj-1"
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  expect_failures = [google_cloud_run_v2_job.this]
}

run "check_job_env_project_mismatch_fails" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo = merge(var.bitbucket.repo, {
      check = merge(var.bitbucket.repo.check, {
        env = merge(var.bitbucket.repo.check.env, { FUGARO_PROJECT = "borealis" })
      })
    })
  }

  expect_failures = [google_cloud_run_v2_job.check]
}

run "bad_fugaro_project" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = "Not A Name"
    region         = var.bitbucket.region
    installation   = var.bitbucket.installation
    repo           = var.bitbucket.repo
  }

  expect_failures = [var.fugaro_project]
}

# The resolved secret reaches every reader (design docs/design/project-secrets.md
# §5, Task 5): the workflow module's job mount, the job account's accessor
# grant, the build account's grant and the check job's secret_key_ref must all
# name the IDENTICAL project-wide container for bitbucket-token, while a name
# with no project copy (sandbox-probe) keeps the repository's own. A divergence
# means one of the paths is still reading a stale, repo-only value.
run "build_and_check_use_the_resolved_secret" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.psec.project
    fugaro_project = var.psec.fugaro_project
    region         = var.psec.region
    installation   = var.psec.installation
    repo           = var.psec.repo
  }

  assert {
    condition     = module.workflow["web"].secret_refs["FUGARO_BITBUCKET_TOKEN"] == var.psec.repo.project_secrets["bitbucket-token"]
    error_message = "the job must mount bitbucket-token from the project-wide container"
  }
  assert {
    condition     = module.workflow["web"].secret_refs["SANDBOX_PROBE"] == var.psec.repo.secrets["sandbox-probe"]
    error_message = "a name with no project copy must keep the repository's own container"
  }
  assert {
    condition = tomap({ for k, m in google_secret_manager_secret_iam_member.build : k => m.secret_id }) == tomap({
      bitbucket-token = var.psec.repo.project_secrets["bitbucket-token"]
      sandbox-probe   = var.psec.repo.secrets["sandbox-probe"]
    })
    error_message = "the build account's grant on a resolved name must be the project-wide container, and its other grants the repository's own"
  }
  assert {
    condition = [for e in google_cloud_run_v2_job.check[0].template[0].template[0].containers[0].env :
      one(one(e.value_source).secret_key_ref).secret
    if length(e.value_source) > 0] == [var.psec.repo.project_secrets["bitbucket-token"]]
    error_message = "the check job must mount bitbucket-token from the project-wide container"
  }
}

# The workflow module grants its job account accessor on whatever map it is
# handed, so the repo module's merged local is what decides which container
# that is; this run pins that the merged map takes the project value for a
# resolved name (the repo module run above cannot reach this resource).
run "resolved_secret_job_grant" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.psec.project
    fugaro_project  = var.psec.fugaro_project
    region          = var.psec.region
    name            = "web"
    repo_label      = var.psec.repo.label
    workflow        = var.psec.repo.workflows.web
    secrets         = merge(var.psec.repo.secrets, var.psec.repo.project_secrets)
    runs_bucket     = var.psec.installation.runs_bucket
    job_runner_role = var.psec.installation.role_ids.job_runner
  }

  assert {
    condition     = google_secret_manager_secret_iam_member.job["bitbucket-token"].secret_id == var.psec.repo.project_secrets["bitbucket-token"]
    error_message = "the job account's accessor grant on a resolved name must be the project-wide container"
  }
  assert {
    condition     = google_secret_manager_secret_iam_member.job["sandbox-probe"].secret_id == var.psec.repo.secrets["sandbox-probe"]
    error_message = "the job account's accessor grant on an unresolved name must stay the repository's own container"
  }
}

# The credentials service off (the installation every repository has today,
# D147): the job account keeps its accessor grant on each secret its workflow
# mounts, and holds nothing on a service that does not exist.
run "credentials_service_off" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
  }

  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = length(google_cloud_run_v2_service_iam_member.credentials) == 0
    error_message = "an installation that has not opted in creates no service, so the job account is granted nothing on one"
  }
  assert {
    condition = toset(keys(google_secret_manager_secret_iam_member.job)) == toset([
      "claude-oauth-token", "bitbucket-token", "sandbox-probe",
    ])
    error_message = "with the service off the job account keeps accessor on every secret its workflow mounts, exactly as today"
  }
}

# The credentials service on (D143, D146, D147): the model secrets leave the
# job account's accessor grant — even the repository's own container of one,
# because the drop is by logical name — and the job account gets run.invoker
# on the service instead, which its IAM-checked ingress makes the only way in.
#
# The mount stays: dropping a model secret from the job's environment is
# C1.20's spec.go change, not this one's. So the plan here is a job that
# mounts a secret it can no longer read, and the assertion below says so out
# loud rather than letting it be discovered at run time.
run "credentials_service_on" {
  command = plan

  module {
    source = "../../modules/workflow"
  }

  variables {
    project         = var.bitbucket.project
    fugaro_project  = var.bitbucket.fugaro_project
    region          = var.bitbucket.region
    name            = "web"
    repo_label      = var.bitbucket.repo.label
    workflow        = var.bitbucket.repo.workflows.web
    secrets         = var.bitbucket.repo.secrets
    runs_bucket     = var.bitbucket.installation.runs_bucket
    job_runner_role = var.bitbucket.installation.role_ids.job_runner
    credentials_service = {
      name    = "fugaro-credentials"
      region  = "us-east5"
      secrets = ["claude-oauth-token"]
    }
  }

  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = toset(keys(google_secret_manager_secret_iam_member.job)) == toset([
      "bitbucket-token", "sandbox-probe",
    ])
    error_message = "with the service on the job account holds no accessor grant on a model secret: the service's own account is the only identity that reads one (Check 94)"
  }
  assert {
    condition = alltrue([for m in google_secret_manager_secret_iam_member.job :
      m.role == "roles/secretmanager.secretAccessor" &&
    m.member == "serviceAccount:fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com"])
    error_message = "the grants that remain are still secretAccessor to the job's own account: the drop moves one secret, not the shape"
  }
  assert {
    condition = length([for e in google_cloud_run_v2_job.this[0].template[0].template[0].containers[0].env :
    e if length(e.value_source) > 0]) == 3
    error_message = "the job must still mount its three secrets: removing a model secret's mount is C1.20's change, and this task leaves a job mounting what it mounts today"
  }
  assert {
    condition = (length(google_cloud_run_v2_service_iam_member.credentials) == 1 &&
      google_cloud_run_v2_service_iam_member.credentials[0].role == "roles/run.invoker" &&
      google_cloud_run_v2_service_iam_member.credentials[0].name == "fugaro-credentials" &&
      google_cloud_run_v2_service_iam_member.credentials[0].location == "us-east5" &&
    google_cloud_run_v2_service_iam_member.credentials[0].member == "serviceAccount:fugaro-acme-sandbox-w-7815dfaf@proj-1234.iam.gserviceaccount.com")
    error_message = "the job account must reach the cascade with its own identity: run.invoker on the service the opt-in names, in its region, and nothing wider"
  }
}

# A service the opt-in cannot address is refused at the repository too, and
# with the same message: the name and the region here are the two values every
# job account's run.invoker grant is built from.
run "credentials_service_needs_an_address" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.bitbucket.project
    fugaro_project = var.bitbucket.fugaro_project
    region         = var.bitbucket.region
    installation = merge(var.bitbucket.installation, {
      credentials_service = {
        name    = "FugaroCredentials"
        region  = "us-east5"
        secrets = ["claude-oauth-token"]
      }
    })
    repo = var.bitbucket.repo
  }

  expect_failures = [var.installation]
}
