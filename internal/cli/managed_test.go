package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The property this feature's safety rests on, and the one place it is
// asserted once and exhaustively rather than per task: **a project that is
// not managed never proceeds without its confirmation, and an applying step
// in a coding agent's session in a project that is not managed never proceeds
// at all.** D55's answer of 2026-10-16 removes the refusal for a managed
// project and for nothing else, so this is the direction that must not move:
// the change that would be catastrophic is not a managed project applying the
// wrong thing, it is an ordinary project quietly stopping asking.
//
// Every expectation below is an ORACLE WRITTEN BY HAND, never derived from
// the code under test:
//
//   - which marker states are managed is each world's own `managed` field
//     (managedPropertyWorlds), and the read is asserted to return exactly it;
//   - what a non-managed project's confirmation does under each combination of
//     flags and terminal is a literal table (confirmTable);
//   - which environment variables are a coding agent's is a literal list
//     (expectedAgentMarkers), not agentMarkers;
//   - every flag of `fugaro init` has a written entry (initFlagCases).
//
// The decision is computed over the program's real paths: the managed read
// (managed.IsManaged over internal/gcpfake), combined with initRun.ask (the
// ordinary confirmation every apply goes through) and initRun.askTyped (the
// money-or-permanent one, through initRun.conditions and canConfirmTyped),
// with every agent marker really set, unset and empty in the environment.
// The rule itself (initflow.CanConfirm / CanConfirmRun) is checked against
// the same table, and every other inline agent-marker decision of init is
// checked to refuse under each marker.
//
// What it does NOT cover: the managed direction (Task 3.1: a managed project
// may apply only what the gate admitted), D58's flagging, the deployer
// identity (D75, Check 68), the model's review, and the commands outside init
// that read the marker (image, recipes, config publish, upgrade).

// expectedAgentMarkers is the oracle for which environment variables mean a
// coding agent's session: written here by hand, so a marker dropped from
// agentMarkers is a failure rather than a smaller test
// (docs/design/managed-projects.md §3 finding 2 names these six).
var expectedAgentMarkers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"}

// agentEnvState is one state of the environment a run can be in: no marker
// set, one marker set to a value, or one marker present with an empty value.
// An empty value is not a coding agent's session (agentMarker reads getenv's
// value, and an agent that can set a variable empty can unset it), and that
// is pinned here so a change to it is a decision rather than a drift.
type agentEnvState struct {
	name, key, value string
	agent            string // the marker the run must report, "" for none: written by hand
}

func agentEnvStates() []agentEnvState {
	out := []agentEnvState{{name: "no marker"}}
	for _, k := range expectedAgentMarkers {
		out = append(out, agentEnvState{name: k + "=1", key: k, value: "1", agent: k})
		out = append(out, agentEnvState{name: k + "= (empty)", key: k, value: "", agent: ""})
	}
	return out
}

