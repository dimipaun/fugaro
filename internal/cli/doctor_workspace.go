package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"google.golang.org/api/googleapi"
	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// doctor_workspace.go is doctor's own lines for the workspace: the one
// workspace state the workspace's root cannot converge (the split installation),
// and — WS14, F20 — the workspace's service, the node it binds its callers to,
// the pool it offers, a member's pin and the credential route each of its
// repositories publishes (R4).
//
// The split check reads the local config and nothing else: no cloud, no
// credentials. The rest read the cloud, and each reads only what its own line
// is about: the service from Cloud Run's admin API, the node from the
// workspace's own database, the pool from the workspace's own bundle, and — on
// a member's machine — the route from the leaf WS9 publishes it at. Nothing
// here writes, and nothing here computes the reconciliation alarm: the ledger it
// would need is built by nothing that runs and a member grants its read to the
// workspace's service account alone, so the alarm cannot be computed from the
// owner's machine (D2991, F31). What the workspace spent is `fugaro budget`'s
// question, not this file's.

// workspaceSplitCheckID is doctor's line about a workspace's own installation
// whose Firebase is another project's.
const workspaceSplitCheckID = "workspace-firebase"

// doctorWorkspaceSplitCheck is that check: nil for every installation that is
// not a workspace's own, and for a workspace's own project whose Firebase is its
// own, which is every installation this root converges.
//
// A warning and not a failure: the workspace's own service account is not
// created and the workspace does not work until the two projects are one, but
// the installation itself converges and every run it launches keeps running —
// and the fix is the owner's own edit plus a command, which doctor performs
// neither of.
func doctorWorkspaceSplitCheck(lc *localcfg.Config) *doctorCheck {
	if !workspaceDeclared(lc) || workspaceSplitFirebase(lc) == "" {
		return nil
	}
	return &doctorCheck{ID: workspaceSplitCheckID, Severity: "warning",
		Problem: fmt.Sprintf("project %s declares a workspace of its own, and its Firebase is another project's: the config records budget.firebase_project %s, so the workspace's database is %s's while the project the workspace is named after — whose own service account and single grant fugaro update creates — is %s. fugaro update skips the workspace's own project there (no service account is created and no grant is made) and converges everything else, so the workspace does not work until the two are one project (docs/workspaces/spec.md §3)", lc.Name, lc.Budget.FirebaseProject, lc.Budget.FirebaseProject, lc.GCPProject),
		Fix: "`fugaro update --firebase-project " + firebaseProjectIDWord(lc.GCPProject) +
			"` makes the two one project and creates the workspace's service account and its grant." + adoptOwnFirebaseProject(lc)}
}

// The IDs of WS14's checks. Each is one question a person asks of one machine:
// the owner's three are about the thing the owner deploys and publishes, and the
// member's two are about what this project takes from the workspace and how.
const (
	// workspaceServiceCheckID is whether the workspace's own credentials
	// service is deployed in the workspace's project.
	workspaceServiceCheckID = "workspace-service"
	// workspaceNodeCheckID is whether the workspace's credentials node is
	// published and readable where the service reads it per call.
	workspaceNodeCheckID = "workspace-node"
	// workspacePoolCheckID is whether the bundle holds every credential the
	// node declares shared (D1298): an entry the node offers and the bundle
	// does not hold is offered to a member and served to nobody.
	workspacePoolCheckID = "workspace-pool"
	// workspacePinCheckID is the address a member's pin names (R3).
	workspacePinCheckID = "workspace-pin"
	// workspaceRouteCheckID is the credential route this project publishes for
	// each of its repositories, and — as its warning state — a route the
	// bundle, the policy and the pin no longer derive (R4).
	workspaceRouteCheckID = "workspace-route"
	// workspacePersonalCheckID is the member's switch for the personal half of
	// the workspace's credentials (R9), and — as its warning state — a switch
	// that is on where the model route is this project's, which shadows it
	// (D3141, WC11).
	workspacePersonalCheckID = "workspace-personal-token"
)

