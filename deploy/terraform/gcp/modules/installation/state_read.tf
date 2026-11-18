# The state readers' reach into the installation's database: the front door's
# (O16b, D2020 (a)) and the voice hop's (P5.3.3). It is the ONE role that lets
# a process read the orchestrator's node, and the ONE permission that role may
# carry.
#
# WHO READS THROUGH IT. The front door serves get_state and get_events as its
# own account `fugaro-mcp` (ee/coordinator/cli/coordinator_serve.go registers
# them over mcpauth's own reach, over internal/rtdb), and the state they answer
# with lives in the project's Firebase Realtime Database under
# orchestrator/<id>/{state,lease,audit} (D1220). The voice hop (voice.tf) reads
# the device node config/admin/devices/$uid (D166) and the project's voice
# policy node before a phone's speech leaves it. Until this file the account
# held no database grant at all, so a turned-on front door could serve the two
# tools and read nothing.
#
# Why the role is ONE permission, and why it is a role of its own. Realtime
# Database IAM has NO path-level scope: a binding is per instance and reaches
# the whole database, so what a role can say is which OPERATIONS its holder may
# make. firebasedatabase.instances.get is the only read there is — get is both
# the instance read and the data read — and firebasedatabase.instances.update,
# the only write (a node write is an update), is deliberately NOT bound: the
# state group's writes stay with `fugaro orchestrator patch` and the person's
# own credential (D1223, D1241), and apply_patch is served by nothing (D2021).
# The write direction is therefore a property of IAM rather than a promise in a
# handler: there is no write permission for a later revision of the front door
# to reach for. state_read_test.go's
# TestFrontDoorStateRoleHoldsNoDatabaseWrite fails first if one is added back.
#
# WHY THE TWO READERS SHARE THE ONE ROLE, and why that is not a widening. They
# read the same instance, so a second role could only differ by holding a
# permission the first does not — which is exactly the write this file exists to
# withhold. What the hop may reach is therefore the same whole-instance read the
# front door already has, and what narrows each reader to its own nodes is its
# own code and not the grant: internal/voicesvc's own paths for the hop
# (voice.tf's header states the reach and its cost), mcpauth's and
# ee/coordinator's for the door. That is also why there is no path-prefix
# condition here: RTDB cannot express one, and the credentials service's
# database binding states the same reasoning.
#
# It is a file of its own, beside the store's, for the store's own reason: the
# front door's account may be named by exactly one grant in coordinator.tf (the
# count TestCoordinatorServiceHasItsOwnAccount reads that file alone), so each
# of the account's other grants lives beside the resource it binds.
#
# The role is declared in the FIREBASE project — the one the database belongs
# to (D6) — and not in var.project, and the local below names that project. It
# cannot be a bare var.coordinator.firebase_project: this file is created when
# EITHER reader is on, and an installation that turned the hop on and never
# asked for a front door has no var.coordinator at all, so the attribute would
# be an attribute of null. The two variables name the same database in any real
# installation (the coordinator's Firebase project and the credentials
# service's are the installation's one Firebase project), and the coordinator's
# wins when both are set, so an installation that already runs the front door
# plans the value it always planned.
#
# coalesce and not a nested ternary alone, because a coordinator block this
# module is handed need not name a firebase_project at all (an installation
# whose repositories are all GitHub keeps no coordinator read of the database,
# and a root whose front door is on can carry such a block): a null there is a
# value and not an error, so a ternary alone would pick it and the role would be
# created in no project. coalesce skips it for the credentials service's own
# member, which names the same database. try() over the two is what keeps a
# hand-written document carrying one block and not the other from failing as
# "attempt to get attribute from null value" — and the case where neither names
# a project at all, which coalesce refuses outright because every argument it
# was given is empty, is the role's own precondition below, whose sentence an
# operator can act on (voice.tf's service precondition is the same guard for the
# hop's environment).
locals {
  state_read_firebase_project = try(coalesce(
    local.mcp_server_on ? var.coordinator.firebase_project : null,
    local.voice_on ? var.credentials_service.firebase_project : null,
  ), "")
}

# The role is a LITERAL and not the role resource's own name attribute, for the
# credentials role's reason: google_project_iam_custom_role's name is computed
# (the provider fills it during apply), so a reference reaches the plan as an
# UNKNOWN role and internal/infra/tf's cover could then admit any custom role
# of the plan. Written out through the local above — which is a plain string
# known at plan time — the plan-time string says exactly
# firebasedatabase.instances.get on the database's own project, and a widened
# role is refused by that cover rather than invisible to it.
resource "google_project_iam_custom_role" "state_read" {
  count = local.mcp_server_on || local.voice_on ? 1 : 0

  project = local.state_read_firebase_project
  role_id = "fugaroStateRead"
  title   = "Fugaro state readers (database read only)"
  permissions = [
    "firebasedatabase.instances.get", # read: the orchestrator's state and audit nodes, and nothing this role could change
  ]

  # A reader is on and neither block names the database's project: that is a
  # plan that cannot apply (a role is created in a project, and "" is none), so
  # it is refused here with the sentence an operator needs rather than as an
  # unusable project further down.
  lifecycle {
    precondition {
      condition     = local.state_read_firebase_project != ""
      error_message = "the state readers' role is created when the front door or the voice hop is on, and neither block this module reads the database's project from names one: var.coordinator.firebase_project (the front door's, where the coordinator's node lives) and var.credentials_service.firebase_project (the voice hop's) are both unset or empty. Write the block of the reader this installation runs — every 'fugaro update' writes them — and apply again."
    }
  }
}

# ... and the front door's binding, on that role, to the front door's own
# account. No condition: RTDB cannot express a path prefix in IAM, so a
# condition here would be a fence that looks like one and is not — the same
# reasoning the credentials service's database binding states. No other member:
# no operator, no job account and not the sweep's account reads the database
# through this role, and nobody is granted actAs on the account either
# (coordinator.tf's own rule).
#
# It arrives with the front door's opt-in (local.mcp_server_on) and with
# nothing else: an installation that never asked for a front door plans no
# member for the door — which is every installation by default (D934).
resource "google_project_iam_member" "state_read" {
  count = local.mcp_server_on ? 1 : 0

  project = local.state_read_firebase_project
  role    = "projects/${local.state_read_firebase_project}/roles/fugaroStateRead"
  member  = google_service_account.mcp_server[0].member

  depends_on = [google_project_iam_custom_role.state_read]
}

# ... and the voice hop's binding of the same role to the hop's own account
# (voice.tf), which is the second half of the pair the role is made for. It is
# a binding of its own rather than an entry in the one above because the two
# arrive on different switches and name different accounts: the front door's is
# created only with the door, and this one only with the hop, so an
# installation with one of the two plans one member and not both.
#
# It reaches what the front door's binding reaches and no more (the
# instance-wide read, which is the whole grant), and voice.tf's header and
# SECURITY.md state what that means for the hop: every node of the database is
# readable by the account, and internal/voicesvc's own paths are what keep it to
# the device node and the policy. No condition, for the reason above, and no
# actAs on the account either.
resource "google_project_iam_member" "voice_state_read" {
  count = local.voice_on ? 1 : 0

  project = local.state_read_firebase_project
  role    = "projects/${local.state_read_firebase_project}/roles/fugaroStateRead"
  member  = google_service_account.voice[0].member

  depends_on = [google_project_iam_custom_role.state_read]
}
