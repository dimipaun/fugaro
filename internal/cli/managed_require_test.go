package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/srctree"
)

// managed_require_test.go is D1541's fence's own tests: the classification of
// the tree (root.go), the mechanism that reads it (managed_require.go's
// requireManaged, called by openCloudFor and by the commands that open their
// clients another way), the refusal every fenced command prints, and the two
// directions a command may NOT take — a fenced command on an unmanaged project
// writing anything, and an exempt one being refused or read at all.
//
// The worlds are built here rather than taken from a command fixture, because
// what the fence reads is two marks and nothing else: internal/gcpfake's
// Resource Manager behind the resource_manager endpoint (the project, its
// number and its fugaro_managed label) and its GCS behind the storage one (the
// runs bucket, its label and the creation fact). A world that carries neither
// mark is the unmanaged project the fence exists for, and one whose project
// read fails is D2218's warning.

// managedFencedCommand returns path's command out of the real tree, failing
// when it does not resolve: a path that is not there would make the assertion
// below a sentence about nothing.
func managedFencedCommand(t *testing.T, path string) *cobra.Command {
	t.Helper()
	c, _, err := NewRootCmd().Find(strings.Fields(path))
	if err != nil || c == nil {
		t.Fatalf("%s does not resolve in the tree: %v", path, err)
	}
	return c
}

// fugaroAtItsOwnName is the name the refusal prints: the binary's own, rather
// than whatever path this test binary was started from, so the sentence the
// owner reads is what the test pins.
func fugaroAtItsOwnName(t *testing.T) {
	t.Helper()
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
}

// managedFenceRig is the fence's own world: an installation whose managed read has
// somewhere to go — both endpoints, under the no_auth a test configuration
// carries — and whose GCP project carries neither mark. It is the shape every
// fenced command is refused in, and the shape an exempt command must not even
// read.
type managedFenceRig struct {
	gcs *gcpfake.GCS
	crm *gcpfake.CRM
	lc  *localcfg.Config
}

func newManagedFenceRig(t *testing.T) *managedFenceRig {
	t.Helper()
	g, c := gcpfake.NewGCS(t), gcpfake.NewCRM(t)
	c.AddProject(initProject, initProjectNumber)
	return &managedFenceRig{gcs: g, crm: c, lc: &localcfg.Config{
		Name: initProjectName, GCPProject: initProject,
		Endpoints: localcfg.Endpoints{NoAuth: true, Storage: g.URL + "/storage/v1/", ResourceManager: c.URL + "/"},
	}}
}

// requests is everything the two fakes were asked, so a test can say "nothing
// was written" about an empty listing rather than about the call it remembered
// to look for.
func (r *managedFenceRig) requests() []gcpfake.Request {
	return append(append([]gcpfake.Request{}, r.gcs.Requests()...), r.crm.Requests()...)
}

// TestEveryWritingCommandIsClassified: root.go's two tables are the whole
// answer to "which commands refuse an unmanaged project", and this is what
// holds them complete. Every runnable command in the finished tree — the paid
// names' stubs and everything applyExtensions registered included, which is why
// the classification is applied to the finished tree and not to the core one —
// is either fenced or exempt, never both and never neither. A command that is
// neither is the hole D2217 is about: it writes to a project and nothing stops
// it, and it is found here rather than in a review.
func TestEveryWritingCommandIsClassified(t *testing.T) {
	root := NewRootCmd()
	seen := 0
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
			if !sub.Runnable() {
				continue
			}
			seen++
			fenced := sub.Annotations[managedRequiresAnnotation]
			exempt := sub.Annotations[managedExemptAnnotation]
			switch {
			case fenced != "" && exempt != "":
				t.Errorf("%s is classified twice: fenced (%s) and exempt (%s)", sub.CommandPath(), fenced, exempt)
			case fenced == "" && exempt == "":
				t.Errorf("%s is in neither table: add it to managedFencedCommands or to managedExemptCommands, with the one line that says why", sub.CommandPath())
			}
		}
	}
	walk(root)
	if seen == 0 {
		t.Fatal("the tree has no runnable command: this walk proves nothing")
	}
}

