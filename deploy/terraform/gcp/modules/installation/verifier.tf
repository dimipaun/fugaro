# The verifier service account (C3.9g1, D1068, CSR14): the identity that may
# ask the credentials service's verify route whether a verdict's signature is
# that service's. It is the ONE caller class of that route, and the account is
# the installation's own rather than a person's: every `user:` operator is
# already rendered into the service's FUGARO_PERSON_ACCOUNTS
# (credentials.tf), and internal/credsvc's NewAuthenticator refuses an address
# that is both a person and a verifier — so an installation whose
# verifier_accounts named the owner would not start at all.
#
# The account is created by the same apply that turns the service on, and
# nothing else hangs off it. It reads no secret, mounts no bucket, holds no
# project role and runs no job: its whole reach is the one invoker grant on the
# service (iam.tf) and the one token-creator grant the operators hold on IT
# (iam.tf) — the impersonation the owner's own
# `gcloud auth print-identity-token --impersonate-service-account=…` and the
# launcher's own mint both stand on.
#
# The ID is a LITERAL, and that is the load-bearing half of the answer: a
# module that knows var.project and an account ID fixed in the source makes the
# address a fact of the CONFIGURATION rather than of the apply —
# `<id>@<project>.iam.gserviceaccount.com`, computable by internal/infra before
# anything is planned (infra.VerifierAccountID, the spelling
# internal/cli/doctor_secret_readers.go already uses for the credentials
# account). That is what lets one `fugaro update` both write the address into
# the owner's local config — and so into the published policy node — and have
# the same apply's service admit it, with no output to read back and no second
# run nobody was told to make (CLAUDE.md's HARD RULE).
resource "google_service_account" "fugaro_verifier" {
  count = local.credentials_service_on ? 1 : 0

  project      = var.project
  account_id   = "fugaro-verifier"
  display_name = "Fugaro verifier"

  depends_on = [google_project_service.this]
}