// doctorWorkspaceChecks is WS14's whole contribution to one doctor run, on
// whichever side of a workspace the machine running it is: the owner's three
// checks, or the member's three. A project in no workspace gets nothing at all,
// which is every installation that has not joined one.
//
// The split installation is deliberately absent here: the workspace's own
// project is skipped by `fugaro update` in that state, so the service is not
// deployed, the node is not in this project's database and the pool is not in
// this project's bundle — three findings that are one fact, and the split check
// above is where that fact is said (workspace-firebase).
func doctorWorkspaceChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	switch {
	case workspaceDeclared(lc) && workspaceSplitFirebase(lc) == "":
		return doctorWorkspaceOwnerChecks(ctx, lc)
	case workspaceMember(lc):
		return doctorWorkspaceMemberChecks(ctx, lc)
	}
	return nil
}

// doctorWorkspaceOwnerChecks is the owner's machine's three lines: the service
// the workspace's own project deploys, the node it publishes, and the pool its
// bundle holds.
func doctorWorkspaceOwnerChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	out := doctorWorkspaceServiceCheck(ctx, lc)
	node, nodeChecks := doctorWorkspaceNodeCheck(ctx, lc)
	out = append(out, nodeChecks...)
	return append(out, doctorWorkspacePoolChecks(ctx, lc, node)...)
}

// doctorWorkspaceServiceCheck is whether the workspace's own credentials service
// is deployed in the workspace's project.
//
// It is read exactly as doctorCredentialsServiceDeployed reads the
// installation's service and for the reason it is read that way: the Cloud Run
// admin API answers a service that is not there with NOT_FOUND, which is the
// only way to see one (no service get is made — the fake behind this check
// serves none, and the real API's get is the same question asked twice), and
// the IAM policy of the service is the resource the module that deploys it
// writes. What is asked here is only whether the resource is there: which job
// accounts may invoke the workspace's service is the workspace's own node's
// business (the member table's accounts), and the acceptance of WS14 stops at
// the deploy.
//
// A warning and not a measurement, for the reason the split check is one: with
// no service, no member's run can fetch a credential of the workspace's pool,
// and the workspace owner is the one person who can put it there. It is not a
// failure: a workspace whose owner has not run their `fugaro update` since
// joining a member is a legitimate state, and --strict is where it becomes one.
func doctorWorkspaceServiceCheck(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	id := workspaceServiceCheckID
	unreadable := func(what string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("project %s declares a workspace of its own, and %s could not be read: whether the workspace's credentials service %s is deployed could not be checked", safetext.Field(lc.Name), what, creds.WorkspaceServiceName),
			Fix:     "rerun doctor with your credentials and the Cloud Run API available, or read it yourself: gcloud run services describe " + creds.WorkspaceServiceName + " --region " + pluginwire.Printable(lc.Region)}}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		return unreadable("the Cloud Run service " + creds.WorkspaceServiceName + ", in a test configuration")
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return unreadable("the Cloud Run API")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", lc.GCPProject, lc.Region, creds.WorkspaceServiceName)
	_, err = svc.Projects.Locations.Services.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	var ge *googleapi.Error
	if err != nil {
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return []doctorCheck{{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("project %s declares a workspace of its own, and Cloud Run has no service %s in project %s, region %s: the workspace's own `fugaro update` deploys it from this project's root, and until it is there no member's run can fetch a credential the workspace offers", safetext.Field(lc.Name), creds.WorkspaceServiceName, pluginwire.Printable(lc.GCPProject), pluginwire.Printable(lc.Region)),
				Fix:     selfCommand() + " update (applies the workspace's own project, which creates the service)"}}
		}
		return unreadable("the IAM policy of the Cloud Run service " + creds.WorkspaceServiceName + " (" + oneLineCLI(err.Error()) + ")")
	}
	return []doctorCheck{{ID: id, OK: true, Severity: "info",
		Problem: fmt.Sprintf("the workspace %s's credentials service %s is deployed in project %s, region %s: it is the second deployment of the credentials service, and it serves the workspace's pool to the member accounts the node below admits (D1293, D2980)", safetext.Field(lc.Name), creds.WorkspaceServiceName, pluginwire.Printable(lc.GCPProject), pluginwire.Printable(lc.Region)),
		Fix:     "nothing to run for this: which credentials it may serve is the node's members and shared list, and which of them a member takes is that member's own published policy"}}
}

