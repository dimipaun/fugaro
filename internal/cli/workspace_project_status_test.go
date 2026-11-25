package cli

// WU11's tests. Every fake and helper here carries the task's id (wu11…), so the
// level-2 tasks that land in this package later merge without a name clash
// (plan §1).
//
// Two rules of the task's acceptance are held by this file's shape rather than
// by a single test. The first is that the read writes nothing on ANY path: every
// interface the read consumes is faked here with its write methods implemented
// and recording, and wu11New asserts at the end of every test that not one of
// them was called -- so a path that starts writing fails the test that walks it,
// not only the test named for it. The second is that a fact this machine could
// not read is never a pass: the fakes' failing modes are all "the read refused",
// and the tests that use them read the row's warn.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// wu11Now is the fixed clock every row's age is read against, so a row's
// sentence is a value a test can assert rather than one that drifts with the
// wall clock.
var wu11Now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// wu11Writes records every write any fake in this file was asked to make. The
// two commands write nothing anywhere (WU11's acceptance, D2809), and every
// test asserts this is empty when it ends.
type wu11Writes struct {
	calls []string
}

func (w *wu11Writes) note(call string) { w.calls = append(w.calls, call) }

func (w *wu11Writes) check(t *testing.T) {
	t.Helper()
	if len(w.calls) != 0 {
		t.Fatalf("the read wrote, and it writes nothing on any path: %v", w.calls)
	}
}

// wu11FakeStore is C1's Store in memory. Its Write is implemented rather than
// left out, because "the read never writes" is a claim about the caller and the
// only way to test it is to have a write to call.
type wu11FakeStore struct {
	raw    []byte
	reads  int
	writes *wu11Writes
}

func (s *wu11FakeStore) Path() string { return "workspaceRosters/acme" }

func (s *wu11FakeStore) Read(context.Context) (workspace.Revision, []byte, error) {
	s.reads++
	return workspace.Revision{ETag: "rev-1", Found: len(s.raw) > 0}, s.raw, nil
}

func (s *wu11FakeStore) Write(context.Context, workspace.Revision, []byte) error {
	s.writes.note("Store.Write")
	return errors.New("this read does not write")
}

// wu11FakeAuthority is C2's Authority: the two reads a status run makes, and
// every write it must never make.
type wu11FakeAuthority struct {
	holds     bool
	holdsErr  error
	admins    []string
	adminsErr error
	writes    *wu11Writes
}

func (a *wu11FakeAuthority) Kind() workspace.AuthorityKind { return workspace.AuthoritySA }

func (a *wu11FakeAuthority) Link(context.Context, workspace.MemberTarget) error {
	a.writes.note("Authority.Link")
	return nil
}

func (a *wu11FakeAuthority) Unlink(context.Context, workspace.MemberTarget) error {
	a.writes.note("Authority.Unlink")
	return nil
}

func (a *wu11FakeAuthority) AddAdmin(context.Context, string) error {
	a.writes.note("Authority.AddAdmin")
	return nil
}

func (a *wu11FakeAuthority) RemoveAdmin(context.Context, string) error {
	a.writes.note("Authority.RemoveAdmin")
	return nil
}

func (a *wu11FakeAuthority) Holds(context.Context, workspace.MemberTarget) (bool, error) {
	return a.holds, a.holdsErr
}

func (a *wu11FakeAuthority) AdminBindings(context.Context) ([]string, error) {
	return slices.Clone(a.admins), a.adminsErr
}

// wu11FakeOps is C2's MemberOps: PendingApply and Bindings answer, and every
// write the interface carries records itself as the failure it would be.
type wu11FakeOps struct {
	pending     workspace.Pending
	pendingErr  error
	bindings    []workspace.MemberBinding
	bindingsErr error
	// want is what the row asked PendingApply to compare, which is the one
	// argument a test can be wrong about.
	want workspace.ApplyWant
	// pendingCalls counts the asks, so a test can hold the once-per-run rule.
	pendingCalls int
	writes       *wu11Writes
}

func (o *wu11FakeOps) ReadGrants(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{}, nil
}

func (o *wu11FakeOps) WriteGrants(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	o.writes.note("MemberOps.WriteGrants")
	return nil
}

func (o *wu11FakeOps) ReadMirror(context.Context, workspace.MemberTarget) (workspace.MemberDoc, error) {
	return workspace.MemberDoc{}, nil
}

func (o *wu11FakeOps) WriteMirror(context.Context, workspace.MemberTarget, workspace.MemberDoc, []byte) error {
	o.writes.note("MemberOps.WriteMirror")
	return nil
}

func (o *wu11FakeOps) Bindings(context.Context, workspace.MemberTarget) ([]workspace.MemberBinding, error) {
	return slices.Clone(o.bindings), o.bindingsErr
}

func (o *wu11FakeOps) PendingApply(_ context.Context, _ workspace.MemberTarget, want workspace.ApplyWant) (workspace.Pending, error) {
	o.pendingCalls++
	o.want = want
	return o.pending, o.pendingErr
}

func (o *wu11FakeOps) Offboard(context.Context, workspace.MemberTarget, string) error {
	o.writes.note("MemberOps.Offboard")
	return nil
}

func (o *wu11FakeOps) Audit(context.Context, workspace.MemberTarget, string, []byte) error {
	o.writes.note("MemberOps.Audit")
	return nil
}

// wu11FakeWriter is C2's StateWriter: LastSaved answers and Save records.
type wu11FakeWriter struct {
	at     time.Time
	found  bool
	err    error
	writes *wu11Writes
}

func (w *wu11FakeWriter) Save(context.Context, workspace.Snapshot) (workspace.SnapshotWritten, error) {
	w.writes.note("StateWriter.Save")
	return workspace.SnapshotWritten{}, nil
}

func (w *wu11FakeWriter) LastSaved(context.Context, workspace.Snapshot) (time.Time, bool, error) {
	return w.at, w.found, w.err
}

// wu11FakeProbe is C2's CredentialProbe: the answers, and the count of times it
// was asked -- a probe read with the owner's own credentials must be asked
// exactly once per member and never in an agent's session.
type wu11FakeProbe struct {
	results []workspace.CredentialResult
	err     error
	calls   int
}

