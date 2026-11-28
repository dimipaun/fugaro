package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// The workspace's half of doctor (WS14, R4): the owner's machine reports the
// workspace's own service, the node it resolves its callers against and the
// pool its bundle holds; a member's reports the address its pin names and the
// credential route each of its repositories publishes. Every fixture here is
// hermetic — the rigs' own fakes, their own config home, no cloud — and the
// member's route is always the one WS9's own publishing stage wrote, so a
// doctor line and a `fugaro update` cannot disagree about it in these tests
// either.

// wsDoctorSharedYAML is the workspace's own shared list as this machine
// declares it (workspace_shared): one model credential and one Bitbucket token,
// which are the two kinds a pool entry can be (F15, U2). The node published
// from it carries the same two entries with the coverage canonicalized
// (wsDoctorNode).
const wsDoctorSharedYAML = `workspace_shared:
  anthropic-api-key:
    kind: model
    projects:
      - member-b
  bitbucket-token:
    kind: bitbucket
    projects:
      - member-b
    covers:
      - owner/repo-b
`

// wsDoctorShared is that declaration as the credentials node carries it: the
// covers of a forge entry are canonical (`<provider>/<owner>/<name>`, R6).
func wsDoctorShared() []projectpolicy.WorkspaceShared {
	return []projectpolicy.WorkspaceShared{
		{Name: config.AnthropicSecret, Kind: projectpolicy.SharedKindModel, Projects: []string{"member-b"}},
		{Name: config.BitbucketSecret, Kind: gitprov.KindBitbucket, Projects: []string{"member-b"},
			Covers: []string{gitprov.KindBitbucket + "/owner/repo-b"}},
	}
}

// wsDoctorJobAccount is the one member row's job service account: an address
// and no credential, which is the shape the node's own Validate admits.
const wsDoctorJobAccount = "job-account-b@member-b-gcp.iam.gserviceaccount.com"

// wsDoctorNode is the credentials node one workspace's owner publishes, with
// the one member wsOwnerTable declares (its Firebase project, its fp, its
// exchange URL and the repositories its job account is a job of) and the pool
// entries the caller names.
func wsDoctorNode(name string, shared ...projectpolicy.WorkspaceShared) *projectpolicy.WorkspaceCredentials {
	return &projectpolicy.WorkspaceCredentials{
		Version: projectpolicy.WorkspaceCredentialsVersion,
		Name:    name,
		Members: []projectpolicy.WorkspaceCredentialMember{{
			Project:         "member-b",
			FP:              "member-b",
			FirebaseProject: "member-b-fb",
			JobAccounts:     []string{wsDoctorJobAccount},
			JobAccountRepos: []projectpolicy.WorkspaceJobAccount{{
				Account: wsDoctorJobAccount,
				Repos:   []string{"github/owner/repo-b"},
			}},
			ExchangeURL: "https://exchange.example.com/v1",
		}},
		Shared: shared,
	}
}

// wsDoctorSeedNode publishes node at the workspace's own node path in the fake
// the rig's config names, written by C3's own encoder: what doctor reads is the
// document a `fugaro update` writes, and a fixture this encoder refuses would
// make the test prove nothing about the read.
func wsDoctorSeedNode(t *testing.T, db *gcpfake.RTDB, name string, node *projectpolicy.WorkspaceCredentials) {
	t.Helper()
	raw, err := projectpolicy.WorkspaceCredentialsNode(node)
	if err != nil {
		t.Fatalf("the node fixture is not one a Fugaro writes, so doctor's read of it would prove nothing: %v", err)
	}
	db.Set(projectpolicy.PathWorkspaceCredentials(name), json.RawMessage(raw))
}

// wsDoctorBundleDoc is the workspace's own bundle document: the model
// credential the pool declares shared, and — when forge is set — the Bitbucket
// token of its second entry. With forge unset the node offers an entry the
// bundle no longer holds, which is the state the pool check exists for.
func wsDoctorBundleDoc(t *testing.T, forge bool) []byte {
	t.Helper()
	doc := config.AnthropicSecret + ": " + credsValue(t, "model") + "\n"
	if forge {
		doc += config.BitbucketSecret + ": " + credsValue(t, "forge") + "\n"
	}
	return []byte(doc)
}