// doctorWorkspaceNodeCheck reads the workspace's credentials node — the
// document the workspace's service binds a caller and a pool entry to, per call
// — and answers it beside its line.
//
// It is read from the workspace's OWN database, which is the database the
// workspace's record and this node are published into: on the owner's machine
// that is the installation's own Firebase, and the machine this check runs on
// holds both the read and the write there. A decode this binary refuses is a
// warning and not a read that failed: the node is there and it is one a newer
// Fugaro wrote (or one whose keys this one does not model), and what a member's
// run meets at the service's door is a refusal decided by the version.
func doctorWorkspaceNodeCheck(ctx context.Context, lc *localcfg.Config) (*projectpolicy.WorkspaceCredentials, []doctorCheck) {
	id := workspaceNodeCheckID
	path := projectpolicy.PathWorkspaceCredentials(lc.Name)
	unreadable := func(what string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("project %s declares a workspace of its own, and %s could not be read: whether the workspace's credentials node at %s is published could not be checked", safetext.Field(lc.Name), what, pluginwire.Printable(path)),
			Fix:     "rerun doctor with the Realtime Database reachable, or read it yourself in the Firebase console"}}
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return nil, unreadable("the project's database (" + oneLineCLI(err.Error()) + ")")
	}
	var raw json.RawMessage
	found, err := db.Get(ctx, path, &raw)
	if err != nil {
		return nil, unreadable("the node at " + pluginwire.Printable(path) + " (" + oneLineCLI(err.Error()) + ")")
	}
	if !found {
		return nil, []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("project %s declares a workspace of its own, and no credentials node is published at %s: the workspace's service resolves every caller against that node and serves nothing without it, and %s publishes it from this config's member table and shared list", safetext.Field(lc.Name), pluginwire.Printable(path), selfCommand()+" update"),
			Fix:     selfCommand() + " update (publishes the node from this machine's workspace_members and workspace_shared)"}}
	}
	node, _, err := projectpolicy.DecodeWorkspaceCredentials(raw)
	if err != nil {
		return nil, []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credentials node at %s is not one this Fugaro reads (%s): the workspace's service resolves every caller and every pool entry against this document, so the state it is in decides what is served", pluginwire.Printable(path), oneLineCLI(err.Error())),
			Fix:     selfCommand() + " update (migrates the node in place and refuses rather than dropping a key it does not model)"}}
	}
	shared := 0
	for _, s := range node.Shared {
		if s.Name != "" {
			shared++
		}
	}
	return node, []doctorCheck{{ID: id, OK: true, Severity: "info",
		Problem: fmt.Sprintf("the workspace %s's credentials node is published at %s: version %d, %d member row(s), %d shared credential(s)", safetext.Field(node.Name), pluginwire.Printable(path), node.Version, len(node.Members), shared),
		Fix:     "nothing to run for this: the node is what the workspace's service reads per call, and this config's member table and shared list are what publishes it"}}
}