// TestEveryManagedCommandResolves: root.go applies the classification by cobra
// path and skips a path that does not resolve (annotateManagedPath's own
// return), so an entry that is misspelled, or that names a command the tree no
// longer has, would leave the command saying managedRequired's generic words
// instead of the clause the table wrote for it, and the table would claim a
// command is declared when it is not. Every entry resolves and carries its own
// text.
func TestEveryManagedCommandResolves(t *testing.T) {
	if len(managedFencedCommands) == 0 || len(managedExemptCommands) == 0 {
		t.Fatal("a table is empty: a command that should refuse an unmanaged project carries nothing")
	}
	for path, clause := range managedFencedCommands {
		if clause == "" {
			t.Errorf("%s is declared with no clause: its refusal would say nothing about what it declines", path)
		}
		c := managedFencedCommand(t, path)
		if got := c.Annotations[managedRequiresAnnotation]; got != clause {
			t.Errorf("%s carries the annotation %q, want the table's clause %q", path, got, clause)
		}
		if !c.Runnable() {
			t.Errorf("%s is not runnable: the fenced table names the commands a person runs", path)
		}
	}
	for path, why := range managedExemptCommands {
		if why == "" {
			t.Errorf("%s is exempt with no reason: the reason is what a reader checks the exemption against", path)
		}
		c := managedFencedCommand(t, path)
		if got := c.Annotations[managedExemptAnnotation]; got != why {
			t.Errorf("%s carries the exemption %q, want the table's reason %q", path, got, why)
		}
	}
}

// seamFencedCommands are the fenced commands that open their cloud clients
// through openCloud or openCloudFor — the two doors the root's pre-run makes
// the fence free at — and the door each one opens them at. It is a list of the
// call sites rather than of the fenced table: the point of the mechanism is
// that a command listed here is refused by construction and cannot forget the
// check, and a fenced command NOT listed here reaches its clients another way
// and asks for itself (requireManaged; T7b and T7c).
var seamFencedCommands = []struct{ path, door string }{
	{"accounts add", "accountsAddConverge"},
	{"accounts rm", "accountsRmDeleteSecret"},
	{"budget chain raise", "runChainRaise"},
	{"cancel", "newCancelCmd"},
	{"config publish", "newConfigPublishCmd"},
	{"image build", "openBuildCloud"},
	{"image prune", "runImagePrune"},
	{"orchestrator run", "runOrchestratorRun"},
	{"project repos add", "reposEnrolmentStages"},
	{"recipes publish", "newRecipesPublishCmd"},
	{"review", "runReviewLaunch"},
	{"run", "runRun"},
	{"secrets rm", "secretsRm"},
	{"secrets set", "secretsSet"},
}

// TestFencedCommandRefusesUnmanaged: a fenced command, run against a project
// that is not managed, is refused — through the seam it shares with every other
// command that opens its clients there, and, for `fugaro apply`, through the
// call its own run makes (it opens no client of its own: it goes straight to
// the init engine).
//
// The refusal is one sentence for all of them, and the parts of it that matter
// are all here: what the command declines (its own clause out of the table),
// which project the read was about and what the read found, and the one command
// that flags a project managed — spelled plain, with nothing after it and no
// prompt anywhere.
func TestFencedCommandRefusesUnmanaged(t *testing.T) {
	noAgentSession(t)
	noRunSession(t)
	fugaroAtItsOwnName(t)
	r := newManagedFenceRig(t)
	for _, tc := range seamFencedCommands {
		t.Run(tc.path, func(t *testing.T) {
			cmd := managedFencedCommand(t, tc.path)
			env, err := openCloudFor(withManagedCommand(context.Background(), cmd), r.lc)
			if env != nil {
				t.Fatalf("%s opened its cloud clients before the fence answered", tc.path)
			}
			checkFenceRefusal(t, err, tc.path)
			for _, req := range r.requests() {
				if req.Method != http.MethodGet {
					t.Errorf("%s wrote through a seam the refusal never reached: %s %s", tc.path, req.Method, req.Path)
				}
			}
		})
	}
	// The rest of the fenced commands: the ones that open the project's own
	// state through a seam openCloudFor does not cover — the budget database
	// (budgetDBFor), the reaper's own project selection, and the project's
	// issue store. The arm above calls openCloudFor itself, which is exactly
	// why it cannot see these: it would answer a question the command never
	// asks. So they are RUN here, against a world whose fakes record every
	// call, and the refusal is the command's own.
	t.Run("the seams outside openCloudFor", func(t *testing.T) {
		world := newInitRig(t)
		// The refusal is what these commands are run for, and the fence is the
		// one an unmanaged project gets: a world the marks answer managed for
		// would let each command past the fence and into a step no fake can
		// finish.
		world.unmarkedInstallation(t)
		buildBudgetForRig(t, world)
		t.Chdir(t.TempDir())
		patch := patchFile(t, patchDoc(t, 0, orchestrator.Op{Op: orchestrator.OpFactSet,
			Fact: &orchestrator.FactOp{Key: "branch", Value: "main"}}))
		for _, tc := range []struct {
			path string
			args []string
		}{
			{"backlog add", []string{"--title", "a title", "--repo", "acme/app"}},
			{"budget kill", []string{"--all"}},
			{"budget resume", []string{"--all"}},
			{"budget set", []string{"--global", "--per-run", "3"}},
			{"issue dismiss", []string{"1"}},
			{"issue sync", nil},
			{"orchestrator import", []string{"-f", patch}},
			{"orchestrator patch", []string{"-f", patch}},
			{"pair", nil},
			{"pair revoke", []string{"--uid", "m~a"}},
			{"project sync", nil},
			{"reaper", nil},
			{"runs digest", nil},
			{"user grant", []string{"user:me@example.com", "--role", "viewer", "--scope", "project"}},
			{"user revoke", []string{"user:me@example.com", "--role", "viewer", "--scope", "project"}},
		} {
			t.Run(tc.path, func(t *testing.T) {
				before := len(exemptWorldCalls(world))
				_, _, err := executeStdin(t, "", append(strings.Fields(tc.path), tc.args...)...)
				checkFenceRefusal(t, err, tc.path)
				for _, req := range exemptWorldCalls(world)[before:] {
					if req.Method != http.MethodGet {
						t.Errorf("%s wrote through a seam the fence did not reach: %s %s", tc.path, req.Method, req.Path)
					}
				}
			})
		}
	})

	// `fugaro apply`, end to end: its fence is called where its own run starts,
	// so the world is the one that command runs in — the installation, the
	// record and the steps, with no mark on the project — and the proof is the
	// whole invocation rather than a call to a helper.
	t.Run("apply", func(t *testing.T) {
		applyWorldUnmanaged(t)
		out, errOut, err := executeStdin(t, "", "apply")
		checkFenceRefusal(t, err, "apply")
		if said := out + errOut + err.Error(); !strings.Contains(said, managedFencedCommands["apply"]) {
			t.Errorf("the refusal does not say what apply declines:\n%s", said)
		}
	})
}

