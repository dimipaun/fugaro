# The installation root: the resources every repository shares. fugaro init
# writes it into a workdir with terraform.tfvars.json and a backend config
# naming the state bucket, and plans and applies it there.
terraform {
  required_version = ">= 1.7.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.4.0"
    }
  }

  backend "gcs" {}
}

# The project is always explicit, and API quota is billed to it, as it is
# with user credentials and a quota project.
provider "google" {
  project               = var.project
  region                = var.region
  billing_project       = var.project
  user_project_override = true
}

module "installation" {
  source = "../../modules/installation"

  project                = var.project
  fugaro_project         = var.fugaro_project
  region                 = var.region
  runs_bucket            = var.runs_bucket
  state_bucket           = var.state_bucket
  names                  = var.names
  log_bucket_description = var.log_bucket_description
  bucket_lifecycle       = var.bucket_lifecycle
  enable_vertex          = var.enable_vertex
  manage_apis            = var.manage_apis
  launchers              = var.launchers
  operators              = var.operators
  budget                 = var.budget
  alert_email            = var.alert_email
  registry_cleanup       = var.registry_cleanup
  adopt_legacy_registry  = var.adopt_legacy_registry
  log_isolation          = var.log_isolation
}
