package tf

// The member's half of the workspace cap (docs/workspaces/spec.md §5; task W7
// of docs/workspaces/implementation-plan.md): the ONE grant a member's own
// `fugaro update` makes so that the workspace's credentials service can read
// the member's own ledger — and the ONE permission that grant may carry.
//
// It is a READ, and only a read. The workspace's cap is measured against the
// figure the workspace's service keeps in its OWN database (the reserve and
// settle of every call it pays for); what it needs from a member is the
// member's ledger, read once per reconciliation run so the two figures can be
// reported when they disagree beyond a stated bound. Realtime Database IAM has
// no path-level scope — a binding is per instance and reaches the whole
// database — so the narrowest grant that can read one node is the instance's
// read permission alone. firebasedatabase.instances.update is NOT bound, and
// that is the point rather than an omission: the write direction of "a
// member's database is the member's" is a property of IAM here rather than of
// the service's code, because there is no write permission for a later
// revision of the service to reach for. TestMemberUpdateBindsNoWriteOnThe-
// MembersDatabase fails first if one is ever added back.
//
// Who is bound is the MEMBER's decision and comes from the MEMBER's own
// config: localcfg's WorkspacePin.ServiceAccount, the fourth value `fugaro
// workspace add` writes beside the issuer, the Firebase project and the
// assertion key. It is never read from the workspace's published record —
// §2's record names no service account, for the same reason it names no
// issuer, so a workspace cannot widen its own reach by publishing a different
// one. A member whose config names no workspace passes an empty account, and
// nothing is created: the absent pin is not an account, and the module's
// resources are counted on that variable being non-empty.
//
// A managed project's plan gate refuses this binding, and that is the correct
// answer rather than a limitation: the member hands read access to a principal
// that is not this installation's own, which is a loosening, and the managed
// cover admits no project-level IAM at all (managed_cover.go's
// google_project_iam_member row).
//
// What actually happens is one step short of that refusal: this binding is
// planned by the Firebase root, and the Firebase root is planned only by
// `fugaro update --firebase-project` (the Firebase stage's own skip: without the
// flag the stage does not run). That flag is an installation-shaping one
// (init.go's installationFlags), and a shaped run keeps no gate at all
// (initRun.gateable), so the managed cover is never handed this plan in the
// first place — a run that plans the binding writes no audit record for it and
// the gate's refusal is not what stops it
// (TestUpdateFirebaseProjectKeepsItsOwnConfirmationOnAManagedProject drives the
// run and holds both facts). The cover's refusal is what would answer if a
// later shape of this grant were ever judged by the gate; meanwhile the stage's
// own confirmation is what applies it, which the member's owner gives at their
// own terminal on `fugaro update --firebase-project <its own project>` — once
// per member, at the moment they join the workspace — and a coding agent's
// session is left the step rather than applying it.

const (
	// WorkspaceReadRole is the custom role modules/firebase/workspace_access.tf
	// creates in the member's own Firebase project, holding
	// WorkspaceReadPermissions and nothing else. It is the read half of the
	// credentials service's own role (CredentialsDatabaseRole) split out
	// rather than reused: that role holds the write too, and this grant may
	// not.
	WorkspaceReadRole = "fugaroWorkspaceRead"
	// WorkspaceAccessVariable is the name the grant's account travels under,
	// as the Firebase module's variable, the Firebase root's variable and the
	// Firebase root's tfvars key (FirebaseSpec.WorkspaceServiceAccount). One
	// spelling for the three, so a plan's value can be read back to the pin
	// it came from.
	WorkspaceAccessVariable = "workspace_service_account"
)

// WorkspaceReadPermissions are the role's permissions: the instance's read,
// and no second one. They are a var rather than a literal inside the
// RolePermissions map so a test can hold the map, the module's own source and
// this list to each other (TestMemberUpdateBindsNoWriteOnTheMembersDatabase).
var WorkspaceReadPermissions = []string{"firebasedatabase.instances.get"}