// doctorWorkspacePoolChecks is whether the workspace's own bundle holds every
// credential the node declares shared (D1298).
//
// **The bundle document is read here, and that read is a person's.** It is the
// one container the workspace's service serves every credential of the pool
// out of, so asking which entries are present means opening it — there is no
// second reading of it (no per-entry metadata, no listing) — and the refusal
// that guards every other reader of it is the one this check makes: a coding
// agent's session and a Fugaro run's own process do not open it, and the line
// they get says so rather than answering. No value is printed, put in an error
// or written anywhere: what the check compares is names, and what its lines
// carry is names.
//
// An entry the node offers and the bundle does not hold is the half-written
// declaration credsvc.WorkspaceShared skips: the service offers nothing for it,
// so a member admitted to it is served a credential that is not there. The
// other direction — a credential in the bundle the node does not declare — is
// not a finding here and deliberately so: it is offered to nobody (D1298), it
// is the state of every credential an owner stored before sharing it, and only
// the owner knows whether it is waiting to be shared or waiting to be removed.
func doctorWorkspacePoolChecks(ctx context.Context, lc *localcfg.Config, node *projectpolicy.WorkspaceCredentials) []doctorCheck {
	id := workspacePoolCheckID
	if node == nil {
		// The node could not be read, so which entries to ask the bundle about
		// is unknown: the node's own line says that, and a second line saying it
		// again would count one state twice.
		return nil
	}
	if len(node.Shared) == 0 {
		return []doctorCheck{{ID: id, OK: true, Severity: "info",
			Problem: fmt.Sprintf("the workspace %s declares no shared credential: its pool is empty, so every member's run is served by its own project's copy", safetext.Field(node.Name)),
			Fix:     "nothing to run for this: share one with `" + selfCommand() + " workspace share NAME --projects <members>`, and store its value with `" + selfCommand() + " creds set NAME`"}}
	}
	unreadable := func(what string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the workspace %s declares %d shared credential(s), and %s could not be read: whether its bundle holds them could not be checked", safetext.Field(node.Name), len(node.Shared), what),
			Fix:     "rerun doctor at your own terminal, with the bundle's accessor available, or read the document's names yourself: " + selfCommand() + " creds ls"}}
	}
	if err := credsOwnerOnly(os.Getenv, "read"); err != nil {
		return unreadable("the workspace's bundle was not read (" + oneLineCLI(err.Error()) + ")")
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable("the bundle (" + oneLineCLI(err.Error()) + ")")
	}
	if err := store.check(ctx); err != nil {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the workspace %s declares %d shared credential(s) and has no bundle container (%s): nothing of the pool has a value to attach, so every entry it declares shared is offered to a member and served to nobody", safetext.Field(node.Name), len(node.Shared), pluginwire.Printable(store.id)),
			Fix:     selfCommand() + " update (the workspace's own installation creates the container), then " + selfCommand() + " creds set NAME"}}
	}
	meta, err := store.newest(ctx)
	switch {
	case errors.Is(err, errNoBundleVersion):
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the workspace %s declares %d shared credential(s) and its bundle (%s) holds no version: every entry the node offers is served to nobody until a value is stored", safetext.Field(node.Name), len(node.Shared), pluginwire.Printable(store.id)),
			Fix:     selfCommand() + " creds set NAME (writes the bundle's first version)"}}
	case err != nil:
		return unreadable("the bundle's newest version (" + oneLineCLI(err.Error()) + ")")
	}
	if meta.state != "ENABLED" {
		fix := selfCommand() + " creds set NAME writes a new version"
		if meta.state == "DISABLED" {
			fix = fmt.Sprintf("gcloud secrets versions enable %s --secret=%s (the document is there; Secret Manager will not serve a disabled version), then %s creds set NAME writes a new one", pluginwire.Printable(meta.number), pluginwire.Printable(store.id), selfCommand())
		}
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the workspace %s declares %d shared credential(s) and the newest version of its bundle (%s, version %s) is %s: Secret Manager will not serve it, so every entry the node offers is served to nobody", safetext.Field(node.Name), len(node.Shared), pluginwire.Printable(store.id), pluginwire.Printable(meta.number), pluginwire.Printable(meta.state)),
			Fix:     fix}}
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unreadable("the bundle's document (" + oneLineCLI(err.Error()) + ")")
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return unreadable("the document is not one the service accepts: " + oneLineCLI(err.Error()))
	}
	// The pool's own reader answers which entries the bundle holds, in the
	// node's order and with the ones it does not hold skipped
	// (credsvc.WorkspaceShared), so the difference is asked of it rather than
	// derived a second time here — a second reading of what a bundle key means
	// is the one thing this check may not add.
	missing := workspacePoolMissing(node, credsvc.WorkspaceShared(b, node))
	if len(missing) > 0 {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the workspace %s's bundle (%s) holds no credential for %s, and the node declares each of them shared: a member the entry is offered to is served nothing from it, and the value is stored under the entry's own name", safetext.Field(node.Name), pluginwire.Printable(store.id), strings.Join(missing, ", ")),
			Fix:     selfCommand() + " creds set NAME for each one named above"}}
	}
	return []doctorCheck{{ID: id, OK: true, Severity: "info",
		Problem: fmt.Sprintf("the workspace %s's bundle (%s) holds every one of the %d credential(s) the node declares shared", safetext.Field(node.Name), pluginwire.Printable(store.id), len(node.Shared)),
		Fix:     "nothing to run for this: an entry the bundle holds and the node does not declare is offered to nobody, so a credential waiting to be shared or to be removed is not a finding here"}}
}