func (p *wu11FakeProbe) Probe(context.Context, workspace.MemberTarget) ([]workspace.CredentialResult, error) {
	p.calls++
	return slices.Clone(p.results), p.err
}

// wu11FakeCloud is the member-facing read surface this file declares: what a
// workspace admin's own credentials can see. It has no write anywhere, which is
// the point of the surface (the implementation's own type is read-only by
// construction), and every failing mode is a refused read a row must report as
// "not measured" rather than as a pass.
type wu11FakeCloud struct {
	reachErr error

	creds    memberCredentials
	credsErr error

	broker    brokerState
	brokerErr error

	images    []imageJob
	imagesErr error

	caps    memberCaps
	capsErr error
	// capNames is the member each caps read was asked about: the figure is the
	// workspace's own meter, so the read carries the member whose entry of it the
	// row compares, once per member and never the same name for both.
	capNames []string

	accounts   map[string]accountAccess
	accountErr error

	reached []string
}

func (c *wu11FakeCloud) reads() workspaceMemberReads {
	return workspaceMemberReads{
		reachable: func(_ context.Context, m workspace.MemberTarget) error {
			c.reached = append(c.reached, m.Project)
			return c.reachErr
		},
		credentials: func(context.Context, workspace.MemberTarget, *localcfg.Config) (memberCredentials, error) {
			return c.creds, c.credsErr
		},
		broker: func(context.Context, *localcfg.Config) (brokerState, error) {
			return c.broker, c.brokerErr
		},
		images: func(context.Context, *localcfg.Config) ([]imageJob, error) {
			return slices.Clone(c.images), c.imagesErr
		},
		caps: func(_ context.Context, name string) (memberCaps, error) {
			c.capNames = append(c.capNames, name)
			return c.caps, c.capsErr
		},
		account: func(_ context.Context, account string) (accountAccess, error) {
			if c.accountErr != nil {
				return accountAccess{}, c.accountErr
			}
			return c.accounts[account], nil
		},
	}
}

// wu11Account is the workspace IAM account the default harness's members name in
// their live bindings: the one read that places it (D2806, F17).
const wu11Account = "ws-iam@acme-ws.iam.gserviceaccount.com"

// wu11Harness is one run of the read: a status built with every side faked, and
// the fakes themselves so a test can set an answer and read what was asked.
type wu11Harness struct {
	t      *testing.T
	s      *WorkspaceProjectStatus
	writes *wu11Writes
	store  *wu11FakeStore
	auth   *wu11FakeAuthority
	ops    *wu11FakeOps
	writer *wu11FakeWriter
	probe  *wu11FakeProbe
	cloud  *wu11FakeCloud

	drift       workspace.DriftReport
	driftErr    error
	driftCalls  int
	roster      *workspace.Roster
	memberCfg   *localcfg.Config
	sharedErr   error
	sharedCalls int
	getenv      func(string) string
	terminal    bool
}

// wu11New builds the harness every test starts from: a workspace of two members,
// a roster with two admins and one stored grant, and every read answering so
// that every row passes. A test that is about one row changes that one answer.
func wu11New(t *testing.T) *wu11Harness {
	t.Helper()
	h := &wu11Harness{t: t, writes: &wu11Writes{}}
	// Every fix sentence names the command a person runs, so the test's own
	// binary path must not be in them: the whole package's tests do this.
	oldSelf := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = oldSelf })
	h.roster = wu11Roster(t, []string{"one@example.com", "two@example.com"},
		[]workspace.StoredGrant{{Project: "alpha", User: "three@example.com", Role: workspace.RoleAdmin}})
	raw, err := workspace.EncodeRoster(h.roster)
	if err != nil {
		t.Fatal(err)
	}
	h.store = &wu11FakeStore{raw: raw, writes: h.writes}
	h.auth = &wu11FakeAuthority{holds: true, admins: wu11Members(h.roster.Admins), writes: h.writes}
	h.ops = &wu11FakeOps{
		bindings: []workspace.MemberBinding{{
			Project:   "alpha-gcp",
			Principal: "serviceAccount:" + wu11Account,
			Role:      "roles/owner",
			Kind:      workspace.PrincipalWorkspaceIAM,
		}},
		writes: h.writes,
	}
	h.writer = &wu11FakeWriter{at: wu11Now.Add(-2 * time.Hour), found: true, writes: h.writes}
	h.probe = &wu11FakeProbe{results: []workspace.CredentialResult{{
		Name: "anthropic", Kind: workspace.CredentialModelKey, OK: true, Status: "200",
	}}}
	h.cloud = &wu11FakeCloud{
		creds: memberCredentials{
			Stored:    []memberCredential{{Name: "anthropic", Stored: true, Created: wu11Now.Add(-3 * 24 * time.Hour)}},
			Newest:    wu11Now.Add(-3 * 24 * time.Hour),
			ReadMarks: true,
		},
		broker:   brokerState{Serving: true, Detail: " revision acme-00042"},
		images:   []imageJob{{Name: "fugaro-preload-alpha-gcp", LastSuccess: wu11Now.Add(-4 * time.Hour)}},
		caps:     memberCaps{Set: true, Monthly: 100_000_000, Spent: 10_000_000, SpentRead: true},
		accounts: map[string]accountAccess{wu11Account: {Keys: []accountKey{{ID: "key-1", Created: wu11Now.Add(-30 * 24 * time.Hour)}}, ActAs: wu11Members(h.roster.Admins)}},
	}
	h.memberCfg = &localcfg.Config{}
	h.getenv = func(string) string { return "" }
	h.terminal = true

	// The member's own published config is another installation's document, read
	// through the seam every other reader uses: a test must not reach a bucket,
	// so the read answers here.
	oldFetch := sharedFetch
	sharedFetch = func(context.Context, func(string) string, time.Time, string, string) (*localcfg.Config, string, error) {
		h.sharedCalls++
		if h.sharedErr != nil {
			return nil, "", h.sharedErr
		}
		return h.memberCfg, "rev-1", nil
	}
	t.Cleanup(func() { sharedFetch = oldFetch })

	lc := &localcfg.Config{
		Name:       "acme",
		GCPProject: "acme-ws",
		WorkspaceCap: &localcfg.WorkspaceCap{
			Monthly: 100,
		},
		WorkspaceMembers: &map[string]localcfg.WorkspaceMember{
			"alpha": {
				FP:              "alpha",
				FirebaseProject: "alpha-gcp",
				JobAccounts:     []localcfg.WorkspaceJobAccount{{Account: "job@alpha-gcp.iam.gserviceaccount.com", Repos: []string{"acme/one"}}},
				Repos:           []string{"acme/one"},
			},
			"beta": {
				FP:              "beta",
				FirebaseProject: "beta-gcp",
				JobAccounts:     []localcfg.WorkspaceJobAccount{{Account: "job@beta-gcp.iam.gserviceaccount.com", Repos: []string{"acme/two"}}},
				Repos:           []string{"acme/two"},
			},
		},
	}
	h.s = &WorkspaceProjectStatus{
		lc:       lc,
		getenv:   func(k string) string { return h.getenv(k) },
		terminal: h.terminal,
		now:      func() time.Time { return wu11Now },
		cloud:    h.cloud.reads(),
		sides: workspaceProjectSides{
			Store:     func(context.Context, *localcfg.Config, string) (workspace.Store, error) { return h.store, nil },
			Authority: func(context.Context, *localcfg.Config) (workspace.Authority, error) { return h.auth, nil },
			Member:    func(context.Context, *localcfg.Config) (workspace.MemberOps, error) { return h.ops, nil },
			Writer:    func(context.Context, *localcfg.Config) (workspace.StateWriter, error) { return h.writer, nil },
			Drift: func(context.Context, *localcfg.Config) (workspace.Drift, error) {
				return func(context.Context, workspace.MemberTarget) (workspace.DriftReport, error) {
					h.driftCalls++
					return h.drift, h.driftErr
				}, nil
			},
			Probe: func(context.Context, *localcfg.Config) (workspace.CredentialProbe, error) { return h.probe, nil },
		},
	}
	// Every test ends by asserting the read never wrote, on the path it walked.
	t.Cleanup(func() { h.writes.check(t) })
	return h
}

