# Every grant is a member resource, never a binding or policy, so nothing
# here takes away access someone else granted. Job accounts get nothing in
# the FP: a run holds a Firebase ID token, which isn't an IAM identity.

# The key that signs the runs' custom tokens. It is granted NO roles, in the
# FP or anywhere: signing goes through IAM signJwt as this account, and
# nothing on this account's own identity is ever used.
resource "google_service_account" "signer" {
  project      = var.project
  account_id   = var.names.signer_account_id
  display_name = "Fugaro token signer"
  description  = "Signs the custom tokens of the Fugaro project ${var.fugaro_project}'s runs. Holds no roles."

  depends_on = [google_project_service.this]
}

# signJwt and nothing else: narrower than serviceAccountTokenCreator, which
# would also let its holder mint access tokens as the signer.
resource "google_project_iam_custom_role" "token_minter" {
  project     = var.project
  role_id     = var.names.minter_role_id
  title       = "Fugaro token minter"
  permissions = ["iam.serviceAccounts.signJwt"]

  depends_on = [google_project_service.this]
}

locals {
  people = toset(concat(var.launchers, var.operators))
  admins = toset(concat(var.admins, var.budget_admins))
}

# Launchers and operators mint tokens, granted on the signer account alone.
resource "google_service_account_iam_member" "minter" {
  for_each = local.people

  service_account_id = google_service_account.signer.name
  role               = google_project_iam_custom_role.token_minter.name
  member             = each.value
}

# ... and read the database (fugaro watch, budget show).
resource "google_project_iam_member" "viewer" {
  for_each = local.people

  project = var.project
  role    = "roles/firebasedatabase.viewer"
  member  = each.value
}

# Budget admins (D6) write /config over IAM, which bypasses the rules.
resource "google_project_iam_member" "admin" {
  for_each = local.admins

  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = each.value
}

# ... and read the spend history in Firestore (fugaro report). Viewer only:
# nobody but the history account writes it, and it writes through
# datastore.user, below. Launchers, operators and admins; no domain or
# wildcard member can get here (the variables' validation).
resource "google_project_iam_member" "datastore_viewer" {
  for_each = setunion(local.people, local.admins)

  project = var.project
  role    = "roles/datastore.viewer"
  member  = each.value
}

# The history job's account: its sweep edits the registry and the run
# ledgers, and deletes the Auth users of expired runs. It is the most
# privileged new identity (firebasedatabase.admin could lift caps; its code
# never writes /config, which IAM can't enforce). Trivy flags a privileged
# role on a service account: accepted by design §11, and only this account
# (and no job account) holds one on the FP.
#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "history_database" {
  project = var.project
  role    = "roles/firebasedatabase.admin"
  member  = "serviceAccount:${var.history_account}"
}

#trivy:ignore:AVD-GCP-0007
resource "google_project_iam_member" "history_auth" {
  project = var.project
  role    = "roles/firebaseauth.admin"
  member  = "serviceAccount:${var.history_account}"
}

# The rollover writes the spend history: documents only (datastore.user can't
# create or delete a database or change indexes; never datastore.owner).
resource "google_project_iam_member" "history_firestore" {
  project = var.project
  role    = "roles/datastore.user"
  member  = "serviceAccount:${var.history_account}"
}

# The Firestore client sends X-Goog-User-Project: <this project> (user
# credentials need a quota project, and a service account's own project does
# not have the Firestore API enabled), which requires
# serviceusage.services.use on it. serviceUsageConsumer is that one
# permission set, held by the history account and by the people who get
# datastore.viewer above, on this project only; no domain or wildcard member
# can get here (the variables' validation).
resource "google_project_iam_member" "usage_consumer" {
  for_each = setunion(local.people, local.admins)

  project = var.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = each.value
}

resource "google_project_iam_member" "history_usage" {
  project = var.project
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "serviceAccount:${var.history_account}"
}
