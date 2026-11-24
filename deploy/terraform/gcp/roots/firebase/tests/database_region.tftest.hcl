# A Realtime Database instance that EXISTS keeps the location it was created
# in (F8, R4, D3091): the apply below creates it where the mapping said at the
# time (us-central1), and the plan after it moves the mapping to europe-west1
# and finds the instance still planned in us-central1. The region is a
# creation-time field and the module's ignore_changes is what keeps it.
#
# The assertion is the PLANNED VALUE and not "no replacement". The mock
# provider plans no replacement with or without ignore_changes — measured — so
# a run asserting one would pass either way, while the planned region is
# us-central1 exactly when the field is ignored and europe-west1 when it is
# not.
#
# It is a file of its own rather than two more runs of firebase.tftest.hcl for
# two reasons: the pair APPLIES the module, and only a file's own runs share
# the state that leaves behind; and an apply needs the mock_resource overrides
# below, which would be in force for every run of the file they sat in.
#
# What this cannot say is what the REAL provider plans for an instance it
# already manages and whose mapping has since changed, which is the case every
# installation is in after its first apply: internal/infra plans that against a
# synthetic prior state with the real provider
# (TestAnExistingInstanceKeepsItsOwnRegion,
# TestAManagedInstanceOfAnEUInstallationPlansNoChange).

mock_provider "google" {
  # An IAM member's service_account_id is validated as a service account name,
  # so the signer's computed name has to be readable during the apply; the mock
  # provider generates an arbitrary string for it otherwise.
  mock_resource "google_service_account" {
    defaults = {
      name   = "projects/fp-1234/serviceAccounts/fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      email  = "fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-token-signer@fp-1234.iam.gserviceaccount.com"
    }
  }
}
mock_provider "google-beta" {}

variables {
  project         = "fp-1234"
  fugaro_project  = "aurora"
  database_region = "us-central1"
  names = {
    signer_account_id = "fugaro-token-signer"
    minter_role_id    = "fugaroTokenMinter"
    api_key           = "fugaro-web"
  }
  launchers       = ["user:launcher@example.com"]
  operators       = ["user:operator@example.com"]
  admins          = ["user:owner@example.com"]
  history_account = "fugaro-history@proj-1234.iam.gserviceaccount.com"
}

run "the_instance_is_created_where_the_mapping_says" {
  command = apply

  module {
    source = "../../modules/firebase"
  }

  assert {
    condition     = google_firebase_database_instance.this.region == "us-central1"
    error_message = "a new instance is created in the mapping's location (D3089)"
  }
}

run "an_existing_instance_keeps_its_own_region" {
  command = plan

  module {
    source = "../../modules/firebase"
  }

  variables {
    database_region = "europe-west1"
  }

  assert {
    condition     = google_firebase_database_instance.this.region == "us-central1"
    error_message = "an existing instance must keep the location it was created in, not be moved to the mapping's new one (F8, R4)"
  }
  assert {
    condition     = google_firebase_database_instance.this.instance_id == "fp-1234-default-rtdb"
    error_message = "the instance the plan is about is the project's default database"
  }
}