// wsDoctorServiceRig is doctor's rig for a workspace's OWN machine: the
// ordinary policy rig (the installation's database, rules and published policy)
// with the workspace's own declaration appended to its config, plus the Cloud
// Run *services* fake its service is read from — served on the one endpoint the
// config names, beside the jobs fake (cloudRunEndpoint).
//
// The markers a Fugaro run's own process carries are cleared, because a workspace
// owner at their own terminal is the person these checks are for: the pool's
// check opens the bundle, and the refusal that guards every other reader of it
// would answer "not read" for the whole test suite otherwise.
func wsDoctorServiceRig(t *testing.T, shared string) (*doctorRig, *gcpfake.RTDB, *gcpfake.RunServices) {
	t.Helper()
	for _, k := range append(append([]string{}, agentMarkers...), managedFlagRunMarkers...) {
		t.Setenv(k, "")
	}
	r, db := doctorPolicyRig(t, doctorPolicy(nil), wsOwnerTable+wsOwnerCap+shared)
	svcs := gcpfake.NewRunServices(t)
	cfg := strings.Replace(r.cfg, "run: "+r.run.URL+"/", "run: "+cloudRunEndpoint(t, r.run, svcs)+"/", 1)
	if cfg == r.cfg {
		t.Fatal("the rig's config no longer names the Cloud Run fake the way this helper replaces it")
	}
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	return r, db, svcs
}

// wsDoctorRun runs doctor over the rig's installation and answers the parsed
// document beside the bytes it read, so a failure can print the whole run. The
// terraform lookup is stubbed for the reason newDoctorRig stubs it: a check
// about the workspace must not depend on what the machine running the tests has
// installed.
func wsDoctorRun(t *testing.T) (doctorOutput, string) {
	t.Helper()
	doctorFakeTerraform(t, t.TempDir())
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	return o, out
}