// clearAgentEnv unsets every marker the oracle names (restored at the end of
// the test): TestMain unsets agentMarkers only, which is the list under test.
func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, k := range expectedAgentMarkers {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// apply puts the run's environment in the state.
func (s agentEnvState) apply(t *testing.T) {
	t.Helper()
	clearAgentEnv(t)
	if s.key != "" {
		t.Setenv(s.key, s.value)
	}
}

// confirmRow is one combination of the flags and the terminal that
// initRun.conditions reads, with what a project that is NOT managed and has
// NO agent marker must do, written by hand:
//
//   - condYes: initflow.Conditions.Yes as initRun.conditions must build it
//     (--plan-only never takes --yes);
//   - ordRight/ordWrong: whether initRun.ask (the YesOrTyped class) proceeds
//     when the project's name is typed / when something else is typed;
//   - typRight/typWrong: the same for initRun.askTyped (the Typed class).
//
// Under any agent marker every one of the four is false.
type confirmRow struct {
	yes, planOnly, nonInteractive, json, terminal bool
	condYes                                       bool
	ordRight, ordWrong, typRight, typWrong        bool
}

// confirmTable is every combination of --yes, --plan-only, --non-interactive,
// --json and a terminal (2^5 = 32 rows), each written out.
var confirmTable = []confirmRow{
	//  yes    plan   nonInt json   term     condYes   ordR   ordW   typR   typW
	{false, false, false, false, false /**/, false /**/, false, false, false, false},
	{false, false, false, false, true /* */, false /**/, true, false, true, false},
	{false, false, false, true, false /* */, false /**/, false, false, false, false},
	{false, false, false, true, true /*  */, false /**/, true, false, false, false},
	{false, false, true, false, false /* */, false /**/, false, false, false, false},
	{false, false, true, false, true /*  */, false /**/, false, false, false, false},
	{false, false, true, true, false /*  */, false /**/, false, false, false, false},
	{false, false, true, true, true /*   */, false /**/, false, false, false, false},
	{false, true, false, false, false /* */, false /**/, false, false, false, false},
	{false, true, false, false, true /*  */, false /**/, true, false, true, false},
	{false, true, false, true, false /*  */, false /**/, false, false, false, false},
	{false, true, false, true, true /*   */, false /**/, true, false, false, false},
	{false, true, true, false, false /*  */, false /**/, false, false, false, false},
	{false, true, true, false, true /*   */, false /**/, false, false, false, false},
	{false, true, true, true, false /*   */, false /**/, false, false, false, false},
	{false, true, true, true, true /*    */, false /**/, false, false, false, false},
	{true, false, false, false, false /* */, true /* */, true, true, false, false},
	{true, false, false, false, true /*  */, true /* */, true, true, false, false},
	{true, false, false, true, false /*  */, true /* */, true, true, false, false},
	{true, false, false, true, true /*   */, true /* */, true, true, false, false},
	{true, false, true, false, false /*  */, true /* */, true, true, false, false},
	{true, false, true, false, true /*   */, true /* */, true, true, false, false},
	{true, false, true, true, false /*   */, true /* */, true, true, false, false},
	{true, false, true, true, true /*    */, true /* */, true, true, false, false},
	{true, true, false, false, false /*  */, false /**/, false, false, false, false},
	{true, true, false, false, true /*   */, false /**/, true, false, true, false},
	{true, true, false, true, false /*   */, false /**/, false, false, false, false},
	{true, true, false, true, true /*    */, false /**/, true, false, false, false},
	{true, true, true, false, false /*   */, false /**/, false, false, false, false},
	{true, true, true, false, true /*    */, false /**/, false, false, false, false},
	{true, true, true, true, false /*    */, false /**/, false, false, false, false},
	{true, true, true, true, true /*     */, false /**/, false, false, false, false},
}

func (c confirmRow) String() string {
	return fmt.Sprintf("--yes=%v --plan-only=%v --non-interactive=%v --json=%v terminal=%v", c.yes, c.planOnly, c.nonInteractive, c.json, c.terminal)
}

// confirmClasses is every confirmation class the product runs over, by the
// name internal/initflow/stage.go declares it under.
var confirmClasses = []struct {
	name  string
	class initflow.ConfirmClass
}{
	{"YesOrTyped", initflow.YesOrTyped},
	{"Typed", initflow.Typed},
}

// declaredConfirmClasses reads the ConfirmClass constants out of
// internal/initflow/stage.go, in declaration order: Go cannot enumerate a
// constant set at run time, and a class with no case here is a class whose
// confirmations the property stops covering.
func declaredConfirmClasses(t *testing.T) []string {
	t.Helper()
	const src = "../initflow/stage.go"
	f, err := parser.ParseFile(token.NewFileSet(), src, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}
	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// A const spec with neither a type nor a value repeats the one before
		// it (`Typed` after `YesOrTyped ConfirmClass = iota`), so the block is
		// tracked rather than each spec.
		var class bool
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				id, ok := vs.Type.(*ast.Ident)
				class = ok && id.Name == "ConfirmClass"
			}
			if class {
				for _, n := range vs.Names {
					names = append(names, n.Name)
				}
			}
		}
	}
	return names
}

// managedRead is one answer the marker read gave, with the verdict the world
// it was read from must have — written by hand, never taken from the read.
type managedRead struct {
	state   string
	managed bool // the hand-written verdict
	ok      bool
	reason  string
	err     error
}

// The installation the reads below are asked about. The Fugaro project's name
// is deliberately neither the GCP project's nor a substring of the bucket's.
const (
	managedPropertyProject = "sandbox"
	managedPropertyGCP     = "fugaro-managed-property"
	managedPropertyNumber  = 424242
)

func managedPropertyBucket() string { return "fugaro-runs-" + managedPropertyGCP }