// workspacePoolMissing is the node's shared entries the bundle holds nothing
// for, by the names a person reads.
//
// The answer is asked of the pool's own reader — credsvc.WorkspaceShared, the
// one reader of the pool — and never derived a second time from the document:
// what the difference is a difference of is the entries that reader answered,
// so a change to what a bundle key means moves this check with it. The kind is
// part of the key because a node may declare one name as two entries, and an
// entry whose name and kind the pool offers is held whichever order the node
// lists them in.
func workspacePoolMissing(node *projectpolicy.WorkspaceCredentials, served []creds.Shared) []string {
	held := make(map[string]bool, len(served))
	for _, s := range served {
		held[workspacePoolKey(s.Name, s.Kind)] = true
	}
	var missing []string
	for _, s := range node.Shared {
		if held[workspacePoolKey(s.Name, workspaceNodeKind(s.Kind))] {
			continue
		}
		missing = append(missing, safetext.Field(s.Name)+workspacePoolKind(s))
	}
	return missing
}

// workspacePoolKey is one pool entry's identity for the comparison above: its
// name under the kind the POOL knows it by, which is a forge kind's own name or
// nothing at all for a model credential — the spelling creds.Shared carries and
// the reason the node's kind is normalized first (C3's rule 8).
func workspacePoolKey(name, kind string) string { return kind + "\x00" + name }

// workspaceNodeKind is a node entry's kind as the pool names it: nothing for
// a model entry, whether the node wrote SharedKindModel or nothing at all.
func workspaceNodeKind(kind string) string {
	if kind == projectpolicy.SharedKindModel {
		return ""
	}
	return kind
}

// workspacePoolKind is the kind of a node entry as the line beside the name
// reads it: nothing for a model entry, which is the pool's own kind and the one
// an unqualified name means.
func workspacePoolKind(s projectpolicy.WorkspaceShared) string {
	if s.Kind == "" || s.Kind == projectpolicy.SharedKindModel {
		return ""
	}
	return " (" + safetext.Field(s.Kind) + ")"
}

// doctorWorkspaceMemberChecks is the member's machine's three lines: the address
// its pin names (R3), the credential route each of its repositories publishes
// (R4), and — WC11, R9 — whether the personal half's switch is on and whether
// that route shadows it (D3141).
func doctorWorkspaceMemberChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	rc := routeConfigOf(lc)
	routes, derived, checks := doctorWorkspaceRouteChecks(ctx, lc, rc)
	out := append([]doctorCheck{doctorWorkspacePinCheck(lc, rc)}, checks...)
	return append(out, doctorWorkspacePersonalCheck(ctx, lc, rc, routes, derived)...)
}

// doctorWorkspacePersonalCheck reports the member's switch for the personal half
// of the workspace's credentials (R9, D2972, WC11), and flags the state D3141
// makes a trap: a switch that is on where the model route is this project's, so
// no run of it is ever served a person's own token.
//
// **It calls neither the workspace's service nor its intake** (F24): what it
// reads is this machine's own two documents — the switch out of this project's
// published policy, through the reader the launcher and the service share
// (projectpolicy.PersonSwitchOverDB), and the model route out of the derivation
// the route line above already made. A person's identity is therefore sent
// nowhere, and doctor can be run at a member's terminal without reaching the
// workspace at all.
//
// A switch that cannot be read answers nothing, and the line is unread rather
// than a measurement of off: the node may be half-published, and off is a state
// D2972 gives its own meaning (R9: absent is off, a policy that could not be
// read is an error).
func doctorWorkspacePersonalCheck(ctx context.Context, lc *localcfg.Config, rc routeConfig, routes map[string]creds.Route, derived bool) []doctorCheck {
	id := workspacePersonalCheckID
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: "the switch for the personal half of this project's workspace credentials could not be read: the project's database (" + oneLineCLI(err.Error()) + ") was not reachable, so this project's published policy was not read",
			Fix:     "rerun doctor with the Realtime Database reachable"}}
	}
	on, err := projectpolicy.PersonSwitchOverDB(db).PersonSwitchOn(ctx, lc.Name)
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: "the switch for the personal half of this project's workspace credentials could not be read: this project's published policy at " + projectpolicy.PathShared() + " is not one this Fugaro reads (" + oneLineCLI(err.Error()) + ")",
			Fix:     "republish it with `" + selfCommand() + " project policy publish`, or fix what the reason names, and rerun doctor"}}
	}
	return []doctorCheck{workspacePersonalLine(lc, on, rc, routes, derived)}
}