// TestDoctorReportsTheWorkspaceService is the owner's half of WS14's acceptance:
// `fugaro doctor` on the workspace owner's machine reports the service deployed,
// the node published and readable, and each shared entry present in the bundle.
// The three are measurements and never findings — an owner reads what their
// workspace is, and `--strict` is where a missing service becomes a failure —
// and the two failing states are here too, because the red step is a doctor that
// said nothing about a workspace with no service.
func TestDoctorReportsTheWorkspaceService(t *testing.T) {
	t.Run("the service, the node and the pool are all there", func(t *testing.T) {
		r, db, svcs := wsDoctorServiceRig(t, wsDoctorSharedYAML)
		svcs.AddService(creds.WorkspaceServiceName)
		wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
		bundleFixtureFor(t, r.sm, "aurora", wsDoctorBundleDoc(t, true))

		o, out := wsDoctorRun(t)
		for _, id := range []string{workspaceServiceCheckID, workspaceNodeCheckID, workspacePoolCheckID} {
			c, ok := doctorCheckByID(o.Checks, id)
			if !ok {
				t.Fatalf("%s is absent from a workspace owner's doctor run, so a reader cannot tell a fact that was read from one that was never asked:\n%s", id, out)
			}
			if !c.OK {
				t.Errorf("%s = %+v, want ok: the workspace's service is deployed, its node is published and its bundle holds every entry the node declares", id, c)
			}
			if c.Severity != "info" {
				t.Errorf("%s carries severity %q, want info: a workspace's own machine reports what it is, and no line about it fails doctor (D2991)", id, c.Severity)
			}
			if !strings.Contains(c.Fix, "nothing to run for this") {
				t.Errorf("%s's fix = %q, want the line that says there is nothing to run", id, c.Fix)
			}
		}

		// What each line is about, in the words a person reads: the workspace's
		// own service, the node's version and counts, and the pool's size.
		if c, _ := doctorCheckByID(o.Checks, workspaceServiceCheckID); !strings.Contains(c.Problem, creds.WorkspaceServiceName) {
			t.Errorf("the service line does not name %s: %q", creds.WorkspaceServiceName, c.Problem)
		}
		node, _ := doctorCheckByID(o.Checks, workspaceNodeCheckID)
		if !strings.Contains(node.Problem, projectpolicy.PathWorkspaceCredentials("aurora")) {
			t.Errorf("the node line does not name the path it read: %q", node.Problem)
		}
		for _, want := range []string{"version 2", "1 member row(s)", "2 shared credential(s)"} {
			if !strings.Contains(node.Problem, want) {
				t.Errorf("the node line does not say %q: %q", want, node.Problem)
			}
		}
		if pool, _ := doctorCheckByID(o.Checks, workspacePoolCheckID); !strings.Contains(pool.Problem, "every one of the 2 credential(s)") {
			t.Errorf("the pool line does not say the bundle holds both entries the node declares: %q", pool.Problem)
		}
	})

	// The red step: a workspace whose owner has not run their own `fugaro update`
	// since the service's root was written. It is a warning and never a failure —
	// an installation mid-opt-in is a legitimate state — and the node and the
	// pool are still read and reported: one missing thing is one line.
	t.Run("no service is deployed", func(t *testing.T) {
		r, db, _ := wsDoctorServiceRig(t, wsDoctorSharedYAML)
		wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
		bundleFixtureFor(t, r.sm, "aurora", wsDoctorBundleDoc(t, true))

		o, out := wsDoctorRun(t)
		c, ok := doctorCheckByID(o.Checks, workspaceServiceCheckID)
		if !ok {
			t.Fatalf("no %s check on a workspace whose service is not deployed:\n%s", workspaceServiceCheckID, out)
		}
		if c.OK || c.Severity != "warning" {
			t.Errorf("%s = %+v, want a warning: with no service no member's run can be served the pool, and the owner is the one person who can put it there", workspaceServiceCheckID, c)
		}
		if !strings.Contains(c.Problem, creds.WorkspaceServiceName) || !strings.Contains(c.Fix, "update") {
			t.Errorf("%s = %+v, want the missing service named beside the command that deploys it", workspaceServiceCheckID, c)
		}
		if _, ok := doctorCheckByID(o.Checks, workspaceNodeCheckID); !ok {
			t.Errorf("the node's own line is absent because the service is not deployed: one fact is one line, and the node is readable without it:\n%s", out)
		}
		if pool, ok := doctorCheckByID(o.Checks, workspacePoolCheckID); !ok || !pool.OK {
			t.Errorf("the pool check = %+v (present=%v), want it read and ok: the bundle is this machine's own and does not depend on the service being deployed", pool, ok)
		}
	})

	// The other half of "each shared entry present in the bundle": an entry the
	// node offers and the bundle does not hold is served to nobody, and the check
	// names it — and only it, since the difference is asked of the pool's own
	// reader rather than re-derived from the document's keys.
	t.Run("the bundle no longer holds a shared entry", func(t *testing.T) {
		r, db, svcs := wsDoctorServiceRig(t, wsDoctorSharedYAML)
		svcs.AddService(creds.WorkspaceServiceName)
		wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
		bundleFixtureFor(t, r.sm, "aurora", wsDoctorBundleDoc(t, false))

		o, out := wsDoctorRun(t)
		c, ok := doctorCheckByID(o.Checks, workspacePoolCheckID)
		if !ok {
			t.Fatalf("no %s check:\n%s", workspacePoolCheckID, out)
		}
		if c.OK || c.Severity != "warning" {
			t.Errorf("%s = %+v, want a warning: the node declares an entry shared and the bundle holds nothing for it", workspacePoolCheckID, c)
		}
		if !strings.Contains(c.Problem, config.BitbucketSecret) {
			t.Errorf("%s = %+v, want the missing entry named", workspacePoolCheckID, c)
		}
		if strings.Contains(c.Problem, config.AnthropicSecret) {
			t.Errorf("%s = %+v, names %s, which the bundle does hold: the entries it does hold are not findings", workspacePoolCheckID, c, config.AnthropicSecret)
		}
	})

	// The bundle's own state, before any entry of it is asked about: the
	// container's absence, the container with nothing in it, and a version
	// Secret Manager will not serve. Each line's fix is the command that runs in
	// the state it reports, and a `creds set` is refused in two of the three
	// (the container that is not there, the DISABLED version).
	t.Run("the bundle's own state", func(t *testing.T) {
		pool := func(t *testing.T) doctorCheck {
			t.Helper()
			o, out := wsDoctorRun(t)
			c, ok := doctorCheckByID(o.Checks, workspacePoolCheckID)
			if !ok {
				t.Fatalf("no %s check:\n%s", workspacePoolCheckID, out)
			}
			if c.OK || c.Severity != "warning" {
				t.Fatalf("%s = %+v, want a warning", workspacePoolCheckID, c)
			}
			return c
		}

		t.Run("the container is not there", func(t *testing.T) {
			_, db, _ := wsDoctorServiceRig(t, wsDoctorSharedYAML)
			wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))

			c := pool(t)
			if !strings.Contains(c.Problem, "has no bundle container") {
				t.Errorf("the line does not name the state it read: %q", c.Problem)
			}
			if update, set := strings.Index(c.Fix, selfCommand()+" update"), strings.Index(c.Fix, selfCommand()+" creds set"); update < 0 || set < 0 || update > set {
				t.Errorf("the fix = %q, want the apply that creates the container first: a `creds set` of this state is refused", c.Fix)
			}
		})

		t.Run("the container holds no version", func(t *testing.T) {
			r, db, _ := wsDoctorServiceRig(t, wsDoctorSharedYAML)
			wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
			bundleFixtureFor(t, r.sm, "aurora", nil)

			c := pool(t)
			if !strings.Contains(c.Problem, "holds no version") {
				t.Errorf("the line does not name the state it read: %q", c.Problem)
			}
			if !strings.Contains(c.Fix, selfCommand()+" creds set NAME") || strings.Contains(c.Fix, "update") {
				t.Errorf("the fix = %q, want the first `creds set` alone", c.Fix)
			}
		})

		t.Run("the newest version is DISABLED", func(t *testing.T) {
			r, db, _ := wsDoctorServiceRig(t, wsDoctorSharedYAML)
			wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
			id := bundleFixtureFor(t, r.sm, "aurora", wsDoctorBundleDoc(t, true))
			r.sm.DisableVersions(id)

			c := pool(t)
			if !strings.Contains(c.Problem, "DISABLED") {
				t.Errorf("the line does not name the state it read: %q", c.Problem)
			}
			if enable, set := strings.Index(c.Fix, "gcloud secrets versions enable"), strings.Index(c.Fix, selfCommand()+" creds set"); enable < 0 || set < 0 || enable > set {
				t.Errorf("the fix = %q, want the enable first: a `creds set` of a disabled bundle is refused", c.Fix)
			}
		})
	})

	// The pool's check is a reader of the workspace's own bundle document, so it
	// is behind the refusal that guards every other reader of one (credsOwnerOnly):
	// a coding agent's session is answered "the bundle was not read" instead of
	// answering, and the document is left unopened. Every other subtest here clears
	// the markers (wsDoctorServiceRig), which is why this one sets one back: without
	// it the refusal could be deleted and the whole suite would still pass.
	t.Run("an agent's session is not served the bundle", func(t *testing.T) {
		r, db, svcs := wsDoctorServiceRig(t, wsDoctorSharedYAML)
		svcs.AddService(creds.WorkspaceServiceName)
		wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
		bundleID := bundleFixtureFor(t, r.sm, "aurora", wsDoctorBundleDoc(t, true))
		t.Setenv("CLAUDECODE", "1")

		o, out := wsDoctorRun(t)
		c, ok := doctorCheckByID(o.Checks, workspacePoolCheckID)
		if !ok {
			t.Fatalf("no %s check in an agent's session, which is the machine an owner never runs doctor on:\n%s", workspacePoolCheckID, out)
		}
		// An unread line is a measurement that answers nothing: not ok, and info so
		// that it fails no run (--json carries no "unread" field, so what it says is
		// in the line's own words).
		if c.OK || c.Severity != "info" {
			t.Errorf("%s = %+v, want the unread info line: whether the bundle holds the node's entries is not answered in an agent's session, and that is not a finding about the workspace", workspacePoolCheckID, c)
		}
		for _, want := range []string{"was not read", "could not be read"} {
			if !strings.Contains(c.Problem, want) {
				t.Errorf("%s does not carry %q, so a reader cannot tell a bundle that was refused from one that was read:\n%s", workspacePoolCheckID, want, c.Problem)
			}
		}
		if slices.Contains(r.sm.Accessed(), bundleID) {
			t.Errorf("an agent's session opened the bundle's document (%s): the refusal is asked before the store is opened, and no value of it is read on that machine", bundleID)
		}
	})
}

