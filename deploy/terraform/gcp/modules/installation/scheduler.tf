# The account Cloud Scheduler starts each repository's image check as. The
# repository module grants it run access on each check job; its display name
# is the ownership mark, since service accounts carry no labels.
resource "google_service_account" "scheduler" {
  project      = var.project
  account_id   = var.names.scheduler_service_account_id
  display_name = "Fugaro scheduler"

  depends_on = [google_project_service.this]
}

# Creating a Scheduler job that runs as this account needs actAs on it.
#
# The account is a literal path built from var.project and the account's own
# ID, not the account resource's name attribute, for the reason deployer.tf's
# token-creator grant gives for the same choice: that attribute is unknown at
# plan time (the account is created by this same apply), and a grant whose
# account nobody can name is a grant nobody can check. Here it is worse than
# unreadable — this module also creates the deployer account, and the review
# that screens the plan (internal/infra/tf's Cover.onDeployer) refuses any
# grant in a deployer-creating module whose account is computed, precisely
# because it could be on the deployer. A computed account here therefore made
# the installation apply stop riding the run's one confirmation and ask for its
# own, printing a reason that was false about this module's own grant. The ID
# is var.names.scheduler_service_account_id, checked by variables.tf's
# validation to be a service account ID, so the literal cannot name anything
# but the account above.
resource "google_service_account_iam_member" "scheduler_user" {
  for_each = toset(var.operators)

  service_account_id = "projects/${var.project}/serviceAccounts/${var.names.scheduler_service_account_id}@${var.project}.iam.gserviceaccount.com"
  role               = "roles/iam.serviceAccountUser"
  member             = each.value

  depends_on = [google_service_account.scheduler]
}