// wu11Members is a list of addresses in the spelling the live side answers in:
// IAM names a person "user:" and the address, while the record's `Admins` holds
// the bare address, and the fakes answer the way the API does so the two
// spellings are what the rows are held to.
func wu11Members(addrs []string) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, "user:"+a)
	}
	return out
}

// wu11Roster builds a roster the encoder accepts: the admins and the granted
// people as this roster's people, in the order every list of it is written in.
func wu11Roster(t *testing.T, admins []string, grants []workspace.StoredGrant) *workspace.Roster {
	t.Helper()
	set := map[string]bool{}
	for _, a := range admins {
		set[a] = true
	}
	for _, g := range grants {
		set[g.User] = true
	}
	addrs := make([]string, 0, len(set))
	for a := range set {
		addrs = append(addrs, a)
	}
	slices.Sort(addrs)
	people := make([]workspace.Person, 0, len(addrs))
	for _, a := range addrs {
		people = append(people, workspace.Person{Address: a, Kind: workspace.KindHuman})
	}
	sorted := slices.Clone(admins)
	slices.Sort(sorted)
	r := &workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People:    people,
		Admins:    sorted,
		Grants:    slices.Clone(grants),
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("the test's own roster is not one the encoder writes: %v", err)
	}
	return r
}

// doc is one run of the read, with the flags the test set.
func (h *wu11Harness) doc(named string) wsProjectsDoc {
	h.t.Helper()
	d, err := h.s.Doc(context.Background(), named)
	if err != nil {
		h.t.Fatalf("Doc(%q): %v", named, err)
	}
	return d
}

// out prints the document the way the command does, and answers what a person
// read and the exit status.
func (h *wu11Harness) out(d wsProjectsDoc, asJSON bool) (string, error) {
	h.t.Helper()
	var buf bytes.Buffer
	err := h.s.Print(&buf, d, asJSON)
	return buf.String(), err
}

// screen is the two commands' whole run: the document, printed.
func (h *wu11Harness) screen(named string, asJSON bool) (string, error) {
	h.t.Helper()
	return h.out(h.doc(named), asJSON)
}

// rows is one member's rows as a map, for the tests that assert one row.
func wu11Rows(t *testing.T, d wsProjectsDoc, member string) map[string]wsProjectRow {
	t.Helper()
	for _, m := range d.Members {
		if m.Project == member {
			out := map[string]wsProjectRow{}
			for _, r := range m.Rows {
				out[r.ID] = r
			}
			return out
		}
	}
	t.Fatalf("the document has no member %q: %+v", member, d.Members)
	return nil
}

// wu11Row is one row of the document by id, and a failure when it is not there.
func wu11Row(t *testing.T, d wsProjectsDoc, member, id string) wsProjectRow {
	t.Helper()
	r, ok := wu11Rows(t, d, member)[id]
	if !ok {
		t.Fatalf("%s has no %s row: %+v", member, id, d)
	}
	return r
}

// wu11WorkspaceRow is one of the workspace's own rows by id.
func wu11WorkspaceRow(t *testing.T, d wsProjectsDoc, id string) wsProjectRow {
	t.Helper()
	for _, r := range d.Rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("the document has no workspace row %s: %+v", id, d.Rows)
	return wsProjectRow{}
}