// TestDoctorReportsAMembersRoute is the member's half: the pin's address (R3)
// and the credential route this project publishes for each of its repositories
// (R4). The record is the one WS9's own stage writes, so what the check reports
// as "the route" is the route a `fugaro update` on this machine leaves behind.
func TestDoctorReportsAMembersRoute(t *testing.T) {
	t.Run("the pin's address and the published route", func(t *testing.T) {
		r := newRouteRig(t, "")
		routeSeedBundle(t, r.sm, config.ReviewSigningKeySecret("alice")+": "+credsValue(t, "review"))
		routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
			[]string{"bitbucket/acme/other", "github/acme/app"}, nil)
		r.apply(t)

		o, out := wsDoctorRun(t)
		pin, ok := doctorCheckByID(o.Checks, workspacePinCheckID)
		if !ok {
			t.Fatalf("a member's machine reports no %s check:\n%s", workspacePinCheckID, out)
		}
		if !pin.OK || pin.Severity != "info" || !strings.Contains(pin.Problem, routeAddress) {
			t.Errorf("%s = %+v, want the address the pin names (%s) reported as a measurement", workspacePinCheckID, pin, routeAddress)
		}

		routes := checksByID(o, workspaceRouteCheckID)
		if len(routes) != 1 {
			t.Fatalf("%s is %d line(s) on a member whose records match this machine's derivation, want one:\n%s", workspaceRouteCheckID, len(routes), out)
		}
		c := routes[0]
		if !c.OK || c.Severity != "info" {
			t.Errorf("%s = %+v, want one ok line: the published records are the ones this machine derives", workspaceRouteCheckID, c)
		}
		// The line is the publishing run's own (RouteKindLine): the model kind is
		// the workspace's service for both repositories, and acme/app's Bitbucket
		// kind is the project's own copy — the other forge's kind reaches no
		// repository of this installation.
		for _, want := range []string{"acme/app: ", "acme/other: ", "model: the workspace's service at " + routeAddress,
			"bitbucket: this project's own copy"} {
			if !strings.Contains(c.Problem, want) {
				t.Errorf("%s does not carry %q:\n%s", workspaceRouteCheckID, want, c.Problem)
			}
		}

		// The owner's three are the workspace's OWN machine's facts, and a member
		// has none of them: its own project holds no such service, node or bundle.
		for _, id := range []string{workspaceServiceCheckID, workspaceNodeCheckID, workspacePoolCheckID} {
			if _, ok := doctorCheckByID(o.Checks, id); ok {
				t.Errorf("a member's machine reports %s, which is the workspace owner's own fact about their own project", id)
			}
		}
	})

	// A membership whose pin names no address takes no workspace credential
	// (R3), which is a state and not a finding: the pin's line says so, and no
	// route is published or reported at all.
	t.Run("a pin that names no address", func(t *testing.T) {
		newProjectFixture(t, wsPin)
		o, out := wsDoctorRun(t)
		pin, ok := doctorCheckByID(o.Checks, workspacePinCheckID)
		if !ok || !pin.OK || !strings.Contains(pin.Problem, "names no workspace service address") {
			t.Fatalf("%s = %+v (present=%v), want the pin's own state reported:\n%s", workspacePinCheckID, pin, ok, out)
		}
		if c, ok := doctorCheckByID(o.Checks, workspaceRouteCheckID); ok {
			t.Errorf("%s = %+v, want no route line for a pin that names no address", workspaceRouteCheckID, c)
		}
	})
}

