# Plans the Firebase root against mock providers: no credentials, no network
# beyond the provider downloads. The names are the ones fugaro update writes
# into terraform.tfvars.json.
#
# An assertion can't reach into a child module's resources, so the first run
# plans the root (its wiring, through the outputs) and the others plan the
# module itself.

mock_provider "google" {}
mock_provider "google-beta" {}

# database_region is what fugaro update writes for an installation in a US
# region: the Realtime Database is offered in three locations and us-central1
# is the nearest to every us-* region (internal/place). The run that asserts it
# overrides it with another location, because a value every run shares is one a
# module could ignore.
variables {
  project         = "fp-1234"
  fugaro_project  = "aurora"
  database_region = "us-central1"
  names = {
    signer_account_id = "fugaro-token-signer"
    minter_role_id    = "fugaroTokenMinter"
    api_key           = "fugaro-web"
  }
  launchers     = ["user:launcher@example.com"]
  operators     = ["user:operator@example.com"]
  admins        = ["user:owner@example.com", "group:editors@example.com"]
  budget_admins = ["user:extra@example.com"]
  # A viewer is in the fixture on purpose (RS4 of docs/roles-scopes): the
  # viewer is a member kind of its own here — it reads the database and the
  # spend history and mints nothing — and a list nobody names is a list no run
  # reads. Every enumeration below that a viewer changes names it.
  viewers         = ["user:viewer@example.com"]
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
      [for m in google_project_iam_member.usage_consumer : m.member != google_service_account.signer.member],
      [google_project_iam_member.history_usage.member != google_service_account.signer.member],
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
  # The viewer's absence from the minter is asserted on its own, and not left
  # to the key list above, because it is the one grant that would hand a
  # viewer everything roles.md §4 withholds: a viewer holds no way to mint an
  # identity. The fixture names a viewer precisely so this reads something.
  assert {
    condition     = !contains(keys(google_service_account_iam_member.minter), "user:viewer@example.com")
    error_message = "a viewer must not hold the token minter: minting an identity is everything the viewer list withholds"
  }
  assert {
    condition     = alltrue([for m in google_service_account_iam_member.minter : m.service_account_id == google_service_account.signer.name])
    error_message = "the minter role is granted on the signer account, never on the project"
  }
  assert {
    condition = (keys(google_project_iam_member.viewer) == ["user:launcher@example.com", "user:operator@example.com", "user:viewer@example.com"] &&
    alltrue([for m in google_project_iam_member.viewer : m.role == "roles/firebasedatabase.viewer"]))
    error_message = "launchers, operators and viewers get roles/firebasedatabase.viewer on the FP, and nobody else"
  }
  # The launcher holds no admin role, and no project-level role other than
  # the viewer: the minter role is only ever on the signer.
  assert {
    condition     = !contains(keys(google_project_iam_member.admin), "user:launcher@example.com") && !contains(keys(google_project_iam_member.admin), "user:operator@example.com")
    error_message = "a launcher or operator who isn't also an owner, editor or listed budget admin must not be a budget admin"
  }
}

# The issuer's row (launcher-does-not-mint plan task LM11; D3121). With an
# account given, the module plans exactly one signJwt row for it on the runs'
# signer, and the people's row is untouched beside it: the two coexist for the
# one release the update that moves the grant lives in (LM14).
run "TestIssuerMinterRow" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    issuer_account = "fugaro-credentials@proj-1234.iam.gserviceaccount.com"
  }

  assert {
    condition = (length(google_service_account_iam_member.issuer_minter) == 1 &&
      google_service_account_iam_member.issuer_minter[0].service_account_id == "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com" &&
      google_service_account_iam_member.issuer_minter[0].role == "projects/fp-1234/roles/fugaroTokenMinter" &&
    google_service_account_iam_member.issuer_minter[0].member == "serviceAccount:fugaro-credentials@proj-1234.iam.gserviceaccount.com")
    error_message = "the issuer's account holds iam.serviceAccounts.signJwt on the runs' signer of the FP, and nowhere else: the row is bound on the account and not on the project"
  }
  assert {
    condition     = keys(google_service_account_iam_member.minter) == ["user:launcher@example.com", "user:operator@example.com"]
    error_message = "the issuer's row is added beside the people's and not instead of it: the row the launchers and operators hold leaves in the update that moves the grant, and not in this one"
  }
}

