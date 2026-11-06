# The verifier service account (CSR14, C3.9g1, D1068) against a mock provider:
# no credentials, no network beyond the provider download.
#
# The subject is the installation's own verifier identity, and what the four
# runs below pin is the whole of it:
#
#   * the account exists exactly while the credentials service does, created by
#     the same apply, with `account_id` a LITERAL — which is what makes its
#     address a fact of the configuration (`fugaro-verifier@<project>.iam
#     .gserviceaccount.com`, computable by internal/infra before anything is
#     planned) rather than of the apply, and so what lets one ordinary `fugaro
#     update` name the account this same apply creates (CLAUDE.md's HARD RULE);
#   * the service's own environment names it in the ONE state the account is a
#     default in — the block's `verifier_accounts` null, the member absent —
#     and the three states stay apart: the owner's list is rendered exactly as
#     it is with the account NOT added (a union would admit an identity the
#     owner cannot remove with `[]`, and would put CSR15's launcher and the
#     service on two different identities), and an explicit `[]` is rendered as
#     nobody;
#   * its whole reach is two rows: `roles/run.invoker` on the credentials
#     service, and the operators' `roles/iam.serviceAccountTokenCreator` on IT
#     (the impersonation the owner's `gcloud auth print-identity-token
#     --impersonate-service-account=…` and the launcher's own mint stand on).
#     Nothing else in the module names the account: no bucket, no secret, no
#     project role, no job, and no other identity may act as it.
#
# What this file cannot see is the exact SET of grants in the tree — a tftest
# cannot enumerate a module's resources, so a fifth grant beside these is
# invisible here. That gate is internal/infra/managed's scan, which pins every
# grant to a member that is not a set of people whole (runAccountGrants), holds
# the verifier account's two rows and reports both a grant no row holds and a
# row no module grants any more. What this file adds is the shape of each row:
# the role, the member, the account the impersonation is on, and the one state
# the environment entry is rendered from.

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
  # The installation that opted in and has not written the list yet, which is
  # every installation the release this task ships into: the block is rendered
  # with no `verifier_accounts` member, and that absence is the state the
  # installation's own account is the default in. The runs below that write the
  # member restate the whole block: a run's `variables` block replaces the
  # file's value of the variable rather than merging into it.
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

# An installation that never opted in is byte for byte the installation it was:
# no account, no grant on the service, no impersonation of it. This is the half
# of the guard the credentials service's own opt-in is, and it is what keeps the
# change unplannable for a project that filed the service away.
run "no_verifier_without_the_service" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    credentials_service = null
  }

  assert {
    condition = (
      length(google_service_account.fugaro_verifier) == 0 &&
      length(google_cloud_run_v2_service_iam_member.credentials_verifier) == 0 &&
      length(google_service_account_iam_member.fugaro_verifier_token_creator) == 0 &&
      length(google_cloud_run_v2_service.credentials) == 0
    )
    error_message = "without credentials_service the installation must declare no verifier account, no invoker grant on the service, no impersonation of the account and no service for the environment entry to be rendered on"
  }
  assert {
    condition     = alltrue([for k, g in google_service_account_iam_member.fugaro_deployer_token_creator : !strcontains(g.service_account_id, "fugaro-verifier")])
    error_message = "with the service off there is no verifier account to impersonate, so no grant may name one"
  }
}