// TestDoctorFlagsAStaleRoute is R4's warning state: a record this machine's own
// derivation no longer agrees with. The record here was published while the
// project's own bundle still served its model kind, and the bundle no longer
// holds it — so the record leaves the model kind to the project while this
// machine derives the workspace's service for it, which is the direction where a
// run of that repository is served from a copy this project does not have.
func TestDoctorFlagsAStaleRoute(t *testing.T) {
	r := newRouteRig(t, "")
	routeSeedBundle(t, r.sm, config.ReviewSigningKeySecret("alice")+": "+credsValue(t, "review"))
	routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
		[]string{"bitbucket/acme/other", "github/acme/app"}, nil)
	rec, err := creds.RouteRecord(creds.Route{Version: creds.RouteVersion, Address: routeAddress})
	if err != nil {
		t.Fatalf("the fixture record is not one C2's writer writes: %v", err)
	}
	slug := routeSlug(t, "github", "acme/app")
	r.f.db.Set(creds.RoutePath(slug), json.RawMessage(rec))

	o, out := wsDoctorRun(t)
	cs := checksByID(o, workspaceRouteCheckID)
	if len(cs) == 0 {
		t.Fatalf("no %s check on a member whose published record this machine no longer derives:\n%s", workspaceRouteCheckID, out)
	}
	var stale *doctorCheck
	for i, c := range cs {
		if !c.OK {
			stale = &cs[i]
		}
	}
	if stale == nil {
		t.Fatalf("%s = %+v, want a warning: the record at %s says every kind is the project's, and this machine derives the workspace's service for the model kind", workspaceRouteCheckID, cs, creds.RoutePath(slug))
	}
	if stale.Severity != "warning" {
		t.Errorf("%s carries severity %q, want warning: a run of that repository is served from the place the record names until the next update republishes it", workspaceRouteCheckID, stale.Severity)
	}
	for _, want := range []string{"no longer matches", "acme/app", "model is project", "derives workspace"} {
		if !strings.Contains(stale.Problem, want) {
			t.Errorf("%s does not carry %q:\n%s", workspaceRouteCheckID, want, stale.Problem)
		}
	}
	// The other repository has no record at all, and its derivation moves the
	// model kind: the absence of a record is the same answer as a record that
	// moves nothing, so it is a stale route too — and there the path is the fact
	// a person needs, because there is no record to read.
	other := creds.RoutePath(routeSlug(t, "bitbucket", "acme/other"))
	if !strings.Contains(stale.Problem, other) {
		t.Errorf("%s does not name %s, the path this project publishes acme/other's record at:\n%s", workspaceRouteCheckID, other, stale.Problem)
	}
	if !strings.Contains(stale.Fix, "update") {
		t.Errorf("%s's fix = %q, want the command that republishes every route of this project", workspaceRouteCheckID, stale.Fix)
	}
}

