# Plans the workspace root against a mock provider: no credentials, no network
# beyond the provider downloads. The names are the ones fugaro update writes
# into terraform.tfvars.json.
#
# What it holds is the root's whole shape: the workspace's own service account,
# and the ONE grant that reaches the workspace's own database. A third resource
# here would be a workspace holding a member's work (spec §9), and it is refused
# before this root is applied at all: internal/infra/tf's WorkspaceNotCovered
# (workspace.go) is the plan-side half, and internal/budget/rules' own test reads
# this file and holds every grant it makes to the workspace's own account.

mock_provider "google" {}

variables {
  project   = "workspace-alpha-1234"
  workspace = "workspace-alpha"
  names = {
    workspace_account_id = "fugaro-workspace"
  }
}

run "the_workspace_service_account_is_in_the_workspaces_own_project" {
  command = plan

  # The account's email is what the grant's member is computed from, and
  # terraform does not know it until apply: the override is what makes both runs
  # below able to read the address the module will create.
  override_resource {
    target          = google_service_account.workspace
    override_during = plan
    values = {
      name  = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      email = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = google_service_account.workspace.project == "workspace-alpha-1234"
    error_message = "the workspace's service account must be created in the workspace's own project and nowhere else"
  }
  assert {
    condition     = google_service_account.workspace.account_id == "fugaro-workspace"
    error_message = "the account id is the one fugaro update names"
  }
  assert {
    condition     = google_service_account.workspace.email == "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    error_message = "the account's address is <id>@<project>.iam.gserviceaccount.com, in the workspace's own project"
  }
  assert {
    condition     = output.workspace_service_account == google_service_account.workspace.email
    error_message = "the root must output the account W6 deploys the workspace's bundle onto"
  }
}

run "the_only_grant_is_the_workspace_database_role_on_the_workspace_service" {
  command = plan

  # The same override as the run above, which each run block needs of its own:
  # a run is a plan of the root by itself.
  override_resource {
    target          = google_service_account.workspace
    override_during = plan
    values = {
      name  = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      email = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = google_project_iam_member.workspace_database.role == "roles/firebasedatabase.admin"
    error_message = "the workspace's own service reaches its database with the whole-database role: Realtime Database IAM has no path-level scope (spec §8)"
  }
  assert {
    condition     = google_project_iam_member.workspace_database.member == "serviceAccount:${google_service_account.workspace.email}"
    error_message = "the grant must be bound to the workspace's own service account: any other member would be a client that can read the record and the admitted-token hashes"
  }
  assert {
    condition     = google_project_iam_member.workspace_database.project == var.project
    error_message = "the grant is on the workspace's own project"
  }
}