# The switch (LM11): with it off, no launcher and no operator holds the minter
# row, and the issuer's row is not the switch's — it is planned either way,
# which is what makes the one plan that turns the switch off a plan that moves
# the grant rather than one that leaves nobody able to mint.
run "TestPeopleMinterIsOffWithTheSwitch" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    people_minter  = false
    issuer_account = "fugaro-credentials@proj-1234.iam.gserviceaccount.com"
  }

  assert {
    condition     = length(google_service_account_iam_member.minter) == 0
    error_message = "with people_minter = false not one launcher or operator holds the minter row"
  }
  assert {
    condition     = length(google_service_account_iam_member.issuer_minter) == 1
    error_message = "people_minter gates the people's row alone: the issuer's row is planned whatever it says"
  }
  assert {
    condition     = alltrue([for m in google_project_iam_member.viewer : m.role == "roles/firebasedatabase.viewer"])
    error_message = "people_minter gates the minter row alone: a launcher and an operator keep every read the roster gives them"
  }
}

# ... and its default, which is what makes the release carrying this change a
# plan with nothing new in it: the people's minter row, and no row for an
# account nobody has named.
run "people_minter_defaults_on" {
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

  assert {
    condition = (keys(google_service_account_iam_member.minter) == ["user:launcher@example.com", "user:operator@example.com"] &&
    length(google_service_account_iam_member.issuer_minter) == 0)
    error_message = "with the defaults the module still plans the people's row and plans no issuer row: the first update carrying this change has to plan nothing new"
  }
}

run "bad_issuer_account" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    issuer_account = "not-an-email"
  }

  expect_failures = [var.issuer_account]
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
      "group:editors@example.com", "user:extra@example.com", "user:launcher@example.com", "user:operator@example.com", "user:owner@example.com", "user:viewer@example.com",
    ] && alltrue([for m in google_project_iam_member.datastore_viewer : m.role == "roles/datastore.viewer" && m.project == "fp-1234"]))
    error_message = "launchers, operators, admins, budget admins and viewers get roles/datastore.viewer on the FP, and nobody else"
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

# The Firestore client sends X-Goog-User-Project: serviceusage.services.use on
# the FP goes to exactly the datastore.viewer principals and the history
# account, nobody else.
run "TestUsageConsumerGrants" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  assert {
    condition = (keys(google_project_iam_member.usage_consumer) == [
      "group:editors@example.com", "user:extra@example.com", "user:launcher@example.com", "user:operator@example.com", "user:owner@example.com", "user:viewer@example.com",
    ] && alltrue([for m in google_project_iam_member.usage_consumer : m.role == "roles/serviceusage.serviceUsageConsumer" && m.project == "fp-1234"]))
    error_message = "serviceUsageConsumer goes to the datastore.viewer principals, viewers included, and nobody else"
  }
  assert {
    condition     = keys(google_project_iam_member.usage_consumer) == keys(google_project_iam_member.datastore_viewer)
    error_message = "the consumer set equals the datastore.viewer set"
  }
  assert {
    condition = (google_project_iam_member.history_usage.role == "roles/serviceusage.serviceUsageConsumer" && google_project_iam_member.history_usage.project == "fp-1234" &&
    google_project_iam_member.history_usage.member == "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com")
    error_message = "the history account holds serviceUsageConsumer on the FP"
  }
  assert {
    condition     = alltrue([for m in google_project_iam_member.usage_consumer : !startswith(m.member, "serviceAccount:") && !startswith(m.member, "domain:") && !strcontains(m.member, "*")])
    error_message = "no service account, domain or wildcard among the people's consumer grants"
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

# A NEW instance is created where the mapping says (D3089), which is the
# database_region input: europe-west1 here, so the assertion fails if the
# resource goes back to a constant. That an existing one keeps its own is not
# this run's to say — a module-level run plans the config's own value, the
# ignore_changes in firebase.tf makes a plan show no change and no replacement
# either way — and two runs of database_region.tftest.hcl hold that (and
# internal/infra's TestAnExistingInstanceKeepsItsOwnRegion against a synthetic
# prior state).
run "TestRtdbRegionComesFromTheMapping" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    database_region = "europe-west1"
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
    condition     = google_firebase_database_instance.this.region == "europe-west1"
    error_message = "a new RTDB instance must go where the mapping says (D3089), not to a constant: the region is a creation-time field that can't be changed later"
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

# The coordinator's writer is granted only once coordinator_account is set:
# the installation root creates that account in coordinator part P5, and IAM
# refuses a grant to a member that does not exist, so until then the default
# (empty) grants nothing.
run "coordinator_grant_off_by_default" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  assert {
    condition     = length(google_project_iam_member.coordinator_database) == 0
    error_message = "without coordinator_account nothing is granted to the coordinator's writer"
  }
}

