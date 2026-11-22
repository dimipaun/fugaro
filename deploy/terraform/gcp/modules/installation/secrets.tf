# The installation's project-wide secret containers (design
# docs/project-secrets/spec.md §4). They hold no value here: values are
# added with fugaro secrets set NAME --project-wide, so none ever reaches
# Terraform or its state. The labels are the marks that command sets and
# that fugaro update's discovery checks before adopting one.
#
# for_each is var.project_secrets, which fugaro update computes and unions with
# every correctly-labelled container that already exists live: a config edit
# can therefore only ever add a key, never drop one, since prevent_destroy
# turns a dropped key into a hard terraform plan failure (N3).
resource "google_secret_manager_secret" "project" {
  for_each = var.project_secrets

  project   = var.project
  secret_id = each.value
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
    fugaro_secret  = each.key
  }

  # A container the CLI created is replicated in the installation's region, and
  # replication is creation-time only: a differing value here would plan a
  # replacement, which prevent_destroy refuses and which deletes every version
  # first. So the field is ignored below, and this block decides only a
  # container Terraform itself creates.
  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [replication]
  }
}

locals {
  # Every (secret, operator) pair, for the operator grants below.
  operator_project_secrets = { for p in setproduct(keys(var.project_secrets), var.operators) : "${p[0]} ${p[1]}" => { secret = p[0], member = p[1] } }
}

# Operators store values and read the secrets' metadata, never their values.
resource "google_secret_manager_secret_iam_member" "operator" {
  for_each = merge(
    { for k, v in local.operator_project_secrets : "${k} secretVersionAdder" => merge(v, { role = "roles/secretmanager.secretVersionAdder" }) },
    { for k, v in local.operator_project_secrets : "${k} viewer" => merge(v, { role = "roles/secretmanager.viewer" }) },
  )

  project   = var.project
  secret_id = google_secret_manager_secret.project[each.value.secret].secret_id
  role      = each.value.role
  member    = each.value.member
}

# The voice hop's provider keys (P5.3.5, design
# docs/mobile/design/voice-evaluation-switch.md §4.4 point 4): ONE container
# holding the document a call is credentialed by — a JSON object of register
# names to keys, `{"deepgram":"…","elevenlabs":"…"}` — so a provider added to
# the register is a member added to a value an operator already set and not a
# new container, a new grant and a new environment name.
#
# WHO READS IT, AND WHO ELSE CANNOT. The account is voice.tf's own — the
# service the hop runs as — and the grant below is the ONLY principal the
# container names: no job account, no launcher, no operator (an operator's
# read of THIS container is their project IAM's, the same Owner-or-Editor read
# doctor_secret_readers.go names once for every container, and never a grant
# written here). This is the one credential the hop holds, and it is the one
# that makes an utterance cost money at a third party: SECURITY.md's disclosure
# and voice.tf's header are where what a compromised revision of this process
# then reaches is written down.
#
# ONE CONTAINER AND NEVER A VERSION, credentials.tf's rule for the credentials
# bundle and for its reason: the document is written by the person who holds
# the provider keys —
#
#   gcloud secrets versions add fugaro-voice-provider-keys --data-file=<file>
#
# — with their own identity and never by Terraform, so no provider key ever
# reaches Terraform's state or this module's plan. The container is created
# before the value exists, and that is the state it is in until the owner sets
# one: the hop reads it per call and refuses a call whose key it cannot read
# (internal/voicesvc's ErrKeyUnavailable), which is a named refusal and not a
# call made with an empty credential.
#
# THE ID IS A LITERAL AND NOT A DERIVED NAME. Every project-wide secret of this
# tree is named by gcp.ProjectSecretID (internal/infra builds the tfvars names
# from it), and Terraform cannot compute one: the suffix is a hash. The hop
# reads this container by its OWN constant — internal/backend/gcp's
# VoiceProviderKeysSecret, which is this same string — beside the GCP project
# its environment names, so the two spellings are one fact; deploy/terraform's
# TestVoiceProviderKeysContainerIsTheGoSpelling is what holds them together.
# The labels are credentials.tf's bundle's, and
# fugaro_secret is deliberately absent for that resource's reason: that label
# is what `fugaro update`'s discovery and `fugaro secrets ls --project-wide`
# read to decide which *logical secret* of the project's set a container holds,
# and this container is not one of those — carrying it would make discovery
# union this very container into repo.project_secrets beside this resource and
# the apply would plan the same secret ID twice.
#
# prevent_destroy is the reason it is for the bundle: a config edit that drops
# the container must be a hard plan failure, not a deletion of the keys every
# remote utterance of the installation is credentialed by.
resource "google_secret_manager_secret" "voice_provider_keys" {
  count = local.voice_on ? 1 : 0

  project   = var.project
  secret_id = "fugaro-voice-provider-keys"
  labels = {
    fugaro         = "managed"
    fugaro_project = var.fugaro_project
  }

  # A container the CLI created is replicated in the installation's region, and
  # replication is creation-time only: a differing value here would plan a
  # replacement, which prevent_destroy refuses and which deletes every version
  # first. So the field is ignored below, and this block decides only a
  # container Terraform itself creates.
  replication {
    user_managed {
      replicas {
        location = var.region
      }
    }
  }

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [replication]
  }

  depends_on = [google_project_service.this]
}

# The ONE grant on it: roles/secretmanager.secretAccessor, to the hop's own
# account and to nobody else — no condition (the container is the fence, and
# Secret Manager scopes a binding per secret, so a condition here would fence
# nothing), and no second member anywhere. internal/infra/tf's cover reads the
# container of this binding rather than accepting the row alone
# (voiceProviderKeyGrant), because the provider keys are the whole of what a
# call presents and a plan that pointed this principal at another container of
# the run would otherwise be covered.
resource "google_secret_manager_secret_iam_member" "voice_provider_keys" {
  count = local.voice_on ? 1 : 0

  project   = var.project
  secret_id = google_secret_manager_secret.voice_provider_keys[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.voice[0].member
}
