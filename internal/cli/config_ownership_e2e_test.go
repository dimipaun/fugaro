package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// config_ownership_e2e_test.go is I1's integration half
// (docs/config-ownership/implementation-plan.md, cm-conformance). Two things
// are pinned here. The stub C1 left behind is gone and the seam holds N3's
// function, which is a fact about the whole binary and not about one command
// (TestNoStubSurvives). And the semantic commands of the plan leave this
// machine's config and the published node saying the same thing — after the
// command, after `fugaro project policy publish` and after `fugaro update`,
// with stdin closed at every one of them (TestConfigOwnershipEndToEnd).

// afterWriteStubFile is C1's stub, the file I1 deletes: an init assigning
// runAfterWrite the no-op that keeps every semantic command's write out of the
// published node. Its absence is what TestNoStubSurvives reads, so the name is
// a string the test holds rather than a path only a comment spells: a tree
// where the file came back under this name fails.
const afterWriteStubFile = "after_write_stub.go"

// afterWriteAssigns matches an assignment to the hook seam in a source file,
// and not the three other things a line can spell: the declaration
// (after_write.go's `var runAfterWrite afterWriteFunc`), a comparison (a test's
// `runAfterWrite == nil`), and a call (`runAfterWrite(ctx, cmd, change)`).
// Comments are skipped by the caller, so a sentence about the seam is not read
// as one.
var afterWriteAssigns = regexp.MustCompile(`\brunAfterWrite\s*=\s*[^=]`)

// TestNoStubSurvives is I1's red step: it fails while after_write_stub.go
// exists, because that file's init assigns the no-op and every semantic
// command's write would go unpublished with nothing reporting it.
//
// What the test reads is deliberately more than the file's absence. The seam
// is one variable, so what EVERY semantic command reaches is decided by the
// assignments to it rather than by which commands call it — a second file
// assigning it (a stub, a test's own no-op in a non-test file, a later
// "simplification") would take the hook away from all of them at once, which
// is exactly how C1's stub was built and exactly why deleting it is not
// enough on its own. So three facts are read together: the file is gone, the
// variable holds N3's function by pointer, and exactly one non-test source
// file assigns it, that being write_publish.go's init.
func TestNoStubSurvives(t *testing.T) {
	if _, err := os.Stat(afterWriteStubFile); err == nil {
		t.Fatalf("%s is in internal/cli: its init assigns runAfterWrite the no-op, so every semantic command's write is saved and never published — delete the file (I1)", afterWriteStubFile)
	} else if !os.IsNotExist(err) {
		t.Fatalf("reading %s: %v", afterWriteStubFile, err)
	}
	if runAfterWrite == nil {
		t.Fatal("runAfterWrite is nil: a semantic command's write would publish nothing and report nothing")
	}
	if got, want := reflect.ValueOf(runAfterWrite).Pointer(), reflect.ValueOf(publishAfterWrite).Pointer(); got != want {
		t.Fatalf("runAfterWrite holds %#x and publishAfterWrite is %#x: the hook the seam holds is not write_publish.go's", got, want)
	}
	if got, want := afterWriteAssigningFiles(t), []string{"write_publish.go"}; !slices.Equal(got, want) {
		t.Errorf("the non-test files assigning runAfterWrite are %v, want %v: one assignment is what makes the hook N3's for every command that calls it, and a second is a file silently taking it away", got, want)
	}
}

