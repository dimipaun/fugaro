# A secret container that EXISTS keeps the replication it has (D3082, F9, R5):
# the apply below creates the installation's project-wide containers in
# us-east5, and the plan after it moves the installation's region to
# europe-west1 and finds the containers where they are. That is the whole of
# what ignore_changes on the field buys.
#
# It is a file of its own rather than two more runs of installation.tftest.hcl
# for two reasons: the pair applies the module, so it needs computed names the
# mock provider is given below, and those defaults would be visible to every
# other run in that file (several of them assert members of their own accounts,
# which an override_resource gives them there); and a file is its own test run,
# so the state the apply leaves behind reaches nothing else.
#
# What this cannot say is the AUTOMATIC container, which is what every
# container created before this declaration is: no hermetic test can hold a real
# container, and the mock provider plans the config's own value rather than a
# replacement of what is in state. The automatic, adopted and mixed cases are
# planned in internal/infra against a synthetic prior state with the real
# provider (TestAnExistingSecretKeepsItsOwnReplication and its neighbours), and
# what the real provider does to an adopted container is U9, measured by Check
# 821 after the release (O11).

mock_provider "google" {
  # An IAM member is validated as a member string, so the accounts' computed
  # name, email and member have to be readable during the apply.
  mock_resource "google_service_account" {
    defaults = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-mock@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-mock@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-mock@proj-1234.iam.gserviceaccount.com"
    }
  }
}

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
}

run "the_containers_are_created_in_the_region" {
  command = apply

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
    condition = alltrue([for s in google_secret_manager_secret.project :
      length(s.replication) == 1 && length(s.replication[0].user_managed) == 1 &&
    s.replication[0].user_managed[0].replicas[0].location == "us-east5"])
    error_message = "a container created from now on is replicated in the installation's region (D3082)"
  }
}

run "an_existing_container_keeps_its_own_region" {
  command = plan

  module {
    source = "../../modules/installation"
  }

  variables {
    region = "europe-west1"
    project_secrets = {
      "anthropic-api-key" = "fugaro-aurora-anthropic-api-key-0f0f0f0f0f0f0f0f"
      "bitbucket-token"   = "fugaro-aurora-bitbucket-token-1e1e1e1e1e1e1e1e"
    }
  }

  assert {
    condition = alltrue([for s in google_secret_manager_secret.project :
      length(s.replication) == 1 && length(s.replication[0].user_managed) == 1 &&
    s.replication[0].user_managed[0].replicas[0].location == "us-east5"])
    error_message = "an existing container must keep the region it was created in, not be moved to the installation's new one (D3082)"
  }
}
