# Plans the workspace root against a mock provider: no credentials, no network
# beyond the provider downloads. The names are the ones fugaro update writes
# into terraform.tfvars.json.
#
# What it holds is the root's whole shape: the workspace's own service account,
# the role that names what it reaches — the database's two data permissions, and
# no lifecycle permission — and the ONE grant that binds them. A fourth resource
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
  # terraform does not know it until apply: the override is what makes the runs
  # below able to read the address the root will create. member is overridden
  # with it because the mock provider otherwise derives a random one, which is
  # not the serviceAccount:<address> string IAM accepts and is not what the
  # grant binds.
  override_resource {
    target          = google_service_account.workspace
    override_during = plan
    values = {
      name   = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      email  = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
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

run "the_role_is_the_databases_two_data_permissions_and_no_lifecycle_one" {
  command = plan

  assert {
    condition     = google_project_iam_custom_role.workspace_database.project == "workspace-alpha-1234"
    error_message = "the role is created in the workspace's own project"
  }
  assert {
    condition     = google_project_iam_custom_role.workspace_database.role_id == "fugaroWorkspaceDatabase"
    error_message = "the role id is the one fugaro update names, internal/infra/tf/workspace.go's own pin"
  }
  assert {
    condition     = length(google_project_iam_custom_role.workspace_database.permissions) == 2
    error_message = "the workspace's own service holds exactly two permissions: a third is a decision a person makes here and in internal/infra/tf/workspace.go together, and the instance lifecycle is never one of them"
  }
  assert {
    condition = alltrue([
      contains(google_project_iam_custom_role.workspace_database.permissions, "firebasedatabase.instances.get"),
      contains(google_project_iam_custom_role.workspace_database.permissions, "firebasedatabase.instances.update"),
    ])
    error_message = "the two permissions are the read of the record and the hashes and the write of the meter's reserve and settle (W7)"
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
      name   = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      email  = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = google_project_iam_member.workspace_database.role == "projects/workspace-alpha-1234/roles/fugaroWorkspaceDatabase"
    error_message = "the grant binds the workspace's own custom role, spelled as the literal path internal/infra/tf's cover reads: a predefined role here would be roles/firebasedatabase.admin and its instance lifecycle"
  }
  assert {
    condition     = google_project_iam_member.workspace_database.member == "serviceAccount:fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    error_message = "the grant must be bound to the workspace's own service account: any other member would be a client that can read the record and the admitted-token hashes"
  }
  assert {
    condition     = google_project_iam_member.workspace_database.project == var.project
    error_message = "the grant is on the workspace's own project"
  }
}
