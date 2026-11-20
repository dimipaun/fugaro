# fugaro update reads these with terraform output -json: the account the
# workspace's own service runs as, which W6 deploys the workspace's bundle onto
# and which the workspace's own config records, and the SA fallback's IAM
# account, which WU20's Link has a member's owner grant roles/owner on their
# project and which the workspace commands impersonate.
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

# The name C2's member-operations contract fixes
# (docs/workspace-projects-users/implementation-plan.md, C2), so the SA authority
# and the commands above it read the account by one spelling: the email, never a
# bare account id, because every grant and every impersonation the CLI makes
# names a principal as an address.
output "workspace_iam_account" {
  value = google_service_account.workspace_iam.email
}

