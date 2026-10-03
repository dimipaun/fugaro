# Plans the Firebase root against mock providers: no credentials, no network
# beyond the provider downloads. The names are the ones fugaro init writes
# into terraform.tfvars.json.
#
# An assertion can't reach into a child module's resources, so the first run
# plans the root (its wiring, through the outputs) and the others plan the
# module itself.

mock_provider "google" {}
mock_provider "google-beta" {}

variables {
  project        = "fp-1234"
  fugaro_project = "aurora"
  names = {
    signer_account_id = "fugaro-token-signer"
    minter_role_id    = "fugaroTokenMinter"
    api_key           = "fugaro-run-signin"
  }
  launchers       = ["user:launcher@example.com"]
  operators       = ["user:operator@example.com"]
  admins          = ["user:owner@example.com", "group:editors@example.com"]
  budget_admins   = ["user:extra@example.com"]
  history_account = "fugaro-history@proj-1234.iam.gserviceaccount.com"
}

run "root_passes_inputs" {
  command = plan

  override_resource {
    target          = module.firebase.google_service_account.signer
    override_during = plan
    values = {
      name  = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = module.firebase.google_firebase_database_instance.this
    override_during = plan
    values = {
      database_url = "https://fp-1234-default-rtdb.firebaseio.com"
    }
  }
  override_resource {
    target          = module.firebase.google_apikeys_key.web
    override_during = plan
    values = {
      key_string = "AIzaSyMockKey"
    }
  }

  assert {
    condition     = output.firebase_project == "fp-1234"
    error_message = "the root must pass project to the module"
  }
  assert {
    condition     = output.token_signer == "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    error_message = "the root must pass names.signer_account_id and project: the signer is in the FP"
  }
  assert {
    condition     = output.rtdb_url == "https://fp-1234-default-rtdb.firebaseio.com" && output.firebase_api_key == "AIzaSyMockKey"
    error_message = "the root must output the RTDB's URL and the sign-in API key"
  }
}

run "TestSignerHasNoRoles" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  # Every member the module grants roles to, project-wide or on the signer.
  # This checks the grant set the plan produces; the structural guarantee
  # (no resource can name the signer as a member) is the Go text check
  # TestFirebaseModuleGrantsNothingToSignerOrJobs.
  assert {
    condition = alltrue(flatten([
      [for m in google_project_iam_member.viewer : m.member != google_service_account.signer.member],
      [for m in google_project_iam_member.admin : m.member != google_service_account.signer.member],
      [google_project_iam_member.history_database.member != google_service_account.signer.member],
      [google_project_iam_member.history_auth.member != google_service_account.signer.member],
      [google_project_iam_member.history_firestore.member != google_service_account.signer.member],
      [for m in google_project_iam_member.datastore_viewer : m.member != google_service_account.signer.member],
    ]))
    error_message = "the token signer must hold no role on the FP"
  }
  assert {
    condition     = alltrue([for m in google_service_account_iam_member.minter : m.member != google_service_account.signer.member])
    error_message = "the token signer must not be granted anything on itself either"
  }
  assert {
    condition     = google_service_account.signer.account_id == "fugaro-token-signer" && google_service_account.signer.display_name == "Fugaro token signer" && google_service_account.signer.project == "fp-1234"
    error_message = "the signer is fugaro-token-signer in the FP, its display name being the ownership mark"
  }
}

