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


# The workspace's service URL, which the update reads back on the apply after
# the one that creates the service and writes into credentials_service.url: it
# is the audience a member's run mints its ID token for, so a revision that
# guessed it would accept a token minted for another service. Empty when the
# service is not deployed, which is what the update reads as "there is nothing
# to carry forward".
output "workspace_service_url" {
  value = local.credentials_service_on ? google_cloud_run_v2_service.workspace[0].uri : ""
}