run "TestCoordinatorAccountGrant" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    coordinator_account = "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"
  }

  assert {
    condition = (length(google_project_iam_member.coordinator_database) == 1 &&
      google_project_iam_member.coordinator_database[0].role == "roles/firebasedatabase.admin" &&
      google_project_iam_member.coordinator_database[0].project == "fp-1234" &&
    google_project_iam_member.coordinator_database[0].member == "serviceAccount:fugaro-coordinator@proj-1234.iam.gserviceaccount.com")
    error_message = "with coordinator_account set, the writer holds roles/firebasedatabase.admin on the FP and nothing else here"
  }
}

run "bad_coordinator_account" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    coordinator_account = "not-an-email"
  }

  expect_failures = [var.coordinator_account]
}

# W7's binding (docs/workspaces/spec.md §5): one project grant, of one custom
# role holding exactly the instance's read, to the account the member's own
# config pins — and nothing at all for an installation that pins no workspace,
# which is every install that has not joined one.
run "TestWorkspaceReadGrant" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    workspace_service_account = "fugaro-credentials@ws-1234.iam.gserviceaccount.com"
  }

  assert {
    condition = (length(google_project_iam_member.workspace_read) == 1 &&
      google_project_iam_member.workspace_read[0].project == "fp-1234" &&
      google_project_iam_member.workspace_read[0].role == "projects/fp-1234/roles/fugaroWorkspaceRead" &&
    google_project_iam_member.workspace_read[0].member == "serviceAccount:fugaro-credentials@ws-1234.iam.gserviceaccount.com")
    error_message = "the pinned account holds the read-only role on the FP, and it is the only member bound here"
  }
  assert {
    condition = (length(google_project_iam_custom_role.workspace_read) == 1 &&
      contains(google_project_iam_custom_role.workspace_read[0].permissions, "firebasedatabase.instances.get") &&
    !contains(google_project_iam_custom_role.workspace_read[0].permissions, "firebasedatabase.instances.update"))
    error_message = "the role is the instance's read and no write: a member's database is the member's, and this grant is IAM's half of that"
  }
}

run "TestNoWorkspaceReadWithoutAPin" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    workspace_service_account = ""
  }

  assert {
    condition = (length(google_project_iam_member.workspace_read) == 0 &&
    length(google_project_iam_custom_role.workspace_read) == 0)
    error_message = "an installation in no workspace creates neither the role nor the binding: the absent pin is not an account"
  }
}

run "bad_workspace_account" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    workspace_service_account = "not-an-email"
  }

  expect_failures = [var.workspace_service_account]
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

# The viewer list's own validation, which the fixture above cannot read: a
# viewer is granted on the FP, so a domain member (every account of a domain
# reading every run's spend history) and a wildcard are refused here.
run "domain_viewer_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    viewers = ["domain:example.com"]
  }

  expect_failures = [var.viewers]
}

run "wildcard_viewer_rejected" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    viewers = ["user:*@example.com"]
  }

  expect_failures = [var.viewers]
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

run "bad_database_region" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    database_region = "us-east5"
  }

  expect_failures = [var.database_region]
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
      api_key           = "fugaro-web"
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
      api_key           = "fugaro-web"
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