// workspacePersonalLine is the switch's line itself, from the facts it is about:
// whether the switch is on, the route each of this project's repositories
// derives, and whether that derivation could be made at all. It is a function of
// the facts and not of a read, so the states below are stated without a database
// (doctor_workspace_test.go, TestDoctorFlagsASwitchTheRouteShadows).
func workspacePersonalLine(lc *localcfg.Config, on bool, rc routeConfig, routes map[string]creds.Route, derived bool) doctorCheck {
	id := workspacePersonalCheckID
	project := routeProject(lc)
	// The state every branch below reads from: the switch's own name, and whose
	// it is. The decision numbers this line is derived from stay in this file's
	// comments and the design docs, never in what a person reads.
	state := project + " (workspace " + safetext.Field(lc.Workspace) + "): " + creds.PersonSwitchKey
	if !on {
		return doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: state + " is off: no run of it draws on a person's own token, and every call takes the workspace's shared entries",
			Fix:     "nothing to run for this: turn it on with `" + selfCommand() + " settings set " + creds.PersonSwitchKey + " true`, then run `" + selfCommand() + " update` to publish it"}
	}
	if !derived {
		return doctorCheck{ID: id, Severity: "info", unread: true,
			Problem: state + " is on, and the route this project derives could not be read: whether it serves a personal token was not checked",
			Fix:     "fix what the route line beside this one names, and rerun doctor"}
	}
	if !rc.publishes() {
		return doctorCheck{ID: id, Severity: "warning",
			Problem: state + " is on, and the pin names no workspace service address: the model route is this project's own, so the switch reaches nothing",
			Fix:     "name the address with `" + selfCommand() + " workspace add --service-address <url>` and run `" + selfCommand() + " update`, or turn the switch off"}
	}
	var shadowed []string
	for _, repo := range slices.Sorted(maps.Keys(routes)) {
		if routes[repo].Holder(creds.KindModel) == creds.HolderProject {
			shadowed = append(shadowed, safetext.Field(repo))
		}
	}
	if len(routes) == 0 {
		return doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: state + " is on, and this project records no repository: there is no model route for the switch to serve",
			Fix:     "nothing to run for this"}
	}
	if len(shadowed) == 0 {
		return doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: state + " is on, and every repository's model route is the workspace's: runs here draw on the token its person stored there",
			Fix:     "nothing to run for this: the switch is on and the route serves the workspace"}
	}
	return doctorCheck{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("%s is on, and %d of its repositories take their model credential from this project's own copy (%s), where no personal token is served", state, len(shadowed), strings.Join(shadowed, ", ")),
		Fix:     "remove the account that serves them (`" + selfCommand() + " accounts rm <name>` removes it, its admission and its secret), or turn the switch off: while a repository's model route is this project's, no personal token is served in it"}
}

// doctorWorkspacePinCheck reports the pin's own half of the membership: the
// address of the workspace's service this project takes credentials from.
//
// A pin without an address is a membership that takes no workspace credential —
// R3's own words — and it is a measurement and not a finding: a member whose
// own copy serves every kind is the state every pin written before the address
// existed converges to. What is reported is the address the pin holds and
// nothing of what it trusts besides: the issuer, the assertion key and the
// service account are the exchange's, and doctor names a fact a person can act
// on (the address is the one the workspace's owner publishes; the other three
// are written by `fugaro workspace add`).
func doctorWorkspacePinCheck(lc *localcfg.Config, rc routeConfig) doctorCheck {
	id := workspacePinCheckID
	if !rc.publishes() {
		return doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: fmt.Sprintf("project %s is a member of workspace %s and its pin names no workspace service address: no kind of any of its repositories is served by the workspace, so every credential its runs use is this project's own copy (R3)", safetext.Field(lc.Name), safetext.Field(lc.Workspace)),
			Fix:     "nothing to run for this: name the address with `" + selfCommand() + " workspace add --service-address <url>` (or edit workspace_pin.service_address), then run `" + selfCommand() + " update` to publish the routes it moves"}
	}
	return doctorCheck{ID: id, OK: true, Severity: "info",
		Problem: fmt.Sprintf("project %s is a member of workspace %s and its pin names the workspace's credentials service at %s: a kind the route below moves to the workspace is fetched there (R3, R4)", safetext.Field(lc.Name), safetext.Field(lc.Workspace), pluginwire.Printable(rc.address)),
		Fix:     "nothing to run for this: the address is the workspace's own, and the route below says which kinds of which repositories are fetched there"}
}

