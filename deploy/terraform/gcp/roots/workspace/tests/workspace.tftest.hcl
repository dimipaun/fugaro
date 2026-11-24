# Plans the workspace root against a mock provider: no credentials, no network
# beyond the provider downloads. The names are the ones fugaro update writes
# into terraform.tfvars.json.
#
# What it holds is the root's whole shape: the workspace's two accounts — the
# service the workspace's bundle runs as, and the SA fallback's IAM account (WU3,
# D2803) — the role that names what the service reaches (the database's two data
# permissions, and no lifecycle permission) and the ONE grant that binds them. A
# resource outside those would be a workspace holding a member's work (spec §9),
# and it is refused before this root is applied at all: internal/infra/tf's
# WorkspaceNotCovered (workspace.go) is the plan-side half, and
# internal/budget/rules' own test reads this file and holds every grant it makes
# to the workspace's own account.

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

run "the_iam_account_is_not_the_service_account_and_terraform_makes_no_key_for_it" {
  command = plan

  # The SA fallback's account (WU3, D2803): the identity a member's owner grants
  # roles/owner on their project, which the workspace's admins impersonate. It is
  # a SECOND account and never the service above (F20): the service reads the
  # workspace's own database and the members' ledgers, and this one holds
  # roles/owner on every linked member, so a compromise of either is not the
  # other's reach.
  override_resource {
    target          = google_service_account.workspace
    override_during = plan
    values = {
      name   = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      email  = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    }
  }

  override_resource {
    target          = google_service_account.workspace_iam
    override_during = plan
    values = {
      name   = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace-iam@workspace-alpha-1234.iam.gserviceaccount.com"
      email  = "fugaro-workspace-iam@workspace-alpha-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-workspace-iam@workspace-alpha-1234.iam.gserviceaccount.com"
    }
  }

  assert {
    condition     = google_service_account.workspace_iam.project == "workspace-alpha-1234"
    error_message = "the SA fallback's IAM account must be created in the workspace's own project and nowhere else"
  }
  assert {
    condition     = google_service_account.workspace_iam.account_id == "fugaro-workspace-iam"
    error_message = "the account id is the literal internal/infra/tf pins (WorkspaceIAMAccountID): the cover holds this account by its id, so a rename here leaves every plan of the root refused"
  }
  assert {
    condition     = google_service_account.workspace_iam.email != google_service_account.workspace.email
    error_message = "the SA fallback's account and the workspace's own service must be two accounts and never one (F20): one account that both reads the workspace's database and holds roles/owner on every member is the whole of the workspace's reach in a single identity's compromise"
  }
  assert {
    condition     = output.workspace_iam_account == google_service_account.workspace_iam.email
    error_message = "the root must output the SA fallback's account as workspace_iam_account: that is the name C2's contract fixes and the one WU20's SA authority reads the account by"
  }
  # There is no assertion here that NO key exists, and that is deliberate: this
  # is the root's own resources, and what it says about keys is that THIS ROOT
  # WRITES NONE — no google_service_account_key in this file, and no grant of any
  # kind naming the account (its token creators are the workspace admins, written
  # live by `workspace user add --admin`, D2818). A key can exist anyway on a
  # project Fugaro does not own alone, by two paths this root has no control
  # over: iam.serviceAccounts.actAs, which roles/editor and
  # roles/iam.serviceAccountUser carry, and iam.serviceAccountKeys.create, which
  # roles/editor resolves to. The organization policy
  # constraints/iam.disableServiceAccountKeyCreation is the only control that
  # closes the second, and WU11's `status` is what takes both up.
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

run "the_database_grant_is_the_custom_role_on_the_workspace_service" {
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

# The service half of the root (WS6, D2980, R9), planned with the variables the
# workspace owner's `fugaro update` writes: null — the default the runs above
# plan with — creates none of it, and one image, one region and one member table
# create the service, its ONE bundle accessor and one invoke grant per member job
# account. The same variables carry the workspace's roster (WC2, R6, D2971), and
# with a non-empty people list they add one invoke grant per person and the ONE
# conditioned accessor the workspace's own account holds on a person's own Claude
# token.
#
# What these runs hold is what the Go-side cover reads a plan for
# (internal/infra/tf's WorkspaceServiceEnv, workspaceService and
# workspaceBundleAccessor): the identity the service runs as, R9's three numbers,
# the bundle container, and the members the grants are bound to. The cover is the
# half that refuses an operator's hand-edited tfvars; this is the half that fails
# on a hand-edited main.tf.

run "the_service_is_off_until_the_owner_deploys_it" {
  command = plan

  assert {
    condition     = length(google_cloud_run_v2_service.workspace) == 0
    error_message = "this root creates no service, no bundle accessor and no invoke grant while var.credentials_service is unset: the account, the role and its grant are the whole of it, and a member's run keeps reaching the workspace's bundle through its own project's service"
  }
  assert {
    condition     = length(google_secret_manager_secret_iam_member.workspace_bundle) == 0
    error_message = "no accessor may exist without the service that reads it"
  }
  assert {
    condition     = length(google_cloud_run_v2_service_iam_member.workspace_invoker) == 0
    error_message = "no invoke grant may exist without the service it is bound to"
  }
  assert {
    condition     = length(google_cloud_run_v2_service_iam_member.workspace_person_invoker) == 0
    error_message = "no roster person's invoke grant may exist without the service it is bound to either (R6, D2971)"
  }
  assert {
    condition     = length(google_project_iam_member.workspace_person_token) == 0
    error_message = "no conditioned accessor may exist without the service that reads a person's token through it (R6)"
  }
}

run "the_service_runs_as_the_workspaces_own_account_inside_r9s_limits" {
  command = plan

  variables {
    credentials_service = {
      service          = "fugaro-workspace-credentials"
      image            = "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
      region           = "us-central1"
      bundle_secret    = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
      firebase_project = "workspace-alpha-1234"
      rtdb_url         = "https://workspace-alpha-1234.firebaseio.com"
    }
    members = {
      "member-alpha" = {
        job_service_accounts = ["fugaro-run-alpha@member-alpha-1234.iam.gserviceaccount.com"]
      }
      "member-beta" = {
        job_service_accounts = [
          "fugaro-run-beta@member-beta-5678.iam.gserviceaccount.com",
          "fugaro-shared-beta@member-beta-5678.iam.gserviceaccount.com",
        ]
      }
    }
    # The roster's people, as fugaro update renders them from the workspace's own
    # roster node: the addresses the root binds one invoke grant to each (R6,
    # D2971).
    people = ["ada@example.com", "grace@example.org"]
  }

  # The account's email and member are what the service's runtime identity and
  # the accessor are written from, and terraform does not know either until
  # apply: the override is the account the root will create. uri is overridden
  # with it too, so the output the update reads back is a URL rather than a
  # mock's own random one.
  override_resource {
    target          = google_service_account.workspace
    override_during = plan
    values = {
      name   = "projects/workspace-alpha-1234/serviceAccounts/fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      email  = "fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-workspace@workspace-alpha-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_cloud_run_v2_service.workspace
    override_during = plan
    values = {
      uri = "https://fugaro-workspace-credentials-abc.a.run.app"
    }
  }

  assert {
    condition     = length(google_cloud_run_v2_service.workspace) == 1
    error_message = "the workspace deploys ONE service: its own credentials service"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].name == "fugaro-workspace-credentials"
    error_message = "the service's name is the one the update names, which is what the members' invoke grants are bound to"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].project == var.project
    error_message = "the workspace's own service is a service of the workspace's own project and of no other"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].location == "us-central1"
    error_message = "the service is deployed to the region the update names"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].invoker_iam_disabled == false
    error_message = "IAM is the first fence every caller passes, and the process's own ID-token check is layered on top of it: with the setting on, anyone reaches the process that reads every model credential the workspace holds"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].template[0].service_account == google_service_account.workspace.email
    error_message = "the service runs as the workspace's own account, which is the ONE identity the bundle accessor below grants: any other runtime identity reads the workspace's own credentials"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].template[0].max_instance_request_concurrency == 40
    error_message = "R9 states a concurrency of forty per instance"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].template[0].scaling[0].max_instance_count == 10
    error_message = "R9 states a maximum of ten instances"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].template[0].scaling[0].min_instance_count == 0
    error_message = "the service keeps no instance warm: a member's run pays a cold start rather than the workspace paying around the clock"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].scaling[0].min_instance_count == 0
    error_message = "the service-level minimum is written out literally at zero as well: the managed gate prices a Cloud Run service by its minimum instance count and an attribute the provider computes is unknown at plan time"
  }
  assert {
    condition     = google_cloud_run_v2_service.workspace[0].template[0].containers[0].ports[0].name == "h2c"
    error_message = "the port is named h2c: a member's run reaches this service for its git traffic too, and the front end in front of an HTTP/1 container refuses a large git pack (413)"
  }
  assert {
    condition = sort([
      for e in google_cloud_run_v2_service.workspace[0].template[0].containers[0].env : e.name
      ]) == sort([
      "FUGARO_WORKSPACE_MODE",
      "FUGARO_PROJECT",
      "FUGARO_GCP_PROJECT",
      "FUGARO_REGION",
      "FUGARO_RTDB_URL",
      "FUGARO_FIREBASE_PROJECT",
      "FUGARO_CREDENTIALS_SERVICE_URL",
    ])
    error_message = "the container sets exactly the names internal/infra/tf's WorkspaceServiceEnv admits, and no credential and no secret ID is among them"
  }
  assert {
    condition = [
      for e in google_cloud_run_v2_service.workspace[0].template[0].containers[0].env : e.value
      if e.name == "FUGARO_WORKSPACE_MODE"
    ] == [var.workspace]
    error_message = "FUGARO_WORKSPACE_MODE is what tells this service from an installation's (the same image, the same code), and it names the workspace it serves"
  }
  assert {
    condition = [
      for e in google_cloud_run_v2_service.workspace[0].template[0].containers[0].env : e.value
      if e.name == "FUGARO_CREDENTIALS_SERVICE_URL"
    ] == ["https://workspace-credentials-url-unassigned.invalid"]
    error_message = "the apply that creates the service carries the unassigned placeholder: Cloud Run assigns the URL, and a revision that guessed its own audience would accept a token minted for another service"
  }
  assert {
    condition     = output.workspace_service_url == "https://fugaro-workspace-credentials-abc.a.run.app"
    error_message = "the root outputs the service's URL, which the update writes back into credentials_service.url on the apply after the one that created the service"
  }

  assert {
    condition     = length(google_secret_manager_secret_iam_member.workspace_bundle) == 1
    error_message = "the workspace's service has ONE accessor and no more: every model credential the workspace holds is one document in one container"
  }
  assert {
    condition     = google_secret_manager_secret_iam_member.workspace_bundle[0].secret_id == var.credentials_service.bundle_secret
    error_message = "the accessor is on the workspace's own bundle container: the container is derived from the workspace's name, and a second derivation here would be a second container"
  }
  assert {
    condition     = google_secret_manager_secret_iam_member.workspace_bundle[0].role == "roles/secretmanager.secretAccessor"
    error_message = "the accessor is the read of the container's versions and nothing else"
  }
  assert {
    condition     = google_secret_manager_secret_iam_member.workspace_bundle[0].member == google_service_account.workspace.member
    error_message = "the container is read by the workspace's own account and by nobody else"
  }

  assert {
    condition     = length(google_cloud_run_v2_service_iam_member.workspace_invoker) == 3
    error_message = "the root makes ONE invoke grant per member job account, and the member table above names three"
  }
  assert {
    condition = alltrue([
      for g in values(google_cloud_run_v2_service_iam_member.workspace_invoker) :
      g.role == "roles/run.invoker" && g.name == "fugaro-workspace-credentials" && g.project == var.project
    ])
    error_message = "every invoke grant is roles/run.invoker on the workspace's own service in the workspace's own project"
  }
  assert {
    condition = sort([
      for g in values(google_cloud_run_v2_service_iam_member.workspace_invoker) : g.member
      ]) == sort([
      "serviceAccount:fugaro-run-alpha@member-alpha-1234.iam.gserviceaccount.com",
      "serviceAccount:fugaro-run-beta@member-beta-5678.iam.gserviceaccount.com",
      "serviceAccount:fugaro-shared-beta@member-beta-5678.iam.gserviceaccount.com",
    ])
    error_message = "the grants are bound to the member table's own job accounts and to no other principal"
  }

  # The roster's people (WC2, R6, D2971): one invoke grant per human the roster
  # names, on the same service and in the same project, and the ONE conditioned
  # accessor the workspace's own account holds on a person's own Claude token.
  assert {
    condition     = length(google_cloud_run_v2_service_iam_member.workspace_person_invoker) == 2
    error_message = "the root makes ONE invoke grant per person of the workspace's roster, and the people above name two"
  }
  assert {
    condition = alltrue([
      for g in values(google_cloud_run_v2_service_iam_member.workspace_person_invoker) :
      g.role == "roles/run.invoker" && g.name == "fugaro-workspace-credentials" && g.project == var.project
    ])
    error_message = "every person's invoke grant is roles/run.invoker on the workspace's own service in the workspace's own project"
  }
  assert {
    condition = sort([
      for g in values(google_cloud_run_v2_service_iam_member.workspace_person_invoker) : g.member
      ]) == sort([
      "user:ada@example.com",
      "user:grace@example.org",
    ])
    error_message = "the grants are bound to the roster's own addresses as user:<address> and to no other principal: a bare address, a serviceAccount: or a group here is a caller the roster never named"
  }
  assert {
    condition     = length(google_project_iam_member.workspace_person_token) == 1
    error_message = "the workspace's own account holds ONE conditioned accessor on a person's token (R6), and a per-person container cannot be written here at all: its name is the verified token's own sub"
  }
  assert {
    condition     = google_project_iam_member.workspace_person_token[0].role == "roles/secretmanager.secretAccessor"
    error_message = "the conditioned accessor is the read of a container's versions and nothing else"
  }
  assert {
    condition     = google_project_iam_member.workspace_person_token[0].member == google_service_account.workspace.member
    error_message = "a person's token is read by the workspace's own account and by nobody else"
  }
  assert {
    condition     = google_project_iam_member.workspace_person_token[0].condition[0].title == "fugaro-workspace-person-token"
    error_message = "the condition's title is the one internal/infra/tf holds a plan to (gcp.WorkspacePersonTokenConditionTitle)"
  }
  assert {
    condition     = google_project_iam_member.workspace_person_token[0].condition[0].expression == "resource.name.extract(\"/secrets/{secret}\").startsWith(\"fugaro_u_\") && resource.name.extract(\"/secrets/{secret}\").endsWith(\"_claude-oauth-token\")"
    error_message = "the condition is the fence byte for byte (gcp.WorkspacePersonTokenCondition): the per-person prefix AND the one name this half serves, and a wider one hands the workspace's account every personal secret a person keeps in its project"
  }
}

