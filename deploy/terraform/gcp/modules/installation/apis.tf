# The APIs the installation and its repositories use. Enabling one is free;
# using it may be billed. Nothing here ever disables an API: other things in
# the project may depend on it.
locals {
  apis = toset(concat(
    [
      "run.googleapis.com",
      "storage.googleapis.com",
      "secretmanager.googleapis.com",
      "artifactregistry.googleapis.com",
      "cloudbuild.googleapis.com",
      "cloudscheduler.googleapis.com",
      "logging.googleapis.com",
      "monitoring.googleapis.com",
      "iam.googleapis.com",
      # Project IAM members need it.
      "cloudresourcemanager.googleapis.com",
      # The whole-project inventory of a managed project reads through it:
      # the one query that sees resource types discovery does not know.
      "cloudasset.googleapis.com",
    ],
    var.enable_vertex ? ["aiplatform.googleapis.com"] : [],
    var.budget == null ? [] : ["billingbudgets.googleapis.com"],
    # The image-build estimator reads Cloud Build's prices from the public
    # Cloud Billing catalog at build time (D1550), on the caller's own
    # credentials. The lookup runs only where the budget backend is on —
    # without the ledger there is no dollar reservation to size, so a
    # backend-less installation must not look a price up at all — which is
    # what enable_budget names here. Enabling the API is free; the read is.
    var.enable_budget ? ["cloudbilling.googleapis.com"] : [],
    # The voice hop's google engine synthesizes speech through Cloud
    # Text-to-Speech (internal/voicesvc's provider.go, one constant endpoint),
    # and it makes that call as the hop's own account rather than with a
    # provider key: enabling the API IS that engine's credential, so a hop
    # whose project never enabled it answers every google call with
    # SERVICE_DISABLED and the register's row cannot be measured at all (P5.3.5,
    # Check 85). Gated on the hop like the entries above: the call exists only
    # for an installation that carries it.
    local.voice_on ? ["texttospeech.googleapis.com"] : [],
    # The consumer's signing key lives in Cloud KMS (mailbox_key.tf, plan task
    # J6): a key cannot be created in a project whose KMS API is off, and the
    # key exists exactly where its own switch does — the pairing front end or
    # the resident orchestrator. Gated on the same local as every entry above,
    # so an installation that has neither plans the API list it planned before
    # the key existed.
    local.mailbox_key_on ? ["cloudkms.googleapis.com"] : [],
  ))
}

resource "google_project_service" "this" {
  for_each = var.manage_apis ? local.apis : toset([])

  project                    = var.project
  service                    = each.value
  disable_on_destroy         = false
  disable_dependent_services = false
}
