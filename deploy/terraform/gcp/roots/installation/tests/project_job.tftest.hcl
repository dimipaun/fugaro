# The shared project job's module (design project-centric §4.1, plan tasks
# 3.2 and 3.5), planned against a mock provider: no credentials, no network
# beyond the provider download.
#
# It pins the three things phase 3's security review reads here — the job's
# whole grant surface:
#   1. the launcher's job-runner grant on the job (modules/project/iam.tf),
#      the surface `fugaro doctor`'s launcher-jobs check reports (task 3.5
#      step 1) — with no condition, because the job's name is the limit;
#   2. the job account's bucket condition on the whole of runs/, cache/ and
#      locks/ (modules/project/sa.tf, D4/D13, accepted by the owner on
#      2026-10-17) — the widening SECURITY.md states, pinned here as the
#      widened condition rather than as the per-slug one;
#   3. FUGARO_GITHUB_APP_ID in the job's environment, with its precondition
#      (owner ruling of 2026-10-17, task 3.5 step 4).
# Nothing here derives a name: the job name, the account id and the condition
# are what internal/infra wrote into the tfvars, and the assertions read the
# module's own resources. The two names below are the literals
# gcp.ProjectJobName/ProjectServiceAccountID produce for (standard, github,
# personal) and (standard, github, "").

mock_provider "google" {}

variables {
  project         = "proj-1234"
  fugaro_project  = "aurora"
  region          = "us-east5"
  runs_bucket     = "fugaro-runs-proj-1234"
  secrets         = {}
  job_runner_role = "projects/proj-1234/roles/fugaroJobRunner"
  launchers       = ["user:launcher@example.com"]
  operators       = ["user:operator@example.com"]
  job = {
    profile  = "standard"
    provider = "github"
    account  = "personal"
    job      = "fugaro-p-standard-github-personal-63f23ec61b6d"
    service_account = {
      account_id   = "fugaro-p-standard-git-63f23ec6"
      display_name = "Fugaro project job standard/github/personal"
    }
    image          = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/base:0.9.0"
    cpu            = "4"
    memory         = "8Gi"
    task_timeout_s = 2820
    env = {
      FUGARO_GITHUB_APP_ID = "123456789"
      FUGARO_PROJECT       = "aurora"
      FUGARO_GCP_PROJECT   = "proj-1234"
      FUGARO_REGION        = "us-east5"
    }
    secret_env = {}
    bucket_condition = {
      title = "fugaro-project-job-runs"
      expression = join(" || ", [
        "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")",
        "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/cache/\")",
        "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/locks/\")",
      ])
    }
    vertex = false
  }
}

