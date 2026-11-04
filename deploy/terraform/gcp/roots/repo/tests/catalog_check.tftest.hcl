# The project's catalog check job (PIC4, D1148 task (ii)) and THIS repository:
# the one grant the repository root makes to it, and the fact that it makes no
# other.
#
# The job schedules a build of an entry of the project's image catalog, and
# that build runs as the account of the repository the entry belongs to —
# never as the project's, and never as another repository's. What makes it so
# is this single actAs (roles/iam.serviceAccountUser) on the build account,
# written here because this is where the build account lives and because the
# grant is this repository's fact. So the thing to pin is not that the grant
# exists: it is that the account reaches NOTHING else here — no second member
# on the build account, no role on this repository's bucket prefix, no secret,
# no registry. A grant that gave the project job the repository's own rights
# directly would make the entry's repository a fiction.
#
# The installation's account arrives in this root's tfvars
# (installation.catalog_check_service_account), which is what the installation
# root outputs; the value below is the name that output carries.

mock_provider "google" {}

variables {
  github = jsondecode(file("tests/testdata/github-vertex.tfvars.json"))
}

run "actas_on_the_build_account_only" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  override_resource {
    target          = google_service_account.build
    override_during = plan
    values = {
      name   = "projects/proj-1234/serviceAccounts/fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
      email  = "fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
      member = "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
    }
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = merge(var.github.installation, { catalog_check_service_account = "fugaro-catalog-check@proj-1234.iam.gserviceaccount.com" })
    repo           = var.github.repo
  }

  assert {
    condition = (
      length(google_service_account_iam_member.catalog_check_build) == 1 &&
      google_service_account_iam_member.catalog_check_build[0].role == "roles/iam.serviceAccountUser" &&
      google_service_account_iam_member.catalog_check_build[0].member == "serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com" &&
      google_service_account_iam_member.catalog_check_build[0].service_account_id == "projects/proj-1234/serviceAccounts/fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
    )
    error_message = "the project's catalog check job must hold one actAs (roles/iam.serviceAccountUser) on this repository's build account — the account the entry's own repository's build runs as — and on no other account here"
  }

  # The account reaches nothing else in this repository: not the build
  # account's own actAs (which is the build account acting as itself), not an
  # operator's, not the project members the build account holds, and not the
  # bucket prefix the daily check reads and writes. There is no
  # google_secret_manager_secret_iam_member naming it either, which is the
  # grant that would matter most: the build account keeps the BuildKit secret
  # path, and a project job holding it could read every repository's
  # credentials from one schedule.
  assert {
    condition = (
      google_service_account_iam_member.build_self.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com" &&
      google_storage_bucket_iam_member.catalog_read.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com" &&
      google_storage_bucket_iam_member.build.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com" &&
      alltrue([for m in google_project_iam_member.build : m.member == "serviceAccount:fugaro-b-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"]) &&
      alltrue([for m in google_service_account_iam_member.operator_build : m.member != "serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"]) &&
      alltrue([for m in google_secret_manager_secret_iam_member.build : m.member != "serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"]) &&
      google_artifact_registry_repository_iam_member.build_images.member != "serviceAccount:fugaro-catalog-check@proj-1234.iam.gserviceaccount.com"
    )
    error_message = "the catalog check job's only grant in this repository is the actAs: it holds no bucket role, no project role, no secret and no registry role of its own — those stay the build account's, which is what a build of an entry runs as"
  }
}

run "no_grant_while_the_installation_has_no_job" {
  command = plan

  module {
    source = "../../modules/repo"
  }

  variables {
    project        = var.github.project
    fugaro_project = var.github.fugaro_project
    region         = var.github.region
    installation   = var.github.installation
    repo           = var.github.repo
  }

  # The gate: an installation whose tfvars carry no catalog check account
  # (which is every installation that publishes no catalog, and every update
  # that runs before the installation root has created the account) is a
  # repository root that grants nobody actAs. The installation root applies
  # before the repository roots create their build accounts, so a grant that
  # ignored this would be planned against an account that does not exist yet.
  assert {
    condition     = length(google_service_account_iam_member.catalog_check_build) == 0
    error_message = "without installation.catalog_check_service_account the repository root must grant no actAs to a catalog check job"
  }
}