// afterWriteAssigningFiles names the package's non-test source files that
// assign runAfterWrite, sorted. The package's own directory is the working
// directory `go test` runs in (closedStdinSourceDir's rule), so the scan is of
// the files the binary is built from.
func afterWriteAssigningFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if afterWriteAssigns.MatchString(trimmed) {
				out = append(out, name)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestConfigOwnershipEndToEnd is the fact the config-ownership contract exists
// for, read off the whole binary rather than one command at a time
// (docs/contracts/config-ownership.md §1, D2609–D2615): a semantic command's
// write leaves this machine's config and the published node saying the SAME
// thing, and it does so at once — the owner runs no second command to make
// their edit live.
//
// Four blocks are walked, one per family of the plan, and each of them is a
// carrier whose store is the local config while what serves it is the node:
// pools (W1–W3), crafts (A2/R1), the credentials cascade (K1/N1) and binaries
// (K2). Every one of them is the case one store cannot state, which is why the
// hook runs the policy stage rather than the store's own (contract §3).
//
// Three moments are read, because three doors can leave the node behind: the
// command's own hook, the explicit `fugaro project policy publish`, and `fugaro
// update` — the whole upgrade, which must not undo what a command published
// (the HARD RULE's own "it changes nothing a run holds"). After every step of
// the walk and at each of the two later doors, every block this machine's
// config states is compared member for member with the node's copy of it.
//
// Every command the plan adds is run with its stdin a closed pipe under
// closedStdinDeadline and is held to the tree's own policy: it returns, it
// exits zero, and it leaves no question for a person (docs/no-prompt-policy;
// ownershipQuestionsRead, which reads the run's own confirmations and requires
// the run to have answered each of them itself). The point of reading it here
// and not only in noprompt_harness_test.go is that this walk drives the
// commands at the installation an update converges in, with the hook's own
// in-process run of the stages behind each write — the one path where a stage
// could reach a confirmation the leaf-level harness never gets to.
func TestConfigOwnershipEndToEnd(t *testing.T) {
	w := newOwnershipWorld(t)
	// The node is published from this config before the walk begins, so that
	// every block that arrives after it is one a command of the walk put there
	// rather than one the initial publish had already carried.
	w.run("project", "policy", "publish")
	w.prePublish(t)
	for _, step := range ownershipSteps {
		out := w.run(step.args...)
		ownershipQuestionsRead(t, step.what, out)
		lc := w.localConfig()
		if !step.wrote(lc) {
			t.Fatalf("%s ran and this machine's config does not hold its write: the walk asserts what the command LEFT, so a run that exited zero without writing says nothing about the node", step.what)
		}
		w.agree(step.what)
	}
	out := w.run("project", "policy", "publish")
	ownershipQuestionsRead(t, "fugaro project policy publish", out)
	w.agree("fugaro project policy publish")
	// `fugaro update` is not one of the commands this plan adds: it is the third
	// door the node is read at, the whole upgrade that must not undo what a
	// command published. It is run the way this package's own update tests run
	// it (fakeTerminal): its installation step rewrites this machine's config
	// and its layer file, and a person's being at the terminal is what answers
	// that — nothing is typed at it and no value is read from it, which is the
	// one thing a closed pipe cannot express for this command.
	fakeTerminal(t)
	w.run("update")
	w.agree("fugaro update")
}

// ownershipOwner is the owner the pool is written with, and it is not
// decoration: a published pool with no owner is one this Fugaro refuses to read
// back, so the publish leaves it out of the node and names it (D2608, the row
// `accounts pool add` writes today). A pool written without one would make the
// agreement assertion below impossible to satisfy rather than fail — the walk
// would be measuring the pool rule and not the hook.
const ownershipOwner = "someone@example.com"

// ownershipWorld is the installation the walk runs in: one local config the
// four commands write and one published node they converge, both this test's
// own. The base is updateBudgetRig because `fugaro update` is one of the three
// moments the node is read at, and that rig is the world an update converges in
// (the fakes its stages reach for, a plan that changes nothing); the node is the
// same rig's Realtime Database fake, at the path the publisher writes.
type ownershipWorld struct {
	t  *testing.T
	db *gcpfake.RTDB
}

func newOwnershipWorld(t *testing.T) *ownershipWorld {
	t.Helper()
	// The isolation closedStdinWorld's runs have, for the same reasons: no
	// outbound request, no update check, no state directory of this
	// repository's own. And no coding agent's session: the four writes are
	// narrowing edits a session is admitted to, and this walk is a person at
	// their own machine — the state the hook's own gate read is written for.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("FUGARO_NO_UPDATE_CHECK", "1")
	t.Setenv("FUGARO_STATE_DIR", "")
	clearAgentMarkers(t)
	terminal(t, false)
	r, db := updateBudgetRig(t)
	// D2217: every command of the walk writes the project's own state — the
	// published node, the pool, the craft — so the installation they
	// run at is a managed one, with both halves of the read the fence makes:
	// the creation fact in the project's runs bucket and the label on the
	// project itself. A walk run at an installation carrying neither would
	// measure the fence rather than the hook: the first publish is refused
	// before it reads the node, which is the refusal managed_require_test.go
	// pins for exactly this command.
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{gcp.LabelManaged: gcp.ManagedValue})
	r.gcs.Put(initRunsBucket, managed.MarkerObject, managedAddMarker())
	labelled(r.crm, initProject)
	// A managed project's steps record the account they run as (D1014), so the
	// walk needs the identity the other managed fixtures stub.
	actingAccount(t)
	// The rules are deployed: a publish refuses under rules that carry no
	// policy marker, which is the state before an installation has updated
	// once. The project's own marker is the rig's already (budget.PathProject).
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	// The credentials the pool and the craft name must be defined, and the
	// craft must exist before the write that takes it away: a pool member no
	// credential of the document defines and a craft the file does not state
	// are each command's own refusal BEFORE its write, and a walk that stopped
	// at one of them would prove nothing about the hook.
	r.appendConfig(t, "credentials:\n  - name: team-a\n    type: claude-oauth\n  - name: team-b\n    type: claude-oauth\ncrafts:\n  coder:\n    access: team-a\n")
	return &ownershipWorld{t: t, db: db}
}