// TestFencedCommandWritesNothingOnUnmanaged: the refusal is the whole of the
// run. A fenced command that refused having already written something would be
// the worst of both — the owner is told nothing happened while the project
// holds a change — so the world is a write-recording fake and the assertion is
// that nothing but reads ever reached it, and that the engine behind `fugaro
// apply` (the fake terraform on the rig's PATH) was never started.
func TestFencedCommandWritesNothingOnUnmanaged(t *testing.T) {
	noAgentSession(t)
	noRunSession(t)
	fugaroAtItsOwnName(t)
	r, _ := applyWorldUnmanaged(t)
	if _, _, err := executeStdin(t, "", "apply"); err == nil {
		t.Fatal("fugaro apply ran on a project that is not managed")
	}
	for _, req := range r.gcs.Requests() {
		if req.Method != http.MethodGet {
			t.Errorf("the refused run wrote to GCS: %s %s", req.Method, req.Path)
		}
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("the refused run started the init engine: terraform was called %v", calls)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Errorf("the refused run created %d project(s)", n)
	}
}

// checkFenceRefusal is the one sentence, checked on the refusal of one command:
// the clause its own entry in the table holds, the project and the read's
// reason, and the command that flags a project managed — plain, so nothing is
// typed at a prompt and no flag stands in for the declaration.
func checkFenceRefusal(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s ran on a project that is not managed", path)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("%s exited %d, want %d", path, got, ExitUserError)
	}
	said := err.Error()
	for _, want := range []string{
		managedFencedCommands[path],
		initProject, "is not managed",
		"`" + managedFlagCommand() + "`, run by its owner at their own terminal",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, said)
		}
	}
	// The command is named with nothing after it: the argument is the
	// installation's own project, so a sentence that printed a project or a
	// flag after the name would be naming something the command does not read.
	if strings.Contains(said, managedFlagCommand()+"` ") {
		t.Errorf("the refusal spells something after the flag command:\n%s", said)
	}
	for _, never := range []string{"the argument is the declaration itself", "--budget", "Name a project"} {
		if strings.Contains(said, never) {
			t.Errorf("the refusal still carries %q:\n%s", never, said)
		}
	}
}

// exemptCommandsMayWrite are the exempt paths whose own exemption says the
// command writes: D2219's in-run commands (escalation's two readers included,
// because the ledger they read is the run's own record) and D3160's
// `fugaro user offboard`. The rule this arm of the fence is pinned to is zero
// writes *apart from what those name*, so a write is tolerated for these and
// for nothing else; a path that gains an exemption here without gaining a
// reason that says it writes is exactly the hole the run below refuses.
var exemptCommandsMayWrite = map[string]bool{
	"escalation ls": true, "escalation show": true,
	"followups": true, "mailbox": true, "tell": true,
	"user offboard": true,
}

