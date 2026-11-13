# The front door's reach into the installation's database (O16b, D2020 (a)):
# the ONE grant that lets the state group read the orchestrator's node, and the
# ONE permission that grant may carry.
#
# The front door serves get_state and get_events as its own account
# `fugaro-mcp` (ee/coordinator/cli/coordinator_serve.go registers them over
# mcpauth's own reach, over internal/rtdb), and the state they answer with
# lives in the project's Firebase Realtime Database under
# orchestrator/<id>/{state,lease,audit} (D1220). Until this file the account
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
# It is a file of its own, beside the store's, for the store's own reason: the
# front door's account may be named by exactly one grant in coordinator.tf (the
# count TestCoordinatorServiceHasItsOwnAccount reads that file alone), so each
# of the account's other grants lives beside the resource it binds.
#
# The role is declared in the FIREBASE project and not in var.project: the
# database belongs to the Firebase root's project (D6), which the coordinator's
# own configuration names (var.coordinator.firebase_project), and binding a
# role declared in one project to a member of another is what cross-project IAM
# is — the shape the credentials service's database role and the history
# account's grant already have.
#
# The role is a LITERAL and not the role resource's own name attribute, for the
# credentials role's reason: google_project_iam_custom_role's name is computed
# (the provider fills it during apply), so a reference reaches the plan as an
# UNKNOWN role and internal/infra/tf's cover could then admit any custom role
# of the plan. Written out, the plan-time string says exactly
# firebasedatabase.instances.get on the coordinator's Firebase project, and a
# widened role is refused by that cover rather than invisible to it.
resource "google_project_iam_custom_role" "state_read" {
  count = local.mcp_server_on ? 1 : 0

  project = var.coordinator.firebase_project
  role_id = "fugaroStateRead"
  title   = "Fugaro MCP front door (database read only)"
  permissions = [
    "firebasedatabase.instances.get", # read: the orchestrator's state and audit nodes, and nothing this role could change
  ]
}

# ... and the one binding, on that role, to the front door's own account. No
# condition: RTDB cannot express a path prefix in IAM, so a condition here would
# be a fence that looks like one and is not — the same reasoning the
# credentials service's database binding states. No second member: no operator,
# no job account and not the sweep's account reads the database through this
# role, and nobody is granted actAs on the account either (coordinator.tf's own
# rule).
#
# It arrives with the front door's opt-in (local.mcp_server_on) and with
# nothing else: an installation that never asked for a front door plans exactly
# what it planned before this file existed, which is every installation by
# default (D934).
resource "google_project_iam_member" "state_read" {
  count = local.mcp_server_on ? 1 : 0

  project = var.coordinator.firebase_project
  role    = "projects/${var.coordinator.firebase_project}/roles/fugaroStateRead"
  member  = google_service_account.mcp_server[0].member

  depends_on = [google_project_iam_custom_role.state_read]
}
