package cli

import (
	"maps"
	"slices"
)

// The document `fugaro workspace ls --json` and `fugaro workspace show --json`
// print (spec.md §6): one versioned shape, read offline from this machine's own
// project configs.
//
// It is a NAMED SET OF FIELDS and deliberately not a marshalled
// localcfg.Config, which is the `config list` rule (config_list.go): a
// marshalled config would publish every key the file happens to carry — a
// credential among them — and would change shape every time the config does.
// What it publishes is the membership, and the pin's assertion key is not one
// of the fields: the document says a membership is pinned, never what the
// member's key is, so no reader can log one by accident.
//
// Members are only ever ADDED within a version: a renamed or re-meant member is
// a new member under a new name, and removing or retyping one bumps
// workspaceLsDocVersion. No object here is closed for that reason —
// additionalProperties is left true everywhere in the schema — because a
// consumer that refused a member this schema does not know would refuse a newer
// CLI's document under the same version.
const workspaceLsDocVersion = 1

// workspaceDoc is the whole document.
type workspaceDoc struct {
	Version    int               `json:"version"`
	Workspaces []workspaceDocRow `json:"workspaces"`
	// Warnings are the project configs this read could not use, each naming the
	// config and the loader's own refusal. They are in the document and not
	// only on stderr, because a consumer of the JSON would otherwise read a
	// short listing as a complete one.
	Warnings []string `json:"warnings"`
}

// workspaceDocRow is one workspace as this machine holds it.
type workspaceDocRow struct {
	Name string `json:"name"`
	// Owner reports whether this machine holds the workspace's own project
	// config, which is where the member table and the cap are written. It is
	// what makes Members readable: false means the members are on the
	// workspace owner's machine and this document says nothing about them,
	// rather than saying the workspace has none.
	Owner bool `json:"owner"`
	// Cap is the workspace's own ceiling, as this machine's own config states
	// it. Absent when there is none and when Owner is false.
	Cap *workspaceDocCap `json:"cap,omitempty"`
	// Members is the member table of the workspace's own config here, in name
	// order: [] is a workspace whose table declares no member yet, and null is
	// a machine that does not hold that config at all. A consumer reads owner
	// before it reads this.
	Members []workspaceDocMember `json:"members"`
	// Memberships are the project configs on this machine that name the
	// workspace, in name order: the member's own half.
	Memberships []workspaceDocMembership `json:"memberships"`
	// Problems are the half-done memberships this machine's files show (W2's
	// two-sided check, as far as this machine can make it). Empty on a machine
	// that holds no owner's config.
	Problems []string `json:"problems"`
}

// workspaceDocCap is the workspace's own spend ceiling, in the file's own unit:
// US dollars a month, the same amount `fugaro workspace cap set --monthly`
// takes.
type workspaceDocCap struct {
	MonthlyUSD float64 `json:"monthly_usd"`
}

// workspaceDocMember is one entry of the workspace owner's member table: how
// the workspace reaches that member. The member's database URL is not among the
// fields and never will be — it is derived from the Firebase project the
// verified iss names (finding 6) — and neither is a runs bucket (O1).
type workspaceDocMember struct {
	Project            string   `json:"project"`
	JobServiceAccounts []string `json:"job_service_accounts"`
	FirebaseProject    string   `json:"firebase_project"`
	FP                 string   `json:"fp"`
	ExchangeURL        string   `json:"exchange_url"`
	Repos              []string `json:"repos"`
}

// workspaceDocMembership is one project config on this machine that names the
// workspace: the member's own half, which is the key and the pin it carries.
// There is no assertion-key field: the document says the membership is pinned,
// and the key itself is the member's, printed nowhere.
type workspaceDocMembership struct {
	Project         string `json:"project"`
	FirebaseProject string `json:"firebase_project"`
	Issuer          string `json:"issuer"`
	ServiceAccount  string `json:"service_account"`
}

// document is one workspace's row as the document prints it.
func (r *workspaceRow) document(m *workspaceMachine) workspaceDocRow {
	row := workspaceDocRow{
		Name:        r.name,
		Owner:       r.owner != nil,
		Memberships: []workspaceDocMembership{},
		Problems:    r.problems(m),
	}
	if row.Problems == nil {
		row.Problems = []string{}
	}
	for _, mem := range r.memberships {
		row.Memberships = append(row.Memberships, workspaceDocMembership{
			Project:         mem.project,
			FirebaseProject: mem.pin.FirebaseProject,
			Issuer:          mem.pin.Issuer,
			ServiceAccount:  mem.pin.ServiceAccount,
		})
	}
	if r.owner == nil {
		return row
	}
	row.Members = []workspaceDocMember{}
	if r.owner.WorkspaceCap != nil {
		row.Cap = &workspaceDocCap{MonthlyUSD: r.owner.WorkspaceCap.Monthly}
	}
	for _, p := range slices.Sorted(maps.Keys(*r.owner.WorkspaceMembers)) {
		mm := (*r.owner.WorkspaceMembers)[p]
		row.Members = append(row.Members, workspaceDocMember{
			Project:            p,
			JobServiceAccounts: nonNil(mm.JobServiceAccounts),
			FirebaseProject:    mm.FirebaseProject,
			FP:                 mm.FP,
			ExchangeURL:        mm.ExchangeURL,
			Repos:              nonNil(mm.Repos),
		})
	}
	return row
}