// wu11IDs is a list of row ids, for the order assertions.
func wu11IDs(rows []wsProjectRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// memberRowIDs is the acceptance's order for one member's rows, the probe
// excluded: it is the one row a flag decides.
func memberRowIDs() []string {
	return []string{
		rowReachable, rowLinked, rowCredentialsSvc, rowCredentials,
		rowImages, rowCaps, rowPending, rowStateRepo, rowDrift,
	}
}

// TestStatusRowsInOrder pins the order the acceptance names, and that a member
// which passes every row says so: the probe's row is present only under
// --check-credentials and sits beside the credentials it asked about.
func TestStatusRowsInOrder(t *testing.T) {
	h := wu11New(t)
	d := h.doc("")
	if got := wu11IDs(d.Members[0].Rows); !slices.Equal(got, memberRowIDs()) {
		t.Fatalf("alpha's rows are %v, want %v", got, memberRowIDs())
	}
	if got := wu11IDs(d.Rows); !slices.Equal(got, []string{rowAdminBinding, rowSingleAdmin, rowAccountKeys, rowAccountActAs}) {
		t.Fatalf("the workspace's rows are %v", got)
	}
	if d.Members[0].Worst != wsPass {
		t.Fatalf("alpha's worst row is %s, want pass: %+v", d.Members[0].Worst, d.Members[0].Rows)
	}
	if _, err := h.screen("", false); err != nil {
		t.Fatalf("a screen whose rows all pass exits nonzero: %v", err)
	}

	h.s.check = true
	withProbe := h.doc("")
	want := []string{rowReachable, rowLinked, rowCredentialsSvc, rowCredentials, rowCredentialsProbe, rowImages, rowCaps, rowPending, rowStateRepo, rowDrift}
	if got := wu11IDs(withProbe.Members[0].Rows); !slices.Equal(got, want) {
		t.Fatalf("with --check-credentials alpha's rows are %v, want %v", got, want)
	}
	if !withProbe.CheckedCredentials {
		t.Fatal("the document does not say the credentials were checked")
	}
	if h.probe.calls != 2 {
		t.Fatalf("the probe was asked %d times for two members, want once each", h.probe.calls)
	}
}

// TestStatusCapsReadTheWorkspaceMeter is the caps row's own fact, which the
// acceptance names as "caps set and use against them": the figure compared with
// the cap is this member's entry of the workspace's METER -- the quantity the
// workspace's cap governs (D1301), which the workspace's own credentials service
// keeps -- asked for under the member's name and never read out of the member's
// own ledger, which is a different quantity under a different cap. Every state
// the row can be in is pinned here, the fail included: a member whose whole cap
// is spent has every call of it refused until the window turns.
func TestStatusCapsReadTheWorkspaceMeter(t *testing.T) {
	h := wu11New(t)
	r := wu11Row(t, h.doc(""), "alpha", rowCaps)
	if r.Status != wsPass {
		t.Fatalf("a member inside its cap is %s: %q", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, wsUSD(100_000_000)) || !strings.Contains(r.Detail, wsUSD(10_000_000)) {
		t.Fatalf("the row does not state the cap and the use against it: %q", r.Detail)
	}
	if !slices.Equal(h.cloud.capNames, []string{"alpha", "beta"}) {
		t.Fatalf("the caps were read for %v, want each member's own figure of the workspace's meter", h.cloud.capNames)
	}

	// A member with its whole cap spent is a fail -- every further call of it is
	// refused -- and the fix is the one command that raises the cap.
	h = wu11New(t)
	h.cloud.caps = memberCaps{Set: true, Monthly: 100_000_000, Spent: 100_000_000, SpentRead: true}
	r = wu11Row(t, h.doc(""), "alpha", rowCaps)
	if r.Status != wsFail {
		t.Fatalf("a member at its cap is %s, want fail: %q", r.Status, r.Detail)
	}
	if !strings.Contains(r.Fix, " workspace cap set --workspace acme --monthly ") {
		t.Fatalf("the fail does not name the command that raises the cap: %q", r.Fix)
	}
	if _, err := h.screen("", false); err == nil {
		t.Fatal("a member over its cap did not make the command exit nonzero")
	}

	// A month the meter did not answer for is a warn that says so: the figure
	// nobody produced is not a spend of zero.
	h = wu11New(t)
	h.cloud.caps = memberCaps{Set: true, Monthly: 100_000_000, Why: "the workspace's own meter did not answer"}
	r = wu11Row(t, h.doc(""), "alpha", rowCaps)
	if r.Status != wsWarn {
		t.Fatalf("an unread meter is %s, want warn: %q", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "was not measured") || !strings.Contains(r.Detail, h.cloud.caps.Why) {
		t.Fatalf("the row does not say the use was not measured and why: %q", r.Detail)
	}
	if strings.Contains(r.Detail, wsUSD(0)) {
		t.Fatalf("an unread meter was reported as a spend: %q", r.Detail)
	}

	// No cap at all: nothing bounds what the workspace's credentials pay for.
	h = wu11New(t)
	h.cloud.caps = memberCaps{Set: false, SpentRead: true}
	r = wu11Row(t, h.doc(""), "alpha", rowCaps)
	if r.Status != wsWarn || !strings.Contains(r.Fix, " workspace cap set --workspace acme --monthly ") {
		t.Fatalf("no cap is %s with fix %q", r.Status, r.Fix)
	}
}

// TestStatusCapsMeterIsReadFromTheWorkspaceDatabase drives the read the caps
// row's figure comes out of, against the RTDB fake: the use against the cap is
// this member's entry of the workspace's own meter for the month, read at the
// workspace's own path, with the room its calls in flight hold counted in and
// another member's spend left out. The three answers a month can give are all
// pinned here: a node holding figures, no node at all, and a read the database
// refused.
func TestStatusCapsMeterIsReadFromTheWorkspaceDatabase(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	db, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "test-id-token" }})
	if err != nil {
		t.Fatal(err)
	}
	reads := NewWorkspaceMemberCloud(
		&localcfg.Config{Name: "acme", WorkspaceCap: &localcfg.WorkspaceCap{Monthly: 100}}, db)

	now := time.Now()
	month := budget.MonthKey(now)
	node := budget.PathWorkspaceSpend("acme", month)
	f.Set(node, map[string]any{
		"month":        month,
		"members":      map[string]any{"alpha": 30_000_000, "beta": 1_000_000},
		"reservations": map[string]any{"call-1": map[string]any{"member": "alpha", "amount": 5_000_000, "at": now.Add(-time.Minute).UnixMilli()}},
	})

	got, err := reads.caps(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("reading the workspace's meter: %v", err)
	}
	if !got.Set || got.Monthly != 100_000_000 {
		t.Fatalf("the workspace's own cap is %+v, want $100.00 set", got)
	}
	if !got.SpentRead {
		t.Fatalf("a month with a node is not read: %+v", got)
	}
	if got.Spent != 35_000_000 {
		t.Fatalf("alpha's use against the cap is %d micros, want its settled 30.00 plus its 5.00 in flight", got.Spent)
	}
	if reqs := f.Requests(); !slices.ContainsFunc(reqs, func(r gcpfake.Request) bool {
		return strings.HasSuffix(r.Path, "/"+node+".json")
	}) {
		t.Fatalf("the meter was not read at the workspace's own path %s: %v", node, reqs)
	}

	// A month with no node is not a spend of zero: nothing has measured it, and
	// the row's warn says so rather than passing a figure nobody produced.
	f.Set(node, nil)
	got, err = reads.caps(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("reading a month with no node: %v", err)
	}
	if got.SpentRead || got.Spent != 0 {
		t.Fatalf("a month with no node was read as a spend: %+v", got)
	}
	if got.Why == "" || !strings.Contains(got.Why, month) {
		t.Fatalf("the unread month is not explained: %+v", got)
	}

	// A refused read is a refusal, and the row reports it as one: a meter nobody
	// could read is not a meter reading zero.
	f.DenyRead(node)
	if _, err := reads.caps(context.Background(), "alpha"); err == nil {
		t.Fatal("a refused read of the meter answered a figure")
	}
}