// doctorWorkspaceRouteChecks reports the credential route each of this
// project's repositories publishes, and flags the ones the machine's own
// derivation no longer agrees with (R4).
//
// **Both halves are WS9's own derivation, called and never restated.** The
// record this run would publish for a repository is built here by the same
// three calls the publishing stage makes — routeConfigOf for the pin's half,
// routeFactsOf for what this project's bundle holds and what its published
// policy admits, and routePublication.rowFor for the record itself — so a
// doctor line and a `fugaro update` cannot disagree about what the route should
// be. The `initRun` they are handed is doctor's own and writes nowhere: its
// writer is the null device, so the warnings those reads produce for a
// publishing run (the bundle this session may not open) are not printed twice.
//
// A record that differs from this derivation is the state the acceptance names:
// the bundle moved (a credential was stored or removed) or the policy moved, and
// the record still says what was true before. It is a warning because a run of
// that repository is served from the wrong place until the next `fugaro update`
// republishes it — and the derived half says which way: a kind the record sends
// to the workspace that this project now holds itself is money the workspace
// pays for nothing, and a kind the record leaves to the project that the
// workspace now serves is a run that fails on a credential the project no longer
// has.
//
// A repository whose route cannot be derived at all (its entry names no
// provider, or its name is not one a record's key can be made from) is reported
// as not read rather than as a finding, which is routePublication.rowFor's own
// answer and the publishing stage's own rule about the leaves it cannot
// attribute.
//
// It answers the route it derived for each repository beside its lines — the
// personal check reads the model half of them (D3141) — and whether EVERY
// repository's route was derived: a repository whose derivation answered an
// unknown is one the personal check must not report at all, because leaving it
// out of the map and calling the rest complete would state its route as the
// workspace's when nobody derived it, which is the D3141 trap going unseen.
func doctorWorkspaceRouteChecks(ctx context.Context, lc *localcfg.Config, rc routeConfig) (map[string]creds.Route, bool, []doctorCheck) {
	id := workspaceRouteCheckID
	if !rc.publishes() {
		// The pin names no address, so this project publishes no route and has
		// none to report: the pin's own line says which state that is, and the
		// personal check has a branch of its own for it — asked before the map
		// is read, so the empty one here is no claim about any repository.
		return nil, true, nil
	}
	unreadable := func(what, fix string) (map[string]creds.Route, bool, []doctorCheck) {
		return nil, false, []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the pin names the workspace's service at %s, and %s could not be read: the credential route this project publishes for its repositories could not be checked", pluginwire.Printable(rc.address), what),
			Fix:     fix}}
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return unreadable("the project's database ("+oneLineCLI(err.Error())+")", "rerun doctor with the Realtime Database reachable")
	}
	// An initRun whose writer is the null device, for the reason above: the
	// reads below warn the way they warn for a publishing run, and doctor's own
	// lines are the only account of them a reader needs.
	facts, err := routeFactsOf(ctx, &initRun{w: io.Discard}, lc)
	if err != nil {
		return unreadable("what the route is derived from ("+oneLineCLI(err.Error())+")", "fix what the reason names — the bundle's read, the published policy, or the database — and rerun doctor")
	}
	p := &routePublication{lc: lc, db: db, address: rc.address}
	var (
		report  []string
		stale   []string
		unknown []string
		routes  = map[string]creds.Route{}
	)
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		row, unk, err := p.rowFor(repo, lc.Repos[repo], facts)
		if err != nil {
			return unreadable("the route for "+safetext.Field(repo)+" ("+oneLineCLI(err.Error())+")", "fix what the reason names and rerun doctor")
		}
		if unk == nil {
			routes[repo] = row.route
		}
		if unk != nil {
			unknown = append(unknown, safetext.Field(unk.repo)+" ("+unk.why+")")
			continue
		}
		var raw json.RawMessage
		_, found, err := db.GetETag(ctx, row.path, &raw)
		if err != nil {
			return unreadable("the record at "+pluginwire.Printable(row.path)+" ("+oneLineCLI(err.Error())+")", "rerun doctor with the Realtime Database reachable")
		}
		if !found {
			// No record: a run reads the zero Route, which is every kind the
			// project's. A derivation that moves nothing answers the same thing,
			// so the absence is the state the record would have written.
			if workspaceRouteMoves(row.route) {
				stale = append(stale, fmt.Sprintf("%s: no record is published where this project derives a route from the workspace (%s)", safetext.Field(repo), pluginwire.Printable(row.path)))
				continue
			}
			report = append(report, safetext.Field(repo)+": no record is published, and this project serves every kind itself")
			continue
		}
		published, ok, err := creds.DecodeRoute(raw)
		if err != nil || !ok {
			stale = append(stale, fmt.Sprintf("%s: the record at %s is not one this Fugaro reads, so a run of it is refused rather than served", safetext.Field(repo), pluginwire.Printable(row.path)))
			continue
		}
		if published != row.route {
			stale = append(stale, fmt.Sprintf("%s: %s", safetext.Field(repo), workspaceRouteDiff(published, row.route)))
			continue
		}
		report = append(report, safetext.Field(repo)+": "+workspaceRouteLine(published))
	}
	var out []doctorCheck
	if len(report) > 0 {
		out = append(out, doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: fmt.Sprintf("the credential route this project publishes, as the bundle and the published policy derive it: %s", strings.Join(report, "; ")),
			Fix:     "nothing to run for this: the route is published by `" + selfCommand() + " update`, and the kinds it leaves to the project are served by this project's own copy"})
	}
	if len(stale) > 0 {
		out = append(out, doctorCheck{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credential route published for %d repository(ies) no longer matches what this machine derives (the workspace's service is %s): %s. Until the record is republished a run of that repository is served from the place the record names, which is not the place this project's bundle and policy now decide", len(stale), pluginwire.Printable(rc.address), strings.Join(stale, "; ")),
			Fix:     selfCommand() + " update (republishes every route of this project from its bundle and its published policy)"})
	}
	if len(unknown) > 0 {
		out = append(out, doctorCheck{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the route of %d repository(ies) could not be derived, so whether one is published for them was not checked: %s", len(unknown), strings.Join(unknown, "; ")),
			Fix:     "fix what each reason names (a repository's provider, or a name a record's key can be made from) and rerun doctor"})
	}
	if len(out) == 0 {
		out = append(out, doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: "this project records no repository, so it publishes no credential route: a run of nothing is served from nowhere", Fix: "nothing to run for this"})
	}
	return routes, len(unknown) == 0, out
}

