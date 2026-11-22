# The Firebase root: everything that lives in the Fugaro project's own
# Firebase project (the FP: a GCP project of its own, or the installation's
# own project, design D3 revised 2026-10-04): the APIs, Firebase,
# the Realtime Database, the sign-in API key, the token signer and every
# grant onto the FP. fugaro update writes it into a workdir with
# terraform.tfvars.json and a backend config naming the installation's state
# bucket (state fugaro/firebase), and plans and applies it there.
terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

# The project is always the FP, and API quota is billed to it, as it is
# with user credentials and a quota project.
provider "google" {
  project               = var.project
  billing_project       = var.project
  user_project_override = true
}

provider "google-beta" {
  project               = var.project
  billing_project       = var.project
  user_project_override = true
}

module "firebase" {
  source = "../../modules/firebase"

  project             = var.project
  database_region     = var.database_region
  fugaro_project      = var.fugaro_project
  names               = var.names
  manage_apis         = var.manage_apis
  skip_apis           = var.skip_apis
  launchers           = var.launchers
  operators           = var.operators
  viewers             = var.viewers
  admins              = var.admins
  budget_admins       = var.budget_admins
  history_account     = var.history_account
  coordinator_account = var.coordinator_account

  # The issuer's own row and the switch that gates the people's
  # (launcher-does-not-mint plan task LM11): the credentials service's account,
  # which `fugaro update` passes once the installation root has created it, and
  # the switch it leaves on until the plan that moves the grant off the people
  # (LM14).
  issuer_account = var.issuer_account
  people_minter  = var.people_minter

  # The public mobile service's runtime account, when the installation runs that
  # service: the module creates the device-only signer and grants it signJwt
  # (iam.tf, mobile_token_minter).
  mobile_account = var.mobile_account

  # The workspace service account this member's own workspace_pin names, when
  # it names one: the Firebase module grants it a read-only role on this
  # project's database (docs/workspaces/spec.md §5, W7). Internal to the pin:
  # the workspace's published record names no service account.
  workspace_service_account = var.workspace_service_account

  # Whether the FP's Firestore database is there, as `fugaro update` read it:
  # the module's firestore_indexes.tf applies the run index's composite indexes
  # only under a database that exists, and `fugaro update` creates that database
  # AFTER this root (RI2, D3027).
  firestore_database_exists = var.firestore_database_exists
}
