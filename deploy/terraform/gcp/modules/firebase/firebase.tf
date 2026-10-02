# Firebase on the user's GCP project, and its Realtime Database. This root
# never creates the project or links billing: fugaro init --firebase adopts
# a project that exists and has billing, and refuses otherwise.
resource "google_firebase_project" "this" {
  provider = google-beta

  project = var.project

  depends_on = [google_project_service.this]
}

# RTDB offers three locations and can't move one later; D4 chose
# us-central1. The default instance's ID is <project>-default-rtdb, which is
# also the one Firebase creates itself, so an adopted project keeps it. It
# holds the budget counters and the run registry, so a plan never deletes
# it. The rules and the /fugaro/mark are written by fugaro init over REST,
# not by Terraform.
resource "google_firebase_database_instance" "this" {
  provider = google-beta

  project       = var.project
  region        = "us-central1"
  instance_id   = "${var.project}-default-rtdb"
  type          = "DEFAULT_DATABASE"
  desired_state = "ACTIVE"

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_firebase_project.this]
}

# The web API key a job signs in with: signInWithCustomToken and the refresh
# both go through Identity Toolkit and Secure Token, and the key may call
# nothing else. It isn't a secret (it is in every Firebase web app), the
# restriction only stops it from being used against other APIs.
resource "google_apikeys_key" "web" {
  project      = var.project
  name         = var.names.api_key
  display_name = "Fugaro run sign-in"

  restrictions {
    api_targets {
      service = "identitytoolkit.googleapis.com"
    }
    api_targets {
      service = "securetoken.googleapis.com"
    }
  }

  depends_on = [google_project_service.this]
}