run "TestLauncherHasViewerAndMinterOnly" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  assert {
    condition     = google_project_iam_custom_role.token_minter.permissions == toset(["iam.serviceAccounts.signJwt"])
    error_message = "fugaroTokenMinter must hold exactly iam.serviceAccounts.signJwt, narrower than serviceAccountTokenCreator"
  }
  assert {
    condition     = google_project_iam_custom_role.token_minter.role_id == "fugaroTokenMinter" && google_project_iam_custom_role.token_minter.title == "Fugaro token minter"
    error_message = "the minter role's ID is the input"
  }
  assert {
    condition = (keys(google_service_account_iam_member.minter) == ["user:launcher@example.com", "user:operator@example.com"] &&
    alltrue([for m in google_service_account_iam_member.minter : m.role == "projects/fp-1234/roles/fugaroTokenMinter"]))
    error_message = "launchers and operators get the minter role, on the signer account"
  }
  assert {
    condition     = alltrue([for m in google_service_account_iam_member.minter : m.service_account_id == google_service_account.signer.name])
    error_message = "the minter role is granted on the signer account, never on the project"
  }
  assert {
    condition = (keys(google_project_iam_member.viewer) == ["user:launcher@example.com", "user:operator@example.com"] &&
    alltrue([for m in google_project_iam_member.viewer : m.role == "roles/firebasedatabase.viewer"]))
    error_message = "launchers and operators get roles/firebasedatabase.viewer on the FP"
  }
  # The launcher holds no admin role, and no project-level role other than
  # the viewer: the minter role is only ever on the signer.
  assert {
    condition     = !contains(keys(google_project_iam_member.admin), "user:launcher@example.com") && !contains(keys(google_project_iam_member.admin), "user:operator@example.com")
    error_message = "a launcher or operator who isn't also an owner, editor or listed budget admin must not be a budget admin"
  }
}

# M9d: the spend history in Firestore. The history account writes (user, on
# the FP only); everyone who can read the RTDB, and every admin, reads
# (viewer). No domain: or wildcard member can reach these (variable
# validation); the Go text check TestHistoryNoDatastoreAdmin pins the roles.
run "TestFirebaseRootHasDatastoreGrants" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  assert {
    condition = (keys(google_project_iam_member.datastore_viewer) == [
      "group:editors@example.com", "user:extra@example.com", "user:launcher@example.com", "user:operator@example.com", "user:owner@example.com",
    ] && alltrue([for m in google_project_iam_member.datastore_viewer : m.role == "roles/datastore.viewer" && m.project == "fp-1234"]))
    error_message = "launchers, operators, admins and budget admins get roles/datastore.viewer on the FP, and nobody else"
  }
  assert {
    condition = (google_project_iam_member.history_firestore.role == "roles/datastore.user" && google_project_iam_member.history_firestore.project == "fp-1234" &&
    google_project_iam_member.history_firestore.member == "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com")
    error_message = "the history account holds roles/datastore.user on the FP"
  }
  assert {
    condition     = alltrue([for m in google_project_iam_member.datastore_viewer : !startswith(m.member, "serviceAccount:") && !startswith(m.member, "domain:") && !strcontains(m.member, "*")])
    error_message = "datastore.viewer goes to people only: no service account, domain or wildcard"
  }
  assert {
    condition     = google_project_iam_member.history_database.role == "roles/firebasedatabase.admin" && google_project_iam_member.history_auth.role == "roles/firebaseauth.admin"
    error_message = "the history account's existing RTDB and Auth grants are unchanged"
  }
}

# There is no firebase-module test for "job accounts hold nothing on the FP":
# the module takes no job account and has no resource that could grant one,
# so a plan assertion would only compare inputs. That invariant is pinned by
# the Go text check TestFirebaseModuleGrantsNothingToSignerOrJobs.
run "service_account_grants_are_the_history_account_only" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  assert {
    condition = alltrue(concat(
      [for m in google_project_iam_member.viewer : !startswith(m.member, "serviceAccount:")],
      [for m in google_project_iam_member.admin : !startswith(m.member, "serviceAccount:")],
      [for m in google_project_iam_member.datastore_viewer : !startswith(m.member, "serviceAccount:")],
    ))
    error_message = "with these inputs, no service account holds a viewer or admin role except the history account's own grants"
  }
}

run "TestAdminGrantsFromOwnersAndEditors" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  assert {
    condition     = keys(google_project_iam_member.admin) == ["group:editors@example.com", "user:extra@example.com", "user:owner@example.com"]
    error_message = "budget admins are exactly the discovered owners and editors plus budget_admins"
  }
  assert {
    condition     = alltrue([for m in google_project_iam_member.admin : m.role == "roles/firebasedatabase.admin" && m.project == "fp-1234"])
    error_message = "budget admins get roles/firebasedatabase.admin on the FP"
  }
}

run "no_admins" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  variables {
    admins        = []
    budget_admins = []
  }

  assert {
    condition     = length(google_project_iam_member.admin) == 0
    error_message = "without owners, editors or budget_admins nobody is granted admin (the FP's own owners hold it implicitly)"
  }
}