// workspaceRouteMoves is whether a derived route serves any kind from the
// workspace: a record that moves nothing is the same answer a run gets from no
// record at all (creds.Route's zero value is every kind the project's).
func workspaceRouteMoves(route creds.Route) bool {
	for _, kind := range creds.Kinds {
		if route.Holder(kind) == creds.HolderWorkspace {
			return true
		}
	}
	return false
}

// workspaceRouteLine is one repository's route as R4's report reads it, in the
// kinds' own order: RouteKindLine, which is the line the publishing run prints
// for the same record.
func workspaceRouteLine(route creds.Route) string {
	parts := make([]string, 0, len(creds.Kinds))
	for _, kind := range creds.Kinds {
		parts = append(parts, RouteKindLine(kind, route.Holder(kind), route.Address))
	}
	return strings.Join(parts, "; ")
}

// workspaceRouteDiff is what a published record says that this machine's
// derivation does not, kind by kind and then the address: it is the half of the
// warning a person acts on, and it names the direction (which of the two says
// the workspace serves the kind) rather than printing two records.
func workspaceRouteDiff(published, derived creds.Route) string {
	var out []string
	for _, kind := range creds.Kinds {
		if published.Holder(kind) != derived.Holder(kind) {
			out = append(out, fmt.Sprintf("%s is %s, and this machine derives %s", kind, published.Holder(kind), derived.Holder(kind)))
		}
	}
	if published.Address != derived.Address {
		out = append(out, fmt.Sprintf("the service address is %s, and this machine's pin names %s", workspaceAddressOrNone(published.Address), workspaceAddressOrNone(derived.Address)))
	}
	return strings.Join(out, ", ")
}

// workspaceAddressOrNone is an address, or the word for a record that carries
// none: a record always carries one (creds.RouteRecord refuses to write one
// without it), and the zero Route a run reads from no record is the one that
// does not — so the word is what a difference against it reads as.
func workspaceAddressOrNone(address string) string {
	if address == "" {
		return "none (no record)"
	}
	return pluginwire.Printable(address)
}