// exemptRunArgs is the argument list the few paths are run with whose bare name
// never returns here: `watch` draws the project's live screen until it is
// stopped, and --once is the mode the command itself offers for one snapshot.
// Every other exempt path is run bare, and that is the point of running it: a
// command with no arguments refuses in one line, and the four services, `exec`
// and `verify` — each of which serves or runs a whole process when it has what
// it needs — refuse for want of their environment or their argument before they
// reach any of that.
var exemptRunArgs = map[string][]string{
	"watch": {"--once", "--plain"},
}

// exemptWorldCalls is every call the rig's fakes were asked, so "nothing was
// written" is said about a listing rather than about the one call this test
// remembered to look for. The budget database is in it: it is the project's own
// store, and a command that wrote a cap, a counter or a kill switch shows here.
func exemptWorldCalls(r *initRig) []gcpfake.Request {
	var all []gcpfake.Request
	for _, s := range []*gcpfake.Server{
		r.gcs.Server, r.ar.Server, r.iam.Server, r.crm.Server, r.run.Server,
		r.sm.Server, r.logs.Server, r.sched.Server, r.su.Server, r.budget.Server,
	} {
		all = append(all, s.Requests()...)
	}
	return all
}

// unreachableBucket takes the world's bucket away: a config that carries the
// runs bucket by name alone computes a gs:// URL, and a storage client that is
// refused the dial retries it until its context ends, so a command that reads
// the bucket would sit there instead of failing. The name is dropped (so the
// record bucket a build records into is empty too) and bucket_url names a
// scheme no driver opens, which fails at once. A write that would have gone to
// a bucket is refused rather than landing somewhere no assertion watches.
func unreachableBucket(t *testing.T, r *initRig) {
	t.Helper()
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	const named = "runs_bucket: fugaro-runs-proj-1234\n"
	if !strings.Contains(string(cfg), named) {
		t.Fatalf("the rig's config does not carry the runs bucket this rewrites:\n%s", cfg)
	}
	cfg = []byte(strings.Replace(string(cfg), named, "", 1) + "bucket_url: nosuch://runs\n")
	if err := os.WriteFile(r.cfg, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestExemptCommandsWriteNothing: the other half of the classification, and the
// half a fence with an over-broad reading gets wrong. It is answered three
// times, because the three answers fail for different reasons.
//
// The seam first: every exempt path is asked the same question the seams ask —
// with its own command in the context, against a world that would have answered
// "not managed" — and every one of them is answered without a single call to
// either fake. An exempt command is not read at all, so an installation whose
// marks are stale, unreadable or absent never pays for a command that was never
// fenced.
//
// Then the reads a person runs: a read-only command on a project that is not
// managed is admitted, which is A5's whole point — it is the command that tells
// an owner their project is not managed — and the assertion is about the
// command and not about the fence, because what the seam above would miss is a
// read that refuses an unmanaged project somewhere of its own.
//
// And finally every exempt path is RUN, which is what makes a misclassification
// fail here rather than in a review: the command is executed against a world
// that records every call its fakes answer, and anything but a read is a write
// the exemption did not have the right to make. The world is deliberately the
// installation's own — the rig's project carries no managed mark, no published
// policy and no seeded rules, and the project layer's bucket cannot be reached
// at all — so a command that would write has to get past every one of those
// refusals to reach a fake, and one that does is reported by name.
func TestExemptCommandsWriteNothing(t *testing.T) {
	noAgentSession(t)
	noRunSession(t)
	fugaroAtItsOwnName(t)
	r := newManagedFenceRig(t)
	for path := range managedExemptCommands {
		cmd := managedFencedCommand(t, path)
		if err := requireManaged(withManagedCommand(context.Background(), cmd), cmd, r.lc); err != nil {
			t.Errorf("%s is exempt and was refused: %v", path, err)
		}
	}
	if reqs := r.requests(); len(reqs) != 0 {
		t.Errorf("an exempt command was read or written through: %v", reqs)
	}

	world := newInitRig(t)
	buildBudgetForRig(t, world)
	// Every path is run from a directory of its own: several of them write the
	// checkout's files (a fugaro.yaml, a docs tree, an image map), and one of
	// those landing in this package's own directory would leave the repository
	// changed under the test that is running.
	t.Chdir(t.TempDir())
	// The project layer's bucket is opened for the reads that resolve it, and
	// the rig's runs bucket is a name, not a URL: reached for real it would go
	// to storage.googleapis.com, so it is a bucket that cannot be reached. A
	// write there is refused rather than silently landing in memory, and a read
	// takes the path an offline machine takes. That last part is what the world
	// has to be built for and not assumed: `project repos ls` and `image status`
	// open the bucket themselves (blobx.Open over lc.BucketURL and
	// lc.RecordBucketURL), a gs:// URL there builds a storage client whatever
	// the credentials, and a client that is refused the dial RETRIES it for as
	// long as the command's context lives — the read never takes the offline
	// path, it hangs on it. So the world carries no bucket a driver can open at
	// all (unreachableBucket below), and every bucket read fails at once.
	unreachableBucket(t, world)
	oldLayer := layerBucketOpener
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	t.Cleanup(func() { layerBucketOpener = oldLayer })
	// troubleshoot fetches the Fugaro source tree over the network
	// (srctree.Ensure), which no test may do: it is answered with the offline
	// path, which is the path a machine that cannot reach it takes anyway.
	oldSource := troubleshootSource
	troubleshootSource = func(context.Context, srctree.Options) (string, srctree.Record, error) {
		return "", srctree.Record{}, errors.New("a test reaches no network")
	}
	t.Cleanup(func() { troubleshootSource = oldSource })

	// And the same answer end to end, for the reads that need no project of
	// their own: a person checking their installation on a project that is not
	// managed is admitted, and the world's fakes answer nothing but reads.
	for _, args := range [][]string{{"version"}, {"budget", "prices"}, {"config", "example"}} {
		before := len(world.gcs.Requests())
		out, errOut, err := execute(t, args...)
		said := out + errOut
		if err != nil {
			said += err.Error()
		}
		if strings.Contains(said, "is not managed") {
			t.Errorf("fugaro %s was refused on a project that is not managed:\n%s", strings.Join(args, " "), said)
		}
		for _, req := range world.gcs.Requests()[before:] {
			if req.Method != http.MethodGet {
				t.Errorf("fugaro %s wrote to GCS before it was fenced: %s %s", strings.Join(args, " "), req.Method, req.Path)
			}
		}
	}

	for path := range managedExemptCommands {
		t.Run(path, func(t *testing.T) {
			before := len(exemptWorldCalls(world))
			args := append([]string{}, strings.Fields(path)...)
			// The error is not the assertion: most of these refuse in one line
			// for want of an argument, a flag or an installation, and a command
			// that failed wrote nothing either way.
			_, _, _ = executeStdin(t, "", append(args, exemptRunArgs[path]...)...)
			if exemptCommandsMayWrite[path] {
				return
			}
			for _, req := range exemptWorldCalls(world)[before:] {
				if req.Method != http.MethodGet {
					t.Errorf("fugaro %s is classified exempt but wrote to the project: %s %s", path, req.Method, req.Path)
				}
			}
		})
	}
}

// TestFenceReadFailsWarns: D2218, the direction the fence must not take. A read
// that FAILS is not an answer, so it neither refuses the command — an
// installation whose marks cannot be read is not thereby an unmanaged one — nor
// admits it to anything the managed path holds. The command goes on along the
// path an unmanaged project takes, and the owner reads one warning naming the
// project and the read that broke.
func TestFenceReadFailsWarns(t *testing.T) {
	noAgentSession(t)
	noRunSession(t)
	fugaroAtItsOwnName(t)
	r := newManagedFenceRig(t)
	// The project read is the fence's first call and the one the label comes
	// back on: an API that answers a 5xx is a read that failed, which is
	// neither the 403/404 the read answers "not managed" to nor a label.
	r.crm.FailGet(initProject, 500, "INTERNAL", "the project read failed")
	cmd := managedFencedCommand(t, "config publish")
	var stderr bytes.Buffer
	cmd.SetOut(&stderr)
	cmd.SetErr(&stderr)
	if err := requireManaged(withManagedCommand(context.Background(), cmd), cmd, r.lc); err != nil {
		t.Fatalf("a managed read that failed refused the command: %v", err)
	}
	said := stderr.String()
	for _, want := range []string{"could not read whether", initProject, "going on as a project that is not managed"} {
		if !strings.Contains(said, want) {
			t.Errorf("the warning does not say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, managedFlagCommand()) {
		t.Errorf("the warning names the flag command: a failed read is not a verdict to act on:\n%s", said)
	}
	// The read is the fence's, and it is not a permission: the same command
	// still writes nothing on the strength of it.
	for _, req := range r.requests() {
		if req.Method != http.MethodGet {
			t.Errorf("the warned run wrote through the fence: %s %s", req.Method, req.Path)
		}
	}
}