// managedPropertyWorld is one state the installation can be in, as the read's
// two clients see it, and whether that state is managed: by hand, and only
// for the creation fact itself (the marker object present and well-formed,
// in the runs bucket of the right project number, carrying the label).
type managedPropertyWorld struct {
	name    string
	managed bool
	build   func(t *testing.T) (*storage.Service, *crm.Service)
}

// managedWorldOpts is a world's shape: everything is the managed one unless
// a field says otherwise.
type managedWorldOpts struct {
	noBucket     bool
	bucketNumber uint64
	noLabel      bool
	object       []byte // nil: the right marker; use noObject for none
	noObject     bool
	refuseObject int // a status for the marker object's read, 0 for none
	refuseGCS    int // a status for every storage call, 0 for none
	refuseCRM    int // a status for every Resource Manager call, 0 for none
}

func managedWorld(o managedWorldOpts) func(t *testing.T) (*storage.Service, *crm.Service) {
	return func(t *testing.T) (*storage.Service, *crm.Service) {
		bucket := managedPropertyBucket()
		f := gcpfake.NewGCS(t)
		if !o.noBucket {
			labels := map[string]string{"fugaro": "managed"}
			if o.noLabel {
				labels = map[string]string{"other": "label"}
			}
			number := uint64(managedPropertyNumber)
			if o.bucketNumber != 0 {
				number = o.bucketNumber
			}
			f.AddBucket(bucket, number, labels)
			if !o.noObject {
				obj := o.object
				if obj == nil {
					obj = []byte(`{"version":1,"gcp_project":"` + managedPropertyGCP + `"}`)
				}
				f.Put(bucket, managed.MarkerObject, obj)
			}
		}
		if o.refuseObject != 0 {
			f.RefusePath(managed.MarkerObject, o.refuseObject, http.StatusText(o.refuseObject), "", "refused")
		}
		if o.refuseGCS != 0 {
			f.Refuse(o.refuseGCS, "INTERNAL", "", "backend error")
		}
		cf := gcpfake.NewCRM(t)
		cf.AddProject(managedPropertyGCP, managedPropertyNumber)
		if o.refuseCRM != 0 {
			cf.Refuse(o.refuseCRM, "INTERNAL", "", "backend error")
		}
		return managedStorage(t, f), managedCRM(t, cf)
	}
}

// managedPropertyWorlds is every state the read has an answer for. Exactly
// one is managed.
func managedPropertyWorlds() []managedPropertyWorld {
	return []managedPropertyWorld{
		{"the creation fact is there (marker, right project number, label)", true, managedWorld(managedWorldOpts{})},
		{"the fugaro=managed label alone, no creation fact", false, managedWorld(managedWorldOpts{noObject: true})},
		{"a creation fact that cannot be read (403)", false, managedWorld(managedWorldOpts{refuseObject: http.StatusForbidden})},
		{"a creation fact whose read fails (500)", false, managedWorld(managedWorldOpts{refuseObject: http.StatusInternalServerError})},
		{"a malformed creation fact", false, managedWorld(managedWorldOpts{object: []byte(`{"version":1,"gcp_project":`)})},
		{"a creation fact with a field this build does not write", false, managedWorld(managedWorldOpts{object: []byte(`{"version":1,"gcp_project":"` + managedPropertyGCP + `","managed":true}`)})},
		{"a creation fact of an unknown version", false, managedWorld(managedWorldOpts{object: []byte(`{"version":2,"gcp_project":"` + managedPropertyGCP + `"}`)})},
		{"a creation fact about another project", false, managedWorld(managedWorldOpts{object: []byte(`{"version":1,"gcp_project":"someone-elses-project"}`)})},
		{"a runs bucket in another project (wrong project number)", false, managedWorld(managedWorldOpts{bucketNumber: managedPropertyNumber + 1})},
		{"a creation fact in a bucket without the label", false, managedWorld(managedWorldOpts{noLabel: true})},
		{"no runs bucket at all", false, managedWorld(managedWorldOpts{noBucket: true})},
		{"a 500 reading the runs bucket", false, managedWorld(managedWorldOpts{refuseGCS: http.StatusInternalServerError})},
		{"a 500 reading the project number", false, managedWorld(managedWorldOpts{refuseCRM: http.StatusInternalServerError})},
		{"the project number cannot be read (403)", false, managedWorld(managedWorldOpts{refuseCRM: http.StatusForbidden})},
	}
}