// ownershipSteps is the walk, one step per family, in the order they are run:
// the words, and the reading of this machine's config that says the step's own
// write landed. The reading is what makes the agreement that follows about the
// HOOK: `wrote` proves the config moved, and `agree` proves the node moved with
// it.
var ownershipSteps = []struct {
	what  string
	args  []string
	wrote func(*localcfg.Config) bool
}{
	{
		what: "fugaro accounts pool add",
		args: []string{"accounts", "pool", "add", "claude-pool", "team-a", "team-b", "--owner", ownershipOwner},
		wrote: func(lc *localcfg.Config) bool {
			return len(lc.Pools) == 1 && lc.Pools[0].Name == "claude-pool" &&
				lc.Pools[0].Owner == ownershipOwner && slices.Equal(lc.Pools[0].Members, []string{"team-a", "team-b"})
		},
	},
	{
		// MG4 retired the write that moved a craft's access, so the craft step
		// of the walk is the other command of that family: the removal takes the
		// whole craft out of this machine's config and owes the publish that
		// takes it off the node, which is the same agreement read one block
		// emptier. A craft write that no longer exists would leave the crafts
		// carrier unreachable, and the walk would be about three blocks rather
		// than four.
		what: "fugaro accounts craft rm",
		args: []string{"accounts", "craft", "rm", "coder"},
		wrote: func(lc *localcfg.Config) bool {
			return lc.Crafts.Of(config.CraftCoder) == config.Craft{}
		},
	},
	{
		what: "fugaro accounts cascade set",
		args: []string{"accounts", "cascade", "set", "--backoff-cap", "1h"},
		wrote: func(lc *localcfg.Config) bool {
			return lc.CredentialCascade != nil && lc.CredentialCascade.Backoff != nil && lc.CredentialCascade.Backoff.Cap == "1h"
		},
	},
	{
		what: "fugaro project binaries allow",
		args: []string{"project", "binaries", "allow", "brand/*.png"},
		wrote: func(lc *localcfg.Config) bool {
			return lc.Binaries != nil && slices.Contains(lc.Binaries.Allow, "brand/*.png")
		},
	},
}

