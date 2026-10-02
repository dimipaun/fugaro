# The Firebase root: everything that lives in the Fugaro project's own
# Firebase project (the FP, a GCP project of its own): the APIs, Firebase,
# the Realtime Database, the sign-in API key, the token signer and every
# grant onto the FP. fugaro init --firebase writes it into a workdir with
# terraform.tfvars.json and a backend config naming the installation's state
# bucket (state fugaro/firebase), and plans and applies it there.
terraform {
  required_version = ">= 1.7.0, < 2.0.0"

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

  project         = var.project
  fugaro_project  = var.fugaro_project
  names           = var.names
  manage_apis     = var.manage_apis
  launchers       = var.launchers
  operators       = var.operators
  admins          = var.admins
  budget_admins   = var.budget_admins
  history_account = var.history_account
}