// managedReads is what managed.IsManaged answers in each world, beside the
// world's hand-written verdict, plus one answer the read never gives (managed
// together with an error), which is not managed either.
func managedReads(t *testing.T) []managedRead {
	t.Helper()
	var out []managedRead
	for _, w := range managedPropertyWorlds() {
		s, c := w.build(t)
		ok, reason, err := managed.IsManaged(context.Background(), s, c, managedPropertyProject, managedPropertyGCP)
		out = append(out, managedRead{state: w.name, managed: w.managed, ok: ok, reason: reason, err: err})
	}
	out = append(out, managedRead{
		state: "a read that answered managed and then failed", managed: false, ok: true, err: errors.New("the read failed"),
	})
	return out
}

func managedStorage(t *testing.T, f *gcpfake.GCS) *storage.Service {
	t.Helper()
	s, err := storage.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/storage/v1/"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func managedCRM(t *testing.T, f *gcpfake.CRM) *crm.Service {
	t.Helper()
	s, err := crm.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// managedMode is the rule the apply path must use for the three results of
// managed.IsManaged: a confirmation may be replaced only by a fact, so only
// by a read that answered true and did not fail. The reason is never a
// signal, and an error is never a reason to proceed (Global Constraint 3).
// Task 3.1 wires it into the apply path.
func managedMode(ok bool, reason string, err error) bool { return ok && err == nil }

// The read returns exactly the hand-written verdict of every world: managed
// only for the creation fact itself, and for every other world — a failed or
// 5xx read, a missing, unreadable or malformed marker, the label alone, the
// wrong project number, another project's marker — ok is false (IsManaged's
// contract: managed is true only with a nil error), whatever the error.
func TestManagedReadReturnsTheHandWrittenVerdict(t *testing.T) {
	var managedN, notManaged int
	for _, r := range managedReads(t) {
		if r.managed {
			managedN++
		} else {
			notManaged++
		}
		if r.state == "a read that answered managed and then failed" {
			if managedMode(r.ok, r.reason, r.err) {
				t.Errorf("managedMode takes %q as managed", r.state)
			}
			continue
		}
		if r.ok != r.managed || (r.managed && r.err != nil) {
			t.Errorf("%s: IsManaged answered ok=%v err=%v (%s), and this world is managed=%v", r.state, r.ok, r.err, r.reason, r.managed)
		}
		if got := managedMode(r.ok, r.reason, r.err); got != r.managed {
			t.Errorf("%s: managedMode says %v, and this world is managed=%v", r.state, got, r.managed)
		}
	}
	if managedN != 1 || notManaged < 2 {
		t.Fatalf("the worlds hold %d managed and %d not managed: the oracle is one managed world against every way of not being one", managedN, notManaged)
	}
}

// confirmRig is an initRun over the row's flags and terminal, with what is
// typed at the prompt; the spy says whether stdin was read at all.
func confirmRig(t *testing.T, row confirmRow, typed string) (*initRun, *readSpy) {
	t.Helper()
	spy := &readSpy{Reader: strings.NewReader(typed + "\n")}
	cmd := &cobra.Command{}
	cmd.SetIn(spy)
	o := &initOptions{yes: row.yes, planOnly: row.planOnly, nonInteractive: row.nonInteractive, asJSON: row.json}
	return &initRun{cmd: cmd, o: o, w: io.Discard, in: bufio.NewReader(spy), projectName: initProjectName, gcpProject: initProject}, spy
}

// withTerminal makes stdin a terminal or not for the test.
func withTerminal(t *testing.T, on bool) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return on }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// managedDecision is the decision an applying step of class makes in a
// project whose read gave rd, under the run r: the confirmation is replaced
// only when mode says managed, and otherwise it is the program's own
// confirmation path — initRun.ask for YesOrTyped, initRun.askTyped (which
// reads initRun.conditions and canConfirmTyped) for Typed.
func managedDecision(mode func(bool, string, error) bool, rd managedRead, r *initRun, class initflow.ConfirmClass) (bool, error) {
	if mode(rd.ok, rd.reason, rd.err) {
		return true, nil
	}
	if class == initflow.Typed {
		ok, reachable, err := r.askTyped("an applying step")
		return ok && reachable && err == nil, err
	}
	return r.ask("an applying step")
}