run "TestHistoryAccountGrants" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  assert {
    condition     = google_project_iam_member.history_database.role == "roles/firebasedatabase.admin" && google_project_iam_member.history_database.project == "fp-1234"
    error_message = "the history account holds roles/firebasedatabase.admin on the FP"
  }
  assert {
    condition     = google_project_iam_member.history_auth.role == "roles/firebaseauth.admin" && google_project_iam_member.history_auth.project == "fp-1234"
    error_message = "the history account holds roles/firebaseauth.admin on the FP, to delete expired run users"
  }
}

run "TestApiKeyRestricted" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  assert {
    condition     = length(one(google_apikeys_key.web.restrictions).api_targets) == 2
    error_message = "the API key may call exactly two APIs"
  }
  assert {
    condition = toset([for t in one(google_apikeys_key.web.restrictions).api_targets : t.service]) == toset([
      "identitytoolkit.googleapis.com", "securetoken.googleapis.com",
    ])
    error_message = "the API key is restricted to Identity Toolkit and Secure Token"
  }
}

run "TestRtdbRegionIsUsCentral1" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  assert {
    condition     = google_firebase_database_instance.this.region == "us-central1"
    error_message = "the RTDB must be in us-central1 (D4), a location that can't be changed later"
  }
  assert {
    condition     = google_firebase_database_instance.this.instance_id == "fp-1234-default-rtdb" && google_firebase_database_instance.this.type == "DEFAULT_DATABASE"
    error_message = "the instance is the project's default database"
  }
}

run "apis" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  assert {
    condition = keys(google_project_service.this) == [
      "apikeys.googleapis.com", "cloudresourcemanager.googleapis.com", "firebase.googleapis.com",
      "firebasedatabase.googleapis.com", "firebaserules.googleapis.com", "firestore.googleapis.com", "iam.googleapis.com", "iamcredentials.googleapis.com",
      "identitytoolkit.googleapis.com", "securetoken.googleapis.com",
    ]
    error_message = "the FP enables firebase, firebasedatabase, identitytoolkit, securetoken, iamcredentials, apikeys, firestore and firebaserules, plus iam and cloudresourcemanager"
  }
}

run "apis_unmanaged" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/fp-1234/roles/fugaroTokenMinter"
    }
  }

  variables {
    manage_apis = false
  }

  assert {
    condition     = length(google_project_service.this) == 0
    error_message = "manage_apis = false enables no API"
  }
}

run "bad_history_account" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    history_account = "not-an-email"
  }

  expect_failures = [var.history_account]
}

run "bad_member" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    admins = ["owner@example.com"]
  }

  expect_failures = [var.admins]
}

run "domain_admin_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    budget_admins = ["domain:example.com"]
  }

  expect_failures = [var.budget_admins]
}

run "domain_owner_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    admins = ["domain:example.com"]
  }

  expect_failures = [var.admins]
}

run "wildcard_admin_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    budget_admins = ["user:*@example.com"]
  }

  expect_failures = [var.budget_admins]
}

run "all_users_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    launchers = ["allUsers"]
  }

  expect_failures = [var.launchers]
}

run "domain_launcher_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    launchers = ["domain:example.com"]
  }

  expect_failures = [var.launchers]
}

run "bad_operator_member" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    operators = ["operator@example.com"]
  }

  expect_failures = [var.operators]
}

run "bad_project_id" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    project = "Bad_Project"
  }

  expect_failures = [var.project]
}

run "bad_fugaro_project" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    fugaro_project = "-bad"
  }

  expect_failures = [var.fugaro_project]
}

run "bad_signer_account_id" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    names = {
      signer_account_id = "X"
      minter_role_id    = "fugaroTokenMinter"
      api_key           = "fugaro-run-signin"
    }
  }

  expect_failures = [var.names]
}

run "bad_minter_role_id" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    names = {
      signer_account_id = "fugaro-token-signer"
      minter_role_id    = "bad role"
      api_key           = "fugaro-run-signin"
    }
  }

  expect_failures = [var.names]
}

run "bad_api_key_name" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    names = {
      signer_account_id = "fugaro-token-signer"
      minter_role_id    = "fugaroTokenMinter"
      api_key           = "Bad Key"
    }
  }

  expect_failures = [var.names]
}