// TestDoctorWritesNothing is WS14's own discipline: these lines read the
// workspace's service, its node and its bundle, and they write nowhere — no
// record is republished, no node is migrated and no credential's value is
// touched. It asserts the reads happened (a test that proved nothing was written
// by reading nothing would pass over an absent implementation) and that the
// bundle is the one document this machine's membership opens.
func TestDoctorWritesNothing(t *testing.T) {
	t.Run("a workspace owner's machine", func(t *testing.T) {
		r, db, svcs := wsDoctorServiceRig(t, wsDoctorSharedYAML)
		svcs.AddService(creds.WorkspaceServiceName)
		wsDoctorSeedNode(t, db, "aurora", wsDoctorNode("aurora", wsDoctorShared()...))
		bundleID := bundleFixtureFor(t, r.sm, "aurora", wsDoctorBundleDoc(t, true))

		wsDoctorRun(t)

		if len(svcs.Requests()) == 0 {
			t.Error("doctor asked the Cloud Run services fake nothing: the service's own state is what this check reports")
		}
		if len(db.Requests()) == 0 {
			t.Error("doctor read nothing from the workspace's database")
		}
		if !slices.Contains(r.sm.Accessed(), bundleID) {
			t.Errorf("doctor did not read the bundle's document (%s): each shared entry's presence can only be answered by opening it", bundleID)
		}
		for _, id := range r.sm.Accessed() {
			if id != bundleID {
				t.Errorf("doctor read the value of %s, and the workspace's own bundle (%s) is the one document this machine's membership opens", id, bundleID)
			}
		}
		for _, req := range db.Requests() {
			if req.Method != http.MethodGet {
				t.Errorf("doctor made a %s request to the workspace's database (%s): WS14's lines read the node and write nothing", req.Method, req.Path)
			}
		}
		for _, req := range r.sm.Requests() {
			if req.Method != http.MethodGet {
				t.Errorf("doctor made a %s request to Secret Manager (%s): no update and no doctor writes a credential", req.Method, req.Path)
			}
		}
		if del := r.sm.Deleted(); len(del) != 0 {
			t.Errorf("doctor deleted %v, and nothing about a workspace's membership removes a container", del)
		}
	})

	t.Run("a member's machine", func(t *testing.T) {
		r := newRouteRig(t, "")
		routeSeedBundle(t, r.sm, config.ReviewSigningKeySecret("alice")+": "+credsValue(t, "review"))
		routeSeedPolicy(t, r.f, map[string][]string{config.AnthropicKeyAccount: {"github/acme/app"}},
			[]string{"github/acme/app"}, nil)
		before := len(r.sm.Requests())

		wsDoctorRun(t)

		if len(r.f.db.Requests()) == 0 {
			t.Error("doctor read nothing from the project's database, so the absence of a write proves nothing")
		}
		for _, req := range r.f.db.Requests() {
			if req.Method != http.MethodGet {
				t.Errorf("doctor made a %s request to the project's database (%s): the route it reports is the one the record already holds, and republishing it is `fugaro update`'s own step", req.Method, req.Path)
			}
		}
		for _, req := range r.sm.Requests()[before:] {
			if req.Method != http.MethodGet {
				t.Errorf("doctor made a %s request to Secret Manager (%s)", req.Method, req.Path)
			}
		}
	})
}