// ownershipCarriers is the four blocks, as the pair of values that must be
// equal: this machine's config's carrier, and the node's block the same values
// are served out of. `stated` is what keeps the comparison honest — a block the
// config is silent about is not a disagreement, it is the node's own, because
// the publish merges those blocks rather than replacing them (D844's rule, the
// clone_urls line of buildProjectPolicy) — and prePublish reads the node the
// initial publish left, so a comparison made here is never of two absences.
//
// Each pair is the two spellings of one block and not two derivations of it.
// The binaries row is the one subtrahend: the node always carries the cap, the
// config states it only when the operator wrote one, and BinariesEffective is
// the rule that says what the config's block therefore means (the row's own
// keep rule, init_policy_publish.go). The other three are compared as the
// structs they are.
var ownershipCarriers = []struct {
	name   string
	stated func(*localcfg.Config) bool
	sides  func(t *testing.T, lc *localcfg.Config, p *projectpolicy.Policy) (any, any)
}{
	{
		name:   "pools",
		stated: func(lc *localcfg.Config) bool { return len(lc.Pools) > 0 },
		sides: func(t *testing.T, lc *localcfg.Config, p *projectpolicy.Policy) (any, any) {
			if p.CraftPools == nil {
				t.Fatalf("the node carries no craft_pools block although this machine's config states %s", ownershipJSON(lc.Pools))
			}
			return lc.Pools, p.CraftPools
		},
	},
	{
		name:   "crafts",
		stated: func(lc *localcfg.Config) bool { return lc.Crafts != (config.Crafts{}) },
		sides: func(t *testing.T, lc *localcfg.Config, p *projectpolicy.Policy) (any, any) {
			if p.Crafts == nil {
				t.Fatalf("the node carries no crafts block although this machine's config states %s", ownershipJSON(lc.Crafts))
			}
			return lc.Crafts, *p.Crafts
		},
	},
	{
		name:   "the credentials cascade",
		stated: func(lc *localcfg.Config) bool { return lc.CredentialCascade != nil },
		sides: func(t *testing.T, lc *localcfg.Config, p *projectpolicy.Policy) (any, any) {
			if p.Credentials == nil {
				t.Fatalf("the node carries no credentials block although this machine's config states %s", ownershipJSON(*lc.CredentialCascade))
			}
			return *lc.CredentialCascade, *p.Credentials
		},
	},
	{
		name:   "binaries",
		stated: func(lc *localcfg.Config) bool { return lc.Binaries != nil },
		sides: func(t *testing.T, lc *localcfg.Config, p *projectpolicy.Policy) (any, any) {
			if p.Binaries == nil {
				t.Fatalf("the node carries no binaries block although this machine's config states %s", ownershipJSON(lc.Binaries))
			}
			return lc.BinariesEffective(p.Binaries), p.Binaries
		},
	},
}

// agree asserts the fact the whole task exists for: after step, every block
// this machine's config states is the block the published node carries.
func (w *ownershipWorld) agree(step string) {
	w.t.Helper()
	lc, p := w.localConfig(), w.node()
	for _, c := range ownershipCarriers {
		if !c.stated(lc) {
			continue
		}
		want, got := c.sides(w.t, lc, p)
		if !reflect.DeepEqual(want, got) {
			w.t.Errorf("after %s, this machine's config and the published node disagree about %s: the config says %s and the node carries %s. A write that reaches one of the two and not the other is the second command the HARD RULE forbids, and the after-write hook exists to run it (I1, D2609–D2615)", step, c.name, ownershipJSON(want), ownershipJSON(got))
		}
	}
}

// prePublish reads the node the initial publish left, which is what makes the
// walk's comparisons changes the commands made rather than a node that was
// already carrying them: three of the four blocks are absent from it, and the
// fourth — the crafts, which this machine's config seeds so that `accounts
// craft rm` has a craft to take away — carries the seed's own value, and the
// walk's own step is what takes it off.
//
// It is a reading of the node block by block and not "the node is empty": the
// publish writes the whole config, so a block the seed states IS published, and
// a walk that demanded an empty node would be asserting something about the
// seed rather than about the commands.
func (w *ownershipWorld) prePublish(t *testing.T) {
	t.Helper()
	p := w.node()
	for _, c := range []struct {
		name string
		held bool
	}{
		{"craft_pools", p.CraftPools != nil},
		{"credentials", p.Credentials != nil},
		{"binaries", p.Binaries != nil},
	} {
		if c.held {
			t.Errorf("the node carries a %s block after the initial publish, so the agreement the walk reads for it would be about that publish rather than about the command the walk is holding", c.name)
		}
	}
	if p.Crafts == nil {
		t.Fatalf("the node carries no crafts block after the initial publish, although this machine's config seeds one: `accounts craft set` has no craft to move and the walk would fail on its own fixture")
	}
	if got := p.Crafts.Of(config.CraftCoder).Access; got != "team-a" {
		t.Errorf("the node's coder craft reads %q after the initial publish, want the seed's team-a: the step that takes the craft away would otherwise be agreeing with a value it did not take off", got)
	}
}

