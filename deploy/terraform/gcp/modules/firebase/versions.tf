terraform {
  required_version = ">= 1.7.0, < 2.0.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 8.4.0, < 9.0.0"
    }
    # google_firebase_project and google_firebase_database_instance exist
    # only in google-beta (A5).
    google-beta = {
      source  = "hashicorp/google-beta"
      version = ">= 8.4.0, < 9.0.0"
    }
  }
}