// TestStatusWritesNothing walks every path of both commands -- every row,
// --json, --check-credentials, a named member, the refusal and the usage
// refusal -- and the harness's own cleanup asserts that not one write was made.
// It is the task's red step: a read that went anywhere but through these fakes
// would write, and this test would be the one that failed.
func TestStatusWritesNothing(t *testing.T) {
	h := wu11New(t)
	// Every read refuses, so the rows a passing run never builds are built here:
	// the failing path of a row is a path like any other.
	h.cloud.reachErr = errors.New("alpha-gcp did not answer")
	h.cloud.credsErr = errors.New("the member's secrets were not listed")
	h.cloud.brokerErr = errors.New("the member's service was not read")
	h.cloud.imagesErr = errors.New("the member's jobs were not read")
	h.cloud.capsErr = errors.New("the cap was not read")
	h.cloud.accountErr = errors.New("the IAM API refused")
	h.ops.pendingErr = errors.New("the member's roster was not read")
	h.ops.bindingsErr = errors.New("the member's policy was not read")
	h.writer.err = errors.New("the state store was not read")
	h.driftErr = errors.New("the live IAM was not read")
	h.auth.holdsErr = errors.New("the mechanism was not read")
	h.auth.adminsErr = errors.New("the live bindings were not read")
	h.sharedErr = errors.New("the member's config was not read")
	h.s.check = true

	for _, tc := range []struct {
		name    string
		named   string
		asJSON  bool
		summary bool
	}{
		{name: "status"},
		{name: "status --json", asJSON: true},
		{name: "ls", summary: true},
		{name: "ls --json", summary: true, asJSON: true},
		{name: "status alpha", named: "alpha"},
		{name: "status alpha --json", named: "alpha", asJSON: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.s.summary = tc.summary
			out, err := h.screen(tc.named, tc.asJSON)
			if out == "" {
				t.Fatal("the command printed nothing")
			}
			if !tc.asJSON {
				var exit *ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("a screen whose rows fail exits zero: %v", err)
				}
			}
		})
	}

	// The usage refusal and the flag's own refusal never reach a read at all.
	h.s.summary = false
	if _, err := (&WorkspaceProjectStatus{lc: &localcfg.Config{Name: "acme"}, getenv: h.s.getenv, now: h.s.now}).Doc(context.Background(), ""); err == nil {
		t.Fatal("a config with no member table is not refused")
	}
	h.getenv = func(k string) string {
		if k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}
	if _, err := h.s.Doc(context.Background(), ""); err == nil {
		t.Fatal("--check-credentials in an agent's session is not refused")
	}
}

// TestStatusNotLinked is D2819's row: a member the mechanism no longer holds is
// a fail that says "not linked" and names the command that links it, in the
// member owner's own hands.
func TestStatusNotLinked(t *testing.T) {
	h := wu11New(t)
	h.auth.holds = false
	d := h.doc("")
	r := wu11Row(t, d, "alpha", rowLinked)
	if r.Status != wsFail {
		t.Fatalf("the linked row is %s, want fail", r.Status)
	}
	if !strings.Contains(r.Detail, "not linked") {
		t.Fatalf("the row does not say \"not linked\": %q", r.Detail)
	}
	if !strings.Contains(r.Fix, selfCommand()+" workspace project link alpha") {
		t.Fatalf("the row does not name the command that links the member: %q", r.Fix)
	}
	if _, err := h.screen("", false); err == nil {
		t.Fatal("a failing row did not make the command exit nonzero")
	}

	// A mechanism that could not be read is not an unlink: the row warns, and
	// the unlinked-grants row stays away because nothing measured it.
	h = wu11New(t)
	h.auth.holdsErr = errors.New("the mechanism was not read")
	d = h.doc("")
	if r := wu11Row(t, d, "alpha", rowLinked); r.Status != wsWarn {
		t.Fatalf("an unread mechanism is %s, want warn", r.Status)
	}
	for _, r := range d.Members[0].Rows {
		if r.ID == rowGrantsLeft {
			t.Fatalf("an unread mechanism produced the unlinked-grants row: %+v", r)
		}
	}
}