# The image half of this root's own fence, and the shape every instance of the
# service is: the released pin an installation's credentials service is deployed
# with (D2980), which is what the Go-side cover holds a plan to as well
# (internal/infra/tf's credentialsImageOK, the closure an independent security
# review gave the installation's own service). This root states everything of
# that shape its own variables can — the run's registry and the remote
# (pull-through) repository, which is not the project's own writable one — and
# the owner, the one part a tfvars here does not carry, is checked against the
# machine's recorded config on the plan side.
#
# Every instance of this service reads every model credential the workspace
# holds, and every operator holds roles/artifactregistry.writer on this project's
# own repositories, so a digest an operator pushed and named is theirs to choose:
# what these four runs refuse is that choice, and a registry that is not this
# installation's.
run "the_service_on_an_image_from_another_registry_is_refused" {
  command = plan

  variables {
    credentials_service = {
      service          = "fugaro-workspace-credentials"
      image            = "europe-west1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
      region           = "us-central1"
      bundle_secret    = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
      firebase_project = "workspace-alpha-1234"
      rtdb_url         = "https://workspace-alpha-1234.firebaseio.com"
    }
  }

  expect_failures = [var.credentials_service]
}

run "the_service_on_an_image_from_another_project_is_refused" {
  command = plan

  variables {
    credentials_service = {
      service          = "fugaro-workspace-credentials"
      image            = "us-central1-docker.pkg.dev/member-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
      region           = "us-central1"
      bundle_secret    = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
      firebase_project = "workspace-alpha-1234"
      rtdb_url         = "https://workspace-alpha-1234.firebaseio.com"
    }
  }

  expect_failures = [var.credentials_service]
}