# The same-project layout (design D3, revised 2026-10-04): the Firebase
# project is the installation's own GCP project, so the history account, the
# signer and the grants all live in one project with the job, build and
# scheduler accounts. The Firebase root leaves the APIs the installation root
# enables to it (skip_apis), and everything else is as with two projects.
run "same_project_apis" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroTokenMinter"
    }
  }

  variables {
    project   = "proj-1234"
    skip_apis = ["cloudresourcemanager.googleapis.com", "iam.googleapis.com"]
  }

  assert {
    condition = keys(google_project_service.this) == [
      "apikeys.googleapis.com", "firebase.googleapis.com",
      "firebasedatabase.googleapis.com", "firebaserules.googleapis.com", "firestore.googleapis.com", "iamcredentials.googleapis.com",
      "identitytoolkit.googleapis.com", "securetoken.googleapis.com",
    ]
    error_message = "with skip_apis the Firebase root enables everything but iam and cloudresourcemanager, which the installation root owns"
  }
  assert {
    condition     = alltrue([for s in google_project_service.this : s.project == "proj-1234" && !s.disable_on_destroy])
    error_message = "the APIs are in the one project and are never disabled on destroy"
  }
}

# The roles on the project are the Firebase module's, and the only service
# account that receives any is the history account (its sweep and rollover);
# the signer holds none, and its one IAM grant is the minter role, on itself.
run "same_project_grants" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  override_resource {
    target          = google_service_account.signer
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
    }
  }
  override_resource {
    target          = google_project_iam_custom_role.token_minter
    override_during = plan
    values = {
      name = "projects/proj-1234/roles/fugaroTokenMinter"
    }
  }

  variables {
    project   = "proj-1234"
    skip_apis = ["cloudresourcemanager.googleapis.com", "iam.googleapis.com"]
  }

  assert {
    condition = alltrue(flatten([
      [for m in google_project_iam_member.viewer : m.member != google_service_account.signer.member && m.project == "proj-1234"],
      [for m in google_project_iam_member.admin : m.member != google_service_account.signer.member && m.project == "proj-1234"],
      [for m in google_project_iam_member.datastore_viewer : m.member != google_service_account.signer.member],
      [for m in google_project_iam_member.usage_consumer : m.member != google_service_account.signer.member],
      [for m in [google_project_iam_member.history_database, google_project_iam_member.history_auth, google_project_iam_member.history_firestore, google_project_iam_member.history_usage] :
      m.member == "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com" && m.project == "proj-1234"],
    ]))
    error_message = "in one project the signer holds no role and only the history account holds service-account roles"
  }
  assert {
    condition = alltrue(concat(
      [for m in google_project_iam_member.viewer : !startswith(m.member, "serviceAccount:")],
      [for m in google_project_iam_member.admin : !startswith(m.member, "serviceAccount:")],
      [for m in google_project_iam_member.datastore_viewer : !startswith(m.member, "serviceAccount:")],
      [for m in google_project_iam_member.usage_consumer : !startswith(m.member, "serviceAccount:")],
    ))
    error_message = "no service account is among the people's grants, so no job, build or scheduler account gets a role on the backend"
  }
  assert {
    condition = (keys(google_project_iam_member.admin) == ["group:editors@example.com", "user:extra@example.com", "user:owner@example.com"] &&
    alltrue([for m in google_project_iam_member.admin : m.role == "roles/firebasedatabase.admin"]))
    error_message = "the budget admins are the owners, editors and budget_admins, with roles/firebasedatabase.admin, exactly as with two projects"
  }
  assert {
    condition = (google_project_iam_custom_role.token_minter.permissions == toset(["iam.serviceAccounts.signJwt"]) &&
    alltrue([for m in google_service_account_iam_member.minter : m.service_account_id == google_service_account.signer.name && m.role == "projects/proj-1234/roles/fugaroTokenMinter"]))
    error_message = "the signer's IAM is unchanged: signJwt for the launchers and operators, on the signer account only"
  }
}

run "bad_skip_apis" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    skip_apis = ["not an api"]
  }

  expect_failures = [var.skip_apis]
}
