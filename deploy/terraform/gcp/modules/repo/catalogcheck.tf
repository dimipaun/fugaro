# The project's catalog check job submits a build of one of THIS repository's
# catalog entries as this repository's build account (PIC4, D1148 task (ii)):
# the build reads the repository's secrets, pushes to its registry and moves
# the tag the entry names, which is exactly what that account is for — the
# entry's own repository's account, never the project's. That actAs
# (roles/iam.serviceAccountUser) is the whole of the grant: impersonating this
# account lets the project job do nothing this repository's own jobs could not
# already do, and it names one account rather than the account's own key.
#
# It is written here, in the repository root, because that is where the build
# account lives (build.tf creates it) and because the granting is this
# repository's fact: a repository whose tfvars do not carry the project job's
# account is a repository nothing scheduled may build, which is what an
# installation with no catalog and a repository being created in the same
# update both look like.
#
# Gated like check.tf's deploy_job, and for the same reason: the installation
# root applies before the repository roots create their build accounts, so the
# grant follows the account the installation has actually deployed rather than
# a name this root would otherwise have to spell. There is no role for the
# catalog check job to hold on the bucket or the project here, and no secret
# accessor: it submits a build and reads the record that build wrote, and both
# of those are the installation's grants (modules/installation/catalogcheck.tf).
resource "google_service_account_iam_member" "catalog_check_build" {
  count = var.installation.catalog_check_service_account == "" ? 0 : 1

  service_account_id = google_service_account.build.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${var.installation.catalog_check_service_account}"
}