run "the_service_on_an_image_from_the_projects_own_writable_repository_is_refused" {
  command = plan

  variables {
    credentials_service = {
      service          = "fugaro-workspace-credentials"
      image            = "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-base/dimipaun/fugaro-base:1.2.3@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
      region           = "us-central1"
      bundle_secret    = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
      firebase_project = "workspace-alpha-1234"
      rtdb_url         = "https://workspace-alpha-1234.firebaseio.com"
    }
  }

  expect_failures = [var.credentials_service]
}

run "the_service_on_a_tag_with_no_digest_is_refused" {
  command = plan

  variables {
    credentials_service = {
      service          = "fugaro-workspace-credentials"
      image            = "us-central1-docker.pkg.dev/workspace-alpha-1234/fugaro-ghcr/dimipaun/fugaro-base:1.2.3"
      region           = "us-central1"
      bundle_secret    = "fugaro-project-workspace-alpha-creds-bundle-0123456789abcdef"
      firebase_project = "workspace-alpha-1234"
      rtdb_url         = "https://workspace-alpha-1234.firebaseio.com"
    }
  }

  expect_failures = [var.credentials_service]
}

# A roster row is a person or a machine by its kind AND by its address (WC2's
# review): the member this root binds a person through is `user:<address>` and
# nothing else, and IAM refuses a `user:` member that is a machine's. A row
# marked a `human` at a service account's address is therefore refused HERE, at
# the plan, rather than at apply in the API's own words about the member — which
# name no roster row. A machine the workspace admits is admitted as one, through
# the members' own invoke grant above.
run "a_persons_address_that_is_a_machines_is_refused" {
  command = plan

  variables {
    people = ["ada@example.com", "fugaro-run-alpha@member-alpha-1234.iam.gserviceaccount.com"]
  }

  expect_failures = [var.people]
}
