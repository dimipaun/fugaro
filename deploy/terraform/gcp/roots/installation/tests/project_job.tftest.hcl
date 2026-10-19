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
    token_condition = {
      title      = "fugaro-project-tokens-fugaro-p-standard-git-63f23ec6"
      expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
    }
    vertex = false
  }
}

run "project_job" {
  command = plan

  module {
    source = "../../modules/project"
  }

  # The account's computed identity, fixed at plan time, so the grants below
  # can be pinned to *this* account rather than to whatever member they name.
  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-p-standard-git-63f23ec6@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-p-standard-git-63f23ec6@proj-1234.iam.gserviceaccount.com"
    }
  }

  # Every grant to the job's account is to this account, and the operator's
  # actAs is on this account: none may name another (a launcher, a wildcard,
  # a neighbour's account).
  assert {
    condition     = google_storage_bucket_iam_member.job.member == "serviceAccount:fugaro-p-standard-git-63f23ec6@proj-1234.iam.gserviceaccount.com"
    error_message = "the runs-bucket grant must be to the job's own account"
  }
  assert {
    condition = alltrue([for m in google_service_account_iam_member.operator :
    m.service_account_id == "projects/proj-1234/serviceAccounts/fugaro-p-standard-git-63f23ec6@proj-1234.iam.gserviceaccount.com"])
    error_message = "an operator's serviceAccountUser must be on the job's own account and no other"
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
  # And exactly that condition, byte for byte: three prefix clauses and
  # nothing else, so no clause can widen it (an `|| true`, another prefix,
  # the bucket root) and none can be dropped.
  assert {
    condition = google_storage_bucket_iam_member.job.condition[0].expression == join(" || ", [
      "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")",
      "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/cache/\")",
      "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/locks/\")",
    ])
    error_message = "a project job's bucket condition must be exactly the three prefix clauses gcp.ProjectBucketCondition writes"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job.bucket == "fugaro-runs-proj-1234" && google_storage_bucket_iam_member.job.condition[0].title == "fugaro-project-job-runs"
    error_message = "a project job's bucket grant must be on the runs bucket, under the title internal/infra gave it"
  }
  # The tokens/ grant (the seal, #563): objectViewer, never objectUser, so the
  # account can read a queued run's sealed token and can neither delete nor
  # replace it; the whole of tokens/ in one clause, to this account alone.
  assert {
    condition     = google_storage_bucket_iam_member.job_tokens.role == "roles/storage.objectViewer" && google_storage_bucket_iam_member.job_tokens.bucket == "fugaro-runs-proj-1234"
    error_message = "a project job's tokens/ grant must be read-only (objectViewer) on the runs bucket"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job_tokens.member == "serviceAccount:fugaro-p-standard-git-63f23ec6@proj-1234.iam.gserviceaccount.com"
    error_message = "the tokens/ grant must be to the job's own account"
  }
  assert {
    condition     = google_storage_bucket_iam_member.job_tokens.condition[0].expression == "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")" && google_storage_bucket_iam_member.job_tokens.condition[0].title == "fugaro-project-tokens-fugaro-p-standard-git-63f23ec6"
    error_message = "a project job's tokens/ condition must be exactly gcp.ProjectTokenBucketCondition, under the title internal/infra gave it"
  }
  # The rest of the job account's grant surface, for a profile that mounts no
  # secret and does not use Vertex AI: nothing.
  assert {
    condition     = length(google_secret_manager_secret_iam_member.job) == 0 && length(google_project_iam_member.vertex) == 0
    error_message = "a profile with no secrets and no Vertex AI grants its account no secret and no project role"
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
    # A plain env entry is one with no value_source block at all: an empty
    # value_source is a list, so it is neither null nor absent, and testing
    # for null here would make this assertion true of every entry.
    condition = !anytrue([for e in google_cloud_run_v2_job.this.template[0].template[0].containers[0].env :
    strcontains(lower(e.name), "key") && length(e.value_source) == 0 && e.value != ""])
    error_message = "no key material may travel in the job's plain environment: the App's key is a mounted secret"
  }
}

