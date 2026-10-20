# The compute root: the Batch backend's installation-level provisioning
# (design docs/design/compute-backend.md §8.1 task 6). fugaro update writes it
# into a workdir of its own with terraform.tfvars.json and a backend config
# naming the state bucket, and plans and applies it there — a workdir and a
# state prefix of its own, so the installation's tfvars, imports and saved
# plan are never rewritten by this root's.
#
# Nothing an existing installation runs reaches it: the root is applied only
# by the stage behind --provision-batch (internal/cli/init_compute.go), and
# `backend: cloud-batch` stays refused by config validation until the enable
# task (§8.1 task 9). See the module's variables.tf for what each input is.
terraform {
  required_version = ">= 1.9.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

module "compute" {
  source = "../../modules/compute"

  project              = var.project
  project_number       = var.project_number
  region               = var.region
  network              = var.network
  subnetwork           = var.subnetwork
  names                = var.names
  manage_apis          = var.manage_apis
  launchers            = var.launchers
  operators            = var.operators
  run_accounts         = var.run_accounts
  registry_repository  = var.registry_repository
  base_image           = var.base_image
  base_project         = var.base_project
  base_family          = var.base_family
  preload_zone         = var.preload_zone
  preload_schedule     = var.preload_schedule
  preload_refresh_days = var.preload_refresh_days
  builder_machine_type = var.builder_machine_type
  build_submitter_role = var.build_submitter_role
}
