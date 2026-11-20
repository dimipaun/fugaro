# SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
# See ee/LICENSE for the terms.

# The identity project's Firebase Auth side (D2781, D2793): the project linked to
# Firebase, the four sign-in methods the spec names, and one provider
# configuration per external identity provider.
#
# The providers' OAuth clients are the owner's: the console that registers an app
# with Google, Microsoft and Apple is a person's, and its client secret is a
# credential this repository never holds. Each is an input variable with no
# default, so an apply that was handed none refuses before it creates a provider
# that cannot complete a sign-in (F28, F37, U9).
#
# Email verification is not a field of any resource here: Identity Platform sends
# the verification mail from its own template, which is the owner's work (F28), and
# the signer refuses an assertion for a password identity whose `email_verified` is
# false (A1). This file holds which methods are on and which providers exist.

# Linking the project to Firebase is what makes Identity Platform's project-level
# resources below exist at all; the resource is in the beta provider (main.tf).
resource "google_firebase_project" "this" {
  provider = google-beta
  project  = var.project

  depends_on = [google_project_service.this]
}

# The sign-in methods. Google, Microsoft, Apple and email with a password are the
# spec's four (D2793), and the two Identity Platform offers that the spec does not
# name are off rather than left to the API's default: a default is a method nobody
# chose, and an anonymous identity is not a member of any project.
resource "google_identity_platform_config" "identity" {
  project = var.project

  sign_in {
    email {
      enabled           = true
      password_required = true
    }

    anonymous {
      enabled = false
    }

    phone_number {
      enabled = false
    }
  }

  depends_on = [google_firebase_project.this]
}

# One configuration per provider the spec names. The client id and secret are the
# owner's registration (F28); `enabled` is written rather than left out because a
# provider the app offers and Identity Platform refuses is a sign-in that fails
# after the user has chosen it.
resource "google_identity_platform_default_supported_idp_config" "google" {
  project       = var.project
  idp_id        = "google.com"
  enabled       = true
  client_id     = var.google_client_id
  client_secret = var.google_client_secret

  depends_on = [google_identity_platform_config.identity]
}

# Microsoft (Entra) SSO. Its tenant-verified email is not a verified email the
# signer may trust (F37, U9), so the app treats a Microsoft identity as it does
# any other: the assertion's `email_verified` is what the signer decides.
resource "google_identity_platform_default_supported_idp_config" "microsoft" {
  project       = var.project
  idp_id        = "microsoft.com"
  enabled       = true
  client_id     = var.microsoft_client_id
  client_secret = var.microsoft_client_secret

  depends_on = [google_identity_platform_config.identity]
}

resource "google_identity_platform_default_supported_idp_config" "apple" {
  project       = var.project
  idp_id        = "apple.com"
  enabled       = true
  client_id     = var.apple_client_id
  client_secret = var.apple_client_secret

  depends_on = [google_identity_platform_config.identity]
}

variable "google_client_id" {
  description = "The OAuth client id of the owner's Google registration (F28). No default: an apply handed none refuses rather than creating a provider no sign-in can complete."
  type        = string
}

variable "google_client_secret" {
  description = "The OAuth client secret of the owner's Google registration. Held by the owner and passed at apply time; this repository holds no client secret (D2793)."
  type        = string
  sensitive   = true
}

variable "microsoft_client_id" {
  description = "The application (client) id of the owner's Microsoft Entra registration (F28). No default, as the Google pair's."
  type        = string
}

variable "microsoft_client_secret" {
  description = "The client secret of the owner's Microsoft Entra registration. Held by the owner and passed at apply time."
  type        = string
  sensitive   = true
}

variable "apple_client_id" {
  description = "The Services ID of the owner's Sign in with Apple registration (F28). No default, as the other two."
  type        = string
}

variable "apple_client_secret" {
  description = "The client secret of the owner's Sign in with Apple registration. Held by the owner and passed at apply time."
  type        = string
  sensitive   = true
}