// TestStatusCheckCredentialsOnlyInOwnTerminal is D2820's fence: the probe reads
// credential values with the owner's own credentials, so an agent's session is
// refused before a single read, and a person's own terminal gets the provider's
// own answer as a row.
func TestStatusCheckCredentialsOnlyInOwnTerminal(t *testing.T) {
	h := wu11New(t)
	h.s.check = true
	h.getenv = func(k string) string {
		if k == "CLAUDE_CODE_ENTRYPOINT" {
			return "cli"
		}
		return ""
	}
	if _, err := h.s.Doc(context.Background(), ""); err == nil {
		t.Fatal("--check-credentials in a coding agent's session was not refused")
	} else if !strings.Contains(err.Error(), checkCredentialsFlag) || !strings.Contains(err.Error(), "coding agent's session") {
		t.Fatalf("the refusal does not name the flag and the session: %v", err)
	}
	if h.probe.calls != 0 {
		t.Fatalf("the probe ran %d time(s) before the refusal", h.probe.calls)
	}

	// Without the flag the same session reads: nothing of a credential's value
	// is read by a run that does not ask for it.
	h.getenv = func(string) string { return "" }
	h.s.check = false
	h.getenv = func(k string) string {
		if k == "CLAUDE_CODE_ENTRYPOINT" {
			return "cli"
		}
		return ""
	}
	d := h.doc("")
	if h.probe.calls != 0 {
		t.Fatalf("the probe ran %d time(s) without --check-credentials", h.probe.calls)
	}
	for _, r := range d.Members[0].Rows {
		if r.ID == rowCredentialsProbe {
			t.Fatalf("a run without the flag carries the probe's row: %+v", r)
		}
	}

	// In the owner's own terminal the probe's answer is the row's sentence.
	h = wu11New(t)
	h.s.check = true
	h.probe.results = []workspace.CredentialResult{
		{Name: "anthropic", Kind: workspace.CredentialModelKey, OK: true, Status: "200"},
		{Name: "claude-team", Kind: workspace.CredentialClaudeAccount, OK: false, Status: "401"},
	}
	r := wu11Row(t, h.doc(""), "alpha", rowCredentialsProbe)
	if r.Status != wsWarn {
		t.Fatalf("a provider that refused is %s, want warn", r.Status)
	}
	if !strings.Contains(r.Detail, "refused claude-team") || !strings.Contains(r.Detail, "401") {
		t.Fatalf("the row does not carry the provider's own answer: %q", r.Detail)
	}
}

// TestStatusSingleAdminWarns is R1: a workspace with one admin works and is one
// accident away from being unadministerable, which is a warn and not a fail.
func TestStatusSingleAdminWarns(t *testing.T) {
	h := wu11New(t)
	h.roster = wu11Roster(t, []string{"one@example.com"}, nil)
	raw, err := workspace.EncodeRoster(h.roster)
	if err != nil {
		t.Fatal(err)
	}
	h.store.raw = raw
	h.auth.admins = wu11Members(h.roster.Admins)

	d := h.doc("")
	r := wu11WorkspaceRow(t, d, rowSingleAdmin)
	if r.Status != wsWarn {
		t.Fatalf("one admin is %s, want warn", r.Status)
	}
	if !strings.Contains(r.Detail, "one@example.com") {
		t.Fatalf("the row does not name the single admin: %q", r.Detail)
	}
	if !strings.Contains(r.Fix, selfCommand()+" workspace user add <address> --admin") {
		t.Fatalf("the row does not name the command that adds one, which R1 fixes as `fugaro workspace user add <email> --admin`: %q", r.Fix)
	}
	if _, err := h.screen("", false); err != nil {
		t.Fatalf("a warn made the command exit nonzero: %v", err)
	}
}

// TestStatusFlagsAdminTampering is D2823 in both directions: an entry of the
// record with no live binding, and a live binding whose holder the record does
// not name. The record is a claim about IAM, so either direction is a fail.
func TestStatusFlagsAdminTampering(t *testing.T) {
	h := wu11New(t)
	h.auth.admins = wu11Members([]string{"two@example.com", "stranger@example.com"})
	r := wu11WorkspaceRow(t, h.doc(""), rowAdminBinding)
	if r.Status != wsFail {
		t.Fatalf("tampering is %s, want fail", r.Status)
	}
	if !strings.Contains(r.Detail, "one@example.com") {
		t.Fatalf("the row does not name the entry with no binding: %q", r.Detail)
	}
	if !strings.Contains(r.Detail, "stranger@example.com") {
		t.Fatalf("the row does not name the binding the record does not list: %q", r.Detail)
	}
	if _, err := h.screen("", false); err == nil {
		t.Fatal("admin tampering did not make the command exit nonzero")
	}

	// The two lists agreeing is a pass, and the record's own size is what it
	// says was compared.
	h = wu11New(t)
	r = wu11WorkspaceRow(t, h.doc(""), rowAdminBinding)
	if r.Status != wsPass {
		t.Fatalf("agreeing lists are %s: %q", r.Status, r.Detail)
	}
}

// TestStatusUnlinkedGrantsLeft is D2822: a member owner unlinked, and the
// workspace's stored grants are still written in the record until the workspace
// project's owner removes the member from it.
func TestStatusUnlinkedGrantsLeft(t *testing.T) {
	h := wu11New(t)
	h.auth.holds = false
	d := h.doc("")
	r := wu11Row(t, d, "alpha", rowGrantsLeft)
	if r.Status != wsWarn {
		t.Fatalf("grants left after an unlink are %s, want warn", r.Status)
	}
	if !strings.Contains(r.Detail, "unlinked, grants left") {
		t.Fatalf("the row does not say what the acceptance names: %q", r.Detail)
	}
	if !strings.Contains(r.Detail, "three@example.com") {
		t.Fatalf("the row does not name the grant left behind: %q", r.Detail)
	}
	if !strings.Contains(r.Fix, selfCommand()+" workspace project rm alpha") {
		t.Fatalf("the row does not name the command that removes it: %q", r.Fix)
	}

	// A member the mechanism still holds has no such row, and neither has one
	// the record stores no grant for: the row is the two facts together.
	h = wu11New(t)
	for _, r := range h.doc("").Members[0].Rows {
		if r.ID == rowGrantsLeft {
			t.Fatalf("a linked member carries the unlinked-grants row: %+v", r)
		}
	}
	h = wu11New(t)
	h.auth.holds = false
	h.roster = wu11Roster(t, []string{"one@example.com", "two@example.com"}, nil)
	raw, err := workspace.EncodeRoster(h.roster)
	if err != nil {
		t.Fatal(err)
	}
	h.store.raw = raw
	for _, r := range h.doc("").Members[0].Rows {
		if r.ID == rowGrantsLeft {
			t.Fatalf("a member with no stored grant carries the row: %+v", r)
		}
	}
}

