# The repository root: one repository's resources, in a state of its own,
# so an apply can only ever touch that repository. fugaro init --repo writes
# it into a workdir with terraform.tfvars.json (the installation's outputs
# included, so there is no remote state to read) and a backend config
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

module "repo" {
  source = "../../modules/repo"

  project          = var.project
  fugaro_project   = var.fugaro_project
  region           = var.region
  installation     = var.installation
  repo             = var.repo
  allow_job_delete = var.allow_job_delete
}
