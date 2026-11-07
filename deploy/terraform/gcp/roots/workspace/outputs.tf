# fugaro update reads these with terraform output -json: the account the
# workspace's own service runs as, which W6 deploys the workspace's bundle onto
# and which the workspace's own config records.
#
# The database's URL is deliberately not here: the workspace's own project is an
# installation, so its budget.rtdb_url is already in the local config `fugaro
# update --firebase-project` wrote, and a second source of the same URL is a
# second value that can disagree with it.

output "project" {
  value = var.project
}

output "workspace_service_account" {
  value = google_service_account.workspace.email
}