// managedViolations runs the decision over the whole product — every read,
// every agent environment state, every row of the table, both classes, the
// right name typed and a wrong one — and returns every combination in which
// a project the oracle says is NOT managed does other than the hand-written
// table says (and, in a coding agent's session, anything but refuse).
func managedViolations(t *testing.T, mode func(bool, string, error) bool, reads []managedRead) (bad []string, n int) {
	t.Helper()
	old := canConfirmTyped
	canConfirmTyped = initflow.CanConfirm
	t.Cleanup(func() { canConfirmTyped = old })
	for _, env := range agentEnvStates() {
		env.apply(t)
		for _, row := range confirmTable {
			withTerminal(t, row.terminal)
			for _, rd := range reads {
				if rd.managed {
					continue // the managed direction is Task 3.1's
				}
				for _, class := range confirmClasses {
					for _, right := range []bool{true, false} {
						n++
						typed := "not-the-project"
						if right {
							typed = initProjectName
						}
						want := false
						if env.agent == "" {
							switch {
							case class.class == initflow.YesOrTyped && right:
								want = row.ordRight
							case class.class == initflow.YesOrTyped:
								want = row.ordWrong
							case right:
								want = row.typRight
							default:
								want = row.typWrong
							}
						}
						r, spy := confirmRig(t, row, typed)
						got, err := managedDecision(mode, rd, r, class.class)
						where := fmt.Sprintf("%s, %s, %s, typed right=%v, %s", class.name, row, env.name, right, rd.state)
						if got != want {
							bad = append(bad, fmt.Sprintf("%s: proceeds=%v, want %v (err %v)", where, got, want, err))
						}
						if env.agent != "" {
							if spy.read {
								bad = append(bad, where+": stdin was read in a coding agent's session")
							}
							var ae *initflow.AgentError
							if class.class == initflow.YesOrTyped && (!errors.As(err, &ae) || ae.Marker != env.agent) {
								bad = append(bad, fmt.Sprintf("%s: ask's refusal is %v, want the agent refusal naming %s", where, err, env.agent))
							}
						}
					}
				}
			}
		}
	}
	return bad, n
}

// The property itself, over the whole product and the program's real paths.
func TestManagedNonManagedProjectNeverSkipsAConfirmation(t *testing.T) {
	reads := managedReads(t)
	if len(confirmTable) != 1<<5 {
		t.Fatalf("the table has %d rows, want 32", len(confirmTable))
	}
	bad, n := managedViolations(t, managedMode, reads)
	// 13 environment states x 32 rows x 14 non-managed reads x 2 classes x 2 answers.
	if want := len(agentEnvStates()) * len(confirmTable) * (len(reads) - 1) * len(confirmClasses) * 2; n != want || n == 0 {
		t.Fatalf("the property ran %d combinations, want %d", n, want)
	}
	if len(bad) != 0 {
		t.Errorf("a project that is not managed changed its decision in %d of %d combination(s):\n%s", len(bad), n, firstFew(bad))
	}
}

// Each predicate below is a mutation of managedMode that would skip the
// confirmation of a project nothing recorded, and each must be reported.
func TestManagedPropertyCatchesAWrongPredicate(t *testing.T) {
	reads := managedReads(t)
	for _, tc := range []struct {
		name string
		mode func(ok bool, reason string, err error) bool
	}{
		{"an answer with no error is managed", func(ok bool, reason string, err error) bool { return err == nil }},
		{"every answer is managed", func(ok bool, reason string, err error) bool { return true }},
		{"the read's first result alone is managed", func(ok bool, reason string, err error) bool { return ok }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if bad, _ := managedViolations(t, tc.mode, reads); len(bad) == 0 {
				t.Fatal("the property accepted a predicate that reads a project nothing recorded as managed")
			}
		})
	}
}