// ownershipQuestionsRead reads the confirmations a run printed: every banner it
// shows must be one the run answered for ITSELF ("confirmed by this run's own
// answer", initRun.askBanner's --yes arm), which is what says no question was
// left standing for a person. It is deliberately not assertNoPrompt, which
// noprompt_harness_test.go applies leaf by leaf: a semantic command's write runs
// the stages in process with the command's own answer already given
// (write_publish.go builds its run with yes), and a stage prints its banner
// before answering it — the harness's "no banner at all" reading would call that
// a prompt, while a run whose every banner is answered by its own construction
// is a run that waited on nothing. What the run cannot do with its stdin closed
// is leave a banner unanswered, and that is exactly what this reads.
func ownershipQuestionsRead(t *testing.T, what, out string) {
	t.Helper()
	banners := strings.Count(out, "⚠ CONFIRM")
	if answered := strings.Count(out, "confirmed by this run's own answer"); banners > answered {
		t.Errorf("%s printed %d confirmation(s) and answered %d of them itself, with its stdin a closed pipe: a question left for a person is a question nobody can answer, and the run would be waiting on it (D1540):\n%s", what, banners, answered, out)
	}
}

// run runs one command of the walk with its stdin a closed pipe: it must return
// before closedStdinDeadline and exit zero. What it printed is returned for the
// caller's own reading — the question check is the caller's, because the one
// command of the walk that is not this plan's is driven at a terminal.
func (w *ownershipWorld) run(args ...string) string {
	t := w.t
	t.Helper()
	r, end, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The write end is closed before the run, so the pipe is at EOF: this is
	// the closed stdin the policy names, and it is an *os.File, which is what
	// makes stdinIsTerminal answer false for the unpatched reason too.
	end.Close()
	defer r.Close()
	out := &syncBuf{}
	root := NewRootCmd()
	root.SetIn(r)
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(NormalizeArgs(args))
	done := make(chan error, 1)
	go func() { done <- root.Execute() }()
	select {
	case err := <-done:
		got := out.String()
		if err != nil {
			t.Fatalf("fugaro %s exited %v with its stdin closed: the walk is about what the command LEFT behind, so a run that stops short says nothing about it:\n%s", strings.Join(args, " "), err, got)
		}
		return got
	case <-time.After(closedStdinDeadline):
		// The output is read while the run is still writing to it, which is
		// what syncBuf is for.
		t.Fatalf("fugaro %s did not return within %s with its stdin closed: a read that waits for a person is a wait nobody can end (it printed %q)", strings.Join(args, " "), closedStdinDeadline, out.String())
		return ""
	}
}

// localConfig is this machine's config as the walk left it, read the way every
// reader reads it: the file FUGARO_CONFIG names, which is the installation the
// commands were run in.
func (w *ownershipWorld) localConfig() *localcfg.Config {
	w.t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		w.t.Fatalf("reading this machine's config after the walk: %v", err)
	}
	return lc
}

// node is the published policy as the database holds it, decoded by the
// reader the publisher's own output is read by (publishedPolicy does the same
// for its fixture).
func (w *ownershipWorld) node() *projectpolicy.Policy {
	w.t.Helper()
	v := w.db.Value(projectpolicy.PathShared())
	if v == nil {
		w.t.Fatalf("no node at %s: nothing was published", projectpolicy.PathShared())
	}
	b, err := json.Marshal(v)
	if err != nil {
		w.t.Fatalf("reading the node at %s: %v", projectpolicy.PathShared(), err)
	}
	p, found, err := projectpolicy.Decode(b)
	if err != nil || !found {
		w.t.Fatalf("the node at %s does not decode (found=%v): %v", projectpolicy.PathShared(), found, err)
	}
	return p
}

// ownershipJSON is a value as the failure message prints it: JSON where it can
// be, and the value itself where it cannot, so that a disagreement names the
// two sides rather than two addresses.
func ownershipJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%+v", v)
	}
	return string(b)
}