# An account job's one secret, and the whole of it: CLAUDE_CODE_OAUTH_TOKEN
# mounted from its own account's project-wide copy, the account's
# secretAccessor member on that copy, and nothing else (design
# project-named-oauth-tokens §4.3 and D2; plan task 2).
#
# This is the carrier the credentials service replaces, so what the review
# reads here is the complete surface: one secret_env entry, one member, on
# this job's own account. The three copies are in var.secrets, exactly as the
# installation passes every project-wide container it holds — a neighbour's
# token among them — so the assertions below are about what this job's account
# may reach, not about what exists.
#
# Mutation (run, restore): add the other accounts' logical names to
# job.secret_env (the plan's red step, one level up from the deployer's):
# the member and the mount assertions both fail.
run "account_job_reads_only_own_secret" {
  command = plan

  module {
    source = "../../modules/project"
  }

  variables {
    secrets = {
      claude-oauth-token               = "fugaro-project-aurora-claude-oauth-token-b2904480b41a631b"
      claude-oauth-token-acme-personal = "fugaro-project-aurora-claude-oauth-token-acme-personal-93bc900e5b996e65"
      claude-oauth-token-acme-work     = "fugaro-project-aurora-claude-oauth-token-acme-work-ac0b8ecc6c56414f"
    }
    job = {
      profile  = "standard"
      provider = "github"
      account  = "acme-personal"
      job      = "fugaro-p-standard-github-acme-person-1c8f9d6ace37"
      service_account = {
        account_id   = "fugaro-p-standard-git-1c8f9d6a"
        display_name = "Fugaro project job standard/github/acme-personal"
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
      secret_env = {
        CLAUDE_CODE_OAUTH_TOKEN = "claude-oauth-token-acme-personal"
      }
      bucket_condition = {
        title = "fugaro-project-job-runs"
        expression = join(" || ", [
          "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/runs/\")",
          "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/cache/\")",
          "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/locks/\")",
        ])
      }
      token_condition = {
        title      = "fugaro-project-tokens-fugaro-p-standard-git-1c8f9d6a"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
      }
      vertex = false
    }
  }

  override_resource {
    target          = google_service_account.job
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-p-standard-git-1c8f9d6a@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-p-standard-git-1c8f9d6a@proj-1234.iam.gserviceaccount.com"
    }
  }

  # Exactly one member, secretAccessor, on this account's copy, to this job's
  # own account. One token, one job, one service account: the account's token
  # is readable by the runs in that account's job and by no other job's.
  assert {
    condition = toset([for m in google_secret_manager_secret_iam_member.job :
      "${m.role} ${m.secret_id} ${m.member}"]) == toset([
      "roles/secretmanager.secretAccessor fugaro-project-aurora-claude-oauth-token-acme-personal-93bc900e5b996e65 serviceAccount:fugaro-p-standard-git-1c8f9d6a@proj-1234.iam.gserviceaccount.com",
    ])
    error_message = "an account job's account must hold secretAccessor on its own account's token and on nothing else"
  }
  # The neighbouring accounts' copies are in var.secrets and this job's
  # account reaches none of them: a job that could read another account's
  # token would make the account segment of its name a label rather than a
  # boundary.
  assert {
    condition = !anytrue([for m in google_secret_manager_secret_iam_member.job :
      contains([
        "fugaro-project-aurora-claude-oauth-token-b2904480b41a631b",
        "fugaro-project-aurora-claude-oauth-token-acme-work-ac0b8ecc6c56414f",
    ], m.secret_id)])
    error_message = "an account job's account must not reach another account's token, nor the default account's"
  }

  # The mount: one secret variable, CLAUDE_CODE_OAUTH_TOKEN, from this
  # account's own copy at version latest — and no other secret variable at
  # all, which is what makes the removal of this entry the whole of the swap.
  assert {
    condition = toset([for e in google_cloud_run_v2_job.this.template[0].template[0].containers[0].env :
    e.name if length(e.value_source) > 0]) == toset(["CLAUDE_CODE_OAUTH_TOKEN"])
    error_message = "an account job must mount exactly one secret variable: its own account's token"
  }
  assert {
    condition = length([for e in google_cloud_run_v2_job.this.template[0].template[0].containers[0].env :
      e if e.name == "CLAUDE_CODE_OAUTH_TOKEN" &&
      e.value_source[0].secret_key_ref[0].secret == "fugaro-project-aurora-claude-oauth-token-acme-personal-93bc900e5b996e65" &&
    e.value_source[0].secret_key_ref[0].version == "latest"]) == 1
    error_message = "CLAUDE_CODE_OAUTH_TOKEN must be mounted from this account's own project-wide copy, at version latest"
  }
  # The token travels as a mounted secret and never as a plain value.
  assert {
    condition = !anytrue([for e in google_cloud_run_v2_job.this.template[0].template[0].containers[0].env :
    strcontains(upper(e.name), "OAUTH") && length(e.value_source) == 0 && e.value != ""])
    error_message = "no token may travel in the job's plain environment: the mount is the only carrier"
  }

  # The job is the account's own, name and account id together: the name is
  # what the runner matches, and the account segment of it is what separates
  # one account's job from another's.
  assert {
    condition     = google_cloud_run_v2_job.this.name == "fugaro-p-standard-github-acme-person-1c8f9d6ace37"
    error_message = "an account job's name must be gcp.ProjectJobName of its (profile, provider, account)"
  }
  assert {
    condition     = google_service_account.job.account_id == "fugaro-p-standard-git-1c8f9d6a"
    error_message = "an account job's account must be gcp.ProjectServiceAccountID of the (profile, provider, account) it serves"
  }
  # The job says which account it serves, in the label the log sink and
  # exclusion select on: an account's logs must be attributable to it.
  assert {
    condition     = google_cloud_run_v2_job.this.labels["fugaro_account"] == "acme-personal"
    error_message = "an account job's fugaro_account label must name the account it serves"
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
      token_condition = {
        title      = "fugaro-project-tokens-fugaro-p-standard-git-0ca96e64"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
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
      token_condition = {
        title      = "fugaro-project-tokens-fugaro-p-standard-bit-31e1bbd3"
        expression = "resource.name.startsWith(\"projects/_/buckets/fugaro-runs-proj-1234/objects/tokens/\")"
      }
      vertex = false
    }
  }

  assert {
    condition     = google_cloud_run_v2_job.this.name == "fugaro-p-standard-bitbucket-31e1bbd30202"
    error_message = "a non-github project job plans without FUGARO_GITHUB_APP_ID"
  }
}