// The rule and the run's conditions, against the same hand-written table:
// initflow.CanConfirm for each class, CanConfirmRun, and canConfirmTyped (the
// seam askTyped asks) say what the table's "typed right" columns say for
// conditions without --plan-only (plan-only is folded into Yes by
// initRun.conditions, checked here too), and nothing under any marker.
func TestManagedConfirmRuleMatchesTheTable(t *testing.T) {
	for _, env := range agentEnvStates() {
		env.apply(t)
		for _, row := range confirmTable {
			withTerminal(t, row.terminal)
			r, _ := confirmRig(t, row, "")
			want := initflow.Conditions{Yes: row.condYes, NonInteractive: row.nonInteractive, JSON: row.json, Terminal: row.terminal, Agent: env.agent}
			if got := r.conditions(); got != want {
				t.Errorf("%s, %s: initRun.conditions() = %+v, want %+v", row, env.name, got, want)
			}
			if row.planOnly {
				continue
			}
			for _, rule := range []struct {
				name string
				fn   func(initflow.ConfirmClass, initflow.Conditions) bool
			}{{"initflow.CanConfirm", initflow.CanConfirm}, {"canConfirmTyped", canConfirmTyped}} {
				ord, typ := row.ordRight, row.typRight
				if env.agent != "" {
					ord, typ = false, false
				}
				if got := rule.fn(initflow.YesOrTyped, want); got != ord {
					t.Errorf("%s(YesOrTyped) under %s, %s = %v, want %v", rule.name, row, env.name, got, ord)
				}
				if got := rule.fn(initflow.Typed, want); got != typ {
					t.Errorf("%s(Typed) under %s, %s = %v, want %v", rule.name, row, env.name, got, typ)
				}
			}
			typ := row.typRight && env.agent == ""
			if got := initflow.CanConfirmRun(want); got != typ {
				t.Errorf("initflow.CanConfirmRun under %s, %s = %v, want %v", row, env.name, got, typ)
			}
		}
	}
}

// inlineAgentSite is one of init's inline agent-marker decisions, run under
// conditions in which everything else allows it (a terminal everywhere, no
// flag but what the site needs, the project's name typed): allowed reports
// whether the site let the step through.
type inlineAgentSite struct {
	where   string
	allowed func(t *testing.T, r *initRun, e *initEngine, marker string) bool
}

// inlineAgentSites lists every inline agentMarker/agentEnv decision on
// init's paths (`grep -n 'agentMarker(\|agentEnv(' internal/cli/init*.go`),
// beyond ask and conditions, which the property drives.
var inlineAgentSites = []inlineAgentSite{
	{"init_review.go runCovers (the run's one confirmation)", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		r.review = &runReview{state: reviewTaken}
		ok, err := r.runCovers()
		return ok && err == nil
	}},
	{"init_review.go askOrdinary", func(t *testing.T, r *initRun, e *initEngine, marker string) bool {
		r.review = &runReview{state: reviewTaken}
		ok, err := r.askOrdinary("an ordinary step", "")
		var ae *initflow.AgentError
		if marker != "" && (!errors.As(err, &ae) || ae.Marker != marker) {
			t.Errorf("askOrdinary's refusal under %s is %v, want the agent refusal", marker, err)
		}
		return ok && err == nil
	}},
	{"init_project.go canType", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		return (&projectStage{e: e}).canType()
	}},
	{"initsecrets.go canPrompt", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		return (&secretsStage{e: e}).canPrompt()
	}},
	{"init_repo_gate.go canConfirmRepo", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		return e.canConfirmRepo()
	}},
	{"init_repo_gate.go authState's --onboard-repo opt-in", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		r.o.onboardRepo = "acme/app"
		a, err := e.authState(originInfo{Repo: "acme/app", Host: "github.com"})
		return a == authOK && err == nil
	}},
	{"init_repo_gate.go onboardLeft (the flag offered as the route)", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		l := onboardLeft(originInfo{Repo: "acme/app", Host: "github.com"})
		return l.Kind == initflow.LeftCommand && strings.Contains(l.Text, "--onboard-repo")
	}},
	{"initsecrets.go Left (the wording of the secrets route)", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		l := (&secretsStage{e: e, tg: &secretTarget{repo: "acme/app", wanted: []string{"API_KEY"}}}).Left()
		return !strings.Contains(l.Text, "not through a coding agent")
	}},
	{"init_retire.go retireConfirm", func(t *testing.T, r *initRun, e *initEngine, marker string) bool {
		err := r.retireConfirm(nil, infra.RepoSpec{}, "nothing was retired")
		var ae *initflow.AgentError
		if marker != "" && (!errors.As(err, &ae) || ae.Marker != marker) {
			t.Errorf("retireConfirm's refusal under %s is %v, want the agent refusal", marker, err)
		}
		return err == nil
	}},
	{"init.go checkOnboardRepo", func(t *testing.T, r *initRun, e *initEngine, _ string) bool {
		r.o.onboardRepo = "acme/app"
		return r.o.checkOnboardRepo() == nil
	}},
}

