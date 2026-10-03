# The APIs the FP uses. Enabling one is free; using it may be billed.
# Nothing here ever disables an API: other things in the project may depend
# on it. iamcredentials is enabled here because the signer, whose signJwt
# call it serves, lives in the FP. firestore and firebaserules serve the spend history (M9d); the
# Firestore database itself is not Terraform (init --firebase ensures it).
locals {
  apis = toset([
    "firebase.googleapis.com",
    "firebasedatabase.googleapis.com",
    "identitytoolkit.googleapis.com",
    "securetoken.googleapis.com",
    "iamcredentials.googleapis.com",
    "apikeys.googleapis.com",
    "iam.googleapis.com",
    "firestore.googleapis.com",
    "firebaserules.googleapis.com",
    # Project IAM members need it.
    "cloudresourcemanager.googleapis.com",
  ])
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? local.apis : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}
