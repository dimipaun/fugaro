# The APIs the installation and its repositories use. Enabling one is free;
# using it may be billed. Nothing here ever disables an API: other things in
# the project may depend on it.
locals {
  apis = toset(concat(
    [
      "run.googleapis.com",
      "storage.googleapis.com",
      "secretmanager.googleapis.com",
      "artifactregistry.googleapis.com",
      "cloudbuild.googleapis.com",
      "cloudscheduler.googleapis.com",
      "logging.googleapis.com",
      "monitoring.googleapis.com",
      "iam.googleapis.com",
      # Project IAM members need it.
      "cloudresourcemanager.googleapis.com",
    ],
    var.enable_vertex ? ["aiplatform.googleapis.com"] : [],
    var.budget == null ? [] : ["billingbudgets.googleapis.com"],
  ))
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? local.apis : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}