// Every inline decision refuses under each marker, and only under a marker:
// with none, or with one present but empty, the same site lets the step
// through, so the refusal is the marker's and the test is not vacuous.
func TestManagedEveryInlineAgentDecisionRefusesUnderEachMarker(t *testing.T) {
	withTerminal(t, true)
	oldW := writerIsTerminal
	writerIsTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { writerIsTerminal = oldW })
	old := canConfirmTyped
	canConfirmTyped = initflow.CanConfirm
	t.Cleanup(func() { canConfirmTyped = old })
	for _, site := range inlineAgentSites {
		for _, env := range agentEnvStates() {
			t.Run(site.where+"/"+env.name, func(t *testing.T) {
				env.apply(t)
				r, _ := confirmRig(t, confirmRow{terminal: true}, initProjectName)
				var out strings.Builder
				r.w = &out
				r.cmd.SetOut(&out)
				r.cmd.SetErr(&out)
				e := &initEngine{r: r, lc: &localcfg.Config{}}
				if got, want := site.allowed(t, r, e, env.agent), env.agent == ""; got != want {
					t.Errorf("allowed=%v, want %v", got, want)
				}
			})
		}
	}
}

// The oracle's markers are the program's: a marker dropped from agentMarkers
// (or one added without a case here) fails here rather than shrinking the
// product.
func TestManagedAgentMarkersAreTheExpectedSix(t *testing.T) {
	if !slices.Equal(agentMarkers, expectedAgentMarkers) {
		t.Fatalf("agentMarkers is %v and the oracle is %v", agentMarkers, expectedAgentMarkers)
	}
	for _, k := range expectedAgentMarkers {
		if got := agentMarker(func(s string) string {
			if s == k {
				return "1"
			}
			return ""
		}); got != k {
			t.Errorf("agentMarker with only %s set = %q", k, got)
		}
	}
}

// initFlagCases is every flag of `fugaro init` and its relationship to the
// confirmations the property is about. A flag with no entry fails
// TestManagedEveryInitFlagHasACase: a new flag is a new condition until
// someone writes down what it does to a confirmation.
var initFlagCases = map[string]string{
	// The table's dimensions (confirmTable).
	"yes":             "dimension: confirms YesOrTyped (ask), never Typed; refused under a marker",
	"non-interactive": "dimension: never prompts, so nothing typed confirms",
	"json":            "dimension: never takes a Typed confirmation",
	"plan-only":       "dimension: folded into Yes=false by initRun.conditions; never takes --yes",
	// Flags whose steps confirm through the paths the property drives.
	"create-project":   "its creation confirms through the Typed class (askTyped/canType, both covered)",
	"link-billing":     "the billing link confirms through the Typed class (askTyped/canType, both covered)",
	"replace-image":    "names the intent only; replacing confirms through askTyped (covered)",
	"retire":           "both confirmations are askTyped; retireConfirm's agent refusal is an inline site (covered)",
	"repo":             "the repository's applies confirm through ask/askOrdinary (covered)",
	"firebase":         "its applies confirm through ask, its Firestore location through the Typed class (covered)",
	"forget":           "its state removals confirm through ask (covered)",
	"config-only":      "writes the local config; any confirmation is ask's (covered)",
	"onboard-repo":     "an opt-in in place of the typed owner/name: refused under a marker (checkOnboardRepo, authState: inline sites, covered); for a non-managed project without a marker it opts in without typing, which is NOT in the table",
	"anchor":           "OUTSIDE the property: edits only the checkout's fugaro.yaml and honours --yes under a marker by design (init --help)",
	"publish-config":   "OUTSIDE the property: refused under a marker in init.go before any write; no confirmation",
	"check-github-app": "OUTSIDE the property: a secret read, refused under a marker (githubapp.go); no confirmation",
	"allow-delete":     "OUTSIDE the property: feeds tf.Guard, not a confirmation (D74, Task 1.4, is the managed case)",
	"allow-job-delete": "OUTSIDE the property: changes what a --repo plan holds, not whether it is confirmed",
	"allow-fork":       "OUTSIDE the property: lets plugin wiring move a ref; the wiring's apply still confirms through ask",
	"print-vars":       "no cloud call and no confirmation (canAsk is false under it)",
	"no-build":         "only drops the first image build's offer (a Typed step); takes nothing away from a confirmation",
	// Values a confirmed step carries: they change what is confirmed, never whether.
	"alert-email": "value", "base": "value", "base-image": "value", "billing-account": "value", "budget": "value",
	"budget-admin": "value", "budget-currency": "value", "budget-mode": "value", "config": "value",
	"display-name": "value", "expect-digest": "value", "gcp-project": "value", "github-app-id": "value",
	"image-source": "value", "launcher": "value", "name": "value", "no-log-isolation": "value",
	"operator": "value", "parent": "value", "project": "value", "region": "value",
	"registry-cleanup": "value", "runs-bucket": "value", "scheduler-region": "value", "state-bucket": "value",
}

