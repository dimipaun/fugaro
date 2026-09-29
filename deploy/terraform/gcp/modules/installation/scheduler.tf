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
resource "google_service_account_iam_member" "scheduler_user" {
  for_each = toset(var.operators)

  service_account_id = google_service_account.scheduler.name
  role               = "roles/iam.serviceAccountUser"
  member             = each.value
}
