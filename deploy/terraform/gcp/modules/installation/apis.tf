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
      # The whole-project inventory of a managed project reads through it:
      # the one query that sees resource types discovery does not know.
      "cloudasset.googleapis.com",
    ],
    var.enable_vertex ? ["aiplatform.googleapis.com"] : [],
    var.budget == null ? [] : ["billingbudgets.googleapis.com"],
    # The image-build estimator reads Cloud Build's prices from the public
    # Cloud Billing catalog at build time (D1550), on the caller's own
    # credentials. The lookup runs only where the budget backend is on —
    # without the ledger there is no dollar reservation to size, so a
    # backend-less installation must not look a price up at all — which is
    # what enable_budget names here. Enabling the API is free; the read is.
    var.enable_budget ? ["cloudbilling.googleapis.com"] : [],
  ))
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? local.apis : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}