// Every flag `fugaro init` declares has an entry above, and every entry is a
// flag init declares: the enumeration is the command's own flag set, so a
// flag added without a case fails here.
func TestManagedEveryInitFlagHasACase(t *testing.T) {
	seen := map[string]bool{}
	newInitCmd().Flags().VisitAll(func(f *pflag.Flag) {
		seen[f.Name] = true
		if _, ok := initFlagCases[f.Name]; !ok {
			t.Errorf("fugaro init --%s has no case in initFlagCases: say what it does to a confirmation, and add it to the table if it is a condition", f.Name)
		}
	})
	for name := range initFlagCases {
		if !seen[name] {
			t.Errorf("initFlagCases names --%s, which fugaro init does not declare", name)
		}
	}
	for _, d := range []string{"yes", "non-interactive", "json", "plan-only"} {
		if !strings.HasPrefix(initFlagCases[d], "dimension:") {
			t.Errorf("--%s is one of the table's dimensions", d)
		}
	}
}

// FUTURE (documented, not yet a case): a config that claims a project is
// managed. Nothing reads such a claim today — the only authority is the cloud
// marker IsManaged reads — and when something does, it may only narrow:
// a claim without the marker must still confirm. This test is the place that
// case goes.
func TestManagedConfigClaimingManagedIsNotManaged(t *testing.T) {
	t.Skip("FUTURE: no config field claims managed yet; when one exists, a claim without the cloud marker must not skip any confirmation")
}

// conditionsString spells a run's conditions for a failure message.
func conditionsString(c initflow.Conditions) string {
	var b strings.Builder
	v := reflect.ValueOf(c)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%s=%v", typ.Field(i).Name, v.Field(i).Interface())
	}
	return b.String()
}

// firstFew is the head of a violation list.
func firstFew(bad []string) string {
	const most = 5
	if len(bad) > most {
		return strings.Join(bad[:most], "\n") + fmt.Sprintf("\n... and %d more", len(bad)-most)
	}
	return strings.Join(bad, "\n")
}

// initflow.Conditions holds exactly the fields initRun.conditions fills and
// the table sets: a new one is a condition the table has no column for.
func TestManagedConditionEnumerationIsComplete(t *testing.T) {
	typ := reflect.TypeOf(initflow.Conditions{})
	declared := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		declared = append(declared, typ.Field(i).Name)
	}
	if want := []string{"Yes", "NonInteractive", "JSON", "Terminal", "Agent"}; !slices.Equal(declared, want) {
		t.Fatalf("initflow.Conditions is %v and the table covers %v (%s)", declared, want, conditionsString(initflow.Conditions{}))
	}
}

// The confirmation classes against the declaration in
// internal/initflow/stage.go, in its iota order.
func TestManagedConfirmClassEnumerationIsComplete(t *testing.T) {
	declared := declaredConfirmClasses(t)
	got := make([]string, 0, len(confirmClasses))
	for _, c := range confirmClasses {
		got = append(got, c.name)
	}
	if !slices.Equal(declared, got) {
		t.Fatalf("initflow declares the confirmation classes %v and the product enumerates %v", declared, got)
	}
	for i, c := range confirmClasses {
		if int(c.class) != i {
			t.Errorf("the product's %s is value %d, want %d", c.name, int(c.class), i)
		}
	}
}