# The default: the block names no verifier and the service's own environment
# carries the account this apply creates, ALONE. This is the state one ordinary
# `fugaro update` leaves an opted-in installation in — the same run writes the
# address into the owner's config and the policy node — and it is the state the
# whole task exists for: the account is admitted by the apply that creates it,
# with no second run nobody was told to make.
run "the_default_is_the_installation_own_account" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  # The grants below are written from the account's computed member, so the mock
  # provider has to give it one during the plan for them to be readable.
  override_resource {
    target          = google_service_account.fugaro_verifier[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-verifier@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-verifier@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-verifier@proj-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition = (length(google_service_account.fugaro_verifier) == 1 &&
      google_service_account.fugaro_verifier[0].account_id == "fugaro-verifier" &&
      google_service_account.fugaro_verifier[0].project == "proj-1234" &&
    google_service_account.fugaro_verifier[0].display_name == "Fugaro verifier")
    error_message = "the account is the installation's own, `fugaro-verifier` in this project: the ID is a LITERAL in the module, which is what makes the address computable by internal/infra before anything is planned, and that is what lets one ordinary update both create the account and name it"
  }
  assert {
    condition = jsondecode(one([
      for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.value
      if e.name == "FUGARO_VERIFIER_ACCOUNTS"
    ])) == ["fugaro-verifier@proj-1234.iam.gserviceaccount.com"]
    error_message = "with no verifier_accounts member the service's entry must carry the installation's own verifier account ALONE: the account is a default and never an addition, so a block that names none admits exactly this one caller"
  }
  assert {
    condition = (length(google_cloud_run_v2_service_iam_member.credentials_verifier) == 1 &&
      google_cloud_run_v2_service_iam_member.credentials_verifier[0].role == "roles/run.invoker" &&
      google_cloud_run_v2_service_iam_member.credentials_verifier[0].member == google_service_account.fugaro_verifier[0].member &&
      google_cloud_run_v2_service_iam_member.credentials_verifier[0].name == google_cloud_run_v2_service.credentials[0].name &&
    google_cloud_run_v2_service_iam_member.credentials_verifier[0].location == var.region)
    error_message = "the account's one grant is roles/run.invoker on the credentials service and nothing else: without it the front end refuses the verifier's ID token before the container runs (invoker_iam_disabled false), so the one caller class the route is built for could never reach it"
  }
  assert {
    condition = alltrue([
      for k, g in google_service_account_iam_member.fugaro_verifier_token_creator :
      k == "user:operator@example.com" &&
      g.role == "roles/iam.serviceAccountTokenCreator" &&
      g.member == "user:operator@example.com" &&
      g.service_account_id == "projects/proj-1234/serviceAccounts/fugaro-verifier@proj-1234.iam.gserviceaccount.com"
    ]) && length(google_service_account_iam_member.fugaro_verifier_token_creator) == 1
    error_message = "the operators, and nobody else, may mint an ID token for the verifier account, on the account's own literal path: that path is what the deployer's grant is spelled with for the same reason (the account resource's name is unknown at plan time), and a grant whose account nobody can name is a grant nobody can check"
  }
  assert {
    condition = length(concat(
      [for k, g in google_service_account_iam_member.scheduler_user : k if strcontains(g.service_account_id, "fugaro-verifier")],
      [for k, g in google_service_account_iam_member.fugaro_deployer_token_creator : k if strcontains(g.service_account_id, "fugaro-verifier")],
    )) == 0
    error_message = "the verifier account must be impersonable through no other grant: the operators' token-creator row above is the whole of who may become it, and a second row — the scheduler account's or the deployer's — would be a second identity reading the verify route as the installation"
  }
  assert {
    condition = (
      length([for k, g in google_storage_bucket_iam_member.runs : k if g.member == google_service_account.fugaro_verifier[0].member]) == 0 &&
      length([for k, g in google_secret_manager_secret_iam_member.operator : k if g.member == google_service_account.fugaro_verifier[0].member]) == 0 &&
      length([for k, g in google_project_iam_member.launcher : k if g.member == google_service_account.fugaro_verifier[0].member]) == 0 &&
      length([for k, g in google_cloud_run_v2_service_iam_member.credentials_invoker : k if g.member == google_service_account.fugaro_verifier[0].member]) == 0
    )
    error_message = "the verifier account holds nothing but the invoker grant on the service: no member of the runs bucket, none of the project's secret containers, no project role, and not the read mint's invoker row either. An account created for one route that can reach anything else is an identity an operator can become and walk with"
  }
}

# The owner's own list, rendered as it is. The account is NOT added to it: an
# owner who names their own verifier keeps the service admitting that one alone,
# which is the state CSR15's launcher mints as and the state D1075 makes theirs
# to write.
run "the_owners_list_is_rendered_as_it_is" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  override_resource {
    target          = google_service_account.fugaro_verifier[0]
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-verifier@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-verifier@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-verifier@proj-1234.iam.gserviceaccount.com"
    }
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
      verifier_accounts = ["person-1@example.com", "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"]
    }
  }

  assert {
    condition = jsondecode(one([
      for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.value
      if e.name == "FUGARO_VERIFIER_ACCOUNTS"
    ])) == ["person-1@example.com", "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"]
    error_message = "a written verifier_accounts must be rendered exactly as it is, with the installation's own account NOT added: a union would admit an identity the owner cannot remove with [], and it would leave the service and CSR15's launcher on two different identities in every installation whose owner wrote their own list"
  }
  # The account still EXISTS here — it is created by the same apply, and the
  # operators' impersonation of it is what the owner's own check runs — while
  # the service admits the owner's list and not it. The two facts are separate
  # on purpose: the account is a caller class this installation may mint for,
  # and the list is who the route admits.
  assert {
    condition     = length(google_service_account.fugaro_verifier) == 1 && length(google_cloud_run_v2_service_iam_member.credentials_verifier) == 1
    error_message = "the account and its invoker grant exist in every state of the list: only the environment entry follows the list, and an installation whose owner wrote their own verifier still has the account its operators may impersonate"
  }
}

# The owner's clear, which is the state a union would get wrong: `[]` means
# nobody at all, and the entry is rendered as a PRESENT empty array rather than
# dropped — an absent member is the state the default above is for, so a clear
# that dropped the key would hand the service exactly the caller the owner
# switched off.
run "the_owners_clear_admits_nobody" {
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
      verifier_accounts = []
    }
  }

  assert {
    condition = (length([
      for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e
      if e.name == "FUGARO_VERIFIER_ACCOUNTS"
      ]) == 1 && jsondecode(one([
        for e in google_cloud_run_v2_service.credentials[0].template[0].containers[0].env : e.value
        if e.name == "FUGARO_VERIFIER_ACCOUNTS"
    ])) == [])
    error_message = "an explicit verifier_accounts = [] must reach the service as a present empty list — nobody — and never as an absent entry: the absence is the module's own default, the account this installation creates, so a clear that dropped the key would switch the route back on to exactly the caller the owner cleared"
  }
  assert {
    condition     = length(google_service_account.fugaro_verifier) == 1
    error_message = "the account is still created (and still impersonable by the operators — the row above is about the service's admitted list, not about who may mint for the account); what an empty list means is that the service admits nobody, not that the installation has no verifier identity"
  }
}