// TestStatusNamesAccountKeysAndActAsHolders is the acceptance's last two rows:
// the workspace IAM account's user-managed keys, each with its id and age, and
// every principal that can act as it. Both say in one line that a key outlives
// `workspace user rm --admin` and name the organization policy Fugaro cannot set, so
// neither row is read as "the binding is still there".
func TestStatusNamesAccountKeysAndActAsHolders(t *testing.T) {
	h := wu11New(t)
	h.cloud.accounts[wu11Account] = accountAccess{
		Keys: []accountKey{
			{ID: "key-1", Created: wu11Now.Add(-30 * 24 * time.Hour)},
			{ID: "key-2", Created: wu11Now.Add(-90 * 24 * time.Hour)},
		},
		ActAs: wu11Members([]string{"one@example.com", "two@example.com", "stranger@example.com"}),
	}
	d := h.doc("")

	keys := wu11WorkspaceRow(t, d, rowAccountKeys)
	if keys.Status != wsWarn {
		t.Fatalf("a key on the account is %s, want warn", keys.Status)
	}
	if !strings.Contains(keys.Detail, "key-1") || !strings.Contains(keys.Detail, "key-2") {
		t.Fatalf("the row does not name the keys by id: %q", keys.Detail)
	}
	if !strings.Contains(keys.Detail, "30d old") || !strings.Contains(keys.Detail, "90d old") {
		t.Fatalf("the row does not carry each key's age: %q", keys.Detail)
	}

	actAs := wu11WorkspaceRow(t, d, rowAccountActAs)
	if actAs.Status != wsWarn {
		t.Fatalf("a principal the record does not name can act as the account and the row is %s", actAs.Status)
	}
	if !strings.Contains(actAs.Detail, "stranger@example.com") {
		t.Fatalf("the row does not name the stranger: %q", actAs.Detail)
	}
	for _, admin := range h.roster.Admins {
		if strings.Contains(actAs.Detail, admin) {
			t.Fatalf("the row counts the record's own admin %s as a stranger: %q", admin, actAs.Detail)
		}
	}

	for _, r := range []wsProjectRow{keys, actAs} {
		whole := r.Detail + " " + r.Fix
		if !strings.Contains(whole, "outlives") || !strings.Contains(whole, selfCommand()+" workspace user rm") {
			t.Fatalf("the %s row does not say a key outlives the binding's removal: %q", r.ID, whole)
		}
		if !strings.Contains(whole, "constraints/iam.disableServiceAccountKeyCreation") {
			t.Fatalf("the %s row does not name the control Fugaro cannot set: %q", r.ID, whole)
		}
	}

	// No key and only the record's own admins holding actAs: both rows pass,
	// and they still carry the sentence about what a key is.
	h = wu11New(t)
	h.cloud.accounts[wu11Account] = accountAccess{ActAs: wu11Members(h.roster.Admins)}
	d = h.doc("")
	for _, id := range []string{rowAccountKeys, rowAccountActAs} {
		r := wu11WorkspaceRow(t, d, id)
		if r.Status != wsPass {
			t.Fatalf("%s is %s: %q", id, r.Status, r.Detail)
		}
		if !strings.Contains(r.Detail, "disableServiceAccountKeyCreation") {
			t.Fatalf("%s does not carry the sentence about a key: %q", id, r.Detail)
		}
	}

	// A workspace held by the folder has no account of its own to report, and
	// says so rather than warning about one it could not place.
	h = wu11New(t)
	h.roster = &workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismFolder,
		FolderID:  "folders/123456789012",
		People:    h.roster.People,
		Admins:    h.roster.Admins,
		Grants:    h.roster.Grants,
	}
	raw, err := workspace.EncodeRoster(h.roster)
	if err != nil {
		t.Fatal(err)
	}
	h.store.raw = raw
	h.cloud.accountErr = errors.New("the folder workspace's account must not be read")
	d = h.doc("")
	for _, id := range []string{rowAccountKeys, rowAccountActAs} {
		if r := wu11WorkspaceRow(t, d, id); r.Status != wsPass {
			t.Fatalf("a folder workspace's %s is %s: %q", id, r.Status, r.Detail)
		}
	}
}

// TestStatusEscapesMemberStrings holds the rule that everything a member or a
// cloud API controls is escaped before it reaches a terminal or the document:
// a project name, a credential name, a key id and an admin's address.
func TestStatusEscapesMemberStrings(t *testing.T) {
	const esc = "\x1b[31m"
	h := wu11New(t)
	h.roster = wu11Roster(t, []string{"one@example.com"}, nil)
	raw, err := workspace.EncodeRoster(h.roster)
	if err != nil {
		t.Fatal(err)
	}
	h.store.raw = raw
	h.auth.admins = wu11Members([]string{"one@example.com"})
	table := map[string]localcfg.WorkspaceMember{
		"alpha" + esc: {
			FP:              "alpha",
			FirebaseProject: "alpha-gcp",
			JobAccounts:     []localcfg.WorkspaceJobAccount{{Account: "job@alpha-gcp.iam.gserviceaccount.com"}},
		},
	}
	h.s.lc.WorkspaceMembers = &table
	h.cloud.creds.Stored = []memberCredential{{Name: "anthropic" + esc, Stored: true, Created: wu11Now}}
	h.cloud.creds.Missing = []string{"beta" + esc}
	h.cloud.accounts[wu11Account] = accountAccess{
		Keys:  []accountKey{{ID: "key" + esc, Created: wu11Now}},
		ActAs: []string{"user:stranger" + esc + "@example.com"},
	}

	d := h.doc("")
	out, _ := h.out(d, false)
	if strings.Contains(out, esc) {
		t.Fatalf("the screen carries a member-controlled escape sequence:\n%s", out)
	}
	if !strings.Contains(out, "\\u001b") {
		t.Fatalf("the screen does not carry the escaped form:\n%s", out)
	}
	js, _ := h.out(d, true)
	if strings.Contains(js, esc) {
		t.Fatalf("the document carries a raw escape sequence:\n%s", js)
	}
	if !strings.Contains(js, "\\\\u001b") {
		t.Fatalf("the document does not carry the escaped form:\n%s", js)
	}
}