run "project_job" {
  command = plan

  module {
    source = "../../modules/project"
  }

  # The launcher grant: exactly the launchers, the job-runner role, on this
  # job alone, and no condition — a job's name is the whole of what limits it.
  assert {
    condition = toset([for m in google_cloud_run_v2_job_iam_member.launcher :
      "${m.name} ${m.role} ${m.member}"]) == toset([
      "fugaro-p-standard-github-personal-63f23ec61b6d projects/proj-1234/roles/fugaroJobRunner user:launcher@example.com",
    ])
    error_message = "every launcher must hold the job-runner role on the project job, and nobody else may"
  }
  assert {
    condition     = alltrue([for m in google_cloud_run_v2_job_iam_member.launcher : length(m.condition) == 0])
    error_message = "a launcher's grant on the job must carry no condition: the job's own name is what it reaches"
  }

  # The operator may act as the job's account, which deploying it needs. The
  # account resource's own `name` is not known until apply, so the binding is
  # pinned by role and member and the account it binds to is pinned separately.
  assert {
    condition = toset([for m in google_service_account_iam_member.operator :
      "${m.role} ${m.member}"]) == toset([
      "roles/iam.serviceAccountUser user:operator@example.com",
    ])
    error_message = "an operator must hold serviceAccountUser on the job's own account"
  }
  assert {
    condition     = google_service_account.job.account_id == "fugaro-p-standard-git-63f23ec6"
    error_message = "the job's account must be gcp.ProjectServiceAccountID of the (profile, provider, account) it serves"
  }

  # The bucket grant: objectUser, and the whole of runs/, cache/ and locks/ —
  # no slug, and the same condition for every profile's account (D4).
  assert {
    condition     = google_storage_bucket_iam_member.job.role == "roles/storage.objectUser"
    error_message = "a project job's account holds objectUser on the runs bucket"
  }
  assert {
    condition = alltrue([for p in ["runs", "cache", "locks"] :
      strcontains(google_storage_bucket_iam_member.job.condition[0].expression,
    "projects/_/buckets/fugaro-runs-proj-1234/objects/${p}/")])
    error_message = "a project job's bucket condition must cover the whole of runs/, cache/ and locks/ (D4, accepted 2026-10-17)"
  }
  assert {
    condition     = !strcontains(google_storage_bucket_iam_member.job.condition[0].expression, "runs/fugaro-")
    error_message = "a project job's bucket condition must name no slug: it serves every repository admitted to its profile"
  }

  # The App's ID, non-secret, in the job's environment (task 3.5 step 4). The
  # private key travels as a mounted secret, never here.
  assert {
    condition = length([for e in google_cloud_run_v2_job.this.template[0].template[0].containers[0].env :
    e if e.name == "FUGARO_GITHUB_APP_ID" && e.value == "123456789"]) == 1
    error_message = "a github project job must carry FUGARO_GITHUB_APP_ID (owner ruling 2026-10-17)"
  }
  assert {
    condition = !anytrue([for e in google_cloud_run_v2_job.this.template[0].template[0].containers[0].env :
    strcontains(lower(e.name), "key") && e.value_source == null && e.value != ""])
    error_message = "no key material may travel in the job's plain environment: the App's key is a mounted secret"
  }
}

# A github job with no App ID fails the plan, and the message says how to
# record one: the runner refuses to build its github provider without it, so
# such a job would halt every run it starts, at run time, in the credential
# (owner ruling of 2026-10-17, task 3.5 step 4).
run "github_job_without_the_app_id_fails_the_plan" {
  command = plan

  module {
    source = "../../modules/project"
  }

  variables {
    job = {
      profile  = "standard"
      provider = "github"
      account  = ""
      job      = "fugaro-p-standard-github-0ca96e64b636"
      service_account = {
        account_id   = "fugaro-p-standard-git-0ca96e64"
        display_name = "Fugaro project job standard/github"
      }
      image          = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/base:0.9.0"
      cpu            = "4"
      memory         = "8Gi"
      task_timeout_s = 2820
      env = {
        FUGARO_PROJECT     = "aurora"
        FUGARO_GCP_PROJECT = "proj-1234"
        FUGARO_REGION      = "us-east5"
      }
      secret_env = {}
      bucket_condition = {
        title      = "fugaro-project-job-runs"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"
      }
      vertex = false
    }
  }

  expect_failures = [google_cloud_run_v2_job.this]
}

# The precondition is the github provider's alone: a profile on another
# provider mounts no GitHub App and must plan without the variable.
run "bitbucket_job_needs_no_app_id" {
  command = plan

  module {
    source = "../../modules/project"
  }

  variables {
    job = {
      profile  = "standard"
      provider = "bitbucket"
      account  = ""
      job      = "fugaro-p-standard-bitbucket-31e1bbd30202"
      service_account = {
        account_id   = "fugaro-p-standard-bit-31e1bbd3"
        display_name = "Fugaro project job standard/bitbucket"
      }
      image          = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/base:0.9.0"
      cpu            = "4"
      memory         = "8Gi"
      task_timeout_s = 2820
      env = {
        FUGARO_PROJECT     = "aurora"
        FUGARO_GCP_PROJECT = "proj-1234"
        FUGARO_REGION      = "us-east5"
      }
      secret_env = {}
      bucket_condition = {
        title      = "fugaro-project-job-runs"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")"
      }
      vertex = false
    }
  }

  assert {
    condition     = google_cloud_run_v2_job.this.name == "fugaro-p-standard-bitbucket-31e1bbd30202"
    error_message = "a non-github project job plans without FUGARO_GITHUB_APP_ID"
  }
}