// TestProjectLsJSONSchema holds `workspace project ls --json` to
// schemas/workspace-projects.schema.json, which is what an outside reader --
// the console, the mobile app, the history -- reads instead of this build's
// own type.
func TestProjectLsJSONSchema(t *testing.T) {
	h := wu11New(t)
	h.s.check = true
	h.s.summary = true
	// A member with a failing row, a warn and a pass all at once, so the
	// document the schema is held to is not one shape only.
	h.cloud.creds.Missing = []string{"claude-team"}
	h.auth.admins = wu11Members([]string{"two@example.com"})
	h.ops.pending = workspace.Pending{Admins: []string{"three@example.com"}, NotMeasured: []string{workspace.NotMeasuredViewers}}
	out, err := h.screen("", true)
	var exit *ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("a document whose rows fail exits for another reason: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-projects.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/workspace-projects.schema.json has no $id")
	}
	// The schema's own const is the same number: the two are one contract.
	var plain map[string]any
	if err := json.Unmarshal(raw, &plain); err != nil {
		t.Fatal(err)
	}
	props, _ := plain["properties"].(map[string]any)
	ver, _ := props["version"].(map[string]any)
	if got := ver["const"]; got != float64(wsProjectDocVersion) {
		t.Fatalf("the schema's version const is %v, and wsProjectDocVersion is %d: the document and its schema have to state one version", got, wsProjectDocVersion)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/workspace-projects.schema.json: %v\n%s", err, out)
	}

	// `ls` is the same read, summarised: its --json document IS this one (the
	// flag only changes what a person reads), and its screen is one line per
	// member with the member's worst row.
	h.s.summary = false
	status, err := h.screen("", true)
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("a document whose rows fail exits for another reason: %v", err)
	}
	if out != status {
		t.Fatalf("ls --json and status --json wrote different documents:\n%s\n%s", out, status)
	}

	h.s.summary = true
	h.s.check = false
	printed, _ := h.screen("", false)
	lines := strings.Split(strings.TrimSuffix(printed, "\n"), "\n")
	if len(lines) != len(h.doc("").Members) {
		t.Fatalf("ls printed %d line(s) for %d members:\n%s", len(lines), len(h.doc("").Members), printed)
	}
	for _, line := range lines {
		if !strings.Contains(line, ": ") {
			t.Fatalf("an ls line does not name a member and its worst row: %q", line)
		}
	}
	h.s.summary = false
	whole, _ := h.screen("", false)
	if len(strings.Split(strings.TrimSuffix(whole, "\n"), "\n")) <= len(lines) {
		t.Fatalf("status printed no more than ls did:\n%s", whole)
	}
}

// TestStatusCredentialsFollowDoctorNaming is the concrete credentials read
// against the rule doctor's own claude-accounts-stored check follows: a named
// account the member's config declares as a KEY credential has no token
// container and never will, so it is stored under credential-<name>-key and
// must not be reported as a missing token; an account whose container is really
// absent is reported, with the member's own machine named as where it is stored.
func TestStatusCredentialsFollowDoctorNaming(t *testing.T) {
	list := []gcp.SecretInfo{
		{ID: config.CredentialSecret("team"), Labels: map[string]string{gcp.LabelSecret: config.CredentialSecret("team")}, Latest: "3", Created: wu11Now.Add(-2 * time.Hour)},
		{ID: config.AnthropicSecret, Labels: map[string]string{gcp.LabelSecret: config.AnthropicSecret}, Latest: "1", Created: wu11Now.Add(-time.Hour)},
	}
	member := &localcfg.Config{
		Repos:       map[string]localcfg.Repo{"acme/one": {}},
		Credentials: []config.Credential{{Name: "team", Type: config.CredentialTypeAnthropic}},
		ClaudeAccounts: map[string]config.ClaudeAccount{
			"team": {AllowDataTo: []string{"acme/one"}},
			"gone": {AllowDataTo: []string{"acme/one"}},
		},
	}
	c := &workspaceMemberCloud{
		lc:      &localcfg.Config{},
		now:     func() time.Time { return wu11Now },
		lists:   map[string][]gcp.SecretInfo{},
		secrets: func(context.Context, gcp.Options) ([]gcp.SecretInfo, error) { return list, nil },
		marks:   func(context.Context) (map[string]creds.Mark, error) { return map[string]creds.Mark{}, nil },
		serving: workspaceMemberServing,
		jobs:    workspaceMemberJobs,
		keys:    func(context.Context, string) (accountAccess, error) { return accountAccess{}, nil },
	}
	got, err := c.credentials(context.Background(), workspace.MemberTarget{Project: "alpha-gcp"}, member)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Missing, []string{"gone"}) {
		t.Fatalf("the missing accounts are %v, want the one whose container is absent: a key credential is not a missing token", got.Missing)
	}
	if len(got.Stored) != 1 || got.Stored[0].Name != config.AnthropicSecret {
		t.Fatalf("the stored model credentials are %+v, want the project's model key alone: a key credential's container is not one of them", got.Stored)
	}
	if !got.ReadMarks {
		t.Fatal("the marks were read and the row still says they were not")
	}

	// A member's marks that could not be read is "not measured", never "none is
	// broken": the row warns with the reason instead of passing.
	c.marks = func(context.Context) (map[string]creds.Mark, error) {
		return nil, errors.New("the credential state was not read")
	}
	got, err = c.credentials(context.Background(), workspace.MemberTarget{Project: "alpha-gcp"}, member)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadMarks {
		t.Fatal("a marks read that refused is reported as read")
	}
	if got.MarksWhy == "" {
		t.Fatal("the reason the marks were not read is not carried")
	}
}
